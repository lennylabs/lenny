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
	updated, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		p.Provider = body.Provider
		p.Credentials = toCredentials(body.Credentials)
		p.AssignmentStrategy = body.AssignmentStrategy
		p.MaxConcurrentSessions = body.MaxConcurrentSessions
		p.CooldownOnRateLimitSeconds = body.CooldownOnRateLimitSeconds
		p.LeaseTTLSeconds = body.LeaseTTLSeconds
		p.RenewBeforeBufferSeconds = body.RenewBeforeBufferSeconds
		p.HostPatterns = body.HostPatterns
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
