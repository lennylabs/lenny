// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// ConnectorPayload is the §9.3 / §15.1 admin-connector wire shape.
type ConnectorPayload struct {
	ID           string            `json:"id"`
	DisplayName  string            `json:"displayName,omitempty"`
	MCPServerURL string            `json:"mcpServerUrl,omitempty"`
	Transport    string            `json:"transport,omitempty"`
	Auth         *ConnectorAuthPayload `json:"auth,omitempty"`
	Visibility   string            `json:"visibility,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	CreatedAt    string            `json:"createdAt,omitempty"`
	UpdatedAt    string            `json:"updatedAt,omitempty"`
	DeletedAt    string            `json:"deletedAt,omitempty"`
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
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	c := connectorstore.Connector{
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
	stored, _ := r.connectors.Get(req.Context(), body.ID)
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.connector.created", body.ID, map[string]any{
		"transport":  stored.Transport,
		"visibility": stored.Visibility,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromConnector(stored))
}

func (r *Router) handleListConnectors(w http.ResponseWriter, req *http.Request) {
	rows, err := r.connectors.List(req.Context(), connectorstore.ListFilter{
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
	id := req.PathValue("id")
	row, err := r.connectors.Get(req.Context(), id)
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
	id := req.PathValue("id")
	var body ConnectorPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	updated, err := r.connectors.Update(req.Context(), id, func(c *connectorstore.Connector) error {
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
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.connector.updated", id, nil)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromConnector(updated))
}

func (r *Router) handleDeleteConnector(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	if err := r.connectors.SoftDelete(req.Context(), id, r.clock()); err != nil {
		if errors.Is(err, connectorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
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
	r.emit(req.Context(), principal, "admin.connector.soft_deleted", id, nil)
	w.WriteHeader(http.StatusNoContent)
}
