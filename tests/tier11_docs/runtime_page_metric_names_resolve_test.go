// SPDX-License-Identifier: MIT

// Tier-11 check that every metric series the runtime-facing pages name is
// documented in the metrics catalog a reader is sent to.
//
// docs/reference/adapter-contract.md and docs/runtime-author-guide/platform-tools.md
// state the dispositions of a session-scoped JSONL frame and attribute each to a
// counter. Both pages point the reader at docs/reference/metrics.md for the
// series they name. A page that names a series the catalog does not carry sends
// the reader to a page where the name cannot be looked up, and the series is
// usually one no process emits yet.
//
// spec: 16.1 (metrics catalog), 28.5.3 (session-scoped frame addressing)

package tier11_docs_test

import (
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
// catalog they link to.
var runtimeFacingMetricPages = []string{
	filepath.Join("docs", "reference", "adapter-contract.md"),
	filepath.Join("docs", "runtime-author-guide", "platform-tools.md"),
}

// pendingCatalogMirror names each series a runtime-facing page states ahead of
// its docs/reference/metrics.md row, with the reason the row is not there yet.
// An entry is honoured only while the §16.1 catalog in spec/16_observability.md
// already carries the series, so a name invented on a reader-facing page is
// still a failure. The map drains: once the deployer-facing catalog carries the
// row, the entry is stale and the sweep fails until it is removed.
var pendingCatalogMirror = map[string]string{
	"lenny_adapter_unaddressed_frame_rejected_total": "§16.1 carries the row; the deployer-facing mirror lands with the counter's registration in pkg/adapter/metrics.go",
}

// specMetricCatalog returns the §16.1 metric catalog, which is the normative
// list of the series the platform emits.
func specMetricCatalog(t *testing.T, root string) string {
	t.Helper()
	return readRepoFile(t, root, "spec", "16_observability.md")
}

// spec: 16.1, 28.5.3
// diagnosis: a runtime-facing page names a Prometheus series that
//
//	docs/reference/metrics.md does not carry. The page tells a runtime author
//	that a frame disposition is counted on a series, and the catalog the same
//	page links to has no row for it, so the author cannot find the series and
//	no process is emitting it. A failure here means a reader-facing page
//	documents a counter ahead of the catalog row and the code that registers
//	it.
func TestRuntimeFacingPagesNameOnlyCatalogedMetrics(t *testing.T) {
	root := repoRoot(t)
	catalog := readDocPage(t, filepath.Join(root, "docs", "reference", "metrics.md"))
	specCatalog := specMetricCatalog(t, root)

	for _, rel := range runtimeFacingMetricPages {
		page := readDocPage(t, filepath.Join(root, rel))
		matches := documentedMetricSeries.FindAllStringSubmatch(page, -1)
		if len(matches) == 0 {
			t.Errorf("%s: no metric series is named on the page (renamed or removed?)", rel)
			continue
		}
		for _, m := range matches {
			series := m[1]
			reason, pending := pendingCatalogMirror[series]
			switch {
			case strings.Contains(catalog, series):
				if pending {
					t.Errorf("%s: names %q, which docs/reference/metrics.md now carries; remove its pendingCatalogMirror entry (%s)",
						rel, series, reason)
				}
			case pending && strings.Contains(specCatalog, series):
				t.Logf("%s: names %q ahead of its docs/reference/metrics.md row (%s)", rel, series, reason)
			case pending:
				t.Errorf("%s: names %q as a pending mirror, but the §16.1 catalog in spec/16_observability.md carries no row for it either, so the series is documented ahead of the contract that defines it",
					rel, series)
			default:
				t.Errorf("%s: names %q, which docs/reference/metrics.md does not carry; a reader following the page's own link cannot look the series up",
					rel, series)
			}
		}
	}
}
