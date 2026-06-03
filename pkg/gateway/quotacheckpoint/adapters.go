// SPDX-License-Identifier: MIT

package quotacheckpoint

import (
	"context"

	sessionapi "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// *quotastore.Counter satisfies the read and restore seams directly.
var (
	_ WindowReader    = (*quotastore.Counter)(nil)
	_ CounterRestorer = (*quotastore.Counter)(nil)
)

// TenantLister enumerates the tenant ids whose active sessions are
// scanned for checkpoint subjects. cmd/lenny-gateway wires the same
// tenantstore-backed lister it uses for the delegation-budget reconciler.
type TenantLister func(ctx context.Context) ([]string, error)

// SessionSubjectLister derives the active (tenant, user) checkpoint
// subjects from the SessionStore: it iterates the tenants and collects the
// distinct (tenant, user) pair of every non-terminal session. A session
// with no user id is skipped (it contributes no per-user window). The
// SessionStore is Postgres-authoritative, so the subject set survives a
// gateway replica loss.
type SessionSubjectLister struct {
	Sessions sessionstore.Store
	Tenants  TenantLister
}

// ListActiveSubjects implements SubjectLister.
func (l SessionSubjectLister) ListActiveSubjects(ctx context.Context) ([]Subject, error) {
	ids, err := l.Tenants(ctx)
	if err != nil {
		return nil, err
	}
	var subjects []Subject
	seen := make(map[string]struct{})
	for _, tenantID := range ids {
		rows, err := l.Sessions.List(ctx, tenantID, sessionstore.ListFilter{})
		if err != nil {
			return nil, err
		}
		for _, s := range rows {
			if sessionapi.IsTerminal(s.State) || s.UserID == "" {
				continue
			}
			key := tenantID + "\x00" + s.UserID
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			subjects = append(subjects, Subject{TenantID: tenantID, UserID: s.UserID})
		}
	}
	return subjects, nil
}

var _ SubjectLister = SessionSubjectLister{}

// PeriodResolverFunc adapts a closure to PeriodResolver. cmd/lenny-gateway
// wires it to the §11.2 tenant-limits lookup so the checkpoint reads the
// same reset period QuotaEvaluator enforces.
type PeriodResolverFunc func(ctx context.Context, tenantID string) (quota.ResetPeriod, error)

// ResolvePeriod implements PeriodResolver.
func (f PeriodResolverFunc) ResolvePeriod(ctx context.Context, tenantID string) (quota.ResetPeriod, error) {
	return f(ctx, tenantID)
}

var _ PeriodResolver = PeriodResolverFunc(nil)

// TenantExistsFunc adapts a closure to TenantExister.
type TenantExistsFunc func(ctx context.Context, tenantID string) (bool, error)

// TenantExists implements TenantExister.
func (f TenantExistsFunc) TenantExists(ctx context.Context, tenantID string) (bool, error) {
	return f(ctx, tenantID)
}

var _ TenantExister = TenantExistsFunc(nil)
