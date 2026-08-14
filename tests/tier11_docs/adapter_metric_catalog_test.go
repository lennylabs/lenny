// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the adapter's registered
// Prometheus metrics with the two catalogs an operator reads: the §16.1
// metric catalog in spec/16_observability.md and the deployer-facing
// reference in docs/reference/metrics.md. A counter that exists only in
// code is invisible to whoever decides what to scrape and alert on.
//
// This test is NOT under a build tag: it reads the repository state
// directly and needs no external infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// metricNamePattern matches the Name field of a Prometheus metric option
// literal in pkg/adapter/metrics.go, which is where every adapter metric
// is registered.
var metricNamePattern = regexp.MustCompile(`Name:\s*"(lenny_[a-z0-9_]+)"`)

// undocumentedAdapterMetrics are adapter metrics that reach neither
// catalog. Each predates the sweep and closing it needs a spec amendment
// through the proposal pipeline, so they are excluded from the
// documentation assertion rather than silently passing it. An entry that
// is no longer registered, or that has since been documented, fails the
// sweep so the list drains rather than accumulates.
var undocumentedAdapterMetrics = map[string]string{
	"lenny_llm_inflight_requests":                     "§4.7 direct-mode in-flight gate gauge",
	"lenny_credential_rotation_inflight_wait_seconds": "§4.7 rotation gate wait histogram",
	"lenny_credential_rotation_timeout_total":         "§4.7 credentials_acknowledged timeout counter",
	"lenny_credential_rotation_grace_period_seconds":  "§4.7 rotation grace-period histogram",
	"lenny_adapter_control_events_total":              "§4.7 AdapterEvents delivery counter",
	"lenny_adapter_control_events_dropped_total":      "§4.7 AdapterEvents drop counter",
}

// specCatalogPending are adapter metrics that the deployer-facing
// reference documents while the §16.1 catalog does not name them yet.
// Each entry names the proposal that stages the catalog row, so a metric
// added to the reference with no staged amendment fails the sweep. When
// the row lands the entry goes stale and the sweep fails until it is
// removed.
var specCatalogPending = map[string]string{}

// registeredAdapterMetrics returns the metric names pkg/adapter/metrics.go
// registers, which is the adapter's whole metric surface.
func registeredAdapterMetrics(t *testing.T, root string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "pkg", "adapter", "metrics.go"))
	if err != nil {
		t.Fatalf("read pkg/adapter/metrics.go: %v", err)
	}
	var names []string
	for _, m := range metricNamePattern.FindAllStringSubmatch(string(body), -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatal("pkg/adapter/metrics.go registered no metric: the sweep's source of truth moved")
	}
	sort.Strings(names)
	return names
}

// spec: §16.1 (metric catalog names every emitted metric), §4.7 (adapter
//
//	metrics), §28.5.3 (set_tracing_context drop counter)
//
// diagnosis: an adapter metric is registered in pkg/adapter/metrics.go and
//
//	named in neither docs/reference/metrics.md nor the §16.1 catalog, so a
//	deployer deciding what to scrape and alert on cannot learn the series
//	exists. A new metric must reach the reference; one that cannot reach the
//	§16.1 catalog without a spec amendment must have that amendment staged
//	as a proposal.
func TestAdapterMetricsReachTheDocumentationCatalogs(t *testing.T) {
	root := repoRoot(t)
	registered := registeredAdapterMetrics(t, root)
	reference := readDoc(t, filepath.Join(root, "docs", "reference", "metrics.md"))
	catalog := readDoc(t, filepath.Join(root, "spec", "16_observability.md"))

	registeredSet := make(map[string]bool, len(registered))
	for _, name := range registered {
		registeredSet[name] = true
	}

	for _, name := range registered {
		if _, pending := undocumentedAdapterMetrics[name]; pending {
			if strings.Contains(reference, name) || strings.Contains(catalog, name) {
				t.Errorf("%s is documented now: remove it from undocumentedAdapterMetrics so the sweep holds it to both catalogs", name)
			}
			continue
		}
		if !strings.Contains(reference, name) {
			t.Errorf("%s is registered in pkg/adapter/metrics.go and named nowhere in docs/reference/metrics.md", name)
		}
		if strings.Contains(catalog, name) {
			if proposal, ok := specCatalogPending[name]; ok {
				t.Errorf("%s now has a §16.1 catalog row: remove its specCatalogPending entry (%s)", name, proposal)
			}
			continue
		}
		proposal, ok := specCatalogPending[name]
		if !ok {
			t.Errorf("%s is registered in pkg/adapter/metrics.go and missing from the §16.1 catalog in spec/16_observability.md, with no staged amendment recorded in specCatalogPending", name)
			continue
		}
		staged := readDoc(t, filepath.Join(root, "proposals", proposal))
		if !strings.Contains(staged, name) {
			t.Errorf("proposals/%s is recorded as staging the §16.1 catalog row for %s but does not name the metric", proposal, name)
		}
	}

	// A stale exception hides the next omission, so both lists are held to
	// the metrics that are actually registered.
	for name := range undocumentedAdapterMetrics {
		if !registeredSet[name] {
			t.Errorf("undocumentedAdapterMetrics names %s, which pkg/adapter/metrics.go no longer registers", name)
		}
	}
	for name := range specCatalogPending {
		if !registeredSet[name] {
			t.Errorf("specCatalogPending names %s, which pkg/adapter/metrics.go no longer registers", name)
		}
	}
}
