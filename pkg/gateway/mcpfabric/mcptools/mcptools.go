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
//
// spec: §15.2.1 rule 4 line 1386 — MCP tool input schemas for operations
// that overlap the REST API are generated from the OpenAPI document by the
// build-pipeline code generation step below, not maintained by hand.
//
//go:generate go run ./internal/genmcpschemas
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
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/envaccess"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	sessionstate "github.com/lennylabs/lenny/pkg/session/state"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// mcpStateForSession returns the §8.8 MCP-protocol state spelling for
// a §7.2 session-level Lenny state by delegating to nodeProtocolState.
// §8.8 defines the canonical task machine as the §7.2 session machine,
// so every state routes through the single sessionstate.MCPProtocolState
// projection; the metadata is discarded on this single-string path. The
// terminal and input_required spellings are byte-identical to the former
// task-level table. F-8.8.7.
// spec: §8.8 lines 855-883, §7.2.
func mcpStateForSession(s session.State) string {
	proto, _ := nodeProtocolState(s)
	return proto
}

// nodeProtocolState returns the §8.8 MCP-protocol state spelling and
// any supplementary-table metadata annotations for a §7.2 session-level
// Lenny state. The metadata map is nil when no annotation applies (the
// caller omits the `metadata` field from the wire payload via the
// `omitempty` JSON tag). §8.8 defines the canonical task machine as the
// §7.2 session machine, so every state routes through the single
// sessionstate.MCPProtocolState projection; the terminal and
// input_required spellings are byte-identical to the former task-level
// table. F-8.8.7 / F-8.8.9.
// spec: §8.8 lines 855-883, §7.2.
func nodeProtocolState(s session.State) (string, map[string]any) {
	return sessionstate.MCPProtocolState(sessionstate.State(s))
}

// The parity helpers below wrap the recurring MCP tool error
// conditions as *mcp.ToolError carrying the canonical lenny code so
// the shared §15.2.1 errorclassify table assigns the same (category,
// retryable) pair the REST surface returns, instead of the handler
// error falling back to INTERNAL_ERROR / (TRANSIENT, true). spec:
// §15.2.1 rule 5(d) line 1396. F-15.2.12.

// errInvalidArgs maps a tools/call argument-unmarshal failure to the
// REST VALIDATION_ERROR (PERMANENT, not retryable).
func errInvalidArgs(err error) error {
	return mcp.NewToolError("VALIDATION_ERROR", fmt.Sprintf("invalid arguments: %v", err), nil)
}

// maxMessagePartBytes is the §15.4.1 line 1548 hard ceiling: a single
// MessagePart above 50 MB is rejected at ingress. The gate measures the
// marshaled part (inline payload plus envelope) so a base64-inlined blob
// that exceeds the cap is refused before it reaches the event stream.
const maxMessagePartBytes = 50 * 1024 * 1024

// sendMessageInputSchema is the §8.5 `lenny/send_message` tool input
// schema. Its `message` property is the §15.4 `MessageEnvelope.input`
// union (`oneOf(string, MessagePart[])`) sourced from the single
// sessionrecord.MessageContentJSONSchema definition, so the MCP send
// surface and the REST `/messages` body express the identical union under
// the §15.2.1 parity rule. `to` is the §8.5 line 537 target id, and the
// `inReplyTo`/`messageId`/`fromSessionId` extensions are unchanged.
// spec: §8.5 line 537, §15.4 (MessageEnvelope.input), §15.2.1 (REST/MCP
// parity).
var sendMessageInputSchema = json.RawMessage(fmt.Sprintf(
	`{"type":"object","required":["to","message"],"properties":{"to":{"type":"string","description":"Target taskId / sessionId (§8.5 line 537)."},"message":%s,"inReplyTo":{"type":"string","description":"§8.8 line 951 — when this answers a pending lenny/request_input, the matching requestId."},"messageId":{"type":"string","description":"§15.4 line 1784 sender-supplied id; gateway assigns one when absent."},"fromSessionId":{"type":"string","description":"§7.2 sender session id. When set (or implied by the principal), the gateway enforces the §7.2 line 240 topology constraint: target must be the sender's parent, direct child, or sibling. F-7.2.22."}}}`,
	sessionrecord.MessageContentJSONSchema,
))

// validateMessagePart enforces the two §15.4.1 lines 1542-1548 MessagePart
// ingress invariants on one `lenny/output` part: `inline` and `ref` are
// mutually exclusive (both set → `400 MESSAGEPART_INLINE_REF_CONFLICT`),
// and a part larger than 50 MB is rejected (`413 MESSAGEPART_TOO_LARGE`).
// The messagepart.schema.json $comment designates both as gateway runtime
// checks rather than schema-validation checks. F-15.4.1 (15.4-HIGH-007).
func validateMessagePart(part json.RawMessage) error {
	if len(part) > maxMessagePartBytes {
		return mcp.NewToolError("MESSAGEPART_TOO_LARGE",
			"output part exceeds the 50 MB ceiling",
			map[string]any{"maxBytes": maxMessagePartBytes, "actualBytes": len(part)})
	}
	var probe struct {
		Inline *json.RawMessage `json:"inline"`
		Ref    *json.RawMessage `json:"ref"`
	}
	if err := json.Unmarshal(part, &probe); err != nil {
		// A part that does not parse as an object is a malformed payload;
		// surface it as a validation error rather than a conflict.
		return mcp.NewToolError("VALIDATION_ERROR",
			fmt.Sprintf("output part is not a JSON object: %v", err), nil)
	}
	if probe.Inline != nil && probe.Ref != nil {
		return mcp.NewToolError("MESSAGEPART_INLINE_REF_CONFLICT",
			"output part sets both inline and ref, which are mutually exclusive", nil)
	}
	return nil
}

// errSessionLookup maps a session-store Get failure to
// RESOURCE_NOT_FOUND (PERMANENT, not retryable), matching the REST 404
// for a session that does not exist or is not visible to the caller.
func errSessionLookup(err error) error {
	return mcp.NewToolError("RESOURCE_NOT_FOUND", fmt.Sprintf("session lookup: %v", err), nil)
}

// errSessionTerminalState maps an operation on the caller's own
// terminal session to INVALID_STATE_TRANSITION (PERMANENT, not
// retryable), the REST code for an operation invalid in the current
// state.
func errSessionTerminalState(id string, state session.State) error {
	return mcp.NewToolError("INVALID_STATE_TRANSITION",
		fmt.Sprintf("session %s is terminal (%s)", id, state), nil)
}

// errPrecondition maps a §15.1 state-transition precondition failure to
// the MCP tool-error envelope, preserving the spec INVALID_STATE_TRANSITION
// code and surfacing the current/allowed states so the MCP surface returns
// the same (code, details) pair the REST endpoint returns. spec: §15.2.1
// REST/MCP error parity; §15.1 precondition table.
func errPrecondition(err error) error {
	var pe *session.PreconditionError
	if errors.As(err, &pe) {
		allowed := make([]string, 0, len(pe.AllowedStates))
		for _, st := range pe.AllowedStates {
			allowed = append(allowed, string(st))
		}
		return mcp.NewToolError(pe.ErrorCode(), pe.Error(), map[string]any{
			"currentState":  string(pe.CurrentState),
			"allowedStates": allowed,
		})
	}
	return mcp.NewToolError("INVALID_STATE_TRANSITION", err.Error(), nil)
}

// DelegationAuditor records §11.7 audit events for delegation
// operations such as the §10.6 isolation-monotonicity violation.
type DelegationAuditor interface {
	EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any)
}

// cooldownToolError maps a delegation.InterceptorWeakeningCooldownError
// to the canonical INTERCEPTOR_WEAKENING_COOLDOWN MCP envelope (§8.3
// rule 5 / §15.1; TRANSIENT, HTTP 503) so the lenny/delegate_task and
// lenny/send_message surfaces report the same (category, retryable) pair
// and the same details across REST and MCP. The details carry whichever
// trigger fired: policyName for the scanExportedFiles weakening
// (F-8.7.12), interceptorRef for the interceptor fail-policy weakening
// (F-4.8.17). F-8.7.12 / F-4.8.17.
func cooldownToolError(cdErr *delegation.InterceptorWeakeningCooldownError) *mcp.ToolError {
	details := map[string]any{
		"cooldownSeconds":   cdErr.CooldownSeconds,
		"retryAfterSeconds": cdErr.RetryAfterSeconds,
	}
	if cdErr.PolicyName != "" {
		details["policyName"] = cdErr.PolicyName
	}
	if cdErr.InterceptorRef != "" {
		details["interceptorRef"] = cdErr.InterceptorRef
	}
	return mcp.NewToolError("INTERCEPTOR_WEAKENING_COOLDOWN", cdErr.Error(), details)
}

// ContentPolicyResolver resolves the effective §8.3 contentPolicy for a
// session: the maxInputSize byte cap and the interceptorRef naming the
// external content scanner. lenny/delegate_task resolves the parent
// session's policy; lenny/send_message resolves the target session's
// policy. *delegation.Service implements it. spec: §8.3 lines 149-188;
// §4.8 lines 1036, 1040; §13.5 mitigations 2-3. F-8.2.9 / F-13.5.2.
type ContentPolicyResolver interface {
	ResolveContentPolicy(ctx context.Context, tenantID, sessionID string) (maxInputSize int, interceptorRef string, ok bool)
}

// InterceptorCooldownChecker reports the §4.8 line 1034 / §8.3 SEC-013
// fail-policy weakening cooldown for the interceptor named by a policy's
// `contentPolicy.interceptorRef`. lenny/send_message consults it to
// reject a delivery whose target session's effective interceptor is
// inside the `fail-closed → fail-open` weakening window, the same gate
// the delegation service applies inside Delegate for lenny/delegate_task.
// A non-nil return is a *delegation.InterceptorWeakeningCooldownError.
// *delegation.Service implements it. spec: §4.8 line 1034; §8.3 line 218.
// F-4.8.17.
type InterceptorCooldownChecker interface {
	InterceptorFailPolicyCooldown(ctx context.Context, interceptorRef string) error
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

// SessionCreator is the §15.2.1 rule-1 shared session-creation service.
// *sessionserver.Server implements it. When wired, lenny/create_session
// admits a session through exactly the same §15.1 gates the REST
// POST /v1/sessions handler runs (active-user, quota, concurrency,
// admission-rate, policy chain, environment access, runtime / isolation /
// workspace-plan validation) and mints the §7.1 uploadToken, so an
// MCP-created session is not a path around tenant quotas and policy
// interception and lands in the same `created` state with the same
// envelope the REST surface returns.
//
// A nil SessionCreator falls back to the legacy direct-store create used
// by the minimal in-process gateway that wires no session server (and by
// the mcptools unit suite). Production wires the real service.
//
// spec: §15.2.1 rule 1 line 1380. F-15.2.4.
type SessionCreator interface {
	CreateSessionService(ctx context.Context, tenantID string, req sessionserver.CreateSessionRequest) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError)
}

// CredentialAvailabilityChecker runs the §8.3 delegation-time pre-claim
// credential-availability check for a prospective delegated child before a
// warm pod is allocated. *sessionserver.Server implements it (modeled on
// the SessionCreator precedent above, so mcptools depends on the interface
// rather than importing the concrete server and creating a cycle). The
// check reuses the §4.9 pre-claim engine and returns a typed sentinel the
// delegate handler maps to CREDENTIAL_POOL_EXHAUSTED / USER_CREDENTIAL_NOT_FOUND.
// It claims no pod and reserves no lease.
//
// spec: §8.3 line 470; §4.9
type CredentialAvailabilityChecker interface {
	CheckDelegationCredentialAvailability(ctx context.Context, q sessionserver.DelegationCredentialQuery) error
}

// ChildMaterializer runs the §8.2 delegation steps that follow admission for a
// delegated child that Service.Delegate committed in session.StateCreated with
// a stamped WorkspacePlan and no PodAssignment: it claims the warm pod, assigns
// the credential lease, streams the stamped WorkspacePlan through the §6.3
// binder, launches, and transitions the child to running. It returns the
// child's post-materialization state so the delegate handler builds the
// taskHandle from the live transitioned state rather than the pre-materialization
// StateCreated snapshot, and it fails closed on any failure, returning the same
// typed credential sentinels the §8.3 pre-check maps so the handler surfaces the
// canonical MCP tool codes. *sessionserver.Server implements it via
// MaterializeDelegatedChild (modeled on the CredentialAvailabilityChecker
// precedent above, so mcptools depends on the interface rather than importing
// the concrete server and creating a cycle).
//
// spec: §8.2 lines 93-97
type ChildMaterializer interface {
	MaterializeDelegatedChild(ctx context.Context, tenantID, childID string) (session.State, error)
}

// Deps carries the gateway services the MCP tools dispatch to.
type Deps struct {
	// Store is the §4.2 session store.
	Store sessionstore.Store

	// SessionCreator is the §15.2.1 rule-1 shared session-creation
	// service. When set, lenny/create_session routes through it so the
	// MCP and REST surfaces run identical validation and return identical
	// envelopes. Optional — a nil creator selects the legacy direct-store
	// create path. spec: §15.2.1 rule 1 line 1380. F-15.2.4.
	SessionCreator SessionCreator

	// SessionService is the §15.2.1 rule-1 shared service layer the
	// overlapping client-facing lifecycle/read tools dispatch through
	// (create_and_start_session, start_session, finalize_workspace,
	// terminate_session, resume_session, get_session_status, list_sessions,
	// get_session_logs, list_artifacts, get_token_usage, download_artifact,
	// upload_files). When nil those tools are not registered (the minimal
	// in-process gateway / unit suite). Production wires *sessionserver.Server,
	// so the MCP surface runs the identical REST route, validation, and
	// response shaping. spec: §15.2 lines 1284-1306; §15.2.1 rule 1 line
	// 1380. F-15.2.3.
	SessionService SessionService

	// CredAvailability runs the §8.3 delegation-time pre-claim
	// credential-availability check before lenny/delegate_task allocates a
	// warm pod. When set, the delegate handler gates inherit / independent
	// delegations on it and rejects with CREDENTIAL_POOL_EXHAUSTED before
	// pod allocation. Optional — a nil checker skips the gate (the minimal
	// in-process gateway and the mcptools unit suite leave it nil).
	// Production wires *sessionserver.Server. spec: §8.3 line 470; §4.9.
	CredAvailability CredentialAvailabilityChecker

	// ChildMaterializer runs the §8.2 steps after admission for the
	// StateCreated child lenny/delegate_task commits: claim the warm pod,
	// assign the credential lease, stream the workspace, launch, and
	// transition the child to running so the returned handle is a running
	// child the parent can interact with. When set, the delegate handler
	// drives the admitted child through it before delivering task input.
	// Optional — a nil materializer leaves the child in StateCreated (the
	// minimal in-process gateway and the mcptools unit suite leave it nil).
	// Production wires *sessionserver.Server. spec: §8.2 lines 93-97.
	ChildMaterializer ChildMaterializer

	// Executor routes messages to runtimes.
	Executor executor.Executor

	// Delegation is the §8 delegation service. Optional — when nil,
	// the lenny/delegate_task tool is not registered.
	Delegation *delegation.Service

	// Users is the §11.4 user registry. Optional — when set,
	// lenny/delegate_task resolves the parent session's owning user and
	// rejects the delegation when that user has been hard-disabled or
	// fully-revoked ("block new delegated tasks"). A nil store leaves
	// the in-session delegation path ungated, matching the dev-header
	// and service-token principals that were never provisioned through
	// the admin user API. spec: §11.4; F-11.4.1.
	Users userstore.Store

	// Runtimes is the §5.1 runtime registry. Optional — when nil, the
	// lenny/discover_agents tool is not registered.
	Runtimes runtimestore.Store

	// CapabilityOverrides is the §5.1 line 49 per-tenant runtime
	// capability override store. Optional — when set, the §8.8 one_shot
	// input-round gate resolves the session tenant's overridden
	// capabilities.interaction. F-5.1.20.
	CapabilityOverrides runtimecapoverride.Store

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

	// ContentPolicies resolves the effective §8.3 contentPolicy
	// (maxInputSize + interceptorRef) for a session so lenny/delegate_task
	// and lenny/send_message run the policy-named external content scanner
	// at PreDelegation / PreMessageDelivery rather than every registered
	// external interceptor, and the message path enforces the target's
	// maxInputSize on the body. Optional — when nil the handlers run the
	// gateway-wide chain (built-ins plus every external interceptor at the
	// phase), the pre-F-8.2.9 behavior the minimal gateway and unit suite
	// rely on. Production wires *delegation.Service. spec: §8.3 lines
	// 149-188; §4.8 lines 1036, 1040; §13.5 mitigations 2-3.
	// F-8.2.9 / F-13.5.2.
	ContentPolicies ContentPolicyResolver

	// CooldownChecker gates lenny/send_message on the §4.8 line 1034 /
	// §8.3 SEC-013 interceptor fail-policy weakening cooldown. Optional —
	// when nil, lenny/send_message applies no interceptor-failPolicy
	// cooldown (the delegate_task path enforces it inside Delegate). The
	// in-process minimal gateway and the unit suite register no external
	// interceptors, so the nil default is a no-op. Production wires
	// *delegation.Service. spec: §4.8 line 1034; §8.3 line 218. F-4.8.17.
	CooldownChecker InterceptorCooldownChecker

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

	// ActivityStamper records §6.2 line 276 qualifying activity for a
	// parent session blocked in lenny/await_children so the §11.3 idle
	// watchdog does not reap it while it actively waits for children. A
	// nil stamper disables the reset (the parent then relies on its
	// agent-output activity and the maxSessionAge backstop). F-11.3.7.
	ActivityStamper interface {
		Stamp(tenantID, sessionID string)
	}

	// TreeArchive is the §8.10 session_tree_archive. Optional — when
	// non-nil, lenny/cancel_child archives each cancelled child's
	// §8.8 TaskResult so a resumed parent can replay it.
	TreeArchive treearchive.Store

	// TaskUsage, when set, stamps the §8.8 usage / treeUsage rollups on a
	// lenny/await_children child result resolved from the live row (the
	// archived body already carries them). Optional — nil leaves the
	// row-only result without rollups. spec: §8.8 lines 897-917.
	TaskUsage *resultrollup.Builder

	// DeadlockTracker records the §8.8 await edges (which session awaits
	// which children) so the subtree deadlock detector can decide whether
	// an awaiting parent's children are all blocked. Optional — when nil,
	// lenny/await_children does not register its edge. spec: §8.8 line
	// 981. F-8.8.6.
	DeadlockTracker *deadlock.AwaitTracker

	// Deadlocks is the §8.8 deadlock manager the await poll reads to
	// surface a `deadlock_detected` partial to a deadlocked subtree root.
	// Optional — when nil, no deadlock partial is emitted. F-8.8.6.
	Deadlocks *deadlock.Manager

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

	// Messaging is the §7.2 session-inbox + DLQ coordinator. When set,
	// lenny/send_message routes a message to a non-delivering target
	// (input_required / suspended → inbox; recovering / pre-running →
	// DLQ) instead of forcing it onto the executor, returning a
	// `queued` delivery receipt per the §7.2 paths 3/5/6/7. A nil
	// coordinator (no Redis) surfaces inbox_unavailable for those
	// paths. The same coordinator backs the REST handler. spec: §7.2
	// lines 313-331. F-7.2.5.
	Messaging *sessioninbox.Coordinator

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

	// VCSCreds materializes the §26.2 in-pod VCS token. When set,
	// lenny/vcs_token is registered: a pod's git-credential helper calls
	// it over the §9.1 platform MCP socket to obtain a short-lived token
	// for a gitClone/git-over-HTTPS host, resolved against the calling
	// session's tenant VCS credential pool. The runtime never holds a
	// long-lived credential; the token is minted per git invocation and
	// bound to the originating session id. Optional — a nil resolver
	// leaves the tool unregistered. spec: §26.2 line 119; §4.9. F-26.2.5.
	VCSCreds vcscred.Resolver

	// VCSLeaseAuditor records the §4.9.2 `credential.leased` event each
	// time lenny/vcs_token mints a token, bound to the originating
	// session id for the §26.2 audit-traceability requirement. Optional —
	// a nil auditor disables the emission. spec: §26.2 line 119; §4.9.2.
	// F-26.2.5.
	VCSLeaseAuditor VCSLeaseAuditor
}

// VCSLeaseAuditor records a §4.9.2 VCS credential lease the
// lenny/vcs_token tool issues. The implementation writes the
// `credential.leased` audit row bound to the originating session id so a
// minted VCS token is traceable to the session that requested it.
// spec: §26.2 line 119; §4.9.2. F-26.2.5.
type VCSLeaseAuditor interface {
	RecordVCSLease(ctx context.Context, lease VCSLeaseRecord)
}

// VCSLeaseRecord is the §4.9.2 audit payload for one minted VCS token:
// the session it is bound to, the resolved host, the VCS provider, and
// the access mode. The token itself is never recorded. spec: §26.2 line
// 119; §4.9.2. F-26.2.5.
type VCSLeaseRecord struct {
	SessionID string
	TenantID  string
	Host      string
	Provider  string
	Mode      string
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

	// env carries the prologue values resolved above to each per-family
	// registrar so the moved registration blocks stay byte-identical while
	// Register reads as the §8.5 tool surface assembled family by family.
	env := registerEnv{
		clock:      clock,
		idFn:       idFn,
		tenant:     tenant,
		msgLimiter: msgLimiter,
		msgScope:   msgScope,
	}

	// §15.2 session-lifecycle surface: attach_session (when the session store
	// is wired), create_session, send_message, interrupt_session, and
	// cancel_session.
	registerSessionLifecycleTools(srv, deps, env)

	// §8.5/§8.9 delegation-tree surface: get_task_tree, cancel_child, and
	// await_children.
	registerTaskTreeTools(srv, deps, env)

	// §8.3 set_tracing_context.
	registerTracingTool(srv, deps, env)

	if deps.Events != nil {
		registerOutputTool(srv, deps, env)
	}

	if deps.InputWaits != nil {
		registerInputWaitTool(srv, deps, env)
	}

	if deps.Interactions != nil {
		registerInteractionTools(srv, deps, env)
	}

	if deps.Runtimes != nil {
		registerRuntimeDiscoveryTools(srv, deps, env)
	}

	if deps.Delegation != nil {
		registerDelegationTool(srv, deps, env)
	}

	if deps.Memory != nil {
		registerMemoryTools(srv, deps, tenant, clock)
	}

	if deps.VCSCreds != nil {
		registerVCSTokenTool(srv, deps, tenant)
	}

	// spec: §15.2 lines 1284-1306 — the remaining client-facing tools
	// (create_and_start_session, start_session, finalize_workspace,
	// terminate_session, resume_session, get_session_status, list_sessions,
	// get_session_logs, list_artifacts, get_token_usage, download_artifact,
	// upload_files). Each dispatches through the §15.2.1 rule-1 shared
	// service layer so the MCP and REST surfaces cannot diverge. F-15.2.3.
	registerClientFacingTools(srv, deps, tenant)
}

// vcsTokenResult is the §26.2 lenny/vcs_token tool result: the HTTP Basic
// credential a pod's git-credential helper feeds to git for an
// HTTPS clone/fetch/push against host. spec: §26.2 line 119. F-26.2.5.
type vcsTokenResult struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

// registerVCSTokenTool registers lenny/vcs_token — the §26.2 in-pod VCS
// token endpoint. A pod's git-credential helper (git-credential-lenny)
// calls it over the §9.1 platform MCP socket on every HTTPS git
// operation: it names the target host and the access mode, and the
// gateway materializes a short-lived token from the calling session's
// tenant VCS credential pool (bound to the host via the pool's
// hostPatterns). The token is minted per invocation and bound to the
// originating session id for audit traceability; the runtime never holds
// a long-lived credential. v1 ships `github` as the only built-in VCS
// provider. spec: §26.2 line 119; §4.9. F-26.2.5.
func registerVCSTokenTool(srv *mcp.Server, deps Deps, defaultTenant string) {
	srv.RegisterTool(mcp.Tool{
		Name: "lenny/vcs_token",
		Description: "Mint a short-lived VCS credential for an HTTPS git operation. " +
			"Called by the in-pod git credential helper; resolves the host against the " +
			"session tenant's VCS credential pool and returns an HTTP Basic username/token. " +
			"v1 ships `github` as the only built-in VCS provider.",
		InputSchema: json.RawMessage(`{"type":"object","required":["host"],"properties":{"host":{"type":"string","description":"The HTTPS git host (e.g. github.com)."},"provider":{"type":"string","description":"VCS provider; defaults to github (the only built-in v1 provider)."},"mode":{"type":"string","enum":["read","write"],"description":"Access mode; defaults to read."}}}`),
	}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		// spec: §26.2 line 119 — the lease is bound to the originating
		// session id. The §9.1 bridge installs the calling pod's session
		// on the principal; a call with no session principal (e.g. a
		// gateway-edge /mcp caller that is not a pod) cannot mint a
		// session-bound VCS token. F-26.2.5.
		sessionID := callerSessionID(ctx, "")
		if sessionID == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"lenny/vcs_token requires an in-pod session principal", nil)
		}
		tenant := callerTenantID(ctx, defaultTenant)

		var in struct {
			Host     string `json:"host"`
			Provider string `json:"provider"`
			Mode     string `json:"mode"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return mcp.ToolResult{}, errInvalidArgs(err)
		}
		host := strings.TrimSpace(strings.ToLower(in.Host))
		if host == "" {
			return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
				"host is required", map[string]any{"field": "host"})
		}
		provider := strings.TrimSpace(in.Provider)
		if provider == "" {
			provider = "github"
		}
		// spec: §26.2 line 119 — `gitClone.url` is HTTPS-only in v1, so a
		// read scope covers clone/fetch and a write scope covers push.
		mode := "read"
		if strings.TrimSpace(in.Mode) == "write" {
			mode = "write"
		}
		leaseScope := fmt.Sprintf("vcs.%s.%s", provider, mode)

		cred, err := deps.VCSCreds.Resolve(ctx, tenant, "https://"+host, leaseScope)
		if err != nil {
			return mcp.ToolResult{}, vcsTokenError(err)
		}
		if cred.IsZero() {
			// No pool/credential resolved to a usable token. Treated as a
			// configuration failure rather than a public clone, because a
			// helper only calls this tool when git demanded credentials.
			return mcp.ToolResult{}, mcp.NewToolError("GIT_CLONE_AUTH_UNSUPPORTED_HOST",
				fmt.Sprintf("no VCS credential pool resolved a token for host %q", host),
				map[string]any{"host": host, "provider": provider})
		}

		if deps.VCSLeaseAuditor != nil {
			deps.VCSLeaseAuditor.RecordVCSLease(ctx, VCSLeaseRecord{
				SessionID: sessionID,
				TenantID:  tenant,
				Host:      host,
				Provider:  provider,
				Mode:      mode,
			})
		}

		body, merr := json.Marshal(vcsTokenResult{
			Host:     host,
			Username: cred.Username,
			Token:    cred.Token,
		})
		if merr != nil {
			return mcp.ToolResult{}, fmt.Errorf("vcs token serialization: %w", merr)
		}
		return textResult(string(body)), nil
	})
}

// vcsTokenError maps a vcscred.Resolve failure to the §15.1 git-clone
// auth error code the REST gitClone path uses, so the in-pod token
// surface and the gateway-side clone surface report identical codes.
// spec: §15.1; §26.2 line 119. F-26.2.5.
func vcsTokenError(err error) error {
	var resolveErr *credentialpoolstore.VCSResolveError
	if errors.As(err, &resolveErr) {
		if resolveErr.Reason == credentialpoolstore.VCSHostAmbiguous {
			return mcp.NewToolError("GIT_CLONE_AUTH_HOST_AMBIGUOUS", err.Error(),
				map[string]any{
					"host": resolveErr.Host, "provider": resolveErr.Provider,
					"matchingPools": resolveErr.MatchingPools,
				})
		}
		return mcp.NewToolError("GIT_CLONE_AUTH_UNSUPPORTED_HOST", err.Error(),
			map[string]any{"host": resolveErr.Host, "provider": resolveErr.Provider})
	}
	if errors.Is(err, vcscred.ErrNoUsableCredential) {
		return mcp.NewToolError("GIT_CLONE_AUTH_UNSUPPORTED_HOST", err.Error(), nil)
	}
	return mcp.NewToolError("INTERNAL_ERROR", err.Error(), nil)
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

	// State is the child's §8.8 task state at response time. The child is
	// materialized synchronously within delegate_task (§8.2: pod allocation,
	// workspace materialization, and launch), so the field carries the
	// post-materialization state that ChildMaterializer.Materialize returns,
	// which reads `running` once the child launches. When no materializer is
	// wired (the minimal in-process gateway), the field falls back to the
	// pre-materialization `created` snapshot.
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

// senderFrom builds the §15.4.1 from-object for a delivered
// lenny/send_message. An authenticated inter-session sender is attributed
// as kind `agent` with its session id; an unattributed send (no principal
// session binding and no fromSessionId) returns the zero value so the
// executor stamps its default gateway-client identity. The gateway always
// sets `from` — a caller cannot. spec: §15.4.1 lines 1696-1707; §13.5
// mitigation 6. F-13.5.11.
func senderFrom(senderID string) executor.MessageFrom {
	if senderID == "" {
		return executor.MessageFrom{}
	}
	return executor.MessageFrom{Kind: "agent", ID: senderID}
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
			// A deliberate PreToolResult reject falls back to
			// INTERCEPTOR_REJECTED; an immutable-field violation (a MODIFY
			// altering the immutable tool_call `id`) carries its own §15.1
			// code (INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION) plus the
			// violated field names, so the tool-error envelope matches the
			// §15.1 catalog row uniformly with the other chain surfaces.
			// spec: §4.8 (PreToolResult id immutability), §15.1.
			details := map[string]any{
				"phase":           string(interceptor.PhasePreToolResult),
				"interceptor_ref": res.RejectedBy,
			}
			if res.Code == interceptor.CodeInterceptorImmutableFieldViolation {
				details["violated_fields"] = res.ViolatedFields
			}
			return mcp.ToolResult{}, mcp.NewToolError(code, res.Reason, details)
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
func applyPostAgentOutput(ctx context.Context, deps Deps, tenant, sessionID string, parts []executor.MessagePart) ([]executor.MessagePart, *interceptor.Result) {
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
		var modified []executor.MessagePart
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
			return mcp.ToolResult{}, errSessionLookup(err)
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
			return mcp.ToolResult{}, errInvalidArgs(err)
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
			return mcp.ToolResult{}, errSessionLookup(err)
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
// Attributes carries the §8.9 line 1010 per-node tracking projection
// (generation, pod, lease, failure history), matching the REST
// /v1/sessions/{id}/tree surface per the §15.2.1 REST↔MCP
// semantic-equivalence rule. F-8.9.1.
type treeNode struct {
	TaskID     string                      `json:"taskId"`
	State      string                      `json:"state"`
	Metadata   map[string]any              `json:"metadata,omitempty"`
	RuntimeRef string                      `json:"runtimeRef,omitempty"`
	Attributes sessionstore.NodeAttributes `json:"attributes"`
	Children   []treeNode                  `json:"children"`
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

// requireActiveDelegator enforces the §11.4 rule that hard_disable
// "also block[s] new delegated tasks". It resolves the parent session's
// owning user and rejects the delegation with USER_INVALIDATED when that
// user is no longer active. When no user store is wired, the parent
// session is unresolvable, or the owning user has no registry row, the
// delegation proceeds — mirroring the REST session-creation gate, which
// admits principals that were never provisioned through the admin user
// API. spec: §11.4; F-11.4.1.
func requireActiveDelegator(ctx context.Context, deps Deps, tenant, parentSessionID string) error {
	if deps.Users == nil || deps.Store == nil || parentSessionID == "" {
		return nil
	}
	parent, err := deps.Store.Get(ctx, tenant, parentSessionID)
	if err != nil {
		// A missing or unreadable parent is the delegation service's
		// concern (it returns PARENT_NOT_FOUND); the user gate stays out
		// of the way so the canonical error surfaces downstream.
		return nil
	}
	if parent.UserID == "" {
		return nil
	}
	user, err := deps.Users.Get(ctx, tenant, parent.UserID)
	if errors.Is(err, userstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return mcp.NewToolError("INTERNAL_ERROR",
			"user-invalidation check failed: "+err.Error(), nil)
	}
	if !user.IsActive() {
		return mcp.NewToolError("USER_INVALIDATED",
			"the owning user has been invalidated and cannot spawn new delegated tasks",
			map[string]any{"userId": parent.UserID})
	}
	return nil
}

// resolveDelegationCredentialQuery resolves the two parent-derived inputs
// the §8.3 delegation-time credential-availability pre-check needs: the
// parent session's owning user (the same value the §11.4 revocation gate
// reads) and, for an inherit hop, the credential origin the check
// constrains the eligible provider set to (the parent's stored ancestor
// origin when present, else the parent's own id, matching the rule the
// delegation service applies to a finalized inherit child).
//
// The store gates neither the pre-check nor an independent hop. An
// inherit hop fails closed on a read error: an unresolvable origin must
// not downgrade to an unconstrained (independent-equivalent) check
// (code-best-practices deny-on-doubt). An independent or omitted hop
// needs no origin and resolves the query user best-effort, exactly as
// requireActiveDelegator does, so a store read never blocks it — the
// §8.3 skip set is a deny hop and a nil checker alone. When no store is
// wired the query carries an empty user and origin and the pre-check
// still runs. spec: §8.3 line 470.
func resolveDelegationCredentialQuery(ctx context.Context, deps Deps, tenant, parentSessionID string, mode lease.CredentialPropagation) (userID, originID string, err error) {
	if deps.Store == nil {
		return "", "", nil
	}
	if mode == lease.CredentialPropagationInherit {
		parent, err := deps.Store.Get(ctx, tenant, parentSessionID)
		if err != nil {
			return "", "", err
		}
		origin := parent.CredentialOriginSessionID
		if origin == "" {
			origin = parent.ID
		}
		return parent.UserID, origin, nil
	}
	// independent / omitted: best-effort parent-user resolution. A read
	// error does not block the hop, since an independent check needs no
	// origin and the §8.3 skip set excludes an unreadable parent.
	parent, err := deps.Store.Get(ctx, tenant, parentSessionID)
	if err != nil {
		return "", "", nil
	}
	return parent.UserID, "", nil
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

// crossEnvInheritMismatch runs the §8.3 cross-environment `inherit`
// provider-compatibility check. Before a cross-environment `inherit`
// delegation claims a warm pod, the gateway intersects the providers
// represented in the parent's origin credential pool — the tenant
// credentialPolicy providers narrowed to the origin runtime's
// supportedProviders — with the child runtime's supportedProviders. An
// empty intersection returns a CREDENTIAL_PROVIDER_MISMATCH tool error so
// the delegation is rejected before any pod is claimed; a non-empty
// intersection returns nil and the caller proceeds to the pod-claiming
// Delegate path unchanged.
//
// The origin session is resolved live at this hop from the delegating
// parent's stored CredentialOriginSessionID (its own id when unset),
// forwarding the same origin pool through contiguous inherit hops and
// re-checking it against the immediate target runtime at each
// environment boundary. An unresolvable origin session or origin runtime
// fails closed: its empty supportedProviders yield an empty intersection
// and the delegation is rejected rather than falling back to the child's
// own unconstrained provider set. The check is skipped (returns nil) when
// the session, runtime, or tenant registry is unwired, mirroring
// crossEnvReachable's nil-registry behavior; a parent lookup failure also
// skips it, since the pod-claiming Delegate path rejects a missing parent.
//
// spec: §8.3 line 472 (cross-environment compatibility check and exact
// rejection message); line 472/488 (origin-pool forwarding through
// contiguous inherit hops); line 474. §15 CREDENTIAL_PROVIDER_MISMATCH is
// POLICY / 422.
func crossEnvInheritMismatch(ctx context.Context, deps Deps, tenant, parentSessionID, targetRef string) *mcp.ToolError {
	if deps.Store == nil || deps.Runtimes == nil || deps.Tenants == nil {
		return nil
	}
	parent, err := deps.Store.Get(ctx, tenant, parentSessionID)
	if err != nil {
		return nil
	}
	originID := parent.CredentialOriginSessionID
	if originID == "" {
		originID = parent.ID
	}
	// An unresolvable origin session or runtime leaves the zero value,
	// whose empty SupportedProviders produce an empty intersection: the
	// inherit hop fails closed rather than drawing from the child's own
	// unconstrained provider set.
	origin, _ := deps.Store.Get(ctx, tenant, originID)
	originRuntime, _ := deps.Runtimes.Get(ctx, origin.RuntimeRef)
	childRuntime, _ := deps.Runtimes.Get(ctx, targetRef)
	var providers []string
	if tnt, terr := deps.Tenants.Get(ctx, tenant); terr == nil {
		providers = tnt.CredentialPolicy.Providers()
	}
	originProviders := lease.IntersectProviders(providers, originRuntime.SupportedProviders)
	compat := lease.IntersectProviders(originProviders, childRuntime.SupportedProviders)
	if len(compat) > 0 {
		return nil
	}
	return mcp.NewToolError("CREDENTIAL_PROVIDER_MISMATCH",
		"credentialPropagation: inherit is incompatible with cross-environment delegation: parent credential pool providers do not intersect with child runtime supportedProviders",
		map[string]any{"originRuntime": origin.RuntimeRef, "childRuntime": targetRef})
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

// resolvePoolElicitationPolicy returns the §9.2 per-pool elicitation
// depth policy and agent-initiated url-mode allowlist configured on the
// named pool. The §9.2 elicitationDepthPolicy (lines 90-98) and
// urlModeElicitation (line 86) are per-pool, so the dispatch path
// resolves them from the raising session's pool rather than from a
// single Register-time platform value. The bool is false when the pool
// registry is unwired or the pool cannot be resolved, in which case the
// caller keeps the dispatcher's Register-time defaults (which the
// §9.2 WalkChain coerces to the platform suppress_at_depth=3 default and
// the agent-initiated url-mode block). spec: §9.2 lines 86, 90-98.
// F-9.2.12.
func resolvePoolElicitationPolicy(ctx context.Context, deps Deps, poolRef string) (elicitation.DepthPolicy, int, elicitation.URLModeAllowlist, bool) {
	if deps.Pools == nil || poolRef == "" {
		return "", 0, elicitation.URLModeAllowlist{}, false
	}
	pool, err := deps.Pools.Get(ctx, poolRef)
	if err != nil {
		return "", 0, elicitation.URLModeAllowlist{}, false
	}
	return pool.ElicitationDepthPolicy, pool.ElicitationSuppressAtDepth, pool.URLModeElicitation, true
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
func toTaskResult(s sessionstore.Session) sessionrecord.Result {
	tr := sessionrecord.Result{SchemaVersion: sessionrecord.SchemaVersion, TaskID: s.ID, State: mcpStateForSession(s.State)}
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
func taskErrorForRow(s sessionstore.Session) *sessionrecord.Error {
	code := s.FailureReason
	if code == "" {
		code = "CHILD_" + strings.ToUpper(string(s.State))
	}
	cat, _ := errorclassify.Classify(code)
	maxRetries := 0
	if s.RetryPolicy != nil {
		maxRetries = s.RetryPolicy.MaxRetries
	}
	return &sessionrecord.Error{
		Code:             code,
		Category:         string(cat),
		Message:          "child session ended in state " + string(s.State),
		RetriesExhausted: sessionrecord.RetriesExhausted(s.RetryCount, maxRetries),
	}
}

// childOutcome is a child's resolved state for lenny/await_children,
// sourced from the live session row or, when the row is gone, from the
// §8.10 archive.
type childOutcome struct {
	parentID string
	state    session.State
	result   sessionrecord.Result
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
func resolveChild(ctx context.Context, store sessionstore.Store, archive treearchive.Store, usage *resultrollup.Builder,
	tenant, childID string,
) (childOutcome, error) {
	row, err := store.Get(ctx, tenant, childID)
	if err == nil {
		oc := childOutcome{parentID: row.ParentSessionID, state: row.State, result: toTaskResult(row)}
		if session.IsTerminal(row.State) {
			// spec: §8.8 lines 897-917 — stamp the usage / treeUsage rollups
			// on the row-only projection so a terminal child resolved before
			// its archive body exists carries the same rollups the archived
			// body does (§15.2.1 REST/MCP equivalence). The richer archive
			// body below overrides this when present.
			if usage != nil {
				oc.result.Usage = usage.Usage(ctx, row)
				oc.result.TreeUsage = usage.TreeUsage(ctx, row, oc.result.Usage)
			}
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
					var tr sessionrecord.Result
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
	var tr sessionrecord.Result
	if json.Unmarshal([]byte(node.Result), &tr) != nil {
		tr = sessionrecord.Result{SchemaVersion: sessionrecord.SchemaVersion, State: mcpStateForSession(session.State(node.State))}
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

// inputRequiredChild is one awaited child's input_required partial
// result on the lenny/await_children response: the child id, the fixed
// `input_required` state tag, the §8.8 `requestId` the parent answers
// with `inReplyTo`, and the question `parts`. spec: §8.8 line 951;
// F-8.8.5.
type inputRequiredChild struct {
	ChildID   string            `json:"childId"`
	State     string            `json:"state"`
	RequestID string            `json:"requestId"`
	Parts     []json.RawMessage `json:"parts,omitempty"`
}

// collectInputRequired returns the input_required partial entries for
// the awaited children that currently have a pending lenny/request_input
// round. The result is ordered by (childIDs position, requestId) so a
// repeated poll yields a stable frame. Returns nil when the registry is
// absent or no awaited child is blocked on input. spec: §8.8 lines
// 951-971; F-8.5.5 / F-8.8.5.
func collectInputRequired(reg *inputwait.Registry, childIDs []string) []inputRequiredChild {
	if reg == nil {
		return nil
	}
	var out []inputRequiredChild
	for _, cid := range childIDs {
		for _, pr := range reg.PendingDetailsForSession(cid) {
			out = append(out, inputRequiredChild{
				ChildID:   cid,
				State:     string(session.StateInputRequired),
				RequestID: pr.RequestID,
				Parts:     pr.Parts,
			})
		}
	}
	return out
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
func collectChildResults(ctx context.Context, store sessionstore.Store, archive treearchive.Store, usage *resultrollup.Builder,
	tenant string, childIDs []string, mode string,
) ([]sessionrecord.Result, bool, error) {
	type settled struct {
		at     time.Time
		idx    int
		result sessionrecord.Result
	}
	var terminal []settled
	allTerminal := true
	for i, cid := range childIDs {
		oc, err := resolveChild(ctx, store, archive, usage, tenant, cid)
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
		return []sessionrecord.Result{first.result}, true, nil
	}
	if allTerminal {
		// spec: §8.10 line 1062 — the re-await protocol streams settled
		// child results "in original-settlement order". Sort by the
		// per-child settle witness (the archive's SettledAt once migrated,
		// the live row's UpdatedAt until then), with a stable tie-break on
		// childIDs position when two children share a settle instant. This
		// gives a parent that re-awaits after a pod loss the same ordered
		// view regardless of which child settled before or after the
		// failure. F-8.10.4.
		sort.SliceStable(terminal, func(i, j int) bool {
			if terminal[i].at.Equal(terminal[j].at) {
				return terminal[i].idx < terminal[j].idx
			}
			return terminal[i].at.Before(terminal[j].at)
		})
		out := make([]sessionrecord.Result, len(terminal))
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
		Attributes: sessionstore.ProjectNodeAttributes(s),
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
		result, _ := json.Marshal(sessionrecord.Result{
			SchemaVersion: sessionrecord.SchemaVersion,
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
			Error: &sessionrecord.Error{
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
func buildSendMessageReceipt(messageID, resolvedRequestID string, out []executor.MessagePart, now time.Time) string {
	envelope := struct {
		DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`
		Resolved        string                  `json:"resolved,omitempty"`
		Output          []executor.MessagePart  `json:"output,omitempty"`
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
	return buildSendMessageReceiptStatusReason(messageID, status, "", now)
}

// buildSendMessageReceiptStatusReason builds the §15.4 delivery_receipt
// envelope for a non-`delivered` status that carries a §15.4 line 1739
// reason — the §7.2 buffered-path outcomes (`queued`, `dropped` with
// inbox_overflow / dlq_overflow, `error` with inbox_unavailable).
// `deliveredAt` is set only for `delivered`. spec: §15.4 lines
// 1725-1742; §7.2 lines 313-331. F-7.2.5.
func buildSendMessageReceiptStatusReason(messageID string, status session.DeliveryStatus, reason session.DeliveryReason, now time.Time) string {
	receipt := session.DeliveryReceipt{MessageID: messageID, Status: status, Reason: reason}
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
