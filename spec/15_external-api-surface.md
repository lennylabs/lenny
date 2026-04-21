## 15. External API Surface

**REST vs MCP split (operator vs session).** Lenny's external surface has two roles, exposed over two protocols that callers pick based on what they're doing:

- **Admin / operator flows — REST.** Creating tenants, runtimes, pools, credential pools, credential leases, delegation policies, environments, backups, upgrades, RBAC bindings, retention configuration, and every other `/v1/admin/*` resource uses the REST API ([§15.1](#151-rest-api)). These are imperative, single-shot operations against platform state; their consumers are CLIs, CI/CD jobs, dashboards, and agent operability clients ([§25](25_agent-operability.md)). The canonical CLI entry points are the `lenny-ctl admin <resource> <verb>` commands ([§24](24_lenny-ctl-command-reference.md)).
- **Session flows — MCP.** Creating and interacting with sessions — `lenny/create_session`, `lenny/send_message`, `lenny/delegate_task`, output streaming, elicitation, interrupt — flows through the MCP API ([§15.2](#152-mcp-api)). Session creation **also** exists as a plain REST call (`POST /v1/sessions`) for callers that cannot implement MCP, but the MCP path is the recommended one because the same connection carries the interactive lifecycle. The canonical CLI entry points are the `lenny session <verb>` commands ([§24.17](24_lenny-ctl-command-reference.md#2417-session-operations)).

Both surfaces live behind the same adapter registry below; the split above is a description of **which path a given caller uses**, not an architectural partition. Deployer-side runtimes consume a third contract — the **Runtime Adapter Specification** ([§15.4](#154-runtime-adapter-specification)) and the **Runtime Author SDKs** ([§15.7](#157-runtime-author-sdks)) — which is independent of both REST and MCP.

Lenny exposes multiple client-facing APIs through the **`ExternalAdapterRegistry`** — a pluggable adapter system where simultaneously active adapters route by path prefix. All adapters implement a common interface:

```go
type ExternalProtocolAdapter interface {
    // Required — all adapters must implement these three.
    HandleInbound(ctx, w, r, dispatcher) error
    HandleDiscovery(ctx, w, r, runtimes []AuthorizedRuntime, caps AdapterCapabilities) error
    Capabilities() AdapterCapabilities

    // Optional lifecycle hooks — adapters that manage stateful protocols
    // (A2A task lifecycle, push notifications) implement these.
    // Default no-op implementations are provided by BaseAdapter; adapters
    // that embed BaseAdapter only override hooks they need.
    OnSessionCreated(ctx, sessionID, metadata SessionMetadata) error
    OnSessionEvent(ctx, sessionID, event SessionEvent) error
    OnSessionTerminated(ctx, sessionID, reason TerminationReason) error

    // OutboundCapabilities declares what the adapter can push to clients
    // (e.g., streaming updates, push notifications, task state transitions).
    // Adapters with no outbound behavior return an empty declaration.
    OutboundCapabilities() OutboundCapabilitySet

    // OpenOutboundChannel is called by the gateway when an adapter with
    // outbound push capability (OutboundCapabilitySet.PushNotifications == true)
    // needs to deliver events to a registered callback or subscriber.
    // The returned OutboundChannel is owned by the adapter; the gateway calls
    // Send() for each qualifying SessionEvent and Close() when the adapter is
    // unregistered. Adapters with no outbound push return a no-op channel.
    OpenOutboundChannel(ctx context.Context, sessionID string, sub OutboundSubscription) (OutboundChannel, error)
}

// AdapterCapabilities declares the routing and protocol capabilities of an adapter.
// BaseAdapter.Capabilities() returns a zero value with PathPrefix and Protocol
// populated from the adapter's registration; all bool fields default to false.
type AdapterCapabilities struct {
    // PathPrefix is the URL path prefix this adapter owns (e.g., "/mcp", "/a2a", "/v1").
    // The gateway routes inbound requests to this adapter when the request path
    // has this prefix. Must be unique across all registered adapters.
    PathPrefix string

    // Protocol is the protocol identifier for this adapter (e.g., "mcp", "a2a",
    // "openai-completions", "openai-responses"). Used in audit events and metrics.
    Protocol string

    // SupportsSessionContinuity indicates the adapter can resume interrupted sessions
    // (i.e., it persists sufficient state to reconstruct the protocol session after
    // a gateway restart or failover).
    SupportsSessionContinuity bool

    // SupportsDelegation indicates the adapter handles delegated task routing —
    // it can receive and forward delegate_task calls from parent sessions.
    SupportsDelegation bool

    // SupportsElicitation indicates the adapter can surface lenny/request_elicitation
    // calls to the client (human-in-the-loop input collection).
    SupportsElicitation bool

    // SupportsInterrupt indicates the adapter handles interrupt_request signals
    // from the lifecycle channel and can surface them to the client.
    SupportsInterrupt bool
}

// OutboundCapabilitySet declares the asynchronous push capabilities of an adapter.
// All fields are false in the zero value (BaseAdapter default).
type OutboundCapabilitySet struct {
    // PushNotifications indicates the adapter can deliver state-change events
    // to a caller-registered callback URL or persistent connection after the
    // initial inbound response has been sent. Required for A2A streaming updates
    // and webhook-based integrations.
    PushNotifications bool

    // SupportedEventKinds lists the SessionEvent kinds the adapter is prepared
    // to push. An empty slice means no events are pushed even if PushNotifications
    // is true. Values MUST be drawn from the closed SessionEventKind enum defined
    // below (SessionEventStateChange, SessionEventOutput, SessionEventElicitation,
    // SessionEventToolUse, SessionEventError, SessionEventTerminated); the
    // gateway filters dispatch by this declaration (see dispatch-filter rule in
    // "SessionEvent Kind Registry").
    SupportedEventKinds []SessionEventKind

    // MaxConcurrentSubscriptions is the maximum number of simultaneous
    // OutboundChannel instances the adapter supports per session. 0 = unlimited.
    MaxConcurrentSubscriptions int
}

// OutboundSubscription carries the caller-supplied delivery target registered
// when the external protocol request was accepted (e.g., an A2A webhook URL,
// a long-poll response writer, or a persistent SSE stream handle).
type OutboundSubscription struct {
    // CallbackURL is the webhook URL to POST events to, if applicable.
    // Empty for connection-coupled delivery (SSE, long-poll).
    CallbackURL string

    // ResponseWriter is set for connection-coupled adapters; nil for webhook.
    ResponseWriter http.ResponseWriter

    // Metadata carries adapter-specific fields (e.g., A2A task ID, correlation IDs).
    Metadata map[string]string
}

// OutboundChannel is a handle to an active push channel for a single session.
// The gateway calls Send for each qualifying event and Close when the session
// terminates or the subscription is cancelled.
type OutboundChannel interface {
    // Send delivers a SessionEvent to the subscriber. Implementations must be
    // non-blocking; if the subscriber is slow, events may be buffered or dropped
    // according to the normative back-pressure policy below. Send returns an
    // error if the channel is permanently unavailable (e.g., webhook URL
    // consistently unreachable); the gateway will close the channel on non-nil error.
    Send(ctx context.Context, event SessionEvent) error

    // Close releases resources. Called exactly once by the gateway.
    Close() error
}

// Normative back-pressure policy for OutboundChannel implementations.
//
// Each OutboundChannel MUST implement one of the following two policies:
//
//   1. Buffered-drop policy (REQUIRED for webhook-based adapters):
//      The channel maintains an in-memory event buffer with a maximum depth of
//      MaxOutboundBufferDepth (default: 256 events; configurable per adapter via
//      the `adapter.outboundBufferDepth` Helm value, range: 16–4096). When Send
//      is called and the buffer is full, the oldest event in the buffer is evicted
//      (head-drop) and the new event is enqueued. The eviction increments the
//      `lenny_outbound_channel_buffer_drop_total` counter (labeled by `adapter`,
//      `session_id`). Send MUST return nil even on eviction — buffer overflow is
//      a degradation signal, not a fatal error, so the gateway does not close the
//      channel on drop. To preserve the subscriber's ability to reason about
//      event continuity, the channel MUST surface the drop to the subscriber by
//      emitting a single `gap_detected` protocol-level frame
//      (`{"lastSeenSeq": N, "nextSeq": M}`, reusing the shape defined in
//      [Section 10.4](10_gateway-internals.md#104-gateway-reliability)) before
//      the next successfully-delivered event, where `N` is the `SeqNum` of the
//      last event that reached the subscriber prior to the eviction window and
//      `M` is the `SeqNum` of the next event actually delivered. Consecutive
//      evictions before the next successful delivery collapse into a single
//      `gap_detected` frame covering the combined range. The adapter carries
//      this frame over its native protocol using the same out-of-band channel
//      it uses for replay-buffer gaps (e.g., the `MCPAdapter` emits the frame
//      on the SSE stream without a `SeqNum`; webhook adapters include it as a
//      distinguished envelope kind alongside the next event delivery). The
//      frame is not a `SessionEvent`, carries no `SeqNum`, and is not part of
//      the `SessionEventKind` closed enum.
//
//   2. Bounded-error policy (REQUIRED for connection-coupled adapters — SSE, long-poll):
//      The channel attempts a non-blocking write to the underlying transport.
//      If the write would block (subscriber's read loop is behind), the channel
//      MUST return a non-nil error from Send within 100 ms. The gateway closes
//      the channel on non-nil error and removes it from the session's dispatch
//      map. The subscriber must reconnect. This ensures a single slow subscriber
//      cannot block the gateway's event dispatch loop.
//
// Both policies share these invariants:
//   - Send MUST NOT block the caller for more than MaxOutboundSendTimeoutMs
//     (default: 100 ms; configured globally via `adapter.outboundSendTimeoutMs`).
//   - Send MUST be safe to call concurrently from multiple goroutines.
//   - The buffer depth limit applies per OutboundChannel instance (per session),
//     not globally across all channels — a slow subscriber for one session cannot
//     starve delivery to other sessions.
//
// Adapters that embed BaseAdapter inherit the buffered-drop policy with
// MaxOutboundBufferDepth = 256. Adapters that override Send must document
// which policy they implement.
```

The gateway provides a **`BaseAdapter`** struct with no-op implementations of all optional methods. Adapters that embed `BaseAdapter` satisfy the full interface and only override lifecycle hooks they need — existing adapters (MCP, OpenAI Completions, Open Responses) require no changes. `BaseAdapter.OutboundCapabilities()` returns a zero-value `OutboundCapabilitySet` (all false). `BaseAdapter.OpenOutboundChannel()` returns a no-op channel that discards all events.

#### Shared Adapter Types

The following Go types are referenced by the `ExternalProtocolAdapter` interface and the `OutboundChannel` contract above. They are normative: every gateway-supplied value passed to an adapter MUST conform to these shapes, and every third-party adapter registered via the admin API tier ([Section 15](#15-external-api-surface), "Three tiers of pluggability") MUST accept them without additional fields expected. All enum-typed fields are closed sets — values outside the declared constants are gateway-internal bugs, not adapter-extension points.

```go
// SessionMetadata is passed to OnSessionCreated with the immutable details
// of a newly-created session. It is derived from CreateSessionRequest plus
// gateway-determined fields (SessionID, negotiated protocol version). All
// fields are populated by the gateway before OnSessionCreated is invoked;
// adapters MUST treat SessionMetadata as read-only.
type SessionMetadata struct {
    // TenantID is the tenant that owns this session. Matches the tenant
    // resolved from the caller's credentials at session-create time.
    TenantID string

    // SessionID is the gateway-assigned ULID (`sess_` prefix) that names
    // this session across all subsequent lifecycle hooks, audit events,
    // and storage records. Stable for the session's lifetime.
    SessionID string

    // RuntimeName is the runtime this session was dispatched to — matches
    // an entry in the runtime registry ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)).
    RuntimeName string

    // DelegationDepth is the 0-based depth of this session in its delegation
    // tree: 0 for a root session, 1 for a direct child created via
    // `lenny/delegate_task`, and so on. Matches the `delegationDepth` field
    // on `MessageEnvelope` for messages originating from this session.
    DelegationDepth int

    // CallerIdentity is the authenticated identity that created the session
    // (JWT `sub` and associated principal attributes). Derived from the
    // Lenny JWT claim structure in [Section 13](13_security-model.md).
    CallerIdentity CallerIdentity

    // NegotiatedProtocolVersion is the protocol version the adapter and
    // gateway agreed upon at session-create time (e.g., the negotiated MCP
    // spec version for MCPAdapter-created sessions). Empty string for
    // adapters that do not perform explicit version negotiation.
    NegotiatedProtocolVersion string
}

// CallerIdentity is the projection of the authenticated caller's JWT claims
// that the gateway exposes to adapters. It is derived from the Lenny JWT
// claim structure ([Section 13.3](13_security-model.md#133-credential-flow))
// at session-create time and is immutable for the session's lifetime.
// Adapters MUST treat CallerIdentity as read-only and MUST NOT attempt to
// re-validate or re-resolve claims — the gateway has already validated the
// token and applied scope-narrowing / tenant-scope enforcement before
// populating this struct.
type CallerIdentity struct {
    // Sub is the authenticated subject identifier (JWT `sub` claim). Opaque
    // string scoped to the issuing IdP; adapters MAY surface it in audit
    // or discovery responses but MUST NOT parse it.
    Sub string

    // CallerType is the closed-enum caller category from the JWT
    // `caller_type` claim ([Section 25.1](25_agent-operability.md#251-design-philosophy-and-agent-model)):
    // "human", "service", or "agent". Values outside this set are
    // gateway-internal bugs.
    CallerType string

    // Scope is the space-separated scope set granted to the token
    // (JWT `scope` claim). Adapters MAY consult it to gate surface-specific
    // features (e.g., operability scopes) but MUST NOT attempt to broaden
    // scope from the adapter side — scope narrowing is enforced at token
    // issuance only.
    Scope string

    // Act carries the RFC 8693 `act` claim when the session was created via
    // a delegation child token ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)).
    // Nil for root sessions. When non-nil, adapters that surface delegation
    // provenance (e.g., A2A `delegator` agent card field) SHOULD render
    // Act.Sub as the delegating actor.
    Act *ActorClaim
}

// ActorClaim is the RFC 8693 `act` claim projection exposed to adapters. It
// identifies the session's delegating actor. Fields mirror the JWT `act`
// claim one-for-one.
type ActorClaim struct {
    // Sub is the delegating actor's JWT `sub`.
    Sub string

    // TenantID is the delegating actor's tenant scope. Invariant:
    // ActorClaim.TenantID == SessionMetadata.TenantID (cross-tenant
    // delegation is forbidden — see [Section 13.3](13_security-model.md#133-credential-flow)).
    TenantID string

    // SessionID is the parent session's ID. Empty for actors that are not
    // themselves Lenny sessions (e.g., external service principals).
    SessionID string

    // DelegationDepth is the parent's delegation depth; the current
    // session's depth (SessionMetadata.DelegationDepth) equals this value
    // plus one.
    DelegationDepth int
}

// SessionEvent is the outbound event envelope pushed via OutboundChannel.Send.
// Kind is drawn from the closed enum documented in "SessionEvent Kind Registry"
// below; adapters MUST NOT attempt to interpret unrecognized kind values.
// Payload sub-schema is determined by Kind (see the kind registry for the
// per-kind mapping).
type SessionEvent struct {
    // Kind is the closed-enum event category (see SessionEvent Kind Registry).
    Kind SessionEventKind

    // SeqNum is a monotonic per-session sequence number assigned by the
    // gateway at event-dispatch time. Adapters MAY use SeqNum to detect
    // gaps when delivering over lossy transports (e.g., webhook retries)
    // and MUST forward it into their native protocol's event envelope so
    // that clients can use it to resume after a stream drop — the
    // `MCPAdapter` surfaces SeqNum as the SSE `id:` line on the Streamable
    // HTTP transport and honors a client-supplied `resumeFromSeq` on
    // `attach_session` to replay buffered events (see
    // [Section 10.4](10_gateway-internals.md#104-gateway-reliability)
    // event replay buffer and [Section 15.2](#152-mcp-api)
    // `attach_session`).
    SeqNum uint64

    // Payload is the kind-specific event body. The sub-schema is determined
    // by Kind — consult the SessionEvent Kind Registry for the mapping.
    Payload json.RawMessage

    // Timestamp is the gateway-assigned wall-clock time the event was
    // materialized. Adapters that surface timestamps to clients SHOULD
    // pass this value through verbatim rather than regenerating it at
    // send time, so that the timestamp reflects the event, not the delivery.
    Timestamp time.Time
}

// SessionEventKind is the closed enum of event categories the gateway may
// dispatch to an OutboundChannel. Adapters declare the subset they handle
// via OutboundCapabilitySet.SupportedEventKinds; the gateway MUST filter
// events by that declaration before calling Send (see dispatch-filter rule
// in "SessionEvent Kind Registry" below).
type SessionEventKind string

const (
    SessionEventStateChange  SessionEventKind = "state_change"
    SessionEventOutput       SessionEventKind = "output"
    SessionEventElicitation  SessionEventKind = "elicitation"
    SessionEventToolUse      SessionEventKind = "tool_use"
    SessionEventError        SessionEventKind = "error"
    SessionEventTerminated   SessionEventKind = "terminated"
)

// TerminationReason is the closed-enum reason passed to OnSessionTerminated
// and returned by Runtime SDK Handler.OnTerminate ([Section 15.7](#157-runtime-author-sdks)).
// The Code field MUST be one of the TerminationCode constants below;
// Detail is free-form human-readable text for operator debugging and
// MUST NOT contain secrets (credentials, tokens, PII) — the gateway emits
// Detail into audit logs verbatim.
type TerminationReason struct {
    // Code is the closed-enum termination category.
    Code TerminationCode

    // Detail is a human-readable description suitable for operator
    // surfaces (logs, admin UI). MUST NOT contain secrets. MAY be empty.
    Detail string
}

// TerminationCode is the closed enum of terminal-state causes. Third-party
// adapters translating to their native protocol's terminal state MUST map
// each code to an appropriate protocol-level value (see §21.1 for the
// A2AAdapter mapping). Values outside this set are gateway-internal bugs.
type TerminationCode string

const (
    // TerminationCompleted — the runtime finished normally and emitted a
    // final response. Maps to the "success" terminal state in all protocols.
    TerminationCompleted TerminationCode = "completed"

    // TerminationFailed — the runtime exited abnormally (non-zero exit,
    // panic, unrecoverable error). Maps to the "failure" terminal state.
    TerminationFailed TerminationCode = "failed"

    // TerminationCancelled — the session was cancelled by the caller
    // (DELETE /v1/sessions/{id} or equivalent). Maps to the protocol's
    // "cancelled" state (A2A `canceled`, MCP task cancellation, etc.).
    TerminationCancelled TerminationCode = "cancelled"

    // TerminationExpired — the session exceeded `maxSessionAgeSeconds`
    // ([Section 11](11_policy-and-controls.md)) or the delegation lease
    // `perChildMaxAge`.
    TerminationExpired TerminationCode = "expired"

    // TerminationDrained — the gateway or pod was drained for a planned
    // operation (node drain, rolling upgrade) and could not resume the
    // session within the drain deadline. Distinct from `failed` so
    // operators can correlate terminations with planned maintenance.
    TerminationDrained TerminationCode = "drained"
)

// AuthorizedRuntime is the element type in the slice passed to HandleDiscovery.
// It mirrors the shape returned by `GET /v1/runtimes` ([Section 15.1](#151-rest-api)),
// filtered to runtimes the caller is authorized to see per the visibility
// rules in [Section 11](11_policy-and-controls.md).
// `PublishedMetadata` entries are also visibility-filtered — entries the
// caller cannot see are omitted before the slice is handed to the adapter,
// so adapters MUST NOT attempt additional authorization checks on the
// metadata list.
type AuthorizedRuntime struct {
    // Name is the runtime identifier (matches `runtime.yaml` `name` field).
    Name string

    // AgentInterface is the runtime's declared agent interface descriptor
    // ([Section 5.1 `agentInterface` Field](05_runtime-registry-and-pool-model.md#agentinterface-field)),
    // used by adapters to auto-generate discovery formats (A2A agent cards,
    // MCP `list_runtimes` response, REST runtime summaries). The struct
    // mirrors the §5.1 YAML shape verbatim — field names, JSON tags, and
    // nested types are the normative contract. `nil` for `type: mcp`
    // runtimes (which do not carry an `agentInterface`) and for `type:
    // agent` runtimes that omit the optional block; JSON serialization
    // omits the field when `nil` (`omitempty`).
    AgentInterface *AgentInterface `json:"agentInterface,omitempty"`

    // McpEndpoint is the dedicated MCP endpoint URL for `type: mcp`
    // runtimes (format: `/mcp/runtimes/{name}`). Empty string for
    // `type: agent` runtimes.
    McpEndpoint string

    // AdapterCapabilities reflects the capabilities of the adapter currently
    // serving the discovery request, NOT per-runtime capabilities.
    // Duplicated here so adapters can embed the capability block inline in
    // their native discovery format without additional lookups.
    AdapterCapabilities AdapterCapabilities

    // PublishedMetadata carries the visibility-filtered set of
    // `publishedMetadata` entries for this runtime
    // ([Section 5.1 `publishedMetadata` Field](05_runtime-registry-and-pool-model.md#publishedmetadata-field)).
    // Each ref is an opaque handle; adapters retrieve the materialized
    // card via the gateway's metadata-fetch API as needed.
    PublishedMetadata []PublishedMetadataRef
}

// AgentInterface is the structured descriptor declared on `type: agent`
// runtimes ([Section 5.1 `agentInterface` Field](05_runtime-registry-and-pool-model.md#agentinterface-field)).
// The fields below mirror the §5.1 YAML shape verbatim — §5.1 is the
// authoritative source; any field added there MUST be added here in
// lockstep. Adapters consume this struct to auto-generate A2A agent cards,
// MCP `list_runtimes` response entries, and REST runtime summaries without
// re-parsing the gateway-rendered `agent-card` `publishedMetadata` entry.
// All fields are optional; zero-valued fields (`""`, `nil`, `false`) MUST
// be omitted from JSON serialization via the `omitempty` tags below so
// adapters can compose native discovery formats without carrying noise for
// unused capabilities.
type AgentInterface struct {
    // Description is the human-readable summary of the runtime's role
    // (matches the `description` field in §5.1 YAML).
    Description string `json:"description,omitempty"`

    // InputModes enumerates the media types the runtime accepts on input
    // (matches the `inputModes` field in §5.1 YAML). Each entry carries a
    // `type` (IANA media type) and an optional `role`.
    InputModes []AgentInterfaceMode `json:"inputModes,omitempty"`

    // OutputModes enumerates the media types the runtime emits on output
    // (matches the `outputModes` field in §5.1 YAML). Each entry carries a
    // `type` (IANA media type) and an optional `role` such as `"primary"`.
    OutputModes []AgentInterfaceMode `json:"outputModes,omitempty"`

    // SupportsWorkspaceFiles signals that workspace files in TaskSpec are
    // honored by the runtime (matches the `supportsWorkspaceFiles` field
    // in §5.1 YAML). Distinguishes Lenny-internal runtimes that consume
    // workspace materializations from external agents that do not.
    SupportsWorkspaceFiles bool `json:"supportsWorkspaceFiles,omitempty"`

    // Skills enumerates the discrete capabilities the runtime advertises
    // (matches the `skills` field in §5.1 YAML). Rendered as the
    // `skills` array on auto-generated A2A agent cards.
    Skills []AgentInterfaceSkill `json:"skills,omitempty"`

    // Examples provides worked usage samples (matches the `examples`
    // field in §5.1 YAML). Rendered on A2A agent cards for human
    // consumers evaluating whether a runtime fits their use case.
    Examples []AgentInterfaceExample `json:"examples,omitempty"`
}

// AgentInterfaceMode is one entry in `AgentInterface.InputModes` or
// `AgentInterface.OutputModes`. Mirrors the YAML entry shape in §5.1.
type AgentInterfaceMode struct {
    // Type is the IANA media type (e.g., `"text/plain"`,
    // `"application/json"`).
    Type string `json:"type"`

    // Role is an optional tag distinguishing, e.g., `"primary"` from
    // supplemental modes. Omitted when unset.
    Role string `json:"role,omitempty"`
}

// AgentInterfaceSkill is one entry in `AgentInterface.Skills`. Mirrors
// the YAML entry shape in §5.1.
type AgentInterfaceSkill struct {
    // ID is the stable identifier for the skill (e.g., `"review"`).
    ID string `json:"id"`

    // Name is the human-readable skill name (e.g., `"Code Review"`).
    Name string `json:"name,omitempty"`

    // Description elaborates on what the skill does.
    Description string `json:"description,omitempty"`
}

// AgentInterfaceExample is one entry in `AgentInterface.Examples`.
// Mirrors the YAML entry shape in §5.1.
type AgentInterfaceExample struct {
    // Description explains what the example demonstrates.
    Description string `json:"description,omitempty"`

    // Input is the prompt or input text the example submits.
    Input string `json:"input,omitempty"`
}

// PublishedMetadataRef is an opaque handle to one `publishedMetadata` entry
// registered on a runtime ([Section 5.1 `publishedMetadata` Field](05_runtime-registry-and-pool-model.md#publishedmetadata-field)).
// Adapters receive refs — not materialized content — because metadata
// values may be large (A2A agent cards, OpenAPI specs) and because the
// gateway owns the opaque-pass-through storage contract. To surface a ref
// in a native discovery format, adapters issue an HTTP GET against URI,
// which resolves to `GET /v1/runtimes/{name}/meta/{key}` for public entries
// or `GET /internal/runtimes/{name}/meta/{key}` for tenant/internal entries
// (see the REST surface in [Section 15.1](#151-rest-api)). The gateway
// applies the same visibility filtering at serve time, so following a URI
// the adapter was handed will always succeed for the caller whose identity
// produced the slice.
type PublishedMetadataRef struct {
    // Key is the registration key of the entry (matches the `key` field in
    // the runtime's `publishedMetadata` YAML list). Adapters MAY use Key to
    // select a specific entry (e.g., `"agent-card"`) when composing their
    // native discovery format.
    Key string

    // ContentType is the IANA media type of the materialized value
    // (matches the `contentType` field in `publishedMetadata` YAML, e.g.,
    // `"application/json"`). Adapters SHOULD honor it when forwarding the
    // fetched body to downstream consumers.
    ContentType string

    // Visibility is the closed-enum visibility class of the entry:
    // "public", "tenant", or "internal". Values outside this set are
    // gateway-internal bugs. Adapters MUST NOT surface refs with
    // Visibility == "internal" or "tenant" on unauthenticated endpoints
    // (e.g., A2A `/.well-known/agent.json`); the gateway's visibility
    // filter already excludes refs the caller cannot see, but adapters
    // that cache refs across requests MUST re-check Visibility before
    // serving a cached ref to a different caller.
    Visibility string

    // URI is the absolute fetch URL the adapter uses to retrieve the
    // materialized value. It is stable for the lifetime of the entry and
    // MAY be embedded verbatim in discovery responses (e.g., an A2A agent
    // card's `agentCardUrl` field) when Visibility == "public".
    URI string

    // ETag is the current value's entity tag (RFC 7232). Adapters MAY
    // issue conditional fetches (`If-None-Match`) to avoid re-downloading
    // unchanged content. Empty string if the gateway has not yet computed
    // an ETag for the entry.
    ETag string
}
```

#### SessionEvent Kind Registry

The `SessionEventKind` enum above is **closed** — the gateway will never dispatch a kind value not listed below, and third-party adapters MUST NOT rely on receiving unknown kinds through `OutboundChannel.Send`. The registry below is authoritative for the gateway outbound dispatcher (see "Gateway outbound dispatch" paragraph further down in this section); additions require a `SessionEvent` schema version bump and a corresponding update to `AdapterCapabilities` / `OutboundCapabilitySet` documentation.

| Kind            | Constant                  | `SessionEvent.Payload` schema                                                                                                                         | Fires when                                                                                                                  |
| --------------- | ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `state_change`  | `SessionEventStateChange` | `{ "from": "<state>", "to": "<state>", "subState": "<input_required\|suspended\|...>?" }` — states and sub-states enumerated in [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). | The session transitions between top-level states or between `running` sub-states (`input_required`, `suspended`, etc.). One event per transition. |
| `output`        | `SessionEventOutput`      | `OutputPart[]` — the canonical output array defined in the `OutputPart` schema (see "Internal `OutputPart` Format" below).                           | The runtime emits an `agent_output` frame; each frame becomes one `SessionEvent` (or is batched per adapter-declared policy). |
| `elicitation`   | `SessionEventElicitation` | `MessageEnvelope` with `type: "message"` carrying the `lenny/request_elicitation` payload ([Section 9.2](09_mcp-integration.md#92-elicitation-chain)); adapters that also surface `lenny/request_input` MAY carry it under this kind. | The runtime calls `lenny/request_elicitation` or `lenny/request_input` and the elicitation chain surfaces the request to the adapter. |
| `tool_use`      | `SessionEventToolUse`     | `{ "toolCallId": "...", "tool": "...", "arguments": {...}, "phase": "requested\|approved\|denied\|completed", "result": OutputPart[]? }` — the adapter-facing projection of the `lenny/tool_call` lifecycle ([Section 9](09_mcp-integration.md)). | A tool call enters a new lifecycle phase (requested, approved/denied, completed). One event per phase transition.            |
| `error`         | `SessionEventError`       | `{ "code": "<ErrorCode>", "category": "TRANSIENT\|PERMANENT\|POLICY\|UPSTREAM", "message": "...", "retryable": bool, "details": {...}? }` — the shared error taxonomy from [Section 15.2.1](#1521-restmcp-consistency-contract) item 3. | A non-terminal error surfaces mid-session (validation, policy, upstream degradation) that the adapter should reflect to the client. |
| `terminated`    | `SessionEventTerminated`  | `TerminationReason` (Go struct serialized as `{"code": "...", "detail": "..."}`).                                                                     | The session enters a terminal state (`completed`, `failed`, `cancelled`, `expired`, `drained`). Exactly one event per session. |

**Dispatch-filter rule (normative).** Adapters declare the subset of kinds they handle via `OutboundCapabilitySet.SupportedEventKinds`. The gateway outbound dispatcher MUST filter events by each adapter's declared `SupportedEventKinds` before invoking `OutboundChannel.Send` — adapters whose declaration omits a kind MUST NOT receive events of that kind. A `SessionEvent` delivered in violation of this rule is a gateway bug; adapters MAY drop such events silently and SHOULD emit an `unexpected_event_kind` metric for operator investigation. Adapters MUST NOT extend their behavior to depend on events they did not declare — declaration is the authoritative contract.

**`state_change` sub-state coverage.** The `state_change` kind is the only mechanism by which adapters learn about `running` sub-states (`input_required`, `suspended`). Adapters that omit `state_change` from `SupportedEventKinds` forfeit visibility into these sub-states — they will only observe top-level state transitions (indirectly, via `terminated` for terminal states). Adapters requiring fine-grained lifecycle surfacing (A2A `input-required` tracking, pause/resume surfaces) MUST declare `state_change`.

**Capability-consistency invariant with elicitation policy.** An adapter that declares `OutboundCapabilitySet.SupportedEventKinds` including `elicitation` MUST also return `AdapterCapabilities.SupportsElicitation: true`. Conversely, adapters that impose `elicitationDepthPolicy: block_all` at session creation (notably `A2AAdapter`, [Section 21.1](21_planned-post-v1.md#21-planned--post-v1)) MUST NOT include `elicitation` in `SupportedEventKinds` — the session will never generate an elicitation event for such adapters, and declaring the kind would mislead clients. The `A2AAdapter`'s four-kind declaration (`state_change`, `output`, `error`, `terminated`) satisfies this invariant by construction.

**Gateway outbound dispatch.** When a session event fires, the gateway iterates all adapters that have an active `OutboundChannel` for the session (tracked in an adapter-keyed map per session). For each channel — after filtering by the dispatch-filter rule above — it calls `Send` with the event. Channels that return a non-nil error are closed and removed from the map. Adapters choose their own delivery semantics inside `Send` — buffered HTTP POST, SSE frame write, or silent drop with a metric increment. The gateway does not impose a delivery order guarantee across adapters.

**`HandleDiscovery` is required on all adapters.** Every adapter translates Lenny's policy-scoped runtime list into its protocol's native discovery format. Each adapter **must** include its own `AdapterCapabilities` as an `adapterCapabilities` annotation in its discovery output so that consumers know which protocol-level capabilities (elicitation, delegation, interrupts, session continuity) the active adapter provides. The gateway calls `Capabilities()` on the serving adapter and passes the result to `HandleDiscovery` as an additional parameter alongside the runtime list; adapters embed the capability fields in their native discovery format (e.g., a top-level `adapterCapabilities` object in REST and `list_runtimes` responses, or a `capabilities` node in A2A agent cards). At minimum, `supportsElicitation` must be surfaced — callers must not start elicitation-dependent workflows against an adapter that returns `supportsElicitation: false`.

**Three tiers of pluggability:**

- **Built-in** (compiled in): MCP, OpenAI Completions, Open Responses. Always available, configurable via admin API.
- **Config-driven**: deployer points gateway at a Go plugin binary or gRPC service at startup.
- **Runtime registration via admin API**: `POST /v1/admin/external-adapters` — takes effect immediately, no restart.

**Built-in adapter inventory:**

| Adapter                    | Path prefix            | Protocol                     | Status  |
| -------------------------- | ---------------------- | ---------------------------- | ------- |
| `MCPAdapter`               | `/mcp`                 | MCP Streamable HTTP          | V1      |
| `OpenAICompletionsAdapter` | `/v1/chat/completions` | OpenAI Chat Completions      | V1      |
| `OpenResponsesAdapter`     | `/v1/responses`        | Open Responses Specification | V1      |
| `A2AAdapter`               | `/a2a/{runtime}`       | A2A                          | Post-V1 |
| `AgentProtocolAdapter`     | `/ap/v1/agent`         | Agent Protocol               | Post-V1 |

`OpenResponsesAdapter` covers both Open Responses-compliant clients and OpenAI Responses API clients. OpenAI's Responses API is a proper superset of Open Responses; the difference is OpenAI's proprietary hosted tools, which Lenny doesn't implement.

**`type: mcp` runtime dedicated endpoints:** Each enabled `type: mcp` runtime gets a dedicated MCP endpoint at `/mcp/runtimes/{runtime-name}`. Standard MCP capability negotiation. Not aggregated. An implicit session record is created per connection for audit and billing. Discovery: `GET /v1/runtimes` and `list_runtimes` return the `mcpEndpoint` field on the `AuthorizedRuntime` schema ([Shared Adapter Types](#shared-adapter-types)) for `type: mcp` runtimes; a `mcp-capabilities` `publishedMetadata` entry ([Section 5.1](05_runtime-registry-and-pool-model.md#publishedmetadata-field)) carries the tools preview and is fetched via `GET /v1/runtimes/{name}/meta/{key}`.

### 15.1 REST API

The REST API covers all non-interactive operations. It is the primary integration point for CI/CD pipelines, admin dashboards, CLIs, and clients in any language.

**OpenAPI spec endpoint.** The gateway serves its OpenAPI 3.x specification at `GET /openapi.yaml` (no authentication required). The same document is available at `GET /openapi.json` for clients that prefer JSON. The served spec reflects the API version of the running gateway instance; the `info.version` field in the spec matches the gateway's release version. Community SDK generators should target `/openapi.yaml` as the canonical source. The spec is generated from the same source-of-truth that drives REST/MCP contract tests ([Section 15.2.1](#1521-restmcp-consistency-contract)).

**Session lifecycle:**

| Method   | Endpoint                      | Description                                                               |
| -------- | ----------------------------- | ------------------------------------------------------------------------- |
| `POST`   | `/v1/sessions`                | Create a new session                                                      |
| `POST`   | `/v1/sessions/start`          | Create, upload inline files, and start in one call (convenience)          |
| `GET`    | `/v1/sessions/{id}`           | Get session status and metadata                                           |
| `GET`    | `/v1/sessions`                | List sessions (filterable by status, runtime, tenant, labels)             |
| `POST`   | `/v1/sessions/{id}/upload`    | Upload workspace files (pre-start or mid-session if enabled)              |
| `POST`   | `/v1/sessions/{id}/finalize`  | Finalize workspace and run setup                                          |
| `POST`   | `/v1/sessions/{id}/start`     | Start the agent runtime                                                   |
| `POST`   | `/v1/sessions/{id}/interrupt` | Interrupt current agent work                                              |
| `POST`   | `/v1/sessions/{id}/terminate` | End a session                                                             |
| `POST`   | `/v1/sessions/{id}/resume`    | Explicitly resume after retry exhaustion                                  |
| `POST`   | `/v1/sessions/{id}/derive`    | Create a new session pre-populated with this session's workspace snapshot |
| `POST`   | `/v1/sessions/{id}/tool-use/{tool_call_id}/approve` | Approve a pending tool call                                |
| `POST`   | `/v1/sessions/{id}/tool-use/{tool_call_id}/deny`    | Deny a pending tool call. Optional body: `{"reason": "<string>"}` |
| `POST`   | `/v1/sessions/{id}/elicitations/{elicitation_id}/respond` | Answer an elicitation request. Body: `{"response": <value>}` |
| `POST`   | `/v1/sessions/{id}/elicitations/{elicitation_id}/dismiss` | Dismiss a pending elicitation                         |
| `DELETE` | `/v1/sessions/{id}`           | Terminate and clean up                                                    |

**State-mutating endpoint preconditions.** The following table maps each state-mutating session endpoint to its valid precondition states and resulting state transitions. Calling an endpoint in an invalid state returns `409 INVALID_STATE_TRANSITION` with `details.currentState` and `details.allowedStates`.

| Endpoint                           | Valid precondition states                                          | Resulting transition                                                                   | Notes                                                                                                                           |
| ---------------------------------- | ------------------------------------------------------------------ | -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `POST /v1/sessions/{id}/upload`    | `created`; also `running` when runtime declares `capabilities.midSessionUpload: true` and deployer policy allows it | remains in current state                                                               | Pre-start: gateway rejects if session already finalized or started. Mid-session: see [Section 7.4](07_session-lifecycle.md#74-upload-safety)                                |
| `POST /v1/sessions/{id}/finalize`  | `created`                                                          | `finalizing` → `ready`                                                                 | Triggers workspace materialization and setup commands                                                                           |
| `POST /v1/sessions/{id}/start`     | `ready`                                                            | `starting` → `running`                                                                 | Starts the agent runtime                                                                                                        |
| `POST /v1/sessions/{id}/interrupt` | `running`                                                          | `suspended`                                                                            | Only valid while the agent is actively executing. Not valid in `suspended`, `starting`, `finalizing`, or any terminal state.    |
| `POST /v1/sessions/{id}/terminate` | `created`, `finalizing`, `ready`, `starting`, `running`, `suspended`, `resume_pending`, `awaiting_client_action` | `completed`                                                                            | Valid in any non-terminal state. For `created`, the gateway cancels any pending finalization, releases resources if any were allocated, and marks the session `completed`. For `finalizing` and `ready`, the gateway aborts the in-progress setup or dequeues the waiting session, releases the pod, and marks the session `completed`. Graceful shutdown.                                                                  |
| `POST /v1/sessions/{id}/resume`    | `awaiting_client_action`                                           | `resume_pending` → `running`                                                           | Only valid after automatic retries are exhausted. Not valid in `suspended` (use message delivery or `resume_session` for that). `resuming` is an internal-only transient state between `resume_pending` and `running`; the API reports the transition as `resume_pending` → `running`. |
| `POST /v1/sessions/{id}/messages`  | Any non-terminal state. Delivery semantics vary by state: `running` and `suspended` deliver or buffer per [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) paths 1-7; `resume_pending` and `awaiting_client_action` enqueue to DLQ; pre-running states buffer (inter-session) or reject with `TARGET_NOT_READY` (external client). | `running` (if `suspended` with `delivery: immediate`, atomically resumes and delivers); no state change for other states | See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) for full delivery semantics per state.                                                                                         |
| `POST /v1/sessions/{id}/derive`    | `completed`, `failed`, `cancelled`, `expired` (default); `running`, `suspended`, `resume_pending`, `awaiting_client_action` (requires `allowStale: true` in request body) | Creates a new session (original unchanged) | Terminal sessions use the sealed or last-checkpoint snapshot. Non-terminal sessions require `allowStale: true`; derive uses the most recent successful checkpoint snapshot. Response includes `workspaceSnapshotSource` and `workspaceSnapshotTimestamp`. See [Section 7.1](07_session-lifecycle.md#71-normal-flow) derive semantics. |
| `DELETE /v1/sessions/{id}`         | Any non-terminal state                                             | `cancelled`                                                                            | Force-cancels the session and releases resources. Unlike `/terminate` (which marks the session `completed` as a graceful, successful end), `DELETE` always records a `cancelled` terminal state for audit and billing purposes. Use `/terminate` to signal normal completion; use `DELETE` to abandon a session. |

**Externally visible vs. internal-only states.** The REST API (`GET /v1/sessions/{id}`) returns session states from the **session/task state model** ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model), 8.8), not the pod state model ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). Pod states are internal implementation details not exposed to API callers.

| External session state (returned in API) | Description                                                   | Terminal? |
| ---------------------------------------- | ------------------------------------------------------------- | --------- |
| `created`                                | Session created; a warm pod has been claimed and credentials assigned (see [§7.1](07_session-lifecycle.md#71-normal-flow) steps 4–6), awaiting workspace file uploads or finalization. **TTL:** `maxCreatedStateTimeoutSeconds` (default 300s). On expiry the gateway transitions the session to `expired`, releases the pod claim back to the pool, and revokes the credential lease. `maxCreatedStateTimeoutSeconds` prevents stale sessions from accumulating indefinitely. Configurable via `gateway.maxCreatedStateTimeoutSeconds`. | No        |
| `finalizing`                             | Workspace materialization and setup commands in progress      | No        |
| `ready`                                  | Setup complete, awaiting `start`                              | No        |
| `starting`                               | Agent runtime is launching                                    | No        |
| `running`                                | Agent is actively executing                                   | No        |
| `suspended`                              | Agent paused via `interrupt`; pod held, workspace preserved   | No        |
| `resume_pending`                         | Pod failed; gateway is retrying on a new pod                  | No        |
| `awaiting_client_action`                 | Retries exhausted; client must explicitly resume or terminate | No        |
| `completed`                              | Agent finished successfully                                   | Yes       |
| `failed`                                 | Unrecoverable error                                           | Yes       |
| `cancelled`                              | Cancelled by client or parent                                 | Yes       |
| `expired`                                | Lease, budget, or deadline exhausted                          | Yes       |

Internal-only states (from the pod state machine in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) such as `warming`, `idle`, `claimed`, `receiving_uploads`, `running_setup`, `sdk_connecting`, and `resuming` are **never** returned in external API responses. These are tracked in the `Sandbox` CRD `.status.phase` for controller reconciliation and operational monitoring only.

**Session `failureClass` field and reachability.** When `state = "failed"`, the session response body includes a `failureClass` field (nullable string) classifying the failure origin. Values and semantics are authoritative in [§7.1](07_session-lifecycle.md#71-normal-flow) Session `failureClass` field. Callers can filter `GET /v1/sessions` by this field via `?failureClass=<value>` (repeatable).

**Derive-failure audit rows (`failureClass = derive_failure`).** Terminal `failed` Session rows written under the `gateway.persistDeriveFailureRows: true` opt-in ([§7.1](07_session-lifecycle.md#71-normal-flow) derive rule 2) are visible through the REST API with the following bounded reachability. These rows represent failed `POST /v1/sessions/{id}/derive` attempts captured for audit; no pod, credential lease, workspace, transcript, or event stream ever existed for them.

| Endpoint                                                                | Behavior for a `derive_failure` row                                                                                                                                                                                           |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `GET /v1/sessions/{id}`                                                  | Returns `200` with the session envelope. `state = "failed"`, `failureClass = "derive_failure"`. `sessionIsolationLevel` reflects the intended target pool. `workspaceSnapshotSource` is absent (no workspace was materialised). |
| `GET /v1/sessions`                                                        | **Included by default.** Supports `?includeDeriveFailures=false` to exclude them. Also supports `?failureClass=derive_failure` to filter to only these rows. Default behaviour preserves audit visibility.                     |
| `GET /v1/admin/sessions/{id}`                                             | Returns `200` with the admin envelope; pod assignment and pool details are absent (no pod was ever claimed). `failureClass = "derive_failure"`.                                                                                 |
| `POST /v1/sessions/{id}/terminate`                                        | Returns `409 INVALID_STATE_TRANSITION` — row is already terminal. `details.currentState = "failed"`, `details.allowedStates` lists the non-terminal states per the precondition table.                                         |
| `POST /v1/sessions/{id}/start`                                            | Returns `409 INVALID_STATE_TRANSITION` — row is already terminal.                                                                                                                                                              |
| `POST /v1/sessions/{id}/resume`                                           | Returns `409 INVALID_STATE_TRANSITION` — row is already terminal.                                                                                                                                                              |
| `POST /v1/sessions/{id}/interrupt`, `/upload`, `/finalize`, `/messages`, `/eval`, `/replay` | Return `409 INVALID_STATE_TRANSITION` — row is already terminal. `/replay` additionally fails because no workspace snapshot exists (no MinIO object was ever written for the derived session).                                |
| `DELETE /v1/sessions/{id}`                                                | Returns `409 INVALID_STATE_TRANSITION` — row is already terminal. To permanently remove an audit row, operators use tenant-scoped retention/purge tooling ([§12.8](12_storage-architecture.md#128-compliance-interfaces)).     |
| `POST /v1/sessions/{id}/derive`                                            | Returns `400 VALIDATION_ERROR` with `details.fields[0].field: "sourceSessionId"` and message `"source session has no resolvable workspace snapshot"` (same error as deriving from any session that never produced a snapshot). |
| Event-stream attach (`attach_session`, SSE reconnect, `/v1/sessions/{id}/events`) | Returns `404 RESOURCE_NOT_FOUND`. No `SeqNum` counter was ever allocated; no events were ever emitted; the row is not `attach_session`-reachable.                                                                               |
| `GET /v1/sessions/{id}/artifacts`, `/workspace`, `/transcript`, `/logs`, `/setup-output`, `/tree`, `/usage`, `/webhook-events`, `/messages` | Return `404 RESOURCE_NOT_FOUND`. No workspace, transcript, logs, tree, usage data, or delivered messages ever existed.                                                                                                         |

These rows do **not** consume active-session quota (they are terminal from birth), do **not** reserve pool or credential capacity, and do **not** appear in `GET /v1/sessions/{id}/tree` even when the failed derive had a source session (the parent lineage is recorded in audit events but no tree node is materialised). The audit-only nature is the primary reason the opt-in defaults to `false`: deployers without a regulatory requirement for failed-derive audit trails should avoid the storage and listing noise by leaving the default in place.

**Artifacts and introspection:**

| Method | Endpoint                             | Description                                                                                                                          |
| ------ | ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------ |
| `GET`  | `/v1/sessions/{id}/artifacts`        | List session artifacts                                                                                                               |
| `GET`  | `/v1/sessions/{id}/artifacts/{path}` | Download a specific artifact/file                                                                                                    |
| `GET`  | `/v1/sessions/{id}/workspace`        | Download workspace snapshot (tar.gz)                                                                                                 |
| `GET`  | `/v1/sessions/{id}/transcript`       | Get session transcript (paginated)                                                                                                   |
| `GET`  | `/v1/sessions/{id}/logs`             | Get session logs (paginated, streamable via SSE)                                                                                     |
| `GET`  | `/v1/sessions/{id}/setup-output`     | Get setup command stdout/stderr                                                                                                      |
| `GET`  | `/v1/sessions/{id}/tree`             | Get delegation task tree                                                                                                             |
| `GET`  | `/v1/sessions/{id}/usage`            | Get token and resource usage. Returns tree-aggregated usage (including all descendant tasks) when the session has a delegation tree. |
| `POST` | `/v1/sessions/{id}/extend-retention` | Extend artifact retention TTL. Body: `{"ttlSeconds": <n>}`. See [Section 7.1](07_session-lifecycle.md#71-normal-flow).                                                        |
| `GET`  | `/v1/sessions/{id}/webhook-events`   | List undelivered webhook events after retry exhaustion. See [Section 14](14_workspace-plan-schema.md) (`callbackUrl` field).                                        |

**Blob resolution:**

| Method | Endpoint              | Description                                                                                                                                                                                                                                                                                                                                                                        |
| ------ | --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/v1/blobs/{ref}`     | Resolve and download a `lenny-blob://` reference (see [Section 15.4.1](#1541-adapterbinary-protocol), `LennyBlobURI` scheme). `{ref}` is the full `lenny-blob://` URI, URL-encoded. The gateway verifies that the caller's identity has read access to the tenant and session embedded in the URI (`tenant_id`, `session_id` components) before retrieving the blob from the artifact store and streaming it back. Returns the blob bytes with the `Content-Type` header set to the blob's `mimeType`. Returns `404` if the blob has expired (`ttl` elapsed) or was never written; returns `403` if the caller lacks access to the owning session. REST adapter clients use this endpoint to dereference `ref` fields in `OutputPart` responses — external protocol adapters (MCP, OpenAI, A2A) MUST dereference `ref` fields internally and MUST NOT pass `lenny-blob://` URIs to external callers. |

**Async job support:**

| Method | Endpoint                     | Description                                                                                                                                     |
| ------ | ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST` | `/v1/sessions/start`         | Accepts optional `callbackUrl` for completion notification                                                                                      |
| `POST` | `/v1/sessions/{id}/messages` | Send a message to a session (unified endpoint — replaces `send`). Gateway rejects injection against runtimes with `injection.supported: false`. |
| `GET`  | `/v1/sessions/{id}/messages` | List messages sent to or from a session (paginated). Returns message history including delivery receipts and state. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model).             |

**Discovery and introspection:**

| Method | Endpoint                         | Description                                                                                                                                           |
| ------ | -------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/v1/runtimes`                   | List registered runtimes. Each item follows the `AuthorizedRuntime` schema ([Shared Adapter Types](#shared-adapter-types)): `name`, `agentInterface`, `mcpEndpoint`, `adapterCapabilities`, and `publishedMetadata` refs. Identity-filtered and policy-scoped. Per-runtime extras (labels, capability previews, agent cards) are surfaced as `publishedMetadata` entries ([Section 5.1](05_runtime-registry-and-pool-model.md#publishedmetadata-field)) and fetched via `GET /v1/runtimes/{name}/meta/{key}`. |
| `GET`  | `/v1/runtimes/{name}/meta/{key}` | Get published metadata for a runtime (visibility-controlled)                                                                                          |
| `GET`  | `/.well-known/agent.json`        | **Post-V1 (A2A).** Aggregated A2A agent card discovery endpoint. Returns JSON array of all public `agent-card` entries (**intentional Lenny extension** — the A2A spec requires a single `AgentCard` object; see [Section 21.1](21_planned-post-v1.md) for rationale and per-runtime standard-compliant endpoints). No auth. |
| `GET`  | `/a2a/runtimes/{name}/.well-known/agent.json` | **Post-V1 (A2A).** Per-runtime A2A agent card endpoint. Returns a single `AgentCard` object conforming to the A2A spec (§3). Standard A2A clients that expect a single object SHOULD use this endpoint. No auth. See [Section 21.1](21_planned-post-v1.md). |
| `GET`  | `/v1/models`                     | OpenAI-compatible model list (identity-filtered)                                                                                                      |
| `GET`  | `/v1/pools`                      | List pools and warm pod counts                                                                                                                        |
| `GET`  | `/v1/usage`                      | Usage report (filterable by tenant, user, window, labels)                                                                                             |
| `GET`  | `/v1/metering/events`            | Paginated billing event stream                                                                                                                        |

**User credential management:**

| Method   | Endpoint                           | Description                                                                                                                                  |
| -------- | ---------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST`   | `/v1/credentials`                                  | Register a credential for the authenticated user. One credential per provider; re-registering replaces. See [Section 4.9](04_system-components.md#49-credential-leasing-service) pre-authorized flow. |
| `GET`    | `/v1/credentials`                                  | List the authenticated user's registered credentials (no secret material returned).                                                          |
| `PUT`    | `/v1/credentials/{credential_ref}`                 | Rotate (replace) secret material for an existing credential. Active leases are immediately rotated. See [Section 4.9](04_system-components.md#49-credential-leasing-service).                         |
| `POST`   | `/v1/credentials/{credential_ref}/revoke`          | Revoke a credential and immediately invalidate all active leases backed by it. See [Section 4.9](04_system-components.md#49-credential-leasing-service).                                              |
| `DELETE` | `/v1/credentials/{credential_ref}`                 | Remove a registered credential. Active session leases are unaffected. See [Section 4.9](04_system-components.md#49-credential-leasing-service).                                                      |

**Evaluation hooks:**

| Method | Endpoint                   | Description                                                                                                                     |
| ------ | -------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `POST` | `/v1/sessions/{id}/eval`   | Accept scored evaluator results (LLM-as-judge scores, custom heuristics, ground-truth comparisons). Stored as session metadata. |
| `POST` | `/v1/sessions/{id}/replay` | Re-run a session against a different runtime version using the same workspace and prompt history. See **Session Replay Semantics** below. |

#### Session Replay Semantics

`POST /v1/sessions/{id}/replay` creates a new independent session that replays the source session's prompt history against a different runtime version. This is the primary mechanism for regression testing and A/B evaluation of runtime upgrades.

**Request body:**

```json
{
  "targetRuntime": "<runtime-name>",
  "targetPool": "<pool-name (optional)>",
  "replayMode": "prompt_history | workspace_derive",
  "evalRef": "<eval-id (optional)>",
  "allowIsolationDowngrade": false
}
```

**Semantics:**

- **`replayMode: prompt_history`** (default): The source session's prompt history (from `GET /v1/sessions/{id}/transcript`) is replayed verbatim as the initial message sequence to the new session. The workspace is populated with the source session's sealed final workspace snapshot (or last checkpoint if the source session did not seal cleanly). The replayed session starts fresh — it has no knowledge of prior tool call results and will re-execute tool calls using the new runtime.
- **`replayMode: workspace_derive`**: Equivalent to `POST /v1/sessions/{id}/derive` with `targetRuntime` substituted. The new session starts with the source workspace but receives no pre-loaded prompt history. Use this mode when testing the new runtime's behavior from a clean start with an identical filesystem state.
- **`targetRuntime`** (required): The runtime name to replay against. Must be a registered runtime with the same `executionMode` as the source session. A different `executionMode` returns `400 INCOMPATIBLE_RUNTIME`.
- **`targetPool`** (optional): If omitted, the gateway selects the default pool for `targetRuntime`. Must be a pool backed by `targetRuntime`.
- **`evalRef`** (optional): If provided, links the replayed session to an experiment or eval set. The `evalRef` is recorded in the new session's metadata and can be used to filter `GET /v1/sessions` by eval context.
- **`allowIsolationDowngrade`** (optional, default `false`, SEC-001): The monotonicity rule applies whenever the replay targets a pool whose `sessionIsolationLevel.isolationProfile` differs from the source session's: the target pool's profile MUST be at least as restrictive as the source session's (`standard` < `sandboxed` < `microvm`, matching [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) delegation monotonicity). A weaker target pool is rejected with `ISOLATION_MONOTONICITY_VIOLATED` (HTTP 422) unless this flag is set to `true`. Setting the flag requires the caller to hold the `platform-admin` role; non-admin callers receive `403 FORBIDDEN`. When the override is used, the gateway emits a `derive.isolation_downgrade` audit event (shared with `POST /v1/sessions/{id}/derive`) capturing source/target isolation profiles and the authorizing admin identity. The rule applies to both replay modes, as follows: for `replayMode: workspace_derive`, the monotonicity check runs against the resolved `targetPool` (or the default pool for `targetRuntime` when `targetPool` is omitted). For `replayMode: prompt_history` without an explicit `targetPool`, the replay reuses the source session's pool, the monotonicity check is trivially satisfied, and the flag is a no-op. For `replayMode: prompt_history` with an explicit `targetPool`, the monotonicity rule and `allowIsolationDowngrade` flag apply identically to `workspace_derive` — same error code, same admin-role check, same audit event.

**Preconditions:** Source session must be in a terminal state (`completed`, `failed`, `cancelled`, `expired`) with a resolvable workspace snapshot. Non-terminal source sessions return `409 REPLAY_ON_LIVE_SESSION`.

**Response:** Returns the new session's `session_id`, `uploadToken`, and `sessionIsolationLevel` — identical to `POST /v1/sessions`. The new session proceeds through the standard lifecycle (upload → finalize → start → run).

**Credential handling:** The replayed session goes through standard `CredentialPolicy` evaluation ([Section 7.1](07_session-lifecycle.md#71-normal-flow), step 6) independently — credentials are never inherited from the source session.

**Comprehensive Admin API:**

All operational configuration is API-managed. Configuration is split into two planes:

**Operational plane — API-managed:** Runtimes, Delegation Policies, Connectors, Pools, Credential Pools, Tenants, Quotas (embedded in tenant records — managed via `PUT /v1/admin/tenants/{id}`), User Role Assignments, Experiments, External Adapters, Environments, Tenant RBAC Config.

**Bootstrap plane — Helm only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions, Memory Store implementation choice and backend connection config.

> **Note on items not listed above:** Egress Profiles are an enum field on pool and runtime definitions, managed through pool/runtime endpoints — they are not a separate CRUD resource. Scaling Policies are a sub-field within pool definitions (`scalePolicy`), managed through `PUT /v1/admin/pools/{name}` and `PUT /v1/admin/pools/{name}/warm-count`. Webhook delivery (`callbackUrl`) is a per-session field, not a platform-admin-managed subscription resource.

CRDs become derived state reconciled from Postgres by PoolScalingController.

All admin CRUD resources use `{name}` as the path identifier (human-readable, unique within scope). Tenants use `{id}` (opaque UUID) because tenant names are mutable display labels. This convention applies uniformly to every admin resource type below.

**Runtime and pool records are platform-global** (no `tenant_id` column, no RLS). Tenant-scoped visibility is enforced at the application layer via `runtime_tenant_access` and `pool_tenant_access` join tables. `platform-admin` callers receive unfiltered results; `tenant-admin` and `tenant-viewer` callers receive results filtered to the access-table entries for their tenant. Only `platform-admin` can create new runtime/pool definitions or grant access to a tenant; `tenant-admin` can update configuration for already-granted records only.

| Method   | Endpoint                                                        | Description                                                                                                                                                                                                                                                                                                                |
| -------- | --------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST`   | `/v1/admin/runtimes`                                            | Create a runtime definition (`platform-admin` only; creates a global record not yet visible to any tenant)                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/runtimes`                                            | List runtime definitions (application-layer filtered: `tenant-admin` sees own tenant's access-table entries; `platform-admin` sees all)                                                                                                                                                                                    |
| `GET`    | `/v1/admin/runtimes/{name}`                                     | Get a specific runtime definition (returns `404` if not in caller's access-table entries)                                                                                                                                                                                                                                  |
| `PUT`    | `/v1/admin/runtimes/{name}`                                     | Update a runtime definition (requires `If-Match`; `tenant-admin` restricted to access-table entries for own tenant)                                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/runtimes/{name}`                                     | Delete a runtime definition (`platform-admin` only)                                                                                                                                                                                                                                                                        |
| `POST`   | `/v1/admin/runtimes/{name}/tenant-access`                       | Grant a tenant access to a runtime. Body: `{"tenantId": "<uuid>"}`. Creates a `runtime_tenant_access` join-table entry. Idempotent — returns `200` if the grant already exists. Requires `platform-admin`.                                                                                                                 |
| `GET`    | `/v1/admin/runtimes/{name}/tenant-access`                       | List tenants with access to a runtime. Returns `[{"tenantId", "tenantName", "grantedAt", "grantedBy"}]`. Requires `platform-admin`.                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/runtimes/{name}/tenant-access/{tenantId}`            | Revoke a tenant's access to a runtime. Deletes the `runtime_tenant_access` join-table entry. Returns `404` if the grant does not exist. Requires `platform-admin`.                                                                                                                                                          |
| `POST`   | `/v1/admin/delegation-policies`                                 | Create a delegation policy                                                                                                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/delegation-policies`                                 | List all delegation policies                                                                                                                                                                                                                                                                                               |
| `GET`    | `/v1/admin/delegation-policies/{name}`                          | Get a specific delegation policy                                                                                                                                                                                                                                                                                           |
| `PUT`    | `/v1/admin/delegation-policies/{name}`                          | Update a delegation policy (requires `If-Match`)                                                                                                                                                                                                                                                                           |
| `DELETE` | `/v1/admin/delegation-policies/{name}`                          | Delete a delegation policy. Rejected with `RESOURCE_HAS_DEPENDENTS` if any runtime or active delegation lease references this policy (see [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease), deletion guard).                                                                                                                                                    |
| `POST`   | `/v1/admin/connectors`                                          | Create a connector definition                                                                                                                                                                                                                                                                                              |
| `GET`    | `/v1/admin/connectors`                                          | List all connector definitions                                                                                                                                                                                                                                                                                             |
| `GET`    | `/v1/admin/connectors/{name}`                                   | Get a specific connector definition                                                                                                                                                                                                                                                                                        |
| `PUT`    | `/v1/admin/connectors/{name}`                                   | Update a connector definition (requires `If-Match`)                                                                                                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/connectors/{name}`                                   | Delete a connector definition                                                                                                                                                                                                                                                                                              |
| `POST`   | `/v1/admin/connectors/{name}/test`                              | Live connectivity test: DNS, TLS, MCP handshake, auth validation (rate-limited: 10/min per connector)                                                                                                                                                                                                                      |
| `POST`   | `/v1/admin/pools`                                               | Create a pool configuration (`platform-admin` only; pool is platform-global until tenant access is granted)                                                                                                                                                                                                                |
| `GET`    | `/v1/admin/pools`                                               | List pool configurations (application-layer filtered: `tenant-admin` sees own tenant's access-table entries; `platform-admin` sees all)                                                                                                                                                                                    |
| `GET`    | `/v1/admin/pools/{name}`                                        | Get a specific pool configuration (returns `404` if not in caller's access-table entries)                                                                                                                                                                                                                                  |
| `PUT`    | `/v1/admin/pools/{name}`                                        | Update a pool configuration (requires `If-Match`; `tenant-admin` restricted to access-table entries for own tenant)                                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/pools/{name}`                                        | Delete a pool configuration (`platform-admin` only)                                                                                                                                                                                                                                                                        |
| `POST`   | `/v1/admin/pools/{name}/drain`                                  | Drain a pool — transitions the pool to `draining` state, stops assigning warm pods to new sessions, and waits for in-flight sessions to complete or timeout before pod cleanup. **Backpressure for in-flight sessions:** while the pool is in `draining` state, any new `POST /v1/sessions` (or `create_session` MCP call) that would have selected this pool returns `503 POOL_DRAINING` with a `Retry-After: <seconds>` response header. The `Retry-After` value is computed as `ceil(estimated_drain_completion_seconds)` based on the longest active session age in the pool (capped at `maxSessionAgeSeconds`). Clients MUST respect `Retry-After` before retrying; the gateway rate-limits retry-after violations per client IP. The drain API response body includes `{"status": "draining", "activeSessions": <n>, "estimatedDrainSeconds": <n>}`. A `GET /v1/admin/pools/{name}` query returns `"phase": "draining"` and `"activeSessions": <n>` while drain is in progress. Metric: `lenny_pool_draining_sessions_total` (gauge, labeled by `pool`) tracks in-flight sessions during drain. |
| `GET`    | `/v1/admin/pools/{name}/sync-status`                            | Report CRD reconciliation state: `postgresGeneration`, `crdGeneration`, `lastReconciledAt`, `lagSeconds`, `inSync`                                                                                                                                                                                                         |
| `POST`   | `/v1/admin/pools/{name}/resume-reconciliation`                  | Clear a pool's `PoolScalingAdmissionStuck` state (see [§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries) "PoolScalingController admission-denial handling policy"). Resets the PSC's in-memory consecutive-denial counter for the pool so the next reconciliation cycle attempts an apply. Use after the operator has corrected the Postgres configuration that was causing admission denials (typically a tenant-authored invalid `checkpointBarrierAckTimeoutSeconds` or a tier classification mismatch). Returns `200` with `{"pool": "<name>", "denialCounterCleared": <n>}`. Returns `404` if the pool has no active denial state. Emits `pool.reconciliation_resumed` audit event. Requires `platform-admin`. |
| `PUT`    | `/v1/admin/pools/{name}/warm-count`                             | Adjust minWarm/maxWarm at runtime (requires `If-Match`)                                                                                                                                                                                                                                                                    |
| `PUT`    | `/v1/admin/pools/{name}/circuit-breaker`                        | Override the SDK-warm circuit-breaker state for a pool. Body: `{"sdkWarm": {"circuitBreakerOverride": "enabled" \| "disabled" \| "auto"}}`. Values: `enabled` — forces SDK-warm on regardless of demotion rate (use only after narrowing `sdkWarmBlockingPaths`); `disabled` — forces SDK-warm off regardless of demotion rate; `auto` — clears any override and restores automatic circuit-breaker control. Returns `409 INVALID_STATE_TRANSITION` if the pool's `sdkWarm.enabled` is `false` (circuit-breaker override has no effect on non-SDK-warm pools). Emits `pool.sdk_warm_circuit_breaker_override` audit event recording operator identity, previous state, and new value. Requires `platform-admin` or `tenant-admin` role (requires `If-Match`). See [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) (SDK-warm circuit-breaker).                                             |
| `POST`   | `/v1/admin/pools/{name}/tenant-access`                          | Grant a tenant access to a pool. Body: `{"tenantId": "<uuid>"}`. Creates a `pool_tenant_access` join-table entry. Idempotent — returns `200` if the grant already exists. Requires `platform-admin`.                                                                                                                       |
| `GET`    | `/v1/admin/pools/{name}/tenant-access`                          | List tenants with access to a pool. Returns `[{"tenantId", "tenantName", "grantedAt", "grantedBy"}]`. Requires `platform-admin`.                                                                                                                                                                                           |
| `DELETE` | `/v1/admin/pools/{name}/tenant-access/{tenantId}`               | Revoke a tenant's access to a pool. Deletes the `pool_tenant_access` join-table entry. Returns `404` if the grant does not exist. Requires `platform-admin`.                                                                                                                                                               |
| `POST`   | `/v1/admin/credential-pools`                                    | Create a credential pool (tenant-scoped; `tenant-admin` sees own tenant's pools, `platform-admin` sees all with optional `?tenant_id=` filter)                                                                                                                                                                             |
| `GET`    | `/v1/admin/credential-pools`                                    | List credential pools (tenant-scoped)                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/credential-pools/{name}`                             | Get a specific credential pool                                                                                                                                                                                                                                                                                             |
| `PUT`    | `/v1/admin/credential-pools/{name}`                             | Update a credential pool (requires `If-Match`)                                                                                                                                                                                                                                                                             |
| `DELETE` | `/v1/admin/credential-pools/{name}`                             | Delete a credential pool                                                                                                                                                                                                                                                                                                   |
| `POST`   | `/v1/admin/credential-pools/{name}/credentials/{credId}/revoke` | Emergency revocation of a single compromised credential; immediately invalidates all active leases backed by that credential and adds it to the credential deny list (see [Section 4.9](04_system-components.md#49-credential-leasing-service) Emergency Credential Revocation)                                                                                                     |
| `POST`   | `/v1/admin/credential-pools/{name}/credentials/{credId}/re-enable` | Re-enable a previously revoked pool credential. Restores credential to `healthy` status with a fresh health score. Requires `platform-admin`. Body: optional `reason` (string). Emits `credential.re_enabled` audit event (fields: `pool_id`, `credential_id`, `reason`, `re_enabled_by`). Use after emergency rotation to restore the original credential if the revocation was temporary. |
| `POST`   | `/v1/admin/credential-pools/{name}/revoke`                      | Emergency revocation of all credentials in a pool                                                                                                                                                                                                                                                                          |
| `POST`   | `/v1/admin/tenants`                                             | Create a tenant. Handler creates a per-tenant Postgres billing sequence: `CREATE SEQUENCE IF NOT EXISTS billing_seq_{tenant_id} START WITH 1 INCREMENT BY 1 NO CYCLE`. This sequence must exist before any billing event is written for the tenant. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                     |
| `GET`    | `/v1/admin/tenants`                                             | List all tenants                                                                                                                                                                                                                                                                                                           |
| `GET`    | `/v1/admin/tenants/{id}`                                        | Get a specific tenant                                                                                                                                                                                                                                                                                                      |
| `PUT`    | `/v1/admin/tenants/{id}`                                        | Update a tenant (requires `If-Match`)                                                                                                                                                                                                                                                                                      |
| `DELETE` | `/v1/admin/tenants/{id}`                                        | Delete a tenant                                                                                                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/tenants/{id}/suspend`                                | Suspend a tenant. Body: `{"reason": "<string>"}`. All active sessions in the tenant are drained; new session creation and message injection are rejected with `TENANT_SUSPENDED` until the tenant is resumed. The suspension is recorded in the audit trail with operator identity and reason. Requires `platform-admin`. |
| `POST`   | `/v1/admin/tenants/{id}/resume`                                 | Resume a previously suspended tenant. Restores normal tenant operation. Existing sessions remain terminated — suspension is not a pause. Requires `platform-admin`.                                                                                                                                                        |
| `PUT`    | `/v1/admin/tenants/{id}/rbac-config`                            | Set tenant RBAC configuration (requires `If-Match`)                                                                                                                                                                                                                                                                        |
| `GET`    | `/v1/admin/tenants/{id}/rbac-config`                            | Get tenant RBAC configuration                                                                                                                                                                                                                                                                                              |
| `GET`    | `/v1/admin/tenants/{id}/access-report`                          | Cross-environment access matrix                                                                                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/tenants/{id}/rotate-erasure-salt`                    | Rotate the tenant's billing pseudonymization salt. On rotation, the old salt is retained during a one-time re-hash migration job that re-pseudonymizes historical billing records under the new salt; the old salt is deleted only after migration completes. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                    |
| `GET`    | `/v1/admin/tenants/{id}/users`                                  | List users in a tenant with their platform-managed role assignments. Returns `user_id`, `role`, `assignedAt`, `assignedBy`. `tenant-admin` callers are scoped to their own tenant. Requires `platform-admin` or `tenant-admin`. |
| `PUT`    | `/v1/admin/tenants/{id}/users/{user_id}/role`                   | Assign or update the platform-managed role for a user within a tenant (requires `If-Match`). Body: `{"role": "<role-name>"}`. Valid roles: `tenant-admin`, `tenant-viewer`, `billing-viewer`, `user`, or any custom role defined in the tenant RBAC config. The platform-managed assignment takes precedence over OIDC-derived roles (see Authorization and RBAC in [Section 10.2](10_gateway-internals.md#102-authentication)). Requires `platform-admin` or `tenant-admin`. Emits `user.role_assigned` audit event. |
| `DELETE` | `/v1/admin/tenants/{id}/users/{user_id}/role`                   | Remove the platform-managed role assignment for a user within a tenant. After removal, the user's effective role reverts to their OIDC-derived role (if any). Requires `platform-admin` or `tenant-admin`. Emits `user.role_removed` audit event. |
| `POST`   | `/v1/admin/tenants/{id}/roles`                                | Create a custom role. Body: `{"name": "<string>", "permissions": ["<operation>", ...]}`. Permissions must be a subset of `tenant-admin` permissions. Requires `platform-admin` or `tenant-admin`. |
| `GET`    | `/v1/admin/tenants/{id}/roles`                                | List custom roles for a tenant. Returns role name, permissions, `createdAt`, `updatedAt`. Requires `platform-admin` or `tenant-admin`. |
| `GET`    | `/v1/admin/tenants/{id}/roles/{name}`                         | Get a specific custom role. Requires `platform-admin` or `tenant-admin`. |
| `PUT`    | `/v1/admin/tenants/{id}/roles/{name}`                         | Update a custom role (requires `If-Match`). Body: `{"permissions": ["<operation>", ...]}`. Requires `platform-admin` or `tenant-admin`. |
| `DELETE` | `/v1/admin/tenants/{id}/roles/{name}`                         | Delete a custom role. Blocked if any users are assigned this role (`RESOURCE_HAS_DEPENDENTS`). Requires `platform-admin` or `tenant-admin`. |
| `POST`   | `/v1/admin/users/{user_id}/invalidate`                          | Terminate all active sessions for a user and revoke their tokens immediately. Used during incident response to stop an active attacker's sessions. Requires `platform-admin` or `tenant-admin` (scoped to own tenant). Emits `user.invalidated` audit event. See [Section 11.4](11_policy-and-controls.md#114-user-invalidation).                                             |
| `POST`   | `/v1/admin/environments`                                        | Create an environment                                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/environments`                                        | List all environments                                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/environments/{name}`                                 | Get a specific environment                                                                                                                                                                                                                                                                                                 |
| `PUT`    | `/v1/admin/environments/{name}`                                 | Update an environment (requires `If-Match`)                                                                                                                                                                                                                                                                                |
| `DELETE` | `/v1/admin/environments/{name}`                                 | Delete an environment                                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/environments/{name}/usage`                           | Environment billing rollup                                                                                                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/environments/{name}/access-report`                   | Resolved member list with group expansion                                                                                                                                                                                                                                                                                  |
| `GET`    | `/v1/admin/environments/{name}/runtime-exposure`                | Runtimes/connectors in scope                                                                                                                                                                                                                                                                                               |
| `POST`   | `/v1/admin/experiments`                                         | Create an experiment                                                                                                                                                                                                                                                                                                       |
| `GET`    | `/v1/admin/experiments`                                         | List all experiments                                                                                                                                                                                                                                                                                                       |
| `GET`    | `/v1/admin/experiments/{name}`                                  | Get a specific experiment                                                                                                                                                                                                                                                                                                  |
| `PUT`    | `/v1/admin/experiments/{name}`                                  | Update an experiment (requires `If-Match`)                                                                                                                                                                                                                                                                                 |
| `PATCH`  | `/v1/admin/experiments/{name}`                                  | Partial update of an experiment — canonical endpoint for status transitions (`active`, `paused`, `concluded`). Uses JSON Merge Patch. Requires `If-Match`. See [Section 10.7](10_gateway-internals.md#107-experiment-primitives).                                                                                                                                               |
| `DELETE` | `/v1/admin/experiments/{name}`                                  | Delete an experiment                                                                                                                                                                                                                                                                                                       |
| `GET`    | `/v1/admin/experiments/{name}/results`                          | Experiment results by variant. Returns per-variant session counts and eval score aggregates collected via `POST /v1/sessions/{id}/eval`. Requires `platform-admin` or `tenant-admin` role. Optional query parameters (EXP-002): `delegation_depth` (uint32), `inherited` (bool), `exclude_post_conclusion` (bool), `breakdown_by` (`delegation_depth \| inherited \| submitted_after_conclusion`) — see [Section 10.7](10_gateway-internals.md#107-experiment-primitives) "Results API query parameters" for filter semantics and performance trade-offs.                                                                                           |
| `POST`   | `/v1/admin/external-adapters`                                   | Register an external protocol adapter                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/external-adapters`                                   | List all external protocol adapters                                                                                                                                                                                                                                                                                        |
| `GET`    | `/v1/admin/external-adapters/{name}`                            | Get a specific external adapter                                                                                                                                                                                                                                                                                            |
| `PUT`    | `/v1/admin/external-adapters/{name}`                            | Update an external adapter (requires `If-Match`)                                                                                                                                                                                                                                                                           |
| `POST`   | `/v1/admin/external-adapters/{name}/validate`                   | Run the `RegisterAdapterUnderTest` compliance suite against the adapter in a sandboxed environment. Transitions `status` from `pending_validation` to `active` on success, or `validation_failed` (with per-test details) on failure. Required before an adapter receives traffic. See [Section 15.2.1](#1521-restmcp-consistency-contract).                                                                                                                                                                             |
| `DELETE` | `/v1/admin/external-adapters/{name}`                            | Delete an external adapter                                                                                                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/sessions/{id}`                                       | Get session state, metadata, and assigned pod for operator investigation. Returns the same session state model as `GET /v1/sessions/{id}` plus internal pod assignment and pool details. Requires `platform-admin`. See [Section 24.11](24_lenny-ctl-command-reference.md#2411-session-investigation).                                                                                      |
| `POST`   | `/v1/admin/sessions/{id}/force-terminate`                       | Force-terminate a session                                                                                                                                                                                                                                                                                                  |
| `POST`   | `/v1/admin/users/{user_id}/erase`                               | Initiate a GDPR user-level erasure job. Returns a job ID. Requires `platform-admin` or `tenant-admin` role. Runs the DeleteByUser legal-hold preflight ([§12.8](12_storage-architecture.md#128-compliance-interfaces) Step 0) before queuing the job: if any active legal hold is scoped to the target user (sessions, artifacts, audit ranges, or workspace snapshots), the request is rejected with `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` and emits a `gdpr.erasure_blocked_by_hold` audit event. `platform-admin` callers may override the preflight by submitting `{"acknowledgeHoldOverride": true, "justification": "<required text>"}` in the request body — the override is separately audited as `gdpr.legal_hold_overridden` and does not clear the underlying holds. `tenant-admin` callers cannot self-override. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces) Step 0.                                                                                                                                                                                              |
| `GET`    | `/v1/admin/erasure-jobs/{job_id}`                               | Query erasure job status: phase, completion percentage, time elapsed, errors. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/erasure-jobs/{job_id}/retry`                         | Retry a failed erasure job. The job must be in `failed` state. Requires `platform-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                                                                                                                                                |
| `POST`   | `/v1/admin/erasure-jobs/{job_id}/clear-processing-restriction`  | Manually clear the `processing_restricted` flag for a user after a failed erasure job. Body: `{"justification": "<text>"}`. Operator identity and justification are recorded in the audit trail. Requires `platform-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                              |
| `POST`   | `/v1/admin/billing-corrections`                                 | Issue a billing correction event. Requires `platform-admin`. Body: `tenant_id`, `corrects_sequence`, `correction_reason_code` (enum), optional `correction_detail`, replacement values (`tokens_input`, `tokens_output`, `pod_minutes`). Returns the correction event with assigned `sequence_number`. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream). |
| `POST`   | `/v1/admin/bootstrap`                                           | Apply a seed file (idempotent upsert of runtimes, pools, tenants, etc.). Same schema as `bootstrap` Helm values. See [Section 17.6](17_deployment-topology.md#176-packaging-and-installation). Every invocation emits a `platform.bootstrap_applied` audit event (T3) recording: calling service account identity, seed file SHA-256 hash, resource changes summary (resource type, name, action: `created`/`updated`/`skipped`/`error`), and `dryRun: true/false`. The audit event is emitted even when `?dryRun=true` (with the dry-run flag set) so operators have a record of what a bootstrap run would have changed. The bootstrap Job's ServiceAccount is documented in [Section 17.6](17_deployment-topology.md#176-packaging-and-installation) — it uses a minimal-RBAC ServiceAccount scoped to `lenny-system` with no cluster-wide permissions. |
| `POST`   | `/v1/admin/legal-hold`                                          | Set or clear a legal hold on a session or artifact. Body: `{"resourceType": "session"\|"artifact", "resourceId": "<id>", "hold": true\|false, "note": "<string> (required when hold is true)"}`. Requires `platform-admin` or `tenant-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                                             |
| `GET`    | `/v1/admin/legal-holds`                                         | List active legal holds. Query params: `?tenant_id=`, `?resource_type=session\|artifact`, `?resource_id=`. Returns paginated list with fields: `resourceType`, `resourceId`, `setBy`, `setAt`, `note`. `tenant-admin` callers are automatically scoped to their own tenant. Requires `platform-admin` or `tenant-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                  |
| `POST`   | `/v1/admin/tenants/{id}/force-delete`                           | Force-delete a tenant despite active legal holds. Body: `{"acknowledgeHoldOverride": true, "justification": "<required text>"}`. When holds exist and `acknowledgeHoldOverride` is omitted or false, the request is rejected with `409 TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` and a `admin.tenant.deletion_blocked` audit event is emitted. When the override is provided, the Phase 3.5 legal-hold segregation step re-encrypts held evidence under the platform-managed `legal_hold_escrow_kek` and migrates it to the legal-hold escrow bucket with an independent retention policy before Phase 4 / 4a tenant KMS destruction. Operator identity and justification are recorded in the `gdpr.legal_hold_overridden_tenant` audit event (retained under `audit.gdprRetentionDays`) and raise the `LegalHoldOverrideUsedTenant` warning alert. `tenant-admin` callers cannot self-override — requires `platform-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces) tenant deletion lifecycle Phase 3.5.                                                                                                                   |
| `DELETE` | `/v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial` | Clear the extension-denied flag on a session subtree immediately, bypassing the rejection cool-off window. `rootSessionId` is the `root_session_id` of the delegation tree (the `session_id` of the root session that originated the tree). `sessionId` is the `session_id` of the denied subtree to clear. Requires `platform-admin` or `tenant-admin`. See [Section 8.6](08_recursive-delegation.md#86-lease-extension).                                                                                                                                                   |
| `POST`   | `/v1/admin/pools/{name}/upgrade/start`                          | Begin rolling image upgrade for a pool. Body: `{"newImage": "<digest>"}`. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy) (`RuntimeUpgrade` state machine).                                                                                                                                                                                               |
| `POST`   | `/v1/admin/pools/{name}/upgrade/proceed`                        | Advance to next upgrade phase. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/pools/{name}/upgrade/pause`                          | Pause upgrade state machine. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                                                                                              |
| `POST`   | `/v1/admin/pools/{name}/upgrade/resume`                         | Resume paused upgrade. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                                                                                                    |
| `POST`   | `/v1/admin/pools/{name}/upgrade/rollback`                       | Rollback in-progress upgrade. Body: optional `{"restoreOldPool": true}` for late-stage rollback. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                          |
| `GET`    | `/v1/admin/pools/{name}/upgrade-status`                         | Show upgrade state and progress. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                                                                                          |
| `DELETE` | `/v1/admin/pools/{name}/bootstrap-override`                     | Remove the bootstrap `minWarm` override and switch to formula-driven scaling. See [Section 17.8.2](17_deployment-topology.md#1782-capacity-tier-reference).                                                                                                                                                                                                                           |
| `POST`   | `/v1/admin/credential-pools/{name}/credentials`                 | Add a credential to a pool. See [Section 24.5](24_lenny-ctl-command-reference.md#245-credential-management).                                                                                                                                                                                                                                                                               |
| `PUT`    | `/v1/admin/credential-pools/{name}/credentials/{credId}`        | Update a credential in the pool (body may change `secretRef`). Requires `If-Match`. When `secretRef` changes, the handler performs the admin-time RBAC live-probe ([Section 4.9](04_system-components.md#49-credential-leasing-service)); fails with `422 CREDENTIAL_SECRET_RBAC_MISSING` if the Token Service ServiceAccount lacks `get` on the new Secret. See [Section 24.5](24_lenny-ctl-command-reference.md#245-credential-management). |
| `DELETE` | `/v1/admin/credential-pools/{name}/credentials/{credId}`        | Remove a credential from a pool. Active leases backed by the credential are rotated via the standard fallback path ([Section 4.9](04_system-components.md#49-credential-leasing-service) Fallback Flow). See [Section 24.5](24_lenny-ctl-command-reference.md#245-credential-management). |
| `POST`   | `/v1/admin/quota/reconcile`                                     | Re-aggregate in-flight session usage from Postgres into Redis after Redis recovery. See [Section 24.6](24_lenny-ctl-command-reference.md#246-quota-operations).                                                                                                                                                                                                                       |
| `POST`   | `/v1/oauth/token`                                               | [RFC 6749](https://www.rfc-editor.org/rfc/rfc6749) token endpoint augmented with [RFC 8693 token exchange](https://www.rfc-editor.org/rfc/rfc8693) (`grant_type=urn:ietf:params:oauth:grant-type:token-exchange`). Canonical endpoint for: admin token rotation (`subject_token=<current>`, `requested_token_type=<same>`), delegation child-token minting (internal; `actor_token=<parent_session_token>`, narrowed `scope`), credential-lease token issuance, and operability-scope narrowing. See [Section 13](13_security-model.md#13-security-model) for the claim-mapping table and [Section 24.9](24_lenny-ctl-command-reference.md#249-user-and-token-management) for the CLI mapping. |
| `POST`   | `/v1/admin/billing-corrections/{id}/approve`                    | Approve a pending billing correction. Requires `platform-admin`; submitter cannot approve their own request (self-approval rejected). See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                                                                                                                                   |
| `POST`   | `/v1/admin/billing-corrections/{id}/reject`                     | Reject a pending billing correction. Requires `platform-admin`; submitter cannot reject their own request (self-rejection rejected). The correction remains in `billing_correction_pending` state with `rejected` outcome for audit purposes and is never promoted to the billing stream. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                |
| `POST`   | `/v1/admin/billing-correction-reasons`                          | Add a deployer-defined `correction_reason_code` to the closed enum. Body: `{"code": "<string>", "description": "<string>"}`. Requires `platform-admin`. Audit-logged. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                                                                                                  |
| `GET`    | `/v1/admin/billing-correction-reasons`                          | List all `correction_reason_code` values (built-in and deployer-added). Requires `platform-admin`. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                                                                                                                                                                     |
| `DELETE` | `/v1/admin/billing-correction-reasons/{code}`                   | Remove a deployer-added `correction_reason_code`. Built-in codes cannot be deleted. Requires `platform-admin`. Audit-logged. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                                                                                                                                            |
| `POST`   | `/v1/admin/preflight`                                           | Run preflight checks (Postgres, Redis, MinIO connectivity and schema version). POST because the endpoint performs active outbound connectivity probes — it is not idempotent or side-effect-free. See [Section 17.6](17_deployment-topology.md#176-packaging-and-installation).                                                                                                         |
| `GET`    | `/v1/admin/schema/migrations/status`                            | Return the current expand-contract migration phase for each active migration: `version`, `phase` (`phase1_applied` \| `phase2_deployed` \| `phase3_applied` \| `complete`), `appliedAt`, `gateCheckResult` (for Phase 3: `pass`, `fail:<N>_rows`, or `not_run`), and `migrationJobName` (the Kubernetes Job that applied it). See [Section 24.13](24_lenny-ctl-command-reference.md#2413-migration-management). Requires `platform-admin`. |
| `POST`   | `/v1/admin/schema/migrations/{version}/down`                    | Launch the down-migration Job for version `<version>` as a last-resort recovery when a forward-fix is infeasible. Request body `{"confirm": true}` is required; the endpoint rejects the call with `422 CONFIRMATION_REQUIRED` otherwise. Releases stale advisory locks, applies the `down.sql` file, and clears the `dirty` flag on success. Audited as `platform.schema_migration_rolled_back`. See [Section 17.7](17_deployment-topology.md#177-operational-runbooks) (Schema migration failure) and [Section 24.13](24_lenny-ctl-command-reference.md#2413-migration-management). Requires `platform-admin`. |

**Additional operational endpoints** are defined in [Section 24](24_lenny-ctl-command-reference.md) (`lenny-ctl` command reference), each with its REST API mapping. The table above includes all endpoints; [Section 24](24_lenny-ctl-command-reference.md) provides CLI wrappers and usage examples.

**Web playground auth endpoints.** The bundled web playground ([Section 27](27_web-playground.md)) adds three cookie-authenticated endpoints that exist only when `playground.enabled=true` and `playground.authMode=oidc`. These endpoints are intentionally **not** exposed as admin-API tools (no `x-lenny-mcp-tool`, no `x-lenny-scope`) — they are browser-only and carry no operational capability. Authentication is the HttpOnly `lenny_playground_session` cookie issued by the gateway; standard `Authorization: Bearer` headers are rejected. The exchange flow, TTLs, refresh, and revocation semantics are specified in [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange).

| Method | Endpoint                     | Description                                                                                                                                                                                                                                                           |
| ------ | ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/playground/auth/login`     | Start the OIDC authorization-code flow. Sets a signed `lenny_playground_oidc_state` cookie (TTL 10 min) carrying the PKCE verifier and CSRF `state`, then 302-redirects to the configured OIDC provider. No request body. See [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange). |
| `GET`  | `/playground/auth/callback`  | OIDC provider redirect target. Validates `state`, performs the PKCE-protected token exchange, validates the ID token, creates a server-side playground session record, sets `lenny_playground_session=<opaque-id>; Path=/playground/; HttpOnly; Secure; SameSite=Strict; Max-Age=<oidcSessionTtlSeconds>`, and 302-redirects to the playground index. See [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange). |
| `POST` | `/playground/auth/logout`    | Cookie-auth only. Invalidates the server-side playground session record, revokes all MCP bearer tokens minted for it (§10.3 JWT deny-list propagation), and clears the `lenny_playground_session` cookie. Emits `playground.bearer_revoked` audit event. Returns `204 No Content`. See [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange). |
| `POST` | `/v1/playground/token`       | The single playground bearer-mint endpoint; **mode-polymorphic** across all three `playground.authMode` values (`oidc`, `apiKey`, `dev`). Admission material per mode: **`oidc`** accepts only the `lenny_playground_session` cookie (HttpOnly, `Path=/playground/`) and rejects `Authorization: Bearer` with `400 LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL` to prevent bearer-to-bearer reissuance; **`apiKey`** accepts only `Authorization: Bearer <token>` carrying an OIDC ID token or previously-minted `user_bearer` gateway JWT (same credential as the standard Client→Gateway / Automated-clients auth paths in [§10.2](10_gateway-internals.md#102-authentication)) and rejects any cookie with the same error; **`dev`** requires no admission material (the `global.devMode=true` gate is enforced at Helm-validate per [§27.2](27_web-playground.md#272-placement-and-gating)) and ignores any cookie or bearer presented. Cross-mode rejections use `400 LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL` with `details.configuredAuthMode` and `details.presentedMaterial`. Mints a short-lived MCP bearer token from the admitted subject (or synthetic `dev-user` principal in `dev` mode), applying the [§10.2 Playground mint invariants](10_gateway-internals.md#102-authentication) (subject-typ, scope narrowing, tenant preservation, origin, caller-type preservation). Request body: `{}` (reserved for future metadata). Response: `{"bearerToken", "tokenType": "Bearer", "expiresInSeconds", "reusable", "issuedAt"}`. Default TTL 15 min, reusable across concurrent WebSocket connections for the same user. Returns `503 KMS_SIGNING_UNAVAILABLE` if the signer is unavailable; returns `401 UNAUTHORIZED` with `details.reason` in (`playground_session_expired`, `user_invalidated`, `bearer_revoked`) otherwise. See [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange) ("Bearer token exchange" — authoritative per-mode Auth by mode table). |

**Agent operability endpoints.** [Section 25](25_agent-operability.md) adds two endpoint families authoritative for AI-agent-driven operation (full tables and response shapes in Section 25):

- **Gateway-hosted (§25.3):** `/v1/admin/health[/{component}|/summary]`, `/v1/admin/recommendations`, `/v1/admin/platform/version`, `/v1/admin/platform/config`, `/v1/admin/events/buffer`.
- **`lenny-ops`-hosted (§25.4+):** `/v1/admin/me`, `/v1/admin/me/authorized-tools`, `/v1/admin/me/operations`, `/v1/admin/operations`, `/v1/admin/operations/{id}`, `/v1/admin/events`, `/v1/admin/events/stream`, `/v1/admin/event-subscriptions`, `/v1/admin/diagnostics/*`, `/v1/admin/runbooks`, `/v1/admin/audit-events`, `/v1/admin/audit-events/summary`, `/v1/admin/drift`, `/v1/admin/drift/validate`, `/v1/admin/drift/snapshot/refresh`, `/v1/admin/backups`, `/v1/admin/backups/{id}/verify`, `/v1/admin/backups/schedule`, `/v1/admin/backups/policy`, `/v1/admin/restore/preview`, `/v1/admin/restore/safety-check`, `/v1/admin/restore/execute`, `/v1/admin/restore/{id}/status`, `/v1/admin/restore/resume`, `/v1/admin/restore/{id}/confirm-legal-hold-ledger`, `/v1/admin/platform/upgrade/*`, `/v1/admin/platform/version/full`, `/v1/admin/platform/upgrade-check`, `/v1/admin/platform/registry`, `/v1/admin/remediation-locks`, `/v1/admin/remediation-locks/{id}`, `/v1/admin/remediation-locks/{id}/steal`, `/v1/admin/escalations`, `/v1/admin/logs/pods/*`, `/v1/admin/ops/health`, `/v1/openapi.json`, `/v1/openapi.yaml`, `/v1/openapi/{endpoint-id}`, `/mcp/management`.

**OIDC claim extensions for agent callers.** §25.1 introduces two JWT claims on top of the session/user claims documented in [§10.2](10_gateway-internals.md#102-authentication):

- `caller_type` — one of `"human"`, `"service"`, `"agent"`. Labels audit events and metrics so agent activity can be broken out in observability.
- `scope` — the standard OAuth 2.0 / RFC 9068 `scope` claim. Space-separated list of `tools:<domain>:<action>` values (e.g., `"tools:pool:* tools:health:read"`). When present, both the admin-API middleware (before routing to any handler) and the MCP Management Server (before `tools/call` dispatch) enforce the caller's tool invocation against the claim. Absent claim = no additional restriction beyond role. Scopes only restrict; they never elevate above role. See §25.1 Scoped Tokens for full semantics and §25.12 for the scope-forbidden response shape.

**Scope taxonomy.** The `scope` claim values draw from a closed taxonomy so deployers configuring OIDC providers have a single reference to grant against. Format: `tools:<domain>:<action>`.

- **Domains:** `pool`, `health`, `diagnostics`, `recommendations`, `runbooks`, `events`, `audit`, `drift`, `backup`, `restore`, `upgrade`, `locks`, `escalation`, `logs`, `me`, `operations`, `tenant`, `credential_pool`, `credential`, `runtime`, `quota`, `config`.
- **Actions:** `read` (any `_list` / `_get` / `_summary` tool), `write` (any mutating tool), a specific tool action name (e.g., `scale`, `rotate`, `create`, `steal`), or `*` (all actions in the domain).
- **Enforcement:** every admin-API endpoint declares its `x-lenny-scope` (see MCP extension below). Mismatch → `403 SCOPE_FORBIDDEN` with `requiredScope` and `activeScope` in the response (§25.12).

This list is the source-of-truth; new domains must be added here before being introduced in handlers.

**Admin API MCP extension contract.** Every admin-API endpoint with documented RBAC MUST be exposed as an MCP tool on `/mcp/management` (§25.12). The MCP tool inventory is auto-generated from the OpenAPI spec at build time, so every endpoint in the OpenAPI document MUST carry the following extensions:

- `x-lenny-mcp-tool` — canonical MCP tool name (e.g., `"lenny_pool_scale"`). Set to `null` only for endpoints purely used for internal component-to-component communication.
- `x-lenny-scope` — scope identifier in `tools:<domain>:<action>` format (e.g., `"tools:pool:scale"`). Enforced against the caller's `scope` claim at the admin-API middleware and the MCP adapter. `null` only when `x-lenny-mcp-tool` is also `null`.
- `x-lenny-required-role` — `"platform-admin"` or `"tenant-admin"`.
- `x-lenny-category` — `"observation"` | `"coordination"` | `"mutation"` | `"destructive"` | `"lifecycle"`.
- `x-lenny-idempotency-key` — `"required"` | `"recommended"` | `"ignored"`.
- `x-lenny-dry-run-support` — `"confirm-bool"` | `"none"`.
- `x-lenny-guards` — array of conditional-requirement rules for parameters (§25.12 Tool Schema Details).

**CI contract.** A build-time check fails the build if any admin-API endpoint lacks `x-lenny-mcp-tool` (including `null`), `x-lenny-scope`, `x-lenny-required-role`, or `x-lenny-category`. An additional check asserts that every `x-lenny-scope` value conforms to `tools:<domain>:<action>` syntax and its domain is in the taxonomy above.

**Optional correlation headers.** Every admin-API request accepts two optional headers (§25.1):

- `X-Lenny-Operation-ID` — caller-generated UUID. Propagated to audit events, operational events, and structured logs so a single orchestrated task can be traced across every subsystem it touches.
- `X-Lenny-Agent-Name` — human-readable agent instance identifier. Propagated to metrics labels and audit records.

**Canonical response envelopes.** The admin API adopts four canonical envelopes defined in §25.2:

- **`degradation`** — attached to responses whose data quality depends on external dependency availability (e.g., Prometheus, Redis). Callers inspect this to decide whether to trust the response.
- **`pagination`** — attached to every list endpoint. Fields: `cursor`, `hasMore`, `limit`, `cursorKind`, `gapDetected`.
- **`error`** — the error envelope above, extended with `retryable`, `suggestedRetryAfter`, and a larger shared code catalog that Section 25 adds to per subsection.
- **`progress`** — attached to long-running operation responses. Contains `kind`, `status`, `phase`, `stepsCompleted`, `stepsTotal`, `percent`, `etaSeconds`, `etaConfidence`, `etaMethod` (§25.2 Canonical Progress Envelope).

**OpenAPI generation and discovery.** The admin API exposes its contract as an OpenAPI 3.1 document at `/v1/openapi.json` and `/v1/openapi.yaml`. The document is generated at build time from the Go type definitions; it is the single source of truth for the admin API contract and is consumed by `lenny-ctl`, the MCP Management Server (§25.12), and any external SDK. The build fails if the OpenAPI is out-of-sync with the Go types.

**MCP endpoint boundary.** Lenny exposes two distinct MCP endpoint families and they MUST NOT be confused:

- `/mcp/runtimes/{runtime-name}` — agent-pod tool proxy (already documented in §15 above). Served by the gateway; authentication is a session-scoped token; tool surface is runtime-supplied.
- `/mcp/management` — admin/operability surface (§25.12). Served by `lenny-ops`; authentication is the same OIDC/JWT mechanism as REST admin calls; tool surface is auto-generated from the admin OpenAPI spec.

Both speak the MCP protocol; neither proxies to the other.

**Admin API design constraints:** Error taxonomy, OIDC auth, etag-based concurrency, `dryRun` support, OpenAPI spec, audit logging.

**Error response envelope.** All REST API endpoints (both client-facing and admin) return errors using a canonical JSON envelope:

```json
{
  "error": {
    "code": "QUOTA_EXCEEDED",
    "category": "POLICY",
    "message": "Tenant t1 has exceeded its monthly session quota (limit: 500).",
    "retryable": false,
    "details": {}
  }
}
```

Fields: `code` (string, required) — machine-readable error code from the table below. `category` (string, required) — one of `TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM` as defined in [Section 16.3](16_observability.md#163-distributed-tracing). `message` (string, required) — human-readable description. `retryable` (boolean, required) — whether the client should retry. `details` (object, optional) — additional context; structure varies by error code.

**Error code catalog:**

| Code                        | Category    | HTTP Status | Description                                                                                                                                                                                                                                              |
| --------------------------- | ----------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `VALIDATION_ERROR`          | `PERMANENT` | 400         | Request body or query parameters failed validation                                                                                                                                                                                                       |
| `INVALID_STATE_TRANSITION`  | `PERMANENT` | 409         | Requested operation is not valid for the current resource state                                                                                                                                                                                          |
| `RESOURCE_NOT_FOUND`        | `PERMANENT` | 404         | The requested resource does not exist or is not visible to the caller                                                                                                                                                                                    |
| `RESOURCE_ALREADY_EXISTS`   | `PERMANENT` | 409         | A resource with the given identifier already exists                                                                                                                                                                                                      |
| `RESOURCE_HAS_DEPENDENTS`   | `PERMANENT` | 409         | Resource cannot be deleted because it is referenced by active dependents. `details.dependents` lists blocking references by type, name, count, and (where a stable identifier exists) an `ids` array of up to 20 individual resource IDs. When more than 20 dependents of a given type exist, the array is truncated and `truncated: true` is set on that entry. |
| `ETAG_MISMATCH`             | `PERMANENT` | 412         | The `If-Match` etag does not match the current resource version. `details.currentEtag` contains the current ETag value.                                                                                                                                  |
| `ETAG_REQUIRED`             | `PERMANENT` | 428         | `If-Match` header is required on PUT but was not provided                                                                                                                                                                                                |
| `UNAUTHORIZED`              | `PERMANENT` | 401         | Missing or invalid authentication credentials                                                                                                                                                                                                            |
| `FORBIDDEN`                 | `POLICY`    | 403         | Authenticated but not authorized for this operation                                                                                                                                                                                                      |
| `QUOTA_EXCEEDED`            | `POLICY`    | 429         | Tenant or user quota exceeded                                                                                                                                                                                                                            |
| `RATE_LIMITED`              | `POLICY`    | 429         | Request rate limit exceeded                                                                                                                                                                                                                              |
| `CREDENTIAL_POOL_EXHAUSTED` | `POLICY`    | 503         | No available credentials in the assigned pool                                                                                                                                                                                                            |
| `CREDENTIAL_SECRET_RBAC_MISSING` | `PERMANENT` | 422    | Admin credential-pool write rejected because the Token-Service-owned live-probe returned `DENIED` or `NOT_FOUND` for one or more referenced Secrets — the Token Service's own ServiceAccount lacks `get` permission on (or the API server does not contain) each named Secret. Returned by pool creation (`POST /v1/admin/credential-pools`), credential addition (`POST /v1/admin/credential-pools/{name}/credentials`), and credential update (`PUT /v1/admin/credential-pools/{name}/credentials/{credId}`). `details.resourceNames` is an array naming the missing Secret(s) and `details.rbacPatch` contains the required RBAC patch command (equivalent to `lenny-ctl admin credential-pools add-credential`'s emitted patch) covering the full set. Apply the grant and retry. See [Section 4.9](04_system-components.md#49-credential-leasing-service) (Admin-time RBAC live-probe).                                                                                                |
| `CREDENTIAL_PROBE_UNAVAILABLE` | `TRANSIENT` | 503    | Admin credential-pool write rejected because the Token-Service-owned live-probe could not return a definitive verdict — Token Service unreachable, mTLS handshake failure, or upstream Kubernetes API error prevented evaluation of the requested Secret(s). Returned by the same three admin endpoints as `CREDENTIAL_SECRET_RBAC_MISSING`. The handler does not fail open: no CR change is persisted. Distinct from `CREDENTIAL_SECRET_RBAC_MISSING` so operators can discriminate a denied probe (fix RBAC) from a failed probe (fix Token Service reachability). `details.probeError` describes the underlying failure mode for diagnosis. Retry after resolving Token Service health; see [Section 4.9](04_system-components.md#49-credential-leasing-service) (Admin-time RBAC live-probe).                                                                                                |
| `USER_CREDENTIAL_NOT_FOUND` | `PERMANENT` | 404         | No pre-registered credential found for user and provider. Register a credential via `POST /v1/credentials` or configure pool fallback.                                                                                                                   |
| `RUNTIME_UNAVAILABLE`       | `TRANSIENT` | 503         | No healthy pods available for the requested runtime                                                                                                                                                                                                      |
| `POD_CRASH`                 | `TRANSIENT` | 502         | The session pod terminated unexpectedly                                                                                                                                                                                                                  |
| `TIMEOUT`                   | `TRANSIENT` | 504         | Operation timed out                                                                                                                                                                                                                                      |
| `UPSTREAM_ERROR`            | `UPSTREAM`  | 502         | An external dependency (MCP tool, auth provider) returned an error                                                                                                                                                                                       |
| `TARGET_TERMINAL`           | `PERMANENT` | 409         | Target task or session is in a terminal state                                                                                                                                                                                                            |
| `INJECTION_REJECTED`        | `POLICY`    | 403         | Message injection rejected (runtime has `injection.supported: false`)                                                                                                                                                                                    |
| `SCOPE_DENIED`              | `POLICY`    | 403         | Inter-session message rejected because the sender's effective `messagingScope` does not permit messaging the target session. Returned as the `error` reason in a `delivery_receipt` event. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model).                                                                                                                         |
| `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` | `POLICY` | 403 | `lenny/delegate_task` rejected because the child's resolved effective `messagingScope` is `siblings` but the child lease's resolved effective `treeVisibility` is not `full` (`self-only` or `parent-and-self`). `messagingScope` is not a per-delegation input — the gateway resolves it from the deployment/tenant/runtime hierarchy per [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) and compares it against the lease's `treeVisibility`. Sibling messaging requires `treeVisibility: full` so that children can discover one another via `lenny/get_task_tree`. `details.effectiveMessagingScope`, `details.effectiveTreeVisibility`, and `details.requiredTreeVisibility` (`"full"`) are included. Not retryable as-is — the caller must either upgrade `treeVisibility` to `full` or narrow the effective `messagingScope` (via deployment/tenant/runtime configuration) to `direct` or tighter. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) (`treeVisibility` vs. `messagingScope` delegation-time compatibility check) and [Section 8.5](08_recursive-delegation.md#85-delegation-tools). |
| `MCP_VERSION_UNSUPPORTED`   | `PERMANENT` | 400         | Client MCP version is not supported                                                                                                                                                                                                                      |
| `MCP_PROTOCOL_VERSION_RETIRED` | `PERMANENT` | 400      | New MCP `initialize` handshake rejected because the requested `protocolVersion` has exited its deprecation window and its handler has been removed from the gateway. `details.retiredVersion` carries the rejected version; `details.currentVersions` lists the currently supported versions. Distinct from `MCP_VERSION_UNSUPPORTED` (version never supported) in that the rejected version was supported at some earlier gateway release. Not retryable without upgrading the client to negotiate a currently supported version. Active sessions that negotiated this version before the handler was removed are retained to termination (see [Section 15.2](#152-mcp-api), "Session-lifetime exception for deprecated versions"). |
| `IMAGE_RESOLUTION_FAILED`   | `PERMANENT` | 422         | Container image reference is invalid or could not be resolved. `details.image` contains the unresolvable reference; `details.reason` describes the failure (e.g., `invalid_digest`, `tag_not_found`, `registry_unreachable`).                            |
| `RESERVED_IDENTIFIER`       | `PERMANENT` | 422         | A field value uses a platform-reserved identifier (e.g., variant `id: "control"`). `details.field` identifies the offending field; `details.value` is the reserved value that was rejected.                                                              |
| `CONFIGURATION_CONFLICT`    | `PERMANENT` | 422         | The requested configuration contains mutually incompatible field values. `details.conflicts` is an array of `{"fields": ["fieldA", "fieldB"], "message": "..."}` entries describing each incompatibility.                                                |
| `SEED_CONFLICT`             | `PERMANENT` | 409         | A bootstrap/seed upsert conflicts with an existing resource in a non-idempotent way and `--force-update` was not set. `details.resource` identifies the conflicting resource by type and name; `details.conflictingFields` lists the fields that differ. |
| `INTERCEPTOR_TIMEOUT`       | `TRANSIENT` | 503         | An external interceptor did not respond within its configured timeout. `details.interceptor_ref`, `details.phase`, and `details.timeout_ms` are included. Returned when `failPolicy: fail-closed`; suppressed (request proceeds) when `failPolicy: fail-open`. Distinct from `LLM_REQUEST_REJECTED` (which indicates a deliberate REJECT decision, not a timeout). See [Section 4.8](04_system-components.md#48-gateway-policy-engine). |
| `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` | `POLICY` | 400 | An external interceptor returned `MODIFY` with changes to immutable fields (e.g., `user_id`, `tenant_id`). `details.interceptor_ref`, `details.phase`, and `details.violated_fields` are included. The modification is rejected and the original payload is preserved. See [Section 4.8](04_system-components.md#48-gateway-policy-engine). |
| `INTERCEPTOR_WEAKENING_COOLDOWN` | `TRANSIENT` | 503 | A `delegate_task` or `lenny/send_message` call was rejected because its effective `DelegationPolicy` references an interceptor whose `failPolicy` was recently weakened (`fail-closed → fail-open`) and the `gateway.interceptorWeakeningCooldownSeconds` cooldown window has not yet elapsed. `details.interceptor_ref`, `details.transition_ts`, `details.cooldown_remaining_seconds`, and `details.affected_policy` are included. Callers may retry after `cooldown_remaining_seconds`. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) (Interceptor configuration lifecycle, rule 5). |
| `INTERCEPTOR_COOLDOWN_IMMUTABLE` | `POLICY`    | 400         | `POST /v1/admin/interceptors` or `PUT /v1/admin/interceptors/{name}` rejected because the request body attempted to set `transition_ts` (or any server-minted cooldown state). `transition_ts` is server-minted on every `fail-closed → fail-open` transition and is not admin-writable — this prevents a compromised admin credential from flipping `failPolicy` and backdating the timestamp in the same call to collapse the cooldown window. `details.field` identifies the offending field (e.g., `transition_ts`). Not retryable without removing the offending field. The request is rejected as a whole and no interceptor state is persisted. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) rule 5 (`transition_ts` is server-minted and admin-immutable). |
| `LLM_REQUEST_REJECTED`      | `PERMANENT` | 403         | LLM request rejected by `PreLLMRequest` interceptor. `details.reason` contains the interceptor's rejection reason. Proxy mode only ([Section 4.8](04_system-components.md#48-gateway-policy-engine)).                                                                                                      |
| `LLM_RESPONSE_REJECTED`     | `PERMANENT` | 502         | LLM response rejected by `PostLLMResponse` interceptor. `details.reason` contains the interceptor's rejection reason. Proxy mode only ([Section 4.8](04_system-components.md#48-gateway-policy-engine)).                                                                                                    |
| `CONNECTOR_REQUEST_REJECTED` | `PERMANENT` | 403         | Connector tool call rejected by `PreConnectorRequest` interceptor. `details.reason` contains the interceptor's rejection reason ([Section 4.8](04_system-components.md#48-gateway-policy-engine)).                                                                                                           |
| `CONNECTOR_RESPONSE_REJECTED` | `PERMANENT` | 502         | Connector response rejected by `PostConnectorResponse` interceptor. `details.reason` contains the interceptor's rejection reason ([Section 4.8](04_system-components.md#48-gateway-policy-engine)).                                                                                                          |
| `INTERNAL_ERROR`            | `TRANSIENT` | 500         | Unexpected server error                                                                                                                                                                                                                                  |
| `WARM_POOL_EXHAUSTED`       | `TRANSIENT` | 503         | No idle pods are available in the warm pool after exhausting both the API-server claim path and the Postgres fallback. Client should retry with exponential backoff. See [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle).                                                                  |
| `INVALID_INTERCEPTOR_PRIORITY` | `PERMANENT` | 422      | External interceptor registration specifies `priority ≤ 100`, which is reserved for built-in security-critical interceptors. Set `priority > 100`. See [Section 4.8](04_system-components.md#48-gateway-policy-engine).                                                                                     |
| `INVALID_INTERCEPTOR_PHASE` | `PERMANENT` | 422         | External interceptor registration includes the `PreAuth` phase, which is exclusively reserved for built-in interceptors. Remove `PreAuth` from the phase set. See [Section 4.8](04_system-components.md#48-gateway-policy-engine).                                                                          |
| `CREDENTIAL_PROVIDER_MISMATCH`    | `POLICY`  | 422       | Cross-environment delegation with `credentialPropagation: inherit` rejected because the parent's credential pool providers and the child runtime's `supportedProviders` have no intersection. Use `credentialPropagation: independent` for cross-environment delegations where the runtimes use different providers. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `DEADLOCK_TIMEOUT`          | `TRANSIENT` | 504         | A delegated subtree deadlock was not resolved within `maxDeadlockWaitSeconds`. The deepest blocked tasks have been failed. The root task may retry after breaking the deadlock. See [Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema).                                                         |
| `SESSION_NOT_EVAL_ELIGIBLE` | `PERMANENT` | 422         | Eval submission rejected because the target session is in a terminal state (`cancelled` or `expired`) that is not eligible for eval storage. See [Section 10.7](10_gateway-internals.md#107-experiment-primitives).                                                                                 |
| `EVAL_QUOTA_EXCEEDED`       | `POLICY`    | 429         | The per-session `EvalResult` storage cap has been reached (`maxEvalsPerSession`, default 10,000). `details.sessionId` and `details.limit` are included. See [Section 10.7](10_gateway-internals.md#107-experiment-primitives).                                                                      |
| `STORAGE_QUOTA_EXCEEDED`    | `POLICY`    | 429         | Tenant artifact storage quota would be exceeded by the upload or checkpoint write. `details.currentBytes` and `details.limitBytes` are included. See [Section 11.2](11_policy-and-controls.md#112-budgets-and-quotas).                                                                                      |
| `CREDENTIAL_RENEWAL_FAILED` | `TRANSIENT` | 503         | All credential renewal retries were exhausted before the active lease expired. The session is entering the credential fallback flow. `details.provider` identifies the affected credential provider. See [Section 4.9](04_system-components.md#49-credential-leasing-service).                                    |
| `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` | `PERMANENT` | 422  | A stored `WorkspacePlan` uses a `schemaVersion` higher than this gateway version understands. Workspace materialization is blocked. `details.knownVersion` and `details.encounteredVersion` are included. See [Section 14](14_workspace-plan-schema.md).                               |
| `BUDGET_STATE_UNRECOVERABLE` | `TRANSIENT` | 503        | A delegation tree's budget state could not be reconstructed after Redis recovery (Postgres checkpoint too stale and coordinating gateway replica was also lost). The root session is moved to `awaiting_client_action`. See [Section 11.2](11_policy-and-controls.md#112-budgets-and-quotas).                |
| `PERMISSION_DENIED`         | `POLICY`    | 403         | The authenticated identity lacks the required permission for this specific resource or operation. Distinguished from `FORBIDDEN` (role-level rejection) in that `PERMISSION_DENIED` is policy-evaluated at the resource level (e.g., delegation scope, policy rule). |
| `SCOPE_FORBIDDEN`           | `POLICY`    | 403         | Caller's OAuth 2.0 `scope` claim does not grant the scope required by this endpoint. `details.requiredScope` (e.g., `"tools:pool:scale"`) and `details.activeScope` (the caller's scope claim) are included. Distinguished from `FORBIDDEN` / `PERMISSION_DENIED` in that the caller's role would otherwise permit the call — only the scope claim is restricting. See [Section 25.1](25_agent-operability.md#251-design-philosophy-and-agent-model) Scoped Tokens. |
| `CREDENTIAL_REVOKED`        | `POLICY`    | 403         | The credential backing the active session lease has been explicitly revoked (placed on the deny list). Active sessions using this credential are terminated immediately; no further requests can be made with the revoked credential. See [Section 4.9](04_system-components.md#49-credential-leasing-service).   |
| `INVALID_POOL_CONFIGURATION` | `PERMANENT` | 422        | Pool creation or update rejected due to an invalid configuration constraint (e.g., `cleanupTimeoutSeconds / maxConcurrent < 5`, or `terminationGracePeriodSeconds` too small for the tiered checkpoint cap). `details.message` describes the violated constraint. See [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle). |
| `CIRCUIT_BREAKER_OPEN`      | `POLICY`    | 503         | Session creation or delegation rejected because an operator-declared circuit breaker is active. `details.circuit_name`, `details.reason`, and `details.opened_at` are included. Not `retryable` — the client should wait for the circuit breaker to be closed by an operator before retrying. See [Section 11.6](11_policy-and-controls.md#116-circuit-breakers). |
| `POOL_DRAINING`             | `TRANSIENT` | 503         | Session creation rejected because the target pool is in `draining` state and is no longer accepting new sessions. `Retry-After` header indicates estimated drain completion. `details.pool` and `details.estimatedDrainSeconds` are included. See [Section 15.1](#151-rest-api) (pool drain). |
| `DELEGATION_CYCLE_DETECTED` | `PERMANENT` | 400         | Delegation rejected because the target's resolved `(runtime_name, pool_name)` identity tuple appears in the caller's delegation lineage, which would create a circular wait. `details.cycleRuntimeName` and `details.cyclePoolName` identify the offending identity. Not retryable — the caller must choose a different target. See [Section 8.2](08_recursive-delegation.md#82-delegation-mechanism). |
| `DELEGATION_PARENT_REVOKED` | `POLICY`    | 403         | `lenny/delegate_task` rejected because the parent session's token was rotated or revoked between the call and the internal child-token exchange, so the actor_token's `jti` resolves to a revoked entry in the revocation cache. No child token is issued and no child pod is allocated. Also returned to any concurrent `lenny/delegate_task` calls pending on a parent that is the subject of a recursive revocation. `details.parentSessionId` and `details.revocationReason` (e.g., `token_rotated`, `recursive_revocation`) are included. Not retryable — the caller must re-authenticate or the parent session has been terminated. Aligned with the canonical §15.4 pattern for credential-identity revocation rejections (matches `CREDENTIAL_REVOKED`, `LEASE_SPIFFE_MISMATCH`, `INJECTION_REJECTED`); HTTP 403 is reserved for credential-identity and role/scope-based authz denials, distinct from HTTP 409 which is reserved for resource-state conflicts (`INVALID_STATE_TRANSITION`, `TARGET_TERMINAL`, `REPLAY_ON_LIVE_SESSION`). See [Section 8.2](08_recursive-delegation.md#82-delegation-mechanism) (actor-token freshness) and [Section 13.3](13_security-model.md#133-credential-flow) (token rotation and revocation). |
| `DELEGATION_AUDIT_CONTENTION` | `TRANSIENT` | 503         | `lenny/delegate_task` rejected because the per-tenant audit advisory lock could not be acquired within `audit.lock.acquireTimeoutMs` during the child-token exchange's audit write, after the gateway exhausted `audit.lock.maxRetries` internal retries. The exchange fails closed: no child token is issued and no child pod is allocated. Retryable with backoff; the `Retry-After` header indicates the recommended wait. The parent agent MUST retry the entire `lenny/delegate_task` call (not just the token exchange step) so that policy evaluation, cycle detection, interceptors, and the actor-token freshness check all re-run on retry. `details.tenantId` and `details.retryAfterSeconds` are included. See [Section 8.2](08_recursive-delegation.md#82-delegation-mechanism) (retry semantics on audit contention) and [Section 11.7](11_policy-and-controls.md#117-audit-logging). |
| `OUTPUTPART_TOO_LARGE`      | `PERMANENT` | 413         | An `OutputPart` payload exceeds the per-part size limit (50 MB). The part was rejected at ingress. `details.partIndex`, `details.sizeBytes`, and `details.limitBytes` are included. See [Section 15.4.1](#1541-adapterbinary-protocol). |
| `REQUEST_INPUT_TIMEOUT`     | `TRANSIENT` | 504         | A `lenny/request_input` call blocked longer than `maxRequestInputWaitSeconds` without receiving a response. Delivered as a tool-call error to the blocking runtime. `details.requestId` and `details.timeoutSeconds` are included. See [Section 11.3](11_policy-and-controls.md#113-timeouts-and-cancellation). |
| `ERASURE_IN_PROGRESS`       | `POLICY`    | 403         | Session creation rejected because the target `user_id` has a pending GDPR erasure job and `processing_restricted: true` is set. `details.userId` and `details.jobId` are included. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). |
| `ERASURE_BLOCKED_BY_LEGAL_HOLD` | `POLICY` | 409         | `POST /v1/admin/users/{user_id}/erase` rejected by the DeleteByUser legal-hold preflight ([§12.8](12_storage-architecture.md#128-compliance-interfaces) Step 0). One or more active legal holds scoped to the target user (sessions, artifacts, audit ranges, or workspace snapshots) would be destroyed by the erasure, which would constitute spoliation of evidence subject to preservation orders. `details.userId`, `details.holdCount`, and `details.holds` (array of `{resourceType, resourceId, holdSetAt, holdSetBy, note}`) are included. Not retryable as-is — the caller must either release the blocking holds via `POST /v1/admin/legal-hold` (with `hold: false`) or re-invoke erasure with `{"acknowledgeHoldOverride": true, "justification": "<text>"}` (requires `platform-admin`; audited as `gdpr.legal_hold_overridden`). Emits `gdpr.erasure_blocked_by_hold` audit event. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces) Step 0. |
| `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` | `POLICY` | 409   | Tenant deletion rejected by the Phase 3.5 legal-hold segregation gate ([§12.8](12_storage-architecture.md#128-compliance-interfaces) tenant deletion lifecycle, **Legal hold interaction during deletion**). Returned when tenant-delete (including `POST /v1/admin/tenants/{id}/force-delete` **without** `acknowledgeHoldOverride: true`) would advance to Phase 4 while one or more active legal holds are scoped to the tenant's sessions, artifacts, audit ranges, or workspace snapshots — Phase 4's `DeleteByTenant` and Phase 4a's tenant KMS destruction would render that evidence cryptographically unrecoverable, constituting spoliation. `details.tenantId`, `details.holdCount`, and `details.holds` (array of `{resourceType, resourceId, holdSetAt, holdSetBy, note}`) are included. Not retryable as-is — the caller must either release the blocking holds via `POST /v1/admin/legal-hold` (with `hold: false`) and re-enter the deletion lifecycle, or re-invoke force-delete with `{"acknowledgeHoldOverride": true, "justification": "<text>"}` (requires `platform-admin`; audited as `gdpr.legal_hold_overridden_tenant`, raises the `LegalHoldOverrideUsedTenant` warning alert, triggers the Phase 3.5 re-encryption of held evidence under the platform-managed `legal_hold_escrow_kek` and migration to the legal-hold escrow bucket). Emits `admin.tenant.deletion_blocked` audit event. This is the tenant-scope analog of `ERASURE_BLOCKED_BY_LEGAL_HOLD`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces) tenant deletion lifecycle Phase 3.5. |
| `URL_MODE_ELICITATION_DOMAIN_REQUIRED` | `PERMANENT` | 400 | Pool registration or update rejected because `urlModeElicitation.enabled: true` was set without a non-empty `domainAllowlist`. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) (elicitation). |
| `DUPLICATE_MESSAGE_ID`      | `PERMANENT` | 400         | A sender-supplied message `id` is not globally unique within the tenant — a message with the same ID was received within the deduplication window. `details.duplicateId` is included. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |
| `UNREGISTERED_PART_TYPE`    | `WARNING`   | —           | An `OutputPart` carries an unprefixed `type` not present in the current platform-defined registry. The part is passed through with a custom-type-to-`text` fallback and an `unregistered_platform_type` warning annotation. Third-party types should use the `x-<vendor>/` namespace prefix. `details.type` is included. See [Section 15.4.1](#1541-adapterbinary-protocol). |
| `REPLAY_ON_LIVE_SESSION`    | `PERMANENT` | 409         | `POST /v1/sessions/{id}/replay` rejected because the source session is not in a terminal state. The source session must be `completed`, `failed`, `cancelled`, or `expired`. See [Section 15.1](#151-rest-api) (session replay). |
| `INCOMPATIBLE_RUNTIME`      | `PERMANENT` | 400         | `POST /v1/sessions/{id}/replay` rejected because `targetRuntime` has a different `executionMode` than the source session. Replay requires matching execution mode. `details.sourceExecutionMode` and `details.targetExecutionMode` are included. See [Section 15.1](#151-rest-api) (session replay). |
| `DOMAIN_NOT_ALLOWLISTED`    | `POLICY`    | 403         | An agent-initiated URL-mode elicitation was dropped because the URL's effective host does not match any entry in the pool's `urlModeElicitation.domainAllowlist`. `details.host` and `details.allowlist` are included. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) (elicitation). |
| `COMPLIANCE_PGAUDIT_REQUIRED` | `PERMANENT` | 422       | Tenant creation or update rejected because the tenant's `complianceProfile` requires `audit.pgaudit.enabled: true` with a configured `sinkEndpoint`, but these are not set. Configure pgaudit before creating a regulated tenant. See [Section 11.7](11_policy-and-controls.md#117-audit-logging). |
| `DERIVE_ON_LIVE_SESSION`      | `PERMANENT` | 409         | `POST /v1/sessions/{id}/derive` rejected because the source session is not in a terminal state and `allowStale: true` was not set in the request body. See [Section 15.1](#151-rest-api) (derive semantics). |
| `DERIVE_LOCK_CONTENTION`      | `POLICY`    | 429         | `POST /v1/sessions/{id}/derive` rejected because too many concurrent derive operations are in progress for this session. Retry with exponential backoff. See [Section 15.1](#151-rest-api) (derive semantics). |
| `DERIVE_SNAPSHOT_UNAVAILABLE` | `TRANSIENT` | 503         | `POST /v1/sessions/{id}/derive` failed because the referenced workspace snapshot object was not found in object storage (e.g., deleted by a GC bug or premature TTL expiry). Retrying immediately is unlikely to help; the caller should wait and retry or derive from a different source state. `details.snapshotRef` includes the missing object path. See [Section 15.1](#151-rest-api) (derive semantics). |
| `ISOLATION_MONOTONICITY_VIOLATED` | `POLICY` | 422         | Request rejected because the target pool's `sessionIsolationLevel.isolationProfile` is weaker than the source (parent or derive/replay source) session's (`standard` < `sandboxed` < `microvm`). Applies uniformly to `delegate_task` (calling session's `minIsolationProfile`), `POST /v1/sessions/{id}/derive`, and `POST /v1/sessions/{id}/replay` with `replayMode: workspace_derive`. `details.sourceIsolationProfile`, `details.targetIsolationProfile`, and `details.targetPool` are included (delegation responses may use the legacy `details.parentIsolation` / `details.targetIsolation` keys as aliases). On derive/replay only, overridable by `platform-admin` callers via `allowIsolationDowngrade: true` in the request body — this path emits a `derive.isolation_downgrade` audit event. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) delegation monotonicity, [Section 7.1](07_session-lifecycle.md#71-normal-flow) derive semantics, and [Section 15.1](#151-rest-api) replay semantics. |
| `VARIANT_ISOLATION_UNAVAILABLE` | `POLICY` | 422         | Session creation rejected because the `ExperimentRouter`'s isolation monotonicity check ([Section 10.7](10_gateway-internals.md#107-experiment-primitives)) found the active experiment's variant pool isolation profile weaker than the session's `minIsolationProfile`, and the router fails closed rather than silently falling through to the control bucket (which would contaminate control-group eval aggregates with a non-randomly-sampled subset). `details.experimentId`, `details.variantId`, `details.sessionMinIsolation`, and `details.variantPoolIsolation` are included. The caller must either relax the session's `minIsolationProfile`, or the operator must re-provision the variant pool at a compatible isolation profile. Not retryable as-is. An `experiment.isolation_mismatch` warning event is emitted alongside the rejection. |
| `REGION_CONSTRAINT_VIOLATED`  | `POLICY`    | 403         | Request rejected because the resolved storage region does not satisfy the session's `dataResidencyRegion` constraint. `details.requiredRegion` and `details.resolvedRegion` are included. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). |
| `REGION_CONSTRAINT_UNRESOLVABLE` | `PERMANENT` | 422      | Session creation rejected because no storage or pool configuration can satisfy the requested `dataResidencyRegion`. `details.region` identifies the unresolvable constraint. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). |
| `REGION_UNAVAILABLE`          | `TRANSIENT` | 503         | The storage region required by the session's data residency constraint is temporarily unavailable. Retry when the region recovers. `details.region` identifies the affected region. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). |
| `KMS_REGION_UNRESOLVABLE`     | `PERMANENT` | 422         | Session or credential operation rejected because no KMS key is configured for the required region. `details.region` and `details.provider` are included. See [Section 4.9](04_system-components.md#49-credential-leasing-service). |
| `LEASE_SPIFFE_MISMATCH`       | `POLICY`    | 403         | A pod presented a SPIFFE identity that does not match the credential lease's expected identity (`details.expectedSpiffeId`, `details.actualSpiffeId`). The lease is invalidated. See [Section 4.9](04_system-components.md#49-credential-leasing-service). |
| `ENV_VAR_BLOCKLISTED`         | `PERMANENT` | 400         | Session creation or runtime registration rejected because one or more requested environment variables are on the platform blocklist. `details.blocklisted` lists the offending variable names. See [Section 14](14_workspace-plan-schema.md). |
| `GIT_CLONE_AUTH_UNSUPPORTED_HOST` | `POLICY`    | 422     | Session creation rejected because a `gitClone` source has `auth` set but the URL's host does not match any VCS credential pool's `hostPatterns` in the tenant. `details.host` and `details.sourceIndex` are included. The caller must register a VCS credential pool whose `hostPatterns` covers the host, or remove `auth` for public repos. See [Section 14](14_workspace-plan-schema.md) and [Section 4.9](04_system-components.md#49-credential-leasing-service). |
| `GIT_CLONE_AUTH_HOST_AMBIGUOUS` | `POLICY`    | 422       | Session creation rejected because a `gitClone` URL's host matches multiple VCS credential pools' `hostPatterns` in the tenant. `details.host`, `details.sourceIndex`, and `details.matchingPools` are included. Operators must tighten `hostPatterns` so exactly one pool matches any given host. See [Section 4.9](04_system-components.md#49-credential-leasing-service). |
| `INPUT_TOO_LARGE`             | `PERMANENT` | 413         | Delegation rejected because `TaskSpec.input` exceeds `contentPolicy.maxInputSize`. `details.sizeBytes` and `details.limitBytes` are included. Not retryable — the caller must reduce input size. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `CONTENT_POLICY_WEAKENING`    | `POLICY`    | 422         | Delegation rejected because the child lease sets `contentPolicy.interceptorRef: null` when the parent had a non-null reference. Removing a content check is always a weakening and is not permitted. Aligned with the canonical §15.4 pattern for delegation-admission POLICY rejections of well-formed requests (matches `ISOLATION_MONOTONICITY_VIOLATED`, `CREDENTIAL_PROVIDER_MISMATCH`, `VARIANT_ISOLATION_UNAVAILABLE`); HTTP 422 is reserved for unprocessable semantic rejections, distinct from HTTP 403 which is reserved for role/scope-based authz denials (`FORBIDDEN`, `PERMISSION_DENIED`, `SCOPE_FORBIDDEN`). See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` | `POLICY` | 422  | Delegation rejected because the child lease names a different `contentPolicy.interceptorRef` than the parent without retaining the parent's reference. A child lease may not substitute the parent's named interceptor with an unrelated one. Aligned with the canonical §15.4 pattern for delegation-admission POLICY rejections of well-formed requests (matches `ISOLATION_MONOTONICITY_VIOLATED`, `CREDENTIAL_PROVIDER_MISMATCH`, `VARIANT_ISOLATION_UNAVAILABLE`). See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `DELEGATION_POLICY_WEAKENING` | `POLICY`    | 422         | Delegation rejected because the child lease's `maxDelegationPolicy` would expand the effective delegation authority beyond the parent's effective `maxDelegationPolicy`. A child's `maxDelegationPolicy` must be at least as restrictive as the parent's. `details.parentPolicy` (the parent's effective `maxDelegationPolicy`, or `null` if uncapped) and `details.childPolicy` (the child's requested value) are included. Aligned with the canonical §15.4 pattern for delegation-admission POLICY rejections of well-formed requests (matches `ISOLATION_MONOTONICITY_VIOLATED`, `CREDENTIAL_PROVIDER_MISMATCH`, `VARIANT_ISOLATION_UNAVAILABLE`). See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `TREE_VISIBILITY_WEAKENING`   | `POLICY`    | 422         | Delegation rejected because the child lease's `treeVisibility` would widen the visibility boundary beyond the parent's effective `treeVisibility`. The ordering `full → parent-and-self → self-only` is strict: a child may narrow visibility but never widen it. `details.parentTreeVisibility` and `details.childTreeVisibility` identify both sides of the mismatch. Aligned with the canonical §15.4 pattern for delegation-admission POLICY rejections of well-formed requests (matches `CONTENT_POLICY_WEAKENING`, `DELEGATION_POLICY_WEAKENING`, `ISOLATION_MONOTONICITY_VIOLATED`). See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) (`treeVisibility` inheritance semantics). |
| `COMPLIANCE_SIEM_REQUIRED`    | `POLICY`    | 422         | Tenant creation or update rejected because the tenant's `complianceProfile` requires a SIEM endpoint (`audit.siem.endpoint`) to be configured, but it is not set. See [Section 11.7](11_policy-and-controls.md#117-audit-logging). |
| `CLASSIFICATION_CONTROL_VIOLATION` | `POLICY` | 422         | Storage-tier classification control rejection. Emitted when a T4 tenant's online KMS availability probe fails on `PUT /v1/admin/tenants/{id}` (tenant-scoped KMS key unreachable), when a T4 artifact write cannot proceed because the `tenant:{tenant_id}` KMS key is unavailable at write time, or when a write's effective tier does not match the configured store (e.g., T4 data directed to a store without envelope encryption). `details.tenantId`, `details.tier`, and `details.reason` (e.g., `kms_probe_failed`, `kms_unavailable`, `tier_store_mismatch`) are included. Not retryable at the API layer — the operator must restore KMS key availability or correct the storage-tier configuration before retrying. See [Section 12.5](12_storage-architecture.md#125-artifact-store) and [Section 12.9](12_storage-architecture.md#129-data-classification). |
| `BUDGET_EXHAUSTED`            | `POLICY`    | 429         | Delegation or lease extension rejected because the remaining token budget or tree-size budget is insufficient. `details.limitType` is `token_budget`, `tree_size`, or `tree_memory` (distinguishing the exhausted resource). `TOKEN_BUDGET_EXHAUSTED` and `TREE_SIZE_EXCEEDED` are internal Lua script result codes; the wire error code is always `BUDGET_EXHAUSTED`. Not retryable without a budget extension. See Sections 8.3, 8.6. |
| `EXTENSION_COOL_OFF_ACTIVE`   | `POLICY`    | 403         | Lease-extension request auto-rejected because the requesting subtree is in a rejection cool-off window following a user-denied extension elicitation. `details.subtreeId` identifies the denied subtree and `details.coolOffExpiresAt` is the UTC timestamp at which the cool-off window ends. Not retryable until cool-off expires; operators may clear the denial early via `DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial`. See [Section 8.6](08_recursive-delegation.md#86-lease-extension). |
| `OUTPUTPART_INLINE_REF_CONFLICT` | `PERMANENT` | 400      | An `OutputPart` has both `inline` and `ref` fields set, which are mutually exclusive. Set exactly one field: `inline` for direct byte embedding or `ref` for external blob storage reference. See [Section 15.4.1](#1541-adapterbinary-protocol). |
| `INVALID_DELIVERY_VALUE`      | `PERMANENT` | 400         | A message delivery envelope contains an unrecognized `delivery` field value. Valid values are `queued` and `immediate`. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |
| `SDK_DEMOTION_NOT_SUPPORTED`  | `PERMANENT` | 422         | Session creation failed because the pool uses SDK-warm mode (`preConnect: true`) and the adapter does not implement the `DemoteSDK` RPC. The workspace includes files from `sdkWarmBlockingPaths` that require demotion, but demotion is unavailable. Runtime authors must implement `DemoteSDK` before declaring `preConnect: true`. See [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like). |
| `ELICITATION_NOT_FOUND`       | `PERMANENT` | 404         | `respond_to_elicitation` or `dismiss_elicitation` rejected because the `(session_id, user_id, elicitation_id)` triple does not match any pending elicitation. The ID is unknown, belongs to a different session, or belongs to a different user. 404 is returned in all mismatch cases to avoid leaking the existence of elicitations in other sessions. See [Section 9.2](09_mcp-integration.md#92-elicitation-chain). |
| `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` | `POLICY` | 400  | Pool registration or update rejected because `cacheScope: tenant` was set on a pool whose `complianceProfile` is a regulated value (`hipaa`, `fedramp`). Cross-user cache sharing is prohibited under these compliance profiles. Use `cacheScope: per-user` (default). See [Section 4.9](04_system-components.md#49-credential-leasing-service). |
| `IDEMPOTENCY_KEY_REUSED`    | `PERMANENT` | 422         | An idempotency key was reused with a different request body. Each idempotency key must correspond to a single unique request. See [Section 11.5](11_policy-and-controls.md#115-idempotency). |
| `UPLOAD_TOKEN_EXPIRED`        | `PERMANENT` | 401         | The upload token's TTL has elapsed (`session_creation_time + maxCreatedStateTimeoutSeconds`). The client must create a new session. See [Section 7.1](07_session-lifecycle.md#71-normal-flow). |
| `UPLOAD_TOKEN_MISMATCH`       | `PERMANENT` | 403         | The upload token's embedded `session_id` does not match the target session. Tokens are session-scoped and cannot be reused across sessions. See [Section 7.1](07_session-lifecycle.md#71-normal-flow). |
| `UPLOAD_TOKEN_CONSUMED`       | `PERMANENT` | 410         | The upload token has already been invalidated by a successful `FinalizeWorkspace` call. Replay of a consumed token is not permitted. See [Section 7.1](07_session-lifecycle.md#71-normal-flow). |
| `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` | `PERMANENT` | 413         | Archive extraction aborted because the archive violates a platform-normative validator defined in [Section 13.4](13_security-model.md#134-upload-security) / [Section 7.4](07_session-lifecycle.md#74-upload-safety). `details.reason` carries the specific sub-code: `max_decompressed_size`, `max_decompression_ratio`, `max_entry_count`, `max_entry_size`, `max_path_depth`, `max_path_length`, `path_escapes_root`, `non_regular_entry`, or `symlink`. Not retryable — the client must supply a conformant archive. Applies to client uploads and to delegation file exports ([Section 8.7](08_recursive-delegation.md#87-file-export-model)). |
| `TARGET_NOT_READY`            | `TRANSIENT` | 409         | Inter-session message rejected because the target session is in a pre-running state (`created`, `ready`, `starting`, `finalizing`) and has no inbox. Retry after the session transitions to `running`. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |
| `CROSS_TENANT_MESSAGE_DENIED` | `POLICY`    | 403         | Inter-session message rejected because the sender and target sessions belong to different tenants. Cross-tenant messaging is unconditionally prohibited. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |
| `TENANT_SUSPENDED`            | `POLICY`    | 403         | Tenant is suspended. New session creation and message injection are rejected. The suspension is recorded in the audit trail. Wait for tenant resumption or contact administrators. See [Section 15.1](#151-rest-api) (`POST /v1/admin/tenants/{id}/suspend`). |
| `INVALID_CALLBACK_URL`        | `PERMANENT` | 400         | A caller-supplied push-notification callback URL failed SSRF validation at registration time. Applies to the A2A adapter's `pushNotification.url` field on `POST /a2a/{runtime}/tasks`, rejected at `OpenOutboundChannel` before the subscription is stored. The URL must be HTTPS, must resolve to a public (non-private/non-link-local/non-loopback) IP under DNS pinning, must not target cloud metadata hostnames, and must match the optional domain allowlist when configured. `details.reason` describes the specific validation failure (e.g., `scheme_not_https`, `private_ip`, `metadata_host`, `domain_not_allowlisted`). Distinct from `WEBHOOK_VALIDATION_FAILED` ([Section 25.5](25_agent-operability.md#255-operational-event-stream)), which applies to `lenny-ops` event-subscription webhooks. Not retryable — the caller must supply a conformant URL. See [Section 21.1](21_planned-post-v1.md) (A2A outbound push) and [Section 14](14_workspace-plan-schema.md) (SSRF validation rules). |
| `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED` | `PERMANENT` | 401 | Playground `/playground/*` mint path (apiKey mode or, as defense-in-depth, OIDC cookie-to-bearer exchange) rejected the subject token because its `typ` is not `user_bearer`. A `session_capability`, `a2a_delegation`, or `service_token` pasted into the playground API-key form cannot be re-minted into a broader, fresh-lifetime playground JWT — this invariant mirrors the OAuth token-exchange tenant invariant in [§13.3](13_security-model.md#133-credential-flow) with a symmetric subject-type restriction. `details.subjectTyp` carries the offending value (`session_capability` \| `a2a_delegation` \| `service_token`); `details.ingressPath` records the `/playground/*` route. Not retryable as-is — the caller must obtain a `user_bearer`-typed credential (an OIDC ID token or rotated `user_bearer` JWT) or switch to `authMode=oidc` for human-user access. See [Section 10.2](10_gateway-internals.md#102-authentication) (Playground mint invariants) and [Section 27.3](27_web-playground.md#273-authentication). |

**Validation error format.** When `code` is `VALIDATION_ERROR`, the `details` field contains a `fields` array describing each validation failure:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "category": "PERMANENT",
    "message": "Request validation failed.",
    "retryable": false,
    "details": {
      "fields": [
        {
          "field": "runtime",
          "message": "must not be empty",
          "rule": "required"
        },
        {
          "field": "workspace.maxSizeMB",
          "message": "must be between 1 and 10240",
          "rule": "range",
          "params": { "min": 1, "max": 10240 }
        }
      ]
    }
  }
}
```

Each entry: `field` (string) — JSON path to the invalid field. `message` (string) — human-readable description. `rule` (string) — validation rule that failed (e.g., `required`, `range`, `pattern`, `enum`). `params` (object, optional) — rule-specific parameters.

**Rate-limit headers.** All REST API responses include rate-limit headers:

| Header                  | Description                                                       |
| ----------------------- | ----------------------------------------------------------------- |
| `X-RateLimit-Limit`     | Maximum requests permitted in the current window                  |
| `X-RateLimit-Remaining` | Requests remaining in the current window                          |
| `X-RateLimit-Reset`     | UTC epoch seconds when the current window resets                  |
| `Retry-After`           | Seconds to wait before retrying (present on `429` and `503` responses) |

**`dryRun` query parameter.** Most admin `POST` and `PUT` endpoints accept `?dryRun=true`. Exceptions: action endpoints (`drain`, `force-terminate`, `warm-count`) and `DELETE` endpoints do not support `dryRun` — see below. Behavior: the gateway performs full request validation — schema, field constraints, referential integrity, policy checks, and quota evaluation — but **does not persist** the result or trigger any side effects (no CRD reconciliation, no pool scaling, no webhook dispatch). Audit events are **not** emitted for dry-run requests, with one exception: `POST /v1/admin/bootstrap?dryRun=true` emits a `platform.bootstrap_applied` audit event with `dryRun: true` so operators have a record of what a bootstrap run would have changed (see [Section 15.1](#151-rest-api) bootstrap endpoint). **`dryRun` never makes outbound network calls.** All referential integrity checks performed under `dryRun` are syntactic and against locally cached state only — for example, connector `mcpServerUrl` validation checks URL format and scheme allowlist but does not attempt a network connection. Live connectivity verification requires the dedicated `POST /v1/admin/connectors/{name}/test` endpoint (see below). The response body is identical to a non-dry-run success response (including the computed resource representation), with one addition: the response includes the header `X-Dry-Run: true`.

**Endpoint-specific `dryRun` semantics:**

- **Connectors (`POST /v1/admin/connectors`, `PUT /v1/admin/connectors/{name}`):** Validates URL format, scheme allowlist (`https` only in production), authentication field structure, and referential integrity against known environments. Does **not** perform DNS resolution, TLS handshake, or any outbound call to the connector endpoint. For live reachability testing, use `POST /v1/admin/connectors/{name}/test` (described below).
- **Experiments (`POST /v1/admin/experiments`, `PUT /v1/admin/experiments/{name}`):** Validates experiment definition, variant weight constraint (Σ variant_weights must be in [0, 1) — remainder is reserved for the control group), runtime/pool references, and variant-pool-to-base-runtime isolation monotonicity (each variant pool's `sessionIsolationLevel.isolationProfile` must be at least as restrictive as the base runtime's default pool profile; violations return `422 CONFIGURATION_CONFLICT` with `details.conflicts[]`, see [Section 10.7](10_gateway-internals.md#107-experiment-primitives)). Capacity validation is **not** included — `dryRun` does not query current pool utilization or node availability. Capacity feasibility is evaluated asynchronously by the PoolScalingController when the experiment is activated. To pre-check capacity, use `GET /v1/admin/pools/{name}` to inspect current `availableCount` and `warmCount` before activating an experiment.
- **Environments (`POST /v1/admin/environments`, `PUT /v1/admin/environments/{name}`):** Validates membership selectors and runtime scoping. When `dryRun=true`, the response body includes an additional `preview` object alongside the computed resource representation:

  ```json
  {
    "resource": {
      /* computed environment representation */
    },
    "preview": {
      "matchedRuntimes": ["claude-sonnet", "gpt-4-turbo"],
      "matchedConnectors": ["github-mcp", "jira-mcp"],
      "unmatchedSelectorTerms": []
    }
  }
  ```

  `matchedRuntimes` and `matchedConnectors` list all resources whose labels satisfy the environment's selectors at the time of the dry run. `unmatchedSelectorTerms` lists any selector terms that matched zero resources (useful for detecting typos in label keys or values). This preview is the primary mechanism for the Environment Management UI ([Section 21.5](21_planned-post-v1.md)).

**Connector live test endpoint.** `POST /v1/admin/connectors/{name}/test` performs a live connectivity check against an already-created connector: DNS resolution, TLS handshake, MCP `initialize` handshake (if the connector type supports it), and authentication credential validation. The response reports pass/fail for each stage:

```json
{
  "connector": "github-mcp",
  "stages": [
    { "name": "dns_resolution", "status": "passed", "latencyMs": 12 },
    { "name": "tls_handshake", "status": "passed", "latencyMs": 45 },
    { "name": "mcp_initialize", "status": "passed", "latencyMs": 230 },
    { "name": "auth_validation", "status": "passed", "latencyMs": 15 }
  ],
  "overall": "passed"
}
```

The endpoint requires `platform-admin` or `tenant-admin` role. It is rate-limited to 10 requests per connector per minute to prevent abuse as a network scanning tool. The test uses the connector's stored credentials and does not accept inline credential overrides.

Supported endpoints:

| Method | Endpoint                               | Notes                                                                               |
| ------ | -------------------------------------- | ----------------------------------------------------------------------------------- |
| `POST` | `/v1/admin/runtimes`                   | Validates runtime definition, checks image reference format                         |
| `PUT`  | `/v1/admin/runtimes/{name}`            | Validates update, checks etag                                                       |
| `POST` | `/v1/admin/delegation-policies`        | Validates policy rules and selector syntax                                          |
| `PUT`  | `/v1/admin/delegation-policies/{name}` | Validates update, checks etag                                                       |
| `POST` | `/v1/admin/connectors`                 | Validates connector config, checks URL format (no outbound calls)                   |
| `PUT`  | `/v1/admin/connectors/{name}`          | Validates update, checks etag (no outbound calls)                                   |
| `POST` | `/v1/admin/pools`                      | Validates pool spec, checks runtime reference                                       |
| `PUT`  | `/v1/admin/pools/{name}`               | Validates update, checks etag                                                       |
| `POST` | `/v1/admin/credential-pools`           | Validates credential pool structure                                                 |
| `PUT`  | `/v1/admin/credential-pools/{name}`    | Validates update, checks etag                                                       |
| `POST` | `/v1/admin/environments`               | Validates membership selectors and runtime scoping; returns `preview` object        |
| `PUT`  | `/v1/admin/environments/{name}`        | Validates update, returns `preview` with matched runtimes/connectors ([Section 21.5](21_planned-post-v1.md)) |
| `POST` | `/v1/admin/experiments`                | Validates definition, variant weights, and variant-pool-to-base-runtime isolation monotonicity (`422 CONFIGURATION_CONFLICT` when violated, see [Section 10.7](10_gateway-internals.md#107-experiment-primitives)); no capacity check |
| `PUT`  | `/v1/admin/experiments/{name}`         | Validates update, checks etag, and re-runs variant-pool isolation monotonicity; no capacity check |
| `POST` | `/v1/admin/external-adapters`          | Validates adapter configuration                                                     |
| `PUT`  | `/v1/admin/external-adapters/{name}`   | Validates update, checks etag                                                       |

ETag interaction: when `dryRun=true` is combined with `If-Match`, the gateway validates the etag against the current resource version and returns `412 ETAG_MISMATCH` if it does not match — the same behavior as a real request. This allows clients to pre-validate an update without committing it. When `dryRun=true` is used on a `POST` (create), `If-Match` is ignored since no prior version exists.

`DELETE` endpoints do not support `dryRun` — deletion validation is trivial (existence + authorization) and does not benefit from a preview. Action endpoints (`drain`, `force-terminate`, `warm-count`) do not support `dryRun` because their value is in the side effect, not validation.

**ETag-based optimistic concurrency.** Every admin resource in Postgres carries an integer `version` column (starts at 1, incremented on every successful write). The ETag value is the quoted decimal version: `"3"`. The gateway enforces ETags as follows:

- **GET responses.** All `GET` endpoints that return an admin resource (single-item or list) include an `ETag` header set to the resource's current version. List responses include per-item ETags in the response body (`"etag": "3"` on each object).
- **PUT requests — `If-Match` required.** Every admin `PUT` request **must** include an `If-Match` header containing the ETag obtained from a prior `GET`. If the header is missing or empty, the gateway returns `428 Precondition Required` with error code `ETAG_REQUIRED`. If the header is present but malformed (not a quoted decimal version per [RFC 7232 §2.3](https://www.rfc-editor.org/rfc/rfc7232#section-2.3) — e.g. unquoted, non-decimal, or containing a weak validator `W/` prefix which is not supported for admin resources), the gateway returns `400 Bad Request` with error code `VALIDATION_ERROR` and a `details.fields` entry identifying `If-Match` as the failing header. If the header is present and well-formed but does not match the current version, the gateway returns `412 Precondition Failed` with error code `ETAG_MISMATCH` (already in the error catalog above); the `412` response includes `details.currentEtag` containing the resource's current ETag so clients can refresh without a round-trip `GET`. On success, the response includes the new `ETag` reflecting the incremented version.
- **Retry pattern after `412 ETAG_MISMATCH`.** When a `PUT` returns `412`, the recommended client pattern is: (1) use `details.currentEtag` from the error response if present, or (2) re-`GET` the **specific resource** (not re-list the collection) to obtain the current ETag and resource body, then merge changes and retry. Clients performing bulk updates from a list response should re-`GET` only the individual resource that conflicted, not re-fetch the entire list.
- **POST requests.** `If-Match` is not required on `POST` (resource creation) and is ignored if present, since no prior version exists.
- **DELETE requests.** `If-Match` is **optional** on `DELETE`. When provided, the gateway validates it and returns `412 ETAG_MISMATCH` on mismatch. When omitted, the delete proceeds unconditionally (last-writer-wins). This avoids forcing clients to fetch before deleting, while still allowing concurrency-safe deletion when desired.
- **Deletion semantics for resources with dependents.** Deleting a resource that is referenced by active dependents is **blocked** (not cascaded). The gateway returns `409` with error code `RESOURCE_HAS_DEPENDENTS` and a `details.dependents` array listing the blocking references. Each entry in `details.dependents` includes `type`, `name` or `count`, and (where applicable) an `ids` array of up to 20 individual resource IDs — set `truncated: true` on the entry when the total count exceeds 20. Specific rules per resource type:
  - **Runtime:** blocked if referenced by any active pool (`status != draining/drained`) or any non-terminal session. `details.dependents` example: `[{"type": "pool", "name": "default-pool", "count": 1, "ids": ["default-pool"]}, {"type": "session", "state": "running", "count": 3, "ids": ["sess-abc", "sess-def", "sess-ghi"]}]`.
  - **Pool:** blocked if any sessions are running or suspended in the pool. Drain the pool first (`POST /v1/admin/pools/{name}/drain`), then delete once all sessions complete.
  - **Delegation Policy:** blocked if referenced by any runtime or derived runtime definition (`delegationPolicyRef`), or by any active (non-terminal) delegation lease (`delegationPolicyRef` or `maxDelegationPolicy`). Remove the reference from runtimes and wait for active leases to reach a terminal state before deleting. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) (deletion guard).
  - **Connector:** blocked if referenced by any environment or runtime. Remove the reference first.
  - **Credential Pool:** blocked if any active credential leases exist. Revoke leases first.
  - **Tenant:** blocked if any non-terminal sessions, pools, or credential pools exist under the tenant. All child resources must be removed first.
  - **Environment:** blocked if any sessions are active within the environment.
  - **Experiment:** blocked if `status: active` or `status: paused` and any non-terminal sessions have an `experimentContext` referencing this experiment. When `paused`, variant pools may still have in-flight sessions (see PoolScalingController behavior in [Section 10.7](10_gateway-internals.md#107-experiment-primitives)). Transition the experiment to `concluded` and wait for all enrolled sessions to reach a terminal state before deleting.
  - **External Adapter:** blocked if `status: active`. Set to `inactive` first.
- **Implementation.** The Postgres `UPDATE ... WHERE id = $1 AND version = $2` pattern ensures atomicity without application-level locking. If zero rows are affected, the gateway re-reads the current version and returns `412`.

Rate limits are applied per tenant and per user. Admin API endpoints have separate (higher) rate-limit windows from client-facing endpoints.

**Cursor-based pagination.** All list endpoints return paginated results using a cursor-based envelope. This applies to: `GET /v1/sessions`, `GET /v1/runtimes`, `GET /v1/pools`, `GET /v1/metering/events`, `GET /v1/sessions/{id}/artifacts`, `GET /v1/sessions/{id}/transcript`, `GET /v1/sessions/{id}/logs`, and all admin `GET` collection endpoints (e.g., `/v1/admin/runtimes`, `/v1/admin/pools`). Note: `GET /v1/admin/experiments/{name}/results` is **not** a paginated list endpoint — it returns a single aggregated object per experiment (see [Section 10.7](10_gateway-internals.md#107-experiment-primitives)).

Query parameters:

| Parameter | Type    | Default           | Description                                                                                                                                                                                     |
| --------- | ------- | ----------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cursor`  | string  | (none)            | Opaque cursor returned from a previous response. Omit for the first page.                                                                                                                       |
| `limit`   | integer | 50                | Number of items per page. Minimum: 1, maximum: 200. Values outside this range are clamped.                                                                                                      |
| `sort`    | string  | `created_at:desc` | Sort field and direction, formatted as `field:asc` or `field:desc`. Supported fields vary by resource (typically `created_at`, `updated_at`, `name`). Invalid fields return `VALIDATION_ERROR`. |

Response envelope:

```json
{
  "items": [
    /* array of resource objects */
  ],
  "cursor": "eyJpZCI6IjAxOTVmMzQ...",
  "hasMore": true,
  "total": 1247
}
```

Fields: `items` (array, required) — the page of results. `cursor` (string, nullable) — opaque cursor to pass as the `cursor` query parameter to fetch the next page; `null` when there are no more results. `hasMore` (boolean, required) — `true` if additional pages exist beyond this one. `total` (integer, optional) — total count of matching items across all pages, present only when cheaply computable (i.e., available from a cached count or inexpensive `COUNT(*)` query). Omitted when the count would require a full table scan or is otherwise expensive to compute. UIs may use `total` to render "X results found" or pagination progress indicators; they must not rely on its presence.

Cursors are opaque, URL-safe strings. They encode the sort key and unique tiebreaker (typically `id`) to guarantee stable iteration even when new items are inserted. Cursors are valid for 24 hours; expired cursors return `VALIDATION_ERROR` with `details.fields[0].rule: "cursor_expired"`. Clients must not parse or construct cursors — they are an internal implementation detail.

**`GET /v1/usage` response schema.** Note: `GET /v1/usage` is an aggregated endpoint (like `GET /v1/admin/experiments/{name}/results`), not a paginated list endpoint. It returns a single aggregated object and does not use the cursor-based pagination envelope.

```json
{
  "period": { "start": "2025-01-01T00:00:00Z", "end": "2025-01-31T23:59:59Z" },
  "totalSessions": 1523,
  "totalTokens": { "input": 45000000, "output": 22000000 },
  "totalPodMinutes": 12500.5,
  "byTenant": [
    {
      "tenantId": "t1",
      "sessions": 800,
      "tokens": { "input": 25000000, "output": 12000000 }
    }
  ],
  "byRuntime": [
    {
      "runtime": "claude-worker",
      "sessions": 1200,
      "tokens": { "input": 38000000, "output": 18000000 }
    }
  ]
}
```

### 15.2 MCP API

The MCP interface is for **interactive streaming sessions** and **recursive delegation**. It exposes the gateway as an MCP server over Streamable HTTP via the `MCPAdapter`.

**MCP tools (client-facing):**

| Tool                       | Description                                                    |
| -------------------------- | -------------------------------------------------------------- |
| `create_session`           | Create a new agent session                                     |
| `create_and_start_session` | Create, upload inline files, and start in one call             |
| `upload_files`             | Upload workspace files                                         |
| `finalize_workspace`       | Seal workspace, run setup                                      |
| `start_session`            | Start the agent runtime                                        |
| `attach_session`           | Attach to a running session (returns streaming task). Accepts optional `resumeFromSeq: uint64` to replay buffered events with `SeqNum > resumeFromSeq` before streaming new ones (see "Event-stream resume" below).           |
| `send_message`             | Send a message to a session (unified — replaces `send_prompt`) |
| `interrupt_session`        | Interrupt current agent work                                   |
| `get_session_status`       | Query session state (including `suspended`)                    |
| `get_task_tree`            | Get delegation tree for a session                              |
| `get_session_logs`         | Get session logs (paginated)                                   |
| `get_token_usage`          | Get token usage for a session                                  |
| `list_artifacts`           | List artifacts for a session                                   |
| `download_artifact`        | Download a specific artifact                                   |
| `terminate_session`        | End a session gracefully (marks `completed`)                   |
| `cancel_session`           | Force-cancel a session (marks `cancelled`); REST equivalent is `DELETE /v1/sessions/{id}` |
| `resume_session`           | Resume a suspended or paused session                           |
| `list_sessions`            | List active/recent sessions (filterable)                       |
| `list_runtimes`            | List available runtimes (identity-filtered, policy-scoped)     |

**Target MCP spec version:** MCP 2025-03-26 (latest stable at time of writing). All MCP features used by Lenny are gated on this version or later.

**Version negotiation.** The `MCPAdapter` performs MCP protocol version negotiation during connection initialization:

1. The client sends its supported MCP version in the `initialize` request (`protocolVersion` field per MCP spec).
2. The gateway responds with the highest mutually supported version. Lenny supports the **current** (`2025-03-26`) and **previous** (`2024-11-05`) MCP spec versions concurrently.
3. If the client's version is older than the oldest supported version, the gateway rejects the connection with a structured error (`MCP_VERSION_UNSUPPORTED`) including the list of supported versions.
4. Once negotiated, the connection is pinned to that version for its lifetime. The `MCPAdapter` dispatches to version-specific serialization logic internally — tool schemas, error formats, and streaming behavior conform to the negotiated version.

**Compatibility policy:** Lenny supports the two most recent stable MCP spec versions simultaneously. When a new MCP spec version is adopted, the oldest supported version enters a 6-month deprecation window. The gateway emits a `X-Lenny-Mcp-Version-Deprecated` warning header on connections using the deprecated version. (Header uses hyphens per RFC 7230; underscore-named headers are dropped by some proxies.)

**Session-lifetime exception for deprecated versions.** When a version exits the deprecation window (i.e., the gateway drops support for it on the 6-month boundary), connections that are already established and mid-session at that instant MUST NOT be forcibly terminated. The gateway enforces the following rule: version support removal applies only to **new** connection negotiations — any `MCPAdapter` connection that completed `initialize` handshake before the deprecation deadline is permitted to continue for the duration of its session (up to `maxSessionAgeSeconds`, [Section 11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)). Concretely:
- The gateway maintains a per-connection `negotiatedVersion` field set at `initialize` time.
- Version enforcement checks at message dispatch time use `negotiatedVersion` — not the current supported-version set — to route to the correct serialization logic.
- When the deprecated version's handler is scheduled for removal (deployment of the gateway binary that drops the old version), the `lenny-preflight` Job emits a warning if any sessions older than 1 hour are active on the deprecated version (`lenny_mcp_deprecated_version_active_sessions` gauge). Operators must drain these sessions (via graceful terminate + resume on the new version) before the deployment to avoid a mid-session protocol mismatch.
- If a session on the deprecated version is still active after the deployment (i.e., the operator did not drain), the gateway retains the deprecated version's serializer for those active sessions; new `initialize` handshakes on the retired version are rejected with `MCP_PROTOCOL_VERSION_RETIRED` (see Error code catalog above). Active sessions continue emitting the `X-Lenny-Mcp-Version-Deprecated` header already set during the deprecation window through their natural termination with no additional degradation signal. If a post-handler-removal defect nevertheless forces early termination of such a session, the session closes with a structured close carrying a `mcp_protocol_version_retired` degradation annotation on the enclosing `MessageEnvelope` (fields: `retiredVersion` — the negotiated MCP spec version whose handler has been removed, e.g., `"2024-11-05"`; `currentVersions` — the currently supported versions, e.g., `["2025-03-26", "2025-06-18"]`). This annotation is distinct from `schema_version_ahead` ([Section 15.5](#155-api-versioning-and-stability) item 7): `schema_version_ahead` signals "new writer, old reader" on the `schemaVersion` field of a persisted or streamed record, whereas `mcp_protocol_version_retired` signals the inverse — "old writer, new reader" on the negotiated MCP protocol version of a still-running connection. Observability dashboards filtering on `schema_version_ahead` MUST NOT conflate the two. The nonce-handshake mechanism ([Section 15.4.3](#1543-runtime-integration-levels), "Nonce wire format (v1 — intra-pod only)") is scoped exclusively to adapter↔runtime Unix-socket MCP connections inside a pod and has no role on the external-facing MCP surface; external clients that negotiated an MCP spec version at gateway-edge `initialize` time have never used, and will never use, a nonce.

**MCP features used:**

- Tasks (for long-running session lifecycle and delegation)
- Elicitation (for user prompts, auth flows)
- Streamable HTTP transport

**Event-stream resume.** Every `SessionEvent` frame the `MCPAdapter` writes to the client carries its `SeqNum` as the SSE `id:` line on the Streamable HTTP transport, parallel to the ops-event stream in [Section 25.5](25_agent-operability.md#255-operational-event-stream). `attach_session` accepts an optional `resumeFromSeq: uint64` parameter; when supplied, the gateway replays buffered events with `SeqNum > resumeFromSeq` before switching to live delivery. The gateway maintains a per-session event replay buffer sized by `gateway.sessionEventReplayBufferDepth` ([Section 10.4](10_gateway-internals.md#104-gateway-reliability)); clients MAY also rely on the SSE `Last-Event-ID` header as an equivalent, implicit `resumeFromSeq` on plain reconnects. When the requested sequence has been evicted from the buffer, the adapter emits a single protocol-level `gap_detected` frame carrying `{"lastSeenSeq": <resumeFromSeq>, "nextSeq": <oldestRetainedSeq>}` before resuming live delivery, so clients can surface a gap warning rather than silently losing events; this frame is a stream-control signal, not a `SessionEvent`, and is not part of the `SessionEventKind` closed enum above. Clients that did not observe any prior events (first attach) SHOULD omit `resumeFromSeq`.

**Stream keepalive.** To prevent idle-stream termination by intermediaries (Cloudflare, ELBs, and other L7 proxies commonly enforce 30–60s idle timeouts), the `MCPAdapter` writes an SSE comment line `:keepalive\n\n` on the Streamable HTTP response whenever no `SessionEvent` frame has been written for `20` seconds. The interval is fixed by the protocol contract so clients can derive a reliable liveness timeout without configuration; it is not tunable per connection. Comment lines are invisible to conforming SSE parsers (`EventSource` implementations ignore lines beginning with `:`) and carry no `id:` — they do not affect `SeqNum`, `Last-Event-ID` tracking, or the `gap_detected` contract. Clients SHOULD treat the absence of any byte (event or keepalive) for more than `60` seconds as a broken connection, close the stream, and reattach via `attach_session` with `resumeFromSeq` set to the last `SeqNum` they observed (or rely on the SSE built-in reconnect with `Last-Event-ID`); the event replay buffer and `gap_detected` frame handle any events emitted during the disconnect window exactly as on any other reconnect path.

**MCPAdapter OutboundChannel mapping.** The `MCPAdapter` is the platform's primary streaming surface: it MUST deliver session-scoped asynchronous events to attached clients over the Streamable HTTP SSE transport opened by `attach_session`. To participate in the [dispatch-filter rule](#sessionevent-kind-registry) ("Dispatch-filter rule"), the `MCPAdapter` overrides the `BaseAdapter` zero-value default of `OutboundCapabilities()` with the following explicit declaration (the override is mandatory — inheriting the `BaseAdapter` no-op would leave the streaming transport empty and contradict the adapter's documented role):

```go
func (a *MCPAdapter) OutboundCapabilities() OutboundCapabilitySet {
    return OutboundCapabilitySet{
        PushNotifications: true,
        SupportedEventKinds: []SessionEventKind{
            SessionEventStateChange,
            SessionEventOutput,
            SessionEventElicitation,
            SessionEventToolUse,
            SessionEventError,
            SessionEventTerminated,
        },
        MaxConcurrentSubscriptions: 0, // unlimited; one OutboundChannel per attached `attach_session` stream
    }
}
```

The six declared kinds cover the full closed `SessionEventKind` enum defined in [Shared Adapter Types](#shared-adapter-types); see the [SessionEvent Kind Registry](#sessionevent-kind-registry) for each kind's `SessionEvent.Payload` schema and firing conditions. `MCPAdapter` also declares `AdapterCapabilities.SupportsElicitation: true` (satisfying the [capability-consistency invariant](#sessionevent-kind-registry) that pairs `SupportedEventKinds` containing `elicitation` with `AdapterCapabilities.SupportsElicitation: true`). This contrasts with `A2AAdapter`, whose `elicitationDepthPolicy: block_all` ([Section 21.1](21_planned-post-v1.md#21-planned--post-v1)) requires omitting both `elicitation` and `tool_use` from `SupportedEventKinds` — the MCP transport has no such restriction because the MCP protocol natively supports the hop-by-hop elicitation chain ([Section 9.2](09_mcp-integration.md#92-elicitation-chain)).

**Per-kind MCP wire projection.** The `MCPAdapter.OutboundChannel.Send` implementation MUST map each `SessionEvent.Kind` to exactly one MCP wire frame on the session's SSE stream, using the table below. Each frame MUST carry the `SessionEvent.SeqNum` as the SSE `id:` line so that `attach_session` `resumeFromSeq` and `Last-Event-ID` reconnect paths replay the frame verbatim ([Event-stream resume](#152-mcp-api) above).

| `SessionEventKind`       | MCP wire projection                                                                                   | MCP method / notification name           | Notes                                                                                                                                                                                                                                                                                                                 |
| ------------------------ | ----------------------------------------------------------------------------------------------------- | ---------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SessionEventStateChange` | MCP task status notification (Tasks feature) carrying `{from, to, subState?}` in `params.metadata`.   | `notifications/tasks/statusUpdate`       | `from` / `to` values are translated through the Lenny-state → MCP-Tasks mapping in [Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema); `subState` (when present) is surfaced in `params.metadata.subState`.                                                                                |
| `SessionEventOutput`      | MCP streaming content block(s) appended to the `attach_session` task response.                        | Streaming content frames on `attach_session` (no standalone notification method) | Each `OutputPart` in the payload is translated through the [Translation Fidelity Matrix](#translation-fidelity-matrix) MCP column; `ref` fields are dereferenced per the [Adapter dereference obligation](#internal-outputpart-format).                                                                               |
| `SessionEventElicitation` | Native MCP `elicitation/create` request (MCP Elicitation feature) carrying the `lenny/request_elicitation` / `lenny/request_input` payload. | `elicitation/create`                     | The client's response flows back over the same SSE channel as the MCP elicitation reply; the gateway routes it through the hop-by-hop elicitation chain ([Section 9.2](09_mcp-integration.md#92-elicitation-chain)).                                                                                                  |
| `SessionEventToolUse` (phase: `requested`, approval required) | Native MCP `elicitation/create` request carrying the tool-approval prompt (`toolCallId`, `tool`, `arguments`). | `elicitation/create`                     | The elicitation reply carries the approve/deny decision, which the gateway consumes to emit the subsequent `phase: approved` or `phase: denied` wire frame (see row below). The approval-required requested-phase frame is the **only** `tool_use` projection that uses MCP Elicitation; this preserves the existing §15.2.1 "MCP elicitation exchanges" contract for exactly this case. |
| `SessionEventToolUse` (phase: `requested`, auto-approved or replay of already-approved) | MCP notification carrying the full `tool_use` payload (`toolCallId`, `tool`, `arguments`, `phase: "requested"`). | `notifications/lenny/toolCall`           | Used when policy does not require approval, or when the event is replayed from the event buffer for an already-resolved tool call. A notifications frame — not an elicitation request — because no client response is expected.                                                                                      |
| `SessionEventToolUse` (phase: `approved` / `denied`) | MCP notification carrying the `toolCallId` and resolved phase.                                        | `notifications/lenny/toolCall`           | Emitted after the approval elicitation (above row) resolves, or immediately after the requested frame under auto-approve policy. Allows observability clients that did not surface the elicitation to still see the resolution.                                                                                     |
| `SessionEventToolUse` (phase: `completed`) | MCP notification carrying the `toolCallId`, `phase: "completed"`, and `result: OutputPart[]`.         | `notifications/lenny/toolCall`           | Always a notifications frame — `completed` is never an elicitation because it is a unidirectional observability signal (no client response expected). This row is what the approval-collapsed model cannot express; making it explicit here closes the observability gap.                                            |
| `SessionEventError`       | MCP notification carrying `{code, category, message, retryable, details?}` from the shared error taxonomy ([Section 15.2.1](#1521-restmcp-consistency-contract) item 3). | `notifications/lenny/error`              | Non-terminal mid-session errors. Terminal errors are surfaced via the session's terminal task frame instead (see `SessionEventTerminated`).                                                                                                                                                                           |
| `SessionEventTerminated`  | MCP task completion frame on the `attach_session` task, with `status` mapped from `TerminationCode` through the [Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema) protocol mapping and the `TerminationReason.Detail` surfaced in `result.metadata.terminationDetail`. | Task completion on `attach_session` (MCP Tasks final-state frame) | After the terminal frame is written, the adapter closes the SSE stream and `OutboundChannel.Close()` runs. No further SessionEvents are delivered on this channel.                                                                                                                                                    |

The `notifications/lenny/*` namespace is a Lenny-defined extension under the `notifications/` method-name convention established elsewhere in the spec (e.g., [`notifications/message`](25_agent-operability.md) for operational events); it scopes observability signals that have no native MCP method. Clients that do not recognize a `notifications/lenny/*` method MUST ignore it per the MCP specification's unknown-notification handling — the SSE stream is not interrupted.

**Phase-transition contract preserved.** Mapping each `tool_use` phase transition to exactly one wire frame restores the [SessionEvent Kind Registry](#sessionevent-kind-registry) "one event per phase transition" contract: (a) `requested` always emits one frame, either `elicitation/create` (approval required) or `notifications/lenny/toolCall` (auto-approved / replay); (b) `approved` / `denied` always emits one `notifications/lenny/toolCall` frame, independent of how the decision was sourced (elicitation reply, REST `/approve` call, policy auto-approval); (c) `completed` always emits one `notifications/lenny/toolCall` frame. Observability clients see the full lifecycle regardless of approval policy; approval-only clients can still participate via the `elicitation/create` frame without losing completed-phase signals on other channels.

**Cross-reference and replay.** The per-kind table above is authoritative for `MCPAdapter`; the closed `SessionEventKind` enum and its `SessionEvent.Payload` schemas are defined in the [SessionEvent Kind Registry](#sessionevent-kind-registry). The [Event-stream resume](#152-mcp-api) contract (SSE `id:` line, `resumeFromSeq`, `gap_detected`) applies uniformly to every frame in the table regardless of method name — third-party MCP-like adapters that replicate this wire projection MUST replay each kind's frame verbatim from the replay buffer to pass the `RegisterAdapterUnderTest` "event-stream resume" matrix entry ([Section 15.2.1](#1521-restmcp-consistency-contract)).

#### 15.2.1 REST/MCP Consistency Contract

The REST API ([Section 15.1](#151-rest-api)) and MCP tools ([Section 15.2](#152-mcp-api)) intentionally overlap for operations like session creation, status queries, and artifact retrieval. Five rules govern this overlap:

1. **Semantic equivalence.** REST and MCP endpoints that perform the same operation (e.g., `POST /v1/sessions` and `create_session` MCP tool) must return semantically identical responses. Both API surfaces share a common service layer in the gateway so that business logic, validation, and response shaping are implemented exactly once.

2. **Tool versioning.** MCP tool schema evolution is governed by [Section 15.5](#155-api-versioning-and-stability) (API Versioning and Stability), item 2.

3. **Shared error taxonomy.** All error responses — REST and MCP — use the error categories defined in [Section 16.3](16_observability.md#163-distributed-tracing) (`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`). REST errors return a JSON body: `{"error": {"code": "QUOTA_EXCEEDED", "category": "POLICY", "message": "...", "retryable": false}}`. MCP tool errors use the same `code` and `category` fields inside the MCP error response format, so clients can apply a single error-handling strategy regardless of API surface.

4. **OpenAPI as source of truth.** The REST API's OpenAPI spec is the single authoritative schema for all overlapping operations. MCP tool schemas for overlapping operations (e.g., `create_session`, `get_session_status`, `list_artifacts`) are generated from the OpenAPI spec's request/response definitions, not maintained independently. A code generation step in the build pipeline produces MCP tool JSON schemas from OpenAPI operation definitions, ensuring structural consistency by construction. Any manual MCP-only tool (e.g., `lenny/delegate_task`) that has no REST counterpart is authored independently but must use the shared error taxonomy (item 3).

5. **Contract testing.** CI includes contract tests that call the REST endpoint and **every built-in external adapter** (MCP, OpenAI Completions, Open Responses) for every overlapping operation and assert both structural and behavioral equivalence of responses. These tests cover:

   (a) **Success paths** — identical response payloads modulo transport envelope.

   (b) **Validation errors** — same error `code` and `category` for identical invalid inputs.

   (c) **Authz rejections** — same denial behavior.

   (d) **Behavioral equivalence — `retryable` and `category` flags.** For every error condition exercised in (b) and (c), the `retryable` flag and error `category` must be identical across REST and all adapter surfaces. A transient error that is `retryable: true` on REST must be `retryable: true` on MCP and every other adapter. This prevents silent breakage of client retry logic when switching API surfaces.

   (e) **Behavioral equivalence — session state transitions.** After performing an identical sequence of operations (e.g., create session, interrupt session), `GET /v1/sessions/{id}` and the `get_session_status` MCP tool must return the same session state. The contract tests include a set of fixed operation sequences that exercise all externally visible state transitions (see [Section 7.2](07_session-lifecycle.md#72-interactive-session-model)) and assert state identity across surfaces.

   (f) **Behavioral equivalence — pagination.** For overlapping list operations (e.g., listing artifacts), default page size, cursor semantics, and empty-result shapes must be identical across REST and adapter surfaces. Adapters must not silently return a different subset of results for the same query.

   Contract tests run on every PR; a failure blocks merge. The test harness is introduced in Phase 5 ([Section 18](18_build-sequence.md)) alongside the first phase where both REST and MCP surfaces are active.

   **REST-only operations.** The following REST endpoints intentionally have no MCP tool equivalents: `POST /v1/sessions/{id}/derive`, `POST /v1/sessions/{id}/replay`, `POST /v1/sessions/{id}/extend-retention`, `POST /v1/sessions/{id}/eval`, `POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve`, `POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny`, `POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond`, and `POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss`. Rationale: `derive` and `replay` are developer workflow operations typically driven by CI pipelines or human operators, not by agents mid-session. `extend-retention` is an administrative lifecycle action. `eval` is a post-hoc scoring endpoint called by external pipelines ([Section 10.7](10_gateway-internals.md#107-experiment-primitives)). The tool-use approval and elicitation response/dismiss endpoints carry no MCP tool equivalents because MCP clients receive and resolve these prompts through the native MCP **Elicitation** feature ([Section 9.2](09_mcp-integration.md#92-elicitation-chain)) — the gateway's `MCPAdapter` surfaces pending elicitations and **approval-required** `tool_use` requested-phase events as MCP `elicitation/create` exchanges on the session's streaming transport, and the client's response flows back over that same channel. This elicitation mapping is **scoped specifically to the approval-required requested phase**; auto-approved `requested`, `approved` / `denied`, and `completed` phases are surfaced as `notifications/lenny/toolCall` frames rather than elicitations — see the [MCPAdapter OutboundChannel mapping](#152-mcp-api) subsection for the full per-kind wire projection. Exposing the approval and elicitation REST endpoints as additional MCP tools would duplicate the native elicitation path for the approval-required case; the other `tool_use` phases have no elicitation counterpart to duplicate because they are unidirectional observability signals. MCP-first clients needing the REST-only operations should use the REST API directly — the gateway accepts both surfaces on the same authentication credentials.

   **`RegisterAdapterUnderTest` test matrix.** The contract test harness exposes a `RegisterAdapterUnderTest(adapter ExternalProtocolAdapter)` entry point so that third-party adapter authors can run the suite against their implementation. The test matrix covers the full operation set of overlapping endpoints:
   - All session lifecycle operations: create, get status, interrupt, resume, terminate, list artifacts, retrieve artifact.
   - All error classes: `VALIDATION_ERROR`, `QUOTA_EXCEEDED`, `RATE_LIMITED`, `RESOURCE_NOT_FOUND`, `INVALID_STATE_TRANSITION`, `PERMISSION_DENIED`, `CREDENTIAL_REVOKED`, `CREDENTIAL_POOL_EXHAUSTED`, `ISOLATION_MONOTONICITY_VIOLATED`, and the session-creation rejection family catalogued in §15.4 (`VARIANT_ISOLATION_UNAVAILABLE`, `REGION_CONSTRAINT_UNRESOLVABLE`, `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS`, `ENV_VAR_BLOCKLISTED`, `SDK_DEMOTION_NOT_SUPPORTED`, `POOL_DRAINING`, `CIRCUIT_BREAKER_OPEN`, `ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`) — each exercised with a canonical triggering input on `POST /v1/sessions` and its MCP `create_session` counterpart. For each, the test asserts identical `code`, `category`, `retryable`, and HTTP status (REST-side) / structured-error status (MCP-side) values, keeping the matrix in lockstep with the §15.4 error code catalog (any future session-creation rejection added to §15.4 MUST be added to this list in the same change).
   - All state transition sequences: at minimum the sequences `create→running→completed`, `create→running→interrupted→resumed→completed`, and `create→running→terminated`.
   - Pagination: multi-page artifact list traversal asserting cursor behavior and total result set identity.
   - Event-stream resume: after a forced stream disconnect, a subsequent `attach_session` with `resumeFromSeq` (or SSE `Last-Event-ID`) returns all events with `SeqNum > resumeFromSeq` in order; when the requested sequence is beyond the replay buffer ([§10.4](10_gateway-internals.md#104-gateway-reliability)), a single `gap_detected` protocol-level frame precedes the oldest retained event.
   - Coordinator-handoff reattach: a client reattaching with `resumeFromSeq < lastSeqBeforeHandoff` after a coordinator handoff MUST receive a synthesized state-frame sequence reconstructing session, delegation-tree, and elicitation state from durable Postgres state BEFORE any `gap_detected` frame is emitted. The test asserts: (a) the frame set includes exactly one `session.resumed` with `resumeMode: "coordinator_handoff"` and a `workspaceRecoveryFraction` sourced from `session_checkpoint_meta` when present; (b) a `status_change` frame is present if and only if the current session state differs from the state inferred at `resumeFromSeq`; (c) a `children_reattached` frame is present if and only if one or more children's completion records in `session_tree_archive` carry `completion_seq > resumeFromSeq`; (d) every synthesized frame carries a monotonic `SeqNum` strictly greater than `resumeFromSeq` and less than or equal to the current `sessions.last_seq`; (e) `gap_detected`, if emitted, follows the synthesized frames and references only the volatile-buffer residue (e.g., transient `agent_output` deltas) — see [§10.4](10_gateway-internals.md#104-gateway-reliability) "Coordinator-handoff reattach — volatile vs. reconstructible events".

   Third-party adapters that do not pass this full matrix **must not be enabled in production**. The `POST /v1/admin/external-adapters` registration endpoint enforces this gate: a new adapter is created in `status: pending_validation` and will not receive traffic until `POST /v1/admin/external-adapters/{name}/validate` is called and returns a passing result. `POST /v1/admin/external-adapters/{name}/validate` runs the `RegisterAdapterUnderTest` suite in a sandboxed environment against the registered adapter and transitions the adapter to `status: active` on success or `status: validation_failed` (with per-test failure details) on failure. Adapters in `pending_validation` or `validation_failed` status are excluded from all traffic routing. This makes compliance testable without out-of-band coordination, and makes the production gate machine-enforceable rather than a documentation requirement.

### 15.3 Internal Control API (Custom Protocol)

Gateway ↔ Pod communication over gRPC + mTLS. See [Section 4.7](04_system-components.md#47-runtime-adapter) (Runtime Adapter) for the full RPC surface. The wire contract is published as machine-readable artifacts in [Section 15.4](#154-runtime-adapter-specification); this section (15.4 and its subsections) is the normative prose reference and is kept in sync with those artifacts.

### 15.4 Runtime Adapter Specification

The runtime adapter contract is published as three machine-readable artifacts committed to the repository and released alongside each Lenny release:

- **`schemas/lenny-adapter.proto`** — Protobuf service and message definitions for the gateway ↔ adapter gRPC surface ([Section 4.7](04_system-components.md#47-runtime-adapter) RPC table). Includes the structured error code enum with categories (transient, permanent, policy), the `Attach` bidirectional streaming messages, the version negotiation protocol (adapter advertises capabilities at startup; gateway selects a compatible protocol version), and the gRPC Health Checking Protocol binding.
- **`schemas/lenny-adapter-jsonl.schema.json`** — JSON Schema (Draft 2020-12) for every adapter↔binary stdin/stdout message defined in [Section 15.4.1](#1541-adapterbinary-protocol) (`message`, `tool_result`, `heartbeat`, `shutdown`, `response`, `tool_call`, `heartbeat_ack`, `status`, `set_tracing_context`, and every lifecycle-channel message). Open-string `type` fields are modeled via `anyOf` with pass-through for unknown types per the canonical type registry contract.
- **`schemas/outputpart.schema.json`** — JSON Schema for the `OutputPart` envelope, including the canonical type registry tables, `schemaVersion` per-type field contract, and the namespace convention for third-party `x-<vendor>/<typeName>` types.

The artifacts are versioned by Lenny release tag. Breaking changes to the `.proto` file follow [buf](https://buf.build/)-style breaking-change rules; JSON Schema changes follow the `additionalProperties` discipline documented per message. A Go reference implementation of the adapter (`examples/runtimes/echo/`) is built from the same `.proto` file and serves as the executable reference. **This section (15.4 and its subsections) remains the normative prose description**; any discrepancy between the artifacts and this prose is a bug that must be reconciled before release.

**SDK-warm demotion contract:** Adapters for runtimes that declare `capabilities.preConnect: true` **must** implement the `DemoteSDK` RPC. This RPC cleanly terminates the pre-connected agent process and returns the pod to a pod-warm state so that workspace files (including those matching `sdkWarmBlockingPaths`) can be materialized before the agent starts. The specification must document: expected teardown behavior, timeout (default: 10s — if the SDK process does not exit within this window, the adapter sends SIGKILL), post-demotion pod state (equivalent to a freshly warmed pod-warm pod), and the `UNIMPLEMENTED` error code for adapters that do not support demotion. Runtime authors who set `preConnect: true` without implementing `DemoteSDK` will see session failures whenever a client uploads files matching `sdkWarmBlockingPaths`.

#### 15.4.1 Adapter↔Binary Protocol

The runtime adapter communicates with the agent binary over **stdin/stdout** using newline-delimited JSON (JSON Lines). Each message is a single JSON object terminated by `\n`. The `prompt` message type is removed — the unified `message` type handles all inbound content delivery.

**Inbound messages (adapter → agent binary via stdin):**

| `type` field  | Description                                                                                                                                                         |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `message`     | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification. Carries optional `slotId` for concurrent-workspace mode. |
| `tool_result` | The result of a tool call requested by the agent. Carries `slotId` in concurrent-workspace mode.                                                                    |
| `heartbeat`   | Periodic liveness ping; agent must respond                                                                                                                          |
| `shutdown`    | Graceful shutdown with no new task                                                                                                                                  |

The `message` type carries an `input` field containing an `OutputPart[]` array (see Internal `OutputPart` Format below), supporting text, images, structured data, and other content types. No `sessionState` field — the runtime knows it's receiving its first message by virtue of just having started. No `follow_up` or `prompt` type anywhere in the protocol.

**Outbound messages (agent binary → adapter via stdout):**

| `type` field             | Description                                                                                           |
| ------------------------ | ----------------------------------------------------------------------------------------------------- |
| `response`               | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` in concurrent-workspace mode. |
| `tool_call`              | Agent requests execution of a tool. Carries `slotId` in concurrent-workspace mode.                    |
| `heartbeat_ack`          | Acknowledges an inbound `heartbeat`. Protocol-level; no content payload.                              |
| `status`                 | Optional status/trace update                                                                          |
| `set_tracing_context`    | Registers tracing identifiers for propagation through delegation. Payload: `{"type": "set_tracing_context", "context": {"langsmith_run_id": "run_abc123"}}`. The adapter stores the context and automatically attaches it to all subsequent `lenny/delegate_task` gRPC requests. Validation rules ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) are enforced by the gateway when the delegation request arrives. Available at all tiers. See [Section 16.3](16_observability.md#163-distributed-tracing) for the two-tier tracing model. |

**`input_required` outbound message type removed.** Replaced by `lenny/request_input` blocking MCP tool call on the platform MCP server.

**`slotId` for concurrent-workspace multiplexing:** Session mode and task mode messages never carry `slotId` and runtimes for those modes never see it. Concurrent-workspace runtimes implement a dispatch loop keyed on `slotId` — each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This allows multiple independent concurrent task streams through a single stdin channel.

**Task mode between-task signaling:** Adapter sends `{type: "task_complete", taskId: "..."}` on the lifecycle channel after a task completes. The runtime releases task-specific resources and replies with `{type: "task_complete_acknowledged", taskId: "..."}`. After deployer-defined `cleanupCommands` and Lenny scrub complete, the adapter sends `{type: "task_ready", taskId: "..."}` with the new task's ID. The runtime re-reads the adapter manifest (regenerated per task) and the next `{type: "message"}` on stdin is the start of the new task. This is distinct from `terminate`, which always means process exit.

**stderr** is captured by the adapter for logging and diagnostics but is **not** parsed as protocol messages.

**stdout flushing requirement:** Every JSON Lines message written to stdout MUST be followed by a flush before the binary blocks on the next `read_line(stdin)`. Many language runtimes buffer stdout by default; without an explicit flush the adapter never receives the message and the session hangs silently. Language-specific guidance:

| Language | Required action |
| -------- | --------------- |
| Python   | `sys.stdout.flush()` after each `print()`, or open stdout with `sys.stdout = io.TextIOWrapper(sys.stdout.buffer, line_buffering=True)` |
| Node.js  | Use `process.stdout.write(line + "\n")` — Node's stdout is line-buffered when connected to a pipe, but only unbuffered when writing synchronously; always call the callback or await the write before blocking on stdin |
| Ruby     | `$stdout.sync = true` at startup |
| Java     | Use `PrintStream` with `autoFlush=true`: `new PrintStream(System.out, true)` |
| Go       | Use `bufio.NewWriter(os.Stdout)` with an explicit `Flush()` call after each `WriteString`, or write directly to `os.Stdout` (unbuffered by default) |
| Rust     | Call `stdout.flush()` from `std::io::Write` after each write, or use `BufWriter` with explicit flush |
| C / C++  | `fflush(stdout)` after each `fputs`/`printf`, or set `setbuf(stdout, NULL)` at startup for unbuffered mode |

Runtimes that use a line-buffered or fully-buffered stdout MUST flush after every outbound message. The reference Go implementation (`examples/runtimes/echo/`) writes directly to `os.Stdout` (unbuffered) and requires no explicit flush call.

#### Internal `OutputPart` Format

`agent_text` streaming event is replaced by `agent_output` carrying `OutputPart` array. `TaskResult` and `TaskSpec` use `OutputPart` arrays. This is Lenny's internal content model — the adapter translates to/from external protocol formats (MCP, A2A) at the boundary.

```json
{
  "schemaVersion": 1,
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "lenny-blob://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

**Properties:**

- **`schemaVersion` is an integer identifying the OutputPart schema revision (default `1`).** Present on every persisted `OutputPart`. The forward-compatibility contract has obligations on both sides:
  - **Producer obligation:** Producers MUST set `schemaVersion` to the highest version required by the fields they emit. When a schema version introduces semantically important fields (e.g., `citations` in v2), the producer MUST set `schemaVersion` to that version so consumers can detect the presence of fields they may not understand.
  - **Consumer obligation — streaming/live delivery:** Consumers MUST NOT reject an `OutputPart` solely because its `schemaVersion` is higher than the consumer understands. When a consumer encounters a `schemaVersion` it does not recognize, it processes the fields it does understand and MUST surface a **degradation signal**: a `schema_version_ahead` annotation on the parent `MessageEnvelope` (with `"knownVersion"` and `"encounteredVersion"` fields) so the end user or upstream caller is informed that the response may be incomplete. Consumers MUST NOT silently discard unknown fields without this signal. This ensures data loss from schema mismatch is always visible rather than hidden. `schema_version_ahead` is scoped specifically to the "new writer, old reader" direction on the `schemaVersion` field of a record; it is one of several distinct degradation annotation kinds catalogued in [Section 15.5](#155-api-versioning-and-stability) item 7 (Degradation annotation catalog) and MUST NOT be reused for unrelated signals such as retired MCP protocol versions.
  - **Consumer obligation — durable storage (TaskRecord):** When `OutputPart` arrays are persisted as part of a `TaskRecord` ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)), the forward-read rule from [Section 15.5](#155-api-versioning-and-stability) item 7 applies: if a reader encounters an `OutputPart` with a `schemaVersion` it does not recognize, it MUST **forward-read** — process all fields it understands and preserve all unknown fields verbatim (pass-through) — rather than rejecting the record. Billing and audit records retained for 13 months will span multiple schema revisions; silent data loss or outright rejection in these records is unacceptable. If a durable consumer cannot safely pass through unknown fields (e.g., it writes to a schema-strict sink), it MUST emit a `durable_schema_version_ahead` structured error to an operator alert channel and queue the record for manual review rather than dropping it. This rule is consistent with the general durable-consumer rule in [Section 15.5](#155-api-versioning-and-stability) item 7 and extends it explicitly to `OutputPart` arrays embedded within persisted `TaskRecord` objects.
- **`type` is an open string — not a closed enum — with a versioned canonical type registry.** The registry defines platform-defined types and their guaranteed translation behavior per adapter. Unprefixed names are reserved for the platform registry; third-party extensibility uses the `x-<vendor>/` namespace (see namespace convention below). Any type not in the current registry version is treated as a custom type and falls back to `text` with the original type preserved in `annotations.originalType`. Types may be added to the registry in minor releases; removing a type or changing its translation behavior is a breaking change requiring a major version bump. To preserve forward-compatibility across minor releases, unknown unprefixed types are **not** rejected at ingress — they are passed through with the same custom-type fallback, plus an `unregistered_platform_type` warning annotation, so that a newly registered type can be emitted by an updated runtime before all gateways have been upgraded. This retains open-string extensibility while making translation deterministic across adapter implementations.

  **Canonical Type Registry (v1):**

  | Type               | Description                                   | MCP Translation                                              | OpenAI Translation                           | A2A Translation                                      |
  | ------------------ | --------------------------------------------- | ------------------------------------------------------------ | -------------------------------------------- | ---------------------------------------------------- |
  | `text`             | Plain or formatted text                       | `TextContent` block                                          | `text` content                               | A2A `TextPart`                                       |
  | `code`             | Source code with optional language annotation | `TextContent` with `language` annotation                     | `text` content                               | A2A `TextPart` with `mimeType`                       |
  | `reasoning_trace`  | Model reasoning/chain-of-thought              | `TextContent` with `thinking` annotation                     | `text` content (reasoning not representable) | A2A `TextPart` with `metadata.semantic: "reasoning"` |
  | `citation`         | Source citation or reference                  | `TextContent` with citation annotation                       | `text` content                               | A2A `TextPart` with `metadata.semantic: "citation"`  |
  | `screenshot`       | Screen capture image                          | `ImageContent` block                                         | `image_url` content                          | A2A `FilePart` with image MIME type                  |
  | `image`            | General image content                         | `ImageContent` block                                         | `image_url` content                          | A2A `FilePart` with image MIME type                  |
  | `diff`             | Code diff / patch                             | `TextContent` with `language: "diff"`                        | `text` content                               | A2A `TextPart` with `mimeType: "text/x-diff"`        |
  | `file`             | File content (binary or text)                 | `ResourceContent` block                                      | Resolved to inline `text` or dropped         | A2A `FilePart`                                       |
  | `execution_result` | Compound output from code execution           | Flattened to sequential `TextContent` blocks with `parentId` | Flattened to sequential `text` entries       | A2A composite part                                   |
  | `error`            | Error or diagnostic message                   | `TextContent` with `isError: true`                           | `text` content                               | A2A `TextPart` with `metadata.semantic: "error"`     |

  **Custom types** (any `type` value not listed above): collapsed to `text` with `annotations.originalType` set to the original type string. Runtimes may emit any custom type; the gateway passes them through internally but adapters apply the fallback rule at the protocol boundary. The registry is published as part of the runtime adapter specification and versioned alongside the adapter protocol.

  **Namespace convention for third-party types.** To avoid collisions with future platform-defined types, all vendor- or community-defined custom types MUST use a reverse-DNS namespace prefix in the form `x-<vendor>/<typeName>` (e.g., `x-acme/heatmap`, `x-myorg/audio-transcript`). Unprefixed names are reserved for platform-defined registry types. The gateway logs and annotates unknown unprefixed types at ingress (adding an `unregistered_platform_type` warning annotation with the unrecognized type string) but does **not** reject them — they fall through to the standard custom-type-to-`text` collapse so that newly registered types introduced in a minor release are forward-compatible across gateway versions that have not yet been upgraded.

  **`schemaVersion` per-type contract.** The `schemaVersion` field on an `OutputPart` is scoped to the envelope schema (field set, semantics of existing fields). The stable field set guaranteed at each registry version is:

  | Type               | `schemaVersion` 1 — guaranteed fields                                               | Notes on future versions                                              |
  | ------------------ | ----------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
  | `text`             | `type`, `inline`, `mimeType` (`text/plain`)                                         | v2 may add `citations[]`                                              |
  | `code`             | `type`, `inline`, `mimeType`, `annotations.language`                                | —                                                                     |
  | `reasoning_trace`  | `type`, `inline`                                                                    | v2 may add structured `steps[]`                                       |
  | `citation`         | `type`, `inline`, `annotations.source`                                              | —                                                                     |
  | `screenshot`       | `type`, `inline` (base64) or `ref`, `mimeType` (image/*)                            | —                                                                     |
  | `image`            | `type`, `inline` (base64) or `ref`, `mimeType` (image/*)                            | —                                                                     |
  | `diff`             | `type`, `inline`, `annotations.language` (`diff`)                                   | —                                                                     |
  | `file`             | `type`, `inline` or `ref`, `mimeType`                                               | —                                                                     |
  | `execution_result` | `type`, `parts[]` (each part is a full `OutputPart`)                                | v2 may add `exitCode`, `duration`                                     |
  | `error`            | `type`, `inline` (human-readable message), `annotations.errorCode` (optional)       | —                                                                     |

  A producer emitting fields that were introduced in a later schema version MUST set `schemaVersion` to that version. Consumers that encounter a `(type, schemaVersion)` combination they do not recognize apply the forward-compatibility rules defined above: degradation signal for live delivery; forward-read with unknown-field preservation for durable storage (see [Section 15.5](#155-api-versioning-and-stability) item 7).

- **`mimeType` handles encoding separately.** The gateway validates, logs, and routes based on MIME type without understanding semantics.
- **`inline` vs `ref` — resolution protocol.** A part either contains bytes inline (`inline` field set, base64 for binary content) or references external blob storage (`ref` field set). The two fields are mutually exclusive on any given part; setting both is a validation error (`400 OUTPUTPART_INLINE_REF_CONFLICT`). The gateway selects the representation automatically based on part size:

  | Part size | Gateway action | Consumer sees |
  | --- | --- | --- |
  | ≤ 64 KB | Store inline (base64 for binary, UTF-8 for text) | `inline` field populated; `ref` absent |
  | > 64 KB and ≤ 50 MB | Stage to blob store; set `ref` to `LennyBlobURI` | `ref` populated; `inline` absent |
  | > 50 MB | Rejected at ingress | `413 OUTPUTPART_TOO_LARGE` |

  **`LennyBlobURI` scheme.** Blob references use the URI scheme `lenny-blob://`:

  ```
  lenny-blob://{tenant_id}/{session_id}/{part_id}?ttl={seconds}&enc=aes256gcm
  ```

  | Component | Description |
  | --- | --- |
  | `tenant_id` | Tenant namespace — prevents cross-tenant dereference |
  | `session_id` | Originating session — scopes the blob to one session |
  | `part_id` | Stable part identifier (matches `OutputPart.id`) |
  | `ttl` | Seconds until the blob expires in storage (see TTL table below) |
  | `enc` | Encryption indicator; always `aes256gcm` for stored blobs |

  **Immutability guarantee.** Blob storage is write-once per `(tenant_id, session_id, part_id)` triple. The gateway writes a blob exactly once when staging an `OutputPart`; subsequent reads always return the same bytes. No `generation` component is needed in the URI because part IDs are globally unique within a session — the internal `coordination_generation` counter ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)) is used only for coordinator fencing and never causes part IDs to be reused or existing blobs to be overwritten. A `lenny-blob://` URI is safe to cache and share for the duration of its `ttl`.

  **TTL policy by context:**

  | Context | Default TTL | Configurable? |
  | --- | --- | --- |
  | Live streaming delivery (session active) | 3 600 s (1 h) | Yes — `blobStore.liveDeliveryTtlSeconds` |
  | Persisted in `TaskRecord` | 2 592 000 s (30 d) | Yes — `blobStore.taskRecordTtlSeconds` |
  | Audit / billing event payload | 34 128 000 s (13 months) | Yes — `blobStore.auditTtlSeconds` |
  | Delegation export (parent → child) | Duration of child session + 1 h | Fixed |

  **Consumer fallback obligation.** When a consumer encounters a `ref` it cannot dereference (blob expired, storage unavailable, network partition), it MUST:
  1. Surface a `blob_ref_unresolvable` degradation annotation on the `MessageEnvelope` (fields: `partId`, `ref`, `reason`).
  2. Substitute a placeholder `OutputPart` of type `error` with `inline: "Blob reference unresolvable: {reason}"`.
  3. Never silently drop the part.

  **Adapter dereference obligation.** External protocol adapters (MCP, OpenAI, A2A) MUST dereference `ref` fields before serializing outbound messages to external clients — external protocols do not speak `lenny-blob://`. The REST adapter passes `ref` values through as-is (REST clients may dereference directly via `GET /v1/blobs/{ref}`).
- **`annotations` as an open metadata map.** `role`, `confidence`, `language`, `final`, `audience` — any metadata. The gateway can index and filter on annotations without understanding the part type.
- **`parts` for nesting.** Compound outputs (e.g., `execution_result` containing code, stdout, stderr, chart) are first-class.
- **`id` enables part-level streaming updates** — concurrent part delivery where text streams while an image renders.

**Rationale for internal format over MCP content blocks directly:** Runtimes are insulated from external protocol evolution. When MCP adds new block types or A2A parts change, only the gateway's `ExternalProtocolAdapter` translation layer updates — runtimes are untouched.

**MCP content block → OutputPart mapping (inbound translation):** When the gateway receives MCP content blocks from a client and delivers them to a runtime, the adapter translates each MCP block to an `OutputPart` as follows:

| MCP content block type | → `OutputPart.type` | `OutputPart.inline` source                  | `OutputPart.mimeType`          | `OutputPart.ref` source   | Notes                                                             |
| ---------------------- | ------------------- | ------------------------------------------- | ------------------------------ | ------------------------- | ----------------------------------------------------------------- |
| `TextContent`          | `text`              | `text` field                                | `text/plain`                   | —                         | `language` annotation → `annotations.language` if present        |
| `ImageContent` (url)   | `image`             | —                                           | from `mimeType` if present     | `url.url`                 | URL set as `ref`; inline not populated                            |
| `ImageContent` (base64)| `image`             | base64 data string                          | `mimeType`                     | —                         | Stored inline                                                     |
| `EmbeddedResource` (text blob) | `file`    | resource text content                       | `text/plain` or resource MIME  | —                         | Stored inline when small; large blobs staged to artifact store    |
| `EmbeddedResource` (blob)      | `file`    | —                                           | resource MIME type             | artifact URI              | Staged to artifact store; `ref` set to `lenny-blob://` URI        |
| `EmbeddedResource` (uri)       | `file`    | —                                           | resource MIME type             | resource URI              | `ref` set directly from resource URI                              |
| MCP `isError: true` annotation | `error`   | inherited from enclosing block              | —                              | —                         | `type` overridden to `error`; `annotations.errorCode` populated if present |

Runtime authors who produce output using MCP-familiar content block objects can use the `from_mcp_content()` helper (see below) to perform this translation without manual field mapping.

**Minimum required fields for Basic-level runtimes:** Only `type` and `inline` are required. All other fields (`schemaVersion`, `id`, `mimeType`, `ref`, `annotations`, `parts`, `status`) are optional and have sensible defaults — `schemaVersion` defaults to `1` if absent, `id` is generated by the adapter if absent, `mimeType` defaults to `text/plain` for `type: "text"`, `status` defaults to `complete` for non-streaming responses. A minimal valid `OutputPart` is `{"type": "text", "inline": "hello"}`.

**Simplified text-only response shorthand:** Basic-level runtimes may emit a simplified response form with a top-level `text` field instead of an `output` array:

```json
{ "type": "response", "text": "The answer is 4." }
```

The adapter normalizes this to the canonical form `{"type": "response", "output": [{"type": "text", "inline": "The answer is 4."}]}` before forwarding to the gateway. This shorthand is strictly equivalent — runtimes that need structured output (multiple parts, non-text types, annotations) use the full `output` array form.

**Optional SDK helper `from_mcp_content(blocks)`** converts MCP content blocks to `OutputPart` arrays for runtime authors who want to produce output using familiar MCP formats. Availability:

- **Go:** Ships in the `github.com/lennylabs/runtime-sdk-go/outputpart` sub-package of the Runtime Author SDK ([§15.7](#157-runtime-author-sdks)) (Phase 2 deliverable). Import the package and call `outputpart.FromMCPContent(blocks)`.
- **Other languages:** Not yet published as a library. Use the mapping table above to implement the conversion inline — the logic is a straightforward switch on `content.type`. A copy-paste reference implementation is distributed alongside the runtime adapter specification artifacts ([Section 15.4](#154-runtime-adapter-specification)).
- **No SDK required:** Runtimes can construct `OutputPart` objects directly without any Lenny SDK dependency. The SDK helper is a convenience only.

#### Translation Fidelity Matrix

Each `ExternalProtocolAdapter` translates between `OutputPart` and its wire format. The following matrix documents field-level fidelity for each built-in adapter, plus the REST surface, plus the Post-V1 A2A adapter for forward planning. Round-trip through adapters that mark a field as **`[lossy]`** or **`[dropped]`** is not reversible — callers that require full fidelity should use the REST adapter or persist `OutputPart` directly.

> **Closed-enum contract.** Fidelity classifications below apply only to `OutputPart` fields. Adapter-facing types that carry closed enums — `SessionEventKind`, `TerminationCode`, and the dispatch-filter rule bound to `OutboundCapabilitySet.SupportedEventKinds` — are **not** subject to fidelity degradation: the gateway dispatcher filters by enum membership before calling `Send`, and adapters MUST map each enum value to a well-defined protocol-level state (see "Shared Adapter Types" and "SessionEvent Kind Registry" above). A lossy translation of a closed-enum value in a wire format is an adapter implementation bug, not a modeled degradation.

**Fidelity tag legend:**

| Tag | Meaning |
| --- | --- |
| `[exact]` | Field round-trips with no information loss. |
| `[lossy]` | Field is representable in the target protocol but some information is lost or transformed; the original value cannot be fully reconstructed from the wire form alone. |
| `[dropped]` | Field has no representation in the target protocol and is not present on the wire. A round-trip ingests the field back with a default value. |
| `[unsupported]` | Field semantics are fundamentally incompatible with the target protocol. No mapping attempt is made; the field is silently omitted. Use `protocolHints` to influence fallback behavior. |
| `[extended]` | Field carries richer semantics in the Lenny internal model than the target protocol can represent; extra information is preserved in a protocol extension (annotation, metadata, sidecar) that conformant clients may ignore. |

| `OutputPart` field | MCP | OpenAI Completions | Open Responses | REST | A2A |
| --- | --- | --- | --- | --- | --- |
| `schemaVersion` | **`[dropped]`** — MCP content blocks have no version field; re-added with default on ingest. Round-trip: inbound always reconstructed as version 1. | **`[dropped]`** — not representable; re-added with default on ingest. Round-trip asymmetric: version information permanently lost. | **`[dropped]`** — Responses API output items carry no schema version field; re-added with default on ingest. Round-trip asymmetric: version information permanently lost. | **`[exact]`** | **`[lossy]`** — mapped to A2A `metadata.schemaVersion` string; survives round-trip but as string, not integer. |
| `id` | **`[extended]`** — mapped to MCP `partId` annotation; preserved in extension, ignored by non-Lenny MCP clients. | **`[dropped]`** — no per-content-block ID in Chat Completions. Round-trip: adapter generates new IDs on ingest; original IDs permanently lost. | **`[extended]`** — mapped to Responses API `output[].id`; preserved on outbound and recoverable on inbound for top-level output items. Nested part IDs within composite outputs are not preserved. | **`[exact]`** | **`[exact]`** — mapped to A2A `partId`. |
| `type` | **`[lossy]`** — platform-defined types (see Canonical Type Registry) mapped to nearest MCP block type (`text`, `image`, `resource`); custom types (not in registry) collapsed to `text` with original type preserved in `annotations.originalType`. `reasoning_trace` type has no native MCP representation — collapsed to `TextContent` with `thinking` annotation; round-trip loses semantic typing. | **`[lossy]`** — everything becomes `text` or `image_url`; custom types and `reasoning_trace` collapsed to `text` with no type recovery on round-trip. `thinking` content (from `reasoning_trace` parts) becomes indistinguishable from regular text. | **`[lossy]`** — text, image, and file output types map natively; `reasoning_trace` mapped to `output_text` with a `reasoning` role annotation. Custom types not in the Canonical Type Registry collapse to `output_text` with no type recovery on inbound. | **`[exact]`** | **`[lossy]`** — platform-defined types mapped to A2A part kinds per registry; custom types placed in `metadata.originalType`. `reasoning_trace` → A2A `TextPart` with `metadata.semantic: "reasoning"`; recoverable on ingest if consumer reads `metadata.semantic`. |
| `mimeType` | **`[exact]`** — carried in `resource` or `image` block metadata. | **`[lossy]`** — only `image/*` MIME types preserved via `image_url`; all other MIME types dropped. Non-image blobs become opaque `text` entries with no MIME recovery. | **`[lossy]`** — `image/*` and well-known file MIME types preserved via `output_image` and file output items; uncommon MIME types collapsed to generic file output with no MIME recovery on inbound. | **`[exact]`** | **`[exact]`** — A2A parts carry `mimeType` natively. |
| `inline` | **`[exact]`** | **`[exact]`** (as `content` string or base64 for images) | **`[exact]`** (as `text` or base64-encoded `image` content) | **`[exact]`** | **`[exact]`** |
| `ref` (`lenny-blob://` URI) | **`[dropped]`** — adapters dereference `lenny-blob://` URIs and inline the resolved content before sending to external MCP clients (see SCH-005 resolution protocol). Round-trip: ref scheme permanently lost; content inlined. If blob is expired at send time, the part is replaced with an error part. | **`[dropped]`** — no URI reference in Chat Completions; adapter resolves `ref` to inline before sending. Round-trip: ref scheme permanently lost; content inlined. If blob is expired at send time, the part is replaced with an error part. | **`[dropped]`** — no `lenny-blob://` URI reference in Responses API; adapter resolves `ref` to inline before sending. Round-trip: ref scheme permanently lost; content inlined. If blob is expired at send time, the part is replaced with an error part. | **`[exact]`** — REST clients may dereference via `GET /v1/blobs/{ref}`. | **`[lossy]`** — mapped to A2A `artifact.uri`; `lenny-blob://` scheme rewritten to a gateway-issued HTTPS URL. Scheme is not recoverable from the wire form. |
| `annotations` | **`[lossy]`** — well-known keys (`role`, `final`, `audience`) mapped to MCP annotation fields; unknown keys placed in `metadata` extension if the MCP client negotiated metadata support, otherwise `[dropped]`. Round-trip: unknown annotation keys are lost for clients that do not support MCP metadata extensions. | **`[dropped]`** — no annotation mechanism in Chat Completions. All annotation keys permanently lost on outbound; not recovered on inbound. | **`[dropped]`** — no per-output annotation mechanism in the Open Responses Specification. All annotation keys permanently lost on outbound; not recovered on inbound. | **`[exact]`** | **`[lossy]`** — mapped to A2A `metadata` map; nested objects flattened to JSON strings. Nested structure not recoverable on round-trip. |
| `parts` (nesting) | **`[lossy]`** — flattened to sequential MCP content blocks with `parentId` annotation; one level of nesting reconstructible on ingest if `parentId` is present. Deeper nesting permanently flattened. | **`[dropped]`** — flattened to sequential content entries; nesting structure not recoverable on round-trip. | **`[dropped]`** — Responses API output items are flat; nesting structure not representable and not recoverable on round-trip. | **`[exact]`** | **`[lossy]`** — A2A supports one nesting level via composite parts; deeper nesting flattened. Round-trip: nesting beyond one level permanently lost. |
| `status` | **`[lossy]`** — mapped to MCP streaming progress events; `failed` mapped to `isError`; `streaming` and `complete` distinctions partially recoverable via SSE stream termination signals. Per-part granularity partially preserved. | **`[dropped]`** — Chat Completions has `finish_reason` only at message level; per-part status not representable. | **`[lossy]`** — `failed` status mapped to an output item with `status: "failed"`; `streaming` and `complete` distinctions partially recoverable via SSE streaming events. Per-part status granularity partially preserved; better than Chat Completions but not exact. | **`[exact]`** | **`[lossy]`** — mapped to A2A task state; per-part status granularity lost (only terminal state survives). |
| `protocolHints` | **`[dropped]`** — consumed by adapter before serialization; intentionally not sent on wire. Never recovered on ingest. | **`[dropped]`** — consumed by adapter before serialization. | **`[dropped]`** — consumed by adapter before serialization. | **`[exact]`** | **`[dropped]`** — consumed by adapter before serialization. |

**Round-trip asymmetry summary.** The following fields have asymmetric round-trips (outbound → external protocol → inbound produces a different value than the original):

| Field | Adapter | Asymmetry | Impact |
| --- | --- | --- | --- |
| `schemaVersion` | MCP, OpenAI Completions, Open Responses | Always reconstructed as `1` on inbound regardless of original value | Consumers must not rely on `schemaVersion` surviving an MCP, OpenAI Completions, or Open Responses round-trip |
| `id` | OpenAI Completions | New IDs generated on inbound | Part correlation across an OpenAI Completions round-trip requires application-level tracking |
| `type` (`reasoning_trace`) | MCP, OpenAI Completions, Open Responses | Collapsed to `text`/`TextContent` with annotation; `reasoning_trace` semantic lost in OpenAI Completions; role annotation present but not semantically typed in Open Responses | Agents receiving their own reasoning output via OpenAI Completions cannot distinguish reasoning from regular text; Open Responses consumers must read the role annotation |
| `ref` | MCP, OpenAI Completions, Open Responses | Inlined; scheme lost | Callers that stored a `lenny-blob://` ref cannot recover it after an MCP, OpenAI Completions, or Open Responses round-trip |
| `annotations` (unknown keys) | MCP (no metadata ext.), OpenAI Completions, Open Responses | Unknown keys dropped | Vendor-defined annotations are lost for non-Lenny MCP clients and all OpenAI Completions and Open Responses consumers |

**`protocolHints` annotation field.** `OutputPart.annotations` may include a `protocolHints` key containing adapter-specific directives that influence translation behavior. The gateway adapter reads and removes `protocolHints` before serializing the outbound message — hints never appear on the wire. Structure:

```json
{
  "annotations": {
    "protocolHints": {
      "mcp": { "preferResourceBlock": true },
      "openai": { "collapseToText": false },
      "a2a": { "artifactType": "file" }
    }
  }
}
```

Adapters ignore hint keys they do not recognize. Runtimes that do not set `protocolHints` get default translation behavior as described in the matrix above. Hints are optional and only needed when the default translation is inadequate for a specific use case (e.g., forcing a binary blob to be sent as an MCP resource rather than inline base64).

#### `MessageEnvelope` — Unified Message Format

All inbound **content** messages (type `message`) use a unified `MessageEnvelope` across the stdin binary protocol, platform MCP server tools, and all external APIs. Non-content lifecycle messages (`heartbeat`, `shutdown`, `heartbeat_ack`) use their own minimal schemas and are not `MessageEnvelope` instances — see Protocol Reference below.

```json
{
  "schemaVersion": 1,
  "type": "message",
  "id": "msg_xyz789",
  "from": {
    "kind": "client | agent | system | external",
    "id": "..."
  },
  "inReplyTo": "req_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "delegationDepth": 0,
  "slotId": "slot_01",
  "input": ["OutputPart[]"]
}
```

**`schemaVersion`** — gateway-injected integer (default `1`). Every `MessageEnvelope` persisted to the `session_messages` table carries this field. Runtimes MUST NOT set it; the gateway writes it at inbox-enqueue time and it is immutable once written. Forward-compatibility rules follow the bifurcated consumer model in [Section 15.5](#155-api-versioning-and-stability) item 7: live consumers (streaming adapters, in-flight delivery) MAY reject an unrecognized version but SHOULD forward-read; durable consumers (message DAG readers, audit pipelines) MUST forward-read and preserve unknown fields verbatim.

**`from` object schema — adapter-injected, runtime never supplies these fields:**

`from.kind` is a closed enum with exactly four values. `from.id` format depends on `kind`:

| `kind`     | `id` format           | Description                                                                                                                                                   | Example               |
| ---------- | --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------- |
| `client`   | `client_{opaque}`     | Client-scoped identifier assigned by the gateway at session creation.                                                                                         | `client_8f3a2b`       |
| `agent`    | `sess_{session_id}`   | The session ID of the sending agent. Enables reply routing via `inReplyTo`.                                                                                   | `sess_01J5K9...`      |
| `system`   | `lenny-gateway`       | Always the literal string `lenny-gateway`. Used for platform-injected messages (heartbeats, shutdown, credential rotation notices).                           | `lenny-gateway`       |
| `external` | `conn_{connector_id}` | The registered connector ID from the `ConnectorDefinition`. Used for messages originating from external A2A agents or MCP servers routed through a connector. | `conn_slack_bot_prod` |

The adapter populates `from.kind` and `from.id` from execution context before delivering the message to the runtime. Runtimes MUST NOT set these fields; any runtime-supplied `from` is silently overwritten by the adapter.

**Additional adapter-injected fields:**

- `requestId` in `lenny/request_input` — generated by the gateway; runtime only supplies `parts`

**`slotId`** — optional string; present only in concurrent-workspace mode. Identifies the concurrent slot this message is addressed to. Session-mode and task-mode messages never carry `slotId`. See [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) and the `slotId` multiplexing note in the Protocol Reference.

**`delivery`** — optional closed enum controlling interrupt behaviour. Defined values:

| Value | Meaning | Gateway behaviour |
| --- | --- | --- |
| `"immediate"` | Interrupt the running agent and deliver now. | If session is `running`, the gateway sends an interrupt signal on the lifecycle channel and writes the message to stdin as soon as the runtime emits `interrupt_acknowledged` (Full-level) or immediately after the in-flight stdin write completes (Basic/Standard-level). If session is `suspended`, the gateway atomically resumes (`suspended → running`) then delivers. **Exception — `input_required`:** When the session is in the `input_required` sub-state (blocked in `lenny/request_input`), `delivery: "immediate"` does **not** override path 3 buffering ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)). The runtime is not reading from stdin while blocked in a `request_input` call, so direct delivery is impossible regardless of the `delivery` flag. The message is buffered in the session inbox and delivered in FIFO order once the `request_input` resolves. Receipt: `queued`. For all other `running` sub-states, receipt: `delivered` once the runtime confirms stdin consumption within the delivery timeout (default: 30 seconds). If the runtime does not confirm message consumption within this timeout, the message falls through to inbox buffering (path 5 behavior in [Section 7.2](07_session-lifecycle.md#72-interactive-session-model)) with receipt status `queued`. |
| `"queued"` | Buffer for next natural pause. | Message appended to the session inbox. Delivered in FIFO order when the runtime next enters `ready_for_input`. Receipt: `queued`. |
| absent | Same as `"queued"`. | Default behaviour. |

No other values are valid. The gateway rejects unknown `delivery` values with `400 INVALID_DELIVERY_VALUE`.

**`delivery_receipt` acknowledgement schema.** Every `lenny/send_message` call returns a synchronous `delivery_receipt` object. Senders that require reliable delivery MUST track receipts and re-send on gap detection (see [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) inbox crash-recovery note):

```json
{
  "messageId":   "msg_xyz789",
  "status":      "delivered | queued | dropped | expired | rate_limited | error",
  "reason":      "<string — populated when status is dropped, expired, or rate_limited>",
  "deliveredAt": "<RFC 3339 timestamp — populated when status is delivered>",
  "queueDepth":  "<integer — inbox depth after enqueue; populated when status is queued>"
}
```

`status` values: `delivered` (runtime consumed); `queued` (buffered in inbox); `dropped` (overflow — oldest entry evicted; `reason: "inbox_overflow"` when the session inbox is full, or `reason: "dlq_overflow"` when the dead-letter queue is full); `expired` (DLQ TTL elapsed before delivery); `rate_limited` (inbound rate cap exceeded, [Section 7.2](07_session-lifecycle.md#72-interactive-session-model)); `error` (delivery failed due to infrastructure error, e.g., `reason: "inbox_unavailable"` when Redis is unreachable for durable inbox, or `reason: "scope_denied"` when messaging scope denies the target).

**`delivery_receipt.reason` enum.** When the receipt carries a `reason` field, it is drawn from a canonical closed enum, keyed by the `status` value:

| `status` | `reason` | Emitted when |
| --- | --- | --- |
| `dropped` | `inbox_overflow` | The session inbox reached `maxInboxSize` and the oldest buffered message was evicted. See [§7.2](07_session-lifecycle.md#72-interactive-session-model) session inbox definition. |
| `dropped` | `dlq_overflow` | The dead-letter queue reached `maxDLQSize` and the oldest DLQ entry was evicted. See [§7.2](07_session-lifecycle.md#72-interactive-session-model) dead-letter handling. |
| `error` | `inbox_unavailable` | Durable inbox enqueue failed because Redis was unreachable (`durableInbox: true` deployments only). See [§7.2](07_session-lifecycle.md#72-interactive-session-model) durable inbox prerequisites. |
| `error` | `scope_denied` | The sender's effective `messagingScope` does not permit messaging the target session. Corresponds to the `SCOPE_DENIED` error code ([§15.1](#151-rest-api) error code catalog). |

For `status: "delivered"` and `status: "queued"`, the `reason` field is omitted (the status is self-describing). For `status: "expired"` and `status: "rate_limited"`, v1 does not define additional `reason` enum values — the status alone conveys the condition. No other values are valid. Implementations MUST NOT emit ad-hoc reason strings; new reason values require a spec update.

**`message_expired` event `reason` enum.** When queued messages cannot be delivered and the gateway emits a `message_expired` event to the sender's event stream (see [Section 7.2](07_session-lifecycle.md#72-interactive-session-model)), the `reason` field is drawn from a canonical three-value enum:

| Reason | Emitted when |
| --- | --- |
| `dlq_ttl_expired` | Pre-terminal DLQ TTL elapsed while the target session remained in a recovering state (`resume_pending` or `awaiting_client_action`) and no resume occurred before the TTL boundary. |
| `durable_inbox_ttl_expired` | The durable-inbox `per_message_ttl` elapsed while the target session remained in a recovering state. The durable-inbox trimmer is state-gated — it is a no-op while the session is `running` and activates only during `resume_pending` / `awaiting_client_action`, so this reason is emitted only for messages that were still buffered when the target entered a recovering state and failed to resume before the TTL boundary. See the durable-mode Per-message TTL row in [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |
| `target_terminated` | The target session transitioned to a terminal state (`completed`, `failed`, `cancelled`, `expired`) while messages were still buffered. This covers both the in-memory/durable inbox drain-on-terminal path and the DLQ drain-on-terminal path in [Section 7.2](07_session-lifecycle.md#72-interactive-session-model); senders handle both identically. |

No other values are valid. Implementations MUST NOT emit synonyms (e.g., `session_terminal`, `target_ttl_exceeded`) — these legacy strings were consolidated into the three canonical values above.

**`message_expired` event schema.** The `message_expired` event is delivered asynchronously on the **sender session's event stream** (it is **not** a field on the synchronous `delivery_receipt` returned by `lenny/send_message`). It notifies a sender that a previously-queued message will no longer be delivered. The payload is:

```json
{
  "schemaVersion": 1,
  "type": "message_expired",
  "messageId": "msg_abc123",
  "targetSessionId": "sess_01J5K9...",
  "reason": "dlq_ttl_expired | durable_inbox_ttl_expired | target_terminated",
  "expiredAt": "<RFC 3339 timestamp>"
}
```

| Field | Type | Description |
| --- | --- | --- |
| `schemaVersion` | integer | Event schema revision. Defaults to `1`. Follows the bifurcated forward-compat model in [§15.5](#155-api-versioning-and-stability) item 7: live consumers MAY reject unrecognized versions but SHOULD forward-read; durable consumers MUST forward-read. |
| `type` | string | Always `"message_expired"`. |
| `messageId` | string | The ID of the original message (the `MessageEnvelope.id` the sender received a `queued` receipt for). |
| `targetSessionId` | string | The session ID of the intended target. Populated so senders tracking multiple outstanding messages to different targets can correlate the event without matching on `messageId` alone. |
| `reason` | string | One of the three canonical values defined in the `message_expired` event `reason` enum above. No other values are valid. |
| `expiredAt` | string | RFC 3339 timestamp of when the gateway decided the message had expired (TTL boundary crossed or terminal-state drain performed). Used by senders for correlation and audit. |

The event is persisted to the sender session's event store and replayable within the replay window per the reconnect semantics in [§7.2](07_session-lifecycle.md#72-interactive-session-model). A `message_expired` event is the authoritative asynchronous signal that a previously-queued message will not be delivered — senders MUST NOT infer expiry from any other signal.

**`id`** — every message has a stable ID enabling threading, reply tracking, and non-linear context retrieval. IDs are gateway-assigned ULIDs (`msg_` prefix) when the sender omits them; sender-supplied IDs MUST be globally unique within the tenant or are rejected with `400 DUPLICATE_MESSAGE_ID`. **Deduplication window:** seen IDs are stored in a Redis sorted set (`t:{tenant_id}:session:{session_id}:msg_dedup`, scored by receipt timestamp) and retained for `deduplicationWindowSeconds` (default: 3600s, configurable per deployment via `messaging.deduplicationWindowSeconds` in Helm values). The set is trimmed on each write to remove entries older than the window.

**`inReplyTo`** — optional. If it matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin.

**`threadId`** and `inReplyTo` — DAG conversation model. Messages within a session form a directed acyclic graph (DAG), not a flat list:

- Each message node has: `id` (self), `inReplyTo` (parent edge, optional), `threadId` (thread label, optional).
- In v1 there is one implicit thread per session (`threadId` absent or the same value for all messages). Multi-thread sessions are additive post-v1.
- The gateway records every delivered message in the session's `MessageDAG` store (Postgres `session_messages` table, indexed on `(session_id, id, thread_id)`). Clients may retrieve the DAG via `GET /v1/sessions/{id}/messages` with optional `?threadId=` and `?since=` filters.
- **Ordering guarantee:** Within a single thread, messages are ordered by the coordinator-local sequence number assigned at inbox-enqueue time (a monotonic integer per session, persisted to Postgres). This provides **coordinator-local FIFO** — not global wall-clock order. Cross-sender causal ordering requires application-level sequence numbers or vector clocks embedded in message content.
- **Delegation forwarding:** When a `lenny/send_message` call targets a session in a different delegation tree node, the `delegationDepth` field (integer, 0-based, gateway-injected) records how many tree hops the message crossed. Runtimes MAY inspect `delegationDepth` to detect unexpected cross-tree routing. The field is informational; the gateway does not alter delivery semantics based on it.

**`threadId`** — optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1.

**Future-proof:** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId`, `delivery`, and `delegationDepth` accommodates all future conversational patterns without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

#### Protocol Reference — Message Schemas

All **content** messages on stdin (type `message`) use the full `MessageEnvelope` format ([Section 15.4.1](#1541-adapterbinary-protocol)). Lifecycle messages (`heartbeat`, `shutdown`) use their own minimal schemas defined below and are not `MessageEnvelope` instances. Runtimes MUST ignore unrecognized fields. Basic-level runtimes need only read `type`, `id`, and `input` — all other envelope fields (`from`, `inReplyTo`, `threadId`, `delivery`, `delegationDepth`, `slotId`) can be safely ignored.

##### Inbound: `message`

```json
{
  "type": "message",
  "id": "msg_001",
  "input": [{ "type": "text", "inline": "What is 2+2?" }],
  "from": { "kind": "client", "id": "client_8f3a2b" },
  "threadId": "t_01",
  "delivery": "queued",
  "slotId": "slot_01"
}
```

Basic-level: read `type`, `id`, `input`. Ignore all other fields. `slotId` is optional — present only in concurrent-workspace mode.

##### Inbound: `heartbeat`

```json
{ "type": "heartbeat", "ts": 1717430400 }
```

Agent must respond with `heartbeat_ack` (see below). If no ack within 10 seconds, the adapter considers the process hung and sends SIGTERM.

##### Inbound: `shutdown`

```json
{ "type": "shutdown", "reason": "drain", "deadline_ms": 10000 }
```

Agent must finish current work and exit within `deadline_ms`. No acknowledgment required — the adapter watches for process exit. If the process does not exit by the deadline, the adapter sends SIGTERM, then SIGKILL after 10 seconds.

##### Inbound: `tool_result`

Schema:

```json
{
  "type": "tool_result",
  "id": "<string, required — matches the tool_call.id this result responds to>",
  "content": ["<OutputPart[], required — result content>"],
  "isError": "<boolean, optional — true if tool execution failed; defaults to false>",
  "slotId": "<string, optional — present only in concurrent-workspace mode>"
}
```

Example:

```json
{
  "type": "tool_result",
  "id": "tc_001",
  "content": [{ "type": "text", "inline": "file contents here" }],
  "isError": false
}
```

**Correlation:** Every `tool_result.id` MUST match the `id` of a previously emitted `tool_call`. The adapter validates this — a `tool_result` with an unknown `id` is dropped and logged as a protocol error. Agents may have multiple outstanding `tool_call` requests; results may arrive in any order.

**Delivery semantics:** Tool calls use synchronous request/response semantics within the stdin/stdout channel. The agent emits a `tool_call`, then continues reading stdin until it receives the matching `tool_result` (identified by `id`). Other inbound messages (`heartbeat`, additional `message` content) may arrive before the `tool_result` — the agent must handle interleaved delivery. There is no async callback or webhook mechanism; all tool results are delivered inline on stdin.

**Tool access by level:**

| Level        | Tool access                                                                                                        | `tool_call` / `tool_result` behavior                                                                                                                                                                                                                               |
| ------------ | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Basic**    | No MCP tools available. The agent binary has no platform MCP server or connector MCP servers.                      | Agents MAY still emit `tool_call` for adapter-local tools (e.g., `read_file`, `write_file` provided by the adapter's local sandbox tooling). The adapter resolves these locally and returns `tool_result` on stdin. No platform or connector tools are accessible. |
| **Standard** | Platform MCP server tools (`lenny/delegate_task`, `lenny/request_input`, etc.) and per-connector MCP server tools. | The agent calls MCP tools via the MCP client connection to the adapter's local servers (not via `tool_call` on stdin). The stdin `tool_call`/`tool_result` channel is used for adapter-local tools only.                                                           |
| **Full**     | Same as Standard plus lifecycle channel capabilities.                                                              | Same as Standard.                                                                                                                                                                                                                                                  |

##### Outbound: `response`

```json
{
  "type": "response",
  "output": [{ "type": "text", "inline": "The answer is 4." }],
  "slotId": "<string, optional — present only in concurrent-workspace mode>"
}
```

Basic-level shorthand (adapter normalizes to canonical form above):

```json
{ "type": "response", "text": "The answer is 4." }
```

**Error reporting via `response`.** The `response` message supports an optional `error` field for structured error reporting: `{"code": string, "message": string}`, matching the `TaskResult.error` shape. When `error` is present, the adapter maps the task to `failed` state and populates `TaskResult.error` from the response error. This allows runtimes to report failure details while still delivering partial output in the `output` array, without relying solely on non-zero exit codes (which lose error context). When `error` is absent and the process exits zero, the task completes successfully. When the process exits non-zero without emitting a `response`, the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr.

**Relationship between `lenny/output` and stdout `response`:** At Standard and Full levels, runtimes may emit output parts incrementally via the `lenny/output` platform MCP tool. The stdout `{type: "response"}` message is always required to signal task completion, regardless of whether `lenny/output` was used. Its `output` array contains only parts not already emitted via `lenny/output`; runtimes that have already emitted all output parts via `lenny/output` send an empty `output` array (`[])`. The adapter concatenates `lenny/output` parts (in call order) with the final `response.output` parts to form the complete task output. Basic-level runtimes, which have no access to `lenny/output`, must include all output in the stdout `response.output` array. Standard-level runtimes may use either delivery path or both.

##### Outbound: `tool_call`

Schema:

```json
{
  "type": "tool_call",
  "id": "<string, required — unique call identifier; used to correlate the inbound tool_result>",
  "name": "<string, required — tool name>",
  "arguments": "<object, required — tool-specific parameters; validated by the adapter against the tool's input schema>",
  "slotId": "<string, optional — present only in concurrent-workspace mode>"
}
```

Example:

```json
{
  "type": "tool_call",
  "id": "tc_001",
  "name": "read_file",
  "arguments": { "path": "/workspace/foo.txt" }
}
```

The `id` field is generated by the agent and must be unique within the session. Recommended format: `tc_` prefix with a monotonic counter or random suffix (e.g., `tc_001`, `tc_a7f3b`). The adapter uses this `id` to route the corresponding `tool_result` back on stdin.

**Adapter-Local Tool Reference**

Adapter-local tools are resolved entirely within the adapter process — no MCP server, no platform access, and no network call is required. They are available at all levels (Basic, Standard, Full). The following tools are built into every adapter:

| Tool name     | Description                                                       |
| ------------- | ----------------------------------------------------------------- |
| `read_file`   | Read the contents of a file in the workspace                      |
| `write_file`  | Create or overwrite a file in the workspace                       |
| `list_dir`    | List the entries of a directory in the workspace                  |
| `delete_file` | Delete a file or empty directory from the workspace               |

Discovery: agents discover adapter-local tools by inspecting the `adapterLocalTools` array in the adapter manifest (`/run/lenny/adapter-manifest.json`). Each entry contains the tool `name`, a human-readable `description`, and a JSON Schema `inputSchema` for its `arguments` object. Adapters MUST populate this array before spawning the runtime; the set is fixed for the lifetime of the pod.

Schemas for the four built-in tools:

```json
[
  {
    "name": "read_file",
    "description": "Read the contents of a file in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the file." }
      },
      "required": ["path"]
    }
  },
  {
    "name": "write_file",
    "description": "Create or overwrite a file in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path":    { "type": "string", "description": "Workspace-relative or absolute path to the file." },
        "content": { "type": "string", "description": "UTF-8 text content to write." }
      },
      "required": ["path", "content"]
    }
  },
  {
    "name": "list_dir",
    "description": "List the entries of a directory in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the directory." }
      },
      "required": ["path"]
    }
  },
  {
    "name": "delete_file",
    "description": "Delete a file or empty directory from the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the target." }
      },
      "required": ["path"]
    }
  }
]
```

All `read_file` / `write_file` / `list_dir` / `delete_file` calls are confined to the pod's workspace volume (`/workspace`). The adapter rejects any path that resolves outside `/workspace` with a `tool_result` carrying `isError: true` and `content[0].inline` set to the string `"path_outside_workspace"`. Custom adapters MAY extend this list with additional adapter-local tools; they MUST declare all custom tools in `adapterLocalTools` before spawning the runtime.

##### Outbound: `heartbeat_ack`

```json
{ "type": "heartbeat_ack" }
```

##### Outbound: `status` (optional)

```json
{ "type": "status", "state": "thinking", "message": "Analyzing code..." }
```

**Exit Codes**

| Code | Meaning                                                            |
| ---- | ------------------------------------------------------------------ |
| 0    | Normal completion — session ended cleanly or shutdown honored      |
| 1    | Runtime error — adapter logs stderr and reports failure to gateway |
| 2    | Protocol error — agent could not parse inbound messages            |
| 137  | SIGKILL (set by OS) — adapter treats as crash, pod is not reused   |

Any non-zero exit during an active session causes the gateway to report a session error to the client. During draining, exit code 0 confirms graceful shutdown; non-zero triggers an alert but the session result (if any) is still delivered.

**Annotated Protocol Trace — Basic-Level Session**

```
1. Adapter starts agent binary, stdin/stdout pipes open.
2. Adapter writes to stdin:
   {"type": "message", "id": "msg_001", "input": [{"type": "text", "inline": "Hello"}], "from": {"kind": "client", "id": "client_8f3a2b"}, "threadId": "t_01"}
3. Agent reads line from stdin, parses JSON, reads type/id/input (ignores other fields).
4. Agent writes to stdout (either form is valid):
   {"type": "response", "text": "Echo: Hello"}
   — or equivalently —
   {"type": "response", "output": [{"type": "text", "inline": "Echo: Hello"}]}
5. Adapter reads line from stdout, delivers response to gateway.
6. [Heartbeat interval] Adapter writes:
   {"type": "heartbeat", "ts": 1717430410}
7. Agent writes:
   {"type": "heartbeat_ack"}
8. Gateway initiates shutdown. Adapter writes:
   {"type": "shutdown", "reason": "drain", "deadline_ms": 10000}
9. Agent finishes, exits with code 0.
10. Adapter reports clean termination to gateway.
```

#### 15.4.2 RPC Lifecycle State Machine

The adapter follows a well-defined state machine:

```
INIT ──→ READY ──→ ACTIVE ──→ DRAINING ──→ TERMINATED
                     │                          ▲
                     └──────────────────────────┘
                       (session ends normally)
```

| State        | Description                                                                                           |
| ------------ | ----------------------------------------------------------------------------------------------------- |
| `INIT`       | Adapter process starts, opens gRPC connection to gateway (mTLS), writes placeholder manifest. The adapter sends an `AdapterInit` message on the control stream with `adapterProtocolVersion` (semver string, e.g., `"1.0.0"`). The gateway responds with `AdapterInitAck` carrying `selectedVersion` (the highest compatible version the gateway supports) or closes the stream with `PROTOCOL_VERSION_INCOMPATIBLE` if no compatible version exists. Major version changes are breaking; minor/patch are backwards compatible. Current protocol version: `"1.0.0"`. |
| `READY`      | Adapter signals readiness. Pod enters warm pool. Gateway may now assign sessions.                     |
| `ACTIVE`     | A session is in progress. Adapter manages MCP servers, lifecycle channel, and stdin/stdout relay.     |
| `DRAINING`   | Graceful shutdown requested. The adapter finishes the current exchange and signals the agent to stop. |
| `TERMINATED` | The adapter has exited. The gateway marks the pod as no longer available.                             |

Transitions are initiated by either the gateway (e.g., session assignment, drain request) or the adapter itself (e.g., readiness signal, exit on completion).

#### 15.4.3 Runtime Integration Levels

To lower the barrier for third-party runtime authors, the spec defines three integration levels (for `type: agent` runtimes only):

**Basic** — enough to get a custom runtime working:

- stdin/stdout binary protocol only
- Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout
- Must handle `{type: "heartbeat"}` by responding with `{type: "heartbeat_ack"}` — failure to ack within 10 seconds causes SIGTERM
- Must handle `{type: "shutdown"}` by exiting within the specified `deadline_ms`
- Zero Lenny knowledge required beyond the above message types
- No checkpoint/restore support, no detailed health reporting

**Standard** — minimum plus MCP integration:

- Connects to adapter's platform MCP server and connector servers via the adapter manifest
- Uses platform capabilities (delegation, discovery, output parts, elicitation)
- Standard MCP — no Lenny-specific code

**Standard-Level MCP Integration**

Standard-level runtimes connect to the adapter's local MCP servers as a standard MCP client. The following details apply:

- **Transport.** All intra-pod MCP servers use **abstract Unix sockets** exclusively (names listed in the adapter manifest, e.g., `@lenny-platform-mcp`, `@lenny-connector-github`). There is no stdio transport for intra-pod MCP — stdio is reserved for the binary protocol between the adapter and the runtime. **Platform compatibility note:** Abstract Unix sockets (names beginning with `@`) are a Linux kernel feature and are **not supported on macOS**. Standard- and Full-level runtime development therefore requires a Linux environment. The recommended approach for macOS developers is to use `docker compose up` ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) Compose Mode), which runs the adapter inside a Linux container. `make run` ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) Source Mode) supports macOS for Basic-level runtimes only, since Basic level uses the stdin/stdout binary protocol exclusively and does not open any Unix sockets.
- **Protocol version.** The adapter's local MCP servers speak **MCP 2025-03-26** (the platform's target MCP spec version; see [Section 15.2](#152-mcp-api) for version negotiation details). The local servers also accept **MCP 2024-11-05** for backward compatibility. Intra-pod MCP version support follows the same rolling two-version policy as the gateway ([Section 15.5](#155-api-versioning-and-stability) item 2): the oldest accepted version enters a 6-month deprecation window when a new MCP spec version is adopted, and removal applies only to new connection negotiations (active sessions on the deprecated version are not forcibly terminated).
- **Client libraries.** Runtime authors should use an existing MCP client library for their language (e.g., `mcp-go` for Go, `@modelcontextprotocol/sdk` for TypeScript/Node.js, `mcp` for Python). These libraries work against the adapter's local servers with one Lenny-specific addition: the runtime must send the manifest nonce as the first message of the MCP `initialize` handshake (see Authentication below).
- **Tool discovery.** The runtime calls `tools/list` on each MCP server (platform and connectors) to discover available tools. The platform MCP server exposes the tools listed in Part A of this section (e.g., `lenny/delegate_task`, `lenny/output`). Each connector server exposes that connector's tools.
- **Authentication.** Intra-pod MCP connections require a manifest-nonce handshake, identical in mechanism to the lifecycle channel handshake ([Section 4.7](04_system-components.md#47-runtime-adapter), item 1). The adapter writes a random nonce into the adapter manifest (`/run/lenny/adapter-manifest.json`, read-only to the agent container) before spawning the runtime. The runtime must present this nonce as the first message of the MCP `initialize` handshake on each MCP connection (platform MCP server and every connector MCP server). The adapter rejects — with an immediate close — any MCP connection that does not present a valid nonce before dispatching tools. This prevents any process that has not read the manifest from connecting to a privileged MCP server, regardless of its UID. The nonce is stored in the manifest under the top-level key `mcpNonce` (a random 256-bit hex string, regenerated per task execution alongside the rest of the manifest).

  **Nonce wire format (v1 — intra-pod only).** The nonce is a Lenny-private convention for intra-pod MCP connections only; it does not appear on any external-facing MCP endpoint and is not part of the MCP specification. The canonical injection location is the top-level `_lennyNonce` field in the MCP `initialize` request's `params` object:
  ```json
  {
    "method": "initialize",
    "params": {
      "_lennyNonce": "<nonce_hex>",
      "clientInfo": {
        "name": "my-agent",
        "version": "1.0.0"
      },
      "protocolVersion": "2025-03-26"
    }
  }
  ```
  The adapter validates the `_lennyNonce` value against the manifest's `mcpNonce` field before processing any tool dispatch. The nonce must be the hex-encoded 256-bit value exactly as written in the manifest; no normalization or encoding is applied. After successful validation, the adapter **strips** the `_lennyNonce` field from `params` before dispatching the `initialize` request to its internal MCP server implementation, ensuring the MCP server never sees the non-standard field. This stripping is required because the adapter's MCP server validates `initialize` params against the MCP schema, which does not include `_lennyNonce`.

  > **Strict MCP client libraries.** Some MCP client libraries enforce schema validation on outgoing requests and may reject the `_lennyNonce` field in `params`. Runtime authors using such libraries should either (a) add `_lennyNonce` to the `initialize` params after the library constructs the request but before it is serialized to the socket, or (b) disable outbound schema validation for the `initialize` call only. The adapter accepts the field regardless of its position relative to other `params` keys.

  > **Deprecated location.** Earlier adapter versions also accepted `_lennyNonce` inside `params.clientInfo.extensions`. That location is no longer canonical and will not be checked in adapter manifest `version: 2`. Runtime authors MUST use `params._lennyNonce` (top-level in `params`).

  > **Migration path — v2 out-of-band handshake.** Injecting authentication material into MCP `initialize` parameters is a stopgap. Adapter manifest `version: 2` will replace this with a pre-`initialize` out-of-band handshake: the runtime sends a single JSON line `{"type":"lenny_nonce","nonce":"<nonce_hex>"}` on the socket before the MCP `initialize` exchange, keeping the nonce entirely outside the MCP message stream. The `params._lennyNonce` field will be supported (but ignored) in `version: 2` for a two-release backward-compat window; runtimes should migrate to the pre-initialize message at `version: 2` adoption.

**Full** — standard plus lifecycle channel:

- Opens the lifecycle channel for operational signals
- True session continuity, clean interrupt points, mid-session credential rotation
- `DRAINING` state with graceful shutdown coordination
- Checkpoint/restore support

**Level Comparison Matrix**

The following matrix enumerates every level-sensitive capability with its behavior at each integration level. Capabilities marked "N/A" are not available and have no fallback.

| Capability                                                                 | Basic                                                                                                                                         | Standard                                                                                                                                   | Full                                                                                                                                            |
| -------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| **stdin/stdout binary protocol**                                           | Yes                                                                                                                                           | Yes                                                                                                                                        | Yes                                                                                                                                             |
| **Heartbeat / shutdown handling**                                          | Yes                                                                                                                                           | Yes                                                                                                                                        | Yes                                                                                                                                             |
| **Platform MCP server** (delegation, discovery, elicitation, output parts) | N/A — runtime operates without platform tools                                                                                                 | Yes                                                                                                                                        | Yes                                                                                                                                             |
| **Connector MCP servers**                                                  | N/A — no connector access                                                                                                                     | Yes                                                                                                                                        | Yes                                                                                                                                             |
| **Lifecycle channel**                                                      | N/A — operates in fallback-only mode                                                                                                          | N/A — operates in fallback-only mode                                                                                                       | Yes                                                                                                                                             |
| **Checkpoint / restore**                                                   | No checkpoint support; pod failure loses in-flight context. Gateway restarts session from last gateway-persisted state.                       | Best-effort snapshot without runtime pause (`consistency: best-effort`). Minor workspace inconsistencies possible on resume ([Section 4.4](04_system-components.md#44-event--checkpoint-store)). | Consistent checkpoint with runtime pause via lifecycle channel `checkpoint_request` / `checkpoint_ready`.                                       |
| **Interrupt**                                                              | No clean interrupt. Gateway sends SIGTERM; runtime has no opportunity to reach a safe stop point.                                             | No clean interrupt. Same SIGTERM-based termination as Basic.                                                                               | Clean interrupt via `interrupt_request` on lifecycle channel; runtime acknowledges with `interrupt_acknowledged` and reaches a safe stop point. |
| **Credential rotation**                                                    | Checkpoint → pod restart → `AssignCredentials` with new lease → `Resume`. If checkpoint unsupported, in-flight context is lost ([Section 4.7](04_system-components.md#47-runtime-adapter)). | Checkpoint → pod restart → `AssignCredentials` with new lease → `Resume`. Brief session pause; client sees reconnect.                      | In-place rotation via `RotateCredentials` RPC and `credentials_rotated` lifecycle message. No session interruption.                             |
| **Deadline / expiry warning**                                              | No advance warning. `DEADLINE_APPROACHING` signal requires lifecycle channel; Basic-level receives only `shutdown` at expiry.                | No advance warning. Same as Basic — no lifecycle channel to deliver `DEADLINE_APPROACHING`.                                                | `DEADLINE_APPROACHING` signal delivered on lifecycle channel before session expiry ([Section 10](10_gateway-internals.md)).                                                |
| **Graceful drain (`DRAINING` state)**                                      | No drain coordination. Adapter sends `shutdown` with `deadline_ms`; SIGTERM on timeout.                                                       | No drain coordination. Same as Basic.                                                                                                      | `DRAINING` state via lifecycle channel enables graceful shutdown coordination before `shutdown`.                                                |
| **Task mode pod reuse**                                                    | No pod reuse. Adapter sends `shutdown` on stdin after task; pod replaced from warm pool. Effectively `maxTasksPerPod: 1` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).       | No pod reuse. Same as Basic — no lifecycle channel for between-task signaling.                                                             | Full pod reuse via `task_complete` / `task_complete_acknowledged` / `task_ready` on lifecycle channel. Scrub + reuse cycle as described in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). |
| **Simplified response shorthand** (`{type: "response", text: "..."}`)      | Yes — adapter normalizes to canonical `OutputPart` form ([Section 15.4.1](#1541-adapterbinary-protocol)).                                                                     | Yes — available but typically unused since Standard runtimes produce structured output.                                                    | Yes — available but typically unused.                                                                                                           |
| **OutputPart minimal fields**                                              | Only `type` and `inline` required; all other fields optional with defaults ([Section 15.4.1](#1541-adapterbinary-protocol)).                                                  | Full `OutputPart` schema available.                                                                                                        | Full `OutputPart` schema available.                                                                                                             |
| **MessageEnvelope fields**                                                 | Only `type`, `id`, `input` needed; all other envelope fields safely ignored ([Section 15.4.1](#1541-adapterbinary-protocol)).                                                 | Full envelope including `from`, `inReplyTo`, `threadId`, `delivery`.                                                                       | Full envelope including `from`, `inReplyTo`, `threadId`, `delivery`.                                                                            |

> **Basic-level limitations — complete list:**
> Basic-level runtimes operate without the lifecycle channel and without platform MCP server access. The following capabilities are **unavailable** at Basic level and have no fallback:
>
> - **Checkpoint / restore:** Pod failure loses all in-flight context. The gateway restarts the session from the last gateway-persisted state; any unsaved intermediate work is gone.
> - **Clean interrupt:** No opportunity for the runtime to reach a safe stop point. The gateway issues `shutdown` on stdin and follows with SIGTERM after `deadline_ms`; the runtime cannot acknowledge an interrupt cleanly.
> - **Credential rotation without disruption:** Rotation requires a full pod restart (checkpoint → restart → `AssignCredentials` → `Resume`). If the runtime does not support checkpoint, the in-flight context is lost during rotation.
> - **Delegation (`lenny/delegate_task`):** Requires the platform MCP server, which is unavailable at Basic level. Basic-level runtimes cannot spawn sub-tasks.
> - **Platform MCP tools** (including `lenny/output`, `lenny/request_input`, `lenny/discover_agents`): All platform-side tools are inaccessible. Runtimes must produce all output via the stdout binary protocol.
> - **Connector MCP servers:** No connector (GitHub, filesystem, etc.) tool access.
> - **`DEADLINE_APPROACHING` warning:** Requires the lifecycle channel. Basic-level runtimes receive only the `shutdown` message at expiry with no advance notice.
> - **Graceful drain (`DRAINING` state):** No drain coordination signal. Shutdown is `shutdown`-on-stdin followed by SIGTERM.
> - **Inter-session messaging (`lenny/send_message`):** Requires the platform MCP server. Basic-level runtimes cannot send messages to other sessions or participate in sibling coordination patterns.
> - **Input-required blocking (`lenny/request_input`):** Requires the platform MCP server. Basic-level runtimes cannot request clarification from a parent or client mid-task. `one_shot` Basic-level runtimes must produce their response based solely on the initial input.
>
> These limitations are intentional — Basic level prioritizes simplicity and zero Lenny knowledge. Runtime authors who need any of the above must adopt Standard or Full level.

Third-party authors should start with a basic adapter and incrementally adopt standard and full features as needed.

#### 15.4.4 Sample Echo Runtime

The project includes a reference **`echo-runtime`** — a trivial agent binary that echoes back messages with a sequence number. It serves two purposes:

1. **Platform testing:** Validates the full session lifecycle (pod claim → workspace setup → message → response → teardown) without requiring a real agent runtime or LLM credentials.
2. **Template for custom runtimes:** Demonstrates the stdin/stdout JSON Lines protocol, heartbeat handling, and graceful shutdown — the minimal contract a custom agent binary must implement.

> **Runnable implementation (Phase 2 deliverable):** A fully runnable Go implementation of this echo runtime will be published at `examples/runtimes/echo/` in the repository. It compiles to a single static binary, requires no external dependencies, and can be registered with a local `lenny-dev` instance using `make run`. Runtime authors are encouraged to use it as a baseline when debugging their own adapter setups — if the echo runtime responds correctly, the platform is configured correctly. The pseudocode below serves as a readable summary of the logic; the Go source in `examples/runtimes/echo/` is the authoritative runnable reference.

```
Pseudocode (Basic-level):

    seq = 0
    while line = read_line(stdin):
        msg = json_parse(line)
        switch msg.type:
            case "message":
                seq += 1
                // Basic-level shorthand — the adapter normalizes this to the
                // canonical OutputPart form before forwarding. Use the full
                // `output: [{type: "text", inline: "..."}]` form only when
                // structured output (multiple parts, non-text types,
                // annotations) is required.
                write_line(stdout, json({
                    "type": "response",
                    "text": "echo [seq={seq}]: {msg.input[0].inline}"
                }))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "heartbeat":
                write_line(stdout, json({"type": "heartbeat_ack"}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "shutdown":
                exit(0)
            default:
                // ignore unknown types for forward compatibility
    exit(0)
```

The samples below show the incremental additions required when advancing to Standard or Full level. They assume the Basic-level loop above as their base.

```
Pseudocode (Standard-level addition — nonce + MCP):

    // --- Startup: read adapter manifest and authenticate to local MCP servers ---
    manifest = json_parse(read_file("/run/lenny/adapter-manifest.json"))
    nonce    = manifest.mcpNonce     // 256-bit hex string, regenerated each task

    // Connect to platform MCP server (Unix socket, abstract namespace)
    platform_mcp = mcp_client_connect(manifest.platformMcpServer.socket)

    // Present nonce in MCP initialize params (top-level _lennyNonce field).
    // The adapter validates this before dispatching any tool call.
    platform_mcp.send({
        "method": "initialize",
        "params": {
            "_lennyNonce": nonce,
            "clientInfo": {"name": "my-runtime", "version": "1.0.0"},
            "protocolVersion": "2025-03-26"
        }
    })
    platform_mcp.recv()   // wait for initialize response

    // Optionally connect to each connector MCP server with the same nonce
    for server in manifest.connectorServers:
        conn = mcp_client_connect(server.socket)
        conn.send({"method": "initialize", "params": {
            "_lennyNonce": nonce,
            "clientInfo": {"name": "my-runtime", "version": "1.0.0"},
            "protocolVersion": "2025-03-26"
        }})
        conn.recv()   // wait for initialize response

    // Discover available tools (call tools/list on each connected server)
    tools = platform_mcp.call("tools/list", {})

    // --- Main loop (same as Basic-level, plus MCP tool invocation) ---
    seq = 0
    while line = read_line(stdin):
        msg = json_parse(line)
        switch msg.type:
            case "message":
                seq += 1
                // Invoke a platform tool via MCP instead of a bare echo
                result = platform_mcp.call("lenny/output", {
                    "output": [{"type": "text",
                                "inline": "echo [seq={seq}]: {msg.input[0].inline}"}]
                })
                write_line(stdout, json({"type": "response", "output": []}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "heartbeat":
                write_line(stdout, json({"type": "heartbeat_ack"}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "shutdown":
                platform_mcp.close()
                exit(0)
            default:
                // ignore unknown types for forward compatibility
    exit(0)
```

```
Pseudocode (Full-level addition — lifecycle channel):

    // --- Startup: same manifest read and MCP setup as Standard-level ---
    manifest     = json_parse(read_file("/run/lenny/adapter-manifest.json"))
    nonce        = manifest.mcpNonce
    platform_mcp = mcp_client_connect(manifest.platformMcpServer.socket)
    platform_mcp.send({"method": "initialize", "params": {
        "_lennyNonce": nonce,
        "clientInfo": {"name": "my-runtime", "version": "1.0.0"},
        "protocolVersion": "2025-03-26"
    }})
    platform_mcp.recv()

    // --- Lifecycle channel setup ---
    // Connect to the lifecycle channel (Full-level runtimes only).
    // The socket path is advertised in the manifest; opening it is optional
    // but required for checkpoint, clean interrupt, and credential rotation.
    lc = unix_connect(manifest.lifecycleChannel.socket)  // @lenny-lifecycle

    // Capability negotiation: adapter sends lifecycle_capabilities first.
    cap_msg = json_parse(lc.recv_line())   // type: "lifecycle_capabilities"
    assert cap_msg.type == "lifecycle_capabilities"

    // Declare which capabilities this runtime supports (subset of offered).
    supported = ["checkpoint", "interrupt", "deadline_signal"]   // omit credential_rotation if unused
    lc.send_line(json({"type": "lifecycle_support", "capabilities": supported}))

    // --- Background goroutine: handle lifecycle signals concurrently ---
    spawn background:
        while lc_line = lc.recv_line():
            lc_msg = json_parse(lc_line)
            switch lc_msg.type:
                case "checkpoint_request":
                    // Quiesce: finish current output, flush buffers
                    quiesce_state()
                    lc.send_line(json({
                        "type": "checkpoint_ready",
                        "checkpointId": lc_msg.checkpointId
                    }))
                    // Wait for checkpoint_complete before resuming
                    cc = json_parse(lc.recv_line())
                    assert cc.type == "checkpoint_complete"
                    resume_state()

                case "interrupt_request":
                    // Reach a safe stop point, then acknowledge
                    reach_safe_stop_point()
                    lc.send_line(json({
                        "type": "interrupt_acknowledged",
                        "interruptId": lc_msg.interruptId
                    }))

                case "credentials_rotated":
                    // Reload credentials from the new path and rebind
                    reload_credentials(lc_msg.credentialsPath)
                    lc.send_line(json({
                        "type": "credentials_acknowledged",
                        "leaseId": lc_msg.leaseId,
                        "provider": lc_msg.provider
                    }))

                case "deadline_approaching":
                    // Wrap up long-running work before forced termination
                    begin_graceful_wrap_up(lc_msg.remainingMs)

                case "terminate":
                    // Ordered shutdown — exit within deadlineMs
                    cleanup_and_exit(0)

                default:
                    // ignore unknown lifecycle messages for forward compatibility

    // --- Main loop (same as Standard-level) ---
    seq = 0
    while line = read_line(stdin):
        msg = json_parse(line)
        switch msg.type:
            case "message":
                seq += 1
                result = platform_mcp.call("lenny/output", {
                    "output": [{"type": "text",
                                "inline": "echo [seq={seq}]: {msg.input[0].inline}"}]
                })
                write_line(stdout, json({"type": "response", "output": []}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "heartbeat":
                write_line(stdout, json({"type": "heartbeat_ack"}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "shutdown":
                // shutdown arrives on stdin even for Full-level; lifecycle terminate
                // may arrive first — handle whichever comes first
                platform_mcp.close()
                lc.close()
                exit(0)
            default:
                // ignore unknown types for forward compatibility
    exit(0)
```

#### 15.4.5 Runtime Author Roadmap

Runtime-author information is distributed across this specification. The following reading order provides a guided path from first build to production-ready adapter, organized by integration level.

**Basic-level (get a runtime working):**

1. **[Section 15.4.4](#1544-sample-echo-runtime)** — Sample Echo Runtime. Copy this pseudocode as your starting point.
2. **[Section 15.4.1](#1541-adapterbinary-protocol)** — Adapter↔Binary Protocol. The stdin/stdout JSON Lines contract, message types, `OutputPart` format, and simplified response shorthand.
3. **[Section 15.4.2](#1542-rpc-lifecycle-state-machine)** — RPC Lifecycle State Machine. Read for context: the adapter (not your binary) owns this state machine. Knowing it helps you understand when your binary will start receiving messages (`ACTIVE`), and that `shutdown` arrives only during `DRAINING` — your binary never drives these transitions.
4. **[Section 15.4.3](#1543-runtime-integration-levels)** — Runtime Integration Levels. Level definitions and the capability comparison matrix — confirms what Basic-level runtimes can skip.
5. **[Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout)** — Pod Filesystem Layout. Where your binary's working directory, workspace, and scratch space live (`/workspace/current/`, `/tmp/`, `/artifacts/`).
6. **[Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)** — Local Development Mode (`lenny-dev`). Use `lenny up` (Embedded Mode, the primary path for runtime authors — exercises the real Kubernetes code path via the embedded stack and registers your runtime through the production admin API) or `make run` (Source Mode, for platform contributors who need to modify the gateway or controller source alongside their runtime).

**Standard-level (add MCP integration):**

7. **[Section 4.7](04_system-components.md#47-runtime-adapter)** — Runtime Adapter. Read for the **adapter manifest field reference** (`platformMcpServer.socket`, `connectorServers`, `mcpNonce`). The lifecycle channel message schemas (Part B) are Full-level only — skip for Standard level. The gRPC RPC table at the top of 4.7 is the gateway↔adapter contract and is not relevant to binary authors.
8. **[Section 9.1](09_mcp-integration.md#91-where-mcp-is-used)** — MCP Integration. How the platform MCP server and connector MCP servers are exposed to your runtime.
9. **[Section 8.2](08_recursive-delegation.md#82-delegation-mechanism)** — Delegation Mechanism. How `lenny/delegate_task` works if your runtime delegates sub-tasks.
10. **[Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)** — Runtime. Runtime definition schema (`type`, `capabilities`, `baseRuntime`), registration via admin API.

**Full-level (lifecycle channel and production hardening):**

11. **[Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)** — Pool Configuration and Execution Modes. Execution modes (session, task, concurrent-workspace), resource classes, and pool sizing.
12. **[Section 7.1](07_session-lifecycle.md#71-normal-flow)** — Session Lifecycle Normal Flow. End-to-end session flow from pod claim through teardown.
13. **[Section 13.1](13_security-model.md#131-pod-security)–13.2** — Pod Security and Network Isolation. Security constraints your runtime operates under (seccomp, gVisor, egress rules).
14. **[Section 14](14_workspace-plan-schema.md)** — Workspace Plan Schema. How workspace sources are declared and materialized before your binary starts.
15. **[Section 15.5](#155-api-versioning-and-stability)** — API Versioning and Stability. Versioning guarantees for the adapter protocol.
16. **[Section 15.4.6](#1546-conformance-test-suite)** — Conformance Test Suite. How `lenny runtime validate` exercises your runtime against the level it claims.

#### 15.4.6 Conformance Test Suite

Every runtime repository — first-party reference runtimes in [§26](26_reference-runtime-catalog.md) and third-party runtimes alike — declares an **integration level** (Basic, Standard, or Full) and is exercised by a conformance test suite that verifies the claims specific to that level. "Conformance level" in runtime metadata and [§26.1](26_reference-runtime-catalog.md#261-catalog-overview) is defined as equal to the **Integration Level** from [§15.4.3](#1543-runtime-integration-levels) — Basic, Standard, or Full. There is no separate conformance taxonomy.

**Entry point.** `lenny runtime validate [<path>]` ([§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding)) is the canonical entry point. It reads `runtime.yaml`'s optional `integrationLevel` field ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)) — defaulting to `basic` if absent — to discover the **declared** integration level and executes the corresponding test categories below against a locally-built image or binary. The command exits `0` on a full pass and non-zero with a structured failure report otherwise. CI for first-party reference runtimes ([§26.1](26_reference-runtime-catalog.md#261-catalog-overview)) invokes `lenny runtime validate` on every push and fails the release if any test category regresses.

**Declared vs. observed level reconciliation.** In addition to running the declared level's test categories, the validator probes the running runtime to determine its **observed** level:

1. Start the runtime against the fake adapter with the full Full-level fixture set available (lifecycle channel listening, platform MCP server and connector fake reachable, manifest written).
2. If the runtime connects to `@lenny-lifecycle` and completes the `lifecycle_capabilities` / `lifecycle_support` exchange within a 10 s grace window, observed level is at least `full`.
3. Else if the runtime connects to `@lenny-platform-mcp` and presents a valid `_lennyNonce` during MCP `initialize`, observed level is at least `standard`.
4. Otherwise observed level is `basic`.

The validator then reports:

- **Observed == declared.** Exit `0`; report `"integrationLevel": { "declared": "<x>", "observed": "<x>", "status": "match" }`.
- **Observed > declared** (author under-declared). Exit `0` but print a WARN and set `"status": "underdeclared"`. Suggested remediation: raise `integrationLevel` in `runtime.yaml` to match observed behaviour so that callers and admission can rely on the higher level.
- **Observed < declared** (runtime under-performs its claim). Exit **non-zero** with structured error `runtime_level_underperforms`: `{ "declared": "<x>", "observed": "<y>", "missing": [<capability names not exercised at observed level>] }`. The runtime does not meet the contract it has published; conformance tests for the missing level's categories are reported as failed.

The observed-level probe is the local-tooling counterpart of the registration-time admission check described in [§5.1](05_runtime-registry-and-pool-model.md#51-runtime) `integrationLevel` documentation; both compare declared against the `lifecycle_support` handshake from [§4.7](04_system-components.md#47-runtime-adapter).

**Test categories by integration level.** Each higher level inherits every test category from the levels below it.

| Integration level | Test category | What it asserts |
|---|---|---|
| **Basic** | **stdin/stdout protocol framing** | The binary reads newline-delimited JSON from stdin and writes newline-delimited JSON to stdout; every outbound message is flushed before the next `read_line` call ([§15.4.1](#1541-adapterbinary-protocol) stdout flushing requirement); unknown inbound `type` values are ignored rather than aborting. |
| **Basic** | **`message` / `response` round-trip** | A canonical `{type: "message", input: [...]}` produces a structurally valid `{type: "response", ...}` — either full form with `output: OutputPart[]` or Basic-level shorthand `{"type": "response", "text": "..."}`. The response matches `schemas/lenny-adapter-jsonl.schema.json`. |
| **Basic** | **heartbeat ack** | Within 10 s of receiving `{type: "heartbeat"}`, the binary writes `{type: "heartbeat_ack"}` to stdout. Failure to ack within the window triggers the adapter's unresponsive-agent escalation ([§4.7](04_system-components.md#47-runtime-adapter)). |
| **Basic** | **shutdown within `deadline_ms`** | On `{type: "shutdown", "deadline_ms": N}`, the binary exits cleanly before the deadline elapses (tested with `N = 5000`). Failing this test means the adapter will SIGKILL the process in production, losing any unflushed output. |
| **Basic** | **`OutputPart` schema compliance** | Every `OutputPart` produced by the runtime validates against `schemas/outputpart.schema.json`, including the canonical type registry and the `x-<vendor>/` namespace convention for custom types. |
| **Standard** | **MCP nonce handshake** | On startup, the runtime reads `/run/lenny/adapter-manifest.json`, connects to the platform MCP server, and presents `_lennyNonce` in the `initialize` params. The adapter's fake MCP server rejects any tool call without a valid nonce to verify enforcement. |
| **Standard** | **platform MCP tool invocation** | The runtime successfully calls at least `lenny/output` and `lenny/request_input` via the MCP client. Responses are processed and forwarded through the stdin/stdout channel where applicable. |
| **Standard** | **connector MCP server reachability** | If `manifest.connectorServers` is non-empty, the runtime connects to each with the same nonce and completes the `initialize` handshake. Test uses two fake connector servers. |
| **Standard** | **`tool_call` / `tool_result` correlation** | Adapter-local `tool_call` emissions carry a unique `id` and the corresponding `tool_result` is read from stdin before the runtime emits its final `response`. |
| **Full** | **lifecycle channel opening** | The runtime connects to the lifecycle channel advertised in the manifest (`@lenny-lifecycle` abstract Unix socket) and completes the `lifecycle_capabilities` / `lifecycle_support` exchange. |
| **Full** | **checkpoint quiesce/resume** | On `checkpoint_request`, the runtime quiesces output, replies with `checkpoint_ready`, waits for `checkpoint_complete`, and resumes. Verified via fake-adapter fixture that times the quiesce window. |
| **Full** | **interrupt acknowledgement** | On `interrupt_request`, the runtime reaches a safe stop point and replies with `interrupt_acknowledged` carrying the original `interruptId` within the deadline. |
| **Full** | **credential rotation handling** | If the runtime declares `credential_rotation` support, it successfully re-reads refreshed credentials from the manifest or env on `credential_rotated` and continues to service the next message without restart. |
| **Full** | **deadline signal handling** | On `deadline_signal`, the runtime writes a final `response` (possibly with `error.code: "DEADLINE_EXCEEDED"`) and exits cleanly before the deadline elapses. |

**How the suite is packaged.** The conformance fixtures (fake adapter, fake MCP server, fake connector servers, sample manifests, reference manifest JSON Schemas) ship inside the `lenny` binary as assets of the `lenny runtime validate` subcommand. No additional download is required. The fixtures are versioned with the Runtime Adapter Specification ([§15.4](#154-runtime-adapter-specification)); each Lenny release pins the fixture version that its `lenny runtime validate` executes against. Third-party runtime authors can pin a specific `lenny-ctl` version to stabilize the conformance surface, and can run `lenny runtime validate --report <path>` to emit a machine-readable JSON report for inclusion in release artifacts.

**Failure categorization.** Each test failure is classified as one of: `schema_violation` (message or `OutputPart` did not match the published JSON Schema), `timeout` (ack/shutdown/deadline exceeded), `missing_capability` (runtime claims a level whose required categories are not exercised), or `unexpected_error` (the runtime wrote to stderr or exited non-zero outside a tested failure path). The report lists each failing category with the classification and a reproduction command.

### 15.5 API Versioning and Stability

Community contributors and integrators need clear guarantees about which APIs are stable and how breaking changes are managed. Each external surface follows its own versioning scheme:

1. **REST API:** Versioned via URL path prefix (`/v1/`). Breaking changes require a new version (`/v2/`). Non-breaking additions (new fields, new endpoints) are added to the current version. The previous version is supported for at least 6 months after a new version ships.

2. **MCP tools:** Versioned via the MCP protocol's capability negotiation (see [Section 15.2](#152-mcp-api) for target version and negotiation details). The gateway supports two concurrent MCP spec versions (current + previous) with a 6-month deprecation window for the oldest. Tool schemas can add optional fields without a version bump. Removing or renaming fields, or changing semantics, is a breaking change.

3. **Runtime adapter protocol:** Versioned independently (see [Section 15.4](#154-runtime-adapter-specification)). The adapter advertises a protocol version at INIT; the gateway selects a compatible version. Major version changes are breaking.

4. **CRDs:** All Lenny CRDs (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`) ship initially at **`v1alpha1`** and follow the graduation path `v1alpha1` → `v1beta1` → `v1`. Graduation criteria: `v1alpha1` → `v1beta1` requires Phase 2 benchmark completion and no breaking field changes for 60 days; `v1beta1` → `v1` requires GA load-test sign-off (Phase 14.5) and no breaking changes for 6 months.

   **Conversion webhook deployment.** Multi-version coexistence during upgrades depends on a running conversion webhook. The conversion webhook (`lenny-crd-conversion`) must be deployed **before** adding a new served version to any CRD — the API server begins routing conversion requests to the webhook as soon as `spec.conversion.strategy: Webhook` is set, and a missing webhook causes all CRD operations to fail. The deployment procedure for each version graduation is:
   1. Deploy the `lenny-crd-conversion` Deployment (from `charts/lenny/templates/conversion-webhook.yaml`) and wait for its pods to reach `Ready` state.
   2. Verify the webhook Service and `spec.conversion.webhook.clientConfig` in each CRD are correctly referencing the `lenny-crd-conversion` Service before applying the updated CRD. Run `kubectl get svc -n lenny-system lenny-crd-conversion` to confirm the Service exists.
   3. Apply the updated CRD manifests (`kubectl apply -f charts/lenny/crds/`). The `lenny-preflight` Job validates conversion webhook availability as a preflight check and will fail the upgrade if the webhook Service is absent or not ready.
   4. Confirm `kubectl get crd <name> -o jsonpath='{.spec.versions[*].name}'` lists both the old and new version as served.
   5. Migrate stored objects to the new storage version using `kubectl get <resource> -A --output=yaml | kubectl apply -f -` (re-apply triggers conversion and storage migration). Monitor `apiserver_crd_webhook_conversion_duration_seconds` for conversion latency.
   6. Once all stored objects are migrated, remove the old version from the `served: true` list and update `storage: true` to the new version in the CRD spec.

   Conversion webhooks are deployed with `replicas: 2` and `PodDisruptionBudget minAvailable: 1`. The `lenny-preflight` Job checks webhook availability as part of every upgrade. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy) for the full CRD upgrade procedure.

5. **Definition of "breaking change":** Removing a field, changing a field's type, changing the default behavior of an existing feature, removing an endpoint/tool, or changing error codes for existing operations.

6. **Stability tiers:**
   - `stable`: Covered by versioning guarantees above.
   - `beta`: May change between minor releases with deprecation notice.
   - `alpha`: May change without notice.

7. **Schema versioning — bifurcated consumer rules.** All Postgres-persisted record types carry a `schemaVersion` integer field (starting at `1`) that identifies the schema revision used to write the record. This applies to: `TaskRecord` ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)), billing events ([Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream)), audit events (`EventStore`), checkpoint metadata ([Section 7.1](07_session-lifecycle.md#71-normal-flow)), session records ([Section 7](07_session-lifecycle.md)), `WorkspacePlan` ([Section 14](14_workspace-plan-schema.md)), and `MessageEnvelope` ([Section 15.4.1](#1541-adapterbinary-protocol), persisted in the `session_messages` table). The field is set at write time by the gateway and is immutable once written.

   The forward-compatibility rules differ between **live (streaming) consumers** and **durable (persisted) consumers**:

   **Live consumers** (streaming sessions, real-time adapters, in-memory event handlers):
   - **MAY reject** an unrecognized `schemaVersion` — but SHOULD forward-read (process known fields, surface a `schema_version_ahead` degradation signal) unless the unrecognized version introduces semantically incompatible fields that make silent partial processing dangerous.
   - When a live consumer chooses to forward-read, it MUST surface a `schema_version_ahead` annotation on the enclosing `MessageEnvelope` (fields: `knownVersion`, `encounteredVersion`) so the caller is informed of potential incompleteness.
   - Rationale: live consumers are transient — a rejection causes only a session failure that can be retried with an updated consumer. Silently dropping unknown fields in a live context is acceptable when degradation is signalled.

   **Degradation annotation catalog.** The following degradation annotations MAY appear on a `MessageEnvelope` and are distinct, non-aliasing signals — observability dashboards, SLO alerts, and forward-compat rollout tracking MUST treat them as separate kinds:

   | Annotation                      | Direction                        | Trigger                                                                                                                                             | Fields                                             |
   | ------------------------------- | -------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------- |
   | `schema_version_ahead`          | New writer → old reader          | A consumer encounters a `schemaVersion` (on any persisted or streamed record type listed above, including nested `OutputPart`) higher than it understands and forward-reads. | `knownVersion`, `encounteredVersion`               |
   | `durable_schema_version_ahead`  | New writer → old reader (durable)| A durable consumer cannot safely pass through unknown fields on a record with a `schemaVersion` it does not recognize; the record is queued for manual review. | `knownVersion`, `encounteredVersion`, `recordType` |
   | `mcp_protocol_version_retired`  | Old writer → new reader          | An active MCP session whose negotiated `protocolVersion` has been removed from the gateway is forced to terminate by a post-handler-removal defect ([Section 15.2](#152-mcp-api), "Session-lifetime exception for deprecated versions"). | `retiredVersion`, `currentVersions`                |

   These annotations MUST NOT be conflated: `schema_version_ahead` and `durable_schema_version_ahead` track forward-compat drift on the `schemaVersion` field of records, while `mcp_protocol_version_retired` tracks retirement of the negotiated MCP protocol version of a still-running connection (the inverse direction).

   **Durable consumers** (billing processors, audit log readers, analytics pipelines, compliance exporters):
   - **MUST forward-read** records with unrecognized `schemaVersion`. Rejection at read time creates compliance gaps: billing records retained for 13 months and audit events retained for regulatory periods MUST remain readable even when the consumer binary has not yet been upgraded.
   - Durable consumers MUST process all fields they understand and preserve all unknown fields verbatim (pass-through). If a durable consumer cannot safely pass through unknown fields (e.g., it writes to a schema-strict sink), it MUST emit a `durable_schema_version_ahead` structured error to an operator alert channel and queue the record for manual review rather than dropping it.
   - Durable consumers MUST NOT silently discard records based solely on an unrecognized `schemaVersion`.
   - **Migration window SLA:** When a new `schemaVersion` is introduced, all durable consumers MUST be upgraded to understand the new version within **90 days** of the version's release. After 90 days, the previous schema version may be retired from active write paths, but persisted records at the old version remain readable for the full retention period of each record type.

   **Reader code** uses `schemaVersion` to select the correct deserialization path, enabling rolling schema migrations without downtime. **This durable-consumer forward-read rule extends to `OutputPart` arrays nested within `TaskRecord`:** if any `OutputPart` in a persisted `TaskRecord` carries a `schemaVersion` a durable consumer does not recognize, the consumer MUST forward-read (preserving unknown fields verbatim) rather than rejecting the record or silently dropping unrecognized fields — silent data loss in billing or audit records is unacceptable (see [Section 15.4.1](#1541-adapterbinary-protocol), "Consumer obligation — durable storage (TaskRecord)").

### 15.6 Client SDKs

Lenny provides official client SDKs for **Go** and **TypeScript/JavaScript** as part of the v1 deliverables. SDKs encapsulate session lifecycle management, MCP streaming with automatic reconnect-with-cursor, file upload multipart handling, webhook signature verification, and error handling with retries — logic that is complex and error-prone to re-implement from the protocol specs alone.

**Package names and publication.**

| Language | Package | Repository |
|---|---|---|
| Go | `github.com/lennylabs/client-sdk-go` | `github.com/lennylabs/client-sdk-go` |
| TypeScript / JavaScript | `@lennylabs/client-sdk` (npm) | `github.com/lennylabs/client-sdk-ts` |

These Client SDK packages are distinct from the Runtime Author SDK packages listed in [§15.7](#157-runtime-author-sdks) (`runtime-sdk-go`, `lenny-runtime`, `@lennylabs/runtime-sdk`). Never mix them: Client SDKs target callers of a running Lenny deployment; Runtime Author SDKs target code that runs inside an agent pod.

SDKs are generated from the OpenAPI spec (REST) and MCP tool schemas, with hand-written streaming and reconnect logic layered on top. Community SDKs for other languages can build on the published OpenAPI spec and the MCP protocol specification.

Client SDKs follow the same versioning scheme as the API surfaces they wrap ([Section 15.5](#155-api-versioning-and-stability)): SDK major versions track REST API versions, and SDK releases note any MCP tool schema changes.

> **Client SDKs vs Runtime Author SDKs.** The SDKs described in this subsection target **clients** of a running Lenny deployment — code that creates sessions and consumes their output. The complementary surface for **runtime authors** (code that runs inside an agent pod, talks to the gateway over the Runtime Adapter Specification from [§15.4](#154-runtime-adapter-specification), and packages a runtime image for the platform) is documented in [§15.7](#157-runtime-author-sdks) as a separate set of libraries and scaffolding tools.

### 15.7 Runtime Author SDKs

Runtime authors build a new agent image that plugs into Lenny by implementing the [Runtime Adapter Specification](#154-runtime-adapter-specification). To make that contract approachable without requiring every author to re-derive the stdin/stdout line protocol, abstract-Unix-socket wire format, and manifest-nonce handshake from the spec, Lenny ships **first-party Runtime Author SDKs** in Go, Python, and TypeScript, plus the `lenny runtime init` scaffolding CLI.

**Package names and publication.**

| Language | Package | Repository |
|---|---|---|
| Go | `github.com/lennylabs/runtime-sdk-go` | `github.com/lennylabs/runtime-sdk-go` |
| Python | `lenny-runtime` (PyPI) | `github.com/lennylabs/runtime-sdk-python` |
| TypeScript / JavaScript | `@lennylabs/runtime-sdk` (npm) | `github.com/lennylabs/runtime-sdk-ts` |

All three SDKs are Apache-2.0 licensed and versioned in lockstep with the Runtime Adapter Specification version from [§15.4](#154-runtime-adapter-specification) (`lenny.runtime.protocol_version`).

**What the SDKs provide.**

- **Protocol codec.** Wire-level helpers for every transport the runtime binary touches, scoped by integration level ([§15.4.3](#1543-runtime-integration-levels)):
    - **Binary protocol (all levels).** Line-delimited JSON (JSON Lines) framing and readline over **stdin/stdout** per [§15.4.1](#1541-adapterbinary-protocol), including the stdout-flushing requirement. This is the entire Basic-level wire surface.
    - **Intra-pod abstract Unix sockets (Standard adds MCP; Full adds lifecycle).** Dial helpers for the Linux abstract-namespace sockets advertised in the adapter manifest (`/run/lenny/adapter-manifest.json`, [§4.7](04_system-components.md#47-runtime-adapter)): `@lenny-platform-mcp` (Standard: platform MCP proxy), `@lenny-connector-<id>` (Standard: per-connector MCP servers), and `@lenny-lifecycle` (Full: agent-side lifecycle channel). There is no `@lenny-<pod_id>-ctl` or equivalent catch-all control socket — every intra-pod channel is purpose-specific.
    - **Intra-pod authentication.** The manifest-nonce handshake described in [§15.4.3](#1543-runtime-integration-levels) (injected as `params._lennyNonce` on the MCP `initialize` request and on the lifecycle channel), paired with the adapter-side `SO_PEERCRED` UID check from [§4.7](04_system-components.md#47-runtime-adapter). The runtime process does **not** participate in mTLS and is never issued a gateway certificate; mTLS is exclusively an adapter↔gateway transport concern ([§4.7](04_system-components.md#47-runtime-adapter) "internal gRPC/HTTP+mTLS API"). SDKs read the nonce from the manifest and attach it automatically.
    - **Credential delivery.** Read-only access patterns for `/run/lenny/credentials.json` (present under both proxy and direct delivery modes per [§4.7](04_system-components.md#47-runtime-adapter) manifest `llm` fields), including the rebind-on-`credentials_rotated` loop for Full-level runtimes and the env-var export (`llm.apiKeyEnv`) for proxy mode.
    - **Graceful shutdown.** SIGTERM handling and the `terminate` / `shutdown` deadline contract from [§4.7](04_system-components.md#47-runtime-adapter) and [§15.4.1](#1541-adapterbinary-protocol), plus `task_complete` / `task_ready` handling on the lifecycle channel for Full-level task-mode runtimes.
    - **RPC vocabulary.** The `lenny.runtime.*` request/response vocabulary carried over the transports above.
- **Platform MCP tool helpers.** Typed helpers for the platform MCP tool set defined in [§4.7](04_system-components.md#47-runtime-adapter): `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`, and `lenny/set_tracing_context`. [§4.7](04_system-components.md#47-runtime-adapter) is authoritative for the platform MCP tool set; this list tracks it. Note that `tool_call` is a stdin/stdout adapter protocol frame ([§15.4.1](#1541-adapterbinary-protocol)), not an MCP tool; `interrupt` is a gateway-initiated lifecycle signal ([§4.7](04_system-components.md#47-runtime-adapter) lifecycle), not an MCP tool; and there is no `lenny/ready` on the public surface.
- **Credential access.** A thin wrapper around the credential-lease refresh loop (Proxy mode credential header injection, Direct mode env-var refresh), including the lease-renewal retry schedule from [§4.9](04_system-components.md#49-credential-leasing-service).
- **Workspace utilities.** Helpers for materializing files into `/workspace`, respecting the workspace plan ([§14](14_workspace-plan-schema.md)), and uploading checkpoints ([§7.1](07_session-lifecycle.md#71-normal-flow) seal-and-export).
- **Telemetry.** Prometheus counters for request counts, errors, and latencies, and OpenTelemetry spans wrapping each request.
- **Test doubles.** In-process gateway fake for unit testing; a CI-friendly `runtime-sdk testserver` binary that speaks the gateway-side protocol against a runtime binary for integration tests.

**API surface (Go).**

```go
package runtime

// Handler is the single interface runtime authors implement.
type Handler interface {
    OnCreate(ctx context.Context, req CreateRequest) error
    OnMessage(ctx context.Context, msg Message) (Reply, error)
    OnTerminate(ctx context.Context, reason TerminationReason) error
}

// Run wires up stdin/stdout framing, dials the manifest-advertised
// abstract Unix sockets (platform MCP, connector MCP, lifecycle channel)
// with the manifest-nonce handshake, refreshes credentials from
// /run/lenny/credentials.json, and drives the lenny.runtime.* dispatch
// loop. Blocks until the adapter closes stdin or sends `terminate`.
func Run(h Handler, opts ...Option) error
```

**SDK Handler types.** `CreateRequest`, `Message`, and `Reply` are convenience wrappers materialized by the SDK from the lower-level wire contracts already defined in this spec: the adapter manifest ([§4.7](04_system-components.md#47-runtime-adapter)), the `AssignCredentials`/`StartSession` RPCs ([§4.7](04_system-components.md#47-runtime-adapter)), the `MessageEnvelope` ([§15.4.1](#1541-adapterbinary-protocol) "`MessageEnvelope` — Unified Message Format"), and the `OutputPart` format ([§15.4.1](#1541-adapterbinary-protocol) "Internal `OutputPart` Format"). They do not introduce new wire types — the SDK parses the manifest, stdin framing, and credential file into these structs before invoking the `Handler` methods. Python and TypeScript SDKs expose structurally equivalent types (idiomatic names per language).

```go
// CreateRequest is the snapshot of task-scoped context handed to
// Handler.OnCreate before the first Message is delivered on stdin. The SDK
// assembles this value from (a) the adapter manifest written to
// /run/lenny/adapter-manifest.json before the runtime binary is spawned
// ([§4.7](04_system-components.md#47-runtime-adapter)), (b) the credential
// file written by AssignCredentials at /run/lenny/credentials.json
// ([§4.7](04_system-components.md#47-runtime-adapter) item 4), and (c) the
// StartSession RPC parameters the gateway forwarded to the adapter (see the
// Startup Sequence in [§4.7](04_system-components.md#47-runtime-adapter)).
// Handler implementations MUST treat CreateRequest as read-only — the wire
// sources are authoritative and the SDK will refresh derived fields (notably
// Credentials) in place on rotation events without re-invoking OnCreate.
type CreateRequest struct {
    // SessionID is the session this runtime instance is bound to. Matches
    // `sessionId` in the adapter manifest and `SessionMetadata.SessionID`
    // ([§15 Shared Adapter Types](#shared-adapter-types)).
    SessionID string `json:"sessionId"`

    // TaskID is the current task identifier. Matches `taskId` in the
    // adapter manifest. In task-mode pools ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes))
    // the SDK calls OnCreate again with a new TaskID after each
    // `task_ready` lifecycle signal; in session-mode pools TaskID equals
    // the root task ID.
    TaskID string `json:"taskId"`

    // RuntimeOptions is the effective options map passed by the caller in
    // the CreateSessionRequest `runtimeOptions` field ([§14](14_workspace-plan-schema.md)),
    // validated against the runtime's `runtimeOptionsSchema`
    // ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)). Keys and
    // value shapes are runtime-defined; the SDK performs no additional
    // validation beyond the schema enforced at the gateway.
    RuntimeOptions map[string]any `json:"runtimeOptions,omitempty"`

    // WorkspacePlan is a reference to the materialized workspace plan for
    // this task ([§14](14_workspace-plan-schema.md)). The files and
    // directories described here have already been staged under
    // `/workspace/current` (or the per-slot path in concurrent-workspace
    // mode) before OnCreate is invoked; runtimes typically read this value
    // for metadata (sources, setup commands actually run) rather than to
    // drive materialization themselves.
    WorkspacePlan *WorkspacePlan `json:"workspacePlan,omitempty"`

    // Credentials is the current credential bundle delivered via
    // AssignCredentials and materialized at /run/lenny/credentials.json
    // ([§4.7](04_system-components.md#47-runtime-adapter) item 4 — Runtime
    // credential file contract). The SDK parses the file into this value;
    // on `credentials_rotated` lifecycle messages the SDK re-reads the file
    // and updates this pointer in place (Full-level runtimes) rather than
    // calling OnCreate again. Nil only when the runtime's provider pool has
    // no active lease (matches `llm: null` in the manifest).
    Credentials *CredentialBundle `json:"credentials,omitempty"`

    // ManifestSnapshot is the parsed adapter manifest
    // ([§4.7](04_system-components.md#47-runtime-adapter) "Adapter manifest
    // field reference"). Authors MAY consult it for platform MCP socket,
    // lifecycle channel socket, connector servers, experiment context, and
    // tracing context. The SDK has already dialed the advertised sockets
    // and attached the `mcpNonce` before OnCreate is invoked; authors who
    // only use SDK-provided MCP helpers do not need to read this field
    // directly.
    ManifestSnapshot *AdapterManifest `json:"manifestSnapshot,omitempty"`
}

// Message is the per-turn envelope handed to Handler.OnMessage for every
// `{type: "message"}` frame the adapter writes to stdin
// ([§15.4.1](#1541-adapterbinary-protocol) "Inbound: `message`"). It wraps
// the canonical MessageEnvelope with the session/task IDs the SDK resolved
// from the adapter manifest, so Handler implementations do not have to
// correlate against the manifest on every turn. Fields other than Envelope
// are SDK-derived conveniences — the wire contract is in §15.4.1.
type Message struct {
    // Envelope is the canonical MessageEnvelope as defined in
    // [§15.4.1](#1541-adapterbinary-protocol) "`MessageEnvelope` — Unified
    // Message Format". All message semantics (from, delivery, threadId,
    // inReplyTo, delegationDepth, slotId, input OutputPart[]) live on this
    // field. Basic-level handlers typically only read `Envelope.Input`.
    Envelope *MessageEnvelope `json:"envelope"`

    // SessionID is the session the message was delivered to. Populated
    // from `sessionId` in the adapter manifest; equals
    // CreateRequest.SessionID.
    SessionID string `json:"sessionId"`

    // TaskID is the active task the message belongs to. Populated from
    // `taskId` in the adapter manifest; equals CreateRequest.TaskID at the
    // time OnMessage is invoked.
    TaskID string `json:"taskId"`

    // Sequence is a monotonically increasing, SDK-assigned, per-task
    // counter that orders messages as the SDK observed them on stdin.
    // Distinct from `MessageEnvelope.id` (which is globally unique) and
    // from the coordinator-local sequence number persisted server-side
    // ([§15.4.1](#1541-adapterbinary-protocol) "Ordering guarantee"):
    // Sequence is a local per-process counter suitable for logging and
    // in-handler ordering only.
    Sequence uint64 `json:"sequence"`

    // Metadata is an optional SDK-scoped map for pass-through annotations
    // (e.g., trace-injected headers, dev-mode debug flags). Empty by
    // default; contents are not forwarded on the wire.
    Metadata map[string]string `json:"metadata,omitempty"`
}

// Reply is the value Handler.OnMessage returns to the SDK. The SDK
// serializes it into the stdout `{type: "response"}` frame defined in
// [§15.4.1](#1541-adapterbinary-protocol) "Outbound: `response`" — Parts
// becomes `output`, and the Streaming / Final flags drive how the SDK
// reconciles incremental output delivered via the `lenny/output` platform
// MCP tool ([§4.7](04_system-components.md#47-runtime-adapter) Part A) with
// the final response frame.
type Reply struct {
    // Parts is the OutputPart array the runtime emits for this turn. The
    // SDK places it verbatim into `response.output` after validating each
    // entry against the OutputPart schema
    // ([§15.4.1](#1541-adapterbinary-protocol) "Internal `OutputPart`
    // Format"). Nil or empty is valid when the runtime has already emitted
    // all output via `lenny/output` (Standard/Full levels).
    Parts []OutputPart `json:"parts,omitempty"`

    // Streaming indicates additional Parts may still arrive out-of-band on
    // the OutboundChannel or via subsequent `lenny/output` calls before
    // the turn is final. When Streaming is true, the SDK defers marking
    // the task complete until it observes a later Reply with Final=true
    // (or an error). When Streaming is false, the SDK treats Parts as the
    // complete output for this turn.
    Streaming bool `json:"streaming,omitempty"`

    // Final marks this Reply as the terminal response for the current
    // turn. When Final is true, the SDK emits the stdout response frame
    // and closes any streaming state. Final MUST be true for Basic-level
    // runtimes, which have no out-of-band output path; Standard/Full-level
    // runtimes MAY emit interim Replies with Final=false to flush
    // partial state to the SDK without closing the turn.
    Final bool `json:"final,omitempty"`
}
```

Python and TypeScript SDKs expose an equivalent `Handler` protocol/interface and an equivalent `run()` entrypoint. `CreateRequest`, `Message`, and `Reply` are surfaced as idiomatic language types (e.g., `@dataclass` in Python, `interface` in TypeScript) with the same field set and the same JSON tag mapping above.

**`lenny runtime init` scaffolder.** The [§24](24_lenny-ctl-command-reference.md#24-lenny-ctl-command-reference) CLI includes a `lenny runtime init` subcommand that generates a new runtime skeleton (`<runtime>/`): `Dockerfile`, `main.<lang>` using the SDK's `Handler` interface (except for `--language binary --template minimal`, which emits a no-SDK stdin/stdout skeleton; see [§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding)), `runtime.yaml` ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)), a `Makefile` with `build`, `test`, and `push` targets, and a CI workflow that publishes images to `ghcr.io/<org>/runtime-<name>`. Supported templates: Go, Python, TypeScript, and the generic stdin/stdout binary template.

**Versioning and stability.** SDK releases follow semver. A breaking change to the Runtime Adapter Specification bumps SDK major versions in lockstep; backward-compatible additions (new optional methods) bump the minor version. The gateway honors the protocol version negotiation from [§15.4.1](#1541-adapterbinary-protocol) so that a runtime built against an older SDK continues to work against a newer gateway within the same major version.

**Relationship to reference runtime catalog.** Every reference runtime in [§26](26_reference-runtime-catalog.md) — `claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, `crewai` — is built on top of the Runtime Author SDKs above, using the scaffolder output as the starting point. Third-party runtimes get the same code path at release time: `lenny runtime init <name>` produces a repository identical in structure to the first-party reference runtimes.

