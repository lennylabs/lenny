// SPDX-License-Identifier: MIT

// Package executor abstracts session message execution from the
// session-server HTTP layer. The minimal gateway uses an in-process
// echo executor; production wires a kubernetes-backed executor that
// dispatches messages to the claimed agent pod over the §4.7
// adapter binary protocol.
//
// The interface is intentionally narrow:
//
//   - Send(sessionID, messages) accepts the §7.2 message envelopes
//     and returns one or more output parts.
//   - Close(sessionID) tears down any executor-side state.
//
// Streaming and event delivery are deferred — the minimal gateway
// returns the full response synchronously. Production replaces this
// with the SSE relay + lifecycle channel from §4.7.
package executor

import (
	"context"
	"errors"
)

// Sentinel errors.
var (
	// ErrUnsupported reports that the configured executor does not
	// support the given operation (e.g., a stateless executor asked
	// to maintain conversation context).
	ErrUnsupported = errors.New("executor: operation not supported")
)

// Message captures the §7.2 / §15.4.1 inbound message envelope.
// Implementations of Executor receive a slice of Messages and emit
// a slice of OutputParts.
type Message struct {
	// ID is the §15.4.1 stable message identifier. Gateway-assigned
	// when the sender omits it.
	ID string

	// Role is the §7.2 message role: `user`, `assistant`, `system`.
	Role string

	// Content is the message text. Production extends this with
	// multi-part content (image, file ref) per §15.4.1 OutputPart.
	Content string
}

// OutputPart is the §15.4.1 outbound response envelope.
type OutputPart struct {
	// Type is the §15.4.1 content type: `text`, `tool_call`,
	// `tool_result`, etc. The minimal echo executor emits only
	// `text`.
	Type string `json:"type"`

	// Text carries the inline text content when Type == "text".
	Text string `json:"text,omitempty"`

	// Ref carries the §4.5 lenny-blob:// reference when the content
	// is bound by a blob.
	Ref string `json:"ref,omitempty"`
}

// Executor is the gateway-side abstraction for routing a session's
// messages to a runtime + collecting the response.
type Executor interface {
	// Send delivers the messages to the executor's session context
	// and returns the response output parts. Implementations must
	// not retain the supplied slice after returning.
	Send(ctx context.Context, sessionID string, messages []Message) ([]OutputPart, error)

	// Close releases any executor-side state associated with the
	// session. Idempotent; calling Close on a session that was never
	// opened is a no-op. An executor that drives a §6.2 Sandbox-backed
	// pod also implements SessionReleaser to record the session's terminal
	// disposition before the pod drains; Close on such an executor releases
	// without recording a disposition (the pod still drains).
	Close(ctx context.Context, sessionID string) error
}

// Disposition is how a session ended, supplied to SessionReleaser.Release so
// a pod-backed executor can record the terminal phase on the backing Sandbox
// per §6.2 (attached → completed/failed/cancelled/expired) before draining the
// pod. The empty value carries no disposition and skips the terminal-phase
// write.
type Disposition string

const (
	// DispositionCompleted is a session that reached its terminal state
	// normally (§6.2 completed).
	DispositionCompleted Disposition = "completed"
	// DispositionFailed is a session that ended on an unrecoverable error
	// (§6.2 failed).
	DispositionFailed Disposition = "failed"
	// DispositionCancelled is a session cancelled by the client, a parent
	// cascade, or an admin (§6.2 cancelled).
	DispositionCancelled Disposition = "cancelled"
	// DispositionExpired is a session that hit a deadline (§6.2 expired).
	DispositionExpired Disposition = "expired"
)

// SessionReleaser is the optional Executor extension a pod-backed executor
// implements to release a session while recording its terminal disposition
// on the backing Sandbox (§6.2 attached → completed/failed/cancelled/expired)
// before draining the pod. The gateway's terminal-state path prefers Release
// over Close so the authoritative §6.2 state machine reflects the session
// outcome; executors with no Sandbox phase to record (echo, subprocess) do not
// implement it and the caller falls back to Close. spec: §6.2 lines 105-117,
// 305.
type SessionReleaser interface {
	// Release tears down the session like Close and records disposition on
	// the backing Sandbox. Idempotent; releasing an unbound session is a
	// no-op.
	Release(ctx context.Context, sessionID string, disposition Disposition) error
}

// ReleaseSession tears a terminal session's executor state down, recording the
// §6.2 terminal disposition on the backing Sandbox when the executor is a
// SessionReleaser (pod-backed) and otherwise falling back to Close (echo,
// subprocess). Draining the pod is the §11.4 line 258 clean-cancellation
// mechanism: the §6.2 claimed → draining → terminated transition triggers the
// adapter's graceful shutdown (SIGTERM, wait, then SIGKILL). This is the single
// release entry point shared by the session-server terminal path, the §8.10
// cascade, and the §8.5 lenny/cancel_child cascade so every cancellation route
// drains the runtime the same way. A nil executor is a no-op.
// spec: §6.2 lines 105-117, 305; §11.4 line 258.
func ReleaseSession(ctx context.Context, exec Executor, sessionID string, disp Disposition) error {
	if exec == nil {
		return nil
	}
	if r, ok := exec.(SessionReleaser); ok {
		return r.Release(ctx, sessionID, disp)
	}
	return exec.Close(ctx, sessionID)
}
