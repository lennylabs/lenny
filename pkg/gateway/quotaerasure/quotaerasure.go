// SPDX-License-Identifier: MIT

// Package quotaerasure composes the §12.2 QuotaStore erasure across its
// backends into the single §12.8 step-6 erasure the orchestrator wires as
// one "quota" step. The QuotaStore role is "Redis + Postgres" (§12.2): the
// Redis token counter (pkg/gateway/quotastore), the Postgres
// token_usage_checkpoint (pkg/gateway/quotacheckpoint/pgstore), and the
// in-memory fail-open accumulator (pkg/gateway/quotafailopen) that the
// §11.2 line-48 MAX rule folds in as recovery source (2). A GDPR erasure
// must clear all three: deleting only the Redis counter leaves the
// Postgres checkpoint and the in-memory accumulator able to re-seed the
// erased user's usage on the next recovery reconcile.
//
// spec: §12.2 (QuotaStore — Redis + Postgres); §12.8 step 6 ("delete
// per-user rate-limit counters and budget tracking (Redis + Postgres)");
// §12.1 line 5 (mandatory erasure primitives).
package quotaerasure

import (
	"context"
	"fmt"
)

// UserEraser is the §12.1 DeleteByUser primitive one quota backend exposes.
type UserEraser interface {
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
}

// TenantEraser is the §12.1 DeleteByTenant primitive one quota backend
// exposes.
type TenantEraser interface {
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// Backend is one named QuotaStore backend that participates in erasure. A
// backend may expose only the user eraser, only the tenant eraser, or both
// (the Redis counter, Postgres checkpoint, and in-memory accumulator all
// expose both).
type Backend struct {
	Name   string
	User   UserEraser
	Tenant TenantEraser
}

// Composite erases a user's or tenant's quota state across every wired
// backend, summing the deleted-row counts. Construct with New.
type Composite struct {
	backends []Backend
}

// New returns a Composite over the backends in order, dropping any backend
// that exposes neither eraser so a no-Redis or no-Postgres deployment
// erases the subset it can satisfy rather than carrying a dead entry.
func New(backends ...Backend) *Composite {
	kept := make([]Backend, 0, len(backends))
	for _, b := range backends {
		if b.User == nil && b.Tenant == nil {
			continue
		}
		kept = append(kept, b)
	}
	return &Composite{backends: kept}
}

// DeleteByUser erases the user across every backend exposing a per-user
// eraser and returns the summed deleted-row count. Every backend is
// attempted even when an earlier one errors (each backend's DeleteByUser is
// idempotent, so a retry re-runs only the failed backend); the first error
// is returned alongside the partial count so the orchestrator records the
// step as failed and retries. spec: §12.8 step 6; §12.1 line 5.
func (c *Composite) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	total := 0
	var firstErr error
	for _, b := range c.backends {
		if b.User == nil {
			continue
		}
		n, err := b.User.DeleteByUser(ctx, tenantID, userID)
		total += n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("quotaerasure: backend %q DeleteByUser: %w", b.Name, err)
		}
	}
	return total, firstErr
}

// DeleteByTenant erases the tenant across every backend exposing a tenant
// eraser and returns the summed deleted-row count. As with DeleteByUser,
// every backend is attempted and the first error is returned with the
// partial count. spec: §12.8 Phase 4; §12.1 line 5.
func (c *Composite) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	total := 0
	var firstErr error
	for _, b := range c.backends {
		if b.Tenant == nil {
			continue
		}
		n, err := b.Tenant.DeleteByTenant(ctx, tenantID)
		total += n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("quotaerasure: backend %q DeleteByTenant: %w", b.Name, err)
		}
	}
	return total, firstErr
}
