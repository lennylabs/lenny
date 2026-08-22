// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
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
	s.enterHoldState()
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
	s.enterHoldState()
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
	s.enterHoldState()
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
	s.enterHoldState()
	s.enterHoldState()
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
	s.enterHoldState()

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
	s.enterHoldState()

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

	s.enterHoldState()
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

// sharedHoldRuntime models the pod's one shared runtime process the way
// the socket runtime behaves: Close removes the named session from the
// process's active set and ends the process only when the last member
// leaves, so a non-last close tears nothing down. onClose runs with the
// closing member's identifier before the removal, so a case can observe
// adapter state at the instant a member's runtime is closed.
type sharedHoldRuntime struct {
	mu       sync.Mutex
	active   map[string]struct{}
	closedIn []string
	procEnds int
	onClose  func(sessionID string)
}

func newSharedHoldRuntime() *sharedHoldRuntime {
	return &sharedHoldRuntime{active: map[string]struct{}{}}
}

func (r *sharedHoldRuntime) Start(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[sessionID] = struct{}{}
	return nil
}
func (r *sharedHoldRuntime) WriteEnvelope(string, []byte) error { return nil }
func (r *sharedHoldRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}
func (r *sharedHoldRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *sharedHoldRuntime) Close(_ context.Context, sessionID string) error {
	if r.onClose != nil {
		r.onClose(sessionID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closedIn = append(r.closedIn, sessionID)
	if _, ok := r.active[sessionID]; !ok {
		return nil
	}
	delete(r.active, sessionID)
	if len(r.active) == 0 {
		r.procEnds++
	}
	return nil
}

func (r *sharedHoldRuntime) closes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.closedIn...)
}

func (r *sharedHoldRuntime) processEnds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.procEnds
}

// recordingScrubReporter counts the §5.2 cleanup-outcome reports the
// adapter sends, so a case can assert the hold-termination loop sends none.
type recordingScrubReporter struct {
	mu       sync.Mutex
	sessions []string
}

func (r *recordingScrubReporter) ReportSessionScrub(_ context.Context, _, sessionID string, _ gatewaycontrol.SessionScrubOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions = append(r.sessions, sessionID)
	return nil
}

func (r *recordingScrubReporter) reported() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sessions...)
}

// holdTerminationServer builds an adapter holding one started session per
// identifier, with the per-slot trees rooted under a temp directory and
// the hold timer driven through the injected seam.
func holdTerminationServer(t *testing.T, rt RuntimeProcess, sessions ...string) (*Server, *fakeExpiryClock) {
	t.Helper()
	base := t.TempDir()
	clk := &fakeExpiryClock{}
	s := New("hold-termination")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.PostMortemDir = t.TempDir()
	s.HoldAfterFunc = clk.After
	s.Runtime = rt
	for _, id := range sessions {
		if err := s.claimSessionForTest(id); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if rt != nil {
			if err := rt.Start(context.Background(), id); err != nil {
				t.Fatalf("runtime start %s: %v", id, err)
			}
		}
	}
	return s, clk
}

// fireHoldTimeout arms the hold and fires its timer.
func fireHoldTimeout(t *testing.T, s *Server, clk *fakeExpiryClock) {
	t.Helper()
	s.enterHoldState()
	armed := clk.last()
	if armed == nil {
		t.Fatal("hold timer not armed")
	}
	armed.fire()
}

// drainControlEvents reads want events off the stream and reports that no
// further event follows them.
func drainControlEvents(t *testing.T, stream *fakeControlStream, want int) []controlEvent {
	t.Helper()
	evs := make([]controlEvent, 0, want)
	for i := 0; i < want; i++ {
		evs = append(evs, recvEvent(t, stream))
	}
	select {
	case extra := <-stream.sent:
		t.Errorf("an unexpected extra control event followed: %s", extra.GetEnvelopeJson())
	case <-time.After(50 * time.Millisecond):
	}
	return evs
}

// spec: 10.1 (coordinator-lost self-termination), 10.1.4 (the hold
// timeout), 4.11 (the started entries the timeout terminates)
//
// The timeout terminates every session the adapter started on the pod,
// once per member. A single-valued termination on a pod holding two
// started sessions is inert rather than partial: the shared runtime
// process's own active set makes a non-last close return without touching
// the child, so one close tears nothing down and both agent processes go
// on running with live provider credentials and no coordinator, on a pod
// that admits every RPC again from the line that clears the hold.
//
// diagnosis: a failure means the coordinator-lost termination has gone
// back to acting on one session. Either a co-tenant's agent process
// survives its own pod's self-termination unsupervised, or the gateway
// loses a terminated session's final usage report and its §8.3
// budget_return input with it, or a terminal event goes out unattributed.
func TestCoordinatorHoldTimeoutTerminatesEveryStartedSession_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	rt := newSharedHoldRuntime()
	s, clk := holdTerminationServer(t, rt, "sess-a", "sess-b")
	meter := NewSessionUsageMeter(time.Now)
	meter.Add("sess-a", 11, 1)
	meter.Add("sess-b", 22, 2)
	s.Usage = meter
	scrub := &recordingScrubReporter{}
	s.SessionScrubReporter = scrub
	stream, cancelStream := attachControlStream(t, s)
	defer cancelStream()

	fireHoldTimeout(t, s, clk)

	if got := rt.closes(); len(got) != 2 || got[0] != "sess-a" || got[1] != "sess-b" {
		t.Fatalf("runtime Close calls = %v, want [sess-a sess-b]", got)
	}
	if got := rt.processEnds(); got != 1 {
		t.Errorf("the shared runtime process ended %d time(s), want 1 (on the last member)", got)
	}
	for _, id := range []string{"sess-a", "sess-b"} {
		if _, err := os.ReadFile(filepath.Join(s.PostMortemDir, "coordinator_lost-"+id+".json")); err != nil {
			t.Errorf("post-mortem for %s: %v", id, err)
		}
	}

	terminating := map[string]int{}
	usage := map[string]controlUsage{}
	for _, ev := range drainControlEvents(t, stream, 4) {
		switch ev.Type {
		case eventAdapterTerminating:
			if ev.Reason != reasonCoordinatorLost {
				t.Errorf("AdapterTerminating reason = %q, want %q", ev.Reason, reasonCoordinatorLost)
			}
			terminating[ev.SessionID]++
		case eventFinalUsageReport:
			if ev.Usage == nil {
				t.Fatalf("FINAL_USAGE_REPORT for %q carries no usage", ev.SessionID)
			}
			usage[ev.SessionID] = *ev.Usage
		default:
			t.Errorf("unexpected control event on the hold-termination path: %+v", ev)
		}
	}
	for _, id := range []string{"sess-a", "sess-b"} {
		if terminating[id] != 1 {
			t.Errorf("AdapterTerminating envelopes naming %s = %d, want 1", id, terminating[id])
		}
	}
	if got := usage["sess-a"]; got.InputTokens != 11 || got.OutputTokens != 1 {
		t.Errorf("final usage for sess-a = %+v, want its own totals (11/1)", got)
	}
	if got := usage["sess-b"]; got.InputTokens != 22 || got.OutputTokens != 2 {
		t.Errorf("final usage for sess-b = %+v, want its own totals (22/2)", got)
	}
	if got := scrub.reported(); len(got) != 0 {
		t.Errorf("the hold-termination loop reported %v session scrubs, want none; it performs no scrub", got)
	}
}

// spec: 10.1.4 (the hold timeout's deregistration pass), 4.11 (the
// registry entry a termination removes), 5.2 (per-session teardown)
//
// The timeout deregisters every started entry in one critical section
// before it terminates any of them. Emptying the registry first is what
// makes the loop and a concurrent gateway Shutdown mutually exclusive, and
// it is what keeps the terminated sessions' entries from surviving for the
// life of a pod that goes on serving.
//
// diagnosis: a failure means the loop deregisters per member around each
// close, which reopens the window in which a Shutdown admitted during the
// loop runs the merged per-session teardown for a session the loop is
// already terminating.
func TestCoordinatorHoldTimeoutEmptiesTheSlotRegistry_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	rt := newSharedHoldRuntime()
	s, clk := holdTerminationServer(t, rt, "sess-a", "sess-b")
	var heldAtClose []int
	rt.onClose = func(string) {
		s.mu.Lock()
		heldAtClose = append(heldAtClose, len(s.slots))
		s.mu.Unlock()
	}

	fireHoldTimeout(t, s, clk)

	if len(heldAtClose) != 2 {
		t.Fatalf("observed %d runtime closes, want 2", len(heldAtClose))
	}
	for i, held := range heldAtClose {
		if held != 0 {
			t.Errorf("the registry held %d entr(ies) at close %d, want 0; "+
				"the deregistration pass must empty the registry before any termination work", held, i)
		}
	}
	s.mu.Lock()
	remaining := len(s.slots)
	s.mu.Unlock()
	if remaining != 0 {
		t.Errorf("the registry holds %d entr(ies) after the timeout, want 0", remaining)
	}
}

// spec: 10.1.4 (the coordinator-lost termination), 6.4 (the per-slot
// tree), 4.11 (a bound-not-started entry is not terminated)
//
// Pass 2 removes each terminated member's per-slot tree, which carries
// that session's §6.1 credential file. A termination that left it behind
// leaves a hold-terminated session's credential lease readable by every
// later slot's agent process on the pod. The removal follows that member's
// runtime close, so the agent is not reading a credential file the
// teardown has already unlinked.
//
// diagnosis: a failure means the hold-termination loop skips the
// filesystem half of the teardown, or removes a tree before the runtime
// that reads it is closed, or reaches a co-tenant whose session never
// started on this pod and is live on another.
func TestCoordinatorHoldTimeoutRemovesEveryTerminatedSessionsSlotTree_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	rt := newSharedHoldRuntime()
	s, clk := holdTerminationServer(t, rt, "sess-a", "sess-b")

	// A co-tenant the §4.7 bind sequence bound at credential assignment and
	// that never started here: the gateway has re-placed it on another pod.
	s.mu.Lock()
	cotenant, err := s.ensureSlotStateLocked("sess-c")
	if err != nil {
		s.mu.Unlock()
		t.Fatalf("seed the bound-not-started co-tenant: %v", err)
	}
	cotenant.sessionID = "sess-c"
	trees := map[string]struct{ current, creds string }{}
	for id, st := range s.slots {
		trees[id] = struct{ current, creds string }{st.paths.Current, st.paths.CredentialsFile}
	}
	s.mu.Unlock()

	for id, p := range trees {
		if err := os.WriteFile(filepath.Join(p.current, "agent.txt"), []byte(id), 0o600); err != nil {
			t.Fatalf("seed workspace for %s: %v", id, err)
		}
		if err := os.WriteFile(p.creds, []byte(`{"lease":"`+id+`"}`), 0o600); err != nil {
			t.Fatalf("seed credential file for %s: %v", id, err)
		}
	}

	// Each member's tree must still be readable while its own runtime close
	// is running, and only then removed.
	rt.onClose = func(sessionID string) {
		p := trees[sessionID]
		if _, err := os.Stat(p.creds); err != nil {
			t.Errorf("%s's credential file was already unlinked when its runtime close ran: %v", sessionID, err)
		}
	}

	fireHoldTimeout(t, s, clk)

	for _, id := range []string{"sess-a", "sess-b"} {
		p := trees[id]
		if _, err := os.Stat(p.current); !os.IsNotExist(err) {
			t.Errorf("terminated session %s still has a workspace tree at %s (err=%v)", id, p.current, err)
		}
		if _, err := os.Stat(p.creds); !os.IsNotExist(err) {
			t.Errorf("terminated session %s still has a credential file at %s (err=%v)", id, p.creds, err)
		}
	}
	p := trees["sess-c"]
	if _, err := os.Stat(p.current); err != nil {
		t.Errorf("the bound-not-started co-tenant's workspace tree was removed: %v", err)
	}
	if _, err := os.Stat(p.creds); err != nil {
		t.Errorf("the bound-not-started co-tenant's credential file was removed: %v", err)
	}
	if got := rt.closes(); len(got) != 2 {
		t.Errorf("runtime Close calls = %v, want the two started members alone", got)
	}
}

// spec: 10.1.4 (the hold timeout's deregistration pass), 15.4.2 (the
// CH-RUNTIMEOPS drain signal), 4.6.1 (an unaddressed inbound frame)
//
// The deregistration pass is what lets the pod serve a next session at
// all. Without it the terminated sessions' entries survive, so the next
// session's own teardown finds a bound co-tenant and sends no drain
// signal, and every unaddressed inbound frame on the pod is refused
// because the registry holds more than one entry. Both regressions are
// silent: the drain send error is swallowed.
//
// diagnosis: a failure means the hold timeout left the registry populated,
// so the pod is permanently degraded for every session placed on it after
// a coordinator loss.
func TestCoordinatorHoldTimeoutRecoversTheNextSession_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	lc, fr := startRuntimeOps(t)
	fr.handshake()
	rt := newSharedHoldRuntime()
	s, clk := holdTerminationServer(t, rt, "sess-a", "sess-b")
	s.Lifecycle = lc

	fireHoldTimeout(t, s, clk)

	if err := s.claimSessionForTest("sess-next"); err != nil {
		t.Fatalf("claim the next session: %v", err)
	}
	if err := rt.Start(context.Background(), "sess-next"); err != nil {
		t.Fatalf("start the next session: %v", err)
	}
	// §4.6.1 — an inbound frame carrying no session identifier resolves on
	// a pod holding one session, which is what the pass restored.
	if got := s.soleSession(); got != "sess-next" {
		t.Errorf("sole session after the timeout = %q, want sess-next", got)
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-next"},
		Reason:    "session_complete",
	}); err != nil {
		t.Fatalf("Shutdown the next session: %v", err)
	}
	if got := fr.read(); got.Type != "terminate" {
		t.Errorf("the next session's teardown sent %q, want the CH-RUNTIMEOPS terminate frame; "+
			"a surviving hold-terminated entry holds the drain gate false", got.Type)
	}
}

// spec: §10.1.4 — the hold-resolved line names no session and carries no
// structured fields. The hold's unit is the pod, and the generation it
// armed under is already on the coordinator_connection_lost line that
// opened the hold, so repeating it on the resolved line records nothing
// the operator does not already have. A build that emits any attribute on
// this line fails here.
func TestCoordinatorHoldResolvedLineCarriesNoFields_spec_10_1(t *testing.T) {
	setCoordinatorHold(false)
	logBuf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	clk := &fakeExpiryClock{}
	s := New("hold-test")
	s.HoldAfterFunc = clk.After
	if err := s.claimSessionForTest("s1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	s.enterHoldState()
	s.exitHoldState()

	var resolved map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logBuf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if rec["msg"] == "coordinator_hold_resolved" {
			resolved = rec
		}
	}
	if resolved == nil {
		t.Fatalf("no coordinator_hold_resolved line in %q", logBuf.String())
	}
	for k := range resolved {
		switch k {
		case slog.TimeKey, slog.LevelKey, slog.MessageKey:
		default:
			t.Errorf("coordinator_hold_resolved carries structured field %q = %v; the line takes none", k, resolved[k])
		}
	}
}
