// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// BootstrapRequest is the §24.1 bootstrap seed payload accepted by
// POST /v1/admin/bootstrap. The handler applies upsert semantics
// per §24.1: each entry either creates a new row or merges into an
// existing one. Operations are best-effort — the handler returns an
// aggregate result with per-section counts and per-entry errors.
type BootstrapRequest struct {
	Tenants         []TenantPayload         `json:"tenants,omitempty"`
	Runtimes        []RuntimePayload        `json:"runtimes,omitempty"`
	Users           []UserPayload           `json:"users,omitempty"`
	CredentialPools []CredentialPoolPayload `json:"credentialPools,omitempty"`
}

// BootstrapResponse is the response envelope. CreatedCount tracks
// rows the handler inserted; UpdatedCount tracks rows merged into;
// Errors carries per-entry failures (the handler does NOT stop on
// the first error per §24.1 "all-or-nothing is not required").
type BootstrapResponse struct {
	Tenants         BootstrapSection `json:"tenants,omitempty"`
	Runtimes        BootstrapSection `json:"runtimes,omitempty"`
	Users           BootstrapSection `json:"users,omitempty"`
	CredentialPools BootstrapSection `json:"credentialPools,omitempty"`
}

// BootstrapSection is the per-resource result.
type BootstrapSection struct {
	CreatedCount int              `json:"createdCount"`
	UpdatedCount int              `json:"updatedCount"`
	Errors       []BootstrapError `json:"errors,omitempty"`
	Applied      []string         `json:"applied,omitempty"`
}

// BootstrapError captures a single per-entry failure.
type BootstrapError struct {
	Index   int    `json:"index"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
}

// handleBootstrap implements POST /v1/admin/bootstrap.
func (r *Router) handleBootstrap(w http.ResponseWriter, req *http.Request) {
	var body BootstrapRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}

	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}

	out := BootstrapResponse{}
	if r.tenants != nil {
		out.Tenants = r.upsertTenants(req, body.Tenants)
	}
	if r.runtimes != nil {
		out.Runtimes = r.upsertRuntimes(req, body.Runtimes)
	}
	if r.users != nil {
		out.Users = r.upsertUsers(req, body.Users)
	}
	if r.credentialPools != nil {
		out.CredentialPools = r.upsertCredentialPools(req, body.CredentialPools)
	}

	r.emit(req.Context(), principal, "admin.bootstrap.applied", "platform", map[string]any{
		"tenants":         map[string]any{"created": out.Tenants.CreatedCount, "updated": out.Tenants.UpdatedCount, "errors": len(out.Tenants.Errors)},
		"runtimes":        map[string]any{"created": out.Runtimes.CreatedCount, "updated": out.Runtimes.UpdatedCount, "errors": len(out.Runtimes.Errors)},
		"users":           map[string]any{"created": out.Users.CreatedCount, "updated": out.Users.UpdatedCount, "errors": len(out.Users.Errors)},
		"credentialPools": map[string]any{"created": out.CredentialPools.CreatedCount, "updated": out.CredentialPools.UpdatedCount, "errors": len(out.CredentialPools.Errors)},
	})

	status := http.StatusOK
	if anyFailures(out) {
		// 207 Multi-Status would be ideal; 200 is acceptable because
		// the body carries per-entry results. Use 207 to signal partial
		// failure so curl pipelines fail fast.
		status = http.StatusMultiStatus
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}

func anyFailures(out BootstrapResponse) bool {
	return len(out.Tenants.Errors) > 0 ||
		len(out.Runtimes.Errors) > 0 ||
		len(out.Users.Errors) > 0 ||
		len(out.CredentialPools.Errors) > 0
}

// upsertCredentialPools applies the §4.9 bootstrap.credentialPools seed
// list (§24.1 upsert semantics, keyed by (tenantId, name)). It enforces
// the same §4.9 cacheScope contract the admin POST/PUT path enforces:
// `cacheScope: tenant` is rejected for a regulated complianceProfile.
//
// spec: §4.9 lines 1697, 1711 — credential pools are part of the Helm
// bootstrap seed surface, not only the live admin API.
func (r *Router) upsertCredentialPools(req *http.Request, in []CredentialPoolPayload) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		tenant := p.TenantID
		if tenant == "" {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: "tenantId is required"})
			continue
		}
		if !validAssignmentStrategies[p.AssignmentStrategy] {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name,
				Message: "assignmentStrategy must be least-loaded, round-robin, or sticky-until-failure"})
			continue
		}
		if err := r.bootstrapCacheScope(req, tenant, p.CacheScope); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		_, err := r.credentialPools.Get(req.Context(), tenant, p.Name)
		if errors.Is(err, credentialpoolstore.ErrNotFound) {
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
				CreatedAt:                  r.clock(),
			}
			pool.UpdatedAt = pool.CreatedAt
			if err := r.credentialPools.Create(req.Context(), pool); err != nil {
				out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
				continue
			}
			out.CreatedCount++
			out.Applied = append(out.Applied, p.Name)
			continue
		}
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
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
			pool.DeletedAt = time.Time{}
			return nil
		})
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		out.UpdatedCount++
		out.Applied = append(out.Applied, p.Name)
	}
	return out
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

func (r *Router) upsertTenants(req *http.Request, in []TenantPayload) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if p.ID == "" {
			out.Errors = append(out.Errors, BootstrapError{Index: i, Message: "id is required"})
			continue
		}
		if err := auth.ValidateTenantID(p.ID); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.ID, Message: err.Error()})
			continue
		}
		existing, err := r.tenants.Get(req.Context(), p.ID)
		if errors.Is(err, tenantstore.ErrNotFound) {
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
				out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.ID, Message: err.Error()})
				continue
			}
			out.CreatedCount++
			out.Applied = append(out.Applied, p.ID)
			continue
		}
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.ID, Message: err.Error()})
			continue
		}
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
			// §17.6 bootstrap re-runs: a previously-soft-deleted
			// tenant resurfaces in the bootstrap payload as an
			// active record. Clear the DeletedAt timestamp so the
			// §11.2 QuotaEvaluator and the §4.2 RLS policy treat the
			// tenant as live again. Without this, a tenant deleted
			// by a prior test-cleanup pass stays soft-deleted on
			// the next bootstrap and every session-create returns
			// `QUOTA_EXCEEDED` because LookupLimits sees an inactive
			// tenant.
			t.DeletedAt = time.Time{}
			return nil
		})
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.ID, Message: err.Error()})
			continue
		}
		out.UpdatedCount++
		out.Applied = append(out.Applied, p.ID)
	}
	return out
}

func (r *Router) upsertRuntimes(req *http.Request, in []RuntimePayload) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if err := runtimestore.ValidateName(p.Name); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := p.validatePayloadEnums(); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := validateCapabilities(p.Capabilities); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := validateLimits(p.Limits); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := validateSetupCommandPolicy(p.SetupCommandPolicy); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := validateDefaultPoolConfig(p.DefaultPoolConfig); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := validateWorkspaceDefaults(p.WorkspaceDefaults); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := validateSharedAssets(p.SharedAssets); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := validateRuntimeOptionsSchema(p.RuntimeOptionsSchema); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		// §5.1 line 36: integrationLevel is only valid on type:agent.
		if err := p.validateIntegrationLevelOnType(); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		// §5.1 lines 132-158: a bootstrap-seeded derived runtime is held to
		// the same registration rules as one created via POST — its base
		// must exist and be standalone, it may not set prohibited fields,
		// and its runtimeOptionsSchema may only reference base property
		// names (F-5.1.18).
		if err := r.validateDerivedRuntime(req.Context(), p); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		// §5.1: a type:mcp runtime does not carry an agentInterface.
		if runtimestore.RuntimeType(p.Type) == runtimestore.TypeMCP && p.AgentInterface != nil {
			out.Errors = append(out.Errors, BootstrapError{
				Index: i, ID: p.Name,
				Message: "agentInterface is not valid on a type:mcp runtime",
			})
			continue
		}
		existing, err := r.runtimes.Get(req.Context(), p.Name)
		if errors.Is(err, runtimestore.ErrNotFound) {
			row := runtimeFromPayload(p, r.clock())
			runtimestore.ApplyDefaults(&row)
			row.UpdatedAt = row.CreatedAt
			// §5.1 lines 283-291: a runtime with an agentInterface gets a
			// write-time auto-generated A2A agent card stored as a
			// publishedMetadata entry — the bootstrap install path must
			// generate it just as the POST handler does.
			r.applyGeneratedCard(&row, row.CreatedAt)
			if err := r.runtimes.Create(req.Context(), row); err != nil {
				out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
				continue
			}
			out.CreatedCount++
			out.Applied = append(out.Applied, p.Name)
			continue
		}
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		_, err = r.runtimes.Update(req.Context(), existing.Name, func(rt *runtimestore.Runtime) error {
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
			if p.TaskPolicy != nil {
				rt.TaskPolicy = p.TaskPolicy
			}
			if p.SDKWarmBlockingPaths != nil {
				rt.SDKWarmBlockingPaths = p.SDKWarmBlockingPaths
			}
			// §5.1 lines 283-291: regenerate the auto-generated A2A agent
			// card at write time whenever the resulting runtime carries an
			// agentInterface, matching the PUT handler.
			r.applyGeneratedCard(rt, r.clock())
			return nil
		})
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		out.UpdatedCount++
		out.Applied = append(out.Applied, p.Name)
	}
	return out
}

func (r *Router) upsertUsers(req *http.Request, in []UserPayload) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if err := userstore.ValidateSubject(p.Subject); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
			continue
		}
		if err := auth.ValidateTenantID(p.TenantID); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
			continue
		}
		for _, role := range p.Roles {
			if !role.IsValid() {
				out.Errors = append(out.Errors, BootstrapError{
					Index: i, ID: p.Subject,
					Message: "role " + string(role) + " is not a recognised §10.2 RBAC role",
				})
				continue
			}
		}
		existing, err := r.users.Get(req.Context(), p.TenantID, p.Subject)
		if errors.Is(err, userstore.ErrNotFound) {
			row := userstore.User{
				Subject:     p.Subject,
				TenantID:    p.TenantID,
				Email:       p.Email,
				DisplayName: p.DisplayName,
				Roles:       p.Roles,
				Disabled:    p.Disabled,
				CreatedAt:   r.clock(),
			}
			row.UpdatedAt = row.CreatedAt
			if err := r.users.Create(req.Context(), row); err != nil {
				out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
				continue
			}
			out.CreatedCount++
			out.Applied = append(out.Applied, p.Subject)
			continue
		}
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
			continue
		}
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
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
			continue
		}
		out.UpdatedCount++
		out.Applied = append(out.Applied, p.Subject)
	}
	return out
}
