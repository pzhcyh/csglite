package server

import (
	"net/http"
	"strings"
)

// Local management can contain private originals, derived answers, and full
// inference request bodies. Keep all of those records behind the same boundary
// without tying access to any individual feature store's availability.
func localManagementOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Del("Access-Control-Allow-Origin")
		w.Header().Del("Access-Control-Allow-Credentials")
		w.Header().Set("Cache-Control", "no-store")
		if !isLoopbackRequest(r) || !isDesktopLoopbackHost(r.Host) || !isAllowedDesktopOrigin(r) {
			writeError(w, http.StatusForbidden, "management records are available only from the local application origin")
			return
		}
		next(w, r)
	}
}

func isLocalManagementPath(path string) bool {
	for _, prefix := range []string{"/api/knowledge", "/api/conversations", "/api/observability"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
