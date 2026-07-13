// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test for the §25.7 Path A runbook index built from
// the bundled docs/runbooks/*.md files. The production build path
// (opsserver.LoadRunbookDir, called by lenny-ops against the live
// docs/runbooks directory) is exercised here against the real
// repository directory rather than a hand-built fixture: the unit tests
// in pkg/ops/opsserver drive a fake RunbookSource and never touch the
// shipped files, so a runbook whose front matter stops parsing would be
// silently indexed with empty metadata and vanish from every discovery
// filter without any test failing.
package runbooks_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/runbooks"
)

// repoRoot walks up from the working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("no go.mod found above %s", wd)
		}
		d = parent
	}
}

// TestRunbookIndexLoadsShippedDir asserts the production runbook index
// builds cleanly from the real docs/runbooks/ directory: the index is
// non-empty, every shipped runbook parses with non-empty front matter,
// and the canonical warm-pool-exhaustion runbook resolves both by name
// and through the ?component=warmPools discovery filter.
//
// spec: §25.7 — "The index is built at startup by scanning the bundled
// docs/runbooks/*.md files and parsing their front matter." and "Each
// runbook starts with YAML front matter that agents and the runbook
// index can parse for discovery"; the Path A filter table maps
// ?component=warmPools to the runbook front matter's components[].
//
// diagnosis: a failure means the production runbook index built by
// lenny-ops from docs/runbooks/ is degraded — either LoadRunbookDir
// cannot read the shipped directory, or a shipped runbook's front
// matter no longer parses and was silently indexed with empty metadata,
// which drops it from every discovery filter (?component, ?alert, ?tag,
// ?requires, ?q). Inspect the named runbook file's YAML front matter.
func TestRunbookIndexLoadsShippedDir(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "docs", "runbooks")

	src, err := opsserver.LoadRunbookDir(dir)
	if err != nil {
		t.Fatalf("LoadRunbookDir(%s): %v", dir, err)
	}

	indexed := src.Runbooks()
	if len(indexed) == 0 {
		t.Fatalf("runbook index is empty; expected the bundled docs/runbooks/*.md set")
	}

	// The count of indexed runbooks must equal the count of shipped .md
	// files: LoadRunbookDir silently drops a file it cannot read, so a
	// mismatch means a shipped runbook fell out of the index.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var mdFiles int
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			mdFiles++
		}
	}
	if len(indexed) != mdFiles {
		t.Errorf("indexed %d runbooks but %d .md files ship under docs/runbooks/", len(indexed), mdFiles)
	}

	// Every shipped runbook must carry parseable, non-empty front
	// matter. LoadRunbookDir substitutes an empty FrontMatter when a
	// file's front matter fails to parse, so an indexed entry with an
	// empty title marks a runbook that has silently lost its discovery
	// metadata. Re-parsing the raw markdown independently confirms the
	// failure is in the shipped file rather than the index.
	for _, rb := range indexed {
		md, ok := src.Markdown(rb.Name)
		if !ok {
			t.Errorf("runbook %q is indexed but has no markdown body", rb.Name)
			continue
		}
		if _, perr := runbooks.Parse(md); perr != nil {
			t.Errorf("runbook %q front matter does not parse: %v", rb.Name, perr)
		}
		if rb.Title == "" {
			t.Errorf("runbook %q has empty front matter (no title); its front matter did not parse into the index", rb.Name)
		}
	}

	// The canonical warm-pool-exhaustion runbook must resolve by name.
	md, ok := src.Markdown("warm-pool-exhaustion")
	if !ok || len(md) == 0 {
		t.Fatalf("warm-pool-exhaustion did not resolve by name from the shipped index")
	}

	// And it must resolve through the §25.7 Path A ?component=warmPools
	// filter served by the real opsserver over the shipped index.
	srv := opsserver.New(opsserver.Options{Runbooks: src})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/runbooks?component=warmPools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/admin/runbooks?component=warmPools status = %d, want 200", rec.Code)
	}
	var list struct {
		Runbooks []opsserver.Runbook `json:"runbooks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode runbook list: %v", err)
	}
	var found bool
	for _, rb := range list.Runbooks {
		if rb.Name == "warm-pool-exhaustion" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("?component=warmPools did not return warm-pool-exhaustion; got %d runbooks", len(list.Runbooks))
	}
}
