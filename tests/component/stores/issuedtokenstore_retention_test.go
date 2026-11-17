//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// staticTenants lists a fixed tenant set for a revocation rehydration.
type staticTenants []string

func (s staticTenants) ListTenants(context.Context) ([]string, error) { return s, nil }

// spec: 13.3 (Credential Flow: Authoritative durability for revocation), 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence), 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
// diagnosis: user erasure or tenant deletion removed an issued_tokens row
// whose token can still be presented. DeleteByUser and DeleteByTenant must
// keep every row whose exp is inside the trailing hour, revoked or not, so a
// later Revoke of the unrevoked one finds its row and a rehydration still
// rejects the revoked one, and must delete only the rows past the hour. A
// failed Revoke means a row the token still needs was deleted.
func TestIssuedTokenErasureKeepsTheTrailingHour_spec_13_3(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := issuedtokenstore.New(pg.Pool)
	ctx := context.Background()

	erasers := map[string]func(tenant string) (int, error){
		"DeleteByUser":   func(tenant string) (int, error) { return store.DeleteByUser(ctx, tenant, "alice@acme.com") },
		"DeleteByTenant": func(tenant string) (int, error) { return store.DeleteByTenant(ctx, tenant) },
	}
	for name, erase := range erasers {
		t.Run(name, func(t *testing.T) {
			tenant := freshTenant(t, ctx, pg)
			now := time.Now().UTC().Truncate(time.Microsecond)
			mk := func(label string, exp time.Duration) issuedtokenstore.IssuedToken {
				tok := issuedtokenstore.IssuedToken{
					JTI: "jti-" + label + "-" + newUUID(t), TenantID: tenant, Subject: "alice@acme.com",
					TokenHash: []byte(label), Scope: []string{"sessions:read"}, Audience: "lenny-gateway",
					IssuedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(exp),
				}
				if err := store.Record(ctx, tok); err != nil {
					t.Fatalf("Record %s: %v", label, err)
				}
				return tok
			}
			live := mk("live", time.Hour)
			revoked := mk("revoked", -30*time.Minute)
			if err := store.Revoke(ctx, tenant, revoked.JTI, "test", now.Add(-40*time.Minute)); err != nil {
				t.Fatalf("Revoke inside the window: %v", err)
			}
			old := mk("old", -2*time.Hour)

			n, err := erase(tenant)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if n != 1 {
				t.Errorf("%s deleted %d rows, want 1 (the row past the trailing hour)", name, n)
			}
			if _, err := store.Get(ctx, tenant, old.JTI); !errors.Is(err, issuedtokenstore.ErrNotFound) {
				t.Errorf("row past the trailing hour survived: %v", err)
			}
			if err := store.Revoke(ctx, tenant, live.JTI, "erasure follow-up", now); err != nil {
				t.Errorf("Revoke of the kept unrevoked row failed: %v", err)
			}
			cache := revocation.NewCache()
			if err := cache.Rehydrate(ctx, staticTenants{tenant}, store); err != nil {
				t.Fatalf("Rehydrate: %v", err)
			}
			if !cache.IsRevoked(revoked.JTI) {
				t.Error("a rehydration after the erasure no longer rejects the kept revoked token")
			}
			if n, err := erase(tenant); err != nil || n != 0 {
				t.Errorf("second %s = (%d, %v), want (0, nil)", name, n, err)
			}
		})
	}
}
