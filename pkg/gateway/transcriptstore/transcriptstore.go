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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SchemaVersion is the §15.5 item 7 schema revision the gateway stamps on
// every MessageEnvelope it persists to session_messages. v1 writers use 1.
//
// spec: §15.4.1 line 1694 — "Every MessageEnvelope persisted to the
// session_messages table carries this field ... the gateway writes it at
// inbox-enqueue time and it is immutable once written." §15.5 item 7 — the
// field is an integer "starting at 1".
const SchemaVersion = 1

// Entry is one transcript line.
type Entry struct {
	// ID is the stable per-message identifier the §15.4.1 MessageDAG
	// view (`GET /v1/sessions/{id}/messages`) returns as the message
	// node id. It is the durable session_messages.id row UUID. The
	// Memory store generates one at append time; the Postgres store
	// surfaces the column. Empty on the legacy transcript-write path is
	// harmless — the transcript view does not depend on it.
	//
	// spec: §15.4.1 line 1784 — "every message has a stable ID"; line
	// 1792 — the session_messages table is indexed on (session_id, id,
	// thread_id).
	ID string `json:"id,omitempty"`

	// Seq is the per-session monotonic sequence, starting at 1.
	Seq uint64 `json:"seq"`

	// Role is `user`, `assistant`, or `system` per §7.2.
	Role string `json:"role"`

	// Content is the message text.
	Content string `json:"content"`

	// Timestamp is the UTC instant the entry was recorded.
	Timestamp time.Time `json:"timestamp"`

	// SchemaVersion is the §15.4.1 MessageEnvelope schema revision the
	// gateway stamps at persist time. Zero on input is normalized to
	// SchemaVersion (1) by the store; callers do not set it (the gateway
	// owns it per §15.4.1 line 1694, "Runtimes MUST NOT set it").
	SchemaVersion int `json:"schemaVersion"`
}

// ErrNotFound — no transcript exists for the (tenant, session) pair.
var ErrNotFound = errors.New("transcriptstore: no transcript for session")

// Store is the transcript registry contract.
//
// spec: §12.1 line 5 — DeleteByUser and DeleteByTenant are the
// mandatory erasure primitives every storage role exposes at the
// interface level. Transcripts are session-scoped; the §12.8
// orchestrator walks the user's sessions and calls DeleteBySession.
// DeleteByUser at this layer is a no-op that returns 0 erased rows;
// DeleteByTenant hard-deletes every transcript owned by the tenant.
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

	// DeleteByUser implements the §12.1 mandatory-erasure primitive.
	// Transcripts are session-scoped; the orchestrator walks the user's
	// sessions and calls DeleteBySession per session.
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)

	// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
	// Removes every transcript belonging to tenantID.
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu sync.RWMutex
	// keyed by tenantID + "/" + sessionID
	transcripts map[string][]Entry
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{transcripts: map[string][]Entry{}} }

// spec: §12.1 line 5 — compile-time satisfaction of the mandatory
// erasure-bearing Store interface.
var _ Store = (*Memory)(nil)

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
		if e.SchemaVersion == 0 {
			e.SchemaVersion = SchemaVersion
		}
		// Assign a stable message-node id so the §15.4.1 MessageDAG view
		// has a durable identifier, mirroring the Postgres
		// session_messages.id UUID column. spec: §15.4.1 line 1784.
		if e.ID == "" {
			e.ID = uuid.NewString()
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

// DeleteBySession removes the session's transcript and returns the
// number of entries deleted. It is the §12.8 GDPR-erasure per-session
// adapter for this session-scoped store; the erasure orchestrator
// invokes it for each of an erased user's sessions.
func (m *Memory) DeleteBySession(_ context.Context, tenantID, sessionID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, sessionID)
	n := len(m.transcripts[k])
	delete(m.transcripts, k)
	return n, nil
}

// DeleteByUser implements the §12.1 mandatory-erasure interface.
// Transcripts are session-scoped, not user-scoped; the erasure
// orchestrator walks the user's sessions and calls DeleteBySession
// per session. DeleteByUser at this layer is a no-op that returns
// 0 erased rows.
func (m *Memory) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure interface.
// Removes every transcript whose key prefix matches the tenant id.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := tenantID + "/"
	deleted := 0
	for k, entries := range m.transcripts {
		if strings.HasPrefix(k, prefix) {
			deleted += len(entries)
			delete(m.transcripts, k)
		}
	}
	return deleted, nil
}
