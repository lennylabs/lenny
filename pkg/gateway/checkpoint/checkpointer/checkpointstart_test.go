// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
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

// spec: 5.2 (a session-mode slot's identifier is its session's
// identifier), 10.1.7 (the opening frame addresses the checkpoint
// stream). Neither the CheckpointRequest envelope nor the Checkpoint RPC
// signature carries a session, so CheckpointStart is where the stream is
// addressed. The gateway populates it from the session it is
// checkpointing, on a binding of either concurrency, and sends no second
// address beside it.
func TestDriveCheckpointAddressesTheStartBySession(t *testing.T) {
	cases := []struct {
		name   string
		slotID string
	}{
		{name: "a binding placed on a counted slot", slotID: "s1"},
		{name: "a binding with no separate slot recorded", slotID: ""},
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
			if v := got.GetSessionId().GetValue(); v != "s1" {
				t.Errorf("CheckpointStart.session_id.value = %q, want s1", v)
			}
		})
	}
}
