// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed interceptorstore.Store,
// persisting the §4.8 external-interceptor registry to the interceptors
// table. It is a drop-in alternative to interceptorstore.Memory.
//
// Interceptors are platform-scoped cluster infrastructure (no
// tenant_id), like the runtime and pool registries, so reads and writes
// run directly against the pool without a per-tenant RLS context. The
// §8.3 SEC-013 server-minted fields (fail_open_transition_at,
// cooldown_seconds_at_transition) are ordinary columns the admin write
// path controls; the admin handler never copies them from a request
// body, satisfying the SEC-013 "admin-API-immutable" requirement at the
// application boundary.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/interceptorstore"
)

// Store is the Postgres-backed interceptor registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ interceptorstore.Store = (*Store)(nil)

const selectList = `name, endpoint, priority, fail_policy, timeout_ms, phases,
	fail_open_transition_at, cooldown_seconds_at_transition, created_at, updated_at, version`

// Create inserts a new interceptor row after running the §4.8
// validation. Returns ErrAlreadyExists when the name collides.
func (s *Store) Create(ctx context.Context, ic interceptorstore.Interceptor) error {
	if err := interceptorstore.Validate(ic); err != nil {
		return err
	}
	now := time.Now().UTC()
	if ic.CreatedAt.IsZero() {
		ic.CreatedAt = now
	}
	if ic.UpdatedAt.IsZero() {
		ic.UpdatedAt = ic.CreatedAt
	}
	if ic.Version == 0 {
		ic.Version = 1
	}
	phases, err := phasesJSON(ic.Phases)
	if err != nil {
		return err
	}
	_, ierr := s.pool.Exec(ctx, `INSERT INTO interceptors (
		name, endpoint, priority, fail_policy, timeout_ms, phases,
		fail_open_transition_at, cooldown_seconds_at_transition, created_at, updated_at, version
	) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11)`,
		ic.Name, ic.Endpoint, ic.Priority, string(ic.FailPolicy), ic.TimeoutMs, phases,
		nullTime(ic.FailOpenTransitionAt), nullInt(ic.CooldownSecondsAtTransition),
		ic.CreatedAt, ic.UpdatedAt, ic.Version)
	var pgErr *pgconn.PgError
	if errors.As(ierr, &pgErr) && pgErr.Code == "23505" {
		return interceptorstore.ErrAlreadyExists
	}
	return ierr
}

// Get returns the interceptor keyed by name. A missing row is
// ErrNotFound.
func (s *Store) Get(ctx context.Context, name string) (interceptorstore.Interceptor, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+selectList+` FROM interceptors WHERE name = $1`, name)
	ic, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return interceptorstore.Interceptor{}, interceptorstore.ErrNotFound
	}
	return ic, err
}

// Update applies mutate to the stored row and persists the result with
// an incremented version inside a single transaction.
func (s *Store) Update(ctx context.Context, name string, mutate func(*interceptorstore.Interceptor) error) (interceptorstore.Interceptor, error) {
	var out interceptorstore.Interceptor
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+selectList+` FROM interceptors WHERE name = $1 FOR UPDATE`, name)
		cur, serr := scan(row)
		if errors.Is(serr, pgx.ErrNoRows) {
			return interceptorstore.ErrNotFound
		}
		if serr != nil {
			return serr
		}
		next := cur
		if merr := mutate(&next); merr != nil {
			return merr
		}
		next.Name = name
		if verr := interceptorstore.Validate(next); verr != nil {
			return verr
		}
		next.Version = cur.Version + 1
		next.CreatedAt = cur.CreatedAt
		next.UpdatedAt = time.Now().UTC()
		phases, perr := phasesJSON(next.Phases)
		if perr != nil {
			return perr
		}
		if _, eerr := tx.Exec(ctx, `UPDATE interceptors SET
			endpoint = $2, priority = $3, fail_policy = $4, timeout_ms = $5, phases = $6::jsonb,
			fail_open_transition_at = $7, cooldown_seconds_at_transition = $8,
			updated_at = $9, version = $10
			WHERE name = $1`,
			name, next.Endpoint, next.Priority, string(next.FailPolicy), next.TimeoutMs, phases,
			nullTime(next.FailOpenTransitionAt), nullInt(next.CooldownSecondsAtTransition),
			next.UpdatedAt, next.Version); eerr != nil {
			return eerr
		}
		out = next
		return nil
	})
	if err != nil {
		return interceptorstore.Interceptor{}, err
	}
	return out, nil
}

// List returns every interceptor sorted by name.
func (s *Store) List(ctx context.Context) ([]interceptorstore.Interceptor, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+selectList+` FROM interceptors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []interceptorstore.Interceptor
	for rows.Next() {
		ic, serr := scan(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, ic)
	}
	return out, rows.Err()
}

// Delete removes the interceptor row. A missing row is ErrNotFound. The
// §8.3 rule-6 dependency guard is enforced at the admin layer.
func (s *Store) Delete(ctx context.Context, name string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM interceptors WHERE name = $1`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return interceptorstore.ErrNotFound
	}
	return nil
}

// scanner abstracts pgx.Row and a single pgx.Rows step.
type scanner interface {
	Scan(dest ...any) error
}

func scan(r scanner) (interceptorstore.Interceptor, error) {
	var (
		ic         interceptorstore.Interceptor
		failPolicy string
		phasesRaw  []byte
		transition *time.Time
		cooldown   *int
	)
	if err := r.Scan(&ic.Name, &ic.Endpoint, &ic.Priority, &failPolicy, &ic.TimeoutMs,
		&phasesRaw, &transition, &cooldown, &ic.CreatedAt, &ic.UpdatedAt, &ic.Version); err != nil {
		return interceptorstore.Interceptor{}, err
	}
	ic.FailPolicy = interceptor.FailPolicy(failPolicy)
	if len(phasesRaw) > 0 {
		var names []string
		if err := json.Unmarshal(phasesRaw, &names); err != nil {
			return interceptorstore.Interceptor{}, err
		}
		for _, n := range names {
			ic.Phases = append(ic.Phases, interceptor.Phase(n))
		}
	}
	if transition != nil {
		ic.FailOpenTransitionAt = transition.UTC()
	}
	if cooldown != nil {
		ic.CooldownSecondsAtTransition = *cooldown
	}
	return ic, nil
}

func phasesJSON(phases []interceptor.Phase) ([]byte, error) {
	names := make([]string, 0, len(phases))
	for _, p := range phases {
		names = append(names, string(p))
	}
	return json.Marshal(names)
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
