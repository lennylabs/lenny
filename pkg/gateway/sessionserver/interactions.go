// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
)

// handleToolUseApprove implements
// POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve per §15.1.
func (s *Server) handleToolUseApprove(w http.ResponseWriter, r *http.Request) {
	s.resolveInteraction(w, r, interactionResolution{
		kind:  interactionstore.KindToolUse,
		phase: interactionstore.PhaseApproved,
	})
}

// handleToolUseDeny implements
// POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny per §15.1.
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
// POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond per
// §15.1. Body: {"response": <value>}.
func (s *Server) handleElicitationRespond(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Response any `json:"response"`
	}
	reader := jsonReader(w, r)
	defer reader.Close()
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	s.resolveInteraction(w, r, interactionResolution{
		kind:     interactionstore.KindElicitation,
		phase:    interactionstore.PhaseResponded,
		response: body.Response,
	})
}

// handleElicitationDismiss implements
// POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss per
// §15.1.
func (s *Server) handleElicitationDismiss(w http.ResponseWriter, r *http.Request) {
	s.resolveInteraction(w, r, interactionResolution{
		kind:  interactionstore.KindElicitation,
		phase: interactionstore.PhaseDismissed,
	})
}

// interactionResolution captures the parameters of a resolve call.
type interactionResolution struct {
	kind     interactionstore.Kind
	phase    interactionstore.Phase
	reason   string
	response any
}

// resolveInteraction is the shared body for the four §15.1
// interaction-resolution endpoints. It validates the
// `(session_id, user_id, interaction_id)` authorization triple and
// applies the resolution.
func (s *Server) resolveInteraction(w http.ResponseWriter, r *http.Request, res interactionResolution) {
	if s.interactions == nil {
		s.writeError(w, http.StatusServiceUnavailable, "INTERACTIONS_UNAVAILABLE",
			"gateway has no interaction store configured", nil)
		return
	}
	tenantID := s.resolveTenant(r)
	sessionID := r.PathValue("id")

	// The §15.1 triple is (session_id, user_id, interaction_id). The
	// user_id is the authenticated caller. Resolution against a
	// session whose user differs returns 404 so the existence of
	// another user's interactions never leaks.
	principal, _ := getPrincipal(r)
	userID := principal.Subject

	var interactionID string
	if res.kind == interactionstore.KindToolUse {
		interactionID = r.PathValue("tool_call_id")
	} else {
		interactionID = r.PathValue("elicitation_id")
	}

	out, err := s.interactions.Resolve(r.Context(), tenantID, sessionID, userID, interactionID,
		func(in *interactionstore.Interaction) error {
			if in.Kind != res.kind {
				// A tool_call_id used on an elicitation endpoint (or
				// vice versa) is treated as not found.
				return interactionstore.ErrNotFound
			}
			in.Phase = res.phase
			in.Reason = res.reason
			in.Response = res.response
			return nil
		})
	if err != nil {
		switch {
		case errors.Is(err, interactionstore.ErrNotFound):
			code := "RESOURCE_NOT_FOUND"
			if res.kind == interactionstore.KindElicitation {
				code = "ELICITATION_NOT_FOUND"
			}
			s.writeError(w, http.StatusNotFound, code, "interaction not found", nil)
		case errors.Is(err, interactionstore.ErrAlreadyResolved):
			s.writeError(w, http.StatusConflict, "INTERACTION_ALREADY_RESOLVED",
				"interaction has already been resolved", nil)
		default:
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	// spec: §7.2 lines 124-127 / §11.7 / §16.7 — every state-changing
	// user decision (tool-use approve/deny, elicitation respond/dismiss)
	// writes a §11.7 hash-chained audit row so the post-incident
	// reconstruction can show who approved or denied what. Best-effort:
	// a nil sink or appender error never fails the resolution. F-7.2.8.
	if s.interactionAudit != nil {
		if eventType, ok := interactionResolutionAuditType(res.kind, res.phase); ok {
			s.interactionAudit.EmitInteractionResolution(r.Context(), InteractionResolutionEvent{
				EventType:     eventType,
				TenantID:      tenantID,
				SessionID:     sessionID,
				UserID:        userID,
				InteractionID: interactionID,
				Phase:         string(out.Phase),
				Reason:        res.reason,
				At:            out.ResolvedAt,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         out.ID,
		"phase":      string(out.Phase),
		"resolvedAt": out.ResolvedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	})
}

// interactionResolutionAuditType maps the (interaction kind, target
// phase) tuple to its §11.7 audit event type. Returns ok=false for an
// unrecognized combination so the emission silently skips an
// intermediate / unsupported phase. F-7.2.8.
func interactionResolutionAuditType(kind interactionstore.Kind, phase interactionstore.Phase) (string, bool) {
	switch {
	case kind == interactionstore.KindToolUse && phase == interactionstore.PhaseApproved:
		return auditToolUseApproved, true
	case kind == interactionstore.KindToolUse && phase == interactionstore.PhaseDenied:
		return auditToolUseDenied, true
	case kind == interactionstore.KindElicitation && phase == interactionstore.PhaseResponded:
		return auditElicitationResponded, true
	case kind == interactionstore.KindElicitation && phase == interactionstore.PhaseDismissed:
		return auditElicitationDismissed, true
	default:
		return "", false
	}
}
