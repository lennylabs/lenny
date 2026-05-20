// SPDX-License-Identifier: MIT

// Package interactionstore holds the §6 / §9.2 pending interactive
// session prompts — tool-call approvals and elicitations — that the
// client resolves via the §15.1 tool-use and elicitation endpoints.
//
// A runtime produces a pending interaction; the gateway records it
// here; the owning user resolves it. Resolution is keyed on the
// §15.1 `(session_id, user_id, interaction_id)` authorization
// triple — a mismatch on any component returns "not found" so the
// existence of another session's interactions is never leaked.
package interactionstore

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Kind discriminates the two pending-interaction types.
type Kind string

const (
	// KindToolUse is a §9 tool-call awaiting approve / deny.
	KindToolUse Kind = "tool_use"
	// KindElicitation is a §9.2 elicitation awaiting respond / dismiss.
	KindElicitation Kind = "elicitation"
)

// Phase is the resolution state of an interaction.
type Phase string

const (
	PhasePending   Phase = "pending"
	PhaseApproved  Phase = "approved"
	PhaseDenied    Phase = "denied"
	PhaseResponded Phase = "responded"
	PhaseDismissed Phase = "dismissed"
)

// Interaction is one pending tool-call or elicitation.
type Interaction struct {
	// ID is the tool_call_id or elicitation_id.
	ID string
	// Kind discriminates tool-use from elicitation.
	Kind Kind
	// SessionID + TenantID + UserID form the §15.1 authorization
	// triple. UserID is the user the interaction is directed at.
	SessionID string
	TenantID  string
	UserID    string
	// Phase is the current resolution state.
	Phase Phase
	// Detail is opaque interaction metadata (the tool name +
	// arguments, or the elicitation prompt).
	Detail map[string]any
	// Response carries the client's answer once resolved.
	Response any
	// Reason carries the deny / dismiss reason once resolved.
	Reason string
	// CreatedAt / ResolvedAt audit timestamps.
	CreatedAt  time.Time
	ResolvedAt time.Time
}

// ErrNotFound is returned for any §15.1 authorization-triple
// mismatch — unknown id, wrong session, or wrong user.
var ErrNotFound = errors.New("interactionstore: interaction not found")

// ErrAlreadyResolved is returned when a resolve call targets an
// interaction that is no longer pending.
var ErrAlreadyResolved = errors.New("interactionstore: interaction already resolved")

// Store is the pending-interaction registry contract.
type Store interface {
	// Put records a new pending interaction.
	Put(ctx context.Context, in Interaction) error
	// Resolve applies the resolution to a pending interaction. The
	// caller's (tenant, session, user) must match the stored triple
	// or ErrNotFound is returned. mutate sets Phase + Response /
	// Reason.
	Resolve(ctx context.Context, tenantID, sessionID, userID, id string, mutate func(*Interaction) error) (Interaction, error)
	// Get returns the interaction, scoped to the triple.
	Get(ctx context.Context, tenantID, sessionID, userID, id string) (Interaction, error)

	// CountElicitations returns the number of elicitation interactions
	// recorded for the session, across every resolution phase. The
	// §9.1 maxElicitationsPerSession budget is a per-session lifetime
	// cap, so already-resolved elicitations count toward it.
	CountElicitations(ctx context.Context, tenantID, sessionID string) (int, error)

	// DeleteByUser removes every interaction directed at userID within
	// tenantID and returns the count deleted — the §12.8 GDPR-erasure
	// per-store adapter. Erasing a user with no interactions is a
	// no-op returning (0, nil).
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)

	// DismissByUser sets every pending elicitation directed at userID
	// within tenantID to dismissed and returns the count dismissed —
	// the §11.4 full_revoke step that clears a revoked user's pending
	// elicitations. Pending tool-use interactions are left untouched;
	// §11.4 step 7 scopes the dismissal to elicitations.
	DismissByUser(ctx context.Context, tenantID, userID string) (int, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu sync.RWMutex
	// keyed by tenantID + "/" + sessionID + "/" + id
	interactions map[string]Interaction
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{interactions: map[string]Interaction{}}
}

func key(tenantID, sessionID, id string) string {
	return tenantID + "/" + sessionID + "/" + id
}

// Put implements Store.
func (m *Memory) Put(_ context.Context, in Interaction) error {
	if in.Phase == "" {
		in.Phase = PhasePending
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.interactions[key(in.TenantID, in.SessionID, in.ID)] = in
	return nil
}

// Get implements Store. Every component of the triple must match.
func (m *Memory) Get(_ context.Context, tenantID, sessionID, userID, id string) (Interaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	in, ok := m.interactions[key(tenantID, sessionID, id)]
	if !ok || in.UserID != userID {
		return Interaction{}, ErrNotFound
	}
	return in, nil
}

// Resolve implements Store.
func (m *Memory) Resolve(_ context.Context, tenantID, sessionID, userID, id string, mutate func(*Interaction) error) (Interaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, sessionID, id)
	in, ok := m.interactions[k]
	if !ok || in.UserID != userID {
		return Interaction{}, ErrNotFound
	}
	if in.Phase != PhasePending {
		return Interaction{}, ErrAlreadyResolved
	}
	if err := mutate(&in); err != nil {
		return Interaction{}, err
	}
	in.ResolvedAt = time.Now().UTC()
	m.interactions[k] = in
	return in, nil
}

// CountElicitations implements Store.
func (m *Memory) CountElicitations(_ context.Context, tenantID, sessionID string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, in := range m.interactions {
		if in.TenantID == tenantID && in.SessionID == sessionID && in.Kind == KindElicitation {
			n++
		}
	}
	return n, nil
}

// DeleteByUser implements Store — the §12.8 GDPR-erasure adapter.
func (m *Memory) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	for k, in := range m.interactions {
		if in.TenantID == tenantID && in.UserID == userID {
			delete(m.interactions, k)
			deleted++
		}
	}
	return deleted, nil
}

// DeleteByTenant implements the §12.2.1 / §14.10 mandatory-erasure
// interface. Removes every interaction belonging to tenantID.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	for k, in := range m.interactions {
		if in.TenantID == tenantID {
			delete(m.interactions, k)
			deleted++
		}
	}
	return deleted, nil
}

// DismissByUser implements Store — the §11.4 full_revoke adapter.
func (m *Memory) DismissByUser(_ context.Context, tenantID, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dismissed := 0
	for k, in := range m.interactions {
		if in.TenantID == tenantID && in.UserID == userID &&
			in.Kind == KindElicitation && in.Phase == PhasePending {
			in.Phase = PhaseDismissed
			in.ResolvedAt = time.Now().UTC()
			m.interactions[k] = in
			dismissed++
		}
	}
	return dismissed, nil
}
