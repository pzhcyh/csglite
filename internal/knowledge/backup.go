package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const MaxBackupBytes = 20 * 1024 * 1024

// Backup is a portable canonical snapshot, not a public sharing export.
// Client credentials deliberately live outside Library and never travel here.
type Backup struct {
	Format  string          `json:"format"`
	Version int             `json:"version"`
	Created string          `json:"created"`
	SHA256  string          `json:"sha256"`
	Library json.RawMessage `json:"library"`
}

// Hash canonical JSON values, not formatting/escaping chosen by a client.
// JavaScript and Python re-encode Unicode and object key order differently.
func canonicalBackupJSON(data []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (s *Store) Backup(id string) (*Backup, error) {
	lib, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(lib)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBackupBytes {
		return nil, problem(413, "library exceeds this version's 20 MiB backup limit")
	}
	canonical, err := canonicalBackupJSON(data)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(canonical)
	backup := &Backup{Format: "csglite-knowledge-backup", Version: 1, Created: now(), SHA256: hex.EncodeToString(h[:]), Library: data}
	file, err := json.Marshal(backup)
	if err != nil {
		return nil, err
	}
	if len(file) > MaxBackupBytes {
		return nil, problem(413, "complete backup exceeds this version's 20 MiB file limit")
	}
	return backup, nil
}

func validateBackupLibrary(lib *Library) error {
	if err := validateTitle(lib.Name); err != nil {
		return err
	}
	if len(lib.Description) > 4000 || len(lib.Config.Model) > 500 || len(lib.Config.ModelSource) > 200 {
		return problem(400, "invalid backup metadata")
	}
	seen := map[string]bool{}
	count := 0
	checkID := func(id, prefix string) error {
		count++
		if count > 10000 || !validID.MatchString(id) || !strings.HasPrefix(id, prefix+"-") || seen[id] {
			return problem(400, "invalid, duplicate or excessive backup records")
		}
		seen[id] = true
		return nil
	}
	sourceIDs := map[string]bool{}
	for _, src := range lib.Sources {
		if err := checkID(src.ID, "src"); err != nil {
			return err
		}
		if err := validateTitle(src.Title); err != nil {
			return err
		}
		if err := validateText(src.Content); err != nil {
			return err
		}
		ext := strings.ToLower(filepath.Ext(src.Filename))
		if src.Filename == "" || len(src.Filename) > 240 || strings.ContainsAny(src.Filename, "/\\\x00") || (ext != ".md" && ext != ".txt") || (src.Status != "active" && src.Status != "withdrawn") {
			return problem(400, "invalid backup source")
		}
		h := sha256.Sum256([]byte(src.Content))
		if hex.EncodeToString(h[:]) != src.SHA256 {
			return problem(400, "backup source checksum mismatch")
		}
		sourceIDs[src.ID] = true
	}
	checkContent := func(title, content string, refs []string) error {
		if err := validateTitle(title); err != nil {
			return err
		}
		if err := validateText(content); err != nil {
			return err
		}
		unique := map[string]bool{}
		for _, id := range refs {
			if !sourceIDs[id] || unique[id] {
				return problem(400, "invalid backup source reference")
			}
			unique[id] = true
		}
		var citationErr error
		mapMarkdownText(content, func(prose string) string {
			for _, m := range sourceCitation.FindAllStringSubmatch(prose, -1) {
				if !unique[m[1]] {
					citationErr = problem(400, "backup citation missing from source references")
				}
			}
			return prose
		})
		return citationErr
	}
	pages := map[string]bool{}
	for i := range lib.Pages {
		p := &lib.Pages[i]
		if err := checkID(p.ID, "page"); err != nil {
			return err
		}
		pages[p.ID] = true
		if p.Version < 1 || p.Version > 1000000 {
			return problem(400, "invalid backup version")
		}
		if err := checkContent(p.Title, p.Content, p.SourceIDs); err != nil {
			return err
		}
		versions := map[int]bool{}
		for _, rev := range p.History {
			count++
			if count > 10000 || rev.Version < 1 || rev.Version >= p.Version || versions[rev.Version] {
				return problem(400, "invalid backup revision")
			}
			versions[rev.Version] = true
			if err := checkContent(rev.Title, rev.Content, rev.SourceIDs); err != nil {
				return err
			}
		}
		p.Stale = p.Stale || validateRefs(lib, p.Content, p.SourceIDs) != nil
	}
	for _, p := range lib.Proposals {
		if err := checkID(p.ID, "proposal"); err != nil {
			return err
		}
		if err := validateTitle(p.Title); err != nil {
			return err
		}
		if (p.Status != "pending" && p.Status != "accepted" && p.Status != "rejected") || len(p.Changes) < 1 || len(p.Changes) > 50 {
			return problem(400, "invalid backup proposal")
		}
		if p.Kind != "manual" && p.Kind != "model" && p.Kind != "claw" && p.Kind != "demo" {
			return problem(400, "invalid backup proposal kind")
		}
		for _, c := range p.Changes {
			if (c.PageID != "" && !pages[c.PageID]) || c.BaseVersion < 0 || c.BaseVersion > 1000000 {
				return problem(400, "invalid backup change")
			}
			if err := checkContent(c.Title, c.Content, c.SourceIDs); err != nil {
				return err
			}
		}
	}
	for _, l := range lib.Logs {
		if err := checkID(l.ID, "log"); err != nil {
			return err
		}
		if len(l.Message) > 10000 {
			return problem(400, "invalid backup log")
		}
	}
	if len(lib.StewardJobs) > 100 {
		return problem(400, "too many backup steward jobs")
	}
	for _, j := range lib.StewardJobs {
		if err := checkID(j.ID, "job"); err != nil {
			return err
		}
		if j.Status != "queued" && j.Status != "running" && j.Status != "completed" && j.Status != "failed" && j.Status != "cancelled" {
			return problem(400, "invalid backup job state")
		}
		if len(j.SourceIDs) < 1 || len(j.SourceIDs) > 8 || j.Attempts < 0 || j.Attempts > 3 || len(j.Model) > 500 || len(j.ModelSource) > 200 || len(j.Error) > 4000 {
			return problem(400, "invalid backup job")
		}
		unique := map[string]bool{}
		for _, id := range j.SourceIDs {
			if !sourceIDs[id] || unique[id] {
				return problem(400, "invalid backup job sources")
			}
			unique[id] = true
			if len(j.SourceSnapshots[id]) != 64 {
				return problem(400, "invalid backup job snapshot")
			}
			if _, err := hex.DecodeString(j.SourceSnapshots[id]); err != nil {
				return problem(400, "invalid backup snapshot hash")
			}
		}
		for id := range j.SourceSnapshots {
			if !sourceIDs[id] || len(j.SourceSnapshots[id]) != 64 {
				return problem(400, "invalid backup job snapshot reference")
			}
			if _, err := hex.DecodeString(j.SourceSnapshots[id]); err != nil {
				return problem(400, "invalid backup snapshot hash")
			}
		}
		for id, version := range j.PageVersions {
			if !pages[id] || version < 1 || version > 1000000 {
				return problem(400, "invalid backup job page snapshot")
			}
		}
		if j.ProposalID != "" && (!seen[j.ProposalID] || !strings.HasPrefix(j.ProposalID, "proposal-")) {
			return problem(400, "invalid backup job proposal")
		}
	}
	return nil
}

// RestoreBackup creates an isolated new library and never overwrites existing
// data or reactivates the original library's client permissions.
func (s *Store) RestoreBackup(b Backup) (*Library, error) {
	if b.Format != "csglite-knowledge-backup" || b.Version != 1 || len(b.Library) > MaxBackupBytes {
		return nil, problem(400, "unsupported or oversized knowledge backup")
	}
	// Canonicalize JSON values so formatting and Unicode escaping do not change the checksum.
	var lib Library
	if err := json.Unmarshal(b.Library, &lib); err != nil {
		return nil, problem(400, "invalid backup library")
	}
	canonical, err := canonicalBackupJSON(b.Library)
	if err != nil {
		return nil, problem(400, "invalid backup JSON")
	}
	h := sha256.Sum256(canonical)
	if hex.EncodeToString(h[:]) != b.SHA256 {
		return nil, problem(400, "backup checksum mismatch")
	}
	if err := validateBackupLibrary(&lib); err != nil {
		return nil, err
	}
	lib.ID = newID("lib")
	lib.Config.ClawRead = false
	lib.Config.ClawDraft = false
	NormalizeRestoredSteward(&lib)
	addLog(&lib, "从完整备份恢复为独立知识库；应用访问权限保持关闭")
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, _ := s.dir(lib.ID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		return nil, problem(409, "restore destination already exists")
	}
	if err := s.persist(&lib); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &lib, nil
}
