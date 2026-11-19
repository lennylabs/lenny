// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// TenantElicitationModes returns the Deps.ElicitationModeResolver of a
// gateway: the §9.2 effective elicitation content-integrity enforcement
// mode of a tenant, the maximum of the platform floor that floor returns
// and the tenant's stored mode, which the session core reads
// (GWSEAM-TenantElicitationIntegrity). A failed read, or no tenant facts,
// resolves as a tenant that stored no mode, so the floor and the §9.2
// enforce default apply.
// spec: §9.2. F-9.2.2.
func TenantElicitationModes(floor func() string, tenants seam.TenantFacts) func(context.Context, string) elicitation.EnforcementMode {
	return func(ctx context.Context, tenantID string) elicitation.EnforcementMode {
		stored := ""
		if tenants != nil {
			if mode, err := tenants.TenantElicitationIntegrity(ctx, tenantID); err == nil {
				stored = mode
			}
		}
		return elicitation.ResolveEffectiveWithDefaults(floor(), stored)
	}
}
