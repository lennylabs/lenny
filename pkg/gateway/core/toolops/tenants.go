// SPDX-License-Identifier: MIT

package toolops

import (
	"context"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/environment/environmentmw"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/tenancy/envaccess"
	"github.com/lennylabs/lenny/pkg/tenancy/environmentstore"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
	"github.com/lennylabs/lenny/pkg/tenancy/userstore"
)

// Tenants serves seam.TenantFacts and seam.TenantDirectory over the tenant
// store, the user store the creation fence also reads, and the tenant
// listing the gateway's background workers read.
type Tenants struct {
	tenants tenantstore.Store
	users   userstore.Store
	list    func(context.Context) ([]string, error)
}

var (
	_ seam.TenantFacts     = Tenants{}
	_ seam.TenantDirectory = Tenants{}
)

// NewTenants returns the tenant operations over tenants and users, either
// of which may be nil, and list, the listing of the tenants the sweeps
// visit.
func NewTenants(tenants tenantstore.Store, users userstore.Store, list func(context.Context) ([]string, error)) Tenants {
	return Tenants{tenants: tenants, users: users, list: list}
}

// TenantElicitationIntegrity returns the tenant's stored elicitation
// content integrity mode. With no tenant store it returns the empty mode.
// spec: GWSEAM-TenantElicitationIntegrity
func (t Tenants) TenantElicitationIntegrity(ctx context.Context, tenantID string) (string, error) {
	if t.tenants == nil {
		return "", nil
	}
	row, err := t.tenants.Get(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return row.ElicitationContentIntegrity, nil
}

// TenantCredentialProviders returns the providers the tenant's credential
// policy names. With no tenant store it returns none.
// spec: GWSEAM-TenantCredentialProviders
func (t Tenants) TenantCredentialProviders(ctx context.Context, tenantID string) ([]string, error) {
	if t.tenants == nil {
		return nil, nil
	}
	row, err := t.tenants.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return row.CredentialPolicy.Providers(), nil
}

// DelegatorRefusal reads the creation markers of the user and the tenant
// and returns the refusal the creation fence answers for a delegated child.
// A missing row carries no marker, and an unwired store reads nothing.
// spec: GWSEAM-DelegatorRefusal
func (t Tenants) DelegatorRefusal(ctx context.Context, tenantID, userID string) (*seam.Refusal, error) {
	var tg sessionstore.TenantGetter
	if t.tenants != nil {
		tg = t.tenants
	}
	var ug sessionstore.UserGetter
	if t.users != nil {
		ug = t.users
	}
	m, err := sessionstore.StoreMarkers(tg, ug)(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	// The early gate reads no parent row and leaves ParentTerminal unset, so
	// the child's insert transaction is the only producer of
	// DELEGATION_PARENT_REVOKED.
	m.IsDelegatedChild = true
	ref := m.Refusal(tenantID, userID)
	if ref == nil {
		return nil, nil
	}
	return &seam.Refusal{Code: ref.Code, Message: ref.Message, Details: ref.Details}, nil
}

// ListTenants returns the tenants the sweeps visit.
// spec: GWSEAM-ListTenants
func (t Tenants) ListTenants(ctx context.Context) ([]string, error) {
	if t.list == nil {
		return nil, nil
	}
	return t.list(ctx)
}

// Environments serves seam.EnvironmentResolver over the environment and
// tenant registries and the platform-wide policy for a caller in no
// environment.
type Environments struct {
	environments  environmentstore.Store
	tenants       tenantstore.Store
	defaultPolicy string
}

var _ seam.EnvironmentResolver = Environments{}

// NewEnvironments returns the environment resolver. With either registry
// nil it resolves nothing, so environment filtering is not in effect.
func NewEnvironments(environments environmentstore.Store, tenants tenantstore.Store, defaultPolicy string) Environments {
	return Environments{environments: environments, tenants: tenants, defaultPolicy: defaultPolicy}
}

// ResolveEnvironments resolves the environment membership of the
// context's principal in its tenant and returns ctx carrying the
// resolution, which environmentmw.FromContext reads. A context with no
// principal, or a resolution that is not in effect, returns ctx unchanged.
// spec: GWSEAM-ResolveEnvironments
func (e Environments) ResolveEnvironments(ctx context.Context) (context.Context, error) {
	principal, ok := authmw.FromContext(ctx)
	if !ok {
		return ctx, nil
	}
	if e.environments == nil || e.tenants == nil {
		return ctx, nil
	}
	caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
	res, err := environmentmw.Resolve(ctx, e.environments, e.tenants, e.defaultPolicy, caller, principal.TenantID)
	if err != nil {
		return ctx, err
	}
	if !res.Configured {
		return ctx, nil
	}
	return environmentmw.WithResolution(ctx, res), nil
}
