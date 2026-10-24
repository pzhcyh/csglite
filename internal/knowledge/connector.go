package knowledge

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Connection is safe to return to the UI. Secrets are never part of Library,
// projections, backups, tool results or application logs.
type Connection struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AllowDraft bool   `json:"allowDraft"`
	Created    string `json:"created"`
	ExpiresAt  string `json:"expiresAt"`
	Revoked    bool   `json:"revoked"`
	LastUsed   string `json:"lastUsed,omitempty"`
}
type ConnectionInput struct {
	Name          string `json:"name"`
	AllowDraft    bool   `json:"allowDraft"`
	ExpiresInDays int    `json:"expiresInDays"`
}
type CreatedConnection struct {
	Connection Connection `json:"connection"`
	Token      string     `json:"token"`
}
type connectorCredential struct {
	Connection
	LibraryID string `json:"libraryId"`
	TokenHash string `json:"tokenHash"`
}
type ConnectorProvenance struct {
	ConnectionID string          `json:"connectionId"`
	TaskID       string          `json:"taskId"`
	RunID        string          `json:"runId"`
	RequestID    string          `json:"requestId"`
	RequestHash  string          `json:"requestHash"`
	Sources      []ContextSource `json:"sources"`
}
type ConnectorProposalInput struct {
	TaskID    string   `json:"taskId"`
	RunID     string   `json:"runId"`
	RequestID string   `json:"requestId"`
	Title     string   `json:"title"`
	Changes   []Change `json:"changes"`
}
type ConnectorPage struct {
	LibraryID  string          `json:"libraryId"`
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Content    string          `json:"content"`
	Version    int             `json:"version"`
	SourceIDs  []string        `json:"sourceIds"`
	Sources    []ContextSource `json:"sources"`
	Offset     int             `json:"offset"`
	NextOffset *int            `json:"nextOffset,omitempty"`
}
type ConnectorProposalResult struct {
	LibraryID  string `json:"libraryId"`
	ProposalID string `json:"proposalId"`
	Status     string `json:"status"`
	Duplicate  bool   `json:"duplicate"`
}

func (s *Store) readCredentials() ([]connectorCredential, error) {
	data, err := os.ReadFile(filepath.Join(s.root, ".connections.json"))
	if os.IsNotExist(err) {
		return []connectorCredential{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []connectorCredential
	if err = json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}
func (s *Store) saveCredentials(list []connectorCredential) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	// atomicWrite creates an owner-only file using CreateTemp (0600).
	return atomicWrite(filepath.Join(s.root, ".connections.json"), data)
}
func (s *Store) Connections(id string) ([]Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.load(id); err != nil {
		return nil, err
	}
	creds, err := s.readCredentials()
	if err != nil {
		return nil, err
	}
	result := []Connection{}
	for _, c := range creds {
		if c.LibraryID == id {
			result = append(result, c.Connection)
		}
	}
	return result, nil
}
func (s *Store) CreateConnection(id string, in ConnectionInput) (*CreatedConnection, error) {
	if err := validateTitle(in.Name); err != nil {
		return nil, err
	}
	if in.ExpiresInDays == 0 {
		in.ExpiresInDays = 30
	}
	if in.ExpiresInDays < 1 || in.ExpiresInDays > 365 {
		return nil, problem(400, "expiresInDays must be 1–365")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lib, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if !lib.Config.ClawRead || (in.AllowDraft && !lib.Config.ClawDraft) {
		return nil, problem(403, "enable the library's requested CSGClaw permissions first")
	}
	creds, err := s.readCredentials()
	if err != nil {
		return nil, err
	}
	if len(creds) >= 1000 {
		return nil, problem(409, "connection limit reached")
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return nil, err
	}
	token := "csgk_" + base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	c := Connection{ID: newID("connection"), Name: strings.TrimSpace(in.Name), AllowDraft: in.AllowDraft, Created: now(), ExpiresAt: time.Now().UTC().Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour).Format(time.RFC3339Nano)}
	creds = append(creds, connectorCredential{Connection: c, LibraryID: id, TokenHash: hex.EncodeToString(digest[:])})
	if err = s.saveCredentials(creds); err != nil {
		return nil, err
	}
	return &CreatedConnection{c, token}, nil
}
func (s *Store) RevokeConnection(id, connectionID string) (*Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.load(id); err != nil {
		return nil, err
	}
	creds, err := s.readCredentials()
	if err != nil {
		return nil, err
	}
	for i := range creds {
		if creds[i].LibraryID == id && creds[i].ID == connectionID {
			creds[i].Revoked = true
			if err = s.saveCredentials(creds); err != nil {
				return nil, err
			}
			return &creds[i].Connection, nil
		}
	}
	return nil, problem(404, "connection not found")
}
func connectionActive(c connectorCredential) bool {
	expiry, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	return err == nil && !c.Revoked && time.Now().Before(expiry)
}

// caller holds s.mu; permission check and subsequent read/write share a lock
// with revocation, withdrawal, configuration and review.
func (s *Store) authorizeConnector(token string, draft bool) (*Library, *connectorCredential, error) {
	if len(token) != 48 || !strings.HasPrefix(token, "csgk_") {
		return nil, nil, problem(401, "invalid or expired knowledge credential")
	}
	creds, err := s.readCredentials()
	if err != nil {
		return nil, nil, err
	}
	hash := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(hash[:])
	for i := range creds {
		c := &creds[i]
		if subtle.ConstantTimeCompare([]byte(c.TokenHash), []byte(hashHex)) != 1 {
			continue
		}
		if !connectionActive(*c) {
			return nil, nil, problem(401, "invalid or expired knowledge credential")
		}
		lib, err := s.load(c.LibraryID)
		if err != nil {
			return nil, nil, problem(401, "knowledge connection is no longer available")
		}
		if !lib.Config.ClawRead || (draft && (!lib.Config.ClawDraft || !c.AllowDraft)) {
			return nil, nil, problem(403, "knowledge permission is disabled")
		}
		last, _ := time.Parse(time.RFC3339Nano, c.LastUsed)
		if time.Since(last) > time.Minute {
			c.LastUsed = now()
			if err = s.saveCredentials(creds); err != nil {
				return nil, nil, err
			}
		}
		return lib, c, nil
	}
	return nil, nil, problem(401, "invalid or expired knowledge credential")
}
func (s *Store) ConnectorAccess(token string) (*Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lib, c, err := s.authorizeConnector(token, false)
	if err != nil {
		return nil, err
	}
	result := c.Connection
	result.AllowDraft = result.AllowDraft && lib.Config.ClawDraft
	return &result, nil
}
func pageForConnector(lib *Library, p Page, offset, maxRunes int) ConnectorPage {
	runes := []rune(p.Content)
	end := offset + maxRunes
	if end > len(runes) {
		end = len(runes)
	}
	out := ConnectorPage{LibraryID: lib.ID, ID: p.ID, Title: p.Title, Content: string(runes[offset:end]), Version: p.Version, SourceIDs: p.SourceIDs, Sources: []ContextSource{}, Offset: offset}
	if end < len(runes) {
		out.NextOffset = &end
	}
	for _, id := range p.SourceIDs {
		if src := activeSource(lib, id); src != nil {
			out.Sources = append(out.Sources, ContextSource{src.ID, src.Title, src.SHA256})
		}
	}
	return out
}
func (s *Store) ConnectorRead(token, pageID string, offset int) (*ConnectorPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lib, _, err := s.authorizeConnector(token, false)
	if err != nil {
		return nil, err
	}
	p := findPage(lib, pageID)
	if p == nil || !validPage(lib, *p) {
		return nil, problem(404, "published page is unavailable")
	}
	if offset < 0 || offset > utf8.RuneCountInString(p.Content) {
		return nil, problem(400, "offset is outside the page")
	}
	result := pageForConnector(lib, *p, offset, 8000)
	return &result, nil
}

// Query terms include CJK bigrams, so Chinese natural-language queries do not
// rely on spaces. This bounded lexical retrieval can later use an index.
func connectorTerms(query string) []string {
	seen := map[string]bool{}
	terms := []string{}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			terms = append(terms, v)
		}
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		add(word)
		runes := []rune(word)
		for i := 0; i+1 < len(runes); i++ {
			if unicode.Is(unicode.Han, runes[i]) && unicode.Is(unicode.Han, runes[i+1]) {
				add(string(runes[i : i+2]))
			}
		}
	}
	return terms
}
func (s *Store) ConnectorSearch(token, query string, limit int) ([]ConnectorPage, error) {
	if strings.TrimSpace(query) == "" || len(query) > 1000 || !utf8.ValidString(query) {
		return nil, problem(400, "query must contain 1–1000 UTF-8 bytes")
	}
	if limit == 0 {
		limit = 5
	}
	if limit < 1 || limit > 10 {
		return nil, problem(400, "limit must be 1–10")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lib, _, err := s.authorizeConnector(token, false)
	if err != nil {
		return nil, err
	}
	type match struct {
		page  Page
		score int
	}
	matches := []match{}
	terms := connectorTerms(query)
	for _, p := range lib.Pages {
		if !validPage(lib, p) {
			continue
		}
		score := 0
		title := strings.ToLower(p.Title)
		body := strings.ToLower(p.Content)
		for _, term := range terms {
			if strings.Contains(title, term) {
				score += 3
			}
			if strings.Contains(body, term) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, match{p, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	out := []ConnectorPage{}
	for i, m := range matches {
		if i >= limit {
			break
		}
		out = append(out, pageForConnector(lib, m.page, 0, 600))
	}
	return out, nil
}
func (s *Store) ConnectorPropose(token string, in ConnectorProposalInput) (*ConnectorProposalResult, error) {
	for _, v := range []string{in.TaskID, in.RunID, in.RequestID} {
		if strings.TrimSpace(v) == "" || len(v) > 240 || !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n") {
			return nil, problem(400, "taskId, runId and requestId must contain 1–240 UTF-8 bytes")
		}
	}
	if err := validateTitle(in.Title); err != nil {
		return nil, err
	}
	// Normalize omitted source IDs before hashing, so retries use the same key.
	for i := range in.Changes {
		if in.Changes[i].SourceIDs == nil {
			in.Changes[i].SourceIDs = []string{}
		}
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if len(data) > 4*1024*1024 {
		return nil, problem(400, "proposal exceeds 4 MiB")
	}
	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	lib, c, err := s.authorizeConnector(token, true)
	if err != nil {
		return nil, err
	}
	for _, p := range lib.Proposals {
		if p.Connection != nil && p.Connection.ConnectionID == c.ID && p.Connection.RequestID == in.RequestID {
			if p.Connection.RequestHash != hashHex {
				return nil, problem(409, "requestId already used for different content")
			}
			return &ConnectorProposalResult{lib.ID, p.ID, p.Status, true}, nil
		}
	}
	if err = validateChanges(lib, in.Changes); err != nil {
		return nil, err
	}
	sources := []ContextSource{}
	seen := map[string]bool{}
	for _, ch := range in.Changes {
		for _, id := range ch.SourceIDs {
			if !seen[id] {
				src := activeSource(lib, id)
				sources = append(sources, ContextSource{src.ID, src.Title, src.SHA256})
				seen[id] = true
			}
		}
	}
	p := Proposal{ID: newID("proposal"), Title: in.Title, Kind: "claw", Status: "pending", Created: now(), Changes: in.Changes, Connection: &ConnectorProvenance{c.ID, in.TaskID, in.RunID, in.RequestID, hashHex, sources}}
	lib.Proposals = append(lib.Proposals, p)
	addLog(lib, "CSGClaw 提交待审提案："+in.Title)
	if err = s.persist(lib); err != nil {
		return nil, err
	}
	return &ConnectorProposalResult{lib.ID, p.ID, p.Status, false}, nil
}

// Called from Review under the store lock; restored backups cannot authorize
// old proposals because the new library has no matching connection credential.
func (s *Store) validateConnectorProposal(lib *Library, p *Proposal) error {
	creds, err := s.readCredentials()
	if err != nil {
		return err
	}
	for _, c := range creds {
		if c.ID == p.Connection.ConnectionID && c.LibraryID == lib.ID && connectionActive(c) && c.AllowDraft && lib.Config.ClawRead && lib.Config.ClawDraft {
			for _, src := range p.Connection.Sources {
				current := activeSource(lib, src.ID)
				if current == nil || current.SHA256 != src.SHA256 {
					return problem(409, "proposal source changed or was withdrawn")
				}
			}
			return nil
		}
	}
	return problem(403, "proposal connection was revoked, expired or is unavailable")
}
