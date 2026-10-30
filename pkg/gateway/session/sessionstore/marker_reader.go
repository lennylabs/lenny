// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
)

// MarkerReader returns the creation fence's marker state for a session that
// userID owns in tenantID. It leaves IsDelegatedChild unset; the caller sets
// it from the row. A returned error fails the insert, or the gate, closed.
type MarkerReader func(ctx context.Context, tenantID, userID string) (CreationMarkers, error)

// TenantGetter is the tenant-store read StoreMarkers needs.
type TenantGetter interface {
	Get(ctx context.Context, id string) (tenantstore.Tenant, error)
}

// UserGetter is the user-store read StoreMarkers needs.
type UserGetter interface {
	Get(ctx context.Context, tenantID, subject string) (userstore.User, error)
}

// StoreMarkers returns a MarkerReader over a tenant store and a user store.
// The in-memory store's fence, the gateway's early creation gates, and the
// delegation gate share it, so every early check builds the markers the
// same way the in-memory fence does. A missing row, a nil store, and an
// empty userID carry no marker; any other read error is returned.
// spec: §12.8 (Lifecycle fence and drain).
func StoreMarkers(tenants TenantGetter, users UserGetter) MarkerReader {
	return func(ctx context.Context, tenantID, userID string) (CreationMarkers, error) {
		var m CreationMarkers
		if tenants != nil {
			t, err := tenants.Get(ctx, tenantID)
			switch {
			case errors.Is(err, tenantstore.ErrNotFound):
			case err != nil:
				return m, fmt.Errorf("creation fence: read tenant %s: %w", tenantID, err)
			default:
				m.TenantFound, m.TenantSuspended, m.TenantState = true, t.Suspended, t.State
			}
		}
		if userID == "" || users == nil {
			return m, nil
		}
		u, err := users.Get(ctx, tenantID, userID)
		switch {
		case errors.Is(err, userstore.ErrNotFound):
		case err != nil:
			return m, fmt.Errorf("creation fence: read user %s/%s: %w", tenantID, userID, err)
		default:
			m.UserFound = true
			m.UserDisabled, m.UserDeleted = u.Disabled, !u.DeletedAt.IsZero()
			m.ProcessingRestricted, m.ErasureJobID = u.ProcessingRestricted, u.ErasureJobID
		}
		return m, nil
	}
}
