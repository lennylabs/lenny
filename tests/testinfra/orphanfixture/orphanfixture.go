// SPDX-License-Identifier: MIT

// Package orphanfixture is the shared fixture for the §8.10 orphan-cleanup
// convergence tests. It seeds delegation trees, counts the terminal writes a
// session store applies and the post-terminal hooks the sweepers and the
// cascade run per session, and checks the convergence invariant: no
// non-terminal delegated session has a terminal parent whose cascade policy
// is `cancel_all`.
//
// spec: §8.10 (Orphan cleanup), §7.2 (Interactive Session Model)
package orphanfixture

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Node is one session of a seeded delegation tree. Depth 0 is the root.
type Node struct {
	ID     string
	Parent string
	Depth  uint32
	Policy session.CascadePolicy
}

// RandomTree returns a delegation tree in ascending depth order, root first.
// The root carries `cancel_all`. Each descendant has between one and
// maxFanout children while its depth is below maxDepth, and carries
// `cancel_all` three times in four and `detach` otherwise. newID mints each
// session id, so a Postgres-backed store can receive UUIDs.
func RandomTree(rng *rand.Rand, newID func() string, maxDepth, maxFanout int) []Node {
	root := Node{ID: newID(), Policy: session.CascadeCancelAll}
	nodes := []Node{root}
	level := []Node{root}
	for depth := 1; depth <= maxDepth; depth++ {
		var next []Node
		for _, p := range level {
			for i := 0; i < 1+rng.Intn(maxFanout); i++ {
				policy := session.CascadeCancelAll
				if rng.Intn(4) == 0 {
					policy = session.CascadeDetach
				}
				n := Node{ID: newID(), Parent: p.ID, Depth: uint32(depth), Policy: policy}
				next = append(next, n)
			}
		}
		nodes = append(nodes, next...)
		level = next
	}
	return nodes
}

// Chain returns a full tree of the given depth and fanout in ascending depth
// order, every node carrying `cancel_all`.
func Chain(newID func() string, depth, fanout int) []Node {
	root := Node{ID: newID(), Policy: session.CascadeCancelAll}
	nodes := []Node{root}
	level := []Node{root}
	for d := 1; d <= depth; d++ {
		var next []Node
		for _, p := range level {
			for i := 0; i < fanout; i++ {
				next = append(next, Node{ID: newID(), Parent: p.ID, Depth: uint32(d), Policy: session.CascadeCancelAll})
			}
		}
		nodes = append(nodes, next...)
		level = next
	}
	return nodes
}

// CancelAllReach returns the ids of the descendants of the root of nodes that
// are reached through `cancel_all` ancestors only. A terminal root's cascade,
// completed by orphan cleanup, ends each of them exactly once.
func CancelAllReach(nodes []Node) map[string]bool {
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	reach := map[string]bool{}
	for _, n := range nodes {
		if n.Depth == 0 {
			continue
		}
		ok := true
		for cur := n; cur.Depth > 0; {
			p := byID[cur.Parent]
			if p.Policy.Resolve() != session.CascadeCancelAll {
				ok = false
				break
			}
			cur = p
		}
		if ok {
			reach[n.ID] = true
		}
	}
	return reach
}

// Seed creates every node as a running session from base, which supplies the
// tenant and any column the store requires. Each descendant carries its
// parent, the tree root, and its delegation depth, as lenny/delegate_task
// writes them. Nodes are created in order, so every parent exists and is live
// when its children are inserted.
func Seed(t testing.TB, store sessionstore.Store, base sessionstore.Session, nodes []Node) {
	t.Helper()
	root := ""
	for _, n := range nodes {
		if n.Depth == 0 {
			root = n.ID
		}
		row := base
		row.ID, row.State, row.CascadeOnFailure = n.ID, session.StateRunning, n.Policy
		row.ParentSessionID, row.DelegationDepth = n.Parent, n.Depth
		if n.Depth > 0 {
			row.RootSessionID = root
		}
		if row.CreatedAt.IsZero() {
			now := time.Now().UTC()
			row.CreatedAt, row.UpdatedAt = now, now
		}
		if err := store.Create(context.Background(), row); err != nil {
			t.Fatalf("orphanfixture: seed session %s: %v", n.ID, err)
		}
	}
}

// Cancel commits id `cancelled` over a non-terminal row with the Terminated
// fact recorded, as a funnel writer does, and returns the locked row's prior
// state and the committed row for the writer's post-terminal hook.
func Cancel(ctx context.Context, store sessionstore.Store, tenant, id string, now time.Time) (session.State, sessionstore.Session, error) {
	var from session.State
	row, err := store.Update(ctx, tenant, id, func(r *sessionstore.Session) error {
		if session.IsTerminal(r.State) {
			return sessionstore.ErrSuperseded
		}
		from = r.State
		r.State = session.StateCancelled
		sessionstore.MarkTerminated(r, from, now)
		return nil
	})
	if err != nil {
		return "", sessionstore.Session{}, fmt.Errorf("orphanfixture: cancel %s: %w", id, err)
	}
	return from, row, nil
}

// LiveUnderTerminalCancelAll returns, sorted, the ids of the non-terminal
// delegated sessions in rows whose direct parent is terminal and carries the
// `cancel_all` policy. Orphan cleanup has converged when it is empty.
func LiveUnderTerminalCancelAll(rows []sessionstore.Session) []string {
	byID := make(map[string]sessionstore.Session, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	var out []string
	for _, r := range rows {
		if session.IsTerminal(r.State) || r.DelegationDepth == 0 {
			continue
		}
		p, ok := byID[r.ParentSessionID]
		if ok && session.IsTerminal(p.State) && p.CascadeOnFailure.Resolve() == session.CascadeCancelAll {
			out = append(out, r.ID)
		}
	}
	sort.Strings(out)
	return out
}

// WriteCounter wraps a session store and counts, per session, each applied
// Update whose mutator moved the row from a non-terminal into a terminal
// state.
type WriteCounter struct {
	sessionstore.Store

	mu     sync.Mutex
	writes map[string]int
}

// NewWriteCounter wraps inner.
func NewWriteCounter(inner sessionstore.Store) *WriteCounter {
	return &WriteCounter{Store: inner, writes: map[string]int{}}
}

// Update implements sessionstore.Store.
func (c *WriteCounter) Update(ctx context.Context, tenantID, id string, fn func(*sessionstore.Session) error) (sessionstore.Session, error) {
	var from, to session.State
	row, err := c.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		from = r.State
		if err := fn(r); err != nil {
			return err
		}
		to = r.State
		return nil
	})
	if err == nil && !session.IsTerminal(from) && session.IsTerminal(to) {
		c.mu.Lock()
		c.writes[id]++
		c.mu.Unlock()
	}
	return row, err
}

// Writes returns the terminal writes applied to id.
func (c *WriteCounter) Writes(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes[id]
}

// HookCounter is an orphan-cleanup terminal hook that counts the
// post-terminal hooks it runs per session and tracks the terminal claims the
// sweepers hold until their dispatched hooks return.
type HookCounter struct {
	mu     sync.Mutex
	hooks  map[string]int
	claims sync.WaitGroup
}

// NewHookCounter returns an empty HookCounter.
func NewHookCounter() *HookCounter {
	return &HookCounter{hooks: map[string]int{}}
}

// BeginTerminal opens a claim and returns its idempotent end.
func (h *HookCounter) BeginTerminal(string) func() {
	h.claims.Add(1)
	var once sync.Once
	return func() { once.Do(h.claims.Done) }
}

// OnSessionTerminal counts one post-terminal hook for sess.
func (h *HookCounter) OnSessionTerminal(_ context.Context, _ session.State, sess sessionstore.Session) {
	h.mu.Lock()
	h.hooks[sess.ID]++
	h.mu.Unlock()
}

// Hooks returns the post-terminal hooks run for id.
func (h *HookCounter) Hooks(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hooks[id]
}

// Wait blocks until every open claim ended, failing t after timeout.
func (h *HookCounter) Wait(t testing.TB, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		h.claims.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("orphanfixture: terminal claims still open after %s", timeout)
	}
}

// ArchiveCounter is a §8.10 tree archive that counts the Archive calls per
// node. The gateway's post-terminal hook archives a settled delegated child
// once, whether the cascade or orphan cleanup ended it, so the count is the
// number of post-terminal hooks that ran for the node.
type ArchiveCounter struct {
	treearchive.Store

	mu    sync.Mutex
	nodes map[string]int
}

// NewArchiveCounter returns an ArchiveCounter over an in-memory archive.
func NewArchiveCounter() *ArchiveCounter {
	return &ArchiveCounter{Store: treearchive.NewMemory(), nodes: map[string]int{}}
}

// Archive implements treearchive.Store.
func (a *ArchiveCounter) Archive(ctx context.Context, n treearchive.ArchivedNode) error {
	a.mu.Lock()
	a.nodes[n.NodeSessionID]++
	a.mu.Unlock()
	return a.Store.Archive(ctx, n)
}

// Hooks returns the Archive calls recorded for node id.
func (a *ArchiveCounter) Hooks(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nodes[id]
}

// CheckOnce fails t for every node that received more than one terminal
// write or more than one post-terminal hook, and for every node in exact that
// did not receive exactly one terminal write. hooks returns a node's hook
// count.
func CheckOnce(t testing.TB, nodes []Node, writes *WriteCounter, hooks func(string) int, exact map[string]bool) {
	t.Helper()
	for _, n := range nodes {
		w := writes.Writes(n.ID)
		if w > 1 {
			t.Errorf("session %s received %d terminal writes, want at most one", n.ID, w)
		}
		if exact[n.ID] && w != 1 {
			t.Errorf("session %s under a terminal cancel_all chain received %d terminal writes, want exactly one", n.ID, w)
		}
		if h := hooks(n.ID); h > 1 {
			t.Errorf("session %s ran %d post-terminal hooks, want at most one", n.ID, h)
		}
	}
}
