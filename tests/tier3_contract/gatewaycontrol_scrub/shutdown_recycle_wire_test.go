//go:build contract

// SPDX-License-Identifier: MIT

// This file extends the Tier 3 GatewayControl scrub-report contract suite
// with the §4.7 Gateway → Adapter recycle-scrub trigger carried on
// ShutdownRequest. It pins two properties of the wire contract: a
// ShutdownRequest with `recycle` unset (the terminate path) encodes
// byte-identically to the pre-change form so the recycle field is a
// backward-compatible addition, and a populated RecycleScrub carries its
// scrub parameters (pod_id, cleanup_commands, cleanup_timeout_seconds)
// through a binary encode/decode. The recycle request no longer carries a
// scrub_profile: the gateway resolves the §5.2 recycle disposition
// (reuse for standard/in-place, retire-and-reprovision for vm-restart) from
// its own runtime store rather than echoing the profile on the wire. spec:
// §4.7 (Shutdown recycle disposition); §5.2 (whole-pod scrub trigger,
// fresh-guest reprovision).
package gatewaycontrol_scrub_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// TestShutdownRequestUnsetRecycleWireIdentical pins that a ShutdownRequest
// with `recycle` unset (the terminate path) encodes to exactly the bytes a
// pre-change ShutdownRequest carrying only fields 1-4 produced. The recycle
// sub-message is field 5; proto3 emits nothing for an unset message field,
// so the terminate path must stay byte-identical to before the recycle
// field was added. The expected bytes are the deterministic encoding of the
// session_id, reason, and deadline_ms fields alone.
// spec: 4.7 (Shutdown recycle disposition), 5.2 (whole-pod scrub trigger)
//
// diagnosis: a failure means adding RecycleScrub to ShutdownRequest changed
// the terminate-path wire encoding — a field was renumbered or the recycle
// field emits bytes when unset — so a gateway on the new schema and an
// adapter on the old (or vice versa) would disagree on a plain Shutdown and
// the non-recycle shutdown path would drift.
func TestShutdownRequestUnsetRecycleWireIdentical_spec_4_7(t *testing.T) {
	req := &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		Reason:     "drain",
		DeadlineMs: 5000,
		// Recycle left nil: the terminate path.
	}
	got, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Golden encoding of fields 1-3 only, computed independently of the
	// ShutdownRequest type so the assertion would fail if the recycle field
	// (field 5) emitted any bytes when unset. Field 4 is reserved: the
	// duplicate slot address came off the message, and the encoding must
	// carry no bytes for it. Field wire tags:
	//   field 1 (session_id, message): tag 0x0a
	//   field 2 (reason, string):      tag 0x12
	//   field 3 (deadline_ms, varint): tag 0x18
	want := []byte{
		0x0a, 0x08, 0x0a, 0x06, 's', 'e', 's', 's', '-', '1', // session_id { value: "sess-1" }
		0x12, 0x05, 'd', 'r', 'a', 'i', 'n', // reason: "drain"
		0x18, 0x88, 0x27, // deadline_ms: 5000
	}
	if string(got) != string(want) {
		t.Fatalf("terminate-path encoding drifted:\n got % x\nwant % x", got, want)
	}

	// The golden bytes must also round-trip back to an equal message so the
	// hand-computed encoding is not merely different-but-self-consistent.
	var back adapterv1.ShutdownRequest
	if err := proto.Unmarshal(want, &back); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	if !proto.Equal(req, &back) {
		t.Errorf("golden bytes decoded to %v, want %v", &back, req)
	}
	if back.GetRecycle() != nil {
		t.Errorf("terminate-path ShutdownRequest decoded with a non-nil recycle: %v", back.GetRecycle())
	}
}

// TestShutdownRequestRecycleScrubRoundTrip pins that a ShutdownRequest
// carrying a populated RecycleScrub survives a proto binary
// marshal/unmarshal with its scrub parameters intact (pod_id,
// cleanup_commands, cleanup_timeout_seconds), so the gateway and the adapter
// agree on the recycle-scrub trigger. The message carries no scrub_profile:
// proposal 0034 removed the write-only wire field, so the gateway resolves
// the §5.2 recycle disposition from its own runtime store rather than
// echoing the profile on the wire.
// spec: 4.7 (Shutdown recycle disposition), 5.2 (whole-pod scrub trigger,
// fresh-guest reprovision)
//
// diagnosis: a failure means a field of RecycleScrub was renumbered,
// retyped, or dropped in schemas/lenny-adapter.proto without regenerating
// the Go, so the recycle-scrub parameters no longer round-trip and the
// adapter would run the §5.2 whole-pod scrub with truncated or misread
// cleanup commands or timeout.
func TestShutdownRequestRecycleScrubRoundTrip_spec_5_2(t *testing.T) {
	in := &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		Reason:     "recycle",
		DeadlineMs: 5000,
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 "sandbox-42",
			CleanupCommands:       []string{"rm -rf /tmp/scratch", "truncate --size 0 /var/log/agent.log"},
			CleanupTimeoutSeconds: 30,
		},
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out adapterv1.ShutdownRequest
	if err := proto.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(in, &out) {
		t.Fatalf("round-trip mismatch:\n got %v\nwant %v", &out, in)
	}

	// Assert each surviving RecycleScrub field explicitly so a silent
	// drop of one field (that still round-trips because both sides drop it)
	// is caught.
	rc := out.GetRecycle()
	if rc == nil {
		t.Fatal("recycle sub-message lost in round-trip")
	}
	if rc.GetPodId() != "sandbox-42" {
		t.Errorf("pod_id = %q, want %q", rc.GetPodId(), "sandbox-42")
	}
	if got := rc.GetCleanupCommands(); len(got) != 2 ||
		got[0] != "rm -rf /tmp/scratch" ||
		got[1] != "truncate --size 0 /var/log/agent.log" {
		t.Errorf("cleanup_commands = %q, want the two submitted commands", got)
	}
	if rc.GetCleanupTimeoutSeconds() != 30 {
		t.Errorf("cleanup_timeout_seconds = %d, want 30", rc.GetCleanupTimeoutSeconds())
	}
}

// TestRecycleScrubHasNoScrubProfileField pins that proposal 0034 removed the
// write-only scrub_profile wire field (field 4) from RecycleScrub, so the
// gateway no longer echoes the recycle scrub profile on the wire and resolves
// the §5.2 recycle disposition from its own runtime store instead. It asserts
// the message descriptor carries exactly the three surviving scrub-parameter
// fields (pod_id=1, cleanup_commands=2, cleanup_timeout_seconds=3) and that
// no field is numbered 4 or named scrub_profile. This assertion fails against
// the pre-0034 proto that still declared `string scrub_profile = 4;`.
// spec: 5.2 (fresh-guest reprovision, delivered-parameter enumeration),
// 4.7 (Shutdown recycle disposition)
//
// diagnosis: a failure means the scrub_profile wire field was reintroduced (or
// never removed) on RecycleScrub, so the gateway would ship a dead write-only
// profile echo the adapter no longer reads, contradicting the retire-and-
// reprovision reconciliation.
func TestRecycleScrubHasNoScrubProfileField_spec_5_2(t *testing.T) {
	fields := (&adapterv1.RecycleScrub{}).ProtoReflect().Descriptor().Fields()

	// The surviving field set, keyed by field number, must be exactly these
	// three scrub parameters. A fourth field or a scrub_profile name is the
	// removed wire echo resurfacing.
	want := map[protoreflect.FieldNumber]protoreflect.Name{
		1: "pod_id",
		2: "cleanup_commands",
		3: "cleanup_timeout_seconds",
	}
	if got := fields.Len(); got != len(want) {
		t.Fatalf("RecycleScrub field count = %d, want %d (the removal of scrub_profile leaves three fields)", got, len(want))
	}
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.Name() == "scrub_profile" {
			t.Fatalf("RecycleScrub still declares a scrub_profile field (number %d); proposal 0034 removed the write-only wire echo", f.Number())
		}
		if wantName, ok := want[f.Number()]; !ok || wantName != f.Name() {
			t.Errorf("unexpected RecycleScrub field %d = %q; the surviving set is pod_id=1, cleanup_commands=2, cleanup_timeout_seconds=3", f.Number(), f.Name())
		}
	}
	if f := fields.ByNumber(4); f != nil {
		t.Errorf("RecycleScrub declares field 4 = %q; field 4 (scrub_profile) was removed", f.Name())
	}
	if f := fields.ByName("scrub_profile"); f != nil {
		t.Errorf("RecycleScrub still resolves a scrub_profile field by name (number %d)", f.Number())
	}
}

// TestShutdownMessagePostRemovalDescriptor pins the whole of the shutdown
// message after the duplicate address came off it: ShutdownRequest declares
// exactly session_id, reason, deadline_ms, recycle, and
// coordination_generation, reserves the number and the name the duplicate
// held, and carries no field of the retired wrapper type; ShutdownResponse
// declares exactly exited_cleanly and exit_code; and the Shutdown RPC is
// declared on service Adapter, which is the single end-of-session teardown
// the gateway calls on every release.
// spec: 4.1 (one address per request), 4.7 (Shutdown), 5.2 (a session-mode
// slot's identifier is its session's identifier)
//
// diagnosis: a failure means the shutdown message drifted from the
// post-removal contract — the duplicate address came back, a removed number
// was recycled, a field was added or dropped, or the RPC moved off service
// Adapter. Every one of those changes the bytes on the teardown path, which
// no round-trip case above would catch because both of its ends regenerate
// from the same proto.
func TestShutdownMessagePostRemovalDescriptor_spec_4_1(t *testing.T) {
	reqDesc := (&adapterv1.ShutdownRequest{}).ProtoReflect().Descriptor()

	wantReq := map[protoreflect.FieldNumber]protoreflect.Name{
		1: "session_id",
		2: "reason",
		3: "deadline_ms",
		5: "recycle",
		6: "coordination_generation",
	}
	assertFieldSet(t, reqDesc, wantReq)

	if !reservesNumber(reqDesc, 4) {
		t.Error("ShutdownRequest does not reserve field number 4, which the duplicate address held")
	}
	if !reservesName(reqDesc, "slot_id") {
		t.Error(`ShutdownRequest does not reserve the name "slot_id"`)
	}
	for i := 0; i < reqDesc.Fields().Len(); i++ {
		f := reqDesc.Fields().Get(i)
		if f.Kind() == protoreflect.MessageKind && f.Message().FullName() == "lenny.adapter.v1.SlotId" {
			t.Errorf("ShutdownRequest.%s carries the retired address wrapper", f.Name())
		}
	}

	assertFieldSet(t, (&adapterv1.ShutdownResponse{}).ProtoReflect().Descriptor(),
		map[protoreflect.FieldNumber]protoreflect.Name{
			1: "exited_cleanly",
			2: "exit_code",
		})

	svcs := reqDesc.ParentFile().Services()
	adapter := svcs.ByName("Adapter")
	if adapter == nil {
		t.Fatal("the adapter proto declares no service Adapter")
	}
	rpc := adapter.Methods().ByName("Shutdown")
	if rpc == nil {
		t.Fatal("service Adapter declares no Shutdown RPC; it is the one end-of-session teardown")
	}
	if got := rpc.Input().FullName(); got != "lenny.adapter.v1.ShutdownRequest" {
		t.Errorf("Adapter.Shutdown takes %s, want lenny.adapter.v1.ShutdownRequest", got)
	}
	if got := rpc.Output().FullName(); got != "lenny.adapter.v1.ShutdownResponse" {
		t.Errorf("Adapter.Shutdown returns %s, want lenny.adapter.v1.ShutdownResponse", got)
	}
}

// assertFieldSet reports every difference between md's declared fields and
// want, keyed by field number, so a drift names the field rather than a
// count alone.
func assertFieldSet(t *testing.T, md protoreflect.MessageDescriptor, want map[protoreflect.FieldNumber]protoreflect.Name) {
	t.Helper()
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		wantName, ok := want[f.Number()]
		if !ok {
			t.Errorf("%s declares an unexpected field %d = %q", md.Name(), f.Number(), f.Name())
			continue
		}
		if wantName != f.Name() {
			t.Errorf("%s field %d = %q, want %q", md.Name(), f.Number(), f.Name(), wantName)
		}
	}
	for num, name := range want {
		if fields.ByNumber(num) == nil {
			t.Errorf("%s declares no field %d (%s)", md.Name(), num, name)
		}
	}
}

// reservesNumber reports whether md reserves the given field number.
func reservesNumber(md protoreflect.MessageDescriptor, num protoreflect.FieldNumber) bool {
	ranges := md.ReservedRanges()
	for i := 0; i < ranges.Len(); i++ {
		r := ranges.Get(i)
		if num >= r[0] && num < r[1] {
			return true
		}
	}
	return false
}

// reservesName reports whether md reserves the given field name.
func reservesName(md protoreflect.MessageDescriptor, name protoreflect.Name) bool {
	names := md.ReservedNames()
	for i := 0; i < names.Len(); i++ {
		if names.Get(i) == name {
			return true
		}
	}
	return false
}
