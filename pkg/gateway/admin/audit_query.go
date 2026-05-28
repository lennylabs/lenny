// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// AuditEventPayload is the §25.9 audit-event canonical Postgres tuple.
// The default response wire form is the OCSF translation per §4.4
// line 232 / §11.7; the canonical tuple is retained for the future
// `?format=raw-canonical` callers (chain auditors who recompute the
// hash against the exact bytes Postgres hashed over).
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

// AuditEventEnvelope is the §4.4 line 232 / §25.9 OCSF audit-egress
// response envelope. Every paginated `/v1/admin/audit-events` response
// carries the envelope; the `items[]` array holds the OCSF v1.1.0
// records produced by `pkg/audit/ocsf.Translate`. `translatorVersion`
// and `ocsfVersion` let a consumer correlate the wire form with the
// exact translator implementation and OCSF version that produced it.
type AuditEventEnvelope struct {
	TenantID          string            `json:"tenantId"`
	Items             []json.RawMessage `json:"items"`
	OCSFVersion       string            `json:"ocsfVersion"`
	TranslatorVersion string            `json:"translatorVersion"`
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
// Router over an in-memory ChainSet — the same one the ChainAuditSink
// writes to.
func (r *Router) WithAuditChains(chains *audit.ChainSet) *Router {
	r.auditLog = chainSetAuditLog{chains: chains, clock: r.clock}
	return r
}

// WithAuditLog wires the §25.9 Audit Log Query API onto the Router
// over any AuditLog — used for the Postgres-backed audit chain so the
// query and verify endpoints read the durable trail.
func (r *Router) WithAuditLog(auditLog AuditLog) *Router {
	r.auditLog = auditLog
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
//
// The default response wire form is the §4.4 line 232 / §11.7 / §25.9
// OCSF v1.1.0 translation: every row is run through
// `pkg/audit/ocsf.Translate` and the resulting records are returned
// inside the envelope (`items[]`, `ocsfVersion`, `translatorVersion`).
// The `unmapped.lenny_chain` extension on each record carries the
// hash-chain fields (prev_hash, integrity) so external auditors can
// verify the chain from the OCSF wire form alone.
//
// spec: §4.4 line 232 — "the audit-egress path includes an OCSF
// translator that maps the canonical Postgres-stored tuple to OCSF
// v1.1.0 JSON for every consumer that sits outside the authoritative
// store: the SIEM forwarder, pgaudit sink consumers, the
// `/v1/admin/audit-events` query API, and the CloudEvents-wrapped
// audit events ... The translator version and OCSF wire version are
// surfaced on every response envelope."
func (r *Router) handleListAuditEvents(w http.ResponseWriter, req *http.Request) {
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	rows, err := r.auditLog.Rows(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit query failed: "+err.Error(), nil)
		return
	}

	afterSeq := uint64(0)
	if v := req.URL.Query().Get("afterSeq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			afterSeq = n
		}
	}
	// spec: §25.9 line 3659 — limit default 100, max 1000. Out-of-range
	// or unparseable values are rejected as 400 INVALID_ARGUMENT rather
	// than silently coerced to the default, matching the rejection style
	// of the surrounding admin handlers.
	limit := 100
	if v := req.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 1000 {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
				"limit must be an integer in 1..1000", nil)
			return
		}
		limit = n
	}

	items := make([]json.RawMessage, 0, limit)
	for _, row := range rows {
		if row.Seq <= afterSeq {
			continue
		}
		ocsfBytes, terr := translateRowToOCSF(row)
		if terr != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"audit ocsf translation failed at seq "+strconv.FormatUint(row.Seq, 10)+": "+terr.Error(), nil)
			return
		}
		items = append(items, ocsfBytes)
		if len(items) >= limit {
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AuditEventEnvelope{
		TenantID:          tenant,
		Items:             items,
		OCSFVersion:       ocsf.Version,
		TranslatorVersion: ocsf.TranslatorVersion,
	})
}

// handleGetAuditEvent implements GET /v1/admin/audit-events/{seq}.
// The response wire form is the §4.4 line 232 OCSF v1.1.0 translation
// of the row; the response is envelope-wrapped so the
// `translatorVersion` and `ocsfVersion` accompany the record.
//
// spec: §4.4 line 232.
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
	rows, err := r.auditLog.Rows(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit query failed: "+err.Error(), nil)
		return
	}
	for _, row := range rows {
		if row.Seq == seq {
			ocsfBytes, terr := translateRowToOCSF(row)
			if terr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"audit ocsf translation failed: "+terr.Error(), nil)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(AuditEventEnvelope{
				TenantID:          tenant,
				Items:             []json.RawMessage{ocsfBytes},
				OCSFVersion:       ocsf.Version,
				TranslatorVersion: ocsf.TranslatorVersion,
			})
			return
		}
	}
	writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "audit event not found", nil)
}

// translateRowToOCSF runs one canonical audit row through the §11.7
// OCSF translator and returns the JSON-encoded record. A translation
// failure is surfaced as an error so the handler returns 500 with the
// failed-seq detail; the spec's dead-letter path (DeadLetterReceipt)
// is reserved for the SIEM-forwarder/EventBus consumers where
// blocking on a single bad row would halt the per-tenant stream.
//
// spec: §4.4 line 232 — OCSF translator at the audit-egress boundary.
func translateRowToOCSF(row audit.Row) (json.RawMessage, error) {
	// The in-memory audit chain does not persist a row UUID (the
	// Postgres-backed auditstore does). Synthesize a stable UID from
	// (tenant_id, seq) so the OCSF metadata.uid is deterministic per
	// row even on the in-memory backend; the Postgres path overrides
	// this when row.ID is non-empty in a later batch.
	uid := row.TenantID + ":" + strconv.FormatUint(row.Seq, 10)
	in := ocsf.Input{
		ID:              uid,
		Sequence:        row.Seq,
		TenantID:        row.TenantID,
		EventType:       row.EventType,
		CreatedAtUnixMs: row.Timestamp.UTC().UnixMilli(),
		Payload:         row.Payload,
		PrevHash:        row.PrevHash,
		ChainIntegrity:  audit.ChainUnchecked,
	}
	rec, err := ocsf.Translate(in)
	if err != nil {
		return nil, err
	}
	return ocsf.MarshalRecord(rec)
}

// handleVerifyAuditChain implements
// GET /v1/admin/audit-events/verify — the §11.7 chain-integrity
// check.
func (r *Router) handleVerifyAuditChain(w http.ResponseWriter, req *http.Request) {
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	res, err := r.auditLog.Verify(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit verify failed: "+err.Error(), nil)
		return
	}
	rows, err := r.auditLog.Rows(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit query failed: "+err.Error(), nil)
		return
	}
	resp := AuditVerifyResponse{
		TenantID:  tenant,
		Integrity: string(res.Integrity),
		BreakSeq:  res.BreakSeq,
		Detail:    res.Detail,
		RowCount:  len(rows),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

