// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the carrier of the session identifier.
//
// One runtime process serves every session on a pod, so a pod-global file
// cannot name the session a runtime is serving. The session identifier reaches
// the runtime in the `sessionId` member of the session's own `session_start`
// frame on the message channel, and on every session-scoped frame after it.
// The adapter manifest carries only pod-scoped fields and has no `sessionId`
// or `taskId` member: a session's task identifier equals its `sessionId`.
//
// The retired readings are a manifest member that names the pod's session and
// a manifest member that names the session whose start last wrote the file.
// Either one tells a co-tenanted runtime that a manifest read names its own
// session.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 28.5.3 (CH-MSGSOCK session_start frame), 4.7.6 (adapter manifest field
// set)

package tier11_docs_test

import (
	"testing"
)

// spec: 28.5.3, 4.7.6
// diagnosis: docs/reference/adapter-contract.md names the adapter manifest as
//
//	the carrier of the session identifier, or its `session_start` reference
//	section no longer states the identifier the frame opens. The manifest is
//	pod-global and carries only pod-scoped fields, so a `sessionId` or `taskId`
//	member in its example or its field reference tells a runtime serving
//	several sessions that a manifest read names its own session.
func TestSessionIdentifierIsCarriedBySessionStartRatherThanTheManifest(t *testing.T) {
	root := repoRoot(t)

	contract := adapterContractDoc(t, root)
	frame := section(contract, "`session_start` ---")
	if frame == "" {
		t.Fatal("docs/reference/adapter-contract.md: `session_start` reference section not found (renamed or removed?)")
	}
	row := lineContaining(frame, "| `sessionId` |")
	if row == "" {
		t.Fatal("docs/reference/adapter-contract.md: the `session_start` `sessionId` field row was not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md session_start `sessionId` row", row, []string{
		"The session the frame opens.",
		"The adapter populates it on every pod.",
	})

	manifest := section(contract, "Adapter Manifest")
	if manifest == "" {
		t.Fatal("docs/reference/adapter-contract.md: `Adapter Manifest` section not found (renamed or removed?)")
	}
	requireNoneContain(t, "adapter-contract.md Adapter Manifest section", manifest, []string{
		"| `sessionId` |",
		"| `taskId` |",
		`"sessionId":`,
		`"taskId":`,
		"The session whose start last wrote the manifest.",
		"The session identifier for this pod",
	})
}
