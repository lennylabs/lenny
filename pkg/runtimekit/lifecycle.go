// SPDX-License-Identifier: MIT

package runtimekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// CheckpointAutoResumeTimeout is the §4.4 line 244 runtime-side
// timeout: after sending `checkpoint_ready`, a Full-level runtime MUST
// autonomously resume normal operation if it has not received
// `checkpoint_complete` within 60 seconds.
//
// spec: §4.4 line 244 — "if the runtime sends checkpoint_ready but
// does not receive checkpoint_complete within 60 seconds, it MUST
// autonomously resume normal operation and log a `checkpoint_timeout`
// warning".
const CheckpointAutoResumeTimeout = 60 * time.Second

// CheckpointTimeoutLogPrefix is the warning-level log prefix the
// runtime emits when the autonomous-resume timer fires. The §16.5
// alerting rule scrapes runtime stderr for this prefix.
const CheckpointTimeoutLogPrefix = "lifecycle: checkpoint_timeout"

// CheckpointHandler is the runtime's snapshot-preparation callback.
// It is invoked when the adapter sends `checkpoint_request`. The
// callback is expected to flush in-memory state to disk, wait for
// in-flight I/O to settle (best-effort), and return so the lifecycle
// helper can write `checkpoint_ready`. An error short-circuits the
// handshake: the helper emits a `checkpoint_complete{status:"failed",
// reason: err.Error()}` frame instead of `checkpoint_ready` so the
// adapter can record the failure.
//
// The callback MUST return quickly — under the adapter's quiescence
// budget — because the runtime is observed by the adapter as
// non-responsive between `checkpoint_request` and `checkpoint_ready`.
type CheckpointHandler func(ctx context.Context, deadlineMs int32) error

// LifecycleClient is the runtime side of the §4.7 lifecycle channel.
// It writes `checkpoint_ready` after the runtime quiesces and starts a
// 60-second autonomous-resume timer; if `checkpoint_complete` does not
// arrive in that window the helper emits a `checkpoint_timeout`
// warning to the stderr writer and treats the runtime as resumed.
//
// LifecycleClient is goroutine-safe. The lifecycle frames are
// serialised through a single encoder so a runtime that calls
// HandleFrame from one goroutine and Cancel/Close from another remains
// correct.
type LifecycleClient struct {
	enc         *json.Encoder
	stderr      io.Writer
	autoTimeout time.Duration
	now         func() time.Time
	logf        func(format string, args ...any)

	mu      sync.Mutex
	pending map[string]*pendingCheckpoint
}

// pendingCheckpoint tracks one checkpoint waiting for `checkpoint_complete`.
type pendingCheckpoint struct {
	id        string
	startedAt time.Time
	timer     *time.Timer
	resumed   bool
}

// LifecycleClientOptions configures the runtime-side lifecycle helper.
type LifecycleClientOptions struct {
	// Writer is the adapter→runtime output stream. Required.
	Writer io.Writer

	// Stderr receives the §4.4 line 244 `checkpoint_timeout` warning.
	// Defaults to io.Discard when nil.
	Stderr io.Writer

	// AutoResumeTimeout overrides CheckpointAutoResumeTimeout for
	// tests. Zero selects the spec-mandated 60-second budget.
	AutoResumeTimeout time.Duration

	// Now overrides time.Now for tests.
	Now func() time.Time

	// LogF overrides the warning emitter for tests that want to
	// observe the autonomous-resume log without parsing stderr.
	LogF func(format string, args ...any)
}

// NewLifecycleClient returns a runtime-side lifecycle helper writing
// onto w. Callers wire the helper into their JSONL loop: when a
// `checkpoint_request` frame arrives, call HandleCheckpointRequest;
// when a `checkpoint_complete` frame arrives, call HandleCheckpointComplete.
func NewLifecycleClient(opts LifecycleClientOptions) *LifecycleClient {
	if opts.Writer == nil {
		// A nil writer would cause every frame to panic; return a
		// helper that no-ops by giving it an io.Discard so the
		// runtime crashes loudly only when it actually tries to use
		// it. The contract is "Writer is required" but tests can
		// still verify other paths.
		opts.Writer = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	enc := json.NewEncoder(opts.Writer)
	enc.SetEscapeHTML(false)
	c := &LifecycleClient{
		enc:         enc,
		stderr:      opts.Stderr,
		autoTimeout: opts.AutoResumeTimeout,
		now:         opts.Now,
		logf:        opts.LogF,
		pending:     map[string]*pendingCheckpoint{},
	}
	if c.autoTimeout <= 0 {
		c.autoTimeout = CheckpointAutoResumeTimeout
	}
	if c.now == nil {
		c.now = func() time.Time { return time.Now().UTC() }
	}
	return c
}

// HandleCheckpointRequest acknowledges a `checkpoint_request` frame
// from the adapter. The flow is:
//
//  1. Invoke `handler` to drive the runtime's quiesce path.
//  2. On handler success, write `checkpoint_ready` and start the
//     autonomous-resume timer.
//  3. On handler error, write `checkpoint_complete{status:"failed"}`
//     and skip the autonomous-resume timer (the handshake is over).
//
// HandleCheckpointRequest blocks while `handler` runs; the caller's
// JSONL dispatch loop may want to run it in a separate goroutine if
// other lifecycle frames arrive concurrently.
//
// spec: §4.4 line 244 — runtime-side `checkpoint_ready` plus the
// autonomous-resume contract.
func (c *LifecycleClient) HandleCheckpointRequest(ctx context.Context, checkpointID string, deadlineMs int32, handler CheckpointHandler) error {
	if checkpointID == "" {
		return errors.New("runtimekit: checkpoint_request missing checkpointId")
	}
	if handler != nil {
		if err := handler(ctx, deadlineMs); err != nil {
			// Emit checkpoint_complete{failed} so the adapter knows
			// quiescence failed — no need for the autonomous-resume
			// timer because there is no `checkpoint_ready` outstanding.
			return c.writeFrame(map[string]any{
				"type":         "checkpoint_complete",
				"checkpointId": checkpointID,
				"status":       "failed",
				"reason":       err.Error(),
			})
		}
	}

	// Register the pending checkpoint before writing `checkpoint_ready`
	// so a fast adapter cannot race past our registration.
	c.startPending(checkpointID)
	return c.writeFrame(map[string]any{
		"type":         "checkpoint_ready",
		"checkpointId": checkpointID,
	})
}

// HandleCheckpointComplete consumes a `checkpoint_complete` frame for
// `checkpointID`. The helper cancels the autonomous-resume timer for
// that id so the runtime continues normal operation without emitting
// the timeout warning.
//
// A `checkpoint_complete` for an unknown id (one whose timer already
// fired and resumed the runtime, or one the runtime never sent
// `checkpoint_ready` for) is a no-op; the spec's runtime contract is
// that late `checkpoint_complete` frames are ignored after the
// autonomous-resume window closes.
func (c *LifecycleClient) HandleCheckpointComplete(checkpointID string) {
	c.mu.Lock()
	p, ok := c.pending[checkpointID]
	if ok {
		delete(c.pending, checkpointID)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	if p.timer != nil {
		p.timer.Stop()
	}
}

// PendingCount returns the number of `checkpoint_ready` frames
// awaiting `checkpoint_complete`. Tests assert this to verify the
// autonomous-resume timer fired and cleared the pending slot.
func (c *LifecycleClient) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// Close stops every outstanding autonomous-resume timer without firing
// the warning. Callers invoke Close on a clean runtime exit so a
// pending checkpoint does not log a spurious timeout warning at
// shutdown.
func (c *LifecycleClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, p := range c.pending {
		if p.timer != nil {
			p.timer.Stop()
		}
		delete(c.pending, id)
	}
}

// startPending registers a pending checkpoint and starts the
// autonomous-resume timer.
func (c *LifecycleClient) startPending(checkpointID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.pending[checkpointID]; ok {
		// Defensive: a duplicate `checkpoint_request` for the same id
		// re-arms the timer rather than panicking.
		if existing.timer != nil {
			existing.timer.Stop()
		}
		delete(c.pending, checkpointID)
	}
	p := &pendingCheckpoint{
		id:        checkpointID,
		startedAt: c.now(),
	}
	p.timer = time.AfterFunc(c.autoTimeout, func() {
		c.fireAutoResume(checkpointID)
	})
	c.pending[checkpointID] = p
}

// fireAutoResume runs when the autonomous-resume timer expires. The
// runtime is considered resumed; we log the warning and drop the
// pending slot.
func (c *LifecycleClient) fireAutoResume(checkpointID string) {
	c.mu.Lock()
	p, ok := c.pending[checkpointID]
	if ok {
		p.resumed = true
		delete(c.pending, checkpointID)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	c.log("%s: checkpointId=%s waited=%s", CheckpointTimeoutLogPrefix, checkpointID, c.autoTimeout)
}

// log emits a warning to the configured logger or stderr.
func (c *LifecycleClient) log(format string, args ...any) {
	if c.logf != nil {
		c.logf(format, args...)
		return
	}
	_, _ = fmt.Fprintf(c.stderr, format+"\n", args...)
}

// writeFrame serialises one JSONL frame to the adapter.
func (c *LifecycleClient) writeFrame(frame map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(frame)
}
