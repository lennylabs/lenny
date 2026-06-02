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
	"github.com/lennylabs/lenny/pkg/gateway/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// ErrNotFound — no audit row at the requested sequence number.
var ErrNotFound = errors.New("auditstore: audit row not found")

// ErrEmptyScope — an erasure primitive was called with an empty tenant
// id (or user id). Empty arguments must never be treated as "delete
// everything" (§12.8 line 753).
var ErrEmptyScope = errors.New("auditstore: erasure requires a non-empty tenant_id (and user_id)")

// EventStore is the §12.2 EventStore (audit) role interface. *Store
// satisfies it. Alongside the append/verify primitives it exposes the
// §12.1 mandatory erasure pair so a substitute backend that omits
// either method cannot compile into the gateway binary.
//
// spec: §12.1 line 5 — every store role interface MUST expose
// DeleteByUser and DeleteByTenant, enforced at compile time by Go
// interface satisfaction.
type EventStore interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
	Rows(ctx context.Context, tenantID string) ([]audit.Row, error)
	Verify(ctx context.Context, tenantID string) (audit.VerifyResult, error)
	Get(ctx context.Context, tenantID string, seq uint64) (audit.Row, error)
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// Router is the §12.3 R-03 routing surface this chain depends on: it
// resolves the Postgres pool for a tenant's audit-log writes.
// *storerouter.SingleShardRouter satisfies it. The store never holds a
// raw *pgxpool.Pool, so a future audit-shard split rotates only the
// router implementation and no audit-write call site changes.
//
// spec: §12.3 R-03 line 144 — all audit log inserts MUST be routed
// through the StoreRouter interface rather than accessing a Postgres
// pool directly. AllAuditShards is the §25.9 scatter-gather surface the
// OCSF translation and EventBus retranscribe workers iterate so a
// cross-tenant drain visits every audit shard.
type Router interface {
	AuditShard(ctx context.Context, tenantID storerouter.TenantID) (*pgxpool.Pool, error)
	AllAuditShards(ctx context.Context) ([]storerouter.ShardHandle, error)
}

// Store is the Postgres-backed audit chain. Construct with New.
type Store struct {
	router  Router
	lockCfg LockConfig
	metrics *LockMetrics
	// syncWritePool is the §12.3 line 79 dedicated audit sync write pool
	// (audit.syncWritePoolSize, default 4). When set, the synchronous
	// Append / AppendBatch write path uses it instead of the shared
	// request pool so audit writes do not contend with request-serving
	// connections. Reads stay on the router pool. Nil keeps the v1
	// behavior of writing through the router's audit shard. F-12.3.14.
	syncWritePool *pgxpool.Pool
	// batchBuffer is the §12.3 line 81 opt-in T2 batching buffer. When
	// set (audit.batchingEnabled: true), non-PII T2 operational audit
	// events ride the batched-insert path instead of a synchronous
	// write. Nil keeps every audit write synchronous. F-12.3.14.
	batchBuffer batchEnqueuer
}

// batchEnqueuer is the seam the §12.3 batch buffer satisfies. It is an
// interface (not a direct *auditbatch.Buffer) so the auditbatch flush
// callback can close over this Store's AppendBatch without an import
// cycle.
type batchEnqueuer interface {
	Enqueue(it auditbatch.Item)
}

var _ EventStore = (*Store)(nil)

// Option configures a Store at construction time.
type Option func(*Store)

// WithLockConfig overrides the §11.7 lock acquisition SLO / retry
// tunables. An unset Store uses DefaultLockConfig.
func WithLockConfig(cfg LockConfig) Option { return func(s *Store) { s.lockCfg = cfg } }

// WithLockMetrics wires the §11.7 lock-acquisition Prometheus metrics.
// Without it the Store still enforces the timeout and retries but emits
// no samples.
func WithLockMetrics(m *LockMetrics) Option { return func(s *Store) { s.metrics = m } }

// WithSyncWritePool wires the §12.3 line 79 dedicated audit sync write
// pool (audit.syncWritePoolSize). The synchronous Append / AppendBatch
// write path uses it so audit writes do not consume request-serving
// connections from the shared pool. F-12.3.14.
func WithSyncWritePool(pool *pgxpool.Pool) Option {
	return func(s *Store) { s.syncWritePool = pool }
}

// WithBatchBuffer wires the §12.3 line 81 opt-in T2 batching buffer.
// When set, non-PII T2 operational audit events (the cross_tenant_read
// worker receipts) are enqueued onto the buffer instead of written
// synchronously. F-12.3.14.
func WithBatchBuffer(b batchEnqueuer) Option {
	return func(s *Store) { s.batchBuffer = b }
}

// SetBatchBuffer wires the §12.3 T2 batch buffer after construction.
// The buffer's flush callback closes over this Store's AppendBatch, so
// it must be created after the Store; this setter completes the cycle.
// F-12.3.14.
func (s *Store) SetBatchBuffer(b batchEnqueuer) { s.batchBuffer = b }

// New returns a Store that routes every audit write through router
// (§12.3 R-03). The router must resolve to a database that has the
// migrations/ schema applied. Lock acquisition uses the §11.7 spec
// defaults unless WithLockConfig overrides them.
func New(router Router, opts ...Option) *Store {
	s := &Store{router: router, lockCfg: DefaultLockConfig()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// shard resolves the audit Postgres pool for tenantID through the
// §12.3 R-03 router. Reads (Rows, Get, Verify) and the §12.3 outbox
// scatter reads use it.
func (s *Store) shard(ctx context.Context, tenantID string) (*pgxpool.Pool, error) {
	return s.router.AuditShard(ctx, storerouter.TenantID(tenantID))
}

// writeShard resolves the pool for the synchronous audit write path.
// It prefers the §12.3 line 79 dedicated audit sync write pool when one
// is wired (audit.syncWritePoolSize) so audit writes do not contend
// with request-serving connections, falling back to the router's audit
// shard otherwise. F-12.3.14.
func (s *Store) writeShard(ctx context.Context, tenantID string) (*pgxpool.Pool, error) {
	if s.syncWritePool != nil {
		return s.syncWritePool, nil
	}
	return s.shard(ctx, tenantID)
}

// allShards returns every audit Postgres shard for the §25.9
// cross-tenant scatter-gather worker drains (OCSF translation and
// EventBus retranscribe). v1 returns one shard.
func (s *Store) allShards(ctx context.Context) ([]storerouter.ShardHandle, error) {
	return s.router.AllAuditShards(ctx)
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
	pool, err := s.writeShard(ctx, tenantID)
	if err != nil {
		return audit.Row{}, err
	}
	cfg := s.lockCfg.withDefaults()
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, cfg, attempt); err != nil {
				return audit.Row{}, err
			}
		}
		var committed audit.Row
		err := pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
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
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var out []audit.Row
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
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
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return audit.Row{}, err
	}
	var out audit.Row
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
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

// DeleteByUser satisfies the §12.1 mandatory-erasure primitive. The
// audit ledger is deliberately retained on user erasure: gdpr.* rows
// (erasure receipts) are exempt and kept for the full audit period,
// dead-lettered rows are PII-redacted in place rather than deleted,
// and the audit_log table is keyed only by (tenant_id,
// sequence_number) with no user_id column, so there is nothing this
// store can delete keyed by user without breaking the §11.7 hash
// chain. The substantive §12.8 step-13 selective deletion and step-14
// OCSF dead-letter redaction are tracked under F-12.2.5; this method
// satisfies the §12.1 compile-time contract and returns (0, nil) after
// rejecting empty arguments (§12.8 line 753).
//
// spec: §12.1 line 5 (mandatory primitive); §12.8 line 775 (audit
// retention carve-out for gdpr.* and dead-lettered rows).
func (s *Store) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, ErrEmptyScope
	}
	return 0, nil
}

// DeleteByTenant satisfies the §12.1 mandatory-erasure primitive. It
// removes the tenant's entire audit chain for the §12.8 Phase-4 tenant
// teardown and returns the count deleted. audit_log is append-only:
// the lenny_audit_immutability trigger rejects DELETE unless the
// transaction sets lenny.erasure_mode = 'true', and the table-level
// DELETE privilege is held only by the lenny_erasure role, so this
// method must run on a connection that runs as lenny_erasure (the same
// requirement billingstore.DeleteByTenant carries; wiring the
// erasure-role connection into the orchestrator is F-12.2.16). Empty
// tenant id is rejected.
//
// spec: §12.1 line 5 (mandatory primitive); §11.7 item 7 (erasure_mode
// escape on the append-only ledger); §12.8 Phase 4 (tenant deletion).
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, ErrEmptyScope
	}
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var deleted int64
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL lenny.erasure_mode = 'true'"); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
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
