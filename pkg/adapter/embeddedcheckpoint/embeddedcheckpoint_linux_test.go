// SPDX-License-Identifier: MIT

//go:build linux

package embeddedcheckpoint

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// stubProcess models the kernel for testing: tests script a series of
// state-transition runes and trigger a transition every time SIGSTOP
// or SIGCONT is delivered. The default is that SIGSTOP transitions the
// runtime to 'T' (stopped) and SIGCONT transitions it back to 'R'
// (running) — the happy path the spec describes.
type stubProcess struct {
	mu     sync.Mutex
	state  rune
	signal func(*stubProcess, int)
	// readErr, when set, causes readProcStat to return this error
	// instead of the current state.
	readErr error
	// signalErr, when set, causes sendSignal to return this error.
	signalErr error
}

func newStub() *stubProcess {
	return &stubProcess{
		state: 'R',
		signal: func(s *stubProcess, sig int) {
			switch sig {
			case int(syscall.SIGSTOP):
				s.state = 'T'
			case int(syscall.SIGCONT):
				s.state = 'R'
			}
		},
	}
}

func (s *stubProcess) sendSignal(_ int, sig int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signalErr != nil {
		return s.signalErr
	}
	if s.signal != nil {
		s.signal(s, sig)
	}
	return nil
}

func (s *stubProcess) readProcStat(_ int) (rune, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return '?', s.readErr
	}
	return s.state, nil
}

// newHelper wires a Helper against the stub.
func newHelper(t *testing.T, stub *stubProcess) (*Helper, *StuckFlag) {
	t.Helper()
	flag := &StuckFlag{}
	h := &Helper{
		PID:                4242,
		Stuck:              flag,
		WatchdogTimeout:    200 * time.Millisecond,
		PauseProbeInterval: time.Millisecond,
		PauseProbeBudget:   50 * time.Millisecond,
		SIGCONTAttempts:    3,
		SIGCONTInterval:    time.Millisecond,
		signalSender:       stub.sendSignal,
		procStatReader:     stub.readProcStat,
		clock:              func() time.Time { return time.Now().UTC() },
	}
	return h, flag
}

// spec: §4.4 lines 242, 244, 246 — embedded SIGSTOP pause/resume path.
func TestPauseTransitionsToStopped(t *testing.T) {
	stub := newStub()
	h, flag := newHelper(t, stub)
	if err := h.Pause(context.Background()); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got := stub.state; got != 'T' {
		t.Errorf("state after Pause = %q, want 'T'", got)
	}
	if flag.Load() {
		t.Errorf("Pause must not set checkpointStuck on the happy path")
	}
}

// spec: §4.4 line 250 — Pause sets checkpointStuck when /proc reports
// the process never reaches the stopped state.
func TestPauseSetsStuckWhenProcessNeverStops(t *testing.T) {
	stub := newStub()
	stub.signal = func(s *stubProcess, _ int) { /* never transitions */ }
	h, flag := newHelper(t, stub)
	err := h.Pause(context.Background())
	if !errors.Is(err, ErrCheckpointStuck) {
		t.Fatalf("Pause err = %v, want ErrCheckpointStuck", err)
	}
	if !flag.Load() {
		t.Errorf("checkpointStuck must be raised when Pause cannot confirm SIGSTOP")
	}
}

// spec: §4.4 line 246 — Resume confirms via /proc/{pid}/stat that the
// process has left the stopped state.
func TestResumeConfirmsTransitionFromStopped(t *testing.T) {
	stub := newStub()
	stub.state = 'T'
	h, flag := newHelper(t, stub)
	if err := h.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := stub.state; got != 'R' {
		t.Errorf("state after Resume = %q, want 'R'", got)
	}
	if flag.Load() {
		t.Errorf("Resume must not raise checkpointStuck on the happy path")
	}
}

// spec: §4.4 line 246, 250 — when SIGCONT confirmation exhausts the 5
// retries, the helper raises checkpointStuck immediately rather than
// waiting for the 60-second watchdog.
func TestResumeRaisesStuckWhenProcessRemainsStopped(t *testing.T) {
	stub := newStub()
	stub.state = 'T'
	// SIGCONT is delivered but the process never leaves T.
	stub.signal = func(s *stubProcess, _ int) { /* no-op */ }
	h, flag := newHelper(t, stub)
	err := h.Resume(context.Background())
	if !errors.Is(err, ErrCheckpointStuck) {
		t.Fatalf("Resume err = %v, want ErrCheckpointStuck", err)
	}
	if !flag.Load() {
		t.Errorf("checkpointStuck must be raised when SIGCONT confirmation polling exhausts")
	}
}

// spec: §4.4 line 244 — watchdog timer fires SIGCONT unconditionally
// when the snapshot stalls; the helper raises checkpointStuck.
func TestCheckpointWatchdogFiresOnStuckSnapshot(t *testing.T) {
	stub := newStub()
	h, flag := newHelper(t, stub)
	// Tighten the watchdog so the test runs fast.
	h.WatchdogTimeout = 30 * time.Millisecond

	work := func(ctx context.Context) error {
		// Stall until the watchdog cancels the context, then exit.
		<-ctx.Done()
		return ctx.Err()
	}
	err := h.Checkpoint(context.Background(), work)
	if !errors.Is(err, ErrCheckpointStuck) {
		t.Fatalf("Checkpoint err = %v, want ErrCheckpointStuck", err)
	}
	if !flag.Load() {
		t.Errorf("watchdog must raise checkpointStuck on stall")
	}
}

// spec: §4.4 line 248 — SIGCONT is sent on every exit path, including
// a snapshot error.
func TestCheckpointSendsSIGCONTOnWorkError(t *testing.T) {
	stub := newStub()
	h, _ := newHelper(t, stub)
	work := func(_ context.Context) error {
		// At this point SIGSTOP has been delivered and the process is
		// in 'T'. Returning an error should still drive Resume so the
		// process is unblocked.
		if stub.state != 'T' {
			return fmt.Errorf("process should be stopped during snapshot, got %q", stub.state)
		}
		return errors.New("snapshot failed: minio unreachable")
	}
	err := h.Checkpoint(context.Background(), work)
	if err == nil || err.Error() != "snapshot failed: minio unreachable" {
		t.Fatalf("Checkpoint err = %v, want the work error to surface", err)
	}
	if stub.state != 'R' {
		t.Errorf("SIGCONT must be sent on the work-error path; state = %q", stub.state)
	}
}

// spec: §4.4 line 248 — a recover() inside the checkpoint goroutine
// must set checkpointStuck rather than silently swallowing the panic.
func TestCheckpointRecoversFromWorkPanicAndRaisesStuck(t *testing.T) {
	stub := newStub()
	h, flag := newHelper(t, stub)
	work := func(_ context.Context) error {
		panic("synthetic snapshot panic")
	}
	err := h.Checkpoint(context.Background(), work)
	if err == nil {
		t.Fatal("Checkpoint must surface the panic as an error")
	}
	if !flag.Load() {
		t.Errorf("checkpointStuck must be raised when work panics")
	}
	if stub.state != 'R' {
		t.Errorf("SIGCONT must be sent on the panic path; state = %q", stub.state)
	}
}

// spec: §4.4 line 242 — happy-path Checkpoint quiesces, runs the
// snapshot, and resumes the runtime.
func TestCheckpointHappyPath(t *testing.T) {
	stub := newStub()
	h, flag := newHelper(t, stub)
	var workRan atomic.Bool
	work := func(_ context.Context) error {
		workRan.Store(true)
		if stub.state != 'T' {
			return fmt.Errorf("process must be stopped during snapshot, got %q", stub.state)
		}
		return nil
	}
	if err := h.Checkpoint(context.Background(), work); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if !workRan.Load() {
		t.Errorf("snapshot work must run between SIGSTOP and SIGCONT")
	}
	if stub.state != 'R' {
		t.Errorf("final state = %q, want 'R'", stub.state)
	}
	if flag.Load() {
		t.Errorf("happy path must leave checkpointStuck clear")
	}
}

// spec: §4.4 line 242 — Checkpoint with nil work still drives the
// pause/resume sequence; useful in tests that exercise the signal path.
func TestCheckpointWithNilWorkStillDrivesPauseResume(t *testing.T) {
	stub := newStub()
	h, _ := newHelper(t, stub)
	if err := h.Checkpoint(context.Background(), nil); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if stub.state != 'R' {
		t.Errorf("final state = %q, want 'R'", stub.state)
	}
}

// validate covers pid=0 and nil helper paths.
func TestPauseRejectsInvalidPID(t *testing.T) {
	h := &Helper{PID: 0, Stuck: &StuckFlag{}}
	if err := h.Pause(context.Background()); err == nil {
		t.Errorf("Pause must reject pid <= 0")
	}
}

func TestResumeRejectsInvalidPID(t *testing.T) {
	h := &Helper{PID: 0, Stuck: &StuckFlag{}}
	if err := h.Resume(context.Background()); err == nil {
		t.Errorf("Resume must reject pid <= 0")
	}
}

// signalSender errors propagate as wrapped Pause/Resume failures.
func TestPauseSurfacesSignalError(t *testing.T) {
	stub := newStub()
	stub.signalErr = errors.New("permission denied")
	h, _ := newHelper(t, stub)
	err := h.Pause(context.Background())
	if err == nil {
		t.Fatalf("Pause must surface signal errors")
	}
}

func TestResumeSurfacesSignalError(t *testing.T) {
	stub := newStub()
	stub.signalErr = errors.New("permission denied")
	h, _ := newHelper(t, stub)
	err := h.Resume(context.Background())
	if err == nil {
		t.Fatalf("Resume must surface signal errors")
	}
}

// readProcStatLinux parses real /proc/<self>/stat without crashing.
func TestReadProcStatLinuxParsesSelf(t *testing.T) {
	state, err := readProcStatLinux(syscallGetpid())
	if err != nil {
		t.Fatalf("readProcStatLinux: %v", err)
	}
	switch state {
	case 'R', 'S', 'D', 'T', 't', 'Z':
		// any of these are valid Linux process states
	default:
		t.Errorf("self state = %q, expected a valid /proc state character", state)
	}
}

// syscallGetpid uses syscall.Getpid to avoid pulling os.Getpid into a
// test that already imports syscall.
func syscallGetpid() int { return syscall.Getpid() }
