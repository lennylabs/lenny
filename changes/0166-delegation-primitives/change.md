# Delegation as session primitives

Status: `Decided`, revision 13, 2026-11-24. Programs: [v1](../programs/v1.md).

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, or another change on 2026-11-22. |
| **[design]** | What this change proposes. Nothing implements it. The owner's decisions are in `decisions.md`, and the text names the decision each part follows. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The owner asked on 2026-11-22 whether Lenny should provide delegation at all, or should instead provide mechanisms with which a deployer builds the same delegation outside Lenny or as an extension. This change frames that question with evidence from the tree and proposes an answer for the owner to decide.

Delegation today is a second session-creation path with its own semantics **[source]**:

- [§8](../../spec/08_recursive-delegation.md#8-recursive-delegation) runs to about 1,100 lines, and 655 lines of the numbered specification files mention delegation.
- The Go packages that exist for delegation alone hold about 15,000 lines of non-test source: `pkg/delegation/` and, under `pkg/gateway/platformtools/`, `delegation`, `delegationbudget`, `leasecontrol`, `treebudget`, `treerecovery`, `treearchive`, `orphancleanup`, `resultrollup`, `deadlock`, `tree.go`, `tree_rest.go`, `waits.go`, and `delegation_admin.go`. The platform tool handlers in `mcptools` and the session server carry more.
- Most member changes of the v1 program state a separate rule for a delegated child. [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) gives it an environment and sharing paths. [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) gives it its root's principal and `caller_type`. [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) gives it `userCredentialBindings` on the lease. [`0141-runtime-routing`](../0141-runtime-routing/change.md#post-chain-point) gives it a creation kind and routing labels. [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets) gives it a tree budget scope. [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-9-when-a-parent-ends) gives it states and a cascade, [`0135-message-delivery`](../0135-message-delivery/change.md#md-22-parents-and-children) gives it child events, and [`0126-delegation-availability`](../0126-delegation-availability/change.md) gives it a resolution chain of availability levels. Each feature added later has to state its own rule for a delegated child as well.

Much of §8 encodes orchestration policy, which an orchestrator outside Lenny would normally own **[source]**:

- Cycle detection on runtime identity runs a three-layer AND gate for self-recursion under a mode switch with three values (§8.2). §8.2 justifies it as preventing "circular wait deadlocks". MD-22 of 0135 removes `lenny/request_input`, the `input_required` sub-state, and the subtree deadlock detector, which removes the waits that rationale names. `maxDepth` and the budgets bound recursion on their own. No v1 change revisits the gate.
- The lease carries values that expose gateway internals or one orchestration model, such as `maxTreeMemoryBytes`, `treeVisibility` with its compatibility check against `messagingScope`, `snapshotPolicyAtLease`, `perChildRetryBudget`, the reserved `approvalMode: approval`, and `cascadeOnFailure` (§8.3, §8.4, and §8.10).
- The v1 program already removes parts of §8 piecemeal. [0133](../0133-llm-upstreams-and-cost-budgets/change.md#lu-4-removal) removes `credentialPropagation`, [0141](../0141-runtime-routing/change.md) replaces `experimentContext` with routing labels, [0124](../0124-connector-removal/change.md#cr-1-removal) removes `allowedExternalEndpoints` and the `connector` target type, and [0135](../0135-message-delivery/change.md#md-23-tool-and-frame-names) removes `set_tracing_context` as a tool and renames the tool set.

At the same time, MD-22 of 0135 states the semantic that makes a smaller core possible: "A child session behaves as a top-level session in every way, and its parent is its client for messages." Once a child is a session whose client is its parent, what delegation adds over any other client is a short list of guarantees that only the gateway can enforce, because the pod is untrusted. §8.1 states them: the child's budget is carved from the parent's, the child's scope is a subset of the parent's, and the child's isolation is no weaker than the parent's.

Those guarantees are the reason delegation cannot be moved out of Lenny without a replacement. A deployer who builds delegation outside Lenny today has to place a client credential in the workspace, and a pod that holds one can create a root session outside every delegation limit. 0126 already records this path in its "Limits" ("The public API is a path around the setting"), and §10.2 states that "a pod never holds" a `session_capability` token **[source]**.

The owner prefers integrations outside Lenny's core, reached through REST or egress, with small generic hooks in Lenny. The REST-only client API of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), the removal of connectors and external agents in [`0124-connector-removal`](../0124-connector-removal/change.md), and the removal of provider-specific code in 0133 apply that preference. Delegation is the largest feature that has not been reviewed against it.

## Scope

**Proposed [design]:**

- Primitives in the core that carry the authority and cost guarantees of delegation: a session grant, a parent link on session creation, a budget carved from the parent's at creation, and a parent's control of how its children's events reach it ("DP-1" to "DP-4").
- The inputs from which the gateway resolves a root session's grant, and the resolution of every caveat, including the policies and ceilings a tenant administrator sets for the tenant and for each environment ("DP-1").
- The platform operations a runtime calls to create and manage sessions, and the two ways a session leaves its parent ("DP-1" and "DP-2").
- A reference extension that provides the session tool set over those primitives, shipped with the runtime SDKs and the reference MCP bridge ("DP-5").
- A disposition for each delegation setting of §8 and of the v1 changes: kept as a primitive, moved to the extension, or dropped ("DP-6").
- What each other member change takes on, so that each rule has one owner ("DP-7").

**Owned by other changes [design]:** a mechanism that concerns every session, and not only a session with a parent, stays with the change that owns it, and this change cites it and states its delta (decision 35). The blocking interface for tools a client offers belongs to 0122, child events and their classes to 0135 MD-22, the message that starts no turn to 0135 MD-1 and MD-14, recovery and hand-off to 0146, the REST routes, events, webhooks, and error codes to 0138, where delegation checks run to 0141, crossing between environments to 0149 TS-3, approval holds and the security record to 0142, availability to 0126, and the carve's budget objects to 0170. "DP-7" lists what each takes on.

**Out of scope:**

- Agent-to-agent protocols and external agents. 0124 removed them, and this change does not reintroduce them.
- §8.4's `approval` mode, which is reserved and unimplemented. Approval of a child by a person is SO-5a's of 0142, which gains conditions on the target runtime and on an amount in `llmCredits` (decision 36).
- The availability of the platform tools that are unrelated to children, such as `ask_question`. 0126 "DA-7" reviews them.
- Root-session creation by a client through the public API. It is unchanged.

## Terms

| Term | Meaning |
| :-- | :-- |
| Built-in delegation | Delegation as §8 and the v1 changes specify it: the gateway-hosted tools `lenny/delegate_task` and the rest (`start_child_session` and the rest under MD-23), the delegation lease, `DelegationPolicy`, tree budgets, tree recovery, and the cascade. |
| Primitive | A mechanism this change keeps in the core because only the gateway can enforce it against an untrusted pod. "DP-1" to "DP-4" define the primitives. |
| Session grant | The authority a session holds to create sessions as its children and to act as their client, recorded by the gateway and never held by the pod as a bearer token ("DP-1"). |
| Caveat | A restriction recorded on a session grant. A security caveat never widens from parent to child, and a resource caveat counts something a tree consumes. "DP-1" lists both kinds. |
| Content caveat | The security caveat that holds `contentPolicy` and the export and import limits. Every session carries one, with or without a grant ("DP-1"). |
| Root grant inputs | The settings from which the gateway resolves a root session's grant at creation ("DP-1"). |
| Tags | A small map of strings that a parent sets on `session.create` for its own bookkeeping, which the child receives and the read operations return, and which billing, budgets, and routing never read ("DP-1", decision 44). |
| Idempotency key | A string a parent passes on `session.create` so that a repeated call returns the child the first call created ("DP-1"). |
| `wakeOn` | A parent's setting of the event classes and event kinds that open a turn in it or wake it ("DP-4"). |
| Transfer and release | The two ways a session leaves its parent. A transferred session stays in its tree with the root's client as its client, and a released session becomes a root outside the tree ("DP-2", decisions 32 and 46). |
| Fenced child | A child created with fewer credential bindings, fewer egress hosts, or a stronger isolation profile than its parent ("DP-8"). |
| Parent link | The `parentSessionId` of a session created under a session grant, with the lineage, principal, environment, and cascade that follow from it ("DP-2"). |
| Carved budget | The budget a child receives at creation, reserved from its parent's session budget and returned at the child's end ("DP-3", 0170 "BG-8"). |
| Reference extension | The session tool set implemented over the primitives and shipped with the runtime SDKs and the reference MCP bridge, which a runtime author may use, replace, or omit ("DP-5"). |
| Orchestration policy | A rule about how an orchestrator uses its children, such as cycle avoidance, retry, visibility of siblings, discovery, and presets. This change moves orchestration policy out of the core. |
| Labels | Parts of the design carry the label DP-n, and requirements carry the label DPR-n. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Delegation lease | The record of what a session may do as a delegator: depth, budgets, and policy references ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| `DelegationPolicy` | A named resource of tag rules that states which runtimes and pools a session may target ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| Platform tool server | The tool server `platform`, through which a runtime calls Lenny's platform tools with `tool_call` and `list_tools` frames on `CH-MSGSOCK`, forwarded by the adapter to the gateway ([`0139-runtime-protocol`, "RP-3"](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames)). |
| Reference MCP bridge | The MCP server the runtime SDKs ship, which serves one MCP server per tool server to a harness and turns each MCP request into tool frames ([`0139-runtime-protocol`, "RP-5"](../0139-runtime-protocol/change.md#rp-5-reference-mcp-bridge)). |
| Token exchange | `POST /v1/oauth/token` under RFC 8693, which narrows scope and can mint an `a2a_delegation` child token whose `act` claim names the parent session and its `delegation_depth` ([§13.3](../../spec/13_security-model.md#133-credential-flow)). |
| Credit budgets | 0133's budgets with reservation before a call and settlement after it ([LU-6](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets)), which the budget objects of [`0170-budgets`](../0170-budgets/change.md) generalize. |
| `onParentEnd` | The policy for a parent's non-terminal children when the parent ends, `close_all` or `let_finish` ([`0146-session-lifecycle`, "SL-9"](../0146-session-lifecycle/change.md#sl-9-when-a-parent-ends)). |
| Child event | A message in a parent's inbox that reports a child's mid-turn message, end of turn, end, or other change ([`0135-message-delivery`, "MD-22"](../0135-message-delivery/change.md#md-22-parents-and-children)). MD-22 takes on the event classes, `from`, and the new kinds ("DP-7"). |
| Question | An elicitation that a child raises with `ask_question` or with a `request` of `kind: "question"`, which travels one hop at a time under the [§9.2](../../spec/09_mcp-integration.md#92-elicitation-chain) chain and reaches a parent as a child event under MD-22. |
| Session derive | The creation of a session from another session's state, under [`0144-session-derive`](../0144-session-derive/change.md). |
| Session `labels` | The user-defined metadata of [§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) on a session, indexed and filterable in `GET /v1/sessions`, `GET /v1/usage`, and `GET /v1/metering/events` for cost attribution. |
| Who writes delegation policies | The §10.2 permission matrix gives "Manage delegation policies" to `platform-admin` for all tenants and to `tenant-admin` for its own tenant, read-only to `tenant-viewer`, and "Manage environments" to the same roles **[source]**. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| DA-1 to DA-7 and DAR-6 | [`0126-delegation-availability`](../0126-delegation-availability/change.md) | The availability levels, their resolution, the refusal, the tool list, running sessions, per-hop narrowing, the review of the other platform tools, and the tools a parent with children keeps. |
| TS-3 and decision 26 | [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#client-delegation-limits-decision-26) | The session's stored environment, sharing paths and what crosses them, and a client's `maxDepth: 0`. |
| MD-1, MD-9, MD-14, MD-22, MD-23, MD-25, and MD-26 | [`0135-message-delivery`](../0135-message-delivery/change.md#md-22-parents-and-children) | Delivery intents, inter-session messages and their rate limit, future intents and the extension rule, the one session model with child events, the tool names, watching a child's stream, and importing a child's files. |
| MD-3 | [`0135-message-delivery`](../0135-message-delivery/change.md#md-3-inbox-states-and-custody) | Inbox custody, including deduplication of a resent message by its id. |
| SL-4, SL-6 to SL-10, and SL-12, with SLR-9 | [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-8-parents-children-and-their-states) | Tree suspension, dormancy and qualifying activity, closing, the states of parents and children, the cascade, admission caps and timers, and the recovery of child events, with the rule that a parent stays awake while a descendant runs. |
| T7, T16, CT-1 to CT-10 | [`0122-client-tools`](../0122-client-tools/change.md) | Client tools, their delegation rule, the turn rule, and their admission, validation, pending calls, history, and audit. |
| RO-2 | [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) | The protocol-neutral REST additions. |
| RP-1 to RP-5 | [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) | The handshake with `session_start`, the capabilities, tool calls on frames with their `server` member, availability, and the reference MCP bridge. |
| RT-2 and RT-3 | [`0141-runtime-routing`](../0141-runtime-routing/change.md) | The `PreRoute` chain and its post-chain point, and routing labels, which a child takes from its parent. |
| SO-2 and SO-5a | [`0142-security-oversight`](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | The tenant security record with its single writer, guardrails, and weakening rule, and approval rules that hold a call until an approver answers. |
| LU-4 and LU-6 | [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets) | The removal of LLM credential brokering, and credit pricing with the reservation of a model call in session budgets, whose former scopes, the tree scope among them, the budget set of 0170 replaced. |
| CR-1 | [`0124-connector-removal`](../0124-connector-removal/change.md#cr-1-removal) | The removal of connectors and of every external-agent concept. |
| Effective policy | [`0127-egress-and-git`](../0127-egress-and-git/change.md#effective-policy) | A child in its parent's environment inherits the parent's `egress.allowedHosts`, `egressLockHosts`, and named bindings, except a `userCredential` binding. |
| Placement | [`0136-placement`](../0136-placement/change.md) | For `kind: delegate`, the parent's effective placement requirements apply to the child. |
| RV-1 and `runtimeOptions` | [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-1-revisions) | The delegation `TaskSpec` gains `runtimeOptions`, validated against the child's chosen revision at the post-chain point. |
| DR-4 | [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md#fields) | A tenant-derived runtime may set a narrowing `delegationPolicyRef`. |
| WM-12 | [`0153-workspace-materialization`](../0153-workspace-materialization/change.md#wm-12-mid-session-uploads-and-child-file-imports) | Mid-session uploads by a client, and imports of a child's files. |

## High-level design

Lenny keeps the guarantees of delegation and stops providing the orchestrator. A session that its configuration allows to delegate holds a session grant. With the grant, the session can create sessions as its children, act as their client, and do nothing else that a client credential could do. Each child is an ordinary session, as MD-22 already states, linked to its parent, inside its parent's environment and principal, under a budget carved from its parent's session budget, at an isolation level no lower than its parent's, and with a grant whose caveats are at least as narrow as its parent's. When the parent ends, its children end under `onParentEnd`. The gateway delivers each child's events and questions to the parent under MD-22, and the parent chooses which of them wake it.

The gateway resolves a root session's grant from settings that people with authority over the session set. A platform administrator sets the platform defaults and ceilings. A tenant administrator writes the tenant's delegation policies, as §10.2 already allows, and sets a policy and numeric ceilings for the whole tenant and for each environment. A tenant's security team holds the security-tier switches in the security record of 0142, where one writer and one weakening rule govern them. The runtime contributes its policy reference, its defaults, and its ceilings, and the client can only narrow what the others allow. A parent can only narrow its own grant for a child, with two exceptions that move resources rather than authority: lease extension raises a resource limit within its ceilings, and a parent can move unused budget between itself and a live child.

The pod never holds a credential for this. The runtime asks for a child through the platform tool server, the adapter forwards the request with the session's identity, and the gateway applies the grant. A deployer who wants a different orchestrator does not need to put a client credential in a workspace, so the bypass that 0126's "Limits" records closes.

The tools that a model uses to start and manage other sessions, the tools of MD-23 under generic names such as `start_session`, `await_sessions`, and `send_message`, move to a reference extension. The extension ships with the runtime SDKs and the reference MCP bridge, implements each tool over the primitives, and keeps the schemas of MD-23 and the rendering rule of MD-22. A runtime author uses it, replaces it, or omits it.

Orchestration policy moves to the extension or is dropped. Cycle detection, sibling visibility, retry of a failed child, discovery filtering, and presets become choices of the orchestrator. `maxDepth`, the carved budgets, the isolation floor, the policy cap on targets, and the cascade stay in the core as caveats or primitives. "DP-6" lists each setting.

A mechanism that every session uses, such as child events, client tools, recovery, the REST surface, or crossing between environments, stays with the change that owns it. This change states only what is specific to a session with a parent, and "DP-7" lists what each other change takes on for it (decision 35).

```
 runtime (pod)                 adapter            gateway
 ─────────────                 ───────            ───────
 reference extension
  start_session ──tool_call──> forwards ──> session grant of S?
                                     (session S)    │ no grant ──> refused, no child
                                                    ▼
                                                  caveats: depth, isolation floor,
                                                  policy cap, environment, content
                                                    │   (checked at 0141's post-chain point)
                                                    ▼
                                                  POST /v1/sessions semantics with
                                                  parentSessionId = S, carved budget,
                                                  narrowed child grant
                                                    │
 inbox  <── child events (MD-22), wakeOn ──────────┘
```

## Requirements

| ID | Requirement |
| :-- | :-- |
| DPR-1 | A session creates a child only under its session grant, and a session with no grant creates none. |
| DPR-2 | A child's grant carries every caveat of its parent's grant. No later hop, lease extension, or setting below the platform widens a security caveat. A resource caveat rises only through lease extension within its ceilings, or through a move by `session.set_limits` from the parent's own unused amount. |
| DPR-3 | A child's isolation level and placement are at least its parent's, its remaining depth is less than its parent's, and its environment and principal are its parent's unless a sharing path of TS-3 applies. Within one environment, the credential bindings and egress hosts a child receives are a subset of its parent's. Across environments, TS-3 decides what the child receives (decision 41). |
| DPR-4 | A child's budget is reserved from its parent's session budget at creation, and the unused part returns to the parent when the child ends or when the parent takes it back. Under the default of `gateway.delegation.sessionBudget`, a session without a session budget creates no child, so every child is carved; under `optional`, a parent without one creates children that no carve bounds ("DP-1", decision 52). |
| DPR-5 | No pod holds a bearer token for the session grant. The gateway applies the grant to the session that the adapter names. |
| DPR-6 | When a parent ends, its non-terminal children end under the parent's `onParentEnd`. A force-terminate of the parent, and a tree terminate, close every descendant at once whatever `onParentEnd` holds. |
| DPR-8 | The session tool set is implemented outside the gateway, over the primitives, and a runtime that omits it starts no child through the platform tool server. |
| DPR-9 | A `session.create` that repeats an idempotency key the same parent used before returns the child of the first call and creates no second child. |
| DPR-10 | A root session's policy cap is the intersection of every policy that the platform, the tenant, the session's environment, the runtime, and the client name. Each numeric caveat is the client's value when it sets one, and otherwise the first default that is set among the runtime's default lease, the environment's, the tenant's, and the platform's, at most the smallest ceiling that the platform, the tenant, the environment, and the runtime set ("Resolution" in "DP-1", decisions 40 and 49). No ceiling of `maxDepth`, `maxChildrenTotal`, or `maxParallelChildren` is 0, and a session with no depth left holds no grant (decision 50). |
| DPR-11 | The gateway records the creation of each grant with its resolved caveats and the level each came from, the creation of each child with the parent's grant, the policy version that admitted the target, and the client name of the connection that sent the call, and each leave with its actor, its mode, and the former parent. |
| DPR-12 | A later change that adds a child event kind, a member of an operation's result or of a child's record, or a way for a child to join a parent other than `session.create`, leaves an extension written against this change working: the reference extension ignores what it does not know, and the gateway delivers an event kind that needs the parent to act, or a child created by another path, only to a runtime that declares it. |
| DPR-16 | A session leaves its parent only by `session.transfer` or `session.release`, only while the grant that governs the call is present and the security record does not forbid delegation, only as far as that grant's `leave` allows, only at a depth no greater than `leaveMaxDepth`, where the depth is the session's depth in its original tree, which its lineage records and a release does not reset, for the released session and for every session created under it afterwards, and only while its tree has made fewer leaves than `maxLeaves`. A transferred session stays counted in its tree's `maxTreeSize` and in the reach of the root's cascade and `terminate-tree`, a released session keeps its security caveats and falls under the tenant's session quotas, and after either the former parent has no operation on the session; the root's client changes a transferred session's budget, as 0170 decision 52 states. The grant that governs a call of the parent is the parent's, and the grant that governs a call of the root's client through the REST route is the root's. |
| DPR-18 | A parent's tools reach a child only when `gateway.delegation.parentTools.enabled`, the security record, `offerTools`, the child's revision, and the call all allow them, never across environments, and a fenced child receives none unless its `session.create` sets `offerTools: allow` (decisions 23, 31, 37, and 41). |
| DPR-20 | Every session carries a content caveat resolved from the platform, the tenant, the environment, and the runtime, the strictest value winning, narrowed for a child and never widened, and every file that moves between two sessions passes the sender's content caveat on export and the receiver's on import, whichever operation carries it (decision 45). |
| DPR-21 | A session's `labels` are its root client's, unchanged at every depth, and no parent writes them. A parent's tags reach its child and the read operations, and billing, budgets, and routing never read them (decision 44). |

DPR-7, DPR-13 to DPR-15, DPR-17, and DPR-19 of earlier revisions are requirements that other changes take on, because the mechanisms they bind belong there (decision 35). Their tests travel with them:

- **DPR-7, on 0135 MD-22 and 0146 SL-12.** The gateway delivers each child event, and each question a child raises, to the parent's inbox or to a pending wait, once, and holds an undelivered event or question across the parent's recovery under the recovery rules "DP-7" lists.
- **DPR-13, on 0122.** A runtime calls a tool its client offers, a REST client, an adapter, or a parent, as a blocking call that stays open until the client answers or the hold passes, and a result that arrives after the call is gone reaches the runtime as `toolResults` on its next message.
- **DPR-14, on 0135 MD-22.** A `status` event carries only members the gateway wrote, with any text the child wrote in a member attributed to the child, and a child event outside its parent's `wakeOn` opens no turn and wakes no parent.
- **DPR-15, on 0135 MD-1 and MD-14.** A message with the `append` intent, from a client or a parent, starts no turn in its recipient (decision 38).
- **DPR-17, on 0135 MD-26.** An import takes only a file part that a message or a child event delivered to the importer.
- **DPR-19, on 0135 MD-25 and the client read of 0138.** For a child in another environment than its parent, a parent's read of the child's history and a watch carry only what TS-3 "Model" lists (decision 34).

## Current state

**Delegation is gateway-hosted [source].** §8.2 defines `lenny/delegate_task` on the platform MCP server, and §8.5 lists the delegation tools "for every delegation-capable pod". The gateway builds a virtual MCP child interface per child and reconstructs it from the task tree when a parent pod fails (§8.2 "Virtual child interface lifecycle", §8.10).

**The lease carries policy and budgets together [source].** §8.3 "Delegation Lease" lists depth, children, parallelism, tree size, token budget, policy references, isolation floor, retry budget, maximum age, file export limits, approval mode, cascade, credential propagation, external endpoints, messaging rate limits, tree memory, snapshotting, tree visibility, experiment context, and tracing context.

**Tree budgets use Redis scripts [source].** §8.3 "Budget Reservation Model" reserves tree budgets with `budget_reserve.lua` keyed by `{root_session_id}`, waits for usage quiescence before `budget_return.lua`, and fails the root on `BUDGET_KEYS_EXPIRED`. The code is in `pkg/gateway/platformtools/treebudget` and `delegationbudget`. 0133 LU-6 brings the tree scope under credit budgets.

**A child-token exchange exists [source].** §13.3 defines child minting through the token exchange with `actor_token`, which issues an `a2a_delegation` token whose `act` claim carries the parent's `sub`, `session_id`, `tenant_id`, and `delegation_depth`. 0124 removes external agents, which were the intended holder of such a token, and leaves the exchange in place. §10.2 states that session creation mints no `session_capability` token and that a pod never holds one.

**Tenant administrators write delegation policies, with partial means to apply them [source].** The §10.2 permission matrix lets a `tenant-admin` manage the delegation policies and the environments of its own tenant. A tenant administrator applies a policy through a tenant-derived runtime's `delegationPolicyRef`, which may only narrow (0147 "DR-4"), and through an environment's `defaultDelegationPolicy` (§10.6), which TS-3 of 0149 records that no code applies outside discovery. A platform runtime's `delegationPolicyRef` resolves by name in the session's tenant, so a tenant administrator who writes a policy of that name controls its content (0147). No tenant-wide policy and no tenant ceiling on child counts, parallelism, or tree budget exists, and §8.3's delegation presets are deployment values.

**A child's questions reach its parent through the virtual child interface [source].** §9.2's chain routes a child's elicitation to its parent "via virtual MCP", and §8.2 holds a pending elicitation when the parent pod fails and replays it on resume.

**The child model is already a session model [source].** MD-22 of 0135 makes a child behave as a top-level session with its parent as its client, 0146 gives the two one set of states, and 0144 makes derive one route beside create.

**No step has started [source].** 0126, 0135, 0143, 0146, 0149, and 0151 are `Draft`, and each of their steps is `Planned`, on 2026-11-22. The redesign therefore discards design text and no built code from the v1 program. The code of built-in delegation predates the program.

## Detailed design

Each part below names the decision of `decisions.md` that settles it. A rule that another change owns is cited by its label, and "DP-7" lists what that change takes on.

### DP-1 Session grant

A session grant is a gateway-side record attached to a session row **[design]**. A child receives a grant derived from its parent's, narrowed by the parent's request. The grant holds two kinds of caveat, and this is the one place that lists them:

- **Security caveats**, which never widen from parent to child: the remaining depth, the isolation floor, the policy cap, the environment and any sharing path, the principal, the credential bindings and egress hosts a child may receive, `onParentEnd`, `offerTools`, `leave`, `leaveMaxDepth`, and the content caveat.
- **Resource caveats**, which count what a tree consumes: the child count, the parallelism, the tree size, `maxLeaves`, a child's maximum age, `messagingRateLimit`, `toolCallRateLimit`, and the carved budget.

**Content caveat.** The content caveat holds `contentPolicy` (`interceptorRef`, `maxInputSize`, `scanExportedFiles`, and `maxExportedFileSize`) and the export and import limits, `fileExportLimits` and `fileImportLimits` (decision 45). Unlike the other caveats, every session carries one, with or without a grant, so a session with no grant still has a scan and limits for a file it imports. The gateway resolves it at session creation from the platform, the tenant, the environment, and the runtime, the strictest value winning: a scan that any level requires runs, each size and count is the smallest any level sets, and every interceptor any level names runs. A child's content caveat is its parent's, narrowed by the child's own levels, and never widened. Every file that moves between two sessions passes the sender's content caveat when it leaves the sender's workspace and the receiver's when it enters the receiver's, whether `session.create`'s `files`, a file part of `session.send` with or without `materialize`, or `files.import` carries it. 0142's cooldown after a weakened scan covers every such path ("DP-7").

**Root grant inputs.** A root session receives a grant at creation when 0126 resolves that it may delegate: 0126 keeps the availability levels, their order, the rule that a forbid wins, its checks, and the `availability` notice, cut down to deciding whether a session holds a grant (decision 39). The gateway resolves the grant's caveats from these inputs:

| Input | Level | Written by | Contributes |
| :-- | :-- | :-- | :-- |
| `gateway.delegation.*` Helm values | Platform | Operator, at install | A default and a ceiling for each numeric caveat, including the parallelism ceiling §8.3 sets for its Redis reservation script, the platform defaults of `leave` (`transfer`) and `leaveMaxDepth` (1) (decision 32), `gateway.delegation.parentTools.enabled`, `false` by default (decision 23), `gateway.delegation.sessionBudget`, `requiredToDelegate` by default (decision 52), and the default of `maxLeaves`, 4 (decision 54). |
| Tenant delegation settings | Tenant | `tenant-admin`, or `platform-admin` | `policyRef`, a `DelegationPolicy` applied to every session in the tenant, `ceilings`, a maximum for each numeric resource caveat other than the carved budget, whose defaults and ceilings are the session budget settings of 0170 "BG-2", and `sessionBudget`, the tenant's level of `gateway.delegation.sessionBudget` (decision 52). The route is 0126's tenant sub-resource, which waits on 0126's open O10 (decision 26). |
| Security record | Tenant | `security-admin` | Under 0142 SO-2's single writer and weakening rule: the forbid of delegation, the forbid of parent tools, the tenant floor of `leave` and `leaveMaxDepth`, and the tenant's `offerTools` (decision 37), read live at each `session.create`, `session.transfer`, `session.release`, and parent tool call; and SO-5a's approval rules for a child (decision 36). |
| Environment `defaultDelegationPolicy` and `delegationCeilings` | Environment | `tenant-admin`, or `platform-admin` | A policy applied to every session in the environment, a maximum for each numeric resource caveat other than the carved budget, and `sessionBudget`, the environment's level of `gateway.delegation.sessionBudget` (decision 52). §10.6 defines the policy field today. The environment's off switch, `allowDelegation: false`, is an availability level of 0126 "DA-1" rather than a grant input (decisions 50 and 51). |
| Runtime `delegationPolicyRef`, default lease, and ceilings | Runtime and derived runtime | `platform-admin` for a platform runtime, `tenant-admin` for a tenant-derived runtime (0147) | A policy, a default for each numeric caveat, and, separately, a ceiling for it (decision 40). A derived runtime may only narrow its base. |
| `delegationLease` on `POST /v1/sessions` | Session | Client | Values for the numeric caveats and `maxDelegationPolicy`. A value above a ceiling is lowered to the ceiling, and `maxDepth: 0` turns delegation off for the session (decision 26 of 0149, decision 50). |

**Resolution.** This change owns the resolution of every caveat, and TS-3 cites it for `maxDepth` (decision 40). The policy cap is the intersection of every policy the levels name. Each numeric caveat resolves in two steps (decisions 40 and 49):

- **The requested value.** It is the client's value when the client sets one. A client may name a delegation preset of §8.3 (`"delegationLease": "standard"`, or `{ "preset": "standard", "maxDepth": 2 }` with an inline override), whose values become the client's request for the caveats it sets. A caveat the client leaves unset takes the first default that is set among the runtime's default lease, the environment's, the tenant's, and the platform's. A session budget of [`0170-budgets`](../0170-budgets/change.md) resolves the same way, from the settings "BG-2" names. A preset that an operator attaches to a runtime, an environment, or a tenant is that level's default lease.
- **The ceilings.** The requested value is lowered to the smallest ceiling that the platform, the tenant, the environment, and the runtime set. A runtime contributes a default and, separately, a ceiling, so a client may ask for more than the runtime's default and never more than a ceiling.

The numeric caveats are fixed when the grant is created. Lowering a ceiling applies to grants created afterwards, and an existing grant keeps its values. The policy references and the security record are read live at each `session.create`, so a narrowed policy or a new forbid stops new children of running sessions at once (decision 5). **Zero (decision 50).** A ceiling of 0 for `maxDepth`, `maxChildrenTotal`, or `maxParallelChildren` at the platform, the tenant, the environment, or the runtime is refused at write with `400 VALIDATION_ERROR` naming the field, and its message names the availability levels of 0126 "DA-1", which turn delegation off for a tenant, an environment, or a runtime and are read live. A ceiling is fixed at grant creation, leaves the reference tools listed, and refuses with a limit error, so it is no off switch. A client may set `maxDepth: 0` for its own session, and a parent may set it for a child in `session.create`. A session with no depth left, whether set so or at its tree's depth limit, holds no grant, so its availability notice says delegation is off and the reference bridge does not list `start_session` (0126 "DA-2"). [`0149-tenant-scoping-identity`, "TS-3"](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) and 0170 "BG-2" cite this paragraph and state no resolution of their own.

**A session budget for delegation (decision 52).** `gateway.delegation.sessionBudget` decides whether a session must have a session budget of [`0170-budgets`](../0170-budgets/change.md#bg-2-session-budgets) to delegate:

| Value | Effect |
| :-- | :-- |
| `requiredToDelegate`, the default | A session without a session budget runs, and its `session.create` is refused with `422 SESSION_BUDGET_REQUIRED`. |
| `requiredToCreate` | A session creation that would give the session a grant and no session budget is refused with `422 SESSION_BUDGET_REQUIRED`. A child always has a session budget, its carve, so the value bears on roots and on a released session. |
| `optional` | A parent without a session budget creates children that no carve funds, each with the session budget its own defaults and ceilings give it. |

Under either required value, the session budget must hold at least one limit that counts `llm` and one that counts `compute`, which may be one limit that counts both, so a session budget with only a storage limit does not satisfy the requirement and is refused at the same point with `422 SESSION_BUDGET_REQUIRED` (decision 53).

The Helm value sets the platform's level, the tenant delegation settings the tenant's, and the environment's delegation settings the environment's. A lower level may only make the value stricter, in the order `optional`, `requiredToDelegate`, `requiredToCreate`, and a write that loosens it is refused with `400 VALIDATION_ERROR` naming the level that binds. The value in effect is the strictest of the values the levels set, computed at each read, so a level tightened later binds a lower level's stored looser value (decision 53). It is read live at each `session.create` and session creation, as the policy references are. Under either required value DPR-4 holds for every child.

**Policy authoring.** Delegation policies keep the RBAC of §10.2: a `tenant-admin` creates, edits, and deletes the policies of its own tenant, and a `platform-admin` those of every tenant. A tenant administrator therefore controls which runtimes the sessions of its tenant may start as children, without a platform administrator, and cannot widen what a platform runtime's policy or the platform ceilings allow, because every level intersects.

**Approval.** A person's approval of a child is SO-5a's of 0142 (decision 36). SO-5a's rules on `session.create` take `when`, with `targets` or `targetLabels` for the target runtime and `llmCreditsAbove` for the carve's amount in `llmCredits`, as SO-5a of 0142 states them, and the hold applies at `session.create`, on the final runtime at the post-chain point, at a `session.set_limits` raise, and at a lease extension of a carve that crosses the amount, on the amounts SO-5a states it reads (0170 decision 46). The call waits under SO-5a's hold and its timeout, as any held platform call does.

**Where the checks run.** The gateway checks the policy cap, the isolation floor, the remaining depth, and the binding and egress subsets on the final runtime and pool, at the post-chain point of 0141 RT-2, so a `PreRoute` interceptor that moves a child cannot move it past them ("DP-7").

**Audit.** The gateway writes `delegation.grant_created` when it creates a grant, with each caveat's resolved value and the level it came from, in the form of `disabledBy`. `delegation.spawned` names the parent's grant, the policy and its version that admitted the target, and the client name of the adapter connection that sent `session.create`, which shows whether the reference tool set or another extension made the call. `delegation.left` names the session, its mode, its former parent, and the actor, which is the former parent or the root's client. A call to a parent's tool is audited as 0122 CT-8 audits a client tool call, with the parent as the client and no arguments, so erasure under §12.8 removes them (decision 42).

The pod reaches the grant only through the platform tool server: the runtime calls a platform operation, the adapter forwards it naming the slot's session, and the gateway applies the grant of that session. No bearer token for the grant exists, and the `a2a_delegation` child minting of §13.3 retires (decision 2). A later change that lets an actor outside the pod attach a child to a session adds to the operations and events of this change under DPR-12.

The platform operations are generic session operations. Each operation that names a session acts only on the caller's own children, except `files.import`, which names a session that delivered the caller a file part, and `session.self`, which names none. The operations carry no orchestration semantics. They are platform tools that the extension calls (decision 3):

| Operation | Arguments | Result |
| :-- | :-- | :-- |
| `session.create` | `target` (a runtime name), `task` (the first message, as `MessagePart[]`), and optionally: `runtimeOptions` (validated against the target's chosen revision and the policy cap, under 0140); `files` (an export from the caller's workspace under §8.7, under both content caveats); `limits` (each resource caveat, at most what the caller's grant has left); `credentialBindings` and `egressHosts` (within one environment, subsets of what the caller holds, each defaulting to what 0127 "Effective policy" passes); `onParentEnd` (equal to or stricter than the caller's); `questions` (`deliver`, the default, or `forward`, "DP-4"); `tags` (at most 16 keys of 64 bytes and values of 256 bytes); `wakeOn` ("DP-4"); `tools` (tool definitions the caller offers the child, "DP-8"), with `offerTools: allow` for a fenced child (decision 31), and `clientToolTurns` and `clientToolHoldSeconds` for those tools as "Turn rule" and "Hold" in CT-5 of 0122 state them; `maxDepth` (the child's remaining depth, at most the caller's remaining depth less one; 0 makes the child unable to delegate for its life, the permanent per-hop switch of 0126 "DA-6" and its O19); `leave` and `leaveMaxDepth` (equal to or narrower than the caller's, decision 32); and `idempotencyKey` | The child's session ID, the limits granted, and `created: false` when the idempotency key matched an earlier child. |
| `session.send` | `sessionId`, `parts`, `intent` (an intent of 0135 MD-1, `append` included, decision 38), and optionally `idempotencyKey`, as a client's send under 0138 takes one, and `tools`, the parent's tool definitions for the turn the message opens, as a client declares them under 0122. A `session.send` with no parts carries a child's late results under "Late results" in CT-5 of 0122, and is accepted only while late results wait for the parent. A file part travels by reference under MD-26 as 0135 extends it, and a parent may set `materialize: true` on a file part to place the file in its child's workspace through WM-12 when the child's runtime declares `midSessionUpload`, and is refused with `422 MID_SESSION_UPLOAD_UNSUPPORTED` otherwise (0153 "WM-12") (decision 18) | The message's outcome. A repeated key returns the first send's outcome. |
| `session.interrupt` | `sessionId` | Stops the child's open turn without a message, as a client's turn interrupt under 0138 does, on a runtime that declares `interrupt`. |
| `session.messages` | `sessionId`, and optionally `after` and `limit` | The parent's binding of the client's read of a session's history under 0138, which carries what a watch under MD-25 carries, under MD-25's checks and its crossing rule (decision 34). |
| `events.wait` | Optionally `sessionIds`, `mode` (`any` or `all`), and `maxEntries` | MD-22's wait, under its rules for `await_child_sessions` with the paging and cancel rules that 0135 takes on ("DP-7"). Each entry carries the child's state, its tags, and its event. |
| `events.peek` | Optionally `afterSeq` and `maxEntries` | The caller's child events after that sequence number, each with its sequence number, without taking them from the inbox. The call adds no activity under SL-6 (decision 10). |
| `session.get` | `sessionId` | The child's state, its tags, its pending question when it has one, its last end-of-turn message when it is not terminal, its `TaskResult` once terminal, and its usage with the unused part of its carve. |
| `session.list` | Optionally `depth` (default 1) | The caller's descendants to that depth, with each one's parent, state, tags, and whether it has a pending question. Only direct children can be named in the operations that act on a child, and an operation that names any other session answers `404 RESOURCE_NOT_FOUND`, as for a session that does not exist, under the rule 0149 TS-3 states ("The parent is the calling session"). |
| `session.close` | `sessionId` | The close outcome, under SL-7. |
| `files.import` | `sessionId`, the part's `id`, `dest`, and optionally `overwrite` | The import of MD-26, which 0135 extends to a file part any session delivered to the caller (decision 18), under the receiver's content caveat. |
| `questions.answer` | `sessionId`, `questionId`, and `response` | The outcome of the answer, under §9.2 and MD-22. |
| `questions.forward` | `sessionId` and `questionId` | The question passes one hop up, to the caller's own client, under §9.2 and MD-22. |
| `session.set_limits` | `sessionId`, a change to each resource caveat, positive or negative, and optionally `wakeOn`, which is a setting of the caller rather than a caveat | The child's new limits. A raise draws on the caller's unused amount, and a cut returns the child's unused amount to the caller. A raise of the child's budget follows 0170 "BG-8", which refuses it when no carve of the caller funds the child. A raise that crosses an SO-5a condition waits for its approval (decision 36). |
| `runtimes.list` | None | The runtimes that the grant's policy cap allows, with each runtime's `agentInterface`. |
| `session.transfer` | `sessionId` | The child's state after the leave. The child stays in its tree with the root's client as its client, under "Leaving the parent" in "DP-2" (decisions 32, 33, and 46). |
| `session.release` | `sessionId`, and optionally `handoff: true`, which a root alone may set | The released session's ID. The child becomes a root of the same principal outside the tree, under "Leaving the parent" in "DP-2". `handoff: true` ends the caller with the released session as its successor, under 0146's hand-off ("DP-7"). |
| `tools.result` | `sessionId`, `callId`, `content`, and optionally `isError` | The parent's answer to a call of one of its tools, under 0122's blocking interface ("DP-8"). |
| `session.self` | None | The caller's own `lineage`, tags, session `labels`, and a summary of its grant, for an extension on a tool connection, which never receives `session_start`. |

The platform tool list marks each operation as one the reference MCP bridge never lists to a harness, through an attribute that 0139 RP-3 takes on ("DP-7"), so a model sees only the tools of an extension. A call of an operation by a session with no grant is refused with `403 DELEGATION_DISABLED` and `details.disabledBy`, with two exceptions. A session that has children keeps the tools DAR-6 of 0126 names, which map to `events.wait`, `events.peek`, `session.get`, `session.list`, `session.close`, `session.send`, `session.interrupt`, `session.messages`, `session.set_limits` for a cut, `questions.answer`, `questions.forward`, and `tools.result`, and `session.transfer` and `session.release` are not among them. Every session holds `session.self`, and `files.import` for a file part delivered to it, whatever its grant, so a child whose runtime has `allowDelegation: false`, which holds no grant, still reads its own lineage and imports a file its parent sent. `messagingRateLimit` counts what MD-9 counts, each send of the relay of "DP-5" included.

**Idempotency, tags, and lineage.** The gateway stores an idempotency key with the child row under the parent's session, and a repeat of the key by the same parent returns the existing child, whatever state it is in. Tags are stored on the child's session row and delivered to the child in `session_start`, beside `lineage`, the list of runtimes from the root to the child itself, which the gateway records. 0139 takes on both members of `session_start` ("DP-7"), and `session.self` returns them to an extension on a tool connection. A restored parent matches its children to its work through the tags in `session.list`, and an extension passes a trace identifier or a role in the tags instead of the model's prompt. Neither the tags nor the lineage grant anything: a check an extension makes with them binds a child only while the child's own runtime runs that extension.

### DP-2 Parent link

A session created under a grant records `parentSessionId`, `rootSessionId`, and its depth **[design]**. The parent link carries what 0146, 0143, 0149, 0141, 0127, and 0136 already give a delegated child: the parent's principal and `caller_type`, its environment, its routing labels under RT-3, the egress hosts and named bindings `session.create` passes, the parent's placement requirements under `kind: delegate`, tree suspension under SL-4, and the cascade of SL-9 under `onParentEnd`. Each of those changes keeps its rule.

**Labels and tags.** A session's `labels` are the ones its root's client set, carried unchanged to every descendant, so a tree's usage and billing fall under the project or team that started it, and no parent writes them (decision 44). 0141 RT-2's `labels` for `kind: delegate` and 0121's billing envelope follow ("DP-7"). A parent's own bookkeeping goes in tags, which billing, budgets, and routing never read.

**Ending a tree.** `onParentEnd` is a security caveat ordered `close_all` before `let_finish`: a child's grant holds the parent's value or `close_all`. The cascade itself is SL-9's. A force-terminate of any session in a tree closes its descendants at once whatever `onParentEnd` holds. A `tenant-admin` terminates a tree of its own tenant with `POST /v1/admin/sessions/{rootId}/terminate-tree`, which writes the same audit event as a force-terminate and also reaches each session transferred in the tree.

**Leaving the parent.** A session leaves its parent in one of two ways (decisions 32 and 46):

- `session.transfer` keeps the session in its tree. It stays counted in the tree's `maxTreeSize`, inside the root's cascade and `terminate-tree`, and it keeps its limits. The root's client becomes its client, and its former parent's end no longer cascades to it. Its carve moves from the parent's reservation to the root's, and the transfer is refused when the root's session budget cannot reserve it (0170 "BG-8"). A transfer of a child in another environment than its parent meets that environment's admission check under TS-3, as a release does (0149 decision 43).
- `session.release` makes the session a root of the same principal outside the tree. It keeps its security caveats, takes its resource caveats from the root grant inputs as any root does, takes its unused carve as its session budget under the budget objects of 0170, and falls under the tenant's session quotas. A release of a child in another environment than its parent meets that environment's admission check under TS-3 (decision 41). Only `session.release` takes `handoff: true`, and the hand-off with its successor is 0146's ("DP-7").

The security caveat `leave` takes `none`, `transfer`, or `release`, each allowing the modes before it, and `leaveMaxDepth` is the deepest depth at which a session may leave. The depth is the session's depth in the tree it was created in, which its lineage records: a released session and its descendants keep the depth they had in the former tree for this check, so a chain of releases cannot carry a deep subtree out one level at a time. A session created under a released session after the release takes its depth from the original root as well, through its lineage, as the second review of 2026-11-23 found. Both resolve from the root grant inputs, with platform defaults `transfer` and 1 and a tenant floor in the security record, and `session.create` may narrow them for a child. Neither mode is allowed while the governing grant is absent or the security record forbids delegation. The root's client may transfer or release a descendant of its root through REST routes that 0138 takes on ("DP-7"), under the same caveats and the root's grant.

**Leaves per tree (decision 54).** `maxLeaves` is a resource caveat that counts the transfers and releases a whole tree makes, kept at the root as `maxTreeSize` is, so a parent cannot run past `maxParallelChildren` by moving its children out. It resolves under "Resolution" in "DP-1" from the platform default of 4 and the tenant, environment, and runtime ceilings, and a client or a parent may lower it. A leave beyond it, by a parent or by the root's client, is refused with `429 LEAVE_LIMIT_REACHED`, and the child stays where it was. A released session takes a fresh `maxLeaves` from the root grant inputs as any root does, and its own tree counts its leaves.

A leave carries these rules (decision 33):

- The former parent receives a `left` child event naming the mode, as its last event from the session, and events written before it stay in the parent's inbox.
- The session's pending questions to the parent move to the root's client, on the root's event stream with `originSessionId`.
- Its open calls to the parent's tools return an error with the code `CLIENT_CHANGED`, and the bridge withdraws those tools.
- The parent's messages to it that are still pending are dropped, with a `dropped` notice to the parent.
- The user's client tools reach it only as 0122 states for any session.
- It leaves the parent's relays and group chats, and the parent's watch of it ends.
- File parts it sent the parent before the leave stay importable by the parent within their retention.
- It keeps its `labels`, its tags, its lineage with `leftFrom` and the mode, and its own children, which move with it.
- It stops counting toward the parent's `maxParallelChildren`, and it counts once toward its tree's `maxLeaves`, below.
- `wakeOn` and `questions: forward` lapse, because a client of a top-level session receives every event.
- The root's event stream and its webhook subscriptions receive the session event that 0138 takes on for the mode.

### DP-3 Carved budget

A child's creation reserves its budget from its parent's session budget and returns the unused part when the child ends, as 0170 "BG-8" states **[design]**. The budget objects and the mechanics of the reservation are 0170's, and the carve is 0170's reservation for a child (decision 12, "DP-7"). `limits.budget` on `session.create` and the budget part of `session.set_limits` name amounts by the limit keys of the parent's session budget, as 0170 "BG-8" states (0170 decisions 16 to 18). This change keeps DPR-4 and the operations that move a carve.

**Moving budget within a tree.** `session.set_limits` moves an amount of any resource caveat between a parent and a live child: a raise draws on the parent's unused amount, and a cut returns the child's unused amount to the parent. A parent that keeps idle helpers open for follow-up work takes their unused budget back this way, and a parent gives a child that needs more than its estimate the difference without restarting it.

**A child's age.** `perChildMaxAge` is SL-10's timer. This change adds two things to it (decision 13): `session.set_limits` raises a live child's age, at most to what the caller's own grant allows, and the `ended` event of an expiry follows the parent's `wakeOn`, so a parent that leaves the kind `expired` out of it sleeps through expiries while failures still wake it (decision 25).

**Lease extension.** §8.6 stays for the resource caveats, under LU-6 and 0170. An extension that needs a person goes to the root's client, so a parent session never approves its own child's extension, and an extension never raises a security caveat.

**Parallelism.** A child counts toward its parent's `maxParallelChildren` in every state that SL-10 "Caps" of 0146 counts toward a tenant's caps, which "A parent's parallelism" in SL-10 lists, and in `blocked`, which the caps do not count, because a blocked child holds its place in the parent's work.

### DP-4 Events from children

Child events, their kinds, their classes, the `from` member, and the wait are MD-22's, which takes on what "DP-7" lists, and recovery is SL-12's **[design]**. This change adds what a parent controls.

**`wakeOn`.** A parent sets on `session.create`, and changes with `session.set_limits`, the event classes and event kinds of MD-22 that open a turn in it or wake it (decisions 17 and 25). An event outside `wakeOn` is steered into an open turn or waits for its end under MD-22, and otherwise waits in the inbox for the parent's next turn. By default every class except `update` is in `wakeOn`, so a child's progress updates open no turn in its parent. A runtime's or an operator's default lease sets the default. `wakeOn` is a setting of the parent, which never widens what the grant allows, so it is no caveat.

**Questions.** A parent that creates a child with `questions: forward` has each of the child's questions passed one hop up without delivery to it, which suits a programmatic orchestrator that does not answer questions. Otherwise a question reaches the parent as MD-22 states, and the parent answers it with `questions.answer` or passes it up with `questions.forward`.

**Recovery.** No ordering across a tree remains in recovery: each session recovers under 0146, in parallel with the rest of its tree, under the recovery rules that SL-12 takes on (decision 15). After its resends, the restored parent's runtime receives a system message that lists each child's state and tags and names each pending question by its id, with no text the child wrote. A client reads the tree with `GET /v1/sessions/{id}/tree` after `session.resumed`, and the `children_reattached` SSE event leaves §7.2 (decision 47).

**Events on a tool connection.** Child events reach the parent's runtime connection as inbox messages, and reach a tool connection as the result of `events.wait` or `events.peek`. An extension that runs as its own MCP server, as in Example 3, follows events with `events.peek`, which leaves them in the inbox for the runtime and adds no activity of its own under SL-6, and takes them with `events.wait` when it acts on them. A parent with a `running` descendant stays awake under SLR-9 of 0146 in any case, so `events.peek` matters for a parent whose children are idle.

**Later additions.** A later change can add a way for a child to join a parent other than `session.create`, such as an actor outside the pod attaching a child to a session, and the child event that announces it (decision 2). DPR-12 keeps such an addition compatible with an extension written against this change. The reference extension ignores an unknown event kind and an unknown member, as the leniency rules of MD-24 treat a runtime frame, and it handles a child in `session.list` or in an event that it did not create, with tags it did not set. The gateway delivers a child event kind that the parent must act on, and creates a child by any path other than `session.create`, only for a parent whose runtime declares that kind or that path in `capabilities.childEvents`, a list capability that 0139 takes on ("DP-7"). `capabilities.childSessions` stays the boolean of MD-22. An event that needs no action, such as a notice, reaches every parent.

### DP-5 Reference extension

The reference extension implements the tool set of MD-23, under the names of "Names" below, over the operations of DP-1 and ships in the runtime SDKs and the reference MCP bridge **[design]**. It keeps MD-22's rendering rule, under which the runtime shows its model the sender of each child event, and `capabilities.childSessions` stays the runtime's declaration that it does. The extension owns `discover_agents`, which it answers from `runtimes.list`. An operator who wants other orchestration, such as a fixed fan-out or a workflow engine, writes another extension over the same operations.

The extension consists of a library and a tool set **[design]**:

- **A library.** Each runtime SDK gains a `sessions` module whose client wraps the operations of DP-1, such as `Sessions.create` for `session.create`. A runtime that orchestrates in its own code calls the library and offers its model no session tools. The library also holds a relay, which forwards a child's events to another child with `session.send` by rules the parent's code sets, with no model call, and a `ToolSet`, which runs the handlers of the tools a parent offers its children and answers each call with `tools.result`.
- **A tool set.** The reference MCP bridge serves the tools below over the library and lists them in its `platform` server. It lists `start_session` and `discover_agents` when the session's availability under 0126 allows delegation. It lists the tools that manage children, and the tools that answer or forward a child's question or answer its tool call, while the session has a child, which it learns from `session.list` when it starts and after each `availability_changed`. It lists `transfer_session` and `release_session` when `leave` allows them, and `import_file` when a message or a child event delivers the session a file part. The bridge's option `--session-tools` takes `reference`, the default, or `none`, and its option `--session-defaults` names a file of default `session.create` arguments for each target, such as empty `credentialBindings` for a runtime that must receive none, which the model cannot override. A runtime author who ships another tool set passes `--session-tools none` and adds the other MCP server to the harness configuration.

**Names.** The tools and the operations are named for what they act on, a session, an event, a question, a file, a tool call, or a runtime, rather than for the caller's relation to the session (decision 16). The operations take the namespaces `session.*`, `events.*`, `questions.*`, `files.*`, `tools.*`, and `runtimes.*`. `send_update` and `ask_question` keep their names, because they address the session's own client.

| MD-23 name | Reference tool | Operation |
| :-- | :-- | :-- |
| `start_child_session` | `start_session` | `session.create` |
| `message_child_session` | `send_message` | `session.send` |
| None | `interrupt_session` | `session.interrupt` |
| `await_child_sessions` | `await_sessions` | `events.wait` |
| `list_child_sessions` | `list_sessions` | `session.list` and `session.get` |
| `close_child_session` | `close_session` | `session.close` |
| None | `transfer_session` | `session.transfer` |
| None | `release_session` | `session.release` |
| None | `answer_tool_call` | `tools.result` |
| None | None, the library alone | `session.self` |
| `import_child_session_file` | `import_file` | `files.import` |
| None | `answer_question` | `questions.answer` |
| None | `forward_question` | `questions.forward` |
| `discover_agents` | `discover_agents` | `runtimes.list` |

The MCP adapter's client-facing tools of 0138 include `send_message` and `close_session`. Those run on a client's connection to the adapter, and the reference tools run inside a pod under the bridge's `platform` server, whose prefix a harness shows, so the two never meet on one connection.

`list_sessions` takes MD-23's depth option through `session.list`. `start_session` passes `runtimeOptions` and `tags` through, and sets an idempotency key derived from the MCP tool call, so a call that the harness or the bridge retries creates no second child. A model that decides again, after a restore, to start a child it already started makes a new call, and the reference tool set gives it no protection against that. An orchestrator that needs it sets its own key from its work item, as Example 2 does.

### DP-6 Disposition of each setting

| Setting | Source | Disposition [design] |
| :-- | :-- | :-- |
| `maxDepth` | §8.2, §8.3 | Primitive, as a security caveat. |
| `maxChildrenTotal`, `maxParallelChildren`, `maxTreeSize`, `perChildMaxAge` | §8.2, §8.3 | Primitive, as resource caveats, with tenant, environment, and runtime ceilings (DP-1). `maxLeaves` joins them (decision 54). |
| `fileExportLimits`, and the import limits of MD-26 | §8.3, §8.7 | Primitive, in the content caveat that every session carries (DP-1, decision 45). |
| `maxTokenBudget` and the reservation model | §8.3, 0133 LU-6 | Primitive, as the carved budget over the budget objects of 0170 (DP-3, decision 12). |
| Lease extension and its approval modes and layering | §8.6, 0133 LU-6 | Primitive, for the resource caveats only (DP-3). |
| `onParentEnd` (formerly `cascadeOnFailure`) | §8.10, 0146 SL-9 | Primitive, as a security caveat that a child may only make stricter (DP-2). The cascade stays SL-9's. |
| `runtimeOptions` on the delegation `TaskSpec` | 0140 | Primitive, as an argument of `session.create`. |
| Elicitation forwarding through the virtual child interface | §8.2, §9.2 | Replaced by the `question` child event of MD-22 with `questions.answer` and `questions.forward` (DP-4). |
| `children_reattached` and the re-await protocol | §7.2, §8.2, §8.10 | Replaced by a system message to the restored parent's runtime and by SL-12's resends. The SSE event leaves §7.2, and a client reads the tree route (decision 47). |
| `minIsolationProfile` and isolation monotonicity | §8.3 | Primitive, as a security caveat. |
| `delegationPolicyRef`, `maxDelegationPolicy`, derived-runtime policy | §8.3 | Primitive, as the policy cap, checked at 0141's post-chain point, which also closes F-8.3.14. |
| Environment and sharing paths | 0149 TS-3 | Primitive, as a security caveat (DP-2). TS-3 owns crossing (decision 41). |
| The cascade when a parent ends, and orphan cleanup | §8.10, 0146 SL-9 | SL-9's, with `onParentEnd` as a caveat and a tree terminate for a `tenant-admin` (DP-2). |
| Child event delivery and the wait, with its modes | 0135 MD-22, 0146 SL-12 | MD-22's and SL-12's, with `wakeOn` and `events.peek` added (DP-4). |
| Approval of a child by a person | 0142 SO-5a | SO-5a's, with conditions on the target runtime and an amount in `llmCredits` (decision 36). |
| `contentPolicy.maxInputSize` and `interceptorRef` at `PreDelegation` | §8.3 | Primitive, in the content caveat (decision 45). `PreDelegation` folds into the `PreRoute` chain of RT-2 for `kind: delegate`, so one chain sees every creation (decision 4). |
| `scanExportedFiles`, `maxExportedFileSize`, and the weakening cooldown on export scanning | §8.3, §8.7 | Primitive, in the content caveat, applied on every transfer path, with 0142's cooldown covering each path (decisions 4 and 45). |
| `messagingRateLimit` | §8.3 | Primitive, as a resource caveat that counts what MD-9 counts. |
| `userCredentialBindings`, named bindings, and `egress.allowedHosts` passed to a child | 0151, 0127 | Primitive, as security caveats. Within one environment `session.create` narrows each, and by default passes what 0127 "Effective policy" passes. |
| Placement requirements of a child | 0136 | Primitive, through the parent link (DP-2). |
| `allowSelfRecursion` at three layers and `gateway.cycleDetection.mode` | §8.2, §8.3 | Dropped. `maxDepth` and the budgets bound recursion, and an orchestrator that wants cycle avoidance applies it in the extension with the `lineage` the gateway delivers in `session_start` and `session.self` (decision 5). |
| `treeVisibility` and its check against `messagingScope` | §8.3 | Dropped. The operations of DP-1 reach the caller's own children only, and MD-9 of 0135 already removes the `siblings` messaging scope. |
| `perChildRetryBudget` | §8.3 | Moved to the extension. An orchestrator retries a failed child by creating another. |
| `delegationPresets` | §8.3 | Kept as operator-defined default leases on a runtime, a tenant, or an environment, which feed the root grant inputs (DP-1). |
| `defaultDelegationFraction` | §8.3 | Kept, as 0170 "BG-8" states it. |
| `delegation.budgetKeyTTLSeconds`, the TTL re-arming of the tree's budget keys, and `BUDGET_KEYS_EXPIRED` | §8.3, 0146 SL-10 | Dropped. A session budget's keys carry no TTL under 0170 "BG-9" (0170 decision 43). |
| `snapshotPolicyAtLease` | §8.3 | Dropped. The policy cap is read live at each `session.create` (decision 5). |
| `maxTreeMemoryBytes` and the virtual child interface memory model | §8.2 | Dropped. The gateway's internal memory is not a lease value, and the virtual MCP child interface goes with the gateway-hosted tools. |
| `approvalMode` | §8.4 | Dropped. 0126 removes it as an argument, and its `approval` value is reserved and unimplemented. |
| Bottom-up tree recovery and its timeouts | §8.10 | Dropped. Each session recovers under 0146 (decision 15). |
| `credentialPropagation` | §8.3 | Already removed by 0133 LU-4. |
| `experimentContext` | §8.3 | Already replaced by routing labels under 0141 RT-3. |
| `allowedExternalEndpoints` and the `connector` target type | §8.3 | Already removed by 0124 CR-1. |
| `tracingContext` and `set_tracing_context` | §8.2, §8.3 | Replaced by tags. 0135 already removes the tool, and an extension passes tracing identifiers as tags, which reach the child's runtime in `session_start` and never the model's prompt (DP-1). |
| Availability levels of 0126 | 0126 DA-1 to DA-6 | Kept in 0126, which decides whether a session holds a grant (decision 39). |
| Environment `defaultDelegationPolicy` | §10.6, 0149 TS-3 | Primitive, as a root grant input applied at every `session.create` (DP-1). |

### DP-7 What each change takes on

Each row states what the change takes on, and what this change keeps and cites (decision 35). The owner settled this design on 2026-11-23, and each change took on its row the same day.

| Change | What it takes on [design] | What 0166 keeps |
| :-- | :-- | :-- |
| [0122](../0122-client-tools/change.md) | The blocking interface for tools a client offers, for every client (decisions 23, 24, and 48): tools declared on a message in `conversation.clientTools` and `conversation.toolChoice`; a `tool_call` frame naming `server: "client"`, with a `group` and its size for calls made at one model step (every call is independent, under O23 (i) of 0122 and D14 of 0139); a `tool_call` event to the client, on a REST client's stream and to a parent as a child event; the hold under `clientToolHoldSeconds`, 300 by default with a tenant ceiling, after which the call is withdrawn with `tool_call_withdrawn` and the session may go `dormant`; a late result as `toolResults` on the next message under CT-7's history rules (DPR-13); the provider mapping of a group to one provider response with results from the caller's next request; and the answer route with 0138. T7 and CT-9 restated as "a child never receives its parent's client's tools". T16 becomes the client's choice for the session, its own turns by default or every turn, a provider adapter always its own turns, and every `tool_call` event carries what started the turn and which sessions' content entered it. CT-8's audit covers a parent tool's call, with the parent as the client and no arguments (decision 42). Retires T1's turn-ending exchange, CT-1's `clientToolCalls` on the end-of-turn message, CT-5's pending set and its hold rule for inbox deliveries, CT-7's copy of a pending set on a derive, and CT-10's rule that the SDKs add no blocking helper. T3's clause on the reference runtimes lapses under O22 of 0122. | What is specific to a parent: `tools` on `session.create` and `session.send`, `tools.result`, `offerTools`, the fenced default, `gateway.delegation.parentTools.enabled`, and `toolCallRateLimit` ("DP-8"). |
| [0126](../0126-delegation-availability/change.md) | Availability, cut down to deciding whether a session holds a grant (decision 39): the levels, their order, the rule that a forbid wins, the room and capability checks, the failed-read rule, the `availability` notice, the refusal `DELEGATION_DISABLED` with `disabledBy`, DAR-6 mapped to the operations of "DP-1", and the tenant delegation settings route of O10 with `policyRef` and `ceilings` (decision 26). Retires the tool-list rules for child tools, which the extension owns. | The root grant inputs other than availability, and the resolution of every caveat ("DP-1"). |
| [0135](../0135-message-delivery/change.md#md-22-parents-and-children) | **MD-22:** the event classes `update`, `result`, `question`, `tool_call`, and `status`, with `from: { "kind": "agent", "id": <child> }` on a child's words and `{ "kind": "system", "about": <child> }` on a `status` event, whose child-written text travels in `childContent` and passes the parent's content checks (DPR-14); the kinds `question`, `question_withdrawn`, `tool_call`, `tool_call_withdrawn`, and `left`; delivery by the parent's `wakeOn`; the wait's paging with `maxEntries` and the return to `pending` of events a cancelled or undelivered wait took; and questions as child events, whose record holds the hop where each sits, which `questions.forward` moves under both delivery locks, whose §9.2 per-hop timeout does not run at a parent, where `maxElicitationWait` bounds it, and which an expiry under §9.2 item 6 withdraws with `question_withdrawn` or removes when still pending. MD-23's tools leave the gateway, under the names of "DP-5". **MD-1 and MD-14:** the `append` intent, ready for every sender (decision 38, DPR-15). **MD-3:** events a wait takes are `delivered` with the wait's call id, `consumed` on the adapter's acknowledgement, and `pending` again when a pod loss or a takeover finds none. **MD-25:** the crossing rule for a child in another environment (decision 34, DPR-19). **MD-26:** an import of a file part any session delivered to the importer, and only such a part (decision 18, DPR-17). MD-9 is unchanged (decision 30). | `wakeOn`, `events.peek`, `questions: forward`, and the operations' names ("DP-4" and "DP-5"). |
| [0138](../0138-rest-only-client-api/change.md) | RO-2 rows R20 to R25, citing their owners for semantics: `append` on a send (R20); the transfer and release routes of the root's client, `POST /v1/sessions/{id}/transfer` and `POST /v1/sessions/{id}/release` (R21); the answer route `POST /v1/sessions/{id}/tool-calls/{callId}/result`, `clientToolTurns`, `clientToolHoldSeconds`, `openToolCalls`, and the `tool_call` and `tool_call_withdrawn` events (R22); the events `child_transferred`, `child_released`, and `left` with their webhook kinds (R23); `successorSessionId`, the `handoff` event, and `409 SESSION_HANDED_OFF` (R24); `CLIENT_CHANGED`; and the history read on `GET /v1/sessions/{id}/transcript`, which `session.messages` binds for a parent (R25). ROR-14: the MCP and A2A adapters follow a hand-off. | The tree terminate route and the semantics of each leave ("DP-2"). |
| [0139](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames) | RP-3: `server: "client"` and `group` on `tool_call`, and `internal: true` on a platform tool the bridge never lists (RPR-24). RP-2: `capabilities.childEvents`, which requires `childSessions` and has no defined value in v1, for the event kinds and creation paths beyond this change's that a runtime handles. RP-4: `lineage` and `tags` on `session_start`, which no runtime frame changes (RPR-25); `toolServers` gains `client`; `availability_changed` also fires when a client's tool list changes; and `availability.delegation` means that the session holds a grant, following 0126. RP-5: the reference bridge serves this change's session tools under `--session-tools` and `--session-defaults` and the `client` server. | `session.self` and the extension's listing rules ("DP-1" and "DP-5"). |
| [0141](../0141-runtime-routing/change.md#post-chain-point) | RT-2's post-chain point runs, for `kind: delegate`, the policy cap, the isolation floor, the remaining depth, and the binding and egress subsets on the final runtime and pool, replacing its row for the §8.2 delegation gates, whose cycle detection leaves (decision 5). `PreDelegation` folds into the `PreRoute` chain, the payload's `input` and `runtime_options` come from `session.create`, and RT-2's `labels` for `kind: delegate` carry the root's `labels` (decision 44). 0166.S1 depends on 0141.S1. | What the checks are ("DP-1"). |
| [0142](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | **SO-2:** record fields for the forbid of parent tools, the tenant floor of `leave` and `leaveMaxDepth`, and `offerTools`, under its single writer and weakening rule (decision 37), and the cooldown after a weakened scan covering every transfer path (decision 45). **SO-5a:** a condition on the target runtime and one above a credit amount on `session.create`, held at `session.create`, at a `session.set_limits` raise (decision 36), at a granted extension of a carve, at a `session.release`, and at an extension of a released session (0142 decisions 46 and 47); `approvals.parentTools`, by name or for all, held before a `tool_call` reaches the parent (decision 31); rules name `session.create` in place of `start_child_session`, and a record naming `start_session` is refused with `400 VALIDATION_ERROR`. **Enforcement:** for each guardrail, the operations it refuses, among them `session.create`, `session.transfer`, `session.release`, and a parent tool's call, and its effect on a call in flight; the `crossEnvironmentSharing` row names `session.create` in place of the next `start_child_session`. | The grant inputs those fields feed ("DP-1"). |
| [0144](../0144-session-derive/change.md) | A derive from a parent with children delivers a system message in the derived session's first delivery saying that the children did not carry over (decision 11), and a derive copies no open client tool call, which retires the pending-set copy and its refusal. | Nothing further. |
| [0146](../0146-session-lifecycle/change.md#sl-12-recovery-of-child-events) | **SL-12:** the recovery rules of decision 15: a covered child that commits out of `resume_pending` or `resuming` into a state other than `running` answers a pending wait, and one in `awaiting_client_action` counts as answered when the wait is called; the resends of each question still pending, with its original id and only to the hop where it sits, of an `awaiting_client_action` event while the child stays in that state, of an open tool call, and of the events a wait took; and the system message that replaces `children_reattached`, whose SSE event leaves §7.2 (decision 47). The question's hop record, its timeout at a parent, and its withdrawal are 0135's (MD-22, MDR-64). **SL-7 and SL-9:** the hand-off: a root that releases a child with `handoff: true` ends with `successorSessionId`, a message sent to it is refused naming the successor, and its event stream ends with a `handoff` event (decision 21, SLR-30), which lands in 0146.S4 "Hand-off and leaving a parent", while 0166.S1 lands `session.transfer` and `session.release`; a force-terminate closes a tree whatever `onParentEnd` holds. **SL-10:** "A parent's parallelism" holds the list of states the parallelism count shares with the caps. **SL-8:** the parent-state table follows `wakeOn`. **SL-6:** `events.peek` adds no activity, and an open client tool call counts until its hold passes. | `onParentEnd` as a caveat, the tree terminate, the raise of `perChildMaxAge`, and the parallelism count ("DP-2" and "DP-3"). |
| [0149](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) | **TS-3:** crossing for every operation (decision 41): a crossing child receives what the target environment's policy gives, a parent's tools are refused across environments, a release or a transfer of a crossed child meets the target's admission check (0149 decision 43), and files and the read and watch of a crossed child follow "Model"; its resolution of `maxDepth` cites "DP-1" (decision 40); its delegation gate becomes the environment caveat, decision 26 a client input to the grant, its crossing check runs at `session.create`, and its allow-list of the platform tool server admits the operations of "DP-1"; the environment's `defaultDelegationPolicy` applies at every `session.create`, and the environment gains `delegationCeilings`. | DPR-3 within one environment. |
| [0151](../0151-user-credential-delivery/change.md) | `userCredentialBindings` move from the lease to the grant, and the built-in interceptor scans at the `PreRoute` phase for `kind: delegate` in place of `PreDelegation`. | The binding subset within one environment ("DP-1"). |
| [0153](../0153-workspace-materialization/change.md#wm-12-mid-session-uploads-and-child-file-imports) | WM-12's mid-session overlay takes a parent's `materialize: true` file part, whose bytes come from the parent's workspace export under §8.7, gated on the child runtime's `midSessionUpload`, under both content caveats (decisions 18 and 45). | Nothing further. |
| [`0170-budgets`](../0170-budgets/change.md) | Budget objects, the carve as a reservation from the parent's session budget, with every limit carved and the child's share as an object of the child's session funded by it (0170 decisions 24 and 55), the root client's change of a transferred session's budget (0170 decision 52), and lease extension under LU-6 and §8.6 (decision 12). 0170 joins the v1 program with its own edit to the program (decision 43). | DPR-4 and `session.set_limits` ("DP-3"). |
| [0133](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets) | LU-6's tree scope gives way to 0170's budget objects. | Nothing further. |
| [0121](../0121-billing-resource-attribution/change.md) | The billing envelope carries `root_session_id`, and its `labels` are the root's labels (decision 44). | Nothing further. |
| [0127](../0127-egress-and-git/change.md#effective-policy) | None. Within one environment what a child inherits stays the default, and `session.create` may narrow it. | The subsets ("DP-1"). |
| [0136](../0136-placement/change.md) | None. A child is created with `kind: delegate`, so its parent's placement requirements apply. | Nothing further. |
| [0140](../0140-runtime-revisions/change.md#rv-1-revisions) | `runtimeOptions` moves from the delegation `TaskSpec` to `session.create`, with the same validation. | Nothing further. |
| [0143](../0143-service-account-credentials/change.md) | None. A child's principal and `caller_type` follow from the parent link. | Nothing further. |
| [0147](../0147-tenant-derived-runtimes/change.md#fields) | None. A tenant-derived runtime's `delegationPolicyRef`, defaults, and ceilings are root grant inputs. | Nothing further. |
| §13.3 token exchange | The `a2a_delegation` child minting retires (decision 2). | Nothing further. |

### DP-8 Parity with a client

Under MD-22 a child cannot tell a parent from a person: its parent is its client, and MD-22's table of what a client and a parent each do holds unchanged **[design]**. This section lists the rows this change adds or changes.

| A client of a top-level session | A parent | Status |
| :-- | :-- | :-- |
| Sends idempotently (0138). | `session.send` with `idempotencyKey`. | Same. |
| Interrupts the open turn without a message (0138). | `session.interrupt`. | Same. |
| Reads the session's history (0138). | `session.messages`. | Same, under MD-25's checks and crossing rule (decision 34). |
| Reads the session's usage. | `session.get`. | Same, and for a child in another environment the totals alone, as 0135 "MD-25" states. |
| Attaches to the event stream. | Its runtime watches the child's stream (MD-25). | Same for a runtime. An extension on a tool connection follows events with `events.peek` and receives no streamed parts. |
| Uploads files to the running session (WM-12). | A file part with `materialize: true` on `session.send`. | Same (decision 18). |
| Sends context that starts no turn. | `session.send` with the `append` intent. | Same, under MD-1 (decision 38). |
| Offers its own tools to the session (0122). | `tools` on `session.create` and `session.send`. | Same interface, under 0122, behind the parent's switches (decision 23). |
| Takes a sub-agent over directly. | Transfers or releases a child. | The root's client transfers or releases a descendant through a REST route, and a parent its child with an operation, under the same caveats (decisions 32 and 33). |
| Derives a session from it (0144). | None. | After v1 (decision 14). |

**From the session to the client.** A child uses the same frames and tools toward its parent as toward a person, under MD-22. A person reads a progress update for free, and so does a parent by default, because the `update` class is outside the default `wakeOn` ("DP-4").

**Parent tools.** A parent offers its children tools through 0122's interface, and this change adds what concerns a parent's authority (decisions 23, 31, 37, 41, and 42). A parent's handler runs with the parent's credentials, egress, workspace, and grant, so a child steers actions that carry the parent's authority. Parent tools are off unless `gateway.delegation.parentTools.enabled`, which is `false` by default, the security record, `offerTools`, the child's revision, and the call all allow them, and they are refused for a child in another environment than its parent. A fenced child receives none unless its `session.create` sets `offerTools: allow` within the grant. A security administrator can require a person's approval before a call reaches the parent through SO-5a's `approvals.parentTools`. Each call counts against the child's `toolCallRateLimit`, a result does not count against the parent's `messagingRateLimit`, and the call is audited as 0122 CT-8 audits a client tool call. The SDK library's `ToolSet` runs the handlers in code, and a model-driven parent answers with the reference tool `answer_tool_call`. `questions.forward` refuses a tool call. Delivery to the parent is at least once, so a handler keys its side effects on `callId`. A parent that ends answers its open calls with an error, as a leave does with `CLIENT_CHANGED`. A child never receives its parent's client's tools, and whether a client's tools are callable in a turn another session opened is the client's choice under 0122 (decision 48).

**Between children.** Children message each other through their parent, so every message runs along one edge of the tree and MD-9 of 0135 holds as written (decision 30). A child's event reaches the parent, and the parent sends it on with `session.send`. The SDK library's relay does this by rules the parent's code sets, such as "forward each `result` of the `researcher` child to the `writer` child", with no model call, and each relay send counts under MD-9. A group chat is the parent acting as its manager: it holds the transcript, picks the next speaker, and sends that member what it missed. A parent with a running child stays awake under SLR-9 of 0146, so a relay costs no restore.

## Worked examples

Each example states what the operator configures, what the runtime author ships, what the client sends, and what happens at each step. Every example is **[design]**. The examples follow the owner's decisions in `decisions.md`. They use field names from other draft changes where those changes define them: `allowDelegation` from 0126 "DA-1", `capabilities.childSessions` from 0135 "MD-22", the security record from 0142 "SO-2", `credentialBindings` from 0151, the session `budget` and `sessionBudgetCeilings` from 0170 "BG-2", and service accounts from 0143. The code is illustrative. It shows which calls an author makes and does not fix an SDK interface.

In every example, the gateway enforces the caveats of the session grant whatever the runtime's code does. The runtime's code decides only how it uses its children.

### Example 1: Sub-agents with the reference tool set

A coding agent hands review and test work to helper sessions, and its model decides when. This is the default path, and it reproduces what built-in delegation provides today.

**Operator.** The operator registers a policy that admits the helper runtimes, an orchestrating runtime that may delegate, and helpers that may not:

```yaml
# POST /v1/admin/delegation-policies
name: coding-helpers
rules:
  - target:
      matchLabels: { role: coding-helper }
      types: [agent]
    allow: true
```

```yaml
# Runtime resources
name: claude-code
image: registry.acme.com/claude-code:2026.11
isolationProfile: sandboxed
capabilities:
  interaction: multi_turn
  childSessions: true            # the runtime shows its model each child event's sender
allowDelegation: true
delegationPolicyRef: coding-helpers
labels: { team: platform }
---
name: code-reviewer
baseRuntime: claude-code
allowDelegation: false           # restrict-only on a derived runtime
workspaceDefaults:
  files:
    - path: CLAUDE.md
      content: "Review the diff under review/ and reply with findings."
labels: { role: coding-helper }
---
name: test-runner
baseRuntime: claude-code
allowDelegation: false
labels: { role: coding-helper }
```

**Runtime author.** The `claude-code` image's wrapper writes the harness's MCP configuration with the reference bridge and its default tool set, as RP-5 states:

```json
{ "mcpServers": { "lenny": { "command": "lenny-mcp-bridge", "args": ["--server", "platform"] } } }
```

The author writes no orchestration code.

**Client.**

```http
POST /v1/sessions
{
  "runtime": "claude-code",
  "labels": { "project": "checkout" },
  "delegationLease": { "maxDepth": 1, "maxChildrenTotal": 4, "maxParallelChildren": 2 },
  "budget": { "limits": { "models": { "meters": ["llm"], "amount": 400000, "unit": "llmCredits" } } }
}
```

**What happens.**

1. The gateway resolves the root grant inputs of DP-1: availability (`allowDelegation: true` on the runtime), the policy cap (`coding-helpers`, intersected with any tenant or environment policy), and the client's values lowered to any ceiling. It creates the grant with depth 1, 4 children, 2 in parallel, a session budget of 400,000 LLM credits, the isolation floor `sandboxed`, and the session's environment, and writes `delegation.grant_created`.
2. `session_start` carries `availability.delegation: true`. The bridge lists `start_session`, `discover_agents`, and, once a child exists, the tools that manage children and answer their questions.
3. The model calls `start_session` with `target: "code-reviewer"`, a task, and `runtimeOptions: { "model": "haiku" }`. The bridge exports `review/**` and calls `session.create`. The gateway checks the grant, admits `code-reviewer` because its label matches `coding-helpers`, validates the options against the revision's schema, checks that its pool is at least `sandboxed`, carves the child's budget from the root's, and creates the child with `parentSessionId` set to the root and the root's label `project: checkout`, which no parent changes. The child's remaining depth is 0, so its grant allows no child.
4. The reviewer is unsure which base branch to compare against and calls `ask_question`. The root's bridge receives a `question` child event and shows it to the model, which answers with `answer_question`. The bridge calls `questions.answer`, and the reviewer continues.
5. The child ends its turn with its findings. The gateway delivers an `end_of_turn` child event to the root's inbox, or to a pending `await_sessions`, which the bridge maps to `events.wait`. The child's usage appears under `project: checkout` in `GET /v1/usage`.
6. The root's client closes the root. Under `onParentEnd: close_all`, the gateway closes the child.

### Example 2: Programmatic fan-out with no model-facing tools

A repository audit splits a monorepo by top-level directory, audits each directory in its own session, and merges the reports. The orchestration is fixed code, and no model decides it. The program survives a restore of its pod.

**Operator.**

```yaml
name: repo-audit
image: registry.acme.com/repo-audit:1.4
isolationProfile: sandboxed
capabilities:
  interaction: one_shot
  childSessions: true            # the code reads each child event's sender
allowDelegation: true
delegationPolicyRef: audit-workers   # admits label role: dir-auditor only
---
name: dir-auditor
baseRuntime: claude-code
allowDelegation: false
labels: { role: dir-auditor }
```

**Runtime author.** The `repo-audit` runtime is a Python program that uses the SDK's `sessions` library and runs no MCP bridge. It keeps no state that a restore can lose: each child carries its directory in a tag, each creation carries an idempotency key, and the finished reports are files in the workspace.

```python
from collections import Counter
from lenny_runtime import run, Reply
from lenny_runtime.sessions import Sessions, TERMINAL

MAX_IN_FLIGHT = 8   # orchestration policy, chosen by the author
MAX_ATTEMPTS = 2    # one retry

class Audit:
    def on_message(self, msg, tools):
        sessions = Sessions(tools)
        dirs = top_level_dirs("/workspace/current")
        # Rebuild the run from the gateway and the workspace, so a restore resumes it.
        children = sessions.list()
        attempts = Counter(c.tags["dir"] for c in children)
        running = {c.tags["dir"]: c.session_id for c in children if c.state not in TERMINAL}
        finished = {d for d in dirs
                    if exists(f"reports/{d}.md") or (attempts[d] >= MAX_ATTEMPTS and d not in running)}
        while len(finished) < len(dirs):
            for d in dirs:
                if d in finished or d in running or len(running) >= MAX_IN_FLIGHT:
                    continue
                attempts[d] += 1
                c = sessions.create(
                    target="dir-auditor",
                    task=[{"type": "text", "inline": f"Audit {d}/ and write report.md."}],
                    files=[{"glob": f"{d}/**", "destPrefix": "/"}],
                    limits={"maxChildrenTotal": 0, "budget": {"models": 50000}},
                    questions="forward",               # this program answers no questions
                    tags={"dir": d},
                    idempotency_key=f"{d}#{attempts[d]}",
                )
                running[d] = c.session_id
            for e in sessions.wait(mode="any", max_entries=MAX_IN_FLIGHT):
                d = e.tags["dir"]
                if e.state in ("idle", "completed"):  # the auditor answered
                    sessions.import_file(e.session_id, "report.md", f"reports/{d}.md")
                    sessions.close(e.session_id)
                    finished.add(d)
                elif e.state in TERMINAL and attempts[d] >= MAX_ATTEMPTS:
                    finished.add(d)                    # failed twice; reported as missing
                if e.state in TERMINAL or d in finished:
                    running.pop(d, None)
        return Reply(text=merge(dirs, "reports/"))

run(Audit())
```

**Client.**

```http
POST /v1/sessions
{
  "runtime": "repo-audit",
  "delegationLease": { "maxDepth": 1, "maxChildrenTotal": 60, "maxParallelChildren": 8 },
  "budget": { "limits": { "models": { "meters": ["llm"], "amount": 3000000, "unit": "llmCredits" } } }
}
```

**What happens.**

1. Each `sessions.create` is a `session.create` on the platform tool server. The gateway applies the grant to each call: the 61st child is refused because the child count is spent, a ninth child that is `running` or `idle` is refused because the parallelism is spent, and a child whose request asks for more than the credits left is refused. `limits.maxChildrenTotal: 0` gives each auditor no room to delegate. The program waits with `events.wait`, so the default `wakeOn` suits it: an auditor's progress updates open no turn in the parent.
2. A `dir-auditor` is a `multi_turn` runtime, so it ends its turn with its answer and stays `idle`. The program imports the report and closes the child, which frees its parallelism and returns its unused credits.
3. The node that runs the parent fails. The parent restores on a new pod, receives SL-3's resume message, and runs `on_message` again. `sessions.list()` returns every child with its `dir` tag, so the program counts attempts, finds the children still running, and skips the directories whose reports exist. A `sessions.create` that the old process sent and the gateway committed before the failure is repeated with the same idempotency key and returns the same child.
4. The retry, the in-flight limit of 8, and the merge are the author's code. Under built-in delegation today, the same program would call `lenny/delegate_task` and `lenny/await_children`, would have no idempotency key, and would get the gateway's cycle detection and `perChildRetryBudget`, which this runtime does not need.

### Example 3: An operator's own tool set with roles, cycle avoidance, retry, and tracing

A platform team wants its agents to delegate by role (`planner`, `coder`, or `tester`) rather than by runtime name, to refuse a role that already ran in the same branch of the tree, to retry a failed child once, and to carry a trace identifier to every child. These are orchestration policy, so the team writes them as its own extension and replaces the reference tool set.

**Operator.**

```yaml
# POST /v1/admin/delegation-policies
name: acme-roles
rules:
  - target:
      matchLabels: { suite: acme-roles }
      types: [agent]
    allow: true
```

```yaml
name: acme-planner
baseRuntime: claude-code
allowDelegation: true
delegationPolicyRef: acme-roles
labels: { suite: acme-roles }
---
name: acme-coder
baseRuntime: claude-code
allowDelegation: true
delegationPolicyRef: acme-roles
labels: { suite: acme-roles }
---
name: acme-tester
baseRuntime: claude-code
allowDelegation: false
labels: { suite: acme-roles }
```

**Runtime author.** The team ships `acme-orchestrator`, a stdio MCP server built with the TypeScript runtime SDK, in the base image. Its `Sessions.dial()` opens a tool connection to the adapter as `lenny-mcp-bridge` does under RP-5, so it holds no credential. It reads `lineage` and its tags with `session.self`, which returns what the gateway wrote in `session_start`, because a tool connection never receives that frame, so no ancestor's file can change them:

```ts
import { Sessions } from "@lennylabs/runtime-sdk/sessions";

const ROLES = { planner: "acme-planner", coder: "acme-coder", tester: "acme-tester" };
const sessions = await Sessions.dial();
const { lineage, tags } = await sessions.self();       // session.self
const traceId = tags.traceId ?? newTraceId();

server.tool("assign", { role: "string", brief: "string" }, async ({ role, brief }, call) => {
  const target = ROLES[role];
  if (lineage.includes(target)) return toolError(`${role} already ran in this branch`);
  const task = [{ type: "text", inline: brief }];
  const create = (attempt) => sessions.create({
    target, task,
    tags: { traceId, role },
    idempotencyKey: `${call.id}#${attempt}`,
  });
  let child = await create(1);
  let [result] = await sessions.wait({ sessionIds: [child.sessionId], mode: "all" });
  if (result.state === "failed") {                       // retry once
    child = await create(2);
    [result] = await sessions.wait({ sessionIds: [child.sessionId], mode: "all" });
  }
  return toolResult(result);
});
```

The wrapper turns off the reference tool set and adds the team's server:

```json
{
  "mcpServers": {
    "lenny": { "command": "lenny-mcp-bridge", "args": ["--server", "platform", "--session-tools", "none"] },
    "acme":  { "command": "/opt/acme/acme-orchestrator" }
  }
}
```

**Client.** `POST /v1/sessions` with `"runtime": "acme-planner"` and `"delegationLease": { "maxDepth": 3 }` and `"budget": { "limits": { "models": { "meters": ["llm"], "amount": 1500000, "unit": "llmCredits" } } }`.

**What happens.**

1. The model sees `assign` and the bridge's other platform tools, and no `start_session`. A planner assigns a coder, the coder assigns a tester, and an attempt by the coder to assign a planner is refused by the extension, because `acme-planner` is in the coder's lineage.
2. Each child's runtime receives `traceId` in `session_start` and attaches it to its own traces, and the model never sees it.
3. The role check binds a child only while the child runs `acme-orchestrator`. A child whose runtime skips the check can still start a planner, within its grant. The bound that holds against a misbehaving child is the grant, whose depth of 3, budget, policy cap, and isolation floor the gateway applies to every `session.create`. `delegation.spawned` records that each child was created through the `acme-orchestrator` connection.
4. Under built-in delegation today, the team could not change the tool set or its descriptions without changing the gateway, the roles would be three runtime names that the model has to know, and the trace identifier would travel in `tracingContext` through a gateway-defined platform tool.

### Example 4: Untrusted code in a stronger sandbox, and an incident switch

A triage agent receives a repository from an external contributor. It reads the code itself, and it runs the contributor's tests only in a child with stronger isolation, no egress beyond package mirrors, and none of the parent's credentials.

**Operator.**

```yaml
name: triage
baseRuntime: claude-code
isolationProfile: sandboxed
allowDelegation: true
delegationPolicyRef: triage-runners      # admits label role: untrusted-runner only
---
name: untrusted-runner
baseRuntime: claude-code
isolationProfile: microvm
allowDelegation: false
defaultPoolConfig: { egressProfile: restricted }
labels: { role: untrusted-runner }
```

**Runtime author.** The `triage` runtime uses the reference tool set, as in Example 1, and ships a defaults file for the bridge so that a child of `untrusted-runner` receives no binding and no egress host from its parent, whatever the model asks:

```json
{ "untrusted-runner": { "credentialBindings": [], "egressHosts": [] } }
```

```json
{ "mcpServers": { "lenny": { "command": "lenny-mcp-bridge",
  "args": ["--server", "platform", "--session-defaults", "/etc/triage/child-defaults.json"] } } }
```

**Client.** A session of `triage` with a `userCredential` binding for the user's GitHub token under 0151, so the triage agent can comment on the pull request. The tenant also gives the session a named binding for its package mirror under 0127.

**What happens.**

1. The model calls `start_session` with `target: "untrusted-runner"` and the repository as exported files. The bridge applies the defaults file and calls `session.create` with empty `credentialBindings` and `egressHosts`. The child therefore receives neither the `userCredential` binding nor the mirror binding, and its egress is the `restricted` profile of its own pool. The child's pool is `microvm`, which is at least the parent's `sandboxed`, so the isolation caveat admits it. A request for a `standard` pool would be refused by the same caveat. The child has fewer bindings and a stronger isolation profile than its parent, so it is a fenced child, and the triage agent cannot offer it parent tools without `offerTools: allow` (decision 31).
2. The child runs the tests and ends its turn with the output. The triage agent posts the result with its own credential.
3. During an incident, the tenant's `security-admin` sets `"delegation": "forbid"` with `PUT /v1/admin/tenants/{id}/security-config` under SO-2. The gateway refuses the next `session.create` of every session in the tenant with `403 DELEGATION_DISABLED` and `disabledBy: "securityGuardrail"`. At each session's next turn, `availability_changed` carries `delegation: false`, and the bridge stops listing `start_session`. The bridge keeps the tools that manage children for a session whose `session.list` is not empty, and the running children finish.
4. To stop one runaway tree at once, the tenant's `tenant-admin` calls `POST /v1/admin/sessions/{rootId}/terminate-tree`, which closes every session of the tree whatever `onParentEnd` holds.
5. Removing the forbid takes effect after the record's weakening delay.

### Example 5: A tenant administrator sets the rules for the tenant and for one environment

A tenant runs the shared platform runtime `claude-code`. Its administrator wants every session in the tenant to delegate only to runtimes the tenant approved, with at most 20 children and 2,000,000 credits per tree. Contractors work in the environment `contractors`, whose sessions may delegate only to read-only reviewers, with at most 4 children per tree.

**Tenant administrator.** The `tenant-admin` of `acme` writes two policies, the tenant settings, and the environment, with no platform administrator involved:

```yaml
# POST /v1/admin/delegation-policies   (tenant acme)
name: acme-approved
rules:
  - target: { matchLabels: { acme-approved: "true" }, types: [agent] }
    allow: true
---
name: acme-readers
rules:
  - target: { matchLabels: { access: read-only }, types: [agent] }
    allow: true
```

```http
PUT /v1/admin/tenants/acme/delegation
{
  "allowByDefault": true,
  "policyRef": "acme-approved",
  "ceilings": { "maxDepth": 3, "maxChildrenTotal": 20, "maxParallelChildren": 5 }
}
```

```http
PUT /v1/admin/tenants/acme/session-budget
{
  "ceilings": { "models": { "meters": ["llm"], "amount": 2000000, "unit": "llmCredits" } }
}
```

```http
PUT /v1/admin/environments/contractors
If-Match: "…"
{
  "…": "the environment's other fields, unchanged",
  "defaultDelegationPolicy": "acme-readers",
  "delegationCeilings": { "maxChildrenTotal": 4 }
}
```

The tenant administrator also creates tenant-derived runtimes of approved helpers under 0147 and labels them `acme-approved: "true"`, and `access: read-only` or `access: write` as they are.

**Security administrator.** The tenant's `security-admin` adds two SO-5a rules to the security record with `PUT /v1/admin/tenants/acme/security-config`, one for a target labelled `access: write` and one for any carve above 500,000 credits, each answered by a `security-approver` (decision 36):

```json
"approvals": {
  "tools": [
    { "match": "session.create", "when": { "targetLabels": { "access": "write" } }, "approver": "security", "showArguments": true },
    { "match": "session.create", "when": { "llmCreditsAbove": 500000 }, "approver": "security", "showArguments": true }
  ]
}
```

The record's single writer and its weakening delay govern the rules, so a stolen `tenant-admin` credential cannot remove them.

**Client.** A contractor in `contractors` creates a session of `claude-code` and asks for `"delegationLease": { "maxChildrenTotal": 10 }`.

**What happens.**

1. The gateway resolves the root grant. The policy cap is the intersection of the runtime's `coding-helpers` (Example 1), the tenant's `acme-approved`, and the environment's `acme-readers`, so a child must carry all three policies' labels. The child count is the client's 10 lowered to the environment's ceiling of 4. The depth, parallelism, and budget take the runtime's defaults, lowered to the tenant's ceilings. `delegation.grant_created` records each value and the level it came from.
2. The contractor's agent starts a read-only reviewer, which the cap admits. An attempt to start a helper that writes to repositories is refused at `session.create`, because `acme-readers` does not admit it. A fifth child is refused because the child count is spent.
3. A session of the same runtime in another environment of the tenant may start any approved helper, up to 20 children. Its `session.create` of a helper labelled `access: write` waits under SO-5a until a `security-approver` answers, or SO-5a's timeout denies it, and a `session.set_limits` that raises a child's carve past 500,000 credits waits the same way.
4. The tenant administrator narrows `acme-approved` during a review. The next `session.create` of every session in the tenant reads the new policy, and running children are not affected.

### Example 6: A workflow engine outside the pods

A CI system runs a plan step, then several implementation steps in parallel, then a review step, each as an agent session. The CI system is the orchestrator, and it runs outside Lenny.

**Operator.** The operator creates a service account for the CI system under 0143, with a role that may create sessions in the CI environment, and registers the runtimes the steps use. No delegation setting is needed.

**CI system.** The engine authenticates as the service account and creates each step as a root session through the client API. It forbids delegation inside the steps under decision 26 of 0149, labels each session with the pipeline for cost attribution, and waits on each session through `callbackUrl` or `GET /v1/sessions/{id}/events`:

```http
POST /v1/sessions
{
  "runtime": "claude-code",
  "labels": { "pipeline": "release-482" },
  "workspacePlan": { "sources": [{ "type": "gitClone", "url": "https://github.com/acme/app", "ref": "main" }] },
  "delegationLease": { "maxDepth": 0 },
  "callbackUrl": "https://ci.acme.com/hooks/lenny"
}
```

**What happens.** Each step is a root session with no parent link. The tenant's and the service account's quotas and each session's own budget bound the run, and the engine's code decides the order, the parallelism, and the retries. Lenny provides no tree across the steps: closing the plan session does not close the implementation sessions, and their budgets are not carved from one total.

This example marks the boundary of the design. A later change that lets an actor outside the pod attach a child to a session (decision 2) would let the engine create the implementation sessions as the plan session's children, so they would share its budget and close with it. A principal budget of 0170 that every root session names in `budgetRefs` already runs them under one budget, as 0170's Example 3 shows. This example needs no later change for that.

## Limits, and alternatives rejected

**Limits [design]:**

- An operator who replaces the reference extension can give a model a tool set that behaves differently. The requirements of this change hold whatever the extension does, the audit of DPR-11 records which connection created each child, and the extension's behaviour is the operator's responsibility.
- A check an extension makes with tags or `lineage`, such as Example 3's role check, binds a child only while the child's runtime runs that extension.
- A runtime that keeps an old tool list keeps session tools after its grant is removed, as under 0126 "Limits". The gateway refuses the call.
- The primitives do not prevent an orchestrator from building a deep or wide tree within its caveats. The caveats and the carved budget bound the tree.
- An idempotency key protects against a repeated call. It does not stop a model that decides again to start a child it already started.
- A tree left `dormant` for longer than `perChildMaxAge` loses its children, and by default each expiry wakes the parent (decisions 13 and 25). A deployment that keeps trees for days sets `perChildMaxAge` accordingly or leaves it unset, leaves `expired` out of `wakeOn`, or has the parent raise the age with `session.set_limits`.
- A tree whose parent cannot recover holds its slot and budget for `maxResumeWindowSeconds` plus `maxAwaitingClientActionSeconds`, and its children keep running with no session to answer them, where `maxTreeRecoverySeconds` used to fail the tree (decision 15). The carved budget bounds what the children spend meanwhile.
- No filter keeps a compromised child from encoding what it read in a message to its parent. Within one environment a child reads nothing its parent cannot reach (DPR-3), and across environments the sharing decision of TS-3 bounds what a child reads, while `PostAgentOutput` screening is best-effort (decision 34).
- A relay between children costs the parent a `session.send` per hop, counted under MD-9, and a model-driven relay costs a turn per hop (decision 30).
- A released session leaves its tree's controls by design. `leave`, `leaveMaxDepth`, and the tenant's session quotas bound it, and a transferred session stays inside them (decision 32).
- This change relies on other changes for the rules "DP-7" lists, so its parts that cite them cannot land before those changes take them on. "Steps" records the dependencies.

**Alternatives considered:**

- **Keep built-in delegation and drop the orchestration settings only.** The disposition table of DP-6 applies equally to this option, and it costs less design work. Each later feature still states a rule for a delegated child, the gateway keeps the virtual child interface and the tool set, and an operator who wants another orchestrator still has no path that keeps the pod free of credentials. It was option (b) of O1, which decision 1 did not take.
- **Move delegation outside Lenny with no new primitive.** An operator's orchestrator would call `POST /v1/sessions` with a client credential placed in the workspace. Every guarantee of §8.1 would then depend on the operator's handling of that credential, which is the bypass 0126 records. This option is rejected.
- **Express the grant as a bearer token held by the pod.** The pod would need egress to the gateway's public API, and a stolen token would act outside the session. Decision 2 offers no token form in v1.
- **Edges between children.** A parent-granted edge would let one child message another directly. It contradicts MD-9, adds a loop bound and boundary rules of its own, and lets a fenced child reach a sibling with more authority. Decision 30 keeps the parent as the broker, and edges remain a later addition under DPR-12.
- **State every rule for a delegated child in this change.** Revisions 4 to 6 did so for child events, client tools, recovery, the REST surface, and crossing, which left each rule in two changes with nothing to keep the two in step. Decision 35 returns each to its owner.

## Specification and non-specification changes

**Specification [design].** These are the edits this change makes. The edits that the changes of "DP-7" take on land with them.

- [§8](../../spec/08_recursive-delegation.md#8-recursive-delegation) is rewritten around the primitives: the grant, its caveats with the content caveat, the root grant inputs and the resolution of every caveat, the parent link, the tree terminate, transfer and release with `leaveMaxDepth` across releases and `maxLeaves`, `gateway.delegation.sessionBudget` with its levels, its strictest value in effect, and the limits it requires, the carved budget's operations, `wakeOn`, `events.peek`, the platform operations, parent tools, and the reference extension. The content it gives up moves to the pages that own it, with successor pointers.
- [§10.2](../../spec/10_gateway-internals.md#102-authentication) and [§13.3](../../spec/13_security-model.md#133-credential-flow) state the session grant and the retirement of `a2a_delegation` minting (decision 2). The §10.2 permission matrix gains the tree terminate for `tenant-admin`.
- [§10.6](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model) states that `defaultDelegationPolicy` applies at child creation and adds `delegationCeilings`.
- [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) states the move of budget and resource caveats by `session.set_limits` and the parallelism count with its difference from SL-10. [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) gains `delegation.grant_created`, `delegation.left`, and the new members of `delegation.spawned`.
- [§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) states that a session's `labels` are its root client's at every depth.
- [§15.1](../../spec/15_external-api-surface.md#151-rest-api) gains `parentSessionId` on a session's representation and `POST /v1/admin/sessions/{rootId}/terminate-tree`, and the catalogs CATERR and CATSET gain `SESSION_BUDGET_REQUIRED`, `LEAVE_LIMIT_REACHED`, `gateway.delegation.sessionBudget` with its tenant and environment levels, and the default of `maxLeaves`, while the error catalog loses the codes of the dropped settings, such as `DELEGATION_CYCLE_DETECTED`, `TREE_VISIBILITY_WEAKENING`, and `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`.
- [§15.7](../../spec/15_external-api-surface.md#157-runtime-author-sdks) states the SDK `sessions` library with its relay and `ToolSet`, the reference tools, and the bridge's `--session-tools` and `--session-defaults`.
- [§16](../../spec/16_observability.md#16-observability) loses the metrics and events of the dropped settings.
- [§26](../../spec/26_reference-runtime-catalog.md#26-reference-runtime-catalog) states that the reference runtimes that delegate use the reference extension.

**Non-specification [design]:**

- `pkg/delegation/cycle`, `pkg/gateway/platformtools/treerecovery`, `treearchive`, `deadlock`, and the virtual child interface leave, under decisions 5 and 15.
- `pkg/gateway/platformtools/leasecontrol`, `delegationbudget`, and `treebudget` become the grant and the carved budget.
- The runtime SDKs and the reference MCP bridge gain the reference extension.
- The Helm chart loses `gateway.cycleDetection.mode`, `gateway.allowSelfRecursion`, and the tree recovery timeouts, under decisions 5 and 15, and gains `gateway.delegation.parentTools.enabled`, `gateway.delegation.sessionBudget`, and the platform defaults of `leave`, `leaveMaxDepth`, and `maxLeaves`.

## Documentation

Each page below states every setting, default, limit, failure mode, and runtime-facing rule of this change that its readers meet [design]. The changes of "DP-7" add their own rules to the same pages under their documentation sections.

| Page | Content |
| :-- | :-- |
| `docs/runtime-author-guide/delegation.md` | The primitives, the reference extension, writing another extension over the operations, `wakeOn`, `events.peek`, the relay, group chats with the parent as manager, transfer and release with the rules of a leave, and offering tools to a child with the `ToolSet`, the fenced default, and at-least-once delivery. |
| `docs/runtime-author-guide/platform-tools.md` | The operations of DP-1 with their arguments, results, and refusals, the operations a session holds without a grant, and `session.self`. |
| `docs/runtime-author-guide/runtime-sdk.md` | The `sessions` library, the relay, the `ToolSet`, the reference tools and their names, `--session-tools`, and `--session-defaults`. |
| `docs/reference/error-catalog.md` | `DELEGATION_DISABLED` for the operations of DP-1, the refusals of `leave` and `leaveMaxDepth`, `LEAVE_LIMIT_REACHED`, `SESSION_BUDGET_REQUIRED` with the limits a required session budget holds, and the removal of the codes of the dropped settings. |
| `docs/reference/cloudevents-catalog.md` | `delegation.grant_created`, `delegation.spawned`, and `delegation.left`. |
| `docs/reference/configuration.md` | `gateway.delegation.*` with the defaults and ceilings of each caveat, the platform defaults of `leave`, `leaveMaxDepth`, and `maxLeaves` (4), `gateway.delegation.parentTools.enabled`, `gateway.delegation.sessionBudget` (`requiredToDelegate`) with its tenant and environment levels and the strictest value in effect, and the removal of `gateway.cycleDetection.mode`, `gateway.allowSelfRecursion`, and the tree recovery timeouts. |
| `docs/client-guide/delegation-and-tasks.md` | What a client of a root sees of its tree, the tree route after a resume, taking over a sub-agent with transfer or release, and pending questions that move to the client on a leave. |
| `docs/operator-guide/multi-tenancy.md` | The root grant inputs with tenant, environment, and runtime policies, defaults, and ceilings, the resolution of each caveat, the content caveat, `toolCallRateLimit`, `messagingRateLimit`, `labels` and tags, and the tree terminate for a `tenant-admin`. |
| `docs/operator-guide/security.md` | The security caveats, fenced children, the security record's delegation fields, the authority a parent tool carries, and what crosses an environment (decision 34). |

## Tests

Each requirement of this change needs `unit` and `integration` tests [design]. The requirements this change places on other changes carry their tests with them.

- DPR-12's tests feed the reference extension a child event kind, a result member, and a child it did not create, and check that it carries on, and check that the gateway withholds an event kind that needs action from a runtime that does not declare it in `capabilities.childEvents`.
- DPR-2, DPR-3, DPR-5, DPR-10, DPR-16, DPR-18, DPR-20, and DPR-21 need `security` tests that try to widen a security caveat, to exceed a tenant, environment, or runtime ceiling, to reach the grant from a pod by another path, to move a child past a check with a `PreRoute` interceptor, to leave beyond `leave` or `leaveMaxDepth` or under the security record's forbid, to escape `maxTreeSize` or `terminate-tree` through `session.transfer`, to run past `maxParallelChildren` by transfers beyond `maxLeaves`, to leave from below `leaveMaxDepth` through a session created under a released session, to release a crossed child without the target's admission, to offer parent tools to a fenced child or across environments, to move a file by the path with the weaker content caveat or from a session with no grant, and to write a key into `labels` from a parent.
- DPR-10's `unit` tests refuse a ceiling of 0 for `maxDepth`, `maxChildrenTotal`, and `maxParallelChildren` at each level with `400 VALIDATION_ERROR`, accept a client's and a parent's `maxDepth: 0`, and check that a session with no depth left holds no grant and is not offered `start_session` (decision 50).
- DPR-4 and DPR-9 need `load_local` tests of concurrent child creation against one parent's budget and of concurrent calls with one idempotency key.
- DPR-4 needs `unit` tests of `gateway.delegation.sessionBudget`: each of its values at the platform, tenant, and environment levels, the refusal of a write that loosens a level, the strictest value in effect after a higher level is tightened over a stored looser one, and the refusal of a session budget that holds no limit counting `llm` or none counting `compute` (decisions 52 and 53).
- DPR-16 needs `unit` tests of `maxLeaves`: its resolution, a leave by a parent and by the root's client counted at the root, and `LEAVE_LIMIT_REACHED` past it (decision 54).
- DPR-8 needs a `contract` test of the reference extension and the relay against the platform operations.
