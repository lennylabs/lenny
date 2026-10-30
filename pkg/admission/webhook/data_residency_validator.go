// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"

	drv "github.com/lennylabs/lenny/pkg/admission/data_residency_validator"
)

// residencyObject is the minimal projection the data-residency
// validator decodes from any admitted resource: the dataResidencyRegion
// field (read from either spec or the object root) and the tenant the
// resource belongs to. Every tenant-scoped CRD the §12.8 webhook
// covers — SandboxClaim and any Tenant or Environment configuration
// resource — exposes these under one of the JSON paths probed below.
type residencyObject struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		DataResidencyRegion string `json:"dataResidencyRegion"`
		TenantID            string `json:"tenantId"`
		EnvironmentRef      string `json:"environmentRef"`
	} `json:"spec"`
	// DataResidencyRegion at the object root covers resources that
	// carry the field outside a spec block.
	DataResidencyRegion string `json:"dataResidencyRegion"`
}

// region returns the dataResidencyRegion declared on the resource,
// preferring the spec field and falling back to the object root.
func (o residencyObject) region() string {
	if o.Spec.DataResidencyRegion != "" {
		return o.Spec.DataResidencyRegion
	}
	return o.DataResidencyRegion
}

// TenantRegionResolver resolves the dataResidencyRegion pinned on a
// tenant. The data-residency webhook calls it to apply the §12.8
// inheritance rule when an environment-scoped resource carries a
// tenant reference: the resource's effective region is its own value
// when set, otherwise the tenant's. A resolver that cannot resolve the
// tenant returns an error, which the webhook treats as fail-closed.
type TenantRegionResolver interface {
	// TenantRegion returns the dataResidencyRegion of the named
	// tenant, or "" when the tenant pins no region.
	TenantRegion(ctx context.Context, tenantID string) (string, error)
}

// environmentScopedKinds is the set of resource kinds the §12.8
// inheritance rule treats as environment-scoped: their region must
// match or inherit their tenant's region. A Tenant resource is not in
// the set — a tenant has no parent to inherit from.
var environmentScopedKinds = map[string]struct{}{
	"Environment":  {},
	"SandboxClaim": {},
}

// DataResidencyValidator returns the Decider for the
// lenny-data-residency-validator ValidatingAdmissionWebhook (§12.8,
// §12.9). It is installed fail-closed on the tenant-scoped CRD
// resources that carry a dataResidencyRegion field and rejects any
// resource whose resolved region is not declared in the deployment's
// storage.regions map, or — for an environment-scoped resource — whose
// region diverges from its tenant's region.
//
// declaredRegions is the deployment's storage.regions key set, sourced
// from the Helm values at binary startup. resolver supplies the §12.8
// inheritance lookup: when a resource is environment-scoped and names
// a tenant, resolver yields the tenant's region. A nil resolver
// disables inheritance — a resource is then judged on its own declared
// region only, which is the correct behavior for a single-region
// deployment or for a Tenant resource.
//
// A decode failure or a resolver error rejects, consistent with the
// webhook's fail-closed deployment: a region constraint the webhook
// cannot evaluate must not be admitted (§12.8).
func DataResidencyValidator(declaredRegions []string, resolver TenantRegionResolver) Decider {
	declared := make(map[string]struct{}, len(declaredRegions))
	for _, r := range declaredRegions {
		if r != "" {
			declared[r] = struct{}{}
		}
	}
	return func(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var obj residencyObject
		if err := json.Unmarshal(req.Object.Raw, &obj); err != nil {
			return Deny(http.StatusBadRequest, "decode admitted resource: "+err.Error())
		}

		decReq := drv.Request{
			Kind:            req.Kind.Kind,
			Region:          obj.region(),
			DeclaredRegions: declared,
		}

		_, decReq.IsEnvironmentScoped = environmentScopedKinds[req.Kind.Kind]
		// §12.8 inheritance: resolve the parent tenant's region so an
		// environment-scoped resource that declares no region inherits
		// it, and one that declares a divergent region is rejected.
		if decReq.IsEnvironmentScoped && resolver != nil && obj.Spec.TenantID != "" {
			region, err := resolver.TenantRegion(ctx, obj.Spec.TenantID)
			if err != nil {
				return Deny(http.StatusInternalServerError,
					"resolve tenant dataResidencyRegion for "+obj.Spec.TenantID+": "+err.Error())
			}
			decReq.TenantRegion = region
		}

		decision := drv.Decide(decReq)
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}
