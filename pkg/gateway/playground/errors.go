// SPDX-License-Identifier: MIT

package playground

import (
	"encoding/json"
	"net/http"
)

// errorEnvelope is the canonical gateway error response body
// {"error": {"code", "message", "details"}}. The playground reuses
// it so a playground error is indistinguishable in shape from any
// other gateway error (§15.1).
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// writeError writes the canonical error envelope with the supplied
// HTTP status, code, message, and optional details.
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{
		Code:    code,
		Message: message,
		Details: details,
	}})
}

// writeErrorReason writes the canonical error envelope with a
// details.reason field. The §27.3.1 failure-modes table distinguishes
// several UNAUTHORIZED cases by details.reason
// (playground_session_expired, user_invalidated, bearer_revoked) so
// the client can decide whether to re-exchange, redirect to login, or
// surface the error.
func writeErrorReason(w http.ResponseWriter, status int, code, message, reason string) {
	writeError(w, status, code, message, map[string]any{"reason": reason})
}
