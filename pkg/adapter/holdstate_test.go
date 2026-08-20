// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// holdRuntime is a minimal RuntimeProcess recording whether the §10.1
// hold-timeout path closed it.
type holdRuntime struct {
	closed []string
}

func (r *holdRuntime) Start(context.Context, string) error { return nil }
func (r *holdRuntime) WriteEnvelope(string, []byte) error  { return nil }
func (r *holdRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}
func (r *holdRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *holdRuntime) Close(_ context.Context, sessionID string) error {
	r.closed = append(r.closed, sessionID)
	return nil
}

func holdGauge() float64 { return testutil.ToFloat64(coordinatorHold.WithLabelValues()) }

// spec: §10.1 — a closed gateway control stream with a live
// session is the coordinator-loss signal; the adapter enters hold state,
// raises the gauge, and arms the coordinatorHoldTimeoutSeconds timer.
func TestHoldStateEntersOnControlChannelLoss_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	clk := &fakeExpiryClock{}
	s := New("hold-test")
	s.HoldAfterFunc = clk.After
	s.CoordinatorHoldTimeout = 90 * time.Second
	if err := s.claimSessionForTest("s1"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeControlStream(ctx)
	done := make(chan error, 1)
	go func() { done <- s.AdapterEvents(stream) }()
	awaitRegistration(t, s)

	cancel()
	<-done

	if !s.inHoldState() {
		t.Fatal("expected hold state after CH-ADAPTEREVENTS loss with a live session")
	}
	if holdGauge() != 1 {
		t.Errorf("coordinator-hold gauge = %v, want 1", holdGauge())
	}
	if armed := clk.last(); armed == nil || armed.d != 90*time.Second {
		t.Fatalf("hold timer not armed with configured timeout: %+v", armed)
	}
}

// spec: §10.1.4 — a closed control stream on an idle pod (no live
// session) is not a coordinator loss, so no hold is entered.
func TestHoldStateNotEnteredWhenIdle_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	clk := &fakeExpiryClock{}
	s := New("hold-test")
	s.HoldAfterFunc = clk.After

	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeControlStream(ctx)
	done := make(chan error, 1)
	go func() { done <- s.AdapterEvents(stream) }()
	awaitRegistration(t, s)
	cancel()
	<-done

	if s.inHoldState() {
		t.Fatal("idle pod must not enter hold state")
	}
	if len(clk.armed()) != 0 {
		t.Errorf("no hold timer should be armed for an idle pod, got %d", len(clk.armed()))
	}
}

// spec: §10.1.4 — a successful CoordinatorFence from a new
// coordinator is the only way to exit hold state; it stops the timer and
// lowers the gauge.
func TestHoldStateExitedByCoordinatorFence_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	clk := &fakeExpiryClock{}
	s := New("hold-test")
	s.HoldAfterFunc = clk.After
	if err := s.claimSessionForTest("s1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	s.enterHoldState("s1")
	if !s.inHoldState() {
		t.Fatal("precondition: expected hold state")
	}

	if _, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId:              &adapterv1.SessionId{Value: "s1"},
		CoordinationGeneration: 1,
	}); err != nil {
		t.Fatalf("CoordinatorFence: %v", err)
	}
	if s.inHoldState() {
		t.Fatal("CoordinatorFence must exit hold state")
	}
	if holdGauge() != 0 {
		t.Errorf("gauge = %v, want 0 after fence", holdGauge())
	}
	if armed := clk.last(); armed == nil || !armed.isStopped() {
		t.Errorf("hold timer should be stopped after fence: %+v", armed)
	}
}

// spec: §10.1.4 — when no coordinator fences within the timeout the
// adapter notifies the gateway (AdapterTerminating), writes a disk
// post-mortem, lowers the gauge, and closes the runtime.
func TestHoldStateTimeoutTerminates_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	clk := &fakeExpiryClock{}
	rt := &holdRuntime{}
	dir := t.TempDir()
	s := New("hold-test")
	s.HoldAfterFunc = clk.After
	s.Runtime = rt
	s.PostMortemDir = dir
	if err := s.claimSessionForTest("sess-42"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Record a fenced generation so the post-mortem carries it.
	if _, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId:              &adapterv1.SessionId{Value: "sess-42"},
		CoordinationGeneration: 7,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}

	dropBefore := testutil.ToFloat64(controlEventsDropped.WithLabelValues(eventAdapterTerminating, "no_stream"))
	s.enterHoldState("sess-42")
	armed := clk.last()
	if armed == nil {
		t.Fatal("hold timer not armed")
	}
	armed.fire()

	if s.inHoldState() {
		t.Fatal("hold must clear after timeout")
	}
	if holdGauge() != 0 {
		t.Errorf("gauge = %v, want 0 after timeout", holdGauge())
	}
	if len(rt.closed) != 1 || rt.closed[0] != "sess-42" {
		t.Errorf("runtime Close calls = %v, want [sess-42]", rt.closed)
	}
	dropAfter := testutil.ToFloat64(controlEventsDropped.WithLabelValues(eventAdapterTerminating, "no_stream"))
	if dropAfter-dropBefore != 1 {
		t.Errorf("AdapterTerminating drop delta = %v, want 1 (no coordinator stream)", dropAfter-dropBefore)
	}

	pm := filepath.Join(dir, "coordinator_lost-sess-42.json")
	data, err := os.ReadFile(pm)
	if err != nil {
		t.Fatalf("post-mortem not written: %v", err)
	}
	var rec struct {
		SessionID      string `json:"sessionId"`
		Reason         string `json:"reason"`
		LastGeneration int64  `json:"lastGeneration"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("decode post-mortem: %v", err)
	}
	if rec.SessionID != "sess-42" || rec.Reason != reasonCoordinatorLost || rec.LastGeneration != 7 {
		t.Errorf("post-mortem = %+v", rec)
	}
}

// spec: §10.1.4 — a fence that races in before the timeout fires
// disarms the termination; a stale timer callback is a no-op.
func TestHoldStateTimeoutNoOpAfterFence_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	clk := &fakeExpiryClock{}
	rt := &holdRuntime{}
	s := New("hold-test")
	s.HoldAfterFunc = clk.After
	s.Runtime = rt
	if err := s.claimSessionForTest("s1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	s.enterHoldState("s1")
	armed := clk.last()

	if _, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId:              &adapterv1.SessionId{Value: "s1"},
		CoordinationGeneration: 1,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}
	// Fire the now-stale timer; onHoldTimeout must observe the cleared hold
	// and do nothing.
	armed.fire()
	if len(rt.closed) != 0 {
		t.Errorf("a fenced-out hold must not terminate the runtime, got %v", rt.closed)
	}
}

// spec: §10.1 — entering hold twice arms only one timer.
func TestHoldStateEnterIdempotent_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	clk := &fakeExpiryClock{}
	s := New("hold-test")
	s.HoldAfterFunc = clk.After
	_ = s.claimSessionForTest("s1")
	s.enterHoldState("s1")
	s.enterHoldState("s1")
	if got := len(clk.armed()); got != 1 {
		t.Errorf("armed timers = %d, want 1 (idempotent entry)", got)
	}
}

// spec: §10.1.4 — the unary interceptor rejects every
// non-allowlisted RPC with UNAVAILABLE + coordinator_hold while held, and
// passes CoordinatorFence through.
func TestHoldStateUnaryInterceptor_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	s := New("hold-test")
	_ = s.claimSessionForTest("s1")
	s.enterHoldState("s1")

	handlerCalled := false
	handler := func(context.Context, any) (any, error) {
		handlerCalled = true
		return "ok", nil
	}

	_, err := s.holdStateUnaryInterceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/lenny.adapter.v1.Adapter/SendMessage"}, handler)
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "coordinator_hold") {
		t.Fatalf("operational RPC during hold = %v, want Unavailable coordinator_hold", err)
	}
	if handlerCalled {
		t.Error("rejected RPC must not reach the handler")
	}

	handlerCalled = false
	if _, err := s.holdStateUnaryInterceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/lenny.adapter.v1.Adapter/CoordinatorFence"}, handler); err != nil {
		t.Fatalf("CoordinatorFence must pass through hold: %v", err)
	}
	if !handlerCalled {
		t.Error("allowlisted CoordinatorFence must reach the handler")
	}
}

// spec: §10.1.4 — the stream interceptor rejects Attach while held
// but admits the AdapterEvents so a new coordinator can re-attach.
func TestHoldStateStreamInterceptor_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	s := New("hold-test")
	_ = s.claimSessionForTest("s1")
	s.enterHoldState("s1")

	handlerCalled := false
	handler := func(any, grpc.ServerStream) error { handlerCalled = true; return nil }

	err := s.holdStateStreamInterceptor(nil, nil,
		&grpc.StreamServerInfo{FullMethod: "/lenny.adapter.v1.Adapter/Attach"}, handler)
	if status.Code(err) != codes.Unavailable || handlerCalled {
		t.Fatalf("Attach during hold = %v (called=%v), want Unavailable", err, handlerCalled)
	}

	handlerCalled = false
	if err := s.holdStateStreamInterceptor(nil, nil,
		&grpc.StreamServerInfo{FullMethod: "/lenny.adapter.v1.Adapter/AdapterEvents"}, handler); err != nil || !handlerCalled {
		t.Fatalf("AdapterEvents must pass through hold: err=%v called=%v", err, handlerCalled)
	}
}

// spec: §10.1.4 — the interceptors are inert when no hold is
// active, so steady-state RPCs run unimpeded.
func TestHoldStateInterceptorInertWhenNotHeld_spec_10_1(t *testing.T) {
	s := New("hold-test")
	called := false
	handler := func(context.Context, any) (any, error) { called = true; return nil, nil }
	if _, err := s.holdStateUnaryInterceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/lenny.adapter.v1.Adapter/SendMessage"}, handler); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler must run when not in hold state")
	}
}

// spec: §10.1.4 — the hold timeout defaults to 120s and honours an
// operator override.
func TestCoordinatorHoldTimeoutDefault_spec_10_1(t *testing.T) {
	s := New("hold-test")
	if got := s.coordinatorHoldTimeout(); got != defaultCoordinatorHoldTimeout {
		t.Errorf("default timeout = %v, want %v", got, defaultCoordinatorHoldTimeout)
	}
	s.CoordinatorHoldTimeout = 45 * time.Second
	if got := s.coordinatorHoldTimeout(); got != 45*time.Second {
		t.Errorf("override timeout = %v, want 45s", got)
	}
}

// spec: 10.1 (coordinator-loss hold), 4.11 (the four states a registry
// entry holds), 5.2 (every session is bound to a slot on every pod)
//
// The hold arms from the slot registry's started entries. Every session is
// bound to a slot on every pod, so a pod-global session field named no
// session on a pod whose sessions took the slot path and the hold never
// armed there: the coordinating gateway could crash and the agent kept
// running, unsupervised and unfenced, until the orphan reconciler noticed.
//
// diagnosis: a failure means the hold arms from something other than the
// started entries again. Either it no longer arms for a session the
// adapter started, which leaves the agent running unsupervised after the
// coordinating gateway is lost, or it arms for an entry no session has
// started on, which rejects every RPC on a pod that is serving nobody.
func TestCoordinatorHoldArmsFromStartedSlotRegistry_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	clk := &fakeExpiryClock{}
	s := New("hold-arming")
	s.WorkspaceBase = t.TempDir()
	s.HoldAfterFunc = clk.After
	if err := s.claimSessionForTest("s1"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	s.onCoordinatorChannelClosed()

	if !s.inHoldState() {
		t.Fatal("the hold did not arm on a pod holding a started session")
	}
	if holdGauge() != 1 {
		t.Errorf("coordinator-hold gauge = %v, want 1", holdGauge())
	}

	// The hold's unit is the pod: every inbound RPC outside the allowlist
	// is rejected while it is held, and CoordinatorFence is admitted.
	_, err := s.holdStateUnaryInterceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/lenny.adapter.v1.Adapter/SendMessage"},
		func(context.Context, any) (any, error) { return nil, nil })
	if status.Code(err) != codes.Unavailable {
		t.Errorf("SendMessage under the hold = %v, want Unavailable", status.Code(err))
	}
	if _, err := s.holdStateUnaryInterceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/lenny.adapter.v1.Adapter/CoordinatorFence"},
		func(context.Context, any) (any, error) { return nil, nil }); err != nil {
		t.Errorf("CoordinatorFence under the hold = %v, want admitted", err)
	}

	// A fence exits the hold, so a later RPC is admitted again.
	if _, err := s.CoordinatorFence(context.Background(), &adapterv1.CoordinatorFenceRequest{
		SessionId:              &adapterv1.SessionId{Value: "s1"},
		CoordinationGeneration: 1,
	}); err != nil {
		t.Fatalf("CoordinatorFence: %v", err)
	}
	if s.inHoldState() {
		t.Fatal("the fence did not exit the hold")
	}
	if _, err := s.holdStateUnaryInterceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/lenny.adapter.v1.Adapter/SendMessage"},
		func(context.Context, any) (any, error) { return nil, nil }); err != nil {
		t.Errorf("SendMessage after the fence = %v, want admitted", err)
	}
}

// spec: 10.1, 4.11 (registered and bound-not-started are not started)
//
// A pod whose only entry has not been started is serving no agent process,
// so a closed control stream there is not a coordinator loss. Arming on it
// would reject every RPC on a pod mid-bind, failing the bind that is still
// in flight.
//
// diagnosis: a failure means the arming predicate reads the entry's
// existence or its bound state rather than the started flag, so a pod that
// has only had its workspace prepared, or has only had credentials
// assigned, enters a hold and rejects the rest of its own bind.
func TestCoordinatorHoldDoesNotArmOnAnUnstartedEntry_spec_10_1(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(t *testing.T, s *Server)
	}{
		{
			name: "registered by a workspace-prep RPC and not yet bound",
			seed: func(t *testing.T, s *Server) {
				t.Helper()
				s.mu.Lock()
				defer s.mu.Unlock()
				if _, err := s.ensureSlotStateLocked("s1"); err != nil {
					t.Fatalf("ensure slot state: %v", err)
				}
			},
		},
		{
			name: "bound by the credentials-first bind order and not yet started",
			seed: func(t *testing.T, s *Server) {
				t.Helper()
				s.mu.Lock()
				defer s.mu.Unlock()
				st, err := s.ensureSlotStateLocked("s1")
				if err != nil {
					t.Fatalf("ensure slot state: %v", err)
				}
				st.sessionID = "s1"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setCoordinatorHold(false)
			clk := &fakeExpiryClock{}
			s := New("hold-arming")
			s.WorkspaceBase = t.TempDir()
			s.HoldAfterFunc = clk.After
			tc.seed(t, s)

			s.onCoordinatorChannelClosed()

			if s.inHoldState() {
				t.Error("the hold armed on a pod that has started no session")
			}
			if len(clk.armed()) != 0 {
				t.Errorf("armed %d hold timers, want 0", len(clk.armed()))
			}
		})
	}
}

// spec: 10.1 (coordinator-lost self-termination), 9.1 (platform tool
// surface), 15.4.3 (intra-pod MCP), 11.2 (direct-mode usage)
//
// The hold timeout closes the session on the pod's one shared runtime
// process, so that session leaves the runtime generation with the close.
// A termination that left it in the generation kept naming a terminated
// session as the process's sole occupant: the intra-pod MCP surface went
// on forwarding tool calls under its principal, direct-mode token counts
// went on folding into its budget, and the pod surface could never be
// cancelled because the process never read as idle.
//
// diagnosis: a failure means the coordinator-lost close and the runtime
// generation have come apart again. The pod keeps acting as a terminated
// session after its coordinator was lost, which is a fail-open in the
// gate that exists to fail closed.
func TestCoordinatorHoldTerminationLeavesTheRuntimeGeneration_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	clk := &fakeExpiryClock{}
	rt := &holdRuntime{}
	s := New("hold-generation")
	s.WorkspaceBase = t.TempDir()
	s.HoldAfterFunc = clk.After
	s.Runtime = rt
	if err := s.claimSessionForTest("sess-42"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := s.soleSession(); got != "sess-42" {
		t.Fatalf("sole session before the hold = %q, want sess-42", got)
	}

	s.enterHoldState("sess-42")
	armed := clk.last()
	if armed == nil {
		t.Fatal("hold timer not armed")
	}
	armed.fire()

	if len(rt.closed) != 1 || rt.closed[0] != "sess-42" {
		t.Fatalf("runtime Close calls = %v, want [sess-42]", rt.closed)
	}
	if got := s.soleSession(); got != "" {
		t.Errorf("sole session after the coordinator-lost close = %q, want empty", got)
	}
	if _, err := s.callingSession(); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("intra-pod MCP calling session after the termination = %v, want FailedPrecondition; "+
			"the surface must not forward under a terminated session's principal", status.Code(err))
	}
	s.mu.Lock()
	idle := s.runtimeIdleLocked()
	s.mu.Unlock()
	if !idle {
		t.Error("the shared runtime process still reads as occupied after the terminated session's close, so the pod surface can never be cancelled")
	}
}
