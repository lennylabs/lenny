// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/envaccess"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
)

// restAdapterCapabilities reports the §15 AdapterCapabilities of the REST
// adapter the sessionserver implements. The REST surface owns the /v1
// path prefix. It persists sessions and serves the resume, interrupt, and
// elicitation-respond endpoints, so it reports session continuity,
// interrupt, and elicitation support. Delegation is an MCP platform tool
// with no REST route, so SupportsDelegation is false. §9.1 requires every
// discovery response to embed this block.
func restAdapterCapabilities() adapter.Capabilities {
	return adapter.Capabilities{
		PathPrefix:                "/v1",
		Protocol:                  "rest",
		SupportsSessionContinuity: true,
		SupportsDelegation:        false,
		SupportsElicitation:       true,
		SupportsInterrupt:         true,
	}
}

// RuntimeDiscoveryEntry is one runtime in the §9.1 GET /v1/runtimes
// discovery response. v1 reports the §5.1 registry fields, the §9.1
// per-runtime agentInterface descriptor, the §5.1 publishedMetadata
// refs, and the §9.1 mcpEndpoint pointer for type:mcp runtimes. The
// §9.1 adapterCapabilities block is a top-level response field, not a
// per-runtime one.
type RuntimeDiscoveryEntry struct {
	Name              string                              `json:"name"`
	Type              string                              `json:"type,omitempty"`
	IntegrationLevel  string                              `json:"integrationLevel,omitempty"`
	Description       string                              `json:"description,omitempty"`
	Labels            map[string]string                   `json:"labels,omitempty"`
	AgentInterface    *runtimestore.AgentInterface        `json:"agentInterface,omitempty"`
	PublishedMetadata []runtimestore.PublishedMetadataRef `json:"publishedMetadata,omitempty"`
	// McpEndpoint is the §9.1 line 38 / §15.1 line 698 discovery
	// pointer to the per-runtime intra-pod MCP server. Populated as
	// `/mcp/runtimes/{name}` for type:mcp runtimes; empty (and so
	// omitted from the JSON envelope) for type:agent runtimes. F-9.1.4.
	McpEndpoint string `json:"mcpEndpoint,omitempty"`
}

// mcpEndpointFor returns the §9.1 discovery pointer for runtime
// `name` — `/mcp/runtimes/{name}` for `type:mcp`, empty otherwise.
// F-9.1.4 / coordinated with F-9.1.3.
func mcpEndpointFor(rt runtimestore.Runtime) string {
	if rt.Type != runtimestore.TypeMCP {
		return ""
	}
	return "/mcp/runtimes/" + rt.Name
}

// handleListRuntimes implements GET /v1/runtimes — the §9.1 REST
// runtime-discovery surface. Results are identity-filtered by §10.6
// environment access: a caller sees only the runtimes its environment
// membership authorizes. A not-authorized runtime is simply absent
// from the list, so the response does not enable enumeration.
//
// spec: §10.6 line 672 — accept the optional `?environmentId=` stub.
// When non-empty, the result is further narrowed to runtimes that
// belong to the named environment; the caller still has to be a
// member, so a runtime not in the named environment is dropped from
// the response. An empty value is the v1 default (no narrowing).
// F-10.6.10.
func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.runtimes == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runtimes":            []RuntimeDiscoveryEntry{},
			"adapterCapabilities": restAdapterCapabilities(),
		})
		return
	}
	rows, err := s.runtimes.List(r.Context(), runtimestore.ListFilter{})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	rows = s.resolveRuntimes(r.Context(), rows)
	rows = s.filterRuntimesByEnvironment(r, rows)
	if envID := r.URL.Query().Get("environmentId"); envID != "" {
		rows = s.narrowRuntimesToEnvironment(r, rows, envID)
	}

	out := make([]RuntimeDiscoveryEntry, 0, len(rows))
	for _, rt := range rows {
		out = append(out, RuntimeDiscoveryEntry{
			Name:              rt.Name,
			Type:              string(rt.Type),
			IntegrationLevel:  string(rt.IntegrationLevel),
			Description:       rt.Description,
			Labels:            rt.Labels,
			AgentInterface:    rt.AgentInterface,
			PublishedMetadata: runtimestore.PublicMetadataRefs(rt.PublishedMetadata),
			McpEndpoint:       mcpEndpointFor(rt),
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"runtimes":            out,
		"adapterCapabilities": restAdapterCapabilities(),
	})
}

// OpenAIModel is one entry in the GET /v1/models OpenAI-compatible
// model list. Each Lenny runtime is surfaced as a model so the
// OpenAI Completions and Open Responses adapters can discover targets.
type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// handleListModels implements GET /v1/models — the §9.1 OpenAI
// Completions / Open Responses model-discovery surface. Each runtime
// the caller's §10.6 environment access authorizes is surfaced as an
// OpenAI-compatible model object; a not-authorized runtime is absent.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	models := []OpenAIModel{}
	if s.runtimes != nil {
		rows, err := s.runtimes.List(r.Context(), runtimestore.ListFilter{})
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		rows = s.resolveRuntimes(r.Context(), rows)
		rows = s.filterRuntimesByEnvironment(r, rows)
		for _, rt := range rows {
			models = append(models, OpenAIModel{
				ID:      rt.Name,
				Object:  "model",
				Created: rt.CreatedAt.Unix(),
				OwnedBy: "lenny",
			})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object":              "list",
		"data":                models,
		"adapterCapabilities": restAdapterCapabilities(),
	})
}

// handleRuntimeMeta implements GET /v1/runtimes/{name}/meta/{key} — the
// §5.1 public publishedMetadata fetch endpoint. It serves an entry only
// when the entry's visibility class is public. Per §5.1 a missing
// runtime, a soft-deleted runtime, a missing key, and a non-public
// entry all return an identical 404, so the endpoint does not enable
// enumeration. Content is served opaquely under the entry's declared
// content type; the gateway never parses it.
func (s *Server) handleRuntimeMeta(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	key := r.PathValue("key")
	if s.runtimes == nil {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "metadata not found", nil)
		return
	}
	// §5.1: a derived runtime resolves to its effective definition, so
	// the meta lookup sees the publishedMetadata it inherits from its
	// base. A derived runtime with a missing base resolves to an error.
	rt, err := runtimestore.Resolve(r.Context(), s.runtimes, name)
	if err != nil || !rt.IsActive() {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "metadata not found", nil)
		return
	}
	for _, e := range rt.PublishedMetadata {
		if e.Key != key || e.Visibility != runtimestore.VisibilityPublic {
			continue
		}
		ct := e.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write([]byte(e.Content))
		return
	}
	s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "metadata not found", nil)
}

// handleInternalRuntimeMeta implements GET /internal/runtimes/{name}/meta/{key}
// — the §5.1 internal and tenant publishedMetadata fetch endpoint. It
// requires an authenticated session principal. An internal-visibility
// entry is served to any authenticated caller; a tenant-visibility
// entry is served only when the caller's tenant holds a §4 tenant-access
// grant for the runtime. Per §5.1 a missing principal, a missing or
// soft-deleted runtime, a missing key, a public entry (served instead
// at /v1/runtimes/{name}/meta/{key}), and a tenant entry the caller's
// tenant cannot reach all return an identical 404, so the endpoint does
// not enable enumeration.
func (s *Server) handleInternalRuntimeMeta(w http.ResponseWriter, r *http.Request) {
	notFound := func() {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "metadata not found", nil)
	}
	principal, ok := getPrincipal(r)
	if !ok || s.runtimes == nil {
		notFound()
		return
	}
	// §5.1: a derived runtime resolves to its effective definition so
	// the meta lookup sees the inherited publishedMetadata.
	rt, err := runtimestore.Resolve(r.Context(), s.runtimes, r.PathValue("name"))
	if err != nil || !rt.IsActive() {
		notFound()
		return
	}
	key := r.PathValue("key")
	for _, e := range rt.PublishedMetadata {
		if e.Key != key {
			continue
		}
		switch e.Visibility {
		case runtimestore.VisibilityInternal:
			// An internal entry is served to any authenticated caller.
		case runtimestore.VisibilityTenant:
			if !s.tenantReachesRuntime(r.Context(), principal.TenantID, rt.Name) {
				notFound()
				return
			}
		default:
			// A public entry is served at /v1/...; anything else is hidden.
			notFound()
			return
		}
		ct := e.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write([]byte(e.Content))
		return
	}
	notFound()
}

// tenantReachesRuntime reports whether tenantID holds a §4 tenant-access
// grant for the runtime. It fails closed when the tenant-access registry
// is not wired or the tenant id is empty.
func (s *Server) tenantReachesRuntime(ctx context.Context, tenantID, runtime string) bool {
	if s.tenantAccess == nil || tenantID == "" {
		return false
	}
	grants, err := s.tenantAccess.List(ctx, tenantaccessstore.KindRuntime, runtime)
	if err != nil {
		return false
	}
	for _, g := range grants {
		if g.TenantID == tenantID {
			return true
		}
	}
	return false
}

// resolveRuntimes replaces each §5.1 derived runtime in rows with its
// effective merged definition, so the discovery surface reports the
// fields a derived runtime inherits from its base rather than its
// (partial) declared form. A derived runtime whose base is missing is
// dropped from the result — it is not usable.
func (s *Server) resolveRuntimes(ctx context.Context, rows []runtimestore.Runtime) []runtimestore.Runtime {
	out := make([]runtimestore.Runtime, 0, len(rows))
	for _, rt := range rows {
		if !rt.IsDerived() {
			out = append(out, rt)
			continue
		}
		eff, err := runtimestore.Resolve(ctx, s.runtimes, rt.Name)
		if err != nil {
			continue
		}
		out = append(out, eff)
	}
	return out
}

// filterRuntimesByEnvironment narrows a runtime list to the §10.6
// transparent-filtering view the request principal holds. It reads the
// Resolution the §10.6 environment-resolver middleware
// (pkg/gateway/middleware/environment) attached to the request context
// — the single filtering path the §9.1 discovery surfaces share with
// the MCP discovery tools. When the environment or tenant registry is
// not wired, or the request carries no principal, the Resolution is not
// Configured and the list is returned unchanged — the transparent
// filter is not in effect.
//
// For a request reaching the sessionserver outside the HTTP middleware
// chain (a unit test that calls the handler directly), the Resolution
// is resolved on demand from the server's registries via the shared
// environmentmw.Resolve, so the filtering result is identical either
// way. A store read failure fails closed: the §10.6 filter is an
// authorization boundary, so an empty runtime list is returned rather
// than an unfiltered one.
func (s *Server) filterRuntimesByEnvironment(r *http.Request, runtimes []runtimestore.Runtime) []runtimestore.Runtime {
	res := environmentmw.FromContext(r.Context())
	if !res.Configured {
		principal, ok := getPrincipal(r)
		if !ok || principal.TenantID == "" {
			return runtimes
		}
		caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
		resolved, err := environmentmw.Resolve(r.Context(), s.environments, s.tenants,
			s.defaultNoEnvPolicy, caller, principal.TenantID)
		if err != nil {
			// Fail closed: the §10.6 transparent filter is an
			// authorization boundary, so a registry read failure must
			// not yield an unfiltered runtime list.
			return []runtimestore.Runtime{}
		}
		res = resolved
	}
	return res.FilterRuntimes(runtimes)
}

// narrowRuntimesToEnvironment further restricts a runtime list to those
// reachable through environmentName's runtimeSelector, on top of the
// §10.6 transparent filter already applied by
// filterRuntimesByEnvironment. It is the `?environmentId=` v1 stub: it
// does not promote access (a runtime the transparent filter already
// excluded is still excluded), it lets the caller scope the response to
// a single named environment. A nil environment registry or an unknown
// environmentName collapses the list to empty — an unknown environment
// is treated as a not-allowed scope so a typo never broadens visibility.
// spec: §10.6 line 672. F-10.6.10.
func (s *Server) narrowRuntimesToEnvironment(r *http.Request, runtimes []runtimestore.Runtime, environmentName string) []runtimestore.Runtime {
	if s.environments == nil || environmentName == "" {
		return runtimes
	}
	tenantID := ""
	if principal, ok := getPrincipal(r); ok {
		tenantID = principal.TenantID
	}
	if tenantID == "" {
		tenantID = r.Header.Get("X-Lenny-Tenant-ID")
	}
	if tenantID == "" {
		return []runtimestore.Runtime{}
	}
	env, err := s.environments.Get(r.Context(), tenantID, environmentName)
	if err != nil || env.Name == "" {
		return []runtimestore.Runtime{}
	}
	out := make([]runtimestore.Runtime, 0, len(runtimes))
	for _, rt := range runtimes {
		if env.RuntimeSelector.Matches(environment.Candidate{
			Name: rt.Name, Type: string(rt.Type), Labels: rt.Labels,
		}) {
			out = append(out, rt)
		}
	}
	return out
}

// resolveEnvironmentForRequest returns the §10.6 Resolution for the
// caller in tenantID — preferring the middleware-attached Resolution
// and falling back to direct Resolve when the request reached the
// handler outside the HTTP middleware boundary. ok reports whether a
// principal was attached; sessionserver paths that already established
// a tenant should treat !ok as a programming error rather than a
// runtime denial. spec: §10.6 environment resolver.
func (s *Server) resolveEnvironmentForRequest(r *http.Request) (environmentmw.Resolution, bool, error) {
	if res := environmentmw.FromContext(r.Context()); res.Configured {
		return res, true, nil
	}
	principal, ok := getPrincipal(r)
	if !ok || principal.TenantID == "" {
		return environmentmw.Resolution{}, false, nil
	}
	caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
	resolved, err := environmentmw.Resolve(r.Context(), s.environments, s.tenants,
		s.defaultNoEnvPolicy, caller, principal.TenantID)
	if err != nil {
		return environmentmw.Resolution{}, true, err
	}
	return resolved, true, nil
}

// requireEnvironmentAdmission enforces the §11.1 line 13 admission
// gate: a session create that does not name an environment is
// admitted only when the caller's tenant resolves to allow-all (or
// when the caller is a member of at least one environment, in which
// case the transparent filter governs runtime access for the
// runtimeRef). The platform default deny-all rejects the request with
// 403 FORBIDDEN; an empty NoEnvironmentPolicy is treated as deny-all
// per §10.6 line 646. Returns true when admission passes. A nil
// environment registry leaves admission open — the gateway runs in a
// posture where §10.6 transparent filtering is disabled. spec: §11.1
// line 13; §10.6 line 643; §15.1 FORBIDDEN.
func (s *Server) requireEnvironmentAdmission(w http.ResponseWriter, r *http.Request, requestedEnvironment string) bool {
	if requestedEnvironment != "" {
		return true
	}
	if s.environments == nil || s.tenants == nil {
		return true
	}
	res, hasPrincipal, err := s.resolveEnvironmentForRequest(r)
	if err != nil {
		// §10.6 store read failure: fail closed.
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"environment registry lookup failed: "+err.Error(), nil)
		return false
	}
	if !hasPrincipal || !res.Configured {
		return true
	}
	if len(res.MemberEnvironments) > 0 {
		return true
	}
	policy := res.Policy
	if policy == "" {
		policy = envaccess.PolicyDenyAll
	}
	if policy == envaccess.PolicyAllowAll {
		return true
	}
	s.writeError(w, http.StatusForbidden, "FORBIDDEN",
		"session creation requires either an environment in the request or the tenant's noEnvironmentPolicy to be allow-all (§11.1, §10.6)",
		map[string]any{
			"reason":               "no_environment_policy_deny_all",
			"noEnvironmentPolicy":  policy,
			"memberEnvironments":   0,
		})
	return false
}
