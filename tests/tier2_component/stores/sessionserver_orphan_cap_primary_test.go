//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// cascadeAuditSink records the session lifecycle audit rows the server emits.
type cascadeAuditSink struct {
	mu     sync.Mutex
	events []sessionserver.SessionLifecycleEvent
}

func (s *cascadeAuditSink) EmitSessionLifecycle(_ context.Context, ev sessionserver.SessionLifecycleEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

// fallbackSessions returns the ids the `session.cascade_applied` rows with
// reason `orphan_cap_fallback` name.
func (s *cascadeAuditSink) fallbackSessions(t *testing.T) []string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, ev := range s.events {
		if ev.EventType != "session.cascade_applied" {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(ev.Detail), &detail); err != nil {
			t.Fatalf("cascade_applied detail not JSON: %v", err)
		}
		if detail["reason"] == "orphan_cap_fallback" {
			ids = append(ids, ev.SessionID)
		}
	}
	return ids
}

// TestOrphanCapCountReadsPrimaryOverPostgres_spec_8_10 runs a cascade over a
// Postgres session store whose read pool is a second database holding the
// tree as it stood before the cascade, standing in for a read replica that
// has not replicated the cascade's terminal writes. The cascade starts at a
// mid-tree session under a live root and cancels a `detach` descendant whose
// two live children put the tenant over a cap of one. The orphan-cap count
// reads the primary, so it sees the cancelled descendant and counts its
// children as orphans: the fallback cancels them and writes the descendant's
// `orphan_cap_fallback` row.
//
// spec: §8.10 (maxOrphanTasksPerTenant fallback), §12.3 (Read replicas)
// diagnosis: a failure means the orphan-cap count lists the tenant from the
// read pool, so a lagging replica that still shows the cancelled descendant
// live hides its children from the count and an over-cap detach stands.
func TestOrphanCapCountReadsPrimaryOverPostgres_spec_8_10(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	primary := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	replica := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	tenant := freshTenant(t, ctx, primary)
	if _, err := replica.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, tenant, []byte{0x01}); err != nil {
		t.Fatalf("seed replica tenant: %v", err)
	}

	ids := map[string]string{}
	for _, name := range []string{"live_root", "start", "mid", "leaf_a", "leaf_b"} {
		ids[name] = newUUID(t)
	}
	nodes := []struct {
		name, parent string
		depth        uint32
		policy       session.CascadePolicy
	}{
		{"live_root", "", 0, session.CascadeCancelAll},
		{"start", "live_root", 1, session.CascadeCancelAll},
		{"mid", "start", 2, session.CascadeDetach},
		{"leaf_a", "mid", 3, ""},
		{"leaf_b", "mid", 3, ""},
	}
	seedPrimary := sessionpg.New(primary.Pool)
	seedReplica := sessionpg.New(replica.Pool)
	now := time.Now()
	for _, n := range nodes {
		row := sessionstore.Session{
			ID: ids[n.name], TenantID: tenant, UserID: "alice", RuntimeRef: "echo",
			State: session.StateRunning, ParentSessionID: ids[n.parent], DelegationDepth: n.depth,
			CascadeOnFailure: n.policy, CreatedAt: now, UpdatedAt: now,
		}
		for _, st := range []sessionstore.Store{seedPrimary, seedReplica} {
			if err := st.Create(ctx, row); err != nil {
				t.Fatalf("seed %s: %v", n.name, err)
			}
		}
	}

	store := sessionpg.New(primary.Pool, sessionpg.WithReadPool(replica.Pool))
	sink := &cascadeAuditSink{}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true, MaxOrphanTasksPerTenant: 1, LifecycleAuditSink: sink,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+ids["start"]+"/terminate", nil)
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	// spec: §10.2 (Session ownership) — the terminate runs as the tree's owner.
	ownerprincipal.Wrap(srv.Handler(), "alice").ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}
	if err := srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}

	want := map[string]session.State{
		"live_root": session.StateRunning, "mid": session.StateCancelled,
		"leaf_a": session.StateCancelled, "leaf_b": session.StateCancelled,
	}
	for name, st := range want {
		row, err := seedPrimary.Get(ctx, tenant, ids[name])
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if row.State != st {
			t.Errorf("%s state = %q, want %q", name, row.State, st)
		}
	}
	if got := sink.fallbackSessions(t); len(got) != 1 || got[0] != ids["mid"] {
		t.Fatalf("orphan_cap_fallback rows name %v, want only mid %s", got, ids["mid"])
	}
}
