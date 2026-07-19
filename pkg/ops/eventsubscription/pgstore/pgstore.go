// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed eventsubscription.Store. It
// persists §25.5 webhook subscriptions and their delivery rows to the
// ops_event_subscriptions and ops_event_deliveries tables so the v1
// lenny-ops binary survives a process restart with the subscription
// registry intact.
//
// The tables are platform-scoped (the §25 control plane is not
// multi-tenanted at this boundary), so the store does not run inside
// pgtenant.InTx; the tables have no RLS policy. The subscription's
// own tenant_filter column carries the §25.5 tenant-isolation scope.
// spec: §25.5 lines 2613-2664.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// Store is the Postgres-backed §25.5 webhook subscription registry.
// Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var _ eventsubscription.Store = (*Store)(nil)

const subscriptionColumns = `id, callback_url, types, severity, description,
	secret_hash, secret_fingerprint, previous_secret_fingerprint, secret_rotated_at,
	created_by, created_by_tenant_id, tenant_filter, generation,
	created_at, updated_at, active`

// Create inserts a new subscription row.
func (s *Store) Create(ctx context.Context, rec eventsubscription.Record) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO ops_event_subscriptions (`+subscriptionColumns+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		rec.ID, rec.CallbackURL, rec.Types, nullStrings(rec.Severity), nullStr(rec.Description),
		rec.SecretHash, rec.SecretFingerprint, nullStr(rec.PreviousSecretFingerprint), nullTime(rec.SecretRotatedAt),
		rec.CreatedBy, nullStr(rec.CreatedByTenantID), rec.TenantFilter, rec.Generation,
		rec.CreatedAt, rec.UpdatedAt, rec.Active)
	return err
}

// Get returns the subscription or a typed NotFound error.
func (s *Store) Get(ctx context.Context, id string) (eventsubscription.Record, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+subscriptionColumns+` FROM ops_event_subscriptions WHERE id = $1`, id)
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return eventsubscription.Record{}, notFound(id)
	}
	if err != nil {
		return eventsubscription.Record{}, err
	}
	return rec, nil
}

// List returns every subscription ordered by id for stable output.
func (s *Store) List(ctx context.Context) ([]eventsubscription.Record, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+subscriptionColumns+` FROM ops_event_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []eventsubscription.Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Update reads the row under a transaction, applies mutate, and writes
// the result back. The read-modify-write runs in a single transaction so
// concurrent updates serialize through row locking (SELECT ... FOR
// UPDATE). A missing id returns NotFound; a mutate error rolls back.
func (s *Store) Update(ctx context.Context, id string, mutate func(*eventsubscription.Record) error) (eventsubscription.Record, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return eventsubscription.Record{}, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx,
		`SELECT `+subscriptionColumns+` FROM ops_event_subscriptions WHERE id = $1 FOR UPDATE`, id)
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return eventsubscription.Record{}, notFound(id)
	}
	if err != nil {
		return eventsubscription.Record{}, err
	}
	if err := mutate(&rec); err != nil {
		return eventsubscription.Record{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE ops_event_subscriptions SET
			callback_url=$2, types=$3, severity=$4, description=$5,
			secret_hash=$6, secret_fingerprint=$7, previous_secret_fingerprint=$8, secret_rotated_at=$9,
			created_by_tenant_id=$10, tenant_filter=$11, generation=$12, updated_at=$13, active=$14
		 WHERE id=$1`,
		rec.ID, rec.CallbackURL, rec.Types, nullStrings(rec.Severity), nullStr(rec.Description),
		rec.SecretHash, rec.SecretFingerprint, nullStr(rec.PreviousSecretFingerprint), nullTime(rec.SecretRotatedAt),
		nullStr(rec.CreatedByTenantID), rec.TenantFilter, rec.Generation, rec.UpdatedAt, rec.Active); err != nil {
		return eventsubscription.Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return eventsubscription.Record{}, err
	}
	return rec, nil
}

// Delete removes the subscription and returns the deleted row for the
// audit trail. A missing id returns NotFound.
func (s *Store) Delete(ctx context.Context, id string) (eventsubscription.Record, error) {
	row := s.pool.QueryRow(ctx,
		`DELETE FROM ops_event_subscriptions WHERE id = $1 RETURNING `+subscriptionColumns, id)
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return eventsubscription.Record{}, notFound(id)
	}
	if err != nil {
		return eventsubscription.Record{}, err
	}
	return rec, nil
}

// RecordDelivery appends a delivery-tracking row, allocating the
// BIGSERIAL id.
func (s *Store) RecordDelivery(ctx context.Context, d eventsubscription.Delivery) (eventsubscription.Delivery, error) {
	created := d.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO ops_event_deliveries
			(subscription_id, event_id, event_type, status, attempts, last_attempt_at, last_error, created_at, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		d.SubscriptionID, d.EventID, d.EventType, d.Status, d.Attempts,
		nullTime(d.LastAttemptAt), nullStr(d.LastError), created, d.ExpiresAt)
	if err := row.Scan(&d.ID); err != nil {
		return eventsubscription.Delivery{}, err
	}
	d.CreatedAt = created
	return d, nil
}

// ListDeliveries returns a keyset page of deliveries for subID,
// newest-first by id. An empty cursor returns the newest page; a
// non-empty cursor is the primary key of the previous page's last row and
// returns the rows with a smaller id. The query fetches limit+1 rows to
// decide HasMore. A malformed cursor, or one whose row has aged out below
// the oldest retained delivery, returns a gap (GapDetected with
// OldestAvailableCursor) rather than a silently empty page, so the caller
// resyncs from the retention floor. spec: §25.5 (deliveries keyset
// pagination, gap on aged-out cursor).
func (s *Store) ListDeliveries(ctx context.Context, subID string, cursor string, limit int) ([]eventsubscription.Delivery, eventsubscription.Pagination, error) {
	var cursorID int64
	haveCursor := false
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			// A malformed cursor cannot be honored; report the gap toward the
			// oldest retained delivery rather than a silently empty page.
			return s.deliveriesGap(ctx, subID)
		}
		cursorID = n
		haveCursor = true
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, subscription_id, event_id, event_type, status, attempts,
			last_attempt_at, last_error, created_at, expires_at
		 FROM ops_event_deliveries
		 WHERE subscription_id = $1 AND ($2 = '' OR id < $2::bigint)
		 ORDER BY id DESC LIMIT $3`,
		subID, cursor, limit+1)
	if err != nil {
		return nil, eventsubscription.Pagination{}, err
	}
	defer rows.Close()
	var out []eventsubscription.Delivery
	for rows.Next() {
		var (
			d           eventsubscription.Delivery
			lastAttempt *time.Time
			lastErr     *string
		)
		if err := rows.Scan(&d.ID, &d.SubscriptionID, &d.EventID, &d.EventType, &d.Status,
			&d.Attempts, &lastAttempt, &lastErr, &d.CreatedAt, &d.ExpiresAt); err != nil {
			return nil, eventsubscription.Pagination{}, err
		}
		if lastAttempt != nil {
			d.LastAttemptAt = *lastAttempt
		}
		if lastErr != nil {
			d.LastError = *lastErr
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, eventsubscription.Pagination{}, err
	}

	page := eventsubscription.Pagination{CursorKind: eventsubscription.CursorKindPK}
	if len(out) > limit {
		out = out[:limit]
		page.HasMore = true
		page.Cursor = strconv.FormatInt(out[len(out)-1].ID, 10)
	}
	// An empty continuation page can mean the natural end of the history or
	// a cursor that aged out below the retention floor. Distinguish them:
	// when the cursor points below the oldest retained delivery, rows
	// between it and the floor were purged, so report a gap. spec: §25.5
	// (gap on aged-out cursor).
	if haveCursor && len(out) == 0 {
		oldest, ok, err := s.oldestDeliveryID(ctx, subID)
		if err != nil {
			return nil, eventsubscription.Pagination{}, err
		}
		if ok && cursorID < oldest {
			page.GapDetected = true
			page.OldestAvailableCursor = strconv.FormatInt(oldest, 10)
		}
	}
	return out, page, nil
}

// oldestDeliveryID returns the smallest retained delivery id for subID,
// or ok=false when the subscription has no retained deliveries. spec:
// §25.5 (gap on aged-out cursor).
func (s *Store) oldestDeliveryID(ctx context.Context, subID string) (int64, bool, error) {
	var id *int64
	if err := s.pool.QueryRow(ctx,
		`SELECT MIN(id) FROM ops_event_deliveries WHERE subscription_id = $1`, subID).Scan(&id); err != nil {
		return 0, false, fmt.Errorf("oldest delivery id for %s: %w", subID, err)
	}
	if id == nil {
		return 0, false, nil
	}
	return *id, true, nil
}

// deliveriesGap builds the gap pagination for a cursor that cannot be
// honored, pointing the caller at the oldest retained delivery. spec:
// §25.5 (gap on aged-out cursor).
func (s *Store) deliveriesGap(ctx context.Context, subID string) ([]eventsubscription.Delivery, eventsubscription.Pagination, error) {
	page := eventsubscription.Pagination{CursorKind: eventsubscription.CursorKindPK}
	oldest, ok, err := s.oldestDeliveryID(ctx, subID)
	if err != nil {
		return nil, eventsubscription.Pagination{}, err
	}
	if ok {
		page.GapDetected = true
		page.OldestAvailableCursor = strconv.FormatInt(oldest, 10)
	}
	return nil, page, nil
}

// DeleteExpired removes up to limit delivery rows whose expires_at is at
// or before before, returning the number deleted. The §25.5 retention
// cron calls it in a loop with limit=10000 (line 2661) until a sweep
// deletes fewer than limit, so a single statement never holds a long
// lock. The ctid sub-select bounds the DELETE to limit rows without a
// table-wide scan. spec: §25.5 lines 2649-2664.
func (s *Store) DeleteExpired(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 10000
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM ops_event_deliveries
		 WHERE ctid IN (
			SELECT ctid FROM ops_event_deliveries
			WHERE expires_at <= $1
			ORDER BY expires_at
			LIMIT $2
		 )`, before, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func scanRecord(row pgx.Row) (eventsubscription.Record, error) {
	var (
		rec          eventsubscription.Record
		severity     []string
		description  *string
		prevFinger   *string
		rotatedAt    *time.Time
		createdByTen *string
	)
	if err := row.Scan(
		&rec.ID, &rec.CallbackURL, &rec.Types, &severity, &description,
		&rec.SecretHash, &rec.SecretFingerprint, &prevFinger, &rotatedAt,
		&rec.CreatedBy, &createdByTen, &rec.TenantFilter, &rec.Generation,
		&rec.CreatedAt, &rec.UpdatedAt, &rec.Active,
	); err != nil {
		return eventsubscription.Record{}, err
	}
	rec.Severity = severity
	if description != nil {
		rec.Description = *description
	}
	if prevFinger != nil {
		rec.PreviousSecretFingerprint = *prevFinger
	}
	if rotatedAt != nil {
		rec.SecretRotatedAt = *rotatedAt
	}
	if createdByTen != nil {
		rec.CreatedByTenantID = *createdByTen
	}
	return rec, nil
}

func notFound(id string) error {
	return &eventsubscription.Error{
		Code:    eventsubscription.ErrCodeNotFound,
		Message: fmt.Sprintf("subscription %q not found", id),
	}
}

// nullStr maps an empty string to a SQL NULL so a NULLable text column
// stores NULL rather than ”.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullStrings maps an empty/nil slice to SQL NULL for the NULLable
// severity TEXT[] column.
func nullStrings(s []string) any {
	if len(s) == 0 {
		return nil
	}
	return s
}

// nullTime maps the zero time to SQL NULL for NULLable timestamptz
// columns.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
