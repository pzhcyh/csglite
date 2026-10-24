// Package knowledge implements a local, source-preserving Markdown Wiki.
package knowledge

import "fmt"

const MaxTextBytes = 2 * 1024 * 1024

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }
func problem(status int, format string, args ...any) error {
	return &Error{status, fmt.Sprintf(format, args...)}
}

type Config struct {
	Model       string `json:"model"`
	ModelSource string `json:"modelSource"`
	ClawRead    bool   `json:"clawRead"`
	ClawDraft   bool   `json:"clawDraft"`
}
type Source struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Filename string `json:"filename"`
	Content  string `json:"content"`
	SHA256   string `json:"sha256"`
	Status   string `json:"status"`
	Created  string `json:"created"`
}
type Revision struct {
	Version   int      `json:"version"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	SourceIDs []string `json:"sourceIds"`
	Updated   string   `json:"updated"`
}
type Page struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	SourceIDs []string   `json:"sourceIds"`
	Version   int        `json:"version"`
	Updated   string     `json:"updated"`
	History   []Revision `json:"history"`
	Stale     bool       `json:"stale"`
}
type Change struct {
	PageID      string   `json:"pageId"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	SourceIDs   []string `json:"sourceIds"`
	BaseVersion int      `json:"baseVersion"`
}
type Proposal struct {
	ID         string               `json:"id"`
	Title      string               `json:"title"`
	Kind       string               `json:"kind"`
	Status     string               `json:"status"`
	Changes    []Change             `json:"changes"`
	Created    string               `json:"created"`
	Connection *ConnectorProvenance `json:"connection,omitempty"`
}
type Log struct {
	ID      string `json:"id"`
	Time    string `json:"time"`
	Message string `json:"message"`
}
type Library struct {
	StewardJobs []StewardJob `json:"stewardJobs,omitempty"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Config      Config       `json:"config"`
	Sources     []Source     `json:"sources"`
	Pages       []Page       `json:"pages"`
	Proposals   []Proposal   `json:"proposals"`
	Logs        []Log        `json:"logs"`
}
type SourceInput struct {
	Title    string `json:"title"`
	Filename string `json:"filename"`
	Content  string `json:"content"`
}
type PageInput struct {
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	SourceIDs       []string `json:"sourceIds"`
	ExpectedVersion int      `json:"expectedVersion"`
}
type ProposalInput struct {
	Title   string   `json:"title"`
	Kind    string   `json:"kind"`
	Changes []Change `json:"changes"`
}
