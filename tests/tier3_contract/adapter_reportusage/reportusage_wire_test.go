//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_reportusage_test is the Tier 3 contract suite for the
// ReportUsageRequest message on the gateway ↔ pod adapter gRPC contract
// (schemas/lenny-adapter.proto). ReportUsage is a gateway-initiated pull:
// the gateway calls it on the adapter (server) to read a direct-mode
// session's token accounting. Proposal 0024 adds a `cumulative` bool flag
// (field 2) so a reconnected gateway replica can pull the session's
// running cumulative total for the §11.2 crash-recovery MAX rule
// (MAX(postgres_checkpoint, pod-reported cumulative total)) rather than
// only the steady-state delta. This suite reads the generated protoreflect
// descriptor (the contract the compiled gateway and adapter exchange) to
// pin the field set and numbering, and it exercises the binary wire form:
// the default (false, steady-state poll) decodes cleanly, and the
// cumulative (true, crash-recovery pull) round-trips.
package adapter_reportusage_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// wantField is one expected field on the ReportUsageRequest wire contract:
// its proto field name, its field number, and its scalar kind.
type wantField struct {
	name   string
	number protoreflect.FieldNumber
	kind   protoreflect.Kind
}

// TestReportUsageRequestWireContract pins the exact field set and numbering
// of ReportUsageRequest after proposal 0024 adds the `cumulative` bool flag
// (field 2). It reads the compiled descriptor, so a field addition, rename,
// or renumber that survives a coordinated proto+regeneration edit is still
// caught. This test fails against the pre-0024 generated code, which
// carried only session_id (field 1) and had no cumulative field.
//
// spec: 4.7 (Runtime Adapter, ReportUsage RPC), 11.2 (crash recovery for
// quota counters, pod-reported cumulative total)
//
// diagnosis: the ReportUsageRequest wire contract diverged from the §4.7
// pull contract. Either the cumulative flag is missing (a reconnected
// gateway replica cannot request the pod-reported cumulative total, so the
// §11.2 MAX-rule recovery reads only a delta and under-counts), or a field
// was renumbered (breaking gateway/adapter binary compatibility on the
// ReportUsage pull). Re-edit schemas/lenny-adapter.proto and run
// `make generate-proto`.
func TestReportUsageRequestWireContract(t *testing.T) {
	md := (&adapterv1.ReportUsageRequest{}).ProtoReflect().Descriptor()

	want := []wantField{
		{name: "session_id", number: 1, kind: protoreflect.MessageKind},
		{name: "cumulative", number: 2, kind: protoreflect.BoolKind},
	}

	fields := md.Fields()
	if got := fields.Len(); got != len(want) {
		t.Fatalf("ReportUsageRequest has %d fields, want %d (cumulative must be present per §11.2 crash recovery)", got, len(want))
	}

	for _, w := range want {
		f := fields.ByName(protoreflect.Name(w.name))
		if f == nil {
			t.Errorf("ReportUsageRequest: field %q missing from the wire contract", w.name)
			continue
		}
		if f.Number() != w.number {
			t.Errorf("ReportUsageRequest: field %q has number %d, want %d", w.name, f.Number(), w.number)
		}
		if f.Kind() != w.kind {
			t.Errorf("ReportUsageRequest: field %q has kind %v, want %v", w.name, f.Kind(), w.kind)
		}
	}
}

// TestReportUsageRequestDefaultCumulativeWireIdentical pins that a
// ReportUsageRequest with cumulative left false (the default steady-state
// poll) encodes to exactly the bytes a pre-0024 request carrying only
// session_id produced. proto3 emits nothing for a false scalar, so the
// steady-state poll must stay byte-identical to before the cumulative field
// was added. This keeps a gateway on the new schema and an adapter on the
// old (or vice versa) in agreement on a default delta pull, and it pins that
// the default decodes cleanly to false.
//
// spec: 4.7 (ReportUsage RPC, incremental delta read), 11.2 (direct-mode
// usage pull)
//
// diagnosis: a failure means adding cumulative to ReportUsageRequest changed
// the default-poll wire encoding — the field was renumbered or emits bytes
// when false — so the steady-state ReportUsage pull drifted between a
// gateway and adapter on mismatched schema versions.
func TestReportUsageRequestDefaultCumulativeWireIdentical(t *testing.T) {
	req := &adapterv1.ReportUsageRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		// Cumulative left false: the steady-state delta poll.
	}
	got, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Golden encoding of session_id (field 1) alone, computed independently
	// of the ReportUsageRequest type so the assertion fails if the cumulative
	// field (field 2) emits any bytes when false. Field wire tags:
	//   field 1 (session_id, message): tag 0x0a
	want := []byte{
		0x0a, 0x08, 0x0a, 0x06, 's', 'e', 's', 's', '-', '1', // session_id { value: "sess-1" }
	}
	if string(got) != string(want) {
		t.Fatalf("default-poll encoding drifted:\n got % x\nwant % x", got, want)
	}

	// The golden bytes must round-trip back to an equal message, and the
	// absent cumulative field must decode to false.
	var back adapterv1.ReportUsageRequest
	if err := proto.Unmarshal(want, &back); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	if !proto.Equal(req, &back) {
		t.Errorf("golden bytes decoded to %v, want %v", &back, req)
	}
	if back.GetCumulative() {
		t.Errorf("default ReportUsageRequest decoded with cumulative=true, want false")
	}
}

// TestReportUsageRequestCumulativeRoundTrip pins that a ReportUsageRequest
// with cumulative set (the crash-recovery pull a reconnected gateway replica
// issues) survives a proto binary marshal/unmarshal with the flag intact, so
// the gateway and the adapter agree that this pull must return the
// session-cumulative total for the §11.2 MAX rule rather than a delta.
//
// spec: 4.7 (ReportUsage RPC, cumulative read), 11.2 (crash recovery for
// quota counters, MAX(postgres_checkpoint, pod-reported cumulative total))
//
// diagnosis: a failure means the cumulative flag was retyped or dropped in
// schemas/lenny-adapter.proto without regenerating the Go, so a reconnected
// gateway replica's crash-recovery pull silently reads a delta and the §11.2
// MAX-rule recovery under-counts, un-recovering the budget protection.
func TestReportUsageRequestCumulativeRoundTrip(t *testing.T) {
	in := &adapterv1.ReportUsageRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		Cumulative: true,
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out adapterv1.ReportUsageRequest
	if err := proto.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(in, &out) {
		t.Fatalf("round-trip mismatch:\n got %v\nwant %v", &out, in)
	}
	if !out.GetCumulative() {
		t.Errorf("cumulative flag lost in round-trip: got false, want true")
	}
}
