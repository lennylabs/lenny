// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the §29.3 off-holder matrix. The
// matrix is the normative statement of what a gateway replica does with a
// session-scoped client route when it is not the replica holding that
// session's pod control stream `CH-ATTACH`, so every row it carries must
// state a required outcome. The four tool-use and elicitation resolution
// routes require no forward: the serving replica records the resolution on
// the Postgres record every replica reads, the first resolution wins, and the
// replica whose call is blocked on the record resolves it. The **Forwarding.**
// rules that precede the matrix govern every row whose outcome is a forward
// over `CH-HOLDERFORWARD`. The `Accept: application/json`
// form of the session events route is served on the serving replica from the
// backlog it reads from the shared session-event relay `CH-EVENTRELAY`, the
// same source as the streaming form, so it requires no forward.
//
// The non-happy path this guards is a matrix row that defers its own
// off-holder outcome. A reader arriving at the matrix for one of those routes
// gets no requirement, and an implementor is free to resolve a blocked call
// against a replica's own pending-request state, or to answer an events page
// from a source other than the relay.
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
// spec: 29.3 (Interactive message send, off-holder matrix and Forwarding.
// rules), 28.5.4 (CH-HOLDERFORWARD), 28.5.7 (shared session-event relay),
// 15.1 (REST API), 15.2.1 (REST/MCP consistency contract).

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
//	tool-use and elicitation resolution row reverted to deferring its outcome,
//	reinstated a forward of the resolution to the coordinator, or dropped the
//	no-forward requirement, the first-resolution-wins rule, or the rule that
//	the serving replica reports no resolution it did not record, or the
//	events `Accept: application/json`
//	row no longer serves its envelope from `CH-EVENTRELAY` on the serving
//	replica, or defers its off-holder outcome ahead of its relay-absence
//	clause. Restore the required outcome in the row.
func TestOffHolderMatrixRowsStateTheirOutcome(t *testing.T) {
	root := repoRoot(t)
	scenarios := filepath.Join(root, "spec", "29_communication-scenarios.md")
	s293 := specSection(t, scenarios, "### 29.3 ")

	// The four tool-use and elicitation resolution routes: the pending
	// request is a Postgres record every replica reads, so the serving
	// replica records the resolution there without a forward, the first
	// resolution wins, and it reports no resolution it did not record.
	resolution := requireLine(t, s293, "/elicitations/{elicitationId}/dismiss` |")
	requireAllContain(t, "§29.3 interaction-resolution row", resolution, []string{
		"No forwarding is required.",
		"which every replica reads and which the gateway writes before it surfaces the request to the client",
		"with a write that takes effect only while that record is pending, so the first resolution wins",
		"The replica whose call is blocked on the record reads the record and resolves the call when the record leaves pending",
		"does not resolve the call against its own pending-request state",
		"does not report a resolution it did not record",
	})
	requireNoneContain(t, "§29.3 interaction-resolution row", resolution, append([]string{
		// The forward the row required before it was restated as store-and-wake.
		"Forward the resolution to the coordinator",
	}, deferralPhrases...))

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

// spec: 29.3, 28.5.4, 28.5.7
// diagnosis: The §29.3 **Forwarding.** rules and the matrix rows disagree
//
//	about which rows forward to the coordinator, or the rules no longer name
//	the carrier. The rules state that a forward runs over `CH-HOLDERFORWARD`,
//	that a not-delivered forward of any request other than a message send
//	fails closed with `TARGET_NOT_READY`, and the closing paragraph lists the
//	rows whose outcome is not a forward. A failure here means the rules name
//	the retired `LNK-INTERREPLICA` link, dropped the fail-closed clause, no
//	longer name the events rows among those served from the shared
//	session-event relay, no longer name the resolution row among those served
//	through its Postgres record, or still list the events JSON row as a
//	forwarding row, so the rules contradict the rows they govern.
func TestOffHolderMatrixPreambleMatchesForwardingRows(t *testing.T) {
	root := repoRoot(t)
	scenarios := filepath.Join(root, "spec", "29_communication-scenarios.md")
	s293 := specSection(t, scenarios, "### 29.3 ")

	// The rules span several paragraphs and a numbered list. Scope from the
	// **Forwarding.** label to the header of the first matrix row.
	const preambleStart = "**Forwarding.**"
	const matrixHeader = "| Route | Session state | Required off-holder outcome |"
	idx := strings.Index(s293, preambleStart)
	if idx < 0 {
		t.Fatalf("§29.3 does not carry the **Forwarding.** rules (renamed or removed?)")
	}
	rest := s293[idx:]
	end := strings.Index(rest, matrixHeader)
	if end < 0 {
		t.Fatalf("§29.3 **Forwarding.** rules are not followed by the off-holder matrix header %q", matrixHeader)
	}
	preamble := collapseWhitespace(rest[:end])

	requireAllContain(t, "§29.3 off-holder forwarding preamble", preamble, []string{
		// The carrier every forwarding row uses.
		"forwards the request to the session's coordinating replica over `CH-HOLDERFORWARD`",
		// Rule 6: the fail-closed outcome of a not-delivered forward of any
		// request other than a message send.
		"Every other request whose forward is not delivered fails closed with `TARGET_NOT_READY`, HTTP 409, carrying `currentState`.",
		// Both events rows are among the rows whose outcome is not a forward.
		"the events streaming and `Accept: application/json` rows are served from the shared session-event relay",
		// The resolution row is served through its Postgres record.
		"the tool-use and elicitation resolution row is served through the Postgres record that row states",
	})
	requireNoneContain(t, "§29.3 off-holder forwarding preamble", preamble, []string{
		// The link entry the forward carrier replaced.
		"LNK-INTERREPLICA",
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
