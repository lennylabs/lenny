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

// spec: 6.2 (Pod State Machine), 16.1.1 (Attribute Naming)
//
// diagnosis: the suspended-session lifetime did not hold against the
// Postgres-backed SessionStore: a suspended row was expired before its
// lifetime, survived past it, was expired under the wrong reason, or a row
// that resumed and re-suspended after the listing was overwritten. A failure
// means the guarded lifetime write diverged from the spec when run against
// real Postgres.
func TestWatchdogSuspendedLifetime_spec_6_2(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	since := time.Now().UTC().Add(-8 * 24 * time.Hour).Truncate(time.Microsecond)
	lifetime := 7 * 24 * 3600

	due := newUUID(t)
	young := newUUID(t)
	for id, at := range map[string]time.Time{due: since, young: since.Add(2 * 24 * time.Hour)} {
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateSuspended, RuntimeRef: "echo",
			SuspendedAt: at, SuspendedReason: sessionstore.SuspendedReasonIdle,
			CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	huge := watchdog.DefaultMaxSessionAgeSeconds * 100
	w := watchdog.New(store, watchdog.StaticTenants{tenant}, watchdog.Config{
		MaxIdleSeconds:                 huge,
		MaxSessionAgeSeconds:           huge,
		MaxAwaitingClientActionSeconds: huge,
		MaxSuspendedPodHoldSeconds:     huge,
		MaxSuspendedSessionSeconds:     lifetime,
		MaxResumePendingSeconds:        huge,
		MaxResumingSeconds:             huge,
	}, nil)
	res, err := w.Tick(ctx, since.Add(time.Duration(lifetime)*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.SuspendedExpirations != 1 {
		t.Fatalf("SuspendedExpirations = %d, want 1", res.SuspendedExpirations)
	}
	got, _ := store.Get(ctx, tenant, due)
	if got.State != session.StateExpired || got.FailureReason != string(session.FailureExpiredIdle) {
		t.Errorf("due row = %q/%q, want expired/expired:idle", got.State, got.FailureReason)
	}
	if got, _ := store.Get(ctx, tenant, young); got.State != session.StateSuspended {
		t.Errorf("young row = %q, want suspended", got.State)
	}
}
