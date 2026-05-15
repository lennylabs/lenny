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
	// opened is a no-op.
	Close(ctx context.Context, sessionID string) error
}
