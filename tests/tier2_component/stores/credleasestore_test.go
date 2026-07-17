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
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore/pgstore"
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
// diagnosis: a failure means the credential lease body is persisted in
// cleartext rather than AES-256-GCM ciphertext, exposing the proxy-mode
// bearer token in a database dump (a T4-Restricted breach).
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
// diagnosis: a failure means emergency revocation cannot find every
// active lease backed by a revoked credential, so a compromised
// credential's leases would survive revocation.
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

// credleaseExpiring returns a valid pool-backed proxy lease whose expiry
// is expiresAt, so a test can place a lease on either side of a sweep
// cutoff.
func credleaseExpiring(leaseID, token string, expiresAt time.Time) credential.Lease {
	l := credleaseProxy(leaseID, token)
	l.IssuedAt = expiresAt.Add(-time.Hour).Truncate(time.Microsecond)
	l.ExpiresAt = expiresAt
	return l
}

// spec: §4.9 line 1671 — deny-list entries expire when the credential's
// natural lease TTL lapses, so the store projects the lease's ExpiresAt
// into the plain expires_at column (migration 0175) and the bounded
// sweep deletes rows past that expiry without decrypting the body.
// diagnosis: a failure means Put did not project ExpiresAt into the
// plain column, so the indexed expired-lease sweep cannot find rows past
// their TTL and the credential_leases table grows without bound.
func TestCredLeaseStoreExpiresAtProjectionAndSweep(t *testing.T) {
	t.Parallel()
	store, pg := newCredLeaseStore(t)
	ctx := context.Background()

	t.Run("put projects expires_at into the plain column", func(t *testing.T) {
		want := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t),
			time.Now().UTC().Add(90*time.Minute).Truncate(time.Microsecond))
		if err := store.Put(want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		var got *time.Time
		if err := pg.Pool.QueryRow(ctx,
			`SELECT expires_at FROM credential_leases WHERE lease_id = $1`, want.LeaseID).Scan(&got); err != nil {
			t.Fatalf("read expires_at: %v", err)
		}
		if got == nil {
			t.Fatal("expires_at column is NULL after Put; the projection did not run")
		}
		if !got.Equal(want.ExpiresAt) {
			t.Errorf("expires_at = %v, want %v", got, want.ExpiresAt)
		}
	})

	t.Run("delete expired removes rows past the cutoff and counts them", func(t *testing.T) {
		now := time.Now().UTC()
		past1 := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t), now.Add(-time.Hour).Truncate(time.Microsecond))
		past2 := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t), now.Add(-time.Minute).Truncate(time.Microsecond))
		live := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t), now.Add(time.Hour).Truncate(time.Microsecond))
		for _, l := range []credential.Lease{past1, past2, live} {
			if err := store.Put(l); err != nil {
				t.Fatalf("Put %s: %v", l.LeaseID, err)
			}
		}
		removed, err := store.DeleteExpired(ctx, now)
		if err != nil {
			t.Fatalf("DeleteExpired: %v", err)
		}
		if removed != 2 {
			t.Errorf("DeleteExpired removed %d rows, want 2", removed)
		}
		if _, ok := store.GetByID(past1.LeaseID); ok {
			t.Error("an expired lease survived DeleteExpired")
		}
		if _, ok := store.GetByID(live.LeaseID); !ok {
			t.Error("DeleteExpired dropped an unexpired lease")
		}
	})

	t.Run("delete expired never removes a NULL-expires_at row", func(t *testing.T) {
		now := time.Now().UTC()
		l := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t), now.Add(-time.Hour).Truncate(time.Microsecond))
		if err := store.Put(l); err != nil {
			t.Fatalf("Put: %v", err)
		}
		// Model a pre-migration row by clearing the projection column.
		if _, err := pg.Pool.Exec(ctx,
			`UPDATE credential_leases SET expires_at = NULL WHERE lease_id = $1`, l.LeaseID); err != nil {
			t.Fatalf("null out expires_at: %v", err)
		}
		if _, err := store.DeleteExpired(ctx, now); err != nil {
			t.Fatalf("DeleteExpired: %v", err)
		}
		if _, ok := store.GetByID(l.LeaseID); !ok {
			t.Error("DeleteExpired removed a NULL-expires_at row; a pre-backfill row must be left for the backfill")
		}
	})
}

// spec: §4.9 lines 1694-1695 — the startup rebuild and the deny-list
// sweep seed or drop a deny entry only after a fail-closed active-lease
// existence check, so the count query must distinguish a definitive zero
// from an unanswerable query and count an active or unknown-expiry row.
// diagnosis: a failure means the existence count conflates an active
// lease with an expired one (or a NULL-expiry row with an expired one),
// so the fail-closed deny-entry removal and rebuild filter would drop a
// deny entry that still shadows a live revoked lease.
func TestCredLeaseStoreByCredentialCount(t *testing.T) {
	t.Parallel()
	store, pg := newCredLeaseStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	pool := "pool-" + newUUID(t)[:8]
	credID := "cred-" + newUUID(t)[:8]
	key := credential.CredentialKey{Source: credential.SourcePool, PoolID: pool, CredentialID: credID}
	mk := func(expiresAt time.Time) credential.Lease {
		l := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t), expiresAt.Truncate(time.Microsecond))
		l.PoolID = pool
		l.CredentialID = credID
		return l
	}

	active1 := mk(now.Add(time.Hour))
	active2 := mk(now.Add(30 * time.Minute))
	expired := mk(now.Add(-time.Minute))
	for _, l := range []credential.Lease{active1, active2, expired} {
		if err := store.Put(l); err != nil {
			t.Fatalf("Put %s: %v", l.LeaseID, err)
		}
	}

	n, err := store.LeasesByCredentialCount(ctx, key, now)
	if err != nil {
		t.Fatalf("LeasesByCredentialCount: %v", err)
	}
	if n != 2 {
		t.Errorf("LeasesByCredentialCount = %d, want 2 (the expired lease must not count)", n)
	}

	// A pre-migration NULL-expires_at row counts as active so the guard
	// fails closed on a row the backfill has not yet reached.
	if _, err := pg.Pool.Exec(ctx,
		`UPDATE credential_leases SET expires_at = NULL WHERE lease_id = $1`, expired.LeaseID); err != nil {
		t.Fatalf("null out expires_at: %v", err)
	}
	n, err = store.LeasesByCredentialCount(ctx, key, now)
	if err != nil {
		t.Fatalf("LeasesByCredentialCount after NULL: %v", err)
	}
	if n != 3 {
		t.Errorf("LeasesByCredentialCount = %d, want 3 (a NULL-expiry row counts as active)", n)
	}

	// A credential with no lease reports a definitive zero with a nil
	// error, the answer the fail-closed callers remove a deny entry on.
	absent := credential.CredentialKey{Source: credential.SourcePool, PoolID: "absent-" + newUUID(t)[:8], CredentialID: "absent"}
	n, err = store.LeasesByCredentialCount(ctx, absent, now)
	if err != nil {
		t.Fatalf("LeasesByCredentialCount(absent): %v", err)
	}
	if n != 0 {
		t.Errorf("LeasesByCredentialCount(absent) = %d, want 0", n)
	}
}

// spec: §4.9 line 1671 — a row written before migration 0175 carries a
// NULL expires_at that the sweep cannot treat as expired; the one-time
// startup backfill decrypts each such row and either fills expires_at
// from the lease body or deletes the row when it is already past expiry.
// diagnosis: a failure means a pre-migration expired lease lingers
// indefinitely (never swept) or a pre-migration active lease is never
// projected, so its deny entry can never expire.
func TestCredLeaseStoreBackfillExpiresAt(t *testing.T) {
	t.Parallel()
	store, pg := newCredLeaseStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	live := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t), now.Add(time.Hour).Truncate(time.Microsecond))
	stale := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t), now.Add(-time.Hour).Truncate(time.Microsecond))
	for _, l := range []credential.Lease{live, stale} {
		if err := store.Put(l); err != nil {
			t.Fatalf("Put %s: %v", l.LeaseID, err)
		}
	}
	// Model both as pre-migration rows by clearing the projection column.
	if _, err := pg.Pool.Exec(ctx,
		`UPDATE credential_leases SET expires_at = NULL WHERE lease_id = ANY($1)`,
		[]string{live.LeaseID, stale.LeaseID}); err != nil {
		t.Fatalf("null out expires_at: %v", err)
	}

	filled, deleted, err := store.BackfillExpiresAt(ctx)
	if err != nil {
		t.Fatalf("BackfillExpiresAt: %v", err)
	}
	if filled < 1 || deleted < 1 {
		t.Errorf("BackfillExpiresAt filled=%d deleted=%d, want at least one of each", filled, deleted)
	}

	// The active lease's expires_at is now projected from its body.
	var got *time.Time
	if err := pg.Pool.QueryRow(ctx,
		`SELECT expires_at FROM credential_leases WHERE lease_id = $1`, live.LeaseID).Scan(&got); err != nil {
		t.Fatalf("read filled expires_at: %v", err)
	}
	if got == nil || !got.Equal(live.ExpiresAt) {
		t.Errorf("backfilled expires_at = %v, want %v", got, live.ExpiresAt)
	}
	// The already-expired pre-migration row was removed.
	if _, ok := store.GetByID(stale.LeaseID); ok {
		t.Error("BackfillExpiresAt left an already-expired pre-migration row in place")
	}
}
