// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local ordering coverage for the revocation-set swap.
//
// Cache.Rehydrate replaces the set with the rows its read returned plus the
// jtis Revoke added after the read began. A token.revoked event that lands
// while a read is in flight must therefore survive the swap even when the
// read's snapshot predates it, on a gateway replica and on lenny-ops alike.
//
// spec: §13.3 (Credential Flow: Authoritative durability for revocation),
// §25.4 (The lenny-ops Service: Authentication).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// racingRevokeSource serves fixed rows per tenant. Its read of the last
// tenant starts the Revoke goroutines and returns without waiting for
// them, so each Revoke races the swap that follows.
type racingRevokeSource struct {
	cache   *revocation.Cache
	tenants []string
	jtis    []string
	started sync.WaitGroup
}

func (s *racingRevokeSource) ListTenants(context.Context) ([]string, error) {
	return s.tenants, nil
}

func (s *racingRevokeSource) ListRevoked(_ context.Context, tenantID string) ([]issuedtokenstore.IssuedToken, error) {
	if tenantID == s.tenants[len(s.tenants)-1] {
		for _, jti := range s.jtis {
			s.started.Add(1)
			go func(jti string) {
				defer s.started.Done()
				s.cache.Revoke(jti)
			}(jti)
		}
	}
	return []issuedtokenstore.IssuedToken{{TenantID: tenantID, JTI: "row-" + tenantID}}, nil
}

// spec: 13.3 (Credential Flow), 25.4 (The lenny-ops Service: Authentication)
// diagnosis: a failure means a jti revoked while a rule-1 read was in flight
// was dropped by the swap at the end of that read, so a token.revoked event
// that raced a 30 s read is not enforced until the next read returns the row,
// or never if the row is not yet committed. Check that Cache.Revoke records a
// jti for every read in flight and that Rehydrate unions those jtis into the
// set it swaps in.
func TestRevokeRacingRehydrateIsNeverDropped(t *testing.T) {
	const runs = 200
	tenants := []string{"default", "acme", "globex", "initech"}
	for run := 0; run < runs; run++ {
		cache := revocation.NewCache()
		cache.Revoke("before-read-not-indexed")
		src := &racingRevokeSource{cache: cache, tenants: tenants}
		for i := 0; i < 16; i++ {
			src.jtis = append(src.jtis, fmt.Sprintf("run-%d-racing-%d", run, i))
		}
		if err := cache.Rehydrate(context.Background(), src, src); err != nil {
			t.Fatalf("run %d: Rehydrate: %v", run, err)
		}
		src.started.Wait()
		for _, jti := range src.jtis {
			if !cache.IsRevoked(jti) {
				t.Fatalf("run %d: %s, revoked after Rehydrate began, is missing from the set", run, jti)
			}
		}
		for _, tenant := range tenants {
			if !cache.IsRevoked("row-" + tenant) {
				t.Fatalf("run %d: row of tenant %s is missing", run, tenant)
			}
		}
		if cache.IsRevoked("before-read-not-indexed") {
			t.Fatalf("run %d: a jti revoked before the read that the index does not return survived", run)
		}
	}
}
