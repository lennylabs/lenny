// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance cases for the §4.7 recycle-disposition Shutdown. A
// conforming adapter, on the Shutdown RPC carrying the recycle disposition
// (the §5.2 occupancy-zero whole-pod scrub trigger), ends the session's use
// of the runtime process, keeps its own process and the runtime process
// alive across the recycle boundary (§4.7.10), runs the §5.2 whole-pod
// scrub, and reports its binary outcome and whether the runtime can serve
// the next session for the carried podId exactly once via ReportPodScrub on
// the GatewayControl link.
//
// The concurrent-slot conformance case (concurrent_slot_conformance_test.go)
// drives a reference runtime binary over its §15.4 stdin/stdout transport
// because per-slot dispatch is a runtime-binary behavior. The recycle-scrub
// contract is an adapter-Server behavior: the adapter Server.Shutdown handler
// branches on the recycle disposition, runs the whole-pod scrub, and emits the
// report. These cases therefore drive the exported adapter Server directly,
// with fake scrub host operations and a recording PodScrubReporter. The
// runtime-lifetime cases run on a real SocketRuntimeProcess with a runtime
// that dials once, and assert these properties:
//
//   - Runtime process kept: the recycle Shutdown does not end the runtime's
//     connection.
//   - Replacement session served: a replacement session starts on the
//     runtime's first connection, and its frame reaches that connection.
//   - Whole-pod scrub run: the §5.2 scrub host operations execute.
//   - Exactly one ReportPodScrub for podId: the adapter emits a single report
//     carrying the podId the recycle Shutdown delivered (the gateway
//     missing-report timer key), so the gateway can match the report to the
//     armed timer.
//   - Runtime not live: after the runtime closes its connection, the report
//     carries runtime_live false and a later start fails at once.
//
// spec: 4.7 (shutdown recycle disposition), 4.7.10 (Runtime process
// lifetime), 5.2 (whole-pod scrub), 5.2 (Pod retirement policy).

package tier10_conformance_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// scrubConformanceRuntime is a minimal RuntimeProcess double for the
// recycle-scrub conformance cases that do not exercise the runtime's
// lifetime. closeErr, when set, is returned from Close so the per-slot
// leaked-outcome case can drive a cleanup failure through the slot release
// path. It does not implement ServesNextSession, so a whole-pod scrub on it
// reports runtime_live false.
type scrubConformanceRuntime struct {
	closeErr error
}

func (r *scrubConformanceRuntime) Start(context.Context, string) error           { return nil }
func (r *scrubConformanceRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *scrubConformanceRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *scrubConformanceRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *scrubConformanceRuntime) Close(context.Context, string) error {
	return r.closeErr
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
	podID       string
	outcome     gatewaycontrol.PodScrubOutcome
	runtimeLive bool
}

func (r *scrubConformanceReporter) ReportPodScrub(_ context.Context, podID string, outcome gatewaycontrol.PodScrubOutcome, runtimeLive bool, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, scrubConformanceReport{podID: podID, outcome: outcome, runtimeLive: runtimeLive})
	return nil
}

func (r *scrubConformanceReporter) snapshot() []scrubConformanceReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]scrubConformanceReport, len(r.reports))
	copy(out, r.reports)
	return out
}

// onceDialingRuntime is a runtime double for the sidecar transport: one
// goroutine dials the adapter's runtime socket once, records the sessionId
// of every frame it reads, and never redials. It records whether its read
// ended, which happens only when the adapter closes the connection. The
// mutex keeps it -race clean against the test's reads.
type onceDialingRuntime struct {
	conn net.Conn

	mu       sync.Mutex
	sessions []string
	ended    bool
	frames   chan string
}

// dialRuntimeOnce dials the adapter's runtime socket the way runtimekit
// does (an "@"-prefixed address maps to a leading NUL) and starts the
// reader. The listener is bound at construction, so the dial completes
// into the accept backlog before the first Start accepts it.
func dialRuntimeOnce(t *testing.T, socket string) *onceDialingRuntime {
	t.Helper()
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "unix", addr)
	if err != nil {
		t.Fatalf("dial runtime socket %q: %v", socket, err)
	}
	r := &onceDialingRuntime{conn: conn, frames: make(chan string, 16)}
	t.Cleanup(func() { _ = conn.Close() })
	go r.read()
	return r
}

// read records each frame's sessionId until the connection ends.
func (r *onceDialingRuntime) read() {
	sc := bufio.NewScanner(r.conn)
	for sc.Scan() {
		var frame struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(sc.Bytes(), &frame)
		r.mu.Lock()
		r.sessions = append(r.sessions, frame.SessionID)
		r.mu.Unlock()
		select {
		case r.frames <- frame.SessionID:
		default:
		}
	}
	r.mu.Lock()
	r.ended = true
	r.mu.Unlock()
}

// readEnded reports whether the runtime's read of its connection ended.
func (r *onceDialingRuntime) readEnded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ended
}

// requireFrameFor waits for a frame addressed to sessionID on the runtime's
// one connection.
func (r *onceDialingRuntime) requireFrameFor(t *testing.T, sessionID string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got := <-r.frames:
			if got == sessionID {
				return
			}
		case <-deadline:
			r.mu.Lock()
			seen := append([]string(nil), r.sessions...)
			r.mu.Unlock()
			t.Fatalf("no frame for %s reached the runtime's first connection within 5s; frames seen: %v", sessionID, seen)
		}
	}
}

// conformanceRuntimeSocket returns a socket address for one case: a Linux
// abstract address, which leaves no file, or elsewhere a short filesystem
// path, because the Unix sun_path limit is shorter than t.TempDir on some
// hosts.
func conformanceRuntimeSocket(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" {
		return fmt.Sprintf("@lenny-conf-rt-%d-%s", os.Getpid(), t.Name())
	}
	f, err := os.CreateTemp("", "rt-*.sock")
	if err != nil {
		t.Fatalf("temp socket path: %v", err)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

// socketRecycleFixture is an adapter Server on a real SocketRuntimeProcess
// with fake scrub host operations and a recording PodScrubReporter.
type socketRecycleFixture struct {
	server   *adapter.Server
	sp       *adapter.SocketRuntimeProcess
	ops      *scrubConformanceOps
	reporter *scrubConformanceReporter
	done     chan struct{}
}

// newSocketRecycleFixture builds the fixture. AcceptTimeout is 30s, the
// production default, so a Start that waited for a runtime to dial again
// would run far past the bounds the cases assert.
func newSocketRecycleFixture(t *testing.T) *socketRecycleFixture {
	t.Helper()
	sp, err := adapter.NewSocketRuntimeProcess(conformanceRuntimeSocket(t), adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 30 * time.Second

	f := &socketRecycleFixture{
		sp:       sp,
		ops:      &scrubConformanceOps{},
		reporter: &scrubConformanceReporter{},
		done:     make(chan struct{}),
	}
	s := adapter.New("conformance")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = sp
	s.ScrubOps = f.ops
	s.PodScrubReporter = f.reporter
	s.SetScrubDoneHook(func() { close(f.done) })
	f.server = s
	return f
}

// startSession starts sessionID and fails the case on an error.
func (f *socketRecycleFixture) startSession(t *testing.T, sessionID string) {
	t.Helper()
	if _, err := f.server.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", sessionID, err)
	}
}

// sendMessage delivers a message envelope to sessionID's runtime.
func (f *socketRecycleFixture) sendMessage(t *testing.T, sessionID string) {
	t.Helper()
	if _, err := f.server.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: []byte(`{"type":"message","input":[]}`),
	}); err != nil {
		t.Fatalf("SendMessage(%s): %v", sessionID, err)
	}
}

// recycleShutdown sends the recycle-disposition Shutdown for sessionID, the
// §5.2 occupancy-zero whole-pod scrub trigger, and waits for the async
// scrub to finish.
func (f *socketRecycleFixture) recycleShutdown(t *testing.T, sessionID, podID string) *adapterv1.ShutdownResponse {
	t.Helper()
	resp, err := f.server.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: sessionID},
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 podID,
			CleanupCommands:       []string{},
			CleanupTimeoutSeconds: 30,
		},
	})
	if err != nil {
		t.Fatalf("recycle Shutdown(%s): %v", sessionID, err)
	}
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("recycle scrub did not finish within 5s")
	}
	return resp
}

// requireOneReport asserts exactly one ReportPodScrub for podID with the
// succeeded outcome and returns it.
func (f *socketRecycleFixture) requireOneReport(t *testing.T, podID string) scrubConformanceReport {
	t.Helper()
	reports := f.reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportPodScrub calls = %d, want exactly 1: %+v", len(reports), reports)
	}
	if reports[0].podID != podID {
		t.Errorf("reported podId = %q, want the recycle Shutdown podId %q", reports[0].podID, podID)
	}
	if reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Errorf("reported outcome = %v, want PodScrubSucceeded", reports[0].outcome)
	}
	return reports[0]
}

// spec: 4.7 (shutdown recycle disposition), 4.7.10 (Runtime process
// lifetime), 5.2 (whole-pod scrub).
//
// The case drives the adapter Server on a real SocketRuntimeProcess whose
// runtime dials once and never redials, which is the sidecar deployment
// model: the kubelet starts the runtime container once and nothing inside
// the pod starts it again.
//
// diagnosis: a Property 2 failure means the recycle Shutdown ended the
//
//	runtime process, so the recycled pod cannot serve its next session.
//	A failure of the other properties means the adapter stopped running the
//	§5.2 whole-pod scrub, reported the wrong podId (so the gateway cannot
//	match the report to its armed missing-report timer), emitted more or
//	fewer than one ReportPodScrub, or reported a live runtime as not live.
func TestRecycleScrubShutdownConformance(t *testing.T) {
	// Unlike the runtime-binary conformance cases, this case drives the adapter
	// Server in-process, so it needs no `go build` and no Go-toolchain guard.
	const podID = "pod-recycle-01"
	f := newSocketRecycleFixture(t)
	rt := dialRuntimeOnce(t, f.sp.SocketPath())

	// A session is bound and served before the recycle Shutdown arrives.
	f.startSession(t, "sess-1")
	f.sendMessage(t, "sess-1")
	rt.requireFrameFor(t, "sess-1")

	// The recycle-disposition Shutdown. The scrub profile is not carried on
	// the wire; the gateway routes the §5.2 step-7 vm-restart retire on its
	// own runtime store.
	resp := f.recycleShutdown(t, "sess-1", podID)
	if !resp.GetExitedCleanly() {
		t.Error("recycle Shutdown reported an unclean exit for a healthy runtime")
	}

	// Property 1: the ending session's teardown keeps the runtime process.
	// The runtime reads no end of its connection across the recycle boundary.
	if rt.readEnded() {
		t.Error("the runtime's connection ended at the recycle Shutdown; the runtime process must live as long as the pod")
	}

	// Property 3: the §5.2 whole-pod scrub ran.
	if killed, verified := f.ops.ran(); !killed || !verified {
		t.Errorf("whole-pod scrub did not run: killed=%v verified=%v", killed, verified)
	}

	// Property 4: exactly one ReportPodScrub carrying the recycle Shutdown's
	// podId, and it reports the kept runtime as live, so the gateway reuses
	// the pod rather than retiring it with runtime_not_live.
	if report := f.requireOneReport(t, podID); !report.runtimeLive {
		t.Error("ReportPodScrub runtime_live = false for a runtime whose connection is live; the gateway would retire a reusable pod")
	}

	// Property 2: the replacement session starts on the runtime's first
	// connection. No runtime dials again, so a Start that waited for an
	// accept would run to the 30s bound; one on the kept connection returns
	// at once, and the replacement session's frame reaches that connection.
	began := time.Now()
	f.startSession(t, "sess-2")
	if elapsed := time.Since(began); elapsed > 5*time.Second {
		t.Fatalf("replacement StartSession took %s; it must start on the kept connection without an accept", elapsed)
	}
	f.sendMessage(t, "sess-2")
	rt.requireFrameFor(t, "sess-2")
	if rt.readEnded() {
		t.Error("the runtime's first connection ended while serving the replacement session")
	}
}

// spec: 5.2 (Pod retirement policy, Runtime not live), 4.7.10 (Runtime
// process lifetime), 4.7 (ReportPodScrub).
//
// Property 5: a runtime that closes its connection after the first session
// is not re-created or reconnected inside the pod. The whole-pod scrub
// report states that the runtime cannot serve the next session, so the
// gateway retires the pod with runtime_not_live, and a later start fails at
// once rather than waiting out the accept bound.
//
// diagnosis: a failure means the adapter reports a dead runtime as live, or
// starts a session over a closed connection, or waits for a runtime to dial
// again; a recycled pod whose runtime has exited is then handed to the next
// session.
func TestRecycleScrubRuntimeNotLiveConformance(t *testing.T) {
	const podID = "pod-recycle-not-live"
	f := newSocketRecycleFixture(t)
	rt := dialRuntimeOnce(t, f.sp.SocketPath())

	f.startSession(t, "sess-1")
	f.sendMessage(t, "sess-1")
	rt.requireFrameFor(t, "sess-1")

	// The runtime closes its end after the first session and never redials.
	_ = rt.conn.Close()
	waitForRuntimeEnded(t, f.sp)

	f.recycleShutdown(t, "sess-1", podID)
	if killed, verified := f.ops.ran(); !killed || !verified {
		t.Errorf("whole-pod scrub did not run: killed=%v verified=%v", killed, verified)
	}
	if report := f.requireOneReport(t, podID); report.runtimeLive {
		t.Error("ReportPodScrub runtime_live = true after the runtime closed its connection; want false so the gateway retires the pod")
	}

	began := time.Now()
	_, err := f.server.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-2"},
		Runtime:   "echo",
	})
	if err == nil {
		t.Fatal("StartSession(sess-2) after the runtime closed its connection succeeded; want a start failure")
	}
	if elapsed := time.Since(began); elapsed > 5*time.Second {
		t.Fatalf("StartSession(sess-2) failed after %s; want a prompt failure, not a wait on the 30s accept bound", elapsed)
	}
}

// waitForRuntimeEnded waits for the transport to observe the runtime's
// close, which its fan-out reader records asynchronously.
func waitForRuntimeEnded(t *testing.T, sp *adapter.SocketRuntimeProcess) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for sp.ServesNextSession() {
		if time.Now().After(deadline) {
			t.Fatal("the transport did not observe the runtime's close within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// spec: 5.2 (whole-pod scrub trigger, uniform across session modes), 4.7
// (runtime adapter recycle disposition).
//
// diagnosis: a failure means a conforming adapter does not run the §5.2
// whole-pod scrub on an occupancy-zero recycle Shutdown whose named session
// the adapter no longer holds. The gateway sends that request after the last
// slot on the pod has already been released, so the recycle disposition it
// carries parameterizes a whole-pod scrub beside a per-session teardown that
// has nothing left to tear down. A conforming adapter must dispatch the scrub
// on that request rather than refusing it because the named session is not
// bound. An adapter that gates the scrub behind a live session leaves every
// recycling pool to the gateway missing-report timeout and can no longer reuse
// the pod, so this case pins the occupancy-zero reuse contract.
func TestRecycleScrubAtOccupancyZeroConformance(t *testing.T) {
	const podID = "pod-recycle-concurrent"
	rt := &scrubConformanceRuntime{}
	ops := &scrubConformanceOps{}
	reporter := &scrubConformanceReporter{}
	done := make(chan struct{})

	s := adapter.New("conformance")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = rt
	s.ScrubOps = ops
	s.PodScrubReporter = reporter
	s.SetScrubDoneHook(func() { close(done) })

	// No StartSession: the gateway sends the recycle Shutdown once occupancy
	// has reached zero, so the session it names is already released and the
	// adapter's registry holds no entry for it. The request still carries that
	// session id, which is the only address the message has, and the whole-pod
	// scrub runs from the recycle disposition beside the teardown clause.
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "slot-sess"},
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 podID,
			CleanupCommands:       []string{},
			CleanupTimeoutSeconds: 30,
		},
	}); err != nil {
		t.Fatalf("occupancy-zero recycle Shutdown: %v (the adapter gated the scrub behind a live session)", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("occupancy-zero recycle scrub did not finish within 5s")
	}

	if killed, verified := ops.ran(); !killed || !verified {
		t.Errorf("occupancy-zero whole-pod scrub did not run: killed=%v verified=%v", killed, verified)
	}
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("occupancy-zero ReportPodScrub calls = %d, want exactly 1: %+v", len(reports), reports)
	}
	if reports[0].podID != podID {
		t.Errorf("reported podId = %q, want %q", reports[0].podID, podID)
	}
	if reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Errorf("reported outcome = %v, want PodScrubSucceeded", reports[0].outcome)
	}
}

// scrubConformanceSessionReporter is a SessionScrubReporter double capturing
// every ReportSessionScrub the adapter emits, so the per-session leaked-outcome
// conformance case can assert the adapter emits exactly one report carrying the
// cached pod id, the released session, and the §5.2 per-slot cleanup
// outcome. The mutex keeps it -race clean.
type scrubConformanceSessionReporter struct {
	mu      sync.Mutex
	reports []scrubConformanceSessionReport
}

type scrubConformanceSessionReport struct {
	podID     string
	sessionID string
	outcome   gatewaycontrol.SessionScrubOutcome
}

func (r *scrubConformanceSessionReporter) ReportSessionScrub(_ context.Context, podID, sessionID string, outcome gatewaycontrol.SessionScrubOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, scrubConformanceSessionReport{podID: podID, sessionID: sessionID, outcome: outcome})
	return nil
}

func (r *scrubConformanceSessionReporter) snapshot() []scrubConformanceSessionReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]scrubConformanceSessionReport, len(r.reports))
	copy(out, r.reports)
	return out
}

// spec: 5.2 (per-slot cleanup outcome reporting, maxSessionsPerPod), 4.7
// (runtime adapter leaked determination).
//
// diagnosis: a failure means a conforming adapter mis-reports a failed per-slot
//
//	cleanup on the §6.4 slot release. The adapter derives the per-slot outcome
//	from the same runtime-Close error that sets ShutdownResponse.ExitedCleanly,
//	so a Close that could not reclaim the slot's resources must yield exactly
//	one ReportSessionScrub carrying SESSION_SCRUB_OUTCOME_LEAKED addressed by
//	the pod and the session it names, and a clean Close must yield
//	SESSION_SCRUB_OUTCOME_RELEASED. The leaked outcome is the sole feeder of
//	the gateway leak ledger, the persistent leaked-slot count, and the
//	maxSessionsPerPod drain chain, so an adapter that always emits released (or
//	drops the report) silently disables the entire leaked-pod liveness path and
//	a permanently leaked pod is never reclaimed.
func TestRecycleSessionScrubLeakedOutcomeConformance(t *testing.T) {
	const podID = "pod-slot-leaked"
	// The adapter reads its own pod identity once from the Downward API
	// POD_NAME env at construction and keys every ReportSessionScrub on it.
	t.Setenv("POD_NAME", podID)

	// A slot release whose runtime Close FAILS to reclaim the slot's
	// resources: the adapter must derive the leaked outcome from that error.
	rt := &scrubConformanceRuntime{closeErr: errors.New("shred timed out reclaiming slot tree")}
	reporter := &scrubConformanceSessionReporter{}

	base := t.TempDir()
	s := adapter.New("conformance")
	// The §6.4 roots every session's per-slot workspace is materialized
	// under. The slot layout is the only layout on every pod.
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.Runtime = rt
	s.SessionScrubReporter = reporter

	// Start one session, which binds the pod's one slot, then release it.
	// Every session is bound to a slot on every pod, so the start and the
	// teardown take the slot path whatever the pool's concurrency.
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "slot-sess"},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession(slot-sess): %v", err)
	}

	// Shutdown tears the named session down. The runtime Close returns an
	// error, so the cleanup leaked.
	resp, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "slot-sess"},
	})
	if err != nil {
		t.Fatalf("Shutdown(slot-sess): %v", err)
	}
	// The outcome and the ExitedCleanly flag derive from the same Close error.
	if resp.GetExitedCleanly() {
		t.Error("ExitedCleanly = true for a failed slot cleanup; outcome and flag must agree")
	}

	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportSessionScrub calls = %d, want exactly 1 per slot release: %+v", len(reports), reports)
	}
	got := reports[0]
	if got.outcome != gatewaycontrol.SessionScrubLeaked {
		t.Errorf("reported outcome = %v, want SESSION_SCRUB_OUTCOME_LEAKED for a failed per-slot cleanup", got.outcome)
	}
	if got.podID != podID {
		t.Errorf("reported podId = %q, want the cached pod identity %q", got.podID, podID)
	}
	if got.sessionID != "slot-sess" {
		t.Errorf("reported sessionId = %q, want slot-sess", got.sessionID)
	}
	if got.sessionID == "" {
		t.Error("reported session id is empty; the report is addressed by the session it names")
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
	s.WorkspaceBase = t.TempDir()
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
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "sess-vm"},
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
