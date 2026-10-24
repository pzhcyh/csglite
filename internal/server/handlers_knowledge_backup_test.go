package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/knowledge"
)

func TestKnowledgeBackupRoundTripAPI(t *testing.T) {
	s := newTestServer(t)
	lib, err := s.knowledge.Create("Backup API fictional", "")
	if err != nil {
		t.Fatal(err)
	}
	lib, err = s.knowledge.SeedDemo(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := knowledgeRequest(s, "GET", "/api/knowledge/libraries/"+lib.ID+"/backup", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(w.Code, w.Body.String())
	}
	var backup knowledge.Backup
	if err = json.Unmarshal(w.Body.Bytes(), &backup); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"backup": backup})
	w = knowledgeRequest(s, "POST", "/api/knowledge/restore", string(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var restored knowledge.Library
	if err = json.Unmarshal(w.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.ID == lib.ID || len(restored.Sources) != len(lib.Sources) {
		t.Fatal("restore failed")
	}
	w = knowledgeRequest(s, "POST", "/api/knowledge/restore", `{"backup":{}}`)
	if w.Code != 400 {
		t.Fatal("invalid backup accepted")
	}
}
