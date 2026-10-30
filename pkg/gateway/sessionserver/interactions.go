// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
)

// handleToolUseApprove implements
// POST /v1/sessions/{id}/tool-use/{toolCallId}/approve per §15.1.
func (s *Server) handleToolUseApprove(w http.ResponseWriter, r *http.Request) {
	s.resolveInteraction(w, r, interactionResolution{
		kind:  interactionstore.KindToolUse,
		phase: interactionstore.PhaseApproved,
	})
}

// handleToolUseDeny implements
// POST /v1/sessions/{id}/tool-use/{toolCallId}/deny per §15.1.
// Optional body: {"reason": "<string>"}.
func (s *Server) handleToolUseDeny(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		reader := jsonReader(w, r)
		defer reader.Close()
		_ = json.NewDecoder(reader).Decode(&body)
	}
	s.resolveInteraction(w, r, interactionResolution{
		kind:   interactionstore.KindToolUse,
		phase:  interactionstore.PhaseDenied,
		reason: body.Reason,
	})
}

// handleElicitationRespond implements
// POST /v1/sessions/{id}/elicitations/{elicitationId}/respond per
// §15.1. Body: {"response": <value>}.
func (s *Server) handleElicitationRespond(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Response any `json:"response"`
	}
	reader := jsonReader(w, r)
	defer reader.Close()
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	s.resolveInteraction(w, r, interactionResolution{
		kind:     interactionstore.KindElicitation,
		phase:    interactionstore.PhaseResponded,
		response: body.Response,
	})
}

// handleElicitationDismiss implements
// POST /v1/sessions/{id}/elicitations/{elicitationId}/dismiss per
// §15.1.
func (s *Server) handleElicitationDismiss(w http.ResponseWriter, r *http.Request) {
	s.resolveInteraction(w, r, interactionResolution{
		kind:  interactionstore.KindElicitation,
		phase: interactionstore.PhaseDismissed,
	})
}
