// SPDX-License-Identifier: MIT

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeLifecycleAdapter is the adapter side of a §15.4.3 CH-RUNTIMEOPS: it listens on a Unix socket, sends lifecycle_capabilities on
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
		t.Fatal("the runtime has not connected to CH-RUNTIMEOPS")
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
		t.Fatal("the runtime has not connected to CH-RUNTIMEOPS")
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

// writeFullManifest writes a §4.7 manifest advertising a CH-RUNTIMEOPS socket.
func writeFullManifest(t *testing.T, dir, lifecycleSock string) string {
	t.Helper()
	path := filepath.Join(dir, "adapter-manifest.json")
	body, _ := json.Marshal(map[string]any{
		"version":    1,
		"mcpNonce":   "nonce_full",
		"runtimeOps": map[string]any{"socket": lifecycleSock},
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
// CH-RUNTIMEOPS and completes the lifecycle_support handshake.
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
		t.Fatal("runtime did not dial CH-RUNTIMEOPS")
	}
	support := fa.recv(t, 3*time.Second)
	if support["type"] != "lifecycle_support" {
		t.Fatalf("handshake reply = %v, want lifecycle_support", support)
	}
	caps, _ := support["capabilities"].([]any)
	if len(caps) == 0 {
		t.Fatal("lifecycle_support carries no capabilities")
	}

	// Close stdin as the adapter would. The runtime ends on stdin EOF,
	// because no CH-RUNTIMEOPS frame ends the process.
	stdin.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after stdin EOF", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after stdin EOF")
	}
}

// spec: 4.7.10 (Runtime process lifetime), 28.5.3 (CH-RUNTIMEOPS Messages)
//
// A frame typed terminate on CH-RUNTIMEOPS is an unknown frame: the SDK
// writes no response for it, keeps serving the held session, and still
// answers a heartbeat. Only shutdown or stdin EOF ends the process, so a
// runtime that ended on terminate would leave every other session on the
// pod without a process.
func TestRuntimeOpsTerminateFrameDoesNotEndTheProcess_spec_4_7_10(t *testing.T) {
	l, fa := openFullSession(t, &recorder{}, nil, "sess_a")
	fa.send(t, map[string]any{"type": "terminate", "sessionId": "sess_a", "reason": "done", "deadlineMs": 1000})
	l.send(`{"type":"heartbeat","ts":1}`)
	if f := l.next(3 * time.Second); f["type"] != "heartbeat_ack" {
		t.Fatalf("frame after a CH-RUNTIMEOPS terminate = %v, want heartbeat_ack: the SDK answered the frame or stopped serving", f)
	}
	l.send(msgFrame("sess_a", "m_after", "x"))
	if f := l.next(3 * time.Second); f["type"] != "response" || f["sessionId"] != "sess_a" || f["error"] != nil {
		t.Fatalf("reply to sess_a's message after a CH-RUNTIMEOPS terminate = %v, want its response", f)
	}
}

// openFullSession starts a Full-level runtime over live pipes, waits for
// its CH-RUNTIMEOPS handshake, and opens each of sessionIDs, returning
// once every session's session_started was read.
func openFullSession(t *testing.T, h Handler, hooks []LifecycleOption, sessionIDs ...string) (*liveSDK, *fakeLifecycleAdapter) {
	t.Helper()
	dir := t.TempDir()
	fa := startFakeLifecycle(t, dir)
	manifest := writeFullManifest(t, dir, fa.socket())
	l := startLiveSDK(t, h, WithManifestPath(manifest), WithFullLevel(), WithLifecycleHandlers(hooks...))
	if !waitFor(t, 3*time.Second, fa.connected) {
		t.Fatal("runtime did not dial CH-RUNTIMEOPS")
	}
	if support := fa.recv(t, 3*time.Second); support["type"] != "lifecycle_support" {
		t.Fatalf("handshake reply = %v, want lifecycle_support", support)
	}
	for i, id := range sessionIDs {
		l.send(startFrame(id, fmt.Sprintf("st_%d", i)))
		if f := l.next(3 * time.Second); f["type"] != "session_started" || f["sessionId"] != id {
			t.Fatalf("frame after session_start(%s) = %v, want its session_started", id, f)
		}
	}
	return l, fa
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages), 15.7 (Runtime Author SDKs)
//
// The SDK answers a checkpoint_request naming a held session with
// checkpoint_ready, and the OnCheckpoint callback receives the session
// the frame names.
func TestFullLevelCheckpoint(t *testing.T) {
	var mu sync.Mutex
	var gotSession, gotCheckpoint string
	_, fa := openFullSession(t, &recorder{}, []LifecycleOption{OnCheckpoint(func(sessionID, id string) error {
		mu.Lock()
		gotSession, gotCheckpoint = sessionID, id
		mu.Unlock()
		return nil
	})}, "sess_a")

	fa.send(t, map[string]any{"type": "checkpoint_request", "sessionId": "sess_a", "checkpointId": "ckpt_1", "deadlineMs": 5000})
	ready := fa.recv(t, 3*time.Second)
	if ready["type"] != "checkpoint_ready" || ready["checkpointId"] != "ckpt_1" {
		t.Fatalf("checkpoint reply = %v, want checkpoint_ready ckpt_1", ready)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotSession != "sess_a" || gotCheckpoint != "ckpt_1" {
		t.Fatalf("OnCheckpoint callback got (%q, %q), want (sess_a, ckpt_1)", gotSession, gotCheckpoint)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages), 15.7 (Runtime Author SDKs)
//
// The SDK answers an interrupt_request naming a held session with
// interrupt_acknowledged carrying the original interruptId, and the
// OnInterrupt callback receives the session.
func TestFullLevelInterrupt(t *testing.T) {
	var mu sync.Mutex
	var gotSession string
	_, fa := openFullSession(t, &recorder{}, []LifecycleOption{OnInterrupt(func(sessionID, _ string) error {
		mu.Lock()
		gotSession = sessionID
		mu.Unlock()
		return nil
	})}, "sess_a")

	fa.send(t, map[string]any{"type": "interrupt_request", "sessionId": "sess_a", "interruptId": "int_7", "deadlineMs": 2000})
	ack := fa.recv(t, 3*time.Second)
	if ack["type"] != "interrupt_acknowledged" || ack["interruptId"] != "int_7" {
		t.Fatalf("interrupt reply = %v, want interrupt_acknowledged int_7", ack)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotSession != "sess_a" {
		t.Fatalf("OnInterrupt callback got session %q, want sess_a", gotSession)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages)
//
// A session-scoped event naming a session the runtime does not hold, one
// naming a session that ended, and one naming a session whose context
// creation failed are each dropped without a reply. The reply to a later
// event for a held session is the next frame on the channel, which shows
// the earlier events produced none.
func TestLifecycleEventsForUnheldSessionsAreDroppedWithoutReply(t *testing.T) {
	h := &recorder{createGate: func(_ context.Context, req CreateRequest) error {
		if req.SessionID == "sess_bad" {
			return errors.New("context unavailable")
		}
		return nil
	}}
	l, fa := openFullSession(t, h, nil, "sess_a", "sess_ended", "sess_bad")
	l.send(endFrame("sess_ended"))
	if !waitUntil(3*time.Second, func() bool { return len(h.terminations("sess_ended")) == 1 }) {
		t.Fatal("sess_ended was not released")
	}

	for _, id := range []string{"sess_unknown", "sess_ended", "sess_bad", ""} {
		fa.send(t, map[string]any{"type": "checkpoint_request", "sessionId": id, "checkpointId": "drop_" + id, "deadlineMs": 1000})
		fa.send(t, map[string]any{"type": "interrupt_request", "sessionId": id, "interruptId": "drop_" + id, "deadlineMs": 1000})
	}
	// One-way frames for a held session and an unknown frame type get no
	// reply either.
	fa.send(t, map[string]any{"type": "files_updated", "sessionId": "sess_a"})
	fa.send(t, map[string]any{"type": "checkpoint_complete", "sessionId": "sess_a", "checkpointId": "c0", "status": "ok"})
	fa.send(t, map[string]any{"type": "some_future_event", "sessionId": "sess_a"})
	fa.send(t, map[string]any{"type": "checkpoint_request", "sessionId": "sess_a", "checkpointId": "ckpt_kept", "deadlineMs": 1000})
	reply := fa.recv(t, 3*time.Second)
	if reply["type"] != "checkpoint_ready" || reply["checkpointId"] != "ckpt_kept" {
		t.Fatalf("first CH-RUNTIMEOPS reply = %v, want checkpoint_ready for ckpt_kept: an event for an unheld session was answered", reply)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages, credentials_rotated), 4.7.11
// (item 4), 4.7.10 (Runtime process lifetime)
//
// A credentials_rotated event naming session A reloads A's bundle from
// the file the event names, hands the callback A's identifier, and leaves
// session B's bundle unchanged.
func TestCredentialsRotatedReloadsOnlyTheNamedSession(t *testing.T) {
	dir := t.TempDir()
	pathA := writeProviderBundle(t, dir, "a.json", "anthropic")
	pathB := writeProviderBundle(t, dir, "b.json", "openai")
	rotatedA := writeProviderBundle(t, dir, "a-rotated.json", "rotated")

	fdir := t.TempDir()
	fa := startFakeLifecycle(t, fdir)
	manifest := writeFullManifest(t, fdir, fa.socket())
	rotated := make(chan [2]string, 2)
	h := &credentialEcho{}
	l := startLiveSDK(t, h, WithManifestPath(manifest), WithLifecycleHandlers(
		OnCredentialsRotated(func(sessionID string, c *CredentialBundle) {
			rotated <- [2]string{sessionID, providerOf(c)}
		}),
	))
	if !waitFor(t, 3*time.Second, fa.connected) {
		t.Fatal("runtime did not dial CH-RUNTIMEOPS")
	}
	_ = fa.recv(t, 3*time.Second)
	l.send(fmt.Sprintf(`{"type":"session_start","sessionId":"sess_a","startId":"st_a","credentialsPath":%q}`, pathA))
	l.send(fmt.Sprintf(`{"type":"session_start","sessionId":"sess_b","startId":"st_b","credentialsPath":%q}`, pathB))
	_ = l.next(3 * time.Second)
	_ = l.next(3 * time.Second)

	fa.send(t, map[string]any{
		"type": "credentials_rotated", "sessionId": "sess_a", "provider": "rotated",
		"credentialsPath": rotatedA, "leaseId": "lease_rot",
	})
	if ack := fa.recv(t, 3*time.Second); ack["type"] != "credentials_acknowledged" || ack["leaseId"] != "lease_rot" {
		t.Fatalf("rotation reply = %v, want credentials_acknowledged lease_rot", ack)
	}
	if got := <-rotated; got != [2]string{"sess_a", "rotated"} {
		t.Fatalf("OnCredentialsRotated got %v, want [sess_a rotated]", got)
	}

	l.send(msgFrame("sess_a", "m_a", "x"))
	l.send(msgFrame("sess_b", "m_b", "x"))
	got := map[string]string{}
	for i := 0; i < 2; i++ {
		f := l.next(3 * time.Second)
		got[f["sessionId"].(string)] = firstOutput(f)
	}
	if got["sess_a"] != "rotated" || got["sess_b"] != "openai" {
		t.Fatalf("providers after rotating sess_a = %v, want sess_a=rotated and sess_b=openai", got)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages)
//
// A deadline_approaching event reaches the OnDeadline callback with the
// session the frame names.
func TestDeadlineEventCarriesTheNamedSession(t *testing.T) {
	events := make(chan LifecycleEvent, 1)
	_, fa := openFullSession(t, &recorder{}, []LifecycleOption{OnDeadline(func(ev LifecycleEvent) { events <- ev })}, "sess_a")
	fa.send(t, map[string]any{"type": "deadline_approaching", "sessionId": "sess_a", "remainingMs": 1000, "trigger": "budget"})
	select {
	case ev := <-events:
		if ev.Type != "deadline_approaching" || ev.SessionID != "sess_a" {
			t.Fatalf("OnDeadline got %+v, want deadline_approaching for sess_a", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnDeadline did not run")
	}
}

// credentialEcho answers each message with the provider of the session's
// credential bundle carried on the handler context.
type credentialEcho struct{ recorder }

func (h *credentialEcho) OnMessage(ctx context.Context, _ Message) (Reply, error) {
	return TextReply(providerOf(CredentialsFrom(ctx))), nil
}

// providerOf returns the first providers entry's provider, or "none".
func providerOf(c *CredentialBundle) string {
	if c == nil || len(c.Providers) == 0 {
		return "none"
	}
	return c.Providers[0].Provider
}

// writeProviderBundle writes a providers-layout credential file under
// dir and returns its path.
func writeProviderBundle(t *testing.T, dir, name, provider string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body, _ := json.Marshal(map[string]any{"providers": []map[string]any{{
		"leaseId": "lease_" + provider, "provider": provider, "expiresAt": "2026-10-05T00:00:00Z",
		"deliveryMode": "direct", "materializedConfig": map[string]any{"apiKey": "sk-" + provider},
	}}})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	return path
}
