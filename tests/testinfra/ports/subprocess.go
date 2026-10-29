// SPDX-License-Identifier: MIT

package ports

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A subprocess harness (tests/testinfra/gateway, opsprocess, and
// tokenservice) passes the process a loopback address it chose with Reserve,
// because none of those binaries can bind port 0 and report the port it got.
// Between Reserve and the child's bind, another test's process or a
// container's published port can take the port (TEST-GAPS T-17.4.38). Two
// things go wrong then. The child's bind fails and it exits, and, worse, the
// harness's readiness probe can be answered by the other process that holds
// the port, so the harness hands its test a process that is not its own.
// The helpers below close both: StartRetrying starts the process again on
// fresh addresses when the child exits on a bind refusal, and Held lets the
// readiness wait require that the child itself holds the listener before it
// reports ready.

// StartAttempts is the number of times StartRetrying starts a process before
// it fails the test. A collision needs another bind to land on the same
// ephemeral port inside a window of a few hundred milliseconds, so a second
// attempt almost always succeeds; the extra attempts absorb a burst.
const StartAttempts = 5

// ErrAddrInUse is the error a start function passed to StartRetrying returns
// when the process could not bind an address it was given, so the start is
// retried on fresh addresses.
var ErrAddrInUse = errors.New("address already in use")

// addrInUseText is the text Go's net package puts in a bind refusal
// (syscall.EADDRINUSE), which each binary logs before it exits.
const addrInUseText = "address already in use"

// LogsAddrInUse reports whether a process log records a bind refusal.
func LogsAddrInUse(log []byte) bool {
	return bytes.Contains(log, []byte(addrInUseText))
}

// StartRetrying calls start until it returns a result or an error other than
// ErrAddrInUse, at most StartAttempts times, and fails t when no attempt
// succeeds. start chooses fresh addresses on every call. name labels the
// process in the failure message.
func StartRetrying[T any](t testing.TB, name string, start func() (T, error)) T {
	t.Helper()
	var lastErr error
	for attempt := 1; attempt <= StartAttempts; attempt++ {
		v, err := start()
		if err == nil {
			return v
		}
		if !errors.Is(err, ErrAddrInUse) {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: start attempt %d of %d lost its address to another process; retrying on fresh ports: %v",
			name, attempt, StartAttempts, err)
		lastErr = err
	}
	t.Fatalf("%s: every one of %d start attempts lost its address to another process; last: %v",
		name, StartAttempts, lastErr)
	var zero T
	return zero
}

// Holder says who holds the TCP listener on an address, as Held reads it.
type Holder int

const (
	// HolderUnknown means the platform does not expose socket ownership
	// (no /proc), so the caller cannot tell.
	HolderUnknown Holder = iota
	// HolderNone means no socket listens on the port.
	HolderNone
	// HolderProcess means the process named by pid holds the listener.
	HolderProcess
	// HolderOther means a listener on the port belongs to another process.
	HolderOther
)

// String names the holder for test messages.
func (h Holder) String() string {
	switch h {
	case HolderNone:
		return "none"
	case HolderProcess:
		return "process"
	case HolderOther:
		return "other"
	default:
		return "unknown"
	}
}

// Held reports who holds the IPv4 TCP listener on addr's port: the process
// pid, another process, or nobody. A listener on the wildcard address counts,
// because it also refuses a bind of the loopback address on that port. It
// reads /proc/net/tcp for the listening sockets' inodes and /proc/<pid>/fd for
// the process's sockets, and returns HolderUnknown when either cannot be read,
// so a caller on a platform without /proc falls back to its probe alone.
func Held(pid int, addr string) Holder {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return HolderUnknown
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return HolderUnknown
	}
	inodes, err := listeningInodes("/proc/net/tcp", port)
	if err != nil {
		return HolderUnknown
	}
	if len(inodes) == 0 {
		return HolderNone
	}
	owned, err := socketInodes(pid)
	if err != nil {
		return HolderUnknown
	}
	for _, ino := range inodes {
		if owned[ino] {
			return HolderProcess
		}
	}
	return HolderOther
}

// tcpListen is the st column value of a socket in the LISTEN state.
const tcpListen = "0A"

// listeningInodes returns the inodes of the listening sockets in the
// /proc/net/tcp table at path whose local address is the loopback or the
// wildcard address on port.
func listeningInodes(path string, port int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	want := fmt.Sprintf("%04X", port)
	var out []string
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		// sl local_address rem_address st tx_queue:rx_queue tr:tm->when
		// retrnsmt uid timeout inode ...
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 || fields[3] != tcpListen {
			continue
		}
		ip, p, ok := strings.Cut(fields[1], ":")
		if !ok || p != want {
			continue
		}
		// The address is the little-endian hex of the IPv4 address.
		if ip != "0100007F" && ip != "00000000" {
			continue
		}
		out = append(out, fields[9])
	}
	return out, sc.Err()
}

// socketInodes returns the inodes of the sockets pid holds open.
func socketInodes(pid int) (map[string]bool, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // the descriptor closed while we read the directory
		}
		if ino, ok := strings.CutPrefix(target, "socket:["); ok {
			out[strings.TrimSuffix(ino, "]")] = true
		}
	}
	return out, nil
}

// Ready reports whether pid has bound every address in addrs. On a platform
// where ownership cannot be read it reports true and leaves readiness to the
// caller's probe.
func Ready(pid int, addrs ...string) bool {
	for _, a := range addrs {
		switch Held(pid, a) {
		case HolderProcess, HolderUnknown:
		default:
			return false
		}
	}
	return true
}
