// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
)

// transcriptSortField is the only sort key valid on the §15.1
// transcript: entries are written in monotonic `seq` order and that is
// the only meaningful ordering. spec: §15.1 lines 1228, 1236.
const transcriptSortField = "seq"

// transcriptDefaultSort sorts by per-session monotonic sequence in
// ascending order so the §15.1 transcript reads chronologically.
var transcriptDefaultSort = pagination.Sort{Field: transcriptSortField, Direction: pagination.DirectionAsc}

// handleTranscript implements GET /v1/sessions/{id}/transcript per
// §15.1. Supports the canonical `{items, cursor, hasMore}` envelope
// (spec §15.1 lines 1228-1253) with opaque cursors, [1, 200] limit
// clamping, and 24-hour cursor TTL. The legacy `?afterSeq=` parameter
// is still honoured for backwards-compatible clients that have not
// switched to opaque cursors yet — the cursor is the canonical form.
// Returns 404 RESOURCE_NOT_FOUND when the session does not exist or
// has no recorded transcript.
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

	params, ferr := pagination.ParseRequest(r,
		[]string{transcriptSortField}, transcriptDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}
	afterSeq := uint64(0)
	if params.Cursor.Tiebreak != "" {
		if n, err := strconv.ParseUint(params.Cursor.Tiebreak, 10, 64); err == nil {
			afterSeq = n
		}
	} else if v := r.URL.Query().Get("afterSeq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			afterSeq = n
		}
	}

	// Fetch one extra entry so we can detect whether further pages
	// exist without re-querying the store.
	entries, err := s.transcripts.Page(r.Context(), tenantID, id, afterSeq, params.Limit+1)
	if err != nil && !errors.Is(err, transcriptstore.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if entries == nil {
		entries = []transcriptstore.Entry{}
	}
	hasMore := false
	if len(entries) > params.Limit {
		entries = entries[:params.Limit]
		hasMore = true
	}
	envelope := pagination.Envelope[transcriptstore.Entry]{
		Items:   entries,
		HasMore: hasMore,
	}
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		seqStr := strconv.FormatUint(last.Seq, 10)
		envelope.Cursor = pagination.MintCursor(params.Sort, seqStr, seqStr, s.clock())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)
}

// MessageRequest is the §15.1 POST /v1/sessions/{id}/messages body. It
// carries a §7.2 batch of `MessageEnvelope` payloads; the per-message
// `delivery`, `inReplyTo`, and `slotId` semantics live on the payload
// so a batch can mix immediate and queued messages on the same call.
// spec: §15.4 lines 1672-1721 (`MessageEnvelope`); F-7.2.14.
type MessageRequest struct {
	// Messages is the §7.2 inbound message envelope batch. The
	// gateway evaluates each one's `delivery`/`inReplyTo`/`slotId`
	// fields independently and delivers them in order.
	Messages []MessagePayload `json:"messages"`
}

// MessagePayload is one §15.4 MessageEnvelope on the request batch.
// The wire field names mirror the spec verbatim. spec: §15.4 lines
// 1672-1721.
type MessagePayload struct {
	ID      string `json:"id,omitempty"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content"`

	// InReplyTo, when set, names a pending `lenny/request_input`
	// request the gateway resolves directly (§7.2 path 1) instead of
	// delivering the message to the executor. An inReplyTo that does
	// not match a pending request falls through to executor delivery.
	// spec: §7.2 line 317; §15.4 line 1786.
	InReplyTo string `json:"inReplyTo,omitempty"`

	// Delivery is the §15.4 line 1715 closed enum controlling
	// interrupt behaviour. Valid values: `queued` (default) or
	// `immediate`. Any other value is rejected with
	// `400 INVALID_DELIVERY_VALUE`. The minimal gateway returns the
	// response synchronously regardless of the flag; the
	// interrupt-and-deliver / resume-and-deliver paths land with the
	// §7.2 inbox + DLQ machinery (F-7.2.4).
	// spec: §15.4 lines 1715-1723.
	Delivery string `json:"delivery,omitempty"`

	// SlotID is the §5.2 concurrent-workspace slot identifier. Pods
	// in session-mode or task-mode never see it; concurrent-workspace
	// runtimes route incoming messages by slot. The minimal gateway
	// accepts and forwards the field but does not yet implement the
	// slot-aware routing (tracked under F-5.2 concurrent-workspace
	// build-out). spec: §15.4 line 1713.
	SlotID string `json:"slotId,omitempty"`
}

// MessageResponse is the §15.1 message-injection response. It wraps
// the §15.4 `delivery_receipt` envelope every send_message call
// returns alongside the executor's synchronous output. The minimal
// gateway always emits `status: "delivered"`; the queued / dropped /
// expired / rate_limited / error paths land with the §7.2 inbox + DLQ
// machinery (F-7.2.4).
//
// spec: §15.4 lines 1725-1737 (`delivery_receipt` schema); §7.2 line
// 345; F-7.2.10.
type MessageResponse struct {
	// DeliveryReceipt is the §15.4 envelope clients consume to
	// distinguish delivered from queued / dropped / expired /
	// rate_limited / error outcomes.
	DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`

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
//  3. Applies the §7.2 line 339 pre-running rejection: an external
//     client (REST) call against a `created` / `finalizing` / `ready`
//     / `starting` session is rejected with `409 TARGET_NOT_READY`
//     (F-7.2.15). Inter-session messages from `lenny/send_message`
//     buffer in the DLQ per the same table — that path is not REST.
//  4. Validates each payload's `delivery` value against the §15.4
//     closed enum (`queued` | `immediate`); rejects unknown values
//     with `400 INVALID_DELIVERY_VALUE`.
//  5. Routes §7.2 path 1: a payload whose `inReplyTo` matches a
//     pending `lenny/request_input` is resolved directly against the
//     shared inputwait registry instead of being delivered to the
//     executor (F-7.2.14).
//  6. Routes remaining payloads to the configured executor.
//  7. Returns a synchronous delivery receipt with the executor's
//     response output parts.
//
// The minimal gateway elides:
//   - the §7.2 inter-replica `ForwardMessage` gRPC,
//   - the §7.2 inbox + DLQ persistence (F-7.2.4),
//   - the §7.2 delivery: immediate atomic resume-and-deliver path,
//   - cross-replica coordinator routing,
//   - per-slot routing for concurrent-workspace pods (the SlotID is
//     accepted on the wire and surfaced in the publish payload, but
//     dispatch is deferred until concurrent-workspace mode lands).
//
// Production wires these as the gateway moves from in-memory to
// Redis + Postgres backings.
//
// spec: §7.2 paths 1-7 (lines 313-331); §7.2 line 339 (Pre-running);
// §15.4 lines 1715-1723 (`delivery` enum); §15.4 lines 1725-1737
// (`delivery_receipt`). F-7.2.14, F-7.2.15.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if s.executor == nil {
		s.writeError(w, http.StatusServiceUnavailable, "EXECUTOR_UNAVAILABLE",
			"gateway has no executor wired", nil)
		return
	}
	// spec: §16.3 line 342 — open the gateway-side `session.prompt` span on
	// the request context so the send_message delivery (executor.Send, the
	// §4.8 PostAgentOutput chain, transcript + event publish) rides one
	// trace. The pod-side `session.prompt` span stitches under it via the
	// inherited trace context. Correlation attributes are projected by Start.
	ctx, span := tracing.NewTracer(nil).Start(r.Context(), tracing.SpanSessionPrompt)
	defer span.End()
	r = r.WithContext(ctx)

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

	// spec: §7.2 line 339 (Pre-running row) — external-client REST
	// calls against `created` / `finalizing` / `ready` / `starting`
	// MUST reject with TARGET_NOT_READY so the client retries after
	// starting the session. Inter-session `lenny/send_message`
	// buffers in the DLQ instead — that path is the MCP tool, not
	// this handler. F-7.2.15.
	if isPreRunningState(row.State) {
		s.writeError(w, http.StatusConflict, "TARGET_NOT_READY",
			"session has not yet entered running state; retry after start",
			map[string]any{"currentState": string(row.State)})
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
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if len(req.Messages) == 0 {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"messages must contain at least one entry",
			map[string]any{"field": "messages"})
		return
	}

	// spec: §15.4 lines 1715-1723 — `delivery` is a closed enum;
	// unknown values reject with 400 INVALID_DELIVERY_VALUE. The
	// check runs before any side effect so the batch is admitted
	// atomically. F-7.2.14.
	for i, m := range req.Messages {
		if !isValidDelivery(m.Delivery) {
			s.writeError(w, http.StatusBadRequest, "INVALID_DELIVERY_VALUE",
				"message delivery envelope contains an unrecognized `delivery` field value",
				map[string]any{"messageIndex": i, "delivery": m.Delivery})
			return
		}
	}

	// spec: §7.2 line 317 (path 1) — when a payload's `inReplyTo`
	// matches an outstanding `lenny/request_input`, the gateway
	// resolves the blocked tool call directly. No stdin/executor
	// delivery for that payload. Path 1 wins over every other path
	// per §7.2 line 313 "the first matching path wins". F-7.2.14.
	deliverIdx := make([]int, 0, len(req.Messages))
	resolvedReplies := 0
	for i, m := range req.Messages {
		if m.InReplyTo != "" && s.inputWaits != nil {
			if err := s.inputWaits.Resolve(row.ID, m.InReplyTo, m.Content); err == nil {
				resolvedReplies++
				continue
			}
			// A non-matching inReplyTo falls through to normal
			// delivery — it is then an ordinary threaded message
			// (mirroring mcptools.go's lenny/send_message behaviour).
		}
		deliverIdx = append(deliverIdx, i)
	}

	msgs := make([]executor.Message, 0, len(deliverIdx))
	for _, i := range deliverIdx {
		m := req.Messages[i]
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

	var out []executor.OutputPart
	if len(msgs) > 0 {
		o, err := s.executor.Send(r.Context(), row.ID, msgs)
		if err != nil {
			// spec: §16.3 line 342 / §16 error taxonomy — the executor (the
			// pod) rejected the prompt; UPSTREAM marks it a downstream
			// dependency failure on the `session.prompt` span.
			tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryUpstream))
			s.writeError(w, http.StatusInternalServerError, "EXECUTOR_FAILURE",
				"executor rejected the message batch",
				map[string]any{"reason": err.Error()})
			return
		}
		out = o
	}

	// §4.8 PostAgentOutput: run the chain over the agent's output parts
	// before delivering the response to the client. A REJECT blocks
	// delivery (and writes the §16.7 audit row); a MODIFY rewrites the
	// parts that are transcribed, published, and returned. spec: §4.8
	// line 1054.
	if s.interceptors != nil && len(out) > 0 {
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
		s.publishEvent(row.TenantID, row.ID, "message_delivered", map[string]any{
			"role": m.Role, "content": m.Content,
		})
	}
	for _, p := range out {
		s.publishEvent(row.TenantID, row.ID, "response", map[string]any{
			"type": p.Type, "text": p.Text, "ref": p.Ref,
		})
		// spec: §6.3 line 356, §16.1 line 15 — the first agent-streamed
		// `response` event observed on this session is the §6.3 TTFT
		// signal. recordTTFTOnce LoadOrStores so only the first event
		// per session triggers the histogram observation.
		s.recordTTFTOnce(row, "response")
	}

	// spec: §15.4 lines 1725-1737 — every send_message call returns a
	// synchronous `delivery_receipt`. `messageId` defaults to the
	// first inbound message's sender-supplied id; gateway-assigned
	// ids carry the `msg_` prefix per §15.4 line 1784. The minimal
	// gateway emits `status: "delivered"` after the executor returns;
	// the queued / dropped / expired / rate_limited / error paths
	// land with the §7.2 inbox + DLQ machinery (F-7.2.4). F-7.2.10.
	messageID := ""
	if len(req.Messages) > 0 {
		messageID = req.Messages[0].ID
	}
	if messageID == "" {
		messageID = "msg_" + session.NewID()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(MessageResponse{
		DeliveryReceipt: session.DeliveryReceipt{
			MessageID:   messageID,
			Status:      session.DeliveryStatusDelivered,
			DeliveredAt: s.clock(),
		},
		Output: out,
	})
}

// isValidDelivery reports whether v is a §15.4 line 1715
// `MessageEnvelope.delivery` enum value. The empty string is the
// `absent → "queued"` default per the same table.
func isValidDelivery(v string) bool {
	switch v {
	case "", "queued", "immediate":
		return true
	}
	return false
}

// isPreRunningState reports whether s names a pre-running session
// state per §7.2 line 339 (the pre-running row in the routing-by-
// target-state table). External-client REST calls against any of
// these states reject with TARGET_NOT_READY. F-7.2.15.
func isPreRunningState(s session.State) bool {
	switch s {
	case session.StateCreated, session.StateFinalizing, session.StateReady, session.StateStarting:
		return true
	}
	return false
}
