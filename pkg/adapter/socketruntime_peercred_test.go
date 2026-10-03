// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// foreignUID is a peer UID the CH-MSGSOCK tests present through the lookup
// seam. It differs from every expected UID the tests configure.
const foreignUID = 4242

// lockedBuffer is an io.Writer a slog handler can write from the accepting
// goroutine while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// sequencedPeerUIDs returns a peer-UID lookup that reports uids in order, one
// per accepted connection, and the expected UID after the list is exhausted.
func sequencedPeerUIDs(expected uint32, uids ...uint32) func(net.Conn) (uint32, error) {
	var n atomic.Int64
	return func(net.Conn) (uint32, error) {
		i := int(n.Add(1)) - 1
		if i < len(uids) {
			return uids[i], nil
		}
		return expected, nil
	}
}

// requireRefused asserts that the adapter closed conn without sending a byte:
// a refused peer reads EOF.
func requireRefused(t *testing.T, conn net.Conn, when string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(make([]byte, 1))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("%s: read on the refused connection = (%d, %v), want (0, EOF)", when, n, err)
	}
}

// startAsync runs sp.Start in the background and returns its result channel.
func startAsync(sp *adapter.SocketRuntimeProcess, sessionID string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- sp.Start(context.Background(), sessionID) }()
	return done
}

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-MSGSOCK)
func TestSocketRuntimeRefusesForeignUIDAndAcceptsTheRuntimeNext_spec_4_7_11(t *testing.T) {
	expected := uint32(os.Getuid())
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t), adapter.SocketPeerAuth{ExpectedUID: expected})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 10 * time.Second
	sp.SetPeerUIDLookupForTest(sequencedPeerUIDs(expected, foreignUID))
	logs := &lockedBuffer{}
	sp.SetLoggerForTest(slog.New(slog.NewJSONHandler(logs, nil)))

	started := startAsync(sp, "s1")

	// The foreign peer dials first and is refused. Start keeps waiting.
	foreign := dialRuntimeSocket(t, sp.SocketPath())
	defer foreign.Close()
	requireRefused(t, foreign, "foreign peer")
	select {
	case err := <-started:
		t.Fatalf("Start returned %v after the foreign peer; it must keep waiting for the runtime", err)
	default:
	}
	if sp.ServesNextSession() {
		t.Fatal("ServesNextSession reported true after only a refused peer connected")
	}

	// The runtime dials next and becomes the connection.
	rt := dialRuntimeSocket(t, sp.SocketPath())
	defer rt.Close()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("Start after the runtime dialed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not accept the runtime after refusing the foreign peer")
	}

	// A frame the adapter writes reaches the runtime's connection.
	if err := sp.WriteEnvelope("s1", []byte(`{"type":"message","sessionId":"s1"}`)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	_ = rt.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(rt).ReadString('\n')
	if err != nil || !strings.Contains(line, `"sessionId":"s1"`) {
		t.Fatalf("runtime read = (%q, %v), want the s1 envelope", line, err)
	}

	record := logs.String()
	for _, want := range []string{`"msg":"runtime_peer_refused"`, `"peer_uid":4242`} {
		if !strings.Contains(record, want) {
			t.Errorf("refusal log %q lacks %s", record, want)
		}
	}
	if strings.Count(record, "runtime_peer_refused") != 1 {
		t.Errorf("refusal log %q records %d refusals, want 1", record, strings.Count(record, "runtime_peer_refused"))
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-MSGSOCK)
func TestSocketRuntimeRefusesPeerWhoseCredentialsCannotBeRead_spec_4_7_11(t *testing.T) {
	expected := uint32(os.Getuid())
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t), adapter.SocketPeerAuth{ExpectedUID: expected})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 300 * time.Millisecond
	sp.SetPeerUIDLookupForTest(func(net.Conn) (uint32, error) {
		return 0, errors.New("SO_PEERCRED: operation not supported")
	})
	logs := &lockedBuffer{}
	sp.SetLoggerForTest(slog.New(slog.NewJSONHandler(logs, nil)))

	started := startAsync(sp, "s1")
	peer := dialRuntimeSocket(t, sp.SocketPath())
	defer peer.Close()
	requireRefused(t, peer, "unreadable-credential peer")

	if err := <-started; err == nil {
		t.Fatal("Start accepted a peer whose credentials could not be read")
	}
	if sp.ServesNextSession() {
		t.Fatal("ServesNextSession reported true with no accepted runtime")
	}
	if !strings.Contains(logs.String(), "operation not supported") {
		t.Errorf("refusal log %q lacks the lookup error", logs.String())
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication)
func TestSocketRuntimeAcceptsTheExpectedUIDThroughSOPeercred_spec_4_7_11(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 5 * time.Second

	started := startAsync(sp, "s1")
	rt := dialRuntimeSocket(t, sp.SocketPath())
	defer rt.Close()
	if err := <-started; err != nil {
		t.Fatalf("Start for a peer running as the expected UID: %v", err)
	}
	if !sp.ServesNextSession() {
		t.Fatal("ServesNextSession reported false after the expected UID connected")
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-MSGSOCK)
func TestSocketRuntimeRefusesOtherUIDThroughSOPeercred_spec_4_7_11(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SO_PEERCRED is Linux-only")
	}
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 300 * time.Millisecond
	sp.SetLoggerForTest(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	started := startAsync(sp, "s1")
	peer := dialRuntimeSocket(t, sp.SocketPath())
	defer peer.Close()
	requireRefused(t, peer, "peer running as another UID")
	if err := <-started; err == nil {
		t.Fatal("Start accepted a peer whose SO_PEERCRED UID is not the agent UID")
	}
}

// spec: 4.7.11 (Nonce-only fallback), 28.5.3 (CH-MSGSOCK)
func TestSocketRuntimeNonceOnlyModeAppliesNoPeerCheck_spec_4_7_11(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1, NonceOnly: true})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 5 * time.Second
	sp.SetPeerUIDLookupForTest(func(net.Conn) (uint32, error) {
		t.Error("nonce-only mode read the peer UID; the peer check is unavailable in this mode")
		return foreignUID, nil
	})

	started := startAsync(sp, "s1")
	rt := dialRuntimeSocket(t, sp.SocketPath())
	defer rt.Close()
	if err := <-started; err != nil {
		t.Fatalf("Start in nonce-only mode: %v", err)
	}
}
