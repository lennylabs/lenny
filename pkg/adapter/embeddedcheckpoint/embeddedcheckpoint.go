// SPDX-License-Identifier: MIT

// Package embeddedcheckpoint implements the §4.4 SIGSTOP/SIGCONT
// embedded-adapter checkpoint helper.
//
// In the embedded deployment model the adapter runs in the same
// container as the agent process, so the two share a PID namespace
// and the adapter can deliver signals to freeze the runtime while a
// workspace snapshot is captured. The §4.4 contract is:
//
//   - Pause the runtime with SIGSTOP, then poll `/proc/<pid>/stat`
//     until the kernel reports the process is stopped (state `T`).
//   - Capture the snapshot.
//   - Resume the runtime with SIGCONT, then poll `/proc/<pid>/stat`
//     up to 5 times at 100 ms intervals until the process leaves the
//     stopped state (no longer `T`/`t`). When the poll budget is
//     exhausted, set the shared `checkpointStuck` flag so the
//     adapter's `/healthz` liveness probe returns 503.
//   - Bound the entire path with a 60-second watchdog timer that
//     unconditionally sends SIGCONT and marks the checkpoint failed.
//
// Linux is the only supported host; the spec's `/proc/{pid}/stat`
// dependency makes the path Linux-specific. On other platforms the
// package exports stubs that return ErrNotSupported. The build-tag
// split mirrors the existing peercred_linux/other split in pkg/adapter.
//
// spec: §4.4 lines 242, 244, 246, 250 — embedded-adapter SIGSTOP path,
// 60s watchdog, SIGCONT confirmation via /proc, /healthz integration.
package embeddedcheckpoint

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

// ErrNotSupported reports that the embedded-checkpoint path is
// unavailable on this host. The spec restricts the SIGSTOP path to
// Linux because it depends on `/proc/{pid}/stat`; macOS, Windows, and
// every other non-Linux target return this from Pause/Resume/Checkpoint.
//
// spec: §4.4 line 246 — "On non-Linux hosts where /proc/{pid}/stat is
// unavailable, the adapter skips polling".
var ErrNotSupported = errors.New("embeddedcheckpoint: not supported on this platform")

// ErrCheckpointStuck reports that the embedded-checkpoint helper
// observed an unrecoverable state: either the SIGCONT confirmation
// budget was exhausted (process still in `T`/`t` after 5 retries) or
// the 60-second watchdog fired. The adapter sets its shared
// `checkpointStuck` atomic flag so `/healthz` returns 503 and
// Kubernetes restarts the pod.
//
// spec: §4.4 line 250 — "When `checkpointStuck` is set, the /healthz
// endpoint returns HTTP 503".
var ErrCheckpointStuck = errors.New("embeddedcheckpoint: checkpoint stuck")

// DefaultWatchdogTimeout is the §4.4 watchdog budget bounding the
// Pause→snapshot→Resume window. A SIGSTOP held longer than this is
// considered stuck and the helper sends SIGCONT unconditionally
// (`os.Process.Signal(syscall.SIGCONT)`), marks the checkpoint failed,
// and raises `checkpointStuck`.
//
// spec: §4.4 line 244 — "a 60-second watchdog timer starts when
// SIGSTOP is sent; if the checkpoint does not complete within that
// window, SIGCONT is sent unconditionally".
const DefaultWatchdogTimeout = 60 * time.Second

// DefaultPauseProbeInterval is the polling cadence for confirming the
// process has entered the stopped state after SIGSTOP. The spec allows
// a backoff up to 1 second total; the helper polls in 50 ms intervals
// so a healthy stop is observed in one or two ticks while the worst-case
// wait is still bounded under 1 second.
const DefaultPauseProbeInterval = 50 * time.Millisecond

// DefaultPauseProbeBudget is the upper bound on `Pause` waiting for the
// process state to transition to stopped after `SIGSTOP`. Spec §4.4
// allows up to ~1 s with backoff; the helper uses 1 s as a hard cap
// so a kernel-scheduling delay does not stall the watchdog.
const DefaultPauseProbeBudget = 1 * time.Second

// SIGCONTConfirmAttempts is the §4.4 line 246 SIGCONT confirmation
// retry budget. After sending SIGCONT the helper polls
// `/proc/{pid}/stat` up to 5 times before declaring the resume failed.
//
// spec: §4.4 line 246 — "The adapter polls /proc/{pid}/stat up to 5
// times with 100 ms intervals".
const SIGCONTConfirmAttempts = 5

// SIGCONTConfirmInterval is the §4.4 line 246 SIGCONT confirmation
// polling interval (100 ms between retries).
const SIGCONTConfirmInterval = 100 * time.Millisecond

// CheckpointWork is the snapshot phase the caller wants the helper to
// run between Pause and Resume. The implementation typically archives
// the workspace directory and uploads it to the artifact store. A nil
// CheckpointWork causes Checkpoint to skip directly to Resume so the
// helper can be exercised in tests without a real snapshot path.
type CheckpointWork func(ctx context.Context) error

// StuckFlag is the shared atomic flag the adapter's `/healthz` handler
// reads. The embedded-checkpoint helper sets it whenever the SIGCONT
// confirmation budget is exhausted or the watchdog fires; the adapter
// reads the same flag from its liveness handler so Kubernetes restarts
// the pod once a stuck checkpoint is observed.
//
// The type wraps atomic.Bool so the helper does not introduce an
// import dependency from the adapter's liveness package onto the
// embedded-checkpoint package directly — the adapter owns the
// `*atomic.Bool` and passes it in.
//
// spec: §4.4 line 250 — "The adapter maintains a shared atomic flag
// (`checkpointStuck`)".
type StuckFlag = atomic.Bool

// Helper drives the §4.4 SIGSTOP/SIGCONT checkpoint sequence. It is
// goroutine-safe per-helper: a single helper drives one runtime
// process at a time. The watchdog and SIGCONT confirmation are bounded
// by the configured timeouts; both default to the spec-mandated
// 60 s / 5×100 ms budgets.
//
// On non-Linux hosts every method returns ErrNotSupported.
type Helper struct {
	// PID is the runtime process the helper signals. Required.
	PID int

	// Stuck is the shared `checkpointStuck` flag the adapter wires
	// into its /healthz handler. Required.
	Stuck *StuckFlag

	// WatchdogTimeout overrides DefaultWatchdogTimeout for tests. Zero
	// selects the default.
	WatchdogTimeout time.Duration

	// PauseProbeInterval overrides DefaultPauseProbeInterval. Zero
	// selects the default.
	PauseProbeInterval time.Duration

	// PauseProbeBudget overrides DefaultPauseProbeBudget. Zero selects
	// the default.
	PauseProbeBudget time.Duration

	// SIGCONTAttempts overrides SIGCONTConfirmAttempts. Zero selects
	// the spec-mandated 5.
	SIGCONTAttempts int

	// SIGCONTInterval overrides SIGCONTConfirmInterval. Zero selects
	// the spec-mandated 100 ms.
	SIGCONTInterval time.Duration

	// procStatReader overrides the production reader of
	// /proc/{pid}/stat. Tests inject a stub. Nil selects the real
	// /proc reader on Linux.
	procStatReader procStatReader

	// signalSender overrides the production signal sender. Tests
	// inject a stub. Nil selects the real os.Process signaller on
	// Linux.
	signalSender signalSender

	// clock overrides time.Now for tests.
	clock func() time.Time
}

// procStatReader returns the process state field (field 3 of
// /proc/<pid>/stat). Returning ('?', err) signals the read failed.
type procStatReader func(pid int) (rune, error)

// signalSender delivers a UNIX signal to the supplied pid. The default
// implementation wraps os.FindProcess + Signal; tests inject a stub.
type signalSender func(pid int, signal int) error

// watchdogTimeout returns the helper's effective watchdog budget.
func (h *Helper) watchdogTimeout() time.Duration {
	if h.WatchdogTimeout > 0 {
		return h.WatchdogTimeout
	}
	return DefaultWatchdogTimeout
}

func (h *Helper) pauseProbeInterval() time.Duration {
	if h.PauseProbeInterval > 0 {
		return h.PauseProbeInterval
	}
	return DefaultPauseProbeInterval
}

func (h *Helper) pauseProbeBudget() time.Duration {
	if h.PauseProbeBudget > 0 {
		return h.PauseProbeBudget
	}
	return DefaultPauseProbeBudget
}

func (h *Helper) sigcontAttempts() int {
	if h.SIGCONTAttempts > 0 {
		return h.SIGCONTAttempts
	}
	return SIGCONTConfirmAttempts
}

func (h *Helper) sigcontInterval() time.Duration {
	if h.SIGCONTInterval > 0 {
		return h.SIGCONTInterval
	}
	return SIGCONTConfirmInterval
}

func (h *Helper) now() time.Time {
	if h.clock != nil {
		return h.clock()
	}
	return time.Now().UTC()
}

// LivenessHandler returns an HTTP handler that implements the §4.4
// line 250 `/healthz` integration: it returns HTTP 503 with
// `{"status":"unhealthy","reason":"checkpoint_stuck"}` when the shared
// `Stuck` flag is set and HTTP 200 with
// `{"status":"healthy"}` otherwise. The adapter wires this handler
// against its `/healthz` route so a stuck checkpoint triggers a
// Kubernetes pod restart.
//
// The handler is goroutine-safe — it only reads the atomic flag and
// writes a fixed-size response body — and stateless, so the same
// handler can be mounted under multiple HTTP servers.
//
// spec: §4.4 line 250 — "When `checkpointStuck` is set, the /healthz
// endpoint returns HTTP 503 with `{"status": "unhealthy", "reason":
// "checkpoint_stuck"}`".
func LivenessHandler(stuck *StuckFlag) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if stuck != nil && stuck.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unhealthy","reason":"checkpoint_stuck"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})
}
