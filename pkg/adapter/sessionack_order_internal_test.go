// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/ackruntime"
)

// spec: 28.5.3 (CH-MSGSOCK Session frame writes), 5.2 (slot serialization)
// The failed transition belongs to one start: it applies while the gate
// carries that start's startId, or while no start has reset the gate yet,
// and it leaves a later start's gate alone. Before the transition was keyed,
// a start that returned late moved a later start's pending or read gate to
// failed, and every session-scoped CH-RUNTIMEOPS frame for the running
// session was then refused.
func TestAckGateFailAppliesOnlyToItsOwnStart_spec_28_5_3(t *testing.T) {
	var g ackGate
	g.reset("2", true)
	g.fail("1")
	if got := g.current(); got != ackPending {
		t.Fatalf("fail for an earlier start moved a later start's gate to %s, want pending", got)
	}
	g.fail("")
	if got := g.current(); got != ackPending {
		t.Fatalf("fail for a start with no startId moved a later start's gate to %s, want pending", got)
	}
	g.settle("2")
	g.fail("1")
	if got := g.current(); got != ackRead {
		t.Fatalf("fail for an earlier start moved a read gate to %s, want read", got)
	}
	g.fail("2")
	if got := g.current(); got != ackFailed {
		t.Fatalf("fail for the gate's own start = %s, want failed", got)
	}

	var fresh ackGate
	fresh.fail("")
	if got := fresh.current(); got != ackFailed {
		t.Fatalf("fail on a gate no start has reset = %s, want failed", got)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Session frame writes), 5.2 (slot serialization)
// A start whose slot-guard acquisition expires ends before it mints a
// startId, while another start holds the guard and owns the entry's gate.
// It leaves that start's pending gate unchanged. Before the fix, the
// returning start failed the gate unconditionally and outside the slot
// serialization, which refused the other start's session-scoped frames.
func TestStartWithoutGuardLeavesTheGuardHoldersGate_spec_28_5_3(t *testing.T) {
	s, _, _ := ackServer(t)
	claim, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	unlock, ok := s.lockSlotGuard(context.Background(), "sess-a")
	if !ok {
		t.Fatal("the test could not take the slot guard")
	}
	defer unlock()
	claim.entry.ack.reset("holder", true)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	confirmed, err := s.openRuntimeSession(ctx, "sess-a", claim, manifestInputs{}, false)
	if confirmed || err == nil {
		t.Fatalf("openRuntimeSession without the guard = (%v, %v), want a failed start", confirmed, err)
	}
	if got := claim.entry.ack.current(); got != ackPending {
		t.Fatalf("gate after the guardless start returned = %s, want the holder's pending", got)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 5.2 (slot serialization)
// The open sequence begins when it holds the slot serialization, so a start
// that queues on the slot guard while the runtime completes its
// CH-RUNTIMEOPS capability handshake waits for its session_started and
// leaves the gate read. Before the fix, the wait decision was taken ahead
// of the guard acquisition, so such a start wrote session_start without
// waiting and left the gate not awaiting for the whole session.
func TestStartQueuedOnTheGuardWaitsWhenTheHandshakeCompletesMeanwhile_spec_28_5_3(t *testing.T) {
	lc, peer := startRuntimeOps(t)
	s := New("ack-test")
	s.Lifecycle = lc
	s.Runtime = ackruntime.New(t)
	claim, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	unlock, ok := s.lockSlotGuard(context.Background(), "sess-a")
	if !ok {
		t.Fatal("the test could not take the slot guard")
	}
	type result struct {
		confirmed bool
		err       error
	}
	done := make(chan result, 1)
	go func() {
		confirmed, err := s.openRuntimeSession(context.Background(), "sess-a", claim, manifestInputs{}, false)
		done <- result{confirmed, err}
	}()
	// Give the start time to reach the guard before the handshake completes.
	time.Sleep(50 * time.Millisecond)
	peer.handshake()
	awaitHandshake(t, lc)
	unlock()

	got := <-done
	if !got.confirmed || got.err != nil {
		t.Fatalf("openRuntimeSession = (%v, %v), want a confirmed start", got.confirmed, got.err)
	}
	if st := claim.entry.ack.current(); st != ackRead {
		t.Fatalf("gate = %s, want read: the start did not wait for session_started", st)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Session frame writes), 5.2 (slot serialization)
// A failed start moves its gate to failed before it releases the slot
// guard, so the next start on the entry, which takes the guard and resets
// the gate, never has its gate failed by the earlier start.
// diagnosis: the open sequence applies the failed transition after it
// releases the slot serialization, so a stale failure can overwrite the
// gate a later start reset.
func TestFailedStartFailsTheGateBeforeReleasingTheGuard_spec_28_5_3(t *testing.T) {
	s, _, rt := ackServer(t)
	s.SessionStartAckTimeout = 100 * time.Millisecond
	rt.SetReply(ackruntime.Withhold)
	claim, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	started := make(chan error, 1)
	go func() {
		_, err := s.openRuntimeSession(context.Background(), "sess-a", claim, manifestInputs{}, false)
		started <- err
	}()
	waitForStartID(t, rt)

	observed := make(chan ackState, 1)
	go func() {
		unlock, ok := s.lockSlotGuard(context.Background(), "sess-a")
		if !ok {
			observed <- ackNotStarted
			return
		}
		defer unlock()
		observed <- claim.entry.ack.current()
	}()
	if err := <-started; !errors.Is(err, errSessionStartUnacknowledged) {
		t.Fatalf("openRuntimeSession = %v, want errSessionStartUnacknowledged", err)
	}
	if got := <-observed; got != ackFailed {
		t.Fatalf("gate seen by the next guard holder = %s, want failed", got)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-RUNTIMEOPS Messages)
// A start that reads its session_started but finds the gate no longer
// pending for it, with no removal having released it, fails rather than
// take the record behind a gate that refuses the session's CH-RUNTIMEOPS
// frames: it writes session_end and returns errSessionStartUnacknowledged.
// Before the fix the start discarded settle's result and confirmed.
func TestStartFailsWhenItsGateLeftPendingWithoutRemoval_spec_28_5_3(t *testing.T) {
	s, _, rt := ackServer(t)
	claim, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rt.SetReply(func(in []byte) []byte {
		var f struct {
			StartID string `json:"startId"`
		}
		if json.Unmarshal(in, &f) == nil && f.StartID != "" {
			claim.entry.ack.fail(f.StartID)
		}
		return ackruntime.AnswerSessionStart(in)
	})
	confirmed, err := s.openRuntimeSession(context.Background(), "sess-a", claim, manifestInputs{}, false)
	if confirmed || !errors.Is(err, errSessionStartUnacknowledged) {
		t.Fatalf("openRuntimeSession = (%v, %v), want (false, errSessionStartUnacknowledged)", confirmed, err)
	}
	if got := rt.FrameTypes(); !equalTypes(got, []string{"session_start", "session_end"}) {
		t.Errorf("frames = %v, want [session_start session_end]", got)
	}
	s.mu.Lock()
	held := s.runtimeHoldsLocked("sess-a")
	s.mu.Unlock()
	if held {
		t.Error("the start took the record behind a failed gate")
	}
}

// nopCheckpointTransport satisfies CheckpointTransport for a stream the
// gateway aborts before any PUT.
type nopCheckpointTransport struct{}

func (nopCheckpointTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 200, "", nil
}

func (nopCheckpointTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, errors.New("nopCheckpointTransport serves no GET")
}

// abortingCheckpointStream is the server side of a Checkpoint stream whose
// gateway opens with start and answers the first ChunkReady with an Abort,
// running onChunkReady first so a case can act during the upload.
type abortingCheckpointStream struct {
	grpc.ServerStream
	ctx          context.Context
	start        *adapterv1.CheckpointStart
	opened       bool
	onChunkReady func()
	abort        chan struct{}
}

func (f *abortingCheckpointStream) Context() context.Context { return f.ctx }

func (f *abortingCheckpointStream) Send(r *adapterv1.CheckpointResponse) error {
	if r.GetChunkReady() != nil {
		f.onChunkReady()
		close(f.abort)
	}
	return nil
}

func (f *abortingCheckpointStream) Recv() (*adapterv1.CheckpointRequest, error) {
	if !f.opened {
		f.opened = true
		return &adapterv1.CheckpointRequest{Msg: &adapterv1.CheckpointRequest_Start{Start: f.start}}, nil
	}
	select {
	case <-f.abort:
		return &adapterv1.CheckpointRequest{Msg: &adapterv1.CheckpointRequest_Abort{
			Abort: &adapterv1.CheckpointAbort{Reason: "test abort"},
		}}, nil
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}
}

// checkpointGateServer is an acknowledging server with a running session
// whose workspace holds one file, and a runtime-ops peer that answers the
// checkpoint_request with checkpoint_ready and hands the request to the
// case on the returned channel.
func checkpointGateServer(t *testing.T) (*Server, *fakeRuntime, <-chan lifecycleFrame) {
	t.Helper()
	s, peer, _ := ackServer(t)
	s.WorkspaceBase = t.TempDir()
	s.CheckpointTransport = nopCheckpointTransport{}
	if err := startSession(context.Background(), s, "sess-a"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	current := filepath.Join(s.WorkspaceBase, "slots", "sess-a", "current")
	if err := os.WriteFile(filepath.Join(current, "notes.txt"), []byte("state"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	reqs := make(chan lifecycleFrame, 1)
	go func() {
		req, ok := peer.readWithin(10 * time.Second)
		if !ok {
			close(reqs)
			return
		}
		peer.write(lifecycleFrame{Type: "checkpoint_ready", CheckpointID: req.CheckpointID})
		reqs <- req
	}()
	return s, peer, reqs
}

// expectCheckpointRequest asserts the case's checkpoint_request reached the
// runtime.
func expectCheckpointRequest(t *testing.T, reqs <-chan lifecycleFrame) {
	t.Helper()
	if req, ok := <-reqs; !ok || req.Type != "checkpoint_request" {
		t.Fatalf("runtime saw (%+v, %v), want checkpoint_request", req, ok)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages), 4.4 (checkpoint quiescence)
// The deferred checkpoint_complete reads the session's gate without
// waiting, because it runs while the pod-level op lock is held and the
// stream context carries no deadline. A read gate still admits the frame on
// the abort path; a gate that a later start reset to pending during the
// upload ends the stream at once with no frame and frees the op lock.
// Before the fix the deferred call waited on the gate under the op lock for
// as long as the stream lived, stalling every co-tenant's checkpoint and
// interrupt.
func TestDeferredCheckpointCompleteNeverWaitsUnderTheOpLock_spec_28_5_3(t *testing.T) {
	start := &adapterv1.CheckpointStart{
		CheckpointId:   "ckpt-1",
		SessionId:      &adapterv1.SessionId{Value: "sess-a"},
		Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
		ChunkSizeBytes: 1 << 20,
		DeadlineMs:     5_000,
	}
	t.Run("a read gate still receives the failed completion", func(t *testing.T) {
		s, peer, reqs := checkpointGateServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream := &abortingCheckpointStream{ctx: ctx, start: start, onChunkReady: func() {}, abort: make(chan struct{})}
		if err := s.Checkpoint(stream); err != nil {
			t.Fatalf("Checkpoint: %v", err)
		}
		expectCheckpointRequest(t, reqs)
		got, ok := peer.readWithin(2 * time.Second)
		if !ok || got.Type != "checkpoint_complete" || got.Status != "failed" {
			t.Fatalf("runtime saw (%+v, %v), want a failed checkpoint_complete", got, ok)
		}
	})
	t.Run("a gate reset to pending during the upload holds no op lock", func(t *testing.T) {
		s, peer, reqs := checkpointGateServer(t)
		entry := s.slotStateForSession("sess-a")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream := &abortingCheckpointStream{
			ctx: ctx, start: start, abort: make(chan struct{}),
			onChunkReady: func() { entry.ack.reset("successor", true) },
		}
		done := make(chan error, 1)
		go func() { done <- s.Checkpoint(stream) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Checkpoint is still waiting on the gate while it holds the op lock")
		}
		bctx, bcancel := context.WithTimeout(context.Background(), time.Second)
		defer bcancel()
		release, err := s.ops.Begin(bctx, opInterrupt, "")
		if err != nil {
			t.Fatalf("op lock after the checkpoint returned: %v", err)
		}
		release()
		expectCheckpointRequest(t, reqs)
		if got, ok := peer.readWithin(200 * time.Millisecond); ok {
			t.Fatalf("runtime saw %+v after its session's gate was reset, want no frame", got)
		}
	})
}
