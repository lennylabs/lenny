//go:build component

// SPDX-License-Identifier: MIT

// Component tests for the §13.3 platform-admin impersonation queries of
// issuedtokenstore (the fenced record, the end, the listing, the
// actor revoke, and the step (1a) impersonation refusal) against a real
// Postgres container with the production migrations applied. The
// single-winner guarantee of the end is a Postgres property, so it is
// exercised here rather than against a fake.
package issuedtokencascade_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// impersonationFixture holds the container and store the impersonation
// tests share. issued_tokens.jti is a global primary key, so every seeded
// jti carries a fresh tenant id.
type impersonationFixture struct {
	pg    *containers.Postgres
	store *issuedtokenstore.Store
	ctx   context.Context
}

func newImpersonationFixture(t *testing.T) impersonationFixture {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	return impersonationFixture{pg: pg, store: issuedtokenstore.New(pg.Pool), ctx: context.Background()}
}

// seedUser inserts a user row in tenant, soft-deleted when invalidated,
// which is the marker a hard disable or full revoke sets.
func (f impersonationFixture) seedUser(t *testing.T, tenant, subject string, invalidated bool) {
	t.Helper()
	err := pgtenant.InTx(f.ctx, f.pg.Pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx,
			`INSERT INTO users (tenant_id, subject, deleted_at)
			 VALUES ($1, $2, CASE WHEN $3 THEN now() ELSE NULL END)`,
			tenant, subject, invalidated)
		return err
	})
	if err != nil {
		t.Fatalf("seed user %s/%s: %v", tenant, subject, err)
	}
}

// token returns an issued_tokens row for (tenant, subject) expiring at exp.
func token(jti, tenant, subject, actSub, parent string, exp time.Time) issuedtokenstore.IssuedToken {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: tenant, Subject: subject, TokenHash: []byte(jti),
		Audiences: []string{"lenny-gateway"}, IssuedAt: now, ExpiresAt: exp,
		ActSubject: actSub, ParentJTI: parent,
	}
}

// seed records tok directly, bypassing the fence, and revokes it when
// revoked is true.
func (f impersonationFixture) seed(t *testing.T, tok issuedtokenstore.IssuedToken, revoked bool) {
	t.Helper()
	if err := f.store.Record(f.ctx, tok); err != nil {
		t.Fatalf("Record %s: %v", tok.JTI, err)
	}
	if revoked {
		if err := f.store.Revoke(f.ctx, tok.TenantID, tok.JTI, "explicit_revoke", time.Now().UTC()); err != nil {
			t.Fatalf("Revoke %s: %v", tok.JTI, err)
		}
	}
}

// revokedState reads the row's revoked_reason; ok is false when the row is
// live.
func (f impersonationFixture) revokedState(t *testing.T, tenant, jti string) (string, bool) {
	t.Helper()
	got, err := f.store.Get(f.ctx, tenant, jti)
	if err != nil {
		t.Fatalf("Get %s: %v", jti, err)
	}
	return got.RevokedReason, got.Revoked()
}

// spec: 13.3 (Credential Flow: Platform-admin impersonation, rule 1 (c)), 12.8 (Lifecycle fence and drain), 11.4 (User Invalidation)
// diagnosis: the impersonation record does not read each user row under
// its own tenant's context, does not refuse a user row that records a hard
// disable or full revoke, writes a row despite refusing, or maps a jti
// collision to something other than ErrAlreadyExists. Check
// RecordImpersonation and checkUserNotInvalidated in
// pkg/gateway/storage/issuedtokenstore/impersonation.go.
func TestRecordImpersonationFencesOnUserRows_spec_13_3(t *testing.T) {
	t.Parallel()
	f := newImpersonationFixture(t)
	exp := time.Now().UTC().Add(time.Hour)
	cases := []struct {
		name          string
		adminInvalid  bool
		targetInvalid bool
		seedUsers     bool
		wantRefused   string // "admin", "target", or "" for commit
	}{
		{name: "live users commit", seedUsers: true},
		{name: "missing users commit", seedUsers: false},
		{name: "invalidated target is refused", seedUsers: true, targetInvalid: true, wantRefused: "target"},
		// The admin's user row lives in the caller tenant only; read under
		// the target tenant it would be absent and pass.
		{name: "invalidated admin is refused", seedUsers: true, adminInvalid: true, wantRefused: "admin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			callerTenant := freshTenant(t, f.ctx, f.pg)
			targetTenant := freshTenant(t, f.ctx, f.pg)
			if tc.seedUsers {
				f.seedUser(t, callerTenant, "alice", tc.adminInvalid)
				f.seedUser(t, targetTenant, "bob", tc.targetInvalid)
			}
			tok := token("imp-"+targetTenant, targetTenant, "bob", "alice", "", exp)
			err := f.store.RecordImpersonation(f.ctx, tok, callerTenant)
			_, getErr := f.store.Get(f.ctx, targetTenant, tok.JTI)
			if tc.wantRefused == "" {
				if err != nil || getErr != nil {
					t.Fatalf("RecordImpersonation = %v, Get = %v; want the row committed", err, getErr)
				}
				return
			}
			var inv *issuedtokenstore.UserInvalidatedError
			if !errors.As(err, &inv) {
				t.Fatalf("RecordImpersonation = %v, want *UserInvalidatedError", err)
			}
			wantTenant, wantSub := targetTenant, "bob"
			if tc.wantRefused == "admin" {
				wantTenant, wantSub = callerTenant, "alice"
			}
			if inv.TenantID != wantTenant || inv.Subject != wantSub {
				t.Errorf("refusal names %s/%s, want %s/%s", inv.TenantID, inv.Subject, wantTenant, wantSub)
			}
			if !errors.Is(getErr, issuedtokenstore.ErrNotFound) {
				t.Errorf("refused record left a row (Get err %v)", getErr)
			}
		})
	}

	t.Run("jti collision is ErrAlreadyExists", func(t *testing.T) {
		callerTenant := freshTenant(t, f.ctx, f.pg)
		targetTenant := freshTenant(t, f.ctx, f.pg)
		tok := token("imp-dup-"+targetTenant, targetTenant, "bob", "alice", "", exp)
		if err := f.store.RecordImpersonation(f.ctx, tok, callerTenant); err != nil {
			t.Fatalf("first RecordImpersonation: %v", err)
		}
		if err := f.store.RecordImpersonation(f.ctx, tok, callerTenant); !errors.Is(err, issuedtokenstore.ErrAlreadyExists) {
			t.Fatalf("second RecordImpersonation = %v, want ErrAlreadyExists", err)
		}
	})
}

// spec: 13.3 (Credential Flow: Platform-admin impersonation, rules 2 and 3)
// diagnosis: more than one concurrent end revoked the row, the end did not
// stamp impersonation_ended, a miss was misclassified, or the end reached
// a row outside the impersonation predicate (an ordinary token or a
// delegation child). Check EndImpersonation in
// pkg/gateway/storage/issuedtokenstore/impersonation.go.
func TestEndImpersonationSingleWinnerAndPredicate_spec_13_3(t *testing.T) {
	t.Parallel()
	f := newImpersonationFixture(t)
	exp := time.Now().UTC().Add(time.Hour)

	t.Run("exactly one concurrent end wins", func(t *testing.T) {
		tenant := freshTenant(t, f.ctx, f.pg)
		jti := "imp-race-" + tenant
		f.seed(t, token(jti, tenant, "bob", "alice", "", exp), false)
		const callers = 8
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			winners int
			others  []error
		)
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tok, err := f.store.EndImpersonation(f.ctx, jti, time.Now().UTC())
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					winners++
					if tok.JTI != jti || tok.ActSubject != "alice" || !tok.Revoked() {
						others = append(others, errors.New("winner row does not describe the revoked session"))
					}
					return
				}
				others = append(others, err)
			}()
		}
		wg.Wait()
		if winners != 1 {
			t.Fatalf("winners = %d, want 1 (errors %v)", winners, others)
		}
		for _, err := range others {
			var na *issuedtokenstore.NotActiveError
			if !errors.As(err, &na) || na.State != issuedtokenstore.StateRevoked {
				t.Errorf("loser err = %v, want NotActiveError revoked", err)
			}
		}
		if reason, revoked := f.revokedState(t, tenant, jti); !revoked || reason != "impersonation_ended" {
			t.Errorf("row revoked=%v reason=%q, want impersonation_ended", revoked, reason)
		}
	})

	t.Run("misses are classified and touch nothing outside the predicate", func(t *testing.T) {
		tenant := freshTenant(t, f.ctx, f.pg)
		expired := "imp-exp-" + tenant
		ordinary := "ord-" + tenant
		child := "child-" + tenant
		f.seed(t, token(expired, tenant, "bob", "alice", "", time.Now().UTC().Add(-time.Minute)), false)
		f.seed(t, token(ordinary, tenant, "bob", "", "", exp), false)
		f.seed(t, token(child, tenant, "bob", "alice", ordinary, exp), false)

		_, err := f.store.EndImpersonation(f.ctx, expired, time.Now().UTC())
		var na *issuedtokenstore.NotActiveError
		if !errors.As(err, &na) || na.State != issuedtokenstore.StateExpired {
			t.Errorf("end of expired row = %v, want NotActiveError expired", err)
		}
		for _, jti := range []string{ordinary, child, "unknown-" + tenant} {
			if _, err := f.store.EndImpersonation(f.ctx, jti, time.Now().UTC()); !errors.Is(err, issuedtokenstore.ErrNotFound) {
				t.Errorf("end of %s = %v, want ErrNotFound", jti, err)
			}
		}
		for _, jti := range []string{expired, ordinary, child} {
			if _, revoked := f.revokedState(t, tenant, jti); revoked {
				t.Errorf("%s was revoked by a missed end", jti)
			}
		}
	})
}

// spec: 13.3 (Credential Flow: Platform-admin impersonation, rules 2, 6, and 7)
// diagnosis: the listing returned a row outside the active impersonation
// set or missed one in another tenant, or the actor revoke reached a
// delegation child, another admin's bearer, or an inactive row. Check
// ListActiveImpersonations and RevokeImpersonationsByActor in
// pkg/gateway/storage/issuedtokenstore/impersonation.go.
func TestListAndActorRevokeMatchActiveImpersonationRowsAcrossTenants_spec_13_3(t *testing.T) {
	t.Parallel()
	f := newImpersonationFixture(t)
	exp := time.Now().UTC().Add(time.Hour)
	t1 := freshTenant(t, f.ctx, f.pg)
	t2 := freshTenant(t, f.ctx, f.pg)
	liveA1 := "imp-a1-" + t1
	liveA2 := "imp-a2-" + t2
	liveCarol := "imp-c-" + t1
	f.seed(t, token(liveA1, t1, "bob", "alice", "", exp), false)
	time.Sleep(2 * time.Millisecond) // distinct issued_at for the ordering check
	f.seed(t, token(liveA2, t2, "dave", "alice", "", exp), false)
	f.seed(t, token(liveCarol, t1, "bob", "carol", "", exp), false)
	f.seed(t, token("imp-rev-"+t1, t1, "bob", "alice", "", exp), true)
	f.seed(t, token("imp-exp-"+t2, t2, "dave", "alice", "", time.Now().UTC().Add(-time.Minute)), false)
	f.seed(t, token("ord-"+t1, t1, "bob", "", "", exp), false)
	f.seed(t, token("child-"+t1, t1, "bob", "alice", "ord-"+t1, exp), false)

	listed, err := f.store.ListActiveImpersonations(f.ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListActiveImpersonations: %v", err)
	}
	var got []string
	for _, tok := range listed {
		got = append(got, tok.JTI)
	}
	if want := []string{liveA1, liveA2, liveCarol}; !slices.Equal(got, want) {
		t.Fatalf("listed %v, want %v in issued_at order", got, want)
	}

	revoked, err := f.store.RevokeImpersonationsByActor(f.ctx, "alice", "user_invalidated", time.Now().UTC())
	if err != nil {
		t.Fatalf("RevokeImpersonationsByActor: %v", err)
	}
	slices.Sort(revoked)
	want := []string{liveA1, liveA2}
	slices.Sort(want)
	if !slices.Equal(revoked, want) {
		t.Fatalf("actor revoke returned %v, want %v", revoked, want)
	}
	for _, jti := range []string{liveCarol, "child-" + t1, "ord-" + t1} {
		if _, isRevoked := f.revokedState(t, t1, jti); isRevoked {
			t.Errorf("actor revoke reached %s", jti)
		}
	}
	if reason, ok := f.revokedState(t, t2, liveA2); !ok || reason != "user_invalidated" {
		t.Errorf("%s revoked=%v reason=%q, want user_invalidated", liveA2, ok, reason)
	}
	again, err := f.store.RevokeImpersonationsByActor(f.ctx, "alice", "user_invalidated", time.Now().UTC())
	if err != nil || len(again) != 0 {
		t.Errorf("repeat actor revoke = %v, %v; want nothing revoked", again, err)
	}
}

// spec: 13.3 (Credential Flow: Write-before-issue ordering, step (1a); Platform-admin impersonation, rule 5)
// diagnosis: the issuance transaction minted from a live impersonation
// bearer, or reported the impersonation before a revoked source. Check
// recheckSources and selectSourceStates in
// pkg/gateway/storage/issuedtokenstore/recheck.go.
func TestIssuanceRefusesImpersonationSource_spec_13_3(t *testing.T) {
	t.Parallel()
	f := newImpersonationFixture(t)
	exp := time.Now().UTC().Add(time.Hour)
	cases := []struct {
		name         string
		actorRevoked bool
		wantRevoked  bool
		wantRole     issuedtokenstore.SourceRole
	}{
		{name: "live impersonation subject", wantRole: issuedtokenstore.SourceSubjectToken},
		{name: "revoked actor outranks impersonation subject", actorRevoked: true, wantRevoked: true, wantRole: issuedtokenstore.SourceActorToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := freshTenant(t, f.ctx, f.pg)
			subj, actor := "imp-subj-"+tenant, "actor-"+tenant
			f.seed(t, token(subj, tenant, "bob", "alice", "", exp), false)
			f.seed(t, token(actor, tenant, "bob", "", "", exp), tc.actorRevoked)
			issuedBefore, _ := tenantRowCounts(t, f.ctx, f.pg, tenant)
			_, err := f.store.RecordWithAudit(f.ctx, token("minted-"+tenant, tenant, "bob", "", "", exp),
				[]issuedtokenstore.SourceToken{
					{Role: issuedtokenstore.SourceSubjectToken, JTI: subj},
					{Role: issuedtokenstore.SourceActorToken, JTI: actor},
				}, "token.exchanged", []byte(`{"policy_result":"accepted"}`), time.Now().UTC())
			if tc.wantRevoked {
				var re *issuedtokenstore.SourceRevokedError
				if !errors.As(err, &re) || re.Role != tc.wantRole {
					t.Fatalf("err = %v, want SourceRevokedError for %s", err, tc.wantRole)
				}
			} else {
				var ie *issuedtokenstore.SourceImpersonationError
				if !errors.As(err, &ie) || ie.Role != tc.wantRole || ie.Reason() != "subject_token_impersonation" {
					t.Fatalf("err = %v, want SourceImpersonationError for %s", err, tc.wantRole)
				}
			}
			if issuedAfter, exchanged := tenantRowCounts(t, f.ctx, f.pg, tenant); issuedAfter != issuedBefore || exchanged != 0 {
				t.Errorf("refused issuance committed issued=%d exchanged=%d, want none", issuedAfter-issuedBefore, exchanged)
			}
		})
	}
}
