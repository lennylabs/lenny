// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// SessionTerminator backs the platform-admin session investigation
// endpoints (SessionAdmin) and the lifecycle drains (DrainTerminator).
// GetByID resolves a session by its global id from the session store, and
// ForceTerminate applies the forced terminal transition through the
// session core's GWSEAM-ForceTerminate.
//
// spec: GWSEAM-ForceTerminate
type SessionTerminator struct {
	// Store is the session store GetByID reads.
	Store sessionstore.Store
	// Force is the session core's force-terminate.
	Force seam.ForceTerminations
}

var (
	_ SessionAdmin    = SessionTerminator{}
	_ DrainTerminator = SessionTerminator{}
)

// GetByID returns the session row whose id equals id, across every tenant.
func (t SessionTerminator) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	return t.Store.GetByID(ctx, id)
}

// ForceTerminate moves a session of tenantID to failed and returns the row
// the session core wrote, the state the session left, and whether this
// call moved it. A session the core reports as not found is
// sessionstore.ErrNotFound, which the handlers and the drain branch on.
func (t SessionTerminator) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	rec, prev, transitioned, err := t.Force.ForceTerminate(ctx, tenantID, id)
	if errors.Is(err, seam.ErrSessionNotFound) {
		return sessionstore.Session{}, "", false, fmt.Errorf("%w: %w", sessionstore.ErrNotFound, err)
	}
	if err != nil {
		return sessionstore.Session{}, "", false, err
	}
	row, ok := rec.Stored.(sessionstore.Session)
	if !ok || row.ID != rec.ID || row.TenantID != rec.TenantID {
		return sessionstore.Session{}, "", false, fmt.Errorf("admin: force-terminate of session %s returned no stored row", id)
	}
	return row, session.State(prev), transitioned, nil
}
