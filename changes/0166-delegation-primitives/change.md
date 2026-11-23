# Delegation as session primitives

Status: `Draft`, revision 6, 2026-11-23. Programs: [v1](../programs/v1.md).

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, or another change on 2026-11-22. |
| **[design]** | What this change proposes. Nothing implements it. The owner's decisions are in `decisions.md`, and the text names the decision each part follows. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The owner asked on 2026-11-22 whether Lenny should provide delegation at all, or should instead provide mechanisms with which a deployer builds the same delegation outside Lenny or as an extension. This change frames that question with evidence from the tree and proposes an answer for the owner to decide.

Delegation today is a second session-creation path with its own semantics **[source]**:

- [§8](../../spec/08_recursive-delegation.md#8-recursive-delegation) runs to about 1,100 lines, and 689 lines of the numbered specification files mention delegation.
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

- Primitives in the core that carry the authority and cost guarantees of delegation: a session grant, a parent link on session creation, a budget carved from the parent's at creation, and delivery of a child's events and questions to its parent ("DP-1" to "DP-4").
- The inputs from which the gateway resolves a root session's grant, including the policies and ceilings a tenant administrator sets for the tenant and for each environment ("DP-1").
- A reference extension that provides the session tool set over those primitives, shipped with the runtime SDKs and the reference MCP bridge ("DP-5").
- A disposition for each delegation setting of §8 and of the v1 changes: kept as a primitive, moved to the extension, or dropped ("DP-6").
- A statement of what this change supersedes in the v1 member changes ("DP-7").

**Out of scope:**

- Agent-to-agent protocols and external agents. 0124 removed them, and this change does not reintroduce them.
- §8.4's `approval` mode, which is reserved and unimplemented. Approval of a child by a person stays with SO-5a of 0142, retargeted to `session.create` ("DP-7"), and a `DelegationPolicy` rule can require approval for a target or above a budget (decision 8).
- The availability of the platform tools that are unrelated to children, such as `ask_question`. 0126 "DA-7" reviews them, and the parts of 0126 that concern them survive this change ("DP-7").
- Root-session creation by a client through the public API. It is unchanged.

## Terms

| Term | Meaning |
| :-- | :-- |
| Built-in delegation | Delegation as §8 and the v1 changes specify it: the gateway-hosted tools `lenny/delegate_task` and the rest (`start_child_session` and the rest under MD-23), the delegation lease, `DelegationPolicy`, tree budgets, tree recovery, and the cascade. |
| Primitive | A mechanism this change keeps in the core because only the gateway can enforce it against an untrusted pod. "DP-1" to "DP-4" define the primitives. |
| Session grant | The authority a session holds to create sessions as its children and to act as their client, recorded by the gateway and never held by the pod as a bearer token ("DP-1"). Its caveats can only narrow from parent to child. |
| Caveat | A restriction recorded on a session grant, such as the depth that remains, the isolation floor, the policy cap, or the environment. A child's grant carries every caveat of its parent's, and may add more. |
| Security caveat | A caveat that never widens once a grant holds it: the remaining depth, the isolation floor, the policy cap, the environment and sharing path, the principal, the credential bindings and egress hosts a child may receive, `onParentEnd`, `offerTools`, `leave`, and `leaveMaxDepth` ("DP-1"). |
| Resource caveat | A caveat that counts something a tree consumes: the child count, the parallelism, the tree size, a child's maximum age, the file export limits, `messagingRateLimit`, `toolCallRateLimit`, and the carved budget. Lease extension under §8.6 may raise a resource caveat within its ceilings, and a parent may move unused amounts between itself and a live child ("DP-3"). |
| Root grant inputs | The settings from which the gateway resolves a root session's grant at creation: availability, the policy references, and the numeric ceilings at the platform, tenant, environment, and runtime levels, and the client's request ("DP-1"). |
| Child labels | A small map of strings that a parent sets on `session.create`, which the gateway stores on the child's session row, returns from the read operations, and delivers in the child's `session_start` ("DP-1"). |
| Idempotency key | A string a parent passes on `session.create` so that a repeated call returns the child the first call created ("DP-1"). |
| Event class | The group a child event belongs to, `update`, `result`, `question`, `tool_call`, or `status`, which states who wrote the event and whether it opens a turn in the parent ("DP-4"). |
| `wakeOn` | The event classes and event kinds that open a turn in a parent or wake it, set on `session.create` and changed with `session.set_limits` ("DP-4"). |
| Reattach and release | The two ways a session leaves its parent. A reattached session stays in its tree with the root's client as its client, and a released session becomes a root outside the tree ("DP-2"). |
| Fenced child | A child created with fewer credential bindings, fewer egress hosts, or a stronger isolation profile than its parent ("DP-8"). |
| Question | An elicitation that a child raises with `ask_question` or with a `request` of `kind: "question"`, which travels one hop at a time under the §9.2 chain. The parent answers it or passes it on ("DP-4"). |
| Parent link | The `parentSessionId` of a session created under a session grant, with the lineage, principal, environment, and cascade that follow from it ("DP-2"). |
| Carved budget | The budget a child receives at creation, reserved from its parent's remaining budget and returned at the child's end ("DP-3"). |
| Child event delivery | The delivery of a child's events to its parent's inbox and to a pending wait, which MD-22 and SL-12 define ("DP-4"). |
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
| Credit budgets | 0133's budgets with reservation before a call and settlement after it, across the call, session, tree, user, tenant, and global scopes ([LU-6](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets)). |
| `onParentEnd` | The lease's policy for a parent's non-terminal children when the parent ends, `close_all` or `let_finish` ([`0146-session-lifecycle`, "SL-9"](../0146-session-lifecycle/change.md#sl-9-when-a-parent-ends)). |
| Session derive | The creation of a session from another session's state, under [`0144-session-derive`](../0144-session-derive/change.md). It is a creation path beside a root create and a delegated child. |
| Elicitation chain | The path of [§9.2](../../spec/09_mcp-integration.md#92-elicitation-chain) by which an elicitation travels from a session to its client one hop at a time, with each parent able to answer it or pass it on. Today the virtual MCP child interface carries the hop from a child to its parent. |
| Session `labels` | The user-defined metadata of [§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) on a session, indexed and filterable in `GET /v1/sessions`, `GET /v1/usage`, and `GET /v1/metering/events` for cost attribution. |
| Who writes delegation policies | The §10.2 permission matrix gives "Manage delegation policies" to `platform-admin` for all tenants and to `tenant-admin` for its own tenant, read-only to `tenant-viewer`, and "Manage environments" to the same roles **[source]**. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| DA-1 to DA-7 | [`0126-delegation-availability`](../0126-delegation-availability/change.md) | The availability levels, their resolution, the refusal, the tool list, running sessions, per-hop narrowing, and the review of the other platform tools. |
| TS-3 and decision 26 | [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#client-delegation-limits-decision-26) | The session's stored environment as the delegation gate, sharing paths, and a client's `maxDepth: 0`. |
| MD-9, MD-22, MD-23, MD-25, and MD-26 | [`0135-message-delivery`](../0135-message-delivery/change.md#md-22-parents-and-children) | Inter-session messages, the one session model with child events, the tool names, watching a child's stream, and importing a child's files. |
| SL-4, SL-8, SL-9, and SL-12 | [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-8-parents-children-and-their-states) | Tree suspension, the states of parents and children, the cascade when a parent ends, and the recovery of child events. |
| LU-4 and LU-6 | [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets) | The removal of LLM credential brokering, `credentialPropagation` included, and credit budgets with the tree scope. |
| RT-3 | [`0141-runtime-routing`](../0141-runtime-routing/change.md) | Routing labels, which replace `experimentContext` and which a child takes from its parent. |
| CR-1 | [`0124-connector-removal`](../0124-connector-removal/change.md#cr-1-removal) | The removal of connectors and of every external-agent concept. |
| SO-2 and SO-5a | [`0142-security-oversight`](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | The tenant security record with its delegation guardrail, and approval rules that hold a platform tool call matching a tool name until an approver answers. |
| Effective policy | [`0127-egress-and-git`](../0127-egress-and-git/change.md#effective-policy) | A child in its parent's environment inherits the parent's `egress.allowedHosts`, `egressLockHosts`, and named bindings, except a `userCredential` binding. |
| Placement | [`0136-placement`](../0136-placement/change.md) | For `kind: delegate`, the parent's effective placement requirements apply to the child. |
| RV-1 and `runtimeOptions` | [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-1-revisions) | The delegation `TaskSpec` gains `runtimeOptions`, validated against the child's chosen revision at the post-chain point. |
| DR-4 | [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md#fields) | A tenant-derived runtime may set a narrowing `delegationPolicyRef`. |
| WM-12 | [`0153-workspace-materialization`](../0153-workspace-materialization/change.md#wm-12-mid-session-uploads-and-child-file-imports) | Mid-session uploads by a client, and imports of a child's files. |
| MD-3 | [`0135-message-delivery`](../0135-message-delivery/change.md#md-3-inbox-states-and-custody) | Inbox custody, including deduplication of a resent message by its id. |

## High-level design

Lenny keeps the guarantees of delegation and stops providing the orchestrator. A session that its configuration allows to delegate holds a session grant. With the grant, the session can create sessions as its children, act as their client, and do nothing else that a client credential could do. Each child is an ordinary session, as MD-22 already states, linked to its parent, inside its parent's environment and principal, under a budget carved from its parent's, at an isolation level no lower than its parent's, and with a grant whose caveats are at least as narrow as its parent's. When the parent ends, its children end under `onParentEnd`. The gateway delivers each child's events and questions to the parent, and the parent answers a question or passes it on, as the §9.2 chain does today.

The gateway resolves a root session's grant from settings that people with authority over the session set. A platform administrator sets the platform defaults and ceilings. A tenant administrator writes the tenant's delegation policies, as §10.2 already allows, and sets a policy and numeric ceilings for the whole tenant and for each environment. The runtime contributes its policy reference and defaults, a tenant's security team can forbid delegation outright, and the client can only narrow what the others allow. A parent can only narrow its own grant for a child, with two exceptions that move resources rather than authority: lease extension raises a resource limit within its ceilings, and a parent can move unused budget between itself and a live child.

The pod never holds a credential for this. The runtime asks for a child through the platform tool server, the adapter forwards the request with the session's identity, and the gateway applies the grant. A deployer who wants a different orchestrator does not need to put a client credential in a workspace, so the bypass that 0126's "Limits" records closes.

The tools that a model uses to start and manage other sessions, the tools of MD-23 under generic names such as `start_session`, `await_sessions`, and `send_message`, move to a reference extension. The extension ships with the runtime SDKs and the reference MCP bridge, implements each tool over the primitives, and keeps the schemas of MD-23 and the rendering rule of MD-22. A runtime author uses it, replaces it, or omits it. A runtime that omits it offers its model no session tools, so the availability question of 0126 reduces to whether the session holds a grant.

Orchestration policy moves to the extension or is dropped. Cycle detection, sibling visibility, retry of a failed child, discovery filtering, and presets become choices of the orchestrator. `maxDepth`, the carved budgets, the isolation floor, the policy cap on targets, and the cascade stay in the core as caveats or primitives. "DP-6" lists each setting.

```
 runtime (pod)                 adapter            gateway
 ─────────────                 ───────            ───────
 reference extension
  start_session ──tool_call──> forwards ──> session grant of S?
                                     (session S)    │ no grant ──> refused, no child
                                                    ▼
                                                  caveats: depth, isolation floor,
                                                  policy cap, environment
                                                    │
                                                    ▼
                                                  POST /v1/sessions semantics with
                                                  parentSessionId = S, carved budget,
                                                  narrowed child grant
                                                    │
 inbox  <── child events (MD-22) ──────────────────┘
```

## Requirements

| ID | Requirement |
| :-- | :-- |
| DPR-1 | A session creates a child only under its session grant, and a session with no grant creates none. |
| DPR-2 | A child's grant carries every caveat of its parent's grant. No later hop, lease extension, or setting below the platform widens a security caveat. A resource caveat rises only through lease extension within its ceilings, or through a transfer from the parent's own unused amount. |
| DPR-3 | A child's isolation level and placement are at least its parent's, its environment and principal are its parent's unless a sharing path of TS-3 applies, its remaining depth is less than its parent's, and the credential bindings and egress hosts it receives are a subset of its parent's. |
| DPR-4 | A child's budget is reserved from its parent's remaining budget at creation, and the unused part returns to the parent when the child ends or when the parent takes it back. |
| DPR-5 | No pod holds a bearer token for the session grant. The gateway applies the grant to the session that the adapter names. |
| DPR-6 | When a parent ends, its non-terminal children end under the parent's `onParentEnd`. A force-terminate of the parent, and a tree terminate, close every descendant at once whatever `onParentEnd` holds. |
| DPR-7 | The gateway delivers each child event of MD-22, and each question a child raises, to the parent's inbox or to a pending wait, once, and holds an undelivered event or question across the parent's recovery. |
| DPR-8 | The session tool set is implemented outside the gateway, over the primitives, and a runtime that omits it starts no child through the platform tool server. |
| DPR-9 | A `session.create` that repeats an idempotency key the same parent used before returns the child of the first call and creates no second child. |
| DPR-10 | A root session's policy cap is the intersection of every policy that the platform, the tenant, the session's environment, the runtime, and the client name, and each numeric caveat of a root grant is at most the smallest ceiling those levels set. |
| DPR-11 | The gateway records the creation of each grant with its resolved caveats and the level each came from, and the creation of each child with the parent's grant, the policy version that admitted the target, and the client name of the connection that sent the call. |
| DPR-12 | A later change that adds a child event kind, a member of an operation's result or of a child's record, or a way for a child to join a parent other than `session.create`, leaves an extension written against this change working: the reference extension ignores what it does not know, and the gateway delivers an event kind that needs the parent to act, or a child created by another path, only to a runtime that declares it. |
| DPR-13 | A runtime calls a tool its client offers, a REST client, an adapter, or a parent, as a blocking call that stays open until the client answers or `clientToolHoldSeconds` passes, and a result that arrives after the call is gone reaches the runtime as `toolResults` on its next message, so no result is lost. |
| DPR-14 | A child event of a class or kind outside its parent's `wakeOn` opens no turn in the parent and wakes no parent, and a `status` event carries only members the gateway wrote, with any text the child wrote in a member attributed to the child. |
| DPR-15 | A message with `wake: false`, from a client or a parent, starts no turn in its recipient, and a message that sets `wake: false` with `interrupt` is refused. |
| DPR-16 | A session leaves its parent only by `session.reattach` or `session.release`, only while the grant that governs the call is present and the guardrail does not forbid delegation, only as far as that grant's `leave` allows, and only at a depth no greater than `leaveMaxDepth`. A reattached session stays counted in its tree's `maxTreeSize` and in the reach of the root's cascade and `terminate-tree`, a released session keeps its security caveats and falls under the tenant's session quotas, and after either the former parent has no operation on the session. The grant that governs a call of the parent is the parent's, and the grant that governs a call of the root's client through the REST route is the root's. |
| DPR-17 | `files.import` imports only a file part that a message or a child event delivered to the importer, and every file that enters a workspace from another session passes the import or export limits and `scanExportedFiles`. |
| DPR-18 | A parent's tools reach a child only when the platform switch, the tenant, the environment, the security guardrail, `offerTools`, the child's revision, and the call all allow them, and a fenced child receives none unless its `session.create` sets `offerTools: allow`. A child never receives its parent's client tools, and a client tool's call in a turn that another session opened is refused unless the client marked the tool `allowInOtherTurns`. |
| DPR-19 | For a child in another environment than its parent, `session.messages` and a watch under MD-25 carry only what TS-3 "Model" lists, and leave out activity and tool calls with their results. |

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

Each part below names the decision of `decisions.md` that settles it.

### DP-1 Session grant

A session grant is a gateway-side record attached to a session row **[design]**. It holds the security caveats (the remaining depth, the isolation floor, the policy cap, the environment and any sharing path, the principal, the credential bindings and egress hosts a child may receive, `onParentEnd`, `offerTools`, `leave`, and `leaveMaxDepth`) and the resource caveats (the child count, the parallelism, the tree size, a child's maximum age, the file export limits, `messagingRateLimit`, `toolCallRateLimit`, and the carved budget). A child receives a grant derived from its parent's, narrowed by the parent's request.

**Root grant inputs.** A root session receives a grant at creation when its resolved availability allows delegation. The gateway resolves the grant from these inputs:

| Input | Level | Written by | Contributes |
| :-- | :-- | :-- | :-- |
| `gateway.delegation.*` Helm values | Platform | Operator, at install | Availability defaults and forbid (0126 "DA-1"), the maximum of each numeric caveat, including the parallelism ceiling §8.3 sets for its Redis reservation script, the defaults of `leave` (`reattach`) and `leaveMaxDepth` (1) (decision 32), the ceiling of `clientToolHoldSeconds` (decision 24), and `gateway.delegation.parentTools.enabled`, `false` by default (decision 23). |
| Tenant delegation settings | Tenant | `tenant-admin`, or `platform-admin` | `allowByDefault` (0126), `policyRef`, a `DelegationPolicy` applied to every session in the tenant, and `ceilings`, a maximum for each numeric caveat. The route is 0126's tenant sub-resource, which waits on 0126's open O10 (decision 26). |
| The delegation guardrail | Tenant security record | `security-admin` | A forbid of delegation and a forbid of parent tools (0142 SO-2), read live at each `session.create`, `session.reattach`, `session.release`, and parent tool call. |
| Environment `defaultDelegationPolicy` and `delegationCeilings` | Environment | `tenant-admin`, or `platform-admin` | A policy applied to every session in the environment, and a maximum for each numeric caveat. §10.6 defines the policy field today. |
| Runtime `allowDelegation`, `delegationPolicyRef`, and default lease | Runtime and derived runtime | `platform-admin` for a platform runtime, `tenant-admin` for a tenant-derived runtime (0147) | Availability (0126), a policy, and default values for the numeric caveats. A derived runtime may only narrow its base. |
| Tenant runtime entry `allowDelegation: false` | Tenant, per runtime | `tenant-admin` | A narrowing of availability (0126). |
| `delegationLease` on `POST /v1/sessions` | Session | Client | Values for the numeric caveats and `maxDelegationPolicy`. A value above a ceiling is lowered to the ceiling, and `maxDepth: 0` turns delegation off (decision 26 of 0149). |

The policy cap is the intersection of every policy the levels name. Each numeric caveat is the client's value when the client sets one, and the runtime's default otherwise, lowered to the smallest ceiling that the platform, the tenant, and the environment set, as TS-3 resolves `maxDepth` today. A delegation preset of §8.3 becomes an operator-defined default lease on a runtime, a tenant, or an environment, so presets remain inputs to this resolution. The policy references are read live at each `session.create`, so a tenant administrator who narrows a tenant or environment policy stops new children at once (decision 5). The numeric caveats are fixed when the grant is created.

**Policy authoring.** Delegation policies keep the RBAC of §10.2: a `tenant-admin` creates, edits, and deletes the policies of its own tenant, and a `platform-admin` those of every tenant. A tenant administrator therefore controls which runtimes the sessions of its tenant may start as children, through the tenant policy, the environment policies, and the policies of its tenant-derived runtimes, without a platform administrator. A tenant administrator cannot widen what a platform runtime's policy or the platform ceilings allow, because every level intersects.

**Approval in a policy.** A `DelegationPolicy` rule that admits a target may carry `approval`, which is `none`, the default, `owner`, or `security`, and `approvalAboveCreditBudget`, a credit amount (decision 8). A `session.create` that the rule admits waits for the approval the rule names before the child is created: `owner` asks the root's user, and `security` asks a `security-approver` of the tenant, through SO-5a's approval flow. With `approvalAboveCreditBudget` set, the rule asks only when the child's carved budget exceeds that amount. When more than one level's policy admits the target, the strictest approval applies. The call's result carries `approval: pending`, and the child's creation or the refusal reaches the parent as a `status` child event. The gateway records the admitting rule on the child, and a `session.set_limits` raise that takes the child's carve above that rule's `approvalAboveCreditBudget` waits for the same approval.

**Audit.** The gateway writes `delegation.grant_created` when it creates a grant, with each caveat's resolved value and the level it came from, in the form of `disabledBy`. `delegation.spawned` names the parent's grant, the policy and its version that admitted the target, and the client name of the adapter connection that sent `session.create`, which shows whether the reference tool set or another extension made the call.

The pod reaches the grant only through the platform tool server: the runtime calls a platform operation, the adapter forwards it naming the slot's session, and the gateway applies the grant of that session. No bearer token for the grant exists, and the `a2a_delegation` child minting of §13.3 retires (decision 2). A later change that lets an actor outside the pod attach a child to a session adds to the operations and events of this change under DPR-12.

The platform operations are generic session operations. Each operation that names a session acts only on the caller's own children, except `files.import`, which names a session that delivered the caller a file part, and `session.self`, which names none. The operations carry no orchestration semantics. They are platform tools that the extension calls (decision 3):

| Operation | Arguments | Result |
| :-- | :-- | :-- |
| `session.create` | `target` (a runtime name), `task` (the first message, as `MessagePart[]`), and optionally: `runtimeOptions` (validated against the target's chosen revision and the policy cap, under 0140); `files` (an export from the caller's workspace under §8.7); `limits` (each resource caveat, at most what the caller's grant has left); `credentialBindings` and `egressHosts` (subsets of what the caller holds, each defaulting to what 0127 "Effective policy" passes today); `onParentEnd` (equal to or stricter than the caller's); `questions` (`deliver`, the default, or `forward`, "DP-4"); `labels` (child labels, at most 16 keys of 64 bytes and values of 256 bytes); `wakeOn` (the event classes and event kinds that open a turn in the caller or wake it, under "DP-4"); `tools` (tool definitions the caller offers the child for every turn, under "Tools a client offers" in "DP-8"), with `offerTools: allow` for a fenced child (decision 31); `leave` and `leaveMaxDepth` (equal to or narrower than the caller's, decision 32); and `idempotencyKey` | The child's session ID, the limits granted, `created: false` when the idempotency key matched an earlier child, and `approval: pending` when a policy rule holds the creation for approval. |
| `session.send` | `sessionId`, `parts`, `intent` (`steer`, `queue`, or `interrupt`, under MD-22), and optionally `idempotencyKey`, as a client's send under 0138 takes one, and `wake: false`, which makes the message wait for the recipient's next turn instead of starting one, is steered into an open turn on a runtime that declares `steer`, and is refused with `interrupt` (decisions 19 and 22). `tools` declares tool definitions for the turn this message opens, as a client declares them in `conversation.clientTools` (decision 23). A file part names a file in the caller's workspace, which the recipient imports with `files.import`, and a parent may set `materialize: true` on a file part to place the file in its child's workspace through WM-12 when the child's runtime declares `midSessionUpload`, under `fileExportLimits`, `scanExportedFiles` at `PreExportMaterialization`, and the WM-12 staging checks (decision 18) | The message's outcome. A repeated key returns the first send's outcome. |
| `session.interrupt` | `sessionId` | Stops the child's open turn without a message, as a client's turn interrupt under 0138 does, on a runtime that declares `interrupt`. |
| `session.messages` | `sessionId`, and optionally `after` and `limit` | The stored history of the events a watch of the child under MD-25 carries, paged, under the watch's checks: the child's `PostAgentOutput`, and the caller's `contentPolicy` and `PreMessageDelivery`. For a child in another environment it carries only what TS-3 "Model" lists, as the watch does (decision 34). |
| `events.wait` | Optionally `sessionIds`, `mode` (`any` or `all`), and `maxEntries`, with MD-22's rules for `await_child_sessions` | An entry for each covered child with its state, its labels, and its event, question, or tool call. |
| `session.get` | `sessionId` | The child's state, its labels, its pending question when it has one, its last end-of-turn message when it is not terminal, its `TaskResult` once terminal, and its usage with the unused part of its carve. |
| `session.list` | Optionally `depth` (default 1) | The caller's descendants to that depth, with each one's parent, state, labels, and whether it has a pending question. Only direct children can be named in the operations that act on a child. |
| `session.close` | `sessionId` | The close outcome, under SL-7. |
| `files.import` | `sessionId` (a session that sent the caller a file part, as a child's output or in a message), the part's `id`, `dest`, and optionally `overwrite`, under MD-26 extended to every direction (decision 18) | The imported file's path in the caller's workspace. The gateway refuses a part that no message or child event delivered to the caller. The import applies `fileImportLimits`, the WM-12 staging checks, the workspace size limit, `maxExportedFileSize`, and `scanExportedFiles` under the importer's `contentPolicy`. |
| `questions.answer` | `sessionId`, `questionId`, and `response` | The outcome of the answer, under §9.2. |
| `questions.forward` | `sessionId` and `questionId` | The question passes one hop up, to the caller's own client. |
| `session.set_limits` | `sessionId`, a change to each resource caveat, positive or negative, and optionally `wakeOn`, which is a setting of the caller rather than a caveat | The child's new limits. A raise draws on the caller's unused amount, and a cut returns the child's unused amount to the caller. A raise that crosses the admitting rule's `approvalAboveCreditBudget` waits for that rule's approval ("Approval in a policy"). |
| `runtimes.list` | None | The runtimes that the grant's policy cap allows, with each runtime's `agentInterface`. |
| `session.reattach` | `sessionId` | The child's state after the leave. The child stays in its tree with the root's client as its client, under "Leaving the parent" in "DP-2" (decisions 32 and 33). |
| `session.release` | `sessionId`, and optionally `handoff: true`, which a root alone may set | The released session's ID. The child becomes a root of the same principal outside the tree, under "Leaving the parent" in "DP-2" (decisions 32 and 33). With `handoff: true`, the caller ends after the release with `successorSessionId` naming the released session: a message sent to it is refused with `409 SESSION_HANDED_OFF` naming the successor, and its event stream ends with a `handoff` event naming the successor, so a client or an MCP or A2A adapter moves the conversation to the successor (decision 21). |
| `tools.result` | `sessionId`, `callId`, `content`, and optionally `isError` | The outcome of the answer. A repeated answer returns `answered`, and an answer to a withdrawn call returns `withdrawn` ("Tools a client offers" in "DP-8"). |
| `session.self` | None | The caller's own `lineage`, child labels, session `labels`, and a summary of its grant, for an extension on a tool connection, which never receives `session_start`. |
| `events.peek` | Optionally `afterSeq` and `maxEntries` | The caller's child events and questions after that sequence number, each with its sequence number, without taking them from the inbox. The call does not count as activity under SL-6 (decision 10). |

The platform tool list marks each operation `internal: true`. The reference MCP bridge never lists an internal operation to a harness, so a model sees only the tools of an extension. A call of an operation by a session with no grant is refused with `403 DELEGATION_DISABLED` and `details.disabledBy`, with two exceptions. A session that has children keeps `events.wait`, `events.peek`, `session.get`, `session.list`, `session.close`, `session.send`, `session.interrupt`, `session.messages`, `session.set_limits` for a cut, `questions.answer`, `questions.forward`, and `tools.result` for them (DAR-6 of 0126), and `session.reattach` and `session.release` are not among them. Every session holds `session.self`, and `files.import` for a file part delivered to it, whatever its grant, so a child whose runtime has `allowDelegation: false`, which holds no grant, still reads its own lineage and imports a file its parent sent. `messagingRateLimit` counts each message a session sends to another session under MD-9: `session.send` against the parent, including each send of the relay of "DP-5", and `send_update`, a mid-turn `message`, and the end-of-turn event of a turn the child opened on its own against the child.

**Idempotency and labels.** The gateway stores an idempotency key with the child row under the parent's session, and a repeat of the key by the same parent returns the existing child, whatever state it is in. Child labels are stored on the child's session row and delivered to the child in `session_start`, beside `lineage`, the list of runtimes from the root to the child itself, which the gateway records. `session.self` returns both to an extension on a tool connection. A restored parent matches its children to its work through the labels in `session.list`, and an extension passes a trace identifier or a role in the labels instead of the model's prompt. Neither the labels nor the lineage grant anything: a check an extension makes with them binds a child only while the child's own runtime runs that extension.

### DP-2 Parent link

A session created under a grant records `parentSessionId`, `rootSessionId`, and its depth **[design]**. The parent link carries what 0146, 0143, 0149, 0141, 0127, and 0136 already give a delegated child: the parent's principal and `caller_type`, its environment, its routing labels, the egress hosts and named bindings `session.create` passes, the parent's placement requirements under `kind: delegate`, tree suspension under SL-4, and the cascade of SL-9 under `onParentEnd`. Each of those changes keeps its rule and states it for a session with a parent link.

The child also takes the parent's session `labels`, so its usage and billing events fall under the same project or team as its root's. A child may add a key through its own child labels and may not change a key it inherits. Billing events carry `root_session_id`.

**Ending a tree.** `onParentEnd` is a security caveat ordered `close_all` before `let_finish`: a child's grant holds the parent's value or `close_all`. A force-terminate of any session in a tree closes its descendants at once whatever `onParentEnd` holds, as the `BUDGET_KEYS_EXPIRED` cleanup of §8.3 does. A `tenant-admin` terminates a tree of its own tenant with `POST /v1/admin/sessions/{rootId}/terminate-tree`, which writes the same audit event as a force-terminate. The tree terminate also reaches each session reattached in the tree.

**Leaving the parent.** A session leaves its parent in one of two ways (decision 32):

- `session.reattach` keeps the session in its tree. It stays counted in the tree's `maxTreeSize`, inside the root's cascade and `terminate-tree`, and it keeps its limits. The root's client becomes its client, and its former parent's end no longer cascades to it. Its carve moves from the parent's reservation to the root's.
- `session.release` makes the session a root of the same principal outside the tree. It keeps its security caveats, takes its resource caveats from the root grant inputs as any root does, takes its unused carve as its session budget under the user's budget objects, and falls under the tenant's session quotas. Only `session.release` takes `handoff: true` (decision 21).

The security caveat `leave` takes `none`, `reattach`, or `release`, each allowing the modes before it, and `leaveMaxDepth` is the deepest depth at which a session may leave. Both resolve from the root grant inputs, with platform defaults `reattach` and 1, and `session.create` may narrow them for a child. Neither mode is allowed while the caller holds no grant or the guardrail forbids delegation. The root's client may reattach or release a descendant of its root with `POST /v1/sessions/{id}/reattach` and `POST /v1/sessions/{id}/release`, under the same caveats.

A leave carries these rules (decision 33):

- The former parent receives a `left` `status` event naming the mode, as its last event from the session. Events written before it stay in the parent's inbox.
- The session's pending questions to the parent move to the root's client, on the root's event stream with `originSessionId`.
- Its open calls to the parent's tools return an error with the code `CLIENT_CHANGED`, and the bridge withdraws those tools.
- The parent's messages to it that are still pending are dropped, with a `dropped` notice to the parent.
- The user's client tools reach it only on a message from the user that declares them, under 0122 T16 and CT-9.
- It leaves the parent's relays and group chats, and the parent's watch of it ends. The parent's code removes it on the `left` event.
- File parts it sent the parent before the leave stay importable by the parent within their retention.
- It keeps its labels, its lineage with `leftFrom` and the mode, and its own children, which move with it.
- It stops counting toward the parent's `maxParallelChildren`.
- `wakeOn` and `questions: forward` lapse, because a client of a top-level session receives every event.
- The root's event stream and its webhook subscriptions receive `child_reattached` or `child_released`, naming the session, which appears in `GET /v1/sessions`.

### DP-3 Carved budget

A child's creation reserves its budget from the parent's remaining budget and returns the unused part when the child ends **[design]**. The budget is in credits under 0133 LU-6, so a child on a more expensive model consumes its share faster. The carve is a reservation from the budget objects that apply to the parent, which the budgeting change [`0170-budgets`](../0170-budgets/change.md) defines, and the child's share is a budget object of the child's session funded by that reservation (decision 12). A child's usage therefore counts against its own share and against every other budget object that applies to it, such as a team's or a tenant's.

**Moving budget within a tree.** `session.set_limits` moves an amount of any resource caveat between a parent and a live child: a raise draws on the parent's unused amount, and a cut returns the child's unused amount to the parent. A parent that keeps idle helpers open for follow-up work takes their unused budget back this way, and a parent gives a child that needs more than its estimate the difference without restarting it. A raise of `perChildMaxAge` extends the child's deadline, at most to what the caller's own grant allows (decision 13).

**A child's age.** `perChildMaxAge` is a deadline counted from the child's creation, in every non-terminal state, `dormant` included (decision 13). When it passes, the child expires, its unused carve returns to the parent, and the parent receives an `ended` child event. The event is of the `status` class of "DP-4", so by default it reaches the parent at once and wakes a `dormant` parent. A parent that prefers to sleep through expiries leaves the kind `expired` out of `wakeOn` on `session.create`, or a runtime or operator does so in the default of the grant, and the gateway then holds the event until the parent's next turn while failures and approvals still wake it (decision 25).

**Lease extension.** §8.6 stays for the resource caveats. When a session's or a tree's budget is exhausted, LU-6 requests an extension under §8.6's approval modes and ceilings, and the tenant and runtime layering of §8.6 stays. An extension that needs a person goes to the root's client as a human-required elicitation, so a parent session never approves its own child's extension. An extension never raises a security caveat.

**Parallelism.** A child counts toward its parent's `maxParallelChildren` while it is in setup, `running`, `idle`, `blocked`, `awaiting_client_action`, `resume_pending`, or `resuming`, and stops counting when it is `dormant`, `suspended`, or terminal. Counting the recovery states keeps a restored parent from starting new children while its old ones recover and exceeding the limit when they return. An orchestrator that keeps more helpers open than its parallelism closes or lets them go dormant.

### DP-4 Child event delivery

The gateway delivers each child event of MD-22 to the parent's inbox, or to a pending wait, under the delivery lock of MD-6, and recovers undelivered events under SL-12 **[design]**. This stays in the core because the gateway owns both sessions' delivery state. `events.wait` keeps MD-22's semantics for `await_sessions`, including the modes `any` and `all`, because the gateway decides under the parent's delivery lock which events a wait takes. `maxEntries` bounds one result, and a wide fan-out waits in pages. A wait that is cancelled, or whose result is not delivered, returns the events it took to `pending`, so a restarted bridge or a harness timeout loses no event.

**Questions.** A child's question travels to its parent as a child event `question` with a `questionId`, replacing the hop the virtual child interface carried. The parent answers it with `questions.answer` or passes it one hop up with `questions.forward`, as §9.2 states. A parent that creates a child with `questions: forward` has each of the child's questions passed up without delivery to the parent, which suits a programmatic orchestrator that does not answer questions. A human-required question skips the parent, as MD-22 states. An unanswered question is held across the parent's recovery with the other undelivered events of SL-12, and replayed when the parent resumes.

**Event classes.** Each child event belongs to a class, which says who wrote it and whether it opens a turn in the parent (decision 17):

| Class | Events | Written by | `from` | In `wakeOn` by default |
| :-- | :-- | :-- | :-- | :-- |
| `update` | `message`, a child's mid-turn message from `send_update` or a `message` frame | The child | `{ "kind": "agent", "id": <child> }` | No |
| `result` | `end_of_turn` from the child's own end-of-turn `message`, and `ended` with `completed` | The child | `{ "kind": "agent", "id": <child> }` | Yes |
| `question` | `question` | The child | `{ "kind": "agent", "id": <child> }` | Yes |
| `tool_call` | `tool_call`, a call to a tool the parent offers ("Tools a client offers" in "DP-8") | The child | `{ "kind": "agent", "id": <child> }` | Yes |
| `status` | `ended` with `failed`, `cancelled`, or `expired`; `end_of_turn` with `error` `turn_lost` or `idle_timeout`; `awaiting_client_action`; `suspended`; `resumed`; `question_withdrawn`; `tool_call_withdrawn`; `left`; and the outcome of a `session.create` held for approval | The gateway | `{ "kind": "system", "about": <child> }` | Yes |

A `status` event carries only members the gateway writes, such as the state, the error code, the time, and ids. Text the child wrote, such as a `TaskResult`'s output, a failure message, or a question's text, travels in a separate member `childContent` attributed `{ "kind": "agent", "id": <child> }`, which passes the parent's `contentPolicy` and `PreMessageDelivery` as any child event does (decision 28). A `tool_call` event passes the same checks.

An event of a class or kind in the parent's `wakeOn` follows MD-22: it is steered into an open turn on a runtime that declares `steer`, waits for the turn's end on any other runtime, and opens a turn, waking a `dormant` parent, when the parent has none. An event of any other class is steered into an open turn or waits for the turn's end in the same way, and otherwise waits in the inbox for the parent's next turn, opening none and waking nothing. `events.peek` sees it, and `events.wait` returns it under MD-22's rules for its event. `session.create` takes `wakeOn`, a list of event classes and event kinds, so a parent can leave out one kind, such as `expired`, and keep the rest of its class (decision 25). A runtime's or an operator's default lease sets its default, and a parent changes it for a live child with `session.set_limits`. `wakeOn` is a setting of the parent, which never widens what the grant allows, so it is no caveat. The `from` member lets a runtime show its model a fact the gateway reports apart from words a child wrote, so a child cannot write a message that reads as a report from the platform. A client of a top-level session sees the same distinction on its event stream, where a session's words arrive as `output` events and the gateway's reports as `state_change` and the other session events.

**Recovery rules.** No ordering across a tree remains in recovery: each session recovers under 0146, in parallel with the rest of its tree (decision 15). These rules replace what the bottom-up order implied:

- A covered session that commits out of `resume_pending` or `resuming` into any state other than `running` answers a pending `events.wait` with its state, and a covered session in `awaiting_client_action` counts as answered when the wait is called.
- At a parent's restore, under its delivery lock, SL-12 resends each question still pending in its elicitation record with its original id, and an `awaiting_client_action` event while the child stays in that state, beside the `ended` and `end_of_turn` events it resends today.
- A question's record holds the hop where it sits. `questions.forward` moves it under both delivery locks, SL-12 resends it only to that hop, and its event carries `originSessionId`.
- When a question expires under §9.2 item 6, the gateway removes its `pending` entry from the parent's inbox, or writes a `question_withdrawn` child event when it was delivered. `questions.answer` on an expired or answered question returns `expired` or `answered`.
- §9.2's per-hop forwarding timeout does not run while a question waits at a parent, and `maxElicitationWait` bounds it there.
- Events that a wait takes are `delivered` with the wait's call id. They become `consumed` when the adapter acknowledges the result, return to `pending` when a pod loss or a coordinator takeover finds no acknowledged result, and fall under SL-12's resends once consumed.
- After the resends, at the restore's commit, the gateway writes `children_reattached` as a system message that lists each child's state and labels and names each pending question by its `questionId`, with no text the child wrote.

A tree whose parent cannot recover holds its slot and budget for `maxResumeWindowSeconds` plus `maxAwaitingClientActionSeconds`, and its children keep running with no session to answer them, where `maxTreeRecoverySeconds` used to fail the tree. The carved budget bounds what the children spend meanwhile.

**Events on a tool connection.** Child events reach the parent's runtime connection as inbox messages, and reach a tool connection as the result of `events.wait` or `events.peek`. An extension that runs as its own MCP server, as in Example 3, follows events with `events.peek`, which leaves them in the inbox for the runtime and adds no activity of its own under SL-6, and takes them with `events.wait` when it acts on them. A parent with a `running` descendant stays awake under SLR-9 of 0146 in any case, so `events.peek` matters for a parent whose children are idle.

**Later additions.** A later change can add a way for a child to join a parent other than `session.create`, such as an actor outside the pod attaching a child to a session, and the child event that announces it (decision 2). DPR-12 keeps such an addition compatible with an extension written against this change. Each child event carries `kind`, and an operation's result and a child's record may gain members. The reference extension ignores an unknown event kind and an unknown member, as the leniency rules of MD-24 treat a runtime frame, and it handles a child in `session.list` or in an event that it did not create, with labels it did not set. The gateway delivers a child event kind that the parent must act on, and creates a child by any path other than `session.create`, only for a parent whose runtime declares that kind or that path in `capabilities.childEvents`, a list capability of the event kinds and creation paths beyond this change's that the runtime handles, so a runtime that predates the addition never receives a child or a question it cannot handle. `capabilities.childSessions` stays the boolean of MD-22. An event that needs no action, such as a notice, reaches every parent.

### DP-5 Reference extension

The reference extension implements the tool set of MD-23, under the names of "Names" below, over the operations of DP-1 and DP-4 and ships in the runtime SDKs and the reference MCP bridge **[design]**. It keeps MD-22's rendering rule, under which the runtime shows its model the sender of each child event, and `capabilities.childSessions` stays the runtime's declaration that it does. The extension owns `discover_agents`, which it answers from `runtimes.list`. An operator who wants other orchestration, such as a fixed fan-out or a workflow engine, writes another extension over the same operations.

The extension consists of a library and a tool set **[design]**:

- **A library.** Each runtime SDK gains a `sessions` module whose client wraps the operations of DP-1, such as `Sessions.create` for `session.create`. A runtime that orchestrates in its own code calls the library and offers its model no session tools. The library also holds a relay, which forwards a child's events to another child with `session.send` by rules the parent's code sets, with no model call, and a `ToolSet`, which runs the handlers of the tools a parent offers its children and answers each `tool_call` event with `tools.result`.
- **A tool set.** The reference MCP bridge serves the tools below over the library and lists them in its `platform` server. It lists `start_session` and `discover_agents` when the session's availability has `delegation: true`. It lists the tools that manage children, and the tools that answer or forward a child's question or answer its tool call, while the session has a child, which it learns from `session.list` when it starts and after each `availability_changed`. It lists `reattach_session` and `release_session` when `leave` allows them, and `import_file` when a message or a child event delivers the session a file part. The bridge's option `--session-tools` takes `reference`, the default, or `none`, and its option `--session-defaults` names a file of default `session.create` arguments for each target, such as empty `credentialBindings` for a runtime that must receive none, which the model cannot override. A runtime author who ships another tool set passes `--session-tools none` and adds the other MCP server to the harness configuration.

**Names.** The tools and the operations are named for what they act on, a session, an event, a question, a file, a tool call, or a runtime, rather than for the caller's relation to the session (decision 16). The operations take the namespaces `session.*`, `events.*`, `questions.*`, `files.*`, `tools.*`, and `runtimes.*`. A session the caller reaches is a child today, and whatever a later change adds under DPR-12, with no rename. `send_update` and `ask_question` keep their names, because they address the session's own client.

| MD-23 name | Reference tool | Operation |
| :-- | :-- | :-- |
| `start_child_session` | `start_session` | `session.create` |
| `message_child_session` | `send_message` | `session.send` |
| None | `interrupt_session` | `session.interrupt` |
| `await_child_sessions` | `await_sessions` | `events.wait` |
| `list_child_sessions` | `list_sessions` | `session.list` and `session.get` |
| `close_child_session` | `close_session` | `session.close` |
| None | `reattach_session` | `session.reattach` |
| None | `release_session` | `session.release` |
| None | `answer_tool_call` | `tools.result` |
| None | None, the library alone | `session.self` |
| `import_child_session_file` | `import_file` | `files.import` |
| None | `answer_question` | `questions.answer` |
| None | `forward_question` | `questions.forward` |
| `discover_agents` | `discover_agents` | `runtimes.list` |

The MCP adapter's client-facing tools of 0138 include `send_message` and `close_session`. Those run on a client's connection to the adapter, and the reference tools run inside a pod under the bridge's `platform` server, whose prefix a harness shows, so the two never meet on one connection.

`list_sessions` takes MD-23's depth option through `session.list`. `start_session` passes `runtimeOptions` and `labels` through, and sets an idempotency key derived from the MCP tool call, so a call that the harness or the bridge retries creates no second child. A model that decides again, after a restore, to start a child it already started makes a new call, and the reference tool set gives it no protection against that. An orchestrator that needs it sets its own key from its work item, as Example 2 does.

### DP-6 Disposition of each setting

| Setting | Source | Disposition [design] |
| :-- | :-- | :-- |
| `maxDepth` | §8.2, §8.3 | Primitive, as a security caveat. |
| `maxChildrenTotal`, `maxParallelChildren`, `maxTreeSize`, `perChildMaxAge`, `fileExportLimits` | §8.2, §8.3 | Primitive, as resource caveats, with tenant and environment ceilings (DP-1). |
| `maxTokenBudget` and the reservation model | §8.3, 0133 LU-6 | Primitive, as the carved budget over the budget objects of 0170 (DP-3, decision 12). |
| Lease extension and its approval modes and layering | §8.6, 0133 LU-6 | Primitive, for the resource caveats only (DP-3). |
| `onParentEnd` (formerly `cascadeOnFailure`) | §8.10, 0146 SL-9 | Primitive, as a security caveat that a child may only make stricter (DP-2). |
| `runtimeOptions` on the delegation `TaskSpec` | 0140 | Primitive, as an argument of `session.create`. |
| Elicitation forwarding through the virtual child interface | §8.2, §9.2 | Primitive, as the `question` child event with `questions.answer` and `questions.forward` (DP-4). |
| `children_reattached` and the re-await protocol | §8.2, §8.10 | Kept as a system message after a restore that lists each child's state, labels, and pending question, under the recovery rules of "DP-4". The re-await protocol is replaced by SL-12's resends. |
| `minIsolationProfile` and isolation monotonicity | §8.3 | Primitive, as a caveat. |
| `delegationPolicyRef`, `maxDelegationPolicy`, derived-runtime policy | §8.3 | Primitive, as the policy cap. Its rules apply at child creation, which also closes F-8.3.14. |
| Environment and sharing paths | 0149 TS-3 | Primitive, as a caveat (DP-2). |
| The cascade when a parent ends, and orphan cleanup | §8.10, 0146 SL-9 | Primitive (DP-2), with a tree terminate for a `tenant-admin`. |
| Child event delivery and the wait, with its modes | 0135 MD-22, 0146 SL-12 | Primitive (DP-4). |
| Approval of a child by a person | 0142 SO-5a | Kept in 0142, matching `session.create` (DP-7), and added to a `DelegationPolicy` rule as `approval` and `approvalAboveCreditBudget` (decision 8). |
| `contentPolicy.maxInputSize` and `interceptorRef` at `PreDelegation` | §8.3 | Primitive. `PreDelegation` folds into the `PreRoute` chain, which fires at child creation for `kind: delegate`, so one chain sees every creation (decision 4). |
| `scanExportedFiles`, `maxExportedFileSize`, `fileExportLimits`, and the weakening cooldown on export scanning | §8.3, §8.7 | Primitive. The gateway materializes exported files in the child's workspace (decision 4). |
| `messagingRateLimit` | §8.3 | Primitive, as a resource caveat that counts what MD-9 counts. |
| `userCredentialBindings`, named bindings, and `egress.allowedHosts` passed to a child | 0151, 0127 | Primitive, as security caveats. `session.create` narrows each, and by default passes what 0127 "Effective policy" passes today. |
| Placement requirements of a child | 0136 | Primitive, through the parent link (DP-2). |
| `allowSelfRecursion` at three layers and `gateway.cycleDetection.mode` | §8.2, §8.3 | Dropped. `maxDepth` and the budgets bound recursion, and an orchestrator that wants cycle avoidance applies it in the extension with the `lineage` the gateway delivers in `session_start` and `session.self` (decision 5). |
| `treeVisibility` and its check against `messagingScope` | §8.3 | Dropped. The operations of DP-1 reach the caller's own children only, and MD-9 of 0135 already removes the `siblings` messaging scope, the value the compatibility check concerns. |
| `perChildRetryBudget` | §8.3 | Moved to the extension. An orchestrator retries a failed child by creating another. |
| `delegationPresets` | §8.3 | Kept as operator-defined default leases on a runtime, a tenant, or an environment, which feed the root grant inputs (DP-1). |
| `defaultDelegationFraction` | §8.3 | Kept as the share of the parent's remaining budget a child receives when `session.create` names no budget, settable on the same levels. |
| `snapshotPolicyAtLease` | §8.3 | Dropped. The policy cap is read live at each `session.create`, as 0126 reads availability (decision 5). |
| `maxTreeMemoryBytes` and the virtual child interface memory model | §8.2 | Dropped. The gateway's internal memory is not a lease value, and the virtual MCP child interface goes with the gateway-hosted tools. |
| `approvalMode` | §8.4 | Dropped. 0126 removes it as an argument, and its `approval` value is reserved and unimplemented. |
| Bottom-up tree recovery and its timeouts | §8.10 | Dropped. Each session recovers as a session under 0146, under the recovery rules of "DP-4" (decision 15). |
| `credentialPropagation` | §8.3 | Already removed by 0133 LU-4. |
| `experimentContext` | §8.3 | Already replaced by routing labels under 0141 RT-3. |
| `allowedExternalEndpoints` and the `connector` target type | §8.3 | Already removed by 0124 CR-1. |
| `tracingContext` and `set_tracing_context` | §8.2, §8.3 | Replaced by child labels. 0135 already removes the tool, and an extension passes tracing identifiers as child labels, which reach the child's runtime in `session_start` and never the model's prompt (DP-1). |
| Availability levels of 0126 | 0126 DA-1 to DA-6 | Folded into the root grant inputs: a session with no grant has no session tools from the extension and starts no child (decision 6). |
| Environment `defaultDelegationPolicy` | §10.6, 0149 TS-3 | Primitive, as a root grant input applied at every `session.create` (DP-1). |

### DP-7 What this change supersedes

| Change | Parts superseded [design] | Parts kept |
| :-- | :-- | :-- |
| [0122](../0122-client-tools/change.md) | The turn-ending exchange of T1 and CT-1's `clientToolCalls`, which the blocking call of "Tools a client offers" replaces; T3, under which no reference runtime declares client tools; CT-5's recording of pending calls on an `idle` session, `pendingClientToolCalls`, the `tool_use` event with `target: "client"`, `abandonPendingToolCalls`, the hold rule, and the delivery rules for results, rule 6 included, which a parent's `tools.result` replaces for a parent; CT-7's copy of a pending set on a derive, since a derive copies no open call; and CT-10's rule that the SDKs add no blocking helper (decisions 23, 24, and 29). | T7 and CT-9, under which a child never receives its parent's client tools; T16, under which a client tool's call in a turn that another session opened is refused, with a client's `allowInOtherTurns` on a tool added; `conversation.clientTools` and `conversation.toolChoice` on a message; admission (CT-3); validation (CT-4); the cold-history rules of CT-7 for a late result; interceptors (CT-8); and the provider mappings, which move to the adapters under 0138. |
| [0126](../0126-delegation-availability/change.md) | DA-1 to DA-6 become root grant inputs and the extension's tool list. The tool list rules for child tools move to the extension. | DA-7 and the list rules for `ask_question`, which concern tools other than the child tools. DAR-6, under which a parent with children keeps the tools that manage them. The refusal `DELEGATION_DISABLED` with `disabledBy`, given at grant creation and at each `session.create`. |
| [0135](../0135-message-delivery/change.md#md-22-parents-and-children) | MD-23's tools as gateway-hosted platform tools, which move to the extension under generic names ("DP-5", decision 16), with tools added for questions, tool calls, leaving, and interrupt. MD-22's child events gain classes: a `status` event carries `from: { "kind": "system", "about": <child> }` with child text in `childContent`, and only a class or kind in the parent's `wakeOn` opens a turn or wakes the parent (decisions 17, 25, and 28). MD-22's wait rules gain the recovery rules of "DP-4" (decision 15). MD-25's watch of a child in another environment carries only what TS-3 "Model" lists (decision 34). MD-26's import extends to a file part in any message one session sends another, with the delivered-part check (decision 18). MD-1 gains `wake: false` for every sender (decisions 19 and 22). | MD-9, under which sessions message each other only along the tree (decision 30); MD-22's one session model, child events with the `question` event added, rendering, and `capabilities.childSessions`; and the rest of MD-25 and MD-26. |
| [0146](../0146-session-lifecycle/change.md#sl-8-parents-children-and-their-states) | SL-9's `onParentEnd` becomes a security caveat, and a force-terminate closes a tree whatever it holds. SL-8 to SL-12 are restated for a session with a parent link, SL-8's parent-state table follows the event classes and `wakeOn` of "DP-4", and SL-12's recovery set gains pending questions, `awaiting_client_action` events, pending tool calls, and the events a wait took, under the recovery rules of "DP-4" (decision 15). SL-8's bound on a question waiting at a parent becomes `maxElicitationWait`. SL-6's qualifying activity excludes `events.peek`, and counts an open client tool call only until `clientToolHoldSeconds` (decisions 10 and 24). | Every other part, SLR-9 included. |
| [0142](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | SO-5a's rules and the delegation guardrail name `session.create` in place of `start_child_session`, because the extension's tools do not reach the gateway. A security record that names `start_session` in `approvals.tools` is refused with `400 VALIDATION_ERROR`. SO-5a's rules can match a parent's tools by name, or all of them with `approvals.parentTools: all` (decision 31). SO-2's record gains a forbid of parent tools, and its weakening rules classify a widening of `leave`, `leaveMaxDepth`, or `offerTools`, and the lifting of the parent-tools forbid, as weakenings. | Every other part. |
| [0127](../0127-egress-and-git/change.md#effective-policy) | What a child inherits stays the default, and `session.create` may narrow it. | Every other part. |
| [0138](../0138-rest-only-client-api/change.md) | None superseded. The REST API gains `wake: false` on a send, `POST /v1/sessions/{id}/reattach` and `POST /v1/sessions/{id}/release` for the root's client, `409 SESSION_HANDED_OFF` and `successorSessionId`, the session events `child_reattached`, `child_released`, `handoff`, `tool_call`, and `tool_call_withdrawn` with their webhook deliveries, a route by which a client answers a `tool_call`, and `clientToolHoldSeconds`. | Every part. |
| [0139](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames) | RP-3's `server` takes `client` beside `platform`, and a `tool_call` gains `group` with its size. RP-4's `toolServers` gains `client`, and `availability_changed` also fires when the client's tool list changes. | Every other part. |
| [0144](../0144-session-derive/change.md) | A derive from a parent with children delivers a system message in the derived session's first delivery saying that the children did not carry over (decision 11), and a derive copies no open client tool call. | Every other part. |
| [0153](../0153-workspace-materialization/change.md#wm-12-mid-session-uploads-and-child-file-imports) | WM-12's mid-session overlay also takes a parent's `materialize: true` file part, whose bytes come from the parent's workspace export under §8.7, gated on the child runtime's `midSessionUpload` (decision 18). | Every other part. |
| [0136](../0136-placement/change.md) | None. A child is created with `kind: delegate`, so its parent's placement requirements apply. | Every part. |
| [0140](../0140-runtime-revisions/change.md#rv-1-revisions) | `runtimeOptions` moves from the delegation `TaskSpec` to `session.create`, with the same validation. | Every other part. |
| [0147](../0147-tenant-derived-runtimes/change.md#fields) | None. A tenant-derived runtime's `delegationPolicyRef` is a root grant input. | Every part. |
| [0149](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) | TS-3's delegation gate, which becomes the grant's environment caveat, and decision 26, which becomes a client input to the grant. The environment's `defaultDelegationPolicy` applies at every `session.create`, and the environment gains `delegationCeilings`. | The environment model and sharing paths. |
| [0143](../0143-service-account-credentials/change.md) | None. A child's principal and `caller_type` follow from the parent link. | Every part. |
| [0151](../0151-user-credential-delivery/change.md) | `userCredentialBindings` moves from the lease to the grant. | Every other part. |
| [0133](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets) | The tree scope of LU-6 is restated as the carved budget over the budget objects of 0170 (decision 12). | Every other part. |
| §13.3 token exchange | The `a2a_delegation` child minting, which retires (decision 2). | Every other part. |
| §9.2 elicitation chain | The per-hop forwarding timeout does not run while a question waits at a parent, a question's record holds the hop where it sits, and an expired question is withdrawn from the parent, under the recovery rules of "DP-4" (decision 15). | Every other part. |

### DP-8 Parity with a client

Under MD-22 a child cannot tell a parent from a person: its parent is its client. This section compares what a client of a top-level session does with what a parent does through the operations of DP-1 **[design]**.

**From the client to the session.**

| A client of a top-level session | A parent | Status |
| :-- | :-- | :-- |
| Creates the session with a first message, labels, and `runtimeOptions`. | `session.create`. | Same. |
| Sends a message with `steer`, `queue`, or `interrupt`. | `session.send`. | Same. |
| Sends idempotently (0138). | `session.send` with `idempotencyKey`. | Same. |
| Interrupts the open turn without a message (0138). | `session.interrupt`. | Same. |
| Retracts a `pending` message. | The `retract` frame (MD-11). | Same. |
| Reads the outcome of its messages. | A notice for each failed outcome (MD-22). | Same. |
| Reads the session's messages and transcript. | `session.messages`. | Same, as the stored history of what a watch carries, under the watch's checks, with the crossing rule of DPR-19 (decision 34). |
| Reads the session's usage. | `session.get`. | Same. |
| Attaches to the event stream with streamed parts and activity. | Its runtime watches the child's stream (MD-25). | Same for a runtime. An extension on a tool connection follows events with `events.peek` and receives no streamed parts. |
| Downloads the session's files. | `files.import` (MD-26). | Same. |
| Uploads files to the running session (WM-12). | A file part with `materialize: true` on `session.send`. | Same (decision 18). A parent and its child send each other files by reference, which the recipient imports. |
| Offers its own tools to the session (0122). | `tools` on `session.create` and `session.send`, behind the switch of "Tools a client offers". | Same, through one interface for every client (decision 23), with a fenced child receiving none by default (decision 31). |
| Sends context that needs no answer and starts no turn. | The no-wake choice on `session.send`. | Same: a client and a parent gain `wake: false` together (decisions 19 and 22). |
| Answers an elicitation. | `questions.answer`, or `questions.forward`. | Same, except a human-required elicitation, which skips the parent (MD-22). |
| Approves a harness approval. | None. | An approval always goes to a person watching the root (MD-19). |
| Suspends and resumes the session. | None. | Only the root's client suspends and resumes (0146 SL-4). |
| Closes or cancels the session. | `session.close`. | Same. |
| Derives a session from it (0144). | None. | After v1 (decision 14). |
| Takes a sub-agent over directly. | Reattaches or releases a child. | The root's client reattaches or releases a descendant with a REST route, and a parent its child with an operation, under the same caveats (decisions 32 and 33). |

**From the session to the client.** A child uses the same frames and tools toward its parent as toward a person: `send_update` and a mid-turn `message` for progress, an end-of-turn `message`, `complete: true`, `ask_question`, and output files. A child's message to its parent takes the parent's default intent, so it is steered into an open turn on a runtime that declares `steer`, and it never interrupts, as a message to a person never does (MD-1). A person reads a progress update for free, and so does a parent by default: an `update` event opens no turn and wakes no parent, and a parent that wants to react to updates adds `update` to `wakeOn` ("DP-4", decisions 17 and 25).

**Tools a client offers.** One interface serves every client that offers tools: a REST client, a provider adapter of 0138, and a parent (decision 23) **[design]**. It replaces the turn-ending exchange of 0122 T1:

- **Declaration.** A client declares tools on a message in `conversation.clientTools` and `conversation.toolChoice`, as 0122 CT-1 states, and a parent also on `session.create`, for every turn of the child. The runtime receives the definitions on the message, so a runtime that runs its own model loop puts them in its model request, and the reference bridge serves them as a `client` tool server, named by RP-3's `server` member, whose list it refreshes on `availability_changed`, so a harness sees typed tools.
- **Call.** The runtime calls a tool with a `tool_call` frame that names `server: "client"`, and the call stays open. The gateway validates the arguments with CT-4's schema compiler and delivers a `tool_call` event to the client: on a REST client's event stream, and to a parent as a child event of the class `tool_call`, which carries `from.kind: agent`, passes the parent's `contentPolicy` and `PreMessageDelivery`, is in the default `wakeOn`, answers `events.wait`, and is held across recovery as a question is under the recovery rules above. The client answers with a result, `tools.result` for a parent, and the open call returns it to the runtime.
- **Hold time.** An open call counts as the runtime's activity under SL-6 for up to `clientToolHoldSeconds`, 300 by default, with a ceiling a tenant sets and the platform caps (decision 24). When the hold passes, the gateway withdraws the call with `tool_call_withdrawn`, the runtime receives an error result, and the session may go `dormant`. A result the client sends afterwards follows "Late results".
- **Parallel calls.** A runtime that issues several calls at one model step marks them with one `group` and its size, and the bridge does so for the calls a harness makes in one step. The gateway delivers a group's calls in one event, so an adapter returns them in one provider response, as the provider's own tool use carries them.
- **Late results and cold continuation.** When an open call is gone, because the runtime's pod was lost, its hold passed, or a provider adapter continues the conversation cold under 0138, the gateway withdraws the call with a `tool_call_withdrawn` event. A result the client sends afterwards reaches the runtime as `toolResults` on its next message, with the history rules of 0122 CT-7, so a runtime that runs its own model loop continues from it and a result is never lost (DPR-13).
- **Provider adapters.** An adapter returns a group of calls to its caller as the provider's tool use, in Chat Completions, Responses, or the Messages API, and posts the results when the caller's next request brings them, finding the session through 0138's response identifier or conversation fingerprint. A caller that never returns lets the calls' hold pass.
- **Parent tools.** A parent's handler runs with the parent's credentials, egress, workspace, and grant, so a child steers actions that carry the parent's authority. Parent tools are off unless `gateway.delegation.parentTools.enabled`, which is `false` by default, the tenant, the environment, the security guardrail, the security caveat `offerTools`, which only narrows from parent to child, the child's revision, and the call all allow them (decision 23). A fenced child receives none unless its `session.create` sets `offerTools: allow` within the grant (decision 31). A security administrator can require a person's approval before a parent tool's call reaches the parent, through SO-5a rules that name parent tools or `approvals.parentTools: all` (decision 31). Each call counts against the child's resource caveat `toolCallRateLimit`, a result does not count against the parent's `messagingRateLimit`, and the gateway audits each call as `delegation.tool_called` with a digest of its arguments (decision 28). The SDK library's `ToolSet` runs the handlers in code, and a model-driven parent answers with the reference tool `answer_tool_call`. `questions.forward` refuses a tool call, delivery to the parent is at least once, so a handler keys its side effects on `callId`, and a parent that ends answers its open calls with an error, as a leave does with `CLIENT_CHANGED`.
- **Client tools stay with their client.** A child never receives its parent's client tools (0122 T7 and CT-9), so a parent offers a child only tools it serves itself. A client tool's call in a turn that another session opened, such as a turn a child's result opened, is refused with `no_tools_declared` (0122 T16), unless the client marked the tool `allowInOtherTurns: true` (decision 29). A parent's code therefore cannot pass a child's request to the user's client tools in a turn the child's event opened.

**Between children.** Children message each other through their parent, so every message runs along one edge of the tree and MD-9 of 0135 holds as written (decision 30). A child's event reaches the parent, and the parent sends it on with `session.send`. The SDK library's relay does this by rules the parent's code sets, such as "forward each `result` of the `researcher` child to the `writer` child", with no model call. Each relay send counts against the parent's `messagingRateLimit`, which the parent's grant sizes, and each hop passes both sessions' content checks and both environments' rules, as any message between a parent and a child does. A group chat is the parent acting as its manager: it holds the transcript, picks the next speaker, and sends that member what it missed. A parent with a running child stays awake under SLR-9 of 0146, so a relay costs no restore.

## Worked examples

Each example states what the operator configures, what the runtime author ships, what the client sends, and what happens at each step. Every example is **[design]**. The examples follow the owner's decisions in `decisions.md`. They use field names from other draft changes where those changes define them: `allowDelegation` from 0126 "DA-1", `capabilities.childSessions` from 0135 "MD-22", the security record from 0142 "SO-2", `credentialBindings` from 0151, `maxCreditBudget` from 0133 "LU-6", and service accounts from 0143. The code is illustrative. It shows which calls an author makes and does not fix an SDK interface.

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
  "delegationLease": { "maxDepth": 1, "maxChildrenTotal": 4, "maxParallelChildren": 2, "maxCreditBudget": 400000 }
}
```

**What happens.**

1. The gateway resolves the root grant inputs of DP-1: availability (`allowDelegation: true` on the runtime), the policy cap (`coding-helpers`, intersected with any tenant or environment policy), and the client's values lowered to any ceiling. It creates the grant with depth 1, 4 children, 2 in parallel, 400,000 credits, the isolation floor `sandboxed`, and the session's environment, and writes `delegation.grant_created`.
2. `session_start` carries `availability.delegation: true`. The bridge lists `start_session`, `discover_agents`, and, once a child exists, the tools that manage children and answer their questions.
3. The model calls `start_session` with `target: "code-reviewer"`, a task, and `runtimeOptions: { "model": "haiku" }`. The bridge exports `review/**` and calls `session.create`. The gateway checks the grant, admits `code-reviewer` because its label matches `coding-helpers`, validates the options against the revision's schema, checks that its pool is at least `sandboxed`, carves the child's budget from the root's, and creates the child with `parentSessionId` set to the root and the session label `project: checkout`. The child's remaining depth is 0, so its grant allows no child.
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

**Runtime author.** The `repo-audit` runtime is a Python program that uses the SDK's `sessions` library and runs no MCP bridge. It keeps no state that a restore can lose: each child carries its directory in a child label, each creation carries an idempotency key, and the finished reports are files in the workspace.

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
        attempts = Counter(c.labels["dir"] for c in children)
        running = {c.labels["dir"]: c.session_id for c in children if c.state not in TERMINAL}
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
                    limits={"maxChildrenTotal": 0, "maxCreditBudget": 50000},
                    questions="forward",               # this program answers no questions
                    labels={"dir": d},
                    idempotency_key=f"{d}#{attempts[d]}",
                )
                running[d] = c.session_id
            for e in sessions.wait(mode="any", max_entries=MAX_IN_FLIGHT):
                d = e.labels["dir"]
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
  "delegationLease": { "maxDepth": 1, "maxChildrenTotal": 60, "maxParallelChildren": 8, "maxCreditBudget": 3000000 }
}
```

**What happens.**

1. Each `sessions.create` is a `session.create` on the platform tool server. The gateway applies the grant to each call: the 61st child is refused because the child count is spent, a ninth child that is `running` or `idle` is refused because the parallelism is spent, and a child whose request asks for more than the credits left is refused. `limits.maxChildrenTotal: 0` gives each auditor no room to delegate. The program waits with `events.wait`, so the default `wakeOn` suits it: an auditor's progress updates open no turn in the parent.
2. A `dir-auditor` is a `multi_turn` runtime, so it ends its turn with its answer and stays `idle`. The program imports the report and closes the child, which frees its parallelism and returns its unused credits.
3. The node that runs the parent fails. The parent restores on a new pod, receives SL-3's resume message, and runs `on_message` again. `sessions.list()` returns every child with its `dir` label, so the program counts attempts, finds the children still running, and skips the directories whose reports exist. A `sessions.create` that the old process sent and the gateway committed before the failure is repeated with the same idempotency key and returns the same child.
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

**Runtime author.** The team ships `acme-orchestrator`, a stdio MCP server built with the TypeScript runtime SDK, in the base image. Its `Sessions.dial()` opens a tool connection to the adapter as `lenny-mcp-bridge` does under RP-5, so it holds no credential. It reads `lineage` and the child labels with `session.self`, which returns what the gateway wrote in `session_start`, because a tool connection never receives that frame, so no ancestor's file can change them:

```ts
import { Sessions } from "@lennylabs/runtime-sdk/sessions";

const ROLES = { planner: "acme-planner", coder: "acme-coder", tester: "acme-tester" };
const sessions = await Sessions.dial();
const { lineage, labels } = await sessions.self();       // session.self
const traceId = labels.traceId ?? newTraceId();

server.tool("assign", { role: "string", brief: "string" }, async ({ role, brief }, call) => {
  const target = ROLES[role];
  if (lineage.includes(target)) return toolError(`${role} already ran in this branch`);
  const task = [{ type: "text", inline: brief }];
  const create = (attempt) => sessions.create({
    target, task,
    labels: { traceId, role },
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

**Client.** `POST /v1/sessions` with `"runtime": "acme-planner"` and `"delegationLease": { "maxDepth": 3, "maxCreditBudget": 1500000 }`.

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
  "ceilings": { "maxDepth": 3, "maxChildrenTotal": 20, "maxParallelChildren": 5, "maxCreditBudget": 2000000 }
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

The tenant administrator also creates tenant-derived runtimes of approved helpers under 0147 and labels them `acme-approved: "true"`, and `access: read-only` where they are.

**Client.** A contractor in `contractors` creates a session of `claude-code` and asks for `"delegationLease": { "maxChildrenTotal": 10 }`.

**What happens.**

1. The gateway resolves the root grant. The policy cap is the intersection of the runtime's `coding-helpers` (Example 1), the tenant's `acme-approved`, and the environment's `acme-readers`, so a child must carry all three policies' labels. The child count is the client's 10 lowered to the environment's ceiling of 4. The depth, parallelism, and budget take the runtime's defaults, lowered to the tenant's ceilings. `delegation.grant_created` records each value and the level it came from.
2. The contractor's agent starts a read-only reviewer, which the cap admits. An attempt to start a helper that writes to repositories is refused at `session.create`, because `acme-readers` does not admit it. A fifth child is refused because the child count is spent.
3. A session of the same runtime in another environment of the tenant may start any approved helper, up to 20 children.
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

This example marks the boundary of the design. A later change that lets an actor outside the pod attach a child to a session (decision 2) would let the engine create the implementation sessions as the plan session's children, so they would share its budget and close with it. The budget objects of 0170 (decision 12) would let the engine run every root session under one budget. This example works without either.

## Limits, and alternatives rejected

**Limits [design]:**

- An operator who replaces the reference extension can give a model a tool set that behaves differently. The guarantees of DPR-1 to DPR-19 hold whatever the extension does, the audit of DPR-11 records which connection created each child, and the extension's behaviour is the operator's responsibility.
- A check an extension makes with child labels or `lineage`, such as Example 3's role check, binds a child only while the child's runtime runs that extension.
- A runtime that keeps an old tool list keeps session tools after its grant is removed, as under 0126 "Limits". The gateway refuses the call.
- The primitives do not prevent an orchestrator from building a deep or wide tree within its caveats. The caveats and the carved budget bound the tree.
- An idempotency key protects against a repeated call. It does not stop a model that decides again to start a child it already started.
- `perChildMaxAge` runs in every non-terminal state of a child under SL-10 of 0146, `dormant` included, so a tree left dormant for longer than that age loses its children, and by default each expiry wakes the parent (decisions 13 and 17). A deployment that keeps trees for days sets `perChildMaxAge` accordingly or leaves it unset, leaves `expired` out of `wakeOn`, or has the parent raise the age with `session.set_limits`.
- A session derived under 0144 from a parent with children starts with none, while its restored workspace and transcript may name them. Its operations on those children are refused as not its own, and its first delivery carries a system message saying that the children did not carry over (decision 11).
- No filter keeps a compromised child from encoding what it read in a message to its parent. Within one environment a child reads nothing its parent cannot reach (DPR-3), and across environments the sharing decision of TS-3 bounds what a child reads, while `PostAgentOutput` screening is best-effort (decision 34).
- A relay between children costs the parent a `session.send` per hop, counted against its `messagingRateLimit`, and a model-driven relay costs a turn per hop (decision 30).
- An open client tool call holds the calling session's pod for up to `clientToolHoldSeconds` (decision 24).
- A released session leaves its tree's controls by design. `leave`, `leaveMaxDepth`, and the tenant's session quotas bound it, and a reattached session stays inside them (decision 32).

**Alternatives considered:**

- **Keep built-in delegation and drop the orchestration settings only.** The disposition table of DP-6 applies equally to this option, and it costs less design work. Each later feature still states a rule for a delegated child, the gateway keeps the virtual child interface and the tool set, and an operator who wants another orchestrator still has no path that keeps the pod free of credentials. It was option (b) of O1, which decision 1 did not take.
- **Move delegation outside Lenny with no new primitive.** An operator's orchestrator would call `POST /v1/sessions` with a client credential placed in the workspace. Every guarantee of §8.1 would then depend on the operator's handling of that credential, which is the bypass 0126 records. This option is rejected.
- **Express the grant as a bearer token held by the pod.** The pod would need egress to the gateway's public API, and a stolen token would act outside the session. Decision 2 offers no token form in v1.
- **Edges between children.** A parent-granted edge would let one child message another directly. It contradicts MD-9, adds a loop bound and boundary rules of its own, and lets a fenced child reach a sibling with more authority. Decision 30 keeps the parent as the broker, and edges remain a later addition under DPR-12.
- **Client tools as a turn-ending exchange.** 0122 T1 reaches no harness that runs its tool loop inside one turn. Decision 23 replaces it with a blocking call.

## Specification and non-specification changes

**Specification [design]:**

- [§8](../../spec/08_recursive-delegation.md#8-recursive-delegation) is rewritten around the primitives, and the content it gives up moves to the pages that own it, with successor pointers.
- [§7.2](../../spec/07_session-lifecycle.md#72-interactive-session-model) states the event classes and `wakeOn`, `wake: false` on a message, reattach and release with the `left` event, and the hold of an open client tool call.
- [§9.2](../../spec/09_mcp-integration.md#92-elicitation-chain) states the hop from a child to its parent as the `question` child event with `questions.answer` and `questions.forward`, the hop a question sits at, and the per-hop timeout's exemption at a parent.
- [§10.2](../../spec/10_gateway-internals.md#102-authentication) and [§13.3](../../spec/13_security-model.md#133-credential-flow) state the session grant and the retirement of `a2a_delegation` minting (decision 2). The §10.2 permission matrix gains the tenant delegation settings and the tree terminate for `tenant-admin`.
- [§10.6](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model) applies `defaultDelegationPolicy` at child creation and adds `delegationCeilings`. The tenant security record of 0142 SO-2 gains the parent-tools forbid and `approvals.parentTools`, and its weakening rules cover `leave`, `leaveMaxDepth`, and `offerTools`.
- [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) states the carved budget, the transfer of `session.set_limits`, and the parallelism count by state (decision 12). [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) gains `delegation.grant_created`, `delegation.left`, `delegation.tool_called` with its argument digest, and the new members of `delegation.spawned`.
- [§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema) states that a child takes its parent's session `labels`.
- [§15.1](../../spec/15_external-api-surface.md#151-rest-api) gains `parentSessionId` and `successorSessionId` on a session's representation, `wake: false` on a send, the tenant delegation settings route, `POST /v1/admin/sessions/{rootId}/terminate-tree`, `POST /v1/sessions/{id}/reattach` and `POST /v1/sessions/{id}/release`, the client's route that answers a `tool_call`, the session events `child_reattached`, `child_released`, `handoff`, `tool_call`, and `tool_call_withdrawn` with their webhook deliveries, `409 SESSION_HANDED_OFF`, and `CLIENT_CHANGED`, and the error catalog loses the codes of the dropped settings, such as `DELEGATION_CYCLE_DETECTED`, `TREE_VISIBILITY_WEAKENING`, and `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`.
- [§15.4](../../spec/15_external-api-surface.md#154-runtime-adapter-specification) states `server: "client"` and `group` on `tool_call`, `toolResults` for a late result, the `status` event's `childContent`, and `capabilities.childEvents`.
- [§15.7](../../spec/15_external-api-surface.md#157-runtime-author-sdks) states the SDK `sessions` library with its relay and `ToolSet`, the reference tools, and the bridge's `--session-tools` and `--session-defaults` and its `client` server.
- [§16](../../spec/16_observability.md#16-observability) loses the metrics and events of the dropped settings.
- [§26](../../spec/26_reference-runtime-catalog.md#26-reference-runtime-catalog) states that the reference runtimes that delegate use the reference extension.

**Non-specification [design]:**

- `pkg/delegation/cycle`, `pkg/gateway/platformtools/treerecovery`, `treearchive`, `deadlock`, and the virtual child interface leave, under decisions 5 and 15.
- `pkg/gateway/platformtools/leasecontrol`, `delegationbudget`, and `treebudget` become the grant and the carved budget.
- The runtime SDKs and the reference MCP bridge gain the reference extension.
- The Helm chart loses `gateway.cycleDetection.mode`, `gateway.allowSelfRecursion`, and the tree recovery timeouts, under decisions 5 and 15.

## Documentation

Each page below states every setting, default, limit, failure mode, and runtime-facing rule of this change that its readers meet [design]:

| Page | Content |
| :-- | :-- |
| `docs/runtime-author-guide/delegation.md` | The primitives, the reference extension, writing another extension over the operations, the event classes and `wakeOn`, the relay, group chats with the parent as manager, the recovery rules a parent meets, reattach and release with the rules of a leave, files by reference and `materialize`, and offering tools to a child with the `ToolSet`, the fenced default, and at-least-once delivery. |
| `docs/runtime-author-guide/platform-tools.md` | The operations of DP-1 with their arguments, results, and refusals, the operations a session holds without a grant, `session.self`, and the `client` tool server. |
| `docs/runtime-author-guide/runtime-sdk.md` | The `sessions` library, the relay, the `ToolSet`, the reference tools and their names, `--session-tools`, `--session-defaults`, and `capabilities.childEvents`. |
| `docs/reference/adapter-contract.md` | `server: "client"`, `group`, `toolResults` for a late result, the `status` event with `childContent`, `wake: false`, and `availability_changed` for the client tool list. |
| `docs/reference/error-catalog.md` | `DELEGATION_DISABLED` with the guardrail's parent-tools forbid, `SESSION_HANDED_OFF`, `CLIENT_CHANGED`, the refusals of `leave` and `leaveMaxDepth`, `no_tools_declared` with `allowInOtherTurns`, and the removal of the codes of the dropped settings. |
| `docs/reference/cloudevents-catalog.md` | `delegation.grant_created`, `delegation.spawned`, `delegation.left`, `delegation.tool_called`, and the session events `child_reattached`, `child_released`, `handoff`, `tool_call`, and `tool_call_withdrawn`. |
| `docs/reference/configuration.md` | `gateway.delegation.*` with the platform defaults of `leave` and `leaveMaxDepth`, `gateway.delegation.parentTools.enabled`, the ceiling of `clientToolHoldSeconds`, and the removal of `gateway.cycleDetection.mode`, `gateway.allowSelfRecursion`, and the tree recovery timeouts. |
| `docs/client-guide/delegation-and-tasks.md` | What a client of a root sees of its tree, the reattach and release routes, a hand-off with `successorSessionId`, and pending questions that move to the client on a leave. |
| `docs/client-guide/streaming.md` | `wake: false` on a send, the `tool_call` event and its answer, `clientToolHoldSeconds`, and the session events of a leave. |
| `docs/client-guide/webhooks.md` | The webhook deliveries of `child_reattached`, `child_released`, `handoff`, and `tool_call`. |
| `docs/client-guide/session-lifecycle.md` | `SESSION_HANDED_OFF`, a released session as a root, and the hold of an open client tool call. |
| `docs/operator-guide/multi-tenancy.md` | The root grant inputs with tenant and environment policies and ceilings, `leave`, `leaveMaxDepth`, `offerTools`, `toolCallRateLimit`, `messagingRateLimit`, and the tree terminate for a `tenant-admin`. |
| `docs/operator-guide/security.md` | The security caveats, fenced children, the guardrail's forbids, `approvals.parentTools`, the authority a parent tool carries, and what crosses an environment (decision 34). |

## Tests

Each of DPR-1 to DPR-19 needs `unit` and `integration` tests [design].

- DPR-12's tests feed the reference extension a child event kind, a result member, and a child it did not create, and check that it carries on, and check that the gateway withholds an event kind that needs action from a runtime that does not declare it in `capabilities.childEvents`.
- DPR-2, DPR-3, DPR-5, DPR-10, DPR-16, and DPR-18 need `security` tests that try to widen a security caveat, to exceed a tenant or environment ceiling, to reach the grant from a pod by another path, to leave beyond `leave` or `leaveMaxDepth` or under the guardrail forbid, to escape `maxTreeSize` or `terminate-tree` through `session.reattach`, to offer parent tools to a fenced child, and to call a client tool in a turn that a child's event opened.
- DPR-14 needs a `security` test in which a child writes text that imitates a platform report into a failure and a question, and checks that the text arrives only in `childContent`.
- DPR-17 needs a `security` test that imports a part never delivered to the importer and a `materialize` push over the export limits. DPR-19 needs a `security` test of `session.messages` and a watch of a child in another environment.
- DPR-4 and DPR-9 need `load_local` tests of concurrent child creation against one parent's budget and of concurrent calls with one idempotency key.
- DPR-7 needs a `chaos` test that restores a parent while a child's question, a child's tool call, and an end-of-turn event are pending. DPR-13 needs a `chaos` test that loses the calling session's pod with an open client tool call, then delivers the result late and checks that it reaches the restored runtime as `toolResults`.
- DPR-8 needs a `contract` test of the reference extension and the relay against the platform operations, and DPR-15 a `contract` test of `wake: false` on REST and on `session.send`.
