// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// spec: §27.3.1 line 94 / §27.6 line 201/204 — the idle-timeout sweep
// enumerates playground session records idle past the reclamation window
// and revokes each through the shared revocation primitive with reason
// idle_timeout. These tests cover the store enumeration and the handler
// sweep. F-27.3.7.

// TestMemoryIdleSessionsSelectsIdleRecords: IdleSessions returns records
// whose last activity predates the cutoff and skips active, invalidated,
// and expired records.
func TestMemoryIdleSessionsSelectsIdleRecords_spec_27_6_201(t *testing.T) {
	store := NewMemorySessionStore()
	ctx := context.Background()
	now := time.Now()
	cutoff := now.Add(-30 * time.Minute)

	// Idle: last activity is an hour ago, before the cutoff.
	mustPut(t, store, "acme", "idle1", SessionRecord{
		TenantID: "acme", IssuedAt: now.Add(-2 * time.Hour), LastActivityAt: now.Add(-time.Hour),
	})
	// Active: last activity is recent, after the cutoff.
	mustPut(t, store, "acme", "active1", SessionRecord{
		TenantID: "acme", IssuedAt: now.Add(-2 * time.Hour), LastActivityAt: now.Add(-time.Minute),
	})
	// Idle but already invalidated: skipped (the §11.4 path handled it).
	mustPut(t, store, "acme", "inv1", SessionRecord{
		TenantID: "acme", IssuedAt: now.Add(-2 * time.Hour), LastActivityAt: now.Add(-time.Hour), Invalidated: true,
	})
	// Legacy record with no LastActivityAt falls back to IssuedAt (idle).
	mustPut(t, store, "globex", "legacy1", SessionRecord{
		TenantID: "globex", IssuedAt: now.Add(-time.Hour),
	})

	refs, err := store.IdleSessions(ctx, cutoff)
	if err != nil {
		t.Fatalf("IdleSessions: %v", err)
	}
	got := map[string]string{}
	for _, r := range refs {
		got[r.ID] = r.Tenant
	}
	if len(got) != 2 {
		t.Fatalf("idle refs = %v, want exactly idle1 and legacy1", got)
	}
	if got["idle1"] != "acme" {
		t.Errorf("idle1 not selected with tenant acme: %v", got)
	}
	if got["legacy1"] != "globex" {
		t.Errorf("legacy1 (IssuedAt fallback) not selected: %v", got)
	}
	if _, ok := got["active1"]; ok {
		t.Errorf("active1 was selected as idle")
	}
	if _, ok := got["inv1"]; ok {
		t.Errorf("invalidated record was selected as idle")
	}
}

// TestRedisIdleSessionsScansRecords: the Redis store's SCAN-based
// enumeration finds idle records across tenants and ignores the fan-in /
// user-index keys that share the t: prefix space.
func TestRedisIdleSessionsScansRecords_spec_27_6_201(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisSessionStore(client)
	ctx := context.Background()
	now := time.Now()
	cutoff := now.Add(-30 * time.Minute)

	// PutSession also writes the pg:sess-tenant: fan-in index and (for a
	// record with a UserID) the pg:user: index, so the SCAN pattern must
	// not pick those up.
	mustPut(t, store, "acme", "idleR", SessionRecord{
		TenantID: "acme", UserID: "alice@acme.com",
		IssuedAt: now.Add(-2 * time.Hour), LastActivityAt: now.Add(-time.Hour),
	})
	mustPut(t, store, "acme", "activeR", SessionRecord{
		TenantID: "acme", UserID: "bob@acme.com",
		IssuedAt: now.Add(-2 * time.Hour), LastActivityAt: now.Add(-time.Minute),
	})

	refs, err := store.IdleSessions(ctx, cutoff)
	if err != nil {
		t.Fatalf("IdleSessions: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != "idleR" || refs[0].Tenant != "acme" {
		t.Fatalf("idle refs = %v, want exactly {acme, idleR}", refs)
	}
}

// TestRevokeSessionAdminRevokeThroughRedisPrimitive: the §27.6
// admin-revocation entry point (Handler.RevokeSession called with
// RevokeAdmin) drives the same Redis-backed revocation primitive as
// logout, user.invalidated, and idle timeout — a DEL on the
// session-record key and a SET on the per-bearer pg:revoked:{jti} key
// (§27.3.1 line 207) — and attributes the §27.8 counter to
// admin_revoke. The other reasons each already have a
// RedisSessionStore-backed test verifying the real keys
// (TestRedisIdleSessionsScansRecords for idle enumeration,
// TestRedisSessionStoreUserIndexAndRevoke_spec_11_4 for
// user_invalidated); admin_revoke previously only had in-process
// (MemorySessionStore) coverage.
//
// diagnosis: a failure means an admin-triggered RevokeSession call no
// longer performs the real Redis DEL/SET pair the spec requires, or no
// longer attributes the revocation to the admin_revoke reason.
func TestRevokeSessionAdminRevokeThroughRedisPrimitive_spec_27_3_1_207(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisSessionStore(client)
	h, _, m := newRevokeHandler(t, store)
	ctx := context.Background()

	mustPut(t, store, "acme", "admin-sess", SessionRecord{
		TenantID: "acme", UserID: "bob@acme.com", BearerJTIs: []string{"jti-admin-redis"},
	})

	// The session-record key exists and the revocation marker does not,
	// before the admin revokes the session.
	if n, err := client.Exists(ctx, sessionKey("acme", "admin-sess")).Result(); err != nil || n != 1 {
		t.Fatalf("session-record key missing before revocation: n=%d err=%v", n, err)
	}
	if n, err := client.Exists(ctx, revokedKey("acme", "jti-admin-redis")).Result(); err != nil || n != 0 {
		t.Fatalf("revocation marker present before revocation: n=%d err=%v", n, err)
	}

	if err := h.RevokeSession(ctx, "acme", "admin-sess", RevokeAdmin); err != nil {
		t.Fatalf("RevokeSession(admin_revoke): %v", err)
	}

	// spec: §27.3.1 line 207 — "admin revocation ... drive[s] the same
	// revocation path: DEL on the session-record key + SET on the
	// per-bearer pg:revoked:{jti} key + PUBLISH".
	if n, err := client.Exists(ctx, sessionKey("acme", "admin-sess")).Result(); err != nil || n != 0 {
		t.Errorf("session-record key survived RevokeSession(admin_revoke): n=%d err=%v", n, err)
	}
	if n, err := client.Exists(ctx, revokedKey("acme", "jti-admin-redis")).Result(); err != nil || n != 1 {
		t.Errorf("pg:revoked:{jti} key not set after RevokeSession(admin_revoke): n=%d err=%v", n, err)
	}
	// spec: §27.8 — lenny_playground_session_revocations_total's reason
	// label includes admin_revoke.
	if got := testutil.ToFloat64(m.revocations.WithLabelValues(string(RevokeAdmin))); got != 1 {
		t.Errorf("revocations{reason=admin_revoke} = %v, want 1", got)
	}
}

// TestSweepIdleSessionsRevokesIdleThroughPrimitive: the handler sweep
// revokes idle records through the shared revocation primitive — the
// record is deleted, every minted bearer lands on the deny list, and the
// §27.8 metric attributes the revocation to idle_timeout — while an active
// session survives. F-27.3.7.
func TestSweepIdleSessionsRevokesIdleThroughPrimitive_spec_27_3_1_94(t *testing.T) {
	store := NewMemorySessionStore()
	h, _, m := newRevokeHandler(t, store)
	ctx := context.Background()
	now := h.now()

	// idleReclaimWindow = BearerTTL(15m) + idle grace(300s) = 20m. A record
	// idle for an hour is well past it.
	mustPut(t, store, "acme", "idle1", SessionRecord{
		TenantID: "acme", IssuedAt: now.Add(-2 * time.Hour),
		LastActivityAt: now.Add(-time.Hour), BearerJTIs: []string{"jti-idle"},
	})
	// Active: minted a minute ago — kept.
	mustPut(t, store, "acme", "active1", SessionRecord{
		TenantID: "acme", IssuedAt: now.Add(-2 * time.Hour),
		LastActivityAt: now.Add(-time.Minute), BearerJTIs: []string{"jti-active"},
	})

	revoked, err := h.SweepIdleSessions(ctx)
	if err != nil {
		t.Fatalf("SweepIdleSessions: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked %d, want 1 (idle1 only)", revoked)
	}
	if _, err := store.GetSession(ctx, "acme", "idle1"); err == nil {
		t.Error("idle1 survived the sweep")
	}
	if r, _ := store.IsBearerRevoked(ctx, "acme", "jti-idle"); !r {
		t.Error("idle1 bearer not on the deny list after the sweep")
	}
	if _, err := store.GetSession(ctx, "acme", "active1"); err != nil {
		t.Error("active1 was reclaimed by the sweep")
	}
	if r, _ := store.IsBearerRevoked(ctx, "acme", "jti-active"); r {
		t.Error("active1 bearer was wrongly revoked")
	}
	if got := testutil.ToFloat64(m.revocations.WithLabelValues(string(RevokeIdleTimeout))); got != 1 {
		t.Errorf("revocations{reason=idle_timeout} = %v, want 1", got)
	}
}

// TestSweepIdleSessionsDoesNotReapBetweenMints: a session that minted a
// bearer more than the idle grace (5m) ago but less than a full bearer
// lifetime ago is NOT reaped — the window exceeds BearerTTL so an active
// user who has not yet re-minted is never disconnected. F-27.3.7.
func TestSweepIdleSessionsDoesNotReapBetweenMints_spec_27_6_201(t *testing.T) {
	store := NewMemorySessionStore()
	h, _, _ := newRevokeHandler(t, store)
	ctx := context.Background()
	now := h.now()

	// 10 minutes since the last mint: past the 5-minute idle grace but
	// inside the 20-minute reclamation window. Must survive.
	mustPut(t, store, "acme", "between", SessionRecord{
		TenantID: "acme", IssuedAt: now.Add(-30 * time.Minute),
		LastActivityAt: now.Add(-10 * time.Minute), BearerJTIs: []string{"jti-b"},
	})

	revoked, err := h.SweepIdleSessions(ctx)
	if err != nil {
		t.Fatalf("SweepIdleSessions: %v", err)
	}
	if revoked != 0 {
		t.Fatalf("revoked %d, want 0 (active-between-mints session must survive)", revoked)
	}
	if _, err := store.GetSession(ctx, "acme", "between"); err != nil {
		t.Error("an active-between-mints session was reaped")
	}
}

// TestSweepIdleSessionsNilStore: a handler with no session store sweeps
// nothing without error. F-27.3.7.
func TestSweepIdleSessionsNilStore(t *testing.T) {
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"}, Options{Signer: devSigner()})
	n, err := h.SweepIdleSessions(context.Background())
	if n != 0 || err != nil {
		t.Fatalf("SweepIdleSessions(nil store) = %d, %v; want 0, nil", n, err)
	}
}
