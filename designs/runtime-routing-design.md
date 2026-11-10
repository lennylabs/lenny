# Runtime routing

Status: design for owner review, revision 11, 2026-11-09, with no independent review yet. Revision 5 replaced the design that revision 4 described under the title "Built-in experiment provider removal" and the file name `experiment-provider-removal-design.md`. The owner has settled every decision in "Decisions". It reads against [`session-derive-design.md`](session-derive-design.md) revision 16, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 8, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 64, [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) revision 32, [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) revision 12, [`user-credential-delivery-design.md`](user-credential-delivery-design.md) revision 26, [`user-git-identity-design.md`](user-git-identity-design.md) revision 34, [`egress-and-git-design.md`](egress-and-git-design.md) revision 74, and [`security-oversight-design.md`](security-oversight-design.md) revision 47. It stages no specification edit. The changes go through `change-proposal` after review. Proposals carry the label RT-n, and requirements carry the label RTR-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | Run on 2026-11-03 against this tree through a Go build overlay, with no change to the tree. |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-08, or on 2026-11-09 for the claims revision 7 added. |
| **[design]** | What this design requires. Nothing implements it. |

**Evidence sources.** A *Go build overlay* is the Go toolchain's `-overlay` build flag, which substitutes files at build time and leaves the tree unchanged. The binary size comparison in "Current state" used one to replace `experimentprovider.go` with a stub. "Validation owed" lists the evidence still missing.

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

Lenny carries a built-in experimentation subsystem in [§10.7](../spec/10_gateway-internals.md#107-experiment-primitives). It defines an experiment object with variants, a control group, a status lifecycle, and rules for delegated children. It assigns sessions to variants by a percentage hash or by asking an external flag service through vendor SDKs or OFREP. It sizes variant pools from the variant weights, caches assignments in Redis, and stores evaluation scores with a results API that aggregates them per variant.

The product review called the project's scope too broad for its stage, and this subsystem is a large part of that scope. Revision 4 removed only the vendor SDKs. Two observations widen the change:

- Eval storage duplicates what runtime-native eval platforms do, and §10.7 already names those platforms as the primary path.
- Assigning a session to a variant is a special case of choosing the runtime a session runs. The gateway already has a generic hook for that choice: an interceptor at the `PreRoute` phase of [§4.8](../spec/04_system-components.md#48-gateway-policy-engine) may rewrite the requested runtime. External targeting duplicates that hook with a second configuration surface, a second failure model, and a second credential store.

This design removes the experimentation subsystem and eval storage. It keeps one built-in routing feature, weighted runtime routes, and builds it as a `PreRoute` interceptor that follows the contract an external interceptor follows. It also corrects the `PreRoute` contract, which the code implements more narrowly than the specification states, gives every interceptor phase the same session context, and restricts interceptors to in-cluster services.

Rolling out a new version of one runtime is a related problem with a different owner and a different mechanism. [`runtime-revisions-design.md`](runtime-revisions-design.md) covers it.

## Scope

**Removed:**

- `ExperimentDefinition`, the `/v1/admin/experiments` API, the experiment status lifecycle, and the `ExperimentRouter`.
- The reserved `control` variant, the first-match rule across experiments, and the isolation checks specific to experiments.
- `propagation.childSessions` and the innermost-wins rule for delegated children.
- `targeting.mode: external`, the tenant's `experimentTargeting` block, the OFREP client, the vendor SDK providers, and the targeting circuit breaker.
- The Redis sticky assignment cache and its invalidation.
- Variant pool sizing in the PoolScalingController, including the variant pool formula, the base pool adjustment, and `initialMinWarm`.
- `experimentContext` on `session_start`, the adapter manifest, delegation leases, the adapter proto, and the runtime SDKs.
- Eval storage: `POST /v1/sessions/{id}/eval`, `EvalResult`, `EvalResultStore`, the results API with its blinding rules, the eval erasers, and the `lenny_eval_aggregates` materialized view.
- `evalRef`, which [`session-derive-design.md`](session-derive-design.md) moves from replay to derive ("RT-1").
- The reserved `ExperimentHealthEvaluator`, the manual rollback triggers, and the guidance on combining Lenny with external experimentation platforms.
- The experiment metrics, operational events, audit events, and alerts.
- The `experiment_id` and `variant_id` fields of billing events, and the `variant_id` label on session metrics.

**Added or changed:**

- A single `PreRoute` payload for every session creation path, including delegation and derive, with a `kind` field and the fields routing needs ("RT-2").
- Derive runs the `PreRoute` chain like a create, and its own checks run after the chain ("RT-2").
- One post-chain point, after the chain and revision selection and before the pod claim, at which every check that depends on the runtime, the revision, or the pool runs, including the checks other designs own ("RT-2").
- The same session context on every interceptor phase after creation, and a record of which interceptor last changed a payload ("RT-2").
- Routing labels, a gateway-owned map on the session that interceptors write and runtimes read ("RT-3").
- Runtime routes, a built-in `PreRoute` interceptor that spreads the sessions requesting a runtime across target runtimes by weight ("RT-4").
- Interceptor endpoints restricted to the cluster, with mTLS required outside dev mode ("RT-5").
- Interceptor registration scoped to a list of tenants ("RT-6").
- Operator documentation for running an experiment with these parts ("RT-7").

**Kept without behaviour change:**

- The `PreRoute` and `PostRoute` phases, interceptor priorities, `failPolicy`, timeouts, and fail-open escalation.
- `tracingContext` propagation through delegation, which runtimes use to stitch traces in their eval platform.
- The drain gate and the isolation checks that apply to a requested runtime.

**Out of scope:**

- Statistical analysis, scoring, and experiment lifecycle management. These stay outside Lenny.
- Interceptors written or registered by a `tenant-admin`. Registration stays with `platform-admin` (D4).
- Routes that apply across tenants, and staged rollout of a new version of one runtime. [`runtime-revisions-design.md`](runtime-revisions-design.md) covers the second, which also gives operators a rollout that reaches every tenant (D12).

## Terms

| Term | Meaning |
| :-- | :-- |
| Route | A runtime route: an admin resource that names one runtime a request can ask for and the target runtimes that sessions asking for it run on, each with a weight. "RT-4" defines it. |
| Target | One entry of a route: a runtime, an optional pool, and an integer weight. A target may be the requested runtime itself. |
| Routing key | The value a route hashes to place a session on a target. The route's `key` field selects it: the user ID under `key: user`, the root session ID under `key: session`, or a fresh random value under `key: random`. |
| Root session | The top-level session of a delegation tree. A session that is not a delegated child is its own root, and a derived session is a new root. |
| Routing labels | The `routing` map on a session: string keys and values that `PreRoute` interceptors write, that the gateway stores on the session record, and that the runtime receives in `session_start`. "RT-3" defines it. |
| Route interceptor | The built-in `PreRoute` interceptor that applies routes. |
| `PreRoute` payload | The JSON document the gateway passes to every `PreRoute` interceptor, and the fields an interceptor may change in a `MODIFY` response. "RT-2" defines it. |
| Creation kind | The `kind` field of the `PreRoute` payload: `create` for a REST create or create-and-start, `delegate` for a delegated child, and `derive` for a session derived from an earlier one. |
| Post-chain point and post-chain checks | The post-chain point is the single step of session creation that follows the `PreRoute` chain and revision selection and precedes the pod claim. The post-chain checks are every check that depends on the runtime, the revision, or the pool, which run there on what the chain chose, whichever interceptor chose it. "RT-2" lists them by owning design. |
| Final row | The session row as the post-chain point sees it: the runtime and pool the chain left, the revision chosen for that runtime, and the session's environment. Other designs use the term for the row their re-checks read. |
| Last modifier | The interceptor whose `MODIFY` last changed a field of the payload in one chain run. Errors from post-chain checks name it. |
| Session context | The fields every interceptor call after creation carries in `InterceptRequest.metadata`: tenant, user, session, root session, runtime, pool, and routing labels. "RT-2" defines it. |
| Experimentation subsystem | Everything this design removes from §10.7: experiments, variants, external targeting, the sticky cache, variant pool sizing, and eval storage. |
| OFREP | The OpenFeature Remote Evaluation Protocol, a vendor-neutral REST protocol for asking a flag service for a flag's value. The gateway's OFREP client is one of the removed paths. |
| SDK path | External targeting through vendor SDKs for LaunchDarkly, Statsig, and Unleash compiled into the gateway, in the `experimentprovider` package. |

### Terms from other designs and the platform

This design builds on mechanisms the specification, the tree, and other designs define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Gateway | Lenny's API gateway, the only externally facing component, through which every client request passes and which creates sessions ([§4.1](../spec/04_system-components.md#41-edge-gateway-replicas)). It runs as several replicas, and each replica serves every tenant. |
| Runtime and pool | A runtime is a registered agent image and its configuration. A pool is a set of pre-warmed pods for one runtime at one isolation profile and resource class ([§5](../spec/05_runtime-registry-and-pool-model.md)). A session names a runtime and may pin a pool. |
| Runtime revision | An immutable numbered version of a runtime's pod-defining fields, which [`runtime-revisions-design.md`](runtime-revisions-design.md) adds. The gateway picks a revision of the final runtime after the `PreRoute` chain and before the post-chain checks, so the checks read the chosen revision. |
| Interceptor | A hook the gateway calls at a named phase of request handling ([§4.8](../spec/04_system-components.md#48-gateway-policy-engine)). It answers `ALLOW`, `REJECT`, or `MODIFY`, and a `MODIFY` returns a changed payload. Built-in interceptors run in the gateway process. External interceptors are gRPC services that a `platform-admin` registers through `/v1/admin/interceptors`. |
| `PreRoute` and `PostRoute` | The interceptor phases before and after the gateway selects a session's runtime. A `PreRoute` interceptor may change the runtime. A `PostRoute` interceptor sees the resolved runtime and may not change it. |
| Priority | The order of interceptors within a phase, lowest first. Priorities up to 100 are reserved for built-ins. |
| `failPolicy` and fail-open escalation | How the chain treats an interceptor's timeout or error: `fail-closed` rejects the request and `fail-open` skips the interceptor. A `fail-open` interceptor that fails too often within a window is escalated to `fail-closed`. |
| Immutable field enforcement | The gateway's comparison of protected payload fields before and after a `MODIFY`. A change to a protected field rejects the request with `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`. |
| `GuardrailsInterceptor` | The built-in content classifier hook at priority 400. §4.8 states that a `MODIFY` from an interceptor that runs after it is not checked again. |
| Session creation paths | The REST create (`POST /v1/sessions`), create-and-start, the delegated child a runtime starts with `delegate_task` ([§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism)), and derive (`POST /v1/sessions/{id}/derive`). |
| Derive | A new session that starts from an earlier session, either from its snapshot (`from: latest`, the default) or from its stored plan with no snapshot (`from: start`), with an optional `runtimeRef` and `targetPool`. [`session-derive-design.md`](session-derive-design.md) makes it the single route for this and removes replay. A derive requires the target's `executionMode` to equal the source's and its isolation to be no weaker than the source's, and it refuses a source whose plan holds stopped content. |
| `DelegationPolicy` and `contentPolicy.interceptorRef` | A delegation policy may name an interceptor that inspects delegation input and exported files ([§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| Delegation lease | The record of what a delegated child receives from its parent ([§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)). |
| `session_start` | The first frame the adapter sends a runtime for a session, on the intra-pod channel `CH-MSGSOCK` ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). |
| Session labels | The client-set `labels` map on a session creation request, used for querying, usage reports, and billing filters. [§14](../spec/14_workspace-plan-schema.md) states that labels are not used for internal routing. |
| Isolation profile and `minIsolationProfile` | A pool's sandboxing level, in increasing strength `standard`, `sandboxed`, and `microvm`, and the weakest profile a session accepts. |
| Drain gate | The create-time refusal with `503 POOL_DRAINING` when the selected pool is draining ([§15.1](../spec/15_external-api-surface.md#151-rest-api)). |
| PoolScalingController | The controller that sets each pool's warm pod count (`minWarm`) from its demand history ([§4.6.2](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration)). |
| Tenant view | The runtime lookup that returns only the runtimes a tenant may use: platform runtimes it has an access row for and its own derived runtimes. [`tenant-derived-runtimes-design.md`, "DR-4: Tenant runtime records"](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records) defines it and, under "Reading", requires a miss on the final row to refuse. |
| Pool access and owned pool | A platform pool serves a tenant through a `pool_tenant_access` row, and a tenant-derived runtime's owned pool serves only its owner. `requirePoolSelectable` refuses another tenant's pool as unknown ([`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md), DR-1 and "Pods" under DR-5). |
| Environment, E, and sharing path | A session's environment, written E, is fixed at create and decides which runtimes, pools, and egress rules apply to it. A sharing path is a two-sided declaration that lets a delegated child cross into a target environment T and run under T's policy ([`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md), TS-1 and TS-3). |
| `gateway.interceptorNamespaces` | A Helm value listing the namespaces that hold in-cluster interceptors. The chart renders a gateway egress rule and an interceptor certificate for each one. |
| NET-063 | The rule that the gateway-to-interceptor link uses mTLS with SPIFFE and DNS name checks ([§10.3](../spec/10_gateway-internals.md#103-mtls-pki)). SPIFFE is a standard for workload identities carried in certificates. |
| `lenny-egress` | The central egress proxy that [`egress-and-git-design.md`](egress-and-git-design.md) adds, through which pod traffic leaves the cluster. |
| `platform-admin` and `tenant-admin` | Lenny's administrative roles. A `platform-admin` administers the whole platform and every tenant. A `tenant-admin` administers one tenant. |
| `change-proposal` and `fidelity-review` | The repository's proposal pipeline, which writes a reviewed change proposal under `proposals/`, and the skill that checks a specification section against the code mechanism by mechanism. |
| F-n.n.n and T-n.n.n | Findings filed in `BUILD-GAPS.md` (implementation gaps against the specification) and `TEST-GAPS.md` (test coverage gaps against the specification). |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static checks, 1 unit, 2 component, 3 contract, 4 integration on the compose stack, 5 end to end on Kind (a local Kubernetes cluster), 7a local concurrency, 8 chaos, 9 security, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| DR-1, DR-4, DR-5, DR-8, DR-9, and DP11 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-1-to-dr-3-prerequisites) | DR-1 removes `tenant-admin` writes on platform-global runtimes and pools, makes session creation, discovery, and delegation read the access rows, and confines experiments to single-tenant pools. DR-4 defines the tenant view. DR-5 defines owned pools. DR-8 states the interactions, including the refusal of a delegated child's exports onto a target with no runtime supervisor. DR-9 defines default git clones and their `write` values. DP11 confines an experiment's pool effects to pools whose single tenant is the experiment's tenant. |
| TS-1 and TS-3 | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) | TS-1 scopes runtimes, pools, and egress rules to environments and re-checks them on the final row ("A session's environment"). TS-3 fixes a session's environment at create and defines sharing paths ("Cross-environment sharing"). |
| CA-4 and CA-7 | [`connector-authorization-and-external-memory-design.md`, "CA-4: Link check"](connector-authorization-and-external-memory-design.md#ca-4-link-check) and ["CA-7: Delegated children"](connector-authorization-and-external-memory-design.md#ca-7-delegated-children) | The connector link check at create ("Placement" under CA-4) and for a delegated child, each of which reads the final runtime's capability. |
| SD-1 and SD-P4 | [`session-derive-design.md`](session-derive-design.md) | SD-1 is the derive route and its shared create path, with its target checks under "Checks", "Stops", and "Supervisors". SD-P4 refuses a derive onto another runtime that has no runtime supervisor. Revision 12 refuses a derive when an interceptor rewrites its runtime and adds `evalRef` to derive. "RT-2" and "RT-1" reverse both. |
| RV-2 and RV-7 | [`runtime-revisions-design.md`](runtime-revisions-design.md) | Revision selection, which runs on the final runtime after the `PreRoute` chain and before the post-chain checks, and session options, whose validation against the chosen revision's schema runs at the post-chain point. |
| SO-4a | [`security-oversight-design.md`](security-oversight-design.md) | The mirror of a platform admin's actions on a tenant into that tenant's own audit chain. |

## High-level design

Lenny decides which runtime each session runs. Today an operator can steer that decision in two unrelated ways: through the experimentation subsystem, which has its own objects, storage, and integrations with flag services, or through an interceptor, a service the gateway calls before it picks the runtime. This design removes the experimentation subsystem, together with the storage of evaluation scores, and keeps the interceptor as the one way to steer runtime choice.

Lenny still ships a basic split for tenants who have no flag service. A route says that sessions asking for runtime `claude-worker` run on `claude-worker` 90% of the time and on `claude-worker-v2` 10% of the time, chosen by a stable hash of the user. The route is applied by a built-in interceptor that sees the same input and may make the same changes as any external one, so an operator can turn it off and replace it. Every routing decision, whoever made it, is recorded on the session as routing labels that the runtime reads to tag its traces and that billing and usage reports carry. Scoring and statistics stay in the eval and experimentation platforms deployers already use.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Removal (RT-1).** The experimentation subsystem, eval storage, and `evalRef` are deleted from the specification, the gateway, the controllers, the adapter, the SDKs, the schema, and the documentation.

**Interceptor contract (RT-2).** Every session creation path builds the same `PreRoute` payload, which gains the creation kind, the session, parent, and root IDs, the client labels, the pinned pool, and the routing labels. A `MODIFY` may change the runtime, the pool, the input, and the routing labels on every path, including delegation, which today ignores a runtime change, and derive, which runs no chain today and which [SD-1](session-derive-design.md) would refuse on a runtime change. After the whole chain the gateway picks a revision of the final runtime, then runs every check that depends on the runtime, the revision, or the pool once, at one post-chain point, including the checks other designs own, and names the interceptor that last changed the runtime when one fails. Every later interceptor phase receives the same session context, and every phase's payload is checked against §4.8.

**Routing labels (RT-3).** The session record gains a `routing` map that only `PreRoute` interceptors write. It replaces `experimentContext` on `session_start`, in delegation leases, and in billing events. A delegated child and a derived session start with the map of the session they come from.

**Runtime routes (RT-4).** A tenant-scoped admin resource maps a requested runtime to weighted targets. The route interceptor hashes the routing key to pick a target and writes the route and target names into the routing labels. Routes hold no pool settings, and each target pool scales from its own demand.

**In-cluster interceptors (RT-5).** Registration accepts only a Service name in a namespace listed in `gateway.interceptorNamespaces`. The chart supplies the gateway's mTLS material, and the gateway refuses plaintext outside dev mode and service-mesh mode.

**Tenant scope (RT-6).** An interceptor registration may list tenants, and the chain calls it only for requests of those tenants. A delegation policy cannot reference an interceptor scoped to another tenant.

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
 Revision selection on the final runtime (runtime-revisions-design.md)
        │
        ▼
 Post-chain point, on the final runtime, revision, and pool:
   tenant view and pool access, isolation, drain, environment rules,
   egress pool refusals, link checks, export refusal, runtimeOptions
   against the revision's schema, and derive checks
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

## Decisions

Settled by the owner on 2026-11-08:

| ID | Decision |
| :-- | :-- |
| D1 | Eval storage is removed. |
| D2 | Every feature specific to experimentation is removed, and Lenny keeps no experiment object. |
| D3 | Routes are built in, implemented as a `PreRoute` interceptor against the contract external interceptors follow, and can be turned off and replaced. |
| D4 | Interceptor registration stays with `platform-admin`. Tenant-written interceptors are out of scope. |
| D5 | Interceptors run in the cluster. A deployer reaches a remote service through an in-cluster interceptor or relay of their own. |
| D6 | Interceptor calls do not pass through `lenny-egress`. |
| D7 | An interceptor registration may be scoped to a list of tenants. |
| D8 | Routes match on the requested runtime alone. A deployer who wants rules on session labels writes an interceptor (formerly O1). |
| D9 | A delegated child starts with its parent's routing labels, and the child's chain may change any key (formerly O2). |
| D10 | A `tenant-admin` writes routes for their own tenant, and a `platform-admin` writes routes for any tenant (formerly O3). |
| D11 | `evalRef` is retired. A derive runs the `PreRoute` chain with `kind: derive`, the route interceptor treats it like any creation, and derive's own checks run after the chain (formerly O4). |
| D12 | This design adds no route that applies across tenants. Operator rollouts of a new version of one runtime belong to [`runtime-revisions-design.md`](runtime-revisions-design.md) (formerly O5). |
| D13 | The interceptor changes listed in "RT-2" and "RT-6" beyond the `PreRoute` payload are part of this design: session context on every phase, the payload audit, the last modifier, the scope check on `interceptorRef`, and routing reuse on retry and resume. |

Settled by the owner on 2026-11-09, during the review that compared every design with the others:

| ID | Decision |
| :-- | :-- |
| D14 | A delegated child that crosses into another environment through a sharing path keeps its parent's routing labels, as any delegated child does under D9 (review question Q8). |
| D15 | A tenant can see an interceptor that a `platform-admin` registers for it: [`security-oversight-design.md`](security-oversight-design.md) mirrors the registration to each listed tenant, lists it in the tenant's security posture, and states whether operator access approval applies to it (review question Q7). |

Open:

| ID | Question | Recommendation |
| :-- | :-- | :-- |
| D16 | The plaintext interceptor dial in "Current state" is filed as its own `BUILD-GAPS.md` finding, F-10.3.27, because it contradicts NET-063 today, independent of this design. | **Settled**, in the owner's words "render the chart and file it" (2026-11-09). A render with an interceptor namespace confirmed that the chart passes none of the three TLS flags. |

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

## Current state

**`ExperimentRouter` is outside the chain [source].** The specification lists the `ExperimentRouter` as a built-in `PreRoute` interceptor at priority 300. The code calls it directly (`routeExperiment` in `pkg/gateway/sessionserver/create.go`) and splits the external `PreRoute` chain into a segment below 300 and a segment from 300 up to place it. `create.go` and `create_and_start.go` each carry the split, and the delegation path calls `ApplyExperimentRouting` through an interface in `pkg/gateway/mcpfabric/delegation/service.go`.

**The route payloads are narrower than the specification [source]:**

| Path and phase | Payload type | Fields sent | `MODIFY` honoured |
| :-- | :-- | :-- | :-- |
| REST create and create-and-start, `PreRoute` | `routeTaskSpec` in `sessionserver/route.go` | `tenant_id`, `user_id`, `requested_runtime`, and `input` in the first segment. The second segment omits `input`. | Runtime and input |
| Delegation, `PreRoute` | `childRouteSpec`, declared in `mcptools/mcptools.go` and filled in `mcptools_register.go` | `tenant_id`, `requested_runtime`, and `input`. The type declares `user_id`, which the delegation path never sets. `metadata` carries only the tenant. | Input only. A runtime change is discarded. |
| REST create, `PostRoute` | `routeTaskSpec` | `tenant_id`, `user_id`, and `resolved_runtime_name`. The type has `credential_pool_id`, and `postRouteAfterSelection` leaves it empty. | Checked for immutable fields only |

[§4.8](../spec/04_system-components.md#48-gateway-policy-engine) says the `PreRoute` payload carries the requested runtime, workspace files, and delegation parameters, that an interceptor may change runtime hints on every path, and that the `PostRoute` payload carries runtime tags and the credential pool assignment. No path sends session labels, the pinned pool, or the session's ID: on the create path the request reaches the chain before the gateway mints the ID. `interceptor.Result` records the interceptor that rejected a request (`RejectedBy`) and no field records which one modified it.

**Derive [source].** `handleDerive` runs neither the `PreRoute` chain nor the `PostRoute` chain today (F-15.1.78). [SD-1](session-derive-design.md) adds both and refuses a derive whose runtime an interceptor rewrites.

**Checks after a rewrite [source].** After the late `PreRoute` segment changes the runtime, `preRouteAfterExperiment` re-resolves the isolation level, and `create.go` re-runs the drain gate. The fail-closed isolation check against `minIsolationProfile` lives in the `ExperimentRouter` (`VARIANT_ISOLATION_UNAVAILABLE`), and no equivalent was found for a runtime an external interceptor selects. "Validation owed" records the check.

**Re-checks in other designs [source].** Several designs each place their own checks after the experiment rewrite, at different call sites: TS-1 re-runs its environment checks on the final row ("A session's environment"), DR-4 refuses a tenant-view miss on the final row ("Reading"), CA-4 places its link check after `preRouteAfterExperiment` ("Placement"), CA-7 and DR-8 place theirs after `ApplyExperimentRouting`, and TS-3 re-checks a crossing on the final row. The "RT-2" post-chain point replaces those call sites with one.

**Interceptor transport [source]:**

- Registration records `endpoint`, `phases`, `priority`, `failPolicy`, and `timeout`. It has no TLS or credential field, and no tenant scope. The interceptor store treats interceptors as platform resources, and every `/v1/admin/interceptors` route sits behind `requireAdmin`, which requires `platform-admin` (`pkg/gateway/externalapi/admin/routes.go`, `tenants.go`).
- Transport security is gateway-wide. `dialInterceptor` in `cmd/lenny-gateway/policychain_seams.go` reads `--external-interceptor-tls-cert`, `--external-interceptor-tls-key`, and `--external-interceptor-ca`. The SPIFFE check of NET-063 applies only to a `.svc` host. Any other host gets CA and DNS name checks against the single CA bundle, so a public endpoint with a public certificate fails unless that bundle holds public roots.
- When all three flags are empty, `dialInterceptor` dials in plaintext, and no dev-mode check guards that branch. The chart renders `--interceptor-namespaces` and an interceptor certificate (`lenny-interceptor-tls`) and renders none of the three flags (`charts/lenny/templates/gateway-deployment.yaml`). A chart install therefore appears to dial interceptors in plaintext with no SPIFFE check, which NET-063 forbids. "Validation owed" records the confirmation, and O6 asks whether to file it now.
- §4.8 admits "an external host:port" endpoint, and §13.2 says such an interceptor uses the gateway's public HTTPS rule, which admits TCP 443 only.

**`evalRef` [source].** §15.1 defines `evalRef` on replay as a link "to an experiment or eval set", recorded in the new session's metadata and filterable on `GET /v1/sessions`. `replay.go` decodes it into its request struct, and no code stores it or filters on it. [`session-derive-design.md`](session-derive-design.md) moves it to derive.

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
| Gateway packages | `pkg/experiment`; `pkg/gateway/experiment/` (`evalstore`, `experimentprovider`, `experimentsticky`, `experimentstore`, and `ofrep`); `pkg/gateway/sessionserver/experimentrouter.go` and `eval.go`; `pkg/gateway/externalapi/admin/experiment_results.go` and the experiment handlers; `pkg/gateway/storage/erasure`; `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_experiment.go`; the `ExperimentContext` field in `sessionstore` and its readers in `usage.go`, `watchdog.go`, `podsession`, `adapterclient`, and `mcpfabric/delegation`. |
| Controllers | `pkg/controller/poolscaling/experimentsource.go`, `variants.go`, and the variant paths in `controller.go`. |
| Adapter and SDKs | `pkg/adapter` (`manifest.go`, `sessionframes.go`, `resume.go`, `session.go`, and `sdkwarm.go`); `schemas/lenny-adapter.proto` (`ExperimentContext`); the Go, Python, and TypeScript runtime SDKs and their examples; `cmd/lenny-compliance` fixtures. |
| Alerts | `pkg/alerting/rules`, `charts/lenny/files/alerting-rules.yaml`, and `docs/alerting/rules.yaml`. |
| Migrations | `0011_session_experiment_context`, `0025_tenant_experiment_targeting`, `0029_eval_results`, `0030_experiment_definitions`, `0098_eval_results_idempotency_key`, and `0156_eval_aggregates_matview`. |
| Documentation | `README.md`, `docs/index.md`, `docs/tutorials/evaluation-scoring.md`, `docs/operator-guide/openfeature-integration.md`, and the other pages under `docs/` that name experiments or eval, including `docs/reference/metrics.md` and `docs/reference/error-catalog.md`. |

## RT-1 Removal

Delete every item under "Removed" in "Scope" from the carriers in "Footprint".

- **Schema.** The tree keeps its migrations as they are, because the shared Kind database and its migration guard compare applied migrations by name and content. A new forward migration drops `eval_results`, `lenny_eval_aggregates`, `experiment_definitions`, `tenants.experiment_targeting`, and `sessions.experiment_context`, and adds the tables of "RT-3" and "RT-4". The platform has no deployments, so the migration carries no data.
- **Erasure.** The erasure order loses the sticky cache step and the `EvalResultStore` step. Routing labels live on the session row and are deleted with it.
- **Metrics.** `lenny_session_total`, `lenny_session_error_total`, and `lenny_session_duration_seconds` replace their `variant_id` label with `runtime`, whose values the registered runtimes bound. The experiment and eval metrics are deleted.
- **Error codes.** `RESERVED_IDENTIFIER` loses its variant example, and the proposal deletes it if no other user remains. `SESSION_NOT_EVAL_ELIGIBLE` and `EVAL_QUOTA_EXCEEDED` are deleted. `VARIANT_ISOLATION_UNAVAILABLE` becomes `ROUTE_ISOLATION_UNAVAILABLE` ("RT-2").
- **`evalRef`.** It leaves the replay request in §15.1 and leaves the derive request, the session row, and the session list filter that [`session-derive-design.md`](session-derive-design.md) stages. A client that groups sessions sets a session label, which every query API already filters on.
- **Build sequence.** §18 Phase 16 becomes "Runtime routes", and Phase 16.5 is deleted. `TESTING.md` §13.33 and §13.34 follow.

## RT-2 The interceptor contract

### `PreRoute` payload

One Go type in `pkg/gateway/policy/interceptor` serializes the payload for every creation path. Its fields:

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
| `labels` | No | The client's session labels. |
| `requested_runtime` | Yes | The runtime the session will run. For `kind: derive` it starts as the derive's `runtimeRef`, or the source session's resolved runtime when the request omits it. |
| `requested_pool` | Yes | The pinned pool, or empty. For `kind: derive` it starts as the derive's `targetPool`, or the source's client-named pool when the derive stays on the source's runtime and profile, and otherwise empty, as [SD-1](session-derive-design.md) states under "Checks", **Pool and profile.** |
| `routing` | Yes | The routing labels so far ("RT-3"). |
| `input` | Yes | The task input, as today. Empty for `kind: derive`. |
| `runtime_options` | No | The session's `runtimeOptions`: the create request's, the delegation `TaskSpec`'s for `kind: delegate`, and the derive request's or the source's for `kind: derive`. The post-chain point validates them, so no interceptor may change them ([`runtime-revisions-design.md`, "RV-7 Session options"](runtime-revisions-design.md#rv-7-session-options)). |

The immutable fields join the existing immutable field enforcement. `InterceptRequest.session_id` carries the same `session_id`, and `metadata` carries the session context below. The `PostRoute` payload carries `runtime_options` as an immutable field as well, so the options the post-chain point validates are the options the runtime receives.

### One chain

The gateway runs the `PreRoute` chain once, in ascending priority, with the route interceptor as an ordinary member at priority 300. The split segments, `preRouteBeforeExperiment`, `preRouteAfterExperiment`, and the experiment pivot are deleted from `create.go` and `create_and_start.go`. The delegation and derive paths honour `requested_runtime`, `requested_pool`, and `routing` from a `MODIFY`, as the create paths do.

A derive that omits `runtimeRef` asks for the source's resolved runtime. When Alice's source session asked for `claude-worker` and a route placed it on `claude-worker-v2`, her derive asks for `claude-worker-v2`, and a route keyed on `claude-worker` does not move it. A derive that names `claude-worker` is routed as a create is.

### Post-chain point

Session creation runs in this order on every path:

1. The `PreRoute` chain, including the route interceptor, produces the final runtime and pool.
2. Revision selection ([RV-2](runtime-revisions-design.md)) picks a revision of the final runtime.
3. The post-chain point runs every check that depends on the runtime, the revision, or the pool, on the chosen revision. A check that reads a field [`runtime-revisions-design.md`](runtime-revisions-design.md) versions, such as `capabilities.deferredToolDiscovery`, `deploymentModel`, or `type`, reads it from the chosen revision.
4. The pod claim.

The checks apply on every path and to every outcome, including a runtime the client requested and no interceptor changed. Each owning design states its checks. This design fixes where they run:

| Owner | Checks at the post-chain point |
| :-- | :-- |
| This design | The final pool's isolation profile satisfies the session's `minIsolationProfile`. The drain gate runs once, on the final pool. |
| [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-4-tenant-runtime-records) | The runtime is in the tenant view, and a miss answers as for an absent name ("Reading" under DR-4). The pool is selectable by the tenant through an access row or as its owned pool ("Pods" under DR-5). For `kind: delegate`, the child's exports are refused onto a target with no runtime supervisor ("Delegation" under DR-8). |
| [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md#ts-1-environment-scoping) | The environment rules on the final row that "A session's environment" under TS-1 lists: the selector for E, `ENVIRONMENT_REQUIRED`, the rule for pods with no per-session policy (`ENVIRONMENT_POLICY_NOT_ENFORCEABLE`), the refusal of `lockEgressToHosts` on a pool with `egressProfile: internet`, and the refusal of a `userCredential` binding on a pod that can serve another session. For `kind: delegate`, the rules start from the parent's E, a final runtime outside the parent's selector needs a live sharing path that sets the child's E to T, and the crossing re-check of "Cross-environment sharing" under TS-3 refuses a final row that leaves the path, changes T, or changes whether the hop crosses. |
| [`egress-and-git-design.md`](egress-and-git-design.md#effective-policy) | The refusal of a plan with a `gitClone` source on a pool with no git path ("Credentials"), and the refusal of `egress.allowedHosts` or `egressLockHosts` on a pod with concurrent sessions ("Effective policy"). The gateway's resolution of `gitClone` refs runs after these checks on every creation path, and before the pod claim where the plan arrives with the create request, because its gateway cause assertion names the final pool and the session's environment. At finalize, the pod was claimed at create, so a bad ref fails the finalize on a claimed pod ("Credentials", **The gateway runs no git.**, and "Effective policy", **Where the pool checks run**). |
| [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md#ca-4-link-check) | The connector link check at create ("Placement" under CA-4) and for a delegated child (CA-7), on the chosen revision's capabilities. |
| [`user-credential-delivery-design.md`](user-credential-delivery-design.md#flow) and [`user-git-identity-design.md`](user-git-identity-design.md#ug-2-repository-authorization) | The account link check (step 1 of "Flow") and the repository check of [`user-git-identity-design.md`](user-git-identity-design.md) ("When the check runs"). A missing link from either check, and a connector link that the check above finds missing at create, are reported together in the combined missing-links response, `403 LINKS_REQUIRED`, which CA-4 defines. |
| [`runtime-revisions-design.md`](runtime-revisions-design.md#rv-7-session-options) | For every creation kind, `create`, `delegate`, and `derive`, the session's `runtimeOptions` against the chosen revision's `runtimeOptionsSchema`, and on a derived runtime with its own schema against that schema as well, as separate validations, with absent options validated as an empty object. A refusal answers `400 RUNTIME_OPTIONS_INVALID` with `details.runtimeRevision`, and with `details.runtime` and `details.interceptor_ref` when the chain moved the session ("Validation" and "Refusal" under RV-7). For `kind: delegate`, the parent's `DelegationPolicy` may refuse the `TaskSpec`'s options. |
| [`session-derive-design.md`](session-derive-design.md#sd-1-one-route) | For `kind: derive`, the target checks of SD-1: the admission steps judged against the target runtime and pool, the plan checks, the setup command relabel and `setupCommandPolicy`, and the warm stores on the target ("Checks"); the content stop check ("Stops"); SD-P4's refusal of a target with no runtime supervisor ("Supervisors"); the equality of `executionMode`; and the isolation monotonicity rule unless the request allows a downgrade. |

A write-time or admission check that names a runtime, such as route admission under "RT-4", holds for every active revision of that runtime, because revision selection happens per session.

When the chain changed the runtime or pool and the result fails a check, the gateway refuses the creation with `422 ROUTE_ISOLATION_UNAVAILABLE` for the isolation check, or with the check's own code otherwise, and names the last modifier in `details.interceptor_ref`. It never falls back to the requested runtime. The checks replace the isolation monotonicity rules of §10.7, which compared variant pools with the base runtime's pool at experiment admission.

F-10.6.17, filed in `BUILD-GAPS.md`, records that a rewrite after §10.6 admission escapes the environment selector check. TS-3 owns the rules that close it, and this post-chain point is where they run.

### Last modifier

`interceptor.Result` gains `ModifiedBy`, the name of the interceptor whose `MODIFY` last changed the payload, set by `Chain.Run` and `Chain.RunRange`. Every phase records it in the audit row for the request, and a refusal at the post-chain point names it.

### Session context on every phase

After creation, every `InterceptRequest` on every phase carries the same session context in `metadata`: `tenant_id`, `user_id`, `session_id`, `root_session_id`, `runtime`, `pool`, and the routing labels as `routing.<key>` entries. Today only the route paths set `tenant_id` and `user_id`. An LLM, connector, delegation, or output interceptor can then apply rules per runtime or per routing label without a lookup.

### Payload audit

The proposal runs `fidelity-review` over the payload table of §4.8 for every phase and corrects each payload the code sends to the fields §4.8 states, or corrects §4.8 where the code is right. `PostRoute` is the known case: its payload omits the credential pool assignment and the runtime tags that §4.8 lists, and it gains the session context and the routing labels.

### Retry and resume

A retried or resumed session uses the runtime, pool, and routing labels stored at creation and does not run the `PreRoute` chain again, and its revision follows [`runtime-revisions-design.md`](runtime-revisions-design.md), so a route with `key: random` cannot move a resumed session. "Validation owed" records the confirmation against [§7.3](../spec/07_session-lifecycle.md#73-retry-and-resume).

## RT-3 Routing labels

The session record gains `routing`, a map from string to string.

**Population.**

1. The starting value is empty for `kind: create`, the parent's map for `kind: delegate` (D9), including a child that crosses into another environment through a sharing path (D14), and the source session's map for `kind: derive`.
2. Each `PreRoute` interceptor, in priority order, may return a changed map, and the next one sees the result. The route interceptor sets `route` and `target`. An external interceptor may set any keys, such as `flag` and `variant` taken from a flag service.
3. After the post-chain point passes, the gateway writes the map to the session record. It does not change after that, including on retry and resume.
4. A client request that sets `routing` is rejected with `400 VALIDATION_ERROR`. Client `labels` stay client-owned, so the §14 statement that labels are not used for internal routing remains true.

Keys and values follow the bounds §14 sets for `labels` ("Validation owed").

**Consumption.**

| Reader | Carrier | Use |
| :-- | :-- | :-- |
| Runtime | `session_start.routing` and the adapter manifest's per-session fields, in place of `experimentContext`. The runtime SDKs expose it as a map. | Tag traces and logs so the eval platform can compare targets. |
| Delegated child and derived session | The starting value of their own map | Attribution follows the tree and the derive. |
| API clients | The session response, and `GET /v1/sessions` filters such as `routing.target=claude-worker-v2` | Find the sessions of one test. |
| Usage and billing | Billing events and `GET /v1/usage` carry `routing` beside `labels`, in place of `experiment_id` and `variant_id`. | Cost per target. |
| Later interceptor phases | The session context ("RT-2") | Rules per test arm. |

The gateway makes no decision from `routing`. Placement and enforcement read only the session's runtime, revision, and pool.

Under D9 a child carries the labels of the routing decision that produced its tree, so a child that runs a different runtime can carry `routing.target` naming its parent's target. The session's runtime field records what the child runs. The delegation lease carries `routing` in place of `experimentContext`.

## RT-4 Runtime routes

### Resource

A route is a tenant-scoped admin resource:

```yaml
# POST /v1/admin/routes
name: claude-worker-canary
runtime: claude-worker        # the runtime name a session request asks for
key: user                     # user | session | random
targets:
  - runtime: claude-worker
    weight: 90
  - runtime: claude-worker-v2
    pool: claude-worker-v2-sandboxed-medium   # optional
    weight: 10
```

- A tenant has at most one route per `runtime`, so a request matches at most one route. A route matches on the requested runtime alone (D8).
- Targets name runtimes. A target that names a route, or a route whose `runtime` is another route's target within the same tenant, is refused, so resolution is one step.
- Weights are non-negative integers with a positive sum. A target's share is its weight divided by the sum. A weight of 0 stops new sessions on that target.
- Admission refuses a target outside the tenant view, a pool that does not belong to its target's runtime or that the tenant cannot select, and a target whose pools are all weaker than the requested runtime's default pool. The last rule moves the admission check of §10.7 to routes. Each rule holds for every active revision of the runtimes the route names.
- A route write that names a target runtime whose default clones carry `write` values answers with a `Warning` header naming those runtimes, because the route can add push access to the sessions it moves. [`tenant-derived-runtimes-design.md`, "Write scope"](tenant-derived-runtimes-design.md#write-scope) gives an experiment write the same warning today.
- Updates use `If-Match`. A route has no status. Deleting it stops routing at once.
- A `tenant-admin` writes routes for their own tenant, and a `platform-admin` writes routes for any tenant (D10). A route calls no external service and every target must be in the tenant view, so a `tenant-admin` route reaches only runtimes the tenant could already request. Each write emits the admin audit record ("Validation owed").

### Assignment

For a session whose `requested_runtime` matches a route, the route interceptor:

1. Picks the routing key: the user ID under `key: user`, the root session ID under `key: session` or for an anonymous session under `key: user`, and a random UUID under `key: random`.
2. Computes `HMAC-SHA256(key = tenant_id + "/" + route name, message = routing key)`, maps the first 8 bytes to a bucket in [0, 1), and walks the targets in order by cumulative share. This is the §10.7 bucketing algorithm with integer weights and no control remainder. [`runtime-revisions-design.md`](runtime-revisions-design.md) uses the same function, so the function lives in a shared package.
3. Returns a `MODIFY` that sets `requested_runtime`, sets `requested_pool` when the target names one, and sets `routing.route` and `routing.target` to the route and target runtime names.

Determinism replaces the sticky cache: a user under `key: user` reaches the same target in every session while the route is unchanged, and every session in a delegation tree under `key: session` reaches the same target. A derived session is its own root, so under `key: session` it is placed independently of its source. Changing weights or inserting a target moves the boundaries and reassigns some keys. Appending a target at the end of the list moves the fewest keys. Renaming a route reassigns every key.

### Pools

Routes write no pool configuration. Each target's pool scales from its own demand history through the standard formula of §4.6.2. An operator who adds a target sets that pool's `minWarm` for its first sessions, as for any new pool. DP11 has no equivalent, because a route has no pool effects.

### Turning routes off

The Helm value `gateway.routes.enabled`, default `true`, controls the route interceptor. When it is `false`, the gateway registers no route interceptor and serves no `/v1/admin/routes` endpoint, and the stored routes stay in the database. An operator who replaces routes registers an external `PreRoute` interceptor at any priority above 100.

### Contract tests

One contract test suite at tier 3 drives the `PreRoute` payload and `MODIFY` rules. It runs against the route interceptor in process and against a stub external interceptor over gRPC, so the two cannot drift.

## RT-5 In-cluster interceptors

- **Endpoint.** Registration accepts only `<service>.<namespace>.svc` or `<service>.<namespace>.svc.<cluster domain>`, with a port, where the namespace is listed in `gateway.interceptorNamespaces`. Any other host, including an IP address, is refused with `400 INTERCEPTOR_ENDPOINT_NOT_IN_CLUSTER`. A deployer reaches a remote service through an in-cluster interceptor of their own, which holds its own credentials under its own namespace's egress policy.
- **Transport.** The chart passes the gateway's mTLS leaf and the cluster CA to `--external-interceptor-tls-cert`, `--external-interceptor-tls-key`, and `--external-interceptor-ca`. The gateway dials plaintext only in dev mode or when `mtls.enabled` is `false` because a service mesh provides transport security ("Validation owed"). In any other mode it refuses to start while an interceptor is registered and the material is absent.
- **Network.** §13.2 drops the sentence admitting interceptors through the public HTTPS rule. The interceptor rule of NET-039 is the only gateway egress to an interceptor.
- **`lenny-egress`.** Interceptor calls do not pass through it (D6). Its tunnels carry TLS end to end and inject credentials only toward an upstream proxy, so it would add a dependency to every session creation without supplying trust or credentials.
- **Every phase.** The rule applies to every registration, whatever its phases.

## RT-6 Tenant-scoped interceptors

- A registration gains `tenants`, a list of tenant IDs. An empty or absent list calls the interceptor for every tenant, as today.
- The chain skips a scoped interceptor for a request of any other tenant, on every phase. The filter reads `Request.TenantID`, which every phase already carries.
- Only a `platform-admin` writes registrations (D4). Admission refuses an unknown tenant ID.
- **Policy references.** A `DelegationPolicy` whose `contentPolicy.interceptorRef` names a scoped interceptor applies it only to sessions of the listed tenants. A tenant's own policy that names an interceptor scoped to other tenants is refused at write time. A change to a registration's `tenants` that would leave such a reference re-validates the referencing policies and is refused with their names.
- **Ordering.** The operator guide directs a `platform-admin` to register tenant-scoped interceptors at priorities below the platform's policy interceptors in the same phase, because §4.8 does not re-check a `MODIFY` from an interceptor that runs after `GuardrailsInterceptor` or another policy interceptor.
- Fail-open escalation state is already per interceptor, so one tenant's failing interceptor does not change another tenant's chain.
- **Visibility to the tenant (D15).** A registration that lists tenants is a `platform-admin` write to a resource of each listed tenant. [`security-oversight-design.md`](security-oversight-design.md) mirrors its create, update, and delete to each listed tenant's audit chain under SO-4a, lists the tenant's scoped interceptors in its security posture with their phases, priority, `failPolicy`, and Service name, and states whether its operator access approval covers the registration.
- A SaaS provider either registers one scoped interceptor per customer or runs one unscoped dispatcher that forwards by `tenant_id`. Both use the in-cluster rule of "RT-5".

## RT-7 Documentation

The operator guide page `docs/operator-guide/openfeature-integration.md` is replaced by `docs/operator-guide/runtime-routing.md`, which states:

- How to split sessions across runtimes with a route, change weights, and stop it.
- How to run an A/B comparison: the runtime tags traces with `routing`, scores go to the deployer's eval platform, and cost per target comes from usage reports filtered by `routing`.
- How to delegate assignment to a flag service: an in-cluster `PreRoute` interceptor calls the service, sets `requested_runtime`, and writes its own routing keys. The page states the payload and `MODIFY` rules of "RT-2".
- How a SaaS provider attaches customer logic through "RT-6", and the ordering guidance.
- Where to look for rolling out a new version of one runtime: the page links the revisions guide that [`runtime-revisions-design.md`](runtime-revisions-design.md) adds.

`docs/tutorials/evaluation-scoring.md` is deleted, and the runtime author guide replaces `experimentContext` with `routing`.

## Limits

- **No scores or experiment state in Lenny.** A deployer without an eval platform has no built-in way to compare targets beyond the session metrics by `runtime` and the usage reports by `routing`.
- **One route per runtime.** Two concurrent tests on one runtime share a route with several targets, or use an interceptor. The first-match rule that kept tests mutually exclusive is gone.
- **Reassignment on change.** Weight changes reassign some keys.
- **Cold start on a new target.** The operator sets the new pool's `minWarm`, because routes do not size pools.
- **The requested runtime is a hint.** A client that asks for `claude-worker` may run `claude-worker-v2`. The session response names the resolved runtime and the routing labels.
- **A routed derive can change runtime.** A derive that names a routed runtime can run on another target, subject to derive's checks. A client that wants a specific runtime names a runtime no route matches.
- **A relay for remote services.** A SaaS customer's externally hosted logic needs an in-cluster relay that the provider operates.
- **Later interceptors win.** An external interceptor above priority 300 can override a route. The last modifier in the audit row makes that visible.

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

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| [§10.7 Experiment Primitives](../spec/10_gateway-internals.md#107-experiment-primitives) | Replace the section with "10.7 Runtime routing", stating "RT-3" and "RT-4". The heading changes, so the proposal adds an anchor redirect for `#107-experiment-primitives`. |
| [§4.6.1](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle), "Re-activation grace period" | Remove the variant pool example. |
| [§4.6.2](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration) | Remove the experiment sizing role, the variant pool formula, the base pool adjustment, and the experiment teardown pointer. |
| [§4.7.6](../spec/04_system-components.md#476-adapter-manifest-field-reference), "Per-session fields" | Replace `experimentContext` with `routing`. |
| [§4.8](../spec/04_system-components.md#48-gateway-policy-engine), built-in table, phase payload table, and field dependencies | Replace `ExperimentRouter` with the route interceptor. State the "RT-2" payload, `kind`, `MODIFY` rights, `runtime_options` as immutable in the `PreRoute` and `PostRoute` payloads, single chain, post-chain checks, last modifier, session context, and the corrected payload of each phase. State the "RT-5" endpoint rule and the "RT-6" tenant scope and ordering. Remove "or an external host:port". |
| [§5.2](../spec/05_runtime-registry-and-pool-model.md), "Execution Mode Scaling Implications" | Remove the variant pool paragraph. |
| [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow), step 3a | Describe the `PreRoute` chain, revision selection, the post-chain point, and the pod claim, in that order. |
| [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume) | State that a retry or resume keeps the stored runtime, pool, and routing labels. |
| [§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism) step 2b and [§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) "Delegation Lease" | State that the child's chain honours runtime, pool, and routing changes. Replace `experimentContext` with `routing`. State the tenant scope rule for `contentPolicy.interceptorRef`. |
| [§10.3](../spec/10_gateway-internals.md#103-mtls-pki), NET-063 | State that every interceptor is in-cluster, and the dev-mode and service-mesh exceptions for plaintext. |
| [§11.2.1 Billing Event Stream](../spec/11_policy-and-controls.md#1121-billing-event-stream) | Replace `experiment_id` and `variant_id` with `routing`. |
| [§12.2](../spec/12_storage-architecture.md#122-storage-roles), [§12.4](../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes), [§12.6](../spec/12_storage-architecture.md#126-interface-design), and [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces) | Remove `EvalResultStore`, the sticky cache keys and their failure row, and their erasure steps. Add the route store. |
| [§13.2](../spec/13_security-model.md#132-network-isolation), gateway egress row and the NET-039 note | Remove the external interceptor sentence. |
| [§14](../spec/14_workspace-plan-schema.md), `CreateSessionRequest` fields | State that `routing` is gateway-owned and refused on requests. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | Remove the eval and experiment endpoints, their dry-run and deletion rules, the eval error codes, and `evalRef`. Add `/v1/admin/routes`, `routing` on session responses and filters, `ROUTE_ISOLATION_UNAVAILABLE`, `INTERCEPTOR_ENDPOINT_NOT_IN_CLUSTER`, and the `tenants` registration field. If [`session-derive-design.md`](session-derive-design.md) lands first, its derive row loses `evalRef` here. |
| [§15.7 Runtime Author SDKs](../spec/15_external-api-surface.md#157-runtime-author-sdks) | Replace `ExperimentContext` with `Routing`. |
| [§16.1](../spec/16_observability.md#161-metrics), [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos), [§16.6](../spec/16_observability.md#166-operational-events-catalog), and [§16.7](../spec/16_observability.md#167-section-25-audit-events) | Remove the experiment and eval metrics, alerts, and events. Replace `variant_id` with `runtime` on the session metrics. Remove the experiment-targeting meaning of the `provider` label. Add the last modifier to the interceptor audit records. |
| [§18](../spec/18_build-sequence.md) phase map, Phase 16, and Phase 16.5 | Rename Phase 16 to runtime routes and delete Phase 16.5. |
| [§23](../spec/23_competitive-landscape.md#additional-platform-capabilities) matrix rows and capability entries | Replace the experimentation and eval hook rows and entries with runtime routing through `PreRoute`. |
| [§28.5.3 Intra-pod](../spec/28_communication-channels.md#2853-intra-pod) | Replace `experimentContext` on `session_start` with `routing`. The proposal's verifier confirms whether §28 registers the gateway-to-interceptor link and whether its row needs the in-cluster rule. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| Removed packages and files | Every gateway, controller, adapter, SDK, and alert location in "Footprint". |
| `pkg/gateway/policy/interceptor` | The shared `PreRoute` payload type, immutable field paths for its new fields, `ModifiedBy` on `Result`, the session context builder, and the tenant filter in `Chain`. |
| `pkg/gateway/policy/interceptor/interceptorstore` and `admin/interceptors.go` | The `tenants` field, the in-cluster endpoint check, admission of tenant IDs, and re-validation of referencing delegation policies. |
| `pkg/gateway/routing` (new) | The route interceptor, `routestore` with its Postgres store, and the shared bucketing function. |
| `pkg/gateway/externalapi/admin` | The `/v1/admin/routes` handlers and the gate of D10. The delegation policy handlers check `interceptorRef` scope. |
| `pkg/gateway/sessionserver` | One chain run, the minted session ID before the chain, the post-chain point with the checks every owning design places there, `routing` on the record, request refusal of `routing`, `routing` on usage, and the derive path through the shared create path. |
| `pkg/gateway/mcpfabric/mcptools` | The shared payload, honoured runtime, pool, and routing changes, and the parent's routing for the child. |
| Every interceptor call site | The session context in `metadata`, and the corrected payloads the audit finds. |
| `pkg/gateway/runtime/watchdog` | `routing` on billing events. |
| `pkg/adapter`, `schemas/lenny-adapter.proto`, and the runtime SDKs | `routing` in place of `ExperimentContext`. Regenerate the proto code. |
| `migrations` | The forward migration of "RT-1". |
| `cmd/lenny-gateway` | Route interceptor wiring, the `gateway.routes.enabled` flag, and the transport rule of "RT-5". |
| `charts/lenny` | The interceptor TLS flags, `gateway.routes.enabled`, and removal of experiment values. Regenerate `pkg/embedded/manifests/manifests.yaml` if it changes. |
| `docs/` and `README.md` | "RT-7", and removal of every experiment, eval, OpenFeature, and vendor reference. |
| `TESTING.md`, `tests/spec-map.json`, `tests/change-graph.json`, `tests/registers/change-graph-coverage.yaml`, and `pkg/gateway/ARCHITECTURE.md` | Remove the experiment and eval entries and add the routing ones. |
| `BUILD-GAPS.md` and `TEST-GAPS.md` | The proposal records which §10.7 findings its removal retires, and leaves closed findings' text intact. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 0 | A static check fails if a feature-flag vendor SDK or `open-feature/go-sdk` enters the gateway's dependencies, or if `experimentContext`, `EvalResult`, or `evalRef` reappears in the schema or the code (RTR-1). |
| 1 | Bucketing shares within tolerance over many keys, stability per key, appended-target movement, anonymous `key: user`, and `key: random` (RTR-4). Route admission rules. Immutable field enforcement on each new payload field, including `kind` and `runtime_options`, in the `PreRoute` and `PostRoute` payloads. `ModifiedBy` across several `MODIFY` responses. The tenant filter on every phase (RTR-9). The endpoint check (RTR-8). |
| 3 | The shared contract suite against the route interceptor and a stub external interceptor (RTR-5). The `session_start` frame with `routing`, and the proto round trip. The session context and the corrected payload on each phase (RTR-10). |
| 4 | On the compose stack, through each creation kind: a route moves a session, an external interceptor at priority 150 changes the requested runtime before the route matches, one at priority 400 overrides the route, a delegated child honours a runtime change and inherits `routing`, and a derive that names a routed runtime is routed while one that omits `runtimeRef` stays on the source's runtime (RTR-2). Post-chain refusals with `ROUTE_ISOLATION_UNAVAILABLE` and with derive's execution-mode check, each naming the last modifier, and the drain gate on a routed pool (RTR-3). A route that moves a session onto a runtime or pool that fails one owning design's check is refused with that design's code, one case for each row of the post-chain table, and a check that reads a versioned field reads the chosen revision. A route that moves a session onto a runtime whose schema refuses its options is refused with `RUNTIME_OPTIONS_INVALID` naming the runtime, the revision, and the last modifier, on each creation kind, and a `PreRoute` or `PostRoute` `MODIFY` that changes `runtime_options` is refused with `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`. A delegated child that crosses environments keeps its parent's routing labels (D14). `routing` on the session response, the list filter, usage, and billing events, and refusal of a client-set `routing` (RTR-7). A resumed session under a `key: random` route keeps its runtime and labels (RTR-11). With `gateway.routes.enabled: false`, every other creation test passes unchanged (RTR-6). |
| 5 | On Kind: an in-cluster interceptor is reached over mTLS with the SPIFFE check, and a registration naming an outside host is refused (RTR-8). |
| 7a | Concurrent creates across tenants with scoped interceptors stay isolated under `-race`, and the route store's cache stays consistent during weight updates. |
| 8 | A `fail-closed` scoped interceptor that is killed rejects only its tenant's creates, and recovery restores them. |
| 9 | A `tenant-admin` cannot write a route for another tenant or with a target outside its tenant view. A tenant-scoped interceptor never receives another tenant's payload, and a tenant's delegation policy cannot reference another tenant's scoped interceptor. The gateway refuses to start with registered interceptors and no TLS material outside dev mode and service-mesh mode. |
| 11 | Every route example in the documentation passes admission, and no documentation page names the removed endpoints, fields, metrics, or alerts. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Run Kind to confirm that the gateway dials interceptors in plaintext | "Current state" infers it from the template and the dial code. | The chart render is done (2026-11-09): with `gateway.interceptorNamespaces` set, the gateway gets `--dev-mode=false` and none of the three TLS flags. Filed as F-10.3.27. The Kind run remains open. |
| Confirm the isolation check the gateway applies to a directly requested runtime, and whether any check covers an external interceptor's rewrite | The post-chain checks of "RT-2" reuse the direct check. | Open. |
| Confirm that retry and resume read the stored runtime and pool and run no `PreRoute` chain | "RT-2" and RTR-11 rely on it. | Open. |
| Confirm that each check in the post-chain table reads only the final row and can move to one call site | "RT-2" moves checks that other designs place at separate call sites. | Open, for the proposal. |
| Confirm the bounds §14 places on `labels` | "RT-3" reuses them for `routing`. If none exist, the proposal sets bounds for both. | Open. |
| Confirm that admin writes emit a generic audit record | Routes rely on it in place of `experiment.status_changed`. | Open. |
| Confirm the interceptor transport under `mtls.enabled: false` with a service mesh | "RT-5" admits plaintext to a mesh sidecar. | Open. |
| Confirm that the shared Kind database's migration guard accepts the forward drop migration | "RT-1" relies on it. | Open, for the proposal. |
| Confirm the §28 link row for the gateway-to-interceptor connection | "Specification changes" assumes the verifier finds or adds it. | Open, for the proposal's verifier. |

## Instructions for reviewers of other designs

These designs refer to the removed subsystem, the `PreRoute` chain, derive, the checks after a rewrite, or interceptor registration:

- [`session-derive-design.md`](session-derive-design.md) defines "Experiment" and `evalRef`, moves `evalRef` to derive, refuses a derive whose runtime an interceptor rewrites ("Rewrites" under "Checks"), and leaves `routeExperiment` out of the shared row build. Its reviewer removes the "Experiment" term, `evalRef` from the derive request, the session row, the list filter, and the §15.1 edits, and the `routeExperiment` mentions. It replaces the rewrite refusal with D11: a derive runs the `PreRoute` chain with `kind: derive`, starts its routing labels from the source's, starts `requested_pool` as "RT-2" states, and meets its "Checks", "Stops", and "Supervisors" at the post-chain point on the chosen revision.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md), "Fields", lists `experiment_id` and `variant_id` in the common envelope of `session.pod_usage`. Its reviewer replaces them with `routing`, and with `runtime_revision` from [`runtime-revisions-design.md`](runtime-revisions-design.md).
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md) defines "Experiment routing", places the CA-4 check after `routeExperiment` and `preRouteAfterExperiment` ("Placement"), and places the CA-7 check after `ApplyExperimentRouting`. Its reviewer removes the term and states both checks at the post-chain point of "RT-2", reading the chosen revision's capabilities.
- [`user-credential-delivery-design.md`](user-credential-delivery-design.md) refuses a missing account link before any pod claim (step 1 of "Flow") and a `userCredential` binding on a pod that can serve another session. Its reviewer states that both run at the post-chain point, on the final runtime and pool.
- [`user-git-identity-design.md`](user-git-identity-design.md), "When the check runs", refuses a missing repository link before any pod claim. Its reviewer states that the check runs at the post-chain point.
- [`egress-and-git-design.md`](egress-and-git-design.md) refuses a `gitClone` plan on a pool with no git path ("Git mirror") and a session list of hosts on a pod with concurrent sessions ("Effective policy"). Its reviewer states that both refusals read the final pool at the post-chain point.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) refers to experiments in several places. Its reviewer:
  - removes the "Experiment and variant" term, DP11, DR-1's experiment rules and DF-9's experiment path, `checkVariantIsolation` under "Reading", the `initialMinWarm` refusal under DR-5, the "Experiments" subsection of DR-8, decision 13, and the experiment tests, including those of `ExperimentVariantSource`;
  - restates the tenant-view miss under "Reading" and the export refusal under "Delegation" of DR-8 as checks at the post-chain point;
  - replaces the experiment bullet under "Write scope" with the route write `Warning` of "RT-4", and the experiment sentence under "Binding" with a route target;
  - checks that route targets read the tenant view and pool access, as "RT-4" requires.
- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) refers to experiment routing in its terms, its checks, a scenario, and a finding. Its reviewer:
  - replaces the "Experiment routing and `PreRoute`" term with the `PreRoute` chain and the post-chain point;
  - restates the final-row checks of "A session's environment" under TS-1 and the crossing re-check of "Cross-environment sharing" under TS-3 as checks at the post-chain point, with `environment` in the `PreRoute` payload being the parent's for `kind: delegate`;
  - adds `routing` to what moves across under **Model.**, per D14;
  - rewrites the "An experiment variant" scenario as a route, the §10.7 row of its specification list, and the experiment wording of its tests;
  - records F-10.6.17 as closed by TS-3's rules at the post-chain point of "RT-2".
  "SSRF and NetworkPolicy" needs no change, because "RT-5" removes interceptors from the external HTTPS path and adds no rule.
- [`security-oversight-design.md`](security-oversight-design.md) does not cover tenant-scoped interceptors. Its reviewer applies D15: SO-4a mirrors a registration's create, update, and delete to each listed tenant as `kind: admin_write`, "Posture" gains a section that lists the tenant's scoped interceptors, and the design states whether decision 15's approval gate covers a registration.

## Proposal split and order

Two proposals, in order:

1. **Interceptor contract and scope.** "RT-2" without the route interceptor, "RT-5", and "RT-6". It stands alone: it corrects the delegation path and the phase payloads, adds the session context and the last modifier, closes the plaintext dial, and gives SaaS providers tenant scope while the `ExperimentRouter` still exists. If [`session-derive-design.md`](session-derive-design.md) lands first, this proposal adds `kind: derive` to its create path. Otherwise the derive proposal adopts the payload this one lands.
2. **Runtime routes and removal.** "RT-1", "RT-3", "RT-4", and "RT-7". It depends on the first proposal's payload and single chain. [`runtime-revisions-design.md`](runtime-revisions-design.md) depends on the second proposal's shared bucketing function and lands after it.

Within the second proposal, the specification edits land first, then routes and routing labels with their tests, then the removal and the migration, then the documentation and records.

## Review history

This section records how the design reached its current form. No independent review has run yet.

### Revision notes

- Revision 2, dated 2026-11-03, is the first revision in the repository's history. It records the owner's choice of contract documentation for deployers on vendors without an OFREP server.
- Revision 3, dated 2026-11-07, followed a pass for a reader new to the project. It added "Terms", "Terms from other designs and the platform", "Labels from other designs", "High-level design", and the evidence sources under the tag table, named the object and the field behind each configuration value, and linked the specification sections and the other designs the text cites. It changed no rule.
- Revision 4, dated 2026-11-08, corrected the "Current state" row that cited T-10.7.9 for the missing vendor SDK test, which T-10.7.9 does not record, and the "Gateway" term, which described one gateway process where the gateway runs as several replicas, that a validation pass found, and changed no rule.
- Revision 5, dated 2026-11-08, follows an owner discussion that settled D1 to D7. It widens the design from removing the vendor SDK providers to removing the experimentation subsystem and eval storage, replaces external targeting with the `PreRoute` interceptor contract, adds runtime routes and routing labels, restricts interceptors to the cluster, and adds tenant-scoped registration. The file moved from `experiment-provider-removal-design.md` to `runtime-routing-design.md`, and the labels changed from AB-n and ABR-n to RT-n and RTR-n. Revision 4's OFREP corrections (AB-2 to AB-6), its vendor coverage table, and its sources are retired because the OFREP client is removed. Its defect list survives in condensed form under "Current state".
- Revision 6, dated 2026-11-08, records the owner's settlement of O1 to O5 as D8 to D12 and of the interceptor changes as D13. It adds the creation kind, routes derives through the chain with derive's checks after it, retires `evalRef`, adds the session context on every phase, the payload audit, the last modifier, the scope rule for `interceptorRef`, the ordering guidance, and routing reuse on retry and resume, and moves operator rollouts of one runtime to [`runtime-revisions-design.md`](runtime-revisions-design.md).
- Revision 7, dated 2026-11-09, applies the review that compared every design with the others, and the owner's answers to its questions Q7 and Q8 as D14 and D15. It defines one post-chain point after the `PreRoute` chain and revision selection, lists the checks each owning design runs there, and reads versioned fields from the chosen revision. It corrects the environment check, which refused every child that crossed through a sharing path, states the derive's starting `requested_pool`, adds the route write `Warning`, records F-10.6.17's ownership, corrects the location of `childRouteSpec`, the derive gloss, and the tenant-view citation, and widens "Instructions for reviewers of other designs" to every design that places a check after a rewrite.
- Revision 8, dated 2026-11-09, applies the owner's decision N1 of that date, settled in the owner's word "ok". The post-chain table row for the account link check and the repository check names the combined missing-links response by its code, `403 LINKS_REQUIRED`, which CA-4 now uses whichever kinds of link are missing, in place of `USER_LINK_REQUIRED`. The design's own rules are unchanged.
- Revision 9, dated 2026-11-09, applies the owner's decisions of that date on mirror fill causes and ref resolution, given in the owner's words "Adversarially review your recommendations and, if they pass, go with them". The egress row of the post-chain table states that the gateway's `gitClone` ref resolution runs after the post-chain checks on every creation path, and before the pod claim where the plan arrives with the create request, while at finalize a bad ref fails on the pod claimed at create, as `egress-and-git-design.md` revision 74 requires, and the row's citation of the git path refusal names "Credentials", where that refusal is stated, in place of "Git mirror". The design's own rules are unchanged.
- Revision 10, dated 2026-11-09, records O6 as settled decision D16. A chart render with an interceptor namespace confirmed the plaintext dial, which `BUILD-GAPS.md` now carries as F-10.3.27.
- Revision 11, dated 2026-11-09, applies decisions S6 and Rule 1 of the owner's settlement of O2 in `runtime-revisions-design.md`, given in the owner's words "Go with this", and changes these rules. The `PreRoute` payload gains `runtime_options`, which no interceptor may modify, and the `PostRoute` payload carries it as immutable too, so the validated options are the options that run. The post-chain table gains a `runtimeOptions` row for every creation kind, owned by `runtime-revisions-design.md`, in place of the derive-only mention in the `session-derive-design.md` row. "Specification changes" and the tier 1 and tier 4 tests follow.
