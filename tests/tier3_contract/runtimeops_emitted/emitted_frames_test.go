//go:build contract

// SPDX-License-Identifier: MIT

// Package runtimeops_emitted_test is the Tier 3 contract suite for the
// session-scoped CH-RUNTIMEOPS frames the adapter writes. It drives each
// adapter-to-runtime sender of the production RuntimeOps over a real Unix
// socket, reads the frame the runtime receives, and validates it against
// the published schemas/runtime-ops-events.schema.json, which requires a
// non-empty sessionId on every session-scoped frame. It also asserts that
// each frame names the session the sender was given, so a multi-session
// runtime can tell which session a checkpoint quiesces, which session to
// interrupt, and which session's credentials rotated.
//
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
package runtimeops_emitted_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	runtimeOpsArtifact = "schemas/runtime-ops-events.schema.json"
	// sessionIDFixture is the session every sender is given.
	sessionIDFixture = "sess_01HX9F0YWXKK0V7QZ7G6P3R5JN"
)

// runtimePeer is the runtime end of CH-RUNTIMEOPS.
type runtimePeer struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

// read returns the next frame the adapter wrote, raw and decoded.
func (p *runtimePeer) read() map[string]any {
	p.t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := p.r.ReadBytes('\n')
	if err != nil {
		p.t.Fatalf("read CH-RUNTIMEOPS frame: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		p.t.Fatalf("decode CH-RUNTIMEOPS frame %q: %v", line, err)
	}
	return m
}

// write sends one frame to the adapter.
func (p *runtimePeer) write(m map[string]any) {
	p.t.Helper()
	if err := json.NewEncoder(p.conn).Encode(m); err != nil {
		p.t.Fatalf("write CH-RUNTIMEOPS frame: %v", err)
	}
}

// startRuntimeOps runs the adapter's RuntimeOps on a short socket path,
// dials it as the runtime, and completes the capability handshake.
func startRuntimeOps(t *testing.T) (*adapter.RuntimeOps, *runtimePeer) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-rtops-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "lc.sock")
	lc, err := adapter.NewRuntimeOps(sock, adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- lc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = lc.Close()
		<-done
	})
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial CH-RUNTIMEOPS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	p := &runtimePeer{t: t, conn: conn, r: bufio.NewReader(conn)}
	caps := p.read()
	if caps["type"] != "lifecycle_capabilities" {
		t.Fatalf("first frame = %v, want lifecycle_capabilities", caps)
	}
	p.write(map[string]any{"type": "lifecycle_support", "capabilities": caps["capabilities"]})
	if !lc.WaitHandshake(context.Background(), 5*time.Second) {
		t.Fatal("CH-RUNTIMEOPS handshake did not complete")
	}
	return lc, p
}

// spec: 28.5.3 (CH-RUNTIMEOPS, Messages)
//
// diagnosis: a session-scoped frame the adapter writes on CH-RUNTIMEOPS
// does not validate against the published runtime-ops schema or does not
// name the session its sender was given. A multi-session runtime then
// cannot tell which session the frame concerns: a checkpoint quiesces the
// wrong session, an interrupt stops the wrong one, or a credential
// rotation rebinds a co-tenant. Confirm every adapter-to-runtime sender
// sets lifecycleFrame.SessionID from its session argument.
func TestAdapterSessionScopedFramesNameTheirSession_spec_28_5_3(t *testing.T) {
	validator := schematest.Compile(t, runtimeOpsArtifact)
	lc, peer := startRuntimeOps(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		send  func() error
		reply func(frame map[string]any)
	}{
		{
			name: "checkpoint_request",
			send: func() error { return lc.RequestCheckpoint(ctx, sessionIDFixture, "ckpt-1", 5000) },
			reply: func(f map[string]any) {
				peer.write(map[string]any{"type": "checkpoint_ready", "checkpointId": f["checkpointId"]})
			},
		},
		{
			name: "checkpoint_complete",
			send: func() error { return lc.CompleteCheckpoint(sessionIDFixture, "ckpt-1", "ok", "") },
		},
		{
			name: "interrupt_request",
			send: func() error { return lc.RequestInterrupt(ctx, sessionIDFixture, "int-1", 2000) },
			reply: func(f map[string]any) {
				peer.write(map[string]any{"type": "interrupt_acknowledged", "interruptId": f["interruptId"]})
			},
		},
		{
			name: "credentials_rotated",
			send: func() error {
				return lc.RotateCredentials(ctx, sessionIDFixture, "anthropic",
					"/run/lenny/slots/"+sessionIDFixture+"/credentials.json", "lease-1")
			},
			reply: func(f map[string]any) {
				peer.write(map[string]any{"type": "credentials_acknowledged", "leaseId": f["leaseId"], "provider": "anthropic"})
			},
		},
		{
			name: "deadline_approaching",
			send: func() error { return lc.SignalDeadlineApproaching(sessionIDFixture, 5000, "session_age") },
		},
		{
			name: "files_updated",
			send: func() error { return lc.SignalFilesUpdated(sessionIDFixture) },
		},
	}
	for _, tc := range cases {
		errc := make(chan error, 1)
		go func() { errc <- tc.send() }()
		frame := peer.read()
		if frame["type"] != tc.name {
			t.Fatalf("%s: runtime read %v", tc.name, frame)
		}
		if err := validator.Validate(frame); err != nil {
			t.Errorf("%s: emitted frame does not validate against %s: %v (frame %v)", tc.name, runtimeOpsArtifact, err, frame)
		}
		if frame["sessionId"] != sessionIDFixture {
			t.Errorf("%s: sessionId = %v, want %s", tc.name, frame["sessionId"], sessionIDFixture)
		}
		if tc.reply != nil {
			tc.reply(frame)
		}
		if err := <-errc; err != nil {
			t.Fatalf("%s: sender returned %v", tc.name, err)
		}
	}
}
