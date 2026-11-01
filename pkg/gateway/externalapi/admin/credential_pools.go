// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
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
	CachePolicy                *CachePolicyPayload      `json:"cachePolicy,omitempty"`
	CreatedAt                  string                   `json:"createdAt,omitempty"`
	UpdatedAt                  string                   `json:"updatedAt,omitempty"`
	DeletedAt                  string                   `json:"deletedAt,omitempty"`

	// ETag is the §15.1 optimistic-concurrency entity tag — the quoted
	// decimal version. List and GET responses carry it so a client can
	// supply it as the If-Match header on a later PUT.
	// spec: §15.1.
	ETag string `json:"etag,omitempty"`
}

// CachePolicyPayload is the §4.9 semantic-cache configuration on a pool. It is absent when the pool declares no
// cachePolicy; §4.9 caching is disabled by default and opt-in per pool.
type CachePolicyPayload struct {
	Enabled             bool    `json:"enabled"`
	Strategy            string  `json:"strategy,omitempty"`
	TTLSeconds          int     `json:"ttl,omitempty"`
	SimilarityThreshold float64 `json:"similarityThreshold,omitempty"`
	Backend             string  `json:"backend,omitempty"`
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

	// Health and LeaseCount are the §24.5 row-2 per-credential runtime
	// signals surfaced on GET. They are populated only when a
	// PoolCredentialHealthReader is wired (the gateway holds the
	// credential-lease store); a minimal gateway omits them. Health is
	// "revoked" for a revoked credential and "healthy" otherwise;
	// LeaseCount is the credential's active lease count read from the
	// lease store. spec: §24.5.
	Health     string `json:"health,omitempty"`
	LeaseCount *int   `json:"leaseCount,omitempty"`
}

// CredentialEntryRequest is the request form of one credential entry. It
// adds the request-only secretData field, which no response type carries,
// so a submitted value can never be written back.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 1 and 7.
type CredentialEntryRequest struct {
	CredentialEntryPayload
	SecretData map[string]string `json:"secretData,omitempty"`
}

// CredentialPoolRequest is the request form of a pool on pool creation,
// pool update, and the bootstrap seed. Its entries decode secretData so
// that rule 1 can refuse it; a decode into CredentialEntryPayload would
// drop the field silently.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 1.
type CredentialPoolRequest struct {
	CredentialPoolPayload
	Credentials []CredentialEntryRequest `json:"credentials,omitempty"`
}

// poolPayload returns the pool with its entries in payload form, or the
// rule 1 refusal when any entry carries secretData.
func (p CredentialPoolRequest) poolPayload() (CredentialPoolPayload, error) {
	out := p.CredentialPoolPayload
	out.Credentials = nil
	if p.Credentials != nil {
		out.Credentials = make([]CredentialEntryPayload, 0, len(p.Credentials))
	}
	for _, e := range p.Credentials {
		if e.SecretData != nil {
			return CredentialPoolPayload{}, errSecretDataOnPool
		}
		out.Credentials = append(out.Credentials, e.CredentialEntryPayload)
	}
	return out, nil
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
// spec: §4.9 — `cacheScope: tenant` on a pool whose
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

// validatePoolProxyDialect enforces the §4.9 proxy-dialect
// admission boundary at pool registration/update. A credential pool
// carries no static runtime binding, so "the Runtime's
// credentialCapabilities.proxyDialect set" is resolved as the set of
// agent runtimes whose supportedProviders includes the pool's provider.
// The pool is rejected with 422 INVALID_POOL_PROXY_DIALECT only when at
// least one such runtime exists and none of them declares the pool's
// dialect (a pool no runtime can speak). When no agent runtime
// references the provider yet, the check is deferred to the
// session-creation runtime↔pool join (sessionserver.resolveCredentialPools),
// which enforces the same boundary per-runtime before a pod is claimed.
// A direct-mode pool (empty proxyDialect) and an unwired runtime
// registry skip the check. It reports whether the request may proceed;
// on rejection it has already written the response.
func (r *Router) validatePoolProxyDialect(w http.ResponseWriter, req *http.Request, provider, dialect string) bool {
	if dialect == "" || provider == "" || r.runtimes == nil {
		return true
	}
	rts, err := r.runtimes.List(req.Context(), runtimestore.ListFilter{Type: runtimestore.TypeAgent})
	if err != nil {
		// A registry read failure must not block pool admin; the
		// session-creation join still enforces the boundary fail-closed.
		return true
	}
	var supports, declares bool
	for _, rt := range rts {
		matched := false
		for _, p := range rt.SupportedProviders {
			if p == provider {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		supports = true
		if rt.CredentialCapabilities.AllowsProxyDialect(dialect) {
			declares = true
			break
		}
	}
	if supports && !declares {
		writeError(w, http.StatusUnprocessableEntity, "INVALID_POOL_PROXY_DIALECT",
			fmt.Sprintf("pool proxyDialect %s is not declared in runtime credentialCapabilities.proxyDialect", dialect),
			map[string]any{"provider": provider, "proxyDialect": dialect})
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
// spec: §4.9 — the controller rejects an http:// proxyEndpoint
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
		CachePolicy:                fromCachePolicy(p.CachePolicy),
		CreatedAt:                  rfc3339Nano(p.CreatedAt),
		UpdatedAt:                  rfc3339Nano(p.UpdatedAt),
		DeletedAt:                  rfc3339Nano(p.DeletedAt),
		// spec: §15.1 — the ETag is the quoted decimal version,
		// carried per-item on list responses and in the GET header.
		ETag: formatETag(p.Version),
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

// toCachePolicy maps the §4.9 wire cachePolicy to the store
// representation. A nil payload maps to a nil policy (caching off).
func toCachePolicy(in *CachePolicyPayload) *credentialpoolstore.CachePolicy {
	if in == nil {
		return nil
	}
	return &credentialpoolstore.CachePolicy{
		Enabled:             in.Enabled,
		Strategy:            in.Strategy,
		TTLSeconds:          in.TTLSeconds,
		SimilarityThreshold: in.SimilarityThreshold,
		Backend:             in.Backend,
	}
}

// fromCachePolicy maps a stored §4.9 cachePolicy to the wire payload. A
// nil policy maps to an absent payload.
func fromCachePolicy(in *credentialpoolstore.CachePolicy) *CachePolicyPayload {
	if in == nil {
		return nil
	}
	return &CachePolicyPayload{
		Enabled:             in.Enabled,
		Strategy:            in.Strategy,
		TTLSeconds:          in.TTLSeconds,
		SimilarityThreshold: in.SimilarityThreshold,
		Backend:             in.Backend,
	}
}

// WithCredentialPools wires the §15.1 credential-pool CRUD handlers
// onto the Router.
func (r *Router) WithCredentialPools(s credentialpoolstore.Store) *Router {
	r.credentialPools = s
	return r
}

// SecretProbeVerdict is the §4.9 admin-time RBAC live-probe outcome for
// a single secretRef. spec: §4.9.
type SecretProbeVerdict int

const (
	// SecretProbeAllowed — the Token Service can read the Secret.
	SecretProbeAllowed SecretProbeVerdict = iota
	// SecretProbeDenied — the Token Service Get returned Forbidden (the
	// chart Role is absent).
	SecretProbeDenied
	// SecretProbeNotFound — the Secret is absent from the credentials
	// namespace.
	SecretProbeNotFound
)

// SecretAccessProber runs the §4.9 admin-time Secret probe against the
// Token Service over the gateway↔Token-Service mTLS link. secretRef is the
// canonical "<credentials namespace>/<name>" ref; the gateway
// implementation sends the Token Service the Secret name alone in an
// unkeyed ProbeSecretAccess call, and the Token Service performs one Get in
// the credentials namespace. A non-nil error is an indeterminate probe
// (Token Service unreachable, mTLS failure, Kubernetes API timeout) that
// the handler maps to 503 CREDENTIAL_PROBE_UNAVAILABLE and never fails
// open.
//
// spec: §4.9 (Credential Storage Topology) — Admin-time Secret probe
// (required).
type SecretAccessProber interface {
	ProbeSecretAccess(ctx context.Context, secretRef string) (SecretProbeVerdict, error)
}

// WithSecretAccessProber wires the §4.9 admin-time Secret probe onto the
// Router. With it set, every admin write that introduces a secretRef
// probes each introduced ref before the write commits, rejecting an
// unreadable ref with 422 CREDENTIAL_SECRET_RBAC_MISSING (or 503
// CREDENTIAL_PROBE_UNAVAILABLE when the probe cannot be evaluated).
// Without it no probe runs, which is the §4.9 posture when no Token
// Service is configured (`--token-service-grpc-addr` empty).
//
// spec: §4.9 (Credential Storage Topology) — Admin-time Secret probe
// (required).
func (r *Router) WithSecretAccessProber(p SecretAccessProber) *Router {
	r.secretProber = p
	return r
}

// handleCreateCredentialPool implements POST /v1/admin/credential-pools.
//
// Every credentials[].secretRef is parsed and stored canonical, and a new
// pool introduces each of them, so the registration rule, the reserved
// prefix, and (outside dryRun) the admin-time Secret probe run over the
// full set before the pool is created.
//
// spec: §4.9 (Credential Storage Topology); §15.1.
func (r *Router) handleCreateCredentialPool(w http.ResponseWriter, req *http.Request) {
	var in CredentialPoolRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, in.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if !validAssignmentStrategies[in.AssignmentStrategy] {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"assignmentStrategy must be least-loaded, round-robin, or sticky-until-failure",
			map[string]any{"field": "assignmentStrategy"})
		return
	}
	if !r.validateCacheScope(w, req, tenant, in.CacheScope) {
		return
	}
	// spec: §4.9 — reject a proxy-mode pool whose proxyDialect
	// no agent runtime serving its provider can speak.
	if !r.validatePoolProxyDialect(w, req, in.Provider, in.ProxyDialect) {
		return
	}
	// spec: §4.9 (Credential values submitted through the admin API) rule
	// 1 — pool creation refuses secretData.
	body, err := in.poolPayload()
	if err != nil {
		writeAdminWriteError(w, err)
		return
	}
	dryRun := req.URL.Query().Get("dryRun") == "true"
	if err := r.canonicalizeSecretRefs(body.Credentials); err != nil {
		writeAdminWriteError(w, err)
		return
	}
	// spec: §4.9 — a new pool carries no ref, so every submitted ref is
	// introduced. A dryRun request makes no probe call.
	if err := r.checkSecretRefWrite(req.Context(), nil, body.Credentials, !dryRun); err != nil {
		writeAdminWriteError(w, err)
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
		CachePolicy:                toCachePolicy(body.CachePolicy),
		CreatedAt:                  r.clock(),
	}
	pool.UpdatedAt = pool.CreatedAt
	// spec: §15.1 — ?dryRun=true validates without persisting or auditing.
	if dryRun {
		if verr := credentialpoolstore.Validate(pool); verr != nil {
			if writeProxyEndpointError(w, verr) {
				return
			}
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", verr.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusCreated, fromCredentialPool(pool))
		return
	}
	if err := r.credentialPools.Create(req.Context(), pool); err != nil {
		if errors.Is(err, credentialpoolstore.ErrAlreadyExists) {
			// spec: §15.1 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
			writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS",
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
	// spec: §15.1 — canonical cursor-paginated envelope. F-15.1.6.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(x CredentialPoolPayload, s pagination.Sort) (string, string) {
			switch s.Field {
			case "name":
				return x.Name, x.Name
			case "updated_at":
				return x.UpdatedAt, x.Name
			default:
				return x.CreatedAt, x.Name
			}
		})
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
	payload := fromCredentialPool(row)
	r.enrichCredentialHealth(&payload, row)
	// spec: §15.1 — GET responses for an admin resource carry the
	// ETag header so the client can use it as the next PUT's If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// enrichCredentialHealth fills the §24.5 row-2 per-credential health and
// lease-count fields on the GET payload. Health derives from the
// persisted credential status (the admin store's authoritative
// revocation state); lease counts come from the wired
// PoolCredentialHealthReader (the credential-lease store). With no
// reader wired only the status-derived health is set, since the admin
// store carries no runtime lease state. spec: §24.5.
func (r *Router) enrichCredentialHealth(payload *CredentialPoolPayload, row credentialpoolstore.CredentialPool) {
	var counts map[string]int
	if r.poolCredHealth != nil {
		ids := make([]string, 0, len(row.Credentials))
		for _, c := range row.Credentials {
			ids = append(ids, c.ID)
		}
		counts = r.poolCredHealth.PoolCredentialLeaseCounts(row.Name, ids)
	}
	for i := range payload.Credentials {
		c := &payload.Credentials[i]
		if c.Status == string(credentialpoolstore.CredentialRevoked) {
			c.Health = "revoked"
		} else {
			c.Health = "healthy"
		}
		if counts != nil {
			n := counts[c.ID]
			c.LeaseCount = &n
		}
	}
}

// applyCredentialPoolUpdate merges a §15.1 CredentialPoolPayload onto a
// credential pool in place — a full replace of the mutable fields — while
// carrying forward the §4.9 per-credential revocation state by id. It is
// the single merge implementation shared by the real store Update closure
// and the dry-run preview.
func applyCredentialPoolUpdate(p *credentialpoolstore.CredentialPool, body CredentialPoolPayload) {
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
	p.CachePolicy = toCachePolicy(body.CachePolicy)
}

// handleUpdateCredentialPool implements PUT — a full replace of the
// mutable fields (provider, credentials, strategy, limits, host
// patterns).
func (r *Router) handleUpdateCredentialPool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var in CredentialPoolRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, in.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if !validAssignmentStrategies[in.AssignmentStrategy] {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"assignmentStrategy must be least-loaded, round-robin, or sticky-until-failure",
			map[string]any{"field": "assignmentStrategy"})
		return
	}
	if !r.validateCacheScope(w, req, tenant, in.CacheScope) {
		return
	}
	// spec: §4.9 — PUT is a full replace, so the body carries
	// the effective provider/proxyDialect; reject when the updated
	// proxy-mode pool declares a dialect no agent runtime serving its
	// provider can speak.
	if !r.validatePoolProxyDialect(w, req, in.Provider, in.ProxyDialect) {
		return
	}
	// spec: §4.9 (Credential values submitted through the admin API) rule
	// 1 — pool update refuses secretData.
	body, err := in.poolPayload()
	if err != nil {
		writeAdminWriteError(w, err)
		return
	}
	if err := r.canonicalizeSecretRefs(body.Credentials); err != nil {
		writeAdminWriteError(w, err)
		return
	}
	if req.URL.Query().Get("dryRun") == "true" {
		r.previewCredentialPoolUpdate(w, req, tenant, name, body)
		return
	}
	// spec: §15.1 — every admin PUT requires If-Match. The presence and
	// format checks run here, ahead of any store read, so a PUT with no
	// If-Match against a missing pool receives 428 ahead of 404.
	want, ok := requireIfMatch(w, req)
	if !ok {
		return
	}
	// spec: §4.9 — the version compare, the registration rule, the
	// reserved prefix, and the probe run against the row the store holds
	// locked, so a concurrent write between a read and the commit cannot
	// change what they decide. The Delete of each dropped Lenny-written
	// ref runs last, after the explicit Validate, under the same lock.
	ops := &secretOps{}
	updated, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		ops.reset()
		if err := compareIfMatch(want, p.Version); err != nil {
			return err
		}
		if err := r.checkSecretRefWrite(req.Context(), p.Credentials, body.Credentials, true); err != nil {
			return err
		}
		if err := r.planMutation(p, ops, func(p *credentialpoolstore.CredentialPool) { applyCredentialPoolUpdate(p, body) }); err != nil {
			return err
		}
		return r.applySecretOps(req.Context(), ops)
	})
	if err != nil {
		if writeAfterSecretOp(w, err, ops) {
			return
		}
		r.writeCredentialPoolUpdateError(w, err)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.updated", name,
		map[string]any{"tenantId": tenant})
	// spec: §15.1 — a successful PUT carries the bumped ETag so the
	// client can chain a subsequent write without a refresh GET.
	w.Header().Set("ETag", formatETag(updated.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCredentialPool(updated))
}

// previewCredentialPoolUpdate serves a dryRun pool PUT: it reads the pool
// without a lock (a missing pool is 404), applies the If-Match
// precondition against it, runs the registration and reserved-prefix
// checks against its credentials with no probe call, validates the
// preview, refuses it as the write would be refused when it drops a
// Lenny-written ref and no writer is wired, and returns it without
// persisting, auditing, or performing a Secret operation.
//
// spec: §15.1 (dryRun); §4.9 (Credential Storage Topology).
func (r *Router) previewCredentialPoolUpdate(w http.ResponseWriter, req *http.Request, tenant, name string, body CredentialPoolPayload) {
	current, ok := r.readPoolForPreview(w, req, tenant, name)
	if !ok {
		return
	}
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	if err := r.checkSecretRefWrite(req.Context(), current.Credentials, body.Credentials, false); err != nil {
		writeAdminWriteError(w, err)
		return
	}
	preview := current
	preview.TenantID = tenant
	ops := &secretOps{}
	if err := r.planMutation(&preview, ops, func(p *credentialpoolstore.CredentialPool) { applyCredentialPoolUpdate(p, body) }); err != nil {
		r.writeCredentialPoolUpdateError(w, err)
		return
	}
	if err := r.checkWriterFor(ops); err != nil {
		writeAdminWriteError(w, err)
		return
	}
	writeDryRun(w, http.StatusOK, fromCredentialPool(preview))
}

// readPoolForPreview reads a pool without a lock for a dryRun request. A
// missing pool is 404 and any other read failure is 500; on either it has
// written the response and reports false.
func (r *Router) readPoolForPreview(w http.ResponseWriter, req *http.Request, tenant, name string) (credentialpoolstore.CredentialPool, bool) {
	current, err := r.credentialPools.Get(req.Context(), tenant, name)
	if err == nil {
		return current, true
	}
	if errors.Is(err, credentialpoolstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
		return credentialpoolstore.CredentialPool{}, false
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	return credentialpoolstore.CredentialPool{}, false
}

// writeCredentialPoolUpdateError maps a pool PUT failure: a missing pool is
// 404, a check refusal is its own response, a plaintext proxy endpoint is
// 422 INVALID_POOL_PROXY_ENDPOINT, and any other failure is the store's
// validation refusal, 400.
func (r *Router) writeCredentialPoolUpdateError(w http.ResponseWriter, err error) {
	if errors.Is(err, credentialpoolstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
		return
	}
	if writeAdminWriteError(w, err) || writeProxyEndpointError(w, err) {
		return
	}
	writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
}

// errPoolAlreadyDeleted is the pool DELETE row guard for a pool that is
// already soft-deleted. The handler answers 204 with the row unchanged and
// no Secret operation.
var errPoolAlreadyDeleted = errors.New("credential pool is already soft-deleted")

// handleDeleteCredentialPool implements DELETE
// /v1/admin/credential-pools/{name}. The If-Match compare (when the header
// is present), the soft-deleted guard, the soft delete, and the Delete of
// every Lenny-written Secret the pool references run as one operation
// under the pool row lock, the Deletes last. A pool that is already
// soft-deleted answers 204 with no Secret operation.
//
// spec: §15.1; §4.9 (Credential Storage Topology) — Credential values
// submitted through the admin API rules 3, 4, and 6.
func (r *Router) handleDeleteCredentialPool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// spec: §15.1 — DELETE honours If-Match only when present: a
	// stale tag returns 412 ETAG_MISMATCH, an absent header proceeds.
	want, hasIfMatch, ok := optionalIfMatch(w, req)
	if !ok {
		return
	}
	ops := &secretOps{}
	_, err = r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		ops.reset()
		if hasIfMatch {
			if err := compareIfMatch(want, p.Version); err != nil {
				return err
			}
		}
		return r.softDeleteLocked(req.Context(), p, ops)
	})
	if err != nil && !errors.Is(err, errPoolAlreadyDeleted) {
		r.writeCredentialPoolDeleteError(w, err, ops)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.soft_deleted", name,
		map[string]any{"tenantId": tenant})
	w.WriteHeader(http.StatusNoContent)
}

// softDeleteLocked runs the pool DELETE against the locked row p: the
// soft-deleted guard, the soft delete, the explicit Validate, and the
// Delete of each Lenny-written ref the pool carries.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 3 and 4.
func (r *Router) softDeleteLocked(ctx context.Context, p *credentialpoolstore.CredentialPool, ops *secretOps) error {
	if !p.IsActive() {
		return errPoolAlreadyDeleted
	}
	p.DeletedAt = r.clock()
	if err := credentialpoolstore.Validate(*p); err != nil {
		return err
	}
	ops.deletes = droppedSecretNames(r.managedRefs(p.Credentials), nil)
	return r.applySecretOps(ctx, ops)
}

// writeCredentialPoolDeleteError maps a pool DELETE failure: a failure
// after a writer call is 503, a missing pool is 404, a check refusal (the
// If-Match 412, the no-writer 422) is its own response, and any other
// failure is 500.
func (r *Router) writeCredentialPoolDeleteError(w http.ResponseWriter, err error, ops *secretOps) {
	if writeAfterSecretOp(w, err, ops) || writeAdminWriteError(w, err) {
		return
	}
	if errors.Is(err, credentialpoolstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}

// errCredentialExists is the mutate sentinel for an add-credential whose
// id is already present in the pool. handleAddCredential maps it to a
// 409 RESOURCE_ALREADY_EXISTS.
var errCredentialExists = errors.New("credential id already exists in pool")

// handleAddCredential implements POST
// /v1/admin/credential-pools/{name}/credentials — the §24.5 row-3
// add-credential operation. The body is a single credential entry carrying
// at most one of secretRef, secretData, and roleArn. A secretRef is stored
// canonical, and inside the mutate closure the active check, the id check,
// the registration rule, the reserved prefix, and the admin-time Secret
// probe run against the locked row before the entry is appended. An entry
// sent with secretData points at its Lenny-written Secret, which the
// writer Puts as the last step before the row commits.
//
// spec: §15.1; §24.5 row 3; §4.9 (Credential Storage Topology).
func (r *Router) handleAddCredential(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	in, tenant, ok := r.decodeCredentialEntry(w, req)
	if !ok {
		return
	}
	if in.ID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "credential id is required",
			map[string]any{"field": "id"})
		return
	}
	body, ok := r.checkCredentialEntry(w, in)
	if !ok {
		return
	}
	entries := []CredentialEntryPayload{body}
	plan := func(ctx context.Context, p *credentialpoolstore.CredentialPool, ops *secretOps, probe bool) error {
		if err := addCredentialGuard(p, body.ID); err != nil {
			return err
		}
		// spec: §4.9 — registration, reserved prefix, and probe against
		// the row the closure holds.
		if err := r.checkSecretRefWrite(ctx, p.Credentials, entries, probe); err != nil {
			return err
		}
		return r.planMutation(p, ops, func(p *credentialpoolstore.CredentialPool) {
			c := credentialpoolstore.Credential{ID: body.ID, SecretRef: body.SecretRef, RoleArn: body.RoleArn, Region: body.Region}
			r.planEntryPut(ops, &c, tenant, name, in.SecretData)
			p.Credentials = append(p.Credentials, c)
		})
	}
	if req.URL.Query().Get("dryRun") == "true" {
		r.previewCredentialWrite(w, req, tenant, name, http.StatusCreated, plan)
		return
	}
	updated, ok := r.commitCredentialWrite(w, req, tenant, name, plan)
	if !ok {
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.credential_added", name+"/"+body.ID,
		credentialAuditPayload(tenant, name, body.ID, updated))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromCredentialPool(updated))
}

// handleUpdateCredentialEntry implements PUT
// /v1/admin/credential-pools/{name}/credentials/{credId} — the §24.5
// row-4 update-credential operation. It replaces the addressed
// credential's mutable fields (secretRef, roleArn, region) in place,
// preserving its §4.9 revocation status (an update is not a re-enable).
// The secretRef is stored canonical. When it is a ref the pool does not
// carry, the registration rule and the admin-time Secret probe run over it
// against the locked row, and the reserved prefix is checked against the
// addressed credential. A body with secretData Puts the entry's
// Lenny-written Secret, and a body that moves the entry off its
// Lenny-written Secret Deletes it.
//
// spec: §15.1; §24.5 row 4; §4.9 (Credential Storage Topology).
func (r *Router) handleUpdateCredentialEntry(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	credID := req.PathValue("credId")
	in, tenant, ok := r.decodeCredentialEntry(w, req)
	if !ok {
		return
	}
	in.ID = credID
	body, ok := r.checkCredentialEntry(w, in)
	if !ok {
		return
	}
	entries := []CredentialEntryPayload{body}
	plan := func(ctx context.Context, p *credentialpoolstore.CredentialPool, ops *secretOps, probe bool) error {
		idx, err := updateCredentialGuard(p, credID)
		if err != nil {
			return err
		}
		// spec: §4.9 — an unchanged ref is carried by the pool and so is
		// not introduced; a changed one is checked and probed against the
		// row the closure holds.
		if err := r.checkSecretRefWrite(ctx, p.Credentials, entries, probe); err != nil {
			return err
		}
		return r.planMutation(p, ops, func(p *credentialpoolstore.CredentialPool) {
			c := &p.Credentials[idx]
			c.SecretRef = body.SecretRef
			c.RoleArn = body.RoleArn
			c.Region = body.Region
			r.planEntryPut(ops, c, tenant, name, in.SecretData)
		})
	}
	if req.URL.Query().Get("dryRun") == "true" {
		r.previewCredentialWrite(w, req, tenant, name, http.StatusOK, plan)
		return
	}
	updated, ok := r.commitCredentialWrite(w, req, tenant, name, plan)
	if !ok {
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.credential_updated", name+"/"+credID,
		credentialAuditPayload(tenant, name, credID, updated))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCredentialPool(updated))
}

// handleRemoveCredential implements DELETE
// /v1/admin/credential-pools/{name}/credentials/{credId} — the §24.5
// row-5 remove-credential operation. The credential is dropped from the
// pool, so no new lease selects it. Per §15.1 the active
// leases backed by it are rotated via the standard §4.9 fallback path
// (the renewal path can no longer resolve the removed credential and
// re-acquires from the remaining pool credentials); removal does not add
// the credential to the deny list, which is reserved for emergency
// revocation (CREDENTIAL_REVOKED hard-reject). A removed entry that
// referenced a Lenny-written Secret has that Secret deleted under the row
// lock as the last step before the row commits.
//
// spec: §15.1; §24.5 row 5; §4.9 (Credential Storage Topology).
func (r *Router) handleRemoveCredential(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	credID := req.PathValue("credId")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	updated, ok := r.commitCredentialWrite(w, req, tenant, name, func(_ context.Context, p *credentialpoolstore.CredentialPool, ops *secretOps, _ bool) error {
		idx := credentialIndex(p, credID)
		if idx < 0 {
			return errCredentialNotFound
		}
		return r.planMutation(p, ops, func(p *credentialpoolstore.CredentialPool) {
			p.Credentials = append(p.Credentials[:idx], p.Credentials[idx+1:]...)
		})
	})
	if !ok {
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.credential_removed", name+"/"+credID,
		map[string]any{"tenantId": tenant, "pool_id": name, "credential_id": credID})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCredentialPool(updated))
}

// credentialPlan runs one credential write's row guards, checks, and
// mutation against p, recording the write's Secret operations in ops. It
// probes only when probe is true; a dryRun request passes false.
type credentialPlan func(ctx context.Context, p *credentialpoolstore.CredentialPool, ops *secretOps, probe bool) error

// decodeCredentialEntry decodes a credential addition or update body and
// authorizes the caller for the pool's tenant. On a refusal it has written
// the response and reports false.
func (r *Router) decodeCredentialEntry(w http.ResponseWriter, req *http.Request) (CredentialEntryRequest, string, bool) {
	var in CredentialEntryRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return CredentialEntryRequest{}, "", false
	}
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return CredentialEntryRequest{}, "", false
	}
	return in, tenant, true
}

// checkCredentialEntry runs the secretData check and the ref parse on a
// credential addition or update body and returns the entry with its ref in
// canonical form. On a refusal it has written the response and reports
// false.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 1; Secret references rules 1 and 2.
func (r *Router) checkCredentialEntry(w http.ResponseWriter, in CredentialEntryRequest) (CredentialEntryPayload, bool) {
	if err := checkSecretData(in); err != nil {
		writeAdminWriteError(w, err)
		return CredentialEntryPayload{}, false
	}
	entries := []CredentialEntryPayload{in.CredentialEntryPayload}
	if err := r.canonicalizeSecretRefs(entries); err != nil {
		writeAdminWriteError(w, err)
		return CredentialEntryPayload{}, false
	}
	return entries[0], true
}

// addCredentialGuard is the credential addition row guard: a soft-deleted
// pool is not found, and an id the pool already holds is a conflict.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 4; §15.1.
func addCredentialGuard(p *credentialpoolstore.CredentialPool, id string) error {
	if !p.IsActive() {
		return credentialpoolstore.ErrNotFound
	}
	if credentialIndex(p, id) >= 0 {
		return errCredentialExists
	}
	return nil
}

// updateCredentialGuard is the credential update row guard: a soft-deleted
// pool is not found, and an id the pool does not hold is not found. It
// returns the addressed credential's index.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 4; §15.1.
func updateCredentialGuard(p *credentialpoolstore.CredentialPool, id string) (int, error) {
	if !p.IsActive() {
		return -1, credentialpoolstore.ErrNotFound
	}
	idx := credentialIndex(p, id)
	if idx < 0 {
		return -1, errCredentialNotFound
	}
	return idx, nil
}

// commitCredentialWrite runs plan inside the credentialPools.Update mutate
// closure, then performs the planned Secret operations as the last step
// before the row commits. On a failure it has written the response and
// reports false.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 4, 5, and 6.
func (r *Router) commitCredentialWrite(w http.ResponseWriter, req *http.Request, tenant, name string, plan credentialPlan) (credentialpoolstore.CredentialPool, bool) {
	ops := &secretOps{}
	updated, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		ops.reset()
		if err := plan(req.Context(), p, ops, true); err != nil {
			return err
		}
		return r.applySecretOps(req.Context(), ops)
	})
	if err == nil {
		return updated, true
	}
	if writeAfterSecretOp(w, err, ops) || writeProxyEndpointError(w, err) {
		return credentialpoolstore.CredentialPool{}, false
	}
	r.writeCredentialMutateError(w, err)
	return credentialpoolstore.CredentialPool{}, false
}

// previewCredentialWrite serves a dryRun credential addition or update:
// it runs plan against an unlocked read with no probe call, refuses the
// preview as the write would be refused when the write needs a Secret
// operation and no writer is wired, and returns it without persisting,
// auditing, or performing a Secret operation.
//
// spec: §15.1 (dryRun); §4.9 (Credential Storage Topology) — Credential
// values submitted through the admin API rule 5.
func (r *Router) previewCredentialWrite(w http.ResponseWriter, req *http.Request, tenant, name string, status int, plan credentialPlan) {
	preview, ok := r.readPoolForPreview(w, req, tenant, name)
	if !ok {
		return
	}
	ops := &secretOps{}
	err := plan(req.Context(), &preview, ops, false)
	if err == nil {
		err = r.checkWriterFor(ops)
	}
	if err != nil {
		if !writeProxyEndpointError(w, err) {
			r.writeCredentialMutateError(w, err)
		}
		return
	}
	writeDryRun(w, status, fromCredentialPool(preview))
}

// credentialAuditPayload is the id-only audit payload of a credential
// addition or update: the tenant, the pool, the credential id, and the
// canonical secretRef the committed entry carries. It never carries a
// submitted value.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 7.
func credentialAuditPayload(tenant, pool, credID string, updated credentialpoolstore.CredentialPool) map[string]any {
	out := map[string]any{"tenantId": tenant, "pool_id": pool, "credential_id": credID}
	if idx := credentialIndex(&updated, credID); idx >= 0 && updated.Credentials[idx].SecretRef != "" {
		out["secretRef"] = updated.Credentials[idx].SecretRef
	}
	return out
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
// spec: §4.9 — mark the credential revoked, look up all
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

// PoolCredentialHealthReader returns the §24.5 row-2 per-credential
// runtime signals — the active lease count keyed by credential id —
// for one pool. The gateway implementation reads the credential-lease
// store; the admin store itself holds no runtime lease state (§4.9
// separates the pool definition from the leasing machinery). The
// returned map is keyed by credential id; a credential absent from the
// map has no active lease on this replica.
//
// spec: §24.5 — `get --pool <name>` shows per-credential health
// scores and lease counts.
type PoolCredentialHealthReader interface {
	PoolCredentialLeaseCounts(poolName string, credentialIDs []string) map[string]int
}

// WithPoolCredentialHealth wires the §24.5 per-credential health reader
// onto the Router. With it set, GET /v1/admin/credential-pools/{name}
// surfaces each credential's active lease count and a derived health
// indicator. Without it the GET response omits those fields, the
// minimal-gateway posture where no lease store is wired.
func (r *Router) WithPoolCredentialHealth(h PoolCredentialHealthReader) *Router {
	r.poolCredHealth = h
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
// spec: §4.9 — mark the credential revoked in the store,
// terminate its active leases via the deny list, emit credential.revoked,
// and return a 200 summary.
func (r *Router) handleRevokeCredential(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	credID := req.PathValue("credId")
	body, err := decodeOptionalRevokeBody(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
	// spec: §11.2.1 — emergency credential revocation is a billing-stream
	// cost-attribution / compliance event under the credential pool's tenant.
	r.appendBilling(req.Context(), billingfanout.CredentialRevoked(
		tenant, name, credID, principal.Subject, body.Reason, uint32(terminated),
	))
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
// spec: §4.9 — revoke all credentials in the pool; all
// active sessions are rotated to their fallback chain, or terminated
// with CREDENTIAL_POOL_EXHAUSTED if no fallback is available.
func (r *Router) handleRevokePool(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	body, err := decodeOptionalRevokeBody(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
// spec: §4.9.
func (r *Router) handleReEnableCredential(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	credID := req.PathValue("credId")
	body, err := decodeOptionalRevokeBody(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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

// writeCredentialMutateError maps the shared credential mutate failures
// to HTTP responses: a check refusal is its own response, an unknown pool
// or credential is 404, a duplicate id is 409, and any other failure is a
// 400 validation error.
func (r *Router) writeCredentialMutateError(w http.ResponseWriter, err error) {
	if writeAdminWriteError(w, err) {
		return
	}
	switch {
	case errors.Is(err, credentialpoolstore.ErrNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
	case errors.Is(err, errCredentialNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential not found in pool", nil)
	case errors.Is(err, errCredentialExists):
		// spec: §15.1 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
		writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS", "credential id already exists in pool", nil)
	default:
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
	}
}
