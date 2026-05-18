// SPDX-License-Identifier: MIT

// Package data_residency_validator implements the pure-decision logic
// of the lenny-data-residency-validator ValidatingAdmissionWebhook per
// spec §12.8 ("Data residency admission control") and §12.9. The
// webhook is deployed with failurePolicy: Fail and intercepts CREATE
// and UPDATE on the tenant-scoped CRD resources that carry a
// dataResidencyRegion field (SandboxClaim and any Tenant or
// Environment configuration resources).
//
// The §12.8 rule. On each admission request the webhook resolves the
// effective dataResidencyRegion — applying the §12.8 inheritance rules
// — and rejects the resource when the resolved region value is
// non-empty and not declared in the deployment's storage.regions map.
// The rejection message names the offending field and lists the
// declared regions.
//
// Inheritance (§12.8 "Inheritance"). A session inherits its region
// from its environment, which inherits from its tenant unless
// explicitly overridden. An environment must use the same region as
// its tenant or inherit the tenant's value; specifying a different
// region is itself a violation, rejected with REGION_CONSTRAINT_VIOLATED.
//
// Fail-closed. The decision logic never admits a resource it cannot
// validate: an undeclared region, a region-mismatch against an
// inherited region, or — at the webhook-transport layer — a missing
// region configuration all reject. A webhook outage rejects via
// failurePolicy: Fail so an unverifiable region constraint cannot be
// silently admitted (§12.8: "if the webhook is unavailable, admission
// is denied").
//
// The decision logic is split from the webhook HTTP/AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack.
package data_residency_validator

import (
	"fmt"
	"sort"
	"strings"
)

// Error codes stamped on a denied admission, per §12.8.
const (
	// CodeRegionConstraintUnresolvable rejects a resource whose
	// resolved dataResidencyRegion is non-empty but not declared in
	// storage.regions. It mirrors the StorageRouter fail-closed
	// REGION_CONSTRAINT_UNRESOLVABLE behavior at the admission layer.
	CodeRegionConstraintUnresolvable = "REGION_CONSTRAINT_UNRESOLVABLE"

	// CodeRegionConstraintViolated rejects an Environment-scoped
	// resource that names a region different from its tenant's region.
	// §12.8 "Inheritance": an environment must inherit or match the
	// tenant region; a divergent value is REGION_CONSTRAINT_VIOLATED.
	CodeRegionConstraintViolated = "REGION_CONSTRAINT_VIOLATED"
)

// Request is the input to Decide. It carries the resolved region
// configuration of the deployment plus the residency-relevant fields
// of the admitted resource.
type Request struct {
	// Kind is the admitted resource kind, used in rejection messages
	// (for example "SandboxClaim", "Tenant", "Environment").
	Kind string

	// Field is the JSON path of the dataResidencyRegion field on the
	// admitted resource, used verbatim in the rejection message so the
	// offending field is identified (§12.8). Defaults to
	// "spec.dataResidencyRegion" when empty.
	Field string

	// Region is the dataResidencyRegion declared directly on the
	// admitted resource. Empty when the resource declares none.
	Region string

	// TenantRegion is the dataResidencyRegion of the tenant the
	// resource belongs to, resolved by the webhook before calling
	// Decide. It is the inherited region for an Environment-scoped or
	// session-scoped resource. Empty when the tenant declares no
	// region or the resource is itself a Tenant.
	TenantRegion string

	// IsEnvironmentScoped marks a resource whose region must match or
	// inherit its tenant's region (§12.8 "Inheritance"). When true and
	// Region is non-empty, Region must equal TenantRegion. A Tenant
	// resource sets this false: a tenant has no parent to inherit from.
	IsEnvironmentScoped bool

	// DeclaredRegions is the set of regions declared in the
	// deployment's storage.regions Helm map. A resolved region absent
	// from this set fails closed.
	DeclaredRegions map[string]struct{}
}

// Decision is the admission outcome.
type Decision struct {
	// Allowed is true when the resource's resolved dataResidencyRegion
	// is permitted under the deployment's region configuration.
	Allowed bool

	// Reason is the rejection message relayed to the offending client
	// when Allowed is false; empty when Allowed is true.
	Reason string

	// Code mirrors the HTTP status the webhook surfaces: 200 on allow,
	// 403 on rejection.
	Code int
}

// EffectiveRegion is the dataResidencyRegion the resource is admitted
// under: the resource's own value when set, otherwise the inherited
// tenant value. Empty when neither is set — an unconstrained resource.
func (r Request) EffectiveRegion() string {
	if r.Region != "" {
		return r.Region
	}
	return r.TenantRegion
}

// Decide applies the §12.8 data-residency admission rule.
//
// It admits a resource that declares no region and inherits none — an
// unconstrained resource imposes no residency requirement. For an
// Environment-scoped resource that declares a region differing from
// its tenant's region it rejects with REGION_CONSTRAINT_VIOLATED
// (§12.8 inheritance). Otherwise it resolves the effective region and
// rejects with REGION_CONSTRAINT_UNRESOLVABLE when that region is not
// declared in storage.regions. The check is fail-closed: any resolved
// region the deployment cannot place is rejected, never admitted.
func Decide(r Request) Decision {
	// §12.8 inheritance: an environment-scoped resource may inherit the
	// tenant region or restate it, but never diverge from it. This
	// check runs before the storage.regions lookup so a divergent
	// environment region is reported as a constraint violation rather
	// than as an unresolvable region.
	if r.IsEnvironmentScoped && r.Region != "" && r.TenantRegion != "" &&
		r.Region != r.TenantRegion {
		return reject(CodeRegionConstraintViolated, r.Kind, r.fieldName(),
			fmt.Sprintf("declares dataResidencyRegion %q but its tenant is pinned to %q; "+
				"an environment must inherit its tenant's region or restate the same value",
				r.Region, r.TenantRegion))
	}

	region := r.EffectiveRegion()
	if region == "" {
		// No region declared and none inherited: the resource carries
		// no residency constraint and is admitted unchanged (§12.8:
		// "When dataResidencyRegion is unset, the platform uses the
		// default backend — no behavioral change").
		return Decision{Allowed: true, Code: 200}
	}

	if _, ok := r.DeclaredRegions[region]; !ok {
		return reject(CodeRegionConstraintUnresolvable, r.Kind, r.fieldName(),
			fmt.Sprintf("resolves dataResidencyRegion %q, which is not declared in "+
				"storage.regions. Declared regions: %s. Add a storage.regions.%s entry "+
				"or correct the region value", region, declaredList(r.DeclaredRegions), region))
	}
	return Decision{Allowed: true, Code: 200}
}

// fieldName returns the offending-field path for a rejection message,
// defaulting to spec.dataResidencyRegion when the caller left it empty.
func (r Request) fieldName() string {
	if r.Field == "" {
		return "spec.dataResidencyRegion"
	}
	return r.Field
}

// declaredList renders the declared-region set as a stable,
// comma-separated list for the rejection message. An empty set renders
// as "(none)" so the operator sees that storage.regions is unset.
func declaredList(regions map[string]struct{}) string {
	if len(regions) == 0 {
		return "(none)"
	}
	out := make([]string, 0, len(regions))
	for r := range regions {
		out = append(out, r)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// reject builds a denied Decision carrying the §12.8 error code, the
// resource kind, the offending field, and the remediation message.
func reject(code, kind, field, msg string) Decision {
	if kind == "" {
		kind = "resource"
	}
	return Decision{
		Allowed: false,
		Code:    403,
		Reason:  fmt.Sprintf("%s: %s field %s %s", code, kind, field, msg),
	}
}
