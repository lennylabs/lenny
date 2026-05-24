// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeRuntime plays the agent-runtime end of the §4.7 lifecycle
// channel: it dials the adapter's Unix socket and exchanges JSONL
// frames.
type fakeRuntime struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func (fr *fakeRuntime) read() lifecycleFrame {
	fr.t.Helper()
	_ = fr.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	frame, err := readLifecycleFrame(fr.r)
	if err != nil {
		fr.t.Fatalf("fakeRuntime read: %v", err)
	}
	return frame
}

func (fr *fakeRuntime) write(f lifecycleFrame) {
	fr.t.Helper()
	enc := json.NewEncoder(fr.conn)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		fr.t.Fatalf("fakeRuntime write: %v", err)
	}
}

// handshake completes the lifecycle_capabilities exchange from the
// runtime side, declaring support for every capability the adapter
// advertised.
func (fr *fakeRuntime) handshake() {
	fr.t.Helper()
	caps := fr.read()
	if caps.Type != "lifecycle_capabilities" {
		fr.t.Fatalf("first adapter frame = %q, want lifecycle_capabilities", caps.Type)
	}
	fr.write(lifecycleFrame{Type: "lifecycle_support", Capabilities: caps.Capabilities})
}

// startLifecycleChannel creates a LifecycleChannel on a temporary
// socket, runs it, and dials it as a fake runtime. Run and the
// connection are torn down on test cleanup.
func startLifecycleChannel(t *testing.T) (*LifecycleChannel, *fakeRuntime) {
	return startLifecycleChannelOpts(t, nil)
}

// startLifecycleChannelOpts is startLifecycleChannel with a hook to
// mutate the channel (set task mode, tighten the ack timeout) before Run
// performs the handshake.
func startLifecycleChannelOpts(t *testing.T, configure func(*LifecycleChannel)) (*LifecycleChannel, *fakeRuntime) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lc-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "lifecycle.sock")

	lc, err := NewLifecycleChannel(sock)
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	if configure != nil {
		configure(lc)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runErr := make(chan error, 1)
	go func() { runErr <- lc.Run(ctx) }()
	t.Cleanup(func() {
		_ = lc.Close()
		<-runErr
	})

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial lifecycle socket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return lc, &fakeRuntime{t: t, conn: conn, r: bufio.NewReader(conn)}
}

func TestLifecycleChannelHandshake(t *testing.T) {
	lc, fr := startLifecycleChannel(t)

	caps := fr.read()
	if caps.Type != "lifecycle_capabilities" {
		t.Fatalf("first frame type = %q, want lifecycle_capabilities", caps.Type)
	}
	if caps.ProtocolVersion != lifecycleProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", caps.ProtocolVersion, lifecycleProtocolVersion)
	}
	want := map[string]bool{
		"checkpoint": true, "interrupt": true,
		"credential_rotation": true, "deadline_signal": true,
	}
	for _, c := range caps.Capabilities {
		delete(want, c)
	}
	if len(want) != 0 {
		t.Errorf("adapter omitted lifecycle capabilities: %v", want)
	}

	// The runtime declares support for a subset; the channel records it.
	fr.write(lifecycleFrame{Type: "lifecycle_support", Capabilities: []string{"checkpoint", "interrupt"}})
	select {
	case <-lc.currentReady():
	case <-time.After(3 * time.Second):
		t.Fatal("handshake did not complete")
	}
	if !lc.Supports("checkpoint") {
		t.Error("Supports(checkpoint) = false, want true")
	}
	if lc.Supports("deadline_signal") {
		t.Error("Supports(deadline_signal) = true, want false (runtime did not declare it)")
	}
}

func TestLifecycleChannelCheckpointRoundTrip(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	errc := make(chan error, 1)
	go func() {
		errc <- lc.RequestCheckpoint(context.Background(), "ckpt-1", 5000)
	}()

	req := fr.read()
	if req.Type != "checkpoint_request" || req.CheckpointID != "ckpt-1" {
		t.Fatalf("request = %+v, want type checkpoint_request id ckpt-1", req)
	}
	if req.DeadlineMs != 5000 {
		t.Errorf("request deadlineMs = %d, want 5000", req.DeadlineMs)
	}
	fr.write(lifecycleFrame{Type: "checkpoint_ready", CheckpointID: "ckpt-1"})
	if err := <-errc; err != nil {
		t.Fatalf("RequestCheckpoint: %v", err)
	}

	if err := lc.CompleteCheckpoint("ckpt-1", "ok", ""); err != nil {
		t.Fatalf("CompleteCheckpoint: %v", err)
	}
	done := fr.read()
	if done.Type != "checkpoint_complete" || done.CheckpointID != "ckpt-1" || done.Status != "ok" {
		t.Errorf("checkpoint_complete = %+v, want id ckpt-1 status ok", done)
	}
}

func TestLifecycleChannelInterruptRoundTrip(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	errc := make(chan error, 1)
	go func() {
		errc <- lc.RequestInterrupt(context.Background(), "int-1", 2000)
	}()

	req := fr.read()
	if req.Type != "interrupt_request" || req.InterruptID != "int-1" {
		t.Fatalf("request = %+v, want type interrupt_request id int-1", req)
	}
	if req.DeadlineMs != 2000 {
		t.Errorf("interrupt deadlineMs = %d, want 2000", req.DeadlineMs)
	}
	fr.write(lifecycleFrame{Type: "interrupt_acknowledged", InterruptID: "int-1"})
	if err := <-errc; err != nil {
		t.Fatalf("RequestInterrupt: %v", err)
	}
}

func TestLifecycleChannelCredentialRotation(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	errc := make(chan error, 1)
	go func() {
		errc <- lc.RotateCredentials(context.Background(), "anthropic", "/run/lenny/credentials.json", "lease-9")
	}()

	req := fr.read()
	if req.Type != "credentials_rotated" || req.LeaseID != "lease-9" {
		t.Fatalf("request = %+v, want credentials_rotated lease-9", req)
	}
	if req.Provider != "anthropic" || req.CredentialsPath != "/run/lenny/credentials.json" {
		t.Errorf("credentials_rotated provider=%q credentialsPath=%q", req.Provider, req.CredentialsPath)
	}
	fr.write(lifecycleFrame{Type: "credentials_acknowledged", LeaseID: "lease-9", Provider: "anthropic"})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}
}

func TestLifecycleChannelDeadlineApproaching(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	if err := lc.SignalDeadlineApproaching(5000, "session_age"); err != nil {
		t.Fatalf("SignalDeadlineApproaching: %v", err)
	}
	got := fr.read()
	if got.Type != "deadline_approaching" || got.RemainingMs != 5000 || got.Trigger != "session_age" {
		t.Errorf("frame = %+v, want deadline_approaching remainingMs 5000 trigger session_age", got)
	}
}

func TestLifecycleChannelTerminate(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	if err := lc.Terminate(2000, "session_complete"); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	got := fr.read()
	if got.Type != "terminate" || got.DeadlineMs != 2000 || got.Reason != "session_complete" {
		t.Errorf("frame = %+v, want terminate deadlineMs 2000 reason session_complete", got)
	}
}

func TestLifecycleChannelCheckpointContextCancelled(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// The runtime never acknowledges, so the request unwinds on ctx.
	err := lc.RequestCheckpoint(ctx, "ckpt-stall", 5000)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RequestCheckpoint err = %v, want context.DeadlineExceeded", err)
	}
	if req := fr.read(); req.Type != "checkpoint_request" {
		t.Errorf("runtime saw %q, want checkpoint_request", req.Type)
	}
}

// TestLifecycleCapabilities_TaskMode_spec_4_7 verifies the adapter
// advertises task_lifecycle only on task-mode pods (spec line 686-694).
func TestLifecycleCapabilities_TaskMode_spec_4_7(t *testing.T) {
	base := &LifecycleChannel{}
	for _, c := range base.capabilities() {
		if c == taskLifecycleCapability {
			t.Fatalf("non-task-mode channel advertised %q", taskLifecycleCapability)
		}
	}

	task := &LifecycleChannel{taskMode: true}
	var found bool
	for _, c := range task.capabilities() {
		if c == taskLifecycleCapability {
			found = true
		}
	}
	if !found {
		t.Fatalf("task-mode channel did not advertise %q; caps=%v", taskLifecycleCapability, task.capabilities())
	}
}

// TestLifecycleChannelTaskCompleteRoundTrip_spec_4_7 drives the
// task_complete → task_complete_acknowledged exchange (spec line 678-708).
func TestLifecycleChannelTaskCompleteRoundTrip_spec_4_7(t *testing.T) {
	lc, fr := startLifecycleChannelOpts(t, func(lc *LifecycleChannel) { lc.taskMode = true })
	// The runtime declares task_lifecycle in its handshake reply.
	caps := fr.read()
	if caps.Type != "lifecycle_capabilities" {
		t.Fatalf("first frame = %q", caps.Type)
	}
	fr.write(lifecycleFrame{Type: "lifecycle_support", Capabilities: caps.Capabilities})

	errc := make(chan error, 1)
	go func() { errc <- lc.RequestTaskComplete(context.Background(), "task-1") }()

	req := fr.read()
	if req.Type != "task_complete" || req.TaskID != "task-1" {
		t.Fatalf("request = %+v, want task_complete task-1", req)
	}
	fr.write(lifecycleFrame{Type: "task_complete_acknowledged", TaskID: "task-1"})
	if err := <-errc; err != nil {
		t.Fatalf("RequestTaskComplete: %v", err)
	}

	if err := lc.SignalTaskReady("task-1"); err != nil {
		t.Fatalf("SignalTaskReady: %v", err)
	}
	ready := fr.read()
	if ready.Type != "task_ready" || ready.TaskID != "task-1" {
		t.Errorf("frame = %+v, want task_ready task-1", ready)
	}
}

// TestLifecycleChannelTaskCompleteAckTimeout_spec_4_7 verifies the
// 30s ack timeout (here shortened): on timeout the adapter logs,
// increments the counter, and proceeds (returns nil) per spec line 708.
func TestLifecycleChannelTaskCompleteAckTimeout_spec_4_7(t *testing.T) {
	lc, fr := startLifecycleChannelOpts(t, func(lc *LifecycleChannel) {
		lc.taskMode = true
		lc.taskCompleteAckTimeout = 80 * time.Millisecond
	})
	fr.handshake()

	before := testutil.ToFloat64(taskCompleteAckTimeout.WithLabelValues())
	// The runtime never acknowledges; the request must return nil (proceed).
	if err := lc.RequestTaskComplete(context.Background(), "task-stall"); err != nil {
		t.Fatalf("RequestTaskComplete on timeout = %v, want nil (proceed with cleanup)", err)
	}
	if req := fr.read(); req.Type != "task_complete" {
		t.Errorf("runtime saw %q, want task_complete", req.Type)
	}
	if got := testutil.ToFloat64(taskCompleteAckTimeout.WithLabelValues()); got != before+1 {
		t.Errorf("task_complete_ack_timeout_total = %v, want %v", got, before+1)
	}
}

// TestLifecycleChannelTaskCompleteCtxCancel_spec_4_7 verifies a caller
// cancellation (distinct from the ack timeout) surfaces as an error.
func TestLifecycleChannelTaskCompleteCtxCancel_spec_4_7(t *testing.T) {
	lc, fr := startLifecycleChannelOpts(t, func(lc *LifecycleChannel) { lc.taskMode = true })
	fr.handshake()

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- lc.RequestTaskComplete(ctx, "task-c") }()
	if req := fr.read(); req.Type != "task_complete" {
		t.Fatalf("runtime saw %q, want task_complete", req.Type)
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestTaskComplete err = %v, want context.Canceled", err)
	}
}

// TestLifecycleChannelInflightCounter_spec_4_7 verifies llm_request_-
// started / completed adjust the per-provider in-flight counter and that
// it never goes below zero (spec line 820).
func TestLifecycleChannelInflightCounter_spec_4_7(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	waitInflight := func(provider string, want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if lc.InflightCount(provider) == want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("InflightCount(%q) = %d, want %d", provider, lc.InflightCount(provider), want)
	}

	fr.write(lifecycleFrame{Type: "llm_request_started", RequestID: "r1", Provider: "anthropic"})
	fr.write(lifecycleFrame{Type: "llm_request_started", RequestID: "r2", Provider: "anthropic"})
	waitInflight("anthropic", 2)

	fr.write(lifecycleFrame{Type: "llm_request_completed", RequestID: "r1", Provider: "anthropic", Status: "ok"})
	waitInflight("anthropic", 1)

	// A spurious completion with no matching start floors at zero.
	fr.write(lifecycleFrame{Type: "llm_request_completed", RequestID: "r2", Provider: "anthropic", Status: "ok"})
	fr.write(lifecycleFrame{Type: "llm_request_completed", RequestID: "rx", Provider: "anthropic", Status: "error"})
	waitInflight("anthropic", 0)
}

func TestLifecycleChannelCloseFailsPendingRequest(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	errc := make(chan error, 1)
	go func() {
		errc <- lc.RequestInterrupt(context.Background(), "int-x", 2000)
	}()
	// Once the runtime has the request, RequestInterrupt is parked on the
	// acknowledgement; closing the channel must fail it rather than hang.
	if req := fr.read(); req.Type != "interrupt_request" {
		t.Fatalf("runtime saw %q, want interrupt_request", req.Type)
	}
	_ = lc.Close()
	if err := <-errc; !errors.Is(err, errLifecycleClosed) {
		t.Fatalf("RequestInterrupt err = %v, want errLifecycleClosed", err)
	}
}

// spec: §4.7 lines 836-842 — the startup sequence (runtime connects to
// the lifecycle channel) applies for both fresh sessions and resumes, so
// after a runtime disconnects the channel must accept a fresh runtime's
// connection and re-handshake. Closes F-4.7.14 / F-4.7.19.
func TestLifecycleChannelAcceptsReconnect_spec_4_7(t *testing.T) {
	dir, err := os.MkdirTemp("", "lc-reconnect-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "lifecycle.sock")

	lc, err := NewLifecycleChannel(sock)
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runErr := make(chan error, 1)
	go func() { runErr <- lc.Run(ctx) }()
	t.Cleanup(func() {
		_ = lc.Close()
		<-runErr
	})

	// First runtime: handshake, drive one request, then disconnect.
	dial := func() *fakeRuntime {
		conn, derr := net.Dial("unix", sock)
		if derr != nil {
			t.Fatalf("dial lifecycle socket: %v", derr)
		}
		return &fakeRuntime{t: t, conn: conn, r: bufio.NewReader(conn)}
	}

	roundTrip := func(fr *fakeRuntime, id string) {
		errc := make(chan error, 1)
		go func() { errc <- lc.RequestInterrupt(context.Background(), id, 2000) }()
		req := fr.read()
		if req.Type != "interrupt_request" || req.InterruptID != id {
			t.Fatalf("runtime saw %q/%q, want interrupt_request/%s", req.Type, req.InterruptID, id)
		}
		fr.write(lifecycleFrame{Type: "interrupt_acknowledged", InterruptID: id})
		if err := <-errc; err != nil {
			t.Fatalf("RequestInterrupt(%s) = %v", id, err)
		}
	}

	fr1 := dial()
	fr1.handshake()
	roundTrip(fr1, "int-1")
	fr1.conn.Close()

	// Second runtime dials the same socket (the Resume restart path) and
	// must complete a fresh handshake and serve a new request.
	fr2 := dial()
	fr2.handshake()
	select {
	case <-lc.currentReady():
	case <-time.After(3 * time.Second):
		t.Fatal("second handshake did not complete")
	}
	roundTrip(fr2, "int-2")
}
