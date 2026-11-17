//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	erasurepg "github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// erasureEnv is one Postgres container with the production migrations, an
// erasure job store, and a user store over it.
type erasureEnv struct {
	pg     *containers.Postgres
	jobs   *erasurepg.Store
	users  *userpg.Store
	tenant string
}

func newErasureEnv(t *testing.T) *erasureEnv {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	e := &erasureEnv{pg: pg, jobs: erasurepg.New(pg.Pool), users: userpg.New(pg.Pool)}
	e.addTenant(t)
	return e
}

// addTenant switches the env to a fresh tenant holding alice and bob, so one
// container serves several independent cases.
func (e *erasureEnv) addTenant(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	e.tenant = freshTenant(t, ctx, e.pg)
	for _, sub := range []string{"alice", "bob"} {
		if err := e.users.Create(ctx, userstore.User{TenantID: e.tenant, Subject: sub}); err != nil {
			t.Fatalf("seed user %s: %v", sub, err)
		}
	}
}

// erasureAt is a timestamp at microsecond precision, the precision timestamptz
// keeps, so a value read back compares Equal.
var erasureAt = time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)

func (e *erasureEnv) job(id, user string) erasurejob.Job {
	return erasurejob.Job{
		ID: id, TenantID: e.tenant, UserID: user, Phase: erasurejob.PhaseInitiated,
		StartedAt: erasureAt, Deadline: 72 * time.Hour,
		PhaseLog: []erasurejob.PhaseTransition{{Phase: erasurejob.PhaseInitiated, At: erasureAt}},
	}
}

func (e *erasureEnv) start(t *testing.T, id, user string) erasurejob.Job {
	t.Helper()
	j, err := e.jobs.Start(context.Background(), e.job(id, user))
	if err != nil {
		t.Fatalf("Start %s: %v", id, err)
	}
	return j
}

func (e *erasureEnv) fail(t *testing.T, id string) erasurejob.Job {
	t.Helper()
	j, err := e.jobs.Update(context.Background(), id, func(j *erasurejob.Job) error {
		j.Phase = erasurejob.PhaseFailed
		j.Failure = "boom"
		j.CompletedAt = erasureAt.Add(time.Minute)
		j.PhaseLog = append(j.PhaseLog, erasurejob.PhaseTransition{Phase: erasurejob.PhaseFailed, At: erasureAt.Add(time.Minute)})
		return nil
	})
	if err != nil {
		t.Fatalf("fail %s: %v", id, err)
	}
	return j
}

func (e *erasureEnv) user(t *testing.T, sub string) userstore.User {
	t.Helper()
	u, err := e.users.Get(context.Background(), e.tenant, sub)
	if err != nil {
		t.Fatalf("get user %s: %v", sub, err)
	}
	return u
}

func (e *erasureEnv) jobRows(t *testing.T, user string) int {
	t.Helper()
	var n int
	if err := e.pg.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM erasure_jobs WHERE tenant_id = $1 AND user_id = $2`, e.tenant, user).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

// holdInProgressInsert opens a transaction that inserts an in-progress job
// for user and stays open, standing in for a Start or Restart of another
// job that has written but not committed. The caller commits it.
func (e *erasureEnv) holdInProgressInsert(t *testing.T, id, user string) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+e.tenant+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO erasure_jobs (id, tenant_id, user_id, status, started_at)
		VALUES ($1, $2, $3, 'initiated', $4)`, id, e.tenant, user, erasureAt); err != nil {
		t.Fatalf("held insert: %v", err)
	}
	return tx
}

// waitBlocked gives a call that must wait on a lock time to reach it, and
// fails the test when it returned instead.
func waitBlocked(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("the call returned while the conflicting transaction was open")
	case <-time.After(300 * time.Millisecond):
	}
}

// TestErasureJobStoreStart_spec_12_8 pins the Postgres Start: the job row
// and the processing_restricted flag naming it commit together, a failed
// users write leaves neither, a second Start for the user returns
// *ErrActiveJob carrying the first job and writes nothing, including when
// it waits on the in-progress index while the first commits, and a job
// whose tenant is empty or the all-tenants sentinel writes nothing.
//
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 12.8 (Compliance Interfaces: User-level erasure invocation)
// diagnosis: a failure means an erasure job can exist without its
// restriction, a user can hold two in-progress jobs (so the Article 18
// trigger rejects a completion), or a tenant-admin's erase request runs
// under the all-tenants sentinel.
func TestErasureJobStoreStart_spec_12_8(t *testing.T) {
	ctx := context.Background()
	e := newErasureEnv(t)

	t.Run("admitted start writes the job and the flag", func(t *testing.T) {
		e.addTenant(t)
		before := e.user(t, "alice").Version
		got := e.start(t, "erasure_a_"+e.tenant, "alice")
		stored, err := e.jobs.Get(ctx, e.tenant, got.ID)
		if err != nil || stored.Phase != erasurejob.PhaseInitiated || stored.Deadline != 72*time.Hour {
			t.Fatalf("stored job = %+v (%v)", stored, err)
		}
		u := e.user(t, "alice")
		if !u.ProcessingRestricted || u.ErasureJobID != got.ID || u.Version != before+1 {
			t.Fatalf("user = %+v, want restricted naming %s at version %d", u, got.ID, before+1)
		}
	})

	t.Run("failed users write leaves neither row", func(t *testing.T) {
		e.addTenant(t)
		if _, err := e.jobs.Start(ctx, e.job("erasure_x_"+e.tenant, "nobody")); !errors.Is(err, userstore.ErrNotFound) {
			t.Fatalf("Start for a missing user = %v, want userstore.ErrNotFound", err)
		}
		if n := e.jobRows(t, "nobody"); n != 0 {
			t.Fatalf("failed Start left %d job rows", n)
		}
	})

	t.Run("second start returns the in-progress job", func(t *testing.T) {
		e.addTenant(t)
		first := e.start(t, "erasure_1_"+e.tenant, "alice")
		_, err := e.jobs.Start(ctx, e.job("erasure_2_"+e.tenant, "alice"))
		var active *erasurejob.ErrActiveJob
		if !errors.As(err, &active) || active.Job.ID != first.ID {
			t.Fatalf("second Start = %v, want *ErrActiveJob naming %s", err, first.ID)
		}
		if n := e.jobRows(t, "alice"); n != 1 {
			t.Fatalf("job rows = %d, want 1", n)
		}
		if u := e.user(t, "alice"); u.ErasureJobID != first.ID {
			t.Fatalf("flag names %q, want %s", u.ErasureJobID, first.ID)
		}
	})

	t.Run("start waiting on the index sees the committed job", func(t *testing.T) {
		e.addTenant(t)
		heldID := "erasure_held_" + e.tenant
		tx := e.holdInProgressInsert(t, heldID, "alice")
		done := make(chan struct{})
		var err error
		go func() {
			defer close(done)
			_, err = e.jobs.Start(ctx, e.job("erasure_late_"+e.tenant, "alice"))
		}()
		waitBlocked(t, done)
		if cerr := tx.Commit(ctx); cerr != nil {
			t.Fatalf("commit held insert: %v", cerr)
		}
		<-done
		var active *erasurejob.ErrActiveJob
		if !errors.As(err, &active) || active.Job.ID != heldID {
			t.Fatalf("waiting Start = %v, want *ErrActiveJob naming %s", err, heldID)
		}
		if n := e.jobRows(t, "alice"); n != 1 {
			t.Fatalf("job rows = %d, want only the held job", n)
		}
		if u := e.user(t, "alice"); u.ProcessingRestricted {
			t.Fatal("the refused Start set the flag")
		}
	})

	for _, tenant := range []string{"", erasurejob.AllTenants} {
		t.Run("tenant "+tenant+" writes nothing", func(t *testing.T) {
			j := e.job("erasure_bad_tenant", "alice")
			j.TenantID = tenant
			if _, err := e.jobs.Start(ctx, j); !errors.Is(err, erasurejob.ErrInvalidTenant) {
				t.Fatalf("Start = %v, want ErrInvalidTenant", err)
			}
			var n int
			if err := e.pg.Pool.QueryRow(ctx, `SELECT count(*) FROM erasure_jobs WHERE id = 'erasure_bad_tenant'`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("rows = %d (%v), want 0", n, err)
			}
		})
	}

	t.Run("get is tenant scoped", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_g_"+e.tenant, "alice")
		owner := e.tenant
		e.addTenant(t)
		if _, err := e.jobs.Get(ctx, e.tenant, j.ID); !errors.Is(err, erasurejob.ErrNotFound) {
			t.Fatalf("Get from another tenant = %v, want ErrNotFound", err)
		}
		if _, err := e.jobs.Get(ctx, owner, j.ID); err != nil {
			t.Fatalf("Get from the owner tenant: %v", err)
		}
		if _, err := e.jobs.Get(ctx, erasurejob.AllTenants, j.ID); err != nil {
			t.Fatalf("Get under the all-tenants scope: %v", err)
		}
	})
}

// TestErasureJobStoreCompleteAndClear_spec_12_8 pins Complete and
// ClearRestriction against the migration 0042 trigger: Complete clears only
// a flag naming its job, completes with no users write after an operator
// clear, and writes nothing on a stale expect; ClearRestriction refuses a
// flag held by another job; and the job-row lock orders a clear and a
// restart of the same job in both interleavings. Every users write bumps
// the row version.
//
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 15.1 (REST API)
// diagnosis: a failure means the completion transaction is rejected by the
// Article 18 trigger, a job lifts another job's restriction, a stale runner
// completes a restarted job, or an operator clear empties the flag of a job
// a concurrent retry has restarted.
func TestErasureJobStoreCompleteAndClear_spec_12_8(t *testing.T) {
	ctx := context.Background()
	e := newErasureEnv(t)
	done := erasureAt.Add(time.Hour)

	t.Run("complete passes the trigger and clears its own flag", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_c_"+e.tenant, "alice")
		v := e.user(t, "alice").Version
		got, err := e.jobs.Complete(ctx, j.ID, done, j)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if got.Phase != erasurejob.PhaseCompleted || !got.CompletedAt.Equal(done) {
			t.Fatalf("completed job = %+v", got)
		}
		u := e.user(t, "alice")
		if u.ProcessingRestricted || u.ErasureJobID != "" || u.Version != v+1 {
			t.Fatalf("user = %+v, want cleared at version %d", u, v+1)
		}
	})

	t.Run("complete leaves another job's flag", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_c_"+e.tenant, "alice")
		e.repointFlag(t, "alice", "erasure_other")
		v := e.user(t, "alice").Version
		if _, err := e.jobs.Complete(ctx, j.ID, done, j); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if u := e.user(t, "alice"); !u.ProcessingRestricted || u.ErasureJobID != "erasure_other" || u.Version != v {
			t.Fatalf("user = %+v, want the other job's flag untouched", u)
		}
	})

	t.Run("complete after an operator clear writes no users row", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_c_"+e.tenant, "alice")
		failed := e.fail(t, j.ID)
		if _, err := e.jobs.ClearRestriction(ctx, j.ID); err != nil {
			t.Fatalf("ClearRestriction: %v", err)
		}
		v := e.user(t, "alice").Version
		if _, err := e.jobs.Complete(ctx, j.ID, done, failed); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if u := e.user(t, "alice"); u.ProcessingRestricted || u.Version != v {
			t.Fatalf("user = %+v, want untouched at version %d", u, v)
		}
	})

	t.Run("complete with a stale expect writes nothing", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_c_"+e.tenant, "alice")
		e.fail(t, j.ID)
		if _, err := e.jobs.Restart(ctx, j.ID); err != nil {
			t.Fatalf("Restart: %v", err)
		}
		if _, err := e.jobs.Complete(ctx, j.ID, done, j); !errors.Is(err, erasurejob.ErrJobMoved) {
			t.Fatalf("Complete = %v, want ErrJobMoved", err)
		}
		stored, _ := e.jobs.Get(ctx, e.tenant, j.ID)
		if stored.Phase != erasurejob.PhaseInitiated {
			t.Fatalf("phase = %q, want initiated", stored.Phase)
		}
		if u := e.user(t, "alice"); !u.ProcessingRestricted || u.ErasureJobID != j.ID {
			t.Fatalf("refused Complete changed the flag: %+v", u)
		}
	})

	t.Run("clear refuses a flag held by another job", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_c_"+e.tenant, "alice")
		e.fail(t, j.ID)
		e.repointFlag(t, "alice", "erasure_other")
		if _, err := e.jobs.ClearRestriction(ctx, j.ID); !errors.Is(err, userstore.ErrRestrictionHeldByOtherJob) {
			t.Fatalf("ClearRestriction = %v, want ErrRestrictionHeldByOtherJob", err)
		}
		if u := e.user(t, "alice"); !u.ProcessingRestricted || u.ErasureJobID != "erasure_other" {
			t.Fatalf("refused clear changed the flag: %+v", u)
		}
	})

	t.Run("clear refuses a job that is not failed", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_c_"+e.tenant, "alice")
		if _, err := e.jobs.ClearRestriction(ctx, j.ID); !errors.Is(err, erasurejob.ErrNotFailed) {
			t.Fatalf("ClearRestriction = %v, want ErrNotFailed", err)
		}
		if u := e.user(t, "alice"); !u.ProcessingRestricted {
			t.Fatal("refused clear lifted the flag")
		}
	})

	t.Run("clear waiting on a restart's job-row lock answers not failed", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_c_"+e.tenant, "alice")
		e.fail(t, j.ID)
		tx := e.holdRestart(t, j.ID, "alice")
		done := make(chan struct{})
		var err error
		go func() {
			defer close(done)
			_, err = e.jobs.ClearRestriction(ctx, j.ID)
		}()
		waitBlocked(t, done)
		if cerr := tx.Commit(ctx); cerr != nil {
			t.Fatalf("commit restart: %v", cerr)
		}
		<-done
		var nf *erasurejob.NotFailedError
		if !errors.Is(err, erasurejob.ErrNotFailed) || !errors.As(err, &nf) || nf.Phase != erasurejob.PhaseInitiated {
			t.Fatalf("ClearRestriction behind a restart = %v, want *NotFailedError carrying the locked phase initiated", err)
		}
		if u := e.user(t, "alice"); !u.ProcessingRestricted || u.ErasureJobID != j.ID {
			t.Fatalf("flag = %+v, want set naming the restarted job", u)
		}
	})

	t.Run("restart after a committed clear sets the flag again", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_c_"+e.tenant, "alice")
		e.fail(t, j.ID)
		if _, err := e.jobs.ClearRestriction(ctx, j.ID); err != nil {
			t.Fatalf("ClearRestriction: %v", err)
		}
		got, err := e.jobs.Restart(ctx, j.ID)
		if err != nil || got.Phase != erasurejob.PhaseInitiated {
			t.Fatalf("Restart = %+v (%v)", got, err)
		}
		if u := e.user(t, "alice"); !u.ProcessingRestricted || u.ErasureJobID != j.ID {
			t.Fatalf("flag = %+v, want set naming the job", u)
		}
	})
}

// repointFlag sets the user's flag naming jobID through the shared helper.
func (e *erasureEnv) repointFlag(t *testing.T, sub, jobID string) {
	t.Helper()
	if err := pgxInTenant(e, func(tx pgx.Tx) error {
		_, err := userpg.SetErasureRestrictionTx(context.Background(), tx, e.tenant, sub, jobID, true, "")
		return err
	}); err != nil {
		t.Fatalf("repoint flag: %v", err)
	}
}

// pgxInTenant runs fn in a committed transaction under the env's tenant.
func pgxInTenant(e *erasureEnv, fn func(pgx.Tx) error) error {
	ctx := context.Background()
	tx, err := e.pg.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+e.tenant+"'"); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// holdRestart opens a transaction that makes a restart's writes on job id
// (the job row locked and moved to initiated, the flag set naming it) and
// stays open. The caller commits it.
func (e *erasureEnv) holdRestart(t *testing.T, id, sub string) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+e.tenant+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM erasure_jobs WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("lock job: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE erasure_jobs SET status = 'initiated', failure = '', completed_at = NULL WHERE id = $1`, id); err != nil {
		t.Fatalf("restart job: %v", err)
	}
	if _, err := userpg.SetErasureRestrictionTx(ctx, tx, e.tenant, sub, id, true, ""); err != nil {
		t.Fatalf("set flag: %v", err)
	}
	return tx
}

// TestErasureJobStoreRestart_spec_12_8 pins the Postgres Restart: a failed
// job moves to initiated with its attempt fields cleared and the flag set
// naming it, a job that is not failed is refused, and a user with another
// in-progress job gets *ErrActiveJob, including when that job's write
// commits while Restart waits on the in-progress index.
//
// spec: 12.8 (Compliance Interfaces: User-level erasure invocation), 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
// diagnosis: a failure means a retry can run a second in-progress job for
// a user, restart a job that is not failed, or restart it without
// restoring the restriction an operator clear lifted.
func TestErasureJobStoreRestart_spec_12_8(t *testing.T) {
	ctx := context.Background()
	e := newErasureEnv(t)

	t.Run("restart of a failed job", func(t *testing.T) {
		e.addTenant(t)
		j := e.job("erasure_r_"+e.tenant, "alice")
		j.OverrideReceipt = map[string]any{"legalHoldOverride": true}
		if _, err := e.jobs.Start(ctx, j); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := e.jobs.Update(ctx, j.ID, func(x *erasurejob.Job) error {
			x.ErasedAt, x.ReceiptAt = erasureAt, erasureAt
			x.Billing = erasurejob.BillingErasureOutcome{Disposition: "pseudonymized", Pseudonymized: 1}
			return nil
		}); err != nil {
			t.Fatalf("seed fields: %v", err)
		}
		e.fail(t, j.ID)
		if _, err := e.jobs.ClearRestriction(ctx, j.ID); err != nil {
			t.Fatalf("ClearRestriction: %v", err)
		}
		got, err := e.jobs.Restart(ctx, j.ID)
		if err != nil {
			t.Fatalf("Restart: %v", err)
		}
		stored, _ := e.jobs.Get(ctx, erasurejob.AllTenants, j.ID)
		for _, x := range []erasurejob.Job{got, stored} {
			if x.Phase != erasurejob.PhaseInitiated || x.Failure != "" || !x.CompletedAt.IsZero() ||
				!x.ErasedAt.IsZero() || !x.ReceiptAt.IsZero() || x.Billing != (erasurejob.BillingErasureOutcome{}) {
				t.Fatalf("restarted job = %+v", x)
			}
			if len(x.PhaseLog) != 3 || x.OverrideReceipt["legalHoldOverride"] != true || !x.StartedAt.Equal(erasureAt) {
				t.Fatalf("restart lost the log, the override, or the start: %+v", x)
			}
		}
		if u := e.user(t, "alice"); !u.ProcessingRestricted || u.ErasureJobID != j.ID {
			t.Fatalf("flag = %+v, want set naming the job", u)
		}
	})

	t.Run("restart refuses a job that is not failed", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_r_"+e.tenant, "alice")
		_, err := e.jobs.Restart(ctx, j.ID)
		var nf *erasurejob.NotFailedError
		if !errors.Is(err, erasurejob.ErrNotFailed) || !errors.As(err, &nf) || nf.Phase != j.Phase {
			t.Fatalf("Restart = %v, want *NotFailedError carrying the locked phase %q", err, j.Phase)
		}
		if _, err := e.jobs.Restart(ctx, "erasure_absent"); !errors.Is(err, erasurejob.ErrNotFound) {
			t.Fatalf("Restart unknown = %v, want ErrNotFound", err)
		}
	})

	t.Run("restart beside a committed in-progress job", func(t *testing.T) {
		e.addTenant(t)
		old := e.start(t, "erasure_old_"+e.tenant, "alice")
		e.fail(t, old.ID)
		newer := e.start(t, "erasure_new_"+e.tenant, "alice")
		_, err := e.jobs.Restart(ctx, old.ID)
		var active *erasurejob.ErrActiveJob
		if !errors.As(err, &active) || active.Job.ID != newer.ID {
			t.Fatalf("Restart = %v, want *ErrActiveJob naming %s", err, newer.ID)
		}
		stored, _ := e.jobs.Get(ctx, e.tenant, old.ID)
		if stored.Phase != erasurejob.PhaseFailed {
			t.Fatalf("refused restart moved the job to %q", stored.Phase)
		}
	})

	t.Run("restart waiting on the index", func(t *testing.T) {
		e.addTenant(t)
		old := e.start(t, "erasure_old_"+e.tenant, "alice")
		e.fail(t, old.ID)
		heldID := "erasure_held_" + e.tenant
		tx := e.holdInProgressInsert(t, heldID, "alice")
		done := make(chan struct{})
		var err error
		go func() {
			defer close(done)
			_, err = e.jobs.Restart(ctx, old.ID)
		}()
		waitBlocked(t, done)
		if cerr := tx.Commit(ctx); cerr != nil {
			t.Fatalf("commit held insert: %v", cerr)
		}
		<-done
		var active *erasurejob.ErrActiveJob
		if !errors.As(err, &active) || active.Job.ID != heldID {
			t.Fatalf("waiting Restart = %v, want *ErrActiveJob naming %s", err, heldID)
		}
		stored, _ := e.jobs.Get(ctx, e.tenant, old.ID)
		if stored.Phase != erasurejob.PhaseFailed {
			t.Fatalf("refused restart moved the job to %q", stored.Phase)
		}
	})
}

// TestErasureJobStoreUpdateSerializesAndRoundTrips_spec_12_8 pins Update:
// two concurrent Updates of one job serialize, so the second mutate sees
// the first's write; a mutate error writes nothing; and every field the
// detail column carries reads back equal through Get and ListActive on a
// second store over the same database.
//
// spec: 12.8 (Compliance Interfaces: Erasure job phase field (crash recovery))
// diagnosis: a failure means a phase advance can be lost to a concurrent
// write, or a field the runner depends on to resume (ErasedAt, ReceiptAt,
// the phase log, the override receipt, the counts) does not survive the
// replica that wrote it.
func TestErasureJobStoreUpdateSerializesAndRoundTrips_spec_12_8(t *testing.T) {
	ctx := context.Background()
	e := newErasureEnv(t)

	t.Run("concurrent updates serialize", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_u_"+e.tenant, "alice")
		var wg sync.WaitGroup
		seen := make(chan int, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.jobs.Update(ctx, j.ID, func(x *erasurejob.Job) error {
					seen <- x.Total
					time.Sleep(100 * time.Millisecond)
					x.Total++
					return nil
				}); err != nil {
					t.Errorf("Update: %v", err)
				}
			}()
		}
		wg.Wait()
		close(seen)
		var reads []int
		for v := range seen {
			reads = append(reads, v)
		}
		stored, _ := e.jobs.Get(ctx, e.tenant, j.ID)
		// The mutates read 0 and 1 in some order: the second saw the first's write.
		if stored.Total != 2 || len(reads) != 2 || reads[0]+reads[1] != 1 {
			t.Fatalf("Total = %d, mutates read %v, want 2 and one mutate reading the other's write", stored.Total, reads)
		}
	})

	t.Run("a mutate error writes nothing", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_u_"+e.tenant, "alice")
		boom := errors.New("boom")
		if _, err := e.jobs.Update(ctx, j.ID, func(x *erasurejob.Job) error {
			x.Total = 9
			return boom
		}); !errors.Is(err, boom) {
			t.Fatalf("Update = %v, want the mutate error", err)
		}
		if stored, _ := e.jobs.Get(ctx, e.tenant, j.ID); stored.Total != 0 {
			t.Fatalf("Total = %d after a failed mutate", stored.Total)
		}
	})

	t.Run("every detail field round-trips", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_full_"+e.tenant, "alice")
		want, err := e.jobs.Update(ctx, j.ID, func(x *erasurejob.Job) error {
			x.Phase = erasurejob.PhasePseudonymizing
			x.Deadline = time.Hour
			x.Deleted = map[string]int{"sessions": 3, "memory": 1}
			x.Total = 4
			x.Billing = erasurejob.BillingErasureOutcome{Disposition: "pseudonymized", Pseudonymized: 5, Verified: true}
			x.DeadLetterRedacted = 2
			x.PhaseLog = append(x.PhaseLog,
				erasurejob.PhaseTransition{Phase: erasurejob.PhaseStoreDeleting, At: erasureAt.Add(time.Second)},
				erasurejob.PhaseTransition{Phase: erasurejob.PhasePseudonymizing, At: erasureAt.Add(2 * time.Second)})
			x.Failure = "earlier"
			x.InitiatedBy = "bob@acme.com"
			x.OverrideReceipt = map[string]any{"legalHoldOverride": true, "overrideBy": "carol@acme.com"}
			x.ErasedAt = erasureAt.Add(3 * time.Second)
			x.ReceiptAt = erasureAt.Add(4 * time.Second)
			x.CompletedAt = erasureAt.Add(5 * time.Second)
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		second := erasurepg.New(e.pg.Pool)
		got, err := second.Get(ctx, e.tenant, j.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		assertJobsEqual(t, got, want)
		active, err := second.ListActive(ctx)
		if err != nil {
			t.Fatalf("ListActive: %v", err)
		}
		var found bool
		for _, a := range active {
			if a.ID == j.ID {
				found = true
				assertJobsEqual(t, a, want)
			}
		}
		if !found {
			t.Fatalf("ListActive does not hold %s", j.ID)
		}
	})

	t.Run("list active excludes terminal jobs", func(t *testing.T) {
		e.addTenant(t)
		j := e.start(t, "erasure_t_"+e.tenant, "alice")
		e.fail(t, j.ID)
		active, err := e.jobs.ListActive(ctx)
		if err != nil {
			t.Fatalf("ListActive: %v", err)
		}
		for _, a := range active {
			if a.ID == j.ID {
				t.Fatal("ListActive returned a failed job")
			}
		}
	})
}

// assertJobsEqual compares two jobs field by field, times through Equal.
func assertJobsEqual(t *testing.T, got, want erasurejob.Job) {
	t.Helper()
	if got.ID != want.ID || got.TenantID != want.TenantID || got.UserID != want.UserID ||
		got.Phase != want.Phase || got.Deadline != want.Deadline || got.Total != want.Total ||
		got.Billing != want.Billing || got.DeadLetterRedacted != want.DeadLetterRedacted ||
		got.Failure != want.Failure || got.InitiatedBy != want.InitiatedBy {
		t.Fatalf("job = %+v\nwant  %+v", got, want)
	}
	for name, pair := range map[string][2]time.Time{
		"StartedAt": {got.StartedAt, want.StartedAt}, "CompletedAt": {got.CompletedAt, want.CompletedAt},
		"ErasedAt": {got.ErasedAt, want.ErasedAt}, "ReceiptAt": {got.ReceiptAt, want.ReceiptAt},
	} {
		if !pair[0].Equal(pair[1]) {
			t.Fatalf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
	if len(got.Deleted) != len(want.Deleted) {
		t.Fatalf("Deleted = %v, want %v", got.Deleted, want.Deleted)
	}
	for k, v := range want.Deleted {
		if got.Deleted[k] != v {
			t.Fatalf("Deleted = %v, want %v", got.Deleted, want.Deleted)
		}
	}
	if len(got.PhaseLog) != len(want.PhaseLog) {
		t.Fatalf("PhaseLog = %v, want %v", got.PhaseLog, want.PhaseLog)
	}
	for i := range want.PhaseLog {
		if got.PhaseLog[i].Phase != want.PhaseLog[i].Phase || !got.PhaseLog[i].At.Equal(want.PhaseLog[i].At) {
			t.Fatalf("PhaseLog[%d] = %+v, want %+v", i, got.PhaseLog[i], want.PhaseLog[i])
		}
	}
	if len(got.OverrideReceipt) != len(want.OverrideReceipt) {
		t.Fatalf("OverrideReceipt = %v, want %v", got.OverrideReceipt, want.OverrideReceipt)
	}
	for k, v := range want.OverrideReceipt {
		if got.OverrideReceipt[k] != v {
			t.Fatalf("OverrideReceipt = %v, want %v", got.OverrideReceipt, want.OverrideReceipt)
		}
	}
}
