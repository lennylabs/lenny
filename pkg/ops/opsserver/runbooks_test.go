// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/runbooks"
)

type fakeRunbookSource struct{ books []opsserver.Runbook }

func (f fakeRunbookSource) Runbooks() []opsserver.Runbook { return f.books }

type runbookList struct {
	Runbooks []opsserver.Runbook `json:"runbooks"`
}

func getRunbooks(t *testing.T, srv http.Handler, path string) runbookList {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
	}
	var list runbookList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return list
}

func TestListRunbooksUnavailableWithoutSource(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/admin/runbooks", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when no runbook source is configured", rec.Code)
	}
}

func TestListRunbooks(t *testing.T) {
	src := fakeRunbookSource{books: []opsserver.Runbook{
		{Name: "warm-pool-exhaustion", FrontMatter: runbooks.FrontMatter{
			Triggers:   []runbooks.Trigger{{Alert: "WarmPoolExhausted"}},
			Components: []string{"warmPools"},
			Tags:       []string{"scaling"},
		}},
		{Name: "postgres-failover", FrontMatter: runbooks.FrontMatter{
			Triggers:   []runbooks.Trigger{{Alert: "PostgresDown"}},
			Components: []string{"postgres"},
		}},
	}}
	srv := opsserver.New(opsserver.Options{Runbooks: src})

	// Unfiltered: both runbooks, sorted by name.
	all := getRunbooks(t, srv, "/v1/admin/runbooks")
	if len(all.Runbooks) != 2 {
		t.Fatalf("unfiltered list has %d runbooks, want 2", len(all.Runbooks))
	}
	if all.Runbooks[0].Name != "postgres-failover" || all.Runbooks[1].Name != "warm-pool-exhaustion" {
		t.Errorf("list order = %q,%q, want sorted by name", all.Runbooks[0].Name, all.Runbooks[1].Name)
	}

	// Filtered by component.
	byComponent := getRunbooks(t, srv, "/v1/admin/runbooks?component=warmPools")
	if len(byComponent.Runbooks) != 1 || byComponent.Runbooks[0].Name != "warm-pool-exhaustion" {
		t.Errorf("component filter = %+v, want only warm-pool-exhaustion", byComponent.Runbooks)
	}

	// Filtered by alert that matches nothing.
	none := getRunbooks(t, srv, "/v1/admin/runbooks?alert=NoSuchAlert")
	if len(none.Runbooks) != 0 {
		t.Errorf("no-match filter returned %d runbooks, want 0", len(none.Runbooks))
	}
}
