// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncBuffer is an io.Writer a slog handler writes from the accepting
// goroutine while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runRuntimeOps binds a CH-RUNTIMEOPS listener with auth, lets configure
// install test seams before Run starts, and runs it until the test ends.
func runRuntimeOps(t *testing.T, auth SocketPeerAuth, configure func(*RuntimeOps)) (*RuntimeOps, string) {
	t.Helper()
	sock := shortSocketName(t, "ops.sock")
	lc, err := NewRuntimeOps(sock, auth)
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	if configure != nil {
		configure(lc)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- lc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = lc.Close()
		<-runErr
	})
	return lc, sock
}

// dialOps dials the CH-RUNTIMEOPS socket as a runtime.
func dialOps(t *testing.T, sock string) *fakeRuntime {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial CH-RUNTIMEOPS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &fakeRuntime{t: t, conn: conn, r: bufio.NewReader(conn)}
}

// requireOpsRefused asserts the adapter closed fr's connection before
// sending any frame.
func requireOpsRefused(t *testing.T, fr *fakeRuntime, which string) {
	t.Helper()
	_ = fr.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := fr.conn.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("%s: read = (%d, %v), want (0, EOF)", which, n, err)
	}
}

// requireOpsServed asserts the adapter opened the handshake on fr's
// connection, which it does only for the connection Run serves.
func requireOpsServed(t *testing.T, fr *fakeRuntime, which string) {
	t.Helper()
	if f := fr.read(); f.Type != "lifecycle_capabilities" {
		t.Fatalf("%s: first frame = %q, want lifecycle_capabilities", which, f.Type)
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-RUNTIMEOPS)
func TestRuntimeOpsRefusesForeignUIDAndServesTheRuntimeNext_spec_4_7_11(t *testing.T) {
	expected := uint32(os.Getuid())
	logs := &syncBuffer{}
	var calls atomic.Int64
	_, sock := runRuntimeOps(t, SocketPeerAuth{ExpectedUID: expected}, func(lc *RuntimeOps) {
		lc.peerUID = func(net.Conn) (uint32, error) {
			if calls.Add(1) == 1 {
				return 4242, nil
			}
			return expected, nil
		}
		lc.logger = slog.New(slog.NewJSONHandler(logs, nil))
	})

	requireOpsRefused(t, dialOps(t, sock), "foreign peer")
	requireOpsServed(t, dialOps(t, sock), "agent-UID peer after a refusal")

	record := logs.String()
	for _, want := range []string{`"msg":"runtimeops_peer_refused"`, `"peer_uid":4242`} {
		if !strings.Contains(record, want) {
			t.Errorf("refusal log %q lacks %s", record, want)
		}
	}
}

// spec: 4.7.11 (Separate UIDs and connection authentication)
func TestRuntimeOpsServesTheAgentUIDThroughSOPeercred_spec_4_7_11(t *testing.T) {
	_, sock := runRuntimeOps(t, SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}, nil)
	requireOpsServed(t, dialOps(t, sock), "agent-UID peer")
}

// spec: 4.7.11 (Separate UIDs and connection authentication, Nonce-only
// fallback), 28.5.3 (CH-RUNTIMEOPS)
func TestRuntimeOpsNonceOnlyAndEmbeddedPosturesApplyNoPeerCheck_spec_4_7_11(t *testing.T) {
	for name, auth := range map[string]SocketPeerAuth{
		"nonce-only": {ExpectedUID: uint32(os.Getuid()) + 1, NonceOnly: true},
		"embedded":   EmbeddedPeerAuth(),
	} {
		t.Run(name, func(t *testing.T) {
			_, sock := runRuntimeOps(t, auth, func(lc *RuntimeOps) {
				lc.peerUID = func(net.Conn) (uint32, error) {
					t.Errorf("the %s posture read the peer UID; it applies no peer check", name)
					return 4242, nil
				}
			})
			requireOpsServed(t, dialOps(t, sock), name+" peer")
		})
	}
}
