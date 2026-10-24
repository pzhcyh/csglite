package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/knowledge"
)

func knowledgeRequest(s *Server, method, url, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+url, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}

func TestKnowledgeOriginGuard(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		name, host, peer, origin string
		want                     int
	}{
		{"same origin", "localhost", "127.0.0.1:1234", "http://localhost", 200},
		{"local CLI", "127.0.0.1", "127.0.0.1:1234", "", 200},
		{"IPv6 local", "[::1]", "[::1]:1234", "", 200},
		{"remote peer", "localhost", "10.0.0.4:1234", "", 403},
		{"DNS rebinding", "attacker.example", "127.0.0.1:1234", "", 403},
		{"foreign site", "localhost", "127.0.0.1:1234", "https://attacker.example", 403},
		{"null origin", "localhost", "127.0.0.1:1234", "null", 403},
		{"wrong scheme", "localhost", "127.0.0.1:1234", "https://localhost", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/api/knowledge", nil)
			r.RemoteAddr = tc.peer
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("knowledge exposes cross-origin read")
			}
		})
	}
	// The fixed desktop inference listener must never serve management data.
	r := httptest.NewRequest(http.MethodGet, "http://localhost/api/knowledge", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.externalAPIRoutes().ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("knowledge exposed on inference listener")
	}
}

func TestKnowledgeAPIEndToEnd(t *testing.T) {
	s := newTestServer(t)
	w := knowledgeRequest(s, "GET", "/api/knowledge", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"libraries":[]`) {
		t.Fatalf("initial list: %s", w.Body.String())
	}
	w = knowledgeRequest(s, "POST", "/api/knowledge/libraries", `{"name":"虚构 API 验证"}`)
	var lib knowledge.Library
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	base := "/api/knowledge/libraries/" + lib.ID
	w = knowledgeRequest(s, "POST", base+"/demo", `{}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	w = knowledgeRequest(s, "POST", base+"/proposals/"+lib.Proposals[0].ID+"/accept", `{}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	if lib.Pages[1].Version != 2 {
		t.Fatal("accept endpoint did not save")
	}
	w = knowledgeRequest(s, "GET", base+"/export?includeRaw=false", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" || !bytes.HasPrefix(w.Body.Bytes(), []byte("PK")) {
		t.Fatal("missing ZIP")
	}
	w = knowledgeRequest(s, "GET", base+"/context", "")
	if w.Code != 403 {
		t.Fatal("context permission ignored")
	}
	w = knowledgeRequest(s, "PUT", base+"/config", `{"model":"","modelSource":"local","clawRead":true,"clawDraft":false}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = knowledgeRequest(s, "GET", base+"/context", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"history"`) {
		t.Fatal("context endpoint leaked history or failed")
	}
}

func TestKnowledgeAPIRejectsInvalidBodies(t *testing.T) {
	s := newTestServer(t)
	for _, body := range []string{`{"name":"one"} {"name":"two"}`, `{"name":"one","unknown":true}`, string([]byte{'{', '"', 'n', 'a', 'm', 'e', '"', ':', '"', 255, '"', '}'}), strings.Repeat(" ", 8*1024*1024+1)} {
		w := knowledgeRequest(s, "POST", "/api/knowledge/libraries", body)
		if w.Code != 400 {
			t.Fatalf("malformed body accepted: %d", w.Code)
		}
	}
	w := knowledgeRequest(s, "GET", "/api/knowledge", "")
	if !strings.Contains(w.Body.String(), `"libraries":[]`) {
		t.Fatal("invalid request mutated storage")
	}
}
