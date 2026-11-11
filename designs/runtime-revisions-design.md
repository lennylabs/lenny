# Runtime revisions

Status: design for owner review, revision 10, 2026-11-10. A cross-design sync review ran on 2026-11-08, and revision 3 applied its findings. Nothing here is built or staged. It reads against [`runtime-routing-design.md`](runtime-routing-design.md) revision 12, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 65, [`session-derive-design.md`](session-derive-design.md) revision 16, [`egress-and-git-design.md`](egress-and-git-design.md) revision 74, [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) revision 30, [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) revision 12, and [`message-delivery-design.md`](message-delivery-design.md) revision 8. "Decisions" lists what the owner has settled and what remains open. It stages no specification edit. The changes go through `change-proposal` after review. Proposals carry the label RV-n, and requirements carry the label RVR-n. "Review history" at the end records the revisions.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree or the specification on 2026-11-08, or on 2026-11-09 for the claims revisions 6 and 7 added. |
| **[design]** | What this design requires. Nothing implements it. |
| **[estimate]** | A planning figure computed from stated assumptions and approximate AWS list prices. Nothing measured it. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

An operator who ships a new version of a runtime wants clients to keep asking for the same runtime name while new sessions move to the new version gradually, with the option to stop and go back. Lenny has no mechanism that does this for the whole runtime:

- [§10.5](../spec/10_gateway-internals.md#105-upgrade-and-rollback-strategy) defines a `RuntimeUpgrade` state machine with a canary split, but it changes only a pool's image, and the session creation path never reads it ("Current state").
- Every other field of a runtime, such as its command, environment, or capabilities, changes in place. The change reaches every pool of the runtime at once, and under [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) every tenant's derived runtime as well.
- Versioning by name, as `claude-worker-v2` beside `claude-worker`, is not transparent. Clients must change the name they ask for, every tenant needs a new access row, and every derived runtime still names the old base in `baseRuntime`.

This design adds revisions to runtimes. A revision is an immutable copy of the fields that define what runs in a runtime's pods. The runtime keeps its name, holds a weight per active revision, and the gateway places each new session on a revision by those weights. A rollout moves the weights and retires the old revision's pods.

## Scope

**Added:**

- Immutable runtime revisions for the fields that define a runtime's pods ("RV-1").
- Revision selection at session creation, and `runtimeRevision` on the session record, `session_start`, billing events, and session metrics ("RV-2").
- Pod sets per pool, keyed on the hash of a revision's pod-affecting fields together with the derived runtime's and the pool's pod-affecting contributions, with the warm count split by weight ("RV-3").
- A rollout procedure at the runtime level that replaces the pool-level `RuntimeUpgrade` ("RV-4").
- Base revisions for derived runtimes ("RV-5").
- Revision rules for resume, retry, delegation, and derive ("RV-6").
- Session options on revisions: an inline, versioned `runtimeOptionsSchema`, validation of a session's `runtimeOptions` at the post-chain point against the chosen revision, and their delivery in `session_start` ("RV-7").
- Discovery of the revision a caller would be assigned ("Discovery" under "RV-2").

**Changed:**

- The pod builder reads the image, command, and environment from the revision rather than from the live `Runtime` resource.
- The `Runtime` resource's `runtimeOptionsSchemaRef` becomes an inline `runtimeOptionsSchema`, as the admin API carries it, and the built-in reference runtimes' schemas become files in the repository.
- §5.1's override-replace merge of `runtimeOptionsSchema` and its rule that a derived schema names only the base's properties give way to validation against the base revision's schema and the derived runtime's schema separately.
- The `runtimeOptions` field of the pod-scoped adapter manifest moves to `session_start`.
- §5.1's statement that `image` is immutable through the API. A change to `image` creates a revision.
- `type` and `executionMode`, which §5.1 lets an API write change today, become fixed. A different value needs a new runtime.
- Checks at session creation that read a versioned field read it from the chosen revision, at the post-chain point of [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract).
- The warm list of a pool, its hosts, and the readable set of a pod before binding are kept per pool and revision.
- A runtime's `isolationProfile` becomes a live floor that pool admission and the WarmPoolController enforce ("Isolation floor" under "RV-1").
- The agent card is generated on read from the selected revision's `agentInterface`, in place of §5.1's card generated at write ("Discovery" under "RV-2").
- The per-tenant capability override loses `capabilities.preConnect`, may only narrow `capabilities.interaction` and `capabilities.injection`, and may only append to `sdkWarmBlockingPaths` ("Per-tenant capability override" under "RV-1").
- The integration-level check keys its cache on the revision's binary and declared level ("Integration-level check" under "RV-2").

**Kept:**

- The runtime name, access rows, the tenant view, `baseRuntime` references, and pools as the unit an operator configures.
- Runtime routes and the `PreRoute` chain of [`runtime-routing-design.md`](runtime-routing-design.md), which choose a runtime before this design chooses a revision.

**Out of scope:**

- Choosing a revision by name in a request (D2).
- Statistical comparison of revisions. A runtime tags its traces with `runtimeRevision`, and the comparison happens in the deployer's eval platform.

## Terms

| Term | Meaning |
| :-- | :-- |
| Revision | An immutable, numbered copy of a runtime's versioned fields, written `claude-worker` revision 7. Revision numbers increase by one per runtime. |
| Versioned fields | The runtime fields that define what runs in a pod and what the binary in it can do. A change to any of them creates a revision. "RV-1" lists them and divides them into pod-affecting and gateway-read fields. |
| Pod-affecting fields | The versioned fields that shape the pod, its template, or its warm state before bind. |
| Gateway-read fields | The versioned fields that reach a session only through the gateway, which reads them, turns them into session data, or delivers them to the adapter per session at bind, such as `runtimeOptionsSchema` and `checkpointExcludes`. A change to them alone needs no new pods. |
| Pod hash | The hash of a revision's pod-affecting fields. Revisions with the same pod hash share a pod set on a pool. |
| Pod-set key | The hash that names a pod set: the revision's pod hash together with the derived runtime's and the pool's pod-affecting contributions, such as a derived runtime's `sharedAssets` append and the warm entries of a tenant-derived runtime's owned pool. "RV-3" defines it. |
| Live fields | The runtime fields the gateway enforces or publishes. A change to any of them applies to new sessions at once and creates no revision. |
| Isolation floor | The runtime's `isolationProfile`, a live field that names the weakest isolation profile a pool may run the runtime at. "Isolation floor" under "RV-1" defines it. |
| Agent card | The A2A description of a runtime that the gateway generates from `agentInterface` and serves at `GET /v1/runtimes/{name}/meta/agent-card` ([§5.1](../spec/05_runtime-registry-and-pool-model.md#agentinterface-field)). |
| Write | Any operation that creates or changes a runtime record: `POST /v1/admin/runtimes`, `PUT /v1/admin/runtimes/{name}`, an apply of a `Runtime` custom resource that the RuntimeReconciler mirrors, and the bootstrap upsert of `lenny-ctl bootstrap` with `--force-update`. |
| Traffic | The runtime's list of active revisions and their integer weights. A runtime with no rollout in progress has one active revision at full weight. |
| Rollout | The procedure that moves traffic from one revision to another and retires the old revision's pods. "RV-4" defines it. |
| Rollout policy | The runtime's `rollout.policy` field, which states how a new revision reaches traffic: `automatic`, `manual`, or `immediate`. "RV-1" defines each. |
| Rollout settings | The runtime's `rollout` object: `policy`, `key`, `canaryPercent`, `drainTimeoutSeconds`, and `ownedPoolWarmTimeoutSeconds`. "Rollout settings" under "RV-1" defines each. |
| Post-chain point | The step at session creation, after the `PreRoute` chain has fixed the runtime and pool and after this design has chosen the revision, where the gateway runs every check that depends on the runtime, the revision, or the pool. [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) defines it as RT-2's post-chain checks. |
| Promote | The operator action that gives a revision the full weight at once, without waiting for its pods to warm. A write can request it with `?rollout=immediate`. |
| Retention | The platform-level period after which an unreferenced revision record, and schema content no revision references, is deleted, 365 days by default. |
| Rollout key | The value the gateway hashes to choose a revision: the user ID, the root session ID, or a random value, set by the runtime's `rollout.key`. |
| Pod set | The warm pods of one pool built for one pod-set key. A pool has one pod set outside a rollout. During a rollout it has one per distinct pod hash among the active revisions, so a rollout whose new revision changes only gateway-read fields adds none. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Runtime | A registered agent image and its configuration ([§5.1](../spec/05_runtime-registry-and-pool-model.md#51-runtime)). A runtime is registered either through a `Runtime` custom resource, which the RuntimeReconciler validates and mirrors into the gateway's runtime registry, or through the admin API, which writes the registry directly. |
| Derived runtime | A runtime that names another in `baseRuntime` and changes some fields under §5.1's merge rules. A derived runtime never carries its own `image`, `command`, or `env`, but it can override or append to other fields that this design versions, such as `credentialCapabilities` and `sharedAssets`. |
| Tenant-derived runtime | A derived runtime that a tenant creates, which [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) adds, together with an owned pool on it (DR-5) and the notice to tenants when a base changes (DR-6). |
| Owned pool and the tenant's charge | The one pool of a tenant-derived runtime, copied from a platform template pool (DR-5). The tenant's charge is the pod figure checked against the grant's `maxOwnedPods` budget, which DR-5 "Bounds" defines, and a reservation (DR-10) is a floor of pods held for an owned pool within the deployment's `reservations.totalPods`. |
| Warm list | The list per pool of repository stores that the pool's pods fill before they report ready, which [`egress-and-git-design.md`](egress-and-git-design.md) defines in its "Terms". It includes the `gitRepository` entries of the runtime's `sharedAssets`. |
| Quarantine | The forensics state of one agent pod, carried by the `lenny.dev/quarantine` label, in which the pod and its Sandbox stay until a recorded release ([`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md), FE-1). |
| Pool, `SandboxTemplate`, and `SandboxWarmPool` | A pool is a set of pre-warmed pods for one runtime at one isolation profile and resource class ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). `SandboxTemplate` and `SandboxWarmPool` are the custom resources that describe a pool's pods and its warm count. |
| PoolScalingController | The controller that sets each pool's warm count (`minWarm`) from its demand history ([§4.6.2](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration)). |
| `RuntimeUpgrade` | The §10.5 state machine for replacing a pool's image, with the phases `Pending`, `Expanding`, `Draining`, `Contracting`, and `Complete`, a `Paused` state, `canaryPercent`, and rollback. |
| `PreRoute` chain | The interceptor phase that chooses a session's runtime and pool, including the route interceptor that applies runtime routes, as [`runtime-routing-design.md`](runtime-routing-design.md) defines it. |
| Routing labels | The `routing` map on a session that records the `PreRoute` chain's decisions ([`runtime-routing-design.md`](runtime-routing-design.md)). |
| Root session | The top-level session of a delegation tree. A derived session is a new root. |
| `session_start` | The first frame the adapter sends a runtime for a session ([§28.5.3](../spec/28_communication-channels.md#2853-intra-pod)). |
| `runtimeOptions` and `runtimeOptionsSchema` | `runtimeOptions` is the session field that carries runtime-specific options, at most 64 KB of JSON, which the runtime reads at start. `runtimeOptionsSchema` is the JSON Schema a runtime registers for them ([§14](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema), the `runtimeOptions` field notes). A runtime with no schema receives its options unvalidated, with a `RuntimeOptionsUnschematized` warning. |
| Delegation `TaskSpec` | The request a runtime sends with `delegate_task` to start a delegated child ([§8.2](../spec/08_recursive-delegation.md#82-delegation-mechanism)), which the `DelegationPolicy` of [§8.3](../spec/08_recursive-delegation.md#83-delegation-policy-and-lease) checks. |
| Discovery | `GET /v1/runtimes` and `list_runtimes`, which list the runtimes a caller may use with their published fields, the agent card, and the web playground's form, which [§27.4](../spec/27_web-playground.md#274-ui-surface) generates from a runtime's `runtimeOptionsSchema`. |
| Resume | The reattachment of a session to a new pod from its checkpoint after a pod loss or a suspension ([§7.3](../spec/07_session-lifecycle.md#73-retry-and-resume)). |
| Derive | A new session that starts from an earlier session's snapshot ([`session-derive-design.md`](session-derive-design.md)). |
| `injection.defaultMode`, `capabilities.output.maxFileBytes`, `maxWorkspaceSealDurationSeconds`, and `harnessApprovals` | Runtime fields that [`message-delivery-design.md`](message-delivery-design.md) adds. `injection.defaultMode` names the delivery intent a message takes when its sender names none, and must be one of `injection.modes` (MD-2). `capabilities.output.maxFileBytes` lowers the operator's limit on a file the runtime produces for the user (MD-18). `maxWorkspaceSealDurationSeconds`, a pool setting under §7.1, becomes a runtime field as well: the runtime's value overrides the pool's seal window, the time within which the seal at session end retries and the session's file copies still running finish, at most the chart value `gateway.workspaceSealMaxDurationCeilingSeconds`, 600 seconds by default (MD-18, decision A19 of that design). The runtime's `harnessApprovals` value is the default for a session's policy on the permission requests a harness raises for its own tools (MD-19). |
| `platform-admin` and `tenant-admin` | Lenny's administrative roles for the whole platform and for one tenant. |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static checks, 1 unit, 2 component, 3 contract, 4 integration on the compose stack, 5 end to end on Kind, 7a local concurrency, 8 chaos, 9 security, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| RT-2, RT-3, and RT-4 | [`runtime-routing-design.md`](runtime-routing-design.md) | The `PreRoute` contract and the post-chain point, routing labels, and runtime routes with their bucketing function. |
| DR-2 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) | A prerequisite that adds `SandboxTemplate.spec.podRuntimeRef`, the base runtime whose `Runtime` resource an owned pool's pods are built from. |
| DR-5 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-5-owned-pools) | Owned pools, their template copy, and their "Bounds": the tenant's charge against `maxOwnedPods` and the rule that creates no pod past it. |
| DR-6 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-6-base-changes) | Base changes: a platform write to a base always succeeds, a tenant-derived runtime it breaks reads as `incompatible`, and `runtime.base_changed` notifies the tenant. |
| DR-10 and decision 35 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-10-capacity-reservation) | Capacity reservations for owned pools, and the decision that a pod failed by a platform-declared cause does not count in the tenant's charge. |
| SD-1 | [`session-derive-design.md`](session-derive-design.md) | The derive route and its checks on the target runtime. |
| FE-1 | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md#fe-1-quarantine) | Quarantine of an agent pod and the retirement paths that skip a quarantined pod. |

## High-level design

A runtime today is one mutable record. Changing its image or command changes what the next pod runs, for every pool and every tenant at once, and no switch moves only some sessions onto the new version. This design keeps the runtime's name and record, and gives the part of the record that defines the pod a history. Each change to that part creates a numbered revision. The runtime holds a weight for each revision in use, and the gateway places each new session on one of them by a stable hash, so an operator can send 10% of sessions to revision 7, watch, and then move the rest or move back. Clients keep asking for `claude-worker` and never see the rollout, apart from the revision number their session reports.

Fields the gateway enforces, such as setup command policy, limits, and the runtime's isolation floor, stay outside revisions and change at once, so a security tightening never waits for a rollout.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Revisions (RV-1).** A write that changes a versioned field creates the next revision. Live fields change in place. Versioned fields divide into pod-affecting fields, which reach the pod, and gateway-read fields, such as the options schema, which only the gateway reads. The runtime's `rollout` policy decides whether the new revision reaches traffic once its pods are warm, waits for an operator, or takes every new session at once. A write or an operator can request the last of these for one urgent change. Revisions are kept until a platform-wide retention period passes and nothing references them.

**Selection (RV-2).** After the `PreRoute` chain has fixed the runtime and pool, the gateway hashes the rollout key over the runtime's traffic to choose a revision. The post-chain point then runs every check that depends on the runtime, the revision, or the pool, and a check that reads a versioned field reads the chosen revision. The session records `runtimeRevision`, and `session_start`, billing events, and session metrics carry it. Discovery shows each caller the revision it would be assigned.

**Pods (RV-3).** During a rollout each pool keeps a pod set per distinct pod hash among the active revisions, so revisions that differ only in gateway-read fields share one. A pod set's key also covers what a derived runtime or the pool adds to the pods, such as a tenant's warm repositories, so a change to those replaces the pod set without creating a revision. The PoolScalingController splits the pool's warm count by weight. The pod builder reads the image, command, and environment from the revision, and the warm list comes from the revision's `sharedAssets`. A rollout's extra pods on a tenant's owned pool do not count against the tenant's budget.

**Rollout (RV-4).** The `RuntimeUpgrade` phases move from a pool to a runtime and act on traffic: warm the new revision, shift weight, stop new sessions on the old revision, wait for its sessions, and delete its pod sets.

**Derived runtimes (RV-5).** A derived runtime has no revisions of its own. A session on it uses a revision of the base chosen by the base's traffic, with the derived runtime's fields merged on top. A narrowing that the derived runtime declares has to hold against every revision in the base's traffic.

**Sessions over time (RV-6).** A resumed or retried session stays on its revision while that revision has pods. A delegated child and a derive choose a revision as a create does.

**Session options (RV-7).** A runtime's options schema is stored inline on the revision and compiled when it is written. A session's options are validated at the post-chain point against the chosen revision's schema, and against a derived runtime's own schema as well, so the options a session runs with are the options its revision accepts. The runtime receives them in `session_start`.

```
 Write to runtime claude-worker (API, custom resource, or bootstrap)
        │
        ├── live fields changed ──> apply at once
        └── versioned fields changed ──> revision 7
                                          │
               rollout policy: automatic ─┐   manual ─┐   immediate ─┐
                                          ▼           ▼              ▼
                    warm rev 7, then 100%   rev 7 at weight 0,   rev 7 at 100% now,
                                            operator shifts      cold starts until
                                            weight               its pods warm

 Session creation
        │
        ▼
 PreRoute chain, including the route interceptor (runtime-routing-design.md)
        │   runtime: claude-worker, pool: claude-worker-sandboxed-medium
        ▼
 Revision selection: HMAC("revision/" + runtime name, rollout key) over traffic
        │   traffic: rev 6 weight 90, rev 7 weight 10
        ▼
 Post-chain point: every runtime, revision, and pool check, on the chosen revision,
        │            including runtimeOptions against the revision's schema
        ▼
 Claim from the pool's pod set for the chosen revision's pod hash
        │
        ▼
 Session record: runtime, runtimeRevision, pool, routing labels
```

"Limits" states what the change costs: a pod set per distinct pod hash during a rollout, more pod sets for owned pools outside their tenants' budgets, a resumed session that can change revision once its revision is retired, and options that the new revision's schema can refuse.

## Decisions

Settled by the owner on 2026-11-08:

| ID | Decision |
| :-- | :-- |
| D1 | Operator rollouts of a new version of one runtime are designed here, separately from [`runtime-routing-design.md`](runtime-routing-design.md), which adds no routes across tenants. |
| D2 | No caller can choose a revision. Every session receives the revision the traffic selects. |
| D3 | `automatic` is the default rollout policy. A change that must reach the next session uses `immediate`, either as the runtime's policy, as `?rollout=immediate` on a write, or as an operator's promote. "RV-1" states the policies (formerly O1). |
| D4 | Every revision is kept until a platform-level retention period passes, 365 days by default, and is then deleted only when no traffic or live session references it (formerly O3). |
| D5 | Derived runtimes have no revisions of their own and follow their base's revisions (formerly O4). A derived runtime's overrides merge over the selected base revision, so the base's traffic alone decides which binary a session runs. |
| D6 | The default rollout key is `user`, with the root session ID for an anonymous session (formerly O5). |
| D7 | A resume whose revision has been retired runs on the revision with the highest weight, and the change is recorded on the session and in an audit event (formerly O6). |

Settled by the owner on 2026-11-09, as Q1 of the cross-design sync review:

| ID | Decision |
| :-- | :-- |
| D8 | While a platform rollout is in progress, the rollout's extra pod set on an owned pool is exempt from the tenant's `maxOwnedPods`, its warm target, and `reservations.totalPods`, as decision 35 of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#decisions) exempts pods that a platform cause failed. Under `automatic`, the rollout waits for each owned pool's new pod set with a timeout, and then moves traffic with cold starts on that pool. In the owner's word, "ok". The idle minutes of the extra pod set are billed to the platform rather than the owning tenant, which the owner settled on 2026-11-09 in the word "ok". |

Settled by the owner on 2026-11-09, in the owner's words "Go with this":

| ID | Decision |
| :-- | :-- |
| D9 | Which fields are versioned (formerly O2). The classification of "Field classes" under "RV-1" stands, and `runtimeOptionsSchema` is versioned and gateway-read. The `Runtime` resource carries the schema inline, the built-in reference runtimes' schemas ship as files, no component fetches a schema URL, a write compiles the schema and refuses one that does not compile, and the revision stores the schema by digest ("RV-7"). A session's options are validated at the post-chain point against the chosen revision's schema, and a derived runtime's own schema is a second, separate check. Validation against every revision in the traffic was rejected, because renaming an option would then refuse every client as soon as the new revision entered the traffic. Versioned fields split into pod-affecting and gateway-read fields, and revisions with the same pod hash share a pod set, so a rollout that changes only gateway-read fields warms nothing ("RV-1", "RV-3", and "RV-4"). Discovery shows the revision a caller would be assigned ("Discovery" under "RV-2"). The options reach the runtime in `session_start`, the delegation `TaskSpec` and a derive can carry them, an interceptor cannot change them ([`runtime-routing-design.md`, "RT-2 The interceptor contract"](runtime-routing-design.md#rt-2-the-interceptor-contract)), and a resume onto a revision whose schema refuses them fails with a recorded reason ("RV-6"). |

Settled by the owner on 2026-11-09, after an adversarial review of revision 6's "Field classes". D10 was accepted before the others. The owner settled D11 to D19 in the words "go with your recommendations", and the removal of the tenant's `preConnect` override in D15 in the words "I agree we shouldn't have a tenant override for preConnect". D18 follows the coordinator's correction of the review's finding, whose premise about the tree was false:

| ID | Decision |
| :-- | :-- |
| D10 | Pod-affecting and gateway-read fields (review finding F3). A field is pod-affecting when it shapes the pod, its template, or its warm state before bind, and data the gateway delivers per session at bind is gateway-read. `sdkWarmBlockingPaths` and `minPlatformVersion` become gateway-read. `restartable` leaves the field list, because `Spec.Restartable()` computes it. `requireSoPeercred`, `deploymentModel`, `workspaceTier`, and `capabilities.preConnect` stay pod-affecting ("Field classes"). |
| D11 | Isolation floor (G1). A runtime's `isolationProfile` is a live floor and is not versioned. Pool admission and the WarmPoolController refuse a pool below it, pool creation and a session that names no profile are raised to it, and a write that raises it is refused while a weaker pool references the runtime ("Isolation floor"). |
| D12 | Agent card (G2). `agentInterface` is versioned and gateway-read. Discovery publishes it per caller, and the gateway generates the agent card on read from the selected revision's `agentInterface` and caches it by revision. §5.1's card generated at write time and `regenerate-cards` are retired ("Discovery" under "RV-2"). |
| D13 | Pod-set key (G3). A pod set's key hashes the base revision's pod-affecting fields with the derived runtime's and the pool's pod-affecting contributions. A change to those contributions creates a pod set without creating a revision ("RV-3"). |
| D14 | Checkpoint exclusions (G4). `checkpointExcludes` is gateway-read, and the gateway delivers each session's list at bind: the base revision's list with a tenant's entries appended ("Field classes", and IC-8 of [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md#runtime-fields-and-revisions)). |
| D15 | `preConnect` (F4 and G5). A tenant cannot override `capabilities.preConnect`. The gateway's bind branch and the recycle re-warm read it from the revision of the pod's pod set. A tenant turns SDK-warm off with a blocking path such as `*` on a shared pool, and with the pool setting `sdkWarm: disabled` on an owned pool ("Per-tenant capability override"). |
| D16 | Blocking paths (G6). The per-tenant override of `sdkWarmBlockingPaths` is append-only, and the demotions that a tenant's appended patterns force are counted apart from a shared pool's SDK-warm circuit breaker rate. A platform-owned derived runtime may still replace the list ("Per-tenant capability override" and "RV-5"). |
| D17 | Interaction and injection (G7). The per-tenant capability override may only narrow `capabilities.interaction` and `capabilities.injection`, and a widening is refused, because these fields describe the binary ("Per-tenant capability override"). |
| D18 | Derived runtimes and `capabilities` (G8, as corrected). A derived runtime may set no `capabilities` field, as §5.1 "Example B" states, and §5.1's merge table gains the Prohibited rows it lacks for `capabilities.preConnect` and `capabilities.midSessionUpload` ("RV-5"). |
| D19 | Resume across revisions (G9). A resume that moves revision under D7 keeps the session's stored plan, and §7.3 step 3e's snapshotless rebuild runs that plan's setup commands under the new binary. The base `workspaceDefaults` stays gateway-read, and a tenant-derived runtime's warm entries reach the pods through the pod-set key of D13 ("RV-6" and "Field classes"). |

No decision is open.

## Requirements

| ID | Requirement |
| :-- | :-- |
| RVR-1 | A change to a versioned field never changes the pods of a revision that already exists. |
| RVR-2 | A client asks for a runtime by name alone. During a rollout, discovery output can depend on the revision the caller would be assigned, and the acceptance of `runtimeOptions` can depend on the session's revision. A rollout changes no other request or response field apart from the reported revision. |
| RVR-3 | During a rollout, new sessions reach each active revision in proportion to its weight, and the same rollout key reaches the same revision while the traffic is unchanged. |
| RVR-4 | A rollout can be paused and rolled back until the old revision's pod sets are deleted. |
| RVR-5 | A change to a live field applies to the next session on every revision. |
| RVR-6 | A session on a derived runtime runs on a base revision chosen by the base's traffic. |
| RVR-7 | A resumed or retried session keeps its revision while that revision has pods. |
| RVR-8 | The session record, `session_start`, billing events, and session metrics carry the revision. |
| RVR-9 | A check at session creation that reads a versioned field reads the chosen revision. A check at a write or at pool admission that reads a versioned field holds for every revision in the runtime's traffic. |
| RVR-10 | A rollout never deletes a quarantined pod or its Sandbox. |
| RVR-11 | Under `automatic` and `manual`, a revision's weight rises only after its pod sets hold their share of the warm target at the new weight. Only `immediate`, a promote, and a rollback move traffic before that. |
| RVR-12 | A session runs only with `runtimeOptions` that the chosen revision's schema accepts, and on a derived runtime with its own schema, that schema as well. The options the post-chain point validates are the options the runtime receives. |
| RVR-13 | Revisions whose pod-affecting fields hash the same share one pod set, and a rollout whose new revision differs only in gateway-read fields warms no pod set. |
| RVR-14 | No pod runs a runtime at an isolation profile weaker than the runtime's isolation floor. |
| RVR-15 | A per-tenant capability override never declares more than the binary declares, and never changes `capabilities.preConnect`. |

## Current state

**Registration [source].** §5.1 registers a runtime through a `Runtime` custom resource or the admin API. The `Runtime` resource carries `image`, `command`, `env`, `capabilities`, `credentialCapabilities`, `sharedAssets`, the policy fields, and others (`pkg/apis/lenny/v1alpha1/runtime_types.go`). `applyCRDFields` in `pkg/controller/runtime/controller.go` mirrors most of them into the registry and notes that `command` and `env` are read by the pod builder from the live resource.

**Pods read the live resource [source].** The sandbox controller builds each pod with `RuntimeImage: rt.Spec.Image` from the `Runtime` resource it reads at pod creation (`pkg/controller/sandbox/controller.go`). `SandboxTemplate` carries `runtimeRef` and no image. No code was found that replaces warm pods whose image differs from the resource, so an edit to the resource's `image` produces a pool of mixed images until the old pods are recycled ("Validation owed").

**API mutability [source].** §5.1 states that `image` and `name` are immutable through the API and that every other field is mutable, with changes that would invalidate a derived runtime refused. A change through the API applies to every new session at once.

**`RuntimeUpgrade` [source].** §10.5 runs the state machine per pool with `--new-image <digest>` and `canaryPercent`. `pkg/gateway/upgrade/runtimeupgrade` delivers the durable store, the admin endpoints under `/v1/admin/pools/{name}/upgrade-*`, the CLI, and the metrics. Its package comment states that the Kubernetes side "remains a follow-on". No code under `pkg/gateway/sessionserver` reads the upgrade state, so `canaryPercent` places no session. A pool-level image also has no field to live in, because `SandboxTemplate` has no image.

**Derived runtimes [source].** A derived runtime inherits `image`, `command`, `env`, `integrationLevel`, and `requireSoPeercred` from its base and may not set them, and it may not set `type`, `executionMode`, `isolationProfile`, `allowedResourceClasses`, or any `capabilities` field (§5.1 "Inheritance Rules", "Normative Merge Algorithm", and "Example B"). `validateDerivedRuntime` refuses a derived payload that carries `capabilities`, and `Merge` in `runtimestore/merge.go` takes `capabilities` and `sdkWarmBlockingPaths` from the base. The merge table lacks Prohibited rows for `capabilities.preConnect` and `capabilities.midSessionUpload`, which F-5.1.32 records. It may override `credentialCapabilities`, `runtimeOptionsSchema`, and `capabilityInferenceMode`, and append to `sharedAssets` and `workspaceDefaults`. [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes) lets a platform write to a base succeed whatever tenant-derived runtimes exist and notifies the affected tenants. §5.1's refusal of a base write that would invalidate a derived runtime stays for platform-owned derived runtimes.

**Readers of versioned fields at session creation [source, from the sync review].** Several checks read fields that this design versions from the runtime row: connector authorization reads `capabilities.deferredToolDiscovery`, the export refusal of DR-8 and session derive's supervisor refusal read `deploymentModel` and `type`, the grant conditions of tenant-derived runtimes read `requireSoPeercred`, and pool admission reads `credentialCapabilities.proxyDialect`. "RV-2" states where each reads once revisions exist.

**Session options [source, 2026-11-09].** The `Runtime` resource names its schema by URL in `runtimeOptionsSchemaRef`, and the admin API stores it inline. The defects below are filed in `BUILD-GAPS.md` as findings of 2026-11-09, and "RV-7" closes each:

- The RuntimeReconciler mirrors a `runtimeOptionsSchemaRef` as `{"$ref": "<url>"}`. `validateAgainstSchema` in `pkg/gateway/sessionserver/envelope.go` reads a schema that fails to compile as no constraint, so a `$ref` schema validates nothing, and a `file://` `$ref` reads the gateway's filesystem.
- A derived schema replaces the base's under §5.1's merge, and the write checks only property names, so a derived schema can widen the base's to no constraint.
- `required` is not enforced when a request carries no options, because the check returns before validation.
- Options are validated in `validateAndBuildCreateRow`, between the two `PreRoute` segments, against the runtime the request named, so a later interceptor that changes the runtime leaves them checked against the wrong schema.
- Options never reach the runtime: the runtime SDKs read them from the pod-scoped manifest, and nothing writes that field.
- The RuntimeReconciler's mirror does not call `derivedRuntimesInvalidatedBy`, and it clears a schema the admin API set whenever the resource carries no `runtimeOptionsSchemaRef`.

**Field placement, isolation, and tenant overrides [source, 2026-11-09].** The adversarial review of revision 6 read these in the tree, and the defects among them are filed in `BUILD-GAPS.md`:

- The per-tenant capability override (`runtimecapoverride.ResolveForTenant`, `runtimestore.ApplyCapabilityOverride`) replaces `capabilities.interaction`, `capabilities.injection`, `capabilities.preConnect`, and `sdkWarmBlockingPaths` for a tenant. The gateway's bind branch reads `preConnect` through it (`runtimeSDKWarm` in `sessionserver/envelope.go`), while the sandbox controller built the pod from the runtime's own value, so a tenant's `preConnect` changes how the gateway binds a session and not how the pod was built (F-5.1.35). The recycle re-warm reads the live runtime row by the pool's `runtimeRef` (`resolveRuntime` in `session/recycle/scrubreporter_seams.go`) and applies no override. The override can also raise `interaction` to `multi_turn` and `injection.supported` to `true` on a binary that declares neither (F-5.1.36).
- `sdkWarmBlockingPaths` reaches no pod. `demotionReason` in `podsession/sdkdemotion.go` reads it after the claim, and no adapter message carries it. Each demotion counts toward the pool's SDK-warm circuit breaker, which sets `spec.sdkWarmDisabled` when the rolling 5-minute demotion rate passes 90% (§6.1 **Circuit-breaker for SDK-warm pools.**), and a tenant's demotions count toward the breaker of a pool other tenants share. A derived runtime cannot store the `[]` that §6.1 tells it to set, because `Merge` takes the base's list, and `ApplyDefaults` replaces an empty list with `CLAUDE.md` and `.claude/*` when `preConnect` is true (F-6.1.29).
- `minPlatformVersion` is compared with the gateway's version only at write (`validateMinPlatformVersion` in `admin/runtimes.go`). At bind the gateway copies it into the pod-scoped adapter manifest, and no component compares it there.
- `Spec.Restartable()` (`runtime_restartable.go`) computes `restartable` from `command`, `executionMode`, and `capabilities.preConnect`.
- Pool creation with no `isolationProfile` takes the deployment default (`isolation.DefaultForMode` in `admin/pools.go`), and a session that names no profile takes the gateway's default profile (`create.go`). `ResolvePool` then matches a template's profile exactly. Pool admission does not compare a pool's profile with its runtime's `isolationProfile`, and the recycle path reads the runtime's value only as a fallback for a pool with none.
- The admin API generates the agent card when a runtime is written and stores it as the `agent-card` `publishedMetadata` entry, and `POST /v1/admin/runtimes/regenerate-cards` regenerates the stored cards.
- `podsession.Binder.integrationVerified` caches a passed integration-level check by runtime name for the life of the gateway process, so a new image or a raised declared level is not probed again (F-5.1.37). `runtimeIntegrationLevel` in `sessionserver/bind_request.go` reads the declared level from the runtime row.

## RV-1 Revisions

### Field classes

A runtime's fields fall into three groups by what they describe:

- **What runs in the pod:** the image, the process command and environment, the files placed in the pod at warm time, whether the runtime process starts before a session, the pod's authentication mode and data tier, and the files and commands a session's plan starts from. Two revisions differ by these.
- **What the binary can do:** fields that describe the program inside the image, which the gateway reads to decide how to treat a session, such as whether it accepts injected messages (`capabilities`), which LLM dialects it speaks (`credentialCapabilities.proxyDialect`), how much of the adapter protocol it implements (`integrationLevel`), which workspace paths stop it from running SDK-warm (`sdkWarmBlockingPaths`), which session options it accepts (`runtimeOptionsSchema`), and how it describes its skills to other agents (`agentInterface`). A new binary can change any of them, so they travel with the image.
- **What the platform enforces or publishes:** policy the gateway applies regardless of the binary, such as the weakest isolation profile a pool may run the runtime at, which setup commands a session may run, session limits, which delegation policy applies, how tools are classified, how often a session is checkpointed, and the documents the platform publishes for the runtime.

The first two groups are versioned, and the gateway reads them from the session's revision. The third group is live, so a security tightening reaches the next session on every revision without a rollout.

Versioned fields split by when their values reach the pod (D9 and D10). A pod-affecting field shapes the pod, its template, or its warm state before bind: the pod builder, the sandbox controller, or the warm-up reads it before any session exists. A gateway-read field reaches a session only through the gateway, which reads it, turns it into session data such as a plan, or delivers it to the adapter per session at bind. A pod built for one revision can serve a session of another revision whose pod-affecting fields are the same, so a pod set is keyed on the hash of the pod-affecting fields ("RV-3").

| Class | Fields | On change |
| :-- | :-- | :-- |
| Versioned, pod-affecting | `image`, `command`, `env`, `deploymentModel`, `sharedAssets`, `workspaceTier`, `requireSoPeercred`, and `capabilities.preConnect` | A new revision with a new pod hash, whose pod sets warm before traffic moves. |
| Versioned, gateway-read | `runtimeOptionsSchema`, the other `capabilities` entries, `capabilities.output.maxFileBytes` among them, `credentialCapabilities`, `integrationLevel`, `workspaceDefaults`, `sdkWarmBlockingPaths`, `minPlatformVersion`, `checkpointExcludes`, `agentInterface`, `maxWorkspaceSealDurationSeconds`, and the `harnessApprovals` default | A new revision that shares the pod set of the revision with the same pod hash. |
| Live | `isolationProfile`, `setupCommandPolicy`, `setupPolicy`, `limits`, `sessionPolicy`, `archivePolicy`, `delegationPolicyRef`, `allowSelfRecursion`, `supportedProviders`, `allowedResourceClasses`, `defaultPoolConfig`, `capabilityInferenceMode`, `toolCapabilityOverrides`, `checkpointPolicy`, `periodicCheckpointIntervalSeconds`, `publishedMetadata`, `labels`, and `rollout` | Applied to the next session on every revision. |
| Fixed | `name`, `type`, `executionMode`, and `baseRuntime` | Refused. A different value needs a new runtime. |

`restartable` is in no class, because no write sets it. `Spec.Restartable()` in `pkg/apis/lenny/v1alpha1/runtime_restartable.go` computes it from `command`, `executionMode`, and `capabilities.preConnect`, so it changes only when one of those changes (D10).

Each versioned field was placed by its reader in the tree, read on 2026-11-09:

| Field | Reader | Kind |
| :-- | :-- | :-- |
| `image`, `command`, and `env` | The sandbox controller's pod builder, from the live `Runtime` resource (`pkg/controller/sandbox/controller.go`). | Pod-affecting |
| `deploymentModel` | The pod builder, which runs the runtime supervisor on a sidecar pod (`supervision.go`), and the gateway's checks at the post-chain point. | Pod-affecting |
| `workspaceTier` | The pod builder, which adds the `T4` node isolation (`podspec/t4_node_isolation.go`). | Pod-affecting |
| `requireSoPeercred` | The pod builder, which sets nonce-only mode (`podspec/nonce_only.go`). | Pod-affecting |
| `capabilities.preConnect` | The sandbox controller, which SDK-warms the pod (`sdkwarm.go`), and `Spec.Restartable()`, which the pod builder reads before it creates a `restart` pod and pool admission reads. The gateway's bind branch (`runtimeSDKWarm` in `sessionserver/envelope.go`, then `podsession/bindlaunch.go`) and the recycle re-warm (`resolveRuntime` in `session/recycle/scrubreporter_seams.go`) read it from the runtime row today. Under D15 both read it from the revision of the pod's pod set, as "Per-tenant capability override" states. | Pod-affecting |
| `sharedAssets` | The pod builder, the adapter's warm layout (`pkg/adapter/warmlayout.go`), and the warm list. | Pod-affecting |
| `sdkWarmBlockingPaths` | The gateway alone. `demotionReason` in `podsession/sdkdemotion.go` decides after the claim whether the session's plan demotes an SDK-warm pod, and no adapter message carries the list. Each demotion counts toward the pool's SDK-warm circuit breaker (`pkg/controller/poolscaling/circuitbreaker.go`). | Gateway-read |
| `minPlatformVersion` | The gateway, which compares it with its own version only at write (`validateMinPlatformVersion` in `admin/runtimes.go`), copies it into the pod-scoped adapter manifest at bind as session data (`adapterclient`), and publishes it in discovery. | Gateway-read |
| `checkpointExcludes` | Not in the tree. Under IC-8 of [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md#runtime-fields-and-revisions), the gateway delivers each session's list at bind, and the capture on the pod leaves the listed directories out (D14). | Gateway-read |
| `capabilities.interaction`, `capabilities.injection`, `capabilities.midSessionUpload`, and `capabilities.deferredToolDiscovery` | The gateway: delivery and injection rules, pool admission (`poolstore.go`), uploads to a running session (`upload_to_session.go`), the link check of [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md), which adds `deferredToolDiscovery`, and discovery. | Gateway-read |
| `capabilities.output.maxFileBytes` and the `harnessApprovals` default | Not in the tree. Under [`message-delivery-design.md`](message-delivery-design.md), the gateway reads `maxFileBytes` when it starts a file export, as the smaller of it and the operator's limit, and reads the `harnessApprovals` default when a session's create request carries none. Neither reaches the pod. | Gateway-read |
| `maxWorkspaceSealDurationSeconds` | Not on the `Runtime` in the tree, which reads the pool-level window of §7.1 only as the gateway flag `--workspace-seal-max-duration-seconds` (F-5.2.57). Under [`message-delivery-design.md`](message-delivery-design.md), the gateway reads it from the session's revision, over the pool's value and then the gateway flag, each at most the ceiling (A23 of that design), as the session's seal window: at the seal, for the seal's retry window and the time the session's exports still running have to finish, and after the terminal transition, for the session's terminal pipeline budget. A write above `gateway.workspaceSealMaxDurationCeilingSeconds`, or not greater than `delegation.usageQuiescenceTimeoutSeconds`, is refused. It does not reach the pod. A tenant-derived runtime may set it within the same bounds, as [`tenant-derived-runtimes-design.md`, "Fields"](tenant-derived-runtimes-design.md#fields) states. | Gateway-read |
| `credentialCapabilities` | The gateway: credential pre-claim (`credential_preclaim.go`) and pool admission. | Gateway-read |
| `integrationLevel` | The gateway, which compares it with the level the adapter reports at a session's assignment (`podsession/integrationlevel.go`), and discovery. "Integration-level check" under "RV-2" states how that check keys on the revision. | Gateway-read |
| `workspaceDefaults` | The gateway, which composes it into the session's plan, and the pod receives the plan as session data. A base runtime's warm repositories come from `sharedAssets` (decision 32 of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#decisions)). A tenant-derived runtime's `gitClones` entries with `warm: true` fill its owned pool's pods before bind under DR-11, and they enter the pod-set key as the derived runtime's contribution (D13 and D19). | Gateway-read |
| `runtimeOptionsSchema` | The gateway: options validation, discovery, and the playground. | Gateway-read |
| `agentInterface` | The gateway: discovery, the agent card it generates on read (D12), and the pod-scoped adapter manifest at bind (`sessionserver/bind_request.go`). | Gateway-read |

Notes on the classification:

- `workspaceTier` sets the data classification of the pods and, at `T4`, forbids cross-tenant pod reuse and requires dedicated nodes. A pod built under one tier cannot become another, so it is versioned. A tightening such as `T3` to `T4` uses `immediate`.
- `isolationProfile` on a runtime is a live floor (D11). Pool resolution happens before revision selection, so a per-revision floor could not choose a pool and would only create revisions that change no pod. "Isolation floor" below states the rule.
- `workspaceDefaults` holds the files and setup commands a session's plan starts from. A new binary often needs different setup commands, so they travel with it. The plan is composed at the post-chain point from the chosen revision.
- `sdkWarmBlockingPaths` and `checkpointExcludes` describe the binary's warm behaviour and the regenerable directories it writes, such as `node_modules/`, and `minPlatformVersion` states what the binary needs from Lenny. Each reaches a session as data that the gateway reads or delivers at bind, so a change to any of them alone shares the pod set (D10 and D14). A write that creates a revision checks `minPlatformVersion`.
- A revision that changes only `sdkWarmBlockingPaths` shares its pod set with the revision it amends, and its demotions count toward the circuit breaker of that pod set's pool, which sets `spec.sdkWarmDisabled` on the `SandboxWarmPool` when the rolling 5-minute demotion rate passes 90% (§6.1 **Circuit-breaker for SDK-warm pools.**). A rollout that only adds a blocking path can therefore flip a pod set that several revisions share to pod-warm. A tenant's appended patterns do not count toward that rate (D16).
- `checkpointPolicy` and `periodicCheckpointIntervalSeconds` are policy that [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) lets a runtime and a pool each set, with the stricter policy and the pool's interval winning. They are live.
- `capabilityInferenceMode` and `toolCapabilityOverrides` decide the capability the gateway assigns each MCP tool, which environment filters enforce, so they are live.
- `archivePolicy` governs how the gateway extracts uploaded archives, so it is live.
- `type` and `executionMode` change how the gateway dispatches every session and which pools can serve the runtime, so a revision cannot carry them. Making them fixed is a change from §5.1, which lets an API write change them today.
- A check at a write or at pool admission that reads a versioned field holds for every revision in the runtime's traffic, because the pool serves all of them. A pool with `deliveryMode: proxy` requires `credentialCapabilities.proxyDialect`, so admission checks every revision in the traffic, and a write that creates a revision without it is refused while such a pool references the runtime. A check at session creation reads the chosen revision at the post-chain point ("RV-2").
- `runtimeOptionsSchema` describes the options the binary accepts, so it is versioned (D9). Only the gateway reads it, so a schema fix shares the pod set of the revision it amends. "RV-7" states how the schema is stored and how a session's options are validated against the chosen revision.
- `workspaceDefaults` is gateway-read (D19). The base runtime's defaults reach a pod only inside a session's plan, which the gateway composes per session, so a warm pod holds nothing from them. A tenant-derived runtime's warm entries under DR-11 already shape its owned pool's pods before bind. They are the derived runtime's contribution to the pod-set key under "RV-3", and a change to them replaces the owned pool's pod set without creating a base revision.
- `agentInterface` describes the binary's skills to other agents, so it is versioned and gateway-read (D12). `description`, the admin-facing text, stays live with `publishedMetadata`. The owner's statement of D9 named descriptions among the gateway-read fields, and `description` describes no part of the binary, so "Validation owed" asks the owner to confirm its placement.
- `sharedAssets` entries that reference an artifact are pinned to the artifact's content digest in the revision, so a later change to the artifact behind the same reference does not change an existing revision.
- A live field that the platform already validates against derived runtimes keeps that validation.

### Isolation floor

A runtime's `isolationProfile` names the weakest profile a pool may run it at, in the order `standard`, `sandboxed`, and `microvm` of [§5.3](../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles). It is live (D11) **[design]**:

- **Pool admission.** The gateway's pool admission refuses, at create and at update, a pool whose `isolationProfile` is weaker than its runtime's floor, and the refusal names the floor. The WarmPoolController also refuses to build pods from a `SandboxTemplate` whose profile is weaker than the floor of the runtime it serves, and records the refusal as a condition on the template. This follows the `acknowledgeNonceOnlyAuth` precedent of §5.3, under which the WarmPoolController renders nonce-only mode only for acknowledged pools, so neither the embedded stack's `runtime apply` (`pkg/embedded/stack/runtimeapply.go`) nor a direct `kubectl` apply can place a pod below the floor.
- **Defaults.** A pool created with no `isolationProfile` takes the stronger of the platform default and the runtime's floor, where today it takes the platform default alone **[source]**. A session that names no profile is raised to the floor of its final runtime before pool resolution, where today it asks for the platform default and `ResolvePool` requires a template with exactly that profile **[source]**. A session that names a profile below the floor finds no pool, as a session that names a profile no pool runs does today.
- **Raising the floor.** A write that raises a runtime's floor is refused while a pool with a weaker profile references the runtime, and the refusal names those pools. The operator raises or deletes the pools first. A write that lowers the floor applies to the next pool write and the next session at once. Derived runtimes still may not set `isolationProfile`, so a derived runtime's floor is its base's.
- **Bootstrap.** [§17.6](../spec/17_deployment-topology.md#176-packaging-and-installation) item 5 names a runtime's `isolationProfile` as a security-critical field that `lenny-ctl bootstrap --force-update` never overwrites. That block stays, restated for the floor: a bootstrap upsert never changes a runtime's floor, and an operator changes it through the admin API, where the refusal above applies. A floor change creates no revision, so `--rollout immediate` has no effect on it.

### Per-tenant capability override

The per-tenant capability override of §5.1 ("Capabilities are customizable per tenant") lets a tenant adjust a runtime's capabilities for its own sessions, through `runtimecapoverride.ResolveForTenant` and `runtimestore.ApplyCapabilityOverride` (F-5.1.20). Today it replaces `capabilities.interaction`, `capabilities.injection`, `capabilities.preConnect`, and `sdkWarmBlockingPaths` ("Current state"). Under D15, D16, and D17 it becomes a narrowing **[design]**:

- **`preConnect` leaves the override (D15).** `capabilities.preConnect` is pod-affecting: the sandbox controller decided before any session whether the pod is SDK-warm, so a per-tenant value cannot change a pod that a shared pool built. The gateway's bind branch (`runtimeSDKWarm` to `podsession/bindlaunch.go`) and the recycle re-warm (`resolveRuntime` in `session/recycle/scrubreporter_seams.go`) read `preConnect` from the revision of the pod's pod set. The recycle path takes that revision from the `runtimeRevision` of the pod's `SandboxTemplate` ("RV-3"). A stored override that sets `preConnect` is refused at write.
- **Turning SDK-warm off.** A tenant on a shared pool appends a blocking path such as `*`, which demotes every SDK-warm pod its sessions claim. A tenant on its owned pool sets the pool setting `sdkWarm: disabled`, which the owned pool's tenant may write under [DR-5](tenant-derived-runtimes-design.md#template-copy). That setting is pod-affecting pool configuration, so it enters the pod-set key ("RV-3").
- **`sdkWarmBlockingPaths` is append-only (D16).** A tenant's patterns are appended to the list of the session's revision, and a tenant cannot remove or replace an entry. A demotion that only a tenant-appended pattern forces counts toward a separate per-tenant demotion count rather than the pool's circuit breaker rate, so one tenant's `*` cannot flip a pod set that other tenants share to pod-warm.
- **`interaction` and `injection` are restrict-only (D17).** A tenant may lower `interaction` from `multi_turn` to `one_shot`, set `injection.supported` to `false`, keep a subset of `injection.modes` that still contains `queue`, and set `injection.defaultMode` to one of the intents that subset keeps, as [`message-delivery-design.md`, "MD-2"](message-delivery-design.md#md-2-runtime-declaration) requires. A widening is refused, because these fields describe the binary. A write checks the override against every revision in the runtime's traffic, and the override applies over the chosen revision at the post-chain point.

### Rollout settings

The `rollout` object is live, and only a `platform-admin` writes it on a platform runtime.

| Field | Meaning | Default |
| :-- | :-- | :-- |
| `policy` | `automatic`, `manual`, or `immediate`, as "A write" states. | `automatic` |
| `key` | `user`, `session`, or `random`, as "RV-2" states. | `user` |
| `canaryPercent` | The weight a `manual` rollout gives the new revision when the operator first proceeds, and the share of the warm target its pod sets warm to in `Pending`. Unset, the operator sets each weight with `set-weight`. `automatic` ignores it. | Unset |
| `drainTimeoutSeconds` | How long `Draining` waits for the old revision's sessions, after which the remaining ones end with a checkpoint, as §10.5 states for the pool-level procedure today. | The runtime's `maxSessionAge` |
| `warmTimeoutSeconds` | How long a weight increase under `automatic` or `manual` waits for the new revision's pod sets on platform pools to reach their share of the warm target. When it passes, the rollout enters `Paused` with the weight unchanged and emits `upgrade_progressed` with the reason `warm_timeout`. | 1800 |
| `ownedPoolWarmTimeoutSeconds` | How long a weight increase waits for an owned pool's new pod set to warm before it moves traffic with cold starts on that pool (D8). An owned pool's capacity sits outside the platform's control, so this wait ends by moving traffic rather than by pausing. | 600 |

### A write

A write is any of the operations "Terms" lists. Each one computes a hash over the versioned fields, and the pod hash over the pod-affecting fields:

- The first write of a runtime creates revision 1 at full weight.
- A write whose hash equals the latest revision's creates no revision. A `PUT` that changes only live fields, a re-apply of an unchanged custom resource, and a bootstrap upsert with no change all fall here.
- A write whose hash differs creates the next revision, records the writer and the time, and starts a rollout under the runtime's `rollout.policy`:
  - `automatic` (default, D3): the new revision enters at weight 0, its pod sets warm to their target, and traffic then moves to it in one step. A new revision whose pod hash equals an active revision's shares that revision's warm pod sets, so traffic moves without a warm-up. An owned pool whose new pod set is not warm after `rollout.ownedPoolWarmTimeoutSeconds` no longer holds the move, and that pool's sessions on the new revision wait on pod creation until its pod set warms (D8). An operator can still pause or roll back.
  - `manual`: the new revision enters at weight 0 and waits for an operator to drive "RV-4".
  - `immediate`: the new revision takes the full weight at once, and every earlier revision drains. New sessions that arrive before the new pod sets are warm wait on pod creation, as a session does today when a pool has no idle pod. Sessions already running on earlier revisions continue, and an operator who needs them stopped uses the existing force-terminate action.
- **Urgent changes.** A change that must reach the next session, such as an image with a security fix, uses `immediate` without changing the runtime's policy, in one of two ways:
  - `PUT /v1/admin/runtimes/{name}?rollout=immediate`, or `lenny-ctl bootstrap --force-update --rollout immediate`, applies `immediate` to the revision this write creates.
  - `lenny-ctl admin runtimes rollout promote` (`POST /v1/admin/runtimes/{name}/rollout/promote`) gives the newest revision the full weight at once. This serves a change applied through the `Runtime` custom resource, whose apply carries no per-write option.
  - A security tightening of the isolation floor needs neither, because the floor is live and creates no revision ("Isolation floor").
- A write that creates a revision while a rollout is in progress is refused with `409 ROLLOUT_IN_PROGRESS`, naming the rollout, unless it requests `immediate`. An `immediate` write or a promote ends the rollout in progress: the new revision takes the full weight and every earlier revision drains.

A write through the `Runtime` custom resource follows the same rules. The RuntimeReconciler computes the hash when it mirrors the resource and reports `latestRevision` and the traffic in the resource's status. The rollout's progress is state the gateway owns, so a GitOps tool that applies the resource does not fight the operator's weight changes.

### Record

The registry gains a `runtime_revisions` table keyed on runtime name and revision number. Each row holds the versioned fields, their hash, the pod hash, the writer, and the creation time. A row is never updated. Inline `sharedAssets` content is stored once per content digest in a separate table that rows reference, because most revisions change only the image and would otherwise copy the same files. `runtimeOptionsSchema` content is stored the same way, once per digest of its canonical JSON, and the row holds the digest, which the revision's hash covers ("RV-7").

`GET /v1/admin/runtimes/{name}` returns the live fields, the latest revision's versioned fields, the traffic, and the rollout state. `GET /v1/admin/runtimes/{name}/revisions` lists revisions with pagination.

### Retention

- Every revision is kept until the retention period passes (D4). The gateway flag `--runtime-revision-retention`, default 365 days, sets the period for the whole platform.
- A daily job deletes revisions older than the period that no traffic entry, no live session, and no pod set's template references, in batches, and deletes stored asset content and schema content that no remaining revision references.
- A finished session keeps its `runtimeRevision` number after the revision's record is deleted. `GET` of a deleted revision returns `410 REVISION_EXPIRED`.
- A `platform-admin` can delete one revision early with `DELETE /v1/admin/runtimes/{name}/revisions/{n}` when no traffic entry and no live session references it. This removes a revision whose `env` held a value that should not have been stored.

### Storage and performance

The estimates below are unmeasured and assume Postgres compresses large JSON values, which it does above about 2 KB:

| Case | Revisions per year | Size per revision | Storage per year |
| :-- | --: | --: | --: |
| 100 platform runtimes, one versioned change a week | 5,200 | 2 to 10 KB | 10 to 50 MB |
| 100 platform runtimes, one versioned change a day | 36,500 | 2 to 10 KB | 70 to 370 MB |
| 100 platform runtimes, an image build pushed 20 times a day by CI | 730,000 | 0.5 to 2 KB with assets and schemas stored once | 0.4 to 1.5 GB |

- Revisions grow with platform runtimes and their change rate. Derived runtimes, including every tenant-derived runtime, create none (D5), so the count does not grow with tenants. A derived runtime's own fields stay on its runtime row.
- A `Runtime` resource cannot exceed the Kubernetes object size limit of about 1.5 MB, which bounds a single revision written through that path. The admin API applies the same bound.
- Session creation reads only the active revisions, at most a few per runtime, from the gateway's in-memory registry view, which refreshes on change. It never queries the revisions table, so the table's size does not reach the session path.
- The table's indexes are the primary key on runtime name and revision number, and the creation time for the retention job. Listing is paginated.
- "Validation owed" lists a measurement against a generated table of a million revisions.

## RV-2 Selection

Session creation runs in this order, which [RT-2](runtime-routing-design.md#rt-2-the-interceptor-contract) states in the same terms:

1. The `PreRoute` chain, including the route interceptor, produces the final runtime and pool.
2. Revision selection runs on the final runtime, as the steps below state.
3. The post-chain point of RT-2 runs every check that depends on the runtime, the revision, or the pool, on the chosen revision.
4. The gateway claims a pod.

Revision selection:

1. Reads the runtime's traffic from its cached registry view. With one active revision, it takes that revision and skips the hash. For a derived runtime it reads the base's traffic ("RV-5").
2. Picks the rollout key under `rollout.key`: the user ID under `user`, the root session ID under `session` or for an anonymous session under `user`, and a random UUID under `random`.
3. Computes `HMAC-SHA256(key = "revision/" + runtime name, message = rollout key)` with the bucketing function that [RT-4](runtime-routing-design.md#rt-4-runtime-routes) defines, and walks the active revisions in ascending order by cumulative weight.

**Checks on the chosen revision.** Every check at the post-chain point that reads a versioned field reads the chosen revision, merged with the derived runtime's fields and the tenant's capability override where they apply. That covers, among others, `capabilities.deferredToolDiscovery` for the link checks of [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md), `deploymentModel` and `type` for the delegation export refusal of DR-8 and the supervisor refusal of [`session-derive-design.md`](session-derive-design.md), `requireSoPeercred` for the grant conditions of tenant-derived runtimes, and `runtimeOptionsSchema` for the session's options ("RV-7"). A check that fails refuses the session as it does today. The gateway does not try another revision, because the session's revision follows its rollout key.

**Claim.** The gateway claims from the pool's pod set for the chosen revision's pod hash, under the pool's current pod-set key ("RV-3"). An empty pod set takes the existing path for a pool with no idle pod, and the gateway does not substitute a pod set of another pod hash, because substitution would move the split away from the weights under load.

The session record gains `runtimeRevision`. The session response, `session_start` (as `runtimeRevision`), the adapter manifest's per-session fields, billing events (as `runtime_revision`), and the session metrics `lenny_session_total`, `lenny_session_error_total`, and `lenny_session_duration_seconds` (as the label `runtime_revision`) carry it. Active revisions bound the label's values.

The revision is not written into the routing labels, because the `PreRoute` chain did not choose it.

**Integration-level check.** §5.1 compares the integration level a runtime declares with the level its adapter reports, and refuses the first assignment of an underperforming runtime with `RUNTIME_LEVEL_UNDERPERFORMS`. `runtimeIntegrationLevel` in `sessionserver/bind_request.go` reads the declared level from the session's revision, merged as above, rather than from the runtime row. `podsession.Binder.integrationVerified` keys a passed check on the runtime, the pod hash of the pod's pod set, and the declared level, where today it keys on the runtime name alone (F-5.1.37). A revision with a new binary or a new declared level is then probed again at its first assignment, and a revision that shares a pod hash and a declared level with a verified one is not. The pod hash covers the image, so it serves where an image digest would. **[design]**

### Discovery

Discovery publishes versioned fields: `capabilities`, `integrationLevel`, `minPlatformVersion`, `runtimeOptionsSchema`, and `agentInterface`. It covers REST `GET /v1/runtimes`, MCP `list_runtimes`, the public `GET /v1/runtimes/{name}/meta/agent-card`, and the playground. During a rollout the revisions in the traffic can disagree on these fields, so discovery shows one revision's values, chosen per caller (D9 and D12):

- Under `rollout.key: user`, for a caller with a user ID, the revision the caller would be assigned, computed with the HMAC of the selection steps above and the caller's user ID as the rollout key.
- Under `rollout.key: session` or `random`, and for a caller without a user, such as an unauthenticated read of a public agent card, the revision with the highest weight, the older revision on a tie.

Each entry reports `runtimeRevision` beside those fields. A derived runtime's entry uses its base's traffic and name, as selection does ("RV-5"). The rule covers every versioned field that discovery publishes. A session the caller then creates under `rollout.key: user` lands on the revision discovery showed while the traffic is unchanged and no route or interceptor moves it to another runtime, so its options pass the schema the playground rendered.

**Agent card (D12).** The gateway generates the agent card on read from the selected revision's `agentInterface` and caches it by runtime and revision, so a card is generated once per revision and replica. On a derived runtime, the derived runtime's `agentInterface` override merges over the selected base revision's value under §5.1's merge, and the cache keys on the derived runtime and the base revision. §5.1's statement that the gateway generates the card at write time and stores it as an opaque `publishedMetadata` entry is dropped, and `POST /v1/admin/runtimes/regenerate-cards` is retired, because no stored card remains to regenerate. A hand-written `agent-card` entry in `publishedMetadata` on a runtime with no `agentInterface` is served as stored, as today. **[design]**

## RV-3 Pods

- **Pod-set key (D13).** A pod set's key hashes the revision's pod hash together with the pod-affecting contributions of the derived runtime and the pool: the derived runtime's `sharedAssets` append under §5.1's merge table, the pool's DR-11 warm entries (a tenant's warm default clones and cache-only entries, and a platform admin's private entries on an owned pool's row, as "Warm clones" under [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#warm-clones) states), and the owned pool's `sdkWarm` setting ("Per-tenant capability override"). Within one pool the contributions are the same for every revision, so revisions with one pod hash share a pod set, and this design names a pod set by its revision's pod hash where the contributions do not change.
- **A change to the contributions.** A change to the derived runtime's or the pool's contributions creates a new pod-set key without creating a revision and starts no rollout. New pods are built under the new key, and DR-11's "Changing the list" governs how the earlier pod set's pods leave: an idle pod holding a store no longer on the list drains, a pod whose stores fall outside a session's `coveredStores` is refused at the claim, and every other pod stays claimable until it leaves under the age limit, because it holds a subset of what the session was checked for. A change to `sdkWarm` drains the earlier pod set's idle pods, because their runtime process state differs. A tenant makes these changes, so the earlier pod set gets no exemption under D8, and its pods count in the tenant's charge until they leave.
- `SandboxTemplate` gains `podHash`, the pod-set key, and `runtimeRevision`. The warm pool controller keeps one `SandboxWarmPool` and `SandboxTemplate` pair per pool and distinct pod-set key, labelled with the key, and deletes a pair once no active revision has its pod hash and no session uses it, or once the contributions it was built for have been replaced and its last pod has left. The template's `runtimeRevision` names the lowest-numbered revision with that pod hash, and every revision with the pod hash gives the pod builder the same pod-affecting values (RVR-13). It never deletes a Sandbox whose pod carries the `lenny.dev/quarantine` label: the pair stays while such a Sandbox exists, and the deletion reports it to the forensics reconciler, as FE-1's "Retirement paths" states for pool deletion (RVR-10).
- The pod builder reads `image`, `command`, `env`, and the other pod-affecting fields from the revision named by the pod's template. It no longer reads them from the live `Runtime` resource, so no edit reaches an existing revision's pods (RVR-1). For an owned pool, the template's `podRuntimeRef` (DR-2) names the base and `runtimeRevision` names the base revision.
- **Warm list per revision.** The warm list comes partly from the revision's `sharedAssets`, so the PoolScalingController renders one warm list per pool and pod set, onto that pod set's template. `sharedAssets` is pod-affecting, and the derived runtime's and the pool's warm entries are part of the pod-set key, so revisions that share a pod set share its warm list, and "per pool and revision" below resolves through the revision's pod set. The hosts the list adds to a warm pod's reach and the readable set of a pod before binding, which [`egress-and-git-design.md`](egress-and-git-design.md#effective-policy) and "Warm clones" under [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#warm-clones) read from the pool row today, key on pool and revision, so a pod warming for revision 7 reaches only revision 7's repositories.
- The PoolScalingController computes the pool's warm target from the pool's demand history, as §4.6.2 states, and splits it across pod sets by the summed weight of the active revisions in each, with a floor of one pod for a pod set whose revisions hold positive weight. Under `automatic`, a new revision's pod set warms to the full target before traffic moves. A new revision that shares a pod set adds nothing to warm, because the pod set already holds the share of the revisions in it. The pool's demand history is shared by its revisions, so a new revision has no cold-start gap.
- During a rollout a pool holds up to the sum of its pod sets' targets. Outside a rollout it holds one pod set. A rollout whose new revision changes only gateway-read fields keeps one pod set throughout.
- **Owned pools during a base rollout (D8).** On an owned pool, the pods of every pod set other than the one that holds the revision with the highest weight, the older revision on a tie, count in neither the tenant's charge nor the creation rule of `maxOwnedPods`, and a pool with a reservation places that extra pod set outside its reserved pods, so `reservations.totalPods` is unchanged. This holds while the platform rollout is in progress, as decision 35 exempts pods failed by a platform cause. The namespace `ResourceQuota` still bounds the extra pods. When the rollout ends, the remaining pod set is the pool's only one and counts as before. A rollout cannot raise what a tenant is charged, and a tenant at its bound still gets the new revision. `pool.idle_usage` in [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md#poolidle_usage) attributes the extra pod set's idle minutes to the platform rather than the owning tenant.

## RV-4 Rollout

The §10.5 phases move from a pool to a runtime and act on traffic. Every pool of the runtime moves together.

| Phase | Traffic and pods |
| :-- | :-- |
| `Pending` | The new revision exists at weight 0. Its pod sets warm to the share of the target that `rollout.canaryPercent` sets, or to the full target under `automatic`, with an owned pool waited on for at most `rollout.ownedPoolWarmTimeoutSeconds`. |
| `Expanding` | The new revision's weight rises to `rollout.canaryPercent`, then by operator steps, or to the full weight in one step under `automatic`. Each increase warms first, as "Warm before weight" states. |
| `Draining` | The new revision holds the full weight. The old revision receives no new sessions, and its sessions run to completion or until `rollout.drainTimeoutSeconds`, after which the remaining ones end with a checkpoint. A session whose revision shares a pod set with the new revision is not ended, because no pod set of its pod hash is deleted. |
| `Contracting` | The old revision's pod sets that no active revision shares are deleted, apart from any quarantined Sandbox, which stays with its pair until FE-1 releases it (RVR-10). |
| `Complete` | One revision remains active. |
| `Paused` | Weights and pod sets hold. Reachable from every phase except `Complete`. |

- **Warm before weight.** Every weight increase under `automatic` or `manual`, including `proceed` and `set-weight`, first raises the new revision's pod sets to their share of the warm target at the requested weight. The weight moves once they hold it. Until then the old revision keeps its share at the current weight, so a step adds at most the new revision's increase in warm pods. The wait is bounded by `rollout.warmTimeoutSeconds` on platform pools and by `rollout.ownedPoolWarmTimeoutSeconds` on owned pools. `status` reports a pending step and each pod set's warm count against its share (RVR-11).
- **Gateway-read rollouts.** A new revision whose pod hash equals the old revision's shares the old revision's pod sets, which already hold the warm target at the summed weight, so each weight increase moves at once and no phase waits for a warm-up. The traffic weights still apply: under `manual` with `canaryPercent: 5`, 5% of new sessions run on the new revision, read its gateway-read fields, and have their options validated against its schema, while every session claims from the same pod sets (RVR-13).
- **Options the new revision refuses.** Before `Contracting`, `status` reports how many suspended sessions on the old revision hold `runtimeOptions` that the new revision's schema refuses, because each of them fails its resume once its pod set is gone ("RV-6").
- **Rollback.** Until `Contracting`, rollback returns the full weight to the old revision and drains the new one. Rollback does not wait for warm pods, because the new revision is the one being left. A weight of 0 releases a revision's idle warm pods, so a rollback after the old revision reached weight 0 starts new sessions cold until its pod sets warm again. After `Complete`, an operator rolls back by writing the old revision's versioned fields, which creates a new revision with the old content and starts a rollout.
- **Promote.** A promote moves the rollout from any phase before `Draining` straight to `Draining`, with the newest revision at full weight. It does not wait for the new pod sets to warm.
- **Surface.** `/v1/admin/runtimes/{name}/rollout` replaces `/v1/admin/pools/{name}/upgrade-*`, and `lenny-ctl admin runtimes rollout` with `status`, `proceed`, `set-weight`, `promote`, `pause`, `resume`, and `rollback` replaces `lenny-ctl admin pools upgrade`. Only a `platform-admin` drives a rollout of a platform runtime.
- **Schema migrations.** The `drainFirst` and `schemaVersion` fields of §10.5 move to the runtime rollout unchanged.
- **Events.** Each phase transition emits the existing `upgrade_progressed` event with the runtime, the revisions, and their weights. The `RuntimeUpgrade` alerts and metrics move from pool labels to runtime labels.

## RV-5 Derived runtimes

- **What a derived runtime carries.** §5.1 forbids a derived runtime to set `image`, `command`, `env`, `integrationLevel`, `requireSoPeercred`, `type`, `executionMode`, `isolationProfile`, and any `capabilities` field, as §5.1 "Example B" states and `validateDerivedRuntime` enforces (D18). §5.1's merge table gains the Prohibited rows it lacks for `capabilities.preConnect` and `capabilities.midSessionUpload`, so the table matches Example B. It lets a derived runtime override `credentialCapabilities` and `agentInterface`, and append to `sharedAssets` and `workspaceDefaults`. A platform-owned derived runtime may replace `sdkWarmBlockingPaths`, which keeps §6.1's guidance that a derived runtime whose `workspaceDefaults` carry `CLAUDE.md` sets `[]` (D16). `Merge` takes the base's list today, and F-6.1.29 records it. Tenant-derived runtimes may narrow `credentialCapabilities` and append `workspaceDefaults` under their grant, and [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md) lets a tenant set `checkpointExcludes` on a derived runtime. A derived runtime therefore carries values for versioned fields, kept on its own runtime row.
- **Merge.** A session on a derived runtime chooses a revision from the base's traffic, using the base's name in the hash, and merges the derived runtime's fields over that revision's fields under §5.1's merge algorithm. An override replaces the revision's value, and an append adds to it. The merged result is what the post-chain point and the pod builder read. A derived runtime's pod-affecting contribution, its `sharedAssets` append, enters the pod-set key of "RV-3" rather than any revision, so a change to it replaces the derived runtime's pod sets without creating a base revision. `runtimeOptionsSchema` is the exception: it does not merge, and "RV-7" validates a session's options against the base revision's schema and the derived runtime's schema separately.
- **Why no derived revisions (D5).** The binary comes from the base, so the base's traffic alone decides which binary a session runs. A derived runtime's own fields change in place, and a write to them applies to the next session on every base revision, as a live field does.
- **Narrowing holds against every base revision.** A restrict-only or narrowing value, such as a narrowed `credentialCapabilities.proxyDialect` or a subset of `supportedProviders`, has to hold against every revision in the base's traffic, because any of them can serve the next session. A write to the derived runtime checks every revision in the traffic, and a new base revision is checked against every derived runtime.
- **Options schemas across base revisions.** A base revision that drops a property a derived schema names leaves that option unusable on the sessions that revision serves, because the base schema refuses it, and the derived runtime stays compatible. A derived schema that lists a property in `required` while some revision in the base's traffic forbids that property would refuse every session on that revision, so the tenant-derived runtime reads as `incompatible` under DR-6, and DR-6's notice names the property and the revision. A revision forbids a property when its schema gives the property the schema `false`, or omits it from `properties` under `additionalProperties: false`. A conflict of any other form surfaces as `RUNTIME_OPTIONS_INVALID` at session creation.
- **Pools.** Pools on a derived runtime, including owned pools under [DR-5](tenant-derived-runtimes-design.md#dr-5-owned-pools), keep a pod set per distinct pod hash among the active base revisions during a base rollout, sized as "RV-3" states, with the owned-pool exemption of D8. Each pod set's key also covers the derived runtime's and the pool's contributions.
- **Base writes and platform-owned derived runtimes.** A base write that creates a revision invalidating a platform-owned derived runtime is refused and names the runtime, as §5.1 refuses such a change today.
- **Base writes and tenant-derived runtimes.** A base write always succeeds under [DR-6](tenant-derived-runtimes-design.md#dr-6-base-changes). A tenant-derived runtime is evaluated against every revision in the base's traffic, the new one included, and reads as `incompatible` while any of them breaks it. New sessions then answer `409 DERIVED_RUNTIME_UNAVAILABLE` until the tenant edits the runtime or the breaking revision leaves the traffic. DR-6's notice fires when the write creates the base revision, and its text names the revision, the rollout policy, and whether the revision makes the runtime `incompatible`. The notice no longer means the change has already reached the tenant's sessions.

## RV-6 Sessions over time

- **Retry and resume.** A retried or resumed session claims from the pod set of its recorded revision's pod hash while that pod set exists, which includes a pod set the revision shares with an active revision. A revision does not leave the registry while a live session references it. A resumable session can outlive `rollout.drainTimeoutSeconds`, and when its pod set is gone, it resumes on the revision with the highest weight, the gateway updates `runtimeRevision`, and it emits `session.runtime_revision_changed` (D7).
- **Stored plan on a revision change (D19).** A resume that moves to another revision under D7 keeps the session's stored plan and its stored `checkpointExcludes` list, and does not compose a plan from the new revision's `workspaceDefaults`. A session with a checkpoint restores it under the new binary. A session with neither a checkpoint nor a minimal state record takes the snapshotless rebuild of [§7.3](../spec/07_session-lifecycle.md#73-retry-and-resume) step 3e, which materializes the stored plan on the replacement pod and runs the plan's setup commands under the new binary. A setup command that fails there fails the resume with `setup_command_failed`, as §7.3 states for a failure in step 3e.
- **Options on a revision change.** A resume that moves to another revision under D7 validates the session's stored `runtimeOptions` against the new revision's schema, as "Validation" under "RV-7" states. When the schema refuses them, the resume fails, the session records the reason `runtime_options_invalid` with the new revision and the validation report, and the session's snapshot is kept. The recovery path is a derive `from: latest` with a `runtimeOptions` override ([`session-derive-design.md`, "Request"](session-derive-design.md#request)), which the new revision's schema validates at the post-chain point. "Options the new revision refuses" under "RV-4" reports the sessions at risk before `Contracting`.
- **Delegation.** A delegated child chooses a revision of its own runtime by "RV-2". Under `rollout.key: session`, the key is the root session ID, so a delegation tree stays on one revision of each runtime it uses. The delegation `TaskSpec` carries `runtimeOptions` for the child, which "RV-7" validates against the child's revision.
- **Derive.** A derive chooses a revision by "RV-2" like a create. It does not inherit its source's revision (D2). Its options are the source's or the request's, validated against the chosen revision ("RV-7").

## RV-7 Session options

### Schema storage

- **Inline on both paths.** The `Runtime` resource carries `runtimeOptionsSchema` inline, as the admin API does, in place of `runtimeOptionsSchemaRef`. The platform has no deployments, so the rename has no compatibility shim. The RuntimeReconciler mirrors the inline schema as it mirrors the other fields, and a mirror write passes the checks of a base write that the admin API applies, `derivedRuntimesInvalidatedBy` included.
- **Reference schemas as files.** The schemas of the built-in reference runtimes, which exist today only inline in §14, become files under `schemas/runtime-options/`, one per runtime, such as `schemas/runtime-options/claude-code.json`. `schemas/embed.go` widens its embed pattern to that directory, so the binaries carry them, and the chart renders each file into its reference runtime's `Runtime` resource.
- **No fetch.** No component fetches a schema URL. The compiler has no loader, so a `$ref` that leaves the document does not resolve, and the schema fails to compile.
- **Compiled at write.** Every write compiles the schema and refuses one that does not compile: a platform write with `400 VALIDATION_ERROR` naming `runtimeOptionsSchema`, and a tenant's write with `422 INVALID_DERIVED_RUNTIME` and `details.field`. A tenant-written derived schema is also refused when it carries `$schema`, `$id`, or a `$ref` other than a local `#/...` reference ([`tenant-derived-runtimes-design.md`, "Fields"](tenant-derived-runtimes-design.md#fields)). A platform-shipped file may carry `$id`, which the platform strips before it compiles the schema.
- **Stored by digest.** The revision stores the schema content once per digest and holds the digest, which the revision's hash covers ("Record"). "Retention" deletes schema content that no revision references. The gateway caches compiled schemas by digest, so session creation compiles none.

### Validation

The size and syntax checks stay early, and schema validation runs at the post-chain point and on a resume that changes revision:

- **Early.** At request validation, the gateway applies the 64 KB size cap and parses the JSON, as it does today. These checks need no runtime.
- **The post-chain point.** On every creation kind, the gateway validates the session's options against the chosen revision's schema: a create, a create-and-start (whose request gains the `runtimeOptions` field), a derive, and a delegated child. The mid-chain validation in `validateAndBuildCreateRow` is removed.
- **A resume that changes revision.** A resume that moves to another revision under D7 validates the stored options against the new revision's schema ("RV-6").

The rules wherever the schema validation runs:

- On a derived runtime that carries its own schema, such as a tenant-derived runtime, the gateway compiles the base revision's schema and the derived schema as separate resources, and both must accept the options. This replaces §5.1's override-replace merge, under which the derived schema took the base's place. §5.1's rule that a derived schema names only the base's properties is dropped, together with the subset paragraph of §14 that answers `INVALID_DERIVED_RUNTIME` for it, because a derived schema can only add constraints when both schemas validate.
- A request with no options is validated as an empty object, so a schema's `required` refuses it.
- A runtime and revision with no schema accept any options and emit `RuntimeOptionsUnschematized`, as §14 states today.

### Refusal

A refusal answers `400 RUNTIME_OPTIONS_INVALID` with the validation report, as today, and `details.runtimeRevision`. When the `PreRoute` chain moved the session to another runtime, the refusal also carries `details.runtime` and `details.interceptor_ref`, which names the last modifier. The counter `lenny_runtime_options_refused_total`, labelled `runtime` and `runtime_revision`, counts refusals, so an operator sees a rollout that refuses clients' options.

### Delivery

The adapter delivers the validated options in `session_start`, beside `runtimeRevision`, and the `runtimeOptions` field of the pod-scoped adapter manifest is removed. The Go, Python, and TypeScript runtime SDKs read the options from `session_start`. A resume rewrites the frame with the session's stored options, so a resumed runtime receives what was validated. The options an interceptor sees cannot change, because [`runtime-routing-design.md`, "RT-2 The interceptor contract"](runtime-routing-design.md#rt-2-the-interceptor-contract) makes `runtimeOptions` immutable in the `PreRoute` and `PostRoute` payloads (RVR-12).

### Delegation

The delegation `TaskSpec` gains `runtimeOptions`, which the child session stores. The gateway validates them at the post-chain point against the child's chosen revision, and the parent's `DelegationPolicy` may refuse them. §8.2's statement that §14 defines the full session creation schema, `runtimeOptions` included, then holds for the `TaskSpec` as well.

## Worked example: an upgrade on AWS

An operator offers one runtime, `claude-worker`, to every customer and runs it on Amazon EKS in `us-east-1`, with Karpenter provisioning nodes. The operator ships a new image and wants no customer to notice. The figures are **[estimate]**: they follow from the assumptions below and from approximate AWS list prices, which "Validation owed" lists for checking.

| Assumption | Value |
| :-- | :-- |
| Pools | One pool for `claude-worker`. |
| Agent pod request | 1 vCPU and 4 GiB, for the runtime and the adapter together. |
| Node type | `m7i.4xlarge` (16 vCPU and 64 GiB) at about $0.81 an hour on demand, holding about 14 agent pods after DaemonSets. |
| Peak load | 2,000 live sessions and a warm target of 300 pods, on about 165 nodes, which cost about $133 an hour. |
| Off-peak load, at 03:00 UTC | 400 live sessions and a warm target of 60 pods, on about 33 nodes. |
| Image | 3 GB compressed, in Amazon ECR. |
| `maxSessionAge` | 7,200 seconds, the default of §11.3. |
| Warm-up time | 5 to 10 minutes for a pod set, covering node launch, the image pull, and the runtime's start. |

### Default rollout at off-peak

The runtime keeps the default `rollout.policy: automatic`.

1. The operator writes the new image with `PUT /v1/admin/runtimes/claude-worker`. The write changes a versioned field, so it creates revision 8 at weight 0 and starts a rollout in `Pending`.
2. Revision 8's pod set warms to the full warm target of 60 pods. Karpenter adds about 5 nodes, and each new node pulls the image once. No session reaches revision 8 yet.
3. The rollout enters `Expanding` and gives revision 8 the full weight. New sessions claim revision 8's warm pods. Revision 7's weight is 0, so its warm target is 0 and its 60 idle pods are deleted.
4. In `Draining`, the roughly 400 sessions on revision 7 continue until they end. A resumed session returns to revision 7 while its pod set exists. No session outlives `maxSessionAge`, and `drainTimeoutSeconds` defaults to it, so the drain ends within 2 hours and ends no session early. Nodes carry no revision, so revision 8's pods fill the capacity that revision 7's sessions release.
5. `Contracting` deletes revision 7's pod set, and Karpenter consolidates the nodes that are left underused.

The operator follows the rollout with `lenny-ctl admin runtimes rollout status` and can run `rollback` until `Contracting`.

| Temporary cost | Off-peak | Peak, for comparison |
| :-- | :-- | :-- |
| Second warm pod set during the warm-up, about 15 minutes | 5 nodes, about $1 | 22 nodes and 352 vCPU, about $4.50 |
| Underused nodes during the drain, before consolidation | about 3 nodes for an hour, about $2.50 | about 14 nodes for an hour, about $11 |
| Image pulls, one per node | about 100 GB | about 500 GB |
| Image pulls through a NAT gateway, at $0.045 per GB | about $5 | about $22 |
| Image pulls through an S3 gateway endpoint | $0 | $0 |
| ECR storage for the new image | about $0.30 a month | about $0.30 a month |
| Total | about $4 to $9 | about $15 to $40 |

The rollout costs less than an hour of the fleet. Its largest item is the second warm pod set during the warm-up, which scales with the warm target. The warm target follows demand, so a rollout at off-peak needs the fewest extra nodes and is the least exposed to an EC2 capacity shortage.

### Canary rollout

An operator who wants to watch the new revision on a small share of users first sets `rollout.policy: manual` and `rollout.canaryPercent: 5` before writing the image.

1. In `Pending`, revision 8's pod set warms to 5% of the warm target: 3 pods at off-peak, or 15 pods on 1 or 2 extra nodes at peak.
2. `proceed` gives revision 8 a weight of 5. Under the default `rollout.key: user`, the same users reach revision 8 for as long as the weights hold. The operator compares session failures and latency by the `runtime_revision` label.
3. `set-weight 25`, `set-weight 50`, and `promote` complete the rollout. Each step warms revision 8's larger share before the weight moves ("Warm before weight"), and the warm target is split by weight, so the warm pod count stays near the target throughout. The extra cost is about $1 to $2. A `promote` skips the warm-up of its own step, so the operator promotes once revision 8 already holds most of the weight.

### A schema-only fix

After revision 8 completes, the operator finds that its `runtimeOptionsSchema` caps `maxTokens` at 4,096 where the binary accepts 32,000, and writes the corrected schema.

1. The write changes a gateway-read field alone, so it creates revision 9 with revision 8's pod hash. Revision 9 shares revision 8's pod set.
2. Under `automatic`, the rollout moves revision 9 to the full weight at once, because the shared pod set already holds the warm target. Karpenter adds no node and no node pulls an image.
3. `Draining` ends no session, because revision 8's pod set stays as revision 9's, and `Contracting` deletes nothing.

The fix costs no warm-up: no extra node, no image pull, and no cold start. Under `manual` with `canaryPercent: 5`, the weights still apply, so 5% of users have their options validated against revision 9's schema while every session claims from the one pod set.

### Effect on customers

- A running session stays on its revision until it ends, so the rollout interrupts no session.
- A new session claims a warm pod of the revision it is sent to, so the rollout adds no cold start.
- A rollback after revision 7 reached weight 0 starts new sessions cold for the warm-up time.
- An `immediate` write or a promote moves traffic before the new pods are warm, so new sessions start cold for the warm-up time. They exist for urgent fixes.

### Preparation

- An S3 gateway endpoint lets nodes download ECR image layers at no data charge, and ECR interface endpoints cost about $0.01 an hour for each availability zone. Without them, each node pulls the image through the NAT gateway.
- The Karpenter `NodePool` limits, the EC2 vCPU service quota, and the agent namespace's `ResourceQuota` each need room for twice the warm target during the warm-up: 352 vCPU at the peak in the example.
- A rollout at off-peak keeps the temporary footprint smallest.

## Limits

- **Pods during a rollout.** A pool holds a pod set per distinct pod hash among the active revisions, so a rollout that changes a pod-affecting field raises the warm pod count until `Contracting`. A rollout that changes only gateway-read fields adds no pods.
- **Tenant-derived pools multiply the cost.** A base rollout that changes a pod-affecting field gives every pool on every derived runtime of that base a pod set per distinct pod hash. On owned pools the platform carries that cost outside the tenants' budgets (D8), bounded by the namespace `ResourceQuota`.
- **Cold starts on owned pools.** An owned pool whose new pod set does not warm within `rollout.ownedPoolWarmTimeoutSeconds` serves its first sessions on the new revision cold.
- **A breaking base revision stops tenant runtimes during the canary.** A tenant-derived runtime that one revision in the traffic breaks reads as `incompatible` for the whole rollout, including while that revision holds a small weight.
- **No revision choice.** A client cannot send a session, or a derive, to a specific revision (D2). A regression test against a new revision runs once the rollout gives the test's rollout key that revision.
- **Live fields are not staged.** A change to a live field reaches every revision at once. A live-field change that a new binary needs has to stay compatible with the old revision while both run.
- **A retired revision moves resumable sessions.** A session suspended longer than the drain timeout resumes on another revision (D7), and that resume fails when the new revision's schema refuses the session's options. The client recovers with a derive that replaces the options.
- **Options depend on the revision during a rollout.** A client sent to the new revision has its options validated against the new schema, so a renamed or removed option refuses the clients the new revision serves while the old revision accepts it. Discovery shows each caller the schema of the revision it would be assigned only under `rollout.key: user`.
- **A blocking-path change can turn a shared pod set pod-warm.** A revision that changes only `sdkWarmBlockingPaths` shares its pod set, and a demotion rate above the circuit breaker's threshold on that revision's sessions sets `spec.sdkWarmDisabled` for every revision on the pool.
- **A floor raise waits for the pools.** A write that raises a runtime's isolation floor is refused until no weaker pool references the runtime, so the operator moves or deletes those pools first.
- **Tenants lose two levers.** A tenant can no longer turn SDK-warm on through an override, and it cannot widen `interaction` or `injection` or remove a blocking path. It turns SDK-warm off with a blocking path on a shared pool or with `sdkWarm: disabled` on an owned pool.
- **A warm-list change replaces an owned pool's pod set.** A change to a tenant's warm entries or `sdkWarm` creates a pod set outside any rollout, and the earlier pod set's pods count in the tenant's charge until they leave.
- **One rollout at a time.** A runtime has at most one rollout in progress. A non-urgent change during a canary waits for a rollback or completion, and an `immediate` change ends the canary.
- **Cold starts under `immediate`.** Sessions created before the new pod sets are warm wait for pod creation.
- **Revision history costs storage.** "Storage and performance" estimates it, and a CI pipeline that writes an image on every build produces the largest case.

## Alternatives rejected

- **Finish the pool-level `RuntimeUpgrade` as specified.** It covers only the image, has no field to hold the image in `SandboxTemplate`, and lets pools of one runtime run different images with no runtime-level record.
- **Version by name with aliases.** `claude-worker` as a route to `claude-worker-v1` and `claude-worker-v2` breaks `baseRuntime` references and access rows, and needs routes across tenants.
- **Version every field.** A security tightening of `setupCommandPolicy` or `limits` would wait for a rollout, and a live-field change would warm a new pod set for nothing.
- **Let callers choose a revision.** The owner declined it (D2). It lets a caller step outside the rollout.
- **Substitute another revision when a pod set is empty.** It moves the split away from the weights under load and hides an undersized pod set.
- **Give derived runtimes their own revisions.** The binary comes from the base, so a derived revision would add a second traffic table that cannot change which image a session runs, and every base revision would multiply by every derived revision. A derived runtime's own values merge over the selected base revision instead (D5).
- **Count the rollout's extra pods in the tenant's budget.** A platform rollout would push a tenant at its bound into `QUOTA_EXCEEDED` for a change the tenant did not make (D8).
- **Keep `runtimeOptionsSchema` live.** Every running binary would have to accept every option the one live schema admits, so a binary that adds or renames an option could not ship its schema with it.
- **Validate options against every revision in the traffic.** A new revision that renames an option would refuse every client as soon as it entered the traffic, at any weight, including the clients the old revision still serves (D9).
- **Merge a derived schema over the base's.** A replacing derived schema can widen the base's to no constraint, and a name check does not prevent that. Separate validations against both schemas hold the base's constraints with no merge rule.
- **Fetch schemas by URL.** Session creation would depend on a remote host, and a `file:` reference reads the gateway's filesystem. The schemas ship in the binaries and the chart instead.
- **Key pod sets on the revision.** A schema fix would warm a full pod set and drain the old one, at the cost of a pod-affecting rollout, for a field no pod reads.
- **Version `isolationProfile`.** A pool is chosen before the revision, so a per-revision floor could not choose a pool, and a change to it would create a revision that changes no pod (D11).
- **Keep a per-tenant `preConnect` override.** The pod is SDK-warm or not before any session, so the override changes how the gateway binds a session and not the pod a shared pool built (D15).
- **Let a tenant replace `sdkWarmBlockingPaths`.** A tenant could remove a blocking path the binary needs, and a tenant's demotions would trip the circuit breaker of a pool other tenants share (D16).
- **Generate the agent card at write.** A stored card describes one revision, so during a rollout a caller assigned to another revision would read a card for a binary it does not run (D12).
- **Put a tenant's warm entries in the base revision.** A tenant's entries are per pool, and a base revision is shared by every tenant on the base, so the entries key the pod set instead (D13).

## Specification changes

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), opening, "Inheritance Rules", and "Normative Merge Algorithm" | Add revisions, the field classes with the pod-affecting and gateway-read kinds, the write rule, `rollout`, and the revision record with the pod hash and the schema digest. Make `type` and `executionMode` fixed. Replace "Base runtime mutability" with the field classes. State that a derived runtime's fields merge over the selected base revision, that a narrowing holds against every revision in the base's traffic, and that the refusal of an invalidating base write applies to platform-owned derived runtimes. Replace the `runtimeOptionsSchema` merge row, an override with a property-name subset rule, with the separate validations of "RV-7" against both schemas, and state that a schema is compiled at write and refused when it does not compile. |
| §5.1, the paragraph **Capabilities are customizable per tenant** | State the per-tenant capability override of "Per-tenant capability override": it may only narrow `capabilities.interaction` and `capabilities.injection`, it carries no `capabilities.preConnect`, and it appends to `sdkWarmBlockingPaths` without removing an entry. This change goes through the proposal pipeline with the rest of this table. |
| §5.1 "Inheritance Rules" and "Normative Merge Algorithm" | Add the Prohibited rows for `capabilities.preConnect` and `capabilities.midSessionUpload`, so the merge table matches "Example B", under which a derived runtime sets no `capabilities` field. State that a platform-owned derived runtime may replace `sdkWarmBlockingPaths`. State that a derived runtime's `sharedAssets` append enters the pod-set key rather than a revision. |
| §5.1 **`agentInterface` Field** and **`publishedMetadata` Field** | State that the gateway generates the agent card on read from the selected revision's `agentInterface`, merged with a derived runtime's override, and caches it by revision. Drop the statement that the card is generated at write time and stored as an opaque `publishedMetadata` entry, and remove `POST /v1/admin/runtimes/regenerate-cards`. |
| [§5.3 Isolation Profiles](../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles) | State the meaning of a runtime's `isolationProfile` as the isolation floor of "Isolation floor": pool admission and the WarmPoolController refuse a pool below it, as they do for `acknowledgeNonceOnlyAuth`, pool creation and a session that names no profile are raised to it, and a write that raises it is refused while a weaker pool references the runtime. |
| [§6.1 What a Pre-Warmed Pod Looks Like](../spec/06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like), **Circuit-breaker for SDK-warm pools.** | State that demotions forced by a tenant's appended blocking paths are counted apart from the pool's demotion rate. State that an owned pool's `sdkWarm: disabled` keeps its pods pod-warm. |
| [§17.6 Packaging and Installation](../spec/17_deployment-topology.md#176-packaging-and-installation), item 5 | Restate the bootstrap block on a runtime's `isolationProfile` as the isolation floor: `--force-update` never changes it, and the admin API changes it under §5.3's refusal. |
| [§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) and [§4.6.2](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration) | Add pod sets per pod-set key, the derived runtime's and the pool's contributions to the key, the owned pool's `sdkWarm: disabled` setting, the warm split by summed weight, the warm list per pool and pod set, and pool admission against every revision in the traffic. |
| [§4.6.1](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle) | The warm pool controller keeps a template and warm pool pair per pool and distinct pod-set key, skips a quarantined Sandbox when it deletes a pair, refuses to build from a template whose profile is below its runtime's isolation floor, and the pod builder reads the revision the template names. |
| [§4.7.6](../spec/04_system-components.md#476-adapter-manifest-field-reference) and [§28.5.3](../spec/28_communication-channels.md#2853-intra-pod) | Add `runtimeRevision` to the per-session fields, and `runtimeRevision` and `runtimeOptions` to `session_start`. State that a resume rewrites `session_start` with the stored options. |
| [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow) | Add revision selection after the `PreRoute` chain and before the post-chain point, and state that the post-chain point reads versioned fields from the chosen revision and validates `runtimeOptions` against its schema. |
| [§7.3 Retry and Resume](../spec/07_session-lifecycle.md#73-retry-and-resume) | Add the revision rule of "RV-6", and the failure of a resume onto a revision whose schema refuses the stored options, with its recorded reason and the kept snapshot. State in step 3e that a resume that moves revision keeps the stored plan and rebuilds it under the new binary, with a setup failure reported as `setup_command_failed`. |
| [§8.2 Delegation Mechanism](../spec/08_recursive-delegation.md#82-delegation-mechanism) | Add `runtimeOptions` to the delegation `TaskSpec`, validated at the post-chain point against the child's revision and subject to the `DelegationPolicy`, so the citation of §14 for the session creation schema holds for the `TaskSpec`. |
| [§14 Workspace Plan Schema](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema), the `runtimeOptions` field notes, and [§14.1](../spec/14_workspace-plan-schema.md#141-workspaceplan-schema-versioning), **Outer-envelope validation of `CreateSessionRequest` fields.** | State that options are validated at the post-chain point against the chosen revision's schema, and against a derived runtime's schema separately, that absent options are validated as an empty object, and that a refusal carries `details.runtimeRevision`. Move the built-in schemas to `schemas/runtime-options/` with a pointer from §14, remove the `https://schemas.lenny.dev/runtime-options/` URLs, and remove the paragraph that refuses a derived schema's new property names with `INVALID_DERIVED_RUNTIME`. |
| [§26](../spec/26_reference-runtime-catalog.md) | State that each reference runtime carries its schema inline, rendered from its file under `schemas/runtime-options/`, in place of a URL. |
| [§27.4 UI surface](../spec/27_web-playground.md#274-ui-surface) | State that the session configuration form uses the schema of the revision discovery reports for the caller. |
| [§10.5](../spec/10_gateway-internals.md#105-upgrade-and-rollback-strategy) | Replace the pool-level `RuntimeUpgrade` with the runtime rollout of "RV-4". |
| [§11.2.1 Billing Event Stream](../spec/11_policy-and-controls.md#1121-billing-event-stream) | Add `runtime_revision`. |
| [§12.2](../spec/12_storage-architecture.md#122-storage-roles) | Add the revision table, the asset and schema content tables, and the retention rule with its flag. |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api) | Add the revision, rollout, promote, and revision delete endpoints, `?rollout=immediate` on runtime writes, `runtimeRevision` on session responses and discovery entries, the per-caller revision rule of "Discovery", `runtimeOptions` on create-and-start, `details.runtimeRevision`, `details.runtime`, and `details.interceptor_ref` on `RUNTIME_OPTIONS_INVALID`, the suspended-session count in rollout `status`, `ROLLOUT_IN_PROGRESS`, and `REVISION_EXPIRED`. Remove the pool upgrade endpoints. State that `agentInterface` on `GET /v1/runtimes` and the agent card at `GET /v1/runtimes/{name}/meta/agent-card` follow the per-caller revision, and remove `POST /v1/admin/runtimes/regenerate-cards`. |
| [§15.5 API Versioning and Stability](../spec/15_external-api-surface.md#155-api-versioning-and-stability), the `Runtime` resource | Add `rollout` to the spec, replace `runtimeOptionsSchemaRef` with the inline `runtimeOptionsSchema`, and add `latestRevision` and the traffic to the status. The proposal's verifier confirms that this is where the resource's schema is stated, or finds the section that states it. |
| [§15.7 Runtime Author SDKs](../spec/15_external-api-surface.md#157-runtime-author-sdks) | State that `CreateRequest.RuntimeOptions` comes from `session_start`. |
| [§16.1](../spec/16_observability.md#161-metrics), [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos), and [§16.6](../spec/16_observability.md#166-operational-events-catalog) | Add the `runtime_revision` label, `lenny_runtime_options_refused_total`, `session.runtime_revision_changed`, the runtime labels on the rollout metrics and alerts, and a counter of the SDK-warm demotions that tenant-appended blocking paths force, labelled by pool, which the circuit breaker does not read. |
| [§24](../spec/24_lenny-ctl-command-reference.md) | Replace `lenny-ctl admin pools upgrade` with `lenny-ctl admin runtimes rollout`. |

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/gateway/runtime/runtimestore` | The revision table, the hash and the pod hash, the schema content table by digest, traffic, and rollout state. |
| `pkg/controller/runtime` | The hash on mirror, the inline schema in place of `runtimeOptionsSchemaFromRef`, the base-write checks of `derivedRuntimesInvalidatedBy` on mirror, and `latestRevision` and traffic in the resource status. |
| `pkg/apis/lenny/v1alpha1` | `podHash` and `runtimeRevision` on `SandboxTemplate`, the status fields on `Runtime`, `rollout` on the spec, and `runtimeOptionsSchema` in place of `runtimeOptionsSchemaRef`. Regenerate the CRD manifests. |
| `schemas/runtime-options/` and `schemas/embed.go` | One schema file per reference runtime, taken from §14, and the embed pattern widened to the directory. |
| `charts/lenny` | `templates/reference-runtimes.yaml` and the reference runtime values render each schema file inline. Regenerate `pkg/embedded/manifests/manifests.yaml`. |
| `pkg/gateway/externalapi/admin` runtime writes | Compile the schema at write, refuse a schema that does not compile, refuse `$schema`, `$id`, and a non-local `$ref` on a tenant-written schema, and strip `$id` from a platform-shipped file. |
| `pkg/controller/sandbox` and `podspec` | Read pod-affecting fields from the revision the template names. |
| `pkg/controller/warmpool` and `pkg/controller/poolscaling` | Pod sets per pod hash, the warm split by summed weight, the warm list per pod set, the owned-pool exemption of D8, and the quarantine skip. |
| `pkg/gateway/sessionserver` post-chain point | The checks that read versioned fields read the chosen revision. Options validation moves out of `validateAndBuildCreateRow` to the post-chain point, with the compiled-schema cache by digest, the separate validations against both schemas on a derived runtime, the empty-object validation of absent options, the refusal details, and the refusal counter. |
| `pkg/gateway/sessionserver` resume and `pkg/gateway/mcpfabric` delegation | Options validation on a resume that changes revision, with the recorded reason, and `runtimeOptions` on the delegation `TaskSpec`. |
| `pkg/gateway/sessionserver` discovery and `pkg/gateway/playground` | The per-caller revision of "Discovery" and `runtimeRevision` on each entry. |
| `pkg/gateway/upgrade/runtimeupgrade` and `pkg/runtime/upgrade/state` | Runtime-level rollout on traffic. |
| `pkg/gateway/sessionserver` and `pkg/gateway/mcpfabric` | Revision selection, `runtimeRevision` on the record, resume by revision, and billing. |
| `pkg/gateway/externalapi/admin` and `pkg/ctlcli` | The revision and rollout endpoints and commands. |
| `pkg/adapter`, `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, and the runtime SDKs | `runtimeRevision`, and `runtimeOptions` in `session_start`, rewritten on resume. The Go, Python, and TypeScript SDKs read the options from `session_start`, and the manifest's `runtimeOptions` field is removed. |
| `migrations` | The revision table, `sessions.runtime_revision`, and the rollout state. |
| `pkg/gateway/runtime/runtimestore` (`capoverride.go`) and `pkg/gateway/runtime/runtimecapoverride` | The override drops `PreConnect`, appends `SDKWarmBlockingPaths` rather than replacing it, and refuses a widening of `interaction` or `injection` at write, checked against every revision in the traffic. |
| `pkg/gateway/sessionserver` (`envelope.go`) and `pkg/gateway/podlifecycle/podsession` | `runtimeSDKWarm` reads `preConnect` from the revision of the claimed pod's pod set, and the demotion decision marks a demotion that only a tenant-appended pattern forced. `runtimeIntegrationLevel` reads the session's revision, and `integrationVerified` keys on the runtime, the pod hash, and the declared level. |
| `pkg/gateway/session/recycle` | `resolveRuntime` reads `preConnect` from the revision that the pod's `SandboxTemplate` names in `runtimeRevision`. |
| `pkg/controller/poolscaling` | The circuit breaker leaves tenant-forced demotions out of its rate. The pod-set key covers the derived runtime's and the pool's contributions, including an owned pool's `sdkWarm` setting. |
| `pkg/gateway/externalapi/admin` pool writes and `pkg/controller/warmpool` | Pool admission refuses a pool below its runtime's floor, pool creation defaults to the stronger of the platform default and the floor, a runtime write that raises the floor is refused while a weaker pool references it, and the WarmPoolController refuses a template below the floor. |
| `pkg/gateway/sessionserver` create paths | A session that names no profile is raised to its final runtime's floor before pool resolution. |
| `pkg/gateway/runtime/agentcard`, `pkg/gateway/externalapi/admin`, and `pkg/ops/mcp` | Generate the card on read with a cache by revision, remove the write-time generation and the `regenerate-cards` route, and regenerate the `lenny-ops` tool list. |
| `docs/` | An operator guide page on revisions and rollouts, linked from the routing guide of [`runtime-routing-design.md`](runtime-routing-design.md). |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 1 | Field classification and the hash, and a write changing `type` or `executionMode` refused. A live-field write creates no revision. Selection shares and stability per key, and the single-revision shortcut (RVR-3). The resume rule for a retired revision. The retention job keeps referenced revisions and deletes unreferenced ones past the period, and an early delete of a referenced revision is refused. An `immediate` write during a rollout ends it. A derived runtime's override and append merge over the selected base revision, and a narrowing that one revision in the traffic breaks is refused at the derived write and reads as `incompatible` on a base write. The charge of an owned pool during a base rollout leaves out the extra pod set (D8). Two revisions that differ only in `runtimeOptionsSchema`, `credentialCapabilities`, `capabilities.interaction`, `sdkWarmBlockingPaths`, `minPlatformVersion`, `checkpointExcludes`, or `agentInterface` have one pod hash, and two that differ in `image` or `capabilities.preConnect` have two (RVR-13). A write that changes only `isolationProfile` creates no revision. A change to a derived runtime's `sharedAssets` append, a pool's warm entries, or an owned pool's `sdkWarm` changes the pod-set key and creates no revision (D13). A pool create with no profile takes the stronger of the platform default and the floor, a pool below the floor is refused at create and at update, a floor raise is refused while a weaker pool references the runtime and names it, a floor lowering passes, and a session that names no profile is raised to the floor (RVR-14). A tenant override that sets `preConnect`, raises `interaction` to `multi_turn`, sets `injection.supported` to `true` over `false`, or adds an injection mode is refused, and a narrowing passes (RVR-15). A tenant's blocking paths append to the revision's list and cannot remove an entry, and a demotion that only a tenant pattern forces is left out of the breaker's rate and counted apart. A derived payload that carries any `capabilities` field is refused, and a platform-owned derived runtime's `sdkWarmBlockingPaths: []` reaches the merged result. `integrationVerified` probes again for a new pod hash or a new declared level and not for a revision that shares both with a verified one. The agent card is generated from the selected revision's `agentInterface`, merged with a derived override, and its cache keys on the revision. The warm split sums the weights of the revisions sharing a pod set. A schema that does not compile is refused at write. A tenant schema with `$schema`, `$id`, a remote `$ref`, or a `file:` `$ref` is refused, a local `#/...` `$ref` passes, and a platform file's `$id` is stripped before compiling. A schema write stores one content row per digest, and the retention job deletes unreferenced schema content. Options validate against the chosen revision's schema and, on a derived runtime, against both schemas, with a value the base allows and the derived schema refuses refused and the reverse refused too. Absent options meet a schema's `required`. Discovery under `rollout.key: user` reports the revision the caller's HMAC selects, and otherwise the highest-weight revision, the older on a tie. A derived schema that requires a property one revision in the traffic forbids reads as `incompatible`, and one that only names it stays compatible. |
| 2 | Against envtest: a `Runtime` resource change creates a revision and leaves existing pods' templates unchanged (RVR-1), the warm pool controller creates and deletes pod sets per pod hash, a schema-only change creates no pod set, and the resource status reports the traffic. The RuntimeReconciler mirrors an inline schema, refuses a mirror that invalidates a platform-owned derived runtime, and creates no revision when it reconciles an unchanged resource. The WarmPoolController creates no pod from a `SandboxTemplate` whose profile is below its runtime's floor and records the refusal on the template. |
| 3 | `session_start` carries `runtimeRevision` and `runtimeOptions`, a resumed session's `session_start` carries the stored options, and the manifest carries `runtimeRevision` and no `runtimeOptions`. The Go, Python, and TypeScript SDKs expose the options from `session_start`. The rollout endpoints, the discovery entry's `runtimeRevision`, and the `RUNTIME_OPTIONS_INVALID` details. The chart renders each reference runtime with its schema inline, and each file under `schemas/runtime-options/` compiles. |
| 4 | On the compose stack: sessions split by weight across revisions, a delegated child under `rollout.key: session` matches its root, a derive selects by traffic, a resume stays on its revision, billing events and metrics carry it, and a live-field change reaches both revisions at once (RVR-2, RVR-5, RVR-7, RVR-8). A session on a derived runtime runs a base revision (RVR-6). With revision 6 at `deploymentModel: sidecar` and revision 7 at `embedded`, a delegated child with exports passes on revision 6 and is refused on revision 7, and a session on a runtime whose revisions differ in `capabilities.deferredToolDiscovery` takes the link check of its chosen revision (RVR-9). With revision 7 accepting option `model` and revision 8 renaming it, a session sent to revision 8 with `model` is refused with `RUNTIME_OPTIONS_INVALID` naming revision 8 and counted under that `runtime_revision`, and one sent to revision 7 passes, on a create, a create-and-start, a derive, and a delegated child (RVR-12). A route that moves a session to another runtime yields a refusal naming that runtime and the last modifier. A child whose `TaskSpec` options the `DelegationPolicy` refuses is refused. A suspended session whose revision is retired and whose options revision 8 refuses fails its resume with `runtime_options_invalid`, keeps its snapshot, and is counted in rollout `status` before `Contracting`, and a derive `from: latest` with replaced options recovers it. A session with no checkpoint whose revision is retired resumes on the new revision with its stored plan, runs the plan's setup commands under the new binary, and fails with `setup_command_failed` when one of them fails (D19). Discovery and the agent card at `GET /v1/runtimes/{name}/meta/agent-card` show revision 8's `agentInterface` to a caller assigned revision 8, and an unauthenticated read shows the highest-weight revision's. A tenant's `*` blocking path demotes every SDK-warm pod its sessions claim on a shared pool, and the pool's breaker does not trip on those demotions. |
| 5, owned pools and forensics | On Kind: a base rollout on a tenant at its `maxOwnedPods` bound warms the new pod set without a `QUOTA_EXCEEDED`, an owned pool that cannot warm moves after `ownedPoolWarmTimeoutSeconds`, a pod warming for one revision reaches only that revision's warm list, and `Contracting` leaves a quarantined Sandbox and its pair in place (RVR-10). |
| 5 | On Kind: an `automatic` rollout warms the new pod set before moving traffic, a `manual` `set-weight` holds the weight until the new pod set reaches its share and enters `Paused` with the weight unchanged when `warmTimeoutSeconds` passes (RVR-11), a `manual` rollout pauses and rolls back with the old pod set intact, `Contracting` deletes the old pod set only after its sessions end (RVR-4), and an `immediate` write or a promote sends the next session to the new revision before its pod set is warm. A schema-only rollout under `automatic` moves traffic with no new pod, and under `manual` at weight 5 sends that share of sessions to the new revision while every session claims from the one pod set, and `Draining` ends none of them (RVR-13). |
| 5, isolation and SDK-warm | On Kind: a `SandboxWarmPool` and `SandboxTemplate` applied with `kubectl` below the runtime's floor produce no pod. An owned pool set to `sdkWarm: disabled` replaces its SDK-warm pods with pod-warm ones without a base revision. The recycle re-warm of a pod reads `preConnect` from its template's revision. |
| 7a | Concurrent creates during a weight change keep each key on one revision per traffic version, under `-race`. |
| 8 | A gateway restart during `Expanding` keeps the traffic and the phase. |
| 9 | A tenant's capability override cannot widen `interaction` or `injection`, cannot set `preConnect`, and cannot remove a platform blocking path, and a pool below its runtime's floor serves no session on any creation path. |
| 11 | The rollout guide's commands match the CLI, and the rollout alerts resolve to runbooks. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Confirm that no controller replaces warm pods whose image differs from the `Runtime` resource | "Current state" infers a mixed pool from the absence of such code. | Open. |
| Confirm how a runtime registered only through the admin API reaches the pod builder, which reads the `Runtime` resource | "RV-3" moves the pod builder to the revision, and both registration paths must produce one. | Open. |
| Review where the gateway reads each versioned field, and settle `runtimeOptionsSchema` | O2 asked it. The sync review found readers of `capabilities.deferredToolDiscovery`, `deploymentModel`, `type`, and `requireSoPeercred` before selection, which "RV-2" moves to the post-chain point. | Settled by D9. "Field classes" places each versioned field by its reader in the tree, read on 2026-11-09. |
| Confirm the placement of `isolationProfile`, `workspaceDefaults`, and `description` | Revision 6 placed `isolationProfile` and `workspaceDefaults` by judgement and kept `description` live, while D9 named descriptions among the gateway-read fields. | Settled for `isolationProfile` by D11, for `workspaceDefaults` by D19, and for `agentInterface` by D12. `description` stays live and is open, for the owner. |
| Confirm that the WarmPoolController can read a runtime's floor for a runtime registered only through the admin API | "Isolation floor" makes the WarmPoolController refuse a template below the floor, and it reads the `Runtime` resource today. | Open. |
| Confirm that the demotion decision can tell a demotion that only a tenant-appended pattern forced from one the revision's list forced | D16 counts the two apart, and `demotionReason` reports one matched pattern today. | Open. |
| Confirm that the recycle path can read the template's `runtimeRevision` for a pod it re-warms | D15 moves `resolveRuntime` from the pool's runtime row to the pod's revision. | Open. |
| Confirm that every session creation path can raise a session's profile to the floor before pool resolution, including a pinned pool and a derive | "Isolation floor" raises a session that names no profile. | Open. |
| Confirm that each built-in schema in §14 compiles as a standalone file with no `$ref` outside the document | "RV-7" moves them to `schemas/runtime-options/` and compiles every schema at write. | Open, for the proposal. |
| Confirm that the controller can render a warm list and the pre-bind reach per pool and revision | "RV-3" keys them on the revision, and the egress decision service reads them from the pool row today. | Open. |
| Measure the revisions table at a million rows: insert rate, the retention job's batch time, and listing latency | "Storage and performance" is an estimate. | Open. |
| Confirm that artifact references in `sharedAssets` resolve to a content digest that a revision can pin | "Field classes" pins them. | Open. |
| Confirm that resume claims by the session's recorded pool and can be extended to the revision | "RV-6" relies on it. | Open. |
| Measure the warm pod cost of a base rollout with tenant-derived pools | "Limits" states the cost without a number. | Open. |
| Check the AWS prices and measure the warm-up time in "Worked example: an upgrade on AWS" | The example uses approximate list prices and an assumed warm-up time of 5 to 10 minutes. | Open. |

## Instructions for reviewers of other designs

- [`runtime-routing-design.md`](runtime-routing-design.md): its reviewer confirms that RT-2 states the order of "RV-2" (the `PreRoute` chain, revision selection, the post-chain point on the chosen revision, then the claim), that `runtimeRevision` stays out of the routing labels, and that the bucketing function is shared. Its reviewer also confirms that `runtimeOptions` is immutable in the `PreRoute` and `PostRoute` payloads, and that the post-chain table carries a `runtimeOptions` row for every creation kind, as "RV-7" requires.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md): its reviewer:
  - restates DR-6's notice as firing when a base revision is created, and states that `baseStatus` and `incompatible` evaluate against every revision in the base's traffic;
  - drops the DR-6 rows for `type` and `executionMode`, which become fixed, and states that the rows for `deploymentModel` and `requireSoPeercred` evaluate per revision;
  - states that the grant conditions hold for every revision in the base's traffic;
  - adds pod sets per base revision to DR-5's owned pools, with `podRuntimeRef` (DR-2) naming the base and `runtimeRevision` the base revision;
  - adds to DR-5 "Bounds" and DR-10 the exemption of D8: a base rollout's extra pod set counts in neither the tenant's charge, nor the creation rule of `maxOwnedPods`, nor the pool's reserved pods, while the rollout is in progress;
  - keys the warm list under "Warm clones", its hosts, and the readable set before binding on pool and revision;
  - moves DR-8's delegation export refusal to the post-chain point on the chosen revision's `deploymentModel`;
  - restates DR-6's row for base `workspaceDefaults`, which is versioned, as applying to plans composed from a revision created after the write;
  - replaces the pool-level `upgrade` route and the §10.5 row in "Specification sections affected" with the runtime rollout, and the "Removes the base's `Runtime` resource" row with its effect on every revision in the traffic;
  - replaces the `runtimeOptionsSchema` row of "Fields", its subset-of-names rule, with the separate validations of "RV-7" against both schemas and the write refusals of a tenant schema, and adds the DR-6 row of "Options schemas across base revisions" under "RV-5".
  - states the `agentInterface` row of "Fields" as an override merged over the selected base revision, with the card generated on read (D12);
  - adds the owned pool's `sdkWarm: disabled` setting to "Template copy" as a tenant-settable, pod-affecting pool setting that enters the pod-set key (D15);
  - restates DR-8's "Per-tenant capability overrides" without `preConnect`, with `interaction` and `injection` restrict-only and `sdkWarmBlockingPaths` append-only (D15 to D17);
  - states under "Warm clones" that a tenant's warm entries enter the pod-set key, so a change to them replaces the owned pool's pod set without a base revision, under "Changing the list" (D13 and D19);
  - adds a DR-6 row for a base write that raises the isolation floor above a template pool's profile (D11).
- [`session-derive-design.md`](session-derive-design.md): its reviewer states that a derive selects a revision by traffic and does not inherit its source's revision, and that SD-1's target checks, including the supervisor refusal on `deploymentModel`, run at the post-chain point on the chosen revision. Its reviewer also states that a derive request may carry `runtimeOptions`, validated at the post-chain point against the chosen revision, as the recovery path of "RV-6".
- [`connector-authorization-and-external-memory-design.md`](connector-authorization-and-external-memory-design.md): its reviewer states that the link checks read `capabilities.deferredToolDiscovery` from the chosen revision at the post-chain point.
- [`egress-and-git-design.md`](egress-and-git-design.md): its reviewer keys the warm list, the warm-list hosts in "Effective policy", and the pre-bind readable set on pool and revision, which resolves through the revision's pod set.
- [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md): its reviewer adds a row to FE-1's "Retirement paths" for a rollout's `Contracting`, which skips a quarantined Sandbox and its pair.
- [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md): its reviewer states that `checkpointPolicy` and `periodicCheckpointIntervalSeconds` are live, that `checkpointExcludes` is versioned and gateway-read, that a tenant's `checkpointExcludes` on a derived runtime merges over the selected base revision, and how the capture receives the session's list (D14).
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): its reviewer adds `runtime_revision` to the common envelope of `session.pod_usage`.

## Proposal split and order

One proposal, after the second proposal of [`runtime-routing-design.md`](runtime-routing-design.md), whose shared bucketing function it uses:

1. Land the specification edits.
2. Move `runtimeOptionsSchema` inline on the `Runtime` resource, ship the reference schemas as files, compile schemas at write, and move options validation to the post-chain point and their delivery to `session_start`. These close the defects under "Session options" in "Current state" and need no revision.
3. Make `isolationProfile` a live floor, narrow the per-tenant capability override, count tenant-forced demotions apart from the circuit breaker, and key the integration-level cache on the runtime, the image, and the declared level. These close F-5.1.35, F-5.1.36, F-5.1.37, and F-6.1.29 and need no revision, so the cache keys on the image digest until step 4 adds the pod hash.
4. Add the revision record, the hash, and the pod hash on both registration paths, with every runtime at one revision, and move the pod builder to the revision.
5. Add pod sets per pod-set key, the warm split, selection, discovery by revision, and the agent card generated on read.
6. Move the rollout procedure to the runtime and retire the pool-level endpoints.
7. Update the documentation and records.

## Review history

This section records how the design reached its current form. A cross-design sync review ran on 2026-11-08 across every design under `designs/`.

### Revision notes

- Revision 1, dated 2026-11-08, follows the owner's discussion of [`runtime-routing-design.md`](runtime-routing-design.md) O5, which asked for a transparent rollout of a new runtime version. The owner chose a separate design (D1) and declined revision pinning (D2).
- Revision 2, dated 2026-11-08, records the owner's settlement of O1 and O3 to O6 as D3 to D7. It adds the `immediate` policy, the per-write option, and promote for changes that must reach the next session, the retention period with its storage estimate and early delete, and asset content stored once per digest. It explains the field classes, moves `archivePolicy` to the live class, and leaves `runtimeOptionsSchemaRef` unclassified pending the owner's review.
- Revision 3, dated 2026-11-09, applies the sync review and the owner's answer to its question Q1:
  - "RV-2" states the creation order shared with [`runtime-routing-design.md`](runtime-routing-design.md) revision 7: revision selection runs after the `PreRoute` chain and before the post-chain point, which reads versioned fields from the chosen revision (RVR-9). Revision 2 placed selection after the post-chain checks, so checks read versioned fields before a revision existed.
  - "RV-5" corrects revision 2's claim that a derived runtime carries no versioned field. Overrides and appends merge over the selected base revision, a narrowing holds against every revision in the traffic, and D5 stays with that reason. The refusal of an invalidating base write applies to platform-owned derived runtimes only, and a tenant-derived runtime reads as `incompatible` under DR-6.
  - "RV-3" keys the warm list and the pre-bind reach on pool and revision, adds the owned-pool exemption (D8), and keeps quarantined Sandboxes through a rollout (RVR-10).
  - "RV-1" classifies the fields revision 2 left out, uses the API name `runtimeOptionsSchema`, lists `type` and `executionMode` under "Changed", and defines the rollout settings, including `canaryPercent`, `drainTimeoutSeconds`, and `ownedPoolWarmTimeoutSeconds`.
  - The diagram's hash key matches "RV-2", owned pools cite DR-5 in place of DR-2, and the reviewer instructions cover the designs the sync review found affected.
- Revision 4, dated 2026-11-09, follows the owner's request for a worked AWS example and a fix to manual steps. "RV-4" adds "Warm before weight" (RVR-11): a `manual` step now warms the new revision's share before its weight rises, as `automatic` already did, and `rollout.warmTimeoutSeconds` pauses a rollout whose platform pod sets cannot warm. Revision 3 let `set-weight` move traffic at once, so a large step started new sessions cold. "Worked example: an upgrade on AWS" estimates the temporary footprint and cost of a rollout for an operator with one runtime.
- Revision 5, dated 2026-11-09, applies the owner's decision N5 of that date, settled in the owner's word "ok". D8 and "Owned pools during a base rollout (D8)" under "RV-3" state that the idle minutes of a base rollout's extra pod set on an owned pool are billed to the platform rather than the owning tenant, as `pool.idle_usage` of `billing-resource-attribution-design.md` attributes them, which matches D8's exemption of those pods from the tenant's charge and limits.
- Revision 6, dated 2026-11-09, records the owner's settlement of O2 as decision D9, given in the owner's words "Go with this", and changes these rules. Under S1, the `Runtime` resource carries `runtimeOptionsSchema` inline, the reference runtimes' schemas become files under `schemas/runtime-options/` that `schemas/embed.go` embeds and the chart renders, no component fetches a schema URL, a write compiles the schema and refuses one that does not compile, a tenant-written schema may not carry `$schema`, `$id`, or a non-local `$ref`, and the revision stores the schema by digest, which "Retention" collects and the gateway's compiled-schema cache keys on. Under S2, the schema is versioned and a session's options are validated against the chosen revision's schema, RVR-2 is amended, and validation against every revision in the traffic is recorded as rejected. Under Rule 1, the size cap and the parse stay early, schema validation runs at the post-chain point on every creation kind and on a resume that changes revision, a derived schema is a second, separate validation in place of §5.1's merge and its name-subset rule, and absent options meet `required`. Under Rule 4, a refusal carries `details.runtimeRevision`, with `details.runtime` and `details.interceptor_ref` when the chain moved the session, and `lenny_runtime_options_refused_total` counts refusals. Under Rule 5, the options reach the runtime in `session_start`, rewritten on resume. Under S3, the delegation `TaskSpec` carries `runtimeOptions`. Under S4, "Discovery" under "RV-2" shows each caller the revision it would be assigned. Under S5, versioned fields split into pod-affecting and gateway-read fields, placed by their readers in the tree, pod sets key on the pod hash, and a rollout that changes only gateway-read fields warms nothing, which "RV-3", "RV-4", the worked example, "Limits", and the tests state. Under R2, a resume onto a revision whose schema refuses the stored options fails with a recorded reason and keeps its snapshot, and rollout `status` counts such sessions before `Contracting`. Under R5, a derived schema that requires a property a revision in the traffic forbids makes a tenant-derived runtime `incompatible`. "Current state" records the session options defects filed in `BUILD-GAPS.md` that day, and RVR-12, RVR-13, "RV-7", and the alternatives, specification changes, non-specification changes, validation items, reviewer instructions, and proposal steps follow.
- Revision 7, dated 2026-11-09, applies the adversarial review of revision 6's "Field classes" and the owner's settlement of its findings as D10 to D19, given in the owner's words "go with your recommendations" for G1 to G9 and "I agree we shouldn't have a tenant override for preConnect" for F4, with G8 as the coordinator corrected it. Under D10, a pod-affecting field shapes the pod, its template, or its warm state before bind, `sdkWarmBlockingPaths` and `minPlatformVersion` become gateway-read, the reader table no longer claims that the gateway sends `sdkWarmBlockingPaths` to the adapter, and `restartable` leaves the field list. Under D11, "Isolation floor" makes `isolationProfile` a live floor that pool admission and the WarmPoolController enforce. Under D12, `agentInterface` is versioned and gateway-read, and the agent card is generated on read. Under D13, the pod-set key covers the derived runtime's and the pool's contributions. Under D14, `checkpointExcludes` is gateway-read and delivered per session. Under D15 to D17, "Per-tenant capability override" removes `preConnect` from the override, reads it from the pod's revision on bind and recycle, makes blocking paths append-only with tenant-forced demotions counted apart from the breaker, and makes `interaction` and `injection` restrict-only. Under D18, "RV-5" states that a derived runtime sets no `capabilities` field, as §5.1 "Example B" states, and drops revision 6's statement that it may set the other `capabilities` entries. Under D19, a resume that moves revision keeps the stored plan and rebuilds it under the new binary, and the note on `workspaceDefaults` states that a tenant-derived runtime's warm entries already shape owned-pool pods. "Integration-level check" under "RV-2" corrects the integration-level cache. "Current state" records the review's tree findings with F-5.1.35, F-5.1.36, F-5.1.37, and F-6.1.29, and "Terms", RVR-14, RVR-15, "Limits", the alternatives, the specification and non-specification changes, the tests, "Validation owed", the reviewer instructions, and the proposal steps follow. The worked example is unchanged, because none of these decisions changes its figures.
- Revision 8, dated 2026-11-10, applies two reviewer instructions of `message-delivery-design.md`, the `injection.defaultMode` override rule and the classification of the fields its revision 3 adds, which the owner approved with that design's revision 5 on 2026-11-10 in the words "go with all your recommendations", and changes these rules. "Field classes" lists `capabilities.output.maxFileBytes` and the `harnessApprovals` default as versioned and gateway-read, with a reader row. "Per-tenant capability override" states that a narrowed `injection.modes` keeps `queue` and that `injection.defaultMode` may move only to an intent the narrowed list keeps. "Terms from other designs and the platform" glosses the three fields, and the status line reads against `message-delivery-design.md` revision 5, `session-derive-design.md` revision 16, and `forensics-evidence-preservation-design.md` revision 30.
- Revision 9, dated 2026-11-10, applies the reviewer instruction of `message-delivery-design.md` revision 6 for decision A19 of that design, which the owner settled that day as re-review decision R3 and then revised the same day after a short review of that revision, and changes these rules. Under the revised A19, the seal checkpoint and the session's exports share one seal window, and the owner's condition on R3 makes `maxWorkspaceSealDurationSeconds` a runtime field as well as a pool setting. "Field classes" lists it as versioned and gateway-read, with a reader row: the runtime's value overrides the pool's, a write above the chart ceiling `gateway.workspaceSealMaxDurationCeilingSeconds` or not greater than `delegation.usageQuiescenceTimeoutSeconds` is refused, and a tenant-derived runtime may set it within those bounds. "Terms from other designs and the platform" glosses it. This revision as first written listed `files.sealExportWaitSeconds`, a separate export wait after the seal checkpoint, which the revised A19 removes. The re-review's other findings and decisions R1, R5, and R8 change nothing here. The status line reads against `message-delivery-design.md` revision 6, `incremental-checkpoint-design.md` revision 11, and `tenant-derived-runtimes-design.md` revision 65.
- Revision 10, dated 2026-11-10, follows decision A23 of `message-delivery-design.md` revision 7: the `maxWorkspaceSealDurationSeconds` reader row states the resolution order of revision, pool, and gateway flag under the ceiling.
