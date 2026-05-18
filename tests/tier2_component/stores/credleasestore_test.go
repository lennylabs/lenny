//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.9 gateway-replica credential-lease store,
// exercising the Postgres-backed pkg/gateway/credleasestore/pgstore
// against a real container with the production migrations applied.
// Covers the Put / GetByToken / GetByID round-trip, the Put
// upsert-or-insert semantics, the rotated-token replacement, Remove
// idempotency, Len counting, and the lookup-miss behaviour.
package stores_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credleasestore/pgstore"
)

// credleaseProxy returns a valid pool-backed proxy lease with the given
// id and lease token. A pool-backed lease carries no tenant_id, which
// is why the credential_leases table is platform-global.
func credleaseProxy(leaseID, token string) credential.Lease {
	return credential.Lease{
		LeaseID:      leaseID,
		SessionID:    "s_" + leaseID,
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryProxy,
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   token,
		},
	}
}

// credleaseDirect returns a valid user-backed direct-mode lease with
// the given id. A direct-mode lease carries no lease token.
func credleaseDirect(leaseID string) credential.Lease {
	return credential.Lease{
		LeaseID:       leaseID,
		SessionID:     "s_" + leaseID,
		Provider:      credential.ProviderAnthropicDirect,
		Source:        credential.SourceUser,
		TenantID:      "acme",
		CredentialRef: "cred-1",
		DeliveryMode:  credential.DeliveryDirect,
		ExpiresAt:     time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	}
}

// spec: 4.9
// diagnosis: the Postgres-backed credential-lease store in
// pkg/gateway/credleasestore/pgstore did not behave as specified. Put
// must validate and persist a lease, GetByToken and GetByID must
// round-trip it, Put must upsert on a repeated lease id and drop a
// rotated-away token, Remove must be idempotent, Len must count the
// stored leases, and a token or id miss must report ok = false.
func TestCredLeaseStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := credleasepg.New(pg.Pool)

	t.Run("put then get by token and id round-trip", func(t *testing.T) {
		want := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		if err := store.Put(want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		byToken, ok := store.GetByToken(want.Proxy.LeaseToken)
		if !ok {
			t.Fatal("GetByToken did not resolve the stored lease token")
		}
		if byToken.LeaseID != want.LeaseID || byToken.SessionID != want.SessionID ||
			byToken.Provider != want.Provider || byToken.Source != want.Source ||
			byToken.PoolID != want.PoolID || byToken.CredentialID != want.CredentialID ||
			byToken.DeliveryMode != want.DeliveryMode {
			t.Errorf("GetByToken scalar mismatch:\n got %+v\nwant %+v", byToken, want)
		}
		if !byToken.ExpiresAt.Equal(want.ExpiresAt) {
			t.Errorf("ExpiresAt mismatch: got %v want %v", byToken.ExpiresAt, want.ExpiresAt)
		}
		if byToken.Proxy == nil || byToken.Proxy.LeaseToken != want.Proxy.LeaseToken ||
			byToken.Proxy.ProxyURL != want.Proxy.ProxyURL ||
			byToken.Proxy.ProxyDialect != want.Proxy.ProxyDialect {
			t.Errorf("Proxy config lost in round-trip: got %+v want %+v", byToken.Proxy, want.Proxy)
		}
		byID, ok := store.GetByID(want.LeaseID)
		if !ok {
			t.Fatal("GetByID did not resolve the stored lease")
		}
		if byID.LeaseID != want.LeaseID || byID.Proxy == nil ||
			byID.Proxy.LeaseToken != want.Proxy.LeaseToken {
			t.Errorf("GetByID mismatch: got %+v want %+v", byID, want)
		}
	})

	t.Run("put rejects an invalid lease and stores nothing", func(t *testing.T) {
		// A proxy lease with no materializedConfig fails Lease.Validate.
		bad := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		bad.Proxy = nil
		if err := store.Put(bad); err == nil {
			t.Error("Put accepted an invalid lease")
		}
		if _, ok := store.GetByID(bad.LeaseID); ok {
			t.Error("GetByID resolved a lease that failed validation")
		}
	})

	t.Run("direct-mode lease has no token index", func(t *testing.T) {
		direct := credleaseDirect("cl-" + newUUID(t))
		if err := store.Put(direct); err != nil {
			t.Fatalf("Put direct lease: %v", err)
		}
		if _, ok := store.GetByID(direct.LeaseID); !ok {
			t.Error("GetByID did not resolve the direct lease")
		}
		if _, ok := store.GetByToken(""); ok {
			t.Error("a direct-mode lease was resolvable by an empty token")
		}
	})

	t.Run("put upserts on a repeated lease id and drops the rotated token", func(t *testing.T) {
		id := "cl-" + newUUID(t)
		oldToken := "lt-" + newUUID(t)
		newToken := "lt-" + newUUID(t)
		if err := store.Put(credleaseProxy(id, oldToken)); err != nil {
			t.Fatalf("first Put: %v", err)
		}
		before := store.Len()
		if err := store.Put(credleaseProxy(id, newToken)); err != nil {
			t.Fatalf("re-Put: %v", err)
		}
		if got := store.Len(); got != before {
			t.Errorf("Len changed across an upsert: before=%d after=%d", before, got)
		}
		if _, ok := store.GetByToken(oldToken); ok {
			t.Error("the rotated-away lease token still resolves")
		}
		got, ok := store.GetByToken(newToken)
		if !ok || got.LeaseID != id {
			t.Errorf("GetByToken(newToken) = %+v ok=%v, want lease %s", got, ok, id)
		}
	})

	t.Run("remove drops the lease and is idempotent", func(t *testing.T) {
		lease := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		if err := store.Put(lease); err != nil {
			t.Fatalf("Put: %v", err)
		}
		store.Remove(lease.LeaseID)
		if _, ok := store.GetByID(lease.LeaseID); ok {
			t.Error("GetByID resolved a removed lease")
		}
		if _, ok := store.GetByToken(lease.Proxy.LeaseToken); ok {
			t.Error("GetByToken resolved a removed lease's token")
		}
		// Removing an absent lease is a no-op success.
		store.Remove(lease.LeaseID)
		store.Remove("cl-absent-" + newUUID(t))
	})

	t.Run("len counts the stored leases", func(t *testing.T) {
		before := store.Len()
		ids := []string{"cl-" + newUUID(t), "cl-" + newUUID(t), "cl-" + newUUID(t)}
		for _, id := range ids {
			if err := store.Put(credleaseProxy(id, "lt-"+newUUID(t))); err != nil {
				t.Fatalf("Put %s: %v", id, err)
			}
		}
		if got := store.Len(); got != before+len(ids) {
			t.Errorf("Len after %d Puts = %d, want %d", len(ids), got, before+len(ids))
		}
		store.Remove(ids[0])
		if got := store.Len(); got != before+len(ids)-1 {
			t.Errorf("Len after a Remove = %d, want %d", got, before+len(ids)-1)
		}
	})

	t.Run("get miss reports ok false", func(t *testing.T) {
		if _, ok := store.GetByToken("lt-unknown-" + newUUID(t)); ok {
			t.Error("GetByToken resolved an unknown token")
		}
		if _, ok := store.GetByID("cl-unknown-" + newUUID(t)); ok {
			t.Error("GetByID resolved an unknown lease id")
		}
	})

	t.Run("leases by session returns every lease for the given sessions", func(t *testing.T) {
		// credleaseProxy stamps SessionID as "s_" + leaseID.
		a := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		b := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		other := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		for _, l := range []credential.Lease{a, b, other} {
			if err := store.Put(l); err != nil {
				t.Fatalf("Put %s: %v", l.LeaseID, err)
			}
		}
		got := store.LeasesBySession([]string{a.SessionID, b.SessionID})
		ids := map[string]bool{}
		for _, l := range got {
			ids[l.LeaseID] = true
		}
		if !ids[a.LeaseID] || !ids[b.LeaseID] {
			t.Errorf("LeasesBySession missed a requested lease: got %v", ids)
		}
		if ids[other.LeaseID] {
			t.Error("LeasesBySession returned a lease for an unrequested session")
		}
		if n := len(store.LeasesBySession(nil)); n != 0 {
			t.Errorf("LeasesBySession(nil) returned %d leases, want 0", n)
		}
		if n := len(store.LeasesBySession([]string{"s_absent-" + newUUID(t)})); n != 0 {
			t.Errorf("LeasesBySession for an unknown session returned %d, want 0", n)
		}
	})
}
