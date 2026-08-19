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
// counters partition the rejections, so every reader-facing statement of one
// disposition's count states the other's, and both series resolve in the
// catalog the pages link to.
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
// one slot. misaddressedDropSeries is the §16.1 counter for the other
// rejecting disposition.
const (
	unaddressedRejectionSeries = "lenny_adapter_unaddressed_frame_rejected_total"
	misaddressedDropSeries     = "lenny_adapter_set_tracing_context_dropped_total"
)

// unaddressedRejectionStatements returns the three reader-facing sentences
// that state what happens to an unaddressed session-scoped frame, keyed by the
// label a failure reports. Each one also states the other rejecting
// disposition, so each one names both counters.
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
//	rejecting dispositions disagree with the metrics catalogs. Either a page
//	attributes one disposition to a counter and leaves the other attributed to
//	none, so the two counters no longer visibly partition the rejections, or a
//	series a page names is missing from a catalog and a reader following the
//	page's own link cannot look it up. A failure here means a change to the
//	rejection rule, to a catalog row, or to one of the three reader pages did
//	not carry the others with it.
func TestUnaddressedFrameRejectionCounterIsNamedWithItsCatalogRow(t *testing.T) {
	root := repoRoot(t)

	series := []string{unaddressedRejectionSeries, misaddressedDropSeries}
	specCatalog := readRepoFile(t, root, "spec", "16_observability.md")
	reference := readDocPage(t, filepath.Join(root, "docs", "reference", "metrics.md"))
	for _, name := range series {
		if !strings.Contains(specCatalog, name) {
			t.Errorf("spec/16_observability.md: §16.1 carries no row for %q", name)
		}
		if !strings.Contains(reference, name) {
			t.Errorf("docs/reference/metrics.md: carries no row for %q, so the reader pages that name it link to a catalog it is missing from", name)
		}
	}

	for label, body := range unaddressedRejectionStatements(t, root) {
		requireAllContain(t, label, body, series)
	}
}
