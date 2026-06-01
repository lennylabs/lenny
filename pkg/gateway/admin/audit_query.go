// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	auditcat "github.com/lennylabs/lenny/pkg/observability/audit"
)

// scopeRawCanonical and scopeRetranslate are the §25.9 scope strings
// gating the audit-recovery surface, in the §15.2 canonical
// `tools:<domain>:<action>` taxonomy form (the spec's shorthand
// `audit:raw-canonical:read` / `audit:retranslate` map onto the
// `audit` domain, matching the `audit:republish` aka
// `tools:audit:republish` convention §25.9 names). HasScope returns
// true when the JWT carries no scope claim, so the surrounding
// requireAuditReader role gate still applies in that case.
//
// spec: §25.9 line 3653, line 3662; §15.2 scope taxonomy.
const (
	scopeRawCanonical = "tools:audit:raw_canonical_read"
	scopeRetranslate  = "tools:audit:retranslate"
)

// auditTranslationLog is the optional §25.9 OCSF-translation-state
// surface. The Postgres-backed auditstore implements it; the in-memory
// audit.ChainSet does not, because it translates inline at query time
// and never dead-letters a row. When the wired backend does not
// implement it, every row is treated as `succeeded` (translated
// inline) and the retranslate endpoint reports rows ineligible.
//
// spec: §11.7 lines 422-426 (the ocsf_translation_state machine).
type auditTranslationLog interface {
	TranslationState(ctx context.Context, tenantID string, seq uint64) (audit.OCSFTranslationState, int, error)
	SetTranslationState(ctx context.Context, tenantID string, seq uint64, state audit.OCSFTranslationState, retryCount int) error
}

// rowTranslationState resolves a row's §11.7 ocsf_translation_state.
// Backends without translation-state tracking translate inline at
// query time, so their rows are reported `succeeded`.
func (r *Router) rowTranslationState(ctx context.Context, tenant string, seq uint64) (audit.OCSFTranslationState, int, error) {
	ts, ok := r.auditLog.(auditTranslationLog)
	if !ok {
		return audit.OCSFSucceeded, 0, nil
	}
	return ts.TranslationState(ctx, tenant, seq)
}

// rowToCanonical projects a canonical audit row onto the §25.9
// raw-canonical wire tuple — the exact field set Postgres hashed over,
// for chain auditors recomputing the hash independently.
func rowToCanonical(row audit.Row) AuditEventPayload {
	return AuditEventPayload{
		Seq:       row.Seq,
		TenantID:  row.TenantID,
		EventType: row.EventType,
		Payload:   row.Payload,
		Timestamp: row.Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
		PrevHash:  row.PrevHash,
		Hash:      row.Hash,
		Redacted:  row.Redacted,
	}
}

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

	// spec: §25.9 line 3659 — ?ocsf_translation_state filters rows by
	// the §11.7 translator state (pending | retry_pending | succeeded |
	// dead_lettered). Combining filters is AND. An unparseable value is
	// rejected rather than ignored.
	var stateFilter audit.OCSFTranslationState
	if v := req.URL.Query().Get("ocsf_translation_state"); v != "" {
		stateFilter = audit.OCSFTranslationState(v)
		if !stateFilter.IsValid() {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
				"ocsf_translation_state must be one of pending, retry_pending, succeeded, dead_lettered", nil)
			return
		}
	}

	items := make([]json.RawMessage, 0, limit)
	for _, row := range rows {
		if row.Seq <= afterSeq {
			continue
		}
		if stateFilter != "" {
			st, _, serr := r.rowTranslationState(req.Context(), tenant, row.Seq)
			if serr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"audit translation-state lookup failed at seq "+strconv.FormatUint(row.Seq, 10)+": "+serr.Error(), nil)
				return
			}
			if st != stateFilter {
				continue
			}
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
	// spec: §25.9 line 3653 — ?format=raw-canonical returns the
	// Lenny-internal canonical tuple (pre-OCSF) for chain auditors who
	// recompute the hash against the exact bytes Postgres hashed over.
	// Scope-restricted to audit:raw-canonical:read.
	rawCanonical := req.URL.Query().Get("format") == "raw-canonical"
	if rawCanonical {
		if p, _ := authmw.FromContext(req.Context()); !p.HasScope(scopeRawCanonical) {
			writeError(w, http.StatusForbidden, "FORBIDDEN",
				"raw-canonical format requires the audit:raw-canonical:read scope", nil)
			return
		}
	}
	rows, err := r.auditLog.Rows(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit query failed: "+err.Error(), nil)
		return
	}
	for _, row := range rows {
		if row.Seq == seq {
			if rawCanonical {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rowToCanonical(row))
				return
			}
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

// retranslateRequest is the §25.9 POST .../retranslate body. The
// translatorVersion is optional and defaults to the active translator.
type retranslateRequest struct {
	TranslatorVersion string `json:"translatorVersion"`
}

// retranslateResponse echoes the updated row state and the receiving
// translator version per §25.9.
type retranslateResponse struct {
	Seq                  uint64 `json:"seq"`
	OCSFTranslationState string `json:"ocsfTranslationState"`
	TranslatorVersion    string `json:"translatorVersion"`
}

// handleRetranslateAuditEvent implements
// POST /v1/admin/audit-events/{seq}/retranslate. It re-queues a single
// audit row for OCSF translation after a translator-version bump or a
// schema-gap fix. Only rows in retry_pending or dead_lettered are
// eligible; other rows return 409 ocsf_translation_not_retryable. A
// redacted dead-letter row returns 410 DEADLETTER_REDACTED because its
// canonical payload was rewritten by the §12.8 GDPR erasure path and
// cannot be re-translated. On success the row transitions back to
// pending for the next translator sweep.
//
// spec: §25.9 line 3662; §11.7 lines 418, 424 (DEADLETTER_REDACTED on
// a redacted dead-letter row).
func (r *Router) handleRetranslateAuditEvent(w http.ResponseWriter, req *http.Request) {
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	if p, _ := authmw.FromContext(req.Context()); !p.HasScope(scopeRetranslate) {
		writeError(w, http.StatusForbidden, "FORBIDDEN",
			"retranslate requires the audit:retranslate scope", nil)
		return
	}
	seq, err := strconv.ParseUint(req.PathValue("seq"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "seq must be a positive integer", nil)
		return
	}
	var body retranslateRequest
	if req.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(req.Body, 1<<16))
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON body", nil)
				return
			}
		}
	}

	ts, ok := r.auditLog.(auditTranslationLog)
	if !ok {
		// Backends without translation-state tracking translate inline at
		// query time, so no row is ever retry_pending or dead_lettered.
		writeError(w, http.StatusConflict, "ocsf_translation_not_retryable",
			"audit backend translates inline; no row is eligible for retranslation", nil)
		return
	}

	// Locate the row to honor the §11.7 DEADLETTER_REDACTED rule before
	// touching translator state.
	rows, err := r.auditLog.Rows(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit query failed: "+err.Error(), nil)
		return
	}
	var found *audit.Row
	for i := range rows {
		if rows[i].Seq == seq {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "audit event not found", nil)
		return
	}

	state, _, err := ts.TranslationState(req.Context(), tenant, seq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit translation-state lookup failed: "+err.Error(), nil)
		return
	}

	// spec: §11.7 line 424 — a retranslate against a redacted
	// dead-letter row is rejected: the canonical payload was rewritten
	// by the §12.8 erasure step and the original bytes are gone.
	if found.Redacted && state == audit.OCSFDeadLettered {
		writeError(w, http.StatusGone, "DEADLETTER_REDACTED",
			"the dead-lettered row was GDPR-redacted and cannot be re-translated", nil)
		return
	}

	if state != audit.OCSFRetryPending && state != audit.OCSFDeadLettered {
		writeError(w, http.StatusConflict, "ocsf_translation_not_retryable",
			"only retry_pending or dead_lettered rows are eligible for retranslation; row is "+string(state), nil)
		return
	}

	// Reset to pending and clear the retry counter so the next
	// translator sweep re-runs translation against the active version.
	if err := ts.SetTranslationState(req.Context(), tenant, seq, audit.OCSFPending, 0); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit translation-state update failed: "+err.Error(), nil)
		return
	}

	version := body.TranslatorVersion
	if version == "" {
		version = ocsf.TranslatorVersion
	}
	if p, pok := authmw.FromContext(req.Context()); pok {
		r.emit(req.Context(), p, auditcat.EventAuditOcsfRetranslateRequested.String(),
			"audit-event/"+strconv.FormatUint(seq, 10), map[string]any{
				"tenantId":          tenant,
				"seq":               seq,
				"priorState":        string(state),
				"translatorVersion": version,
			})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(retranslateResponse{
		Seq:                  seq,
		OCSFTranslationState: string(audit.OCSFPending),
		TranslatorVersion:    version,
	})
}
