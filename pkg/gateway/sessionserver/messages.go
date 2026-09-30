// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// serviceUnavailableRetryAfterSeconds is the Retry-After header the §5.1
// injection gate sets when it fails closed with SERVICE_UNAVAILABLE on a
// transient runtime- or override-store read error. SERVICE_UNAVAILABLE is
// TRANSIENT/503/retryable per the §15.1 catalog; 5 seconds matches the
// store-recovery cool-down the other transient gateway 503s use.
// spec: §15.1 (SERVICE_UNAVAILABLE), §5.1 (injection fail-closed).
const serviceUnavailableRetryAfterSeconds = 5

// Injection-gate fail-closed cause labels for the
// lenny_injection_gate_failclosed_total{cause} metric. They distinguish
// which of the two backing stores the §5.1 injection gate consults
// returned the transient error. spec: §5.1 (injection fail-closed).
const (
	injectionFailClosedCauseOverrideStore = "override_store"
	injectionFailClosedCauseRuntimeStore  = "runtime_store"
)

// injectionFailClosedCause attributes a transient ResolveForTenant error
// to the backing store that produced it. runtimecapoverride wraps an
// override-store read failure with runtimecapoverride.ErrOverrideStore;
// any other transient error came from the runtime registry. The label
// keeps the runtime-store-versus-override-store distinction observable in
// the metric even though the client code stays the coarse
// SERVICE_UNAVAILABLE. spec: §5.1 (injection fail-closed).
func injectionFailClosedCause(err error) string {
	if errors.Is(err, runtimecapoverride.ErrOverrideStore) {
		return injectionFailClosedCauseOverrideStore
	}
	return injectionFailClosedCauseRuntimeStore
}

// MessageRequest is the §15.1 POST /v1/sessions/{id}/messages body. It
// carries a §7.2 batch of `MessageEnvelope` payloads; the per-message
// `delivery` and `inReplyTo` semantics live on the payload so a batch can
// mix immediate and queued messages on the same call.
// spec: §15.4; F-7.2.14.
type MessageRequest struct {
	// Messages is the §7.2 inbound message envelope batch. The
	// gateway evaluates each one's `delivery`/`inReplyTo`
	// fields independently and delivers them in order.
	Messages []MessagePayload `json:"messages"`
}

// MessagePayload is one §15.4 MessageEnvelope on the request batch.
// The wire field names mirror the spec verbatim. spec: §15.4.
type MessagePayload struct {
	ID   string `json:"id,omitempty"`
	Role string `json:"role,omitempty"`

	// Content is the §15.4 `MessageEnvelope.input` union: a bare string
	// or a §28.5.3 `MessagePart[]` array. A bare string is sugar for a
	// single text part. The identical union is accepted by the MCP
	// `lenny/send_message` `message` argument, so the two surfaces are
	// parallel representations of the one §15.4 message-send contract
	// under the §15.2.1 parity rule. spec: §15.4 (MessageEnvelope.input),
	// §15.2.1 (REST/MCP parity).
	Content sessionrecord.MessageContent `json:"content"`

	// InReplyTo, when set, names a pending `lenny/request_input`
	// request the gateway resolves directly (§7.2 path 1) instead of
	// delivering the message to the executor. An inReplyTo that does
	// not match a pending request falls through to executor delivery.
	// spec: §7.2; §15.4.
	InReplyTo string `json:"inReplyTo,omitempty"`

	// Delivery is the §15.4 closed enum controlling
	// interrupt behaviour. Valid values: `queued` (default) or
	// `immediate`. Any other value is rejected with
	// `400 INVALID_DELIVERY_VALUE`. The minimal gateway returns the
	// response synchronously regardless of the flag; the
	// interrupt-and-deliver / resume-and-deliver paths land with the
	// §7.2 inbox + DLQ machinery (F-7.2.4).
	// spec: §15.4.
	Delivery string `json:"delivery,omitempty"`

	// The per-session identifier is deliberately absent from the request
	// body. It is internal and gateway-injected: a client addresses a
	// message by session_id (the path parameter on
	// POST /v1/sessions/{id}/messages), and the gateway stamps that session
	// on the outbound adapter envelope as `sessionId` on every pod,
	// whatever the pool's concurrency. A client-supplied `sessionId` in the
	// body is silently ignored because the field does not deserialize onto
	// the payload. spec: §28.5.3 (every session-scoped frame carries the
	// session), §15.4.
}

// MessageResponse is the §15.1 message-injection response. It wraps
// the §15.4 `delivery_receipt` envelope every send_message call
// returns alongside the executor's synchronous output. The minimal
// gateway always emits `status: "delivered"`; the queued / dropped /
// expired / rate_limited / error paths land with the §7.2 inbox + DLQ
// machinery (F-7.2.4).
//
// spec: §15.4; §7.2; F-7.2.10.
type MessageResponse struct {
	// DeliveryReceipt is the §15.4 envelope clients consume to
	// distinguish delivered from queued / dropped / expired /
	// rate_limited / error outcomes.
	DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`

	// Output is the executor's synchronous response. Empty when the
	// executor delivered the message but produced no immediate
	// output (e.g., the runtime is awaiting an upstream LLM call).
	Output []executor.MessagePart `json:"output,omitempty"`
}

// handleMessages implements POST /v1/sessions/{id}/messages.
//
// The handler:
//
//  1. Looks up the session row.
//  2. Validates the §15.1 precondition: any non-terminal state.
//  3. Applies the §7.2 pre-running rejection: an external
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
//   - cross-replica coordinator routing.
//
// A `delivery: "immediate"` message to a `suspended`, pod-held session
// takes the §7.2 path-6 atomic resume-and-deliver on the coordinating
// replica (deliverMessageBatch): it resumes suspended → running and
// delivers, failing closed to `queued` inbox buffering on failure.
//
// Slot routing is internal: the client supplies no slot address, and the
// session identifier the request already carries names the slot that
// session holds on its pod whatever the pool's concurrency, so the
// executor addresses the outbound adapter envelope with it (see
// pkg/gateway/session/executor). A bind that resolves no session
// identifier fails closed on the §7.2 dispatch invariant rather than
// misdelivering.
//
// Production wires these as the gateway moves from in-memory to
// Redis + Postgres backings.
//
// spec: §7.2 paths 1-7; §7.2;
// §15.4; §15.4
// (`delivery_receipt`). F-7.2.14, F-7.2.15.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if s.executor == nil {
		s.writeError(w, http.StatusServiceUnavailable, "EXECUTOR_UNAVAILABLE",
			"gateway has no executor wired", nil)
		return
	}
	// spec: §16.3 — open the gateway-side `session.prompt` span on
	// the request context so the send_message delivery (executor.Send, the
	// §4.8 PostAgentOutput chain, transcript + event publish) rides one
	// trace. The pod-side `session.prompt` span stitches under it via the
	// inherited trace context. Correlation attributes are projected by Start.
	ctx, span := tracing.NewTracer(nil).Start(r.Context(), tracing.SpanSessionPrompt)
	defer span.End()
	r = r.WithContext(ctx)

	tenantID := s.resolveTenant(r)
	row, ok := s.loadMessageTarget(w, r, tenantID)
	if !ok {
		return
	}

	req, msgs, deliverIdx, ok := s.parseMessageBatch(w, r, row)
	if !ok {
		return
	}

	outcome, ok := s.deliverMessageBatch(w, r, span, row, tenantID, req, msgs, deliverIdx)
	if !ok {
		return
	}

	s.writeDeliveryReceipt(w, req, outcome)
}

// loadMessageTarget runs the §15.1 message-injection precondition gates:
// the session lookup in the active tenant, the §15.1
// tenant-suspend gate, the §15.1 precondition-table check, the §7.2 pre-running TARGET_NOT_READY guard, and the §5.1 fail-closed
// mid-session injection-support gate. It writes the §15.1 error envelope
// and returns ok=false on any rejection. spec: §15.1, §7.2, §5.1.
func (s *Server) loadMessageTarget(w http.ResponseWriter, r *http.Request, tenantID string) (sessionstore.Session, bool) {
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return sessionstore.Session{}, false
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return sessionstore.Session{}, false
	}
	// spec: §15.1 — message injection against a suspended tenant
	// is rejected with TENANT_SUSPENDED. The session exists (checked
	// above) before the suspension state leaks via this endpoint.
	if !s.requireTenantNotSuspended(w, r, tenantID) {
		return sessionstore.Session{}, false
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointMessages,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return sessionstore.Session{}, false
	}

	// spec: §7.2 — external-client REST
	// calls against `created` / `finalizing` / `ready` / `starting`
	// MUST reject with TARGET_NOT_READY so the client retries after
	// starting the session. Inter-session `lenny/send_message`
	// buffers in the DLQ instead — that path is the MCP tool, not
	// this handler. F-7.2.15.
	if isPreRunningState(row.State) {
		s.writeError(w, http.StatusConflict, "TARGET_NOT_READY",
			"session has not yet entered running state; retry after start",
			map[string]any{"currentState": string(row.State)})
		return sessionstore.Session{}, false
	}

	if !s.requireInjectionSupported(w, r, row) {
		return sessionstore.Session{}, false
	}
	return row, true
}

// requireInjectionSupported runs the §5.1 / §15.1 mid-session
// injection-support gate. It rejects with INJECTION_REJECTED when the
// resolved runtime declares capabilities.injection.supported: false, and
// fails closed with a retryable SERVICE_UNAVAILABLE on a transient
// runtime- or override-store read error. The gate degrades open only on a
// definite "no record" answer (unwired registry, empty RuntimeRef, or a
// not-found from either backing store). spec: §5.1 (injection fail-closed),
// §15.1 (SERVICE_UNAVAILABLE).
func (s *Server) requireInjectionSupported(w http.ResponseWriter, r *http.Request, row sessionstore.Session) bool {
	// §5.1 / §15.1: reject mid-session injection when the session's
	// runtime declares capabilities.injection.supported: false. Per
	// §5.1 injection support defaults to false. The runtime is resolved
	// to its effective definition, so a derived runtime is checked
	// against the injection support it inherits from its base.
	//
	// The gate degrades open only on a definite "no record" answer: an
	// unwired registry (s.runtimes == nil), an empty RuntimeRef, or a
	// not-found from either backing store. A transient read error from
	// either the runtime registry or the per-tenant capability-override
	// store is not a definite answer, so the gate fails closed with a
	// retryable SERVICE_UNAVAILABLE rather than admitting injection
	// against an un-overlaid runtime. INJECTION_REJECTED is reserved for
	// the policy denial (POLICY/non-retryable); a transient infra blip
	// would mislabel a retryable condition as a permanent policy block.
	// spec: §5.1 (injection fail-closed).
	if s.runtimes == nil || row.RuntimeRef == "" {
		return true
	}
	// §5.1: overlay the session tenant's capability override so
	// a tenant that disabled injection.supported on this runtime has
	// the injection gate enforce its narrowed value. The override-store
	// read error now propagates from ResolveForTenant, so a transient
	// override-store blip on the F-5.1.20 tenant-narrowing path fails
	// closed here instead of admitting injection. F-5.1.20.
	rt, err := runtimecapoverride.ResolveForTenant(r.Context(), s.runtimes, s.capOverrides, row.TenantID, row.RuntimeRef)
	switch {
	case err == nil:
		if !rt.InjectionSupported() {
			s.writeError(w, http.StatusForbidden, "INJECTION_REJECTED",
				"runtime does not support mid-session message injection", nil)
			return false
		}
		return true
	case errors.Is(err, runtimestore.ErrNotFound):
		// No runtime record from either store: degrade open so a
		// gateway whose registry has no entry for the ref does not
		// block injection.
		return true
	default:
		// A transient read error from either backing store: fail
		// closed. The granular "runtime-store read failed" versus
		// "override-store read failed" cause is recorded in both the
		// gateway log and the lenny_injection_gate_failclosed_total
		// metric rather than as a distinct client code, keeping the
		// client surface coarse (the cause is not an authorization or
		// existence oracle). The metric label lets an operator see the
		// runtime-store-versus-override-store distinction the coarse
		// SERVICE_UNAVAILABLE code hides. spec: §5.1 (injection
		// fail-closed), §15.1 (SERVICE_UNAVAILABLE).
		cause := injectionFailClosedCause(err)
		log.Printf("sessionserver: §5.1 injection gate failed closed for session %s runtime %s (cause=%s): resolve error: %v",
			row.ID, row.RuntimeRef, cause, err)
		if s.incInjectionGateFailClosed != nil {
			s.incInjectionGateFailClosed(cause)
		}
		w.Header().Set("Retry-After", strconv.Itoa(serviceUnavailableRetryAfterSeconds))
		s.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE",
			"runtime capability lookup is transiently unavailable; retry", nil)
		return false
	}
}

// isPreRunningState reports whether s names a pre-running session
// state per §7.2. External-client REST calls against any of
// these states reject with TARGET_NOT_READY. F-7.2.15.
func isPreRunningState(s session.State) bool {
	switch s {
	case session.StateCreated, session.StateFinalizing, session.StateReady, session.StateStarting:
		return true
	}
	return false
}
