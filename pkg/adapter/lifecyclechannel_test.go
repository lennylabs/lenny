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
	"sync"
	"testing"
	"time"
)

// fakeRuntimeReadDeadline bounds a single fakeRuntime read. The frames
// travel an in-process loopback Unix socket, so a multi-second wait is
// only ever scheduler starvation under the whole-repo parallel test run,
// never a genuine protocol stall. The deadline guards against a true
// hang (the read still fails with a clear message); the generous value
// keeps the no-op rotation-gate pass-through and the handshake reads from
// timing out when 28+ packages contend for cores. spec: §4.7.
const fakeRuntimeReadDeadline = 30 * time.Second

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
	_ = fr.conn.SetReadDeadline(time.Now().Add(fakeRuntimeReadDeadline))
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

// shortSocketName returns a Unix socket path under a short temp directory.
// t.TempDir() embeds the (often long) test name, so a socket derived from it
// can overflow the platform sun_path limit (~104 bytes on darwin); binding
// under os.MkdirTemp's short root keeps the path within that limit.
func shortSocketName(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-sock-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// startLifecycleChannel creates a LifecycleChannel on a temporary
// socket, runs it, and dials it as a fake runtime. Run and the
// connection are torn down on test cleanup.
func startLifecycleChannel(t *testing.T) (*LifecycleChannel, *fakeRuntime) {
	t.Helper()
	sock := shortSocketName(t, "lifecycle.sock")

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

// recordingSink is a tokenSink that sums the folded token counts.
type recordingSink struct {
	mu     sync.Mutex
	input  int64
	output int64
	calls  int
}

func (r *recordingSink) AddTokens(input, output int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.input += input
	r.output += output
	r.calls++
}

func (r *recordingSink) totals() (int64, int64, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.input, r.output, r.calls
}

// spec: §4.7 (llm_request_completed token fields), §11.2 (direct-mode usage)
// An llm_request_completed frame carrying inputTokens/outputTokens folds
// those counts into the wired usage sink, and a frame without token fields
// folds nothing (the runtime that cannot extract counts, a zero delta).
// This pins the S3 fold that did not exist before this step.
func TestLifecycleChannelFoldsCompletedTokens_spec_11_2(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	sink := &recordingSink{}
	lc.SetUsageSink(sink)
	fr.handshake()

	waitCalls := func(want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, _, calls := sink.totals(); calls == want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		_, _, calls := sink.totals()
		t.Fatalf("sink calls = %d, want %d", calls, want)
	}

	fr.write(lifecycleFrame{
		Type: "llm_request_completed", RequestID: "r1", Provider: "anthropic",
		Status: "ok", InputTokens: 40, OutputTokens: 12,
	})
	fr.write(lifecycleFrame{
		Type: "llm_request_completed", RequestID: "r2", Provider: "anthropic",
		Status: "ok", InputTokens: 10, OutputTokens: 3,
	})
	waitCalls(2)

	// A frame with no token fields folds nothing (no extra sink call).
	fr.write(lifecycleFrame{
		Type: "llm_request_completed", RequestID: "r3", Provider: "anthropic", Status: "ok",
	})
	// Give the read loop a moment; the call count must stay at 2.
	time.Sleep(20 * time.Millisecond)

	input, output, calls := sink.totals()
	if input != 50 || output != 15 {
		t.Fatalf("folded totals = (%d,%d), want (50,15)", input, output)
	}
	if calls != 2 {
		t.Fatalf("sink calls = %d, want 2 (a token-less frame must not fold)", calls)
	}
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

// spec: §15.5 line 2431 (item 3) — when the runtime advertises a
// matching `protocolVersion` in `lifecycle_support`, the handshake
// completes and the channel exposes the runtime's capability set.
// F-15.5.9.
func TestLifecycleVersionNegotiation_AcceptsCompatible_spec_15_5_2431(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	caps := fr.read()
	if caps.Type != "lifecycle_capabilities" {
		t.Fatalf("first frame = %q, want lifecycle_capabilities", caps.Type)
	}
	fr.write(lifecycleFrame{
		Type:            "lifecycle_support",
		ProtocolVersion: "1.7",
		Capabilities:    caps.Capabilities,
	})
	select {
	case <-lc.currentReady():
	case <-time.After(3 * time.Second):
		t.Fatal("compatible-version handshake did not complete")
	}
}

// spec: §15.5 line 2431 (item 3) — an empty `protocolVersion` is
// accepted for forward compatibility with runtimes that pre-date the
// field. F-15.5.9.
func TestLifecycleVersionNegotiation_AcceptsEmpty_spec_15_5_2431(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	caps := fr.read()
	if caps.Type != "lifecycle_capabilities" {
		t.Fatalf("first frame = %q, want lifecycle_capabilities", caps.Type)
	}
	fr.write(lifecycleFrame{
		Type:         "lifecycle_support",
		Capabilities: caps.Capabilities,
	})
	select {
	case <-lc.currentReady():
	case <-time.After(3 * time.Second):
		t.Fatal("missing-version handshake did not complete")
	}
}

// spec: §15.5 line 2431 (item 3) — a runtime advertising a different
// major version is rejected; the handshake never marks the channel
// ready. F-15.5.9.
func TestLifecycleVersionNegotiation_RejectsIncompatibleMajor_spec_15_5_2431(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	caps := fr.read()
	if caps.Type != "lifecycle_capabilities" {
		t.Fatalf("first frame = %q, want lifecycle_capabilities", caps.Type)
	}
	fr.write(lifecycleFrame{
		Type:            "lifecycle_support",
		ProtocolVersion: "2.0",
		Capabilities:    caps.Capabilities,
	})
	select {
	case <-lc.currentReady():
		t.Fatal("incompatible-version handshake unexpectedly completed")
	case <-time.After(250 * time.Millisecond):
		// Expected: connection was dropped before ready fires.
	}
}

// spec: §15.5 line 2431 (item 3) — malformed `protocolVersion` strings
// (no dot, leading dot, non-numeric prefix) are rejected so a typo
// surfaces at handshake. F-15.5.9.
func TestLifecycleVersionsCompatible_MalformedRejected(t *testing.T) {
	cases := []struct {
		runtime string
		want    bool
	}{
		{"1.0", true},
		{"1.7", true},
		{"2.0", false},
		{"", false}, // direct compat predicate test; the handshake handles "" separately.
		{"abc", false},
		{".5", false},
		{"1", false},
		{"1.0.0", true}, // extra trailing segments still parse to major=1 via the first dot.
	}
	for _, tc := range cases {
		if got := lifecycleVersionsCompatible(lifecycleProtocolVersion, tc.runtime); got != tc.want {
			t.Errorf("lifecycleVersionsCompatible(%q, %q) = %v, want %v", lifecycleProtocolVersion, tc.runtime, got, tc.want)
		}
	}
}
