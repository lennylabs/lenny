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
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// foreignUIDRuntimeSocket binds a CH-MSGSOCK listener whose expected agent
// UID is not the test process's UID, so every dial from the test process is
// a foreign peer under the host kernel's SO_PEERCRED. The listener's nonce
// provider reads a published test manifest, whose nonce it returns, so a
// dial can present a valid nonce line and its refusal is attributable to
// the peer UID alone.
func foreignUIDRuntimeSocket(t *testing.T, acceptTimeout time.Duration) (*adapter.SocketRuntimeProcess, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rtpc")
	if err != nil {
		t.Fatalf("temp runtime socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	manifest := runtimenonce.Publish(t, nil)
	sp, err := adapter.NewSocketRuntimeProcess(filepath.Join(dir, "r.sock"),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1}, adapter.PublishedManifestNonce(manifest.Dir))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = acceptTimeout
	return sp, manifest.Nonce
}

// dialAndExpectRefusal dials the runtime socket, writes the nonce line
// carrying the listener's published nonce, and asserts the adapter closes
// the connection before writing a byte. The valid nonce line makes the
// refusal attributable to the peer UID rather than to a missing nonce.
func dialAndExpectRefusal(t *testing.T, sp *adapter.SocketRuntimeProcess, nonce, which string) {
	t.Helper()
	conn, err := net.Dial("unix", sp.SocketPath())
	if err != nil {
		t.Fatalf("%s: dial runtime socket: %v", which, err)
	}
	defer conn.Close()
	// The peer check can close the connection before the line goes out, so
	// a write error is the refusal arriving first rather than a failure.
	_ = runtimenonce.Write(conn, nonce)
	requireClosedByAdapter(t, conn, which+": the adapter served a peer whose SO_PEERCRED UID is not the agent UID")
}

// requireClosedByAdapter asserts the adapter closes conn without writing a
// byte. A close with the runtime's nonce line still unread arrives as a
// reset rather than EOF.
func requireClosedByAdapter(t *testing.T, conn net.Conn, which string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(make([]byte, 1))
	if n != 0 || !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("%s: read = (%d, %v), want (0, EOF or reset)", which, n, err)
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
	sp, nonce := foreignUIDRuntimeSocket(t, 500*time.Millisecond)

	started := make(chan error, 1)
	go func() { started <- sp.Start(context.Background(), "s1") }()
	dialAndExpectRefusal(t, sp, nonce, "foreign peer")

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
	sp, nonce := foreignUIDRuntimeSocket(t, 30*time.Second)

	started := make(chan error, 1)
	go func() { started <- sp.Start(context.Background(), "s1") }()
	dialAndExpectRefusal(t, sp, nonce, "first foreign peer")
	dialAndExpectRefusal(t, sp, nonce, "second foreign peer")

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
	manifest := runtimenonce.Publish(t, nil)
	lc, err := adapter.NewRuntimeOps(filepath.Join(dir, "o.sock"),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1}, adapter.PublishedManifestNonce(manifest.Dir))
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
	// The valid nonce line makes the refusal attributable to the peer UID.
	// The peer check can close the connection before the line goes out.
	_ = runtimenonce.Write(conn, manifest.Nonce)
	requireClosedByAdapter(t, conn, "CH-RUNTIMEOPS: the adapter served a peer whose SO_PEERCRED UID is not the agent UID")
}

// spec: 4.7.11 (Runtime connection handshake, Nonce-only fallback), 28.5.3
// (CH-MSGSOCK), 28.5.3 (CH-RUNTIMEOPS)
// diagnosis: a failure means that in nonce-only mode, where no peer check
// applies, a process running as the agent UID that presents no nonce line
// connected to the CH-MSGSOCK or CH-RUNTIMEOPS socket and was not refused,
// so any process in the pod's network namespace that can reach the abstract
// socket becomes the runtime connection without proving it read the
// manifest. Check that the accept path runs authenticateRuntimeConn in every
// posture and closes a connection whose first line is not the published
// nonce.
func TestRuntimeSocketsRefuseANonceLessPeerInNonceOnlyMode_spec_4_7_11(t *testing.T) {
	auth := adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()), NonceOnly: true}
	t.Run("CH-MSGSOCK", func(t *testing.T) {
		manifest := runtimenonce.Publish(t, nil)
		dir, err := os.MkdirTemp("", "rtno")
		if err != nil {
			t.Fatalf("temp socket dir: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		sp, err := adapter.NewSocketRuntimeProcess(filepath.Join(dir, "r.sock"), auth, adapter.PublishedManifestNonce(manifest.Dir))
		if err != nil {
			t.Fatalf("NewSocketRuntimeProcess: %v", err)
		}
		t.Cleanup(func() { _ = sp.CloseListener() })
		sp.AcceptTimeout = 30 * time.Second
		started := make(chan error, 1)
		go func() { started <- sp.Start(context.Background(), "s1") }()

		bare, err := net.Dial("unix", sp.SocketPath())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer bare.Close()
		if _, err := bare.Write([]byte(`{"type":"session_started","sessionId":"s1","startId":"st_1"}` + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		requireClosedByAdapter(t, bare, "nonce-less same-UID peer on CH-MSGSOCK")
		select {
		case err := <-started:
			t.Fatalf("Start returned %v after only a nonce-less peer dialed", err)
		default:
		}

		// The runtime that presents the nonce and answers the challenge
		// is installed next.
		rt, err := runtimekit.DialAuthenticated(context.Background(), sp.SocketPath(), manifest.Path)
		if err != nil {
			t.Fatalf("authenticated dial: %v", err)
		}
		defer rt.Close()
		go func() { _, _ = rt.Read(make([]byte, 1)) }()
		select {
		case err := <-started:
			if err != nil {
				t.Fatalf("Start after the authenticated dial: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Start did not install the authenticated runtime after the refusal")
		}
	})
	t.Run("CH-RUNTIMEOPS", func(t *testing.T) {
		manifest := runtimenonce.Publish(t, nil)
		dir, err := os.MkdirTemp("", "rtno")
		if err != nil {
			t.Fatalf("temp socket dir: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		lc, err := adapter.NewRuntimeOps(filepath.Join(dir, "o.sock"), auth, adapter.PublishedManifestNonce(manifest.Dir))
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

		bare, err := net.Dial("unix", lc.SocketPath())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer bare.Close()
		requireClosedByAdapter(t, bare, "nonce-less same-UID peer on CH-RUNTIMEOPS")

		rt, err := runtimekit.DialAuthenticated(context.Background(), lc.SocketPath(), manifest.Path)
		if err != nil {
			t.Fatalf("authenticated dial: %v", err)
		}
		defer rt.Close()
		_ = rt.SetReadDeadline(time.Now().Add(10 * time.Second))
		line, err := bufio.NewReader(rt).ReadString('\n')
		if err != nil || !strings.Contains(line, `"lifecycle_capabilities"`) {
			t.Fatalf("authenticated runtime read = (%q, %v), want lifecycle_capabilities", line, err)
		}
	})
}
