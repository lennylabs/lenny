// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"bytes"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// lockedBuffer is a stderr stand-in the signal goroutine and the test share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitSignalCount waits until st has counted name at least once.
func waitSignalCount(t *testing.T, st *state, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st.record().Signals[name] > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("signal %q was not counted; counts %v", name, st.record().Signals)
}

// spec: §4.7.10 (Runtime supervisor, Signals)
//
// diagnosis: the probe never registered the fault signals or SIGPIPE, so Go
// dropped a kill()-sent copy and a supervisor that forwarded one to the
// runtime process passed the tier-9 signal case; or the probe listed SIGURG
// as counted while the Go runtime owned it. Read countedSignals.
func TestCountedSignalsCoverTheFaultSignalsAndSIGPIPE(t *testing.T) {
	cases := []struct {
		name          string
		godebug       string
		wantURG       bool
		wantUncounted []string
	}{
		{name: "preemption on", godebug: "", wantURG: false, wantUncounted: []string{syscall.SIGURG.String()}},
		{name: "other settings only", godebug: "madvdontneed=1", wantURG: false, wantUncounted: []string{syscall.SIGURG.String()}},
		{name: "preemption off", godebug: "madvdontneed=1,asyncpreemptoff=1", wantURG: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counted, uncounted := countedSignals(tc.godebug)
			for _, s := range []syscall.Signal{
				syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGFPE, syscall.SIGILL, syscall.SIGPIPE,
				syscall.SIGTERM, syscall.SIGINT, syscall.SIGCHLD, syscall.Signal(34), syscall.Signal(64),
			} {
				if !slices.Contains(counted, os.Signal(s)) {
					t.Errorf("%v is not registered, so a forwarded copy would go unseen", s)
				}
			}
			for _, s := range []syscall.Signal{syscall.SIGKILL, syscall.SIGSTOP} {
				if slices.Contains(counted, os.Signal(s)) {
					t.Errorf("uncatchable %v is registered", s)
				}
			}
			if got := slices.Contains(counted, os.Signal(syscall.SIGURG)); got != tc.wantURG {
				t.Errorf("SIGURG registered = %v, want %v", got, tc.wantURG)
			}
			if !slices.Equal(uncounted, tc.wantUncounted) {
				t.Errorf("uncounted = %v, want %v", uncounted, tc.wantUncounted)
			}
		})
	}
}

// spec: §4.7.10 (Runtime supervisor, Signals; End), §15.4 (Standard-level
// graceful shutdown)
//
// diagnosis: the serving probe counted SIGTERM and kept running, so every
// supervisor `end` ran out its grace and finished with SIGKILL, and a
// kill()-sent fault signal or SIGPIPE was dropped uncounted. Read
// countSignals and recordSignals.
func TestServingProbeCountsFaultSignalsAndExitsZeroOnSIGTERM(t *testing.T) {
	st := &state{signals: map[string]int{}, srv: &recordServer{}}
	stderr := &lockedBuffer{}
	exited := make(chan int, 1)
	st.countSignals(stderr, func(code int) { exited <- code })
	t.Cleanup(func() { signal.Stop(st.sigs) })

	for _, s := range []syscall.Signal{syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGFPE, syscall.SIGILL, syscall.SIGPIPE, syscall.SIGINT} {
		if err := syscall.Kill(syscall.Getpid(), s); err != nil {
			t.Fatalf("send %v: %v", s, err)
		}
		waitSignalCount(t, st, s.String())
	}
	select {
	case code := <-exited:
		t.Fatalf("the probe exited %d on a signal other than SIGTERM", code)
	default:
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case code := <-exited:
		if code != exitOK {
			t.Errorf("the probe exited %d on SIGTERM, want %d", code, exitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the probe did not exit on SIGTERM, so a supervisor end would wait out its grace")
	}
	if !strings.Contains(stderr.String(), SignalLogPrefix+" terminated") {
		t.Errorf("SIGTERM was not logged before the exit:\n%s", stderr.String())
	}
	if n := st.record().Signals[syscall.SIGTERM.String()]; n != 1 {
		t.Errorf("SIGTERM counted %d times, want 1", n)
	}
}
