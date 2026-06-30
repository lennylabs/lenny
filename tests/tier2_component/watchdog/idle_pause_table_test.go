//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §6.2 maxClientIdleSeconds clock against the
// Postgres-backed SessionStore. The §11.3 watchdog lists session rows by
// state from the real pgstore and idle-expires those whose idle clock has
// run past the effective bound. This exercises the clock's own pause table
// end-to-end: the sweep must list and expire the clock-running states
// (running, input_required, awaiting_client_action) and must leave the
// paused states (suspended, resume_pending, resuming, finalizing) untouched
// by the idle path.
package watchdog_component_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// newUUID returns a fresh random UUIDv4 string. Session IDs are UUIDs per
// §12.6; the sessions.id column is typed UUID.
func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// startStore brings up a Postgres container with the production migrations
// applied and returns the pgstore plus the raw handle.
func startStore(t *testing.T) (*pgstore.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	return pgstore.New(pg.Pool), pg
}

// freshTenant inserts a uniquely-named tenant and returns its id.
func freshTenant(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	id := "t-" + newUUID(t)[:8]
	if _, err := pg.Pool.Exec(
		ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01},
	); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
	return id
}

// spec: 6.2 (maxClientIdleSeconds clock), 11.3 line 199 (max client idle
// row), 9.2 (elicitation-wait idle clock)
//
// diagnosis: the §11.3 watchdog idle sweep did not behave as specified
// against the Postgres-backed SessionStore. The idle clock must run in
// running, input_required, and awaiting_client_action and reclaim an
// abandoned session there, and must stay paused in suspended,
// resume_pending, resuming, and finalizing so a deliberately halted or
// recovering session is not falsely idle-terminated. A failure means the
// state-filtered List path or the per-state idle transition diverged from
// the spec pause table when run against real Postgres.
func TestWatchdogIdleSweepPauseTable_spec_6_2(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	born := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)

	// clockRunning: the idle clock runs here, so an abandoned session is
	// idle-reclaimed. paused: the idle clock is paused here.
	clockRunning := []session.State{
		session.StateRunning,
		session.StateInputRequired,
		session.StateAwaitingClientAction,
	}
	paused := []session.State{
		session.StateSuspended,
		session.StateResumePending,
		session.StateResuming,
		session.StateFinalizing,
	}

	ids := map[session.State]string{}
	for _, st := range append(append([]session.State{}, clockRunning...), paused...) {
		id := newUUID(t)
		ids[st] = id
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: st, RuntimeRef: "echo",
			CreatedAt: born, UpdatedAt: born,
		}); err != nil {
			t.Fatalf("create %s row: %v", st, err)
		}
	}

	// A 600s idle cap with every other expiry sweep disabled, so only the
	// idle path can transition a row.
	huge := watchdog.DefaultMaxSessionAgeSeconds * 100
	w := watchdog.New(store, watchdog.StaticTenants{tenant}, watchdog.Config{
		MaxIdleSeconds:                 600,
		MaxSessionAgeSeconds:           huge,
		MaxAwaitingClientActionSeconds: huge,
		MaxSuspendedPodHoldSeconds:     huge,
		MaxResumePendingSeconds:        huge,
		MaxResumingSeconds:             huge,
		MaxFinalizingSeconds:           huge,
	}, nil)

	// One hour after birth: every row is well past the 600s idle cap. Only
	// the clock-running states must be idle-expired.
	res, err := w.Tick(ctx, born.Add(time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != len(clockRunning) {
		t.Fatalf("IdleExpirations: got %d, want %d (running, input_required, awaiting_client_action)",
			res.IdleExpirations, len(clockRunning))
	}

	for _, st := range clockRunning {
		got, err := store.Get(ctx, tenant, ids[st])
		if err != nil {
			t.Fatalf("get %s row: %v", st, err)
		}
		if got.State != session.StateExpired {
			t.Errorf("%s session: state %q, want expired (idle clock runs there)", st, got.State)
		}
		if got.FailureReason != string(session.FailureExpiredIdle) {
			t.Errorf("%s session: FailureReason %q, want %q", st, got.FailureReason, session.FailureExpiredIdle)
		}
	}
	for _, st := range paused {
		got, err := store.Get(ctx, tenant, ids[st])
		if err != nil {
			t.Fatalf("get %s row: %v", st, err)
		}
		if got.State != st {
			t.Errorf("%s session: state %q, want %q (idle clock paused there)", st, got.State, st)
		}
	}
}
