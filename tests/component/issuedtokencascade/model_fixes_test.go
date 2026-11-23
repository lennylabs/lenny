//go:build component

// SPDX-License-Identifier: MIT

package issuedtokencascade_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// modelFixture starts a Postgres container with the production migrations
// and returns its issued-token store and a recorder of plain rows.
func modelFixture(t *testing.T) (*containers.Postgres, *issuedtokenstore.Store, func(t *testing.T, tok issuedtokenstore.IssuedToken)) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := issuedtokenstore.New(pg.Pool)
	record := func(t *testing.T, tok issuedtokenstore.IssuedToken) {
		t.Helper()
		now := time.Now().UTC()
		if tok.TokenHash == nil {
			tok.TokenHash = []byte(tok.JTI)
		}
		if tok.IssuedAt.IsZero() {
			tok.IssuedAt = now
		}
		if tok.ExpiresAt.IsZero() {
			tok.ExpiresAt = now.Add(time.Hour)
		}
		if err := store.Record(context.Background(), tok); err != nil {
			t.Fatalf("Record %s: %v", tok.JTI, err)
		}
	}
	return pg, store, record
}

// revokedState returns the revoked_reason of each listed jti, or "live".
func revokedState(t *testing.T, store *issuedtokenstore.Store, tenant string, jtis ...string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(jtis))
	for _, jti := range jtis {
		row, err := store.Get(context.Background(), tenant, jti)
		if err != nil {
			t.Fatalf("Get %s: %v", jti, err)
		}
		out[jti] = "live"
		if row.Revoked() {
			out[jti] = row.RevokedReason
		}
	}
	return out
}

// spec: §13.3 (Credential Flow)
// diagnosis: a failure means a recursive revocation whose root has no
// issued_tokens row neither records the root nor reaches the tokens
// derived from it (the scenario CX-5 of change 0165's model), or that the
// cascade no longer reports the whole lineage subtree, including rows
// revoked before the request, which the impersonator-link revocation reads.
func TestRevokeLineageRecordsARootWithoutARow(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	record(t, issuedtokenstore.IssuedToken{JTI: "narrowed", TenantID: tenant, Subject: "alice@acme.com", ParentJTI: "id-token"})
	record(t, issuedtokenstore.IssuedToken{JTI: "rotated", TenantID: tenant, Subject: "alice@acme.com", ParentJTI: "narrowed",
		RevokedAt: time.Now().UTC(), RevokedReason: "rotation_replaced"})
	record(t, issuedtokenstore.IssuedToken{JTI: "successor", TenantID: tenant, Subject: "alice@acme.com", ParentJTI: "rotated"})

	now := time.Now().UTC().Truncate(time.Second)
	res, err := store.RevokeLineage(ctx, issuedtokenstore.LineageRevocation{
		TenantID: tenant,
		Root: issuedtokenstore.IssuedToken{
			JTI: "id-token", Subject: "alice@acme.com", TokenHash: []byte("hash"),
			IssuedAt: now, ExpiresAt: now.Add(time.Hour),
		},
		RootReason: "explicit_revoke", ChildReason: "cascade_from_parent", At: now,
	})
	if err != nil {
		t.Fatalf("RevokeLineage: %v", err)
	}
	got := revokedState(t, store, tenant, "id-token", "narrowed", "rotated", "successor")
	want := map[string]string{
		"id-token": "explicit_revoke", "narrowed": "cascade_from_parent",
		"rotated": "rotation_replaced", "successor": "cascade_from_parent",
	}
	for jti, w := range want {
		if got[jti] != w {
			t.Errorf("%s = %q, want %q", jti, got[jti], w)
		}
	}
	if len(res.Revoked) != 3 {
		t.Errorf("revoked %d rows, want 3 (the root, narrowed, and successor)", len(res.Revoked))
	}
	subtree := append([]string(nil), res.Subtree...)
	sort.Strings(subtree)
	if len(subtree) != 4 || subtree[0] != "id-token" || subtree[3] != "successor" {
		t.Errorf("subtree = %v, want the four rows including the already-revoked one", subtree)
	}

	other := freshTenant(t, ctx, pg)
	if _, err := store.RevokeLineage(ctx, issuedtokenstore.LineageRevocation{
		TenantID: other, Root: issuedtokenstore.IssuedToken{JTI: "ghost"}, RootReason: "explicit_revoke", At: now,
	}); !errors.Is(err, issuedtokenstore.ErrNotFound) {
		t.Errorf("a root without a row and without a token hash: err = %v, want ErrNotFound", err)
	}
}

// spec: §10.2 (Authentication), §13.3 (Credential Flow)
// diagnosis: a failure means a playground bearer recorded with its pasted
// subject as parent is not reached by the recursive revocation of the
// subject (the scenario CX-3 of change 0165's model), or that the record
// does not refuse a subject revoked before it commits.
func TestPlaygroundBearerRowFollowsItsSubject(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	record(t, issuedtokenstore.IssuedToken{JTI: "pasted", TenantID: tenant, Subject: "alice@acme.com"})
	now := time.Now().UTC()
	bearer := issuedtokenstore.IssuedToken{
		JTI: "pg-bearer", TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte("pg"),
		IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute), ParentJTI: "pasted",
	}
	sources := subjectSource("pasted")
	if err := store.RecordWithSources(ctx, bearer, sources); err != nil {
		t.Fatalf("RecordWithSources: %v", err)
	}
	if _, err := store.RevokeCascadeByCaller(ctx, tenant, "", "pasted", "explicit_revoke", "cascade_from_parent", now); err != nil {
		t.Fatalf("RevokeCascadeByCaller: %v", err)
	}
	if got := revokedState(t, store, tenant, "pg-bearer"); got["pg-bearer"] != "cascade_from_parent" {
		t.Errorf("playground bearer = %q, want cascade_from_parent", got["pg-bearer"])
	}

	second := bearer
	second.JTI = "pg-bearer-2"
	var re *issuedtokenstore.SourceRevokedError
	if err := store.RecordWithSources(ctx, second, sources); !errors.As(err, &re) || re.Role != issuedtokenstore.SourceSubjectToken {
		t.Errorf("record from a revoked subject: err = %v, want a SourceRevokedError naming the subject", err)
	}
	if _, err := store.Get(ctx, tenant, "pg-bearer-2"); !errors.Is(err, issuedtokenstore.ErrNotFound) {
		t.Errorf("a refused record left a row: err = %v", err)
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a failure means the impersonator-link revocation does not end
// an impersonation session in another tenant whose platform-admin bearer a
// recursive revocation reached (the scenario CX-4 of change 0165's model),
// or that it reaches a row started from another tenant's bearer, a row
// that is not an impersonation row, or a session that already ended.
func TestImpersonatorLinkEndsTheSessionAcrossTenants(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	adminTenant := freshTenant(t, ctx, pg)
	targetTenant := freshTenant(t, ctx, pg)
	otherTenant := freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	imp := func(jti, impTenant, impJTI string) issuedtokenstore.IssuedToken {
		return issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: targetTenant, Subject: "alice@initech.com", TokenHash: []byte(jti),
			IssuedAt: now, ExpiresAt: now.Add(30 * time.Minute), ActSubject: "carol@globex.com",
			ImpersonatorTenantID: impTenant, ImpersonatorJTI: impJTI,
		}
	}
	for _, tok := range []issuedtokenstore.IssuedToken{
		imp("imp-reached", adminTenant, "carol-bearer"),
		imp("imp-other-tenant", otherTenant, "carol-bearer"),
		imp("imp-other-bearer", adminTenant, "dave-bearer"),
	} {
		if err := store.RecordImpersonation(ctx, tok, adminTenant); err != nil {
			t.Fatalf("RecordImpersonation %s: %v", tok.JTI, err)
		}
	}
	row, err := store.Get(ctx, targetTenant, "imp-reached")
	if err != nil || row.ImpersonatorTenantID != adminTenant || row.ImpersonatorJTI != "carol-bearer" {
		t.Fatalf("recorded impersonation row = %+v, %v; want the impersonator link", row, err)
	}
	// A delegation child that happens to carry the same link columns is not
	// an impersonation row and is left alone.
	record(t, issuedtokenstore.IssuedToken{JTI: "child", TenantID: targetTenant, Subject: "alice@initech.com",
		ActSubject: "carol@globex.com", ParentJTI: "p", ImpersonatorTenantID: adminTenant, ImpersonatorJTI: "carol-bearer"})

	ended, err := store.RevokeImpersonationsByImpersonator(ctx, adminTenant, []string{"carol-root", "carol-bearer"}, "cascade_from_parent", now)
	if err != nil {
		t.Fatalf("RevokeImpersonationsByImpersonator: %v", err)
	}
	if len(ended) != 1 || ended[0].JTI != "imp-reached" || ended[0].TenantID != targetTenant || ended[0].Subject != "alice@initech.com" {
		t.Errorf("ended = %+v, want imp-reached in the target tenant", ended)
	}
	got := revokedState(t, store, targetTenant, "imp-reached", "imp-other-tenant", "imp-other-bearer", "child")
	if got["imp-reached"] != "cascade_from_parent" || got["imp-other-tenant"] != "live" ||
		got["imp-other-bearer"] != "live" || got["child"] != "live" {
		t.Errorf("states = %v", got)
	}
	again, err := store.RevokeImpersonationsByImpersonator(ctx, adminTenant, []string{"carol-bearer"}, "cascade_from_parent", now)
	if err != nil || len(again) != 0 {
		t.Errorf("second call ended %+v, %v; want nothing", again, err)
	}
	if none, err := store.RevokeImpersonationsByImpersonator(ctx, adminTenant, nil, "cascade_from_parent", now); err != nil || none != nil {
		t.Errorf("empty jti list = %+v, %v; want no query", none, err)
	}
}

// spec: §11.4 (User Invalidation), §13.3 (Credential Flow)
// diagnosis: a failure means a full revoke of a user leaves live a token
// another user derived from one of the user's tokens (the scenario CX-11
// of change 0165's model), misses the descendants of a token the user had
// already revoked, or revokes tokens outside the user's lineage.
func TestRevokeBySubjectFollowsTheLineage(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	record(t, issuedtokenstore.IssuedToken{JTI: "alice-a", TenantID: tenant, Subject: "alice@acme.com"})
	record(t, issuedtokenstore.IssuedToken{JTI: "bob-child", TenantID: tenant, Subject: "bob@acme.com",
		ActSubject: "alice@acme.com", ParentJTI: "alice-a"})
	record(t, issuedtokenstore.IssuedToken{JTI: "bob-narrowed", TenantID: tenant, Subject: "bob@acme.com", ParentJTI: "bob-child"})
	record(t, issuedtokenstore.IssuedToken{JTI: "alice-old", TenantID: tenant, Subject: "alice@acme.com",
		RevokedAt: time.Now().UTC(), RevokedReason: "rotation_replaced"})
	record(t, issuedtokenstore.IssuedToken{JTI: "carol-from-old", TenantID: tenant, Subject: "carol@acme.com", ParentJTI: "alice-old"})
	record(t, issuedtokenstore.IssuedToken{JTI: "bob-own", TenantID: tenant, Subject: "bob@acme.com"})

	jtis, err := store.RevokeBySubject(ctx, tenant, "alice@acme.com", "full_revoke", time.Now().UTC())
	if err != nil {
		t.Fatalf("RevokeBySubject: %v", err)
	}
	sort.Strings(jtis)
	want := []string{"alice-a", "bob-child", "bob-narrowed", "carol-from-old"}
	if len(jtis) != len(want) {
		t.Fatalf("revoked %v, want %v", jtis, want)
	}
	for i := range want {
		if jtis[i] != want[i] {
			t.Fatalf("revoked %v, want %v", jtis, want)
		}
	}
	if got := revokedState(t, store, tenant, "bob-own", "alice-old"); got["bob-own"] != "live" || got["alice-old"] != "rotation_replaced" {
		t.Errorf("states = %v, want bob-own live and alice-old untouched", got)
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a failure means the write-before-issue re-read admits a mint
// whose caller token is revoked (the scenario CX-10 of change 0165's
// model), or refuses a caller token for being an impersonation row.
func TestRecordWithAuditRefusesARevokedCaller(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	record(t, issuedtokenstore.IssuedToken{JTI: "subject", TenantID: tenant, Subject: "alice@acme.com"})
	record(t, issuedtokenstore.IssuedToken{JTI: "caller", TenantID: tenant, Subject: "alice@acme.com",
		RevokedAt: time.Now().UTC(), RevokedReason: "rotation_replaced"})
	record(t, issuedtokenstore.IssuedToken{JTI: "imp-caller", TenantID: tenant, Subject: "alice@acme.com", ActSubject: "admin@acme.com"})
	now := time.Now().UTC()
	mint := func(jti, caller string) error {
		_, err := store.RecordWithAudit(ctx, issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: tenant, Subject: "alice@acme.com", TokenHash: []byte(jti),
			IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: "subject",
		}, []issuedtokenstore.SourceToken{
			{Role: issuedtokenstore.SourceSubjectToken, JTI: "subject"},
			{Role: issuedtokenstore.SourceCallerToken, JTI: caller},
		}, "token.exchanged", json.RawMessage(`{}`), now)
		return err
	}
	var re *issuedtokenstore.SourceRevokedError
	if err := mint("minted-1", "caller"); !errors.As(err, &re) || re.Reason() != "caller_token_revoked" {
		t.Errorf("mint with a revoked caller: err = %v, want caller_token_revoked", err)
	}
	if err := mint("minted-2", "imp-caller"); err != nil {
		t.Errorf("mint with an impersonation caller: err = %v, want nil", err)
	}
}
