package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencsgs/csglite/internal/knowledge"
)

func TestKnowledgeStewardCloudRunUsesNativeSourceAndCreatesOnlyPendingDraft(t *testing.T) {
	s := newTestServer(t)
	s.cfg.OpenCSGAPIKey = "fictional-steward-test-token"
	lib, err := s.knowledge.Create("虚构管家 API 测试", "")
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.knowledge.AddSource(lib.ID, knowledge.SourceInput{Title: "虚构记录", Filename: "fiction.md", Content: "虚构阅读活动有 12 名参与者。"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.knowledge.Configure(lib.ID, knowledge.Config{Model: "steward/cloud", ModelSource: "cloud"})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := newCloudCatalogAndInferenceServer(t, "steward/cloud", "text-generation", "/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fictional-steward-test-token" {
			t.Error("native credential channel not used")
		}
		var payload struct {
			Model     string                           `json:"model"`
			Stream    bool                             `json:"stream"`
			MaxTokens int                              `json:"max_tokens"`
			Messages  []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "steward/cloud" || !payload.Stream || payload.MaxTokens != 6000 || len(payload.Messages) != 2 {
			t.Errorf("inference contract %+v", payload)
		}
		if len(payload.Messages) == 2 && !strings.Contains(payload.Messages[1].Content, lib.Sources[0].ID) {
			t.Error("source context lost")
		}
		proposal, _ := json.Marshal(map[string]any{"changes": []map[string]string{{"pageId": "", "title": "虚构阅读活动", "content": "参与者共 12 人。[source:" + lib.Sources[0].ID + "]"}}})
		event, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": string(proposal)}, "finish_reason": "stop"}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
	})
	defer upstream.Close()
	attachCloudTestService(s, upstream.URL)
	base := "/api/knowledge/libraries/" + lib.ID + "/steward"
	w := knowledgeRequest(s, "POST", base+"/jobs", `{"sourceIds":["`+lib.Sources[0].ID+`"]}`)
	var job knowledge.StewardJob
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
		t.Fatalf("queue %d %s", w.Code, w.Body.String())
	}
	w = knowledgeRequest(s, "POST", base+"/jobs/"+job.ID+"/run", `{}`)
	var result knowledge.StewardResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Job.Status != "completed" {
		t.Fatalf("run %d %s", w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("cloud route calls=%d", calls.Load())
	}
	current, _ := s.knowledge.Get(lib.ID)
	if len(current.Pages) != 0 || len(current.Proposals) != 1 || current.Proposals[0].Status != "pending" {
		t.Fatal("generated content published without review")
	}
	w = knowledgeRequest(s, "POST", base+"/jobs/"+job.ID+"/run", `{}`)
	if w.Code != 409 || calls.Load() != 1 {
		t.Fatal("repeated run called upstream")
	}
	w = knowledgeRequest(s, "GET", base, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"issues"`) || strings.Contains(w.Body.String(), "fictional-steward-test-token") {
		t.Fatal("state endpoint failed or exposed token")
	}
}

func TestKnowledgeStewardModelFailureIsActionableAndSanitized(t *testing.T) {
	s := newTestServer(t)
	s.cfg.OpenCSGAPIKey = "fictional-steward-test-token"
	lib, _ := s.knowledge.Create("虚构失败测试", "")
	lib, _ = s.knowledge.AddSource(lib.ID, knowledge.SourceInput{Title: "虚构资料", Filename: "fiction.md", Content: "虚构记录"})
	lib, _ = s.knowledge.Configure(lib.ID, knowledge.Config{Model: "steward/cloud", ModelSource: "cloud"})
	upstream := newCloudCatalogAndInferenceServer(t, "steward/cloud", "text-generation", "/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"secret-provider-key at /private/path"}}`)
	})
	defer upstream.Close()
	attachCloudTestService(s, upstream.URL)
	job, err := s.knowledge.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/knowledge/libraries/" + lib.ID + "/steward/jobs/" + job.ID
	w := knowledgeRequest(s, "POST", base+"/run", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"failed"`) || strings.Contains(w.Body.String(), "secret-provider") || strings.Contains(w.Body.String(), "/private/") {
		t.Fatalf("unsafe or unreported model failure: %s", w.Body.String())
	}
	w = knowledgeRequest(s, "POST", base+"/retry", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"queued"`) {
		t.Fatalf("retry %s", w.Body.String())
	}
	w = knowledgeRequest(s, "POST", base+"/cancel", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel %s", w.Body.String())
	}
}

func TestKnowledgeStewardManagementGuard(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/api/knowledge/libraries/lib-invalid/steward", "/api/knowledge/libraries/lib-invalid/steward/jobs"} {
		method := "GET"
		if strings.HasSuffix(path, "/jobs") {
			method = "POST"
		}
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(`{}`))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "https://foreign.example")
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("foreign browser reached steward: %d", w.Code)
		}
		w = httptest.NewRecorder()
		s.externalAPIRoutes().ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatal("inference listener exposed steward")
		}
	}
}

func TestStewardResponseAndStreamBudgets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &stewardResponse{header: http.Header{}, limit: 10, cancel: cancel}
	if _, err := w.Write([]byte(strings.Repeat("x", 11))); err == nil || !w.overflow || ctx.Err() == nil || w.body.Len() != 0 {
		t.Fatal("response was not bounded/cancelled")
	}
	for _, raw := range []string{
		`data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n",
		`data: {"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"length"}]}` + "\n\ndata: [DONE]\n\n",
		`data: {"error":{"message":"sensitive provider error"}}` + "\n\ndata: [DONE]\n\n",
	} {
		if _, err := parseStewardStream([]byte(raw)); err == nil {
			t.Fatal("accepted invalid/incomplete model stream")
		}
	}
}
