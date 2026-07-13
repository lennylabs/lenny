// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §25.4 remediation-lock store
// (Tier 1, the default). It persists locks to the ops_remediation_locks
// table (migration 0121) so exclusivity holds across every lenny-ops
// replica: the UNIQUE (scope) constraint is the compare-and-set primitive
// that closes the check-then-write window. The outage-epoch counter lives
// in ops_lock_epoch and the resolved split-brain log in ops_lock_conflicts.
// A Postgres outage surfaces as coordination.ErrStoreUnavailable so the
// tiered Service falls back to the Redis (Tier 2) store.
//
// All three tables are platform-scoped (the §25 control plane is not
// multi-tenanted at this boundary; §25.4 line 1492), so the store runs no
// tenant-scoped transaction and the tables carry no RLS policy.
//
// spec: §25.4 lines 2160-2267.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
)

// advisoryReconcileKey is the constant §25.4 line 2226
// pg_advisory_xact_lock key (the spec's 0xLOCK_EPOCH_RECONCILE) the
// reconciliation transaction holds so a new Tier 1 acquire blocks until
// reconciliation commits rather than reading a stale epoch mid-pass. It is
// passed to the bigint form of pg_advisory_xact_lock.
const advisoryReconcileKey int64 = 0x10C4_E90C

// Store is the Postgres-backed §25.4 Tier 1 remediation-lock store.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied (0121).
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// columns is the ops_remediation_locks projection shared by every read.
const columns = `id, scope, operation, acquired_by, operation_id, acquired_at, expires_at, epoch, revision`

// Tier reports the postgres lockStore label.
func (s *Store) Tier() string { return coordination.StorePostgres }

// ServerTime reads the Postgres Tier 1 clock (now() at time zone 'UTC'),
// the same clock the §25.4 lease expiresAt computation is authored from,
// so the Postgres-Redis clock-skew sampler compares the two dependency
// clocks the lease path actually uses. A connection failure surfaces as
// coordination.ErrStoreUnavailable so the sampler skips the sample
// rather than reporting a spurious skew during a Postgres outage.
//
// spec: §25.4 line 2280 (Postgres-Redis skew monitoring); the Tier 1
// clock is Postgres now() at time zone 'UTC'.
func (s *Store) ServerTime(ctx context.Context) (time.Time, error) {
	var t time.Time
	if err := s.pool.QueryRow(ctx, `SELECT now() AT TIME ZONE 'UTC'`).Scan(&t); err != nil {
		return time.Time{}, classifyErr(err)
	}
	return t.UTC(), nil
}

// classifyErr maps a pgx connection/transport failure to
// coordination.ErrStoreUnavailable (the §25.4 Postgres-outage case), so the
// tiered Service falls the remediation lock from the Postgres tier to the
// Redis tier instead of returning a hard error. A genuine server-side query
// error (a PgError the database answered with, such as a constraint
// violation) is left intact for the caller to surface.
//
// A connection refused before pgx wraps a PgError is the common outage
// signal and is handled by the default branch. A Postgres administrator
// shutdown or failover window is different: the server answers an in-flight
// request with a FATAL PgError before the connection drops. Those codes are
// still "Postgres is unreachable" for the purpose of the §25.4 tier
// fall-through, so classifyErr treats the operator_intervention codes
// (57P01 admin_shutdown, 57P02 crash_shutdown, 57P03 cannot_connect_now)
// and the connection-exception class (08) as store-unavailable rather than
// surfacing them.
//
// spec: §25.4 lines 2170-2178 (Storage Tiers; attempts storage tiers in
// order, falling back on failure; Tier 2 Redis when Postgres unreachable).
func classifyErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"):
			// Class 08 — connection exception (08000, 08003, 08006, etc.).
			return coordination.ErrStoreUnavailable
		case pgErr.Code == "57P01", pgErr.Code == "57P02", pgErr.Code == "57P03":
			// admin_shutdown, crash_shutdown, cannot_connect_now — the backend
			// is terminating or not yet accepting connections, both of which a
			// shutdown or failover produces.
			return coordination.ErrStoreUnavailable
		default:
			// The database answered with a query-level error; surface it.
			return err
		}
	}
	return coordination.ErrStoreUnavailable
}

// Acquire inserts a §25.4 lock on req.Scope using the table's UNIQUE
// (scope) constraint as the compare-and-set primitive. Expired rows for
// the scope are reaped in the same statement (the §25.4 lazy cleanup) so a
// stale row does not block a fresh acquire. epoch is ignored here: Tier 1
// acquisitions use epoch 0 (§25.4 line 2193, "reserved for the Postgres
// tier"). On a live conflict it returns coordination.ErrCodeConflict
// carrying the holder; on a connection failure, ErrStoreUnavailable.
func (s *Store) Acquire(ctx context.Context, req coordination.LockRequest, _ uint64) (*coordination.Lock, error) {
	ttl := coordination.NormalizeTTL(req.TTLSeconds)
	// The upsert is the atomic CAS: a free scope inserts; a scope held by an
	// expired row is taken over (the §25.4 lazy cleanup) because the
	// ON CONFLICT branch fires only WHERE the existing row is expired; a
	// scope held by a live row matches neither path and returns no row,
	// which is the conflict. A WITH-modifying-CTE delete is not used here
	// because its effect is invisible to the same statement's conflict check.
	lock, err := scanLock(s.pool.QueryRow(ctx, `
		INSERT INTO ops_remediation_locks
			(id, scope, operation, acquired_by, operation_id, acquired_at, expires_at, epoch, revision)
		VALUES ($1, $2, $3, $4, $5, now(), now() + make_interval(secs => $6), 0, 0)
		ON CONFLICT (scope) DO UPDATE SET
			id = EXCLUDED.id, operation = EXCLUDED.operation, acquired_by = EXCLUDED.acquired_by,
			operation_id = EXCLUDED.operation_id, acquired_at = EXCLUDED.acquired_at,
			expires_at = EXCLUDED.expires_at, epoch = EXCLUDED.epoch, revision = 0
		WHERE ops_remediation_locks.expires_at <= now()
		RETURNING `+columns,
		coordination.NewLockID(), req.Scope, req.Operation, req.AcquiredBy,
		nullStr(req.OperationID), float64(ttl)))
	if errors.Is(err, pgx.ErrNoRows) {
		// The scope is held by a non-expired row: read the holder to build
		// the §25.4 conflict error.
		return nil, s.conflictFor(ctx, req.Scope)
	}
	if err != nil {
		return nil, classifyErr(err)
	}
	return lock, nil
}

// conflictFor reads the current holder of scope to populate the §25.4
// REMEDIATION_LOCK_CONFLICT message.
func (s *Store) conflictFor(ctx context.Context, scope string) error {
	held, err := scanLock(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM ops_remediation_locks WHERE scope = $1`, scope))
	if errors.Is(err, pgx.ErrNoRows) {
		// Raced with a release/reap: report the conflict generically rather
		// than inventing a holder.
		return &coordination.Error{Code: coordination.ErrCodeConflict, Message: "scope " + scope + " is held"}
	}
	if err != nil {
		return classifyErr(err)
	}
	return &coordination.Error{
		Code: coordination.ErrCodeConflict,
		Message: "scope " + scope + " is held by " + held.AcquiredBy +
			" until " + held.ExpiresAt.Format(time.RFC3339),
	}
}

// List returns every active (non-expired) §25.4 lock ordered by scope.
func (s *Store) List(ctx context.Context) ([]coordination.Lock, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+columns+` FROM ops_remediation_locks WHERE expires_at > now() ORDER BY scope`)
	if err != nil {
		return nil, classifyErr(err)
	}
	return collectLocks(rows)
}

// ActiveLocks returns the same non-expired set as List; it is the
// reconciliation snapshot source.
func (s *Store) ActiveLocks(ctx context.Context) ([]coordination.Lock, error) {
	return s.List(ctx)
}

// Get returns a single §25.4 lock, or REMEDIATION_LOCK_NOT_FOUND when the
// id is unknown or the lock has expired. When the id is that of a lock that
// lost a split-brain resolution to a still-active pre-outage holder, it
// returns the 409 split-brain conflict instead so the losing holder learns
// the outcome (§25.4 line 2267).
func (s *Store) Get(ctx context.Context, lockID string) (*coordination.Lock, error) {
	lock, err := scanLock(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM ops_remediation_locks WHERE id = $1 AND expires_at > now()`, lockID))
	if errors.Is(err, pgx.ErrNoRows) {
		if sb := s.splitBrainLoserConflict(ctx, lockID); sb != nil {
			return nil, sb
		}
		return nil, &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
	}
	if err != nil {
		return nil, classifyErr(err)
	}
	return lock, nil
}

// Extend renews a §25.4 lock's TTL to now() + ttlSeconds. caller MUST be
// the current acquiredBy; extension increments revision.
func (s *Store) Extend(ctx context.Context, lockID string, ttlSeconds int, caller string) (*coordination.Lock, error) {
	ttl := coordination.NormalizeTTL(ttlSeconds)
	lock, err := scanLock(s.pool.QueryRow(ctx, `
		UPDATE ops_remediation_locks
		SET expires_at = now() + make_interval(secs => $2), revision = revision + 1
		WHERE id = $1 AND expires_at > now() AND acquired_by = $3
		RETURNING `+columns, lockID, float64(ttl), caller))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, s.notFoundOrNotOwned(ctx, lockID, caller)
	}
	if err != nil {
		return nil, classifyErr(err)
	}
	return lock, nil
}

// Release frees a §25.4 lock. caller MUST be the current acquiredBy.
func (s *Store) Release(ctx context.Context, lockID, caller string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM ops_remediation_locks WHERE id = $1 AND expires_at > now() AND acquired_by = $2`,
		lockID, caller)
	if err != nil {
		return classifyErr(err)
	}
	if tag.RowsAffected() == 0 {
		return s.notFoundOrNotOwned(ctx, lockID, caller)
	}
	return nil
}

// notFoundOrNotOwned distinguishes a missing/expired lock (NOT_FOUND) from
// a live lock the caller does not own (NOT_OWNED), so the §25.4
// identity-based rule surfaces the correct status.
func (s *Store) notFoundOrNotOwned(ctx context.Context, lockID, caller string) error {
	var owner string
	err := s.pool.QueryRow(ctx,
		`SELECT acquired_by FROM ops_remediation_locks WHERE id = $1 AND expires_at > now()`, lockID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		// The lock is not a live Postgres row. If its id lost a split-brain
		// resolution to a still-active pre-outage holder, surface the 409
		// split-brain conflict so the losing holder's heartbeat/release
		// learns the outcome rather than a bare not-found (§25.4 line 2267).
		if sb := s.splitBrainLoserConflict(ctx, lockID); sb != nil {
			return sb
		}
		return &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
	}
	if err != nil {
		return classifyErr(err)
	}
	if owner != caller {
		return &coordination.Error{Code: coordination.ErrCodeNotOwned, Message: "lock " + lockID + " is not owned by the caller"}
	}
	// Owner matches but the earlier mutation affected no rows: report
	// not-found rather than claim a phantom failure.
	return &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
}

// splitBrainLoserConflict returns the §25.4 split-brain 409 for a lock id
// that lost a both-active resolution to a pre-outage holder, or nil when
// the id is not a recorded losing lock. The reconciliation logs each such
// resolution to ops_lock_conflicts with the losing (post-outage) lock in
// post_outage_lock; a losing holder that heartbeats, releases, or fetches
// its removed lock is told winner:"pre_outage" and the retained holder
// rather than a bare not-found (§25.4 line 2267, line 2271: "always
// notified via the heartbeat path").
func (s *Store) splitBrainLoserConflict(ctx context.Context, lockID string) *coordination.Error {
	var winnerHolder string
	err := s.pool.QueryRow(ctx, `
		SELECT pre_outage_lock->>'acquiredBy'
		FROM ops_lock_conflicts
		WHERE winner = 'pre_outage'
		  AND loser_was_active
		  AND post_outage_lock->>'id' = $1
		ORDER BY detected_at DESC
		LIMIT 1`, lockID).Scan(&winnerHolder)
	if err != nil {
		// No recorded conflict for this id (ErrNoRows), or the store is
		// unreachable: fall back to the caller's not-found path rather than
		// inventing a split-brain.
		return nil
	}
	return &coordination.Error{
		Code:         coordination.ErrCodeConflict,
		Message:      "lock " + lockID + " lost a split-brain resolution to the pre-outage holder " + winnerHolder,
		SplitBrain:   true,
		Winner:       "pre_outage",
		WinnerHolder: winnerHolder,
	}
}

// Steal takes over a §25.4 lock: caller becomes acquiredBy, the prior
// holder is recorded in stolen_from via the returned record, and revision
// increments. Scope-based authorization is the HTTP layer's job; the store
// performs the takeover unconditionally on a live lock.
func (s *Store) Steal(ctx context.Context, lockID string, req coordination.StealRequest) (*coordination.Lock, error) {
	ttl := coordination.NormalizeTTL(req.TTLSeconds)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, classifyErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Capture the prior holder under FOR UPDATE so a concurrent steal does
	// not race the takeover and so stolenFrom records the real prior owner.
	var prior string
	err = tx.QueryRow(ctx,
		`SELECT acquired_by FROM ops_remediation_locks WHERE id = $1 AND expires_at > now() FOR UPDATE`,
		lockID).Scan(&prior)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
	}
	if err != nil {
		return nil, classifyErr(err)
	}
	lock, err := scanLockTx(tx.QueryRow(ctx, `
		UPDATE ops_remediation_locks
		SET acquired_by = $2, operation_id = NULL, acquired_at = now(),
			expires_at = now() + make_interval(secs => $3), revision = revision + 1
		WHERE id = $1
		RETURNING `+columns, lockID, req.AcquiredBy, float64(ttl)))
	if err != nil {
		return nil, classifyErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, classifyErr(err)
	}
	lock.StolenFrom = prior
	return lock, nil
}

// Reap drops every expired §25.4 lock and returns the count removed.
func (s *Store) Reap(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM ops_remediation_locks WHERE expires_at <= now()`)
	if err != nil {
		return 0, classifyErr(err)
	}
	return int(tag.RowsAffected()), nil
}

// Epoch returns the stored §25.4 outage epoch from ops_lock_epoch,
// defaulting to 0 when the singleton row is absent.
func (s *Store) Epoch(ctx context.Context) (uint64, error) {
	var cur int64
	err := s.pool.QueryRow(ctx, `SELECT current FROM ops_lock_epoch WHERE id = 'singleton'`).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, classifyErr(err)
	}
	return uint64(cur), nil
}

// IncrementEpoch advances the §25.4 Postgres-side outage epoch by one and
// returns the new value, upserting the singleton row.
func (s *Store) IncrementEpoch(ctx context.Context) (uint64, error) {
	var cur int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ops_lock_epoch (id, current, updated_at) VALUES ('singleton', 1, now())
		ON CONFLICT (id) DO UPDATE SET current = ops_lock_epoch.current + 1, updated_at = now()
		RETURNING current`).Scan(&cur)
	if err != nil {
		return 0, classifyErr(err)
	}
	return uint64(cur), nil
}

// SetEpoch writes v as the §25.4 outage epoch when v exceeds the stored
// value, upserting the singleton row. The epoch never moves backward.
func (s *Store) SetEpoch(ctx context.Context, v uint64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ops_lock_epoch (id, current, updated_at) VALUES ('singleton', $1, now())
		ON CONFLICT (id) DO UPDATE SET current = GREATEST(ops_lock_epoch.current, $1), updated_at = now()`,
		int64(v))
	return classifyErr(err)
}

// Reconcile runs the §25.4 single-transaction reconciliation pass. It
// holds pg_advisory_xact_lock(advisoryReconcileKey) for the duration so a
// concurrent Tier 1 acquire blocks until the new epoch is committed. It
// writes MAX(redisEpoch, postgresEpoch) back to ops_lock_epoch, then for
// each active Redis lock either inserts it (no pre-outage Postgres lock on
// the scope) or applies the deterministic split-brain resolution rule and
// records the conflict in ops_lock_conflicts. now is the resolution-time
// clock for the expired-by-clock test.
//
// spec: §25.4 lines 2226-2267.
func (s *Store) Reconcile(ctx context.Context, redisEpoch uint64, redisLocks []coordination.Lock, now time.Time) (coordination.ReconcileOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return coordination.ReconcileOutcome{}, classifyErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryReconcileKey); err != nil {
		return coordination.ReconcileOutcome{}, classifyErr(err)
	}

	var pgEpoch int64
	if err := tx.QueryRow(ctx,
		`SELECT current FROM ops_lock_epoch WHERE id = 'singleton'`).Scan(&pgEpoch); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return coordination.ReconcileOutcome{}, classifyErr(err)
	}
	maxEpoch := uint64(pgEpoch)
	if redisEpoch > maxEpoch {
		maxEpoch = redisEpoch
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_lock_epoch (id, current, updated_at) VALUES ('singleton', $1, now())
		ON CONFLICT (id) DO UPDATE SET current = $1, updated_at = now()`, int64(maxEpoch)); err != nil {
		return coordination.ReconcileOutcome{}, classifyErr(err)
	}

	out := coordination.ReconcileOutcome{Epoch: maxEpoch}
	for _, rl := range redisLocks {
		pre, err := scanLockTx(tx.QueryRow(ctx,
			`SELECT `+columns+` FROM ops_remediation_locks WHERE scope = $1 FOR UPDATE`, rl.Scope))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No pre-outage Postgres lock: copy the Redis lock in with its
			// post-outage epoch.
			if err := insertLockTx(ctx, tx, rl, maxEpoch); err != nil {
				return coordination.ReconcileOutcome{}, classifyErr(err)
			}
			out.Copied++
		case err != nil:
			return coordination.ReconcileOutcome{}, classifyErr(err)
		default:
			conflict, err := s.resolveSplitBrain(ctx, tx, *pre, rl, maxEpoch, now)
			if err != nil {
				return coordination.ReconcileOutcome{}, classifyErr(err)
			}
			out.Conflicts = append(out.Conflicts, conflict)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return coordination.ReconcileOutcome{}, classifyErr(err)
	}
	return out, nil
}

// resolveSplitBrain applies the §25.4 deterministic resolution rule to a
// scope held by both a pre-outage Postgres lock and a post-outage Redis
// lock, mutating ops_remediation_locks and recording the conflict in
// ops_lock_conflicts. It runs inside the reconciliation transaction.
//
// spec: §25.4 lines 2255-2267.
func (s *Store) resolveSplitBrain(ctx context.Context, tx pgx.Tx, pre, post coordination.Lock, epoch uint64, now time.Time) (coordination.SplitBrainConflict, error) {
	preExpired := !now.Before(pre.ExpiresAt)
	postExpired := !now.Before(post.ExpiresAt)

	conflict := coordination.SplitBrainConflict{Scope: pre.Scope}
	switch {
	case preExpired:
		// Redis wins: replace the expired Postgres row with the Redis lock.
		if _, err := tx.Exec(ctx, `DELETE FROM ops_remediation_locks WHERE scope = $1`, pre.Scope); err != nil {
			return conflict, err
		}
		if err := insertLockTx(ctx, tx, post, epoch); err != nil {
			return conflict, err
		}
		conflict.Winner = "post_outage"
		conflict.WinnerHolder = post.AcquiredBy
		conflict.LoserHolder = pre.AcquiredBy
		conflict.LoserLockID = pre.ID
		conflict.LoserWasActive = false
	case postExpired:
		// Postgres wins: the expired Redis lock is discarded, the Postgres
		// lock retained with its pre-outage epoch.
		conflict.Winner = "pre_outage"
		conflict.WinnerHolder = pre.AcquiredBy
		conflict.LoserHolder = post.AcquiredBy
		conflict.LoserLockID = post.ID
		conflict.LoserWasActive = false
	default:
		// Both active: pre-outage (Postgres) wins; the losing Redis lock is
		// removed and its holder is notified with the split-brain 409 on its
		// next heartbeat/list/release (§25.4 line 2267). The conflict row
		// (below) keyed on the losing lock id drives that notification.
		conflict.Winner = "pre_outage"
		conflict.WinnerHolder = pre.AcquiredBy
		conflict.LoserHolder = post.AcquiredBy
		conflict.LoserLockID = post.ID
		conflict.LoserWasActive = true
	}

	preJSON, _ := json.Marshal(pre)
	postJSON, _ := json.Marshal(post)
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_lock_conflicts (scope, pre_outage_lock, post_outage_lock, winner, loser_was_active)
		VALUES ($1, $2, $3, $4, $5)`,
		pre.Scope, preJSON, postJSON, conflict.Winner, conflict.LoserWasActive); err != nil {
		return conflict, err
	}
	return conflict, nil
}

// insertLockTx inserts l into ops_remediation_locks within tx, stamping
// epoch. Used to copy a winning Redis lock into Postgres during
// reconciliation; the scope is free (the caller deleted any prior row).
func insertLockTx(ctx context.Context, tx pgx.Tx, l coordination.Lock, epoch uint64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO ops_remediation_locks
			(id, scope, operation, acquired_by, operation_id, acquired_at, expires_at, epoch, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		l.ID, l.Scope, l.Operation, l.AcquiredBy, nullStr(l.OperationID),
		l.AcquiredAt, l.ExpiresAt, int64(epoch), int64(l.Revision))
	return err
}

// scanLock decodes one ops_remediation_locks row from a pool query.
func scanLock(row pgx.Row) (*coordination.Lock, error) {
	var (
		l           coordination.Lock
		operationID *string
		epoch, rev  int64
	)
	if err := row.Scan(&l.ID, &l.Scope, &l.Operation, &l.AcquiredBy, &operationID,
		&l.AcquiredAt, &l.ExpiresAt, &epoch, &rev); err != nil {
		return nil, err
	}
	if operationID != nil {
		l.OperationID = *operationID
	}
	l.Epoch = uint64(epoch)
	l.Revision = uint64(rev)
	l.LockStore = coordination.StorePostgres
	return &l, nil
}

// scanLockTx decodes one row from a transaction query (the FOR UPDATE
// read in Reconcile).
func scanLockTx(row pgx.Row) (*coordination.Lock, error) { return scanLock(row) }

// collectLocks drains rows into a slice, classifying a transport error as
// ErrStoreUnavailable.
func collectLocks(rows pgx.Rows) ([]coordination.Lock, error) {
	defer rows.Close()
	var out []coordination.Lock
	for rows.Next() {
		l, err := scanLock(rows)
		if err != nil {
			return nil, classifyErr(err)
		}
		out = append(out, *l)
	}
	return out, classifyErr(rows.Err())
}

// nullStr returns nil for an empty string so the column stores NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Compile-time guard that *Store satisfies the coordination tier and
// reconciliation contracts.
var (
	_ coordination.Store        = (*Store)(nil)
	_ coordination.EpochManager = (*Store)(nil)
)
