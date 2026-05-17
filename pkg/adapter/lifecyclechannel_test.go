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
)

// fakeRuntime plays the agent-runtime end of the §15.4.6 lifecycle
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
	fr.write(lifecycleFrame{Type: "lifecycle_support", Supported: caps.Capabilities})
}

// startLifecycleChannel creates a LifecycleChannel on a temporary
// socket, runs it, and dials it as a fake runtime. Run and the
// connection are torn down on test cleanup.
func startLifecycleChannel(t *testing.T) (*LifecycleChannel, *fakeRuntime) {
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
	fr.write(lifecycleFrame{Type: "lifecycle_support", Supported: []string{"checkpoint", "interrupt"}})
	select {
	case <-lc.ready:
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
		errc <- lc.RequestCheckpoint(context.Background(), "ckpt-1", 5000, "full")
	}()

	req := fr.read()
	if req.Type != "checkpoint_request" || req.CheckpointID != "ckpt-1" {
		t.Fatalf("request = %+v, want type checkpoint_request id ckpt-1", req)
	}
	if req.DeadlineMs != 5000 || req.Level != "full" {
		t.Errorf("request deadline=%d level=%q, want 5000/full", req.DeadlineMs, req.Level)
	}
	fr.write(lifecycleFrame{Type: "checkpoint_ready", CheckpointID: "ckpt-1"})
	if err := <-errc; err != nil {
		t.Fatalf("RequestCheckpoint: %v", err)
	}

	if err := lc.CompleteCheckpoint("ckpt-1", "success"); err != nil {
		t.Fatalf("CompleteCheckpoint: %v", err)
	}
	done := fr.read()
	if done.Type != "checkpoint_complete" || done.CheckpointID != "ckpt-1" || done.Outcome != "success" {
		t.Errorf("checkpoint_complete = %+v, want id ckpt-1 outcome success", done)
	}
}

func TestLifecycleChannelInterruptRoundTrip(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	errc := make(chan error, 1)
	go func() {
		errc <- lc.RequestInterrupt(context.Background(), "int-1", 2000, "drain")
	}()

	req := fr.read()
	if req.Type != "interrupt_request" || req.InterruptID != "int-1" {
		t.Fatalf("request = %+v, want type interrupt_request id int-1", req)
	}
	if req.Reason != "drain" {
		t.Errorf("interrupt reason = %q, want drain", req.Reason)
	}
	fr.write(lifecycleFrame{Type: "interrupt_acknowledged", InterruptID: "int-1"})
	if err := <-errc; err != nil {
		t.Fatalf("RequestInterrupt: %v", err)
	}
}

func TestLifecycleChannelCredentialsRotated(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	if err := lc.NotifyCredentialsRotated("lease-9", "proactive_renewal"); err != nil {
		t.Fatalf("NotifyCredentialsRotated: %v", err)
	}
	got := fr.read()
	if got.Type != "credentials_rotated" || got.LeaseID != "lease-9" || got.Trigger != "proactive_renewal" {
		t.Errorf("frame = %+v, want credentials_rotated lease-9 proactive_renewal", got)
	}
}

func TestLifecycleChannelDeadlineSignal(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	if err := lc.SignalDeadline(1000); err != nil {
		t.Fatalf("SignalDeadline: %v", err)
	}
	got := fr.read()
	if got.Type != "deadline_signal" || got.DeadlineMs != 1000 {
		t.Errorf("frame = %+v, want deadline_signal deadline_ms 1000", got)
	}
}

func TestLifecycleChannelCheckpointContextCancelled(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// The runtime never acknowledges, so the request unwinds on ctx.
	err := lc.RequestCheckpoint(ctx, "ckpt-stall", 5000, "full")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RequestCheckpoint err = %v, want context.DeadlineExceeded", err)
	}
	if req := fr.read(); req.Type != "checkpoint_request" {
		t.Errorf("runtime saw %q, want checkpoint_request", req.Type)
	}
}

func TestLifecycleChannelCloseFailsPendingRequest(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	errc := make(chan error, 1)
	go func() {
		errc <- lc.RequestInterrupt(context.Background(), "int-x", 2000, "shutdown")
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
