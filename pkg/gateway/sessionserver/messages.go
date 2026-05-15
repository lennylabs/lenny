// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// MessageRequest is the §15.1 POST /v1/sessions/{id}/messages body.
type MessageRequest struct {
	// Messages is the §7.2 inbound message envelope batch. The
	// gateway delivers them in order to the executor.
	Messages []MessagePayload `json:"messages"`

	// Delivery controls the §7.2 delivery semantics. Valid values
	// are `immediate` (interrupt-on-suspend) and the default
	// `queued`. The minimal gateway returns the response
	// synchronously regardless of this flag.
	Delivery string `json:"delivery,omitempty"`
}

// MessagePayload is one message in the batch.
type MessagePayload struct {
	ID      string `json:"id,omitempty"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content"`
}

// MessageResponse is the §15.1 message-injection response. The
// minimal gateway returns the synchronous output of the in-process
// executor; production wires the §10.1 inbox path so the response
// streams via the event channel.
type MessageResponse struct {
	// DeliveryStatus echoes the §7.2 delivery-receipt status. The
	// minimal executor always returns `delivered`.
	DeliveryStatus string `json:"deliveryStatus"`

	// Output is the executor's synchronous response. Empty when the
	// executor delivered the message but produced no immediate
	// output (e.g., the runtime is awaiting an upstream LLM call).
	Output []executor.OutputPart `json:"output,omitempty"`
}

// handleMessages implements POST /v1/sessions/{id}/messages.
//
// The handler:
//
//  1. Looks up the session row.
//  2. Validates the §15.1 precondition: any non-terminal state.
//  3. Routes the message batch to the configured executor.
//  4. Returns a synchronous delivery receipt with the executor's
//     response output parts.
//
// The minimal gateway elides:
//   - the §7.2 inter-replica `ForwardMessage` gRPC,
//   - the §7.2 inbox + DLQ persistence,
//   - the §7.2 delivery: immediate atomic resume-and-deliver path,
//   - cross-replica coordinator routing.
//
// Production wires these as the gateway moves from in-memory to
// Redis + Postgres backings.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if s.executor == nil {
		s.writeError(w, http.StatusServiceUnavailable, "EXECUTOR_UNAVAILABLE",
			"gateway has no executor wired", nil)
		return
	}
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
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointMessages,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	var req MessageRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if len(req.Messages) == 0 {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"messages must contain at least one entry",
			map[string]any{"field": "messages"})
		return
	}

	msgs := make([]executor.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := m.Role
		if role == "" {
			role = "user"
		}
		msgs = append(msgs, executor.Message{
			ID:      m.ID,
			Role:    role,
			Content: m.Content,
		})
	}

	out, err := s.executor.Send(r.Context(), row.ID, msgs)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "EXECUTOR_FAILURE",
			"executor rejected the message batch",
			map[string]any{"reason": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(MessageResponse{
		DeliveryStatus: "delivered",
		Output:         out,
	})
}
