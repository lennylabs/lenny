# Delegation availability

Status: design for owner review, revision 2, 2026-11-09, with no independent review yet. Nothing here is built or staged. It reads against [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`runtime-routing-design.md`](runtime-routing-design.md) revision 11, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 7, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 64, [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12, [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 2, [`security-oversight-design.md`](security-oversight-design.md) revision 47, and [`session-derive-design.md`](session-derive-design.md) revision 15. "Decisions" lists what is settled and what remains open for the owner. It stages no specification edit. The changes go through `change-proposal` after review. Proposals carry the label DA-n, and requirements carry the label DAR-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. |
| **[external]** | Read on an external site on 2026-11-09. "Sources" lists the pages. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The owner asked for this: "If that's not the case today already, the ability for a runtime/pool to delegate should be configurable with a platform/tenant default. If disabled, then delegateTask shouldn't be a tool offered to the runtime. Not sure if the same should apply to other platform tools (I don't think so, but worth double-checking)." The owner later added a thought for this design: "we should probably allow sessions to disable delegation."

It is not the case today. No setting at the platform, tenant, runtime, or pool level makes delegation unavailable, and every session of a `type: agent` runtime is offered `lenny/delegate_task` **[source]**:

- The Runtime fields that concern delegation, `delegationPolicyRef` and `allowSelfRecursion`, decide which targets a session may reach and whether a runtime may repeat in its own lineage. Neither turns delegation off. Pools carry no delegation field, and the tenant record carries none.
- A `DelegationPolicy` with no allow rule permits no target. The gateway applies the policy's rules to `lenny/discover_agents` and to connector authorization, and it does not apply them to `lenny/delegate_task`, so such a policy empties discovery and leaves delegation open ("Current state", F-8.3.14).
- The platform MCP server inside the pod lists every tool the gateway's `/mcp` endpoint registers, for every session, whatever its configuration (F-9.1.13).

One part of the request already has a settled answer. [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26) settled, as its decision 26, that a client creating a session with `delegationLease.maxDepth: 0` disables delegation for that session, that the gateway refuses `lenny/delegate_task` with `403 DELEGATION_DISABLED`, and that the intra-pod tool list omits the tool for that session. Its proposal TS-3 carries the work. This design builds the levels above the session on the same refusal and the same list filtering, and it settles the questions decision 26 leaves open for the session level.

## Scope

**Added:**

- A delegation availability setting at the platform, tenant, runtime, and pool levels, with a platform default and a tenant default, and a resolution rule that combines them with the session level ("DA-1" and "DA-2").
- A call-time refusal that names the level that disabled delegation ("DA-3").
- A per-session platform tool list that omits `lenny/delegate_task` and the tools that depend on it when the session cannot delegate ("DA-4").
- A notification to a running runtime when its platform tool list changes ("DA-5").
- A per-hop rule for a parent that wants its child unable to delegate, using the existing `approvalMode: deny` ("DA-6").

**Changed:**

- §8.5's statement that the delegation tools are "available on the platform MCP server for every delegation-capable pod" becomes the per-session list of "DA-4".
- §8.4's `approvalMode: deny`, which the code applies to the caller's own call, takes the meaning "DA-6" states.
- The platform MCP server declares `tools.listChanged` and sends `notifications/tools/list_changed`.

**Out of scope:**

- Per-tool switches for the other platform tools. "DA-7" reviews each tool and finds an existing control for each, and O7 records the recommendation.
- Which targets a session may reach. `DelegationPolicy` decides that, and the defect that `lenny/delegate_task` does not evaluate the policy's rules is recorded under "Defects found while verifying" for its own proposal. `BUILD-GAPS.md` files it as F-8.3.14.
- An environment level. An environment's `defaultDelegationPolicy` already restricts targets for the sessions created in it ([§10.6](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)).
- Publishing a runtime's delegation availability in discovery (`GET /v1/runtimes`).

## Terms

| Term | Meaning |
| :-- | :-- |
| Delegation availability | Whether a session's configuration allows it to start delegated children. The platform, tenant, runtime, and pool levels decide it ("DA-1"). |
| `allowDelegation` | The availability field on a Runtime and on a pool. On a Runtime it is `true`, `false`, or unset. On a pool it is `false` or unset. |
| Default | A level's value that applies when the runtime and pool say nothing: `allowByDefault` at the platform and the tenant. A more specific level can override a default in either direction. |
| Forbid | A level's switch that makes delegation unavailable whatever the more specific levels say: `forbid` at the platform and the tenant. |
| Room | Whether the session's stored delegation lease allows one more level of depth: the stored `maxDepth` is greater than the session's own depth. A root under `maxDepth: 0` has no room. TS-3 defines the stored value. |
| May delegate | Delegation availability and room together. A session that may delegate is offered `lenny/delegate_task`, and its calls pass this gate. |
| Delegation-dependent tools | `lenny/discover_agents`, whose only use is to find delegation targets, and `lenny/await_children` and `lenny/cancel_child`, whose only use is to manage children. |
| Platform tool list | The answer to `tools/list` on the platform MCP server inside the pod, for the session the adapter resolves as the caller. |
| `disabledBy` | The field of the `DELEGATION_DISABLED` refusal that names the level that made delegation unavailable ("DA-3"). |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Delegation | A session starts a child session through the platform tool `lenny/delegate_task` ([§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism)). Children form a delegation tree under a root session. |
| Delegation lease | The record of what a session may do as a delegator: depth, budgets, and policy references ([§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). A child's lease is narrower than its parent's. |
| `maxDepth` | The lease's depth limit, counted from the root. Today §8.2 requires a positive integer, and TS-3 lets a client set 0. |
| `DelegationPolicy` and `delegationPolicyRef` | A named resource of tag rules that states which runtimes, pools, and connectors a session may target. A Runtime names one in `delegationPolicyRef` ([§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| `allowSelfRecursion` | The opt-in, at the platform, the Runtime, and the `DelegationPolicy`, that lets a runtime and pool appear twice in one delegation lineage ([§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism) cycle detection). |
| `approvalMode` | The lease field of [§8.4](../spec/08_recursive-delegation.md#84-approval-modes) with the values `policy`, `approval`, and `deny`. `approval` is reserved and behaves as `policy` in v1. |
| Platform MCP server | The MCP server the runtime adapter runs inside an agent pod on the abstract Unix socket `@lenny-platform-mcp`. It forwards each `tools/list` and `tools/call` to the gateway under the calling session (`CH-MCP-PLATFORM` in [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). A Basic-level runtime does not connect to it ([§15.4.3](../spec/15_external-api-surface.md#1543-runtime-integration-levels)). |
| Gateway `/mcp` | The client-facing MCP endpoint of the gateway ([§15.2](../spec/15_external-api-surface.md#152-mcp-api)). |
| `type: agent` and `type: mcp` runtime | An agent runtime runs sessions and reaches the platform tools. A `type: mcp` runtime is itself an MCP server that the gateway exposes at `/mcp/runtimes/{name}`, has no platform MCP server, and is refused as a delegation target with `target_not_an_agent` ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime), [§9.1](../spec/09_mcp-integration.md#91-where-mcp-is-used)). |
| Pool | A set of pre-warmed pods for one runtime at one isolation profile and resource class ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). |
| Derived runtime and tenant-derived runtime | A derived runtime names a base in `baseRuntime` and changes some fields under §5.1's merge rules. A tenant-derived runtime is one a tenant creates, with an owned pool copied from a platform template pool ([`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-5-owned-pools)). |
| `PreRoute` chain and post-chain point | The interceptors that settle a session's runtime and pool on every creation path, including a delegated child (`kind: delegate`), and the step after them where every check that depends on the runtime, revision, or pool runs ([`runtime-routing-design.md`](runtime-routing-design.md#post-chain-point)). |
| Live and versioned fields | Under [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions), a versioned Runtime field is copied into an immutable revision, and a live field applies to the next session on every revision. `delegationPolicyRef` and `allowSelfRecursion` are live. |
| `capabilities.deferredToolDiscovery` | The Runtime capability that [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-8-runtime-capability-and-late-tools) adds, which states that the runtime keeps a connector server whose `tools/list` failed and lists again on `notifications/tools/list_changed`. |
| `notifications/tools/list_changed` | The MCP notification a server that declared `tools.listChanged` sends when its tool list changes, after which the client lists again **[external]**. Under MCP `2026-07-28` the client receives it on a `subscriptions/listen` stream it opened with `toolsListChanged: true` **[external]**. |
| Coordinating replica and `CH-HOLDERFORWARD` | The gateway replica that holds a session's pod connection, and the channel through which another replica forwards a request for that session to it ([§28.5.4](../spec/28_communication-channels.md#2854-inter-replica)). |
| `platform-admin`, `tenant-admin`, and `security-admin` | Lenny's administrative roles for the platform, for one tenant, and for one tenant's security record. |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static checks, 1 unit, 2 component, 3 contract, 4 integration on the compose stack, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| TS-3 and decision 26 | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#ts-3-session-environment-context-and-cross-environment-sharing) | TS-3 reworks the delegation gate: the calling session is the only parent a runtime can name, the intra-pod tool surface is allow-listed to §9.1's table, and a client's `delegationLease.maxDepth: 0` disables delegation for a session with `403 DELEGATION_DISABLED` ("Client delegation limits (decision 26)"). |
| RT-2 | [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) | The `PreRoute` contract and the post-chain point. |
| RV-1 | [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions) | The field classes: versioned fields, live fields, and fixed fields. |
| DR-4, DR-5, and DR-6 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#fields) | What a tenant may set on a derived runtime ("Fields" under DR-4), owned pools and their template copy, and how a base change reaches a derived runtime. |
| CA-8 and CA-10 | [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-8-runtime-capability-and-late-tools) | CA-8 adds `deferredToolDiscovery`, `listChanged` on connector servers, and a gateway-to-adapter notification RPC. [CA-10](connector-authorization-and-external-memory-design.md#ca-10-removing-built-in-memory) removes built-in memory and its tools. |
| MP-5 | [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md#mp-5-intra-pod-servers) | The intra-pod MCP servers move to MCP `2026-07-28` and `2025-11-25`. |
| SO-2 and SO-5 | [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture) | SO-2 is the tenant security record of guardrails that a `security-admin` writes. [SO-5](security-oversight-design.md#so-5-delegated-approvers) holds a tool call that matches a rule until a `security-approver` approves it. |

## High-level design

Delegation stays a platform primitive, and an operator can now turn it off where it does not belong. The platform sets a default and can forbid delegation outright. A tenant can replace the platform default for its own sessions and can forbid delegation for the tenant. A runtime can state that its sessions delegate or do not, which overrides both defaults, and a pool can turn delegation off for the sessions it serves. A client creating a session can turn it off for that session and its tree, as decision 26 already settled. The forbids, a pool's `false`, a base runtime's `false`, and the session's choice can only take delegation away.

The gateway decides, on every `lenny/delegate_task` call, whether the calling session may delegate, from the session's row and the live settings, and refuses with `403 DELEGATION_DISABLED` when it may not. The refusal names the level responsible. The platform MCP server inside the pod lists only the tools the session can use, so a session that may not delegate is offered no `lenny/delegate_task` and no tool that only serves delegation. When a setting changes while a session runs, the adapter tells the runtime that its list changed. A runtime that ignores the notice, or cached its list, still meets the refusal.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Levels (DA-1).** `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` in Helm, `delegation.allowByDefault` and `delegation.forbid` on the tenant, `allowDelegation` on the Runtime with a restrict-only rule for derived runtimes, and `allowDelegation: false` on a pool.

**Resolution (DA-2).** A forbid wins, then a pool's `false`, then the runtime's value, then the tenant default, then the platform default. The session's room under TS-3 applies on top. The settings are live: the gateway reads them at each call.

**Refusal (DA-3).** `403 DELEGATION_DISABLED`, which TS-3 introduces, gains `details.disabledBy`. The gateway checks at the top of the `lenny/delegate_task` handler and again in the delegation service.

**Tool list (DA-4).** The gateway answers `ListPlatformTools` for the calling session. It omits `lenny/delegate_task` and `lenny/discover_agents` when the session may not delegate, and omits `lenny/await_children` and `lenny/cancel_child` as well when the session has no child.

**Running sessions (DA-5).** The platform MCP server declares `tools.listChanged` and sends `notifications/tools/list_changed` when the gateway reports a change for the session, and when a new session binds to a runtime process that has listed before.

**Per-hop narrowing (DA-6).** A parent that passes `approvalMode: deny` on `lenny/delegate_task` creates a child with no room, so the child cannot delegate.

**Other tools (DA-7).** Each other platform tool already has a control of its own, so no per-tool switch is added (O7).

```
 lenny/delegate_task from session S (pod or gateway /mcp)
        │
        ▼
 S is the calling session (TS-3)
        │
        ▼
 Availability of S, read live from S's tenant, runtime, and pool:
   platform forbid ─ tenant forbid ─ pool false ─ runtime value ─ tenant default ─ platform default
        │ unavailable ──> 403 DELEGATION_DISABLED, details.disabledBy
        ▼
 Room on S's stored lease (TS-3)
        │ no room ──> 403 DELEGATION_DISABLED (disabledBy: session) or DELEGATION_DEPTH_EXCEEDED
        ▼
 Existing gates: target scope, PreDelegation, PreRoute chain (kind: delegate), post-chain point, cycle, depth, budget
```

"Limits" states what the change costs: a runtime that ignores the notification keeps a stale list, a platform-level change reaches running sessions only at their next call or bind, and the fan-out of a tenant-wide change touches every running session in the tenant.

## Decisions

Settled by an earlier owner decision or by a project rule:

| ID | Decision |
| :-- | :-- |
| S1 | A client disables delegation for one session and its tree with `delegationLease.maxDepth: 0` at creation. The owner settled it on 2026-11-03 as decision 26 of [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26), in the words "yes, add it with maxDepth: 0". O5 records what decision 26 leaves open. |
| S2 | The refusal is the control and the hidden tool is a convenience. TS-3 states this in its words "Hiding is a convenience, and the refusal is the control", and this design applies it to every level. |
| S3 | `allowDelegation` on the Runtime is a live field under [RV-1](runtime-revisions-design.md#rv-1-revisions), because it is policy the platform enforces and does not describe the binary, which is how RV-1 classifies `delegationPolicyRef` and `allowSelfRecursion`. A runtime author who ships a binary that relies on delegation keeps delegation enabled while that binary runs. |
| S4 | No compatibility mode. Nothing is deployed, so the new fields carry no migration path, and the meaning of `approvalMode: deny` changes in place (DA-6). |

Open for the owner. Each carries a recommendation:

| ID | Question | Recommendation |
| :-- | :-- | :-- |
| O1 | How the levels combine. (A) Restrict-only at every level, so any `false` wins and a platform "default" of off can never be turned on below it. (B) Defaults resolved by specificity, a runtime overriding the tenant default and the tenant default overriding the platform default, with a forbid at the platform and the tenant that nothing below lifts, and restrict-only between a runtime, its base, and its pool. (C) As (B) without forbids. | (B). The owner asked for a platform and a tenant *default*, which a more specific level must be able to override, so (A) does not give one. Without a forbid, a tenant that wants no delegation cannot stop a platform runtime that sets `allowDelegation: true`, so (C) leaves the tenant without a hard switch. (B) follows the layering §8.6 uses for lease extension: defaults by specificity, capped by ceilings. |
| O2 | Whether pools carry the setting, and in which direction. | A pool carries `allowDelegation: false` only. The owner named pools, and a pool is the unit an operator sizes and isolates, for example a small `standard` pool for workers beside a `microvm` pool for orchestrators of the same runtime. A pool that could enable delegation against its runtime's `false` would override the runtime author's statement, so a pool narrows. |
| O3 | The platform default. | `gateway.delegation.allowByDefault: true`. §8.1 describes delegation as a platform primitive that every agent runtime can use, a deployment that sets nothing behaves as today, and the depth, fan-out, budget, and tree-size limits of §8.3 still bound every tree. A deployer who wants delegation off by default sets the value to `false`. |
| O4 | How a change reaches a running session. (A) Live in both directions: each call and each list reads the current settings. (B) A snapshot at creation, so a change reaches new sessions only. (C) Live for a change that disables, snapshot for one that enables. | (A), with a best-effort notification ("DA-5"). §8.3 already evaluates a `DelegationPolicy` live at each call, which lets an operator stop delegation during an incident without ending sessions. (B) leaves a disabled runtime delegating for the life of its sessions. (C) needs a stored snapshot beside the live read for no gain the owner asked for. The session's own choice (S1) stays stored, because it is the client's and nothing lifts it. |
| O5 | The session-level switch, from the owner's thought. Whether a new field is needed, whether a session can change it mid-session, and how derive and resume treat it. | Reuse decision 26's `delegationLease.maxDepth: 0` and add no field ("Session level" under DA-2). A session cannot change it after creation. A derive request accepts `delegationLease.maxDepth`, which can only narrow the value TS-3 carries over from the source. A resume keeps the stored value. |
| O6 | What `approvalMode: deny` means. Today the code refuses the caller's own call when the caller passes it, which no caller needs. (A) Make it the per-hop narrowing of "DA-6": the child gets no room. (B) Remove `deny` from §8.4. | (A). A parent has no other way to stop a child from delegating, because `lenny/delegate_task` no longer takes `maxDepth` and a `leaseSlice` with `maxChildrenTotal: 0` means no limit. (A) reuses the field §8.4 already defines as "Delegation not permitted" and stores its effect in the child's lease, where §8.3 says the lease carries it. |
| O7 | Whether any other platform tool needs a per-runtime or per-pool switch. | No. "DA-7" finds an existing control for each tool, and two of them leave the surface under other designs. A deployer who wants a tool held for approval uses an SO-5 rule. |
| O8 | Who writes the tenant forbid and the tenant default, and whether a `tenant-admin` can clear a forbid that a `platform-admin` set. | A `tenant-admin` writes both for its own tenant, and a `platform-admin` writes both for any tenant. A forbid records who set it, and a `tenant-admin` cannot clear a forbid a `platform-admin` set. A customer security team that wants a forbid its own tenant admins cannot clear uses an SO-2 guardrail, which "Instructions for reviewers of other designs" asks SO-2 to consider. |

## Requirements

| ID | Requirement |
| :-- | :-- |
| DAR-1 | A session that may not delegate starts no child, whether the call comes from its pod or from a client at the gateway `/mcp` endpoint naming it as parent. |
| DAR-2 | The refusal names the level that made delegation unavailable. |
| DAR-3 | A platform forbid, a tenant forbid, a pool's `false`, a base runtime's `false`, and a session's `maxDepth: 0` are never lifted by a more specific level or by a later hop. |
| DAR-4 | The platform tool list of a session that may not delegate omits `lenny/delegate_task` and `lenny/discover_agents`, and also omits `lenny/await_children` and `lenny/cancel_child` when the session has no child. |
| DAR-5 | A change at the tenant, runtime, or pool level governs the next `lenny/delegate_task` call of every running session it reaches. |
| DAR-6 | Making delegation unavailable ends no running child, and a parent with children keeps the tools that manage them. |
| DAR-7 | A deployment that sets none of the new values behaves as today. |
| DAR-8 | A session cannot reach a root-session creation tool from its pod, so the setting cannot be bypassed by creating a new root. TS-3's allow-list provides this, and this design depends on it. F-9.1.13 records the defect. |

## Current state

**No availability setting [source].** §5.1 gives the Runtime `delegationPolicyRef` and `allowSelfRecursion` (`pkg/apis/lenny/v1alpha1/runtime_types.go`, `runtimestore.Runtime`). §5.2 gives pools no delegation field. The tenant record carries no delegation field, although it carries `messaging.scope` (§7.2) and `leaseExtension` (§8.6). The Helm values that concern delegation are `gateway.cycleDetection.mode`, `gateway.allowSelfRecursion`, and `gateway.delegation.defaultMaxDepth` (§8.3), and the last is not rendered by the chart (F-8.3.12).

**`maxDepth` cannot be 0 today [source].** §8.2 **Effective `maxDepth` resolution** requires every effective lease to carry a positive integer, and every layer of its chain can only produce one. A client's `delegationLease` at creation is validated, stored, and echoed with no effect (F-8.2.34). TS-3 wires it and admits 0 as decision 26.

**`DelegationPolicy` rules are not applied to `lenny/delegate_task` [source].** `DelegationPolicy.Evaluate` has these production callers: `filterByEffectiveDelegationPolicy` for `lenny/discover_agents` (`pkg/gateway/mcpfabric/mcptools/mcptools.go`), `connectorauthz` for connector calls, and the pool isolation audit. The `lenny/delegate_task` handler (`mcptools_register.go`) checks environment scope through `runtimeAuthorizedForCaller` and `crossEnvReachable`, and `Service.detectCycle` (`pkg/gateway/mcpfabric/delegation/service.go`) reads the *target* runtime's `delegationPolicyRef` for the self-recursion layer and the content policy. No step evaluates the calling session's policy rules against the target. A policy that allows nothing therefore hides every target from discovery and blocks no delegation. The audit note 8.3-H1 in `BUILD-GAPS.md` records the missing call with no checkbox or F-id, and revision 2 files it as F-8.3.14.

**`approvalMode: deny` refuses the caller's own call [source].** §8.4 defines `deny` as "Delegation not permitted" on the lease. `lenny/delegate_task` accepts `approvalMode` as an argument, and `validateDelegation` returns `ErrDelegationDenied` (`DELEGATION_DENIED`) when the caller passes `deny`, before it reads the parent. Nothing stores the value on the child's lease, and nothing sets it on a root, so a runtime can only refuse itself (F-8.4.1's resolution). Revision 2 files this as F-8.4.4.

**How the tool list is built [source].**

- The adapter starts the platform MCP server once per pod (`startPlatformMCP`, `pkg/adapter/platformmcp.go`). Each `tools/list` goes to `platformToolProvider.List`, which resolves the calling session through `callingSession` and forwards `ListPlatformTools` with that session's ID over `GatewayControl` (`pkg/adapter/platformtoolprovider.go`).
- The gateway's `Bridge.ListPlatformTools` (`pkg/gateway/gatewaycontrol/platformtools/platformtools.go`) ignores the session ID and returns `Catalog()` of the gateway's `/mcp` server. Its comment states that "the catalog is uniform across type:agent sessions".
- That catalog holds every tool `mcptools.Register` installs: the tools of §9.1's table, `lenny/vcs_token`, and the client-facing tools such as `lenny/create_session`, `lenny/create_and_start_session`, `lenny/list_sessions`, and `lenny/upload_files`. A runtime can call `lenny/create_session` from its pod and start a new root session outside its delegation lease. TS-3 found this and allow-lists the bridge to §9.1's table. `BUILD-GAPS.md` already records it as F-9.1.13, so revision 2 files no new finding for it.
- `lenny/delegate_task` is never refused for being disabled, because nothing disables it.

**The server cannot announce a change [source].** `initializeResponse` in `pkg/adapter/mcp/server.go` declares `"tools": {}` with no `listChanged`, and the serve loop only answers requests. The server speaks MCP `2025-03-26`, which MP-5 replaces. CA-8 found the same for connector servers and proposes the adapter changes and a gateway-to-adapter RPC, `NotifyConnectorToolsChanged`, for a runtime that declares `deferredToolDiscovery`.

**A process can outlive a session [source].** The platform MCP server is pod-wide, and `callingSession` resolves the caller per request to the single session the pod's shared runtime process holds. §28.5.3 states that a later session's manifest write does not re-arm the server. TS-3 notes that a recycled or SDK-warm process can hold a list from before its current session.

**What MCP requires [external].** Under `2025-11-25`, a server that declares `tools.listChanged` "SHOULD" send `notifications/tools/list_changed` when its tools change, and the client lists again. Under `2026-07-28`, the server sends it on a `subscriptions/listen` stream the client opened with `toolsListChanged: true`. `2026-07-28` also states that the tool set "MAY change over time" and "MUST NOT vary per-connection", that it "MAY vary by the authorization presented on the request", and that every `tools/list` result carries `ttlMs` and `cacheScope`, where `ttlMs: 0` marks the result stale at once and `cacheScope: "private"` forbids sharing it across authorization contexts. Both versions report an unknown tool as JSON-RPC error `-32602`.

**Runtimes and list changes [source, from CA-9].** In the spike of [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#spike-results-for-claude-code), Claude Code 2.1.317 listed again on `notifications/tools/list_changed` and could call a tool added that way. No run tested a tool removed that way, and no Codex run is recorded.

**Precedents for layered settings [source].** §7.2's `messagingScope` resolves deployment, tenant, and runtime as "most-restrictive wins, can only narrow". §8.6's lease extension settings resolve by specificity, runtime over tenant over deployment, capped by a tenant maximum and a deployment maximum.

## DA-1 Levels

| Level | Fields | Written by | Values |
| :-- | :-- | :-- | :-- |
| Platform | `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` (Helm, rendered to gateway flags) | Deployer | `allowByDefault`: `true` (default, O3) or `false`. `forbid`: `false` (default) or `true`. |
| Tenant | `delegation.allowByDefault` and `delegation.forbid` on the tenant record, through `PUT /v1/admin/tenants/{id}/delegation` with `If-Match` | `tenant-admin` for its tenant, `platform-admin` for any (O8) | `allowByDefault`: unset, `true`, or `false`. `forbid`: `false` or `true`, with `forbidSetBy` recorded as `platform` or `tenant`. |
| Runtime | `allowDelegation` | `platform-admin` on a platform runtime. `tenant-admin` on a tenant-derived runtime, under "Derived runtimes" below. | Unset, `true`, or `false`. |
| Pool | `allowDelegation` | `platform-admin`. `tenant-admin` on an owned pool. | Unset or `false` (O2). |
| Session | `delegationLease.maxDepth` at creation, and `approvalMode: deny` on the parent's `lenny/delegate_task` | Client, and the parent session | Under "Session level" in DA-2 and under DA-6. |

**Derived runtimes.** `allowDelegation` merges restrict-only, as `allowSelfRecursion` does under §5.1. A derived runtime may set `false` on any base. It may set `true` only when the base's value is not `false`, and the write answers `422 INVALID_DERIVED_RUNTIME` with `details.field: "allowDelegation"` otherwise. The merged value is `false` when either value is `false`, `true` when either is `true` and neither is `false`, and unset otherwise. A base that later changes to `false` makes the merged value `false` at the next resolve, which DR-6 already lists as a narrowing that "applies at the next resolve". A derived runtime's `true` over an unset base lets a tenant override its own tenant default for that runtime, and it cannot lift a forbid.

**Owned pools.** An owned pool copies `allowDelegation` from its template pool ("Template copy" under DR-5). A `tenant-admin` may set `false` on it and may clear a `false` the tenant set, and cannot clear a `false` copied from the template.

**`type: mcp` runtimes.** A write that sets `allowDelegation` on a `type: mcp` runtime answers `INVALID_RUNTIME: allowDelegation is only valid on type: agent runtimes`, as §5.1 does for `integrationLevel`, because such a runtime has no sessions with platform tools and cannot delegate. A pool of a `type: mcp` runtime refuses the field in the same way.

**Writes and audit.** Each field is part of an existing admin write, so the existing audit rows for runtime, pool, and tenant changes record it. The tenant sub-resource writes a `tenant.delegation_updated` audit event carrying the old and new values and the actor **[design]**.

## DA-2 Resolution

For a session S with tenant T, runtime R (merged with its base when R is derived), and pool P, read live at each call:

| Order | Condition | Result | `disabledBy` |
| :-- | :-- | :-- | :-- |
| 1 | `gateway.delegation.forbid: true` | Unavailable | `platform` |
| 2 | T's `delegation.forbid: true` | Unavailable | `tenant` |
| 3 | P's `allowDelegation: false` | Unavailable | `pool` |
| 4 | R's merged `allowDelegation` is set | That value | `runtime` when `false` |
| 5 | T's `delegation.allowByDefault` is set | That value | `tenantDefault` when `false` |
| 6 | Otherwise | `gateway.delegation.allowByDefault` | `platformDefault` when `false` |

S may delegate when it is available and has room. Room is TS-3's: S's stored `maxDepth` is greater than S's own depth. A session with no room because its stored `maxDepth` is 0 gets `disabledBy: session`. A session with room exhausted at a positive depth keeps today's `DELEGATION_DEPTH_EXCEEDED`, because its tree can delegate and it has reached the limit.

**Examples.** Platform default `true` with no forbid:

| Tenant default | Runtime | Pool | Effective | Reason |
| :-- | :-- | :-- | :-- | :-- |
| Unset | Unset | Unset | Available | Platform default |
| `false` | Unset | Unset | Unavailable, `tenantDefault` | The tenant default replaces the platform default |
| `false` | `true` | Unset | Available | The runtime is more specific than the tenant default |
| Unset | `true` | `false` | Unavailable, `pool` | A pool narrows its runtime |
| `true`, with tenant `forbid: true` | `true` | Unset | Unavailable, `tenant` | A forbid wins over every value below it |

**Live reads.** The gateway reads the settings at each `lenny/delegate_task` call and each `ListPlatformTools` call (O4). The tenant, runtime, and pool rows are read through the caches the gateway already keeps for them, and the platform values are process flags. A change reaches a running session at its next call. This matches §8.3's rule that a delegation policy "is re-evaluated on each `delegate_task` call", and it keeps §8.3's rule that a change "does not affect active child sessions": a child already running continues (DAR-6).

**Delegated children.** A child's own availability is resolved from the child's row, whose runtime and pool are the ones its `PreRoute` chain (`kind: delegate`) settled at the post-chain point. The post-chain point gains no check: availability is a property of the session as a delegator, and a runtime with delegation unavailable can still be a target. Whether it can be a target is the `DelegationPolicy`'s question. The parent's availability is read from the parent's row before the chain runs.

**Session level.** The session level reuses decision 26 and adds no field (O5):

- **Create.** A client sets `delegationLease.maxDepth: 0` on `POST /v1/sessions`, `POST /v1/sessions/start`, and `lenny/create_and_start_session`, as TS-3 wires them. The other existing request fields do not express the switch: `delegationLease.maxChildrenTotal: 0` is stored as `maxDepth: 0` under TS-3, and `delegationLease.delegationPolicyRef` is accepted and has no effect (F-8.2.34), so a client cannot rely on a policy that allows nothing.
- **Narrowing only.** A session's `maxDepth` combines with the other layers by minimum under TS-3, so a session can turn delegation off and can never turn it on where any level makes it unavailable. A session that sets a positive `maxDepth` on a runtime with delegation unavailable gets `disabledBy` from that level.
- **Children.** A child stores its parent's `maxDepth` under TS-3, so a root at 0 has no children, and a parent with room can narrow one child with `approvalMode: deny` (DA-6). The availability levels are not inherited: each child resolves its own runtime and pool, and its tree's stored `maxDepth` still bounds it.
- **Mid-session.** No request changes a session's stored `maxDepth`, and §8.6 already makes `maxDepth` not extendable. An operator who must stop a running session's delegation uses a level above the session, which takes effect at the next call.
- **Derive.** TS-3 stores the source's `maxDepth` minus the source's depth, with a floor of 0. This design adds `delegationLease.maxDepth` to the derive request, which can only lower that value, so a client can derive a session that cannot delegate. The derived session resolves availability from its own runtime and pool, which [`session-derive-design.md`](session-derive-design.md#sd-1-one-route) lets differ from the source's.
- **Resume and recovery.** A resume keeps the row, so it keeps the stored value, and the levels are read live after it. Tree recovery brings a node back on its stored lease (§8.10), and the node's next call reads the levels live.

## DA-3 Refusal

The gateway refuses `lenny/delegate_task` with `403 DELEGATION_DISABLED`, which TS-3 introduces with category `POLICY` and `retryable: false`. This design adds `details.disabledBy`, one of `platform`, `tenant`, `pool`, `runtime`, `tenantDefault`, `platformDefault`, and `session`, and `details.effectiveSettings` with the values each level held, as §8.2's cycle refusal reports `blockedBy` and `effectiveSettings`. A `pool` value names the pool, and a `runtime` value names the runtime and states whether the `false` came from its base.

The check runs at each of these points, as TS-3 places its own:

- At the top of the `lenny/delegate_task` handler, after TS-3 fixes the parent as the calling session, and before the target resolution, the `PreDelegation` phase, the export, and any pod claim. This covers a call from the pod and a call from a client at the gateway `/mcp` endpoint that names its own session as parent (DAR-1).
- In `validateDelegation` in the delegation service, before the parent lookup's later checks, for any caller of `Service.Delegate` that bypasses the handler.

`lenny/discover_agents` called by a session that may not delegate answers with no targets, as TS-3 states for `maxDepth: 0`, because discovery is a read and an empty answer reveals nothing. `lenny/await_children` and `lenny/cancel_child` keep their behaviour: they act on existing children and are refused for a session with none, as they are today.

The gateway counts refusals in `lenny_delegation_disabled_refusals_total{disabled_by}`. A runtime that calls a tool its list no longer offers appears there, which tells an operator that the runtime ignores list changes. Each refusal also writes the delegation rejection audit that §8.2 states for a refused delegation, with `DELEGATION_DISABLED` and `disabledBy`.

## DA-4 Tool list

`Bridge.ListPlatformTools` uses the session ID it already receives. After TS-3's allow-list to §9.1's table, it removes tools by this table:

| Tool | Listed when |
| :-- | :-- |
| `lenny/delegate_task` | The session may delegate. |
| `lenny/discover_agents` | The session may delegate. |
| `lenny/await_children` and `lenny/cancel_child` | The session may delegate, or it has at least one child in any state. |
| Every other tool on the allow-list | Always, as "DA-7" states. |

The list changes only when "may delegate" changes. "Has a child" becomes true only through a delegation, which requires "may delegate", and stays true, so a session that loses delegation keeps the tools that manage the children it has (DAR-6).

`Bridge.CallPlatformTool` does not check the list. A call to a tool the session's list omits reaches the handler, which answers `DELEGATION_DISABLED` for `lenny/delegate_task` and the existing answers for the others. A call to a name outside TS-3's allow-list answers `ErrPlatformToolNotFound`. Today `callTool` in `pkg/adapter/mcp/server.go` reports any forwarded failure as JSON-RPC `-32603` **[source]** (F-15.4.8), and this design has the adapter report the not-found status as `-32602`, the MCP error for an unknown tool **[external]**.

The gateway `/mcp` endpoint keeps one catalog for every client, as TS-3 states, because a connection names no session until a call does. The refusal covers it.

**`type: mcp` runtimes and runtimes that use MCP.** A `type: mcp` runtime is an MCP server the gateway calls, and it has no platform MCP server and no delegation, so nothing in this design reaches it beyond the refused write in DA-1. An agent runtime whose binary is an MCP client connected to several servers, such as Claude Code with the platform server, connector servers, and servers of its own, sees the platform server's list change as it would any server's, and a client that prefixes tool names by server, as Claude Code did in the CA-9 spike, keeps the prefix on the platform tools. The virtual child interface of §8.2 step 8 exists only for children, so a session that cannot delegate gets none, and a session that loses delegation keeps the interfaces of its existing children.

## DA-5 Running sessions

The platform MCP server declares `"tools": {"listChanged": true}` on every connection, whatever the runtime declares. A client that ignores the notification is unaffected by receiving it, and a client that honours it lists again. This differs from CA-8's connector servers, which tie the declaration to `deferredToolDiscovery` because a failing connector list breaks some clients, a risk a platform list that changes without failing does not carry.

The adapter sends `notifications/tools/list_changed` on each open platform connection in these cases **[design]**:

1. **A setting changed for the session.** The gateway sends the adapter the notification RPC that CA-8 adds, generalized to carry the server: the platform server or one connector ID. The adapter checks that the named session is the one its runtime process holds, and sends the notification.
2. **A new session binds to a process that has listed.** When the adapter binds a session to a runtime process that already holds an open platform connection on which it answered `tools/list`, it sends the notification after the bind, because the new session's list can differ from the previous session's.

**Fan-out.** A tenant, runtime, or pool write that changes a value the resolution reads makes the gateway enumerate the non-terminal sessions with a bound pod whose tenant, runtime, or pool matches, recompute "may delegate" for each, and send the RPC for each session whose value changed, through the session's coordinating replica over `CH-HOLDERFORWARD`, as CA-8 does for a link callback. The fan-out is best effort and rate limited by a gateway flag, `--delegation-list-change-rate`, default 200 notifications per second per replica, which is a judgment. A failure is logged and counted in `lenny_platform_tools_list_changed_total{outcome}`, and the refusal stays the control. A change to a platform Helm value takes effect when the gateway restarts with it and sends no fan-out, so a running process sees it at its next call or its next bind.

**MCP versions.** Under MP-5 the intra-pod servers serve `2025-11-25` and `2026-07-28`. On `2025-11-25` the notification goes on the connection. On `2026-07-28` it goes on a `subscriptions/listen` stream that the client opened with `toolsListChanged: true`, so the intra-pod server implements `subscriptions/listen`, which MP-5 does not list today. The platform server's `tools/list` result carries `ttlMs: 0` and `cacheScope: "private"`, because the list depends on the calling session. The list varies by the session the adapter resolves for each request and changes over time as settings and sessions change, which `2026-07-28` permits, and it does not vary by connection **[external]**.

## DA-6 Per-hop narrowing

A parent that passes `approvalMode: deny` on `lenny/delegate_task` creates the child with no room: the child's stored `maxDepth` is set to the child's own depth, and the child's lease records `approvalMode: deny` (O6). The child cannot delegate, its list omits the delegation tools, and its refusal names `disabledBy: session`. A grandchild cannot exist, so no inheritance rule is needed below it.

The parent's own call proceeds through every other gate. `ErrDelegationDenied` and `DELEGATION_DENIED` go, because no caller refuses itself under this meaning. A lease stored with `approvalMode: deny` can never be widened, because a child stores its parent's `maxDepth` under TS-3 and `maxDepth` is not extendable under §8.6.

## DA-7 Other platform tools

The owner asked whether other platform tools need a per-runtime or per-pool switch. This table reviews each tool on the allow-list TS-3 sets, which is §9.1's table plus `lenny/vcs_token`:

| Tool | What it does | Existing control | Switch warranted |
| :-- | :-- | :-- | :-- |
| `lenny/output` | Emits output parts to the parent or client. | None needed: it is the result path of a session. | No |
| `lenny/request_input` | Blocks until the client or parent answers. | `capabilities.interaction` (`one_shot` allows one call, `multi_turn` many), and `maxRequestInputWaitSeconds` (§5.1). | No |
| `lenny/request_elicitation` | Asks the human through the elicitation chain. | The per-pool allow list, deep elicitation suppression at delegation depth 3 and deeper, and `maxElicitationWaitSeconds` (§9.2). | No |
| `lenny/send_message` | Sends a message to a task in the tree. | `messagingScope` from deployment, tenant, and runtime, and `messagingRateLimit` on the lease (§7.2, §8.3). | No |
| `lenny/get_task_tree` | Returns the visible part of the task tree. | `treeVisibility` on the lease (§8.3). | No |
| `lenny/set_tracing_context` | Registers tracing identifiers that the gateway attaches to children's leases. | Validation rules of §8.3. Its only effect is on children, so a session that cannot delegate gets none from it, and the same function is open through the `set_tracing_context` JSONL message, so hiding the tool removes nothing. | No |
| `lenny/memory_write` and `lenny/memory_query` | Built-in memory. | CA-10 removes the tools and the feature. | No, they leave |
| `lenny/vcs_token` | The pod's git credential helper fetches a token. | The VCS pool's host patterns and user authorization. [`egress-and-git-design.md`](egress-and-git-design.md#proposal-split) EG-3 removes the tool. | No, it leaves |
| `lenny/delegate_task`, `lenny/discover_agents`, `lenny/await_children`, and `lenny/cancel_child` | Delegation and its dependent tools. | This design. | Covered by DA-1 to DA-4 |

A deployer who needs a human to approve a particular tool call has SO-5's approval rules, which match a tool by name and hold the call at the gateway. A general per-runtime tool allow-list was considered and rejected under "Alternatives rejected".

## Limits

- **A runtime that ignores the notification keeps a stale list.** It is offered `lenny/delegate_task` after delegation became unavailable, and its call is refused, or it is not offered the tool after delegation became available until it lists again. The refusal counter shows the first case.
- **Removal after a notification is unmeasured.** The CA-9 spike measured a tool added through the notification and no tool removed. "Validation owed" lists it.
- **The model is not told of the change.** In the CA-9 spike, nothing in the stream showed the model that its tools changed. A model that planned to delegate learns of the change from the refusal text.
- **Platform changes reach running processes late.** A Helm change sends no fan-out, so a process sees it at its next call or bind.
- **Fan-out cost.** A tenant-wide change sends one RPC per running session in the tenant whose value changed, bounded by the rate flag, so a large tenant's sessions learn of it over seconds to minutes.
- **The gateway `/mcp` catalog still lists the tool.** A client at the gateway sees `lenny/delegate_task` and meets the refusal.
- **A disabled level does not stop a running child.** A child created before the change runs to completion, and its own next call reads the new value.
- **`approvalMode: deny` changes meaning.** A runtime written against the current code, which refuses its own call when it passes `deny`, now creates a child that cannot delegate.

## Alternatives rejected

- **Express "disabled" as a `DelegationPolicy` that allows nothing.** The policy decides targets, it has no default at the tenant or the platform, the gateway does not apply its rules to `lenny/delegate_task` today, and "allows nothing" can only be known by evaluating every runtime's labels, so the tool list could not follow it.
- **Use `maxDepth: 0` in the Runtime's default lease or in a preset.** §8.2's item 3 and item 2 would carry it, and TS-3's minimum would make it restrict-only. Neither layer exists in code, a minimum gives no default that a lower level can override, and it ties availability to the depth budget, so a refusal could not name the level.
- **Make the levels restrict-only (O1 option A).** A platform or tenant default of `false` could never be overridden, so it would be a forbid under another name.
- **Snapshot availability at creation (O4 option B).** A runtime turned off during an incident would keep delegating for the life of its sessions.
- **Keep a uniform tool list and rely on the refusal.** The owner asked that the tool not be offered, and an agent offered a tool it cannot use wastes turns on it.
- **Omit the platform server from the manifest when delegation is unavailable.** The other platform tools would go with it.
- **A per-runtime allow-list of platform tools.** Every tool already has a control of its own (DA-7), a list of names would duplicate those controls without their semantics, and an omitted core tool such as `lenny/output` would break a session.
- **An environment level.** Environments already scope targets through `defaultDelegationPolicy`, and another level would add a resolution step with no use case the owner named.

## Specification changes

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), the Runtime YAML and field notes, and "Normative Merge Algorithm" | Add `allowDelegation` with its values, its refusal on `type: mcp`, and a merge row: restrict-only, a derived `true` refused over a base `false`, and the merged value of "DA-1". |
| [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) | Add the pool's `allowDelegation: false`. |
| [§8.1 Design Philosophy](../spec/08_recursive-delegation.md#81-design-philosophy) | State that whether a session may delegate is decided by the binary and by the configuration of "Delegation availability" in §8.3. |
| [§8.2 Delegation Mechanism](../spec/08_recursive-delegation.md#82-delegation-mechanism), **Delegation flow** | Add the availability and room check as the first step after the parent is fixed, with `DELEGATION_DISABLED` and `details.disabledBy`. |
| [§8.3 Delegation Policy and Lease](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) | Add a paragraph **Delegation availability** with the levels, the resolution table of "DA-2", the live reads, and the session level. Add `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` to **Helm settings governing cycle detection and delegation depth**. |
| [§8.4 Approval Modes](../spec/08_recursive-delegation.md#84-approval-modes) | Restate `deny` as "DA-6": the child is created with no room and records the value. |
| [§8.5 Delegation Tools](../spec/08_recursive-delegation.md#85-delegation-tools) | Replace "Available on the platform MCP server for every delegation-capable pod" with the per-session listing of "DA-4". |
| [§9.1 Where MCP Is Used](../spec/09_mcp-integration.md#91-where-mcp-is-used), **Platform MCP Server Tools** | State that the list is per session, with the rule of "DA-4", and that a call is answered by the tool's own refusal. |
| [§14 Workspace Plan Schema](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) and the derive request in [§15.1](../spec/15_external-api-surface.md#151-rest-api) | Add `delegationLease.maxDepth` to the derive request, narrowing only. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | Add `GET` and `PUT /v1/admin/tenants/{id}/delegation`, `allowDelegation` on the runtime and pool payloads, `details.disabledBy` and `details.effectiveSettings` on `DELEGATION_DISABLED`, and the removal of `DELEGATION_DENIED`. |
| [§15.4.3 Runtime Integration Levels](../spec/15_external-api-surface.md#1543-runtime-integration-levels) | State that the platform server declares `tools.listChanged`, the cases in which it notifies, and the cache hints on its list. |
| [§16.1 Metrics](../spec/16_observability.md#161-metrics) and [§16.7 Section 25 Audit Events](../spec/16_observability.md#167-section-25-audit-events) | Add `lenny_delegation_disabled_refusals_total{disabled_by}`, `lenny_platform_tools_list_changed_total{outcome}`, and `tenant.delegation_updated`. |
| [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod), the `CH-MCP-PLATFORM` card | **Messages**: the per-session tool set and the notification. **Preconditions**: the list depends on the calling session. Name the RPC CA-8 adds, generalized to the platform server, under the naming law of §28.1 at proposal time. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/apis/lenny/v1alpha1`, `pkg/controller/runtime`, and `pkg/gateway/runtime/runtimestore` | `AllowDelegation *bool` on the Runtime resource, its mirror, the registry row, and `Merge`. Regenerate the CRD manifests. |
| `pkg/gateway/externalapi/admin` | The field on runtime and pool payloads with the derived and `type: mcp` refusals, the tenant `delegation` sub-resource with `forbidSetBy`, and its audit event. |
| `migrations` | Columns for the runtime field, the pool field, and the tenant block. |
| `charts/lenny` | `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` in `values.yaml` and the schema, rendered to gateway flags. |
| `pkg/delegation` | A resolver that takes the tenant, runtime, and pool rows and the platform flags and returns availability and `disabledBy`, shared by the handler, the service, and the bridge. |
| `pkg/gateway/mcpfabric/mcptools` and `pkg/gateway/mcpfabric/delegation` | The two checks of "DA-3", the details, the counter, and the new meaning of `approvalMode: deny`. |
| `pkg/gateway/gatewaycontrol/platformtools` | `ListPlatformTools` filters by the calling session under "DA-4". |
| `pkg/adapter/mcp` and `pkg/adapter` | `listChanged: true`, notifications on open connections serialized against responses, the notification after a bind, the RPC handler, the cache hints, and `-32602` for a forwarded not-found status. The connection writer is shared with CA-8. |
| `schemas/lenny-adapter.proto` | The RPC CA-8 adds, with a field naming the server. |
| `pkg/gateway` admin write paths | The fan-out of "DA-5" with its rate flag and counter. |
| `docs/` | An operator page on delegation availability and its levels, and the runtime author guide's statement that the platform tool list can change during a session. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | The resolver over every combination of the levels, with `disabledBy` for each (DAR-2, DAR-3). The derived merge and its refusal. The list rule of "DA-4" for may-delegate and has-child. `approvalMode: deny` stores the child's depth as its `maxDepth`. |
| 2 | The registry, pool, and tenant stores round-trip the fields, and the RuntimeReconciler mirrors `allowDelegation`. |
| 3 | The `DELEGATION_DISABLED` envelope with `disabledBy` and `effectiveSettings`. The platform server's `initialize` declares `listChanged`, a `2026-07-28` `tools/list` carries `ttlMs: 0` and `cacheScope: "private"`, and a call to a name outside the allow-list answers `-32602`. |
| 4 | On the compose stack: a session on a runtime with `allowDelegation: false` lists no delegation tools and is refused from the pod and from the gateway `/mcp` (DAR-1, DAR-4). A tenant default of `false` with a runtime `true` delegates. A tenant forbid set by a `platform-admin` cannot be cleared by a `tenant-admin`. A runtime set to `false` mid-session refuses the next call, leaves the running child alive, and keeps `lenny/await_children` listed (DAR-5, DAR-6). A child moved by a `PreRoute` interceptor to a pool with `false` lists no delegation tools. A derive with `delegationLease.maxDepth: 0` cannot delegate. A deployment with no values set behaves as today (DAR-7). |
| 5 | On Kind with the production adapter: a runtime-level change sends `notifications/tools/list_changed` on the session's open connection through the coordinating replica, and a new session bound to a process that has listed receives the notification. |
| 7a | Concurrent `lenny/delegate_task` calls racing a runtime write each see one value per call, under `-race`. |
| 9 | A runtime calling `lenny/delegate_task` after it left its list is refused. A runtime cannot reach `lenny/create_session` from its pod (DAR-8, with TS-3). A child of a `deny` hop cannot delegate through any path. |
| 11 | The operator page's examples match the resolution table, and the error catalog entry matches the envelope. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Whether Claude Code and Codex drop a tool that a `notifications/tools/list_changed` removed, and whether the model stops calling it | The CA-9 spike measured only an added tool, and "DA-5" relies on removal for the convenience half of the design. | Open |
| How long a runtime process outlives a session on a pod, and whether common runtimes cache `tools/list` across sessions | It sizes the stale-list window that the bind notification closes. | Open |
| That a per-session `tools/list` behind one pod-wide socket conforms to `2026-07-28`'s "MUST NOT vary per-connection" | "DA-5" argues that the list varies by the calling session and over time. The MP-5 proposal confirms it against the schema and the conformance suite. | Open |
| The number of running sessions per tenant at the largest expected deployment | It sizes the fan-out and the default rate. | Open |
| Whether the tenant record is changed through `PUT /v1/admin/tenants/{id}` alone, or a sub-resource fits, as `elicitation-content-integrity` does | "DA-1" proposes a sub-resource. | Open, for the proposal |

## Defects found while verifying

Revision 2 files these in `BUILD-GAPS.md`, at the owner's request of 2026-11-09, under the ID in the "BUILD-GAPS" column:

| Defect | Evidence | BUILD-GAPS |
| :-- | :-- | :-- |
| `lenny/delegate_task` does not evaluate the calling session's `DelegationPolicy` rules against the target, so a policy's allow list does not limit delegation, against §8.3 "Dynamic tag evaluation at delegation time". The audit note 8.3-H1 records it with no checkbox or F-id. | "Current state", `DelegationPolicy` paragraph. | F-8.3.14 |
| `approvalMode: deny` refuses the caller's own call and is never stored on a lease, against §8.4 and the lease example of §8.3. | "Current state", `approvalMode` paragraph. O6 proposes the fix. | F-8.4.4 |
| A runtime can call `lenny/create_session` and the other client-facing tools from its pod, because the platform MCP server lists and forwards the whole `/mcp` catalog. | "Current state", "How the tool list is built". TS-3 closes it. | F-9.1.13, filed before this design. |
| The adapter reports a forwarded unknown tool as JSON-RPC `-32603`, where MCP specifies `-32602`. | "DA-4 Tool list". | F-15.4.8 |
| §8.5 uses "delegation-capable pod", which the specification does not define. | §8.5 opening sentence. | Not filed. The owner's request of 2026-11-09 did not name it. |

## Instructions for reviewers of other designs

- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26): its reviewer states that TS-3's refusal reads the availability of this design together with the stored lease, adds `details.disabledBy` to `DELEGATION_DISABLED`, extends the "Hiding the tool" table to `lenny/discover_agents` and to `lenny/await_children` and `lenny/cancel_child` for a session with no child, adds `delegationLease.maxDepth` narrowing to the derive request, and records the new meaning of `approvalMode: deny`.
- [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions): its reviewer adds `allowDelegation` to the Live row of "Field classes", and notes that delegation settings, like `delegationPolicyRef`, are read at each call of a running session.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#fields): its reviewer adds an `allowDelegation` row to "Fields" (restrict-only, `true` only over a base that is not `false`), adds the pool field to "Template copy" with the tenant's right to set `false`, and adds the tenant `delegation` block and the runtime and pool fields to "Configuration by scope".
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-8-runtime-capability-and-late-tools): its reviewer generalizes `NotifyConnectorToolsChanged` to carry the server, the platform server or a connector, and states that the platform server declares `listChanged` without reference to `deferredToolDiscovery`.
- [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md#mp-5-intra-pod-servers): its reviewer adds `subscriptions/listen` with `toolsListChanged` to MP-5's intra-pod servers, and the cache hints of "DA-5" to the platform server's `tools/list`.
- [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture): its reviewer considers a `delegation: forbid` guardrail in the tenant security record, which reads as a tenant forbid at the gate, and states that an SO-5 approval hold on `lenny/delegate_task` runs after this design's refusal.
- [`runtime-routing-design.md`](runtime-routing-design.md#post-chain-point): no change. Its reviewer confirms that the post-chain point needs no availability check, because a child's availability reads the final row at the child's own calls.

## Proposal split and order

1. **DA-A**, after TS-3's proposal, whose calling-session parent, allow-list, `DELEGATION_DISABLED`, and stored `maxDepth` it extends: the specification edits; the levels, the resolver, and the refusal; the per-session list; the derived runtime and owned pool rules; the derive narrowing; and the meaning of `approvalMode: deny`.
2. **DA-B**, after CA-8's adapter notification work and MP-5: `listChanged`, the bind notification, the generalized RPC, the fan-out, and the cache hints.

The defect that `lenny/delegate_task` does not apply `DelegationPolicy` rules (F-8.3.14) is independent of both and takes its own proposal.

## Sources

- MCP `2025-11-25` tools, including the `listChanged` capability, the list changed notification, and unknown-tool errors: https://modelcontextprotocol.io/specification/2025-11-25/server/tools
- MCP `2026-07-28` tools, including the rule on per-connection variance and delivery through `subscriptions/listen`: https://modelcontextprotocol.io/specification/2026-07-28/server/tools
- MCP `2026-07-28` caching, `ttlMs` and `cacheScope`: https://modelcontextprotocol.io/specification/2026-07-28/server/utilities/caching

## Review history

### Revision notes

- Revision 1, 2026-11-09: first draft, written from the owner's request for delegation configurable per runtime or pool with platform and tenant defaults, and from the owner's later thought that sessions should be able to disable delegation, which O5 answers by reusing decision 26 of [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26).
- Revision 2, 2026-11-09: files the defects found while writing this design in `BUILD-GAPS.md`, at the owner's request of 2026-11-09, as F-8.3.14, F-8.4.4, and F-15.4.8. The pod's reach to `lenny/create_session` was already filed as F-9.1.13, so it gains that ID and no new finding.
