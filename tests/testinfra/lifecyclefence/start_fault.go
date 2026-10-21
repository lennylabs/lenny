// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// startFaultSQLState is the SQLSTATE the Postgres start-fault trigger
// raises, so the harness tells its own fault from any other store error.
const startFaultSQLState = "LF901"

// startFaultDDL installs the Postgres start fault: a table of armed job IDs
// and a BEFORE UPDATE trigger on users that raises when a write sets the
// restriction naming an armed job. The erasure job store's Start inserts the
// job row and then writes the restriction in one transaction, so the fault
// fires after the insert has run and the whole transaction must roll back.
// The trigger matches only armed job IDs, so installing it changes nothing
// for a store no test arms.
const startFaultDDL = `
CREATE TABLE IF NOT EXISTS lifecyclefence_start_faults (job_id TEXT PRIMARY KEY);
CREATE OR REPLACE FUNCTION lifecyclefence_start_fault() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF EXISTS (SELECT 1 FROM lifecyclefence_start_faults WHERE job_id = NEW.erasure_job_id) THEN
		RAISE EXCEPTION 'lifecyclefence: injected start fault for erasure job %', NEW.erasure_job_id
			USING ERRCODE = '` + startFaultSQLState + `';
	END IF;
	RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS lifecyclefence_start_fault ON users;
CREATE TRIGGER lifecyclefence_start_fault BEFORE UPDATE ON users
	FOR EACH ROW WHEN (NEW.processing_restricted) EXECUTE FUNCTION lifecyclefence_start_fault();`

// startFault fails the restriction write inside the erasure job store's
// Start for the job IDs a test arms, so the fault lands inside the store's
// own write rather than before it.
type startFault interface {
	// arm makes the restriction write naming jobID fail until disarm runs.
	arm(ctx context.Context, jobID string) (disarm func(context.Context) error, err error)
	// reached reports whether err is the armed fault, which proves the
	// store reached its restriction write.
	reached(err error) bool
}

// pgStartFault arms the Postgres trigger by inserting the job ID into its
// table.
type pgStartFault struct{ pool *pgxpool.Pool }

func installPGStartFault(ctx context.Context, pool *pgxpool.Pool) (*pgStartFault, error) {
	if _, err := pool.Exec(ctx, startFaultDDL); err != nil {
		return nil, fmt.Errorf("install start-fault trigger: %w", err)
	}
	return &pgStartFault{pool: pool}, nil
}

func (f *pgStartFault) arm(ctx context.Context, jobID string) (func(context.Context) error, error) {
	if _, err := f.pool.Exec(ctx, `INSERT INTO lifecyclefence_start_faults (job_id) VALUES ($1)`, jobID); err != nil {
		return nil, fmt.Errorf("arm start fault for %s: %w", jobID, err)
	}
	return func(ctx context.Context) error {
		if _, err := f.pool.Exec(ctx, `DELETE FROM lifecyclefence_start_faults WHERE job_id = $1`, jobID); err != nil {
			return fmt.Errorf("disarm start fault for %s: %w", jobID, err)
		}
		return nil
	}, nil
}

func (f *pgStartFault) reached(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == startFaultSQLState
}

// memStartFault is the in-memory store's restriction writer with armed job
// IDs: a restriction write naming an armed job answers ErrInjected and
// writes nothing.
type memStartFault struct {
	erasurejob.RestrictionWriter

	// mu guards armed.
	mu    sync.Mutex
	armed map[string]bool
}

func newMemStartFault(w erasurejob.RestrictionWriter) *memStartFault {
	return &memStartFault{RestrictionWriter: w, armed: map[string]bool{}}
}

// SetErasureRestriction implements erasurejob.RestrictionWriter.
func (f *memStartFault) SetErasureRestriction(ctx context.Context, tenantID, subject, jobID string, restricted bool, requireJobID string) (int64, error) {
	f.mu.Lock()
	fire := restricted && f.armed[jobID]
	f.mu.Unlock()
	if fire {
		return 0, ErrInjected
	}
	return f.RestrictionWriter.SetErasureRestriction(ctx, tenantID, subject, jobID, restricted, requireJobID)
}

func (f *memStartFault) arm(_ context.Context, jobID string) (func(context.Context) error, error) {
	f.mu.Lock()
	f.armed[jobID] = true
	f.mu.Unlock()
	return func(context.Context) error {
		f.mu.Lock()
		delete(f.armed, jobID)
		f.mu.Unlock()
		return nil
	}, nil
}

func (f *memStartFault) reached(err error) bool { return errors.Is(err, ErrInjected) }

// startWithFault runs store's Start for j with the fault armed for j's ID.
// It records j's ID as a faulted Start and answers an error wrapping
// ErrInjected only when the store reached the armed restriction write, so a
// Start that fails before its write, or succeeds past it, surfaces as what
// it is.
func (e *Erasure) startWithFault(ctx context.Context, store erasurejob.Store, j erasurejob.Job) (erasurejob.Job, error) {
	disarm, err := e.w.S.startFault.arm(ctx, j.ID)
	if err != nil {
		return erasurejob.Job{}, err
	}
	got, err := store.Start(ctx, j)
	if derr := disarm(context.WithoutCancel(ctx)); derr != nil {
		e.w.Violatef("%v", derr)
	}
	e.mu.Lock()
	e.faultedStarts = append(e.faultedStarts, j.ID)
	e.mu.Unlock()
	if err != nil && e.w.S.startFault.reached(err) {
		return got, fmt.Errorf("%w: %w", ErrInjected, err)
	}
	return got, err
}

// RequireStartLeftNothing fails t unless the last want faulted Starts all
// reached the armed restriction write and left nothing behind: no job row
// under any attempted ID, no in-progress job for user, and no restriction
// on user. A Start whose job insert committed apart from its restriction
// write fails the first check.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18))
func RequireStartLeftNothing(ctx context.Context, t testing.TB, e *Erasure, tenantID, user string, want int) {
	t.Helper()
	ids := e.FaultedStarts()
	if len(ids) < want {
		t.Fatalf("faulted Starts = %v, want %d", ids, want)
	}
	for _, id := range ids[len(ids)-want:] {
		if j, err := e.Jobs.Get(ctx, erasurejob.AllTenants, id); !errors.Is(err, erasurejob.ErrNotFound) {
			t.Errorf("job %s after its failed Start: %+v, %v; want ErrNotFound", id, j, err)
		}
	}
	active, err := e.Jobs.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active erasure jobs: %v", err)
	}
	for _, j := range active {
		if j.TenantID == tenantID && j.UserID == user {
			t.Errorf("a failed Start left in-progress job %s for %s", j.ID, user)
		}
	}
	u, err := e.w.S.Users.Get(ctx, tenantID, user)
	if err != nil {
		t.Fatalf("read user %s: %v", user, err)
	}
	if u.ProcessingRestricted || u.ErasureJobID != "" {
		t.Errorf("a failed Start left processing_restricted=%v naming %q", u.ProcessingRestricted, u.ErasureJobID)
	}
}
