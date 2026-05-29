// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// WithEnvironments wires the §10.6 / §15.1 environment admin endpoints
// onto the Router.
func (r *Router) WithEnvironments(s environmentstore.Store) *Router {
	r.environments = s
	return r
}

// RequirementPayload is the wire shape of one §10.6 matchExpressions
// entry.
type RequirementPayload struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// SelectorPayload is the wire shape of a §10.6 tag-based selector.
type SelectorPayload struct {
	MatchLabels      map[string]string    `json:"matchLabels,omitempty"`
	MatchExpressions []RequirementPayload `json:"matchExpressions,omitempty"`
	Types            []string             `json:"types,omitempty"`
	Include          []string             `json:"include,omitempty"`
	Exclude          []string             `json:"exclude,omitempty"`
}

// IdentityPayload is the wire shape of a §10.6 member identity.
type IdentityPayload struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// MemberPayload is the wire shape of a §10.6 environment member.
type MemberPayload struct {
	Identity IdentityPayload `json:"identity"`
	Role     string          `json:"role"`
}

// MCPRuntimeFilterPayload is the wire shape of a §10.6 capability
// filter for mcp runtimes.
type MCPRuntimeFilterPayload struct {
	RuntimeSelector     SelectorPayload `json:"runtimeSelector"`
	AllowedCapabilities []string        `json:"allowedCapabilities,omitempty"`
	DeniedCapabilities  []string        `json:"deniedCapabilities,omitempty"`
}

// CrossEnvRulePayload is one §10.6 bilateral cross-environment rule.
// The `environment` field names the peer — the target for an outbound
// rule, the permitted source for an inbound rule.
type CrossEnvRulePayload struct {
	Environment string          `json:"environment"`
	Runtimes    SelectorPayload `json:"runtimes"`
}

// CrossEnvDelegationPayload is the §10.6 crossEnvironmentDelegation
// block.
type CrossEnvDelegationPayload struct {
	Outbound []CrossEnvRulePayload `json:"outbound,omitempty"`
	Inbound  []CrossEnvRulePayload `json:"inbound,omitempty"`
}

// EnvironmentPayload is the §15.1 environment wire shape.
type EnvironmentPayload struct {
	Name                       string                    `json:"name"`
	TenantID                   string                    `json:"tenantId,omitempty"`
	Description                string                    `json:"description,omitempty"`
	Members                    []MemberPayload           `json:"members,omitempty"`
	RuntimeSelector            SelectorPayload           `json:"runtimeSelector"`
	MCPRuntimeFilters          []MCPRuntimeFilterPayload `json:"mcpRuntimeFilters,omitempty"`
	ConnectorSelector          SelectorPayload           `json:"connectorSelector"`
	DefaultDelegationPolicy    string                    `json:"defaultDelegationPolicy,omitempty"`
	CrossEnvironmentDelegation CrossEnvDelegationPayload `json:"crossEnvironmentDelegation"`
	CreatedAt                  string                    `json:"createdAt,omitempty"`
	UpdatedAt                  string                    `json:"updatedAt,omitempty"`
}

func toSelector(p SelectorPayload) environment.Selector {
	reqs := make([]environment.Requirement, len(p.MatchExpressions))
	for i, r := range p.MatchExpressions {
		reqs[i] = environment.Requirement{
			Key: r.Key, Operator: environment.LabelOperator(r.Operator), Values: r.Values,
		}
	}
	return environment.Selector{
		MatchLabels: p.MatchLabels, MatchExpressions: reqs,
		Types: p.Types, Include: p.Include, Exclude: p.Exclude,
	}
}

func fromSelector(s environment.Selector) SelectorPayload {
	reqs := make([]RequirementPayload, len(s.MatchExpressions))
	for i, r := range s.MatchExpressions {
		reqs[i] = RequirementPayload{Key: r.Key, Operator: string(r.Operator), Values: r.Values}
	}
	return SelectorPayload{
		MatchLabels: s.MatchLabels, MatchExpressions: reqs,
		Types: s.Types, Include: s.Include, Exclude: s.Exclude,
	}
}

func toCrossEnvRules(ps []CrossEnvRulePayload) []environmentstore.CrossEnvRule {
	if len(ps) == 0 {
		return nil
	}
	out := make([]environmentstore.CrossEnvRule, len(ps))
	for i, p := range ps {
		out[i] = environmentstore.CrossEnvRule{Environment: p.Environment, Runtimes: toSelector(p.Runtimes)}
	}
	return out
}

func fromCrossEnvRules(rs []environmentstore.CrossEnvRule) []CrossEnvRulePayload {
	if len(rs) == 0 {
		return nil
	}
	out := make([]CrossEnvRulePayload, len(rs))
	for i, r := range rs {
		out[i] = CrossEnvRulePayload{Environment: r.Environment, Runtimes: fromSelector(r.Runtimes)}
	}
	return out
}

// toEnvironment maps a wire payload to a stored environment within the
// resolved tenant.
func toEnvironment(p EnvironmentPayload, tenant string) environmentstore.Environment {
	members := make([]environmentstore.Member, len(p.Members))
	for i, m := range p.Members {
		members[i] = environmentstore.Member{
			Identity: environmentstore.Identity{Type: m.Identity.Type, Value: m.Identity.Value},
			Role:     environment.Role(m.Role),
		}
	}
	filters := make([]environmentstore.MCPRuntimeFilter, len(p.MCPRuntimeFilters))
	for i, f := range p.MCPRuntimeFilters {
		filters[i] = environmentstore.MCPRuntimeFilter{
			RuntimeSelector:     toSelector(f.RuntimeSelector),
			AllowedCapabilities: f.AllowedCapabilities,
			DeniedCapabilities:  f.DeniedCapabilities,
		}
	}
	return environmentstore.Environment{
		Name:                    p.Name,
		TenantID:                tenant,
		Description:             p.Description,
		Members:                 members,
		RuntimeSelector:         toSelector(p.RuntimeSelector),
		MCPRuntimeFilters:       filters,
		ConnectorSelector:       toSelector(p.ConnectorSelector),
		DefaultDelegationPolicy: p.DefaultDelegationPolicy,
		CrossEnvOutbound:        toCrossEnvRules(p.CrossEnvironmentDelegation.Outbound),
		CrossEnvInbound:         toCrossEnvRules(p.CrossEnvironmentDelegation.Inbound),
	}
}

// fromEnvironment maps a stored environment to the wire payload.
func fromEnvironment(e environmentstore.Environment) EnvironmentPayload {
	members := make([]MemberPayload, len(e.Members))
	for i, m := range e.Members {
		members[i] = MemberPayload{
			Identity: IdentityPayload{Type: m.Identity.Type, Value: m.Identity.Value},
			Role:     string(m.Role),
		}
	}
	filters := make([]MCPRuntimeFilterPayload, len(e.MCPRuntimeFilters))
	for i, f := range e.MCPRuntimeFilters {
		filters[i] = MCPRuntimeFilterPayload{
			RuntimeSelector:     fromSelector(f.RuntimeSelector),
			AllowedCapabilities: f.AllowedCapabilities,
			DeniedCapabilities:  f.DeniedCapabilities,
		}
	}
	return EnvironmentPayload{
		Name:                    e.Name,
		TenantID:                e.TenantID,
		Description:             e.Description,
		Members:                 members,
		RuntimeSelector:         fromSelector(e.RuntimeSelector),
		MCPRuntimeFilters:       filters,
		ConnectorSelector:       fromSelector(e.ConnectorSelector),
		DefaultDelegationPolicy: e.DefaultDelegationPolicy,
		CrossEnvironmentDelegation: CrossEnvDelegationPayload{
			Outbound: fromCrossEnvRules(e.CrossEnvOutbound),
			Inbound:  fromCrossEnvRules(e.CrossEnvInbound),
		},
		CreatedAt: rfc3339Nano(e.CreatedAt),
		UpdatedAt: rfc3339Nano(e.UpdatedAt),
	}
}

func (r *Router) handleCreateEnvironment(w http.ResponseWriter, req *http.Request) {
	var body EnvironmentPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// spec: §10.6 line 562 — admin caller asserts a tenantId on the
	// body. When the asserted value disagrees with the authorized
	// tenant, fail loudly rather than silently rewrite the row.
	// F-10.6.12.
	if body.TenantID != "" && body.TenantID != tenant {
		writeError(w, http.StatusBadRequest, "TENANT_ID_MISMATCH",
			"request body tenantId does not match the authorized tenant",
			map[string]any{"bodyTenantId": body.TenantID, "authorizedTenantId": tenant})
		return
	}
	env := toEnvironment(body, tenant)
	if err := r.environments.Create(req.Context(), env); err != nil {
		if errors.Is(err, environmentstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"environment with this name already exists in tenant", nil)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.environments.Get(req.Context(), tenant, env.Name)
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.environment.created", env.Name, map[string]any{"tenantId": tenant})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromEnvironment(stored))
}

func (r *Router) handleListEnvironments(w http.ResponseWriter, req *http.Request) {
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	rows, err := r.environments.List(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]EnvironmentPayload, 0, len(rows))
	for _, e := range rows {
		out = append(out, fromEnvironment(e))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"environments": out})
}

func (r *Router) handleGetEnvironment(w http.ResponseWriter, req *http.Request) {
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	row, err := r.environments.Get(req.Context(), tenant, req.PathValue("name"))
	if err != nil {
		if errors.Is(err, environmentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "environment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromEnvironment(row))
}

func (r *Router) handleUpdateEnvironment(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body EnvironmentPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	tenant, _, err := r.authorizedTenantForUser(req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// spec: §10.6 line 562 — body tenantId must match the authorized
	// tenant on Update too; a silent rewrite would let an admin re-tag
	// a row to look like it belonged to a different tenant. F-10.6.12.
	if body.TenantID != "" && body.TenantID != tenant {
		writeError(w, http.StatusBadRequest, "TENANT_ID_MISMATCH",
			"request body tenantId does not match the authorized tenant",
			map[string]any{"bodyTenantId": body.TenantID, "authorizedTenantId": tenant})
		return
	}
	desired := toEnvironment(body, tenant)
	updated, err := r.environments.Update(req.Context(), tenant, name, func(e *environmentstore.Environment) error {
		desired.Name = e.Name
		desired.CreatedAt = e.CreatedAt
		*e = desired
		return nil
	})
	if err != nil {
		if errors.Is(err, environmentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "environment not found", nil)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.environment.updated", name, map[string]any{"tenantId": tenant})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromEnvironment(updated))
}

func (r *Router) handleDeleteEnvironment(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if err := r.environments.Delete(req.Context(), tenant, name); err != nil {
		if errors.Is(err, environmentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "environment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), principal, "admin.environment.deleted", name, map[string]any{"tenantId": tenant})
	w.WriteHeader(http.StatusNoContent)
}
