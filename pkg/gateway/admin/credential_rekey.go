// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
)

// CredentialRekeyer runs the §4.9.1 KMS-key-rotation re-encryption job
// for a tenant. *rekey.Job satisfies it. The admin handler exposes Run
// (re-encrypt every sealed credential row under the current KEK version)
// and Verify (the gate the operator checks before disabling the old key
// in the KMS).
//
// spec: §4.9.1 lines 1714-1730.
type CredentialRekeyer interface {
	Run(ctx context.Context, tenantID string) (rekey.Summary, error)
	Verify(ctx context.Context, tenantID string) (int, error)
}

// WithCredentialRekey wires the §4.9.1 re-encryption job onto the
// Router, registering `POST /v1/admin/tenants/{id}/credential-rekey`
// (run the job) and `GET` (verification status). Without it the routes
// are absent: re-keying applies only to envelope-backed stores, which
// exist when the gateway runs with Postgres. The dev-mode in-memory
// stores hold plaintext and have no KEK to rotate.
func (r *Router) WithCredentialRekey(j CredentialRekeyer) *Router {
	r.credentialRekey = j
	return r
}

// handleCredentialRekey runs the §4.9.1 re-encryption loop for the path
// tenant: it re-wraps every sealed credential row's DEK under the
// tenant's current KEK version and reports per-store counts plus the
// verification result. The operator runs this after rotating the
// tenant's KEK and before disabling the old version; `verified: true`
// means no row remains below the current version.
//
// spec: §4.9.1 lines 1718-1724.
func (r *Router) handleCredentialRekey(w http.ResponseWriter, req *http.Request) {
	tenantID := req.PathValue("id")
	if tenantID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenant id is required", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	sum, err := r.credentialRekey.Run(req.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, "admin.credential.rekeyed", tenantID, map[string]any{
		"tenantId": tenantID,
		"rekeyed":  sum.Rekeyed,
		"stale":    sum.Stale,
		"verified": sum.Verified,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rekeySummaryPayload(sum))
}

// handleCredentialRekeyStatus runs the §4.9.1 verification query for the
// path tenant without re-keying, returning the count of rows still below
// the current KEK version and whether the old version is safe to
// disable. spec: §4.9.1 lines 1723-1724.
func (r *Router) handleCredentialRekeyStatus(w http.ResponseWriter, req *http.Request) {
	tenantID := req.PathValue("id")
	if tenantID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenant id is required", nil)
		return
	}
	stale, err := r.credentialRekey.Verify(req.Context(), tenantID)
	if err != nil && stale == 0 {
		// A non-zero stale count is the expected "incomplete" signal
		// (ErrRekeyIncomplete), not a transport error; only a zero-count
		// error is a real failure.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tenantId": tenantID,
		"stale":    stale,
		"verified": stale == 0,
	})
}

func rekeySummaryPayload(sum rekey.Summary) map[string]any {
	results := make([]map[string]any, 0, len(sum.Results))
	for _, res := range sum.Results {
		results = append(results, map[string]any{
			"store":   res.Store,
			"rekeyed": res.Rekeyed,
			"stale":   res.Stale,
		})
	}
	return map[string]any{
		"tenantId": sum.TenantID,
		"rekeyed":  sum.Rekeyed,
		"stale":    sum.Stale,
		"verified": sum.Verified,
		"results":  results,
	}
}
