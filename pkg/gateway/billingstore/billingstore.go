// SPDX-License-Identifier: MIT

// Package billingstore is the §11.2.1 billing event ledger. It records
// the structured, per-tenant, per-session cost-attribution events that
// back the `GET /v1/metering/events` API and downstream billing
// integrations.
//
// The ledger is append-only: Append assigns the next per-tenant
// sequence_number and commits the event; there is no update or delete.
// Consumers detect lost events as gaps in the sequence_number stream
// and replay them with Since.
//
// The in-memory Memory implementation here backs tests and the
// minimal gateway; billingstore/pgstore is the durable Postgres
// implementation over the append-only billing_events table.
package billingstore

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// EventType is a §11.2.1 billing event type. The constants cover the
// event types the gateway currently emits; the spec enumerates more,
// added as the emitting code paths are built.
type EventType string

const (
	// EventSessionCreated is emitted when a new session is created.
	EventSessionCreated EventType = "session.created"

	// EventSessionCompleted is emitted when a session reaches a
	// terminal state (completed, failed, cancelled, or expired).
	EventSessionCompleted EventType = "session.completed"
)

// defaultSchemaVersion is the §15.5 billing-event schema revision
// stamped on an event whose SchemaVersion is left zero.
const defaultSchemaVersion = 1

// Event is one §11.2.1 billing event. The fields mirror the core
// columns of the billing_events table. SequenceNumber is assigned by
// Append and is monotonically increasing within a tenant.
type Event struct {
	TenantID       string
	SequenceNumber uint64
	SchemaVersion  uint32
	UserID         string
	SessionID      string
	ExperimentID   string
	VariantID      string
	EventType      EventType
	TokensInput    uint64
	TokensOutput   uint64
	CreatedAt      time.Time
}

// ErrInvalidEvent — the event is missing a tenant id or event type.
var ErrInvalidEvent = errors.New("billingstore: event requires a tenant id and event type")

// Store is the §11.2.1 billing event ledger contract. It is
// append-only: implementations never update or delete a committed
// event.
type Store interface {
	// Append commits e to the tenant's ledger, assigning the next
	// per-tenant sequence_number, and returns the sealed event.
	Append(ctx context.Context, e Event) (Event, error)

	// Since returns the tenant's events with sequence_number greater
	// than since, in ascending sequence order, capped at limit. A
	// limit of zero or less applies no cap.
	Since(ctx context.Context, tenantID string, since uint64, limit int) ([]Event, error)
}

// Validate reports the §11.2.1 minimum-field requirements. Every Store
// implementation runs it before committing an event.
func Validate(e Event) error {
	if e.TenantID == "" || e.EventType == "" {
		return ErrInvalidEvent
	}
	return nil
}

// Normalize fills the server-assigned defaults on an event before it
// is committed: the §15.5 schema version and the creation timestamp.
// It does not assign SequenceNumber, which each Store derives from its
// own per-tenant counter.
func Normalize(e Event, now time.Time) Event {
	if e.SchemaVersion == 0 {
		e.SchemaVersion = defaultSchemaVersion
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.CreatedAt = e.CreatedAt.UTC()
	return e
}

// Memory is the in-memory Store. Events are held per tenant in
// sequence order.
type Memory struct {
	mu     sync.Mutex
	events map[string][]Event
	now    func() time.Time
}

// NewMemory returns an empty in-memory billing ledger.
func NewMemory() *Memory {
	return &Memory{
		events: map[string][]Event{},
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Append implements Store.
func (m *Memory) Append(_ context.Context, e Event) (Event, error) {
	if err := Validate(e); err != nil {
		return Event{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	committed := Normalize(e, m.now())
	committed.SequenceNumber = uint64(len(m.events[e.TenantID])) + 1
	m.events[e.TenantID] = append(m.events[e.TenantID], committed)
	return committed, nil
}

// Since implements Store.
func (m *Memory) Since(_ context.Context, tenantID string, since uint64, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, 0)
	for _, e := range m.events[tenantID] {
		if e.SequenceNumber > since {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SequenceNumber < out[j].SequenceNumber })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ Store = (*Memory)(nil)
