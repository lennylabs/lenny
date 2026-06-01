// SPDX-License-Identifier: MIT

// Package auditstore is the Postgres-backed §11.7 audit hash chain
// over the audit_log table. It makes the per-tenant tamper-evident
// audit trail durable: an in-memory chain is lost on a gateway
// restart, which §11.7 forbids for the audit ledger.
//
// Each Append seals a row with the same hash construction as the
// in-memory pkg/audit chain (ComputeHash / LinkHash) and is
// serialized per tenant by a transaction advisory lock, so two
// concurrent writers cannot fork a tenant's chain (§11.7 item 3).
// Verify loads the persisted rows and walks them with the shared
// pkg/audit verification logic.
//
// The audit_log row hash uses the v1 pkg/audit construction. The
// §11.7 wire form (RFC 8785 canonical JSON, the id and
// event_schema_version columns in the hash input) is a Phase 13
// upgrade; payload_canonical_json currently mirrors the payload.
package auditstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// ErrNotFound — no audit row at the requested sequence number.
var ErrNotFound = errors.New("auditstore: audit row not found")

// Store is the Postgres-backed audit chain. Construct with New.
type Store struct {
	pool    *pgxpool.Pool
	lockCfg LockConfig
	metrics *LockMetrics
}

// Option configures a Store at construction time.
type Option func(*Store)

// WithLockConfig overrides the §11.7 lock acquisition SLO / retry
// tunables. An unset Store uses DefaultLockConfig.
func WithLockConfig(cfg LockConfig) Option { return func(s *Store) { s.lockCfg = cfg } }

// WithLockMetrics wires the §11.7 lock-acquisition Prometheus metrics.
// Without it the Store still enforces the timeout and retries but emits
// no samples.
func WithLockMetrics(m *LockMetrics) Option { return func(s *Store) { s.metrics = m } }

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied. Lock acquisition
// uses the §11.7 spec defaults unless WithLockConfig overrides them.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	s := &Store{pool: pool, lockCfg: DefaultLockConfig()}
	for _, o := range opts {
		o(s)
	}
	return s
}

const selectList = `sequence_number, event_type, payload, created_at, prev_hash`

// Append commits an audit event to the tenant's chain and returns the
// sealed row. The append runs under a per-tenant transaction advisory
// lock so the tail read, the prev_hash computation, and the insert
// are atomic with respect to other writers (§11.7 item 3).
//
// Lock acquisition is bounded by the configured statement_timeout. A
// timeout returns AUDIT_CONCURRENCY_TIMEOUT internally and the append is
// retried on the same replica up to MaxRetries with jittered
// exponential backoff; exhausting the budget returns an
// *AuditUnavailableError (HTTP 503 audit_unavailable). A non-lock
// failure is returned immediately without consuming a retry.
// spec: §11.7 item 3 line 368.
func (s *Store) Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	cfg := s.lockCfg.withDefaults()
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, cfg, attempt); err != nil {
				return audit.Row{}, err
			}
		}
		var committed audit.Row
		err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
			if err := acquireAuditLock(ctx, tx, tenantID, cfg, s.metrics); err != nil {
				return err
			}
			row, err := sealAndInsert(ctx, tx, tenantID, eventType, payload, at)
			if err != nil {
				return err
			}
			committed = row
			return nil
		})
		if err == nil {
			return committed, nil
		}
		var cte *ConcurrencyTimeoutError
		if !errors.As(err, &cte) {
			return audit.Row{}, err
		}
		lastErr = err
	}
	return audit.Row{}, &AuditUnavailableError{TenantID: tenantID, Attempts: cfg.MaxRetries + 1, Err: lastErr}
}

// Rows returns the tenant's audit rows in sequence order.
func (s *Store) Rows(ctx context.Context, tenantID string) ([]audit.Row, error) {
	var out []audit.Row
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM audit_log WHERE tenant_id = $1
			 ORDER BY sequence_number`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRow(rows, tenantID)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Verify walks the tenant's persisted chain and reports its §11.7
// integrity state, using the same verification as the in-memory
// pkg/audit chain.
func (s *Store) Verify(ctx context.Context, tenantID string) (audit.VerifyResult, error) {
	rows, err := s.Rows(ctx, tenantID)
	if err != nil {
		return audit.VerifyResult{}, err
	}
	return audit.ChainFromRows(tenantID, rows, nil).Verify(), nil
}

// Get returns the row at seq for the tenant, or ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantID string, seq uint64) (audit.Row, error) {
	var out audit.Row
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM audit_log
			 WHERE tenant_id = $1 AND sequence_number = $2`, tenantID, int64(seq))
		r, err := scanRow(row, tenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return audit.Row{}, err
	}
	return out, nil
}

// AppendInTx commits an audit event into the tenant's chain on tx and
// returns the sealed row. The caller already owns the transaction —
// pgtenant.InTx must have set app.current_tenant for tenantID. The
// helper acquires the §11.7 per-tenant audit advisory lock, reads the
// chain tail, seals the row with prev_hash + content hash, and INSERTs
// the audit_log row. Callers that need to bind an external write to the
// audit row in one transaction (the §13.3 write-before-issue Token
// Service path that pairs an issued_tokens INSERT with the
// token.exchanged audit INSERT) call AppendInTx from inside their own
// pgtenant.InTx so the audit row and the bound write share one COMMIT.
// spec: §11.7 (per-tenant advisory lock) and §13.3 line 589 (write-
// before-issue single Postgres transaction).
func AppendInTx(ctx context.Context, tx pgx.Tx, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if err := acquireAuditLock(ctx, tx, tenantID, DefaultLockConfig(), nil); err != nil {
		return audit.Row{}, err
	}
	return sealAndInsert(ctx, tx, tenantID, eventType, payload, at)
}

// sealAndInsert reads the chain tail, seals the new row with its
// prev_hash + content hash, and INSERTs the audit_log row. The caller
// must already hold the §11.7 per-tenant advisory lock (acquireAuditLock)
// so the tail read and the insert are serialized against other writers.
func sealAndInsert(ctx context.Context, tx pgx.Tx, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	tail, hasTail, err := scanTail(ctx, tx, tenantID)
	if err != nil {
		return audit.Row{}, err
	}
	row := audit.Row{
		Seq:       1,
		TenantID:  tenantID,
		EventType: eventType,
		Payload:   payload,
		Timestamp: at,
		PrevHash:  audit.GenesisPrevHash,
	}
	if hasTail {
		row.Seq = tail.Seq + 1
		row.PrevHash = audit.LinkHash(tail)
	}
	row.Hash = audit.ComputeHash(row)
	prevHashBytes, err := hex.DecodeString(row.PrevHash)
	if err != nil {
		return audit.Row{}, fmt.Errorf("auditstore: encode prev_hash: %w", err)
	}
	canonical := string(payload)
	if canonical == "" {
		canonical = "null"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log (
		tenant_id, sequence_number, prev_hash, event_type,
		payload, payload_canonical_json, created_at
	) VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7)`,
		tenantID, int64(row.Seq), prevHashBytes, eventType,
		canonical, canonical, at); err != nil {
		return audit.Row{}, err
	}
	return row, nil
}

// scanTail reads the highest-sequence row for the tenant. hasTail is
// false when the chain is empty.
func scanTail(ctx context.Context, tx pgx.Tx, tenantID string) (audit.Row, bool, error) {
	row := tx.QueryRow(ctx,
		`SELECT `+selectList+` FROM audit_log WHERE tenant_id = $1
		 ORDER BY sequence_number DESC LIMIT 1`, tenantID)
	r, err := scanRow(row, tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return audit.Row{}, false, nil
	}
	if err != nil {
		return audit.Row{}, false, err
	}
	return r, true, nil
}

// scanRow reads one row in selectList order and recomputes its
// content hash (audit_log does not store the per-row hash; it is
// derived, exactly as the in-memory chain derives it).
func scanRow(row pgx.Row, tenantID string) (audit.Row, error) {
	var (
		r        audit.Row
		seq      int64
		payload  []byte
		prevHash []byte
	)
	if err := row.Scan(&seq, &r.EventType, &payload, &r.Timestamp, &prevHash); err != nil {
		return audit.Row{}, err
	}
	r.Seq = uint64(seq)
	r.TenantID = tenantID
	r.Payload = json.RawMessage(payload)
	r.Timestamp = r.Timestamp.UTC()
	r.PrevHash = hex.EncodeToString(prevHash)
	r.Hash = audit.ComputeHash(r)
	return r, nil
}
