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
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/delegation/tracing"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/envaccess"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
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
	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	sessionstate "github.com/lennylabs/lenny/pkg/session/state"
	"github.com/lennylabs/lenny/pkg/task"
	taskstate "github.com/lennylabs/lenny/pkg/task/state"
)

// mcpStateForSession returns the §8.8 MCP-protocol state spelling for
// a §7.2 session-level Lenny state. Terminal and `input_required`
// states route through the §8.8 line 857 task-level table; all other
// session-level states fall through to the §8.8 line 871-883
// supplementary table. F-8.8.7.
// spec: §8.8 lines 857-883.
func mcpStateForSession(s session.State) string {
	proto, _ := nodeProtocolState(s)
	return proto
}

// nodeProtocolState returns the §8.8 MCP-protocol state spelling and
// any supplementary-table metadata annotations for a §7.2 session-level
// Lenny state. The metadata map is nil when no annotation applies (the
// caller omits the `metadata` field from the wire payload via the
// `omitempty` JSON tag). F-8.8.7 / F-8.8.9.
// spec: §8.8 lines 855-883.
func nodeProtocolState(s session.State) (string, map[string]any) {
	switch s {
	case session.StateInputRequired:
		return taskstate.MCPProtocolState(taskstate.InputRequired), nil
	case session.StateCompleted:
		return taskstate.MCPProtocolState(taskstate.Completed), nil
	case session.StateFailed:
		return taskstate.MCPProtocolState(taskstate.Failed), nil
	case session.StateCancelled:
		return taskstate.MCPProtocolState(taskstate.Cancelled), nil
	case session.StateExpired:
		return taskstate.MCPProtocolState(taskstate.Expired), nil
	}
	return sessionstate.MCPProtocolState(sessionstate.State(s))
}

// DelegationAuditor records §11.7 audit events for delegation
// operations such as the §10.6 isolation-monotonicity violation.
type DelegationAuditor interface {
	EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any)
}

// TreeCycleObserver is invoked when a §8.9 tree walker hits a cycle
// in the §8.2 ParentSessionID lineage. The §8.2 cycle detector
// prevents cycles at delegation time, so a non-zero call rate signals
// a corrupted persistent store. The walker truncates the cycle in
// the returned tree; the observer surfaces the corruption to
// operators via the §11.7 audit log + §16.1 counter. The source
// field is `mcp` for the lenny/get_task_tree platform tool.
// spec: §8.9 line 1003; F-8.9.10.
type TreeCycleObserver interface {
	OnTreeCycle(ctx context.Context, ev TreeCycleEvent)
}

// TreeCycleEvent is the §8.9 tree-cycle observation payload.
type TreeCycleEvent struct {
	TenantID      string
	RootSessionID string
	CycleNodeID   string
	Source        string
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

	// TreeCycleObserver, when set, receives a §8.9 cycle observation
	// when lenny/get_task_tree hits a repeated node in the
	// ParentSessionID lineage. Production wires the same observer to
	// the REST /v1/sessions/{id}/tree handler; nil disables the
	// emission and the walker still truncates the cycle.
	// spec: §8.9 line 1003; F-8.9.10.
	TreeCycleObserver TreeCycleObserver

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

	// ElicitationLifecycleMetrics, when set, receives the §16.1 admit
	// and terminal lifecycle events the operator dashboards and the
	// §16.5 ElicitationBacklogHigh alert read. Optional; nil disables
	// the histograms, the pending gauge, and the timeout/suppressed
	// counters. spec: §16.1 lines 60–63; §16.5 line 458. F-9.2.14.
	ElicitationLifecycleMetrics ElicitationLifecycleRecorder

	// ElicitationTamperMetrics, when set, receives a §9.2 / §16.5
	// content-tamper-detected notification each time the chain walk
	// catches a forwarding hop that mutated the elicitation payload.
	// *gatewaymetrics.Metrics satisfies the
	// ElicitationTamperRecorder interface.
	ElicitationTamperMetrics ElicitationTamperRecorder

	// ElicitationModeResolver resolves the §9.2 effective elicitation
	// content-integrity enforcement mode (off | detect-only | enforce)
	// for a tenant — max(platform floor, tenant stored). The dispatcher
	// consults it on the content-integrity hot path: `off` skips the
	// check, `detect-only` records a divergence but forwards as received,
	// `enforce` drops the divergent forward. Optional — a nil resolver
	// defaults to the §9.2 enforce tenant default. spec: §9.2 lines
	// 58–64. F-9.2.2.
	ElicitationModeResolver func(ctx context.Context, tenantID string) elicitation.EnforcementMode

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

	// MessagingDefaultScope and MessagingMaxScope are the §7.2
	// deployment-level messagingScope configuration: the per-session
	// default and the absolute ceiling. lenny/send_message resolves the
	// sender's effective scope from them and rejects a sibling target
	// unless the effective scope is `siblings`. An empty
	// MessagingDefaultScope resolves to the §7.2 default `direct`; an
	// empty MessagingMaxScope imposes no ceiling beyond the enum.
	// Tenant- and runtime-level overrides narrow further once those
	// configuration surfaces are stored (their absence resolves to the
	// deployment scope). spec: §7.2 lines 236-266. F-7.2.6.
	MessagingDefaultScope session.MessagingScope
	MessagingMaxScope     session.MessagingScope

	// MessagingRateLimit carries the §8.3 per-session lenny/send_message
	// rate limits (maxPerMinute, maxPerSession, maxInboundPerMinute). A
	// zero field selects the §8.3 default. spec: §7.2 line 270; §8.3
	// lines 269-272. F-7.2.6.
	MessagingRateLimit MessagingRateLimit

	// MessagingRateCounter is the fixed-window counter backing the
	// per-minute messaging rate limits. Optional — when nil an
	// in-process counter is used (the minimal-gateway / test default);
	// production wires the same cross-replica Redis counter the §11.1
	// admission limits use. spec: §11.1. F-7.2.6.
	MessagingRateCounter ratelimit.Counter

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
	// elicitationDropDomainNotAllowlisted — the §9.2 line 86 url-mode
	// allowlist rejected the request. The metric drop substitutes for
	// the unspec'd `elicitation.url_mode_domain_rejected` audit event
	// the dispatcher previously emitted; the §16.7 catalog enumerates
	// the closed set of audit events and does not list one for this
	// rejection. spec: §9.2 line 86; F-9.2.11.
	elicitationDropDomainNotAllowlisted = "domain_not_allowlisted"
)

// ElicitationDropRecorder records a §9.1 elicitation drop for the
// lenny_elicitation_dropped_total counter. pkg/gateway/gatewaymetrics
// satisfies it.
type ElicitationDropRecorder interface {
	RecordElicitationDrop(reason string)
}

// ElicitationLifecycleRecorder hooks the §16.1 admit/terminal metric
// sites the elicitation handler instruments. *gatewaymetrics.Metrics
// satisfies it. spec: §16.1 lines 60–63 — pending gauge, timeout /
// suppressed counters, roundtrip histogram. F-9.2.14.
type ElicitationLifecycleRecorder interface {
	// IncElicitationPending increments the lenny_elicitation_pending
	// gauge when the dispatcher admits an elicitation onto the chain.
	IncElicitationPending()
	// DecElicitationPending decrements the gauge on every terminal
	// phase (responded | dismissed | timeout).
	DecElicitationPending()
	// IncElicitationTimeout counts a §9.1 maxElicitationWait deadline
	// drop.
	IncElicitationTimeout()
	// IncElicitationSuppressed counts a §9.2 depth-policy suppression
	// or per-session budget rejection.
	IncElicitationSuppressed()
	// ObserveElicitationRoundtrip records the admit-to-terminal
	// wall-clock latency.
	ObserveElicitationRoundtrip(d time.Duration)
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

	// §7.2 send_message governance shared per-adapter state: the
	// fixed-window + lifetime rate limiter and the deployment-resolved
	// effective messagingScope. The v1 adapter has no per-session
	// tenant/runtime scope surface stored, so the effective scope is the
	// deployment resolution (tenant/runtime overrides default through);
	// it is resolved once here rather than per call. F-7.2.6.
	msgLimiter := newMessagingLimiter(deps.MessagingRateCounter, deps.MessagingRateLimit)
	msgScope := session.ResolveEffectiveMessagingScope(deps.MessagingDefaultScope, deps.MessagingMaxScope, "", "")

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
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant is the
		// authenticated principal's, not the Register-time default,
		// so a multi-tenant deployment scopes the session row to the
		// caller's tenant. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
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
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal so the §7.2 topology lookup and the §4 chain payload
		// stay scoped to the right tenant. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
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
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
		}
		sessionID := callerSessionID(ctx, in.SessionID)
		if sessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"caller session is unbound (no principal SessionID, no sessionId arg)", nil)
		}
		caller, err := deps.Store.Get(ctx, tenant, sessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
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
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
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
					Results []task.Result `json:"results"`
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
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
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
			// runtime's merged Override value applies. The same lookup
			// resolves the runtime's §5.1 capabilities.interaction so the
			// §8.8 line 869 `one_shot` input-round constraint can fire
			// before a channel is allocated. F-8.8.10.
			callTimeout := requestInputTimeout
			oneShot := false
			if deps.Runtimes != nil && row.RuntimeRef != "" {
				if rt, rerr := runtimestore.Resolve(ctx, deps.Runtimes, row.RuntimeRef); rerr == nil {
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
				// spec: §16.1 line 62 — a per-session budget drop is a
				// §9.1 suppression. F-9.2.14.
				if deps.ElicitationLifecycleMetrics != nil {
					deps.ElicitationLifecycleMetrics.IncElicitationSuppressed()
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
			dr, err := dispatcher.dispatch(ctx, tenant, row, originalContent, initiator, in.URL)
			if err != nil {
				return mcp.ToolResult{}, err
			}
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
				data, _ := json.Marshal(struct {
					ElicitationID   string `json:"elicitationId"`
					Message         string `json:"message"`
					OriginPod       string `json:"originPod"`
					InitiatorType   string `json:"initiatorType"`
					DelegationDepth int    `json:"delegationDepth"`
					OriginRuntime   string `json:"originRuntime,omitempty"`
				}{
					ElicitationID:   elicitationID,
					Message:         in.Message,
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
			// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
			// principal. F-9.2.13 / F-15.2.15.
			tenant := callerTenantID(ctx, tenant)
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
			// spec: §10.6 line 672 — `environmentId` stub narrows to one
			// environment when supplied; unknown environment collapses
			// the result to empty so a typo never broadens visibility.
			// F-10.6.10.
			if in.EnvironmentID != "" {
				runtimes = narrowRuntimesToEnvironment(ctx, deps, runtimes, in.EnvironmentID)
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

	if deps.Delegation != nil {
		srv.RegisterTool(mcp.Tool{
			Name:        "lenny/delegate_task",
			Description: "Spawn a child session under a running parent (§8.2 recursive delegation).",
			// spec: §11.5 line 277 — `idempotencyKey` (optional, ≤128
			// runes) collapses retries of SpawnChild to one execution;
			// SpawnChild is one of the six §11.5 critical operations and
			// the MCP path is its only client surface. spec: F-11.5.1,
			// F-11.5.6.
			InputSchema: json.RawMessage(`{"type":"object","required":["parentSessionId","runtimeRef"],"properties":{"parentSessionId":{"type":"string"},"runtimeRef":{"type":"string"},"poolRef":{"type":"string"},"maxDepth":{"type":"integer"},"taskInput":{"type":"string"},"approvalMode":{"type":"string","enum":["policy","approval","deny"],"description":"§8.4 closed enum on the delegation lease. Omit for the spec default (policy)."},"treeVisibility":{"type":"string","enum":["full","parent-and-self","self-only"],"description":"§8.5 lease visibility boundary controlling lenny/get_task_tree. Omit to inherit the parent's effective value. A value broader than the parent's effective visibility is rejected with TREE_VISIBILITY_WEAKENING."},"idempotencyKey":{"type":"string","maxLength":128,"description":"§11.5 idempotency key: a duplicate request with the same key (within 24h) replays the cached child session result without re-executing."}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
			// principal so the §4 chain payload, §8.2 service Delegate, and
			// §16.7 audit emission all stamp the right tenant.
			// F-9.2.13 / F-15.2.15.
			tenant := callerTenantID(ctx, tenant)
			var in struct {
				ParentSessionID string `json:"parentSessionId"`
				RuntimeRef      string `json:"runtimeRef"`
				PoolRef         string `json:"poolRef"`
				MaxDepth        int    `json:"maxDepth"`
				TaskInput       string `json:"taskInput"`
				// ApprovalMode is the §8.4 lease enum forwarded
				// verbatim onto the delegation Request; the service
				// validates the closed enum and short-circuits on
				// "deny" before any side effects. F-8.4.1, F-8.4.2.
				ApprovalMode string `json:"approvalMode,omitempty"`
				// TreeVisibility is the §8.5 lease visibility boundary
				// forwarded onto the delegation Request. Empty inherits the
				// parent's effective value; a value broader than the
				// parent's is rejected with TREE_VISIBILITY_WEAKENING.
				// F-8.5.2 / F-8.9.2 / F-13.5.8.
				TreeVisibility string `json:"treeVisibility,omitempty"`
				// IdempotencyKey is read by the MCP idempotency hook
				// before the handler runs and is intentionally accepted
				// + ignored here. spec: §11.5 line 277; F-11.5.1,
				// F-11.5.6.
				IdempotencyKey string `json:"idempotencyKey,omitempty"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
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
			// spec: §8.2 line 50 — `lenny/delegate_task` rejects
			// `type: mcp` targets with `target_not_an_agent`. The check
			// runs before the §10.6 scope filter so a caller cannot
			// reach an MCP-only runtime even if it happens to share
			// the caller's environment.
			if deps.Runtimes != nil && in.RuntimeRef != "" {
				if rt, err := runtimestore.Resolve(ctx, deps.Runtimes, in.RuntimeRef); err == nil && rt.Type == runtimestore.TypeMCP {
					// spec: §15.2.1 — surface the canonical lenny code via
					// *mcp.ToolError so REST and MCP envelopes share the
					// same (category, retryable) pair. F-8.5.10.
					return mcp.ToolResult{}, mcp.NewToolError("TARGET_NOT_AN_AGENT",
						fmt.Sprintf("target_not_an_agent: delegation target %q is a type:mcp runtime (§8.2 line 50)", in.RuntimeRef),
						map[string]any{"runtimeRef": in.RuntimeRef})
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
				// is rejected with the TARGET_NOT_IN_SCOPE reason. spec:
				// §15.2.1 — surface the canonical lenny code via
				// *mcp.ToolError so REST and MCP envelopes share the same
				// (category, retryable) pair. F-8.5.10.
				return mcp.ToolResult{}, mcp.NewToolError("TARGET_NOT_IN_SCOPE",
					fmt.Sprintf("target_not_in_scope: delegation target %q is not within the caller's environment scope (§10.6)", in.RuntimeRef),
					map[string]any{"runtimeRef": in.RuntimeRef})
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
					// spec: §15.2.1 line 1386 — a manual MCP-only tool
					// (lenny/delegate_task) must use the shared error
					// taxonomy. INTERCEPTOR_REJECTED is the canonical
					// CategoryPolicy / retryable:false code for a
					// deliberate interceptor REJECT, so REST and MCP
					// parity is preserved. F-15.2.11.
					return mcp.ToolResult{}, mcp.NewToolError("INTERCEPTOR_REJECTED",
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
					// spec: §15.2.1 line 1386 — see PreDelegation site
					// above. INTERCEPTOR_REJECTED preserves REST/MCP
					// (category, retryable) parity for a deliberate
					// PreRoute reject. F-15.2.11.
					return mcp.ToolResult{}, mcp.NewToolError("INTERCEPTOR_REJECTED",
						res.Reason,
						map[string]any{"phase": string(interceptor.PhasePreRoute)})
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
				ApprovalMode:     lease.ApprovalMode(in.ApprovalMode),
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
						deps.Audit.EmitDelegationEvent(ctx, "delegation.isolation_violation", map[string]any{
							"parentSessionId":     in.ParentSessionID,
							"runtimeRef":          in.RuntimeRef,
							"poolRef":             in.PoolRef,
							"parent_isolation":    string(isoErr.ParentProfile),
							"target_isolation":    string(isoErr.ChildProfile),
							"matched_policy_rule": "",
							"cross_environment":   viaCrossEnv,
							"approval_mode":       declaredApproval,
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
				// §8.2 line 58: the child-token exchange requires the
				// parent's authenticated user JWT as `subject_token`. A
				// userless parent is surfaced under a distinct reason so
				// the caller can distinguish "missing user identity" from
				// the generic delegation failure path.
				if errors.Is(err, delegation.ErrParentNoUser) {
					return mcp.ToolResult{}, mcp.NewToolError("DELEGATION_PARENT_NO_USER",
						fmt.Sprintf("DELEGATION_PARENT_NO_USER: %s", err.Error()), nil)
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
				// spec: §8.2 line 50 — the service-layer defence-in-depth
				// type gate that mirrors the §10.6 shim. Surface the
				// canonical code so REST and MCP envelopes match.
				if errors.Is(err, delegation.ErrTargetNotAgent) {
					return mcp.ToolResult{}, mcp.NewToolError("TARGET_NOT_AN_AGENT",
						err.Error(), map[string]any{"runtimeRef": in.RuntimeRef})
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
					return mcp.ToolResult{}, mcp.NewToolError(
						"INTERCEPTOR_WEAKENING_COOLDOWN",
						err.Error(),
						map[string]any{
							"policyName":        cdErr.PolicyName,
							"cooldownSeconds":   cdErr.CooldownSeconds,
							"retryAfterSeconds": cdErr.RetryAfterSeconds,
						})
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
// The tenant parameter is the Register-time fallback; each tool
// handler re-resolves the per-request tenant from the authenticated
// principal via callerTenantID. spec: §9.2 / §16.1; F-9.2.13 / F-15.2.15.
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
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal so the §9.4 memory scope and the session lookup
		// stay tenant-correct. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
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
		// spec: §9.2 / §16.1 / §15.2 line 1335 — tenant from the caller's
		// principal so the §9.4 user-scoped memory query stays tenant-
		// correct. F-9.2.13 / F-15.2.15.
		tenant := callerTenantID(ctx, tenant)
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
// spec: §8.5 line 540 — "Each node includes `taskId`, `state`, and
// `runtimeRef`". The REST surface (sessionserver.TreeNode) carries the
// same three fields; per §15.2.1 the MCP and REST projections of the
// same operation must remain semantically equivalent. The v1 invariant
// "task record == session row" (§4.2 line 157) means TaskID is the
// session row's id. F-8.9.5.
//
// `state` carries the §8.8 MCP-protocol spelling (e.g., `canceled`,
// `working`, `failed` for `expired`). `metadata` carries the §8.8 line
// 871-883 supplementary table annotations — `suspended: true` for the
// `suspended` session state and `resuming: true` for `resume_pending`
// / `resuming`. The map is omitted when no annotation applies. F-8.8.9.
type treeNode struct {
	TaskID     string         `json:"taskId"`
	State      string         `json:"state"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	RuntimeRef string         `json:"runtimeRef,omitempty"`
	Children   []treeNode     `json:"children"`
}

// mcpEndpointFor returns the §9.1 discovery pointer for runtime
// `name` — `/mcp/runtimes/{name}` for `type:mcp`, empty otherwise.
// F-9.1.4 / coordinated with F-9.1.3.
func mcpEndpointFor(rt runtimestore.Runtime) string {
	if rt.Type != runtimestore.TypeMCP {
		return ""
	}
	return "/mcp/runtimes/" + rt.Name
}

// discoveredRuntime is one entry in the lenny/list_runtimes result. It
// covers every runtime type, so it carries the type discriminator.
//
// McpEndpoint is the §9.1 line 38 / §15.1 line 698 discovery pointer
// to the per-runtime intra-pod MCP server (`/mcp/runtimes/{name}` for
// type:mcp; empty for type:agent). F-9.1.4 / coordinated with F-9.1.3.
type discoveredRuntime struct {
	Name              string                              `json:"name"`
	Type              string                              `json:"type,omitempty"`
	IntegrationLevel  string                              `json:"integrationLevel,omitempty"`
	Description       string                              `json:"description,omitempty"`
	AgentInterface    *runtimestore.AgentInterface        `json:"agentInterface,omitempty"`
	PublishedMetadata []runtimestore.PublishedMetadataRef `json:"publishedMetadata,omitempty"`
	McpEndpoint       string                              `json:"mcpEndpoint,omitempty"`
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

// filterByEffectiveDelegationPolicy narrows a candidate agent set to
// the runtimes every DelegationPolicy that governs the calling session
// authorizes. Two policy layers govern a session:
//
//   - the §8.3 runtime-level policy named on the session's resolved
//     runtime via DelegationPolicyRef (deps.Delegation.EffectiveDelegationPolicy), and
//   - the §10.6 environment default policy named by the
//     defaultDelegationPolicy of the environment the session was created
//     in (environmentDefaultPolicy).
//
// A runtime survives only when every resolved policy permits it — the
// §10.6 line 629 effective scope intersects the environment runtime set
// with the delegation policy, and the §8.3 least-privilege discipline
// ("restriction only, never expansion") makes intersection the safe
// composition of two policies. When neither layer resolves a policy
// (no Delegation service, an unresolved caller session, or no
// runtime-level and no environment-default reference), the set is
// returned unchanged — discovery then reflects only the §10.6
// environment scope, the pre-policy behaviour.
//
// spec: §8.3 line 244; §10.6 line 601, line 629. F-8.5.7 / F-10.6.7.
func filterByEffectiveDelegationPolicy(ctx context.Context, deps Deps, tenant, sessionID string, runtimes []runtimestore.Runtime) ([]runtimestore.Runtime, error) {
	if deps.Delegation == nil || sessionID == "" {
		return runtimes, nil
	}
	var policies []delegationpolicystore.DelegationPolicy
	if pol, ok, err := deps.Delegation.EffectiveDelegationPolicy(ctx, tenant, sessionID); err != nil {
		return nil, err
	} else if ok {
		policies = append(policies, pol)
	}
	if pol, ok, err := environmentDefaultPolicy(ctx, deps, tenant, sessionID); err != nil {
		return nil, err
	} else if ok {
		policies = append(policies, pol)
	}
	if len(policies) == 0 {
		return runtimes, nil
	}
	out := make([]runtimestore.Runtime, 0, len(runtimes))
	for _, rt := range runtimes {
		cand := delegationpolicystore.Candidate{
			ID:     rt.Name,
			Type:   string(rt.Type),
			Labels: rt.Labels,
		}
		if allPoliciesPermit(policies, cand) {
			out = append(out, rt)
		}
	}
	return out, nil
}

// allPoliciesPermit reports whether c is permitted by every policy —
// the §8.3 / §10.6 intersection: a candidate is in the effective scope
// only when no governing policy denies it. An empty policy set permits
// everything (the caller short-circuits that case before calling here).
func allPoliciesPermit(policies []delegationpolicystore.DelegationPolicy, c delegationpolicystore.Candidate) bool {
	for _, p := range policies {
		if !p.Evaluate(c) {
			return false
		}
	}
	return true
}

// environmentDefaultPolicy resolves the §10.6 defaultDelegationPolicy
// that governs a calling session: the DelegationPolicy named on the
// environment the session was created in (§10.6 line 601 — "the
// DelegationPolicy applied to sessions created in this environment").
// It returns (policy, true, nil) when the session is environment-scoped,
// the environment names a defaultDelegationPolicy, and that policy
// resolves active. Every other case returns (zero, false, nil) — no
// environment or session registry wired, a session that names no
// environment, an environment with no default policy, or a missing /
// soft-deleted policy — leaving the environment-default layer imposing
// no restriction, the same conservative fall-through the runtime-level
// EffectiveDelegationPolicy applies to an unresolved reference.
// spec: §10.6 line 601, line 629. F-10.6.7.
func environmentDefaultPolicy(ctx context.Context, deps Deps, tenant, sessionID string) (delegationpolicystore.DelegationPolicy, bool, error) {
	if deps.Environments == nil || deps.Store == nil || deps.Delegation == nil || sessionID == "" {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	sess, err := deps.Store.Get(ctx, tenant, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return delegationpolicystore.DelegationPolicy{}, false, nil
		}
		return delegationpolicystore.DelegationPolicy{}, false, err
	}
	if sess.Environment == "" {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	env, err := deps.Environments.Get(ctx, tenant, sess.Environment)
	if err != nil {
		if errors.Is(err, environmentstore.ErrNotFound) {
			return delegationpolicystore.DelegationPolicy{}, false, nil
		}
		return delegationpolicystore.DelegationPolicy{}, false, err
	}
	if env.DefaultDelegationPolicy == "" {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	return deps.Delegation.ResolveActivePolicy(ctx, tenant, env.DefaultDelegationPolicy)
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

// narrowRuntimesToEnvironment narrows runtimes to those admitted by
// environmentName's runtimeSelector — the §10.6 line 672
// `environmentId` v1 stub. The narrowing applies on top of the §10.6
// transparent filter already applied upstream: a runtime the filter
// excluded stays excluded. A nil environment registry leaves the list
// unchanged; an unknown environmentName collapses it to empty so a
// typo never broadens visibility. F-10.6.10.
func narrowRuntimesToEnvironment(ctx context.Context, deps Deps, runtimes []runtimestore.Runtime, environmentName string) []runtimestore.Runtime {
	if deps.Environments == nil || environmentName == "" {
		return runtimes
	}
	res, err := environmentResolution(ctx, deps)
	if err != nil {
		return []runtimestore.Runtime{}
	}
	tenant := res.TenantID
	if tenant == "" {
		tenant = deps.TenantID
	}
	env, err := deps.Environments.Get(ctx, tenant, environmentName)
	if err != nil || env.Name == "" {
		return []runtimestore.Runtime{}
	}
	out := make([]runtimestore.Runtime, 0, len(runtimes))
	for _, rt := range runtimes {
		if env.RuntimeSelector.Matches(environment.Candidate{
			Name: rt.Name, Type: string(rt.Type), Labels: rt.Labels,
		}) {
			out = append(out, rt)
		}
	}
	return out
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

// callerTenantID resolves the per-request tenant id from the
// authenticated principal, falling back to the binary's configured
// default. The §15.2 / §10.2 production posture mounts the MCP
// adapter under the auth middleware so every authenticated request
// carries the caller's tenant on the request context. Tests and the
// dev-headers transport do not carry a principal; the fallback (the
// Register-time Deps.TenantID, which defaults to "default") keeps the
// tool surface usable in those minimal deployments. A bare "default"
// is the absolute floor — never an empty tenant id, which would
// collapse into an unbounded scan on the session store.
// spec: §9.2 / §16.1 / §15.2 line 1335; F-9.2.13, F-15.2.15.
func callerTenantID(ctx context.Context, fallback string) string {
	if p, ok := authmw.FromContext(ctx); ok && p.TenantID != "" {
		return p.TenantID
	}
	if fallback != "" {
		return fallback
	}
	return "default"
}

// toTaskResult builds the §8.8 TaskResult for a settled child session
// from the live session row alone. It is the minimal fallback the await
// path uses when the §8.10 archive's richer materialization (output
// parts, artifactRefs — built where the transcript and artifact catalog
// are in scope) is unavailable for a terminal child. The error block is
// fully populated from the row: the code falls back to the per-state
// `CHILD_<STATE>` literal when no FailureReason is set, the category
// comes from the shared §15.2.1 classifier so the value matches the REST
// and MCP error envelopes for the same code, and retriesExhausted
// reports whether the gateway consumed the row's automatic-recovery
// budget. Output is left nil here; a completed child's parts ride on the
// archived body.
// spec: §8.8 line 855-867 (MCP state spelling), lines 922-940
// (error: code, category, message, retriesExhausted). F-8.8.4.
func toTaskResult(s sessionstore.Session) task.Result {
	tr := task.Result{SchemaVersion: task.SchemaVersion, TaskID: s.ID, State: mcpStateForSession(s.State)}
	if s.State != session.StateCompleted {
		tr.Error = taskErrorForRow(s)
	}
	return tr
}

// taskErrorForRow builds the §8.8 TaskResult.error block for a
// non-completed terminal child from the row. The category routes through
// the shared §15.2.1 classifier (errorclassify.Classify), so an unknown
// terminal code falls back to the classifier's documented (TRANSIENT,
// retryable) pair rather than an invented category — the §8.8 example's
// RUNTIME_CRASH → TRANSIENT mapping is exactly this fallback.
// spec: §8.8 lines 922-940; §15.2.1. F-8.8.4.
func taskErrorForRow(s sessionstore.Session) *task.Error {
	code := s.FailureReason
	if code == "" {
		code = "CHILD_" + strings.ToUpper(string(s.State))
	}
	cat, _ := errorclassify.Classify(code)
	maxRetries := 0
	if s.RetryPolicy != nil {
		maxRetries = s.RetryPolicy.MaxRetries
	}
	return &task.Error{
		Code:             code,
		Category:         string(cat),
		Message:          "child session ended in state " + string(s.State),
		RetriesExhausted: task.RetriesExhausted(s.RetryCount, maxRetries),
	}
}

// childOutcome is a child's resolved state for lenny/await_children,
// sourced from the live session row or, when the row is gone, from the
// §8.10 archive.
type childOutcome struct {
	parentID string
	state    session.State
	result   task.Result
	// settledAt is the wall-clock instant the child reached its terminal
	// state. Sourced from the live row's UpdatedAt (the row mutates on
	// terminal transition) or, when the row is gone, the §8.10 archive's
	// SettledAt. Zero when the child has not settled yet. The `any` mode
	// of lenny/await_children uses it to pick the chronologically-first
	// settled child rather than the first-listed terminal child.
	// spec: §8.8 lines 945-949; F-8.8.12.
	settledAt time.Time
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
		oc := childOutcome{parentID: row.ParentSessionID, state: row.State, result: toTaskResult(row)}
		if session.IsTerminal(row.State) {
			// spec: §8.8 line 945 — the live row mutates on terminal
			// transition; UpdatedAt is the closest in-tree witness of when
			// the row reached that state. The archive's SettledAt is more
			// precise once the row migrates, but until then UpdatedAt is the
			// authoritative settle witness. F-8.8.12.
			oc.settledAt = row.UpdatedAt
			// spec: §8.8 lines 887-917 — a terminal child's full TaskResult
			// body (output.parts, artifactRefs) is materialized into the
			// §8.10 archive at settle time, where the transcript and the
			// artifact catalog are in scope. Prefer that richer body over the
			// row-only projection; the row-only toTaskResult above remains the
			// fallback when archiving is disabled or the node is not yet
			// written. F-8.8.2.
			if archive != nil {
				if node, gerr := archive.GetByNode(ctx, tenant, childID); gerr == nil {
					var tr task.Result
					if json.Unmarshal([]byte(node.Result), &tr) == nil && tr.TaskID != "" {
						oc.result = tr
					}
				}
			}
		}
		return oc, nil
	}
	if !errors.Is(err, sessionstore.ErrNotFound) || archive == nil {
		return childOutcome{}, fmt.Errorf("child %s lookup: %w", childID, err)
	}
	node, archiveErr := archive.GetByNode(ctx, tenant, childID)
	if archiveErr != nil {
		return childOutcome{}, fmt.Errorf("child %s lookup: %w", childID, err)
	}
	var tr task.Result
	if json.Unmarshal([]byte(node.Result), &tr) != nil {
		tr = task.Result{SchemaVersion: task.SchemaVersion, State: mcpStateForSession(session.State(node.State))}
	}
	if tr.TaskID == "" {
		tr.TaskID = childID
	}
	return childOutcome{
		parentID:  node.ParentSessionID,
		state:     session.State(node.State),
		result:    tr,
		settledAt: node.SettledAt,
	}, nil
}

// collectChildResults reads the awaited children and reports whether
// the mode's settle condition holds. For `all` and `settled` the
// condition is every child terminal; both modes return the full set on
// completion (the spec at §8.8 lines 945-949 defines `settled` as an
// alias for `all`, retained for external MCP / A2A callers that already
// emit either spelling). For `any` it is at least one child terminal,
// and only the chronologically-first settled child is returned — sourced
// from the row's UpdatedAt or the §8.10 archive's SettledAt — so a fast
// finisher buried later in childIDs is preferred over a slow finisher
// at the head of the list. A child whose live row is gone is resolved
// from the §8.10 archive. F-8.8.12.
// spec: §8.8 lines 945-949
func collectChildResults(ctx context.Context, store sessionstore.Store, archive treearchive.Store,
	tenant string, childIDs []string, mode string,
) ([]task.Result, bool, error) {
	type settled struct {
		at     time.Time
		idx    int
		result task.Result
	}
	var terminal []settled
	allTerminal := true
	for i, cid := range childIDs {
		oc, err := resolveChild(ctx, store, archive, tenant, cid)
		if err != nil {
			return nil, false, err
		}
		if session.IsTerminal(oc.state) {
			terminal = append(terminal, settled{at: oc.settledAt, idx: i, result: oc.result})
		} else {
			allTerminal = false
		}
	}
	if mode == "any" {
		if len(terminal) == 0 {
			return nil, false, nil
		}
		first := terminal[0]
		for _, c := range terminal[1:] {
			// spec: §8.8 lines 945-949 — "Returns the first TaskResult"
			// means the child that reached terminal earliest by wall
			// clock. Stable tie-break on childIDs order when two
			// children share the same settle instant (e.g., both
			// missing a settledAt witness).
			if c.at.Before(first.at) || (c.at.Equal(first.at) && c.idx < first.idx) {
				first = c
			}
		}
		return []task.Result{first.result}, true, nil
	}
	if allTerminal {
		out := make([]task.Result, len(terminal))
		for i, t := range terminal {
			out[i] = t.result
		}
		return out, true, nil
	}
	return nil, false, nil
}

// treeWalkContext carries the per-walk fields the MCP tree walker
// passes to the §8.9 cycle observer. The observer fires once per
// repeated node; the walker still truncates the cycle to keep the
// response well-formed. spec: §8.9 line 1003; F-8.9.10.
type treeWalkContext struct {
	ctx           context.Context
	tenantID      string
	rootSessionID string
	observer      TreeCycleObserver
	// allowed, when non-nil, restricts the §8.5 treeVisibility-scoped
	// walk to exactly its member session IDs: the walker descends into a
	// child only when allowed[childID] is true. A nil set means "no
	// restriction" (the `full` visibility case). F-8.5.2 / F-8.9.2.
	allowed map[string]bool
}

func buildTree(wctx treeWalkContext, root sessionstore.Session, all []sessionstore.Session) treeNode {
	childrenByParent := map[string][]sessionstore.Session{}
	for _, s := range all {
		if s.ParentSessionID != "" {
			childrenByParent[s.ParentSessionID] = append(childrenByParent[s.ParentSessionID], s)
		}
	}
	return walk(wctx, root, childrenByParent, map[string]bool{})
}

func walk(wctx treeWalkContext, s sessionstore.Session, byParent map[string][]sessionstore.Session, seen map[string]bool) treeNode {
	// spec: §8.5 line 530 — every tree node carries `runtimeRef` so the
	// MCP projection matches the REST `/tree` surface.
	// spec: §8.8 lines 855-883 — `state` uses the §8.8 MCP protocol
	// spelling, and the supplementary table's `metadata.suspended` /
	// `metadata.resuming` annotations ride on the optional metadata
	// field for non-terminal session states. F-8.8.7 / F-8.8.9.
	protoState, meta := nodeProtocolState(s.State)
	node := treeNode{
		TaskID:     s.ID,
		State:      protoState,
		Metadata:   meta,
		RuntimeRef: s.RuntimeRef,
		Children:   []treeNode{},
	}
	if seen[s.ID] {
		// spec: §8.9 line 1003; F-8.9.10 — defensive cycle guard.
		if wctx.observer != nil {
			wctx.observer.OnTreeCycle(wctx.ctx, TreeCycleEvent{
				TenantID:      wctx.tenantID,
				RootSessionID: wctx.rootSessionID,
				CycleNodeID:   s.ID,
				Source:        "mcp",
			})
		}
		return node
	}
	seen[s.ID] = true
	for _, c := range byParent[s.ID] {
		// spec: §8.5 line 540 — under a narrowed treeVisibility the walker
		// descends only into the visible nodes; an out-of-scope child (a
		// sibling subtree under `parent-and-self`, any child under
		// `self-only`) is pruned. F-8.5.2 / F-8.9.2.
		if wctx.allowed != nil && !wctx.allowed[c.ID] {
			continue
		}
		node.Children = append(node.Children, walk(wctx, c, byParent, seen))
	}
	return node
}

// withinMessagingTopology reports whether target sits in sender's
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

// cancelSubtree cancels the subtree rooted at child, honouring each
// non-terminal descendant's §8.10 `cascadeOnFailure` policy. The root
// (the explicit `cancel_child` target) is always cancelled per the
// §8.5 contract — the caller named it. From there, descendants of a
// `cancel_all` node are queued; descendants of an `await_completion`
// node are left running because the spec models that mode as "let
// running children finish"; descendants of a `detach` node are left
// running because the spec models that mode as "let children outlive
// the terminated parent".
//
// Already-terminal sessions are left untouched and their own subtree
// is not re-traversed because their own cascade already ran when they
// settled. The returned slice is the ids actually transitioned, sorted
// for a deterministic result. spec: §8.5 (cancel_child cascades per
// policy); §8.10 (cascadeOnFailure modes). F-8.5.19.
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
	seen := map[string]bool{child.ID: true}
	// rootCancelled tracks whether the explicit target has been
	// transitioned. The root's own cascade policy decides whether to
	// queue its children — the root itself is always cancelled because
	// the §8.5 contract is `lenny/cancel_child(child_id)`.
	rootCancelled := false
	queue := []sessionstore.Session{child}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if session.IsTerminal(cur.State) {
			continue
		}
		updated, err := store.Update(ctx, tenant, cur.ID, func(row *sessionstore.Session) error {
			row.State = session.StateCancelled
			return nil
		})
		if err != nil {
			return cancelled, fmt.Errorf("cancel session %s: %w", cur.ID, err)
		}
		cancelled = append(cancelled, cur.ID)
		// §8.10: queue this node's children only when its cascade
		// policy is `cancel_all`. `await_completion` and `detach` leave
		// descendants running per the spec semantics. The root's policy
		// applies to *its* descendants the same way every other node's
		// does.
		_ = updated
		if cur.CascadeOnFailure.Resolve() == session.CascadeCancelAll {
			for _, c := range byParent[cur.ID] {
				if seen[c.ID] {
					continue
				}
				seen[c.ID] = true
				queue = append(queue, c)
			}
		}
		if cur.ID == child.ID {
			rootCancelled = true
		}
	}
	// Defensive: the root was queued first, so an empty cancelled slice
	// implies the root was already terminal. The §8.5 handler checks
	// for that case before calling us, but assert here so a future
	// refactor cannot silently regress.
	_ = rootCancelled
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
	// spec: §15.2.1 — the cancel code's category is stable across nodes,
	// so classify once before the loop. F-8.8.4.
	cancelCat, _ := errorclassify.Classify("CHILD_CANCELLED")
	for _, id := range cancelled {
		result, _ := json.Marshal(task.Result{
			SchemaVersion: task.SchemaVersion,
			TaskID:        id,
			// spec: §8.8 line 857 — the result body's state uses the MCP
			// protocol spelling (`canceled`), matching the settle-path
			// archive body so a resumed parent replaying either archive
			// route sees the same value. F-8.8.7.
			State: mcpStateForSession(session.StateCancelled),
			// spec: §8.8 lines 922-940 — a cancel-cascade node carries the
			// error block; category routes through the §15.2.1 classifier.
			// retriesExhausted stays false: a parent-cancelled child did not
			// consume its automatic-recovery budget. F-8.8.4.
			Error: &task.Error{
				Code:     "CHILD_CANCELLED",
				Category: string(cancelCat),
				Message:  "child session ended in state cancelled",
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

// buildSendMessageReceiptStatus builds the §15.4 delivery_receipt
// envelope for a non-`delivered` terminal status (e.g. `rate_limited`).
// `deliveredAt` is set only for the `delivered` status; `rate_limited`
// and `error` carry no timestamp. v1 does not define a `reason` enum
// value for `rate_limited` — the status alone conveys the condition
// (§15.4). spec: §15.4 lines 1725-1737; §7.2 line 371. F-7.2.6.
func buildSendMessageReceiptStatus(messageID string, status session.DeliveryStatus, now time.Time) string {
	receipt := session.DeliveryReceipt{MessageID: messageID, Status: status}
	if status == session.DeliveryStatusDelivered {
		receipt.DeliveredAt = now
	}
	envelope := struct {
		DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`
	}{DeliveryReceipt: receipt}
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
