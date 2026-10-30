// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"net/http"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// EventStreamService is the canonical §25.5 event-stream service contract — the surface the spec at names as the
// unifying interface for the operational-event delivery surface. Every
// method is implemented, split across two packages: this package's
// Service covers StreamEvents and ListEvents against the Redis
// ops:events:stream, the gateway-buffer fan-out, and the local ring;
// eventsubscription.Service covers the subscription CRUD methods and
// the keyset-paginated ListDeliveries. What this declaration does not
// yet have is a single production type that satisfies it in one piece;
// the HTTP routes are the contract today, and the single-implementor
// aggregator is deferred to a follow-on change. The declaration fixes the signature so test
// doubles and alternative event sources substitute without divergence.
// spec: §25.5.
type EventStreamService interface {
	StreamEvents(ctx context.Context, w http.ResponseWriter, filter EventFilter) error
	ListEvents(ctx context.Context, filter EventFilter, cursor string, limit int) (*EventPage, error)
	CreateSubscription(ctx context.Context, sub SubscriptionRequest) (*Subscription, error)
	ListSubscriptions(ctx context.Context) ([]Subscription, error)
	GetSubscription(ctx context.Context, id string) (*Subscription, error)
	UpdateSubscription(ctx context.Context, id string, update SubscriptionUpdate) (*Subscription, error)
	DeleteSubscription(ctx context.Context, id string) error
	ListDeliveries(ctx context.Context, subID string, cursor string, limit int) (*DeliveryPage, error)
}

// EventFilter is the canonical §25.5 / §25.3 filter for stream and
// polling queries. Aliased to the gateway-side EventFilter so the
// stream surface uses one filter shape across the gateway buffer and
// the lenny-ops buffer. spec: §25.5.
type EventFilter = gwevents.EventFilter

// Subscription is the canonical §25.5 webhook subscription record.
// Aliased to the eventsubscription.Subscription used by the v1 CRUD
// surface so substitutable implementations share the wire shape.
// spec: §25.5.
type Subscription = eventsubscription.Subscription

// SubscriptionRequest is the canonical §25.5 webhook-subscription
// create body. Aliased to the eventsubscription.CreateRequest used by
// the v1 CRUD surface. spec: §25.5.
type SubscriptionRequest = eventsubscription.CreateRequest

// SubscriptionUpdate is the canonical §25.5 webhook-subscription
// update body — every field is optional so callers may patch the
// callback URL, the type/severity filters, the description, or the
// active flag without rewriting unrelated fields. spec: §25.5; field-level semantics §25.5.
type SubscriptionUpdate struct {
	CallbackURL *string   `json:"callbackUrl,omitempty"`
	Types       *[]string `json:"types,omitempty"`
	Severity    *[]string `json:"severity,omitempty"`
	Description *string   `json:"description,omitempty"`
	Active      *bool     `json:"active,omitempty"`
}

// EventPage is the canonical §25.5 polling-response envelope. Items is the
// page of CloudEvents records, each carrying the §25.3 top-level wrapper id:
// the monotonic position the source that served it assigned, which is the
// emitting replica's ring position on the buffer-served and fan-out paths and
// the Redis stream position on the Redis-served path. The id is per-source
// rather than globally ordered, so agents deduplicate across sources on the
// canonical eventKey and resume on the CloudEvents id (Event.ID) and the opaque
// pagination cursor. Pagination follows the §25.2 canonical envelope. spec:
// §25.5; §25.3 (monotonic per-source id for cursor-based
// polling).
type EventPage struct {
	Items      []gwevents.BufferedEvent `json:"items"`
	Pagination Pagination               `json:"pagination"`
	// Degradation is the §25.5 degradation envelope, attached when the
	// read surface is serving from a fall-back source (the gateway buffer
	// during a Redis outage). Omitted when serving from the primary
	// source. spec: §25.5.
	Degradation *conventions.Degradation `json:"degradation,omitempty"`
}

// Pagination is the §25.2 canonical pagination envelope shared by
// the §25.5 polling and ListDeliveries responses. spec: §25.2
// canonical pagination envelope, restated §25.5.
type Pagination struct {
	Cursor     string `json:"cursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
	CursorKind string `json:"cursorKind,omitempty"`
	HeadCursor string `json:"headCursor,omitempty"`
	// GapDetected reports the encoded source cannot be honoured (the
	// stream evicted past the cursor, or the cursor was minted at a
	// different actualSource this buffer cannot translate). spec: §25.2; §25.5.
	GapDetected bool `json:"gapDetected,omitempty"`
	// GapReason explains a detected gap in human-readable form. spec:
	// §25.2.
	GapReason string `json:"gapReason,omitempty"`
	// OldestAvailableCursor pairs with GapDetected so a poller can
	// resume from the oldest retained event. spec: §25.5.
	OldestAvailableCursor string `json:"oldestAvailableCursor,omitempty"`
	// SuggestedAction is the §25.2 recovery hint ("resync") emitted on a
	// gap so the agent re-reads platform state before assuming
	// continuity. spec: §25.2.
	SuggestedAction string `json:"suggestedAction,omitempty"`
}

// DeliveryPage is the canonical §25.5 webhook-delivery query envelope.
// spec: §25.5 — the deliveries endpoint, the EventStreamService Go
// interface, and the `ops_event_deliveries` columns.
type DeliveryPage struct {
	Items      []Delivery `json:"items"`
	Pagination Pagination `json:"pagination"`
}

// Delivery is one row from `ops_event_deliveries`. spec: §25.5.
type Delivery struct {
	ID             string    `json:"id"`
	SubscriptionID string    `json:"subscriptionId"`
	EventID        string    `json:"eventId"`
	EventType      string    `json:"eventType"`
	Status         string    `json:"status"`
	Attempts       int       `json:"attempts"`
	LastAttemptAt  time.Time `json:"lastAttemptAt"`
	LastError      string    `json:"lastError,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
}
