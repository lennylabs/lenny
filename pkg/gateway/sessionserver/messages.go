// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/messagerouting"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/sessioninbox"
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
// `delivery` and `inReplyTo` semantics live on the payload so a batch can
// mix immediate and queued messages on the same call.
// spec: §15.4 lines 1672-1721 (`MessageEnvelope`); F-7.2.14.
type MessageRequest struct {
	// Messages is the §7.2 inbound message envelope batch. The
	// gateway evaluates each one's `delivery`/`inReplyTo`
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

	// The §5.2 slotId is deliberately absent from the request body. slotId is
	// adapter-injected (§15.4) and internal: a client addresses a message by
	// session_id (the path parameter on POST /v1/sessions/{id}/messages), and
	// the gateway derives the bound slot from the session, stamping it on the
	// outbound adapter envelope. A client-supplied slotId is silently ignored
	// because the field does not deserialize onto the payload. spec: §7.2
	// (per-slot routing), §15.4.
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
//   - cross-replica coordinator routing.
//
// Per-slot routing for §5.2 concurrent-workspace pods is internal: the
// client never supplies a slotId, the gateway derives the bound slot from
// the session, and the executor stamps the resolved slotId on the outbound
// adapter envelope (see pkg/gateway/executor). A concurrent-session bind
// that resolves no slot fails closed with the §7.2 SLOT_ID_REQUIRED
// invariant rather than misdelivering.
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
	// spec: §15.1 line 818 — message injection against a suspended tenant
	// is rejected with TENANT_SUSPENDED. The session exists (checked
	// above) before the suspension state leaks via this endpoint.
	if !s.requireTenantNotSuspended(w, r, tenantID) {
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
		// §5.1 line 49: overlay the session tenant's capability override so
		// a tenant that disabled injection.supported on this runtime has
		// the injection gate enforce its narrowed value. F-5.1.20.
		if rt, err := runtimecapoverride.ResolveForTenant(r.Context(), s.runtimes, s.capOverrides, row.TenantID, row.RuntimeRef); err == nil && !rt.InjectionSupported() {
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

	// spec: §7.2 paths 1-7 (lines 313-331) — select the delivery path
	// for the non-reply messages. Path 1 (inReplyTo) was resolved
	// above; "the first matching path wins". A synchronous in-process
	// executor is `ready_for_input` by construction, so a `running`
	// target without a concurrent `input_required` takes path 2 (direct
	// delivery); the input_required / suspended / recovering targets
	// buffer in the inbox or DLQ. The pod-adapter `ready_for_input`
	// signal that distinguishes path 2 from path 5 (runtime-busy), the
	// path-4 in-flight-tool interrupt, the path-6 pod-held
	// resume-and-deliver, the cross-replica `ForwardMessage`, and the
	// concurrent-workspace per-slot inbox are gated on the pod-adapter
	// readiness model and §5.2 concurrent-workspace build-out; a
	// coordinating replica that cannot drive them buffers and returns
	// `queued`, which §7.2 sanctions. F-7.2.5.
	deliveryStatus := session.DeliveryStatusDelivered
	var deliveryReason session.DeliveryReason
	queueDepth := 0
	var out []executor.OutputPart
	// respAnnotations carries the §15.4.1 envelope-level degradation
	// annotations (schema_version_ahead, blob_ref_unresolvable) the
	// executor, as a live consumer, surfaced while ingesting the runtime's
	// response. They are published on the session event stream so an SSE
	// subscriber is informed of potential response incompleteness.
	var respAnnotations map[string]any

	if len(msgs) > 0 {
		inputRequired := row.State == session.StateInputRequired ||
			(s.inputWaits != nil && len(s.inputWaits.PendingForSession(row.ID)) > 0)
		immediate := false
		for _, i := range deliverIdx {
			if req.Messages[i].Delivery == "immediate" {
				immediate = true
				break
			}
		}
		decision := messagerouting.Classify(row.State, inputRequired, immediate, messagerouting.SourceExternal)
		switch decision.Action {
		case messagerouting.ActionDeliver:
			o, err := s.executor.Send(r.Context(), row.ID, msgs)
			if err != nil {
				// spec: §16.3 line 342 / §16 error taxonomy — the executor
				// (the pod) rejected the prompt; UPSTREAM marks it a
				// downstream dependency failure on the `session.prompt`
				// span.
				tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryUpstream))
				s.writeError(w, http.StatusInternalServerError, "EXECUTOR_FAILURE",
					"executor rejected the message batch",
					map[string]any{"reason": err.Error()})
				return
			}
			out = o.Parts
			respAnnotations = o.Annotations

			// §4.8 PostAgentOutput: run the chain over the agent's output
			// parts before delivering the response to the client. A
			// REJECT blocks delivery (and writes the §16.7 audit row); a
			// MODIFY rewrites the parts that are transcribed, published,
			// and returned. spec: §4.8 line 1054.
			if s.interceptors != nil && len(out) > 0 {
				modified, rejected := s.runPostAgentOutput(r.Context(), w, tenantID, row.ID, out)
				if rejected {
					return
				}
				out = modified
			}

			// Record the §15.1 transcript: inbound messages followed by
			// the runtime's text response parts. Best-effort — a
			// transcript write failure does not fail the message delivery.
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
				// spec: §6.3 line 356, §16.1 line 15 — the first
				// agent-streamed `response` event observed on this session
				// is the §6.3 TTFT signal. recordTTFTOnce LoadOrStores so
				// only the first event per session triggers the histogram.
				s.recordTTFTOnce(row, "response")
			}
			// spec: §15.4.1 lines 1501, 1577 — when the gateway forward-read
			// a response part it did not fully understand (a schemaVersion
			// ahead of its known max, or an unresolvable ref), it surfaces
			// the degradation annotation so the subscriber is informed the
			// response may be incomplete rather than silently dropping it.
			if len(respAnnotations) > 0 {
				s.publishEvent(row.TenantID, row.ID, "response_degraded", respAnnotations)
			}
			deliveryStatus = session.DeliveryStatusDelivered

		case messagerouting.ActionBufferInbox:
			dropped, depth, berr := s.bufferIncomingMessages(r.Context(), row, req.Messages, deliverIdx, bufferTargetInbox, 0)
			if berr != nil {
				s.writeError(w, http.StatusServiceUnavailable, "INBOX_UNAVAILABLE",
					"session inbox is not available; message not buffered",
					map[string]any{"reason": berr.Error()})
				return
			}
			deliveryStatus = session.DeliveryStatusQueued
			queueDepth = depth
			if dropped {
				deliveryStatus = session.DeliveryStatusDropped
				deliveryReason = session.DeliveryReasonInboxOverflow
			}

		case messagerouting.ActionBufferDLQ:
			dropped, _, berr := s.bufferIncomingMessages(r.Context(), row, req.Messages, deliverIdx, bufferTargetDLQ, 0)
			if berr != nil {
				s.writeError(w, http.StatusServiceUnavailable, "INBOX_UNAVAILABLE",
					"session dead-letter queue is not available; message not buffered",
					map[string]any{"reason": berr.Error()})
				return
			}
			deliveryStatus = session.DeliveryStatusQueued
			if dropped {
				deliveryStatus = session.DeliveryStatusDropped
				deliveryReason = session.DeliveryReasonDLQOverflow
			}

		case messagerouting.ActionRejectTerminal:
			// spec: §7.2 dead-letter table terminal row.
			s.writeError(w, http.StatusConflict, "TARGET_TERMINAL",
				"target session is in terminal state "+string(row.State),
				map[string]any{"targetState": string(row.State)})
			return

		case messagerouting.ActionRejectNotReady:
			// spec: §7.2 line 339 pre-running external-client row. (The
			// early pre-running guard above already covers this for REST;
			// retained so the classifier's contract holds on every path.)
			s.writeError(w, http.StatusConflict, "TARGET_NOT_READY",
				"session has not yet entered running state; retry after start",
				map[string]any{"currentState": string(row.State)})
			return
		}
	}

	// spec: §15.4 lines 1725-1737 — every send_message call returns a
	// synchronous `delivery_receipt`. `messageId` defaults to the
	// first inbound message's sender-supplied id; gateway-assigned
	// ids carry the `msg_` prefix per §15.4 line 1784. The status is
	// the §7.2 path outcome computed above (delivered / queued /
	// dropped). F-7.2.5, F-7.2.10.
	messageID := ""
	if len(req.Messages) > 0 {
		messageID = req.Messages[0].ID
	}
	if messageID == "" {
		messageID = "msg_" + session.NewID()
	}
	receipt := session.DeliveryReceipt{
		MessageID: messageID,
		Status:    deliveryStatus,
		Reason:    deliveryReason,
	}
	if deliveryStatus == session.DeliveryStatusDelivered {
		receipt.DeliveredAt = s.clock()
	}
	if deliveryStatus == session.DeliveryStatusQueued {
		receipt.QueueDepth = queueDepth
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(MessageResponse{
		DeliveryReceipt: receipt,
		Output:          out,
	})
}

// bufferTarget selects the §7.2 destination for a buffered message:
// the session inbox (paths 3/5/6) or the dead-letter queue (path 7).
type bufferTarget int

const (
	bufferTargetInbox bufferTarget = iota
	bufferTargetDLQ
)

// bufferIncomingMessages buffers the selected non-reply messages into
// the session inbox or DLQ per the §7.2 routing decision. It returns
// whether the overflow policy evicted any message (so the caller emits
// a `dropped` receipt), the inbox depth after the enqueue (for the
// `queued` receipt's `queueDepth`), and an error when the messaging
// coordinator is not wired (the caller maps it to inbox_unavailable).
// Each payload is serialized as the §15.4 MessageEnvelope so the
// dequeue path on resume re-delivers the original wire form.
//
// spec: §7.2 lines 319-331 (inbox/DLQ buffering, queued/dropped receipts).
func (s *Server) bufferIncomingMessages(ctx context.Context, row sessionstore.Session, all []MessagePayload, idx []int, target bufferTarget, ttl time.Duration) (dropped bool, depth int, err error) {
	for _, i := range idx {
		m := all[i]
		payload, mErr := json.Marshal(m)
		if mErr != nil {
			return dropped, depth, mErr
		}
		id := m.ID
		if id == "" {
			id = "msg_" + session.NewID()
		}
		msg := sessioninbox.Message{
			MessageID:  id,
			Payload:    payload,
			EnqueuedAt: s.clock(),
		}
		var evicted *sessioninbox.Message
		switch target {
		case bufferTargetInbox:
			evicted, err = s.messaging.EnqueueInbox(ctx, row.TenantID, row.ID, msg)
		case bufferTargetDLQ:
			evicted, err = s.messaging.EnqueueDLQ(ctx, row.TenantID, row.ID, msg, ttl)
		}
		if err != nil {
			return dropped, depth, err
		}
		if evicted != nil {
			dropped = true
		}
	}
	if target == bufferTargetInbox {
		depth, _ = s.messaging.InboxLen(ctx, row.TenantID, row.ID)
	}
	return dropped, depth, nil
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
