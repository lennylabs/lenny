# Spec changes — Gateway replicas have no inter-replica request-forwarding carrier

## Design (as the spec must state it)

A replica that serves a session-scoped client request for a session another replica coordinates forwards the request to that replica over `CH-HOLDERFORWARD`, a new HTTP channel that replaces the `LNK-INTERREPLICA` link entry. §29.3 **Forwarding.** rule 2 (SPEC-5) states what the replica forwards and what it returns.

The forward target comes from one authority, the address in the `REG-COORDLEASE` holder value. SPEC-2 states the format and the dial bound in §10.1.1 **Replica address:**, and SPEC-1 points the register row at it.

The forwarding rules have one normative home, the numbered **Forwarding.** rules that SPEC-5 writes into §29.3. They cover when a replica forwards, what it forwards, the coordinator's authentication and authorization, the never-forward-twice rule, the receiving replica's lease check, and the two failure classes (not delivered, outcome unknown). The §28.5.4 card (SPEC-3), the §28.6 and §28.8 entries (SPEC-4), and the §10.3 peer-validation paragraph (SPEC-7) cite those rules.

The message fallback moves from the serving replica's in-memory inbox to the session DLQ, which the coordinator reads. It applies only to a message carrying no `inReplyTo` whose forward was not delivered. It reuses the existing §7.2 delivery path 6 bullets and adds no §7.2 text. A message carrying `inReplyTo` fails closed (summary D10). The interaction-resolution rows stop forwarding and are restated as store-and-wake.

The §11.4 binding-release fan-out on the Redis pub/sub channel `lenny:session:terminate` is registered as `CH-BINDRELEASE` on the `gateway-to-store` boundary (SPEC-1, SPEC-4). The §12.8 drain publishes on it (SPEC-6).

`ForwardMessage` was never a channel identifier, so the naming table gets no row for it. Its spec occurrences are deleted by SPEC-1, SPEC-3, and SPEC-8.

## Edge cases and accepted failure modes

The staged text owns each case below. The rule that decides each one is cited rather than restated.

- A forward reaches a replica that lost the lease after the serving replica read the holder value. §29.3 **Forwarding.** rule 5 decides it.
- A coordinator loses the lease after its own lease check. The pod rejects it on the generation stamp (§29.3 **Forwarding.** rule 5, §10.1.1 **Generation counters:**).
- A forward fails after a request byte may have been written. §29.3 **Forwarding.** rule 7 decides it: 409 and no DLQ hold, for every kind.
- A not-delivered message waits in the DLQ for the next delivery point. When the coordinator is reachable but receives no further message, the entry waits for the idle suspension or **Backlog resume.** (§29.3 **Forwarding.** rule 6, citing §7.2 **Delivery of held messages.**). This is accepted.
- A restarted replica with a pinned replica identity writes a new holder value. Until its previous process's leases expire, it reads them as held by another replica (§10.1.1 **Replica address:**).
- In dev mode, a gateway with no certificate material does not forward, and every forward takes the not-delivered outcome (§10.3 NET-072).
- A call on a WebSocket connection whose bearer has expired since the upgrade is forwarded to the coordinator. §29.3 **Forwarding.** rule 3 and §27.3.1 **Silent refresh.** (SPEC-5 (g)) decide it.
- A dropped `CH-BINDRELEASE` publish is not redelivered (§28.5.7 `CH-BINDRELEASE` **Degradation.**).

## Staged edits

### SPEC-1 · spec/28_communication-channels.md § 28.3 Registers

(a) In the **Link register**, delete the row that begins "| `LNK-INTERREPLICA` | Gateway replica and gateway replica |".

(b) Delete the paragraph that begins "`LNK-INTERREPLICA` carries no channel row, because the specification states the connection" and ends "rather than as an absent transport in this register."

(c) In the **Channel register**, insert the following row immediately after the row that begins "| `CH-OBJSTORE` |". The row stays one physical line.

```markdown
| `CH-HOLDERFORWARD` | None | `inter-replica` | Content | Forwarding replica | Forwarding replica | HTTP | Off-holder session requests forwarded to the session's coordinating replica | C19 |
```

(d) In the **Channel register**, insert the following row immediately after the row that begins "| `CH-EVENTRELAY` |". The row stays one physical line.

```markdown
| `CH-BINDRELEASE` | None | `gateway-to-store` | Control | Gateway | Gateway | Redis | Binding-release requests for terminal sessions, fanned out to every gateway replica | None |
```

(e) In the **Register-entry register**, in the row that begins "| `REG-COORDLEASE` |", replace the Store cell "Redis" with `Redis, and Postgres during a Redis outage ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Fallback:**)`, and replace the Semantics cell "One holder per tenant and session, on a compare-and-set with a 60 second expiry" with the text below. The Key cell is unchanged, and the row stays one physical line.

```markdown
One holder per tenant and session, on a compare-and-set with a 60 second expiry. [§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Replica address:** states the holder value
```

The **Naming table** gets no row. `ForwardMessage` was never a channel identifier, and `CH-HOLDERFORWARD` and `CH-BINDRELEASE` are new.

### SPEC-2 · spec/10_gateway-internals.md § 10.1.1 Stateless Replicas and Per-Session Coordination, § 10.1.8 Rolling Updates and the CheckpointBarrier Protocol

(a) In **Per-session coordination:**, in the bullet that begins "- **Fallback:** If Redis is unavailable, replicas acquire coordination rights using `SELECT ... FOR UPDATE SKIP LOCKED` on the session row in Postgres.", insert the following sentence immediately after the sentence that ends "so it survives PgBouncer connection recycling without risk of silent lock release.":

```markdown
The fallback records the lease holder and its expiry in a Postgres lease row keyed by tenant and session, under the same holder value, so `REG-COORDLEASE` reads return the holder during a Redis outage.
```

(b) Insert the following bullet immediately after the bullet that begins "- **Primary:** Redis-based distributed lease (`SET NX` with TTL)" and before the bullet that begins "- **Fallback:**":

```markdown
- **Replica address:** Each replica's `REG-COORDLEASE` holder value is `<replica identity>.<process token>@<address>`. `<process token>` is a random value the replica draws once at process start, so a restarted replica writes a new holder value even when its replica identity is pinned, and it acquires the leases its previous process held only after they expire. `<address>` is `<pod IP>:<gateway.holderForwardPort>`, and an IPv6 pod IP is written in brackets. The replica writes this value as its holder in the Redis lease and in the Postgres fallback, and as the `coordinator_replica` of its `REG-COORDMIRROR` rows. It decides whether it coordinates a session by comparing the holder value it reads with its own. A replica that does not coordinate a session reaches the coordinating replica at the `<address>` of the holder value over `CH-HOLDERFORWARD` ([§28.5.4](28_communication-channels.md#2854-inter-replica)). The forwarding replica bounds the TCP dial and the TLS handshake together at 2 seconds. When the holder value is absent or unreadable, or carries no address, the coordinating replica is unreachable, and the forward takes the not-delivered outcome of [§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 6. A replica does not read the `REG-COORDMIRROR` projection to find the coordinating replica, because the Sweeper is that projection's only writer and it lags the lease.
```

(c) In §10.1.8, in the list item that begins "1. **Barrier signal:**", replace `WHERE coordinator_replica = $this_replica_id AND released_at IS NULL` with `WHERE coordinator_replica = $this_holder_value AND released_at IS NULL`.

### SPEC-3 · spec/28_communication-channels.md § 28.5.4 Inter-replica

(a) Replace the lead paragraph, which begins "This boundary carries the channels between two gateway replicas. The §28.3 channel register places no channel on this boundary, so this subsection carries no card." and ends "The specification does not state the address that connection is reached at (§28.3).", with:

```markdown
This boundary carries the channels between two gateway replicas. The §28.3 channel register places one
channel on this boundary, `CH-HOLDERFORWARD`, between a forwarding gateway replica and a session's
coordinating gateway replica, and it carries `None` in its Link column, so its own register row together
with the endpoint stated in its card describes the connection.
```

(b) Replace the ASCII diagram fenced block that follows the lead paragraph, including its caption lines, with:

```
  forwarding replica                                 coordinating replica
  +----------------------+                    +----------------------+
  |                      |  CH-HOLDERFORWARD  |                      |
  |   gateway replica    |                    |   gateway replica    |
  |                      |        =====>      |                      |
  +----------------------+                    +----------------------+

  CH-HOLDERFORWARD: HTTP over mutual TLS, dialled by the forwarding replica,
  at the address in the coordinating replica's REG-COORDLEASE holder value,
  TCP gateway.holderForwardPort.
```

(c) Replace the paragraph that begins "The conversation the connection carries is the internal gRPC `ForwardMessage` RPC used for cross-replica message routing." and ends "This subsection gains a card at the point the §28.3 channel register places a channel on this boundary." with the following card:

```markdown
**`CH-HOLDERFORWARD`**

- **Link.** `None` (§28.3).
- **Endpoint.** The `<address>` part of the coordinating replica's `REG-COORDLEASE` holder value, which is
  its pod IP and TCP `gateway.holderForwardPort`
  ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Replica address:**).
  Both ends present the gateway replica certificate under the peer validation
  [§10.3](10_gateway-internals.md#103-mtls-pki) **Gateway ↔ Gateway mTLS peer validation (NET-072).** states,
  and the gateway NetworkPolicy admits the port from gateway pods alone
  ([§13.2](13_security-model.md#132-network-isolation)).
- **Axes.** Content plane, dialled by the forwarding replica, message authority with the forwarding
  replica, HTTP transport (§28.3).
- **Messages.** The forwarded request and its response, as
  [§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 2 states, in the
  request and response formats of the route forwarded
  ([§15.1](15_external-api-surface.md#151-rest-api), [§15.2](15_external-api-surface.md#152-mcp-api)).
- **Preconditions.** [§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.**
  rule 1 states when a replica forwards, and rules 3 and 4 state what the coordinator applies to a forwarded
  request. A replica forwards only when it holds the gateway replica certificate material
  ([§10.3](10_gateway-internals.md#103-mtls-pki) NET-072).
- **Timing.** The forward runs under the client request's deadline and is not retried
  ([§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 8). The dial and
  handshake bound is the one
  [§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Replica address:**
  states.
- **Exclusivity.** Any replica may forward to the coordinating replica, and several may do so at the same
  time. A forwarded request reaches the session's pod only through the coordinating replica, under the
  receiving replica's lease check of [§29.3](29_communication-scenarios.md#293-interactive-message-send)
  **Forwarding.** rule 5 and the `coordination_generation` stamp
  ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination)
  **Generation counters:**, §28.6).
- **Degradation.** [§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.**
  rule 6 states the outcome of a forward that is not delivered, and rule 7 states the outcome of a forward
  whose outcome is unknown.
```

### SPEC-4 · spec/28_communication-channels.md § 28.5.7 Gateway-to-store, § 28.6 Exclusivity and concurrency model, § 28.8 Failure and degradation matrix

(a) In the §28.5.7 lead paragraph, replace "The §28.3 channel register places one channel on this boundary, `CH-EVENTRELAY`, and it carries `None` in its Link column, so its own register row together with the endpoint stated in its card describes the connection." with:

```markdown
The §28.3 channel register places two channels on this boundary, `CH-EVENTRELAY` and `CH-BINDRELEASE`, and
each carries `None` in its Link column, so each channel's own register row together with the endpoint stated
in its card describes the connection.
```

(b) In the §28.5.7 diagram caption, replace the caption lines that begin "CH-EVENTRELAY: Redis, dialled by the gateway, on a stream per session, wired" with:

```
  CH-EVENTRELAY: Redis, dialled by the gateway, on a stream per session, wired
  only when Redis is present. The diagram shows CH-EVENTRELAY alone;
  CH-BINDRELEASE is a Redis pub/sub channel every replica subscribes to.
```

(c) In §28.5.7, after the last bullet of the `CH-EVENTRELAY` card (the **Degradation.** bullet) and before the heading `### 28.6 Exclusivity and concurrency model`, insert:

```markdown
**`CH-BINDRELEASE`**

- **Link.** `None` (§28.3).
- **Endpoint.** The Redis pub/sub channel `lenny:session:terminate`, on the deployment's Redis endpoint,
  under the TLS, AUTH, and client-certificate requirements the `CH-EVENTRELAY` **Endpoint.** bullet states
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes),
  [§10.3](10_gateway-internals.md#103-mtls-pki)).
- **Axes.** Control plane, dialled by the gateway, message authority with the gateway, Redis transport
  (§28.3).
- **Messages.** The [§11.4](11_policy-and-controls.md#114-user-invalidation) step-2 release request,
  published by full revoke and by the [§12.8](12_storage-architecture.md#128-compliance-interfaces) drain.
- **Preconditions.** The channel is wired only when Redis is present in the deployment
  ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). Every gateway replica subscribes,
  and a subscriber releases a binding as [§11.4](11_policy-and-controls.md#114-user-invalidation) step 2
  states.
- **Timing.** Peer replicas apply a request within seconds
  ([§11.4](11_policy-and-controls.md#114-user-invalidation)). The specification states no publish
  deadline, retry count, or interval for the channel.
- **Exclusivity.** The specification states no exclusivity constraint on the channel.
- **Degradation.** Delivery is at-most-once, and the specification does not state a redelivery. A dropped
  request leaves the binding on the replica that holds it.
```

(d) In §28.6, append the following sentence to the end of the paragraph that begins "**One holder per session.**", after "([§10.1](10_gateway-internals.md#101-horizontal-scaling), [§4.7](04_system-components.md#47-runtime-adapter)).":

```markdown
`CH-HOLDERFORWARD` carries the same unit to its receiving end: a replica performs a forwarded request only
while it holds the session's `REG-COORDLEASE`, as
[§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 5 states, so a
forwarded request reaches these channels only through the holder (§28.5.4).
```

(e) In §28.6, in the paragraph that begins "**Channels the specification states no constraint on.**", replace "`CH-ADMISSION` (§28.5.6), and `CH-EVENTRELAY` (§28.5.7)," with:

```markdown
`CH-ADMISSION` (§28.5.6), and `CH-EVENTRELAY` and `CH-BINDRELEASE` (§28.5.7),
```

(f) In the §28.8 matrix, insert the following row immediately after the row that begins "| `CH-OBJSTORE` |". The row stays one physical line.

```markdown
| `CH-HOLDERFORWARD` | A forward that is not delivered takes the outcome of [§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 6 (§28.5.4) | A forward whose outcome is unknown fails closed with `TARGET_NOT_READY`, HTTP 409, and is never held in the DLQ ([§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 7, §28.5.4) | A replica that receives a forwarded request for a session whose `REG-COORDLEASE` it does not hold performs no effect and applies rule 6, and a coordinator that loses the lease after that check is rejected at the pod on the `coordination_generation` stamp ([§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 5, §28.5.4, §28.6) | The specification names no metric or alert scoped to this channel (§28.5.4) |
```

(g) In the §28.8 matrix, insert the following row immediately after the row that begins "| `CH-EVENTRELAY` |". The row stays one physical line.

```markdown
| `CH-BINDRELEASE` | The publish is not redelivered (§28.5.7) | Delivery is at-most-once, and a request lost mid-publish is not redelivered (§28.5.7, [§11.4](11_policy-and-controls.md#114-user-invalidation)) | The specification states no exclusivity constraint on the channel (§28.5.7) | The specification names no metric or alert scoped to this channel (§28.5.7) |
```

(h) Replace the paragraph that begins "The register places no channel on the inter-replica boundary, so the matrix carries no row for it." and ends "likewise not a channel and carries no row here." with:

```markdown
A register entry in the §28.3 register-entry register is not a channel and carries no row here.
```

### SPEC-5 · spec/29_communication-scenarios.md § 29.2 Session start, § 29.3 Interactive message send; spec/11_policy-and-controls.md § 11.5 Idempotency; spec/27_web-playground.md § 27.3.1 OIDC cookie-to-MCP-bearer exchange

(a) Replace the paragraph that begins "Two requirements govern the rows whose required outcome is a forward to the coordinator." and ends "and the store-only row requires no forwarding at all." with the following:

```markdown
**Forwarding.** On the rows whose required outcome is a forward to the coordinator, the serving replica
forwards the request to the session's coordinating replica over `CH-HOLDERFORWARD` (§28.5.4), and the
coordinator performs the effect. The following rules govern every forwarding row.

1. The serving replica decides to forward after it has authenticated the request, read the session row, and
   found the session in a state for which a row requires a forward. It decides before it writes any state,
   sends any RPC to the pod, or writes the session inbox or DLQ. It forwards when the `REG-COORDLEASE`
   holder value names another replica
   ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination)
   **Replica address:**). When the holder value names the serving replica, the serving replica performs the
   effect itself.
2. The forwarded request is the request the serving replica's handler received: its method, path, query,
   body, and content headers, together with the client credential the serving replica authenticated. A tool
   call that its tool handler serves without a §15.1 route is forwarded as the `tools/call` request for the
   same tool and arguments ([§15.2](15_external-api-surface.md#152-mcp-api)). The handler returns the
   coordinator's response as its own, and the caller receives it as it receives the handler's local response
   ([§15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract)).
3. The coordinator authenticates the forwarded client credential as it authenticates a credential presented
   to it directly, including the `exp` check of [§13.3](13_security-model.md#133-credential-flow), and applies
   the authorization of the route or tool. A platform tool call reaches the serving replica over
   `LNK-GWCONTROL` with no client
   credential ([§28.5.3](28_communication-channels.md#2853-intra-pod)), so its forward carries the calling
   session's identifier, and the coordinator runs the call as a call from that session. The coordinator
   accepts no other principal the forwarding replica asserts
   ([§10.3](10_gateway-internals.md#103-mtls-pki) NET-072).
4. A forwarded request is never forwarded again. The coordinator does not apply the
   [§11.1](11_policy-and-controls.md#111-admission-and-fairness) rate limits, the
   [§11.5](11_policy-and-controls.md#115-idempotency) idempotency handling, or the
   [§4.8](04_system-components.md#48-gateway-policy-engine) `PreToolResult` phase to it, because the serving
   replica applies them.
5. A replica that receives a forwarded request for a session whose `REG-COORDLEASE` it does not hold
   performs no effect and applies the not-delivered outcome of rule 6 itself. A coordinator that loses the
   lease after that check is rejected at the pod on the `coordination_generation` stamp
   ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination)
   **Generation counters:**, §28.6).
6. A forward is not delivered when no holder value is readable, when the holder value carries no address,
   or when the dial or the TLS handshake fails. On a forward that is not delivered, the replica runs the
   authorization, scope, and rate-limit checks of the route or tool for a message carrying no `inReplyTo` to a
   `running` or `input_required` session, refuses the message as those checks state, and otherwise enqueues
   it in the session's DLQ. The
   entry takes the TTL of the [§7.2](07_session-lifecycle.md#72-interactive-session-model) dead-letter
   table's recovering-state row. The enqueue runs under §7.2 delivery path 6
   **Enqueue racing a terminal transition.**, and the message receives the receipt §7.2 delivery path 6
   **Held messages.** states. The coordinating replica delivers the entry as §7.2 delivery path 6
   **Delivery of held messages.** states, including the case in which no further message arrives and the
   entry waits for the next delivery point that bullet names. Every other request whose forward is not
   delivered fails closed with `TARGET_NOT_READY`, HTTP 409, carrying `currentState`.
7. A forward's outcome is unknown when it fails after the connection to the coordinator was established. A
   request whose forward outcome is unknown fails closed with `TARGET_NOT_READY`, HTTP 409, carrying
   `currentState`, whatever its route, and it is never enqueued in the DLQ. A client's retry is deduplicated
   by the route's own rules: a sender-supplied message `id`
   ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)) and the
   [§15.1](15_external-api-surface.md#151-rest-api) precondition table for an effect that already took place.
   A message sent without a sender-supplied `id` can be delivered twice.
8. The forward runs under the client request's deadline and is not retried. The dial and the handshake are
   bounded as [§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination)
   **Replica address:** states.

Where a row below says that the serving replica fails closed on an unreachable coordinator, rules 6 and 7
apply. A serving replica never reports a successful outcome for an effect that did not reach the pod, and
never writes a session state transition that depends on an effect at the pod it did not perform. This
section states `TARGET_NOT_READY` for the conditions of rules 6 and 7; the pre-running condition §15.1
states for the same code is a separate condition. The rows whose outcome is not a forward are stated by the
rows themselves: the `POST /v1/sessions/{id}/start` row addresses a state for which the specification
establishes no holder, so the serving replica performs the start itself, the
`POST /v1/sessions/{id}/finalize` row addresses `created` for the same reason, the events streaming and
`Accept: application/json` rows are served from the shared session-event relay, the logs streaming row is
served from the Postgres `EventStore`, the built-in adapter row addresses no pre-existing session and so has
no holder to forward to, the `POST /v1/sessions/{id}/resume` rows for `awaiting_client_action` and for a
`suspended` session whose pod was released and the `suspended` message-send rows are served on the serving
replica without a forward, the tool-use and elicitation resolution row is served through the Postgres
record that row states, and the store-only row requires no forwarding at all.
```

(b) In the off-holder matrix, replace the row that begins "| `POST /v1/sessions/{id}/messages`, no matching `inReplyTo` | `running` |" with the following row. The row stays one physical line.

```markdown
| `POST /v1/sessions/{id}/messages`, no matching `inReplyTo` | `running` and `input_required` | Forward to the coordinator, which evaluates the §7.2 delivery paths and produces the receipt. In `input_required` the coordinator applies delivery path 3. When the forward is not delivered, **Forwarding.** rule 6 applies | §29 |
```

(c) In the off-holder matrix, replace the row that begins "| `POST /v1/sessions/{id}/messages` carrying `inReplyTo` | `input_required` |" with the following row. The row stays one physical line.

```markdown
| `POST /v1/sessions/{id}/messages` carrying `inReplyTo` | `input_required` | Forward to the coordinator, which resolves the outstanding `lenny/request_input` call. The serving replica does not resolve the call against its own pending-request state and does not answer `delivered`. When the forward is not delivered or its outcome is unknown, the serving replica fails closed with `TARGET_NOT_READY`, HTTP 409, and holds nothing | §29 |
```

(d) In the off-holder matrix, replace the row that begins "| `POST /v1/sessions/{id}/tool-use/{toolCallId}/approve`, `POST /v1/sessions/{id}/tool-use/{toolCallId}/deny`," with the following row. The row stays one physical line.

```markdown
| `POST /v1/sessions/{id}/tool-use/{toolCallId}/approve`, `POST /v1/sessions/{id}/tool-use/{toolCallId}/deny`, `POST /v1/sessions/{id}/elicitations/{elicitationId}/respond`, and `POST /v1/sessions/{id}/elicitations/{elicitationId}/dismiss` | Any non-terminal state | No forwarding is required. Each pending tool-call approval and elicitation is a Postgres record keyed by tenant, session, user, and tool call or elicitation identifier, which every replica reads and which the gateway writes before it surfaces the request to the client. The serving replica records the resolution with a write that takes effect only while that record is pending, so the first resolution wins, and a resolution that finds no pending record for the session and user is refused as [§7.2](07_session-lifecycle.md#72-interactive-session-model) and [§9.2](09_mcp-integration.md#92-elicitation-chain) **`respond_to_elicitation` authorization:** state. The replica whose call is blocked on the record reads the record and resolves the call when the record leaves pending. The serving replica does not resolve the call against its own pending-request state and does not report a resolution it did not record | §29 |
```

The interrupt, terminate, delete, held-pod resume, and upload rows are unchanged. Their "fails closed" wording is governed by the sentence in (a) that maps it to rules 6 and 7.

(e) In §11.5, in the paragraph that begins "**Mechanism.**", insert the following sentence immediately after the sentence "Within this window, a duplicate request with the same key returns the cached response (same HTTP status and body) without re-executing the operation.":

```markdown
The gateway does not store a `TARGET_NOT_READY` response that [§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 6 or rule 7 produces, so a retry with the same key re-executes the operation.
```

(f) In §29.2 Session start, step 9 ("`gateway` → `postgres`, no register entry, the Postgres `SessionStore` role"), replace "places one channel on the gateway-to-store boundary, `CH-EVENTRELAY`, which carries the session event backlog on Redis (§28.5.7)" with "places two channels on the gateway-to-store boundary, `CH-EVENTRELAY` and `CH-BINDRELEASE`, which carry the session event backlog and the binding-release fan-out on Redis (§28.5.7)".

(g) In §27.3.1, in the bullet that begins "**Silent refresh.**", insert the following sentence immediately after "they continue running under the token they were upgraded with until the connection drops or the token's underlying session expiry is hit.":

```markdown
A call on such a connection that the serving replica forwards to the session's coordinating replica is authenticated by the coordinator ([§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 3), so once the old bearer has expired the coordinator refuses that call as unauthenticated.
```

### SPEC-6 · spec/12_storage-architecture.md § 12.4 Redis HA and Failure Modes, § 12.6 Interface Design, § 12.8 Compliance Interfaces; spec/11_policy-and-controls.md § 11.4 User Invalidation

(a) In §12.8, in the paragraph that begins "**Lifecycle fence and drain.**", insert the following text immediately after the sentence "The transition writes the terminal state on the session row." and before the sentence that follows it:

```markdown
After its transitions, the drain publishes the identifiers of the sessions it moved to a terminal state on `CH-BINDRELEASE` ([Section 28.5.7](28_communication-channels.md#2857-gateway-to-store)). Each replica that holds the binding of one of those sessions releases it as [§11.4](11_policy-and-controls.md#114-user-invalidation) step 2 states. A publish that is lost is not redelivered.
```

This anchor sentence differs from the sentence proposal 0100's SPEC-1 (e) replaces ("A successful drain does not wait for the session's terminal steps or the release of its pod, so a step that follows it can start before either one finishes."), so the two edits apply in either order.

(b) In §11.4, in the note that begins "> **Note:** Full-revoke propagation is two-tiered.", replace "the gateway publishes the step-2 release request and the token revocations on Redis pub/sub channels" with:

```markdown
the gateway publishes the step-2 release request, which names the tenant and the sessions whose bindings are to be released, on `CH-BINDRELEASE` ([§28.5.7](28_communication-channels.md#2857-gateway-to-store)) and the token revocations on a Redis pub/sub channel
```

(c) In §11.4, in **Full revoke propagation mechanism:** step 2, insert the following sentence immediately after the sentence that ends "which sends the pod the [§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` RPC.":

```markdown
The replica that holds the binding releases it only when the session's row on the Postgres primary is terminal.
```

(d) In §12.4, make the following changes.

Insert the following row immediately after the key-table row that begins "| `lenny:events:{session_id}` |". The row stays one physical line.

```markdown
| `lenny:session:terminate` | Binding-release fan-out (`CH-BINDRELEASE`) | **Platform-scoped** (no tenant prefix). One pub/sub channel to which every gateway replica subscribes; each release request names its tenant ([§11.4](11_policy-and-controls.md#114-user-invalidation)). Wired only when Redis is present. See [§28.5.7](28_communication-channels.md#2857-gateway-to-store). |
```

In the sentence that begins "The remaining keys fall into two categories", insert "the `lenny:session:terminate` binding-release channel is platform-scoped (one channel serves every gateway replica), " immediately before "and the `{root_session_id}:dlg:*` delegation-budget keys".

In the paragraph that begins "The tenant-leading convention is enforced in the Redis wrapper layer", replace "(`rl:`, `sq:`, `pg:sess-tenant:`, `conn:oauth:state:`, `derive_lock:`, `lenny:events:`)" with "(`rl:`, `sq:`, `pg:sess-tenant:`, `conn:oauth:state:`, `derive_lock:`, `lenny:events:`, and `lenny:session:terminate`)", and replace "and a session-scoped stream key for `lenny:events:`" with "a session-scoped stream key for `lenny:events:`, and the tenant each release request names for `lenny:session:terminate`".

(e) In §12.6, in the paragraph that begins "**Infrastructure pub/sub exclusion.**", make the following replacements. Replace "Three pub/sub pathways in the system intentionally bypass EventBus:" with "The following pub/sub pathways intentionally bypass EventBus:". Replace ", and certificate deny list propagation ([§10.3](10_gateway-internals.md#103-mtls-pki))" with:

```markdown
, certificate deny list propagation ([§10.3](10_gateway-internals.md#103-mtls-pki)), and the binding-release fan-out (`CH-BINDRELEASE`, [§28.5.7](28_communication-channels.md#2857-gateway-to-store), channel `lenny:session:terminate`)
```

Replace "(2) they use a poll-with-pub/sub-hint pattern where the pub/sub channel is a latency optimization, not a delivery guarantee (each has a polling fallback and startup-read path);" with:

```markdown
(2) the pub/sub channel is not a delivery guarantee: the circuit breaker and deny list pathways each have a polling fallback and startup-read path, and [§28.5.7](28_communication-channels.md#2857-gateway-to-store) states the binding-release fan-out's degradation;
```

### SPEC-7 · spec/10_gateway-internals.md § 10.3 mTLS PKI; spec/13_security-model.md § 13.2.4 `lenny-system` Namespace NetworkPolicies

(a) In §10.3, insert the following paragraph immediately before the paragraph that begins "**Gateway ↔ Interceptor mTLS peer validation (NET-063).**":

```markdown
**Gateway ↔ Gateway mTLS peer validation (NET-072).** On `CH-HOLDERFORWARD` ([§28.5.4](28_communication-channels.md#2854-inter-replica)) both replicas present the gateway replica certificate, and peer validation is symmetric, as on the Token Service ↔ Gateway link above. The dialling replica sets `tls.Config.ServerName` to `lenny-gateway.lenny-system.svc`, because it dials a pod IP rather than the Service name. The accepting listener on `gateway.holderForwardPort` requires a client certificate that chains to the cluster CA and carries that DNS SAN. At handshake it rejects plaintext, one-way TLS, and any other cluster-CA certificate, including agent-pod SPIFFE certificates. Possession of a valid cluster-CA certificate is never sufficient on its own. Outside dev mode ([§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)), a gateway configured with a coordination lease store refuses to start when the gateway replica certificate material is absent. In dev mode, a gateway without that material does not forward, and every forward takes the not-delivered outcome of [§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 6. A successful handshake establishes only that the peer is a gateway replica and carries no user authority. [§29.3](29_communication-scenarios.md#293-interactive-message-send) **Forwarding.** rule 3 states the authorization of a forwarded request.
```

(b) In §13.2.4, in the table row that begins "| **Gateway** (`lenny.dev/component: gateway`)", make two insertions. The row stays one physical line.

In the Egress cell, insert the following text immediately after "Token Service (TCP `{{ .Values.tokenService.grpcPort }}` — default 50052, mTLS); ":

```markdown
gateway pods (TCP `{{ .Values.gateway.holderForwardPort }}` — default 50054, mTLS, `CH-HOLDERFORWARD`, selector `lenny.dev/component: gateway`); 
```

In the Ingress cell, insert the following text immediately after "`GET /internal/drain-readiness` — NET-037); ":

```markdown
gateway pods (TCP `{{ .Values.gateway.holderForwardPort }}` — default 50054, mTLS, `CH-HOLDERFORWARD`, the only source the rule admits on that port, selector `lenny.dev/component: gateway`); 
```

(c) In §10.3, in the paragraph that begins "**Certificate authority:**", replace "a service mesh (Istio/Linkerd) is an optional alternative." with:

```markdown
a service mesh (Istio/Linkerd) is an optional alternative wherever NET-072 below does not require the gateway replica certificate.
```

### SPEC-8 · spec/18_build-sequence.md § 18.19 Phase 6 — Interactive sessions, SDKs

In the bullet that begins "- Session inbox subsystem per [§7.2]", replace "`ForwardMessage` cross-replica gRPC" with:

```markdown
the `CH-HOLDERFORWARD` off-holder request forward ([§28.5.4](28_communication-channels.md#2854-inter-replica))
```

## Spec files touched

- spec/10_gateway-internals.md (SPEC-2, SPEC-7)
- spec/11_policy-and-controls.md (SPEC-5, SPEC-6)
- spec/12_storage-architecture.md (SPEC-6)
- spec/13_security-model.md (SPEC-7)
- spec/18_build-sequence.md (SPEC-8)
- spec/27_web-playground.md (SPEC-5)
- spec/28_communication-channels.md (SPEC-1, SPEC-3, SPEC-4)
- spec/29_communication-scenarios.md (SPEC-5)
