// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// readWithin reads one CH-RUNTIMEOPS frame if the runtime end receives one
// inside the given window, and reports whether it did. A case that asserts a
// frame was withheld needs a bounded read: fakeRuntime.read fails the case on
// a timeout, which is the outcome the withhold arm expects.
func (fr *fakeRuntime) readWithin(d time.Duration) (lifecycleFrame, bool) {
	fr.t.Helper()
	_ = fr.conn.SetReadDeadline(time.Now().Add(d))
	frame, err := readLifecycleFrame(fr.r)
	_ = fr.conn.SetReadDeadline(time.Time{})
	return frame, err == nil
}

// probeRuntime is a RuntimeProcess that runs a probe on entry to Close,
// before it records the session it closed. The merged shutdown handler runs
// the drain, the close, and the per-slot tree removal in that order, so a
// probe taken here observes the pod one step after the §15.4.2 grace window
// opens. The window's own opening is observed from the runtime side instead,
// by a reader that probes as soon as the terminate frame arrives.
type probeRuntime struct {
	onClose func(sessionID string)
	closed  []string
}

func (r *probeRuntime) Start(context.Context, string) error { return nil }
func (r *probeRuntime) WriteEnvelope(string, []byte) error  { return nil }

func (r *probeRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *probeRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *probeRuntime) Close(_ context.Context, sessionID string) error {
	if r.onClose != nil {
		r.onClose(sessionID)
	}
	r.closed = append(r.closed, sessionID)
	return nil
}

// slotPod builds an adapter with the four per-slot roots resolved under one
// temporary base, a cached pod id, and a recording scrub reporter, so a case
// can drive the merged shutdown handler and read both the on-disk tree and
// the cleanup-outcome report. spec: §6.4.
func slotPod(t *testing.T) (*Server, *recordingSessionScrubReporter) {
	t.Helper()
	base := t.TempDir()
	s := New("slot-shutdown-test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.podID = "pod-slot-shutdown"
	reporter := &recordingSessionScrubReporter{}
	s.SessionScrubReporter = reporter
	return s, reporter
}

// slotTreeProbe reports whether the session's per-slot cwd and its per-slot
// credential file are both still on disk. spec: §6.4; §6.1.
func slotTreeProbe(t *testing.T, s *Server, sessionID string) (current, credentials bool) {
	t.Helper()
	paths, err := s.resolveSlotPaths(sessionID)
	if err != nil {
		t.Fatalf("resolve slot paths for %s: %v", sessionID, err)
	}
	_, curErr := os.Stat(paths.Current)
	_, credErr := os.Stat(paths.CredentialsFile)
	return curErr == nil, credErr == nil
}

// spec: §15.4.2 (the drain precedes the hard close), §6.4 (the per-slot
// tree is removed on slot cleanup), §6.1 (the per-slot credential file)
//
// The merged shutdown handler removes the ending session's per-slot tree
// after Runtime.Close has returned. The removal is the second of the two
// release steps for that reason: the agent process is still reading its
// §6.1 credential file and its §6.4 cwd for the whole §15.4.2 grace window
// the drain opens, and a removal folded back into the locked deregistration
// step deletes both out from under it. Both call orders compile at every
// caller, so nothing but this ordering assertion holds the split.
func TestShutdownRemovesTheSlotTreeAfterTheRuntimeClose_spec_15_4_2(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()

	s, reporter := slotPod(t)
	s.Lifecycle = lc

	type probe struct {
		current, credentials bool
	}
	var atDrain, atClose, atReport probe
	// The drain observation is taken from the runtime side, at the moment
	// the peer reads the terminate frame, which is where the §15.4.2 grace
	// window opens. Close's own observation is taken independently one step
	// later, so the two assertions below read two samples rather than one.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		got, ok := fr.readWithin(4 * time.Second)
		if !ok {
			t.Errorf("no CH-RUNTIMEOPS frame reached the runtime end before Runtime.Close")
			return
		}
		if got.Type != "terminate" {
			t.Errorf("frame read at the drain = %q, want terminate", got.Type)
		}
		atDrain.current, atDrain.credentials = slotTreeProbe(t, s, "alice")
	}()
	rt := &probeRuntime{onClose: func(string) {
		<-drained
		atClose.current, atClose.credentials = slotTreeProbe(t, s, "alice")
	}}
	s.Runtime = rt
	reporterProbe := func() { atReport.current, atReport.credentials = slotTreeProbe(t, s, "alice") }
	reporter.beforeReport = reporterProbe

	// §4.7.1 rule 6: the credential assignment precedes the start.
	assignOne(t, s, "alice", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", directPayload, time.Time{}))
	if err := s.claimSessionForTest("alice"); err != nil {
		t.Fatalf("claim alice: %v", err)
	}

	if cur, cred := slotTreeProbe(t, s, "alice"); !cur || !cred {
		t.Fatalf("before shutdown: cwd present = %v, credential file present = %v, want both", cur, cred)
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "alice"},
		DeadlineMs:            4000,
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if !atDrain.current || !atDrain.credentials {
		t.Errorf("at the drain: cwd present = %v, credential file present = %v, want both; the per-slot "+
			"tree was removed inside the locked deregistration step, so the agent process lost its "+
			"credential file and its cwd inside the grace window the drain opened",
			atDrain.current, atDrain.credentials)
	}
	if !atClose.current || !atClose.credentials {
		t.Errorf("on entry to Runtime.Close: cwd present = %v, credential file present = %v, want both",
			atClose.current, atClose.credentials)
	}
	if atReport.current || atReport.credentials {
		t.Errorf("at ReportSessionScrub: cwd present = %v, credential file present = %v, want neither; "+
			"the cleanup outcome is reported after the removal it reports on",
			atReport.current, atReport.credentials)
	}
	if cur, cred := slotTreeProbe(t, s, "alice"); cur || cred {
		t.Errorf("after shutdown: cwd present = %v, credential file present = %v, want neither", cur, cred)
	}
	if len(reporter.snapshot()) != 1 {
		t.Errorf("ReportSessionScrub calls = %d, want 1", len(reporter.snapshot()))
	}
}

// spec: §5.2 (the slot registry holds one entry per session on every pod),
// §15.4.2 (the pod-global drain signal names no session)
//
// The pair of teardowns on one two-slot pod pins both arms of the
// bound-entry quantity the drain is gated on. A session ending on a
// co-tenanted pod sends no drain: the signal is pod-global and terminates
// the shared runtime process, so sending it while a co-tenant is still bound
// tears down a runtime that is still serving. The co-tenant keeps its slot
// entry, its message path, and its runtime, and the ending session's final
// usage report and cleanup outcome name that session alone. The second
// session's shutdown then sends the drain before closing that session's
// runtime, because deregistering it leaves no bound entry.
func TestShutdownWithholdsDrainWhileACoTenantIsBound_spec_5_2(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()

	s, reporter := slotPod(t)
	s.Lifecycle = lc
	rt := &probeRuntime{}
	s.Runtime = rt
	s.Usage = NewSessionUsageMeter(time.Now)
	stream, cancel := attachControlStream(t, s)
	defer cancel()

	for _, id := range []string{"alice", "bob"} {
		if err := s.claimSessionForTest(id); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "alice"},
	}); err != nil {
		t.Fatalf("Shutdown alice: %v", err)
	}

	if frame, ok := fr.readWithin(750 * time.Millisecond); ok {
		t.Errorf("CH-RUNTIMEOPS carried a %q frame while a co-tenant was still bound; the drain "+
			"terminates the shared runtime process the co-tenant is being served by", frame.Type)
	}

	s.mu.Lock()
	_, aliceHeld := s.slots["alice"]
	bob, bobHeld := s.slots["bob"]
	bobBound := bobHeld && bob.sessionID == "bob"
	s.mu.Unlock()
	if aliceHeld {
		t.Error("the ending session still holds a slot entry after its shutdown")
	}
	if !bobBound {
		t.Fatal("the co-tenant's slot entry did not survive the ending session's teardown")
	}
	if cur, _ := slotTreeProbe(t, s, "bob"); !cur {
		t.Error("the co-tenant's per-slot cwd was removed by the ending session's teardown")
	}
	if _, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "bob"},
		EnvelopeJson: []byte(`{"type":"prompt"}`),
	}); err != nil {
		t.Errorf("the co-tenant's message path did not survive the ending session's teardown: %v", err)
	}
	if len(rt.closed) != 1 || rt.closed[0] != "alice" {
		t.Errorf("runtime closed = %v, want [alice] alone", rt.closed)
	}

	ev := recvEvent(t, stream)
	if ev.Type != eventFinalUsageReport || ev.SessionID != "alice" {
		t.Errorf("control event = %+v, want FINAL_USAGE_REPORT for alice", ev)
	}
	reports := reporter.snapshot()
	if len(reports) != 1 || reports[0].sessionID != "alice" {
		t.Errorf("session scrub reports = %+v, want one naming alice", reports)
	}

	// The second teardown on the same pod deregisters the last bound entry,
	// so the drain the first teardown withheld goes out, and it goes out
	// before the runtime the co-tenant was being served by is closed. The
	// bound-entry answer is therefore read from the registry the
	// deregistration left behind rather than from how many entries the pod
	// has ever held.
	var (
		drainFrame lifecycleFrame
		drainRead  bool
	)
	drained := make(chan struct{})
	go func() {
		drainFrame, drainRead = fr.readWithin(30 * time.Second)
		close(drained)
	}()
	closedBeforeDrain := false
	rt.onClose = func(sessionID string) {
		if sessionID != "bob" {
			return
		}
		select {
		case <-drained:
		case <-time.After(5 * time.Second):
			closedBeforeDrain = true
		}
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "bob"},
		DeadlineMs:            3500,
		Reason:                "session_complete",
	}); err != nil {
		t.Fatalf("Shutdown bob: %v", err)
	}

	<-drained
	if !drainRead {
		t.Fatal("no CH-RUNTIMEOPS drain signal reached the runtime once the last bound entry was " +
			"deregistered; the pod is serving no session and its runtime is closed without a graceful drain")
	}
	if drainFrame.Type != "terminate" || drainFrame.DeadlineMs != 3500 || drainFrame.Reason != "session_complete" {
		t.Errorf("drain frame = %+v, want terminate with deadlineMs 3500 and reason session_complete", drainFrame)
	}
	if closedBeforeDrain {
		t.Error("the co-tenant's runtime was closed before the drain signal reached it; the grace " +
			"window the drain opens is the window the close ends")
	}
	if len(rt.closed) != 2 || rt.closed[0] != "alice" || rt.closed[1] != "bob" {
		t.Errorf("runtime closed = %v, want [alice bob]", rt.closed)
	}
}

// spec: §5.2 (the slot registry distinguishes a bound entry from a
// registered one), §15.4.2 (the drain goes out once no bound entry remains)
//
// A session ending beside another session's workspace preparation sends the
// drain. The preparation RPCs register a slot entry without binding it, so a
// gate that counts registry entries rather than bound ones withholds the
// §15.4.2 drain behind an entry no session is being served on, and the
// shared runtime is killed without a graceful drain.
func TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()

	s, _ := slotPod(t)
	s.Lifecycle = lc
	s.Runtime = &probeRuntime{}

	if err := s.claimSessionForTest("alice"); err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	if _, err := s.ensureSlotPaths("bob", slotResolve{allowCreate: true}); err != nil {
		t.Fatalf("register bob's unbound slot: %v", err)
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "alice"},
		DeadlineMs:            2500,
		Reason:                "session_complete",
	}); err != nil {
		t.Fatalf("Shutdown alice: %v", err)
	}

	frame, ok := fr.readWithin(30 * time.Second)
	if !ok {
		t.Fatal("no CH-RUNTIMEOPS drain signal reached the runtime; a registered-but-unbound entry " +
			"must not withhold the drain")
	}
	if frame.Type != "terminate" || frame.DeadlineMs != 2500 {
		t.Errorf("drain frame = %+v, want terminate with deadlineMs 2500", frame)
	}
}

// spec: §4.9 (a direct-mode lease's expiry timer is cancelled when the
// session it was armed for ends), §5.2 (a sibling slot is undisturbed)
//
// The merged shutdown handler cancels every expiry timer armed on the
// ending session's entry, and cancels no other session's. A timer left armed
// fires AUTH_EXPIRED against a session that has already ended; a
// cancellation taken over the whole registry stops a live co-tenant's timer
// and its credentials then outlive their lease.
func TestShutdownCancelsTheEndingSessionsExpiryTimers_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	s.SessionsRoot = t.TempDir()
	s.ArtifactsRoot = t.TempDir()
	s.podID = "pod-expiry"
	s.Runtime = &probeRuntime{}
	stream, cancel := attachControlStream(t, s)
	defer cancel()

	// §4.7.1 rule 6: each credential assignment precedes its start.
	assignOne(t, s, "alice", "anthropic_direct",
		expiryLease("l-alice", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	aliceTimer := clk.last()
	assignOne(t, s, "bob", "anthropic_direct",
		expiryLease("l-bob", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	bobTimer := clk.last()
	for _, id := range []string{"alice", "bob"} {
		if err := s.claimSessionForTest(id); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "alice"},
	}); err != nil {
		t.Fatalf("Shutdown alice: %v", err)
	}

	if !aliceTimer.isStopped() {
		t.Error("the shutdown left the ending session's expiry timer armed")
	}
	if n := len(sessionTimers(s, "alice")); n != 0 {
		t.Errorf("the ending session tracks %d expiry timers after its shutdown, want 0", n)
	}
	if bobTimer.isStopped() {
		t.Error("the shutdown stopped the co-tenant's expiry timer")
	}
	if _, armed := sessionTimers(s, "bob")["anthropic_direct"]; !armed {
		t.Error("the co-tenant no longer tracks its expiry timer after the co-tenant's shutdown")
	}

	// The deadline the ending session's lease carried passes on the
	// AfterFunc seam. Nothing may reach the control stream for it.
	aliceTimer.fire()
	bobTimer.fire()
	ev := recvEvent(t, stream)
	if ev.Type != eventAuthExpired || ev.LeaseID != "l-bob" {
		t.Errorf("control event = %+v, want AUTH_EXPIRED for the co-tenant's lease l-bob; an "+
			"AUTH_EXPIRED for the ended session means its timer outlived its slot entry", ev)
	}
	select {
	case extra := <-stream.sent:
		t.Errorf("a second control event followed the co-tenant's expiry: %s", extra.GetEnvelopeJson())
	case <-time.After(200 * time.Millisecond):
	}
}

// The cases below pin §4.7.1's Shutdown cascade, rules 10 through 15, and
// the two teardowns the removing arm performs under their own
// preconditions: the slot release for any entry removed, and the runtime
// teardown for a session that started.

// shutdownAttempt is the bind attempt token the cases below stamp through
// assignOne and name on a fenced Shutdown.
const shutdownAttempt = "attempt-a"

// fencedShutdown builds the fenced form of Shutdown, naming attempt.
func fencedShutdown(sessionID, attempt string) *adapterv1.ShutdownRequest {
	return &adapterv1.ShutdownRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		BindAttempt: attempt,
	}
}

// unconditionalShutdownReq builds the unconditional form of Shutdown.
func unconditionalShutdownReq(sessionID string) *adapterv1.ShutdownRequest {
	return &adapterv1.ShutdownRequest{
		SessionId:             &adapterv1.SessionId{Value: sessionID},
		UnconditionalTeardown: true,
	}
}

// credentialDirExists reports whether the slot's per-slot credential
// directory, /run/lenny/slots/{sessionId}, is on disk.
func credentialDirExists(t *testing.T, s *Server, sessionID string) bool {
	t.Helper()
	paths, err := s.resolveSlotPaths(sessionID)
	if err != nil {
		t.Fatalf("resolve slot paths for %s: %v", sessionID, err)
	}
	_, statErr := os.Stat(paths.CredentialsDir)
	return statErr == nil
}

// runtimeHolds reports whether the shared runtime process holds sessionID.
func runtimeHolds(s *Server, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeHoldsLocked(sessionID)
}

// bindUnstarted puts sessionID in the bound-but-unstarted state: the entry
// is created and stamped with shutdownAttempt by AssignCredentials, and no
// start has run.
func bindUnstarted(t *testing.T, s *Server, sessionID string) {
	t.Helper()
	assignOne(t, s, sessionID, "anthropic_direct",
		expiryLease("l-"+sessionID, "anthropic_direct", directPayload, time.Time{}))
}

// bindAndStart puts sessionID in the running state: bound under
// shutdownAttempt, claimed, and recorded as held by the shared runtime.
func bindAndStart(t *testing.T, s *Server, sessionID string) {
	t.Helper()
	bindUnstarted(t, s, sessionID)
	if err := s.claimSessionForTest(sessionID); err != nil {
		t.Fatalf("claim %s: %v", sessionID, err)
	}
}

// aliceUnstarted and aliceStarted seed alice in the bound-but-unstarted and
// the running states for the table-driven cases below.
func aliceUnstarted(t *testing.T, s *Server) { bindUnstarted(t, s, "alice") }

func aliceStarted(t *testing.T, s *Server) { bindAndStart(t, s, "alice") }

// laterBindRefusedByHold reports whether a bind naming sessionID is refused
// with the reclaim-hold sentinel.
func laterBindRefusedByHold(s *Server, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.ensureSlotStateLocked(sessionID, slotResolve{bindAttempt: "attempt-later", allowCreate: true})
	return isSlotReclaimInProgress(err)
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// Rule 10 admits a Shutdown carrying exactly one of the two teardown
// fields. A request carrying neither or both is refused InvalidArgument and
// performs nothing: it removes no entry and starts no whole-pod scrub even
// when it carries the recycle disposition, so no ReportPodScrub is filed.
func TestShutdownRequiresExactlyOneTeardownField_spec_4_7_1(t *testing.T) {
	refused := []struct {
		name string
		req  *adapterv1.ShutdownRequest
	}{
		{"neither field", &adapterv1.ShutdownRequest{}},
		{"both fields", &adapterv1.ShutdownRequest{BindAttempt: shutdownAttempt, UnconditionalTeardown: true}},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			s, reporter, _, done := recycleServer(t)
			startRecycleSession(t, s, "alice")
			tc.req.SessionId = &adapterv1.SessionId{Value: "alice"}
			tc.req.Recycle = &adapterv1.RecycleScrub{PodId: "pod-x"}
			_, err := s.Shutdown(context.Background(), tc.req)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("Shutdown with %s = %v, want InvalidArgument", tc.name, err)
			}
			if !hasEntry(s, "alice") {
				t.Error("a refused Shutdown removed the entry")
			}
			select {
			case <-done:
				t.Error("a refused Shutdown started the whole-pod scrub")
			case <-time.After(300 * time.Millisecond):
			}
			if n := len(reporter.snapshot()); n != 0 {
				t.Errorf("a refused Shutdown filed %d ReportPodScrub calls, want 0", n)
			}
		})
	}
	admitted := []struct {
		name string
		req  *adapterv1.ShutdownRequest
	}{
		{"bind_attempt alone", fencedShutdown("alice", shutdownAttempt)},
		{"unconditional_teardown alone", unconditionalShutdownReq("alice")},
	}
	for _, tc := range admitted {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := slotPod(t)
			s.Runtime = &probeRuntime{}
			bindUnstarted(t, s, "alice")
			resp, err := s.Shutdown(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Shutdown with %s: %v", tc.name, err)
			}
			if resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
				t.Errorf("slot_reclaim = %v, want RECLAIMED", resp.GetSlotReclaim())
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// shutdownReclaimOutcome carries rules 11 through 14, one arm each, in the
// order the cascade fixes.
func TestShutdownReclaimOutcomeDecidesEachRule_spec_4_7_1(t *testing.T) {
	untokened := &slotState{}
	stamped := &slotState{bindAttempt: "attempt-a"}
	cases := []struct {
		name          string
		cur           *slotState
		ok            bool
		unconditional bool
		attempt       string
		want          adapterv1.SlotReclaimOutcome
		remove        bool
		untokened     bool
	}{
		{"rule 11, no entry, unconditional form", nil, false, true, "", adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT, false, false},
		{"rule 11, no entry, fenced form", nil, false, false, "attempt-a", adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT, false, false},
		{"rule 12, unconditional against a stamped entry", stamped, true, true, "", adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true, false},
		{"rule 12, unconditional against an untokened entry", untokened, true, true, "", adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true, false},
		{"rule 13, the entry carries no token", untokened, true, false, "attempt-a", adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED, false, true},
		{"rule 13, the entry carries another token", stamped, true, false, "attempt-b", adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED, false, false},
		{"rule 14, the tokens match", stamped, true, false, "attempt-a", adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, remove, untok := shutdownReclaimOutcome(tc.cur, tc.ok, tc.unconditional, tc.attempt)
			if got != tc.want || remove != tc.remove || untok != tc.untokened {
				t.Errorf("shutdownReclaimOutcome = (%v, remove %v, untokened %v), want (%v, %v, %v)",
					got, remove, untok, tc.want, tc.remove, tc.untokened)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// The handler answers each arm of the cascade against a real registry. On
// every arm that removes nothing the entry, its per-slot cwd and its
// credentials.json all survive the call.
func TestShutdownTeardownCascadeAgainstTheRegistry_spec_4_7_1(t *testing.T) {
	cases := []struct {
		name    string
		seed    func(t *testing.T, s *Server)
		req     *adapterv1.ShutdownRequest
		want    adapterv1.SlotReclaimOutcome
		removed bool
	}{
		{
			"rule 11 unconditional form", func(*testing.T, *Server) {}, unconditionalShutdownReq("alice"),
			adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT, false,
		},
		{
			"rule 11 fenced form", func(*testing.T, *Server) {}, fencedShutdown("alice", shutdownAttempt),
			adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT, false,
		},
		{
			"rule 12", aliceUnstarted, unconditionalShutdownReq("alice"),
			adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true,
		},
		{
			"rule 13 untokened entry", func(t *testing.T, s *Server) {
				// A start creates an entry carrying no token.
				if err := s.claimSessionForTest("alice"); err != nil {
					t.Fatalf("claim alice: %v", err)
				}
			}, fencedShutdown("alice", shutdownAttempt),
			adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED, false,
		},
		{
			"rule 13 differing token", aliceUnstarted, fencedShutdown("alice", "attempt-b"),
			adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED, false,
		},
		{
			"rule 14", aliceUnstarted, fencedShutdown("alice", shutdownAttempt),
			adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := slotPod(t)
			s.Runtime = &probeRuntime{}
			tc.seed(t, s)
			seeded := hasEntry(s, "alice")
			var cur0, cred0 bool
			if seeded {
				cur0, cred0 = slotTreeProbe(t, s, "alice")
			}
			resp, err := s.Shutdown(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if resp.GetSlotReclaim() != tc.want {
				t.Errorf("slot_reclaim = %v, want %v", resp.GetSlotReclaim(), tc.want)
			}
			if got := seeded && !hasEntry(s, "alice"); got != tc.removed {
				t.Errorf("entry removed = %v, want %v", got, tc.removed)
			}
			if tc.removed || !seeded {
				return
			}
			cur, cred := slotTreeProbe(t, s, "alice")
			if cur != cur0 || cred != cred0 || !cur {
				t.Errorf("a Shutdown that removed nothing changed the tree: cwd %v→%v, credentials.json %v→%v",
					cur0, cur, cred0, cred)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §16.1 (metrics)
//
// A Shutdown naming a token against an entry carrying none answers
// superseded, removes nothing, and counts the untokened entry once.
func TestShutdownAgainstAnUntokenedEntryFiresItsCounter_spec_4_7_1(t *testing.T) {
	s, _ := slotPod(t)
	s.Runtime = &probeRuntime{}
	if err := s.claimSessionForTest("alice"); err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	before := testutil.ToFloat64(slotShutdownUntokenedEntry.WithLabelValues())
	resp, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt))
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED {
		t.Errorf("slot_reclaim = %v, want SUPERSEDED", resp.GetSlotReclaim())
	}
	if !hasEntry(s, "alice") {
		t.Error("a Shutdown against an untokened entry removed it")
	}
	if got := testutil.ToFloat64(slotShutdownUntokenedEntry.WithLabelValues()) - before; got != 1 {
		t.Errorf("lenny_slot_shutdown_untokened_entry_total rose by %v, want 1", got)
	}
	// A differing token against a stamped entry is superseded as well, and is
	// not an untokened entry.
	bindUnstarted(t, s, "bob")
	before = testutil.ToFloat64(slotShutdownUntokenedEntry.WithLabelValues())
	if _, err := s.Shutdown(context.Background(), fencedShutdown("bob", "attempt-b")); err != nil {
		t.Fatalf("Shutdown bob: %v", err)
	}
	if got := testutil.ToFloat64(slotShutdownUntokenedEntry.WithLabelValues()) - before; got != 0 {
		t.Errorf("a differing-token Shutdown moved the untokened counter by %v, want 0", got)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// The unconditional teardown every non-compensating caller sends removes a
// started session whatever token its entry carries, and runs its full
// teardown: the runtime close, the tree removal and the cleanup report.
func TestTheUnconditionalTeardownStaysUnconditional_spec_4_7_1(t *testing.T) {
	s, reporter := slotPod(t)
	rt := &probeRuntime{}
	s.Runtime = rt
	bindAndStart(t, s, "alice")
	resp, err := s.Shutdown(context.Background(), unconditionalShutdownReq("alice"))
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED || !resp.GetExitedCleanly() {
		t.Errorf("response = %+v, want RECLAIMED and a clean exit", resp)
	}
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("the unconditional teardown left the entry or its tree")
	}
	if len(rt.closed) != 1 || rt.closed[0] != "alice" {
		t.Errorf("runtime closed = %v, want [alice]", rt.closed)
	}
	if r := reporter.snapshot(); len(r) != 1 || r[0].outcome != gatewaycontrol.SessionScrubReleased {
		t.Errorf("session scrub reports = %+v, want one released", r)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §15.4.2
//
// A Shutdown of an entry a workspace RPC created and nobody bound removes
// the entry, its slot tree and its credential directory. It closes no
// runtime, sends no drain and files no cleanup report.
func TestShutdownOfARegisteredUnboundEntryRemovesItsTree_spec_4_7_1(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()
	s, reporter := slotPod(t)
	s.Lifecycle = lc
	rt := &probeRuntime{}
	s.Runtime = rt
	if _, err := s.ensureSlotPaths("alice", slotResolve{bindAttempt: shutdownAttempt, allowCreate: true}); err != nil {
		t.Fatalf("register alice: %v", err)
	}
	if !credentialDirExists(t, s, "alice") {
		t.Fatal("the fixture did not create the slot's credential directory")
	}
	if _, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt)); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if hasEntry(s, "alice") {
		t.Error("the unbound entry survived its Shutdown")
	}
	if slotDirExists(s, "alice") || credentialDirExists(t, s, "alice") {
		t.Error("the unbound entry's slot tree or credential directory survived its Shutdown")
	}
	if len(rt.closed) != 0 {
		t.Errorf("runtime closed = %v for a session that never started", rt.closed)
	}
	if frame, ok := fr.readWithin(500 * time.Millisecond); ok {
		t.Errorf("CH-RUNTIMEOPS carried a %q frame for a session that never started", frame.Type)
	}
	if n := len(reporter.snapshot()); n != 0 {
		t.Errorf("session scrub reports = %d, want 0 for a session that never started", n)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §4.9; §15.4.2
//
// A Shutdown of a bound-but-unstarted entry removes the entry, the
// credential file and the tree, cancels the armed expiry timer, and runs no
// runtime teardown: no close, no cleanup report, no FINAL_USAGE_REPORT and
// no terminate frame on CH-RUNTIMEOPS. No other bound entry is on the pod,
// so a terminate withheld here is withheld by the started gate rather than
// by a co-tenant.
func TestShutdownOfABoundUnstartedEntryRunsNoRuntimeTeardown_spec_4_7_1(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s, reporter := slotPod(t)
	s.ExpiryAfterFunc = clk.After
	s.ExpiryNow = clk.Now
	s.Lifecycle = lc
	rt := &probeRuntime{}
	s.Runtime = rt
	meter := NewSessionUsageMeter(time.Now)
	meter.Add("alice", 5, 1)
	s.Usage = meter
	stream, cancel := attachControlStream(t, s)
	defer cancel()

	assignOne(t, s, "alice", "anthropic_direct",
		expiryLease("l-alice", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	timer := clk.last()
	if timer == nil {
		t.Fatal("the fixture armed no expiry timer")
	}
	resp, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt))
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !resp.GetExitedCleanly() || resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
		t.Errorf("response = %+v, want RECLAIMED and a clean exit", resp)
	}
	if hasEntry(s, "alice") || slotDirExists(s, "alice") || credentialDirExists(t, s, "alice") {
		t.Error("the entry, its tree or its credential directory survived the reclaim")
	}
	if !timer.isStopped() {
		t.Error("the reclaim left the unstarted session's expiry timer armed")
	}
	if len(rt.closed) != 0 {
		t.Errorf("runtime closed = %v for an unstarted session", rt.closed)
	}
	if n := len(reporter.snapshot()); n != 0 {
		t.Errorf("session scrub reports = %d, want 0 for an unstarted session", n)
	}
	if frame, ok := fr.readWithin(500 * time.Millisecond); ok {
		t.Errorf("CH-RUNTIMEOPS carried a %q frame for an unstarted session", frame.Type)
	}
	select {
	case extra := <-stream.sent:
		t.Errorf("a control event reached the gateway for an unstarted session: %s", extra.GetEnvelopeJson())
	case <-time.After(200 * time.Millisecond):
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §15.4.2
//
// A Shutdown of a started session the runtime holds runs today's full
// teardown in order: the drain signal, the close, the tree removal and one
// released cleanup report.
func TestShutdownOfAStartedSessionRunsTheFullTeardown_spec_4_7_1(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()
	s, reporter := slotPod(t)
	s.Lifecycle = lc
	var order []string
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		if frame, ok := fr.readWithin(10 * time.Second); ok && frame.Type == "terminate" {
			order = append(order, "drain")
		}
	}()
	rt := &probeRuntime{onClose: func(string) {
		<-drained
		order = append(order, "close")
	}}
	s.Runtime = rt
	s.removeSlotTreeFn = func(st *slotState) error {
		order = append(order, "tree")
		return removeSlotTree(st)
	}
	reporter.beforeReport = func() { order = append(order, "report") }
	bindAndStart(t, s, "alice")

	if _, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt)); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if strings.Join(order, ",") != "drain,close,tree,report" {
		t.Errorf("teardown order = %v, want [drain close tree report]", order)
	}
	if r := reporter.snapshot(); len(r) != 1 || r[0].sessionID != "alice" || r[0].outcome != gatewaycontrol.SessionScrubReleased {
		t.Errorf("session scrub reports = %+v, want one released for alice", r)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// A session whose claim ran and which the shared runtime does not yet hold
// is a start still in flight. Its reclaim runs the runtime teardown,
// removes the entry and the tree, and files no cleanup report, which is
// what holds the started gate and the live gate apart.
func TestShutdownOfAClaimedButUnrecordedStartTearsDownWithoutReporting_spec_4_7_1(t *testing.T) {
	s, reporter := slotPod(t)
	rt := &probeRuntime{}
	s.Runtime = rt
	bindUnstarted(t, s, "alice")
	if _, _, err := s.claimSessionSlot("alice", slotResolve{}, false, false); err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	if runtimeHolds(s, "alice") {
		t.Fatal("the fixture recorded the start; the case needs it in flight")
	}
	resp, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt))
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if len(rt.closed) != 1 || rt.closed[0] != "alice" {
		t.Errorf("runtime closed = %v, want [alice]; a start in flight is torn down", rt.closed)
	}
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("the entry or its tree survived the reclaim")
	}
	if n := len(reporter.snapshot()); n != 0 {
		t.Errorf("session scrub reports = %d, want 0 for a slot that never reached running", n)
	}
	if !resp.GetExitedCleanly() {
		t.Error("exited_cleanly = false for a reclaim whose close and removal both succeeded")
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// Interrupt of the last active session leaves SocketRuntimeProcess
// connected with an empty active set, the state in which any Close tears
// down the shared connection, the child and the listener. A Shutdown of a
// bound-but-unstarted entry on that pod runs no runtime close, so the
// listener stays bound and the runtime can still reach the adapter.
func TestShutdownOfAnUnstartedEntryLeavesTheSocketRuntimeIntact_spec_4_7_1(t *testing.T) {
	sp, err := NewSocketRuntimeProcess(shortSocketName(t, "rt.sock"))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	defer func() { _ = sp.listener.Close() }()
	dialed := make(chan net.Conn, 1)
	go func() {
		c, derr := net.Dial("unix", sp.SocketPath())
		if derr != nil {
			t.Errorf("dial runtime socket: %v", derr)
		}
		dialed <- c
	}()
	if err := sp.Start(context.Background(), "carol"); err != nil {
		t.Fatalf("Start carol: %v", err)
	}
	if c := <-dialed; c != nil {
		defer c.Close()
	}
	if err := sp.Interrupt(context.Background(), "carol", false); err != nil {
		t.Fatalf("Interrupt carol: %v", err)
	}

	s, _ := slotPod(t)
	s.Runtime = sp
	bindUnstarted(t, s, "alice")
	if _, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt)); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	sp.mu.Lock()
	connected := sp.connected
	sp.mu.Unlock()
	if !connected {
		t.Error("the Shutdown of an unstarted entry tore the shared runtime connection down")
	}
	c, err := net.Dial("unix", sp.SocketPath())
	if err != nil {
		t.Fatalf("the runtime socket stopped accepting after the Shutdown of an unstarted entry: %v", err)
	}
	_ = c.Close()
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// exited_cleanly implements the §5.2 clean-exit column. For a slot that did
// not reach running it carries the tree removal and the guard as well as the
// close, so a failed removal answers false. For a slot that reached running
// it answers on the close alone and the report stays released, while the
// hold is retained in both cases because the cleanup did not complete.
func TestShutdownExitedCleanlyFollowsTheCleanExitColumn_spec_4_7_1(t *testing.T) {
	t.Run("a reclaim the runtime never held with a failed tree removal", func(t *testing.T) {
		s, reporter := slotPod(t)
		s.Runtime = &probeRuntime{}
		bindUnstarted(t, s, "alice")
		s.removeSlotTreeFn = func(*slotState) error { return errors.New("injected removal failure") }
		resp, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt))
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED || resp.GetExitedCleanly() {
			t.Errorf("response = %+v, want RECLAIMED with exited_cleanly false", resp)
		}
		if !laterBindRefusedByHold(s, "alice") {
			t.Error("a later bind was admitted after a failed tree removal")
		}
		if n := len(reporter.snapshot()); n != 0 {
			t.Errorf("session scrub reports = %d, want 0", n)
		}
	})
	t.Run("a started session with a failed tree removal answers on the close", func(t *testing.T) {
		s, reporter := slotPod(t)
		s.Runtime = &probeRuntime{}
		bindAndStart(t, s, "alice")
		s.removeSlotTreeFn = func(*slotState) error { return errors.New("injected removal failure") }
		resp, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt))
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if !resp.GetExitedCleanly() {
			t.Error("exited_cleanly = false for a running slot whose close succeeded")
		}
		if !laterBindRefusedByHold(s, "alice") {
			t.Error("a later bind was admitted after a failed tree removal")
		}
		if r := reporter.snapshot(); len(r) != 1 || r[0].outcome != gatewaycontrol.SessionScrubReleased {
			t.Errorf("session scrub reports = %+v, want one released", r)
		}
	})
	clean := []struct {
		name string
		seed func(t *testing.T, s *Server)
		req  *adapterv1.ShutdownRequest
	}{
		{"an unstarted reclaim whose removal succeeds", aliceUnstarted, fencedShutdown("alice", shutdownAttempt)},
		{"a superseded answer", aliceUnstarted, fencedShutdown("alice", "attempt-b")},
		{"an absent answer", func(*testing.T, *Server) {}, fencedShutdown("alice", shutdownAttempt)},
	}
	for _, tc := range clean {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := slotPod(t)
			s.Runtime = &probeRuntime{}
			tc.seed(t, s)
			resp, err := s.Shutdown(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if !resp.GetExitedCleanly() {
				t.Errorf("exited_cleanly = false for %s", tc.name)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// A fenced Shutdown on an already-cancelled context while another section
// holds the slot's guard takes the on-expiry disposition: it removes the
// entry and its tree unguarded and keeps the reclaim hold after the holder
// releases. For a slot that did not reach running it answers exited_cleanly
// false; for one in the shared runtime it answers on the close alone and
// files a released report.
func TestAnExpiredGuardAcquisitionKeepsTheHoldAndFailsTheCleanExit_spec_5_2(t *testing.T) {
	cases := []struct {
		name        string
		seed        func(t *testing.T, s *Server)
		wantClean   bool
		wantReports int
	}{
		{"bound but unstarted", aliceUnstarted, false, 0},
		{"in the shared runtime", aliceStarted, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, reporter := slotPod(t)
			s.Runtime = &probeRuntime{}
			tc.seed(t, s)
			releaseHolder := holdGuard(t, s, "alice")
			respCh := make(chan *adapterv1.ShutdownResponse, 1)
			go func() {
				resp, err := s.Shutdown(cancelledContext(), fencedShutdown("alice", shutdownAttempt))
				if err != nil {
					t.Errorf("Shutdown: %v", err)
				}
				respCh <- resp
			}()
			var resp *adapterv1.ShutdownResponse
			select {
			case resp = <-respCh:
			case <-time.After(5 * time.Second):
				releaseHolder()
				t.Fatal("the Shutdown waited for the guard holder past its context")
			}
			if resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
				t.Errorf("slot_reclaim = %v, want RECLAIMED", resp.GetSlotReclaim())
			}
			if resp.GetExitedCleanly() != tc.wantClean {
				t.Errorf("exited_cleanly = %v, want %v", resp.GetExitedCleanly(), tc.wantClean)
			}
			if hasEntry(s, "alice") || slotDirExists(s, "alice") {
				t.Error("an expired acquisition abandoned the removal")
			}
			releaseHolder()
			if !laterBindRefusedByHold(s, "alice") {
				t.Error("a later bind was admitted after an unguarded removal")
			}
			r := reporter.snapshot()
			if len(r) != tc.wantReports {
				t.Fatalf("session scrub reports = %+v, want %d", r, tc.wantReports)
			}
			if tc.wantReports == 1 && r[0].outcome != gatewaycontrol.SessionScrubReleased {
				t.Errorf("report outcome = %v, want released", r[0].outcome)
			}
		})
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// Clause three, the whole-pod recycle scrub, runs on every outcome a
// Shutdown answers. The absent arm is what Binder.ReleaseSlot reaches after
// its separate teardown removed the last slot; the removing arm is what a
// recycling pool's only teardown reaches.
func TestShutdownRunsTheRecycleScrubOnEveryOutcome_spec_5_2(t *testing.T) {
	cases := []struct {
		name    string
		seed    bool
		want    adapterv1.SlotReclaimOutcome
		removed bool
	}{
		{"absent arm", false, adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT, false},
		{"removing arm", true, adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, reporter, _, done := recycleServer(t)
			if tc.seed {
				startRecycleSession(t, s, "alice")
			}
			req := unconditionalShutdownReq("alice")
			req.Recycle = &adapterv1.RecycleScrub{PodId: "pod-x"}
			resp, err := s.Shutdown(context.Background(), req)
			if err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if resp.GetSlotReclaim() != tc.want {
				t.Errorf("slot_reclaim = %v, want %v", resp.GetSlotReclaim(), tc.want)
			}
			if tc.removed && hasEntry(s, "alice") {
				t.Error("the removing arm left the entry")
			}
			waitScrubDone(t, done)
			if n := len(reporter.snapshot()); n != 1 {
				t.Errorf("ReportPodScrub calls = %d, want 1", n)
			}
		})
	}
}

// waitForGuardWaiter blocks until some goroutine is parked acquiring a
// contended slot guard, so a case can act while a removing Shutdown waits
// between its two decisions.
func waitForGuardWaiter(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		n := runtime.Stack(buf, true)
		if strings.Contains(string(buf[:n]), "acquireSlotGuardChan") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no goroutine parked on the slot guard")
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// A removing Shutdown releases s.mu while it acquires the slot guard, so
// the registry can change between its two decisions. Here the guard holder
// removes the compensating attempt's entry and a successor attempt creates
// a fresh one under the same identifier before the guard is released. The
// Shutdown decides again under the guard, answers superseded and removes
// nothing, so the successor's entry, cwd and credential file survive. A
// handler that acted on its first decision would delete them.
func TestTheRemovingShutdownDecidesAgainUnderTheGuard_spec_4_7_1(t *testing.T) {
	s, _ := slotPod(t)
	s.Runtime = &probeRuntime{}
	bindUnstarted(t, s, "alice")
	releaseHolder := holdGuard(t, s, "alice")

	respCh := make(chan *adapterv1.ShutdownResponse, 1)
	go func() {
		resp, err := s.Shutdown(context.Background(), fencedShutdown("alice", shutdownAttempt))
		if err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		respCh <- resp
	}()
	waitForGuardWaiter(t)

	// Still under the guard: the holder's own failure path removes the entry,
	// and a successor attempt re-creates it.
	s.releaseSessionSlotUnderGuard("alice", true)
	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		BindAttempt: "attempt-b",
		SessionId:   &adapterv1.SessionId{Value: "alice"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic_direct": expiryLease("l-b", "anthropic_direct", directPayload, time.Time{}),
		},
	}); err != nil {
		t.Fatalf("successor AssignCredentials: %v", err)
	}
	releaseHolder()

	var resp *adapterv1.ShutdownResponse
	select {
	case resp = <-respCh:
	case <-time.After(10 * time.Second):
		t.Fatal("the Shutdown did not return once the guard was released")
	}
	if resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED {
		t.Errorf("slot_reclaim = %v, want SUPERSEDED", resp.GetSlotReclaim())
	}
	if st := s.slotStateForSession("alice"); st == nil || st.bindAttempt != "attempt-b" {
		t.Fatalf("successor entry = %+v, want the successor's entry intact", st)
	}
	if cur, cred := slotTreeProbe(t, s, "alice"); !cur || !cred {
		t.Errorf("successor cwd present = %v, credentials.json present = %v, want both", cur, cred)
	}
}
