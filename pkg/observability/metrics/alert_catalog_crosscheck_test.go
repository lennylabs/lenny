// SPDX-License-Identifier: MIT

package metrics_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// metricRefRE extracts lenny_-prefixed Prometheus identifiers from an
// alert/recording expression. PromQL label keys and values are not
// lenny_-prefixed, so the prefix restriction keeps label noise out of
// the extracted set.
var metricRefRE = regexp.MustCompile(`lenny_[a-z0-9_]+`)

// histogramSuffixes are the synthetic per-bucket/per-sum/per-count
// series Prometheus derives from a histogram. An alert that references
// lenny_x_bucket / _count / _sum is referencing the catalogued
// histogram lenny_x. The suffix is stripped only when the stripped base
// is itself a catalogued histogram, so a gauge whose name legitimately
// ends in _count (lenny_gateway_replica_count) is not mis-normalised.
var histogramSuffixes = []string{"_bucket", "_count", "_sum"}

// referencedSeries returns the raw set of lenny_ metric tokens
// referenced across every §16.5 alert expression in the catalog.
func referencedSeries() map[string]bool {
	out := map[string]bool{}
	for _, r := range rules.Catalog() {
		for _, m := range metricRefRE.FindAllString(r.Expr, -1) {
			out[m] = true
		}
	}
	return out
}

// resolves reports whether a referenced token is covered by the known
// catalog set, accounting for histogram-derived _bucket/_count/_sum
// series whose base histogram is catalogued.
func resolves(token string, known map[string]bool, histograms map[string]bool) bool {
	if known[token] {
		return true
	}
	for _, sfx := range histogramSuffixes {
		if strings.HasSuffix(token, sfx) {
			if base := strings.TrimSuffix(token, sfx); histograms[base] {
				return true
			}
		}
	}
	return false
}

// TestEverySpec165AlertMetricIsCatalogued is the F-16.5.2 regression
// gate: every metric series an §16.5 alert expression evaluates against
// must be declared in the metrics catalog (the §16.1 canonical table in
// MetricCatalog, or the §16.5 alert-support series in
// AlertSupportCatalog). A reference with no catalog row means the alert
// evaluates against a series that the platform never declares it
// exports — the expression silently resolves to "no series" and the
// alert can never fire.
//
// spec: §16.5 (alerting rules), §16.1 (metrics catalog).
func TestEverySpec165AlertMetricIsCatalogued(t *testing.T) {
	known := map[string]bool{}
	histograms := map[string]bool{}
	for _, m := range metrics.MetricCatalog() {
		known[m.Name] = true
		if m.Type == metrics.TypeHistogram {
			histograms[m.Name] = true
		}
	}
	for _, m := range metrics.AlertSupportCatalog() {
		known[m.Name] = true
		if m.Type == metrics.TypeHistogram {
			histograms[m.Name] = true
		}
	}

	var missing []string
	for s := range referencedSeries() {
		if !resolves(s, known, histograms) {
			missing = append(missing, s)
		}
	}
	sort.Strings(missing)
	for _, s := range missing {
		t.Errorf("§16.5 alert expression references %q, which is in neither MetricCatalog nor AlertSupportCatalog", s)
	}
}

// TestAlertSupportCatalogIsDisjointFromCanonical guards the split
// between the §16.1 canonical metric table and the §16.5 alert-support
// series: a series belongs to exactly one catalog so the canonical
// §16.1 surface stays an accurate transcription of the spec table.
//
// spec: §16.1 (canonical metrics), §16.5 (alert-support series).
func TestAlertSupportCatalogIsDisjointFromCanonical(t *testing.T) {
	canonical := map[string]bool{}
	for _, m := range metrics.MetricCatalog() {
		canonical[m.Name] = true
	}
	for _, m := range metrics.AlertSupportCatalog() {
		if canonical[m.Name] {
			t.Errorf("%q appears in both MetricCatalog and AlertSupportCatalog; it must belong to exactly one", m.Name)
		}
		if !m.Type.IsValid() {
			t.Errorf("AlertSupportCatalog entry %q has invalid type %q", m.Name, m.Type)
		}
		if m.Help == "" {
			t.Errorf("AlertSupportCatalog entry %q has no Help text", m.Name)
		}
	}
}
