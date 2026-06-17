// SPDX-License-Identifier: MIT

package admin

import (
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// upsertPools applies the §17.6 line 408 bootstrap.pools seed list. It
// holds each seed entry to the same validation the POST /v1/admin/pools
// handler runs (name, enum, runtimeRef resolution, §6.1 SDK-warm
// execution-mode rule, §5.2 line 396 T4 cross-tenant-reuse rule) and
// then applies the §17.6 line 444 upsert semantics keyed by pool name.
// isolationProfile is treated as a §17.6 line 451 security-critical field
// (an isolation downgrade is blocked regardless of --force-update).
//
// spec: §17.6 lines 408, 429 (a Pool is required for any session-creating
// deployment); §5.2; §5.3.
func (r *Router) upsertPools(req *http.Request, in []PoolPayload, opts bootstrapOptions) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if err := poolstore.ValidateName(p.Name); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := p.validateEnums(); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		// Capture the runtime's §12.9 tier and §6.1 preConnect posture so
		// the cross-resource invariants below can be checked, mirroring
		// handleCreatePool.
		var runtimeTier runtimestore.WorkspaceTier
		var runtimePreConnect bool
		if r.runtimes != nil && p.RuntimeRef != "" {
			rt, err := r.runtimes.Get(req.Context(), p.RuntimeRef)
			if err != nil {
				out.add(i, p.Name, actionError, seedValidationCode,
					"runtimeRef does not resolve to a registered runtime", nil)
				continue
			}
			runtimeTier = rt.WorkspaceTier
			runtimePreConnect = poolstore.RuntimePreConnect(rt)
		}
		pl := r.poolFromPayload(p)
		// spec: §5.2 line 430, §6.1 lines 77-78 — an SDK-warm runtime cannot
		// back a service-mode pool or a concurrent session-mode pool.
		if err := poolstore.ValidatePreConnectExecutionMode(runtimePreConnect, pl.ExecutionMode, pl.SessionPolicy); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		// spec: §5.2 line 396 — cross-tenant reuse is rejected on a pool
		// whose runtime is T4.
		if err := poolstore.ValidateCrossTenantReuseTier(pl, runtimeTier); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		existing, err := r.pools.Get(req.Context(), p.Name)
		if errors.Is(err, poolstore.ErrNotFound) {
			if !opts.dryRun {
				if err := r.pools.Create(req.Context(), pl); err != nil {
					out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
					continue
				}
			}
			out.add(i, p.Name, actionCreated, "", "", nil)
			continue
		}
		if err != nil {
			out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
			continue
		}
		conflicts, securityCritical := poolConflicts(existing, p)
		action, code, doUpdate := resolveExisting(conflicts, securityCritical, opts.forceUpdate)
		if action == actionError && code == seedSecurityCriticalCode {
			out.add(i, p.Name, actionError, seedSecurityCriticalCode,
				"pool "+p.Name+": seed would overwrite security-critical field(s) "+
					joinFields(securityCritical)+"; blocked regardless of --force-update", nil)
			continue
		}
		if doUpdate && !opts.dryRun {
			desired := pl
			if _, err = r.pools.Update(req.Context(), existing.Name, func(stored *poolstore.Pool) error {
				applyPoolSeed(stored, desired)
				return nil
			}); err != nil {
				out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
				continue
			}
		}
		out.add(i, p.Name, action, code, "", conflicts)
	}
	return out
}

// applyPoolSeed overwrites the mutable fields of an existing pool with
// the §17.6 line 450 force-update PUT (a full replace of the configurable
// scalars). Name and CreatedAt are preserved; UpdatedAt advances to the
// desired record's stamp.
func applyPoolSeed(stored *poolstore.Pool, desired poolstore.Pool) {
	stored.RuntimeRef = desired.RuntimeRef
	stored.IsolationProfile = desired.IsolationProfile
	stored.ExecutionMode = desired.ExecutionMode
	stored.MaxConcurrent = desired.MaxConcurrent
	stored.SessionPolicy = desired.SessionPolicy
	stored.ResourceClass = desired.ResourceClass
	stored.WarmCount = desired.WarmCount
	stored.MaxSessionAgeSeconds = desired.MaxSessionAgeSeconds
	stored.AllowStandardIsolation = desired.AllowStandardIsolation
	stored.EgressProfile = desired.EgressProfile
	stored.UpdatedAt = desired.UpdatedAt
}

// poolConflicts returns the seed-specified pool fields whose values
// differ from the stored row (§17.6 line 450), and separately the subset
// that is security-critical (§17.6 line 451 — isolationProfile). Only
// fields the seed sets (non-zero) are compared so an omitted field is not
// treated as a conflict against a defaulted stored value. The stored row
// already carries resolved defaults, so the comparison resolves the seed
// payload's defaults too before comparing.
func poolConflicts(existing poolstore.Pool, p PoolPayload) (conflicts, securityCritical []string) {
	if p.RuntimeRef != "" && existing.RuntimeRef != p.RuntimeRef {
		conflicts = append(conflicts, "runtimeRef")
	}
	if p.IsolationProfile != "" && string(existing.IsolationProfile) != p.IsolationProfile {
		conflicts = append(conflicts, "isolationProfile")
		securityCritical = append(securityCritical, "isolationProfile")
	}
	if p.ExecutionMode != "" && string(existing.ExecutionMode) != p.ExecutionMode {
		conflicts = append(conflicts, "executionMode")
	}
	if p.ResourceClass != "" && existing.ResourceClass != p.ResourceClass {
		conflicts = append(conflicts, "resourceClass")
	}
	if p.WarmCount != 0 && existing.WarmCount != p.WarmCount {
		conflicts = append(conflicts, "warmCount")
	}
	if p.MaxSessionAgeSeconds != 0 && existing.MaxSessionAgeSeconds != p.MaxSessionAgeSeconds {
		conflicts = append(conflicts, "maxSessionAgeSeconds")
	}
	if p.MaxConcurrent != 0 && existing.MaxConcurrent != p.MaxConcurrent {
		conflicts = append(conflicts, "maxConcurrent")
	}
	if p.EgressProfile != "" && string(existing.EgressProfile) != p.EgressProfile {
		conflicts = append(conflicts, "egressProfile")
	}
	return conflicts, securityCritical
}

// upsertDelegationPolicies applies the §17.6 line 410
// bootstrap.delegationPolicies seed list. Each entry is keyed by
// (tenantId, name); the tenantId is read directly from the seed payload
// (the bootstrap caller is platform-admin and seeds explicit tenant
// scopes). The §8.3 structural invariants (Validate) are checked before
// any store mutation so a malformed policy is a per-entry validation
// error rather than an operational failure.
//
// spec: §17.6 line 410; §8.3.
func (r *Router) upsertDelegationPolicies(req *http.Request, in []DelegationPolicyPayload, opts bootstrapOptions) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if p.TenantID == "" {
			out.add(i, p.Name, actionError, seedValidationCode, "tenantId is required", nil)
			continue
		}
		desired := delegationpolicystore.DelegationPolicy{
			TenantID:           p.TenantID,
			Name:               p.Name,
			Rules:              toDelegationRules(p.Rules),
			ContentPolicy:      toContentPolicy(p.ContentPolicy),
			AllowSelfRecursion: p.AllowSelfRecursion,
			CreatedAt:          r.clock(),
		}
		desired.UpdatedAt = desired.CreatedAt
		delegationpolicystore.ApplyDefaults(&desired)
		if err := delegationpolicystore.Validate(desired); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		existing, err := r.delegationPolicies.Get(req.Context(), p.TenantID, p.Name)
		if errors.Is(err, delegationpolicystore.ErrNotFound) {
			if !opts.dryRun {
				if err := r.delegationPolicies.Create(req.Context(), desired); err != nil {
					out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
					continue
				}
			}
			out.add(i, p.Name, actionCreated, "", "", nil)
			continue
		}
		if err != nil {
			out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
			continue
		}
		conflicts := delegationPolicyConflicts(existing, desired)
		action, code, doUpdate := resolveExisting(conflicts, nil, opts.forceUpdate)
		if doUpdate && !opts.dryRun {
			if _, err = r.delegationPolicies.Update(req.Context(), p.TenantID, p.Name, func(dp *delegationpolicystore.DelegationPolicy) error {
				dp.Rules = desired.Rules
				dp.ContentPolicy = desired.ContentPolicy
				dp.AllowSelfRecursion = desired.AllowSelfRecursion
				// §17.6 re-runs: a previously soft-deleted policy named in
				// the seed resurfaces as an active record.
				dp.DeletedAt = time.Time{}
				return nil
			}); err != nil {
				out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
				continue
			}
		}
		out.add(i, p.Name, action, code, "", conflicts)
	}
	return out
}

// delegationPolicyConflicts returns the seed-specified delegation-policy
// fields whose values differ from the stored row (§17.6 line 450). The
// force-update path replaces the full mutable body, so rules,
// contentPolicy, and allowSelfRecursion are all compared.
func delegationPolicyConflicts(existing, desired delegationpolicystore.DelegationPolicy) []string {
	var c []string
	if !reflect.DeepEqual(existing.Rules, desired.Rules) {
		c = append(c, "rules")
	}
	if existing.ContentPolicy != desired.ContentPolicy {
		c = append(c, "contentPolicy")
	}
	if existing.AllowSelfRecursion != desired.AllowSelfRecursion {
		c = append(c, "allowSelfRecursion")
	}
	return c
}

// upsertEnvironments applies the §17.6 line 411 bootstrap.environments
// seed list (the §17.6 line 438 Option B access path). Each entry is
// keyed by (tenantId, name); tenantId is read directly from the seed
// payload. The §11.7 line 449 SIEM-required gate and the §12.9 tier
// override invariant are enforced before any store mutation so a seed
// under a regulated tenant with no SIEM endpoint, or a tier-loosening
// override, is a per-entry error rather than a silent admission.
//
// spec: §17.6 line 411, 438; §10.6; §11.7 line 449; §12.9.
func (r *Router) upsertEnvironments(req *http.Request, in []EnvironmentPayload, opts bootstrapOptions) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		tenant := p.TenantID
		if tenant == "" {
			out.add(i, p.Name, actionError, seedValidationCode, "tenantId is required", nil)
			continue
		}
		// spec: §11.7 line 449 — an environment under a regulated tenant
		// requires a configured SIEM endpoint.
		if r.tenants != nil {
			if row, gErr := r.tenants.Get(req.Context(), tenant); gErr == nil && r.requireSIEMForProfile(row.ComplianceProfile) {
				out.add(i, p.Name, actionError, seedValidationCode,
					complianceSIEMRequiredMessage(row.ComplianceProfile), nil)
				continue
			}
			// spec: §11.7 line 377 — and pgaudit DDL/ROLE capture configured.
			if row, gErr := r.tenants.Get(req.Context(), tenant); gErr == nil && r.requirePgauditForProfile(row.ComplianceProfile) {
				out.add(i, p.Name, actionError, seedValidationCode,
					compliancePgauditRequiredMessage(row.ComplianceProfile), nil)
				continue
			}
		}
		// spec: §12.9 — the environment tier override may only tighten the
		// tenant's tier.
		if err := r.environmentTierOverrideError(req.Context(), tenant, p.WorkspaceTier); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		desired := toEnvironment(p, tenant)
		if err := desired.Validate(); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		existing, err := r.environments.Get(req.Context(), tenant, p.Name)
		if errors.Is(err, environmentstore.ErrNotFound) {
			if !opts.dryRun {
				if err := r.environments.Create(req.Context(), desired); err != nil {
					out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
					continue
				}
			}
			out.add(i, p.Name, actionCreated, "", "", nil)
			continue
		}
		if err != nil {
			out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
			continue
		}
		conflicts := environmentConflicts(existing, desired)
		action, code, doUpdate := resolveExisting(conflicts, nil, opts.forceUpdate)
		if doUpdate && !opts.dryRun {
			if _, err = r.environments.Update(req.Context(), tenant, p.Name, func(e *environmentstore.Environment) error {
				desired.Name = e.Name
				desired.TenantID = e.TenantID
				desired.CreatedAt = e.CreatedAt
				*e = desired
				return nil
			}); err != nil {
				out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
				continue
			}
		}
		out.add(i, p.Name, action, code, "", conflicts)
	}
	return out
}

// environmentConflicts reports whether the seed environment differs from
// the stored row in any mutable field (§17.6 line 450). Environments are
// a full-replace resource, so the comparison ignores only the immutable
// identity (name, tenantId) and the audit timestamps.
func environmentConflicts(existing, desired environmentstore.Environment) []string {
	a := existing
	b := desired
	a.CreatedAt, a.UpdatedAt = b.CreatedAt, b.UpdatedAt
	a.Name, a.TenantID = b.Name, b.TenantID
	if reflect.DeepEqual(a, b) {
		return nil
	}
	return []string{"environment"}
}
