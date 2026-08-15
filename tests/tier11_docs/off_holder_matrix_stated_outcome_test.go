// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the §29.3 off-holder matrix. The
// matrix is the normative statement of what a gateway replica does with a
// session-scoped client route when it is not the replica holding that
// session's pod control stream `CH-ATTACH`, so every row it carries must
// state a required outcome. Two rows recorded the outcome as unstated
// instead: the four tool-use and elicitation resolution routes, and the
// `Accept: application/json` form of the session events route. Both now
// require a forward to the session's coordinating replica and fail closed
// when that replica is unreachable, and the matrix preamble lists both among
// the forwarding rows.
//
// The non-happy path this guards is a matrix row that defers its own
// off-holder outcome. A reader arriving at the matrix for one of those routes
// gets no requirement, and an implementor is free to resolve a blocked call
// or answer an events page from a replica that holds neither the pending
// request nor the session's replay buffer.
//
// The events streaming row's "does not state" clause is about relay absence
// rather than about the off-holder outcome, so this test scopes its deferral
// check to the two rows the matrix owns the outcome for.
//
// The test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 29.3 (off-holder matrix), 10.4 (gateway reliability, event replay
// buffer), 15.1 (REST API), 15.2.1 (REST/MCP consistency contract).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// deferralPhrases are the ways a matrix row can record its off-holder
// outcome as unstated. A row that owns an outcome must carry none of them.
var deferralPhrases = []string{
	"is a condition the specification does not state",
	"What an off-holder replica",
	"whether it must forward the resolution to the coordinator",
	"whether it forwards the request to the coordinator",
}

// spec: 29.3, 15.2.1, 10.4
// diagnosis: A §29.3 off-holder matrix row that the matrix owns the outcome
//
//	for records that outcome as unstated. The matrix is the normative
//	statement of off-holder behaviour for the session-scoped client routes it
//	names, because the client-to-gateway session REST surface is not a channel
//	in the §28 register and no §28 card owns it. A failure here means the
//	tool-use and elicitation resolution row, or the events `Accept:
//	application/json` row, reverted to deferring its outcome, or dropped the
//	forward-to-coordinator requirement or the fail-closed rule for an
//	unreachable coordinator. Restore the required outcome in the row.
func TestOffHolderMatrixRowsStateTheirOutcome(t *testing.T) {
	root := repoRoot(t)
	scenarios := filepath.Join(root, "spec", "29_communication-scenarios.md")
	s293 := specSection(t, scenarios, "### 29.3 ")

	// The four tool-use and elicitation resolution routes: the pending call
	// lives on the coordinator, so the resolution is forwarded there and the
	// serving replica neither resolves it locally nor reports a resolution it
	// did not deliver.
	resolution := requireLine(t, s293, "/elicitations/{elicitationId}/dismiss` |")
	requireAllContain(t, "§29.3 interaction-resolution row", resolution, []string{
		"Forward the resolution to the coordinator",
		"resolves the pending tool-call approval or elicitation the pod is blocked on",
		"does not resolve it against its own pending-request state",
		"does not report a resolution it did not deliver",
		"On an unreachable coordinator it fails closed",
	})
	requireNoneContain(t, "§29.3 interaction-resolution row", resolution, deferralPhrases)

	// The events JSON form reads the per-session replay buffer §15.1 fixes as
	// its source, and only the coordinating replica holds that buffer.
	eventsJSON := requireLine(t, s293, "`Accept: application/json` form |")
	requireAllContain(t, "§29.3 events JSON row", eventsJSON, []string{
		"Forward the request to the coordinator",
		"serves the envelope from the buffer it holds",
		"does not answer the request from its own buffer",
		"On an unreachable coordinator it fails closed",
	})
	requireNoneContain(t, "§29.3 events JSON row", eventsJSON, deferralPhrases)

	// The streaming events row keeps its relay-absence clause, which is a
	// different condition from the off-holder outcome. Its outcome is stated.
	eventsStream := requireLine(t, s293, "`GET /v1/sessions/{id}/events`, streaming form |")
	requireAllContain(t, "§29.3 events streaming row", eventsStream, []string{
		"Serve both the backlog and the live tail from the shared session-event relay `CH-EVENTRELAY`",
	})
}

// spec: 29.3
// diagnosis: The §29.3 matrix preamble and the matrix rows disagree about
//
//	which rows forward to the coordinator. The preamble states the two
//	requirements that govern every forwarding row, including the fail-closed
//	`TARGET_NOT_READY` outcome for an unreachable coordinator, and it
//	separately lists the rows whose outcome is not a forward. A failure here
//	means the interaction-resolution row or the events JSON row is missing
//	from the forwarding enumeration, or is still narrated as a row that
//	records no outcome, so the preamble contradicts the row it describes.
func TestOffHolderMatrixPreambleMatchesForwardingRows(t *testing.T) {
	root := repoRoot(t)
	scenarios := filepath.Join(root, "spec", "29_communication-scenarios.md")
	s293 := specSection(t, scenarios, "### 29.3 ")

	// The preamble spans several source lines. Scope to the paragraph that
	// opens with the two forwarding requirements.
	const preambleStart = "Two requirements govern the rows whose required outcome is a forward to the coordinator."
	idx := strings.Index(s293, preambleStart)
	if idx < 0 {
		t.Fatalf("§29.3 does not carry the off-holder forwarding preamble (renamed or removed?)")
	}
	rest := s293[idx:]
	end := strings.Index(rest, "\n\n")
	if end < 0 {
		t.Fatalf("§29.3 forwarding preamble is not delimited by a blank line")
	}
	preamble := rest[:end]

	requireAllContain(t, "§29.3 off-holder forwarding preamble", preamble, []string{
		// The two rows this change moved into the forwarding set are named there.
		"the interrupt, terminate, delete, resume, interaction-resolution, upload, and events JSON rows below",
		// The fail-closed rule that governs every forwarding row other than a message send.
		"fails closed with `TARGET_NOT_READY`, HTTP 409",
	})
	requireNoneContain(t, "§29.3 off-holder forwarding preamble", preamble, []string{
		// The narration that recorded the two rows as carrying no outcome.
		"the events JSON row and the interaction-resolution row record that the specification does not state",
	})
}
