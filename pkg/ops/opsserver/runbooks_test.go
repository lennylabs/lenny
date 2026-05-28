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

type fakeRunbookSource struct {
	books []opsserver.Runbook
	md    map[string][]byte
}

func (f fakeRunbookSource) Runbooks() []opsserver.Runbook { return f.books }

func (f fakeRunbookSource) Markdown(name string) ([]byte, bool) {
	md, ok := f.md[name]
	return md, ok
}

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

func TestRunbookSteps(t *testing.T) {
	body := "### Step 1: Check pool\n" +
		"\n" +
		"<!-- access: api method=GET path=/v1/admin/diagnostics/pools/{name} -->\n" +
		"```\n" +
		"GET /v1/admin/diagnostics/pools/x\n" +
		"```\n"
	src := fakeRunbookSource{md: map[string][]byte{"warm-pool-exhaustion": []byte(body)}}
	srv := opsserver.New(opsserver.Options{Runbooks: src})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/admin/runbooks/warm-pool-exhaustion/steps", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Steps []runbooks.Step `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Steps) != 1 || got.Steps[0].ID != "step-1" {
		t.Fatalf("steps = %+v, want one step-1", got.Steps)
	}
	if len(got.Steps[0].Paths) != 1 || got.Steps[0].Paths[0].Method != "GET" {
		t.Errorf("step path = %+v, want a GET api path", got.Steps[0].Paths)
	}
}

func TestRunbookStepsNotFound(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Runbooks: fakeRunbookSource{md: map[string][]byte{}}})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/admin/runbooks/nonexistent/steps", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown runbook", rec.Code)
	}
}

// TestRunbookMarkdownEndpoint covers the §25.7 GET
// /v1/admin/runbooks/{name} endpoint: an agent that already holds the
// runbook name reads the full markdown plus front matter in one hop.
// spec: §25.7 lines 3055-3057.
func TestRunbookMarkdownEndpoint_spec_25_7_3055(t *testing.T) {
	body := "# Warm pool exhausted\n\nRemediation goes here.\n"
	src := fakeRunbookSource{
		books: []opsserver.Runbook{{
			Name: "warm-pool-exhaustion",
			FrontMatter: runbooks.FrontMatter{
				Components: []string{"warmPools"},
			},
		}},
		md: map[string][]byte{"warm-pool-exhaustion": []byte(body)},
	}
	srv := opsserver.New(opsserver.Options{Runbooks: src})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/admin/runbooks/warm-pool-exhaustion", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Name        string             `json:"name"`
		FrontMatter runbooks.FrontMatter `json:"frontMatter"`
		Markdown    string             `json:"markdown"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "warm-pool-exhaustion" {
		t.Errorf("name = %q, want warm-pool-exhaustion", got.Name)
	}
	if got.Markdown != body {
		t.Errorf("markdown = %q, want %q", got.Markdown, body)
	}
	if len(got.FrontMatter.Components) != 1 || got.FrontMatter.Components[0] != "warmPools" {
		t.Errorf("front matter components = %v, want [warmPools]", got.FrontMatter.Components)
	}
}

// TestRunbookMarkdownNotFound covers the canonical 404 path so a stale
// alert pointing at a removed runbook does not crash the agent.
// spec: §25.7 line 3055.
func TestRunbookMarkdownNotFound_spec_25_7_3055(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Runbooks: fakeRunbookSource{md: map[string][]byte{}}})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/admin/runbooks/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestRunbookMarkdownUnavailable covers the 503 path so an agent
// receives the canonical error envelope when the runbook index has
// not been loaded.
// spec: §25.7 line 3055.
func TestRunbookMarkdownUnavailable_spec_25_7_3055(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/admin/runbooks/warm-pool-exhaustion", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
