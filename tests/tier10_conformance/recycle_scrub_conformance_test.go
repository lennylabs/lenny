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
	// scrub trigger carrying the podId and the standard scrub profile.
	resp, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 podID,
			CleanupCommands:       []string{},
			CleanupTimeoutSeconds: 30,
			ScrubProfile:          "standard",
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
