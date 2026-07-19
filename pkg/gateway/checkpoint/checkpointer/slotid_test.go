// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// startRecorder is a minimal streaming adapter that records the
// gateway-minted CheckpointStart it receives and closes the attempt with
// an empty CheckpointSummary. It carries no manifest side, so the wire
// frame is observable without driving the chunk grant/confirm loop.
type startRecorder struct {
	adapterv1.UnimplementedAdapterServer
	mu    sync.Mutex
	start *adapterv1.CheckpointStart
}

func (r *startRecorder) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointRequest, adapterv1.CheckpointResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.start = first.GetStart()
	r.mu.Unlock()
	return stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Summary{Summary: &adapterv1.CheckpointSummary{}},
	})
}

func (r *startRecorder) recorded() *adapterv1.CheckpointStart {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.start
}

// spec: 5.2 (per-slot checkpoint granularity), 15 (single-session
// runtimes never see a slotId), 10.1 (the 'default' sentinel is
// manifest-only). The gateway populates CheckpointStart.slot_id from the
// raw binding.SlotID: a concurrent-workspace binding sends the slot id on
// the wire so the adapter can address the slot subtree, and a
// single-session binding sends no slot_id field so the adapter stays on
// the pod-global path. The gateway must not leak the manifest-side
// "default" sentinel onto the wire, which the adapter would route down the
// per-slot branch that has no assigned slot state.
func TestDriveCheckpointSendsRawSlotIDOnTheWire(t *testing.T) {
	cases := []struct {
		name      string
		slotID    string
		wantValue string
		wantNil   bool
	}{
		{name: "concurrent binding sends the raw slot id", slotID: "slot-a", wantValue: "slot-a"},
		{name: "single-session binding sends no slot_id", slotID: "", wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &startRecorder{}
			client := dialAdapter(t, rec)

			registry := podsession.NewRegistry()
			registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", SlotID: tc.slotID, Adapter: client})

			store := memstore.New()
			runningSession(t, store, "acme", "s1")

			cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
			if err := cp.Checkpoint(context.Background(), "acme", "s1"); err != nil {
				t.Fatalf("Checkpoint: %v", err)
			}

			got := rec.recorded()
			if got == nil {
				t.Fatal("adapter recorded no CheckpointStart")
			}
			if tc.wantNil {
				if got.GetSlotId() != nil {
					t.Errorf("GetSlotId() = %v, want nil for a single-session binding", got.GetSlotId())
				}
				return
			}
			if v := got.GetSlotId().GetValue(); v != tc.wantValue {
				t.Errorf("CheckpointStart.slot_id.value = %q, want %q", v, tc.wantValue)
			}
		})
	}
}

// spec: 10.1 (the 'default' sentinel is manifest-only), 15
// (single-session runtimes never see a slotId). Sending the raw
// binding.SlotID on the wire must not change the manifest-side
// substitution: a single-session binding (empty SlotID) still scopes its
// §10.1 intent row on the SlotDefault sentinel, so the intent-row, the
// single-flight lock, and the supersede fence keep their pod-global key
// while the wire field stays empty.
func TestDriveCheckpointKeepsManifestSlotDefaultForEmptyBinding(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    10,
		chunkLens:     []int64{10},
		truncateAfter: -1,
	}, 1<<30)
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	rec, err := h.manifests.LatestFull(context.Background(), "acme", sid)
	if err != nil {
		t.Fatalf("LatestFull: %v", err)
	}
	if rec.SlotID != partialmanifeststore.SlotDefault {
		t.Errorf("intent-row SlotID = %q, want %q for a single-session binding", rec.SlotID, partialmanifeststore.SlotDefault)
	}
}
