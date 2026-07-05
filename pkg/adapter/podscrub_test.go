// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/adapter/scrub"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakePodScrubOps is a scrub.Ops double that records the host operations the
// whole-pod scrub performs and can be programmed to fail a step, so the
// adapter recycle driver is testable without running kill -9 -1 or touching
// the real filesystem. It mirrors the scrub package's own fakeOps but lives in
// the adapter package's internal test scope.
type fakePodScrubOps struct {
	mu       sync.Mutex
	killErr  error
	dirty    map[string]bool // paths PathState reports non-empty
	killed   bool
	removed  []string
	cleared  []string
	verified bool
}

func newFakePodScrubOps() *fakePodScrubOps {
	return &fakePodScrubOps{dirty: map[string]bool{}}
}

func (f *fakePodScrubOps) KillUserProcesses(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = true
	return f.killErr
}

func (f *fakePodScrubOps) PurgeIPCShm(context.Context) error { return nil }

func (f *fakePodScrubOps) RemoveAll(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, path)
	delete(f.dirty, path)
	return nil
}

func (f *fakePodScrubOps) ClearContents(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, dir)
	delete(f.dirty, dir)
	return nil
}

func (f *fakePodScrubOps) PathState(path string) (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verified = true
	if f.dirty[path] {
		return true, false, nil
	}
	return false, true, nil
}

// recycleRuntime is a minimal mutex-guarded RuntimeProcess double for the
// recycle tests. It records the sessions Close was called for so a test can
// assert the ending session's runtime was torn down. The guard keeps it
// -race clean when the async scrub goroutine and the test read concurrently.
type recycleRuntime struct {
	mu     sync.Mutex
	closed []string
}

func (r *recycleRuntime) Start(context.Context, string) error           { return nil }
func (r *recycleRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *recycleRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *recycleRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *recycleRuntime) Close(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = append(r.closed, sessionID)
	return nil
}

func (r *recycleRuntime) closedSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.closed))
	copy(out, r.closed)
	return out
}

// recordingPodScrubReporter is a PodScrubReporter double capturing every
// ReportPodScrub call so a test can assert the reported pod id, outcome, and
// whether a report was emitted at all (the withhold path emits none).
type recordingPodScrubReporter struct {
	mu      sync.Mutex
	reports []podScrubReport
	err     error
}

type podScrubReport struct {
	podID   string
	outcome gatewaycontrol.PodScrubOutcome
	detail  string
}

func (r *recordingPodScrubReporter) ReportPodScrub(_ context.Context, podID string, outcome gatewaycontrol.PodScrubOutcome, detail string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, podScrubReport{podID: podID, outcome: outcome, detail: detail})
	return r.err
}

func (r *recordingPodScrubReporter) snapshot() []podScrubReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]podScrubReport, len(r.reports))
	copy(out, r.reports)
	return out
}

// recycleServer builds a Server wired for the recycle-scrub driver: a fake
// runtime, fake scrub ops, a recording reporter, and a scrubDone seam that
// closes done once the async scrub goroutine returns. It returns the server,
// the reporter, the ops, and the done channel.
func recycleServer(t *testing.T) (*Server, *recordingPodScrubReporter, *fakePodScrubOps, chan struct{}) {
	t.Helper()
	rt := &recycleRuntime{}
	reporter := &recordingPodScrubReporter{}
	ops := newFakePodScrubOps()
	done := make(chan struct{})
	s := New("test")
	s.WorkspaceRoot = t.TempDir()
	s.Runtime = rt
	s.PodScrubReporter = reporter
	s.ScrubOps = ops
	s.scrubDone = func() { close(done) }
	return s, reporter, ops, done
}

func startRecycleSession(t *testing.T, s *Server, sessionID string) {
	t.Helper()
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
}

// waitScrubDone blocks until the async scrub goroutine finishes or the test
// deadline elapses.
func waitScrubDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recycle scrub did not finish within 5s")
	}
}

// TestShutdownRecycleRunsWholePodScrubAndReportsSuccess asserts the §5.2
// recycle boundary: a Shutdown carrying a standard-profile recycle disposition
// closes the ending session's runtime, keeps the pod alive, runs the whole-pod
// scrub, and emits exactly one PodScrubSucceeded report carrying the message's
// pod_id (the gateway timer key).
//
// diagnosis: a failure means the adapter recycle boundary stopped triggering
// the whole-pod scrub, reported the wrong pod id, or emitted the wrong outcome.
// spec: 5.2 (whole-pod scrub), 4.7 (reportpodscrub)
func TestShutdownRecycleRunsWholePodScrubAndReportsSuccess_spec_5_2(t *testing.T) {
	s, reporter, ops, done := recycleServer(t)
	rt := s.Runtime.(*recycleRuntime)
	startRecycleSession(t, s, "sess-1")

	resp, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:        "pod-abc",
			ScrubProfile: "standard",
		},
	})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !resp.ExitedCleanly {
		t.Error("recycle Shutdown reported an unclean exit for a healthy runtime")
	}
	// The ending session's runtime is closed.
	if closed := rt.closedSnapshot(); len(closed) != 1 || closed[0] != "sess-1" {
		t.Errorf("runtime closed = %v, want [sess-1]", closed)
	}
	waitScrubDone(t, done)

	if !ops.killed || !ops.verified {
		t.Errorf("whole-pod scrub did not run: killed=%v verified=%v", ops.killed, ops.verified)
	}
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportPodScrub calls = %d, want exactly 1", len(reports))
	}
	if reports[0].podID != "pod-abc" {
		t.Errorf("reported pod id = %q, want the message pod_id %q", reports[0].podID, "pod-abc")
	}
	if reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Errorf("outcome = %v, want PodScrubSucceeded", reports[0].outcome)
	}

	// The pod process stays alive across the recycle boundary: releaseSession
	// returned it to idle, so a replacement session can bind.
	startRecycleSession(t, s, "sess-2")
}

// TestShutdownRecycleEmptyCleanupCommandsStillReportsSuccess asserts a recycle
// with no cleanupCommands still runs scrub steps 1-6 and reports success.
//
// diagnosis: a failure means the empty-cleanup recycle path stopped scrubbing
// or stopped reporting.
// spec: 5.2 (whole-pod scrub), 4.7 (reportpodscrub)
func TestShutdownRecycleEmptyCleanupCommandsStillReportsSuccess_spec_5_2(t *testing.T) {
	s, reporter, ops, done := recycleServer(t)
	startRecycleSession(t, s, "sess-1")

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:        "pod-empty",
			ScrubProfile: "standard",
			// CleanupCommands intentionally empty.
		},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitScrubDone(t, done)

	if !ops.killed || !ops.verified {
		t.Errorf("scrub steps 1-6 did not run with empty cleanupCommands: killed=%v verified=%v", ops.killed, ops.verified)
	}
	reports := reporter.snapshot()
	if len(reports) != 1 || reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Fatalf("reports = %+v, want one PodScrubSucceeded", reports)
	}
}

// TestShutdownRecycleVMRestartReportsSuccessNoWithhold asserts the §5.2
// retire-and-reprovision reconciliation: a vm-restart pool now runs the same
// whole-pod scrub as every other profile and, on a clean scrub, emits exactly
// one PodScrubSucceeded report rather than withholding it. Withholding was the
// pre-reconciliation fail-closed stopgap for the impossible in-guest restart;
// the cross-tenant retire now happens at the gateway, which routes on the
// profile it already holds. A withheld report here would fail this test,
// pinning the corrected outcome against the pre-fix withhold-and-timeout code.
//
// diagnosis: a failure means a vm-restart recycle boundary withheld its scrub
// report again, forcing the emergent missing-report timeout instead of the
// deliberate gateway retire and leaving the report the gateway retire needs
// unsent.
// spec: 5.2 (whole-pod scrub, retire-and-reprovision), 4.7 (reportpodscrub)
func TestShutdownRecycleVMRestartReportsSuccessNoWithhold_spec_5_2(t *testing.T) {
	s, reporter, ops, done := recycleServer(t)
	startRecycleSession(t, s, "sess-1")

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:        "pod-vm",
			ScrubProfile: "vm-restart",
		},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitScrubDone(t, done)

	if !ops.killed || !ops.verified {
		t.Errorf("vm-restart whole-pod scrub did not run: killed=%v verified=%v", ops.killed, ops.verified)
	}
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportPodScrub calls = %d, want exactly 1 (no withhold); got %+v", len(reports), reports)
	}
	if reports[0].podID != "pod-vm" {
		t.Errorf("reported pod id = %q, want %q", reports[0].podID, "pod-vm")
	}
	if reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Errorf("outcome = %v, want PodScrubSucceeded", reports[0].outcome)
	}
}

// TestShutdownRecycleVMRestartReportsFailedOnDirtyScrub asserts a vm-restart
// pool whose whole-pod scrub fails a step reports PodScrubFailed rather than
// withholding. The gateway routes the failed report to the same retire path it
// routes a clean vm-restart report to; either way the adapter reports its
// binary outcome and the gateway decides the disposition.
//
// diagnosis: a failure means a failed vm-restart scrub stopped reporting
// PodScrubFailed, hiding a scrub failure the gateway retire audits.
// spec: 5.2 (whole-pod scrub outcome), 4.7 (reportpodscrub)
func TestShutdownRecycleVMRestartReportsFailedOnDirtyScrub_spec_5_2(t *testing.T) {
	s, reporter, ops, done := recycleServer(t)
	ops.killErr = errors.New("kill -9 -1 failed") // step 1 fails → Failed scrub
	startRecycleSession(t, s, "sess-1")

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:        "pod-vm-dirty",
			ScrubProfile: "vm-restart",
		},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitScrubDone(t, done)

	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportPodScrub calls = %d, want exactly 1 (no withhold on failure); got %+v", len(reports), reports)
	}
	if reports[0].outcome != gatewaycontrol.PodScrubFailed {
		t.Errorf("outcome = %v, want PodScrubFailed", reports[0].outcome)
	}
	if reports[0].detail == "" {
		t.Error("failed vm-restart scrub reported an empty detail, want a non-empty audit detail")
	}
}

// TestShutdownTerminatePathRunsNoScrub asserts the terminate path (recycle
// unset) closes the runtime and releases the pod without running the whole-pod
// scrub or emitting any ReportPodScrub, byte-identical to the pre-change
// behavior.
//
// diagnosis: a failure means the default terminate Shutdown started running
// the recycle scrub, changing the replace-the-pod semantics.
// spec: 5.2 (recycle disposition), 4.7 (shutdown default disposition)
func TestShutdownTerminatePathRunsNoScrub_spec_4_7(t *testing.T) {
	s, reporter, ops, _ := recycleServer(t)
	// No scrubDone fires on the terminate path, so leave the seam as-is; the
	// assertion below reads the reporter and ops synchronously after Shutdown.
	s.scrubDone = nil
	startRecycleSession(t, s, "sess-1")

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if ops.killed || ops.verified {
		t.Errorf("terminate Shutdown ran the whole-pod scrub: killed=%v verified=%v", ops.killed, ops.verified)
	}
	if reports := reporter.snapshot(); len(reports) != 0 {
		t.Errorf("terminate Shutdown emitted %d ReportPodScrub, want 0", len(reports))
	}
}

// TestScrubConfigThreadsParametersUniformlyAcrossProfiles asserts scrubConfig
// threads the cleanup, credential, and workspace parameters identically for
// every profile and leaves ShellMode false, with no per-profile restart
// divergence. After the §5.2 retire-and-reprovision reconciliation the adapter
// no longer sets a per-profile restart field: the fresh-guest requirement of a
// vm-restart pool is met by the gateway retiring the pod, so standard,
// in-place, and vm-restart yield byte-identical scrub configs.
//
// diagnosis: a failure means scrubConfig diverged the config on the scrub
// profile again, reintroducing the removed in-guest restart seam.
// spec: 5.2 (whole-pod scrub parameters, retire-and-reprovision)
func TestScrubConfigThreadsParametersUniformlyAcrossProfiles_spec_5_2(t *testing.T) {
	s := New("test")
	s.WorkspaceRoot = "/workspace/current"
	s.CredentialsDir = "/run/lenny"

	var first scrub.Config
	for i, profile := range []string{"standard", "in-place", "vm-restart"} {
		cfg := s.scrubConfig(&adapterv1.RecycleScrub{
			ScrubProfile:          profile,
			CleanupCommands:       []string{"echo cleanup"},
			CleanupTimeoutSeconds: 12,
		})
		if cfg.ShellMode {
			t.Errorf("profile %q: ShellMode = true, want false (no cleanup shell field)", profile)
		}
		if len(cfg.CleanupCommands) != 1 || cfg.CleanupCommands[0] != "echo cleanup" {
			t.Errorf("profile %q: CleanupCommands = %v, want [echo cleanup]", profile, cfg.CleanupCommands)
		}
		if cfg.CleanupTimeout != 12*time.Second {
			t.Errorf("profile %q: CleanupTimeout = %v, want 12s", profile, cfg.CleanupTimeout)
		}
		if cfg.CredentialFile != "/run/lenny/credentials.json" {
			t.Errorf("profile %q: CredentialFile = %q, want /run/lenny/credentials.json", profile, cfg.CredentialFile)
		}
		if cfg.WorkspaceDir != "/workspace/current" {
			t.Errorf("profile %q: WorkspaceDir = %q, want /workspace/current", profile, cfg.WorkspaceDir)
		}
		// Every profile yields an identical config: no per-profile restart
		// divergence survives the retire-and-reprovision reconciliation.
		if i == 0 {
			first = cfg
		} else if !reflect.DeepEqual(cfg, first) {
			t.Errorf("profile %q: config diverged from standard (%+v vs %+v)", profile, cfg, first)
		}
	}
}

// TestScrubOutcomeMapping asserts scrubOutcome maps a start error and a Failed
// report to PodScrubFailed with a non-empty detail, and a succeeded report to
// PodScrubSucceeded with an empty detail.
//
// spec: 5.2 (whole-pod scrub outcome)
func TestScrubOutcomeMapping_spec_5_2(t *testing.T) {
	// Start error → PodScrubFailed with the error text.
	outcome, detail := scrubOutcome(nil, errors.New("boom"))
	if outcome != gatewaycontrol.PodScrubFailed || detail == "" {
		t.Errorf("start error: outcome=%v detail=%q, want PodScrubFailed with non-empty detail", outcome, detail)
	}

	// Failed report with a step error → PodScrubFailed with a detail naming
	// the step.
	failed := &scrub.Report{
		Result: scrub.Failed,
		Steps: []scrub.StepRecord{
			{Step: scrub.StepKillProcesses, Err: errors.New("kill failed")},
		},
	}
	outcome, detail = scrubOutcome(failed, nil)
	if outcome != gatewaycontrol.PodScrubFailed {
		t.Errorf("failed report: outcome = %v, want PodScrubFailed", outcome)
	}
	if !strings.Contains(detail, string(scrub.StepKillProcesses)) {
		t.Errorf("failed report detail = %q, want it to name the failing step", detail)
	}

	// Failed report with only a dirty verification path → detail names the path.
	dirty := &scrub.Report{Result: scrub.Failed, VerifyDirty: []string{"/workspace/current"}}
	_, detail = scrubOutcome(dirty, nil)
	if !strings.Contains(detail, "/workspace/current") {
		t.Errorf("dirty report detail = %q, want it to name the dirty path", detail)
	}

	// Failed report with neither a step error nor a dirty path → the
	// defensive generic marker (the scrub does not produce this, but the
	// detail must never be empty on a failure).
	_, detail = scrubOutcome(&scrub.Report{Result: scrub.Failed}, nil)
	if detail == "" {
		t.Error("failed report with no step error: detail is empty, want a non-empty marker")
	}

	// Succeeded report → PodScrubSucceeded with empty detail.
	outcome, detail = scrubOutcome(&scrub.Report{Result: scrub.Succeeded}, nil)
	if outcome != gatewaycontrol.PodScrubSucceeded || detail != "" {
		t.Errorf("succeeded report: outcome=%v detail=%q, want PodScrubSucceeded with empty detail", outcome, detail)
	}
}

// TestStartPodScrubWithNilScrubOpsWithholdsReport pins the production
// fail-closed behavior when ScrubOps is not wired. Before the fix, a nil
// ScrubOps made scrub.Run return a nil-Ops error the driver mapped to
// PodScrubFailed and reported; under the default warn policy podscrub.Decide
// reuses the pod for the next session without any scrub having run (a
// between-session isolation regression). The driver now withholds the report
// on a nil ScrubOps, so the gateway missing-report timeout retires the pod.
// This test emits a report against the pre-fix code and none against the fix,
// so it fails on the regression.
//
// diagnosis: a failure means a production adapter with ScrubOps unwired would
// report PodScrubFailed and let the gateway reuse the pod under warn without a
// scrub — the exact fail-open isolation regression this fix closes.
// spec: 5.2 (whole-pod scrub, fail-closed on a wiring gap), 4.7 (reportpodscrub)
func TestStartPodScrubWithNilScrubOpsWithholdsReport_spec_5_2(t *testing.T) {
	reporter := &recordingPodScrubReporter{}
	done := make(chan struct{})
	s := New("test")
	s.WorkspaceRoot = t.TempDir()
	s.ScrubOps = nil // the pre-fix production state: never wired
	s.PodScrubReporter = reporter
	s.scrubDone = func() { close(done) }

	s.startPodScrub(&adapterv1.RecycleScrub{PodId: "pod-nilops", ScrubProfile: "standard"})
	waitScrubDone(t, done)

	if reports := reporter.snapshot(); len(reports) != 0 {
		t.Fatalf("startPodScrub emitted %d reports with nil ScrubOps, want 0 (report withheld fail-closed); got %+v",
			len(reports), reports)
	}
}

// TestNewServerLeavesScrubOpsNilForExplicitWiring documents that adapter.New
// does not default ScrubOps; the production binary (cmd/lenny-adapter) wires it
// explicitly, and a Server whose ScrubOps was never assigned is the wiring gap
// the fail-closed guard above protects against.
//
// spec: 5.2 (whole-pod scrub)
func TestNewServerLeavesScrubOpsNilForExplicitWiring_spec_5_2(t *testing.T) {
	if s := New("test"); s.ScrubOps != nil {
		t.Fatalf("New() set ScrubOps = %T, want nil (production wires it explicitly)", s.ScrubOps)
	}
}

// TestStartPodScrubToleratesReportError asserts a ReportPodScrub transport
// failure does not crash the driver: the scrub still runs and the goroutine
// returns. The gateway missing-report timeout is the backstop on a failed
// report, so the driver logs and moves on.
//
// spec: 5.2 (async scrub report), 4.7 (reportpodscrub)
func TestStartPodScrubToleratesReportError_spec_5_2(t *testing.T) {
	reporter := &recordingPodScrubReporter{err: errors.New("gateway unreachable")}
	done := make(chan struct{})
	s := New("test")
	s.WorkspaceRoot = t.TempDir()
	s.ScrubOps = newFakePodScrubOps()
	s.PodScrubReporter = reporter
	s.scrubDone = func() { close(done) }

	s.startPodScrub(&adapterv1.RecycleScrub{PodId: "pod-err", ScrubProfile: "standard"})
	waitScrubDone(t, done)

	// The driver attempted exactly one report and returned despite the error.
	if reports := reporter.snapshot(); len(reports) != 1 {
		t.Fatalf("ReportPodScrub attempts = %d, want 1 even when the report errors", len(reports))
	}
}

// TestStartPodScrubWithoutReporterDoesNotPanic asserts the dev path (no
// gateway link, PodScrubReporter nil) runs the scrub and returns without
// reporting or panicking. The gateway missing-report timeout is the backstop.
//
// spec: 5.2 (async scrub report)
func TestStartPodScrubWithoutReporterDoesNotPanic_spec_5_2(t *testing.T) {
	done := make(chan struct{})
	s := New("test")
	s.WorkspaceRoot = t.TempDir()
	s.ScrubOps = newFakePodScrubOps()
	s.PodScrubReporter = nil
	s.scrubDone = func() { close(done) }

	s.startPodScrub(&adapterv1.RecycleScrub{PodId: "pod-dev", ScrubProfile: "standard"})
	waitScrubDone(t, done)
}

// gatedScrubOps blocks the first host operation (KillUserProcesses) until
// release is closed, so a test can assert the recycle Shutdown response
// returns while the scrub is still mid-flight (asynchronous) and the report
// arrives only after the scrub is allowed to finish.
type gatedScrubOps struct {
	*fakePodScrubOps
	release chan struct{}
}

func (g *gatedScrubOps) KillUserProcesses(ctx context.Context) error {
	<-g.release
	return g.fakePodScrubOps.KillUserProcesses(ctx)
}

// TestShutdownRecycleScrubIsAsynchronous asserts the recycle Shutdown response
// returns before the scrub completes (the driver runs on a background
// goroutine), and that the report still arrives after the scrub is released,
// even though the gateway→adapter connection would be closed immediately after
// Shutdown returns (the report travels the separate GatewayControl link).
//
// diagnosis: a failure means the scrub became synchronous inside the Shutdown
// response, coupling the report to the connection the gateway closes.
// spec: 5.2 (async scrub report)
func TestShutdownRecycleScrubIsAsynchronous_spec_5_2(t *testing.T) {
	rt := &recycleRuntime{}
	reporter := &recordingPodScrubReporter{}
	ops := &gatedScrubOps{fakePodScrubOps: newFakePodScrubOps(), release: make(chan struct{})}
	done := make(chan struct{})
	s := New("test")
	s.WorkspaceRoot = t.TempDir()
	s.Runtime = rt
	s.PodScrubReporter = reporter
	s.ScrubOps = ops
	s.scrubDone = func() { close(done) }
	startRecycleSession(t, s, "sess-1")

	// Shutdown must return while the scrub is still gated (blocked in
	// KillUserProcesses). If it blocked on the scrub, this call would hang on
	// the unreleased gate and the test would time out.
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Recycle:   &adapterv1.RecycleScrub{PodId: "pod-async", ScrubProfile: "standard"},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// The scrub has not reported yet because it is still gated.
	if reports := reporter.snapshot(); len(reports) != 0 {
		t.Fatalf("scrub reported before it was released (%d reports); Shutdown did not run it asynchronously", len(reports))
	}

	// Release the scrub and confirm the report arrives afterward.
	close(ops.release)
	waitScrubDone(t, done)
	reports := reporter.snapshot()
	if len(reports) != 1 || reports[0].podID != "pod-async" || reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Fatalf("post-release reports = %+v, want one PodScrubSucceeded for pod-async", reports)
	}
}
