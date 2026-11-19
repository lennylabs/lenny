// SPDX-License-Identifier: MIT

package platformtools

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/interactionstore"
)

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
func (h *Handler) resolveInteraction(w http.ResponseWriter, r *http.Request, res interactionResolution) {
	if h.interactions == nil {
		h.writeError(w, http.StatusServiceUnavailable, "INTERACTIONS_UNAVAILABLE",
			"gateway has no interaction store configured", nil)
		return
	}
	// spec: §11.4 — hard_disable rejects pending delegation approvals
	// for the user. A tool-use approval (the gateway-issued approval
	// when lenny/delegate_task runs under a §11.2 delegation policy) and
	// the elicitation responses are the in-code form of those pending
	// approvals; a disabled or revoked user may not resolve them.
	if !h.admitActiveCaller(w, r, "cannot resolve interactions") {
		return
	}
	tenantID := h.identity.RequestTenant(r)
	sessionID := r.PathValue("id")

	// The §15.1 triple is (session_id, user_id, interaction_id). The
	// user_id is the authenticated caller. Resolution against a
	// session whose user differs returns 404 so the existence of
	// another user's interactions never leaks.
	caller, _ := h.callers.RequestCaller(r.Context())
	userID := caller.Subject

	// spec: §15.1 path-parameter casing — camelCase route templates.
	var interactionID string
	if res.kind == interactionstore.KindToolUse {
		interactionID = r.PathValue("toolCallId")
	} else {
		interactionID = r.PathValue("elicitationId")
	}

	out, err := h.interactions.Resolve(r.Context(), tenantID, sessionID, userID, interactionID,
		func(in *interactionstore.Interaction) error {
			if in.Kind != res.kind {
				// A toolCallId used on an elicitation endpoint (or
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
			h.writeError(w, http.StatusNotFound, code, "interaction not found", nil)
		case errors.Is(err, interactionstore.ErrAlreadyResolved):
			h.writeError(w, http.StatusConflict, "INTERACTION_ALREADY_RESOLVED",
				"interaction has already been resolved", nil)
		default:
			h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	// spec: §7.2 — a tool-use approve/deny must unblock the
	// runtime's blocked tool call, not merely flip the interaction phase.
	// Deliver the verdict onto the shared approval-waiter registry so the
	// PodExecutor read blocked on this tool_call wakes and relays the
	// result to the runtime (approve → forward the call; deny → a
	// tool_result error). A missing waiter (no executor on this replica is
	// blocked, or the non-pod posture) is ignored: the phase update above
	// is the authoritative record. F-7.2.18.
	if res.kind == interactionstore.KindToolUse && h.approvals != nil {
		_ = h.approvals.ResolveApproval(sessionID, interactionID, seam.ApprovalDecision{
			Approved: res.phase == interactionstore.PhaseApproved,
			Reason:   res.reason,
		})
	}
	// spec: §7.2 / §11.7 / §16.7 — every state-changing
	// user decision (tool-use approve/deny, elicitation respond/dismiss)
	// writes a §11.7 hash-chained audit row so the post-incident
	// reconstruction can show who approved or denied what. Best-effort:
	// a nil sink or appender error never fails the resolution. F-7.2.8.
	if h.audit != nil {
		if eventType, ok := interactionResolutionAuditType(res.kind, res.phase); ok {
			h.audit.EmitInteractionResolution(r.Context(), InteractionResolutionEvent{
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

// admitActiveCaller applies the user lifecycle check through
// GWSEAM-AdmitActiveCaller and writes its refusal: 403 with the refusal's
// code, message, and details, or 500 INTERNAL_ERROR when the user cannot
// be read. It returns true when the caller may act.
//
// spec: GWSEAM-AdmitActiveCaller
func (h *Handler) admitActiveCaller(w http.ResponseWriter, r *http.Request, action string) bool {
	err := h.active.AdmitActiveCaller(r.Context(), action)
	if err == nil {
		return true
	}
	if ref, ok := seam.IsRefusal(err); ok {
		h.writeError(w, http.StatusForbidden, ref.Code, ref.Message, ref.Details)
		return false
	}
	h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	return false
}
