// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

// TranscriptResponse is the §15.1 GET /v1/sessions/{id}/transcript
// envelope.
type TranscriptResponse struct {
	SessionID string                  `json:"sessionId"`
	Entries   []transcriptstore.Entry `json:"entries"`
}

// handleTranscript implements GET /v1/sessions/{id}/transcript per
// §15.1. Supports ?afterSeq= and ?limit= pagination. Returns
// 404 RESOURCE_NOT_FOUND when the session does not exist or has no
// recorded transcript.
func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	// The session must exist (and belong to the tenant) before we
	// surface a transcript — a transcript for a foreign session must
	// not leak.
	if _, err := s.store.Get(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if s.transcripts == nil {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"session has no recorded transcript", nil)
		return
	}

	afterSeq := uint64(0)
	if v := r.URL.Query().Get("afterSeq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			afterSeq = n
		}
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	entries, err := s.transcripts.Page(r.Context(), tenantID, id, afterSeq, limit)
	if err != nil {
		if errors.Is(err, transcriptstore.ErrNotFound) {
			// Session exists but has no transcript yet — return an
			// empty list rather than 404, so a freshly created
			// session is distinguishable from a missing one.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(TranscriptResponse{
				SessionID: id,
				Entries:   []transcriptstore.Entry{},
			})
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TranscriptResponse{SessionID: id, Entries: entries})
}

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

	// §5.1 / §15.1: reject mid-session injection when the session's
	// runtime declares capabilities.injection.supported: false. Per
	// §5.1 injection support defaults to false. The runtime is resolved
	// to its effective definition, so a derived runtime is checked
	// against the injection support it inherits from its base. The
	// check degrades safely when the runtime registry is not wired or
	// the runtime is not found, so a gateway without a wired registry
	// does not block injection.
	if s.runtimes != nil && row.RuntimeRef != "" {
		if rt, err := runtimestore.Resolve(r.Context(), s.runtimes, row.RuntimeRef); err == nil && !rt.InjectionSupported() {
			s.writeError(w, http.StatusForbidden, "INJECTION_REJECTED",
				"runtime does not support mid-session message injection", nil)
			return
		}
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

	// §4.8 PostAgentOutput: run the chain over the agent's output parts
	// before delivering the response to the client. A REJECT blocks
	// delivery (and writes the §16.7 audit row); a MODIFY rewrites the
	// parts that are transcribed, published, and returned. spec: §4.8
	// line 1054.
	if s.interceptors != nil {
		modified, rejected := s.runPostAgentOutput(r.Context(), w, tenantID, row.ID, out)
		if rejected {
			return
		}
		out = modified
	}

	// Record the §15.1 transcript: inbound messages followed by the
	// runtime's text response parts. Best-effort — a transcript
	// write failure does not fail the message delivery.
	if s.transcripts != nil {
		entries := make([]transcriptstore.Entry, 0, len(msgs)+len(out))
		now := s.clock()
		for _, m := range msgs {
			entries = append(entries, transcriptstore.Entry{
				Role: m.Role, Content: m.Content, Timestamp: now,
			})
		}
		for _, p := range out {
			if p.Type == "text" {
				entries = append(entries, transcriptstore.Entry{
					Role: "assistant", Content: p.Text, Timestamp: now,
				})
			}
		}
		_ = s.transcripts.Append(r.Context(), tenantID, row.ID, entries...)
	}

	// Publish the §15.1 session events so SSE subscribers observe
	// the message + response live.
	for _, m := range msgs {
		s.publishEvent(row.ID, "message_delivered", map[string]any{
			"role": m.Role, "content": m.Content,
		})
	}
	for _, p := range out {
		s.publishEvent(row.ID, "response", map[string]any{
			"type": p.Type, "text": p.Text, "ref": p.Ref,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(MessageResponse{
		DeliveryStatus: "delivered",
		Output:         out,
	})
}
