// SPDX-License-Identifier: MIT

//go:build integration && linux

package tier4_integration_test

import (
	"net"
	"os"
	"strconv"
	"syscall"
)

// peerExecutableReadable reports that peerExecutable can name the process
// at the other end of a Unix connection on this platform.
const peerExecutableReadable = true

// peerExecutable reads the executable of the process at the other end of a
// Unix connection from its SO_PEERCRED credentials and /proc. It returns ""
// when either read fails.
func peerExecutable(conn net.Conn) string {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return ""
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return ""
	}
	var cred *syscall.Ucred
	if err := raw.Control(func(fd uintptr) {
		cred, _ = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || cred == nil {
		return ""
	}
	exe, err := os.Readlink("/proc/" + strconv.Itoa(int(cred.Pid)) + "/exe")
	if err != nil {
		return ""
	}
	return exe
}
