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
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
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

	// Events is the §15.1 session event bus. Optional — when nil, the
	// lenny/output tool is not registered.
	Events *events.Bus

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

	// ElicitationDepthPolicy is the §9.2 depth policy applied to an
	// agent-initiated elicitation. An unset or invalid value resolves
	// to `allow_all` (no depth suppression).
	ElicitationDepthPolicy elicitation.DepthPolicy

	// ElicitationSuppressAtDepth is the §9.2 delegation depth at which
	// the `suppress_at_depth` policy starts suppressing elicitations.
	ElicitationSuppressAtDepth int

	// Clock + IDFunc match the session server's construction; pass
	// nil for production defaults.
	Clock  func() time.Time
	IDFunc func() string

	// TenantID is the tenant the MCP session operates within. The
	// MCP adapter is mounted per-tenant; v1 binds one tenant per
	// adapter instance.
	TenantID string
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

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/create_session",
		Description: "Create a new agent session against a runtime.",
		InputSchema: json.RawMessage(`{"type":"object","required":["runtimeRef"],"properties":{"runtimeRef":{"type":"string"},"userId":{"type":"string"},"environment":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			RuntimeRef  string `json:"runtimeRef"`
			UserID      string `json:"userId"`
			Environment string `json:"environment"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.RuntimeRef == "" {
			return mcp.ToolResult{}, errors.New("runtimeRef is required")
		}
		now := clock()
		row := sessionstore.Session{
			ID:               idFn(),
			TenantID:         tenant,
			UserID:           in.UserID,
			RuntimeRef:       in.RuntimeRef,
			Environment:      in.Environment,
			State:            session.StateRunning,
			IsolationProfile: isolation.Default(),
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
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","content"],"properties":{"sessionId":{"type":"string"},"content":{"type":"string"},"inReplyTo":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID string `json:"sessionId"`
			Content   string `json:"content"`
			InReplyTo string `json:"inReplyTo"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		row, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		if session.IsTerminal(row.State) {
			return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", in.SessionID, row.State)
		}
		// §8.5: when the message answers a pending lenny/request_input
		// call, it resolves that blocked call directly instead of being
		// delivered to the runtime. A non-matching inReplyTo falls
		// through to normal delivery — it is then an ordinary threaded
		// message.
		if in.InReplyTo != "" && deps.InputWaits != nil {
			err := deps.InputWaits.Resolve(in.SessionID, in.InReplyTo, in.Content)
			if err == nil {
				return textResult(fmt.Sprintf(`{"resolved":%q}`, in.InReplyTo)), nil
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
		messageBody := in.Content
		if deps.Interceptors != nil {
			res := deps.Interceptors.Run(ctx, interceptor.Request{
				Phase:     interceptor.PhasePreMessageDelivery,
				SessionID: row.ID,
				TenantID:  tenant,
				Content:   []byte(in.Content),
			})
			if res.Action == interceptor.ActionReject {
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
		content := make([]mcp.ToolContent, 0, len(out))
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
		Name:        "lenny/cancel_child",
		Description: "Cancel a child session and cascade the cancellation to its descendants (§8.5).",
		InputSchema: json.RawMessage(`{"type":"object","required":["parentSessionId","childSessionId"],"properties":{"parentSessionId":{"type":"string"},"childSessionId":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			ParentSessionID string `json:"parentSessionId"`
			ChildSessionID  string `json:"childSessionId"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.ParentSessionID == "" || in.ChildSessionID == "" {
			return mcp.ToolResult{}, errors.New("parentSessionId and childSessionId are required")
		}
		if in.ParentSessionID == in.ChildSessionID {
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
		if !isDescendant(child, in.ParentSessionID, all) {
			return mcp.ToolResult{}, fmt.Errorf("session %s is not a child of %s",
				in.ChildSessionID, in.ParentSessionID)
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
			Name:        "lenny/output",
			Description: "Emit output parts to a session's event stream (§8.5).",
			InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","output"],"properties":{"sessionId":{"type":"string"},"output":{"type":"array","items":{"type":"object"}}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				SessionID string          `json:"sessionId"`
				Output    json.RawMessage `json:"output"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.SessionID == "" {
				return mcp.ToolResult{}, errors.New("sessionId is required")
			}
			var parts []json.RawMessage
			if err := json.Unmarshal(in.Output, &parts); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("output must be an array of output parts: %w", err)
			}
			if len(parts) == 0 {
				return mcp.ToolResult{}, errors.New("output must contain at least one part")
			}
			row, err := deps.Store.Get(ctx, tenant, in.SessionID)
			if err != nil {
				return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
			}
			if session.IsTerminal(row.State) {
				return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", in.SessionID, row.State)
			}
			// §15.4.1: lenny/output parts are surfaced on the session's
			// event stream as an agent_output event, the same stream the
			// §15.1 GET /v1/sessions/{id}/events SSE relay reads.
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
			Name:        "lenny/request_input",
			Description: "Block until a peer answers via lenny/send_message with a matching inReplyTo (§8.5).",
			InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","requestId"],"properties":{"sessionId":{"type":"string"},"requestId":{"type":"string"},"prompt":{"type":"string"}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				SessionID string `json:"sessionId"`
				RequestID string `json:"requestId"`
				Prompt    string `json:"prompt"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.SessionID == "" || in.RequestID == "" {
				return mcp.ToolResult{}, errors.New("sessionId and requestId are required")
			}
			row, err := deps.Store.Get(ctx, tenant, in.SessionID)
			if err != nil {
				return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
			}
			if session.IsTerminal(row.State) {
				return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", in.SessionID, row.State)
			}
			ch, err := deps.InputWaits.Register(in.SessionID, in.RequestID)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			// §8.5: surface the question on the session event stream so a
			// peer can see what input is being asked for.
			if deps.Events != nil {
				data, _ := json.Marshal(struct {
					RequestID string `json:"requestId"`
					Prompt    string `json:"prompt"`
				}{RequestID: in.RequestID, Prompt: in.Prompt})
				deps.Events.Publish(in.SessionID, "request_input", string(data), clock())
			}
			// Block in the §7.2 input_required sub-state until a peer
			// resolves the request or the §11.3 timeout fires.
			select {
			case answer := <-ch:
				body, _ := json.Marshal(struct {
					RequestID string `json:"requestId"`
					Answer    string `json:"answer"`
				}{RequestID: in.RequestID, Answer: answer})
				return textResult(string(body)), nil
			case <-time.After(requestInputTimeout):
				deps.InputWaits.Cancel(in.SessionID, in.RequestID)
				return mcp.ToolResult{}, fmt.Errorf(
					"REQUEST_INPUT_TIMEOUT: no input arrived for %s within %s", in.RequestID, requestInputTimeout)
			case <-ctx.Done():
				deps.InputWaits.Cancel(in.SessionID, in.RequestID)
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
		srv.RegisterTool(mcp.Tool{
			Name:        "lenny/request_elicitation",
			Description: "Request human input via the §9.2 elicitation chain and block until it resolves.",
			InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","message"],"properties":{"sessionId":{"type":"string"},"message":{"type":"string"},"schema":{"type":"object"},"elicitationId":{"type":"string"}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				SessionID     string          `json:"sessionId"`
				Message       string          `json:"message"`
				Schema        json.RawMessage `json:"schema"`
				ElicitationID string          `json:"elicitationId"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.SessionID == "" || in.Message == "" {
				return mcp.ToolResult{}, errors.New("sessionId and message are required")
			}
			row, err := deps.Store.Get(ctx, tenant, in.SessionID)
			if err != nil {
				return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
			}
			if session.IsTerminal(row.State) {
				return mcp.ToolResult{}, fmt.Errorf("session %s is terminal (%s)", in.SessionID, row.State)
			}
			// §9.1: the per-session elicitation budget bounds how many
			// elicitations an agent may raise — an over-budget request
			// is dropped so an agent cannot spam the user.
			count, err := deps.Interactions.CountElicitations(ctx, tenant, in.SessionID)
			if err != nil {
				return mcp.ToolResult{}, err
			}
			if count >= maxElicitations {
				if deps.ElicitationMetrics != nil {
					deps.ElicitationMetrics.RecordElicitationDrop(elicitationDropBudgetExceeded)
				}
				return mcp.ToolResult{}, fmt.Errorf(
					"elicitation budget exhausted: session %s has reached the maxElicitationsPerSession limit of %d",
					in.SessionID, maxElicitations)
			}
			// §9.2: the depth policy suppresses an agent elicitation
			// raised too deep in the delegation tree.
			depthPolicy := deps.ElicitationDepthPolicy
			if !depthPolicy.IsValid() {
				depthPolicy = elicitation.DepthAllowAll
			}
			depth := sessionDepth(ctx, deps.Store, tenant, row)
			if depthPolicy.ShouldSuppress(elicitation.InitiatorAgent, depth, deps.ElicitationSuppressAtDepth) {
				if deps.ElicitationMetrics != nil {
					deps.ElicitationMetrics.RecordElicitationDrop(elicitationDropDepthSuppressed)
				}
				return mcp.ToolResult{}, fmt.Errorf(
					"elicitation suppressed: the %q depth policy suppresses an agent elicitation at delegation depth %d",
					depthPolicy, depth)
			}
			elicitationID := in.ElicitationID
			if elicitationID == "" {
				elicitationID = idFn()
			}
			detail := map[string]any{"message": in.Message}
			if len(in.Schema) > 0 {
				var schema any
				if json.Unmarshal(in.Schema, &schema) == nil {
					detail["schema"] = schema
				}
			}
			if err := deps.Interactions.Put(ctx, interactionstore.Interaction{
				ID:        elicitationID,
				Kind:      interactionstore.KindElicitation,
				SessionID: in.SessionID,
				TenantID:  tenant,
				UserID:    row.UserID,
				Phase:     interactionstore.PhasePending,
				Detail:    detail,
			}); err != nil {
				return mcp.ToolResult{}, err
			}
			// §9.2: surface the elicitation on the session event stream so
			// the human responder (and the elicitation chain) can see it.
			if deps.Events != nil {
				data, _ := json.Marshal(struct {
					ElicitationID string `json:"elicitationId"`
					Message       string `json:"message"`
				}{ElicitationID: elicitationID, Message: in.Message})
				deps.Events.Publish(in.SessionID, "elicitation_requested", string(data), clock())
			}
			// Block until a human resolves the elicitation or the §9.1
			// maxElicitationWait timeout fires.
			ticker := time.NewTicker(awaitPollInterval)
			defer ticker.Stop()
			timeout := time.After(elicitationTimeout)
			for {
				cur, err := deps.Interactions.Get(ctx, tenant, in.SessionID, row.UserID, elicitationID)
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
					// §9.1: on timeout the elicitation is dismissed and the
					// agent receives a timeout error.
					_, _ = deps.Interactions.Resolve(ctx, tenant, in.SessionID, row.UserID, elicitationID,
						func(i *interactionstore.Interaction) error {
							if i.Phase == interactionstore.PhasePending {
								i.Phase = interactionstore.PhaseDismissed
								i.Reason = "ELICITATION_TIMEOUT"
							}
							return nil
						})
					return mcp.ToolResult{}, fmt.Errorf(
						"ELICITATION_TIMEOUT: no response for %s within %s", elicitationID, elicitationTimeout)
				case <-ticker.C:
				}
			}
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
			InputSchema: json.RawMessage(`{"type":"object","required":["parentSessionId","runtimeRef"],"properties":{"parentSessionId":{"type":"string"},"runtimeRef":{"type":"string"},"poolRef":{"type":"string"},"maxDepth":{"type":"integer"},"taskInput":{"type":"string"}}}`),
		}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			var in struct {
				ParentSessionID string `json:"parentSessionId"`
				RuntimeRef      string `json:"runtimeRef"`
				PoolRef         string `json:"poolRef"`
				MaxDepth        int    `json:"maxDepth"`
				TaskInput       string `json:"taskInput"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
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
					in.RuntimeRef)
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
					return mcp.ToolResult{}, fmt.Errorf("delegation rejected by policy: %s", res.Reason)
				}
				if res.Action == interceptor.ActionModify {
					taskInput = string(res.ModifiedContent)
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
						deps.Audit.EmitDelegationEvent(ctx, "delegation.isolation_violation", map[string]any{
							"parentSessionId":   in.ParentSessionID,
							"runtimeRef":        in.RuntimeRef,
							"poolRef":           in.PoolRef,
							"parentProfile":     string(isoErr.ParentProfile),
							"childProfile":      string(isoErr.ChildProfile),
							"cross_environment": viaCrossEnv,
						})
					}
					return mcp.ToolResult{}, fmt.Errorf("ISOLATION_MONOTONICITY_VIOLATED: %w", err)
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
			return textResult(fmt.Sprintf(`{"childSessionId":%q,"depth":%d}`, res.Child.ID, res.Depth)), nil
		})
	}

	if deps.Memory != nil {
		registerMemoryTools(srv, deps, tenant, clock)
	}
}

// registerMemoryTools wires the §9.4 lenny/memory_write and
// lenny/memory_query platform MCP tools. A memory is written under
// the calling session's user, runtime, and session scope; a query
// recalls across all of the user's sessions within the tenant.
func registerMemoryTools(srv *mcp.Server, deps Deps, tenant string, _ func() time.Time) {
	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/memory_write",
		Description: "Write a memory to the §9.4 memory store, scoped to the calling session's user.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId","content"],"properties":{"sessionId":{"type":"string"},"content":{"type":"string"},"metadata":{"type":"object"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID string         `json:"sessionId"`
			Content   string         `json:"content"`
			Metadata  map[string]any `json:"metadata"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.SessionID == "" || in.Content == "" {
			return mcp.ToolResult{}, errors.New("sessionId and content are required")
		}
		row, err := deps.Store.Get(ctx, tenant, in.SessionID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("session lookup: %w", err)
		}
		scope := memorystore.MemoryScope{
			TenantID: tenant, UserID: row.UserID,
			AgentType: row.RuntimeRef, SessionID: row.ID,
		}
		if err := deps.Memory.Write(ctx, scope, []memorystore.Memory{
			{Content: in.Content, Metadata: in.Metadata},
		}); err != nil {
			return mcp.ToolResult{}, fmt.Errorf("memory write: %w", err)
		}
		return textResult(`{"written":1}`), nil
	})

	srv.RegisterTool(mcp.Tool{
		Name:        "lenny/memory_query",
		Description: "Query the §9.4 memory store across the calling session's user's memories.",
		InputSchema: json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct {
			SessionID string `json:"sessionId"`
			Query     string `json:"query"`
			Limit     int    `json:"limit"`
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
		// §9.4: memory recall is user-scoped — it spans every session
		// the user has run, not just the calling session.
		results, err := deps.Memory.Query(ctx,
			memorystore.MemoryScope{TenantID: tenant, UserID: row.UserID}, in.Query, in.Limit)
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

// filterByEnvironmentAccess applies §10.6 transparent filtering to a
// runtime list: it returns only the runtimes the caller's environment
// membership authorizes. Filtering runs when the environment and
// tenant registries are wired and the request context carries an
// authenticated principal; otherwise the list is returned unchanged
// (a minimal deployment with no environment registry). A caller in no
// environment is governed by the tenant's §10.6 noEnvironmentPolicy.
func filterByEnvironmentAccess(ctx context.Context, deps Deps, runtimes []runtimestore.Runtime) ([]runtimestore.Runtime, error) {
	if deps.Environments == nil || deps.Tenants == nil {
		return runtimes, nil
	}
	principal, ok := authmw.FromContext(ctx)
	if !ok || principal.TenantID == "" {
		return runtimes, nil
	}
	envs, err := deps.Environments.List(ctx, principal.TenantID)
	if err != nil {
		return nil, err
	}
	tenant, err := deps.Tenants.Get(ctx, principal.TenantID)
	if err != nil {
		return nil, err
	}
	// §10.6: the per-tenant noEnvironmentPolicy takes precedence; an
	// unset tenant value falls back to the platform-wide default.
	policy := tenant.NoEnvironmentPolicy
	if policy == "" {
		policy = deps.DefaultNoEnvironmentPolicy
	}
	caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
	return envaccess.AuthorizedRuntimes(caller, envs, runtimes, policy), nil
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
	envs, err := deps.Environments.List(ctx, parent.TenantID)
	if err != nil {
		return false, err
	}
	// §10.6: the parent session's environment is caller-supplied at
	// session creation and is only trusted here when the caller is
	// genuinely a member of it. Without this check a session tagged
	// with an environment the caller does not belong to could borrow
	// that environment's cross-environment delegation reach.
	principal, ok := authmw.FromContext(ctx)
	if !ok {
		return false, nil
	}
	var parentEnv environmentstore.Environment
	for _, e := range envs {
		if e.Name == parent.Environment {
			parentEnv = e
		}
	}
	caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
	if _, isMember := envaccess.Membership(caller, parentEnv); !isMember {
		return false, nil
	}
	return envaccess.CrossEnvironmentReachable(parent.Environment, rt, envs), nil
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
	tenant, childID string) (childOutcome, error) {

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
	tenant string, childIDs []string, mode string) ([]taskResult, bool, error) {

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
	child sessionstore.Session, all []sessionstore.Session) ([]string, error) {

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

// sessionDepth returns sess's delegation depth — the number of hops
// from sess up to the §8 delegation tree root (root = 0). The seen set
// guards against a malformed cyclic chain.
func sessionDepth(ctx context.Context, store sessionstore.Store, tenant string, sess sessionstore.Session) int {
	depth := 0
	cur := sess
	seen := map[string]bool{}
	for cur.ParentSessionID != "" && !seen[cur.ID] {
		seen[cur.ID] = true
		parent, err := store.Get(ctx, tenant, cur.ParentSessionID)
		if err != nil {
			break
		}
		cur = parent
		depth++
	}
	return depth
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
	tenant, rootSessionID string, cancelled []string, all []sessionstore.Session, now time.Time) {

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
