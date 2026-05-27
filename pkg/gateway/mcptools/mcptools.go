// SPDX-License-Identifier: MIT

// Package mcptools wires the §8.5 platform MCP tool surface to the
// gateway's session + delegation services. It is the bridge between
// the transport-only pkg/gateway/mcp adapter and the concrete
// gateway operations, kept separate so the MCP adapter has no
// dependency on the session store.
//
// v1 registers the core §8.5 tools:
//
//   - `lenny/create_session`      — create a session.
//   - `lenny/send_message`        — deliver a message to a session.
//   - `lenny/get_task_tree`       — read the §8 delegation task tree.
//   - `lenny/cancel_child`        — cancel a child session and cascade.
//   - `lenny/await_children`      — wait for child sessions to settle.
//   - `lenny/discover_agents`     — list §8 delegation targets.
//   - `lenny/list_runtimes`       — §9.1 runtime discovery.
//   - `lenny/set_tracing_context` — register §8.3 tracing identifiers.
//   - `lenny/output`              — emit output parts to the event stream.
//   - `lenny/request_input`       — block until a peer provides input.
//   - `lenny/request_elicitation` — block until a human responds (§9.2).
//   - `lenny/delegate_task`       — spawn a child session (§8.2).
//
// Each handler runs the same validation as the equivalent REST
// endpoint so the REST and MCP surfaces stay in lockstep per the
// §15.2.1 consistency contract.
package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/tracing"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/envaccess"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// DelegationAuditor records §11.7 audit events for delegation
// operations such as the §10.6 isolation-monotonicity violation.
type DelegationAuditor interface {
	EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any)
}

// Deps carries the gateway services the MCP tools dispatch to.
type Deps struct {
	// Store is the §4.2 session store.
	Store sessionstore.Store

	// Executor routes messages to runtimes.
	Executor executor.Executor

	// Delegation is the §8 delegation service. Optional — when nil,
	// the lenny/delegate_task tool is not registered.
	Delegation *delegation.Service

	// Runtimes is the §5.1 runtime registry. Optional — when nil, the
	// lenny/discover_agents tool is not registered.
	Runtimes runtimestore.Store

	// Environments is the §10.6 environment registry. Optional — when
	// set together with Tenants, lenny/discover_agents applies §10.6
	// transparent filtering, returning only the agent runtimes the
	// caller's environment membership authorizes.
	Environments environmentstore.Store

	// Tenants is the §10.2 tenant registry. Optional — consulted with
	// Environments to read the caller tenant's noEnvironmentPolicy for
	// the §10.6 transparent-filtering fallback.
	Tenants tenantstore.Store

	// Pools is the §5.2 pool registry. Optional — when set,
	// lenny/delegate_task resolves the child pool's §5.3 isolation
	// profile so the §8.3 monotonicity check evaluates the child pool
	// rather than the profile inherited from the parent session.
	Pools poolstore.Store

	// Audit records §11.7 delegation audit events. Optional — a nil
	// Audit disables delegation audit emission.
	Audit DelegationAuditor

	// DefaultNoEnvironmentPolicy is the §10.6 platform-wide
	// noEnvironmentPolicy applied to a caller whose tenant has set no
	// per-tenant value. The per-tenant value takes precedence; an empty
	// default is treated as deny-all by the resolver.
	DefaultNoEnvironmentPolicy string

	// Interceptors is the §4 RequestInterceptor chain. Optional — when
	// non-nil, lenny/send_message runs the PreMessageDelivery phase
	// over the message body before delivery.
	Interceptors *interceptor.Chain

	// PolicyAudit emits the §16.7 interceptor.rejected audit row when a
	// chain REJECTs at PreDelegation or PreMessageDelivery. Optional —
	// when nil, a chain REJECT still blocks the request but writes no
	// audit row. spec: §4.8 line 981, §11.7.
	PolicyAudit *policy.AuditSink

	// Events is the §15.1 session event bus. Optional — when nil, the
	// lenny/output tool is not registered.
	Events *sessionevents.Bus

	// InputWaits is the §8.5 lenny/request_input pending-call registry.
	// Optional — when nil, lenny/request_input is not registered.
	InputWaits *inputwait.Registry

	// TreeArchive is the §8.10 session_tree_archive. Optional — when
	// non-nil, lenny/cancel_child archives each cancelled child's
	// §8.8 TaskResult so a resumed parent can replay it.
	TreeArchive treearchive.Store

	// RequestInputTimeout caps a lenny/request_input block per the
	// §11.3 maxRequestInputWaitSeconds limit. Zero selects the default.
	RequestInputTimeout time.Duration

	// Interactions is the §9.2 pending-interaction store backing
	// lenny/request_elicitation. Optional — when nil, the tool is not
	// registered.
	Interactions interactionstore.Store

	// Memory is the §9.4 MemoryStore backing lenny/memory_write and
	// lenny/memory_query. Optional — when nil, those tools are not
	// registered.
	Memory memorystore.Store

	// ElicitationTimeout caps a lenny/request_elicitation block per the
	// §9.1 maxElicitationWait limit. Zero selects the default.
	ElicitationTimeout time.Duration

	// MaxElicitationsPerSession caps a session's lifetime elicitation
	// count per §9.1. Zero selects the default.
	MaxElicitationsPerSession int

	// ElicitationMetrics, when set, receives a §9.1 drop event for
	// every elicitation the gateway drops. Optional.
	ElicitationMetrics ElicitationDropRecorder

	// ElicitationTamperMetrics, when set, receives a §9.2 / §16.5
	// content-tamper-detected notification each time the chain walk
	// catches a forwarding hop that mutated the elicitation payload.
	// *gatewaymetrics.Metrics satisfies the
	// ElicitationTamperRecorder interface.
	ElicitationTamperMetrics ElicitationTamperRecorder

	// ElicitationDepthPolicy is the §9.2 depth policy applied to an
	// agent-initiated elicitation. An unset or invalid value resolves
	// to `allow_all` (no depth suppression).
	ElicitationDepthPolicy elicitation.DepthPolicy

	// ElicitationSuppressAtDepth is the §9.2 delegation depth at which
	// the `suppress_at_depth` policy starts suppressing elicitations.
	ElicitationSuppressAtDepth int

	// ElicitationURLModeAllowlist is the §9.2 per-pool agent-initiated
	// url-mode elicitation allowlist. The zero value blocks every
	// agent-initiated url-mode elicitation, which is the §9.2 default.
	ElicitationURLModeAllowlist elicitation.URLModeAllowlist

	// ElicitationIntercepts, when non-nil, reports whether an ancestor
	// session on the §9.2 elicitation chain is configured to intercept
	// the elicitation rather than forward it onward. A nil predicate
	// means no parent intercepts — every elicitation forwards up the
	// task tree to the human-facing edge.
	ElicitationIntercepts func(sess sessionstore.Session) bool

	// Clock + IDFunc match the session server's construction; pass
	// nil for production defaults.
	Clock  func() time.Time
	IDFunc func() string

	// TenantID is the tenant the MCP session operates within. The
	// MCP adapter is mounted per-tenant; v1 binds one tenant per
	// adapter instance.
	TenantID string

	// DevMode is the platform global.devMode (LENNY_DEV_MODE=true). When
	// true, a session created without an explicit isolation profile
	// defaults to `standard` (runc) per §5.3 line 677 rather than the
	// production `sandboxed`.
	DevMode bool
}

// defaultRequestInputTimeout is the §11.3 maxRequestInputWaitSeconds
// default applied when Deps.RequestInputTimeout is zero.
const defaultRequestInputTimeout = 600 * time.Second

// defaultElicitationTimeout is the §9.1 maxElicitationWait default
// applied when Deps.ElicitationTimeout is zero.
const defaultElicitationTimeout = 600 * time.Second

// defaultMaxElicitationsPerSession is the §9.1 per-session elicitation
// budget applied when Deps.MaxElicitationsPerSession is zero.
const defaultMaxElicitationsPerSession = 50

// §9.1 lenny_elicitation_dropped_total `reason` label values.
const (
	// elicitationDropBudgetExceeded — the per-session budget rejected
	// the request.
	elicitationDropBudgetExceeded = "budget_exceeded"
	// elicitationDropDepthSuppressed — the §9.2 depth policy suppressed
	// the request.
	elicitationDropDepthSuppressed = "depth_suppressed"
)

// ElicitationDropRecorder records a §9.1 elicitation drop for the
// lenny_elicitation_dropped_total counter. pkg/gateway/gatewaymetrics
// satisfies it.
type ElicitationDropRecorder interface {
	RecordElicitationDrop(reason string)
}

// Register installs the §8.5 tools onto the MCP server.
func Register(srv *mcp.Server, deps Deps) {
	clock := deps.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	idFn := deps.IDFunc
	if idFn == nil {
		idFn = randomSessionID
	}
	tenant := deps.TenantID
	if tenant == "" {
		tenant = "default"
	}

	// §4.8 PreToolResult: install the transport-layer hook that runs the
	// interceptor chain over every tool result before it reaches the
	// agent. A nil chain leaves the hook unset. spec: §4.8 line 1053.
	if ri := buildPreToolResultInterceptor(deps, tenant); ri != nil {
		srv.SetResultInterceptor(ri)
	}

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/create_session",
		Description: "Create a new agent session against a runtime.",
		// spec: §11.5 line 277 — `idempotencyKey` (optional, ≤128 runes)
		// collapses retries of CreateSession to one execution; identical
		// retries replay the cached ToolResult, mismatched bodies are
		// rejected with IDEMPOTENCY_KEY_REUSED. spec: F-11.5.1.
		InputSchema: json.RawMessage(`{"type":"object","required":["runtimeRef"],"properties":{"runtimeRef":{"type":"string"},"userId":{"type":"string"},"environment":{"type":"string"},"idempotencyKey":{"type":"string","maxLength":128,"description":"§11.5 idempotency key: a duplicate request with the same key (within 24h) replays the cached result without re-executing."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			RuntimeRef  string `json:"runtimeRef"`
			UserID      string `json:"userId"`
			Environment string `json:"environment"`
			// IdempotencyKey is read by the MCP idempotency hook before
			// the handler runs and is intentionally ignored here so a
			// stray field on a non-idempotency-aware deployment is just
			// dropped instead of producing a validation error. spec:
			// §11.5 line 277; F-11.5.1.
			IdempotencyKey string `json:"idempotencyKey,omitempty"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("INVALID_REQUEST",
				fmt.Sprintf("invalid arguments: %v", err), nil)
		}
		if in.RuntimeRef == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"runtimeRef is required",
				map[string]any{"field": "runtimeRef"})
		}
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
		// F-8.5.16 (rename), F-7.2.22 (fromSessionId).
		InputSchema: json.RawMessage(`{"type":"object","required":["to","message"],"properties":{"to":{"type":"string","description":"Target taskId / sessionId (§8.5 line 537)."},"message":{"type":"string","description":"Message content (§8.5 line 537)."},"inReplyTo":{"type":"string","description":"§8.8 line 951 — when this answers a pending lenny/request_input, the matching requestId."},"messageId":{"type":"string","description":"§15.4 line 1784 sender-supplied id; gateway assigns one when absent."},"fromSessionId":{"type":"string","description":"§7.2 sender session id. When set (or implied by the principal), the gateway enforces the §7.2 line 240 topology constraint: target must be the sender's parent, direct child, or sibling. F-7.2.22."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			// To is the §8.5 target session id (renamed from the legacy
			// `sessionId` to match the §8.5 line 537 schema). F-8.5.16.
			To string `json:"to"`
			// Message is the §8.5 content field (renamed from the legacy
			// `content` to match the §8.5 line 537 schema). F-8.5.16.
			Message   string `json:"message"`
			InReplyTo string `json:"inReplyTo"`
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
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.To == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"to is required (§8.5 line 537)", nil)
		}
		row, err := deps.Store.Get(ctx, tenant, in.To)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		if session.IsTerminal(row.State) {
			return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", in.To, row.State)
		}
		// spec: §7.2 line 240 (`direct` and `siblings` messagingScope)
		// and §7.2 line 373 (parent communication asymmetry) — restrict
		// the target to a parent/child/sibling of the declared sender.
		// The principal's SessionID claim is the canonical sender id;
		// the legacy `fromSessionId` field is the transport fallback
		// for callers that have not yet bound a principal. F-7.2.22,
		// F-8.5.16.
		senderID := callerSessionID(ctx, in.FromSessionID)
		if senderID != "" {
			sender, err := deps.Store.Get(ctx, tenant, senderID)
			if err != nil {
				return mcp.ToolResult{}, mcp.NewToolError("SCOPE_DENIED",
					fmt.Sprintf("sender session %s not found", senderID), nil)
			}
			if !withinMessagingTopology(sender, row) {
				return mcp.ToolResult{}, mcp.NewToolError("SCOPE_DENIED",
					fmt.Sprintf("target %s is not a parent, direct child, or sibling of sender %s",
						in.To, senderID), nil)
			}
		}
		// spec: §15.4 line 1784 — the gateway assigns a `msg_` prefix
		// id when the sender omits one so every receipt is
		// correlatable. F-7.2.10.
		messageID := in.MessageID
		if messageID == "" {
			messageID = "msg_" + idFn()
		}
		// §8.5: when the message answers a pending lenny/request_input
		// call, it resolves that blocked call directly instead of being
		// delivered to the runtime. A non-matching inReplyTo falls
		// through to normal delivery — it is then an ordinary threaded
		// message.
		if in.InReplyTo != "" && deps.InputWaits != nil {
			err := deps.InputWaits.Resolve(in.To, in.InReplyTo, in.Message)
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
		if deps.Executor == nil {
			return mcp.ToolResult{}, errors.New("no executor configured")
		}
		// §4 PreMessageDelivery: run the interceptor chain over the
		// message body before delivery. A REJECT blocks the message; a
		// MODIFY rewrites what the target session receives.
		messageBody := in.Message
		if deps.Interceptors != nil {
			res := deps.Interceptors.Run(ctx, interceptor.Request{
				Phase:     interceptor.PhasePreMessageDelivery,
				SessionID: row.ID,
				TenantID:  tenant,
				Content:   []byte(in.Message),
			})
			if res.Action == interceptor.ActionReject {
				recordChainRejection(ctx, deps, tenant, row.ID, interceptor.PhasePreMessageDelivery, res)
				return mcp.ToolResult{}, fmt.Errorf("message delivery rejected by policy: %s", res.Reason)
			}
			if res.Action == interceptor.ActionModify {
				messageBody = string(res.ModifiedContent)
			}
		}
		out, err := deps.Executor.Send(ctx, row.ID, []executor.Message{
			{Role: "user", Content: messageBody},
		})
		if err != nil {
			return mcp.ToolResult{}, err
		}
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

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/get_task_tree",
		Description: "Return the §8 delegation task tree rooted at a session.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		root, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		all, err := deps.Store.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		tree := buildTree(root, all)
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
		var in struct {
			// ParentSessionID is the transport fallback used when the
			// principal carries no SessionID claim. F-8.5.15.
			ParentSessionID string `json:"parentSessionId,omitempty"`
			ChildSessionID  string `json:"childSessionId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
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
			return mcp.ToolResult{}, errors.New("a session cannot cancel itself as its own child")
		}
		child, err := deps.Store.Get(ctx, tenant, in.ChildSessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("child session lookup: %w", err)
		}
		all, err := deps.Store.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			return mcp.ToolResult{}, err
		}
		// Authorization: the caller may cancel only sessions inside its
		// own §8 delegation subtree.
		if !isDescendant(child, parentSessionID, all) {
			return mcp.ToolResult{}, fmt.Errorf("session %s is not a child of %s",
				in.ChildSessionID, parentSessionID)
		}
		if session.IsTerminal(child.State) {
			return mcp.ToolResult{}, fmt.Errorf("child session %s is already terminal (%s)",
				in.ChildSessionID, child.State)
		}
		cancelled, err := cancelSubtree(ctx, deps.Store, tenant, child, all)
		if err != nil {
			return mcp.ToolResult{}, err
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
		var in struct {
			SessionID string   `json:"sessionId"`
			ChildIDs  []string `json:"childIds"`
			Mode      string   `json:"mode"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.SessionID == "" || len(in.ChildIDs) == 0 {
			return mcp.ToolResult{}, errors.New("sessionId and a non-empty childIds are required")
		}
		mode := in.Mode
		if mode == "" {
			mode = "all"
		}
		if mode != "all" && mode != "any" && mode != "settled" {
			return mcp.ToolResult{}, fmt.Errorf("mode %q is not one of all, any, or settled", mode)
		}
		if _, err := deps.Store.Get(ctx, tenant, in.SessionID); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		// Authorization: every awaited id must be a direct child of the
		// caller — a session may only await children it delegated. A
		// child whose live row is gone is resolved from the §8.10
		// archive so a resumed parent can still re-await it.
		for _, cid := range in.ChildIDs {
			oc, err := resolveChild(ctx, deps.Store, deps.TreeArchive, tenant, cid)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			if oc.parentID != in.SessionID {
				return mcp.ToolResult{}, fmt.Errorf("session %s is not a child of %s", cid, in.SessionID)
			}
		}
		// Poll the child states until the mode's settle condition holds.
		ticker := time.NewTicker(awaitPollInterval)
		defer ticker.Stop()
		for {
			results, settled, err := collectChildResults(ctx, deps.Store, deps.TreeArchive, tenant, in.ChildIDs, mode)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			if settled {
				body, _ := json.Marshal(struct {
					Results []taskResult `json:"results"`
				}{Results: results})
				return textResult(string(body)), nil
			}
			select {
			case <-ctx.Done():
				return mcp.ToolResult{}, ctx.Err()
			case <-ticker.C:
			}
		}
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/set_tracing_context",
		Description: "Register §8.3 tracing identifiers on a session for propagation through delegation.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","context"],"properties":{"sessionId":{"type":"string"},"context":{"type":"object","additionalProperties":{"type":"string"}}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID string            `json:"sessionId"`
			Context   map[string]string `json:"context"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.SessionID == "" {
			return mcp.ToolResult{}, errors.New("sessionId is required")
		}
		row, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		if session.IsTerminal(row.State) {
			return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", in.SessionID, row.State)
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

	if deps.Events != nil {
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
			var in struct {
				// SessionID is the transport fallback used when the
				// principal carries no SessionID claim. F-8.5.11.
				SessionID string          `json:"sessionId,omitempty"`
				Output    json.RawMessage `json:"output"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
			sessionID := callerSessionID(ctx, in.SessionID)
			if sessionID == "" {
				return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
					"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
			}
			var parts []json.RawMessage
			if err := json.Unmarshal(in.Output, &parts); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("output must be an array of output parts: %w", err)
			}
			if len(parts) == 0 {
				return mcp.ToolResult{}, errors.New("output must contain at least one part")
			}
			row, err := deps.Store.Get(ctx, tenant, sessionID)
			if err != nil {
				return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
			}
			if session.IsTerminal(row.State) {
				return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", sessionID, row.State)
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

	if deps.InputWaits != nil {
		requestInputTimeout := deps.RequestInputTimeout
		if requestInputTimeout <= 0 {
			requestInputTimeout = defaultRequestInputTimeout
		}
		srv.RegisterTool(mcp.Tool{
			Name: "lenny/request_input",
			// spec: §8.5 line 539 / §8.8 line 951 — the §8.5 contract is
			// `lenny/request_input(parts)`; the question travels as an
			// OutputPart[] so an agent can pose a structured prompt
			// (text, JSON-shaped form, etc.) instead of a flat string.
			// `requestId` is optional; when omitted the gateway assigns
			// one and returns it on the resolution. `sessionId` is the
			// transport fallback used when the principal carries no
			// SessionID claim. F-8.5.12.
			Description: "Block until a peer answers via lenny/send_message with a matching inReplyTo (§8.5).",
			InputSchema: json.RawMessage(`{"type":"object","required":["parts"],"properties":{"parts":{"type":"array","items":{"type":"object"},"description":"OutputPart[] describing the structured question."},"requestId":{"type":"string","description":"Optional caller-supplied request id; gateway assigns one when absent."},"sessionId":{"type":"string","description":"§15.2.1 transport-fallback session id; the principal's SessionID claim takes precedence."}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				// SessionID is the transport fallback used when the
				// principal carries no SessionID claim. F-8.5.12.
				SessionID string            `json:"sessionId,omitempty"`
				RequestID string            `json:"requestId,omitempty"`
				Parts     []json.RawMessage `json:"parts"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
			sessionID := callerSessionID(ctx, in.SessionID)
			if sessionID == "" {
				return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
					"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
			}
			if len(in.Parts) == 0 {
				return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
					"parts is required and must contain at least one OutputPart", nil)
			}
			requestID := in.RequestID
			if requestID == "" {
				requestID = "req_" + idFn()
			}
			row, err := deps.Store.Get(ctx, tenant, sessionID)
			if err != nil {
				return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
			}
			if session.IsTerminal(row.State) {
				return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", sessionID, row.State)
			}
			// §11.3 / §5.1 limits.maxRequestInputWaitSeconds: the session's
			// runtime may declare a per-runtime wait cap that overrides the
			// platform default. Resolve the effective runtime so a derived
			// runtime's merged Override value applies.
			callTimeout := requestInputTimeout
			if deps.Runtimes != nil && row.RuntimeRef != "" {
				if rt, rerr := runtimestore.Resolve(ctx, deps.Runtimes, row.RuntimeRef); rerr == nil {
					if rt.Limits != nil && rt.Limits.MaxRequestInputWaitSeconds > 0 {
						callTimeout = time.Duration(rt.Limits.MaxRequestInputWaitSeconds) * time.Second
					}
				}
			}
			ch, err := deps.InputWaits.Register(sessionID, requestID)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			// spec: §7.2 line 136 — surface the question on the session
			// event stream as the canonical `elicitation_request` SSE event.
			// `lenny/request_input` (§8.5) and `lenny/request_elicitation`
			// (§9.2) both ask the user for input and so share the §7.2
			// catalog name. F-7.2.17. The event payload carries the §8.5
			// `parts` array (rather than the legacy flat `prompt`) so
			// rendering surfaces can re-use the same OutputPart visitor
			// the runtime adapter applies. F-8.5.12.
			if deps.Events != nil {
				data, _ := json.Marshal(struct {
					RequestID string            `json:"requestId"`
					Parts     []json.RawMessage `json:"parts"`
				}{RequestID: requestID, Parts: in.Parts})
				deps.Events.Publish(sessionID, "elicitation_request", string(data), clock())
			}
			// Block in the §7.2 input_required sub-state until a peer
			// resolves the request or the §11.3 timeout fires.
			select {
			case answer := <-ch:
				body, _ := json.Marshal(struct {
					RequestID string `json:"requestId"`
					Answer    string `json:"answer"`
				}{RequestID: requestID, Answer: answer})
				return textResult(string(body)), nil
			case <-time.After(callTimeout):
				deps.InputWaits.Cancel(sessionID, requestID)
				return mcp.ToolResult{}, fmt.Errorf(
					"REQUEST_INPUT_TIMEOUT: no input arrived for %s within %s", requestID, callTimeout,
				)
			case <-ctx.Done():
				deps.InputWaits.Cancel(sessionID, requestID)
				return mcp.ToolResult{}, ctx.Err()
			}
		})
	}

	if deps.Interactions != nil {
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
			tenantID:         tenant,
			depthPolicy:      deps.ElicitationDepthPolicy,
			suppressAtDepth:  deps.ElicitationSuppressAtDepth,
			urlModeAllowlist: deps.ElicitationURLModeAllowlist,
			intercepts:       deps.ElicitationIntercepts,
			audit:            deps.Audit,
			tamperMetrics:    deps.ElicitationTamperMetrics,
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
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
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
				return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
			}
			if session.IsTerminal(row.State) {
				return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", sessionID, row.State)
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
				return mcp.ToolResult{}, fmt.Errorf(
					"elicitation budget exhausted: session %s has reached the maxElicitationsPerSession limit of %d",
					sessionID, maxElicitations,
				)
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
				return mcp.ToolResult{}, fmt.Errorf("elicitation content is not canonicalizable: %w", err)
			}
			// §9.2: dispatch the elicitation up the hop-by-hop chain. The
			// dispatcher runs the url-mode provenance check, walks the
			// delegation tree from this session upward verifying the
			// content-integrity digest at each forward hop, applies the
			// depth policy, and reports the chain resolver.
			dr, err := dispatcher.dispatch(ctx, row, originalContent, initiator, in.URL)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			if dr.Suppressed {
				// §9.2: a depth-suppressed elicitation returns a SUPPRESSED
				// response the originating pod handles as "user declined".
				if deps.ElicitationMetrics != nil {
					deps.ElicitationMetrics.RecordElicitationDrop(elicitationDropDepthSuppressed)
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
			detail := map[string]any{
				"message": in.Message,
				// §9.2 gateway-origin binding: the recorded digest lets a
				// forward-hop re-emission be verified against the original.
				"contentDigest": originalDigest,
				"originPod":     row.ID,
				"initiatorType": string(initiator),
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
			// spec: §7.2 line 136 — surface the elicitation on the resolver
			// session's event stream as the canonical `elicitation_request`
			// SSE event. The previous `elicitation_requested` synonym was
			// not in the §7.2 catalog; clients filtering on the documented
			// event name silently missed elicitation prompts. F-7.2.17.
			if deps.Events != nil {
				data, _ := json.Marshal(struct {
					ElicitationID string `json:"elicitationId"`
					Message       string `json:"message"`
					OriginPod     string `json:"originPod"`
				}{ElicitationID: elicitationID, Message: in.Message, OriginPod: row.ID})
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
					return mcp.ToolResult{}, fmt.Errorf("elicitation lookup: %w", err)
				}
				switch cur.Phase {
				case interactionstore.PhaseResponded:
					body, _ := json.Marshal(struct {
						ElicitationID string `json:"elicitationId"`
						Response      any    `json:"response"`
					}{ElicitationID: elicitationID, Response: cur.Response})
					return textResult(string(body)), nil
				case interactionstore.PhaseDismissed:
					return textResult(fmt.Sprintf(`{"elicitationId":%q,"dismissed":true}`, elicitationID)), nil
				}
				select {
				case <-ctx.Done():
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
					return mcp.ToolResult{}, mcp.NewToolError("ELICITATION_TIMEOUT",
						fmt.Sprintf("no response for %s within %s", elicitationID, elicitationTimeout),
						map[string]any{
							"elicitationId":  elicitationID,
							"timeoutSeconds": elicitationTimeout.Seconds(),
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
			var in struct {
				SessionID     string          `json:"sessionId"`
				ElicitationID string          `json:"elicitationId"`
				Response      json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, mcp.NewToolError("INVALID_REQUEST",
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
			var in struct {
				SessionID     string `json:"sessionId"`
				ElicitationID string `json:"elicitationId"`
				Reason        string `json:"reason"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, mcp.NewToolError("INVALID_REQUEST",
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

	if deps.Runtimes != nil {
		srv.RegisterTool(mcp.Tool{
			Name:        "lenny/discover_agents",
			Description: "List the agent runtimes available as §8 delegation targets.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"nameContains":{"type":"string"}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				NameContains string `json:"nameContains"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &in); err != nil {
					return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
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
			InputSchema: json.RawMessage(`{"type":"object","properties":{"nameContains":{"type":"string"}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				NameContains string `json:"nameContains"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &in); err != nil {
					return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
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
				})
			}
			body, _ := json.Marshal(struct {
				Runtimes            []discoveredRuntime  `json:"runtimes"`
				AdapterCapabilities adapter.Capabilities `json:"adapterCapabilities"`
			}{Runtimes: out, AdapterCapabilities: mcpAdapterCapabilities()})
			return textResult(string(body)), nil
		})
	}

	if deps.Delegation != nil {
		srv.RegisterTool(mcp.Tool{
			Name:        "lenny/delegate_task",
			Description: "Spawn a child session under a running parent (§8.2 recursive delegation).",
			// spec: §11.5 line 277 — `idempotencyKey` (optional, ≤128
			// runes) collapses retries of SpawnChild to one execution;
			// SpawnChild is one of the six §11.5 critical operations and
			// the MCP path is its only client surface. spec: F-11.5.1,
			// F-11.5.6.
			InputSchema: json.RawMessage(`{"type":"object","required":["parentSessionId","runtimeRef"],"properties":{"parentSessionId":{"type":"string"},"runtimeRef":{"type":"string"},"poolRef":{"type":"string"},"maxDepth":{"type":"integer"},"taskInput":{"type":"string"},"idempotencyKey":{"type":"string","maxLength":128,"description":"§11.5 idempotency key: a duplicate request with the same key (within 24h) replays the cached child session result without re-executing."}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				ParentSessionID string `json:"parentSessionId"`
				RuntimeRef      string `json:"runtimeRef"`
				PoolRef         string `json:"poolRef"`
				MaxDepth        int    `json:"maxDepth"`
				TaskInput       string `json:"taskInput"`
				// IdempotencyKey is read by the MCP idempotency hook
				// before the handler runs and is intentionally accepted
				// + ignored here. spec: §11.5 line 277; F-11.5.1,
				// F-11.5.6.
				IdempotencyKey string `json:"idempotencyKey,omitempty"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
			// spec: §8.2 line 50 — `lenny/delegate_task` rejects
			// `type: mcp` targets with `target_not_an_agent`. The check
			// runs before the §10.6 scope filter so a caller cannot
			// reach an MCP-only runtime even if it happens to share
			// the caller's environment.
			if deps.Runtimes != nil && in.RuntimeRef != "" {
				if rt, err := runtimestore.Resolve(ctx, deps.Runtimes, in.RuntimeRef); err == nil && rt.Type == runtimestore.TypeMCP {
					return mcp.ToolResult{}, fmt.Errorf(
						"target_not_an_agent: delegation target %q is a type:mcp runtime (§8.2 line 50)",
						in.RuntimeRef,
					)
				}
			}
			// §10.6: the delegation target must be within the caller's
			// environment scope — the same transparent-filter boundary
			// lenny/discover_agents applies, enforced so a hard-coded
			// runtimeRef cannot reach an out-of-scope runtime.
			authorized, err := runtimeAuthorizedForCaller(ctx, deps, in.RuntimeRef)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			viaCrossEnv := false
			if !authorized {
				// §10.6: a runtime outside the caller's environment scope
				// may still be reachable through a bilateral
				// cross-environment-delegation declaration from the parent
				// session's environment.
				reachable, err := crossEnvReachable(ctx, deps, tenant, in.ParentSessionID, in.RuntimeRef)
				if err != nil {
					return mcp.ToolResult{}, err
				}
				authorized = reachable
				viaCrossEnv = reachable
			}
			if !authorized {
				// §10.6: a target outside the effective delegation scope
				// is rejected with the target_not_in_scope reason.
				return mcp.ToolResult{}, fmt.Errorf(
					"target_not_in_scope: delegation target %q is not within the caller's environment scope (§10.6)",
					in.RuntimeRef,
				)
			}
			// §4 PreDelegation: run the interceptor chain over the
			// TaskSpec.input before the gateway processes the delegation.
			// A REJECT blocks the delegation; a MODIFY rewrites the input
			// the child receives. The chain payload is the task input
			// only — delegation metadata (runtimeRef, poolRef, maxDepth)
			// is structurally immutable because it is not in the payload.
			taskInput := in.TaskInput
			if taskInput != "" && deps.Interceptors != nil {
				res := deps.Interceptors.Run(ctx, interceptor.Request{
					Phase:     interceptor.PhasePreDelegation,
					SessionID: in.ParentSessionID,
					TenantID:  tenant,
					Content:   []byte(taskInput),
				})
				if res.Action == interceptor.ActionReject {
					recordChainRejection(ctx, deps, tenant, in.ParentSessionID, interceptor.PhasePreDelegation, res)
					return mcp.ToolResult{}, fmt.Errorf("delegation rejected by policy: %s", res.Reason)
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
					RequestedRuntime: in.RuntimeRef,
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
					return mcp.ToolResult{}, fmt.Errorf("delegation rejected by policy: %s", res.Reason)
				}
				if res.Action == interceptor.ActionModify {
					var modified childRouteSpec
					if uerr := json.Unmarshal(res.ModifiedContent, &modified); uerr != nil {
						return mcp.ToolResult{}, fmt.Errorf("child PreRoute MODIFY is not a valid task spec: %w", uerr)
					}
					taskInput = modified.Input
				}
			}
			res, err := deps.Delegation.Delegate(ctx, tenant, delegation.Request{
				ParentSessionID:  in.ParentSessionID,
				RuntimeRef:       in.RuntimeRef,
				PoolRef:          in.PoolRef,
				MaxDepth:         in.MaxDepth,
				IsolationProfile: resolvePoolIsolation(ctx, deps, in.PoolRef),
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
						deps.Audit.EmitDelegationEvent(ctx, "delegation.isolation_violation", map[string]any{
							"parentSessionId":     in.ParentSessionID,
							"runtimeRef":          in.RuntimeRef,
							"poolRef":             in.PoolRef,
							"parent_isolation":    string(isoErr.ParentProfile),
							"target_isolation":    string(isoErr.ChildProfile),
							"matched_policy_rule": "",
							"cross_environment":   viaCrossEnv,
						})
					}
					return mcp.ToolResult{}, fmt.Errorf("ISOLATION_MONOTONICITY_VIOLATED: %w", err)
				}
				// §8.2 line 58: the child-token exchange requires the
				// parent's authenticated user JWT as `subject_token`. A
				// userless parent is surfaced under a distinct reason so
				// the caller can distinguish "missing user identity" from
				// the generic delegation failure path.
				if errors.Is(err, delegation.ErrParentNoUser) {
					return mcp.ToolResult{}, fmt.Errorf("DELEGATION_PARENT_NO_USER: %w", err)
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

	if deps.Memory != nil {
		registerMemoryTools(srv, deps, tenant, clock)
	}
}

// taskHandle is the §8.2 return envelope for lenny/delegate_task. The
// spec frames the signature as `→ TaskHandle`; v1 ships the minimal
// fields callers need to address the child and observe its initial
// state. The envelope is additive-only — new fields go at the end with
// `omitempty` so existing parsers continue to decode older payloads.
type taskHandle struct {
	// ChildSessionID is the §8.8 taskId / sessionId identifying the
	// admitted child session.
	ChildSessionID string `json:"childSessionId"`

	// State is the child's §8.8 task state at admission. v1 returns
	// `created` (the §7 session create state) because §8.2 step 7 (pod
	// allocation + workspace materialization) is unbuilt; once the
	// allocation flow lands, the state will be `submitted` or `running`
	// per §8.8.
	State string `json:"state"`

	// RuntimeRef echoes the resolved §5.1 runtime so the caller can
	// confirm the binding the gateway selected (in case of derived /
	// alias resolution) without a separate GET.
	RuntimeRef string `json:"runtimeRef"`

	// Depth is the child's depth in the §8.2 delegation tree (root = 0).
	// Out-of-spec but stable — useful for the caller to surface the tree
	// position without re-walking the lineage.
	Depth int `json:"depth"`
}

// recordChainRejection emits the §16.7 interceptor.rejected audit row
// childRouteSpec is the §8.2 line 90 PreRoute content payload for a
// delegated child: the augmented TaskSpec the chain inspects before
// runtime selection. The JSON field names match the PreRoute immutable
// fields (tenant_id/user_id) the chain enforces on MODIFY (spec: §4.8
// line 1048).
type childRouteSpec struct {
	TenantID         string `json:"tenant_id"`
	UserID           string `json:"user_id,omitempty"`
	RequestedRuntime string `json:"requested_runtime,omitempty"`
	Input            string `json:"input,omitempty"`
}

// recordChainRejection emits the §16.7 `interceptor.rejected` audit row
// when a §4.8 chain REJECTs at a phase run from the MCP fabric
// (PreDelegation, PreMessageDelivery). The chain stamps Result.RejectedBy
// with the rejecting interceptor so the audit row identifies it. It is
// best-effort: a nil PolicyAudit is a no-op, and an append failure is
// logged-by-omission rather than blocking the rejection, because the
// caller already fails the request closed. spec: §4.8 line 981, §11.7.
func recordChainRejection(ctx context.Context, deps Deps, tenant, sessionID string, phase interceptor.Phase, res interceptor.Result) {
	if deps.PolicyAudit == nil {
		return
	}
	_ = deps.PolicyAudit.RecordRejection(ctx, policy.RejectionContext{
		TenantID:  tenant,
		SessionID: sessionID,
		Phase:     phase,
	}, res)
}

// preToolResultPayload is the §4.8 line 1053 PreToolResult content
// payload: the tool result delivered back to the agent. `id` is the
// originating tool_call.id and is immutable on MODIFY (the chain
// enforces it via phaseImmutableFields); `content` and `isError` are
// mutable. spec: §4.8 line 1053.
type preToolResultPayload struct {
	ID      string            `json:"id"`
	Content []mcp.ToolContent `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

// buildPreToolResultInterceptor returns the mcp.ResultInterceptor that
// runs the §4.8 PreToolResult chain over every tool result before it
// reaches the agent. It returns nil when no chain is configured so the
// transport adapter keeps its zero-cost default. A REJECT (including an
// immutable-id MODIFY violation, which the chain converts to a REJECT)
// surfaces as a tool error; a MODIFY substitutes the rewritten result.
// spec: §4.8 line 1053.
func buildPreToolResultInterceptor(deps Deps, tenant string) mcp.ResultInterceptor {
	if deps.Interceptors == nil {
		return nil
	}
	return func(ctx context.Context, callID, name string, result mcp.ToolResult) (mcp.ToolResult, error) {
		raw, err := json.Marshal(preToolResultPayload{
			ID:      callID,
			Content: result.Content,
			IsError: result.IsError,
		})
		if err != nil {
			return result, nil
		}
		res := deps.Interceptors.Run(ctx, interceptor.Request{
			Phase:    interceptor.PhasePreToolResult,
			TenantID: tenant,
			Content:  raw,
			Metadata: map[string]string{"tool_name": name},
		})
		switch res.Action {
		case interceptor.ActionReject:
			recordChainRejection(ctx, deps, tenant, "", interceptor.PhasePreToolResult, res)
			code := res.Code
			if code == "" {
				code = "INTERCEPTOR_REJECTED"
			}
			return mcp.ToolResult{}, mcp.NewToolError(code, res.Reason, nil)
		case interceptor.ActionModify:
			var modified preToolResultPayload
			if err := json.Unmarshal(res.ModifiedContent, &modified); err != nil {
				// A MODIFY that does not deserialize back into the
				// payload fails closed: the original result is not
				// trustworthy once an opaque rewrite has been applied.
				return mcp.ToolResult{}, mcp.NewToolError("INTERCEPTOR_REJECTED",
					"PreToolResult MODIFY returned an unparseable tool result", nil)
			}
			return mcp.ToolResult{Content: modified.Content, IsError: modified.IsError}, nil
		default:
			return result, nil
		}
	}
}

// applyPostAgentOutput runs the §4.8 PostAgentOutput chain over the
// agent's output parts before they are delivered to the client or to
// the parent session. It returns the possibly-modified parts and a
// non-nil rejection Result when the chain REJECTs; the caller surfaces
// the rejection to the agent or client. A nil chain or a malformed
// MODIFY payload leaves the parts unchanged. spec: §4.8 line 1054.
func applyPostAgentOutput(ctx context.Context, deps Deps, tenant, sessionID string, parts []executor.OutputPart) ([]executor.OutputPart, *interceptor.Result) {
	if deps.Interceptors == nil {
		return parts, nil
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return parts, nil
	}
	res := deps.Interceptors.Run(ctx, interceptor.Request{
		Phase:     interceptor.PhasePostAgentOutput,
		SessionID: sessionID,
		TenantID:  tenant,
		Content:   raw,
	})
	switch res.Action {
	case interceptor.ActionReject:
		recordChainRejection(ctx, deps, tenant, sessionID, interceptor.PhasePostAgentOutput, res)
		return parts, &res
	case interceptor.ActionModify:
		var modified []executor.OutputPart
		if err := json.Unmarshal(res.ModifiedContent, &modified); err != nil {
			return parts, nil
		}
		return modified, nil
	default:
		return parts, nil
	}
}

// registerMemoryTools wires the §9.4 lenny/memory_write and
// lenny/memory_query platform MCP tools. A memory is written under
// the calling session's user, runtime, and session scope; a query
// recalls across all of the user's sessions within the tenant.
func registerMemoryTools(srv *mcp.Server, deps Deps, tenant string, _ func() time.Time) {
	srv.RegisterTool(mcp.Tool{
		Name: "lenny/memory_write",
		// spec: §8.5 line 577 — the §8.5 JSON Schema lists `content` as
		// the only required input; the §9.4 memory record is scoped to
		// the calling principal's user via the session lookup. Metadata
		// values are constrained to strings per the spec schema
		// (`additionalProperties: {"type":"string"}`). `sessionId` is
		// the transport fallback used when the principal carries no
		// SessionID claim. F-8.5.14.
		Description: "Write a memory to the §9.4 memory store, scoped to the calling session's user.",
		InputSchema: json.RawMessage(`{"type":"object","required":["content"],"properties":{"content":{"type":"string","description":"The memory content to store."},"metadata":{"type":"object","description":"Optional key-value metadata attached to the memory record.","additionalProperties":{"type":"string"}},"sessionId":{"type":"string","description":"§15.2.1 transport-fallback session id; the principal's SessionID claim takes precedence."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			// SessionID is the transport fallback used when the
			// principal carries no SessionID claim. F-8.5.14.
			SessionID string `json:"sessionId,omitempty"`
			Content   string `json:"content"`
			// Metadata values are decoded as strings per the §8.5 line
			// 577 schema (`additionalProperties: {"type":"string"}`). A
			// non-string value rejects the call so the storage layer is
			// never asked to coerce. F-8.5.14.
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				fmt.Sprintf("invalid arguments: %v (metadata values must be strings per §8.5 line 577)", err), nil)
		}
		sessionID := callerSessionID(ctx, in.SessionID)
		if sessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
		}
		if in.Content == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"content is required (§8.5 line 577)", nil)
		}
		row, err := deps.Store.Get(ctx, tenant, sessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		scope := memorystore.MemoryScope{
			TenantID: tenant, UserID: row.UserID,
			AgentType: row.RuntimeRef, SessionID: row.ID,
		}
		// The memorystore.Memory.Metadata is `map[string]any`; the
		// schema-tightened input is widened back at the boundary so the
		// store contract is unchanged.
		md := make(map[string]any, len(in.Metadata))
		for k, v := range in.Metadata {
			md[k] = v
		}
		if err := deps.Memory.Write(ctx, scope, []memorystore.Memory{
			{Content: in.Content, Metadata: md},
		}); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("memory write: %w", err)
		}
		return textResult(`{"written":1}`), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name: "lenny/memory_query",
		// spec: §8.5 line 596 — the §8.5 JSON Schema lists `query` as
		// required and declares `limit` with `default: 10`. The session
		// is implicit in the calling principal. `sessionId` is the
		// transport fallback used when the principal carries no
		// SessionID claim. F-8.5.14.
		Description: "Query the §9.4 memory store across the calling session's user's memories.",
		InputSchema: json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string","description":"Natural-language query for semantic search over the memory store."},"limit":{"type":"integer","description":"Maximum number of results to return. Default: 10.","default":10},"sessionId":{"type":"string","description":"§15.2.1 transport-fallback session id; the principal's SessionID claim takes precedence."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			// SessionID is the transport fallback used when the
			// principal carries no SessionID claim. F-8.5.14.
			SessionID string `json:"sessionId,omitempty"`
			Query     string `json:"query"`
			Limit     int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		sessionID := callerSessionID(ctx, in.SessionID)
		if sessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
		}
		if in.Query == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"query is required (§8.5 line 596)", nil)
		}
		// spec: §8.5 line 596 — `limit` declares `default: 10`. The
		// MCP transport does not auto-fill JSON Schema defaults, so a
		// caller that omits the field arrives with the zero value; the
		// handler applies the documented default here. F-8.5.14.
		limit := in.Limit
		if limit == 0 {
			limit = 10
		}
		row, err := deps.Store.Get(ctx, tenant, sessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		// §9.4: memory recall is user-scoped — it spans every session
		// the user has run, not just the calling session.
		results, err := deps.Memory.Query(ctx,
			memorystore.MemoryScope{TenantID: tenant, UserID: row.UserID}, in.Query, limit)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("memory query: %w", err)
		}
		out := make([]memoryResult, len(results))
		for i, m := range results {
			out[i] = memoryResult{ID: m.ID, Content: m.Content, Metadata: m.Metadata}
		}
		data, _ := json.Marshal(struct {
			Memories []memoryResult `json:"memories"`
		}{Memories: out})
		return textResult(string(data)), nil
	})
}

// memoryResult is the §9.4 memory shape lenny/memory_query returns.
type memoryResult struct {
	ID       string         `json:"id"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// treeNode mirrors the §8 tree shape the get_task_tree tool returns.
type treeNode struct {
	SessionID string     `json:"sessionId"`
	State     string     `json:"state"`
	Children  []treeNode `json:"children"`
}

// discoveredRuntime is one entry in the lenny/list_runtimes result. It
// covers every runtime type, so it carries the type discriminator.
type discoveredRuntime struct {
	Name              string                              `json:"name"`
	Type              string                              `json:"type,omitempty"`
	IntegrationLevel  string                              `json:"integrationLevel,omitempty"`
	Description       string                              `json:"description,omitempty"`
	AgentInterface    *runtimestore.AgentInterface        `json:"agentInterface,omitempty"`
	PublishedMetadata []runtimestore.PublishedMetadataRef `json:"publishedMetadata,omitempty"`
}

// mcpAdapterCapabilities reports the §15 AdapterCapabilities of the MCP
// adapter. The MCP surface owns the /mcp path prefix. The MCP transport
// natively carries the §9.2 hop-by-hop elicitation chain, the
// lenny/delegate_task platform tool, and interrupt signalling, and
// sessions persist for resumption, so every capability flag is set. §9.1
// requires the list_runtimes response to embed this block.
func mcpAdapterCapabilities() adapter.Capabilities {
	return adapter.Capabilities{
		PathPrefix:                "/mcp",
		Protocol:                  "mcp",
		SupportsSessionContinuity: true,
		SupportsDelegation:        true,
		SupportsElicitation:       true,
		SupportsInterrupt:         true,
	}
}

// discoveredAgent is one entry in the lenny/discover_agents result.
type discoveredAgent struct {
	Name             string `json:"name"`
	IntegrationLevel string `json:"integrationLevel"`
	Description      string `json:"description,omitempty"`
}

// resolveDerivedRuntimes replaces each §5.1 derived runtime with its
// effective merged definition, so the discovery surface reports the
// fields a derived runtime inherits from its base. A derived runtime
// whose base is missing is dropped — it is not usable.
func resolveDerivedRuntimes(ctx context.Context, store runtimestore.Store, rows []runtimestore.Runtime) []runtimestore.Runtime {
	out := make([]runtimestore.Runtime, 0, len(rows))
	for _, rt := range rows {
		if !rt.IsDerived() {
			out = append(out, rt)
			continue
		}
		eff, err := runtimestore.Resolve(ctx, store, rt.Name)
		if err != nil {
			continue
		}
		out = append(out, eff)
	}
	return out
}

// environmentResolution returns the §10.6 transparent-filtering
// Resolution for the request. It prefers the Resolution the
// environment-resolver middleware (pkg/gateway/middleware/environment)
// attached to the request context — the production path, where the MCP
// server is mounted under that middleware. When no Resolution is on the
// context (an MCP tool exercised directly without the HTTP middleware
// chain), it resolves the same Resolution from the Deps registries via
// the shared environmentmw.Resolve. Either way the §10.6 filtering goes
// through a single Resolution type and a single envaccess code path.
func environmentResolution(ctx context.Context, deps Deps) (environmentmw.Resolution, error) {
	if res := environmentmw.FromContext(ctx); res.Configured {
		return res, nil
	}
	principal, ok := authmw.FromContext(ctx)
	if !ok {
		return environmentmw.Resolution{}, nil
	}
	caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
	return environmentmw.Resolve(ctx, deps.Environments, deps.Tenants,
		deps.DefaultNoEnvironmentPolicy, caller, principal.TenantID)
}

// filterByEnvironmentAccess applies §10.6 transparent filtering to a
// runtime list: it returns only the runtimes the caller's environment
// membership authorizes. Filtering runs when the environment and
// tenant registries are wired and the request carries an authenticated
// principal; otherwise the list is returned unchanged (a minimal
// deployment with no environment registry). A caller in no environment
// is governed by the tenant's §10.6 noEnvironmentPolicy.
func filterByEnvironmentAccess(ctx context.Context, deps Deps, runtimes []runtimestore.Runtime) ([]runtimestore.Runtime, error) {
	res, err := environmentResolution(ctx, deps)
	if err != nil {
		return nil, err
	}
	return res.FilterRuntimes(runtimes), nil
}

// runtimeAuthorizedForCaller reports whether the caller's §10.6
// environment access admits runtimeRef as a delegation target — the
// "environment definition" half of the §10.6 effective delegation
// scope. When the environment, tenant, or runtime registry is not
// wired, or the runtime cannot be resolved, it returns true: the
// transparent-filter boundary is not in effect and the delegation
// service remains the authority on the runtime reference.
func runtimeAuthorizedForCaller(ctx context.Context, deps Deps, runtimeRef string) (bool, error) {
	if deps.Environments == nil || deps.Tenants == nil || deps.Runtimes == nil {
		return true, nil
	}
	rt, err := deps.Runtimes.Get(ctx, runtimeRef)
	if err != nil {
		return true, nil
	}
	authorized, err := filterByEnvironmentAccess(ctx, deps, []runtimestore.Runtime{rt})
	if err != nil {
		return false, err
	}
	return len(authorized) > 0, nil
}

// crossEnvReachable reports whether runtimeRef is reachable from the
// parent session's §10.6 environment through a bilateral
// cross-environment-delegation declaration. It returns false when the
// registries are not wired, the parent session is not environment-
// scoped, or the runtime cannot be resolved — the cross-environment
// path simply does not widen the delegation scope in those cases.
func crossEnvReachable(ctx context.Context, deps Deps, tenant, parentSessionID, runtimeRef string) (bool, error) {
	if deps.Environments == nil || deps.Store == nil || deps.Runtimes == nil {
		return false, nil
	}
	parent, err := deps.Store.Get(ctx, tenant, parentSessionID)
	if err != nil || parent.Environment == "" {
		return false, nil
	}
	rt, err := deps.Runtimes.Get(ctx, runtimeRef)
	if err != nil {
		return false, nil
	}
	// The §10.6 environment Resolution carries every environment defined
	// for the tenant and the subset the caller is a member of — the same
	// set the transparent-filter discovery surfaces resolved from.
	res, err := environmentResolution(ctx, deps)
	if err != nil {
		return false, err
	}
	if !res.Configured {
		return false, nil
	}
	// §10.6: the parent session's environment is caller-supplied at
	// session creation and is only trusted here when the caller is
	// genuinely a member of it. Without this check a session tagged
	// with an environment the caller does not belong to could borrow
	// that environment's cross-environment delegation reach.
	if !memberOfEnvironment(res, parent.Environment) {
		return false, nil
	}
	return envaccess.CrossEnvironmentReachable(parent.Environment, rt, res.AllEnvironments), nil
}

// memberOfEnvironment reports whether the resolved caller is a member
// of the named environment, consulting the §10.6 Resolution's already-
// resolved member-environment set.
func memberOfEnvironment(res environmentmw.Resolution, name string) bool {
	for _, e := range res.MemberEnvironments {
		if e.Name == name {
			return true
		}
	}
	return false
}

// resolvePoolIsolation returns the explicit §5.3 isolation profile of
// the named pool. It returns the empty profile when the pool registry
// is not wired, the pool cannot be resolved, or the pool inherits its
// runtime's default profile. A resolved profile is handed to the
// delegation service so the §8.3 monotonicity check evaluates the
// child pool's isolation rather than the profile inherited from the
// parent session — §10.6 requires that a delegation, including a
// cross-environment one, never relaxes the isolation invariant.
func resolvePoolIsolation(ctx context.Context, deps Deps, poolRef string) isolation.Profile {
	if deps.Pools == nil || poolRef == "" {
		return ""
	}
	pool, err := deps.Pools.Get(ctx, poolRef)
	if err != nil {
		return ""
	}
	return pool.IsolationProfile
}

// awaitPollInterval is how often lenny/await_children re-reads its
// children's states while waiting for the mode's settle condition.
const awaitPollInterval = 25 * time.Millisecond

// callerSessionID resolves the calling session id. It prefers the
// authenticated principal's SessionID claim (the §15 production path,
// where every JWT carries the originating session) and falls back to
// the explicit field a caller passed in the JSON args for transports
// that have not yet bound a session-scoped principal (tests, the dev-
// headers path). The spec §8.5 schemas do not include `sessionId`
// because the session is implicit in the caller's identity; the
// fallback keeps the v1 tool surface usable in deployments that have
// not yet rotated to session-bound bearer tokens.
// spec: §8.5 lines 544, 559, 577, 596; F-8.5.11, F-8.5.13, F-8.5.14.
func callerSessionID(ctx context.Context, fallback string) string {
	if p, ok := authmw.FromContext(ctx); ok && p.SessionID != "" {
		return p.SessionID
	}
	return fallback
}

// taskResult is the §8.8 TaskResult lenny/await_children returns for a
// settled child. v1 reports the identity and terminal state; the §8.8
// usage and treeUsage rollups are not yet tracked.
type taskResult struct {
	SchemaVersion int        `json:"schemaVersion"`
	TaskID        string     `json:"taskId"`
	State         string     `json:"state"`
	Error         *taskError `json:"error,omitempty"`
}

// taskError is the §8.8 TaskResult.error payload for a non-completed
// terminal child.
type taskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// toTaskResult builds the §8.8 TaskResult for a settled child session.
func toTaskResult(s sessionstore.Session) taskResult {
	tr := taskResult{SchemaVersion: 1, TaskID: s.ID, State: string(s.State)}
	if s.State != session.StateCompleted {
		code := s.FailureReason
		if code == "" {
			code = "CHILD_" + strings.ToUpper(string(s.State))
		}
		tr.Error = &taskError{Code: code, Message: "child session ended in state " + string(s.State)}
	}
	return tr
}

// childOutcome is a child's resolved state for lenny/await_children,
// sourced from the live session row or, when the row is gone, from the
// §8.10 archive.
type childOutcome struct {
	parentID string
	state    session.State
	result   taskResult
}

// resolveChild resolves a child to its current outcome. It reads the
// live session row, falling back to the §8.10 archive when the row is
// gone — a child that settled and was reclaimed, or whose pod failed
// while its resumed parent re-awaits it.
func resolveChild(ctx context.Context, store sessionstore.Store, archive treearchive.Store,
	tenant, childID string,
) (childOutcome, error) {
	row, err := store.Get(ctx, tenant, childID)
	if err == nil {
		return childOutcome{
			parentID: row.ParentSessionID,
			state:    row.State,
			result:   toTaskResult(row),
		}, nil
	}
	if !errors.Is(err, sessionstore.ErrNotFound) || archive == nil {
		return childOutcome{}, fmt.Errorf("child %s lookup: %w", childID, err)
	}
	node, archiveErr := archive.GetByNode(ctx, tenant, childID)
	if archiveErr != nil {
		return childOutcome{}, fmt.Errorf("child %s lookup: %w", childID, err)
	}
	var tr taskResult
	_ = json.Unmarshal([]byte(node.Result), &tr)
	if tr.TaskID == "" {
		tr.TaskID = childID
	}
	return childOutcome{
		parentID: node.ParentSessionID,
		state:    session.State(node.State),
		result:   tr,
	}, nil
}

// collectChildResults reads the awaited children and reports whether
// the mode's settle condition holds. For `all` and `settled` the
// condition is every child terminal; for `any` it is at least one
// child terminal, and only the first such child (in childIDs order) is
// returned. A child whose live row is gone is resolved from the §8.10
// archive.
func collectChildResults(ctx context.Context, store sessionstore.Store, archive treearchive.Store,
	tenant string, childIDs []string, mode string,
) ([]taskResult, bool, error) {
	var terminal []taskResult
	allTerminal := true
	for _, cid := range childIDs {
		oc, err := resolveChild(ctx, store, archive, tenant, cid)
		if err != nil {
			return nil, false, err
		}
		if session.IsTerminal(oc.state) {
			terminal = append(terminal, oc.result)
		} else {
			allTerminal = false
		}
	}
	if mode == "any" {
		if len(terminal) > 0 {
			return terminal[:1], true, nil
		}
		return nil, false, nil
	}
	if allTerminal {
		return terminal, true, nil
	}
	return nil, false, nil
}

func buildTree(root sessionstore.Session, all []sessionstore.Session) treeNode {
	childrenByParent := map[string][]sessionstore.Session{}
	for _, s := range all {
		if s.ParentSessionID != "" {
			childrenByParent[s.ParentSessionID] = append(childrenByParent[s.ParentSessionID], s)
		}
	}
	return walk(root, childrenByParent, map[string]bool{})
}

func walk(s sessionstore.Session, byParent map[string][]sessionstore.Session, seen map[string]bool) treeNode {
	node := treeNode{SessionID: s.ID, State: string(s.State), Children: []treeNode{}}
	if seen[s.ID] {
		return node
	}
	seen[s.ID] = true
	for _, c := range byParent[s.ID] {
		node.Children = append(node.Children, walk(c, byParent, seen))
	}
	return node
}

// withinMessagingTopology reports whether target sits in sender's
// §7.2 line 240 messaging neighborhood: direct parent, direct child,
// or sibling (same parent). Self-messaging is rejected per the same
// rule. The check is constant-time — no tree walk — because every
// admissible relation is a one-hop ParentSessionID comparison.
// spec: §7.2 line 240 (`direct` / `siblings` scope), §7.2 line 373
// (parent communication asymmetry). F-7.2.22.
func withinMessagingTopology(sender, target sessionstore.Session) bool {
	if sender.ID == target.ID {
		return false
	}
	// target is sender's direct child
	if target.ParentSessionID == sender.ID {
		return true
	}
	// target is sender's direct parent
	if sender.ParentSessionID == target.ID && target.ID != "" {
		return true
	}
	// target is sender's sibling (share a non-empty parent)
	if sender.ParentSessionID != "" && sender.ParentSessionID == target.ParentSessionID {
		return true
	}
	return false
}

// isDescendant reports whether child sits in the §8 delegation subtree
// rooted at parentID. It walks the ParentSessionID chain upward from
// child; the seen set guards against a malformed cyclic chain.
func isDescendant(child sessionstore.Session, parentID string, all []sessionstore.Session) bool {
	byID := make(map[string]sessionstore.Session, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}
	seen := map[string]bool{}
	cur := child
	for cur.ParentSessionID != "" && !seen[cur.ID] {
		seen[cur.ID] = true
		if cur.ParentSessionID == parentID {
			return true
		}
		next, ok := byID[cur.ParentSessionID]
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// cancelSubtree transitions child and every non-terminal session in the
// subtree rooted at it to `cancelled` — the default §8 cascade policy.
// Already-terminal sessions are left untouched, but the traversal still
// descends through them to reach any non-terminal descendants. It
// returns the ids it cancelled, sorted for a deterministic result.
func cancelSubtree(ctx context.Context, store sessionstore.Store, tenant string,
	child sessionstore.Session, all []sessionstore.Session,
) ([]string, error) {
	byParent := map[string][]sessionstore.Session{}
	for _, s := range all {
		if s.ParentSessionID != "" {
			byParent[s.ParentSessionID] = append(byParent[s.ParentSessionID], s)
		}
	}
	var cancelled []string
	seen := map[string]bool{}
	queue := []sessionstore.Session{child}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur.ID] {
			continue
		}
		seen[cur.ID] = true
		queue = append(queue, byParent[cur.ID]...)
		if session.IsTerminal(cur.State) {
			continue
		}
		if _, err := store.Update(ctx, tenant, cur.ID, func(row *sessionstore.Session) error {
			row.State = session.StateCancelled
			return nil
		}); err != nil {
			return cancelled, fmt.Errorf("cancel session %s: %w", cur.ID, err)
		}
		cancelled = append(cancelled, cur.ID)
	}
	sort.Strings(cancelled)
	return cancelled, nil
}

// treeRoot returns the id of the §8 delegation tree's root by walking
// the ParentSessionID chain up from start. The seen set guards against
// a malformed cyclic chain.
func treeRoot(start sessionstore.Session, all []sessionstore.Session) string {
	byID := make(map[string]sessionstore.Session, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}
	cur := start
	seen := map[string]bool{}
	for cur.ParentSessionID != "" && !seen[cur.ID] {
		seen[cur.ID] = true
		next, ok := byID[cur.ParentSessionID]
		if !ok {
			break
		}
		cur = next
	}
	return cur.ID
}

// archiveCancelled records each cancelled subtree node in the §8.10
// session_tree_archive, keyed by the delegation tree's root. Archiving
// is best-effort: an archive error does not undo the cancellation, so
// the error is dropped.
func archiveCancelled(ctx context.Context, archive treearchive.Store,
	tenant, rootSessionID string, cancelled []string, all []sessionstore.Session, now time.Time,
) {
	parentOf := make(map[string]string, len(all))
	for _, s := range all {
		parentOf[s.ID] = s.ParentSessionID
	}
	for _, id := range cancelled {
		result, _ := json.Marshal(taskResult{
			SchemaVersion: 1,
			TaskID:        id,
			State:         string(session.StateCancelled),
			Error: &taskError{
				Code:    "CHILD_CANCELLED",
				Message: "child session ended in state cancelled",
			},
		})
		_ = archive.Archive(ctx, treearchive.ArchivedNode{
			TenantID:        tenant,
			RootSessionID:   rootSessionID,
			NodeSessionID:   id,
			ParentSessionID: parentOf[id],
			State:           string(session.StateCancelled),
			Result:          string(result),
			SettledAt:       now,
		})
	}
}

func textResult(s string) mcp.ToolResult {
	return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: s}}}
}

// buildSendMessageReceipt builds the §15.4 `delivery_receipt` envelope
// for a lenny/send_message call. The minimal gateway always emits
// `status: "delivered"` once the executor has accepted the message or
// the inReplyTo path has resolved a pending lenny/request_input. The
// queued / dropped / expired / rate_limited / error paths land with the
// §7.2 inbox + DLQ machinery (F-7.2.4) and update this helper then.
// When inReplyTo resolves a pending request the `resolved` field
// carries the resolved request id so callers correlating by
// inReplyTo can pivot off either field; for the executor path it is
// empty. The `output` field mirrors the executor's text output parts so
// the runtime's reply travels in the same JSON envelope as the receipt.
// spec: §15.4 lines 1725-1737; F-7.2.10.
func buildSendMessageReceipt(messageID, resolvedRequestID string, out []executor.OutputPart, now time.Time) string {
	envelope := struct {
		DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`
		Resolved        string                  `json:"resolved,omitempty"`
		Output          []executor.OutputPart   `json:"output,omitempty"`
	}{
		DeliveryReceipt: session.DeliveryReceipt{
			MessageID:   messageID,
			Status:      session.DeliveryStatusDelivered,
			DeliveredAt: now,
		},
		Resolved: resolvedRequestID,
		Output:   out,
	}
	body, _ := json.Marshal(envelope)
	return string(body)
}

// elicitationOrGen returns id when non-empty, otherwise a freshly
// generated identifier. It lets the §9.2 SUPPRESSED response carry an
// elicitation_id even though a suppressed elicitation is never
// recorded in the interaction store.
func elicitationOrGen(id string, gen func() string) string {
	if id != "" {
		return id
	}
	return gen()
}
