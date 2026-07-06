// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// recordingSessionScrubReporter is a SessionScrubReporter double capturing
// every ReportSessionScrub call so a test can assert the pod id, session id,
// slot id, and outcome the adapter reported (and how many reports it emitted,
// since the withhold and terminate paths emit none).
type recordingSessionScrubReporter struct {
	mu      sync.Mutex
	reports []sessionScrubCall
	err     error
}

type sessionScrubCall struct {
	podID     string
	sessionID string
	slotID    string
	outcome   gatewaycontrol.SessionScrubOutcome
}

func (r *recordingSessionScrubReporter) ReportSessionScrub(_ context.Context, podID, sessionID, slotID string, outcome gatewaycontrol.SessionScrubOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, sessionScrubCall{podID: podID, sessionID: sessionID, slotID: slotID, outcome: outcome})
	return r.err
}

func (r *recordingSessionScrubReporter) snapshot() []sessionScrubCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sessionScrubCall, len(r.reports))
	copy(out, r.reports)
	return out
}

// concurrentReportServer builds a Server for the concurrent (slot) path wired
// to a recording session-scrub reporter, a non-empty cached pod id, and a
// runtime whose Close returns closeErr, so the slot-release path derives and
// reports the outcome.
func concurrentReportServer(t *testing.T, closeErr error) (*Server, *recordingSessionScrubReporter) {
	t.Helper()
	base := t.TempDir()
	s := New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.Runtime = &recycleRuntime{closeErr: closeErr}
	s.podID = "pod-concurrent"
	reporter := &recordingSessionScrubReporter{}
	s.SessionScrubReporter = reporter
	return s, reporter
}

func startSlot(t *testing.T, s *Server, sessionID, slotID string) {
	t.Helper()
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
		SlotId:    &adapterv1.SlotId{Value: slotID},
	}); err != nil {
		t.Fatalf("StartSession(slot %s): %v", slotID, err)
	}
}

func shutdownSlotReq(sessionID, slotID string) *adapterv1.ShutdownRequest {
	return &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		SlotId:    &adapterv1.SlotId{Value: slotID},
	}
}

// TestShutdownSlotEmitsReleasedOnCleanClose asserts the §5.2 per-slot cleanup
// report: a concurrent-slot Shutdown whose runtime Close returns cleanly emits
// exactly one ReportSessionScrub for the slot carrying outcome=released, the
// cached pod id, the released session, and the slot id. This advances
// sessions_served (feeding maxSessionsPerPod) on every clean release.
//
// diagnosis: a failure means a concurrent pool no longer reports a clean slot
// release, so sessions_served never advances and the maxSessionsPerPod
// retirement stays inert on a healthy concurrent pool.
// spec: 5.2 (ReportSessionScrub, maxSessionsPerPod), 4.7 (per-slot cleanup outcome)
func TestShutdownSlotEmitsReleasedOnCleanClose_spec_5_2(t *testing.T) {
	s, reporter := concurrentReportServer(t, nil)
	startSlot(t, s, "sess-a", "slot-a")

	resp, err := s.Shutdown(context.Background(), shutdownSlotReq("sess-a", "slot-a"))
	if err != nil {
		t.Fatalf("Shutdown(slot-a): %v", err)
	}
	if !resp.GetExitedCleanly() {
		t.Error("ExitedCleanly = false for a clean close")
	}
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportSessionScrub calls = %d, want exactly 1 per slot release", len(reports))
	}
	got := reports[0]
	if got.outcome != gatewaycontrol.SessionScrubReleased {
		t.Errorf("outcome = %v, want released for a clean cleanup", got.outcome)
	}
	if got.podID != "pod-concurrent" {
		t.Errorf("pod id = %q, want the cached pod identity", got.podID)
	}
	if got.sessionID != "sess-a" || got.slotID != "slot-a" {
		t.Errorf("report = {session %q, slot %q}, want {sess-a, slot-a}", got.sessionID, got.slotID)
	}
}

// TestShutdownSlotEmitsLeakedOnFailedClose is the spec-named-failure branch: a
// concurrent-slot Shutdown whose runtime Close returns an error (a cleanup that
// could not reclaim the slot's resources) emits exactly one ReportSessionScrub
// with outcome=leaked. The leaked outcome is the sole feeder of the gateway
// leak ledger, the persistent leaked count, and the drain chain, so a wrong
// determination (always released) silently disables the leaked-pod liveness.
//
// diagnosis: a failure means the adapter mis-reports a failed per-slot cleanup
// as released, so the leak ledger, the persistent leaked count, and the drain
// never fire and a permanently leaked concurrent pod is never reclaimed.
// spec: 5.2 (per-slot cleanup outcome), 4.7 (leaked determination)
func TestShutdownSlotEmitsLeakedOnFailedClose_spec_5_2(t *testing.T) {
	s, reporter := concurrentReportServer(t, errors.New("shred timed out reclaiming slot tree"))
	startSlot(t, s, "sess-a", "slot-a")

	resp, err := s.Shutdown(context.Background(), shutdownSlotReq("sess-a", "slot-a"))
	if err != nil {
		t.Fatalf("Shutdown(slot-a): %v", err)
	}
	// The outcome and the ExitedCleanly flag derive from the same closeErr.
	if resp.GetExitedCleanly() {
		t.Error("ExitedCleanly = true for a failed close; outcome and flag must agree")
	}
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportSessionScrub calls = %d, want exactly 1", len(reports))
	}
	if reports[0].outcome != gatewaycontrol.SessionScrubLeaked {
		t.Errorf("outcome = %v, want leaked for a failed cleanup", reports[0].outcome)
	}
}

// TestShutdownSlotWithdrawsReportWhenPodIDEmpty asserts the fail-closed pod-id
// path: with an empty cached podID (a missing or misnamed Downward API
// POD_NAME env), the slot release withholds the report rather than reporting
// under an empty key the gateway would reject InvalidArgument. The slot still
// tears down and the response is unaffected.
//
// diagnosis: a failure means the adapter reports a session scrub under an empty
// pod id, so the gateway rejects it and a broken pod-spec env is not surfaced
// while sessions_served silently never advances.
// spec: 5.2, 4.7 (adapter pod identity, fail-closed pod id)
func TestShutdownSlotWithdrawsReportWhenPodIDEmpty_spec_5_2(t *testing.T) {
	s, reporter := concurrentReportServer(t, nil)
	s.podID = "" // simulate an unset POD_NAME env
	startSlot(t, s, "sess-a", "slot-a")

	if _, err := s.Shutdown(context.Background(), shutdownSlotReq("sess-a", "slot-a")); err != nil {
		t.Fatalf("Shutdown(slot-a): %v", err)
	}
	if got := reporter.snapshot(); len(got) != 0 {
		t.Errorf("ReportSessionScrub emitted %d reports with an empty pod id, want 0 (withheld fail-closed): %+v", len(got), got)
	}
}

// TestShutdownSlotToleratesReporterError asserts the report is best-effort: a
// ReportSessionScrub transport failure is logged and does not fail the slot
// release the caller has already completed. The gateway missing-report timeout
// and the next release's idempotent re-report are the backstops.
//
// diagnosis: a failure means a transient gateway-control error on the session
// scrub report propagates out of Shutdown, failing an otherwise-complete slot
// release and stalling the recycle boundary on a report the gateway would
// recover on its own.
// spec: 5.2 (ReportSessionScrub best-effort), 4.7
func TestShutdownSlotToleratesReporterError_spec_5_2(t *testing.T) {
	s, reporter := concurrentReportServer(t, nil)
	reporter.err = errors.New("gateway-control unavailable")
	startSlot(t, s, "sess-a", "slot-a")

	resp, err := s.Shutdown(context.Background(), shutdownSlotReq("sess-a", "slot-a"))
	if err != nil {
		t.Fatalf("Shutdown returned an error for a failed report; the release must not fail: %v", err)
	}
	if !resp.GetExitedCleanly() {
		t.Error("ExitedCleanly = false; a report error must not affect the close outcome")
	}
	// The report was attempted (and failed) exactly once.
	if got := reporter.snapshot(); len(got) != 1 {
		t.Errorf("ReportSessionScrub attempts = %d, want 1 (attempted then logged on error)", len(got))
	}
}

// TestBaseRecycleShutdownEmitsSessionScrub asserts CODE-B in base mode: a base
// (maxConcurrentSessions == 1) recycling Shutdown emits exactly one
// ReportSessionScrub with an empty slot id for the ending session before the
// whole-pod scrub, so advanceScrubCounters reads back a non-zero sessions_served
// and maxSessionsPerPod becomes functional in base mode. Base-mode
// maxSessionsPerPod was inert because this path never emitted the report.
//
// diagnosis: a failure means a base recycling pool does not advance
// sessions_served on release, so its maxSessionsPerPod retirement never fires
// and the pool serves unboundedly past its configured session cap.
// spec: 5.2 (ReportSessionScrub base mode, maxSessionsPerPod)
func TestBaseRecycleShutdownEmitsSessionScrub_spec_5_2(t *testing.T) {
	s, _, _, done := recycleServer(t)
	reporter := &recordingSessionScrubReporter{}
	s.SessionScrubReporter = reporter
	s.podID = "pod-base"
	startRecycleSession(t, s, "sess-1")

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Recycle:   &adapterv1.RecycleScrub{PodId: "pod-base"},
	}); err != nil {
		t.Fatalf("Shutdown(recycle): %v", err)
	}
	waitScrubDone(t, done)

	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportSessionScrub calls = %d, want exactly 1 for a base recycle release", len(reports))
	}
	got := reports[0]
	if got.slotID != "" {
		t.Errorf("slot id = %q, want empty for a base (non-concurrent) pod", got.slotID)
	}
	if got.sessionID != "sess-1" {
		t.Errorf("session id = %q, want sess-1", got.sessionID)
	}
	if got.outcome != gatewaycontrol.SessionScrubReleased {
		t.Errorf("outcome = %v, want released for a clean base recycle close", got.outcome)
	}
}

// TestBaseTerminateShutdownEmitsNoSessionScrub asserts the terminate path (no
// recycle disposition) emits no ReportSessionScrub: a session-mode pod is
// replaced rather than reused, so it advances no sessions_served and runs no
// scrub. Only the recycle boundary reports.
//
// diagnosis: a failure means the adapter reports a session scrub on the
// terminate path, inflating sessions_served for pods that are being replaced
// and never recycled, corrupting the maxSessionsPerPod accounting.
// spec: 5.2 (ReportSessionScrub on the recycle boundary only)
func TestBaseTerminateShutdownEmitsNoSessionScrub_spec_5_2(t *testing.T) {
	s, _, _, _ := recycleServer(t)
	reporter := &recordingSessionScrubReporter{}
	s.SessionScrubReporter = reporter
	s.podID = "pod-base"
	startRecycleSession(t, s, "sess-1")

	// No Recycle disposition: the terminate path.
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	}); err != nil {
		t.Fatalf("Shutdown(terminate): %v", err)
	}
	if got := reporter.snapshot(); len(got) != 0 {
		t.Errorf("ReportSessionScrub emitted %d reports on the terminate path, want 0: %+v", len(got), got)
	}
}
