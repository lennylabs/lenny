// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/audit"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
)

// AuditEventPayload is the §25.9 audit-event wire shape.
type AuditEventPayload struct {
	Seq       uint64          `json:"seq"`
	TenantID  string          `json:"tenantId"`
	EventType string          `json:"eventType"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp string          `json:"timestamp"`
	PrevHash  string          `json:"prevHash"`
	Hash      string          `json:"hash"`
	Redacted  bool            `json:"redacted,omitempty"`
}

// AuditVerifyResponse is the §11.7 chain-verification response.
type AuditVerifyResponse struct {
	TenantID  string `json:"tenantId"`
	Integrity string `json:"integrity"`
	BreakSeq  uint64 `json:"breakSeq,omitempty"`
	Detail    string `json:"detail,omitempty"`
	RowCount  int    `json:"rowCount"`
}

// WithAuditChains wires the §25.9 Audit Log Query API onto the
// Router. The supplied ChainSet is the same one the ChainAuditSink
// writes to.
func (r *Router) WithAuditChains(chains *audit.ChainSet) *Router {
	r.auditChains = chains
	return r
}

// auditTenant resolves the tenant whose chain a caller may read.
// platform-admin may read any tenant via the ?tenantId= query
// param; tenant-admin is constrained to its own tenant. Returns the
// resolved tenant and ok=false (with the response already written)
// when the caller is not authorised.
func (r *Router) auditTenant(w http.ResponseWriter, req *http.Request) (string, bool) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "endpoint requires authentication", nil)
		return "", false
	}
	requested := req.URL.Query().Get("tenantId")
	if p.HasRole(pkgauth.RolePlatformAdmin) {
		if requested == "" {
			requested = "platform"
		}
		return requested, true
	}
	if p.HasRole(pkgauth.RoleTenantAdmin) {
		// tenant-admin sees only its own tenant's chain.
		if requested != "" && requested != p.TenantID {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"tenant-admin may only read its own tenant's audit chain", nil)
			return "", false
		}
		return p.TenantID, true
	}
	writeError(w, http.StatusForbidden, "FORBIDDEN",
		"audit query requires platform-admin or tenant-admin", nil)
	return "", false
}

// handleListAuditEvents implements GET /v1/admin/audit-events.
// Supports ?tenantId=, ?limit=, and ?afterSeq= for pagination.
func (r *Router) handleListAuditEvents(w http.ResponseWriter, req *http.Request) {
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	chain := r.auditChains.Chain(tenant)
	rows := []audit.Row{}
	if chain != nil {
		rows = chain.Rows()
	}

	afterSeq := uint64(0)
	if v := req.URL.Query().Get("afterSeq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			afterSeq = n
		}
	}
	limit := 100
	if v := req.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	out := make([]AuditEventPayload, 0, limit)
	for _, row := range rows {
		if row.Seq <= afterSeq {
			continue
		}
		out = append(out, auditRowPayload(row))
		if len(out) >= limit {
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tenantId":    tenant,
		"auditEvents": out,
	})
}

// handleGetAuditEvent implements GET /v1/admin/audit-events/{seq}.
func (r *Router) handleGetAuditEvent(w http.ResponseWriter, req *http.Request) {
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	seq, err := strconv.ParseUint(req.PathValue("seq"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "seq must be a positive integer", nil)
		return
	}
	chain := r.auditChains.Chain(tenant)
	if chain == nil {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "audit event not found", nil)
		return
	}
	for _, row := range chain.Rows() {
		if row.Seq == seq {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(auditRowPayload(row))
			return
		}
	}
	writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "audit event not found", nil)
}

// handleVerifyAuditChain implements
// GET /v1/admin/audit-events/verify — the §11.7 chain-integrity
// check.
func (r *Router) handleVerifyAuditChain(w http.ResponseWriter, req *http.Request) {
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	chain := r.auditChains.Chain(tenant)
	resp := AuditVerifyResponse{TenantID: tenant}
	if chain == nil {
		resp.Integrity = string(audit.ChainVerified)
		resp.RowCount = 0
	} else {
		res := chain.Verify()
		resp.Integrity = string(res.Integrity)
		resp.BreakSeq = res.BreakSeq
		resp.Detail = res.Detail
		resp.RowCount = chain.Len()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func auditRowPayload(row audit.Row) AuditEventPayload {
	return AuditEventPayload{
		Seq:       row.Seq,
		TenantID:  row.TenantID,
		EventType: row.EventType,
		Payload:   row.Payload,
		Timestamp: rfc3339Nano(row.Timestamp),
		PrevHash:  row.PrevHash,
		Hash:      row.Hash,
		Redacted:  row.Redacted,
	}
}
