// SPDX-License-Identifier: MIT

package barrier

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
)

// slotDrainCheckpointer models the adapter side of a concurrent
// (maxConcurrentSessions > 1) pod's drain: the gateway opens one
// Checkpoint stream per occupied slot, so a distinct
// CheckpointWithTrigger call arrives for each slot's session. Every call
// blocks on entry until all expect slots have entered, so a drain that
// coalesced its slots into fewer streams than occupied slots would never
// release the entry gate and the dispatch would deadlock past its
// deadline. The completed set records which slots finished their upload
// so the test can assert every acked target was captured, never acked
// with a checkpoint that coalesced away.
type slotDrainCheckpointer struct {
	entered chan string
	release chan struct{}

	mu        sync.Mutex
	completed map[string]int
}

func newSlotDrainCheckpointer(expect int) *slotDrainCheckpointer {
	return &slotDrainCheckpointer{
		entered:   make(chan string, expect),
		release:   make(chan struct{}),
		completed: map[string]int{},
	}
}

func (c *slotDrainCheckpointer) CheckpointWithTrigger(ctx context.Context, _, sessionID string, _ checkpoint.Trigger) error {
	c.entered <- sessionID
	select {
	case <-c.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	c.mu.Lock()
	c.completed[sessionID]++
	c.mu.Unlock()
	return nil
}

func (c *slotDrainCheckpointer) completions(sessionID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.completed[sessionID]
}

// spec: §5.2 (per-slot checkpoint serialization), §4.4 (checkpoint store
// durability), §10.1 line 169. A concurrent pod's drain fans one
// gateway-side Checkpoint stream out per occupied slot and acks each
// target only after that slot's checkpoint is recorded. With three
// occupied slots against the one adapter, every slot drives its own
// stream (the entry gate releases only when all three have entered, so a
// drain that coalesced slots into a single pod-wide checkpoint would
// deadlock here) and no target is acked at barrier.go's ack point with a
// checkpoint that coalesced away.
func TestDrainAdmitsEveryOccupiedSlot_spec_5_2(t *testing.T) {
	const podAddr = "10.0.0.7"
	sessions := []string{"slot-a", "slot-b", "slot-c"}
	cp := newSlotDrainCheckpointer(len(sessions))
	disp := newFakeDispatcher()
	targets := make([]Target, 0, len(sessions))
	for _, s := range sessions {
		disp.acks[s] = Ack{CheckpointRef: "ck-" + s}
		targets = append(targets, Target{
			TenantID:               "acme",
			SessionID:              s,
			CoordinationGeneration: 1,
			PodAddr:                podAddr,
		})
	}
	c := New(&fakeLister{targets: targets, source: SourcePostgres},
		disp, sessioncheckpointmeta.NewMemoryStore(nil), nil, cp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan DispatchSummary, 1)
	errc := make(chan error, 1)
	go func() {
		sum, err := c.Dispatch(ctx)
		if err != nil {
			errc <- err
			return
		}
		done <- sum
	}()

	// Every occupied slot must drive its own Checkpoint stream. A drain
	// that coalesced slots into fewer streams than occupied slots would
	// enter fewer than len(sessions) here and time out.
	seen := map[string]bool{}
	for range sessions {
		select {
		case s := <-cp.entered:
			seen[s] = true
		case err := <-errc:
			t.Fatalf("Dispatch: %v", err)
		case <-ctx.Done():
			t.Fatalf("only %d of %d occupied slots drove a Checkpoint stream; a coalesced drain drops slots", len(seen), len(sessions))
		}
	}
	for _, s := range sessions {
		if !seen[s] {
			t.Errorf("slot %s drove no Checkpoint stream", s)
		}
	}
	close(cp.release)

	var sum DispatchSummary
	select {
	case sum = <-done:
	case err := <-errc:
		t.Fatalf("Dispatch: %v", err)
	case <-ctx.Done():
		t.Fatal("Dispatch did not return after every slot's checkpoint was released")
	}

	if len(sum.Outcomes) != len(sessions) {
		t.Fatalf("outcomes = %d, want one per occupied slot (%d)", len(sum.Outcomes), len(sessions))
	}
	for _, o := range sum.Outcomes {
		if !o.Acked {
			t.Errorf("slot %s not acked: %+v", o.Target.SessionID, o)
		}
		// The ack point is reached only after the slot's checkpoint
		// stream terminated, so an acked target always carries a recorded
		// checkpoint. A target acked with a coalesced-away checkpoint
		// would show a zero completion count here.
		if got := cp.completions(o.Target.SessionID); got != 1 {
			t.Errorf("slot %s captured %d times before ack, want exactly 1", o.Target.SessionID, got)
		}
		if o.CheckpointErr != nil {
			t.Errorf("slot %s unexpected checkpoint error: %v", o.Target.SessionID, o.CheckpointErr)
		}
	}
}
