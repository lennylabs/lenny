//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.2 TokenIssuanceStore, exercising
// pkg/gateway/issuedtokenstore against a real Postgres container with
// the production migrations applied. Covers record/get round-trip,
// the JTI uniqueness guard, cross-tenant isolation, revocation,
// revoked-token listing, and expired-row GC.
package stores_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
)

func TestTokenIssuanceStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := issuedtokenstore.New(pg.Pool)
	ctx := context.Background()

	t.Run("record and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		ts := time.Now().UTC().Truncate(time.Microsecond)
		want := issuedtokenstore.IssuedToken{
			JTI:        "jti-" + newUUID(t),
			TenantID:   tenant,
			Subject:    "alice@acme.com",
			TokenHash:  []byte{0xde, 0xad, 0xbe, 0xef},
			Scope:      []string{"sessions:read", "sessions:write"},
			Audience:   "lenny-gateway",
			IssuedAt:   ts,
			ExpiresAt:  ts.Add(time.Hour),
			ActSubject: "bob@acme.com",
			ParentJTI:  "jti-parent",
		}
		if err := store.Record(ctx, want); err != nil {
			t.Fatalf("Record: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.JTI)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Subject != want.Subject || got.Audience != want.Audience ||
			got.ActSubject != want.ActSubject || got.ParentJTI != want.ParentJTI {
			t.Errorf("scalar mismatch:\n got %+v\nwant %+v", got, want)
		}
		if !bytes.Equal(got.TokenHash, want.TokenHash) {
			t.Errorf("TokenHash mismatch: got %x want %x", got.TokenHash, want.TokenHash)
		}
		if !slices.Equal(got.Scope, want.Scope) {
			t.Errorf("Scope mismatch: got %v want %v", got.Scope, want.Scope)
		}
		if !got.IssuedAt.Equal(want.IssuedAt) || !got.ExpiresAt.Equal(want.ExpiresAt) {
			t.Errorf("timestamp mismatch: got %v/%v want %v/%v",
				got.IssuedAt, got.ExpiresAt, want.IssuedAt, want.ExpiresAt)
		}
		if got.Revoked() {
			t.Error("freshly recorded token reports Revoked()")
		}
	})

	t.Run("duplicate jti is rejected", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		tok := issuedtokenstore.IssuedToken{
			JTI: "jti-" + newUUID(t), TenantID: tenant, Subject: "alice@acme.com",
			TokenHash: []byte{0x01}, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		}
		if err := store.Record(ctx, tok); err != nil {
			t.Fatalf("first Record: %v", err)
		}
		if err := store.Record(ctx, tok); !errors.Is(err, issuedtokenstore.ErrAlreadyExists) {
			t.Errorf("duplicate Record: got %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("get missing and cross-tenant return ErrNotFound", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, owner, "jti-absent"); !errors.Is(err, issuedtokenstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		jti := "jti-" + newUUID(t)
		if err := store.Record(ctx, issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: owner, Subject: "alice@acme.com",
			TokenHash: []byte{0x02}, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("Record: %v", err)
		}
		if _, err := store.Get(ctx, intruder, jti); !errors.Is(err, issuedtokenstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	t.Run("revoke marks the token", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		jti := "jti-" + newUUID(t)
		if err := store.Record(ctx, issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: tenant, Subject: "alice@acme.com",
			TokenHash: []byte{0x03}, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("Record: %v", err)
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		if err := store.Revoke(ctx, tenant, jti, "compromised", at); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		got, err := store.Get(ctx, tenant, jti)
		if err != nil {
			t.Fatalf("Get after Revoke: %v", err)
		}
		if !got.Revoked() || got.RevokedReason != "compromised" || !got.RevokedAt.Equal(at) {
			t.Errorf("revocation not recorded: %+v", got)
		}
		if err := store.Revoke(ctx, tenant, "jti-absent", "x", at); !errors.Is(err, issuedtokenstore.ErrNotFound) {
			t.Errorf("Revoke missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list revoked returns revoked tokens in order", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		base := time.Now().UTC().Truncate(time.Microsecond)
		var jtis []string
		for i := 0; i < 3; i++ {
			jti := "jti-" + newUUID(t)
			jtis = append(jtis, jti)
			if err := store.Record(ctx, issuedtokenstore.IssuedToken{
				JTI: jti, TenantID: tenant, Subject: "alice@acme.com",
				TokenHash: []byte{byte(i)}, IssuedAt: base, ExpiresAt: base.Add(time.Hour),
			}); err != nil {
				t.Fatalf("Record %d: %v", i, err)
			}
		}
		// Revoke the first two at distinct, increasing instants.
		if err := store.Revoke(ctx, tenant, jtis[0], "first", base.Add(2*time.Minute)); err != nil {
			t.Fatalf("Revoke 0: %v", err)
		}
		if err := store.Revoke(ctx, tenant, jtis[1], "second", base.Add(time.Minute)); err != nil {
			t.Fatalf("Revoke 1: %v", err)
		}
		revoked, err := store.ListRevoked(ctx, tenant)
		if err != nil {
			t.Fatalf("ListRevoked: %v", err)
		}
		if len(revoked) != 2 {
			t.Fatalf("ListRevoked returned %d, want 2", len(revoked))
		}
		// Ordered by revoked_at ascending: jtis[1] (base+1m) before jtis[0] (base+2m).
		if revoked[0].JTI != jtis[1] || revoked[1].JTI != jtis[0] {
			t.Errorf("ListRevoked order: got %s,%s want %s,%s",
				revoked[0].JTI, revoked[1].JTI, jtis[1], jtis[0])
		}
	})

	t.Run("delete expired removes only past-expiry rows", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		now := time.Now().UTC().Truncate(time.Microsecond)
		expired := "jti-" + newUUID(t)
		live := "jti-" + newUUID(t)
		if err := store.Record(ctx, issuedtokenstore.IssuedToken{
			JTI: expired, TenantID: tenant, Subject: "alice@acme.com",
			TokenHash: []byte{0x04}, IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
		}); err != nil {
			t.Fatalf("Record expired: %v", err)
		}
		if err := store.Record(ctx, issuedtokenstore.IssuedToken{
			JTI: live, TenantID: tenant, Subject: "alice@acme.com",
			TokenHash: []byte{0x05}, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("Record live: %v", err)
		}
		n, err := store.DeleteExpired(ctx, tenant, now)
		if err != nil {
			t.Fatalf("DeleteExpired: %v", err)
		}
		if n != 1 {
			t.Errorf("DeleteExpired removed %d rows, want 1", n)
		}
		if _, err := store.Get(ctx, tenant, expired); !errors.Is(err, issuedtokenstore.ErrNotFound) {
			t.Errorf("expired token still present: %v", err)
		}
		if _, err := store.Get(ctx, tenant, live); err != nil {
			t.Errorf("live token wrongly removed: %v", err)
		}
	})
}
