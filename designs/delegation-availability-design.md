# Delegation availability

Status: design for owner review, revision 4, 2026-11-10, after one adversarial review. No decision is open: the owner settled O1, O3, O4, O5, and O7 on 2026-11-10. Nothing here is built or staged. It reads against [`message-delivery-design.md`](message-delivery-design.md) revision 8, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`runtime-routing-design.md`](runtime-routing-design.md) revision 12, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 10, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 65, [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12, [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md) revision 2, [`security-oversight-design.md`](security-oversight-design.md) revision 47, and [`session-derive-design.md`](session-derive-design.md) revision 16. "Decisions" lists what is settled. It stages no specification edit. The changes go through `change-proposal` after review. Proposals carry the label DA-n, and requirements carry the label DAR-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-09. The claims revision 4 adds or relies on were read again on 2026-11-10. |
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
- A per-session platform tool list that omits `lenny/delegate_task` and the tools that depend on it when the session cannot delegate, and omits each other tool whose call the session's settings guarantee to be refused or to have no effect ("DA-4", O7).
- A runtime declaration, `capabilities.harnessApprovalRequests: true`, without which the platform tool list omits `lenny/request_approval` ("DA-4", O7, and "MD-19" of [`message-delivery-design.md`](message-delivery-design.md#md-19-harness-approvals)).
- Live reads of the settings at each `lenny/delegate_task` call and each platform tool list, with no copy of the list in the adapter ("DA-5", O4).
- A per-hop rule for a parent that wants its child unable to delegate: `lease_slice.maxChildrenTotal: 0` (D4, "DA-6").
- Audit events that distinguish a change which widens availability from one which narrows it ("DA-1").

**Changed:**

- §8.5's statement that the delegation tools are "available on the platform MCP server for every delegation-capable pod" becomes the per-session list of "DA-4".
- An explicit `maxChildrenTotal: 0` in a lease slice means that the child starts no child, and lease extension never raises it ("DA-6").
- `Bridge.ListPlatformTools`, which ignores the session ID today, filters the list for the calling session ("DA-4").

**Removed:**

- `approvalMode` as an argument of `lenny/delegate_task`, with `ErrDelegationDenied`, the `DELEGATION_DENIED` code, and `ERROR_CODE_DELEGATION_DENIED` in the adapter proto, whose number is reserved (D4, "DA-6"). The lease field of §8.4 stays.

**Out of scope:**

- A pool level. The owner dropped it (D2).
- Per-tool switches for the other platform tools. "DA-7" reviews each tool and finds an existing control for each, and O7 records the owner's answer.
- A notification to a running runtime when its platform tool list changes. The owner chose live reads with no notification (O4), and "DA-5" states how the notification can be added later.
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
| Platform tool list | The answer to `tools/list` on the platform MCP server inside the pod, for the session the adapter resolves as the caller. The gateway computes it at each `tools/list` ("DA-4" and "DA-5"). |
| Inapplicable tool | A platform tool whose call the session's current settings guarantee to be refused or to have no effect, such as `lenny/request_input` on a session created on a provider surface. "DA-4" lists each one, and the platform tool list leaves it out so that its description costs the agent no context (O7). |
| Stale list | A platform tool list that a runtime keeps from a `tools/list` made before a setting changed. The runtime holds it until it lists again ("DA-5"). |
| `capabilities.harnessApprovalRequests` | The Runtime capability this design adds, with "MD-19" of [`message-delivery-design.md`](message-delivery-design.md#md-19-harness-approvals), stating that the runtime forwards its harness's own permission requests through `lenny/request_approval`. It is `true` or unset, versioned and gateway-read. It is distinct from the `harnessApprovals` default that the same design puts on the runtime revision, which states the policy for unattended sessions. |
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
| Provider surface | The gateway's Chat Completions, Responses, and Messages API endpoints, where a request is one turn. A session created through one of them is a session created on a provider surface ([`message-delivery-design.md`](message-delivery-design.md#md-8-provider-surfaces), "MD-8"). |
| `lenny/request_approval` and harness approval | A harness approval is a permission request that a coding harness raises for a tool it runs itself. A runtime forwards it to the gateway's tool-use approval path with the platform tool `lenny/request_approval`, which "MD-19" of [`message-delivery-design.md`](message-delivery-design.md#md-19-harness-approvals) adds. |
| `elicitationDepthPolicy` and the pool's allow list | The per-pool setting of [§9.2](../spec/09_mcp-integration.md#92-elicitation-chain) "Deep elicitation suppression" that suppresses agent-initiated elicitations by delegation depth (`allow_all`, `suppress_at_depth: N`, or `block_all`), and the pool's list of elicitation types exempt from suppression. A suppressed elicitation returns `SUPPRESSED` to the pod. |
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
| MD-8, MD-19, and A6 | [`message-delivery-design.md`](message-delivery-design.md#md-8-provider-surfaces) | MD-8 states that elicitation and `lenny/request_input` stay refused on a session created on a provider surface. [MD-19](message-delivery-design.md#md-19-harness-approvals) adds `lenny/request_approval`. Decision A6 removes `lenny/output` from the platform tool set, so the stdout `output` frame is the only output path. |
| SO-2 and SO-5 | [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture) | SO-2 is the tenant security record of guardrails that a `security-admin` writes, with its weakening delay. [SO-5](security-oversight-design.md#so-5-delegated-approvers) holds a tool call that matches a rule until a `security-approver` approves it. |

## High-level design

Delegation stays a platform primitive, and an operator can now turn it off where it does not belong. The platform sets a default and can forbid delegation outright. A tenant can replace the platform default for its own sessions and can turn delegation off for one runtime in the tenant. A tenant's security team can forbid delegation for the whole tenant through its security record, and lifting that forbid waits out the record's weakening delay. A runtime can state that its sessions delegate or do not, which overrides both defaults, and a derived runtime can turn delegation off for the sessions it serves, which covers workers that run beside an orchestrator of the same base runtime. A client creating a session can turn it off for that session and its tree, as decision 26 already settled, and a parent can turn it off for one child. The forbids, a tenant runtime entry, a base runtime's `false`, and the session's and parent's choices can only take delegation away.

The gateway decides, on every `lenny/delegate_task` call, whether the calling session may delegate, from the session's row and the live settings, and refuses with `403 DELEGATION_DISABLED` when it may not. The refusal names the level responsible. The platform MCP server inside the pod lists only the tools the session can use, so a session that may not delegate is offered no `lenny/delegate_task` and no tool that only serves delegation, and no session is offered a tool whose call its settings guarantee to fail or to have no effect. A tool left out of the list costs the agent no context tokens and cannot be chosen. The gateway computes the list from the current settings at each `tools/list`. A runtime that listed before a setting changed keeps its list until it lists again, and the refusal covers a stale entry.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Levels (DA-1).** `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` in Helm, `delegation.allowByDefault` on the tenant, the delegation guardrail in the tenant security record, `allowDelegation: false` on a tenant runtime entry, and `allowDelegation` on the Runtime with a restrict-only rule for derived runtimes.

**Resolution (DA-2).** A forbid wins, then a tenant runtime entry, then the runtime's value, then the tenant default, then the platform default. The session's room applies on top. The settings are live: the gateway reads them at each call.

**Refusal (DA-3).** `403 DELEGATION_DISABLED`, which TS-3 introduces, gains `details.disabledBy`. The gateway checks at the top of the `lenny/delegate_task` handler and again in the delegation service.

**Tool list (DA-4).** The gateway answers `ListPlatformTools` for the calling session. It omits `lenny/delegate_task`, `lenny/discover_agents`, and `lenny/set_tracing_context` when the session may not delegate, and omits `lenny/await_children` and `lenny/cancel_child` as well when the session has no child. It omits `lenny/request_input` and `lenny/request_elicitation` on a session created on a provider surface, `lenny/request_elicitation` where §9.2 suppresses every elicitation of the session, and `lenny/request_approval` for a runtime that does not declare `capabilities.harnessApprovalRequests`.

**Running sessions (DA-5).** The `lenny/delegate_task` handler reads the current settings at each call, and `ListPlatformTools` computes the list from the current settings at each list. The adapter keeps no copy, and nothing notifies the runtime of a change.

**Per-hop narrowing (DA-6).** A parent that passes `lease_slice.maxChildrenTotal: 0` on `lenny/delegate_task` creates a child with no room, so the child cannot delegate.

**Other tools (DA-7).** Each other platform tool already has a control of its own, so no per-tool switch is added (O7). The list rules of "DA-4" read those controls.

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

"Limits" states what the change costs: a runtime keeps a stale list until it lists again, a list change costs the runtime a provider prompt-cache write, a platform Helm change reaches running processes only after a gateway restart, and a pod that can reach the gateway's public API with a client credential can still create a root session.

## Decisions

Settled by an earlier owner decision or by a project rule:

| ID | Decision |
| :-- | :-- |
| S1 | A client disables delegation for one session and its tree with `delegationLease.maxDepth: 0` at creation. The owner settled it on 2026-11-03 as decision 26 of [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26), in the words "yes, add it with maxDepth: 0". O5 settles what decision 26 and D3 left open. |
| S2 | The refusal is the enforcement. Leaving an unavailable tool out of the platform tool list keeps its description out of the agent's context on every model call and keeps the agent from choosing a tool that will be refused. TS-3 states the first half in its words "Hiding is a convenience, and the refusal is the control", which this design keeps as the history of the rule. The owner stated the stronger reason on 2026-11-10: "It's more than a convenience. Tools cost context tokens and can confuse the agent." This design applies the rule at every level and to the inapplicable tools of "DA-4". |
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

Settled by the owner on 2026-11-10. These decisions were open in revision 3 and keep their IDs, which the rest of this document cites:

| ID | Question | Owner's answer and what it settles |
| :-- | :-- | :-- |
| O1 | How the levels combine. (A) Restrict-only at every level, so any `false` wins and a platform "default" of off can never be turned on below it. (B) Defaults resolved by specificity, a runtime overriding the tenant default and the tenant default overriding the platform default, with forbids that nothing below lifts, and restrict-only between a runtime, its base, and a tenant runtime entry. (C) As (B) without forbids. | "ok" to (B). The more specific level wins among the defaults, and the platform forbid in Helm and the tenant forbid, which is the delegation guardrail (D5), override every other level, as the resolution table of "DA-2" states. The tenant runtime entry (D1) is the tenant's narrowing of a single runtime. The owner asked for a platform and a tenant *default*, which a more specific level must be able to override, and (A) gives none. Without a forbid, a tenant that wants no delegation cannot stop a platform runtime that sets `allowDelegation: true`, so (C) leaves the tenant without a hard switch. (B) follows the layering §8.6 uses for lease extension: defaults by specificity, capped by ceilings. |
| O3 | The platform default. | "ok" to `gateway.delegation.allowByDefault: true`. §8.1 describes delegation as a platform primitive that every agent runtime can use, a deployment that sets nothing behaves as today, and the depth, fan-out, budget, and tree-size limits of §8.3 still bound every tree. A deployer who wants delegation off by default sets the value to `false`. |
| O4 | How a change reaches a running session. Revision 3 offered (A) live reads in both directions with a notification to the runtime, (B) a snapshot at creation, and (C) live for a change that disables and a snapshot for one that enables, and recommended (A). | "ok" to option (D), a new option. The owner asked for "whichever means the smallest blast radius from an implementation point of view". The comparison rests on the tree: the adapter caches nothing today, and each `tools/list` is forwarded live to the gateway; `ListPlatformTools` returns one catalog for every session; and no list-change notification exists in the tree ("Current state"). Under (D) the `lenny/delegate_task` handler reads the current settings at each call, and `ListPlatformTools` filters the list per session from the current settings at each list. The design keeps no adapter cache, no notification RPC, no fan-out, no `listChanged` declaration, and no `subscriptions/listen`. A session that listed before a change keeps its list until its runtime lists again, and the refusal covers a stale entry. Most harnesses list once per connection. The notification machinery can be added later without changing (D) ("DA-5"). The session's own choice (S1) stays stored, because it is the client's and nothing lifts it. "Alternatives rejected" records revision 3's (A), (B), and (C). |
| O5 | How derive and resume treat the session level. D3 settles that a session cannot change it mid-session. | "ok". A derive request accepts `delegationLease.maxDepth`, which can only lower the value TS-3 carries over from the source. A resume keeps the stored value ("Session level" under DA-2). |
| O7 | Whether any other platform tool needs a per-runtime switch. | "ok", with an extension. No new per-runtime switch is added: "DA-7" finds an existing control for each tool, some tools leave the surface under other designs, and a deployer who wants a tool held for approval uses an SO-5 rule. The extension: the per-session list filter of "DA-4" also leaves out the inapplicable tools, those whose call the session's existing settings guarantee to fail or to have no effect, so their descriptions cost no context. The refusals remain the enforcement. The extension adds one runtime declaration, `capabilities.harnessApprovalRequests`, which "MD-19" of [`message-delivery-design.md`](message-delivery-design.md#md-19-harness-approvals) states. |

## Requirements

| ID | Requirement |
| :-- | :-- |
| DAR-1 | A session that may not delegate starts no child, whether the call comes from its pod or from a client at the gateway `/mcp` endpoint naming it as parent. |
| DAR-2 | The refusal names the level that made delegation unavailable. |
| DAR-3 | A platform forbid, the delegation guardrail, a tenant runtime entry, a base runtime's `false`, a session's `maxDepth: 0`, and a parent's `lease_slice.maxChildrenTotal: 0` are never lifted by a more specific level, by a later hop, or by lease extension. |
| DAR-4 | The platform tool list of a session that may not delegate omits `lenny/delegate_task` and `lenny/discover_agents`, and also omits `lenny/await_children` and `lenny/cancel_child` when the session has no child. |
| DAR-5 | A change at the tenant, security record, tenant runtime entry, or runtime level governs the next `lenny/delegate_task` call and the next `tools/list` of every running session it reaches. |
| DAR-6 | Making delegation unavailable ends no running child, and a parent with children keeps the tools that manage them. |
| DAR-7 | A deployment that sets none of the new values behaves as today. |
| DAR-8 | A session cannot reach a root-session creation tool from its pod's platform MCP server, so the setting cannot be bypassed by creating a new root through that path. TS-3's allow-list provides this, and this design depends on it. F-9.1.13 records the defect. "Limits" states the path through the gateway's public API, which this requirement does not cover. |
| DAR-9 | Removing the delegation guardrail takes effect only after the SO-2 weakening delay, and every write that widens or narrows availability at any level writes an audit event that names its direction. |
| DAR-10 | The platform tool list of a session omits each inapplicable tool under the rules of "DA-4", and a call to an omitted tool is answered by that tool's own handler, as a call to a listed tool is (O7). |

## Current state

**No availability setting [source].** §5.1 gives the Runtime `delegationPolicyRef` and `allowSelfRecursion` (`pkg/apis/lenny/v1alpha1/runtime_types.go`, `runtimestore.Runtime`). §5.2 gives pools no delegation field. The tenant record carries no delegation field, although it carries `messaging.scope` (§7.2) and `leaseExtension` (§8.6). The Helm values that concern delegation are `gateway.cycleDetection.mode`, `gateway.allowSelfRecursion`, and `gateway.delegation.defaultMaxDepth` (§8.3), and the last is not rendered by the chart (F-8.3.12).

**A per-tenant runtime record exists [source].** §5.1 states "Capabilities are customizable per tenant", and the admin API serves `GET`, `PUT`, and `DELETE /v1/admin/tenants/{id}/runtime-capability-overrides/{runtime}` (`pkg/gateway/externalapi/admin/runtime_capability_overrides.go`), whose body holds capability axes such as `interaction` and `sdkWarmBlockingPaths`. RV-1 makes the record a narrowing ("Per-tenant capability override"). It carries no delegation field.

**`maxDepth` cannot be 0 today [source].** §8.2 **Effective `maxDepth` resolution** requires every effective lease to carry a positive integer, and every layer of its chain can only produce one. A client's `delegationLease` at creation is validated, stored, and echoed with no effect (F-8.2.34). TS-3 wires it and admits 0 as decision 26.

**A zero `maxChildrenTotal` in a lease slice means no limit [source].** §8.2's `LeaseSlice` table describes `maxChildrenTotal` as "Max children the child may spawn". The `lenny/delegate_task` input schema carries `leaseSlice.maxChildrenTotal` as an integer, `lease.ValidateChildSlice` checks a child's value only when both the parent's and the child's are greater than 0, and the stored `sessionstore.DelegationLease` documents that "A zero field means 'no limit set at this scope'". A parent that passes 0 today grants its child unlimited children. The session-creation request is different: `sessionstore.DelegationLeaseRequest` holds `maxDepth` and `maxChildrenTotal` as pointers so an explicit zero is distinguishable. §8.6 lists `maxChildrenTotal` among the extendable fields.

**`DelegationPolicy` rules are not applied to `lenny/delegate_task` [source].** `DelegationPolicy.Evaluate` has these production callers: `filterByEffectiveDelegationPolicy` for `lenny/discover_agents` (`pkg/gateway/mcpfabric/mcptools/mcptools.go`), `connectorauthz` for connector calls, and the pool isolation audit. The `lenny/delegate_task` handler (`mcptools_register.go`) checks environment scope through `runtimeAuthorizedForCaller` and `crossEnvReachable`, and `Service.detectCycle` (`pkg/gateway/mcpfabric/delegation/service.go`) reads the *target* runtime's `delegationPolicyRef` for the self-recursion layer and the content policy. No step evaluates the calling session's policy rules against the target. A policy that allows nothing therefore hides every target from discovery and blocks no delegation. The audit note 8.3-H1 in `BUILD-GAPS.md` records the missing call with no checkbox or F-id, and revision 2 files it as F-8.3.14.

**`approvalMode: deny` refuses the caller's own call [source].** §8.4 defines `deny` as "Delegation not permitted" on the lease, and §8.4 states that the `approval` value "is accepted at policy registration time". `lenny/delegate_task` accepts `approvalMode` as an argument, and `validateDelegation` returns `ErrDelegationDenied` (`DELEGATION_DENIED`) when the caller passes `deny`, before it reads the parent. Nothing stores the value on the child's lease, and nothing sets it on a root, so a runtime can only refuse itself (F-8.4.1's resolution). Revision 2 files this as F-8.4.4.

**`DELEGATION_DENIED` lives only in code and in the adapter proto [source].** No specification section and no page under `docs/` names it. The code names it in `errorclassify` (`"DELEGATION_DENIED": {CategoryPolicy, false}`), in the handler of `mcptools_register.go`, in comments of `delegation/service.go`, and in `delegate_approvalmode_test.go`. `schemas/lenny-adapter.proto` declares `ERROR_CODE_DELEGATION_DENIED = 20`, and the generated `pkg/proto/adapter/v1/lenny-adapter.pb.go` carries it.

**How the tool list is built [source].**

- The adapter starts the platform MCP server once per pod (`startPlatformMCP`, `pkg/adapter/platformmcp.go`). Each `tools/list` goes to `platformToolProvider.List`, which resolves the calling session through `callingSession` and forwards `ListPlatformTools` with that session's ID over `GatewayControl` (`pkg/adapter/platformtoolprovider.go`). The adapter keeps no copy: `toolList` in `pkg/adapter/mcp/server.go` calls the provider on every `tools/list`, so each list reaches the gateway.
- The gateway's `Bridge.ListPlatformTools` (`pkg/gateway/gatewaycontrol/platformtools/platformtools.go`) ignores the session ID and returns `Catalog()` of the gateway's `/mcp` server. Its comment states that "the catalog is uniform across type:agent sessions".
- That catalog holds every tool `mcptools.Register` installs: the tools of §9.1's table, `lenny/vcs_token`, and the client-facing tools such as `lenny/create_session`, `lenny/create_and_start_session`, `lenny/list_sessions`, and `lenny/upload_files`. A runtime can call `lenny/create_session` from its pod and start a new root session outside its delegation lease. TS-3 found this and allow-lists the bridge to §9.1's table. `BUILD-GAPS.md` already records it as F-9.1.13, so revision 2 files no new finding for it.
- `lenny/delegate_task` is never refused for being disabled, because nothing disables it.

**The server cannot announce a change [source].** `initializeResponse` in `pkg/adapter/mcp/server.go` declares `"tools": {}` with no `listChanged`, and the serve loop only answers requests. No list-change notification exists in the tree: no Go file under `pkg/`, `cmd/`, or `sdks/` and no proto under `schemas/` names `listChanged` or `list_changed`. The server speaks MCP `2025-03-26`, which MP-5 replaces. CA-8 found the same for connector servers and proposes the adapter changes and a gateway-to-adapter RPC, `NotifyConnectorToolsChanged`, for a runtime that declares `deferredToolDiscovery`.

**A process can outlive a session [source].** The platform MCP server is pod-wide, and `callingSession` resolves the caller per request to the single session the pod's shared runtime process holds. §28.5.3 states that a later session's manifest write does not re-arm the server. TS-3 notes that a recycled or SDK-warm process can hold a list from before its current session.

**What MCP requires [external].** Under `2025-11-25`, a server declares its capabilities in the `initialize` result, and a server that declares `tools.listChanged` "SHOULD" send `notifications/tools/list_changed` when its tools change, after which the client lists again. Under `2026-07-28`, no `initialize` handshake exists for a modern client: a server "MUST implement `server/discover`", whose result carries `capabilities`, and the server sends the notification on a `subscriptions/listen` stream the client opened with `toolsListChanged: true`. A dual-era server may serve both. `2026-07-28` also states that the tool set "MAY change over time" and "MUST NOT vary per-connection", that it "MAY vary by the authorization presented on the request", and that every `tools/list` result carries `ttlMs` and `cacheScope`, where `ttlMs: 0` marks the result stale at once and `cacheScope: "private"` forbids sharing it across authorization contexts. It recommends a deterministic tool order because it "improves LLM prompt cache hit rates when tools are included in model context". Both versions report an unknown tool as JSON-RPC error `-32602`.

**Runtimes and list changes [source, from CA-9].** In the spike of [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#spike-results-for-claude-code), Claude Code 2.1.317 listed again on `notifications/tools/list_changed` and could call a tool added that way. No run tested a tool removed that way, and no Codex run is recorded.

**Precedents for layered settings [source].** §7.2's `messagingScope` resolves deployment, tenant, and runtime as "most-restrictive wins, can only narrow". §8.6's lease extension settings resolve by specificity, runtime over tenant over deployment, capped by a tenant maximum and a deployment maximum.

**Settings that refuse every call of another tool [source].** The list rules of "DA-4" read these, each checked on 2026-11-10:

- [§9.2](../spec/09_mcp-integration.md#92-elicitation-chain) "Deep elicitation suppression" suppresses agent-initiated elicitations at delegation depth 3 and deeper by default, "unless the elicitation type appears in the pool's allow list", and lets a pool set `elicitationDepthPolicy` to `allow_all`, to `suppress_at_depth: N`, which suppresses at depth N and deeper, or to `block_all`, which allows "no elicitations from delegated sessions". A suppressed elicitation returns `SUPPRESSED`. Every call of `lenny/request_elicitation` is therefore suppressed for a delegated session in a pool whose policy is `block_all`, and for a session whose depth is at or beyond the pool's suppression depth when the pool's allow list names no elicitation type. The connector exemption of §9.2 covers gateway-initiated OAuth flows and never a runtime's own call. §9.2's "Depth-based restrictions" limit elicitation types by depth, and no rule there refuses every type.
- "MD-8" of [`message-delivery-design.md`](message-delivery-design.md#md-8-provider-surfaces) states that elicitation and `lenny/request_input` stay refused on the provider surfaces whatever caused the turn **[design]**, which extends AM-6 of the Messages API design to every provider surface.
- [§7.2](../spec/07_session-lifecycle.md#72-interactive-session-model) "Messaging scope" defines the `messagingScope` values `direct`, whose targets are the direct parent and direct children, and `siblings`, which adds the children of the same parent. Each value allows the direct parent and the direct children, so no value allows no recipient.

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

**The guardrail's transitions [design].** The delegation guardrail follows SO-2's rules for every forbid in the record. Setting it is a tightening: it applies in the write's transaction and emits `security.config_tightened`, and each session's next call and next list read it. Removing it is a weakening: it needs a `justification`, creates a pending version, emits `security.config_weakening_requested` at once, and takes effect only at `effectiveAt`, after the tenant's `weakeningDelaySeconds`, when the applier emits `security.config_weakened` and the next call and list of each session read the new version. A `security-admin` can cancel the pending removal. A `tenant-admin` cannot write the record, so a tenant's own administrators cannot lift the guardrail, and a stolen administrator credential cannot lift it inside the delay without the security team seeing the request (D5, DAR-9).

**Switches for an incident.** `gateway.delegation.forbid` takes effect only when the gateway restarts with the new value, so it is a deployment posture and not a switch for an incident. During an incident an operator uses a live level: a `platform-admin` sets `allowDelegation: false` on the runtime involved, which reaches every tenant's sessions of that runtime at their next call, and a tenant's `security-admin` sets the delegation guardrail, which applies in the write's transaction. A `tenant-admin` can also set the tenant default to `false` or write a tenant runtime entry. Each change is enforced at the next call of every running session it reaches, and it reaches a session's list when the session's runtime lists again ("DA-5").

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

**Live reads.** The gateway reads the settings at each `lenny/delegate_task` call and each `ListPlatformTools` call, and the adapter keeps no copy of the list (O4, "DA-5"). The tenant, security record, tenant runtime entry, and runtime rows are read through the caches the gateway already keeps for them, and the platform values are process flags. A change reaches a running session at its next call. This matches §8.3's rule that a delegation policy "is re-evaluated on each `delegate_task` call", and it keeps §8.3's rule that a change "does not affect active child sessions": a child already running continues (DAR-6).

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

The gateway counts refusals in `lenny_delegation_disabled_refusals_total{disabled_by}`. A runtime that calls `lenny/delegate_task` from a stale list appears there, which tells an operator that the runtime has not listed since the change. Each refusal also writes the delegation rejection audit that §8.2 states for a refused delegation, with `DELEGATION_DISABLED` and `disabledBy`.

## DA-4 Tool list

`Bridge.ListPlatformTools` uses the session ID it already receives. At each list it reads the session's row and the current settings, and after TS-3's allow-list to §9.1's table it removes the inapplicable tools by this table (DAR-4, DAR-10). The third column states the answer a call receives when the runtime calls the tool anyway, for example from a stale list. That answer is the enforcement, and the list keeps the tool's description out of the agent's context (S2).

| Tool | Listed when | Answer to a call the list omits |
| :-- | :-- | :-- |
| `lenny/delegate_task` | The session may delegate. | `DELEGATION_DISABLED` or `DELEGATION_DEPTH_EXCEEDED` ("DA-2" and "DA-3"). |
| `lenny/discover_agents` | The session may delegate. | An answer with no targets ("DA-3"). |
| `lenny/await_children` and `lenny/cancel_child` | The session may delegate, or it has at least one child in any state. | The refusal for a session with no child, as today. |
| `lenny/set_tracing_context` | The session may delegate. | Accepted as today. The tool's only effect is on the leases of children the session creates later, so the call has no effect. |
| `lenny/request_input` | The session was not created on a provider surface. | The refusal of "MD-8". |
| `lenny/request_elicitation` | The session was not created on a provider surface, and §9.2 does not suppress every elicitation of the session. §9.2 suppresses every elicitation when the session is delegated and its pool's `elicitationDepthPolicy` is `block_all`, and when the session's depth is at or beyond the pool's suppression depth (3 by default, N under `suppress_at_depth: N`, none under `allow_all`) and the pool's allow list names no elicitation type. | The refusal of "MD-8", or `SUPPRESSED` under §9.2. |
| `lenny/request_approval` | The session's runtime revision declares `capabilities.harnessApprovalRequests: true`. | Handled as "MD-19" states. The declaration governs the list alone. |
| Every other tool on the allow-list | Always, as "DA-7" states. | The tool's own answer. |

**`lenny/send_message`.** The list has no rule for it. Each `messagingScope` value of §7.2, `direct` and `siblings`, allows the direct parent and the direct children, so no value guarantees that a call is refused ("Current state").

**What changes the list.** The list of a running session changes when "may delegate" changes or when its pool's `elicitationDepthPolicy` or allow list changes. The session's depth, the surface it was created on, and its runtime revision's declaration are fixed for the session. "Has a child" becomes true only through a delegation, which requires "may delegate", and stays true, so a session that loses delegation keeps the tools that manage the children it has (DAR-6). The list keeps a fixed tool order, as `2026-07-28` recommends.

`Bridge.CallPlatformTool` does not check the list. A call to a tool the session's list omits reaches the handler, which answers `DELEGATION_DISABLED` for `lenny/delegate_task` and the existing answers for the others. A call to a name outside TS-3's allow-list answers `ErrPlatformToolNotFound`. Today `callTool` in `pkg/adapter/mcp/server.go` reports any forwarded failure as JSON-RPC `-32603` **[source]** (F-15.4.8), and this design has the adapter report the not-found status as `-32602`, the MCP error for an unknown tool **[external]**.

The gateway `/mcp` endpoint keeps one catalog for every client, as TS-3 states, because a connection names no session until a call does. The refusal covers it.

**`type: mcp` runtimes and runtimes that use MCP.** A `type: mcp` runtime is an MCP server the gateway calls, and it has no platform MCP server and no delegation, so nothing in this design reaches it beyond the refused writes in DA-1. An agent runtime whose binary is an MCP client connected to several servers, such as Claude Code with the platform server, connector servers, and servers of its own, sees a change to the platform server's list when it lists that server again, and a client that prefixes tool names by server, as Claude Code did in the CA-9 spike, keeps the prefix on the platform tools. The virtual child interface of §8.2 step 8 exists only for children, so a session that cannot delegate gets none, and a session that loses delegation keeps the interfaces of its existing children.

## DA-5 Running sessions

The owner chose option (D) on 2026-11-10 (O4) **[design]**.

**Reads at each call and each list.** The `lenny/delegate_task` handler reads the current settings at each call ("DA-2" and "DA-3"). `ListPlatformTools` computes the list for the calling session from the current settings at each list ("DA-4"). The adapter forwards each `tools/list` to the gateway, as it does today, and keeps no copy of the list.

**No notification.** The platform MCP server declares `"tools": {}` with no `listChanged`, as it does today, and sends no `notifications/tools/list_changed`. Under `2026-07-28` this design needs no `subscriptions/listen` on the intra-pod server. The gateway sends the adapter no message when a setting changes, and no write path enumerates running sessions.

**Stale lists.** A session that listed before a change keeps its list until its runtime lists again. Most harnesses list once per connection, so a running process commonly keeps its list until it connects to the platform server again ("Validation owed"). A stale list that still offers `lenny/delegate_task` after delegation became unavailable leads to the refusal of "DA-3", which is the enforcement (S2). A stale list that lacks a tool that became available offers the tool only after the runtime lists again. A runtime process that outlives its session and serves the next session on the same pod holds the previous session's list until it lists again, as TS-3 notes, and the refusals cover that list as well.

**Cache hints.** Under `2026-07-28` every `tools/list` result carries `ttlMs` and `cacheScope` **[external]**. The platform server answers with `ttlMs: 0` and `cacheScope: "private"`, so a client that honours the hints treats the list as stale at once and never shares it across sessions. Each list the client makes reaches the gateway.

**Blast radius.** Option (D) changes the `lenny/delegate_task` handler and `Bridge.ListPlatformTools` in the gateway. The adapter's list path stays as it is apart from the `-32602` answer of "DA-4" and the cache hints that every `2026-07-28` result carries under MP-5. Revision 3's option (A) also needed a per-session list in the adapter, a gateway-to-adapter RPC, a fan-out from every admin write path and from the SO-2 applier, a rate flag, a counter, two `listChanged` declarations, and `subscriptions/listen` ("Alternatives rejected").

**Adding a notification later.** A list-change notification can be added later without changing option (D). The reads at each call and each list stay as they are, and a notification would only prompt a runtime that honours it to list again sooner. The refusals remain the enforcement either way.

**MCP versions.** Under MP-5 the intra-pod servers serve `2025-11-25` and `2026-07-28`. The list varies by the session the adapter resolves for each request and changes over time as settings and sessions change, which `2026-07-28` permits, and it does not vary by connection **[external]**.

## DA-6 Per-hop narrowing

**The switch (D4) [design].** A parent that passes `lease_slice.maxChildrenTotal: 0` on `lenny/delegate_task` creates the child with no room. The child's stored lease records `maxChildrenTotal` as an explicit 0, the child cannot delegate, its list omits the delegation tools, and its refusal names `disabledBy: session` under the rule of DA-2. A grandchild cannot exist, so no inheritance rule is needed below it. The field already means "Max children the child may spawn" in §8.2, so 0 reads as its plain meaning.

**Explicit zero.** The tool's input distinguishes an explicit 0 from an absent field, as `DelegationLeaseRequest` already does on session creation, and the stored lease of the child distinguishes them as well. An absent field keeps today's meaning, no limit set at this hop. `lease.ValidateChildSlice` admits an explicit 0 against any parent, because it narrows. This changes the meaning of an explicit 0 in a slice, which today means no limit (S4).

**Extension.** §8.6 lists `maxChildrenTotal` as extendable. An extension never raises a `maxChildrenTotal` that is an explicit 0, because the 0 records the parent's decision that the child is not to delegate, which an exhausted budget does not.

**Removal of the argument.** `lenny/delegate_task` stops accepting `approvalMode`. `ErrDelegationDenied` and the `DELEGATION_DENIED` code go from `validateDelegation`, the handler of `mcptools_register.go`, `errorclassify`, and `delegate_approvalmode_test.go`, because no caller refuses itself any longer. `schemas/lenny-adapter.proto` drops `ERROR_CODE_DELEGATION_DENIED = 20` and declares `reserved 20;` and `reserved "ERROR_CODE_DELEGATION_DENIED";` in the `ErrorCode` enum, so neither the number nor the name is reused, and the generated code is regenerated. No specification section changes for the code, because none names it. §8.4's `approvalMode` stays a lease field set at policy registration, so the lease example of §8.3 is unchanged. The removal closes the self-refusal half of F-8.4.4. The other half, that no path stores `deny` on a lease, stays with F-8.4.4.

## DA-7 Other platform tools

The owner asked whether other platform tools need a per-runtime or per-pool switch, and answered "ok" to adding none (O7). This table reviews each tool on the allow-list TS-3 sets, which is §9.1's table plus `lenny/vcs_token`, with the changes [`message-delivery-design.md`](message-delivery-design.md#md-19-harness-approvals) makes to it. The fourth column names the list rule of "DA-4" that reads the tool's existing control.

| Tool | What it does | Existing control | Omitted from the list ("DA-4") | Switch warranted |
| :-- | :-- | :-- | :-- | :-- |
| `lenny/output` | Emits output parts to the parent or client. | Message delivery decision A6 removes the tool, and the stdout `output` frame becomes the only output path. | Not applicable. | No, it leaves |
| `lenny/request_input` | Blocks until the client or parent answers. | `capabilities.interaction` (`one_shot` allows one call, `multi_turn` many), and `maxRequestInputWaitSeconds` (§5.1). "MD-8" refuses it on a session created on a provider surface. | On a session created on a provider surface. | No |
| `lenny/request_elicitation` | Asks the human through the elicitation chain. | The per-pool allow list, deep elicitation suppression at delegation depth 3 and deeper, `elicitationDepthPolicy`, and `maxElicitationWait` (§9.2). "MD-8" refuses it on a session created on a provider surface. | On a session created on a provider surface, and where §9.2 suppresses every elicitation of the session. | No |
| `lenny/request_approval` | Forwards a harness approval to the tool-use approval path and returns the decision. | "MD-19" adds the tool, with the approval timeout of SO-5a and the `harnessApprovals` policy for unattended sessions. | For a runtime revision that does not declare `capabilities.harnessApprovalRequests: true`. The declaration states what the binary does, and an operator does not set it per deployment. | No |
| `lenny/send_message` | Sends a message to a task in the tree. | `messagingScope` from deployment, tenant, and runtime, and `messagingRateLimit` on the lease (§7.2, §8.3). | Never. No `messagingScope` value allows no recipient. | No |
| `lenny/get_task_tree` | Returns the visible part of the task tree. | `treeVisibility` on the lease (§8.3). | Never. | No |
| `lenny/set_tracing_context` | Registers tracing identifiers that the gateway attaches to children's leases. | Validation rules of §8.3. The same function is open through the `set_tracing_context` JSONL message. | When the session may not delegate, because its only effect is on children. | No |
| `lenny/memory_write` and `lenny/memory_query` | Built-in memory. | CA-10 removes the tools and the feature. | Not applicable. | No, they leave |
| `lenny/vcs_token` | The pod's git credential helper fetches a token. | The VCS pool's host patterns and user authorization. [`egress-and-git-design.md`](egress-and-git-design.md#proposal-split) EG-3 removes the tool. | Not applicable. | No, it leaves |
| `lenny/delegate_task`, `lenny/discover_agents`, `lenny/await_children`, and `lenny/cancel_child` | Delegation and its dependent tools. | This design. | Under the rules of "DA-4". | Covered by DA-1 to DA-4 |

A deployer who needs a human to approve a particular tool call has SO-5's approval rules, which match a tool by name and hold the call at the gateway. A general per-runtime tool allow-list was considered and rejected under "Alternatives rejected".

## Limits

- **A runtime keeps a stale list until it lists again.** No notification tells a runtime that its list changed (O4). A runtime that listed before delegation became unavailable is still offered `lenny/delegate_task`, and its call is refused, which the refusal counter of "DA-3" shows. A runtime that listed before delegation became available is not offered the tool until it lists again. Most harnesses list once per connection, so the window commonly lasts until the runtime connects to the platform server again. The same holds for every list rule of "DA-4" that reads a live setting, such as a pool's `elicitationDepthPolicy`.
- **A process that serves a new session keeps the previous session's list.** A runtime process that outlives its session on a pod, such as a recycled or SDK-warm process, holds the list it made for the previous session until it lists again. The refusals answer for the new session.
- **A list change costs a prompt-cache write.** A runtime that lists again sends its model a changed set of tool definitions. Providers cache a prompt prefix in the order tools, system, and messages, and a change to the tools invalidates the cached prefix after it **[external]**, so the runtime's next model call after a change pays the provider's cache-write price for its whole prompt. A change at a tenant or runtime level reaches every running session of that tenant or runtime, so it costs one such write per session that lists again. The fixed tool order of "DA-4" keeps a list that did not change from costing one.
- **The model is not told of the change.** A runtime that keeps a stale list gives its model no sign of the change. A model that planned to delegate learns of the change from the refusal text.
- **A Helm forbid is not a switch for an incident.** `gateway.delegation.forbid` and `gateway.delegation.allowByDefault` take effect when the gateway restarts with them, so a process meets them at its next call after the restart and sees them in its list when it lists again. "Switches for an incident" under DA-1 names the live levels.
- **Lifting the guardrail waits out the delay.** A tenant that set the delegation guardrail during an incident regains delegation only after the SO-2 weakening delay, which is the protection D5 asks for.
- **The gateway `/mcp` catalog still lists the tool.** A client at the gateway sees `lenny/delegate_task` and meets the refusal.
- **A disabled level does not stop a running child.** A child created before the change runs to completion, and its own next call reads the new value.
- **The public API is a path around the setting.** DAR-8 closes the pod's path to root-session creation through the platform MCP server. A pod whose egress reaches the gateway's public API and which holds a client credential, such as a user's bearer passed into the workspace, can call `POST /v1/sessions` and start a root session outside its delegation lease and outside this setting. The egress policy of the pod and the handling of client credentials govern that path, and this design adds no check to it.
- **An explicit zero changes meaning.** A runtime written against the current code, which treats `leaseSlice.maxChildrenTotal: 0` as no limit, now creates a child that cannot delegate.

## Alternatives rejected

- **Express "disabled" as a `DelegationPolicy` that allows nothing.** The policy decides targets, it has no default at the tenant or the platform, the gateway does not apply its rules to `lenny/delegate_task` today, and "allows nothing" can only be known by evaluating every runtime's labels, so the tool list could not follow it.
- **Use `maxDepth: 0` in the Runtime's default lease or in a preset.** §8.2's item 3 and item 2 would carry it, and TS-3's minimum would make it restrict-only. Neither layer exists in code, a minimum gives no default that a lower level can override, and it ties availability to the depth budget, so a refusal could not name the level.
- **Make the levels restrict-only (O1 option A).** A platform or tenant default of `false` could never be overridden, so it would be a forbid under another name.
- **Notify the runtime of each change (revision 3's O4 option A).** Revision 3 recommended live reads with a best-effort notification. The platform MCP server declared `tools.listChanged` in the `2025-11-25` `initialize` result and in the `2026-07-28` `server/discover` result, and implemented `subscriptions/listen` for `2026-07-28` clients. The adapter kept one list per session and cleared it on a notification RPC from the gateway, which generalized CA-8's `NotifyConnectorToolsChanged`, and on a new session's bind. Each admin write and each activation of a security record version that changed a value the resolution reads made the gateway enumerate the running sessions it reached and send the RPC for each through the session's coordinating replica, rate limited by `--delegation-list-change-rate` and counted in `lenny_platform_tools_list_changed_total{outcome}`. The owner asked for the smallest blast radius, and option (D) has it: the adapter caches nothing today, `ListPlatformTools` already receives the session ID, and no notification exists in the tree, so (D) changes two gateway functions where (A) added an adapter cache, an RPC, a fan-out from every write path, a flag, a counter, and two protocol features. The refusal enforces the setting under both options, and most harnesses list once per connection, so the notification would shorten the stale window only for a runtime that honours it. The notification can be added later without changing (D) (O4).
- **Snapshot availability at creation (O4 option B).** The gateway would resolve each session's availability at creation, store it on the session row, and read the stored value at each call and list. It changes the session store and every creation path, including delegation, derive, and the provider surfaces, as well as the two gateway functions that (D) changes, so it is larger than (D). It also loses the incident switch: a runtime turned off during an incident would keep delegating for the life of its sessions.
- **Live for a change that disables, snapshot for one that enables (O4 option C).** It needs the stored snapshot of (B) beside the live read of (D), for no gain the owner asked for.
- **A pool level.** Revision 2 proposed `allowDelegation: false` on a pool. A `PreRoute` interceptor or a runtime route can move a session to another pool of the same runtime, so a pool setting would make availability depend on a placement the client and the runtime author do not see, and the pool level added an owned-pool copy rule and a resolution step. A derived runtime with `allowDelegation: false` serves the same case on the name the caller selects. The owner dropped the pool level (D2).
- **`approvalMode: deny` as the per-hop switch.** Revision 2 recommended giving `deny` that meaning. `approvalMode` is a policy-level enum whose `approval` value is reserved, and a field of the lease slice that already counts children carries the meaning directly. The owner chose `lease_slice.maxChildrenTotal: 0` (D4).
- **A tenant forbid on the tenant record.** Revision 2 put `delegation.forbid` and `forbidSetBy` on the tenant record, written by a `tenant-admin` or a `platform-admin`. A forbid that the tenant's own administrators can clear protects no one from a stolen administrator credential, and the security record already gives every forbid a single writer, a weakening delay, and SIEM events. The owner moved the hard switch there (D5).
- **Keep a uniform tool list and rely on the refusal.** The owner asked that the tool not be offered. Every listed tool's description costs context tokens on every model call, and an agent offered a tool it cannot use can choose it and waste a turn on the refusal (S2).
- **Omit the platform server from the manifest when delegation is unavailable.** The other platform tools would go with it.
- **A per-runtime allow-list of platform tools.** Every tool already has a control of its own (DA-7), a list of names would duplicate those controls without their semantics, and an omitted core tool such as `lenny/request_input` would break a session that needs it. The list rules of "DA-4" read the existing controls, so an operator sets nothing new (O7).
- **An environment level.** Environments already scope targets through `defaultDelegationPolicy`, and another level would add a resolution step with no use case the owner named.

## Specification changes

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), the Runtime YAML and field notes, "Normative Merge Algorithm", and "Capabilities are customizable per tenant" | Add `allowDelegation` with its values, its refusal on `type: mcp`, and a merge row: restrict-only, a derived `true` refused over a base `false`, and the merged value of "DA-1". State that a derived runtime that sets `false` is the way to run workers that do not delegate beside an orchestrator of the same base. State that the per-tenant record accepts `allowDelegation: false` and refuses `true`. Add `capabilities.harnessApprovalRequests`, `true` or unset, versioned and gateway-read, which states that the runtime forwards harness approvals through `lenny/request_approval`. |
| [§8.1 Design Philosophy](../spec/08_recursive-delegation.md#81-design-philosophy) | State that whether a session may delegate is decided by the binary and by the configuration of "Delegation availability" in §8.3. |
| [§8.2 Delegation Mechanism](../spec/08_recursive-delegation.md#82-delegation-mechanism), the `LeaseSlice` table and **Delegation flow** | State that an explicit `maxChildrenTotal: 0` creates a child that starts no child, and that an absent field sets no limit. Add the availability and room check as the first step after the parent is fixed, with `DELEGATION_DISABLED` and `details.disabledBy`. |
| [§8.3 Delegation Policy and Lease](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) | Add a paragraph **Delegation availability** with the levels, the resolution table of "DA-2", the room rule and its refusals, the live reads, the session level, and the switches for an incident. Add `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` to **Helm settings governing cycle detection and delegation depth**, stating that they apply at restart. |
| [§8.5 Delegation Tools](../spec/08_recursive-delegation.md#85-delegation-tools) | Replace "Available on the platform MCP server for every delegation-capable pod" with the per-session listing of "DA-4". |
| [§8.6 Lease Extension](../spec/08_recursive-delegation.md#86-lease-extension) | State under **Extendable fields** that an extension never raises a `maxChildrenTotal` that is an explicit 0. |
| [§9.1 Where MCP Is Used](../spec/09_mcp-integration.md#91-where-mcp-is-used), **Platform MCP Server Tools** | State that the list is per session and computed from the current settings at each list, with every rule of "DA-4": the delegation tools and `lenny/set_tracing_context` by availability and room, `lenny/request_input` and `lenny/request_elicitation` on a session created on a provider surface, `lenny/request_elicitation` where the suppression of [§9.2](../spec/09_mcp-integration.md#92-elicitation-chain) covers every elicitation of the session, and `lenny/request_approval` by `capabilities.harnessApprovalRequests`. State that a call to an omitted tool is answered by the tool's own handler. |
| [§14 Workspace Plan Schema](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) and the derive request in [§15.1](../spec/15_external-api-surface.md#151-rest-api) | Add `delegationLease.maxDepth` to the derive request, narrowing only. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | Add `GET` and `PUT /v1/admin/tenants/{id}/delegation` with `allowByDefault` alone, `allowDelegation` on the runtime payload and on the per-tenant runtime record, and `details.disabledBy` and `details.effectiveSettings` on `DELEGATION_DISABLED`. |
| [§15.4.3 Runtime Integration Levels](../spec/15_external-api-surface.md#1543-runtime-integration-levels) | State that the platform tool list depends on the calling session and on the settings current at each `tools/list`, that the platform server sends no list-change notification, that a runtime which keeps a list keeps it until it lists again and meets the tools' refusals, and the cache hints on its `2026-07-28` list. |
| [§16.1 Metrics](../spec/16_observability.md#161-metrics) and [§16.7 Section 25 Audit Events](../spec/16_observability.md#167-section-25-audit-events) | Add `lenny_delegation_disabled_refusals_total{disabled_by}`, `delegation.availability_narrowed`, and `delegation.availability_widened`. |
| [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod), the `CH-MCP-PLATFORM` card | **Messages**: the per-session tool set and the cache hints on a `2026-07-28` list. **Preconditions**: the list depends on the calling session and on the settings current at each `tools/list`, and the server sends no list-change notification. |

The delegation guardrail's place in the tenant security record, and its tightening and weakening, are specification edits of [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture), which "Instructions for reviewers of other designs" asks for. This design's §8.3 paragraph cites them.

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/apis/lenny/v1alpha1`, `pkg/controller/runtime`, and `pkg/gateway/runtime/runtimestore` | `AllowDelegation *bool` on the Runtime resource, its mirror, the registry row, and `Merge`. `capabilities.harnessApprovalRequests` on the Runtime resource and in the revision's versioned fields. Regenerate the CRD manifests. |
| `pkg/gateway/externalapi/admin` | The field on runtime payloads with the derived and `type: mcp` refusals, `allowDelegation` on the per-tenant runtime record (`runtime_capability_overrides.go`) accepting only `false`, the tenant `delegation` sub-resource with `allowByDefault` alone, and the narrowed and widened audit events. |
| `migrations` | Columns for the runtime field, the per-tenant runtime record field, and the tenant default. |
| `charts/lenny` | `gateway.delegation.allowByDefault` and `gateway.delegation.forbid` in `values.yaml` and the schema, rendered to gateway flags. |
| `pkg/delegation` | A resolver that takes the tenant row, the active security record, the tenant runtime entry, the runtime row, and the platform flags and returns availability and `disabledBy`, shared by the handler, the service, and the bridge. `lease.ValidateChildSlice` admits an explicit `maxChildrenTotal: 0`. |
| `pkg/gateway/mcpfabric/mcptools` and `pkg/gateway/mcpfabric/delegation` | The two checks of "DA-3", the details, and the counter. The tool's input schema loses `approvalMode` and distinguishes an explicit `leaseSlice.maxChildrenTotal: 0`. `ErrDelegationDenied`, the `DELEGATION_DENIED` answer, and `delegate_approvalmode_test.go` go. |
| `pkg/gateway/session/sessionstore` and the lease extension path | The stored `DelegationLease` distinguishes an explicit `maxChildrenTotal: 0`, and `ExtendLease` never raises it. |
| `pkg/gateway/externalapi/errorclassify` | Remove the `DELEGATION_DENIED` entry and its test row. |
| `schemas/lenny-adapter.proto` and `pkg/proto/adapter/v1` | Replace `ERROR_CODE_DELEGATION_DENIED = 20` with `reserved 20;` and `reserved "ERROR_CODE_DELEGATION_DENIED";`, and regenerate the Go code. |
| `pkg/gateway/gatewaycontrol/platformtools` | `ListPlatformTools` reads the calling session's row and the current settings at each list and filters by every rule of "DA-4", in a fixed order: availability and room from the resolver, the session's children, the surface the session was created on, the pool's `elicitationDepthPolicy` and allow list with the session's depth, and the runtime revision's `capabilities.harnessApprovalRequests`. |
| `pkg/adapter/mcp` and `pkg/adapter` | `-32602` for a forwarded not-found status. The list path keeps forwarding each `tools/list` to the gateway with no copy, and the cache hints arrive with MP-5's `2026-07-28` result. |
| `docs/` | An operator page on delegation availability, its levels, the switches for an incident, and the derived-runtime pattern for workers, and the runtime author guide's statements that the platform tool list depends on the session and its settings, that a change reaches the runtime only when it lists again, that a change costs a prompt-cache write, and that a runtime declares `capabilities.harnessApprovalRequests` to be offered `lenny/request_approval`. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 0 | `schemas/lenny-adapter.proto` reserves number 20 and the name `ERROR_CODE_DELEGATION_DENIED`, and no Go file outside `proposals/` and the records names `DELEGATION_DENIED`. |
| 1 | The resolver over every combination of the levels, the guardrail, and the tenant runtime entry, with `disabledBy` for each (DAR-2, DAR-3). The derived merge and its refusal. A tenant runtime entry that names a base narrows its derived runtimes, and a write of `true` is refused. Every list rule of "DA-4", in a fixed order: may-delegate and has-child for the delegation tools and `lenny/set_tracing_context`, a session created on each provider surface, `elicitationDepthPolicy` values `block_all`, `suppress_at_depth: N`, and `allow_all` with an empty and a non-empty allow list at depths on each side of the suppression depth, and a revision with and without `capabilities.harnessApprovalRequests` (DAR-10). `lenny/send_message` is listed under every `messagingScope` value. An explicit `maxChildrenTotal: 0` in a slice is stored as 0 and leaves the child with no room, an absent field sets no limit, and both refusal paths of DA-2 name `disabledBy: session` for a client's 0 and a parent's 0. An extension request never raises an explicit 0. |
| 2 | The registry, tenant, and per-tenant runtime record stores round-trip the fields, and the RuntimeReconciler mirrors `allowDelegation`. |
| 3 | The `DELEGATION_DISABLED` envelope with `disabledBy` and `effectiveSettings`. The platform server declares no `listChanged`, a `2026-07-28` `tools/list` carries `ttlMs: 0` and `cacheScope: "private"`, and a call to a name outside the allow-list answers `-32602`. The `lenny/delegate_task` input schema has no `approvalMode`. |
| 4 | On the compose stack: a session on a runtime with `allowDelegation: false` lists no delegation tools and is refused from the pod and from the gateway `/mcp` (DAR-1, DAR-4). A tenant default of `false` with a runtime `true` delegates, and a tenant runtime entry for that runtime then refuses it with `tenantRuntime`. A guardrail written by a `security-admin` refuses at the next call, a `tenant-admin` cannot write it, and its removal takes effect only at the end of the weakening delay and can be cancelled (DAR-9). Each narrowing and widening write emits the event of its direction. A runtime set to `false` mid-session refuses the next call, leaves the running child alive, omits `lenny/delegate_task` from the next `tools/list`, and keeps `lenny/await_children` listed (DAR-5, DAR-6). A session created on the Responses surface lists neither `lenny/request_input` nor `lenny/request_elicitation`, a delegated child in a `block_all` pool lists no `lenny/request_elicitation`, and a runtime without `capabilities.harnessApprovalRequests` lists no `lenny/request_approval`, while a call to each omitted tool gets that tool's own answer (DAR-10). A derived runtime with `allowDelegation: false` lists no delegation tools while its base's sessions delegate. A derive with `delegationLease.maxDepth: 0` cannot delegate. A deployment with no values set behaves as today (DAR-7). |
| 5 | On Kind with the production adapter: after a runtime-level change, the session's next `tools/list` on its open platform connection returns the new list with no adapter restart, the session's runtime receives no notification, and a new session bound to a process that has listed gets its own list at its next `tools/list`. |
| 7a | Concurrent `lenny/delegate_task` calls racing a runtime write each see one value per call, under `-race`. Concurrent `tools/list` requests racing a runtime write each return a list computed from one value. |
| 9 | A runtime calling `lenny/delegate_task` from a stale list after delegation became unavailable is refused. A runtime cannot reach `lenny/create_session` from its pod's platform MCP server (DAR-8, with TS-3). A child created with `lease_slice.maxChildrenTotal: 0` cannot delegate through any path, including lease extension. A `tenant-admin` credential cannot remove the guardrail. |
| 11 | The operator page's examples match the resolution table, and the error catalog entry matches the envelope. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| How often Claude Code and Codex send `tools/list` to the platform server during one connection | "DA-5" states that most harnesses list once per connection, which sizes the stale-list window. | Open |
| How long a runtime process outlives a session on a pod, and whether common runtimes cache `tools/list` across sessions | It sizes the window in which a new session holds the previous session's list. | Open |
| That a per-session `tools/list` behind one pod-wide socket conforms to `2026-07-28`'s "MUST NOT vary per-connection" | "DA-5" argues that the list varies by the calling session and over time. The MP-5 proposal confirms it against the schema and the conformance suite. | Open |
| Whether the tenant default is changed through `PUT /v1/admin/tenants/{id}` alone, or a sub-resource fits, as `elicitation-content-integrity` does | "DA-1" proposes a sub-resource. | Open, for the proposal |
| Whether the per-tenant runtime record can hold `allowDelegation` beside its capability axes, or a sibling record keyed the same way fits better | "DA-1" places D1's entry on the existing record. The record's store and RV-1's narrowing rules are written for capability axes. | Open, for the proposal |

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

- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26): its reviewer states that TS-3's refusal reads the availability of this design together with the stored lease, adds `details.disabledBy` to `DELEGATION_DISABLED`, extends the "Hiding the tool" table to `lenny/discover_agents`, to `lenny/set_tracing_context`, and to `lenny/await_children` and `lenny/cancel_child` for a session with no child, adds `delegationLease.maxDepth` narrowing to the derive request, and records that an explicit `lease_slice.maxChildrenTotal: 0` from a parent leaves the child with no room and the `disabledBy: session` refusal.
- [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-1-revisions): its reviewer adds `allowDelegation` to the Live row of "Field classes", with S3's ground, notes that delegation settings, like `delegationPolicyRef`, are read at each call of a running session, and adds `allowDelegation: false` to "Per-tenant capability override" as a narrowing that refuses `true`. Its reviewer also lists `capabilities.harnessApprovalRequests` among the versioned, gateway-read fields of "Field classes", beside the `harnessApprovals` default that `message-delivery-design.md` adds, because the gateway reads the declaration at each list of the session's platform tools and it describes the binary.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#fields): its reviewer adds an `allowDelegation` row to "Fields" (restrict-only, `false` always allowed, `true` only over a base that is not `false`), states that a tenant-derived runtime with `false` is how a tenant runs workers that do not delegate, and adds the tenant default, the tenant runtime entry field, and the runtime field to "Configuration by scope". Owned pools and "Template copy" gain no delegation field.
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-8-runtime-capability-and-late-tools): no change. Revision 3 asked its reviewer to generalize `NotifyConnectorToolsChanged` to the platform server, and revision 4 withdraws the request, because the platform server sends no list-change notification (O4).
- [`mcp-protocol-upgrade-design.md`](mcp-protocol-upgrade-design.md#mp-5-intra-pod-servers): its reviewer adds the cache hints of "DA-5", `ttlMs: 0` and `cacheScope: "private"`, to the platform server's `2026-07-28` `tools/list`. This design no longer needs `subscriptions/listen` or a `tools.listChanged` capability on the intra-pod platform server, which revision 3 asked for.
- [`message-delivery-design.md`](message-delivery-design.md#md-19-harness-approvals): its revision 8 states in "MD-19" the `capabilities.harnessApprovalRequests` declaration and that the platform tool list omits `lenny/request_approval` without it. No further change. "DA-4" reads its "MD-8" refusal of elicitation and `lenny/request_input` on provider surfaces, and "DA-7" records its removal of `lenny/output` (A6).
- [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture): its reviewer adds `"delegation": "forbid"` to the SO-2 record, lists its removal among the weakenings of "Tightening and weakening" and its setting among the tightenings, and states that the gateway's delegation gate and the platform tool list read the active version at each call and each list. Its reviewer also states that an SO-5 approval hold on `lenny/delegate_task` runs after this design's refusal, and that a `tenant-admin` cannot write the guardrail.
- [`runtime-routing-design.md`](runtime-routing-design.md#post-chain-point): no change. Its reviewer confirms that the post-chain point needs no availability check, because a child's availability reads the final row at the child's own calls and depends on no pool.

## Proposal split and order

1. **DA-A**, after TS-3's proposal, whose calling-session parent, allow-list, `DELEGATION_DISABLED`, and stored `maxDepth` it extends: the specification edits; the levels, the resolver, and the refusal; the per-session list and its rules; the derived runtime rule and the tenant runtime entry; the derive narrowing; the explicit-zero per-hop switch and the extension rule; the adapter's `-32602`; and the removal of the `approvalMode` argument and `DELEGATION_DENIED` with the proto reservation. Three parts land with or after the proposals that add what they read: the guardrail row of the resolution with the SO-2 proposal of [`security-oversight-design.md`](security-oversight-design.md#so-2-tenant-security-record-and-posture), which adds the record; the provider-surface rules for `lenny/request_input` and `lenny/request_elicitation` with MD-B of [`message-delivery-design.md`](message-delivery-design.md#proposal-split-and-order), which lands "MD-8"; and the `lenny/request_approval` rule and `capabilities.harnessApprovalRequests` with MD-D of the same design, which lands "MD-19".

Revision 3's DA-B, the notification work, is withdrawn under O4. The cache hints land with MP-5.

The defect that `lenny/delegate_task` does not apply `DelegationPolicy` rules (F-8.3.14) is independent of both and takes its own proposal.

## Sources

- MCP `2025-11-25` tools, including the `listChanged` capability, the list changed notification, and unknown-tool errors: https://modelcontextprotocol.io/specification/2025-11-25/server/tools
- MCP `2026-07-28` tools, including the rule on per-connection variance, the deterministic order and prompt-cache statement, and delivery through `subscriptions/listen`: https://modelcontextprotocol.io/specification/2026-07-28/server/tools, read again on 2026-11-09 for revision 3.
- MCP `2026-07-28` caching, `ttlMs` and `cacheScope`: https://modelcontextprotocol.io/specification/2026-07-28/server/utilities/caching
- MCP `2026-07-28` discovery, the `server/discover` request and its `capabilities`: https://modelcontextprotocol.io/specification/2026-07-28/server/discover, read 2026-11-09.
- MCP `2026-07-28` versioning and compatibility, the absence of an `initialize` handshake for modern clients and dual-era servers: https://modelcontextprotocol.io/specification/2026-07-28/basic/lifecycle, read 2026-11-09.
- Amazon Web Services, "Prompt caching for faster model inference", https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html, read 2026-11-09, which states that cache checkpoints are processed in the order tools, system, and messages, and that modifying the tools invalidates the caches of the later sections.

## Review history

An adversarial review read revision 2 on 2026-11-09. Revision 3 applies its fixes and the owner's answers of the same date. Revision 4 applies the owner's answers of 2026-11-10 to the decisions revision 3 left open.

### Revision notes

- Revision 1, 2026-11-09: first draft, written from the owner's request for delegation configurable per runtime or pool with platform and tenant defaults, and from the owner's later thought that sessions should be able to disable delegation, which O5 answers by reusing decision 26 of [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#client-delegation-limits-decision-26).
- Revision 2, 2026-11-09: files the defects found while writing this design in `BUILD-GAPS.md`, at the owner's request of 2026-11-09, as F-8.3.14, F-8.4.4, and F-15.4.8. The pod's reach to `lenny/create_session` was already filed as F-9.1.13, so it gains that ID and no new finding.
- Revision 3, 2026-11-09: applies the owner's answers of 2026-11-09 and the adversarial review of revision 2. The owner's answers, as recorded: "D1 settled: a tenant can narrow one platform runtime through a narrow-only per-tenant entry." On D2, in the owner's words: "I agree with you. As long as it can be disabled in derived runtimes, we're good." "D3 settled: no mid-session disable." "D4 settled: the per-hop switch is `lease_slice.maxChildrenTotal: 0`. `approvalMode` stops being a `delegate_task` argument." "D5 settled: the tenant hard switch becomes a security-oversight SO-2 guardrail `delegation: "forbid"`, set by `security-admin`. Tenant admins write only `delegation.allowByDefault`. `delegation.forbid` and `forbidSetBy` are dropped." In response, the tenant runtime entry joins the levels and the resolution; the pool level, its owned-pool rules, its tests, and its reviewer instructions go, and "Workers beside orchestrators" states the derived-runtime pattern; the session level is fixed at creation; DA-6 uses an explicit `maxChildrenTotal: 0`, distinguishes it from an absent field, protects it from extension, and removes the `approvalMode` argument and `DELEGATION_DENIED`; and the tenant forbid moves to the SO-2 record. O2, O6, and O8 of revision 2 become D2, D4, and D5, O5 narrows to derive and resume, and O1, O3, O4, O5, and O7 stay open with their recommendations. The review's fixes: DA-2 and DA-6 now give one refusal for a child that cannot delegate; `DELEGATION_DENIED` is recorded as living only in code and in the proto, the §15.1 row no longer removes it from the specification, and the proto reserves number 20; the adapter keeps one tool list per session, cleared by the CA-8 RPC and on bind, and answers with `ttlMs: 0`; "Limits" states the prompt-cache cost of a list change and the public-API path around DAR-8; S3 rests on who writes the field; under MCP `2026-07-28`, `listChanged` is declared in the `server/discover` result; removing the guardrail takes the SO-2 weakening delay, and widening and narrowing writes emit distinct audit events; and "Limits" and DA-1 state that a Helm forbid is not a switch for an incident.
- Revision 4, 2026-11-10: applies the owner's answers of 2026-11-10, after which no decision is open. The open table is removed, and its decisions move to "Settled by the owner on 2026-11-10" with their IDs.
  - S2, reworded. The owner wrote: "It's more than a convenience. Tools cost context tokens and can confuse the agent." S2 now states that the refusal is the enforcement and that leaving an unavailable tool out of the list keeps its description out of the agent's context on every model call and keeps the agent from choosing a tool that will be refused. TS-3's quote stays as history. "High-level design", "DA-4", and the rejected uniform list follow.
  - O1, "ok": option (B). The more specific level wins among the defaults, and the platform and tenant forbids override every other level, as "DA-2" already stated.
  - O3, "ok": the platform default is `true`.
  - O4, "ok" to option (D), a new option, after the owner asked for "whichever means the smallest blast radius from an implementation point of view". The decision row records the comparison with the tree. "DA-5" is rewritten: the handler and `ListPlatformTools` read the current settings at each call and each list, the adapter keeps no copy, and a stale list lasts until the runtime lists again. The per-session list cache in the adapter, the list-change notifications, the `listChanged` declarations, `subscriptions/listen`, the fan-out, `--delegation-list-change-rate`, `lenny_platform_tools_list_changed_total`, and the dependency on CA-8's notification RPC are removed, with revision 3's DA-B. "Alternatives rejected" records revision 3's option (A) and its reason, option (B) as larger and without the incident switch, and option (C). "Scope", "Terms", the guardrail's transitions and the incident switches in "DA-1", "DA-3", the requirements, "Limits", the specification and non-specification changes, the tests, "Validation owed", and the reviewer instructions for the connector authorization, MCP protocol upgrade, and security oversight designs change to match.
  - O5, "ok": a derive request accepts `delegationLease.maxDepth`, which can only lower the carried value, and a resume keeps the stored value.
  - O7, "ok", with an extension. No per-runtime switch is added. The list filter of "DA-4" also leaves out the inapplicable tools: `lenny/request_input` and `lenny/request_elicitation` on a session created on a provider surface, where "MD-8" of `message-delivery-design.md` refuses both; `lenny/request_elicitation` where §9.2 suppresses every elicitation of the session, checked against §9.2 "Deep elicitation suppression" as a delegated session in a `block_all` pool or a session at or beyond the pool's suppression depth whose pool's allow list names no type; `lenny/set_tracing_context` for a session that may not delegate; and `lenny/request_approval` for a runtime that does not declare the new `capabilities.harnessApprovalRequests`. The candidate rule for `lenny/send_message` is dropped, because each `messagingScope` value of §7.2 allows the direct parent and the direct children. "Terms", "Current state", "DA-4" with a third column for the answer to an omitted call, requirement DAR-10, the specification and non-specification changes, the tests, and the proposal order follow. "DA-7" records that `lenny/output` leaves under message delivery decision A6 and that `lenny/request_approval` arrives under "MD-19", and it gains a column naming each tool's list rule. A reviewer instruction asks `runtime-revisions-design.md` to list `capabilities.harnessApprovalRequests`, and `message-delivery-design.md` revision 8 states the declaration in "MD-19".
  - The status line reads against `message-delivery-design.md` revision 8.
