# Delegation availability

Status: design for owner review, revision 3, 2026-11-09, after one adversarial review. Nothing here is built or staged. It reads against [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`runtime-routing-design.md`](runtime-routing-design.md) revision 11, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 8, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 64, [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12, [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 2, [`security-oversight-design.md`](security-oversight-design.md) revision 47, and [`session-derive-design.md`](session-derive-design.md) revision 16. "Decisions" lists what is settled and what remains open for the owner. It stages no specification edit. The changes go through `change-proposal` after review. Proposals carry the label DA-n, and requirements carry the label DAR-n. "Review history" at the end records the revisions.

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

The owner's request names pools. The owner decided on 2026-11-09 that the pool level is dropped, because a derived runtime can turn delegation off for the sessions that need it (D2).

## Scope

**Added:**

- A delegation availability setting at the platform, tenant, and runtime levels, with a platform default, a tenant default, a narrow-only tenant entry for one runtime, and a resolution rule that combines them with the session level ("DA-1" and "DA-2").
- A tenant hard switch as the guardrail `"delegation": "forbid"` in the tenant security record of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), written by a `security-admin` (D5).
- A call-time refusal that names the level that disabled delegation ("DA-3").
- A per-session platform tool list that omits `lenny/delegate_task` and the tools that depend on it when the session cannot delegate ("DA-4").
- A notification to a running runtime when its platform tool list changes, and a per-session list cache in the adapter ("DA-5").
- A per-hop rule for a parent that wants its child unable to delegate: `lease_slice.maxChildrenTotal: 0` (D4, "DA-6").
- Audit events that distinguish a change which widens availability from one which narrows it ("DA-1").

**Changed:**

- §8.5's statement that the delegation tools are "available on the platform MCP server for every delegation-capable pod" becomes the per-session list of "DA-4".
- An explicit `maxChildrenTotal: 0` in a lease slice means that the child starts no child, and lease extension never raises it ("DA-6").
- The platform MCP server declares `tools.listChanged`, in the `initialize` result under MCP `2025-11-25` and in the `server/discover` result under MCP `2026-07-28`, and sends `notifications/tools/list_changed`.

**Removed:**

- `approvalMode` as an argument of `lenny/delegate_task`, with `ErrDelegationDenied`, the `DELEGATION_DENIED` code, and `ERROR_CODE_DELEGATION_DENIED` in the adapter proto, whose number is reserved (D4, "DA-6"). The lease field of §8.4 stays.

**Out of scope:**

- A pool level. The owner dropped it (D2).
- Per-tool switches for the other platform tools. "DA-7" reviews each tool and finds an existing control for each, and O7 records the recommendation.
- Which targets a session may reach. `DelegationPolicy` decides that, and the defect that `lenny/delegate_task` does not evaluate the policy's rules is recorded under "Defects found while verifying" for its own proposal. `BUILD-GAPS.md` files it as F-8.3.14.
- An environment level. An environment's `defaultDelegationPolicy` already restricts targets for the sessions created in it ([§10.6](../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)).
- Publishing a runtime's delegation availability in discovery (`GET /v1/runtimes`).
- Root-session creation through the gateway's public API from inside a pod. "Limits" states it.

## Terms

| Term | Meaning |
| :-- | :-- |
| Delegation availability | Whether a session's configuration allows it to start delegated children. The platform, the tenant, the tenant security record, a tenant runtime entry, and the runtime decide it ("DA-1"). |
| `allowDelegation` | The availability field on a Runtime and on a tenant runtime entry. On a Runtime it is `true`, `false`, or unset. On a tenant runtime entry it is `false` or unset. |
| Tenant runtime entry | A tenant's per-runtime record of §5.1 "Capabilities are customizable per tenant", stored at `/v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}` **[source]**. This design adds `allowDelegation: false` to it, which narrows one runtime for one tenant (D1). |
| Default | A level's value that applies when the runtime says nothing: `allowByDefault` at the platform and the tenant. A more specific level can override a default in either direction. |
| Forbid | A switch that makes delegation unavailable whatever the more specific levels say: `gateway.delegation.forbid` at the platform, and the delegation guardrail at the tenant. |
| Delegation guardrail | The entry `"delegation": "forbid"` in the tenant security record of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), written by a `security-admin`, or by a `platform-admin` with a justification, under SO-2's tightening and weakening rules (D5). |
| Room | Whether the session's stored delegation lease allows one more child: the stored `maxDepth` is greater than the session's own depth, and the stored `maxChildrenTotal` is not an explicit 0. A root under `maxDepth: 0` has no room, and neither has a child whose parent passed `lease_slice.maxChildrenTotal: 0`. TS-3 defines the stored `maxDepth`. |
| May delegate | Delegation availability and room together. A session that may delegate is offered `lenny/delegate_task`, and its calls pass this gate. |
| Delegation-dependent tools | `lenny/discover_agents`, whose only use is to find delegation targets, and `lenny/await_children` and `lenny/cancel_child`, whose only use is to manage children. |
| Platform tool list | The answer to `tools/list` on the platform MCP server inside the pod, for the session the adapter resolves as the caller. |
| `disabledBy` | The field of the `DELEGATION_DISABLED` refusal that names the level that made delegation unavailable ("DA-3"). |
| Widening and narrowing | A write that makes delegation available where it was unavailable widens availability, and a write that does the reverse narrows it. Each writes its own audit event ("DA-1"). |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Delegation | A session starts a child session through the platform tool `lenny/delegate_task` ([§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism)). Children form a delegation tree under a root session. |
| Delegation lease | The record of what a session may do as a delegator: depth, budgets, and policy references ([§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). A child's lease is narrower than its parent's. |
| `maxDepth` | The lease's depth limit, counted from the root. Today §8.2 requires a positive integer, and TS-3 lets a client set 0. |
| `lease_slice` | The optional argument of `lenny/delegate_task` that §8.2 defines as `LeaseSlice`, the budget a parent grants its child: `maxTokenBudget`, `maxChildrenTotal`, `maxTreeSize`, `maxParallelChildren`, and `perChildMaxAge`. The tool's input schema in the tree spells it `leaseSlice` **[source]**. |
| `DelegationPolicy` and `delegationPolicyRef` | A named resource of tag rules that states which runtimes, pools, and connectors a session may target. A Runtime names one in `delegationPolicyRef` ([§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| `allowSelfRecursion` | The opt-in, at the platform, the Runtime, and the `DelegationPolicy`, that lets a runtime and pool appear twice in one delegation lineage ([§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism) cycle detection). |
| `approvalMode` | The lease field of [§8.4](../spec/08_recursive-delegation.md#84-approval-modes) with the values `policy`, `approval`, and `deny`. `approval` is reserved and behaves as `policy` in v1. The code also accepts it as an argument of `lenny/delegate_task`, which this design removes (D4). |
| Platform MCP server | The MCP server the runtime adapter runs inside an agent pod on the abstract Unix socket `@lenny-platform-mcp`. It forwards each `tools/list` and `tools/call` to the gateway under the calling session (`CH-MCP-PLATFORM` in [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). A Basic-level runtime does not connect to it ([§15.4.3](../spec/15_external-api-surface.md#1543-runtime-integration-levels)). |
| Gateway `/mcp` | The client-facing MCP endpoint of the gateway ([§15.2](../spec/15_external-api-surface.md#152-mcp-api)). |
| `type: agent` and `type: mcp` runtime | An agent runtime runs sessions and reaches the platform tools. A `type: mcp` runtime is itself an MCP server that the gateway exposes at `/mcp/runtimes/{name}`, has no platform MCP server, and is refused as a delegation target with `target_not_an_agent` ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime), [§9.1](../spec/09_mcp-integration.md#91-where-mcp-is-used)). |
| Pool | A set of pre-warmed pods for one runtime at one isolation profile and resource class ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). |
| Derived runtime and tenant-derived runtime | A derived runtime names a base in `baseRuntime` and changes some fields under §5.1's merge rules. A tenant-derived runtime is one a tenant creates, with an owned pool copied from a platform template pool ([`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-5-owned-pools)). |
| `PreRoute` chain and post-chain point | The interceptors that settle a session's runtime and pool on every creation path, including a delegated child (`kind: delegate`), and the step after them where every check that depends on the runtime, revision, or pool runs ([`runtime-routing-design.md`](runtime-routing-design.md#post-chain-point)). |
| Live and versioned fields | Under [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions), a versioned Runtime field is copied into an immutable revision, and a live field, which the gateway enforces or publishes, applies to the next session on every revision. `delegationPolicyRef` and `allowSelfRecursion` are live. |
| `capabilities.deferredToolDiscovery` | The Runtime capability that [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-8-runtime-capability-and-late-tools) adds, which states that the runtime keeps a connector server whose `tools/list` failed and lists again on `notifications/tools/list_changed`. |
| `notifications/tools/list_changed` | The MCP notification a server that declared `tools.listChanged` sends when its tool list changes, after which the client lists again **[external]**. Under MCP `2026-07-28` the client receives it on a `subscriptions/listen` stream it opened with `toolsListChanged: true` **[external]**. |
| `server/discover` | The MCP `2026-07-28` request that every server implements, whose result carries the server's supported versions and its `capabilities`. Under `2025-11-25` the `initialize` result carried the capabilities **[external]**. |
| Coordinating replica and `CH-HOLDERFORWARD` | The gateway replica that holds a session's pod connection, and the channel through which another replica forwards a request for that session to it ([§28.5.4](../spec/28_communication-channels.md#2854-inter-replica)). |
| `platform-admin`, `tenant-admin`, and `security-admin` | Lenny's administrative roles for the platform, for one tenant, and for one tenant's security record. |
| Weakening delay | SO-2's rule that a change which removes a guardrail creates a pending version that takes effect after the tenant's `weakeningDelaySeconds`, with the events `security.config_weakening_requested` and `security.config_weakened`, while a tightening applies in the write's transaction with `security.config_tightened` ([SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), "Tightening and weakening"). |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static checks, 1 unit, 2 component, 3 contract, 4 integration on the compose stack, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| TS-3 and decision 26 | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#ts-3-session-environment-context-and-cross-environment-sharing) | TS-3 reworks the delegation gate: the calling session is the only parent a runtime can name, the intra-pod tool surface is allow-listed to §9.1's table, and a client's `delegationLease.maxDepth: 0` disables delegation for a session with `403 DELEGATION_DISABLED` ("Client delegation limits (decision 26)"). |
| RT-2 | [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) | The `PreRoute` contract and the post-chain point. |
| RV-1 | [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions) | The field classes: versioned fields, live fields, and fixed fields. Its "Per-tenant capability override" makes the tenant runtime entry a narrowing. |
| DR-4, DR-5, and DR-6 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#fields) | What a tenant may set on a derived runtime ("Fields" under DR-4), owned pools and their template copy, and how a base change reaches a derived runtime. |
| CA-8 and CA-10 | [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-8-runtime-capability-and-late-tools) | CA-8 adds `deferredToolDiscovery`, `listChanged` on connector servers, and a gateway-to-adapter notification RPC. [CA-10](connector-authorization-and-external-memory-design.md#ca-10-removing-built-in-memory) removes built-in memory and its tools. |
| MP-5 | [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md#mp-5-intra-pod-servers) | The intra-pod MCP servers move to MCP `2026-07-28` and `2025-11-25`. |
| SO-2 and SO-5 | [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture) | SO-2 is the tenant security record of guardrails that a `security-admin` writes, with its weakening delay. [SO-5](security-oversight-design.md#so-5-delegated-approvers) holds a tool call that matches a rule until a `security-approver` approves it. |

## High-level design

Delegation stays a platform primitive, and an operator can now turn it off where it does not belong. The platform sets a default and can forbid delegation outright. A tenant can replace the platform default for its own sessions and can turn delegation off for one runtime in the tenant. A tenant's security team can forbid delegation for the whole tenant through its security record, and lifting that forbid waits out the record's weakening delay. A runtime can state that its sessions delegate or do not, which overrides both defaults, and a derived runtime can turn delegation off for the sessions it serves, which covers workers that run beside an orchestrator of the same base runtime. A client creating a session can turn it off for that session and its tree, as decision 26 already settled, and a parent can turn it off for one child. The forbids, a tenant runtime entry, a base runtime's `false`, and the session's and parent's choices can only take delegation away.

The gateway decides, on every `lenny/delegate_task` call, whether the calling session may delegate, from the session's row and the live settings, and refuses with `403 DELEGATION_DISABLED` when it may not. The refusal names the level responsible. The platform MCP server inside the pod lists only the tools the session can use, so a session that may not delegate is offered no `lenny/delegate_task` and no tool that only serves delegation. When a setting changes while a session runs, the adapter tells the runtime that its list changed. A runtime that ignores the notice, or cached its list, still meets the refusal.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Levels (DA-1).** `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` in Helm, `delegation.allowByDefault` on the tenant, the delegation guardrail in the tenant security record, `allowDelegation: false` on a tenant runtime entry, and `allowDelegation` on the Runtime with a restrict-only rule for derived runtimes.

**Resolution (DA-2).** A forbid wins, then a tenant runtime entry, then the runtime's value, then the tenant default, then the platform default. The session's room applies on top. The settings are live: the gateway reads them at each call.

**Refusal (DA-3).** `403 DELEGATION_DISABLED`, which TS-3 introduces, gains `details.disabledBy`. The gateway checks at the top of the `lenny/delegate_task` handler and again in the delegation service.

**Tool list (DA-4).** The gateway answers `ListPlatformTools` for the calling session. It omits `lenny/delegate_task` and `lenny/discover_agents` when the session may not delegate, and omits `lenny/await_children` and `lenny/cancel_child` as well when the session has no child.

**Running sessions (DA-5).** The platform MCP server declares `tools.listChanged` and sends `notifications/tools/list_changed` when the gateway reports a change for the session, and when a new session binds to a runtime process that has listed before. The adapter keeps one list per session and clears it on those events.

**Per-hop narrowing (DA-6).** A parent that passes `lease_slice.maxChildrenTotal: 0` on `lenny/delegate_task` creates a child with no room, so the child cannot delegate.

**Other tools (DA-7).** Each other platform tool already has a control of its own, so no per-tool switch is added (O7).

```
 lenny/delegate_task from session S (pod or gateway /mcp)
        │
        ▼
 S is the calling session (TS-3)
        │
        ▼
 Availability of S, read live from S's tenant, security record, and runtime:
   platform forbid ─ guardrail ─ tenant runtime entry ─ runtime value ─ tenant default ─ platform default
        │ unavailable ──> 403 DELEGATION_DISABLED, details.disabledBy
        ▼
 Room on S's stored lease (maxDepth under TS-3, explicit maxChildrenTotal 0)
        │ no room ──> 403 DELEGATION_DISABLED (disabledBy: session) or DELEGATION_DEPTH_EXCEEDED
        ▼
 Existing gates: target scope, PreDelegation, PreRoute chain (kind: delegate), post-chain point, cycle, depth, budget
```

"Limits" states what the change costs: a runtime that ignores the notification keeps a stale list, a list change costs the runtime a provider prompt-cache write, a platform Helm change reaches running processes only after a gateway restart, the fan-out of a tenant-wide change touches every running session in the tenant, and a pod that can reach the gateway's public API with a client credential can still create a root session.

## Decisions

Settled by an earlier owner decision or by a project rule:

| ID | Decision |
| :-- | :-- |
| S1 | A client disables delegation for one session and its tree with `delegationLease.maxDepth: 0` at creation. The owner settled it on 2026-11-03 as decision 26 of [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26), in the words "yes, add it with maxDepth: 0". O5 records what decision 26 and D3 leave open. |
| S2 | The refusal is the control and the hidden tool is a convenience. TS-3 states this in its words "Hiding is a convenience, and the refusal is the control", and this design applies it to every level. |
| S3 | `allowDelegation` on the Runtime is a live field under [RV-1](runtime-revisions-design.md#rv-1-revisions). The ground is who writes it. A `platform-admin` writes it on a platform runtime and a `tenant-admin` on a tenant-derived runtime, as the authority over where and for whom the runtime runs, and the gateway enforces it, which is RV-1's definition of a live field. The image the runtime author builds does not carry the value, so a change to it describes no change to the binary and needs no revision. A binary that relies on delegation keeps delegating only while its admin leaves delegation enabled, and meets the refusal of "DA-3" otherwise. |
| S4 | No compatibility mode. Nothing is deployed, so the new fields carry no migration path, and the meaning of an explicit `maxChildrenTotal: 0` changes in place (DA-6). |

Settled by the owner on 2026-11-09:

| ID | Decision |
| :-- | :-- |
| D1 | A tenant can narrow one platform runtime for its own sessions through a narrow-only per-tenant entry, the tenant runtime entry with `allowDelegation: false` ("DA-1"). |
| D2 | The pool level is dropped. A derived runtime can set `allowDelegation: false`, which covers a pool of workers beside a pool of orchestrators of the same base runtime. In the owner's words: "I agree with you. As long as it can be disabled in derived runtimes, we're good." Revision 2 recorded this as O2. |
| D3 | A session cannot disable its delegation mid-session. The session level is set at creation only ("Session level" under DA-2). |
| D4 | The per-hop switch is `lease_slice.maxChildrenTotal: 0`, and `approvalMode` stops being an argument of `lenny/delegate_task` ("DA-6"). Revision 2 recorded the per-hop question as O6 and recommended `approvalMode: deny`. |
| D5 | The tenant hard switch is the SO-2 guardrail `"delegation": "forbid"`, set by a `security-admin`. A `tenant-admin` writes only `delegation.allowByDefault`, and the tenant record's `delegation.forbid` and `forbidSetBy` of revision 2 are dropped. Revision 2 recorded the question as O8. |

Open for the owner. Each carries a recommendation:

| ID | Question | Recommendation |
| :-- | :-- | :-- |
| O1 | How the levels combine. (A) Restrict-only at every level, so any `false` wins and a platform "default" of off can never be turned on below it. (B) Defaults resolved by specificity, a runtime overriding the tenant default and the tenant default overriding the platform default, with forbids that nothing below lifts, and restrict-only between a runtime, its base, and a tenant runtime entry. (C) As (B) without forbids. | (B), with the platform forbid in Helm and the tenant forbid as the delegation guardrail (D5), and with the tenant runtime entry (D1) as the tenant's narrowing of a single runtime. The owner asked for a platform and a tenant *default*, which a more specific level must be able to override, so (A) does not give one. Without a forbid, a tenant that wants no delegation cannot stop a platform runtime that sets `allowDelegation: true`, so (C) leaves the tenant without a hard switch. (B) follows the layering §8.6 uses for lease extension: defaults by specificity, capped by ceilings. |
| O3 | The platform default. | `gateway.delegation.allowByDefault: true`. §8.1 describes delegation as a platform primitive that every agent runtime can use, a deployment that sets nothing behaves as today, and the depth, fan-out, budget, and tree-size limits of §8.3 still bound every tree. A deployer who wants delegation off by default sets the value to `false`. |
| O4 | How a change reaches a running session. (A) Live in both directions: each call and each list reads the current settings. (B) A snapshot at creation, so a change reaches new sessions only. (C) Live for a change that disables, snapshot for one that enables. | (A), with a best-effort notification ("DA-5"). §8.3 already evaluates a `DelegationPolicy` live at each call, which lets an operator stop delegation during an incident without ending sessions. (B) leaves a disabled runtime delegating for the life of its sessions. (C) needs a stored snapshot beside the live read for no gain the owner asked for. The session's own choice (S1) stays stored, because it is the client's and nothing lifts it. |
| O5 | How derive and resume treat the session level. D3 settles that a session cannot change it mid-session. | A derive request accepts `delegationLease.maxDepth`, which can only narrow the value TS-3 carries over from the source. A resume keeps the stored value ("Session level" under DA-2). |
| O7 | Whether any other platform tool needs a per-runtime switch. | No. "DA-7" finds an existing control for each tool, and some of them leave the surface under other designs. A deployer who wants a tool held for approval uses an SO-5 rule. |

## Requirements

| ID | Requirement |
| :-- | :-- |
| DAR-1 | A session that may not delegate starts no child, whether the call comes from its pod or from a client at the gateway `/mcp` endpoint naming it as parent. |
| DAR-2 | The refusal names the level that made delegation unavailable. |
| DAR-3 | A platform forbid, the delegation guardrail, a tenant runtime entry, a base runtime's `false`, a session's `maxDepth: 0`, and a parent's `lease_slice.maxChildrenTotal: 0` are never lifted by a more specific level, by a later hop, or by lease extension. |
| DAR-4 | The platform tool list of a session that may not delegate omits `lenny/delegate_task` and `lenny/discover_agents`, and also omits `lenny/await_children` and `lenny/cancel_child` when the session has no child. |
| DAR-5 | A change at the tenant, security record, tenant runtime entry, or runtime level governs the next `lenny/delegate_task` call of every running session it reaches. |
| DAR-6 | Making delegation unavailable ends no running child, and a parent with children keeps the tools that manage them. |
| DAR-7 | A deployment that sets none of the new values behaves as today. |
| DAR-8 | A session cannot reach a root-session creation tool from its pod's platform MCP server, so the setting cannot be bypassed by creating a new root through that path. TS-3's allow-list provides this, and this design depends on it. F-9.1.13 records the defect. "Limits" states the path through the gateway's public API, which this requirement does not cover. |
| DAR-9 | Removing the delegation guardrail takes effect only after the SO-2 weakening delay, and every write that widens or narrows availability at any level writes an audit event that names its direction. |

## Current state

**No availability setting [source].** §5.1 gives the Runtime `delegationPolicyRef` and `allowSelfRecursion` (`pkg/apis/lenny/v1alpha1/runtime_types.go`, `runtimestore.Runtime`). §5.2 gives pools no delegation field. The tenant record carries no delegation field, although it carries `messaging.scope` (§7.2) and `leaseExtension` (§8.6). The Helm values that concern delegation are `gateway.cycleDetection.mode`, `gateway.allowSelfRecursion`, and `gateway.delegation.defaultMaxDepth` (§8.3), and the last is not rendered by the chart (F-8.3.12).

**A per-tenant runtime record exists [source].** §5.1 states "Capabilities are customizable per tenant", and the admin API serves `GET`, `PUT`, and `DELETE /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}` (`pkg/gateway/externalapi/admin/runtime_capability_overrides.go`), whose body holds capability axes such as `interaction` and `sdkWarmBlockingPaths`. RV-1 makes the record a narrowing ("Per-tenant capability override"). It carries no delegation field.

**`maxDepth` cannot be 0 today [source].** §8.2 **Effective `maxDepth` resolution** requires every effective lease to carry a positive integer, and every layer of its chain can only produce one. A client's `delegationLease` at creation is validated, stored, and echoed with no effect (F-8.2.34). TS-3 wires it and admits 0 as decision 26.

**A zero `maxChildrenTotal` in a lease slice means no limit [source].** §8.2's `LeaseSlice` table describes `maxChildrenTotal` as "Max children the child may spawn". The `lenny/delegate_task` input schema carries `leaseSlice.maxChildrenTotal` as an integer, `lease.ValidateChildSlice` checks a child's value only when both the parent's and the child's are greater than 0, and the stored `sessionstore.DelegationLease` documents that "A zero field means 'no limit set at this scope'". A parent that passes 0 today grants its child unlimited children. The session-creation request is different: `sessionstore.DelegationLeaseRequest` holds `maxDepth` and `maxChildrenTotal` as pointers so an explicit zero is distinguishable. §8.6 lists `maxChildrenTotal` among the extendable fields.

**`DelegationPolicy` rules are not applied to `lenny/delegate_task` [source].** `DelegationPolicy.Evaluate` has these production callers: `filterByEffectiveDelegationPolicy` for `lenny/discover_agents` (`pkg/gateway/mcpfabric/mcptools/mcptools.go`), `connectorauthz` for connector calls, and the pool isolation audit. The `lenny/delegate_task` handler (`mcptools_register.go`) checks environment scope through `runtimeAuthorizedForCaller` and `crossEnvReachable`, and `Service.detectCycle` (`pkg/gateway/mcpfabric/delegation/service.go`) reads the *target* runtime's `delegationPolicyRef` for the self-recursion layer and the content policy. No step evaluates the calling session's policy rules against the target. A policy that allows nothing therefore hides every target from discovery and blocks no delegation. The audit note 8.3-H1 in `BUILD-GAPS.md` records the missing call with no checkbox or F-id, and revision 2 files it as F-8.3.14.

**`approvalMode: deny` refuses the caller's own call [source].** §8.4 defines `deny` as "Delegation not permitted" on the lease, and §8.4 states that the `approval` value "is accepted at policy registration time". `lenny/delegate_task` accepts `approvalMode` as an argument, and `validateDelegation` returns `ErrDelegationDenied` (`DELEGATION_DENIED`) when the caller passes `deny`, before it reads the parent. Nothing stores the value on the child's lease, and nothing sets it on a root, so a runtime can only refuse itself (F-8.4.1's resolution). Revision 2 files this as F-8.4.4.

**`DELEGATION_DENIED` lives only in code and in the adapter proto [source].** No specification section and no page under `docs/` names it. The code names it in `errorclassify` (`"DELEGATION_DENIED": {CategoryPolicy, false}`), in the handler of `mcptools_register.go`, in comments of `delegation/service.go`, and in `delegate_approvalmode_test.go`. `schemas/lenny-adapter.proto` declares `ERROR_CODE_DELEGATION_DENIED = 20`, and the generated `pkg/proto/adapter/v1/lenny-adapter.pb.go` carries it.

**How the tool list is built [source].**

- The adapter starts the platform MCP server once per pod (`startPlatformMCP`, `pkg/adapter/platformmcp.go`). Each `tools/list` goes to `platformToolProvider.List`, which resolves the calling session through `callingSession` and forwards `ListPlatformTools` with that session's ID over `GatewayControl` (`pkg/adapter/platformtoolprovider.go`).
- The gateway's `Bridge.ListPlatformTools` (`pkg/gateway/gatewaycontrol/platformtools/platformtools.go`) ignores the session ID and returns `Catalog()` of the gateway's `/mcp` server. Its comment states that "the catalog is uniform across type:agent sessions".
- That catalog holds every tool `mcptools.Register` installs: the tools of §9.1's table, `lenny/vcs_token`, and the client-facing tools such as `lenny/create_session`, `lenny/create_and_start_session`, `lenny/list_sessions`, and `lenny/upload_files`. A runtime can call `lenny/create_session` from its pod and start a new root session outside its delegation lease. TS-3 found this and allow-lists the bridge to §9.1's table. `BUILD-GAPS.md` already records it as F-9.1.13, so revision 2 files no new finding for it.
- `lenny/delegate_task` is never refused for being disabled, because nothing disables it.

**The server cannot announce a change [source].** `initializeResponse` in `pkg/adapter/mcp/server.go` declares `"tools": {}` with no `listChanged`, and the serve loop only answers requests. The server speaks MCP `2025-03-26`, which MP-5 replaces. CA-8 found the same for connector servers and proposes the adapter changes and a gateway-to-adapter RPC, `NotifyConnectorToolsChanged`, for a runtime that declares `deferredToolDiscovery`.

**A process can outlive a session [source].** The platform MCP server is pod-wide, and `callingSession` resolves the caller per request to the single session the pod's shared runtime process holds. §28.5.3 states that a later session's manifest write does not re-arm the server. TS-3 notes that a recycled or SDK-warm process can hold a list from before its current session.

**What MCP requires [external].** Under `2025-11-25`, a server declares its capabilities in the `initialize` result, and a server that declares `tools.listChanged` "SHOULD" send `notifications/tools/list_changed` when its tools change, after which the client lists again. Under `2026-07-28`, no `initialize` handshake exists for a modern client: a server "MUST implement `server/discover`", whose result carries `capabilities`, and the server sends the notification on a `subscriptions/listen` stream the client opened with `toolsListChanged: true`. A dual-era server may serve both. `2026-07-28` also states that the tool set "MAY change over time" and "MUST NOT vary per-connection", that it "MAY vary by the authorization presented on the request", and that every `tools/list` result carries `ttlMs` and `cacheScope`, where `ttlMs: 0` marks the result stale at once and `cacheScope: "private"` forbids sharing it across authorization contexts. It recommends a deterministic tool order because it "improves LLM prompt cache hit rates when tools are included in model context". Both versions report an unknown tool as JSON-RPC error `-32602`.

**Runtimes and list changes [source, from CA-9].** In the spike of [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#spike-results-for-claude-code), Claude Code 2.1.317 listed again on `notifications/tools/list_changed` and could call a tool added that way. No run tested a tool removed that way, and no Codex run is recorded.

**Precedents for layered settings [source].** §7.2's `messagingScope` resolves deployment, tenant, and runtime as "most-restrictive wins, can only narrow". §8.6's lease extension settings resolve by specificity, runtime over tenant over deployment, capped by a tenant maximum and a deployment maximum.

## DA-1 Levels

| Level | Fields | Written by | Values |
| :-- | :-- | :-- | :-- |
| Platform | `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` (Helm, rendered to gateway flags) | Deployer | `allowByDefault`: `true` (default, O3) or `false`. `forbid`: `false` (default) or `true`. |
| Tenant default | `delegation.allowByDefault` on the tenant record, through `PUT /v1/admin/tenants/{id}/delegation` with `If-Match` | `tenant-admin` for its tenant, `platform-admin` for any | Unset, `true`, or `false`. |
| Tenant guardrail | `"delegation": "forbid"` in the tenant security record of SO-2, through `PUT /v1/admin/tenants/{id}/security-config` | `security-admin`, or `platform-admin` with a justification under SO-4 (D5) | Absent or `"forbid"`. |
| Tenant runtime entry | `allowDelegation` on the tenant runtime entry for a runtime, through `PUT /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}` | `tenant-admin` for its tenant, `platform-admin` for any (D1) | Unset or `false`. |
| Runtime | `allowDelegation` | `platform-admin` on a platform runtime. `tenant-admin` on a tenant-derived runtime, under "Derived runtimes" below. | Unset, `true`, or `false`. |
| Session | `delegationLease.maxDepth` at creation, and `lease_slice.maxChildrenTotal: 0` on the parent's `lenny/delegate_task` | Client, and the parent session | Under "Session level" in DA-2 and under DA-6. |

**Derived runtimes.** `allowDelegation` merges restrict-only, as `allowSelfRecursion` does under §5.1. A derived runtime may set `false` on any base. It may set `true` only when the base's value is not `false`, and the write answers `422 INVALID_DERIVED_RUNTIME` with `details.field: "allowDelegation"` otherwise. The merged value is `false` when either value is `false`, `true` when either is `true` and neither is `false`, and unset otherwise. A base that later changes to `false` makes the merged value `false` at the next resolve, which DR-6 already lists as a narrowing that "applies at the next resolve". A derived runtime's `true` over an unset base lets a tenant override its own tenant default for that runtime, and it cannot lift a forbid.

**Workers beside orchestrators.** An operator who runs one base runtime as an orchestrator that delegates and as workers that must not delegate registers a derived runtime for the workers with `allowDelegation: false`, with its own pool at the isolation profile and size the workers need. A tenant does the same with a tenant-derived runtime and its owned pool. The client or the orchestrator names the worker runtime as the target, so the setting sits on the name the caller selects, and a `PreRoute` interceptor that moves a session to another pool of the same runtime does not change its availability. This is the case revision 2 served with a pool-level `false`, which the owner dropped (D2).

**Owned pools.** Pools carry no delegation field, so the template copy of DR-5 copies none, and an owned pool has no delegation rule of its own.

**Tenant runtime entry.** A tenant runtime entry with `allowDelegation: false` applies to the sessions of that tenant whose runtime is the named runtime or a derived runtime whose base it is, merged restrict-only as above. It accepts only `false`: a write of `true` is refused as a widening, as RV-1's "Per-tenant capability override" refuses a widening of `capabilities.interaction`, because a tenant widens a runtime only through a tenant-derived runtime. Clearing the field removes the narrowing. The entry is the tenant's way to turn delegation off for one platform runtime whose `allowDelegation: true` overrides the tenant default (D1).

**`type: mcp` runtimes.** A write that sets `allowDelegation` on a `type: mcp` runtime, or on a tenant runtime entry for one, answers `INVALID_RUNTIME: allowDelegation is only valid on type: agent runtimes`, as §5.1 does for `integrationLevel`, because such a runtime has no sessions with platform tools and cannot delegate.

**Writes and audit [design].** Each field is part of an existing admin write, so the existing audit rows for runtime, tenant, tenant runtime entry, and security record changes record it. On top of them, a write at the tenant default, the tenant runtime entry, or the runtime level that changes the resolved availability of any running or future session writes one of these events, carrying the level, the old and new values, and the actor: `delegation.availability_narrowed` when it makes delegation unavailable where it was available, and `delegation.availability_widened` when it does the reverse. A SIEM rule can then alert on widening alone.

**The guardrail's transitions [design].** The delegation guardrail follows SO-2's rules for every forbid in the record. Setting it is a tightening: it applies in the write's transaction and emits `security.config_tightened`, and the fan-out of "DA-5" starts at once. Removing it is a weakening: it needs a `justification`, creates a pending version, emits `security.config_weakening_requested` at once, and takes effect only at `effectiveAt`, after the tenant's `weakeningDelaySeconds`, when the applier emits `security.config_weakened` and the fan-out starts. A `security-admin` can cancel the pending removal. A `tenant-admin` cannot write the record, so a tenant's own administrators cannot lift the guardrail, and a stolen administrator credential cannot lift it inside the delay without the security team seeing the request (D5, DAR-9).

**Switches for an incident.** `gateway.delegation.forbid` takes effect only when the gateway restarts with the new value, so it is a deployment posture and not a switch for an incident. During an incident an operator uses a live level: a `platform-admin` sets `allowDelegation: false` on the runtime involved, which reaches every tenant's sessions of that runtime at their next call, and a tenant's `security-admin` sets the delegation guardrail, which applies in the write's transaction. A `tenant-admin` can also set the tenant default to `false` or write a tenant runtime entry. Each change reaches running sessions through the fan-out of "DA-5" and is enforced at their next call.

## DA-2 Resolution

For a session S with tenant T and runtime R (merged with its base when R is derived), read live at each call:

| Order | Condition | Result | `disabledBy` |
| :-- | :-- | :-- | :-- |
| 1 | `gateway.delegation.forbid: true` | Unavailable | `platform` |
| 2 | T's active security record carries `"delegation": "forbid"` | Unavailable | `securityGuardrail` |
| 3 | T's tenant runtime entry for R, or for R's base, carries `allowDelegation: false` | Unavailable | `tenantRuntime` |
| 4 | R's merged `allowDelegation` is set | That value | `runtime` when `false` |
| 5 | T's `delegation.allowByDefault` is set | That value | `tenantDefault` when `false` |
| 6 | Otherwise | `gateway.delegation.allowByDefault` | `platformDefault` when `false` |

S may delegate when it is available and has room. S has room when its stored `maxDepth` is greater than its own depth and its stored `maxChildrenTotal` is not an explicit 0. A session without room gets one of these refusals:

- `DELEGATION_DISABLED` with `disabledBy: session` when its stored `maxDepth` is 0, which a client sets at creation, or its stored `maxChildrenTotal` is an explicit 0, which a client sets at creation or a parent sets through `lease_slice` (DA-6). Either value states that the session is not to delegate.
- `DELEGATION_DEPTH_EXCEEDED`, as today, when its stored `maxDepth` is positive and not greater than its depth, because its tree can delegate and it has reached the limit.

The rule and "DA-6" therefore give one refusal for a child that a parent created unable to delegate: `DELEGATION_DISABLED` with `disabledBy: session`. Revision 2 stored the child's depth as its `maxDepth` for this case, which this rule would have answered with `DELEGATION_DEPTH_EXCEEDED`, and the review found the disagreement.

**Examples.** Platform default `true` with no forbid:

| Tenant default | Tenant runtime entry | Runtime | Effective | Reason |
| :-- | :-- | :-- | :-- | :-- |
| Unset | Unset | Unset | Available | Platform default |
| `false` | Unset | Unset | Unavailable, `tenantDefault` | The tenant default replaces the platform default |
| `false` | Unset | `true` | Available | The runtime is more specific than the tenant default |
| Unset | `false` | `true` | Unavailable, `tenantRuntime` | The tenant narrows one runtime |
| `true`, with the guardrail set | Unset | `true` | Unavailable, `securityGuardrail` | A forbid wins over every value below it |

**Live reads.** The gateway reads the settings at each `lenny/delegate_task` call and each `ListPlatformTools` call (O4). The tenant, security record, tenant runtime entry, and runtime rows are read through the caches the gateway already keeps for them, and the platform values are process flags. A change reaches a running session at its next call. This matches §8.3's rule that a delegation policy "is re-evaluated on each `delegate_task` call", and it keeps §8.3's rule that a change "does not affect active child sessions": a child already running continues (DAR-6).

**Delegated children.** A child's own availability is resolved from the child's row, whose runtime is the one its `PreRoute` chain (`kind: delegate`) settled at the post-chain point. The post-chain point gains no check: availability is a property of the session as a delegator, and a runtime with delegation unavailable can still be a target. Whether it can be a target is the `DelegationPolicy`'s question. The parent's availability is read from the parent's row before the chain runs.

**Session level.** The session level reuses decision 26 and adds no field:

- **Create.** A client sets `delegationLease.maxDepth: 0` on `POST /v1/sessions`, `POST /v1/sessions/start`, and `lenny/create_and_start_session`, as TS-3 wires them. `delegationLease.maxChildrenTotal: 0` has the same effect, and TS-3 stores it as `maxDepth: 0`. `delegationLease.delegationPolicyRef` is accepted and has no effect (F-8.2.34), so a client cannot rely on a policy that allows nothing.
- **Narrowing only.** A session's `maxDepth` combines with the other layers by minimum under TS-3, so a session can turn delegation off and can never turn it on where any level makes it unavailable. A session that sets a positive `maxDepth` on a runtime with delegation unavailable gets `disabledBy` from that level.
- **Children.** A child stores its parent's `maxDepth` under TS-3, so a root at 0 has no children, and a parent with room can narrow one child with `lease_slice.maxChildrenTotal: 0` (DA-6). The availability levels are not inherited: each child resolves its own runtime, and its tree's stored `maxDepth` still bounds it.
- **Mid-session (D3).** No request changes a session's stored `maxDepth` or `maxChildrenTotal` to disable delegation, and §8.6 already makes `maxDepth` not extendable. An operator who must stop a running session's delegation uses a level above the session, which takes effect at the next call.
- **Derive (O5).** TS-3 stores the source's `maxDepth` minus the source's depth, with a floor of 0. This design adds `delegationLease.maxDepth` to the derive request, which can only lower that value, so a client can derive a session that cannot delegate. The derived session resolves availability from its own runtime, which [`session-derive-design.md`](session-derive-design.md#sd-1-one-route) lets differ from the source's.
- **Resume and recovery (O5).** A resume keeps the row, so it keeps the stored value, and the levels are read live after it. Tree recovery brings a node back on its stored lease (§8.10), and the node's next call reads the levels live.

## DA-3 Refusal

The gateway refuses `lenny/delegate_task` with `403 DELEGATION_DISABLED`, which TS-3 introduces with category `POLICY` and `retryable: false`. This design adds `details.disabledBy`, one of `platform`, `securityGuardrail`, `tenantRuntime`, `runtime`, `tenantDefault`, `platformDefault`, and `session`, and `details.effectiveSettings` with the values each level held, as §8.2's cycle refusal reports `blockedBy` and `effectiveSettings`. A `runtime` value names the runtime and states whether the `false` came from its base, a `tenantRuntime` value names the runtime the entry names, and a `securityGuardrail` value names the active security record version.

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

The list changes only when "may delegate" changes. "Has a child" becomes true only through a delegation, which requires "may delegate", and stays true, so a session that loses delegation keeps the tools that manage the children it has (DAR-6). The list keeps a fixed tool order, as `2026-07-28` recommends.

`Bridge.CallPlatformTool` does not check the list. A call to a tool the session's list omits reaches the handler, which answers `DELEGATION_DISABLED` for `lenny/delegate_task` and the existing answers for the others. A call to a name outside TS-3's allow-list answers `ErrPlatformToolNotFound`. Today `callTool` in `pkg/adapter/mcp/server.go` reports any forwarded failure as JSON-RPC `-32603` **[source]** (F-15.4.8), and this design has the adapter report the not-found status as `-32602`, the MCP error for an unknown tool **[external]**.

The gateway `/mcp` endpoint keeps one catalog for every client, as TS-3 states, because a connection names no session until a call does. The refusal covers it.

**`type: mcp` runtimes and runtimes that use MCP.** A `type: mcp` runtime is an MCP server the gateway calls, and it has no platform MCP server and no delegation, so nothing in this design reaches it beyond the refused writes in DA-1. An agent runtime whose binary is an MCP client connected to several servers, such as Claude Code with the platform server, connector servers, and servers of its own, sees the platform server's list change as it would any server's, and a client that prefixes tool names by server, as Claude Code did in the CA-9 spike, keeps the prefix on the platform tools. The virtual child interface of §8.2 step 8 exists only for children, so a session that cannot delegate gets none, and a session that loses delegation keeps the interfaces of its existing children.

## DA-5 Running sessions

**Declaration.** The platform MCP server declares `"tools": {"listChanged": true}` on every connection, whatever the runtime declares **[design]**. Under `2025-11-25` the declaration is in the `initialize` result. Under `2026-07-28` it is in the `capabilities` of the `server/discover` result, because a modern client sends no `initialize` **[external]**. A client that ignores the notification is unaffected by receiving it, and a client that honours it lists again. This differs from CA-8's connector servers, which tie the declaration to `deferredToolDiscovery` because a failing connector list breaks some clients, a risk a platform list that changes without failing does not carry.

**Per-session list in the adapter [design].** The adapter keeps one platform tool list per session. The first `tools/list` for a session fills it from `ListPlatformTools`, and later `tools/list` requests for the same session are answered from it inside the pod. The adapter clears the session's list when the gateway's notification RPC names the session, and when it binds a new session to the runtime process, and the next `tools/list` asks the gateway again. Every answer carries `ttlMs: 0` and `cacheScope: "private"` under `2026-07-28`, so the runtime treats the list as stale at once and never shares it across sessions, while the gateway is asked once per change.

**Notifications [design].** The adapter sends `notifications/tools/list_changed` on each open platform connection in these cases:

1. **A setting changed for the session.** The gateway sends the adapter the notification RPC that CA-8 adds, generalized to carry the server: the platform server or one connector ID. The adapter checks that the named session is the one its runtime process holds, clears that session's list, and sends the notification.
2. **A new session binds to a process that has listed.** When the adapter binds a session to a runtime process that already holds an open platform connection on which it answered `tools/list`, it clears the list and sends the notification after the bind, because the new session's list can differ from the previous session's.

**Fan-out.** A tenant default write, a tenant runtime entry write, a runtime write, or an activation of the security record that changes a value the resolution reads makes the gateway enumerate the non-terminal sessions with a bound pod whose tenant or runtime matches, recompute "may delegate" for each, and send the RPC for each session whose value changed, through the session's coordinating replica over `CH-HOLDERFORWARD`, as CA-8 does for a link callback. A guardrail that is set fans out in its write's transaction, and a guardrail that is removed fans out when the pending version activates. The fan-out is best effort and rate limited by a gateway flag, `--delegation-list-change-rate`, default 200 notifications per second per replica, which is a judgment. A failure is logged and counted in `lenny_platform_tools_list_changed_total{outcome}`, and the refusal stays the control. A change to a platform Helm value takes effect when the gateway restarts with it and sends no fan-out, so a running process sees it at its next call or its next bind.

**MCP versions.** Under MP-5 the intra-pod servers serve `2025-11-25` and `2026-07-28`. On `2025-11-25` the notification goes on the connection. On `2026-07-28` it goes on a `subscriptions/listen` stream that the client opened with `toolsListChanged: true`, so the intra-pod server implements `server/discover` and `subscriptions/listen`, which MP-5 does not list today. The list varies by the session the adapter resolves for each request and changes over time as settings and sessions change, which `2026-07-28` permits, and it does not vary by connection **[external]**.

## DA-6 Per-hop narrowing

**The switch (D4) [design].** A parent that passes `lease_slice.maxChildrenTotal: 0` on `lenny/delegate_task` creates the child with no room. The child's stored lease records `maxChildrenTotal` as an explicit 0, the child cannot delegate, its list omits the delegation tools, and its refusal names `disabledBy: session` under the rule of DA-2. A grandchild cannot exist, so no inheritance rule is needed below it. The field already means "Max children the child may spawn" in §8.2, so 0 reads as its plain meaning.

**Explicit zero.** The tool's input distinguishes an explicit 0 from an absent field, as `DelegationLeaseRequest` already does on session creation, and the stored lease of the child distinguishes them as well. An absent field keeps today's meaning, no limit set at this hop. `lease.ValidateChildSlice` admits an explicit 0 against any parent, because it narrows. This changes the meaning of an explicit 0 in a slice, which today means no limit (S4).

**Extension.** §8.6 lists `maxChildrenTotal` as extendable. An extension never raises a `maxChildrenTotal` that is an explicit 0, because the 0 records the parent's decision that the child is not to delegate, which an exhausted budget does not.

**Removal of the argument.** `lenny/delegate_task` stops accepting `approvalMode`. `ErrDelegationDenied` and the `DELEGATION_DENIED` code go from `validateDelegation`, the handler of `mcptools_register.go`, `errorclassify`, and `delegate_approvalmode_test.go`, because no caller refuses itself any longer. `schemas/lenny-adapter.proto` drops `ERROR_CODE_DELEGATION_DENIED = 20` and declares `reserved 20;` and `reserved "ERROR_CODE_DELEGATION_DENIED";` in the `ErrorCode` enum, so neither the number nor the name is reused, and the generated code is regenerated. No specification section changes for the code, because none names it. §8.4's `approvalMode` stays a lease field set at policy registration, so the lease example of §8.3 is unchanged. The removal closes the self-refusal half of F-8.4.4. The other half, that no path stores `deny` on a lease, stays with F-8.4.4.

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
- **A list change costs a prompt-cache write.** A runtime that lists again sends its model a changed set of tool definitions. Providers cache a prompt prefix in the order tools, system, and messages, and a change to the tools invalidates the cached prefix after it **[external]**, so the runtime's next model call after a change pays the provider's cache-write price for its whole prompt. A change at a tenant or runtime level reaches every running session of that tenant or runtime, so it costs one such write per session that lists again. The fixed tool order of "DA-4" keeps a list that did not change from costing one.
- **Removal after a notification is unmeasured.** The CA-9 spike measured a tool added through the notification and no tool removed. "Validation owed" lists it.
- **The model is not told of the change.** In the CA-9 spike, nothing in the stream showed the model that its tools changed. A model that planned to delegate learns of the change from the refusal text.
- **A Helm forbid is not a switch for an incident.** `gateway.delegation.forbid` and `gateway.delegation.allowByDefault` take effect when the gateway restarts with them, and they send no fan-out, so a process sees them at its next call or bind after the restart. "Switches for an incident" under DA-1 names the live levels.
- **Lifting the guardrail waits out the delay.** A tenant that set the delegation guardrail during an incident regains delegation only after the SO-2 weakening delay, which is the protection D5 asks for.
- **Fan-out cost.** A tenant-wide change sends one RPC per running session in the tenant whose value changed, bounded by the rate flag, so a large tenant's sessions learn of it over seconds to minutes.
- **The gateway `/mcp` catalog still lists the tool.** A client at the gateway sees `lenny/delegate_task` and meets the refusal.
- **A disabled level does not stop a running child.** A child created before the change runs to completion, and its own next call reads the new value.
- **The public API is a path around the setting.** DAR-8 closes the pod's path to root-session creation through the platform MCP server. A pod whose egress reaches the gateway's public API and which holds a client credential, such as a user's bearer passed into the workspace, can call `POST /v1/sessions` and start a root session outside its delegation lease and outside this setting. The egress policy of the pod and the handling of client credentials govern that path, and this design adds no check to it.
- **An explicit zero changes meaning.** A runtime written against the current code, which treats `leaseSlice.maxChildrenTotal: 0` as no limit, now creates a child that cannot delegate.

## Alternatives rejected

- **Express "disabled" as a `DelegationPolicy` that allows nothing.** The policy decides targets, it has no default at the tenant or the platform, the gateway does not apply its rules to `lenny/delegate_task` today, and "allows nothing" can only be known by evaluating every runtime's labels, so the tool list could not follow it.
- **Use `maxDepth: 0` in the Runtime's default lease or in a preset.** §8.2's item 3 and item 2 would carry it, and TS-3's minimum would make it restrict-only. Neither layer exists in code, a minimum gives no default that a lower level can override, and it ties availability to the depth budget, so a refusal could not name the level.
- **Make the levels restrict-only (O1 option A).** A platform or tenant default of `false` could never be overridden, so it would be a forbid under another name.
- **Snapshot availability at creation (O4 option B).** A runtime turned off during an incident would keep delegating for the life of its sessions.
- **A pool level.** Revision 2 proposed `allowDelegation: false` on a pool. A `PreRoute` interceptor or a runtime route can move a session to another pool of the same runtime, so a pool setting would make availability depend on a placement the client and the runtime author do not see, and the pool level added an owned-pool copy rule, a resolution step, and a fan-out trigger. A derived runtime with `allowDelegation: false` serves the same case on the name the caller selects. The owner dropped the pool level (D2).
- **`approvalMode: deny` as the per-hop switch.** Revision 2 recommended giving `deny` that meaning. `approvalMode` is a policy-level enum whose `approval` value is reserved, and a field of the lease slice that already counts children carries the meaning directly. The owner chose `lease_slice.maxChildrenTotal: 0` (D4).
- **A tenant forbid on the tenant record.** Revision 2 put `delegation.forbid` and `forbidSetBy` on the tenant record, written by a `tenant-admin` or a `platform-admin`. A forbid that the tenant's own administrators can clear protects no one from a stolen administrator credential, and the security record already gives every forbid a single writer, a weakening delay, and SIEM events. The owner moved the hard switch there (D5).
- **Keep a uniform tool list and rely on the refusal.** The owner asked that the tool not be offered, and an agent offered a tool it cannot use wastes turns on it.
- **Omit the platform server from the manifest when delegation is unavailable.** The other platform tools would go with it.
- **A per-runtime allow-list of platform tools.** Every tool already has a control of its own (DA-7), a list of names would duplicate those controls without their semantics, and an omitted core tool such as `lenny/output` would break a session.
- **An environment level.** Environments already scope targets through `defaultDelegationPolicy`, and another level would add a resolution step with no use case the owner named.

## Specification changes

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), the Runtime YAML and field notes, "Normative Merge Algorithm", and "Capabilities are customizable per tenant" | Add `allowDelegation` with its values, its refusal on `type: mcp`, and a merge row: restrict-only, a derived `true` refused over a base `false`, and the merged value of "DA-1". State that a derived runtime that sets `false` is the way to run workers that do not delegate beside an orchestrator of the same base. State that the per-tenant record accepts `allowDelegation: false` and refuses `true`. |
| [§8.1 Design Philosophy](../spec/08_recursive-delegation.md#81-design-philosophy) | State that whether a session may delegate is decided by the binary and by the configuration of "Delegation availability" in §8.3. |
| [§8.2 Delegation Mechanism](../spec/08_recursive-delegation.md#82-delegation-mechanism), the `LeaseSlice` table and **Delegation flow** | State that an explicit `maxChildrenTotal: 0` creates a child that starts no child, and that an absent field sets no limit. Add the availability and room check as the first step after the parent is fixed, with `DELEGATION_DISABLED` and `details.disabledBy`. |
| [§8.3 Delegation Policy and Lease](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) | Add a paragraph **Delegation availability** with the levels, the resolution table of "DA-2", the room rule and its refusals, the live reads, the session level, and the switches for an incident. Add `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` to **Helm settings governing cycle detection and delegation depth**, stating that they apply at restart. |
| [§8.5 Delegation Tools](../spec/08_recursive-delegation.md#85-delegation-tools) | Replace "Available on the platform MCP server for every delegation-capable pod" with the per-session listing of "DA-4". |
| [§8.6 Lease Extension](../spec/08_recursive-delegation.md#86-lease-extension) | State under **Extendable fields** that an extension never raises a `maxChildrenTotal` that is an explicit 0. |
| [§9.1 Where MCP Is Used](../spec/09_mcp-integration.md#91-where-mcp-is-used), **Platform MCP Server Tools** | State that the list is per session, with the rule of "DA-4", and that a call is answered by the tool's own refusal. |
| [§14 Workspace Plan Schema](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) and the derive request in [§15.1](../spec/15_external-api-surface.md#151-rest-api) | Add `delegationLease.maxDepth` to the derive request, narrowing only. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | Add `GET` and `PUT /v1/admin/tenants/{id}/delegation` with `allowByDefault` alone, `allowDelegation` on the runtime payload and on the per-tenant runtime record, and `details.disabledBy` and `details.effectiveSettings` on `DELEGATION_DISABLED`. |
| [§15.4.3 Runtime Integration Levels](../spec/15_external-api-surface.md#1543-runtime-integration-levels) | State that the platform server declares `tools.listChanged` in the `initialize` result and in the `server/discover` result, the cases in which it notifies, the per-session list in the adapter, and the cache hints on its list. |
| [§16.1 Metrics](../spec/16_observability.md#161-metrics) and [§16.7 Section 25 Audit Events](../spec/16_observability.md#167-section-25-audit-events) | Add `lenny_delegation_disabled_refusals_total{disabled_by}`, `lenny_platform_tools_list_changed_total{outcome}`, `delegation.availability_narrowed`, and `delegation.availability_widened`. |
| [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod), the `CH-MCP-PLATFORM` card | **Messages**: the per-session tool set, the notification, and the declaration in `server/discover`. **Preconditions**: the list depends on the calling session. Name the RPC CA-8 adds, generalized to the platform server, under the naming law of §28.1 at proposal time. |

The delegation guardrail's place in the tenant security record, and its tightening and weakening, are specification edits of [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture), which "Instructions for reviewers of other designs" asks for. This design's §8.3 paragraph cites them.

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/apis/lenny/v1alpha1`, `pkg/controller/runtime`, and `pkg/gateway/runtime/runtimestore` | `AllowDelegation *bool` on the Runtime resource, its mirror, the registry row, and `Merge`. Regenerate the CRD manifests. |
| `pkg/gateway/externalapi/admin` | The field on runtime payloads with the derived and `type: mcp` refusals, `allowDelegation` on the per-tenant runtime record (`runtime_capability_overrides.go`) accepting only `false`, the tenant `delegation` sub-resource with `allowByDefault` alone, and the narrowed and widened audit events. |
| `migrations` | Columns for the runtime field, the per-tenant runtime record field, and the tenant default. |
| `charts/lenny` | `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` in `values.yaml` and the schema, rendered to gateway flags. |
| `pkg/delegation` | A resolver that takes the tenant row, the active security record, the tenant runtime entry, the runtime row, and the platform flags and returns availability and `disabledBy`, shared by the handler, the service, and the bridge. `lease.ValidateChildSlice` admits an explicit `maxChildrenTotal: 0`. |
| `pkg/gateway/mcpfabric/mcptools` and `pkg/gateway/mcpfabric/delegation` | The two checks of "DA-3", the details, and the counter. The tool's input schema loses `approvalMode` and distinguishes an explicit `leaseSlice.maxChildrenTotal: 0`. `ErrDelegationDenied`, the `DELEGATION_DENIED` answer, and `delegate_approvalmode_test.go` go. |
| `pkg/gateway/session/sessionstore` and the lease extension path | The stored `DelegationLease` distinguishes an explicit `maxChildrenTotal: 0`, and `ExtendLease` never raises it. |
| `pkg/gateway/externalapi/errorclassify` | Remove the `DELEGATION_DENIED` entry and its test row. |
| `schemas/lenny-adapter.proto` and `pkg/proto/adapter/v1` | Replace `ERROR_CODE_DELEGATION_DENIED = 20` with `reserved 20;` and `reserved "ERROR_CODE_DELEGATION_DENIED";`, add the RPC CA-8 adds with a field naming the server, and regenerate the Go code. |
| `pkg/gateway/gatewaycontrol/platformtools` | `ListPlatformTools` filters by the calling session under "DA-4", in a fixed order. |
| `pkg/adapter/mcp` and `pkg/adapter` | `listChanged: true` in the `initialize` and `server/discover` results, the per-session list and its clearing, notifications on open connections serialized against responses, the notification after a bind, the RPC handler, the cache hints, and `-32602` for a forwarded not-found status. The connection writer is shared with CA-8. |
| `pkg/gateway` admin write paths and the SO-2 applier | The fan-out of "DA-5" with its rate flag and counter, including the activation of a pending security record version. |
| `docs/` | An operator page on delegation availability, its levels, the switches for an incident, and the derived-runtime pattern for workers, and the runtime author guide's statement that the platform tool list can change during a session and that a change costs a prompt-cache write. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 0 | `schemas/lenny-adapter.proto` reserves number 20 and the name `ERROR_CODE_DELEGATION_DENIED`, and no Go file outside `proposals/` and the records names `DELEGATION_DENIED`. |
| 1 | The resolver over every combination of the levels, the guardrail, and the tenant runtime entry, with `disabledBy` for each (DAR-2, DAR-3). The derived merge and its refusal. A tenant runtime entry that names a base narrows its derived runtimes, and a write of `true` is refused. The list rule of "DA-4" for may-delegate and has-child, in a fixed order. An explicit `maxChildrenTotal: 0` in a slice is stored as 0 and leaves the child with no room, an absent field sets no limit, and both refusal paths of DA-2 name `disabledBy: session` for a client's 0 and a parent's 0. An extension request never raises an explicit 0. |
| 2 | The registry, tenant, and per-tenant runtime record stores round-trip the fields, and the RuntimeReconciler mirrors `allowDelegation`. |
| 3 | The `DELEGATION_DISABLED` envelope with `disabledBy` and `effectiveSettings`. The platform server declares `listChanged` in its `2025-11-25` `initialize` result and in its `2026-07-28` `server/discover` result, a `2026-07-28` `tools/list` carries `ttlMs: 0` and `cacheScope: "private"`, and a call to a name outside the allow-list answers `-32602`. The `lenny/delegate_task` input schema has no `approvalMode`. |
| 4 | On the compose stack: a session on a runtime with `allowDelegation: false` lists no delegation tools and is refused from the pod and from the gateway `/mcp` (DAR-1, DAR-4). A tenant default of `false` with a runtime `true` delegates, and a tenant runtime entry for that runtime then refuses it with `tenantRuntime`. A guardrail written by a `security-admin` refuses at the next call, a `tenant-admin` cannot write it, and its removal takes effect only at the end of the weakening delay and can be cancelled (DAR-9). Each narrowing and widening write emits the event of its direction. A runtime set to `false` mid-session refuses the next call, leaves the running child alive, and keeps `lenny/await_children` listed (DAR-5, DAR-6). A derived runtime with `allowDelegation: false` lists no delegation tools while its base's sessions delegate. A derive with `delegationLease.maxDepth: 0` cannot delegate. A deployment with no values set behaves as today (DAR-7). |
| 5 | On Kind with the production adapter: a runtime-level change sends `notifications/tools/list_changed` on the session's open connection through the coordinating replica and clears the adapter's list for the session, a new session bound to a process that has listed receives the notification and a fresh list, and a list that did not change is answered inside the pod. |
| 7a | Concurrent `lenny/delegate_task` calls racing a runtime write each see one value per call, under `-race`. Concurrent `tools/list` requests racing the notification RPC never return the list from before the change after the RPC has been handled. |
| 9 | A runtime calling `lenny/delegate_task` after it left its list is refused. A runtime cannot reach `lenny/create_session` from its pod's platform MCP server (DAR-8, with TS-3). A child created with `lease_slice.maxChildrenTotal: 0` cannot delegate through any path, including lease extension. A `tenant-admin` credential cannot remove the guardrail. |
| 11 | The operator page's examples match the resolution table, and the error catalog entry matches the envelope. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Whether Claude Code and Codex drop a tool that a `notifications/tools/list_changed` removed, and whether the model stops calling it | The CA-9 spike measured only an added tool, and "DA-5" relies on removal for the convenience half of the design. | Open |
| How long a runtime process outlives a session on a pod, and whether common runtimes cache `tools/list` across sessions | It sizes the stale-list window that the bind notification closes. | Open |
| That a per-session `tools/list` behind one pod-wide socket conforms to `2026-07-28`'s "MUST NOT vary per-connection" | "DA-5" argues that the list varies by the calling session and over time. The MP-5 proposal confirms it against the schema and the conformance suite. | Open |
| The number of running sessions per tenant at the largest expected deployment | It sizes the fan-out and the default rate. | Open |
| Whether the tenant default is changed through `PUT /v1/admin/tenants/{id}` alone, or a sub-resource fits, as `elicitation-content-integrity` does | "DA-1" proposes a sub-resource. | Open, for the proposal |
| Whether the per-tenant runtime record can hold `allowDelegation` beside its capability axes, or a sibling record keyed the same way fits better | "DA-1" places D1's entry on the existing record. The record's store and RV-1's narrowing rules are written for capability axes. | Open, for the proposal |
| How a dual-era intra-pod server answers `server/discover` and `initialize` on one socket | "DA-5" declares `listChanged` in both. MP-5 owns the dual-era behaviour. | Open, for MP-5 |

## Defects found while verifying

Revision 2 files these in `BUILD-GAPS.md`, at the owner's request of 2026-11-09, under the ID in the "BUILD-GAPS" column:

| Defect | Evidence | BUILD-GAPS |
| :-- | :-- | :-- |
| `lenny/delegate_task` does not evaluate the calling session's `DelegationPolicy` rules against the target, so a policy's allow list does not limit delegation, against §8.3 "Dynamic tag evaluation at delegation time". The audit note 8.3-H1 records it with no checkbox or F-id. | "Current state", `DelegationPolicy` paragraph. | F-8.3.14 |
| `approvalMode: deny` refuses the caller's own call and is never stored on a lease, against §8.4 and the lease example of §8.3. | "Current state", `approvalMode` paragraph. D4 removes the argument, which closes the first half ("DA-6"). | F-8.4.4 |
| A runtime can call `lenny/create_session` and the other client-facing tools from its pod, because the platform MCP server lists and forwards the whole `/mcp` catalog. | "Current state", "How the tool list is built". TS-3 closes it. | F-9.1.13, filed before this design. |
| The adapter reports a forwarded unknown tool as JSON-RPC `-32603`, where MCP specifies `-32602`. | "DA-4 Tool list". | F-15.4.8 |
| §8.5 uses "delegation-capable pod", which the specification does not define. | §8.5 opening sentence. | Not filed. The owner's request of 2026-11-09 did not name it. |

## Instructions for reviewers of other designs

- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26): its reviewer states that TS-3's refusal reads the availability of this design together with the stored lease, adds `details.disabledBy` to `DELEGATION_DISABLED`, extends the "Hiding the tool" table to `lenny/discover_agents` and to `lenny/await_children` and `lenny/cancel_child` for a session with no child, adds `delegationLease.maxDepth` narrowing to the derive request, and records that an explicit `lease_slice.maxChildrenTotal: 0` from a parent leaves the child with no room and the `disabledBy: session` refusal.
- [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions): its reviewer adds `allowDelegation` to the Live row of "Field classes", with S3's ground, notes that delegation settings, like `delegationPolicyRef`, are read at each call of a running session, and adds `allowDelegation: false` to "Per-tenant capability override" as a narrowing that refuses `true`.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#fields): its reviewer adds an `allowDelegation` row to "Fields" (restrict-only, `false` always allowed, `true` only over a base that is not `false`), states that a tenant-derived runtime with `false` is how a tenant runs workers that do not delegate, and adds the tenant default, the tenant runtime entry field, and the runtime field to "Configuration by scope". Owned pools and "Template copy" gain no delegation field.
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-8-runtime-capability-and-late-tools): its reviewer generalizes `NotifyConnectorToolsChanged` to carry the server, the platform server or a connector, and states that the platform server declares `listChanged` without reference to `deferredToolDiscovery`.
- [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md#mp-5-intra-pod-servers): its reviewer adds `server/discover` with the `tools.listChanged` capability and `subscriptions/listen` with `toolsListChanged` to MP-5's intra-pod servers, and the cache hints of "DA-5" to the platform server's `tools/list`.
- [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture): its reviewer adds `"delegation": "forbid"` to the SO-2 record, lists its removal among the weakenings of "Tightening and weakening" and its setting among the tightenings, states that the gateway's delegation gate reads the active version, and includes the record's activation among the events that start this design's fan-out. Its reviewer also states that an SO-5 approval hold on `lenny/delegate_task` runs after this design's refusal, and that a `tenant-admin` cannot write the guardrail.
- [`runtime-routing-design.md`](runtime-routing-design.md#post-chain-point): no change. Its reviewer confirms that the post-chain point needs no availability check, because a child's availability reads the final row at the child's own calls and depends on no pool.

## Proposal split and order

1. **DA-A**, after TS-3's proposal, whose calling-session parent, allow-list, `DELEGATION_DISABLED`, and stored `maxDepth` it extends: the specification edits; the levels, the resolver, and the refusal; the per-session list; the derived runtime rule and the tenant runtime entry; the derive narrowing; the explicit-zero per-hop switch and the extension rule; and the removal of the `approvalMode` argument and `DELEGATION_DENIED` with the proto reservation. The guardrail row of the resolution lands with or after the SO-2 proposal of [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture), which adds the record.
2. **DA-B**, after CA-8's adapter notification work and MP-5: `listChanged` in both declarations, the per-session list in the adapter, the bind notification, the generalized RPC, the fan-out, and the cache hints.

The defect that `lenny/delegate_task` does not apply `DelegationPolicy` rules (F-8.3.14) is independent of both and takes its own proposal.

## Sources

- MCP `2025-11-25` tools, including the `listChanged` capability, the list changed notification, and unknown-tool errors: https://modelcontextprotocol.io/specification/2025-11-25/server/tools
- MCP `2026-07-28` tools, including the rule on per-connection variance, the deterministic order and prompt-cache statement, and delivery through `subscriptions/listen`: https://modelcontextprotocol.io/specification/2026-07-28/server/tools, read again on 2026-11-09 for revision 3.
- MCP `2026-07-28` caching, `ttlMs` and `cacheScope`: https://modelcontextprotocol.io/specification/2026-07-28/server/utilities/caching
- MCP `2026-07-28` discovery, the `server/discover` request and its `capabilities`: https://modelcontextprotocol.io/specification/2026-07-28/server/discover, read 2026-11-09.
- MCP `2026-07-28` versioning and compatibility, the absence of an `initialize` handshake for modern clients and dual-era servers: https://modelcontextprotocol.io/specification/2026-07-28/basic/lifecycle, read 2026-11-09.
- Amazon Web Services, "Prompt caching for faster model inference", https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html, read 2026-11-09, which states that cache checkpoints are processed in the order tools, system, and messages, and that modifying the tools invalidates the caches of the later sections.

## Review history

An adversarial review read revision 2 on 2026-11-09. Revision 3 applies its fixes and the owner's answers of the same date.

### Revision notes

- Revision 1, 2026-11-09: first draft, written from the owner's request for delegation configurable per runtime or pool with platform and tenant defaults, and from the owner's later thought that sessions should be able to disable delegation, which O5 answers by reusing decision 26 of [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26).
- Revision 2, 2026-11-09: files the defects found while writing this design in `BUILD-GAPS.md`, at the owner's request of 2026-11-09, as F-8.3.14, F-8.4.4, and F-15.4.8. The pod's reach to `lenny/create_session` was already filed as F-9.1.13, so it gains that ID and no new finding.
- Revision 3, 2026-11-09: applies the owner's answers of 2026-11-09 and the adversarial review of revision 2. The owner's answers, as recorded: "D1 settled: a tenant can narrow one platform runtime through a narrow-only per-tenant entry." On D2, in the owner's words: "I agree with you. As long as it can be disabled in derived runtimes, we're good." "D3 settled: no mid-session disable." "D4 settled: the per-hop switch is `lease_slice.maxChildrenTotal: 0`. `approvalMode` stops being a `delegate_task` argument." "D5 settled: the tenant hard switch becomes a security-oversight SO-2 guardrail `delegation: "forbid"`, set by `security-admin`. Tenant admins write only `delegation.allowByDefault`. `delegation.forbid` and `forbidSetBy` are dropped." In response, the tenant runtime entry joins the levels and the resolution; the pool level, its owned-pool rules, its tests, and its reviewer instructions go, and "Workers beside orchestrators" states the derived-runtime pattern; the session level is fixed at creation; DA-6 uses an explicit `maxChildrenTotal: 0`, distinguishes it from an absent field, protects it from extension, and removes the `approvalMode` argument and `DELEGATION_DENIED`; and the tenant forbid moves to the SO-2 record. O2, O6, and O8 of revision 2 become D2, D4, and D5, O5 narrows to derive and resume, and O1, O3, O4, O5, and O7 stay open with their recommendations. The review's fixes: DA-2 and DA-6 now give one refusal for a child that cannot delegate; `DELEGATION_DENIED` is recorded as living only in code and in the proto, the §15.1 row no longer removes it from the specification, and the proto reserves number 20; the adapter keeps one tool list per session, cleared by the CA-8 RPC and on bind, and answers with `ttlMs: 0`; "Limits" states the prompt-cache cost of a list change and the public-API path around DAR-8; S3 rests on who writes the field; under MCP `2026-07-28`, `listChanged` is declared in the `server/discover` result; removing the guardrail takes the SO-2 weakening delay, and widening and narrowing writes emit distinct audit events; and "Limits" and DA-1 state that a Helm forbid is not a switch for an incident.
