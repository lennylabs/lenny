// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// ConnectorPayload is the §9.3 / §15.1 admin-connector wire shape.
type ConnectorPayload struct {
	ID string `json:"id"`
	// TenantID is read-only on the wire. The admin handler always
	// resolves it from the calling principal (or, for a
	// platform-admin ?tenant_id= override, from the query string).
	TenantID     string                `json:"tenantId,omitempty"`
	DisplayName  string                `json:"displayName,omitempty"`
	MCPServerURL string                `json:"mcpServerUrl,omitempty"`
	Transport    string                `json:"transport,omitempty"`
	Auth         *ConnectorAuthPayload `json:"auth,omitempty"`
	Visibility   string                `json:"visibility,omitempty"`
	Labels       map[string]string     `json:"labels,omitempty"`
	CreatedAt    string                `json:"createdAt,omitempty"`
	UpdatedAt    string                `json:"updatedAt,omitempty"`
	DeletedAt    string                `json:"deletedAt,omitempty"`
}

// ConnectorAuthPayload is the OAuth2 auth block. The client secret
// is always a reference (`namespace/name`); the raw secret is never
// accepted or returned on the wire per §9.3.
type ConnectorAuthPayload struct {
	Type                  string   `json:"type,omitempty"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint,omitempty"`
	TokenEndpoint         string   `json:"tokenEndpoint,omitempty"`
	ClientID              string   `json:"clientId,omitempty"`
	ClientSecretRef       string   `json:"clientSecretRef,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
}

func fromConnector(c connectorstore.Connector) ConnectorPayload {
	out := ConnectorPayload{
		ID:           c.ID,
		TenantID:     c.TenantID,
		DisplayName:  c.DisplayName,
		MCPServerURL: c.MCPServerURL,
		Transport:    c.Transport,
		Visibility:   c.Visibility,
		Labels:       c.Labels,
		CreatedAt:    rfc3339Nano(c.CreatedAt),
		UpdatedAt:    rfc3339Nano(c.UpdatedAt),
		DeletedAt:    rfc3339Nano(c.DeletedAt),
	}
	if c.Auth != nil {
		out.Auth = &ConnectorAuthPayload{
			Type:                  c.Auth.Type,
			AuthorizationEndpoint: c.Auth.AuthorizationEndpoint,
			TokenEndpoint:         c.Auth.TokenEndpoint,
			ClientID:              c.Auth.ClientID,
			ClientSecretRef:       c.Auth.ClientSecretRef,
			Scopes:                c.Auth.Scopes,
		}
	}
	return out
}

func toConnectorAuth(p *ConnectorAuthPayload) *connectorstore.ConnectorAuth {
	if p == nil {
		return nil
	}
	return &connectorstore.ConnectorAuth{
		Type:                  p.Type,
		AuthorizationEndpoint: p.AuthorizationEndpoint,
		TokenEndpoint:         p.TokenEndpoint,
		ClientID:              p.ClientID,
		ClientSecretRef:       p.ClientSecretRef,
		Scopes:                p.Scopes,
	}
}

// WithConnectors wires the §15.1 connector CRUD handlers onto the
// Router.
func (r *Router) WithConnectors(s connectorstore.Store) *Router {
	r.connectors = s
	return r
}

func (r *Router) handleCreateConnector(w http.ResponseWriter, req *http.Request) {
	var body ConnectorPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID, err := resolveTargetTenant(principal, req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	c := connectorstore.Connector{
		TenantID:     tenantID,
		ID:           body.ID,
		DisplayName:  body.DisplayName,
		MCPServerURL: body.MCPServerURL,
		Transport:    body.Transport,
		Auth:         toConnectorAuth(body.Auth),
		Visibility:   body.Visibility,
		Labels:       body.Labels,
		CreatedAt:    r.clock(),
	}
	if c.Transport == "" {
		c.Transport = "streamable_http"
	}
	if c.Visibility == "" {
		c.Visibility = "tenant"
	}
	c.UpdatedAt = c.CreatedAt
	if err := r.connectors.Create(req.Context(), c); err != nil {
		if errors.Is(err, connectorstore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"connector with this id already exists", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.connectors.Get(req.Context(), tenantID, body.ID)
	// spec: §16.7 / §9.3 line 116-164 — emit through the typed catalog
	// so audit-sink validators recognize the event type. F-9.3.9.
	r.emit(req.Context(), principal, audit.EventAdminConnectorCreated.String(), body.ID, map[string]any{
		"tenant_id":  tenantID,
		"transport":  stored.Transport,
		"visibility": stored.Visibility,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromConnector(stored))
}

func (r *Router) handleListConnectors(w http.ResponseWriter, req *http.Request) {
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID := listTenantScope(principal, req)
	rows, err := r.connectors.List(req.Context(), tenantID, connectorstore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]ConnectorPayload, 0, len(rows))
	for _, c := range rows {
		out = append(out, fromConnector(c))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"connectors": out})
}

func (r *Router) handleGetConnector(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("name")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID := listTenantScope(principal, req)
	row, err := r.connectors.Get(req.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, connectorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromConnector(row))
}

func (r *Router) handleUpdateConnector(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("name")
	var body ConnectorPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID, err := resolveTargetTenant(principal, req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	updated, err := r.connectors.Update(req.Context(), tenantID, id, func(c *connectorstore.Connector) error {
		if body.DisplayName != "" {
			c.DisplayName = body.DisplayName
		}
		if body.MCPServerURL != "" {
			c.MCPServerURL = body.MCPServerURL
		}
		if body.Transport != "" {
			c.Transport = body.Transport
		}
		if body.Visibility != "" {
			c.Visibility = body.Visibility
		}
		if body.Labels != nil {
			c.Labels = body.Labels
		}
		if body.Auth != nil {
			c.Auth = toConnectorAuth(body.Auth)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, connectorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, audit.EventAdminConnectorUpdated.String(), id, map[string]any{
		"tenant_id": tenantID,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromConnector(updated))
}

func (r *Router) handleDeleteConnector(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("name")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID, err := resolveTargetTenant(principal, req, "")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if err := r.connectors.SoftDelete(req.Context(), tenantID, id, r.clock()); err != nil {
		if errors.Is(err, connectorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, audit.EventAdminConnectorSoftDeleted.String(), id, map[string]any{
		"tenant_id": tenantID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// resolveTargetTenant determines which tenant a write should target.
// For tenant-admin callers the answer is always their own tenant id
// regardless of body or query — they cannot write outside their tenant.
// For platform-admin callers the optional body.tenantId / ?tenant_id=
// query parameter selects the target; an empty value defaults to the
// caller's tenant (the platform tenant for the platform-admin role).
//
// spec: §4.2 line 173 — connectors are tenant-scoped; the admin API
// enforces that a tenant-admin cannot write across tenants.
func resolveTargetTenant(p authmw.Principal, req *http.Request, bodyTenantID string) (string, error) {
	requested := bodyTenantID
	if requested == "" {
		requested = req.URL.Query().Get("tenant_id")
	}
	if !p.HasRole(auth.RolePlatformAdmin) {
		if requested != "" && requested != p.TenantID {
			return "", errors.New("only platform-admin may write across tenants")
		}
		return p.TenantID, nil
	}
	if requested == "" {
		return p.TenantID, nil
	}
	return requested, nil
}

// listTenantScope determines the tenant scope for a list/read. A
// tenant-admin reads only their own tenant. A platform-admin reads
// across tenants by default (AllTenantsSentinel) and may narrow to a
// specific tenant via ?tenant_id=.
//
// spec: §4.2 line 173 / §10.6: tenant-admins see only their own
// tenant; platform-admins see all tenants with optional filter.
func listTenantScope(p authmw.Principal, req *http.Request) string {
	if !p.HasRole(auth.RolePlatformAdmin) {
		return p.TenantID
	}
	if t := req.URL.Query().Get("tenant_id"); t != "" {
		return t
	}
	return connectorstore.AllTenantsSentinel
}
