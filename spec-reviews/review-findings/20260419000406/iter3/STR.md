# Iter3 STR Review

Scope note. Prior STR iterations (STR-001..006) focused on artifact/checkpoint storage semantics; this iter3 pass shifts to the streaming/flow-control/reconnection perspective declared by the caller. Regression-check of iter2 §10 and §15.1 streaming-related fixes: no regressions detected. The iter2 `SessionEvent` envelope + `SeqNum` + normative `OutboundChannel` back-pressure policy (§15, lines 122–155, 202–241, 329–348) is internally consistent, and the `Shared Adapter Types`/`Kind Registry` closure is well-formed. §10.3 gateway startup validation and §10 preStop stage-3 stream-drain (lines 123–124) remain intact. No regression of STR-005 (`CLASSIFICATION_CONTROL_VIOLATION` catalog) or STR-006 observability pairing.

New findings below concern gaps that existed pre-iter2 and survived iter2 — i.e., they are **missed streaming issues** surfaced when reviewing the new adapter machinery and §7.2 client streaming contract together.

---

### STR-007 `SessionEvent.SeqNum` has no client-facing resume path — clients lose events on stream drop [High] — **Status: Fixed**

**Resolution.** Iter3 plumbed `SeqNum` through the MCP client-facing stream end-to-end:
- `SessionEvent.SeqNum` docstring updated (§15) to require adapters to forward it into the native protocol envelope; `MCPAdapter` surfaces it as the SSE `id:` line.
- `attach_session` row in §15.2 gained an optional `resumeFromSeq: uint64` parameter, plus a new "Event-stream resume" paragraph describing the replay semantic (including the implicit `Last-Event-ID` equivalence on SSE reconnects).
- §10.4 Gateway Reliability gained an "Event replay buffer" bullet defining the per-session ring buffer (`gateway.sessionEventReplayBufferDepth`, default 512, range 64–4096), the eviction-case `gap_detected` protocol frame, and the coordinator-handoff discard-and-gap path.
- §7.2 streaming events table is followed by a new "Event ordering and resume" paragraph documenting the client-replay semantic and calling out `children_reattached` / `session.resumed` coverage.
- §15.2.1 `RegisterAdapterUnderTest` matrix adds an "Event-stream resume" assertion.
- `gap_detected` is documented as a stream-control frame, not a `SessionEvent` — the `SessionEventKind` closed enum is unchanged.


**Files:** `/Users/joan/projects/lenny/spec/15_external-api-surface.md` (§15, lines 202–214, 329–348, 1019 `attach_session`), `/Users/joan/projects/lenny/spec/07_session-lifecycle.md` (§7.2 streaming events table, lines 117–141), `/Users/joan/projects/lenny/spec/10_gateway-internals.md` (§10.4 lines 290–296 "reconnect/reattaches")

Iter2 added a monotonic per-session `SeqNum` on `SessionEvent` (§15 line 211) with the documented purpose: "Adapters MAY use SeqNum to detect gaps when delivering over lossy transports (e.g., webhook retries)." This is the correct primitive, but the spec only wires it **adapter-side** — i.e., for gateway→external-protocol-adapter dispatch and for the adapter's own downstream consumers (A2A webhooks, §21.1). There is no corresponding client-facing mechanism on the MCP `attach_session` streaming path, the primary session-streaming surface.

Concrete gap:
1. **No `ResumeFromSeq` / `Last-Event-Id` on `attach_session`.** §15 line 1019 defines `attach_session` as "Attach to a running session (returns streaming task)". §7.2 lines 117–129 enumerate the streaming events (`agent_output`, `tool_use_requested`, `tool_result`, `elicitation_request`, `status_change`, `session.resumed`, `children_reattached`, `error`, `session_complete`) delivered to clients over the MCP Streamable HTTP surface — **none** carry `seq`, and `attach_session` takes no "events since N" parameter. By contrast, §25.5 SSE delivery (line 2472) explicitly supports `Last-Event-ID` against `XRANGE` for the ops-event stream. The MCP session stream has no such property.
2. **§10.4 design principle contradicts observable behavior.** Line 290: *"Gateway pod failure causes a broken stream and reconnect, never session loss."* Line 296: "App-level reconnect: client reconnects with session_id, gateway looks up state, reattaches." Session *state* survives (good), but any `SessionEvent`s emitted between the gateway pod's last client write and the client's reconnect are lost — the client's next `attach_session` frame begins at "now," not at the last delivered `SeqNum`. This silently drops `agent_output` deltas, `tool_use_requested` (blocking the UI's approval affordance), `status_change(input_required)` (blocking the elicitation UX), and any `error` events the UI needs to render.
3. **Resume-related events that the spec promises `"exactly once per parent resume"` are at risk.** §7.2 line 141: "`children_reattached` is delivered exactly once per parent resume." Without a replay mechanism, a network blip at the wrong moment downgrades "exactly once" to "zero." The same applies to `session.resumed` (§7.2 line 126) carrying `workspaceRecoveryFraction`, which a UX needs to render the "partial workspace recovery" warning.

**Recommendation:** Make `SeqNum` end-to-end and plumb it through the MCP adapter's client-facing stream:

- Include `seq` on every outbound event frame the `MCPAdapter` writes to the client. For SSE framing inside MCP Streamable HTTP, emit the `SeqNum` as the SSE `id:` line (parallel to §25.5 line 2472 for ops events).
- Extend `attach_session` with an optional `resumeFromSeq` parameter. On reconnect, the gateway replays all buffered `SessionEvent`s with `SeqNum > resumeFromSeq`. Define an in-gateway per-session ring buffer (default depth: 512 events; configurable via `gateway.sessionEventReplayBufferDepth`; range 64–4096), sized so that a typical 60s reconnect window at 10 events/s fits with headroom.
- When a client's `resumeFromSeq` points to an event that has been evicted from the ring buffer, emit a single `gap_detected` event carrying `{"lastSeenSeq": N, "nextSeq": M}` before resuming live delivery — analogous to §25.5 line 2478 `:gap {}` comment for ops-event source transitions.
- Document the client-replay semantic in §7.2 alongside the events table (after line 129): "Every event carries `seq` (monotonic per-session). Clients that reconnect via `attach_session` MAY include `resumeFromSeq` to receive buffered events; if the requested seq has been evicted, the gateway emits `gap_detected` and resumes at the oldest retained event."
- Add a §15.2.1 contract-test item: "Event-stream resume — after forcible gateway restart or client disconnect, a reconnect with `resumeFromSeq` returns all events with `seq > resumeFromSeq` in order, or a single `gap_detected` when beyond the ring buffer."

Without this, the iter2 `SeqNum` addition delivers its promised value only to A2A post-v1 webhook consumers, not to the MCP primary surface that §15 lines 1006–1008 describes as Lenny's "interactive streaming sessions" flagship integration.

---

### STR-008 No client-facing keepalive/heartbeat frames on MCP session stream — intermediary idle-disconnect hazard [Medium]
**Files:** `/Users/joan/projects/lenny/spec/15_external-api-surface.md` (§15.2 MCP API lines 1006–1056, §15.4.1 `heartbeat` lines 1454–1460), `/Users/joan/projects/lenny/spec/07_session-lifecycle.md` (§7.2 streaming events lines 117–129), `/Users/joan/projects/lenny/spec/17_deployment-topology.md` (§17.6 ingress tuning)

The spec defines a `heartbeat`/`heartbeat_ack` pair **inside** the adapter↔binary stdin/stdout channel (§15.4.1 lines 1121, 1132, 1454–1460; 10s timeout → SIGTERM). This protects the adapter from a hung agent process but says nothing about the client-facing leg. There is no specified keepalive frame, SSE comment line, or WebSocket ping cadence for the external `attach_session` stream.

Concrete impact:
1. **Idle-timeout disconnects on intermediaries.** Agents generate bursty output — `reasoning_trace` phases, long tool calls, human elicitations under §9.2 `maxElicitationWait` (default 600s, §9 line 89). An intermediary L7 load balancer, corporate proxy, or browser-based WebSocket gateway with a 60–120s idle-connection timeout will drop the stream during any of these quiescent phases. §25 lines 867–868 set `idleTimeoutSeconds: 900` on the ops ingress "to keep SSE clients through reconnects" — this tuning is documented only for the ops-event endpoint (`/v1/admin/events/stream`) and not for the main MCP ingress serving client session streams.
2. **Playground WebSocket is worst case.** §27.5 line 142 pins the playground to "MCP WebSocket at `/mcp/v1/ws`". WebSocket `pong`/`ping` frames are the canonical idle keepalive, but §15.2 / §27 define neither cadence nor who initiates; the playground's default of 10+ minute agent quiescence during elicitation (§9.2 line 89) exceeds most corporate WebSocket proxy timeouts.
3. **No client detection of a half-open stream.** The client cannot distinguish "agent is thinking" from "TCP connection half-open and my intermediary silently dropped it." Without server-emitted keepalive frames the only signal is a TCP RST whenever the client next tries to send, which with STR-007 compounds into data loss.

**Recommendation:** Add a client-facing keepalive contract to §15.2 and §7.2:

- Define a server-emitted `:keepalive\n` SSE comment (for SSE-framed Streamable HTTP) and a WebSocket ping (for `/mcp/v1/ws`) at a configurable cadence (default 15s; Helm key `gateway.mcp.clientKeepaliveSeconds`; range 5–60s). The cadence must be strictly less than the most restrictive intermediary idle timeout the deployer is known to traverse; §17.6 deployment-topology should document this in the ingress-tuning table.
- Document in §7.2 streaming-events notes: "The gateway emits a protocol-native keepalive (SSE `:keepalive` comment or WebSocket ping) on the client stream every `gateway.mcp.clientKeepaliveSeconds` (default 15s) when no other frame has been written. These frames are not `SessionEvent`s and carry no `seq`; they exist only to keep intermediaries from closing an idle connection."
- Add a `lenny_mcp_client_keepalive_frames_total` counter (labeled by `adapter`, `session_id`) to §16.1 so operators can confirm keepalives are flowing in production.
- Extend §10.4 reliability bullets with: "Intermediary idle-timeout mitigation: gateway emits client-facing keepalives at `gateway.mcp.clientKeepaliveSeconds` cadence; clients that fail to receive two consecutive expected keepalives SHOULD treat the stream as dead and reconnect per STR-007 `resumeFromSeq`."

Pairs directly with STR-007: keepalive keeps streams alive during quiescent phases; `resumeFromSeq` recovers when keepalives fail.

---

### STR-009 Adapter buffered-drop head-eviction loses events silently from the subscriber's perspective [Medium]
**Files:** `/Users/joan/projects/lenny/spec/15_external-api-surface.md` (§15 lines 122–155 normative back-pressure policy)

Iter2's new back-pressure policy (line 126–135) defines the buffered-drop policy: "When Send is called and the buffer is full, the oldest event in the buffer is evicted (head-drop) and the new event is enqueued. The eviction increments the `lenny_outbound_channel_buffer_drop_total` counter." The gateway emits a server-side metric, but the **subscriber** (e.g., a webhook receiver, an A2A callback target) receives no in-band signal that events were dropped. The `SeqNum` field (line 211) is the mechanism a consumer would use to detect such gaps, but:

1. The spec does **not** require adapters to forward `SeqNum` into their native protocol's event envelope. §21.1 line 25 `A2AAdapter` serialization is described generically ("serializes the SessionEvent to the A2A streaming response format") with no statement that `SeqNum` must be preserved as an A2A event attribute.
2. Even when `SeqNum` is forwarded, the bounded-error policy and buffered-drop policy diverge in observability. Bounded-error closes the channel on overflow (subscriber gets a clean "reconnect and resume" signal), but buffered-drop silently head-drops and leaves the connection open; the subscriber only discovers the gap when it notices `SeqNum` jumped.

Concrete impact: a webhook receiver counting on the A2A `status` update stream to drive a UI state machine cannot distinguish "no state changes occurred" from "a `submitted→working` transition was dropped"; the `session_complete` event may still arrive but an intermediate `tool_use` or `elicitation` can be silently dropped.

**Recommendation:** Tighten the back-pressure contract so gaps are discoverable in-band:

- Normative addition in §15 (after line 135, within the buffered-drop bullet): "Each head-dropped event produces a **gap marker** that the channel MUST emit in place of the dropped event when the buffer next has capacity: a single `SessionEvent` with `Kind: "gap"` (add to the Kind registry) whose `Payload` is `{"droppedFromSeq": N, "droppedToSeq": M, "droppedCount": K}`. Subscribers consuming the channel therefore see an in-band signal that events were elided, with the exact `SeqNum` range. The per-channel gap marker replaces the K dropped events with a single frame, never with zero frames."
- Update §15 Kind Registry table (line 333) to include the new `gap` kind with `fires when`: "Emitted by the gateway outbound dispatcher after a buffered-drop head-eviction. Subscribers that declare `SupportedEventKinds` without `gap` do not receive this frame; such subscribers forfeit in-band visibility of dropped events and must rely on out-of-band `lenny_outbound_channel_buffer_drop_total` metric scraping."
- Require every third-party adapter (per the `RegisterAdapterUnderTest` matrix, §15.2.1 line 1087) that implements buffered-drop to preserve `SeqNum` in its native protocol envelope so the consumer can cross-check. Add a contract-test assertion: "For any adapter advertising buffered-drop, the sequence of `SeqNum` values observed by the subscriber across any 5-minute window is monotonic and emits exactly one `gap` frame for every N consecutive missing values."
- The bounded-error policy (connection-coupled SSE/long-poll) is unchanged — gap handling is not needed because the channel is closed on overflow and the subscriber resumes via STR-007's `resumeFromSeq`.

This closes the observability asymmetry between the two back-pressure policies and makes the iter2 `SeqNum` addition actionable end-to-end.

---

### STR-010 Stream-drain preStop stage-3 interaction with slow-subscriber policy is unspecified [Low]
**Files:** `/Users/joan/projects/lenny/spec/10_gateway-internals.md` (§10 preStop hook lines 107–125 including iter2 Postgres-read-failure addition), `/Users/joan/projects/lenny/spec/15_external-api-surface.md` (§15 lines 137–143 bounded-error policy)

§10 preStop stage 3 (line 123): "**Drain active streams (remaining grace period).** The hook polls `active_streams > 0` at 1-second intervals for the remainder of `terminationGracePeriodSeconds` after stages 1 and 2. This gives in-flight streams time to complete naturally or allows clients to detect the closing connection (via gRPC `GOAWAY` or SSE stream close) and reconnect to another replica via the load balancer."

The bounded-error policy (§15 line 137–143) says a connection-coupled `Send` must return non-nil within 100ms if the subscriber is slow, causing channel close. But during stage-3 drain, `Send` on a slow subscriber would return error immediately, closing the channel and (correctly) bypassing the 1-second polling — reducing the drain window. The interaction is not analyzed: is `active_streams` decremented at channel-close time (the intended behavior), or does the replica wait for slow subscribers? The iter2 Postgres-fallback addition (line 110) deals with preStop stage 2, but the stage-3 + slow-subscriber interaction is still ambiguous.

**Recommendation:** Add one sentence to §10 preStop stage 3 (after line 123): "The `active_streams > 0` predicate uses the post-policy count — channels closed by the bounded-error 100ms overflow rule (§15) or by a client-initiated disconnect decrement the counter immediately, so a replica with only slow subscribers drains faster than the grace period. Slow-subscriber close increments `lenny_stream_drain_slow_subscriber_close_total` (labeled by `adapter`) so operators can distinguish fast-drain from normal completion."

Non-critical; pure operability clarification.

---

No further streaming findings. Verified intact from iter2:
- `SessionEvent`/`SessionEventKind` closed enum + dispatch-filter rule (§15 lines 232–348) internally consistent.
- Bounded-error 100ms timeout (§15 line 140) reconciled with stage-3 drain polling (§10 line 123) modulo STR-010.
- `OutboundCapabilitySet.SupportedEventKinds` declaration-equals-contract rule (§15 line 342) is sound.
- Coordinator-local FIFO ordering guarantee (§15 line 1427) unchanged.
- Adapter↔binary stdin/stdout `heartbeat`/`heartbeat_ack` protocol (§15.4.1 lines 1454–1460, 10s SIGTERM escalation) unchanged.
