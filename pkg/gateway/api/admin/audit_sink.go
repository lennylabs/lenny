// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// AuditLog is the §11.7 audit-chain surface the admin subsystem
// writes to and reads from. The in-memory audit.ChainSet (via the
// chainSetAuditLog adapter) and the Postgres-backed
// pkg/gateway/auditstore both satisfy it, so the gateway swaps the
// audit backend without changing the admin handlers.
type AuditLog interface {
	// Append commits an admin event to the tenant's chain.
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
	// Rows returns the tenant's audit rows in sequence order.
	Rows(ctx context.Context, tenantID string) ([]audit.Row, error)
	// Verify reports the tenant chain's §11.7 integrity state.
	Verify(ctx context.Context, tenantID string) (audit.VerifyResult, error)
}

// chainSetAuditLog adapts an in-memory audit.ChainSet to AuditLog.
type chainSetAuditLog struct {
	chains *audit.ChainSet
	clock  func() time.Time
}

func (a chainSetAuditLog) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if at.IsZero() {
		if a.clock != nil {
			at = a.clock()
		} else {
			at = time.Now().UTC()
		}
	}
	return a.chains.Append(tenantID, eventType, payload, at), nil
}

func (a chainSetAuditLog) Rows(_ context.Context, tenantID string) ([]audit.Row, error) {
	c := a.chains.Chain(tenantID)
	if c == nil {
		return nil, nil
	}
	return c.Rows(), nil
}

func (a chainSetAuditLog) Verify(_ context.Context, tenantID string) (audit.VerifyResult, error) {
	c := a.chains.Chain(tenantID)
	if c == nil {
		return audit.VerifyResult{Integrity: audit.ChainVerified}, nil
	}
	return c.Verify(), nil
}
