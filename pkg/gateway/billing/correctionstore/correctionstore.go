// SPDX-License-Identifier: MIT

// Package correctionstore is the §11.2.1 pending billing-correction
// registry. It backs the operator-initiated billing-correction workflow
// (Category 2): a correction request a human operator submits through
// POST /v1/admin/billing-corrections is recorded here in a
// billing_correction_pending state, optionally routed through
// dual-control approval, and only promoted to the immutable billing
// ledger once approved.
//
// The registry never mutates a committed billing event — corrections
// are appended to the ledger as billing_correction events, preserving
// the §11.7 append-only immutability control. The registry holds only
// the *pending* request and its approval outcome; the committed
// correction lives in billingstore.
//
// The in-memory Memory implementation here backs tests and the minimal
// gateway.
package correctionstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
)

// State is the §11.2.1 billing_correction_pending lifecycle state.
type State string

const (
	// StatePending — the correction request is recorded and awaiting a
	// second platform-admin's approval (dual-control path).
	StatePending State = "pending"
	// StateApproved — a second platform-admin approved the request; the
	// billing_correction event has been written to the ledger.
	StateApproved State = "approved"
	// StateRejected — a second platform-admin rejected the request. It
	// is retained for audit and never promoted to the billing ledger.
	StateRejected State = "rejected"
	// StateExpired — the request was not actioned within the approval
	// timeout and is retired without being promoted.
	StateExpired State = "expired"
)

// AllStates returns the §11.2.1 state enum, the order used by the
// lenny_billing_correction_pending_total metric labels.
func AllStates() []State {
	return []State{StatePending, StateApproved, StateRejected, StateExpired}
}

// Terminal reports whether s is an end state. A correction in a
// terminal state takes no further transitions.
func (s State) Terminal() bool {
	return s == StateApproved || s == StateRejected || s == StateExpired
}

// PendingCorrection is one §11.2.1 operator-initiated billing-correction
// request. The replacement cost values (TokensInput, TokensOutput,
// PodMinutes) are the §11.2.1 superseding values for the original
// event's corresponding fields.
type PendingCorrection struct {
	// ID is the §11.2.1 approval_request_id — the opaque identifier the
	// approve/reject endpoints address the request by.
	ID string

	// TenantID is the tenant whose billing ledger the correction
	// adjusts.
	TenantID string

	// CorrectsSequence is the sequence_number of the original billing
	// event being corrected.
	CorrectsSequence uint64

	// ReasonCode is the §11.2.1 structured correction_reason_code.
	ReasonCode billingstore.ReasonCode

	// Detail is the optional free-text correction_detail.
	Detail string

	// TokensInput, TokensOutput, PodMinutes are the replacement values.
	TokensInput  uint64
	TokensOutput uint64
	PodMinutes   float64

	// State is the current §11.2.1 lifecycle state.
	State State

	// SubmittedBy is the `sub` of the platform-admin who submitted the
	// request. The §11.2.1 four-eyes rule forbids this identity from
	// approving the request.
	SubmittedBy string

	// DecidedBy is the `sub` of the platform-admin who approved or
	// rejected the request. Empty while pending or after expiry.
	DecidedBy string

	// DualControl records whether the request required a second
	// platform-admin's approval. A request at or below the configured
	// threshold is single-control and is committed by the submitter.
	DualControl bool

	// CommittedSequence is the sequence_number of the billing_correction
	// event written to the ledger once the request is approved. Zero
	// until the correction is committed.
	CommittedSequence uint64

	// SubmittedAt, DecidedAt are the lifecycle timestamps. DecidedAt is
	// zero while the request is pending.
	SubmittedAt time.Time
	DecidedAt   time.Time
}

// Filter narrows a List query. A zero Filter lists every pending
// correction.
type Filter struct {
	// TenantID, when set, restricts the result to one tenant.
	TenantID string
	// State, when set, restricts the result to one lifecycle state.
	State State
}

// Sentinel errors.
var (
	// ErrNotFound — no pending correction has the requested id.
	ErrNotFound = errors.New("correctionstore: pending correction not found")
	// ErrNotPending — the correction exists but is no longer pending, so
	// it cannot be approved, rejected, or committed again.
	ErrNotPending = errors.New("correctionstore: correction is not in the pending state")
)

// Store is the §11.2.1 pending billing-correction registry contract.
type Store interface {
	// Create records a new pending correction and returns it with its
	// assigned id, SubmittedAt timestamp, and initial state.
	Create(ctx context.Context, c PendingCorrection) (PendingCorrection, error)

	// Get returns the pending correction with the given id.
	Get(ctx context.Context, id string) (PendingCorrection, error)

	// List returns the pending corrections matching f, newest first.
	List(ctx context.Context, f Filter) ([]PendingCorrection, error)

	// Transition moves a pending correction to a terminal state. It
	// returns ErrNotPending if the correction is no longer pending, so a
	// double approval or a rejection of an already-decided request is
	// rejected. The mutate callback runs while the correction is locked,
	// letting the caller stamp DecidedBy, DecidedAt, and (for an
	// approval) CommittedSequence atomically with the state change.
	Transition(ctx context.Context, id string, to State, mutate func(*PendingCorrection)) (PendingCorrection, error)

	// Counts returns the number of corrections in each §11.2.1 state,
	// the source of the lenny_billing_correction_pending_total metric.
	Counts(ctx context.Context) (map[State]int, error)
}

// newID returns a random 128-bit hex identifier — the §11.2.1
// approval_request_id.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Memory is the in-memory Store.
type Memory struct {
	mu    sync.Mutex
	rows  map[string]PendingCorrection
	clock func() time.Time
}

// NewMemory returns an empty in-memory pending-correction registry.
func NewMemory() *Memory {
	return &Memory{
		rows:  make(map[string]PendingCorrection),
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// NewMemoryWithClock returns an in-memory registry with an injected
// clock, for deterministic tests.
func NewMemoryWithClock(clock func() time.Time) *Memory {
	m := NewMemory()
	if clock != nil {
		m.clock = clock
	}
	return m
}

var _ Store = (*Memory)(nil)

// Create implements Store.
func (m *Memory) Create(_ context.Context, c PendingCorrection) (PendingCorrection, error) {
	id, err := newID()
	if err != nil {
		return PendingCorrection{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c.ID = id
	c.State = StatePending
	c.SubmittedAt = m.clock()
	m.rows[id] = c
	return c, nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, id string) (PendingCorrection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[id]
	if !ok {
		return PendingCorrection{}, ErrNotFound
	}
	return c, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, f Filter) ([]PendingCorrection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PendingCorrection, 0, len(m.rows))
	for _, c := range m.rows {
		if f.TenantID != "" && c.TenantID != f.TenantID {
			continue
		}
		if f.State != "" && c.State != f.State {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SubmittedAt.After(out[j].SubmittedAt)
	})
	return out, nil
}

// Transition implements Store.
func (m *Memory) Transition(_ context.Context, id string, to State, mutate func(*PendingCorrection)) (PendingCorrection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[id]
	if !ok {
		return PendingCorrection{}, ErrNotFound
	}
	if c.State != StatePending {
		return PendingCorrection{}, ErrNotPending
	}
	c.State = to
	if mutate != nil {
		mutate(&c)
	}
	m.rows[id] = c
	return c, nil
}

// Counts implements Store.
func (m *Memory) Counts(_ context.Context) (map[State]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := make(map[State]int, len(AllStates()))
	for _, s := range AllStates() {
		counts[s] = 0
	}
	for _, c := range m.rows {
		counts[c.State]++
	}
	return counts, nil
}
