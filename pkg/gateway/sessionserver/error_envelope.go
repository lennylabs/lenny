// SPDX-License-Identifier: MIT

package sessionserver

import (
	"net/http"

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
	writeErrorEnvelope(w, status, code, message, details)
}

// writeErrorEnvelope is writeError for a handler that runs outside a
// Server, such as the inter-replica forward listener's chain. The envelope
// writer is errorclassify.WriteError, which every gateway part's REST
// surface shares.
func writeErrorEnvelope(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	errorclassify.WriteError(w, status, code, message, details)
}

// writePreconditionError maps a session.PreconditionError to the
// §15.1 INVALID_STATE_TRANSITION envelope.
func (s *Server) writePreconditionError(w http.ResponseWriter, err error) {
	errorclassify.WritePreconditionError(w, err)
}
