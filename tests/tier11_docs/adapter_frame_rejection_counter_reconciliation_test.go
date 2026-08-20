// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the two adapter counters that
// partition the rejections of a session-scoped intra-pod JSONL frame.
//
// A frame carrying no per-session identifier on a pod holding more than one
// slot is rejected by each receiving stream's demultiplexer and counted on
// `lenny_adapter_unaddressed_frame_rejected_total`. A frame whose identifier
// names no live binding on the receiving stream is dropped and counted on
// `lenny_adapter_set_tracing_context_dropped_total`. The metric catalog in
// spec/16_observability.md and the deployer-facing reference in
// docs/reference/metrics.md are the two places an operator learns which
// series carries which rejection, so a reference row that still describes the
// drop counter as covering every mis-addressed frame sends an operator to the
// wrong series.
//
// This test reads the repository state directly: no build tag, no external
// infrastructure, the same posture as the other tier-11 doc checks.

package tier11_docs_test

import (
	"path/filepath"
	"testing"
)

// metricsReferenceRow returns the docs/reference/metrics.md table row for
// `metric`, or "" when the reference names no such row.
func metricsReferenceRow(t *testing.T, root, metric string) string {
	t.Helper()
	return lineContaining(readDoc(t, filepath.Join(root, "docs", "reference", "metrics.md")), "| `"+metric+"` |")
}

// spec: 16.1 (metric catalog rows for the two adapter frame-rejection
//
//	counters), 28.5.3 (intra-pod frame addressing and its two rejecting
//	dispositions), 4.6.1 (the rejection is taken per receiving stream)
//
// diagnosis: docs/reference/metrics.md has lost one of the two adapter
//
//	frame-rejection counters, or describes the drop counter as covering a
//	frame that carries no per-session identifier. The two series partition
//	the rejections: the unaddressed counter carries the frame that names no
//	session on a pod holding more than one slot, and the drop counter carries
//	the frame whose identifier names no live binding on the receiving stream.
//	A failure here means an operator reading the reference would alert on one
//	series for both conditions and would see no signal for the other.
func TestAdapterFrameRejectionCountersDocumentedSeparately(t *testing.T) {
	root := repoRoot(t)

	unaddressed := metricsReferenceRow(t, root, "lenny_adapter_unaddressed_frame_rejected_total")
	if unaddressed == "" {
		t.Fatal("docs/reference/metrics.md: no row for lenny_adapter_unaddressed_frame_rejected_total; the counter that carries the unaddressed-frame rejection reaches no deployer-facing catalog")
	}
	requireAllContain(t, "metrics.md unaddressed_frame_rejected row", unaddressed, []string{
		// The label the series carries, without which an operator cannot
		// break the rejections down by frame.
		"`frame_type`",
		// The rejection condition, stated on the absent identifier rather
		// than on a mis-addressed one.
		"carrying no per-session identifier on a pod holding more than one slot",
		// The counting unit: the rejection is taken in each stream's
		// demultiplexer, so one frame on a pod holding two live Attach
		// streams increments the counter twice.
		"once per Attach stream",
		"increments the counter twice",
		// The adapter emits it inside the agent pod, so it is outside the
		// default scrape target set.
		"Outside the default scrape set.",
	})

	dropped := metricsReferenceRow(t, root, "lenny_adapter_set_tracing_context_dropped_total")
	if dropped == "" {
		t.Fatal("docs/reference/metrics.md: no row for lenny_adapter_set_tracing_context_dropped_total")
	}
	requireAllContain(t, "metrics.md set_tracing_context_dropped row", dropped, []string{
		// Both reasons a frame's identifier names no live binding.
		"is not the session of the Attach stream that delivered it",
		"no longer holds that identifier with a bound session",
		// The row points at the sibling series rather than absorbing its
		// condition.
		"counted by `lenny_adapter_unaddressed_frame_rejected_total` instead",
		"Outside the default scrape set.",
	})
	// The condition the row carried while the drop counter stood alone. A
	// frame that carries no identifier is rejected before the drop point, so
	// a row that still reads this way tells an operator the drop series
	// covers it.
	requireNoneContain(t, "metrics.md set_tracing_context_dropped row", dropped, []string{
		"does not address the Attach stream that delivered it and the adapter drops it",
	})
}

// spec: 16.1 (metric catalog names every emitted metric)
// diagnosis: the deployer-facing reference and the §16.1 catalog disagree on
//
//	which adapter frame-rejection counters exist. The catalog is the single
//	source of the metric inventory and the reference mirrors it, so a series
//	named in one and absent from the other leaves an operator unable to tell
//	whether it is emitted.
func TestAdapterFrameRejectionCountersReachTheSpecCatalog(t *testing.T) {
	root := repoRoot(t)
	catalog := readDoc(t, filepath.Join(root, "spec", "16_observability.md"))

	for _, metric := range []string{
		"lenny_adapter_unaddressed_frame_rejected_total",
		"lenny_adapter_set_tracing_context_dropped_total",
	} {
		if row := lineContaining(catalog, metric); row == "" {
			t.Errorf("spec/16_observability.md §16.1 names no row for %s, which docs/reference/metrics.md documents", metric)
		}
		if row := metricsReferenceRow(t, root, metric); row == "" {
			t.Errorf("docs/reference/metrics.md names no row for %s, which the §16.1 catalog documents", metric)
		}
	}
}
