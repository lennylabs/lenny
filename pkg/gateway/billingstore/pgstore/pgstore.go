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
	"errors"
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
	experiment_id, variant_id, event_type, tokens_input, tokens_output,
	pod_minutes, corrects_sequence, correction_reason_code, correction_detail,
	environment_id, created_at`

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
			experiment_id, variant_id, event_type, tokens_input, tokens_output,
			pod_minutes, corrects_sequence, correction_reason_code, correction_detail,
			environment_id, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			e.TenantID, int64(e.SequenceNumber), int32(e.SchemaVersion), e.UserID,
			pgtenant.NullString(e.SessionID), e.ExperimentID, e.VariantID,
			string(e.EventType), int64(e.TokensInput), int64(e.TokensOutput),
			e.PodMinutes, correctsSequence(e), string(e.CorrectionReasonCode),
			e.CorrectionDetail, e.EnvironmentID, e.CreatedAt); err != nil {
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
		e          billingstore.Event
		seq        int64
		schemaVer  int32
		sessionID  *string
		eventType  string
		tokensIn   int64
		tokensOut  int64
		correctsTo *int64
		reasonCode string
		detail     string
	)
	if err := row.Scan(&seq, &schemaVer, &e.UserID, &sessionID,
		&e.ExperimentID, &e.VariantID, &eventType, &tokensIn, &tokensOut,
		&e.PodMinutes, &correctsTo, &reasonCode, &detail, &e.EnvironmentID,
		&e.CreatedAt); err != nil {
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
	if correctsTo != nil {
		e.CorrectsSequence = uint64(*correctsTo)
	}
	e.CorrectionReasonCode = billingstore.ReasonCode(reasonCode)
	e.CorrectionDetail = detail
	e.CreatedAt = e.CreatedAt.UTC()
	return e, nil
}

// correctsSequence maps a billing event's CorrectsSequence to the
// nullable column value: NULL for a non-correction event (sequence
// numbers start at 1, so zero is never a real reference), the integer
// value otherwise.
func correctsSequence(e billingstore.Event) any {
	if e.CorrectsSequence == 0 {
		return nil
	}
	return int64(e.CorrectsSequence)
}

// InsertFromStream commits a billing event reclaimed from the §11.2.1
// failover Redis stream, keyed by the Redis stream entry id for
// idempotency. It is the redisstream.Inserter implementation: the
// INSERT ... ON CONFLICT (tenant_id, stream_entry_id) DO NOTHING means a
// reclaimed entry a crashed consumer had already inserted is a no-op, so
// the reclaiming consumer can safely acknowledge and delete the entry.
//
// The flusher acquires the authoritative per-tenant sequence number at
// flush time (§11.2.1) — the provisional number stamped during the
// outage is discarded — so InsertFromStream re-derives the sequence
// under the per-tenant advisory lock exactly like Append.
func (s *Store) InsertFromStream(ctx context.Context, e billingstore.Event, streamEntryID string) error {
	if err := billingstore.Validate(e); err != nil {
		return err
	}
	e = billingstore.Normalize(e, time.Now())
	return pgtenant.InTx(ctx, s.pool, e.TenantID, func(tx pgx.Tx) error {
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
		// §11.2.1: ON CONFLICT (tenant_id, stream_entry_id) DO NOTHING
		// makes a redelivered stream entry idempotent.
		_, err := tx.Exec(ctx, `INSERT INTO billing_events (
			tenant_id, sequence_number, schema_version, user_id, session_id,
			experiment_id, variant_id, event_type, tokens_input, tokens_output,
			pod_minutes, corrects_sequence, correction_reason_code, correction_detail,
			environment_id, stream_entry_id, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (tenant_id, stream_entry_id) DO NOTHING`,
			e.TenantID, int64(e.SequenceNumber), int32(e.SchemaVersion), e.UserID,
			pgtenant.NullString(e.SessionID), e.ExperimentID, e.VariantID,
			string(e.EventType), int64(e.TokensInput), int64(e.TokensOutput),
			e.PodMinutes, correctsSequence(e), string(e.CorrectionReasonCode),
			e.CorrectionDetail, e.EnvironmentID, streamEntryID, e.CreatedAt)
		return err
	})
}

// PseudonymizeUser rewrites every billing event in tenantID owned by
// userID to the §12.8 salted-hash pseudonym and returns the count
// rewritten. It is idempotent: a second call finds no event still keyed
// to userID.
//
// billing_events is append-only; the §11.7 lenny_billing_immutability
// trigger permits the UPDATE only when the transaction has set
// lenny.erasure_mode = 'true'. The connection must additionally run as
// the lenny_erasure role, which is the only role granted
// UPDATE (user_id) ON billing_events (migration 0002); the lenny_app
// pool used for normal writes is intentionally not authorized to
// rewrite the ledger. Wiring that dedicated erasure-role connection
// into the §12.8 orchestrator is the F-12.2.16 follow-up — the durable
// pseudonymize path is implemented here so the §12.1 contract is
// satisfied and the orchestrator has a real method to call once the
// erasure-role pool is supplied.
//
// spec: §12.8 tenant-controlled billing erasure; §11.7 immutability.
func (s *Store) PseudonymizeUser(ctx context.Context, tenantID, userID string, salt []byte) (int, error) {
	if tenantID == "" || userID == "" || len(salt) == 0 {
		return 0, billingstore.ErrPseudonymizeArg
	}
	pseudonym := billingstore.Pseudonymize(userID, salt)
	var n int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE billing_events SET user_id = $3 WHERE tenant_id = $1 AND user_id = $2`,
			tenantID, userID, pseudonym)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// CountUser returns the number of billing events in tenantID still keyed
// to userID. The §12.8 post-pseudonymization verification calls it to
// confirm the rewrite removed every reference to the original user id.
// It is a read, so it runs under the normal SELECT grant.
//
// spec: §12.8 erasure verification.
func (s *Store) CountUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("billingstore/pgstore: CountUser requires non-empty tenant_id and user_id")
	}
	var n int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM billing_events WHERE tenant_id = $1 AND user_id = $2`,
			tenantID, userID).Scan(&n)
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// DeleteByUser implements the §12.1 mandatory-erasure primitive. Billing
// events are append-only and the user-erasure path pseudonymizes (see
// PseudonymizeUser) rather than deletes, so DeleteByUser is a no-op that
// returns (0, nil). The method is mandatory at the interface level so
// the §12.1 compile-time contract holds.
//
// spec: §12.1 line 5, §12.8.
func (s *Store) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("billingstore/pgstore: DeleteByUser requires non-empty tenant_id and user_id")
	}
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure primitive. It
// hard-deletes every billing event owned by tenantID — the §12.8
// Phase 4 tenant-teardown path. The §11.2.1 immutability constraint
// does not apply to a tenant being torn down.
//
// As with PseudonymizeUser, billing_events is append-only: the
// lenny_billing_immutability trigger permits the DELETE only under
// lenny.erasure_mode, and the lenny_erasure role must hold
// GRANT DELETE ON billing_events (migration 0093). The erasure-role
// connection wiring is the F-12.2.16 follow-up.
//
// spec: §12.1 line 5, §12.8 Phase 4; §11.7 immutability.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("billingstore/pgstore: DeleteByTenant requires a concrete tenant_id")
	}
	var n int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`DELETE FROM billing_events WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
