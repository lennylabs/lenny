// SPDX-License-Identifier: MIT

package sessioninbox

import "time"

// Event type strings for the §7.2 / §15.4.1 asynchronous messaging
// events the gateway publishes on session event streams.
const (
	// EventMessageExpired is the §15.4.1 event published on the sender
	// session's stream when a previously-queued message will no longer
	// be delivered.
	EventMessageExpired = "message_expired"

	// EventInboxCleared is the §7.2 line 284 event published on the
	// target session's own stream when a new coordinator acquires the
	// session and finds an empty in-memory inbox.
	EventInboxCleared = "inbox_cleared"
)

// message_expired `reason` enum — the canonical three values defined in
// §15.4.1. No other value is valid.
const (
	// ReasonDLQTTLExpired — the pre-terminal DLQ TTL elapsed while the
	// target remained in a recovering state and no resume occurred.
	ReasonDLQTTLExpired = "dlq_ttl_expired"

	// ReasonDurableInboxTTLExpired — the durable-inbox per-message TTL
	// elapsed while the target remained in a recovering state.
	ReasonDurableInboxTTLExpired = "durable_inbox_ttl_expired"

	// ReasonTargetTerminated — the target transitioned to a terminal
	// state while messages were still buffered (covers both the inbox
	// drain-on-terminal and the DLQ drain-on-terminal paths).
	ReasonTargetTerminated = "target_terminated"
)

// inboxClearedReason is the §7.2 line 284 `inbox_cleared` reason. The
// event currently fires only on coordinator failover.
const inboxClearedReason = "coordinator_failover"

// schemaVersion stamps the §15.4.1 message_expired envelope.
const messageExpiredSchemaVersion = 1

// MessageExpiredEvent is the §15.4.1 `message_expired` payload published
// on the sender session's event stream. It is the authoritative
// asynchronous signal that a previously-queued message will not be
// delivered; senders MUST NOT infer expiry from any other signal.
//
// spec: §15.4.1 lines 1760-1782.
type MessageExpiredEvent struct {
	SchemaVersion   int    `json:"schemaVersion"`
	Type            string `json:"type"`
	MessageID       string `json:"messageId"`
	TargetSessionID string `json:"targetSessionId"`
	Reason          string `json:"reason"`
	ExpiredAt       string `json:"expiredAt"`
}

// NewMessageExpiredEvent builds a §15.4.1 message_expired event for a
// message that expired against targetSessionID at now with the given
// reason (one of the message_expired reason enum constants).
func NewMessageExpiredEvent(messageID, targetSessionID, reason string, now time.Time) MessageExpiredEvent {
	return MessageExpiredEvent{
		SchemaVersion:   messageExpiredSchemaVersion,
		Type:            EventMessageExpired,
		MessageID:       messageID,
		TargetSessionID: targetSessionID,
		Reason:          reason,
		ExpiredAt:       now.UTC().Format(time.RFC3339),
	}
}

// InboxClearedEvent is the §7.2 line 284 `inbox_cleared` payload
// published on the target session's own event stream after a new
// coordinator acquires the session with an empty in-memory inbox.
// MessagesPreservedInDLQ counts inbox messages successfully drained to
// the DLQ before the prior coordinator crashed: `> 0` means those
// messages survive in the DLQ, `0` means the in-memory inbox was lost.
//
// spec: §7.2 line 284.
type InboxClearedEvent struct {
	Type                   string `json:"type"`
	Reason                 string `json:"reason"`
	ClearedAt              string `json:"clearedAt"`
	SessionID              string `json:"sessionId"`
	MessagesPreservedInDLQ int    `json:"messagesPreservedInDLQ"`
}

// NewInboxClearedEvent builds a §7.2 inbox_cleared event for
// targetSessionID at now, reporting how many messages were preserved in
// the DLQ before the coordinator handoff.
func NewInboxClearedEvent(targetSessionID string, preservedInDLQ int, now time.Time) InboxClearedEvent {
	return InboxClearedEvent{
		Type:                   EventInboxCleared,
		Reason:                 inboxClearedReason,
		ClearedAt:              now.UTC().Format(time.RFC3339),
		SessionID:              targetSessionID,
		MessagesPreservedInDLQ: preservedInDLQ,
	}
}

// Emitter publishes the §7.2 / §15.4.1 asynchronous messaging events
// onto session event streams. The gateway adapts its SSE event bus to
// this interface; a nil Emitter disables emission (the events are
// best-effort and never block a state transition).
type Emitter interface {
	// MessageExpired publishes ev on the sender session's event stream.
	// tenantID scopes the publish; senderSessionID is the stream key.
	MessageExpired(tenantID, senderSessionID string, ev MessageExpiredEvent)

	// InboxCleared publishes ev on the target session's own event
	// stream.
	InboxCleared(tenantID, targetSessionID string, ev InboxClearedEvent)
}
