// SPDX-License-Identifier: MIT

package adapter

import "net"

// peerCheckedListener wraps a net.Listener and runs a per-connection
// credential check before yielding a connection from Accept. A
// connection the check rejects is closed, and Accept moves on to the
// next, so the §4.7 platform MCP server never serves a peer that fails
// the §13 peer-credential check. The check is defense in depth on top
// of the manifest-nonce handshake.
type peerCheckedListener struct {
	net.Listener
	check func(net.Conn) error
}

// Accept returns the next connection that passes the credential check.
// A rejected connection is closed and Accept continues to the next.
func (l *peerCheckedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.check == nil || l.check(conn) == nil {
			return conn, nil
		}
		_ = conn.Close()
	}
}
