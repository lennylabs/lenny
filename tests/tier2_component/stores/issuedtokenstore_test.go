//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.2 TokenIssuanceStore, exercising
// pkg/gateway/issuedtokenstore against a real Postgres container with
// the production migrations applied. Covers record/get round-trip,
// the JTI uniqueness guard, cross-tenant isolation, revocation,
// revoked-token listing, expired-row GC, and the §13.3 platform-admin
// impersonation queries.
package stores_test

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 12.2
// diagnosis: the Postgres-backed TokenIssuanceStore in
// pkg/gateway/issuedtokenstore did not behave as specified. Record and
// Get must round-trip a token, a duplicate JTI must be rejected,
// cross-tenant Get must return ErrNotFound, Revoke must mark the
// token, ListRevoked must return revoked tokens ordered by revoked_at,
// and DeleteExpired must remove only past-expiry rows.
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

	t.Run("audiences list and dialect cap round-trip", func(t *testing.T) {
		// spec: §4.3, migration 0058.
		// Multi-valued audience set MUST be stored on the audiences[]
		// column so forensic queries can match individual audiences.
		// The dialect cap applied to the issued exp MUST be stored on
		// dialect_cap_applied_seconds so an operator can later answer
		// "why did this token live exactly Nh".
		tenant := freshTenant(t, ctx, pg)
		ts := time.Now().UTC().Truncate(time.Microsecond)
		want := issuedtokenstore.IssuedToken{
			JTI:                      "jti-" + newUUID(t),
			TenantID:                 tenant,
			Subject:                  "alice@acme.com",
			TokenHash:                []byte{0x10, 0x11},
			Scope:                    []string{"sessions:read"},
			Audience:                 "lenny-gateway lenny-ops",
			Audiences:                []string{"lenny-gateway", "lenny-ops"},
			DialectCapAppliedSeconds: 86400, // §13.3 lenny-gateway: 24h cap.
			IssuedAt:                 ts,
			ExpiresAt:                ts.Add(24 * time.Hour),
		}
		if err := store.Record(ctx, want); err != nil {
			t.Fatalf("Record: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.JTI)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !slices.Equal(got.Audiences, want.Audiences) {
			t.Errorf("Audiences = %v, want %v", got.Audiences, want.Audiences)
		}
		if got.DialectCapAppliedSeconds != want.DialectCapAppliedSeconds {
			t.Errorf("DialectCapAppliedSeconds = %d, want %d",
				got.DialectCapAppliedSeconds, want.DialectCapAppliedSeconds)
		}
		// Forensic reverse lookup must work: a row written with
		// "lenny-ops" in audiences[] must surface on a query that
		// filters by that audience. The audiences[] column carries a
		// GIN index from migration 0058.
		var hits int
		row := pg.Pool.QueryRow(ctx,
			`SELECT count(*) FROM issued_tokens
			 WHERE tenant_id = $1 AND $2 = ANY(audiences)`,
			tenant, "lenny-ops")
		if err := row.Scan(&hits); err != nil {
			t.Fatalf("ANY(audiences) lookup: %v", err)
		}
		if hits != 1 {
			t.Errorf("audiences ANY lookup hits = %d, want 1", hits)
		}
	})

	t.Run("audiences list is back-filled from legacy single audience", func(t *testing.T) {
		// spec: §4.3, migration 0058.
		// A caller that supplies only the legacy single-valued
		// Audience field still gets the audiences[] column populated
		// from the writer-side fallback (audienceListArg). Single
		// values like "lenny-gateway" land as ["lenny-gateway"].
		tenant := freshTenant(t, ctx, pg)
		ts := time.Now().UTC().Truncate(time.Microsecond)
		tok := issuedtokenstore.IssuedToken{
			JTI:       "jti-" + newUUID(t),
			TenantID:  tenant,
			Subject:   "alice@acme.com",
			TokenHash: []byte{0x12},
			Scope:     []string{"sessions:read"},
			Audience:  "lenny-gateway",
			// Audiences intentionally nil — the writer must populate
			// it from the legacy single value.
			IssuedAt:  ts,
			ExpiresAt: ts.Add(time.Hour),
		}
		if err := store.Record(ctx, tok); err != nil {
			t.Fatalf("Record: %v", err)
		}
		got, err := store.Get(ctx, tenant, tok.JTI)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		want := []string{"lenny-gateway"}
		if !slices.Equal(got.Audiences, want) {
			t.Errorf("Audiences = %v, want %v (back-filled from legacy Audience)",
				got.Audiences, want)
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

// impEnv is one migrated Postgres with the issued-token store and the user
// store over one pool, as cmd/lenny-gateway builds them, shared by the
// §13.3 platform-admin impersonation cases. Every impersonation row a case
// writes goes through RecordImpersonation, which the impersonation writer
// invariant checks at the end.
type impEnv struct {
	pg       *containers.Postgres
	store    *issuedtokenstore.Store
	users    *userpg.Store
	ctx      context.Context
	rng      *rand.Rand
	mu       sync.Mutex
	recorded map[string]bool
}

func newImpEnv(t *testing.T) *impEnv {
	t.Helper()
	_, pg := startStore(t)
	seed := time.Now().UnixNano()
	t.Logf("interleaving seed %d", seed)
	return &impEnv{
		pg: pg, store: issuedtokenstore.New(pg.Pool), users: userpg.New(pg.Pool),
		ctx: context.Background(), rng: rand.New(rand.NewPCG(uint64(seed), 0x13)),
		recorded: map[string]bool{},
	}
}

// jitter returns a random start delay of up to three milliseconds.
func (e *impEnv) jitter() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return time.Duration(e.rng.Int64N(int64(3 * time.Millisecond)))
}

// impToken returns an impersonation row for target (tenant, subject)
// minted by actor, expiring at exp. issued_tokens.jti is a global primary
// key, so every jti carries a fresh UUID.
func impToken(t *testing.T, tenant, subject, actor string, exp time.Time) issuedtokenstore.IssuedToken {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	jti := "imp-" + newUUID(t)
	return issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: tenant, Subject: subject, ActSubject: actor, TokenHash: []byte(jti),
		Audiences: []string{"lenny-gateway"}, IssuedAt: now, ExpiresAt: exp,
	}
}

// plainToken returns a non-impersonation row: an ordinary bearer when
// parent is empty, a delegation child otherwise.
func plainToken(t *testing.T, tenant, subject, actor, parent string) issuedtokenstore.IssuedToken {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	jti := "tok-" + newUUID(t)
	return issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: tenant, Subject: subject, ActSubject: actor, ParentJTI: parent, TokenHash: []byte(jti),
		Audiences: []string{"lenny-gateway"}, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
}

// record writes tok through the fenced impersonation record and notes the
// jti for the writer invariant.
func (e *impEnv) record(t *testing.T, tok issuedtokenstore.IssuedToken, callerTenant string) error {
	t.Helper()
	err := e.store.RecordImpersonation(e.ctx, tok, callerTenant)
	if err == nil {
		e.mu.Lock()
		e.recorded[tok.JTI] = true
		e.mu.Unlock()
	}
	return err
}

// mustRecord records tok and fails the test on error. A caller tenant with
// no users row lets the fence pass.
func (e *impEnv) mustRecord(t *testing.T, tok issuedtokenstore.IssuedToken) issuedtokenstore.IssuedToken {
	t.Helper()
	if err := e.record(t, tok, freshTenant(t, e.ctx, e.pg)); err != nil {
		t.Fatalf("RecordImpersonation %s: %v", tok.JTI, err)
	}
	return tok
}

func (e *impEnv) mustRecordPlain(t *testing.T, tok issuedtokenstore.IssuedToken) issuedtokenstore.IssuedToken {
	t.Helper()
	if err := e.store.Record(e.ctx, tok); err != nil {
		t.Fatalf("Record %s: %v", tok.JTI, err)
	}
	return tok
}

func (e *impEnv) revoke(t *testing.T, tok issuedtokenstore.IssuedToken) {
	t.Helper()
	if err := e.store.Revoke(e.ctx, tok.TenantID, tok.JTI, "explicit_revoke", time.Now().UTC()); err != nil {
		t.Fatalf("Revoke %s: %v", tok.JTI, err)
	}
}

func (e *impEnv) addUser(t *testing.T, tenant, subject string) {
	t.Helper()
	if err := e.users.Create(e.ctx, userstore.User{TenantID: tenant, Subject: subject}); err != nil {
		t.Fatalf("seed user %s/%s: %v", tenant, subject, err)
	}
}

// invalidate writes the full-revoke invalidation level on the user row
// through the production user store, as §11.4 does before step 1.
func (e *impEnv) invalidate(tenant, subject string) error {
	_, err := e.users.Update(e.ctx, tenant, subject, func(u *userstore.User) error {
		u.DeletedAt = time.Now().UTC()
		return nil
	})
	return err
}

// row reads the issued_tokens row of jti in tenant; ok is false when no
// row exists.
func (e *impEnv) row(t *testing.T, tenant, jti string) (issuedtokenstore.IssuedToken, bool) {
	t.Helper()
	tok, err := e.store.Get(e.ctx, tenant, jti)
	if errors.Is(err, issuedtokenstore.ErrNotFound) {
		return issuedtokenstore.IssuedToken{}, false
	}
	if err != nil {
		t.Fatalf("Get %s: %v", jti, err)
	}
	return tok, true
}

// tenantCounts returns the tenant's issued_tokens rows and token.exchanged
// audit rows.
func (e *impEnv) tenantCounts(t *testing.T, tenant string) (issued, exchanged int) {
	t.Helper()
	if err := e.pg.Pool.QueryRow(e.ctx,
		`SELECT count(*) FROM issued_tokens WHERE tenant_id = $1`, tenant).Scan(&issued); err != nil {
		t.Fatalf("count issued_tokens: %v", err)
	}
	if err := e.pg.Pool.QueryRow(e.ctx,
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND event_type = 'token.exchanged'`, tenant).Scan(&exchanged); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	return issued, exchanged
}

// awaitUserShareLockWaiter polls pg_stat_activity until a backend of this
// database waits on a lock while running the fence's FOR SHARE read of a
// users row. It fails when the deadline passes or when the record returns
// on done before ever blocking.
func (e *impEnv) awaitUserShareLockWaiter(t *testing.T, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(rowLockWaitDeadline)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("RecordImpersonation returned %v without waiting for the users row lock", err)
		default:
		}
		var n int
		if err := e.pg.Pool.QueryRow(e.ctx,
			`SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'
			   AND query LIKE '%FROM users%FOR SHARE%'`).Scan(&n); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("RecordImpersonation never waited on the users row lock")
}

// TestImpersonationIssuedTokensContract_spec_13_3 exercises the §13.3
// platform-admin impersonation queries of issuedtokenstore against a real
// Postgres: the end, its single winner across concurrent callers and
// against the full-revoke revocations, the listing, the actor revoke, the
// rule 1 (c) recording fence, RLS on the rows, and the step (1a) refusal of
// an impersonation bearer as a source. The single-winner and fence
// guarantees are Postgres properties, so a fake-backed test cannot make
// these assertions.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 13.3 (Credential Flow: Write-before-issue ordering), 12.2 (Storage Roles), 11.4 (User Invalidation), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a subtest names the query that broke. EndImpersonation,
// ListActiveImpersonations, RevokeImpersonationsByActor, and
// RecordImpersonation are in
// pkg/gateway/storage/issuedtokenstore/impersonation.go; the step (1a)
// re-read is recheckSources in pkg/gateway/storage/issuedtokenstore/recheck.go.
// A single-winner failure means the end's UPDATE is not conditional on
// revoked_at IS NULL, a fence failure means a users row is not read FOR
// SHARE under its own tenant inside the insert's transaction, and an
// invariant failure means a path other than RecordImpersonation writes a
// row with act_sub set and no parent_jti.
func TestImpersonationIssuedTokensContract_spec_13_3(t *testing.T) {
	t.Parallel()
	e := newImpEnv(t)
	hour := time.Now().UTC().Add(time.Hour)

	t.Run("end revokes once and classifies every miss", func(t *testing.T) {
		tenant := freshTenant(t, e.ctx, e.pg)
		live := e.mustRecord(t, impToken(t, tenant, "bob", "alice", hour))
		expired := e.mustRecord(t, impToken(t, tenant, "bob", "alice", time.Now().UTC().Add(-time.Minute)))
		ordinary := e.mustRecordPlain(t, plainToken(t, tenant, "bob", "", ""))
		child := e.mustRecordPlain(t, plainToken(t, tenant, "bob", "alice", ordinary.JTI))

		got, err := e.store.EndImpersonation(e.ctx, live.JTI, time.Now().UTC())
		if err != nil || got.JTI != live.JTI || got.ActSubject != "alice" || got.RevokedReason != "impersonation_ended" {
			t.Fatalf("first end = %+v, %v; want the row revoked with impersonation_ended", got, err)
		}
		var na *issuedtokenstore.NotActiveError
		if _, err := e.store.EndImpersonation(e.ctx, live.JTI, time.Now().UTC()); !errors.As(err, &na) || na.State != issuedtokenstore.StateRevoked {
			t.Errorf("retry = %v, want NotActiveError revoked", err)
		}
		if _, err := e.store.EndImpersonation(e.ctx, expired.JTI, time.Now().UTC()); !errors.As(err, &na) || na.State != issuedtokenstore.StateExpired {
			t.Errorf("end of expired row = %v, want NotActiveError expired", err)
		}
		for _, jti := range []string{ordinary.JTI, child.JTI, "imp-unknown-" + tenant} {
			if _, err := e.store.EndImpersonation(e.ctx, jti, time.Now().UTC()); !errors.Is(err, issuedtokenstore.ErrNotFound) {
				t.Errorf("end of %s = %v, want ErrNotFound", jti, err)
			}
		}
		for _, tok := range []issuedtokenstore.IssuedToken{expired, ordinary, child} {
			if row, _ := e.row(t, tenant, tok.JTI); row.Revoked() {
				t.Errorf("%s was revoked by a missed end", tok.JTI)
			}
		}
	})

	t.Run("one of N jittered concurrent ends wins", func(t *testing.T) {
		const callers = 12
		for round := range 4 {
			tenant := freshTenant(t, e.ctx, e.pg)
			tok := e.mustRecord(t, impToken(t, tenant, "bob", "alice", hour))
			base := time.Now().UTC().Truncate(time.Microsecond)
			var (
				wg      sync.WaitGroup
				mu      sync.Mutex
				winners []time.Time
				losers  []error
			)
			for i := range callers {
				at := base.Add(time.Duration(i) * time.Millisecond)
				delay := e.jitter()
				wg.Go(func() {
					time.Sleep(delay)
					_, err := e.store.EndImpersonation(e.ctx, tok.JTI, at)
					mu.Lock()
					defer mu.Unlock()
					if err == nil {
						winners = append(winners, at)
						return
					}
					losers = append(losers, err)
				})
			}
			wg.Wait()
			if len(winners) != 1 {
				t.Fatalf("round %d: %d winners, want 1 (losers %v)", round, len(winners), losers)
			}
			for _, err := range losers {
				if !errors.Is(err, issuedtokenstore.ErrNotActive) {
					t.Errorf("round %d: loser err = %v, want ErrNotActive", round, err)
				}
			}
			if row, _ := e.row(t, tenant, tok.JTI); !row.RevokedAt.Equal(winners[0]) {
				t.Errorf("round %d: revoked_at = %s, want the winner's %s", round, row.RevokedAt, winners[0])
			}
		}
	})

	t.Run("end, subject revoke, and actor revoke interleave to one revocation", func(t *testing.T) {
		for round := range 16 {
			tenant := freshTenant(t, e.ctx, e.pg)
			actor := "admin-" + newUUID(t)
			tok := e.mustRecord(t, impToken(t, tenant, "bob", actor, hour))
			base := time.Now().UTC().Truncate(time.Microsecond)
			type outcome struct {
				name   string
				at     time.Time
				reason string
				won    bool
				err    error
			}
			results := make([]outcome, 3)
			ops := []func(at time.Time) (bool, error){
				func(at time.Time) (bool, error) {
					_, err := e.store.EndImpersonation(e.ctx, tok.JTI, at)
					if errors.Is(err, issuedtokenstore.ErrNotActive) {
						return false, nil
					}
					return err == nil, err
				},
				func(at time.Time) (bool, error) {
					jtis, err := e.store.RevokeBySubject(e.ctx, tenant, "bob", "user_invalidated", at)
					return slices.Contains(jtis, tok.JTI), err
				},
				func(at time.Time) (bool, error) {
					jtis, err := e.store.RevokeImpersonationsByActor(e.ctx, actor, "actor_invalidated", at)
					return slices.Contains(jtis, tok.JTI), err
				},
			}
			names := []string{"end", "subject revoke", "actor revoke"}
			reasons := []string{"impersonation_ended", "user_invalidated", "actor_invalidated"}
			var wg sync.WaitGroup
			for i, op := range ops {
				at := base.Add(time.Duration(i+1) * time.Millisecond)
				delay := e.jitter()
				wg.Go(func() {
					time.Sleep(delay)
					won, err := op(at)
					results[i] = outcome{name: names[i], at: at, reason: reasons[i], won: won, err: err}
				})
			}
			wg.Wait()
			var won []outcome
			for _, r := range results {
				if r.err != nil {
					t.Fatalf("round %d: %s: %v", round, r.name, r.err)
				}
				if r.won {
					won = append(won, r)
				}
			}
			if len(won) != 1 {
				t.Fatalf("round %d: %d operations revoked the row, want 1 (%+v)", round, len(won), results)
			}
			row, _ := e.row(t, tenant, tok.JTI)
			if !row.RevokedAt.Equal(won[0].at) || row.RevokedReason != won[0].reason {
				t.Errorf("round %d: row revoked_at=%s reason=%q, want the first committer %s (%s, %s)",
					round, row.RevokedAt, row.RevokedReason, won[0].name, won[0].at, won[0].reason)
			}
		}
	})

	t.Run("listing and actor revoke match active impersonation rows across tenants", func(t *testing.T) {
		t1 := freshTenant(t, e.ctx, e.pg)
		t2 := freshTenant(t, e.ctx, e.pg)
		actor := "admin-" + newUUID(t)
		other := "admin-" + newUUID(t)
		liveA1 := e.mustRecord(t, impToken(t, t1, "bob", actor, hour))
		liveA2 := e.mustRecord(t, impToken(t, t2, "dave", actor, hour))
		liveOther := e.mustRecord(t, impToken(t, t1, "bob", other, hour))
		revoked := e.mustRecord(t, impToken(t, t1, "bob", actor, hour))
		e.revoke(t, revoked)
		expired := e.mustRecord(t, impToken(t, t2, "dave", actor, time.Now().UTC().Add(-time.Minute)))
		ordinary := e.mustRecordPlain(t, plainToken(t, t1, "bob", "", ""))
		child := e.mustRecordPlain(t, plainToken(t, t1, "bob", actor, ordinary.JTI))

		listed, err := e.store.ListActiveImpersonations(e.ctx, time.Now().UTC())
		if err != nil {
			t.Fatalf("ListActiveImpersonations: %v", err)
		}
		inList := map[string]bool{}
		for _, tok := range listed {
			inList[tok.JTI] = true
		}
		for _, tok := range []issuedtokenstore.IssuedToken{liveA1, liveA2, liveOther} {
			if !inList[tok.JTI] {
				t.Errorf("listing misses active row %s in %s", tok.JTI, tok.TenantID)
			}
		}
		for _, tok := range []issuedtokenstore.IssuedToken{revoked, expired, ordinary, child} {
			if inList[tok.JTI] {
				t.Errorf("listing returned inactive or non-impersonation row %s", tok.JTI)
			}
		}
		if !slices.IsSortedFunc(listed, func(a, b issuedtokenstore.IssuedToken) int { return a.IssuedAt.Compare(b.IssuedAt) }) {
			t.Error("listing is not ordered by issued_at")
		}

		got, err := e.store.RevokeImpersonationsByActor(e.ctx, actor, "user_invalidated", time.Now().UTC())
		if err != nil {
			t.Fatalf("RevokeImpersonationsByActor: %v", err)
		}
		slices.Sort(got)
		want := []string{liveA1.JTI, liveA2.JTI}
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("actor revoke returned %v, want %v", got, want)
		}
		for _, tok := range []issuedtokenstore.IssuedToken{liveOther, child, ordinary, expired} {
			if row, _ := e.row(t, tok.TenantID, tok.JTI); row.Revoked() {
				t.Errorf("actor revoke reached %s", tok.JTI)
			}
		}
		if again, err := e.store.RevokeImpersonationsByActor(e.ctx, actor, "user_invalidated", time.Now().UTC()); err != nil || len(again) != 0 {
			t.Errorf("repeat actor revoke = %v, %v; want nothing revoked", again, err)
		}
	})

	t.Run("recording fence states", func(t *testing.T) {
		cases := []struct {
			name        string
			prepare     func(t *testing.T, callerTenant, targetTenant string)
			wantRefused string // "admin", "target", or "" for a commit
		}{
			{name: "missing users rows pass", prepare: func(*testing.T, string, string) {}},
			{name: "soft-disabled users pass", prepare: func(t *testing.T, callerTenant, targetTenant string) {
				for _, u := range [][2]string{{callerTenant, "alice"}, {targetTenant, "bob"}} {
					e.addUser(t, u[0], u[1])
					if _, err := e.users.Update(e.ctx, u[0], u[1], func(x *userstore.User) error { x.Disabled = true; return nil }); err != nil {
						t.Fatalf("soft disable %s: %v", u[1], err)
					}
				}
			}},
			{name: "hard-disabled target is refused", wantRefused: "target", prepare: func(t *testing.T, callerTenant, targetTenant string) {
				e.addUser(t, callerTenant, "alice")
				e.addUser(t, targetTenant, "bob")
				if err := e.users.SoftDelete(e.ctx, targetTenant, "bob", time.Now().UTC()); err != nil {
					t.Fatalf("hard disable: %v", err)
				}
			}},
			{name: "hard-disabled admin is refused", wantRefused: "admin", prepare: func(t *testing.T, callerTenant, targetTenant string) {
				e.addUser(t, callerTenant, "alice")
				e.addUser(t, targetTenant, "bob")
				if err := e.users.SoftDelete(e.ctx, callerTenant, "alice", time.Now().UTC()); err != nil {
					t.Fatalf("hard disable: %v", err)
				}
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				callerTenant := freshTenant(t, e.ctx, e.pg)
				targetTenant := freshTenant(t, e.ctx, e.pg)
				tc.prepare(t, callerTenant, targetTenant)
				tok := impToken(t, targetTenant, "bob", "alice", hour)
				err := e.record(t, tok, callerTenant)
				_, exists := e.row(t, targetTenant, tok.JTI)
				if tc.wantRefused == "" {
					if err != nil || !exists {
						t.Fatalf("RecordImpersonation = %v, row exists = %v; want a commit", err, exists)
					}
					return
				}
				var inv *issuedtokenstore.UserInvalidatedError
				if !errors.As(err, &inv) || exists {
					t.Fatalf("RecordImpersonation = %v, row exists = %v; want *UserInvalidatedError and no row", err, exists)
				}
				wantTenant, wantSub := targetTenant, "bob"
				if tc.wantRefused == "admin" {
					wantTenant, wantSub = callerTenant, "alice"
				}
				if inv.TenantID != wantTenant || inv.Subject != wantSub {
					t.Errorf("refusal names %s/%s, want %s/%s", inv.TenantID, inv.Subject, wantTenant, wantSub)
				}
			})
		}
		t.Run("jti collision is ErrAlreadyExists", func(t *testing.T) {
			tok := e.mustRecord(t, impToken(t, freshTenant(t, e.ctx, e.pg), "bob", "alice", hour))
			if err := e.record(t, tok, freshTenant(t, e.ctx, e.pg)); !errors.Is(err, issuedtokenstore.ErrAlreadyExists) {
				t.Fatalf("second RecordImpersonation = %v, want ErrAlreadyExists", err)
			}
		})
		t.Run("empty caller tenant writes nothing", func(t *testing.T) {
			targetTenant := freshTenant(t, e.ctx, e.pg)
			tok := impToken(t, targetTenant, "bob", "alice", hour)
			if err := e.record(t, tok, ""); err == nil {
				t.Fatal("RecordImpersonation with no caller tenant succeeded")
			}
			if _, exists := e.row(t, targetTenant, tok.JTI); exists {
				t.Error("a record with no caller tenant wrote a row")
			}
		})
	})

	// The record must wait on an uncommitted invalidation of either user
	// row and then refuse, which only holds when the fence reads the row
	// FOR SHARE under that row's own tenant inside the insert's
	// transaction. The caller's row lives in a tenant other than the
	// target's.
	t.Run("recording fence waits on an uncommitted invalidation", func(t *testing.T) {
		for _, locked := range []string{"target", "admin"} {
			t.Run(locked, func(t *testing.T) {
				callerTenant := freshTenant(t, e.ctx, e.pg)
				targetTenant := freshTenant(t, e.ctx, e.pg)
				e.addUser(t, callerTenant, "alice")
				e.addUser(t, targetTenant, "bob")
				lockTenant, lockSub := targetTenant, "bob"
				if locked == "admin" {
					lockTenant, lockSub = callerTenant, "alice"
				}
				tok := impToken(t, targetTenant, "bob", "alice", hour)
				done := make(chan error, 1)
				err := pgtenant.InTx(e.ctx, e.pg.Pool, lockTenant, func(tx pgx.Tx) error {
					if _, err := tx.Exec(e.ctx,
						`SELECT 1 FROM users WHERE tenant_id = $1 AND subject = $2 FOR UPDATE`, lockTenant, lockSub); err != nil {
						return err
					}
					if _, err := tx.Exec(e.ctx,
						`UPDATE users SET deleted_at = now() WHERE tenant_id = $1 AND subject = $2`, lockTenant, lockSub); err != nil {
						return err
					}
					go func() { done <- e.record(t, tok, callerTenant) }()
					e.awaitUserShareLockWaiter(t, done)
					return nil
				})
				if err != nil {
					t.Fatalf("invalidating transaction: %v", err)
				}
				recErr := <-done
				var inv *issuedtokenstore.UserInvalidatedError
				if !errors.As(recErr, &inv) || inv.TenantID != lockTenant || inv.Subject != lockSub {
					t.Fatalf("RecordImpersonation = %v, want *UserInvalidatedError naming %s/%s", recErr, lockTenant, lockSub)
				}
				if _, exists := e.row(t, targetTenant, tok.JTI); exists {
					t.Error("the refused record left a row")
				}
			})
		}
	})

	// Either the record observes the invalidation level and writes
	// nothing, or it commits first and the full revoke's step 5 finds the
	// row. No interleaving leaves a live row.
	t.Run("recording fence interleaves with the full-revoke sequence", func(t *testing.T) {
		for _, revoked := range []string{"target", "admin"} {
			t.Run(revoked, func(t *testing.T) {
				for round := range 16 {
					callerTenant := freshTenant(t, e.ctx, e.pg)
					targetTenant := freshTenant(t, e.ctx, e.pg)
					admin := "admin-" + newUUID(t)
					e.addUser(t, callerTenant, admin)
					e.addUser(t, targetTenant, "bob")
					revTenant, revSub := targetTenant, "bob"
					if revoked == "admin" {
						revTenant, revSub = callerTenant, admin
					}
					tok := impToken(t, targetTenant, "bob", admin, hour)
					var (
						wg     sync.WaitGroup
						recErr error
						revErr error
					)
					recDelay, revDelay := e.jitter(), e.jitter()
					wg.Go(func() {
						time.Sleep(recDelay)
						recErr = e.record(t, tok, callerTenant)
					})
					wg.Go(func() {
						time.Sleep(revDelay)
						if revErr = e.invalidate(revTenant, revSub); revErr != nil {
							return
						}
						at := time.Now().UTC()
						if _, revErr = e.store.RevokeBySubject(e.ctx, revTenant, revSub, "user_invalidated", at); revErr != nil {
							return
						}
						_, revErr = e.store.RevokeImpersonationsByActor(e.ctx, revSub, "user_invalidated", at)
					})
					wg.Wait()
					if revErr != nil {
						t.Fatalf("round %d: full-revoke sequence: %v", round, revErr)
					}
					row, exists := e.row(t, targetTenant, tok.JTI)
					var inv *issuedtokenstore.UserInvalidatedError
					switch {
					case errors.As(recErr, &inv):
						if exists {
							t.Errorf("round %d: the refused record left a row", round)
						}
					case recErr == nil:
						if !exists || !row.Revoked() {
							t.Errorf("round %d: the record committed and the full revoke left the row live (exists=%v)", round, exists)
						}
					default:
						t.Fatalf("round %d: RecordImpersonation: %v", round, recErr)
					}
				}
			})
		}
	})

	t.Run("RLS confines impersonation rows to their tenant", func(t *testing.T) {
		owner := freshTenant(t, e.ctx, e.pg)
		intruder := freshTenant(t, e.ctx, e.pg)
		tok := e.mustRecord(t, impToken(t, owner, "bob", "alice", hour))
		count := func(run func(func(pgx.Tx) error) error) int {
			var n int
			if err := run(func(tx pgx.Tx) error {
				return tx.QueryRow(e.ctx, `SELECT count(*) FROM issued_tokens WHERE jti = $1`, tok.JTI).Scan(&n)
			}); err != nil {
				t.Fatalf("count under RLS: %v", err)
			}
			return n
		}
		inTenant := func(id string) func(func(pgx.Tx) error) error {
			return func(fn func(pgx.Tx) error) error { return pgtenant.InTx(e.ctx, e.pg.Pool, id, fn) }
		}
		if n := count(inTenant(intruder)); n != 0 {
			t.Errorf("another tenant's context sees %d impersonation rows, want 0", n)
		}
		if n := count(inTenant(owner)); n != 1 {
			t.Errorf("the owning tenant's context sees %d rows, want 1", n)
		}
		allTenants := func(fn func(pgx.Tx) error) error { return pgtenant.InAllTenants(e.ctx, e.pg.Pool, fn) }
		if n := count(allTenants); n != 1 {
			t.Errorf("the __all__ context sees %d rows, want 1", n)
		}
	})

	t.Run("step (1a) refuses an impersonation source", func(t *testing.T) {
		type sourceKind int
		const (
			liveOrdinary sourceKind = iota
			liveImpersonation
			revokedImpersonation
			revokedOrdinary
			delegationChild
		)
		cases := []struct {
			name          string
			subject       sourceKind
			actor         sourceKind
			wantImpRole   issuedtokenstore.SourceRole
			wantRevRole   issuedtokenstore.SourceRole
			wantCommitted bool
		}{
			{name: "live impersonation subject", subject: liveImpersonation, actor: liveOrdinary, wantImpRole: issuedtokenstore.SourceSubjectToken},
			{name: "live impersonation actor", subject: liveOrdinary, actor: liveImpersonation, wantImpRole: issuedtokenstore.SourceActorToken},
			{name: "revoked impersonation subject", subject: revokedImpersonation, actor: liveOrdinary, wantRevRole: issuedtokenstore.SourceSubjectToken},
			{name: "revoked actor outranks a live impersonation subject", subject: liveImpersonation, actor: revokedOrdinary, wantRevRole: issuedtokenstore.SourceActorToken},
			{name: "delegation child subject passes", subject: delegationChild, actor: liveOrdinary, wantCommitted: true},
		}
		source := func(t *testing.T, tenant string, kind sourceKind) string {
			switch kind {
			case liveImpersonation:
				return e.mustRecord(t, impToken(t, tenant, "bob", "alice", hour)).JTI
			case revokedImpersonation:
				tok := e.mustRecord(t, impToken(t, tenant, "bob", "alice", hour))
				e.revoke(t, tok)
				return tok.JTI
			case revokedOrdinary:
				tok := e.mustRecordPlain(t, plainToken(t, tenant, "bob", "", ""))
				e.revoke(t, tok)
				return tok.JTI
			case delegationChild:
				parent := e.mustRecordPlain(t, plainToken(t, tenant, "bob", "", ""))
				return e.mustRecordPlain(t, plainToken(t, tenant, "bob", "alice", parent.JTI)).JTI
			default:
				return e.mustRecordPlain(t, plainToken(t, tenant, "bob", "", "")).JTI
			}
		}
		issuers := map[string]func(tok issuedtokenstore.IssuedToken, sources []issuedtokenstore.SourceToken) error{
			"RecordWithAudit": func(tok issuedtokenstore.IssuedToken, sources []issuedtokenstore.SourceToken) error {
				_, err := e.store.RecordWithAudit(e.ctx, tok, sources, "token.exchanged", []byte(`{"policy_result":"accepted"}`), time.Now().UTC())
				return err
			},
			"RecordWithRotationAudit": func(tok issuedtokenstore.IssuedToken, sources []issuedtokenstore.SourceToken) error {
				_, _, err := e.store.RecordWithRotationAudit(e.ctx, tok, sources, sources[0].JTI, "rotation_replaced",
					"token.exchanged", []byte(`{"policy_result":"accepted"}`), time.Now().UTC())
				return err
			},
		}
		for issuerName, issue := range issuers {
			for _, tc := range cases {
				t.Run(issuerName+"/"+tc.name, func(t *testing.T) {
					tenant := freshTenant(t, e.ctx, e.pg)
					sources := []issuedtokenstore.SourceToken{
						{Role: issuedtokenstore.SourceSubjectToken, JTI: source(t, tenant, tc.subject)},
						{Role: issuedtokenstore.SourceActorToken, JTI: source(t, tenant, tc.actor)},
					}
					issuedBefore, _ := e.tenantCounts(t, tenant)
					err := issue(plainToken(t, tenant, "bob", "", ""), sources)
					issuedAfter, exchanged := e.tenantCounts(t, tenant)
					if tc.wantCommitted {
						if err != nil || issuedAfter != issuedBefore+1 || exchanged != 1 {
							t.Fatalf("err = %v, committed issued=%d exchanged=%d; want one of each", err, issuedAfter-issuedBefore, exchanged)
						}
						return
					}
					if tc.wantRevRole != "" {
						var re *issuedtokenstore.SourceRevokedError
						if !errors.As(err, &re) || re.Role != tc.wantRevRole {
							t.Fatalf("err = %v, want *SourceRevokedError naming %s", err, tc.wantRevRole)
						}
					} else {
						var ie *issuedtokenstore.SourceImpersonationError
						if !errors.As(err, &ie) || ie.Role != tc.wantImpRole || ie.Reason() != string(tc.wantImpRole)+"_impersonation" {
							t.Fatalf("err = %v, want *SourceImpersonationError naming %s", err, tc.wantImpRole)
						}
					}
					if issuedAfter != issuedBefore || exchanged != 0 {
						t.Errorf("refused issuance committed issued=%d exchanged=%d, want none", issuedAfter-issuedBefore, exchanged)
					}
					if row, _ := e.row(t, tenant, sources[0].JTI); tc.subject == liveImpersonation && row.Revoked() {
						t.Error("a refused rotation revoked its impersonation subject")
					}
				})
			}
		}
	})

	// Runs last: every row with act_sub set and no parent_jti that any
	// case left behind was written by RecordImpersonation.
	t.Run("impersonation writer invariant", func(t *testing.T) {
		var jtis []string
		if err := pgtenant.InAllTenants(e.ctx, e.pg.Pool, func(tx pgx.Tx) error {
			rows, err := tx.Query(e.ctx,
				`SELECT jti FROM issued_tokens WHERE act_sub IS NOT NULL AND parent_jti IS NULL`)
			if err != nil {
				return err
			}
			jtis, err = pgx.CollectRows(rows, pgx.RowTo[string])
			return err
		}); err != nil {
			t.Fatalf("read impersonation rows: %v", err)
		}
		if len(jtis) == 0 {
			t.Fatal("no impersonation rows exist, so the invariant reads nothing")
		}
		for _, jti := range jtis {
			if !e.recorded[jti] {
				t.Errorf("impersonation row %s was not written by RecordImpersonation", jti)
			}
		}
	})
}
