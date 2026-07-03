// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Bootstrap upsert outcome codes surfaced on the §15.1 wire.
const (
	// seedConflictCode is the §15.1 line 1007 SEED_CONFLICT code. It is
	// attached to a skipped entry (not a failure) whose seed fields differ
	// from the stored resource and `--force-update` was not supplied. The
	// §17.6 line 450 upsert table makes this case a skip (exit 0), so the
	// code rides on the per-entry Skipped record rather than Errors.
	seedConflictCode = "SEED_CONFLICT"

	// seedSecurityCriticalCode marks a §17.6 line 451 security-critical
	// field overwrite (e.g. runtime isolationProfile). The operation is
	// blocked regardless of `--force-update` and the run exits 1.
	seedSecurityCriticalCode = "SEED_SECURITY_CRITICAL_FIELD"

	// seedValidationCode marks a per-entry validation/policy rejection
	// raised before any store mutation (exit 1 class).
	seedValidationCode = "SEED_VALIDATION"

	// seedStoreErrorCode marks a per-entry operational failure from a
	// store Create/Update call (exit 2 partial-failure class).
	seedStoreErrorCode = "SEED_STORE_ERROR"
)

// Bootstrap per-entry action labels, per the §15.1 line 863 audit summary
// (`created`/`updated`/`skipped`/`error`).
const (
	actionCreated = "created"
	actionUpdated = "updated"
	actionSkipped = "skipped"
	actionError   = "error"
)

// BootstrapRequest is the §24.1 bootstrap seed payload accepted by
// POST /v1/admin/bootstrap. The handler applies upsert semantics
// per §17.6 line 444: each entry creates a new row, leaves an existing
// row unchanged (skip), updates it under `--force-update`, or is
// rejected. Operations are best-effort — the handler returns an
// aggregate result with per-section counts and per-entry outcomes.
type BootstrapRequest struct {
	Tenants            []TenantPayload           `json:"tenants,omitempty"`
	Runtimes           []RuntimePayload          `json:"runtimes,omitempty"`
	Users              []UserPayload             `json:"users,omitempty"`
	Pools              []PoolPayload             `json:"pools,omitempty"`
	CredentialPools    []CredentialPoolPayload   `json:"credentialPools,omitempty"`
	DelegationPolicies []DelegationPolicyPayload `json:"delegationPolicies,omitempty"`
	Environments       []EnvironmentPayload      `json:"environments,omitempty"`
}

// bootstrapOptions carries the per-request §17.6 upsert modifiers parsed
// from the query string: `?dryRun=true` (§15.1 line 1140) and
// `?forceUpdate=true` (§17.6 line 450, the server-side projection of the
// `--force-update` CLI flag).
type bootstrapOptions struct {
	dryRun      bool
	forceUpdate bool
}

// BootstrapResponse is the response envelope. CreatedCount tracks
// rows the handler inserted; UpdatedCount tracks rows replaced under
// `--force-update`; SkippedCount tracks rows left unchanged (identical
// or differing-without-force); Errors carries per-entry failures (the
// handler does NOT stop on the first error — §17.6 line 420 partial
// failure). Results is the ordered per-entry action summary.
type BootstrapResponse struct {
	Tenants            BootstrapSection `json:"tenants,omitempty"`
	Runtimes           BootstrapSection `json:"runtimes,omitempty"`
	Users              BootstrapSection `json:"users,omitempty"`
	Pools              BootstrapSection `json:"pools,omitempty"`
	CredentialPools    BootstrapSection `json:"credentialPools,omitempty"`
	DelegationPolicies BootstrapSection `json:"delegationPolicies,omitempty"`
	Environments       BootstrapSection `json:"environments,omitempty"`
	// AdminToken reports the §17.6 initial-admin-credential outcome so the
	// §24.1 CLI can print the first-use prompt. Populated only when the
	// gateway has an admin-token provisioner wired and the run is not a
	// dry-run. F-17.6.3 / F-24.1.7.
	AdminToken *AdminTokenSection `json:"adminToken,omitempty"`
}

// BootstrapSection is the per-resource result.
type BootstrapSection struct {
	CreatedCount int               `json:"createdCount"`
	UpdatedCount int               `json:"updatedCount"`
	SkippedCount int               `json:"skippedCount"`
	Errors       []BootstrapError  `json:"errors,omitempty"`
	Skipped      []BootstrapSkip   `json:"skipped,omitempty"`
	Applied      []string          `json:"applied,omitempty"`
	Results      []BootstrapResult `json:"results,omitempty"`
}

// BootstrapError captures a single per-entry failure.
type BootstrapError struct {
	Index   int    `json:"index"`
	ID      string `json:"id,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// BootstrapSkip captures a single per-entry skip whose seed fields differ
// from the stored resource (§17.6 line 450). It carries the §15.1 line
// 1007 SEED_CONFLICT code and the differing field names so an operator
// (or the CLI WARN log) can see why the resource was left unchanged.
type BootstrapSkip struct {
	Index             int      `json:"index"`
	ID                string   `json:"id,omitempty"`
	Code              string   `json:"code"`
	ConflictingFields []string `json:"conflictingFields,omitempty"`
}

// BootstrapResult is one entry's resolved action for the §15.1 line 863
// per-resource audit summary (`{type, name, action}`).
type BootstrapResult struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// add records a per-entry outcome on the section, keeping Results,
// counts, Applied, Errors, and Skipped consistent.
func (s *BootstrapSection) add(index int, id, action, code, message string, conflicts []string) {
	s.Results = append(s.Results, BootstrapResult{Name: id, Action: action})
	switch action {
	case actionCreated:
		s.CreatedCount++
		s.Applied = append(s.Applied, id)
	case actionUpdated:
		s.UpdatedCount++
		s.Applied = append(s.Applied, id)
	case actionSkipped:
		s.SkippedCount++
		if len(conflicts) > 0 || code != "" {
			s.Skipped = append(s.Skipped, BootstrapSkip{Index: index, ID: id, Code: code, ConflictingFields: conflicts})
		}
	case actionError:
		s.Errors = append(s.Errors, BootstrapError{Index: index, ID: id, Code: code, Message: message})
	}
}

// handleBootstrap implements POST /v1/admin/bootstrap.
//
// spec: §24.1; §15.1 lines 863, 1140; §17.6 lines 417-453.
func (r *Router) handleBootstrap(w http.ResponseWriter, req *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(req.Body, 8<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body could not be read", nil)
		return
	}
	var body BootstrapRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}

	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}

	// §15.1 line 1140 — `?dryRun=true` performs full validation but does
	// not persist; §17.6 line 450 — `?forceUpdate=true` is the server-side
	// projection of the `--force-update` CLI flag.
	opts := bootstrapOptions{
		dryRun:      req.URL.Query().Get("dryRun") == "true",
		forceUpdate: req.URL.Query().Get("forceUpdate") == "true",
	}

	// Seed in dependency order so a single bootstrap run can stand up a
	// functional deployment: tenants first, then runtimes, then pools
	// (which reference a runtimeRef), credential pools, delegation
	// policies, and environments (which reference a defaultDelegationPolicy
	// and a tenant). This mirrors the §17.6 lines 403-411 values block.
	out := BootstrapResponse{}
	if r.tenants != nil {
		out.Tenants = r.upsertTenants(req, body.Tenants, opts)
	}
	if r.runtimes != nil {
		out.Runtimes = r.upsertRuntimes(req, body.Runtimes, opts)
	}
	if r.users != nil {
		out.Users = r.upsertUsers(req, body.Users, opts)
	}
	if r.pools != nil {
		out.Pools = r.upsertPools(req, body.Pools, opts)
	}
	if r.credentialPools != nil {
		out.CredentialPools = r.upsertCredentialPools(req, body.CredentialPools, opts)
	}
	if r.delegationPolicies != nil {
		out.DelegationPolicies = r.upsertDelegationPolicies(req, body.DelegationPolicies, opts)
	}
	if r.environments != nil {
		out.Environments = r.upsertEnvironments(req, body.Environments, opts)
	}

	// §17.6 lines 455-474 — provision the initial admin credential after
	// the seed resources stand (the lenny-admin user row the credential
	// references is upserted by the provisioner itself). Skipped on a
	// dry-run so no token is minted or Secret written. F-17.6.3 / F-24.1.7.
	if section, ok := r.provisionAdminToken(req.Context(), opts.dryRun); ok {
		out.AdminToken = &section
	}

	// §15.1 line 863 — the `platform.bootstrap_applied` audit event (T3)
	// records the calling identity (actor columns), the seed-file SHA-256,
	// the per-resource `{type, name, action}` summary, and `dryRun`. Per
	// §15.1 line 1140 the event is emitted even when `?dryRun=true` so
	// operators have a record of what a bootstrap run would have changed.
	seedHash := sha256.Sum256(raw)
	r.emit(req.Context(), principal, "platform.bootstrap_applied", "platform", map[string]any{
		"dryRun":     opts.dryRun,
		"seedSha256": hex.EncodeToString(seedHash[:]),
		"resources":  bootstrapResourceSummary(out),
		// Retain the per-section counts for at-a-glance forensic reads.
		"tenants":            bootstrapSectionAuditPayload(out.Tenants),
		"runtimes":           bootstrapSectionAuditPayload(out.Runtimes),
		"users":              bootstrapSectionAuditPayload(out.Users),
		"pools":              bootstrapSectionAuditPayload(out.Pools),
		"credentialPools":    bootstrapSectionAuditPayload(out.CredentialPools),
		"delegationPolicies": bootstrapSectionAuditPayload(out.DelegationPolicies),
		"environments":       bootstrapSectionAuditPayload(out.Environments),
	})

	if opts.dryRun {
		// §15.1 line 1140 — the response body is identical to a non-dry-run
		// success; the only addition is the X-Dry-Run header.
		w.Header().Set("X-Dry-Run", "true")
	}

	status := http.StatusOK
	if anyFailures(out) {
		// 207 Multi-Status signals partial failure so curl/CI pipelines
		// (and the lenny-ctl exit-code mapping, §17.6 line 420) fail fast
		// while the body still carries the per-entry results.
		status = http.StatusMultiStatus
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}

// bootstrapResourceSummary flattens every section's per-entry results
// into the §15.1 line 863 `{type, name, action}` summary list.
func bootstrapResourceSummary(out BootstrapResponse) []map[string]any {
	rows := make([]map[string]any, 0)
	appendSection := func(kind string, s BootstrapSection) {
		for _, res := range s.Results {
			rows = append(rows, map[string]any{
				"type":   kind,
				"name":   res.Name,
				"action": res.Action,
			})
		}
	}
	appendSection("tenant", out.Tenants)
	appendSection("runtime", out.Runtimes)
	appendSection("user", out.Users)
	appendSection("pool", out.Pools)
	appendSection("credentialPool", out.CredentialPools)
	appendSection("delegationPolicy", out.DelegationPolicies)
	appendSection("environment", out.Environments)
	return rows
}

// bootstrapSectionAuditPayload builds the per-section audit payload for
// one BootstrapSection: counts plus per-entry `{name, action, message}`
// error rows so a forensic reader can answer "which seed entry failed"
// from the audit chain alone.
func bootstrapSectionAuditPayload(s BootstrapSection) map[string]any {
	out := map[string]any{
		"created": s.CreatedCount,
		"updated": s.UpdatedCount,
		"skipped": s.SkippedCount,
	}
	if len(s.Errors) == 0 {
		return out
	}
	rows := make([]map[string]any, 0, len(s.Errors))
	for _, e := range s.Errors {
		rows = append(rows, map[string]any{
			"name":    e.ID,
			"action":  actionError,
			"code":    e.Code,
			"message": e.Message,
		})
	}
	out["errors"] = rows
	return out
}

func anyFailures(out BootstrapResponse) bool {
	return len(out.Tenants.Errors) > 0 ||
		len(out.Runtimes.Errors) > 0 ||
		len(out.Users.Errors) > 0 ||
		len(out.Pools.Errors) > 0 ||
		len(out.CredentialPools.Errors) > 0 ||
		len(out.DelegationPolicies.Errors) > 0 ||
		len(out.Environments.Errors) > 0
}

// resolveExisting decides the §17.6 line 444 action when a resource
// already exists. conflicts is the set of seed-specified fields whose
// values differ from the stored row; securityCritical is the subset
// that is security-critical (§17.6 line 451, blocked regardless of
// force-update). The boolean reports whether the caller should persist
// an update.
func resolveExisting(conflicts, securityCritical []string, force bool) (action, code string, doUpdate bool) {
	if len(securityCritical) > 0 {
		return actionError, seedSecurityCriticalCode, false
	}
	if len(conflicts) == 0 {
		// Identical fields — no-op (§17.6 line 449). Recorded as a skip
		// in the audit summary (the resource is left unchanged).
		return actionSkipped, "", false
	}
	if force {
		return actionUpdated, "", true
	}
	// Differing fields without force-update — skip (§17.6 line 450).
	return actionSkipped, seedConflictCode, false
}

// upsertCredentialPools applies the §4.9 bootstrap.credentialPools seed
// list (§17.6 upsert semantics, keyed by (tenantId, name)). It enforces
// the same §4.9 cacheScope contract the admin POST/PUT path enforces:
// `cacheScope: tenant` is rejected for a regulated complianceProfile.
//
// spec: §4.9 lines 1697, 1711 — credential pools are part of the Helm
// bootstrap seed surface, not only the live admin API.
func (r *Router) upsertCredentialPools(req *http.Request, in []CredentialPoolPayload, opts bootstrapOptions) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		tenant := p.TenantID
		if tenant == "" {
			out.add(i, p.Name, actionError, seedValidationCode, "tenantId is required", nil)
			continue
		}
		if !validAssignmentStrategies[p.AssignmentStrategy] {
			out.add(i, p.Name, actionError, seedValidationCode,
				"assignmentStrategy must be least-loaded, round-robin, or sticky-until-failure", nil)
			continue
		}
		if err := r.bootstrapCacheScope(req, tenant, p.CacheScope); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		existing, err := r.credentialPools.Get(req.Context(), tenant, p.Name)
		if errors.Is(err, credentialpoolstore.ErrNotFound) {
			if !opts.dryRun {
				pool := credentialpoolstore.CredentialPool{
					TenantID:                   tenant,
					Name:                       p.Name,
					Provider:                   p.Provider,
					Credentials:                toCredentials(p.Credentials),
					AssignmentStrategy:         p.AssignmentStrategy,
					MaxConcurrentSessions:      p.MaxConcurrentSessions,
					CooldownOnRateLimitSeconds: p.CooldownOnRateLimitSeconds,
					LeaseTTLSeconds:            p.LeaseTTLSeconds,
					RenewBeforeBufferSeconds:   p.RenewBeforeBufferSeconds,
					HostPatterns:               p.HostPatterns,
					DeliveryMode:               p.DeliveryMode,
					ProxyDialect:               p.ProxyDialect,
					ProxyEndpoint:              p.ProxyEndpoint,
					CacheScope:                 p.CacheScope,
					CachePolicy:                toCachePolicy(p.CachePolicy),
					CreatedAt:                  r.clock(),
				}
				pool.UpdatedAt = pool.CreatedAt
				if err := r.credentialPools.Create(req.Context(), pool); err != nil {
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
		conflicts := credentialPoolConflicts(existing, p)
		action, code, doUpdate := resolveExisting(conflicts, nil, opts.forceUpdate)
		if doUpdate && !opts.dryRun {
			_, err = r.credentialPools.Update(req.Context(), tenant, p.Name, func(pool *credentialpoolstore.CredentialPool) error {
				pool.Provider = p.Provider
				pool.Credentials = toCredentials(p.Credentials)
				pool.AssignmentStrategy = p.AssignmentStrategy
				pool.MaxConcurrentSessions = p.MaxConcurrentSessions
				pool.CooldownOnRateLimitSeconds = p.CooldownOnRateLimitSeconds
				pool.LeaseTTLSeconds = p.LeaseTTLSeconds
				pool.RenewBeforeBufferSeconds = p.RenewBeforeBufferSeconds
				pool.HostPatterns = p.HostPatterns
				pool.DeliveryMode = p.DeliveryMode
				pool.ProxyDialect = p.ProxyDialect
				pool.ProxyEndpoint = p.ProxyEndpoint
				pool.CacheScope = p.CacheScope
				pool.CachePolicy = toCachePolicy(p.CachePolicy)
				pool.DeletedAt = time.Time{}
				return nil
			})
			if err != nil {
				out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
				continue
			}
		}
		out.add(i, p.Name, action, code, "", conflicts)
	}
	return out
}

// bootstrapT4KMSProbe runs the §12.5 line 301 admin-time KMS availability
// probe for a bootstrap tenant entry that requests workspaceTier: T4. It
// is a no-op for any other tier and when no probe is wired, mirroring the
// PUT /v1/admin/tenants/{id} promotion path so the bootstrap seed cannot
// mark a tenant T4 without the per-tenant key being observed usable. The
// returned error carries the §12.9 CLASSIFICATION_CONTROL_VIOLATION
// semantics translated into a per-entry seed-validation failure.
func (r *Router) bootstrapT4KMSProbe(ctx context.Context, tenantID, workspaceTier string) error {
	if workspaceTier != tenantstore.WorkspaceTierT4 || r.kmsProbe == nil {
		return nil
	}
	if err := r.kmsProbe.ProbeAvailability(ctx, tenantID, tenantstore.WorkspaceTierT4); err != nil {
		return errors.New("T4 KMS key availability probe failed (CLASSIFICATION_CONTROL_VIOLATION); " +
			"the per-tenant KMS key must be reachable before the tenant can be marked workspaceTier T4: " + err.Error())
	}
	return nil
}

// bootstrapCacheScope enforces the §4.9 cacheScope contract outside the
// HTTP response path so the bootstrap loop can record a per-entry error
// instead of writing a status code. It mirrors validateCacheScope.
func (r *Router) bootstrapCacheScope(req *http.Request, tenant, scope string) error {
	if !validCacheScopes[scope] {
		return errors.New("cacheScope must be per-user, per-session, or tenant")
	}
	if scope != "tenant" {
		return nil
	}
	row, err := r.tenants.Get(req.Context(), tenant)
	if err != nil {
		return errors.New("cacheScope tenant requires a known tenant")
	}
	if crossUserCacheRegulatedProfiles[row.ComplianceProfile] {
		return errors.New("cacheScope tenant is prohibited for a tenant with a regulated complianceProfile (COMPLIANCE_CROSS_USER_CACHE_PROHIBITED)")
	}
	return nil
}

func (r *Router) upsertTenants(req *http.Request, in []TenantPayload, opts bootstrapOptions) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if p.ID == "" {
			out.add(i, "", actionError, seedValidationCode, "id is required", nil)
			continue
		}
		if err := auth.ValidateTenantID(p.ID); err != nil {
			out.add(i, p.ID, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		// spec: §12.9 line 1048; §15.1 line 816 — the bootstrap seed path
		// is held to the same closed workspaceTier enum (T3 default or T4)
		// as the live admin POST/PUT path, so a typo or stale tier name in
		// a seed file is rejected rather than persisted as "not T4".
		if !validWorkspaceTier(p.WorkspaceTier) {
			out.add(i, p.ID, actionError, seedValidationCode, "workspaceTier must be T3 or T4", nil)
			continue
		}
		existing, err := r.tenants.Get(req.Context(), p.ID)
		if errors.Is(err, tenantstore.ErrNotFound) {
			// spec: §12.5 line 301 / §12.9 — a seed that creates a T4 tenant
			// runs the admin-time KMS availability probe before persisting,
			// so the bootstrap path cannot mark a tenant T4 without the
			// per-tenant key having been observed usable.
			if err := r.bootstrapT4KMSProbe(req.Context(), p.ID, p.WorkspaceTier); err != nil {
				out.add(i, p.ID, actionError, seedValidationCode, err.Error(), nil)
				continue
			}
			if !opts.dryRun {
				row := tenantstore.Tenant{
					ID:                  p.ID,
					DisplayName:         p.DisplayName,
					ComplianceProfile:   p.ComplianceProfile,
					DataResidencyRegion: p.DataResidencyRegion,
					WorkspaceTier:       p.WorkspaceTier,
					CreatedAt:           r.clock(),
				}
				row.UpdatedAt = row.CreatedAt
				if err := r.tenants.Create(req.Context(), row); err != nil {
					out.add(i, p.ID, actionError, seedStoreErrorCode, err.Error(), nil)
					continue
				}
				// spec: §15.1 — a seed-created tenant is provisioned the same
				// per-tenant billing_seq_/audit_seq_ sequences as the live
				// POST /v1/admin/tenants path, so a bootstrap-seeded tenant
				// (including the Day-1 default tenant) has both sequences
				// before its first billing or audit event. A provisioning
				// failure is reported as a seed error for this row rather
				// than silently leaving the tenant unable to bill or audit.
				// F-11.2.10.
				if err := r.provisionTenantSequences(req.Context(), p.ID); err != nil {
					out.add(i, p.ID, actionError, seedStoreErrorCode, err.Error(), nil)
					continue
				}
			}
			out.add(i, p.ID, actionCreated, "", "", nil)
			continue
		}
		if err != nil {
			out.add(i, p.ID, actionError, seedStoreErrorCode, err.Error(), nil)
			continue
		}
		// spec: §12.9 line 1033; §15.1 line 816 — workspaceTier is ratcheted
		// stricter-only. A bootstrap re-run that names a looser tier on a
		// currently-stricter tenant (e.g. T3 over a T4 tenant) is rejected
		// regardless of --force-update; a silent downgrade would weaken the
		// tenant's data-classification controls.
		if p.WorkspaceTier != "" && tenantstore.IsWorkspaceTierDowngrade(existing.WorkspaceTier, p.WorkspaceTier) {
			out.add(i, p.ID, actionError, seedValidationCode,
				"workspaceTier may be tightened in place but not lowered; the seed would downgrade tenant "+
					p.ID+" from "+existing.WorkspaceTier+" to "+p.WorkspaceTier, nil)
			continue
		}
		// spec: §12.5 line 301 — re-running the seed with workspaceTier: T4
		// (a promotion or an idempotent re-assert) re-runs the admin-time
		// KMS availability probe, matching the PUT /v1/admin/tenants/{id}
		// contract.
		if err := r.bootstrapT4KMSProbe(req.Context(), p.ID, p.WorkspaceTier); err != nil {
			out.add(i, p.ID, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		conflicts := tenantConflicts(existing, p)
		action, code, doUpdate := resolveExisting(conflicts, nil, opts.forceUpdate)
		if doUpdate && !opts.dryRun {
			_, err = r.tenants.Update(req.Context(), existing.ID, func(t *tenantstore.Tenant) error {
				if p.DisplayName != "" {
					t.DisplayName = p.DisplayName
				}
				if p.ComplianceProfile != "" {
					t.ComplianceProfile = p.ComplianceProfile
				}
				if p.DataResidencyRegion != "" {
					t.DataResidencyRegion = p.DataResidencyRegion
				}
				if p.WorkspaceTier != "" {
					t.WorkspaceTier = p.WorkspaceTier
				}
				// §17.6 bootstrap re-runs: a previously-soft-deleted tenant
				// resurfaces in the bootstrap payload as an active record.
				// Clear the DeletedAt timestamp so the §11.2 QuotaEvaluator
				// and the §4.2 RLS policy treat the tenant as live again.
				t.DeletedAt = time.Time{}
				return nil
			})
			if err != nil {
				out.add(i, p.ID, actionError, seedStoreErrorCode, err.Error(), nil)
				continue
			}
		}
		out.add(i, p.ID, action, code, "", conflicts)
	}
	return out
}

func (r *Router) upsertRuntimes(req *http.Request, in []RuntimePayload, opts bootstrapOptions) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if err := runtimestore.ValidateName(p.Name); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := p.validatePayloadEnums(); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := validateCapabilities(p.Capabilities); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := validateLimits(p.Limits); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		// §6.2 line 260: the gateway-side maxFinalizingTimeoutSeconds outer
		// bound must be ≥ the runtime-side setupPolicy.timeoutSeconds inner
		// bound; reject configurations that violate the invariant at every
		// admission path, including bootstrap.
		if err := validateSetupPolicy(p.SetupPolicy, r.maxFinalizingTimeoutSeconds); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := validateSetupCommandPolicy(p.SetupCommandPolicy); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := validateDefaultPoolConfig(p.DefaultPoolConfig); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := validateWorkspaceDefaults(p.WorkspaceDefaults); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := validateSharedAssets(p.SharedAssets); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := validateRuntimeOptionsSchema(p.RuntimeOptionsSchema); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		// §5.1 line 36: integrationLevel is only valid on type:agent.
		if err := p.validateIntegrationLevelOnType(); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		// §5.1 lines 132-158: a bootstrap-seeded derived runtime is held to
		// the same registration rules as one created via POST.
		if err := r.validateDerivedRuntime(req.Context(), p); err != nil {
			out.add(i, p.Name, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		// §5.1: a type:mcp runtime does not carry an agentInterface.
		if runtimestore.RuntimeType(p.Type) == runtimestore.TypeMCP && p.AgentInterface != nil {
			out.add(i, p.Name, actionError, seedValidationCode,
				"agentInterface is not valid on a type:mcp runtime", nil)
			continue
		}
		existing, err := r.runtimes.Get(req.Context(), p.Name)
		if errors.Is(err, runtimestore.ErrNotFound) {
			// §5.1 line 51: labels are required from v1 on a newly-registered
			// runtime. An update of an existing runtime may omit labels in the
			// seed (the stored set persists via applyRuntimePayload).
			if lerr := p.validateLabelsRequired(); lerr != nil {
				out.add(i, p.Name, actionError, seedValidationCode, lerr.Error(), nil)
				continue
			}
			if !opts.dryRun {
				row := runtimeFromPayload(p, r.clock())
				runtimestore.ApplyDefaults(&row, r.devMode)
				row.UpdatedAt = row.CreatedAt
				// §5.1 lines 283-291: a runtime with an agentInterface gets a
				// write-time auto-generated A2A agent card.
				r.applyGeneratedCard(&row, row.CreatedAt)
				if err := r.runtimes.Create(req.Context(), row); err != nil {
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
		conflicts, securityCritical := runtimeConflicts(existing, p)
		action, code, doUpdate := resolveExisting(conflicts, securityCritical, opts.forceUpdate)
		if action == actionError && code == seedSecurityCriticalCode {
			// §17.6 line 451: a security-critical field (isolationProfile)
			// overwrite is blocked regardless of --force-update.
			out.add(i, p.Name, actionError, seedSecurityCriticalCode,
				"runtime "+p.Name+": seed would overwrite security-critical field(s) "+
					joinFields(securityCritical)+"; blocked regardless of --force-update", nil)
			continue
		}
		if doUpdate && !opts.dryRun {
			_, err = r.runtimes.Update(req.Context(), existing.Name, func(rt *runtimestore.Runtime) error {
				applyRuntimePayload(rt, p)
				// §5.1 lines 283-291: regenerate the auto-generated A2A
				// agent card at write time whenever the resulting runtime
				// carries an agentInterface, matching the PUT handler.
				r.applyGeneratedCard(rt, r.clock())
				return nil
			})
			if err != nil {
				out.add(i, p.Name, actionError, seedStoreErrorCode, err.Error(), nil)
				continue
			}
		}
		out.add(i, p.Name, action, code, "", conflicts)
	}
	return out
}

// applyRuntimePayload merges the seed-specified runtime fields onto rt,
// the §17.6 line 450 force-update PUT (only fields present in the seed
// are replaced). Shared by the create-then-update and update paths.
func applyRuntimePayload(rt *runtimestore.Runtime, p RuntimePayload) {
	if p.Image != "" {
		rt.Image = p.Image
	}
	if p.ExecutionMode != "" {
		rt.ExecutionMode = runtimestore.ExecutionMode(p.ExecutionMode)
	}
	if p.IsolationProfile != "" {
		rt.IsolationProfile = isolation.Profile(p.IsolationProfile)
	}
	if p.IntegrationLevel != "" {
		rt.IntegrationLevel = runtimestore.IntegrationLevel(p.IntegrationLevel)
	}
	if p.WorkspaceTier != "" {
		rt.WorkspaceTier = runtimestore.WorkspaceTier(p.WorkspaceTier)
	}
	if p.Description != "" {
		rt.Description = p.Description
	}
	if p.Capabilities != nil {
		rt.Capabilities = p.Capabilities
	}
	if p.DelegationPolicyRef != "" {
		rt.DelegationPolicyRef = p.DelegationPolicyRef
	}
	if p.Labels != nil {
		rt.Labels = p.Labels
	}
	if p.AgentInterface != nil {
		rt.AgentInterface = p.AgentInterface
	}
	if p.PublishedMetadata != nil {
		rt.PublishedMetadata = p.PublishedMetadata
	}
	if p.CapabilityInferenceMode != "" {
		rt.CapabilityInferenceMode = capabilityinference.Mode(p.CapabilityInferenceMode)
	}
	if p.ToolCapabilityOverrides != nil {
		rt.ToolCapabilityOverrides = p.ToolCapabilityOverrides
	}
	if p.SetupPolicy != nil {
		rt.SetupPolicy = p.SetupPolicy
	}
	if p.Limits != nil {
		rt.Limits = p.Limits
	}
	if p.SetupCommandPolicy != nil {
		rt.SetupCommandPolicy = p.SetupCommandPolicy
	}
	if p.DefaultPoolConfig != nil {
		rt.DefaultPoolConfig = p.DefaultPoolConfig
	}
	if p.WorkspaceDefaults != nil {
		rt.WorkspaceDefaults = p.WorkspaceDefaults
	}
	if p.SharedAssets != nil {
		rt.SharedAssets = p.SharedAssets
	}
	if len(p.RuntimeOptionsSchema) > 0 {
		rt.RuntimeOptionsSchema = p.RuntimeOptionsSchema
	}
	if p.MinPlatformVersion != "" {
		rt.MinPlatformVersion = p.MinPlatformVersion
	}
	if p.SessionPolicy != nil {
		rt.SessionPolicy = p.SessionPolicy
	}
	if p.SDKWarmBlockingPaths != nil {
		rt.SDKWarmBlockingPaths = p.SDKWarmBlockingPaths
	}
}

func (r *Router) upsertUsers(req *http.Request, in []UserPayload, opts bootstrapOptions) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if err := userstore.ValidateSubject(p.Subject); err != nil {
			out.add(i, p.Subject, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		if err := auth.ValidateTenantID(p.TenantID); err != nil {
			out.add(i, p.Subject, actionError, seedValidationCode, err.Error(), nil)
			continue
		}
		invalidRole := false
		for _, role := range p.Roles {
			if !role.IsValid() {
				out.add(i, p.Subject, actionError, seedValidationCode,
					"role "+string(role)+" is not a recognised §10.2 RBAC role", nil)
				invalidRole = true
				break
			}
		}
		if invalidRole {
			continue
		}
		existing, err := r.users.Get(req.Context(), p.TenantID, p.Subject)
		if errors.Is(err, userstore.ErrNotFound) {
			if !opts.dryRun {
				row := userstore.User{
					Subject:     p.Subject,
					TenantID:    p.TenantID,
					Email:       p.Email,
					DisplayName: p.DisplayName,
					Roles:       p.Roles,
					// spec: §10.2 line 294 — a seeded user's platform-managed
					// roles override the OIDC claim.
					RoleAssigned: true,
					Disabled:     p.Disabled,
					CreatedAt:    r.clock(),
				}
				row.UpdatedAt = row.CreatedAt
				if err := r.users.Create(req.Context(), row); err != nil {
					out.add(i, p.Subject, actionError, seedStoreErrorCode, err.Error(), nil)
					continue
				}
			}
			out.add(i, p.Subject, actionCreated, "", "", nil)
			continue
		}
		if err != nil {
			out.add(i, p.Subject, actionError, seedStoreErrorCode, err.Error(), nil)
			continue
		}
		conflicts := userConflicts(existing, p)
		action, code, doUpdate := resolveExisting(conflicts, nil, opts.forceUpdate)
		if doUpdate && !opts.dryRun {
			_, err = r.users.Update(req.Context(), existing.TenantID, existing.Subject, func(u *userstore.User) error {
				if p.Email != "" {
					u.Email = p.Email
				}
				if p.DisplayName != "" {
					u.DisplayName = p.DisplayName
				}
				if len(p.Roles) > 0 {
					u.Roles = p.Roles
				}
				u.Disabled = p.Disabled
				return nil
			})
			if err != nil {
				out.add(i, p.Subject, actionError, seedStoreErrorCode, err.Error(), nil)
				continue
			}
		}
		out.add(i, p.Subject, action, code, "", conflicts)
	}
	return out
}

// joinFields renders a field-name list for an error message.
func joinFields(fields []string) string {
	out := ""
	for i, f := range fields {
		if i > 0 {
			out += ", "
		}
		out += f
	}
	return out
}

// tenantConflicts returns the seed-specified tenant fields whose values
// differ from the stored row (§17.6 line 450). Only fields the seed sets
// (non-empty) are compared, mirroring the merge-update mutator.
func tenantConflicts(existing tenantstore.Tenant, p TenantPayload) []string {
	var c []string
	if p.DisplayName != "" && existing.DisplayName != p.DisplayName {
		c = append(c, "displayName")
	}
	if p.ComplianceProfile != "" && existing.ComplianceProfile != p.ComplianceProfile {
		c = append(c, "complianceProfile")
	}
	if p.DataResidencyRegion != "" && existing.DataResidencyRegion != p.DataResidencyRegion {
		c = append(c, "dataResidencyRegion")
	}
	if p.WorkspaceTier != "" && existing.WorkspaceTier != p.WorkspaceTier {
		c = append(c, "workspaceTier")
	}
	return c
}

// userConflicts returns the seed-specified user fields whose values
// differ from the stored row (§17.6 line 450).
func userConflicts(existing userstore.User, p UserPayload) []string {
	var c []string
	if p.Email != "" && existing.Email != p.Email {
		c = append(c, "email")
	}
	if p.DisplayName != "" && existing.DisplayName != p.DisplayName {
		c = append(c, "displayName")
	}
	if len(p.Roles) > 0 && !reflect.DeepEqual(existing.Roles, p.Roles) {
		c = append(c, "roles")
	}
	if existing.Disabled != p.Disabled {
		c = append(c, "disabled")
	}
	return c
}

// runtimeConflicts returns the seed-specified runtime fields whose values
// differ from the stored row (§17.6 line 450), and separately the subset
// that is security-critical (§17.6 line 451 — isolationProfile). Only
// fields the seed sets are compared, mirroring the merge-update mutator.
func runtimeConflicts(existing runtimestore.Runtime, p RuntimePayload) (conflicts, securityCritical []string) {
	if p.Image != "" && existing.Image != p.Image {
		conflicts = append(conflicts, "image")
	}
	if p.ExecutionMode != "" && string(existing.ExecutionMode) != p.ExecutionMode {
		conflicts = append(conflicts, "executionMode")
	}
	if p.IsolationProfile != "" && string(existing.IsolationProfile) != p.IsolationProfile {
		conflicts = append(conflicts, "isolationProfile")
		securityCritical = append(securityCritical, "isolationProfile")
	}
	if p.IntegrationLevel != "" && string(existing.IntegrationLevel) != p.IntegrationLevel {
		conflicts = append(conflicts, "integrationLevel")
	}
	if p.WorkspaceTier != "" && string(existing.WorkspaceTier) != p.WorkspaceTier {
		conflicts = append(conflicts, "workspaceTier")
	}
	if p.Description != "" && existing.Description != p.Description {
		conflicts = append(conflicts, "description")
	}
	if p.DelegationPolicyRef != "" && existing.DelegationPolicyRef != p.DelegationPolicyRef {
		conflicts = append(conflicts, "delegationPolicyRef")
	}
	if p.MinPlatformVersion != "" && existing.MinPlatformVersion != p.MinPlatformVersion {
		conflicts = append(conflicts, "minPlatformVersion")
	}
	if p.CapabilityInferenceMode != "" && string(existing.CapabilityInferenceMode) != p.CapabilityInferenceMode {
		conflicts = append(conflicts, "capabilityInferenceMode")
	}
	if p.Capabilities != nil && !reflect.DeepEqual(existing.Capabilities, p.Capabilities) {
		conflicts = append(conflicts, "capabilities")
	}
	if p.Labels != nil && !reflect.DeepEqual(existing.Labels, p.Labels) {
		conflicts = append(conflicts, "labels")
	}
	if p.AgentInterface != nil && !reflect.DeepEqual(existing.AgentInterface, p.AgentInterface) {
		conflicts = append(conflicts, "agentInterface")
	}
	return conflicts, securityCritical
}

// credentialPoolConflicts returns the seed-specified credential-pool
// fields whose values differ from the stored row (§17.6 line 450). The
// credential-pool update mutator replaces every field, so all seeded
// scalars are compared.
func credentialPoolConflicts(existing credentialpoolstore.CredentialPool, p CredentialPoolPayload) []string {
	var c []string
	if p.Provider != "" && existing.Provider != p.Provider {
		c = append(c, "provider")
	}
	if p.AssignmentStrategy != "" && existing.AssignmentStrategy != p.AssignmentStrategy {
		c = append(c, "assignmentStrategy")
	}
	if p.MaxConcurrentSessions != 0 && existing.MaxConcurrentSessions != p.MaxConcurrentSessions {
		c = append(c, "maxConcurrentSessions")
	}
	if p.CooldownOnRateLimitSeconds != 0 && existing.CooldownOnRateLimitSeconds != p.CooldownOnRateLimitSeconds {
		c = append(c, "cooldownOnRateLimitSeconds")
	}
	if p.LeaseTTLSeconds != 0 && existing.LeaseTTLSeconds != p.LeaseTTLSeconds {
		c = append(c, "leaseTtlSeconds")
	}
	if p.CacheScope != "" && existing.CacheScope != p.CacheScope {
		c = append(c, "cacheScope")
	}
	if p.DeliveryMode != "" && existing.DeliveryMode != p.DeliveryMode {
		c = append(c, "deliveryMode")
	}
	if p.Credentials != nil && !reflect.DeepEqual(existing.Credentials, toCredentials(p.Credentials)) {
		c = append(c, "credentials")
	}
	return c
}
