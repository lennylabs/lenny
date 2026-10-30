//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_bind_attempt_test is the Tier 3 contract suite for the bind
// attempt token on the gateway → pod adapter gRPC contract
// (schemas/lenny-adapter.proto). The gateway mints one token per bind attempt
// and carries it on the requests that create or resolve a slot registry
// entry; the adapter stamps it on the entry it creates and compares it on
// every later resolve, and a Shutdown either names the attempt it compensates
// or asks for the unconditional teardown and reports what became of the
// entry (§4.7.1). This file is the descriptor-and-bytes gate: it reads the
// generated protoreflect descriptors to pin every field, error code, and
// outcome value that contract adds, at its assigned number and kind, and it
// checks that each added field is additive on the wire.
package adapter_bind_attempt_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// bindAttemptField is one field the bind attempt contract adds to a message,
// pinned by name, number, and kind.
type bindAttemptField struct {
	msg    proto.Message
	name   protoreflect.Name
	number protoreflect.FieldNumber
	kind   protoreflect.Kind
}

// bindAttemptFields enumerates every field the contract adds. The token rides
// the requests the §4.7.1 carriage table lists as carrying it; StartSession
// and ConfigureWorkspace carry none because the start issues them without
// minting a token. PrepareWorkspace gains the mid_session marker that
// FinalizeWorkspace already carried at field 4. Shutdown carries the two
// halves of the teardown precondition, and its response carries the reclaim
// outcome.
var bindAttemptFields = []bindAttemptField{
	{msg: &adapterv1.PrepareWorkspaceRequest{}, name: "bind_attempt", number: 5, kind: protoreflect.StringKind},
	{msg: &adapterv1.PrepareWorkspaceRequest{}, name: "mid_session", number: 6, kind: protoreflect.BoolKind},
	{msg: &adapterv1.FinalizeWorkspaceRequest{}, name: "bind_attempt", number: 6, kind: protoreflect.StringKind},
	{msg: &adapterv1.RunSetupRequest{}, name: "bind_attempt", number: 5, kind: protoreflect.StringKind},
	{msg: &adapterv1.AssignCredentialsRequest{}, name: "bind_attempt", number: 4, kind: protoreflect.StringKind},
	{msg: &adapterv1.ResumeRequest{}, name: "bind_attempt", number: 16, kind: protoreflect.StringKind},
	{msg: &adapterv1.ShutdownRequest{}, name: "bind_attempt", number: 7, kind: protoreflect.StringKind},
	{msg: &adapterv1.ShutdownRequest{}, name: "unconditional_teardown", number: 8, kind: protoreflect.BoolKind},
	{msg: &adapterv1.ShutdownResponse{}, name: "slot_reclaim", number: 3, kind: protoreflect.EnumKind},
}

// TestBindAttemptFieldsPinnedByNumberAndKind pins each field the bind attempt
// contract adds, by message, number, name, and kind, and confirms each is a
// bare scalar rather than a presence-tracked optional.
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4
// (runtime adapter specification)
//
// diagnosis: a failure means a bind attempt field was renumbered, renamed,
// retyped, moved to another message, or made optional. A third-party adapter
// built from the published proto would then read the token, the mid-session
// marker, the teardown precondition, or the reclaim outcome from a different
// field than the gateway writes, which no in-repository round trip catches
// because both ends regenerate from the same file.
func TestBindAttemptFieldsPinnedByNumberAndKind(t *testing.T) {
	for _, want := range bindAttemptFields {
		md := want.msg.ProtoReflect().Descriptor()
		f := md.Fields().ByNumber(want.number)
		if f == nil {
			t.Errorf("%s declares no field %d (%s)", md.Name(), want.number, want.name)
			continue
		}
		if f.Name() != want.name {
			t.Errorf("%s field %d = %q, want %q", md.Name(), want.number, f.Name(), want.name)
		}
		if f.Kind() != want.kind {
			t.Errorf("%s.%s kind = %s, want %s", md.Name(), want.name, f.Kind(), want.kind)
		}
		if f.Cardinality() != protoreflect.Optional || f.HasPresence() {
			t.Errorf("%s.%s must be a bare singular scalar without presence tracking", md.Name(), want.name)
		}
	}
	slot := (&adapterv1.ShutdownResponse{}).ProtoReflect().Descriptor().Fields().ByName("slot_reclaim")
	if slot != nil && slot.Enum().FullName() != "lenny.adapter.v1.SlotReclaimOutcome" {
		t.Errorf("ShutdownResponse.slot_reclaim is typed %s, want lenny.adapter.v1.SlotReclaimOutcome", slot.Enum().FullName())
	}
}

// TestBindAttemptTokenAbsentFromStartRequests pins the carriage table's two
// exclusions: StartSession and ConfigureWorkspace carry no token and no
// mid-session marker.
// spec: §4.7.1 (role and gateway RPC contract)
//
// diagnosis: a failure means a token field was added to a start request. The
// start may be issued by a later stage of the same binding than the stage that
// created the entry, so a token compared on it would refuse a start against an
// entry the same binding created.
func TestBindAttemptTokenAbsentFromStartRequests(t *testing.T) {
	for _, m := range []proto.Message{&adapterv1.StartSessionRequest{}, &adapterv1.ConfigureWorkspaceRequest{}} {
		md := m.ProtoReflect().Descriptor()
		for _, name := range []protoreflect.Name{"bind_attempt", "mid_session"} {
			if f := md.Fields().ByName(name); f != nil {
				t.Errorf("%s declares %s at field %d; the carriage table excludes it", md.Name(), name, f.Number())
			}
		}
	}
}

// TestSlotBindRefusalCodesPinned pins the two slot-bind refusal codes by number
// and name on the adapter's error envelope.
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a failure means a slot-bind refusal code was renumbered, renamed,
// or its number reused. The gateway branches on these codes to answer a
// client with the retryable fallback, so a drifted number turns a refusal from
// a third-party adapter into a different error.
func TestSlotBindRefusalCodesPinned(t *testing.T) {
	ed := adapterv1.Error_ERROR_CODE_UNSPECIFIED.Descriptor()
	want := map[protoreflect.EnumNumber]protoreflect.Name{
		28: "ERROR_CODE_SLOT_BIND_ALREADY_STARTED",
		29: "ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED",
	}
	for num, name := range want {
		v := ed.Values().ByNumber(num)
		if v == nil {
			t.Errorf("ErrorCode declares no value %d (%s)", num, name)
			continue
		}
		if v.Name() != name {
			t.Errorf("ErrorCode value %d = %q, want %q", num, v.Name(), name)
		}
	}
	if got := adapterv1.Error_ERROR_CODE_SLOT_BIND_ALREADY_STARTED; got != 28 {
		t.Errorf("generated ERROR_CODE_SLOT_BIND_ALREADY_STARTED = %d, want 28", got)
	}
	if got := adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED; got != 29 {
		t.Errorf("generated ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED = %d, want 29", got)
	}
}

// TestSlotReclaimOutcomeClosedValueSet pins every SlotReclaimOutcome value by
// name and number and rejects any value outside that set.
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// diagnosis: a failure means a reclaim outcome was renumbered, renamed, added,
// or removed. The gateway's compensation branches on the outcome every
// Shutdown response carries, so an adapter built against a different
// numbering reports an outcome other than the one it means.
func TestSlotReclaimOutcomeClosedValueSet(t *testing.T) {
	want := map[protoreflect.EnumNumber]protoreflect.Name{
		0: "SLOT_RECLAIM_OUTCOME_UNSPECIFIED",
		1: "SLOT_RECLAIM_OUTCOME_RECLAIMED",
		2: "SLOT_RECLAIM_OUTCOME_ABSENT",
		3: "SLOT_RECLAIM_OUTCOME_SUPERSEDED",
	}
	ed := adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_UNSPECIFIED.Descriptor()
	if ed.FullName() != "lenny.adapter.v1.SlotReclaimOutcome" {
		t.Fatalf("SlotReclaimOutcome full name = %s", ed.FullName())
	}
	values := ed.Values()
	for i := 0; i < values.Len(); i++ {
		v := values.Get(i)
		name, ok := want[v.Number()]
		if !ok {
			t.Errorf("SlotReclaimOutcome declares an unexpected value %d = %q", v.Number(), v.Name())
			continue
		}
		if v.Name() != name {
			t.Errorf("SlotReclaimOutcome value %d = %q, want %q", v.Number(), v.Name(), name)
		}
	}
	for num, name := range want {
		if values.ByNumber(num) == nil {
			t.Errorf("SlotReclaimOutcome declares no value %d (%s)", num, name)
		}
	}
}

// TestBindAttemptFieldsAreAdditiveOnTheWire checks that each added field adds
// no bytes when unset, so a peer built before the fields existed reads the
// same encoding, and that a set value round-trips at its number.
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
//
// diagnosis: a failure means an added field serialises a zero value or does
// not survive a marshal and unmarshal, so the wire form of a request that
// carries no token differs from the form an earlier peer produces.
func TestBindAttemptFieldsAreAdditiveOnTheWire(t *testing.T) {
	for _, want := range bindAttemptFields {
		md := want.msg.ProtoReflect().Descriptor()
		empty, err := proto.Marshal(want.msg.ProtoReflect().New().Interface())
		if err != nil {
			t.Fatalf("marshal empty %s: %v", md.Name(), err)
		}
		if len(empty) != 0 {
			t.Errorf("empty %s encodes %d bytes, want 0", md.Name(), len(empty))
		}

		set := want.msg.ProtoReflect().New()
		fd := md.Fields().ByNumber(want.number)
		if fd == nil {
			t.Errorf("%s declares no field %d", md.Name(), want.number)
			continue
		}
		set.Set(fd, nonZeroValue(fd))
		wire, err := proto.Marshal(set.Interface())
		if err != nil {
			t.Fatalf("marshal %s.%s: %v", md.Name(), want.name, err)
		}
		back := want.msg.ProtoReflect().New()
		if err := proto.Unmarshal(wire, back.Interface()); err != nil {
			t.Fatalf("unmarshal %s.%s: %v", md.Name(), want.name, err)
		}
		if !back.Get(fd).Equal(set.Get(fd)) {
			t.Errorf("%s.%s round-trips as %v, want %v", md.Name(), want.name, back.Get(fd), set.Get(fd))
		}
	}
}

// nonZeroValue returns a non-default value for a string, bool, or enum field.
func nonZeroValue(fd protoreflect.FieldDescriptor) protoreflect.Value {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString("attempt-token")
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(true)
	case protoreflect.EnumKind:
		return protoreflect.ValueOfEnum(fd.Enum().Values().ByNumber(3).Number())
	default:
		panic("unreachable: the bind attempt fields are string, bool, or enum")
	}
}
