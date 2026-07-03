// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	auditcat "github.com/lennylabs/lenny/pkg/observability/audit"
)

// scatterCacheTTL is the §25.9 line 3709 cross-tenant scatter-gather
// result cache lifetime: 5 minutes.
const scatterCacheTTL = 5 * time.Minute

// auditScatterReader is the optional §25.9 line 3668 platform-admin
// cross-tenant audit read surface. *auditstore.Store satisfies it
// (ScatterGatherRows fans out across AllAuditShards). When the wired
// backend does not implement it — the in-memory dev gateway — the
// platform-admin no-tenantId query stays on the single-`platform`-tenant
// read path.
type auditScatterReader interface {
	// ScatterGatherRows returns every tenant's audit rows across all
	// shards (ordered by tenant_id, sequence_number) plus the list of
	// shards that were unreachable (driving the 207 AUDIT_PARTIAL_RESULTS
	// path). A non-nil error signals a total outage → 503.
	ScatterGatherRows(ctx context.Context) (rows []audit.Row, missingShards []string, err error)
}

// ScatterGatherCache is the §25.9 line 3709 cross-tenant result cache.
// The in-memory MemScatterGatherCache satisfies it; a Redis-backed
// implementation is a documented seam behind the same interface (the
// gateway wires whichever is available). Get returns ok=false on a miss
// or an expired entry.
type ScatterGatherCache interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl time.Duration)
}

// MemScatterGatherCache is the in-process default ScatterGatherCache: a
// per-key TTL map with an injectable clock. The Redis-backed cache the
// §25.9 line 3709 spec names is the same interface; this keeps the
// single-replica dev/test gateway functional without Redis.
type MemScatterGatherCache struct {
	clock func() time.Time
	mu    sync.Mutex
	items map[string]scatterCacheEntry
}

type scatterCacheEntry struct {
	value   []byte
	expires time.Time
}

// NewMemScatterGatherCache returns an in-memory ScatterGatherCache. clock
// defaults to time.Now when nil.
func NewMemScatterGatherCache(clock func() time.Time) *MemScatterGatherCache {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &MemScatterGatherCache{clock: clock, items: map[string]scatterCacheEntry{}}
}

// Get returns the cached bytes for key when present and unexpired.
func (c *MemScatterGatherCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if !c.clock().Before(e.expires) {
		delete(c.items, key)
		return nil, false
	}
	return e.value, true
}

// Set stores value under key for ttl. A non-positive ttl is a no-op.
func (c *MemScatterGatherCache) Set(key string, value []byte, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = scatterCacheEntry{value: value, expires: c.clock().Add(ttl)}
}

// WithAuditScatter wires the §25.9 platform-admin cross-tenant
// scatter-gather reader onto the Router. Without it the platform-admin
// no-tenantId query reads only the `platform` tenant.
func (r *Router) WithAuditScatter(s auditScatterReader) *Router {
	r.auditScatter = s
	return r
}

// WithScatterGatherCache wires the §25.9 line 3709 cross-tenant result
// cache and enables it. Pass enabled=false to honor
// `ops.audit.scatterGatherCacheEnabled: false` (the cache is opt-out).
func (r *Router) WithScatterGatherCache(cache ScatterGatherCache, enabled bool) *Router {
	r.scatterCache = cache
	r.scatterCacheEnabled = enabled
	return r
}

// AuditDegradation is the §25.9 207 AUDIT_PARTIAL_RESULTS envelope: it
// lists the audit shards that were unreachable for a cross-tenant
// scatter-gather query whose response carries only the reachable shards'
// events.
type AuditDegradation struct {
	Level         string   `json:"level"`
	MissingShards []string `json:"missingShards"`
	Reason        string   `json:"reason"`
}

// isCrossTenantAuditQuery reports whether req is the §25.9 line 3668
// platform-admin cross-tenant query: a platform-admin caller that names
// no `tenantId`, when a scatter-gather reader is wired.
func (r *Router) isCrossTenantAuditQuery(req *http.Request) bool {
	if r.auditScatter == nil {
		return false
	}
	if req.URL.Query().Get("tenantId") != "" {
		return false
	}
	p, ok := authmw.FromContext(req.Context())
	return ok && p.HasRole(pkgauth.RolePlatformAdmin)
}

// scatterCacheKey hashes the §25.9 line 3709 query parameters into the
// cross-tenant cache key so a different page or filter does not serve a
// stale entry.
func scatterCacheKey(f auditQueryFilter, limit int, cursor string) string {
	canonical := strings.Join([]string{
		"cross",
		f.since.UTC().Format(time.RFC3339Nano),
		f.until.UTC().Format(time.RFC3339Nano),
		f.eventType,
		f.actorID,
		f.resourceType,
		f.resourceID,
		f.severity,
		f.operationID,
		string(f.translationState),
		string(f.publishState),
		"limit=" + strconv.Itoa(limit),
		"cursor=" + cursor,
	}, "\x1f")
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// crossCursor is the §25.9 cross-tenant pagination marker. A single
// sequence number is insufficient because each tenant numbers its chain
// independently; the cross-tenant page orders rows by (tenant_id, seq)
// and resumes after the (tenant, seq) tuple of the last returned row.
type crossCursor struct {
	tenant string
	seq    uint64
}

func encodeCrossCursor(c crossCursor) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte("xseq:" + c.tenant + ":" + strconv.FormatUint(c.seq, 10)),
	)
}

// decodeCrossCursor parses a cross-tenant cursor. An empty cursor starts
// at the beginning. A malformed cursor writes 400 and returns ok=false.
func decodeCrossCursor(w http.ResponseWriter, cursor string) (crossCursor, bool) {
	if cursor == "" {
		return crossCursor{}, true
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), "xseq:") {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor is malformed", nil)
		return crossCursor{}, false
	}
	rest := strings.TrimPrefix(string(raw), "xseq:")
	idx := strings.LastIndexByte(rest, ':')
	if idx < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor is malformed", nil)
		return crossCursor{}, false
	}
	seq, err := strconv.ParseUint(rest[idx+1:], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor is malformed", nil)
		return crossCursor{}, false
	}
	return crossCursor{tenant: rest[:idx], seq: seq}, true
}

// afterCross reports whether (tenant, seq) sorts strictly after the
// cursor marker in the (tenant_id, seq) total order the cross-tenant
// page iterates.
func afterCross(c crossCursor, tenant string, seq uint64) bool {
	if tenant != c.tenant {
		return tenant > c.tenant
	}
	return seq > c.seq
}

// crossTenantKey composes the per-row §11.7 integrity-map key for the
// cross-tenant page. Sequence numbers repeat across tenants, so the key
// must carry the tenant.
func crossTenantKey(tenant string, seq uint64) string {
	return tenant + "\x00" + strconv.FormatUint(seq, 10)
}

// crossTenantIntegrities verifies each tenant's §11.7 chain separately
// (chains are per-tenant) and returns a composite map keyed by
// crossTenantKey. Rows arrive grouped by tenant in ascending
// (tenant_id, seq) order.
func crossTenantIntegrities(rows []audit.Row) map[string]audit.ChainIntegrity {
	out := make(map[string]audit.ChainIntegrity, len(rows))
	i := 0
	for i < len(rows) {
		j := i
		for j < len(rows) && rows[j].TenantID == rows[i].TenantID {
			j++
		}
		group := rows[i:j]
		verdicts := audit.ChainFromRows(group[0].TenantID, group, nil).VerifyRows()
		for _, row := range group {
			out[crossTenantKey(row.TenantID, row.Seq)] = verdicts[row.Seq]
		}
		i = j
	}
	return out
}

// crossTenantGaps computes §25.9 line 3679 suspected gap windows
// per-tenant (sequence numbers reset across tenants, so a cross-tenant
// boundary is never a gap) and concatenates them.
func crossTenantGaps(rows []audit.Row) []AuditGapWindow {
	var out []AuditGapWindow
	i := 0
	for i < len(rows) {
		j := i
		for j < len(rows) && rows[j].TenantID == rows[i].TenantID {
			j++
		}
		// The cross-tenant page does not load per-tenant redaction
		// receipts (verdicts above use a nil receipt map for the same
		// reason), so a lawfully-redacted predecessor across a gap is
		// treated as non-linking. gapWindows lists only the benign
		// nextval-rollback window, so such a gap is omitted here and the
		// per-row chainIntegrity verdict flags it instead.
		out = append(out, gapWindows(rows[i:j], nil)...)
		i = j
	}
	return out
}

// auditRowItem applies the §25.9 per-row translation-state, publish-state,
// and severity filters to one row, runs the §4.4 OCSF translation, and
// returns the marshalled record. include=false means the row is filtered
// out; ok=false means a fatal read/translation error was written to w.
// tenant is the row's tenant (the row.TenantID for the cross-tenant page,
// the fixed tenant for the single-tenant page).
func (r *Router) auditRowItem(w http.ResponseWriter, req *http.Request, tenant string, row audit.Row, integrity audit.ChainIntegrity, filter auditQueryFilter) (item []byte, include, ok bool) {
	if filter.translationState != "" {
		st, _, serr := r.rowTranslationState(req.Context(), tenant, row.Seq)
		if serr != nil {
			writeError(w, http.StatusServiceUnavailable, "AUDIT_STORE_UNAVAILABLE",
				"audit translation-state lookup failed at seq "+strconv.FormatUint(row.Seq, 10)+": "+serr.Error(), nil)
			return nil, false, false
		}
		if st != filter.translationState {
			return nil, false, true
		}
	}
	if filter.publishState != "" {
		ps, _, perr := r.rowPublishState(req.Context(), tenant, row.Seq)
		if perr != nil {
			writeError(w, http.StatusServiceUnavailable, "AUDIT_STORE_UNAVAILABLE",
				"audit publish-state lookup failed at seq "+strconv.FormatUint(row.Seq, 10)+": "+perr.Error(), nil)
			return nil, false, false
		}
		if ps != filter.publishState {
			return nil, false, true
		}
	}
	rec, terr := ocsfRecordForRow(row, integrity)
	if terr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit ocsf translation failed at seq "+strconv.FormatUint(row.Seq, 10)+": "+terr.Error(), nil)
		return nil, false, false
	}
	if !filter.matchesSeverity(rec.SeverityID) {
		return nil, false, true
	}
	ocsfBytes, merr := ocsf.MarshalRecord(rec)
	if merr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit ocsf marshal failed at seq "+strconv.FormatUint(row.Seq, 10)+": "+merr.Error(), nil)
		return nil, false, false
	}
	return ocsfBytes, true, true
}

// listAuditEventsCrossTenant serves the §25.9 line 3668 platform-admin
// cross-tenant query. It reads cached results when the cache is enabled
// and `?fresh=true` is absent; otherwise it scatter-gathers across all
// audit shards, verifies each tenant's chain, and renders the page. A
// partial-shard outage returns 207 AUDIT_PARTIAL_RESULTS with the
// degradation envelope; a total outage returns 503.
//
// spec: §25.9 lines 3668, 3709, 3710; "Degradation" (207/503).
func (r *Router) listAuditEventsCrossTenant(w http.ResponseWriter, req *http.Request, limit int, cursorRaw string, filter auditQueryFilter) {
	start := r.clock()
	fresh := req.URL.Query().Get("fresh") == "true"
	useCache := r.scatterCacheEnabled && r.scatterCache != nil && !fresh
	key := scatterCacheKey(filter, limit, cursorRaw)
	if useCache {
		if cached, hit := r.scatterCache.Get(key); hit {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cached)
			r.emitAuditQueryCross(req, filter, cachedItemCount(cached), true)
			r.recordAuditQuery("list_cross_tenant", start, 1, 0, 0)
			return
		}
	}

	after, ok := decodeCrossCursor(w, cursorRaw)
	if !ok {
		return
	}
	rows, missing, err := r.auditScatter.ScatterGatherRows(req.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "AUDIT_STORE_UNAVAILABLE",
			"audit store unreachable: "+err.Error(), nil)
		return
	}
	integrities := crossTenantIntegrities(rows)

	items := make([]json.RawMessage, 0, limit)
	report := ChainIntegrityReport{}
	brokenSeen := false
	var nextCursor string
	for _, row := range rows {
		if !afterCross(after, row.TenantID, row.Seq) {
			continue
		}
		if !filter.matchesRow(row) {
			continue
		}
		integrity := integrities[crossTenantKey(row.TenantID, row.Seq)]
		ocsfBytes, include, rowOK := r.auditRowItem(w, req, row.TenantID, row, integrity, filter)
		if !rowOK {
			return
		}
		if !include {
			continue
		}
		if len(items) >= limit {
			nextCursor = encodeCrossCursor(after)
			break
		}
		items = append(items, ocsfBytes)
		tallyIntegrity(&report, integrity)
		if integrity == audit.ChainBroken {
			brokenSeen = true
		}
		after = crossCursor{tenant: row.TenantID, seq: row.Seq}
	}
	if nextCursor != "" {
		nextCursor = encodeCrossCursor(after)
	}

	envelope := AuditEventEnvelope{
		TenantID:             "",
		Items:                items,
		OCSFVersion:          ocsf.Version,
		TranslatorVersion:    ocsf.TranslatorVersion,
		ChainIntegrityReport: &report,
		NextCursor:           nextCursor,
	}
	if gaps := crossTenantGaps(rows); len(gaps) > 0 {
		envelope.AuditMetadata = &AuditMetadata{SuspectedGaps: gaps}
	}
	status := http.StatusOK
	if len(missing) > 0 {
		envelope.Degradation = &AuditDegradation{
			Level:         "degraded",
			MissingShards: missing,
			Reason:        "partial_shard_outage",
		}
		status = http.StatusMultiStatus
	}
	body, merr := json.Marshal(envelope)
	if merr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"audit response marshal failed: "+merr.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)

	// spec: §25.9 line 3709 — cache only complete (200) cross-tenant
	// results; a partial-shard 207 is degraded and must not be served as a
	// healthy cache hit.
	if useCache && status == http.StatusOK {
		r.scatterCache.Set(key, body, scatterCacheTTL)
	}

	r.emitAuditQueryCross(req, filter, len(items), false)
	if brokenSeen {
		r.emitChainIntegrityBroken(req, "platform")
	}
	r.recordAuditQuery("list_cross_tenant", start, 1, report.Broken, report.RechainedPostOutage)
}

// emitAuditQueryCross emits the §25.9 line 3750 audit.query_executed
// receipt for a cross-tenant query, carrying the empty tenant scope, the
// cache-hit flag, and the shard fan-out width (1 in single-shard v1).
func (r *Router) emitAuditQueryCross(req *http.Request, filter auditQueryFilter, resultCount int, cacheHit bool) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		return
	}
	r.emit(req.Context(), p, auditcat.EventAuditQueryExecuted.String(),
		"audit-events", map[string]any{
			"tenantId":      "",
			"crossTenant":   true,
			"since":         filter.since.Format(time.RFC3339),
			"until":         filter.until.Format(time.RFC3339),
			"eventType":     filter.eventType,
			"actorId":       filter.actorID,
			"resourceType":  filter.resourceType,
			"resourceId":    filter.resourceID,
			"severity":      filter.severity,
			"operationId":   filter.operationID,
			"resultCount":   resultCount,
			"cacheHit":      cacheHit,
			"shardsTouched": 1,
		})
}

// cachedItemCount reports the number of items in a cached cross-tenant
// envelope so the cache-hit audit receipt records the served result
// count without re-running the scatter-gather.
func cachedItemCount(cached []byte) int {
	var env struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(cached, &env); err != nil {
		return 0
	}
	return len(env.Items)
}
