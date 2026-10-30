// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// lazyPool returns a pgx pool that never connects unless a query runs. The
// posture tests only build the eraser lists, so no query runs.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://lenny:lenny@127.0.0.1:1/lenny?connect_timeout=1")
	if err != nil {
		t.Fatalf("lazy pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// redisPosture wires the Redis-backed stores of the eraser lists over
// miniredis.
func redisPosture(t *testing.T, w *gatewayWiring) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	w.erasureLeaseStore = leasestore.New(rc)
	w.erasureSticky = experimentsticky.NewRedis(rc)
	w.treeBudgetConcrete = treebudget.New(rc, time.Hour)
	w.messagingCoord = sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: sessioninbox.NewMemoryInbox(1),
		DLQ:   sessioninbox.NewDLQ(rc, 1),
	})
}

// postgresPosture wires the Postgres-backed stores of the eraser lists over
// a pool that is never queried.
func postgresPosture(t *testing.T, w *gatewayWiring) {
	w.pgPool = lazyPool(t)
	w.coordMirror = coordlease.NewMemoryStore(nil)
	w.auditOpsStore = auditstore.New(nil)
}

// nilNamesTenant returns the names of tenant entries with a nil function.
func nilNamesTenant(erasers []namedTenantEraser) []string {
	var out []string
	for _, e := range erasers {
		if e.fn == nil {
			out = append(out, e.name)
		}
	}
	sort.Strings(out)
	return out
}

// nilNamesUser returns the names of user-path entries with a nil function.
func nilNamesUser(cfg erasure.Config) []string {
	var out []string
	for _, se := range cfg.SessionScoped {
		if se.DeleteBySession == nil {
			out = append(out, se.Name)
		}
	}
	for _, e := range cfg.UserScoped {
		if e.DeleteByUser == nil {
			out = append(out, e.Name)
		}
	}
	sort.Strings(out)
	return out
}

// TestEraserBuildersListEveryStoreInEveryPosture_spec_12_8 builds both
// eraser lists in each backend posture the gateway starts in. Each list
// passes its startup validator, which requires every ranked store to be
// wired or exempt, and each absent backend appears by name with a nil
// function rather than being omitted.
//
// spec: 12.8 (Compliance Interfaces: Storage backends in erasure scope), 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence), 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestEraserBuildersListEveryStoreInEveryPosture_spec_12_8(t *testing.T) {
	inMemoryNilTenant := []string{
		"audit", "checkpoint_meta", "coordination_mirror", "delegation_budget", "experiment_sticky",
		"issued_tokens", "leases", "quota", "semantic_cache", "session_message_queues",
	}
	inMemoryNilUser := []string{
		"audit", "checkpoint_meta", "coordination_mirror", "delegation_budget", "experiment_sticky",
		"issued_tokens", "leases", "quota", "semantic_cache", "session_message_queues",
	}
	cases := []struct {
		name          string
		setup         func(t *testing.T, w *gatewayWiring) *semanticcache.InMemory
		withQuota     bool
		wantNilTenant []string
		wantNilUser   []string
	}{
		{
			name:          "no Redis, no Postgres, semantic cache off, no messaging coordinator",
			setup:         func(*testing.T, *gatewayWiring) *semanticcache.InMemory { return nil },
			wantNilTenant: inMemoryNilTenant,
			wantNilUser:   inMemoryNilUser,
		},
		{
			name: "semantic cache on",
			setup: func(*testing.T, *gatewayWiring) *semanticcache.InMemory {
				return semanticcache.NewInMemory(nil, 0, 0, time.Now)
			},
			wantNilTenant: withoutString(inMemoryNilTenant, "semantic_cache"),
			wantNilUser:   withoutString(inMemoryNilUser, "semantic_cache"),
		},
		{
			name: "Redis only",
			setup: func(t *testing.T, w *gatewayWiring) *semanticcache.InMemory {
				redisPosture(t, w)
				return nil
			},
			wantNilTenant: []string{"audit", "checkpoint_meta", "coordination_mirror", "issued_tokens", "quota", "semantic_cache"},
			wantNilUser:   []string{"audit", "checkpoint_meta", "coordination_mirror", "issued_tokens", "quota", "semantic_cache"},
		},
		{
			name: "Postgres only",
			setup: func(t *testing.T, w *gatewayWiring) *semanticcache.InMemory {
				postgresPosture(t, w)
				return nil
			},
			wantNilTenant: []string{"delegation_budget", "experiment_sticky", "leases", "semantic_cache", "session_message_queues"},
			// The user path deletes the delegation_tree_budget rows through
			// Postgres even without Redis, because the §8.6 denial store
			// writes those rows whenever Postgres is configured.
			wantNilUser: []string{"experiment_sticky", "leases", "semantic_cache", "session_message_queues"},
		},
		{
			// --memory-enabled=false leaves w.memories nil; both paths list
			// memory by name with a nil function so erasure skips it rather
			// than calling DeleteByUser on a nil store.
			name: "memory backend disabled",
			setup: func(_ *testing.T, w *gatewayWiring) *semanticcache.InMemory {
				w.memories = nil
				return nil
			},
			wantNilTenant: withString(inMemoryNilTenant, "memory"),
			wantNilUser:   withString(inMemoryNilUser, "memory"),
		},
		{
			name: "full posture",
			setup: func(t *testing.T, w *gatewayWiring) *semanticcache.InMemory {
				redisPosture(t, w)
				postgresPosture(t, w)
				return semanticcache.NewInMemory(nil, 0, 0, time.Now)
			},
			withQuota: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := inMemoryEraserWiring()
			cache := tc.setup(t, w)
			var accum *quotafailopen.Accumulator
			if tc.withQuota {
				accum = quotafailopen.New()
			}
			pools := credentialpoolstore.NewMemory()

			tenant := w.buildTenantErasers(nil, accum, pools, cache)
			if err := erasure.ValidateTenantOrder(tenantEraserNames(tenant)); err != nil {
				t.Fatalf("tenant list rejected: %v", err)
			}
			if got := nilNamesTenant(tenant); strings.Join(got, ",") != strings.Join(tc.wantNilTenant, ",") {
				t.Errorf("tenant nil-function entries = %v, want %v", got, tc.wantNilTenant)
			}

			user := erasure.Config{
				SessionScoped: w.buildSessionScopedErasers(),
				UserScoped:    w.buildUserErasers(nil, accum, pools, cache),
			}
			if err := erasure.ValidateOrder(user); err != nil {
				t.Fatalf("user list rejected: %v", err)
			}
			if got := nilNamesUser(user); strings.Join(got, ",") != strings.Join(tc.wantNilUser, ",") {
				t.Errorf("user nil-function entries = %v, want %v", got, tc.wantNilUser)
			}
		})
	}
}

// withString returns names with s added, sorted.
func withString(names []string, s string) []string {
	out := append(append([]string(nil), names...), s)
	sort.Strings(out)
	return out
}

// withoutString returns names without s.
func withoutString(names []string, s string) []string {
	var out []string
	for _, n := range names {
		if n != s {
			out = append(out, n)
		}
	}
	return out
}

// TestTenantEraserSkipsNilFunctionEntries_spec_12_8 asserts that Phase 4
// skips an entry listed with a nil function, leaves its name out of the
// receipt tally, and still runs the entries after it.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestTenantEraserSkipsNilFunctionEntries_spec_12_8(t *testing.T) {
	var ran []string
	step := func(name string) namedTenantEraser {
		return namedTenantEraser{name, func(context.Context, string) (int, error) {
			ran = append(ran, name)
			return 1, nil
		}}
	}
	e := &tenantEraser{erasers: []namedTenantEraser{
		{"leases", nil},
		step("quota"),
		{"audit", nil},
		step("sessions"),
	}}
	counts, err := e.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if strings.Join(ran, ",") != "quota,sessions" {
		t.Fatalf("ran = %v, want quota then sessions", ran)
	}
	for _, skipped := range []string{"leases", "audit"} {
		if _, ok := counts[skipped]; ok {
			t.Errorf("counts carry %q although it never ran: %v", skipped, counts)
		}
	}
	if counts["quota"] != 1 || counts["sessions"] != 1 {
		t.Errorf("counts = %v, want quota=1 sessions=1", counts)
	}
}

// TestUserErasureSkipsMemoryWhenMemoryBackendDisabled_spec_12_8 runs the
// user erasure over the gateway's user eraser list with the memory backend
// disabled (--memory-enabled=false leaves the MemoryStore nil). The job
// completes, skips the memory entry, and leaves it out of the counts rather
// than calling DeleteByUser on a nil store.
//
// spec: 12.8 (Compliance Interfaces: Storage backends in erasure scope), 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence)
func TestUserErasureSkipsMemoryWhenMemoryBackendDisabled_spec_12_8(t *testing.T) {
	w := inMemoryEraserWiring()
	w.memories = nil
	orch := erasure.New(erasure.Config{
		UserScoped: w.buildUserErasers(nil, nil, credentialpoolstore.NewMemory(), nil),
	})
	var (
		res erasure.Result
		err error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("user erasure panicked with the memory backend disabled: %v", r)
			}
		}()
		res, err = orch.DeleteByUser(context.Background(), "acme", "alice")
	}()
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if _, ok := res.Deleted["memory"]; ok {
		t.Errorf("counts carry memory although the backend is disabled: %v", res.Deleted)
	}
	if _, ok := res.Deleted["sessions"]; !ok {
		t.Errorf("counts lack sessions; the entries after memory did not run: %v", res.Deleted)
	}
}
