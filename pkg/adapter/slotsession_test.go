// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
// probe taken here observes the pod exactly as the agent process inside the
// §15.4.2 grace window observes it.
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
	rt := &probeRuntime{onClose: func(string) {
		// The frame is on the socket before Close runs, so reading it here
		// is the moment the runtime end learns the drain has started.
		if got := fr.read(); got.Type != "terminate" {
			t.Errorf("frame read on entry to Runtime.Close = %q, want terminate", got.Type)
		}
		atDrain.current, atDrain.credentials = slotTreeProbe(t, s, "alice")
		atClose = atDrain
	}}
	s.Runtime = rt
	reporterProbe := func() { atReport.current, atReport.credentials = slotTreeProbe(t, s, "alice") }
	reporter.beforeReport = reporterProbe

	if err := s.claimSessionForTest("alice"); err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	assignOne(t, s, "alice", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", directPayload, time.Time{}))

	if cur, cred := slotTreeProbe(t, s, "alice"); !cur || !cred {
		t.Fatalf("before shutdown: cwd present = %v, credential file present = %v, want both", cur, cred)
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "alice"},
		DeadlineMs: 4000,
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
// A session ending on a co-tenanted pod sends no drain. The signal is
// pod-global and terminates the shared runtime process, so sending it while
// a co-tenant is still bound tears down a runtime that is still serving. The
// co-tenant keeps its slot entry, its message path, and its runtime, and the
// ending session's final usage report and cleanup outcome name that session
// alone.
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
		SessionId: &adapterv1.SessionId{Value: "alice"},
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
	if _, err := s.ensureSlotPaths("bob"); err != nil {
		t.Fatalf("register bob's unbound slot: %v", err)
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "alice"},
		DeadlineMs: 2500,
		Reason:     "session_complete",
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

	for _, id := range []string{"alice", "bob"} {
		if err := s.claimSessionForTest(id); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}
	assignOne(t, s, "alice", "anthropic_direct",
		expiryLease("l-alice", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	aliceTimer := clk.last()
	assignOne(t, s, "bob", "anthropic_direct",
		expiryLease("l-bob", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	bobTimer := clk.last()

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "alice"},
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
