// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/delegation/tracing"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/fileexport"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/messagerouting"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	obstracing "github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// registerEnv carries the prologue values Register resolves once from Deps
// and threads to each per-family registrar: the UTC clock, the id
// generator, the resolved default tenant, and the §7.2 send_message
// fixed-window+lifetime rate limiter and deployment-resolved effective
// messaging scope. Bundling them keeps each registerXTools helper a thin
// extraction of one tool family's registration block while Register stays a
// short sequence of guarded calls. spec: §8.5 (tool surface), §7.2 line 240
// (messaging scope). F-7.2.6.
type registerEnv struct {
	clock      func() time.Time
	idFn       func() string
	tenant     string
	msgLimiter *messagingLimiter
	msgScope   session.MessagingScope
}

// registerSessionLifecycleTools installs the §15.2 session-lifecycle
// tools: attach_session (when the session store is wired), and the
// create_session, send_message, interrupt_session, and cancel_session
// surfaces. spec: §15.2 lines 1289, 1331 (attach_session); §15.2.1
// (create_session); §7.2/§8.5 (send_message); §8.5 (interrupt/cancel).
// F-15.2.2, F-15.2.7, F-7.2.6, F-8.5.16.
func registerSessionLifecycleTools(srv *mcp.Server, deps Deps, env registerEnv) {
	clock := env.clock
	idFn := env.idFn
	tenant := env.tenant
	msgLimiter := env.msgLimiter
	msgScope := env.msgScope
	// spec: §15.2 lines 1289, 1331 — attach_session. The streaming
	// Streamable HTTP SSE channel is intercepted in the transport layer
	// (mcp.Server.handleAttachStream) when the client sends Accept:
	// text/event-stream; this handler is the non-streaming snapshot a
	// WebSocket or plain-JSON caller receives, carrying the session's
	// current state and the resumeFromSeq cursor (the durable last_seq)
	// to reconnect the stream with. Registered whenever the session store
	// is wired so the tool is discoverable in tools/list on every
	// transport. F-15.2.2.
	if deps.Store != nil {
		srv.RegisterTool(mcp.Tool{
			Name:        mcp.AttachToolName,
			Description: "Attach to a running session's event stream. Reconnect with Accept: text/event-stream to stream events; optional resumeFromSeq replays buffered events with SeqNum greater than the cursor before live delivery.",
			InputSchema: json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string"},"resumeFromSeq":{"type":"integer","minimum":0,"description":"§15.2 event-stream resume: replay buffered events with SeqNum greater than this cursor before live delivery."}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			tenant := callerTenantID(ctx, tenant)
			var in struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, errInvalidArgs(err)
			}
			if in.SessionID == "" {
				return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR", "sessionId is required", nil)
			}
			row, err := deps.Store.Get(ctx, tenant, in.SessionID)
			if err != nil {
				return mcp.ToolResult{}, errSessionLookup(err)
			}
			out, _ := json.Marshal(map[string]any{
				"sessionId":     row.ID,
				"state":         string(row.State),
				"resumeFromSeq": row.LastSeq,
			})
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: string(out)}}}, nil
		})
	}

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/create_session",
		Description: "Create a new agent session against a runtime.",
		// spec: §15.2.1 rule 4 line 1386 — the input schema is generated
		// from the OpenAPI `CreateSessionRequest` (the single authoritative
		// schema for this overlapping operation) by the build-pipeline code
		// generation step in pkg/gateway/mcptools/internal/genmcpschemas,
		// so the MCP create_session surface advertises the same fields the
		// REST POST /v1/sessions validates (runtimeRef, userId, environment,
		// workspacePlan, isolationProfile, metadata, retryPolicy) plus the
		// MCP-only §11.5 `idempotencyKey` extension.
		// TestGeneratedSchemasMatchOpenAPI guards against drift. F-15.2.7.
		//
		// §11.5 line 277 — `idempotencyKey` (optional, ≤128 runes)
		// collapses retries of CreateSession to one execution; identical
		// retries replay the cached ToolResult, mismatched bodies are
		// rejected with IDEMPOTENCY_KEY_REUSED. spec: F-11.5.1.
		InputSchema: GeneratedCreateSessionInputSchema,
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant is the
		// authenticated principal's, not the Register-time default,
		// so a multi-tenant deployment scopes the session row to the
		// caller's tenant. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		// spec: §15.2.1 rule 4 line 1386 — decode the full OpenAPI
		// CreateSessionRequest and forward every field to the shared
		// service so the MCP create_session path runs the same workspace-
		// plan, isolation-profile, metadata, and retry-policy validation
		// the REST handler runs. The §11.5 idempotencyKey is read by the
		// MCP idempotency hook before this handler runs and is not a
		// CreateSessionRequest field, so json.Unmarshal drops it here.
		// F-15.2.7 / F-15.2.4 / F-11.5.1.
		var in sessionserver.CreateSessionRequest
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				fmt.Sprintf("invalid arguments: %v", err), nil)
		}
		if in.RuntimeRef == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"runtimeRef is required",
				map[string]any{"field": "runtimeRef"})
		}
		// spec: §10.6 line 557 — a connection scoped to the explicit
		// environment surface (/mcp/environments/{name}) defaults the
		// session to that environment when the call omits one. An explicit
		// `environment` argument still wins. F-10.6.11.
		if in.Environment == "" {
			in.Environment = environmentmw.ExplicitEnvironmentFromContext(ctx)
		}
		// spec: §15.2.1 rule 1 line 1380 — route the create through the
		// shared §15.1 service layer so the MCP surface runs the same
		// active-user, quota, concurrency, admission-rate, policy-chain,
		// environment-access, and runtime / isolation / workspace-plan
		// gates the REST POST /v1/sessions handler runs, and returns the
		// same `created`-state envelope with a §7.1 uploadToken. An
		// MCP-created session therefore consumes tenant quota and is
		// subject to policy interception exactly like a REST-created one.
		// F-15.2.4.
		if deps.SessionCreator != nil {
			resp, svcErr := deps.SessionCreator.CreateSessionService(ctx, tenant, in)
			if svcErr != nil {
				// The §15.2.1 rule-3 envelope: surface the REST code +
				// details so the shared errorclassify table assigns the
				// same (category, retryable) pair on both surfaces.
				return mcp.ToolResult{}, mcp.NewToolError(svcErr.Code, svcErr.Message, svcErr.Details)
			}
			// Project the shared response onto the MCP create_session
			// envelope: the `sessionId`/`state` fields the MCP clients and
			// SDKs already read, plus the §7.1 `uploadToken` the REST
			// surface returns (previously absent on the MCP path, so a
			// subsequent upload over REST failed differently — impact (e)).
			// F-15.2.4.
			out := map[string]any{
				"sessionId":   resp.ID,
				"state":       resp.State,
				"uploadToken": resp.UploadToken,
			}
			payload, err := json.Marshal(out)
			if err != nil {
				return mcp.ToolResult{}, mcp.NewToolError("INTERNAL_ERROR",
					"create response encode: "+err.Error(), nil)
			}
			return textResult(string(payload)), nil
		}
		// Legacy direct-store fallback for the minimal in-process gateway
		// that wires no session server. It runs no gates and mints no
		// uploadToken; production always wires deps.SessionCreator. The
		// runtimeRef-required check above already covers both paths.
		// F-15.2.4.
		now := clock()
		row := sessionstore.Session{
			ID:               idFn(),
			TenantID:         tenant,
			UserID:           in.UserID,
			RuntimeRef:       in.RuntimeRef,
			Environment:      in.Environment,
			State:            session.StateRunning,
			IsolationProfile: isolation.DefaultForMode(deps.DevMode),
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := deps.Store.Create(ctx, row); err != nil {
			return mcp.ToolResult{}, err
		}
		return textResult(fmt.Sprintf(`{"sessionId":%q,"state":%q}`, row.ID, row.State)), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/send_message",
		Description: "Deliver a message to a running session and return the response.",
		// spec: §8.5 line 537 — the §8.5 contract is
		// `lenny/send_message(to, message)`; the implementation MUST
		// expose `to` (target taskId) and `message` (content) as the
		// schema-named fields. `inReplyTo` is a §7.2/§8.8 extension
		// surfaced from the await_children pattern, `messageId` is the
		// §15.4 line 1784 sender-supplied id surface, and
		// `fromSessionId` enables the §7.2 line 240 topology check
		// when the calling transport has no principal binding.
		// F-8.5.16 (rename), F-7.2.22 (fromSessionId). The `message`
		// argument is the §15.4 MessageEnvelope.input union; see
		// sendMessageInputSchema. F-MS5.
		InputSchema: sendMessageInputSchema,
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal so the §7.2 topology lookup and the §4 chain payload
		// stay scoped to the right tenant. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			// To is the §8.5 target session id (renamed from the legacy
			// `sessionId` to match the §8.5 line 537 schema). F-8.5.16.
			To string `json:"to"`
			// Message is the §8.5 content field (renamed from the legacy
			// `content` to match the §8.5 line 537 schema). It is the §15.4
			// MessageEnvelope.input union (bare string or MessagePart[]),
			// identical to the REST /messages content field under the
			// §15.2.1 parity rule. F-8.5.16, F-MS5.
			Message   sessionrecord.MessageContent `json:"message"`
			InReplyTo string                       `json:"inReplyTo"`
			// MessageID is the §15.4 line 1784 sender-supplied id. When
			// empty the gateway assigns a `msg_` prefix id so every
			// receipt is correlatable. F-7.2.10.
			MessageID string `json:"messageId"`
			// FromSessionID, when set, enables the §7.2 line 373
			// parent/child/sibling topology check. Empty falls through
			// to the principal's SessionID claim, then to no topology
			// check (the pre-F-7.2.22 behaviour). F-7.2.22.
			FromSessionID string `json:"fromSessionId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		if in.To == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"to is required (§8.5 line 537)", nil)
		}
		row, err := deps.Store.Get(ctx, tenant, in.To)
		if err != nil {
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		if session.IsTerminal(row.State) {
			// spec: §15 catalog (15:998) — a message to a terminal
			// target is TARGET_TERMINAL (PERMANENT), distinct from the
			// caller's own INVALID_STATE_TRANSITION. F-15.2.12.
			return mcp.ToolResult{}, mcp.NewToolError("TARGET_TERMINAL",
				fmt.Sprintf("session %s is terminal (%s)", in.To, row.State), nil)
		}
		// spec: §7.2 line 268 — cross-tenant validation runs before
		// scope evaluation and rate limiting and applies to every
		// message path (inter-session, inReplyTo, delivery:immediate).
		if crossTenantDenied(tenant, row) {
			return mcp.ToolResult{}, mcp.NewToolError("CROSS_TENANT_MESSAGE_DENIED",
				"target session belongs to a different tenant (§7.2 line 268)", nil)
		}
		// spec: §7.2 line 240 messagingScope + line 373 parent
		// asymmetry — restrict the target to the sender's direct
		// parent/child, and to a sibling only when the effective scope
		// is `siblings`. The principal's SessionID claim is the
		// canonical sender id; the legacy `fromSessionId` field is the
		// transport fallback for callers that have not yet bound a
		// principal. F-7.2.6, F-7.2.22, F-8.5.16.
		senderID := callerSessionID(ctx, in.FromSessionID)
		if senderID != "" {
			sender, err := deps.Store.Get(ctx, tenant, senderID)
			if err != nil {
				return mcp.ToolResult{}, mcp.NewToolError("SCOPE_DENIED",
					fmt.Sprintf("sender session %s not found", senderID), nil)
			}
			if !withinMessagingScope(sender, row, msgScope) {
				return mcp.ToolResult{}, mcp.NewToolError("SCOPE_DENIED",
					fmt.Sprintf("target %s is not reachable from sender %s under messagingScope %q",
						in.To, senderID, msgScope.OrDefault()), nil)
			}
		}
		// spec: §15.4 line 1784 — the gateway assigns a `msg_` prefix
		// id when the sender omits one so every receipt is
		// correlatable. F-7.2.10.
		messageID := in.MessageID
		if messageID == "" {
			messageID = "msg_" + idFn()
		}
		// spec: §7.2 line 270 + §8.3 lines 269-272 — per-sender
		// outbound + lifetime and per-target inbound aggregate rate
		// limits, evaluated before delivery. An exceeded limit returns a
		// RATE_LIMITED delivery receipt (§7.2 line 371, §8.3 line 309)
		// rather than a tool error, so the sender can react. F-7.2.6.
		allowed, err := msgLimiter.allow(ctx, tenant, senderID, in.To, clock())
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("messaging rate limit: %w", err)
		}
		if !allowed {
			return textResult(buildSendMessageReceiptStatus(messageID, session.DeliveryStatusRateLimited, clock())), nil
		}
		// §8.5: when the message answers a pending lenny/request_input
		// call, it resolves that blocked call directly instead of being
		// delivered to the runtime. A non-matching inReplyTo falls
		// through to normal delivery — it is then an ordinary threaded
		// message.
		if in.InReplyTo != "" && deps.InputWaits != nil {
			err := deps.InputWaits.Resolve(in.To, in.InReplyTo, in.Message.Text())
			if err == nil {
				// spec: §15.4 lines 1725-1737 — the inReplyTo path
				// counts as delivered (the runtime consumed the
				// answer). The receipt also carries the resolved
				// requestId so callers correlating by inReplyTo can
				// pivot off either field. F-7.2.10.
				return textResult(buildSendMessageReceipt(messageID, in.InReplyTo, nil, clock())), nil
			}
			if !errors.Is(err, inputwait.ErrNotFound) {
				return mcp.ToolResult{}, err
			}
		}
		// spec: §7.2 paths 3/5/6/7 (lines 319-331) — an inter-session
		// message to a target that is not reading from stdin is buffered,
		// not delivered. Path 1 (inReplyTo) resolved above; terminal
		// returned TARGET_TERMINAL above. Classify the remaining message
		// by the target state: input_required / suspended → inbox;
		// recovering (resume_pending / awaiting_client_action) and
		// pre-running parent→child → DLQ. Only a `running` target falls
		// through to the executor (path 2). The pod-adapter readiness
		// signal and cross-replica forwarding are gated as in the REST
		// path. F-7.2.5.
		inputRequired := row.State == session.StateInputRequired ||
			(deps.InputWaits != nil && len(deps.InputWaits.PendingForSession(row.ID)) > 0)
		decision := messagerouting.Classify(row.State, inputRequired, false, messagerouting.SourceInterSession)
		if decision.Action == messagerouting.ActionBufferInbox || decision.Action == messagerouting.ActionBufferDLQ {
			// spec: §15.4 (MessageEnvelope.input) — buffer the §15.4 union
			// content in its wire form (a bare string stays a JSON string, a
			// part array stays an array) so the deferred redelivery path
			// re-delivers the original multipart envelope rather than a
			// text-flattened copy. F-MS5.
			payload, mErr := json.Marshal(in.Message)
			if mErr != nil {
				return mcp.ToolResult{}, fmt.Errorf("marshal buffered message content: %w", mErr)
			}
			buffered := sessioninbox.Message{
				MessageID:       messageID,
				SenderSessionID: senderID,
				Payload:         payload,
				EnqueuedAt:      clock(),
			}
			var evicted *sessioninbox.Message
			var berr error
			overflowReason := session.DeliveryReasonInboxOverflow
			if decision.Action == messagerouting.ActionBufferInbox {
				evicted, berr = deps.Messaging.EnqueueInbox(ctx, tenant, row.ID, buffered)
			} else {
				evicted, berr = deps.Messaging.EnqueueDLQ(ctx, tenant, row.ID, buffered, 0)
				overflowReason = session.DeliveryReasonDLQOverflow
			}
			if berr != nil {
				// No inbox/DLQ wired (no Redis): surface inbox_unavailable
				// rather than delivering to a non-running runtime.
				return textResult(buildSendMessageReceiptStatusReason(messageID,
					session.DeliveryStatusError, session.DeliveryReasonInboxUnavailable, clock())), nil
			}
			status := session.DeliveryStatusQueued
			reason := session.DeliveryReason("")
			if evicted != nil {
				status = session.DeliveryStatusDropped
				reason = overflowReason
			}
			return textResult(buildSendMessageReceiptStatusReason(messageID, status, reason, clock())), nil
		}
		if deps.Executor == nil {
			return mcp.ToolResult{}, errors.New("no executor configured")
		}
		// §4 PreMessageDelivery: run the interceptor chain over the
		// message body before delivery. A REJECT blocks the message; a
		// MODIFY rewrites what the target session receives. The §15.4 union
		// content is projected to its text form for the interceptor scan and
		// the maxInputSize bound; the full multipart envelope lands when the
		// executor carries MessagePart[] end to end. spec: §15.4
		// (MessageEnvelope.input).
		messageText := in.Message.Text()
		messageBody := messageText
		if deps.Interceptors != nil {
			req := interceptor.Request{
				Phase:     interceptor.PhasePreMessageDelivery,
				SessionID: row.ID,
				TenantID:  tenant,
				Content:   []byte(messageText),
			}
			// spec: §4.8 line 1040 / §13.5 mitigation 3 — apply the same
			// content policy the delegation path applies: the target
			// session's effective contentPolicy.maxInputSize bounds the
			// message body, and contentPolicy.interceptorRef selects the
			// single external scanner that runs at PreMessageDelivery. A
			// policy with interceptorRef: null runs no external scan.
			// F-13.5.2.
			var res interceptor.Result
			if deps.ContentPolicies != nil {
				ref := ""
				if maxSize, r, ok := deps.ContentPolicies.ResolveContentPolicy(ctx, tenant, row.ID); ok {
					ref = r
					// spec: §4.8 line 1034 / §8.3 line 218 (SEC-013,
					// F-4.8.17) — reject the delivery when the target
					// session's effective interceptorRef names an
					// interceptor inside the `fail-closed → fail-open`
					// weakening cooldown, mirroring the delegate_task gate
					// so a stolen admin credential cannot use a brief
					// fail-open window to push messages past a now-disabled
					// content scanner.
					if deps.CooldownChecker != nil && ref != "" {
						if cdErr := deps.CooldownChecker.InterceptorFailPolicyCooldown(ctx, ref); cdErr != nil {
							var wc *delegation.InterceptorWeakeningCooldownError
							if errors.As(cdErr, &wc) {
								return mcp.ToolResult{}, cooldownToolError(wc)
							}
						}
					}
					if maxSize > 0 && len(messageText) > maxSize {
						return mcp.ToolResult{}, mcp.NewToolError(policy.CodeInputTooLarge,
							fmt.Sprintf("message body is %d bytes, exceeding the target session's contentPolicy.maxInputSize limit of %d bytes", len(messageText), maxSize),
							map[string]any{"phase": string(interceptor.PhasePreMessageDelivery)})
					}
				}
				res = deps.Interceptors.RunPolicyScoped(ctx, req, ref)
			} else {
				res = deps.Interceptors.Run(ctx, req)
			}
			if res.Action == interceptor.ActionReject {
				recordChainRejection(ctx, deps, tenant, row.ID, interceptor.PhasePreMessageDelivery, res)
				// spec: §15.2.1 rule 3 — a deliberate PreMessageDelivery
				// REJECT is a policy decision; emit INTERCEPTOR_REJECTED so
				// the envelope is POLICY / not-retryable, matching the
				// PostAgentOutput path below. A built-in code on the Result
				// (e.g. INTERCEPTOR_TIMEOUT when the policy-named scanner is
				// unreachable, §4.8 line 1032) is surfaced verbatim so the
				// (category, retryable) pair is correct. F-15.2.12 /
				// F-13.5.2.
				code := "INTERCEPTOR_REJECTED"
				if res.Code != "" {
					code = res.Code
				}
				return mcp.ToolResult{}, mcp.NewToolError(code,
					fmt.Sprintf("message delivery rejected by policy: %s", res.Reason), nil)
			}
			if res.Action == interceptor.ActionModify {
				messageBody = string(res.ModifiedContent)
			}
		}
		// spec: §15.4.1 lines 1696-1707 / §13.5 mitigation 6 — the gateway
		// stamps the delivered envelope's `from` from the authenticated
		// sending session so the target can attribute the message and the
		// runtime cannot forge an origin. An unattributed send (no principal
		// session binding and no fromSessionId) defers to the executor's
		// default gateway-client identity. F-13.5.11.
		resp, err := deps.Executor.Send(ctx, row.ID, []executor.Message{
			{Role: "user", Content: messageBody, From: senderFrom(senderID)},
		})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		out := resp.Parts
		// §4.8 PostAgentOutput: run the chain over the agent's output
		// parts before delivering the response to the calling agent. A
		// REJECT blocks delivery; a MODIFY rewrites the parts.
		out, rej := applyPostAgentOutput(ctx, deps, tenant, row.ID, out)
		if rej != nil {
			code := rej.Code
			if code == "" {
				code = "INTERCEPTOR_REJECTED"
			}
			return mcp.ToolResult{}, mcp.NewToolError(code, rej.Reason, nil)
		}
		// spec: §15.4 lines 1725-1737 — emit the `delivery_receipt`
		// as the first text block so a strict client can parse it
		// before reading the runtime's text output. The executor
		// output follows as additional text blocks so existing
		// callers parsing `content[*].text` still see the runtime's
		// response.
		content := make([]mcp.ToolContent, 0, 1+len(out))
		content = append(content, mcp.ToolContent{
			Type: "text",
			Text: buildSendMessageReceipt(messageID, "", out, clock()),
		})
		for _, p := range out {
			if p.Type == "text" {
				content = append(content, mcp.ToolContent{Type: "text", Text: p.Text})
			}
		}
		return mcp.ToolResult{Content: content}, nil
	})

	// spec: §15.2 lines 1295, 1303 — interrupt_session and cancel_session
	// are client-facing MCP tools on the public surface. §27.5 binds the
	// playground to "a client of the public MCP surface", so the chat
	// pane's Interrupt and Cancel buttons (and the §27.6 best-effort
	// cancel hint on browser close) MUST resolve to registered tools.
	// Both reuse the §15.1 precondition table so the MCP transition is
	// identical to the REST POST /v1/sessions/{id}/interrupt and
	// DELETE /v1/sessions/{id} edges. F-27.5.3.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/interrupt_session",
		Description: "Interrupt the running agent in a session (§15.2). REST equivalent: POST /v1/sessions/{id}/interrupt.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string","description":"Target session id."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		if in.SessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR", "sessionId is required", map[string]any{"field": "sessionId"})
		}
		row, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		// spec: §15.1 — /interrupt is valid only from running /
		// input_required; any other state is INVALID_STATE_TRANSITION.
		if perr := session.Validate(session.PreconditionRequest{
			Endpoint:     session.EndpointInterrupt,
			CurrentState: row.State,
		}); perr != nil {
			return mcp.ToolResult{}, errPrecondition(perr)
		}
		updated, err := deps.Store.Update(ctx, tenant, in.SessionID, func(row *sessionstore.Session) error {
			// spec: §15.1 — interrupt transitions running → suspended.
			row.State = session.StateSuspended
			return nil
		})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		return textResult(fmt.Sprintf(`{"sessionId":%q,"state":%q}`, updated.ID, updated.State)), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name: "lenny/cancel_session",
		// spec: §27.6 line 202 — the optional `reason` carries the
		// playground best-effort cancel hint (`playground_client_closed`).
		// When the reason marks a best-effort hint the gateway accepts the
		// frame even if the session is already gone or terminal (the
		// dropped-frame fallback is the §27.6 idle-timeout path), instead
		// of returning a hard error. F-27.5.3 / F-27.6.5.
		Description: "Force-cancel a session; marks cancelled (§15.2). REST equivalent: DELETE /v1/sessions/{id}.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string","description":"Target session id."},"reason":{"type":"string","description":"§27.6 best-effort hint reason, e.g. playground_client_closed."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			SessionID string `json:"sessionId"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		if in.SessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR", "sessionId is required", map[string]any{"field": "sessionId"})
		}
		// spec: §27.6 line 202 — playground_client_closed is a best-effort
		// hint, not an authoritative teardown. A dropped/late frame must
		// not error; it falls through to the idle-timeout path.
		bestEffort := in.Reason == "playground_client_closed"
		row, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			if bestEffort {
				return textResult(fmt.Sprintf(`{"sessionId":%q,"accepted":true,"reason":%q}`, in.SessionID, in.Reason)), nil
			}
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		// spec: §15.1 — DELETE/cancel is valid from any non-terminal
		// state. For a best-effort hint a terminal session is a no-op
		// (already torn down), so the hint succeeds idempotently.
		if perr := session.Validate(session.PreconditionRequest{
			Endpoint:     session.EndpointDelete,
			CurrentState: row.State,
		}); perr != nil {
			if bestEffort {
				return textResult(fmt.Sprintf(`{"sessionId":%q,"accepted":true,"state":%q,"reason":%q}`, in.SessionID, row.State, in.Reason)), nil
			}
			return mcp.ToolResult{}, errPrecondition(perr)
		}
		updated, err := deps.Store.Update(ctx, tenant, in.SessionID, func(row *sessionstore.Session) error {
			// spec: §15.1 — cancel marks the session cancelled.
			row.State = session.StateCancelled
			return nil
		})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		if in.Reason != "" {
			return textResult(fmt.Sprintf(`{"sessionId":%q,"state":%q,"reason":%q}`, updated.ID, updated.State, in.Reason)), nil
		}
		return textResult(fmt.Sprintf(`{"sessionId":%q,"state":%q}`, updated.ID, updated.State)), nil
	})
}

// registerTaskTreeTools installs the §8.5/§8.9 delegation-tree tools:
// get_task_tree, cancel_child, and await_children. spec: §8.5 line 540
// (get_task_tree), §8.9 lines 615-623; §8.5 (cancel_child); §8.8
// (await_children). F-8.5.11.
func registerTaskTreeTools(srv *mcp.Server, deps Deps, env registerEnv) {
	clock := env.clock
	tenant := env.tenant
	srv.RegisterTool(mcp.Tool{
		Name: "lenny/get_task_tree",
		// spec: §8.5 line 540 — returns the task hierarchy visible to
		// the calling session (scoped by `treeVisibility`). §8.9 lines
		// 615-623 fix the input schema at `{"type":"object",
		// "properties":{},"required":[]}` — the caller's session is
		// resolved from the MCP principal, not from a request field.
		// The legacy `sessionId` field is accepted as a transport
		// fallback for tests and dev-headers callers that have not
		// bound a session-scoped principal yet; it is not in
		// `required` so a spec-conformant caller sending an empty
		// object succeeds. F-8.9.11.
		Description: "Return the §8 delegation task tree rooted at the calling session (visibility scoped by §8.3 treeVisibility).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sessionId":{"type":"string","description":"§15.2.1 transport-fallback session id; the principal's SessionID claim takes precedence."}},"required":[]}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			// SessionID is the §15.2.1 transport fallback used when the
			// principal carries no SessionID claim. The spec schema
			// names no input parameters; the fallback keeps the tool
			// usable in pre-bearer dev deployments. F-8.9.11.
			SessionID string `json:"sessionId,omitempty"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, errInvalidArgs(err)
			}
		}
		sessionID := callerSessionID(ctx, in.SessionID)
		if sessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
		}
		caller, err := deps.Store.Get(ctx, tenant, sessionID)
		if err != nil {
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		// spec: §8.9 line 1010 — read only the rows belonging to the
		// requested session's delegation tree via the §12.5
		// `idx_sessions_root` index; the cost is O(tree size) instead
		// of O(tenant size). F-8.9.7.
		rootSessionID := caller.RootSessionID
		if rootSessionID == "" {
			rootSessionID = caller.ID
		}
		all, err := deps.Store.ListByRoot(ctx, tenant, rootSessionID)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// spec: §8.5 line 540 / §8.3 lines 311-319 — scope the response to
		// the caller's effective treeVisibility. `full` roots the response
		// at the tree apex (the caller sees the whole tree including
		// siblings); `parent-and-self` and `self-only` narrow it to the
		// caller's parent+self or self alone. The allowed set restricts
		// which children the walker descends into. F-8.5.2 / F-8.9.2.
		respRoot, allowed := sessionstore.VisibleTree(caller, all, caller.TreeVisibility)
		wctx := treeWalkContext{
			ctx:           ctx,
			tenantID:      tenant,
			rootSessionID: caller.ID,
			observer:      deps.TreeCycleObserver,
			allowed:       allowed,
		}
		tree := buildTree(wctx, respRoot, all)
		body, _ := json.Marshal(tree)
		return textResult(string(body)), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name: "lenny/cancel_child",
		// spec: §8.5 line 531 — the §8.5 contract is
		// `lenny/cancel_child(child_id)`; the parent is implicit in the
		// calling principal. The legacy `parentSessionId` field is
		// accepted as a transport fallback for tests and dev-headers
		// callers that have not yet bound a session-scoped principal
		// but never participates in `required`. F-8.5.15.
		Description: "Cancel a child session and cascade the cancellation to its descendants (§8.5).",
		InputSchema: json.RawMessage(`{"type":"object","required":["childSessionId"],"properties":{"childSessionId":{"type":"string"},"parentSessionId":{"type":"string","description":"§15.2.1 transport-fallback parent session id; the principal's SessionID claim takes precedence."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			// ParentSessionID is the transport fallback used when the
			// principal carries no SessionID claim. F-8.5.15.
			ParentSessionID string `json:"parentSessionId,omitempty"`
			ChildSessionID  string `json:"childSessionId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		parentSessionID := callerSessionID(ctx, in.ParentSessionID)
		if parentSessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"caller session is unbound (no principal SessionID, no parentSessionId arg)", nil)
		}
		if in.ChildSessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"childSessionId is required", nil)
		}
		if parentSessionID == in.ChildSessionID {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"a session cannot cancel itself as its own child", nil)
		}
		child, err := deps.Store.Get(ctx, tenant, in.ChildSessionID)
		if err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("RESOURCE_NOT_FOUND",
				fmt.Sprintf("child session lookup: %v", err), nil)
		}
		// spec: §8.9 line 1010 — read only the rows in the child's
		// delegation tree (the parent must be in the same tree to
		// authorize the cancel) instead of the whole tenant. F-8.9.7.
		rootID := child.RootSessionID
		if rootID == "" {
			rootID = child.ID
		}
		all, err := deps.Store.ListByRoot(ctx, tenant, rootID)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// Authorization: the caller may cancel only sessions inside its
		// own §8 delegation subtree.
		if !isDescendant(child, parentSessionID, all) {
			// spec: §15 catalog (15:1028) — cancelling outside the
			// caller's own subtree is an authorization failure;
			// PERMISSION_DENIED (POLICY), not a generic error. F-15.2.12.
			return mcp.ToolResult{}, mcp.NewToolError("PERMISSION_DENIED",
				fmt.Sprintf("session %s is not a child of %s", in.ChildSessionID, parentSessionID), nil)
		}
		if session.IsTerminal(child.State) {
			return mcp.ToolResult{}, mcp.NewToolError("TARGET_TERMINAL",
				fmt.Sprintf("child session %s is already terminal (%s)", in.ChildSessionID, child.State), nil)
		}
		cancelled, err := cancelSubtree(ctx, deps.Store, tenant, child, all)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// spec: §11.3 line 236 / §11.4 line 258 — a cancel must drain each
		// cancelled runtime's pod, not merely flip its row. Without this the
		// child agents keep running, holding tokens, executing tool calls, and
		// charging their credential leases until the watchdog's maxSessionAge
		// clock fires hours later. ReleaseSession records the §6.2 cancelled
		// disposition and triggers the adapter's graceful shutdown; it no-ops
		// on a nil executor (the in-memory dev posture). Best-effort per id.
		// F-11.3.1.
		if deps.Executor != nil {
			for _, id := range cancelled {
				_ = executor.ReleaseSession(ctx, deps.Executor, id, executor.DispositionCancelled)
			}
		}
		// §8.10: a child reaching a terminal state is archived to the
		// session_tree_archive so a resumed parent can replay its
		// result. Archiving is best-effort observability — the
		// cancellation itself has already committed.
		if deps.TreeArchive != nil {
			archiveCancelled(ctx, deps.TreeArchive, tenant, treeRoot(child, all), cancelled, all, clock())
		}
		body, _ := json.Marshal(struct {
			Cancelled []string `json:"cancelled"`
		}{Cancelled: cancelled})
		return textResult(string(body)), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/await_children",
		Description: "Wait for delegated child sessions to reach terminal states (§8.5).",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","childIds"],"properties":{"sessionId":{"type":"string"},"childIds":{"type":"array","items":{"type":"string"}},"mode":{"type":"string","enum":["all","any","settled"]}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			SessionID string   `json:"sessionId"`
			ChildIDs  []string `json:"childIds"`
			Mode      string   `json:"mode"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		if in.SessionID == "" || len(in.ChildIDs) == 0 {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"sessionId and a non-empty childIds are required", nil)
		}
		mode := in.Mode
		if mode == "" {
			mode = "all"
		}
		if mode != "all" && mode != "any" && mode != "settled" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				fmt.Sprintf("mode %q is not one of all, any, or settled", mode), nil)
		}
		if _, err := deps.Store.Get(ctx, tenant, in.SessionID); err != nil {
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		// Authorization: every awaited id must be a direct child of the
		// caller — a session may only await children it delegated. A
		// child whose live row is gone is resolved from the §8.10
		// archive so a resumed parent can still re-await it.
		for _, cid := range in.ChildIDs {
			oc, err := resolveChild(ctx, deps.Store, deps.TreeArchive, deps.TaskUsage, tenant, cid)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			if oc.parentID != in.SessionID {
				// spec: §15 catalog (15:1028) — awaiting a session the
				// caller did not delegate is an authorization failure.
				// F-15.2.12.
				return mcp.ToolResult{}, mcp.NewToolError("PERMISSION_DENIED",
					fmt.Sprintf("session %s is not a child of %s", cid, in.SessionID), nil)
			}
		}
		// spec: §16.3 line 345 — the gateway-side await/collect path runs
		// under one delegation.await_child span per await call (the poll
		// loop reuses the same span rather than emitting one per tick).
		// Correlation attributes auto-project from the context; the awaited
		// child count is a non-PII descriptive attribute.
		ctx, span := obstracing.NewTracer(nil).Start(ctx, obstracing.SpanDelegationAwaitChild)
		span.SetAttributes(attribute.Int("delegation.await.child_count", len(in.ChildIDs)))
		defer span.End()
		// spec: §8.8 line 981 — register the await edge for the duration of
		// the poll loop so the subtree deadlock detector can see that this
		// session is blocking on these children. The edge drops on return.
		// F-8.8.6.
		defer deps.DeadlockTracker.Begin(tenant, in.SessionID, in.ChildIDs)()
		// Poll the child states until the mode's settle condition holds.
		ticker := time.NewTicker(awaitPollInterval)
		defer ticker.Stop()
		for {
			// spec: §6.2 line 276 — an await_children invocation and each
			// poll round while blocked on children is qualifying activity,
			// so the §11.3 idle watchdog does not falsely expire a parent
			// actively waiting on slow children. The stamper coalesces to
			// ≤1/s. F-11.3.7.
			if deps.ActivityStamper != nil {
				deps.ActivityStamper.Stamp(tenant, in.SessionID)
			}
			results, settled, err := collectChildResults(ctx, deps.Store, deps.TreeArchive, deps.TaskUsage, tenant, in.ChildIDs, mode)
			if err != nil {
				obstracing.RecordError(span, err)
				return mcp.ToolResult{}, err
			}
			if settled {
				body, _ := json.Marshal(struct {
					Results []sessionrecord.Result `json:"results"`
				}{Results: results})
				return textResult(string(body)), nil
			}
			// spec: §8.8 lines 981-997 — when the detector has flagged this
			// session as a deadlocked subtree root, yield the
			// deadlock_detected event so the parent's agent can break the
			// deadlock (resolve a pending request_input or cancel a blocked
			// child) before willTimeoutAt. The v1 MCP transport is unary, so
			// the event rides one partial frame and the parent re-awaits.
			// F-8.8.6.
			if deps.Deadlocks != nil {
				if ev, ok := deps.Deadlocks.Event(in.SessionID); ok {
					body, _ := json.Marshal(struct {
						Partial  bool           `json:"partial"`
						Deadlock deadlock.Event `json:"deadlock"`
					}{Partial: true, Deadlock: ev})
					return textResult(string(body)), nil
				}
			}
			// spec: §8.8 lines 951-971 — when an awaited child enters the
			// input_required sub-state (it has a pending lenny/request_input
			// round), the call yields a partial result carrying that child's
			// `requestId` and question `parts` instead of blocking until the
			// child settles, so the parent can answer via lenny/send_message
			// (inReplyTo) and re-await. The v1 MCP transport is unary (the
			// gRPC streaming AwaitChildren is post-v1, mcp/mcp.go), so each
			// call returns the currently-blocked children in one partial
			// frame and the parent re-invokes after acting. Without this the
			// parent would block until every child terminates and could never
			// answer a child's question. F-8.5.5 / F-8.8.5.
			if partial := collectInputRequired(deps.InputWaits, in.ChildIDs); len(partial) > 0 {
				body, _ := json.Marshal(struct {
					Partial       bool                 `json:"partial"`
					InputRequired []inputRequiredChild `json:"inputRequired"`
				}{Partial: true, InputRequired: partial})
				return textResult(string(body)), nil
			}
			select {
			case <-ctx.Done():
				obstracing.RecordError(span, ctx.Err())
				return mcp.ToolResult{}, ctx.Err()
			case <-ticker.C:
			}
		}
	})
}

// registerTracingTool installs lenny/set_tracing_context, which records
// the §8.3 tracing identifiers on a session for propagation through
// delegation. spec: §8.3.
func registerTracingTool(srv *mcp.Server, deps Deps, env registerEnv) {
	tenant := env.tenant
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/set_tracing_context",
		Description: "Register §8.3 tracing identifiers on a session for propagation through delegation.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","context"],"properties":{"sessionId":{"type":"string"},"context":{"type":"object","additionalProperties":{"type":"string"}}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			SessionID string            `json:"sessionId"`
			Context   map[string]string `json:"context"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		if in.SessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"sessionId is required", nil)
		}
		row, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		if session.IsTerminal(row.State) {
			return mcp.ToolResult{}, errSessionTerminalState(in.SessionID, row.State)
		}
		// §8.3: new entries merge with the inherited context and cannot
		// overwrite or remove existing (parent) entries. Validation runs
		// on the merged result that will be registered.
		merged := tracing.Merge(row.TracingContext, in.Context)
		if err := tracing.Validate(merged); err != nil {
			// spec: §8.3 — a validation failure carries a stable
			// TRACING_CONTEXT_* code. Surface it through *mcp.ToolError so
			// REST and MCP envelopes share the §15.2.1 (category, retryable)
			// pair instead of falling back to INTERNAL_ERROR. F-8.5.17.
			var verr *tracing.ValidationError
			if errors.As(err, &verr) {
				return mcp.ToolResult{}, mcp.NewToolError(string(verr.Code), verr.Detail, nil)
			}
			return mcp.ToolResult{}, err
		}
		updated, err := deps.Store.Update(ctx, tenant, in.SessionID, func(row *sessionstore.Session) error {
			row.TracingContext = merged
			return nil
		})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		body, _ := json.Marshal(struct {
			SessionID      string            `json:"sessionId"`
			TracingContext map[string]string `json:"tracingContext"`
		}{SessionID: updated.ID, TracingContext: updated.TracingContext})
		return textResult(string(body)), nil
	})
}

// registerOutputTool installs lenny/output, the §8.5 agent-output
// surface that publishes output parts onto the session event stream as
// an agent_output event. spec: §8.5 line 544; §15.4.1 lines 1542-1548.
// F-8.5.11 (15.4-HIGH-007).
func registerOutputTool(srv *mcp.Server, deps Deps, env registerEnv) {
	clock := env.clock
	tenant := env.tenant
	srv.RegisterTool(mcp.Tool{
		Name: "lenny/output",
		// spec: §8.5 line 544 — the §8.5 schema lists `output` as the
		// only required input; the session is implicit in the calling
		// principal. The legacy `sessionId` field is accepted as a
		// transport-fallback for tests and dev-headers callers that
		// have not yet bound a session-scoped principal but never
		// participates in `required`. F-8.5.11.
		Description: "Emit output parts to the parent/client (§8.5).",
		InputSchema: json.RawMessage(`{"type":"object","required":["output"],"properties":{"output":{"type":"array","items":{"type":"object"}},"sessionId":{"type":"string","description":"§15.2.1 transport-fallback session id; the principal's SessionID claim takes precedence."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			// SessionID is the transport fallback used when the
			// principal carries no SessionID claim. F-8.5.11.
			SessionID string          `json:"sessionId,omitempty"`
			Output    json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		sessionID := callerSessionID(ctx, in.SessionID)
		if sessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
		}
		var parts []json.RawMessage
		if err := json.Unmarshal(in.Output, &parts); err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				fmt.Sprintf("output must be an array of output parts: %v", err), nil)
		}
		if len(parts) == 0 {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"output must contain at least one part", nil)
		}
		// spec: §15.4.1 lines 1542-1548 — the §4.1 ingress runtime
		// check the messagepart.schema.json $comment defers to the
		// gateway: a part may not carry both `inline` and `ref`
		// (`400 MESSAGEPART_INLINE_REF_CONFLICT`), and a part larger
		// than 50 MB is rejected (`413 MESSAGEPART_TOO_LARGE`). The
		// size gate uses the marshaled part length, which bounds the
		// inline payload plus its envelope; it sits below the §13.4
		// archive ceiling that governs uploads. F-15.4.1 (15.4-HIGH-007).
		for _, part := range parts {
			if err := validateMessagePart(part); err != nil {
				return mcp.ToolResult{}, err
			}
		}
		row, err := deps.Store.Get(ctx, tenant, sessionID)
		if err != nil {
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		if session.IsTerminal(row.State) {
			return mcp.ToolResult{}, errSessionTerminalState(sessionID, row.State)
		}
		// §15.4.1: lenny/output parts are surfaced on the session's
		// event stream as an agent_output event, the same stream the
		// §15.1 GET /v1/sessions/{id}/events SSE relay reads. The
		// reconciliation contract with the terminal `response.output`
		// (so a parent observing both does not double-count) is
		// tracked by the §15.4.1 work for child→parent delivery.
		data, _ := json.Marshal(struct {
			Output []json.RawMessage `json:"output"`
		}{Output: parts})
		deps.Events.Publish(row.ID, "agent_output", string(data), clock())
		return textResult(fmt.Sprintf(`{"emitted":%d}`, len(parts))), nil
	})
}

// registerInputWaitTool installs lenny/request_input, the §8.5/§8.8
// structured-prompt surface that registers a pending input wait and
// surfaces a request_input_expired event on the §9.1 timeout. spec:
// §8.5 line 539, §8.8 line 951.
func registerInputWaitTool(srv *mcp.Server, deps Deps, env registerEnv) {
	clock := env.clock
	idFn := env.idFn
	tenant := env.tenant
	requestInputTimeout := deps.RequestInputTimeout
	if requestInputTimeout <= 0 {
		requestInputTimeout = defaultRequestInputTimeout
	}
	srv.RegisterTool(mcp.Tool{
		Name: "lenny/request_input",
		// spec: §8.5 line 539 / §8.8 line 951 — the §8.5 contract is
		// `lenny/request_input(parts)`; the question travels as an
		// MessagePart[] so an agent can pose a structured prompt
		// (text, JSON-shaped form, etc.) instead of a flat string.
		// `requestId` is optional; when omitted the gateway assigns
		// one and returns it on the resolution. `sessionId` is the
		// transport fallback used when the principal carries no
		// SessionID claim. F-8.5.12.
		Description: "Block until a peer answers via lenny/send_message with a matching inReplyTo (§8.5).",
		InputSchema: json.RawMessage(`{"type":"object","required":["parts"],"properties":{"parts":{"type":"array","items":{"type":"object"},"description":"MessagePart[] describing the structured question."},"requestId":{"type":"string","description":"Optional caller-supplied request id; gateway assigns one when absent."},"sessionId":{"type":"string","description":"§15.2.1 transport-fallback session id; the principal's SessionID claim takes precedence."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			// SessionID is the transport fallback used when the
			// principal carries no SessionID claim. F-8.5.12.
			SessionID string            `json:"sessionId,omitempty"`
			RequestID string            `json:"requestId,omitempty"`
			Parts     []json.RawMessage `json:"parts"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		sessionID := callerSessionID(ctx, in.SessionID)
		if sessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
		}
		if len(in.Parts) == 0 {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"parts is required and must contain at least one MessagePart", nil)
		}
		requestID := in.RequestID
		if requestID == "" {
			requestID = "req_" + idFn()
		}
		row, err := deps.Store.Get(ctx, tenant, sessionID)
		if err != nil {
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		if session.IsTerminal(row.State) {
			return mcp.ToolResult{}, errSessionTerminalState(sessionID, row.State)
		}
		// §11.3 / §5.1 limits.maxRequestInputWaitSeconds: the session's
		// runtime may declare a per-runtime wait cap that overrides the
		// platform default. Resolve the effective runtime so a derived
		// runtime's merged Override value applies. The same lookup
		// resolves the runtime's §5.1 capabilities.interaction so the
		// §8.8 line 869 `one_shot` input-round constraint can fire
		// before a channel is allocated. F-8.8.10.
		callTimeout := requestInputTimeout
		oneShot := false
		if deps.Runtimes != nil && row.RuntimeRef != "" {
			// §5.1 line 49: overlay the session tenant's capability
			// override so a tenant that pinned this runtime to one_shot
			// (or back to multi_turn) governs the §8.8 input-round gate.
			// F-5.1.20.
			if rt, rerr := runtimecapoverride.ResolveForTenant(ctx, deps.Runtimes, deps.CapabilityOverrides, tenant, row.RuntimeRef); rerr == nil {
				if rt.Limits != nil && rt.Limits.MaxRequestInputWaitSeconds > 0 {
					callTimeout = time.Duration(rt.Limits.MaxRequestInputWaitSeconds) * time.Second
				}
				if rt.Capabilities != nil && rt.Capabilities.Interaction == runtimestore.InteractionOneShot {
					oneShot = true
				}
			}
		}
		// spec: §8.8 line 869 — a `one_shot` runtime may use
		// lenny/request_input at most once per task. The second
		// attempt is rejected with ONE_SHOT_INPUT_EXHAUSTED. The
		// gateway enforces the constraint regardless of whether
		// the client read the maxInputRounds annotation. F-8.8.10.
		if oneShot && deps.InputWaits.Consumed(sessionID) >= 1 {
			return mcp.ToolResult{}, mcp.NewToolError("ONE_SHOT_INPUT_EXHAUSTED",
				fmt.Sprintf("one_shot runtime %q has already consumed its single request_input round (§8.8 line 869)", row.RuntimeRef),
				map[string]any{"sessionId": sessionID, "runtimeRef": row.RuntimeRef, "maxInputRounds": 1})
		}
		// spec: §8.8 line 951 — store the question `parts` with the
		// pending registration so a parent's lenny/await_children call
		// can surface them in the input_required partial result without
		// re-fetching the child. F-8.8.5.
		ch, err := deps.InputWaits.Register(sessionID, requestID, in.Parts)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// spec: §7.2 line 136 — surface the question on the session
		// event stream as the canonical `elicitation_request` SSE event.
		// `lenny/request_input` (§8.5) and `lenny/request_elicitation`
		// (§9.2) both ask the user for input and so share the §7.2
		// catalog name. F-7.2.17. The event payload carries the §8.5
		// `parts` array (rather than the legacy flat `prompt`) so
		// rendering surfaces can re-use the same MessagePart visitor
		// the runtime adapter applies. F-8.5.12.
		//
		// spec: §8.8 line 869 — a `one_shot` runtime's
		// elicitation_request carries `metadata.maxInputRounds: 1`
		// so a client surface that renders the SSE payload sees the
		// constraint alongside the question itself. The annotation
		// is informational; enforcement runs above. F-8.8.10.
		if deps.Events != nil {
			payload := struct {
				RequestID string            `json:"requestId"`
				Parts     []json.RawMessage `json:"parts"`
				Metadata  map[string]any    `json:"metadata,omitempty"`
			}{RequestID: requestID, Parts: in.Parts}
			if oneShot {
				payload.Metadata = map[string]any{"maxInputRounds": 1}
			}
			data, _ := json.Marshal(payload)
			deps.Events.Publish(sessionID, "elicitation_request", string(data), clock())
		}
		// Block in the §7.2 input_required sub-state until a peer
		// resolves the request, an external Cancel closes the
		// channel, or the §11.3 timeout fires. A closed channel
		// (ok=false) is the inputwait Registry's cancellation
		// signal; surface it as the canonical
		// REQUEST_INPUT_CANCELLED tool error so the runtime
		// distinguishes a peer-cancelled prompt from a real timeout.
		select {
		case answer, ok := <-ch:
			if !ok {
				return mcp.ToolResult{}, mcp.NewToolError("REQUEST_INPUT_CANCELLED",
					fmt.Sprintf("request_input %s was cancelled", requestID),
					map[string]any{"requestId": requestID, "sessionId": sessionID})
			}
			body, _ := json.Marshal(struct {
				RequestID string `json:"requestId"`
				Answer    string `json:"answer"`
			}{RequestID: requestID, Answer: answer})
			return textResult(string(body)), nil
		case <-time.After(callTimeout):
			deps.InputWaits.Cancel(sessionID, requestID)
			// spec: §15.2.1 / §11.3 line 238 — return the canonical
			// REQUEST_INPUT_TIMEOUT code via *mcp.ToolError so the
			// REST and MCP error envelopes share the same (category,
			// retryable) pair. Details include the absolute
			// `expiredAt` (ISO 8601 UTC) so a runtime can pivot on
			// the same timestamp shape the §11.3 line 238
			// `request_input_expired` await_children event uses. The
			// human-readable Msg preserves the spec reason inline so
			// log scrapers that only read content[0].text still
			// pivot on the code string. F-8.5.10 / F-11.3.23.
			expiredAt := clock().UTC().Format(time.RFC3339Nano)
			// spec: §11.3 line 238 — on a request_input timeout the
			// gateway emits a `request_input_expired` event on the
			// parent's lenny/await_children stream so the awaiting parent
			// can distinguish "child's input request timed out" from
			// "child expired for other reasons". The event is published
			// on the parent session's event stream (the channel the
			// parent observes); the request_input caller is the child, so
			// its ParentSessionID names the awaiter. A root session
			// (no parent) calling request_input has no awaiter, so the
			// publish is skipped. F-11.3.4.
			if deps.Events != nil && row.ParentSessionID != "" {
				evt, _ := json.Marshal(struct {
					Type      string `json:"type"`
					ChildID   string `json:"childId"`
					RequestID string `json:"requestId"`
					ExpiredAt string `json:"expiredAt"`
				}{
					Type:      "request_input_expired",
					ChildID:   sessionID,
					RequestID: requestID,
					ExpiredAt: expiredAt,
				})
				deps.Events.Publish(row.ParentSessionID, "request_input_expired", string(evt), clock())
			}
			return mcp.ToolResult{}, mcp.NewToolError("REQUEST_INPUT_TIMEOUT",
				fmt.Sprintf("REQUEST_INPUT_TIMEOUT: no input arrived for %s within %s (expiredAt=%s)", requestID, callTimeout, expiredAt),
				map[string]any{
					"requestId":      requestID,
					"timeout":        callTimeout.String(),
					"timeoutSeconds": callTimeout.Seconds(),
					"expiredAt":      expiredAt,
				})
		case <-ctx.Done():
			deps.InputWaits.Cancel(sessionID, requestID)
			return mcp.ToolResult{}, ctx.Err()
		}
	})
}

// registerInteractionTools installs the §8.6 elicitation tools:
// request_elicitation, respond_to_elicitation, and dismiss_elicitation.
// spec: §8.6; §9.1 maxElicitationWait; §16.1 lines 60-63. F-9.2.14.
func registerInteractionTools(srv *mcp.Server, deps Deps, env registerEnv) {
	clock := env.clock
	idFn := env.idFn
	tenant := env.tenant
	elicitationTimeout := deps.ElicitationTimeout
	if elicitationTimeout <= 0 {
		elicitationTimeout = defaultElicitationTimeout
	}
	maxElicitations := deps.MaxElicitationsPerSession
	if maxElicitations <= 0 {
		maxElicitations = defaultMaxElicitationsPerSession
	}
	dispatcher := &elicitationDispatcher{
		store:            deps.Store,
		depthPolicy:      deps.ElicitationDepthPolicy,
		suppressAtDepth:  deps.ElicitationSuppressAtDepth,
		urlModeAllowlist: deps.ElicitationURLModeAllowlist,
		intercepts:       deps.ElicitationIntercepts,
		dropMetrics:      deps.ElicitationMetrics,
		tamperMetrics:    deps.ElicitationTamperMetrics,
		// spec: §9.2 lines 58–64 — the dispatcher resolves the tenant's
		// effective content-integrity mode and emits the §16.7 tamper
		// audit event on a divergence. F-9.2.2, F-9.2.3.
		effectiveMode: deps.ElicitationModeResolver,
		audit:         deps.Audit,
		clock:         clock,
	}
	srv.RegisterTool(mcp.Tool{
		Name: "lenny/request_elicitation",
		// spec: §9.2 lines 87–88 — agent-facing lenny/request_elicitation
		// is always treated as agent-initiated. The InitiatorType input
		// was removed to close F-9.2.19: a pod must not be able to
		// self-declare `connector` and bypass the url-mode allowlist.
		// A future gateway-mediated connector path will issue elicitations
		// through a different authenticated surface (the registered
		// connector binding), not by self-assertion at this tool.
		//
		// spec: §8.5 line 559 — the §8.5 JSON Schema lists `schema`
		// AND `message` as required; the session is implicit in the
		// calling principal. `sessionId` is the transport fallback
		// used when the principal carries no SessionID claim.
		// F-8.5.13.
		Description: "Request human input via the §9.2 elicitation chain and block until it resolves.",
		InputSchema: json.RawMessage(`{"type":"object","required":["schema","message"],"properties":{"schema":{"type":"object","description":"JSON Schema describing the input to collect from the user."},"message":{"type":"string","description":"Human-readable prompt displayed to the user."},"elicitationId":{"type":"string"},"url":{"type":"string"},"sessionId":{"type":"string","description":"§15.2.1 transport-fallback session id; the principal's SessionID claim takes precedence."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal so the elicitation budget, lookup, and tamper metric
		// scope to the right tenant in a multi-tenant deployment.
		// F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			// SessionID is the transport fallback used when the
			// principal carries no SessionID claim. F-8.5.13.
			SessionID     string          `json:"sessionId,omitempty"`
			Message       string          `json:"message"`
			Schema        json.RawMessage `json:"schema"`
			ElicitationID string          `json:"elicitationId"`
			// URL is set for a §9.2 url-mode elicitation (an OAuth
			// flow, for example). Empty for a non-url-mode prompt.
			URL string `json:"url"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		sessionID := callerSessionID(ctx, in.SessionID)
		if sessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
		}
		if in.Message == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"message is required (§8.5 line 559)", nil)
		}
		// spec: §8.5 line 559 — `schema` is required. An empty object
		// is acceptable (the agent has declared the input shape is
		// free-form), but the property must be present so renderers
		// can dispatch on the schema type. F-8.5.13.
		if len(in.Schema) == 0 {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"schema is required (§8.5 line 559)", nil)
		}
		row, err := deps.Store.Get(ctx, tenant, sessionID)
		if err != nil {
			return mcp.ToolResult{}, errSessionLookup(err)
		}
		if session.IsTerminal(row.State) {
			return mcp.ToolResult{}, errSessionTerminalState(sessionID, row.State)
		}
		// spec: §11.3 lines 202-203 — maxElicitationWait and
		// maxElicitationsPerSession are configurable per pool in the
		// `limits:` block of the RuntimeDefinition. Resolve the
		// effective runtime so a derived runtime's merged Override value
		// applies, shadowing the platform-default closure values for
		// this call only. Same lookup request_input performs for
		// maxRequestInputWaitSeconds above. F-11.3.6.
		maxElicitations := maxElicitations
		elicitationTimeout := elicitationTimeout
		if deps.Runtimes != nil && row.RuntimeRef != "" {
			if rt, rerr := runtimestore.Resolve(ctx, deps.Runtimes, row.RuntimeRef); rerr == nil && rt.Limits != nil {
				if rt.Limits.MaxElicitationsPerSession > 0 {
					maxElicitations = rt.Limits.MaxElicitationsPerSession
				}
				if rt.Limits.MaxElicitationWaitSeconds > 0 {
					elicitationTimeout = time.Duration(rt.Limits.MaxElicitationWaitSeconds) * time.Second
				}
			}
		}
		// spec: §9.2 line 88 — agent binaries cannot self-declare as a
		// connector. lenny/request_elicitation is the agent surface; an
		// elicitation raised through it is always agent-initiated and
		// must pass the per-pool url-mode allowlist to carry a URL.
		// F-9.2.19.
		initiator := elicitation.InitiatorAgent
		// §9.1: the per-session elicitation budget bounds how many
		// elicitations an agent may raise — an over-budget request
		// is dropped so an agent cannot spam the user.
		count, err := deps.Interactions.CountElicitations(ctx, tenant, sessionID)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		if count >= maxElicitations {
			if deps.ElicitationMetrics != nil {
				deps.ElicitationMetrics.RecordElicitationDrop(elicitationDropBudgetExceeded)
			}
			// spec: §16.1 line 62 — a per-session budget drop is a
			// §9.1 suppression. F-9.2.14.
			if deps.ElicitationLifecycleMetrics != nil {
				deps.ElicitationLifecycleMetrics.IncElicitationSuppressed()
			}
			// spec: §15 catalog (15:988) — a per-session elicitation
			// budget drop is a quota condition; QUOTA_EXCEEDED
			// (POLICY, not retryable) rather than INTERNAL_ERROR.
			// F-15.2.12.
			return mcp.ToolResult{}, mcp.NewToolError("QUOTA_EXCEEDED",
				fmt.Sprintf("elicitation budget exhausted: session %s has reached the maxElicitationsPerSession limit of %d",
					sessionID, maxElicitations), nil)
		}
		// §9.2: the gateway records the original {message, schema}
		// pair at origination. Its SHA-256 digest is the
		// content-integrity reference verified at every forward hop.
		var schemaValue any
		if len(in.Schema) > 0 {
			_ = json.Unmarshal(in.Schema, &schemaValue)
		}
		originalContent := elicitation.Content{Message: in.Message, Schema: schemaValue}
		originalDigest, err := originalContent.Digest()
		if err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				fmt.Sprintf("elicitation content is not canonicalizable: %v", err), nil)
		}
		// spec: §9.2 lines 86, 90-98 — resolve the per-pool depth
		// policy and agent-initiated url-mode allowlist from the
		// raising session's pool so the dispatch applies this pool's
		// configuration rather than a single Register-time platform
		// value. A pool with no explicit policy leaves the dispatcher
		// defaults in place: WalkChain coerces the empty depth policy
		// to the §9.2 line 92 suppress_at_depth=3 default, and the
		// zero-value url-mode allowlist blocks agent-initiated
		// url-mode (the §9.2 default). The dispatcher is copied per
		// call so the override never races the shared registration.
		// F-9.2.12.
		perCall := *dispatcher
		if dp, suppressAt, urlMode, ok := resolvePoolElicitationPolicy(ctx, deps, row.PoolRef); ok {
			perCall.depthPolicy = dp
			perCall.suppressAtDepth = suppressAt
			perCall.urlModeAllowlist = urlMode
		}
		// §9.2: dispatch the elicitation up the hop-by-hop chain. The
		// dispatcher runs the url-mode provenance check, walks the
		// delegation tree from this session upward verifying the
		// content-integrity digest at each forward hop, applies the
		// depth policy, and reports the chain resolver.
		//
		// spec: §16.3 line 350 — the elicitation chain is "Full chain
		// (each hop is a child span)"; this is the gateway hop's span,
		// opened at the request-path entry into the chain. Correlation
		// attributes auto-project from the context; the initiator kind
		// is a non-PII descriptive attribute. The post-dispatch blocking
		// wait for the human response is a separate concern and is not
		// part of this span.
		elicitCtx, elicitSpan := obstracing.NewTracer(nil).Start(ctx, obstracing.SpanMCPElicitation)
		elicitSpan.SetAttributes(attribute.String("mcp.elicitation.initiator", string(initiator)))
		dr, err := perCall.dispatch(elicitCtx, tenant, row, originalContent, initiator, in.URL)
		if err != nil {
			obstracing.RecordError(elicitSpan, err)
			elicitSpan.End()
			return mcp.ToolResult{}, err
		}
		elicitSpan.End()
		if dr.Suppressed {
			// §9.2: a depth-suppressed elicitation returns a SUPPRESSED
			// response the originating pod handles as "user declined".
			if deps.ElicitationMetrics != nil {
				deps.ElicitationMetrics.RecordElicitationDrop(elicitationDropDepthSuppressed)
			}
			// spec: §16.1 line 62 — every §9.2 depth-policy
			// suppression bumps the suppression counter; the metric
			// is what the operator-facing dashboard reads. F-9.2.14.
			if deps.ElicitationLifecycleMetrics != nil {
				deps.ElicitationLifecycleMetrics.IncElicitationSuppressed()
			}
			return textResult(fmt.Sprintf(`{"elicitationId":%q,"suppressed":true}`, elicitationOrGen(in.ElicitationID, idFn))), nil
		}
		elicitationID := in.ElicitationID
		if elicitationID == "" {
			elicitationID = idFn()
		}
		// §9.2: the elicitation is recorded against the chain
		// resolver session — the human-facing edge or an intercepting
		// parent. The §15.1 respond/dismiss authorization triple then
		// targets the resolver, not an intermediate hop.
		resolverSessionID := dr.ResolverSessionID
		// spec: §9.2 lines 70–82 — the gateway stamps the §9.2
		// provenance metadata on the elicitation at origination so
		// client UIs can render it prominently (distinguishing a
		// platform OAuth flow from an agent-initiated prompt) and the
		// §16.7 audit row can source delegation_depth / initiator_type
		// from the stored record. delegation_depth is the origin pod's
		// depth in the §8 task tree (the chain's deepest hop);
		// origin_runtime is the raising session's runtime. The
		// connector-only fields (connector_id, expected_domain,
		// purpose) are empty for an agent-initiated elicitation — the
		// only v1 path through this tool (F-9.2.19). F-9.2.6.
		delegationDepth := 0
		if len(dr.Chain.Hops) > 0 {
			delegationDepth = dr.Chain.Hops[0].Depth
		}
		prov := elicitation.Provenance{
			OriginPod:       row.ID,
			DelegationDepth: delegationDepth,
			OriginRuntime:   row.RuntimeRef,
			InitiatorType:   initiator,
		}
		detail := map[string]any{
			"message": in.Message,
			// §9.2 gateway-origin binding: the recorded digest lets a
			// forward-hop re-emission be verified against the original.
			"contentDigest":   originalDigest,
			"originPod":       prov.OriginPod,
			"initiatorType":   string(prov.InitiatorType),
			"delegationDepth": prov.DelegationDepth,
		}
		if prov.OriginRuntime != "" {
			detail["originRuntime"] = prov.OriginRuntime
		}
		if schemaValue != nil {
			detail["schema"] = schemaValue
		}
		if in.URL != "" {
			detail["url"] = in.URL
		}
		if err := deps.Interactions.Put(ctx, interactionstore.Interaction{
			ID:        elicitationID,
			Kind:      interactionstore.KindElicitation,
			SessionID: resolverSessionID,
			TenantID:  tenant,
			UserID:    row.UserID,
			Phase:     interactionstore.PhasePending,
			Detail:    detail,
		}); err != nil {
			return mcp.ToolResult{}, err
		}
		// spec: §16.1 lines 60–61 — admit-time stamp drives the
		// admit-to-terminal roundtrip histogram and the in-flight
		// pending gauge. Every return path below the put MUST hit
		// the matching Dec / Observe sites. F-9.2.14.
		admittedAt := clock()
		if deps.ElicitationLifecycleMetrics != nil {
			deps.ElicitationLifecycleMetrics.IncElicitationPending()
		}
		recordTerminal := func() {
			if deps.ElicitationLifecycleMetrics == nil {
				return
			}
			deps.ElicitationLifecycleMetrics.DecElicitationPending()
			deps.ElicitationLifecycleMetrics.ObserveElicitationRoundtrip(clock().Sub(admittedAt))
		}
		// spec: §7.2 line 136 — surface the elicitation on the resolver
		// session's event stream as the canonical `elicitation_request`
		// SSE event. The previous `elicitation_requested` synonym was
		// not in the §7.2 catalog; clients filtering on the documented
		// event name silently missed elicitation prompts. F-7.2.17.
		if deps.Events != nil {
			// spec: §9.2 lines 70–82 — the client UI receives the §9.2
			// provenance over the live channel so it can display the
			// origin pod, the initiator type, and the delegation depth
			// alongside the prompt. Without these the UI cannot honour
			// the "users can distinguish platform OAuth flows from
			// agent-initiated prompts" trust requirement. F-9.2.6.
			// spec: §15.2 line 1362 — carry the {message, schema} pair so
			// the MCPAdapter projects the native MCP `elicitation/create`
			// request with `requestedSchema` populated. The schema is the
			// gateway-recorded original (the content-integrity reference),
			// so the wire frame and the §9.2 origin binding agree.
			// F-15.2.13.
			data, _ := json.Marshal(struct {
				ElicitationID   string          `json:"elicitationId"`
				Message         string          `json:"message"`
				Schema          json.RawMessage `json:"schema,omitempty"`
				OriginPod       string          `json:"originPod"`
				InitiatorType   string          `json:"initiatorType"`
				DelegationDepth int             `json:"delegationDepth"`
				OriginRuntime   string          `json:"originRuntime,omitempty"`
			}{
				ElicitationID:   elicitationID,
				Message:         in.Message,
				Schema:          json.RawMessage(in.Schema),
				OriginPod:       prov.OriginPod,
				InitiatorType:   string(prov.InitiatorType),
				DelegationDepth: prov.DelegationDepth,
				OriginRuntime:   prov.OriginRuntime,
			})
			deps.Events.Publish(resolverSessionID, "elicitation_request", string(data), clock())
		}
		// Block until the chain resolver resolves the elicitation or
		// the §9.1 maxElicitationWait timeout fires.
		ticker := time.NewTicker(awaitPollInterval)
		defer ticker.Stop()
		timeout := time.After(elicitationTimeout)
		for {
			cur, err := deps.Interactions.Get(ctx, tenant, resolverSessionID, row.UserID, elicitationID)
			if err != nil {
				recordTerminal()
				return mcp.ToolResult{}, fmt.Errorf("elicitation lookup: %w", err)
			}
			switch cur.Phase {
			case interactionstore.PhaseResponded:
				recordTerminal()
				body, _ := json.Marshal(struct {
					ElicitationID string `json:"elicitationId"`
					Response      any    `json:"response"`
				}{ElicitationID: elicitationID, Response: cur.Response})
				return textResult(string(body)), nil
			case interactionstore.PhaseDismissed:
				recordTerminal()
				return textResult(fmt.Sprintf(`{"elicitationId":%q,"dismissed":true}`, elicitationID)), nil
			}
			select {
			case <-ctx.Done():
				recordTerminal()
				return mcp.ToolResult{}, ctx.Err()
			case <-timeout:
				// §9.1 line 103: on timeout the elicitation is dismissed
				// and the agent receives a structured timeout error. The
				// shared §15.2.1 classifier maps ELICITATION_TIMEOUT to
				// the same (category, retryable) pair on REST and MCP.
				// F-9.2.18.
				_, _ = deps.Interactions.Resolve(ctx, tenant, resolverSessionID, row.UserID, elicitationID,
					func(i *interactionstore.Interaction) error {
						if i.Phase == interactionstore.PhasePending {
							i.Phase = interactionstore.PhaseDismissed
							i.Reason = "ELICITATION_TIMEOUT"
						}
						return nil
					})
				// spec: §16.1 line 63 — record the timeout site so
				// operators can graph the rate. Pair with the
				// admit/decrement bookkeeping. F-9.2.14.
				if deps.ElicitationLifecycleMetrics != nil {
					deps.ElicitationLifecycleMetrics.IncElicitationTimeout()
				}
				recordTerminal()
				expiredAt := clock().UTC().Format(time.RFC3339Nano)
				return mcp.ToolResult{}, mcp.NewToolError("ELICITATION_TIMEOUT",
					fmt.Sprintf("no response for %s within %s (expiredAt=%s)", elicitationID, elicitationTimeout, expiredAt),
					map[string]any{
						"elicitationId":  elicitationID,
						"timeoutSeconds": elicitationTimeout.Seconds(),
						"timeout":        elicitationTimeout.String(),
						"expiredAt":      expiredAt,
					})
			case <-ticker.C:
			}
		}
	})

	// spec: §9.2 line 108 — respond_to_elicitation/dismiss_elicitation
	// are MCP-callable resolution surfaces parallel to the §15.1 REST
	// endpoints. The gateway validates the (session_id, user_id,
	// elicitation_id) triple before routing the response down the
	// chain; any mismatch collapses to ELICITATION_NOT_FOUND so the
	// existence of another session's or user's elicitation never
	// leaks. F-9.2.17.
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/respond_to_elicitation",
		Description: "Respond to a pending §9.2 elicitation on the calling session.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","elicitationId","response"],"properties":{"sessionId":{"type":"string"},"elicitationId":{"type":"string"},"response":{}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			SessionID     string          `json:"sessionId"`
			ElicitationID string          `json:"elicitationId"`
			Response      json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				fmt.Sprintf("invalid arguments: %v", err), nil)
		}
		if in.SessionID == "" || in.ElicitationID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"sessionId and elicitationId are required", nil)
		}
		var response any
		if len(in.Response) > 0 {
			_ = json.Unmarshal(in.Response, &response)
		}
		return resolveElicitationTool(ctx, deps, tenant, in.SessionID,
			in.ElicitationID, interactionstore.PhaseResponded, response, "")
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/dismiss_elicitation",
		Description: "Dismiss a pending §9.2 elicitation on the calling session.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","elicitationId"],"properties":{"sessionId":{"type":"string"},"elicitationId":{"type":"string"},"reason":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			SessionID     string `json:"sessionId"`
			ElicitationID string `json:"elicitationId"`
			Reason        string `json:"reason"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				fmt.Sprintf("invalid arguments: %v", err), nil)
		}
		if in.SessionID == "" || in.ElicitationID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"sessionId and elicitationId are required", nil)
		}
		return resolveElicitationTool(ctx, deps, tenant, in.SessionID,
			in.ElicitationID, interactionstore.PhaseDismissed, nil, in.Reason)
	})
}

// registerRuntimeDiscoveryTools installs the §8.2/§10 runtime-discovery
// tools: discover_agents and list_runtimes, each filtered by the
// caller's effective §10.6 environment access and §8.2 delegation
// policy. spec: §8.2, §10.6.
func registerRuntimeDiscoveryTools(srv *mcp.Server, deps Deps, env registerEnv) {
	tenant := env.tenant
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/discover_agents",
		Description: "List the agent runtimes available as §8 delegation targets, filtered by the caller's effective §8.3 delegation policy.",
		// spec: §8.3 line 244 — discovery returns only the targets
		// the calling session's effective DelegationPolicy
		// authorizes. The session is resolved from the caller's
		// principal; the optional `sessionId` field is the
		// transport fallback for dev/test callers without a bound
		// principal, the same posture the other §8.5 tools take.
		// F-8.5.7.
		InputSchema: json.RawMessage(`{"type":"object","properties":{"nameContains":{"type":"string"},"sessionId":{"type":"string","description":"§15.2.1 transport-fallback caller session id; the principal's SessionID claim takes precedence. Resolves the §8.3 effective delegation policy that scopes the result."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §15.2 line 1335 — tenant from the caller's
		// principal so the §8.3 effective-policy lookup stays scoped
		// to the right tenant. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			NameContains string `json:"nameContains"`
			SessionID    string `json:"sessionId"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, errInvalidArgs(err)
			}
		}
		// §8.5: discovery returns `type: agent` runtimes only —
		// `type: mcp` runtimes are never delegation targets. The
		// store filter also drops soft-deleted runtimes.
		runtimes, err := deps.Runtimes.List(ctx, runtimestore.ListFilter{Type: runtimestore.TypeAgent})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// §10.6: narrow the list to the runtimes the caller's
		// environment membership authorizes.
		runtimes, err = filterByEnvironmentAccess(ctx, deps, runtimes)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// spec: §8.3 line 244 — narrow the discoverable set to the
		// agents the caller's effective DelegationPolicy authorizes.
		// A caller whose session resolves no policy (or no
		// Delegation service is wired) sees every
		// environment-authorized agent. F-8.5.7.
		runtimes, err = filterByEffectiveDelegationPolicy(ctx, deps, tenant, callerSessionID(ctx, in.SessionID), runtimes)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		needle := strings.ToLower(in.NameContains)
		agents := make([]discoveredAgent, 0, len(runtimes))
		for _, rt := range runtimes {
			if needle != "" && !strings.Contains(strings.ToLower(rt.Name), needle) {
				continue
			}
			agents = append(agents, discoveredAgent{
				Name:             rt.Name,
				IntegrationLevel: string(rt.IntegrationLevel),
				Description:      rt.Description,
			})
		}
		body, _ := json.Marshal(struct {
			Agents []discoveredAgent `json:"agents"`
		}{Agents: agents})
		return textResult(string(body)), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/list_runtimes",
		Description: "List the runtimes available to the caller (§9.1 discovery).",
		// spec: §10.6 line 672 — optional `environmentId` stub
		// narrows the discovery view to one named environment. The
		// v1 stub is non-promoting: a runtime the §10.6 transparent
		// filter already excluded stays excluded. F-10.6.10.
		InputSchema: json.RawMessage(`{"type":"object","properties":{"nameContains":{"type":"string"},"environmentId":{"type":"string","description":"§10.6 v1 stub: narrow discovery to runtimes admitted by this environment's runtimeSelector."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			NameContains  string `json:"nameContains"`
			EnvironmentID string `json:"environmentId"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, errInvalidArgs(err)
			}
		}
		// §9.1 discovery lists every runtime type; the store filter
		// drops soft-deleted rows.
		runtimes, err := deps.Runtimes.List(ctx, runtimestore.ListFilter{})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// §5.1: a derived runtime is reported as its effective
		// merged definition so discovery shows the fields it
		// inherits from its base.
		runtimes = resolveDerivedRuntimes(ctx, deps.Runtimes, runtimes)
		// §9.1: discovery is identity-filtered by §10.6 environment
		// access — a not-authorized runtime is simply absent, so the
		// response does not enable enumeration.
		runtimes, err = filterByEnvironmentAccess(ctx, deps, runtimes)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// spec: §10.6 line 672 — `environmentId` stub narrows to one
		// environment when supplied; unknown environment collapses
		// the result to empty so a typo never broadens visibility.
		// F-10.6.10. A connection on the /mcp/environments/{name}
		// surface supplies the scope when the call omits it. F-10.6.11.
		envID := in.EnvironmentID
		if envID == "" {
			envID = environmentmw.ExplicitEnvironmentFromContext(ctx)
		}
		if envID != "" {
			runtimes = narrowRuntimesToEnvironment(ctx, deps, runtimes, envID)
		}
		needle := strings.ToLower(in.NameContains)
		out := make([]discoveredRuntime, 0, len(runtimes))
		for _, rt := range runtimes {
			if needle != "" && !strings.Contains(strings.ToLower(rt.Name), needle) {
				continue
			}
			out = append(out, discoveredRuntime{
				Name:              rt.Name,
				Type:              string(rt.Type),
				IntegrationLevel:  string(rt.IntegrationLevel),
				Description:       rt.Description,
				AgentInterface:    rt.AgentInterface,
				PublishedMetadata: runtimestore.PublicMetadataRefs(rt.PublishedMetadata),
				McpEndpoint:       mcpEndpointFor(rt),
			})
		}
		body, _ := json.Marshal(struct {
			Runtimes            []discoveredRuntime  `json:"runtimes"`
			AdapterCapabilities adapter.Capabilities `json:"adapterCapabilities"`
		}{Runtimes: out, AdapterCapabilities: mcpAdapterCapabilities()})
		return textResult(string(body)), nil
	})
}

// registerDelegationTool installs lenny/delegate_task, the §8.2
// recursive-delegation surface that spawns a child session under a
// running parent. spec: §8.2 lines 12-34; §11.5 line 277
// (idempotencyKey). F-11.5.1, F-11.5.6.
func registerDelegationTool(srv *mcp.Server, deps Deps, env registerEnv) {
	tenant := env.tenant
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/delegate_task",
		Description: "Spawn a child session under a running parent (§8.2 recursive delegation).",
		// spec: §11.5 line 277 — `idempotencyKey` (optional, ≤128
		// runes) collapses retries of SpawnChild to one execution;
		// SpawnChild is one of the six §11.5 critical operations and
		// the MCP path is its only client surface. spec: F-11.5.1,
		// F-11.5.6.
		// spec: §8.2 lines 12-34 — the normative signature is
		// `lenny/delegate_task(target: string, task: TaskSpec,
		// lease_slice?: LeaseSlice)`. `target` is the opaque target id
		// (the runtime never learns whether it resolves to a standalone
		// runtime, a derived runtime, or an external registered agent).
		// `task.input` is a MessagePart[] envelope and
		// `task.workspaceFiles.export` carries the §8.7 export specs. No
		// per-call `maxDepth`: the effective ceiling is resolved at lease
		// issuance via the §8.2.bis precedence chain (F-8.2.6).
		InputSchema: json.RawMessage(`{"type":"object","required":["parentSessionId","target"],"properties":{"parentSessionId":{"type":"string"},"target":{"type":"string","description":"§8.2 opaque delegation target id. The runtime does not know whether it resolves to a standalone runtime, a derived runtime, or an external registered agent; the gateway resolves it server-side. A type:mcp target is rejected with target_not_an_agent."},"poolRef":{"type":"string"},"task":{"type":"object","description":"§8.2 TaskSpec (delegation subset).","properties":{"input":{"type":"array","description":"§15.4.1 MessagePart[] task input delivered to the child as its first message.","items":{"type":"object","required":["type"],"properties":{"type":{"type":"string"},"mimeType":{"type":"string"},"inline":{"type":"string"},"ref":{"type":"string"}}}},"workspaceFiles":{"type":"object","properties":{"export":{"type":"array","items":{"type":"object","required":["source"],"properties":{"source":{"type":"string"},"destPrefix":{"type":"string"}}},"description":"§8.7 export entries: each source glob is resolved inside the parent's /workspace/current and the matched files are rebased under destPrefix in the child workspace."}}}}},"approvalMode":{"type":"string","enum":["policy","approval","deny"],"description":"§8.4 closed enum on the delegation lease. Omit for the spec default (policy)."},"credentialPropagation":{"type":"string","enum":["inherit","independent","deny"],"description":"§8.3 credential propagation mode on the delegation lease. Omit for the default (independent)."},"treeVisibility":{"type":"string","enum":["full","parent-and-self","self-only"],"description":"§8.5 lease visibility boundary controlling lenny/get_task_tree. Omit to inherit the parent's effective value. A value broader than the parent's effective visibility is rejected with TREE_VISIBILITY_WEAKENING."},"idempotencyKey":{"type":"string","maxLength":128,"description":"§11.5 idempotency key: a duplicate request with the same key (within 24h) replays the cached child session result without re-executing."},"fileExportLimits":{"type":"object","properties":{"maxFiles":{"type":"integer"},"maxTotalSize":{"type":"integer"}},"description":"§8.3 fileExportLimits ceiling on the task.workspaceFiles.export set. Omit for the defaults (100 files, 100 MiB)."},"leaseSlice":{"type":"object","properties":{"maxTokenBudget":{"type":"integer"},"maxChildrenTotal":{"type":"integer"},"maxTreeSize":{"type":"integer"},"maxTreeMemoryBytes":{"type":"integer"},"maxParallelChildren":{"type":"integer"},"perChildMaxAge":{"type":"integer"}},"description":"§8.2 lease_slice: the per-subtree resource ceiling for the child. Each axis may only tighten the parent's granted budget; a slice exceeding the parent's remaining budget on any axis is rejected with BUDGET_EXHAUSTED. Omit for no explicit budget binding."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal so the §4 chain payload, §8.2 service Delegate, and
		// §16.7 audit emission all stamp the right tenant.
		// F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
		var in struct {
			ParentSessionID string `json:"parentSessionId"`
			// Target is the §8.2 opaque delegation target id. The
			// runtime never learns whether it resolves to a standalone
			// runtime, a derived runtime, or an external registered
			// agent; resolveDelegationTarget performs the resolution
			// server-side. F-8.2.1.
			Target  string `json:"target"`
			PoolRef string `json:"poolRef"`
			// Task is the §8.2 TaskSpec delegation subset: the input
			// MessagePart[] and the §8.7 workspaceFiles.export specs.
			// F-8.2.1.
			Task struct {
				Input          []sessionrecord.MessagePart `json:"input"`
				WorkspaceFiles struct {
					Export []struct {
						Source     string `json:"source"`
						DestPrefix string `json:"destPrefix"`
					} `json:"export"`
				} `json:"workspaceFiles"`
			} `json:"task"`
			// ApprovalMode is the §8.4 lease enum forwarded
			// verbatim onto the delegation Request; the service
			// validates the closed enum and short-circuits on
			// "deny" before any side effects. F-8.4.1, F-8.4.2.
			ApprovalMode string `json:"approvalMode,omitempty"`
			// CredentialPropagation is the §8.3 credential propagation
			// mode forwarded verbatim onto the delegation Request. The
			// empty string defaults to independent; the service and the
			// MCP boundary validate the closed enum and reject any other
			// value with INVALID_LEASE_FIELD before any side effects.
			// spec: §8.3.
			CredentialPropagation string `json:"credentialPropagation,omitempty"`
			// TreeVisibility is the §8.5 lease visibility boundary
			// forwarded onto the delegation Request. Empty inherits the
			// parent's effective value; a value broader than the
			// parent's is rejected with TREE_VISIBILITY_WEAKENING.
			// F-8.5.2 / F-8.9.2 / F-13.5.8.
			TreeVisibility string `json:"treeVisibility,omitempty"`
			// IdempotencyKey is read by the MCP idempotency hook
			// before the handler runs and is intentionally accepted
			// + ignored here. The hook is wired only on the
			// client-facing edge /mcp surface, so it collapses
			// client-facing gateway-failover and retry duplicates. It
			// does not run on the intra-pod platform-tool path a
			// restored parent re-issues delegate_task over, so it does
			// not deduplicate a spawn re-derived across an unplanned
			// parent restore; that duplicate subtree is the unmitigated
			// at-least-once residual documented in §8.10 "Duplicate
			// spawn across parent recovery". spec: §11.5 line 277,
			// §8.10; F-11.5.1, F-11.5.6.
			IdempotencyKey string `json:"idempotencyKey,omitempty"`
			// FileExportLimits is the optional §8.3 line 264 ceiling on
			// the task.workspaceFiles.export set; omit for the spec
			// defaults. F-8.7.1.
			FileExportLimits *struct {
				MaxFiles     int   `json:"maxFiles"`
				MaxTotalSize int64 `json:"maxTotalSize"`
			} `json:"fileExportLimits,omitempty"`
			// LeaseSlice is the §8.2 lines 38-48 lease_slice the caller
			// declares on the child lease. The service validates it
			// against the parent's granted slice and rejects an
			// over-budget request with BUDGET_EXHAUSTED. Omit for no
			// budget binding. F-8.2.1 / F-8.2.2.
			LeaseSlice *struct {
				MaxTokenBudget      int64 `json:"maxTokenBudget"`
				MaxChildrenTotal    int   `json:"maxChildrenTotal"`
				MaxTreeSize         int   `json:"maxTreeSize"`
				MaxTreeMemoryBytes  int64 `json:"maxTreeMemoryBytes"`
				MaxParallelChildren int   `json:"maxParallelChildren"`
				PerChildMaxAge      int   `json:"perChildMaxAge"`
			} `json:"leaseSlice,omitempty"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		// spec: §11.4 — hard_disable "also block[s] new delegated
		// tasks". The in-session delegation path (lenny/delegate_task)
		// is otherwise ungated: the REST session-creation gate never
		// runs here. Resolve the parent session's owning user and
		// reject when it is no longer active. F-11.4.1.
		if err := requireActiveDelegator(ctx, deps, tenant, in.ParentSessionID); err != nil {
			return mcp.ToolResult{}, err
		}
		// §8.4: validate the closed enum at the MCP boundary so a
		// malformed value (typo, casing, post-v1 mode) is rejected
		// with INVALID_LEASE_FIELD before the parent lookup runs.
		// The service repeats the check as defence-in-depth.
		// F-8.4.2.
		if err := lease.ValidateApprovalMode(lease.ApprovalMode(in.ApprovalMode)); err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("INVALID_LEASE_FIELD",
				err.Error(),
				map[string]any{"field": "approvalMode", "value": in.ApprovalMode})
		}
		// §8.3: validate the credentialPropagation closed enum at the MCP
		// boundary so a malformed value (typo, casing, post-v1 mode) is
		// rejected with INVALID_LEASE_FIELD before the parent lookup
		// runs. An empty value (the independent default) is valid. The
		// service repeats the check as defence-in-depth.
		if err := lease.ValidateCredentialPropagation(lease.CredentialPropagation(in.CredentialPropagation)); err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("INVALID_LEASE_FIELD",
				err.Error(),
				map[string]any{"field": "credentialPropagation", "value": in.CredentialPropagation})
		}
		// §8.5: validate the treeVisibility closed enum at the MCP
		// boundary so a typo is rejected with INVALID_LEASE_FIELD
		// before the parent lookup. An empty value (inherit) is valid.
		// The service repeats the check as defence-in-depth.
		// F-8.5.2 / F-8.9.2.
		if in.TreeVisibility != "" && !session.TreeVisibility(in.TreeVisibility).IsValid() {
			return mcp.ToolResult{}, mcp.NewToolError("INVALID_LEASE_FIELD",
				fmt.Sprintf("treeVisibility %q is not a recognised §8.5 value (full, parent-and-self, self-only)", in.TreeVisibility),
				map[string]any{"field": "treeVisibility", "value": in.TreeVisibility})
		}
		// spec: §8.2 lines 12-23 — `target` is the opaque delegation
		// target id. The gateway resolves it server-side into a
		// concrete runtime reference and an internal kind; the runtime
		// never learns whether the target was a standalone runtime, a
		// derived runtime, or an external registered agent. F-8.2.1.
		if in.Target == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"target is required",
				map[string]any{"field": "target"})
		}
		targetRef, targetKind := resolveDelegationTarget(ctx, deps, in.Target)
		// spec: §8.2 line 50 — `lenny/delegate_task` rejects
		// `type: mcp` targets with `target_not_an_agent`. The check
		// runs before the §10.6 scope filter so a caller cannot
		// reach an MCP-only runtime even if it happens to share
		// the caller's environment.
		if targetKind == targetKindMCP {
			// spec: §15.2.1 — surface the canonical lenny code via
			// *mcp.ToolError so REST and MCP envelopes share the
			// same (category, retryable) pair. F-8.5.10.
			return mcp.ToolResult{}, mcp.NewToolError("TARGET_NOT_AN_AGENT",
				fmt.Sprintf("target_not_an_agent: delegation target %q is a type:mcp runtime (§8.2 line 50)", in.Target),
				map[string]any{"target": in.Target})
		}
		// §10.6: the delegation target must be within the caller's
		// environment scope — the same transparent-filter boundary
		// lenny/discover_agents applies, enforced so the resolved
		// target cannot reach an out-of-scope runtime.
		authorized, err := runtimeAuthorizedForCaller(ctx, deps, targetRef)
		if err != nil {
			return mcp.ToolResult{}, err
		}
		viaCrossEnv := false
		if !authorized {
			// §10.6: a runtime outside the caller's environment scope
			// may still be reachable through a bilateral
			// cross-environment-delegation declaration from the parent
			// session's environment.
			reachable, err := crossEnvReachable(ctx, deps, tenant, in.ParentSessionID, targetRef)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			authorized = reachable
			viaCrossEnv = reachable
		}
		if !authorized {
			// §10.6: a target outside the effective delegation scope
			// is rejected with the TARGET_NOT_IN_SCOPE reason. spec:
			// §15.2.1 — surface the canonical lenny code via
			// *mcp.ToolError so REST and MCP envelopes share the same
			// (category, retryable) pair. F-8.5.10.
			return mcp.ToolResult{}, mcp.NewToolError("TARGET_NOT_IN_SCOPE",
				fmt.Sprintf("target_not_in_scope: delegation target %q is not within the caller's environment scope (§10.6)", in.Target),
				map[string]any{"target": in.Target})
		}
		// §4 PreDelegation: run the interceptor chain over the
		// TaskSpec.input before the gateway processes the delegation.
		// A REJECT blocks the delegation; a MODIFY rewrites the input
		// the child receives. The chain payload is the task input
		// only — delegation metadata (target, poolRef) is structurally
		// immutable because it is not in the payload. The §8.2
		// MessagePart[] input is flattened to its text projection for
		// the interceptor content and child delivery. F-8.2.1.
		taskInput := flattenTaskInput(in.Task.Input)
		if taskInput != "" && deps.Interceptors != nil {
			req := interceptor.Request{
				Phase:     interceptor.PhasePreDelegation,
				SessionID: in.ParentSessionID,
				TenantID:  tenant,
				Content:   []byte(taskInput),
			}
			// spec: §8.3 lines 157-188 / §4.8 line 1036 / §13.5
			// mitigation 2 — resolve the parent's effective
			// contentPolicy.interceptorRef and run only that named
			// external content scanner at PreDelegation, alongside the
			// built-in DelegationPolicyEvaluator (maxInputSize). External
			// interceptors the policy does not name are not invoked; a
			// policy with interceptorRef: null runs no external scan.
			// F-8.2.9 / F-13.5.2.
			var res interceptor.Result
			if deps.ContentPolicies != nil {
				ref := ""
				if _, r, ok := deps.ContentPolicies.ResolveContentPolicy(ctx, tenant, in.ParentSessionID); ok {
					ref = r
				}
				res = deps.Interceptors.RunPolicyScoped(ctx, req, ref)
			} else {
				res = deps.Interceptors.Run(ctx, req)
			}
			if res.Action == interceptor.ActionReject {
				recordChainRejection(ctx, deps, tenant, in.ParentSessionID, interceptor.PhasePreDelegation, res)
				// spec: §15.2.1 line 1386 — a manual MCP-only tool
				// (lenny/delegate_task) must use the shared error
				// taxonomy. A built-in evaluator that names a canonical
				// §15.1 code on its Result (e.g. the §4.8
				// DelegationPolicyEvaluator returns INPUT_TOO_LARGE for
				// a §8.3 contentPolicy.maxInputSize overflow) surfaces
				// that code so REST and MCP envelopes share the same
				// (category, retryable) pair; a plain REJECT with no
				// code falls back to INTERCEPTOR_REJECTED
				// (CategoryPolicy / retryable:false). F-15.2.11 /
				// F-13.5.1 / F-8.2.9.
				code := "INTERCEPTOR_REJECTED"
				if res.Code != "" {
					code = res.Code
				}
				return mcp.ToolResult{}, mcp.NewToolError(code,
					res.Reason,
					map[string]any{"phase": string(interceptor.PhasePreDelegation)})
			}
			if res.Action == interceptor.ActionModify {
				taskInput = string(res.ModifiedContent)
			}
		}
		// §8.2 line 90: after PreDelegation passes, the gateway runs
		// the PreRoute chain over the child's augmented TaskSpec — the
		// same chain that fires during top-level session creation. A
		// REJECT blocks the delegation; a MODIFY rewrites the input the
		// child receives. The chain rejects any MODIFY that alters the
		// authenticated identity (tenant_id/user_id) with
		// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION before it is seen here.
		if deps.Interceptors != nil && deps.Interceptors.Len(interceptor.PhasePreRoute) > 0 {
			spec := childRouteSpec{
				TenantID:         tenant,
				RequestedRuntime: targetRef,
				Input:            taskInput,
			}
			payload, merr := json.Marshal(spec)
			if merr != nil {
				return mcp.ToolResult{}, fmt.Errorf("child task spec serialization: %w", merr)
			}
			res := deps.Interceptors.Run(ctx, interceptor.Request{
				Phase:     interceptor.PhasePreRoute,
				SessionID: in.ParentSessionID,
				TenantID:  tenant,
				Content:   payload,
				Metadata:  map[string]string{"tenant_id": tenant},
			})
			if res.Action == interceptor.ActionReject {
				recordChainRejection(ctx, deps, tenant, in.ParentSessionID, interceptor.PhasePreRoute, res)
				// spec: §15.2.1 line 1386 — see PreDelegation site
				// above. A deliberate PreRoute reject falls back to
				// INTERCEPTOR_REJECTED, preserving REST/MCP (category,
				// retryable) parity. An immutable-field violation
				// carries its own §15.1 code
				// (INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION) plus the
				// violated_fields detail the §4.8/§15.1 envelope
				// mandates. F-15.2.11.
				// spec: §4.8, §15.1
				code := res.Code
				if code == "" {
					code = "INTERCEPTOR_REJECTED"
				}
				details := map[string]any{
					"phase":           string(interceptor.PhasePreRoute),
					"interceptor_ref": res.RejectedBy,
				}
				if res.Code == interceptor.CodeInterceptorImmutableFieldViolation {
					details["violated_fields"] = res.ViolatedFields
				}
				return mcp.ToolResult{}, mcp.NewToolError(code, res.Reason, details)
			}
			if res.Action == interceptor.ActionModify {
				var modified childRouteSpec
				if uerr := json.Unmarshal(res.ModifiedContent, &modified); uerr != nil {
					return mcp.ToolResult{}, fmt.Errorf("child PreRoute MODIFY is not a valid task spec: %w", uerr)
				}
				taskInput = modified.Input
			}
		}
		// §8.7: translate the §8.2 task.workspaceFiles.export entries
		// into the delegation Request. The Service runs the export
		// materializer when the slice is non-empty. F-8.7.1 / F-8.2.1.
		var fileExport []export.Spec
		for _, e := range in.Task.WorkspaceFiles.Export {
			fileExport = append(fileExport, export.Spec{Source: e.Source, DestPrefix: e.DestPrefix})
		}
		var fileExportLimits fileexport.FileExportLimits
		if in.FileExportLimits != nil {
			fileExportLimits = fileexport.FileExportLimits{
				MaxFiles:     in.FileExportLimits.MaxFiles,
				MaxTotalSize: in.FileExportLimits.MaxTotalSize,
			}
		}
		// §8.2 lines 38-48: forward the caller-declared lease_slice
		// so the service validates it against the parent's granted
		// budget. Omitted (nil) leaves the zero slice (no budget
		// binding). F-8.2.1 / F-8.2.2.
		var leaseSlice lease.LeaseSlice
		if in.LeaseSlice != nil {
			leaseSlice = lease.LeaseSlice{
				MaxTokenBudget:      in.LeaseSlice.MaxTokenBudget,
				MaxChildrenTotal:    in.LeaseSlice.MaxChildrenTotal,
				MaxTreeSize:         in.LeaseSlice.MaxTreeSize,
				MaxTreeMemoryBytes:  in.LeaseSlice.MaxTreeMemoryBytes,
				MaxParallelChildren: in.LeaseSlice.MaxParallelChildren,
				PerChildMaxAge:      in.LeaseSlice.PerChildMaxAge,
			}
		}
		// §8.2 line 59: build the RFC 8693 actor_token material from
		// the authenticated principal — the parent pod's session
		// token. The service runs the in-process child-token exchange
		// over it (narrow scope, build the act chain, fix
		// delegation_depth, read the parent jti against the §13.3
		// revocation cache). Absent under the dev-headers path (no
		// principal), which leaves the exchange leg unrun.
		// F-8.1.2 / F-8.2.7.
		var parentToken *delegation.ParentToken
		if p, ok := authmw.FromContext(ctx); ok && p.Subject != "" {
			var parentScope []string
			for _, sc := range p.Scopes.Scopes() {
				parentScope = append(parentScope, sc.String())
			}
			parentToken = &delegation.ParentToken{
				Subject:    p.Subject,
				SessionID:  in.ParentSessionID,
				JTI:        p.JTI,
				Scope:      parentScope,
				CallerType: p.CallerType,
			}
		}
		res, err := deps.Delegation.Delegate(ctx, tenant, delegation.Request{
			ParentSessionID: in.ParentSessionID,
			// §8.2: the opaque target is resolved to a concrete runtime
			// reference server-side. MaxDepth is intentionally unset —
			// the effective ceiling is resolved at lease issuance via
			// the §8.2.bis precedence chain (F-8.2.6), not supplied
			// per-call by the runtime. F-8.2.1.
			RuntimeRef:       targetRef,
			PoolRef:          in.PoolRef,
			IsolationProfile: resolvePoolIsolation(ctx, deps, in.PoolRef),
			ApprovalMode:     lease.ApprovalMode(in.ApprovalMode),
			// §8.3: the credential propagation mode forwarded onto the
			// child lease. Empty defaults to independent. On a
			// cross-environment inherit hop the service runs the
			// provider-compatibility check before claiming a pod.
			CredentialPropagation: lease.CredentialPropagation(in.CredentialPropagation),
			LeaseSlice:            leaseSlice,
			ParentToken:           parentToken,
			FileExport:            fileExport,
			FileExportLimits:      fileExportLimits,
			// §8.3 lines 311-319: the lease visibility boundary. Empty
			// inherits the parent's effective value in the Service.
			// EffectiveMessagingScope is left unset (resolves to the
			// §7.2 default `direct`); the deployment/tenant/runtime
			// messagingScope resolver is the separate §7.2 concern.
			// F-8.5.2 / F-8.9.2 / F-13.5.8.
			TreeVisibility: session.TreeVisibility(in.TreeVisibility),
		})
		if err != nil {
			// §10.6 / §8.3: a SEC-001 isolation-monotonicity failure
			// is surfaced under the spec's ISOLATION_MONOTONICITY_VIOLATED
			// reason so the caller can distinguish it, and the §10.6
			// delegation.isolation_violation audit event is emitted.
			var isoErr *delegation.IsolationViolationError
			if errors.As(err, &isoErr) {
				if deps.Audit != nil {
					// spec: §11.7 lines 99-101 — the §11.7 delegation
					// payload schema names parent_isolation /
					// target_isolation / matched_policy_rule. v1
					// rejection happens before the §8.3
					// DelegationPolicy registry is consulted, so
					// matched_policy_rule is emitted as the empty
					// string until F-8.5.7 lands the policy-scoped
					// filtering and can attribute the matching rule.
					// §8.4 / F-8.4.3: the declared approvalMode rides
					// the rejection audit so the post-incident replay
					// can distinguish whether the violation occurred
					// under a policy or v1 `approval` (aliased)
					// authorisation.
					declaredApproval := in.ApprovalMode
					if declaredApproval == "" {
						declaredApproval = string(lease.ApprovalModePolicy)
					}
					// §8.3 / F-8.4.3: the declared credentialPropagation
					// mode rides the rejection audit alongside approval_mode
					// so the post-incident replay records how the rejected
					// hop would have credentialed the child. An omitted
					// value is recorded as the independent default.
					declaredPropagation := in.CredentialPropagation
					if declaredPropagation == "" {
						declaredPropagation = string(lease.CredentialPropagationIndependent)
					}
					deps.Audit.EmitDelegationEvent(ctx, "delegation.isolation_violation", map[string]any{
						"parentSessionId":        in.ParentSessionID,
						"runtimeRef":             targetRef,
						"poolRef":                in.PoolRef,
						"parent_isolation":       string(isoErr.ParentProfile),
						"target_isolation":       string(isoErr.ChildProfile),
						"matched_policy_rule":    "",
						"cross_environment":      viaCrossEnv,
						"approval_mode":          declaredApproval,
						"credential_propagation": declaredPropagation,
					})
				}
				// spec: §15.2.1 — return *mcp.ToolError so the REST and
				// MCP envelopes share the same (category, retryable)
				// pair. F-8.5.10.
				return mcp.ToolResult{}, mcp.NewToolError("ISOLATION_MONOTONICITY_VIOLATED",
					fmt.Sprintf("ISOLATION_MONOTONICITY_VIOLATED: %s", err.Error()),
					map[string]any{
						"parent_isolation": string(isoErr.ParentProfile),
						"target_isolation": string(isoErr.ChildProfile),
					})
			}
			// spec: §8.2 "Cycle-detection decision matrix" — a
			// self-recursive hop the three-layer AND gate rejected.
			// §15.2.1 line 1395 (item 4) requires every manual
			// MCP-only tool, including lenny/delegate_task, to use
			// the shared error taxonomy rather than a bare error
			// that falls through to INTERNAL_ERROR. `details`
			// carries blockedBy (the first false layer in canonical
			// platform -> runtime -> policy order), effectiveSettings
			// (the resolved {mode, platform, runtime, policy} tuple),
			// and the offending identity. F-8.2 cycle detection.
			var cycleErr *cycle.Rejection
			if errors.As(err, &cycleErr) {
				return mcp.ToolResult{}, mcp.NewToolError("DELEGATION_CYCLE_DETECTED",
					err.Error(),
					map[string]any{
						"blockedBy":        string(cycleErr.BlockedBy),
						"cycleRuntimeName": cycleErr.CycleRuntimeName,
						"cyclePoolName":    cycleErr.CyclePoolName,
						"effectiveSettings": map[string]any{
							"mode":     string(cycleErr.EffectiveSettings.Mode),
							"platform": cycleErr.EffectiveSettings.PlatformAllowSelfRec,
							"runtime":  cycleErr.EffectiveSettings.RuntimeAllowSelfRec,
							"policy":   cycleErr.EffectiveSettings.PolicyAllowSelfRec,
						},
					})
			}
			// spec: §8.2 "2a-bis. Effective maxDepth resolution
			// (normative, always enforced)" — every effective
			// delegation lease carries a positive integer maxDepth
			// enforced on every hop; pkg/delegation/lease.CheckDepth
			// rejects a hop that would push the chain past it. §15.2.1
			// line 1395 (item 4) requires the shared error taxonomy
			// rather than the INTERNAL_ERROR fallback a bare error
			// produces.
			var depthErr *lease.DepthExceededError
			if errors.As(err, &depthErr) {
				return mcp.ToolResult{}, mcp.NewToolError("DELEGATION_DEPTH_EXCEEDED",
					err.Error(),
					map[string]any{"current": depthErr.Current, "max": depthErr.Max})
			}
			// §8.2 line 58: the child-token exchange requires the
			// parent's authenticated user JWT as `subject_token`. A
			// userless parent is surfaced under a distinct reason so
			// the caller can distinguish "missing user identity" from
			// the generic delegation failure path.
			if errors.Is(err, delegation.ErrParentNoUser) {
				return mcp.ToolResult{}, mcp.NewToolError("DELEGATION_PARENT_NO_USER",
					fmt.Sprintf("DELEGATION_PARENT_NO_USER: %s", err.Error()), nil)
			}
			// §11.1 line 9: the owning user already holds the maximum
			// count of live delegated children across all their
			// sessions. Surface the canonical QUOTA_EXCEEDED (the same
			// code the §11.1 concurrent-session caps return) carrying
			// the breached scope so the caller distinguishes a per-user
			// admission cap from the per-tree BUDGET_EXHAUSTED. F-11.1.4.
			if errors.Is(err, delegation.ErrUserChildrenExhausted) {
				return mcp.ToolResult{}, mcp.NewToolError("QUOTA_EXCEEDED",
					err.Error(),
					map[string]any{"scope": "user", "control": "active_delegated_children"})
			}
			// §8.4 line 521: an `approvalMode: "deny"` lease is
			// the platform-provided mechanism for an operator to opt
			// a lease out of delegation entirely. spec: §15.2.1 —
			// surface the canonical lenny code via *mcp.ToolError
			// so REST and MCP envelopes share the same (category,
			// retryable) pair. F-8.4.1.
			if errors.Is(err, delegation.ErrDelegationDenied) {
				return mcp.ToolResult{}, mcp.NewToolError("DELEGATION_DENIED",
					err.Error(),
					map[string]any{"approvalMode": in.ApprovalMode})
			}
			// §8.4: a structurally invalid approvalMode survives the
			// MCP-layer validator only if the service is invoked
			// from another code path. Map the typed error so the
			// MCP envelope is the canonical INVALID_LEASE_FIELD.
			// F-8.4.2.
			var iameErr *lease.InvalidApprovalModeError
			if errors.As(err, &iameErr) {
				return mcp.ToolResult{}, mcp.NewToolError("INVALID_LEASE_FIELD",
					err.Error(),
					map[string]any{"field": "approvalMode", "value": iameErr.Value})
			}
			// §8.3: a structurally invalid credentialPropagation survives
			// the MCP-layer validator only if the service is invoked from
			// another code path. Map the typed error so the MCP envelope
			// is the canonical INVALID_LEASE_FIELD.
			var icpErr *lease.InvalidCredentialPropagationError
			if errors.As(err, &icpErr) {
				return mcp.ToolResult{}, mcp.NewToolError("INVALID_LEASE_FIELD",
					err.Error(),
					map[string]any{"field": "credentialPropagation", "value": icpErr.Value})
			}
			// spec: §8.2 lines 38-48, 127 — the caller's lease_slice
			// exceeds the parent's granted budget on at least one
			// axis. Surface the canonical BUDGET_EXHAUSTED envelope so
			// REST and MCP share the same (category, retryable) pair,
			// carrying the per-axis violations the service reported.
			// F-8.2.2.
			var budgetErr *lease.BudgetExceededError
			if errors.As(err, &budgetErr) {
				return mcp.ToolResult{}, mcp.NewToolError("BUDGET_EXHAUSTED",
					err.Error(),
					map[string]any{"violations": budgetErr.Violations})
			}
			// spec: §12.4 line 213 — the §12.4 Redis-backed delegation
			// tree budget counters could not be consulted (outage or
			// script error). The admission path fails closed: surface
			// the retryable DELEGATION_BUDGET_UNAVAILABLE so the caller
			// retries the whole delegate_task once Redis recovers,
			// rather than admitting an unbudgeted child. F-8.2.18.
			if errors.Is(err, delegation.ErrBudgetUnavailable) {
				return mcp.ToolResult{}, mcp.NewToolError("DELEGATION_BUDGET_UNAVAILABLE",
					err.Error(), nil)
			}
			// spec: §8.2 line 61 — the §13.3 actor-token freshness
			// check found the parent token revoked mid-flight (the
			// parent was rotated or recursively revoked between this
			// call and the in-process child-token exchange). No child
			// pod is allocated; surface the canonical
			// DELEGATION_PARENT_REVOKED. F-8.1.2 / F-8.2.7.
			if errors.Is(err, delegation.ErrParentRevoked) {
				return mcp.ToolResult{}, mcp.NewToolError("DELEGATION_PARENT_REVOKED",
					err.Error(), map[string]any{"parentSessionId": in.ParentSessionID})
			}
			// spec: §8.2 line 63 — the per-tenant audit advisory lock
			// timed out during the child-token exchange. Surface the
			// retryable DELEGATION_AUDIT_CONTENTION; the parent agent
			// retries the entire lenny/delegate_task so the full
			// admission pipeline (including the freshness check)
			// re-runs. F-8.1.2 / F-8.2.7.
			if errors.Is(err, delegation.ErrAuditContention) {
				return mcp.ToolResult{}, mcp.NewToolError("DELEGATION_AUDIT_CONTENTION",
					err.Error(), nil)
			}
			// spec: §8.2 line 50 — the service-layer defence-in-depth
			// type gate that mirrors the §10.6 shim. Surface the
			// canonical code so REST and MCP envelopes match.
			if errors.Is(err, delegation.ErrTargetNotAgent) {
				return mcp.ToolResult{}, mcp.NewToolError("TARGET_NOT_AN_AGENT",
					err.Error(), map[string]any{"target": in.Target})
			}
			// spec: §8.3 line 181 — the resolved DelegationPolicy
			// is inside the cluster-scoped scanExportedFiles
			// weakening cooldown. Map the typed error to the
			// canonical INTERCEPTOR_WEAKENING_COOLDOWN envelope
			// (TRANSIENT, HTTP 503) so callers and §15.2.1 parity
			// scanners observe the same `(category, retryable)`
			// pair across REST and MCP. F-8.7.12 / F-13.5.7.
			var cdErr *delegation.InterceptorWeakeningCooldownError
			if errors.As(err, &cdErr) {
				return mcp.ToolResult{}, cooldownToolError(cdErr)
			}
			// spec: §8.3 lines 313-317 — the child lease's treeVisibility
			// widens the parent's effective visibility. Surface the
			// canonical TREE_VISIBILITY_WEAKENING (POLICY, HTTP 422) with
			// both sides of the mismatch so REST and MCP envelopes share
			// the same (category, retryable) pair. F-8.5.2 / F-13.5.8.
			var tvwErr *delegation.TreeVisibilityWeakeningError
			if errors.As(err, &tvwErr) {
				return mcp.ToolResult{}, mcp.NewToolError("TREE_VISIBILITY_WEAKENING",
					err.Error(),
					map[string]any{
						"parentTreeVisibility": string(tvwErr.ParentVisibility),
						"childTreeVisibility":  string(tvwErr.ChildVisibility),
					})
			}
			// spec: §8.3 lines 157-187 — the child's resolved
			// contentPolicy weakens the parent's effective policy on one
			// of the inheritance axes (maxInputSize, maxExportedFileSize,
			// scanExportedFiles true→false, or interceptorRef
			// non-null→null). Surface the canonical CONTENT_POLICY_WEAKENING
			// (POLICY, HTTP 422) carrying the offending axis so REST and
			// MCP envelopes share the same (category, retryable) pair.
			// F-13.5.10.
			var cpwErr *delegation.ContentPolicyWeakeningError
			if errors.As(err, &cpwErr) {
				return mcp.ToolResult{}, mcp.NewToolError("CONTENT_POLICY_WEAKENING",
					err.Error(),
					map[string]any{
						"axis":        cpwErr.Axis,
						"parentValue": cpwErr.ParentValue,
						"childValue":  cpwErr.ChildValue,
					})
			}
			// spec: §8.3 line 188 — the child names a different non-null
			// interceptorRef than the parent's; the substitution cannot be
			// verified as equally restrictive and is rejected
			// unconditionally. Surface the canonical
			// CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION. F-13.5.10.
			var cpsErr *delegation.ContentPolicyInterceptorSubstitutionError
			if errors.As(err, &cpsErr) {
				return mcp.ToolResult{}, mcp.NewToolError("CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION",
					err.Error(),
					map[string]any{
						"parentInterceptorRef": cpsErr.ParentRef,
						"childInterceptorRef":  cpsErr.ChildRef,
					})
			}
			// spec: §8.3 lines 321-324 — the child's effective
			// messagingScope is `siblings` but its effective
			// treeVisibility is not `full`. Surface the canonical
			// TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE with the
			// resolved scope, resolved visibility, and the required
			// `full` value. F-13.5.8.
			var tvmErr *delegation.TreeVisibilityMessagingScopeError
			if errors.As(err, &tvmErr) {
				return mcp.ToolResult{}, mcp.NewToolError("TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE",
					err.Error(),
					map[string]any{
						"effectiveMessagingScope": string(tvmErr.EffectiveMessagingScope),
						"effectiveTreeVisibility": string(tvmErr.EffectiveTreeVisibility),
						"requiredTreeVisibility":  string(session.VisibilityFull),
					})
			}
			// §8.7 / §8.3: a fileExport that cannot be materialized
			// because the export engine is unconfigured, or a
			// scanExportedFiles policy with no resolvable interceptor,
			// fails closed. Surface the canonical codes so REST and MCP
			// envelopes share the same (category, retryable) pair.
			// F-8.7.1.
			if errors.Is(err, delegation.ErrExportNotConfigured) {
				return mcp.ToolResult{}, mcp.NewToolError("EXPORT_NOT_CONFIGURED",
					err.Error(), nil)
			}
			if errors.Is(err, delegation.ErrExportScanUnavailable) {
				return mcp.ToolResult{}, mcp.NewToolError("EXPORT_FILE_SCAN_UNAVAILABLE",
					err.Error(), nil)
			}
			return mcp.ToolResult{}, err
		}
		// Deliver the (possibly interceptor-modified) task input to
		// the child as its first message.
		if taskInput != "" && deps.Executor != nil {
			if _, err := deps.Executor.Send(ctx, res.Child.ID, []executor.Message{
				{Role: "user", Content: taskInput},
			}); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("child %s created but task input delivery failed: %w",
					res.Child.ID, err)
			}
		}
		handle := taskHandle{
			ChildSessionID: res.Child.ID,
			State:          string(res.Child.State),
			RuntimeRef:     res.Child.RuntimeRef,
			Depth:          res.Depth,
		}
		body, merr := json.Marshal(handle)
		if merr != nil {
			return mcp.ToolResult{}, fmt.Errorf("task handle serialization: %w", merr)
		}
		return textResult(string(body)), nil
	})
}
