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
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// sessionScopedFrameAddressField is the JSONL frame property that names the
// session a session-scoped frame is addressed to, as
// schemas/lenny-adapter-jsonl.schema.json publishes it.
const sessionScopedFrameAddressField = "slotId"

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

	mode := section(page, "Session Mode")
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

// spec: 4.7, 28.5.3
// diagnosis: a documented frame example in docs/reference/adapter-contract.md
//
//	carries the per-session identifier as JSON null or as an empty string. The
//	field tables on the same page state the adapter populates the identifier on
//	every pod and the published JSONL schema types it as a string, so a null
//	example both contradicts the table above it and fails schema validation. A
//	runtime author copying the example emits a frame the adapter rejects. A
//	failure here means the page's examples and its field tables disagree about
//	whether a session-scoped frame is addressed.
func TestDocumentedFrameExamplesCarryAnIdentifierValue(t *testing.T) {
	page := filepath.Join(repoRoot(t), "docs", "reference", "adapter-contract.md")

	blocks := documentedFrameExamples(t, page)
	if len(blocks) == 0 {
		t.Fatalf("%s: no documented frame example carries a per-session identifier (renamed or removed?)", page)
	}

	for _, b := range blocks {
		frame := map[string]any{}
		if err := json.Unmarshal([]byte(b.Body), &frame); err != nil {
			t.Errorf("%s:%d: documented frame example is not a JSON object: %v", page, b.StartLine, err)
			continue
		}
		value, ok := frame[sessionScopedFrameAddressField].(string)
		if !ok {
			t.Errorf("%s:%d: documented frame example addresses no session: %q is %#v, and the published JSONL schema types it as a string",
				page, b.StartLine, sessionScopedFrameAddressField, frame[sessionScopedFrameAddressField])
			continue
		}
		if strings.TrimSpace(value) == "" {
			t.Errorf("%s:%d: documented frame example carries an empty %q", page, b.StartLine, sessionScopedFrameAddressField)
		}
	}
}

// documentedFrameExamples returns the JSON code blocks of the adapter contract
// page that carry the session-scoped frames' per-session address field. Those
// are the examples the field tables on the same page describe, so they are the
// examples that must agree with the population rule and with the published
// schema.
func documentedFrameExamples(t *testing.T, page string) []fencedBlock {
	t.Helper()

	blocks, err := extractFencedBlocks(page)
	if err != nil {
		t.Fatalf("read %s: %v", page, err)
	}

	var carrying []fencedBlock
	for _, b := range blocks {
		if normalize(b.Language) != "json" {
			continue
		}
		if !strings.Contains(b.Body, `"`+sessionScopedFrameAddressField+`"`) {
			continue
		}
		carrying = append(carrying, b)
	}
	return carrying
}
