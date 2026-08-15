//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_generation_fence_test is the Tier 3 contract suite for the
// coordination generation fence on the gateway → pod adapter gRPC contract
// (schemas/lenny-adapter.proto). Each session row carries a
// `coordination_generation` counter that a replica increments when it takes
// coordination of the session over, and a pod validates the generation on
// every gateway-to-pod RPC so a replica that has lost coordination cannot
// drive the pod (§10.1). The fence is only enforceable where the request
// message carries it, so this suite reads the generated protoreflect
// descriptor to pin the field, at its assigned number and int64 kind, on the
// request messages the coordinating replica sends to drive a session already
// bound to the pod, and exercises the binary wire form: an unset fence adds no
// bytes, a set fence round-trips, and the fence on the streaming Checkpoint
// request is readable alongside every arm of the request's `msg` oneof.
//
// The pinned set is enumerated in fencedMessages and its boundary is stated
// there. It is a subset of the Adapter service's request messages: the
// session-assignment calls that run before the session is bound, the
// pod-level calls that name no session, and CoordinatorFence itself are
// outside it.
package adapter_generation_fence_test

import (
	"math"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fenceFieldName is the proto field name of the generation fence.
const fenceFieldName = "coordination_generation"

// fencedMessage is one fenced gateway-to-pod request message together with the
// field number its generation fence is assigned.
type fencedMessage struct {
	msg    proto.Message
	number protoreflect.FieldNumber
}

// fencedMessages enumerates the request messages this suite pins and the fence
// field number each assigns. The set is the requests a coordinating replica
// sends against a session already bound to the pod: the client frame of the
// attach stream and the message delivery it carries (§28.5.1 CH-ATTACH, whose
// preconditions state the stamp, and §29 for the send-message delivery path),
// the interrupt signal, the deadline warning, the usage pull, the client frame
// of the checkpoint stream (§28.5.1 CH-CHECKPOINT), the drain barrier (§28.5.1
// CH-BARRIER), the resume, the shutdown, the three mid-session credential
// calls the §4.9 lease machinery drives against a running session (the hot
// rotation, the lease extension the Token Service unavailability guard sends,
// and the emergency revocation), and the §8.7 delegation export the gateway
// takes from the live workspace.
//
// The boundary of the set: CoordinatorFenceRequest is excluded because it
// carries the generation as the announcement being fenced rather than as a
// stamp validated against a recorded one (§28.5.1 CH-FENCE). The Adapter
// service's remaining request messages are the §4.7 session-assignment
// sequence that runs before the session is bound (PrepareWorkspace,
// FinalizeWorkspace, RunSetup, StartSession, ConfigureWorkspace, and the
// initial AssignCredentials) and the pod-level calls that name no session;
// §10.1's rule reaches every gateway-to-pod RPC, and §28.5.1 CH-PODHEALTH
// records that the specification does not state how it applies to a request
// against a pod not yet serving a coordinated session, so this gate claims
// nothing about them.
var fencedMessages = map[string]fencedMessage{
	"AttachRequest":                {msg: &adapterv1.AttachRequest{}, number: 4},
	"SendMessageRequest":           {msg: &adapterv1.SendMessageRequest{}, number: 4},
	"InterruptRequest":             {msg: &adapterv1.InterruptRequest{}, number: 4},
	"SignalDeadlineRequest":        {msg: &adapterv1.SignalDeadlineRequest{}, number: 4},
	"ReportUsageRequest":           {msg: &adapterv1.ReportUsageRequest{}, number: 3},
	"ResumeRequest":                {msg: &adapterv1.ResumeRequest{}, number: 14},
	"CheckpointRequest":            {msg: &adapterv1.CheckpointRequest{}, number: 4},
	"CheckpointBarrierRequest":     {msg: &adapterv1.CheckpointBarrierRequest{}, number: 2},
	"ShutdownRequest":              {msg: &adapterv1.ShutdownRequest{}, number: 6},
	"RotateCredentialsRequest":     {msg: &adapterv1.RotateCredentialsRequest{}, number: 5},
	"ExtendCredentialLeaseRequest": {msg: &adapterv1.ExtendCredentialLeaseRequest{}, number: 6},
	"RevokeCredentialsRequest":     {msg: &adapterv1.RevokeCredentialsRequest{}, number: 5},
	"ExportPathsRequest":           {msg: &adapterv1.ExportPathsRequest{}, number: 3},
}

// TestBoundSessionRequestsCarryGenerationFence pins that each request message
// in fencedMessages declares the generation fence at its assigned number as an
// int64, and that the fence is a plain field rather than a member of a oneof,
// so a request carrying any other arm still carries the fence.
//
// spec: 10.1 (coordination generation counter; pods validate the generation on
// every gateway-to-pod RPC and reject a stale coordinator's request), 4.7
// (Runtime Adapter gRPC surface), 28.5.1 (gateway-to-pod channel cards, whose
// preconditions state the stamp)
//
// diagnosis: a fenced gateway-to-pod request message lost the
// coordination generation fence, or the fence was renumbered or retyped. A
// request message with no fence gives the pod nothing to validate, so a
// replica that has lost coordination can still drive the pod and the §10.1
// split-brain guarantee does not hold for that RPC. A renumber breaks binary
// compatibility between a gateway and an adapter built from different
// revisions of the contract. Re-edit schemas/lenny-adapter.proto and run
// `make generate-proto`.
func TestBoundSessionRequestsCarryGenerationFence(t *testing.T) {
	for name, want := range fencedMessages {
		md := want.msg.ProtoReflect().Descriptor()
		f := md.Fields().ByName(fenceFieldName)
		if f == nil {
			t.Errorf("%s: %s field missing from the wire contract", name, fenceFieldName)
			continue
		}
		if f.Number() != want.number {
			t.Errorf("%s: %s has number %d, want %d", name, fenceFieldName, f.Number(), want.number)
		}
		if f.Kind() != protoreflect.Int64Kind {
			t.Errorf("%s: %s has kind %v, want %v", name, fenceFieldName, f.Kind(), protoreflect.Int64Kind)
		}
		if f.IsList() || f.IsMap() {
			t.Errorf("%s: %s must be a singular field", name, fenceFieldName)
		}
		if oo := f.ContainingOneof(); oo != nil {
			t.Errorf("%s: %s sits inside oneof %q; the fence applies to every frame the gateway sends and must be readable alongside any other arm", name, fenceFieldName, oo.Name())
		}
	}
}

// TestUnsetGenerationFenceAddsNoBytes pins that a request whose fence is left
// at the zero value encodes to exactly the bytes the same request encoded
// before the fence existed. proto3 emits nothing for a zero scalar, so a
// gateway that has not yet recorded a generation for the session sends the
// same bytes it always sent, and a pod decoding a request with no fence reads
// zero rather than a spurious generation.
//
// spec: 10.1 (coordination generation counter; zero where the gateway has
// recorded no generation), 4.7 (Runtime Adapter gRPC surface)
//
// diagnosis: adding the fence changed the encoding of a request that does not
// set it. Either the field emits bytes at its zero value or it was declared in
// a way that forces presence, so a gateway and an adapter built from different
// revisions of the contract disagree on the bytes of an unfenced request.
func TestUnsetGenerationFenceAddsNoBytes(t *testing.T) {
	// session_id (field 1) alone, computed independently of the message types
	// under test: field 1, wire type 2 (0x0a), holding SessionId{value:"sess-1"}.
	want := []byte{0x0a, 0x08, 0x0a, 0x06, 's', 'e', 's', 's', '-', '1'}
	sid := &adapterv1.SessionId{Value: "sess-1"}

	unfenced := map[string]proto.Message{
		"AttachRequest":                &adapterv1.AttachRequest{SessionId: sid},
		"SendMessageRequest":           &adapterv1.SendMessageRequest{SessionId: sid},
		"ShutdownRequest":              &adapterv1.ShutdownRequest{SessionId: sid},
		"InterruptRequest":             &adapterv1.InterruptRequest{SessionId: sid},
		"SignalDeadlineRequest":        &adapterv1.SignalDeadlineRequest{SessionId: sid},
		"ReportUsageRequest":           &adapterv1.ReportUsageRequest{SessionId: sid},
		"ResumeRequest":                &adapterv1.ResumeRequest{SessionId: sid},
		"CheckpointBarrierRequest":     &adapterv1.CheckpointBarrierRequest{SessionId: sid},
		"RotateCredentialsRequest":     &adapterv1.RotateCredentialsRequest{SessionId: sid},
		"ExtendCredentialLeaseRequest": &adapterv1.ExtendCredentialLeaseRequest{SessionId: sid},
		"RevokeCredentialsRequest":     &adapterv1.RevokeCredentialsRequest{SessionId: sid},
		"ExportPathsRequest":           &adapterv1.ExportPathsRequest{SessionId: sid},
	}
	for name, m := range unfenced {
		got, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s: unset-fence encoding drifted:\n got % x\nwant % x", name, got, want)
		}
	}

	// The streaming Checkpoint request carries no session_id, so its unset
	// form is the empty encoding.
	empty, err := proto.MarshalOptions{Deterministic: true}.Marshal(&adapterv1.CheckpointRequest{})
	if err != nil {
		t.Fatalf("CheckpointRequest: marshal: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("CheckpointRequest: unset-fence encoding is % x, want empty", empty)
	}
}

// TestGenerationFenceRoundTrips pins that a set fence survives a binary
// marshal and unmarshal on every request message in fencedMessages,
// at the boundary values a generation counter can reach. A fence that does not
// survive the round trip is a fence the pod compares against the wrong value.
//
// spec: 10.1 (coordination generation counter; pods validate the generation on
// every gateway-to-pod RPC), 4.7 (Runtime Adapter gRPC surface)
//
// diagnosis: the fence was retyped or renumbered in schemas/lenny-adapter.proto
// without a regeneration, so the generation a gateway sends is not the
// generation the pod reads and the §10.1 fence compares a corrupted value.
func TestGenerationFenceRoundTrips(t *testing.T) {
	gens := []int64{1, math.MaxInt64}
	for _, gen := range gens {
		for name, fm := range fencedMessages {
			in := proto.Clone(fm.msg)
			setFence(t, in, gen)

			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("%s: marshal: %v", name, err)
			}
			out := in.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(raw, out); err != nil {
				t.Fatalf("%s: unmarshal: %v", name, err)
			}
			if got := fence(t, out); got != gen {
				t.Errorf("%s: fence round-tripped to %d, want %d", name, got, gen)
			}
		}
	}
}

// TestCheckpointRequestFenceCoexistsWithEveryOneofArm pins the placement the
// streaming Checkpoint request uses: the fence sits outside the `msg` oneof, so
// every frame the gateway sends on the stream carries it and setting the fence
// clears no arm. Placing the fence inside the oneof, or on the opening
// CheckpointStart arm alone, would leave the grant and abort frames unfenced.
//
// spec: 10.1 (coordination generation validated on every gateway-to-pod RPC),
// 4.7 (Runtime Adapter gRPC surface), 10.1.8 (gateway-driven checkpoint stream)
//
// diagnosis: the fence on the streaming Checkpoint request became a oneof arm
// or was moved onto one, so a grant or abort frame either carries no fence or
// displaces the arm it was sent with, and the stream's later frames are
// unfenced against a coordinator that lost the session mid-stream.
func TestCheckpointRequestFenceCoexistsWithEveryOneofArm(t *testing.T) {
	const gen = int64(7)
	arms := map[string]*adapterv1.CheckpointRequest{
		"start": {
			Msg:                    &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{CheckpointId: "ckpt-1"}},
			CoordinationGeneration: gen,
		},
		"grant": {
			Msg:                    &adapterv1.CheckpointRequest_Grant{Grant: &adapterv1.CheckpointGrant{Index: 3}},
			CoordinationGeneration: gen,
		},
		"abort": {
			Msg:                    &adapterv1.CheckpointRequest_Abort{Abort: &adapterv1.CheckpointAbort{Reason: "drain"}},
			CoordinationGeneration: gen,
		},
	}
	for name, in := range arms {
		raw, err := proto.Marshal(in)
		if err != nil {
			t.Fatalf("%s arm: marshal: %v", name, err)
		}
		var out adapterv1.CheckpointRequest
		if err := proto.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s arm: unmarshal: %v", name, err)
		}
		if out.GetCoordinationGeneration() != gen {
			t.Errorf("%s arm: fence decoded to %d, want %d", name, out.GetCoordinationGeneration(), gen)
		}
		if out.GetMsg() == nil {
			t.Errorf("%s arm: setting the fence cleared the oneof arm", name)
		}
		if !proto.Equal(in, &out) {
			t.Errorf("%s arm: round-trip mismatch:\n got %v\nwant %v", name, &out, in)
		}
	}
}

// setFence writes gen into the message's generation fence field through
// protoreflect, so the helper works over every message under test without a
// per-type switch.
func setFence(t *testing.T, m proto.Message, gen int64) {
	t.Helper()
	r := m.ProtoReflect()
	f := r.Descriptor().Fields().ByName(fenceFieldName)
	if f == nil {
		t.Fatalf("%s: %s field missing", r.Descriptor().Name(), fenceFieldName)
	}
	r.Set(f, protoreflect.ValueOfInt64(gen))
}

// fence reads the message's generation fence field through protoreflect.
func fence(t *testing.T, m proto.Message) int64 {
	t.Helper()
	r := m.ProtoReflect()
	f := r.Descriptor().Fields().ByName(fenceFieldName)
	if f == nil {
		t.Fatalf("%s: %s field missing", r.Descriptor().Name(), fenceFieldName)
	}
	return r.Get(f).Int()
}
