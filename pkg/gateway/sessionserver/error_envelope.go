// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
)

// errorEnvelope is the §15.1 error response shape.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string         `json:"code"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

// writeError writes a §15.1 error envelope. The category and
// retryable fields are populated from the shared §15.2.1
// errorclassify table so REST and MCP report the same values for the
// same code. An unmapped code resolves through the status-aware
// ClassifyStatus, so an unmapped non-5xx code classifies as
// (PERMANENT, false) here exactly as it does on the admin surface
// (ClassifyStatus in tenants.go), rather than the (TRANSIENT, true)
// fallback the code-only Classify returns. spec: §15.2.1
// (classification consistency).
func (s *Server) writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	cat, retryable := errorclassify.ClassifyStatus(code, status)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{
		Code:      code,
		Category:  string(cat),
		Message:   message,
		Retryable: retryable,
		Details:   details,
	}})
}

// writePreconditionError maps a session.PreconditionError to the
// §15.1 INVALID_STATE_TRANSITION envelope.
func (s *Server) writePreconditionError(w http.ResponseWriter, err error) {
	var pe *session.PreconditionError
	if !errors.As(err, &pe) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	allowed := make([]string, 0, len(pe.AllowedStates))
	for _, st := range pe.AllowedStates {
		allowed = append(allowed, string(st))
	}
	s.writeError(w, pe.Code(), pe.ErrorCode(), pe.Error(), map[string]any{
		"currentState":  string(pe.CurrentState),
		"allowedStates": allowed,
	})
}
