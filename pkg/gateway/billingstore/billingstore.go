// SPDX-License-Identifier: MIT

// Package billingstore is the §11.2.1 billing event ledger. It records
// the structured, per-tenant, per-session cost-attribution events that
// back the `GET /v1/metering/events` API and downstream billing
// integrations.
//
// The ledger is append-only: Append assigns the next per-tenant
// sequence_number and commits the event; there is no update or delete.
// Consumers detect lost events as gaps in the sequence_number stream
// and replay them with Since. The sole exception is the §12.8 GDPR
// erasure path: PseudonymizeUser rewrites a user's events in place so
// the billing history is de-identified without breaking sequence
// continuity.
//
// The in-memory Memory implementation here backs tests and the
// minimal gateway; billingstore/pgstore is the durable Postgres
// implementation over the append-only billing_events table.
package billingstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	// EventBillingCorrection corrects a previously emitted billing
	// event. A correction carries its own SequenceNumber and references
	// the original event via CorrectsSequence; the original event is
	// never mutated (§11.2.1 correction semantics).
	EventBillingCorrection EventType = "billing_correction"
)

// ReasonCode is a §11.2.1 correction_reason_code. The closed enum below
// is the built-in set; deployers extend it via the admin API. Each code
// is classified as gateway-emitted (Category 1) or operator-initiated
// (Category 2); a deployer-added code is always Category 2.
type ReasonCode string

const (
	// ReasonMeteringBug — Category 1: the gateway self-corrects a
	// metering inconsistency.
	ReasonMeteringBug ReasonCode = "METERING_BUG"
	// ReasonRetryOvercounting — Category 1: the gateway removes a
	// double-count from retried requests.
	ReasonRetryOvercounting ReasonCode = "RETRY_OVERCOUNTING"
	// ReasonTestSessionCleanup — Category 2: an operator removes the
	// cost of a test session.
	ReasonTestSessionCleanup ReasonCode = "TEST_SESSION_CLEANUP"
	// ReasonGatewayCrashReconstruction — Category 1: the gateway
	// reconciles billing from pod-reported totals after crash recovery.
	ReasonGatewayCrashReconstruction ReasonCode = "GATEWAY_CRASH_RECONSTRUCTION"
	// ReasonOperatorManualAdjustment — Category 2: an operator records a
	// manual revenue adjustment.
	ReasonOperatorManualAdjustment ReasonCode = "OPERATOR_MANUAL_ADJUSTMENT"
)

// gatewayEmittedReasons is the §11.2.1 Category 1 set: corrections the
// gateway emits as an automated consequence of crash recovery, retry
// deduplication, or self-healing metering. These are written through
// the normal EventStore path and never require operator approval.
var gatewayEmittedReasons = map[ReasonCode]bool{
	ReasonMeteringBug:                true,
	ReasonRetryOvercounting:          true,
	ReasonGatewayCrashReconstruction: true,
}

// operatorReasons is the §11.2.1 Category 2 built-in set: corrections a
// human operator issues through POST /v1/admin/billing-corrections.
var operatorReasons = map[ReasonCode]bool{
	ReasonTestSessionCleanup:       true,
	ReasonOperatorManualAdjustment: true,
}

// IsGatewayEmittedReason reports whether code is a §11.2.1 Category 1
// (gateway-emitted automated reconciliation) reason code.
func IsGatewayEmittedReason(code ReasonCode) bool {
	return gatewayEmittedReasons[code]
}

// IsBuiltinReason reports whether code is one of the built-in §11.2.1
// reason codes. A code that is well-formed but not built-in is a
// deployer-added code, which is always operator-initiated.
func IsBuiltinReason(code ReasonCode) bool {
	return gatewayEmittedReasons[code] || operatorReasons[code]
}

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
	PodMinutes     float64
	CreatedAt      time.Time

	// CorrectsSequence references the original event a billing_correction
	// adjusts (§11.2.1). It is zero for every non-correction event;
	// sequence numbers start at 1, so zero is never a valid reference.
	CorrectsSequence uint64

	// CorrectionReasonCode is the structured §11.2.1 reason code carried
	// on a billing_correction event. It is empty for every other event
	// type and required for a billing_correction.
	CorrectionReasonCode ReasonCode

	// CorrectionDetail is the optional free-text detail supplementing the
	// structured reason code on a billing_correction event.
	CorrectionDetail string
}

// IsCorrection reports whether e is a §11.2.1 billing_correction event.
func (e Event) IsCorrection() bool {
	return e.EventType == EventBillingCorrection
}

// ErrInvalidEvent — the event is missing a tenant id or event type.
var ErrInvalidEvent = errors.New("billingstore: event requires a tenant id and event type")

// ErrInvalidCorrection — a billing_correction event is missing its
// corrects_sequence reference or its correction_reason_code, or a
// non-correction event carries correction-only fields. §11.2.1 makes
// both fields mandatory on a correction and absent on every other
// event type.
var ErrInvalidCorrection = errors.New("billingstore: a billing_correction event requires corrects_sequence and correction_reason_code")

// ErrPseudonymizeArg — PseudonymizeUser was called without a tenant id,
// a user id, or a salt. Pseudonymizing with no salt would produce a
// trivially reversible hash, so the empty-salt case is rejected rather
// than silently weakened.
var ErrPseudonymizeArg = errors.New("billingstore: pseudonymize requires a tenant id, a user id, and a salt")

// Pseudonymize returns the §12.8 one-way pseudonym for a billing
// event's user id: the hex-encoded SHA-256 of the user id followed by
// the tenant's erasure salt. The salt is a per-tenant 256-bit secret;
// once it is destroyed the pseudonym cannot be reversed, which is what
// makes the pseudonymized events effectively anonymous (§12.8 GDPR
// Recital 26 note).
func Pseudonymize(userID string, salt []byte) string {
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write(salt)
	return hex.EncodeToString(h.Sum(nil))
}

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
//
// For a billing_correction event, §11.2.1 additionally requires a
// non-zero corrects_sequence and a correction_reason_code. The
// correction-only fields must not appear on any other event type — the
// null/absent field contract makes event_type the discriminant — so a
// non-correction event carrying them is rejected as well.
func Validate(e Event) error {
	if e.TenantID == "" || e.EventType == "" {
		return ErrInvalidEvent
	}
	if e.IsCorrection() {
		if e.CorrectsSequence == 0 || e.CorrectionReasonCode == "" {
			return ErrInvalidCorrection
		}
		return nil
	}
	// A non-correction event must carry no correction-only field.
	if e.CorrectsSequence != 0 || e.CorrectionReasonCode != "" || e.CorrectionDetail != "" {
		return ErrInvalidCorrection
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

// PseudonymizeUser rewrites every billing event in tenantID owned by
// userID, replacing the user id with its §12.8 pseudonym. It is the
// erasure-only exception to the append-only contract: the GDPR erasure
// job calls it so a user's billing history is de-identified while the
// sequence numbers, tenant id, and cost dimensions stay intact for
// financial reconciliation. It returns the count of events rewritten
// and is idempotent — a second call with the same user id finds no
// event still keyed to it. The current billing-event schema carries no
// free-text columns, so the user id is the only personal field.
func (m *Memory) PseudonymizeUser(_ context.Context, tenantID, userID string, salt []byte) (int, error) {
	if tenantID == "" || userID == "" || len(salt) == 0 {
		return 0, ErrPseudonymizeArg
	}
	pseudonym := Pseudonymize(userID, salt)
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	events := m.events[tenantID]
	for i := range events {
		if events[i].UserID == userID {
			events[i].UserID = pseudonym
			n++
		}
	}
	return n, nil
}

// CountUser returns the number of billing events in tenantID owned by
// userID. The §12.8 erasure verification calls it to confirm no event
// remains keyed to a pseudonymized user's original id.
func (m *Memory) CountUser(_ context.Context, tenantID, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.events[tenantID] {
		if e.UserID == userID {
			n++
		}
	}
	return n, nil
}

// DeleteByUser implements the §12.1 mandatory-erasure interface.
// Billing events are append-only per §11.2.1; the §12.8 erasure path
// pseudonymizes rather than deletes them so the chain stays intact.
// DeleteByUser at this layer is a no-op that returns 0 erased rows;
// the orchestrator runs PseudonymizeUser for the user-scoped phase.
func (m *Memory) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure interface.
// Tenant deletion is the only path that removes billing events; the
// §11.2.1 immutability constraint does not apply to a tenant being
// torn down.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.events[tenantID])
	delete(m.events, tenantID)
	return n, nil
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

// ReconcileLedger applies the §11.2.1 correction semantics to a stream
// of billing events read in sequence-number order. For each
// billing_correction it encounters, the correction's tokens_input,
// tokens_output, and pod_minutes supersede the referenced original
// event's corresponding fields. Multiple corrections to the same
// original are applied in sequence-number order, so the latest
// correction wins. The original event is never mutated; ReconcileLedger
// returns a new slice carrying the reconciled originals and drops the
// correction records, which is the accurate billing ledger a consumer
// computes from the immutable stream.
//
// A correction that references an unknown sequence number is retained
// in the output unchanged: the consumer cannot reconcile it, and
// dropping it would hide a billing adjustment.
func ReconcileLedger(events []Event) []Event {
	// Index the originals by sequence number so a correction can be
	// applied in place on a copy.
	byseq := make(map[uint64]int, len(events))
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if !e.IsCorrection() {
			byseq[e.SequenceNumber] = len(out)
			out = append(out, e)
		}
	}
	// Apply corrections in ascending sequence order so the latest
	// correction to a given original takes precedence.
	corrections := make([]Event, 0)
	for _, e := range events {
		if e.IsCorrection() {
			corrections = append(corrections, e)
		}
	}
	sort.Slice(corrections, func(i, j int) bool {
		return corrections[i].SequenceNumber < corrections[j].SequenceNumber
	})
	for _, c := range corrections {
		idx, ok := byseq[c.CorrectsSequence]
		if !ok {
			// The referenced original is not in this window; surface the
			// correction so the consumer does not silently lose it.
			out = append(out, c)
			continue
		}
		out[idx].TokensInput = c.TokensInput
		out[idx].TokensOutput = c.TokensOutput
		out[idx].PodMinutes = c.PodMinutes
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SequenceNumber < out[j].SequenceNumber })
	return out
}
