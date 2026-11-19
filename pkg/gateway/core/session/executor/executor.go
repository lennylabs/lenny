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
// with the SSE relay + CH-RUNTIMEOPS from §4.7.
package executor

import (
	"context"
	"errors"
	"strconv"

	"github.com/lennylabs/lenny/pkg/runtimeexec"
)

// Sentinel errors.
var (
	// ErrUnsupported reports that the configured executor does not
	// support the given operation (e.g., a stateless executor asked
	// to maintain conversation context).
	ErrUnsupported = errors.New("executor: operation not supported")

	// ErrSessionSealed reports that a message did not reach the session's
	// runtime because the session is sealed against delivery. Two seals
	// produce it: the runtime adapter refused the message because an idle
	// suspension sealed the session (§4.7.1 **Session seal.**), or the
	// executor refused to open or use the session's stream because the
	// binding is marked closing for the terminal seal (spec: §7.1 (Delivery
	// and checkpoints during the seal)). The message did not reach the
	// runtime, and the stream end is not a stream failure, so the caller
	// routes the message again by the session's state.
	ErrSessionSealed = errors.New("executor: session sealed against message delivery")

	// ErrNotBound reports that this replica holds no pod binding for the
	// session, so nothing was sent and the message did not reach the
	// runtime. A caller whose classification read found the session
	// running routes the message again when a re-read finds it in another
	// state, such as after an idle suspension released the pod.
	ErrNotBound = errors.New("executor: session is not bound to a pod")
)

// SealedError is the error a Send returns when the adapter refused one of
// the batch's messages as sealed, or when the session's binding is marked
// closing for the terminal seal. Delivered counts the batch's messages the
// runtime received before the refusal, in order, and Response carries their
// output; the refused message and every later one were not delivered. A
// closing binding refuses the batch before any message is written, so
// Delivered is zero. errors.Is(err, ErrSessionSealed) holds for it.
//
// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.1 (CH-ATTACH), §7.1
// (Delivery and checkpoints during the seal)
type SealedError struct {
	Delivered int
	Response  Response
}

// Error implements error.
func (e *SealedError) Error() string {
	return "executor: session sealed against message delivery after " + strconv.Itoa(e.Delivered) + " delivered message(s)"
}

// Unwrap returns ErrSessionSealed.
func (e *SealedError) Unwrap() error { return ErrSessionSealed }

// The message model the executors exchange is declared in pkg/runtimeexec,
// beside the runtime's §28.5.3 frames and the subprocess executor, so the
// adapter binary builds its developer-loop runtime without importing the
// gateway. The aliases keep the gateway's callers on the executor names.
type (
	// Message is runtimeexec.Message, the §7.2 / §15.4 inbound message
	// envelope.
	Message = runtimeexec.Message
	// MessageFrom is runtimeexec.MessageFrom, the §15.4 `from` attribution.
	MessageFrom = runtimeexec.MessageFrom
	// MessagePart is runtimeexec.MessagePart, the §28.5.3 output part.
	MessagePart = runtimeexec.MessagePart
	// Response is runtimeexec.Response, the result of an Executor.Send.
	Response = runtimeexec.Response
)

// SenderFrom builds the §15.4 from-object for a message an inter-session
// sender sent, as runtimeexec.SenderFrom does.
func SenderFrom(senderID string) MessageFrom {
	return runtimeexec.SenderFrom(senderID)
}

// Executor is the gateway-side abstraction for routing a session's
// messages to a runtime + collecting the response.
type Executor interface {
	// Send delivers the messages to the executor's session context
	// and returns the runtime's Response (output parts plus any
	// envelope-level §28.5.3 degradation annotations). Implementations
	// must not retain the supplied slice after returning.
	Send(ctx context.Context, sessionID string, messages []Message) (Response, error)

	// Close releases any executor-side state associated with the
	// session. Idempotent; calling Close on a session that was never
	// opened is a no-op. An executor that drives a §6.2 AgentPod-backed
	// pod also implements SessionReleaser to record the session's terminal
	// disposition before the pod drains; Close on such an executor releases
	// without recording a disposition (the pod still drains).
	Close(ctx context.Context, sessionID string) error
}

// Disposition is how a session ended, supplied to SessionReleaser.Release so
// a pod-backed executor can record the terminal phase on the backing AgentPod
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
// on the backing AgentPod (§6.2 attached → completed/failed/cancelled/expired)
// before draining the pod. The gateway's terminal-state path prefers Release
// over Close so the authoritative §6.2 state machine reflects the session
// outcome; executors with no AgentPod phase to record (echo, subprocess) do not
// implement it and the caller falls back to Close. spec: §6.2.
type SessionReleaser interface {
	// Release tears down the session like Close and records disposition on
	// the backing AgentPod. Idempotent; releasing an unbound session is a
	// no-op.
	Release(ctx context.Context, sessionID string, disposition Disposition) error
}

// ReleaseSession tears a terminal session's executor state down, recording the
// §6.2 terminal disposition on the backing AgentPod when the executor is a
// SessionReleaser (pod-backed) and otherwise falling back to Close (echo,
// subprocess). Draining the pod is the §11.4 clean-cancellation
// mechanism: the §6.2 claimed → draining → terminated transition triggers the
// adapter's session teardown, which sends the runtime process no signal; the
// process ends with the pod (§4.7.10). This is the single
// release entry point shared by the session-server terminal path, the §8.10
// cascade, and the §8.5 lenny/cancel_child cascade so every cancellation route
// drains the runtime the same way. A nil executor is a no-op.
// spec: §6.2; §11.4.
func ReleaseSession(ctx context.Context, exec Executor, sessionID string, disp Disposition) error {
	if exec == nil {
		return nil
	}
	if r, ok := exec.(SessionReleaser); ok {
		return r.Release(ctx, sessionID, disp)
	}
	return exec.Close(ctx, sessionID)
}
