# Pod lifecycle controllers

Status: design for owner review, revision 1, 2026-11-15, with no independent review yet. Nothing here is built or staged. It is written from the review of upstream `kubernetes-sigs/agent-sandbox` of 2026-11-15, filed under [`evidence/pod-lifecycle-controller-design/`](evidence/pod-lifecycle-controller-design/README.md), and from the owner's decisions of the same day (D1 to D3). It reads against every design under `designs/` as implemented, as the owner asked (decision D2 of [`session-isolation-design.md`](session-isolation-design.md#decisions)), in particular [`agent-pod-layout-design.md`](agent-pod-layout-design.md) revision 15, [`session-isolation-design.md`](session-isolation-design.md) revision 17, [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md) revision 7, [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md) revision 21, [`placement-design.md`](placement-design.md) revision 11, [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) revision 77, [`runtime-revisions-design.md`](runtime-revisions-design.md) revision 21, [`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md) revision 8, [`tenant-security-settings-design.md`](tenant-security-settings-design.md) revision 9, and [`session-lifecycle-design.md`](session-lifecycle-design.md) revision 12. It stages no specification edit. The changes go through `change-proposal` after review. Proposals carry the label PLC-n, requirements carry the label PLCR-n, and open decisions carry the label O-n. "Review history" at the end records the revisions.

Each factual claim carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, the documentation, or a design under `designs/` on 2026-11-15, at commit `8702041e8`. |
| **[external]** | Read in the upstream repository or on a Kubernetes page. The review in `evidence/pod-lifecycle-controller-design/` gives a URL for each upstream claim, and "Sources" lists the pages this design read itself. |
| **[design]** | What this design requires. Nothing implements it. |
| **[inference]** | Reasoned from the sources and not run. "Validation owed" lists each one that matters. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The specification says Lenny is built on the upstream project `kubernetes-sigs/agent-sandbox` ([§4.6.1 Warm Pool Controller (Pod Lifecycle)](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle), **Implementation:**), and the code has no dependency on it **[source]**. `go.mod` does not list `sigs.k8s.io/agent-sandbox`. Lenny defines its own resource kinds `Sandbox`, `SandboxTemplate`, `SandboxWarmPool`, and `SandboxClaim`, with a further kind `Runtime`, under `lenny.dev/v1alpha1` in `pkg/apis/lenny/v1alpha1`, ships them in `charts/lenny/crds/`, and runs its own controllers in `pkg/controller/sandbox`, `pkg/controller/warmpool`, and `pkg/controller/poolscaling` **[source]**. The tree is in the state the specification's fallback plan describes.

The specification's dependency text therefore binds nothing that exists **[source]**:

- **Implementation:** credits upstream with community maintenance, "Pod Snapshots on GKE", and a claim model designed by SIG Apps. Upstream's snapshot feature is part of its Python client, and its controllers contain no snapshot logic **[external]**.
- **Dependency pinning and upgrade policy:** pins a tagged release and calls upstream "a v0.x project". Upstream is at `v1.0.11` with a `v1beta1` API **[external]**.
- **Go/no-go criteria for the agent-sandbox dependency:** and **Fallback plan if agent-sandbox is abandoned or diverges:** gate Phase 2 on an assessment of upstream and estimate "2-3 engineering-weeks" for a replacement that is already built.
- §18.6, §18.9, §18.40, §19, and §23 repeat the dependency.

The kinds share upstream's names and differ in meaning. Lenny's `SandboxClaim` is a lock the gateway takes on a pod it has already chosen, and upstream's is a request for a sandbox that a controller fulfils. Lenny's `SandboxWarmPool` keeps claimed pods as members and returns them to idle inventory, and upstream's gives a claimed sandbox away. The specification states none of these differences. Where a field means what upstream's field of the same purpose means, the names still differ: Lenny's `Sandbox.status.podIP` against upstream's `podIPs`, and Lenny's `SandboxWarmPool.spec.templateRef` against upstream's `sandboxTemplateRef.name` **[source]** **[external]**.

The review also found places where the specification and the code disagree ("Current state"), and the Go types that implement the specification's pod lifecycle interfaces are named `AgentSandbox*`, which describes the `lenny.dev` types as upstream types **[source]**.

## Scope

**Changed:**

- The specification describes Lenny's own controllers, modelled on upstream's resource kinds, in §4.6.1, §3, §17.1, §17.6, §18.2, §18.6, §18.9, §18.40, §19, and §23 ("PLC-1").
- Fields whose meaning matches upstream take upstream's names: `Sandbox.status.podIP` becomes `status.podIPs`, and `SandboxWarmPool.spec.templateRef` becomes `spec.sandboxTemplateRef.name`. The CRD schema version moves from "1" to "2" ("PLC-2").
- The specification and the documentation state, per kind, where Lenny's semantics differ from upstream's ("PLC-3").
- The spec and code mismatches the review found are corrected ("PLC-4").
- The `AgentSandbox*` Go types become `CRD*` types, and the §17.1 row names them ("PLC-5").

**Removed:**

- The specification's dependency text: "built on", the pinned release, the v0.x upgrade policy, the go/no-go criteria, the fallback plan, the Phase 2 dependency assessment, the upstream-contribution cadence, and the "default implementation wraps `kubernetes-sigs/agent-sandbox`" statements (D3).

**Added:**

- One recorded statement of when adopting upstream is worth reconsidering, in §4.6.1 ("PLC-6").

**Out of scope:**

- Renaming Lenny's kinds. The owner kept upstream's kind names (D2).
- Adopting upstream `v1beta1` as a dependency, or using upstream's `Sandbox` controller alone. The review's option (b) and its middle paths are recorded under "Alternatives rejected", and PLC-6 states when to reconsider them.
- GKE Pod Snapshots, the multi-cluster warm-pool planner, refill pacing, and the other upstream features section 4 of the review lists as worth borrowing. O2 covers the one snapshot statement the specification already carries.
- The content of resource classes. [`billing-resource-attribution-design.md`, "Resource classes in configuration"](billing-resource-attribution-design.md#resource-classes-in-configuration) (BR-1) owns it, and PLC-4 records how it settles the review's resource class finding.
- Contributing Lenny's slot model upstream.

## Terms

| Term | Meaning |
| :-- | :-- |
| Upstream | The project `kubernetes-sigs/agent-sandbox`, a SIG Apps subproject, at release `v1.0.11`. Its kinds are `Sandbox` in the group `agents.x-k8s.io` and `SandboxTemplate`, `SandboxClaim`, and `SandboxWarmPool` in `extensions.agents.x-k8s.io`, all at `v1beta1` **[external]**. |
| Lenny kind | One of the custom resource kinds Lenny defines under `lenny.dev/v1alpha1`: `Runtime`, `Sandbox`, `SandboxTemplate`, `SandboxWarmPool`, and `SandboxClaim`. The chart ships one CRD per kind in `charts/lenny/crds/`, and `pkg/embedded/crds/` mirrors them for the embedded stack **[source]**. |
| WarmPoolController | Lenny's leader-elected controller that creates each pool's `Sandbox` objects and their pods, renders the pods, projects each `Sandbox`'s phase from its claim, and garbage-collects orphaned claims. Its Sandbox-to-Pod reconciler lives in `pkg/controller/sandbox`, and its pool planner in `pkg/controller/warmpool` **[source]**. |
| PoolScalingController | Lenny's controller that renders each pool record in Postgres into a `SandboxTemplate` and a `SandboxWarmPool` and sets the warm targets ([§4.6.2](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration)) **[source]**. |
| Pool record | The pool's row in Postgres, which the admin API writes. The `SandboxTemplate` and `SandboxWarmPool` carry the part of it the controllers and the pool-config webhook read. |
| Occupancy claim | Lenny's `SandboxClaim`: one per pod, named `claim-<podName>`, created by the gateway when it acquires an idle pod and held across every session on the pod and the reserved hold after them ([§4.6.1](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle) **Pod claim mechanism (per-pod occupancy claim):**). |
| Reserved hold | The period after a recycling pod's scrub during which the pod waits for its pinned tenant's next session before it returns to idle inventory (§4.6.1 **Reserved hold (claim retention across same-tenant sessions):**). |
| Pinned idle inventory | Idle pods pinned to a tenant by an earlier session, which only that tenant can claim ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). |
| Slot count | The number of slot containers the controller rendered into a pod, equal to the pool's `maxConcurrentSessionsPerPod` when the pod was created ([SI-4](session-isolation-design.md#si-4-slot-liveness-and-the-slot-count)). |
| Schema-version annotation | `lenny.dev/schema-version` on each Lenny CRD. Each controller compares it at startup with the value its binary expects, and `lenny-preflight` compares it with the value the chart release expects; a mismatch is fatal ([§10.5 Upgrade and Rollback Strategy](../spec/10_gateway-internals.md#105-upgrade-and-rollback-strategy) **Helm CRD upgrade limitation:**). The value is "1" today in the types, the CRDs, and `preflight.CurrentCRDSchemaVersion` **[source]**. |
| Pod lifecycle interfaces | `PoolReader`, `PodLifecycleManager`, and `PoolManager`, which §4.6.1 **Internal abstraction layer:** defines and `pkg/podlifecycle/interfaces.go` declares **[source]**. |
| Reconsideration trigger | The one recorded statement of the upstream changes that would make adopting upstream worth reconsidering (D3, "PLC-6"). |

### Labels from other designs

| Label | Where | Meaning here |
| :-- | :-- | :-- |
| PL-8 | [`agent-pod-layout-design.md`](agent-pod-layout-design.md#pl-8-removal-of-pre-launched-runtimes) | Removal of pre-launched runtimes, with `sdkWarmDisabled`, `status.sdkWarmCircuitBreaker`, `sdkConnectTimeoutSeconds`, and the phase `sdk_connecting`. |
| PL-9 | [`agent-pod-layout-design.md`](agent-pod-layout-design.md#pl-9-pool-settings-and-renames) | The rename of `sessionPolicy.maxConcurrentSessions` to `sessionPolicy.maxConcurrentSessionsPerPod` and of `acknowledgeProcessLevelIsolation` to `sessionPolicy.acknowledgeSharedPod`, with no alias, in the pool schema and its CRDs. |
| SI-3, SI-4 | [`session-isolation-design.md`](session-isolation-design.md#si-4-slot-liveness-and-the-slot-count) | SI-3 removes `recycle.runtimeProcess`. SI-4 records the slot count on the `Sandbox` when the pod is created and gives the field no name. |
| AR-2 | [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md#ar-2-service-mode) | Removal of service mode, with `executionMode` and `maxConcurrent` on the `SandboxTemplate` and the `Sandbox`. |
| AR D6 | [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md#decisions) | The owner's one rule for removed fields: a write that names a removed field is refused with an error that names the field. |
| BR-1 | [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md#resource-classes-in-configuration) | Resource classes defined in a chart ConfigMap, checked at write, load, and reconcile, with the `SandboxTemplate` enum removed. |
| DR-5, DR-10 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#dr-5-owned-pools) | Owned pools rendered as a `SandboxTemplate` and `SandboxWarmPool` pair, and capacity reservation with `Sandbox.spec.reserved` and `SandboxWarmPool` `reservedPods` ([DR-10](tenant-derived-runtimes-design.md#dr-10-capacity-reservation)). |
| PM-5, PM-6 | [`placement-design.md`](placement-design.md#pm-6-enforcement) | The resolved placement on `SandboxTemplate.spec`, and the template conditions `PlacementRefused` and `PlacementUnschedulable`. |
| TSS-9 | [`tenant-security-settings-design.md`](tenant-security-settings-design.md#tss-9-removal-of-workspacetier) | Removal of `workspaceTier`, including from `SandboxTemplate`. |
| LU-4 | [`llm-upstreams-and-cost-budgets-design.md`](llm-upstreams-and-cost-budgets-design.md#lu-4-removal) | Removal of credential `deliveryMode`, which the `SandboxTemplate` and the `Sandbox` carry today. |

## High-level design

Lenny runs its own controllers for its own resource kinds, and the specification says so. The kinds keep the names upstream gave them, because the names describe what the objects are: a sandbox is one agent pod, a template defines a pool, a warm pool keeps pods ready, and a claim marks a pod as taken. The kinds live in Lenny's `lenny.dev` group, so they coexist with an upstream installation in the same cluster, and Lenny does not install, import, pin, or follow upstream (D3).

Where a Lenny field records the same fact as an upstream field, it takes upstream's name. The fields that qualify are the backing pod's IP addresses on the `Sandbox`, which become `status.podIPs`, and the template a warm pool builds from, which becomes `spec.sandboxTemplateRef.name`. Nothing is deployed, so the renames ship with no alias and no migration, and the CRD schema version moves to "2" so that a stale development cluster fails preflight with the existing message ("PLC-2").

Where a kind means something different from upstream's kind of the same name, the specification and the documentation say how. A Lenny claim is a lock on a pod the gateway has already chosen. A Lenny template is a pool definition with no pod spec. A Lenny warm pool is sized by minimum and maximum warm counts that the PoolScalingController computes, and it keeps its pods after they are claimed and takes them back after scrub ("PLC-3").

The specification's own errors about these kinds are corrected: the field placement in its kind table, the ADR-007 premise, the count of CRDs that preflight checks, the pool field that sets the slot count, and the resource class enum ("PLC-4"). The Go types behind the pod lifecycle interfaces take names that describe what they act on ("PLC-5").

One statement replaces the dependency text: adopting upstream is worth reconsidering if upstream ships several sandboxes per pod with a way to return a pod to its pool, or moves snapshots into its controller ("PLC-6").

## Decisions

| # | Question | Owner's answer | Applied in |
| :-- | :-- | :-- | :-- |
| D1 | What the specification says about the pod lifecycle controllers (2026-11-15). | "1 and 2: ok", to the review's option (a): the specification describes Lenny's own controllers modelled on agent-sandbox's resource kinds, and Lenny renames its fields where the meaning already matches upstream. The decision covers the spec rewrite of §4.6.1 and §17.6, the field renames, the spec and code mismatches the review found, and the cleanup of the `AgentSandbox*` Go type names. | PLC-1, PLC-2, PLC-4, PLC-5 |
| D2 | Whether Lenny keeps upstream's kind names (2026-11-15). | "1 and 2: ok", to keeping the names `Sandbox`, `SandboxTemplate`, `SandboxClaim`, and `SandboxWarmPool` in Lenny's own `lenny.dev` group, with the specification stating where their semantics differ from upstream's. | PLC-3 |
| D3 | Whether Lenny stops tracking and using upstream (2026-11-15). | The owner asked: "This means the project should stop tracking and using the agent sandbox upstream, right?" The answer given and recorded: yes for using it, and the code already has no dependency. The spec's dependency text goes: "built on", the pinned release, the v0.x upgrade policy, the go/no-go criteria, and the fallback plan, because Lenny's own controllers are that fallback. Tracking is reduced to one recorded statement: option (b), adopting upstream, would be worth reconsidering if upstream ships several sandboxes per pod with a way to return a pod to its pool, or moves snapshots into its controller. Nobody follows upstream releases. | PLC-1, PLC-6 |

## Open decisions

| ID | Question | Options | Recommendation |
| :-- | :-- | :-- | :-- |
| O1 | Whether the `Sandbox` gains upstream's condition types. The review named "the condition names" among the renames. No Lenny condition shares a meaning with an upstream one: the `Sandbox` carries `SOPeercredDisabled` alone, and the template carries `PoolWarmingUp`, `PoolDrained`, `SecurityDegradedMode`, `Degraded`, and, under PM-6, `PlacementRefused` and `PlacementUnschedulable` **[source]**. Upstream's `Sandbox` conditions are `Ready`, `Suspended`, `PodScheduled`, and `Finished` **[external]**. Taking a name therefore adds a condition and renames nothing. | (a) Add no condition. (b) Add `PodScheduled` to `Sandbox.status.conditions`, mirrored from the pod's own `PodScheduled` condition with its status, reason, and message copied as upstream does, and absent while the `Sandbox` has no pod. (c) Add `PodScheduled` and `Ready`. | (b). An operator sees why a warm pod is unscheduled on its `Sandbox`, beside PM-6's pool-level `PlacementUnschedulable`, and the WarmPoolController already watches the pod. `Ready` is left out because `status.phase` is the record the gateway and the claim path read for readiness, and a second carrier of the same fact can disagree with it. `Suspended` and `Finished` have no Lenny counterpart, because a Lenny pod is never suspended in place and a terminal pod projects `failed` or `terminated`. |
| O2 | Whether §4.6.1 keeps `SnapshotPod` and `RestoreFromSnapshot` as reserved extension points "for GKE Pod Snapshots integration". Section 4.1 of the review finds that a GKE snapshot captures every slot of a pod together, writes the per-slot memory volumes that hold credentials and the connection nonce to object storage, gives the restored pod a new IP, and invalidates the pinned broker roots of SI-5. | (a) Remove the reservation. (b) Keep it, narrowed to whole-pod snapshots of warm pods that hold no session. | (a). The reservation names a vendor feature that conflicts with the slot model, and no code or test refers to it **[source]**. A later design that adds snapshots states its own interface. PLC-6's trigger covers the case where upstream moves snapshots into its controller. |

## Requirements

| ID | Requirement |
| :-- | :-- |
| PLCR-1 | No specification section, document, chart value, or code comment states that Lenny is built on, depends on, pins, wraps, or defaults to `kubernetes-sigs/agent-sandbox`. The references that remain name upstream as the origin of the kind names, state where Lenny's kinds differ, and state the reconsideration trigger (D3). |
| PLCR-2 | `go.mod` lists no `sigs.k8s.io/agent-sandbox` module, and no Lenny chart or manifest installs a CRD in `agents.x-k8s.io` or `extensions.agents.x-k8s.io` (D3). |
| PLCR-3 | `Sandbox.status.podIPs` is a list of strings holding the `ip` members of the backing pod's `status.podIPs` in order, empty until the pod has an address. No `Sandbox` carries `status.podIP` (PLC-2). |
| PLCR-4 | The gateway dials a pod's adapter at the first entry of `Sandbox.status.podIPs`, and refuses to dial when the list is empty, as it does today for an empty `podIP` (PLC-2). |
| PLCR-5 | `SandboxWarmPool.spec.sandboxTemplateRef.name` is required and names the pool's `SandboxTemplate`. `lenny-pool-config-validator` refuses a `SandboxWarmPool` write whose spec carries `templateRef`, with a message that names the field and its replacement (AR D6, PLC-2). |
| PLCR-6 | Every Lenny CRD and `preflight.CurrentCRDSchemaVersion` carry schema version "2", and a CRD at "1" fails the controller startup check and `lenny-preflight` with their existing messages (PLC-2). |
| PLCR-7 | §4.6.1 states, for each Lenny kind that shares a name with an upstream kind, how its meaning differs from upstream's (D2, PLC-3). |
| PLCR-8 | The §17.6 CRD check and its documentation name every Lenny CRD the chart ships, `Runtime` included, and check the schema-version annotation (PLC-4). |
| PLCR-9 | The `SandboxTemplate` carries `spec.sessionPolicy.maxConcurrentSessionsPerPod`, and the `Sandbox` records its slot count in `spec.slotCount`, set at creation and never changed (PLC-4). |
| PLCR-10 | The implementations of the pod lifecycle interfaces are `CRDPoolReader`, `CRDPodLifecycleManager`, and `CRDPoolManager`, and no Go identifier, file name, or test name in `pkg/podlifecycle` or `cmd/lenny-gateway` contains `AgentSandbox` (PLC-5). |

## Current state

### Specification

Each place below names upstream as a dependency or carries a mismatch the review found **[source]**:

| Section | What it says |
| :-- | :-- |
| [§3 High-Level Architecture](../spec/03_high-level-architecture.md#3-high-level-architecture) | The diagram labels the controller "Warm Pool Controller (pod lifecycle, agent-sandbox CRDs)". |
| [§4.6.1](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle), **Implementation:** | "Built on **`kubernetes-sigs/agent-sandbox`** (launched at KubeCon Atlanta, November 2025)", with upstream maintenance, "Pod Snapshots on GKE", and a SIG Apps claim model as the benefits. |
| §4.6.1, **Internal abstraction layer:** | "never directly with agent-sandbox CRD types"; "Default implementations: `AgentSandboxPodLifecycleManager` and `AgentSandboxPoolManager`, which delegate to agent-sandbox CRDs"; the reserved `SnapshotPod`/`RestoreFromSnapshot` "for GKE Pod Snapshots integration"; and "a breaking upstream change or a decision to replace agent-sandbox". |
| §4.6.1, **CRD mapping from agent-sandbox:** and **API group:** | The label names upstream. **API group:** says "All four CRDs" and calls the fallback "replacing agent-sandbox with custom controllers". The table header is "Agent-Sandbox CRD", with a "Replaces (old Lenny CRD)" column. The `SandboxTemplate` row lists "warm count range, scaling policy", and the `SandboxWarmPool` row says "Upstream warm pool management with configurable `minWarm`/`maxWarm`". |
| §4.6.1, **ADR-007: `SandboxClaim` optimistic-locking and failover fencing verification — Phase 1 blocking prerequisite.** | "verify that `SandboxClaim`'s optimistic-locking semantics match Lenny's claim approach", which presumes an upstream claim that Lenny uses. |
| §4.6.1, **CRD validation:** | Names `sessionPolicy.maxConcurrentSessions >= 1` and "resource class values within the allowed set". |
| §4.6.1, **Dependency pinning and upgrade policy:**, **Go/no-go criteria for the agent-sandbox dependency:**, and **Fallback plan if agent-sandbox is abandoned or diverges:** | The pinned release, "a v0.x project", the go/no-go gate and its three criteria, and the "2-3 engineering-weeks" estimate. |
| [§4.6.3 CRD Field Ownership and Write Boundaries](../spec/04_system-components.md#463-crd-field-ownership-and-write-boundaries) | The `SandboxWarmPool` spec row lists `spec.minWarm`, `spec.maxWarm`, `spec.scalePolicy`, and `spec.sdkWarmDisabled` for the PoolScalingController and does not name the template reference. |
| [§17.1 Kubernetes Resources](../spec/17_deployment-topology.md#171-kubernetes-resources) | The Warm Pool Controller row: "(default implementation: `kubernetes-sigs/agent-sandbox` CRDs)". |
| [§17.6 Packaging and Installation](../spec/17_deployment-topology.md#176-packaging-and-installation), "Checks performed" | The "Agent-sandbox CRDs" row: "Verify all four `agent-sandbox` CRDs", lists `sandboxtemplates`, `sandboxwarmpools`, `sandboxes`, and `sandboxclaims`, checks `spec.versions[*].name`, and answers "agent-sandbox CRD '<name>' not found". |
| [§18.2 Phase Map](../spec/18_build-sequence.md#182-phase-map) | Row 2 depends on "**Phase 1** (agent-sandbox)". |
| [§18.6 Phase 2](../spec/18_build-sequence.md#186-phase-2--adapter-protocol-make-run-imageresolver-startup-benchmark) | The deliverable "Documented upstream-contribution cadence for `kubernetes-sigs/agent-sandbox`", and **Prerequisites.**: "The agent-sandbox dependency health assessment in §4.6.1 must be resolved before Phase 2 begins". |
| [§18.9 Phase 3](../spec/18_build-sequence.md#189-phase-3--pool-scaling-delegation-policy-runtime-upgrade-mtls) | "`RuntimeProvider` abstraction ... default implementation wraps `kubernetes-sigs/agent-sandbox`". |
| [§18.40 Hard Prerequisite Chain](../spec/18_build-sequence.md#1840-hard-prerequisite-chain) | Item 2, "Phase 1 → Phase 2 (agent-sandbox dependency health)". |
| [§19 Resolved Decisions](../spec/19_resolved-decisions.md#19-resolved-decisions) | Row 4, "Controller framework": "`kubernetes-sigs/agent-sandbox` for pod lifecycle CRDs". Row 14 lists "`kubernetes-sigs/agent-sandbox` upstream compatibility" among the license criteria. |
| [§23 Competitive Landscape](../spec/23_competitive-landscape.md#23-competitive-landscape) and [§23.2 Community Adoption Strategy](../spec/23_competitive-landscape.md#232-community-adoption-strategy) | The `kubernetes-sigs/agent-sandbox` row, "adopted as Lenny's infrastructure layer", and the **Open-source license.** paragraph's criterion "compatibility with `kubernetes-sigs/agent-sandbox` upstream". |

The specification names neither `status.podIP` nor `templateRef`, so the renames add field names to the specification rather than changing existing ones **[source]**.

### Tree

| Area | What exists **[source]** |
| :-- | :-- |
| `pkg/apis/lenny/v1alpha1` | `SandboxStatus.PodIP` (`json:"podIP"`), which the comment says the gateway dials. `SandboxWarmPoolSpec.TemplateRef` (`json:"templateRef"`, required), with the printer column `.spec.templateRef`. `SandboxTemplateSpec.MaxConcurrent`, commented "the per-pod slot count for service mode", and `SessionPolicy` with `Recycle` alone, so the template carries no session concurrency. `SandboxTemplateSpec.ResourceClass` with `+kubebuilder:validation:Enum=small;medium;large`, while `SandboxSpec.ResourceClass` is commented "small, medium, large, or a deployer-defined class". Every kind carries `lenny.dev/schema-version=1` and `PreserveUnknownFields` on spec and status. |
| `charts/lenny/crds/` and `pkg/embedded/crds/` | One CRD per kind, `Runtime` included. The `podIP` and `templateRef` schema entries and the `templateRef` printer column. |
| `pkg/preflight/crdschema.go` | `CurrentCRDSchemaVersion = "1"` and `LennyCRDNames`, which lists all the chart's CRDs, `runtimes.lenny.dev` included. The check reads the schema-version annotation and does not read `spec.versions`. |
| `pkg/controller/sandbox` | Names the pod after its `Sandbox`, records the name in `status.podName`, and copies the pod's `status.podIP` into the `Sandbox`. Sets the `SOPeercredDisabled` condition alone. |
| `pkg/controller/warmpool` | The pool is the controller owner of each `Sandbox` (`SetControllerReference`). Carries `status.podIP` through its status patches in `gc.go`, `pod_reconciler.go`, and `occupancy.go`. Sets `PoolWarmingUp`, `PoolDrained`, `SecurityDegradedMode`, and `Degraded` on the template. |
| `pkg/gateway/podlifecycle/podsession` | Dials the adapter at `Sandbox.status.podIP` (`poddial.go`) and reads it in `bindclaim.go`, `bindlaunch.go`, `resume.go`, and `slotbinder.go`. |
| `pkg/podregistry/crd.go`, `cmd/lenny-gateway/coordination_seams.go`, `tests/testinfra/lifecycleharness` | Read `Sandbox.status.podIP` into `PodRecord.PodIP` and the re-adoption binding. `PodRecord.PodIP` is the pod registry's own field and stays. |
| `TemplateRef` users | `pkg/admission/webhook`, `pkg/admission/pool_config_validator`, `pkg/controller/poolscaling`, `pkg/controller/sandbox`, `pkg/controller/warmpool`, `pkg/embedded/stack`, `pkg/gateway/podlifecycle/podsession`, `pkg/gateway/sessionserver`, `pkg/ops/backup/runner`, `pkg/podlifecycle`, `cmd/lenny-gateway`, the tier 2, 3, 4, 7a, 8, and 9 tests, and the templates `tests/testinfra/k8s/agent-workload-load.yaml.tmpl`, `runtimeclass-pool-gvisor.yaml.tmpl`, and `runtimeclass-pool-kata.yaml.tmpl`. |
| `pkg/podlifecycle` | `agentsandbox.go` defines `AgentSandboxPoolReader`, `AgentSandboxPodLifecycleManager`, and `AgentSandboxPoolManager`, with the comment "the agent-sandbox types are the v1 PoolReader, PodLifecycleManager, and PoolManager". `interfaces.go` and `errors.go` describe the package against "the agent-sandbox CRDs". Tests: `agentsandbox_test.go`, `agentsandbox_errorpaths_test.go`, and `poolmanager_binding_test.go` with `TestAgentSandboxIsTheDefaultPoolManager_spec_17_1`. `cmd/lenny-gateway/reconciler_workers.go` constructs `AgentSandboxPodLifecycleManager`. |
| `tests/tier9_security/credential_lookup_failclosed_test.go` | `lookupOutageAgentSandbox` names a test fixture and has no relation to upstream. It stays. |

### Documentation

The documentation names upstream in these places **[source]**, found with `grep -rn -i agent-sandbox docs/`, which returns these lines, and in `README.md` and `TESTING.md`:

| File | What it says |
| :-- | :-- |
| `docs/index.md` | The Warm Pool Controller row: "Creates and retires agent pods through the `kubernetes-sigs/agent-sandbox` CRDs". |
| `docs/operator-guide/installation.md` | The preflight row: "Confirms all four agent-sandbox CRDs are installed at expected versions". |
| `docs/tutorials/deploy-to-cluster.md` | Preflight output: "[PASS] Agent-sandbox CRDs: all 4 CRDs at expected versions". |
| `docs/getting-started/architecture.md` | The diagram node "Warm Pool Controller<br/>(agent-sandbox CRDs)", and "Built on the **`kubernetes-sigs/agent-sandbox`** project, which provides the CRD-based warm pool management primitives". Its CRD table repeats the specification's `SandboxTemplate` row with "warm count range, scaling policy". |
| `docs/assets/diagrams/architecture-overview.svg` | The text "Pod lifecycle, agent-sandbox CRDs, claim and drain". |
| `docs/reference/glossary.md` | "Sandbox CRD": "The `kubernetes-sigs/agent-sandbox` Custom Resource Definition used by Lenny ... Adopted as Lenny's infrastructure layer." "Warm Pool Controller": "via the `kubernetes-sigs/agent-sandbox` CRDs". The "SandboxTemplate" entry calls the template a pod template with "container image, resource limits, RuntimeClass, environment variables", which it does not hold, and the "CRD" entry lists `RuntimeUpgrade`, which is not a CRD. |
| `docs/adr/index.md` | ADR-0004, "`kubernetes-sigs/agent-sandbox` + controller-runtime", Planned. |
| `docs/adr/template.md` | The example driver "Stay compatible with `kubernetes-sigs/agent-sandbox` CRD contract". |
| `docs/adr/0007-sandboxclaim-optimistic-locking.md` | "Related ADRs: ADR-0004 (`kubernetes-sigs/agent-sandbox` for pod lifecycle CRDs)". |
| `docs/adr/0008-open-source-license.md` | The context sentence "The project builds on `kubernetes-sigs/agent-sandbox`" and the driver "**Upstream compatibility.** The license must be compatible with `kubernetes-sigs/agent-sandbox`". |
| `docs/about/governance.md` | "**Ecosystem alignment.** Compatible with `kubernetes-sigs/agent-sandbox` (Apache 2.0)". |
| `README.md` | The Warm Pool Controller row, as in `docs/index.md`. |
| `TESTING.md` | The tier 5 row's backing services: "Kind cluster, Helm install, agent-sandbox CRDs". |

No document names `status.podIP` or `templateRef` **[source]**.

### Records

Every `BUILD-GAPS.md` finding that cites the dependency or its fallback is closed, and no open finding depends on it **[source]**:

| Finding | State | What it cites |
| :-- | :-- | :-- |
| F-4.6.17 | Closed | The §4.6.1 fallback plan's "2-3 engineering-weeks" estimate, as the reason the pod lifecycle interfaces were needed. Its resolution added `pkg/podlifecycle` with the `AgentSandbox*` types. |
| F-17.1.11 | Closed | The §17.1 row's "default implementation: `kubernetes-sigs/agent-sandbox` CRDs". Its resolution names `TestAgentSandboxIsTheDefaultPoolManager_spec_17_1`. The §17.1 audit's summary scoreboard row "Agent Pods" records the same gap. |
| F-17.6.1 | Closed | Preflight checks, including "agent-sandbox CRD presence + schema version (`crdschema.go`)". |
| F-6.3.10 | Closed | Quotes §18.2 row 2, "Phase 1 (agent-sandbox)". |
| F-5.3.9 | Closed | The §5.3 `RuntimeProvider` abstraction, deferred past v1, which §18.9 says "wraps `kubernetes-sigs/agent-sandbox`". |
| F-5.2.70, F-5.2.71, F-5.2.72 | Open | Resource class content, the `SandboxTemplate` enum, and an unknown class. BR-1 owns them. They are listed here because PLC-4's resource class item depends on BR-1. |

`TEST-GAPS.md` cites `pkg/podlifecycle/agentsandbox_test.go` by path in one entry. It is an audit record and stays as written.

## PLC-1 The specification describes Lenny's controllers

**§4.6.1 Implementation.** The paragraph states what Lenny runs **[design]**: the WarmPoolController and the PoolScalingController are Lenny's own controllers, built with controller-runtime, acting on the Lenny kinds in the `lenny.dev` group. The kinds are modelled on the resource kinds of `kubernetes-sigs/agent-sandbox` and keep their names, and Lenny does not depend on, install, or import that project. The paragraph ends with the reconsideration trigger of "PLC-6". The gateway, the adapter protocol, credential leasing, delegation, and the rest of the platform remain outside the controllers, as today's last sentence says, without the reference to upstream.

**§4.6.1 Internal abstraction layer.** "never directly with agent-sandbox CRD types" becomes "never directly with the CRD types". The default implementations are `CRDPodLifecycleManager` and `CRDPoolManager`, which act on the `lenny.dev` resources through the Kubernetes API ("PLC-5"). The closing sentence states that a change to the resource schemas or a different backend changes only the implementations behind the interfaces. The future extension points keep `PodDebugger` and `clusterID`, and O2 decides `SnapshotPod` and `RestoreFromSnapshot`.

**§4.6.1 resource kinds.** The label **CRD mapping from agent-sandbox:** becomes **Resource kinds:**. **API group:** states that every Lenny kind is registered under `lenny.dev`, that the chart ships one CRD per kind, and that the group lets the CRDs coexist with an upstream installation, whose kinds are in other groups. The sentence about the fallback plan goes. The table loses the "Replaces (old Lenny CRD)" column, which records names no code carries, gains a `Runtime` row with a pointer to [§5.1 Runtime](../spec/05_runtime-registry-and-pool-model.md#51-runtime), and gains the "Difference from upstream" column of "PLC-3".

**Removed paragraphs.** **Dependency pinning and upgrade policy:**, **Go/no-go criteria for the agent-sandbox dependency:**, and **Fallback plan if agent-sandbox is abandoned or diverges:** are deleted (D3). They are bold labels inside §4.6.1, so no heading anchor retires, and no section, document, or code comment cites any of them by name **[source]**. The reconsideration trigger, the one part of the tracking that survives, sits in **Implementation:** ("PLC-6").

**Elsewhere in the specification.**

- §3's diagram label becomes "Warm Pool Controller (pod lifecycle, Lenny CRDs)".
- §17.1's row follows "PLC-5".
- §18.2 row 2 depends on "**Phase 1**".
- §18.6 loses the upstream-contribution deliverable and the **Prerequisites.** sentence about the dependency assessment. The Phase 1.5 exit gate stays as the prerequisite.
- §18.9's `RuntimeProvider` item keeps the extension point and drops "default implementation wraps `kubernetes-sigs/agent-sandbox`".
- §18.40 loses item 2, and the items after it move up by one. No section, document, or test cites a §18.40 item by number **[source]**.
- §19 row 4, "Controller framework", reads: Lenny's own controllers for the pod lifecycle kinds in the `lenny.dev` group and for the PoolScalingController, built with kubebuilder (controller-runtime), with a pointer to §4.6.1.
- §19 row 14 and §23.2's **Open-source license.** paragraph replace the named criterion with compatibility with the Apache-2.0 cloud-native dependency graph, such as `controller-runtime`. MIT stays selected, and the other criteria are unchanged.
- §23's row describes upstream as a pod lifecycle project for single sandboxes per pod, whose kind names Lenny's kinds take, and states that Lenny does not depend on it, with a pointer to §4.6.1.

## PLC-2 Field renames

The fields below take upstream's names, because each records the fact that upstream's field of that name records **[design]**:

| Old field | New field | CRD | Reason |
| :-- | :-- | :-- | :-- |
| `status.podIP`, a string | `status.podIPs`, a list of strings | `Sandbox` (`sandboxes.lenny.dev`) | Upstream's `Sandbox.status.podIPs` holds "the IP addresses of the underlying pod" as a list of strings **[external]**, and Lenny's field holds the same pod's address. The list carries both addresses on a dual-stack cluster. The WarmPoolController copies the `ip` member of each entry of the pod's `status.podIPs` in order, and the gateway dials the first entry, which Kubernetes requires to equal the pod's `status.podIP` **[external]**. |
| `spec.templateRef`, a string | `spec.sandboxTemplateRef.name`, an object with a `name` member | `SandboxWarmPool` (`sandboxwarmpools.lenny.dev`) | Upstream's `SandboxWarmPool.spec.sandboxTemplateRef.name` names the template the pool builds its members from **[external]**, and Lenny's field names the same thing. The printer column "Template" reads `.spec.sandboxTemplateRef.name`. |

The review named `podIPs`, `sandboxTemplateRef`, and the condition names. Each field below was checked against the CRDs and the types and is left as it is **[source]** **[external]**:

| Field | Why it keeps its name |
| :-- | :-- |
| `Sandbox.status.nodeName`, `observedGeneration` on every kind | They already carry upstream's names. |
| `Sandbox.status.conditions` | No Lenny condition shares a meaning with an upstream condition. O1 decides whether `PodScheduled` is added. |
| `Sandbox.status.podName` | Upstream has no such field, because its pod takes the `Sandbox`'s name. Lenny's pod also takes the `Sandbox`'s name, so the field repeats it. It stays, because the printer column "Pod" and the pod registry read it, and removing it is not a rename. |
| `SandboxWarmPool.status.warmCount` and `readyCount` | Upstream's `status.replicas` and `readyReplicas` count unclaimed members against a `spec.replicas` with a `/scale` subresource. Lenny's counts exclude tenant-pinned idle pods and pair with no `spec.replicas`, so upstream's names would suggest a scale target that does not exist ("PLC-3"). |
| `SandboxWarmPool.spec.minWarm` and `maxWarm` | Upstream's `replicas` is a single target driven by HPA or KEDA. The meaning differs ("PLC-3"). |
| `Sandbox.spec.poolRef` | Upstream records pool membership by owner reference and labels, and moves ownership to the claim. No upstream field means the same. |
| `SandboxClaim.spec.sandboxRef` | Upstream's claim names a warm pool in `spec.warmPoolRef` and learns its sandbox in `status.sandbox.name`, chosen by its controller. Lenny's gateway chooses the pod before it creates the claim. |
| `Sandbox.status.phase`, `SandboxClaim.status.phase` | Upstream's kinds have no phase. |


**No alias and no migration.** Nothing is deployed, so neither field keeps its old name in any form, and no conversion or data migration runs (the owner's no-backward-compatibility rule). Under AR D6, a write that names a removed field is refused with an error that names it: `lenny-pool-config-validator`, which already reviews `SandboxWarmPool` writes ([§17.2](../spec/17_deployment-topology.md#172-namespace-layout)), refuses a `SandboxWarmPool` whose spec carries `templateRef`, and its message names `spec.sandboxTemplateRef.name` as the replacement **[design]**. `Sandbox.status` has one writer, the WarmPoolController, so `status.podIP` leaves with no refusal, and the static check of "Tests" finds no writer of it.

**Schema version.** The renames change two CRD schemas so that an object valid under the old schema is refused or misread under the new one. `lenny.dev/schema-version` moves from "1" to "2" on every Lenny CRD, and `preflight.CurrentCRDSchemaVersion` moves to "2" in the same change, because the check compares every CRD with one value and the type comments require the marker and the constant to move together **[source]** **[design]**. The controller startup check and `lenny-preflight` then report a CRD at "1" with their existing messages, for example `CRD '<name>' schema version is '1'; expected '2'. Apply updated CRDs before running helm upgrade.` The API version stays `v1alpha1`: one version is served, so no conversion webhook entry and no stored-version migration is needed. A development cluster that ran an earlier build is reinstalled.

**Code.** `SandboxStatus.PodIP string` becomes `PodIPs []string` with the JSON name `podIPs`, and a method `PrimaryPodIP()` returns the first entry or an empty string, so the gateway's readers keep one call. `SandboxWarmPoolSpec.TemplateRef string` becomes `SandboxTemplateRef SandboxTemplateRef`, a struct with `Name string` **[design]**. "Non-specification changes" lists the readers and writers.

## PLC-3 Semantic differences from upstream

§4.6.1's kind table gains a column, "Difference from upstream", and the glossary and the architecture page state the same differences **[design]**. Upstream's side of each row comes from the review, sections 1.2, 1.3, and 2.2 **[external]**.

**`Sandbox`.**

- One object per agent pod, with the pod named after it, as upstream does.
- It holds no pod template. The controller renders the pod from the pool's `SandboxTemplate`, the referenced `Runtime`, and the pool's resolved placement (PM-5).
- It has no `operatingMode`. A Lenny pod is never suspended in place: a session suspension checkpoints the slot's files and releases the slot ([`session-lifecycle-design.md`](session-lifecycle-design.md)), and the pod stays.
- It has no `shutdownTime` or `shutdownPolicy`. A pod retires through its pool's recycle limits and uptime limit.
- It has no `failedPodPolicy`. A failed pod is retired and replaced through its pool, and a dead slot counts toward the whole-pod replacement trigger of SI-4.
- It has no Service and no `serviceFQDN`. The gateway dials the first entry of `status.podIPs`.
- `status.phase` is Lenny's own occupancy projection of the pod's claim, which upstream's kinds do not carry.
- The pool owns the `Sandbox` for its whole life, and the `Sandbox` serves many sessions in turn.

**`SandboxTemplate`.**

- It defines a pool and holds no `PodSpec` and no volume claim templates. It names the runtime and carries the pool settings that the WarmPoolController, the pod builder, and `lenny-pool-config-validator` read.
- The PoolScalingController writes it from the pool record ([§4.6.3](../spec/04_system-components.md#463-crd-field-ownership-and-write-boundaries)), and neither a user nor an operator writes it directly.
- It generates no NetworkPolicy. The pool's pods carry labels that select NetworkPolicies the chart creates per egress profile.
- It has no environment or volume injection policies. Per-session inputs travel over the adapter protocol.

**`SandboxWarmPool`.**

- It is sized by `minWarm` and `maxWarm`, which the PoolScalingController computes from demand, schedules, and bootstrap data ([§4.6.2](../spec/04_system-components.md#462-poolscalingcontroller-pool-configuration)). It has no `replicas` and no `/scale` subresource, so an HPA or KEDA cannot size it.
- A claimed pod stays a member. After its sessions end, the pod is scrubbed, held for its pinned tenant through the reserved hold, and returned to idle inventory. Upstream moves a claimed sandbox's ownership to its claim, and the sandbox never returns.
- `status.warmCount` and `status.readyCount` leave out pinned idle pods, which only their tenant can claim.
- It has no `updateStrategy`. A runtime revision gets a pod set of its own ([`runtime-revisions-design.md`](runtime-revisions-design.md)).

**`SandboxClaim`.**

- It is a lock the gateway takes on a pod it has already chosen, named `claim-<podName>` and naming the pod in `spec.sandboxRef`. Upstream's claim names a warm pool, and upstream's claim controller chooses or cold-starts the sandbox.
- No controller fulfils it. Creation either succeeds or fails with `AlreadyExists`, and `lenny-sandboxclaim-guard` refuses a second claim on a pod.
- It does not own its `Sandbox`, carries no owner reference, and is deleted explicitly at hold expiry, at a release that ends the occupancy, by orphan collection, or when the pod terminates.
- It lasts one occupancy episode, which spans every session on the pod and the reserved hold after them. It has no `shutdownTime`, `ttlSecondsAfterFinished`, or per-claim `env`, volume claim templates, or pod metadata.

**`Runtime`.** It has no upstream counterpart.

## PLC-4 Spec and code mismatches

Each mismatch below gets the stated fix **[design]**:

| Mismatch | Ruling | Fix |
| :-- | :-- | :-- |
| §4.6.1's kind table puts "warm count range, scaling policy" on `SandboxTemplate`. The code puts `minWarm`, `maxWarm`, and `scalePolicy` on `SandboxWarmPool` **[source]**. | The code is right: the PoolScalingController owns the warm targets on the warm pool, as §4.6.3 already says. | The `SandboxTemplate` row describes the pool definition. The `SandboxWarmPool` row names `spec.sandboxTemplateRef.name`, `minWarm`, `maxWarm`, and `scalePolicy`. §4.6.3's PoolScalingController row for `SandboxWarmPool` gains `spec.sandboxTemplateRef`, and PL-8 removes `spec.sdkWarmDisabled` and the `status.sdkWarmCircuitBreaker` carve-out from it. |
| The `SandboxWarmPool` row says "Upstream warm pool management with configurable `minWarm`/`maxWarm`". Upstream's warm pool has `replicas` **[external]**. | Lenny's warm pool is its own type. | The row states Lenny's warm-pool semantics and their difference from upstream ("PLC-3"). |
| The ADR-007 paragraph asks to verify "that `SandboxClaim`'s optimistic-locking semantics match Lenny's claim approach", which presumes upstream's claim. | Lenny's claim is its own type. | The label becomes **ADR-007: `SandboxClaim` optimistic locking and failover fencing.** The paragraph states that ADR-007 records the verification that the API server's create-time name uniqueness and `resourceVersion` preconditions on Lenny's `SandboxClaim` fence concurrent claims and a failed-over leader's in-flight writes. Its required tests stay as written, and **Why optimistic locking provides sufficient fencing:** is unchanged. |
| §17.6 checks "all four `agent-sandbox` CRDs". The chart ships one more, `runtimes.lenny.dev`, and `LennyCRDNames` checks all of them **[source]**. The row checks `spec.versions[*].name`, and the code checks the schema-version annotation, as §10.5 states. | The code is right on both counts. A schema change under one served version keeps the version name, so a name check cannot find a stale schema. | The row is renamed "Lenny CRDs" and verifies that every Lenny CRD the chart ships exists, naming `runtimes.lenny.dev`, `sandboxes.lenny.dev`, `sandboxtemplates.lenny.dev`, `sandboxwarmpools.lenny.dev`, and `sandboxclaims.lenny.dev`, and that each carries the `lenny.dev/schema-version` value the release expects, with a pointer to §10.5. Its messages are the code's: `CRD '<name>' is missing — apply updated CRDs before running helm upgrade` and the §10.5 mismatch message. §4.6.1 **API group:** stops counting the CRDs. |
| The specification names `sessionPolicy.maxConcurrentSessions` (§4.6.1 **CRD validation:**, the webhook and claim paragraphs, §4.6.3, and §5.2). The `SandboxTemplate` CRD carries no session concurrency and a top-level `maxConcurrent` commented "the per-pod slot count for service mode" **[source]**. | Under the designs treated as implemented, AR-2 removes `maxConcurrent` with service mode, PL-9 renames the pool field `sessionPolicy.maxConcurrentSessionsPerPod` in the pool schema and its CRDs, and SI-4 and PL-1 have the controller render one slot container per session the pod serves and record the count on the `Sandbox`. The controller can render slots only from a field it reads, so the template carries the pool field. | The `SandboxTemplate` carries `spec.sessionPolicy.maxConcurrentSessionsPerPod`, which the PoolScalingController copies from the pool record. The `Sandbox` carries `spec.slotCount`, which the WarmPoolController sets when it creates the pod and never changes. This design names the field, because SI-4 gives it no name. §4.6.1 **CRD validation:** checks `sessionPolicy.maxConcurrentSessionsPerPod >= 1`, and PL-9 owns the rename of the remaining mentions. The kind table states that the template carries the pool settings the controllers and the webhook read and that the pool record holds the rest, such as the recycle limits other than the scrub controls. |
| `SandboxTemplate.spec.resourceClass` is an enum of `small`, `medium`, and `large`, and the `Sandbox.spec.resourceClass` comment says "small, medium, large, or a deployer-defined class" **[source]**. | BR-1, treated as implemented, makes the class set operator-defined in a chart ConfigMap, checks a pool's class at write, and removes the template enum. The comment is right and the enum is wrong. No decision is open. | BR-1 removes the enum and owns F-5.2.70 to F-5.2.72. The kind table describes `resourceClass` as a class name defined in the deployment's class configuration, with a pointer to the section BR-1 lands. |

**Fields after the designs.** The kind table describes each kind by purpose and points to §4.6.3 for ownership, so it does not list every field. For the reader of this design, the designs treated as implemented change the kinds as follows **[source]**: AR-2 removes `executionMode` from the `Sandbox` and the template and `maxConcurrent` from the template; LU-4 removes `deliveryMode` from both; TSS-9 removes `workspaceTier` from the template; SI-3 removes `sessionPolicy.recycle.runtimeProcess`; PL-8 removes `sdkWarmDisabled`, `status.sdkWarmCircuitBreaker`, `scalePolicy.sdkConnectTimeoutSeconds`, the circuit-breaker settings, and the phase `sdk_connecting`; PL-9 adds `sessionPolicy.maxConcurrentSessionsPerPod` and `sessionPolicy.acknowledgeSharedPod`; DR-2 and DR-5 add `podRuntimeRef` and the owner to the template, and the warm-store fields to the template and the `Sandbox`; DR-10 adds `Sandbox.spec.reserved` and the warm pool's `reservedPods`; and PM-5 and PM-6 add the resolved placement and the placement conditions to the template. PLC-2's renames apply to the fields as these designs leave them, and none of these designs touches `status.podIP` or `templateRef`.

## PLC-5 Go type names

The implementations of the pod lifecycle interfaces take names that describe what they act on, following `CRDPodRegistry` in `pkg/podregistry/crd.go` **[design]**:

| Old | New |
| :-- | :-- |
| `AgentSandboxPoolReader` | `CRDPoolReader` |
| `AgentSandboxPodLifecycleManager` | `CRDPodLifecycleManager` |
| `AgentSandboxPoolManager` | `CRDPoolManager` |
| `pkg/podlifecycle/agentsandbox.go` | `pkg/podlifecycle/crd.go` |
| `agentsandbox_test.go`, `agentsandbox_errorpaths_test.go` | `crd_test.go`, `crd_errorpaths_test.go` |
| `TestAgentSandboxIsTheDefaultPoolManager_spec_17_1` and the `TestAgentSandbox*` test functions | `TestCRDPoolManagerIsTheDefault_spec_17_1` and `TestCRD*` |

The package comment in `interfaces.go` describes the indirection between Lenny's components and the `lenny.dev` resources, and drops the fallback plan. The comment in `errors.go` refers to the Kubernetes API client's error taxonomy. The compile-time assertions in `crd.go` keep their meaning, and their comment drops "`kubernetes-sigs/agent-sandbox` CRDs". `cmd/lenny-gateway/reconciler_workers.go` constructs `CRDPodLifecycleManager`.

§17.1's Warm Pool Controller row reads: "Manages pod lifecycle through the `PoolManager` interface (default implementation: `CRDPoolManager`, acting on the `lenny.dev` CRDs)". §4.6.1 names the same types ("PLC-1").

The rename changes no behaviour. It lands after the specification text that names the new types, so every `// spec:` citation still holds.

## PLC-6 Reconsideration trigger

§4.6.1 **Implementation:** ends with one statement **[design]**: adopting `kubernetes-sigs/agent-sandbox` as a dependency is worth reconsidering if upstream ships several sandboxes per pod together with a way to return a pod to its pool, or moves snapshot handling into its controller. Lenny does not follow upstream releases.

The two conditions are the ones under which the review's assessment would change. Section 5.5 of the review finds that the models diverge at the claim, because Lenny serves several sessions per pod and returns pods to their pool, and that the related upstream roadmap item, "API Support for Multi-Sandbox per Pod", is planned with no design **[external]**. Upstream's snapshot handling lives in its Python client, so it gives a Go controller nothing to use **[external]**. No review cadence, owner, or checklist goes with the statement (D3). The statement records the conditions for whoever next weighs the question.

## Limits

- Lenny maintains its controllers alone. Upstream fixes and performance work reach Lenny only when someone ports them.
- An operator who also runs upstream sees `sandboxes.agents.x-k8s.io` next to `sandboxes.lenny.dev`. The groups keep the CRDs apart, and readers who know upstream rely on the documentation of "PLC-3" to tell the kinds apart.
- The renames give no runtime compatibility with upstream tools. The claim and template schemas stay different, so an upstream client cannot read or write Lenny's objects.
- Nobody follows upstream releases, so a change upstream that meets the trigger is found only when someone looks.

## Alternatives rejected

- **Adopt upstream `v1beta1` (option (b)).** Upstream's `Sandbox` controller would own the pod create and observe loop. Lenny would keep its own claim, recycle, hold, phase projection, scaling, and tenant pinning, because upstream's claim picks the sandbox and never returns it. It would move Lenny from Go 1.25, `k8s.io/api` v0.31, and `controller-runtime` v0.19 to Go 1.26, v0.37, and v0.25, share one cluster-wide CRD version and controller with any other upstream user, and follow weekly releases whose patch releases carry action-required changes **[external]**. The owner chose (a) (D1), and PLC-6 records when to reconsider.
- **Use upstream's `Sandbox` alone.** It carries most of the costs of (b) for the pod create and observe loop alone, and upstream's `Sandbox.status` has no phase to carry Lenny's projection.
- **Rename the kinds,** for example to `AgentPod`, `AgentPool`, and `PodClaim`. It removes the shared names and touches the specification, the code, the chart, the RBAC, the webhooks, and the documentation. The owner kept the names (D2).
- **Keep a quarterly tracking checklist in place of the go/no-go gate.** The owner reduced tracking to one statement (D3).
- **Rename `warmCount` and `readyCount` to `replicas` and `readyReplicas`.** The counts exclude pinned idle pods and pair with no scale target, so the names would mislead ("PLC-2").
- **Keep `status.podIP` beside `status.podIPs`.** The no-backward-compatibility rule excludes a second carrier of the same fact.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Regenerate the CRD manifests in `charts/lenny/crds/` and `pkg/embedded/crds/` and confirm that each carries `lenny.dev/schema-version: "2"`. | An earlier generator run dropped the manually added annotation, and PLCR-6 depends on it. | Open, for PLC-B. |
| Confirm that `lenny-pool-config-validator` sees the raw `templateRef` member of a `SandboxWarmPool` write under `PreserveUnknownFields`, so PLCR-5's refusal works **[inference]**. | The webhook decodes the object, and a typed decode drops unknown members. | Open, for PLC-B. |
| Confirm that no chart template, script, or deployer document reads `.status.podIP` of a `Sandbox` or `.spec.templateRef` of a `SandboxWarmPool` through `kubectl` or `jsonpath`. | PLCR-3 and PLCR-5. A search of `charts/`, `scripts/`, and `docs/` found none **[source]**. | Open, for PLC-B. |
| Confirm on a dual-stack Kind cluster that the gateway dials the first `podIPs` entry and reaches the adapter. | PLCR-4 assumes the adapter listens on the pod's primary address family. | Open, for PLC-B's tier 5 test. |

## Specification changes

The following edits locate their targets by section heading and bold paragraph label:

| Section | Change |
| :-- | :-- |
| [§3 High-Level Architecture](../spec/03_high-level-architecture.md#3-high-level-architecture) | The diagram label "Warm Pool Controller (pod lifecycle, agent-sandbox CRDs)" becomes "Warm Pool Controller (pod lifecycle, Lenny CRDs)" ("PLC-1"). |
| [§4.6.1 Warm Pool Controller (Pod Lifecycle)](../spec/04_system-components.md#461-warm-pool-controller-pod-lifecycle), **Implementation:** | Replace the paragraph with the statement of Lenny's own controllers and the reconsideration trigger ("PLC-1", "PLC-6"). |
| §4.6.1, **Internal abstraction layer:** | "never directly with the CRD types"; the default implementations `CRDPodLifecycleManager` and `CRDPoolManager`; the closing sentence without upstream; and the snapshot extension point as O2 decides ("PLC-1", "PLC-5"). |
| §4.6.1, **CRD mapping from agent-sandbox:** and **API group:** | The label becomes **Resource kinds:**. **API group:** names every Lenny kind without a count and drops the fallback sentence. The table drops "Replaces (old Lenny CRD)", adds the `Runtime` row and the "Difference from upstream" column, and corrects the `SandboxTemplate` and `SandboxWarmPool` rows ("PLC-1", "PLC-3", "PLC-4"). The table names `Sandbox.status.podIPs`, `Sandbox.spec.slotCount`, `SandboxTemplate.spec.sessionPolicy.maxConcurrentSessionsPerPod`, and `SandboxWarmPool.spec.sandboxTemplateRef.name` ("PLC-2", "PLC-4"). If O1 takes (b), the `Sandbox` row names the `PodScheduled` condition. |
| §4.6.1, the ADR-007 paragraph | The label becomes **ADR-007: `SandboxClaim` optimistic locking and failover fencing.** and the paragraph verifies Lenny's own claim, keeping its two tests ("PLC-4"). |
| §4.6.1, **CRD validation:** | `sessionPolicy.maxConcurrentSessionsPerPod >= 1` ("PLC-4", with PL-9). The resource class rule follows BR-1. |
| §4.6.1, **Dependency pinning and upgrade policy:**, **Go/no-go criteria for the agent-sandbox dependency:**, and **Fallback plan if agent-sandbox is abandoned or diverges:** | Delete. The reconsideration trigger in **Implementation:** is the successor of the tracking content (D3, "PLC-6"). |
| [§4.6.3 CRD Field Ownership and Write Boundaries](../spec/04_system-components.md#463-crd-field-ownership-and-write-boundaries) | The PoolScalingController's `SandboxWarmPool` row gains `spec.sandboxTemplateRef` ("PLC-4"). The WarmPoolController's `Sandbox` `spec.*` row covers `spec.slotCount`, and its `status.*` row covers `status.podIPs`, with no edit needed. |
| [§17.1 Kubernetes Resources](../spec/17_deployment-topology.md#171-kubernetes-resources) | The Warm Pool Controller row names `CRDPoolManager` ("PLC-5"). |
| [§17.6 Packaging and Installation](../spec/17_deployment-topology.md#176-packaging-and-installation), "Checks performed" | The "Agent-sandbox CRDs" row becomes "Lenny CRDs", lists every Lenny CRD, `runtimes.lenny.dev` included, checks the schema-version annotation with a pointer to §10.5, and uses the code's messages ("PLC-4"). |
| [§18.2 Phase Map](../spec/18_build-sequence.md#182-phase-map) | Row 2: "**Phase 1**" ("PLC-1"). |
| [§18.6 Phase 2](../spec/18_build-sequence.md#186-phase-2--adapter-protocol-make-run-imageresolver-startup-benchmark) | Remove the upstream-contribution deliverable and the dependency sentence of **Prerequisites.** ("PLC-1"). |
| [§18.9 Phase 3](../spec/18_build-sequence.md#189-phase-3--pool-scaling-delegation-policy-runtime-upgrade-mtls) | The `RuntimeProvider` item drops "default implementation wraps `kubernetes-sigs/agent-sandbox`" ("PLC-1"). |
| [§18.40 Hard Prerequisite Chain](../spec/18_build-sequence.md#1840-hard-prerequisite-chain) | Remove item 2 and renumber the later items ("PLC-1"). |
| [§19 Resolved Decisions](../spec/19_resolved-decisions.md#19-resolved-decisions) | Row 4 states Lenny's own controllers. Row 14 replaces the upstream criterion ("PLC-1"). |
| [§23 Competitive Landscape](../spec/23_competitive-landscape.md#23-competitive-landscape) and [§23.2 Community Adoption Strategy](../spec/23_competitive-landscape.md#232-community-adoption-strategy) | The upstream row as "PLC-1" states, and the **Open-source license.** criterion as in §19 row 14. |

No heading changes, so no anchor retires and the anchor-move map gains no entry. The specification's citation of ADR-007's test names, §28's "pod IP" endpoint wording, and §12.6's `PodRecord.PodIP` are unaffected. ADR-007's tests and §28's endpoint name the address, and `PodRecord.PodIP` is the pod registry's own field.

## Non-specification changes

### Code and chart

| Area | Change |
| :-- | :-- |
| `pkg/apis/lenny/v1alpha1` | `SandboxStatus.PodIPs []string` with `PrimaryPodIP()`, `SandboxSpec.SlotCount int32`, `SandboxWarmPoolSpec.SandboxTemplateRef` with the struct `SandboxTemplateRef{Name string}` and the printer column `.spec.sandboxTemplateRef.name`, `sessionPolicy.maxConcurrentSessionsPerPod` on the template with PL-9, and the schema-version marker "2" on every kind. If O1 takes (b), a `SandboxConditionPodScheduled` constant. The deep-copy file is regenerated. |
| `charts/lenny/crds/` and `pkg/embedded/crds/` | Regenerated, with the annotation checked ("Validation owed"). |
| `pkg/preflight/crdschema.go` | `CurrentCRDSchemaVersion = "2"`. |
| `pkg/controller/sandbox` | Copies the pod's `status.podIPs` into the `Sandbox`, sets `spec.slotCount` at creation, and, under O1 (b), mirrors the pod's `PodScheduled` condition. |
| `pkg/controller/warmpool` | `gc.go`, `pod_reconciler.go`, and `occupancy.go` carry `status.podIPs` through their status patches, and the planner reads `spec.sandboxTemplateRef.name`. |
| `pkg/controller/poolscaling` | Writes `spec.sandboxTemplateRef.name` and copies `maxConcurrentSessionsPerPod` from the pool record into the template. |
| `pkg/gateway/podlifecycle/podsession`, `pkg/podregistry/crd.go`, `cmd/lenny-gateway/coordination_seams.go`, and `tests/testinfra/lifecycleharness` | Read `PrimaryPodIP()`. `poddial.go` refuses to dial when the list is empty. |
| `pkg/admission/pool_config_validator` and `pkg/admission/webhook` | Read `spec.sandboxTemplateRef.name`, and refuse a `SandboxWarmPool` write that carries `templateRef` (PLCR-5). |
| `pkg/embedded/stack`, `pkg/ops/backup/runner`, `pkg/gateway/sessionserver`, and `pkg/podlifecycle` | Read or write `sandboxTemplateRef.name`. |
| `pkg/podlifecycle` and `cmd/lenny-gateway/reconciler_workers.go` | The renames of "PLC-5". |
| `tests/` | The tier 2, 3, 4, 7a, 8, and 9 tests that build `SandboxWarmPool` or `Sandbox` objects, and `tests/testinfra/k8s/agent-workload-load.yaml.tmpl`, `runtimeclass-pool-gvisor.yaml.tmpl`, and `runtimeclass-pool-kata.yaml.tmpl`, use the new fields. `tests/spec-map.json` follows the renamed tests. |
| `go.mod` | No change. It already lists no upstream module. |

### Records

The proposal edits no record. The findings of "Records" under "Current state" stay as written: each one that cites the dependency or its fallback is closed, so this design retires their premises without reopening or editing them. F-4.6.17's fallback estimate and F-17.1.11's named default lose their subject once PLC-A lands. F-5.2.70 to F-5.2.72 remain open under BR-1.

## Documentation

Every change below is owed by the proposal that lands the matching part **[design]**:

| File | Change |
| :-- | :-- |
| `docs/index.md` and `README.md` | The Warm Pool Controller row: "Creates and retires agent pods through Lenny's `Sandbox` resources, and keeps each pool at its warm count." |
| `docs/getting-started/architecture.md` | The diagram node becomes "Warm Pool Controller<br/>(Lenny CRDs)". The section opens with the statement of Lenny's own controllers and the kind names taken from upstream. The CRD table gains a `Runtime` row and a "Difference from upstream" column with the content of "PLC-3", and its `SandboxTemplate` and `SandboxWarmPool` rows are corrected as "PLC-4" states. |
| `docs/assets/diagrams/architecture-overview.svg` | The text becomes "Pod lifecycle, Lenny CRDs, claim and drain". |
| `docs/reference/glossary.md` | "Sandbox CRD": Lenny's resource for one agent pod in the `lenny.dev` group, with the kind name taken from `kubernetes-sigs/agent-sandbox`, and the differences of "PLC-3". "SandboxTemplate": a pool definition with no pod spec, written by the PoolScalingController from the pool record. New entries "SandboxWarmPool" and "SandboxClaim" with their differences from upstream. "Warm Pool Controller": "manages agent pod lifecycle through Lenny's `Sandbox`, `SandboxWarmPool`, and `SandboxClaim` resources". "CRD": lists `Runtime`, `Sandbox`, `SandboxTemplate`, `SandboxWarmPool`, and `SandboxClaim`, and drops `RuntimeUpgrade`. |
| `docs/operator-guide/installation.md` | The preflight row: "Lenny CRDs: confirms every Lenny CRD, `Runtime` included, is installed with the schema version the release expects". |
| `docs/tutorials/deploy-to-cluster.md` | The sample output: "[PASS] Lenny CRDs: all CRDs at the expected schema version". |
| `docs/operator-guide/upgrades.md` and `docs/runbooks/crd-upgrade.md` | State that the CRD schema version is "2", that a cluster whose CRDs carry "1" fails preflight and controller startup until the new CRDs are applied, and that a development cluster from an earlier build is reinstalled. |
| `docs/adr/index.md` | ADR-0004 becomes "Lenny's own pod lifecycle controllers on controller-runtime", still Planned, citing §19 row 4. |
| `docs/adr/template.md` | The example driver becomes "Stay compatible with the `lenny.dev/v1alpha1` CRD contract". |
| `docs/adr/0007-sandboxclaim-optimistic-locking.md` | "Related ADRs" names ADR-0004 by its new title. An amendment dated when PLC-A lands states that the claim the ADR verifies is Lenny's own `SandboxClaim`, as its existing per-pod amendment already describes. |
| `docs/adr/0008-open-source-license.md` | An amendment dated when PLC-A lands states that Lenny no longer depends on `kubernetes-sigs/agent-sandbox`, that MIT stays compatible with the Apache-2.0 dependencies Lenny uses, such as `controller-runtime`, and that the decision is unchanged. The original context and drivers stay as the record of the decision. |
| `docs/about/governance.md` | "**Ecosystem alignment.** Compatible with Apache-2.0 upstream dependencies such as `controller-runtime`." |
| `docs/operator-guide/troubleshooting.md` | `lenny-pool-config-validator`'s refusal of a `SandboxWarmPool` that carries `templateRef`, with its message and the fix: name the template in `spec.sandboxTemplateRef.name`. The schema-version mismatch at preflight and at controller startup, with a pointer to `docs/runbooks/crd-upgrade.md`. |
| `TESTING.md` | The tier 5 row's backing services: "Kind cluster, Helm install, Lenny CRDs". The tests of "Tests" join their tier sections. |
| `CHANGELOG.md` | States the renames `Sandbox.status.podIP` to `status.podIPs` and `SandboxWarmPool.spec.templateRef` to `spec.sandboxTemplateRef.name`, with no alias; the CRD schema version "2"; the `CRD*` type names; and that the specification no longer describes a dependency on `kubernetes-sigs/agent-sandbox`. Under O1 (b), the `PodScheduled` condition on `Sandbox`. |

**Settings, defaults, limits, and failure modes.** This design adds no setting, default, or limit. Its failure modes are the preflight and controller-startup refusal of a CRD at schema version "1", whose messages the upgrade documentation quotes, and `lenny-pool-config-validator`'s refusal of a `SandboxWarmPool` that carries `templateRef`, which `docs/operator-guide/troubleshooting.md` lists with its message. An operator reading a `Sandbox` reads its addresses from `status.podIPs`.

**Runtime authors.** Nothing changes for a runtime. The adapter protocol, the pod layout, and every runtime-visible path are unaffected, so no runtime author document changes.

## Tests

| Tier | Coverage |
| :-- | :-- |
| 0 | A repository check finds no `agent-sandbox` or `kubernetes-sigs/agent-sandbox` outside `designs/`, `proposals/`, the records, `CHANGELOG.md`, the ADR amendments, the §4.6.1 statements PLCR-1 allows, §23's row, and the glossary entries (PLCR-1). `go.mod` lists no `sigs.k8s.io/agent-sandbox`, and no file under `charts/` names `agents.x-k8s.io` (PLCR-2). No Go identifier or file name under `pkg/podlifecycle` or `cmd/lenny-gateway` contains `AgentSandbox` (PLCR-10). No Go source writes `Status.PodIP` of a `Sandbox` or `Spec.TemplateRef` of a `SandboxWarmPool`. Every CRD manifest in `charts/lenny/crds/` and `pkg/embedded/crds/` carries `lenny.dev/schema-version: "2"`, equal to `CurrentCRDSchemaVersion` (PLCR-6). |
| 1 | `PrimaryPodIP()` returns the first entry and an empty string for an empty list. The Sandbox-to-Pod reconciler copies a single-stack and a dual-stack pod's `status.podIPs` in order and clears the list when the pod is absent (PLCR-3). The pod builder records `spec.slotCount` equal to the slot containers it renders for slot counts 1 and 3 (PLCR-9). The compile-time assertions bind `CRDPoolReader`, `CRDPodLifecycleManager`, and `CRDPoolManager` to the interfaces (PLCR-10). Under O1 (b), the mirrored `PodScheduled` condition copies the pod's status, reason, and message and is absent with no pod. |
| 2 | On envtest, the API server accepts a `SandboxWarmPool` with `spec.sandboxTemplateRef.name` and refuses one without it. `lenny-pool-config-validator` refuses a `SandboxWarmPool` that carries `templateRef` with a message naming `spec.sandboxTemplateRef.name` (PLCR-5). `lenny-preflight` and the controller startup check report a CRD at "1" as stale with the §10.5 message, a missing `runtimes.lenny.dev` as missing, and pass when every CRD is at "2" (PLCR-6, PLCR-8). |
| 4 | On the compose stack, a session starts on a warm pod: the gateway reads the pod's address from `status.podIPs` and binds the session (PLCR-4). A `Sandbox` whose `status.podIPs` is empty is not dialled. |
| 5 | On Kind, a Helm install passes the "Lenny CRDs" preflight check, `kubectl get sandboxwarmpools` shows the template column, and a session runs end to end. On a dual-stack Kind cluster, the `Sandbox` lists both addresses and the session binds through the first ("Validation owed"). |
| 11 | The documentation checks find the new glossary entries and the renamed ADR-0004 title, and the ADR catalog check passes with the amendments. |

Every test carries the `// spec:` annotation for the section it verifies, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Instructions for reviewers of other designs

- [`session-isolation-design.md`](session-isolation-design.md): SI-4's recorded slot count is `Sandbox.spec.slotCount`, set by the WarmPoolController at creation and never changed ("PLC-4"). The reservation script reads it.
- [`agent-pod-layout-design.md`](agent-pod-layout-design.md): PL-9's rename lands on `SandboxTemplate.spec.sessionPolicy.maxConcurrentSessionsPerPod`, a field the template does not carry today, and the PoolScalingController copies it from the pool record ("PLC-4"). PL-1's pod builder reads it.
- [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md): AR-2's removal of `maxConcurrent` and `executionMode` from the `SandboxTemplate` and the `Sandbox` is the premise of PLC-4's `maxConcurrent` ruling.
- [`billing-resource-attribution-design.md`](billing-resource-attribution-design.md): BR-1 settles the review's resource class finding. §4.6.1's kind table points to the section BR-1 lands.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md): an owned pool's `SandboxWarmPool` names its template in `spec.sandboxTemplateRef.name` (DR-5's rendered pair).
- [`placement-design.md`](placement-design.md): applied in its revision 11. If O1 takes (b), PM-6's **What happens to a pod or pool that fails** can name the `Sandbox`'s `PodScheduled` condition beside the template's `PlacementUnschedulable`.

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| PLC-A | The specification changes of PLC-1, PLC-3, PLC-4, PLC-5's §17.1 and §4.6.1 names, and PLC-6, with the documentation. | The designs' own §4.6.1 and §5.2 edits for the fields PLC-4 names: AR-2's removals, PL-9's rename, SI-4's slot count, and BR-1's class configuration. PLC-A lands with or after them, so §4.6.1's new text describes the final fields. |
| PLC-B | The field renames and the schema-version bump of PLC-2, `spec.slotCount` and the template's `maxConcurrentSessionsPerPod` of PLC-4, and O1's condition if taken, with their tests. | PLC-A for the specification text that names the fields. PL-9's code for `maxConcurrentSessionsPerPod`, and SI-4's code for the slot count's reader. |
| PLC-C | The Go type renames of PLC-5. | PLC-A for the §17.1 and §4.6.1 names. It is independent of PLC-B. |

PLC-B and PLC-C can land in either order once PLC-A is in.

## Sources

The upstream facts come from the review in [`evidence/pod-lifecycle-controller-design/agent-sandbox-review.md`](evidence/pod-lifecycle-controller-design/agent-sandbox-review.md), which gives a URL for each. This design read these pages itself on 2026-11-15:

- `kubernetes-sigs/agent-sandbox`, `api/v1beta1/sandbox_types.go` at `v1.0.11`, https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/v1.0.11/api/v1beta1/sandbox_types.go. It defines `status.podIPs` as `[]string` and the condition types `Ready`, `Suspended`, `PodScheduled`, and `Finished`, with `PodScheduled` mirrored from the pod.
- `kubernetes-sigs/agent-sandbox`, `extensions/api/v1beta1/sandboxwarmpool_types.go` at `v1.0.11`, https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/v1.0.11/extensions/api/v1beta1/sandboxwarmpool_types.go. It defines `spec.sandboxTemplateRef` as the struct `SandboxTemplateRef` with `name`, and the printer column `.spec.sandboxTemplateRef.name`.
- The Kubernetes Pod API reference, https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/. It states that `podIPs` "holds the IP addresses allocated to the pod. If this field is specified, the 0th entry must match the podIP field", and that each entry is a `PodIP` object with an `ip` member.

## Review history

### Revision notes

- Revision 1, 2026-11-15. Written from the review of upstream `kubernetes-sigs/agent-sandbox` of 2026-11-15, filed under `evidence/pod-lifecycle-controller-design/`, and the owner's decisions of the same day: "1 and 2: ok" to option (a) with field-name alignment and to keeping the kind names (D1, D2), and the answer recorded as D3 to "This means the project should stop tracking and using the agent sandbox upstream, right?". The review's list of renames was checked against `pkg/apis/lenny/v1alpha1`, `charts/lenny/crds/`, and upstream's `v1.0.11` types: `podIP` and `templateRef` are renamed, and no Lenny condition shares a meaning with an upstream one, so the condition names become O1. The review's resource class finding is settled by BR-1, and its `maxConcurrent` finding by AR-2, PL-9, and SI-4 together, which leaves the `Sandbox` slot count without a field name, so PLC-4 names it `spec.slotCount`. The review's snapshot extension point finding becomes O2. No independent review has read revision 1.
