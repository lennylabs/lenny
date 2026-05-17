// SPDX-License-Identifier: MIT

//go:build !linux

package adapter

import "net"

// checkPeerUID is a no-op on non-Linux platforms: SO_PEERCRED is a
// Linux feature, and the intra-pod MCP fabric runs on Linux in
// production. On a non-Linux development host the manifest-nonce
// handshake remains the authentication for intra-pod MCP connections.
func checkPeerUID(_ net.Conn, _ uint32) error {
	return nil
}
