// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the population rule on the
// session-scoped JSONL frames.
//
// The frames used to carry their per-session identifier only on a pod whose
// pool set `sessionPolicy.maxConcurrentSessions > 1`, so absence of the
// identifier meant either "this pod serves one session" or "no session was
// named" and a reader could not tell which. Every session is now bound to a
// slot on every pod: the adapter populates the identifier on every frame it
// writes whatever the pool's concurrency, the runtime echoes the identifier it
// was handed, and an identifier the frame omits resolves to the receiving
// stream's own binding on a pod holding at most one slot and is rejected on a
// pod holding more.
//
// Two reader-facing pages restate that rule: the frame reference in
// docs/reference/adapter-contract.md and the session-mode list in
// docs/runtime-author-guide/lifecycle.md. These cases pin the value rule on
// both pages and assert the retired presence condition is gone from each, so a
// later edit cannot reintroduce the conditional a runtime author would then
// implement. They read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (adapter manifest and frame contract), 5.2 (session-mode slot on
// every pod), 6.4 (per-session workspace layout), 15.4.3 (runtime-adapter
// capability matrix), 28.5.3 (session-scoped frame schemas and addressing)

package tier11_docs_test

import (
	"path/filepath"
	"testing"
)

// sessionScopedFrameSections are the frame reference sections of
// docs/reference/adapter-contract.md whose field table declares the
// per-session identifier, keyed by the heading fragment that opens each.
var sessionScopedFrameSections = []string{
	"`message` ---",
	"`tool_result` ---",
	"`tool_call` ---",
	"`set_tracing_context` ---",
}

// spec: 4.7, 28.5.3
// diagnosis: docs/reference/adapter-contract.md still tells a runtime author
//
//	that a session-scoped frame carries its per-session identifier only on a
//	pod whose pool sets `sessionPolicy.maxConcurrentSessions > 1`. Every
//	session is bound to a slot on every pod, so an author who implements the
//	documented condition reads no identifier on a default-pool pod and emits
//	frames the adapter rejects once the pod holds a second slot. A failure
//	here means the reference page and the applied contract disagree about
//	when the identifier is present.
func TestSessionScopedFrameReferenceStatesNoPresenceCondition(t *testing.T) {
	root := repoRoot(t)
	contract := adapterContractDoc(t, root)

	for _, heading := range sessionScopedFrameSections {
		entry := section(contract, heading)
		if entry == "" {
			t.Fatalf("docs/reference/adapter-contract.md: %s entry not found (renamed or removed?)", heading)
		}
		label := "adapter-contract.md " + heading + " entry"
		requireNoneContain(t, label, entry, []string{
			"Present only when `sessionPolicy.maxConcurrentSessions > 1`",
			"Omit the field on a pod that serves one session at a time",
		})
	}
}

// spec: 4.7, 28.5.3
// diagnosis: docs/reference/adapter-contract.md no longer states, on the
//
//	adapter-written frames, that the adapter populates the per-session
//	identifier on every pod, or no longer states, on the runtime-written
//	frames, that the runtime echoes the identifier it was handed and that an
//	omitted identifier resolves on a pod holding at most one slot and is
//	rejected on a pod holding more. A failure here means the page documents an
//	identifier whose population and resolution a runtime author cannot derive.
func TestSessionScopedFrameReferenceStatesThePopulationRule(t *testing.T) {
	root := repoRoot(t)
	contract := adapterContractDoc(t, root)

	// The adapter writes `message` and `tool_result`, so it populates the
	// identifier on both, on every pod.
	for _, heading := range []string{"`message` ---", "`tool_result` ---"} {
		entry := section(contract, heading)
		if entry == "" {
			t.Fatalf("docs/reference/adapter-contract.md: %s entry not found (renamed or removed?)", heading)
		}
		requireAllContain(t, "adapter-contract.md "+heading+" entry", entry, []string{
			"The adapter populates it on every pod, whatever the pool's `sessionPolicy.maxConcurrentSessions`.",
		})
	}

	// The runtime writes `tool_call` and `set_tracing_context`, so each states
	// the echo obligation and what an omitted identifier resolves to.
	for _, heading := range []string{"`tool_call` ---", "`set_tracing_context` ---"} {
		entry := section(contract, heading)
		if entry == "" {
			t.Fatalf("docs/reference/adapter-contract.md: %s entry not found (renamed or removed?)", heading)
		}
		requireAllContain(t, "adapter-contract.md "+heading+" entry", entry, []string{
			"Echo the identifier",
			"resolves to the binding of the stream that delivered it on a pod holding at most one slot, and is rejected on a pod holding more",
		})
	}
}

// spec: 5.2, 6.4, 15.4.3
// diagnosis: docs/runtime-author-guide/lifecycle.md still scopes the dispatch
//
//	loop, the per-session identifier on the binary protocol messages, and the
//	per-session workspace to a pool that sets `maxConcurrentSessions > 1`. All
//	three hold on every pod, so an author of a default-pool runtime reading the
//	conditional implements none of them and assumes a pod-global
//	`/workspace/current` that no pod has. A failure here means the page the
//	runtime-author guide owns for the execution modes contradicts the contract.
func TestSessionModeGuideStatesTheSlotRuleOnEveryPod(t *testing.T) {
	root := repoRoot(t)
	page := readDocPage(t, filepath.Join(root, "docs", "runtime-author-guide", "lifecycle.md"))

	mode := stripInlineMarkdownLinks(section(page, "Session Mode"))
	if mode == "" {
		t.Fatal("docs/runtime-author-guide/lifecycle.md: Session Mode section not found (renamed or removed?)")
	}
	requireAllContain(t, "lifecycle.md Session Mode section", mode, []string{
		"Every session is bound to a slot on every pod, whatever `maxConcurrentSessions`",
		"dispatch loop keyed on the per-session identifier",
		"echoes the identifier it was handed",
		"`/workspace/slots/{sessionId}/current/` on every pod",
	})

	// The concurrency bullet keeps the co-tenancy facts alone. It may no
	// longer be the only place the identifier and the workspace are stated.
	concurrent := lineContaining(mode, "With `maxConcurrentSessions > 1`")
	if concurrent == "" {
		t.Fatal("docs/runtime-author-guide/lifecycle.md: the maxConcurrentSessions > 1 bullet was not found (renamed or removed?)")
	}
	requireNoneContain(t, "lifecycle.md maxConcurrentSessions > 1 bullet", concurrent, []string{
		"dispatch loop",
		"carry a `slotId` field",
		"/workspace/slots/",
	})
	requireAllContain(t, "lifecycle.md maxConcurrentSessions > 1 bullet", concurrent, []string{
		"Cross-slot isolation is process-level and filesystem-level only",
		"CPU and memory are shared across slots",
		"`preConnect` is admitted only when `maxConcurrentSessions` is 1",
	})
}

// spec: 5.2, 6.4
// diagnosis: docs/getting-started/concepts.md still presents the per-session
//
//	workspace tree and the per-session credential lease as something a pool
//	gets by setting `maxConcurrentSessions > 1`. Both hold on every pod, so a
//	reader of the default configuration concludes that a session on a
//	single-session pod works out of a pod-global directory that no pod has. A
//	failure here means the concepts page contradicts the workspace layout the
//	runtime-author guide and the adapter contract state.
func TestConceptsPageStatesTheSlotRuleOnEveryPod(t *testing.T) {
	root := repoRoot(t)
	page := readDocPage(t, filepath.Join(root, "docs", "getting-started", "concepts.md"))

	modes := stripInlineMarkdownLinks(section(page, "Execution modes"))
	if modes == "" {
		t.Fatal("docs/getting-started/concepts.md: Execution modes section not found (renamed or removed?)")
	}
	requireAllContain(t, "concepts.md Execution modes section", modes, []string{
		"Every session is bound to a slot on every pod",
		"`/workspace/slots/{sessionId}/current/`",
	})

	// The concurrency bullet keeps the co-tenancy facts alone.
	concurrent := lineContaining(modes, "With `maxConcurrentSessions > 1`")
	if concurrent == "" {
		t.Fatal("docs/getting-started/concepts.md: the maxConcurrentSessions > 1 bullet was not found (renamed or removed?)")
	}
	requireNoneContain(t, "concepts.md maxConcurrentSessions > 1 bullet", concurrent, []string{
		"/workspace/slots/",
		"credential lease",
	})
	requireAllContain(t, "concepts.md maxConcurrentSessions > 1 bullet", concurrent, []string{
		"`acknowledgeProcessLevelIsolation: true`",
	})
}
