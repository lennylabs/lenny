// SPDX-License-Identifier: MIT

package session

import "time"

// DeliveryReceipt is the synchronous §15.4 envelope every
// lenny/send_message call (MCP) and POST /v1/sessions/{id}/messages
// call (REST) returns. The wire field names mirror the §15.4 schema
// exactly so the REST and MCP transports remain in lockstep per the
// §15.2.1 consistency contract.
//
// spec: §15.4 lines 1725-1737 (`delivery_receipt` acknowledgement
// schema); §7.2 line 345; §15.1 message-injection response.
type DeliveryReceipt struct {
	// MessageID is the sender-supplied or gateway-assigned identifier
	// of the message this receipt acknowledges. A gateway-assigned ID
	// carries the `msg_` prefix per §15.4 line 1784.
	MessageID string `json:"messageId"`

	// Status is the closed five-value enum from §15.4 line 1730:
	// `delivered` | `queued` | `dropped` | `expired` | `rate_limited`
	// | `error`. The MessageResponse marshaller never emits any other
	// value; implementations MUST NOT introduce synonyms.
	Status DeliveryStatus `json:"status"`

	// Reason is populated only for the §15.4 line 1742 reason enum
	// (status ∈ {dropped, error}). Empty for `delivered` and
	// `queued` per the same table.
	Reason DeliveryReason `json:"reason,omitempty"`

	// DeliveredAt is the RFC 3339 timestamp set when the runtime
	// consumed the message (`status="delivered"` only).
	DeliveredAt time.Time `json:"deliveredAt,omitempty"`

	// QueueDepth is the inbox depth after enqueue (`status="queued"`
	// only). A zero value with `status="queued"` is valid (the message
	// is the head of an otherwise-empty queue).
	QueueDepth int `json:"queueDepth,omitempty"`
}

// DeliveryStatus is the closed enum on DeliveryReceipt.Status. The
// canonical values are defined in §15.4 line 1730.
type DeliveryStatus string

const (
	DeliveryStatusDelivered   DeliveryStatus = "delivered"
	DeliveryStatusQueued      DeliveryStatus = "queued"
	DeliveryStatusDropped     DeliveryStatus = "dropped"
	DeliveryStatusExpired     DeliveryStatus = "expired"
	DeliveryStatusRateLimited DeliveryStatus = "rate_limited"
	DeliveryStatusError       DeliveryStatus = "error"
)

// DeliveryReason is the closed enum on DeliveryReceipt.Reason. The
// canonical values are defined in the §15.4 line 1739
// `delivery_receipt.reason` table.
type DeliveryReason string

const (
	// DeliveryReasonInboxOverflow is emitted when the session inbox
	// reaches `maxInboxSize` and the oldest buffered message is
	// evicted. Paired with DeliveryStatusDropped.
	DeliveryReasonInboxOverflow DeliveryReason = "inbox_overflow"
	// DeliveryReasonDLQOverflow is emitted when the dead-letter queue
	// reaches `maxDLQSize`. Paired with DeliveryStatusDropped.
	DeliveryReasonDLQOverflow DeliveryReason = "dlq_overflow"
	// DeliveryReasonInboxUnavailable is emitted when the durable
	// inbox enqueue fails because Redis is unreachable. Paired with
	// DeliveryStatusError.
	DeliveryReasonInboxUnavailable DeliveryReason = "inbox_unavailable"
	// DeliveryReasonScopeDenied is emitted when the sender's
	// effective `messagingScope` does not permit the target. Paired
	// with DeliveryStatusError.
	DeliveryReasonScopeDenied DeliveryReason = "scope_denied"
)
