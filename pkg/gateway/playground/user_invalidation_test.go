// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// spec: §27.3.1 line 148 / §27.6 line 204 — user.invalidated drives the
// playground revocation primitive for every session the user holds. The
// §11.4 user-invalidation fan-out (Handler.RevokeSessionsForUser) looks
// the user's sessions up through the user index and revokes each.
// F-27.6.4, F-27.3.2.

func idSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func mustPut(t *testing.T, store SessionStore, tenant, id string, rec SessionRecord) {
	t.Helper()
	if err := store.PutSession(context.Background(), tenant, id, rec, time.Hour); err != nil {
		t.Fatalf("PutSession(%s, %s): %v", tenant, id, err)
	}
}

// newRevokeHandler builds an oidc-mode Handler over store with an
// in-memory audit emitter and a minimal §27.8 metric set so the
// revocation reason is observable. The revocation path touches only the
// revocations counter, so the set is built directly here rather than via
// NewMetrics.
func newRevokeHandler(t *testing.T, store SessionStore) (*Handler, *MemoryAuditEmitter, *Metrics) {
	t.Helper()
	m := &Metrics{
		revocations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_playground_session_revocations_total",
			Help: "test",
		}, []string{"reason"}),
	}
	audit := NewMemoryAuditEmitter()
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, BearerTTL: 15 * time.Minute, OIDCSessionTTL: time.Hour},
		Options{Signer: devSigner(), Sessions: store, Metrics: m}).WithAuditEmitter(audit)
	return h, audit, m
}

func TestMemorySessionStoreSessionsForUser_spec_11_4(t *testing.T) {
	store := NewMemorySessionStore()
	ctx := context.Background()
	mustPut(t, store, "acme", "s1", SessionRecord{UserID: "alice@acme.com", TenantID: "acme", Origin: PlaygroundOrigin})
	mustPut(t, store, "acme", "s2", SessionRecord{UserID: "alice@acme.com", TenantID: "acme", Origin: PlaygroundOrigin})
	mustPut(t, store, "acme", "s3", SessionRecord{UserID: "bob@acme.com", TenantID: "acme", Origin: PlaygroundOrigin})
	// Same user id under a different tenant must never appear under acme.
	mustPut(t, store, "globex", "s4", SessionRecord{UserID: "alice@acme.com", TenantID: "globex", Origin: PlaygroundOrigin})

	ids, err := store.SessionsForUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("SessionsForUser: %v", err)
	}
	got := idSet(ids)
	if len(got) != 2 || !got["s1"] || !got["s2"] {
		t.Fatalf("SessionsForUser(acme, alice) = %v, want {s1,s2}", ids)
	}
	if got["s4"] {
		t.Fatal("SessionsForUser(acme, alice) leaked globex session s4 (cross-tenant)")
	}
	bob, _ := store.SessionsForUser(ctx, "acme", "bob@acme.com")
	if len(bob) != 1 || bob[0] != "s3" {
		t.Fatalf("SessionsForUser(acme, bob) = %v, want [s3]", bob)
	}
	none, _ := store.SessionsForUser(ctx, "acme", "nobody@acme.com")
	if len(none) != 0 {
		t.Fatalf("SessionsForUser(acme, nobody) = %v, want empty", none)
	}
}

func TestMemorySessionStoreSessionsForUserExcludesExpired_spec_11_4(t *testing.T) {
	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	clk := base
	store := NewMemorySessionStore()
	store.now = func() time.Time { return clk }
	ctx := context.Background()
	if err := store.PutSession(ctx, "acme", "s1",
		SessionRecord{UserID: "alice@acme.com", TenantID: "acme"}, 10*time.Minute); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	// Advance past the record TTL: an expired record is not a live session.
	clk = base.Add(11 * time.Minute)
	ids, _ := store.SessionsForUser(ctx, "acme", "alice@acme.com")
	if len(ids) != 0 {
		t.Fatalf("SessionsForUser returned an expired session: %v", ids)
	}
}

func TestRevokeSessionsForUserRevokesEveryUserSession_spec_27_6_204(t *testing.T) {
	store := NewMemorySessionStore()
	h, audit, m := newRevokeHandler(t, store)
	ctx := context.Background()
	mustPut(t, store, "acme", "s1", SessionRecord{UserID: "alice@acme.com", TenantID: "acme", BearerJTIs: []string{"jti-1"}})
	mustPut(t, store, "acme", "s2", SessionRecord{UserID: "alice@acme.com", TenantID: "acme", BearerJTIs: []string{"jti-2a", "jti-2b"}})
	mustPut(t, store, "acme", "s3", SessionRecord{UserID: "bob@acme.com", TenantID: "acme", BearerJTIs: []string{"jti-3"}})

	n, err := h.RevokeSessionsForUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("RevokeSessionsForUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("revoked %d sessions, want 2", n)
	}
	// alice's records are gone and a subsequent lookup finds nothing.
	if _, err := store.GetSession(ctx, "acme", "s1"); err == nil {
		t.Error("alice session s1 survived revocation")
	}
	if _, err := store.GetSession(ctx, "acme", "s2"); err == nil {
		t.Error("alice session s2 survived revocation")
	}
	// alice's bearers are on the deny list (in-flight WebSockets disconnect).
	for _, jti := range []string{"jti-1", "jti-2a", "jti-2b"} {
		if revoked, _ := store.IsBearerRevoked(ctx, "acme", jti); !revoked {
			t.Errorf("alice bearer %s not on the deny list after invalidation", jti)
		}
	}
	// bob is untouched: the fan-out is scoped to the named user.
	if _, err := store.GetSession(ctx, "acme", "s3"); err != nil {
		t.Error("bob session s3 was revoked by alice invalidation")
	}
	if revoked, _ := store.IsBearerRevoked(ctx, "acme", "jti-3"); revoked {
		t.Error("bob bearer jti-3 was revoked by alice invalidation")
	}
	// §27.8: the revocation metric attributes each session to user_invalidated.
	if got := testutil.ToFloat64(m.revocations.WithLabelValues(string(RevokeUserInvalidated))); got != 2 {
		t.Errorf("revocations{reason=user_invalidated} = %v, want 2", got)
	}
	// §27.3.1 step 6: a bearer_revoked audit event per revoked bearer.
	revokedEvents := 0
	for _, ev := range audit.Events() {
		if ev.Type == "playground.bearer_revoked" {
			revokedEvents++
		}
	}
	if revokedEvents != 3 {
		t.Errorf("emitted %d bearer_revoked audit events, want 3 (one per bearer)", revokedEvents)
	}
}

func TestRevokeSessionsForUserIdempotentAndNilStore_spec_27_6_204(t *testing.T) {
	store := NewMemorySessionStore()
	h, _, _ := newRevokeHandler(t, store)
	ctx := context.Background()
	// A user with no playground session: zero revoked, no error.
	if n, err := h.RevokeSessionsForUser(ctx, "acme", "ghost@acme.com"); n != 0 || err != nil {
		t.Fatalf("RevokeSessionsForUser(no sessions) = %d, %v; want 0, nil", n, err)
	}
	mustPut(t, store, "acme", "s1", SessionRecord{UserID: "alice@acme.com", TenantID: "acme", BearerJTIs: []string{"jti-1"}})
	if n, _ := h.RevokeSessionsForUser(ctx, "acme", "alice@acme.com"); n != 1 {
		t.Fatalf("first revoke = %d, want 1", n)
	}
	// The record is gone, so a repeat revocation is a no-op.
	if n, err := h.RevokeSessionsForUser(ctx, "acme", "alice@acme.com"); n != 0 || err != nil {
		t.Fatalf("idempotent revoke = %d, %v; want 0, nil", n, err)
	}
	// A handler with no session store (apiKey/dev mode) is a no-op.
	noStore := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{Signer: devSigner()})
	if n, err := noStore.RevokeSessionsForUser(ctx, "acme", "alice@acme.com"); n != 0 || err != nil {
		t.Fatalf("nil-store revoke = %d, %v; want 0, nil", n, err)
	}
}

func TestRedisSessionStoreUserIndexAndRevoke_spec_11_4(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisSessionStore(client)
	ctx := context.Background()

	mustPut(t, store, "acme", "s1", SessionRecord{UserID: "alice@acme.com", TenantID: "acme", BearerJTIs: []string{"jti-1"}})
	mustPut(t, store, "acme", "s2", SessionRecord{UserID: "alice@acme.com", TenantID: "acme", BearerJTIs: []string{"jti-2"}})
	mustPut(t, store, "globex", "s3", SessionRecord{UserID: "alice@acme.com", TenantID: "globex", BearerJTIs: []string{"jti-3"}})

	ids, err := store.SessionsForUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("SessionsForUser: %v", err)
	}
	if got := idSet(ids); len(got) != 2 || !got["s1"] || !got["s2"] {
		t.Fatalf("SessionsForUser(acme, alice) = %v, want {s1,s2}", ids)
	}
	// §12.4 tenant isolation: the globex index is separate.
	gx, _ := store.SessionsForUser(ctx, "globex", "alice@acme.com")
	if len(gx) != 1 || gx[0] != "s3" {
		t.Fatalf("SessionsForUser(globex, alice) = %v, want [s3]", gx)
	}

	h, _, _ := newRevokeHandler(t, store)
	n, err := h.RevokeSessionsForUser(ctx, "acme", "alice@acme.com")
	if err != nil || n != 2 {
		t.Fatalf("RevokeSessionsForUser(acme, alice) = %d, %v; want 2, nil", n, err)
	}
	for _, jti := range []string{"jti-1", "jti-2"} {
		if revoked, _ := store.IsBearerRevoked(ctx, "acme", jti); !revoked {
			t.Errorf("acme bearer %s not revoked", jti)
		}
	}
	// globex/alice is untouched by the acme invalidation.
	if revoked, _ := store.IsBearerRevoked(ctx, "globex", "jti-3"); revoked {
		t.Error("globex bearer jti-3 revoked by an acme invalidation")
	}
}
