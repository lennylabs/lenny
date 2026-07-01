// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// requireEnvironmentTierOverride enforces the §12.9 line 1033
// environment-level workspaceTier override: a non-empty override must be
// a tenant-settable §12.9 tier (T3 or T4) and may only tighten the parent
// tenant's tier, never loosen it. An empty value inherits the tenant tier
// and is always admitted. When the parent tenant cannot be resolved the
// enum-valid override is admitted (the §10.2 tenant path governs unknown
// tenants). It returns true when the write may proceed; when it returns
// false it has already written the response. spec: §12.9 line 1033.
func (r *Router) requireEnvironmentTierOverride(w http.ResponseWriter, req *http.Request, tenant, envTier string) bool {
	if envTier == "" {
		return true
	}
	if !tenantstore.ValidWorkspaceTier(envTier) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"workspaceTier must be T3 or T4",
			map[string]any{"field": "workspaceTier"})
		return false
	}
	if r.tenants == nil {
		return true
	}
	row, err := r.tenants.Get(req.Context(), tenant)
	if err != nil {
		return true
	}
	envRank, _ := tenantstore.WorkspaceTierRank(envTier)
	tenantRank, _ := tenantstore.WorkspaceTierRank(row.WorkspaceTier)
	if envRank < tenantRank {
		tenantTier := row.WorkspaceTier
		if tenantTier == "" {
			tenantTier = tenantstore.WorkspaceTierT3
		}
		writeError(w, http.StatusUnprocessableEntity, "CLASSIFICATION_CONTROL_VIOLATION",
			"environment workspaceTier override may only tighten the tenant tier, never loosen it",
			map[string]any{
				"tenantId":   tenant,
				"tier":       envTier,
				"tenantTier": tenantTier,
				"reason":     "tier_override_looser",
			})
		return false
	}
	return true
}

// environmentTierOverrideError is the non-HTTP core of
// requireEnvironmentTierOverride. It returns a non-nil error when the
// environment workspaceTier override is malformed or would loosen the
// tenant's tier (§10.6 / §12.9 CLASSIFICATION_CONTROL_VIOLATION). The
// §17.6 bootstrap environment seed reuses it so a seeded environment is
// held to the same tier-tightening invariant as the live admin POST.
func (r *Router) environmentTierOverrideError(ctx context.Context, tenant, envTier string) error {
	if envTier == "" {
		return nil
	}
	if !tenantstore.ValidWorkspaceTier(envTier) {
		return errors.New("workspaceTier must be T3 or T4")
	}
	if r.tenants == nil {
		return nil
	}
	row, err := r.tenants.Get(ctx, tenant)
	if err != nil {
		return nil
	}
	envRank, _ := tenantstore.WorkspaceTierRank(envTier)
	tenantRank, _ := tenantstore.WorkspaceTierRank(row.WorkspaceTier)
	if envRank < tenantRank {
		return errors.New("environment workspaceTier override may only tighten the tenant tier, never loosen it (CLASSIFICATION_CONTROL_VIOLATION)")
	}
	return nil
}

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

// ConnectorSelectorPayload is the wire shape of a §10.6
// connectorSelector: the tag-based selector fields plus the capability
// allow/deny lists that govern what the selected connectors may do.
// spec: §10.6 lines 595-599. F-10.6.3.
type ConnectorSelectorPayload struct {
	MatchLabels         map[string]string    `json:"matchLabels,omitempty"`
	MatchExpressions    []RequirementPayload `json:"matchExpressions,omitempty"`
	Types               []string             `json:"types,omitempty"`
	Include             []string             `json:"include,omitempty"`
	Exclude             []string             `json:"exclude,omitempty"`
	AllowedCapabilities []string             `json:"allowedCapabilities,omitempty"`
	DeniedCapabilities  []string             `json:"deniedCapabilities,omitempty"`
}

// CrossEnvOutboundRulePayload is one §10.6 outbound cross-environment
// delegation rule. spec: §10.6 lines 613-625 — the outbound declaration
// names the permitted delegation target under `targetEnvironment`. The
// bilateral field names carry the direction semantics, so the wire shape
// keeps them distinct from the inbound `sourceEnvironment` rather than
// collapsing both onto a single `environment` field. F-10.6.4.
type CrossEnvOutboundRulePayload struct {
	TargetEnvironment string          `json:"targetEnvironment"`
	Runtimes          SelectorPayload `json:"runtimes"`
}

// CrossEnvInboundRulePayload is one §10.6 inbound cross-environment
// delegation rule. spec: §10.6 lines 613-625 — the inbound declaration
// names the permitted delegation source under `sourceEnvironment` ("*"
// matches any source). F-10.6.4.
type CrossEnvInboundRulePayload struct {
	SourceEnvironment string          `json:"sourceEnvironment"`
	Runtimes          SelectorPayload `json:"runtimes"`
}

// CrossEnvDelegationPayload is the §10.6 crossEnvironmentDelegation
// block.
type CrossEnvDelegationPayload struct {
	Outbound []CrossEnvOutboundRulePayload `json:"outbound,omitempty"`
	Inbound  []CrossEnvInboundRulePayload  `json:"inbound,omitempty"`
}

// EnvironmentPayload is the §15.1 environment wire shape.
type EnvironmentPayload struct {
	Name                       string                    `json:"name"`
	TenantID                   string                    `json:"tenantId,omitempty"`
	Description                string                    `json:"description,omitempty"`
	WorkspaceTier              string                    `json:"workspaceTier,omitempty"`
	Members                    []MemberPayload           `json:"members,omitempty"`
	RuntimeSelector            SelectorPayload           `json:"runtimeSelector"`
	MCPRuntimeFilters          []MCPRuntimeFilterPayload `json:"mcpRuntimeFilters,omitempty"`
	ConnectorSelector          ConnectorSelectorPayload  `json:"connectorSelector"`
	DefaultDelegationPolicy    string                    `json:"defaultDelegationPolicy,omitempty"`
	CrossEnvironmentDelegation CrossEnvDelegationPayload `json:"crossEnvironmentDelegation"`
	CreatedAt                  string                    `json:"createdAt,omitempty"`
	UpdatedAt                  string                    `json:"updatedAt,omitempty"`
	// ETag is the §15.1 optimistic-concurrency entity tag — the quoted
	// decimal version. A list consumer reads it per item to supply
	// If-Match on a later PUT without a follow-up GET. spec: §15.1 line 1209.
	ETag string `json:"etag,omitempty"`
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

func toConnectorSelector(p ConnectorSelectorPayload) environmentstore.ConnectorSelector {
	return environmentstore.ConnectorSelector{
		Selector: toSelector(SelectorPayload{
			MatchLabels: p.MatchLabels, MatchExpressions: p.MatchExpressions,
			Types: p.Types, Include: p.Include, Exclude: p.Exclude,
		}),
		AllowedCapabilities: p.AllowedCapabilities,
		DeniedCapabilities:  p.DeniedCapabilities,
	}
}

func fromConnectorSelector(c environmentstore.ConnectorSelector) ConnectorSelectorPayload {
	s := fromSelector(c.Selector)
	return ConnectorSelectorPayload{
		MatchLabels: s.MatchLabels, MatchExpressions: s.MatchExpressions,
		Types: s.Types, Include: s.Include, Exclude: s.Exclude,
		AllowedCapabilities: c.AllowedCapabilities,
		DeniedCapabilities:  c.DeniedCapabilities,
	}
}

func toCrossEnvOutbound(ps []CrossEnvOutboundRulePayload) []environmentstore.CrossEnvRule {
	if len(ps) == 0 {
		return nil
	}
	out := make([]environmentstore.CrossEnvRule, len(ps))
	for i, p := range ps {
		out[i] = environmentstore.CrossEnvRule{Environment: p.TargetEnvironment, Runtimes: toSelector(p.Runtimes)}
	}
	return out
}

func toCrossEnvInbound(ps []CrossEnvInboundRulePayload) []environmentstore.CrossEnvRule {
	if len(ps) == 0 {
		return nil
	}
	out := make([]environmentstore.CrossEnvRule, len(ps))
	for i, p := range ps {
		out[i] = environmentstore.CrossEnvRule{Environment: p.SourceEnvironment, Runtimes: toSelector(p.Runtimes)}
	}
	return out
}

func fromCrossEnvOutbound(rs []environmentstore.CrossEnvRule) []CrossEnvOutboundRulePayload {
	if len(rs) == 0 {
		return nil
	}
	out := make([]CrossEnvOutboundRulePayload, len(rs))
	for i, r := range rs {
		out[i] = CrossEnvOutboundRulePayload{TargetEnvironment: r.Environment, Runtimes: fromSelector(r.Runtimes)}
	}
	return out
}

func fromCrossEnvInbound(rs []environmentstore.CrossEnvRule) []CrossEnvInboundRulePayload {
	if len(rs) == 0 {
		return nil
	}
	out := make([]CrossEnvInboundRulePayload, len(rs))
	for i, r := range rs {
		out[i] = CrossEnvInboundRulePayload{SourceEnvironment: r.Environment, Runtimes: fromSelector(r.Runtimes)}
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
		WorkspaceTier:           p.WorkspaceTier,
		Members:                 members,
		RuntimeSelector:         toSelector(p.RuntimeSelector),
		MCPRuntimeFilters:       filters,
		ConnectorSelector:       toConnectorSelector(p.ConnectorSelector),
		DefaultDelegationPolicy: p.DefaultDelegationPolicy,
		CrossEnvOutbound:        toCrossEnvOutbound(p.CrossEnvironmentDelegation.Outbound),
		CrossEnvInbound:         toCrossEnvInbound(p.CrossEnvironmentDelegation.Inbound),
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
		WorkspaceTier:           e.WorkspaceTier,
		Members:                 members,
		RuntimeSelector:         fromSelector(e.RuntimeSelector),
		MCPRuntimeFilters:       filters,
		ConnectorSelector:       fromConnectorSelector(e.ConnectorSelector),
		DefaultDelegationPolicy: e.DefaultDelegationPolicy,
		CrossEnvironmentDelegation: CrossEnvDelegationPayload{
			Outbound: fromCrossEnvOutbound(e.CrossEnvOutbound),
			Inbound:  fromCrossEnvInbound(e.CrossEnvInbound),
		},
		CreatedAt: rfc3339Nano(e.CreatedAt),
		UpdatedAt: rfc3339Nano(e.UpdatedAt),
		// spec: §15.1 line 1207 — the ETag is the quoted decimal version.
		ETag: formatETag(e.Version),
	}
}

// EnvironmentDryRunPreview is the §15.1 environments dry-run preview
// object. It lists the runtimes and connectors the environment's §10.6
// selectors admit at evaluation time and any selector terms that matched
// zero resources (a likely label-key or label-value typo).
type EnvironmentDryRunPreview struct {
	MatchedRuntimes        []string `json:"matchedRuntimes"`
	MatchedConnectors      []string `json:"matchedConnectors"`
	UnmatchedSelectorTerms []string `json:"unmatchedSelectorTerms"`
}

// EnvironmentDryRunResponse is the §15.1 environments dry-run body: the
// computed resource alongside the selector preview. spec: §15.1 line 1140.
type EnvironmentDryRunResponse struct {
	Resource EnvironmentPayload       `json:"resource"`
	Preview  EnvironmentDryRunPreview `json:"preview"`
}

// selectorTerm is one label term of an environment selector paired with
// a single-term selector that evaluates it in isolation, so a term that
// admits zero candidates is reported as an unmatched (likely typo) term.
type selectorTerm struct {
	label string
	sel   environment.Selector
}

// selectorTerms decomposes a selector into its individual label terms
// (matchLabels entries and matchExpressions requirements). The types,
// include, and exclude overrides are not label terms and are skipped.
func selectorTerms(s environment.Selector) []selectorTerm {
	terms := make([]selectorTerm, 0, len(s.MatchLabels)+len(s.MatchExpressions))
	for k, v := range s.MatchLabels {
		terms = append(terms, selectorTerm{
			label: k + "=" + v,
			sel:   environment.Selector{MatchLabels: map[string]string{k: v}},
		})
	}
	for _, req := range s.MatchExpressions {
		terms = append(terms, selectorTerm{
			label: formatRequirement(req),
			sel:   environment.Selector{MatchExpressions: []environment.Requirement{req}},
		})
	}
	return terms
}

// formatRequirement renders a matchExpressions requirement as a readable
// selector term for the dry-run preview.
func formatRequirement(req environment.Requirement) string {
	switch req.Operator {
	case environment.OpExists:
		return req.Key + " exists"
	case environment.OpDoesNotExist:
		return req.Key + " does not exist"
	case environment.OpNotIn:
		return req.Key + " notin (" + strings.Join(req.Values, ", ") + ")"
	default: // OpIn
		return req.Key + " in (" + strings.Join(req.Values, ", ") + ")"
	}
}

// environmentDryRunPreview evaluates the environment's §10.6 selectors
// against the current runtime and connector registries for the §15.1
// dry-run preview. It performs no writes. spec: §15.1 line 1140.
func (r *Router) environmentDryRunPreview(ctx context.Context, env environmentstore.Environment) EnvironmentDryRunPreview {
	preview := EnvironmentDryRunPreview{
		MatchedRuntimes:        []string{},
		MatchedConnectors:      []string{},
		UnmatchedSelectorTerms: []string{},
	}
	if r.runtimes != nil {
		runtimes, _ := r.runtimes.List(ctx, runtimestore.ListFilter{})
		for _, rt := range runtimes {
			if env.RuntimeSelector.Matches(environment.Candidate{Name: rt.Name, Type: string(rt.Type), Labels: rt.Labels}) {
				preview.MatchedRuntimes = append(preview.MatchedRuntimes, rt.Name)
			}
		}
		for _, term := range selectorTerms(env.RuntimeSelector) {
			if !anyRuntimeMatches(runtimes, term.sel) {
				preview.UnmatchedSelectorTerms = append(preview.UnmatchedSelectorTerms, term.label)
			}
		}
	}
	if r.connectors != nil {
		connectors, _ := r.connectors.List(ctx, env.TenantID, connectorstore.ListFilter{})
		for _, c := range connectors {
			if env.ConnectorSelector.Selector.Matches(environment.Candidate{Name: c.ID, Labels: c.Labels}) {
				preview.MatchedConnectors = append(preview.MatchedConnectors, c.ID)
			}
		}
		for _, term := range selectorTerms(env.ConnectorSelector.Selector) {
			if !anyConnectorMatches(connectors, term.sel) {
				preview.UnmatchedSelectorTerms = append(preview.UnmatchedSelectorTerms, term.label)
			}
		}
	}
	return preview
}

func anyRuntimeMatches(rts []runtimestore.Runtime, sel environment.Selector) bool {
	for _, rt := range rts {
		if sel.Matches(environment.Candidate{Name: rt.Name, Type: string(rt.Type), Labels: rt.Labels}) {
			return true
		}
	}
	return false
}

func anyConnectorMatches(cs []connectorstore.Connector, sel environment.Selector) bool {
	for _, c := range cs {
		if sel.Matches(environment.Candidate{Name: c.ID, Labels: c.Labels}) {
			return true
		}
	}
	return false
}

func (r *Router) handleCreateEnvironment(w http.ResponseWriter, req *http.Request) {
	var body EnvironmentPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
	// spec: §11.7 line 449 — environment creation under a regulated
	// tenant re-checks audit.siem.endpoint presence on every request and
	// rejects with COMPLIANCE_SIEM_REQUIRED when SIEM has become absent
	// since the tenant was created. The parent tenant's complianceProfile
	// is read fresh so a SIEM removal cannot be papered over by an
	// already-admitted tenant row.
	if row, gErr := r.tenants.Get(req.Context(), tenant); gErr == nil && r.requireSIEMForProfile(row.ComplianceProfile) {
		writeError(w, http.StatusUnprocessableEntity, "COMPLIANCE_SIEM_REQUIRED",
			complianceSIEMRequiredMessage(row.ComplianceProfile),
			map[string]any{"tenantId": tenant, "complianceProfile": row.ComplianceProfile})
		return
	}
	// spec: §11.7 line 377 — environment creation under a regulated tenant
	// also re-checks pgaudit configuration on every request and rejects
	// with COMPLIANCE_PGAUDIT_REQUIRED when pgaudit has become absent.
	if row, gErr := r.tenants.Get(req.Context(), tenant); gErr == nil && r.requirePgauditForProfile(row.ComplianceProfile) {
		writeError(w, http.StatusUnprocessableEntity, "COMPLIANCE_PGAUDIT_REQUIRED",
			compliancePgauditRequiredMessage(row.ComplianceProfile),
			map[string]any{"tenantId": tenant, "complianceProfile": row.ComplianceProfile})
		return
	}
	if !r.requireEnvironmentTierOverride(w, req, tenant, body.WorkspaceTier) {
		return
	}
	env := toEnvironment(body, tenant)
	// spec: §15.1 line 1140 — ?dryRun=true validates the definition and
	// returns the selector preview without persisting or auditing.
	if req.URL.Query().Get("dryRun") == "true" {
		if err := env.Validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusCreated, EnvironmentDryRunResponse{
			Resource: fromEnvironment(env),
			Preview:  r.environmentDryRunPreview(req.Context(), env),
		})
		return
	}
	if err := r.environments.Create(req.Context(), env); err != nil {
		if errors.Is(err, environmentstore.ErrAlreadyExists) {
			// spec: §15.1 line 983 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
			writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS",
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
	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope. F-15.1.6.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(x EnvironmentPayload, s pagination.Sort) (string, string) {
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
	// spec: §15.1 line 1209 — GET responses carry the ETag header so the
	// client can use it as the next PUT's If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromEnvironment(row))
}

func (r *Router) handleUpdateEnvironment(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body EnvironmentPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
	if !r.requireEnvironmentTierOverride(w, req, tenant, body.WorkspaceTier) {
		return
	}
	desired := toEnvironment(body, tenant)
	// spec: §15.1 lines 1207-1211 — every admin PUT requires If-Match.
	// Resolve the current environment first so the entity tag (its version)
	// is known before applying the mutation. This runs before the dry-run
	// branch so a dry-run with a stale If-Match still returns 412 and one
	// with no If-Match still returns 428. A missing environment 404s ahead
	// of the precondition.
	current, gErr := r.environments.Get(req.Context(), tenant, name)
	if gErr != nil {
		if errors.Is(gErr, environmentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "environment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gErr.Error(), nil)
		return
	}
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	// spec: §15.1 line 1140 — ?dryRun=true validates the merged definition
	// and returns the selector preview without persisting or auditing. The
	// preview reflects the real (unchanged) name and creation time.
	if req.URL.Query().Get("dryRun") == "true" {
		desired.Name = current.Name
		desired.CreatedAt = current.CreatedAt
		if err := desired.Validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, EnvironmentDryRunResponse{
			Resource: fromEnvironment(desired),
			Preview:  r.environmentDryRunPreview(req.Context(), desired),
		})
		return
	}
	updated, err := r.environments.Update(req.Context(), tenant, name, func(e *environmentstore.Environment) error {
		desired.Name = e.Name
		desired.CreatedAt = e.CreatedAt
		// spec: §15.1 line 1207 — a full-replace PUT must carry the stored
		// entity-tag version forward so the store's increment is monotonic.
		desired.Version = e.Version
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
	// spec: §15.1 line 1210 — a successful PUT carries the bumped ETag so
	// the client can chain a subsequent write without a refresh GET.
	w.Header().Set("ETag", formatETag(updated.Version))
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
	// spec: §15.1 line 1213 — DELETE honours If-Match when present. Resolve
	// the current environment so the precondition reads the stored entity
	// tag; a missing environment 404s ahead of the precondition.
	current, gErr := r.environments.Get(req.Context(), tenant, name)
	if gErr != nil {
		if errors.Is(gErr, environmentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "environment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gErr.Error(), nil)
		return
	}
	if !enforceIfMatchIfPresent(w, req, current.Version) {
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
