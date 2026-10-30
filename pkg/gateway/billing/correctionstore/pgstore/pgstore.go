// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed correctionstore.Store over the
// billing_correction_pending table. It is the durable §11.2.1
// "Category 2 — Operator-initiated manual corrections" registry;
// correctionstore.Memory is the in-memory alternative.
//
// The §11.2.1 dual-control workflow is platform-admin operated and
// spans tenants (the approve/reject endpoints address a request by its
// opaque approval_request_id without a tenant scope), so the table is
// platform-operational state rather than tenant-isolated: it carries no
// lenny_tenant_guard trigger and no RLS policy, and this store operates
// on the pool directly without a per-tenant transaction context.
// tenant_id is a data column. The committed correction still lands in
// the RLS-protected, append-only billing_events ledger through
// billingstore; this table holds only the pending request and its
// approval outcome so a gateway restart does not lose a pending
// dual-control request or its four-eyes audit trail.
package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/correctionstore"
)

// Store is the Postgres-backed pending billing-correction registry.
// Construct with New.
type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

var _ correctionstore.Store = (*Store)(nil)

// New returns a Store over pool. The pool must address a database that
// has the migrations/ schema applied (billing_correction_pending and
// the tenants table its tenant_id references both live on the primary
// instance).
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, clock: func() time.Time { return time.Now().UTC() }}
}

// NewWithClock returns a Store with an injected clock, for tests that
// need a deterministic SubmittedAt.
func NewWithClock(pool *pgxpool.Pool, clock func() time.Time) *Store {
	s := New(pool)
	if clock != nil {
		s.clock = clock
	}
	return s
}

const selectCols = `id, tenant_id, corrects_sequence, reason_code, detail,
	tokens_input, tokens_output, pod_minutes, state, submitted_by,
	decided_by, dual_control, committed_sequence, submitted_at, decided_at`

// newID returns a random 128-bit hex identifier — the §11.2.1
// approval_request_id.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Create implements correctionstore.Store. It records a new request in
// the pending state with a freshly assigned id and SubmittedAt.
func (s *Store) Create(ctx context.Context, c correctionstore.PendingCorrection) (correctionstore.PendingCorrection, error) {
	id, err := newID()
	if err != nil {
		return correctionstore.PendingCorrection{}, err
	}
	c.ID = id
	c.State = correctionstore.StatePending
	c.SubmittedAt = s.clock()
	c.DecidedBy = ""
	c.DecidedAt = time.Time{}
	c.CommittedSequence = 0
	_, err = s.pool.Exec(ctx,
		`INSERT INTO billing_correction_pending (
			id, tenant_id, corrects_sequence, reason_code, detail,
			tokens_input, tokens_output, pod_minutes, state, submitted_by,
			decided_by, dual_control, committed_sequence, submitted_at, decided_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		c.ID, c.TenantID, int64(c.CorrectsSequence), string(c.ReasonCode), c.Detail,
		int64(c.TokensInput), int64(c.TokensOutput), c.PodMinutes, string(c.State), c.SubmittedBy,
		c.DecidedBy, c.DualControl, int64(c.CommittedSequence), c.SubmittedAt, decidedAtArg(c.DecidedAt))
	if err != nil {
		return correctionstore.PendingCorrection{}, fmt.Errorf("correctionstore/pgstore: insert: %w", err)
	}
	return c, nil
}

// Get implements correctionstore.Store.
func (s *Store) Get(ctx context.Context, id string) (correctionstore.PendingCorrection, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+selectCols+` FROM billing_correction_pending WHERE id = $1`, id)
	c, err := scanCorrection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return correctionstore.PendingCorrection{}, correctionstore.ErrNotFound
	}
	if err != nil {
		return correctionstore.PendingCorrection{}, fmt.Errorf("correctionstore/pgstore: get: %w", err)
	}
	return c, nil
}

// List implements correctionstore.Store. Rows are returned newest
// first, filtered by f.TenantID and f.State when set.
func (s *Store) List(ctx context.Context, f correctionstore.Filter) ([]correctionstore.PendingCorrection, error) {
	q := `SELECT ` + selectCols + ` FROM billing_correction_pending`
	var args []any
	var conds []string
	if f.TenantID != "" {
		args = append(args, f.TenantID)
		conds = append(conds, fmt.Sprintf("tenant_id = $%d", len(args)))
	}
	if f.State != "" {
		args = append(args, string(f.State))
		conds = append(conds, fmt.Sprintf("state = $%d", len(args)))
	}
	q += whereClause(conds) + " ORDER BY submitted_at DESC"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("correctionstore/pgstore: list: %w", err)
	}
	defer rows.Close()
	var out []correctionstore.PendingCorrection
	for rows.Next() {
		c, serr := scanCorrection(rows)
		if serr != nil {
			return nil, fmt.Errorf("correctionstore/pgstore: scan: %w", serr)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("correctionstore/pgstore: list rows: %w", err)
	}
	return out, nil
}

// Transition implements correctionstore.Store. The UPDATE's
// `WHERE state = 'pending'` guard makes the transition atomic against a
// concurrent decision: a request already approved, rejected, or expired
// matches no row, so a double approval is rejected with ErrNotPending.
// The mutate callback stamps DecidedBy, DecidedAt, and (on approval)
// CommittedSequence onto the row read inside the same transaction so
// the state change and those fields commit together.
func (s *Store) Transition(ctx context.Context, id string, to correctionstore.State, mutate func(*correctionstore.PendingCorrection)) (correctionstore.PendingCorrection, error) {
	var out correctionstore.PendingCorrection
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+selectCols+` FROM billing_correction_pending WHERE id = $1 FOR UPDATE`, id)
		c, serr := scanCorrection(row)
		if errors.Is(serr, pgx.ErrNoRows) {
			return correctionstore.ErrNotFound
		}
		if serr != nil {
			return serr
		}
		if c.State != correctionstore.StatePending {
			return correctionstore.ErrNotPending
		}
		c.State = to
		if mutate != nil {
			mutate(&c)
		}
		if _, uerr := tx.Exec(ctx,
			`UPDATE billing_correction_pending
			 SET state = $2, decided_by = $3, committed_sequence = $4, decided_at = $5
			 WHERE id = $1 AND state = 'pending'`,
			id, string(c.State), c.DecidedBy, int64(c.CommittedSequence), decidedAtArg(c.DecidedAt)); uerr != nil {
			return uerr
		}
		out = c
		return nil
	})
	if err != nil {
		if errors.Is(err, correctionstore.ErrNotFound) || errors.Is(err, correctionstore.ErrNotPending) {
			return correctionstore.PendingCorrection{}, err
		}
		return correctionstore.PendingCorrection{}, fmt.Errorf("correctionstore/pgstore: transition: %w", err)
	}
	return out, nil
}

// Counts implements correctionstore.Store. It returns the number of
// requests in each §11.2.1 state — the source of the
// lenny_billing_correction_pending_total metric — with every state
// present (zero when none).
func (s *Store) Counts(ctx context.Context) (map[correctionstore.State]int, error) {
	counts := make(map[correctionstore.State]int, len(correctionstore.AllStates()))
	for _, st := range correctionstore.AllStates() {
		counts[st] = 0
	}
	rows, err := s.pool.Query(ctx, `SELECT state, count(*) FROM billing_correction_pending GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("correctionstore/pgstore: counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if serr := rows.Scan(&st, &n); serr != nil {
			return nil, fmt.Errorf("correctionstore/pgstore: counts scan: %w", serr)
		}
		counts[correctionstore.State(st)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("correctionstore/pgstore: counts rows: %w", err)
	}
	return counts, nil
}

// scanRow is the minimal surface scanCorrection needs from a pgx.Row or
// a pgx.Rows; both satisfy it.
type scanRow interface {
	Scan(dest ...any) error
}

// scanCorrection maps one billing_correction_pending row into a
// PendingCorrection. decided_at is nullable (NULL while pending), so it
// scans into a *time.Time and a nil result becomes the zero time the
// in-memory store also uses for an undecided request.
func scanCorrection(row scanRow) (correctionstore.PendingCorrection, error) {
	var (
		c                                              correctionstore.PendingCorrection
		correctsSeq, tokensIn, tokensOut, committedSeq int64
		reasonCode, state                              string
		decidedAt                                      *time.Time
	)
	if err := row.Scan(
		&c.ID, &c.TenantID, &correctsSeq, &reasonCode, &c.Detail,
		&tokensIn, &tokensOut, &c.PodMinutes, &state, &c.SubmittedBy,
		&c.DecidedBy, &c.DualControl, &committedSeq, &c.SubmittedAt, &decidedAt,
	); err != nil {
		return correctionstore.PendingCorrection{}, err
	}
	c.CorrectsSequence = uint64(correctsSeq)
	c.TokensInput = uint64(tokensIn)
	c.TokensOutput = uint64(tokensOut)
	c.CommittedSequence = uint64(committedSeq)
	c.ReasonCode = billingstore.ReasonCode(reasonCode)
	c.State = correctionstore.State(state)
	if decidedAt != nil {
		c.DecidedAt = decidedAt.UTC()
	}
	return c, nil
}

// decidedAtArg renders DecidedAt for the wire: the zero time (an
// undecided request) is stored as SQL NULL so it round-trips back to
// the zero value the in-memory store uses.
func decidedAtArg(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// whereClause joins conditions into a SQL WHERE clause, or returns the
// empty string when there are none.
func whereClause(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	out := " WHERE " + conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out
}
