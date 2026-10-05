// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/tests/testinfra/ackruntime"
)

// awaitHandshake blocks until the runtime-ops peer's capability handshake
// has completed, so a start that begins afterwards waits for its
// session_started.
func awaitHandshake(t *testing.T, lc *RuntimeOps) {
	t.Helper()
	if !lc.WaitHandshake(context.Background(), 5*time.Second) {
		t.Fatal("CH-RUNTIMEOPS capability handshake did not complete")
	}
}

// ackServer is a Server whose runtime completed the CH-RUNTIMEOPS
// capability handshake, with an acknowledging runtime installed, so every
// start it runs waits for session_started.
func ackServer(t *testing.T) (*Server, *fakeRuntime, *ackruntime.Runtime) {
	t.Helper()
	lc, peer := startRuntimeOps(t)
	peer.handshake()
	awaitHandshake(t, lc)
	s := New("ack-test")
	s.Lifecycle = lc
	rt := ackruntime.New(t)
	s.Runtime = rt
	return s, peer, rt
}

// startSession runs StartSession for sessionID.
func startSession(ctx context.Context, s *Server, sessionID string) error {
	_, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	})
	return err
}

// waitForStartID polls until rt has recorded its first session_start and
// returns that start's startId.
func waitForStartID(t *testing.T, rt *ackruntime.Runtime) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if id, ok := rt.StartID(0); ok {
			return id
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the adapter wrote no session_start")
	return ""
}

// gateOf returns the acknowledgement gate state of sessionID's registry
// entry, and false when the registry holds none.
func gateOf(s *Server, sessionID string) (ackState, bool) {
	st := s.slotStateForSession(sessionID)
	if st == nil {
		return 0, false
	}
	return st.ack.current(), true
}

// equalTypes reports whether got lists exactly want, in order.
func equalTypes(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-RUNTIMEOPS Messages)
// Each acknowledgement-gate transition: a sender waits on a not-started or
// pending gate and wakes at the start's outcome, proceeds through a read or
// not-awaiting gate without consulting its context, and is refused by a
// failed or released gate; released is terminal; and only the start the
// gate was reset to settles it, and only from pending.
func TestAckGateTransitions_spec_28_5_3(t *testing.T) {
	t.Run("not started refuses an unbounded caller and a bounded wait", func(t *testing.T) {
		var g ackGate
		if g.current() != ackNotStarted {
			t.Fatalf("zero gate = %s, want not_started", g.current())
		}
		if err := g.ready(); !errors.Is(err, errSessionStartNotAcknowledged) {
			t.Errorf("ready() on not_started = %v, want errSessionStartNotAcknowledged", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := g.await(ctx)
		if !errors.Is(err, errSessionStartNotAcknowledged) || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("await on not_started = %v, want the gate error wrapping the deadline", err)
		}
	})
	t.Run("a waiter from before the start returns on that start's read", func(t *testing.T) {
		var g ackGate
		done := make(chan error, 1)
		go func() { done <- g.await(context.Background()) }()
		time.Sleep(10 * time.Millisecond)
		g.reset("7", true)
		select {
		case err := <-done:
			t.Fatalf("waiter returned %v at the reset to pending, want it to wait for the outcome", err)
		case <-time.After(20 * time.Millisecond):
		}
		if g.settle("6") {
			t.Fatal("settle for another start's startId moved the gate")
		}
		if !g.settle("7") {
			t.Fatal("settle for the gate's startId did not move it")
		}
		if err := <-done; err != nil {
			t.Fatalf("waiter after read = %v, want nil", err)
		}
		if g.settle("7") {
			t.Fatal("settle moved a gate that is no longer pending")
		}
	})
	t.Run("a failed gate refuses a waiter", func(t *testing.T) {
		var g ackGate
		g.reset("1", true)
		g.fail()
		if g.current() != ackFailed {
			t.Fatalf("gate = %s, want failed", g.current())
		}
		if err := g.await(context.Background()); !errors.Is(err, errSessionStartNotAcknowledged) {
			t.Errorf("await on failed = %v, want errSessionStartNotAcknowledged", err)
		}
	})
	t.Run("read and not awaiting admit a caller whose context ended", func(t *testing.T) {
		ended, cancel := context.WithCancel(context.Background())
		cancel()
		for _, awaiting := range []bool{true, false} {
			var g ackGate
			g.reset("1", awaiting)
			if awaiting {
				g.settle("1")
			}
			if err := g.await(ended); err != nil {
				t.Errorf("await(ended ctx) on %s = %v, want nil", g.current(), err)
			}
			if err := g.ready(); err != nil {
				t.Errorf("ready() on %s = %v, want nil", g.current(), err)
			}
		}
	})
	t.Run("release wakes a waiter and is terminal", func(t *testing.T) {
		var g ackGate
		g.reset("1", true)
		done := make(chan error, 1)
		go func() { done <- g.await(context.Background()) }()
		time.Sleep(10 * time.Millisecond)
		g.release()
		if err := <-done; !errors.Is(err, errSessionStartNotAcknowledged) {
			t.Fatalf("waiter after release = %v, want errSessionStartNotAcknowledged", err)
		}
		g.reset("2", false)
		g.fail()
		if g.settle("1") || g.current() != ackReleased {
			t.Fatalf("gate after reset, fail, and settle = %s, want released", g.current())
		}
	})
	t.Run("a nil entry is refused", func(t *testing.T) {
		s := New("ack-test")
		if err := s.awaitSessionStarted(context.Background(), nil); !errors.Is(err, errSessionStartNotAcknowledged) {
			t.Errorf("awaitSessionStarted(nil) = %v", err)
		}
		if err := s.sessionStartedNow(nil); !errors.Is(err, errSessionStartNotAcknowledged) {
			t.Errorf("sessionStartedNow(nil) = %v", err)
		}
	})
	t.Run("state names", func(t *testing.T) {
		for st, want := range map[ackState]string{
			ackNotStarted: "not_started", ackPending: "pending", ackRead: "read",
			ackFailed: "failed", ackNotAwaiting: "not_awaiting", ackReleased: "released",
			ackState(42): "ackState(42)",
		} {
			if st.String() != want {
				t.Errorf("%d.String() = %q, want %q", int(st), st.String(), want)
			}
		}
	})
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-MSGSOCK Session frame writes)
// A start whose runtime completed the CH-RUNTIMEOPS handshake waits for the
// session_started carrying its startId: an answered start takes the record
// with the gate read and writes no session_end, and a start whose wait ends
// without the frame, whose frame carries error, or whose only answer
// carries another start's startId fails with errSessionStartUnacknowledged
// after writing session_end, leaving the entry's gate failed.
func TestOpenSequenceWaitsForSessionStarted_spec_28_5_3(t *testing.T) {
	t.Run("answered start", func(t *testing.T) {
		s, _, rt := ackServer(t)
		if err := startSession(context.Background(), s, "sess-a"); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		if st, _ := gateOf(s, "sess-a"); st != ackRead {
			t.Errorf("gate = %s, want read", st)
		}
		if got := rt.FrameTypes(); !equalTypes(got, []string{"session_start"}) {
			t.Errorf("frames = %v, want [session_start]", got)
		}
	})
	failures := map[string]ackruntime.Reply{
		"withheld":       ackruntime.Withhold,
		"carries error":  func(in []byte) []byte { return answerWith(in, "", "CONTEXT_CREATE_FAILED") },
		"stale start id": func(in []byte) []byte { return answerWith(in, "999", "") },
	}
	for name, reply := range failures {
		t.Run(name, func(t *testing.T) {
			s, _, rt := ackServer(t)
			s.SessionStartAckTimeout = 100 * time.Millisecond
			rt.SetReply(reply)
			claim, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false)
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			confirmed, err := s.openRuntimeSession(context.Background(), "sess-a", claim, manifestInputs{}, false)
			if confirmed || !errors.Is(err, errSessionStartUnacknowledged) {
				t.Fatalf("openRuntimeSession = (%v, %v), want (false, errSessionStartUnacknowledged)", confirmed, err)
			}
			if got := rt.FrameTypes(); !equalTypes(got, []string{"session_start", "session_end"}) {
				t.Errorf("frames = %v, want [session_start session_end]", got)
			}
			if got := claim.entry.ack.current(); got != ackFailed {
				t.Errorf("gate = %s, want failed", got)
			}
			s.mu.Lock()
			held := s.runtimeHoldsLocked("sess-a")
			s.mu.Unlock()
			if held {
				t.Error("the unacknowledged start took the record")
			}
		})
	}
	t.Run("StartSession answers Internal and releases the claim", func(t *testing.T) {
		s, _, rt := ackServer(t)
		s.SessionStartAckTimeout = 50 * time.Millisecond
		rt.SetReply(ackruntime.Withhold)
		err := startSession(context.Background(), s, "sess-a")
		if status.Code(err) != codes.Internal {
			t.Fatalf("StartSession = %v, want Internal", err)
		}
		if _, ok := gateOf(s, "sess-a"); ok {
			t.Error("the failed start left its registry entry")
		}
	})
}

// answerWith answers a session_start with a session_started carrying
// startID in place of the frame's own when startID is set, and an error
// object when code is set.
func answerWith(in []byte, startID, code string) []byte {
	var f struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		StartID   string `json:"startId"`
	}
	if err := json.Unmarshal(in, &f); err != nil || f.Type != "session_start" {
		return nil
	}
	if startID == "" {
		startID = f.StartID
	}
	return ackruntime.SessionStarted(f.SessionID, startID, code)
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started)
// The wait ends at the earlier of the request's deadline and
// SessionStartAckTimeout, whose zero value selects the runtimekit default; a
// start whose runtime had not completed the handshake when it began does
// not wait and leaves the gate not awaiting.
func TestSessionStartedWaitBounds_spec_28_5_3(t *testing.T) {
	t.Run("zero value selects the runtimekit default", func(t *testing.T) {
		s := New("ack-test")
		if got := s.sessionStartAckTimeout(); got != runtimekit.DefaultSessionStartAckTimeout {
			t.Errorf("default = %s, want %s", got, runtimekit.DefaultSessionStartAckTimeout)
		}
		s.SessionStartAckTimeout = 3 * time.Second
		if got := s.sessionStartAckTimeout(); got != 3*time.Second {
			t.Errorf("configured = %s, want 3s", got)
		}
	})
	t.Run("the request deadline ends the wait first", func(t *testing.T) {
		s, _, rt := ackServer(t)
		s.SessionStartAckTimeout = time.Minute
		rt.SetReply(ackruntime.Withhold)
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		began := time.Now()
		if err := startSession(ctx, s, "sess-a"); err == nil {
			t.Fatal("StartSession succeeded with no session_started")
		}
		if elapsed := time.Since(began); elapsed > 5*time.Second {
			t.Errorf("the wait ran %s, want it bounded by the 150ms request deadline", elapsed)
		}
	})
	t.Run("no handshake means no wait", func(t *testing.T) {
		lc, _ := startRuntimeOps(t)
		s := New("ack-test")
		s.Lifecycle = lc
		rt := ackruntime.New(t)
		rt.SetReply(ackruntime.Withhold)
		s.Runtime = rt
		if err := startSession(context.Background(), s, "sess-a"); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		if st, _ := gateOf(s, "sess-a"); st != ackNotAwaiting {
			t.Errorf("gate = %s, want not_awaiting", st)
		}
	})
	t.Run("an mcp runtime does not wait", func(t *testing.T) {
		s, _, _ := ackServer(t)
		s.RuntimeKind = RuntimeKindMCP
		if s.startAwaitsSessionStarted(context.Background()) {
			t.Error("a type: mcp start waits for session_started")
		}
	})
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages), 28.5.3 (CH-MSGSOCK Outbound: session_started)
// Regression: a deadline_approaching for a session whose start is still
// waiting for its session_started is held until the adapter reads that
// frame. Before the gate, SignalDeadline wrote the frame at once, so a
// multi-session runtime could receive it ahead of the session_start it
// concerns.
func TestSignalDeadlineWaitsForSessionStarted_spec_28_5_3(t *testing.T) {
	s, peer, rt := ackServer(t)
	rt.SetReply(ackruntime.Withhold)
	startErr := make(chan error, 1)
	go func() { startErr <- startSession(context.Background(), s, "sess-a") }()
	startID := waitForStartID(t, rt)

	signalled := make(chan *adapterv1.SignalDeadlineResponse, 1)
	go func() {
		resp, err := s.SignalDeadline(context.Background(), &adapterv1.SignalDeadlineRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-a"}, RemainingMs: 30_000,
		})
		if err != nil {
			t.Errorf("SignalDeadline: %v", err)
		}
		signalled <- resp
	}()
	expectNoFrame(t, peer, 200*time.Millisecond)

	rt.Emit(ackruntime.SessionStarted("sess-a", startID, ""))
	if err := <-startErr; err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	got := peer.read()
	if got.Type != "deadline_approaching" || got.SessionID != "sess-a" {
		t.Fatalf("runtime saw %+v, want deadline_approaching for sess-a", got)
	}
	if resp := <-signalled; resp == nil || !resp.GetDelivered() {
		t.Errorf("SignalDeadline = %+v, want delivered", resp)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages)
// A sender whose session's gate failed writes no frame and ends as it ends
// when the runtime does not answer: SignalDeadline reports undelivered, a
// clean Interrupt answers INTERRUPT_TIMEOUT, a rotation takes the
// acknowledgement-timeout fallback, a checkpoint fails its quiesce
// handshake, and a mid-session FinalizeWorkspace delivers no files_updated.
func TestFailedGateWritesNoRuntimeOpsFrame_spec_28_5_3(t *testing.T) {
	s, peer, _ := ackServer(t)
	s.CredentialsDir = t.TempDir()
	s.WorkspaceBase = t.TempDir()
	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		BindAttempt: "attempt-a",
		SessionId:   &adapterv1.SessionId{Value: "sess-a"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "l-1", Provider: "anthropic", Payload: []byte("{}")},
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	if err := startSession(context.Background(), s, "sess-a"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	st := s.slotStateForSession("sess-a")
	st.ack.fail()
	ctx := context.Background()
	sid := &adapterv1.SessionId{Value: "sess-a"}

	sd, err := s.SignalDeadline(ctx, &adapterv1.SignalDeadlineRequest{SessionId: sid, RemainingMs: 1000})
	if err != nil || sd.GetDelivered() {
		t.Errorf("SignalDeadline = (%+v, %v), want undelivered", sd, err)
	}
	ir, err := s.Interrupt(ctx, &adapterv1.InterruptRequest{
		SessionId: sid, Mode: adapterv1.InterruptRequest_MODE_CLEAN, DeadlineMs: 1000,
	})
	if err != nil || ir.GetStatus() != adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT {
		t.Errorf("Interrupt = (%+v, %v), want INTERRUPT_TIMEOUT", ir, err)
	}
	_, err = s.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
		SessionId: sid,
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "l-2", Provider: "anthropic", Payload: []byte("{}")},
		},
	})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("RotateCredentials = %v, want the DeadlineExceeded fallback", err)
	}
	if err := s.awaitCheckpointGate(ctx, st, &adapterv1.CheckpointStart{DeadlineMs: 1000}); status.Code(err) != codes.Internal {
		t.Errorf("checkpoint gate = %v, want Internal", err)
	}
	s.signalFilesUpdated(ctx, st, "sess-a")
	expectNoFrame(t, peer, 200*time.Millisecond)
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages)
// A runtime with no CH-RUNTIMEOPS has no checkpoint frame to gate, and a
// clean interrupt whose caller gave up while it waited on the gate answers
// the caller's context error.
func TestRuntimeOpsGateEdges_spec_28_5_3(t *testing.T) {
	s := New("ack-test")
	if err := s.awaitCheckpointGate(context.Background(), nil, &adapterv1.CheckpointStart{}); err != nil {
		t.Errorf("checkpoint gate without CH-RUNTIMEOPS = %v, want nil", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := interruptGateRefused(ctx); status.Code(err) != codes.Canceled {
		t.Errorf("interruptGateRefused(cancelled) = %v, want Canceled", err)
	}
	resp, err := s.awaitInterruptGate(context.Background(), &slotState{}, 20)
	if err != nil || resp.GetStatus() != adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT {
		t.Errorf("awaitInterruptGate on a gate no start settles = (%+v, %v), want INTERRUPT_TIMEOUT", resp, err)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages), 28.5.3 (CH-MSGSOCK Session frame writes)
// A removal releases the entry's gate: a sender waiting on a pending gate
// returns when a Shutdown whose guard acquisition expired deregisters the
// entry while the start still waits for its session_started.
func TestDeregistrationReleasesTheGate_spec_28_5_3(t *testing.T) {
	s, _, rt := ackServer(t)
	rt.SetReply(ackruntime.Withhold)
	s.SessionStartAckTimeout = 2 * time.Second
	go func() { _ = startSession(context.Background(), s, "sess-a") }()
	waitForStartID(t, rt)
	st := s.slotStateForSession("sess-a")
	waited := make(chan error, 1)
	go func() { waited <- s.awaitSessionStarted(context.Background(), st) }()
	// The open sequence holds the slot guard across its wait, so a Shutdown
	// whose guard acquisition expires removes the entry unguarded, which is
	// the removal that can land while an acknowledgement is pending.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.Shutdown(shutdownCtx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-a"}, UnconditionalTeardown: true,
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-waited:
		if !errors.Is(err, errSessionStartNotAcknowledged) {
			t.Errorf("waiter after Shutdown = %v, want errSessionStartNotAcknowledged", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter did not return when Shutdown released the gate")
	}
	if st.ack.current() != ackReleased {
		t.Errorf("deregistered gate = %s, want released", st.ack.current())
	}
}
