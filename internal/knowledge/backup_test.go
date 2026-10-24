package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupRestorePreservesHistoryWithoutGrantingAccess(t *testing.T) {
	s, lib, root := fixture(t)
	lib, err := s.Review(lib.ID, lib.Proposals[0].ID, true)
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.Configure(lib.ID, Config{Model: "fictional-model", ModelSource: "cloud", ClawRead: true, ClawDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(lib)
	b, err := s.Backup(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	pretty, _ := json.MarshalIndent(b, "", "  ")
	var clientValues map[string]any
	if err = json.Unmarshal(pretty, &clientValues); err != nil {
		t.Fatal(err)
	}
	pretty, _ = json.Marshal(clientValues) // Clients may reorder object keys.
	pretty = bytes.ReplaceAll(pretty, []byte("青"), []byte(`\u9752`))
	if err = json.Unmarshal(pretty, &b); err != nil {
		t.Fatal(err)
	}
	restored, err := s.RestoreBackup(*b)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID == lib.ID || restored.Config.ClawRead || restored.Config.ClawDraft {
		t.Fatal("restore reused identity/access")
	}
	if len(restored.Pages) != len(lib.Pages) || len(restored.Pages[1].History) != len(lib.Pages[1].History) || restored.Proposals[0].Status != "accepted" {
		t.Fatal("lost canonical history")
	}
	for _, src := range restored.Sources {
		data, err := os.ReadFile(filepath.Join(root, "knowledge", restored.ID, "raw", src.ID+filepath.Ext(src.Filename)))
		if err != nil || string(data) != src.Content {
			t.Fatal("raw not restored", err)
		}
	}
	old, _ := s.Get(lib.ID)
	after, _ := json.Marshal(old)
	if string(before) != string(after) {
		t.Fatal("original library changed")
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(restored.ID)
	if err != nil || len(got.Pages) != len(lib.Pages) {
		t.Fatal("restored library did not survive restart", err)
	}
}

func TestBackupPreservesLiteralCitationExamples(t *testing.T) {
	s, lib, _ := fixture(t)
	content := "Inline `[source:missing]`\n\n```txt\n[source:another]\n```\n\n    [source:code]\n"
	lib, err := s.SavePage(lib.ID, "", PageInput{Title: "Citation syntax", Content: content, SourceIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Backup(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RestoreBackup(*b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pages[len(r.Pages)-1].Content != content {
		t.Fatal("literal example changed")
	}
}

func TestBackupValidatesStewardRecordsAndCancelsRestoredJobs(t *testing.T) {
	s, lib, _ := fixture(t)
	_, err := s.Configure(lib.ID, Config{Model: "fixture-model", ModelSource: "local"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Backup(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RestoreBackup(*b)
	if err != nil {
		t.Fatal(err)
	}
	if r.StewardJobs[0].Status != "cancelled" {
		t.Fatal("restored work remained executable")
	}
	var bad Library
	_ = json.Unmarshal(b.Library, &bad)
	bad.StewardJobs[0].SourceIDs = nil
	b.Library, _ = json.Marshal(bad)
	canonical, _ := canonicalBackupJSON(b.Library)
	h := sha256.Sum256(canonical)
	b.SHA256 = hex.EncodeToString(h[:])
	_, err = s.RestoreBackup(*b)
	requireStatus(t, err, 400)
}

func TestBackupEnforcesCompleteFileLimit(t *testing.T) {
	s, lib, root := fixture(t)
	lib.Pages = []Page{}
	lib.Proposals = []Proposal{}
	lib.Logs = []Log{}
	lib.Sources = []Source{}
	for i := 0; i < 10; i++ {
		text := strings.Repeat("x", MaxTextBytes)
		h := sha256.Sum256([]byte(text))
		lib.Sources = append(lib.Sources, Source{ID: newID("src"), Title: "Large fictional source", Filename: "sample.txt", Content: text, SHA256: hex.EncodeToString(h[:]), Status: "active", Created: now()})
	}
	data, _ := json.Marshal(lib)
	last := &lib.Sources[len(lib.Sources)-1]
	last.Content = last.Content[:len(last.Content)-(len(data)-(MaxBackupBytes-64))]
	h := sha256.Sum256([]byte(last.Content))
	last.SHA256 = hex.EncodeToString(h[:])
	data, _ = json.Marshal(lib)
	state := filepath.Join(root, "knowledge", lib.ID, ".knowledge", "state.json")
	if err := atomicWrite(state, data); err != nil {
		t.Fatal(err)
	}
	_, err := s.Backup(lib.ID)
	requireStatus(t, err, 413)
	last.Content = last.Content[:len(last.Content)-4096]
	h = sha256.Sum256([]byte(last.Content))
	last.SHA256 = hex.EncodeToString(h[:])
	data, _ = json.Marshal(lib)
	if err = atomicWrite(state, data); err != nil {
		t.Fatal(err)
	}
	b, err := s.Backup(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := json.Marshal(b)
	if len(file) > MaxBackupBytes {
		t.Fatal("export cannot fit restore limit")
	}
	if _, err = s.RestoreBackup(*b); err != nil {
		t.Fatal("near-limit backup is not restorable", err)
	}
}

func TestBackupRestoreRejectsTamperingAndTraversal(t *testing.T) {
	s, lib, _ := fixture(t)
	for _, kind := range []string{"checksum", "source-id", "page-id", "filename", "source-hash", "revision"} {
		t.Run(kind, func(t *testing.T) {
			b, err := s.Backup(lib.ID)
			if err != nil {
				t.Fatal(err)
			}
			var v Library
			_ = json.Unmarshal(b.Library, &v)
			switch kind {
			case "checksum":
				b.SHA256 = "wrong"
			case "source-id":
				v.Sources[0].ID = "../../escape"
			case "page-id":
				v.Pages[0].ID = "../../escape"
			case "filename":
				v.Sources[0].Filename = "../a.md"
			case "source-hash":
				v.Sources[0].SHA256 = "wrong"
			case "revision":
				v.Pages[0].History = []Revision{{Version: -1, Title: "bad"}}
			}
			if kind != "checksum" {
				b.Library, _ = json.Marshal(v)
				canonical, _ := canonicalBackupJSON(b.Library)
				h := sha256.Sum256(canonical)
				b.SHA256 = hex.EncodeToString(h[:])
			}
			_, err = s.RestoreBackup(*b)
			requireStatus(t, err, 400)
			libs, err := s.List()
			if err != nil || len(libs) != 1 {
				t.Fatal("rejected backup created partial library", err)
			}
		})
	}
}

func TestBackupKeepsWithdrawnSourcesAndStalePages(t *testing.T) {
	s, lib, _ := fixture(t)
	lib, err := s.Withdraw(lib.ID, lib.Sources[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Backup(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RestoreBackup(*b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Sources[0].Status != "withdrawn" {
		t.Fatal("withdrawn source reactivated")
	}
	for i, p := range lib.Pages {
		if p.Stale && !r.Pages[i].Stale {
			t.Fatal("stale page reactivated")
		}
	}
}
