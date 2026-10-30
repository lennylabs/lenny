// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// runAttack runs the attack args[0] names with the rest of args. Each attack
// is an operation the agent UID must not be able to complete against the
// supervisor, so OK reports a breach, except for AttackSignalPID1 (the
// kernel accepts the signal; the supervisor's reaction is what the test
// observes) and AttackBindSupervise (a bind may succeed once the adapter has
// closed its listener; the connection count is what the test observes).
// spec: §4.7.11 item 8 (Runtime supervisor); §4.7.10 (Runtime supervisor,
// Signals).
func runAttack(args []string) AttackResult {
	if len(args) == 0 {
		return AttackResult{Detail: "no attack named"}
	}
	name, rest := args[0], args[1:]
	switch name {
	case AttackPtracePID1:
		return attackResult(name, unix.PtraceAttach(1))
	case AttackReadProc1Mem:
		return readProc1Mem()
	case AttackReadProc1FD:
		_, err := os.ReadDir("/proc/1/fd")
		return attackResult(name, err)
	case AttackWriteSupervisor:
		err := os.WriteFile(filepath.Join(SupervisorMount, "probe-write"), []byte("x"), 0o600)
		return attackResult(name, err)
	case AttackSignalPID1:
		return signalPID1(rest)
	case AttackBindSupervise:
		return squatSupervise(rest)
	case AttackForkBomb:
		return forkBomb(rest)
	default:
		return AttackResult{Attack: name, Detail: "unknown attack"}
	}
}

// attackResult builds an AttackResult from an operation's error.
func attackResult(name string, err error) AttackResult {
	r := AttackResult{Attack: name, OK: err == nil}
	if err != nil {
		r.Errno = errnoName(err)
	}
	return r
}

// readProc1Mem opens the supervisor's memory. The supervisor is
// non-dumpable, so the open must fail at the agent UID, and a successful
// open is the breach whatever a following read returns. spec: §4.7.11
// item 8 (Property self-test).
func readProc1Mem() AttackResult {
	return readProcMem("/proc/1/mem", "/proc/1/maps")
}

// readProcMem opens memPath and reports a successful open as the breach.
// It then confirms the breach by reading one byte at the first readable
// mapping mapsPath lists, and records the read's outcome in Detail without
// changing OK, because the supervisor need not map any fixed address.
func readProcMem(memPath, mapsPath string) AttackResult {
	f, err := os.Open(memPath)
	if err != nil {
		return attackResult(AttackReadProc1Mem, err)
	}
	defer f.Close()
	r := AttackResult{Attack: AttackReadProc1Mem, OK: true, Detail: "open succeeded"}
	addr, err := firstReadableMapping(mapsPath)
	if err != nil {
		r.Detail += "; no readable mapping: " + err.Error()
		return r
	}
	if _, err := f.ReadAt(make([]byte, 1), addr); err != nil {
		r.Detail += fmt.Sprintf("; read at %#x: %s", addr, errnoName(err))
		return r
	}
	r.Detail += fmt.Sprintf("; read at %#x succeeded", addr)
	return r
}

// firstReadableMapping returns the start address of the first mapping in
// the /proc/<pid>/maps file at path whose permissions allow reads.
func firstReadableMapping(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "r") {
			continue
		}
		start, _, ok := strings.Cut(fields[0], "-")
		if !ok {
			continue
		}
		addr, err := strconv.ParseInt(start, 16, 64)
		if err != nil {
			continue
		}
		return addr, nil
	}
	return 0, fmt.Errorf("%s lists no readable mapping", path)
}

// signalPID1 sends the named signal to PID 1. args[0] is a signal number.
func signalPID1(args []string) AttackResult {
	if len(args) == 0 {
		return AttackResult{Attack: AttackSignalPID1, Detail: "no signal named"}
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return AttackResult{Attack: AttackSignalPID1, Detail: fmt.Sprintf("signal %q: %v", args[0], err)}
	}
	r := attackResult(AttackSignalPID1, unix.Kill(1, syscall.Signal(n)))
	r.Detail = unix.SignalName(syscall.Signal(n))
	return r
}

// squatSupervise binds the CH-SUPERVISE socket name and counts the
// connections it accepts within the wait args[0] names (seconds, default
// 10). The adapter accepts the supervisor's connection once and closes its
// listener, and the supervisor dials only at its start, so a squatter
// receives nothing. spec: §28.5.3 (CH-SUPERVISE, Endpoint).
func squatSupervise(args []string) AttackResult {
	wait := 10 * time.Second
	if len(args) > 0 {
		if s, err := strconv.Atoi(args[0]); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
	}
	ln, err := net.Listen("unix", abstractAddr(SuperviseSocket))
	if err != nil {
		return attackResult(AttackBindSupervise, err)
	}
	defer ln.Close()
	r := AttackResult{Attack: AttackBindSupervise, OK: true}
	deadline := time.Now().Add(wait)
	ul := ln.(*net.UnixListener)
	for time.Now().Before(deadline) {
		_ = ul.SetDeadline(deadline)
		conn, err := ul.Accept()
		if err != nil {
			break
		}
		r.Connections++
		_ = conn.Close()
	}
	return r
}

// forkBomb starts sleeping children until a start fails or the cap args[0]
// names (default 4096) is reached, reports how many started, and kills
// them. A pids limit on the container stops the loop before the cap.
func forkBomb(args []string) AttackResult {
	limit := 4096
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			limit = n
		}
	}
	var started []*os.Process
	defer func() {
		for _, p := range started {
			_ = p.Kill()
			_, _ = p.Wait()
		}
	}()
	var lastErr error
	for len(started) < limit {
		cmd := exec.Command(selfExe(), subDaemonGrandchild)
		if err := cmd.Start(); err != nil {
			lastErr = err
			break
		}
		started = append(started, cmd.Process)
	}
	r := AttackResult{Attack: AttackForkBomb, OK: lastErr == nil, Detail: strconv.Itoa(len(started))}
	if lastErr != nil {
		r.Errno = errnoName(lastErr)
	}
	return r
}
