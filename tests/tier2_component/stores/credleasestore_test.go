//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.9 gateway-replica credential-lease store,
// exercising the Postgres-backed pkg/gateway/credleasestore/pgstore
// against a real container with the production migrations applied.
// Covers the Put / GetByToken / GetByID round-trip, the Put
// upsert-or-insert semantics, the rotated-token replacement, Remove
// idempotency, Len counting, the lookup-miss behaviour, the §4.9
// LeasesByCredential revocation lookup, and the §12.9 T4 envelope
// encryption of the lease body and bearer token at rest.
package stores_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credleasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// newCredLeaseStore builds the Postgres-backed credential-lease store
// over a fresh Postgres container and a local KMS KEK provider, the
// §12.9 envelope-encryption posture the store requires. Returns the
// store and the raw Postgres handle so a test can inspect the stored
// lease column directly.
func newCredLeaseStore(t *testing.T) (*credleasepg.Store, *containers.Postgres) {
	t.Helper()
	_, pg := startStore(t)
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("kms provider: %v", err)
	}
	store, err := credleasepg.New(pg.Pool, provider)
	if err != nil {
		t.Fatalf("credleasepg.New: %v", err)
	}
	return store, pg
}

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
		IssuedAt:     time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond),
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
		IssuedAt:      time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond),
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
	store, _ := newCredLeaseStore(t)

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

// spec: §12.9 line 1048 — a credential lease is T4 — Restricted. The
// persisted lease body carries the §4.9 proxy-mode bearer token, the
// capability a runtime presents to the LLM reverse proxy; a database
// dump must not expose it. This reads the raw stored bytes and asserts
// the lease column holds AES-256-GCM envelope ciphertext and that the
// bearer token is never persisted in cleartext (the token-hash column
// is the SHA-256 digest, not the token).
func TestCredLeaseCiphertextAtRest(t *testing.T) {
	t.Parallel()
	store, pg := newCredLeaseStore(t)
	ctx := context.Background()

	const token = "lt-PLAINTEXT-bearer-capability-DO-NOT-LEAK"
	lease := credleaseProxy("cl-"+newUUID(t), token)
	if err := store.Put(lease); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Read the raw columns straight out of Postgres, bypassing decrypt.
	// credential_leases is platform-global, so the read needs no
	// app.current_tenant.
	var (
		blob         []byte
		keyVersion   int
		tokenHashCol *string
	)
	if err := pg.Pool.QueryRow(ctx,
		`SELECT lease, lease_key_version, lease_token_hash FROM credential_leases WHERE lease_id = $1`,
		lease.LeaseID).Scan(&blob, &keyVersion, &tokenHashCol); err != nil {
		t.Fatalf("read lease columns: %v", err)
	}

	// The stored body must not contain the bearer token anywhere.
	if bytes.Contains(blob, []byte(token)) {
		t.Fatalf("credential_leases.lease column contains the plaintext bearer token: % x", blob)
	}
	// The token-hash column is the SHA-256 hex of the token, never the
	// token itself.
	sum := sha256.Sum256([]byte(token))
	wantHash := hex.EncodeToString(sum[:])
	if tokenHashCol == nil || *tokenHashCol != wantHash {
		t.Errorf("lease_token_hash = %v, want %q", tokenHashCol, wantHash)
	}
	if tokenHashCol != nil && *tokenHashCol == token {
		t.Error("lease_token_hash stores the plaintext bearer token")
	}
	// The §4.9.1 key_version column is populated and matches the blob.
	if keyVersion < 1 {
		t.Errorf("lease_key_version: got %d, want >= 1", keyVersion)
	}
	sealed, err := envelope.Decode(blob)
	if err != nil {
		t.Fatalf("stored lease is not a valid envelope blob: %v", err)
	}
	if sealed.KEKVersion != keyVersion {
		t.Errorf("envelope blob KEK version %d != lease_key_version %d", sealed.KEKVersion, keyVersion)
	}
	if len(sealed.WrappedDEK) == 0 {
		t.Error("stored envelope blob has no wrapped DEK")
	}

	// The store still decrypts the body on read and resolves by token.
	got, ok := store.GetByToken(token)
	if !ok {
		t.Fatal("GetByToken did not resolve the stored token")
	}
	if got.Proxy == nil || got.Proxy.LeaseToken != token {
		t.Errorf("decrypted lease lost its proxy token: %+v", got.Proxy)
	}

	// A direct-mode lease carries a NULL token hash.
	direct := credleaseDirect("cl-" + newUUID(t))
	if err := store.Put(direct); err != nil {
		t.Fatalf("Put direct: %v", err)
	}
	var directHash *string
	if err := pg.Pool.QueryRow(ctx,
		`SELECT lease_token_hash FROM credential_leases WHERE lease_id = $1`,
		direct.LeaseID).Scan(&directHash); err != nil {
		t.Fatalf("read direct hash: %v", err)
	}
	if directHash != nil {
		t.Errorf("direct-mode lease stored a non-NULL token hash: %q", *directHash)
	}
}

// spec: §4.9 lines 1640-1652 — emergency credential revocation looks up
// every active lease backed by the revoked credential. With the lease
// body encrypted (§12.9), the lookup matches the dedicated source-aware
// credential-key columns rather than the JSONB body.
func TestCredLeaseStoreByCredential(t *testing.T) {
	t.Parallel()
	store, _ := newCredLeaseStore(t)

	// credleaseProxy stamps PoolID=claude-prod, CredentialID=key-1. Two
	// leases share that pool credential; a third names a different one.
	a := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
	b := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
	other := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
	other.CredentialID = "key-2"
	for _, l := range []credential.Lease{a, b, other} {
		if err := store.Put(l); err != nil {
			t.Fatalf("Put %s: %v", l.LeaseID, err)
		}
	}
	poolKey := credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: "key-1"}
	ids := map[string]bool{}
	for _, l := range store.LeasesByCredential(poolKey) {
		ids[l.LeaseID] = true
	}
	if !ids[a.LeaseID] || !ids[b.LeaseID] {
		t.Errorf("LeasesByCredential missed a lease: got %v", ids)
	}
	if ids[other.LeaseID] {
		t.Error("LeasesByCredential returned a lease for a different credential")
	}

	// credleaseDirect stamps Source=user, TenantID=acme, CredentialRef=cred-1.
	user := credleaseDirect("cl-" + newUUID(t))
	if err := store.Put(user); err != nil {
		t.Fatalf("Put user lease: %v", err)
	}
	userKey := credential.CredentialKey{Source: credential.SourceUser, TenantID: "acme", CredentialRef: "cred-1"}
	gotUser := store.LeasesByCredential(userKey)
	if len(gotUser) != 1 || gotUser[0].LeaseID != user.LeaseID {
		t.Errorf("LeasesByCredential(user) = %+v, want the single user lease %s", gotUser, user.LeaseID)
	}

	// An unknown credential resolves nothing.
	if n := len(store.LeasesByCredential(credential.CredentialKey{Source: credential.SourcePool, PoolID: "absent", CredentialID: "absent"})); n != 0 {
		t.Errorf("LeasesByCredential for an unknown credential returned %d, want 0", n)
	}
}
