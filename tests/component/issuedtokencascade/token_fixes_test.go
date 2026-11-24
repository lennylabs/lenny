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

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// inTenant runs fn in a transaction under tenant's context on the
// container's pool and commits it.
func inTenant(t *testing.T, pg *containers.Postgres, tenant string, fn func(tx pgx.Tx) error) {
	t.Helper()
	if err := pgtenant.InTx(context.Background(), pg.Pool, tenant, fn); err != nil {
		t.Fatalf("transaction in %s: %v", tenant, err)
	}
}

// openTenantTx begins a transaction under tenant's context and returns it
// open, so a test can hold a lock while another call runs. The cleanup
// rolls it back when the test has not ended it.
func openTenantTx(t *testing.T, pg *containers.Postgres, tenant string) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if err := pgtenant.SetLocal(ctx, tx, tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	return tx
}

// seedUser writes a user row, invalidated when deletedAt is set and soft
// disabled when disabled is true.
func seedUser(t *testing.T, pg *containers.Postgres, tenant, sub string, deletedAt *time.Time, disabled bool) {
	t.Helper()
	inTenant(t, pg, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO users (tenant_id, subject, deleted_at, disabled) VALUES ($1, $2, $3, $4)`,
			tenant, sub, deletedAt, disabled)
		return err
	})
}

// startImpersonation records an impersonation bearer jti of sub in
// targetTenant started by the caller bearer callerJTI of callerTenant.
func startImpersonation(t *testing.T, store *issuedtokenstore.Store, jti, targetTenant, sub, callerTenant, callerJTI string) error {
	t.Helper()
	now := time.Now().UTC()
	return store.RecordImpersonation(context.Background(), issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: targetTenant, Subject: sub, TokenHash: []byte(jti),
		IssuedAt: now, ExpiresAt: now.Add(30 * time.Minute), ActSubject: "bob@acme.com",
		ImpersonatorTenantID: callerTenant, ImpersonatorJTI: callerJTI,
	}, callerTenant)
}

// startRecords returns the target tenants of the start records visible
// under tenant's context to the application role, which row-level
// security restricts; the container's superuser bypasses it.
func startRecords(t *testing.T, pg *containers.Postgres, tenant string) []string {
	t.Helper()
	var out []string
	inTenant(t, pg, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), "SET LOCAL ROLE lenny_app"); err != nil {
			return err
		}
		rows, err := tx.Query(context.Background(), `SELECT target_tenant_id FROM impersonation_starts ORDER BY target_tenant_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out
}

// spec: §13.3 (Credential Flow)
// diagnosis: the cascade refused a caller that the same request's
// impersonation step revoked, so an impersonation bearer started from the
// root could not revoke the root (TOKX-I22, finding F1), or it accepted a
// revoked caller that the step did not revoke. Check checkCallerLive and
// LineageRevocation.StepRevokedCallers in pkg/storage/issuedtokenstore.
func TestCascadeAcceptsOnlyTheCallersItsOwnStepRevoked(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	acme := freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	record(t, issuedtokenstore.IssuedToken{JTI: "bob-b", TenantID: acme, Subject: "bob@acme.com"})
	record(t, issuedtokenstore.IssuedToken{JTI: "bob-d", TenantID: acme, Subject: "bob@acme.com", RevokedAt: now, RevokedReason: "explicit_revoke"})
	if err := startImpersonation(t, store, "imp-own", acme, "bob@acme.com", acme, "bob-b"); err != nil {
		t.Fatalf("RecordImpersonation: %v", err)
	}
	res, err := store.RevokeImpersonationStep(ctx, issuedtokenstore.ImpersonationStep{
		TenantID: acme, CallerJTI: "imp-own", CallerSubject: "bob@acme.com", RootJTI: "bob-b", Reason: "cascade_from_parent", At: now,
	})
	if err != nil {
		t.Fatalf("RevokeImpersonationStep: %v", err)
	}
	stepRevoked := res.JTIsIn(acme)
	if len(stepRevoked) != 1 || stepRevoked[0] != "imp-own" {
		t.Fatalf("step revoked %v in the request's tenant, want imp-own", stepRevoked)
	}
	lineage := func(caller string) issuedtokenstore.LineageRevocation {
		return issuedtokenstore.LineageRevocation{
			TenantID: acme, CallerJTI: caller, CallerSubject: "bob@acme.com", StepRevokedCallers: stepRevoked,
			Root: issuedtokenstore.IssuedToken{JTI: "bob-b"}, RootReason: "explicit_revoke", ChildReason: "cascade_from_parent", At: now,
		}
	}
	if _, err := store.RevokeLineage(ctx, lineage("bob-d")); !errors.Is(err, issuedtokenstore.ErrCallerRevoked) {
		t.Fatalf("a caller another request revoked: err = %v, want ErrCallerRevoked", err)
	}
	if got := revokedState(t, store, acme, "bob-b"); got["bob-b"] != "live" {
		t.Fatalf("a refused cascade revoked the root: %v", got)
	}
	if _, err := store.RevokeLineage(ctx, lineage("imp-own")); err != nil {
		t.Fatalf("the caller its own step revoked: %v", err)
	}
	if got := revokedState(t, store, acme, "bob-b", "imp-own"); got["bob-b"] != "explicit_revoke" || got["imp-own"] != "cascade_from_parent" {
		t.Errorf("states = %v, want the root revoked and the caller ended by the step", got)
	}
}

// spec: §13.3 (Credential Flow), §4.2 (Session Manager)
// diagnosis: the impersonation step did not revoke the impersonation rows of
// the request's own tenant and of a target tenant that a start record
// names, revoked a row in a tenant that no start record names, or the
// start record was visible or writable from another tenant. Check
// RecordImpersonation and RevokeImpersonationStep in
// pkg/storage/issuedtokenstore and migration 0201.
func TestImpersonationStepFollowsTheStartRecords(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	acme, globex, initech := freshTenant(t, ctx, pg), freshTenant(t, ctx, pg), freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	record(t, issuedtokenstore.IssuedToken{JTI: "bob-b", TenantID: acme, Subject: "bob@acme.com"})
	if err := startImpersonation(t, store, "imp-acme", acme, "alice@acme.com", acme, "bob-b"); err != nil {
		t.Fatalf("RecordImpersonation acme: %v", err)
	}
	if err := startImpersonation(t, store, "imp-globex", globex, "carol@globex.com", acme, "bob-b"); err != nil {
		t.Fatalf("RecordImpersonation globex: %v", err)
	}
	// A row linked to bob-b in initech that no start record names, as a row
	// written without rule 1 (c) would be.
	record(t, issuedtokenstore.IssuedToken{
		JTI: "imp-initech", TenantID: initech, Subject: "dave@initech.com", ActSubject: "bob@acme.com",
		ImpersonatorTenantID: acme, ImpersonatorJTI: "bob-b",
	})
	if got := startRecords(t, pg, acme); len(got) != 2 {
		t.Fatalf("start records in acme = %v, want two", got)
	}
	if got := startRecords(t, pg, globex); len(got) != 0 {
		t.Fatalf("start records visible from globex = %v, want none", got)
	}
	err := pgtenant.InTx(ctx, pg.Pool, globex, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO impersonation_starts (tenant_id, impersonator_jti, target_tenant_id, bearer_jti, exp)
			VALUES ($1, 'x', $2, 'y', now())`, acme, globex)
		return err
	})
	if err == nil {
		t.Fatal("a start record for acme was written under globex's context")
	}

	res, err := store.RevokeImpersonationStep(ctx, issuedtokenstore.ImpersonationStep{
		TenantID: acme, CallerJTI: "bob-b", CallerSubject: "bob@acme.com", RootJTI: "bob-b", Reason: "cascade_from_parent", At: now,
	})
	if err != nil {
		t.Fatalf("RevokeImpersonationStep: %v", err)
	}
	var tenants []string
	for _, r := range res.Revoked {
		tenants = append(tenants, r.TenantID+"/"+r.JTI)
	}
	sort.Strings(tenants)
	want := []string{acme + "/imp-acme", globex + "/imp-globex"}
	sort.Strings(want)
	if len(tenants) != 2 || tenants[0] != want[0] || tenants[1] != want[1] {
		t.Errorf("revoked %v, want %v", tenants, want)
	}
	if got := revokedState(t, store, initech, "imp-initech"); got["imp-initech"] != "live" {
		t.Errorf("a tenant no start record names was updated: %v", got)
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a failure of the impersonation step after it updated the
// request's tenant and a target tenant left either update committed. The
// step's updates must all run in the one outer transaction, which rolls
// back as a whole. Check impersonationStepOnce in
// pkg/storage/issuedtokenstore.
func TestImpersonationStepRollsBackEveryTenantOnFailure(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	acme, globex := freshTenant(t, ctx, pg), freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	record(t, issuedtokenstore.IssuedToken{JTI: "bob-b", TenantID: acme, Subject: "bob@acme.com"})
	if err := startImpersonation(t, store, "imp-acme", acme, "alice@acme.com", acme, "bob-b"); err != nil {
		t.Fatalf("RecordImpersonation acme: %v", err)
	}
	if err := startImpersonation(t, store, "imp-globex", globex, "carol@globex.com", acme, "bob-b"); err != nil {
		t.Fatalf("RecordImpersonation globex: %v", err)
	}
	// A start record naming a target the step cannot switch to, sorted after
	// globex, so the step fails after its first two updates.
	inTenant(t, pg, acme, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO impersonation_starts (tenant_id, impersonator_jti, target_tenant_id, bearer_jti, exp)
			VALUES ($1, 'bob-b', 'zz invalid', 'imp-bad', now() + interval '1 hour')`, acme)
		return err
	})
	_, err := store.RevokeImpersonationStep(ctx, issuedtokenstore.ImpersonationStep{
		TenantID: acme, RootJTI: "bob-b", Reason: "cascade_from_parent", At: now,
	})
	if !errors.Is(err, pgtenant.ErrInvalidTenantID) {
		t.Fatalf("err = %v, want ErrInvalidTenantID", err)
	}
	if got := revokedState(t, store, acme, "imp-acme"); got["imp-acme"] != "live" {
		t.Errorf("the request's tenant kept an update of the failed step: %v", got)
	}
	if got := revokedState(t, store, globex, "imp-globex"); got["imp-globex"] != "live" {
		t.Errorf("the target tenant kept an update of the failed step: %v", got)
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: the impersonation start accepted a caller bearer that step
// (1a) rejects as a subject_token, wrote a start record for a caller
// without a jti, or did not wait for an uncommitted revocation of its
// caller bearer, so an impersonation could be linked to a revoked bearer
// (G3 of change 0169). Check checkCallerBearer and RecordImpersonation in
// pkg/storage/issuedtokenstore.
func TestImpersonationStartReadsItsCallerBearerUnderAShareLock(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	acme, globex := freshTenant(t, ctx, pg), freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	record(t, issuedtokenstore.IssuedToken{JTI: "revoked-b", TenantID: acme, Subject: "bob@acme.com", RevokedAt: now, RevokedReason: "explicit_revoke"})
	record(t, issuedtokenstore.IssuedToken{JTI: "imp-b", TenantID: acme, Subject: "bob@acme.com", ActSubject: "root@acme.com"})
	record(t, issuedtokenstore.IssuedToken{JTI: "live-b", TenantID: acme, Subject: "bob@acme.com"})

	for _, caller := range []string{"revoked-b", "imp-b"} {
		err := startImpersonation(t, store, "from-"+caller, globex, "carol@globex.com", acme, caller)
		var refused *issuedtokenstore.CallerBearerRefusedError
		if !errors.As(err, &refused) {
			t.Errorf("caller %s: err = %v, want CallerBearerRefusedError", caller, err)
		}
		if _, err := store.Get(ctx, globex, "from-"+caller); !errors.Is(err, issuedtokenstore.ErrNotFound) {
			t.Errorf("caller %s: a refused start left a bearer row", caller)
		}
	}
	if got := startRecords(t, pg, acme); len(got) != 0 {
		t.Errorf("refused starts left start records %v", got)
	}
	if err := startImpersonation(t, store, "from-nojti", globex, "carol@globex.com", acme, ""); err != nil {
		t.Fatalf("caller without a jti: %v", err)
	}
	if got := startRecords(t, pg, acme); len(got) != 0 {
		t.Errorf("a caller without a jti wrote start records %v", got)
	}

	// A revocation of live-b that has not committed holds the row lock, so
	// the start waits for it and then reads the bearer revoked.
	revoking := openTenantTx(t, pg, acme)
	if _, err := revoking.Exec(ctx, `UPDATE issued_tokens SET revoked_at = now(), revoked_reason = 'cascade_from_parent' WHERE jti = 'live-b'`); err != nil {
		t.Fatalf("revoke live-b: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- startImpersonation(t, store, "from-live", globex, "carol@globex.com", acme, "live-b") }()
	select {
	case err := <-done:
		t.Fatalf("the start did not wait for the uncommitted revocation of its caller: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := revoking.Commit(ctx); err != nil {
		t.Fatalf("commit revocation: %v", err)
	}
	var refused *issuedtokenstore.CallerBearerRefusedError
	if err := <-done; !errors.As(err, &refused) {
		t.Errorf("start after the revocation committed: err = %v, want CallerBearerRefusedError", err)
	}
}

// spec: §11.4 (User Invalidation), §13.3 (Credential Flow)
// diagnosis: a full revoke of alice left live the child erin minted with an
// unrecorded token of alice's as actor_token, or that child's descendant
// (TOKX-I19, finding F2), or revoked an impersonation row through the
// act_sub seed. Check revokeSubjectWalk in pkg/storage/issuedtokenstore.
func TestFullRevokeReachesTheDelegationsMadeWithATokenWithoutARow(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	acme := freshTenant(t, ctx, pg)
	record(t, issuedtokenstore.IssuedToken{JTI: "erin-own", TenantID: acme, Subject: "erin@acme.com"})
	record(t, issuedtokenstore.IssuedToken{
		JTI: "erin-child", TenantID: acme, Subject: "erin@acme.com",
		ActSubject: "alice@acme.com", ParentJTI: "alice-unrecorded",
	})
	record(t, issuedtokenstore.IssuedToken{JTI: "erin-narrowed", TenantID: acme, Subject: "erin@acme.com", ParentJTI: "erin-child"})
	record(t, issuedtokenstore.IssuedToken{JTI: "imp-by-alice", TenantID: acme, Subject: "erin@acme.com", ActSubject: "alice@acme.com"})

	jtis, err := store.RevokeBySubject(ctx, acme, "alice@acme.com", "full_revoke", time.Now().UTC())
	if err != nil {
		t.Fatalf("RevokeBySubject: %v", err)
	}
	sort.Strings(jtis)
	if len(jtis) != 2 || jtis[0] != "erin-child" || jtis[1] != "erin-narrowed" {
		t.Errorf("revoked %v, want erin-child and erin-narrowed", jtis)
	}
	if got := revokedState(t, store, acme, "erin-own", "imp-by-alice"); got["erin-own"] != "live" || got["imp-by-alice"] != "live" {
		t.Errorf("states = %v, want erin-own and the impersonation row live", got)
	}
}

// spec: §13.3 (Credential Flow), §11.4 (User Invalidation)
// diagnosis: step (1a) of an exchange accepted a subject, actor, or caller
// token whose user row records a hard disable or a full revoke, including
// a caller without an issued_tokens row, or refused a soft-disabled user or
// a user without a row, or reported a user refusal ahead of a revoked row.
// Check recheckSourceUsers in pkg/storage/issuedtokenstore.
func TestExchangeRefusesTheTokensOfAnInvalidatedUser(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	acme := freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	seedUser(t, pg, acme, "alice@acme.com", &now, false)
	seedUser(t, pg, acme, "dave@acme.com", nil, true)
	seedUser(t, pg, acme, "bob@acme.com", nil, false)
	for _, k := range []struct{ jti, sub string }{{"alice-t", "alice@acme.com"}, {"bob-t", "bob@acme.com"}, {"dave-t", "dave@acme.com"}, {"carol-t", "carol@acme.com"}} {
		record(t, issuedtokenstore.IssuedToken{JTI: k.jti, TenantID: acme, Subject: k.sub})
	}
	record(t, issuedtokenstore.IssuedToken{JTI: "alice-revoked", TenantID: acme, Subject: "alice@acme.com", RevokedAt: now, RevokedReason: "explicit_revoke"})
	src := func(role issuedtokenstore.SourceRole, jti, sub string) issuedtokenstore.SourceToken {
		return issuedtokenstore.SourceToken{Role: role, JTI: jti, Subject: sub}
	}
	subj, actor, caller := issuedtokenstore.SourceSubjectToken, issuedtokenstore.SourceActorToken, issuedtokenstore.SourceCallerToken
	cases := []struct {
		name    string
		sources []issuedtokenstore.SourceToken
		want    string
	}{
		{"invalidated subject", []issuedtokenstore.SourceToken{src(subj, "alice-t", "alice@acme.com"), src(caller, "bob-t", "bob@acme.com")}, "subject_user_invalidated"},
		{"invalidated actor", []issuedtokenstore.SourceToken{src(subj, "bob-t", "bob@acme.com"), src(actor, "alice-t", "alice@acme.com")}, "actor_user_invalidated"},
		{"invalidated caller without a row", []issuedtokenstore.SourceToken{src(subj, "bob-t", "bob@acme.com"), src(caller, "", "alice@acme.com")}, "caller_user_invalidated"},
		{"revoked row first", []issuedtokenstore.SourceToken{src(subj, "bob-t", "bob@acme.com"), src(caller, "alice-revoked", "alice@acme.com")}, "caller_token_revoked"},
		{"soft-disabled subject", []issuedtokenstore.SourceToken{src(subj, "dave-t", "dave@acme.com")}, ""},
		{"user without a row", []issuedtokenstore.SourceToken{src(subj, "carol-t", "carol@acme.com")}, ""},
	}
	for i, tc := range cases {
		jti := "minted-" + string(rune('a'+i))
		_, err := store.RecordWithAudit(ctx, issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: acme, Subject: tc.sources[0].Subject, TokenHash: []byte(jti),
			IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: tc.sources[0].JTI,
		}, tc.sources, "token.exchanged", json.RawMessage(`{}`), now)
		var reason string
		var (
			userErr *issuedtokenstore.SourceUserInvalidatedError
			revErr  *issuedtokenstore.SourceRevokedError
		)
		switch {
		case errors.As(err, &userErr):
			reason = userErr.Reason()
		case errors.As(err, &revErr):
			reason = revErr.Reason()
		case err != nil:
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if reason != tc.want {
			t.Errorf("%s: refusal %q, want %q", tc.name, reason, tc.want)
		}
		_, getErr := store.Get(ctx, acme, jti)
		if (tc.want == "") != (getErr == nil) {
			t.Errorf("%s: row present %t, want %t", tc.name, getErr == nil, tc.want == "")
		}
	}
}

// spec: §13.3 (Credential Flow), §12.2 (Storage Roles), §10.2 (Authentication)
// diagnosis: an issued row did not record its lineage depth (the parent's
// depth plus one, 1 under a parent without a row, 0 without a parent), an
// exchange or a rotation whose parent is at the cap was not refused, or
// the playground mint applied the cap. Check recheckExchange and
// recordDepth in pkg/storage/issuedtokenstore.
func TestLineageDepthIsRecordedAndCappedOnExchanges(t *testing.T) {
	t.Parallel()
	pg, base, record := modelFixture(t)
	store := base.WithLimits(issuedtokenstore.Limits{LineageMaxDepth: 2})
	ctx := context.Background()
	acme := freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	record(t, issuedtokenstore.IssuedToken{JTI: "root", TenantID: acme, Subject: "alice@acme.com"})
	mint := func(jti, parent string) error {
		_, err := store.RecordWithAudit(ctx, issuedtokenstore.IssuedToken{
			JTI: jti, TenantID: acme, Subject: "alice@acme.com", TokenHash: []byte(jti),
			IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: parent,
		}, subjectSource(parent), "token.exchanged", json.RawMessage(`{}`), now)
		return err
	}
	depthOf := func(jti string) int {
		row, err := store.Get(ctx, acme, jti)
		if err != nil {
			t.Fatalf("Get %s: %v", jti, err)
		}
		return row.Depth
	}
	for _, c := range []struct {
		jti, parent string
		want        int
	}{{"d1", "root", 1}, {"d2", "d1", 2}, {"under-rowless", "unrecorded", 1}, {"no-parent", "", 0}} {
		if err := mint(c.jti, c.parent); err != nil {
			t.Fatalf("mint %s: %v", c.jti, err)
		}
		if got := depthOf(c.jti); got != c.want {
			t.Errorf("%s depth = %d, want %d", c.jti, got, c.want)
		}
	}
	var depthErr *issuedtokenstore.LineageDepthError
	if err := mint("d3", "d2"); !errors.As(err, &depthErr) || depthErr.Reason() != "lineage_depth_exceeded" || depthErr.Depth != 2 {
		t.Fatalf("exchange under a parent at the cap: err = %v, want LineageDepthError at depth 2", err)
	}
	if _, err := store.Get(ctx, acme, "d3"); !errors.Is(err, issuedtokenstore.ErrNotFound) {
		t.Errorf("a refused exchange left a row")
	}
	_, _, err := store.RecordWithRotationAudit(ctx, issuedtokenstore.IssuedToken{
		JTI: "d2-rotated", TenantID: acme, Subject: "alice@acme.com", TokenHash: []byte("d2-rotated"),
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: "d2",
	}, subjectSource("d2"), "d2", "rotation_replaced", "token.exchanged", json.RawMessage(`{}`), now)
	if !errors.As(err, &depthErr) {
		t.Errorf("rotation of a token at the cap: err = %v, want LineageDepthError", err)
	}
	if err := store.RecordWithSources(ctx, issuedtokenstore.IssuedToken{
		JTI: "pg-bearer", TenantID: acme, Subject: "alice@acme.com", TokenHash: []byte("pg"),
		IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute), ParentJTI: "d2",
	}, subjectSource("d2")); err != nil {
		t.Fatalf("playground mint under a parent at the cap: %v", err)
	}
	if got := depthOf("pg-bearer"); got != 3 {
		t.Errorf("playground bearer depth = %d, want 3", got)
	}
}

// spec: §13.3 (Credential Flow), §11.4 (User Invalidation)
// diagnosis: a cascade or a full revoke's walk that reached the statement
// timeout, or could not take the tenant's audit advisory lock within the
// acquisition timeout, revoked some of its tokens or failed with an error
// other than ErrRevocationTimeout. Check lockForRevocation and
// revocationTimeoutError in pkg/storage/issuedtokenstore.
func TestCascadeAndFullRevokeTimeOutAndRevokeNothing(t *testing.T) {
	t.Parallel()
	pg, base, record := modelFixture(t)
	store := base.WithLimits(issuedtokenstore.Limits{RevocationTimeout: 200 * time.Millisecond})
	ctx := context.Background()
	acme := freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	record(t, issuedtokenstore.IssuedToken{JTI: "root", TenantID: acme, Subject: "alice@acme.com"})
	record(t, issuedtokenstore.IssuedToken{JTI: "child", TenantID: acme, Subject: "alice@acme.com", ParentJTI: "root"})
	cascade := issuedtokenstore.LineageRevocation{
		TenantID: acme, Root: issuedtokenstore.IssuedToken{JTI: "root"}, RootReason: "explicit_revoke", ChildReason: "cascade_from_parent", At: now,
	}

	t.Run("statement timeout", func(t *testing.T) {
		// A transaction holding the child's row lock makes the cascade's
		// update wait past the statement timeout.
		holder := openTenantTx(t, pg, acme)
		if _, err := holder.Exec(ctx, `SELECT 1 FROM issued_tokens WHERE jti = 'child' FOR UPDATE`); err != nil {
			t.Fatalf("lock child: %v", err)
		}
		if _, err := store.RevokeLineage(ctx, cascade); !errors.Is(err, issuedtokenstore.ErrRevocationTimeout) {
			t.Errorf("cascade: err = %v, want ErrRevocationTimeout", err)
		}
		if _, err := store.RevokeBySubject(ctx, acme, "alice@acme.com", "full_revoke", now); !errors.Is(err, issuedtokenstore.ErrRevocationTimeout) {
			t.Errorf("full revoke: err = %v, want ErrRevocationTimeout", err)
		}
		_ = holder.Rollback(ctx)
		if got := revokedState(t, store, acme, "root", "child"); got["root"] != "live" || got["child"] != "live" {
			t.Errorf("a timed-out revocation revoked %v", got)
		}
	})
	t.Run("lock acquisition timeout", func(t *testing.T) {
		holder := openTenantTx(t, pg, acme)
		if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "audit:"+acme); err != nil {
			t.Fatalf("take the advisory lock: %v", err)
		}
		if _, err := store.RevokeLineage(ctx, cascade); !errors.Is(err, issuedtokenstore.ErrRevocationTimeout) {
			t.Errorf("cascade: err = %v, want ErrRevocationTimeout", err)
		}
		_ = holder.Rollback(ctx)
		if got := revokedState(t, store, acme, "root"); got["root"] != "live" {
			t.Errorf("a cascade that could not take the lock revoked %v", got)
		}
	})
}

// spec: §13.3 (Credential Flow), §11.4 (User Invalidation)
// diagnosis: an issuance from a token of a cascade's subtree that held the
// tenant's audit advisory lock when the cascade started committed a row the
// cascade then left live (TOKX-I13, R1 and R2 of change 0169), or an
// issuance after the cascade committed was accepted. The cascade and the
// full revoke's walk must take the lock before they read the subtree.
// Check lockForRevocation in pkg/storage/issuedtokenstore.
func TestCascadeOrdersAfterAnIssuanceHoldingTheLock(t *testing.T) {
	t.Parallel()
	pg, store, record := modelFixture(t)
	ctx := context.Background()
	acme := freshTenant(t, ctx, pg)
	now := time.Now().UTC()
	record(t, issuedtokenstore.IssuedToken{JTI: "root", TenantID: acme, Subject: "alice@acme.com"})
	record(t, issuedtokenstore.IssuedToken{JTI: "alice-k", TenantID: acme, Subject: "alice@acme.com"})

	// insertUnderLock stands in for an issuance between its lock and its
	// commit: it holds the advisory lock and has inserted a child of parent.
	insertUnderLock := func(jti, parent string) pgx.Tx {
		tx := openTenantTx(t, pg, acme)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "audit:"+acme); err != nil {
			t.Fatalf("take the advisory lock: %v", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO issued_tokens (jti, tenant_id, sub, token_hash, issued_at, exp, parent_jti)
			VALUES ($1, $2, 'bob@acme.com', $3, now(), now() + interval '1 hour', $4)`, jti, acme, []byte(jti), parent); err != nil {
			t.Fatalf("insert %s: %v", jti, err)
		}
		return tx
	}
	waitThenCommit := func(tx pgx.Tx, done <-chan error) error {
		select {
		case err := <-done:
			t.Fatalf("the revocation did not wait for the issuance holding the lock: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit issuance: %v", err)
		}
		return <-done
	}

	issuance := insertUnderLock("root-child", "root")
	done := make(chan error, 1)
	go func() {
		_, err := store.RevokeLineage(ctx, issuedtokenstore.LineageRevocation{
			TenantID: acme, Root: issuedtokenstore.IssuedToken{JTI: "root"}, RootReason: "explicit_revoke", ChildReason: "cascade_from_parent", At: now,
		})
		done <- err
	}()
	if err := waitThenCommit(issuance, done); err != nil {
		t.Fatalf("cascade: %v", err)
	}
	if got := revokedState(t, store, acme, "root-child"); got["root-child"] != "cascade_from_parent" {
		t.Errorf("the child committed under the lock = %q, want revoked by the cascade", got["root-child"])
	}

	issuance = insertUnderLock("k-child", "alice-k")
	go func() {
		_, err := store.RevokeBySubject(ctx, acme, "alice@acme.com", "full_revoke", now)
		done <- err
	}()
	if err := waitThenCommit(issuance, done); err != nil {
		t.Fatalf("full revoke: %v", err)
	}
	if got := revokedState(t, store, acme, "k-child"); got["k-child"] != "full_revoke" {
		t.Errorf("the descendant committed under the lock = %q, want revoked by the walk", got["k-child"])
	}

	_, err := store.RecordWithAudit(ctx, issuedtokenstore.IssuedToken{
		JTI: "late", TenantID: acme, Subject: "alice@acme.com", TokenHash: []byte("late"),
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: "root",
	}, subjectSource("root"), "token.exchanged", json.RawMessage(`{}`), now)
	var revErr *issuedtokenstore.SourceRevokedError
	if !errors.As(err, &revErr) {
		t.Errorf("an issuance after the cascade committed: err = %v, want SourceRevokedError", err)
	}
}
