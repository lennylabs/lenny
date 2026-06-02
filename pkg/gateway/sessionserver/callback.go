// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// webhookEventDTO is one undelivered §14 callback event in the
// GET /v1/sessions/{id}/webhook-events response. The CloudEvents body is
// rendered inline as JSON rather than the base64 []byte form so a client
// can read the event it can re-deliver. spec: §14 line 150; §15.1 line
// 678. F-14.1.11.
type webhookEventDTO struct {
	EventID     string          `json:"eventId"`
	EventType   string          `json:"eventType"`
	CallbackURL string          `json:"callbackUrl"`
	Event       json.RawMessage `json:"event,omitempty"`
	Attempts    int             `json:"attempts"`
	LastError   string          `json:"lastError,omitempty"`
	LastStatus  int             `json:"lastStatus,omitempty"`
	FailedAt    time.Time       `json:"failedAt"`
}

// webhookEventsResponse is the GET /v1/sessions/{id}/webhook-events
// envelope. Items is never null so a client can range over it directly.
type webhookEventsResponse struct {
	SessionID string            `json:"sessionId"`
	Items     []webhookEventDTO `json:"items"`
	HasMore   bool              `json:"hasMore"`
}

// handleWebhookEvents implements GET /v1/sessions/{id}/webhook-events:
// the §15.1 line 678 list of §14 callback events that exhausted their
// delivery retry budget. spec: §15.1 line 678; §14 line 150. F-14.1.11.
func (s *Server) handleWebhookEvents(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	items := make([]webhookEventDTO, 0, len(row.WebhookEvents))
	for _, ev := range row.WebhookEvents {
		items = append(items, webhookEventDTO{
			EventID:     ev.EventID,
			EventType:   ev.EventType,
			CallbackURL: ev.CallbackURL,
			Event:       json.RawMessage(ev.Body),
			Attempts:    ev.Attempts,
			LastError:   ev.LastError,
			LastStatus:  ev.LastStatus,
			FailedAt:    ev.FailedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(webhookEventsResponse{
		SessionID: row.ID,
		Items:     items,
		HasMore:   false,
	})
}
