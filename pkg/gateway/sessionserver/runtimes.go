// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/envaccess"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
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
// per-runtime agentInterface descriptor, and the §5.1 publishedMetadata
// refs; the §9.1 mcpEndpoint block is not yet surfaced. The §9.1
// adapterCapabilities block is a top-level response field, not a
// per-runtime one.
type RuntimeDiscoveryEntry struct {
	Name              string                              `json:"name"`
	Type              string                              `json:"type,omitempty"`
	IntegrationLevel  string                              `json:"integrationLevel,omitempty"`
	Description       string                              `json:"description,omitempty"`
	Labels            map[string]string                   `json:"labels,omitempty"`
	AgentInterface    *runtimestore.AgentInterface        `json:"agentInterface,omitempty"`
	PublishedMetadata []runtimestore.PublishedMetadataRef `json:"publishedMetadata,omitempty"`
}

// handleListRuntimes implements GET /v1/runtimes — the §9.1 REST
// runtime-discovery surface. Results are identity-filtered by §10.6
// environment access: a caller sees only the runtimes its environment
// membership authorizes. A not-authorized runtime is simply absent
// from the list, so the response does not enable enumeration.
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
	rows = s.filterRuntimesByEnvironment(r, rows)

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
	rt, err := s.runtimes.Get(r.Context(), name)
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

// filterRuntimesByEnvironment narrows a runtime list to the §10.6
// environment access the request principal holds. When the
// environment or tenant registry is not wired, or the request carries
// no principal, the list is returned unchanged — the transparent
// filter is not in effect.
func (s *Server) filterRuntimesByEnvironment(r *http.Request, runtimes []runtimestore.Runtime) []runtimestore.Runtime {
	if s.environments == nil || s.tenants == nil {
		return runtimes
	}
	principal, ok := getPrincipal(r)
	if !ok || principal.TenantID == "" {
		return runtimes
	}
	envs, err := s.environments.List(r.Context(), principal.TenantID)
	if err != nil {
		return runtimes
	}
	policy := s.defaultNoEnvPolicy
	if tenant, err := s.tenants.Get(r.Context(), principal.TenantID); err == nil && tenant.NoEnvironmentPolicy != "" {
		policy = tenant.NoEnvironmentPolicy
	}
	caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
	return envaccess.AuthorizedRuntimes(caller, envs, runtimes, policy)
}
