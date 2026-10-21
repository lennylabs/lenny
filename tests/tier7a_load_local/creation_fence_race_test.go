// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the creation fence against the lifecycle drain. Each
// round races concurrent session creates against the commit of a lifecycle
// marker and then runs one drain, over the in-memory store and over the
// Postgres store, and checks the fence's invariant: once the marker has
// committed and one drain has run, no non-terminal row of the fenced scope
// exists, every refused create inserted nothing, and every admitted create
// left its row.
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// fenceStores is one posture's session, tenant, and user stores.
type fenceStores struct {
	sessions sessionstore.Store
	tenants  tenantstore.Store
	users    userstore.Store
	// newTenant provisions a fresh tenant with the users alice and bob.
	newTenant func(t *testing.T) string
}

// storeTerminator is the drain's forced terminal transition over the
// session store alone: it writes the §24.11 failed state under the row
// lock and reports whether this call made the transition.
type storeTerminator struct{ store sessionstore.Store }

func (s storeTerminator) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	var prev session.State
	transitioned := false
	row, err := s.store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		prev = r.State
		if session.IsTerminal(r.State) {
			return nil
		}
		r.State, r.FailureClass, r.FailureReason = session.StateFailed, session.FailureClassRuntime, "FORCE_TERMINATED"
		transitioned = true
		return nil
	})
	return row, prev, transitioned, err
}

func memFenceStores() fenceStores {
	tenants, users := tenantstore.NewMemory(), userstore.NewMemory()
	return fenceStores{
		sessions: memstore.New(memstore.WithCreationMarkers(memstore.StoreMarkers(tenants, users))),
		tenants:  tenants,
		users:    users,
		newTenant: func(t *testing.T) string {
			t.Helper()
			id := "t-" + uuid.NewString()[:8]
			if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: id}); err != nil {
				t.Fatalf("seed tenant: %v", err)
			}
			seedFenceUsers(t, users, id)
			return id
		},
	}
}

func pgFenceStores(t *testing.T) fenceStores {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	tenants, users := tenantpg.New(pg.Pool), userpg.New(pg.Pool)
	return fenceStores{
		sessions: sessionpg.New(pg.Pool),
		tenants:  tenants,
		users:    users,
		newTenant: func(t *testing.T) string {
			t.Helper()
			id := "t-" + uuid.NewString()[:8]
			if _, err := pg.Pool.Exec(context.Background(),
				`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01}); err != nil {
				t.Fatalf("seed tenant: %v", err)
			}
			seedFenceUsers(t, users, id)
			return id
		},
	}
}

func seedFenceUsers(t *testing.T, users userstore.Store, tenant string) {
	t.Helper()
	for _, sub := range []string{"alice", "bob"} {
		if err := users.Create(context.Background(), userstore.User{TenantID: tenant, Subject: sub}); err != nil {
			t.Fatalf("seed user %s: %v", sub, err)
		}
	}
}

// fenceMarker is one lifecycle marker: the scope it fences and its write.
type fenceMarker struct {
	name      string
	scopeUser string // "" fences the whole tenant
	write     func(ctx context.Context, s fenceStores, tenant string) error
}

func fenceMarkers() []fenceMarker {
	return []fenceMarker{
		{"user erasure restriction", "alice", func(ctx context.Context, s fenceStores, tenant string) error {
			_, err := s.users.Update(ctx, tenant, "alice", func(u *userstore.User) error {
				u.ProcessingRestricted, u.ErasureJobID = true, "job-1"
				return nil
			})
			return err
		}},
		{"tenant suspension", "", func(ctx context.Context, s fenceStores, tenant string) error {
			_, err := s.tenants.Update(ctx, tenant, func(tn *tenantstore.Tenant) error {
				tn.Suspended = true
				return nil
			})
			return err
		}},
		{"tenant deletion phase 1", "", func(ctx context.Context, s fenceStores, tenant string) error {
			_, err := s.tenants.Update(ctx, tenant, func(tn *tenantstore.Tenant) error {
				tn.State = tenantstore.TenantStateDisabling
				return nil
			})
			return err
		}},
	}
}

type createOutcome struct {
	row sessionstore.Session
	err error
}

// runFenceRound races creates by alice and bob against one marker write,
// drains the scope once the marker has committed, and checks the invariant.
func runFenceRound(t *testing.T, s fenceStores, m fenceMarker, rng *rand.Rand, creates int) {
	t.Helper()
	ctx := context.Background()
	tenant := s.newTenant(t)
	delays := make([]time.Duration, creates)
	owners := make([]string, creates)
	for i := range delays {
		delays[i] = time.Duration(rng.Intn(3000)) * time.Microsecond
		owners[i] = []string{"alice", "bob"}[rng.Intn(2)]
	}
	markerDelay := time.Duration(rng.Intn(3000)) * time.Microsecond

	outcomes := make([]createOutcome, creates)
	var wg sync.WaitGroup
	for i := 0; i < creates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(delays[i])
			row := sessionstore.Session{ID: uuid.NewString(), TenantID: tenant, UserID: owners[i], RuntimeRef: "echo", State: session.StateRunning}
			outcomes[i] = createOutcome{row: row, err: s.sessions.Create(ctx, row)}
		}(i)
	}
	time.Sleep(markerDelay)
	if err := m.write(ctx, s, tenant); err != nil {
		t.Fatalf("marker write: %v", err)
	}
	// One drain, run as soon as the marker has committed, concurrently with
	// the creates still in flight.
	if _, err := admin.DrainSessions(ctx, s.sessions, storeTerminator{s.sessions}, tenant, m.scopeUser); err != nil {
		t.Fatalf("drain after the marker commit: %v", err)
	}
	wg.Wait()
	checkFenceInvariant(t, s, tenant, m, outcomes)
}

func checkFenceInvariant(t *testing.T, s fenceStores, tenant string, m fenceMarker, outcomes []createOutcome) {
	t.Helper()
	ctx := context.Background()
	for _, o := range outcomes {
		inScope := m.scopeUser == "" || o.row.UserID == m.scopeUser
		got, err := s.sessions.Get(ctx, tenant, o.row.ID)
		var ref *sessionstore.CreationRefusal
		switch {
		case o.err == nil:
			if err != nil {
				t.Fatalf("admitted create %s left no row: %v", o.row.ID, err)
			}
			if inScope && !session.IsTerminal(got.State) {
				t.Fatalf("%s: row %s of the fenced scope is %s after the drain", m.name, o.row.ID, got.State)
			}
		case errors.As(o.err, &ref):
			if !inScope {
				t.Fatalf("%s: create outside the scope refused: %v", m.name, o.err)
			}
			if !errors.Is(err, sessionstore.ErrNotFound) {
				t.Fatalf("refused create %s inserted a row (%v)", o.row.ID, err)
			}
		default:
			t.Fatalf("create %s: unexpected error %v", o.row.ID, o.err)
		}
	}
	rows, err := s.sessions.List(ctx, tenant, sessionstore.ListFilter{UserID: m.scopeUser, Primary: true})
	if err != nil {
		t.Fatalf("list scope: %v", err)
	}
	for _, r := range rows {
		if !session.IsTerminal(r.State) {
			t.Fatalf("%s: non-terminal row %s in the fenced scope after one drain", m.name, r.ID)
		}
	}
}

// TestCreationFenceRaceAgainstMarkerAndDrain_spec_12_8 runs randomized
// interleavings of concurrent creates against each lifecycle marker's
// commit and one drain, over the in-memory store and the Postgres store,
// under -race.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 24.11 (Session Investigation)
// diagnosis: a failure means a create can commit into a fenced scope after
// the marker without the drain seeing it, or a refused create leaves a row:
// the fence's marker read is not serialized with the marker write on that
// store, so a lifecycle operation can act on a scope that still holds a live
// session.
func TestCreationFenceRaceAgainstMarkerAndDrain_spec_12_8(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	postures := []struct {
		name    string
		stores  func(t *testing.T) fenceStores
		rounds  int
		creates int
	}{
		{"memstore", func(*testing.T) fenceStores { return memFenceStores() }, 40, 24},
		{"pgstore", pgFenceStores, 8, 12},
	}
	for _, p := range postures {
		t.Run(p.name, func(t *testing.T) {
			s := p.stores(t)
			for _, m := range fenceMarkers() {
				for r := 0; r < p.rounds; r++ {
					t.Run(fmt.Sprintf("%s/round-%d", m.name, r), func(t *testing.T) {
						runFenceRound(t, s, m, rng, p.creates)
					})
				}
			}
		})
	}
}
