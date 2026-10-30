//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// wireTap records every byte the runtime reads off its CH-MSGSOCK
// connection, so a case asserts the frame order the runtime received
// rather than the order the adapter's helpers were called in.
type wireTap struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *wireTap) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// frames returns the type and sessionId of each complete line read so far.
func (w *wireTap) frames(t *testing.T) []string {
	t.Helper()
	w.mu.Lock()
	raw := append([]byte(nil), w.buf.Bytes()...)
	w.mu.Unlock()
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		var f struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
			t.Fatalf("runtime read a line that is not JSON: %v (%s)", err, sc.Bytes())
		}
		out = append(out, f.Type+":"+f.SessionID)
	}
	return out
}

// dialEchoRuntime dials the adapter's runtime socket and runs the echocore
// loop over the connection, which ignores session_start and session_end
// under the unknown-type rule. Its first line is the nonce line carrying
// nonce. It returns the tap on the connection's inbound side and the
// channel the loop's exit error arrives on.
func dialEchoRuntime(t *testing.T, addr, nonce string) (*wireTap, <-chan error) {
	t.Helper()
	conn, err := net.Dial("unix", addr)
	if err != nil {
		t.Fatalf("dial runtime socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := runtimenonce.Write(conn, nonce); err != nil {
		t.Fatal(err)
	}
	tap := &wireTap{}
	exited := make(chan error, 1)
	go func() {
		exited <- echocore.Run(context.Background(), io.TeeReader(conn, tap), conn, io.Discard)
	}()
	return tap, exited
}

// runtimeSocketAddress returns a filesystem socket path under a short
// temporary directory, within the platform's sun_path limit.
func runtimeSocketAddress(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-wire-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "rt.sock")
}

// nextFrameOfType reads out until a frame of type want arrives.
func nextFrameOfType(t *testing.T, out <-chan []byte, want string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-out:
			if !ok {
				t.Fatalf("runtime output ended before a %s frame", want)
			}
			var m map[string]any
			if json.Unmarshal(line, &m) == nil && m["type"] == want {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s frame within 10s", want)
		}
	}
}

// sendMessage writes a message for sessionID on the runtime connection, as
// the Attach loop does, and reads its response.
func sendMessage(t *testing.T, sp *adapter.SocketRuntimeProcess, out <-chan []byte, sessionID string) {
	t.Helper()
	msg := fmt.Sprintf(`{"schemaVersion":1,"type":"message","id":"msg_%s","sessionId":%q,`+
		`"from":{"kind":"client","id":"c"},"input":[{"type":"text","inline":"ping"}]}`, sessionID, sessionID)
	if err := sp.WriteEnvelope(sessionID, []byte(msg)); err != nil {
		t.Fatalf("write message: %v", err)
	}
	if resp := nextFrameOfType(t, out, "response"); resp["sessionId"] != sessionID {
		t.Fatalf("response = %v, want one for %s", resp, sessionID)
	}
}

// spec: 28.5.3 (CH-MSGSOCK), 28.5.3 (CH-MSGSOCK Session frame writes), 4.7.10 (Runtime process lifetime)
// diagnosis: on the pod's one runtime connection the adapter wrote a
//
//	session's first message before its session_start, wrote a frame for a
//	session after its session_end, or wrote session frames a Basic runtime
//	that ignores them could not survive. A runtime that keeps per-session
//	context answers a message that precedes its session_start with a
//	session error, and one that receives a frame after session_end serves
//	it under a released context. A runtime that predates the session
//	frames treats both as unknown types and must still answer heartbeat and
//	exit on shutdown, so the frames cannot be a breaking change.
func TestSessionFramesBracketEachSessionOnOneRuntimeConnection_spec_28_5_3(t *testing.T) {
	addr := runtimeSocketAddress(t)
	// The listener requires the published manifest's nonce as the
	// connection's first line. spec: 4.7.11 (Runtime connection handshake).
	manifest := runtimenonce.Publish(t, nil)
	sp, err := adapter.NewSocketRuntimeProcess(addr, adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}, adapter.PublishedManifestNonce(manifest.Dir))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 10 * time.Second
	tap, exited := dialEchoRuntime(t, addr, manifest.Nonce)

	s := adapter.New("wire-order")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = sp
	ctx := context.Background()
	var out <-chan []byte
	for _, id := range []string{"sess-a", "sess-b"} {
		if _, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{SessionId: &adapterv1.SessionId{Value: id}}); err != nil {
			t.Fatalf("StartSession(%s): %v", id, err)
		}
		// The connection is installed by the first start; the runtime
		// writes nothing for a session_start it ignores, so subscribing
		// after it misses no frame.
		if out == nil {
			if out, err = sp.Output(ctx, ""); err != nil {
				t.Fatalf("Output: %v", err)
			}
		}
		sendMessage(t, sp, out, id)
		if err := sp.WriteEnvelope(id, []byte(`{"type":"heartbeat","ts":1}`)); err != nil {
			t.Fatalf("write heartbeat: %v", err)
		}
		nextFrameOfType(t, out, "heartbeat_ack")
		if _, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: id}, UnconditionalTeardown: true,
		}); err != nil {
			t.Fatalf("Shutdown(%s): %v", id, err)
		}
	}
	// shutdown is process-scoped: the runtime that ignored every session
	// frame still exits on it, cleanly.
	if err := sp.WriteEnvelope("", []byte(`{"type":"shutdown","reason":"drain","deadline_ms":1000}`)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("runtime exit on shutdown = %v, want a clean exit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the runtime did not exit on shutdown")
	}

	want := []string{
		"session_start:sess-a", "message:sess-a", "heartbeat:", "session_end:sess-a",
		"session_start:sess-b", "message:sess-b", "heartbeat:", "session_end:sess-b",
		"shutdown:",
	}
	got := tap.frames(t)
	if len(got) != len(want) {
		t.Fatalf("runtime read %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runtime read %v, want %v", got, want)
		}
	}
}
