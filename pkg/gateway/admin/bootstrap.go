// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// BootstrapRequest is the §24.1 bootstrap seed payload accepted by
// POST /v1/admin/bootstrap. The handler applies upsert semantics
// per §24.1: each entry either creates a new row or merges into an
// existing one. Operations are best-effort — the handler returns an
// aggregate result with per-section counts and per-entry errors.
type BootstrapRequest struct {
	Tenants  []TenantPayload  `json:"tenants,omitempty"`
	Runtimes []RuntimePayload `json:"runtimes,omitempty"`
	Users    []UserPayload    `json:"users,omitempty"`
}

// BootstrapResponse is the response envelope. CreatedCount tracks
// rows the handler inserted; UpdatedCount tracks rows merged into;
// Errors carries per-entry failures (the handler does NOT stop on
// the first error per §24.1 "all-or-nothing is not required").
type BootstrapResponse struct {
	Tenants  BootstrapSection `json:"tenants,omitempty"`
	Runtimes BootstrapSection `json:"runtimes,omitempty"`
	Users    BootstrapSection `json:"users,omitempty"`
}

// BootstrapSection is the per-resource result.
type BootstrapSection struct {
	CreatedCount int              `json:"createdCount"`
	UpdatedCount int              `json:"updatedCount"`
	Errors       []BootstrapError `json:"errors,omitempty"`
	Applied      []string         `json:"applied,omitempty"`
}

// BootstrapError captures a single per-entry failure.
type BootstrapError struct {
	Index   int    `json:"index"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
}

// handleBootstrap implements POST /v1/admin/bootstrap.
func (r *Router) handleBootstrap(w http.ResponseWriter, req *http.Request) {
	var body BootstrapRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}

	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}

	out := BootstrapResponse{}
	if r.tenants != nil {
		out.Tenants = r.upsertTenants(req, body.Tenants)
	}
	if r.runtimes != nil {
		out.Runtimes = r.upsertRuntimes(req, body.Runtimes)
	}
	if r.users != nil {
		out.Users = r.upsertUsers(req, body.Users)
	}

	r.emit(req.Context(), principal, "admin.bootstrap.applied", "platform", map[string]any{
		"tenants":  map[string]any{"created": out.Tenants.CreatedCount, "updated": out.Tenants.UpdatedCount, "errors": len(out.Tenants.Errors)},
		"runtimes": map[string]any{"created": out.Runtimes.CreatedCount, "updated": out.Runtimes.UpdatedCount, "errors": len(out.Runtimes.Errors)},
		"users":    map[string]any{"created": out.Users.CreatedCount, "updated": out.Users.UpdatedCount, "errors": len(out.Users.Errors)},
	})

	status := http.StatusOK
	if anyFailures(out) {
		// 207 Multi-Status would be ideal; 200 is acceptable because
		// the body carries per-entry results. Use 207 to signal partial
		// failure so curl pipelines fail fast.
		status = http.StatusMultiStatus
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}

func anyFailures(out BootstrapResponse) bool {
	return len(out.Tenants.Errors) > 0 ||
		len(out.Runtimes.Errors) > 0 ||
		len(out.Users.Errors) > 0
}

func (r *Router) upsertTenants(req *http.Request, in []TenantPayload) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if p.ID == "" {
			out.Errors = append(out.Errors, BootstrapError{Index: i, Message: "id is required"})
			continue
		}
		if err := auth.ValidateTenantID(p.ID); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.ID, Message: err.Error()})
			continue
		}
		existing, err := r.tenants.Get(req.Context(), p.ID)
		if errors.Is(err, tenantstore.ErrNotFound) {
			row := tenantstore.Tenant{
				ID:                  p.ID,
				DisplayName:         p.DisplayName,
				ComplianceProfile:   p.ComplianceProfile,
				DataResidencyRegion: p.DataResidencyRegion,
				WorkspaceTier:       p.WorkspaceTier,
				CreatedAt:           r.clock(),
			}
			row.UpdatedAt = row.CreatedAt
			if err := r.tenants.Create(req.Context(), row); err != nil {
				out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.ID, Message: err.Error()})
				continue
			}
			out.CreatedCount++
			out.Applied = append(out.Applied, p.ID)
			continue
		}
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.ID, Message: err.Error()})
			continue
		}
		_, err = r.tenants.Update(req.Context(), existing.ID, func(t *tenantstore.Tenant) error {
			if p.DisplayName != "" {
				t.DisplayName = p.DisplayName
			}
			if p.ComplianceProfile != "" {
				t.ComplianceProfile = p.ComplianceProfile
			}
			if p.DataResidencyRegion != "" {
				t.DataResidencyRegion = p.DataResidencyRegion
			}
			if p.WorkspaceTier != "" {
				t.WorkspaceTier = p.WorkspaceTier
			}
			return nil
		})
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.ID, Message: err.Error()})
			continue
		}
		out.UpdatedCount++
		out.Applied = append(out.Applied, p.ID)
	}
	return out
}

func (r *Router) upsertRuntimes(req *http.Request, in []RuntimePayload) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if err := runtimestore.ValidateName(p.Name); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := p.validatePayloadEnums(); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		if err := validateCapabilities(p.Capabilities); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		existing, err := r.runtimes.Get(req.Context(), p.Name)
		if errors.Is(err, runtimestore.ErrNotFound) {
			row := runtimestore.Runtime{
				Name:             p.Name,
				Type:             runtimestore.RuntimeType(p.Type),
				Image:            p.Image,
				ExecutionMode:    runtimestore.ExecutionMode(p.ExecutionMode),
				IsolationProfile: isolation.Profile(p.IsolationProfile),
				IntegrationLevel: runtimestore.IntegrationLevel(p.IntegrationLevel),
				Description:      p.Description,
				Capabilities:     p.Capabilities,
				CreatedAt:        r.clock(),
			}
			runtimestore.ApplyDefaults(&row)
			row.UpdatedAt = row.CreatedAt
			if err := r.runtimes.Create(req.Context(), row); err != nil {
				out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
				continue
			}
			out.CreatedCount++
			out.Applied = append(out.Applied, p.Name)
			continue
		}
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		_, err = r.runtimes.Update(req.Context(), existing.Name, func(rt *runtimestore.Runtime) error {
			if p.Image != "" {
				rt.Image = p.Image
			}
			if p.ExecutionMode != "" {
				rt.ExecutionMode = runtimestore.ExecutionMode(p.ExecutionMode)
			}
			if p.IsolationProfile != "" {
				rt.IsolationProfile = isolation.Profile(p.IsolationProfile)
			}
			if p.IntegrationLevel != "" {
				rt.IntegrationLevel = runtimestore.IntegrationLevel(p.IntegrationLevel)
			}
			if p.Description != "" {
				rt.Description = p.Description
			}
			if p.Capabilities != nil {
				rt.Capabilities = p.Capabilities
			}
			return nil
		})
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Name, Message: err.Error()})
			continue
		}
		out.UpdatedCount++
		out.Applied = append(out.Applied, p.Name)
	}
	return out
}

func (r *Router) upsertUsers(req *http.Request, in []UserPayload) BootstrapSection {
	out := BootstrapSection{}
	for i, p := range in {
		if err := userstore.ValidateSubject(p.Subject); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
			continue
		}
		if err := auth.ValidateTenantID(p.TenantID); err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
			continue
		}
		for _, role := range p.Roles {
			if !role.IsValid() {
				out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject,
					Message: "role " + string(role) + " is not a recognised §10.2 RBAC role"})
				continue
			}
		}
		existing, err := r.users.Get(req.Context(), p.TenantID, p.Subject)
		if errors.Is(err, userstore.ErrNotFound) {
			row := userstore.User{
				Subject:     p.Subject,
				TenantID:    p.TenantID,
				Email:       p.Email,
				DisplayName: p.DisplayName,
				Roles:       p.Roles,
				Disabled:    p.Disabled,
				CreatedAt:   r.clock(),
			}
			row.UpdatedAt = row.CreatedAt
			if err := r.users.Create(req.Context(), row); err != nil {
				out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
				continue
			}
			out.CreatedCount++
			out.Applied = append(out.Applied, p.Subject)
			continue
		}
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
			continue
		}
		_, err = r.users.Update(req.Context(), existing.TenantID, existing.Subject, func(u *userstore.User) error {
			if p.Email != "" {
				u.Email = p.Email
			}
			if p.DisplayName != "" {
				u.DisplayName = p.DisplayName
			}
			if len(p.Roles) > 0 {
				u.Roles = p.Roles
			}
			u.Disabled = p.Disabled
			return nil
		})
		if err != nil {
			out.Errors = append(out.Errors, BootstrapError{Index: i, ID: p.Subject, Message: err.Error()})
			continue
		}
		out.UpdatedCount++
		out.Applied = append(out.Applied, p.Subject)
	}
	return out
}
