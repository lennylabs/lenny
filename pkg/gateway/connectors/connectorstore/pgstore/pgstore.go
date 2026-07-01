// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed connectorstore.Store,
// persisting the §9.3 connector registry to the connectors table. It
// is a drop-in alternative to connectorstore.Memory.
//
// spec: §4.2 line 173 — connectors are tenant-scoped. Each row
// carries a `tenant_id` column under the standard lenny_tenant_guard
// trigger and lenny_tenant_isolation RLS policy. Every operation
// runs inside a pgtenant transaction that sets app.current_tenant to
// the caller's tenant id (concrete value for tenant-admin paths, the
// AllTenantsSentinel for platform-admin reads).
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

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed connector registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ connectorstore.Store = (*Store)(nil)

const selectList = `tenant_id, id, display_name, mcp_server_url, transport, auth,
	visibility, labels, capability_inference_mode, capabilities, tool_capabilities,
	capabilities_refreshed_at, created_at, updated_at, deleted_at, version`

// Create inserts a new connector row after running the §9.3
// validation. Returns ErrAlreadyExists when (tenant_id, id) collides.
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
	caps, err := capabilitiesJSON(c.Capabilities)
	if err != nil {
		return err
	}
	toolCaps, err := toolCapabilitiesJSON(c.ToolCapabilities)
	if err != nil {
		return err
	}
	// spec: §15.1 line 1207 — every admin resource version starts at 1.
	if c.Version == 0 {
		c.Version = 1
	}
	return pgtenant.InTx(ctx, s.pool, c.TenantID, func(tx pgx.Tx) error {
		_, ierr := tx.Exec(ctx, `INSERT INTO connectors (
			tenant_id, id, display_name, mcp_server_url, transport, auth,
			visibility, labels, capability_inference_mode, capabilities, tool_capabilities,
			capabilities_refreshed_at, created_at, updated_at, deleted_at, version
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8::jsonb, $9, $10::jsonb, $11::jsonb, $12, $13, $14, $15, $16)`,
			c.TenantID, c.ID, c.DisplayName, c.MCPServerURL, c.Transport, auth,
			c.Visibility, labels, capabilityInferenceMode(c.CapabilityInferenceMode), caps, toolCaps,
			pgtenant.NullTime(c.CapabilitiesRefreshedAt), c.CreatedAt, c.UpdatedAt, pgtenant.NullTime(c.DeletedAt), c.Version)
		var pgErr *pgconn.PgError
		if errors.As(ierr, &pgErr) && pgErr.Code == "23505" {
			return connectorstore.ErrAlreadyExists
		}
		return ierr
	})
}

// Get returns the connector row keyed by (tenantID, id). Soft-deleted
// rows are returned (callers consult Connector.IsActive()); a missing
// row is ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantID, id string) (connectorstore.Connector, error) {
	var out connectorstore.Connector
	err := s.runRead(ctx, tenantID, func(tx pgx.Tx) error {
		var row pgx.Row
		if tenantID == connectorstore.AllTenantsSentinel {
			row = tx.QueryRow(ctx,
				`SELECT `+selectList+` FROM connectors WHERE id = $1`, id)
		} else {
			row = tx.QueryRow(ctx,
				`SELECT `+selectList+` FROM connectors WHERE id = $1 AND tenant_id = $2`,
				id, tenantID)
		}
		c, err := scanConnector(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return connectorstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = c
		return nil
	})
	if err != nil {
		return connectorstore.Connector{}, err
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-runs the §9.3 validation, and advances UpdatedAt.
func (s *Store) Update(ctx context.Context, tenantID, id string, mutate func(*connectorstore.Connector) error) (connectorstore.Connector, error) {
	if tenantID == "" || tenantID == connectorstore.AllTenantsSentinel {
		return connectorstore.Connector{}, errors.New("connectorstore: Update requires a concrete tenant_id")
	}
	var out connectorstore.Connector
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM connectors WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
			id, tenantID)
		c, err := scanConnector(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return connectorstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		prev := c.UpdatedAt
		if err := mutate(&c); err != nil {
			return err
		}
		c.TenantID = tenantID
		if err := c.Validate(); err != nil {
			return err
		}
		c.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
		// spec: §15.1 line 1207 — bump the optimistic-concurrency version on
		// every successful Update so the next If-Match compares against it.
		c.Version++
		auth, err := authJSON(c.Auth)
		if err != nil {
			return err
		}
		labels, err := labelsJSON(c.Labels)
		if err != nil {
			return err
		}
		caps, err := capabilitiesJSON(c.Capabilities)
		if err != nil {
			return err
		}
		toolCaps, err := toolCapabilitiesJSON(c.ToolCapabilities)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE connectors SET
			display_name = $3, mcp_server_url = $4, transport = $5, auth = $6::jsonb,
			visibility = $7, labels = $8::jsonb, capability_inference_mode = $9,
			capabilities = $10::jsonb, tool_capabilities = $11::jsonb,
			capabilities_refreshed_at = $12, updated_at = $13, deleted_at = $14, version = $15
		WHERE id = $1 AND tenant_id = $2`,
			id, tenantID, c.DisplayName, c.MCPServerURL, c.Transport, auth,
			c.Visibility, labels, capabilityInferenceMode(c.CapabilityInferenceMode), caps, toolCaps,
			pgtenant.NullTime(c.CapabilitiesRefreshedAt), c.UpdatedAt, pgtenant.NullTime(c.DeletedAt), c.Version); err != nil {
			return err
		}
		out = c
		return nil
	})
	if err != nil {
		return connectorstore.Connector{}, err
	}
	return out, nil
}

// List returns the connector rows for the supplied tenant id (or
// across every tenant under the AllTenantsSentinel), ordered by
// (tenant_id, id). Soft-deleted rows are dropped unless
// filter.IncludeDeleted is set.
func (s *Store) List(ctx context.Context, tenantID string, filter connectorstore.ListFilter) ([]connectorstore.Connector, error) {
	var out []connectorstore.Connector
	err := s.runRead(ctx, tenantID, func(tx pgx.Tx) error {
		q := `SELECT ` + selectList + ` FROM connectors`
		if !filter.IncludeDeleted {
			q += ` WHERE deleted_at IS NULL`
		}
		q += ` ORDER BY tenant_id, id`

		rows, err := tx.Query(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanConnector(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SoftDelete sets deleted_at on the row. It is idempotent:
// soft-deleting an already-deleted connector is a no-op success.
func (s *Store) SoftDelete(ctx context.Context, tenantID, id string, at time.Time) error {
	if tenantID == "" || tenantID == connectorstore.AllTenantsSentinel {
		return errors.New("connectorstore: SoftDelete requires a concrete tenant_id")
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE connectors SET deleted_at = $3, updated_at = $3, version = version + 1
			 WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
			id, tenantID, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM connectors WHERE id = $1 AND tenant_id = $2)`,
			id, tenantID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return connectorstore.ErrNotFound
		}
		return nil
	})
}

// DeleteByUser implements the §12.1 mandatory-erasure primitive.
// Connectors are tenant-scoped and carry no user attribution, so the
// method returns (0, nil) — the orchestrator skips connectorstore on
// user-scoped runs.
//
// spec: §12.1 line 5.
func (s *Store) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
// Hard-deletes every connector owned by tenantID, returning the
// number of rows removed. The erasure orchestrator invokes this from
// the §12.8 Phase 4 tenant-deletion controller.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" || tenantID == connectorstore.AllTenantsSentinel {
		return 0, errors.New("connectorstore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM connectors WHERE tenant_id = $1`, tenantID)
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

// runRead wraps fn in either a tenant-scoped or all-tenants transaction
// depending on tenantID. The AllTenantsSentinel value invokes the §4.2
// platform-admin bypass; concrete tenant ids invoke the per-tenant
// context.
func (s *Store) runRead(ctx context.Context, tenantID string, fn func(pgx.Tx) error) error {
	if tenantID == connectorstore.AllTenantsSentinel {
		return pgtenant.InAllTenants(ctx, s.pool, fn)
	}
	if tenantID == "" {
		return fmt.Errorf("connectorstore: read requires a concrete tenant_id or AllTenantsSentinel")
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, fn)
}

// scanConnector reads one row in selectList order into a Connector.
func scanConnector(row pgx.Row) (connectorstore.Connector, error) {
	var (
		c                      connectorstore.Connector
		authRaw, labelsRaw     []byte
		capsRaw, toolCapsRaw   []byte
		inferMode              string
		refreshedAt, deletedAt *time.Time
	)
	if err := row.Scan(
		&c.TenantID, &c.ID, &c.DisplayName, &c.MCPServerURL, &c.Transport, &authRaw,
		&c.Visibility, &labelsRaw, &inferMode, &capsRaw, &toolCapsRaw,
		&refreshedAt, &c.CreatedAt, &c.UpdatedAt, &deletedAt, &c.Version,
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
	c.CapabilityInferenceMode = capabilityinference.Mode(inferMode)
	if len(capsRaw) > 0 {
		if err := json.Unmarshal(capsRaw, &c.Capabilities); err != nil {
			return connectorstore.Connector{}, fmt.Errorf("connectorstore: decode capabilities: %w", err)
		}
	}
	if len(toolCapsRaw) > 0 {
		if err := json.Unmarshal(toolCapsRaw, &c.ToolCapabilities); err != nil {
			return connectorstore.Connector{}, fmt.Errorf("connectorstore: decode tool_capabilities: %w", err)
		}
	}
	if refreshedAt != nil {
		c.CapabilitiesRefreshedAt = *refreshedAt
	}
	if deletedAt != nil {
		c.DeletedAt = *deletedAt
	}
	return c, nil
}

// capabilityInferenceMode returns the persisted §5.1 inference mode,
// defaulting an empty value to strict so a row that predates the
// capability columns reads back the spec default rather than the empty
// string.
func capabilityInferenceMode(m capabilityinference.Mode) string {
	if m == "" {
		return string(capabilityinference.DefaultMode)
	}
	return string(m)
}

// capabilitiesJSON marshals the §5.1 inferred capability union for the
// capabilities jsonb column. A nil slice serializes as an empty array.
func capabilitiesJSON(caps []capabilityinference.Capability) (string, error) {
	if caps == nil {
		caps = []capabilityinference.Capability{}
	}
	b, err := json.Marshal(caps)
	if err != nil {
		return "", fmt.Errorf("connectorstore: encode capabilities: %w", err)
	}
	return string(b), nil
}

// toolCapabilitiesJSON marshals the §5.1 per-tool capability map for the
// tool_capabilities jsonb column. A nil map serializes as an empty
// object.
func toolCapabilitiesJSON(m map[string][]capabilityinference.Capability) (string, error) {
	if m == nil {
		m = map[string][]capabilityinference.Capability{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("connectorstore: encode tool_capabilities: %w", err)
	}
	return string(b), nil
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
