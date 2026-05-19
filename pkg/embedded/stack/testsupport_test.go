// SPDX-License-Identifier: MIT

package stack

import (
	"net"
	"strings"
)

// listenLoopback opens a loopback TCP listener on an OS-chosen port.
// Tests close it immediately to claim a free port.
func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

// portOf returns the port component of a host:port address.
func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}

// contains reports whether s contains sub.
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
