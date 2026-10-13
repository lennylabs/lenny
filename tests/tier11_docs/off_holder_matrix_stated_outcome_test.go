// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the §29.3 off-holder matrix. The
// matrix is the normative statement of what a gateway replica does with a
// session-scoped client route when it is not the replica holding that
// session's pod control stream `CH-ATTACH`, so every row it carries must
// state a required outcome. The four tool-use and elicitation resolution
// routes require a forward to the session's coordinating replica and fail
// closed when that replica is unreachable. The `Accept: application/json`
// form of the session events route is served on the serving replica from the
// backlog it reads from the shared session-event relay `CH-EVENTRELAY`, the
// same source as the streaming form, so it requires no forward.
//
// The non-happy path this guards is a matrix row that defers its own
// off-holder outcome. A reader arriving at the matrix for one of those routes
// gets no requirement, and an implementor is free to resolve a blocked call
// from a replica that does not hold the pending request, or to answer an
// events page from a source other than the relay.
//
// Each events row ends with a clause stating that what a reader receives when
// the relay or the requested entry is absent is unstated. That clause is
// about relay absence rather than about the off-holder outcome, so the
// deferral check on the events JSON row covers only the part of the row
// before its relay-absence clause.
//
// The test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 29.3 (Interactive message send, off-holder matrix), 28.5.7
// (shared session-event relay), 15.1 (REST API), 15.2.1 (REST/MCP
// consistency contract).

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

// spec: 29.3, 15.2.1, 28.5.7
// diagnosis: A §29.3 off-holder matrix row that the matrix owns the outcome
//
//	for records that outcome as unstated. The matrix is the normative
//	statement of off-holder behaviour for the session-scoped client routes it
//	names, because the client-to-gateway session REST surface is not a channel
//	in the §28 register and no §28 card owns it. A failure here means the
//	tool-use and elicitation resolution row reverted to deferring its outcome
//	or dropped the forward-to-coordinator requirement or the fail-closed rule
//	for an unreachable coordinator, or the events `Accept: application/json`
//	row no longer serves its envelope from `CH-EVENTRELAY` on the serving
//	replica, or defers its off-holder outcome ahead of its relay-absence
//	clause. Restore the required outcome in the row.
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

	// The events JSON form serves its envelope from the backlog the serving
	// replica reads from the shared session-event relay, which every replica
	// reads, so it requires no forward. The deferral check applies only to
	// the outcome, which precedes the relay-absence clause.
	eventsJSON := requireLine(t, s293, "`Accept: application/json` form |")
	requireAllContain(t, "§29.3 events JSON row", eventsJSON, []string{
		"Serve the paginated list envelope `{items, cursor, hasMore}`",
		"from the backlog the serving replica reads from the shared session-event relay `CH-EVENTRELAY`",
		"so no forwarding to the coordinator is required",
	})
	requireNoneContain(t, "§29.3 events JSON row", eventsJSON, []string{
		"Forward the request to the coordinator",
	})
	requireNoneContain(t, "§29.3 events JSON row outcome", outcomeBeforeRelayAbsence(t, eventsJSON), deferralPhrases)

	// The streaming events row keeps its relay-absence clause, which is a
	// different condition from the off-holder outcome. Its outcome is stated.
	eventsStream := requireLine(t, s293, "`GET /v1/sessions/{id}/events`, streaming form |")
	requireAllContain(t, "§29.3 events streaming row", eventsStream, []string{
		"Serve both the backlog and the live tail from the shared session-event relay `CH-EVENTRELAY`",
	})
}

// spec: 29.3, 28.5.7
// diagnosis: The §29.3 matrix preamble and the matrix rows disagree about
//
//	which rows forward to the coordinator. The preamble states the two
//	requirements that govern every forwarding row, including the fail-closed
//	`TARGET_NOT_READY` outcome for an unreachable coordinator, and it
//	separately lists the rows whose outcome is not a forward. A failure here
//	means the forwarding enumeration is not exactly the interrupt, terminate,
//	delete, resume, interaction-resolution, and upload rows, for example
//	because it lists the events JSON row that the matrix serves from
//	`CH-EVENTRELAY`, or the preamble no longer names the events rows among
//	those served from the shared session-event relay, or it still narrates a
//	row as recording no outcome, so the preamble contradicts the row it
//	describes.
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
	preamble := collapseWhitespace(rest[:end])

	requireAllContain(t, "§29.3 off-holder forwarding preamble", preamble, []string{
		// The rows whose forwarded request is not a message send, named whole.
		"which are the interrupt, terminate, delete, resume, interaction-resolution, and upload rows below,",
		// The fail-closed rule that governs every forwarding row other than a message send.
		"fails closed with `TARGET_NOT_READY`, HTTP 409",
		// Both events rows are among the rows whose outcome is not a forward.
		"the events streaming and `Accept: application/json` rows are served from the shared session-event relay",
	})
	requireNoneContain(t, "§29.3 off-holder forwarding preamble", preamble, []string{
		// The forwarding list that still carried the events JSON row.
		"upload, and events JSON rows below",
		// The narration that recorded the two rows as carrying no outcome.
		"the events JSON row and the interaction-resolution row record that the specification does not state",
	})
}

// relayAbsenceClause opens the sentence of an events row that states the
// reader's outcome when the relay or the requested entry is absent.
const relayAbsenceClause = "The relay is absent"

// outcomeBeforeRelayAbsence returns the part of an events matrix row that
// precedes its relay-absence clause, which is the part stating the
// off-holder outcome. It fails the test when the row carries no such clause,
// so a reworded clause surfaces here rather than silently widening the
// deferral check to the whole row.
func outcomeBeforeRelayAbsence(t *testing.T, row string) string {
	t.Helper()
	idx := strings.Index(row, relayAbsenceClause)
	if idx < 0 {
		t.Fatalf("§29.3 events row carries no relay-absence clause %q (reworded or removed?)", relayAbsenceClause)
	}
	return row[:idx]
}
