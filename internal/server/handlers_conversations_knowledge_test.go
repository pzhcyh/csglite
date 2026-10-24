package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/chathistory"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestConversationKnowledgeMetadataRoundTrip(t *testing.T) {
	s := newTestServer(t)
	// Use wire JSON so accidental camelCase tags or missing DTO fields fail.
	body := `{
		"title":"Fictional knowledge conversation","model":"local-model",
		"settings":{"num_ctx":4096,"knowledge":{"library_id":"lib-fictional","page_ids":["page-plan","page-materials"]}},
		"messages":[{"role":"assistant","content":"A fictional answer","meta":{
			"tokens":12,"sources":[{"title":"Web source","url":"https://example.com","snippet":"reference","engine":"bing"}],
			"knowledge":{"library_id":"lib-fictional","library_name":"Fictional reading plan","captured_at":"2026-10-24T08:30:00Z",
				"pages":[{"id":"page-plan","title":"Plan","version":2,"source_ids":["src-confirmation"]},{"id":"page-materials","title":"Materials","version":3,"source_ids":[]}],
				"sources":[{"id":"src-confirmation","title":"Confirmation","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}
		}}]}`
	var expected api.Conversation
	if err := json.Unmarshal([]byte(body), &expected); err != nil {
		t.Fatal(err)
	}
	if expected.Settings.Knowledge == nil || expected.Messages[0].Meta.Knowledge == nil {
		t.Fatal("knowledge wire fields were dropped")
	}
	create := httptest.NewRecorder()
	s.handleConversationCreate(create, httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(body)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	var created api.Conversation
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	assertConversationKnowledge(t, created, expected)

	// Persist a new selection through PUT while preserving the earlier message's
	// captured sources. Future-turn selection and historical context are distinct.
	expected.Settings.Knowledge.PageIDs = []string{"page-plan"}
	created.Settings = expected.Settings
	selectionBody, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	selectionRequest := httptest.NewRequest(http.MethodPut, "/api/conversations/"+created.ID, strings.NewReader(string(selectionBody)))
	selectionRequest.SetPathValue("id", created.ID)
	selectionResponse := httptest.NewRecorder()
	s.handleConversationUpdate(selectionResponse, selectionRequest)
	if selectionResponse.Code != http.StatusOK {
		t.Fatalf("selection update: %d %s", selectionResponse.Code, selectionResponse.Body.String())
	}

	// Changing an unrelated field without settings/messages must not erase the
	// knowledge selection or source snapshot already saved with the conversation.
	patch := httptest.NewRequest(http.MethodPut, "/api/conversations/"+created.ID, strings.NewReader(`{"title":"Renamed conversation"}`))
	patch.SetPathValue("id", created.ID)
	updated := httptest.NewRecorder()
	s.handleConversationUpdate(updated, patch)
	if updated.Code != http.StatusOK {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}

	// Reopen the disk store, then GET through the normal HTTP handler. This tests
	// persisted data, not only the create response or a retained object pointer.
	home, err := config.AppHome()
	if err != nil {
		t.Fatal(err)
	}
	s.conversations = chathistory.NewStore(home)
	get := httptest.NewRequest(http.MethodGet, "/api/conversations/"+created.ID, nil)
	get.SetPathValue("id", created.ID)
	read := httptest.NewRecorder()
	s.handleConversationGet(read, get)
	if read.Code != http.StatusOK {
		t.Fatalf("get: %d %s", read.Code, read.Body.String())
	}
	var saved api.Conversation
	if err := json.Unmarshal(read.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	assertConversationKnowledge(t, saved, expected)
	if saved.Title != "Renamed conversation" {
		t.Fatal("unrelated update was lost")
	}
	for _, field := range []string{`"library_id"`, `"library_name"`, `"captured_at"`, `"page_ids"`, `"source_ids"`} {
		if !strings.Contains(read.Body.String(), field) {
			t.Errorf("missing snake_case field %s", field)
		}
	}
}

func assertConversationKnowledge(t *testing.T, got, expected api.Conversation) {
	t.Helper()
	if got.Settings == nil || !reflect.DeepEqual(got.Settings, expected.Settings) {
		t.Fatalf("settings changed: %#v", got.Settings)
	}
	if len(got.Messages) != 1 || !reflect.DeepEqual(got.Messages[0].Meta, expected.Messages[0].Meta) {
		t.Fatalf("message metadata changed: %#v", got.Messages)
	}
	if got.Model != expected.Model || got.Messages[0].Content != expected.Messages[0].Content {
		t.Fatal("existing conversation fields changed")
	}
}

func TestLegacyConversationOmitsKnowledgeMetadata(t *testing.T) {
	s := newTestServer(t)
	body := `{"title":"Legacy","settings":{"num_ctx":2048,"num_parallel":2},"messages":[{"role":"assistant","content":"old answer","meta":{"tokens":4}}]}`
	create := httptest.NewRecorder()
	s.handleConversationCreate(create, httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(body)))
	if create.Code != http.StatusCreated {
		t.Fatal(create.Body.String())
	}
	var created api.Conversation
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	stored, err := s.conversations.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings.Knowledge != nil || stored.Messages[0].Meta.Knowledge != nil {
		t.Fatal("legacy data gained knowledge metadata")
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"knowledge"`) {
		t.Fatal("optional knowledge is not omitted for legacy data")
	}
	if stored.Settings.NumCtx != 2048 || stored.Settings.NumParallel != 2 || stored.Messages[0].Meta.Tokens != 4 {
		t.Fatal("legacy fields changed")
	}
}
