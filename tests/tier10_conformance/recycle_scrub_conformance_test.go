// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance case for the §4.7 recycle-disposition Shutdown. A
// conforming adapter, on the Shutdown RPC carrying the recycle disposition
// (the §5.2 occupancy-zero whole-pod scrub trigger), closes only the ending
// session's runtime, keeps the pod process alive across the recycle boundary,
// runs the §5.2 whole-pod scrub, and reports its binary outcome for the
// carried podId exactly once via ReportPodScrub on the GatewayControl link.
//
// The concurrent-slot conformance case (concurrent_slot_conformance_test.go)
// drives a reference runtime binary over its §15.4 stdin/stdout transport
// because per-slot dispatch is a runtime-binary behavior. The recycle-scrub
// contract is an adapter-Server behavior: the adapter Server.Shutdown handler
// branches on the recycle disposition, runs the whole-pod scrub, and emits the
// report. This case therefore drives the exported adapter Server directly with
// a fake runtime, fake scrub host operations, and a recording PodScrubReporter,
// asserting the four conformance properties the recycle boundary rests on:
//
//   - Ending-session runtime closed: the recycle Shutdown tears down only the
//     ending session's runtime process.
//   - Pod process kept alive: a replacement session binds after the recycle
//     Shutdown, proving the pod was not terminated.
//   - Whole-pod scrub run: the §5.2 scrub host operations execute.
//   - Exactly one ReportPodScrub for podId: the adapter emits a single report
//     carrying the podId the recycle Shutdown delivered (the gateway
//     missing-report timer key), so the gateway can match the report to the
//     armed timer.
//
// spec: 4.7 (shutdown recycle disposition), 5.2 (whole-pod scrub).

package tier10_conformance_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// scrubConformanceRuntime is a minimal mutex-guarded RuntimeProcess double for
// the recycle-scrub conformance case. It records the sessions Close was called
// for so the case can assert the ending session's runtime was torn down. The
// guard keeps it -race clean when the async scrub goroutine and the test read
// concurrently.
type scrubConformanceRuntime struct {
	mu     sync.Mutex
	closed []string
}

func (r *scrubConformanceRuntime) Start(context.Context, string) error           { return nil }
func (r *scrubConformanceRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *scrubConformanceRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *scrubConformanceRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *scrubConformanceRuntime) Close(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = append(r.closed, sessionID)
	return nil
}

func (r *scrubConformanceRuntime) closedSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.closed))
	copy(out, r.closed)
	return out
}

// scrubConformanceOps is a scrub.Ops double that records the whole-pod scrub
// host operations without running kill -9 -1 or touching the real filesystem,
// so the conformance case can assert the §5.2 scrub ran. It reports every
// verification path as absent so the scrub succeeds.
type scrubConformanceOps struct {
	mu       sync.Mutex
	killed   bool
	verified bool
}

func (o *scrubConformanceOps) KillUserProcesses(context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.killed = true
	return nil
}

func (o *scrubConformanceOps) PurgeIPCShm(context.Context) error { return nil }
func (o *scrubConformanceOps) RemoveAll(string) error            { return nil }
func (o *scrubConformanceOps) ClearContents(string) error        { return nil }

func (o *scrubConformanceOps) PathState(string) (bool, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.verified = true
	return false, true, nil
}

func (o *scrubConformanceOps) ran() (bool, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.killed, o.verified
}

// scrubConformanceReporter is a PodScrubReporter double capturing every
// ReportPodScrub call so the conformance case can assert the adapter emits
// exactly one report carrying the recycle Shutdown's podId.
type scrubConformanceReporter struct {
	mu      sync.Mutex
	reports []scrubConformanceReport
}

type scrubConformanceReport struct {
	podID   string
	outcome gatewaycontrol.PodScrubOutcome
}

func (r *scrubConformanceReporter) ReportPodScrub(_ context.Context, podID string, outcome gatewaycontrol.PodScrubOutcome, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, scrubConformanceReport{podID: podID, outcome: outcome})
	return nil
}

func (r *scrubConformanceReporter) snapshot() []scrubConformanceReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]scrubConformanceReport, len(r.reports))
	copy(out, r.reports)
	return out
}

// spec: 4.7 (shutdown recycle disposition), 5.2 (whole-pod scrub).
//
// diagnosis: A conforming adapter no longer honors the §4.7 recycle-disposition
//
//	Shutdown: it either terminated the pod instead of keeping it alive across
//	the recycle boundary, stopped running the §5.2 whole-pod scrub, reported
//	the wrong podId (so the gateway cannot match the report to its armed
//	missing-report timer), or emitted more or fewer than one ReportPodScrub.
//	The recycle boundary contract regressed and a recycling pod at occupancy
//	zero can no longer be reused.
func TestRecycleScrubShutdownConformance(t *testing.T) {
	// Unlike the runtime-binary conformance cases, this case drives the adapter
	// Server in-process, so it needs no `go build` and no Go-toolchain guard.
	const podID = "pod-recycle-01"
	rt := &scrubConformanceRuntime{}
	ops := &scrubConformanceOps{}
	reporter := &scrubConformanceReporter{}
	done := make(chan struct{})

	s := adapter.New("conformance")
	s.WorkspaceRoot = t.TempDir()
	s.Runtime = rt
	s.ScrubOps = ops
	s.PodScrubReporter = reporter
	s.SetScrubDoneHook(func() { close(done) })

	// A session is bound before the recycle Shutdown arrives.
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// The recycle-disposition Shutdown: the §5.2 occupancy-zero whole-pod
	// scrub trigger carrying the podId and the cleanup parameters. The scrub
	// profile is not carried on the wire (C4); the gateway routes the §5.2
	// step-7 vm-restart retire on its own runtime store.
	resp, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 podID,
			CleanupCommands:       []string{},
			CleanupTimeoutSeconds: 30,
		},
	})
	if err != nil {
		t.Fatalf("recycle Shutdown: %v", err)
	}
	if !resp.GetExitedCleanly() {
		t.Error("recycle Shutdown reported an unclean exit for a healthy runtime")
	}

	// Property 1: the ending session's runtime is closed.
	if closed := rt.closedSnapshot(); len(closed) != 1 || closed[0] != "sess-1" {
		t.Errorf("ending-session runtime closed = %v, want [sess-1]", closed)
	}

	// Wait for the async scrub goroutine to finish deterministically.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recycle scrub did not finish within 5s")
	}

	// Property 3: the §5.2 whole-pod scrub ran.
	if killed, verified := ops.ran(); !killed || !verified {
		t.Errorf("whole-pod scrub did not run: killed=%v verified=%v", killed, verified)
	}

	// Property 4: exactly one ReportPodScrub carrying the recycle Shutdown's
	// podId, so the gateway can match it to the armed missing-report timer.
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportPodScrub calls = %d, want exactly 1: %+v", len(reports), reports)
	}
	if reports[0].podID != podID {
		t.Errorf("reported podId = %q, want the recycle Shutdown podId %q", reports[0].podID, podID)
	}
	if reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Errorf("reported outcome = %v, want PodScrubSucceeded", reports[0].outcome)
	}

	// Property 2: the pod process stays alive across the recycle boundary — a
	// replacement session binds, which is impossible if Shutdown terminated
	// the pod.
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-2"},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("replacement StartSession after recycle: %v (pod did not survive the recycle boundary)", err)
	}
}

// spec: 5.2 (whole-pod scrub trigger, uniform across session modes), 4.7
// (runtime adapter recycle disposition).
//
// diagnosis: a failure means a conforming adapter does not run the §5.2
// whole-pod scrub on a concurrent-mode recycle Shutdown. A concurrent-session
// pod (maxConcurrentSessions > 1) sets only per-slot session state, never the
// pod-global session, so the gateway's occupancy-zero recycle Shutdown carries
// the last-released slot's session id and NO slot id against a pod whose
// pod-global session is empty. A conforming adapter must dispatch the whole-pod
// scrub on that request rather than rejecting it with a "pod has no assigned
// session" precondition failure. An adapter that gates the scrub behind a
// pod-global session (the pre-CODE-A behavior) leaves every recycling
// concurrent pool to the gateway missing-report timeout and can no longer reuse
// it, so this case pins the concurrent occupancy-zero reuse contract.
func TestRecycleScrubConcurrentModeConformance(t *testing.T) {
	const podID = "pod-recycle-concurrent"
	rt := &scrubConformanceRuntime{}
	ops := &scrubConformanceOps{}
	reporter := &scrubConformanceReporter{}
	done := make(chan struct{})

	s := adapter.New("conformance")
	s.WorkspaceRoot = t.TempDir()
	s.Runtime = rt
	s.ScrubOps = ops
	s.PodScrubReporter = reporter
	s.SetScrubDoneHook(func() { close(done) })

	// No StartSession: a concurrent-session pod never sets the pod-global
	// session (only per-slot state). The recycle Shutdown carries the
	// last-released slot's session id (so the non-empty session_id guard admits
	// it) and no slot id (it is a whole-pod, not a per-slot, teardown).
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "slot-sess"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 podID,
			CleanupCommands:       []string{},
			CleanupTimeoutSeconds: 30,
		},
	}); err != nil {
		t.Fatalf("concurrent-mode recycle Shutdown: %v (the adapter gated the scrub behind a pod-global session)", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent-mode recycle scrub did not finish within 5s")
	}

	if killed, verified := ops.ran(); !killed || !verified {
		t.Errorf("concurrent-mode whole-pod scrub did not run: killed=%v verified=%v", killed, verified)
	}
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("concurrent-mode ReportPodScrub calls = %d, want exactly 1: %+v", len(reports), reports)
	}
	if reports[0].podID != podID {
		t.Errorf("reported podId = %q, want %q", reports[0].podID, podID)
	}
	if reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Errorf("reported outcome = %v, want PodScrubSucceeded", reports[0].outcome)
	}
}

// spec: 5.2 (ReportPodScrub binary outcome, retire-and-reprovision), 4.7
// (runtime adapter recycle disposition).
//
// diagnosis: a failure means a conforming adapter withheld or duplicated the
// whole-pod scrub report for a vm-restart pool. Under the §5.2 step-7
// retire-and-reprovision reconciliation the profile is no longer carried on the
// wire and the adapter runs the same whole-pod scrub for every profile, so a
// vm-restart recycle Shutdown must emit exactly one binary ReportPodScrub for
// its podId with no withhold. The removed in-guest VMRestarter seam once made a
// vm-restart pod withhold its report (relying on scrub.ErrNoRestarter); a
// conforming adapter no longer does, and the gateway routes the retire from its
// own runtime store. A withheld report here would force the emergent
// missing-report timeout instead of the deliberate gateway retire.
func TestRecycleScrubVMRestartReportsUniformlyConformance(t *testing.T) {
	const podID = "pod-recycle-vm"
	rt := &scrubConformanceRuntime{}
	ops := &scrubConformanceOps{}
	reporter := &scrubConformanceReporter{}
	done := make(chan struct{})

	s := adapter.New("conformance")
	s.WorkspaceRoot = t.TempDir()
	s.Runtime = rt
	s.ScrubOps = ops
	s.PodScrubReporter = reporter
	s.SetScrubDoneHook(func() { close(done) })

	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-vm"},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// A vm-restart pool's recycle Shutdown carries the same recycle sub-message
	// as any other profile (the profile is not on the wire). The adapter must
	// run the whole-pod scrub and report its binary outcome once, with no
	// per-profile withhold.
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-vm"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 podID,
			CleanupCommands:       []string{},
			CleanupTimeoutSeconds: 30,
		},
	}); err != nil {
		t.Fatalf("recycle Shutdown: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("vm-restart recycle scrub did not finish within 5s")
	}

	if killed, verified := ops.ran(); !killed || !verified {
		t.Errorf("vm-restart whole-pod scrub did not run: killed=%v verified=%v", killed, verified)
	}
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("vm-restart ReportPodScrub calls = %d, want exactly 1 (no withhold): %+v", len(reports), reports)
	}
	if reports[0].podID != podID {
		t.Errorf("reported podId = %q, want %q", reports[0].podID, podID)
	}
	if reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Errorf("reported outcome = %v, want PodScrubSucceeded", reports[0].outcome)
	}
}
