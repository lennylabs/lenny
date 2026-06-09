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
// The row hash is computed over the §11.7 item 3 line 361 canonical
// tuple (id, prev_hash, tenant_id, sequence_number, event_type,
// event_schema_version, payload_canonical_json, created_at). The row id
// is generated at seal time so it can enter the hash, and
// payload_canonical_json holds the RFC 8785 JCS canonical form of the
// payload (audit.CanonicalPayload). On verify the chain is reconstructed
// from the persisted columns and re-canonicalized, so the hash is stable
// across the jsonb normalization the payload column undergoes.
package auditstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	AuditReadShard(ctx context.Context, tenantID storerouter.TenantID) (*pgxpool.Pool, error)
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
	// opsEmitter, when set, receives the §16.7 subset of audit events
	// that escalate onto the §25.5 operational event stream as
	// audit-bearing CloudEvents (datacontenttype application/ocsf+json).
	// Nil disables the escalation path entirely. opsPublisherID is the
	// gateway-replica id stamped onto the CloudEvents source. F-25.5.18.
	opsEmitter     opsStreamEmitter
	opsPublisherID string
	// platformResidency, when wired via WithPlatformAuditResidency,
	// activates the §11.7 CMP-058 platform-tenant audit residency gate:
	// a platform-tenant audit write carrying a non-platform
	// target_tenant_id is routed to that tenant's regional
	// platform-Postgres. Nil keeps every write on the §12.3 R-03 router
	// shard (the single-region default). F-11.7.9.
	platformResidency *platformResidencyRouter
	// scatterCfg bounds the §25.9 line 3710 cross-tenant scatter-gather
	// fan-out (max concurrency, per-shard timeout). The zero value
	// resolves to the storerouter defaults. F-25.9.11.
	scatterCfg storerouter.ScatterConfig
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
// §12.3 R-03 router. The §12.3 outbox scatter reads and the writeShard
// fallback use it; it always resolves to the audit write pool.
func (s *Store) shard(ctx context.Context, tenantID string) (*pgxpool.Pool, error) {
	return s.router.AuditShard(ctx, storerouter.TenantID(tenantID))
}

// readShard resolves the audit pool for read-heavy queries (Rows, Get,
// Verify). When a read replica is configured it returns the replica so
// the §12.3 line 146 audit-read class lands off the primary; otherwise
// it resolves to the same pool as shard. spec: §12.3 line 146.
func (s *Store) readShard(ctx context.Context, tenantID string) (*pgxpool.Pool, error) {
	return s.router.AuditReadShard(ctx, storerouter.TenantID(tenantID))
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

const selectList = `sequence_number, event_type, payload, created_at, prev_hash, id::text, event_schema_version`

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
	// spec: §11.7 lines 430-435 (CMP-058) — a platform-tenant audit write
	// that references a non-platform target tenant via target_tenant_id is
	// residency-routed to the target tenant's regional platform-Postgres,
	// falling back to global when no region is set and failing closed with
	// PLATFORM_AUDIT_REGION_UNRESOLVABLE when the region is unresolvable.
	// Keyed on the payload's target_tenant_id so every current and future
	// platform-tenant event that carries it is routed without per-emit-site
	// changes. The gate is inert unless WithPlatformAuditResidency wired it.
	if s.platformResidency != nil && tenantID == s.platformResidency.platformTenantID {
		if target := targetTenantID(payload); target != "" && target != tenantID {
			return s.appendPlatformTargeted(ctx, eventType, payload, target, at)
		}
	}
	pool, err := s.writeShard(ctx, tenantID)
	if err != nil {
		return audit.Row{}, err
	}
	return s.appendOnPool(ctx, pool, tenantID, eventType, payload, at)
}

// appendOnPool runs the §11.7 item 3 per-tenant lock + seal + insert
// retry loop against an explicit pool. The Append fast path and the
// CMP-058 residency-routed path both call it; only the resolved pool
// differs. spec: §11.7 item 3 line 368.
func (s *Store) appendOnPool(ctx context.Context, pool *pgxpool.Pool, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
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
			s.escalateToOpsStream(ctx, committed)
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

// Rows returns the tenant's audit rows in sequence order. A row rewritten
// in place by a §12.8 step-14 dead-letter redaction is flagged Redacted
// and carries its preserved pre-redaction hash (from the RedactionReceipt)
// so the chain stays linked and the verifier classifies the break as a
// lawful redacted_gdpr discontinuity.
func (s *Store) Rows(ctx context.Context, tenantID string) ([]audit.Row, error) {
	pool, err := s.readShard(ctx, tenantID)
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
	receipts, err := s.Receipts(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	applyReceipts(out, receipts)
	return out, nil
}

// Verify walks the tenant's persisted chain and reports its §11.7
// integrity state, using the same verification as the in-memory
// pkg/audit chain. The §12.8 RedactionReceipts are loaded so a lawfully
// redacted row is classified redacted_gdpr rather than broken.
func (s *Store) Verify(ctx context.Context, tenantID string) (audit.VerifyResult, error) {
	rows, err := s.Rows(ctx, tenantID)
	if err != nil {
		return audit.VerifyResult{}, err
	}
	receipts, err := s.Receipts(ctx, tenantID)
	if err != nil {
		return audit.VerifyResult{}, err
	}
	return audit.ChainFromRows(tenantID, rows, receipts).Verify(), nil
}

// Get returns the row at seq for the tenant, or ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantID string, seq uint64) (audit.Row, error) {
	pool, err := s.readShard(ctx, tenantID)
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
	// Postgres TIMESTAMPTZ has microsecond resolution; truncate before
	// hashing so the created_at fed into the hash equals the value read
	// back, otherwise the §11.7 verify walk would report a false break on
	// a sub-microsecond timestamp.
	at = at.UTC().Truncate(time.Microsecond)
	tail, hasTail, err := scanTail(ctx, tx, tenantID)
	if err != nil {
		return audit.Row{}, err
	}
	row := audit.Row{
		ID:                 uuid.NewString(),
		Seq:                1,
		TenantID:           tenantID,
		EventType:          eventType,
		EventSchemaVersion: audit.DefaultEventSchemaVersion,
		Payload:            payload,
		Timestamp:          at,
		PrevHash:           audit.GenesisPrevHash,
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
	storedPayload := string(payload)
	if storedPayload == "" {
		storedPayload = "null"
	}
	// payload_canonical_json holds the RFC 8785 JCS canonical form the
	// §11.7 hash input is computed over (audit.CanonicalPayload), so an
	// external verifier reads the exact bytes that were hashed.
	canonical := string(audit.CanonicalPayload(payload))
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log (
		id, tenant_id, sequence_number, prev_hash, event_type,
		event_schema_version, payload, payload_canonical_json, created_at
	) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9)`,
		row.ID, tenantID, int64(row.Seq), prevHashBytes, eventType,
		row.EventSchemaVersion, storedPayload, canonical, at); err != nil {
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

// PruneOptions parameterizes a §16.4 audit-retention sweep.
type PruneOptions struct {
	// GeneralCutoff is the §16.4 line 378 retention boundary: rows
	// whose event_type does not match the gdpr.* erasure-receipt
	// prefix and whose created_at predates this instant are eligible
	// for deletion. Derived from audit.retentionDays.
	GeneralCutoff time.Time

	// GDPRCutoff is the separate §16.4 line 380 / §12.8 line 839
	// retention boundary for gdpr.* erasure-receipt rows, which are
	// exempt from the general window and held under the longer
	// audit.gdprRetentionDays floor (>= 2190 days for regulated
	// profiles). A zero value holds every gdpr.* row indefinitely.
	GDPRCutoff time.Time

	// SIEMConfigured reports whether audit.siem.endpoint is set. When
	// true and Force is false, the §16.4 line 378 SIEM delivery guard
	// holds any row whose sequence_number exceeds the SIEM forwarder's
	// last acknowledged high-water mark (siem_delivery_state), so a
	// stalled forwarder cannot lose undelivered events to retention.
	SIEMConfigured bool

	// Force bypasses the SIEM delivery guard. The §16.4
	// line 378 operator override path
	// (POST /v1/admin/audit-partitions/{partition}/drop) sets it after
	// an explicit data-loss acknowledgement.
	Force bool
}

// PruneRetention deletes the tenant's audit rows that have aged past
// the §16.4 retention window and returns the count deleted. gdpr.*
// erasure-receipt rows are held under the separate opts.GDPRCutoff
// window (§16.4 line 380); every other row is eligible once it predates
// opts.GeneralCutoff. When opts.SIEMConfigured is true and opts.Force is
// false, the SIEM delivery guard withholds any row whose
// sequence_number is above the forwarder's last acknowledged high-water
// mark in siem_delivery_state, so a stalled SIEM forwarder never loses
// an undelivered event to retention (§16.4 line 378).
//
// The DELETE runs under SET LOCAL lenny.erasure_mode = 'true' so the
// lenny_audit_immutability trigger admits it, exactly as DeleteByTenant
// does; the connection must run as the lenny_erasure role. An empty
// tenant id is rejected so a misconfigured sweep cannot drop another
// tenant's chain.
//
// spec: §16.4 lines 378-382 (audit-retention partition GC, gdpr.*
// carve-out, SIEM delivery guard); §11.7 line 456 (regulated retention
// floor); §12.8 line 839 (gdprRetentionDays floor).
func (s *Store) PruneRetention(ctx context.Context, tenantID string, opts PruneOptions) (int, error) {
	if tenantID == "" {
		return 0, ErrEmptyScope
	}
	if opts.GeneralCutoff.IsZero() {
		return 0, fmt.Errorf("auditstore: PruneRetention requires a non-zero GeneralCutoff")
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
		// gdpr.* rows are exempt from the general window and only drop
		// once they clear the longer GDPRCutoff. A zero GDPRCutoff means
		// "hold all gdpr.* rows", so the gdpr branch matches nothing.
		// The SIEM guard, when active, caps the highest deletable
		// sequence at the forwarder's acknowledged high-water mark
		// (0 when the forwarder has acked nothing, holding everything).
		tag, err := tx.Exec(ctx, `
			DELETE FROM audit_log
			WHERE tenant_id = $1
			  AND (
			        (event_type NOT LIKE 'gdpr.%' AND created_at < $2)
			     OR (event_type LIKE 'gdpr.%' AND $3::timestamptz IS NOT NULL AND created_at < $3)
			      )
			  AND (
			        $4
			     OR sequence_number <= COALESCE(
			          (SELECT last_acked_sequence FROM siem_delivery_state WHERE tenant_id = $1), 0)
			      )`,
			tenantID,
			opts.GeneralCutoff.UTC(),
			nullableTime(opts.GDPRCutoff),
			opts.Force || !opts.SIEMConfigured,
		)
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

// nullableTime renders a zero time as a SQL NULL so the gdpr.* branch
// of PruneRetention matches no rows when no GDPR cutoff is supplied.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// RetentionWindow summarizes the non-gdpr.* audit rows a §16.4
// force-drop would delete for a tenant up to cutoff. It populates the
// §16.7 audit.partition_drop_forced event payload (oldest/newest event
// timestamps, the lost-row count, and the SIEM high-water mark at drop
// time).
type RetentionWindow struct {
	OldestEvent     time.Time
	NewestEvent     time.Time
	Count           int
	SIEMHighWater   uint64
	SIEMStateExists bool
}

// RetentionWindowStats summarizes the tenant rows older than cutoff
// (excluding gdpr.* erasure receipts, which are held under the separate
// gdprRetentionDays floor) and reads the tenant's SIEM delivery
// high-water mark, so the §16.4 force-drop override can record the
// §16.7 audit.partition_drop_forced payload before it deletes the rows.
//
// spec: §16.4 line 378 (force-drop override); §16.7 line 687
// (audit.partition_drop_forced payload).
func (s *Store) RetentionWindowStats(ctx context.Context, tenantID string, cutoff time.Time) (RetentionWindow, error) {
	if tenantID == "" {
		return RetentionWindow{}, ErrEmptyScope
	}
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return RetentionWindow{}, err
	}
	var (
		win    RetentionWindow
		oldest *time.Time
		newest *time.Time
		count  int64
	)
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT MIN(created_at), MAX(created_at), COUNT(*)
			FROM audit_log
			WHERE tenant_id = $1 AND event_type NOT LIKE 'gdpr.%' AND created_at < $2`,
			tenantID, cutoff.UTC()).Scan(&oldest, &newest, &count); err != nil {
			return err
		}
		var hw int64
		err := tx.QueryRow(ctx,
			`SELECT last_acked_sequence FROM siem_delivery_state WHERE tenant_id = $1`,
			tenantID).Scan(&hw)
		switch {
		case err == nil:
			win.SIEMHighWater = uint64(hw)
			win.SIEMStateExists = true
		case errors.Is(err, pgx.ErrNoRows):
			// No SIEM checkpoint row: the forwarder has acked nothing.
		default:
			return err
		}
		return nil
	})
	if err != nil {
		return RetentionWindow{}, err
	}
	if oldest != nil {
		win.OldestEvent = oldest.UTC()
	}
	if newest != nil {
		win.NewestEvent = newest.UTC()
	}
	win.Count = int(count)
	return win, nil
}

// SIEMHeldCount reports how many of a tenant's non-gdpr.* audit rows
// have aged past cutoff yet are withheld from the §16.4 retention drop
// by the SIEM delivery guard, because their sequence_number is above the
// forwarder's last acknowledged high-water mark in siem_delivery_state.
// A non-zero result is the "partition drop blocked by SIEM lag"
// condition the §16.4 line 378 / §16.5 AuditPartitionDropBlocked alert
// describes: events past their retention TTL that the GC must hold
// because the forwarder has not consumed them. gdpr.* rows are excluded
// (they are held under the separate gdprRetentionDays floor, not the
// SIEM guard), and a tenant with no siem_delivery_state row has an
// implicit high-water mark of 0, so every past-TTL row is held.
//
// spec: §16.4 line 378 (SIEM delivery guard holds undelivered partitions
// past their retention TTL); §16.5 (AuditPartitionDropBlocked).
func (s *Store) SIEMHeldCount(ctx context.Context, tenantID string, cutoff time.Time) (int, error) {
	if tenantID == "" {
		return 0, ErrEmptyScope
	}
	if cutoff.IsZero() {
		return 0, fmt.Errorf("auditstore: SIEMHeldCount requires a non-zero cutoff")
	}
	pool, err := s.shard(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var held int64
	err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM audit_log
			WHERE tenant_id = $1
			  AND event_type NOT LIKE 'gdpr.%'
			  AND created_at < $2
			  AND sequence_number > COALESCE(
			        (SELECT last_acked_sequence FROM siem_delivery_state WHERE tenant_id = $1), 0)`,
			tenantID, cutoff.UTC()).Scan(&held)
	})
	if err != nil {
		return 0, err
	}
	return int(held), nil
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
	if err := row.Scan(&seq, &r.EventType, &payload, &r.Timestamp, &prevHash, &r.ID, &r.EventSchemaVersion); err != nil {
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
