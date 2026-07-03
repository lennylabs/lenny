//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_checkpointbarrier_test is the Tier 3 contract suite for
// the CheckpointBarrierResponse message on the gateway ↔ pod adapter gRPC
// contract (schemas/lenny-adapter.proto). The response message mirrors the
// CheckpointBarrierAck control event the adapter emits on the
// LifecycleChannel; the gateway's barrier-target reconciler consumes both.
// This suite pins the wire fields and their numbers so a field addition,
// rename, or renumber that survives a coordinated proto+regeneration edit
// is still caught. It reads the generated protoreflect descriptor rather
// than the .proto text, so it verifies the contract the compiled gateway
// and adapter actually exchange.
package adapter_checkpointbarrier_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// wantField is one expected field on the CheckpointBarrierResponse wire
// contract: its proto field name and its field number.
type wantField struct {
	name   string
	number protoreflect.FieldNumber
}

// TestCheckpointBarrierResponseWireContract pins the exact field set and
// numbering of CheckpointBarrierResponse after the §10.1 resume-dedup
// removal (proposal 0026, F-10.1.19). The message carries barrier_id,
// checkpoint_ref, and quiesced_ms; last_tool_call_id is removed and the
// trailing fields are renumbered to close the gap. This test fails against
// the pre-0026 generated code, which carried last_tool_call_id = 2 and
// checkpoint_ref = 3.
//
// spec: 10.1 (CheckpointBarrier protocol, ack signature
// CheckpointBarrierAck(barrier_id, checkpoint_ref)), 4.7 (Runtime Adapter,
// CheckpointBarrier RPC).
// diagnosis: The CheckpointBarrierResponse wire contract diverged from the
// §10.1 ack signature. Either last_tool_call_id was reintroduced (the
// removed resume-dedup field is back on the wire) or the retained fields
// were renumbered, which breaks the gateway/adapter binary compatibility.
// Re-edit schemas/lenny-adapter.proto and run `make generate-proto`.
func TestCheckpointBarrierResponseWireContract(t *testing.T) {
	md := (&adapterv1.CheckpointBarrierResponse{}).ProtoReflect().Descriptor()

	want := []wantField{
		{name: "barrier_id", number: 1},
		{name: "checkpoint_ref", number: 2},
		{name: "quiesced_ms", number: 3},
	}

	fields := md.Fields()
	if got := fields.Len(); got != len(want) {
		t.Fatalf("CheckpointBarrierResponse has %d fields, want %d (last_tool_call_id must be absent per §10.1)", got, len(want))
	}

	for _, w := range want {
		f := fields.ByName(protoreflect.Name(w.name))
		if f == nil {
			t.Errorf("CheckpointBarrierResponse: field %q missing from the wire contract", w.name)
			continue
		}
		if f.Number() != w.number {
			t.Errorf("CheckpointBarrierResponse: field %q has number %d, want %d", w.name, f.Number(), w.number)
		}
	}

	// The removed resume-dedup field must not be present under any number.
	if f := fields.ByName("last_tool_call_id"); f != nil {
		t.Errorf("CheckpointBarrierResponse still carries last_tool_call_id (number %d); §10.1 resume-dedup was removed", f.Number())
	}
}
