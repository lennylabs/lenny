// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// allowAllWarning is the §10.6 advisory the lenny-noenvironmentpolicy-audit
// control attaches when a tenant sets noEnvironmentPolicy to allow-all.
const allowAllWarning = `noEnvironmentPolicy: allow-all grants unrestricted ` +
	`runtime access to all authenticated users with no environment membership ` +
	`in this tenant. Verify this matches the intended security posture.`

// IdentityProviderPayload is the §10.6 line 661 OIDC identity-provider
// wire shape.
type IdentityProviderPayload struct {
	Type                         string `json:"type,omitempty"`
	IntrospectionEnabled         bool   `json:"introspectionEnabled,omitempty"`
	IntrospectionEndpoint        string `json:"introspectionEndpoint,omitempty"`
	IntrospectionClientID        string `json:"introspectionClientId,omitempty"`
	IntrospectionClientSecret    string `json:"introspectionClientSecret,omitempty"`
	IntrospectionCacheTTLSeconds int    `json:"introspectionCacheTtlSeconds,omitempty"`
}

// RBACConfigPayload is the §10.6 / §15.1 tenant RBAC-config admin
// payload: the noEnvironmentPolicy field plus the §10.6 line 665
// identityProvider, tokenPolicy, capabilities taxonomy, and
// mcpAnnotationMapping overrides. PUT replaces the full configuration —
// an omitted field is cleared, mirroring the noEnvironmentPolicy
// default-to-deny-all behaviour.
type RBACConfigPayload struct {
	TenantID             string                  `json:"tenantId"`
	NoEnvironmentPolicy  string                  `json:"noEnvironmentPolicy"`
	IdentityProvider     IdentityProviderPayload `json:"identityProvider,omitempty"`
	TokenPolicy          json.RawMessage         `json:"tokenPolicy,omitempty"`
	Capabilities         []string                `json:"capabilities,omitempty"`
	MCPAnnotationMapping map[string][]string     `json:"mcpAnnotationMapping,omitempty"`
}

// toRBACConfig maps the §10.6 extra RBAC-config fields of a payload to
// the stored tenantstore.RBACConfig (the noEnvironmentPolicy field is a
// separate column and is not part of this sub-object).
func toRBACConfig(p RBACConfigPayload) tenantstore.RBACConfig {
	return tenantstore.RBACConfig{
		IdentityProvider: tenantstore.IdentityProvider{
			Type:                         p.IdentityProvider.Type,
			IntrospectionEnabled:         p.IdentityProvider.IntrospectionEnabled,
			IntrospectionEndpoint:        p.IdentityProvider.IntrospectionEndpoint,
			IntrospectionClientID:        p.IdentityProvider.IntrospectionClientID,
			IntrospectionClientSecret:    p.IdentityProvider.IntrospectionClientSecret,
			IntrospectionCacheTTLSeconds: p.IdentityProvider.IntrospectionCacheTTLSeconds,
		},
		TokenPolicy:          p.TokenPolicy,
		Capabilities:         p.Capabilities,
		MCPAnnotationMapping: p.MCPAnnotationMapping,
	}
}

// rbacConfigPayload assembles the full §10.6 RBAC-config wire shape for
// a tenant: the noEnvironmentPolicy column plus the stored RBACConfig
// sub-object.
func rbacConfigPayload(t tenantstore.Tenant) RBACConfigPayload {
	c := t.RBACConfig
	return RBACConfigPayload{
		TenantID:            t.ID,
		NoEnvironmentPolicy: effectiveNoEnvironmentPolicy(t),
		IdentityProvider: IdentityProviderPayload{
			Type:                         c.IdentityProvider.Type,
			IntrospectionEnabled:         c.IdentityProvider.IntrospectionEnabled,
			IntrospectionEndpoint:        c.IdentityProvider.IntrospectionEndpoint,
			IntrospectionClientID:        c.IdentityProvider.IntrospectionClientID,
			IntrospectionClientSecret:    c.IdentityProvider.IntrospectionClientSecret,
			IntrospectionCacheTTLSeconds: c.IdentityProvider.IntrospectionCacheTTLSeconds,
		},
		TokenPolicy:          c.TokenPolicy,
		Capabilities:         c.Capabilities,
		MCPAnnotationMapping: c.MCPAnnotationMapping,
	}
}

// validateRBACConfigExtras reports the §10.6 admission errors for the
// identityProvider, tokenPolicy, capabilities, and mcpAnnotationMapping
// fields. It returns a non-empty message on the first violation.
func validateRBACConfigExtras(p RBACConfigPayload) string {
	// spec: §10.6 line 661 — the identity model is OIDC. Accept the
	// empty type (inherit the platform provider) or the literal "oidc";
	// reject anything else so a typo fails loudly.
	if t := p.IdentityProvider.Type; t != "" && t != "oidc" {
		return fmt.Sprintf("identityProvider.type %q is not supported (use \"oidc\")", t)
	}
	// spec: §10.6 line 661 — introspectionEnabled drives an RFC 7662
	// real-time group check. The check needs an endpoint to call; reject
	// an enabled config that names none so the toggle never fails closed
	// silently at request time. The endpoint must be TLS — the gateway
	// posts the bearer token to it.
	if ip := p.IdentityProvider; ip.IntrospectionEnabled {
		ep := strings.TrimSpace(ip.IntrospectionEndpoint)
		if ep == "" {
			return "identityProvider.introspectionEndpoint is required when introspectionEnabled is true"
		}
		u, err := url.Parse(ep)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "identityProvider.introspectionEndpoint must be an absolute https URL"
		}
		if ip.IntrospectionClientSecret != "" && ip.IntrospectionClientID == "" {
			return "identityProvider.introspectionClientId is required when introspectionClientSecret is set"
		}
	}
	if s := p.IdentityProvider.IntrospectionCacheTTLSeconds; s < 0 {
		return "identityProvider.introspectionCacheTtlSeconds must not be negative"
	}
	// §10.6 line 665 — tokenPolicy is an opaque object. Reject a non-object
	// (array/scalar) so the stored value is always a JSON object the GET
	// round-trips unchanged.
	if len(p.TokenPolicy) > 0 {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(p.TokenPolicy, &obj); err != nil {
			return "tokenPolicy must be a JSON object"
		}
	}
	// §10.6 line 665 — the capability taxonomy is extensible per tenant;
	// validate the names loosely (non-empty, no duplicates).
	seen := map[string]bool{}
	for i, c := range p.Capabilities {
		if c == "" {
			return fmt.Sprintf("capabilities[%d] must not be empty", i)
		}
		if seen[c] {
			return fmt.Sprintf("capabilities[%d] duplicates %q", i, c)
		}
		seen[c] = true
	}
	// §5.1 line 325 — mcpAnnotationMapping overrides the capability
	// inference, so each mapped value must be a closed §5.3 tool
	// capability. Reuse the §5.1 toolCapabilityOverrides validator.
	overrides := make(map[string][]capabilityinference.Capability, len(p.MCPAnnotationMapping))
	for tool, caps := range p.MCPAnnotationMapping {
		cc := make([]capabilityinference.Capability, len(caps))
		for i, c := range caps {
			cc[i] = capabilityinference.Capability(c)
		}
		overrides[tool] = cc
	}
	if err := capabilityinference.ValidateOverrides(overrides); err != nil {
		return "mcpAnnotationMapping: " + err.Error()
	}
	return ""
}

// effectiveNoEnvironmentPolicy reports a tenant's §10.6
// noEnvironmentPolicy, treating an unset value as the platform default
// deny-all — §10.6 requires a tenant-level omission to be treated as
// deny-all.
func effectiveNoEnvironmentPolicy(t tenantstore.Tenant) string {
	if t.NoEnvironmentPolicy == "" {
		return tenantstore.NoEnvPolicyDenyAll
	}
	return t.NoEnvironmentPolicy
}

// handleGetRBACConfig serves GET /v1/admin/tenants/{id}/rbac-config
// (§10.6 / §15.1).
func (r *Router) handleGetRBACConfig(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	row, err := r.tenants.Get(req.Context(), tenant)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 line 1209 — the rbac-config sub-resource's ETag is the
	// tenant row's version, since the configuration is stored on the
	// tenant. GET carries it so the next PUT can supply If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rbacConfigPayload(row))
}

// handlePutRBACConfig serves PUT /v1/admin/tenants/{id}/rbac-config
// (§10.6 / §15.1). A body that omits noEnvironmentPolicy is treated as
// deny-all per §10.6. Setting allow-all succeeds but, as the §10.6
// lenny-noenvironmentpolicy-audit advisory control, attaches a Warning
// response header because allow-all broadens runtime access for
// callers with no environment membership.
func (r *Router) handlePutRBACConfig(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	var body RBACConfigPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	// §10.6: an omitted noEnvironmentPolicy is treated as deny-all.
	policy := body.NoEnvironmentPolicy
	if policy == "" {
		policy = tenantstore.NoEnvPolicyDenyAll
	}
	if policy != tenantstore.NoEnvPolicyDenyAll && policy != tenantstore.NoEnvPolicyAllowAll {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"noEnvironmentPolicy must be deny-all or allow-all",
			map[string]any{"field": "noEnvironmentPolicy"})
		return
	}
	// spec: §10.6 line 665 — validate the identityProvider, tokenPolicy,
	// capabilities, and mcpAnnotationMapping fields before persisting.
	// F-10.6.6.
	if msg := validateRBACConfigExtras(body); msg != "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", msg, nil)
		return
	}
	// spec: §15.1 lines 1207-1211 — the rbac-config PUT enforces If-Match
	// against the tenant row's version (the sub-resource's entity tag). A
	// missing tenant 404s ahead of the precondition.
	current, gerr := r.tenants.Get(req.Context(), tenant)
	if gerr != nil {
		if errors.Is(gerr, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	rbac := toRBACConfig(body)
	updated, err := r.tenants.Update(req.Context(), tenant, func(t *tenantstore.Tenant) error {
		t.NoEnvironmentPolicy = policy
		t.RBACConfig = rbac
		return nil
	})
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.tenant.rbac_config_updated", tenant, map[string]any{
		"noEnvironmentPolicy": policy,
	})
	if policy == tenantstore.NoEnvPolicyAllowAll {
		w.Header().Set("Warning", `299 - "`+allowAllWarning+`"`)
		if r.metrics != nil {
			r.metrics.RecordNoEnvironmentPolicyAllowAll(tenant)
		}
	}
	// spec: §15.1 line 1210 — a successful PUT carries the bumped ETag.
	w.Header().Set("ETag", formatETag(updated.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rbacConfigPayload(updated))
}
