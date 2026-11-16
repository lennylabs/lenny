# Agent runtimes only

Status: design for owner review, revision 7, 2026-11-15. Revision 7 follows [`connector-removal-design.md`](connector-removal-design.md) revision 3: the replacement columns of the removal tables no longer keep capability inference, connector tool filtering, the connector client, or connectors as alternatives. Revision 6 applies the owner's decision of 2026-11-15 to remove connectors ("Yes, let's drop connectors.") under [`connector-removal-design.md`](connector-removal-design.md) revision 2: the alternatives of "AR-6" and the statements on tools, MCP, and the link check no longer name connectors. Revision 5 applies the remaining instruction of [`rest-only-client-api-design.md`](rest-only-client-api-design.md) on the "Implicit session" term. Nothing here is built or staged, and no independent review has read it. It is written from the owner's direction of 2026-11-13 to remove the non-agent runtimes. Revision 2 records the owner's settlement of open decisions O1 to O8 on 2026-11-13 ("O5 (b), yes to all the others") and of `session_start`'s `sessionId` ("ok, let's keep it"), and corrects revision 1's reading of the leniency rules for an unknown member. Revision 3 records the owner's answer of 2026-11-13 that O3's refusal is the one rule for removed fields in every design (D6), which settles the LLM response cache removal design's O3 and which the LLM upstreams design's LU-4 applies to the fields it removes. Revision 4 restates the "Held session" term as "Provider-adapter session" under the simplified O3 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), which the owner answered "yes" on 2026-11-14, and changes no rule of this design. The design reads against every design under `designs/` as implemented, in particular [`agent-pod-layout-design.md`](agent-pod-layout-design.md) revision 15, [`runtime-protocol-design.md`](runtime-protocol-design.md) revision 12, [`message-delivery-design.md`](message-delivery-design.md) revision 23, [`connector-removal-design.md`](connector-removal-design.md) revision 3, [`session-isolation-design.md`](session-isolation-design.md), [`placement-design.md`](placement-design.md), [`egress-and-git-design.md`](egress-and-git-design.md), [`session-derive-design.md`](session-derive-design.md), and [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md), with [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md) revision 12 and [`rest-only-client-api-design.md`](rest-only-client-api-design.md). Its label prefix is AR.

Each factual claim carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, the documentation, or a design under `designs/` on 2026-11-13. |
| **[design]** | What this design requires. Nothing implements it. |
| **[inference]** | Reasoned from the sources and not run. "Validation owed" lists each one that matters. |
| **[unverified]** | A claim the pages and code read do not state. "Validation owed" lists each one. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The specification defines a runtime by its type and, for an agent runtime, by its execution mode ([§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) **[source]**:

- A `type: agent` runtime runs sessions, with a workspace, delegation, elicitation, and multi-turn dialog.
- A `type: mcp` runtime hosts an MCP server that knows nothing about Lenny. Lenny manages its pod lifecycle, isolation, credentials, workspace, pool, egress, and audit, and the gateway exposes it at `/mcp/runtimes/{name}`.
- An agent runtime in `executionMode: session` binds each session to a claimed pod. An agent runtime in `executionMode: service` is a replicated service: one runtime process serves many requests, with no claim, no workspace, no checkpoint, and no per-session lifecycle, and the gateway routes each message to a tenant-pinned replica.

The designs under `designs/` have rebuilt the session-mode agent pod: one slot container per slot under its own UID, a runtime supervisor that launches a fresh runtime process per session at bind, a file-descriptor broker, a fixed runtime-visible layout, a handshake on every runtime connection, and per-slot egress scopes. The `type: mcp` and service-mode cases do not fit that model, and before the cross-design review of 2026-11-13 applied this design, every design that touches the pod, the protocol, egress, or admission carried a separate rule for them **[source]**:

- The pod layout renders a service-mode pod with no `/workspace` or `/session` and a launch at pod start, and a `type: mcp` pod as a pod with one slot whose adapter speaks MCP to the runtime ([PL-1](agent-pod-layout-design.md#pl-1-pod-blueprint)).
- The runtime protocol keeps `sessionId` multiplexing and refuses tool calls on a service-mode pod, because one process serves several requests there ([RP-3](runtime-protocol-design.md#rp-3-tool-calls-on-frames)). The message delivery design keeps `startId` open as decision C1 because service mode might need it ([`message-delivery-design.md`, "Decisions"](message-delivery-design.md#decisions)).
- The egress design plans a whole proposal, its proposal 0, for a durable service-mode tenant pin, a service-mode listener in the adapter, and new network policy rows, to close two High findings: F-5.2.65, in which service-mode tenant pins have no arbiter across gateway replicas, and F-5.2.66, in which the service-mode request path is not wired at all ([`egress-and-git-design.md`, "Relationship to proposal 0111 and open findings"](egress-and-git-design.md#relationship-to-proposal-0111-and-open-findings)). It also leaves the trust anchor unconstrained on service-mode and `type: mcp` pods.
- Tenant scoping evaluates a service-mode pod with no environment and refuses sessions whose environment has anything of its own ([`tenant-scoping-identity-design.md`, "Effective policy"](tenant-scoping-identity-design.md#effective-policy)). The user git identity and user credential delivery designs refuse service-mode pods.
- Session derive and tenant-derived runtimes refuse a target without a runtime supervisor (SD-P4 and the export refusal of DR-8), a case that now reduces to a `type: mcp` target or a stale runtime row.

Neither mode is in use. The `/mcp/runtimes/{name}` endpoint is wired with a nil dispatcher in the gateway, so every call answers `RUNTIME_UNAVAILABLE` ([`mcp-protocol-upgrade-design.md`, "Current state"](mcp-protocol-upgrade-design.md#current-state)) **[source]**. The service-mode slot gate in `pkg/adapter/statelessslot` has no production caller (F-5.2.66) **[source]**. No reference runtime is `type: mcp`, and the specification's catalog already directs MCP servers away from runtimes ([§26.1 Catalog Overview](../spec/26_reference-runtime-catalog.md#261-catalog-overview)) **[source]**. Lenny has no deployments.

The owner decided on 2026-11-13 to remove both modes. After this change Lenny runs `type: agent` runtimes in session mode and nothing else. This design states what leaves, what simplifies in the other designs, how the removed fields are refused, and what users of the removed modes do instead.

## Scope

**Removed:**

- The `type: mcp` runtime: the enum value, the dedicated endpoint `/mcp/runtimes/{name}` and its implicit sessions, the adapter's MCP client to a runtime, `mcpEndpoint` in discovery, `mcpRuntimeFilters` on environments, the `type: mcp` delegation refusal, the reference `type: mcp` runtime, and every `type: mcp` exception in admission, pod rendering, idle handling, derive, delegation, and billing ("AR-1").
- Service mode: `executionMode: service`, `maxConcurrentRequestsPerPod`, claimless tenant-affinity routing, the `/v1/stateless/{pool}` route the tree serves, the service-mode tenant pin of the egress design's proposal 0, the service-mode pod layout, the service-mode scaling formula and its metrics, `conversationContinuity`, and every service-mode exception in the other designs ("AR-2").
- `startId` on `session_start` and `session_started`, `sessionId` on every frame other than `session_start`, and the remaining rules for several sessions on one runtime connection ("AR-3", O5).
- The `type` and `executionMode` fields themselves, under O1 and O2 ("AR-4").

**Changed:**

- Writes that name a removed field are refused by name ("AR-4", O3).
- A derive no longer compares execution modes, and the refusals that existed only for targets without a supervisor leave. A check at the post-chain point that the final runtime row is `registered` takes their place for every create, derive, and delegated child ("AR-5", O8).
- `sessionIsolationLevel` loses `executionMode` and `conversationContinuity` (O4).
- Headings that name the removed concepts are renamed through the anchor-redirect map (O7).

**What stays: MCP as a protocol.** Two different things in Lenny carry the name MCP, and only one of them leaves:

| MCP as a client-facing and tool protocol (stays) | MCP-server runtimes (removed) |
| :-- | :-- |
| The gateway serves MCP to clients: the MCP API of [§15.2 MCP API](../spec/15_external-api-surface.md#152-mcp-api), MCP Tasks, and the protocol versions [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) upgrades. | A runtime whose process is an MCP server, registered with `type: mcp`. |
| Lenny's platform tools, which an agent runtime calls through `tool_call` frames or through the reference MCP bridge ([RP-3](runtime-protocol-design.md#rp-3-tool-calls-on-frames), [RP-5](runtime-protocol-design.md#rp-5-reference-mcp-bridge)). | The gateway's dedicated endpoint `/mcp/runtimes/{name}`, which proxies a client's MCP calls to that process. |
| MCP servers that an operator runs as operator services, which a harness in the pod reaches as an MCP client through egress ([`connector-removal-design.md`, "CR-3 External services"](connector-removal-design.md#cr-3-external-services)). [§9.3 Connector Definition and OAuth/OIDC](../spec/09_mcp-integration.md#93-connector-definition-and-oauthoidc) is a successor pointer, because connectors left under CR-1 of that design. | The adapter's MCP client that drives the runtime process (`pkg/adapter/mcpruntime.go`, `pkg/adapter/mcp/client.go`). |
| MCP servers an agent runtime launches or calls itself, such as the servers a harness configures, reached over the pod's egress. | Capability inference from `ToolAnnotations` at runtime registration, and `mcpRuntimeFilters`. |
| The management MCP server of `lenny-ops` at `/mcp/management` ([§25.12 MCP Management Server](../spec/25_agent-operability.md#2512-mcp-management-server)). | |

Several names look related and stay: `caller_type: service` on tokens, the gateway subsystems' `maxConcurrent` settings in [§17.8.2 Capacity Tier Reference](../spec/17_deployment-topology.md#1782-capacity-tier-reference), `sessionPolicy.maxConcurrentSessionsPerPod`, `files.maxConcurrentExports`, the `mcp` form of `tool_choice` that the client tools design refuses, the Kubernetes Service object, and Embedded Mode, the `lenny up` local stack.

**Non-goals:**

- Pods with several slots. `sessionPolicy.maxConcurrentSessionsPerPod` above 1 stays, with `acknowledgeSharedPod`, as [PL-9](agent-pod-layout-design.md#pl-9-pool-settings-and-renames) states. The rules that serve concurrent session-mode pods stay unless this design names them.
- A replacement for either mode (O6).
- Editing any design other than this one. "Instructions for reviewers of other designs" states each design's change.

## Terms

| Term | Meaning |
| :-- | :-- |
| Agent runtime | A runtime that runs sessions in session mode on an agent pod. After this design every runtime is an agent runtime, and the documents call it a runtime. |
| `type: mcp` runtime | The removed runtime type whose process is an MCP server that knows nothing about Lenny. |
| Service mode | The removed execution mode `executionMode: service`, in which one runtime process on a pod serves many requests from many sessions, with no claim, no slot, no workspace, no checkpoint, and no per-session lifecycle. |
| Session mode | `executionMode: session`, in which each session binds to a slot of a claimed pod. After this design every session runs in it, and the field that names it leaves (O2). |
| Removed field | `type`, `executionMode`, or `maxConcurrentRequestsPerPod`. "AR-4" states how a write that names one is answered. |
| Implicit session | The session record the gateway creates per connection to a `type: mcp` runtime's dedicated endpoint, for audit and billing. It leaves with the endpoint. The built-in client adapters' implicit sessions leave too, under [`rest-only-client-api-design.md`](rest-only-client-api-design.md): the provider adapters move out of Lenny and create ordinary sessions through the REST API. |
| Tenant-affinity routing | The service-mode routing of [§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): the gateway maps a tenant to a set of pinned pod IPs and routes each message to one of them. |
| Service-mode tenant pin | The durable record that the egress design's proposal 0 adds, which ties a service-mode pod to one tenant and arbitrates between gateway replicas. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Slot and slot container | One of the places on a pod where a session runs, and the container `runtime-<n>` that serves slot `n` under its own UID ([SI-1](session-isolation-design.md#si-1-pod-layout)). |
| Runtime supervisor | The platform binary that is the first process of every slot container. It launches the runtime's `command` when the adapter asks on `CH-SUPERVISE`, and it runs setup, cleanup, the slot release, and the file-descriptor broker ([PL-1](agent-pod-layout-design.md#pl-1-pod-blueprint)). |
| `CH-MSGSOCK` | The intra-pod channel on which the adapter and the runtime exchange JSON Lines frames ([§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod)). Under [RP-1](runtime-protocol-design.md#rp-1-handshake) every connection opens with a handshake. |
| `startId` | A member of `session_start` and `session_started` that tells apart starts of one session on one connection (§28.5.3, **Inbound: `session_start`**) **[source]**. |
| The session of a frame (RPR-18) | The rule that a frame's session is the session of the slot whose connection carries it ([RP-3](runtime-protocol-design.md#rp-3-tool-calls-on-frames)). |
| Leniency rules | The rules of "MD-24" under which the adapter keeps a session running when a runtime writes something slightly off. An unknown member of a known frame is ignored with no report. An unknown frame type is ignored, and the gateway emits a `runtime_warning` event at most once per type per session ([`message-delivery-design.md`, "MD-24"](message-delivery-design.md#md-24-runtime-frames-and-leniency)) **[source]**. |
| `sessionIsolationLevel` | The object on a session's response that reports its isolation posture, with `executionMode`, `podReuse`, `residualStateWarning`, and `conversationContinuity` among its fields ([§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow)). |
| Provider-adapter session | An ordinary session that a provider adapter creates. It replaces the term "Held session": Lenny keeps no held-session state and no close-after-turn rule, under the simplified O3 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md), which the owner answered "yes" on 2026-11-14. The adapter may shorten the session's idle timeout, an operator lengthens it through the pool's idle timeout, a `one_shot` runtime ends its own session, and the adapter routes a follow-up request to the session by its conversation fingerprint ([`anthropic-messages-api-design.md`, "Session lifetime"](anthropic-messages-api-design.md#session-lifetime); that design's "RO-10"). The inventory row on `conversationContinuity` in the hold rule records what revision 1 found. |
| Pod with no per-session policy | A pod that evaluates every session's egress policy with no environment ([`tenant-scoping-identity-design.md`, "Effective policy"](tenant-scoping-identity-design.md#effective-policy)). |
| Name-constrained trust anchor | The per-pod CA that `netagent` issues with name constraints, so a runtime trusts it only for terminated hosts ([`egress-and-git-design.md`, "TLS interception"](egress-and-git-design.md#tls-interception)). |
| Post-chain point | The point after the `PreRoute` chain and revision selection where every check on the final runtime and pool runs ([`runtime-routing-design.md`, "Post-chain point"](runtime-routing-design.md#post-chain-point)). |
| Anchor-redirect map | The record through which a specification heading's anchor moves, so that a redirect exists for every inbound link ([§28.1](../spec/28_communication-channels.md), naming law N3; `tests/spec-anchor-moves.json` and `scripts/specshift/anchor`) **[source]**. |
| Tenant outbound connector | A customer-run agent that dials out to the platform so that agent pods reach hosts on the customer's private network ([`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md)). |
| Operator service | A service an operator runs outside Lenny, such as an MCP server, that a runtime reaches through the pod's egress. Lenny keeps no record of it, and a credential binding on its host attaches its credential ([`connector-removal-design.md`, "Terms"](connector-removal-design.md#terms)). |

### Labels from other designs

| Label | Where | Meaning |
| :-- | :-- | :-- |
| PL-1, PL-9, PL D6, PLR-2, PLR-17, PLR-19 | [`agent-pod-layout-design.md`](agent-pod-layout-design.md#pl-1-pod-blueprint) | The pod blueprint with its service-mode and `type: mcp` pods; the pool renames; the supervisor in every runtime container; `command` on every runtime; the pool field names; the launch at bind. |
| RP-3, RP-4, RP-6, RPR-8, RPR-18, RP O10 | [`runtime-protocol-design.md`](runtime-protocol-design.md#rp-3-tool-calls-on-frames) | Tool calls on frames and the remaining multi-session rules; availability; what the adapter enforces; tools on every session-mode pod and none on a service-mode pod; the session of a frame; the retired refusal on session-mode pods. |
| MD-4, MD-24, MD C1 | [`message-delivery-design.md`](message-delivery-design.md#md-24-runtime-frames-and-leniency) | Turn accounting and the `consumed` frame; runtime frames and the leniency rules; whether `startId` stays, which this design's D2 settles. |
| SD-1, SD-P4 | [`session-derive-design.md`](session-derive-design.md#sd-1-one-route) | The derive route and its checks; the refusal of a derive onto a target without a supervisor, answered with `target_unsupervised`, which O8 removes. |
| DR-4, DR-6, DR-8, DF-16, DR decision 10 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records) | Tenant runtime records and the runtime-row mirror, whose "Registered rows" paragraph owns the `registered` check of O8; base changes; interactions, whose export refusal on an unsupervised target this design removed; the finding that refusal closed, which stays closed by DR-3; service-mode bases refused, a decision this design makes moot. |
| EG proposal 0, EG decisions 54, 60, and 62 | [`egress-and-git-design.md`, "Proposal split"](egress-and-git-design.md#proposal-split) and ["Decisions"](egress-and-git-design.md#decisions) | The service-mode tenant pin; the trust anchor on every session-mode pod with service mode and `type: mcp` as exceptions; per-slot scopes, which the owner settled as (a) on 2026-11-13; and the withdrawal of proposal 0 with a name-constrained anchor on every agent pod, which the egress design records under this design. |
| TS decision 15 | [`tenant-scoping-identity-design.md`, "Decisions"](tenant-scoping-identity-design.md#decisions) | Concurrent and service-mode pods evaluate with no environment. |
| PM-4 | [`placement-design.md`, "PM-4"](placement-design.md#pm-4-placement-requirements) | Placement requirements, `sharedPod`, `allowSharedPod`, and `allowReusedPod`. |
| SL-6 | [`session-lifecycle-design.md`, "SL-6"](session-lifecycle-design.md#sl-6-idle-pods-and-dormancy) | Idle pods and dormancy, with the sessions that cannot become dormant. |
| UGI decision 14 | [`user-git-identity-design.md`, "Pod models"](user-git-identity-design.md#pod-models) | The refused pod models. |
| UCD decision 16 | [`user-credential-delivery-design.md`, "Decisions"](user-credential-delivery-design.md#decisions) | `lockEgressToHosts` and the pods on which it is refused. |
| DA-1, DA-4 | [`delegation-availability-design.md`](delegation-availability-design.md#da-1-levels) | `allowDelegation` and its refusal on `type: mcp`; the tool list. |
| MP-6, MP O3 | [`mcp-protocol-upgrade-design.md`, "MP-6"](mcp-protocol-upgrade-design.md#mp-6-outbound-clients) | The outbound MCP clients and the versions they accept. |
| LU-4 | [`llm-upstreams-and-cost-budgets-design.md`, "LU-4"](llm-upstreams-and-cost-budgets-design.md#lu-4-removal) | The removal of LLM credential leases and direct delivery mode, with the pool and runtime fields that leave with them. |
| RC-2, RC O3 | [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md#decisions) | The code removal of the LLM response cache; how a pool write that still names `cachePolicy` or `cacheScope` is answered, which D6 settles. |
| RV field classes | [`runtime-revisions-design.md`, "Field classes"](runtime-revisions-design.md#field-classes) | `type` and `executionMode` as fixed fields, and `restartable`. |
| CR-1, CR-3 | [`connector-removal-design.md`](connector-removal-design.md#cr-1-removal) | The removal of connectors, with §9.3 as a successor pointer; and the guidance by which a runtime reaches operator services through egress. |

## High-level design

Lenny runs one kind of runtime. A runtime is an agent program that runs sessions on an agent pod, and every session binds to a slot of a claimed pod, where the runtime supervisor launches a fresh runtime process after materialization and setup. Every runtime therefore has a supervisor, a workspace, a session volume, slots, checkpoints, and a per-session lifecycle, and every pod follows the layout of [PL-1](agent-pod-layout-design.md#pl-1-pod-blueprint).

The `type` and `executionMode` fields carry one value each after the removal, so they leave the `Runtime` resource, the pool and pod resources, the admin API, the runtime and session rows, discovery, and `sessionIsolationLevel` (O1, O2). A write that still names one is refused with an error that names the field (O3). `maxConcurrentRequestsPerPod` leaves with service mode.

The runtime protocol has one frame contract. A runtime process serves one session, so `startId` leaves `session_start` and `session_started`, and no rule for several sessions on one connection remains. No frame carries `sessionId` except `session_start`, which keeps it as data the runtime reads: to correlate its logs with the platform's, to keep one identity across restarts of its process, to build idempotency keys for actions outside the session, and to detect that its session was derived. The adapter never acts on it. A runtime that still writes `sessionId` on another frame is unaffected, because the adapter ignores an unknown member silently (O5).

Routing has one path: create, claim, and bind. Tenant-affinity routing, the `/v1/stateless/{pool}` route, the service-mode tenant pin, the service-mode listener, and the service-mode scaling formula leave. Egress has no pod without a session binding apart from warm and pinned idle pods, so every runtime process runs under a name-constrained trust anchor, and the egress design's proposal 0 is withdrawn rather than built.

Every refusal that existed for these modes leaves: the service-mode tool refusal, SD-P4's `target_unsupervised`, DR-8's export refusal on an unsupervised target, the `type: mcp` delegation refusal `target_not_an_agent`, the `allowDelegation`, `integrationLevel`, and `agentInterface` refusals on `type: mcp`, the service-mode refusals of the user git identity and user credential designs, and the service-mode clauses of tenant scoping, placement, and tenant security settings. One general check replaces SD-P4's fail-closed property: at the post-chain point of every create, derive, and delegated child, the final runtime's row must be `registered`, and the tenant-derived runtimes design owns the check (O8).

A user who hosted an MCP server as a runtime runs it as an operator service that agent runtimes reach through egress, or has an agent runtime launch it. A user who ran a stateless agent as a service runs it in session mode, where a pool with several slots per pod gives density, or runs it outside Lenny as an operator service when it needs no agent session ("AR-6").

## Decisions

| # | Question | Owner's answer | Recorded as | Applied in |
| :-- | :-- | :-- | :-- | :-- |
| D1 | Whether non-agent runtimes stay. | "Remove non-agent runtimes altogether, in a new design: `type: mcp` and service mode (`executionMode: service`)." After this change Lenny runs only `type: agent` runtimes in session mode. The owner asked this design to decide whether `type` and `executionMode` go entirely. | Decision file, "Owner decisions (2026-11-13)"; the owner's direction of 2026-11-13 | AR-1, AR-2, O1, O2 |
| D2 | What leaves with service mode. | "With service mode gone, `startId` goes (the owner's point), along with any remaining sessionId-on-frames multiplexing, service-mode tenant-affinity routing, `maxConcurrentRequestsPerPod`, the service-mode tool refusal, and the service-mode rows everywhere." | Decision file, "Owner decisions (2026-11-13)" | AR-2, AR-3 |
| D3 | The `type: mcp` supervisor questions. | The removal "makes the type: mcp supervisor questions moot." | Decision file, "Owner decisions (2026-11-13)" | AR-1, AR-5 |
| D4 | Open decisions O1 to O8 of revision 1 (2026-11-13). | "O5 (b), yes to all the others." O1 (a), O2 (a), O3 (b), O4 (a), O5 (b), O6 (a), O7 (a), and O8 (b). | Decision file, "AR decisions settled, 2026-11-13" | O1 to O8 below, AR-3, AR-4, AR-5 |
| D5 | Why `session_start` keeps `sessionId` under O5 (b) (2026-11-13). | The owner asked why the member stays on `session_start`. The answer given: it is the only carrier of the session's identifier to the runtime, it stays the same across restarts of the runtime process, the runtime uses it to correlate its logs with the platform's for support, to build idempotency keys for actions outside the session, and to detect that its session was derived. The owner answered "ok, let's keep it". The adapter never acts on the member. | Decision file, "AR decisions settled, 2026-11-13" | AR-3, ARR-6 |
| D6 | Whether O3's refusal is the rule for removed fields in every design (2026-11-13). | "ok", in the owner's answers to the consolidated list of open decisions of 2026-11-13: one rule for removed fields everywhere. A write that names a removed field is refused with `400 VALIDATION_ERROR` naming the field. The answer settles O3 of [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md#decisions), and LU-4 of [`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md) applies it to `deliveryMode`, `credentialPolicy`, `supportedProviders`, `credentialCapabilities`, and the other fields it removes. | Decision file, "Owner answers to the consolidated list, 2026-11-13", item 6 | AR-4 |

D2 settles open decision C1 of the message delivery design as its option (c), removal of `startId`.

The owner settled open decisions O1 to O8 of revision 1 on 2026-11-13 with "O5 (b), yes to all the others" (D4). Each entry below keeps its options and reasoning as the record of the choice, and states the option taken.

**O1. Whether the `type` field stays.**

- (a) Remove `type` from the `Runtime` resource, the CRD, the admin API, the runtime row and its CHECK constraint, `GET /v1/runtimes`, `list_runtimes`, the runtime scaffolder, and the documentation. Every runtime is an agent runtime.
- (b) Keep `type` as an optional field whose only value is `agent`, defaulted when absent.
- (c) Keep `type` as an open enum reserved for a later runtime kind.

**Recommendation: (a).** A field with one legal value tells a reader nothing and costs a CHECK constraint, an enum, a validator, and a documentation entry. Under (b) every example and every reader still carries the word. (c) reserves a value space for a feature nobody has designed, and a later kind would need its own design and its own field name anyway. Lenny has no deployments, so no stored resource carries the field.

**Settled 2026-11-13: (a)** (D4).

**O2. Whether `executionMode` stays.**

- (a) Remove `executionMode` from the `Runtime` resource, `SandboxTemplate`, `Sandbox`, the `PodRecord` and `PodSpec` interfaces of [§12.6 Interface Design](../spec/12_storage-architecture.md#126-interface-design), the runtime, session, pod-registry, and agent-pod-state rows, `sessionIsolationLevel`, the derive's equality check, and the `INCOMPATIBLE_RUNTIME` error.
- (b) Keep it with the single value `session`.
- (c) Keep it as an enum reserved for a later mode.

**Recommendation: (a)**, for the reasons of O1. [`ROADMAP.md`](../ROADMAP.md) lists an "External-parent session mode" as `executionMode: external` on `POST /v1/sessions` **[source]**. That is a session request field rather than the runtime field, and a design for it chooses its own name, so (a) does not block it.

**Settled 2026-11-13: (a)** (D4). The derive's mode check and `INCOMPATIBLE_RUNTIME` leave with the field.

**O3. How a write that names a removed field is answered.** The admin API decodes JSON without refusing unknown members **[source]**, so a field that only disappears from the schema is ignored, and a `type: mcp` registration would register the MCP server image as an agent runtime. It would then fail at its first bind, when the runtime does not answer the handshake of [RP-1](runtime-protocol-design.md#rp-1-handshake) **[inference]**.

- (a) The removed fields disappear from the schemas and are ignored as any unknown member is.
- (b) The removed fields disappear from the schemas and the documentation, and the admin API, the bootstrap seed, and `lenny-ctl runtime validate` refuse a write that names one with `400 VALIDATION_ERROR`, whose message names the field and states that Lenny runs agent runtimes in session mode. The CRDs drop the fields from their structural schemas, so the API server's field validation reports them as unknown **[inference]**.
- (c) Keep the fields with one legal value and refuse any other value.

**Recommendation: (b).** It turns a late, confusing bind failure into an immediate error that names the cause, and it costs one list of three names in one validator. It adds no alias and no second code path, so it is a refusal rather than a compatibility mode.

**Settled 2026-11-13: (b)** (D4). A write that names a removed field is refused with `400 VALIDATION_ERROR` naming the field.

**O4. Whether `sessionIsolationLevel` keeps `executionMode` and `conversationContinuity`.** After the removal `executionMode` is always `session` and `conversationContinuity` is always `"platform"`.

- (a) Remove both fields from `sessionIsolationLevel`, the client SDK types, and the OpenAPI document. The hold rule of the Open Responses and Messages adapters reduces to its capability condition: the runtime declares `multi_turn` with injection.
- (b) Keep both with their constant values.

**Recommendation: (a).** Lenny has no deployed clients, and a constant field invites a client to branch on it.

**Settled 2026-11-13: (a)** (D4).

**O5. Whether a session-scoped frame may still carry `sessionId`.** RPR-18 takes a frame's session from its connection, and a frame that names another session is refused (SIR-3) **[source]**. RP-3 keeps the member optional "so that one schema serves every mode" **[source]**.

- (a) Keep `sessionId` optional on session-scoped frames, accepted when it names the slot's session and refused otherwise, with the reason restated: it lets a runtime log under the identifier and catches a runtime that confuses sessions.
- (b) Remove the member from every frame other than `session_start`. A runtime that still writes it on another frame is unaffected: the adapter treats it as an unknown member of a known frame, which the leniency rules of "MD-24" ignore silently. Only an unknown frame type produces a `runtime_warning`.

**Recommendation of revision 1: (a).** Revision 1 argued that the runtime SDKs and the frame examples of the message delivery design already send `sessionId`, so (b) would turn every such frame into a warning, and that (a) keeps the slot check that session isolation states for its own reasons. The first half rested on a misreading of "MD-24": an unknown member is ignored with no report, and only an unknown frame type is reported **[source]**. Under (b) a runtime that still sends the member therefore sees no change, and the slot check has nothing left to check, because the adapter takes every frame's session from its connection's slot and reads no session from the frame. The multiplexing rules leave under both options.

**Settled 2026-11-13: (b)** (D4). No frame carries `sessionId` except `session_start`, which keeps it as data the runtime reads (D5): the runtime correlates its logs with the platform's for support, keeps one identity across restarts of its process, builds idempotency keys for actions it takes outside the session, and detects that its session was derived. The adapter never acts on it. Session isolation's refusal of a frame that names another session (SIR-3) leaves with the member, because no frame names a session any more.

**O6. Whether a `type: mcp` use case needs a replacement now.**

- (a) No replacement. The documentation states the alternatives of "AR-6".
- (b) Record Lenny-hosted MCP servers as a candidate in [§21 Planned / Post-V1](../spec/21_planned-post-v1.md).
- (c) Build a hosted-connector feature now, in which Lenny runs a connector's MCP server in a pod it manages.

**Recommendation: (a).** No user, test outside the reference binary, or reference runtime depends on the mode, and the endpoint has never served a call in production **[source]**. Connectors already cover MCP servers that run elsewhere, and an agent runtime can launch an MCP server inside its own slot. A later design can propose (c) from a stated need.

**Settled 2026-11-13: (a)** (D4).

The recommendation above names connectors as they stood on 2026-11-13. [`connector-removal-design.md`](connector-removal-design.md) removed them on 2026-11-15, so an MCP server that runs elsewhere is an operator service that a runtime reaches through egress (CR-3), and option (c) has no connector feature to extend.

**O7. Headings that name the removed concepts.** [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) has about 168 inbound links, and [§4.7.9 Startup Sequence for `type: agent` Runtimes](../spec/04_system-components.md#479-startup-sequence-for-type-agent-runtimes) has 19 **[source]**.

- (a) Rename §5.2 to "5.2 Pool Configuration" and §4.7.9 to "4.7.9 Runtime Startup Sequence", and move both anchors through the anchor-redirect map. §5.2's "Execution Modes" and "Execution Mode Scaling Implications" subsections become "Session policy" material and "Scaling Implications".
- (b) Keep both headings.

**Recommendation: (a).** A heading that names a removed concept misleads every reader who follows a link to it, and the anchor-redirect map exists to move anchors without breaking inbound links.

**Settled 2026-11-13: (a)** (D4). §5.2 becomes "5.2 Pool Configuration" and §4.7.9 becomes "4.7.9 Runtime Startup Sequence", each through the anchor-redirect map.

**O8. Where the fail-closed check on a stale runtime row lives.** SD-P4 now refuses only a target whose runtime row is not `registered`, because DR-4's mirror leaves a row stale when it refuses a `Runtime` resource (the "Supervisors" subsection of [`session-derive-design.md`](session-derive-design.md) before its revision 18, now ["Final runtime row"](session-derive-design.md#final-runtime-row)) **[source]**. That concern is not specific to supervisors or to derive. Whether a create or a delegated child onto a row that is not `registered` is refused today is not established **[unverified]**.

- (a) Remove SD-P4 and `target_unsupervised` and leave stale rows to whatever the general target resolution does.
- (b) Remove SD-P4 and `target_unsupervised`, and require at the post-chain point, for every create, derive, and delegated child, that the final runtime row is `registered`, answered as an unknown runtime. DR-4 owns the rule.

**Recommendation: (b).** It keeps the fail-closed property SD-P4 provided and applies it to every path that can land on a stale row.

**Settled 2026-11-13: (b)** (D4). SD-P4 and `target_unsupervised` leave, and [`tenant-derived-runtimes-design.md`, "DR-4"](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records) owns the check at the post-chain point that the final runtime row of every create, derive, and delegated child is `registered`, answered as an unknown runtime. DR-4's "Registered rows" paragraph states the details: the answer is `RESOURCE_NOT_FOUND`, or the surface's own not-found answer, which is `TARGET_NOT_IN_SCOPE` for a delegated child; a platform-derived runtime also needs its base's row to be `registered`; the admin API writes `true` on a row it registers with no `Runtime` resource; and a resume, which reads the session's stored runtime, is not refused.

## Open decisions

No decision remains open. "Decisions" records O1 to O8, which the owner settled on 2026-11-13 (D4), with their options, their reasoning, and the option taken.

## Requirements

| # | Requirement |
| :-- | :-- |
| ARR-1 | The `Runtime` resource, the admin API, the runtime row, and discovery carry no runtime type. Every runtime is an agent runtime (O1). |
| ARR-2 | No resource, row, response, or interface carries an execution mode. Every session binds to a slot of a claimed pod (O2). |
| ARR-3 | A write to the admin API, the bootstrap seed, or `lenny-ctl runtime validate` that names `type`, `executionMode`, or `maxConcurrentRequestsPerPod` is refused with `400 VALIDATION_ERROR` naming the field (O3). |
| ARR-4 | The gateway serves no `/mcp/runtimes/{name}` route and no `/v1/stateless/{pool}` route. A request to either answers as an unknown route. |
| ARR-5 | `session_start` and `session_started` carry no `startId`. The adapter writes one `session_start` per runtime process and reads one `session_started` in answer (D2). |
| ARR-6 | No rule for several sessions on one runtime connection remains. A session-scoped frame's session is its connection's slot's session (RPR-18). No frame other than `session_start` carries `sessionId`, the adapter ignores the member silently on any other frame, and it never acts on the `sessionId` of `session_start` (O5, D5). |
| ARR-7 | Platform tools reach every runtime process. No pod refuses them for its execution mode. |
| ARR-8 | `netagent` issues the name-constrained trust anchor on every agent pod. |
| ARR-9 | Every runtime's sessions can be idle-suspended and become dormant, subject only to their checkpoint policy. |
| ARR-10 | A derive does not compare execution modes, and no derive or delegation is refused for a target's lack of a runtime supervisor. The final runtime row of every create, derive, and delegated child is `registered` (O8). |
| ARR-11 | `sessionIsolationLevel` carries neither `executionMode` nor `conversationContinuity`, and `podReuse` and `residualStateWarning` are true only for recycling pools and pools with `maxConcurrentSessionsPerPod` above 1 (O4). |
| ARR-12 | Pool scaling uses the session-mode formula for every pool, with `mode_factor` from recycling and slots alone and one `safety_factor` per tier. |

## Current state: inventory

The inventory below lists each rule or mechanism with its source, what removing it takes away, and what replaces it. Sources are specification sections with their headings, design labels, or tree paths. The counts are of matching lines on 2026-11-13 and exclude the names that "Scope" lists as staying **[source]**.

### Specification

About 108 lines across 19 files match, with a further 37 lines that say `type: agent` only to contrast with `type: mcp`.

| Rule or mechanism | Source | What removing it takes away | Replacement |
| :-- | :-- | :-- | :-- |
| The runtime types `agent` and `mcp` | [§2 Goals and Non-Goals](../spec/02_goals-and-non-goals.md#2-goals-and-non-goals), **Goals**; [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), the `type` line and the `type: agent` and `type: mcp` paragraphs; [§23 Competitive Landscape](../spec/23_competitive-landscape.md#23-competitive-landscape), **Feature Comparison Matrix** and **2026 Open-Source Entrants Matrix**; [§23.1 Why Lenny?](../spec/23_competitive-landscape.md#231-why-lenny) item 4 | Managed hosting of an unmodified MCP server. | Operator services reached through egress and runtimes that launch MCP servers ("AR-6"). |
| Dedicated endpoint and implicit session | [§4.1 Edge Gateway Replicas](../spec/04_system-components.md#41-edge-gateway-replicas); [§9.1 Where MCP Is Used](../spec/09_mcp-integration.md#91-where-mcp-is-used), the table row and **Runtime Discovery**; [§15](../spec/15_external-api-surface.md#15-external-api-surface), the paragraph on `type: mcp` runtime dedicated endpoints after **SessionEvent Kind Registry**; [§25.12 MCP Management Server](../spec/25_agent-operability.md#2512-mcp-management-server), **Architecture**; [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces), **Lifecycle fence and drain.**, the implicit sessions of `type: mcp` endpoints | `/mcp/runtimes/{name}`, its MCP negotiation, and its per-connection session record. | None. |
| Discovery fields | [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api), `GET /v1/runtimes`; [§15](../spec/15_external-api-surface.md#15-external-api-surface), **Shared Adapter Types**, `AuthorizedRuntime.McpEndpoint` and the `AgentInterface` comment | `mcpEndpoint` on each runtime. | None. |
| Type-specific admission | §5.1, **`integrationLevel`** (which RP removes anyway), **`agentInterface` Field**, **`command`**, **Inheritance Rules**, and **Normative Merge Algorithm** (`type` and `executionMode` prohibited on derived runtimes) | The rules that refuse agent-only fields on `type: mcp` and the inheritance rows for the removed fields. | None needed. |
| Capability inference at runtime registration | §5.1, **Capability Inference from MCP `ToolAnnotations`** | Inference at `type: mcp` registration. | None. The inference at connector registration left with connectors under CR-1 of [`connector-removal-design.md`](connector-removal-design.md#cr-1-removal), and `capabilityInferenceMode`, `toolCapabilityOverrides`, and `mcpAnnotationMapping` left with it under D10 of that design. |
| `mcpRuntimeFilters` | [§10.6 Environment Resource and RBAC Model](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model), the field, its YAML, and the tool schema cache bullet | Capability-based tool filtering for `type: mcp` runtimes in an environment. | None. `connectorSelector`, the connector tool filter, left under CR-1 of [`connector-removal-design.md`](connector-removal-design.md#cr-1-removal), so an environment selects runtimes alone. |
| `runtimeMcpServers` | [§4.7.6 Adapter Manifest Field Reference](../spec/04_system-components.md#476-adapter-manifest-field-reference) | A manifest field reserved for `type: mcp` runtimes, empty in v1. | None. |
| The `type: mcp` pod and protocol path | [§4.7.10 Deployment Model](../spec/04_system-components.md#4710-deployment-model), the `type: mcp` sentence; [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod), the row "Any path of a `type: mcp` runtime" | A pod on which the adapter speaks MCP to the runtime and exchanges no `CH-MSGSOCK` frame. | None. |
| Delegation refusal and discovery scoping | [§8.2 Delegation Mechanism](../spec/08_recursive-delegation.md#82-delegation-mechanism), `target_not_an_agent`; [§8.3 Delegation Policy and Lease](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), **Discovery scoping** | The refusal of a `type: mcp` delegation target. | None needed: every registered runtime is a delegation target under policy. |
| Idle exemption | [§6.2 Pod State Machine](../spec/06_warm-pod-model.md#62-pod-state-machine), idle suspension | The rule that a `type: mcp` session cannot be idle-suspended. | None needed (ARR-9). |
| `mcp-type` safety factor | [§4.6.2 PoolScalingController (Pool Configuration)](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration); [§17.8.2 Capacity Tier Reference](../spec/17_deployment-topology.md#1782-capacity-tier-reference), the "Pool safety factor (mcp-type)" row and the normative note | The second `safety_factor` per tier. | The agent value, one per tier. |
| Build phases | [§18.1 Phasing Intent](../spec/18_build-sequence.md#181-phasing-intent); [§18.2 Phase Map](../spec/18_build-sequence.md#182-phase-map), rows 12b and 12c; [§18.13 Phase 5](../spec/18_build-sequence.md#1813-phase-5--externaladapterregistry-mcpcompletionsopen-responses); [§18.29 Phase 12b](../spec/18_build-sequence.md#1829-phase-12b--type-mcp-runtime-support); [§18.30 Phase 12c](../spec/18_build-sequence.md#1830-phase-12c--sessionpolicy-presets-and-service-mode) | Phase 12b, and the service-mode half of phase 12c. | Phase 12c keeps the `sessionPolicy` presets. |
| `executionMode` and its values | §2, **Goals**; §5.1, **Standalone Runtime** YAML; §5.2, **Execution Modes**; [§4.6.1 Warm Pool Controller (Pod Lifecycle)](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle), the CRD table; §12.6, `PodRecord` and `PodSpec`; [§26.3 `claude-code`](../spec/26_reference-runtime-catalog.md#263-claude-code) and [§26.7 `chat`](../spec/26_reference-runtime-catalog.md#267-chat) YAML | The mode field on every resource and interface. | None (O2). |
| Service mode | §5.2, the **`service`** definition, the service-mode slot paragraph, **Session contract (service mode).**, **Tenant isolation (service mode).**, the **Service-mode limitations (v1)** and **Preferred alternative** boxes, and **Tenant pinning** | Replicated stateless serving with no claim, no workspace, and no lifecycle. | Session mode with slots for density, or an operator service reached through egress ("AR-6"). |
| Tenant-affinity routing | §5.2, the **`service`** definition and **Tenant pinning** | The gateway's in-memory tenant-to-pod-IP map and its EndpointSlice watch. | None. Session tenant pinning stays. |
| `maxConcurrent` (service) | §5.2 YAML; [§12.5 Artifact Store](../spec/12_storage-architecture.md#125-artifact-store), checkpoint retention for service pools | Per-pod request capacity and its readiness signal. | None. |
| Service-mode scaling | §5.2, **Execution Mode Scaling Implications**, the `service` bullets of `mode_factor` and `burst_mode_factor` and the demand signal paragraph with `lenny_service_requests_total` and `lenny_service_concurrent_active`; §4.6.2, "for service-mode base pools" | A demand signal and a capacity factor for claimless pools. | The session-mode formula (ARR-12). |
| Service-mode client contract | [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow), `sessionIsolationLevel`: `executionMode`, the service clauses of `podReuse` and `residualStateWarning`, and `conversationContinuity`; §15, **Open Responses conversation sessions.** | The fields that told a client its session had no continuity. | None (O4). |
| Replay and derive mode equality | §15.1, **Session Replay Semantics** and the `INCOMPATIBLE_RUNTIME` row of the error catalog | A refusal of a target with another mode. | None needed. |
| Pre-launch interaction | [§6.1 What a Pre-Warmed Pod Looks Like](../spec/06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like), the `service` row; §5.2, the `preConnect` admission bullet | Rules [PL-8](agent-pod-layout-design.md#pl-8-removal-of-pre-launched-runtimes) already removes. | None. |
| Service-mode pod lifecycle | §4.7.10, **Runtime supervisor.** and the process-exit tables, the service-mode clauses and the "Service-mode pod" column of the `Sandbox` phase table | A pod with no claim whose runtime process starts at pod start. | None. |
| Alert scope | [§16.5 Alerting Rules and SLOs](../spec/16_observability.md#165-alerting-rules-and-slos), `PodClaimQueueSaturated` | The parenthetical that excludes service-mode pools. | The alert applies to every pool. |
| `startId` | [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod), the `session_start` member row, **Inbound: `session_start`**, `session_end` rule 2, **Outbound: `session_started`** rule 5 and member row, and the examples; [§15.4.4 Sample Echo Runtime](../spec/15_external-api-surface.md#1544-sample-echo-runtime); [§15.4.6 Conformance Test Suite](../spec/15_external-api-surface.md#1546-conformance-test-suite) | The identifier of one start among several starts of a session on one connection. | None (D2). |
| Multiplexing text | §28.5.3, "a pod multiplexes every session's stream over the one channel, keyed by `sessionId`"; [§28.6 Exclusivity and concurrency model](../spec/28_communication-channels.md#286-exclusivity-and-concurrency-model); [§29.1 Participants and trace notation](../spec/29_communication-scenarios.md#291-participants-and-trace-notation); §4.7.10, "multiplexed by `sessionId`" | The statement that one runtime connection carries several sessions. RP retires it for session-mode pods, and this design removes its last case. | RPR-18. |
| Runtime author material | [§15.4.5 Runtime Author Roadmap](../spec/15_external-api-surface.md#1545-runtime-author-roadmap) item 11; [§26.1 Catalog Overview](../spec/26_reference-runtime-catalog.md#261-catalog-overview); [§29.2 Session start](../spec/29_communication-scenarios.md#292-session-start), the service-mode note and the `type: agent` qualifiers; [§17](../spec/17_deployment-topology.md) examples with `type: agent` | Mentions of the removed modes and qualifiers that only contrast with them. | Plain statements about runtimes. |

### Designs

About 245 lines across 24 designs match. No design under `designs/` depends on either mode for its own purpose: each carries a row, a clause, or a refusal for them. The largest are [`egress-and-git-design.md`](egress-and-git-design.md) (52 lines, with a whole proposal), [`agent-pod-layout-design.md`](agent-pod-layout-design.md) (33), [`runtime-protocol-design.md`](runtime-protocol-design.md) (24), and [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) (22). "Instructions for reviewers of other designs" lists each design's change. The workspace materialization, security oversight, LLM upstreams, LLM response cache removal, GitHub credential add-on, and GitLab credential add-on designs mention neither mode.

| Rule or mechanism | Source | What removing it takes away | Replacement |
| :-- | :-- | :-- | :-- |
| Service-mode and `type: mcp` pods, and the supervisor in their runtime containers | PL-1, PL D6, PLR-2, PLR-19 | Two extra pod layouts and the launch at pod start. | The PL-1 blueprint alone. |
| `maxConcurrentRequestsPerPod` | PL-9, PLR-17; EG decision 58; UCD and UGI "Pod models" | The renamed service-mode capacity field. | None. |
| The service-mode tool refusal and `sessionId` rule | RP O10, RPR-8, RP-3 "Remaining multi-session rules", RP-4 `toolServers`, RP-6, RP "Limits" | The refusal of platform and connector tools on a shared process, and the last multiplexing rule. | Tools on every pod (ARR-7) and RPR-18 alone. |
| The adapter's MCP client to a runtime | RP "Scope"; MP-6 and MP O3; MP "Current state" | Version negotiation with an MCP-server runtime. | None. MP-6's connector client, which kept it, left under CR-1 of [`connector-removal-design.md`](connector-removal-design.md#cr-1-removal). |
| `runtimeMcpServers` in manifest version 2 | RP-6, **Manifest `version: 2`.** | A reserved manifest field. | None. |
| `startId` | MD C1; RP-3 | The open decision. | Settled by D2. |
| `sessionId` on frames other than `session_start` | RP-3 **The session of a frame (RPR-18).** and RPR-18; MD-24 **The session of a frame (RPR-18).** and its leniency example of a frame naming a session the connection does not serve; SIR-3 and SI-2's check table | An optional member checked against the slot's session, and its refusal path. | None (O5 (b)). The member is ignored silently where a runtime still writes it. |
| `target_unsupervised` | SD-P4, SD-1 "What it refuses" and "Supervisors" | A refusal that reduces to a stale row. | The general `registered` check (O8). |
| The export refusal on an unsupervised target | DR-8 "Delegation"; DR "Specification sections affected" (§8.7 and `supportsWorkspaceFiles`) | A refusal that reduces to a `type: mcp` target. | None needed. |
| Derive mode equality | SD-1 "Request"; RR "Terms" (Derive) and "Post-chain point" | A check that always passes. | None. |
| The service-mode tenant pin | EG proposal 0; EG "Egress decision service" (the label visibility bullet and its sub-bullets); TOC "How a CONNECT reaches the right connector" | The pin record, the arbitration, the UID-keyed router, the service-mode listener, and its network and register rows. | None. F-5.2.65 and F-5.2.66 close by removal. |
| Unconstrained trust anchor | EG decision 54, "TLS interception", tier 9 "Anchor constraints" | The pods that carried an unconstrained anchor. | ARR-8. |
| Pods with no per-session policy (service-mode case) | TS decision 15; TS "Effective policy" and "Cross-environment sharing"; DR "Problem", "Scope", and decision 10; UGI "Identity cases" | The service-mode half of the E = ∅ rule. | The concurrent-pod half stays under EG decision 60's fallback. |
| Service-mode refusals | UGI decision 14 and "Pod models"; UCD decision 16 and "Limits under both deliveries"; CA "Placement" and "Limits"; PM-4 `allowSharedPod` and `allowReusedPod`; TSS-6 | Refusal rows and clauses for a pod model that no longer exists. | None needed. |
| `sharedPod` service clause | PM-4 "What `sessionIsolationLevel` reports"; PL "Terms" (Shared pod) | The service-mode case of a shared pod. | `maxConcurrentSessionsPerPod` above 1 alone. |
| `conversationContinuity` in the hold rule | AM "Holding a session"; CT "Terms" (Held session) and CT-5; MD worked example | A condition that is always met. | The capability condition alone (O4). |
| Sessions that cannot become dormant | SL-6 | The `type: mcp` and service-mode exceptions. | None needed (ARR-9). |
| `allowDelegation` refusal on `type: mcp` | DA-1, DA-4, DA "Specification changes" and "Non-specification changes" | A refusal of a removed type. | None. |
| Fixed fields `type` and `executionMode`; `restartable` | RV "Scope", "Field classes", RV-5, D20, "Checks on the chosen revision" | Two fixed fields and a derived flag computed from `command` and `executionMode`. | None. `restartable` leaves (see "AR-5"). |
| Service-mode router as a quarantine enforcement point; per-pod-incarnation keys for service-mode records | FE "Enforcement points", "Sessions on the pod", "Keys", "Residual risks" | The service-mode cases of quarantine and erasure. | The concurrent-slot case stays. |
| Resource class mapping of service-mode and `type: mcp` pods | BR "Terms" (Agent pod containers), "Resource classes in configuration", "Resource fields on usage events" | Two mappings as one slot. | None. |
| Checkpointing a `type: mcp` pod as one slot | IC "Reading the slot's files" | A pod kind. | None. |
| Per-slot UID exceptions | SI "Scope" and "Limits" | The pods that had no per-session UID. | None. |

### Documentation

45 files under `docs/` carry about 190 matching mentions: about 95 on service mode and `executionMode`, about 55 on `type: mcp`, `mcpEndpoint`, and `runtimeMcpServers`, 12 on `startId`, and about 25 `type: agent` mentions that only contrast with `type: mcp` **[source]**. About 14 passages leave whole:

- `runtime-author-guide/integration-levels.md`, "`type: mcp` runtimes";
- `runtime-author-guide/testing.md`, "`type: mcp` runtimes";
- `runtime-author-guide/publishing.md`, "Via Admin API (`type: mcp`)";
- `runtime-author-guide/runtime-configuration.md`, "Execution mode" and "Type-specific fields";
- `runtime-author-guide/lifecycle.md`, "Service Mode";
- `client-guide/session-lifecycle.md`, "Service mode", with its sequence diagram;
- `operator-guide/configuration.md`, the service-mode block;
- `reference/execution-modes.md`, the decision guide's service bullets, and the page's framing as a choice between modes;
- `getting-started/concepts.md`, the `type: mcp` and `service` entries;
- `about/why-lenny.md`, the runtime-type and service-mode paragraphs;
- `runtime-author-guide/index.md` and `index.md`, the MCP-server runtime paragraphs.

The rest are one-line edits: `startId` in the adapter contract, tutorials, and testing pages; `executionMode` in JSON and YAML examples, the configuration reference, the REST API page, and the error catalog's `INCOMPATIBLE_RUNTIME` row; `mcpEndpoint` in the discovery examples of `api/rest.md`, `api/mcp.md`, and `client-guide/sdk-examples/mcp-sdk.md`; the idle-suspension clauses of `state-machines.md`, `cloudevents-catalog.md`, `webhooks.md`, and `web-playground.md`; and the tenant-affinity bullets of `operator-guide/multi-tenancy.md` and `operator-guide/namespace-and-isolation.md`. Whether `docs/assets/diagrams/recycle-lifecycle.svg` shows service mode was not checked **[unverified]**.

### Tree

About 85 non-test Go files in about 35 packages implement or branch on the modes, of which about 20 only carry the `executionMode` field through. About 100 Go test files, 9 runtime and client SDK source files in Python and TypeScript with their tests, and about 25 chart, CRD, schema, manifest, and migration files are affected **[source]**.

Defined values: `ExecutionMode` is `session` or `service` (`pkg/gateway/runtime/runtimestore/runtimestore.go`, `pkg/controller/poolscaling/strategy/strategy.go`, `pkg/controller/warmpool/pinned.go`, the Runtime CRD, the OpenAPI document, and migration `0167_runtime_definitions_execution_mode_service`). The runtime type is `agent` or `mcp` (`runtimestore.go`, `pkg/adapter/server.go`, `pkg/gateway/mcpfabric/mcptools/delegate_target.go`, `cmd/lenny-ctl/runtimescaffold/validate.go`, the CRD, the OpenAPI document, and the CHECK constraint of migration `0001`). No `mcpIngress` or `capabilities.mcp` exists **[source]**.

| Area | Leaves whole | Changes |
| :-- | :-- | :-- |
| Adapter | `pkg/adapter/mcpruntime.go`, `pkg/adapter/mcp/client.go` (its callers are `mcpruntime.go` and `mcpruntimes`), `pkg/adapter/statelessslot` | `server.go` (`RuntimeKind`), `sessionframes.go` (`startId`, `nextStartID`, and the `RuntimeKindMCP` skips), `supervisedruntime.go` and `exit.go` (`LaunchService`), `preready.go` (`ServiceMode`), and `RuntimeKindMCP` branches in `heartbeat.go`, `connectormcp.go`, `sdkwarm.go`, `session.go`, and `supervisewiring.go` |
| API types and admission | | `pkg/apis/lenny/v1alpha1`: `runtime_types.go` (`Type`, `ExecutionMode`), `sandboxtemplate_types.go` (`ExecutionMode`, `MaxConcurrent`), `sandbox_types.go`, `runtime_restartable.go`; `pkg/admission/pool_config_validator/validator.go` (`effectiveMaxConcurrent` in the termination-grace floor) |
| Controllers | | `poolscaling/controller.go`, `poolstoresource.go`, and `strategy/strategy.go` (`ModeService`); `warmpool/pinned.go` and `controller.go`; `sandbox/supervision.go` and `sandbox/podspec/supervisor.go` (`LaunchService`); `runtime/controller.go` |
| Gateway routing | `pkg/gateway/runtime/tenantaffinity`, `statelessproxy`, `statelessrouting`, `pkg/gateway/mcpfabric/mcpruntimes`, `cmd/lenny-gateway/statelessrouting_wiring.go` | `runtimestore` (enums, `merge.go`, `pgstore`), `poolstore` (`MaxConcurrent`, `ValidateServiceConfig`, the `max_concurrent` column), `cmd/lenny-gateway/httpsurface.go` (the `/mcp/runtimes/{name}` mount) and `workers.go` |
| Delegation and discovery | | `mcptools/mcptools.go` (`mcpEndpoint` stamping), `mcptools/delegate_target.go` (`targetKindMCP`), `delegation/service.go` (the `type: mcp` refusal), `sessionserver/runtimes.go` |
| Sessions | | `sessionserver`: `pools.go`, `pod_claim.go`, `pod_launch.go` (the claimless path), `isolationlevel.go`, `finalize.go`, `replay_runtime.go`, `pool_policy_mirror.go`, and field plumbing in `create.go`, `create_and_start.go`, `derive.go`, `lifecycle.go`, `replay.go`, `route.go`, `session_response.go`, and `delegated_child_materialize.go`; `podlifecycle/podsession/resolve.go`; `session/sessionidle/sessionidle.go` (the idle exemption); `session/sessionstore` (the `execution_mode` column); `session/executor/subprocess.go` (`devLoopStartID`) |
| Admin API | | `externalapi/admin/pools.go`, `bootstrap_resources.go`, `bootstrap.go`, `runtimes.go` (the `integrationLevel` and `agentInterface` rules on `type: mcp`), `sessions.go`; `externalapi/openapi/openapi.json`; `externalapi/errorclassify/errorclassify.go` (`INCOMPATIBLE_RUNTIME`) |
| Metrics | | `metrics/gatewaymetrics/gatewaymetrics.go` and `gatewaymetrics_misc.go` (`lenny_service_requests_total`, `lenny_service_concurrent_active`), `gatewaymetrics_experiment.go` (the `session_type` label carries the mode) |
| Pod state and backup | | `pkg/podregistry`, `pkg/agentpodstate` (`PodRecord.ExecutionMode`), `pkg/ops/backup/runner/export.go`, `pkg/ops/mcp/generated_tools.go`, `pkg/embedded/stack` |
| Commands | `cmd/runtimes/mcp-reference` and its `Makefile` entry | `cmd/lenny-adapter/main.go` and `supervise.go` (`LaunchService`), `cmd/lenny-compliance` (`startId`), `cmd/lenny-ctl/runtimescaffold/validate.go` and its templates, `cmd/runtimes/echo-concurrent` and `cmd/runtimes/streaming-echo` (`startId`) |
| SDKs | | Runtime SDKs in Go, Python, and TypeScript (`startId` echo and duplicate-start answers, the `sessionId` routing tables, and `sessionId` on frames other than `session_start`); client SDKs in Go, Python, and TypeScript (`executionMode` on the isolation level) |
| Charts, schemas, and migrations | | CRDs `lenny.dev_runtimes`, `lenny.dev_sandboxes`, and `lenny.dev_sandboxtemplates` and their copies in `pkg/embedded/crds/` (with the `executionMode` printer column); `pkg/embedded/manifests/manifests.yaml`; `charts/lenny/templates/reference-runtimes.yaml`; the commented example in `charts/lenny/values.yaml`; `schemas/lenny-adapter-jsonl.schema.json` (`startId` required on `session_start` and `session_started`, and `sessionId` on session-scoped frames) and `schemas/examples/jsonl.session_start*.json`; migrations `0001`, `0040_warm_pool_concurrency`, and `0167` read by a new migration |

The Helm values define no service-mode or `type: mcp` setting, and `schemas/lenny-adapter.proto` carries no runtime type, mode, or `start_id` **[source]**.

The tree serves a route the specification does not state: `pkg/gateway/runtime/statelessrouting` dispatches `/v1/stateless/{pool}/...` to the service-mode proxy **[source]**.

Shared machinery that stays: `pkg/gateway/storage/slotcounter`, `pkg/gateway/runtime/slothealth`, the session stores' `ReserveSlotUnderLock`, and `recycle/scrubreporter_seams.go` also serve `maxConcurrentSessionsPerPod`, so their `maxConcurrent` parameters are renamed rather than removed. The `echo-concurrent` runtime and the `concurrent_*` tests exercise pods with several slots in session mode and stay, less their `startId` handling **[source]**.

### Root records and tests

- [`BUILD-GAPS.md`](../BUILD-GAPS.md): F-5.2.65 and F-5.2.66 (both High and open) close by removal. F-15.1.76 loses its `executionMode` half and keeps its `runtimeRef` half. 23.1-INFO-007 closes. F-5.2.69 stays, because it concerns `maxConcurrentSessionsPerPod` **[source]**.
- [`TESTING.md`](../TESTING.md): §13.26 "Phase 12b — `type: mcp` runtime support" leaves, and §13.27 loses its service-mode half; the reference to `tests/tier4_integration/mcp_runtime_endpoints_test.go` leaves.
- [`BUILD-PLAN.md`](../BUILD-PLAN.md) and [`README.md`](../README.md): the `type: mcp` and service-mode lines leave. `ROADMAP.md` keeps its entry (O2).
- `tests/`: `tier4_integration/mcp_runtime_lifecycle_test.go`, `tier3_contract/mcp_runtimes/endpoint_test.go`, `tier5_e2e_kind/execution_modes_test.go` (whose tests exercise `sessionPolicy` presets and move), `tier2_component/migrations/prod_columns_test.go`, `tier3_contract/adapter_jsonl/session_scoped_frame_address_test.go`, `tier7a_load_local/session_frame_pairing_race_test.go`, `tier11_docs/slot_definition_glossary_reconciliation_test.go` and `session_suspension_reader_doc_reconciliation_test.go`, the `tier9_security` tenant isolation and pool admission tests, the migration test of `0167`, and the registers `tests/spec-map.json`, `tests/change-graph.json`, and `tests/registers/*.yaml`.

## AR-1 The runtime type

**What leaves.** The `type` field and its values (O1). With it:

- the dedicated endpoint `/mcp/runtimes/{name}`, its MCP capability negotiation, and the implicit session it creates per connection, in §4.1, §9.1, §15, §12.8, and §25.12;
- `mcpEndpoint` on `AuthorizedRuntime`, `GET /v1/runtimes`, `list_runtimes`, and `lenny/discover_agents`;
- the adapter's MCP client to a runtime and `RuntimeKind`, and the gateway package `mcpruntimes`;
- capability inference from `ToolAnnotations` at runtime registration, `mcpRuntimeFilters` on environments, and the gateway's cache of runtime tool schemas;
- `runtimeMcpServers` in the adapter manifest;
- the refusals that exist only for `type: mcp`: `integrationLevel` and `agentInterface` on `type: mcp`, `allowDelegation` on `type: mcp` (DA-1), the delegation refusal `target_not_an_agent` (§8.2), and the export refusal on an unsupervised target (DR-8);
- the idle exemption of §6.2 and SL-6;
- the `mcp-type` `safety_factor` of §4.6.2 and §17.8.2;
- the reference `type: mcp` runtime, build phase 12b, and the documentation listed under "Documentation".

**What stays.** Everything in the left column of the table under "What stays: MCP as a protocol".

**Errors.** `target_not_an_agent` leaves the delegation error set. The `INVALID_RUNTIME` messages that name `type: mcp` leave, and `INVALID_RUNTIME` stays for its other causes.

## AR-2 Service mode

**What leaves.** `executionMode` (O2) and its `service` value, with:

- `maxConcurrentRequestsPerPod`, formerly `maxConcurrent`, on the pool, the `SandboxTemplate`, the pool store, and the admin API, and the `max_concurrent` column;
- the service-mode slot, the service-mode session contract, and the claimless path through create, start, claim, and launch;
- tenant-affinity routing, the gateway's in-memory tenant-to-pod map, its EndpointSlice watch, the `/v1/stateless/{pool}` route, and the packages `tenantaffinity`, `statelessproxy`, `statelessrouting`, and `statelessslot`;
- the egress design's proposal 0: the durable service-mode tenant pin, the label arbitration, the UID-keyed router, the snapshot age limit, the service-mode listener in the adapter with its pod-UID check, the agent ingress and gateway egress rows, the §28 register entry, the §12.8 retirement of service-mode pods before their pin records, the §12.9 classification of pin records, and the §25.11 rebuild of pin records from pod labels;
- the service-mode scaling formula, its demand signal, and the metrics `lenny_service_requests_total` and `lenny_service_concurrent_active`; the experiment metrics' `session_type` label loses its meaning and leaves;
- the service-mode case of the admission webhook's termination-grace floor;
- `executionMode`, `conversationContinuity`, and the service clauses of `podReuse` and `residualStateWarning` in `sessionIsolationLevel` (O4);
- `INCOMPATIBLE_RUNTIME` and the derive's mode equality;
- the service-mode pod of PL-1, the launch at pod start, and the service-mode `Sandbox` column of §4.7.10;
- the service-mode tool refusal (RP O10, RPR-8) and the service-mode `sessionId` rule (RP-3, MD-24);
- every service-mode refusal and clause listed under "Designs", and the service-mode half of build phase 12c.

**What stays.** Pods with several slots, `maxConcurrentSessionsPerPod`, `acknowledgeSharedPod`, recycling, tenant pinning of session-mode pods and its label webhook, and the claim queue. The gateway subsystems' `maxConcurrent` settings are unrelated and stay.

## AR-3 Runtime protocol frames

**`startId` leaves (D2).** Under [SI D4](session-isolation-design.md#decisions) a runtime process receives one `session_start` in its life, so `startId` tells apart nothing on a session-mode pod, and service mode was the one case that might have needed it **[source]**. The adapter writes `session_start` without `startId`, the runtime answers `session_started` without it, and §28.5.3's rule that the adapter drops a `session_started` whose `startId` is not that of the open sequence becomes a rule that the adapter accepts the first `session_started` after the `session_start` it wrote and drops any later one with a `runtime_warning`. A runtime that still sends `startId` is handled under the leniency rules of "MD-24": the member is an unknown member of a known frame and is ignored with no report **[design]**. `schemas/lenny-adapter-jsonl.schema.json` stops requiring it, and its examples drop it.

**No multiplexing remains.** The "Remaining multi-session rules" of RP-3 lose both of their entries, so the section leaves. The runtime SDKs' tables that route frames by `sessionId` and answer duplicate starts leave, because a process serves one session.

**`sessionId` only on `session_start` (O5 (b), D5).** No frame other than `session_start` carries `sessionId`. The JSONL schema drops the member from every other frame, the SDKs stop writing it, and the frame examples of the specification and the message delivery design drop it. A runtime that still writes it on another frame is unaffected, because the adapter ignores an unknown member silently; the adapter takes the frame's session from its connection's slot (RPR-18). `session_start` keeps `sessionId` as data the runtime reads: the identifier it logs under so that support can correlate its logs with the platform's, an identity that survives a restart of its process, the key it builds idempotency keys from for actions outside the session, and the value that tells it its session was derived, because a derived session has a new identifier over a restored workspace. The adapter never acts on it **[design]**.

**Tools on every pod.** RPR-8 becomes "Platform tool calls reach the gateway from every runtime process", and RP-4's `toolServers` lists `platform` for every session, which is its only value since CR-1 of [`connector-removal-design.md`](connector-removal-design.md#cr-1-removal) removed connector tool servers.

## AR-4 Admission of removed fields

**Refusal (O3).** The admin API's runtime and pool writes, the bootstrap seed, and `lenny-ctl runtime validate` refuse a document that names `type`, `executionMode`, or `maxConcurrentRequestsPerPod` with `400 VALIDATION_ERROR`, whose `details.field` names the member and whose message reads, for example, "`type` was removed: Lenny runs agent runtimes in session mode only" **[design]**. The refusal holds whatever value the member carries, `agent` and `session` included, so a document is either current or refused. The CRDs drop the fields from their structural schemas; whether `kubectl apply` refuses an unknown field under its default field validation is owed ("Validation owed").

**Stored data.** Lenny has no deployments, so no stored resource or row carries a `type: mcp` runtime or a service-mode pool **[source]**. A new migration drops `runtime_definitions.type` with its CHECK constraint, `runtime_definitions.execution_mode` with its CHECK constraint, `sandbox_warm_pools.max_concurrent`, `sessions.execution_mode`, and the pod-registry and agent-pod-state `execution_mode` columns. Applied migrations are not edited, because the migration tests read the chain as written **[inference]**. The backup export stops writing the columns.

**Why a refusal rather than silence.** A silent drop would register an MCP server image as an agent runtime, and the error would surface at the first bind as a handshake failure that names no field (O3).

**One rule in every design (D6).** The owner made O3's refusal the rule for every field a design removes. A write that names a removed field is refused with `400 VALIDATION_ERROR`, whose `details.field` names the member, whatever its value. The LLM response cache removal design applies it to `cachePolicy` and `cacheScope` (its O3), and the LLM upstreams design's LU-4 applies it to `deliveryMode`, `credentialPolicy`, `supportedProviders`, `credentialCapabilities`, and the other fields it removes. Each design lists its own removed fields, and the admin validator holds one list of removed field names, so each refusal is one entry in the same check **[design]**.

## AR-5 What simplifies in the other designs

- **One pod layout.** Every pod is the session-mode pod of PL-1: the adapter, `netagent`, and one slot container per slot with the supervisor and the runtime. PL D6 is moot, because every runtime container is a slot container. PLR-2's `command` on every runtime holds without its `type: mcp` clause.
- **Every runtime has a supervisor, a workspace, and slots.** SD-P4's supervisor refusal and DR-8's export refusal have no target left, so both leave, and `target_unsupervised` leaves the derive refusal reasons. The stale-row concern they carried moves to a general check (O8). DR's grant condition that the base is `type: agent` and `executionMode: session`, and DR decision 10 on service-mode bases, leave.
- **One frame contract.** "AR-3".
- **One routing path.** Every session is created, routed through the `PreRoute` chain, claimed, and bound. A derive compares no execution modes.
- **No trust-anchor exception.** `netagent` issues the name-constrained anchor on every agent pod, because no runtime process runs before its session's binding. The rule that the TLS interception bundle is per deployment or pool stays, because warm-up work still runs before any tenant.
- **No service-mode egress pod model.** The "Pod models" table of the egress design loses its service-mode row. The pods bound to a tenant without a session are then pinned idle pods and warm pods of tenant-owned pools.
- **No E = ∅ rule for service mode.** Tenant scoping's "Pods with no per-session policy" keeps only concurrent pods, and only where the per-slot attribution of EG decision 60, settled as (a) on 2026-11-13, is unavailable.
- **One stale-row check.** SD-P4 and DR-8's export refusal leave, and DR-4 checks at the post-chain point that the final runtime row of every create, derive, and delegated child is `registered`, and that a platform-derived runtime's base row is too, under its "Registered rows" paragraph (O8).
- **Fewer refusals.** The service-mode rows of the user git identity, user credential delivery, placement, and tenant security settings designs leave. The account-link check of [UC-3](user-credential-delivery-design.md#uc-3-the-usercredential-binding) applies to every session.
- **Every session can become dormant.** SL-6's exceptions leave; the checkpoint policy alone decides.
- **One scaling formula and one `safety_factor` per tier.**
- **`restartable` leaves.** `Spec.Restartable()` returns true for a runtime with `command`, `executionMode: session`, and no `preConnect` **[source]**. PLR-2 requires `command` on every runtime, PL-8 removes `preConnect`, and this design removes `executionMode`, so the flag is always true, and its reader, `recycle.runtimeProcess: restart`, left with SI D4 **[source]**. The flag, its mirror into the runtime registry, and `runtime_restartable.go` leave.

## AR-6 What users of the removed modes do instead

- **An MCP server reachable over the network.** Run it as an operator service. The harness of an agent runtime reaches it as an MCP client through the pod's egress, the runtime's own configuration names its URL, and a credential binding on its host attaches the credential ([`connector-removal-design.md`, "CR-3 External services"](connector-removal-design.md#cr-3-external-services)). Lenny keeps no record of the service, and the service owns its tool rules, approvals, and per-call audit. [§9.3](../spec/09_mcp-integration.md#93-connector-definition-and-oauthoidc) is a successor pointer to that design's CR-1, and the catalog sentence of [§26.1](../spec/26_reference-runtime-catalog.md#261-catalog-overview) states this path ("Specification changes").
- **An MCP server on a customer's private network.** An agent runtime reaches it over the pod's egress through the tenant outbound connector, on an outbound connector route ([`tenant-outbound-connector-design.md`, "Scope"](tenant-outbound-connector-design.md#scope); [`connector-removal-design.md`, "CR-3 External services"](connector-removal-design.md#cr-3-external-services)).
- **An MCP server that needs Lenny's isolation, workspace, and credentials.** Package it with an agent runtime that launches it as a stdio MCP server inside the slot, as harnesses such as Claude Code launch configured servers. It then runs under the slot UID with the session's workspace, egress, and credentials **[inference]**.
- **A stateless agent served as a replicated service.** Run it in session mode. A pool with `maxConcurrentSessionsPerPod` above 1 and `acknowledgeSharedPod` serves several sessions per pod, and recycling reuses pods across sessions. Each session gets its own process, workspace, and egress scope, which service mode did not give. An agent that needs no session at all runs outside Lenny as an operator service, which agent runtimes reach through egress (CR-3).

## Limits

- **Session cost for stateless work.** Each request that service mode served from a running process now costs a claim, a bind, materialization, and a process launch. The owner noted on 2026-11-12 that runtime start-up times are small and need no validation **[source]**.
- **No hosted MCP servers.** Lenny no longer runs an unmodified MCP server in a pod it manages (O6).
- **Client-visible removals.** `mcpEndpoint`, `executionMode`, and `conversationContinuity` leave public responses and the client SDKs. No client depends on them **[source]**.

## Alternatives rejected

- **Keep `type: mcp` and remove service mode alone.** `type: mcp` keeps the second pod rendering, the adapter's MCP client, the dedicated endpoint, the `ToolAnnotations` path at registration, the discovery fields, the delegation refusal, and the idle and safety-factor exceptions, for a mode whose endpoint has never served a call in production **[source]**.
- **Keep service mode as session mode with zero-cost binds.** It would need the claimless routing and the tenant pin that the egress design's proposal 0 builds, which is what this design removes.
- **Keep the fields with one value.** O1 (b) and O2 (b).

## Validation owed

- Whether `kubectl apply` of a `Runtime` or `SandboxTemplate` that names a removed field fails under the default server-side field validation, or only warns and prunes (O3).
- Whether session creation and delegation refuse a runtime row that is not `registered` today. O8's check applies either way, and the answer sizes the change in the tree.
- Whether any caller outside the tree reads `/v1/stateless/{pool}` or `/mcp/runtimes/{name}`; the tree has none apart from the packages listed **[source]**.
- Whether `docs/assets/diagrams/recycle-lifecycle.svg` shows service mode.

## Specification changes

| Section and heading | Change |
| :-- | :-- |
| [§2 Goals and Non-Goals](../spec/02_goals-and-non-goals.md#2-goals-and-non-goals), **Goals** | The runtime-types bullet states one kind of runtime. The execution-modes bullet becomes a bullet on `sessionPolicy`. |
| [§4.1 Edge Gateway Replicas](../spec/04_system-components.md#41-edge-gateway-replicas) | The dedicated MCP endpoints bullet leaves. |
| [§4.6.1 Warm Pool Controller (Pod Lifecycle)](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle) | The `SandboxTemplate` row loses `executionMode`. |
| [§4.6.2 PoolScalingController (Pool Configuration)](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration) | One `safety_factor` per tier; "and for service-mode base pools" leaves. |
| [§4.7.6 Adapter Manifest Field Reference](../spec/04_system-components.md#476-adapter-manifest-field-reference) | `runtimeMcpServers` and its sentence leave. |
| [§4.7.9 Startup Sequence for `type: agent` Runtimes](../spec/04_system-components.md#479-startup-sequence-for-type-agent-runtimes) | Renamed "Runtime Startup Sequence" through the anchor-redirect map (O7). |
| [§4.7.10 Deployment Model](../spec/04_system-components.md#4710-deployment-model) | The `type: mcp` sentence, every service-mode clause, the "Service-mode pod" column of the `Sandbox` phase table, and "multiplexed by `sessionId`" leave. |
| [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime) | `type` and its paragraphs leave; **Standalone Runtime** loses `type` and `executionMode`; **`command`** loses its type condition; **Inheritance Rules** and **Normative Merge Algorithm** lose `type` and `executionMode`; **`agentInterface` Field** loses its `type: mcp` sentence; **Capability Inference from MCP `ToolAnnotations`** leaves, because its other use, at connector registration, leaves under CR-1 of [`connector-removal-design.md`](connector-removal-design.md#cr-1-removal), with `capabilityInferenceMode`, `toolCapabilityOverrides`, and the derived-runtime merge row under D10 of that design; `restartable` leaves; the minimal configuration example loses `type`. A write naming a removed field is refused (ARR-3). |
| [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) | Renamed "Pool Configuration" (O7). **Execution Modes** is restated as session policy, and the **`service`** definition, the service-mode slot, **Session contract (service mode).**, **Tenant isolation (service mode).**, the limitations and preferred-alternative boxes, and the service clauses of **Tenant pinning** and **Client visibility of weak-isolation reuse** leave. **Execution Mode Scaling Implications** becomes "Scaling Implications" with the session bullets alone. |
| [§6.1 What a Pre-Warmed Pod Looks Like](../spec/06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) | The `service` row leaves, if PL-8 has not removed the table already. |
| [§6.2 Pod State Machine](../spec/06_warm-pod-model.md#62-pod-state-machine) | The `type: mcp` idle exemption leaves. |
| [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow) | `sessionIsolationLevel` loses `executionMode` and `conversationContinuity`; `podReuse` and `residualStateWarning` lose their service clauses; the diagram label loses `executionMode` (O4). |
| [§8.2 Delegation Mechanism](../spec/08_recursive-delegation.md#82-delegation-mechanism) | The `target_not_an_agent` refusal leaves. |
| [§8.3 Delegation Policy and Lease](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), **Discovery scoping** | The `type: mcp` sentence leaves. |
| [§9.1 Where MCP Is Used](../spec/09_mcp-integration.md#91-where-mcp-is-used) | The gateway-to-`type:mcp` row leaves; **Runtime Discovery** loses `mcpEndpoint`; **Platform MCP Server Tools** loses its `type: agent` qualifier. |
| [§10.6 Environment Resource and RBAC Model](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model) | `mcpRuntimeFilters`, its YAML, and the runtime tool schema cache bullet leave. |
| [§12.5 Artifact Store](../spec/12_storage-architecture.md#125-artifact-store) | Checkpoint retention loses the service-pool clause. |
| [§12.6 Interface Design](../spec/12_storage-architecture.md#126-interface-design) | `PodRecord` and `PodSpec` lose `ExecutionMode`. |
| [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces) | **Lifecycle fence and drain.** loses the implicit sessions of `type: mcp` endpoints. The egress design's service-mode retirement before pin records is not added. |
| [§15 External API Surface](../spec/15_external-api-surface.md#15-external-api-surface), **Shared Adapter Types**, the `type: mcp` endpoints paragraph, and **Open Responses conversation sessions.** | `McpEndpoint` and the `type: mcp` comment leave; the endpoints paragraph leaves; the hold rule loses its `conversationContinuity` condition. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | `GET /v1/runtimes` loses `mcpEndpoint`; the `/mcp/runtimes/{runtime-name}` entry and the `INCOMPATIBLE_RUNTIME` row leave; **Session Replay Semantics** leaves with replay under the session derive design. |
| [§15.4.1 Message Format and Binary I/O Requirements](../spec/15_external-api-surface.md#1541-message-format-and-binary-io-requirements), "`MessageEnvelope` — Unified Message Format", and [§15.4.3 Runtime Integration Levels](../spec/15_external-api-surface.md#1543-runtime-integration-levels), the **MessageEnvelope fields** row | The envelope the adapter writes to a runtime carries no `sessionId`, and the statement that a runtime echoes `sessionId` on the session-scoped frames it emits leaves (O5 (b)). The message delivery design stages the envelope row, and the runtime protocol design replaces §15.4.3 with "Runtime Capabilities"; this row states only what O5 (b) requires of both. |
| [§15.4.4 Sample Echo Runtime](../spec/15_external-api-surface.md#1544-sample-echo-runtime), [§15.4.5 Runtime Author Roadmap](../spec/15_external-api-surface.md#1545-runtime-author-roadmap), [§15.4.6 Conformance Test Suite](../spec/15_external-api-surface.md#1546-conformance-test-suite) | `startId` leaves; item 11 names pool configuration and `sessionPolicy` alone. |
| [§16.5 Alerting Rules and SLOs](../spec/16_observability.md#165-alerting-rules-and-slos) | `PodClaimQueueSaturated` loses its service-mode parenthetical. |
| [§17.8.2 Capacity Tier Reference](../spec/17_deployment-topology.md#1782-capacity-tier-reference) | The "Pool safety factor (mcp-type)" row and the mcp values of the normative note leave. §17's examples lose `type: agent`. |
| [§18.1](../spec/18_build-sequence.md#181-phasing-intent), [§18.2](../spec/18_build-sequence.md#182-phase-map), [§18.13](../spec/18_build-sequence.md#1813-phase-5--externaladapterregistry-mcpcompletionsopen-responses), [§18.29](../spec/18_build-sequence.md#1829-phase-12b--type-mcp-runtime-support), [§18.30](../spec/18_build-sequence.md#1830-phase-12c--sessionpolicy-presets-and-service-mode) | Phase 12b leaves with a successor pointer; phase 12c keeps the presets; §18.13 loses the `type: mcp` gateway endpoints. |
| [§23 Competitive Landscape](../spec/23_competitive-landscape.md#23-competitive-landscape) and [§23.1 Why Lenny?](../spec/23_competitive-landscape.md#231-why-lenny) | The runtime-types rows, "MCP server hosting", and item 4 are restated. |
| [§25.12 MCP Management Server](../spec/25_agent-operability.md#2512-mcp-management-server), **Architecture** | The sentence contrasting `/mcp/runtimes/{name}` leaves. |
| [§26.1](../spec/26_reference-runtime-catalog.md#261-catalog-overview), [§26.3](../spec/26_reference-runtime-catalog.md#263-claude-code), [§26.7](../spec/26_reference-runtime-catalog.md#267-chat) | The catalog sentence states that an MCP server runs as an operator service that a runtime's harness reaches through egress, or as a stdio server that an agent runtime launches in its slot; the YAML loses `type` and `executionMode`. |
| [§28.3 Registers](../spec/28_communication-channels.md#283-registers) | The service-mode listener entry the egress design planned is not added. |
| [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod) | `startId` leaves the `session_start` and `session_started` member rows, **Inbound: `session_start`**, `session_end`, **Outbound: `session_started`** rule 5, and the examples ("AR-3"); the `type: mcp` row and the multiplexing sentences leave. |
| [§28.6 Exclusivity and concurrency model](../spec/28_communication-channels.md#286-exclusivity-and-concurrency-model) | "`CH-MSGSOCK` multiplexes every session" leaves. |
| [§29.1](../spec/29_communication-scenarios.md#291-participants-and-trace-notation), [§29.2 Session start](../spec/29_communication-scenarios.md#292-session-start) | The multiplexing sentence, the service-mode note, and the `type: agent` qualifiers leave. |
| [README](../spec/README.md), table of contents | Follows the renamed and removed headings. |

## Non-specification changes

- **Code.** Every row of "Tree": the packages that leave whole are deleted, the others lose their mode branches, and the shared slot machinery is renamed. `pkg/gateway/externalapi/admin` and `cmd/lenny-ctl/runtimescaffold/validate.go` gain the refusal of "AR-4".
- **Schemas and charts.** The CRDs, their embedded copies, the OpenAPI document, the JSONL schema and its examples, `reference-runtimes.yaml`, the embedded manifests, the scaffolder templates, the `values.yaml` example, and the kind test manifests drop the removed fields.
- **Migrations.** One new migration drops the columns and constraints of "AR-4", with its component test.
- **SDKs.** The runtime SDKs drop `startId`, the duplicate-start answer, and the `sessionId` routing tables; the client SDKs drop `executionMode` and `conversationContinuity`.
- **Documentation.** Every file under "Documentation". `reference/execution-modes.md` becomes a page on session policy, or merges into the configuration reference. "What the documentation states" lists what the pages carry.

### What the documentation states

The owner asked on 2026-11-13 that every design's documentation list every setting, default, limit, failure mode, and runtime-facing rule it introduces. Published pages cite no specification section numbers.

- **Runtime kinds.** `docs/getting-started/concepts.md`, `docs/runtime-author-guide/index.md`, and `docs/about/why-lenny.md` state that Lenny runs agent runtimes, each session on a slot of a claimed pod, and that MCP stays as the client-facing protocol, the tool protocol of the reference bridge, and the protocol a harness speaks to operator services through egress.
- **Alternatives for the removed modes.** `docs/runtime-author-guide/index.md` and `docs/operator-guide/configuration.md` state the paths of "AR-6": an MCP server reachable over the network runs as an operator service that a runtime's harness reaches through egress, with the page on reaching operator services that [`connector-removal-design.md`](connector-removal-design.md#changes-to-lennys-documentation) adds; one on a private network is reached through the tenant outbound connector from an agent runtime; one that needs Lenny's isolation runs as a stdio server launched by an agent runtime inside its slot; a stateless agent runs in session mode, with `maxConcurrentSessionsPerPod` above 1 and `acknowledgeSharedPod` for density.
- **Refusal of removed fields.** `docs/reference/configuration.md`, `docs/reference/error-catalog.md`, and `docs/runtime-author-guide/publishing.md` state that a runtime or pool write, a bootstrap seed, or `lenny-ctl runtime validate` that names `type`, `executionMode`, or `maxConcurrentRequestsPerPod` is refused with `400 VALIDATION_ERROR` whose `details.field` names the member, whatever its value, `agent` and `session` included.
- **Removed routes.** `docs/api/rest.md` and `docs/api/mcp.md` state that `/mcp/runtimes/{name}` and `/v1/stateless/{pool}` do not exist, and drop `mcpEndpoint` from `GET /v1/runtimes`, `list_runtimes`, and `lenny/discover_agents`.
- **Client-visible fields.** `docs/client-guide/session-lifecycle.md` and `docs/api/rest.md` drop `executionMode` and `conversationContinuity` from `sessionIsolationLevel`, and drop `INCOMPATIBLE_RUNTIME` from the derive's refusals.
- **The stale-row refusal.** `docs/api/rest.md` and `docs/runtime-author-guide/delegation.md` state that a create, a derive, or a delegated child whose final runtime row is not `registered`, or whose platform-derived runtime's base row is not, is answered as an unknown runtime: `404 RESOURCE_NOT_FOUND`, or `TARGET_NOT_IN_SCOPE` for a delegated child, as DR-4's "Registered rows" states; and that a resume is not refused (O8).
- **Runtime-facing frame rules.** `docs/reference/adapter-contract.md` and `docs/runtime-author-guide/lifecycle.md` state that `session_start` and `session_started` carry no `startId`; that the adapter accepts the first `session_started` after its `session_start` and drops a later one with a `runtime_warning`; that no frame other than `session_start` carries `sessionId`; that `session_start`'s `sessionId` is the runtime's to read, for log correlation, identity across process restarts, idempotency keys, and derived-session detection, and the adapter never acts on it; and that a runtime that still writes `startId` or `sessionId` where it no longer belongs is unaffected, because an unknown member is ignored with no warning while an unknown frame type produces a `runtime_warning`.
- **Isolation and idle behaviour.** `docs/reference/state-machines.md`, `docs/reference/cloudevents-catalog.md`, and `docs/client-guide/webhooks.md` state that every session can be idle-suspended and become dormant under its checkpoint policy, and `docs/operator-guide/namespace-and-isolation.md` and `docs/operator-guide/multi-tenancy.md` drop tenant-affinity routing and state that `netagent` issues a name-constrained trust anchor on every agent pod.
- **Scaling.** `docs/operator-guide/configuration.md` and the capacity pages state one `safety_factor` per tier and one scaling formula.
- **Changelog.** A removal note names each removed field, route, frame member, metric (`lenny_service_requests_total` and `lenny_service_concurrent_active`), error (`target_not_an_agent`, `INCOMPATIBLE_RUNTIME`, and `target_unsupervised`), and the renamed headings of O7.
- **Root records.** `BUILD-GAPS.md`, `TESTING.md`, `BUILD-PLAN.md`, and `README.md` as listed under "Root records and tests". F-5.2.65 and F-5.2.66 are closed with a note that the mechanism was removed.

## Tests

| Tier | Test |
| :-- | :-- |
| 0 static | A lint over `spec/`, `docs/`, and `schemas/` finds no `type: mcp`, `executionMode`, `maxConcurrentRequestsPerPod`, `startId`, `mcpEndpoint`, or `conversationContinuity` outside the naming-table rows and the documentation's removal notes. |
| 1 unit | The admin validator refuses `type`, `executionMode`, and `maxConcurrentRequestsPerPod` with any value, `agent` and `session` included, naming the field (ARR-3). The adapter writes `session_start` without `startId`, accepts the first `session_started`, and drops a second one with a `runtime_warning`; a `session_started` carrying `startId` is accepted, and the member is ignored with no warning (ARR-5). A session-scoped frame that carries a `sessionId` member, naming the slot's session or another, acts for the slot's session, with no refusal and no warning, and the adapter takes no action on the `sessionId` of `session_start` (ARR-6). The scaling strategy has one mode, and `safety_factor` has one value per tier (ARR-12). |
| 2 component | The runtime, pool, session, pod-registry, and agent-pod-state stores read and write without the dropped columns, and the new migration drops them and their constraints. |
| 3 contract | The JSONL schema validates `session_start` and `session_started` without `startId`, defines `sessionId` on `session_start` alone, and validates a session-scoped frame that carries an extra `sessionId` member. The OpenAPI document has no `executionMode`, `type`, `mcpEndpoint`, `maxConcurrent`, or `conversationContinuity`. The SDK contract tests pass without `startId`. |
| 4 integration | `POST /mcp/runtimes/{name}` and `/v1/stateless/{pool}` answer as unknown routes (ARR-4). `GET /v1/runtimes` and `list_runtimes` carry no `mcpEndpoint`. A runtime write with `type: mcp` is refused. A session on any runtime idle-suspends and becomes dormant under its checkpoint policy (ARR-9). A derive onto another runtime is not refused for its mode, and a derive, a create, and a delegated child onto a runtime row that is not `registered` are refused (ARR-10). `sessionIsolationLevel` has no `executionMode` or `conversationContinuity` (ARR-11). |
| 5 end to end on Kind | Every rendered agent pod has the PL-1 blueprint, and no pod launches a runtime process before a bind. A platform tool call succeeds from a pod with two slots (ARR-7). |
| 9 security | `netagent` issues a name-constrained anchor on every pod (ARR-8). The agent ingress and gateway egress policies admit no service-mode listener port. |
| 10 conformance | The runtime conformance suite's session-start category passes for a runtime that sends no `startId`. |
| 11 docs | The glossary and the suspension reconciliation tests drop their service-mode expectations, and no documentation page describes `type: mcp` or service mode apart from the removal note in the changelog. |

The tests that leave are listed under "Tree" and "Root records and tests". The `tier5_e2e_kind/execution_modes_test.go` tests that exercise recycling and concurrent slots move to a session-policy test file.

## Instructions for reviewers of other designs

- [`agent-pod-layout-design.md`](agent-pod-layout-design.md): remove the service-mode pod and `type: mcp` pod paragraphs of PL-1 and the sentence "Every runtime container runs the supervisor (D6)", which becomes "every slot container". Mark D6 moot under this design's D3. Drop "type: mcp included" from PLR-2, the service-mode clause from PLR-19, `maxConcurrentRequestsPerPod` from PLR-17, PL-9's table, PL "Scope", the "Shared pod" term, and the high-level design's rename paragraph. Drop the "Runtime supervisor" term's service-mode and `type: mcp` clauses, the service-mode and `type: mcp` sentences of "Validation owed", "Tests" row 1, the §4.7.10, §5.1, and §5.2 rows of "Specification changes", and the `podspec` row of "Non-specification changes". Drop the instruction to RP about `command` on a `type: mcp` runtime. PL-E no longer renames `maxConcurrent`.
- [`runtime-protocol-design.md`](runtime-protocol-design.md): remove the `type: mcp` bullet of "Scope", the "Service mode" term, the service-mode clause of O10's resolution and of RPR-8, the "Service mode keeps one runtime container" bullet, RP-3's service-mode bullet and its "Remaining multi-session rules" subsection, the service-mode clause of RP-4's `toolServers`, RP-6's service-mode clauses, `runtimeMcpServers` from **Manifest `version: 2`.**, the "Limits" bullet, the service-mode parts of the §5.2 row, the `pkg/adapter` row, test 9, and RP-B. Under O5 (b), remove `sessionId` from every frame other than `session_start`: RP-3's **The session of a frame (RPR-18).** loses the bullet that accepts a matching member and refuses another, RPR-18 loses "a frame that names another session is refused", the tier 1 and tier 9 tests that refuse a frame naming another session check instead that such a frame acts for the slot's session, and the JSONL schema row drops the optional member. State `session_start`'s `sessionId` as data the runtime reads and the adapter never acts on (D5). Drop the request to the message delivery design about `startId`, which D2 settles.
- [`session-isolation-design.md`](session-isolation-design.md): remove the service-mode and `type: mcp` bullet of "Scope", the "Service mode and `type: mcp`" bullet of "Limits", and the service-mode clause of the UGI decision 14 label row. Under O5 (b) no frame other than `session_start` carries `sessionId`, so SIR-3's refusal of a frame naming another session, the matching cell of SI-2's check table, the worked example's refused frame, and the tier 1 test leave: the adapter takes every frame's session from the slot and ignores the member silently.
- [`placement-design.md`](placement-design.md): remove service-mode tenant affinity from the "Rules this design takes over or points to" row, "Service-mode pools fail it" from `allowSharedPod` and `allowReusedPod`, the service-mode clause of `sharedPod`, and the service-mode mention under "Removals".
- [`message-delivery-design.md`](message-delivery-design.md): settle C1 as (c) under this design's D2, remove the service-mode sentences of MD-24 **The session of a frame (RPR-18).** and of MD-4's `consumed` routing, drop "on a pool with `conversationContinuity: \"platform\"`" from the worked example (O4), and drop `startId` from the frame examples and the §28.5.3 row. Under O5 (b), MD-24 **The session of a frame (RPR-18).** states that no frame other than `session_start` carries `sessionId`, that `session_start` keeps it as data the runtime reads and the adapter never acts on (D5), and that a member a runtime still writes on another frame is an unknown member, ignored silently; the leniency rules lose their example of a frame that names a session the connection does not serve; the frame examples that carry `sessionId` drop it, and the tier 4 test row's `sessionId` rule on a service-mode pod leaves.
- [`session-derive-design.md`](session-derive-design.md): remove SD-P4, the `target_unsupervised` row of "What it refuses", the "Supervisors" subsection, and its tier 9 test, replaced by a pointer to DR-4's `registered` check (O8). Remove the `executionMode` equality from "Request", the "Runtime and `executionMode`" term, and the supervisor term's service-mode and `type: mcp` clauses. Restate F-15.1.76 as the `runtimeRef` gap alone.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md): remove DR-8's export refusal under "Delegation", its tier 9 test, and its "Specification sections affected" bullet on §8.7 and `supportsWorkspaceFiles`. Remove the grant conditions on `executionMode: session` and `type: agent`, the `type` and `executionMode` entries of "Fields" and "Template copy", the "Tenant defaults" sentence about a `type: mcp` base, the service-mode bullet of "Scope", the `executionMode` term, and the "Not verified" item on the service-mode Service. Mark decision 10 moot. Restate "Problem" and the "Pods with no per-session policy" term for concurrent pods alone. Add to DR-4 the `registered` check at the post-chain point for create, derive, and delegated child (O8). DF-16 stays closed by DR-3.
- [`egress-and-git-design.md`](egress-and-git-design.md): withdraw proposal 0 and record F-5.2.65 and F-5.2.66 as closed by removal. Remove from "Egress decision service" the service-mode sentences of the label visibility bullet and its sub-bullets **Arbitration.**, **Router.**, **Dial.**, **Order.**, **Lifecycle and scope.**, and **Availability.**; the service-mode row and sentence of "Pod models"; the service-mode clauses of the "Terms" rows for the agent pod, agent UID, and pod models, of the warm-up policy bullet, of the git coordination bullet, of "Effective policy", of "Defaults, replacement, and augmentation", of "Level 1 replacement", and of "TLS interception" **Scope.** (keeping the per-deployment-or-pool rule for warm-up work). Remove the scenario "A chat runtime in service mode calls a tenant API", or restate it on a session-mode pool. Remove the §12.8, §12.9, §25.11, §13.2, and §28 items for pin records and the listener from "Specification sections affected", and the tier 4 "Service-mode pin", tier 5 service-mode row, and tier 9 unconstrained-anchor expectations. Add a decision recording that the anchor is name-constrained on every pod (ARR-8).
- [`runtime-routing-design.md`](runtime-routing-design.md): remove the `executionMode` equality from the "Derive" term and from the session derive row of "Post-chain point", and replace SD-P4 there with DR-4's `registered` check.
- [`runtime-revisions-design.md`](runtime-revisions-design.md): remove the "Fixed" class's `type` and `executionMode`, the "Scope" bullet, RV-5's mentions, the `type` reader of DR-8 and SD-P4 under "Checks on the chosen revision" and D20, the `restartable` paragraph (the flag leaves under "AR-5"), the §5.1 change "Make `type` and `executionMode` fixed", and test 1's refusal of a write changing them. Drop the instruction to DR about the `type` and `executionMode` rows of DR-6.
- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md): restate "Pod with no per-session policy", the "Concurrent-session pool, slot, and service mode" term, the "Effective policy" bullets, the "Cross-environment sharing" row, decision 15, and the tier 5 test for concurrent pods alone, and drop the service-mode request check points. The `caller_type` value `service` is unrelated and stays.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): remove the service-mode and `type: mcp` clauses of the "Agent pod containers" term, "Resource classes in configuration", and the ephemeral storage example of a `type: mcp` pod.
- [`user-git-identity-design.md`](user-git-identity-design.md): remove the service-mode row of "Pod models", service-mode pods from decision 14, the tier 4 test row, the §5.2 item of "Specification sections affected", the service-mode clause of the pod models term, and service mode from "Identity cases".
- [`user-credential-delivery-design.md`](user-credential-delivery-design.md): remove the "Service mode" row of "Limits under both deliveries", service mode from decision 16's refusal, and `maxConcurrentRequestsPerPod` from the §5.2 item of "Specification sections affected".
- [`delegation-availability-design.md`](delegation-availability-design.md): remove the `type: mcp` term row, DA-1's refusal of `allowDelegation` on `type: mcp`, DA-4's `type: mcp` paragraph while keeping its statement on agent runtimes that are MCP clients, and the `type: mcp` parts of the §5.1 specification row and the `pkg/gateway/externalapi/admin` row.
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): remove the "Execution mode" term, the sentence of "Placement" that limits the link check to `executionMode: session`, and the service-mode bullet of "Limits".
- [`client-tools-design.md`](client-tools-design.md) and [`anthropic-messages-api-design.md`](anthropic-messages-api-design.md): restate the "Held session" term, CT-5's and "Holding a session"'s hold rule, and the worked examples without `conversationContinuity` (O4).
- [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md): remove the "Service-mode pod" term, the service-mode router from "Enforcement points", the service-mode sentence of "Sessions on the pod", service mode from the pod-incarnation keys of "Keys" and from "Residual risks", and the service-mode case of the tier 4 sessions test.
- [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md): remove the bullet on pods other than session-mode agent pods under "Reading the slot's files".
- [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md#decisions): record O3 as settled under this design's D6. A pool write that names `cachePolicy` or `cacheScope` is refused with `400 VALIDATION_ERROR` naming the field, in place of "RC-2"'s silent acceptance, and the validation item on unknown pool fields closes.
- [`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md): LU-4 refuses a write that names `deliveryMode`, `credentialPolicy`, `supportedProviders`, `credentialCapabilities`, or any other field it removes with `400 VALIDATION_ERROR` naming the field (D6).
- [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md): remove the `/mcp/runtimes/{name}` passthrough from "Scope", the "`type: mcp` runtime" term, and the adapter's client to a `type: mcp` runtime from the high-level design, O3, "Current state", and MP-6, which then govern the connector client alone. CR-1 of [`connector-removal-design.md`](connector-removal-design.md#cr-1-removal) has since removed the connector client.
- [`session-lifecycle-design.md`](session-lifecycle-design.md): remove "Sessions that cannot become dormant." from SL-6.
- [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md): remove EG-0 from the egress proposal labels row, and restate the platform-scoped justification for `REG-SITEATTACH` without the service-mode pin record as its precedent.
- [`tenant-security-settings-design.md`](tenant-security-settings-design.md): remove "Service-mode pools fail it" from TSS-6's `allowReusedPod` row.
- Every design that writes `type: agent` only to contrast with `type: mcp` (about 41 lines, most in the egress, pod layout, and tenant-derived runtimes designs) writes "agent runtime" or "runtime" under O1 (a). Revision notes and review records keep their wording, because they record what was true when written.

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| AR-A | Removal of `type: mcp`: the endpoint and its implicit sessions, discovery fields, the adapter's MCP client, `mcpRuntimeFilters`, `runtimeMcpServers`, the type-specific refusals, the idle exemption, the `safety_factor` row, the reference runtime, phase 12b, and their documentation and tests ("AR-1"). | None. |
| AR-B | Removal of service mode: the claimless path, tenant-affinity routing and its packages, `/v1/stateless/{pool}`, `maxConcurrent`, the scaling formula and metrics, the `sessionIsolationLevel` fields (O4), the derive equality and `INCOMPATIBLE_RUNTIME`, the service-mode pod, the tool refusal, and the service-mode clauses of the specification and documentation ("AR-2"). It closes F-5.2.65 and F-5.2.66 and withdraws egress proposal 0. | None. It lands before PL-E, so PL-E renames no service-mode field. |
| AR-C | The frame contract: `startId` removal across the specification, the JSONL schema, the adapter, the SDKs, the compliance tool, and the reference runtimes, with the `sessionId` rule of O5 ("AR-3"). | AR-B, and MD-E and RP-A, so the frame tables change once. |
| AR-D | The fields themselves: `type` and `executionMode` leave every resource, row, and interface, the refusal of "AR-4", the migration, the `restartable` removal, the `registered` check of O8, and the heading renames of O7. | AR-A and AR-B. |

AR-A and AR-B go first because they delete code that the pod layout, runtime protocol, and egress proposals would otherwise rewrite. AR-B in particular replaces egress proposal 0, which that design scheduled to run first.

## Sources

This design read no external source. Its inputs are the owner's decision file of 2026-11-12 and 2026-11-13, the specification, the documentation, the designs under `designs/`, the root records, and the tree, all read on 2026-11-13.

## Review history

- 2026-11-13: revision 1, written from the owner's direction of 2026-11-13 and an inventory of the specification, the designs, the documentation, and the tree. Not yet reviewed.
- 2026-11-13: revision 2, from the owner's settlement of O1 to O8 and of `session_start`'s `sessionId`, applied in the cross-design review of that day. Not yet reviewed.
- 2026-11-13: revision 3, from the owner's answers to the consolidated list of open decisions of that day. Not yet reviewed.
- 2026-11-14: revision 4, from the owner's answer "yes" to the simplified O3 of `rest-only-client-api-design.md`. Not yet reviewed.
- 2026-11-15: revision 6, from the owner's decision of 2026-11-15 to remove connectors. Not yet reviewed.

### Revision notes

- Revision 1, 2026-11-13.
  - Records the owner's direction (D1), the items that leave with service mode, `startId` among them (D2), and the moot `type: mcp` supervisor questions (D3).
  - Open decisions O1 to O8, on the `type` field, `executionMode`, the answer to removed fields, `sessionIsolationLevel`, `sessionId` on frames, a replacement for `type: mcp`, heading renames, and the stale-row check.
  - Choices this revision makes within the decisions: the `restartable` flag leaves with `executionMode`; the `session_started` acceptance rule without `startId`; a new migration rather than edits to applied ones; the proposal split.
- Revision 2, 2026-11-13, in the cross-design review of that day.
  - Records the owner's settlement of O1 to O8, "O5 (b), yes to all the others" (D4): O1 (a), O2 (a), O3 (b), O4 (a), O5 (b), O6 (a), O7 (a), and O8 (b), with SD-P4 replaced by DR-4's `registered` check at the post-chain point.
  - Records the owner's answer "ok, let's keep it" on `session_start`'s `sessionId` (D5): the runtime reads it for log correlation, identity across process restarts, idempotency keys, and derived-session detection, and the adapter never acts on it.
  - Corrects revision 1's claim that an unknown member produces a `runtime_warning`. Under "MD-24" an unknown member of a known frame is ignored with no report, and only an unknown frame type produces `runtime_warning`. O5 (b), "AR-3", ARR-6, and the tier 1 test are restated, and a `startId` a runtime still sends is ignored silently.
  - Adds the "Leniency rules" term, the `sessionId` row of the design inventory, the reviewer instructions that remove SIR-3's frame refusal and the optional member from RP-3 and MD-24, and "What the documentation states" under the owner's documentation rule of 2026-11-13.
  - Wording fixes of the fix pass of 2026-11-13, which change no rule: "AR-6" and its documentation entry state, from the tenant outbound connector design's "Scope", that the outbound connector does not carry the gateway's connector calls, and the item leaves "Validation owed"; O8, "AR-5", and the stale-row documentation entry point to DR-4's "Registered rows" for the answer (`RESOURCE_NOT_FOUND`, or `TARGET_NOT_IN_SCOPE` for a delegated child), the base row of a platform-derived runtime, and the resume that is not refused; "Specification changes" gains the §15.4.1 and §15.4.3 row for the envelope's `sessionId`; the egress label row names decision 62, and the DR label row states what this design removed.
  - Wording fixes of the fix pass after the read-only checks of 2026-11-13, which change no rule: O1 to O8, settled, move under "Decisions" with their options and reasoning, and "Open decisions" states that no decision remains open and points there, so the inbound links to `#decisions` land on the record; D4's "Applied in" cell follows; and the designs this one reads against are cited at their current revisions.
  - The last round of the same review, which changes no rule, cites the designs this one reads against at their current revisions.
- Revision 3, 2026-11-13, the owner's answers to the consolidated list of open decisions.
  - D6: O3's refusal is the one rule for removed fields in every design. "AR-4" gains **One rule in every design (D6).**, and the instructions to the LLM response cache removal design, whose O3 the answer settles, and to the LLM upstreams design, whose LU-4 applies the rule to its removed fields, are new.
  - Choice this revision makes within the decision: the admin validator holds one list of removed field names shared by every design's refusal.
- Revision 4, 2026-11-14, the simplified O3 of [`rest-only-client-api-design.md`](rest-only-client-api-design.md).
  - The owner answered "yes": Lenny has no held-session concept and no close-after-turn rule. The "Held session" term becomes "Provider-adapter session", which states that a provider adapter creates ordinary sessions, may shorten their idle timeout, which an operator lengthens through the pool's idle timeout, that a `one_shot` runtime ends its own session, and that the adapter routes follow-ups by conversation fingerprint, as that design's "RO-10" asks of this term. The inventory row and the instruction that name the hold rule keep their wording as the record of what revision 1 found and asked. No rule of this design changes.
  - The status line's reads-against list names the current revision of each cited design that changed in this pass.
- Revision 5, 2026-11-14: the final pass of that day. The "Implicit session" term no longer keeps the built-in adapters' implicit sessions, which leave with the adapters under [`rest-only-client-api-design.md`](rest-only-client-api-design.md), and the documentation item names the REST API page. The status line's reads-against list names the current revision of each cited design.
- Revision 6, 2026-11-15, the connector removal of [`connector-removal-design.md`](connector-removal-design.md) revision 2.
  - The owner decided on 2026-11-15: "Yes, let's drop connectors." Lenny offers no connectors of any kind.
  - "AR-6" restates the alternatives without connectors: an MCP server reachable over the network, and an agent that needs no session, run as operator services that a runtime reaches through egress (CR-3), and one on a private network is reached through the tenant outbound connector.
  - The "What stays" table, the high-level design, ARR-7, "AR-3" **Tools on every pod.**, and "AR-5" **Fewer refusals.** drop connector tools, connector servers, and connector authorization's link check; the account-link check of UC-3 applies to every session. The §5.1 and §26.1 rows of "Specification changes" and the documentation entries follow.
  - "Problem" drops its connector clauses. O6 keeps its recommendation as the record, with a note that connectors have since been removed. The "Operator service" term and the CR label row are new.
  - The status line's reads-against list cites the runtime protocol design at revision 11, the message delivery design at revision 23, and the connector removal design at revision 2.
- Revision 7, 2026-11-15, the final consistency pass of the connector removal, under the owner's decision "Yes, let's drop connectors." and [`connector-removal-design.md`](connector-removal-design.md) revision 3.
  - The owner answered "yes" on 2026-11-15 to "Remove `capabilityInferenceMode`, `toolCapabilityOverrides`, and `tenantRbacConfig.mcpAnnotationMappings`?" (D10 of that design). The specification table's capability inference row no longer keeps the inference at connector registration, and the §5.1 row of "Specification changes" names the two runtime fields among what leaves.
  - Replacement columns that still named connectors as current now name operator services reached through egress: the managed MCP hosting row, the tenant pinning row, the `mcpRuntimeFilters` row, and the MCP client version negotiation row.
