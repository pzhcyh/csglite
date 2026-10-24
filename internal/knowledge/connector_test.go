package knowledge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func connectorFixture(t *testing.T) (*Store, *Library, *CreatedConnection) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create("虚构连接测试", "")
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.AddSource(lib.ID, SourceInput{Title: "虚构活动", Filename: "fixture.md", Content: "RAW-ONLY-SECRET: 虚构活动资料"})
	if err != nil {
		t.Fatal(err)
	}
	sourceID := lib.Sources[0].ID
	lib, err = s.SavePage(lib.ID, "", PageInput{Title: "虚构活动计划", Content: "虚构活动于周六进行。[source:" + sourceID + "]", SourceIDs: []string{sourceID}})
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.Configure(lib.ID, Config{ClawRead: true, ClawDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConnection(lib.ID, ConnectionInput{Name: "测试 Agent", AllowDraft: true, ExpiresInDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	return s, lib, c
}
func expectConnectorStatus(t *testing.T, err error, want int) {
	t.Helper()
	var target *Error
	if !errors.As(err, &target) || target.Status != want {
		t.Fatalf("wanted %d, got %v", want, err)
	}
}
func connectorProposal(lib *Library) ConnectorProposalInput {
	p := lib.Pages[0]
	return ConnectorProposalInput{TaskID: "fictional-task", RunID: "run-1", RequestID: "request-1", Title: "虚构更新", Changes: []Change{{PageID: p.ID, Title: p.Title, Content: "周日进行。[source:" + lib.Sources[0].ID + "]", SourceIDs: []string{lib.Sources[0].ID}, BaseVersion: p.Version}}}
}
func TestConnectorCredentialScopePersistenceAndRead(t *testing.T) {
	s, lib, c := connectorFixture(t)
	data, err := os.ReadFile(filepath.Join(s.root, ".connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), c.Token) || !strings.Contains(string(data), "tokenHash") {
		t.Fatal("credential not safely persisted")
	}
	info, _ := os.Stat(filepath.Join(s.root, ".connections.json"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("credential mode %v", info.Mode())
	}
	list, err := s.Connections(lib.ID)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	public, _ := json.Marshal(list)
	if strings.Contains(string(public), "token") || strings.Contains(string(public), c.Token) {
		t.Fatal("secret exposed")
	}
	s2, err := Open(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	pages, err := s2.ConnectorSearch(c.Token, "活动计划", 5)
	if err != nil || len(pages) != 1 {
		t.Fatal(pages, err)
	}
	body, _ := json.Marshal(pages)
	if strings.Contains(string(body), "RAW-ONLY-SECRET") || len(pages[0].Sources) != 1 || pages[0].Version != 1 {
		t.Fatal("missing provenance or leaked raw")
	}
	other, err := s.Create("其他库", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err = s.SavePage(other.ID, "", PageInput{Title: "其他库私有页", Content: "private"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ConnectorRead(c.Token, other.Pages[0].ID, 0)
	expectConnectorStatus(t, err, 404)
	_, err = s.ConnectorAccess(c.Token + "x")
	expectConnectorStatus(t, err, 401)
	_, err = s.Withdraw(lib.ID, lib.Sources[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ConnectorRead(c.Token, lib.Pages[0].ID, 0)
	expectConnectorStatus(t, err, 404)
	pages, err = s.ConnectorSearch(c.Token, "活动", 5)
	if err != nil || len(pages) != 0 {
		t.Fatal("withdrawn source still searchable", err)
	}
}
func TestConnectorProposalIdempotencyAndRevocation(t *testing.T) {
	s, lib, c := connectorFixture(t)
	in := connectorProposal(lib)
	var wg sync.WaitGroup
	results := make(chan *ConnectorProposalResult, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := s.ConnectorPropose(c.Token, in); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	proposalID := ""
	first := 0
	for r := range results {
		if proposalID != "" && proposalID != r.ProposalID {
			t.Fatal("duplicate proposal created")
		}
		proposalID = r.ProposalID
		if !r.Duplicate {
			first++
		}
	}
	if first != 1 {
		t.Fatalf("non duplicate count %d", first)
	}
	loaded, err := s.Get(lib.ID)
	if err != nil || len(loaded.Proposals) != 1 || loaded.Pages[0].Version != 1 {
		t.Fatal("proposal published or duplicated", err)
	}
	provenance := loaded.Proposals[0].Connection
	if provenance == nil || provenance.TaskID != in.TaskID || provenance.RunID != in.RunID || len(provenance.Sources) != 1 {
		t.Fatal("missing task provenance")
	}
	changed := in
	changed.Title = "不同内容"
	_, err = s.ConnectorPropose(c.Token, changed)
	expectConnectorStatus(t, err, 409)
	if _, err = s.RevokeConnection(lib.ID, c.Connection.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.ConnectorAccess(c.Token)
	expectConnectorStatus(t, err, 401)
	_, err = s.ConnectorPropose(c.Token, in)
	expectConnectorStatus(t, err, 401)
	_, err = s.Review(lib.ID, proposalID, true)
	expectConnectorStatus(t, err, 403)
	if _, err = s.Review(lib.ID, proposalID, false); err != nil {
		t.Fatal("rejection should remain available", err)
	}
}
func TestConnectorProposalAcceptedAndConflict(t *testing.T) {
	s, lib, c := connectorFixture(t)
	in := connectorProposal(lib)
	r, err := s.ConnectorPropose(c.Token, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Review(lib.ID, r.ProposalID, true); err != nil {
		t.Fatal(err)
	}
	r, err = s.ConnectorPropose(c.Token, in)
	if err != nil || !r.Duplicate || r.Status != "accepted" {
		t.Fatal("retry after review not idempotent", err)
	}
	in.RequestID = "new-request"
	_, err = s.ConnectorPropose(c.Token, in)
	expectConnectorStatus(t, err, 409)
	page, err := s.ConnectorRead(c.Token, lib.Pages[0].ID, 0)
	if err != nil || page.Version != 2 {
		t.Fatal(page, err)
	}
}
func TestConnectorReadOnlyExpiryAndLibraryConfig(t *testing.T) {
	s, lib, c := connectorFixture(t)
	ro, err := s.CreateConnection(lib.ID, ConnectionInput{Name: "只读", AllowDraft: false})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ConnectorPropose(ro.Token, connectorProposal(lib))
	expectConnectorStatus(t, err, 403)
	if _, err = s.Configure(lib.ID, Config{}); err != nil {
		t.Fatal(err)
	}
	_, err = s.ConnectorRead(c.Token, lib.Pages[0].ID, 0)
	expectConnectorStatus(t, err, 403)
	if _, err = s.Configure(lib.ID, Config{ClawRead: true, ClawDraft: true}); err != nil {
		t.Fatal(err)
	}
	proposal, err := s.ConnectorPropose(c.Token, connectorProposal(lib))
	if err != nil {
		t.Fatal(err)
	}
	creds, err := s.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	creds[0].ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if err = s.saveCredentials(creds); err != nil {
		t.Fatal(err)
	}
	_, err = s.ConnectorAccess(c.Token)
	expectConnectorStatus(t, err, 401)
	_, err = s.Review(lib.ID, proposal.ProposalID, true)
	expectConnectorStatus(t, err, 403)
}
func TestConnectorReadChunksAndRestoredProposalCannotAccept(t *testing.T) {
	s, lib, c := connectorFixture(t)
	p := lib.Pages[0]
	updated, err := s.SavePage(lib.ID, p.ID, PageInput{Title: p.Title, Content: strings.Repeat("汉", 9000), SourceIDs: p.SourceIDs, ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ConnectorRead(c.Token, p.ID, 0)
	if err != nil || page.NextOffset == nil || *page.NextOffset != 8000 || len([]rune(page.Content)) != 8000 {
		t.Fatal("chunk failed", err)
	}
	tail, err := s.ConnectorRead(c.Token, p.ID, *page.NextOffset)
	if err != nil || tail.NextOffset != nil || len([]rune(tail.Content)) != 1000 {
		t.Fatal("tail failed", err)
	}
	_, err = s.ConnectorRead(c.Token, p.ID, -1)
	expectConnectorStatus(t, err, 400)
	_, err = s.ConnectorPropose(c.Token, connectorProposal(updated))
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.Get(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.Create("恢复副本", "")
	if err != nil {
		t.Fatal(err)
	}
	original.ID = restored.ID
	if err = s.persist(original); err != nil {
		t.Fatal(err)
	}
	_, err = s.Review(original.ID, original.Proposals[0].ID, true)
	expectConnectorStatus(t, err, 403)
}
