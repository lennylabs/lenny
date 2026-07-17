//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.9 / §15.1 end-user credential registry,
// exercising the Postgres-backed pkg/gateway/credentialstore/pgstore
// against a real container with the production migrations applied.
// Covers the register/get round-trip, re-registration replacing the
// secret in place, the sentinel error, cross-tenant isolation, the
// Rotate/Revoke/Delete lifecycle, the user-scoped ref-ordered List,
// and the §4 / §12.9 envelope encryption of the secret column at rest.
package stores_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	credentialpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore/pgstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	stubkms "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// newCredentialStore builds the Postgres-backed credential store over a
// fresh Postgres container and a local KMS KEK provider. Returns the
// store, the KMS provider, and the raw Postgres handle so a test can
// inspect the stored secret column directly.
func newCredentialStore(t *testing.T) (*credentialpg.Store, kms.Provider, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("kms provider: %v", err)
	}
	store, err := credentialpg.New(pg.Pool, provider)
	if err != nil {
		t.Fatalf("credentialpg.New: %v", err)
	}
	return store, provider, pg
}

// spec: 4.9
// diagnosis: the Postgres-backed end-user credential registry in
// pkg/gateway/credentialstore/pgstore did not behave as specified —
// the Register/Get/Rotate/Revoke/Delete/List lifecycle, the secret
// round-trip, provider validation, the (tenant, user, provider)
// re-register reuse, or cross-tenant ErrNotFound isolation.
func TestCredentialStoreContract(t *testing.T) {
	t.Parallel()
	store, _, pg := newCredentialStore(t)
	ctx := context.Background()

	t.Run("register and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderAnthropicDirect, "", "sk-secret")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if c.Ref == "" || c.Status != credentialstore.StatusActive {
			t.Errorf("registered credential malformed: %+v", c)
		}
		if c.CreatedAt.IsZero() {
			t.Error("Register must stamp CreatedAt")
		}
		if !c.RotatedAt.IsZero() || !c.RevokedAt.IsZero() {
			t.Errorf("fresh credential must have zero RotatedAt/RevokedAt: %+v", c)
		}
		got, err := store.Get(ctx, tenant, c.Ref)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.TenantID != tenant || got.UserID != "alice" ||
			got.Provider != credential.ProviderAnthropicDirect ||
			got.Secret != "sk-secret" || got.Status != credentialstore.StatusActive {
			t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, c)
		}
		if !got.CreatedAt.Equal(c.CreatedAt) {
			t.Errorf("CreatedAt mismatch: got %v want %v", got.CreatedAt, c.CreatedAt)
		}
	})

	t.Run("register rejects an unknown provider", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Register(ctx, tenant, "alice", credential.Provider("made-up"), "", "x"); err == nil {
			t.Error("Register with an unknown provider should be rejected")
		}
	})

	t.Run("re-register reuses the ref and replaces the secret", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c1, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "secret-v1")
		if err != nil {
			t.Fatalf("first Register: %v", err)
		}
		c2, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "secret-v2")
		if err != nil {
			t.Fatalf("re-Register: %v", err)
		}
		// Same triple → same ref, refreshed secret + RotatedAt.
		if c1.Ref != c2.Ref {
			t.Errorf("re-register should reuse ref: %q vs %q", c1.Ref, c2.Ref)
		}
		if c2.RotatedAt.IsZero() {
			t.Error("re-register must refresh RotatedAt")
		}
		got, err := store.Get(ctx, tenant, c1.Ref)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Secret != "secret-v2" {
			t.Errorf("secret not replaced: got %q, want secret-v2", got.Secret)
		}
	})

	t.Run("re-register reactivates a revoked credential", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "x")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if _, err := store.Revoke(ctx, tenant, c.Ref); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		again, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "y")
		if err != nil {
			t.Fatalf("re-Register: %v", err)
		}
		if again.Ref != c.Ref {
			t.Errorf("re-register should reuse ref: %q vs %q", c.Ref, again.Ref)
		}
		if again.Status != credentialstore.StatusActive || !again.RevokedAt.IsZero() {
			t.Errorf("re-register must clear the revoked state: %+v", again)
		}
	})

	t.Run("get missing and cross-tenant return ErrNotFound", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, owner, "cred_missing"); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		c, err := store.Register(ctx, owner, "alice", credential.ProviderGitHub, "", "x")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if _, err := store.Get(ctx, intruder, c.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
		// The same triple under another tenant is independent.
		if _, err := store.Register(ctx, intruder, "alice", credential.ProviderGitHub, "", "y"); err != nil {
			t.Errorf("same triple, different tenant: got %v, want nil", err)
		}
	})

	t.Run("rotate replaces the secret and refreshes RotatedAt", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "old")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		rotated, err := store.Rotate(ctx, tenant, c.Ref, "new")
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if rotated.Secret != "new" || rotated.RotatedAt.IsZero() {
			t.Errorf("rotate did not apply: %+v", rotated)
		}
		if rotated.Status != credentialstore.StatusActive {
			t.Errorf("rotate must leave the credential active: %+v", rotated)
		}
		persisted, _ := store.Get(ctx, tenant, c.Ref)
		if persisted.Secret != "new" {
			t.Errorf("rotate not persisted: got %q, want new", persisted.Secret)
		}
		if _, err := store.Rotate(ctx, tenant, "cred_missing", "x"); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Rotate missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("revoke marks the credential revoked", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "x")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		revoked, err := store.Revoke(ctx, tenant, c.Ref)
		if err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if revoked.Status != credentialstore.StatusRevoked || revoked.RevokedAt.IsZero() {
			t.Errorf("revoke not applied: %+v", revoked)
		}
		persisted, _ := store.Get(ctx, tenant, c.Ref)
		if persisted.Status != credentialstore.StatusRevoked || persisted.RevokedAt.IsZero() {
			t.Errorf("revoke not persisted: %+v", persisted)
		}
		if _, err := store.Revoke(ctx, tenant, "cred_missing"); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Revoke missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("delete removes the row and frees the triple", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "x")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := store.Delete(ctx, tenant, c.Ref); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, tenant, c.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, tenant, c.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
		// After delete the triple is free: re-register mints a fresh ref.
		c2, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "y")
		if err != nil {
			t.Fatalf("re-Register after delete: %v", err)
		}
		if c2.Ref == c.Ref {
			t.Error("re-register after delete should mint a fresh ref")
		}
	})

	t.Run("list is user-scoped and ref-ordered", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "x"); err != nil {
			t.Fatalf("Register alice/github: %v", err)
		}
		if _, err := store.Register(ctx, tenant, "alice", credential.ProviderAWSBedrock, "", "y"); err != nil {
			t.Fatalf("Register alice/bedrock: %v", err)
		}
		if _, err := store.Register(ctx, tenant, "bob", credential.ProviderGitHub, "", "z"); err != nil {
			t.Fatalf("Register bob/github: %v", err)
		}
		aliceCreds, err := store.List(ctx, tenant, "alice")
		if err != nil {
			t.Fatalf("List alice: %v", err)
		}
		if len(aliceCreds) != 2 {
			t.Fatalf("alice should have 2 credentials: got %d", len(aliceCreds))
		}
		if aliceCreds[0].Ref >= aliceCreds[1].Ref {
			t.Errorf("List must be ref-ascending: got %q, %q", aliceCreds[0].Ref, aliceCreds[1].Ref)
		}
		bobCreds, err := store.List(ctx, tenant, "bob")
		if err != nil {
			t.Fatalf("List bob: %v", err)
		}
		if len(bobCreds) != 1 {
			t.Errorf("bob should have 1 credential: got %d", len(bobCreds))
		}
		// A user with no credentials yields an empty, non-nil slice.
		empty, err := store.List(ctx, tenant, "carol")
		if err != nil {
			t.Fatalf("List carol: %v", err)
		}
		if empty == nil || len(empty) != 0 {
			t.Errorf("List for a credential-less user: got %v, want empty slice", empty)
		}
	})

	t.Run("list is tenant-scoped", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Register(ctx, owner, "alice", credential.ProviderGitHub, "", "x"); err != nil {
			t.Fatalf("Register: %v", err)
		}
		isolated, err := store.List(ctx, intruder, "alice")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(isolated) != 0 {
			t.Errorf("List must not cross tenants: got %d rows", len(isolated))
		}
	})
}

// spec: 4.9
// diagnosis: the Postgres-backed §4.9 startup deny-list rebuild has no
// user-credential term because credentialstore/pgstore exposes no
// cross-tenant revoked-credential listing query. RevokedCredentials must
// scan every tenant, return only revoked rows in (tenant_id, ref) order,
// and return an empty slice with no error when nothing is revoked, so a
// restarted replica can re-deny a revoked user credential it missed on
// the original pub/sub notification.
func TestCredentialStoreRevokedCredentials(t *testing.T) {
	t.Parallel()
	store, _, pg := newCredentialStore(t)
	ctx := context.Background()

	t.Run("empty when nothing is revoked", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", "x"); err != nil {
			t.Fatalf("Register: %v", err)
		}
		got, err := store.RevokedCredentials(ctx)
		if err != nil {
			t.Fatalf("RevokedCredentials: %v", err)
		}
		for _, rc := range got {
			if rc.TenantID == tenant {
				t.Errorf("active credential leaked into RevokedCredentials: %+v", rc)
			}
		}
	})

	t.Run("cross-tenant scan returns only revoked rows", func(t *testing.T) {
		acme := freshTenant(t, ctx, pg)
		globex := freshTenant(t, ctx, pg)

		acmeRevoked, err := store.Register(ctx, acme, "alice", credential.ProviderAnthropicDirect, "", "sk-a")
		if err != nil {
			t.Fatalf("Register acme/alice: %v", err)
		}
		// An active acme credential must be excluded.
		if _, err := store.Register(ctx, acme, "bob", credential.ProviderGitHub, "", "sk-b"); err != nil {
			t.Fatalf("Register acme/bob: %v", err)
		}
		globexRevoked, err := store.Register(ctx, globex, "carol", credential.ProviderVertexAI, "", "sk-c")
		if err != nil {
			t.Fatalf("Register globex/carol: %v", err)
		}
		if _, err := store.Revoke(ctx, acme, acmeRevoked.Ref); err != nil {
			t.Fatalf("Revoke acme: %v", err)
		}
		if _, err := store.Revoke(ctx, globex, globexRevoked.Ref); err != nil {
			t.Fatalf("Revoke globex: %v", err)
		}

		got, err := store.RevokedCredentials(ctx)
		if err != nil {
			t.Fatalf("RevokedCredentials: %v", err)
		}

		// Filter to the two tenants this subtest owns; the shared store
		// may carry revoked rows from parallel subtests.
		var mine []credentialstore.RevokedUserCredential
		for _, rc := range got {
			if rc.TenantID == acme || rc.TenantID == globex {
				mine = append(mine, rc)
			}
		}
		want := []credentialstore.RevokedUserCredential{
			{TenantID: acme, CredentialRef: acmeRevoked.Ref},
			{TenantID: globex, CredentialRef: globexRevoked.Ref},
		}
		if acme > globex {
			want[0], want[1] = want[1], want[0]
		}
		if len(mine) != len(want) {
			t.Fatalf("RevokedCredentials returned %d rows for this test's tenants, want %d: %+v", len(mine), len(want), mine)
		}
		for i := range want {
			if mine[i] != want[i] {
				t.Errorf("entry %d = %+v, want %+v", i, mine[i], want[i])
			}
		}
	})

	t.Run("a store error surfaces rather than an empty rebuild", func(t *testing.T) {
		// The §4.9 startup rebuild is fail-closed: it aborts the whole
		// rebuild on a listing-query error rather than committing an empty
		// deny list. A cancelled context makes the cross-tenant scan fail
		// before it runs, so the query must return an error.
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := store.RevokedCredentials(canceled); err == nil {
			t.Error("RevokedCredentials must return an error when the cross-tenant scan cannot run (fail closed)")
		}
	})
}

// readSecretColumn reads the raw secret and secret_key_version columns
// for a credential straight out of Postgres, bypassing the store's
// decrypt path. The credentials table is tenant-scoped, so the read
// runs inside a transaction that sets app.current_tenant for the RLS
// policy.
func readSecretColumn(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, ref string) ([]byte, int) {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var blob []byte
	var keyVersion int
	if err := tx.QueryRow(ctx,
		`SELECT secret, secret_key_version FROM credentials WHERE tenant_id = $1 AND ref = $2`,
		tenant, ref).Scan(&blob, &keyVersion); err != nil {
		t.Fatalf("read secret column: %v", err)
	}
	return blob, keyVersion
}

// spec: 4, 12.9
// diagnosis: the §12.9 T4 credential secret is not envelope-encrypted
// at rest. The credentials.secret column must hold AES-256-GCM
// envelope ciphertext, not the plaintext API key — a database dump
// must not expose the upstream credential. This mirrors the row-level
// intent of tests/tier5_e2e_kind/etcd_encryption_test.go, which reads
// the raw stored bytes and asserts they are ciphertext, here applied
// to the Postgres secret column rather than an etcd value.
func TestCredentialSecretCiphertextAtRest(t *testing.T) {
	t.Parallel()
	store, _, pg := newCredentialStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	const plaintext = "sk-ant-PLAINTEXT-upstream-api-key-DO-NOT-LEAK"
	c, err := store.Register(ctx, tenant, "alice", credential.ProviderAnthropicDirect, "", plaintext)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	blob, keyVersion := readSecretColumn(t, ctx, pg, tenant, c.Ref)

	// The stored bytes must not contain the plaintext secret anywhere.
	if bytes.Contains(blob, []byte(plaintext)) {
		t.Fatalf("credentials.secret column contains the plaintext API key: % x", blob)
	}
	// The stored bytes must not be the plaintext UTF-8 either.
	if string(blob) == plaintext {
		t.Fatal("credentials.secret column stores the plaintext API key verbatim")
	}
	// The §4.9.1 key_version column must be populated.
	if keyVersion < 1 {
		t.Errorf("secret_key_version: got %d, want >= 1", keyVersion)
	}
	// The stored bytes must decode as a well-formed envelope blob whose
	// recorded KEK version matches the key_version column.
	sealed, err := envelope.Decode(blob)
	if err != nil {
		t.Fatalf("stored secret is not a valid envelope blob: %v", err)
	}
	if sealed.KEKVersion != keyVersion {
		t.Errorf("envelope blob KEK version %d does not match secret_key_version %d",
			sealed.KEKVersion, keyVersion)
	}
	if len(sealed.WrappedDEK) == 0 {
		t.Error("stored envelope blob has no wrapped DEK")
	}

	// The store still decrypts the secret on read.
	got, err := store.Get(ctx, tenant, c.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Secret != plaintext {
		t.Errorf("decrypted secret round-trip: got %q, want the plaintext", got.Secret)
	}

	// Rotate replaces the ciphertext; the new stored blob also hides
	// the plaintext and differs from the first.
	const rotated = "sk-ant-ROTATED-upstream-api-key"
	if _, err := store.Rotate(ctx, tenant, c.Ref, rotated); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	rotatedBlob, _ := readSecretColumn(t, ctx, pg, tenant, c.Ref)
	if bytes.Contains(rotatedBlob, []byte(rotated)) || bytes.Contains(rotatedBlob, []byte(plaintext)) {
		t.Fatalf("rotated credentials.secret column contains plaintext: % x", rotatedBlob)
	}
	if bytes.Equal(rotatedBlob, blob) {
		t.Error("Rotate did not change the stored ciphertext")
	}
	afterRotate, err := store.Get(ctx, tenant, c.Ref)
	if err != nil {
		t.Fatalf("Get after Rotate: %v", err)
	}
	if afterRotate.Secret != rotated {
		t.Errorf("decrypted secret after Rotate: got %q, want the rotated plaintext", afterRotate.Secret)
	}
}

// spec: 4, 12.9
// diagnosis: per-tenant KEK isolation does not hold. Each tenant's
// credential secrets are envelope-encrypted under a per-tenant KEK
// alias ("tenant:{id}"); a credential decrypted under one tenant's
// store context must not be readable as another tenant's.
func TestCredentialSecretPerTenantKEK(t *testing.T) {
	t.Parallel()
	store, _, pg := newCredentialStore(t)
	ctx := context.Background()
	acme := freshTenant(t, ctx, pg)
	globex := freshTenant(t, ctx, pg)

	acmeCred, err := store.Register(ctx, acme, "alice", credential.ProviderGitHub, "", "acme-secret")
	if err != nil {
		t.Fatalf("Register acme: %v", err)
	}
	globexCred, err := store.Register(ctx, globex, "alice", credential.ProviderGitHub, "", "globex-secret")
	if err != nil {
		t.Fatalf("Register globex: %v", err)
	}

	// Each tenant's stored ciphertext differs even for the same user
	// and provider; the per-tenant KEK and the per-record DEK both
	// guarantee this.
	acmeBlob, _ := readSecretColumn(t, ctx, pg, acme, acmeCred.Ref)
	globexBlob, _ := readSecretColumn(t, ctx, pg, globex, globexCred.Ref)
	if bytes.Equal(acmeBlob, globexBlob) {
		t.Error("two tenants' credential ciphertexts are identical")
	}

	// Each tenant reads back only its own plaintext.
	gotAcme, err := store.Get(ctx, acme, acmeCred.Ref)
	if err != nil {
		t.Fatalf("Get acme: %v", err)
	}
	if gotAcme.Secret != "acme-secret" {
		t.Errorf("acme secret: got %q, want acme-secret", gotAcme.Secret)
	}
	gotGlobex, err := store.Get(ctx, globex, globexCred.Ref)
	if err != nil {
		t.Fatalf("Get globex: %v", err)
	}
	if gotGlobex.Secret != "globex-secret" {
		t.Errorf("globex secret: got %q, want globex-secret", gotGlobex.Secret)
	}
}

// spec: 4, 12.9
// diagnosis: the credential store does not envelope-encrypt against
// the shared KMS test stub. The pgstore must work with any
// kms.Provider, including the fault-injecting tests/testinfra stub the
// rest of the credential suite uses.
func TestCredentialSecretWithStubKMS(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	stub := stubkms.New(t)
	store, err := credentialpg.New(pg.Pool, stub.AsProvider())
	if err != nil {
		t.Fatalf("credentialpg.New with stub KMS: %v", err)
	}
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	const plaintext = "sk-stub-kms-secret"
	c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "", plaintext)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	blob, _ := readSecretColumn(t, ctx, pg, tenant, c.Ref)
	if bytes.Contains(blob, []byte(plaintext)) {
		t.Fatalf("stub-KMS-backed store left plaintext in the secret column: % x", blob)
	}
	got, err := store.Get(ctx, tenant, c.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Secret != plaintext {
		t.Errorf("decrypted secret: got %q, want %q", got.Secret, plaintext)
	}
}
