// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed eventsubscription.Store. It
// persists §25.5 webhook subscriptions to the ops_event_subscriptions
// table from migration 0046 so the v1 lenny-ops binary can survive a
// process restart with the subscription registry intact.
//
// The ops_event_subscriptions table is platform-scoped (the §25
// control plane is not multi-tenanted at this boundary), so the
// store does not run inside pgtenant.InTx; the table has no RLS
// policy and no tenant column.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// Store is the Postgres-backed §25.5 webhook subscription registry.
// Construct with New.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
	idFn func() string
}

// Option configures Store. Production callers pass no options; tests
// inject Now and IDFunc to pin timestamps and ids.
type Option func(*Store)

// WithNow overrides the clock source.
func WithNow(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// WithIDFunc overrides the id generator.
func WithIDFunc(idFn func() string) Option {
	return func(s *Store) { s.idFn = idFn }
}

// New returns a Store backed by pool. The pool must point at a
// database with the migrations/ schema applied.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	s := &Store{pool: pool}
	for _, opt := range opts {
		opt(s)
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	if s.idFn == nil {
		s.idFn = defaultIDFn
	}
	return s
}

var _ eventsubscription.Store = (*Store)(nil)

// Create inserts a new subscription row and returns the persisted
// record with the allocated id and timestamp.
func (s *Store) Create(ctx context.Context, req eventsubscription.CreateRequest) (eventsubscription.Subscription, error) {
	types := append([]string(nil), req.Types...)
	if len(types) > 0 {
		sort.Strings(types)
	}
	typesJSON, err := encodeTypes(types)
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	row := eventsubscription.Subscription{
		ID:          s.idFn(),
		CallbackURL: req.CallbackURL,
		Types:       types,
		Secret:      req.Secret,
		CreatedAt:   s.now().UTC(),
	}
	if _, err := s.pool.Exec(
		ctx,
		`INSERT INTO ops_event_subscriptions (id, callback_url, types, secret, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5)`,
		row.ID, row.CallbackURL, typesJSON, row.Secret, row.CreatedAt,
	); err != nil {
		return eventsubscription.Subscription{}, err
	}
	return row, nil
}

// Get returns the subscription or a typed NotFound error.
func (s *Store) Get(ctx context.Context, id string) (eventsubscription.Subscription, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, callback_url, types, secret, created_at
		 FROM ops_event_subscriptions WHERE id = $1`,
		id)
	out, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return eventsubscription.Subscription{}, &eventsubscription.Error{
			Code:    eventsubscription.ErrCodeNotFound,
			Message: fmt.Sprintf("subscription %q not found", id),
		}
	}
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	return out, nil
}

// List returns every subscription ordered by id for stable output,
// matching the in-memory MemoryStore.
func (s *Store) List(ctx context.Context) ([]eventsubscription.Subscription, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, callback_url, types, secret, created_at
		 FROM ops_event_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []eventsubscription.Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Delete removes the subscription. A missing id returns the typed
// NotFound error.
func (s *Store) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM ops_event_subscriptions WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return &eventsubscription.Error{
			Code:    eventsubscription.ErrCodeNotFound,
			Message: fmt.Sprintf("subscription %q not found", id),
		}
	}
	return nil
}

func encodeTypes(types []string) (string, error) {
	if len(types) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(types)
	if err != nil {
		return "", fmt.Errorf("eventsubscription: encode types: %w", err)
	}
	return string(b), nil
}

func scanSubscription(row pgx.Row) (eventsubscription.Subscription, error) {
	var (
		sub      eventsubscription.Subscription
		typesRaw []byte
	)
	if err := row.Scan(&sub.ID, &sub.CallbackURL, &typesRaw, &sub.Secret, &sub.CreatedAt); err != nil {
		return eventsubscription.Subscription{}, err
	}
	if len(typesRaw) > 0 {
		if err := json.Unmarshal(typesRaw, &sub.Types); err != nil {
			return eventsubscription.Subscription{}, fmt.Errorf("eventsubscription: decode types: %w", err)
		}
	}
	return sub, nil
}

// defaultIDFn allocates a subscription id mirroring the in-memory
// implementation's format.
func defaultIDFn() string {
	return fmt.Sprintf("sub_%d", time.Now().UnixNano())
}
