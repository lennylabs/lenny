// SPDX-License-Identifier: MIT

package playground

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
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

// writeSigningUnavailable writes the §10.2 KMS_SIGNING_UNAVAILABLE
// envelope (HTTP 503) for a mint whose signature could not be obtained.
// `Retry-After` comes from the breaker through err: the remaining
// cooldown when the breaker is open, and 1 second otherwise, including
// a nil err for an unconfigured signer.
//
// spec: §10.2 (KMS signing failure mode); §15.1 (KMS_SIGNING_UNAVAILABLE).
func writeSigningUnavailable(w http.ResponseWriter, err error, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(jwt.SigningRetryAfterSeconds(err)))
	writeError(w, http.StatusServiceUnavailable, "KMS_SIGNING_UNAVAILABLE", message, nil)
}
