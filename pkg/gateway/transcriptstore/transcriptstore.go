// SPDX-License-Identifier: MIT

// Package transcriptstore is the §15.1 session transcript registry.
// It records the ordered conversation history (inbound messages and
// the runtime's responses) for each session so
// `GET /v1/sessions/{id}/transcript` and the §15.1
// `replayMode: prompt_history` path can read it back.
//
// The store is tenant-scoped: every call carries the tenant_id and
// cross-tenant reads return ErrNotFound, matching the §4.2 session
// store isolation model.
package transcriptstore

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Entry is one transcript line.
type Entry struct {
	// Seq is the per-session monotonic sequence, starting at 1.
	Seq uint64 `json:"seq"`

	// Role is `user`, `assistant`, or `system` per §7.2.
	Role string `json:"role"`

	// Content is the message text.
	Content string `json:"content"`

	// Timestamp is the UTC instant the entry was recorded.
	Timestamp time.Time `json:"timestamp"`
}

// ErrNotFound — no transcript exists for the (tenant, session) pair.
var ErrNotFound = errors.New("transcriptstore: no transcript for session")

// Store is the transcript registry contract.
type Store interface {
	// Append adds entries to a session's transcript, assigning each
	// a monotonic Seq. The entries are recorded in the order given.
	Append(ctx context.Context, tenantID, sessionID string, entries ...Entry) error

	// Get returns the full ordered transcript for a session.
	// Returns ErrNotFound when the session has no recorded entries.
	Get(ctx context.Context, tenantID, sessionID string) ([]Entry, error)

	// Page returns up to limit entries after afterSeq. Returns
	// ErrNotFound when the session has no transcript at all.
	Page(ctx context.Context, tenantID, sessionID string, afterSeq uint64, limit int) ([]Entry, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu sync.RWMutex
	// keyed by tenantID + "/" + sessionID
	transcripts map[string][]Entry
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{transcripts: map[string][]Entry{}} }

func key(tenantID, sessionID string) string { return tenantID + "/" + sessionID }

// Append implements Store.
func (m *Memory) Append(_ context.Context, tenantID, sessionID string, entries ...Entry) error {
	if len(entries) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, sessionID)
	existing := m.transcripts[k]
	nextSeq := uint64(len(existing)) + 1
	for _, e := range entries {
		e.Seq = nextSeq
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now().UTC()
		}
		existing = append(existing, e)
		nextSeq++
	}
	m.transcripts[k] = existing
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, sessionID string) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries, ok := m.transcripts[key(tenantID, sessionID)]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out, nil
}

// Page implements Store.
func (m *Memory) Page(_ context.Context, tenantID, sessionID string, afterSeq uint64, limit int) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries, ok := m.transcripts[key(tenantID, sessionID)]
	if !ok {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 100
	}
	out := make([]Entry, 0, limit)
	for _, e := range entries {
		if e.Seq <= afterSeq {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
