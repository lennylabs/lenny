// SPDX-License-Identifier: MIT

//go:build load_local

// load_local-tier ordering coverage for the token rule fixes of change
// 0168, each racing the operations of one counterexample of change 0169's
// concurrency model against a real Postgres issued-token store: two
// impersonation steps whose callers were started from each other's
// subtrees (R5), issuances racing a cascade (R1) and a full revoke (R2),
// a revocation of the caller racing the impersonation step (TOKX-I20), and
// an impersonation start racing a revocation of its caller bearer (G3).
//
// spec: §13.3 (Credential Flow: Recursive-revocation request authorization,
// Platform-admin impersonation, Lineage of derived tokens), §11.4.
package loadlocal_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// raceFixture is a migrated Postgres, its issued-token store, and a random
// source for the delays that vary each run's interleaving.
type raceFixture struct {
	pg    *containers.Postgres
	store *issuedtokenstore.Store
	rng   *rand.Rand
	mu    sync.Mutex
}

func newRaceFixture(t *testing.T) *raceFixture {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	return &raceFixture{pg: pg, store: issuedtokenstore.New(pg.Pool), rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

// tenant registers a fresh tenant and its §11.7 audit sequence.
func (f *raceFixture) tenant(t *testing.T, ctx context.Context) string {
	t.Helper()
	id := "t-" + uuid.NewString()[:8]
	if _, err := f.pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := f.pg.Pool.Exec(ctx, "CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(id)+" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence: %v", err)
	}
	return id
}

// record writes a live row.
func (f *raceFixture) record(t *testing.T, ctx context.Context, tok issuedtokenstore.IssuedToken) {
	t.Helper()
	now := time.Now().UTC()
	tok.TokenHash, tok.IssuedAt, tok.ExpiresAt = []byte(tok.JTI), now, now.Add(time.Hour)
	if err := f.store.Record(ctx, tok); err != nil {
		t.Fatalf("record %s: %v", tok.JTI, err)
	}
}

// delay sleeps up to two milliseconds, chosen at random.
func (f *raceFixture) delay() {
	f.mu.Lock()
	d := time.Duration(f.rng.Intn(2000)) * time.Microsecond
	f.mu.Unlock()
	time.Sleep(d)
}

// race runs fns concurrently, each after a random delay, and waits for
// all of them.
func (f *raceFixture) race(fns ...func()) {
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Add(1)
		go func(fn func()) {
			defer wg.Done()
			f.delay()
			fn()
		}(fn)
	}
	wg.Wait()
}

// revocationRequest runs the impersonation step and then the cascade of a
// recursive revocation of root by caller, as the Token Service does.
func (f *raceFixture) revocationRequest(ctx context.Context, tenant, caller, callerSub, root string) error {
	now := time.Now().UTC()
	res, err := f.store.RevokeImpersonationStep(ctx, issuedtokenstore.ImpersonationStep{
		TenantID: tenant, CallerJTI: caller, CallerSubject: callerSub, RootJTI: root, Reason: "cascade_from_parent", At: now,
	})
	if err != nil {
		return err
	}
	_, err = f.store.RevokeLineage(ctx, issuedtokenstore.LineageRevocation{
		TenantID: tenant, CallerJTI: caller, CallerSubject: callerSub, StepRevokedCallers: res.JTIsIn(tenant),
		Root: issuedtokenstore.IssuedToken{JTI: root}, RootReason: "explicit_revoke", ChildReason: "cascade_from_parent", At: now,
	})
	return err
}

// liveUnder returns the live rows of tenant whose parent_jti chain reaches
// root, root included.
func (f *raceFixture) liveUnder(t *testing.T, ctx context.Context, tenant, root string) []string {
	t.Helper()
	rows, err := f.pg.Pool.Query(ctx, `WITH RECURSIVE s AS (
			SELECT jti FROM issued_tokens WHERE tenant_id = $1 AND jti = $2
			UNION SELECT c.jti FROM issued_tokens c JOIN s ON c.parent_jti = s.jti WHERE c.tenant_id = $1
		) SELECT i.jti FROM issued_tokens i JOIN s ON s.jti = i.jti WHERE i.tenant_id = $1 AND i.revoked_at IS NULL`, tenant, root)
	if err != nil {
		t.Fatalf("read the subtree of %s: %v", root, err)
	}
	live, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("scan the subtree of %s: %v", root, err)
	}
	return live
}

// spec: §13.3 (Credential Flow)
// diagnosis: two revocation requests in acme and globex whose callers are
// impersonation bearers started from each other's roots did not end with
// exactly one root revoked and the other request refused, or did not end
// at all (R5 of change 0169). The impersonation step must run its
// target-tenant updates in the outer transaction, so the deadlock detector
// sees the wait, and retry on 40P01. Check RevokeImpersonationStep in
// pkg/storage/issuedtokenstore.
func TestMutualImpersonationStepsEndWithOneRootRevoked(t *testing.T) {
	f := newRaceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for run := 0; run < 25; run++ {
		acme, globex := f.tenant(t, ctx), f.tenant(t, ctx)
		s := "-" + acme // jti is a global key, so each run names its own tokens
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "A" + s, TenantID: acme, Subject: "alice@acme.com"})
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "G" + s, TenantID: globex, Subject: "carol@globex.com"})
		start := func(jti, target, sub, callerTenant, caller string) {
			now := time.Now().UTC()
			if err := f.store.RecordImpersonation(ctx, issuedtokenstore.IssuedToken{
				JTI: jti, TenantID: target, Subject: sub, TokenHash: []byte(jti), IssuedAt: now, ExpiresAt: now.Add(time.Hour),
				ActSubject: "admin", ImpersonatorTenantID: callerTenant, ImpersonatorJTI: caller,
			}, callerTenant); err != nil {
				t.Fatalf("run %d: start %s: %v", run, jti, err)
			}
		}
		start("Ia-"+acme, acme, "alice@acme.com", globex, "G"+s)
		start("Ig-"+globex, globex, "carol@globex.com", acme, "A"+s)
		var errs [2]error
		f.race(
			func() { errs[0] = f.revocationRequest(ctx, acme, "Ia-"+acme, "alice@acme.com", "A"+s) },
			func() { errs[1] = f.revocationRequest(ctx, globex, "Ig-"+globex, "carol@globex.com", "G"+s) },
		)
		won := 0
		for i, err := range errs {
			switch {
			case err == nil:
				won++
			case errors.Is(err, issuedtokenstore.ErrCallerRevoked):
			default:
				t.Fatalf("run %d: request %d: %v", run, i, err)
			}
		}
		aLive, gLive := len(f.liveUnder(t, ctx, acme, "A"+s)) == 1, len(f.liveUnder(t, ctx, globex, "G"+s)) == 1
		if won != 1 || aLive == (errs[0] == nil) || gLive == (errs[1] == nil) {
			t.Fatalf("run %d: accepted %d requests (errs %v), A live %t, G live %t; want one root revoked by the accepted request", run, won, errs, aLive, gLive)
		}
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a rotation, a narrowing, or a playground mint from a token of
// a cascade's subtree committed while the cascade ran and stayed live
// after it (R1 of change 0169, TOKX-I13). The cascade must take the
// tenant's audit advisory lock before it reads the subtree. Check
// lockForRevocation in pkg/storage/issuedtokenstore.
func TestIssuancesRacingACascadeLeaveNoLiveDescendant(t *testing.T) {
	f := newRaceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for run := 0; run < 30; run++ {
		acme := f.tenant(t, ctx)
		s := "-" + acme // jti is a global key, so each run names its own tokens
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "L" + s, TenantID: acme, Subject: "alice@acme.com"})
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "C" + s, TenantID: acme, Subject: "alice@acme.com"})
		mint := func(jti string) issuedtokenstore.IssuedToken {
			now := time.Now().UTC()
			return issuedtokenstore.IssuedToken{
				JTI: jti, TenantID: acme, Subject: "alice@acme.com", TokenHash: []byte(jti),
				IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: "L" + s,
			}
		}
		src := []issuedtokenstore.SourceToken{{Role: issuedtokenstore.SourceSubjectToken, JTI: "L" + s, Subject: "alice@acme.com"}}
		f.race(
			func() { _ = f.revocationRequest(ctx, acme, "C"+s, "alice@acme.com", "L"+s) },
			func() {
				_, _, _ = f.store.RecordWithRotationAudit(ctx, mint("rotated"+s), src, "L"+s, "rotation_replaced", "token.exchanged", json.RawMessage(`{}`), time.Now().UTC())
			},
			func() {
				_, _ = f.store.RecordWithAudit(ctx, mint("narrowed"+s), src, "token.exchanged", json.RawMessage(`{}`), time.Now().UTC())
			},
			func() { _ = f.store.RecordWithSources(ctx, mint("playground"+s), src) },
		)
		if live := f.liveUnder(t, ctx, acme, "L"+s); len(live) != 0 {
			t.Fatalf("run %d: rows live under the revoked root: %v", run, live)
		}
	}
}

// spec: §11.4 (User Invalidation), §13.3 (Credential Flow)
// diagnosis: a mint from a token of a fully revoked user, or from a
// descendant of one, committed while the full revoke ran and stayed live
// after it (R2 of change 0169), or a mint that presents the user's token
// committed after the invalidation level. Check RevokeBySubject and
// recheckSourceUsers in pkg/storage/issuedtokenstore.
func TestMintsRacingAFullRevokeLeaveNoLiveToken(t *testing.T) {
	f := newRaceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for run := 0; run < 30; run++ {
		acme := f.tenant(t, ctx)
		s := "-" + acme // jti is a global key, so each run names its own tokens
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "A" + s, TenantID: acme, Subject: "alice@acme.com"})
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "K" + s, TenantID: acme, Subject: "bob@acme.com", ParentJTI: "A" + s})
		mint := func(jti, sub, parent string) issuedtokenstore.IssuedToken {
			now := time.Now().UTC()
			return issuedtokenstore.IssuedToken{
				JTI: jti, TenantID: acme, Subject: sub, TokenHash: []byte(jti),
				IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: parent,
			}
		}
		f.race(
			func() {
				if err := pgtenant.InTx(ctx, f.pg.Pool, acme, func(tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `INSERT INTO users (tenant_id, subject, deleted_at) VALUES ($1, 'alice@acme.com', now())`, acme)
					return err
				}); err != nil {
					t.Errorf("run %d: invalidate alice: %v", run, err)
					return
				}
				if _, err := f.store.RevokeBySubject(ctx, acme, "alice@acme.com", "full_revoke", time.Now().UTC()); err != nil {
					t.Errorf("run %d: full revoke: %v", run, err)
				}
			},
			func() {
				_, _ = f.store.RecordWithAudit(ctx, mint("bob-narrowed"+s, "bob@acme.com", "K"+s), []issuedtokenstore.SourceToken{
					{Role: issuedtokenstore.SourceSubjectToken, JTI: "K" + s, Subject: "bob@acme.com"},
				}, "token.exchanged", json.RawMessage(`{}`), time.Now().UTC())
			},
			func() {
				_, _ = f.store.RecordWithAudit(ctx, mint("alice-narrowed"+s, "alice@acme.com", "A"+s), []issuedtokenstore.SourceToken{
					{Role: issuedtokenstore.SourceSubjectToken, JTI: "A" + s, Subject: "alice@acme.com"},
				}, "token.exchanged", json.RawMessage(`{}`), time.Now().UTC())
			},
		)
		if live := f.liveUnder(t, ctx, acme, "A"+s); len(live) != 0 {
			t.Fatalf("run %d: rows live under alice's revoked token: %v", run, live)
		}
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: the impersonation step ended a session although a revocation
// of its caller committed before the step's share read of the caller
// (TOKX-I20), or a step whose caller was revoked concurrently returned an
// error other than ErrCallerRevoked. Check RevokeImpersonationStep in
// pkg/storage/issuedtokenstore.
func TestRevocationOfTheCallerRacingTheImpersonationStep(t *testing.T) {
	f := newRaceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for run := 0; run < 30; run++ {
		acme, globex := f.tenant(t, ctx), f.tenant(t, ctx)
		s := "-" + acme // jti is a global key, so each run names its own tokens
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "B" + s, TenantID: acme, Subject: "bob@acme.com"})
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "Bd" + s, TenantID: acme, Subject: "bob@acme.com"})
		now := time.Now().UTC()
		if err := f.store.RecordImpersonation(ctx, issuedtokenstore.IssuedToken{
			JTI: "Ig-" + globex, TenantID: globex, Subject: "carol@globex.com", TokenHash: []byte("Ig"),
			IssuedAt: now, ExpiresAt: now.Add(time.Hour), ActSubject: "bob@acme.com", ImpersonatorTenantID: acme, ImpersonatorJTI: "B" + s,
		}, acme); err != nil {
			t.Fatalf("run %d: start: %v", run, err)
		}
		var stepErr error
		f.race(
			func() {
				_, stepErr = f.store.RevokeImpersonationStep(ctx, issuedtokenstore.ImpersonationStep{
					TenantID: acme, CallerJTI: "Bd" + s, RootJTI: "B" + s, Reason: "cascade_from_parent", At: time.Now().UTC(),
				})
			},
			func() { _ = f.store.Revoke(ctx, acme, "Bd"+s, "explicit_revoke", time.Now().UTC()) },
		)
		row, err := f.store.Get(ctx, globex, "Ig-"+globex)
		if err != nil {
			t.Fatalf("run %d: read the session: %v", run, err)
		}
		switch {
		case stepErr == nil && !row.Revoked():
			t.Fatalf("run %d: an accepted step left the session live", run)
		case errors.Is(stepErr, issuedtokenstore.ErrCallerRevoked) && row.Revoked():
			t.Fatalf("run %d: a step refused for its revoked caller ended the session", run)
		case stepErr != nil && !errors.Is(stepErr, issuedtokenstore.ErrCallerRevoked):
			t.Fatalf("run %d: step: %v", run, stepErr)
		}
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: an impersonation start racing a cascade of its caller bearer
// committed its bearer row after the cascade revoked the caller bearer
// (G3 of change 0169), left a bearer row or a start record behind a
// refusal, or failed with another error. The start must read its caller
// bearer's row under a share lock in its first transaction. Check
// checkCallerBearer in pkg/storage/issuedtokenstore.
func TestImpersonationStartRacingACascadeOfItsCaller(t *testing.T) {
	f := newRaceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for run := 0; run < 30; run++ {
		acme, globex := f.tenant(t, ctx), f.tenant(t, ctx)
		s := "-" + acme // jti is a global key, so each run names its own tokens
		f.record(t, ctx, issuedtokenstore.IssuedToken{JTI: "B" + s, TenantID: acme, Subject: "bob@acme.com"})
		var (
			startErr   error
			startedAt  time.Time
			cascadeEnd time.Time
		)
		bearer := fmt.Sprintf("imp-%d", run)
		f.race(
			func() {
				now := time.Now().UTC()
				startErr = f.store.RecordImpersonation(ctx, issuedtokenstore.IssuedToken{
					JTI: bearer, TenantID: globex, Subject: "carol@globex.com", TokenHash: []byte(bearer),
					IssuedAt: now, ExpiresAt: now.Add(time.Hour), ActSubject: "bob@acme.com", ImpersonatorTenantID: acme, ImpersonatorJTI: "B" + s,
				}, acme)
				startedAt = time.Now()
			},
			func() {
				_, _ = f.store.RevokeLineage(ctx, issuedtokenstore.LineageRevocation{
					TenantID: acme, Root: issuedtokenstore.IssuedToken{JTI: "B" + s}, RootReason: "explicit_revoke", At: time.Now().UTC(),
				})
				cascadeEnd = time.Now()
			},
		)
		_, getErr := f.store.Get(ctx, globex, bearer)
		var refused *issuedtokenstore.CallerBearerRefusedError
		switch {
		case startErr == nil && getErr != nil:
			t.Fatalf("run %d: an accepted start left no bearer row: %v", run, getErr)
		case errors.As(startErr, &refused) && getErr == nil:
			t.Fatalf("run %d: a refused start left a bearer row", run)
		case startErr != nil && !errors.As(startErr, &refused):
			t.Fatalf("run %d: start: %v", run, startErr)
		case startErr == nil && startedAt.After(cascadeEnd.Add(time.Second)):
			t.Fatalf("run %d: a start was accepted long after the cascade revoked its caller", run)
		}
	}
}
