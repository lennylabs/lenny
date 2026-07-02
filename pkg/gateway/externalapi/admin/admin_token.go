// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
)

// AdminTokenProvisioner provisions and rotates the §17.6 initial admin
// credential. *admintoken.Provisioner satisfies it; a nil provisioner
// leaves the admin-token bootstrap step and the rotate-token route
// inactive.
//
// spec: §17.6 lines 455-474 — F-17.6.3, F-24.1.7.
type AdminTokenProvisioner interface {
	Provision(ctx context.Context) (admintoken.Result, error)
	Rotate(ctx context.Context) (admintoken.Result, error)
	Username() string
	SecretRef() (namespace, name string)
}

// WithAdminTokenProvisioner wires the §17.6 initial-admin-credential
// provisioner onto the Router. When set, a bootstrap run provisions the
// Secret (idempotently) and reports the outcome in the response, and the
// rotate-token route is registered.
func (r *Router) WithAdminTokenProvisioner(p AdminTokenProvisioner) *Router {
	r.adminToken = p
	return r
}

// AdminTokenSection is the §17.6 admin-credential outcome the bootstrap
// response carries so the §24.1 CLI can print the right first-use prompt.
type AdminTokenSection struct {
	// SecretCreated is true when this bootstrap run wrote the Secret (a
	// first run). The §17.6 line 473 first-use prompt fires only then;
	// re-runs print the "already exists" message. F-24.1.7.
	SecretCreated bool `json:"secretCreated"`
	// SecretNamespace / SecretName / Username echo where the credential
	// lives so the CLI can render the exact `kubectl get secret` command.
	SecretNamespace string `json:"secretNamespace,omitempty"`
	SecretName      string `json:"secretName,omitempty"`
	Username        string `json:"username,omitempty"`
}

// provisionAdminToken runs the §17.6 admin-credential provisioning step
// at the end of a bootstrap run. It is a no-op on a dry-run (a dry-run
// must not mint a token or write a Secret) and when no provisioner is
// wired. A provisioning error is surfaced on the section's message but
// does not fail the whole bootstrap: the seeded resources stand.
func (r *Router) provisionAdminToken(ctx context.Context, dryRun bool) (AdminTokenSection, bool) {
	if r.adminToken == nil || dryRun {
		return AdminTokenSection{}, false
	}
	ns, name := r.adminToken.SecretRef()
	section := AdminTokenSection{
		SecretNamespace: ns,
		SecretName:      name,
		Username:        r.adminToken.Username(),
	}
	res, err := r.adminToken.Provision(ctx)
	if err != nil {
		// Surface the failure but keep the bootstrap result intact; the
		// operator can re-run once the cause (e.g. missing Secret RBAC) is
		// fixed. The §17.6 fail-safe credential is the only thing missing.
		return section, false
	}
	section.SecretCreated = res.Created
	return section, true
}

// handleRotateToken implements POST /v1/admin/users/{user}/rotate-token —
// the §17.6 admin-token rotation. It mints a fresh token,
// patches the Secret, and revokes the superseded token immediately. Only
// the managed admin username is rotatable through this route; any other
// user returns 404 so the route does not leak as a generic token mint.
//
// spec: §17.6 — F-17.6.3.
func (r *Router) handleRotateToken(w http.ResponseWriter, req *http.Request) {
	if r.adminToken == nil {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"admin-token rotation is not configured on this gateway", nil)
		return
	}
	user := req.PathValue("user")
	if user != r.adminToken.Username() {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"only the managed initial admin user can be rotated through this route", nil)
		return
	}
	if _, err := r.adminToken.Rotate(req.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin-token rotation failed: "+err.Error(), nil)
		return
	}
	ns, name := r.adminToken.SecretRef()
	section := AdminTokenSection{
		SecretCreated:   true,
		SecretNamespace: ns,
		SecretName:      name,
		Username:        user,
	}
	writeJSON(w, http.StatusOK, section)
}
