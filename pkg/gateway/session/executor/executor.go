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
// a slice of MessageParts.
type Message struct {
	// ID is the §15.4.1 stable message identifier. Gateway-assigned
	// when the sender omits it.
	ID string

	// Role is the §7.2 message role: `user`, `assistant`, `system`.
	Role string

	// Content is the message text. Production extends this with
	// multi-part content (image, file ref) per §15.4.1 MessagePart.
	Content string

	// From is the §15.4.1 from-object the gateway stamps onto the
	// delivered envelope. The zero value defers to the executor's
	// default gateway-client identity (a top-level client turn). An
	// inter-session lenny/send_message sets it to the authenticated
	// sending session so the target can attribute the message and the
	// runtime never has to trust a caller-supplied origin. F-13.5.11.
	From MessageFrom
}

// MessageFrom is the §15.4.1 MessageEnvelope `from` attribution the
// gateway injects before delivering a message to the runtime. Kind is
// the closed enum (`client`, `agent`, `system`, `external`); ID is the
// kind-specific identifier (for an inter-session message, the sending
// session id under kind `agent`). The zero value carries no attribution
// and the executor stamps its default gateway-client identity.
//
// spec: §15.4.1 lines 1696-1707 — `from` is adapter-injected and the
// runtime never supplies it; §13.5 mitigation 6 — `from` is always set
// by the gateway from the calling session's authenticated identity.
// F-13.5.11.
type MessageFrom struct {
	Kind string
	ID   string
}

// MessagePart is the §15.4.1 outbound response envelope.
type MessagePart struct {
	// Type is the §15.4.1 content type: `text`, `tool_call`,
	// `tool_result`, etc. The minimal echo executor emits only
	// `text`. An unregistered unprefixed type is collapsed to `text`
	// at ingress, with the original preserved in Annotations.
	Type string `json:"type"`

	// Text carries the inline text content when Type == "text".
	Text string `json:"text,omitempty"`

	// Ref carries the §4.5 lenny-blob:// reference when the content
	// is bound by a blob.
	Ref string `json:"ref,omitempty"`

	// SchemaVersion is the §15.4.1 per-part MessagePart schema revision
	// (default 1). Ingest defaults a missing or non-positive value to
	// 1 so a durable consumer always reads a value.
	SchemaVersion int `json:"schemaVersion,omitempty"`

	// Annotations is the §15.4.1 open metadata map. Ingest stamps
	// `originalType` and the `unregistered_platform_type` warning here
	// when a part's type falls through the canonical-registry fallback.
	Annotations map[string]any `json:"annotations,omitempty"`
}

// Response is the result of an Executor.Send: the runtime's output parts
// plus the §15.4.1 degradation annotations the gateway, as a live
// consumer, surfaced on the enclosing MessageEnvelope while ingesting the
// runtime's `response` frame. Annotations carries the envelope-scoped
// kinds — `schema_version_ahead` (a part stamped a schemaVersion ahead of
// the gateway's known max) and `blob_ref_unresolvable` (a ref the
// consumer could not dereference). It is nil when ingestion hit no
// envelope-level degradation. Part-scoped warnings such as
// `unregistered_platform_type` live on the part's own Annotations.
//
// spec: §15.4.1 lines 1499-1522 (schema_version_ahead), 1575-1579
// (blob_ref_unresolvable).
type Response struct {
	Parts       []MessagePart
	Annotations map[string]any
}

// Executor is the gateway-side abstraction for routing a session's
// messages to a runtime + collecting the response.
type Executor interface {
	// Send delivers the messages to the executor's session context
	// and returns the runtime's Response (output parts plus any
	// envelope-level §15.4.1 degradation annotations). Implementations
	// must not retain the supplied slice after returning.
	Send(ctx context.Context, sessionID string, messages []Message) (Response, error)

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
