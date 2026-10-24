package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func managementRequest(handler http.Handler, method, path, host, peer, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(`{}`))
	r.RemoteAddr = peer
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if method == http.MethodOptions {
		r.Header.Set("Access-Control-Request-Method", "GET")
		r.Header.Set("Access-Control-Request-Headers", "content-type")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func assertPrivateManagementHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("private management grants CORS: %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("private management grants credentials: %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestLocalManagementRejectsForeignOriginsHostsAndPeers(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()
	endpoints := []struct{ method, path string }{
		{"GET", "/api/conversations"},
		{"POST", "/api/conversations"},
		{"GET", "/api/conversations/search?q=private"},
		{"GET", "/api/conversations/private-id"},
		{"PUT", "/api/conversations/private-id"},
		{"DELETE", "/api/conversations/private-id"},
		{"GET", "/api/observability/requests"},
		{"GET", "/api/observability/requests/private-id"},
		{"GET", "/api/observability/facets"},
		{"GET", "/api/observability/traces"},
		{"GET", "/api/observability/traces/private-id"},
		{"POST", "/api/observability/dataset-exports/preview"},
		{"POST", "/api/observability/dataset-exports"},
		{"GET", "/api/observability/dataset-export-jobs/private-id"},
		{"GET", "/api/observability/dataset-exports/private-id/download"},
		{"DELETE", "/api/observability"},
		{"GET", "/api/knowledge"},
	}
	callers := []struct{ name, host, peer, origin string }{
		{"foreign origin", "localhost", "127.0.0.1:1010", "https://attacker.example"},
		{"null origin", "localhost", "127.0.0.1:1010", "null"},
		{"foreign host", "attacker.example", "127.0.0.1:1010", ""},
		{"remote peer", "localhost", "192.0.2.2:1010", ""},
	}
	for _, endpoint := range endpoints {
		for _, caller := range callers {
			t.Run(endpoint.method+endpoint.path+"/"+caller.name, func(t *testing.T) {
				w := managementRequest(handler, endpoint.method, endpoint.path, caller.host, caller.peer, caller.origin)
				if w.Code != http.StatusForbidden {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
				assertPrivateManagementHeaders(t, w)
			})
		}
	}
}

func TestLocalManagementWorksWithoutKnowledgeStore(t *testing.T) {
	s := newTestServer(t)
	s.knowledge = nil
	handler := s.routes()
	for _, path := range []string{"/api/conversations", "/api/conversations/search", "/api/observability/requests", "/api/observability/facets", "/api/observability/traces"} {
		for _, caller := range []struct{ host, peer, origin string }{
			{"localhost", "127.0.0.1:1010", "http://localhost"},
			{"127.0.0.1", "127.0.0.1:1010", ""},
			{"[::1]", "[::1]:1010", ""},
		} {
			w := managementRequest(handler, "GET", path, caller.host, caller.peer, caller.origin)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
			assertPrivateManagementHeaders(t, w)
		}
	}
	w := managementRequest(handler, "GET", "/api/knowledge", "localhost", "127.0.0.1:1010", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing knowledge storage status = %d", w.Code)
	}
}

func TestLocalManagementPreflightAndExternalListener(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()
	for _, path := range []string{"/api/knowledge", "/api/conversations", "/api/conversations/search", "/api/observability/requests", "/api/observability/dataset-exports/example/download"} {
		w := managementRequest(handler, "OPTIONS", path, "localhost", "127.0.0.1:1010", "https://attacker.example")
		if w.Code != http.StatusForbidden {
			t.Fatalf("foreign preflight %s: %d", path, w.Code)
		}
		assertPrivateManagementHeaders(t, w)
		w = managementRequest(handler, "OPTIONS", path, "localhost", "127.0.0.1:1010", "http://localhost")
		if w.Code != http.StatusNoContent {
			t.Fatalf("local preflight %s: %d", path, w.Code)
		}
		assertPrivateManagementHeaders(t, w)
		w = managementRequest(s.externalAPIRoutes(), "GET", path, "localhost", "127.0.0.1:1010", "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("management exposed externally %s: %d", path, w.Code)
		}
	}
	// Keep the existing public/inference CORS policy outside these management
	// families. This patch must not close model endpoints to existing clients.
	w := managementRequest(handler, "GET", "/api/health", "localhost", "192.0.2.2:1010", "https://client.example")
	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("unrelated CORS changed: %d %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
}
