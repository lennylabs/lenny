// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the §10.1 coordinator-lost
// hold termination.
//
// The hold arms on the sessions the adapter has started on the pod and its
// timeout terminates every one of them. The set is read when the timeout
// fires rather than recorded when the hold armed, because the hold-state
// interceptor gates admission rather than binding: a start admitted before
// the arming claims and runs afterwards, and a recorded set would leave
// that session's agent process running with no coordinator.
//
// The timeout deregisters every started entry in one critical section
// before it terminates any of them, which is what makes the loop and a
// concurrent gateway Shutdown mutually exclusive. That handler decides on
// the outcome of its own locked cancel-deregister step, so its step and
// the pass's are two acquisitions of one lock: one of them is first, and
// there is no schedule in which a request tests the entry before the pass
// and runs its teardown after it.
//
// spec: §10.1 (coordinator-loss hold), §10.1.4 (the hold timeout), §5.2
// (per-session teardown), §4.7 (the started entries the hold acts on).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// holdEvent is the decoded §4.7 control envelope the adapter pushes onto
// the gateway control stream.
type holdEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Reason    string `json:"reason"`
	Usage     *struct {
		InputTokens  int64 `json:"inputTokens"`
		OutputTokens int64 `json:"outputTokens"`
	} `json:"usage"`
}

// holdEventStream is a gateway control stream the case drives directly. It
// collects every envelope the adapter emits and ends when the case closes
// it, which is the coordinator loss the adapter arms its hold on.
type holdEventStream struct {
	grpc.ServerStream
	ctx    context.Context
	closed chan struct{}
	once   sync.Once
	done   chan error

	mu   sync.Mutex
	evs  []holdEvent
	seen int
}

func newHoldEventStream() *holdEventStream {
	return &holdEventStream{ctx: context.Background(), closed: make(chan struct{})}
}

func (f *holdEventStream) Context() context.Context     { return f.ctx }
func (f *holdEventStream) SetHeader(metadata.MD) error  { return nil }
func (f *holdEventStream) SendHeader(metadata.MD) error { return nil }
func (f *holdEventStream) SetTrailer(metadata.MD)       {}
func (f *holdEventStream) SendMsg(any) error            { return nil }
func (f *holdEventStream) RecvMsg(any) error            { return io.EOF }

func (f *holdEventStream) Send(r *adapterv1.AdapterEventsResponse) error {
	var ev holdEvent
	if err := json.Unmarshal(r.GetEnvelopeJson(), &ev); err != nil {
		return err
	}
	f.mu.Lock()
	f.evs = append(f.evs, ev)
	f.seen++
	f.mu.Unlock()
	return nil
}

func (f *holdEventStream) Recv() (*adapterv1.AdapterEventsRequest, error) {
	<-f.closed
	return nil, io.EOF
}

// drop ends the stream, which is the §10.1 coordinator-loss signal, and
// waits for the adapter's handler to return. The handler clears the pod's
// event sink and arms the hold on its way out, and at most one stream is
// open per pod, so a case that attaches a fresh stream waits here first.
func (f *holdEventStream) drop() {
	f.once.Do(func() {
		close(f.closed)
		if f.done != nil {
			<-f.done
		}
	})
}

// events returns everything received so far.
func (f *holdEventStream) events() []holdEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]holdEvent(nil), f.evs...)
}

// settle waits until no further envelope has arrived for quiet, so a case
// reads the whole of the termination's emissions.
func (f *holdEventStream) settle(t *testing.T, quiet time.Duration) []holdEvent {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		f.mu.Lock()
		mark := f.seen
		f.mu.Unlock()
		time.Sleep(quiet)
		f.mu.Lock()
		same := f.seen == mark
		f.mu.Unlock()
		if same {
			return f.events()
		}
		if time.Now().After(deadline) {
			t.Fatal("the control stream never went quiet")
		}
	}
}

// attach opens the control stream on s and blocks until the adapter has
// taken it as the pod's event sink.
func (f *holdEventStream) attach(t *testing.T, s *adapter.Server) {
	t.Helper()
	f.done = make(chan error, 1)
	go func() { f.done <- s.AdapterEvents(f) }()
	t.Cleanup(f.drop)
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.EmitRateLimited("hold-probe")
		f.mu.Lock()
		attached := f.seen > 0
		f.mu.Unlock()
		if attached {
			f.mu.Lock()
			f.evs, f.seen = nil, 0
			f.mu.Unlock()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the adapter never attached the control stream")
		}
		time.Sleep(time.Millisecond)
	}
}

// holdSharedRuntime models the pod's one shared runtime process: Close
// removes the named session from the process's active set and ends the
// process only when the last member leaves, so a non-last close returns
// without touching the child. Gates let a case park a Start or a Close.
type holdSharedRuntime struct {
	mu       sync.Mutex
	active   map[string]struct{}
	closed   []string
	procEnds int

	startGate map[string]chan struct{}
	closeGate map[string]chan struct{}
	onClosed  func(sessionID string)
}

func newHoldSharedRuntime() *holdSharedRuntime {
	return &holdSharedRuntime{
		active:    map[string]struct{}{},
		startGate: map[string]chan struct{}{},
		closeGate: map[string]chan struct{}{},
	}
}

func (r *holdSharedRuntime) gateStart(sessionID string) chan struct{} {
	g := make(chan struct{})
	r.mu.Lock()
	r.startGate[sessionID] = g
	r.mu.Unlock()
	return g
}

func (r *holdSharedRuntime) gateClose(sessionID string) chan struct{} {
	g := make(chan struct{})
	r.mu.Lock()
	r.closeGate[sessionID] = g
	r.mu.Unlock()
	return g
}

func (r *holdSharedRuntime) Start(_ context.Context, sessionID string) error {
	r.mu.Lock()
	g := r.startGate[sessionID]
	r.mu.Unlock()
	if g != nil {
		<-g
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[sessionID] = struct{}{}
	return nil
}

func (r *holdSharedRuntime) WriteEnvelope(string, []byte) error { return nil }

func (r *holdSharedRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *holdSharedRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *holdSharedRuntime) Close(_ context.Context, sessionID string) error {
	r.mu.Lock()
	g := r.closeGate[sessionID]
	r.mu.Unlock()
	if g != nil {
		<-g
	}
	r.mu.Lock()
	r.closed = append(r.closed, sessionID)
	if _, ok := r.active[sessionID]; ok {
		delete(r.active, sessionID)
		if len(r.active) == 0 {
			r.procEnds++
		}
	}
	r.mu.Unlock()
	if r.onClosed != nil {
		r.onClosed(sessionID)
	}
	return nil
}

func (r *holdSharedRuntime) closes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.closed...)
}

func (r *holdSharedRuntime) processEnds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.procEnds
}

func (r *holdSharedRuntime) resident() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.active))
	for id := range r.active {
		out = append(out, id)
	}
	return out
}

// holdScrubReporter counts the §5.2 cleanup-outcome reports per session.
type holdScrubReporter struct {
	mu    sync.Mutex
	count map[string]int
}

func newHoldScrubReporter() *holdScrubReporter {
	return &holdScrubReporter{count: map[string]int{}}
}

func (r *holdScrubReporter) ReportSessionScrub(_ context.Context, _, sessionID string, _ gatewaycontrol.SessionScrubOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count[sessionID]++
	return nil
}

func (r *holdScrubReporter) counts() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for k, v := range r.count {
		out[k] = v
	}
	return out
}

// holdRacePod builds an adapter whose per-slot trees sit under a temp
// directory, with the §10.1 hold timeout set to hold.
func holdRacePod(t *testing.T, rt adapter.RuntimeProcess, hold time.Duration) *adapter.Server {
	t.Helper()
	base := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.PostMortemDir = t.TempDir()
	s.Runtime = rt
	s.CoordinatorHoldTimeout = hold
	return s
}

func startHoldSession(t *testing.T, s *adapter.Server, sessionID string) {
	t.Helper()
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", sessionID, err)
	}
}

// awaitClosed blocks until the runtime has seen a close for every session.
func awaitClosed(t *testing.T, rt *holdSharedRuntime, sessions ...string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		seen := map[string]bool{}
		for _, id := range rt.closes() {
			seen[id] = true
		}
		all := true
		for _, id := range sessions {
			if !seen[id] {
				all = false
			}
		}
		if all {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the termination never closed every session; closes = %v", rt.closes())
		}
		time.Sleep(time.Millisecond)
	}
}

// spec: 10.1 (coordinator-loss hold), 10.1.4 (the hold timeout), 5.2
// (per-session teardown), 4.11 (the started entries the hold terminates)
//
// diagnosis: a failure means the hold termination and a concurrent gateway
// Shutdown are no longer mutually exclusive, or the terminated set is
// recorded when the hold arms rather than read when it fires. The first
// leaves a terminated session double-flushed onto §8.3 budget_return and
// reported as scrubbed by a request that tore nothing down. The second
// leaves the agent process of a session started inside the hold running
// with live provider credentials and no coordinator.
func TestCoordinatorHoldTerminationRacesConcurrentShutdown_spec_10_1(t *testing.T) {
	t.Run("the terminated set is read when the timeout fires", func(t *testing.T) {
		rt := newHoldSharedRuntime()
		s := holdRacePod(t, rt, 150*time.Millisecond)
		meter := adapter.NewSessionUsageMeter(time.Now)
		meter.Add("sess-a", 5, 1)
		meter.Add("sess-b", 7, 2)
		s.Usage = meter
		startHoldSession(t, s, "sess-a")

		arming := newHoldEventStream()
		arming.attach(t, s)
		// The coordinator is lost with one started session on the pod.
		arming.drop()

		// A start admitted before the arming claims and runs afterwards. A
		// set recorded at arming time omits it, and its agent process then
		// survives the pod's own self-termination.
		startHoldSession(t, s, "sess-b")

		// A new coordinator re-attaches the stream without fencing, which
		// the hold's allowlist admits, so the loop's emissions are visible.
		observer := newHoldEventStream()
		observer.attach(t, s)

		awaitClosed(t, rt, "sess-a", "sess-b")
		evs := observer.settle(t, 100*time.Millisecond)

		if got := rt.processEnds(); got != 1 {
			t.Errorf("the shared runtime process ended %d time(s), want 1 (on the last member)", got)
		}
		terminating := map[string]int{}
		for _, ev := range evs {
			if ev.Type == "AdapterTerminating" {
				terminating[ev.SessionID]++
			}
		}
		for _, id := range []string{"sess-a", "sess-b"} {
			if terminating[id] != 1 {
				t.Errorf("AdapterTerminating envelopes naming %s = %d, want 1; events = %+v",
					id, terminating[id], evs)
			}
		}
		if len(terminating) != 2 {
			t.Errorf("AdapterTerminating envelopes by session = %v, want one per terminated session", terminating)
		}
	})

	t.Run("a concurrent Shutdown and the deregistration pass exclude each other", func(t *testing.T) {
		// The §5.2 scrub report is withheld without a cached pod identity,
		// so the case would not see a scrub at all without this.
		t.Setenv("POD_NAME", "pod-hold-race")
		for attempt := 0; attempt < raceAttempts; attempt++ {
			rt := newHoldSharedRuntime()
			hold := 60 * time.Millisecond
			s := holdRacePod(t, rt, hold)
			meter := adapter.NewSessionUsageMeter(time.Now)
			meter.Add("sess-a", 5, 1)
			meter.Add("sess-b", 7, 2)
			s.Usage = meter
			scrub := newHoldScrubReporter()
			s.SessionScrubReporter = scrub
			startHoldSession(t, s, "sess-a")
			startHoldSession(t, s, "sess-b")

			arming := newHoldEventStream()
			arming.attach(t, s)
			fires := time.Now().Add(hold)
			arming.drop()
			// The allowlist admits a new coordinator re-attaching the
			// stream without fencing, which is how the loop's emissions
			// become observable.
			observer := newHoldEventStream()
			observer.attach(t, s)

			// Every attempt issues the terminal request for each member at
			// the instant the timer fires, so one stimulus reaches both
			// interleavings of the two locked deregistration steps.
			rs := newRaceStart(2)
			var wg sync.WaitGroup
			for _, id := range []string{"sess-a", "sess-b"} {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					for time.Now().Before(fires) {
					}
					rs.arrive()
					resp, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
						SessionId: &adapterv1.SessionId{Value: id},
						Reason:    "session_complete",
					})
					if err != nil {
						t.Errorf("terminal Shutdown(%s) = %v, want the idempotent no-op", id, err)
						return
					}
					if !resp.GetExitedCleanly() {
						t.Errorf("terminal Shutdown(%s) reported an unclean exit", id)
					}
				}(id)
			}
			rs.release(t)
			wg.Wait()

			awaitClosed(t, rt, "sess-a", "sess-b")
			evs := observer.settle(t, 50*time.Millisecond)

			usage := map[string]int{}
			for _, ev := range evs {
				if ev.Type == "FINAL_USAGE_REPORT" {
					usage[ev.SessionID]++
				}
			}
			for _, id := range []string{"sess-a", "sess-b"} {
				if usage[id] != 1 {
					t.Fatalf("attempt %d: FINAL_USAGE_REPORTs for %s = %d, want exactly one across the "+
						"termination loop and the terminal request; §8.3 budget_return consumes each one",
						attempt, id, usage[id])
				}
				if got := scrub.counts()[id]; got > 1 {
					t.Fatalf("attempt %d: ReportSessionScrub for %s = %d, want at most one; "+
						"the termination loop performs no scrub", attempt, id, got)
				}
			}
			if got := rt.processEnds(); got != 1 {
				t.Fatalf("attempt %d: the shared runtime process ended %d time(s), want 1", attempt, got)
			}
		}
	})
}

// spec: 10.1 (coordinator-loss hold), 10.1.4 (the hold timeout), 5.2
// (per-session teardown), 4.11 (the started flag records the claim)
//
// The started flag is written by the merged claim rather than by the
// start, so a member whose claim returned and whose Runtime.Start has not
// run is in the terminated set. Closing it ends nothing, because the
// process never held it, and a start landing afterwards leaves an agent
// process with no coordinator and no registry entry naming it. Pass 2
// cannot order around that interleaving: no adapter-side state
// distinguishes a member whose start has run from one whose has not.
//
// diagnosis: this case measures a recorded limit rather than a guarantee.
// A failure means the window has moved. A change that closes it edits this
// case in the same commit; a change that widens it is caught here rather
// than in production.
func TestCoordinatorHoldTerminationRacesALateRuntimeStart_spec_10_1(t *testing.T) {
	rt := newHoldSharedRuntime()
	s := holdRacePod(t, rt, 40*time.Millisecond)

	// sess-a claims its slot and parks inside Runtime.Start, so the
	// registry holds it as started while the process never receives it.
	lateStart := rt.gateStart("sess-a")
	started := make(chan error, 1)
	go func() {
		_, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-a"},
			Runtime:   "echo",
		})
		started <- err
	}()
	awaitStartedEntry(t, s, "sess-a")
	startHoldSession(t, s, "sess-b")

	// The co-tenant's close is held until the parked start has completed,
	// which puts the late start between the two members' closes.
	cotenantClose := rt.gateClose("sess-b")
	rt.onClosed = func(sessionID string) {
		if sessionID != "sess-a" {
			return
		}
		close(lateStart)
		if err := <-started; err != nil {
			t.Errorf("the late StartSession failed: %v", err)
		}
		close(cotenantClose)
	}

	arming := newHoldEventStream()
	arming.attach(t, s)
	arming.drop()

	awaitClosed(t, rt, "sess-a", "sess-b")

	if got := rt.resident(); len(got) != 1 || got[0] != "sess-a" {
		t.Errorf("the shared runtime process holds %v after the termination, want [sess-a]: "+
			"the late start is the window §10.1 records as open", got)
	}
	for _, id := range []string{"sess-a", "sess-b"} {
		if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: id},
		}); err != nil {
			t.Errorf("Shutdown(%s) after the termination = %v, want the idempotent no-op", id, err)
		}
	}
}

// awaitStartedEntry blocks until the session's claim has landed in the
// slot registry. The claim's own critical section records the session that
// took the once-per-pod intra-pod MCP start, which the adapter exposes, so
// the case reads the claim rather than the start that follows it.
func awaitStartedEntry(t *testing.T, s *adapter.Server, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if arming, _ := s.PodMCPArming(); arming == sessionID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the claim for %s never landed", sessionID)
		}
		time.Sleep(time.Millisecond)
	}
}
