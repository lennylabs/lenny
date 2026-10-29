//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// expiryWarningCounter is a TerminalHook that also implements
// watchdog.ExpiryWarningNotifier. Every simulated replica's watchdog shares
// one instance, so it counts the warnings all replicas sent per session.
type expiryWarningCounter struct {
	mu    sync.Mutex
	perID map[string]int
}

func (*expiryWarningCounter) BeginTerminal(string) func() { return func() {} }

func (*expiryWarningCounter) OnSessionTerminal(context.Context, session.State, sessionstore.Session) {
}

func (c *expiryWarningCounter) OnSessionExpiringSoon(_ context.Context, sess sessionstore.Session, _, _ int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.perID[sess.ID]++
}

// expiryWarningConfig sets a 7200s maxSessionAge cap and moves every other
// watchdog deadline out of the way.
func expiryWarningConfig() watchdog.Config {
	const off = 100 * 24 * 3600
	return watchdog.Config{
		MaxSessionAgeSeconds:           7200,
		MaxIdleSeconds:                 off,
		MaxAwaitingClientActionSeconds: off,
		MaxSuspendedPodHoldSeconds:     off,
		MaxSuspendedSessionSeconds:     off,
		MaxResumePendingSeconds:        off,
		MaxResumingSeconds:             off,
	}
}

// spec: §11.3 (Session expiry warning), §10.1.1 (Primary:)
// diagnosis: the Postgres session store did not persist or read back
//
//	expiry_warned_cap_seconds (migration 0196), or two watchdogs sweeping the
//	same Postgres rows concurrently each sent the session_expiring_soon
//	warning. The warning's locked-row claim is not serializing on the row
//	lock, so every gateway replica warns the same client once.
func TestExpiryWarningOnceAcrossReplicasOverPostgres_spec_11_3(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	t.Run("column round trip", func(t *testing.T) {
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := mustGetSession(t, ctx, store, tenant, id).ExpiryWarnedCapSeconds; got != 0 {
			t.Fatalf("a new row's ExpiryWarnedCapSeconds = %d, want 0", got)
		}
		if _, err := store.Update(ctx, tenant, id, func(r *sessionstore.Session) error {
			r.ExpiryWarnedCapSeconds = 7200
			return nil
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got := mustGetSession(t, ctx, store, tenant, id).ExpiryWarnedCapSeconds; got != 7200 {
			t.Fatalf("Get after Update: ExpiryWarnedCapSeconds = %d, want 7200", got)
		}
		rows, err := store.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, r := range rows {
			if r.ID == id && r.ExpiryWarnedCapSeconds != 7200 {
				t.Fatalf("List: ExpiryWarnedCapSeconds = %d, want 7200", r.ExpiryWarnedCapSeconds)
			}
		}
	})

	t.Run("two replicas warn once", func(t *testing.T) {
		wt := freshTenant(t, ctx, pg)
		const n = 12
		ids := make([]string, n)
		for i := range ids {
			ids[i] = newUUID(t)
			// 6900s of active age under a 7200s cap: inside the 300s window
			// for the next five minutes of wall clock.
			if err := store.Create(ctx, sessionstore.Session{
				ID: ids[i], TenantID: wt, State: session.StateRunning, RuntimeRef: "echo",
				LastAgentActivityAt:          time.Now().UTC(),
				AccumulatedSessionAgeSeconds: 6900,
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}
		hook := &expiryWarningCounter{perID: map[string]int{}}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for r := 0; r < 2; r++ {
			w := watchdog.New(store, watchdog.StaticTenants{wt}, expiryWarningConfig(), nil).WithTerminalHook(hook)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for p := 0; p < 2; p++ {
					if _, err := w.Tick(ctx, time.Now()); err != nil {
						t.Errorf("Tick: %v", err)
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		for _, id := range ids {
			if got := hook.perID[id]; got != 1 {
				t.Errorf("session %s: %d warnings across two replicas, want 1", id, got)
			}
			row := mustGetSession(t, ctx, store, wt, id)
			if row.ExpiryWarnedCapSeconds != 7200 || row.State != session.StateRunning {
				t.Errorf("session %s: state %s, ExpiryWarnedCapSeconds %d, want running and 7200", id, row.State, row.ExpiryWarnedCapSeconds)
			}
		}
	})
}

func mustGetSession(t *testing.T, ctx context.Context, store sessionstore.Store, tenant, id string) sessionstore.Session {
	t.Helper()
	row, err := store.Get(ctx, tenant, id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return row
}
