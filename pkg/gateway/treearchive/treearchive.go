// SPDX-License-Identifier: MIT

// Package treearchive is the §8.10 session_tree_archive: the durable
// record of a delegation tree's settled child results. When a child
// session reaches a terminal state the gateway archives its result
// here, keyed by (root_session_id, node_session_id). A resumed parent
// replays the archived results in original-settlement order so it
// observes a consistent view of every child outcome regardless of when
// each settled relative to the parent's own pod failure.
//
// The store is tenant-scoped: every Get and Replay call carries the
// tenant_id and the store asserts the record's tenant_id matches
// before returning it, so a tree in one tenant is never visible to
// another.
package treearchive

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ArchivedNode is one settled node of a delegation tree.
type ArchivedNode struct {
	// TenantID scopes the record.
	TenantID string
	// RootSessionID is the delegation tree's root session.
	RootSessionID string
	// NodeSessionID is the settled child session.
	NodeSessionID string
	// State is the node's terminal session state (`completed`,
	// `failed`, `cancelled`, or `expired`).
	State string
	// Result is the §8.8 TaskResult JSON the resumed parent replays.
	Result string
	// SettledAt is the instant the node reached its terminal state. It
	// defines the §8.10 original-settlement replay order.
	SettledAt time.Time
	// ArchivedAt is the instant the node was written to the archive.
	ArchivedAt time.Time
}

// ErrNotFound is returned by Get for an unknown node or a tenant
// mismatch — a cross-tenant miss is indistinguishable from "does not
// exist" by design.
var ErrNotFound = errors.New("treearchive: archived node not found")

// Store is the §8.10 session_tree_archive contract. Every method is
// goroutine-safe.
type Store interface {
	// Archive records a settled node. Re-archiving the same
	// (tenant, root, node) overwrites the prior record — a node
	// reaches a terminal state once, so re-archiving is idempotent.
	Archive(ctx context.Context, n ArchivedNode) error

	// Replay returns every archived node of the tree rooted at
	// rootSessionID within tenantID, ordered by SettledAt — the §8.10
	// original-settlement order a resumed parent observes.
	Replay(ctx context.Context, tenantID, rootSessionID string) ([]ArchivedNode, error)

	// Get returns one archived node. ErrNotFound is returned when no
	// matching node exists within tenantID.
	Get(ctx context.Context, tenantID, rootSessionID, nodeSessionID string) (ArchivedNode, error)
}

// Memory is the in-memory Store implementation. The minimal gateway
// uses it; production swaps in a Postgres-backed implementation behind
// the same contract.
type Memory struct {
	mu    sync.RWMutex
	nodes map[string]ArchivedNode
}

// NewMemory returns an empty in-memory archive.
func NewMemory() *Memory {
	return &Memory{nodes: map[string]ArchivedNode{}}
}

// key joins the tenant, root, and node ids into a map key. The NUL
// separator cannot appear in an id, so distinct triples never collide.
func key(tenantID, rootSessionID, nodeSessionID string) string {
	return tenantID + "\x00" + rootSessionID + "\x00" + nodeSessionID
}

// Archive implements Store.
func (m *Memory) Archive(_ context.Context, n ArchivedNode) error {
	if n.ArchivedAt.IsZero() {
		n.ArchivedAt = time.Now().UTC()
	}
	m.mu.Lock()
	m.nodes[key(n.TenantID, n.RootSessionID, n.NodeSessionID)] = n
	m.mu.Unlock()
	return nil
}

// Replay implements Store.
func (m *Memory) Replay(_ context.Context, tenantID, rootSessionID string) ([]ArchivedNode, error) {
	m.mu.RLock()
	out := make([]ArchivedNode, 0)
	for _, n := range m.nodes {
		if n.TenantID == tenantID && n.RootSessionID == rootSessionID {
			out = append(out, n)
		}
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].SettledAt.Before(out[j].SettledAt) })
	return out, nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, rootSessionID, nodeSessionID string) (ArchivedNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.nodes[key(tenantID, rootSessionID, nodeSessionID)]
	if !ok {
		return ArchivedNode{}, ErrNotFound
	}
	return n, nil
}
