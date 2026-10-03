// SPDX-License-Identifier: MIT

package adapter

import (
	"fmt"
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
