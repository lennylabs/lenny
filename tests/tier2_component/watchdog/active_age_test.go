//go:build component

// SPDX-License-Identifier: MIT

package watchdog_component_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// diagnosis: the watchdog's maxSessionAge sweep, run against the Postgres
// session store, did not measure age as active time. Either a session older
// than the cap by wall clock and under it by active time was expired, a
// session over the cap by active time survived, a suspended session was
// expired by an age cap or had its row rewritten, or a resume_pending session
// over the cap by its accrued active age was not expired. A failure means
// sessions lose their age budget to time spent paused or in recovery, or a
// suspended session that may resume on its next message is expired.
func TestWatchdogMaxAgeUsesActiveAge_spec_6_2(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	tick := time.Now().UTC().Truncate(time.Microsecond)

	type seed struct {
		state       session.State
		createdAgo  time.Duration
		updatedAgo  time.Duration
		accumulated time.Duration
		wantExpired bool
	}
	seeds := map[string]seed{
		"young by active time": {session.StateRunning, 5 * time.Hour, 10 * time.Minute, 5 * time.Minute, false},
		"over by active time":  {session.StateRunning, 5 * time.Hour, 50 * time.Minute, 20 * time.Minute, true},
		"suspended over cap":   {session.StateSuspended, 48 * time.Hour, 24 * time.Hour, 2 * time.Hour, false},
		"resume_pending over":  {session.StateResumePending, 5 * time.Hour, time.Minute, 2 * time.Hour, true},
		"resume_pending young": {session.StateResumePending, 50 * time.Hour, time.Minute, 0, false},
	}
	ids := map[string]string{}
	for name, s := range seeds {
		id := newUUID(t)
		ids[name] = id
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: s.state, RuntimeRef: "echo",
			CreatedAt:                    tick.Add(-s.createdAgo),
			UpdatedAt:                    tick.Add(-s.updatedAgo),
			LastAgentActivityAt:          tick.Add(-time.Second),
			AccumulatedSessionAgeSeconds: s.accumulated.Seconds(),
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	huge := watchdog.DefaultMaxSessionAgeSeconds * 100
	w := watchdog.New(store, watchdog.StaticTenants{tenant}, watchdog.Config{
		MaxSessionAgeSeconds:           3600,
		MaxIdleSeconds:                 huge,
		MaxAwaitingClientActionSeconds: huge,
		MaxSuspendedPodHoldSeconds:     huge,
		MaxResumePendingSeconds:        huge,
		MaxResumingSeconds:             huge,
	}, nil)
	res, err := w.Tick(ctx, tick)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 2 {
		t.Errorf("Expirations = %d, want 2", res.Expirations)
	}
	for name, s := range seeds {
		row, err := store.Get(ctx, tenant, ids[name])
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		if got := row.State == session.StateExpired; got != s.wantExpired {
			t.Errorf("%s: state %q, want expired=%v", name, row.State, s.wantExpired)
		}
		if !s.wantExpired && !row.UpdatedAt.Equal(tick.Add(-s.updatedAgo)) {
			t.Errorf("%s: a surviving row was rewritten (UpdatedAt %v)", name, row.UpdatedAt)
		}
	}
}
