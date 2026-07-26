// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// spec: §27.3.1 (session record backing store) — the
// `t:{tenant_id}:pg:revoked:{jti}` minted-bearer revocation marker holds
// an "Empty value (presence-only)". Both revocation write paths
// (RevokeSession on logout or admin revocation, and MarkBearerRevoked
// for an individual bearer) must therefore store an empty string, so the
// on-the-wire Redis record matches what an operator or a second
// implementation reads it against. The presence semantics are asserted
// alongside so a marker that is empty because it was never written
// cannot pass.
func TestPlaygroundRevocationMarkerIsPresenceOnly_spec_27_3_1(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	store := NewRedisSessionStore(client)
	ctx := context.Background()
	const tenant = "acme"

	assertPresenceOnly := func(t *testing.T, jti string) {
		t.Helper()
		key := revokedKey(tenant, jti)
		if !mr.Exists(key) {
			t.Fatalf("revocation marker %s absent; want present", key)
		}
		got, err := client.Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("GET %s: %v", key, err)
		}
		if got != "" {
			t.Fatalf("revocation marker %s = %q, want empty value (presence-only)", key, got)
		}
	}

	t.Run("RevokeSession", func(t *testing.T) {
		const (
			sessionID = "sess-revoked"
			jtiFirst  = "jti-current"
			jtiPrior  = "jti-refreshed"
		)
		rec := SessionRecord{
			UserID:     "alice",
			TenantID:   tenant,
			Origin:     PlaygroundOrigin,
			BearerJTIs: []string{jtiPrior, jtiFirst},
		}
		if err := store.PutSession(ctx, tenant, sessionID, rec, time.Hour); err != nil {
			t.Fatalf("PutSession: %v", err)
		}
		// The spec requires a marker for every jti the session held
		// within its lifetime, including the ones silent-refresh replaced.
		if err := store.RevokeSession(ctx, tenant, sessionID, []string{jtiPrior, jtiFirst}, time.Hour); err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
		assertPresenceOnly(t, jtiPrior)
		assertPresenceOnly(t, jtiFirst)
	})

	t.Run("MarkBearerRevoked", func(t *testing.T) {
		const jti = "jti-single"
		if err := store.MarkBearerRevoked(ctx, tenant, jti, time.Hour); err != nil {
			t.Fatalf("MarkBearerRevoked: %v", err)
		}
		assertPresenceOnly(t, jti)
	})

	// The presence-only value must not weaken the read path: a marker
	// holding an empty string still revokes the bearer.
	if revoked, err := store.IsBearerRevoked(ctx, tenant, "jti-single"); err != nil || !revoked {
		t.Fatalf("IsBearerRevoked(jti-single) = %v, %v; want true, nil", revoked, err)
	}
}
