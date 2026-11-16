# Spike findings: the warm pools and pod lifecycle slice

Status: 2026-11-16. This file records what writing the vertical slice "warm pools and the pod lifecycle" under `proposed/` showed about [TAXONOMY.md](TAXONOMY.md) version 0. The slice applies the target state of `pod-lifecycle-controller-design.md` revision 5 and the designs it reads as implemented. Where a source could not settle a fact, the page carries an `<!-- unresolved: ... -->` comment; the slice holds 74 of them.

## Pages and sizes

"Total" counts every word in the file. "Prose" excludes HTML comments (the `src` and `unresolved` reviewer notes) and fenced blocks (the `lenny-def` definitions and diagrams). "Source" is the approximate word count of the current-specification and design text the page replaces or draws on, measured by section with `wc -w`.

| Page | Code | Total | Prose | Source (approx.) | Main sources |
| :-- | :-- | --: | --: | --: | :-- |
| `capabilities/runtimes-and-pools/index.md` | RTPOOL | 586 | 586 | none | |
| `capabilities/runtimes-and-pools/warm-pools.md` | POOL | 5,397 | 4,056 | 14,000 | §4.6.1 scaling, PDB, fill; §4.6.2; §4.6.3 validator and denial policy; §5.2 pinned idle, scaling implications, bootstrap, topology; PLC-2, PLC-7; RV-3; DR-5, DR-10 |
| `capabilities/runtimes-and-pools/pod-claims.md` | CLAIM | 4,494 | 3,701 | 9,000 | §4.6.1 claim paragraphs; §4.6.3 claim row and enumeration; §5.2 tenant pinning, `onPoolExhausted`, slot atomicity bound; §6.2 reserved hold; §12.6 mirror; §28.3 registers; PLC-2; RV-2, RV-6; DR-11 |
| `capabilities/runtimes-and-pools/agent-pod-lifecycle.md` | APOD | 3,883 | 2,984 | 9,700 | §6.2 state machine; §4.6.1 projection, finalizers, disruption, host-schedulable; §5.2 recycle and retirement; PL-1, PL-8; SI-3, SI-4; FE-1 |
| `components/warm-pool-controller.md` | WPC | 2,287 | 1,606 | 6,300 | §4.6.1 leader election, crash drain, mirror recovery, rate limiting, etcd; §4.6.3 SSA and RBAC; §16.1 rows |
| `components/pool-scaling-controller.md` | PSC | 1,081 | 873 | 1,200 | §4.6.2; §4.6.3 |
| `components/admission-webhooks.md` | WEBHOOK | 1,143 | 957 | 2,500 | §17.2 items; §4.6.1 claim webhook; §4.6.3 validator; chart template |
| `components/gateway/index.md` | GW | 146 | 110 | none | stub |
| `components/gateway/pod-claimer.md` | GWCLAIM | 1,229 | 918 | 1,400 | §4.6.1 interfaces; §4.6.3 gateway RBAC; §16.1 rows |
| `interfaces/kubernetes/index.md` | K8S | 860 | 814 | 900 | §4.6.1 API group and CRD validation; PLC-2 "No alias"; §10.5 |
| `interfaces/kubernetes/podset.md` | KPSET | 1,482 | 1,274 | 2,500 | §4.6.1 kind table; §4.6.3 rows; PLC-2, PLC-3, PLC-4; tree types |
| `interfaces/kubernetes/agentpod.md` | KAPOD | 980 | 878 | 1,200 | same |
| `interfaces/kubernetes/podclaim.md` | KPCLAIM | 880 | 773 | 1,300 | same; §4.6.1 webhook |
| `interfaces/kubernetes/labels-and-annotations.md` | KLABEL | 1,116 | 452 | 1,200 | §6.2 label table; §5.2 tenant label; §4.6.1 host-schedulable; PLC-2; FE-1 |
| `interfaces/admin-api/index.md` | ADMIN | 438 | 394 | 600 | §15.1 error table, ETag, dry run; §25 confirm pattern |
| `interfaces/admin-api/pools.md` | APOOLS | 1,396 | 1,258 | 1,200 | §15.1 pool rows; §4.6.2 sync status; PLC-7; DR-1, DR-5 |
| `catalogs/settings.md` | | 500 | 460 | | generated view |
| `catalogs/errors.md` | | 427 | 364 | | generated view |
| `catalogs/metrics.md` | | 469 | 429 | 2,500 (§16.1 rows) | generated view |
| `catalogs/alerts.md` | | 319 | 279 | 1,500 (§16.5 rows) | generated view |
| `catalogs/labels.md` | | 302 | 255 | | generated view |
| `catalogs/requirements.md` | | 757 | 704 | | generated view |
| `catalogs/glossary.md` | | 866 | 812 | | generated view |
| Total | | 31,038 | 24,138 | about 57,000 | |

The non-catalog pages total about 27,400 words against about 57,000 of source. Most of the reduction comes from text the target state removes (the `kubernetes-sigs/agent-sandbox` dependency text, pre-launched runtimes and `sdk_connecting`, service mode, the runtime-process acknowledgment), from repeated statements collapsed to one, and from content that leaves the specification (the ADR-007 verification plan, the etcd tuning guidance, the per-tier tables). The etcd guidance alone is about 2,500 words of §4.6.1 that the slice does not carry (problem 12).

## Placement problems

Each item gives the example, what version 0 said or failed to say, and what the slice did.

1. **Behavioral settings that are process flags.** `claimOrphanTimeout` is the WarmPoolController flag `--claim-orphan-timeout` and is also read by the gateway; `initialFillGracePeriod` is a WarmPoolController flag that suppresses alerts; `podClaimFallbackMaxMirrorLagSeconds` is a gateway flag. Version 0 assigns a setting by what it changes, so all three went to capabilities (CLAIM, POOL), and their `scope` line names the flag. The component pages therefore do not list every flag their process reads, which is what an operator looking at a component wants. The slice left the component pages incomplete in this respect.
2. **Metrics owned by the emitter scatter one capability's metrics.** The claim's metrics are split between GWCLAIM (`lenny_pod_claim_*`, the mirror lag) and WPC (`lenny_orphaned_claims_total`). `lenny_gateway_pod_retirement_total` measures APOD-R8 but is emitted by a gateway subsystem outside the slice, so its definition has no page in the slice. `lenny_controller_workqueue_depth`, `lenny_controller_leader_lease_renewal_age_seconds`, and `lenny_crd_ssa_conflict_total` are emitted by both controllers and have no single emitter; the slice defined them on WPC and pointed PSC at it. `lenny_warmpool_failover_claims_served_fraction` has no stated emitter at all. See open question 3.
3. **Alerts without a capability requirement.** `ControllerLeaderElectionFailed`, `ControllerWorkQueueDepthHigh`, and `CRDSSAConflictStuck` detect a component's failure as a process. Version 0 assigns an alert only to "the capability whose requirement it detects a breach of", so they have no owner. The slice listed them as flagged rows in `catalogs/alerts.md`. `PoolConfigValidatorUnavailable` and `PodClaimGuardUnavailable` were forced onto POOL-R1 and CLAIM-R11; the first fit is weak.
4. **Shared error codes leave the slice with almost no errors of its own.** Every admin API error the pool operations return (`VALIDATION_ERROR`, `RESOURCE_ALREADY_EXISTS`, `RESOURCE_HAS_DEPENDENTS`, `INVALID_POOL_CONFIGURATION`, the ETag codes) is shared, so version 0 sends each to the cross-cutting error model. `WARM_POOL_EXHAUSTED` and `POOL_DRAINING` exist only because of CLAIM-R12 and POOL-R25, but they are returned by the client API, so they belong to `interfaces/client-api/sessions.md`. The capability that produces the outcome cannot hold the definition, and the reader of CLAIM-R12 follows two links to learn the status code. The slice described each outcome in the requirement ("the retryable pool-exhaustion error") and linked the operation. The only errors the slice defines are Kubernetes admission refusals.
5. **TAXONOMY's own example code does not exist.** The "Interface or capability" paragraph uses `409 POOL_EXISTS`. The tree and §15.1 answer a duplicate with the shared `409 RESOURCE_ALREADY_EXISTS`. The slice used the real code.
6. **A label's meaning, its key, and its constraint land in three places.** The pool label's meaning is a capability fact (POOL Terms), its key, values, writers, and readers are on the labels page as version 0 requires, and its immutability is at once a rule with a reason (POOL-R5), a key property (`mutability: immutable` on the labels page), and an observable admission outcome (the `PodSet` and `AgentPod` admission tables). The interface-or-capability test sends "observable to the caller" to the interface and "how Lenny decides" to the capability, and immutability is both. The slice stated the rule and its reason in POOL-R5, the property on the labels page, and the refusal in the resource admission tables, which is three statements of one fact.
7. **Enumerations appear on both the capability and the interface.** The phase values appear in APOD's Terms and in `AgentPod.status.phase`'s type; the binding states appear in CLAIM's state machine and in `PodClaim.status.phase`'s type. A client needs the values on the interface page, and the state machine needs them on the capability. Version 0 has no rule for a value set that is both syntax and domain model. The slice wrote both and relies on a future gate that compares them.
8. **One domain object, several representations, no rule for which owns what.** A claim is a capability object (CLAIM), a Kubernetes resource (`PodClaim`), and a register (`REG-CLAIM` in §28.3, which restates the writer set). A pool is a capability object, a Postgres row, an admin API object, and the spec of one or more `PodSet` objects. The admin API's pool object and `PodSet.spec` list mostly the same fields as two interfaces. The owners table has no row for a channel register, and none for "the same field set exposed by two interfaces". The slice made the resource page own the resource, pointed the register at it, and linked each field's meaning to its capability from both interface tables.
9. **A paragraph of §5.2 splits across pages.** "Pinned idle inventory" defines a pinned idle pod (claim eligibility, CLAIM) and how warm counts treat it (inventory, POOL). "Tenant pinning" is a claim rule, a tenancy security property, and a label transition rule. The slice put the pin and the definition in CLAIM, the accounting in POOL, and the transitions on the labels page.
10. **A setting used by several capabilities has no clear owner.** `sessionPolicy.maxConcurrentSessionsPerPod` fixes APOD's slot count, drives the Slots capability's assignment, and enters POOL's scaling formula. Version 0 says "the capability", singular. The slice defined it on APOD with an unresolved note.
11. **Internal Go interfaces pass question 3.** `PodLifecycleManager` and `PoolManager` are contracts "a party can be built against" (question 3), but they are internal seams between packages rather than published interfaces. The slice put them under each component's "Internal parts". Version 0 needs a line saying that a Go interface inside one binary is a component's internal part.
12. **Operator guidance has no type.** The etcd tuning matrix, the per-tier `minWarm` examples, and the controller tuning tables are neither behavior, nor a component's deployment property, nor history. Question 8 does not list "operator guidance". The slice left them out and did not invent a page. A rule that sends them to `docs/operator-guide/` would settle it.
13. **Verification plans inside normative text.** §4.6.1's ADR-007 paragraph makes two named tests a "Phase 1 blocking prerequisite". The tests verify CLAIM-R8; the gate is a build-plan item. Question 8 sends the plan out, and version 0 has no home for "the tests that verify a requirement". The slice kept the fencing argument as CLAIM-R8's rationale and dropped the plan.
14. **Flows want an operation identifier for Kubernetes calls.** The capability template asks each flow step to link "a component and an interface operation". Admin API operations have identifiers (`admin-api#pools.create`); Kubernetes resource operations do not. The slice linked section anchors such as `podclaim.md#create`.
15. **Requirements and terms have no anchor rule.** Version 0 gives headings explicit anchors and requirements identifiers, but a requirement is a bold paragraph and a term is a table row. The slice added `<a id="pool-r5"></a>` before each requirement and inside each owned term's cell.
16. **The summary is written twice.** The template renders the front-matter summary as the opening paragraph. Writing it by hand in both places states it twice; the slice did so to show the rendered page, and a generator should render it instead.
17. **The area index has no type.** `type:` allows overview, capability, component, interface, cross-cutting, and catalog. A folder index is none of them. The slice used `type: index`.
18. **Interface sub-pages need codes.** Version 0 gives every interface page a code but no scheme for sub-pages. The slice invented `KPSET`, `KAPOD`, `KPCLAIM`, `KLABEL`, and `APOOLS`, which do not read well. Interface operations already carry stable identifiers, so a code per interface folder may be enough.
19. **The catalog of requirements needs titles.** A generated requirements catalog needs a short title per requirement, and version 0 gives requirements none. The slice wrote titles by hand.
20. **The gloss rule held, with one cost.** No gloss in the slice holds a number, a normative keyword, or more than one link. The rule forced glosses such as "the controllers' worst-case crash failover window" where the reader wants "25 seconds", which is the intended trade.
21. **Owned pools "as far as warm inventory goes".** DR-10's "Mechanism" mixes behavior (the floor `max(minWarm, reservedPods - active)`) with implementation (`plan.Compute`, `MinWarm`, `MaxWarm`). The component-or-capability test separated them cleanly: the floor is POOL-R21, and the implementation names were dropped.

## Answers to the open questions

1. **Shared domain objects.** The slice's objects (pool, pod set, claim, agent pod) each fit one owning capability, and the other pages glossed them without strain. The strain came from representations rather than from sharing: each object is also a Kubernetes resource, a Postgres row, or a register (problem 8). A separate model type is not needed for this slice. A rule is needed that says the capability owns the object's definition, states, and invariants; the interface owns each representation's syntax; and a representation's enumerations are checked against the capability by a gate (problem 7). The session and the tenant were glossed only, so the slice gives no evidence on them.
2. **End-to-end scenarios.** Each capability page held its own flows comfortably: CLAIM's claim flow crosses the gateway claimer, the Kubernetes API, the webhook, and the WarmPoolController, and fitted in about 150 words because every step links a requirement. A flow that starts in one capability and ends in another, such as a pod's whole life (create, claim, recycle, retire), sat in APOD with links into CLAIM. The slice supports putting each flow in the capability that starts it, and putting the multi-capability walkthroughs of §29 in a non-normative overview folder.
3. **Metric ownership.** The emitter is the wrong owner for meaning and the right owner for deployment facts. Ownership by emitter scattered the claim's metrics over two components, left a metric whose emitter is outside the slice with no definition, left shared controller metrics with two candidate owners, and left a derived ratio with none (problem 2). Every alert expression on a capability page names metrics defined on a component page, so a reader of an alert follows a link for each metric. Recommended: the capability that the metric measures owns the definition (name, type, labels, meaning); the component page lists the metric names it emits. Controller-generic metrics belong to a cross-cutting observability page.
4. **The gateway.** One gateway folder with a page per subsystem worked for the pod claimer: the claimer has its own responsibilities, code paths, and metrics, and its page stayed at 1,229 words. Deployment properties are the gateway's as a whole, so they belong on the gateway index, which the slice left as a stub. RBAC is the awkward case: the ServiceAccount is gateway-wide, but each grant exists for one subsystem. The slice listed the claimer's grants on its own page. A gateway index that assembles the ServiceAccount's grants from the subsystem pages, by link, would keep each fact once.
5. **The agent pod.** The slice needed the pod blueprint only as a gloss target (`components/agent-pod/index.md`), so it did not write it. The evidence still points one way: the pod's containers (adapter, slot containers with the supervisor, `netagent`, `keepalive`, the init containers) have different owners, different designs, and different failure modes, while the pod's lifecycle is one capability (APOD). Recommended: a capability for the pod's lifecycle, and a component folder `components/agent-pod/` with an index for the pod as a deployed unit and a page per container.
6. **Reference runtimes and the playground.** The slice touched the reference runtimes only through the chart's `reference-runtimes.yaml` and the embedded stack's `lenny runtime apply`, which names its pool after the runtime. That is a registration path (RUNTIME) and a deployment mode (a component), and gives no evidence for the catalog itself. No evidence on the playground.
7. **Page size.** The three capability pages came out at 3,883, 4,494, and 5,397 words, with 2,984 to 4,056 words of prose. All fit the 6,000-word budget, POOL with the least room. POOL would exceed the budget once the unresolved items are filled (the bootstrap convergence criteria, the per-tier factors, the full pool field set) unless the scaling formula and bootstrap mode move to a sub-page `warm-pools/scaling.md`. The component and interface pages stayed between 860 and 2,287 words. About 20% of each capability page's total is reviewer comments and definition blocks, which a rendered page keeps (definitions) or drops (comments).

## Facts the current specification states more than once

| Fact | Places |
| :-- | :-- |
| The reserved hold: the `recycling → reserved` patch, `holdExpiresAt`, the rebind, and the precondition-guarded expiry | §4.6.1 **Reserved hold**; §6.2 **`reserved` hold semantics.**; §6.2 state diagram; §4.6.3 enumeration; §5.2 **Pinned idle inventory.** in part |
| Reserved pods count as occupied | §4.6.2 **Reserved pods count as occupied.**; §6.2 **`reserved` hold semantics.** (last sentence) |
| When a claim is deleted | §4.6.1 kind table `SandboxClaim` row; §4.6.1 **Pod claim mechanism**; §4.6.3 `SandboxClaim` row; §28.3 `REG-CLAIM` row |
| The phase enumeration | §4.6.1 **Occupancy projection**; §6.2 opening paragraph; §6.2 paragraph before **`input_required` sub-state:**; §6.2 **State storage:**; §12.6 `agent_pod_state` column comment |
| The `lenny.dev/state` mapping and which recycling pods carry `active` | §6.2 **Concurrent pod lifecycle** bullet "Partial occupancy"; §6.2 **State storage:**; §4.6.1 **Disruption protection for agent pods:** |
| Host-node schedulability | §4.6.1 **Host-node schedulability labeling**; §6.2 **Host-node schedulability precondition.** |
| The 25-second crash failover window | §4.6.1 **Leader election:**, **Controller failover and warm pool sizing:**, **Simultaneous controller failover:**; §4.6.2 **Leader election:** and the formula text |
| `podClaimQueueTimeout`'s margin over the failover window | §4.6.1 **Controller failover and warm pool sizing:**; §4.6.1 **Simultaneous controller failover:** |
| The claim guard | §4.6.1 **`SandboxClaim` admission webhook**; §4.6.3 enumeration paragraph; §17.2 item 7; §16.5 `SandboxClaimGuardUnavailable` |
| The pool configuration validator | §4.6.3 **Validating webhook for Postgres-authoritative state**; §17.2 item 9; §16.5 `PoolConfigValidatorUnavailable` |
| The uptime drain | §4.6.1 **Occupancy projection** (last paragraph); §5.2 **Pod retirement policy** "Uptime limit"; §6.2 state diagram |
| Sync status | §4.6.2 **Reconciliation drift detection.** items 3 and 4; §15.1 `sync-status` row |
| `PoolWarmingUp` | §5.2 **`PoolWarmingUp` condition.**; §5.2 **Pinned idle inventory.**; §16.5 `WarmPoolBootstrapping` |
| `claimOrphanTimeout`'s bound | §4.6.1 **Orphaned `SandboxClaim` detection:** predicate 1; §5.2 **Slot assignment atomicity** |

Defects found in the current specification while writing:

- §5.2 and §4.6.3 name the tenant label webhook `lenny-tenant-label-immutability`; §17.2 item 5 assigns the same transitions to `lenny-label-immutability`.
- §4.6.2 spells the warm-up input `scalingPolicy.podWarmupSecondsBaseline`, as does §16.5 `WarmPoolReplenishmentSlow`; the tree's type is `scalePolicy`.
- §25.6's runbook example selects `kubectl get sandboxes -l pool=<pool-name>`, a label that does not exist, and `DiagnosePool`'s fallback reads `lenny.dev/pod-state`, which is `lenny.dev/state` in §6.2.
- §4.6.3's WarmPoolController RBAC grants no `create`, `patch`, or `delete` on pods, although the controller creates pods and maintains their labels.
- The pod set deletion guard (`lenny-sandboxtemplate-deletion-guard` in the chart) is absent from §17.2's webhook list and appears only in its NET-068 text.
- `lenny_warmpool_stale_pods` reads `maxIdleSeconds`, which no section defines.
- `PoolDrained` is named as a condition and never defined.

Questions the designs leave open, each carried as an `unresolved` comment: whether the `vm-restart` scrub profile survives SI-3; what the deletion guard checks once rollouts are per runtime (RV-4); the `lenny.dev/state` value for a `quarantined` pod; the key of the scrub warning annotation; the rejection labels of the new `PodSet` and `AgentPod` refusals; and whether `recycle.allowCrossTenantReuse` survives now that the tenant pin makes it inert.

## Links to pages outside the slice

Every link below points at a page the slice did not write. The path and anchors are the slice's guesses; the last column states what the slice assumes the page holds.

| Path | Anchors used | Assumed content |
| :-- | :-- | :-- |
| `capabilities/runtimes-and-pools/runtime-registry.md` | `runtime`, `runtime-name` | RUNTIME: runtime registration and the runtime-name rule of PLC-7 "Runtime names." |
| `capabilities/runtimes-and-pools/runtime-revisions.md` | `revision`, `pod-hash`, `pod-set-key`, `pods`, `selection`, `rollout`, `warm-before-weight`, `warm-list-per-revision` | REV: RV-1 to RV-7, including the pod-set key computation and the per-revision warm list. |
| `capabilities/runtimes-and-pools/tenant-derived-runtimes.md` | `owned-pool`, `capacity-reservation`, `pod-budget`, `changing-the-list`, `covered-stores`, `served-label`, `warm-stores`, `shared-record-boundary`, `tenant-operations` | DERIVED: DR-1 to DR-12. |
| `capabilities/runtimes-and-pools/placement.md` | `placement`, `rendering`, `enforcement` | PLACE: PM-1 to PM-9. |
| `capabilities/runtimes-and-pools/isolation-profiles.md` | none | ISO: §5.3. |
| `capabilities/sessions/slots.md` | `slot`, `slot-claim`, `dead-slot`, `leaked-slot`, `shared-pod`, `launch-at-bind`, `stale-slot-release` | SLOT: slot assignment, the slot set (`REG-SLOTCOUNT`), slot liveness (SI-4), `acknowledgeSharedPod`, the stale-slot release. |
| `capabilities/sessions/slot-release-and-scrub.md` | `slot-release`, `whole-pod-scrub`, `what-the-scrub-reaches` | SCRUB: SI-3 slot release, the whole-pod scrub and `ReportPodScrub`, §5.2 "What the scrub reaches". |
| `capabilities/sessions/session-lifecycle.md` | `pod-assignment`, `post-terminal-window`, `suspension` | SESS: §7.1, §7.2. |
| `capabilities/sessions/retry-and-resume.md` | `revision-on-resume`, `resume-after-pod-failure` | §7.3 and RV-6. |
| `capabilities/sessions/checkpoints.md` | `eviction-checkpoint`, `grace-budget` | CKPT: the eviction checkpoint and the §10.1 grace and barrier floors. |
| `capabilities/forensics/quarantine.md` | `quarantine`, `data-model`, `retirement-paths` | QUAR: FE-1. |
| `capabilities/security/adapter-authentication.md` | `nonce-only` | §4.7 nonce-only mode, `SecurityDegradedMode`, `requireSoPeercred`. |
| `capabilities/security/network-isolation.md` | `cidr-drift`, `webhook-egress` | §13.2, including NET-068. |
| `capabilities/security/workload-certificates.md` | `rotation` | §10.3 certificate TTLs and idle-pod replacement. |
| `capabilities/billing/resource-classes.md` | none | BR-1 resource classes. |
| `capabilities/network/egress.md` | none | Egress profiles and DNS policy. |
| `components/agent-pod/index.md` | none | The pod blueprint of PL-1 as a deployed unit. |
| `cross-cutting/error-model.md` | `validation_error`, `resource_already_exists`, `resource_not_found`, `resource_has_dependents`, `invalid_pool_configuration`, `etag_required`, `forbidden`, `runtime_unavailable` | The shared error codes of §15.1. |
| `cross-cutting/upgrade-and-compatibility.md` | `crd-schema-version` | §10.5 including the CRD schema-version rule. |
| `interfaces/client-api/sessions.md` | `sessions-create`, `errors` | Session create and its errors, including `WARM_POOL_EXHAUSTED` and `POOL_DRAINING`. |
| `interfaces/admin-api/bootstrap.md` | `errors` | The bootstrap seed and `SEED_VALIDATION`. |
| `interfaces/channels/registers.md` | `reg-claim`, `reg-podstate` | The §28.3 register-entry register. |
| `interfaces/kubernetes/runtime.md` | none | The `Runtime` custom resource. |

The admin API index also names operation pages (runtimes, derived runtimes, bootstrap, tenants, quarantines) without links, and the gateway index names subsystems without links.

## Proposed changes to TAXONOMY.md

1. **Metrics are owned by the capability they measure.** The definition block (name, type, labels, meaning) sits on the capability; the component page lists the names it emits. Controller-generic and process metrics go to a cross-cutting observability page. Reason: problem 2 and the answer to question 3.
2. **Allow component alerts.** An alert that detects a component's failure as a process is owned by that component. Reason: problem 3.
3. **A single-cause error is owned by the capability whose rule produces it.** A code returned for one rule, such as `WARM_POOL_EXHAUSTED`, is defined on the capability, and each interface that returns it lists it with a link. Only codes with several causes go to the cross-cutting error model. Reason: problem 4.
4. **Fix the example.** Replace `409 POOL_EXISTS` with `409 RESOURCE_ALREADY_EXISTS` or another code that exists. Reason: problem 5.
5. **Split label facts explicitly.** The labels page owns the key, its values, its carriers, its writers and readers, and its mutability; the capability owns the meaning and the reason; a resource page's admission table states the refusal and links both. A capability requirement that a label is immutable links the labels entry instead of restating the property. Reason: problem 6.
6. **Enumerations: interface lists, capability defines, a gate compares.** An interface field whose values are a capability's states lists the values and links the state machine; a gate checks that the two sets match. Reason: problem 7.
7. **Add representations to the owners table.** Rows for "a Kubernetes resource representing a domain object" (the resource page), "a channel register" (the register row names the store and links the resource or table that owns the writers), and "a stored table" (a stored-schema interface page). Reason: problem 8.
8. **Settings used by several capabilities.** Define the setting on the capability whose object it configures, and have each other user link it. Reason: problem 10.
9. **Component pages list every flag their process reads.** Behavioral flags keep their definition on the capability, and the component's process settings section lists them by link. Reason: problem 1.
10. **Add rules for internal interfaces and operator guidance.** A Go interface inside one binary is a component's internal part. Deployment and tuning guidance leaves the specification for `docs/operator-guide/`. Reason: problems 11 and 12.
11. **Anchor rules for requirements and terms.** Each requirement and each owned term carries an explicit anchor equal to its lower-cased identifier or term slug. Reason: problem 15.
12. **Requirements carry a title.** `**POOL-R5. Labels are required and immutable.**` gives the catalog a title and the reader a scan line. Reason: problem 19.
13. **Do not repeat the summary in the body.** The renderer inserts it. Reason: problem 16.
14. **Add an `index` page type,** with the sections: summary, reading paths, the table of pages, and the components and interfaces that serve the folder. Reason: problem 17.
15. **Operation identifiers for Kubernetes resources.** `kubernetes#podclaim.create`, `kubernetes#podset.delete`, so flows and code can cite them like admin API operations. Reason: problem 14.
16. **Codes per interface folder rather than per interface page,** with operation identifiers for precision. Reason: problem 18.
17. **A tests rule.** A requirement's verification tests are not specification content; the requirement names its test tiers, and the tests cite the requirement. Reason: problem 13.
18. **A page-size rule for capabilities with a model and a formula.** A capability that carries a sizing formula or a sub-procedure longer than about 1,000 words moves it to a sub-page. Reason: the answer to question 7.

## Self-check: five facts, each stated once

| Fact | Where it is stated | Where it is linked or glossed |
| :-- | :-- | :-- |
| The reserved hold lasts `gateway.claimHoldTTLSeconds`, default 10 seconds. | The default is in the setting block in `proposed/capabilities/runtimes-and-pools/pod-claims.md#settings`; the rule is CLAIM-R9. | Glossed in POOL and APOD Terms ("Reserved hold") with a link to `pod-claims.md#reserved-hold`; POOL-R15 links CLAIM; `catalogs/settings.md` lists it. No other page states the value. |
| A pool name matches `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`. | POOL-R2. | `admin-api/pools.md` `pools.create` errors link POOL-R2 and state only that the message names the pattern; the labels page links POOL for the label's meaning. |
| A pod set's warm-pod budget has an integer `minAvailable` of `minWarm - 1` and none below a `minWarm` of 2. | POOL-R16. | `podset.md` "Owned objects" names the budget and links POOL-R16; APOD-R15 and the WarmPoolController responsibilities link POOL-R16. |
| `claimOrphanTimeout` defaults to 15 minutes. | The setting block in `pod-claims.md#settings`. | CLAIM-R15 and CLAIM-R16 use the name; the WarmPoolController page does not restate it (problem 1 records the cost); `catalogs/settings.md` lists it. |
| A claim is named `claim-<podName>`. | CLAIM-R1 states the rule. | `podclaim.md` states the name again as the `metadata.name` constraint and in its create outcomes. This is the residual duplication of problem 6: the name is both a behavioral rule and a field constraint. |

The check also found three statements the slice repeats by design and the proposals above would remove: the phase values (APOD Terms and `AgentPod.status.phase`, problem 7), the binding-state values (CLAIM state machine and `PodClaim.status.phase`, problem 7), and the 25-second crash window, stated on each controller page because each controller has its own lease.
