package knowledge

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (*Store, *Library, string) {
	t.Helper()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create("青松阅读计划", "虚构测试")
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.SeedDemo(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, lib, root
}
func requireStatus(t *testing.T, err error, status int) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != status {
		t.Fatalf("expected %d, got %v", status, err)
	}
}
func zipFiles(t *testing.T, b []byte) map[string]string {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range r.File {
		reader, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func TestEmptyFirstLaunchAndExplicitDemo(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	libs, err := s.List()
	if err != nil || len(libs) != 0 {
		t.Fatalf("nonempty startup: %v", err)
	}
	lib, err := s.Create("Empty", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Pages) != 0 || len(lib.Sources) != 0 || lib.Config.ClawRead || lib.Config.ClawDraft {
		t.Fatal("defaults expose demo or agent access")
	}
	lib, err = s.SeedDemo(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Pages) != 3 || len(lib.Proposals[0].Changes) != 2 {
		t.Fatal("demo missing")
	}
	_, err = s.SeedDemo(lib.ID)
	requireStatus(t, err, 409)
}

func TestAcceptPreservesOriginalsAndSurvivesRestart(t *testing.T) {
	s, lib, root := fixture(t)
	raw := map[string][]byte{}
	for _, src := range lib.Sources {
		p := filepath.Join(root, "knowledge", lib.ID, "raw", src.ID+filepath.Ext(src.Filename))
		raw[p] = []byte(src.Content)
	}
	got, err := s.Review(lib.ID, lib.Proposals[0].ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pages[1].Version != 2 || got.Pages[2].Version != 2 || !strings.Contains(got.Pages[1].Content, "24 人") {
		t.Fatal("all changes not accepted")
	}
	for p, expected := range raw {
		data, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(data, expected) {
			t.Fatal("original changed")
		}
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.Get(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Pages[1].Version != 2 || saved.Proposals[0].Status != "accepted" {
		t.Fatal("state lost at restart")
	}
	_, err = s.Review(lib.ID, lib.Proposals[0].ID, true)
	requireStatus(t, err, 409)
}

func TestProposalConflictDoesNotPartiallyAccept(t *testing.T) {
	s, lib, _ := fixture(t)
	p := lib.Pages[2]
	_, err := s.SavePage(lib.ID, p.ID, PageInput{p.Title, p.Content + "\n人工补充", p.SourceIDs, 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Review(lib.ID, lib.Proposals[0].ID, true)
	requireStatus(t, err, 409)
	got, _ := s.Get(lib.ID)
	if got.Pages[1].Version != 1 || got.Pages[1].Content != lib.Pages[1].Content || got.Proposals[0].Status != "pending" {
		t.Fatal("conflict partially applied")
	}
}

func TestWithdrawBlocksAcceptanceAndExcludesDerivedPages(t *testing.T) {
	s, lib, _ := fixture(t)
	lib, err := s.Review(lib.ID, lib.Proposals[0].ID, true)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := lib.Sources[1].ID
	lib, err = s.Withdraw(lib.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !lib.Pages[1].Stale || !lib.Pages[2].Stale {
		t.Fatal("dependent pages not stale")
	}
	lib, err = s.Configure(lib.ID, Config{ModelSource: "local", ClawRead: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := s.Context(lib.ID)
	if err != nil || len(ctx.Pages) != 1 || len(ctx.Sources) != 0 {
		t.Fatalf("stale content leaked: %#v, %v", ctx, err)
	}
	b, err := s.Export(lib.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, b)
	for name := range files {
		if strings.Contains(name, lib.Pages[1].ID) || strings.HasPrefix(name, "raw/") {
			t.Fatal("stale data exported")
		}
	}
	s2, lib2, _ := fixture(t)
	_, err = s2.Withdraw(lib2.ID, lib2.Sources[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s2.Review(lib2.ID, lib2.Proposals[0].ID, true)
	requireStatus(t, err, 409)
}

func TestIsolationAndClawRevalidation(t *testing.T) {
	s, lib, _ := fixture(t)
	other, err := s.Create("Other", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SavePage(other.ID, "", PageInput{Title: "Leak", Content: "content", SourceIDs: []string{lib.Sources[0].ID}})
	requireStatus(t, err, 409)
	_, err = s.Review(other.ID, lib.Proposals[0].ID, true)
	requireStatus(t, err, 404)
	_, err = s.Context(lib.ID)
	requireStatus(t, err, 403)
	cfg := Config{ClawRead: true, ClawDraft: true, ModelSource: "local"}
	_, err = s.Configure(lib.ID, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Propose(lib.ID, ProposalInput{"Agent draft", "claw", []Change{{Title: "新提案", Content: "草稿", SourceIDs: []string{}}}})
	if err != nil {
		t.Fatal(err)
	}
	proposalID := got.Proposals[len(got.Proposals)-1].ID
	cfg.ClawDraft = false
	_, err = s.Configure(lib.ID, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Review(lib.ID, proposalID, true)
	requireStatus(t, err, 403)
	got, _ = s.Get(lib.ID)
	if len(got.Pages) != 3 {
		t.Fatal("revoked draft was published")
	}
}

func TestRestoreAddsRevisionAndRejectKeepsWiki(t *testing.T) {
	s, lib, _ := fixture(t)
	got, err := s.Review(lib.ID, lib.Proposals[0].ID, false)
	if err != nil || got.Pages[1].Version != 1 {
		t.Fatal("reject modified pages")
	}
	p := got.Pages[1]
	got, err = s.SavePage(lib.ID, p.ID, PageInput{p.Title, "new body", []string{}, 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Restore(lib.ID, p.ID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	p2 := got.Pages[1]
	if p2.Version != 3 || p2.Content != p.Content || len(p2.History) != 2 {
		t.Fatal("restore did not append version")
	}
}

func TestInputValidationAndExactOriginal(t *testing.T) {
	s, lib, root := fixture(t)
	_, err := s.Get("../../bad")
	requireStatus(t, err, 400)
	for _, name := range []string{"../escape.txt", "x/y.md", "x\\y.txt", "binary.pdf", "x\x00.txt"} {
		_, err = s.AddSource(lib.ID, SourceInput{"x", name, "text"})
		requireStatus(t, err, 400)
	}
	for _, text := range []string{string([]byte{255}), "x\x00y", strings.Repeat("x", MaxTextBytes+1)} {
		_, err = s.AddSource(lib.ID, SourceInput{"x", "x.txt", text})
		requireStatus(t, err, 400)
	}
	_, err = s.SavePage(lib.ID, "", PageInput{Title: "Bad citation", Content: "[source:unknown]"})
	requireStatus(t, err, 400)
	raw := "\ufeff# 原始文本\r\n\r\n  保留 空格  \r\n"
	got, err := s.AddSource(lib.ID, SourceInput{"Exact", "exact.txt", raw})
	if err != nil {
		t.Fatal(err)
	}
	src := got.Sources[len(got.Sources)-1]
	b, err := os.ReadFile(filepath.Join(root, "knowledge", lib.ID, "raw", src.ID+".txt"))
	if err != nil || string(b) != raw {
		t.Fatal("source normalized")
	}
}

func TestExportPortableLinksAndRawOptIn(t *testing.T) {
	s, lib, _ := fixture(t)
	b, err := s.Export(lib.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, b)
	for name := range files {
		if strings.HasPrefix(name, "raw/") || strings.Contains(name, "proposal") || strings.Contains(name, "config") {
			t.Fatal("private data exported")
		}
	}
	article := files["wiki/"+lib.Pages[0].ID+".md"]
	if !strings.Contains(article, "("+lib.Pages[1].ID+".md)") || strings.Contains(article, "[[活动安排]]") {
		t.Fatal("wikilink not portable")
	}
	if !strings.Contains(files["manifest.json"], lib.Sources[0].SHA256) {
		t.Fatal("missing source hash")
	}
	b, err = s.Export(lib.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	files = zipFiles(t, b)
	if files["raw/"+lib.Sources[0].ID+".md"] != lib.Sources[0].Content {
		t.Fatal("raw opt-in missing")
	}
	if _, ok := files["raw/"+lib.Sources[1].ID+".txt"]; ok {
		t.Fatal("uncited pending-only source leaked")
	}
}

func TestFailedCommitCanRetryAndReopenRepairsProjection(t *testing.T) {
	s, lib, root := fixture(t)
	p := lib.Pages[1]
	s.commitState = func(string, []byte) error { return errors.New("injected disk failure after projection") }
	_, err := s.SavePage(lib.ID, p.ID, PageInput{p.Title, "failed body", []string{}, 1})
	if err == nil {
		t.Fatal("failure not propagated")
	}
	saved, _ := s.Get(lib.ID)
	if saved.Pages[1].Version != 1 || saved.Pages[1].Content != p.Content {
		t.Fatal("failed commit changed canonical state")
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "knowledge", lib.ID, "wiki", p.ID+".md"))
	if err != nil || strings.Contains(string(body), "failed body") {
		t.Fatal("restart did not repair projection")
	}
	got, err := s2.SavePage(lib.ID, p.ID, PageInput{p.Title, "different retry body", []string{}, 1})
	if err != nil {
		t.Fatalf("orphan revision blocked retry: %v", err)
	}
	if got.Pages[1].Version != 2 || got.Pages[1].Content != "different retry body" {
		t.Fatal("retry did not save")
	}
}

func TestExportAndCitationValidationPreserveMarkdownLiterals(t *testing.T) {
	s, lib, _ := fixture(t)
	ref := lib.Sources[0].ID
	literals := []string{
		"`[[活动安排]] [source:missing-inline]`",
		"``literal ` [[活动安排]] [source:missing-double]``",
		"```markdown\n[[活动安排]] [source:missing-fence]\n```",
		"~~~text\n[[活动安排]] [source:missing-tilde]\n~~~",
		"\\[[活动安排]] \\[source:missing-escape]",
		"    [[活动安排]] [source:missing-indented]",
	}
	body := "Actual [[活动安排]] [source:" + ref + "]\n\n" + strings.Join(literals, "\n\n")
	got, err := s.SavePage(lib.ID, "", PageInput{Title: "语法示例", Content: body, SourceIDs: []string{ref}})
	if err != nil {
		t.Fatalf("code example treated as citation: %v", err)
	}
	p := got.Pages[len(got.Pages)-1]
	for _, includeRaw := range []bool{false, true} {
		b, err := s.Export(lib.ID, includeRaw)
		if err != nil {
			t.Fatal(err)
		}
		exported := zipFiles(t, b)["wiki/"+p.ID+".md"]
		if !strings.Contains(exported, "Actual [活动安排]("+lib.Pages[1].ID+".md)") {
			t.Fatal("real link was not converted")
		}
		for _, literal := range literals {
			if !strings.Contains(exported, literal) {
				t.Fatalf("literal changed: %q\n%s", literal, exported)
			}
		}
	}
}
