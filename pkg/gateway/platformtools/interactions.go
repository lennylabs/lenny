// SPDX-License-Identifier: MIT

package platformtools

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
)

// handleToolUseApprove implements
// POST /v1/sessions/{id}/tool-use/{toolCallId}/approve per §15.1.
func (h *Handler) handleToolUseApprove(w http.ResponseWriter, r *http.Request) {
	h.resolveInteraction(w, r, interactionResolution{
		kind:  interactionstore.KindToolUse,
		phase: interactionstore.PhaseApproved,
	})
}

// handleToolUseDeny implements
// POST /v1/sessions/{id}/tool-use/{toolCallId}/deny per §15.1.
// Optional body: {"reason": "<string>"}.
func (h *Handler) handleToolUseDeny(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		reader := jsonReader(w, r)
		defer reader.Close()
		_ = json.NewDecoder(reader).Decode(&body)
	}
	h.resolveInteraction(w, r, interactionResolution{
		kind:   interactionstore.KindToolUse,
		phase:  interactionstore.PhaseDenied,
		reason: body.Reason,
	})
}

// handleElicitationRespond implements
// POST /v1/sessions/{id}/elicitations/{elicitationId}/respond per
// §15.1. Body: {"response": <value>}.
func (h *Handler) handleElicitationRespond(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Response any `json:"response"`
	}
	reader := jsonReader(w, r)
	defer reader.Close()
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	h.resolveInteraction(w, r, interactionResolution{
		kind:     interactionstore.KindElicitation,
		phase:    interactionstore.PhaseResponded,
		response: body.Response,
	})
}

// handleElicitationDismiss implements
// POST /v1/sessions/{id}/elicitations/{elicitationId}/dismiss per
// §15.1.
func (h *Handler) handleElicitationDismiss(w http.ResponseWriter, r *http.Request) {
	h.resolveInteraction(w, r, interactionResolution{
		kind:  interactionstore.KindElicitation,
		phase: interactionstore.PhaseDismissed,
	})
}
