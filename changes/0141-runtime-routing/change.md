# Runtime routing

Status: `Draft`, revision 21, 2026-11-23. Programs: [v1](../programs/v1.md).

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | Run on 2026-11-03 against this tree through a Go build overlay, with no change to the tree. |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-08, or on 2026-11-09 for the claims revision 7 added. |
| **[design]** | What this design requires. Nothing implements it. |

**Evidence sources.** A *Go build overlay* is the Go toolchain's `-overlay` build flag, which substitutes files at build time and leaves the tree unchanged. The binary size comparison in "Current state" used one to replace `experimentprovider.go` with a stub. "Validation owed" in `steps.md` lists the evidence still missing.

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

Lenny carries a built-in experimentation subsystem in [§10.7](../../spec/10_gateway-internals.md#107-experiment-primitives). It defines an experiment object with variants, a control group, a status lifecycle, and rules for delegated children. It assigns sessions to variants by a percentage hash or by asking an external flag service through vendor SDKs or OFREP. It sizes variant pools from the variant weights, caches assignments in Redis, and stores evaluation scores with a results API that aggregates them per variant.

The product review called the project's scope too broad for its stage, and this subsystem is a large part of that scope. Revision 4 removed only the vendor SDKs. Two observations widen the change:

- Eval storage duplicates what runtime-native eval platforms do, and §10.7 already names those platforms as the primary path.
- Assigning a session to a variant is a special case of choosing the runtime a session runs. The gateway already has a generic hook for that choice: an interceptor at the `PreRoute` phase of [§4.8](../../spec/04_system-components.md#48-gateway-policy-engine) may rewrite the requested runtime. External targeting duplicates that hook with a second configuration surface, a second failure model, and a second credential store.

This design removes the experimentation subsystem and eval storage. It keeps one built-in routing feature, weighted runtime routes, and builds it as a `PreRoute` interceptor that follows the contract an external interceptor follows. It also corrects the `PreRoute` contract, which the code implements more narrowly than the specification states, gives every interceptor phase the same session context, and restricts interceptors to in-cluster services.

Rolling out a new version of one runtime is a related problem with a different owner and a different mechanism. [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) covers it.

## Scope

**Removed:**

- `ExperimentDefinition`, the `/v1/admin/experiments` API, the experiment status lifecycle, and the `ExperimentRouter`.
- The reserved `control` variant, the first-match rule across experiments, and the isolation checks specific to experiments.
- `propagation.childSessions` and the innermost-wins rule for delegated children.
- `targeting.mode: external`, the tenant's `experimentTargeting` block, the OFREP client, the vendor SDK providers, and the targeting circuit breaker.
- The Redis sticky assignment cache and its invalidation.
- Variant pool sizing in the PoolScalingController, including the variant pool formula, the base pool adjustment, and `initialMinWarm`.
- `experimentContext` on `session_start`, the adapter manifest, the delegation lease, the adapter proto, and the runtime SDKs.
- Eval storage: `POST /v1/sessions/{id}/eval`, `EvalResult`, `EvalResultStore`, the results API with its blinding rules, the eval erasers, and the `lenny_eval_aggregates` materialized view.
- `evalRef`, which [`0144-session-derive`](../0144-session-derive/change.md) moves from replay to derive ("RT-1").
- The reserved `ExperimentHealthEvaluator`, the manual rollback triggers, and the guidance on combining Lenny with external experimentation platforms.
- The experiment metrics, operational events, audit events, and alerts.
- The `experiment_id` and `variant_id` fields of billing events, and the `variant_id` label on session metrics.

**Added or changed:**

- A single `PreRoute` payload for every session creation path, including delegation and derive, with a `kind` field and the fields routing needs ("RT-2").
- Derive runs the `PreRoute` chain like a create, and its own checks run after the chain ("RT-2").
- One post-chain point, after the chain and revision selection and before the pod claim, at which every check that depends on the runtime, the revision, or the pool runs, including the checks other designs own ("RT-2"). For `kind: delegate` it runs the checks of a child's creation that [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-1-session-grant) states, and the `PreDelegation` phase folds into the one `PreRoute` chain (decision 4 of 0166).
- The same session context on every interceptor phase after creation, and a record of which interceptor last changed a payload ("RT-2").
- Routing labels, a gateway-owned map on the session that interceptors write and runtimes read ("RT-3").
- Runtime routes, a built-in `PreRoute` interceptor that spreads the sessions requesting a runtime across target runtimes by weight ("RT-4").
- Interceptor endpoints restricted to the cluster, with mTLS required outside dev mode ("RT-5").
- Interceptor registration scoped to a list of tenants ("RT-6").
- Operator documentation for running an experiment with these parts ("RT-7").

**Kept without behaviour change:**

- The `PreRoute` and `PostRoute` phases, interceptor priorities, `failPolicy`, timeouts, and fail-open escalation.
- Trace identifiers passed from a parent to its child, which runtimes use to stitch traces in their eval platform. Under [`0166-delegation-primitives`, "DP-6"](../0166-delegation-primitives/change.md#dp-6-disposition-of-each-setting) the child's tags carry them in place of `tracingContext`.
- The drain gate and the isolation checks that apply to a requested runtime.

**Out of scope:**

- Statistical analysis, scoring, and experiment lifecycle management. These stay outside Lenny.
- Interceptors written or registered by a `tenant-admin`. Registration stays with `platform-admin` (D4).
- Routes that apply across tenants, and staged rollout of a new version of one runtime. [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) covers the second, which also gives operators a rollout that reaches every tenant (D12).

## Terms

| Term | Meaning |
| :-- | :-- |
| Route | A runtime route: an admin resource that maps a name a request can ask for, either a runtime or an alias, to the target runtimes that sessions asking for it run on, each with a weight. "RT-4" defines it. |
| Alias | A route whose requested name is no runtime in the tenant view, such as an Anthropic model ID. A request that asks for the alias runs on the route's targets ("RT-4", **Aliases.**). |
| Target | One entry of a route: a runtime, an optional pool, and an integer weight. A target may be the requested runtime itself. |
| Routing key | The value a route hashes to place a session on a target. The route's `key` field selects it: the user ID under `key: user`, the root session ID under `key: session`, or a fresh random value under `key: random`. |
| Root session | The top-level session of a delegation tree. A session that is not a delegated child is its own root, and a derived session is a new root. |
| Routing labels | The `routing` map on a session: string keys and values that `PreRoute` interceptors write, that the gateway stores on the session record, and that the runtime receives in `session_start`. "RT-3" defines it. |
| Route interceptor | The built-in `PreRoute` interceptor that applies routes. |
| `PreRoute` payload | The JSON document the gateway passes to every `PreRoute` interceptor, and the fields an interceptor may change in a `MODIFY` response. "RT-2" defines it. |
| Creation kind | The `kind` field of the `PreRoute` payload: `create` for a REST create or create-and-start, `delegate` for a delegated child, which a session creates with the platform operation `session.create`, and `derive` for a session derived from an earlier one. |
| Post-chain point and post-chain checks | The post-chain point is the single step of session creation that follows the `PreRoute` chain and revision selection and precedes the pod claim. The post-chain checks are every check that depends on the runtime, the revision, or the pool, which run there on what the chain chose, whichever interceptor chose it. "RT-2" lists them by owning design. |
| Final row | The session row as the post-chain point sees it: the runtime and pool the chain left, the revision chosen for that runtime, and the session's environment. Other designs use the term for the row their re-checks read. |
| Last modifier | The interceptor whose `MODIFY` last changed a field of the payload in one chain run. Errors from post-chain checks name it. |
| Session context | The fields every interceptor call after creation carries in `InterceptRequest.metadata`: tenant, user, session, root session, runtime, pool, and routing labels. "RT-2" defines it. |
| Experimentation subsystem | Everything this design removes from §10.7: experiments, variants, external targeting, the sticky cache, variant pool sizing, and eval storage. |
| OFREP | The OpenFeature Remote Evaluation Protocol, a vendor-neutral REST protocol for asking a flag service for a flag's value. The gateway's OFREP client is one of the removed paths. |
| SDK path | External targeting through vendor SDKs for LaunchDarkly, Statsig, and Unleash compiled into the gateway, in the `experimentprovider` package. |
| Labels | Parts of the design carry the label RT-n, and requirements carry the label RTR-n. |

### Terms from other designs and the platform

This design builds on mechanisms the specification, the tree, and other designs define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Gateway | Lenny's API gateway, the only externally facing component, through which every client request passes and which creates sessions ([§4.1](../../spec/04_system-components.md#41-edge-gateway-replicas)). It runs as several replicas, and each replica serves every tenant. |
| Runtime and pool | A runtime is a registered agent image and its configuration. A pool is a set of pre-warmed pods for one runtime at one isolation profile and resource class ([§5](../../spec/05_runtime-registry-and-pool-model.md)). A session names a runtime and may pin a pool. |
| Runtime revision | An immutable numbered version of a runtime's pod-defining fields, which [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) adds. The gateway picks a revision of the final runtime after the `PreRoute` chain and before the post-chain checks, so the checks read the chosen revision. |
| Interceptor | A hook the gateway calls at a named phase of request handling ([§4.8](../../spec/04_system-components.md#48-gateway-policy-engine)). It answers `ALLOW`, `REJECT`, or `MODIFY`, and a `MODIFY` returns a changed payload. Built-in interceptors run in the gateway process. External interceptors are gRPC services that a `platform-admin` registers through `/v1/admin/interceptors`. |
| `PreRoute` and `PostRoute` | The interceptor phases before and after the gateway selects a session's runtime. A `PreRoute` interceptor may change the runtime. A `PostRoute` interceptor sees the resolved runtime and may not change it. |
| Priority | The order of interceptors within a phase, lowest first. Priorities up to 100 are reserved for built-ins. |
| `failPolicy` and fail-open escalation | How the chain treats an interceptor's timeout or error: `fail-closed` rejects the request and `fail-open` skips the interceptor. A `fail-open` interceptor that fails too often within a window is escalated to `fail-closed`. |
| Immutable field enforcement | The gateway's comparison of protected payload fields before and after a `MODIFY`. A change to a protected field rejects the request with `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`. |
| `GuardrailsInterceptor` | The built-in content classifier hook at priority 400. §4.8 states that a `MODIFY` from an interceptor that runs after it is not checked again. |
| Session creation paths | The REST create (`POST /v1/sessions`), create-and-start, the delegated child a runtime starts with the platform operation `session.create` ([`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant)), which replaces `delegate_task` of [§8.2](../../spec/08_recursive-delegation.md#82-delegation-mechanism), and derive (`POST /v1/sessions/{id}/derive`). In the tree, the create and create-and-start paths run through `CreateSessionService` and `CreateAndStartService` in `pkg/gateway/core/session/sessionserver/service.go`, which the HTTP handlers and the platform tools seam both call, and create-and-start runs its own drain gate (`pool_drain_gate.go`). |
| Derive | A new session that starts from an earlier session, either from its snapshot (`from: latest`, the default) or from its stored plan with no snapshot (`from: start`), with an optional `runtimeRef` and `targetPool`. [`0144-session-derive`](../0144-session-derive/change.md) makes it the single route for this and removes replay. A derive requires the target's isolation to be no weaker than the source's, and it refuses a source whose plan holds stopped content. |
| `DelegationPolicy` and policy cap | A delegation policy holds tag rules that state which runtimes and pools a session may target ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). The policy cap is the intersection of every policy the root grant inputs name ([`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant)). |
| Content caveat and `contentPolicy.interceptorRef` | The content caveat is the restriction every session carries on the content and files it sends and receives, resolved from the platform, the tenant, the environment, and the runtime, the strictest value winning. Its `contentPolicy.interceptorRef` names an interceptor that inspects a child's input and the files that move between sessions ([`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant), decision 45). Before 0166, a delegation policy held `contentPolicy` ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| Session grant and parent link | The session grant is the gateway-side record of what a session may do as a parent, which replaces the delegation lease of [§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease). The parent link is the child's `parentSessionId`, with the lineage, principal, environment, and routing labels that follow from it ([`0166-delegation-primitives`](../0166-delegation-primitives/change.md), "DP-1" and "DP-2"). |
| `session_start` | The first frame the adapter sends a runtime for a session, on the intra-pod channel `CH-MSGSOCK` ([§28.5.3](../../spec/28_communication-channels.md#2853-intra-pod)). |
| Session labels | The client-set `labels` map on a session creation request, used for querying, usage reports, and billing filters. [§14](../../spec/14_workspace-plan-schema.md) states that labels are not used for internal routing. A delegated child carries its root's `labels` unchanged, and no parent writes them ([`0166-delegation-primitives`, "DP-2"](../0166-delegation-primitives/change.md#dp-2-parent-link), decision 44). |
| Isolation profile and `minIsolationProfile` | A pool's sandboxing level, in increasing strength `standard`, `sandboxed`, and `microvm`, and the weakest profile a session accepts. |
| Drain gate | The create-time refusal with `503 POOL_DRAINING` when the selected pool is draining ([§15.1](../../spec/15_external-api-surface.md#151-rest-api)). |
| PoolScalingController | The controller that sets each pool's warm pod count (`minWarm`) from its demand history ([§4.6.2](../../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration)). |
| Tenant view | The runtime lookup that returns only the runtimes a tenant may use: platform runtimes it has an access row for and its own derived runtimes. [`0147-tenant-derived-runtimes`, "DR-4: Tenant runtime records"](../0147-tenant-derived-runtimes/change.md#dr-4-tenant-runtime-records) defines it and, under "Reading", requires a miss on the final row to refuse. |
| Pool access and owned pool | A platform pool serves a tenant through a `pool_tenant_access` row, and a tenant-derived runtime's owned pool serves only its owner. `requirePoolSelectable` refuses another tenant's pool as unknown ([`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md), DR-1 and "Pods" under DR-5). |
| Environment, E, and sharing path | A session's environment, written E, is fixed at create and decides which runtimes, pools, and egress rules apply to it. A sharing path is a two-sided declaration that lets a delegated child cross into a target environment T and run under T's policy ([`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md), TS-1 and TS-3). |
| `gateway.interceptorNamespaces` | A Helm value listing the namespaces that hold in-cluster interceptors. The chart renders a gateway egress rule and an interceptor certificate for each one. |
| NET-063 | The rule that the gateway-to-interceptor link uses mTLS with SPIFFE and DNS name checks ([§10.3](../../spec/10_gateway-internals.md#103-mtls-pki)). SPIFFE is a standard for workload identities carried in certificates. |
| `lenny-egress` | The central egress proxy that [`0127-egress-and-git`](../0127-egress-and-git/change.md) adds, through which pod traffic leaves the cluster. |
| `platform-admin` and `tenant-admin` | Lenny's administrative roles. A `platform-admin` administers the whole platform and every tenant. A `tenant-admin` administers one tenant. |
| The proposal | Text written before this design moved into `changes/` names "the proposal" for the work that lands it. The proposal pipeline and its `fidelity-review` skill are retired, so "the proposal" means the steps in `steps.md`, built with the `build` skill. |
| F-n.n.n and T-n.n.n | Findings filed in `BUILD-GAPS.md` (implementation gaps against the specification) and `TEST-GAPS.md` (test coverage gaps against the specification). |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines, by name: `static`, `unit`, `component`, `contract`, `integration` on the compose stack, `e2e_kind` on Kind (a local Kubernetes cluster), `load_local`, `chaos`, `security`, and `docs`. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| DR-1, DR-4, DR-5, DR-8, DR-9, and DP11 | [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md#dr-1-to-dr-3-prerequisites) | DR-1 removes `tenant-admin` writes on platform-global runtimes and pools, makes session creation, discovery, and delegation read the access rows, and confines experiments to single-tenant pools. DR-4 defines the tenant view and the check that the final runtime row is `registered`. DR-5 defines owned pools. DR-8 states the interactions. DR-9 defines default git clones and their `write` values. DP11 confines an experiment's pool effects to pools whose single tenant is the experiment's tenant. |
| TS-1 and TS-3 | [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) | TS-1 scopes runtimes, pools, and egress rules to environments and re-checks them on the final row ("A session's environment"). TS-3 fixes a session's environment at create and defines sharing paths ("Cross-environment sharing"). |
| CA-4 and CA-7 | [`0123-connector-authorization-and-external-memory`, "CA-4: Link check"](../0123-connector-authorization-and-external-memory/change.md#ca-4-link-check) and ["CA-7: Delegated children"](../0123-connector-authorization-and-external-memory/change.md#ca-7-delegated-children) | The connector link check at create ("Placement" under CA-4) and for a delegated child, each of which read the final runtime's capability. [`0124-connector-removal`](../0124-connector-removal/change.md) withdrew both with connectors, and this document names them only in "Current state", the instructions to other designs, and the revision notes. |
| CR-1 | [`0124-connector-removal`](../0124-connector-removal/change.md) | The removal of connectors, with the connector link check, `details.connectors`, and the connector interceptor phases `PreConnectorRequest` and `PostConnectorResponse`. |
| SD-1 | [`0144-session-derive`](../0144-session-derive/change.md#sd-1-one-route) | The derive route and its shared create path, with its target checks under "Checks" and "Stops". Revision 12 of that design refused a derive when an interceptor rewrote its runtime and added `evalRef` to derive, and "RT-2" and "RT-1" reverse both. Its SD-P4, the refusal of a target with no runtime supervisor, is withdrawn (AR O8). |
| R8 | [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) | [R8](../0138-rest-only-client-api/change.md#ro-2-rest-additions) the public runtime index, the single-runtime read, and the aliases on discovery, from which the provider adapters build their models lists. |
| AR O2 and AR O8 | [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions) | Decisions of the agent-runtimes-only design, which removes the `type: mcp` runtime and service mode. O2 removes `executionMode`, with the derive's mode check and `INCOMPATIBLE_RUNTIME`. O8 replaces SD-P4 and its reason `target_unsupervised` with the post-chain check of DR-4 that the final runtime row is `registered`, for every create, derive, and delegated child. The owner settled both on 2026-11-13. |
| RP-1 and RP-2 | [`0139-runtime-protocol`](../0139-runtime-protocol/change.md#rp-1-handshake) | [RP-1](../0139-runtime-protocol/change.md#rp-1-handshake) the handshake the adapter runs with each runtime process it launches at bind, refused with `RUNTIME_HANDSHAKE_REFUSED`; [RP-2](../0139-runtime-protocol/change.md#rp-2-capabilities-and-their-dependencies) runtime capabilities and the claim-time check of a session's effective capabilities against the pod set's last handshake report, refused with `RUNTIME_CAPABILITY_MISSING` (its decision O2 (d)). |
| EG decision 60 | [`0127-egress-and-git`, "Slot attribution and fetch grants"](../0127-egress-and-git/change.md#slot-attribution-and-fetch-grants) | Per-slot scopes on a pod with several slots, settled by the owner on 2026-11-13 as option (a): `netagent` applies each slot's own session's environment, host lists, and credentials, and a pod keeps the union scope only where slot attribution is unavailable. |
| RV-2 and RV-7 | [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) | Revision selection, which runs on the final runtime after the `PreRoute` chain and before the post-chain checks, and session options, whose validation against the chosen revision's schema runs at the post-chain point. |
| SO-4a and SO-5a | [`0142-security-oversight`](../0142-security-oversight/change.md) | SO-4a is the mirror of a platform admin's actions on a tenant into that tenant's own audit chain. [SO-5a](../0142-security-oversight/change.md#so-5a-tool-use-approvals) holds a call until an approver answers, and its rules on `session.create` take a `when` that names the target runtime or a credit amount. |
| DP-1, DP-2, DP-6, and decisions 4, 5, and 44 | [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) | DP-1 is the session grant with its caveats, the content caveat, the platform operations such as `session.create`, and the checks of a child's creation, which run at this design's post-chain point ("Where the checks run."). DP-2 is the parent link, under which a child carries its root's `labels`. DP-6 states what happens to each delegation setting. Decision 4 folds `PreDelegation` into the `PreRoute` chain for `kind: delegate`, decision 5 removes cycle detection and the self-recursion gate, and decision 44 separates `labels` from a parent's tags. |
| PM-3, PM-4, and PM-7 | [`0136-placement`](../0136-placement/change.md) | [PM-3](../0136-placement/change.md#pm-3-placement-offerings-and-per-pool-nodeplacement) placement offerings, the named platform-admin options that fix where a pool's pods run, and the tenant grants that make a non-default offering usable; [PM-4](../0136-placement/change.md#pm-4-placement-requirements) placement requirements, filters on the pools a session may run on that a tenant, an environment, or the operator sets; and [PM-7](../0136-placement/change.md#pm-7-region-placement) region placement, with its residency check of a pool's region against the session's `dataResidencyRegion`. |

## High-level design

Lenny decides which runtime each session runs. Today an operator can steer that decision in two unrelated ways: through the experimentation subsystem, which has its own objects, storage, and integrations with flag services, or through an interceptor, a service the gateway calls before it picks the runtime. This design removes the experimentation subsystem, together with the storage of evaluation scores, and keeps the interceptor as the one way to steer runtime choice.

Lenny still ships a basic split for tenants who have no flag service. A route says that sessions asking for runtime `claude-worker` run on `claude-worker` 90% of the time and on `claude-worker-v2` 10% of the time, chosen by a stable hash of the user. The route is applied by a built-in interceptor that sees the same input and may make the same changes as any external one, so an operator can turn it off and replace it. Every routing decision, whoever made it, is recorded on the session as routing labels that the runtime reads to tag its traces and that billing and usage reports carry. Scoring and statistics stay in the eval and experimentation platforms deployers already use.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Removal (RT-1).** The experimentation subsystem, eval storage, and `evalRef` are deleted from the specification, the gateway, the controllers, the adapter, the SDKs, the schema, and the documentation.

**Interceptor contract (RT-2).** Every session creation path builds the same `PreRoute` payload, which gains the creation kind, the session, parent, and root IDs, the client labels, the pinned pool, and the routing labels. A `MODIFY` may change the runtime, the pool, the input, and the routing labels on every path, including delegation, which today ignores a runtime change, and derive, which runs no chain today and which [SD-1](../0144-session-derive/change.md) would refuse on a runtime change. After the whole chain the gateway picks a revision of the final runtime, then runs every check that depends on the runtime, the revision, or the pool once, at one post-chain point, including the checks other designs own, and names the interceptor that last changed the runtime when one fails. Every later interceptor phase receives the same session context, and every phase's payload is checked against §4.8.

**Routing labels (RT-3).** The session record gains a `routing` map that only `PreRoute` interceptors write. It replaces `experimentContext` on `session_start`, on the delegation lease, and in billing events. A delegated child, which receives the map through the parent link of 0166 "DP-2", and a derived session start with the map of the session they come from.

**Runtime routes (RT-4).** A tenant-scoped admin resource maps a requested runtime to weighted targets. The route interceptor hashes the routing key to pick a target and writes the route and target names into the routing labels. Routes hold no pool settings, and each target pool scales from its own demand. A route's requested name may name no runtime, which makes it an alias: a client of the Anthropic or OpenAI adapter that asks for a model ID such as `claude-sonnet-5-5` reaches the runtimes the alias's targets name, and Lenny's discovery routes, from which those adapters build their models lists, show the alias.

**In-cluster interceptors (RT-5).** Registration accepts only a Service name in a namespace listed in `gateway.interceptorNamespaces`. The chart supplies the gateway's mTLS material, and the gateway refuses plaintext outside dev mode and service-mesh mode.

**Tenant scope (RT-6).** An interceptor registration may list tenants, and the chain calls it only for requests of those tenants. No setting from which a session's content caveat resolves can reference an interceptor scoped to another tenant.

**Documentation (RT-7).** The operator guide states how to run a split with a route, how to delegate assignment to a flag service through an in-cluster interceptor, and where scoring belongs.

```
 Session creation (kind: create, delegate, or derive)
        │
        ▼
 PreRoute chain, ascending priority, filtered by tenant scope
        │   payload: kind, tenant, user, session/parent/root IDs, labels,
        │            requested runtime and pool, routing labels, input,
        │            runtime options (immutable)
        ├── external interceptors, priority 101–299 (in-cluster, mTLS)
        ├── route interceptor, priority 300 (built-in, same contract)
        └── external interceptors, priority 301 and above
        │
        ▼
 Revision selection on the final runtime (0140-runtime-revisions)
        │
        ▼
 Post-chain point, on the final runtime, revision, and pool:
   tenant view and pool access, isolation, drain, environment rules,
   egress pool refusals, link checks, export refusal, the client-tools
   capability check, runtimeOptions against the revision's schema,
   the checks of a child's creation (0166), and derive checks
        │
        ▼
 Pod claim
        │
        ▼
 Session record: runtime, revision, pool, routing labels
        │
        ├──> session_start.routing ──> runtime tags its traces
        ├──> later interceptor phases receive the session context
        └──> billing events and usage reports carry routing labels
```

"Limits" states what the change costs: Lenny keeps no scores and no experiment state, two concurrent tests on one runtime share one route or need an interceptor, and a tenant's remote service needs an in-cluster relay.

## Requirements

| ID | Requirement |
| :-- | :-- |
| RTR-1 | No experiment object, eval score, `evalRef`, sticky assignment, or built-in flag service client remains in the specification, the code, the schema, or the documentation. The documentation of "RT-7" describes a deployer's own interceptor that calls a flag service. |
| RTR-2 | Every session creation path passes the same `PreRoute` payload and honours the same `MODIFY` rights. |
| RTR-3 | The runtime, revision, and pool a session runs on pass every post-chain check regardless of which interceptor chose them, and every write-time check that names a runtime holds for each of its active revisions. |
| RTR-4 | A route spreads sessions across its targets in proportion to their weights, and the same routing key reaches the same target while the route is unchanged. |
| RTR-5 | The route interceptor reads only the `PreRoute` payload and its own routes, and changes only what an external interceptor may change. |
| RTR-6 | Turning the route interceptor off leaves every other creation behaviour unchanged. |
| RTR-7 | The runtime, the session record, billing events, and usage reports carry the routing labels the chain set, and clients cannot set them. |
| RTR-8 | The gateway calls interceptors only at in-cluster endpoints, over mTLS outside dev mode and service-mesh mode. |
| RTR-9 | A tenant-scoped interceptor is never called for another tenant's request, and no policy of another tenant can reference it. |
| RTR-10 | Every interceptor call after creation carries the session context, and every phase's payload carries the fields §4.8 states for it. |
| RTR-11 | A retried or resumed session keeps the runtime, pool, and routing labels it was created with. |
| RTR-12 | A request that names an alias runs on the alias's targets, judged by every per-runtime check on the final runtime, and discovery, from which the provider adapters build their models lists, shows the alias only to a caller who can use each of its targets. |

## Current state

**`ExperimentRouter` is outside the chain [source].** The specification lists the `ExperimentRouter` as a built-in `PreRoute` interceptor at priority 300. The code calls it directly (`routeExperiment` in `pkg/gateway/sessionserver/create.go`) and splits the external `PreRoute` chain into a segment below 300 and a segment from 300 up to place it. `create.go` and `create_and_start.go` each carry the split, and the delegation path calls `ApplyExperimentRouting` through an interface in `pkg/gateway/mcpfabric/delegation/service.go`.

**The route payloads are narrower than the specification [source]:**

| Path and phase | Payload type | Fields sent | `MODIFY` honoured |
| :-- | :-- | :-- | :-- |
| REST create and create-and-start, `PreRoute` | `routeTaskSpec` in `sessionserver/route.go` | `tenant_id`, `user_id`, `requested_runtime`, and `input` in the first segment. The second segment omits `input`. | Runtime and input |
| Delegation, `PreRoute` | `childRouteSpec`, declared in `mcptools/mcptools.go` and filled in `mcptools_register.go` | `tenant_id`, `requested_runtime`, and `input`. The type declares `user_id`, which the delegation path never sets. `metadata` carries only the tenant. | Input only. A runtime change is discarded. |
| REST create, `PostRoute` | `routeTaskSpec` | `tenant_id`, `user_id`, and `resolved_runtime_name`. The type has `credential_pool_id`, and `postRouteAfterSelection` leaves it empty. | Checked for immutable fields only |

[§4.8](../../spec/04_system-components.md#48-gateway-policy-engine) says the `PreRoute` payload carries the requested runtime, workspace files, and delegation parameters, that an interceptor may change runtime hints on every path, and that the `PostRoute` payload carries runtime tags and the credential pool assignment. No path sends session labels, the pinned pool, or the session's ID: on the create path the request reaches the chain before the gateway mints the ID. `interceptor.Result` records the interceptor that rejected a request (`RejectedBy`) and no field records which one modified it.

**Derive [source].** `handleDerive` runs neither the `PreRoute` chain nor the `PostRoute` chain today (F-15.1.78). [SD-1](../0144-session-derive/change.md) adds both and refuses a derive whose runtime an interceptor rewrites.

**Checks after a rewrite [source].** After the late `PreRoute` segment changes the runtime, `preRouteAfterExperiment` re-resolves the isolation level, and `create.go` re-runs the drain gate. The fail-closed isolation check against `minIsolationProfile` lives in the `ExperimentRouter` (`VARIANT_ISOLATION_UNAVAILABLE`), and no equivalent was found for a runtime an external interceptor selects. "Validation owed" in `steps.md` records the check.

**Re-checks in other designs [source].** Several designs each place their own checks after the experiment rewrite, at different call sites: TS-1 re-runs its environment checks on the final row ("A session's environment"), DR-4 refuses a tenant-view miss on the final row ("Reading"), CA-4 places its link check after `preRouteAfterExperiment` ("Placement"), CA-7 and DR-8 place theirs after `ApplyExperimentRouting`, and TS-3 re-checks a crossing on the final row. The "RT-2" post-chain point replaces those call sites with one.

**Interceptor transport [source]:**

- Registration records `endpoint`, `phases`, `priority`, `failPolicy`, and `timeout`. It has no TLS or credential field, and no tenant scope. The interceptor store treats interceptors as platform resources, and every `/v1/admin/interceptors` route sits behind `requireAdmin`, which requires `platform-admin` (`pkg/gateway/externalapi/admin/routes.go`, `tenants.go`).
- Transport security is gateway-wide. `dialInterceptor` in `cmd/lenny-gateway/policychain_seams.go` reads `--external-interceptor-tls-cert`, `--external-interceptor-tls-key`, and `--external-interceptor-ca`. The SPIFFE check of NET-063 applies only to a `.svc` host. Any other host gets CA and DNS name checks against the single CA bundle, so a public endpoint with a public certificate fails unless that bundle holds public roots.
- When all three flags are empty, `dialInterceptor` dials in plaintext, and no dev-mode check guards that branch. The chart renders `--interceptor-namespaces` and an interceptor certificate (`lenny-interceptor-tls`) and renders none of the three flags (`charts/lenny/templates/gateway-deployment.yaml`). A chart install therefore appears to dial interceptors in plaintext with no SPIFFE check, which NET-063 forbids. "Validation owed" in `steps.md` records the confirmation, and O6 asks whether to file it now.
- §4.8 admits "an external host:port" endpoint, and §13.2 says such an interceptor uses the gateway's public HTTPS rule, which admits TCP 443 only.

**`evalRef` [source].** §15.1 defines `evalRef` on replay as a link "to an experiment or eval set", recorded in the new session's metadata and filterable on `GET /v1/sessions`. `replay.go` decodes it into its request struct, and no code stores it or filters on it. [`0144-session-derive`](../0144-session-derive/change.md) moves it to derive.

**Defects that the removal closes.** Revision 4 recorded these, and removing the subsystem closes each one rather than fixing it:

| Area | Defect |
| :-- | :-- |
| SDK path | The provider cache holds a mutex across vendor SDK initialization, so a new configuration blocks session creation for every tenant using an SDK provider. |
| SDK path | No per-evaluation deadline reaches the SDK providers, so the §10.7 200 ms budget is not enforced for them. |
| SDK path | The Statsig and Unleash SDKs hold process-global state, so only one tenant per gateway process can use each. |
| OFREP path | The documented `ofrep.endpoint` example produces the request path `/ofrep/ofrep/v1/...` and a 404 **[measured]**. |
| OFREP path | The documented `Authorization: "Bearer ${OFREP_TOKEN}"` header is sent literally **[measured]**. |
| OFREP path | OFREP header values, including tokens, are stored in the `tenants.experiment_targeting` column and returned by tenant responses. |
| OFREP path | A missing flag counts as a flag service failure, so one experiment created before its flag disables external experiments for the session and opens the breaker. |
| OFREP path | The client follows redirects, sends no session labels, accepts `http` endpoints, and cannot reach a private address. |

**Size of the SDK path [measured].** Both builds use `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 -trimpath -ldflags='-s -w'`. The second replaces `experimentprovider.go` with a stub through an overlay. The removal in this design is larger, and this result is a lower bound.

| | Current | Without vendor SDKs | Change |
| :-- | --: | --: | --: |
| `lenny-gateway` binary | 110,805,154 bytes | 108,286,114 bytes | 2,519,040 bytes, 2.3% |
| Packages in the gateway build | 1942 | 1884 | 58 |

**Footprint [source].** The subsystem appears in these places:

| Carrier | Locations |
| :-- | :-- |
| Specification | §10.7 throughout; §4.6.1 and §4.6.2 (variant pool sizing); §4.7.6 and §28.5.3 (`experimentContext`); §4.8 (built-in table and field dependencies); §5.2 (variant formula with mode factors); §7.1 (step 3a); §8.2 and §8.3 (delegation); §11.2.1 (billing fields); §12.2, §12.4, §12.6, and §12.8 (stores, Redis keys, and erasure); §15.1 (endpoints, errors, dry-run, deletion rules, and replay's `evalRef`); §15.7 (SDK type); §16.1, §16.5, §16.6, and §16.7 (metrics, alerts, and events); §18 (Phase 16 and 16.5); §23 (comparison rows and capability entries). |
| Gateway packages | Paths as of 2026-11-19, after the package moves of `0117-gateway-package-layout`: `pkg/experiment`; `pkg/gateway/experiment/` (`evalstore`, `experimentprovider`, `experimentsticky`, `experimentstore`, and `ofrep`), whose seven packages `tests/registers/gateway-layout.yaml` lists with `removed-by: '0141'`; `pkg/gateway/core/session/sessionserver/experimentrouter.go` and `eval.go`; `pkg/gateway/api/admin/experiments.go` and `experiment_results.go`; `pkg/storage/erasure`; `pkg/gateway/replica/gatewaymetrics/gatewaymetrics_experiment.go`; the `ExperimentContext` field in `pkg/session/sessionstore` and `pkg/session/sessionrow` and its readers in `sessionserver` (`usage.go`, `lifecycle.go`, `bind_request.go`, and `resume_rebind.go`), `pkg/gateway/core/session/watchdog`, `pkg/gateway/core/session/podsession`, `pkg/gateway/core/session/adapterclient`, and `pkg/gateway/platformtools/delegation`. |
| Controllers | `pkg/controller/poolscaling/experimentsource.go`, `variants.go`, and the variant paths in `controller.go`. |
| Adapter and SDKs | `pkg/adapter` (`manifest.go`, `sessionframes.go`, `resume.go`, `session.go`, and `sdkwarm.go`); `schemas/lenny-adapter.proto` (`ExperimentContext`); the Go, Python, and TypeScript runtime SDKs and their examples; `cmd/lenny-compliance` fixtures. |
| Alerts | `pkg/alerting/rules`, `charts/lenny/files/alerting-rules.yaml`, and `docs/alerting/rules.yaml`. |
| Migrations | `0011_session_experiment_context`, `0025_tenant_experiment_targeting`, `0029_eval_results`, `0030_experiment_definitions`, `0098_eval_results_idempotency_key`, and `0156_eval_aggregates_matview`. |
| Documentation | `README.md`, `docs/index.md`, `docs/tutorials/evaluation-scoring.md`, `docs/operator-guide/openfeature-integration.md`, and the other pages under `docs/` that name experiments or eval, including `docs/reference/metrics.md` and `docs/reference/error-catalog.md`. |

## Detailed design

### RT-1 Removal

Delete every item under "Removed" in "Scope" from the carriers in "Footprint".

- **Schema.** The tree keeps its migrations as they are, because the shared Kind database and its migration guard compare applied migrations by name and content. A new forward migration drops `eval_results`, `lenny_eval_aggregates`, `experiment_definitions`, `tenants.experiment_targeting`, and `sessions.experiment_context`, and adds the tables of "RT-3" and "RT-4". The platform has no deployments, so the migration carries no data.
- **Erasure.** The erasure order loses the sticky cache step and the `EvalResultStore` step. Routing labels live on the session row and are deleted with it. The route store joins the tenant erasure order of §12.8, so a tenant's deletion deletes its routes. Tenant deletion also removes the tenant from the `tenants` list of every interceptor registration ("RT-6"), and a registration whose list that removal empties is deleted, because an empty list calls the interceptor for every tenant. The edit names each erasure step by its store and leaves the renumbering of the sequence to the step that edits it, because other member changes edit the same sequence.
- **Metrics.** `lenny_session_total`, `lenny_session_error_total`, and `lenny_session_duration_seconds` replace their `variant_id` label with `runtime`, whose values the registered runtimes bound. The experiment and eval metrics are deleted.
- **Error codes.** `RESERVED_IDENTIFIER` loses its variant example and stays, because the admin API also refuses the reserved tenant ID `platform` with it (`docs/api/admin.md`). `SESSION_NOT_EVAL_ELIGIBLE` and `EVAL_QUOTA_EXCEEDED` are deleted. `VARIANT_ISOLATION_UNAVAILABLE` becomes `ROUTE_ISOLATION_UNAVAILABLE` ("RT-2").
- **`evalRef`.** It leaves the replay request in §15.1 and leaves the derive request, the session row, and the session list filter that [`0144-session-derive`](../0144-session-derive/change.md) stages. A client that groups sessions sets a session label, which every query API already filters on.
- **Removed request fields.** A tenant write that names `experimentTargeting`, and a derive or session list request that names `evalRef`, are refused with `400 VALIDATION_ERROR` whose `details.field` names the member, whatever value it carries, under the one rule for removed fields (D18). The names stay in the validators' lists of removed fields and nowhere else.
- **Build sequence.** §18 is a successor stub since 0115.S9, so its edit applies to the successor: the "Experimentation primitives" and "Score storage and retrieval" rows of `docs/about/status.md` become a runtime routes row. `TESTING.md` §13.33 (Phase 16) and §13.34 (Phase 16.5) are replaced by the runtime routes tests.

### RT-2 The interceptor contract

#### `PreRoute` payload

One Go type in `pkg/policy/interceptor` serializes the payload for every creation path. Its fields:

| Field | Mutable | Meaning |
| :-- | :-- | :-- |
| `kind` | No | `create`, `delegate`, or `derive`. |
| `tenant_id` | No | The session's tenant. |
| `user_id` | No | The session owner, empty for an anonymous session. Delegation now sends it. |
| `session_id` | No | The ID the session will have. The gateway mints it before the chain runs. |
| `parent_session_id` | No | The parent for `kind: delegate`, otherwise empty. |
| `source_session_id` | No | The source for `kind: derive`, otherwise empty. |
| `root_session_id` | No | The root of the delegation tree, equal to `session_id` for `kind: create` and `kind: derive`. |
| `delegation_depth` | No | 0 except for `kind: delegate`. |
| `environment` | No | The session's environment, when it names one. For `kind: delegate` it is the parent's environment. The child's own environment is settled at the post-chain point, which takes the parent's or, under a sharing path, the target's (TS-3). |
| `labels` | No | The client's session labels. For `kind: delegate`, the root's `labels`, which a delegated child inherits unchanged and no parent writes ([`0166-delegation-primitives`, "DP-2"](../0166-delegation-primitives/change.md#dp-2-parent-link), decision 44). Empty for `kind: derive`, as [SD-1](../0144-session-derive/change.md) states. |
| `requested_runtime` | Yes | The runtime the session will run. For `kind: derive` it starts as the derive's `runtimeRef`, or the source session's resolved runtime when the request omits it. |
| `requested_pool` | Yes | The pinned pool, or empty. For `kind: derive` it starts as the derive's `targetPool`, or the source's client-named pool when the derive stays on the source's runtime and profile, and otherwise empty, as [SD-1](../0144-session-derive/change.md) states under "Checks", **Pool and profile.** |
| `routing` | Yes | The routing labels so far ("RT-3"). |
| `input` | Yes | The task input, as today. For `kind: delegate`, the `task` of the parent's `session.create` ([`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant)). Empty for `kind: derive`. |
| `runtime_options` | No | The session's `runtimeOptions`: the create request's, the `runtimeOptions` of the parent's `session.create` for `kind: delegate`, and the derive request's or the source's for `kind: derive`. The post-chain point validates them, so no interceptor may change them ([`0140-runtime-revisions`, "RV-7 Session options"](../0140-runtime-revisions/change.md#rv-7-session-options)). |

The immutable fields join the existing immutable field enforcement. `InterceptRequest.session_id` carries the same `session_id`, and `metadata` carries the session context below. The `PostRoute` payload carries `runtime_options` as an immutable field as well, so the options the post-chain point validates are the options the runtime receives.

#### One chain

The gateway runs the `PreRoute` chain once, in ascending priority, with the route interceptor as an ordinary member at priority 300. The split segments, `preRouteBeforeExperiment`, `preRouteAfterExperiment`, and the experiment pivot are deleted from `create.go` and `create_and_start.go`. The delegation and derive paths honour `requested_runtime`, `requested_pool`, and `routing` from a `MODIFY`, as the create paths do.

Under decision 4 of [`0166-delegation-primitives`](../0166-delegation-primitives/decisions.md), the `PreDelegation` phase folds into this chain for `kind: delegate`, so one chain sees every creation. The inspection of a child's input that `PreDelegation` ran, including the content caveat's `contentPolicy.interceptorRef` and `maxInputSize`, runs on the `PreRoute` payload of `kind: delegate`, whose `input` is the `task` of `session.create`. 0166 states which interceptors run and the content caveat they apply ("DP-1", **Content caveat.**).

A derive that omits `runtimeRef` asks for the source's resolved runtime. When Alice's source session asked for `claude-worker` and a route placed it on `claude-worker-v2`, her derive asks for `claude-worker-v2`, and a route keyed on `claude-worker` does not move it. A derive that names `claude-worker` is routed as a create is.

#### Post-chain point

Session creation runs in this order on every path:

1. The `PreRoute` chain, including the route interceptor, produces the final runtime and pool.
2. Revision selection ([RV-2](../0140-runtime-revisions/change.md)) picks a revision of the final runtime.
3. The post-chain point runs every check that depends on the runtime, the revision, or the pool, on the chosen revision. A check that reads a field [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) versions, such as `workspaceDefaults` or `runtimeOptionsSchema`, reads it from the chosen revision. Declared capabilities are read there from the chosen revision.
4. The claim-time capability check of [RP-2](../0139-runtime-protocol/change.md#rp-2-capabilities-and-their-dependencies): when the chosen revision's pod set has a recorded handshake report, the gateway compares the session's effective capabilities with the report's offer and refuses a session the offer does not cover with `RUNTIME_CAPABILITY_MISSING`, naming each missing capability, with no pod claimed. A pod set with no report yet passes, and the adapter's check at launch decides.
5. The pod claim.
6. At bind, after materialization and setup, the adapter launches the runtime process and runs the handshake of [RP-1](../0139-runtime-protocol/change.md#rp-1-handshake). A refusal ends the bind and releases the slot, which stays in service, and the creation fails with `RUNTIME_HANDSHAKE_REFUSED`, or with `RUNTIME_CAPABILITY_MISSING` when the process offers less than the session's effective capabilities. A retryable refusal is retried on another pod. A refusal that is not retryable stops claims from that pod set. At a resume or a restore, a refusal that is not retryable ends the session `failed` ([`0139-runtime-protocol`, "Decisions"](../0139-runtime-protocol/decisions.md#decisions), D10, settled by the owner on 2026-11-13 together with SL-Q2 of `0146-session-lifecycle`).

Steps 4 and 6 belong to the runtime protocol design. This design fixes their place: after the post-chain point, so that every routing and policy refusal comes first and names the last modifier, and around the claim, because the report they read belongs to the pod set of the chosen revision rather than to the runtime.

**Order against the owning steps.** 0141.S1 builds the post-chain point and moves to it the checks the tree holds when it lands. Each row of the table below whose owning step lands after 0141.S1 adds its check at the point in that step, and a row whose owning step lands first has its check moved by 0141.S1. Until [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) lands 0140.S1, revision selection returns the runtime's single current definition, and each check that reads a versioned field reads it from that definition. For the 0166 row, 0141.S1 moves the effective `maxDepth` resolution the tree holds, and 0166.S1, which depends on 0141.S1, adds the other checks of the row. 0141.S1 does not move cycle detection or the self-recursion gate, which 0166 removes under its decision 5.

The checks apply on every path and to every outcome, including a runtime the client requested and no interceptor changed. Each owning design states its checks. This design fixes where they run:

| Owner | Checks at the post-chain point |
| :-- | :-- |
| This design | The final pool's isolation profile satisfies the session's `minIsolationProfile`. The drain gate runs once, on the final pool, on every creation path, in place of the calls of `requirePoolNotDraining` (`pool_drain_gate.go`) that `create.go` and `create_and_start.go` each make twice today, once on the requested runtime and again after a rewrite. |
| [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-1-session-grant), with [`0142-security-oversight`](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | For `kind: delegate`, the checks of a child's creation that "Where the checks run." under 0166 "DP-1" names, on the final runtime and pool: the parent grant's policy cap, the isolation floor, the remaining depth, whose runtime-level default is read from the final runtime, and the subsets of credential bindings and egress hosts that `session.create` passes. On the final runtime, the hold of each SO-5a rule of 0142 whose `when` names the target runtime or a credit amount for `session.create` (decision 36 of 0166). Each refusal names the last modifier when the chain changed the runtime or pool. This row replaces the row for the delegation gates of [§8.2](../../spec/08_recursive-delegation.md#82-delegation-mechanism): cycle detection (step 2a) and the three-layer self-recursion gate leave under decision 5 of 0166 and run nowhere, and the policy cap evaluates the `DelegationPolicy` target rules that the delegation path does not evaluate today, which closes F-8.3.14 (0166 "DP-6"). |
| [`0122-client-tools`](../0122-client-tools/change.md) | The refusal of a request with client tools, or with a custom tool, on a chosen revision whose effective capabilities lack the capability, with `422 CLIENT_TOOLS_UNSUPPORTED` on REST. For `kind: derive` with `from: latest`, the refusal of a derive that copies the source's pending client tool calls onto a chosen revision that lacks `capabilities.clientTools`, or lacks `customTools` while a copied call names a custom tool, with `422 CLIENT_TOOLS_UNSUPPORTED`, including when a route moved the derive (T11 and "CT-7" of 0122; "Checks" under [SD-1](../0144-session-derive/change.md#sd-1-one-route)). |
| [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) | The session's effective upstream list, fixed here, and the check that it serves at least one format in the chosen revision's `llmFormats`, refused with `422 LLM_UPSTREAM_NOT_CONFIGURED` ("LU-2", **Session creation.**). |
| [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) | The validation of the tenant's active security settings, including the classification check of its I33, refused with `TENANT_SECURITY_SETTINGS_INVALID`. |
| [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md) | No refusal. The gateway composes the session's `checkpointExcludes` list from the chosen revision's list and the tenant's appended entries and stores it on the session record (**Delivery.**). |
| [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md#dr-4-tenant-runtime-records) | For every creation kind, `create`, `delegate`, and `derive`: the runtime is in the tenant view, and its runtime row is `registered`, the field the RuntimeReconciler mirrors from the `Runtime` resource. For a platform-derived runtime the base's row must also be `registered`. A miss and a row that is not `registered` both answer as for an absent name: `404 RESOURCE_NOT_FOUND`, or for a delegated child the delegation surface's own not-found answer, `TARGET_NOT_IN_SCOPE` ("Reading" and **Registered rows** under DR-4; decision O8 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions)). The pool is selectable by the tenant through an access row or as its owned pool ("Pods" under DR-5). |
| [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) | The environment rules on the final row that "A session's environment" under TS-1 lists: the selector for E, `ENVIRONMENT_REQUIRED`, the rule for pods with no per-session policy (`ENVIRONMENT_POLICY_NOT_ENFORCEABLE`), which under decision 60 (a) of the egress design applies only to a pod with several slots where slot attribution is unavailable. For `kind: delegate`, the rules start from the parent's E, a final runtime outside the parent's selector needs a live sharing path that sets the child's E to T, and the crossing re-check of "Cross-environment sharing" under TS-3 refuses a final row that leaves the path, changes T, or changes whether the hop crosses. |
| [`0127-egress-and-git`](../0127-egress-and-git/change.md#effective-policy) | The refusal of a plan with a `gitClone` source on a pool with no git path ("Credentials"), and the refusal of `egress.allowedHosts` or `egressLockHosts` on a pod with several slots where slot attribution is unavailable, which the post-chain point reads from the pool's status ("Effective policy" and decision 60 (a), "Slot attribution and fetch grants"). The gateway's resolution of `gitClone` refs runs after these checks on every creation path, and before the pod claim where the plan arrives with the create request, because its gateway cause assertion names the final pool and the session's environment. At finalize, the pod was claimed at create, so a bad ref fails the finalize on a claimed pod ("Credentials", **The gateway runs no git.**, and "Effective policy", **Where the pool checks run**). |
| [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md#flow) and [`0152-user-git-identity`](../0152-user-git-identity/change.md#ug-2-repository-authorization) | The account link check (step 1 of "Flow"), the refusal of a `userCredential` binding on a pod that can serve another session with `422 USER_CREDENTIAL_UNSUPPORTED_POD` ("Limits under both deliveries" under UC-3), which under the owner's settlement of 2026-11-13 of that design's decision 41 and the user git identity design's decision 52 refuses a binding, `lockEgressToHosts`, and a user git identity on a pool whose status records the union fallback and on a recycled or used pod, and admits them on a pool whose status records slot attribution, the refusal of a binding's `lockEgressToHosts`, and of a session row's `egressLockHosts`, on a pool with `egressProfile: internet`, whose raw sockets bypass the lock ([UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file)), and the repository check of [`0152-user-git-identity`](../0152-user-git-identity/change.md) ("When the check runs"). A missing link from either check is reported in the combined missing-links response, `403 LINKS_REQUIRED` with `details.userLinks`, which [UC-3](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding) defines. |
| [`0124-connector-removal`](../0124-connector-removal/change.md) | The refusal of an `identityAssertion` binding with `422 IDENTITY_ASSERTION_UNSUPPORTED_POD` on a pool whose status records the union fallback, on a recycling pool, and on a pod that has served a session, as CR-2 **Pods.** states. The check mirrors the `userCredential` refusal of the row above, runs on the final runtime and pool before the pod claim, and applies to a delegated child that receives the binding. |
| [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-7-session-options) | For every creation kind, `create`, `delegate`, and `derive`, the session's `runtimeOptions` against the chosen revision's `runtimeOptionsSchema`, and on a derived runtime with its own schema against that schema as well, as separate validations, with absent options validated as an empty object. A refusal answers `400 RUNTIME_OPTIONS_INVALID` with `details.runtimeRevision`, and with `details.runtime` and `details.interceptor_ref` when the chain moved the session ("Validation" and "Refusal" under RV-7). For `kind: delegate`, the parent grant's policy cap may refuse the `runtimeOptions` of `session.create` (0166 "DP-1"). |
| [`0136-placement`](../0136-placement/change.md#pm-4-placement-requirements) | For every creation kind, `create`, `delegate`, and `derive`: the final pool's placement offering is a default offering or one granted to the session's tenant, refused with `403 PLACEMENT_OFFERING_NOT_GRANTED` ("Routing check" under PM-3); the final pool and the chosen revision's pod set meet the session's effective placement requirements, the union of the tenant's, the environment's, the operator's per-tenant setting, and for `kind: delegate` the parent's, refused with `422 PLACEMENT_REQUIREMENT_UNMET` ("Where they apply" under PM-4); and the final pool's `placement.region` equals the session's `dataResidencyRegion`, refused with `403 REGION_CONSTRAINT_VIOLATED` ("Post-chain residency check" under PM-7). Each refusal names the last modifier when the chain changed the pool. The requirements also filter the candidate pools when the gateway resolves the final pool, so the post-chain check catches a pool the request or an interceptor named explicitly. |
| [`0144-session-derive`](../0144-session-derive/change.md#sd-1-one-route) | For `kind: derive`, the target checks of SD-1: the admission steps judged against the target runtime and pool, the plan checks, the setup command relabel and `setupCommandPolicy`, and the warm stores on the target ("Checks"); the content stop check ("Stops"); and the isolation monotonicity rule unless the request allows a downgrade. A derive meets the `registered` check of the DR-4 row above ("Final runtime row") and compares no execution modes. |

A write-time or admission check that names a runtime, such as route admission under "RT-4", holds for every active revision of that runtime, because revision selection happens per session.

When the chain changed the runtime or pool and the result fails a check, the gateway refuses the creation with `422 ROUTE_ISOLATION_UNAVAILABLE` for the isolation check, or with the check's own code otherwise, and names the last modifier in `details.interceptor_ref`. It never falls back to the requested runtime. The checks replace the isolation monotonicity rules of §10.7, which compared variant pools with the base runtime's pool at experiment admission.

F-10.6.17, filed in `BUILD-GAPS.md`, records that a rewrite after §10.6 admission escapes the environment selector check. TS-3 owns the rules that close it, and this post-chain point is where they run.

#### Last modifier

`interceptor.Result` gains `ModifiedBy`, the name of the interceptor whose `MODIFY` last changed the payload, set by `Chain.Run` and `Chain.RunRange`. Every phase records it in the audit row for the request, and a refusal at the post-chain point names it.

#### Session context on every phase

After creation, every `InterceptRequest` on every phase carries the same session context in `metadata`: `tenant_id`, `user_id`, `session_id`, `root_session_id`, `runtime`, `pool`, and the routing labels as `routing.<key>` entries. Today only the route paths set `tenant_id` and `user_id`. An LLM, delegation, or output interceptor can then apply rules per runtime or per routing label without a lookup.

#### Payload audit

0141.S1 corrects these payloads, and no others:

- `PreRoute`, on every creation path: the payload of "`PreRoute` payload" above, which replaces `routeTaskSpec` and `childRouteSpec`.
- `PostRoute`: it gains the runtime tags that §4.8 lists, `runtime_options` as an immutable field, the session context, and the routing labels. It does not gain the credential pool assignment, which [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) removes from §4.8 with LLM credential pools under its LU-4, so `credential_pool_id` leaves the payload type.
- Every phase after creation: the session context of "Session context on every phase".

The step compares each other phase's payload with the §4.8 phase payload table and files an issue, under the code of the page that owns the phase, for each field that differs, without changing that payload.

#### Retry and resume

A retried or resumed session uses the runtime, pool, and routing labels stored at creation and does not run the `PreRoute` chain again, and its revision follows [`0140-runtime-revisions`](../0140-runtime-revisions/change.md), so a route with `key: random` cannot move a resumed session. "Validation owed" in `steps.md` records the confirmation against [§7.3](../../spec/07_session-lifecycle.md#73-retry-and-resume).

### RT-3 Routing labels

The session record gains `routing`, a map from string to string.

**Population.**

1. The starting value is empty for `kind: create`, the parent's map for `kind: delegate` (D9), including a child that crosses into another environment through a sharing path (D14), and the source session's map for `kind: derive`.
2. Each `PreRoute` interceptor, in priority order, may return a changed map, and the next one sees the result. The route interceptor sets `route` and `target`. An external interceptor may set any keys, such as `flag` and `variant` taken from a flag service.
3. After the post-chain point passes, the gateway writes the map to the session record. It does not change after that, including on retry and resume.
4. A client request that sets `routing` is rejected with `400 VALIDATION_ERROR`. Client `labels` stay client-owned, so the §14 statement that labels are not used for internal routing remains true.

Keys and values follow the bounds §14 sets for `labels` ("Validation owed" in `steps.md`). A `MODIFY` that leaves `routing` outside those bounds fails the creation with `422 ROUTING_LABELS_INVALID`, naming the interceptor in `details.interceptor_ref`. The keys `route` and `target` are not reserved: an interceptor that runs after the route interceptor may overwrite them, as any later interceptor may override a route ("Limits", **Later interceptors win.**), and the last modifier in the audit row records which interceptor did.

**Consumption.**

| Reader | Carrier | Use |
| :-- | :-- | :-- |
| Runtime | `session_start.routing` and the adapter manifest's per-session fields, in place of `experimentContext`. The runtime SDKs expose it as a map. | Tag traces and logs so the eval platform can compare targets. |
| Delegated child and derived session | The starting value of their own map | Attribution follows the tree and the derive. |
| API clients | The session response, and `GET /v1/sessions` filters such as `routing.target=claude-worker-v2` | Find the sessions of one test. |
| Usage and billing | Billing events and `GET /v1/usage` carry `routing` beside `labels`, in place of `experiment_id` and `variant_id`. | Cost per target. |
| Later interceptor phases | The session context ("RT-2") | Rules per test arm. |

The gateway makes no decision from `routing`. Placement and enforcement read only the session's runtime, revision, and pool.

Under D9 a child carries the labels of the routing decision that produced its tree, so a child that runs a different runtime can carry `routing.target` naming its parent's target. The session's runtime field records what the child runs. The child receives `routing` through the parent link of [`0166-delegation-primitives`, "DP-2"](../0166-delegation-primitives/change.md#dp-2-parent-link), which replaces the delegation lease that carried `experimentContext`. Routing labels stay separate from a session's `labels` and from a parent's tags, which routing never reads (decision 44 of 0166).

### RT-4 Runtime routes

#### Resource

A route is a tenant-scoped admin resource:

```yaml
# POST /v1/admin/routes
name: claude-worker-canary
runtime: claude-worker        # the name a session request asks for: a runtime or an alias
key: user                     # user | session | random
targets:
  - runtime: claude-worker
    weight: 90
  - runtime: claude-worker-v2
    pool: claude-worker-v2-sandboxed-medium   # optional
    weight: 10
```

- A tenant has at most one route per `runtime`, so a request matches at most one route. A route matches on the requested runtime alone (D8).
- **Aliases.** A route's `runtime` may name no runtime in the tenant view. Such a route is an alias, and a request that asks for that name runs on the route's targets. The name follows the runtime name rules of §5.1, so it contains no `/` and cannot be read as the scoped model namespace `environments/{name}/{model}` of §10.6. Admission refuses an alias that names a runtime outside the tenant view, because the name would then reach two different runtimes depending on which tenant asks. When a runtime of the alias's name later enters the tenant view, the route applies to requests for that runtime as any route does. Open decision D22 in `decisions.md` asks how this admission rule meets the tenant-view reads of [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md), which give one answer for an absent name and another tenant's name.
- Targets name runtimes. A target that names a route, or a route whose `runtime` is another route's target within the same tenant, is refused, so resolution is one step.
- Weights are non-negative integers with a positive sum. A target's share is its weight divided by the sum. A weight of 0 stops new sessions on that target.
- Admission refuses a target outside the tenant view, a pool that does not belong to its target's runtime or that the tenant cannot select, and a target whose pools are all weaker than the requested runtime's default pool. The last rule moves the admission check of §10.7 to routes, and it does not apply to an alias, which has no requested runtime; the post-chain isolation check against the session's `minIsolationProfile` still judges every session an alias places. Each rule holds for every active revision of the runtimes the route names. Admission reads the access rows that 0147.S1 adds until 0147.S12 of [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) moves it onto the tenant view that 0147.S4 lands ("Runtime routes" under DR-8), and until 0140.S1 lands, the per-revision rule applies to each runtime's single current definition.
- Admission judges targets when the route is written. A target that later leaves the tenant view, because its access row is revoked, its runtime is deleted, or its row stops being `registered`, does not change the route. Sessions placed on that target are refused at the post-chain point as for an absent name, and discovery stops listing an alias that names it ("Limits", **Targets are judged at write.**).
- A route write that names a target runtime whose default clones carry `write` values answers with a `Warning` header naming those runtimes, because the route can add push access to the sessions it moves. [`0147-tenant-derived-runtimes`, "Write scope"](../0147-tenant-derived-runtimes/change.md#write-scope) gives an experiment write the same warning today.
- Updates use `If-Match`. A route has no status. Deleting it stops routing at once. Open decision D20 in `decisions.md` asks how the gateway replicas read the route store, which bounds "at once", and what the route interceptor does when the store cannot be read.
- A `tenant-admin` writes routes for their own tenant, and a `platform-admin` writes routes for any tenant (D10). A route calls no external service and every target must be in the tenant view, so a `tenant-admin` route reaches only runtimes the tenant could already request. Each write emits the admin audit record ("Validation owed" in `steps.md`).

#### Before the chain

No check before the `PreRoute` chain refuses a requested name for naming no runtime. The tenant-view check at the post-chain point judges the final runtime, so an alias with no route behind it, an alias whose route a higher interceptor overrode with a name that is no runtime, and an alias asked for while `gateway.routes.enabled` is `false` answer as for an absent name.

The create path applies the §11.1 per-runtime concurrency limit, the per-runtime and per-pool request rate limits, and the explicit environment's runtime selector to the requested name in `createAdmissionGates`, before the chain **[source]**. Each judges the wrong runtime once a route moves a session, and an alias would pass the first two, because no session counts against a name that is no runtime, and fail the third. The per-runtime and per-pool limits move to the post-chain point and judge the final runtime and pool. The selector check moves there under TS-1's environment rules, which the post-chain table already lists. The runtime-independent gates, the per-user and global limits among them, stay before the chain so that an over-limit create runs no interceptor. `createAdmissionGates` also judges the requested name with the playground's `playground.allowedRuntimes` boundary (`requirePlaygroundRuntimeVisible`), and the create path validates the §14 envelope and applies the §27.6 playground caps against the requested runtime (F-4.8.26). Open decision D19 in `decisions.md` asks which runtime those checks judge once a route or an interceptor moves the session.

A refusal at the post-chain point, from these limits or from any other check in the post-chain table, releases every reservation the create made before it, such as the token-budget reservation that §4.8 assigns to `QuotaEvaluator` at `PostAuth`, before it answers. A refused create therefore holds no budget. The comments in `createAdmissionGates` state that the per-runtime limits run before the `PostAuth` chain so that an over-limit create reserves no token budget, and "Validation owed" in `steps.md` records whether any reservation happens there today.

#### Discovery

`GET /v1/runtimes` and the single-runtime read `GET /v1/runtimes/{name}` list a tenant's aliases beside its runtimes, each marked as an alias, while `gateway.routes.enabled` is `true`, under [R8](../0138-rest-only-client-api/change.md#ro-2-rest-additions) of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). An alias is listed for a caller when every target with a positive weight is a runtime the list would show that caller, so a listed name never places the caller on a runtime outside their view. Its name is the alias. A route over a runtime adds no entry, because the runtime is listed already. 0141.S2 lists the aliases on `GET /v1/runtimes`. The single-runtime read is new in R8, so 0138.S1, which depends on 0141.S2 for this part, adds it with the aliases.

The provider adapters of that design build their models lists, `GET /v1/models` and `GET /v1/models/{model_id}` on the OpenAI and Anthropic adapters, from these routes, so a models list carries the same aliases under the same rule, with the alias as each entry's `id` and display name. Revision 16 placed the list on the OpenAI translators and the Messages API in the gateway, which leave Lenny under that design.

#### Assignment

For a session whose `requested_runtime` matches a route, the route interceptor:

1. Picks the routing key: the user ID under `key: user`, the root session ID under `key: session` or for an anonymous session under `key: user`, and a random UUID under `key: random`.
2. Computes `HMAC-SHA256(key = tenant_id + "/" + route name, message = routing key)`, maps the first 8 bytes to a bucket in [0, 1), and walks the targets in order by cumulative share. This is the §10.7 bucketing algorithm with integer weights and no control remainder. [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) uses the same function, so the function lives in a shared package. The function takes the HMAC key as a parameter, so open decision O16 of 0140, which asks whether the key 0140 passes carries a value per rollout, changes only the key 0140 passes and leaves this function and the route key unchanged.
3. Returns a `MODIFY` that sets `requested_runtime`, sets `requested_pool` when the target names one, and sets `routing.route` and `routing.target` to the route and target runtime names.

Determinism replaces the sticky cache: a user under `key: user` reaches the same target in every session while the route is unchanged, and every session in a delegation tree under `key: session` reaches the same target. A derived session is its own root, so under `key: session` it is placed independently of its source. Changing weights or inserting a target moves the boundaries and reassigns some keys. Appending a target at the end of the list moves the fewest keys. Renaming a route reassigns every key.

#### Pools

Routes write no pool configuration. Each target's pool scales from its own demand history through the standard formula of §4.6.2. An operator who adds a target sets that pool's `minWarm` for its first sessions, as for any new pool. DP11 has no equivalent, because a route has no pool effects.

#### Turning routes off

The Helm value `gateway.routes.enabled`, default `true`, controls the route interceptor. When it is `false`, the gateway registers no route interceptor and serves no `/v1/admin/routes` endpoint, and the stored routes stay in the database. An operator who replaces routes registers an external `PreRoute` interceptor at any priority above 100.

#### Contract tests

One contract test suite in the `contract` tier drives the `PreRoute` payload and `MODIFY` rules. It runs against the route interceptor in process and against a stub external interceptor over gRPC, so the two cannot drift.

### RT-5 In-cluster interceptors

- **Endpoint.** Registration accepts only `<service>.<namespace>.svc` or `<service>.<namespace>.svc.<cluster domain>`, with a port, where the namespace is listed in `gateway.interceptorNamespaces`. Any other host, including an IP address, is refused with `400 INTERCEPTOR_ENDPOINT_NOT_IN_CLUSTER`. A deployer reaches a remote service through an in-cluster interceptor of their own, which holds its own credentials under its own namespace's egress policy.
- **Transport.** The chart passes the gateway's mTLS leaf and the cluster CA to `--external-interceptor-tls-cert`, `--external-interceptor-tls-key`, and `--external-interceptor-ca`. The gateway dials plaintext only in dev mode or when `mtls.enabled` is `false` because a service mesh provides transport security ("Validation owed" in `steps.md`). In any other mode it refuses to start while an interceptor is registered and the material is absent. Because registrations are written at run time, the same rule holds after start: outside those two modes, `dialInterceptor` refuses to dial without the material rather than falling back to plaintext, and a registration write through `/v1/admin/interceptors` is refused with `503 INTERCEPTOR_TLS_UNAVAILABLE` while the material is absent.
- **Network.** §13.2 drops the sentence admitting interceptors through the public HTTPS rule. The interceptor rule of NET-039 is the only gateway egress to an interceptor.
- **`lenny-egress`.** Interceptor calls do not pass through it (D6). Its tunnels carry TLS end to end and inject credentials only toward an upstream proxy, so it would add a dependency to every session creation without supplying trust or credentials.
- **Every phase.** The rule applies to every registration, whatever its phases.

### RT-6 Tenant-scoped interceptors

- A registration gains `tenants`, a list of tenant IDs. An empty or absent list calls the interceptor for every tenant, as today.
- The chain skips a scoped interceptor for a request of any other tenant, on every phase. The filter reads `Request.TenantID`, which every phase a registration can name carries. A registration cannot name `PreAuth`, which runs before the tenant is known, because the interceptor package refuses that phase for every external interceptor (`ErrInvalidPhase` in `pkg/policy/interceptor`).
- Only a `platform-admin` writes registrations (D4). Admission refuses an unknown tenant ID.
- **Policy references.** A `contentPolicy.interceptorRef` that names a scoped interceptor, at any level from which the content caveat of [`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant) resolves, applies it only to sessions of the listed tenants. A tenant's own setting, at the tenant, an environment, or a tenant-derived runtime, that names an interceptor scoped to other tenants is refused at write time. A change to a registration's `tenants` that would leave such a reference re-validates the referencing settings and is refused with their names. Open decision D21 in `decisions.md` asks what a session of an unlisted tenant gets when a policy that applies to it names a scoped interceptor.
- **Ordering.** The operator guide directs a `platform-admin` to register tenant-scoped interceptors at priorities below the platform's policy interceptors in the same phase, because §4.8 does not re-check a `MODIFY` from an interceptor that runs after `GuardrailsInterceptor` or another policy interceptor.
- Fail-open escalation state is already per interceptor, so one tenant's failing interceptor does not change another tenant's chain.
- **Visibility to the tenant (D15).** A registration that lists tenants is a `platform-admin` write to a resource of each listed tenant. [`0142-security-oversight`](../0142-security-oversight/change.md) mirrors its create, update, and delete to each listed tenant's audit chain under SO-4a, lists the tenant's scoped interceptors in its security posture with their phases, priority, `failPolicy`, and Service name, and states whether its operator access approval covers the registration. D15 covers registrations that list the tenant. An unscoped registration, which receives every tenant's payloads, appears in no tenant's posture ("Limits", **Unscoped interceptors are not in a tenant's posture.**).
- A SaaS provider either registers one scoped interceptor per customer or runs one unscoped dispatcher that forwards by `tenant_id`. Both use the in-cluster rule of "RT-5".

### RT-7 Documentation

The operator guide page `docs/operator-guide/openfeature-integration.md` is replaced by `docs/operator-guide/runtime-routing.md`, which states:

- How to split sessions across runtimes with a route, change weights, and stop it.
- How to run an A/B comparison: the runtime tags traces with `routing`, scores go to the deployer's eval platform, and cost per target comes from usage reports filtered by `routing`.
- How to delegate assignment to a flag service: an in-cluster `PreRoute` interceptor calls the service, sets `requested_runtime`, and writes its own routing keys. The page states the payload and `MODIFY` rules of "RT-2".
- How a SaaS provider attaches customer logic through "RT-6", and the ordering guidance.
- Where to look for rolling out a new version of one runtime: the page links the revisions guide that [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) adds.

`docs/tutorials/evaluation-scoring.md` is deleted, and the runtime author guide replaces `experimentContext` with `routing`.

The other pages state every setting, default, limit, failure mode, and runtime-facing rule this design introduces. The design adds no node requirement and no pool setting.

| Page | What it states |
| :-- | :-- |
| `docs/operator-guide/configuration.md` and `docs/reference/configuration.md` | The Helm values `gateway.routes.enabled` (default `true`; when `false`, no route interceptor and no `/v1/admin/routes`, with stored routes kept) and `gateway.interceptorNamespaces` (the namespaces whose interceptors the chart admits, each with a gateway egress rule and an interceptor certificate). The gateway flags `--external-interceptor-tls-cert`, `--external-interceptor-tls-key`, and `--external-interceptor-ca`, and the rule that the gateway dials an interceptor in plaintext only in dev mode or under `mtls.enabled: false` with a service mesh, and otherwise refuses to start while an interceptor is registered and the material is absent, refuses to dial without it, and refuses a registration write while it is absent. |
| `docs/operator-guide/security.md`, "Content Policy / Request Interceptors" | The in-cluster endpoint rule and the transport rule of "RT-5", the `tenants` field and the ordering guidance of "RT-6", and the scope rule for `contentPolicy.interceptorRef`. |
| `docs/runtime-author-guide/delegation.md` and `docs/client-guide/delegation-and-tasks.md` | That the `PreRoute` chain of a child created with `session.create` may change its runtime, pool, and routing labels, that the checks of a child's creation under 0166 "DP-1" judge the final runtime and pool, that `PreDelegation` is part of the `PreRoute` chain for `kind: delegate`, that the child starts with its parent's routing labels and its root's `labels`, and the scope rule for `contentPolicy.interceptorRef`. |
| `docs/api/admin.md` | The route resource of "RT-4": `name`, `runtime`, `key` (`user`, `session`, or `random`), and `targets` with `runtime`, an optional `pool`, and `weight`. Its limits: one route per requested runtime per tenant, non-negative integer weights with a positive sum, no target that names a route, targets in the tenant view, pools of the target's runtime that the tenant can select, and no target whose pools are all weaker than the requested runtime's default pool. Aliases and their name rule. `If-Match` on updates, the `Warning` header for targets whose default clones carry `write` values, and who may write a route. The interceptor registration's `tenants` field, its in-cluster endpoint rule, and the re-validation of the content caveat settings that reference a scoped interceptor. |
| `docs/api/rest.md` and `docs/client-guide/session-lifecycle.md` | `routing` on the session response and the `routing.<key>` list filters; the refusal of a client-set `routing`; that the requested runtime is a hint the session response resolves; aliases in `GET /v1/runtimes` and the single-runtime read; and the order of creation checks: the `PreRoute` chain, revision selection, the post-chain checks, the claim-time capability check, the claim, and the handshake at bind. |
| `docs/reference/error-catalog.md` and `docs/client-guide/error-handling.md` | `422 ROUTE_ISOLATION_UNAVAILABLE`, `400 INTERCEPTOR_ENDPOINT_NOT_IN_CLUSTER`, `503 INTERCEPTOR_TLS_UNAVAILABLE`, `422 ROUTING_LABELS_INVALID`, the refusals of a child's creation under 0166 "DP-1" judged on the final runtime and pool, `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` for a `MODIFY` that changes an immutable payload field, `400 VALIDATION_ERROR` for a client-set `routing` and for a write that names `experimentTargeting` or `evalRef` (D18), `404 RESOURCE_NOT_FOUND` for a final runtime outside the tenant view or whose row is not `registered`, and `details.interceptor_ref` on every post-chain refusal after the chain changed the runtime or pool. The removed experiment and eval codes leave. |
| `docs/runtime-author-guide/runtime-sdk.md` and `docs/reference/adapter-contract.md` | The runtime-facing rule: `session_start.routing` and the manifest's per-session `routing` replace `experimentContext`, as a map the runtime may use to tag traces and logs and on which the gateway makes no decision. |
| `docs/reference/metrics.md` and `docs/operator-guide/observability.md` | `runtime` in place of `variant_id` on the session metrics, the last modifier in interceptor audit records, and the removed experiment and eval metrics, alerts, and events. |
| `docs/operator-guide/scaling.md` | That routes write no pool configuration, so an operator sets a new target pool's `minWarm`. |

Revision 16 also listed `docs/api/mcp.md` and `docs/api/openai-completions.md` beside `docs/api/rest.md`. Under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), those pages move to the documentation of the MCP and OpenAI adapters, which state the same rules for their protocols, aliases in the OpenAI adapter's `GET /v1/models` among them. This design adds no Lenny page for them.

## Limits

- **No scores or experiment state in Lenny.** A deployer without an eval platform has no built-in way to compare targets beyond the session metrics by `runtime` and the usage reports by `routing`.
- **One route per runtime.** Two concurrent tests on one runtime share a route with several targets, or use an interceptor. The first-match rule that kept tests mutually exclusive is gone.
- **Reassignment on change.** Weight changes reassign some keys.
- **Cold start on a new target.** The operator sets the new pool's `minWarm`, because routes do not size pools.
- **Over-limit creates run the chain.** A create that the per-runtime or per-pool limits refuse runs the `PostAuth` and `PreRoute` chains first, so every interceptor in them, external ones included, receives a call for a create that fails. An external flag service can record an assignment for such a create. The per-user, global, and tenant gates still run before the chain and bound the extra calls, and every check at the post-chain point has the same cost.
- **Aliases are per tenant.** A deployer who offers the same alias to every tenant writes one route per tenant, or a `PreRoute` interceptor that maps the name (D12).
- **The requested runtime is a hint.** A client that asks for `claude-worker` may run `claude-worker-v2`. The session response names the resolved runtime and the routing labels.
- **A routed derive can change runtime.** A derive that names a routed runtime can run on another target, subject to derive's checks. A client that wants a specific runtime names a runtime no route matches.
- **A relay for remote services.** A SaaS customer's externally hosted logic needs an in-cluster relay that the provider operates.
- **Later interceptors win.** An external interceptor above priority 300 can override a route. The last modifier in the audit row makes that visible.
- **Targets are judged at write.** A route whose target leaves the tenant view after admission keeps the target. The share of sessions placed on it is refused as for an absent name, and the route's owner receives no signal beyond those refusals and the alias leaving discovery.
- **Unscoped interceptors are not in a tenant's posture.** A tenant's security posture lists the registrations that name the tenant (D15). An unscoped registration receives every tenant's payloads and is listed in no tenant's posture.

## Alternatives rejected

- **Remove only the vendor SDKs.** Revision 4 did this. It kept the experimentation subsystem and added a credential store and an egress rule for OFREP.
- **Interceptors only, with no built-in routes.** Every split would need a deployer-run service.
- **Run routes as a separate gRPC service.** It would add a network hop to every session creation and a component to every deployment. The shared contract tests give the same assurance.
- **Keep the experiment object on top of routes.** The control group, status lifecycle, and propagation modes exist for statistical analysis, which Lenny leaves to other platforms.
- **Match routes on client labels.** This lets a client choose its target (D8).
- **Skip routing for derives.** A derive names a runtime as a create does. Skipping would make a route apply to some creation paths and not others, and SD-1's refusal would refuse every derive a route moves.
- **Keep `evalRef` as a reserved field.** Nothing in Lenny reads it once eval storage is gone, and a session label does the same job.
- **Allow external interceptor endpoints with per-registration TLS and credentials.** This rebuilds the credential and egress surfaces that revision 4 added for OFREP.
- **Route interceptor calls through `lenny-egress`.** It supplies neither trust nor credentials for a tunnelled call, and it is not built.
- **Let a `tenant-admin` register interceptors.** The gateway would call tenant-chosen code on every session creation of that tenant (D4).
- **Store routing labels in client labels under a reserved prefix.** It mixes client-owned and gateway-owned data, and §14 states that labels are not used for routing.
- **Let routes target routes.** Chains admit cycles and make effective shares hard to read.
- **Keep the sticky cache.** Deterministic hashing gives the same stickiness with no store and no erasure step.
- **Add routes that apply across tenants.** Revisions of a platform runtime reach every tenant with access, and their questions of precedence over tenant routes do not arise (D12).

## Specification changes

The following edits locate their targets by heading. Change 0115 created placeholder pages whose "Content today" sections name this change as their source: [`ROUTE`](../../spec/capabilities/runtimes-and-pools/runtime-routes.md) (runtime routes) for the requirements, "RT-3", and "RT-4"; [`INTCPT`](../../spec/capabilities/policy-and-controls/request-interceptors.md) (request interceptors) for "RT-2", "RT-5", and "RT-6"; and [`INTCPTIF`](../../spec/interfaces/interceptor-protocol.md) (interceptor protocol) for the payloads of "RT-2". Under the program's "Order", the area change of each area writes its pages and discharges the specification lane of this change. A row below that names a numbered section applies to the page that holds that section's content when the step lands, and RTR-n becomes a requirement of the page that states it. §18 and §23 are successor stubs, so their rows apply to `docs/about/status.md` and `docs/about/comparisons.md`, as the program's "Reading the program" states.

| Section | Change |
| :-- | :-- |
| [§10.7 Experiment Primitives](../../spec/10_gateway-internals.md#107-experiment-primitives) | Replace the section with "10.7 Runtime routing", stating "RT-3" and "RT-4". The heading changes, so 0141.S2 adds an anchor redirect for `#107-experiment-primitives`. |
| [§4.6.1](../../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle), "Re-activation grace period" | Remove the variant pool example. |
| [§4.6.2](../../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration) | Remove the experiment sizing role, the variant pool formula, the base pool adjustment, and the experiment teardown pointer. |
| [§4.7.6](../../spec/04_system-components.md#476-adapter-manifest-field-reference), "Per-session fields" | Replace `experimentContext` with `routing`, under manifest `version: 2`, whose field set [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) states in "RP-6". |
| [§4.8](../../spec/04_system-components.md#48-gateway-policy-engine), built-in table, phase payload table, and field dependencies | Replace `ExperimentRouter` with the route interceptor. State the "RT-2" payload, `kind`, `MODIFY` rights, `runtime_options` as immutable in the `PreRoute` and `PostRoute` payloads, single chain, post-chain checks, last modifier, session context, and the payload corrections that "RT-2" lists under **Payload audit**. State the "RT-5" endpoint rule and the "RT-6" tenant scope and ordering. Remove "or an external host:port". |
| [§5.2](../../spec/05_runtime-registry-and-pool-model.md), "Scaling Implications" | Remove the variant pool paragraph. [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md#specification-changes) renames §5.2 "Pool Configuration" and this subsection "Scaling Implications" through the anchor-redirect map (its decision O7). |
| [§7.1 Normal Flow](../../spec/07_session-lifecycle.md#71-normal-flow), step 3a | Describe the `PreRoute` chain, revision selection, the post-chain point, the claim-time capability check of RP-2, the pod claim, and the handshake at bind, in that order, with the `registered` check of DR-4 among the post-chain checks. |
| [§7.3 Retry and Resume](../../spec/07_session-lifecycle.md#73-retry-and-resume) | State that a retry or resume keeps the stored runtime, pool, and routing labels. |
| [§8.2](../../spec/08_recursive-delegation.md#82-delegation-mechanism) step 2b and [§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) "Delegation Lease" | State that the child's chain honours runtime, pool, and routing changes. Move step 2a-bis (effective `maxDepth` resolution) after step 2b, so that it judges the final runtime and pool at the post-chain point, and name the last modifier in its refusals. Step 2a (cycle detection and the three-layer self-recursion gate) is not moved, because [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#specification-and-non-specification-changes) removes it under its decision 5 and rewrites §8 around the session grant, whose checks it places at the post-chain point. Replace `experimentContext` with `routing`. State the tenant scope rule for `contentPolicy.interceptorRef`. |
| [§11.1](../../spec/11_policy-and-controls.md#111-admission-and-fairness) | State that the per-runtime concurrency limit and the per-runtime and per-pool request rate limits judge the final runtime and pool after the `PreRoute` chain ("RT-4", "Before the chain"). |
| [§15.1 REST API](../../spec/15_external-api-surface.md#151-rest-api), `GET /v1/runtimes` and R8's single-runtime read | State that discovery carries the caller's tenant aliases, each marked as an alias, under the rule of "RT-4", "Discovery", and that a session's requested runtime may name an alias. Revision 16 named §9.1 "Runtime Discovery" and `GET /v1/models`, which leave Lenny with the MCP and provider surfaces under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). |
| [§10.3](../../spec/10_gateway-internals.md#103-mtls-pki), NET-063 | State that every interceptor is in-cluster, the dev-mode and service-mesh exceptions for plaintext, and that outside them the gateway refuses to dial without TLS material and refuses a registration write while it is absent. |
| [§11.2.1 Billing Event Stream](../../spec/11_policy-and-controls.md#1121-billing-event-stream) | Replace `experiment_id` and `variant_id` with `routing`. |
| [§12.2](../../spec/12_storage-architecture.md#122-storage-roles), [§12.4](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes), [§12.6](../../spec/12_storage-architecture.md#126-interface-design), and [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) | Remove `EvalResultStore`, the sticky cache keys and their failure row, and their erasure steps, naming each step by its store. Add the route store, and add it to the tenant erasure order. Renumber the `DeleteByUser` sequence as it stands when the step lands, because other member changes edit the same sequence. |
| [§13.2](../../spec/13_security-model.md#132-network-isolation), gateway egress row and the NET-039 note | Remove the external interceptor sentence. |
| [§14](../../spec/14_workspace-plan-schema.md), `CreateSessionRequest` fields | State that `routing` is gateway-owned and refused on requests. |
| [§15.1 REST API](../../spec/15_external-api-surface.md#151-rest-api) | Remove the eval and experiment endpoints, their dry-run and deletion rules, the eval error codes, and `evalRef`. Add `/v1/admin/routes`, `routing` on session responses and filters, `ROUTE_ISOLATION_UNAVAILABLE`, `INTERCEPTOR_ENDPOINT_NOT_IN_CLUSTER`, `INTERCEPTOR_TLS_UNAVAILABLE`, `ROUTING_LABELS_INVALID`, and the `tenants` registration field. If [`0144-session-derive`](../0144-session-derive/change.md) lands first, its derive row loses `evalRef` here. |
| [§15.7 Runtime Author SDKs](../../spec/15_external-api-surface.md#157-runtime-author-sdks) | Replace `ExperimentContext` with `Routing`. |
| [§16.1](../../spec/16_observability.md#161-metrics), [§16.5](../../spec/16_observability.md#165-alerting-rules-and-slos), [§16.6](../../spec/16_observability.md#166-operational-events-catalog), and [§16.7](../../spec/16_observability.md#167-section-25-audit-events) | Remove the experiment and eval metrics, alerts, and events. Replace `variant_id` with `runtime` on the session metrics. Remove the experiment-targeting meaning of the `provider` label. Add the last modifier to the interceptor audit records. |
| [§18](../../spec/18_build-sequence.md), successor `docs/about/status.md` | Replace the "Experimentation primitives" and "Score storage and retrieval" rows with a runtime routes row. |
| [§23](../../spec/23_competitive-landscape.md#additional-platform-capabilities), successor `docs/about/comparisons.md` | Replace the experimentation and eval hook rows and entries with runtime routing through `PreRoute`. |
| [§28.5.3 Intra-pod](../../spec/28_communication-channels.md#2853-intra-pod) | Replace `experimentContext` on `session_start` with `routing`. The §28 channel register holds no entry for the gateway-to-interceptor link today: the artifact row for `schemas/lenny-interceptor.proto` records that. Whether 0141.S3 adds a link row with the in-cluster rule, under the channel naming rule, is left to the build of that step. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| Removed packages and files | Every gateway, controller, adapter, SDK, and alert location in "Footprint". The step that removes the `pkg/gateway/experiment/` packages deletes their seven `removed-by: '0141'` rows from `tests/registers/gateway-layout.yaml`, and because its diff touches `tests/registers/`, the `review` skill runs over that diff before it merges (`.claude/rules/git-workflow.md`, "Review changes to the merge gate"). |
| `pkg/policy/interceptor` | The shared `PreRoute` payload type, immutable field paths for its new fields, `ModifiedBy` on `Result`, the session context builder, and the tenant filter in `Chain`. |
| `pkg/policy/interceptor/interceptorstore` and `pkg/gateway/api/admin/interceptors.go` | The `tenants` field, the in-cluster endpoint check, admission of tenant IDs, and re-validation of the referencing content caveat settings. |
| `pkg/routing` (new) | The route interceptor, `pkg/routing/routestore` with its Postgres store, and the shared bucketing function. The route store and the bucketing function are domain logic, which `pkg/gateway/ARCHITECTURE.md` places in concern directories under `pkg/`, and [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) uses the bucketing function from code that the layout's import rule forbids to import a package of `pkg/gateway`. The gateway's composition root registers the route interceptor. |
| `pkg/gateway/api/admin` | The `/v1/admin/routes` handlers and the gate of D10. The handlers of each setting that names a `contentPolicy.interceptorRef` under 0166 "DP-1" check its scope. |
| `pkg/gateway/core/session/sessionserver` | One chain run, in `CreateSessionService`, `CreateAndStartService`, and the derive path, the minted session ID before the chain, the post-chain point with the checks every owning design places there, `routing` on the record, request refusal of `routing`, `routing` on usage, and the derive path through the shared create path. |
| `pkg/gateway/platformtools/mcptools` and `pkg/gateway/platformtools/delegation` | The shared payload, honoured runtime, pool, and routing changes, the parent's routing for the child, and the effective `maxDepth` resolution of §8.2 on the final runtime and pool. 0166 replaces the delegation tools these packages serve with the platform operations of its "DP-1". |
| `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` | Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md): 0141.S1 reshapes `GWSEAM-Intercept` (its result gains the last modifier, its request carries the session context, and a `PreRoute` `MODIFY` returns the runtime, pool, and routing labels) and adds or reshapes the `seam.ChildSessions` operation through which the platform tools seam has the session core apply a delegated child's routing and run its post-chain checks. 0141.S2 removes `GWSEAM-ApplyExperimentRouting` when 0141.S1 has not replaced it. Each step regenerates `spec/catalogs/api-operations.md`. |
| Every interceptor call site | The session context in `metadata`, and the corrected payloads the audit finds. |
| `pkg/gateway/core/session/watchdog` | `routing` on billing events. |
| `pkg/adapter`, `schemas/lenny-adapter.proto`, and the runtime SDKs | `routing` in place of `ExperimentContext`. Regenerate the proto code. |
| `migrations` | The forward migration of "RT-1". |
| `cmd/lenny-gateway` | Route interceptor wiring, the `gateway.routes.enabled` flag, and the transport rule of "RT-5". |
| `charts/lenny` | The interceptor TLS flags, `gateway.routes.enabled`, and removal of experiment values. Regenerate `pkg/embedded/manifests/manifests.yaml` if it changes. |
| `docs/` and `README.md` | "RT-7", and removal of every experiment, eval, OpenFeature, and vendor reference. |
| `TESTING.md` and `pkg/gateway/ARCHITECTURE.md` | Remove the experiment and eval entries and add the routing ones. The spec-map and change-graph files that earlier revisions listed were retired by 0116.S6, and the tests carry `// spec:` annotations in their place. |
| `BUILD-GAPS.md` and `TEST-GAPS.md` | The steps close the findings their `closes` fields list, and leave closed findings' text intact. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| `static` | A static check fails if a feature-flag vendor SDK or `open-feature/go-sdk` enters the gateway's dependencies, or if `experimentContext`, `EvalResult`, or `evalRef` reappears in the schema or the code outside the validators' lists of removed fields and their tests (RTR-1). |
| `unit` | Alias admission: a name that is no runtime is accepted, a name outside the tenant view and a name with `/` are refused, and the isolation admission rule is not applied to an alias. Discovery shows an alias only when every positive-weight target is visible to the caller (RTR-12). Bucketing shares within tolerance over many keys, stability per key, appended-target movement, anonymous `key: user`, and `key: random` (RTR-4). Route admission rules. Immutable field enforcement on each new payload field, including `kind` and `runtime_options`, in the `PreRoute` and `PostRoute` payloads. `ModifiedBy` across several `MODIFY` responses. The tenant filter on every phase (RTR-9). The endpoint check, and the refusal to dial an interceptor without TLS material outside dev mode and service-mesh mode (RTR-8). A `MODIFY` that leaves `routing` outside the bounds fails with `ROUTING_LABELS_INVALID`. |
| `contract` | The shared contract suite against the route interceptor and a stub external interceptor (RTR-5). A tenant write that names `experimentTargeting`, and a derive or session list request that names `evalRef`, are refused with `400 VALIDATION_ERROR` naming the field (D18). The `session_start` frame with `routing`, and the proto round trip. The session context and the corrected payload on each phase (RTR-10). |
| `integration` | On the compose stack, through each creation kind: a route moves a session, an external interceptor at priority 150 changes the requested runtime before the route matches, one at priority 400 overrides the route, a child created with `session.create` honours a runtime change, inherits `routing`, and carries its root's `labels` in the payload, and a derive that names a routed runtime is routed, while one that omits `runtimeRef` asks for the source's resolved runtime, so that a route keyed on the runtime the source first asked for does not move it and a route keyed on the resolved runtime does (RTR-2). Post-chain refusals with `ROUTE_ISOLATION_UNAVAILABLE` and with `RESOURCE_NOT_FOUND` for a final runtime whose row is not `registered`, on each creation kind, each naming the last modifier, and the drain gate on a routed pool (RTR-3). A route that moves a session onto a runtime or pool that fails one owning design's check is refused with that design's code, one case for each row of the post-chain table, including each of the placement row's three refusals on each creation kind, and a check that reads a versioned field reads the chosen revision. A route that moves a session onto a runtime whose pod set's recorded report lacks a declared capability passes every post-chain check and is then refused with `RUNTIME_CAPABILITY_MISSING` before any claim. On a pod with two slots and slot attribution, a session with `egress.allowedHosts` is not refused. A route that moves a session onto a runtime whose schema refuses its options is refused with `RUNTIME_OPTIONS_INVALID` naming the runtime, the revision, and the last modifier, on each creation kind, and a `PreRoute` or `PostRoute` `MODIFY` that changes `runtime_options` is refused with `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`. A delegated child that crosses environments keeps its parent's routing labels (D14). `routing` on the session response, the list filter, usage, and billing events, and refusal of a client-set `routing` (RTR-7). A resumed session under a `key: random` route keeps its runtime and labels (RTR-11). With `gateway.routes.enabled: false`, every other creation test passes unchanged (RTR-6). A REST `POST /v1/sessions` that names an alias runs on a target, `GET /v1/runtimes` lists the alias, a session moved by an alias counts against its target's per-runtime concurrency limit, and an alias with its route deleted answers as an absent name (RTR-12); the provider adapters' cases for aliases belong to the conformance kit of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) (0138.S5). For `kind: delegate`, a route or an interceptor that moves a child onto a runtime and pool already in its lineage with `gateway.allowSelfRecursion: no` is refused with `DELEGATION_CYCLE_DETECTED` naming the last modifier. One case for each row of the post-chain table that names a refusal, including the rows of 0122, with a `from: latest` derive for its derive case, 0133, and 0150, and the `checkpointExcludes` list of 0131 composed from the chosen revision. |
| `e2e_kind` | On Kind: an in-cluster interceptor is reached over mTLS with the SPIFFE check, and a registration naming an outside host is refused (RTR-8). |
| `load_local` | Concurrent creates across tenants with scoped interceptors stay isolated under `-race`, and the route store's cache stays consistent during weight updates. |
| `chaos` | A `fail-closed` scoped interceptor that is killed rejects only its tenant's creates, and recovery restores them. |
| `security` | A `tenant-admin` cannot write a route for another tenant or with a target outside its tenant view. A tenant-scoped interceptor never receives another tenant's payload, and no content caveat setting of a tenant can reference another tenant's scoped interceptor. The gateway refuses to start with registered interceptors and no TLS material outside dev mode and service-mesh mode, and a gateway started without registrations and without the material refuses a registration write with `INTERCEPTOR_TLS_UNAVAILABLE` and dials no interceptor in plaintext. A `tenant-admin` route or an external interceptor that moves a delegated child onto a runtime outside its parent grant's policy cap, onto a pool below its isolation floor, or past an SO-5a hold on the target runtime does not pass the post-chain point (0166 "DP-1"). Tenant deletion removes the tenant's routes and removes it from every registration's `tenants`, and a registration whose list empties is deleted rather than applied to every tenant. |
| `docs` | Every route example in the documentation passes admission, and no documentation page names the removed endpoints, fields, metrics, or alerts. |

Every test carries the `// spec:` annotation, and tests of the `component` tier and the tiers after it carry `// diagnosis:`, as `test-coverage.md` requires.

## Instructions for reviewers of other designs

These designs refer to the removed subsystem, the `PreRoute` chain, derive, the checks after a rewrite, or interceptor registration:

- [`0144-session-derive`](../0144-session-derive/change.md) defines "Experiment" and `evalRef`, moves `evalRef` to derive, refuses a derive whose runtime an interceptor rewrites ("Rewrites" under "Checks"), and leaves `routeExperiment` out of the shared row build. Its reviewer removes the "Experiment" term, `evalRef` from the derive request, the session row, the list filter, and the §15.1 edits, and the `routeExperiment` mentions. It replaces the rewrite refusal with D11: a derive runs the `PreRoute` chain with `kind: derive`, starts its routing labels from the source's, starts `requested_pool` as "RT-2" states, and meets its "Checks" and "Stops" and DR-4's `registered` check at the post-chain point on the chosen revision.
- [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md), "Fields", lists `experiment_id` and `variant_id` in the common envelope of `session.pod_usage`. Its reviewer replaces them with `routing`, and with `runtime_revision` from [`0140-runtime-revisions`](../0140-runtime-revisions/change.md).
- [`0123-connector-authorization-and-external-memory`](../0123-connector-authorization-and-external-memory/change.md) defines "Experiment routing", places the CA-4 check after `routeExperiment` and `preRouteAfterExperiment` ("Placement"), and places the CA-7 check after `ApplyExperimentRouting`. Its reviewer removes the term and states both checks at the post-chain point of "RT-2", reading the chosen revision's capabilities. [`0124-connector-removal`](../0124-connector-removal/change.md) has since withdrawn CA-4 and CA-7, so neither check runs and this instruction is the record of what was asked.
- [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) refuses a missing account link before any pod claim (step 1 of "Flow") and a `userCredential` binding on a pod that can serve another session. Its reviewer states that both run at the post-chain point, on the final runtime and pool.
- [`0152-user-git-identity`](../0152-user-git-identity/change.md), "When the check runs", refuses a missing repository link before any pod claim. Its reviewer states that the check runs at the post-chain point.
- [`0127-egress-and-git`](../0127-egress-and-git/change.md) refuses a `gitClone` plan on a pool with no git path ("Git mirror") and a session list of hosts on a pod with several slots where slot attribution is unavailable ("Effective policy", decision 60 (a)). Its revision 76 states that both refusals read the final pool at the post-chain point ("Effective policy", **Where the pool checks run**), and that the gateway knows before the claim whether a pool's pods have slot attribution from the pool's status, in which the controller records whether `netagent` found a reader at start.
- [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) refers to experiments in several places. Its reviewer:
  - removes the "Experiment and variant" term, DP11, DR-1's experiment rules and DF-9's experiment path, `checkVariantIsolation` under "Reading", the `initialMinWarm` refusal under DR-5, the "Experiments" subsection of DR-8, decision 13, and the experiment tests, including those of `ExperimentVariantSource`;
  - restates the tenant-view miss under "Reading" as a check at the post-chain point, and adds to it the refusal of a final runtime row that is not `registered`, for every create, derive, and delegated child, answered as an unknown runtime (decision O8 of `0119-agent-runtimes-only`); DR-8's export refusal onto a target with no runtime supervisor leaves under the same decision;
  - replaces the experiment bullet under "Write scope" with the route write `Warning` of "RT-4", and the experiment sentence under "Binding" with a route target;
  - checks that route targets read the tenant view and pool access, as "RT-4" requires.
- [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) refers to experiment routing in its terms, its checks, a scenario, and a finding. Its reviewer:
  - replaces the "Experiment routing and `PreRoute`" term with the `PreRoute` chain and the post-chain point;
  - restates the final-row checks of "A session's environment" under TS-1 and the crossing re-check of "Cross-environment sharing" under TS-3 as checks at the post-chain point, with `environment` in the `PreRoute` payload being the parent's for `kind: delegate`;
  - adds `routing` to what moves across under **Model.**, per D14;
  - rewrites the "An experiment variant" scenario as a route, the §10.7 row of its specification list, and the experiment wording of its tests;
  - records F-10.6.17 as closed by TS-3's rules at the post-chain point of "RT-2".
  "SSRF and NetworkPolicy" needs no change, because "RT-5" removes interceptors from the external HTTPS path and adds no rule.
- [`0142-security-oversight`](../0142-security-oversight/change.md) does not cover tenant-scoped interceptors. Its reviewer applies D15: SO-4a mirrors a registration's create, update, and delete to each listed tenant as `kind: admin_write`, "Posture" gains a section that lists the tenant's scoped interceptors, and the design states whether decision 15's approval gate covers a registration.
