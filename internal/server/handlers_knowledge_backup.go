package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/opencsgs/csglite/internal/knowledge"
)

func (s *Server) handleKnowledgeBackup(w http.ResponseWriter, r *http.Request) {
	backup, err := s.knowledge.Backup(r.PathValue("library"))
	if err != nil {
		knowledgeResult(w, nil, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="csglite-knowledge-backup.json"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	knowledgeResult(w, backup, nil)
}

func (s *Server) handleKnowledgeRestoreBackup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, knowledge.MaxBackupBytes+1024*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var in struct {
		Backup knowledge.Backup `json:"backup"`
	}
	if err := d.Decode(&in); err != nil {
		writeError(w, 400, "invalid or oversized backup request")
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeError(w, 400, "one backup object is required")
		return
	}
	lib, err := s.knowledge.RestoreBackup(in.Backup)
	knowledgeResult(w, lib, err)
}
