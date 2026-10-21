// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed erasurejob.Store over the
// erasure_jobs table (migrations 0042 and 0186). The job record lives in
// the database so the job survives the replica that started it, the
// migration 0042 Article 18 trigger has rows to act on, and the job and
// the user's processing_restricted flag are written in one transaction.
//
// id, tenant_id, user_id, status, total, failure, started_at, and
// completed_at map to their columns. Every other Job field is serialized
// into the detail jsonb column.
//
// Tenant context. Start runs under the job's own tenant, because a
// tenant-admin reaches the erase endpoint. Get runs under the caller's
// tenant, or under the all-tenants sentinel when the caller passes
// erasurejob.AllTenants. Update, Restart, ClearRestriction, Complete, and
// ListActive run under the all-tenants sentinel: only platform-admin
// handlers and the leader-gated runner reach them, and the caller emits
// the §12.3 cross_tenant_read event for the read.
//
// Lock order. Every transaction that writes a job and a users row locks
// the job row FOR UPDATE before the users row, so two such transactions
// never deadlock on each other.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §12.8 (Erasure job phase field (crash recovery)), §12.8 (User-level
// erasure invocation).
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// inProgressIndex is the migration 0186 partial unique index that allows
// one in-progress job per (tenant_id, user_id).
const inProgressIndex = "idx_erasure_jobs_in_progress_per_user"

// inProgressPredicate is the index predicate; ON CONFLICT names it so the
// insert infers the partial index.
const inProgressPredicate = `status IN ('initiated', 'store_deleting', 'pseudonymizing', 'verifying')`

const selectCols = `id, tenant_id, user_id, status, total, failure, started_at, completed_at, detail`

// Store is the Postgres erasure job store. Construct with New over the
// primary pool.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Store over pool, which must point at the primary.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

var _ erasurejob.Store = (*Store)(nil)

// detail is the jsonb form of the Job fields no query filters on.
type detail struct {
	Deadline           time.Duration                    `json:"deadline,omitempty"`
	Deleted            map[string]int                   `json:"deleted,omitempty"`
	Billing            erasurejob.BillingErasureOutcome `json:"billing"`
	DeadLetterRedacted int                              `json:"deadLetterRedacted,omitempty"`
	PhaseLog           []erasurejob.PhaseTransition     `json:"phaseLog,omitempty"`
	InitiatedBy        string                           `json:"initiatedBy,omitempty"`
	OverrideReceipt    map[string]any                   `json:"overrideReceipt,omitempty"`
	ErasedAt           time.Time                        `json:"erasedAt"`
	ReceiptAt          time.Time                        `json:"receiptAt"`
}

// Start implements erasurejob.Store. The whole transaction, the
// in-progress read included, runs under the job's own tenant, so an empty
// tenant or the all-tenants sentinel fails before anything is written.
func (s *Store) Start(ctx context.Context, j erasurejob.Job) (erasurejob.Job, error) {
	if j.ID == "" {
		return erasurejob.Job{}, erasurejob.ErrMissingID
	}
	var active *erasurejob.Job
	err := pgtenant.InTx(ctx, s.pool, j.TenantID, func(tx pgx.Tx) error {
		inserted, err := insertJob(ctx, tx, j)
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := activeJobForUser(ctx, tx, j.TenantID, j.UserID)
			if err != nil {
				return err
			}
			active = &existing
			return nil
		}
		_, err = userpg.SetErasureRestrictionTx(ctx, tx, j.TenantID, j.UserID, j.ID, true, "")
		return err
	})
	if errors.Is(err, pgtenant.ErrInvalidTenantID) {
		return erasurejob.Job{}, fmt.Errorf("%w: %v", erasurejob.ErrInvalidTenant, err)
	}
	if err != nil {
		return erasurejob.Job{}, fmt.Errorf("start erasure job %s: %w", j.ID, err)
	}
	if active != nil {
		return erasurejob.Job{}, &erasurejob.ErrActiveJob{Job: *active}
	}
	return j, nil
}

// insertJob inserts j unless the user already has an in-progress job, in
// which case the in-progress index makes the insert a no-op. A concurrent
// Start that has inserted but not committed makes this insert wait, so the
// conflict is decided against the committed row rather than a read taken
// before it.
func insertJob(ctx context.Context, tx pgx.Tx, j erasurejob.Job) (bool, error) {
	d, err := encodeDetail(j)
	if err != nil {
		return false, err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO erasure_jobs
		(id, tenant_id, user_id, status, total, failure, started_at, completed_at, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, user_id) WHERE `+inProgressPredicate+` DO NOTHING
		RETURNING id`,
		j.ID, j.TenantID, j.UserID, string(j.Phase), j.Total, j.Failure,
		j.StartedAt, pgtenant.NullTime(j.CompletedAt), d).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, erasurejob.ErrAlreadyExists
		}
		return false, fmt.Errorf("insert erasure job: %w", err)
	}
	return true, nil
}

// Get implements erasurejob.Store.
func (s *Store) Get(ctx context.Context, tenantID, id string) (erasurejob.Job, error) {
	var out erasurejob.Job
	read := func(q string, args ...any) func(pgx.Tx) error {
		return func(tx pgx.Tx) error {
			j, err := scanJob(tx.QueryRow(ctx, q, args...))
			if err != nil {
				return err
			}
			out = j
			return nil
		}
	}
	var err error
	if tenantID == erasurejob.AllTenants {
		err = pgtenant.InAllTenants(ctx, s.pool,
			read(`SELECT `+selectCols+` FROM erasure_jobs WHERE id = $1`, id))
	} else {
		// Another tenant's job reads as not found: the tenant_id predicate
		// filters it even on a connection the RLS policy does not bind.
		err = pgtenant.InTx(ctx, s.pool, tenantID,
			read(`SELECT `+selectCols+` FROM erasure_jobs WHERE tenant_id = $1 AND id = $2`, tenantID, id))
		if errors.Is(err, pgtenant.ErrInvalidTenantID) {
			return erasurejob.Job{}, erasurejob.ErrNotFound
		}
	}
	if err != nil {
		return erasurejob.Job{}, err
	}
	return out, nil
}

// Update implements erasurejob.Store.
func (s *Store) Update(ctx context.Context, id string, mutate func(*erasurejob.Job) error) (erasurejob.Job, error) {
	var out erasurejob.Job
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		j, err := lockJob(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := mutate(&j); err != nil {
			return err
		}
		if err := writeJob(ctx, tx, j); err != nil {
			return err
		}
		out = j
		return nil
	})
	if err != nil {
		return erasurejob.Job{}, err
	}
	return out, nil
}

// Restart implements erasurejob.Store. It locks the job row and updates it
// with no in-progress read of its own: the in-progress index refuses the
// update when another job for the user is in progress, including one whose
// Start or Restart commits while this update waits on the index, and the
// refused transaction rolls back before the in-progress job is read in a
// new one.
func (s *Store) Restart(ctx context.Context, id string) (erasurejob.Job, error) {
	var out erasurejob.Job
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		j, err := lockJob(ctx, tx, id)
		if err != nil {
			return err
		}
		if j.Phase != erasurejob.PhaseFailed {
			return &erasurejob.NotFailedError{Phase: j.Phase}
		}
		erasurejob.RestartJob(&j, s.now())
		if err := writeJob(ctx, tx, j); err != nil {
			return err
		}
		if _, err := userpg.SetErasureRestrictionTx(ctx, tx, j.TenantID, j.UserID, j.ID, true, ""); err != nil {
			return err
		}
		out = j
		return nil
	})
	if isInProgressConflict(err) {
		return erasurejob.Job{}, s.activeJobError(ctx, id)
	}
	if err != nil {
		return erasurejob.Job{}, err
	}
	return out, nil
}

// activeJobError reads, in a new transaction, the in-progress job that
// refused the restart of id and returns it as *ErrActiveJob.
func (s *Store) activeJobError(ctx context.Context, id string) error {
	var active erasurejob.Job
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		j, err := scanJob(tx.QueryRow(ctx, `SELECT `+selectCols+` FROM erasure_jobs WHERE id = $1`, id))
		if err != nil {
			return err
		}
		active, err = activeJobForUser(ctx, tx, j.TenantID, j.UserID)
		return err
	})
	if err != nil {
		return fmt.Errorf("read in-progress erasure job after restart conflict: %w", err)
	}
	return &erasurejob.ErrActiveJob{Job: active}
}

// ClearRestriction implements erasurejob.Store. The job row is locked
// before the phase check, so a concurrent Restart either commits first and
// the check answers ErrNotFailed, or waits and sets the flag again after
// the clear.
func (s *Store) ClearRestriction(ctx context.Context, id string) (erasurejob.Job, error) {
	var out erasurejob.Job
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		j, err := lockJob(ctx, tx, id)
		if err != nil {
			return err
		}
		if j.Phase != erasurejob.PhaseFailed {
			return &erasurejob.NotFailedError{Phase: j.Phase}
		}
		if err := userpg.ClearProcessingRestrictionTx(ctx, tx, j.TenantID, j.UserID, j.ID); err != nil {
			return err
		}
		out = j
		return nil
	})
	if err != nil {
		return erasurejob.Job{}, err
	}
	return out, nil
}

// Complete implements erasurejob.Store. The job is marked completed before
// the flag is cleared in the same transaction, so the migration 0042
// trigger finds no in-progress job for the user and admits the clear
// without a bypass. A users row the erasure removed completes with no flag
// write.
func (s *Store) Complete(ctx context.Context, id string, completedAt time.Time, expect erasurejob.Job) (erasurejob.Job, error) {
	var out erasurejob.Job
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		j, err := lockJob(ctx, tx, id)
		if err != nil {
			return err
		}
		if !erasurejob.SameAttempt(j, expect) {
			return erasurejob.ErrJobMoved
		}
		erasurejob.CompleteJob(&j, completedAt)
		if err := writeJob(ctx, tx, j); err != nil {
			return err
		}
		if _, err := userpg.SetErasureRestrictionTx(ctx, tx, j.TenantID, j.UserID, "", false, j.ID); err != nil &&
			!errors.Is(err, userstore.ErrNotFound) {
			return err
		}
		out = j
		return nil
	})
	if err != nil {
		return erasurejob.Job{}, err
	}
	return out, nil
}

// ListActive implements erasurejob.Store.
func (s *Store) ListActive(ctx context.Context) ([]erasurejob.Job, error) {
	var out []erasurejob.Job
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+selectCols+` FROM erasure_jobs
			WHERE `+inProgressPredicate+` ORDER BY started_at, id`)
		if err != nil {
			return fmt.Errorf("list in-progress erasure jobs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				return err
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// lockJob reads the job row FOR UPDATE.
func lockJob(ctx context.Context, tx pgx.Tx, id string) (erasurejob.Job, error) {
	return scanJob(tx.QueryRow(ctx, `SELECT `+selectCols+` FROM erasure_jobs WHERE id = $1 FOR UPDATE`, id))
}

// activeJobForUser reads the user's in-progress job.
func activeJobForUser(ctx context.Context, tx pgx.Tx, tenantID, userID string) (erasurejob.Job, error) {
	j, err := scanJob(tx.QueryRow(ctx, `SELECT `+selectCols+` FROM erasure_jobs
		WHERE tenant_id = $1 AND user_id = $2 AND `+inProgressPredicate,
		tenantID, userID))
	if err != nil {
		return erasurejob.Job{}, fmt.Errorf("read in-progress erasure job of %s/%s: %w", tenantID, userID, err)
	}
	return j, nil
}

// writeJob writes every mutable column of j.
func writeJob(ctx context.Context, tx pgx.Tx, j erasurejob.Job) error {
	d, err := encodeDetail(j)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE erasure_jobs SET
		status = $3, total = $4, failure = $5, completed_at = $6, detail = $7
	WHERE tenant_id = $1 AND id = $2`,
		j.TenantID, j.ID, string(j.Phase), j.Total, j.Failure,
		pgtenant.NullTime(j.CompletedAt), d); err != nil {
		return fmt.Errorf("write erasure job %s: %w", j.ID, err)
	}
	return nil
}

// isInProgressConflict reports whether err is a unique violation of the
// in-progress index.
func isInProgressConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == inProgressIndex
}

// encodeDetail serializes the detail fields of j.
func encodeDetail(j erasurejob.Job) ([]byte, error) {
	b, err := json.Marshal(detail{
		Deadline:           j.Deadline,
		Deleted:            j.Deleted,
		Billing:            j.Billing,
		DeadLetterRedacted: j.DeadLetterRedacted,
		PhaseLog:           j.PhaseLog,
		InitiatedBy:        j.InitiatedBy,
		OverrideReceipt:    j.OverrideReceipt,
		ErasedAt:           j.ErasedAt,
		ReceiptAt:          j.ReceiptAt,
	})
	if err != nil {
		return nil, fmt.Errorf("encode erasure job %s detail: %w", j.ID, err)
	}
	return b, nil
}

// scanJob reads one row in selectCols order. A missing row is
// erasurejob.ErrNotFound.
func scanJob(row pgx.Row) (erasurejob.Job, error) {
	var (
		j           erasurejob.Job
		status      string
		completedAt *time.Time
		raw         []byte
	)
	err := row.Scan(&j.ID, &j.TenantID, &j.UserID, &status, &j.Total, &j.Failure,
		&j.StartedAt, &completedAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return erasurejob.Job{}, erasurejob.ErrNotFound
	}
	if err != nil {
		return erasurejob.Job{}, fmt.Errorf("scan erasure job: %w", err)
	}
	j.Phase = erasurejob.Phase(status)
	if completedAt != nil {
		j.CompletedAt = *completedAt
	}
	var d detail
	if err := json.Unmarshal(raw, &d); err != nil {
		return erasurejob.Job{}, fmt.Errorf("decode erasure job %s detail: %w", j.ID, err)
	}
	j.Deadline = d.Deadline
	j.Deleted = d.Deleted
	j.Billing = d.Billing
	j.DeadLetterRedacted = d.DeadLetterRedacted
	j.PhaseLog = d.PhaseLog
	j.InitiatedBy = d.InitiatedBy
	j.OverrideReceipt = d.OverrideReceipt
	j.ErasedAt = d.ErasedAt
	j.ReceiptAt = d.ReceiptAt
	return j, nil
}
