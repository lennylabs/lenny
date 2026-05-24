// SPDX-License-Identifier: MIT

//go:build linux

package adapter

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// checkPeerUID verifies via SO_PEERCRED that the process on the other
// end of conn runs as expectedUID. It is the §4.7 / §13 defense-in-depth
// peer-credential check the adapter applies to every intra-pod MCP
// connection on top of the manifest-nonce handshake: a compromised
// process running as a different UID is rejected even if it somehow
// presents a valid nonce.
func checkPeerUID(conn net.Conn, expectedUID uint32) error {
	uid, err := peerCredUID(conn)
	if err != nil {
		return err
	}
	if uid != expectedUID {
		return fmt.Errorf("adapter: MCP peer uid %d does not match the runtime uid %d",
			uid, expectedUID)
	}
	return nil
}

// peerCredUID reads the peer's effective UID from conn via the
// SO_PEERCRED socket option. It is the single SO_PEERCRED call site the
// per-connection peer check (checkPeerUID) and the startup self-test
// (PeercredSelftest) share.
func peerCredUID(conn net.Conn) (uint32, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("adapter: connection exposes no syscall handle")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("adapter: connection syscall handle: %w", err)
	}
	var ucred *syscall.Ucred
	var credErr error
	if ctrlErr := raw.Control(func(fd uintptr) {
		ucred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); ctrlErr != nil {
		return 0, fmt.Errorf("adapter: read peer credentials: %w", ctrlErr)
	}
	if credErr != nil {
		return 0, fmt.Errorf("adapter: SO_PEERCRED: %w", credErr)
	}
	return ucred.Uid, nil
}

// PeercredSelftest verifies that SO_PEERCRED is functional in the current
// pod environment before the adapter signals READY, per §4.7 "Mandatory
// SO_PEERCRED startup self-test" (spec/04_system-components.md lines
// 870-877). It opens an abstract Unix socket, connects to it from the same
// process, and asserts that the peer UID reported by SO_PEERCRED on the
// accepted connection matches the adapter's own UID. A syscall error or a
// UID mismatch returns a non-nil error; the caller logs FATAL, increments
// lenny_adapter_sopeercred_selftest_failed_total, and exits non-zero so
// the pod crash-loops before any agent process is spawned.
func PeercredSelftest() error {
	const name = "@lenny-sopeercred-selftest"
	lis, err := net.Listen("unix", name)
	if err != nil {
		return fmt.Errorf("adapter: SO_PEERCRED self-test listen: %w", err)
	}
	defer lis.Close()

	type accepted struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan accepted, 1)
	go func() {
		c, e := lis.Accept()
		acceptCh <- accepted{conn: c, err: e}
	}()

	dialed, err := net.Dial("unix", name)
	if err != nil {
		return fmt.Errorf("adapter: SO_PEERCRED self-test dial: %w", err)
	}
	defer dialed.Close()

	a := <-acceptCh
	if a.err != nil {
		return fmt.Errorf("adapter: SO_PEERCRED self-test accept: %w", a.err)
	}
	defer a.conn.Close()

	uid, err := peerCredUID(a.conn)
	if err != nil {
		return err
	}
	if self := uint32(os.Getuid()); uid != self {
		return fmt.Errorf("adapter: SO_PEERCRED self-test peer uid %d does not match adapter uid %d",
			uid, self)
	}
	return nil
}
