// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// listenerNonces maps a runtime listener's socket address to the mcpNonce of
// the manifest its nonce provider reads, so the package's dial helpers write
// the nonce line the listener requires. spec: §4.7.11 (Runtime connection
// handshake).
var listenerNonces sync.Map

// newSocketRuntime publishes a test manifest and binds a CH-MSGSOCK listener
// whose nonce provider reads it.
func newSocketRuntime(t *testing.T, socket string, auth adapter.SocketPeerAuth) (*adapter.SocketRuntimeProcess, error) {
	t.Helper()
	m := runtimenonce.Publish(t, nil)
	sp, err := adapter.NewSocketRuntimeProcess(socket, auth, adapter.PublishedManifestNonce(m.Dir))
	if err == nil {
		registerListenerNonce(t, sp.SocketPath(), m.Nonce)
	}
	return sp, err
}

// newSpawningSocketRuntime is newSocketRuntime for a test that spawns a
// runtime through SpawnPath: it names the published manifest in
// LENNY_ADAPTER_MANIFEST, which the spawned child inherits, so the child's
// nonce line carries the nonce the listener's provider reads.
func newSpawningSocketRuntime(t *testing.T, socket string, auth adapter.SocketPeerAuth) (*adapter.SocketRuntimeProcess, error) {
	t.Helper()
	m := runtimenonce.Publish(t, nil)
	t.Setenv(runtimekit.ManifestEnvVar, m.Path)
	return adapter.NewSocketRuntimeProcess(socket, auth, adapter.PublishedManifestNonce(m.Dir))
}

// newRuntimeOps publishes a test manifest and binds a CH-RUNTIMEOPS listener
// whose nonce provider reads it.
func newRuntimeOps(t *testing.T, socket string, auth adapter.SocketPeerAuth) (*adapter.RuntimeOps, error) {
	t.Helper()
	m := runtimenonce.Publish(t, nil)
	lc, err := adapter.NewRuntimeOps(socket, auth, adapter.PublishedManifestNonce(m.Dir))
	if err == nil {
		registerListenerNonce(t, lc.SocketPath(), m.Nonce)
	}
	return lc, err
}

// registerListenerNonce records nonce for the listener at socket until the
// test ends.
func registerListenerNonce(t *testing.T, socket, nonce string) {
	t.Helper()
	listenerNonces.Store(socket, nonce)
	t.Cleanup(func() { listenerNonces.Delete(socket) })
}

// writeListenerNonce writes the nonce line the listener at socket requires
// on conn. A socket with no registered nonce writes nothing, which a test
// of the refusal of a nonce-less connection relies on.
func writeListenerNonce(conn net.Conn, socket string) error {
	nonce, ok := listenerNonces.Load(socket)
	if !ok {
		return nil
	}
	return runtimenonce.Write(conn, nonce.(string))
}

// answerListenerChallenge reads the nonce-only challenge the listener at
// socket writes after the nonce line and writes the HMAC answer keyed by the
// listener's nonce. It returns the reader, which holds anything the adapter
// wrote after the challenge.
func answerListenerChallenge(t *testing.T, conn net.Conn, socket string) *bufio.Reader {
	t.Helper()
	nonce, ok := listenerNonces.Load(socket)
	if !ok {
		t.Fatalf("no nonce registered for %s", socket)
	}
	r := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	challenge, ok := runtimekit.ChallengeOf(line)
	if !ok {
		t.Fatalf("first adapter line %q is not a challenge", line)
	}
	if _, err := conn.Write(runtimekit.ChallengeResponseLine(nonce.(string), challenge)); err != nil {
		t.Fatalf("write challenge answer: %v", err)
	}
	return r
}

// isClosedByPeer reports whether a read error means the adapter closed the
// connection: EOF, or a reset when the adapter closed it with the runtime's
// nonce line still unread.
func isClosedByPeer(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)
}
