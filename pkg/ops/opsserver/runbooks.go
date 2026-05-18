// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"
	"sort"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/runbooks"
)

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
// symptom — filter the index; an unfiltered request lists every
// runbook.
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
		Symptom:   q.Get("symptom"),
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
