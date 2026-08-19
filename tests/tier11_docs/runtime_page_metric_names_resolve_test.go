// SPDX-License-Identifier: MIT

// Tier-11 check that every metric series the runtime-facing pages name is
// documented in the metrics catalog a reader is sent to and emitted by a
// process that registers it.
//
// docs/reference/adapter-contract.md and docs/runtime-author-guide/platform-tools.md
// state the dispositions of a session-scoped JSONL frame and attribute each to a
// counter. Both pages point the reader at docs/reference/metrics.md for the
// series they name. A page that names a series the catalog does not carry sends
// the reader to a page where the name cannot be looked up, and a page that names
// a series no package registers tells the reader to alert on a counter nothing
// emits. A series enters a reader-facing page in the same change that registers
// the counter and adds its catalog row.
//
// spec: 16.1 (metrics catalog), 28.5.3 (session-scoped frame addressing)

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// documentedMetricSeries matches a backticked Prometheus series name on a
// reader-facing page. The suffixes are the metric-type suffixes the catalog
// uses, which keeps a backticked field or label name out of the match.
var documentedMetricSeries = regexp.MustCompile("`(lenny_[a-z0-9_]+_(?:total|seconds|bytes|count|ratio|info))`")

// runtimeFacingMetricPages are the pages whose metric names must resolve in the
// catalog they link to and in the code that registers the series.
var runtimeFacingMetricPages = []string{
	filepath.Join("docs", "reference", "adapter-contract.md"),
	filepath.Join("docs", "runtime-author-guide", "platform-tools.md"),
}

// registeredMetricSeries returns every Prometheus series name that appears as a
// string literal under pkg/, which is where a collector's Name field is
// declared. A series absent from the set is registered by no package and
// therefore emitted by no process.
func registeredMetricSeries(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	err := filepath.Walk(filepath.Join(root, "pkg"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range registeredSeriesLiteral.FindAllStringSubmatch(string(body), -1) {
			names[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk pkg/ for registered metric series: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("pkg/: no Prometheus series name literal was found (matcher stale?)")
	}
	return names
}

// registeredSeriesLiteral matches a series name written as a Go string literal.
var registeredSeriesLiteral = regexp.MustCompile(`"(lenny_[a-z0-9_]+)"`)

// spec: 16.1, 28.5.3
// diagnosis: a runtime-facing page names a Prometheus series that
//
//	docs/reference/metrics.md does not carry, or that no package under pkg/
//	registers. The page tells a runtime author that a frame disposition is
//	counted on a series, and either the catalog the same page links to has no
//	row for it or no process emits it. A failure here means a reader-facing
//	page documents a counter ahead of the catalog row and the code that
//	registers it.
func TestRuntimeFacingPagesNameOnlyRegisteredAndCatalogedMetrics(t *testing.T) {
	root := repoRoot(t)
	catalog := readDocPage(t, filepath.Join(root, "docs", "reference", "metrics.md"))
	registered := registeredMetricSeries(t, root)

	for _, rel := range runtimeFacingMetricPages {
		page := readDocPage(t, filepath.Join(root, rel))
		matches := documentedMetricSeries.FindAllStringSubmatch(page, -1)
		if len(matches) == 0 {
			t.Errorf("%s: no metric series is named on the page (renamed or removed?)", rel)
			continue
		}
		for _, m := range matches {
			series := m[1]
			if !strings.Contains(catalog, series) {
				t.Errorf("%s: names %q, which docs/reference/metrics.md does not carry; a reader following the page's own link cannot look the series up",
					rel, series)
			}
			if !registered[series] {
				t.Errorf("%s: names %q, which no package under pkg/ registers, so the page attributes a disposition to a counter no process emits",
					rel, series)
			}
		}
	}
}
