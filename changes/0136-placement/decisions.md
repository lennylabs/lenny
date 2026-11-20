# Decisions: 0136 Placement

## Open decisions

The M3 readiness review of 2026-11-19 raised O9 to O18. O1 to O8 are settled and sit under "Decisions", in "Settled open decisions O1 to O8".

**O9. Where the removal of the T4 placement mechanism lands.**

0136.S7 removes the `lenny-t4-node-isolation` webhook with its alert, its preflight check, and its `features.compliance` gate, and the pod builder's `applyT4NodeIsolation` (D10, "Removals" in `change.md`). [`0150-tenant-security-settings`](../0150-tenant-security-settings/steps.md#steps) stated in 0150.S2 that the T4 webhook keeps reading the Runtime's field until 0150.S3, and in 0150.S3 that it lands together with this change's removal of the T4 webhook, while 0150.S3 depends on 0136.S4 and 0136.S6, which land after the removal. 0150's revision of the M3 readiness review dropped both statements, and 0150.S2 and 0150.S3 now leave the step to 0150's O17, which records this question. 0150's `change.md` (its scope, inventory row I22, and its non-specification changes) leaves the removal to this change.

- (a) Keep the removal in 0136.S7. §6.4's **T4 dedicated-node requirement** becomes its successor pointer in 0136.S7's specification lane, so the specification and the code change in one step. Between 0136.S7 and 0150.S3, a T4 pool runs on its profile's class with shared tenancy, on unreleased `main` only.
- (b) Move the removal into a step of this change that shares a branch with 0150.S3. The T4 label, taint, webhook, and `applyT4NodeIsolation` then stay beside the class rendering from 0136.S3 to 0136.S6, and T4 pods carry both the class and the T4 selectors and tolerations.

**Recommendation: (a).** It matches 0150's `change.md`, D10, and the program's rule against transition modes, and the interval exists only on unreleased `main`. (b) needs both mechanisms to coexist over four steps, and no step states who makes them coexist. It blocks 0136.S7 and 0150.S3. The other change is [`0150-tenant-security-settings`](../0150-tenant-security-settings/decisions.md), whose O17 is the matching decision. (raised by the M3 readiness review, 2026-11-19; sources: 0136-F1)

**O10. One residency region per deployment against per-region storage routing.**

D24 and PM-7 **Residency across regions (D24).** state that a deployment serves one residency region and that every component of it runs there. [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) **Data residency** item 2 keeps the `StorageRouter`, which routes each residency tenant's Postgres, MinIO, and Redis writes to per-region backends from the `storage.regions` map, with per-region KMS, backups, and legal-hold escrow. This change edits item 1 only, so the target state holds two residency models. [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) places the tenant key at `storage.regions.<region>.kmsEndpoint`, and [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) reuses `storage.regions.<region>.legalHoldEscrow`.

- (a) Keep per-region storage routing, and narrow PM-7 to state that the data stored through the Kubernetes API (pod specs, Secrets, and custom resources) stays in the control plane's region. A residency claim that covers that data needs the control plane in the tenant's region, and `storage.regions` stays for deployments that accept that limit.
- (b) Retire per-region storage routing in favour of one deployment per region, through a change that edits §12.8, the backup and escrow paths, 0150, and 0128.

**Recommendation: (a)** for v1, because (b) is a removal that no member change scopes. It blocks 0136.S6 and this change's edit of §12.8. The other changes are [`0150-tenant-security-settings`](../0150-tenant-security-settings/decisions.md) and [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/decisions.md). (raised by the M3 readiness review, 2026-11-19; sources: 0136-F4)

**O11. Which step lands the reserved overlay's field.**

0136.S5 adds `placement.reservedOverlay` and depends on [0147.S9](../0147-tenant-derived-runtimes/steps.md#steps). When this decision was raised, 0147.S9 built reservations "with the use of the placement design's reserved overlay" and had no edge to any step of this change, and `reservedPlacement` does not exist in the tree, so 0147.S9 landed before the field it used. 0147's revision of the M3 readiness review changed 0147.S9: it depends on 0136.S2, renders no node placement for reserved pods, and leaves the overlay, its application to the pods it marks, and the overlay's tests to 0136.S5 (0147-p1-F10, 0147-p2-F5). That arrangement is option (c) below.

- (a) The overlay field and its validation against the pool's class land in a step of this change after 0136.S3, which 0147.S9 depends on. 0136.S5 keeps the tenant affinity and toleration of a dedicated pool's reserved pods, and its edge to 0147.S9 only if that part needs it.
- (b) 0147.S9 lands `placement.reservedOverlay` with this change's validation rules of "PM-3", and 0136.S5 keeps its edge to 0147.S9 for the dedicated pool's reserved pods.
- (c) 0136.S5 lands the overlay field, its validation, and its application to the reserved pods that 0147.S9 marks, with the overlay's tests, and keeps its edge to 0147.S9. Between 0147.S9 and 0136.S5, reserved pods take the pool's class placement alone, on unreleased `main` only.

**Recommendation: (c).** Both changes' steps now state it, no step uses the field before it exists, and the overlay's validation stays in the change that defines the classes it reads. (a) would need 0147.S9 to change again. Either way 0147.S9 depends on 0136.S2 for `placement.priorityClasses.reserved`, which its steps now state. It blocks 0136.S5 and 0147.S9. The other change is [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/decisions.md). (raised by the M3 readiness review, 2026-11-19; sources: 0136-F5)

**O12. Placement checks when a session moves to a new pod.**

PM-4 checks the offering and the effective requirements at the post-chain point at creation, and `sessionIsolationLevel` is "populated from the pool and pod set at creation and unchanged for the session's life". The change does not say what happens when a session resumes on a new pod after pod loss, eviction, suspension, or a node drain. Between creation and the resume, an operator can change the offering's `nodePlacement`, a requirement can be added, which "applies at once", or the tenant's settings can move to `dedicated`.

- (a) Every claim of a new pod for an existing session reruns the offering and requirement checks with the current values, refuses with the same codes, and updates `sessionIsolationLevel` to the new pod's placement.
- (b) The decision made at creation holds for the session's life. An added requirement applies to new sessions only, and the documentation states it.

**Recommendation: (a).** A requirement is a security constraint, and under (b) a resumed session can run on placement that its current requirements refuse. It blocks 0136.S6. (raised by the M3 readiness review, 2026-11-19; sources: 0136-F11)

**O13. What a pending version of an environment's `placementRequirements` holds.**

Under D18 a relaxation of an environment's `placementRequirements` is held as a pending version that the tenant security record's applier activates at its `effectiveAt`. Neither this change nor [`0142-security-oversight`](../0142-security-oversight/change.md) nor [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) states what a pending version holds, whether a write is refused while one is pending, or what a later tightening does to it. If a pending version is the full stored state, its activation undoes a requirement added during the delay, with no justification, event, or delay.

- (a) A pending version holds only the relaxed requirements and is applied onto the active version at `effectiveAt`. A later write that tightens a pending requirement cancels that requirement's relaxation. An environment has at most one pending version, and `If-Match` compares the active version.
- (b) Every write to an environment's `placementRequirements` is refused while a relaxation is pending, except a cancel.

**Recommendation: (a)**, stated once in 0142's SO-2 and referenced from this change and from 0150. Once the owner settles it, PM-4 cites the rule as SO-2 states it rather than restating it. It blocks 0136.S6. The other changes are [`0142-security-oversight`](../0142-security-oversight/decisions.md) (its decision 40) and [`0150-tenant-security-settings`](../0150-tenant-security-settings/decisions.md) (its O14), which record the same question for their records. (raised by the M3 readiness review, 2026-11-19; sources: tenancy-security-C4)

**O14. How stored offerings, pools, and tenant settings follow a change to the Helm classes.**

Offerings name classes (`classes: [sandboxed, microvm]`) and live in Postgres, and classes come from Helm values. A switch from `strict` to `compact` replaces `standard`, `sandboxed`, and `microvm` with `agents`, and every stored offering, the seeded `shared` offering included, then names classes that no longer exist, so every pool fails `offering_class_mismatch`. A class tenancy moved to `dedicated` makes every multi-tenant pool of the class invalid under `dedicated_requires_single_tenant`, and the reverse move leaves a dedicated tenant with access to pools of shared tenancy, against PMR-6. A change to a pool's `placement.tenantId` after its pods exist is undefined. Admission validates API writes only, and preflight checks nodes rather than stored records.

- (a) Offerings name members (`members: [sandboxed, microvm]`), which no preset renames, and a pool matches through its isolation profile's member. Preflight fails an upgrade whose class tenancy contradicts a stored pool or a tenant's `placement.nodeTenancy`. `placement.tenantId` is immutable after the pool is created.
- (b) Offerings keep class names. Preflight fails an upgrade that renames or removes a class that a stored offering names, or whose class tenancy contradicts a stored pool or a tenant's setting. A change to `placement.tenantId` replaces the pool's pods.

**Recommendation: (a).** Members are fixed by the platform, so a stored offering survives every preset switch, and an immutable tenant avoids defining a replacement of pods. It blocks 0136.S3, 0136.S4, and 0136.S5. (raised by the M3 readiness review, 2026-11-19; sources: 0136-F9)

**O15. How the widened `lenny-pod-security` avoids locking out `lenny-system`.**

PM-6 widens the fail-closed webhook to `lenny-system`, where its own Deployment runs. When every replica is down, no pod in that namespace can be created, the webhook's own replacement included, and recovery needs a manual deletion of the webhook configuration. Every webhook in the tree is scoped to agent namespaces today (`charts/lenny/templates/admission-policies/pod-security-webhook.yaml` selects `lenny.dev/agent-namespace`). The OpenTelemetry collector in `observability.otlpNamespace` takes the system selector ("PM-5") and sits outside the widened scope, and that namespace may hold the deployer's own workloads.

- (a) A second, fail-closed webhook entry covers the system namespaces, with an `objectSelector` that leaves out the pods of the `lenny-pod-security` Deployment. The continuous check reports a pod that carries that Deployment's labels and is not one of its pods. `observability.otlpNamespace` stays outside the webhook, and the continuous check's system-pod rule covers the collector.
- (b) The entry for the system namespaces uses `failurePolicy: Ignore`, the entry for the agent namespaces stays fail-closed, and the continuous check reports what the webhook did not see.
- (c) One fail-closed scope as written, with the manual recovery in a runbook.

**Recommendation: (a).** It keeps every other system pod checked at admission, and the label exemption it adds is visible to the continuous check. (b) leaves system pods unchecked during any outage, and (c) needs a manual step to recover the platform. Each option adds a `chaos` case in which every webhook replica is lost and the release recovers. It blocks 0136.S7. (raised by the M3 readiness review, 2026-11-19; sources: 0136-F2, 0136-L12)

**O16. The size bound on the annotation `lenny.dev/quota-agent-status`.**

D23 records the owner's answer "3: ok" for the annotation. Its bound of 1 KiB, which `lenny-quota-agent-node-label` enforces ("PM-6"), was the orchestrator's choice for the owner's review, as revision 9 in `review-history.md` and the D29 entry of [`0118-agent-pod-layout`'s review history](../0118-agent-pod-layout/review-history.md) record. Neither change records an answer.

- (a) 1 KiB.
- (b) Another bound.

**Recommendation: (a)**, because the annotation holds one small JSON object with `detection`, `landlock`, `volumesBound`, `assignmentErrors`, and `writtenAt`. It blocks 0118.S6, which renders the policy. The other change is [`0118-agent-pod-layout`](../0118-agent-pod-layout/decisions.md), which owns the annotation as its D29. (raised by the M3 readiness review, 2026-11-19; sources: the review's status assessment of 0136)

**O17. Whether Lenny's system priority classes preempt other pods.**

`lenny-system-critical` and `lenny-system` use `PreemptLowerPriority` ("PM-8"). The system class carries no taint by default, so an operator add-on that shares system nodes, such as an ingress controller, can be preempted by a Lenny pod. Under `single-node`, a system pod can preempt an agent pod, and a preemption bypasses the eviction API and the drain-readiness checks that DR-10 relies on.

- (a) `lenny-system` takes `preemptionPolicy: Never`, `lenny-system-critical` keeps `PreemptLowerPriority`, and under `single-node` both take `Never`.
- (b) Both keep `PreemptLowerPriority`, and the operator documentation states that Lenny's system pods can preempt add-ons on system nodes and agent pods under `single-node`.

**Recommendation: (a).** The components that keep the platform reachable still preempt, and the rest wait for capacity as other workloads do. It blocks 0136.S2. (raised by the M3 readiness review, 2026-11-19; sources: 0136-L10)

**O18. Which step of 0142 this change's edges name if 0142.S2 splits.**

0136.S5 and 0136.S6 depend on [0142.S2](../0142-security-oversight/steps.md#steps). 0136.S5 reads it for the posture's `placementOffering` and `placementConfigured` fields, and 0136.S6 reads it for the tenant security record, its write path, `weakeningDelaySeconds`, and the applier, and adds the record's `placement` section and the posture's placement fields. Decision 41 of [`0142-security-oversight`](../0142-security-oversight/decisions.md) asks whether 0142.S2 splits into the record and applier step, a guardrail step, and a posture step.

- (a) If 0142.S2 splits, 0136.S5's edge moves to the posture step, and 0136.S6 keeps its edge on 0142.S2, which then carries the record and the applier, and gains an edge on the posture step.
- (b) 0142.S2 stays one step, and both edges stay as written.

**Recommendation: (a)**, which follows 0142's recommendation on its decision 41, so each edge names the step that carries what this change reads. The edge of 0136.S5 on 0142.S4 is unaffected. It blocks the edges of 0136.S5 and 0136.S6 on 0142.S2. The other change is [`0142-security-oversight`](../0142-security-oversight/decisions.md) (its decision 41). (raised by the M3 readiness review, 2026-11-19; requested by change 0142)

## Decisions

The owner's decisions of 2026-11-12, worded as the decision brief records them.

| # | Decision | Applied in |
| :-- | :-- | :-- |
| D1 | **Dedicated design.** Placement gets its own design, `changes/0136-placement/change.md`; all other designs and the spec point to it. The owner asked to "make placement rules configurable". | This document, "Instructions for reviewers of other designs" |
| D2 | **No system pods on agent nodes.** System pods must not share nodes with runtimes (agent pods), except in `lenny up` (single-node). | PM-1, PM-5, PM-6 |
| D3 | **Per-pool node placement.** Pools get a per-pool `nodePlacement` (node selector plus tolerations), set by the platform admin only. | PM-3 |
| D4 | **runc.** runc pools require user namespaces (`hostUsers: false`). runc is not dev/test-only; the profile choice is the operator's. | PM-1, PM-5, PM-6 |
| D5 | **Constraints are overridable.** The coordinator proposed operator-defined node classes as groups of members plus presets; the owner asked for "better alternatives" to per-pair relaxation and was given this model. It is recorded as the recommended form, for owner confirmation (O1). Classes are groups of members, with one member per class by default. Mixing needs `acknowledgeMixedClass: true` and produces a preflight warning, a standing alert in production mode, an audit record, and `sessionIsolationLevel` reporting the class co-members. The presets are `strict` (default), `compact`, and `single-node` (lenny up only). runc requires user namespaces, with `preferred` as the acknowledged relaxation. Per-pair `allowSharing` lists, per-pool relaxation, and presets alone were rejected. D13 later replaced the `preferred` relaxation with an explicit `disabled` setting. | PM-1, "Alternatives rejected" |
| D6 | **Cross-tenant node sharing: approved as proposed.** Node tenancy is `shared` (default) or `dedicated`, with a per-class default and a per-tenant `dedicated` set by the operator. Dedicated nodes carry the label `lenny.dev/tenant=<id>` and a matching taint; the tenant's pods select and tolerate them, and nobody else's pods can land there. Warm pods are created before their tenant is known, so a dedicated tenant may only use single-tenant pools; admission refuses a dedicated tenant on a shared pool. No soft anti-affinity across tenants, because it scales badly and guarantees nothing. Dedicated capacity must be charged to the tenant, which is an open item for the billing design. | PM-2, "Instructions for reviewers of other designs" |
| D7 | **Placement offerings: approved, with operator control via the API.** An offering is a named option (for example `shared`, `dedicated`, `fast-nvme`) backed by a `nodePlacement` and a node-tenancy mode. The operator can create, update, or delete offerings, grant or revoke an offering per tenant, and mark offerings `default`; default offerings are available to all tenants without a grant. Excluding a specific tenant from a default offering is an open question (O2). Every pool references one offering. Routing adds a check next to `pool_tenant_access`: the pool's offering must be granted to the session's tenant, explicitly or as a default. An owned pool may pick only among its tenant's granted offerings. | PM-3 |
| D8 | **Placement requirements are filters only (confirmed by the owner).** Settings on the tenant or environment: `requireNodeSeparation`, `nodeTenancy: dedicated`, `allowSharedPod: false`, the region, plus the existing per-session `minIsolationProfile`. They never create nodes and never change pods or pools. They narrow pool selection at the post-chain point; with no match, the session is refused with an error naming the unmet requirement, never downgraded and never provisioned. Tenants never write node selectors or tolerations. `sessionIsolationLevel` reports node tenancy, class co-members, and shared pod. | PM-4 |
| D9 | **Operator and tenant split.** The operator owns classes and their nodes, relaxations, node tenancy, offerings and grants, `nodePlacement`, regions mapped to nodes, runtime isolation floors and pool grants, and priority classes, replicas, and PDBs. The tenant can only narrow, through requirements and the choice of offering on owned pools. | High-level design, PM-3, PM-4 |
| D10 | **T4 (coordinator's instruction of 2026-11-12).** The owner plans to retire T4 as a concept, with each control it bundles becoming its own setting under a pending `0150-tenant-security-settings`. This design does not tie placement to T4: it removes the T4 node label and taint and the `lenny-t4-node-isolation` webhook, and it expresses dedicated nodes only as the generic `nodeTenancy: dedicated`. | PM-2, PM-4, "Specification changes" |
| D11 | **Open decisions O1, O2, O4, O5, and O7 (2026-11-12).** Option (a) for each. On O5 the owner asked what a preset is and then took "presets as defined". | PM-1, PM-3, PM-4 |
| D12 | **O3 (2026-11-12).** "Drop `lenny-agents-kata`, and reproduce the per-class aggregate quota with a per-class PriorityClass and a ResourceQuota scoped by PriorityClass." The owner's condition was that node classes give the same result; the coordinator's answer was that the separation result is the same except for the per-profile aggregate quota, which the per-class priority class reproduces. | PM-8, PM-9 |
| D13 | **O6 (2026-11-12).** "User namespaces on by default for the standard class. The operator may disable them, which logs a loud warning and renders without them. There is no `preferred` mode. The adapter checks uid_map before READY." | PM-1, PM-5, PM-6 |
| D14 | **O8 (2026-11-12).** The `regulated` template of the tenant security settings sets dedicated node tenancy. | PM-2, [`0150-tenant-security-settings`, "TSS-2"](../0150-tenant-security-settings/change.md#tss-2-templates) |
| D15 | **Consistency pass (2026-11-12).** The per-tenant `nodeTenancy` lives in `securitySettings.placement.nodeTenancy` of the tenant security settings, and `allowReusedPod: false` joins the placement requirements, as that design asks. | PM-2, PM-4 |
| D16 | **Agent runtimes only (2026-11-13).** "Remove non-agent runtimes altogether, in a new design: `type: mcp` and service mode (`executionMode: service`)", which [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md) records with the removal of service-mode tenant-affinity routing. Every pool runs session-mode agent pods, so no requirement or report carries a service-mode case. | PM-4, "Current state", "Removals" |
| D17 | **Billing of dedicated capacity (2026-11-13).** The owner settled the billing design's O1, in the words "BR O1: node.dedicated_usage": a periodic `node.dedicated_usage` event reports each tenant's dedicated node capacity, which closes D6's open item. | PM-2, "Billing" |
| D18 | **Relaxing an environment's requirements (2026-11-13).** The owner answered "5 ok" to the consolidated list of open decisions: removing an environment's `placementRequirements` is a relaxation under the rules of the tenant security settings, with a justification and the weakening delay. This replaces revision 3's rule that an environment's removal applies at once and takes no delay. | PM-4 |
| D19 | **`allowReusedPod` set by the tenant's security admin (2026-11-13).** The owner answered "15 ok": a tenant's `security-admin` may set `allowReusedPod: false` as a tenant requirement in the tenant security record, as revision 3 states. | PM-4 |
| D20 | **Chart value names (2026-11-13).** The owner answered "18–21 ok", confirming as written the defaults and names of the consolidated list, among them `kata.emptyDirMode` and `kata.volumeGid`, which PM-6's `fsGroup` row reads. | PM-6 |
| D21 | **Project quotas (2026-11-14).** The owner answered "Adopt the automatic project-quota mode": a node agent that holds `CAP_SYS_ADMIN` and a `hostPath` of the kubelet's directory, and that runs without `privileged: true`, detects project quotas on each node, labels the node, and gives slot volumes a quota slightly below their `sizeLimit`. [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md#pl-10-project-quotas) owns the mechanism as its D25. This design places the agent, `lenny-quota-agent`, as a named node agent in `lenny-node-agents` on every node of every agent class, admits it, and limits its node writes. Those placement choices were the orchestrator's, and the owner confirmed them, with the agent's rights, on 2026-11-14 ("Ok on the rest"). | PM-5, PM-6, PM-8, PM-9 |
| D22 | **Quota agent hardening (2026-11-14).** The owner answered "add the two hardenning steps to lenny-quota-agent": a custom `Localhost` seccomp profile shipped with the chart, and a Landlock self-restriction at start-up. [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md#pl-10-project-quotas) owns both as its D28. This design's admission refuses a quota agent pod without the profile; the orchestrator chose that rule, and the owner confirmed it with the profile's installation by the `seccomp-install` init container on 2026-11-14 ("2. ok"). | PM-6, PM-9 |
| D23 | **Quota agent status (2026-11-15).** The owner answered "3: ok": `lenny-quota-agent` serves no port and writes its figures as the annotation `lenny.dev/quota-agent-status` on its own node, which the admission policy allows beside the `lenny.dev/project-quota` label and nothing else, and `lenny-ops` exports them as metrics and raises `QuotaAgentUnavailable`. [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md#pl-10-project-quotas) owns the agent's side as its D29. This design's `lenny-quota-agent-node-label` admits the annotation, with a value of at most 1 KiB, and its continuous check reads it. | PM-6 |
| D24 | **Residency across regions (2026-11-15).** The owner asked how region placement works and whether a Kubernetes cluster can span regions, and answered "ok, add to the placement design" to the research summary and the recommendation that residency needs one Lenny deployment per region. PM-7 **Residency across regions.** states the finding, the deployment model, and what a stretched cluster does and does not give. | PM-7 |

The related decisions of the same day that this design builds on: runc pools require `hostUsers: false` so each pod gets its own host UID range ([`0145-session-isolation`](../0145-session-isolation/change.md), cross-pod kernel UID sharing, option a); runc is described factually and `allowStandardIsolation` stays; the pod layout moved to [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md); Kata block-plain pools may set an `fsGroup` with a dedicated GID; and [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) replaces `workspaceTier` with explicit tenant settings, including the tenant's node tenancy.

### Settled open decisions O1 to O8

The owner settled O1 to O8 on 2026-11-12 (D11 to D14). Each entry keeps its options and reasoning as a record and states the option taken.

**O1. Confirmation of classes and presets as the override form.**

- (a) Node classes as groups of members, filled by a preset or written as `custom`, with `acknowledgeMixedClass` on any class with more than one member.
- (b) Fixed classes with per-pair `allowSharing` lists.
- (c) Per-pool relaxation flags.
- (d) Presets alone.

**Recommendation: (a).** Under (b) a second list has to stay consistent with the classes, and two statements of one fact drift. Under (c) one pool's flag weakens every pool scheduled on the same nodes, which that pool's owner did not choose. Under (d) an operator whose node pools do not match a preset has no way to say so.

**Settled (2026-11-12): (a)** (D11).

**O2. Excluding a tenant from a default offering.**

- (a) No exclusion. An operator who must keep one tenant off an offering makes it non-default and grants it to the other tenants.
- (b) Per-tenant exclusion rows on a default offering, with `POST /v1/admin/placement-offerings/{name}/tenant-exclusions` and its list and delete routes.
- (c) A tenant-record field listing the default offerings the tenant may not use.

**Recommendation: (a)** for the first version. One rule, explicit grants plus defaults, decides availability. The case that motivates exclusion most, a dedicated tenant that must not land on shared nodes, is covered by that tenant's implied `nodeTenancy: dedicated` requirement ("PM-4"). (b) can be added later without changing the stored grant rows.

**Settled (2026-11-12): (a)** (D11).

**O3. Dropping `lenny-agents-kata`.**

- (a) Drop it. Every agent pod runs in one agent namespace, and node classes do the separation the namespace was meant to give.
- (b) Keep it, and add a pool-to-namespace mapping so microvm pools render into it.

**Recommendation: (a).** The tree already runs one agent namespace: the PoolScalingController takes a single `--agent-namespace`, and no pool-to-namespace mapping exists ([DR-10](../0147-tenant-derived-runtimes/change.md#dr-10-capacity-reservation), "Who can turn it on") **[source]**. A namespace boundary separates API objects, quotas, and network policies, and a node class separates kernels and hardware. Kata pods already run under the same network policies as the other agent pods. (b) would add the mapping, a second quota, and a second set of policies with no separation that classes do not give.

**Settled (2026-11-12): (a)** (D12). The quota the second namespace gave is kept per node class: each agent class gets its own `PriorityClass`, and an optional `ResourceQuota` in `lenny-agents` scoped to that `PriorityClass` caps the class's aggregate requests ("PM-8"). The admission webhook refuses an agent pod whose priority class is not its class's ("PM-6"), so a pod cannot leave its class's quota.

**O4. Per-class tenancy defaults.**

- (a) Every agent class defaults to `shared`. The operator sets `dedicated` per class or per tenant, and the standard class reports its shared tenancy in preflight and in `sessionIsolationLevel`.
- (b) The `standard` class defaults to `dedicated`, so every runc pool is single-tenant unless the operator sets `shared`.

**Recommendation: (a).** runc pods on one node share a kernel across the tenants scheduled there unless the operator dedicates nodes per tenant, and user namespaces bound what an escape lands as without closing kernel exploits. Under (b) every runc pool becomes single-tenant by default, which removes shared warm pools for that profile and turns a profile choice into a capacity plan. (a) keeps the profile choice the operator's (D4) and makes the fact visible: preflight in production mode warns when a `standard` pool with shared tenancy is granted to more than one tenant, and a tenant that cares sets `nodeTenancy: dedicated` as a requirement.

**Settled (2026-11-12): (a)** (D11).

**O5. Whether `compact` exists as a preset.**

- (a) Keep `compact`: all agent members in one class, the system class apart.
- (b) Drop it, and have an operator who wants it write the classes under `custom`.

**Recommendation: (a).** Two node pools, one for the platform and one for agents, is the arrangement most small clusters start from. The preset still requires `acknowledgeMixedClass: true`, so it relaxes nothing silently.

**Settled (2026-11-12): (a)**, presets as defined (D11).

**O6. Meaning of `userNamespaces: preferred` on the standard class.**

- (a) The controller renders `hostUsers: false` on a standard-class pod when the latest node check found every node of the class meeting the user namespace prerequisites, and leaves `hostUsers` unset otherwise. The choice is made when the pod is created and recorded on its `AgentPod`, and `sessionIsolationLevel.userNamespaces` reports it.
- (b) The controller always renders `hostUsers: false`, and `preferred` only lowers the node check's failures to warnings.

**Recommendation: (a).** Under (b) a pod on a node without the prerequisites fails to start, or on a cluster where the feature is off, the API server drops the field with no error and the pod runs in the node's user namespace: [`0127-egress-and-git`](../0127-egress-and-git/change.md#git-mirror-lenny-git-mirror) measured that on Kubernetes 1.31.4 **[measured]**. (a) is deterministic per pod and reports what each session got.

**Settled (2026-11-12): neither option (D13).** The owner's model replaces both. User namespaces are on by default for the standard class (`userNamespaces: enabled`), and every standard-member pod renders `hostUsers: false`. The operator may set `userNamespaces: disabled`, which renders pods without `hostUsers` and logs a loud warning: a preflight warning, a warning in the controller and gateway logs at start and on each configuration change, the standing alert `PlacementUserNamespacesDisabled` in production mode, the relaxation in `platform.placement_config_changed`, and `sessionIsolationLevel.userNamespaces: false` on every affected session. There is no `preferred` mode and no per-pod choice. The adapter's `uid_map` check before READY stays, so an enabled class cannot degrade silently on a cluster whose API server drops `hostUsers` ("PM-6").

**O7. Where a tenant's own requirements are stored.**

- (a) In the tenant security record of [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture), as a `placement` section, so that a weakening takes the record's delay and its audit.
- (b) In a new field of the tenant record, written by `tenant-admin`, with no delay.

**Recommendation: (a).** SO-2 states that one record per tenant holds every customer-set security constraint. A requirement is such a constraint, and its removal by a stolen `tenant-admin` credential is the case the weakening delay exists for.

**Settled (2026-11-12): (a)** (D11).

**O8. T4 placement.** This decision moved to [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md), which decides whether its templates require `nodeTenancy: dedicated` for the tenants that today set `workspaceTier: T4`. This design offers the setting and its enforcement ("PM-2" and "PM-4") and keeps no T4-specific rule.

**Settled (2026-11-12) (D14).** The `regulated` template sets `placement.nodeTenancy: dedicated`, and `standard` and `confidential` set `shared` ("TSS-2").
