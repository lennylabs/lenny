// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// testListenerNonces maps a runtime listener's socket address to the
// mcpNonce of the manifest its nonce provider reads, so the package's dial
// helpers write the nonce line the listener requires. spec: §4.7.11 (Runtime
// connection handshake).
var testListenerNonces sync.Map

// newTestSocketRuntime publishes a test manifest and binds a CH-MSGSOCK
// listener whose nonce provider reads it.
func newTestSocketRuntime(t *testing.T, socket string, auth SocketPeerAuth) (*SocketRuntimeProcess, error) {
	t.Helper()
	m := runtimenonce.Publish(t, nil)
	sp, err := NewSocketRuntimeProcess(socket, auth, PublishedManifestNonce(m.Dir))
	if err == nil {
		registerTestListenerNonce(t, sp.SocketPath(), m.Nonce)
	}
	return sp, err
}

// newTestRuntimeOps publishes a test manifest and binds a CH-RUNTIMEOPS
// listener whose nonce provider reads it.
func newTestRuntimeOps(t *testing.T, socket string, auth SocketPeerAuth) (*RuntimeOps, error) {
	t.Helper()
	m := runtimenonce.Publish(t, nil)
	lc, err := NewRuntimeOps(socket, auth, PublishedManifestNonce(m.Dir))
	if err == nil {
		registerTestListenerNonce(t, lc.SocketPath(), m.Nonce)
	}
	return lc, err
}

// registerTestListenerNonce records nonce for the listener at socket until
// the test ends.
func registerTestListenerNonce(t *testing.T, socket, nonce string) {
	t.Helper()
	testListenerNonces.Store(socket, nonce)
	t.Cleanup(func() { testListenerNonces.Delete(socket) })
}

// writeTestListenerNonce writes the nonce line the listener at socket
// requires on conn. A socket with no registered nonce writes nothing, which
// a test of the refusal of a nonce-less connection relies on.
func writeTestListenerNonce(conn net.Conn, socket string) error {
	nonce, ok := testListenerNonces.Load(socket)
	if !ok {
		return nil
	}
	return runtimenonce.Write(conn, nonce.(string))
}

// answerTestChallenge reads the nonce-only challenge the listener at socket
// writes after the nonce line from r and writes the HMAC answer keyed by the
// listener's nonce.
func answerTestChallenge(t *testing.T, conn net.Conn, r *bufio.Reader, socket string) {
	t.Helper()
	nonce, ok := testListenerNonces.Load(socket)
	if !ok {
		t.Fatalf("no nonce registered for %s", socket)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	challenge, err := handshakeMember(line, mcp.ChallengeParamKey)
	if err != nil {
		t.Fatalf("first adapter line %q is not a challenge", line)
	}
	answer := challengeAnswerLine(nonce.(string), challenge)
	if _, err := conn.Write(answer); err != nil {
		t.Fatalf("write challenge answer: %v", err)
	}
}

// challengeAnswerLine encodes the challenge answer line.
func challengeAnswerLine(nonce, challenge string) []byte {
	return []byte(`{"` + mcp.ChallengeResponseParamKey + `":"` + mcp.ExpectedChallengeResponse(nonce, challenge) + `"}` + "\n")
}

// isClosedByAdapter reports whether a read error means the adapter closed
// the connection: EOF, or a reset when the adapter closed it with the
// runtime's nonce line still unread.
func isClosedByAdapter(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)
}
