// SPDX-License-Identifier: MIT

// Tier-11 check that every metric series the runtime-facing pages name is a
// series the platform defines.
//
// docs/reference/adapter-contract.md and docs/runtime-author-guide/platform-tools.md
// state the dispositions of a session-scoped JSONL frame and attribute each to a
// counter. A page that names a series the platform does not define tells the
// reader to alert on a counter that will never exist.
//
// The authority for "the platform defines this series" is the §16.1 metrics
// catalog. The reader-facing mirror in docs/reference/metrics.md and the
// collector registration under pkg/ follow the catalog, and a reader page, its
// mirror row, and the registration do not have to land in one change. Gating a
// reader page on the mirror row or on the registration would forbid the page
// from naming a series the catalog already carries, which is a rule the
// platform's own sequencing breaks rather than a defect the page has.
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
// metrics catalog.
var runtimeFacingMetricPages = []string{
	filepath.Join("docs", "reference", "adapter-contract.md"),
	filepath.Join("docs", "runtime-author-guide", "platform-tools.md"),
}

// spec: 16.1, 28.5.3
// diagnosis: a runtime-facing page names a Prometheus series the §16.1 metrics
//
//	catalog does not carry. The page tells a runtime author that a frame
//	disposition is counted on a series the platform never defined, so the
//	author alerts on a name no catalog row, no mirror, and no collector will
//	ever resolve. A failure here means a reader-facing page invented a counter
//	name or kept one the catalog retired.
func TestRuntimeFacingPagesNameOnlyCatalogedMetrics(t *testing.T) {
	root := repoRoot(t)
	catalog := readRepoFile(t, root, "spec", "16_observability.md")

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
				t.Errorf("%s: names %q, which the metrics catalog does not carry; the page attributes a disposition to a series the platform never defined",
					rel, series)
			}
		}
	}
}
