// SPDX-License-Identifier: MIT

package barrier

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
)

// recordingCheckpointer records the per-session CheckpointWithTrigger
// calls and the trigger each was invoked with, so a test can assert the
// barrier drove exactly one gateway-side stream per target under the
// eviction trigger.
type recordingCheckpointer struct {
	mu       sync.Mutex
	calls    map[string]int
	triggers map[string]checkpoint.Trigger
	errs     map[string]error
	// started, when non-nil, is closed the first time
	// CheckpointWithTrigger is entered. A test uses it to prove the stream
	// was opened concurrently with the barrier RPC (§10.1 line 169): the
	// dispatcher can block its ack on this signal, so a serial
	// ack-then-checkpoint ordering would never fire it.
	started     chan struct{}
	startedOnce sync.Once
}

func newRecordingCheckpointer() *recordingCheckpointer {
	return &recordingCheckpointer{
		calls:    map[string]int{},
		triggers: map[string]checkpoint.Trigger{},
		errs:     map[string]error{},
	}
}

func (c *recordingCheckpointer) CheckpointWithTrigger(_ context.Context, _, sessionID string, trigger checkpoint.Trigger) error {
	if c.started != nil {
		c.startedOnce.Do(func() { close(c.started) })
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[sessionID]++
	c.triggers[sessionID] = trigger
	return c.errs[sessionID]
}

func (c *recordingCheckpointer) callCount(sessionID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[sessionID]
}

// spec: §10.1 line 169 — the barrier drives one gateway-side Checkpoint
// stream per target under the eviction trigger, opened concurrently with
// the CheckpointBarrier RPC. Each acked Outcome carries the stream's
// error (nil here) and every target is checkpointed exactly once.
func TestDispatchDrivesCheckpointPerTarget_spec_10_1_169(t *testing.T) {
	ctx := context.Background()
	meta := sessioncheckpointmeta.NewMemoryStore(nil)
	disp := newFakeDispatcher()
	disp.acks["s1"] = Ack{CheckpointRef: "ck1"}
	disp.acks["s2"] = Ack{CheckpointRef: "ck2"}
	cp := newRecordingCheckpointer()
	c := New(&fakeLister{
		targets: []Target{
			{TenantID: "acme", SessionID: "s1", CoordinationGeneration: 1},
			{TenantID: "acme", SessionID: "s2", CoordinationGeneration: 1},
		},
		source: SourcePostgres,
	}, disp, meta, nil, cp)

	sum, err := c.Dispatch(ctx)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	for _, sid := range []string{"s1", "s2"} {
		if got := cp.callCount(sid); got != 1 {
			t.Errorf("%s driven %d times, want exactly 1 gateway-side checkpoint", sid, got)
		}
		if got := cp.triggers[sid]; got != checkpoint.TriggerEviction {
			t.Errorf("%s checkpoint trigger = %q, want %q", sid, got, checkpoint.TriggerEviction)
		}
	}
	for _, o := range sum.Outcomes {
		if !o.Acked {
			t.Errorf("%s not acked: %+v", o.Target.SessionID, o)
		}
		if o.CheckpointErr != nil {
			t.Errorf("%s unexpected checkpoint error: %v", o.Target.SessionID, o.CheckpointErr)
		}
	}
}

// gatedDispatcher blocks its ack until the checkpoint stream has been
// observed open (started closed). It models the adapter's
// quiesce-and-hold: the ack is the completion signal of a stream the
// gateway has already started, so a barrier that opened the stream only
// after receiving the ack would never fire `started` and would time out.
type gatedDispatcher struct {
	started <-chan struct{}
}

func (d gatedDispatcher) Send(ctx context.Context, t Target, _ string) (Ack, error) {
	select {
	case <-d.started:
		return Ack{CheckpointRef: "ck-" + t.SessionID}, nil
	case <-ctx.Done():
		return Ack{}, ctx.Err()
	}
}

// spec: §10.1 line 169 — the ack is never blocked on a stream nobody
// opened: the barrier opens the Checkpoint stream concurrently with the
// CheckpointBarrier RPC. Here the ack fires only once the stream has been
// entered, so a serial "ack then open the stream" ordering would time out
// (the ack would wait on a signal the never-started stream cannot send).
func TestDispatchCheckpointConcurrentWithBarrier_spec_10_1_169(t *testing.T) {
	cp := newRecordingCheckpointer()
	cp.started = make(chan struct{})
	disp := gatedDispatcher{started: cp.started}
	c := New(&fakeLister{
		targets: []Target{{TenantID: "acme", SessionID: "s1", CoordinationGeneration: 1}},
		source:  SourcePostgres,
	}, disp, sessioncheckpointmeta.NewMemoryStore(nil), nil, cp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Dispatch(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Dispatch deadlocked: the ack waited on a stream that was never opened concurrently")
	}
	if got := cp.callCount("s1"); got != 1 {
		t.Errorf("s1 checkpoint ran %d times, want exactly 1", got)
	}
}

// spec: §10.1 line 169 — a barrier-window Checkpoint that errors does not
// un-ack the target: the stream finalises the manifest row (a partial row
// on abort) itself, so the session is still captured and the Outcome
// stays Acked with the stream error recorded on CheckpointErr.
func TestDispatchCheckpointErrorKeepsAcked_spec_10_1_169(t *testing.T) {
	streamErr := errors.New("op busy")
	cp := newRecordingCheckpointer()
	cp.errs["s1"] = streamErr
	disp := newFakeDispatcher()
	disp.acks["s1"] = Ack{CheckpointRef: "ck1"}
	c := New(&fakeLister{
		targets: []Target{{TenantID: "acme", SessionID: "s1", CoordinationGeneration: 1}},
		source:  SourcePostgres,
	}, disp, sessioncheckpointmeta.NewMemoryStore(nil), nil, cp)

	sum, err := c.Dispatch(context.Background())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	o := sum.Outcomes[0]
	if !o.Acked {
		t.Error("a barrier-window checkpoint error must keep the target Acked (the stream finalised a partial manifest)")
	}
	if !errors.Is(o.CheckpointErr, streamErr) {
		t.Errorf("CheckpointErr = %v, want the stream error", o.CheckpointErr)
	}
}

// spec: §10.1 line 169 — a nil Checkpointer leaves the barrier firing
// without driving a gateway-side checkpoint (the dev-mode posture); the
// ack path is unchanged and no CheckpointErr is recorded.
func TestDispatchNilCheckpointerFiresBarrierOnly_spec_10_1_169(t *testing.T) {
	disp := newFakeDispatcher()
	disp.acks["s1"] = Ack{CheckpointRef: "ck1"}
	c := New(&fakeLister{
		targets: []Target{{TenantID: "acme", SessionID: "s1", CoordinationGeneration: 1}},
		source:  SourcePostgres,
	}, disp, sessioncheckpointmeta.NewMemoryStore(nil), nil, nil)

	sum, err := c.Dispatch(context.Background())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if o := sum.Outcomes[0]; !o.Acked || o.CheckpointErr != nil {
		t.Errorf("nil checkpointer outcome = %+v, want acked with no checkpoint error", o)
	}
}
