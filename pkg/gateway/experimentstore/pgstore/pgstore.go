// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed experimentstore.Store,
// persisting the §10.7 per-tenant experiment registry to the
// experiment_definitions table. It is a drop-in alternative to
// experimentstore.Memory.
//
// experiment_definitions is tenant-scoped, so every operation runs
// inside a transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy.
//
// The scalar fields (status, base_runtime, the audit timestamps) are
// typed columns. The nested ExperimentDefinition body — the variant
// list and the targeting / child-propagation policy — is stored as a
// single jsonb document, mirroring how the session store persists its
// workspace plan.
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

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed experiment registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ experimentstore.Store = (*Store)(nil)

// selectList is the column projection for reads.
const selectList = `tenant_id, id, status, base_runtime, body,
	created_at, updated_at`

// body is the jsonb document persisted in the body column. It carries
// the nested ExperimentDefinition fields that are not promoted to
// typed columns.
type body struct {
	Variants      []experimentstore.Variant `json:"variants"`
	TargetingMode experiment.TargetingMode  `json:"targetingMode"`
	Sticky        experiment.Sticky         `json:"sticky"`
	Propagation   experiment.Propagation    `json:"propagation"`
}

// Create inserts a new experiment row. It runs the §10.7 admission
// validation first, mirroring experimentstore.Memory. Returns
// ErrAlreadyExists when the (tenant, id) pair is taken.
func (s *Store) Create(ctx context.Context, e experimentstore.Experiment) error {
	if err := e.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = e.CreatedAt
	}
	bodyJSON, err := encodeBody(e)
	if err != nil {
		return err
	}
	err = pgtenant.InTx(ctx, s.pool, e.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO experiment_definitions (
			tenant_id, id, status, base_runtime, body,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)`,
			e.TenantID, e.ID, string(e.Status), e.BaseRuntime, bodyJSON,
			e.CreatedAt, e.UpdatedAt)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return experimentstore.ErrAlreadyExists
	}
	return err
}

// Get returns the experiment row keyed by (tenantID, id). A
// cross-tenant miss is indistinguishable from a missing row (§10.7
// per-tenant isolation).
func (s *Store) Get(ctx context.Context, tenantID, id string) (experimentstore.Experiment, error) {
	var out experimentstore.Experiment
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM experiment_definitions
			 WHERE tenant_id = $1 AND id = $2`,
			tenantID, id)
		e, err := scanExperiment(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return experimentstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = e
		return nil
	})
	if err != nil {
		return experimentstore.Experiment{}, err
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-runs the §10.7 admission validation on the result, and advances
// UpdatedAt. UpdatedAt strictly advances on every successful Update
// (clamped to the prior value + 1µs, the Postgres timestamptz
// resolution) so callers can rely on monotonicity. The mutate error
// is propagated verbatim and aborts the write.
func (s *Store) Update(ctx context.Context, tenantID, id string, mutate func(*experimentstore.Experiment) error) (experimentstore.Experiment, error) {
	var out experimentstore.Experiment
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM experiment_definitions
			 WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			tenantID, id)
		e, err := scanExperiment(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return experimentstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		prev := e.UpdatedAt
		if err := mutate(&e); err != nil {
			return err
		}
		if err := e.Validate(); err != nil {
			return err
		}
		e.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
		bodyJSON, err := encodeBody(e)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE experiment_definitions SET
			status = $3, base_runtime = $4, body = $5::jsonb, updated_at = $6
		WHERE tenant_id = $1 AND id = $2`,
			tenantID, id, string(e.Status), e.BaseRuntime, bodyJSON,
			e.UpdatedAt); err != nil {
			return err
		}
		out = e
		return nil
	})
	if err != nil {
		return experimentstore.Experiment{}, err
	}
	return out, nil
}

// List returns the tenant's experiments id-ascending.
func (s *Store) List(ctx context.Context, tenantID string) ([]experimentstore.Experiment, error) {
	var out []experimentstore.Experiment
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM experiment_definitions
			 WHERE tenant_id = $1 ORDER BY id`,
			tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanExperiment(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the experiment row. Returns ErrNotFound when no
// matching row exists.
func (s *Store) Delete(ctx context.Context, tenantID, id string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM experiment_definitions WHERE tenant_id = $1 AND id = $2`,
			tenantID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return experimentstore.ErrNotFound
		}
		return nil
	})
}

// scanExperiment reads one row in selectList order into an Experiment.
func scanExperiment(row pgx.Row) (experimentstore.Experiment, error) {
	var (
		e        experimentstore.Experiment
		status   string
		bodyJSON []byte
	)
	if err := row.Scan(
		&e.TenantID, &e.ID, &status, &e.BaseRuntime, &bodyJSON,
		&e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return experimentstore.Experiment{}, err
	}
	e.Status = experiment.Status(status)
	if len(bodyJSON) > 0 {
		var b body
		if err := json.Unmarshal(bodyJSON, &b); err != nil {
			return experimentstore.Experiment{}, fmt.Errorf("experimentstore: decode body: %w", err)
		}
		e.Variants = b.Variants
		e.TargetingMode = b.TargetingMode
		e.Sticky = b.Sticky
		e.Propagation = b.Propagation
	}
	return e, nil
}

// encodeBody marshals the nested ExperimentDefinition fields into the
// JSON document persisted in the body jsonb column.
func encodeBody(e experimentstore.Experiment) (string, error) {
	b, err := json.Marshal(body{
		Variants:      e.Variants,
		TargetingMode: e.TargetingMode,
		Sticky:        e.Sticky,
		Propagation:   e.Propagation,
	})
	if err != nil {
		return "", fmt.Errorf("experimentstore: encode body: %w", err)
	}
	return string(b), nil
}
