package knowledge

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var wikiLink = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)

type ContextPage struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Version   int      `json:"version"`
	SourceIDs []string `json:"sourceIds"`
}
type ContextSource struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	SHA256 string `json:"sha256"`
}
type Context struct {
	LibraryID string          `json:"libraryId"`
	Name      string          `json:"name"`
	Pages     []ContextPage   `json:"pages"`
	Sources   []ContextSource `json:"sources"`
}

func validPage(lib *Library, p Page) bool {
	return !p.Stale && validateRefs(lib, p.Content, p.SourceIDs) == nil
}
func (s *Store) Context(id string) (*Context, error) {
	lib, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if !lib.Config.ClawRead {
		return nil, problem(403, "CSGClaw read permission is disabled")
	}
	result := &Context{LibraryID: id, Name: lib.Name, Pages: []ContextPage{}, Sources: []ContextSource{}}
	used := map[string]bool{}
	for _, p := range lib.Pages {
		if !validPage(lib, p) {
			continue
		}
		result.Pages = append(result.Pages, ContextPage{p.ID, p.Title, p.Content, p.Version, p.SourceIDs})
		for _, ref := range p.SourceIDs {
			used[ref] = true
		}
	}
	for _, src := range lib.Sources {
		if used[src.ID] && src.Status == "active" {
			result.Sources = append(result.Sources, ContextSource{src.ID, src.Title, src.SHA256})
		}
	}
	return result, nil
}
func (s *Store) Export(id string, includeRaw bool) ([]byte, error) {
	lib, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	pages := []Page{}
	titles := map[string]string{}
	used := map[string]bool{}
	for _, p := range lib.Pages {
		if validPage(lib, p) {
			pages = append(pages, p)
			titles[p.Title] = p.ID
			for _, ref := range p.SourceIDs {
				used[ref] = true
			}
		}
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	var index strings.Builder
	fmt.Fprintf(&index, "# %s\n\n固定版本快照；不含待审提案、个人记忆或模型配置。\n\n", lib.Name)
	manifest := struct {
		LibraryID  string          `json:"libraryId"`
		Name       string          `json:"name"`
		Created    string          `json:"created"`
		IncludeRaw bool            `json:"includeRaw"`
		Pages      []ContextPage   `json:"pages"`
		Sources    []ContextSource `json:"sources"`
	}{lib.ID, lib.Name, now(), includeRaw, []ContextPage{}, []ContextSource{}}
	for _, p := range pages {
		body := mapMarkdownText(p.Content, func(prose string) string {
			prose = wikiLink.ReplaceAllStringFunc(prose, func(link string) string {
				inside := strings.TrimSuffix(strings.TrimPrefix(link, "[["), "]]")
				target, label, hasAlias := strings.Cut(inside, "|")
				if !hasAlias {
					label = target
				}
				target, _, _ = strings.Cut(target, "#")
				if pageID, ok := titles[strings.TrimSpace(target)]; ok {
					return "[" + strings.ReplaceAll(label, "]", "\\]") + "](" + pageID + ".md)"
				}
				return label
			})
			prose = sourceCitation.ReplaceAllStringFunc(prose, func(cite string) string {
				ref := sourceCitation.FindStringSubmatch(cite)[1]
				if includeRaw {
					src := activeSource(lib, ref)
					if src != nil {
						return "[来源：" + src.Title + "](../raw/" + ref + strings.ToLower(path.Ext(src.Filename)) + ")"
					}
				}
				return "[来源 " + ref + "]"
			})
			return prose
		})
		if err = add("wiki/"+p.ID+".md", pageMarkdown(p.Title, body, p.Version, p.SourceIDs)); err != nil {
			return nil, err
		}
		fmt.Fprintf(&index, "- [%s](wiki/%s.md) · v%d\n", p.Title, p.ID, p.Version)
		manifest.Pages = append(manifest.Pages, ContextPage{p.ID, p.Title, "", p.Version, p.SourceIDs})
	}
	for _, src := range lib.Sources {
		if !used[src.ID] || src.Status != "active" {
			continue
		}
		manifest.Sources = append(manifest.Sources, ContextSource{src.ID, src.Title, src.SHA256})
		if includeRaw {
			if err = add("raw/"+src.ID+strings.ToLower(path.Ext(src.Filename)), []byte(src.Content)); err != nil {
				return nil, err
			}
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = add("manifest.json", data); err != nil {
		return nil, err
	}
	if err = add("index.md", []byte(index.String())); err != nil {
		return nil, err
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
