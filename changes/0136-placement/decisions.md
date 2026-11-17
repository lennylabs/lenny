# Decisions: 0136 Placement

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

## Open decisions

The owner settled every open decision on 2026-11-12 (D11 to D14). Each entry keeps its options and reasoning as a record and states the option taken.

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
