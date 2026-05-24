// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// CredentialPoolPayload is the §4.9 / §15.1 admin CredentialPool wire
// shape.
type CredentialPoolPayload struct {
	TenantID                   string                   `json:"tenantId,omitempty"`
	Name                       string                   `json:"name"`
	Provider                   string                   `json:"provider,omitempty"`
	Credentials                []CredentialEntryPayload `json:"credentials,omitempty"`
	AssignmentStrategy         string                   `json:"assignmentStrategy,omitempty"`
	MaxConcurrentSessions      int                      `json:"maxConcurrentSessions,omitempty"`
	CooldownOnRateLimitSeconds int                      `json:"cooldownOnRateLimitSeconds,omitempty"`
	LeaseTTLSeconds            int                      `json:"leaseTTLSeconds,omitempty"`
	RenewBeforeBufferSeconds   int                      `json:"renewBeforeBufferSeconds,omitempty"`
	HostPatterns               []string                 `json:"hostPatterns,omitempty"`
	CacheScope                 string                   `json:"cacheScope,omitempty"`
	CreatedAt                  string                   `json:"createdAt,omitempty"`
	UpdatedAt                  string                   `json:"updatedAt,omitempty"`
	DeletedAt                  string                   `json:"deletedAt,omitempty"`
}

// CredentialEntryPayload is one §4.9 credential in a pool.
type CredentialEntryPayload struct {
	ID        string `json:"id"`
	SecretRef string `json:"secretRef,omitempty"`
	RoleArn   string `json:"roleArn,omitempty"`
	Region    string `json:"region,omitempty"`
}

// validAssignmentStrategies is the §4.9 closed enum. Empty selects the
// store-side default.
var validAssignmentStrategies = map[string]bool{
	"":                     true,
	"least-loaded":         true,
	"round-robin":          true,
	"sticky-until-failure": true,
}

// validCacheScopes is the §4.9 cacheScope closed enum. Empty selects
// the per-user default.
var validCacheScopes = map[string]bool{
	"":            true,
	"per-user":    true,
	"per-session": true,
	"tenant":      true,
}

// crossUserCacheRegulatedProfiles names the §4.9 compliance profiles
// for which `cacheScope: tenant` is rejected at pool registration.
// The spec names hipaa and fedramp specifically; soc2 is not on the
// list (it is on the broader §12.8 audit-signal set).
var crossUserCacheRegulatedProfiles = map[string]bool{
	"hipaa":   true,
	"fedramp": true,
}

// validateCacheScope enforces the §4.9 cacheScope contract for a pool
// registration. It returns false and writes the error response when
// the scope is outside the enum, or when `cacheScope: tenant` is set on
// a tenant carrying a regulated complianceProfile.
//
// spec: §4.9 lines 1554-1556 — `cacheScope: tenant` on a pool whose
// tenant has a regulated complianceProfile (hipaa, fedramp) is rejected
// with 400 COMPLIANCE_CROSS_USER_CACHE_PROHIBITED. The rejection also
// mitigates a cross-user timing side-channel.
func (r *Router) validateCacheScope(w http.ResponseWriter, req *http.Request, tenant, scope string) bool {
	if !validCacheScopes[scope] {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"cacheScope must be per-user, per-session, or tenant",
			map[string]any{"field": "cacheScope"})
		return false
	}
	if scope != "tenant" {
		return true
	}
	row, err := r.tenants.Get(req.Context(), tenant)
	if err != nil {
		// A missing tenant cannot be confirmed non-regulated; fail
		// closed rather than admit a cross-user cache for an unknown
		// compliance posture.
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"cacheScope tenant requires a known tenant", map[string]any{"field": "cacheScope"})
		return false
	}
	if crossUserCacheRegulatedProfiles[row.ComplianceProfile] {
		writeError(w, http.StatusBadRequest, "COMPLIANCE_CROSS_USER_CACHE_PROHIBITED",
			"cacheScope tenant is prohibited for a tenant with a regulated complianceProfile",
			map[string]any{"complianceProfile": row.ComplianceProfile})
		return false
	}
	return true
}

// fromCredentialPool maps a stored pool to the wire payload.
func fromCredentialPool(p credentialpoolstore.CredentialPool) CredentialPoolPayload {
	out := CredentialPoolPayload{
		TenantID:                   p.TenantID,
		Name:                       p.Name,
		Provider:                   p.Provider,
		AssignmentStrategy:         p.AssignmentStrategy,
		MaxConcurrentSessions:      p.MaxConcurrentSessions,
		CooldownOnRateLimitSeconds: p.CooldownOnRateLimitSeconds,
		LeaseTTLSeconds:            p.LeaseTTLSeconds,
		RenewBeforeBufferSeconds:   p.RenewBeforeBufferSeconds,
		HostPatterns:               p.HostPatterns,
		CacheScope:                 p.CacheScope,
		CreatedAt:                  rfc3339Nano(p.CreatedAt),
		UpdatedAt:                  rfc3339Nano(p.UpdatedAt),
		DeletedAt:                  rfc3339Nano(p.DeletedAt),
	}
	for _, c := range p.Credentials {
		out.Credentials = append(out.Credentials, CredentialEntryPayload{
			ID: c.ID, SecretRef: c.SecretRef, RoleArn: c.RoleArn, Region: c.Region,
		})
	}
	return out
}

// toCredentials maps the wire credential entries to the store
// representation.
func toCredentials(in []CredentialEntryPayload) []credentialpoolstore.Credential {
	if in == nil {
		return nil
	}
	out := make([]credentialpoolstore.Credential, 0, len(in))
	for _, c := range in {
		out = append(out, credentialpoolstore.Credential{
			ID: c.ID, SecretRef: c.SecretRef, RoleArn: c.RoleArn, Region: c.Region,
		})
	}
	return out
}

// WithCredentialPools wires the §15.1 credential-pool CRUD handlers
// onto the Router.
func (r *Router) WithCredentialPools(s credentialpoolstore.Store) *Router {
	r.credentialPools = s
	return r
}

// handleCreateCredentialPool implements POST /v1/admin/credential-pools.
//
// The §4.9 Token Service RBAC live-probe — verifying the Token Service
// can read every referenced secret before the pool is persisted — is
// deferred: it requires the gateway-to-Token-Service mTLS probe link,
// which is not yet built.
func (r *Router) handleCreateCredentialPool(w http.ResponseWriter, req *http.Request) {
	var body CredentialPoolPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if !validAssignmentStrategies[body.AssignmentStrategy] {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"assignmentStrategy must be least-loaded, round-robin, or sticky-until-failure",
			map[string]any{"field": "assignmentStrategy"})
		return
	}
	if !r.validateCacheScope(w, req, tenant, body.CacheScope) {
		return
	}
	pool := credentialpoolstore.CredentialPool{
		TenantID:                   tenant,
		Name:                       body.Name,
		Provider:                   body.Provider,
		Credentials:                toCredentials(body.Credentials),
		AssignmentStrategy:         body.AssignmentStrategy,
		MaxConcurrentSessions:      body.MaxConcurrentSessions,
		CooldownOnRateLimitSeconds: body.CooldownOnRateLimitSeconds,
		LeaseTTLSeconds:            body.LeaseTTLSeconds,
		RenewBeforeBufferSeconds:   body.RenewBeforeBufferSeconds,
		HostPatterns:               body.HostPatterns,
		CacheScope:                 body.CacheScope,
		CreatedAt:                  r.clock(),
	}
	pool.UpdatedAt = pool.CreatedAt
	if err := r.credentialPools.Create(req.Context(), pool); err != nil {
		if errors.Is(err, credentialpoolstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"credential pool with this name already exists in tenant", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.credentialPools.Get(req.Context(), tenant, body.Name)
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.created", body.Name, map[string]any{
		"tenantId":        tenant,
		"provider":        stored.Provider,
		"credentialCount": len(stored.Credentials),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromCredentialPool(stored))
}

func (r *Router) handleListCredentialPools(w http.ResponseWriter, req *http.Request) {
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	rows, err := r.credentialPools.List(req.Context(), tenant, credentialpoolstore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]CredentialPoolPayload, 0, len(rows))
	for _, p := range rows {
		out = append(out, fromCredentialPool(p))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"credentialPools": out})
}

func (r *Router) handleGetCredentialPool(w http.ResponseWriter, req *http.Request) {
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	row, err := r.credentialPools.Get(req.Context(), tenant, req.PathValue("name"))
	if err != nil {
		if errors.Is(err, credentialpoolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCredentialPool(row))
}

// handleUpdateCredentialPool implements PUT — a full replace of the
// mutable fields (provider, credentials, strategy, limits, host
// patterns).
func (r *Router) handleUpdateCredentialPool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body CredentialPoolPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if !validAssignmentStrategies[body.AssignmentStrategy] {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"assignmentStrategy must be least-loaded, round-robin, or sticky-until-failure",
			map[string]any{"field": "assignmentStrategy"})
		return
	}
	if !r.validateCacheScope(w, req, tenant, body.CacheScope) {
		return
	}
	updated, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		p.Provider = body.Provider
		p.Credentials = toCredentials(body.Credentials)
		p.AssignmentStrategy = body.AssignmentStrategy
		p.MaxConcurrentSessions = body.MaxConcurrentSessions
		p.CooldownOnRateLimitSeconds = body.CooldownOnRateLimitSeconds
		p.LeaseTTLSeconds = body.LeaseTTLSeconds
		p.RenewBeforeBufferSeconds = body.RenewBeforeBufferSeconds
		p.HostPatterns = body.HostPatterns
		p.CacheScope = body.CacheScope
		return nil
	})
	if err != nil {
		if errors.Is(err, credentialpoolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.updated", name,
		map[string]any{"tenantId": tenant})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCredentialPool(updated))
}

func (r *Router) handleDeleteCredentialPool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if err := r.credentialPools.SoftDelete(req.Context(), tenant, name, r.clock()); err != nil {
		if errors.Is(err, credentialpoolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.soft_deleted", name,
		map[string]any{"tenantId": tenant})
	w.WriteHeader(http.StatusNoContent)
}
