// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

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
	DeliveryMode               string                   `json:"deliveryMode,omitempty"`
	ProxyDialect               string                   `json:"proxyDialect,omitempty"`
	ProxyEndpoint              string                   `json:"proxyEndpoint,omitempty"`
	CacheScope                 string                   `json:"cacheScope,omitempty"`
	CreatedAt                  string                   `json:"createdAt,omitempty"`
	UpdatedAt                  string                   `json:"updatedAt,omitempty"`
	DeletedAt                  string                   `json:"deletedAt,omitempty"`
}

// CredentialEntryPayload is one §4.9 credential in a pool. The
// revocation fields (status, revokedAt, revokedBy, revocationReason)
// are response-only: the emergency-revocation endpoints own the
// lifecycle transition, and a PUT that round-trips a credential
// preserves its persisted revocation state by id rather than reading it
// from the wire.
type CredentialEntryPayload struct {
	ID               string `json:"id"`
	SecretRef        string `json:"secretRef,omitempty"`
	RoleArn          string `json:"roleArn,omitempty"`
	Region           string `json:"region,omitempty"`
	Status           string `json:"status,omitempty"`
	RevokedAt        string `json:"revokedAt,omitempty"`
	RevokedBy        string `json:"revokedBy,omitempty"`
	RevocationReason string `json:"revocationReason,omitempty"`
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

// writeProxyEndpointError writes the §4.9 422 INVALID_POOL_PROXY_ENDPOINT
// response when err is the store's proxy-endpoint scheme rejection, and
// reports whether it handled the error. The §4.9 proxy-mode guarantee
// (the real API key never leaves the gateway) requires the lease token
// to travel encrypted, so a plaintext `http://` endpoint is rejected.
//
// spec: §4.9 line 1513 — the controller rejects an http:// proxyEndpoint
// (InvalidProxyEndpointScheme).
func writeProxyEndpointError(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, credentialpoolstore.ErrInvalidProxyEndpointScheme) {
		return false
	}
	writeError(w, http.StatusUnprocessableEntity, "INVALID_POOL_PROXY_ENDPOINT",
		"proxyEndpoint must use the https:// scheme", map[string]any{"field": "proxyEndpoint"})
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
		DeliveryMode:               p.DeliveryMode,
		ProxyDialect:               p.ProxyDialect,
		ProxyEndpoint:              p.ProxyEndpoint,
		CacheScope:                 p.CacheScope,
		CreatedAt:                  rfc3339Nano(p.CreatedAt),
		UpdatedAt:                  rfc3339Nano(p.UpdatedAt),
		DeletedAt:                  rfc3339Nano(p.DeletedAt),
	}
	for _, c := range p.Credentials {
		entry := CredentialEntryPayload{
			ID: c.ID, SecretRef: c.SecretRef, RoleArn: c.RoleArn, Region: c.Region,
			Status: string(c.Status), RevokedBy: c.RevokedBy, RevocationReason: c.RevocationReason,
		}
		if !c.RevokedAt.IsZero() {
			entry.RevokedAt = rfc3339Nano(c.RevokedAt)
		}
		out.Credentials = append(out.Credentials, entry)
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
		DeliveryMode:               body.DeliveryMode,
		ProxyDialect:               body.ProxyDialect,
		ProxyEndpoint:              body.ProxyEndpoint,
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
		if writeProxyEndpointError(w, err) {
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
		p.Credentials = preserveRevocations(p.Credentials, toCredentials(body.Credentials))
		p.AssignmentStrategy = body.AssignmentStrategy
		p.MaxConcurrentSessions = body.MaxConcurrentSessions
		p.CooldownOnRateLimitSeconds = body.CooldownOnRateLimitSeconds
		p.LeaseTTLSeconds = body.LeaseTTLSeconds
		p.RenewBeforeBufferSeconds = body.RenewBeforeBufferSeconds
		p.HostPatterns = body.HostPatterns
		p.DeliveryMode = body.DeliveryMode
		p.ProxyDialect = body.ProxyDialect
		p.ProxyEndpoint = body.ProxyEndpoint
		p.CacheScope = body.CacheScope
		return nil
	})
	if err != nil {
		if errors.Is(err, credentialpoolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
			return
		}
		if writeProxyEndpointError(w, err) {
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

// PoolCredentialRevoker terminates the §4.9 credential leases backed by
// one or more pool credentials. The emergency-revocation handlers
// resolve the (poolID, credentialID) of each credential they revoke and
// call this to add the credential's source-aware identity to the §4.9
// deny list (propagated across replicas) and drop the leases this
// replica holds, returning the count of leases it terminated.
//
// A minimal gateway leaves it nil; the revoke still marks the store and
// emits the §4.9.2 audit event, and the §4.9 startup deny-list rebuild
// seeds the revoked credential onto a replica's deny list at the next
// restart, so the credential is still rejected on the upstream path.
//
// spec: §4.9 lines 1640-1652 — mark the credential revoked, look up all
// active leases backed by it, terminate them, and add the credential to
// the in-memory deny list propagated across replicas.
type PoolCredentialRevoker interface {
	// RevokePoolCredentials revokes every credentialID in poolID: it adds
	// each credential to the deny list and removes the leases backed by
	// it, returning the total leases terminated across all credentialIDs.
	RevokePoolCredentials(ctx context.Context, poolID string, credentialIDs []string) int
}

// WithPoolCredentialRevocation wires the §4.9 emergency-revocation lease
// terminator onto the Router. With it set, the revoke endpoints add a
// revoked credential to the deny list and drop its active leases; the
// returned `leasesTerminated` count reflects the leases this replica
// held. Without it the revoke endpoints still mark the store and emit
// the audit event, reporting zero leases terminated on this replica.
func (r *Router) WithPoolCredentialRevocation(rev PoolCredentialRevoker) *Router {
	r.poolCredRevoker = rev
	return r
}

// errCredentialNotFound is the mutate sentinel for a credential id that
// is absent from the pool. The revoke/re-enable handlers map it to a
// 404 RESOURCE_NOT_FOUND.
var errCredentialNotFound = errors.New("credential not found")

// revokeRequest is the optional §4.9 revocation body. Both fields are
// optional; the spec example carries a reason and a free-text note.
type revokeRequest struct {
	Reason string `json:"reason,omitempty"`
	Note   string `json:"note,omitempty"`
}

// decodeOptionalRevokeBody reads the optional {reason, note} body. An
// empty body is permitted (the spec marks the body optional). A
// malformed non-empty body is reported so the caller can 400.
func decodeOptionalRevokeBody(req *http.Request) (revokeRequest, error) {
	var body revokeRequest
	if req.Body == nil {
		return body, nil
	}
	dec := json.NewDecoder(req.Body)
	if err := dec.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return revokeRequest{}, nil
		}
		return revokeRequest{}, err
	}
	return body, nil
}

// preserveRevocations carries forward per-credential revocation state
// from the existing pool onto the incoming credential set, matched by
// id. A PUT replaces the credential set wholesale (§15.1), but the
// emergency-revocation lifecycle (§4.9) is owned by the dedicated
// revoke/re-enable endpoints, so a PUT that round-trips a revoked
// credential must not silently re-enable it.
func preserveRevocations(existing, incoming []credentialpoolstore.Credential) []credentialpoolstore.Credential {
	prior := make(map[string]credentialpoolstore.Credential, len(existing))
	for _, c := range existing {
		prior[c.ID] = c
	}
	for i := range incoming {
		if p, ok := prior[incoming[i].ID]; ok && p.IsRevoked() {
			incoming[i].Status = p.Status
			incoming[i].RevokedAt = p.RevokedAt
			incoming[i].RevokedBy = p.RevokedBy
			incoming[i].RevocationReason = p.RevocationReason
		}
	}
	return incoming
}

// handleRevokeCredential implements the §4.9 single-credential emergency
// revocation: POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke.
//
// spec: §4.9 lines 1626-1652 — mark the credential revoked in the store,
// terminate its active leases via the deny list, emit credential.revoked,
// and return a 200 summary.
func (r *Router) handleRevokeCredential(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	credID := req.PathValue("credId")
	body, err := decodeOptionalRevokeBody(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	at := r.clock()
	if _, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		return revokeCredentialInPool(p, credID, principal.Subject, body.Reason, at)
	}); err != nil {
		r.writeCredentialMutateError(w, err)
		return
	}
	terminated := 0
	if r.poolCredRevoker != nil {
		terminated = r.poolCredRevoker.RevokePoolCredentials(req.Context(), name, []string{credID})
	}
	r.emit(req.Context(), principal, "credential.revoked", name+"/"+credID, map[string]any{
		"tenant_id":                tenant,
		"pool_id":                  name,
		"credential_id":            credID,
		"revoked_by":               principal.Subject,
		"reason":                   body.Reason,
		"active_leases_terminated": terminated,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"revokedCredential": credID,
		"leasesTerminated":  terminated,
		"propagatedAt":      rfc3339Nano(at),
	})
}

// handleRevokePool implements the §4.9 pool-wide force-rotate:
// POST /v1/admin/credential-pools/{name}/revoke. It revokes every
// credential in the pool simultaneously.
//
// spec: §4.9 lines 1654-1659 — revoke all credentials in the pool; all
// active sessions are rotated to their fallback chain, or terminated
// with CREDENTIAL_POOL_EXHAUSTED if no fallback is available.
func (r *Router) handleRevokePool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	body, err := decodeOptionalRevokeBody(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	at := r.clock()
	var revoked []string
	if _, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		revoked = revoked[:0]
		for i := range p.Credentials {
			if p.Credentials[i].IsRevoked() {
				continue
			}
			p.Credentials[i].Status = credentialpoolstore.CredentialRevoked
			p.Credentials[i].RevokedAt = at
			p.Credentials[i].RevokedBy = principal.Subject
			p.Credentials[i].RevocationReason = body.Reason
			revoked = append(revoked, p.Credentials[i].ID)
		}
		return nil
	}); err != nil {
		r.writeCredentialMutateError(w, err)
		return
	}
	terminated := 0
	if r.poolCredRevoker != nil && len(revoked) > 0 {
		terminated = r.poolCredRevoker.RevokePoolCredentials(req.Context(), name, revoked)
	}
	for _, credID := range revoked {
		r.emit(req.Context(), principal, "credential.revoked", name+"/"+credID, map[string]any{
			"tenant_id":     tenant,
			"pool_id":       name,
			"credential_id": credID,
			"revoked_by":    principal.Subject,
			"reason":        body.Reason,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"revokedCredentials": revoked,
		"leasesTerminated":   terminated,
		"propagatedAt":       rfc3339Nano(at),
	})
}

// handleReEnableCredential implements the §4.9 re-enable path:
// POST /v1/admin/credential-pools/{name}/credentials/{credId}/re-enable.
// It returns a revoked credential to active so the pool can assign it
// again after the operator has rotated the underlying secret (runbook
// step 6). The persisted status flip is authoritative: a new lease
// minted after re-enable carries a fresh token, and the §4.9 startup
// rebuild omits the re-enabled credential so a restarted replica no
// longer denies it. A replica that was already running keeps the live
// deny-list entry until it restarts or the entry reaches its lease-TTL
// expiry, so an operator re-enabling a credential on a long-running
// fleet should roll the gateway replicas to clear the live entries.
//
// spec: §4.9 line 1743 (credential.re_enabled), lines 1675-1677 (runbook
// step 6 — rotate the underlying secret before re-enabling).
func (r *Router) handleReEnableCredential(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	credID := req.PathValue("credId")
	body, err := decodeOptionalRevokeBody(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	at := r.clock()
	if _, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		idx := credentialIndex(p, credID)
		if idx < 0 {
			return errCredentialNotFound
		}
		p.Credentials[idx].Status = credentialpoolstore.CredentialActive
		p.Credentials[idx].RevokedAt = time.Time{}
		p.Credentials[idx].RevokedBy = ""
		p.Credentials[idx].RevocationReason = ""
		return nil
	}); err != nil {
		r.writeCredentialMutateError(w, err)
		return
	}
	r.emit(req.Context(), principal, "credential.re_enabled", name+"/"+credID, map[string]any{
		"tenant_id":     tenant,
		"pool_id":       name,
		"credential_id": credID,
		"reason":        body.Reason,
		"re_enabled_by": principal.Subject,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"reEnabledCredential": credID,
		"reEnabledAt":         rfc3339Nano(at),
	})
}

// credentialIndex returns the index of the credential with id in the
// pool, or -1 when absent.
func credentialIndex(p *credentialpoolstore.CredentialPool, id string) int {
	for i := range p.Credentials {
		if p.Credentials[i].ID == id {
			return i
		}
	}
	return -1
}

// revokeCredentialInPool flips one credential to revoked inside an
// Update mutate. A credential already revoked is left unchanged so a
// repeated revoke is idempotent and preserves the original revoker and
// timestamp. An absent credential id yields errCredentialNotFound.
func revokeCredentialInPool(p *credentialpoolstore.CredentialPool, credID, revokedBy, reason string, at time.Time) error {
	idx := credentialIndex(p, credID)
	if idx < 0 {
		return errCredentialNotFound
	}
	if p.Credentials[idx].IsRevoked() {
		return nil
	}
	p.Credentials[idx].Status = credentialpoolstore.CredentialRevoked
	p.Credentials[idx].RevokedAt = at
	p.Credentials[idx].RevokedBy = revokedBy
	p.Credentials[idx].RevocationReason = reason
	return nil
}

// writeCredentialMutateError maps the shared revoke/re-enable mutate
// failures to HTTP responses: an unknown pool or credential is 404, any
// other failure is a 400 validation error.
func (r *Router) writeCredentialMutateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, credentialpoolstore.ErrNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
	case errors.Is(err, errCredentialNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential not found in pool", nil)
	default:
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
	}
}
