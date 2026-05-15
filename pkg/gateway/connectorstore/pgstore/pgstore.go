// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed connectorstore.Store,
// persisting the §9.3 connector registry to the connectors table. It
// is a drop-in alternative to connectorstore.Memory.
//
// connectors is platform-global (§9.3): connectors are platform-wide
// records keyed by id, so operations run as plain queries without an
// app.current_tenant context. Create and Update run Connector.Validate
// so the §9.3 SSRF and transport guards hold regardless of backend.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed connector registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ connectorstore.Store = (*Store)(nil)

const selectList = `id, display_name, mcp_server_url, transport, auth,
	visibility, labels, created_at, updated_at, deleted_at`

// Create inserts a new connector row after running the §9.3
// validation. Returns ErrAlreadyExists when the id is taken.
func (s *Store) Create(ctx context.Context, c connectorstore.Connector) error {
	if err := c.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	auth, err := authJSON(c.Auth)
	if err != nil {
		return err
	}
	labels, err := labelsJSON(c.Labels)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO connectors (
		id, display_name, mcp_server_url, transport, auth,
		visibility, labels, created_at, updated_at, deleted_at
	) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7::jsonb, $8, $9, $10)`,
		c.ID, c.DisplayName, c.MCPServerURL, c.Transport, auth,
		c.Visibility, labels, c.CreatedAt, c.UpdatedAt, pgtenant.NullTime(c.DeletedAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return connectorstore.ErrAlreadyExists
	}
	return err
}

// Get returns the connector row keyed by id. Soft-deleted rows are
// returned (callers consult Connector.IsActive()); a missing row is
// ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (connectorstore.Connector, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+selectList+` FROM connectors WHERE id = $1`, id)
	c, err := scanConnector(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return connectorstore.Connector{}, connectorstore.ErrNotFound
	}
	if err != nil {
		return connectorstore.Connector{}, err
	}
	return c, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-runs the §9.3 validation, and advances UpdatedAt.
func (s *Store) Update(ctx context.Context, id string, mutate func(*connectorstore.Connector) error) (connectorstore.Connector, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return connectorstore.Connector{}, fmt.Errorf("connectorstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx,
		`SELECT `+selectList+` FROM connectors WHERE id = $1 FOR UPDATE`, id)
	c, err := scanConnector(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return connectorstore.Connector{}, connectorstore.ErrNotFound
	}
	if err != nil {
		return connectorstore.Connector{}, err
	}
	prev := c.UpdatedAt
	if err := mutate(&c); err != nil {
		return connectorstore.Connector{}, err
	}
	if err := c.Validate(); err != nil {
		return connectorstore.Connector{}, err
	}
	c.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
	auth, err := authJSON(c.Auth)
	if err != nil {
		return connectorstore.Connector{}, err
	}
	labels, err := labelsJSON(c.Labels)
	if err != nil {
		return connectorstore.Connector{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE connectors SET
		display_name = $2, mcp_server_url = $3, transport = $4, auth = $5::jsonb,
		visibility = $6, labels = $7::jsonb, updated_at = $8, deleted_at = $9
	WHERE id = $1`,
		id, c.DisplayName, c.MCPServerURL, c.Transport, auth,
		c.Visibility, labels, c.UpdatedAt, pgtenant.NullTime(c.DeletedAt)); err != nil {
		return connectorstore.Connector{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return connectorstore.Connector{}, fmt.Errorf("connectorstore: commit: %w", err)
	}
	return c, nil
}

// List returns the connector rows id-ascending. Soft-deleted rows are
// dropped unless filter.IncludeDeleted is set.
func (s *Store) List(ctx context.Context, filter connectorstore.ListFilter) ([]connectorstore.Connector, error) {
	q := `SELECT ` + selectList + ` FROM connectors`
	if !filter.IncludeDeleted {
		q += ` WHERE deleted_at IS NULL`
	}
	q += ` ORDER BY id`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []connectorstore.Connector
	for rows.Next() {
		c, err := scanConnector(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SoftDelete sets deleted_at on the row. It is idempotent:
// soft-deleting an already-deleted connector is a no-op success.
func (s *Store) SoftDelete(ctx context.Context, id string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE connectors SET deleted_at = $2, updated_at = $2
		 WHERE id = $1 AND deleted_at IS NULL`, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM connectors WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return connectorstore.ErrNotFound
	}
	return nil
}

// scanConnector reads one row in selectList order into a Connector.
func scanConnector(row pgx.Row) (connectorstore.Connector, error) {
	var (
		c                  connectorstore.Connector
		authRaw, labelsRaw []byte
		deletedAt          *time.Time
	)
	if err := row.Scan(
		&c.ID, &c.DisplayName, &c.MCPServerURL, &c.Transport, &authRaw,
		&c.Visibility, &labelsRaw, &c.CreatedAt, &c.UpdatedAt, &deletedAt,
	); err != nil {
		return connectorstore.Connector{}, err
	}
	if len(authRaw) > 0 {
		var a connectorstore.ConnectorAuth
		if err := json.Unmarshal(authRaw, &a); err != nil {
			return connectorstore.Connector{}, fmt.Errorf("connectorstore: decode auth: %w", err)
		}
		c.Auth = &a
	}
	if len(labelsRaw) > 0 {
		if err := json.Unmarshal(labelsRaw, &c.Labels); err != nil {
			return connectorstore.Connector{}, fmt.Errorf("connectorstore: decode labels: %w", err)
		}
	}
	if deletedAt != nil {
		c.DeletedAt = *deletedAt
	}
	return c, nil
}

// authJSON marshals an optional ConnectorAuth to a JSON string for
// the auth jsonb column, or nil for a public connector.
func authJSON(a *connectorstore.ConnectorAuth) (*string, error) {
	if a == nil {
		return nil, nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("connectorstore: encode auth: %w", err)
	}
	s := string(b)
	return &s, nil
}

// labelsJSON marshals the label map for the labels jsonb column.
func labelsJSON(m map[string]string) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("connectorstore: encode labels: %w", err)
	}
	return string(b), nil
}
