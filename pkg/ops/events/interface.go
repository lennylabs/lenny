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

// EventStreamService is the canonical §25.5 event-stream service
// contract — the eight-method surface the spec at lines 2574–2585
// names as the unifying interface for the operational-event delivery
// surface. v1 splits the implementation across two packages: this
// package's Service covers StreamEvents/ListEvents; the
// eventsubscription.Service covers the four CRUD methods. The two
// not-yet-implemented methods (UpdateSubscription, ListDeliveries)
// land with §25.5 F-25.5.7 — until then this interface declaration
// fixes the contract so test doubles and alternative event sources
// can substitute against the spec's signature without divergence.
// spec: §25.5 lines 2574-2585.
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
// the lenny-ops buffer. spec: §25.5 line 2693.
type EventFilter = gwevents.EventFilter

// Subscription is the canonical §25.5 webhook subscription record.
// Aliased to the eventsubscription.Subscription used by the v1 CRUD
// surface so substitutable implementations share the wire shape.
// spec: §25.5 lines 2613-2647.
type Subscription = eventsubscription.Subscription

// SubscriptionRequest is the canonical §25.5 webhook-subscription
// create body. Aliased to the eventsubscription.CreateRequest used by
// the v1 CRUD surface. spec: §25.5 line 2579.
type SubscriptionRequest = eventsubscription.CreateRequest

// SubscriptionUpdate is the canonical §25.5 webhook-subscription
// update body — every field is optional so callers may patch the
// callback URL, the type/severity filters, the description, or the
// active flag without rewriting unrelated fields. spec: §25.5 line
// 2582; field-level semantics §25.5 lines 2613-2647.
type SubscriptionUpdate struct {
	CallbackURL *string   `json:"callbackUrl,omitempty"`
	Types       *[]string `json:"types,omitempty"`
	Severity    *[]string `json:"severity,omitempty"`
	Description *string   `json:"description,omitempty"`
	Active      *bool     `json:"active,omitempty"`
}

// EventPage is the canonical §25.5 polling-response envelope. Items
// is the page of CloudEvents records, each carrying its top-level wrapper
// id: a per-source position (the local ring buffer's monotonic sequence
// when the buffer serves, a synthetic monotonic position derived from the
// Redis stream ID when Redis serves), stable in item shape across sources.
// Pagination follows the §25.2 canonical envelope. Agents resume on the
// CloudEvents id (Event.ID) and the opaque pagination cursor rather than the
// per-source wrapper id. spec: §25.5 lines 2687-2699.
type EventPage struct {
	Items      []gwevents.BufferedEvent `json:"items"`
	Pagination Pagination               `json:"pagination"`
	// Degradation is the §25.5 degradation envelope, attached when the
	// read surface is serving from a fall-back source (the gateway buffer
	// during a Redis outage). Omitted when serving from the primary
	// source. spec: §25.5 lines 2768-2780.
	Degradation *conventions.Degradation `json:"degradation,omitempty"`
}

// Pagination is the §25.2 canonical pagination envelope shared by
// the §25.5 polling and ListDeliveries responses. spec: §25.2
// canonical pagination envelope, restated §25.5 lines 2687-2699.
type Pagination struct {
	Cursor     string `json:"cursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
	CursorKind string `json:"cursorKind,omitempty"`
	HeadCursor string `json:"headCursor,omitempty"`
	// GapDetected reports the encoded source cannot be honoured (the
	// stream evicted past the cursor, or the cursor was minted at a
	// different actualSource this buffer cannot translate). spec: §25.2
	// lines 261-271; §25.5 lines 2666-2675.
	GapDetected bool `json:"gapDetected,omitempty"`
	// GapReason explains a detected gap in human-readable form. spec:
	// §25.2 line 269.
	GapReason string `json:"gapReason,omitempty"`
	// OldestAvailableCursor pairs with GapDetected so a poller can
	// resume from the oldest retained event. spec: §25.5 line 2698.
	OldestAvailableCursor string `json:"oldestAvailableCursor,omitempty"`
	// SuggestedAction is the §25.2 recovery hint ("resync") emitted on a
	// gap so the agent re-reads platform state before assuming
	// continuity. spec: §25.2 line 270.
	SuggestedAction string `json:"suggestedAction,omitempty"`
}

// DeliveryPage is the canonical §25.5 webhook-delivery query
// envelope. spec: §25.5 lines 2569, 2584; row shape lines 2649-2664.
type DeliveryPage struct {
	Items      []Delivery `json:"items"`
	Pagination Pagination `json:"pagination"`
}

// Delivery is one row from `ops_event_deliveries`. spec: §25.5 lines
// 2649-2664.
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
