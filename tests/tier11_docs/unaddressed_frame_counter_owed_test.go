// SPDX-License-Identifier: MIT

// Tier-11 check that the two rejecting dispositions of a session-scoped JSONL
// frame and the counters the reader pages attribute them to stay in step.
//
// §28.5.3 rejects a session-scoped frame in two ways, and §16.1 catalogs one
// series for each: a frame carrying no per-session identifier on a pod holding
// more than one slot is rejected and counted on
// `lenny_adapter_unaddressed_frame_rejected_total`, and a frame whose
// identifier names no live binding on the receiving stream is dropped and
// counted on `lenny_adapter_set_tracing_context_dropped_total`. The two
// counters partition the rejections, so a reader page that names one of them
// has to name the other, or say nothing about how the rejections are counted.
//
// The unaddressed counter is cataloged in §16.1 and has no
// docs/reference/metrics.md row yet, and a reader-facing page may not name a
// series the catalog it links to does not carry (see
// runtime_page_metric_names_resolve_test.go). This case holds the resulting
// obligation in both directions: while the catalog row is absent, no reader
// page may claim a count it cannot name; once the row lands, every one of the
// three reader-facing statements of the rejection names the series.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 16.1 (metrics catalog), 28.5.3 (session-scoped frame addressing)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// unaddressedRejectionSeries is the §16.1 counter for a session-scoped frame
// rejected for carrying no per-session identifier on a pod holding more than
// one slot.
const unaddressedRejectionSeries = "lenny_adapter_unaddressed_frame_rejected_total"

// unaddressedRejectionStatements returns the three reader-facing sentences
// that state what happens to an unaddressed session-scoped frame, keyed by the
// label a failure reports. Each one already names the drop counter for the
// other rejecting disposition, so each one is a site where the unaddressed
// counter is owed.
func unaddressedRejectionStatements(t *testing.T, root string) map[string]string {
	t.Helper()

	contract := adapterContractDoc(t, root)
	tools := platformToolsDoc(t, root)

	out := map[string]string{
		"adapter-contract.md set_tracing_context addressing paragraph": lineContaining(contract, "Two dispositions reject a frame"),
		"platform-tools.md lenny/set_tracing_context note":             lineContaining(tools, "The JSONL frame carries the per-session identifier"),
		"platform-tools.md tool-availability paragraph":                lineContaining(tools, "available at every level"),
	}
	for label, body := range out {
		if body == "" {
			t.Fatalf("%s: the statement of an unaddressed session-scoped frame's disposition was not found (rewritten or removed?)", label)
		}
	}
	return out
}

// spec: 16.1, 28.5.3
// diagnosis: the reader-facing statements of a session-scoped frame's two
//
//	rejecting dispositions disagree with the metrics catalog they link to.
//	Either a page claims the rejections are counted separately while naming
//	only one series, so a reader is told a second count exists and cannot find
//	it, or docs/reference/metrics.md has gained the row for
//	`lenny_adapter_unaddressed_frame_rejected_total` and the pages still leave
//	the unaddressed rejection attributed to no counter, so the two counters no
//	longer visibly partition the rejections. A failure here means the change
//	that lands the catalog row did not carry the reader pages with it, or a
//	page reintroduced an unresolvable count.
func TestUnaddressedFrameRejectionCounterIsNamedWithItsCatalogRow(t *testing.T) {
	root := repoRoot(t)

	if specCatalog := readRepoFile(t, root, "spec", "16_observability.md"); !strings.Contains(specCatalog, unaddressedRejectionSeries) {
		t.Fatalf("spec/16_observability.md: §16.1 carries no row for %q; the counter this case tracks is no longer cataloged", unaddressedRejectionSeries)
	}

	statements := unaddressedRejectionStatements(t, root)
	cataloged := strings.Contains(readDocPage(t, filepath.Join(root, "docs", "reference", "metrics.md")), unaddressedRejectionSeries)

	for label, body := range statements {
		if cataloged {
			requireAllContain(t, label, body, []string{unaddressedRejectionSeries})
			continue
		}
		requireNoneContain(t, label, body, []string{unaddressedRejectionSeries})
		// The catalog row is owed, so the page states the disposition
		// without asserting a count it cannot name.
		requireNoneContain(t, label, body, []string{"counted separately"})
	}
}
