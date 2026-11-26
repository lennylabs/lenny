// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres signingkeys.Store over the
// platform_signing_keys table (migration 0204). The table is platform-
// scoped, with no tenant_id column and no RLS, so the store runs plain
// queries without an app.current_tenant context. The secret column holds
// only the wrapped form the caller's Sealer produced.
//
// spec: UPLOAD, SESS
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/signingkeys"
)

// uniqueViolation is the SQLSTATE of a unique-constraint violation.
const uniqueViolation = "23505"

// Store is the Postgres signingkeys.Store.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store over pool.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("signingkeys/pgstore: nil pool")
	}
	return &Store{pool: pool}, nil
}

var _ signingkeys.Store = (*Store)(nil)

// List implements signingkeys.Store.
func (s *Store) List(ctx context.Context, purpose string, now time.Time) ([]signingkeys.Record, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT purpose, key_id, algorithm, kek_alias, wrapped_secret, public_key,
		       rotation_slot, active_from, retire_after, delete_after
		FROM platform_signing_keys
		WHERE purpose = $1 AND (delete_after IS NULL OR delete_after > $2)
		ORDER BY rotation_slot`, purpose, now)
	if err != nil {
		return nil, fmt.Errorf("signingkeys/pgstore: list %q: %w", purpose, err)
	}
	defer rows.Close()
	var out []signingkeys.Record
	for rows.Next() {
		var (
			rec          signingkeys.Record
			retire, dele *time.Time
		)
		if err := rows.Scan(&rec.Purpose, &rec.KeyID, &rec.Algorithm, &rec.KEKAlias,
			&rec.WrappedSecret, &rec.PublicKey, &rec.RotationSlot, &rec.ActiveFrom,
			&retire, &dele); err != nil {
			return nil, fmt.Errorf("signingkeys/pgstore: scan %q key: %w", purpose, err)
		}
		rec.ActiveFrom = rec.ActiveFrom.UTC()
		if retire != nil {
			rec.RetireAfter = retire.UTC()
		}
		if dele != nil {
			rec.DeleteAfter = dele.UTC()
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signingkeys/pgstore: list %q: %w", purpose, err)
	}
	return out, nil
}

// Insert implements signingkeys.Store. The insert and the retirement of
// the key at the previous slot run in one transaction, so a reader never
// sees the next key without the previous key's deletion instant, and the
// unique (purpose, rotation_slot) constraint decides which of several
// concurrent writers holds the slot.
//
// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
func (s *Store) Insert(ctx context.Context, rec signingkeys.Record, verifyFor time.Duration) (bool, error) {
	inserted := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO platform_signing_keys
				(purpose, key_id, algorithm, kek_alias, wrapped_secret, public_key,
				 rotation_slot, active_from)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT ON CONSTRAINT platform_signing_keys_slot_unique DO NOTHING`,
			rec.Purpose, rec.KeyID, rec.Algorithm, rec.KEKAlias, rec.WrappedSecret,
			rec.PublicKey, rec.RotationSlot, rec.ActiveFrom)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		inserted = true
		_, err = tx.Exec(ctx, `
			UPDATE platform_signing_keys
			SET retire_after = $3, delete_after = $4
			WHERE purpose = $1 AND rotation_slot = $2`,
			rec.Purpose, rec.RotationSlot-1, rec.ActiveFrom, rec.ActiveFrom.Add(verifyFor))
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			// A key-id collision on the primary key: the slot constraint
			// already absorbed the slot race, so this is a duplicate id.
			return false, fmt.Errorf("signingkeys/pgstore: duplicate %q key id %s: %w", rec.Purpose, rec.KeyID, err)
		}
		return false, fmt.Errorf("signingkeys/pgstore: insert %q key at slot %d: %w", rec.Purpose, rec.RotationSlot, err)
	}
	return inserted, nil
}

// DeleteExpired implements signingkeys.Store. A row is deleted only once
// its delete_after has passed on both the caller's clock and the database
// server's clock, so a replica whose clock runs ahead cannot cut the
// overlap window short for every replica.
//
// spec: §7.1 (Normal Flow)
func (s *Store) DeleteExpired(ctx context.Context, purpose string, now time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM platform_signing_keys
		WHERE purpose = $1 AND delete_after IS NOT NULL AND delete_after <= LEAST($2::timestamptz, now())`, purpose, now)
	if err != nil {
		return 0, fmt.Errorf("signingkeys/pgstore: delete expired %q keys: %w", purpose, err)
	}
	return int(tag.RowsAffected()), nil
}
