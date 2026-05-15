// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed billingstore.Store over the
// append-only billing_events table. It is the durable §11.2.1 billing
// ledger; billingstore.Memory is the in-memory alternative.
//
// billing_events is tenant-scoped and append-only: every operation
// runs inside a transaction that sets app.current_tenant for the
// §12.3 lenny_tenant_guard trigger and the RLS policy, and the
// lenny_billing_immutability trigger rejects any update or delete.
// Append serializes the per-tenant sequence_number assignment with a
// transaction advisory lock so concurrent writers cannot collide on a
// sequence value.
package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed billing event ledger. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ billingstore.Store = (*Store)(nil)

const selectList = `sequence_number, schema_version, user_id, session_id,
	experiment_id, variant_id, event_type, tokens_input, tokens_output, created_at`

// Append commits a billing event to the tenant's ledger and returns
// the sealed event. The per-tenant advisory lock makes the tail read
// and the insert atomic with respect to other writers; the
// (tenant_id, sequence_number) primary key is the hard backstop.
func (s *Store) Append(ctx context.Context, e billingstore.Event) (billingstore.Event, error) {
	if err := billingstore.Validate(e); err != nil {
		return billingstore.Event{}, err
	}
	e = billingstore.Normalize(e, time.Now())
	var committed billingstore.Event
	err := pgtenant.InTx(ctx, s.pool, e.TenantID, func(tx pgx.Tx) error {
		// Serialize sequence assignment for this tenant; other tenants
		// proceed in parallel because the lock key is per tenant.
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1))`, "billing:"+e.TenantID); err != nil {
			return err
		}
		var tail int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(sequence_number), 0) FROM billing_events WHERE tenant_id = $1`,
			e.TenantID).Scan(&tail); err != nil {
			return err
		}
		e.SequenceNumber = uint64(tail) + 1
		if _, err := tx.Exec(ctx, `INSERT INTO billing_events (
			tenant_id, sequence_number, schema_version, user_id, session_id,
			experiment_id, variant_id, event_type, tokens_input, tokens_output, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			e.TenantID, int64(e.SequenceNumber), int32(e.SchemaVersion), e.UserID,
			pgtenant.NullString(e.SessionID), e.ExperimentID, e.VariantID,
			string(e.EventType), int64(e.TokensInput), int64(e.TokensOutput), e.CreatedAt); err != nil {
			return err
		}
		committed = e
		return nil
	})
	if err != nil {
		return billingstore.Event{}, err
	}
	return committed, nil
}

// Since returns the tenant's events with sequence_number greater than
// since, in ascending sequence order, capped at limit.
func (s *Store) Since(ctx context.Context, tenantID string, since uint64, limit int) ([]billingstore.Event, error) {
	var out []billingstore.Event
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		q := `SELECT ` + selectList + ` FROM billing_events
			WHERE tenant_id = $1 AND sequence_number > $2
			ORDER BY sequence_number`
		args := []any{tenantID, int64(since)}
		if limit > 0 {
			q += ` LIMIT $3`
			args = append(args, limit)
		}
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows, tenantID)
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

// scanEvent reads one row in selectList order into an Event.
func scanEvent(row pgx.Row, tenantID string) (billingstore.Event, error) {
	var (
		e         billingstore.Event
		seq       int64
		schemaVer int32
		sessionID *string
		eventType string
		tokensIn  int64
		tokensOut int64
	)
	if err := row.Scan(&seq, &schemaVer, &e.UserID, &sessionID,
		&e.ExperimentID, &e.VariantID, &eventType, &tokensIn, &tokensOut, &e.CreatedAt); err != nil {
		return billingstore.Event{}, err
	}
	e.TenantID = tenantID
	e.SequenceNumber = uint64(seq)
	e.SchemaVersion = uint32(schemaVer)
	if sessionID != nil {
		e.SessionID = *sessionID
	}
	e.EventType = billingstore.EventType(eventType)
	e.TokensInput = uint64(tokensIn)
	e.TokensOutput = uint64(tokensOut)
	e.CreatedAt = e.CreatedAt.UTC()
	return e, nil
}
