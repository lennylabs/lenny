// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// metering page-size bounds for GET /v1/metering/events.
const (
	meteringPageDefault = 100
	meteringPageMax     = 1000
)

// meteringEvent is the §11.2.1 billing event wire shape. Conditional
// fields are omitted when zero so a consumer sees "not applicable"
// rather than a misleading zero.
type meteringEvent struct {
	SchemaVersion  uint32 `json:"schemaVersion"`
	SequenceNumber uint64 `json:"sequenceNumber"`
	TenantID       string `json:"tenantId"`
	UserID         string `json:"userId,omitempty"`
	SessionID      string `json:"sessionId,omitempty"`
	ExperimentID   string `json:"experimentId,omitempty"`
	VariantID      string `json:"variantId,omitempty"`
	EventType      string `json:"eventType"`
	TokensInput    uint64 `json:"tokensInput,omitempty"`
	TokensOutput   uint64 `json:"tokensOutput,omitempty"`
	Timestamp      string `json:"timestamp"`
}

// meteringPage is the §15.1 cursor-paginated response envelope. When
// hasMore is true, nextCursor carries the since_sequence value for the
// following page.
type meteringPage struct {
	Events     []meteringEvent `json:"events"`
	NextCursor string          `json:"nextCursor,omitempty"`
	HasMore    bool            `json:"hasMore"`
}

// handleMeteringEvents implements GET /v1/metering/events per §15.1 —
// the paginated §11.2.1 billing event stream. Events with
// sequence_number greater than ?since_sequence are returned in
// ascending order, capped at ?limit. The endpoint requires the
// billing-viewer, tenant-admin, or platform-admin role.
func (s *Server) handleMeteringEvents(w http.ResponseWriter, r *http.Request) {
	principal, ok := getPrincipal(r)
	if !ok || !(principal.HasRole(pkgauth.RoleBillingViewer) ||
		principal.HasRole(pkgauth.RoleTenantAdmin) ||
		principal.HasRole(pkgauth.RolePlatformAdmin)) {
		s.writeError(w, http.StatusForbidden, "FORBIDDEN",
			"metering events require the billing-viewer, tenant-admin, or platform-admin role", nil)
		return
	}

	since, err := uintQuery(r, "since_sequence", 0)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"since_sequence must be a non-negative integer", map[string]any{"field": "since_sequence"})
		return
	}
	limit, err := uintQuery(r, "limit", meteringPageDefault)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"limit must be a non-negative integer", map[string]any{"field": "limit"})
		return
	}
	pageSize := int(limit)
	if pageSize <= 0 || pageSize > meteringPageMax {
		pageSize = meteringPageMax
	}

	page := meteringPage{Events: []meteringEvent{}}
	if s.billing != nil {
		tenantID := s.resolveTenant(r)
		// Fetch one extra row to detect whether a further page exists.
		events, err := s.billing.Since(r.Context(), tenantID, since, pageSize+1)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if len(events) > pageSize {
			events = events[:pageSize]
			page.HasMore = true
			page.NextCursor = strconv.FormatUint(events[len(events)-1].SequenceNumber, 10)
		}
		for _, e := range events {
			page.Events = append(page.Events, toMeteringEvent(e))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

// toMeteringEvent maps a stored billing event to its wire shape.
func toMeteringEvent(e billingstore.Event) meteringEvent {
	return meteringEvent{
		SchemaVersion:  e.SchemaVersion,
		SequenceNumber: e.SequenceNumber,
		TenantID:       e.TenantID,
		UserID:         e.UserID,
		SessionID:      e.SessionID,
		ExperimentID:   e.ExperimentID,
		VariantID:      e.VariantID,
		EventType:      string(e.EventType),
		TokensInput:    e.TokensInput,
		TokensOutput:   e.TokensOutput,
		Timestamp:      e.CreatedAt.UTC().Format(time.RFC3339Nano),
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
