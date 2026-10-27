// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// gatedCheckpointAdapter counts the Checkpoint streams it serves. The first
// stream signals entered and waits on release before it reports its summary,
// so a test holds that attempt, and the single-flight lock it owns, open.
type gatedCheckpointAdapter struct {
	adapterv1.UnimplementedAdapterServer
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedCheckpointAdapter() *gatedCheckpointAdapter {
	return &gatedCheckpointAdapter{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *gatedCheckpointAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointRequest, adapterv1.CheckpointResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if g.calls.Add(1) == 1 {
		g.once.Do(func() { close(g.entered) })
		select {
		case <-g.release:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	return stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Summary{Summary: &adapterv1.CheckpointSummary{TotalBytes: 64}},
	})
}

// bindGated seeds a running session bound to a gated adapter and returns the
// binding the registry publishes.
func bindGated(t *testing.T, registry *podsession.Registry, store sessionstore.Store, g *gatedCheckpointAdapter) *podsession.BindResult {
	t.Helper()
	bind := &podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: dialAdapter(t, g)}
	registry.Put(bind)
	runningSession(t, store, "acme", "s1")
	return bind
}

// TestNonSealAttemptOnAClosingBindingOpensNoStream_spec_7_1 pins the
// checkpoint half of the closing gate: once the seal marks the binding
// closing, a periodic or eviction attempt returns ErrNoBinding without
// opening a Checkpoint stream, while the seal itself still runs. Before the
// gate, the periodic and eviction attempts opened a stream on the closing
// binding.
// spec: 7.1 (Normal Flow), 10.1.7 (preStop Hook Drain)
func TestNonSealAttemptOnAClosingBindingOpensNoStream_spec_7_1(t *testing.T) {
	for _, trigger := range []checkpoint.Trigger{checkpoint.TriggerPeriodic, checkpoint.TriggerEviction} {
		t.Run(string(trigger), func(t *testing.T) {
			g := newGatedCheckpointAdapter()
			close(g.release)
			registry := podsession.NewRegistry()
			store := memstore.New()
			bind := bindGated(t, registry, store, g)
			bind.MarkClosing()
			cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}

			err := cp.CheckpointWithTrigger(context.Background(), "acme", "s1", trigger)
			if !errors.Is(err, checkpointer.ErrNoBinding) {
				t.Fatalf("%s attempt on a closing binding = %v, want ErrNoBinding", trigger, err)
			}
			if n := g.calls.Load(); n != 0 {
				t.Fatalf("Checkpoint streams opened = %d, want 0 on a closing binding", n)
			}
			if err := cp.Seal(context.Background(), "acme", "s1"); err != nil {
				t.Fatalf("Seal on a closing binding: %v", err)
			}
			if n := g.calls.Load(); n != 1 {
				t.Errorf("Checkpoint streams opened = %d, want the seal's one", n)
			}
		})
	}
}

// TestEvictionAttemptBehindTheSealKeepsTheSealedSnapshot_spec_7_1 pins the
// single-flight half of the closing gate: an eviction attempt that waits on
// the single-flight lock behind the seal opens no stream once the seal
// returns, and the session row keeps the sealed snapshot. Before the gate,
// the waiting attempt ran after the seal and recorded a checkpoint snapshot
// over the sealed one.
// spec: 7.1 (Normal Flow), 10.1.7 (preStop Hook Drain)
func TestEvictionAttemptBehindTheSealKeepsTheSealedSnapshot_spec_7_1(t *testing.T) {
	g := newGatedCheckpointAdapter()
	registry := podsession.NewRegistry()
	store := memstore.New()
	bind := bindGated(t, registry, store, g)
	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}

	bind.MarkClosing()
	sealDone := make(chan error, 1)
	go func() { sealDone <- cp.Seal(context.Background(), "acme", "s1") }()
	select {
	case <-g.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the seal never opened its Checkpoint stream")
	}

	evictDone := make(chan error, 1)
	go func() {
		evictDone <- cp.CheckpointWithTrigger(context.Background(), "acme", "s1", checkpoint.TriggerEviction)
	}()
	// The eviction attempt waits on the lock the seal holds.
	select {
	case err := <-evictDone:
		t.Fatalf("eviction attempt returned %v while the seal held the single-flight lock", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(g.release)

	if err := <-sealDone; err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := <-evictDone; !errors.Is(err, checkpointer.ErrNoBinding) {
		t.Fatalf("eviction attempt behind the seal = %v, want ErrNoBinding", err)
	}
	if n := g.calls.Load(); n != 1 {
		t.Errorf("Checkpoint streams opened = %d, want the seal's one", n)
	}
	row, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Source != sessionstore.WorkspaceSnapshotSealed {
		t.Fatalf("snapshot = %+v, want the sealed snapshot to survive the eviction attempt", row.WorkspaceSnapshot)
	}
}
