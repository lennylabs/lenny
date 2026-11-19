// SPDX-License-Identifier: MIT

package runtimeexec

// Message captures the §7.2 / §15.4 inbound message envelope.
// An executor receives a slice of Messages and emits a slice of
// MessageParts.
type Message struct {
	// ID is the §15.4 stable message identifier. Gateway-assigned
	// when the sender omits it.
	ID string

	// Role is the §7.2 message role: `user`, `assistant`, `system`.
	Role string

	// Content is the message text. Production extends this with
	// multi-part content (image, file ref) per §28.5.3 MessagePart.
	Content string

	// From is the §15.4 from-object the gateway stamps onto the
	// delivered envelope. The zero value defers to the executor's
	// default gateway-client identity (a top-level client turn). An
	// inter-session lenny/send_message sets it to the authenticated
	// sending session so the target can attribute the message and the
	// runtime never has to trust a caller-supplied origin. F-13.5.11.
	From MessageFrom
}

// MessageFrom is the §15.4 MessageEnvelope `from` attribution the
// gateway injects before delivering a message to the runtime. Kind is
// the closed enum (`client`, `agent`, `system`, `external`); ID is the
// kind-specific identifier (for an inter-session message, the sending
// session id under kind `agent`). The zero value carries no attribution
// and the executor stamps its default gateway-client identity.
//
// spec: §15.4 — `from` is adapter-injected and the
// runtime never supplies it; §13.5 mitigation 6 — `from` is always set
// by the gateway from the calling session's authenticated identity.
// F-13.5.11.
type MessageFrom struct {
	Kind string
	ID   string
}

// SenderFrom builds the §15.4 from-object for a message an inter-session
// sender sent: an authenticated sender is attributed as kind `agent` with its
// session id, and an unattributed send (no sender session) returns the zero
// value so the executor stamps its default gateway-client identity. The
// gateway always sets `from` — a caller cannot. The MCP send path and the
// delivery of held messages both use it, so a held message keeps the sender
// attribution it was accepted with.
//
// spec: §15.4 (Runtime Adapter Specification); §13.5 mitigation 6.
func SenderFrom(senderID string) MessageFrom {
	if senderID == "" {
		return MessageFrom{}
	}
	return MessageFrom{Kind: "agent", ID: senderID}
}

// MessagePart is the §28.5.3 outbound response envelope.
type MessagePart struct {
	// Type is the §28.5.3 content type: `text`, `tool_call`,
	// `tool_result`, etc. The minimal echo executor emits only
	// `text`. An unregistered unprefixed type is collapsed to `text`
	// at ingress, with the original preserved in Annotations.
	Type string `json:"type"`

	// Text carries the inline text content when Type == "text".
	Text string `json:"text,omitempty"`

	// Ref carries the §4.5 lenny-blob:// reference when the content
	// is bound by a blob.
	Ref string `json:"ref,omitempty"`

	// SchemaVersion is the §28.5.3 per-part MessagePart schema revision
	// (default 1). Ingest defaults a missing or non-positive value to
	// 1 so a durable consumer always reads a value.
	SchemaVersion int `json:"schemaVersion,omitempty"`

	// Annotations is the §28.5.3 open metadata map. Ingest stamps
	// `originalType` and the `unregistered_platform_type` warning here
	// when a part's type falls through the canonical-registry fallback.
	Annotations map[string]any `json:"annotations,omitempty"`
}

// Response is the result of an Executor.Send: the runtime's output parts
// plus the §28.5.3 degradation annotations the gateway, as a live
// consumer, surfaced on the enclosing MessageEnvelope while ingesting the
// runtime's `response` frame. Annotations carries the envelope-scoped
// kinds — `schema_version_ahead` (a part stamped a schemaVersion ahead of
// the gateway's known max) and `blob_ref_unresolvable` (a ref the
// consumer could not dereference). It is nil when ingestion hit no
// envelope-level degradation. Part-scoped warnings such as
// `unregistered_platform_type` live on the part's own Annotations.
//
// spec: §15.4 MessagePart format — the live-consumer obligations that
// define schema_version_ahead and blob_ref_unresolvable; §15.5
// degradation annotation catalog.
type Response struct {
	Parts       []MessagePart
	Annotations map[string]any
}
