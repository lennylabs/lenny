// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 fault injection for an interrupted §8.10 cancel_all cascade. Two
// gateway replicas share one Postgres session store. Replica A runs the
// cascade of a cancelled root and stops part-way down a depth-3 subtree, as a
// replica does when its process dies. Orphan cleanup on replica B is the retry
// owner: its next pass cancels every descendant replica A left live.
//
// spec: §8.10 (Orphan cleanup), §7.2 (Interactive Session Model)
package tier8_chaos_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// errReplicaStopped is every store call a stopped replica makes.
var errReplicaStopped = errors.New("injected: replica stopped")

// stoppingStore is one replica's view of the shared session store. It stops
// the replica after its stopAfter-th applied terminal write to a session
// other than root: every later List, Get, and Update fails.
type stoppingStore struct {
	sessionstore.Store

	root      string
	stopAfter int

	mu      sync.Mutex
	applied int
	stopped bool
}

func (s *stoppingStore) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

func (s *stoppingStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	if s.isStopped() {
		return nil, errReplicaStopped
	}
	return s.Store.List(ctx, tenantID, f)
}

func (s *stoppingStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if s.isStopped() {
		return sessionstore.Session{}, errReplicaStopped
	}
	return s.Store.Get(ctx, tenantID, id)
}

func (s *stoppingStore) Update(ctx context.Context, tenantID, id string, fn func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if s.isStopped() {
		return sessionstore.Session{}, errReplicaStopped
	}
	var from session.State
	row, err := s.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		from = r.State
		return fn(r)
	})
	if err == nil && id != s.root && !session.IsTerminal(from) && session.IsTerminal(row.State) {
		s.mu.Lock()
		s.applied++
		if s.applied >= s.stopAfter {
			s.stopped = true
		}
		s.mu.Unlock()
	}
	return row, err
}

// spec: §8.10 (Orphan cleanup), §7.2 (Interactive Session Model)
// diagnosis: replica A's cancel_all cascade of a depth-3 subtree over the
// shared Postgres store stops after three descendant cancels. A failure
// means replica B's next orphan-cleanup pass did not cancel every descendant
// replica A left live under a terminal cancel_all parent (the sweeper waits
// out a cascade window, or keys the selection on the tree root rather than
// the direct parent), or a descendant received a second terminal write or a
// second post-terminal hook.
func TestOrphanCleanupCompletesStoppedReplicaCascade_spec_8_10(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	tenant := "t-" + uuid.NewString()
	seedChaosTenant(t, pg, tenant)
	writes := orphanfixture.NewWriteCounter(sessionpg.New(pg.Pool))
	archive := orphanfixture.NewArchiveCounter()

	// root → 2 → 4 → 8, every node cancel_all.
	nodes := orphanfixture.Chain(uuid.NewString, 3, 2)
	orphanfixture.Seed(t, writes, sessionstore.Session{TenantID: tenant, RuntimeRef: "echo"}, nodes)
	root := nodes[0].ID

	storeA := &stoppingStore{Store: writes, root: root, stopAfter: 3}
	replicaA := sessionserver.New(storeA, sessionserver.Options{DevTenantHeader: true, TreeArchive: archive})
	replicaB := sessionserver.New(writes, sessionserver.Options{DevTenantHeader: true, TreeArchive: archive})

	// Replica A cancels the root and runs its terminal pipeline, whose
	// cascade stops when the replica does.
	end := replicaA.BeginTerminal(root)
	from, row, err := orphanfixture.Cancel(ctx, storeA, tenant, root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replicaA.OnSessionTerminal(ctx, from, row)
	end()
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := replicaA.WaitTerminalWork(waitCtx); err != nil {
		t.Fatalf("replica A terminal work: %v", err)
	}
	if !storeA.isStopped() {
		t.Fatalf("replica A's cascade finished without stopping; the fault did not fire")
	}
	rows, err := writes.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	left := orphanfixture.LiveUnderTerminalCancelAll(rows)
	if len(left) == 0 {
		t.Fatalf("replica A left no live session under a terminal cancel_all parent; the cascade was not partial")
	}
	t.Logf("replica A stopped with %d sessions live under a terminal cancel_all parent", len(left))

	// Replica B's next orphan-cleanup pass.
	sweeper := orphancleanup.New(writes, orphancleanup.StaticTenants{tenant}, orphancleanup.Options{Terminal: replicaB})
	if _, err := sweeper.Tick(ctx, time.Now()); err != nil {
		t.Fatalf("replica B Tick: %v", err)
	}
	if err := replicaB.WaitTerminalWork(waitCtx); err != nil {
		t.Fatalf("replica B terminal work: %v", err)
	}

	rows, err = writes.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range rows {
		if r.ID != root && r.State != session.StateCancelled {
			t.Errorf("descendant %s state = %q after replica B's pass, want cancelled", r.ID, r.State)
		}
	}
	orphanfixture.CheckOnce(t, nodes, writes, archive.Hooks, orphanfixture.CancelAllReach(nodes))
}
