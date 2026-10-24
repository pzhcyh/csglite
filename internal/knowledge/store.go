package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var validID = regexp.MustCompile(`^[a-z]+-[a-f0-9-]{36}$`)
var sourceCitation = regexp.MustCompile(`\[source:([^\]\s]+)\]`)

type Store struct {
	stewardRuns map[string]*stewardRun // protected by mu; never persisted
	root        string
	mu          sync.RWMutex
	commitState func(string, []byte) error
}

func Open(storageRoot string) (*Store, error) {
	if strings.TrimSpace(storageRoot) == "" {
		return nil, fmt.Errorf("knowledge storage root is required")
	}
	s := &Store{root: filepath.Join(storageRoot, "knowledge"), commitState: atomicWrite}
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return nil, fmt.Errorf("creating knowledge root: %w", err)
	}
	// Canonical JSON is committed atomically. Rebuild human-readable projections
	// after a crash interrupted a prior projection write, without changing raw.
	libs, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, lib := range libs {
		if err := s.project(&lib); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func newID(prefix string) string { return prefix + "-" + uuid.NewString() }
func now() string                { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Store) dir(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", problem(400, "invalid library id")
	}
	return filepath.Join(s.root, id), nil
}
func validateTitle(title string) error {
	if strings.TrimSpace(title) == "" || len(title) > 240 || !utf8.ValidString(title) || strings.ContainsAny(title, "\x00\r\n/\\[]") {
		return problem(400, "title must be 1–240 UTF-8 bytes without path separators or brackets")
	}
	return nil
}
func validateText(content string) error {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) || len(content) > MaxTextBytes {
		return problem(400, "content must be UTF-8 text, without NUL, at most 2 MiB")
	}
	return nil
}
func (s *Store) load(id string) (*Library, error) {
	dir, err := s.dir(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, ".knowledge", "state.json"))
	if os.IsNotExist(err) {
		return nil, problem(404, "library not found")
	}
	if err != nil {
		return nil, err
	}
	var lib Library
	if err = json.Unmarshal(data, &lib); err != nil {
		return nil, fmt.Errorf("reading knowledge state: %w", err)
	}
	if lib.ID != id {
		return nil, fmt.Errorf("knowledge state id mismatch")
	}
	return &lib, nil
}
func (s *Store) Get(id string) (*Library, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.load(id)
}
func (s *Store) List() ([]Library, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Library{}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validID.MatchString(entry.Name()) {
			continue
		}
		lib, err := s.load(entry.Name())
		if err != nil {
			if e, ok := err.(*Error); ok && e.Status == 404 {
				continue
			}
			return nil, err
		}
		out = append(out, *lib)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func addLog(lib *Library, message string) {
	lib.Logs = append(lib.Logs, Log{newID("log"), now(), message})
}
func (s *Store) Create(name, description string) (*Library, error) {
	if err := validateTitle(name); err != nil {
		return nil, err
	}
	if len(description) > 4000 || !utf8.ValidString(description) {
		return nil, problem(400, "description too long or invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lib := &Library{ID: newID("lib"), Name: strings.TrimSpace(name), Description: description, Config: Config{ModelSource: "local"}, Sources: []Source{}, Pages: []Page{}, Proposals: []Proposal{}, Logs: []Log{}}
	addLog(lib, "创建空白知识库")
	if err := s.persist(lib); err != nil {
		return nil, err
	}
	return lib, nil
}
func (s *Store) mutate(id string, fn func(*Library) error) (*Library, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lib, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if err = fn(lib); err != nil {
		return nil, err
	}
	if err = s.persist(lib); err != nil {
		return nil, err
	}
	return lib, nil
}
func activeSource(lib *Library, id string) *Source {
	for i := range lib.Sources {
		if lib.Sources[i].ID == id && lib.Sources[i].Status == "active" {
			return &lib.Sources[i]
		}
	}
	return nil
}
func findPage(lib *Library, id string) *Page {
	for i := range lib.Pages {
		if lib.Pages[i].ID == id {
			return &lib.Pages[i]
		}
	}
	return nil
}
func validateRefs(lib *Library, content string, ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return problem(400, "duplicate source reference")
		}
		if activeSource(lib, id) == nil {
			return problem(409, "source is missing, withdrawn, or belongs to another library")
		}
		seen[id] = true
	}
	var citationErr error
	mapMarkdownText(content, func(prose string) string {
		for _, m := range sourceCitation.FindAllStringSubmatch(prose, -1) {
			if !seen[m[1]] {
				citationErr = problem(400, "citation must reference an active source included in sourceIds")
			}
		}
		return prose
	})
	return citationErr
}
func titleAvailable(lib *Library, title, exceptID string) bool {
	for _, p := range lib.Pages {
		if p.ID != exceptID && strings.EqualFold(strings.TrimSpace(p.Title), strings.TrimSpace(title)) {
			return false
		}
	}
	return true
}
func validatePage(lib *Library, title, content string, sourceIDs []string, exceptID string) error {
	if err := validateTitle(title); err != nil {
		return err
	}
	if err := validateText(content); err != nil {
		return err
	}
	if !titleAvailable(lib, title, exceptID) {
		return problem(409, "a page with this title already exists")
	}
	return validateRefs(lib, content, sourceIDs)
}
func (s *Store) AddSource(id string, in SourceInput) (*Library, error) {
	if err := validateTitle(in.Title); err != nil {
		return nil, err
	}
	if err := validateText(in.Content); err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(in.Filename))
	if in.Filename == "" || len(in.Filename) > 240 || strings.ContainsAny(in.Filename, "/\\\x00\r\n") || (ext != ".md" && ext != ".txt") || !utf8.ValidString(in.Filename) {
		return nil, problem(400, "filename must be a Markdown (.md) or text (.txt) basename")
	}
	return s.mutate(id, func(lib *Library) error {
		h := sha256.Sum256([]byte(in.Content))
		lib.Sources = append(lib.Sources, Source{newID("src"), in.Title, in.Filename, in.Content, hex.EncodeToString(h[:]), "active", now()})
		addLog(lib, "保存原件："+in.Title)
		return nil
	})
}
func (s *Store) Withdraw(id, sourceID string) (*Library, error) {
	return s.mutate(id, func(lib *Library) error {
		src := activeSource(lib, sourceID)
		if src == nil {
			return problem(404, "active source not found")
		}
		src.Status = "withdrawn"
		for i := range lib.Pages {
			for _, ref := range lib.Pages[i].SourceIDs {
				if ref == sourceID {
					lib.Pages[i].Stale = true
				}
			}
		}
		addLog(lib, "撤回来源，保留原件："+src.Title)
		return nil
	})
}
func revision(p Page) Revision {
	return Revision{p.Version, p.Title, p.Content, append([]string{}, p.SourceIDs...), p.Updated}
}
func updatePage(p *Page, title, content string, ids []string) {
	p.History = append(p.History, revision(*p))
	p.Version++
	p.Title = title
	p.Content = content
	p.SourceIDs = append([]string{}, ids...)
	p.Updated = now()
	p.Stale = false
}
func createPage(title, content string, ids []string) Page {
	return Page{ID: newID("page"), Title: strings.TrimSpace(title), Content: content, SourceIDs: append([]string{}, ids...), Version: 1, Updated: now(), History: []Revision{}}
}
func (s *Store) SavePage(id, pageID string, in PageInput) (*Library, error) {
	return s.mutate(id, func(lib *Library) error {
		if err := validatePage(lib, in.Title, in.Content, in.SourceIDs, pageID); err != nil {
			return err
		}
		if pageID == "" {
			if in.ExpectedVersion != 0 {
				return problem(409, "new page expectedVersion must be 0")
			}
			lib.Pages = append(lib.Pages, createPage(in.Title, in.Content, in.SourceIDs))
		} else {
			p := findPage(lib, pageID)
			if p == nil {
				return problem(404, "page not found")
			}
			if p.Version != in.ExpectedVersion {
				return problem(409, "page changed; reload before saving")
			}
			updatePage(p, in.Title, in.Content, in.SourceIDs)
		}
		addLog(lib, "保存 Wiki："+in.Title)
		return nil
	})
}
func (s *Store) Restore(id, pageID string, version, expected int) (*Library, error) {
	return s.mutate(id, func(lib *Library) error {
		p := findPage(lib, pageID)
		if p == nil {
			return problem(404, "page not found")
		}
		if p.Version != expected {
			return problem(409, "page changed; reload before restoring")
		}
		for _, rev := range p.History {
			if rev.Version == version {
				if err := validatePage(lib, rev.Title, rev.Content, rev.SourceIDs, pageID); err != nil {
					return err
				}
				updatePage(p, rev.Title, rev.Content, rev.SourceIDs)
				addLog(lib, fmt.Sprintf("恢复 Wiki：%s，历史 v%d → v%d", p.Title, version, p.Version))
				return nil
			}
		}
		return problem(404, "revision not found")
	})
}
func validateChanges(lib *Library, changes []Change) error {
	if len(changes) == 0 || len(changes) > 50 {
		return problem(400, "proposal requires 1–50 changes")
	}
	targets := map[string]bool{}
	titles := map[string]bool{}
	for _, ch := range changes {
		if err := validatePage(lib, ch.Title, ch.Content, ch.SourceIDs, ch.PageID); err != nil {
			return err
		}
		key := strings.ToLower(strings.TrimSpace(ch.Title))
		if titles[key] {
			return problem(409, "proposal contains duplicate page titles")
		}
		titles[key] = true
		if ch.PageID == "" {
			if ch.BaseVersion != 0 {
				return problem(409, "new page baseVersion must be 0")
			}
			continue
		}
		if targets[ch.PageID] {
			return problem(400, "proposal updates a page more than once")
		}
		targets[ch.PageID] = true
		p := findPage(lib, ch.PageID)
		if p == nil {
			return problem(404, "proposal page not found")
		}
		if p.Version != ch.BaseVersion {
			return problem(409, "proposal conflicts with a newer page version")
		}
	}
	return nil
}
func (s *Store) Propose(id string, in ProposalInput) (*Library, error) {
	return s.mutate(id, func(lib *Library) error {
		if err := validateTitle(in.Title); err != nil {
			return err
		}
		if in.Kind != "manual" && in.Kind != "model" && in.Kind != "claw" {
			return problem(400, "kind must be manual, model, or claw")
		}
		if in.Kind == "claw" && (!lib.Config.ClawRead || !lib.Config.ClawDraft) {
			return problem(403, "CSGClaw read and draft permissions are required")
		}
		if err := validateChanges(lib, in.Changes); err != nil {
			return err
		}
		for i := range in.Changes {
			if in.Changes[i].SourceIDs == nil {
				in.Changes[i].SourceIDs = []string{}
			}
		}
		lib.Proposals = append(lib.Proposals, Proposal{ID: newID("proposal"), Title: in.Title, Kind: in.Kind, Status: "pending", Changes: in.Changes, Created: now()})
		addLog(lib, "生成待审提案："+in.Title)
		return nil
	})
}
func (s *Store) Review(id, proposalID string, accept bool) (*Library, error) {
	return s.mutate(id, func(lib *Library) error {
		var proposal *Proposal
		for i := range lib.Proposals {
			if lib.Proposals[i].ID == proposalID {
				proposal = &lib.Proposals[i]
				break
			}
		}
		if proposal == nil {
			return problem(404, "proposal not found")
		}
		if proposal.Status != "pending" {
			return problem(409, "proposal has already been reviewed")
		}
		if !accept {
			proposal.Status = "rejected"
			addLog(lib, "拒绝提案："+proposal.Title)
			return nil
		}
		if proposal.Kind == "claw" && (!lib.Config.ClawRead || !lib.Config.ClawDraft) {
			return problem(403, "CSGClaw permissions were revoked")
		}
		if proposal.Connection != nil {
			if err := s.validateConnectorProposal(lib, proposal); err != nil {
				return err
			}
		}
		// Validate the entire batch before touching any page, then commit one state.
		if err := validateChanges(lib, proposal.Changes); err != nil {
			return err
		}
		for _, ch := range proposal.Changes {
			if ch.PageID == "" {
				lib.Pages = append(lib.Pages, createPage(ch.Title, ch.Content, ch.SourceIDs))
			} else {
				updatePage(findPage(lib, ch.PageID), ch.Title, ch.Content, ch.SourceIDs)
			}
		}
		proposal.Status = "accepted"
		addLog(lib, "接受提案："+proposal.Title)
		return nil
	})
}
func (s *Store) Configure(id string, cfg Config) (*Library, error) {
	if len(cfg.Model) > 500 || len(cfg.ModelSource) > 200 {
		return nil, problem(400, "model setting too long")
	}
	if cfg.ClawDraft && !cfg.ClawRead {
		return nil, problem(400, "draft permission requires read permission")
	}
	return s.mutate(id, func(lib *Library) error { lib.Config = cfg; addLog(lib, "更新知识库能力配置"); return nil })
}

// atomicWrite keeps the previous canonical state intact when writing fails.
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func writeImmutable(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err == nil {
		if string(existing) != string(data) {
			return fmt.Errorf("immutable knowledge file differs: %s", filepath.Base(path))
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}
func (s *Store) persist(lib *Library) error {
	data, err := json.MarshalIndent(lib, "", "  ")
	if err != nil {
		return err
	}
	// Markdown is a projection of the atomic state. A failed write never commits
	// half a proposal; Open reconstructs projections from the last committed state.
	if err = s.project(lib); err != nil {
		return fmt.Errorf("writing knowledge files: %w", err)
	}
	dir, _ := s.dir(lib.ID)
	if err = s.commitState(filepath.Join(dir, ".knowledge", "state.json"), data); err != nil {
		return fmt.Errorf("committing knowledge state: %w", err)
	}
	return nil
}
func pageMarkdown(title, content string, version int, refs []string) []byte {
	return []byte(fmt.Sprintf("---\ntitle: %q\nversion: %d\nsource_ids: %s\n---\n\n%s\n", title, version, jsonList(refs), content))
}
func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func (s *Store) project(lib *Library) error {
	dir, err := s.dir(lib.ID)
	if err != nil {
		return err
	}
	for _, src := range lib.Sources {
		h := sha256.Sum256([]byte(src.Content))
		if hex.EncodeToString(h[:]) != src.SHA256 {
			return fmt.Errorf("source hash mismatch")
		}
		if err = writeImmutable(filepath.Join(dir, "raw", src.ID+strings.ToLower(filepath.Ext(src.Filename))), []byte(src.Content)); err != nil {
			return err
		}
	}
	var index strings.Builder
	fmt.Fprintf(&index, "# %s\n\n%s\n\n", lib.Name, lib.Description)
	for _, p := range lib.Pages {
		if err = atomicWrite(filepath.Join(dir, "wiki", p.ID+".md"), pageMarkdown(p.Title, p.Content, p.Version, p.SourceIDs)); err != nil {
			return err
		}
		for _, rev := range append(append([]Revision{}, p.History...), revision(p)) {
			if err = atomicWrite(filepath.Join(dir, ".knowledge", "revisions", p.ID, fmt.Sprintf("%d.md", rev.Version)), pageMarkdown(rev.Title, rev.Content, rev.Version, rev.SourceIDs)); err != nil {
				return err
			}
		}
		suffix := ""
		if p.Stale {
			suffix = " · 待复核"
		}
		fmt.Fprintf(&index, "- [%s](wiki/%s.md) · v%d%s\n", p.Title, p.ID, p.Version, suffix)
	}
	var log strings.Builder
	log.WriteString("# 变更日志\n\n")
	for _, entry := range lib.Logs {
		fmt.Fprintf(&log, "- %s %s\n", entry.Time, entry.Message)
	}
	cfg, err := json.MarshalIndent(lib.Config, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{"index.md": []byte(index.String()), "log.md": []byte(log.String()), "library.config.json": cfg, "SCHEMA.md": []byte("# LLM Wiki 文件约定\n\nraw/ 保存不可改写的 UTF-8 原件；wiki/ 保存审阅后的 Markdown。引用使用 [source:<id>]，双链使用 [[页面标题]]。AI 只创建待审提案，不直接写入正式 Wiki。撤回来源会使派生页面待复核。历史恢复生成新版本。\n\n.knowledge/state.json 是原子提交的唯一规范状态；Markdown、索引和日志由它投影，重新启动时修复。请通过知识库编辑器保存改动，外部改写投影文件不会自动导入。原件禁止覆盖。\n")}
	for name, data := range files {
		if err = atomicWrite(filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	return nil
}
