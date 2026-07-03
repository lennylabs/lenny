// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed billingstore.Store over the
// append-only billing_events table. It is the durable §11.2.1 billing
// ledger; billingstore.Memory is the in-memory alternative.
//
// billing_events is tenant-scoped and append-only: every operation
// runs inside a transaction that sets app.current_tenant for the
// §12.3 lenny_tenant_guard trigger and the RLS policy, and the
// lenny_billing_immutability trigger rejects any update or delete.
// Append assigns the per-tenant sequence_number by nextval on the
// dedicated §11.2.1 per-tenant Postgres sequence (the §10.2
// length-bounded safe-derived billing_seq_<40hex> name), which the
// tenant-create path provisions before the first billing event. The
// sequence's counter is independent of the table rows, so it retains
// monotonicity across the retention sweep and §12.8 teardown deletes
// that a MAX(sequence_number)+1 scheme cannot survive.
//
// spec: §11.2.1, §10.2.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// Router is the §12.3 R-03 routing surface this ledger depends on: it
// resolves the Postgres pool for a tenant's billing-event writes.
// *storerouter.SingleShardRouter satisfies it. The store never holds a
// raw *pgxpool.Pool, so a future shard split rotates only the router
// implementation and no billing-write call site changes.
//
// spec: §12.3 R-03 line 144 — all billing event inserts MUST be routed
// through the StoreRouter interface rather than accessing a Postgres
// pool directly.
type Router interface {
	BillingShard(ctx context.Context, tenantID storerouter.TenantID) (*pgxpool.Pool, error)
}

// Store is the Postgres-backed billing event ledger. Construct with New.
type Store struct {
	router Router
}

// New returns a Store that routes every billing write through router
// (§12.3 R-03). The router must resolve to a database that has the
// migrations/ schema applied.
func New(router Router) *Store { return &Store{router: router} }

// shard resolves the billing Postgres pool for tenantID through the
// §12.3 R-03 router.
func (s *Store) shard(ctx context.Context, tenantID string) (*pgxpool.Pool, error) {
	return s.router.BillingShard(ctx, storerouter.TenantID(tenantID))
}

var _ billingstore.Store = (*Store)(nil)

const selectList = `sequence_number, schema_version, user_id, session_id,
	experiment_id, variant_id, event_type, tokens_input, tokens_output,
	pod_minutes, corrects_sequence, correction_reason_code, correction_detail,
	environment_id, conditional_fields, labels, created_at`

// Append commits a billing event to the tenant's ledger and returns
// the sealed event. The per-tenant sequence_number is drawn by nextval
// on the dedicated §11.2.1 Postgres sequence inside the INSERT
// transaction, so concurrent writers each get a distinct value without
// a per-tenant advisory lock; the (tenant_id, sequence_number) primary
// key is the hard backstop. A rolled-back INSERT leaves a sequence gap
// the §11.2.1 replay mechanism tolerates. The sequence retains its
// counter across the retention sweep and §12.8 teardown deletes, so it
// never regresses below a value it already issued.
//
// spec: §11.2.1, §10.2.
func (s *Store) Append(ctx context.Context, e billingstore.Event) (billingstore.Event, error) {
	if err := billingstore.Validate(e); err != nil {
		return billingstore.Event{}, err
	}
	e = billingstore.Normalize(e, time.Now())
	pool, err := s.shard(ctx, e.TenantID)
	if err != nil {
		return billingstore.Event{}, err
	}
	var committed billingstore.Event
	err = pgtenant.InTx(ctx, pool, e.TenantID, func(tx pgx.Tx) error {
		// §11.2.1: draw the sequence_number from the dedicated per-tenant
		// Postgres sequence (the §10.2 length-bounded safe-derived
		// billing_seq_<40hex> name) inside the INSERT transaction. nextval
		// serializes across concurrent writers without an advisory lock, and
		// the sequence retains its counter across the retention and teardown
		// deletes that a MAX(sequence_number)+1 read cannot survive. A
		// rolled-back INSERT leaves a gap the §11.2.1 replay mechanism
		// tolerates. The name is a seqname-derived identifier (literal prefix
		// plus 40-hex digest), injection-safe by construction and bound as a
		// value here because nextval takes a regclass argument.
		var seq int64
		if err := tx.QueryRow(ctx,
			`SELECT nextval($1)`, seqname.BillingSequenceName(e.TenantID)).Scan(&seq); err != nil {
			return err
		}
		e.SequenceNumber = uint64(seq)
		conditional, err := conditionalValue(e)
		if err != nil {
			return err
		}
		labels, err := labelsValue(e.Labels)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO billing_events (
			tenant_id, sequence_number, schema_version, user_id, session_id,
			experiment_id, variant_id, event_type, tokens_input, tokens_output,
			pod_minutes, corrects_sequence, correction_reason_code, correction_detail,
			environment_id, conditional_fields, labels, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
			e.TenantID, int64(e.SequenceNumber), int32(e.SchemaVersion), e.UserID,
			pgtenant.NullString(e.SessionID), e.ExperimentID, e.VariantID,
			string(e.EventType), int64(e.TokensInput), int64(e.TokensOutput),
			e.PodMinutes, correctsSequence(e), string(e.CorrectionReasonCode),
			e.CorrectionDetail, e.EnvironmentID, conditional, labels, e.CreatedAt); err != nil {
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
	return s.SinceFiltered(ctx, tenantID, since, limit, nil)
}

// SinceFiltered returns the tenant's events with sequence_number greater
// than since whose §14 labels contain every key=value pair in
// labelFilter, in ascending sequence order, capped at limit. The label
// predicate is pushed into the query (via the labels GIN index), so the
// §15.1 cursor/hasMore pagination stays correct: the limit applies to the
// matching rows. spec: §14 line 106; §15.1 lines 1228-1253. F-14.1.13.
func (s *Store) SinceFiltered(ctx context.Context, tenantID string, since uint64, limit int, labelFilter map[string]string) ([]billingstore.Event, error) {
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var out []billingstore.Event
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		q := `SELECT ` + selectList + ` FROM billing_events
			WHERE tenant_id = $1 AND sequence_number > $2`
		args := []any{tenantID, int64(since)}
		if len(labelFilter) > 0 {
			want, err := json.Marshal(labelFilter)
			if err != nil {
				return err
			}
			args = append(args, string(want))
			q += fmt.Sprintf(" AND labels @> $%d::jsonb", len(args))
		}
		q += ` ORDER BY sequence_number`
		if limit > 0 {
			args = append(args, limit)
			q += fmt.Sprintf(" LIMIT $%d", len(args))
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

// SessionTotals returns the reconciled per-session token + compute usage.
// It loads the session-scoped rows (originals and their §11.2.1
// corrections, which carry the same session_id) in sequence order and
// reconciles them in-process so a correction supersedes its original
// rather than double-counting. spec: §15.1; §11.2.1. F-15.2.3.
func (s *Store) SessionTotals(ctx context.Context, tenantID, sessionID string) (billingstore.SessionUsage, error) {
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return billingstore.SessionUsage{}, err
	}
	var events []billingstore.Event
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+selectList+` FROM billing_events
			WHERE tenant_id = $1 AND session_id = $2
			ORDER BY sequence_number`, tenantID, sessionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows, tenantID)
			if err != nil {
				return err
			}
			events = append(events, e)
		}
		return rows.Err()
	})
	if err != nil {
		return billingstore.SessionUsage{}, err
	}
	return billingstore.SumSessionUsage(events, sessionID), nil
}

// EnvironmentTotals implements billingstore.Store. The query pulls the
// environment's stamped events plus any §11.2.1 correction events that
// reference one of them: a correction event carries no environment_id of
// its own, so filtering by environment_id alone would silently drop
// corrections and over-report usage. Including the corrections lets
// SumEnvironmentUsage run ReconcileLedger and supersede the corrected
// originals before summing, matching the Memory store's semantics.
//
// spec: §15.1 line 840 (environment billing rollup); §10.6 line 663;
// §11.2.1 (correction semantics). F-15.1.3.
func (s *Store) EnvironmentTotals(ctx context.Context, tenantID, environmentID string) (billingstore.SessionUsage, error) {
	if environmentID == "" {
		return billingstore.SessionUsage{}, nil
	}
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return billingstore.SessionUsage{}, err
	}
	var events []billingstore.Event
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+selectList+` FROM billing_events
			WHERE tenant_id = $1 AND (
				environment_id = $2
				OR (corrects_sequence IS NOT NULL AND corrects_sequence IN (
					SELECT sequence_number FROM billing_events
					WHERE tenant_id = $1 AND environment_id = $2))
			)
			ORDER BY sequence_number`, tenantID, environmentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows, tenantID)
			if err != nil {
				return err
			}
			events = append(events, e)
		}
		return rows.Err()
	})
	if err != nil {
		return billingstore.SessionUsage{}, err
	}
	return billingstore.SumEnvironmentUsage(events, environmentID), nil
}

// scanEvent reads one row in selectList order into an Event.
func scanEvent(row pgx.Row, tenantID string) (billingstore.Event, error) {
	var (
		e           billingstore.Event
		seq         int64
		schemaVer   int32
		sessionID   *string
		eventType   string
		tokensIn    int64
		tokensOut   int64
		correctsTo  *int64
		reasonCode  string
		detail      string
		conditional []byte
		labels      []byte
	)
	if err := row.Scan(&seq, &schemaVer, &e.UserID, &sessionID,
		&e.ExperimentID, &e.VariantID, &eventType, &tokensIn, &tokensOut,
		&e.PodMinutes, &correctsTo, &reasonCode, &detail, &e.EnvironmentID,
		&conditional, &labels, &e.CreatedAt); err != nil {
		return billingstore.Event{}, err
	}
	if len(conditional) > 0 {
		var c billingstore.Conditional
		if err := json.Unmarshal(conditional, &c); err != nil {
			return billingstore.Event{}, err
		}
		e.Conditional = &c
	}
	if len(labels) > 0 {
		var m map[string]string
		if err := json.Unmarshal(labels, &m); err != nil {
			return billingstore.Event{}, err
		}
		e.Labels = m
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

// conditionalValue maps an event's §11.2.1 event-type-specific fields to
// the nullable conditional_fields JSONB column value: nil (SQL NULL) when
// the event carries no Conditional block, the JSON encoding otherwise.
// spec: §11.2.1 — Event schema (all events). F-11.2.12.
func conditionalValue(e billingstore.Event) (any, error) {
	if e.Conditional == nil {
		return nil, nil
	}
	b, err := json.Marshal(e.Conditional)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// labelsValue maps an event's §14 labels map to the nullable labels JSONB
// column value: nil (SQL NULL) when the event carries no labels so a
// NULL-labels row never matches a non-empty containment filter, the JSON
// encoding otherwise. spec: §14 line 106. F-14.1.13.
func labelsValue(m map[string]string) (any, error) {
	if len(m) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return b, nil
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
// flush time (§11.2.1: "the flusher acquires the Postgres sequence
// value at flush time, not at buffer time") — the provisional
// stream_seq stamped during the outage is discarded — so
// InsertFromStream draws the sequence_number by nextval on the
// dedicated per-tenant sequence exactly like Append.
//
// spec: §11.2.1, §10.2.
func (s *Store) InsertFromStream(ctx context.Context, e billingstore.Event, streamEntryID string) error {
	if err := billingstore.Validate(e); err != nil {
		return err
	}
	e = billingstore.Normalize(e, time.Now())
	pool, err := s.shard(ctx, e.TenantID)
	if err != nil {
		return err
	}
	return pgtenant.InTx(ctx, pool, e.TenantID, func(tx pgx.Tx) error {
		// §11.2.1 flush-time acquire: draw the authoritative sequence_number
		// from the dedicated per-tenant Postgres sequence (the §10.2
		// billing_seq_<40hex> name), discarding the provisional outage-window
		// stream_seq, exactly like Append.
		var seq int64
		if err := tx.QueryRow(ctx,
			`SELECT nextval($1)`, seqname.BillingSequenceName(e.TenantID)).Scan(&seq); err != nil {
			return err
		}
		e.SequenceNumber = uint64(seq)
		conditional, err := conditionalValue(e)
		if err != nil {
			return err
		}
		// §11.2.1: ON CONFLICT (tenant_id, stream_entry_id) DO NOTHING
		// makes a redelivered stream entry idempotent. The backing unique
		// index (migration 0043, idx_billing_events_stream_entry) is partial
		// (WHERE stream_entry_id IS NOT NULL), so the conflict target must
		// carry the same predicate for Postgres to infer that index; a bare
		// ON CONFLICT (tenant_id, stream_entry_id) raises 42P10 against a
		// partial index.
		_, err = tx.Exec(ctx, `INSERT INTO billing_events (
			tenant_id, sequence_number, schema_version, user_id, session_id,
			experiment_id, variant_id, event_type, tokens_input, tokens_output,
			pod_minutes, corrects_sequence, correction_reason_code, correction_detail,
			environment_id, conditional_fields, stream_entry_id, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (tenant_id, stream_entry_id) WHERE stream_entry_id IS NOT NULL DO NOTHING`,
			e.TenantID, int64(e.SequenceNumber), int32(e.SchemaVersion), e.UserID,
			pgtenant.NullString(e.SessionID), e.ExperimentID, e.VariantID,
			string(e.EventType), int64(e.TokensInput), int64(e.TokensOutput),
			e.PodMinutes, correctsSequence(e), string(e.CorrectionReasonCode),
			e.CorrectionDetail, e.EnvironmentID, conditional, streamEntryID, e.CreatedAt)
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
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var n int64
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
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
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var n int64
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
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
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var n int64
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
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

// DeleteOlderThan prunes the tenant's billing events older than cutoff —
// the §11.2.1 retention sweep. As with DeleteByTenant, billing_events is
// append-only: the lenny_billing_immutability trigger permits the DELETE
// only under lenny.erasure_mode, and the lenny_erasure role must hold
// GRANT DELETE ON billing_events (migration 0093). The erasure-role
// connection wiring is the F-12.2.16 follow-up.
//
// spec: §11.2.1 line 151; §11.7 immutability. F-11.2.15.
func (s *Store) DeleteOlderThan(ctx context.Context, tenantID string, cutoff time.Time) (int, error) {
	if tenantID == "" {
		return 0, errors.New("billingstore/pgstore: DeleteOlderThan requires a concrete tenant_id")
	}
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var n int64
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`DELETE FROM billing_events WHERE tenant_id = $1 AND created_at < $2`, tenantID, cutoff)
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
