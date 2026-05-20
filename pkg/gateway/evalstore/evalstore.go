// SPDX-License-Identifier: MIT

// Package evalstore is the §10.7 built-in eval-result registry: the
// per-session EvalResult records ingested by POST /v1/sessions/{id}/eval
// and aggregated by GET /v1/admin/experiments/{name}/results.
//
// The store enforces the §10.7 per-session storage bound
// (maxEvalsPerSession); experiment attribution (experiment_id,
// variant_id) is auto-populated by the gateway when the session
// carries an experiment context.
package evalstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// DefaultMaxEvalsPerSession is the §10.7 default per-session storage
// bound. The tenant-config override (`maxEvalsPerSession`) may raise
// it to at most 100000.
const DefaultMaxEvalsPerSession = 10000

// EvalResult is one §10.7 eval score stored against a session.
type EvalResult struct {
	// ID is the auto-generated record identifier.
	ID string
	// TenantID owns the session; required for tenant scoping.
	TenantID string
	// SessionID is the session the score pertains to.
	SessionID string
	// ExperimentID and VariantID are auto-populated from the session's
	// experiment context; empty when the session is not enrolled.
	ExperimentID string
	VariantID    string
	// Scorer identifies the scoring method (e.g. "llm-judge").
	Scorer string
	// Score is the normalized aggregate score (0.0–1.0). Nil when the
	// submission provided only the per-dimension Scores map.
	Score *float64
	// Scores holds optional per-dimension scores.
	Scores map[string]float64
	// Metadata holds arbitrary caller key-value pairs.
	Metadata map[string]any
	// DelegationDepth is the session's depth in its delegation tree.
	DelegationDepth uint32
	// Inherited mirrors the session's experimentContext.inherited flag.
	Inherited bool
	// SubmittedAfterConclusion is true when the eval was submitted
	// after the attributed experiment transitioned to concluded.
	SubmittedAfterConclusion bool
	// CreatedAt is the server-generated UTC ingestion timestamp.
	CreatedAt time.Time
}

// ErrQuotaExceeded is returned by Put when the session already holds
// the maximum number of eval results. The §10.7 endpoint maps it to
// 429 EVAL_QUOTA_EXCEEDED.
var ErrQuotaExceeded = errors.New("evalstore: per-session eval quota exceeded")

// Store is the §10.7 eval-result registry contract. Every method is
// goroutine-safe and tenant-scoped.
type Store interface {
	// Put assigns an ID and CreatedAt, enforces the per-session storage
	// bound, and stores the result. Returns ErrQuotaExceeded when the
	// session is already at its cap.
	Put(ctx context.Context, r EvalResult) (EvalResult, error)
	// CountBySession returns the number of eval results stored for the
	// session.
	CountBySession(ctx context.Context, tenantID, sessionID string) (int, error)
	// ListBySession returns the session's eval results, oldest first.
	ListBySession(ctx context.Context, tenantID, sessionID string) ([]EvalResult, error)
	// ListByExperiment returns every eval result attributed to the
	// experiment within the tenant — the input to the §10.7 results
	// aggregation. Results with no experiment attribution are excluded.
	ListByExperiment(ctx context.Context, tenantID, experimentID string) ([]EvalResult, error)
	// DeleteBySession removes every eval result for the session and
	// returns the count deleted — the §12.8 GDPR-erasure adapter.
	DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu            sync.RWMutex
	results       map[string]EvalResult // keyed by EvalResult.ID
	maxPerSession int
	clock         func() time.Time
}

// NewMemory returns an empty Memory store. A non-positive
// maxPerSession selects DefaultMaxEvalsPerSession; a nil clock
// defaults to time.Now.
func NewMemory(maxPerSession int, clock func() time.Time) *Memory {
	if maxPerSession <= 0 {
		maxPerSession = DefaultMaxEvalsPerSession
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Memory{results: map[string]EvalResult{}, maxPerSession: maxPerSession, clock: clock}
}

// Put implements Store.
func (m *Memory) Put(_ context.Context, r EvalResult) (EvalResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, e := range m.results {
		if e.TenantID == r.TenantID && e.SessionID == r.SessionID {
			count++
		}
	}
	if count >= m.maxPerSession {
		return EvalResult{}, ErrQuotaExceeded
	}
	if r.ID == "" {
		r.ID = NewID()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = m.clock()
	}
	m.results[r.ID] = cloneResult(r)
	return cloneResult(r), nil
}

// CountBySession implements Store.
func (m *Memory) CountBySession(_ context.Context, tenantID, sessionID string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, e := range m.results {
		if e.TenantID == tenantID && e.SessionID == sessionID {
			count++
		}
	}
	return count, nil
}

// ListBySession implements Store.
func (m *Memory) ListBySession(_ context.Context, tenantID, sessionID string) ([]EvalResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []EvalResult
	for _, e := range m.results {
		if e.TenantID == tenantID && e.SessionID == sessionID {
			out = append(out, cloneResult(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ListByExperiment implements Store.
func (m *Memory) ListByExperiment(_ context.Context, tenantID, experimentID string) ([]EvalResult, error) {
	if experimentID == "" {
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []EvalResult
	for _, e := range m.results {
		if e.TenantID == tenantID && e.ExperimentID == experimentID {
			out = append(out, cloneResult(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// DeleteBySession implements Store — the §12.8 GDPR-erasure adapter.
func (m *Memory) DeleteBySession(_ context.Context, tenantID, sessionID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	for id, e := range m.results {
		if e.TenantID == tenantID && e.SessionID == sessionID {
			delete(m.results, id)
			deleted++
		}
	}
	return deleted, nil
}

// DeleteByUser implements the §12.2.1 mandatory-erasure interface.
// Eval results carry no user_id directly; user-scoped erasure walks
// the user's sessions via the orchestrator's session lister and then
// calls DeleteBySession per session. DeleteByUser at this layer is
// therefore a no-op that returns 0 erased rows.
func (m *Memory) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.2.1 mandatory-erasure interface.
// Removes every eval result belonging to tenantID.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	for id, e := range m.results {
		if e.TenantID == tenantID {
			delete(m.results, id)
			deleted++
		}
	}
	return deleted, nil
}

// cloneResult deep-copies the Score pointer and the Scores/Metadata
// maps so a stored record and a returned copy never share state.
func cloneResult(r EvalResult) EvalResult {
	if r.Score != nil {
		s := *r.Score
		r.Score = &s
	}
	if r.Scores != nil {
		sc := make(map[string]float64, len(r.Scores))
		for k, v := range r.Scores {
			sc[k] = v
		}
		r.Scores = sc
	}
	if r.Metadata != nil {
		md := make(map[string]any, len(r.Metadata))
		for k, v := range r.Metadata {
			md[k] = v
		}
		r.Metadata = md
	}
	return r
}

// NewID returns a fresh eval-result identifier — 8 random bytes
// hex-encoded with an `eval_` prefix.
func NewID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "eval_" + hex.EncodeToString(buf[:])
}
