package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/opencsgs/csglite/internal/knowledge"
)

// The shared management guard runs before checking feature availability.
func (s *Server) knowledgeOnly(next http.HandlerFunc) http.HandlerFunc {
	return localManagementOnly(func(w http.ResponseWriter, r *http.Request) {
		if s.knowledge == nil {
			writeError(w, http.StatusServiceUnavailable, "knowledge storage is unavailable")
			return
		}
		next(w, r)
	})
}
func knowledgeDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	// JSON may escape every source byte; bound the envelope and check decoded
	// UTF-8 content in the store separately. Max 50 changes of bounded text.
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(body) {
		writeError(w, http.StatusBadRequest, "request must be bounded UTF-8 JSON")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid or oversized JSON request")
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return false
	}
	return true
}
func knowledgeResult(w http.ResponseWriter, result any, err error) {
	if err != nil {
		var e *knowledge.Error
		if errors.As(err, &e) {
			writeError(w, e.Status, e.Message)
		} else {
			writeError(w, http.StatusInternalServerError, "knowledge storage operation failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *Server) handleKnowledgeList(w http.ResponseWriter, r *http.Request) {
	libs, err := s.knowledge.List()
	knowledgeResult(w, map[string]any{"libraries": libs}, err)
}
func (s *Server) handleKnowledgeCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !knowledgeDecode(w, r, &in) {
		return
	}
	lib, err := s.knowledge.Create(in.Name, in.Description)
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeGet(w http.ResponseWriter, r *http.Request) {
	lib, err := s.knowledge.Get(r.PathValue("library"))
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeDemo(w http.ResponseWriter, r *http.Request) {
	lib, err := s.knowledge.SeedDemo(r.PathValue("library"))
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeAddSource(w http.ResponseWriter, r *http.Request) {
	var in knowledge.SourceInput
	if !knowledgeDecode(w, r, &in) {
		return
	}
	lib, err := s.knowledge.AddSource(r.PathValue("library"), in)
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeWithdraw(w http.ResponseWriter, r *http.Request) {
	lib, err := s.knowledge.Withdraw(r.PathValue("library"), r.PathValue("source"))
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeSavePage(w http.ResponseWriter, r *http.Request) {
	var in knowledge.PageInput
	if !knowledgeDecode(w, r, &in) {
		return
	}
	lib, err := s.knowledge.SavePage(r.PathValue("library"), r.PathValue("page"), in)
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeRestore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version         int `json:"version"`
		ExpectedVersion int `json:"expectedVersion"`
	}
	if !knowledgeDecode(w, r, &in) {
		return
	}
	lib, err := s.knowledge.Restore(r.PathValue("library"), r.PathValue("page"), in.Version, in.ExpectedVersion)
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgePropose(w http.ResponseWriter, r *http.Request) {
	var in knowledge.ProposalInput
	if !knowledgeDecode(w, r, &in) {
		return
	}
	lib, err := s.knowledge.Propose(r.PathValue("library"), in)
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeAccept(w http.ResponseWriter, r *http.Request) {
	lib, err := s.knowledge.Review(r.PathValue("library"), r.PathValue("proposal"), true)
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeReject(w http.ResponseWriter, r *http.Request) {
	lib, err := s.knowledge.Review(r.PathValue("library"), r.PathValue("proposal"), false)
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeConfig(w http.ResponseWriter, r *http.Request) {
	var in knowledge.Config
	if !knowledgeDecode(w, r, &in) {
		return
	}
	lib, err := s.knowledge.Configure(r.PathValue("library"), in)
	knowledgeResult(w, lib, err)
}
func (s *Server) handleKnowledgeContext(w http.ResponseWriter, r *http.Request) {
	result, err := s.knowledge.Context(r.PathValue("library"))
	knowledgeResult(w, result, err)
}
func (s *Server) handleKnowledgeExport(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("includeRaw")
	if raw != "" && raw != "true" && raw != "false" {
		writeError(w, http.StatusBadRequest, "includeRaw must be true or false")
		return
	}
	data, err := s.knowledge.Export(r.PathValue("library"), raw == "true")
	if err != nil {
		knowledgeResult(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="knowledge-snapshot.zip"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
