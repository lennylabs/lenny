//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §13.3 encrypted TokenStore — the Postgres
// §9.3 connector-OAuth credential registry. Exercises
// pkg/gateway/connectorcredstore/pgstore against a real Postgres
// container with migration 0048 applied. Covers the Put/Get
// round-trip with both access and refresh tokens, the upsert path,
// the §13.3 SHA-256 revocation-lookup index, KMS-envelope
// encryption of the secret columns at rest, the seed tenants must
// exist before the FK fires, cross-tenant isolation under the new
// RLS policy, and the Delete path.
package stores_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectorcredstore/pgstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func newConnectorCredStore(t *testing.T) (*connectorpg.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("kms provider: %v", err)
	}
	clock := func() time.Time { return time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC) }
	store, err := connectorpg.New(pg.Pool, provider, clock)
	if err != nil {
		t.Fatalf("connectorpg.New: %v", err)
	}
	return store, pg
}

func seedConnectorTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
}

// spec: §13.3 (encrypted TokenStore)
// diagnosis: Put + Get round-trips a connector credential with both
// access and refresh tokens; the stored bytes are envelope-encrypted
// (the access_token_blob is not the plaintext access token); the
// SHA-256 access-token hash is stored in the revocation-lookup
// column for the §13.3 hot path; cross-tenant access returns
// ErrNotFound under the RLS policy.
func TestTokenStoreContract(t *testing.T) {
	t.Parallel()
	store, pg := newConnectorCredStore(t)
	ctx := context.Background()
	seedConnectorTenant(t, ctx, pg, "acme")
	seedConnectorTenant(t, ctx, pg, "globex")

	t.Run("Put + Get round-trips access and refresh tokens", func(t *testing.T) {
		cred := connectorcredstore.ConnectorCredential{
			TenantID:     "acme",
			ConnectorID:  "github",
			UserID:       "alice@acme.com",
			AccessToken:  "gho_AccessToken_AAAAAAAAAAAAAAAAAAAAAAAA",
			RefreshToken: "ghr_RefreshToken_BBBBBBBBBBBBBBBBBBBBBBB",
			TokenType:    "Bearer",
			Scopes:       []string{"repo", "read:org"},
			ExpiresAt:    time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC),
		}
		if err := store.Put(ctx, cred); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := store.Get(ctx, "acme", "github", "alice@acme.com")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.AccessToken != cred.AccessToken {
			t.Errorf("AccessToken = %q, want %q", got.AccessToken, cred.AccessToken)
		}
		if got.RefreshToken != cred.RefreshToken {
			t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, cred.RefreshToken)
		}
		if got.TokenType != "Bearer" {
			t.Errorf("TokenType = %q, want Bearer", got.TokenType)
		}
		if len(got.Scopes) != 2 || got.Scopes[0] != "repo" || got.Scopes[1] != "read:org" {
			t.Errorf("Scopes = %v, want [repo read:org]", got.Scopes)
		}
		if !got.ExpiresAt.Equal(cred.ExpiresAt) {
			t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, cred.ExpiresAt)
		}
	})

	t.Run("stored bytes are envelope-encrypted, not plaintext", func(t *testing.T) {
		// Re-Put under a fresh user so the secret column is fresh too.
		cred := connectorcredstore.ConnectorCredential{
			TenantID:    "acme",
			ConnectorID: "github",
			UserID:      "bob@acme.com",
			AccessToken: "gho_BobAccessToken_AAAAAAAAAAAAAAAAAA",
			TokenType:   "Bearer",
		}
		if err := store.Put(ctx, cred); err != nil {
			t.Fatalf("Put: %v", err)
		}
		// Inspect the raw column under a tenant-context transaction.
		var blob []byte
		ttx, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = ttx.Rollback(ctx) }()
		if _, err := ttx.Exec(ctx,
			"SELECT set_config('app.current_tenant', $1, true)", "acme"); err != nil {
			t.Fatalf("set tenant context: %v", err)
		}
		if err := ttx.QueryRow(ctx, `
			SELECT access_token_blob FROM connector_credentials
			WHERE tenant_id = $1 AND connector_id = $2 AND user_id = $3`,
			"acme", "github", "bob@acme.com").Scan(&blob); err != nil {
			t.Fatalf("scan blob: %v", err)
		}
		if bytes.Contains(blob, []byte(cred.AccessToken)) {
			t.Errorf("access_token_blob contains plaintext: §13.3 envelope encryption violated")
		}
		if len(blob) == 0 {
			t.Errorf("access_token_blob is empty; envelope encryption did not produce ciphertext")
		}
	})

	t.Run("SHA-256(access_token) hash backs the revocation lookup", func(t *testing.T) {
		token := "gho_HashLookupToken_CCCCCCCCCCCCCCCCCC"
		cred := connectorcredstore.ConnectorCredential{
			TenantID:    "acme",
			ConnectorID: "github",
			UserID:      "carol@acme.com",
			AccessToken: token,
			TokenType:   "Bearer",
		}
		if err := store.Put(ctx, cred); err != nil {
			t.Fatalf("Put: %v", err)
		}
		sum := sha256.Sum256([]byte(token))
		found, err := store.FindByAccessTokenHash(ctx, "acme", sum[:])
		if err != nil {
			t.Fatalf("FindByAccessTokenHash: %v", err)
		}
		if found.UserID != "carol@acme.com" || found.AccessToken != token {
			t.Errorf("FindByAccessTokenHash = %+v, want carol's row", found)
		}
		// A miss returns ErrNotFound.
		wrong := sha256.Sum256([]byte("nope"))
		_, err = store.FindByAccessTokenHash(ctx, "acme", wrong[:])
		if !errors.Is(err, connectorcredstore.ErrNotFound) {
			t.Errorf("FindByAccessTokenHash for unknown hash = %v, want ErrNotFound", err)
		}
	})

	t.Run("re-Put upserts and advances UpdatedAt", func(t *testing.T) {
		cred := connectorcredstore.ConnectorCredential{
			TenantID:    "acme",
			ConnectorID: "github",
			UserID:      "dave@acme.com",
			AccessToken: "gho_DaveV1",
			TokenType:   "Bearer",
		}
		if err := store.Put(ctx, cred); err != nil {
			t.Fatalf("first Put: %v", err)
		}
		got1, _ := store.Get(ctx, "acme", "github", "dave@acme.com")
		cred.AccessToken = "gho_DaveV2"
		if err := store.Put(ctx, cred); err != nil {
			t.Fatalf("second Put: %v", err)
		}
		got2, _ := store.Get(ctx, "acme", "github", "dave@acme.com")
		if got2.AccessToken != "gho_DaveV2" {
			t.Errorf("AccessToken after re-Put = %q, want gho_DaveV2", got2.AccessToken)
		}
		if !got2.CreatedAt.Equal(got1.CreatedAt) {
			t.Errorf("CreatedAt advanced on upsert: %v vs %v", got2.CreatedAt, got1.CreatedAt)
		}
		if !got2.UpdatedAt.After(got1.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: %v vs %v", got2.UpdatedAt, got1.UpdatedAt)
		}
	})

	t.Run("cross-tenant Get returns ErrNotFound", func(t *testing.T) {
		cred := connectorcredstore.ConnectorCredential{
			TenantID:    "globex",
			ConnectorID: "github",
			UserID:      "eve@globex.com",
			AccessToken: "gho_EveToken",
			TokenType:   "Bearer",
		}
		if err := store.Put(ctx, cred); err != nil {
			t.Fatalf("Put: %v", err)
		}
		// Reading eve under acme's tenant must miss.
		_, err := store.Get(ctx, "acme", "github", "eve@globex.com")
		if !errors.Is(err, connectorcredstore.ErrNotFound) {
			t.Errorf("cross-tenant Get = %v, want ErrNotFound", err)
		}
	})

	t.Run("Delete removes the row and is ErrNotFound for an absent one", func(t *testing.T) {
		cred := connectorcredstore.ConnectorCredential{
			TenantID:    "acme",
			ConnectorID: "github",
			UserID:      "frank@acme.com",
			AccessToken: "gho_FrankToken",
			TokenType:   "Bearer",
		}
		if err := store.Put(ctx, cred); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := store.Delete(ctx, "acme", "github", "frank@acme.com"); err != nil {
			t.Errorf("Delete: %v", err)
		}
		if err := store.Delete(ctx, "acme", "github", "frank@acme.com"); !errors.Is(err, connectorcredstore.ErrNotFound) {
			t.Errorf("second Delete = %v, want ErrNotFound", err)
		}
	})

	t.Run("ListByConnector returns the connector's credentials in user order", func(t *testing.T) {
		for _, user := range []string{"zeta@acme.com", "alice2@acme.com", "marvin@acme.com"} {
			if err := store.Put(ctx, connectorcredstore.ConnectorCredential{
				TenantID:    "acme",
				ConnectorID: "slack",
				UserID:      user,
				AccessToken: "xoxb-" + user,
				TokenType:   "Bearer",
			}); err != nil {
				t.Fatalf("Put %s: %v", user, err)
			}
		}
		out, err := store.ListByConnector(ctx, "acme", "slack")
		if err != nil {
			t.Fatalf("ListByConnector: %v", err)
		}
		if len(out) != 3 {
			t.Fatalf("ListByConnector len = %d, want 3", len(out))
		}
		want := []string{"alice2@acme.com", "marvin@acme.com", "zeta@acme.com"}
		for i, u := range want {
			if out[i].UserID != u {
				t.Errorf("out[%d].UserID = %q, want %q", i, out[i].UserID, u)
			}
		}
	})
}
