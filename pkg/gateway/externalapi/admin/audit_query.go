// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditretention"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
	auditcat "github.com/lennylabs/lenny/pkg/observability/audit"
)

// scopeRawCanonical is the §25.9 scope string gating the
// ?format=raw-canonical branch of GET /v1/admin/audit-events/{seq}, in
// the §15.2 canonical `tools:<domain>:<action>` taxonomy form (the
// spec's shorthand `audit:raw-canonical:read` maps onto the `audit`
// domain). The raw-canonical scope is query-parameter-conditional, so
// it stays a per-handler check rather than a route-level pre-routing
// gate: the served route scope remains the coarse tools:audit:read so a
// tools:audit:read token still reads the ordinary OCSF event. HasScope
// returns true when the JWT carries no scope claim, so the surrounding
// requireAuditReader role gate still applies in that case.
//
// The three destructive audit sub-routes (drop, retranslate, republish)
// carry their fine-grained scopes (tools:audit:partition_drop,
// tools:audit:retranslate, tools:audit:republish) in the served
// openapi.json, so the central §25.1 scope gate in Router.Handler
// enforces them before routing and no per-handler constant is needed.
//
// spec: §25.9 line 3653; §15.2 scope taxonomy.
const scopeRawCanonical = "tools:audit:raw_canonical_read"

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

// auditPublishLog is the optional §25.9 eventbus_publish_state surface.
// The Postgres-backed auditstore implements it (PublishState); the
// in-memory chain does not, because it does not drive the §12.6
// EventBus retranscribe path. When the wired backend does not implement
// it, every row is treated as `published` (no outbound publish queue).
//
// spec: §12.3.7 eventbus_publish_state enum.
type auditPublishLog interface {
	PublishState(ctx context.Context, tenantID string, seq uint64) (eventbus.PublishState, int, error)
}

// rowPublishState resolves a row's §12.3.7 eventbus_publish_state.
// Backends without publish-state tracking report `published`.
func (r *Router) rowPublishState(ctx context.Context, tenant string, seq uint64) (eventbus.PublishState, int, error) {
	ps, ok := r.auditLog.(auditPublishLog)
	if !ok {
		return eventbus.PublishPublished, 0, nil
	}
	return ps.PublishState(ctx, tenant, seq)
}

// rowToCanonical projects a canonical audit row onto the §25.9
// raw-canonical wire tuple — the exact field set Postgres hashed over,
// for chain auditors recomputing the hash independently. integrity is
// the §25.9 per-row chainIntegrity verdict.
func rowToCanonical(row audit.Row, integrity audit.ChainIntegrity) AuditEventPayload {
	return AuditEventPayload{
		Seq:            row.Seq,
		TenantID:       row.TenantID,
		EventType:      row.EventType,
		Payload:        row.Payload,
		Timestamp:      row.Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
		PrevHash:       row.PrevHash,
		Hash:           row.Hash,
		Redacted:       row.Redacted,
		ChainIntegrity: string(integrity),
	}
}

// AuditEventPayload is the §25.9 audit-event canonical Postgres tuple.
// The default response wire form is the OCSF translation per §4.4
// line 232 / §11.7; the canonical tuple is retained for the future
// `?format=raw-canonical` callers (chain auditors who recompute the
// hash against the exact bytes Postgres hashed over).
type AuditEventPayload struct {
	Seq            uint64          `json:"seq"`
	TenantID       string          `json:"tenantId"`
	EventType      string          `json:"eventType"`
	Payload        json.RawMessage `json:"payload"`
	Timestamp      string          `json:"timestamp"`
	PrevHash       string          `json:"prevHash"`
	Hash           string          `json:"hash"`
	Redacted       bool            `json:"redacted,omitempty"`
	ChainIntegrity string          `json:"chainIntegrity,omitempty"`
}

// ChainIntegrityReport is the §25.9 line 3653 top-level envelope field
// tallying the per-row chainIntegrity verdicts of the returned records.
type ChainIntegrityReport struct {
	Verified            int `json:"verified"`
	Broken              int `json:"broken"`
	GapSuspected        int `json:"gap_suspected"`
	RechainedPostOutage int `json:"rechained_post_outage"`
	RedactedGDPR        int `json:"redacted_gdpr"`
	Unchecked           int `json:"unchecked,omitempty"`
}

// AuditGapWindow is one §25.9 line 3679 suspected temporal-gap window in
// the `auditMetadata` envelope object. A window is emitted between two
// consecutive rows whose sequence numbers are non-contiguous.
type AuditGapWindow struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Reason string `json:"reason"`
}

// AuditMetadata is the §25.9 line 3679 envelope object listing suspected
// gap windows in the chain.
type AuditMetadata struct {
	SuspectedGaps []AuditGapWindow `json:"suspectedGaps,omitempty"`
}

// AuditEventEnvelope is the §4.4 line 232 / §25.9 OCSF audit-egress
// response envelope. Every paginated `/v1/admin/audit-events` response
// carries the envelope; the `items[]` array holds the OCSF v1.1.0
// records produced by `pkg/audit/ocsf.Translate`. `translatorVersion`
// and `ocsfVersion` let a consumer correlate the wire form with the
// exact translator implementation and OCSF version that produced it.
// `chainIntegrityReport` and `auditMetadata` carry the §25.9 chain
// verdict tally and the suspected-gap windows. `nextCursor` is the
// opaque §25.9 pagination token for the following page (empty on the
// last page).
type AuditEventEnvelope struct {
	TenantID             string                `json:"tenantId"`
	Items                []json.RawMessage     `json:"items"`
	OCSFVersion          string                `json:"ocsfVersion"`
	TranslatorVersion    string                `json:"translatorVersion"`
	ChainIntegrityReport *ChainIntegrityReport `json:"chainIntegrityReport,omitempty"`
	AuditMetadata        *AuditMetadata        `json:"auditMetadata,omitempty"`
	NextCursor           string                `json:"nextCursor,omitempty"`
	// Degradation carries the §25.9 207 AUDIT_PARTIAL_RESULTS envelope on
	// a cross-tenant query that reached only some audit shards. Absent on
	// a complete (200) response.
	Degradation *AuditDegradation `json:"degradation,omitempty"`
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

// AuditPartitionDropper force-drops audit rows the SIEM delivery guard
// is holding past their §16.4 retention TTL, after the operator
// acknowledges the resulting data loss. *auditretention.Pruner
// satisfies it.
//
// spec: §16.4 line 378 (force-drop override); §25.9 (the backing
// POST /v1/admin/audit-partitions/{partition}/drop endpoint).
type AuditPartitionDropper interface {
	ForceDrop(ctx context.Context, tenantID, requesterSub string, now time.Time) (auditretention.ForceDropResult, error)
}

// WithAuditPruner wires the §16.4 force-drop override onto the Router.
// When set, POST /v1/admin/audit-partitions/{partition}/drop is
// registered; the in-memory gateway leaves it nil and the route is
// absent.
func (r *Router) WithAuditPruner(p AuditPartitionDropper) *Router {
	r.auditPruner = p
	return r
}

// AuditQueryMetrics records the §25.9 audit-query observability series.
// *gatewaymetrics.Metrics satisfies it. A nil recorder disables emission
// (the query still succeeds).
//
// spec: §25.9 metrics table (lenny_audit_query_duration_seconds,
// lenny_audit_chain_verification_broken_total,
// lenny_audit_chain_rechained_post_outage_total,
// lenny_audit_scatter_gather_shards_queried).
type AuditQueryMetrics interface {
	ObserveAuditQueryDuration(endpoint string, shards int, seconds float64)
	IncAuditChainVerificationBroken()
	IncAuditChainRechainedPostOutage()
	ObserveAuditScatterGatherShards(shards int)
}

// WithAuditMetrics wires the §25.9 audit-query metrics recorder onto the
// Router. Without it the query endpoints emit no audit-query series.
func (r *Router) WithAuditMetrics(m AuditQueryMetrics) *Router {
	r.auditMetrics = m
	return r
}

// recordAuditQuery emits the §25.9 audit-query observability series for
// one query: the latency (labeled by endpoint and the single-shard v1
// fan-out width), the shard count, and the broken / post-outage-rechain
// counters when the returned page surfaced such segments. shards is 1 in
// single-shard v1; broken and rechained are the per-page tallies.
func (r *Router) recordAuditQuery(endpoint string, start time.Time, shards, broken, rechained int) {
	if r.auditMetrics == nil {
		return
	}
	r.auditMetrics.ObserveAuditQueryDuration(endpoint, shards, r.clock().Sub(start).Seconds())
	r.auditMetrics.ObserveAuditScatterGatherShards(shards)
	for i := 0; i < broken; i++ {
		r.auditMetrics.IncAuditChainVerificationBroken()
	}
	for i := 0; i < rechained; i++ {
		r.auditMetrics.IncAuditChainRechainedPostOutage()
	}
}

// forceDropRequest is the §25.9 line 3664 force-drop body
// `{"acknowledgeDataLoss": true, "partition": "<partition-name>"}`. The
// data-loss acknowledgement is mandatory (the lenny-ctl backing is
// `audit drop-partition <partition> --force --acknowledge-data-loss`) and
// the partition field is an anti-footgun cross-check that must match the
// path segment.
type forceDropRequest struct {
	AcknowledgeDataLoss bool   `json:"acknowledgeDataLoss"`
	Partition           string `json:"partition"`
}

// handleForceDropAuditPartition implements
// POST /v1/admin/audit-partitions/{partition}/drop. It force-deletes the
// partition's audit rows that have aged past the §16.4 retention window
// regardless of the SIEM delivery guard, after the operator
// acknowledges the data loss, and records the §16.7
// audit.partition_drop_forced event. The {partition} path segment is the
// tenant chain identifier (v1 keys the audit table by tenant; native
// range partitioning is a separate concern).
//
// The §25.9 line 3664 wire contract requires the `?force=true` query
// parameter, the `audit:partition:drop` scope, and a request body whose
// `partition` field matches the path segment (anti-footgun cross-check).
//
// spec: §16.4 line 378 (force-drop override + data-loss
// acknowledgement); §16.7 line 687 (audit.partition_drop_forced); §25.9
// line 3664 (force query param, scope, partition cross-check).
func (r *Router) handleForceDropAuditPartition(w http.ResponseWriter, req *http.Request) {
	if r.auditPruner == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED",
			"audit force-drop requires a durable audit store", nil)
		return
	}
	// §25.9 line 3664 — the destructive drop is gated on the dedicated
	// tools:audit:partition_drop scope. The central §25.1 scope gate in
	// Router.Handler resolves the route's x-lenny-scope (now the
	// fine-grained partition_drop form) and rejects a scope-narrowed
	// token with SCOPE_FORBIDDEN before this handler runs, so no
	// per-handler scope check is needed here. spec: §25.1 line 94.
	partition := strings.TrimSpace(req.PathValue("partition"))
	if partition == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "partition is required", nil)
		return
	}
	// §25.9 line 3664 — the ?force=true query parameter is mandatory so an
	// unqualified POST can never drop a partition.
	if req.URL.Query().Get("force") != "true" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"the ?force=true query parameter is required to drop an audit partition", nil)
		return
	}
	var body forceDropRequest
	if req.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(req.Body, 1<<16))
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON body", nil)
				return
			}
		}
	}
	if !body.AcknowledgeDataLoss {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"acknowledgeDataLoss must be true to force-drop audit rows the SIEM forwarder has not delivered", nil)
		return
	}
	// §25.9 line 3664 — the body partition must match the path so a
	// fat-fingered path or copy-pasted body cannot drop the wrong
	// partition.
	if body.Partition != partition {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"the body partition must match the path partition", map[string]any{
				"pathPartition": partition,
				"bodyPartition": body.Partition,
			})
		return
	}
	p, _ := authmw.FromContext(req.Context())
	res, err := r.auditPruner.ForceDrop(req.Context(), partition, p.Subject, r.clock())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit force-drop failed: "+err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
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

// auditRows loads the tenant's rows, mapping a backend read failure to
// the §25.9 line 3714 `503 AUDIT_STORE_UNAVAILABLE` degradation code
// (the audit read path fails only when the authoritative Postgres store
// is unreachable; there is no read cache). Returns ok=false with the
// response already written on failure.
func (r *Router) auditRows(w http.ResponseWriter, req *http.Request, tenant string) ([]audit.Row, bool) {
	rows, err := r.auditLog.Rows(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "AUDIT_STORE_UNAVAILABLE",
			"audit store unreachable: "+err.Error(), nil)
		return nil, false
	}
	return rows, true
}

// receiptReader is the optional §12.8 RedactionReceipt surface an AuditLog
// backend may expose. When present, the per-row chainIntegrity walk loads
// the receipts so a lawfully redacted row is classified redacted_gdpr
// rather than broken. The Postgres-backed auditstore.Store satisfies it;
// the in-memory chainSetAuditLog does not (its redactions are read out of
// the chain itself).
type receiptReader interface {
	Receipts(ctx context.Context, tenantID string) (map[uint64]audit.RedactionReceipt, error)
}

// auditReceipts loads the tenant's §12.8 RedactionReceipts when the audit
// backend exposes them, so the chainIntegrity walk can distinguish a lawful
// GDPR redaction from a tamper. A backend without receipts (or a load
// error) yields nil, which the verifier treats as "no receipt" — the
// conservative reading that surfaces an unauthenticated discontinuity.
func (r *Router) auditReceipts(ctx context.Context, tenant string) map[uint64]audit.RedactionReceipt {
	rr, ok := r.auditLog.(receiptReader)
	if !ok {
		return nil
	}
	receipts, err := rr.Receipts(ctx, tenant)
	if err != nil {
		return nil
	}
	return receipts
}

// handleListAuditEvents implements GET /v1/admin/audit-events.
//
// The query supports the §25.9 line 3659 filter set (since, until,
// eventType, actorId, resourceType, resourceId, severity, limit, cursor,
// ocsf_translation_state, eventbus_publish_state) and the line 3703
// diagnostics ?operationId=. Combining filters is AND. A query without a
// time bound defaults to the last 24 hours; a span exceeding 90 days
// without a narrowing filter is rejected with AUDIT_QUERY_TOO_BROAD.
//
// The default response wire form is the §4.4 line 232 / §11.7 OCSF
// v1.1.0 translation: every row runs through `pkg/audit/ocsf.Translate`
// and the records are returned in the envelope (`items[]`,
// `ocsfVersion`, `translatorVersion`). The `unmapped.lenny_chain`
// extension on each record carries the per-row chainIntegrity verdict
// and prev_hash. The envelope's `chainIntegrityReport` tallies the
// verdicts and `auditMetadata.suspectedGaps` lists detected temporal
// gaps (§25.9 lines 3653, 3670-3679).
//
// spec: §25.9 lines 3653-3710; §4.4 line 232 (OCSF audit-egress).
func (r *Router) handleListAuditEvents(w http.ResponseWriter, req *http.Request) {
	start := r.clock()
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	// spec: §25.9 line 3659 — limit default 100, max 1000. Out-of-range
	// or unparseable values are rejected as 400 INVALID_ARGUMENT rather
	// than silently coerced to the default.
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
	filter, ok := parseAuditFilter(w, req, r.clock())
	if !ok {
		return
	}
	// spec: §25.9 line 3668 — a platform-admin query that names no tenantId
	// reads every tenant's chain across all audit shards (scatter-gather),
	// cached per §25.9 line 3709. Routed only when a scatter reader is
	// wired; otherwise the platform-admin no-tenantId query stays on the
	// single-`platform`-tenant path below. F-25.9.11.
	if r.isCrossTenantAuditQuery(req) {
		r.listAuditEventsCrossTenant(w, req, limit, req.URL.Query().Get("cursor"), filter)
		return
	}
	// spec: §25.9 line 3659 — ?cursor= is the opaque pagination token.
	afterSeq, ok := decodeCursor(w, req.URL.Query().Get("cursor"))
	if !ok {
		return
	}

	rows, ok := r.auditRows(w, req, tenant)
	if !ok {
		return
	}
	// Per-row §11.7 integrity is computed over the full ordered chain so
	// link and sequence-gap checks see every row; the filtered page reads
	// each row's verdict out of this map by sequence number.
	integrities := audit.ChainFromRows(tenant, rows, r.auditReceipts(req.Context(), tenant)).VerifyRows()

	items := make([]json.RawMessage, 0, limit)
	report := ChainIntegrityReport{}
	brokenSeen := false
	var nextCursor string
	for _, row := range rows {
		if row.Seq <= afterSeq {
			continue
		}
		if !filter.matchesRow(row) {
			continue
		}
		integrity := integrities[row.Seq]
		ocsfBytes, include, rowOK := r.auditRowItem(w, req, tenant, row, integrity, filter)
		if !rowOK {
			return
		}
		if !include {
			continue
		}
		if len(items) >= limit {
			// One row beyond the page exists: emit a next-page cursor and
			// stop without including it.
			nextCursor = encodeCursor(afterSeq)
			break
		}
		items = append(items, ocsfBytes)
		tallyIntegrity(&report, integrity)
		if integrity == audit.ChainBroken {
			brokenSeen = true
		}
		afterSeq = row.Seq
	}
	if nextCursor != "" {
		nextCursor = encodeCursor(afterSeq)
	}

	envelope := AuditEventEnvelope{
		TenantID:             tenant,
		Items:                items,
		OCSFVersion:          ocsf.Version,
		TranslatorVersion:    ocsf.TranslatorVersion,
		ChainIntegrityReport: &report,
		NextCursor:           nextCursor,
	}
	if gaps := gapWindows(rows, r.auditReceipts(req.Context(), tenant)); len(gaps) > 0 {
		envelope.AuditMetadata = &AuditMetadata{SuspectedGaps: gaps}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)

	// spec: §25.9 line 3750 — record the access (audit-of-audit) and, when
	// a tampered chain segment surfaced in the result, the broken-chain
	// signal that feeds the §16.5 chain-integrity alert pipeline.
	r.emitAuditQueryExecuted(req, tenant, filter, len(items))
	if brokenSeen {
		r.emitChainIntegrityBroken(req, tenant)
	}
	// spec: §25.9 metrics table — query latency + chain-verdict counters.
	// v1 is single-shard, so shards is 1. F-25.9.13.
	r.recordAuditQuery("list", start, 1, report.Broken, report.RechainedPostOutage)
}

// handleGetAuditEvent implements GET /v1/admin/audit-events/{seq}.
// The response wire form is the §4.4 line 232 OCSF v1.1.0 translation
// of the row carrying its §25.9 per-row chainIntegrity verdict, unless
// ?format=raw-canonical returns the pre-OCSF canonical tuple. A missing
// row returns the §25.9 line 3732 AUDIT_EVENT_NOT_FOUND code.
//
// spec: §25.9 lines 3653, 3660, 3732; §4.4 line 232.
func (r *Router) handleGetAuditEvent(w http.ResponseWriter, req *http.Request) {
	start := r.clock()
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
		// The raw-canonical scope is query-parameter-conditional, so it
		// cannot be expressed as the route-level pre-routing gate: the
		// served route scope stays at the coarse tools:audit:read so the
		// ordinary OCSF read is not regressed for a tools:audit:read
		// token. This per-handler check covers only the
		// ?format=raw-canonical branch and now returns the cataloged
		// SCOPE_FORBIDDEN. An absent scope claim defers to the role
		// ceiling (HasScope returns true). spec: §25.9 line 3653; §15.1
		// line 1030.
		if p, _ := authmw.FromContext(req.Context()); !p.HasScope(scopeRawCanonical) {
			writeScopeForbidden(w, scopeRawCanonical, p.Scopes.Raw)
			return
		}
	}
	rows, ok := r.auditRows(w, req, tenant)
	if !ok {
		return
	}
	integrities := audit.ChainFromRows(tenant, rows, r.auditReceipts(req.Context(), tenant)).VerifyRows()
	for _, row := range rows {
		if row.Seq == seq {
			integrity := integrities[row.Seq]
			singleRow := ChainIntegrityReport{}
			tallyIntegrity(&singleRow, integrity)
			if rawCanonical {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rowToCanonical(row, integrity))
				r.recordAuditQuery("get", start, 1, singleRow.Broken, singleRow.RechainedPostOutage)
				return
			}
			rec, terr := ocsfRecordForRow(row, integrity)
			if terr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"audit ocsf translation failed: "+terr.Error(), nil)
				return
			}
			ocsfBytes, merr := ocsf.MarshalRecord(rec)
			if merr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"audit ocsf marshal failed: "+merr.Error(), nil)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(AuditEventEnvelope{
				TenantID:             tenant,
				Items:                []json.RawMessage{ocsfBytes},
				OCSFVersion:          ocsf.Version,
				TranslatorVersion:    ocsf.TranslatorVersion,
				ChainIntegrityReport: &singleRow,
			})
			r.recordAuditQuery("get", start, 1, singleRow.Broken, singleRow.RechainedPostOutage)
			return
		}
	}
	writeError(w, http.StatusNotFound, "AUDIT_EVENT_NOT_FOUND", "audit event not found", nil)
}

// ocsfRecordForRow runs one canonical audit row through the §11.7 OCSF
// translator, stamping its §25.9 per-row chainIntegrity verdict onto the
// `unmapped.lenny_chain.integrity` extension. A translation failure is
// surfaced as an error so the handler returns 500 with the failed-seq
// detail; the spec's dead-letter path (DeadLetterReceipt) is reserved
// for the SIEM-forwarder/EventBus consumers where blocking on a single
// bad row would halt the per-tenant stream.
//
// spec: §4.4 line 232 (OCSF translator); §25.9 lines 3670-3679 (per-row
// chainIntegrity surfaced on each record).
func ocsfRecordForRow(row audit.Row, integrity audit.ChainIntegrity) (ocsf.Record, error) {
	// A row scanned from Postgres carries its stored UUID; the in-memory
	// chain does not, so synthesize a stable UID from (tenant_id, seq)
	// when ID is empty so metadata.uid is deterministic.
	uid := row.ID
	if uid == "" {
		uid = row.TenantID + ":" + strconv.FormatUint(row.Seq, 10)
	}
	if integrity == "" {
		integrity = audit.ChainUnchecked
	}
	in := ocsf.Input{
		ID:              uid,
		Sequence:        row.Seq,
		TenantID:        row.TenantID,
		EventType:       row.EventType,
		CreatedAtUnixMs: row.Timestamp.UTC().UnixMilli(),
		Payload:         row.Payload,
		PrevHash:        row.PrevHash,
		ChainIntegrity:  integrity,
	}
	return ocsf.Translate(in)
}

// tallyIntegrity increments the per-state counter in report for one
// row's §25.9 chainIntegrity verdict.
func tallyIntegrity(report *ChainIntegrityReport, integrity audit.ChainIntegrity) {
	switch integrity {
	case audit.ChainBroken:
		report.Broken++
	case audit.ChainGapSuspected:
		report.GapSuspected++
	case audit.ChainRechainedPostOutage:
		report.RechainedPostOutage++
	case audit.ChainRedactedGDPR:
		report.RedactedGDPR++
	case audit.ChainUnchecked:
		report.Unchecked++
	default:
		report.Verified++
	}
}

// gapReasonNextvalRollback is the §25.9 line 3669 gap-window reason for a
// benign nextval transaction rollback: a sequence-number gap whose
// prev_hash chain links across it. The rollback consumed a per-tenant
// sequence value without committing a row while Postgres was available,
// so it carries no ops_postgres_outage_log window, requires no
// reconciliation, and is never re-stamped rechained_post_outage.
const gapReasonNextvalRollback = "nextval_rollback"

// gapReasonPostgresUnreachable is the §25.9 line 3669 gap-window reason
// for a gap whose prev_hash does not link across it. The gateway does not
// operate the ops_postgres_outage_log subsystem that attributes an outage
// window, so a non-linking gap is reported under this outage reason
// rather than as a benign nextval rollback; the per-row chainIntegrity
// verdict independently flags it, and only a linking gap is benign.
const gapReasonPostgresUnreachable = "postgres_unreachable"

// gapWindows computes the §25.9 line 3669 suspected temporal-gap windows
// from sequence-number discontinuities in the full ordered chain. The
// window spans the timestamps of the rows bracketing the gap. The reason
// distinguishes the two §25.9 gap sources by the prev_hash linkage across
// the gap: a gap whose prev_hash links across it (the row after the gap
// carries the link hash of the row before it) is the benign nextval
// rollback, reported as "nextval_rollback"; a gap whose prev_hash does
// not link is not a benign rollback and is reported under the outage
// reason. The receipts map lets a lawful predecessor redaction still
// count as linked, matching the chain classifier.
//
// spec: §25.9 line 3668-3669 (a gap with an intact prev_hash link is the
// benign nextval rollback reported with reason "nextval_rollback"; only a
// gap with a non-linking prev_hash is broken).
func gapWindows(rows []audit.Row, receipts map[uint64]audit.RedactionReceipt) []AuditGapWindow {
	var out []AuditGapWindow
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq == rows[i-1].Seq+1 {
			continue
		}
		reason := gapReasonPostgresUnreachable
		if audit.PrevHashLinks(rows[i-1], rows[i], receipts) {
			reason = gapReasonNextvalRollback
		}
		out = append(out, AuditGapWindow{
			Start:  rows[i-1].Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
			End:    rows[i].Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
			Reason: reason,
		})
	}
	return out
}

// encodeCursor renders an opaque §25.9 pagination cursor over the last
// returned sequence number. The token is base64 so callers treat it as
// opaque rather than hard-coding a sequence integer.
func encodeCursor(seq uint64) string {
	return base64.RawURLEncoding.EncodeToString([]byte("seq:" + strconv.FormatUint(seq, 10)))
}

// decodeCursor parses an opaque §25.9 pagination cursor back to the
// after-sequence boundary. An empty cursor means "from the start". A
// malformed cursor is rejected as 400 INVALID_ARGUMENT.
func decodeCursor(w http.ResponseWriter, cursor string) (uint64, bool) {
	if cursor == "" {
		return 0, true
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), "seq:") {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor is malformed", nil)
		return 0, false
	}
	seq, err := strconv.ParseUint(strings.TrimPrefix(string(raw), "seq:"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor is malformed", nil)
		return 0, false
	}
	return seq, true
}

// emitAuditQueryExecuted records the §25.9 line 3750 audit.query_executed
// event: the access to the audit trail itself, with the query parameters,
// result count, cache hit/miss, and shards touched. v1 is single-shard
// with no read cache, so cacheHit is false and shardsTouched is 1.
func (r *Router) emitAuditQueryExecuted(req *http.Request, tenant string, filter auditQueryFilter, resultCount int) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		return
	}
	r.emit(req.Context(), p, auditcat.EventAuditQueryExecuted.String(),
		"audit-events", map[string]any{
			"tenantId":      tenant,
			"since":         filter.since.Format("2006-01-02T15:04:05Z07:00"),
			"until":         filter.until.Format("2006-01-02T15:04:05Z07:00"),
			"eventType":     filter.eventType,
			"actorId":       filter.actorID,
			"resourceType":  filter.resourceType,
			"resourceId":    filter.resourceID,
			"severity":      filter.severity,
			"operationId":   filter.operationID,
			"resultCount":   resultCount,
			"cacheHit":      false,
			"shardsTouched": 1,
		})
}

// emitChainIntegrityBroken records the §25.9 line 3750
// audit.chain_integrity_broken_detected event when a query surfaces a
// tampered chain segment, the data source the §16.5 chain-integrity
// alert pipeline consumes.
func (r *Router) emitChainIntegrityBroken(req *http.Request, tenant string) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		return
	}
	r.emit(req.Context(), p, auditcat.EventAuditChainIntegrityBrokenDetected.String(),
		"audit-events", map[string]any{"tenantId": tenant})
}

// AuditSummaryGroup is one §25.9 summary bucket: a grouping key and its
// event count over the queried time window.
type AuditSummaryGroup struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// AuditSummaryResponse is the §25.9 line 3661
// GET /v1/admin/audit-events/summary response: aggregate counts grouped
// by event type, actor, or resource type over a time window.
type AuditSummaryResponse struct {
	TenantID string              `json:"tenantId"`
	GroupBy  string              `json:"groupBy"`
	Since    string              `json:"since"`
	Until    string              `json:"until"`
	Total    int                 `json:"total"`
	Groups   []AuditSummaryGroup `json:"groups"`
}

// handleAuditSummary implements GET /v1/admin/audit-events/summary.
// It returns aggregate counts grouped by eventType (default), actorId,
// or resourceType over the §25.9 time window (defaulting to the last 24
// hours). Groups are returned in descending count order, ties broken by
// key for a stable response.
//
// spec: §25.9 line 3661 (summary endpoint; ?since=, ?until=,
// ?groupBy=eventType|actorId|resourceType).
func (r *Router) handleAuditSummary(w http.ResponseWriter, req *http.Request) {
	start := r.clock()
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	groupBy := req.URL.Query().Get("groupBy")
	if groupBy == "" {
		groupBy = "eventType"
	}
	switch groupBy {
	case "eventType", "actorId", "resourceType":
	default:
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"groupBy must be one of eventType, actorId, resourceType", nil)
		return
	}
	since, until, ok := parseAuditTimeWindow(w, req, r.clock())
	if !ok {
		return
	}
	rows, ok := r.auditRows(w, req, tenant)
	if !ok {
		return
	}

	counts := map[string]int{}
	total := 0
	for _, row := range rows {
		if row.Timestamp.Before(since) || row.Timestamp.After(until) {
			continue
		}
		key := summaryKey(groupBy, row)
		counts[key]++
		total++
	}
	groups := make([]AuditSummaryGroup, 0, len(counts))
	for k, c := range counts {
		groups = append(groups, AuditSummaryGroup{Key: k, Count: c})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Key < groups[j].Key
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AuditSummaryResponse{
		TenantID: tenant,
		GroupBy:  groupBy,
		Since:    since.Format("2006-01-02T15:04:05Z07:00"),
		Until:    until.Format("2006-01-02T15:04:05Z07:00"),
		Total:    total,
		Groups:   groups,
	})
	r.recordAuditQuery("summary", start, 1, 0, 0)
}

// summaryKey extracts a row's grouping key for the §25.9 summary
// endpoint. An empty key is bucketed as "(none)" so the response is
// self-describing rather than carrying a blank bucket.
func summaryKey(groupBy string, row audit.Row) string {
	var key string
	switch groupBy {
	case "eventType":
		key = row.EventType
	case "actorId":
		payload := decodePayload(row.Payload)
		key = stringFrom(payload, "user_id")
		if key == "" {
			key = stringFrom(payload, "actor_subject")
		}
	case "resourceType":
		key = resourceTypeOf(decodePayload(row.Payload))
	}
	if key == "" {
		return "(none)"
	}
	return key
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
	// §25.9 line 3662 — retranslate is gated on tools:audit:retranslate.
	// The central §25.1 scope gate in Router.Handler resolves the route's
	// x-lenny-scope (the fine-grained retranslate form) and rejects a
	// scope-narrowed token with SCOPE_FORBIDDEN before this handler runs,
	// so no per-handler scope check is needed here. spec: §25.1 line 94.
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

// auditPublishStateStore is the optional §25.9 line 3663
// eventbus_publish_state read+write surface backing the republish
// endpoint. The Postgres-backed auditstore implements it; the in-memory
// chain does not, because it drives no §12.6 outbound publish queue, so
// its rows are always reported `published` and never eligible for
// republication. spec: §12.3.7 eventbus_publish_state; §25.9 line 3663.
type auditPublishStateStore interface {
	PublishState(ctx context.Context, tenantID string, seq uint64) (eventbus.PublishState, int, error)
	SetPublishState(ctx context.Context, tenantID string, seq uint64, state eventbus.PublishState, retryCount int) error
}

// republishResponse echoes the reset row state per §25.9 line 3663.
type republishResponse struct {
	Seq                  uint64 `json:"seq"`
	EventbusPublishState string `json:"eventbusPublishState"`
	PriorState           string `json:"priorState"`
	PriorRetryCount      int    `json:"priorRetryCount"`
}

// handleRepublishAuditEvent implements
// POST /v1/admin/audit-events/{seq}/republish. It re-queues a single
// audit row for §12.6 CloudEvents re-publication after the EventBus
// retranscribe worker has terminally abandoned it (state `failed`,
// retry_count ≥ eventBus.maxRetryAttempts). The endpoint resets
// retry_count = 0 and eventbus_publish_state = 'pending' so the next
// retranscribe sweep picks the row up. Only rows in `failed` are
// eligible; rows in `published`, `pending`, or `retry_pending` return
// 409 ALREADY_PUBLISHED carrying details.currentState so an operator can
// distinguish in-flight from completed. A missing seq returns 404
// NOT_FOUND. Scope-gated on audit:republish. On success it records the
// §16.7 eventbus.republish_requested event. The {seq} path segment is the
// audit row identifier (v1 keys the audit table by tenant + sequence
// number); the spec names it {id}.
//
// spec: §25.9 line 3663; §16.7 (eventbus.republish_requested).
func (r *Router) handleRepublishAuditEvent(w http.ResponseWriter, req *http.Request) {
	tenant, ok := r.auditTenant(w, req)
	if !ok {
		return
	}
	// §25.9 line 3663 — republish is gated on tools:audit:republish. The
	// central §25.1 scope gate in Router.Handler resolves the route's
	// x-lenny-scope (the fine-grained republish form) and rejects a
	// scope-narrowed token with SCOPE_FORBIDDEN before this handler runs,
	// so no per-handler scope check is needed here. spec: §25.1 line 94.
	seq, err := strconv.ParseUint(req.PathValue("seq"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "seq must be a positive integer", nil)
		return
	}
	rows, ok := r.auditRows(w, req, tenant)
	if !ok {
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
		// spec: §25.9 line 3663 — a missing id returns 404 NOT_FOUND.
		writeError(w, http.StatusNotFound, "NOT_FOUND", "audit event not found", nil)
		return
	}

	// Resolve the publish state. A backend without publish-state tracking
	// drives no outbound queue, so its rows report `published` and are
	// never eligible.
	state := eventbus.PublishPublished
	retryCount := 0
	store, hasStore := r.auditLog.(auditPublishStateStore)
	if hasStore {
		var serr error
		state, retryCount, serr = store.PublishState(req.Context(), tenant, seq)
		if serr != nil {
			writeError(w, http.StatusServiceUnavailable, "AUDIT_STORE_UNAVAILABLE",
				"audit publish-state lookup failed: "+serr.Error(), nil)
			return
		}
	}
	if state != eventbus.PublishFailed {
		// spec: §25.9 line 3663 — published rows and in-flight rows
		// (pending / retry_pending) are not re-queued; the discriminator
		// is details.currentState.
		writeError(w, http.StatusConflict, "ALREADY_PUBLISHED",
			"audit row is not in the failed publish state (current state "+string(state)+")",
			map[string]any{"currentState": string(state)})
		return
	}

	// Reset to pending and clear the retry counter so the next
	// retranscribe sweep re-publishes the row.
	if err := store.SetPublishState(req.Context(), tenant, seq, eventbus.PublishPending, 0); err != nil {
		writeError(w, http.StatusServiceUnavailable, "AUDIT_STORE_UNAVAILABLE",
			"audit publish-state update failed: "+err.Error(), nil)
		return
	}

	eventID := found.ID
	if eventID == "" {
		eventID = tenant + ":" + strconv.FormatUint(seq, 10)
	}
	if p, pok := authmw.FromContext(req.Context()); pok {
		r.emit(req.Context(), p, auditcat.EventEventBusRepublishRequested.String(),
			"audit-event/"+strconv.FormatUint(seq, 10), map[string]any{
				"event_id":          eventID,
				"prior_state":       string(eventbus.PublishFailed),
				"prior_retry_count": retryCount,
				"requester_sub":     p.Subject,
				"tenant_id":         tenant,
				"topic":             string(eventbus.TopicSessionLifecycle),
			})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(republishResponse{
		Seq:                  seq,
		EventbusPublishState: string(eventbus.PublishPending),
		PriorState:           string(eventbus.PublishFailed),
		PriorRetryCount:      retryCount,
	})
}
