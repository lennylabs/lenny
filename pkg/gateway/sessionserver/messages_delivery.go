// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"

	"go.opentelemetry.io/otel/trace"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/messagerouting"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
)

// parseMessageBatch decodes the §15.4 MessageRequest, rejects an empty
// batch and an unknown `delivery` enum value, resolves the §7.2 path-1
// inReplyTo replies directly against the inputwait registry, and projects
// the remaining payloads to the executor.Message text form. It returns the
// decoded request, the projected messages, and the indexes of the payloads
// still to deliver, writing the §15.1 error envelope and returning ok=false
// on a malformed body or invalid enum. spec: §15.4, §7.2 paths 1/7. F-7.2.14.
func (s *Server) parseMessageBatch(w http.ResponseWriter, r *http.Request, row sessionstore.Session) (MessageRequest, []executor.Message, []int, bool) {
	var req MessageRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return MessageRequest{}, nil, nil, false
	}
	if len(req.Messages) == 0 {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"messages must contain at least one entry",
			map[string]any{"field": "messages"})
		return MessageRequest{}, nil, nil, false
	}

	// spec: §15.4 — `delivery` is a closed enum;
	// unknown values reject with 400 INVALID_DELIVERY_VALUE. The
	// check runs before any side effect so the batch is admitted
	// atomically. F-7.2.14.
	for i, m := range req.Messages {
		if !isValidDelivery(m.Delivery) {
			s.writeError(w, http.StatusBadRequest, "INVALID_DELIVERY_VALUE",
				"message delivery envelope contains an unrecognized `delivery` field value",
				map[string]any{"messageIndex": i, "delivery": m.Delivery})
			return MessageRequest{}, nil, nil, false
		}
	}

	// spec: §7.2 — when a payload's `inReplyTo`
	// matches an outstanding `lenny/request_input`, the gateway
	// resolves the blocked tool call directly. No stdin/executor
	// delivery for that payload. Path 1 wins over every other path
	// per §7.2. F-7.2.14.
	deliverIdx := make([]int, 0, len(req.Messages))
	for i, m := range req.Messages {
		if m.InReplyTo != "" && s.inputWaits != nil {
			if err := s.inputWaits.Resolve(row.ID, m.InReplyTo, m.Content.Text()); err == nil {
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
			ID:   m.ID,
			Role: role,
			// §15.4 message-input union projected to its text form for the
			// gateway's text-only delivery path; the full multipart envelope
			// lands when the executor carries MessagePart[] end to end.
			// spec: §15.4 (MessageEnvelope.input).
			Content: m.Content.Text(),
		})
	}
	return req, msgs, deliverIdx, true
}

// deliveryOutcome carries the §7.2 delivery-path result the receipt render
// consumes: the §15.4 delivery status and reason, the queue depth on a
// buffered path, and the executor response parts on the delivered path.
type deliveryOutcome struct {
	status     session.DeliveryStatus
	reason     session.DeliveryReason
	queueDepth int
	out        []executor.MessagePart
}

// deliverMessageBatch selects and runs the §7.2 paths-1-7 delivery path for
// the non-reply messages: the messagerouting classifier picks direct
// delivery (executor.Send plus the §4.8 PostAgentOutput chain, transcript
// write, and §15.1 event publish), inbox/DLQ buffering, or a terminal /
// not-ready rejection. It writes the §15.1 error envelope and returns
// ok=false on an executor failure or a classifier rejection that ends the
// request; otherwise it returns the deliveryOutcome the receipt echoes.
// spec: §7.2 paths 1-7, §4.8, §15.1, §15.2.1, §15.4. F-7.2.5, F-MS4.
func (s *Server) deliverMessageBatch(w http.ResponseWriter, r *http.Request, span trace.Span, row sessionstore.Session, tenantID string, req MessageRequest, msgs []executor.Message, deliverIdx []int) (deliveryOutcome, bool) {
	// spec: §7.2 paths 1-7 — select the delivery path
	// for the non-reply messages. Path 1 (inReplyTo) was resolved
	// above; "the first matching path wins". A synchronous in-process
	// executor is `ready_for_input` by construction, so a `running`
	// target without a concurrent `input_required` takes path 2 (direct
	// delivery); the input_required / recovering targets buffer in the
	// inbox or DLQ. A `delivery: "immediate"` message to a `suspended`,
	// pod-held session takes path 6: the coordinating replica atomically
	// resumes the session (suspended → running via resumeHeldPod) and
	// delivers the message, returning `delivered`, and fails closed to
	// inbox buffering (`queued`) on a resume or delivery failure so the
	// message is never dropped (line 330). The pod-adapter `ready_for_input`
	// signal that distinguishes path 2 from path 5 (runtime-busy), the
	// path-4 in-flight-tool interrupt, the cross-replica `ForwardMessage`,
	// and the concurrent-workspace per-slot inbox are gated on the
	// pod-adapter readiness model and §5.2 concurrent-workspace build-out; a
	// coordinating replica that cannot drive them buffers and returns
	// `queued`, which §7.2 sanctions. F-7.2.5.
	outcome := deliveryOutcome{status: session.DeliveryStatusDelivered}
	if len(msgs) == 0 {
		return outcome, true
	}

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
			// spec: §16.3 / §16 error taxonomy — the executor
			// (the pod) rejected the prompt; UPSTREAM marks it a
			// downstream dependency failure on the `session.prompt`
			// span.
			tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryUpstream))
			s.writeError(w, http.StatusInternalServerError, "EXECUTOR_FAILURE",
				"executor rejected the message batch",
				map[string]any{"reason": err.Error()})
			return deliveryOutcome{}, false
		}
		out, ok := s.recordDeliveredResponse(w, r, tenantID, row, msgs, o)
		if !ok {
			return deliveryOutcome{}, false
		}
		outcome.out = out
		outcome.status = session.DeliveryStatusDelivered

	case messagerouting.ActionResumeAndDeliver:
		// spec: §7.2 path 6 pod-held branch — atomically
		// resume the suspended session and deliver. resumeHeldPod
		// transitions suspended → running reusing any already-bound pod,
		// guarding a still-suspended row inside its store.Update mutator so
		// the check and the write are atomic (every `suspended` row is
		// pod-held while the §6.2 release sweep is unbuilt). Fail closed to
		// inbox buffering (`queued`) on a resume or delivery failure so the
		// message is never dropped (line 330).
		if err := s.resumeHeldPod(r.Context(), tenantID, row.ID); err != nil {
			// Resume did not happen: leave the row suspended and buffer the
			// message. bufferIncomingMessages yields the `queued` receipt
			// the ActionBufferInbox case produces (line 330: the message is
			// not silently dropped).
			//
			// spec: §16.3 error taxonomy — the taxonomy defines only
			// TRANSIENT, PERMANENT, POLICY, and UPSTREAM. A resume fault is
			// either the resumeHeldPod store.Update write failing or its
			// suspended-guard rejecting a lost terminal-transition race;
			// neither contacted the pod/executor (not UPSTREAM) nor was
			// denied by the policy engine (not POLICY). The `queued`
			// fallback preserves the message and the inbox drains on the
			// next ready_for_input, so the fault is recoverable and TRANSIENT
			// is the correct bucket, matching the create-path convention that
			// tags a store-write failure TRANSIENT (create.go persist-failure
			// path).
			tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
			dropped, depth, berr := s.bufferIncomingMessages(r.Context(), row, req.Messages, deliverIdx, bufferTargetInbox, 0)
			if berr != nil {
				outcome.status = session.DeliveryStatusError
				outcome.reason = session.DeliveryReasonInboxUnavailable
				break
			}
			outcome.status = session.DeliveryStatusQueued
			outcome.queueDepth = depth
			if dropped {
				outcome.status = session.DeliveryStatusDropped
				outcome.reason = session.DeliveryReasonInboxOverflow
			}
			break
		}
		// The session is running. Deliver to the runtime; on a delivery
		// failure buffer to the inbox (`queued`) while leaving the session
		// running (its inbox drains on the next ready_for_input) rather than
		// returning a 500, so the message is preserved (line 330).
		o, err := s.executor.Send(r.Context(), row.ID, msgs)
		if err != nil {
			tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryUpstream))
			dropped, depth, berr := s.bufferIncomingMessages(r.Context(), row, req.Messages, deliverIdx, bufferTargetInbox, 0)
			if berr != nil {
				outcome.status = session.DeliveryStatusError
				outcome.reason = session.DeliveryReasonInboxUnavailable
				break
			}
			outcome.status = session.DeliveryStatusQueued
			outcome.queueDepth = depth
			if dropped {
				outcome.status = session.DeliveryStatusDropped
				outcome.reason = session.DeliveryReasonInboxOverflow
			}
			break
		}
		out, ok := s.recordDeliveredResponse(w, r, tenantID, row, msgs, o)
		if !ok {
			return deliveryOutcome{}, false
		}
		outcome.out = out
		outcome.status = session.DeliveryStatusDelivered

	case messagerouting.ActionBufferInbox:
		dropped, depth, berr := s.bufferIncomingMessages(r.Context(), row, req.Messages, deliverIdx, bufferTargetInbox, 0)
		if berr != nil {
			// spec: §15.2.1 (REST/MCP parity), §15.4 (inbox_unavailable
			// receipt) — an inbox-enqueue failure surfaces as a 200
			// `delivery_receipt` with `status:"error"`/`reason:
			// "inbox_unavailable"`, matching the MCP send_message receipt
			// form (mcptools.buildSendMessageReceiptStatusReason). §15.4
			// defines `inbox_unavailable` strictly as a receipt status/
			// reason, and `INBOX_UNAVAILABLE` is in neither the §15.1
			// catalog nor openapi.json, so the prior 503 envelope was the
			// non-conforming side. F-MS4.
			outcome.status = session.DeliveryStatusError
			outcome.reason = session.DeliveryReasonInboxUnavailable
			break
		}
		outcome.status = session.DeliveryStatusQueued
		outcome.queueDepth = depth
		if dropped {
			outcome.status = session.DeliveryStatusDropped
			outcome.reason = session.DeliveryReasonInboxOverflow
		}

	case messagerouting.ActionBufferDLQ:
		dropped, _, berr := s.bufferIncomingMessages(r.Context(), row, req.Messages, deliverIdx, bufferTargetDLQ, 0)
		if berr != nil {
			// spec: §15.2.1 (REST/MCP parity), §15.4 (inbox_unavailable
			// receipt) — a DLQ-enqueue failure surfaces as the same 200
			// error/inbox_unavailable receipt as the inbox path above,
			// keeping the REST and MCP contracts in lockstep. F-MS4.
			outcome.status = session.DeliveryStatusError
			outcome.reason = session.DeliveryReasonInboxUnavailable
			break
		}
		outcome.status = session.DeliveryStatusQueued
		if dropped {
			outcome.status = session.DeliveryStatusDropped
			outcome.reason = session.DeliveryReasonDLQOverflow
		}

	case messagerouting.ActionRejectTerminal:
		// spec: §7.2 dead-letter table terminal row.
		s.writeError(w, http.StatusConflict, "TARGET_TERMINAL",
			"target session is in terminal state "+string(row.State),
			map[string]any{"targetState": string(row.State)})
		return deliveryOutcome{}, false

	case messagerouting.ActionRejectNotReady:
		// spec: §7.2 pre-running external-client row. (The
		// early pre-running guard above already covers this for REST;
		// retained so the classifier's contract holds on every path.)
		s.writeError(w, http.StatusConflict, "TARGET_NOT_READY",
			"session has not yet entered running state; retry after start",
			map[string]any{"currentState": string(row.State)})
		return deliveryOutcome{}, false
	}
	return outcome, true
}

// recordDeliveredResponse records and publishes a delivered executor response:
// it runs the §4.8 PostAgentOutput chain over the output parts, appends the
// inbound messages and text response parts to the §15.1 transcript, publishes
// the `message_delivered` / `response` / `response_degraded` session events,
// and records the §6.3 TTFT signal on the first `response` event. It returns
// the (possibly PostAgentOutput-modified) parts and ok=true on success. A
// PostAgentOutput REJECT writes the §16.7 error envelope to w and returns
// ok=false so the caller ends the request. This is the shared response-recording
// body reused by the direct-delivery (ActionDeliver) and the §7.2 path-6
// resume-and-deliver (ActionResumeAndDeliver) cases so the two do not duplicate
// it. spec: §4.8, §15.1, §28.5.3, §6.3, §7.2 path 6.
func (s *Server) recordDeliveredResponse(w http.ResponseWriter, r *http.Request, tenantID string, row sessionstore.Session, msgs []executor.Message, o executor.Response) ([]executor.MessagePart, bool) {
	out := o.Parts
	// respAnnotations carries the §28.5.3 envelope-level degradation
	// annotations (schema_version_ahead, blob_ref_unresolvable) the
	// executor, as a live consumer, surfaced while ingesting the
	// runtime's response. They are published on the session event stream
	// so an SSE subscriber is informed of potential response
	// incompleteness.
	respAnnotations := o.Annotations

	// §4.8 PostAgentOutput: run the chain over the agent's output
	// parts before delivering the response to the client. A
	// REJECT blocks delivery (and writes the §16.7 audit row); a
	// MODIFY rewrites the parts that are transcribed, published,
	// and returned. spec: §4.8.
	if s.interceptors != nil && len(out) > 0 {
		modified, rejected := s.runPostAgentOutput(r.Context(), w, tenantID, row.ID, out)
		if rejected {
			return nil, false
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
		// spec: §6.3, §16.1 — the first
		// agent-streamed `response` event observed on this session
		// is the §6.3 TTFT signal. recordTTFTOnce LoadOrStores so
		// only the first event per session triggers the histogram.
		s.recordTTFTOnce(row, "response")
	}
	// spec: §28.5.3 — when the gateway forward-read
	// a response part it did not fully understand (a schemaVersion
	// ahead of its known max, or an unresolvable ref), it surfaces
	// the degradation annotation so the subscriber is informed the
	// response may be incomplete rather than silently dropping it.
	if len(respAnnotations) > 0 {
		s.publishEvent(row.TenantID, row.ID, "response_degraded", respAnnotations)
	}
	return out, true
}

// writeDeliveryReceipt renders the §15.4 synchronous delivery_receipt:
// `messageId` defaults to the first inbound message's sender-supplied id
// (gateway-assigned ids carry the `msg_` prefix), the status is the §7.2
// path outcome, and the timestamp/queueDepth are stamped per status. The
// executor response parts ride alongside on the delivered path. spec: §15.4. F-7.2.5, F-7.2.10, F-MS4.
func (s *Server) writeDeliveryReceipt(w http.ResponseWriter, req MessageRequest, outcome deliveryOutcome) {
	// spec: §15.4 — every send_message call returns a
	// synchronous `delivery_receipt`. `messageId` defaults to the
	// first inbound message's sender-supplied id; gateway-assigned
	// ids carry the `msg_` prefix per §15.4. The status is
	// the §7.2 path outcome computed above (delivered / queued /
	// dropped / error). An inbox-enqueue failure carries
	// status:"error"/reason:"inbox_unavailable" per §15.2.1 parity
	// with the MCP receipt rather than a 503 envelope. F-7.2.5,
	// F-7.2.10, F-MS4.
	messageID := ""
	if len(req.Messages) > 0 {
		messageID = req.Messages[0].ID
	}
	if messageID == "" {
		messageID = "msg_" + session.NewID()
	}
	receipt := session.DeliveryReceipt{
		MessageID: messageID,
		Status:    outcome.status,
		Reason:    outcome.reason,
	}
	if outcome.status == session.DeliveryStatusDelivered {
		receipt.DeliveredAt = s.clock()
	}
	if outcome.status == session.DeliveryStatusQueued {
		receipt.QueueDepth = outcome.queueDepth
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(MessageResponse{
		DeliveryReceipt: receipt,
		Output:          outcome.out,
	})
}

// isValidDelivery reports whether v is a §15.4
// `MessageEnvelope.delivery` enum value. The empty string is the
// `absent → "queued"` default per the same table.
func isValidDelivery(v string) bool {
	switch v {
	case "", "queued", "immediate":
		return true
	}
	return false
}
