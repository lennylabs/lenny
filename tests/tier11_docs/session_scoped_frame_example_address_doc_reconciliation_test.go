// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the per-session address value the
// JSONL frame examples in docs/reference/adapter-contract.md carry.
//
// The frame examples were written when the address was present only on a pod
// whose pool set `sessionPolicy.maxConcurrentSessions > 1`, so the
// `message`, `tool_result`, `response`, and `tool_call` examples spelled the
// address as `null` and the `set_tracing_context` example spelled it as an
// ordinal slot name. Every session is now bound to a slot on every pod and the
// address is a session identifier: the field tables on the same page state
// that the adapter populates it on every pod and that a runtime echoes the
// identifier it was handed, and the published JSONL schema rejects `null` and
// any non-string value. An example that still shows `null` contradicts the
// table directly beneath it, and an ordinal value teaches a runtime author to
// send a name no session has.
//
// These cases read the page directly (no build tag, no infrastructure), the
// same posture as the other tier-11 doc checks. They match both spellings of
// the address key so the rule holds across the key rename.
//
// spec: 5.2 (session-mode slot on every pod), 7.3 (session identifiers),
// 28.5.3 (session-scoped frame schemas and addressing)

package tier11_docs_test

import (
	"regexp"
	"testing"
)

// addressKeyPattern matches either spelling of the per-session address key as
// a JSON property in a documentation example.
const addressKeyPattern = `"(?:slotId|sessionId)"`

var (
	// absentAddressExample matches an example that spells the per-session
	// address as JSON null.
	absentAddressExample = regexp.MustCompile(addressKeyPattern + `\s*:\s*null`)

	// ordinalAddressExample matches an example that spells the per-session
	// address as an ordinal slot name such as `slot_01`.
	ordinalAddressExample = regexp.MustCompile(addressKeyPattern + `\s*:\s*"slot_[^"]*"`)

	// sessionAddressExample matches an example that spells the per-session
	// address as a session identifier, the page's `sess_` convention.
	sessionAddressExample = regexp.MustCompile(addressKeyPattern + `\s*:\s*"sess_[^"]*"`)
)

// spec: 5.2, 7.3, 28.5.3
// diagnosis: a JSONL frame example on docs/reference/adapter-contract.md
//
//	spells the per-session address as `null`. Every session is bound to a slot
//	on every pod and the published JSONL schema accepts only a string there,
//	so the example contradicts the field table printed beneath it and a
//	runtime author who copies it emits a frame the schema rejects. A failure
//	here means the page's examples and its stated value rule disagree.
func TestAdapterContractFrameExamplesCarryNoAbsentSessionAddress(t *testing.T) {
	root := repoRoot(t)
	contract := adapterContractDoc(t, root)

	if found := absentAddressExample.FindAllString(contract, -1); len(found) > 0 {
		t.Errorf("docs/reference/adapter-contract.md: %d frame example(s) spell the per-session address as null: %q", len(found), found)
	}
}

// spec: 5.2, 7.3, 28.5.3
// diagnosis: a JSONL frame example on docs/reference/adapter-contract.md
//
//	spells the per-session address as an ordinal slot name. The address is the
//	session's identifier, so an ordinal example names a value no session
//	carries and a runtime author who copies it addresses a frame the adapter
//	drops. A failure here means the page's examples name the pod-side resource
//	where the wire names the session.
func TestAdapterContractFrameExamplesCarryNoOrdinalSessionAddress(t *testing.T) {
	root := repoRoot(t)
	contract := adapterContractDoc(t, root)

	if found := ordinalAddressExample.FindAllString(contract, -1); len(found) > 0 {
		t.Errorf("docs/reference/adapter-contract.md: %d frame example(s) spell the per-session address as an ordinal slot name: %q", len(found), found)
	}
}

// spec: 5.2, 7.3, 28.5.3
// diagnosis: docs/reference/adapter-contract.md carries no frame example that
//
//	spells the per-session address as a session identifier. The two cases
//	above only reject the wrong values, so a page that dropped its addressed
//	examples altogether would pass them. A failure here means the page no
//	longer shows a runtime author what an addressed frame looks like.
func TestAdapterContractFrameExamplesShowASessionAddress(t *testing.T) {
	root := repoRoot(t)
	contract := adapterContractDoc(t, root)

	// One per session-scoped frame section the page documents, plus the
	// shorthand and trace examples that repeat them.
	const minimumAddressedExamples = 5
	if got := len(sessionAddressExample.FindAllString(contract, -1)); got < minimumAddressedExamples {
		t.Errorf("docs/reference/adapter-contract.md: %d frame example(s) spell the per-session address as a session identifier, want at least %d", got, minimumAddressedExamples)
	}
}
