// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/agentcard"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// applyGeneratedCard regenerates the §5.1 A2A agent card from the
// runtime's agentInterface and writes it into PublishedMetadata under
// the agent-card key, replacing any existing entry with that key. It is
// a no-op when the runtime carries no agentInterface, so a hand-crafted
// agent-card entry on a runtime without an agentInterface is left
// untouched per §5.1.
func (r *Router) applyGeneratedCard(rt *runtimestore.Runtime, at time.Time) {
	if rt.AgentInterface == nil {
		return
	}
	version := r.platformInfo.Version
	if version == "" {
		version = "dev"
	}
	entry := agentcard.Entry(rt.Name, *rt.AgentInterface, at, version)
	out := make([]runtimestore.PublishedMetadataEntry, 0, len(rt.PublishedMetadata)+1)
	for _, e := range rt.PublishedMetadata {
		if e.Key != agentcard.Key {
			out = append(out, e)
		}
	}
	rt.PublishedMetadata = append(out, entry)
}

// RuntimePayload is the §15.1 admin-runtime request/response body.
type RuntimePayload struct {
	Name                string                                `json:"name"`
	Type                string                                `json:"type,omitempty"`
	Image               string                                `json:"image,omitempty"`
	ExecutionMode       string                                `json:"executionMode,omitempty"`
	IsolationProfile    string                                `json:"isolationProfile,omitempty"`
	IntegrationLevel    string                                `json:"integrationLevel,omitempty"`
	Description         string                                `json:"description,omitempty"`
	DelegationPolicyRef string                                `json:"delegationPolicyRef,omitempty"`
	Labels              map[string]string                     `json:"labels,omitempty"`
	AgentInterface      *runtimestore.AgentInterface          `json:"agentInterface,omitempty"`
	PublishedMetadata   []runtimestore.PublishedMetadataEntry `json:"publishedMetadata,omitempty"`
	CreatedAt           string                                `json:"createdAt,omitempty"`
	UpdatedAt           string                                `json:"updatedAt,omitempty"`
	DeletedAt           string                                `json:"deletedAt,omitempty"`
}

// UpdateRuntimeRequest is the §15.1 PUT body. Optional pointer
// fields signal "leave unchanged when omitted". AgentInterface is a
// raw message so the three states omitted, JSON null, and an object
// stay distinct: omitted leaves the descriptor unchanged, null clears
// it, and an object replaces it.
type UpdateRuntimeRequest struct {
	Image               *string                                `json:"image,omitempty"`
	ExecutionMode       *string                                `json:"executionMode,omitempty"`
	IsolationProfile    *string                                `json:"isolationProfile,omitempty"`
	IntegrationLevel    *string                                `json:"integrationLevel,omitempty"`
	Description         *string                                `json:"description,omitempty"`
	DelegationPolicyRef *string                                `json:"delegationPolicyRef,omitempty"`
	Labels              *map[string]string                     `json:"labels,omitempty"`
	AgentInterface      json.RawMessage                        `json:"agentInterface,omitempty"`
	PublishedMetadata   *[]runtimestore.PublishedMetadataEntry `json:"publishedMetadata,omitempty"`
}

// errAgentInterfaceOnMCP is returned from the update mutate closure when
// a PUT attaches an agentInterface to a type:mcp runtime (§5.1).
var errAgentInterfaceOnMCP = errors.New("agentInterface is not valid on a type:mcp runtime")

func fromRuntime(r runtimestore.Runtime) RuntimePayload {
	out := RuntimePayload{
		Name:                r.Name,
		Type:                string(r.Type),
		Image:               r.Image,
		ExecutionMode:       string(r.ExecutionMode),
		IsolationProfile:    string(r.IsolationProfile),
		IntegrationLevel:    string(r.IntegrationLevel),
		Description:         r.Description,
		DelegationPolicyRef: r.DelegationPolicyRef,
		Labels:              r.Labels,
		AgentInterface:      r.AgentInterface,
		PublishedMetadata:   r.PublishedMetadata,
		CreatedAt:           rfc3339Nano(r.CreatedAt),
		UpdatedAt:           rfc3339Nano(r.UpdatedAt),
	}
	if !r.DeletedAt.IsZero() {
		out.DeletedAt = rfc3339Nano(r.DeletedAt)
	}
	return out
}

// WithRuntimes wires the §15.1 runtime CRUD handlers onto the Router.
// Call before Handler() so the mux picks them up.
func (r *Router) WithRuntimes(s runtimestore.Store) *Router {
	r.runtimes = s
	return r
}

// validatePayloadEnums applies the closed-enum checks the store does
// not — runtimes.Memory accepts arbitrary string values for the enum
// fields so the admin handler is the authoritative validator.
func (p RuntimePayload) validatePayloadEnums() error {
	if p.Type != "" && !runtimestore.RuntimeType(p.Type).IsValid() {
		return errors.New("type is not a recognised runtime type")
	}
	if p.ExecutionMode != "" && !runtimestore.ExecutionMode(p.ExecutionMode).IsValid() {
		return errors.New("executionMode is not a recognised mode")
	}
	if p.IsolationProfile != "" && !isolation.IsValid(isolation.Profile(p.IsolationProfile)) {
		return errors.New("isolationProfile is not a recognised §5.3 profile")
	}
	if p.IntegrationLevel != "" && !runtimestore.IntegrationLevel(p.IntegrationLevel).IsValid() {
		return errors.New("integrationLevel is not a recognised level")
	}
	if p.Image != "" {
		// §5.1 / §13.1: digest-pinned references only. Accept the
		// common forms `image@sha256:...` and `image:tag@sha256:...`.
		if !strings.Contains(p.Image, "@sha256:") {
			return errors.New("image must be digest-pinned (contain @sha256:...)")
		}
	}
	return nil
}

func (r *Router) handleCreateRuntime(w http.ResponseWriter, req *http.Request) {
	var body RuntimePayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if err := runtimestore.ValidateName(body.Name); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "name"})
		return
	}
	if err := body.validatePayloadEnums(); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := runtimestore.ValidatePublishedMetadata(body.PublishedMetadata); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	rt := runtimestore.Runtime{
		Name:                body.Name,
		Type:                runtimestore.RuntimeType(body.Type),
		Image:               body.Image,
		ExecutionMode:       runtimestore.ExecutionMode(body.ExecutionMode),
		IsolationProfile:    isolation.Profile(body.IsolationProfile),
		IntegrationLevel:    runtimestore.IntegrationLevel(body.IntegrationLevel),
		Description:         body.Description,
		DelegationPolicyRef: body.DelegationPolicyRef,
		Labels:              body.Labels,
		AgentInterface:      body.AgentInterface,
		PublishedMetadata:   body.PublishedMetadata,
		CreatedAt:           r.clock(),
	}
	runtimestore.ApplyDefaults(&rt)
	rt.UpdatedAt = rt.CreatedAt
	// §5.1: type:mcp runtimes do not carry an agentInterface.
	if rt.Type == runtimestore.TypeMCP && rt.AgentInterface != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"agentInterface is not valid on a type:mcp runtime", nil)
		return
	}
	// §5.1: a runtime with an agentInterface gets a write-time
	// auto-generated A2A agent card stored as a publishedMetadata entry.
	r.applyGeneratedCard(&rt, rt.CreatedAt)
	if err := r.runtimes.Create(req.Context(), rt); err != nil {
		if errors.Is(err, runtimestore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"runtime with this name already exists",
				map[string]any{"name": body.Name})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.runtimes.Get(req.Context(), body.Name)
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.runtime.created", body.Name, map[string]any{
		"type":             string(stored.Type),
		"executionMode":    string(stored.ExecutionMode),
		"isolationProfile": string(stored.IsolationProfile),
		"integrationLevel": string(stored.IntegrationLevel),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromRuntime(stored))
}

func (r *Router) handleListRuntimes(w http.ResponseWriter, req *http.Request) {
	filter := runtimestore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
		Type:           runtimestore.RuntimeType(req.URL.Query().Get("type")),
	}
	rows, err := r.runtimes.List(req.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4: a tenant-admin sees only runtimes granted to their tenant
	// through the runtime_tenant_access table; platform-admin is
	// unfiltered.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		allowed := r.accessibleSet(req.Context(), tenantaccessstore.KindRuntime, tenantID)
		kept := rows[:0]
		for _, rt := range rows {
			if allowed[rt.Name] {
				kept = append(kept, rt)
			}
		}
		rows = kept
	}
	out := make([]RuntimePayload, 0, len(rows))
	for _, rt := range rows {
		out = append(out, fromRuntime(rt))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"runtimes": out})
}

func (r *Router) handleGetRuntime(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	row, err := r.runtimes.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, runtimestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4: a tenant-admin may read a runtime only when their tenant
	// holds an access grant for it.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		if !r.accessibleSet(req.Context(), tenantaccessstore.KindRuntime, tenantID)[row.Name] {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromRuntime(row))
}

func (r *Router) handleUpdateRuntime(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body UpdateRuntimeRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	// Validate enums when present.
	if body.ExecutionMode != nil && *body.ExecutionMode != "" && !runtimestore.ExecutionMode(*body.ExecutionMode).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"executionMode is not a recognised mode", nil)
		return
	}
	if body.IsolationProfile != nil && *body.IsolationProfile != "" && !isolation.IsValid(isolation.Profile(*body.IsolationProfile)) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"isolationProfile is not a recognised §5.3 profile", nil)
		return
	}
	if body.IntegrationLevel != nil && *body.IntegrationLevel != "" && !runtimestore.IntegrationLevel(*body.IntegrationLevel).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"integrationLevel is not a recognised level", nil)
		return
	}
	if body.Image != nil && *body.Image != "" && !strings.Contains(*body.Image, "@sha256:") {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"image must be digest-pinned (contain @sha256:...)", nil)
		return
	}
	// agentInterface: an omitted key leaves the descriptor unchanged; a
	// present key (JSON null or an object) is parsed here so malformed
	// JSON fails as 400 before the store transaction opens.
	agentInterfaceSet := len(body.AgentInterface) > 0
	var newAgentInterface *runtimestore.AgentInterface
	if agentInterfaceSet {
		if err := json.Unmarshal(body.AgentInterface, &newAgentInterface); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"agentInterface is not valid JSON", nil)
			return
		}
	}
	if body.PublishedMetadata != nil {
		if err := runtimestore.ValidatePublishedMetadata(*body.PublishedMetadata); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	updated, err := r.runtimes.Update(req.Context(), name, func(rt *runtimestore.Runtime) error {
		if body.Image != nil {
			rt.Image = *body.Image
		}
		if body.ExecutionMode != nil {
			rt.ExecutionMode = runtimestore.ExecutionMode(*body.ExecutionMode)
		}
		if body.IsolationProfile != nil {
			rt.IsolationProfile = isolation.Profile(*body.IsolationProfile)
		}
		if body.IntegrationLevel != nil {
			rt.IntegrationLevel = runtimestore.IntegrationLevel(*body.IntegrationLevel)
		}
		if body.Description != nil {
			rt.Description = *body.Description
		}
		if body.Labels != nil {
			rt.Labels = *body.Labels
		}
		if body.DelegationPolicyRef != nil {
			rt.DelegationPolicyRef = *body.DelegationPolicyRef
		}
		if agentInterfaceSet {
			// §5.1: type:mcp runtimes do not carry an agentInterface.
			if rt.Type == runtimestore.TypeMCP && newAgentInterface != nil {
				return errAgentInterfaceOnMCP
			}
			rt.AgentInterface = newAgentInterface
		}
		if body.PublishedMetadata != nil {
			rt.PublishedMetadata = *body.PublishedMetadata
		}
		// §5.1: regenerate the auto-generated A2A agent card at write
		// time whenever the runtime carries an agentInterface.
		r.applyGeneratedCard(rt, r.clock())
		return nil
	})
	if err != nil {
		if errors.Is(err, runtimestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
			return
		}
		if errors.Is(err, errAgentInterfaceOnMCP) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
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
	r.emit(req.Context(), principal, "admin.runtime.updated", name, map[string]any{
		"changedFields": changedRuntimeFields(body),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromRuntime(updated))
}

func (r *Router) handleDeleteRuntime(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if err := r.runtimes.SoftDelete(req.Context(), name, r.clock()); err != nil {
		if errors.Is(err, runtimestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
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
	r.emit(req.Context(), principal, "admin.runtime.soft_deleted", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func changedRuntimeFields(b UpdateRuntimeRequest) []string {
	var out []string
	if b.Image != nil {
		out = append(out, "image")
	}
	if b.ExecutionMode != nil {
		out = append(out, "executionMode")
	}
	if b.IsolationProfile != nil {
		out = append(out, "isolationProfile")
	}
	if b.IntegrationLevel != nil {
		out = append(out, "integrationLevel")
	}
	if b.Description != nil {
		out = append(out, "description")
	}
	if len(b.AgentInterface) > 0 {
		out = append(out, "agentInterface")
	}
	if b.PublishedMetadata != nil {
		out = append(out, "publishedMetadata")
	}
	return out
}
