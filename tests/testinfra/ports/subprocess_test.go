// SPDX-License-Identifier: MIT

package ports

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// requireProcNet skips a test that reads socket ownership on a platform
// without /proc/net/tcp.
func requireProcNet(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skipf("blocked: socket ownership needs /proc/net/tcp, absent on %s: %v", runtime.GOOS, err)
	}
}

// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §17.10 (Flake budget)
//
// TestHeldNamesTheHolderOfALoopbackListener pins the ownership read the
// subprocess harnesses use to refuse a readiness probe another process
// answered (TEST-GAPS T-17.4.38): a listener this process holds is the
// process's, the same listener read for a child process is another's, a
// released port has no holder, a malformed address is unknown, and a
// listener read for an exited process is another's (TEST-GAPS T-17.4.40).
func TestHeldNamesTheHolderOfALoopbackListener(t *testing.T) {
	requireProcNet(t)
	ln := NewListener(t)
	addr := ln.Addr().String()

	if got := Held(os.Getpid(), addr); got != HolderProcess {
		t.Errorf("Held(self, own listener) = %v, want process", got)
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Skipf("blocked: cannot start a child process: %v", err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if got := Held(child.Process.Pid, addr); got != HolderOther {
		t.Errorf("Held(child, parent's listener) = %v, want other", got)
	}
	if got := Ready(child.Process.Pid, addr); got {
		t.Errorf("Ready(child, parent's listener) = true, want false")
	}
	if got := Ready(os.Getpid(), addr); !got {
		t.Errorf("Ready(self, own listener) = false, want true")
	}

	free := Reserve(t)
	if got := Held(os.Getpid(), free); got != HolderNone {
		t.Errorf("Held(self, released port) = %v, want none", got)
	}
	if got := Ready(os.Getpid(), free); got {
		t.Errorf("Ready(self, released port) = true, want false")
	}
	for _, bad := range []string{"no-port", "127.0.0.1:http"} {
		if got := Held(os.Getpid(), bad); got != HolderUnknown {
			t.Errorf("Held(self, %q) = %v, want unknown", bad, got)
		}
	}
	// A pid with no /proc entry has exited, so a listener on the port is
	// another process's and the exited process is not ready on it.
	if got := Held(1<<30, addr); got != HolderOther {
		t.Errorf("Held(missing pid, listener) = %v, want other", got)
	}
	if got := Ready(1<<30, addr); got {
		t.Errorf("Ready(missing pid, listener) = true, want false")
	}
	if got := Held(1<<30, free); got != HolderNone {
		t.Errorf("Held(missing pid, released port) = %v, want none", got)
	}
}

// spec: TESTING.md §12.4 (Tier 4 — Integration)
//
// TestListeningInodesReadsOnlyLoopbackAndWildcardListeners pins the
// /proc/net/tcp parse: a LISTEN row on the loopback or the wildcard address
// on the port counts, and a row on another port, another address, or in
// another state does not.
func TestListeningInodesReadsOnlyLoopbackAndWildcardListeners(t *testing.T) {
	const port = 0x1F90
	table := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode",
		"   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0 100 0 0 10 0",
		"   1: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 222 1 0 100 0 0 10 0",
		"   2: 0100007F:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 333 1 0 100 0 0 10 0",
		"   3: 0100007F:1F90 0100007F:9C40 01 00000000:00000000 00:00000000 00000000  1000        0 444 1 0 100 0 0 10 0",
		"   4: 0A00000A:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 555 1 0 100 0 0 10 0",
		"   5: short",
		"",
	}, "\n")
	path := filepath.Join(t.TempDir(), "tcp")
	if err := os.WriteFile(path, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := listeningInodes(path, port)
	if err != nil {
		t.Fatalf("listeningInodes: %v", err)
	}
	if fmt.Sprint(got) != "[111 222]" {
		t.Errorf("listeningInodes = %v, want [111 222]", got)
	}
	if _, err := listeningInodes(filepath.Join(t.TempDir(), "absent"), port); err == nil {
		t.Errorf("listeningInodes of a missing table = nil error, want one")
	}
}

// spec: TESTING.md §12.4 (Tier 4 — Integration)
//
// TestLogsAddrInUseMatchesTheBindRefusal pins the log match the harnesses
// read after a process exits: Go's EADDRINUSE text matches, and another
// startup failure does not.
func TestLogsAddrInUseMatchesTheBindRefusal(t *testing.T) {
	t.Parallel()
	_, err := net.Listen("tcp", NewListener(t).Addr().String())
	if err == nil {
		t.Fatal("a second bind of a held address succeeded")
	}
	if !LogsAddrInUse([]byte("lenny-gateway: listen: " + err.Error() + "\n")) {
		t.Errorf("LogsAddrInUse(%q) = false, want true", err.Error())
	}
	if LogsAddrInUse([]byte("lenny-gateway: refusing to start: --redis-url is plaintext\n")) {
		t.Errorf("LogsAddrInUse(another failure) = true, want false")
	}
}

// fatalRecorder is a testing.TB whose Fatalf records the message and ends
// the calling goroutine, as the real one does.
type fatalRecorder struct {
	testing.TB
	mu    sync.Mutex
	fatal string
	logs  int
}

func (r *fatalRecorder) Helper() {}

func (r *fatalRecorder) Logf(string, ...any) {
	r.mu.Lock()
	r.logs++
	r.mu.Unlock()
}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.fatal = fmt.Sprintf(format, args...)
	r.mu.Unlock()
	runtime.Goexit()
}

// runRetrying runs StartRetrying against rec on its own goroutine, so a
// Fatalf that ends the goroutine does not end the test.
func runRetrying(rec *fatalRecorder, start func() (int, error)) (int, bool) {
	var (
		got      int
		returned bool
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		got = StartRetrying(rec, "proc", start)
		returned = true
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		panic("StartRetrying did not return")
	}
	return got, returned
}

// spec: TESTING.md §12.4 (Tier 4 — Integration), TESTING.md §17.10 (Flake budget)
//
// TestStartRetryingRetriesOnlyABindRefusal pins the retry the subprocess
// harnesses run (TEST-GAPS T-17.4.38): a start that lost its address is
// retried until one succeeds, a start that failed another way fails the test
// at once, and a start that loses its address on every attempt fails the
// test after StartAttempts attempts.
func TestStartRetryingRetriesOnlyABindRefusal(t *testing.T) {
	t.Parallel()
	inUse := fmt.Errorf("gateway exited: %w", ErrAddrInUse)

	t.Run("succeeds after refusals", func(t *testing.T) {
		rec := &fatalRecorder{TB: t}
		calls := 0
		got, ok := runRetrying(rec, func() (int, error) {
			calls++
			if calls < 3 {
				return 0, inUse
			}
			return 7, nil
		})
		if !ok || got != 7 || calls != 3 || rec.fatal != "" || rec.logs != 2 {
			t.Errorf("returned=%v value=%d calls=%d fatal=%q logs=%d, want true 7 3 \"\" 2", ok, got, calls, rec.fatal, rec.logs)
		}
	})
	t.Run("other error fails at once", func(t *testing.T) {
		rec := &fatalRecorder{TB: t}
		calls := 0
		_, ok := runRetrying(rec, func() (int, error) {
			calls++
			return 0, errors.New("gateway exited before it served: exit status 2")
		})
		if ok || calls != 1 || !strings.Contains(rec.fatal, "exit status 2") {
			t.Errorf("returned=%v calls=%d fatal=%q, want false 1 and the cause", ok, calls, rec.fatal)
		}
	})
	t.Run("every attempt refused", func(t *testing.T) {
		rec := &fatalRecorder{TB: t}
		calls := 0
		_, ok := runRetrying(rec, func() (int, error) {
			calls++
			return 0, inUse
		})
		if ok || calls != StartAttempts || !strings.Contains(rec.fatal, "every one of") {
			t.Errorf("returned=%v calls=%d fatal=%q, want false %d and the exhaustion", ok, calls, rec.fatal, StartAttempts)
		}
	})
}
