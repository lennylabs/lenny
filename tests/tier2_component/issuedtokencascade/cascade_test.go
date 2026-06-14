//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §13.3 line 603 / §8.3 recursive token
// revocation (issuedtokenstore.RevokeCascade) against a real Postgres
// container with the production migrations applied. Lives in its own
// package so a stale unrelated test in tests/tier2_component/stores
// does not block it from compiling.
package issuedtokencascade_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func freshTenant(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	id := "t-" + newUUID(t)[:8]
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
	return id
}

// spec: §13.3 line 603 / §8.3 — RevokeCascade revokes the root token
// and every delegation descendant reachable through parent_jti, stamps
// the root with the explicit reason and descendants with the cascade
// reason, is idempotent (a second call revokes nothing already
// revoked), leaves an unrelated tree untouched, and returns ErrNotFound
// for an unknown root or a cross-tenant root.
// diagnosis: a failure means RevokeCascade misses delegation
// descendants, stamps the wrong revocation reason, is not idempotent,
// touches an unrelated tree, or fails to fail-closed on an unknown or
// cross-tenant root.
func TestRevokeCascadeContract_spec_13_3_603(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := issuedtokenstore.New(pg.Pool)
	ctx := context.Background()

	record := func(t *testing.T, tenant, jti, sub, parent string) {
		t.Helper()
		now := time.Now().UTC()
		if err := store.Record(ctx, issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: tenant, Subject: sub, TokenHash: []byte(jti),
			IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: parent,
		}); err != nil {
			t.Fatalf("Record %s: %v", jti, err)
		}
	}

	t.Run("cascade revokes the whole subtree with per-node reasons", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		// root → child → grandchild, plus a sibling child of root.
		record(t, tenant, "root", "alice@acme.com", "")
		record(t, tenant, "child", "bob@acme.com", "root")
		record(t, tenant, "grandchild", "carol@acme.com", "child")
		record(t, tenant, "sibling", "dave@acme.com", "root")
		// An unrelated tree that must NOT be touched.
		record(t, tenant, "other-root", "eve@acme.com", "")

		at := time.Now().UTC().Truncate(time.Microsecond)
		revoked, err := store.RevokeCascade(ctx, tenant, "root", "explicit_revoke", "cascade_from_parent", at)
		if err != nil {
			t.Fatalf("RevokeCascade: %v", err)
		}
		if len(revoked) != 4 {
			t.Fatalf("revoked %d nodes, want 4 (root+child+grandchild+sibling)", len(revoked))
		}
		roots := 0
		for _, rt := range revoked {
			if rt.IsRoot {
				roots++
				if rt.JTI != "root" {
					t.Errorf("IsRoot node = %q, want root", rt.JTI)
				}
			}
		}
		if roots != 1 {
			t.Errorf("IsRoot count = %d, want 1", roots)
		}
		for _, jti := range []string{"root", "child", "grandchild", "sibling"} {
			got, err := store.Get(ctx, tenant, jti)
			if err != nil {
				t.Fatalf("Get %s: %v", jti, err)
			}
			if !got.Revoked() {
				t.Errorf("%s not revoked", jti)
			}
			wantReason := "cascade_from_parent"
			if jti == "root" {
				wantReason = "explicit_revoke"
			}
			if got.RevokedReason != wantReason {
				t.Errorf("%s revoked_reason = %q, want %q", jti, got.RevokedReason, wantReason)
			}
		}
		if other, _ := store.Get(ctx, tenant, "other-root"); other.Revoked() {
			t.Error("unrelated tree was revoked by the cascade")
		}

		// Idempotent: a second cascade revokes nothing new.
		again, err := store.RevokeCascade(ctx, tenant, "root", "explicit_revoke", "cascade_from_parent", at)
		if err != nil {
			t.Fatalf("RevokeCascade (idempotent): %v", err)
		}
		if len(again) != 0 {
			t.Errorf("second cascade revoked %d nodes, want 0", len(again))
		}
	})

	t.Run("unknown root returns ErrNotFound", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.RevokeCascade(ctx, tenant, "ghost", "explicit_revoke", "cascade_from_parent", time.Now()); !errors.Is(err, issuedtokenstore.ErrNotFound) {
			t.Errorf("RevokeCascade unknown root: got %v, want ErrNotFound", err)
		}
	})

	t.Run("cross-tenant cascade does not cross the boundary", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		record(t, owner, "owner-root", "alice@acme.com", "")
		if _, err := store.RevokeCascade(ctx, intruder, "owner-root", "explicit_revoke", "cascade_from_parent", time.Now()); !errors.Is(err, issuedtokenstore.ErrNotFound) {
			t.Errorf("cross-tenant cascade: got %v, want ErrNotFound", err)
		}
		if got, _ := store.Get(ctx, owner, "owner-root"); got.Revoked() {
			t.Error("owner root revoked by a cross-tenant cascade")
		}
	})
}

// spec: §13.3 line 597 — RecordWithRotationAudit binds the new
// issued-token INSERT, the token.exchanged audit row, and the revoked_at
// stamp on the previous token into one transaction: after it returns the
// replacement exists and the previous token is revoked with
// rotation_replaced, with no window in which both are live or the old is
// revoked but the new never issued. A missing or already-revoked
// previous token reports revoked=false and leaves the mint intact.
// diagnosis: a failure means token rotation is not atomic: a window
// exists where both tokens are live or the old is revoked while the new
// was never issued, breaking the §13.3 rotation invariant.
func TestRecordWithRotationAuditContract_spec_13_3_597(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := issuedtokenstore.New(pg.Pool)
	ctx := context.Background()

	record := func(t *testing.T, tenant, jti, sub string) {
		t.Helper()
		now := time.Now().UTC()
		if err := store.Record(ctx, issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: tenant, Subject: sub, TokenHash: []byte(jti),
			IssuedAt: now, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("Record %s: %v", jti, err)
		}
	}

	t.Run("rotation mints replacement and revokes previous atomically", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		record(t, tenant, "old-jti", "alice@acme.com")

		at := time.Now().UTC().Truncate(time.Microsecond)
		newTok := issuedtokenstore.IssuedToken{
			JTI: "new-jti", TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte("new-jti"),
			Scope: []string{"sessions:read"}, Audiences: []string{"lenny-gateway"},
			IssuedAt: at, ExpiresAt: at.Add(time.Hour),
		}
		sub, revoked, err := store.RecordWithRotationAudit(ctx, newTok, "old-jti",
			"rotation_replaced", "token.exchanged", []byte(`{"policy_result":"accepted"}`), at)
		if err != nil {
			t.Fatalf("RecordWithRotationAudit: %v", err)
		}
		if !revoked || sub != "alice@acme.com" {
			t.Fatalf("revoked=%v sub=%q, want true / alice@acme.com", revoked, sub)
		}
		// New token is live.
		got, err := store.Get(ctx, tenant, "new-jti")
		if err != nil {
			t.Fatalf("Get new-jti: %v", err)
		}
		if got.Revoked() {
			t.Error("replacement token is revoked")
		}
		// Old token is revoked with rotation_replaced.
		old, err := store.Get(ctx, tenant, "old-jti")
		if err != nil {
			t.Fatalf("Get old-jti: %v", err)
		}
		if !old.Revoked() || old.RevokedReason != "rotation_replaced" {
			t.Errorf("old token revoked=%v reason=%q, want true / rotation_replaced", old.Revoked(), old.RevokedReason)
		}
	})

	t.Run("re-rotation against an already-revoked previous token is a no-op revoke", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		record(t, tenant, "prev", "bob@acme.com")
		at := time.Now().UTC().Truncate(time.Microsecond)
		// First rotation revokes prev.
		if _, revoked, err := store.RecordWithRotationAudit(ctx, issuedtokenstore.IssuedToken{
			JTI: "r1", TenantID: tenant, Subject: "bob@acme.com", TokenHash: []byte("r1"),
			IssuedAt: at, ExpiresAt: at.Add(time.Hour),
		}, "prev", "rotation_replaced", "token.exchanged", []byte(`{}`), at); err != nil || !revoked {
			t.Fatalf("first rotation: revoked=%v err=%v", revoked, err)
		}
		// Second rotation naming the already-revoked prev still mints but
		// reports revoked=false.
		_, revoked, err := store.RecordWithRotationAudit(ctx, issuedtokenstore.IssuedToken{
			JTI: "r2", TenantID: tenant, Subject: "bob@acme.com", TokenHash: []byte("r2"),
			IssuedAt: at, ExpiresAt: at.Add(time.Hour),
		}, "prev", "rotation_replaced", "token.exchanged", []byte(`{}`), at)
		if err != nil {
			t.Fatalf("second rotation: %v", err)
		}
		if revoked {
			t.Error("second rotation reported a revoke of an already-revoked previous token")
		}
		if got, _ := store.Get(ctx, tenant, "r2"); got.Revoked() {
			t.Error("second replacement should be live")
		}
	})

	t.Run("rotation with absent previous token mints without revoke", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		at := time.Now().UTC().Truncate(time.Microsecond)
		_, revoked, err := store.RecordWithRotationAudit(ctx, issuedtokenstore.IssuedToken{
			JTI: "solo", TenantID: tenant, Subject: "carol@acme.com", TokenHash: []byte("solo"),
			IssuedAt: at, ExpiresAt: at.Add(time.Hour),
		}, "ghost-prev", "rotation_replaced", "token.exchanged", []byte(`{}`), at)
		if err != nil {
			t.Fatalf("rotation with absent prev: %v", err)
		}
		if revoked {
			t.Error("revoked=true for an absent previous token")
		}
		if got, err := store.Get(ctx, tenant, "solo"); err != nil || got.Revoked() {
			t.Errorf("replacement not minted or revoked: err=%v revoked=%v", err, got.Revoked())
		}
	})
}
