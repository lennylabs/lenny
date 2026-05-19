// SPDX-License-Identifier: MIT

package runtime

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeLifecycleAdapter is the adapter side of a §15.4.3 lifecycle
// channel: it listens on a Unix socket, sends lifecycle_capabilities on
// connect, and exposes send/recv for the test to drive events.
type fakeLifecycleAdapter struct {
	ln   net.Listener
	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
}

// startFakeLifecycle listens on a lifecycle socket under dir.
func startFakeLifecycle(t *testing.T, dir string) *fakeLifecycleAdapter {
	t.Helper()
	sock := filepath.Join(dir, "lc.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen %s: %v", sock, err)
	}
	fa := &fakeLifecycleAdapter{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go fa.accept()
	return fa
}

func (fa *fakeLifecycleAdapter) socket() string { return fa.ln.Addr().String() }

func (fa *fakeLifecycleAdapter) accept() {
	conn, err := fa.ln.Accept()
	if err != nil {
		return
	}
	fa.mu.Lock()
	fa.conn = conn
	fa.r = bufio.NewReader(conn)
	fa.mu.Unlock()
	// §15.4.3 handshake: the adapter opens with lifecycle_capabilities.
	enc := json.NewEncoder(conn)
	_ = enc.Encode(map[string]any{
		"type":         "lifecycle_capabilities",
		"capabilities": []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"},
	})
}

// connected reports whether the runtime has dialed the channel.
func (fa *fakeLifecycleAdapter) connected() bool {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.conn != nil
}

// send writes a frame to the runtime.
func (fa *fakeLifecycleAdapter) send(t *testing.T, v any) {
	t.Helper()
	fa.mu.Lock()
	conn := fa.conn
	fa.mu.Unlock()
	if conn == nil {
		t.Fatal("lifecycle channel not connected")
	}
	if err := json.NewEncoder(conn).Encode(v); err != nil {
		t.Fatalf("lifecycle send: %v", err)
	}
}

// recv reads one frame the runtime wrote on the channel.
func (fa *fakeLifecycleAdapter) recv(t *testing.T, d time.Duration) map[string]any {
	t.Helper()
	fa.mu.Lock()
	conn, r := fa.conn, fa.r
	fa.mu.Unlock()
	if conn == nil {
		t.Fatal("lifecycle channel not connected")
	}
	_ = conn.SetReadDeadline(time.Now().Add(d))
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		t.Fatalf("lifecycle recv: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("lifecycle frame not JSON: %v (line %q)", err, line)
	}
	return m
}

// writeFullManifest writes a §4.7 manifest advertising a lifecycle
// channel socket.
func writeFullManifest(t *testing.T, dir, lifecycleSock string) string {
	t.Helper()
	path := filepath.Join(dir, "adapter-manifest.json")
	body, _ := json.Marshal(map[string]any{
		"version":          1,
		"sessionId":        "sess_full",
		"taskId":           "task_full",
		"mcpNonce":         "nonce_full",
		"lifecycleChannel": map[string]any{"socket": lifecycleSock},
	})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// stdinPipe is a held-open stdin for a Full-level runtime test: the
// scanner blocks on Read until close is called, at which point Read
// returns EOF. It mirrors how the adapter holds stdin open and closes
// it to drive runtime exit.
type stdinPipe struct {
	mu     sync.Mutex
	closed bool
	cond   *sync.Cond
}

func newStdinPipe() *stdinPipe {
	p := &stdinPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *stdinPipe) Read([]byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for !p.closed {
		p.cond.Wait()
	}
	return 0, io.EOF
}

func (p *stdinPipe) Close() error {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	return nil
}

// TestFullLevelHandshake confirms a Full-level runtime dials the
// lifecycle channel and completes the lifecycle_support handshake.
func TestFullLevelHandshake(t *testing.T) {
	dir := t.TempDir()
	fa := startFakeLifecycle(t, dir)
	manifest := writeFullManifest(t, dir, fa.socket())

	stdin := newStdinPipe()
	done := make(chan error, 1)
	go func() {
		done <- Run(&echoHandler{}, WithStreams(stdin, &syncBuffer{}),
			WithLogger(nil), WithSocketTransport(false),
			WithFullLevel(), WithManifestPath(manifest))
	}()

	if !waitFor(t, 3*time.Second, fa.connected) {
		t.Fatal("runtime did not dial the lifecycle channel")
	}
	support := fa.recv(t, 3*time.Second)
	if support["type"] != "lifecycle_support" {
		t.Fatalf("handshake reply = %v, want lifecycle_support", support)
	}
	caps, _ := support["capabilities"].([]any)
	if len(caps) == 0 {
		t.Fatal("lifecycle_support carries no capabilities")
	}

	// Drive a terminate event, then close stdin as the adapter would.
	// The runtime exits cleanly.
	fa.send(t, map[string]any{"type": "terminate", "reason": "done", "deadlineMs": 1000})
	stdin.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after terminate", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after lifecycle terminate")
	}
}

// TestFullLevelCheckpoint confirms the SDK answers a checkpoint_request
// with checkpoint_ready and runs the registered OnCheckpoint callback.
func TestFullLevelCheckpoint(t *testing.T) {
	dir := t.TempDir()
	fa := startFakeLifecycle(t, dir)
	manifest := writeFullManifest(t, dir, fa.socket())

	var checkpointed string
	stdin := newStdinPipe()
	done := make(chan error, 1)
	go func() {
		done <- Run(&echoHandler{}, WithStreams(stdin, &syncBuffer{}),
			WithLogger(nil), WithSocketTransport(false),
			WithManifestPath(manifest),
			WithLifecycleHandlers(OnCheckpoint(func(id string) error {
				checkpointed = id
				return nil
			})))
	}()

	if !waitFor(t, 3*time.Second, fa.connected) {
		t.Fatal("runtime did not dial the lifecycle channel")
	}
	_ = fa.recv(t, 3*time.Second) // lifecycle_support

	fa.send(t, map[string]any{"type": "checkpoint_request", "checkpointId": "ckpt_1", "deadlineMs": 5000})
	ready := fa.recv(t, 3*time.Second)
	if ready["type"] != "checkpoint_ready" || ready["checkpointId"] != "ckpt_1" {
		t.Fatalf("checkpoint reply = %v, want checkpoint_ready ckpt_1", ready)
	}
	if checkpointed != "ckpt_1" {
		t.Fatalf("OnCheckpoint callback got %q, want ckpt_1", checkpointed)
	}

	fa.send(t, map[string]any{"type": "terminate", "reason": "done"})
	stdin.Close()
	<-done
}

// TestFullLevelInterrupt confirms the SDK answers an interrupt_request
// with interrupt_acknowledged carrying the original interruptId.
func TestFullLevelInterrupt(t *testing.T) {
	dir := t.TempDir()
	fa := startFakeLifecycle(t, dir)
	manifest := writeFullManifest(t, dir, fa.socket())

	stdin := newStdinPipe()
	done := make(chan error, 1)
	go func() {
		done <- Run(&echoHandler{}, WithStreams(stdin, &syncBuffer{}),
			WithLogger(nil), WithSocketTransport(false),
			WithFullLevel(), WithManifestPath(manifest))
	}()

	if !waitFor(t, 3*time.Second, fa.connected) {
		t.Fatal("runtime did not dial the lifecycle channel")
	}
	_ = fa.recv(t, 3*time.Second) // lifecycle_support

	fa.send(t, map[string]any{"type": "interrupt_request", "interruptId": "int_7", "deadlineMs": 2000})
	ack := fa.recv(t, 3*time.Second)
	if ack["type"] != "interrupt_acknowledged" || ack["interruptId"] != "int_7" {
		t.Fatalf("interrupt reply = %v, want interrupt_acknowledged int_7", ack)
	}

	fa.send(t, map[string]any{"type": "terminate", "reason": "done"})
	stdin.Close()
	<-done
}
