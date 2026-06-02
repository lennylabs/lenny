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

// ListDeliveries returns up to limit recent deliveries for subID,
// newest-first by id.
func (s *Store) ListDeliveries(ctx context.Context, subID string, limit int) ([]eventsubscription.Delivery, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, subscription_id, event_id, event_type, status, attempts,
			last_attempt_at, last_error, created_at, expires_at
		 FROM ops_event_deliveries WHERE subscription_id = $1 ORDER BY id DESC LIMIT $2`,
		subID, limit)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		if lastAttempt != nil {
			d.LastAttemptAt = *lastAttempt
		}
		if lastErr != nil {
			d.LastError = *lastErr
		}
		out = append(out, d)
	}
	return out, rows.Err()
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
