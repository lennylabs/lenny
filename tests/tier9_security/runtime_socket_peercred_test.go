// SPDX-License-Identifier: MIT

//go:build security && linux

// Tier-9 SO_PEERCRED boundary on the CH-MSGSOCK runtime socket, the
// CH-RUNTIMEOPS socket, and the intra-pod platform MCP socket, driven
// against the real adapter.SocketRuntimeProcess listener and the host
// kernel's SO_PEERCRED.
//
// The adapter accepts the runtime's CH-MSGSOCK connection only from the
// agent UID. A process in the pod's network namespace running as any other
// UID reaches the abstract socket, but the listener refuses it: the process
// reads EOF before any frame, it never becomes the runtime connection, and
// the listener keeps accepting, so a refused process cannot hold the
// connection a later session binds to.
//
// The file is Linux-only because SO_PEERCRED is a Linux socket option. A
// test process cannot dial from a second UID without root, so these cases
// configure an expected agent UID the test process does not run as and dial
// from the test process itself. The positive case, where the expected UID's
// dial is accepted after a refusal, runs at tier 1 through the peer-UID
// lookup seam.
//
// spec: §4.7.11 (Separate UIDs and connection authentication); §28.5.3
// (CH-MSGSOCK).
package tier9_security_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// foreignUIDRuntimeSocket binds a CH-MSGSOCK listener whose expected agent
// UID is not the test process's UID, so every dial from the test process is
// a foreign peer under the host kernel's SO_PEERCRED.
func foreignUIDRuntimeSocket(t *testing.T, acceptTimeout time.Duration) *adapter.SocketRuntimeProcess {
	t.Helper()
	dir, err := os.MkdirTemp("", "rtpc")
	if err != nil {
		t.Fatalf("temp runtime socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sp, err := adapter.NewSocketRuntimeProcess(filepath.Join(dir, "r.sock"),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = acceptTimeout
	return sp
}

// dialAndExpectRefusal dials the runtime socket and asserts the adapter
// closes the connection before writing a byte.
func dialAndExpectRefusal(t *testing.T, sp *adapter.SocketRuntimeProcess, which string) {
	t.Helper()
	conn, err := net.Dial("unix", sp.SocketPath())
	if err != nil {
		t.Fatalf("%s: dial runtime socket: %v", which, err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(make([]byte, 1))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("%s: read = (%d, %v), want (0, EOF): the adapter served a peer whose "+
			"SO_PEERCRED UID is not the agent UID", which, n, err)
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-MSGSOCK)
// diagnosis: a failure means a process running as a UID other than the
// agent UID connected to the CH-MSGSOCK runtime socket and was not refused:
// either it read a frame, or Start returned it as the runtime connection, so
// a foreign process in the pod can receive every session's message frames
// and answer as the runtime. Check that NewSocketRuntimeProcess wraps the
// listener in the SO_PEERCRED peer check and that the check compares
// against SocketPeerAuth.ExpectedUID.
func TestRuntimeSocketRefusesForeignUIDPeer_spec_4_7_11(t *testing.T) {
	sp := foreignUIDRuntimeSocket(t, 500*time.Millisecond)

	started := make(chan error, 1)
	go func() { started <- sp.Start(context.Background(), "s1") }()
	dialAndExpectRefusal(t, sp, "foreign peer")

	if err := <-started; err == nil {
		t.Fatal("Start returned the foreign peer as the runtime connection")
	}
	if sp.ServesNextSession() {
		t.Fatal("ServesNextSession reported true after only a foreign peer connected")
	}
	if err := sp.WriteEnvelope("s1", []byte(`{"type":"message","sessionId":"s1"}`)); err == nil {
		t.Fatal("WriteEnvelope wrote to a connection the listener should have refused")
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-MSGSOCK), 4.7.10 (Runtime process lifetime)
// diagnosis: a failure means a refused CH-MSGSOCK peer ended or consumed
// the listener's pending accept, so the second foreign dial was not
// processed by the same Start. On a pod whose listener is kept across
// sessions, that lets one foreign dial stand in front of the runtime's own
// connection. Check that peerCheckedListener closes a refused connection
// and continues accepting instead of returning.
func TestRuntimeSocketKeepsAcceptingAfterRefusals_spec_4_7_11(t *testing.T) {
	sp := foreignUIDRuntimeSocket(t, 30*time.Second)

	started := make(chan error, 1)
	go func() { started <- sp.Start(context.Background(), "s1") }()
	dialAndExpectRefusal(t, sp, "first foreign peer")
	dialAndExpectRefusal(t, sp, "second foreign peer")

	select {
	case err := <-started:
		t.Fatalf("Start returned %v after refusals; it must keep waiting for the agent UID", err)
	default:
	}

	// The pod-scope teardown ends the pending accept.
	if err := sp.CloseListener(); err != nil {
		t.Fatalf("CloseListener: %v", err)
	}
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("Start succeeded with no agent-UID connection")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the pod-scope teardown closed the listener")
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication)
// diagnosis: a failure means a process running as a UID other than the
// agent UID connected to the platform MCP socket and was not refused, so a
// foreign process in the pod reaches the platform tool surface with only a
// nonce between it and a privileged tool. Check that listenIntraPodMCP wraps
// the listener through Server.PeerAuth and that the zero posture is not
// treated as "no check".
func TestPlatformMCPSocketRefusesForeignUIDPeer_spec_4_7_11(t *testing.T) {
	s := adapter.New("test")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = noopRuntime{}
	s.ManifestDir = t.TempDir()
	s.MCPSocket = shortMCPSocket(t)
	s.PeerAuth = adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1}
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-peer"},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			UnconditionalTeardown: true,
			SessionId:             &adapterv1.SessionId{Value: "sess-peer"},
		})
	})
	m := decodeManifestFile(t, s.ManifestDir)
	if m.PlatformMcpServer == nil || m.PlatformMcpServer.Socket == "" {
		t.Fatalf("manifest carries no platform MCP socket: %+v", m.PlatformMcpServer)
	}

	conn, err := net.Dial("unix", m.PlatformMcpServer.Socket)
	if err != nil {
		t.Fatalf("dial platform MCP socket: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := conn.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read on the platform MCP socket = (%d, %v), want (0, EOF): the adapter served "+
			"a peer whose SO_PEERCRED UID is not the agent UID", n, err)
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-RUNTIMEOPS)
// diagnosis: a failure means a process running as a UID other than the
// agent UID connected to the CH-RUNTIMEOPS socket and the adapter opened the
// capability handshake with it, so a foreign process in the pod can receive
// checkpoint, interrupt, credential-rotation, and terminate signals and
// answer them as the runtime. Check that NewRuntimeOps wraps its listener
// through SocketPeerAuth and that Run serves only connections the listener
// yields.
func TestRuntimeOpsSocketRefusesForeignUIDPeer_spec_4_7_11(t *testing.T) {
	dir, err := os.MkdirTemp("", "rtops")
	if err != nil {
		t.Fatalf("temp CH-RUNTIMEOPS socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	lc, err := adapter.NewRuntimeOps(filepath.Join(dir, "o.sock"),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1})
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- lc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = lc.Close()
		<-runErr
	})

	conn, err := net.Dial("unix", lc.SocketPath())
	if err != nil {
		t.Fatalf("dial CH-RUNTIMEOPS: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := conn.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read on CH-RUNTIMEOPS = (%d, %v), want (0, EOF): the adapter served a peer whose "+
			"SO_PEERCRED UID is not the agent UID", n, err)
	}
}
