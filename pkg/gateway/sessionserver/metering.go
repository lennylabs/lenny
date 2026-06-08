// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
)

// meteringSortField is the only sort key the §11.2.1 billing ledger
// exposes — the per-tenant monotonic sequence number that
// `billingstore.Since` indexes on. Other fields would require a full
// scan, which §15.1 line 1252 explicitly excludes from `total`
// computation; we exclude them from `sort` for the same reason.
const meteringSortField = "sequenceNumber"

// meteringDefaultSort sorts by per-tenant monotonic sequence
// ascending so a consumer reading the §11.2.1 billing stream sees
// events in the same order they were appended.
var meteringDefaultSort = pagination.Sort{Field: meteringSortField, Direction: pagination.DirectionAsc}

// meteringEvent is the §11.2.1 billing event wire shape. Conditional
// fields are omitted when zero so a consumer sees "not applicable"
// rather than a misleading zero. The §11.2.1 correction and cost
// dimensions (pod_minutes, corrects_sequence, correction_reason_code,
// correction_detail) and the event-type-specific conditional block are
// surfaced so a consumer following the §11.2.1 "Correction semantics"
// can reconstruct the accurate ledger; the embedded *billingstore.Conditional
// promotes its fields to the top level (and is omitted entirely when the
// event carries no event-type-specific data), honoring the §11.2.1
// null/absent field contract. F-11.2.12.
type meteringEvent struct {
	SchemaVersion        uint32  `json:"schemaVersion"`
	SequenceNumber       uint64  `json:"sequenceNumber"`
	TenantID             string  `json:"tenantId"`
	UserID               string  `json:"userId,omitempty"`
	SessionID            string  `json:"sessionId,omitempty"`
	ExperimentID         string  `json:"experimentId,omitempty"`
	VariantID            string  `json:"variantId,omitempty"`
	EventType            string  `json:"eventType"`
	TokensInput          uint64  `json:"tokensInput,omitempty"`
	TokensOutput         uint64  `json:"tokensOutput,omitempty"`
	PodMinutes           float64 `json:"podMinutes,omitempty"`
	CorrectsSequence     uint64  `json:"correctsSequence,omitempty"`
	CorrectionReasonCode string  `json:"correctionReasonCode,omitempty"`
	CorrectionDetail     string  `json:"correctionDetail,omitempty"`
	// Labels echoes the §14 session-label set denormalized onto the event
	// so a consumer can read the labels it filters on. Omitted when the
	// event carries none. spec: §14 line 106. F-14.1.13.
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp string            `json:"timestamp"`
	*billingstore.Conditional
}

// handleMeteringEvents implements GET /v1/metering/events per §15.1 —
// the paginated §11.2.1 billing event stream. Returns the §15.1
// canonical `{items, cursor, hasMore}` envelope; cursors are opaque
// with 24-hour TTL, limit clamped to [1, 200], sort restricted to
// `sequenceNumber:asc|desc`. The legacy `?since_sequence=` parameter
// is honoured for backwards-compatible callers (cursor is the
// canonical form). Requires the §10.2 view_usage permission.
// spec: §15.1 lines 1228-1253; §11.2.1 line 282.
func (s *Server) handleMeteringEvents(w http.ResponseWriter, r *http.Request) {
	principal, ok := getPrincipal(r)
	if !ok || !pkgauth.RolesGrant(principal.Roles, pkgauth.PermViewUsage) {
		s.writeError(w, http.StatusForbidden, "FORBIDDEN",
			"metering events require the view_usage permission", nil)
		return
	}

	params, ferr := pagination.ParseRequest(r,
		[]string{meteringSortField}, meteringDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}

	since := uint64(0)
	if params.Cursor.Tiebreak != "" {
		if n, err := strconv.ParseUint(params.Cursor.Tiebreak, 10, 64); err == nil {
			since = n
		}
	} else if v := r.URL.Query().Get("since_sequence"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"since_sequence must be a non-negative integer",
				map[string]any{"field": "since_sequence"})
			return
		}
		since = n
	}

	// spec: §14 line 106 — the repeatable `?label=key=value` query scopes
	// the billing stream to events carrying every requested label. The
	// predicate is pushed into the store query so the cursor/hasMore
	// pagination below stays correct. F-14.1.13.
	labelFilter := parseLabelFilter(r.URL.Query()["label"])

	envelope := pagination.Envelope[meteringEvent]{Items: []meteringEvent{}}
	if s.billing != nil {
		tenantID := s.resolveTenant(r)
		// Fetch one extra row to detect whether a further page exists.
		events, err := s.billing.SinceFiltered(r.Context(), tenantID, since, params.Limit+1, labelFilter)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if len(events) > params.Limit {
			events = events[:params.Limit]
			envelope.HasMore = true
		}
		for _, e := range events {
			envelope.Items = append(envelope.Items, toMeteringEvent(e))
		}
		if envelope.HasMore && len(envelope.Items) > 0 {
			last := envelope.Items[len(envelope.Items)-1]
			seqStr := strconv.FormatUint(last.SequenceNumber, 10)
			envelope.Cursor = pagination.MintCursor(params.Sort, seqStr, seqStr, s.clock())
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)
}

// toMeteringEvent maps a stored billing event to its wire shape.
func toMeteringEvent(e billingstore.Event) meteringEvent {
	return meteringEvent{
		SchemaVersion:        e.SchemaVersion,
		SequenceNumber:       e.SequenceNumber,
		TenantID:             e.TenantID,
		UserID:               e.UserID,
		SessionID:            e.SessionID,
		ExperimentID:         e.ExperimentID,
		VariantID:            e.VariantID,
		EventType:            string(e.EventType),
		TokensInput:          e.TokensInput,
		TokensOutput:         e.TokensOutput,
		PodMinutes:           e.PodMinutes,
		CorrectsSequence:     e.CorrectsSequence,
		CorrectionReasonCode: string(e.CorrectionReasonCode),
		CorrectionDetail:     e.CorrectionDetail,
		Labels:               e.Labels,
		Timestamp:            e.CreatedAt.UTC().Format(time.RFC3339Nano),
		Conditional:          e.Conditional,
	}
}

// uintQuery parses a non-negative integer query parameter, returning
// fallback when the parameter is absent.
func uintQuery(r *http.Request, name string, fallback uint64) (uint64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}
