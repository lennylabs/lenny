// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance case for the event-taxonomy pointer in the published
// gateway ↔ adapter gRPC contract. §28.7 lists schemas/lenny-adapter.proto
// as a wire-contract artifact whose consumers are the runtime authors
// implementing the adapter contract and the external-adapter compliance
// suite, so the taxonomy pointer in that artifact is part of what a runtime
// author is handed: it is how the author finds the events a conforming
// adapter emits on CH-ADAPTEREVENTS.
//
// Both comments on that stream, the AdapterEvents RPC doc comment and the
// envelope comment over AdapterEventsRequest and AdapterEventsResponse,
// name the adapter→gateway events table in spec/04_system-components.md.
// This case asserts the pointer resolves: the §4.7.3 table states every
// event the contract names, and it states none of the intra-pod frames the
// §28.5.3 CH-RUNTIMEOPS card owns. A substring check on the proto alone
// cannot establish either.
//
// spec: §4.7.3 (adapter events on CH-ADAPTEREVENTS), §28.5.3 (intra-pod
// contract cards), §28.7 (wire-contract artifact register).
package tier10_conformance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// eventTableHeading opens the §4.7.3 adapter→gateway events table.
const eventTableHeading = "#### 4.7.3 Adapter Events on CH-ADAPTEREVENTS"

// intraPodCardHeading opens the §28.5.3 card set that owns the
// runtime-operations frames.
const intraPodCardHeading = "#### 28.5.3 Intra-pod"

// TestAdapterContractEventTaxonomyPointerResolves asserts that the taxonomy
// pointer the published contract carries lands on a section stating the
// event set the contract names, and that the frames the contract no longer
// credits to the stream are stated by the card that owns them.
//
// diagnosis: a runtime author following the taxonomy pointer out of
// schemas/lenny-adapter.proto reaches a section that does not state the
// adapter→gateway events the comment sent them to look up, or reaches one
// that also states the intra-pod runtime-operations frames, so the event
// set a conforming adapter must emit is unreachable or ambiguous from the
// artifact the author is handed.
//
// spec: 4.7.3 (adapter events on CH-ADAPTEREVENTS), 28.5.3 (intra-pod
// contract cards), 28.7 (wire-contract artifact register).
func TestAdapterContractEventTaxonomyPointerResolves(t *testing.T) {
	root := conformanceRepoRoot(t)

	protoSrc, err := os.ReadFile(filepath.Join(root, "schemas/lenny-adapter.proto"))
	if err != nil {
		t.Fatalf("read schemas/lenny-adapter.proto: %v", err)
	}
	if !strings.Contains(string(protoSrc), "spec/04_system-components.md") {
		t.Fatalf("schemas/lenny-adapter.proto carries no adapter→gateway events-table pointer")
	}

	componentsSrc, err := os.ReadFile(filepath.Join(root, "spec/04_system-components.md"))
	if err != nil {
		t.Fatalf("read spec/04_system-components.md: %v", err)
	}
	table := sectionBody(t, string(componentsSrc), eventTableHeading)

	// Each event the contract's RPC comment names must be stated by the
	// table the comment points at.
	for _, want := range []string{
		"RATE_LIMITED",
		"AUTH_EXPIRED",
		"PROVIDER_UNAVAILABLE",
		"LEASE_REJECTED",
		"AdapterTerminating",
		"FINAL_USAGE_REPORT",
		"CheckpointBarrierAck",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("§4.7.3 must state %q for the contract taxonomy pointer to resolve", want)
		}
	}

	// The intra-pod frames the contract stopped crediting to this stream
	// must not reappear in the table, or the pointer would send the
	// author back to the event set the correction removed.
	channelsSrc, err := os.ReadFile(filepath.Join(root, "spec/28_communication-channels.md"))
	if err != nil {
		t.Fatalf("read spec/28_communication-channels.md: %v", err)
	}
	card := sectionBody(t, string(channelsSrc), intraPodCardHeading)
	for _, frame := range []string{
		"checkpoint_ready",
		"interrupt_acknowledged",
		"credentials_acknowledged",
		"deadline_approaching",
	} {
		if strings.Contains(table, frame) {
			t.Errorf("§4.7.3 states the intra-pod frame %q; the adapter→gateway event set is ambiguous", frame)
		}
		if !strings.Contains(card, frame) {
			t.Errorf("§28.5.3 must state %q, the card that owns the frame the contract no longer credits to CH-ADAPTEREVENTS", frame)
		}
	}
}
