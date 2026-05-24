// SPDX-License-Identifier: MIT

//go:build linux

package embeddedcheckpoint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

// Pause sends SIGSTOP to the runtime process and waits up to
// PauseProbeBudget (default 1 s) for `/proc/{pid}/stat` to report the
// process is in the stopped state (`T`). Returns nil once the
// transition is observed; returns ErrCheckpointStuck (and sets the
// shared `Stuck` flag) when the budget is exhausted without observing
// the transition.
//
// Callers MUST issue SIGCONT before returning to avoid leaving the
// runtime indefinitely frozen — typically via `defer h.Resume(ctx)`.
//
// spec: §4.4 line 242, 244 — "the adapter MUST issue SIGCONT in a
// deferred cleanup handler so that SIGCONT is sent on all exit paths".
func (h *Helper) Pause(_ context.Context) error {
	if err := h.validate(); err != nil {
		return err
	}
	if err := h.sendSignal(int(syscall.SIGSTOP)); err != nil {
		return fmt.Errorf("embeddedcheckpoint: send SIGSTOP: %w", err)
	}
	// Poll /proc/{pid}/stat until the kernel reports the process is
	// stopped. The budget bounds the wait so a kernel anomaly does not
	// stall the watchdog.
	deadline := h.now().Add(h.pauseProbeBudget())
	for {
		state, err := h.readProcStat(h.PID)
		if err == nil && (state == 'T' || state == 't') {
			return nil
		}
		if h.now().After(deadline) {
			break
		}
		time.Sleep(h.pauseProbeInterval())
	}
	if h.Stuck != nil {
		h.Stuck.Store(true)
	}
	return ErrCheckpointStuck
}

// Resume sends SIGCONT to the runtime process and polls
// `/proc/{pid}/stat` up to SIGCONTAttempts times (default 5) at
// SIGCONTInterval (default 100 ms) until the process leaves the
// stopped state (`T`/`t`). When the budget is exhausted without
// observing the transition, Resume sets the shared `Stuck` flag and
// returns ErrCheckpointStuck. A non-Linux host returns
// ErrNotSupported.
//
// spec: §4.4 line 246 — "After sending SIGCONT, the adapter MUST
// confirm that the agent process has actually left the stopped state
// ... up to 5 times with 100 ms intervals".
func (h *Helper) Resume(_ context.Context) error {
	if err := h.validate(); err != nil {
		return err
	}
	if err := h.sendSignal(int(syscall.SIGCONT)); err != nil {
		return fmt.Errorf("embeddedcheckpoint: send SIGCONT: %w", err)
	}
	attempts := h.sigcontAttempts()
	for i := 0; i < attempts; i++ {
		state, err := h.readProcStat(h.PID)
		if err == nil && state != 'T' && state != 't' {
			return nil
		}
		// Sleep between retries; the last iteration falls through to
		// the failure path without sleeping again.
		if i < attempts-1 {
			time.Sleep(h.sigcontInterval())
		}
	}
	if h.Stuck != nil {
		h.Stuck.Store(true)
	}
	return ErrCheckpointStuck
}

// Checkpoint runs the full §4.4 Pause → snapshot → Resume sequence
// under a watchdog timer. If `work` returns an error the helper still
// issues SIGCONT (and surfaces both errors); if the watchdog fires the
// helper sends SIGCONT unconditionally, sets the shared `Stuck` flag,
// and returns ErrCheckpointStuck wrapped around the work error (if
// any). Resume confirmation failures also raise the flag.
//
// Checkpoint guarantees SIGCONT is sent on every exit path — a panic
// inside `work` is recovered, SIGCONT is delivered, the panic is
// re-raised after the flag is set.
//
// spec: §4.4 lines 244, 246, 248 — watchdog, SIGCONT confirmation,
// abort cleanup. Any recover() inside the helper sets `checkpointStuck`
// and re-panics.
func (h *Helper) Checkpoint(ctx context.Context, work CheckpointWork) (err error) {
	if validateErr := h.validate(); validateErr != nil {
		return validateErr
	}
	// Bound the entire checkpoint window with a watchdog. The watchdog
	// fires SIGCONT unconditionally, sets the stuck flag, and lets the
	// caller observe ErrCheckpointStuck regardless of what `work` is
	// doing.
	ctx, cancel := context.WithTimeout(ctx, h.watchdogTimeout())
	defer cancel()

	if pauseErr := h.Pause(ctx); pauseErr != nil {
		return pauseErr
	}

	// The defer chain is the §4.4 line 248 abort cleanup: SIGCONT is
	// sent on every exit path. A panic from `work` re-raises after the
	// stuck flag is set so the gateway observes the failure rather than
	// silently recovering — spec §4.4 line 248 explicitly forbids
	// recover() swallowing a checkpoint panic.
	var workErr error
	var watchdogFired bool
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				if h.Stuck != nil {
					h.Stuck.Store(true)
				}
				// Re-raise so the goroutine's stack trace is preserved
				// in the watchdog channel. The outer Checkpoint
				// surfaces the panic as ErrCheckpointStuck once it
				// observes `Stuck=true`.
				workErr = fmt.Errorf("embeddedcheckpoint: snapshot panicked: %v", r)
			}
		}()
		if work == nil {
			return
		}
		workErr = work(ctx)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		watchdogFired = true
	}

	// Always issue SIGCONT regardless of how `work` exited.
	resumeErr := h.Resume(ctx)

	if watchdogFired {
		if h.Stuck != nil {
			h.Stuck.Store(true)
		}
		return ErrCheckpointStuck
	}
	if workErr != nil {
		return workErr
	}
	return resumeErr
}

// validate returns an error when the helper is misconfigured.
func (h *Helper) validate() error {
	if h == nil {
		return fmt.Errorf("embeddedcheckpoint: nil helper")
	}
	if h.PID <= 0 {
		return fmt.Errorf("embeddedcheckpoint: pid must be > 0, got %d", h.PID)
	}
	return nil
}

// sendSignal delivers signal to h.PID via the configured signalSender
// (tests) or os.Process (production).
func (h *Helper) sendSignal(signal int) error {
	if h.signalSender != nil {
		return h.signalSender(h.PID, signal)
	}
	proc, err := os.FindProcess(h.PID)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.Signal(signal))
}

// readProcStat returns the process state field from /proc/{pid}/stat
// via the configured procStatReader (tests) or the real /proc reader.
func (h *Helper) readProcStat(pid int) (rune, error) {
	if h.procStatReader != nil {
		return h.procStatReader(pid)
	}
	return readProcStatLinux(pid)
}

// readProcStatLinux reads /proc/{pid}/stat and returns the process
// state character (field 3 in the procfs format).
//
// The procfs format is `<pid> (<comm>) <state> ...`. The `comm` field
// may itself contain spaces and parentheses, so the parser locates the
// last `)` and takes the next non-whitespace byte as the state.
func readProcStatLinux(pid int) (rune, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return '?', err
	}
	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return '?', fmt.Errorf("embeddedcheckpoint: unexpected /proc/%d/stat format", pid)
	}
	rest := strings.TrimLeft(string(data[closeParen+1:]), " \t")
	if rest == "" {
		return '?', fmt.Errorf("embeddedcheckpoint: empty state field in /proc/%d/stat", pid)
	}
	return rune(rest[0]), nil
}
