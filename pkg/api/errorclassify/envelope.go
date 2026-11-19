// SPDX-License-Identifier: MIT

package errorclassify

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// envelope is the client API error response body.
type envelope struct {
	Error envelopeBody `json:"error"`
}

type envelopeBody struct {
	Code      string         `json:"code"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

// WriteError writes the client API error envelope for code with the given
// HTTP status, message, and details. The category and retryable fields come
// from ClassifyStatus, so the REST surfaces of every gateway part and the
// MCP tools report the same values for the same code, and an unmapped
// non-5xx code classifies as permanent and not retryable.
//
// spec: §15.2.1 (REST/MCP Consistency Contract)
func WriteError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	cat, retryable := ClassifyStatus(code, status)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Error: envelopeBody{
		Code:      code,
		Category:  string(cat),
		Message:   message,
		Retryable: retryable,
		Details:   details,
	}})
}

// WritePreconditionError writes the envelope for a session precondition
// refusal, with the session's current state and the states the endpoint
// admits in its details. An error that is not a *session.PreconditionError
// is written as INTERNAL_ERROR.
func WritePreconditionError(w http.ResponseWriter, err error) {
	var pe *session.PreconditionError
	if !errors.As(err, &pe) {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	allowed := make([]string, 0, len(pe.AllowedStates))
	for _, st := range pe.AllowedStates {
		allowed = append(allowed, string(st))
	}
	WriteError(w, pe.Code(), pe.ErrorCode(), pe.Error(), map[string]any{
		"currentState":  string(pe.CurrentState),
		"allowedStates": allowed,
	})
}
