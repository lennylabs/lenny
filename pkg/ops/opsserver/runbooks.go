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
