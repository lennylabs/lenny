// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/trace"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
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
		msgs = append(msgs, projectMessage(req.Messages[i]))
	}
	return req, msgs, deliverIdx, true
}

// projectMessage projects one §15.4 message payload onto the executor's
// message: the role defaults to `user`, and the §15.4 message-input union is
// projected to its text form for the gateway's text-only delivery path; the
// full multipart envelope lands when the executor carries MessagePart[] end
// to end. The REST message path and the delivery of held messages both use
// it. spec: §15.4 (MessageEnvelope.input).
func projectMessage(m MessagePayload) executor.Message {
	role := m.Role
	if role == "" {
		role = "user"
	}
	return executor.Message{ID: m.ID, Role: role, Content: m.Content.Text()}
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
// the non-reply messages. It holds the session's delivery lock, taken
// honoring the request context, from the classification read through the
// hold or the send, so on this replica no delivery of held messages
// interleaves with it. The classification reads the primary row; a missing
// row answers 404, and a primary error falls back to the entry row the
// precondition gates read. spec: §12.3 (Postgres HA Requirements).
//
// The classifier picks direct delivery (path 2), the inbox (path 3), the DLQ
// (path 7), a resume of a suspended session (path 6), or a terminal or
// not-ready rejection. It writes the §15.1 error envelope and returns
// ok=false on an executor failure or a rejection that ends the request;
// otherwise it returns the deliveryOutcome the receipt echoes.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §7.2 paths
// 1-7, §12.3 (Postgres HA Requirements), §4.8, §15.1, §15.2.1, §15.4.
// F-7.2.5, F-MS4.
func (s *Server) deliverMessageBatch(w http.ResponseWriter, r *http.Request, span trace.Span, row sessionstore.Session, tenantID string, req MessageRequest, msgs []executor.Message, deliverIdx []int) (deliveryOutcome, bool) {
	if len(msgs) == 0 {
		return deliveryOutcome{status: session.DeliveryStatusDelivered}, true
	}
	unlock, err := s.deliveryLocks.Lock(r.Context(), row.ID)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE",
			"the request ended before the session's delivery lock was taken", nil)
		return deliveryOutcome{}, false
	}
	defer unlock()
	cur, ok := s.classificationRow(w, r, row)
	if !ok {
		return deliveryOutcome{}, false
	}
	d := &restDelivery{s: s, w: w, r: r, span: span, tenantID: tenantID, req: req}
	return d.route(cur, msgs, deliverIdx)
}

// classificationRow reads the row a message is classified on from the
// primary, under the delivery lock. A missing row answers 404
// RESOURCE_NOT_FOUND, as loadMessageTarget does; a primary error is logged
// at WARN and the entry row is used. spec: §7.2 (Message delivery routing —
// seven paths, path 6), §12.3 (Postgres HA Requirements).
func (s *Server) classificationRow(w http.ResponseWriter, r *http.Request, entry sessionstore.Session) (sessionstore.Session, bool) {
	cur, err := s.readPrimary(r.Context(), entry.TenantID, entry.ID)
	switch {
	case err == nil:
		return cur, true
	case errors.Is(err, sessionstore.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
		return sessionstore.Session{}, false
	default:
		slog.Warn("sessionserver: classify a message on the entry row; the primary read failed",
			"session_id", entry.ID, "error", err)
		return entry, true
	}
}

// restDelivery carries one REST message batch through the §7.2 paths. Its
// methods run with the session's delivery lock held.
type restDelivery struct {
	s        *Server
	w        http.ResponseWriter
	r        *http.Request
	span     trace.Span
	tenantID string
	req      MessageRequest
	// rerouted records that a sealed refusal already routed the batch once
	// from a primary re-read, so a second refusal takes the held branch.
	rerouted bool
}

// route classifies row and runs the path the classification selects for
// msgs, the payloads deliverIdx indexes.
func (d *restDelivery) route(row sessionstore.Session, msgs []executor.Message, deliverIdx []int) (deliveryOutcome, bool) {
	s := d.s
	inputRequired := row.State == session.StateInputRequired ||
		(s.inputWaits != nil && len(s.inputWaits.PendingForSession(row.ID)) > 0)
	decision := messagerouting.Classify(row.State, inputRequired, d.immediate(deliverIdx), messagerouting.SourceExternal)
	switch decision.Action {
	case messagerouting.ActionDeliver:
		return d.deliverNow(row, msgs, deliverIdx)
	case messagerouting.ActionResumeAndDeliver:
		return d.resumeAndDeliver(row, msgs, deliverIdx)
	case messagerouting.ActionBufferInbox:
		return s.bufferOrRespond(d.r.Context(), d.w, row, d.req, deliverIdx, bufferTargetInbox)
	case messagerouting.ActionBufferDLQ:
		return s.bufferOrRespond(d.r.Context(), d.w, row, d.req, deliverIdx, bufferTargetDLQ)
	case messagerouting.ActionRejectTerminal:
		// spec: §7.2 dead-letter table terminal row.
		s.writeTargetTerminal(d.w, row.State)
		return deliveryOutcome{}, false
	default:
		// spec: §7.2 pre-running external-client row.
		s.writeError(d.w, http.StatusConflict, "TARGET_NOT_READY",
			"session has not yet entered running state; retry after start",
			map[string]any{"currentState": string(row.State)})
		return deliveryOutcome{}, false
	}
}

// immediate reports whether any payload deliverIdx indexes asks for
// `delivery: "immediate"`.
func (d *restDelivery) immediate(deliverIdx []int) bool {
	for _, i := range deliverIdx {
		if d.req.Messages[i].Delivery == "immediate" {
			return true
		}
	}
	return false
}

// deliverNow is §7.2 path 2: the session's held messages are delivered
// first, so a new message never reaches the runtime ahead of a held entry
// this replica can read, and then the batch is sent. An error of either is
// an error of the send and answers EXECUTOR_FAILURE, except the adapter's
// sealed refusal, which routes the batch's undelivered messages again.
// spec: §7.2 (Message delivery routing — seven paths, path 6), §6.2 (Pod
// State Machine)
func (d *restDelivery) deliverNow(row sessionstore.Session, msgs []executor.Message, deliverIdx []int) (deliveryOutcome, bool) {
	s := d.s
	ctx := d.r.Context()
	if err := s.deliverBuffered(ctx, row); err != nil {
		if errors.Is(err, executor.ErrSessionSealed) {
			return d.rerouteSealed(row, msgs, deliverIdx, nil)
		}
		return d.executorFailure(err)
	}
	o, err := s.executor.Send(ctx, row.ID, msgs)
	var sealed *executor.SealedError
	if errors.As(err, &sealed) {
		delivered, ok := d.recordPartial(row, msgs[:sealed.Delivered], sealed.Response)
		if !ok {
			return deliveryOutcome{}, false
		}
		return d.rerouteSealed(row, msgs[sealed.Delivered:], deliverIdx[sealed.Delivered:], delivered)
	}
	if err != nil {
		return d.executorFailure(err)
	}
	out, ok := s.writeDeliveredResponse(ctx, d.w, d.tenantID, row, msgs, o)
	if !ok {
		return deliveryOutcome{}, false
	}
	return deliveryOutcome{status: session.DeliveryStatusDelivered, out: out}, true
}

// recordPartial records the messages a sealed refusal reports delivered,
// with their response, and returns the output parts.
func (d *restDelivery) recordPartial(row sessionstore.Session, delivered []executor.Message, resp executor.Response) ([]executor.MessagePart, bool) {
	if len(delivered) == 0 {
		return nil, true
	}
	return d.s.writeDeliveredResponse(d.r.Context(), d.w, d.tenantID, row, delivered, resp)
}

// rerouteSealed routes the undelivered messages of a batch the adapter
// refused as sealed, once, from the classification of a primary re-read; a
// second refusal, or a re-read that fails, takes the held branch. The
// receipt is the outcome of the undelivered messages, and the output carries
// the parts of the messages delivered before the refusal.
// spec: §7.2 (Message delivery routing — seven paths, path 6), §6.2 (Pod
// State Machine)
func (d *restDelivery) rerouteSealed(row sessionstore.Session, msgs []executor.Message, deliverIdx []int, delivered []executor.MessagePart) (deliveryOutcome, bool) {
	if len(msgs) == 0 {
		return deliveryOutcome{status: session.DeliveryStatusDelivered, out: delivered}, true
	}
	var outcome deliveryOutcome
	var ok bool
	fresh, err := d.s.readPrimary(d.r.Context(), row.TenantID, row.ID)
	if err != nil || d.rerouted {
		outcome, ok = d.heldBranch(row, deliverIdx)
	} else {
		d.rerouted = true
		outcome, ok = d.route(fresh, msgs, deliverIdx)
	}
	if ok {
		outcome.out = append(delivered, outcome.out...)
	}
	return outcome, ok
}

// executorFailure answers a failed send with EXECUTOR_FAILURE.
func (d *restDelivery) executorFailure(err error) (deliveryOutcome, bool) {
	// spec: §16.3 / §16 error taxonomy — the executor (the pod) rejected
	// the prompt; UPSTREAM marks it a downstream dependency failure on the
	// `session.prompt` span.
	tracing.RecordError(d.span, tracing.CategorizeError(err, tracing.CategoryUpstream))
	d.s.writeError(d.w, http.StatusInternalServerError, "EXECUTOR_FAILURE",
		"executor rejected the message batch",
		map[string]any{"reason": err.Error()})
	return deliveryOutcome{}, false
}

// resumeAndDeliver is §7.2 path 6 for a `suspended` target. On the
// coordinating replica a session that holds its pod resumes on that pod, its
// held messages are delivered, and the batch is sent. Every other case holds
// the batch in the DLQ and moves a podless session to resume_pending: a
// session that released its pod, a replica that does not coordinate the
// session, a resume that failed, and a held delivery that failed after the
// resume, where the message waits behind the failed entry.
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (d *restDelivery) resumeAndDeliver(row sessionstore.Session, msgs []executor.Message, deliverIdx []int) (deliveryOutcome, bool) {
	s := d.s
	ctx := d.r.Context()
	if !s.holdsPod(row) || !s.coordinatesSession(ctx, row.TenantID, row.ID) {
		return d.heldBranch(row, deliverIdx)
	}
	updated, err := s.resumeHeldAndDrain(ctx, row)
	if err != nil {
		tracing.RecordError(d.span, tracing.CategorizeError(err, tracing.CategoryTransient))
		return d.heldBranch(row, deliverIdx)
	}
	o, err := s.executor.Send(ctx, updated.ID, msgs)
	if err != nil {
		tracing.RecordError(d.span, tracing.CategorizeError(err, tracing.CategoryUpstream))
		return d.bufferAfterResumedSendFailure(row, deliverIdx)
	}
	out, ok := s.writeDeliveredResponse(ctx, d.w, d.tenantID, updated, msgs, o)
	if !ok {
		return deliveryOutcome{}, false
	}
	return deliveryOutcome{status: session.DeliveryStatusDelivered, out: out}, true
}

// heldBranch holds the batch in the session's DLQ through holdMessage and,
// on a queued or dropped answer, attempts the guarded suspended →
// resume_pending write, which applies only to a session that holds no pod.
// A guard miss or a logged store error does not change the answer: queued,
// dropped with dlq_overflow, TARGET_TERMINAL, or error with
// inbox_unavailable.
// spec: §7.2 (Message delivery routing — seven paths, path 6), §6.2 (Pod
// State Machine)
func (d *restDelivery) heldBranch(row sessionstore.Session, deliverIdx []int) (deliveryOutcome, bool) {
	outcome, terminal := d.s.holdOutcomeFor(d.r.Context(), row, d.req, deliverIdx)
	if terminal != "" {
		d.s.writeTargetTerminal(d.w, terminal)
		return deliveryOutcome{}, false
	}
	if outcome.status == session.DeliveryStatusQueued || outcome.status == session.DeliveryStatusDropped {
		d.s.resumeHeldSuspended(d.r.Context(), row)
	}
	return outcome, true
}

// resumeHeldSuspended attempts the guarded suspended → resume_pending write
// for a message held for the session, logging a store error. A guard miss
// is the expected outcome for a session that holds its pod.
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) resumeHeldSuspended(ctx context.Context, row sessionstore.Session) {
	row.State = session.StateSuspended
	if _, err := s.enterResumePending(ctx, row); err != nil {
		var pe *session.PreconditionError
		if !errors.As(err, &pe) {
			slog.Warn("sessionserver: resume a suspended session with a held message", "session_id", row.ID, "error", err)
		}
	}
}

// bufferAfterResumedSendFailure handles every error of the resumed batch's
// send. A stream failure reports the session before the failed Send returns,
// so the row is re-read from the primary: a terminal row answers
// TARGET_TERMINAL and enqueues nothing, a running or input_required row
// buffers the batch to the inbox, and any other row, or a re-read error,
// takes the held branch. The inbox is not a valid destination when the row
// cannot rule out a recovering state.
// spec: §7.2 (Message delivery routing — seven paths, paths 6 and 7);
// §28.5.1 (CH-ATTACH Degradation.).
func (d *restDelivery) bufferAfterResumedSendFailure(row sessionstore.Session, deliverIdx []int) (deliveryOutcome, bool) {
	s := d.s
	fresh, err := s.readPrimary(d.r.Context(), row.TenantID, row.ID)
	switch {
	case err != nil:
		return d.heldBranch(row, deliverIdx)
	case session.IsTerminal(fresh.State):
		s.writeTargetTerminal(d.w, fresh.State)
		return deliveryOutcome{}, false
	case fresh.State == session.StateRunning || fresh.State == session.StateInputRequired:
		return s.bufferOrRespond(d.r.Context(), d.w, fresh, d.req, deliverIdx, bufferTargetInbox)
	default:
		return d.heldBranch(fresh, deliverIdx)
	}
}

// bufferOrRespond buffers the batch and returns its receipt outcome, or
// writes TARGET_TERMINAL and returns ok=false when a DLQ hold found the
// session terminal. spec: §7.2 (Message delivery routing — seven paths, path
// 6).
func (s *Server) bufferOrRespond(ctx context.Context, w http.ResponseWriter, row sessionstore.Session, req MessageRequest,
	deliverIdx []int, target bufferTarget,
) (deliveryOutcome, bool) {
	outcome, terminal := s.bufferOutcome(ctx, row, req, deliverIdx, target)
	if terminal != "" {
		s.writeTargetTerminal(w, terminal)
		return deliveryOutcome{}, false
	}
	return outcome, true
}

// bufferOutcome buffers the batch's delivered-path messages to target and
// returns the receipt outcome: queued, dropped on overflow, or
// error/inbox_unavailable when the enqueue fails. An enqueue failure is a 200
// delivery_receipt rather than an error envelope, matching the MCP
// send_message receipt, because §15.4 defines inbox_unavailable only as a
// receipt status and reason. The DLQ receipt carries no queue depth. A DLQ
// hold that found the session terminal returns the terminal state, for the
// caller's TARGET_TERMINAL answer.
// spec: §7.2; §15.2.1 (REST/MCP parity); §15.4 (delivery_receipt). F-MS4.
func (s *Server) bufferOutcome(ctx context.Context, row sessionstore.Session, req MessageRequest,
	deliverIdx []int, target bufferTarget,
) (deliveryOutcome, session.State) {
	if target == bufferTargetDLQ {
		return s.holdOutcomeFor(ctx, row, req, deliverIdx)
	}
	dropped, depth, err := s.bufferIncomingMessages(ctx, row, req.Messages, deliverIdx)
	if err != nil {
		return deliveryOutcome{status: session.DeliveryStatusError, reason: session.DeliveryReasonInboxUnavailable}, ""
	}
	if dropped {
		return deliveryOutcome{status: session.DeliveryStatusDropped, reason: session.DeliveryReasonInboxOverflow, queueDepth: depth}, ""
	}
	return deliveryOutcome{status: session.DeliveryStatusQueued, queueDepth: depth}, ""
}

// holdOutcomeFor holds the batch in the session's DLQ through holdMessage
// and maps its outcome to the receipt. spec: §7.2 (Message delivery routing
// — seven paths, path 6).
func (s *Server) holdOutcomeFor(ctx context.Context, row sessionstore.Session, req MessageRequest, deliverIdx []int) (deliveryOutcome, session.State) {
	msgs, err := s.inboxMessages(req.Messages, deliverIdx)
	if err != nil {
		return deliveryOutcome{status: session.DeliveryStatusError, reason: session.DeliveryReasonInboxUnavailable}, ""
	}
	if s.messaging == nil {
		return deliveryOutcome{status: session.DeliveryStatusError, reason: session.DeliveryReasonInboxUnavailable}, ""
	}
	outcome, state, err := s.holdMessage(ctx, row.TenantID, row.ID, msgs, 0)
	switch {
	case err != nil:
		return deliveryOutcome{status: session.DeliveryStatusError, reason: session.DeliveryReasonInboxUnavailable}, ""
	case outcome == HoldTerminal:
		return deliveryOutcome{}, state
	case outcome == HoldDropped:
		return deliveryOutcome{status: session.DeliveryStatusDropped, reason: session.DeliveryReasonDLQOverflow}, ""
	default:
		return deliveryOutcome{status: session.DeliveryStatusQueued}, ""
	}
}

// writeTargetTerminal answers a message to a terminal session with the §7.2
// dead-letter table's TARGET_TERMINAL error.
func (s *Server) writeTargetTerminal(w http.ResponseWriter, state session.State) {
	s.writeError(w, http.StatusConflict, "TARGET_TERMINAL",
		"target session is in terminal state "+string(state),
		map[string]any{"targetState": string(state)})
}

// writeDeliveredResponse records a delivered executor response for the REST
// message path and, on a PostAgentOutput REJECT, writes the §16.7 error
// envelope (403, or 500 when the audit row could not be recorded) and returns
// ok=false so the caller ends the request.
// spec: §4.8, §15.1, §7.2 path 6.
func (s *Server) writeDeliveredResponse(ctx context.Context, w http.ResponseWriter, tenantID string, row sessionstore.Session, msgs []executor.Message, o executor.Response) ([]executor.MessagePart, bool) {
	out, rejected, auditErr := s.recordDeliveredResponse(ctx, tenantID, row, msgs, o)
	if rejected != nil {
		s.writeRouteRejection(w, interceptor.PhasePostAgentOutput, *rejected, auditErr)
		return nil, false
	}
	return out, true
}

// recordDeliveredResponse records and publishes a delivered executor
// response: it runs the §4.8 PostAgentOutput chain over the output parts,
// appends the inbound messages and text response parts to the §15.1
// transcript, publishes the `message_delivered` / `response` /
// `response_degraded` session events, and records the §6.3 TTFT signal on the
// first `response` event. It returns the (possibly PostAgentOutput-modified)
// parts. On a REJECT it records the §16.7 audit row and returns the chain's
// result and the audit-record error, and transcribes and publishes nothing.
// It writes no response, so the REST paths and the delivery of held messages
// share it. spec: §4.8, §15.1, §28.5.3, §6.3, §7.2 path 6.
func (s *Server) recordDeliveredResponse(ctx context.Context, tenantID string, row sessionstore.Session, msgs []executor.Message, o executor.Response) ([]executor.MessagePart, *interceptor.Result, error) {
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
		modified, rejected, auditErr := s.runPostAgentOutput(ctx, tenantID, row.ID, out)
		if rejected != nil {
			return nil, rejected, auditErr
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
		_ = s.transcripts.Append(ctx, tenantID, row.ID, entries...)
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
	return out, nil, nil
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
