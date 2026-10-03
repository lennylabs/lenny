// SPDX-License-Identifier: MIT

package adapter

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
)

// peerCheckedListener wraps a net.Listener and runs a per-connection
// credential check before yielding a connection from Accept. A
// connection the check rejects is closed, and Accept moves on to the
// next, so a refused peer never becomes the accepted connection and never
// stands in front of the next legitimate one. The adapter wraps the §4.7
// platform and connector MCP listeners and the CH-MSGSOCK runtime
// listener in it. The check is defense in depth on top of the
// manifest-nonce handshake. spec: §4.7.11 (Separate UIDs and connection
// authentication).
type peerCheckedListener struct {
	net.Listener
	check func(net.Conn) error
	// onReject, when set, observes each refused connection and the check's
	// error before the listener closes the connection. It must not block,
	// because it runs on the accepting goroutine.
	onReject func(error)
}

// Accept returns the next connection that passes the credential check.
// A rejected connection is closed and Accept continues to the next.
func (l *peerCheckedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.check == nil {
			return conn, nil
		}
		checkErr := l.check(conn)
		if checkErr == nil {
			return conn, nil
		}
		if l.onReject != nil {
			l.onReject(checkErr)
		}
		_ = conn.Close()
	}
}

// PeerUIDMismatchError reports that SO_PEERCRED named a peer UID other than
// the agent UID the adapter expects. Callers read the two UIDs with
// errors.As, for example to log the refused peer.
type PeerUIDMismatchError struct {
	// Peer is the UID SO_PEERCRED reported for the connecting process.
	Peer uint32
	// Expected is the agent UID the adapter accepts connections from.
	Expected uint32
}

// Error implements error.
func (e *PeerUIDMismatchError) Error() string {
	return fmt.Sprintf("adapter: peer uid %d does not match the runtime uid %d", e.Peer, e.Expected)
}

// matchPeerUID reads the peer UID of conn through lookup and returns a
// *PeerUIDMismatchError when it differs from expectedUID. A lookup error is
// returned as is, so a connection whose credentials cannot be read is
// refused rather than admitted. The lookup is a parameter so the
// CH-MSGSOCK listener's tests can stand in a peer UID the test process
// cannot take without root.
func matchPeerUID(conn net.Conn, expectedUID uint32, lookup func(net.Conn) (uint32, error)) error {
	uid, err := lookup(conn)
	if err != nil {
		return err
	}
	if uid != expectedUID {
		return &PeerUIDMismatchError{Peer: uid, Expected: expectedUID}
	}
	return nil
}

// SocketPeerAuth is the connection-authentication posture of the adapter's
// listeners on the adapter-agent sockets: the CH-MSGSOCK runtime socket and
// the intra-pod platform and connector MCP sockets. The zero value requires
// the peer to run as UID 0, so a caller that omits the agent UID admits no
// unprivileged process rather than every process.
// spec: §4.7.11 (Separate UIDs and connection authentication), §28.5.3
// (CH-MSGSOCK, Endpoint).
type SocketPeerAuth struct {
	// ExpectedUID is the agent UID the adapter accepts connections from: the
	// runtime container's runAsUser, which is a UID within the pod's user
	// namespace. The sidecar adapter receives it as --runtime-uid. A test
	// that dials from its own process sets it to os.Getuid().
	ExpectedUID uint32
	// NonceOnly records that Runtime.spec.requireSoPeercred is false
	// (--require-so-peercred=false), the mode for a confirmed gVisor
	// SO_PEERCRED divergence. The specification states that the peer check
	// is unavailable in this mode and that the manifest nonce and the
	// per-connection HMAC-SHA256 challenge authenticate the connection
	// instead, so the listeners apply no peer check. The MCP servers run the
	// challenge; CH-MSGSOCK has neither exchange, which leaves that socket
	// unauthenticated in this mode (BUILD-GAPS F-4.7.25).
	NonceOnly bool
	// NoSocketBoundary records the embedded deployment model, which the
	// specification describes as a single trusted process with no
	// adapter-agent socket boundary. The listeners apply no peer check, and
	// unlike NonceOnly it adds no challenge-response: the MCP servers keep
	// the manifest-nonce authentication alone. EmbeddedPeerAuth is the only
	// constructor that sets it; the zero value keeps the fail-closed check.
	NoSocketBoundary bool
}

// EmbeddedPeerAuth returns the posture of the embedded deployment model: no
// adapter-agent socket boundary, so no peer check, and no change to the MCP
// servers' nonce authentication. spec: §4.7.11 (Separate UIDs and
// connection authentication).
func EmbeddedPeerAuth() SocketPeerAuth {
	return SocketPeerAuth{NoSocketBoundary: true}
}

// check admits conn only when SO_PEERCRED reports the expected agent UID.
func (a SocketPeerAuth) check(conn net.Conn) error {
	return checkPeerUID(conn, a.ExpectedUID)
}

// wrap returns l wrapped in peerCheckedListener with check and onReject, or
// l itself in nonce-only mode, where the specification states that the
// peer check is unavailable, and in the embedded model, which has no
// adapter-agent socket boundary. Every adapter-agent listener takes its peer
// check through this one decision. spec: §4.7.11 (Separate UIDs and
// connection authentication, Nonce-only fallback), §28.5.3 (CH-MSGSOCK,
// Endpoint).
func (a SocketPeerAuth) wrap(l net.Listener, check func(net.Conn) error, onReject func(error)) net.Listener {
	if a.NonceOnly || a.NoSocketBoundary {
		return l
	}
	return &peerCheckedListener{Listener: l, check: check, onReject: onReject}
}

// logPeerRefusal records a refused adapter-agent connection under event,
// with the peer UID SO_PEERCRED reported or, when the UID could not be read,
// the lookup error. The record carries identifiers only: a refused
// connection never received a byte. A nil logger logs to slog.Default.
func logPeerRefusal(logger *slog.Logger, event, socket string, expectedUID uint32, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []any{"socket", socket, "expected_uid", expectedUID}
	var mismatch *PeerUIDMismatchError
	if errors.As(err, &mismatch) {
		attrs = append(attrs, "peer_uid", mismatch.Peer)
	} else {
		attrs = append(attrs, "err", err)
	}
	logger.Warn(event, attrs...)
}
