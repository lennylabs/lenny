//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_slot_identity_test is the Tier 3 contract suite for the slot
// identifier on the per-slot gateway → pod adapter gRPC requests
// (schemas/lenny-adapter.proto). A pod whose
// `sessionPolicy.maxConcurrentSessions` is greater than one serves several
// sessions at once, each in its own §6.4 slot with its own workspace tree,
// credential directory, and runtime process. A request that addresses one of
// those sessions is therefore received per slot rather than per pod, and it can
// only be routed to the right slot where the request message carries the slot
// identifier.
//
// This suite reads the generated protoreflect descriptor to pin `slot_id`, at
// its assigned number and as the `lenny.adapter.v1.SlotId` message type, on the
// per-slot request messages, and exercises the binary wire form: an unset slot
// identifier adds no bytes so the whole-pod (`maxConcurrentSessions: 1`) form
// of each request is unchanged, a set identifier round-trips, and a present
// identifier holding the empty value stays distinguishable from an absent one.
//
// The pinned set is enumerated in perSlotMessages and its boundary is stated
// there.
package adapter_slot_identity_test

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// slotFieldName is the proto field name of the slot identifier.
const slotFieldName = "slot_id"

// slotIDFullName is the message type every slot identifier field carries.
const slotIDFullName = protoreflect.FullName("lenny.adapter.v1.SlotId")

// perSlotMessage is one per-slot gateway-to-pod request message together with
// the field number its slot identifier is assigned.
type perSlotMessage struct {
	msg    proto.Message
	number protoreflect.FieldNumber
}

// perSlotMessages enumerates the request messages this suite pins and the slot
// identifier field number each assigns. The set is the requests a gateway
// sends against one session on a pod that may be serving several: the
// interrupt signal, the deadline warning, the usage pull, the drain barrier,
// and the resume that restores a checkpoint into a slot's workspace.
//
// The boundary of the set: the messages that already carried the identifier
// before this contract widened (the workspace and credential calls,
// StartSession, CheckpointStart, and Shutdown) are pinned by their own suites
// and are not restated here. CoordinatorFenceRequest and the pod-level calls
// address the pod rather than one of its slots and carry no identifier.
var perSlotMessages = map[string]perSlotMessage{
	"InterruptRequest":         {msg: &adapterv1.InterruptRequest{}, number: 5},
	"SignalDeadlineRequest":    {msg: &adapterv1.SignalDeadlineRequest{}, number: 5},
	"ReportUsageRequest":       {msg: &adapterv1.ReportUsageRequest{}, number: 4},
	"CheckpointBarrierRequest": {msg: &adapterv1.CheckpointBarrierRequest{}, number: 4},
	"ResumeRequest":            {msg: &adapterv1.ResumeRequest{}, number: 15},
}

// TestPerSlotRequestsCarrySlotIdentifier pins that each request message in
// perSlotMessages declares the slot identifier at its assigned number as a
// singular `lenny.adapter.v1.SlotId`, outside any oneof and outside any
// reserved range the message carries, so a request that names one slot of a
// concurrent pod can be routed to it.
//
// spec: 6.4 (per-slot pod filesystem layout on a pod running concurrent
// sessions), 4.7 (Runtime Adapter gRPC surface)
//
// diagnosis: a per-slot gateway-to-pod request message lost its slot
// identifier, or the identifier was renumbered or retyped. Without it the pod
// cannot tell which of its concurrent §6.4 slots the interrupt, deadline
// warning, usage pull, barrier, or resume addresses, so the request either
// applies to the whole pod or to the wrong tenant's session. A renumber breaks
// binary compatibility between a gateway and an adapter built from different
// revisions of the contract. Re-edit schemas/lenny-adapter.proto and run
// `make generate-proto`.
func TestPerSlotRequestsCarrySlotIdentifier(t *testing.T) {
	for name, want := range perSlotMessages {
		md := want.msg.ProtoReflect().Descriptor()
		f := md.Fields().ByName(slotFieldName)
		if f == nil {
			t.Errorf("%s: %s field missing from the wire contract", name, slotFieldName)
			continue
		}
		if f.Number() != want.number {
			t.Errorf("%s: %s has number %d, want %d", name, slotFieldName, f.Number(), want.number)
		}
		if f.Kind() != protoreflect.MessageKind {
			t.Errorf("%s: %s has kind %v, want %v", name, slotFieldName, f.Kind(), protoreflect.MessageKind)
			continue
		}
		if got := f.Message().FullName(); got != slotIDFullName {
			t.Errorf("%s: %s carries %s, want %s", name, slotFieldName, got, slotIDFullName)
		}
		if f.IsList() || f.IsMap() {
			t.Errorf("%s: %s must be a singular field", name, slotFieldName)
		}
		if oo := f.ContainingOneof(); oo != nil {
			t.Errorf("%s: %s sits inside oneof %q; the identifier addresses the request and must be readable alongside any other field", name, slotFieldName, oo.Name())
		}
		for i := 0; i < md.ReservedRanges().Len(); i++ {
			r := md.ReservedRanges().Get(i)
			if f.Number() >= r[0] && f.Number() < r[1] {
				t.Errorf("%s: %s took number %d from the reserved range [%d,%d)", name, slotFieldName, f.Number(), r[0], r[1])
			}
		}
	}
}

// TestUnsetSlotIdentifierAddsNoBytes pins that a request that names no slot
// encodes to exactly the bytes the same request encoded before the identifier
// existed. proto3 emits nothing for an absent message field, so a pod whose
// `maxConcurrentSessions` is 1, where a session claims the whole pod and the
// gateway sets no identifier, sends the bytes it always sent.
//
// spec: 6.4 (slot identifier empty where a session claims the whole pod), 4.7
// (Runtime Adapter gRPC surface)
//
// diagnosis: adding the slot identifier changed the encoding of a request that
// does not name a slot, so a gateway and an adapter built from different
// revisions of the contract disagree on the bytes of a whole-pod request.
func TestUnsetSlotIdentifierAddsNoBytes(t *testing.T) {
	// session_id (field 1) alone, computed independently of the message types
	// under test: field 1, wire type 2 (0x0a), holding SessionId{value:"sess-1"}.
	want := []byte{0x0a, 0x08, 0x0a, 0x06, 's', 'e', 's', 's', '-', '1'}
	sid := &adapterv1.SessionId{Value: "sess-1"}

	slotless := map[string]proto.Message{
		"InterruptRequest":         &adapterv1.InterruptRequest{SessionId: sid},
		"SignalDeadlineRequest":    &adapterv1.SignalDeadlineRequest{SessionId: sid},
		"ReportUsageRequest":       &adapterv1.ReportUsageRequest{SessionId: sid},
		"CheckpointBarrierRequest": &adapterv1.CheckpointBarrierRequest{SessionId: sid},
		"ResumeRequest":            &adapterv1.ResumeRequest{SessionId: sid},
	}
	for name, m := range slotless {
		got, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s: unset-slot encoding drifted:\n got % x\nwant % x", name, got, want)
		}
	}
}

// TestSlotIdentifierRoundTrips pins that a set slot identifier survives a
// binary marshal and unmarshal on every request message in perSlotMessages,
// including the empty-string value a caller can write into a present SlotId. A
// slot identifier that does not survive the round trip routes the request to
// the wrong slot.
//
// spec: 6.4 (per-slot pod filesystem layout), 4.7 (Runtime Adapter gRPC
// surface)
//
// diagnosis: the slot identifier was retyped or renumbered in
// schemas/lenny-adapter.proto without a regeneration, so the slot a gateway
// names is not the slot the pod reads and a per-slot request lands on a
// sibling tenant's session.
func TestSlotIdentifierRoundTrips(t *testing.T) {
	values := []string{"slot-2", "", strings.Repeat("s", 512)}
	for _, v := range values {
		for name, pm := range perSlotMessages {
			in := proto.Clone(pm.msg)
			setSlot(t, in, &adapterv1.SlotId{Value: v})

			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("%s: marshal: %v", name, err)
			}
			out := in.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(raw, out); err != nil {
				t.Fatalf("%s: unmarshal: %v", name, err)
			}
			got := slot(t, out)
			if got == nil {
				t.Errorf("%s: slot identifier %q round-tripped to absent", name, v)
				continue
			}
			if got.GetValue() != v {
				t.Errorf("%s: slot identifier round-tripped to %q, want %q", name, got.GetValue(), v)
			}
		}
	}
}

// TestPresentEmptySlotIdentifierDiffersFromAbsent pins the presence semantics
// the routing depends on: a request carrying a SlotId whose value is empty
// encodes differently from a request carrying no SlotId at all, so a pod can
// tell a malformed per-slot request from a whole-pod request rather than
// silently treating the first as the second.
//
// spec: 6.4 (slot identifier empty where a session claims the whole pod), 4.7
// (Runtime Adapter gRPC surface)
//
// diagnosis: the slot identifier lost message presence, most likely by being
// flattened to a bare string. A pod can then no longer distinguish a per-slot
// request whose identifier failed to populate from a legitimate whole-pod
// request, and it applies the request to every session on the pod.
func TestPresentEmptySlotIdentifierDiffersFromAbsent(t *testing.T) {
	for name, pm := range perSlotMessages {
		absent, err := proto.MarshalOptions{Deterministic: true}.Marshal(proto.Clone(pm.msg))
		if err != nil {
			t.Fatalf("%s: marshal absent: %v", name, err)
		}
		present := proto.Clone(pm.msg)
		setSlot(t, present, &adapterv1.SlotId{})
		raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(present)
		if err != nil {
			t.Fatalf("%s: marshal present: %v", name, err)
		}
		if string(raw) == string(absent) {
			t.Errorf("%s: a present empty slot identifier encodes identically to an absent one (% x)", name, raw)
		}
		out := pm.msg.ProtoReflect().New().Interface()
		if err := proto.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: unmarshal present: %v", name, err)
		}
		if slot(t, out) == nil {
			t.Errorf("%s: a present empty slot identifier decoded as absent", name)
		}
	}
}

// setSlot writes id into the message's slot identifier field through
// protoreflect, so the helper works over every message under test without a
// per-type switch.
func setSlot(t *testing.T, m proto.Message, id *adapterv1.SlotId) {
	t.Helper()
	r := m.ProtoReflect()
	f := r.Descriptor().Fields().ByName(slotFieldName)
	if f == nil {
		t.Fatalf("%s: %s field missing", r.Descriptor().Name(), slotFieldName)
	}
	r.Set(f, protoreflect.ValueOfMessage(id.ProtoReflect()))
}

// slot reads the message's slot identifier field through protoreflect,
// returning nil when the field is absent.
func slot(t *testing.T, m proto.Message) *adapterv1.SlotId {
	t.Helper()
	r := m.ProtoReflect()
	f := r.Descriptor().Fields().ByName(slotFieldName)
	if f == nil {
		t.Fatalf("%s: %s field missing", r.Descriptor().Name(), slotFieldName)
	}
	if !r.Has(f) {
		return nil
	}
	return r.Get(f).Message().Interface().(*adapterv1.SlotId)
}
