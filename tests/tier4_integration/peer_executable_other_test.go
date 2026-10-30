// SPDX-License-Identifier: MIT

//go:build integration && !linux

package tier4_integration_test

import "net"

// peerExecutableReadable reports that peerExecutable cannot name the peer
// process here: SO_PEERCRED and /proc are Linux interfaces.
const peerExecutableReadable = false

// peerExecutable returns "" off Linux.
func peerExecutable(net.Conn) string { return "" }
