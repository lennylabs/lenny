// SPDX-License-Identifier: MIT

package opsserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/runbooks"
)

// DirRunbookSource is the §25.7 runbook index backed by a directory of
// runbook markdown files (docs/runbooks/ in the repository). Each
// `.md` file is one runbook; its name is the filename without the
// extension. The index is read once at construction so serving a
// request does no filesystem I/O.
type DirRunbookSource struct {
	books []Runbook
	md    map[string][]byte
}

// LoadRunbookDir reads every `.md` file under dir, parses its §25.7
// front matter, and returns a DirRunbookSource over the result. A file
// whose front matter does not parse is skipped with a logged-style
// error rather than failing the whole index; a runbook without front
// matter is indexed with an empty FrontMatter so it is still
// retrievable by name. It returns an error only when dir cannot be
// read at all.
func LoadRunbookDir(dir string) (*DirRunbookSource, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read runbook directory %s: %w", dir, err)
	}
	src := &DirRunbookSource{md: make(map[string][]byte)}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		fm, err := runbooks.Parse(raw)
		if err != nil {
			// A runbook without parseable front matter is still served
			// by name; it just carries no discovery metadata.
			fm = runbooks.FrontMatter{}
		}
		src.books = append(src.books, Runbook{Name: name, FrontMatter: fm})
		src.md[name] = raw
	}
	sort.Slice(src.books, func(i, j int) bool { return src.books[i].Name < src.books[j].Name })
	return src, nil
}

// Runbooks returns every indexed runbook.
func (d *DirRunbookSource) Runbooks() []Runbook { return d.books }

// Markdown returns the raw markdown of the named runbook.
func (d *DirRunbookSource) Markdown(name string) ([]byte, bool) {
	md, ok := d.md[name]
	return md, ok
}

// Runbook is one entry of the §25.7 runbook index: a runbook's name
// and its parsed front matter.
type Runbook struct {
	Name string `json:"name"`
	runbooks.FrontMatter
}

// RunbookSource yields the runbooks lenny-ops indexes. The production
// implementation reads docs/runbooks/; tests supply a fixed set.
type RunbookSource interface {
	// Runbooks returns every indexed runbook keyed by name.
	Runbooks() []Runbook
	// Markdown returns the raw markdown of the named runbook. ok is
	// false when no runbook of that name is indexed.
	Markdown(name string) (md []byte, ok bool)
}

// handleListRunbooks serves the §25.7 Path A index, GET /v1/admin/-
// runbooks. The canonical query parameters — alert, component, tag,
// requires, q — filter the index; an unfiltered request lists every
// runbook.
//
// spec: §25.7 lines 3140-3143 — the five combinable filter parameters
// (`alert`, `component`, `tag`, `requires`, `q`); `requires` narrows to
// runbooks the caller can execute and `q` is the full-text search across
// symptoms, tags, and title.
func (s *Server) handleListRunbooks(w http.ResponseWriter, r *http.Request) {
	if s.runbooks == nil {
		conventions.WriteError(w, http.StatusServiceUnavailable, "RUNBOOK_INDEX_UNAVAILABLE",
			conventions.CategoryTransient, "the runbook index is not configured")
		return
	}
	q := r.URL.Query()
	filter := runbooks.Filter{
		Alert:     q.Get("alert"),
		Component: q.Get("component"),
		Tag:       q.Get("tag"),
		Requires:  q.Get("requires"),
		Query:     q.Get("q"),
	}
	matched := make([]Runbook, 0)
	for _, rb := range s.runbooks.Runbooks() {
		if runbooks.Matches(rb.FrontMatter, filter) {
			matched = append(matched, rb)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"runbooks": matched})
}

// handleRunbookSteps serves the §25.7 GET /v1/admin/runbooks/{name}/-
// steps endpoint: the structured access-path steps an agent iterates
// to find the command form matching its capability without parsing
// markdown.
func (s *Server) handleRunbookSteps(w http.ResponseWriter, r *http.Request) {
	if s.runbooks == nil {
		conventions.WriteError(w, http.StatusServiceUnavailable, "RUNBOOK_INDEX_UNAVAILABLE",
			conventions.CategoryTransient, "the runbook index is not configured")
		return
	}
	name := r.PathValue("name")
	md, ok := s.runbooks.Markdown(name)
	if !ok {
		conventions.WriteError(w, http.StatusNotFound, "RUNBOOK_NOT_FOUND",
			conventions.CategoryPermanent, "no runbook named "+name)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": runbooks.ParseSteps(md)})
}

// handleRunbookMarkdown serves the §25.7 GET /v1/admin/runbooks/{name}
// endpoint: the full rendered markdown body of the named runbook. This
// is the §25.7 Path A "follow the link" hop — an agent that already
// knows the runbook name (from alert_fired.runbook or
// suggestedAction.runbook) fetches the document in one round-trip
// without having to scrape the §25.7 index.
//
// spec: §25.7 lines 3055-3057 — "GET /v1/admin/runbooks/{name}
// returning full markdown content"; §17.7 line 741 — "/v1/admin/-
// runbooks/* … served as structured JSON".
func (s *Server) handleRunbookMarkdown(w http.ResponseWriter, r *http.Request) {
	if s.runbooks == nil {
		conventions.WriteError(w, http.StatusServiceUnavailable, "RUNBOOK_INDEX_UNAVAILABLE",
			conventions.CategoryTransient, "the runbook index is not configured")
		return
	}
	name := r.PathValue("name")
	md, ok := s.runbooks.Markdown(name)
	if !ok {
		conventions.WriteError(w, http.StatusNotFound, "RUNBOOK_NOT_FOUND",
			conventions.CategoryPermanent, "no runbook named "+name)
		return
	}
	var fm runbooks.FrontMatter
	for _, rb := range s.runbooks.Runbooks() {
		if rb.Name == name {
			fm = rb.FrontMatter
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        name,
		"frontMatter": fm,
		"markdown":    string(md),
	})
}
