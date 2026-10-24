package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/knowledge"
)

func connectorServerFixture(t *testing.T) (*Server, *knowledge.Library, *knowledge.CreatedConnection) {
	t.Helper()
	s := newTestServer(t)
	lib, err := s.knowledge.Create("虚构 MCP 测试", "")
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.knowledge.AddSource(lib.ID, knowledge.SourceInput{Title: "虚构来源", Filename: "demo.md", Content: "PRIVATE ORIGINAL"})
	if err != nil {
		t.Fatal(err)
	}
	src := lib.Sources[0].ID
	lib, err = s.knowledge.SavePage(lib.ID, "", knowledge.PageInput{Title: "虚构任务", Content: "已审 Wiki [source:" + src + "]", SourceIDs: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.knowledge.Configure(lib.ID, knowledge.Config{ClawRead: true, ClawDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.knowledge.CreateConnection(lib.ID, knowledge.ConnectionInput{Name: "MCP 测试 Agent", AllowDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, lib, c
}
func connectorRPC(s *Server, token, body string, alter func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://localhost/api/knowledge/mcp", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:4444"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", knowledgeMCPVersion)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if alter != nil {
		alter(r)
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}
func rpcBody(method string, params any) string {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return string(body)
}
func TestKnowledgeMCPProtocolAndTools(t *testing.T) {
	s, lib, c := connectorServerFixture(t)
	w := connectorRPC(s, c.Token, rpcBody("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}), func(r *http.Request) { r.Header.Set("MCP-Protocol-Version", "2025-11-25") })
	if w.Code != 200 || !strings.Contains(w.Body.String(), knowledgeMCPVersion) || w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("init", w.Code, w.Body.String())
	}
	w = connectorRPC(s, c.Token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if w.Code != 202 || w.Body.Len() != 0 {
		t.Fatal("notification", w.Code, w.Body.String())
	}
	w = connectorRPC(s, c.Token, rpcBody("tools/list", map[string]any{}), nil)
	for _, name := range []string{"knowledge_search", "knowledge_read", "knowledge_propose"} {
		if !strings.Contains(w.Body.String(), name) {
			t.Fatal("missing tool", name, w.Body.String())
		}
	}
	w = connectorRPC(s, c.Token, rpcBody("tools/call", map[string]any{"name": "knowledge_search", "arguments": map[string]any{"query": "虚构任务"}}), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), lib.Pages[0].ID) || strings.Contains(w.Body.String(), "PRIVATE ORIGINAL") {
		t.Fatal("search", w.Code, w.Body.String())
	}
	w = connectorRPC(s, c.Token, rpcBody("tools/call", map[string]any{"name": "knowledge_read", "arguments": map[string]any{"pageId": lib.Pages[0].ID}}), nil)
	if !strings.Contains(w.Body.String(), `"structuredContent"`) || strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal("read", w.Body.String())
	}
	args := knowledge.ConnectorProposalInput{TaskID: "test-task", RunID: "test-run", RequestID: "test-request", Title: "虚构成果", Changes: []knowledge.Change{{Title: "虚构新页面", Content: "虚构成果", SourceIDs: []string{lib.Sources[0].ID}}}}
	body := rpcBody("tools/call", map[string]any{"name": "knowledge_propose", "arguments": args})
	w = connectorRPC(s, c.Token, body, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"pending"`) {
		t.Fatal("propose", w.Code, w.Body.String())
	}
	w = connectorRPC(s, c.Token, body, nil)
	if !strings.Contains(w.Body.String(), `"duplicate":true`) {
		t.Fatal("duplicate", w.Body.String())
	}
	pending, err := s.knowledge.Get(lib.ID)
	if err != nil || len(pending.Proposals) != 1 || len(pending.Pages) != 1 {
		t.Fatal("unexpected publish", err)
	}
	if _, err = s.knowledge.RevokeConnection(lib.ID, c.Connection.ID); err != nil {
		t.Fatal(err)
	}
	w = connectorRPC(s, c.Token, rpcBody("tools/list", nil), nil)
	if w.Code != 401 {
		t.Fatal("revoked token accepted", w.Code)
	}
	reviewed := knowledgeRequest(s, "POST", "/api/knowledge/libraries/"+lib.ID+"/proposals/"+pending.Proposals[0].ID+"/accept", "{}")
	if reviewed.Code != 403 {
		t.Fatal("revoked proposal accepted", reviewed.Code, reviewed.Body.String())
	}
}
func TestKnowledgeMCPGuardsAndReadOnly(t *testing.T) {
	s, lib, c := connectorServerFixture(t)
	for _, tc := range []struct {
		name  string
		token string
		alter func(*http.Request)
		want  int
	}{
		{"missing token", "", nil, 401},
		{"bad token", "csgk_invalid", nil, 401},
		{"remote peer", c.Token, func(r *http.Request) { r.RemoteAddr = "10.0.0.1:12" }, 403},
		{"rebound host", c.Token, func(r *http.Request) { r.Host = "attacker.example" }, 403},
		{"cross origin", c.Token, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403},
		{"unsupported version", c.Token, func(r *http.Request) { r.Header.Set("MCP-Protocol-Version", "1999-01-01") }, 400},
		{"invalid content type", c.Token, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"invalid accept", c.Token, func(r *http.Request) { r.Header.Set("Accept", "text/html") }, 406},
		{"GET no SSE", c.Token, func(r *http.Request) { r.Method = "GET" }, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := connectorRPC(s, tc.token, rpcBody("ping", nil), tc.alter)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("unexpected CORS")
			}
		})
	}
	ro, err := s.knowledge.CreateConnection(lib.ID, knowledge.ConnectionInput{Name: "只读"})
	if err != nil {
		t.Fatal(err)
	}
	w := connectorRPC(s, ro.Token, rpcBody("tools/list", nil), nil)
	if strings.Contains(w.Body.String(), "knowledge_propose") {
		t.Fatal("write tool discovered by read-only client")
	}
	args := knowledge.ConnectorProposalInput{TaskID: "task", RunID: "run", RequestID: "key", Title: "虚构稿", Changes: []knowledge.Change{{Title: "New", Content: "x"}}}
	w = connectorRPC(s, ro.Token, rpcBody("tools/call", map[string]any{"name": "knowledge_propose", "arguments": args}), nil)
	if !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal("write permission bypass", w.Body.String())
	}
	w = connectorRPC(s, c.Token, rpcBody("tools/call", map[string]any{"name": "knowledge_read", "arguments": map[string]any{"pageId": lib.Pages[0].ID, "libraryId": "another"}}), nil)
	if !strings.Contains(w.Body.String(), `"code":-32602`) {
		t.Fatal("unknown scope field not rejected")
	}
	if _, err = s.knowledge.Withdraw(lib.ID, lib.Sources[0].ID); err != nil {
		t.Fatal(err)
	}
	w = connectorRPC(s, c.Token, rpcBody("tools/call", map[string]any{"name": "knowledge_read", "arguments": map[string]any{"pageId": lib.Pages[0].ID}}), nil)
	if !strings.Contains(w.Body.String(), `"isError":true`) || strings.Contains(w.Body.String(), "已审 Wiki") {
		t.Fatal("stale page leaked")
	}
	// The dedicated external inference listener never registers the MCP endpoint.
	r := httptest.NewRequest("POST", "http://localhost/api/knowledge/mcp", strings.NewReader(rpcBody("ping", nil)))
	r.RemoteAddr = "127.0.0.1:44"
	r.Header.Set("Authorization", "Bearer "+c.Token)
	out := httptest.NewRecorder()
	s.externalAPIRoutes().ServeHTTP(out, r)
	if out.Code == 200 || out.Code == 202 {
		t.Fatal("MCP reachable via external inference listener")
	}
}
func TestKnowledgeConnectionManagementAndDesktopBoundary(t *testing.T) {
	s, lib, c := connectorServerFixture(t)
	base := "/api/knowledge/libraries/" + lib.ID + "/connections"
	w := knowledgeRequest(s, "GET", base, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), c.Token) || strings.Contains(w.Body.String(), "tokenHash") {
		t.Fatal("credential exposed", w.Code, w.Body.String())
	}
	w = knowledgeRequest(s, "POST", base, `{"name":"另一位 Agent","allowDraft":false,"expiresInDays":7}`)
	var created knowledge.CreatedConnection
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Token == "" {
		t.Fatal("create", w.Code, w.Body.String())
	}
	w = knowledgeRequest(s, "POST", base+"/"+created.Connection.ID+"/revoke", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"revoked":true`) {
		t.Fatal("revoke", w.Code, w.Body.String())
	}
	s.cfg.DesktopMode = true
	s.cfg.DesktopSessionToken = "unrelated-desktop-session"
	w = connectorRPC(s, c.Token, rpcBody("ping", nil), nil)
	if w.Code != 200 {
		t.Fatal("MCP must use its own scoped credential in desktop mode", w.Code, w.Body.String())
	}
	w = knowledgeRequest(s, "GET", base, "")
	if w.Code != 401 {
		t.Fatal("MCP exemption leaked into management", w.Code, w.Body.String())
	}
}
