// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.3 lines 389-390 — a session's retryPolicy.maxSessionAgeSeconds
// clamps the platform-wide watchdog cap. A tighter per-session value
// expires the session earlier than the deployer default would.
// F-7.3.24.

func seedRowWithRetryPolicy(t *testing.T, store sessionstore.Store, id, tenant string, born time.Time, policy *session.RetryPolicy) {
	t.Helper()
	row := sessionstore.Session{
		ID:          id,
		TenantID:    tenant,
		State:       session.StateRunning,
		CreatedAt:   born,
		UpdatedAt:   born,
		RetryPolicy: policy,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestPerSessionMaxAgeExpiresEarlierThanPlatformCap_F_7_3_24(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Per-session cap of 30 minutes; platform default is 2 hours.
	seedRowWithRetryPolicy(t, store, "sess_short", "acme", born, &session.RetryPolicy{
		MaxSessionAgeSeconds: 1800,
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// Sweep 45 minutes after birth — over the per-session 30m cap but
	// well under the 2h platform default.
	res, err := w.Tick(context.Background(), born.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Errorf("Expirations: got %d, want 1 (per-session 30m cap should fire)", res.Expirations)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_short")
	if row.State != session.StateExpired {
		t.Errorf("state: got %q, want expired", row.State)
	}
}

func TestPerSessionMaxAgeOverrideRespectsPlatformCap_F_7_3_24(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Per-session "override" of 24 hours; the gateway clamp at admission
	// would have brought this down to the platform cap, but the
	// watchdog also clamps to be defensive in case a row landed via
	// a different path (a §10.4 coordinator-handoff write, replay).
	seedRowWithRetryPolicy(t, store, "sess_huge", "acme", born, &session.RetryPolicy{
		MaxSessionAgeSeconds: 86400, // 24h, well above the 2h default
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// Three hours: well within the per-session override but past the
	// platform 2h cap. The platform cap must still fire.
	res, err := w.Tick(context.Background(), born.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Errorf("Expirations: got %d, want 1 (platform cap should still bound)", res.Expirations)
	}
}

func TestNilRetryPolicyFallsThroughToPlatformCap_F_7_3_24(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRowWithRetryPolicy(t, store, "sess_default", "acme", born, nil)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{MaxIdleSeconds: idleCapDisabled}, nil)

	// One hour: under both the platform 2h cap and the per-session
	// override (which is absent here). No expiry.
	res, err := w.Tick(context.Background(), born.Add(time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 0 {
		t.Errorf("Expirations: got %d, want 0", res.Expirations)
	}
	// Three hours: over the platform 2h cap. Expiry must fire.
	res, err = w.Tick(context.Background(), born.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Errorf("Expirations: got %d, want 1 (platform cap should fire)", res.Expirations)
	}
}

// A zero per-session value behaves the same as nil — falls through to
// the platform cap rather than zero-ing it out.
func TestZeroRetryPolicyMaxSessionAgeFallsThrough_F_7_3_24(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRowWithRetryPolicy(t, store, "sess_zero", "acme", born, &session.RetryPolicy{
		MaxSessionAgeSeconds: 0, // explicit zero
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{MaxIdleSeconds: idleCapDisabled}, nil)

	res, err := w.Tick(context.Background(), born.Add(time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 0 {
		t.Errorf("zero MaxSessionAgeSeconds must not expire at 1h: Expirations=%d", res.Expirations)
	}
}
