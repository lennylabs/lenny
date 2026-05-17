// SPDX-License-Identifier: MIT

//go:build linux

package adapter

import (
	"fmt"
	"net"
	"syscall"
)

// checkPeerUID verifies via SO_PEERCRED that the process on the other
// end of conn runs as expectedUID. It is the §4.7 / §13 defense-in-depth
// peer-credential check the adapter applies to every intra-pod MCP
// connection on top of the manifest-nonce handshake: a compromised
// process running as a different UID is rejected even if it somehow
// presents a valid nonce.
func checkPeerUID(conn net.Conn, expectedUID uint32) error {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return fmt.Errorf("adapter: MCP connection exposes no syscall handle")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return fmt.Errorf("adapter: MCP connection syscall handle: %w", err)
	}
	var ucred *syscall.Ucred
	var credErr error
	if ctrlErr := raw.Control(func(fd uintptr) {
		ucred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); ctrlErr != nil {
		return fmt.Errorf("adapter: read MCP peer credentials: %w", ctrlErr)
	}
	if credErr != nil {
		return fmt.Errorf("adapter: SO_PEERCRED: %w", credErr)
	}
	if ucred.Uid != expectedUID {
		return fmt.Errorf("adapter: MCP peer uid %d does not match the runtime uid %d",
			ucred.Uid, expectedUID)
	}
	return nil
}
