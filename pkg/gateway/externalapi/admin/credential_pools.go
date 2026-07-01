// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	// spec: §15.1 lines 1207-1209.
	ETag string `json:"etag,omitempty"`
}

// CachePolicyPayload is the §4.9 semantic-cache configuration on a pool
// (spec lines 1542-1547). It is absent when the pool declares no
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
	// lease store. spec: §24.5 line 87.
	Health     string `json:"health,omitempty"`
	LeaseCount *int   `json:"leaseCount,omitempty"`
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

// validatePoolProxyDialect enforces the §4.9 line 1476 proxy-dialect
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
		CachePolicy:                fromCachePolicy(p.CachePolicy),
		CreatedAt:                  rfc3339Nano(p.CreatedAt),
		UpdatedAt:                  rfc3339Nano(p.UpdatedAt),
		DeletedAt:                  rfc3339Nano(p.DeletedAt),
		// spec: §15.1 line 1207 — the ETag is the quoted decimal version,
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
// a single secretRef. spec: §4.9 line 1212.
type SecretProbeVerdict int

const (
	// SecretProbeAllowed — the Token Service can read the Secret.
	SecretProbeAllowed SecretProbeVerdict = iota
	// SecretProbeDenied — the Token Service ServiceAccount lacks the
	// RBAC grant to read the Secret.
	SecretProbeDenied
	// SecretProbeNotFound — the grant exists but the Secret is absent.
	SecretProbeNotFound
)

// SecretAccessProber runs the §4.9 admin-time RBAC live-probe against
// the Token Service over the gateway↔Token-Service mTLS link. The
// gateway implementation calls the Token Service's ProbeSecretAccess
// RPC; the Token Service reviews its own ServiceAccount's access. A
// non-nil error is an indeterminate probe (Token Service unreachable,
// mTLS failure, Kubernetes API timeout) that the handler maps to
// 503 CREDENTIAL_PROBE_UNAVAILABLE and never fails open.
//
// spec: §4.9 line 1212.
type SecretAccessProber interface {
	ProbeSecretAccess(ctx context.Context, secretRef string) (SecretProbeVerdict, error)
}

// WithSecretAccessProber wires the §4.9 admin-time RBAC live-probe onto
// the Router. With it set, pool creation and any pool update that
// introduces a new secretRef probe the Token Service's read access to
// each referenced Secret before the write is persisted, rejecting an
// unreadable secretRef with 422 CREDENTIAL_SECRET_RBAC_MISSING (or
// 503 CREDENTIAL_PROBE_UNAVAILABLE when the probe cannot be evaluated).
// Without it the probe is skipped, the dev-mode posture parallel to the
// in-process credential path the gateway uses when no Token Service link
// is configured (`--token-service-grpc-addr` empty); the probe is
// Token-Service-owned and has no meaning without that link.
//
// spec: §4.9 line 1212.
func (r *Router) WithSecretAccessProber(p SecretAccessProber) *Router {
	r.secretProber = p
	return r
}

// probeSecretRefs runs the §4.9 RBAC live-probe over refs (the new or
// changed secretRef values for a pool write). It returns true when every
// ref is readable (or no prober is wired). On a DENIED/NOT_FOUND verdict
// it writes 422 CREDENTIAL_SECRET_RBAC_MISSING naming every failing
// Secret plus the RBAC patch command, and returns false. On an
// indeterminate probe it writes 503 CREDENTIAL_PROBE_UNAVAILABLE and
// returns false — the handler MUST NOT fail open by persisting a write
// whose probe could not be evaluated.
//
// spec: §4.9 line 1212.
func (r *Router) probeSecretRefs(w http.ResponseWriter, req *http.Request, refs []string) bool {
	if r.secretProber == nil {
		return true
	}
	var missing []string
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		verdict, err := r.secretProber.ProbeSecretAccess(req.Context(), ref)
		if err != nil {
			// Indeterminate: a transport/evaluation failure prevents a
			// definitive verdict. Reject with the distinct 5xx so the
			// operator fixes reachability rather than RBAC, and never
			// persist the unprobed secretRef.
			writeError(w, http.StatusServiceUnavailable, "CREDENTIAL_PROBE_UNAVAILABLE",
				"Token Service secret-access probe could not be evaluated; the write was rejected",
				map[string]any{"secretRef": ref})
			return false
		}
		if verdict != SecretProbeAllowed {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "CREDENTIAL_SECRET_RBAC_MISSING",
			"the Token Service ServiceAccount cannot read every referenced Secret; patch its RBAC and retry",
			map[string]any{
				"missingSecrets": missing,
				"remediation":    tokenServiceRBACPatch(missing),
			})
		return false
	}
	return true
}

// secretRefsOf returns the non-empty secretRef values in entries.
func secretRefsOf(entries []CredentialEntryPayload) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.SecretRef != "" {
			out = append(out, e.SecretRef)
		}
	}
	return out
}

// newSecretRefs returns the secretRef values in want that are absent
// from have. It is the §4.9 "update changes secretRef" set: a PUT
// re-keys the full credential list, so only a secretRef not already in
// the stored pool needs an admission-time probe.
func newSecretRefs(have []credentialpoolstore.Credential, want []CredentialEntryPayload) []string {
	existing := make(map[string]struct{}, len(have))
	for _, c := range have {
		if c.SecretRef != "" {
			existing[c.SecretRef] = struct{}{}
		}
	}
	var out []string
	for _, e := range want {
		if e.SecretRef == "" {
			continue
		}
		if _, ok := existing[e.SecretRef]; !ok {
			out = append(out, e.SecretRef)
		}
	}
	return out
}

// tokenServiceRBACPatch builds the kubectl patch command that adds the
// missing Secret names to the Token Service's secret-reader Role
// resourceNames list — the remediation `lenny-ctl admin credential-pools
// add-credential` emits. spec: §4.9 line 1212.
func tokenServiceRBACPatch(missing []string) string {
	ops := make([]string, 0, len(missing))
	for _, s := range missing {
		ops = append(ops, fmt.Sprintf(`{"op":"add","path":"/rules/0/resourceNames/-","value":%q}`, s))
	}
	return fmt.Sprintf(
		`kubectl patch role lenny-token-service-secrets -n lenny-system --type=json -p '[%s]'`,
		strings.Join(ops, ","),
	)
}

// handleCreateCredentialPool implements POST /v1/admin/credential-pools.
//
// When a §4.9 RBAC live-probe is wired (the gateway↔Token-Service mTLS
// link is present), every credentials[].secretRef is probed for Token
// Service read access before the pool is persisted, so a missing RBAC
// grant fails admission with 422 CREDENTIAL_SECRET_RBAC_MISSING rather
// than surfacing later as an opaque CREDENTIAL_POOL_EXHAUSTED. spec:
// §4.9 line 1212.
func (r *Router) handleCreateCredentialPool(w http.ResponseWriter, req *http.Request) {
	var body CredentialPoolPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
	// spec: §4.9 line 1476 — reject a proxy-mode pool whose proxyDialect
	// no agent runtime serving its provider can speak.
	if !r.validatePoolProxyDialect(w, req, body.Provider, body.ProxyDialect) {
		return
	}
	// §4.9 admin-time RBAC live-probe over every referenced Secret. A
	// new pool introduces all of its secretRefs.
	if !r.probeSecretRefs(w, req, secretRefsOf(body.Credentials)) {
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
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	if req.URL.Query().Get("dryRun") == "true" {
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
			// spec: §15.1 line 983 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
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
	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope. F-15.1.6.
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
	// spec: §15.1 line 1209 — GET responses for an admin resource carry the
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
// store carries no runtime lease state. spec: §24.5 line 87.
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
	var body CredentialPoolPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
	// spec: §4.9 line 1476 — PUT is a full replace, so the body carries
	// the effective provider/proxyDialect; reject when the updated
	// proxy-mode pool declares a dialect no agent runtime serving its
	// provider can speak.
	if !r.validatePoolProxyDialect(w, req, body.Provider, body.ProxyDialect) {
		return
	}
	// Resolve the current pool once: the §15.1 If-Match precondition reads
	// its version, the §4.9 secretRef probe diffs against its credential set,
	// and the dry-run preview applies the body onto it (preserving §4.9
	// revocation state). A missing pool 404s ahead of all three.
	current, gerr := r.credentialPools.Get(req.Context(), tenant, name)
	if gerr != nil {
		if errors.Is(gerr, credentialpoolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 lines 1207-1211 — every admin PUT requires If-Match. Enforce
	// the optimistic-concurrency precondition before the probe / dry-run
	// branches so dryRun=true combined with a missing/stale If-Match fails here.
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	// §4.9 admin-time RBAC live-probe. A PUT replaces the full credential
	// list, so probe only the secretRefs the update introduces (those not
	// already in the stored pool). Skipped entirely when no prober is
	// wired so the dev-mode update path takes no extra read.
	if r.secretProber != nil {
		if !r.probeSecretRefs(w, req, newSecretRefs(current.Credentials, body.Credentials)) {
			return
		}
	}
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	if req.URL.Query().Get("dryRun") == "true" {
		preview := current
		applyCredentialPoolUpdate(&preview, body)
		preview.TenantID = tenant
		if verr := credentialpoolstore.Validate(preview); verr != nil {
			if writeProxyEndpointError(w, verr) {
				return
			}
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", verr.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, fromCredentialPool(preview))
		return
	}
	updated, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		applyCredentialPoolUpdate(p, body)
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
	// spec: §15.1 line 1210 — a successful PUT carries the bumped ETag so the
	// client can chain a subsequent write without a refresh GET.
	w.Header().Set("ETag", formatETag(updated.Version))
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
	// Resolve the current pool so the §15.1 DELETE If-Match precondition can
	// compare against its version; a missing pool 404s.
	current, gerr := r.credentialPools.Get(req.Context(), tenant, name)
	if gerr != nil {
		if errors.Is(gerr, credentialpoolstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 line 1213 — DELETE honours If-Match only when present: a
	// stale tag returns 412 ETAG_MISMATCH, an absent header proceeds.
	if !enforceIfMatchIfPresent(w, req, current.Version) {
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

// errCredentialExists is the mutate sentinel for an add-credential whose
// id is already present in the pool. handleAddCredential maps it to a
// 409 RESOURCE_ALREADY_EXISTS.
var errCredentialExists = errors.New("credential id already exists in pool")

// handleAddCredential implements POST
// /v1/admin/credential-pools/{name}/credentials — the §24.5 row-3
// add-credential operation. The body is a single credential entry; the
// handler runs the §4.9 admin-time RBAC live-probe over its secretRef
// before appending it, so a Secret the Token Service cannot read fails
// admission with 422 CREDENTIAL_SECRET_RBAC_MISSING rather than later as
// an opaque CREDENTIAL_POOL_EXHAUSTED.
//
// spec: §15.1 line 876 (POST .../credentials); §24.5 row 3; §4.9 line
// 1212 (admin-time RBAC live-probe — same probe paths as pool create).
func (r *Router) handleAddCredential(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body CredentialEntryPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "credential id is required",
			map[string]any{"field": "id"})
		return
	}
	// §4.9 admin-time RBAC live-probe over the new secretRef.
	if !r.probeSecretRefs(w, req, secretRefsOf([]CredentialEntryPayload{body})) {
		return
	}
	updated, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		if credentialIndex(p, body.ID) >= 0 {
			return errCredentialExists
		}
		p.Credentials = append(p.Credentials, credentialpoolstore.Credential{
			ID: body.ID, SecretRef: body.SecretRef, RoleArn: body.RoleArn, Region: body.Region,
		})
		return nil
	})
	if err != nil {
		if writeProxyEndpointError(w, err) {
			return
		}
		r.writeCredentialMutateError(w, err)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.credential_added", name+"/"+body.ID,
		map[string]any{"tenantId": tenant, "pool_id": name, "credential_id": body.ID})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromCredentialPool(updated))
}

// handleUpdateCredentialEntry implements PUT
// /v1/admin/credential-pools/{name}/credentials/{credId} — the §24.5
// row-4 update-credential operation. It replaces the addressed
// credential's mutable fields (secretRef, roleArn, region) in place,
// preserving its §4.9 revocation status (an update is not a re-enable).
// When the secretRef changes, the §4.9 admin-time RBAC live-probe runs
// over the new value.
//
// spec: §15.1 line 877 (PUT .../credentials/{credId}, RBAC probe on
// secretRef change); §24.5 row 4; §4.9 line 1212.
func (r *Router) handleUpdateCredentialEntry(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	credID := req.PathValue("credId")
	var body CredentialEntryPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// §4.9 admin-time RBAC live-probe runs only when the update changes
	// secretRef (§15.1 line 877). Resolve the current entry first so an
	// unchanged secretRef takes no probe and a missing credential 404s
	// before any probe.
	if r.secretProber != nil && body.SecretRef != "" {
		current, gerr := r.credentialPools.Get(req.Context(), tenant, name)
		if gerr != nil {
			if errors.Is(gerr, credentialpoolstore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
			return
		}
		idx := credentialIndex(&current, credID)
		if idx < 0 {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential not found in pool", nil)
			return
		}
		if body.SecretRef != current.Credentials[idx].SecretRef {
			if !r.probeSecretRefs(w, req, []string{body.SecretRef}) {
				return
			}
		}
	}
	updated, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		idx := credentialIndex(p, credID)
		if idx < 0 {
			return errCredentialNotFound
		}
		p.Credentials[idx].SecretRef = body.SecretRef
		p.Credentials[idx].RoleArn = body.RoleArn
		p.Credentials[idx].Region = body.Region
		return nil
	})
	if err != nil {
		if writeProxyEndpointError(w, err) {
			return
		}
		r.writeCredentialMutateError(w, err)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.credential_updated", name+"/"+credID,
		map[string]any{"tenantId": tenant, "pool_id": name, "credential_id": credID})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCredentialPool(updated))
}

// handleRemoveCredential implements DELETE
// /v1/admin/credential-pools/{name}/credentials/{credId} — the §24.5
// row-5 remove-credential operation. The credential is dropped from the
// pool, so no new lease selects it. Per §15.1 line 878 the active
// leases backed by it are rotated via the standard §4.9 fallback path
// (the renewal path can no longer resolve the removed credential and
// re-acquires from the remaining pool credentials); removal does not add
// the credential to the deny list, which is reserved for emergency
// revocation (CREDENTIAL_REVOKED hard-reject).
//
// spec: §15.1 line 878 (DELETE .../credentials/{credId}); §24.5 row 5.
func (r *Router) handleRemoveCredential(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	credID := req.PathValue("credId")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	updated, err := r.credentialPools.Update(req.Context(), tenant, name, func(p *credentialpoolstore.CredentialPool) error {
		idx := credentialIndex(p, credID)
		if idx < 0 {
			return errCredentialNotFound
		}
		p.Credentials = append(p.Credentials[:idx], p.Credentials[idx+1:]...)
		return nil
	})
	if err != nil {
		r.writeCredentialMutateError(w, err)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.credential_pool.credential_removed", name+"/"+credID,
		map[string]any{"tenantId": tenant, "pool_id": name, "credential_id": credID})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromCredentialPool(updated))
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

// PoolCredentialHealthReader returns the §24.5 row-2 per-credential
// runtime signals — the active lease count keyed by credential id —
// for one pool. The gateway implementation reads the credential-lease
// store; the admin store itself holds no runtime lease state (§4.9
// separates the pool definition from the leasing machinery). The
// returned map is keyed by credential id; a credential absent from the
// map has no active lease on this replica.
//
// spec: §24.5 line 87 — `get --pool <name>` shows per-credential health
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
// spec: §4.9 lines 1626-1652 — mark the credential revoked in the store,
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
// spec: §4.9 lines 1654-1659 — revoke all credentials in the pool; all
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
// spec: §4.9 line 1743 (credential.re_enabled), lines 1675-1677 (runbook
// step 6 — rotate the underlying secret before re-enabling).
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

// writeCredentialMutateError maps the shared revoke/re-enable mutate
// failures to HTTP responses: an unknown pool or credential is 404, any
// other failure is a 400 validation error.
func (r *Router) writeCredentialMutateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, credentialpoolstore.ErrNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential pool not found", nil)
	case errors.Is(err, errCredentialNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential not found in pool", nil)
	case errors.Is(err, errCredentialExists):
		// spec: §15.1 line 983 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
		writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS", "credential id already exists in pool", nil)
	default:
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
	}
}
