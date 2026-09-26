# Problem: Intra-pod MCP and direct-mode usage cannot attribute calls on concurrent pools

## Statement

On a pool with `sessionPolicy.maxConcurrentSessions > 1`, one runtime process serves every slot on the pod. The adapter exposes one pod-wide intra-pod MCP surface on `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR`. The first claim made while the slot registry holds no other entry arms that surface. The servers authenticate connections with the `mcpNonce` that the arming claim's manifest write produced, and the connector servers serve the arming session's connector set. The nonce authenticates a connection and does not identify a session, and no intra-pod MCP connection or call carries a session identifier.

The adapter therefore infers the calling session from occupancy through `soleSession`. That accessor names a session only while the shared runtime process has been given exactly one session since it last served none. It is empty from the moment a second session reaches the process until the process serves no session at all. While it is empty, the adapter refuses `tools/list` and `tools/call` with `FailedPrecondition` on the platform server and on every connector server. The spec states this refusal as normative in the runtime adapter section (item "MCP server security"), in the runtime integration levels section ("Authentication" paragraph of the intra-pod MCP surface), in the communication-channels section (the `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` cards, the scoping paragraph, and the degradation-table rows), and in the communication scenarios section (the nonce scenario and the whole-pod surfaces list). The spec's condition reads "exactly one session and that session is the caller". The adapter cannot check the second half of that condition, because no connection carries a session, so only the occupancy half is enforced.

The only per-session channel to the runtime outside `CH-MSGSOCK` is the adapter manifest, which is one pod-wide file. Each session start renames a fresh document over it with a new `mcpNonce` and that session's `connectorServers`. After a second start, the published manifest therefore advertises a nonce the running servers reject and connector sockets that no server listens on. The Go runtime SDK reads the manifest once, at runtime startup.

The same occupancy inference governs direct-mode token accounting on `CH-RUNTIMEOPS`. The `llm_request_completed` frame carries no session, and its schema sets `additionalProperties: false`. The adapter's token sink folds counts under `soleSession`, and the usage meter discards a fold made under an empty identifier. On a pod whose slots overlap, direct-mode token counts are therefore dropped. The policy-and-controls section and the `CH-RUNTIMEOPS` card promise a per-session cumulative total and do not state how a session-less frame is attributed.

The refusal is a correct fail-closed response to missing attribution. The gateway installs the forwarded session's user and tenant as the principal of a forwarded tool call, so a guessed session would run one user's call as another. Concurrent pods are tenant-pinned and not user-pinned, so the concrete risk of a guess is cross-user impersonation within one tenant.

Intended direction, chosen by the human: make the intra-pod MCP surface and the direct-mode usage path session-addressed, so that the adapter learns the calling session from the connection, the call, or the frame instead of inferring it from occupancy. Concurrent pools then get working MCP tools, per-session connector sets, and correct direct-mode usage.

In scope:

- Session attribution on `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR`, including serving each resident session's connector set.
- The pod-wide manifest as the carrier of per-session data. How per-session data reaches an already-running shared runtime (a per-slot manifest, the `CH-MSGSOCK` start frame, per-slot sockets, or a session identifier on the MCP connection or call) is a design decision inside this proposal. The pod-wide manifest is not a fixed constraint.
- Session attribution of `llm_request_completed` token counts in direct delivery mode.
- A deployer-visible statement of any MCP or direct-mode limitation that remains on concurrent pools after the change.

Out of scope, stated so that a later stage does not re-derive them:

- The other `CH-RUNTIMEOPS` frames. None of them carries a session: `llm_request_started`, `checkpoint_request`, `interrupt_request`, `terminate`, `deadline_approaching`, and `credentials_rotated` are all pod-wide. The adapter sends `deadline_approaching`, `interrupt_request`, and `terminate` to the shared process in response to one session's RPC, and the in-flight counter that gates Full-level credential rotation is keyed by provider across the pod. Proposal 0080 §1.5 inventories the rotation-gate residue. If the chosen mechanism adds a session identifier to `llm_request_completed`, the proposal states whether `llm_request_started` and the in-flight gate take the same field, so that the frame schema changes once.
- Session-less control events on the gateway control stream. The two production emitters that depend on the `soleSession` stamp already hold their session and drop it: `onSlotLeaseExpired` calls `EmitAuthExpired` with `slotID` in scope (the slot identifier equals the session identifier), and `rotateProviderFull` calls `EmitLeaseRejected` with `sessionID` in scope. Passing that identifier through fixes both, as commit 040323634 did for `EmitAdapterTerminating` and `EmitFinalUsageReport`. This is a local adapter correction and needs no channel mechanism. `EmitRateLimited` and `EmitProviderUnavailable` have no production caller.

Constraints:

- Fail closed on any connection, call, or frame whose session cannot be established or whose session holds no live binding on the pod.
- No regression for `maxConcurrentSessions: 1` pods.
- Preserve the nonce handshake's protection against processes that did not read the manifest.
- Keep the once-per-pod start race and the stale-surface takeover correct.
- A per-session secret gives no attribution strength beyond a runtime-declared session identifier, because the one shared process reads every session's secret. A session-addressed call is a runtime assertion, which is the same trust the adapter already gives the `sessionId` on `CH-MSGSOCK` (the "Non-guarantee" paragraph of that card). Any per-session credential must not be readable across slots the way per-slot credential files are, because a cross-slot-readable credential lets one slot's agent processes act as another slot's session.
- The platform is pre-deployment, so no backward-compatibility shims are needed.

## Evidence

- Verified, code: `pkg/adapter/runtimegeneration.go:110-162` (`noteRuntimeStartedLocked` increments `runtimeCohort` on every new session; `noteRuntimeClosed` resets it only when `runtimeLive` empties; `soleSessionLocked` requires `runtimeCohort == 1`).
- Verified, code: `pkg/adapter/platformtoolprovider.go:47-76` (`List` and `Call` resolve `callingSession`, which returns `codes.FailedPrecondition` when `soleSession` is empty); `pkg/adapter/connectortoolprovider.go:38-52` (same resolution, and the session and connector identifier are forwarded to the gateway).
- Verified, code: `pkg/adapter/slotsession.go:142-149` (`claimPodMCPStartLocked` arms only when `len(s.slots) == 1`), `:155-166` (`takePodMCPCancelsLocked`), `:177-181` (`PodMCPArming`, whose comment states that the manifest names whichever rename landed last), and `:397-418` (`cancelPodMCPIfRuntimeIdle`, `mcpArmingHeldLocked`).
- Verified, code: `pkg/adapter/session.go:120-161` (`startPlatformMCP` and `startConnectorMCPServers` run only when the claim set `startMCP`); `pkg/adapter/connectormcp.go:32-88`.
- Verified, code: `pkg/adapter/manifest.go:27-31`, `:220-252`, and `:280-323` (one `ManifestFilename`, renamed over on every start, with a fresh nonce and per-session `connectorServers` per write); `sdks/runtime/go/runtime/runtime.go:122-127` (default manifest path read by the SDK).
- Verified, code: `pkg/adapter/platformmcp.go:32-38` (`markMCPHandshakeSeen` classifies the runtime as at least Standard on handshake, so the observed integration level stays Standard while every tool call fails).
- Verified, code: `pkg/adapter/usage.go:118-121` (`SessionUsageMeter.Add` returns on an empty session), `:204-225` (`sessionTokenSink` folds under `soleSession`), and `:245-251` (`WireDirectModeUsage` wires `SoleSessionID`); `cmd/lenny-adapter/main.go:396` (production wiring).
- Verified, schema: `schemas/runtime-ops-events.schema.json:198-212` (`llm_request_completed` has no `sessionId` and sets `additionalProperties: false`). No frame in that schema carries a `sessionId` property.
- Verified, code: `pkg/adapter/runtimeops.go:392-407` (per-provider in-flight counter) and `:427-502` (`RequestCheckpoint`, `RequestInterrupt`, `RotateCredentials`, `SignalDeadlineApproaching`, and `Terminate` take no session).
- Verified, code: `pkg/adapter/adapterevents.go:152-155` (`emitControlEvent` stamps an empty `SessionID` from `soleSession`) and `:182-240` (emitter signatures); `pkg/adapter/slotcreds.go:253-278` (`onSlotLeaseExpired`); `pkg/adapter/credentials.go:164-227` (`rotateProviderFull`). A search of `pkg/` and `cmd/` outside tests finds no caller of `EmitRateLimited` or `EmitProviderUnavailable`.
- Verified, code: `pkg/gateway/gatewaycontrol/platformtools/platformtools.go:97-124` (the forwarded session's subject, tenant, and session become the call's principal).
- Verified, spec: runtime adapter section, item "MCP server security" and the manifest `mcpNonce` and `connectorServers` fields (`spec/04_system-components.md:1014`, `:836`, `:844`, and `:775`, where a later start replaces `sessionId`, `mcpNonce`, and `credentialsPath` in the one manifest).
- Verified, spec: runtime integration levels section, "Authentication" paragraph of the intra-pod MCP surface (`spec/15_external-api-surface.md:1738`); the `sessionId` field rule for runtime-to-adapter frames (`spec/15_external-api-surface.md:1597`).
- Verified, spec: communication-channels section, the `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` Exclusivity bullets, the scoping paragraph, and the degradation-table rows (`spec/28_communication-channels.md:1189-1195`, `:1237-1244`, `:1728-1737`, and `:1820-1821`); the `CH-MSGSOCK` "Non-guarantee" paragraph (`:871-874`); the `CH-RUNTIMEOPS` card and message table (`:1047-1088`).
- Verified, spec: communication scenarios section, the nonce scenario and the whole-pod surfaces list (`spec/29_communication-scenarios.md:302-311` and `:1521-1525`).
- Verified, spec: pool configuration and execution modes section, "Concurrent sessions" and "Deployer acknowledgment (concurrent sessions)" (`spec/05_runtime-registry-and-pool-model.md:531` and `:533`); the concurrent-pool tenant pinning (`:442` and `:535`).
- Verified, spec: policy-and-controls section, direct mode and the `zero_delta` anomaly counter, with direct mode restricted to single-tenant or development deployments (`spec/11_policy-and-controls.md:49`).
- Verified, tests: `pkg/adapter/podmcp_arming_internal_test.go` (`TestPodMCPArmingDeclinedOnCoTenantedPod_spec_15_4_3`); `pkg/adapter/usage_test.go` (`TestSessionTokenSinkResolvesCurrentSession_spec_4_7`); `tests/tier7a_load_local/sole_session_concurrent_release_test.go`; `tests/tier7a_load_local/podmcp_once_per_pod_start_race_test.go`.
- Verified, history: commit 040323634 ("Address a session on the gRPC leg by its session identifier alone") introduced `callingSession` and the `soleSession`-based token sink and control-event stamp.

## Who observes it

- A deployer who runs a Standard- or Full-level runtime on a pool with `maxConcurrentSessions > 1`. The "Concurrent" pool preset is a documented configuration, and the spec expects the runtime to run a dispatch loop keyed on `sessionId`. No admission check ties `maxConcurrentSessions > 1` to integration level, the concurrent-sessions acknowledgment names no MCP limitation, and the runtime-author and operator documentation present concurrency as a throughput setting without an MCP caveat.
- The users of sessions on such a pod. A session's delegation, `request_input`, elicitation, memory, inter-session messaging, and connector tools work until an unrelated session of the same tenant is scheduled onto the pod, and then fail mid-session. On a busy pool whose occupancy rarely reaches zero, the intra-pod MCP surface is effectively unavailable for the pod's lifetime.
- Operators. `callingSession` records no metric, and the observed integration level still reports Standard because the nonce handshake succeeds. The failure is visible only as tool errors inside the runtime.
- Operators of direct-mode deployments on concurrent pools. Direct mode is restricted to single-tenant or development deployments, which bounds the budget exposure to a tenant exceeding its own quotas.

## What breaks if nothing changes

- Every intra-pod platform and connector MCP call fails with `FailedPrecondition` from the moment a second session reaches the shared runtime process until the process serves no session. A Standard- or Full-level runtime on a concurrent pool silently degrades to Basic behaviour for MCP.
- Later sessions' permitted connectors are never served, and the published manifest advertises a nonce and connector sockets the running servers do not serve. The refusal currently masks this: the stale connector set is visible only while a second session is resident, and every call is refused in that state. It becomes a live defect the moment the refusal is lifted, so it constrains any fix.
- Direct-mode `llm_request_completed` token counts are dropped while the slots overlap. Per-session and delegation budgets are under-counted. The direct-mode idle-clock reset on a non-zero pulled delta is lost for a session whose only activity is a long LLM call; `agent_output` and tool events still count as activity. The `zero_delta` anomaly counter increments falsely against a runtime that reports its tokens correctly, which points operators at a working runtime image.
- A runtime cannot correct the attribution on its side, because the `llm_request_completed` schema forbids additional properties.
- Leaving the gap open adds no security exposure. The refusal fails closed, and before commit 040323634 the providers forwarded under a session captured at construction, which ran a co-tenant's calls under the first session's principal.

## Findings this unblocks

- Proposal 0080 §1.3 (the adapter manifest is pod-global and single-valued) and §1.4 (the intra-pod MCP surface is pod-global with the manifest). This proposal is the per-concern split of those two inventory entries.
- The recorded limit in proposal 0073 §9 "Recorded limits" that a Standard- or Full-level runtime remains unusable on a concurrent pod.

## Prior art considered

- Proposal 0073 (implemented) created this gap knowingly. Its summary and its §9 "Recorded limits" state that the manifest stays single and pod-global, that the intra-pod MCP surface stays pod-global with it, that a Standard- or Full-level runtime remains unusable on a concurrent pod, and that a per-session MCP surface needs a per-session socket path and per-session manifest fields, which it left out of scope. Proposal 0073 is implemented and is not edited. This proposal records the change in its own text.
- Proposal 0080 (early draft, not converged) is an inventory of the residues 0073 recorded. Its §1.3 and §1.4 describe the pod-global manifest and MCP surface, propose no remedy, and state that each entry becomes its own proposal. Its §2 "Already owned" list does not name 0084, so the triage of 0080 needs to record that 0084 owns §1.3 and §1.4. Proposal 0080 does not inventory the direct-mode token drop or the session-less control events. Its §1.5 inventories the pod-wide in-flight rotation gate, which shares the session-less `CH-RUNTIMEOPS` frames with this problem.
- Proposal 0070 (applied) binds the gateway-side tracing handler to the principal's session, and its §5 scopes a later membership check of a forwarded session identifier against the peer pod. Both concern gateway-side trust in the session identifier the adapter forwards. Neither lets the adapter learn which session's runtime made an intra-pod call. A session-addressed intra-pod surface produces the identifier that a 0070 §5 membership check validates, so the two compose without overlap.
- The `CH-MSGSOCK` addressing rule is the spec's existing precedent for runtime-declared session attribution. Every session-scoped frame carries `sessionId`, the adapter accepts a runtime-to-adapter frame only when its `sessionId` matches the stream's session and holds a live binding, and an unaddressed frame is rejected on a pod holding more than one slot. The "Non-guarantee" paragraph accepts that the runtime process stamps that identifier. Extending the same address-and-confirm rule to intra-pod MCP calls and to `llm_request_completed` is a smaller mechanism than per-session nonces or per-slot sockets, and it leaves the once-per-pod start, the stale-surface takeover, and the nonce handshake unchanged. This is a candidate for the design stage.
- Admission and disclosure without a wire change: declare the intra-pod MCP surface and direct-mode usage unavailable on concurrent pools, and reject Standard- or Full-level runtimes or `deliveryMode: direct` at pool admission when `maxConcurrentSessions > 1`, or add the loss to the concurrent-sessions acknowledgment. The spec already gates `preConnect` on `maxConcurrentSessions: 1`, which gives a precedent. This is the smallest intervention that removes the silent degradation. The human chose the session-addressed direction, so this is recorded as a rejected alternative and a fallback.
- The communication scenarios section's list of what the specification does not state has no entry for per-session MCP attribution. The open build-gap and test-gap findings, the proposal queue, and the gateway-runtime-comms audit record contain no finding that stages this change.

## Validated premises

Premise lens (verdict: revise).

- Stands. One runtime process serves every slot on a concurrent pod, and a per-session `Close` does not end it (`pkg/adapter/socketruntime.go:41-49`, `:171-181`; `pkg/adapter/runtimegeneration.go:15-24`).
- Stands. The intra-pod MCP surface is pod-wide and armed at most once per pod by the first claim made while the registry holds no other entry, with that claim's nonce.
- Stands. `soleSession` names a session only when the process has been given exactly one session since it last served none. A pod whose slots keep overlapping never returns to cohort 1, even after the arming session leaves and a single co-tenant remains.
- Stands, with a side detail. Both `tools/list` and `tools/call` are refused on the platform and connector servers. The gateway's `ListPlatformTools` ignores the session because the catalog is uniform, so only the connector list and all calls need a session for attribution.
- Stands. The spec states the refusal as normative in each cited location.
- Stands. A Standard- or Full-level runtime on a concurrent pool silently loses MCP. The level probe does not detect it, because the handshake succeeds and `markMCPHandshakeSeen` classifies the runtime as at least Standard.
- Stands, with a larger effect than first stated. The connector servers keep the arming session's set and nonce, and every later start publishes a manifest with a nonce and connector sockets the running servers do not serve.
- Stands. `llm_request_completed` carries no session, and the direct-mode sink drops counts whenever `soleSession` is empty.
- Refuted. The premise that session-less control events such as `rate_limited` go out with an empty `sessionId` because the attribution information does not exist is false. `EmitRateLimited` and `EmitProviderUnavailable` have no production caller. The production emitters that rely on the stamp, `EmitAuthExpired` from `onSlotLeaseExpired` and `EmitLeaseRejected` from `rotateProviderFull`, hold the session and drop it. This is a local plumbing defect, and the statement now places it out of scope for the channel mechanism.
- Stands, with a narrower scope. Guessing the session would impersonate another user. Concurrent pods are tenant-pinned, so the risk is cross-user within one tenant.
- Holds only as a runtime assertion. A per-session nonce or credential does not prove which session calls, because the shared process reads every session's manifest. A per-session credential exposed with the group-read access of per-slot credential files opens a within-tenant cross-user impersonation path.
- Refuted as a scope premise. `llm_request_completed` is not the only session-less `CH-RUNTIMEOPS` frame: none of the channel's frames carries a session. The statement now names the other frames as out of scope.
- Stands. Commit 040323634 introduced the occupancy-based calling-session resolution.

Evidence lens (verdict: revise).

- Verified. The providers resolve the session through `soleSession` on every `List` and `Call` and refuse with `FailedPrecondition` when it is empty.
- Verified. The cohort semantics match "exactly one session since it last served none", and a pod whose slots keep overlapping never recovers.
- Verified. Arming requires `len(s.slots) == 1`, later claims get `startMCP == false`, connector servers start only under `startMCP`, and every start rewrites the one manifest.
- Verified. The direct-mode sink uses the same accessor and drops counts, the schema forbids a `sessionId`, and the production adapter wires the sink.
- Drifted. The control-event half named `rate_limited`, which has no production caller. The live session-less emitters are `EmitAuthExpired` and `EmitLeaseRejected`, both of which drop an identifier their callers hold. `EmitCheckpointBarrierAck` is the remaining session-less live event.
- Verified. The spec states the refusal normatively in each cited place. The spec names no gRPC code; `FailedPrecondition` is a code-level choice. Only the occupancy half of the spec's condition is enforceable.
- Drifted. The original statement cited "the non-guarantee paragraph" among the intra-pod channel citations. The only "Non-guarantee" paragraph in the communication-channels section sits in the `CH-MSGSOCK` card, and it is relevant as precedent for runtime-stamped attribution rather than as a statement of the MCP refusal.
- Verified, with a spec gap. The policy-and-controls section and the `CH-RUNTIMEOPS` card promise per-session accumulation without stating how a session-less frame is attributed. Direct mode applies to concurrent pools.
- Verified. The concurrent-sessions acknowledgment says nothing about MCP.
- Verified. The gateway installs the forwarded session's owner and tenant as the principal.
- Verified. Commit 040323634 introduced `callingSession` and `soleSession`, and every cited test exists under its cited name.

Prior-art lens (verdict: stands).

- No landed spec mechanism or code path provides session attribution on the intra-pod MCP surface or in direct-mode usage.
- The code premises and the history claim hold.
- Proposal 0073 recorded this gap as an accepted limit and staged no fix.
- Proposal 0080 §1.3 and §1.4 inventory the same gap without a remedy. This proposal is their per-concern split, and 0080's triage needs to record the ownership.
- The in-flight rotation gate shares the root cause and is inventoried in 0080 §1.5. The statement now requires the proposal to state whether `llm_request_started` and the gate take the same field.
- Proposal 0070 is related and composes with this proposal without overlap.
- The concurrent-sessions acknowledgment is accurate as quoted.
- No audit record or queue entry stages this change.

Scope lens (verdict: revise).

- The original statement bundled three mechanisms that share `soleSession` but not a fix: intra-pod MCP attribution, direct-mode token attribution, and control-event stamping. Consequences 1 and 2 form one problem. The statement now keeps MCP attribution as the core.
- The control-event part needs no channel redesign. The statement now places it out of scope as a local adapter correction.
- Direct-mode usage covers one frame of a channel that carries no session on any frame. The lens recommended splitting it into a separate `CH-RUNTIMEOPS` problem, or bounding it to token attribution with the other frames excluded by name. The statement keeps token attribution in scope, because the human's chosen direction names the direct-mode usage path, and excludes the other frames by name.
- The pod-wide manifest is the substrate of the MCP problem. The statement now places it inside the design space instead of treating it as fixed.
- The deployer acknowledgment gap belongs to this proposal as an interim or residual statement.

Impact lens (verdict: revise).

- The MCP refusal is reachable on a documented, admission-unrestricted configuration with no deployer-visible warning.
- The failure is intermittent and non-local from the user's point of view, and it records no metric.
- The code conforms to a normative spec rule, so the defect is a spec gap. Leaving it opens no security hole.
- The direct-mode consequence is reachable in production and is larger than first stated: budgets are under-counted, the idle-clock reset on a non-zero delta is lost, and the `zero_delta` anomaly counter fires falsely. Direct mode is restricted to single-tenant or development deployments.
- The control-event half is not observable today. Two of its emitters have no production caller, and no production gateway code opens the `AdapterEvents` stream, so every control event is currently dropped as `no_stream` whatever `sessionId` it carries.
- The stale connector set and nonce are masked by the refusal in current code and become live only once the refusal is lifted. The statement now presents them as a constraint on the fix.

Alternatives lens (verdict: revise).

- The control-event part needs no wire or spec change. The statement now places it out of scope.
- The `CH-MSGSOCK` address-and-confirm rule is an existing, smaller mechanism that could extend to intra-pod MCP calls and to `llm_request_completed`. The statement records it under prior art as a design candidate.
- A per-session nonce or per-slot socket adds no attribution strength over a runtime-declared session identifier. The statement records this as a constraint.
- The problem is one symptom of `CH-RUNTIMEOPS` carrying no session on any frame. The statement now names the remaining frames as out of scope and requires a decision on `llm_request_started` and the in-flight gate.
- Per-session connector sets follow from session attribution, because the connector provider already forwards the session and connector identifiers and the gateway resolves the permitted set per session. Only the set of open connector sockets remains to decide.
- Admission and disclosure without a wire change is recorded as a rejected alternative and a fallback.
- The code-level description of the refusal matches the tree.
