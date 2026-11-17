# Placement

Status: `Draft`, revision 14, 2026-11-16. Programs: [v1](../programs/v1.md).

Each factual claim carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, or a design on 2026-11-12. |
| **[measured]** | Measured in the spikes of 2026-11-11 or 2026-11-12, which [`0145-session-isolation`, "Validation record"](../0145-session-isolation/steps.md#validation-record) and [`0127-egress-and-git`, "Validation record"](../0127-egress-and-git/steps.md#validation-record) record. |
| **[external]** | Stated by an upstream or vendor source that another design read and lists under its "Sources". |
| **[design]** | What this design requires. Nothing implements it. |
| **[inference]** | Reasoned from the sources above and not checked. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

Lenny decides where its pods run through rules scattered across the specification and several designs, and those rules leave the most exposed combinations open:

- Kata pods must run on dedicated nodes, and the only stated reason is a container escape from a co-located runc pod ([§17.2](../../spec/17_deployment-topology.md#172-namespace-layout), **Node isolation**) **[source]**. Nothing keeps runc pods off nodes that run gVisor pods, and nothing keeps any agent pod off nodes that run Lenny's own components. [`0127-egress-and-git`, "Network policy and placement"](../0127-egress-and-git/change.md#network-policy-and-placement) states that "the specification defines no system node pool" **[source]**. A kernel exploit from a runc pod therefore reaches co-located gVisor sandboxes and every service-account token mounted on that node.
- The dedicated-node rule for `workspaceTier: T4` keeps non-T4 agent pods off T4 nodes, and lets every T4 tenant and every isolation profile share them ([§6.4](../../spec/06_warm-pod-model.md#64-pod-filesystem-layout), **T4 dedicated-node requirement**) **[source]**. Its stated rationale is that "a node compromise exposes all T4 tenants co-located on that node", which the rule does not prevent. [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) retires `workspaceTier` and gives each control it bundled a setting of its own.
- No pool can choose its nodes. The only per-pool node controls are the T4 selector, the Kata controls, and `reservedPlacement` of [DR-10](../0147-tenant-derived-runtimes/change.md#dr-10-capacity-reservation), which applies to reserved pods alone and can move them onto another profile's nodes **[source]**.
- A pool's region is a label that routing compares with a tenant's `dataResidencyRegion` for delegation only ([§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces), **Data residency**). No post-chain check compares them, and no pod builder turns the region into a node constraint **[source]**.
- Several system components have no stated namespace, replica count, disruption budget, spread, or priority, and no Lenny pod carries a `PriorityClass` **[source]**.

The owner decided on 2026-11-12 that placement gets one design, that system pods never share nodes with agent pods except in the single-node local stack, that each pool gets a platform-admin `nodePlacement`, that runc pools require user namespaces, and that placement constraints are configurable ("Decisions" in `decisions.md`). The owner asked to "make placement rules configurable".

## Scope

**In scope:**

- Node classes, which group workload members onto sets of nodes, and presets that fill them ("PM-1").
- Node tenancy, which dedicates nodes to one tenant ("PM-2").
- Placement offerings, the named node options a platform admin grants to tenants, and the per-pool `nodePlacement` they carry ("PM-3").
- Placement requirements, the filters a tenant, an environment, or the operator sets on a tenant ("PM-4").
- How agent pods, system components, named node agents, and third-party DaemonSets are placed ("PM-5").
- Admission, preflight, and continuous checks ("PM-6").
- Region placement ("PM-7").
- The namespace, replicas, disruption budget, anti-affinity, spread, and priority of every system component ("PM-8").
- The namespace layout ("PM-9").
- The removal of `recycle.allowCrossTenantReuse` ("Removals").

**Out of scope:**

- Which tenants get dedicated nodes. [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) retires `workspaceTier`, holds the per-tenant `securitySettings.placement.nodeTenancy` with the tenant's other placement settings, and ships templates, of which `regulated` sets dedicated tenancy ("TSS-1", "TSS-2", and "TSS-6"). This design removes the T4 node label, taint, and webhook, and states the mechanism that the setting selects ("PM-2" and "PM-4").
- GPUs, which v1 does not support ([§23](../../spec/23_competitive-landscape.md)) **[source]**.
- Creating nodes. Lenny creates no node and no node pool. The operator provisions nodes and maps them onto classes.
- The renames of the pool's shared-pod settings, which [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) records, and billing against `netagent`. This design uses the new names where it names those settings: `sessionPolicy.maxConcurrentSessionsPerPod` and `acknowledgeSharedPod`.
- The choice of isolation profile. The operator chooses a pool's profile, and this design places pods of each profile.

## Terms

| Term | Meaning |
| :-- | :-- |
| Member | A category of Lenny workload that placement assigns to nodes as one unit. The members are `system` (every Lenny system component), `standard` (agent pods with `isolationProfile: standard`, which run under runc), `sandboxed` (agent pods under gVisor), and `microvm` (agent pods under Kata). |
| Node class | A named group of one or more members that share one set of nodes, selected by the class's node selector and reserved by its taint. Each member belongs to exactly one class. "PM-1" defines it. |
| Mixed class | A node class with more than one member. Mixing relaxes separation and requires `acknowledgeMixedClass: true` on the class. |
| Preset | A named assignment of members to classes that the chart generates: `strict`, `compact`, or `single-node`. `custom` lets the operator assign members freely. |
| Agent class | A node class whose members are agent members only. |
| Node tenancy | Whether a pod may share its nodes with other tenants' pods (`shared`) or runs only on nodes reserved for its own tenant (`dedicated`). "PM-2" defines it. |
| Dedicated node | A node that carries the label `lenny.dev/tenant=<tenantId>` and the taint `lenny.dev/tenant=<tenantId>:NoSchedule`, so that only that tenant's agent pods schedule there. |
| Single-tenant pool | A pool whose pods have one tenant from creation: an owned pool of a tenant-derived runtime, or a platform pool whose `placement.tenantId` names one tenant. The controller stamps the tenant on each pod at creation. |
| `nodePlacement` | A node selector and a list of tolerations that narrow where a pod runs within its node class. |
| Placement offering | A named, platform-admin-defined option, such as `shared`, `dedicated`, or `fast-nvme`, that carries a `nodePlacement`, a node tenancy, and the classes it applies to. Every pool references one. "PM-3" defines it. |
| Default offering | An offering marked `default: true`, which every tenant may use without a grant. |
| Offering grant | A row that makes a non-default offering available to one tenant. |
| Reserved overlay | A `nodePlacement` that a pool applies to its reserved pods alone, bounded by the pool's class. It replaces `reservedPlacement` ("PM-3"). |
| Placement requirement | A filter on the pools a session may run on. Requirements narrow pool selection and never create nodes, change pools, or downgrade a session. "PM-4" defines them. |
| Effective requirements | The union of the requirements that apply to one session: its tenant's, its environment's, the operator's per-tenant setting, and its delegation parent's. |
| Named node agent | A Lenny workload that runs on agent nodes by design, with tolerations the admission webhook allows by name: `lenny-activity-collector`, `lenny-quota-agent`, and forensics capture Jobs ("PM-5"). |
| Node check | The placement checks over nodes and pods that preflight runs at install and upgrade and `lenny-ops` runs continuously ("PM-6"). |
| Separation matrix | The table that states, per preset, which members and tenants can share a node ("PM-1"). |
| Labels | Parts of the design carry the label PM-n, requirements carry the label PMR-n, and open decisions carry the label O-n. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Isolation profile | A pool's sandboxing level, `standard` (runc), `sandboxed` (gVisor), or `microvm` (Kata), each mapped to one `RuntimeClass` ([§5.3](../../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles)). runc shares the host kernel and gives no protection against kernel exploits. gVisor intercepts system calls in a user-space kernel. Kata runs each pod in a lightweight virtual machine with its own guest kernel. |
| `allowStandardIsolation` | The pool flag through which an operator opts a pool into the `standard` profile ([§5.3](../../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles)). It stays. |
| User namespaces and `hostUsers: false` | A Kubernetes pod setting under which every UID in the pod maps to an unprivileged host UID range, so an escape from the container lands as an unmapped UID on the node. gVisor refuses it **[measured]**. "PM-1" states its prerequisites, and [`0145-session-isolation`](../0145-session-isolation/change.md) records the measurements. |
| Taint and toleration | A node taint repels every pod that lacks a matching toleration. A toleration permits scheduling on a tainted node and does not attract the pod there. |
| Required node affinity | A pod constraint (`requiredDuringSchedulingIgnoredDuringExecution`) that the scheduler must satisfy. A label removed after scheduling does not evict the pod. |
| `RuntimeClass` scheduling | The `scheduling.nodeSelector` and `scheduling.tolerations` fields of a `RuntimeClass`, which Kubernetes merges into every pod that names the class at admission. |
| `PriorityClass` | A cluster-scoped Kubernetes object that sets a pod's scheduling priority, which the scheduler uses for preemption and the kubelet uses to order node-pressure evictions. |
| PodDisruptionBudget (PDB) | A Kubernetes object that bounds voluntary evictions of a set of pods, which `kubectl drain` and the eviction API honour. |
| Pool access | A platform pool serves a tenant through a `pool_tenant_access` row ([§15.1](../../spec/15_external-api-surface.md#151-rest-api)). An owned pool serves only its owner ([DR-5](../0147-tenant-derived-runtimes/change.md#dr-5-owned-pools)). |
| Tenant pin | The `lenny.dev/tenant-id` pod label that the gateway stamps at first assignment, or the controller stamps at creation on an owned pool, after which the pod serves only that tenant ([§5.2](../../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), **Tenant pinning**). |
| Post-chain point | The single step of session creation after the `PreRoute` chain and revision selection and before the pod claim, at which every check that depends on the runtime, the revision, or the pool runs ([`0141-runtime-routing`, "Post-chain point"](../0141-runtime-routing/change.md#post-chain-point)). |
| Pod set | The warm pods of one pool built for one pod-set key, of which a pool has one outside a rollout ([RV-3](../0140-runtime-revisions/change.md#rv-3-pods)). |
| Isolation floor | A runtime's `isolationProfile`, which names the weakest profile a pool may run it at ("Isolation floor" under [RV-1](../0140-runtime-revisions/change.md#rv-1-revisions)). |
| `minIsolationProfile` | The weakest isolation profile a session accepts, carried on the session, the delegation lease ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)), and as a tenant floor. |
| `sessionIsolationLevel` | The object in the session creation response and session metadata that reports the isolation the session received ([§7.1](../../spec/07_session-lifecycle.md#71-normal-flow)). |
| Tenant security settings | `Tenant.securitySettings`, the operator-owned, versioned settings of [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md#tss-1-settings-model), written only by `platform-admin` through `PUT /v1/admin/tenants/{id}/security-settings` or a template apply. A relaxation takes the weakening delay of the tenant security record. Its `placement` section holds `nodeTenancy`, `minIsolationProfile`, `allowSharedPod`, and `allowReusedPod` ("TSS-6"). |
| `dataResidencyRegion` | The optional tenant and environment field that confines a tenant's sessions and data to one region ([§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces)). |
| Production mode | A deployment whose `environment` value is `prod` ([§17.9.1](../../spec/17_deployment-topology.md#1791-composition-dimensions)). |
| `lenny up` | The single-binary local stack on an embedded single-node k3s cluster ([§17.4](../../spec/17_deployment-topology.md#174-local-development-mode-lenny-dev), **Embedded Mode**). |
| `lenny-pod-security` | The fail-closed validating webhook that checks every agent pod's security posture at creation (`pkg/admission/webhook/pod_security.go`) **[source]**. |
| `lenny-pool-config-validator` | The in-object validating webhook on `PodSet` writes ([§17.2](../../spec/17_deployment-topology.md#172-namespace-layout)). |
| `lenny-ops` | The mandatory operability service ([§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service)). |
| `platform-admin` and `tenant-admin` | Lenny's administrative roles. A `platform-admin` administers the platform and every tenant. A `tenant-admin` administers one tenant. |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines: 0 static, 1 unit, 2 component, 3 contract, 4 integration, 5 end to end on Kind, 6 end to end on cloud, 8 chaos, 9 security, and 11 documentation. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| SI-1, SI-11, SIR-17, and SI O8 | [`0145-session-isolation`](../0145-session-isolation/change.md) | SI-1 is the slot layout and stated the 1.27 Kubernetes floor. SI-11 is the hardening with user namespaces. SIR-17 stated that no requirement depends on user namespaces, and O8 made them a pool opt-in; the owner's decision of 2026-11-12 turned O8 into a requirement on runc pools. This design states that requirement for the `standard` class as a default the operator may turn off (O6). |
| TSS-1, TSS-2, TSS-6, and TSS O7 | [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md#tss-6-placement-settings) | The tenant security settings, the built-in templates `standard`, `confidential`, and `regulated`, and the placement settings that enter a session's effective requirements with `source: operator`. TSS O7 makes `placement.nodeTenancy: dedicated` mean this design's dedicated tenancy. |
| PL-1 and PLR-10 | [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md#pl-1-pod-blueprint) | The pod blueprint and its pod-wide settings, including the rule that a pod sets no `fsGroup` outside Kata block-plain pools. |
| EG-2, EG-9, EG-11, and egress groups | [`0127-egress-and-git`, "Steps"](../0127-egress-and-git/steps.md#steps) and ["Defaults, replacement, and augmentation"](../0127-egress-and-git/change.md#defaults-replacement-and-augmentation) | EG-2 is the pod network agent and the `proxied` profile. EG-9 is the egress policy objects. EG-11 is the egress PKI. An egress group is an additional `lenny-egress` Deployment whose traffic leaves from a dedicated address. |
| OC-1 | [`0148-tenant-outbound-connector`, "Proposals"](../0148-tenant-outbound-connector/steps.md#steps) | The outbound gateway, its namespace, and its deployment rules. |
| FE-1, FE-5, and FE-6 | [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md#fe-1-quarantine) | Quarantine, the capture runbook with its node-side capture Jobs in `lenny-forensics`, and agent activity streams with the `lenny-activity-collector` DaemonSet. |
| DR-5 and DR-10 | [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md#dr-5-owned-pools) | Owned pools with their template copy, and capacity reservation with `reservedPlacement` and the `lenny-reserved` `PriorityClass`. |
| RT-2, RT-4, and RT-5 | [`0141-runtime-routing`](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) | The interceptor contract and its post-chain point, runtime routes, and in-cluster interceptors in `gateway.interceptorNamespaces`. |
| RV-1, RV-3, and RV-4 | [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-1-revisions) | Revisions with the isolation floor, pods per pod set, and rollout. |
| TS-1 and TS-3 | [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) | Environment scoping with its post-chain checks, and a session's environment with cross-environment sharing. |
| SO-2 and SO-3 | [`0142-security-oversight`](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) | The tenant security record with its weakening delay and the security posture, and the `lenny-siem-forwarder` Deployment. |
| BR-3 | [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md#poolidle_usage) | `pool.idle_usage`, which attributes idle pod minutes. |
| SD-1 | [`0144-session-derive`](../0144-session-derive/change.md#sd-1-one-route) | The derive route, whose target checks run at the post-chain point. |

## High-level design

Every Lenny pod runs on a node of one **node class**. A class is a set of nodes the operator labels and taints, and a group of the workloads, called members, that may run there. The members are Lenny's system components and the three isolation profiles of agent pods: runc (`standard`), gVisor (`sandboxed`), and Kata (`microvm`). By default each member has its own class, so system components, runc pods, gVisor pods, and Kata pods never share a node. An operator who has fewer node pools can put several members in one class, which relaxes separation, and must acknowledge it on that class. Lenny reports the relaxation in preflight, in a standing alert in production mode, in the audit log, and in every affected session's `sessionIsolationLevel`. System components never share a class with agent pods, except in the single-node local stack. runc pods run with user namespaces by default; an operator may turn them off for the standard class, and Lenny reports that the same way. A **preset** fills the classes for the common cases: `strict` separates everything, `compact` puts all agent profiles in one class apart from the system class, and `single-node` puts everything on one node for `lenny up`.

Pods within one class can still belong to different tenants. **Node tenancy** decides whether they may. Under `shared`, which is the default, pods of several tenants share the class's nodes. Under `dedicated`, a pod runs only on nodes labelled and tainted for its own tenant. The operator chooses dedicated tenancy per class, through an offering, or per tenant through the tenant's security settings, which the `regulated` template sets. A pod's tenant must be known when the pod is created for this to work, and warm pods of an ordinary shared pool are created before any tenant claims them. Dedicated tenancy therefore applies only to single-tenant pools: an owned pool of a tenant-derived runtime, or a platform pool that names its one tenant.

A **placement offering** is how the operator lets pools choose nodes within a class. An offering names a node selector and tolerations, for example nodes with local NVMe disks, and a node tenancy. The platform admin creates offerings, marks some as available to every tenant, and grants the others per tenant through the admin API. Every pool references one offering. Routing refuses a session on a pool whose offering its tenant may not use, and a tenant's own derived-runtime pool may choose only among the offerings its tenant holds.

A **placement requirement** is a filter. A tenant, an environment, or the operator through the tenant's security settings can require node separation, dedicated tenancy, no pods shared with other sessions, no pods reused from earlier sessions, a region, or a minimum isolation profile. At the post-chain point the gateway keeps only pools that meet every requirement. When none does, it refuses the session with an error naming the unmet requirement. A requirement never creates a node, never changes a pool, and never runs a session on weaker placement than it asked for.

Control is split. The operator owns the classes and their nodes, the relaxations, node tenancy, offerings and grants, `nodePlacement`, the mapping from regions to nodes, runtime isolation floors and pool grants, and the priority classes, replica counts, and disruption budgets. A tenant can only narrow: through its requirements, and through the choice of a granted offering for its own derived-runtime pools. A tenant never writes a node selector or a toleration.

```
 strict preset (default), shared tenancy
 +--------------------+  +--------------------+  +--------------------+  +--------------------+
 | class system       |  | class standard     |  | class sandboxed    |  | class microvm      |
 | no taint           |  | taint: standard    |  | taint: sandboxed   |  | taint: microvm     |
 | gateway, Token Svc,|  | runc pods,         |  | gVisor pods        |  | Kata pods          |
 | controllers, ops,  |  | hostUsers: false   |  | + activity         |  |                    |
 | webhooks, stores,  |  |                    |  |   collector        |  |                    |
 | egress, mirror ... |  |                    |  |                    |  |                    |
 +--------------------+  +--------------------+  +--------------------+  +--------------------+
                          a dedicated tenant's nodes sit inside an agent class and add
                          lenny.dev/tenant=<id> as a label and a NoSchedule taint
```

## Requirements

| # | Requirement |
| :-- | :-- |
| PMR-1 | Outside the `single-node` preset, no node runs both a Lenny system component and an agent pod. Named node agents are the only Lenny workloads on agent nodes. |
| PMR-2 | No agent pod runs on a node of a class that does not contain its isolation profile's member. |
| PMR-3 | A class with more than one member exists only with `acknowledgeMixedClass: true`, and every session on it reports the co-members. |
| PMR-4 | Every standard-class agent pod runs with `hostUsers: false`, unless the operator set `userNamespaces: disabled` on the class, and an enabled pod whose user namespace is not in effect never reports READY. |
| PMR-5 | A pod with dedicated tenancy runs only on nodes labelled for its tenant, and no other tenant's pod and no system pod can schedule there. |
| PMR-6 | A pool with dedicated tenancy is a single-tenant pool, and a tenant whose security settings set `placement.nodeTenancy: dedicated` holds access to no pool with shared tenancy. |
| PMR-7 | Every pool references one offering, and a session runs on a pool only when the session's tenant may use that offering. |
| PMR-8 | No offering, `nodePlacement`, or reserved overlay can place a pod outside its class or onto another tenant's dedicated nodes. |
| PMR-9 | A session runs only on a pool and pod set that meet all of its effective requirements, and is refused with an error naming the unmet requirement otherwise. |
| PMR-10 | A session with `dataResidencyRegion` runs only on a pool in that region, for every creation kind, and a pool's pods run only on nodes of the pool's region. |
| PMR-11 | Every Lenny system component has a stated namespace, replica default, disruption budget, anti-affinity, spread, and priority class. |
| PMR-12 | No tenant input reaches a node selector, a toleration, or a node class. |
| PMR-13 | A relaxation of the default separation, a mixed class or disabled user namespaces, is visible in preflight, in an alert in production mode, in the audit log, and in `sessionIsolationLevel`. |
| PMR-14 | Every agent pod carries its class's agent priority class, or `lenny-reserved` when reserved, so a class's quota bounds every non-reserved pod of the class. |

## Security properties

- **P1. Kernel blast radius by class.** A kernel exploit from an agent pod reaches only nodes of its class. Under `strict`, a runc escape reaches no gVisor sandbox, no Kata guest's host, and no system component or its mounted credentials **[design]**.
- **P2. Escape lands unprivileged on runc nodes.** With `hostUsers: false`, a process that leaves a runc container is an unmapped host UID in a range the kubelet gives that pod alone, so per-UID node-wide limits are per pod as well **[design]**, as [`0145-session-isolation`](../0145-session-isolation/change.md) states for the cross-pod UID sharing decision.
- **P3. Tenant blast radius by tenancy.** Under dedicated tenancy, an escape reaches only the escaping tenant's own pods **[design]**. Under shared tenancy, an escape reaches the other tenants' pods of the same class on that node, which `sessionIsolationLevel.nodeTenancy` reports.
- **P4. No tenant-controlled placement.** Tenants choose among offerings the operator granted and set filters. Every selector and toleration comes from Helm values or a `platform-admin` write, and admission checks every agent pod against its class ("PM-6") **[design]**.
- **P5. Placement survives a bad pool write.** A pool or offering that tries to escape its class is refused by the gateway, refused again by `lenny-pool-config-validator` on the CRD write, and refused a third time by `lenny-pod-security` on the pod **[design]**.
- **P6. Drift is detected.** A node that loses its class label keeps its pods, because required node affinity is evaluated at scheduling only. The class taint keeps other workloads off it, and the continuous node check alerts on the drift ("PM-6") **[design]**.

What these properties do not cover:

- Shared node resources other than the kernel: a pod sees the node's page cache timing, its disk and network contention, and the cloud metadata service where network policy allows it.
- Third-party DaemonSets that tolerate every taint, such as many CNI, CSI, and logging agents. They run on every node by their own configuration, and the operator owns them ("PM-5").
- A hypervisor escape from a Kata guest, which reaches the Kata node and its co-located Kata pods of the same tenancy.

## Current state

### Specification

- [§17.2](../../spec/17_deployment-topology.md#172-namespace-layout), **Node isolation**, requires Kata pods on dedicated node pools through three controls: a `RuntimeClass` node selector `lenny.dev/node-pool: kata`, a required node affinity on the same label, and the taint `lenny.dev/isolation=kata:NoSchedule`. Its only stated reason is "a kernel compromise via an runc escape on a shared node" **[source]**.
- [§17.2](../../spec/17_deployment-topology.md#172-namespace-layout) lists four namespaces, `lenny-system`, `lenny-credentials`, `lenny-agents` for gVisor and runc pods, and `lenny-agents-kata` for Kata pods, and splits Pod Security enforcement by `RuntimeClass` through admission policies, with the namespaces at `warn` and `audit` **[source]**.
- [§6.4](../../spec/06_warm-pod-model.md#64-pod-filesystem-layout), **T4 dedicated-node requirement**, requires a pool whose runtime has `workspaceTier: T4` to select `lenny.dev/workspace-tier: t4` nodes tainted `lenny.dev/workspace-tier=t4:NoSchedule`, enforced by the `lenny-t4-node-isolation` webhook. The rule keeps non-T4 agent pods off T4 nodes. It does not separate T4 tenants from each other or isolation profiles from each other **[source]**. [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces), **T4 ephemeral workspace isolation**, states that this rule enforces "per-tenant key isolation at the node boundary" **[source]**.
- [§5.3](../../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles) describes `standard` as "Development/testing only" and its dev-mode fallback logs "Do not use in production" **[source]**.
- [§5.2](../../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) defines tenant pinning with the `lenny-tenant-label-immutability` webhook and soft topology spread across zones and nodes. It also defines `recycle.allowCrossTenantReuse` and states that a pod on a pool that sets it "is never reused across tenants, because the tenant pin applies on every pool" **[source]**.
- [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) restricts pod allocation for a tenant with `dataResidencyRegion` to pools whose `region` label matches, refuses a cross-region delegation with `REGION_CONSTRAINT_VIOLATED`, and fails session creation with `REGION_UNAVAILABLE` when no pool exists in the region **[source]**.
- [§17.1](../../spec/17_deployment-topology.md#171-kubernetes-resources) gives `lenny-ops` "1–2 replicas" and a PDB with `minAvailable: 1`. [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) gives a default of 1 replica and a PDB "always rendered, regardless of replica count", which it calls "effectively a no-op". [§25.16](../../spec/25_agent-operability.md#2516-deployment-topology-summary) gives 1 replica and the PDB "when replicas >= 2" **[source]**.
- [§17.6](../../spec/17_deployment-topology.md#176-packaging-and-installation) checks a Kubernetes server version of 1.27 or later, and [§17.4](../../spec/17_deployment-topology.md#174-local-development-mode-lenny-dev) pins k3s `v1.31.4+k3s1` for `lenny up` **[source]**.
- No section names a `PriorityClass` **[source]**.

### Tree

- `pkg/controller/agentpod/podspec/kata_node_isolation.go` and `t4_node_isolation.go` stamp the Kata and T4 controls, and `pkg/admission/t4_node_isolation` implements the T4 webhook **[source]**.
- `charts/lenny/values.yaml` sets `agentNamespaces` to `lenny-agents` and `lenny-agents-kata`, and the PoolScalingController takes one `--agent-namespace` **[source]**.
- `lenny-pod-security` intercepts Pod `CREATE` in agent namespaces only (`pkg/admission/webhook/pod_security.go`) **[source]**.
- No pool record, controller, or pod builder carries a region (`pkg/gateway/runtime/poolstore`, `pkg/controller/poolscaling`, and `pkg/controller/agentpod` contain none) **[source]**.
- The chart renders no `PriorityClass`, and agent pods carry none, as DR-10 also states **[source]**.
- The pool admission path validates `allowCrossTenantReuse` against the runtime's tier (`ValidateCrossTenantReuseTier` in `pkg/gateway/externalapi/admin/pools.go`) **[source]**.

### Rules this design takes over or points to

| Rule | Where it lives today | Under this design |
| :-- | :-- | :-- |
| Kata node isolation, controls 1 to 3 | §17.2 **Node isolation** | Restated per class for every agent class (PM-1, PM-5, PM-6). The Kata label and taint become the class label and taint. |
| T4 dedicated nodes and `lenny-t4-node-isolation` | §6.4, §12.8 | Removed. The generic dedicated tenancy replaces the mechanism, and [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) replaces the link to T4 with `securitySettings.placement.nodeTenancy`. |
| Profiles and RuntimeClasses | §5.3 | Unchanged. Each profile is a member (PM-1). |
| Isolation monotonicity and the post-chain `minIsolationProfile` check | §8.3, RT-2 | Unchanged. `minIsolationProfile` joins the effective requirements (PM-4). |
| Isolation floor | RV-1 | Unchanged, and applied before the requirement filter (PM-4). |
| Monitored gVisor may share nodes with unmonitored gVisor | FE-6 | Both are the `sandboxed` member (PM-1). |
| `reservedPlacement` and `lenny-reserved` | DR-10 | Folded into the reserved overlay (PM-3). The `PriorityClass` joins the scheme (PM-8). |
| Egress groups with a dedicated address | EG | A per-component placement within the system class (PM-8). |
| Namespaces and Pod Security by RuntimeClass | §17.2 | One agent namespace (PM-9). Pod Security by RuntimeClass is unchanged. |
| Outbound gateway namespace and anti-affinity | OC-1 | Namespace stays with OC. Anti-affinity rendered from both sides (PM-8). |
| Mirror namespace, Pod Security level, anti-affinity, and `gitMirror.hostUsers` | EG | Namespace, Pod Security level, and `hostUsers` stay with EG. The mirror is a system component on system nodes, and its anti-affinity is rendered from both sides (PM-8). |
| `lenny-forensics` namespace and capture Jobs | FE-5 | Capture Jobs are named node agents (PM-5). |
| Interceptor namespaces | RT-5 | Deployer workloads, documented (PM-5). |
| Component replicas, PDBs, anti-affinity, and spread | §17.1, §17.8.2 | One table (PM-8). |
| Tenant pinning, the label webhook, the owned-pool label, and the single-tenant rule for concurrent pods | §5.2, DR-5 | Unchanged. Single-tenant pools build on the owned-pool label (PM-2). |
| Topology spread | §5.2 | Unchanged, and combined with the class affinity (PM-5). |
| Quarantine | FE-1 | The node check never drains a quarantined pod (PM-6). |
| Residency routing and `lenny-data-residency-validator` | §12.8 | The routing check moves to the post-chain point for every creation kind. The webhook is unchanged (PM-7). |

### Gaps from the inventory and their resolution

| # | Gap | Resolution |
| :-- | :-- | :-- |
| 1 | Nothing separates runc pods from gVisor pods or from system pods. | Classes under `strict` (PM-1). |
| 2 | §17.2 names only runc as the reason for Kata node isolation. | The class rules state the reason per member pair (PM-1, separation matrix). |
| 3 | `lenny up`, dev mode, forensics capture Jobs, the activity collector, and third-party DaemonSets have no stated placement. | The `single-node` preset, named node agents, and documented third-party DaemonSets (PM-1, PM-5). |
| 4 | SIR-17 and SI O8 make user namespaces optional, SI-1 claims the 1.27 floor suffices, `lenny up` pins k3s 1.31.4, and the git mirror runs `hostUsers: true`. | The standard class runs with user namespaces by default, and the operator may disable them with a loud warning (O6), with Kubernetes 1.33 or later checked on standard-class nodes only. The k3s pin moves to 1.33 or later, and a missing prerequisite on the single-node preset is a warning. The mirror's `hostUsers` stays with EG, now on system nodes (PM-1, PM-6, PM-8). |
| 5 | T4 nodes are shared across T4 tenants and across profiles. Kata plus T4 has no rule. RV versions `workspaceTier`, so a rollout can mix T3 and T4 pod sets. §6.4 names "one-session-only pod lifecycle" as a control while T4 pools may recycle or run several slots. | The T4 placement mechanism is removed (D10). The profile mix disappears, because T4 pods no longer have their own nodes and every pod runs in its profile's class. The tenant mix is closed wherever the operator sets dedicated tenancy, which applies within any class, Kata included. The rollout mix disappears, because placement reads no versioned field. The §6.4 control list and the link from T4 to dedicated nodes go to [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md), whose `regulated` template sets dedicated tenancy. |
| 6 | Residency is unchecked after routing in RT, RV, TDR, SD, TS, and DA. A pool's region is metadata. Owned-pool template copy is silent on region. | A post-chain residency check, a region node selector, and a template-copied region (PM-7). |
| 7 | No pool-to-namespace mapping exists for `lenny-agents-kata`. TDR renders `lenny-reserved` "in the `--agent-namespace` alone", while a `PriorityClass` is cluster-scoped. | One agent namespace with a per-class priority class and quota (O3), and the scope correction (PM-8, PM-9). |
| 8 | No general per-pool node selection. | Offerings and per-pool `nodePlacement` (PM-3). |
| 9 | System components lack namespaces, replicas, PDBs, spread, priorities, and two-sided anti-affinity. `lenny-ops` replica and PDB statements conflict. | One table and a PriorityClass scheme, with `lenny-ops` settled at 1 replica and a PDB at 2 or more (PM-8). |
| 10 | `allowCrossTenantReuse` is inert. | Removed ("Removals"). |
| 11 | `reservedPlacement` refuses Kata and T4 placement and allows a runc template to place reserved pods on another profile's nodes. | The reserved overlay is bounded by the class (PM-3). |
| 12 | Renames of the shared-pod settings and billing against `netagent`. | Noted only. [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) records the renames, and the billing design owns billing. |

## Detailed design

### PM-1 Node classes and presets

#### Configuration

Classes and presets are Helm values under `placement`, because they describe nodes the operator provisions, which is the bootstrap plane of [§15.1](../../spec/15_external-api-surface.md#151-rest-api) **[design]**:

```yaml
placement:
  preset: strict                 # strict | compact | single-node | custom
  classLabelKey: lenny.dev/node-class
  regionLabel: topology.kubernetes.io/region
  classes:                       # per-class overrides; under custom, the full assignment
    system:
      members: [system]
      nodeSelector: { lenny.dev/node-class: system }
      taint: null                # system nodes carry no Lenny taint by default
    standard:
      members: [standard]
      nodeSelector: { lenny.dev/node-class: standard }
      taint: { key: lenny.dev/node-class, value: standard, effect: NoSchedule }
      tenancy: shared            # shared | dedicated
      userNamespaces: enabled    # enabled | disabled (standard member only)
      quota: null                # optional ResourceQuota hard limits for the class (PM-8)
    sandboxed:
      members: [sandboxed]
      nodeSelector: { lenny.dev/node-class: sandboxed }
      taint: { key: lenny.dev/node-class, value: sandboxed, effect: NoSchedule }
      tenancy: shared
      quota: null
    microvm:
      members: [microvm]
      nodeSelector: { lenny.dev/node-class: microvm }
      taint: { key: lenny.dev/node-class, value: microvm, effect: NoSchedule }
      tenancy: shared
      quota: null
  # acknowledgeMixedClass: true is required on any class whose members list has more than one entry
  componentPlacement: {}         # per system component, within the system class (PM-8)
  priorityClasses: {}            # PM-8
```

- **Members.** Each of `system`, `standard`, `sandboxed`, and `microvm` belongs to exactly one class. A member with no class is refused when a pool uses it, and a profile no pool uses may stay unassigned only under `custom`.
- **Node selector.** The labels a node of the class carries. Every agent pod of the class requires them through node affinity, and every system pod selects the system class's labels. A class may name existing node-pool labels and taints, such as a managed gVisor node pool's own label and taint, so an operator maps classes onto node pools it already has. Which provider labels are stable for this is under "Validation owed" in `steps.md`.
- **Taint.** The taint that keeps other pods off the class's nodes. Every agent class must carry one, because the taint is what keeps system pods and third-party pods without a matching toleration away. The system class carries none by default, so cluster add-ons that tolerate no Lenny taint, such as an ingress controller, can share system nodes. An operator may set one.
- **Tenancy.** The class's default node tenancy ("PM-2"). The system class is always `shared`.
- **User namespaces.** On the class that contains the `standard` member only. `enabled`, the default, renders `hostUsers: false` on every standard-member pod, and the node check fails on a node without the prerequisites. `disabled` renders standard-member pods without `hostUsers` and logs a loud warning (O6). There is no other value.
- **Quota.** Optional `ResourceQuota` hard limits for the class's aggregate requests, such as `requests.cpu`, `requests.memory`, and `pods`, rendered in the agent namespace and scoped to the class's agent priority class ("PM-8"). Unset renders no quota.
- **Presets.** `preset` generates class names and memberships. The operator may still override each generated class's node selector, taint, tenancy, quota, and user namespace setting by name. `custom` uses `classes` as written.

| Preset | Classes and members | Constraint |
| :-- | :-- | :-- |
| `strict` (default) | `system` {system}, `standard` {standard}, `sandboxed` {sandboxed}, `microvm` {microvm} | None. |
| `compact` | `system` {system}, `agents` {standard, sandboxed, microvm} | `classes.agents.acknowledgeMixedClass: true` is required, and the chart refuses the preset without it. Kata needs nodes with hardware virtualization, so a `compact` cluster that runs microvm pools runs every agent pod on such nodes. |
| `single-node` | `all` {system, standard, sandboxed, microvm}, empty node selector, no taint | Rendered only with `global.devMode: true`. The chart refuses it otherwise, and preflight fails it in production mode. |

Hard rules that no acknowledgment relaxes **[design]**:

- A class containing `system` contains no agent member, except under `single-node`.
- Every agent class has a taint, except under `single-node`.
- No two classes share a node selector, so no node can match two classes.

#### Mixing and its visibility

Putting several members in one class lets their pods share nodes. Under a mixed class with `standard` and `sandboxed`, a kernel exploit from a runc pod reaches the gVisor sandboxes on the same node. The class's `acknowledgeMixedClass: true` records that the operator accepts it, and the platform reports it in four places **[design]**:

- **Preflight.** A warning naming each mixed class and its members, in every mode.
- **Alert.** `PlacementClassMixed`, a warning that fires continuously in production mode while any class has more than one member, so the posture stays visible after install.
- **Audit.** The chart renders the resolved classes into the ConfigMap `lenny-placement-config`. The gateway reads it at start and on change, and writes `platform.placement_config_changed` with the classes, members, tenancy defaults, and user namespace settings, and the same event with `relaxations` listed while any relaxation is active. A mixed class and disabled user namespaces are relaxations. This follows the pattern of the elicitation integrity floor in [§17.2](../../spec/17_deployment-topology.md#172-namespace-layout).
- **Session.** `sessionIsolationLevel.nodeClassMembers` lists the members of the session pod's class ("PM-4").

#### User namespaces on the standard class

runc shares the host kernel and gives no protection against kernel exploits. Its blast radius is bounded by the node class, which keeps other members off its nodes, and by user namespaces, which land an escape as an unmapped, unprivileged host UID. The choice of profile stays the operator's, and `allowStandardIsolation` stays as its explicit opt-in (D4).

The prerequisites apply to standard-class nodes only, because gVisor refuses `hostUsers: false` and Kata does not need it **[measured]**:

| Prerequisite | Value |
| :-- | :-- |
| Kubernetes | 1.33 or later, the first release with the feature on by default **[external]**. |
| Container runtime | containerd 2.0 or later, or CRI-O 1.25 or later, with runc 1.2 or later **[external]**. |
| Kernel | Idmapped mounts for every volume type a Lenny pod mounts. tmpfs needs 6.3 or later. EKS AL2023 with kernel 6.1 failed and 6.18 worked **[measured]**. |
| Bottlerocket | `user.max_user_namespaces` above 0 **[measured]**. |

The deployment-wide Kubernetes floor of [§17.6](../../spec/17_deployment-topology.md#176-packaging-and-installation) stays at 1.27 for clusters with no standard-class pool. A cluster that runs a standard pool with user namespaces enabled needs 1.33 on the API server and on every standard-class node, which preflight checks.

**Disabling.** An operator whose standard-class nodes cannot meet the prerequisites sets `placement.classes.<class>.userNamespaces: disabled` on the class that contains `standard` (O6). Pods then render without `hostUsers`, and a process that leaves a runc container lands as its own UID on the node, which may be shared with other pods' UIDs. The setting needs no acknowledgment field, and it is never silent: preflight warns, the controllers and the gateway log a warning at start and on each configuration change, `PlacementUserNamespacesDisabled` fires continuously in production mode, `platform.placement_config_changed` lists it under `relaxations`, and each affected session reports `sessionIsolationLevel.userNamespaces: false`.

**No silent degradation.** On a cluster where the feature is off, the API server drops `hostUsers` with no error, and the pod runs in the node's user namespace: [`0127-egress-and-git`](../0127-egress-and-git/change.md#git-mirror-lenny-git-mirror) measured that on Kubernetes 1.31.4 **[measured]**. The adapter therefore reads `/proc/self/uid_map` on every pod rendered with `hostUsers: false` and refuses to report READY when UID 0 maps to node UID 0 ("PM-6"), so an enabled class never runs without its user namespaces.

#### The single-node preset

`lenny up` and dev mode run every pod on one node. The development profile of the Embedded Mode chart sets `placement.preset: single-node`. The preset renders no class selector and no class taint. The user namespace setting applies as on any preset: with `enabled`, standard-member pods render `hostUsers: false`, and a missing prerequisite is a preflight warning that names `userNamespaces: disabled` as the setting for a node that cannot meet it. The embedded k3s pin moves from `v1.31.4` to a 1.33 or later release, because on 1.31.4 the API server drops `hostUsers` with no error **[measured]**. Whether rootless k3s offers user namespaces to pods is under "Validation owed" in `steps.md`.

#### Separation matrix

| Pair on one node | `strict` | `compact` | `single-node` |
| :-- | :-- | :-- | :-- |
| System component and any agent pod | Never | Never | Allowed |
| runc pod and gVisor pod | Never | Allowed, acknowledged | Allowed |
| runc pod and Kata pod | Never | Allowed, acknowledged | Allowed |
| gVisor pod and Kata pod | Never | Allowed, acknowledged | Allowed |
| Monitored and unmonitored gVisor pods | Allowed (one member, FE-6) | Allowed | Allowed |
| Two tenants' pods of one member, tenancy `shared` | Allowed | Allowed | Allowed |
| Two tenants' pods, either with tenancy `dedicated` | Never | Never | Not applicable (one node) |
| Named node agent and an agent pod | Allowed, by name | Allowed, by name | Allowed |

Under `custom`, each pair follows from the class memberships the operator wrote, and every mixed pair is acknowledged on its class.

### PM-2 Node tenancy

#### Shared and dedicated

A pod set's node tenancy is `dedicated` when its pool's class default is `dedicated` or the pool's offering is `dedicated`, and `shared` otherwise **[design]**. The operator sets tenancy in two places:

- **Per class**, through `placement.classes.<class>.tenancy` (Helm). A class default of `dedicated` makes every pool in the class single-tenant.
- **Per tenant**, through `securitySettings.placement.nodeTenancy: dedicated` in the tenant security settings, written by `platform-admin` only, through `PUT /v1/admin/tenants/{id}/security-settings` or a template apply such as `regulated` ([`0150-tenant-security-settings`, "TSS-6"](../0150-tenant-security-settings/change.md#tss-6-placement-settings)). It is a statement that every pool the tenant uses has dedicated tenancy, and it becomes an implied requirement on every session of that tenant with `source: operator` ("PM-4"). A move back to `shared` is a relaxation and takes the weakening delay of the tenant security record. The tenant record itself carries no `nodeTenancy`.

A dedicated pod renders a required node affinity on `lenny.dev/tenant In [<tenantId>]` and a toleration for `lenny.dev/tenant=<tenantId>:NoSchedule`, on top of its class's affinity and toleration. The operator labels and taints each dedicated node with the tenant's identifier, within a node pool that also carries the class label and taint. A pod of another tenant lacks the toleration, and a system pod tolerates no tenant taint, so neither can schedule there. Kubernetes label values are at most 63 characters, so a tenant whose identifier exceeds that or is not a valid label value cannot be set to `dedicated`, and the write answers `422 CONFIGURATION_CONFLICT` **[design]**. The existing `lenny.dev/tenant-id` pod label carries the same limit today.

No soft anti-affinity between tenants is rendered (D6). A preferred anti-affinity term per tenant pair grows with the number of tenants and does not prevent co-location when the scheduler finds no other node.

#### Single-tenant pools

Warm pods are created before a session claims them, and on a shared platform pool their tenant is unknown until the claim. A node chosen at creation cannot be one tenant's dedicated node if the tenant is not yet known. Dedicated tenancy therefore requires that the pool fixes its tenant before pods exist **[design]**:

- **Owned pools** of tenant-derived runtimes. The controller already stamps `lenny.dev/tenant-id` with the owner at creation ([DR-5](../0147-tenant-derived-runtimes/change.md#dr-5-owned-pools), "Pods").
- **Single-tenant platform pools.** A new pool field, `placement.tenantId`, written by `platform-admin`, names the one tenant. The pool may hold a `pool_tenant_access` row for that tenant only, the controller stamps the label at creation as for an owned pool, and the label webhook's controller rule for owned pools extends to these pools. The `lenny.dev/served` reader rule of DR-5, which distinguishes served pods from pods stamped at creation, applies to them as well.

A pinned pod of a shared pool does not qualify. It was placed before its pin, on a node any tenant could use.

#### Admission and grant rules

| Write | Rule | Refusal |
| :-- | :-- | :-- |
| Pool create or update | A pool whose tenancy is `dedicated` must be a single-tenant pool. | `422 INVALID_POOL_CONFIGURATION`, `details.rule: dedicated_requires_single_tenant` |
| Pool create or update | A single-tenant pool whose tenant's security settings set `placement.nodeTenancy: dedicated` must have tenancy `dedicated`. | `422 INVALID_POOL_CONFIGURATION`, `details.rule: tenant_requires_dedicated` |
| `POST /v1/admin/pools/{name}/tenant-access` | A dedicated or `placement.tenantId` pool accepts no second tenant, and a tenant whose settings set `placement.nodeTenancy: dedicated` gets no access to a pool with shared tenancy. | `422 CONFIGURATION_CONFLICT`, naming the pool and the tenant |
| `PUT /v1/admin/tenants/{id}/security-settings` or `POST /v1/admin/tenants/{id}/security-settings/apply-template` setting `placement.nodeTenancy: dedicated` | Refused while the tenant holds access to a pool with shared tenancy, or when the tenant identifier is not a valid label value. The `details.conflicts` entries name those pools, and `?dryRun=true` lists them without writing. A `POST /v1/admin/tenants` that applies such a template to a new tenant holds no pool access yet and passes. | `422 CONFIGURATION_CONFLICT` |
| Session creation | A session whose effective requirements include `nodeTenancy: dedicated` runs only on a pod set with dedicated tenancy ("PM-4"). | `422 PLACEMENT_REQUIREMENT_UNMET` |

#### Billing

Dedicated nodes cost the operator capacity whether or not the tenant's pods fill them, and D6 charges that capacity to the tenant. [`0121-billing-resource-attribution`, "Dedicated node capacity (BR-7)"](../0121-billing-resource-attribution/change.md#dedicated-node-capacity-br-7) carries it as the periodic event `node.dedicated_usage`, settled by the owner on 2026-11-13 as that design's decision 17 (D17): per tenant, node class, and usage window, the node minutes and the allocatable CPU, memory, and ephemeral storage of the Ready nodes that carry `lenny.dev/tenant=<tenantId>` and an agent class's label, sampled by the WarmPoolController. A node with a tenant label and no agent class label is reported by the continuous check ("PM-6") rather than billed. The pods on those nodes are still billed through the session and idle events, and the operator's billing system decides which figure to price.

### PM-3 Placement offerings and per-pool `nodePlacement`

#### The resource

An offering is a platform-global admin resource, stored in Postgres like runtimes and pools **[design]**:

```yaml
# POST /v1/admin/placement-offerings
name: fast-nvme
description: Nodes with local NVMe disks
classes: [sandboxed, microvm]   # node classes the offering may be used in
tenancy: shared                 # shared | dedicated
default: false                  # true: every tenant may use it without a grant
nodePlacement:
  nodeSelector: { lenny.dev/disk: nvme }
  tolerations:
    - { key: lenny.dev/disk, operator: Equal, value: nvme, effect: NoSchedule }
```

The chart's bootstrap seed creates one offering, `shared`, with every agent class in `classes`, `tenancy: shared`, `default: true`, and an empty `nodePlacement`. A pool created with no offering references the offering that the Helm value `placement.defaultPoolOffering` names, `shared` by default.

#### Admin API

The endpoints follow the conventions of [§15.1](../../spec/15_external-api-surface.md#151-rest-api): `{name}` as the path identifier, `If-Match` on updates, and tenant-access sub-resources shaped as those of runtimes and pools **[design]**.

| Method | Endpoint | Description |
| :-- | :-- | :-- |
| `POST` | `/v1/admin/placement-offerings` | Create an offering. Requires `platform-admin`. Refuses a duplicate name with `409 RESOURCE_ALREADY_EXISTS` and an offering that escapes its classes ("Validation") with `422 CONFIGURATION_CONFLICT`. |
| `GET` | `/v1/admin/placement-offerings` | List offerings. `platform-admin` sees every field. `tenant-admin` and `tenant-viewer` see the offerings their tenant may use, with `name`, `description`, `classes`, `tenancy`, and `default`, and without `nodePlacement`. |
| `GET` | `/v1/admin/placement-offerings/{name}` | Get one offering, under the same visibility. Returns `404 RESOURCE_NOT_FOUND` for an offering the caller's tenant may not use. |
| `PUT` | `/v1/admin/placement-offerings/{name}` | Update an offering. Requires `If-Match` and `platform-admin`. A change to `nodePlacement`, `tenancy`, or `classes` is validated against every pool that references the offering, and a change that would make one of them invalid is refused with `422 CONFIGURATION_CONFLICT` naming the pools. Running pods keep their placement, and pods created afterwards take the new one, as any pool edit does today. |
| `DELETE` | `/v1/admin/placement-offerings/{name}` | Delete an offering. Requires `platform-admin`. Refused with `409 RESOURCE_HAS_DEPENDENTS` while any pool, including an owned pool or a template pool, references it. |
| `POST` | `/v1/admin/placement-offerings/{name}/tenant-access` | Grant a tenant the offering. Body: `{"tenantId": "<uuid>"}`. Creates a `placement_offering_tenant_access` row. Idempotent: returns `200` when the grant exists. Requires `platform-admin`. |
| `GET` | `/v1/admin/placement-offerings/{name}/tenant-access` | List grants. Returns `[{"tenantId", "tenantName", "grantedAt", "grantedBy"}]`. Requires `platform-admin`. |
| `DELETE` | `/v1/admin/placement-offerings/{name}/tenant-access/{tenantId}` | Revoke a grant. Returns `404 RESOURCE_NOT_FOUND` when the grant does not exist. Refused with `409 RESOURCE_HAS_DEPENDENTS` while an owned pool of that tenant references the offering. When platform pools that the tenant can access reference it, the revocation succeeds with a `Warning` header naming them, because routing then refuses the tenant's sessions on those pools. Requires `platform-admin`. |
| `GET` | `/v1/admin/tenants/{id}/placement-offerings` | The offerings the tenant may use: its grants and every default offering, under the tenant-visible fields. Requires `platform-admin`, or `tenant-admin` or `tenant-viewer` of that tenant. |

Each write emits an admin audit event (`placement.offering_created`, `placement.offering_updated`, `placement.offering_deleted`, `placement.offering_granted`, and `placement.offering_revoked`), and [SO-4a](../0142-security-oversight/change.md#so-4a-mirror-to-the-tenant-chain) mirrors a grant or revocation to the tenant's chain because it is a platform admin's write that changes what the tenant may use. A `default: true` flag set or cleared is an update, and its audit event lists `default` among the changed fields. Excluding one tenant from a default offering is O2.

#### Pools reference one offering

A pool gains a `placement` block, written by `platform-admin` on platform pools and copied from the template on owned pools **[design]**:

```yaml
placement:
  offering: fast-nvme          # required; defaults to placement.defaultPoolOffering
  tenantId: null               # single-tenant platform pool (PM-2)
  region: eu-west-1            # PM-7
  reservedOverlay:             # reserved pods only; replaces reservedPlacement
    nodeSelector: { lenny.dev/reserved: "true" }
    tolerations:
      - { key: lenny.dev/reserved, operator: Equal, value: "true", effect: NoSchedule }
```

The pool's class follows from its `isolationProfile`: the class whose members include that profile. Pool admission refuses a pool whose class is not in its offering's `classes` with `422 INVALID_POOL_CONFIGURATION` and `details.rule: offering_class_mismatch`.

#### Routing check

At the post-chain point, next to the pool access check of [DR-5](../0147-tenant-derived-runtimes/change.md#dr-5-owned-pools), the gateway checks that the final pool's offering is a default offering or is granted to the session's tenant. A failure refuses with `403 PLACEMENT_OFFERING_NOT_GRANTED` and `details.pool` and `details.offering`, and names the last modifier in `details.interceptor_ref` when the chain changed the pool. The check runs on every creation kind **[design]**. A grant of pool access to a tenant that may not use the pool's offering succeeds with a `Warning` header naming the offering, so the operator sees the gap before a session meets it.

#### Owned pools choose among granted offerings

An owned pool's offering is a new row in the template copy table of [DR-5](../0147-tenant-derived-runtimes/change.md#dr-5-owned-pools): the template supplies the default, and the tenant may choose any offering its tenant may use whose `classes` include the template's class, through `PUT /v1/admin/derived-runtimes/{name}`. A choice outside that set answers `403 PLACEMENT_OFFERING_NOT_GRANTED`, the same status the code answers at the post-chain point, so each code has one HTTP status **[design]**. The tenant writes the offering's name only, never a selector or a toleration.

#### Reserved overlay

`reservedPlacement` of DR-10 becomes `placement.reservedOverlay`: a `nodePlacement` applied to the pool's reserved pods alone, written on the template by `platform-admin`, copied to owned pools, and never written by a tenant. It is validated as an offering's `nodePlacement` is, so it cannot leave the pool's class or name another tenant's nodes. On a pool with dedicated tenancy, the reserved pods also carry the tenant affinity and toleration ("PM-5"), so they stay on the tenant's own nodes. This replaces DR-10's specific refusals of the T4 selector and the Kata toleration, covers what the T4 refusal protected ([`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md), inventory row I25), and closes the case in which a runc template placed reserved pods on gVisor nodes **[design]**.

#### Validation

The gateway validates every offering, every pool's `placement` block, and every reserved overlay, and `lenny-pool-config-validator` repeats the checks on the rendered `PodSet`, from the same class configuration that the chart passes it **[design]**. A `nodePlacement`:

- may not set a key equal to `placement.classLabelKey`, a key of any class's node selector, or `lenny.dev/tenant`;
- may not tolerate any class taint, any `lenny.dev/tenant` taint, or every taint (a toleration with an empty key and `operator: Exists`);
- may not tolerate a taint key with `operator: Exists` when a class or tenant taint uses that key.

The platform renders the class selector and toleration and the tenant selector and toleration itself ("PM-5"), so no written `nodePlacement` ever needs them.

### PM-4 Placement requirements

#### The settings

| Setting | Meaning | Who sets it |
| :-- | :-- | :-- |
| `requireNodeSeparation: true` | The pod's node class has exactly one member. | Tenant, environment |
| `nodeTenancy: dedicated` | The pod set has dedicated tenancy. | Tenant, environment, and the operator through `securitySettings.placement.nodeTenancy` |
| `allowSharedPod: false` | The pool runs one session per pod: `sessionPolicy.maxConcurrentSessionsPerPod: 1`. | Tenant, environment, and the operator through `securitySettings.placement.allowSharedPod` |
| `allowReusedPod: false` | The pool's pods serve one session and retire: recycling off, or `recycle.maxSessionsPerPod: 1`. | Tenant, environment, and the operator through `securitySettings.placement.allowReusedPod` |
| `dataResidencyRegion` | The pool's `placement.region` equals it ("PM-7"). | Tenant, environment, as today |
| `minIsolationProfile` | The pool's isolation profile is at least this strong. | Session, delegation lease, and the tenant floor, which is `securitySettings.placement.minIsolationProfile` |

A tenant's own requirements live in the tenant security record of SO-2 as a `placement` section (O7), written by the tenant's security team, and an environment's live in a new `placementRequirements` field of the environment record **[design]**. A tenant's `security-admin` may set every requirement of the table that names the tenant, `allowReusedPod: false` included. The operator's settings live in the tenant security settings, which the operator writes and the tenant's roles only read ([`0150-tenant-security-settings`, "TSS-6"](../0150-tenant-security-settings/change.md#tss-6-placement-settings)). `dataResidencyRegion` stays where it is. An environment's requirements add to its tenant's and cannot remove one. Under SO-2, adding a requirement applies at once and removing one is a weakening that takes the record's delay, and the tenant security settings apply the same rule to the operator's relaxations. An environment's `placementRequirements` are environment data that a `tenant-admin` writes through `PUT /v1/admin/environments/{name}`. Adding a requirement there applies at once. Removing one, or moving one to a weaker value, is a relaxation under the rules of the tenant security settings (D18) **[design]**: the write needs a non-empty `justification` in its body, or it is refused with `400 SECURITY_SETTING_JUSTIFICATION_REQUIRED` whose `details.relaxed` names the relaxed requirements; the relaxation is recorded in the tenant's audit chain; and it takes effect after the tenant security record's `weakeningDelaySeconds`, held as a pending version of the environment's `placementRequirements` that the pending-version applier activates at its `effectiveAt` ([`0150-tenant-security-settings`, "TSS-1"](../0150-tenant-security-settings/change.md#order-and-validation)). The tenant's `security-admin` or a `platform-admin` can cancel the pending relaxation through `POST /v1/admin/environments/{name}/placement-requirements/pending/{version}/cancel`. A write that adds some requirements and removes others applies the additions at once and holds the removals. The audit events are `environment.placement_relaxation_requested` (critical), `environment.placement_relaxed` (critical), and `environment.placement_relaxation_cancelled` (warning), each naming the environment and the requirements. A stolen `tenant-admin` credential therefore cannot remove an environment requirement before the tenant's security team can see and cancel the change. A requirement that must hold for every environment of the tenant belongs in the tenant security record.

#### Effective requirements

A session's effective requirements are the union of **[design]**:

- the tenant's requirements;
- its environment's requirements, for the environment that the post-chain point settles under TS-1 and TS-3;
- the operator's placement settings in the tenant's security settings, with `source: operator`: `nodeTenancy: dedicated`, `minIsolationProfile`, `allowSharedPod: false`, and `allowReusedPod: false` ("PM-2", "TSS-6");
- for `kind: delegate`, the parent's effective requirements, so a delegated child never runs on weaker placement than its parent, as isolation monotonicity holds for profiles ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease)) and residency holds across a tree ([§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces));
- `minIsolationProfile` from the session and the delegation lease, and `dataResidencyRegion`, resolved as today.

A derived session is a new root, so its requirements are its own tenant's and environment's, and SD-1's target checks already compare isolation with the source.

#### Where they apply

Requirements narrow pool selection. When the gateway resolves the final pool after the `PreRoute` chain, it considers only pools that meet every effective requirement. At the post-chain point it checks the final pool again, together with the pod set of the chosen revision, because a pod set's offering is fixed at the pool and the check reads both. A pool the request or an interceptor named explicitly is checked and never replaced by another pool **[design]**. The order with the existing checks is:

1. The runtime's isolation floor raises a session that names no profile ([RV-1](../0140-runtime-revisions/change.md#rv-1-revisions), "Isolation floor").
2. Pool access, the offering grant ("PM-3"), and the requirements filter the candidate pools.
3. The post-chain checks of [RT-2](../0141-runtime-routing/change.md#post-chain-point) run on the final row, including this design's row.
4. The claim takes a pod from the chosen pod set, whose placement all its pods share.

#### Errors

| Code | Category | HTTP | When |
| :-- | :-- | :-- | :-- |
| `PLACEMENT_REQUIREMENT_UNMET` | `POLICY` | 422 | No candidate pool meets the effective requirements, or the final pool fails one. `details.requirement` names the first unmet requirement in the order of the settings table, `details.unmet` lists every unmet one, `details.source` names where each came from (`tenant`, `environment`, `operator`, or `parent`), and `details.pool` and `details.interceptor_ref` are included when a pool was chosen. |
| `PLACEMENT_OFFERING_NOT_GRANTED` | `POLICY` | 403 | The final pool's offering is neither a default nor granted to the tenant, or a derived-runtime write chooses an offering outside the tenant's set ("PM-3"). |
| `REGION_CONSTRAINT_VIOLATED` | `POLICY` | 403 | The final pool's region does not equal the session's `dataResidencyRegion`, or a derived-runtime write names a base whose template region differs from the tenant's ("PM-7"). The existing code, now raised on every creation kind. |
| `ROUTE_ISOLATION_UNAVAILABLE` and `ISOLATION_MONOTONICITY_VIOLATED` | `POLICY` | 422 | Unchanged, for `minIsolationProfile` under RT-2 and §8.3. |

A refusal never falls back to another runtime, never downgrades the session, and provisions nothing. A session that meets no pool stays refused until the operator creates a pool that meets it.

#### What `sessionIsolationLevel` reports

The object of [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow) gains these fields, populated from the pool and pod set at creation and unchanged for the session's life **[design]**:

| Field | Type | Description |
| :-- | :-- | :-- |
| `nodeClass` | `string` | The name of the pod's node class. |
| `nodeClassMembers` | `array` of `string` | The members of that class. More than one entry means the class is mixed. |
| `nodeTenancy` | `string` | `shared` or `dedicated`. |
| `sharedPod` | `boolean` | `true` when the pod can serve other sessions at the same time: `maxConcurrentSessionsPerPod` above 1. |
| `perSlotScopes` | `boolean` | `true` when `netagent` applies each session's own egress scope, environment, and credentials to its slot, which holds on a one-session pod and on a pod with several slots whose pool's runtime class answers slot attribution. `false` on a pod with several slots whose pool's status records slot attribution as unavailable, where `netagent` keeps the union over the pod's sessions ([`0127-egress-and-git`, decision 60](../0127-egress-and-git/decisions.md#decisions), settled 2026-11-13). The egress design owns the field, and this table lists it beside `sharedPod` so that one table states what the object reports about the pod. |
| `userNamespaces` | `boolean` | `true` when the pod runs with `hostUsers: false`. |
| `placementOffering` | `string` | The pool's offering. |
| `region` | `string` | The pool's region, absent when the pool has none. |

### PM-5 Rendering

#### Agent pods

The PoolScalingController writes the pool's resolved placement into each `PodSet.spec`, and the WarmPoolController copies it into each pod at creation, under the CRD field ownership of [§4.6.3](../../spec/04_system-components.md#463-crd-field-ownership-and-write-boundaries), as it does for topology spread today. For each pod set **[design]**:

1. **Class.** The class of the pool's isolation profile. Placement reads no versioned field of the revision, so every pod set of a pool has the same class.
2. **Required node affinity.** One term, ANDed with any term already present: the class's node selector, the offering's `nodePlacement.nodeSelector`, `placement.regionLabel In [<region>]` when the pool has a region, and `lenny.dev/tenant In [<tenantId>]` when tenancy is `dedicated`. Reserved pods add the reserved overlay's selector.
3. **Tolerations.** The class taint, the offering's tolerations, `lenny.dev/tenant=<tenantId>:NoSchedule` when tenancy is `dedicated`, and on reserved pods the overlay's tolerations.
4. **`hostUsers: false`** on pods of the `standard` member while the class's `userNamespaces` is `enabled`, and nothing when it is `disabled` (O6).
5. **Priority class.** The agent priority class of the pod's node class, `lenny-agent-<class>`, or `lenny-reserved` on a reserved pod ("PM-8").
6. **Topology spread.** The pool's `topologySpreadConstraints` of [§5.2](../../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), unchanged.
7. **Labels.** `lenny.dev/node-class: <class>` on the pod, immutable under `lenny-label-immutability`, so admission and the node check read the class the pod was built for.

When the chart creates `RuntimeClass` objects (`runtimeClasses.create: true`), it renders each class's node selector and taint toleration into the `scheduling` field of the `RuntimeClass` of each member, including `gvisor-monitored`. This is control 1 of today's §17.2 Kata rule, restated for every class. When the operator manages the objects, preflight checks that their `scheduling` fields do not contradict the class.

#### System components

Helm renders the system class's node selector into every Lenny system workload, the system class's toleration when the class has a taint, the component's priority class ("PM-8"), and any `placement.componentPlacement.<component>` overlay, which is validated to stay within the system class **[design]**. The workloads are:

- the gateway, the Token Service, the WarmPoolController, the PoolScalingController, and `lenny-ops` with the `lenny-backup` Jobs it creates;
- every admission webhook Deployment, including `lenny-pod-security`, `lenny-cosign-verify`, `lenny-registry-digest`, `lenny-tenant-label-immutability`, and `lenny-podset-deletion-guard`, and the `lenny-crd-conversion` webhook;
- PgBouncer and the in-cluster stores: Postgres, Redis with Sentinel, and MinIO;
- the dedicated agent DNS (`lenny-agent-dns`) and the `proxied` DNS resolver, `lenny-proxied-dns`;
- `lenny-egress`, each egress group, `lenny-egress-authz`, `lenny-evidence`, `lenny-evidence-reconcile`, and `lenny-git-mirror`;
- `lenny-outbound-gateway` and `lenny-siem-forwarder`;
- the OpenTelemetry collector when the chart renders it;
- the chart's Jobs and CronJobs: preflight, bootstrap, migrate, CRD validation, deployment config sync, MinIO bucket lifecycle, the Redis cluster setup Job, and the restore test.

An egress group that leaves the cluster from a dedicated node pool uses `componentPlacement` to select that node pool within the system class, as [`0127-egress-and-git`](../0127-egress-and-git/change.md#defaults-replacement-and-augmentation) allows.

#### Named node agents

These Lenny workloads run on agent nodes by design. The admission webhook allows their tolerations by namespace and component label, and nothing else **[design]**:

| Agent | Kind and namespace | Nodes | Tolerations |
| :-- | :-- | :-- | :-- |
| `lenny-activity-collector` (FE-6b) | DaemonSet in `lenny-node-agents` | Nodes of the class that contains `sandboxed`, narrowed by `forensics.activity.nodeSelector` to the nodes whose `runsc` handler names the sink | That class's taint and `lenny.dev/tenant` with `operator: Exists`, so it also runs on dedicated gVisor nodes |
| `lenny-quota-agent` ([PL-10](../0118-agent-pod-layout/change.md#pl-10-project-quotas)) | DaemonSet in `lenny-node-agents`, rendered while `quotaAgent.mode` is `auto` | Every node of every agent class | Each agent class's taint and `lenny.dev/tenant` with `operator: Exists`, so it also runs on dedicated nodes |
| Forensics capture Job (FE-5) | Job in `lenny-forensics` | The target pod's node, by `nodeName` | The taints of that node |

The collector's namespace is new ("PM-9"). The collector mounts a Unix socket from the node, and a `hostPath` volume is outside the Restricted and Baseline Pod Security levels, so its namespace enforces `privileged` and `lenny-pod-security` narrows it to the pod templates of its named node agents, and `lenny-forensics` takes the same form for the capture Job, as [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) states. "Validation owed" in `steps.md` lists the check. `lenny-quota-agent` mounts the kubelet's `pods` directory read-write and holds `CAP_SYS_ADMIN` as UID 0, both outside the Baseline level, so it needs the same namespace; it is not privileged, and [PL-10](../0118-agent-pod-layout/change.md#pl-10-project-quotas) states its container, its rights, its seccomp profile and Landlock restriction, and why its mount reaches every pod's volumes on its node. Its init container `seccomp-install` writes the agent's seccomp profile through a `hostPath` of the kubelet's `seccomp/lenny` directory. Its tolerations put it on dedicated nodes of every tenant, as the collector's put the collector on dedicated gVisor nodes, because a dedicated node's slot volumes need their quotas too.

#### Third-party DaemonSets

CNI, CSI, kube-proxy, logging, and monitoring agents usually tolerate every taint and run on every node. They are the operator's, and Lenny neither renders nor refuses them. The operator documentation lists them as the workloads that share agent nodes outside Lenny's control, recommends that each run with the least privilege its function allows, and states that the node check reports, at `info`, every pod on an agent node that is neither a Lenny agent pod nor a named node agent nor owned by a DaemonSet **[design]**.

#### Deployer interceptors

In-cluster interceptors of [RT-5](../0141-runtime-routing/change.md#rt-5-in-cluster-interceptors) run in `gateway.interceptorNamespaces` and are deployer workloads. The class taints keep them off agent nodes unless they tolerate a class taint. The operator documentation recommends the system class or a node pool of their own.

### PM-6 Enforcement

#### Admission

`lenny-pod-security` gains the placement rules, and `lenny-t4-node-isolation` is removed, because its check was a node label and taint rule that the class and tenancy rules replace **[design]**. The webhook's scope widens from agent namespaces to every namespace the chart renders Lenny workloads into (`lenny-system`, `lenny-outbound`, `lenny-mirror` when used, `lenny-forensics`, and `lenny-node-agents`), on Pod `CREATE`. Each refusal carries a rejection label under §17.2's **Rejection labels.** rule:

| Rule | Rejection label |
| :-- | :-- |
| An agent pod whose required node affinity lacks its class's node selector, or whose class label does not match its `RuntimeClass`'s member. | `POD_PLACEMENT_CLASS_MISMATCH` |
| An agent pod that tolerates a taint of a class other than its own, every taint, or a class taint key with `operator: Exists`. | `POD_PLACEMENT_FOREIGN_TOLERATION` |
| A pod that tolerates `lenny.dev/tenant=<id>` without carrying `lenny.dev/tenant-id: <id>` and requiring `lenny.dev/tenant In [<id>]`, or that tolerates the tenant key with `operator: Exists`, outside the named node agents. | `POD_PLACEMENT_TENANT_MISMATCH` |
| A system pod that tolerates any agent class taint or any tenant taint, outside the named node agents. | `POD_PLACEMENT_SYSTEM_ON_AGENT_NODE` |
| A standard-member pod without `hostUsers: false` while its class's `userNamespaces` is `enabled`. | `POD_PLACEMENT_USERNS_REQUIRED` |
| An agent pod whose `priorityClassName` is neither its class's agent priority class nor, on a reserved pod, `lenny-reserved`. | `POD_PLACEMENT_PRIORITY_MISMATCH` |
| A pod of a dedicated-tenancy pool without the tenant label at creation. | `POD_PLACEMENT_DEDICATED_UNSTAMPED` |
| A pod in `lenny-node-agents` other than the pod template of `lenny-activity-collector` or of `lenny-quota-agent`, or a pod in `lenny-forensics` other than a capture Job's, because both namespaces enforce `privileged` ("PM-9"). A `lenny-quota-agent` pod whose capabilities add anything other than `SYS_ADMIN`, that sets `privileged: true`, `hostPID`, `hostNetwork`, or `hostIPC`, whose agent container's `seccompProfile` is anything other than `type: Localhost` with `localhostProfile: lenny/quota-agent.json`, or that mounts any host path other than the kubelet's `pods` directory in its agent container and the kubelet's `seccomp/lenny` directory in its `seccomp-install` init container, is refused as another template (D22). | `POD_NAMED_AGENT_ONLY` |
| An agent pod that sets `fsGroup`, unless its `runtimeClassName` is the `microvm` profile's class while the chart value `kata.emptyDirMode` is `block-plain`, and the value equals the chart value `kata.volumeGid`, the dedicated volume GID, 29000 by default. The webhook reads these two chart values, which [PL-5](../0118-agent-pod-layout/change.md#pl-5-volumes-size-limits-and-eviction) defines under **Which pods are Kata block-plain.** (PL D8). The rule is the pod layout design's, and this webhook enforces it. | `POD_FSGROUP_NOT_ALLOWED` |

The ValidatingAdmissionPolicy `lenny-quota-agent-node-label`, which the chart renders with the agent, admits an update to a `Node` by the `lenny-quota-agent` ServiceAccount only when the node's name equals the `authentication.kubernetes.io/node-name` claim of the request's pod-bound token, and only when the update changes the label `lenny.dev/project-quota`, the annotation `lenny.dev/quota-agent-status` with a value of at most 1 KiB, or both, and no other field, so the agent cannot relabel or taint a node into another class or tenancy ([PL-10](../0118-agent-pod-layout/change.md#pl-10-project-quotas)) **[design]**. The annotation carries the agent's figures, because the agent serves no port (D23).

The gateway refuses an offering, a pool `placement` block, or a reserved overlay that escapes its class ("PM-3", "Validation"), and a dedicated tenant on a shared pool ("PM-2"). `lenny-pool-config-validator` repeats the class and escape checks on every `PodSet.spec` write. Under the `single-node` preset the class rules render no selector and no taint, and the webhook checks only the user namespace rule and the tenant rules.

The adapter checks the pod's own user namespace at start (O6): on a pod rendered with `hostUsers: false`, it reads `/proc/self/uid_map` before it reports READY and refuses to report READY when UID 0 maps to node UID 0, with the failure reason `user_namespace_missing` that [`0145-session-isolation`](../0145-session-isolation/change.md) names, so a cluster that dropped the field fails the pod visibly. The pod never enters the warm pool, and its failure follows "What happens to a pod or pool that fails". The git mirror already performs this check for itself ([`0127-egress-and-git`](../0127-egress-and-git/change.md#git-mirror-lenny-git-mirror)).

#### Preflight

`lenny-preflight` gains these checks, which `lenny-ctl preflight` also runs **[design]**:

| Check | Validation | Severity |
| :-- | :-- | :-- |
| Class nodes | Each class that a bootstrap pool or the system uses has at least one Ready node matching its node selector. | Fail |
| No double class | No node matches two classes' node selectors. | Fail |
| Class taints | Every node of an agent class carries the class taint. | Fail |
| User namespace prerequisites | Every node of the class containing `standard`: kubelet 1.33 or later, containerd 2.0 or later (or CRI-O 1.25 or later), runc 1.2 or later, a kernel with idmapped tmpfs (6.3 or later), and on Bottlerocket `user.max_user_namespaces` above 0, read from node status and a probe pod. The API server is 1.33 or later when any standard pool exists. Runs while the class's `userNamespaces` is `enabled`. | Fail, or warning under `single-node` |
| User namespaces disabled | The class containing `standard` sets `userNamespaces: disabled`. The message states that runc escapes then land as the container's UID on the node. | Warning |
| Mixed classes | Lists each mixed class with its members. | Warning |
| Dedicated tenants | Each tenant whose security settings set `placement.nodeTenancy: dedicated` and that holds pools has Ready nodes labelled for it in each class its pools use, carrying the tenant taint. | Fail |
| Offerings | Each offering a pool references has Ready nodes matching its class selector, its `nodePlacement`, and the pool's region. | Warning |
| Shared runc across tenants | In production mode, each `standard` pool with shared tenancy that more than one tenant can access. | Warning |
| Region | Each pool region has Ready nodes with `placement.regionLabel` equal to it. | Fail |
| `RuntimeClass` scheduling | Each member's `RuntimeClass` has no `scheduling` field that contradicts its class. | Fail |
| Priority classes | Each rendered priority class, including one agent priority class per agent class, exists with the configured value, and no existing class of the same name has another value. Each class quota selects its class's agent priority class. | Fail |

The T4 node isolation webhook check is removed with the webhook.

#### Continuous check in `lenny-ops`

`lenny-ops` runs the node checks above every `ops.placement.checkIntervalSeconds` (default 300) and adds checks over running pods **[design]**:

- every Lenny agent pod runs on a node of its class label, and on a node labelled for its tenant when its tenancy is `dedicated`;
- no system pod runs on a node of an agent class, outside `single-node`;
- no pod other than a named node agent tolerates a tenant taint and runs on a dedicated node of another tenant;
- every node that carries `lenny.dev/tenant` also carries the label of an agent class, because a dedicated node outside every class schedules no Lenny pod and is not billed as dedicated capacity.

The same pass computes each pool's `diskQuota` from the `lenny.dev/project-quota` labels of its class's nodes, as [PL-10](../0118-agent-pod-layout/change.md#pl-10-project-quotas) defines it, for the `pools` section of posture ([`0142-security-oversight`, "Posture"](../0142-security-oversight/change.md#posture)). It also reads the `lenny.dev/quota-agent-status` annotation of each node of an agent class while `quotaAgent.mode` is `auto`, exports its figures as the `lenny_quota_agent_*` series by node that PL-10 lists, and raises `QuotaAgentUnavailable` (D23).

| Alert | Severity | Condition |
| :-- | :-- | :-- |
| `PlacementClassNodesMissing` | Critical | A class used by a pool or the system has no Ready node. |
| `PlacementNodeClassConflict` | Critical | A node matches two classes, an agent-class node lacks its taint, or a node carries `lenny.dev/tenant` and no agent class label. |
| `PlacementPodOnWrongNode` | Critical | A running Lenny pod violates one of the pod checks, which means label drift or a bypassed webhook. |
| `PlacementUserNamespacePrerequisitesUnmet` | Warning | A standard-class node fails a prerequisite. |
| `PlacementClassMixed` | Warning | Production mode and any mixed class, continuously ("PM-1"). |
| `PlacementUserNamespacesDisabled` | Warning | Production mode and the class containing `standard` sets `userNamespaces: disabled`, continuously (O6). |
| `PlacementDedicatedNodesMissing` | Critical | A dedicated tenant's pool has no Ready node labelled for the tenant in its class. |
| `QuotaAgentUnavailable` | Warning | While `quotaAgent.mode` is `auto`, a Ready node of an agent class has no `lenny.dev/quota-agent-status` annotation, or one whose `writtenAt` is older than three times `quotaAgent.recheckSeconds` ([PL-10](../0118-agent-pod-layout/change.md#pl-10-project-quotas), D23). |

#### What happens to a pod or pool that fails

- **A refused pod.** The WarmPoolController records the refusal on the `PodSet` as the condition `PlacementRefused` with the rejection label, increments `lenny_warmpool_warmup_failure_total{error_type="placement_refused"}`, and retries with its existing backoff, so a refusal cannot loop tightly. The pool reports `PoolWarmingUp` and its warm-up alert fires as for any pool that cannot warm.
- **An unschedulable pod.** A pod whose class, offering, region, or tenant has no free node stays `Pending`. The template gets `PlacementUnschedulable` with the scheduler's reason, and `PlacementClassNodesMissing` or `PlacementDedicatedNodesMissing` fires when no node matches at all.
- **A pod on a drifted node.** Running sessions continue. The WarmPoolController stamps the drain request on idle pods whose node no longer matches their placement, and the next pods schedule on matching nodes. A quarantined pod is never drained, under FE-1's retirement rules.
- **A refused write.** A pool, offering, grant, or tenant write that breaks a rule is refused with the code its section names, and nothing is stored.

### PM-7 Region placement

**Post-chain residency check.** When a session's `dataResidencyRegion` is set, the post-chain point refuses a final pool whose `placement.region` is not equal to it with `403 REGION_CONSTRAINT_VIOLATED`, `details.requiredRegion`, `details.resolvedRegion`, and `details.interceptor_ref` when the chain changed the pool. A pool with no region fails the check. The check runs for `create`, `delegate`, and `derive`, whichever interceptor chose the pool, and emits `DataResidencyViolationAttempt` as [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) states for delegation today. The requirement filter of "PM-4" already removes pools of other regions before the post-chain point, so this check catches a pool the request or an interceptor named explicitly. `REGION_UNAVAILABLE` at create, when no pool exists in the region, is unchanged **[design]**.

**Region as a node selector.** A pool's `placement.region`, written by `platform-admin`, replaces the `region` label of §12.8. The controller renders it as a required node affinity on `placement.regionLabel`, default `topology.kubernetes.io/region`, so a pool's pods run only on nodes of its region. Kubernetes clusters usually span one region, so in most deployments the selector confirms that the pool belongs to this cluster, and preflight fails a pool whose region has no node **[inference]**.

**Owned-pool template copy.** `placement.region` is a template row of DR-5's copy table with no tenant input. A tenant whose `dataResidencyRegion` differs from the template's region cannot create a derived runtime on that base: the write answers `403 REGION_CONSTRAINT_VIOLATED`, the status the code answers at the post-chain point, because every session on the owned pool would fail the residency check.

**Residency across regions (D24).** A data-residency claim covers every place a session's data is stored or processed, and pod placement covers only the nodes. Research on 2026-11-15, recorded in [`evidence/placement-design/residency-and-sandboxes-research.md`](evidence/residency-and-sandboxes-research.md), found the following **[documented]**:

- The managed Kubernetes offerings of AWS, Google Cloud, and Azure bind a cluster's control plane to one region, and their multi-region products manage several clusters. EKS Hybrid Nodes attach on-premises and edge machines only: AWS states that "Running EKS Hybrid Nodes on cloud infrastructure, including AWS Regions ... or in other clouds, is not supported" ([EKS Hybrid Nodes overview](https://docs.aws.amazon.com/eks/latest/userguide/hybrid-nodes-overview.html)). Scaleway Kubernetes Kosmos runs one control plane with nodes in several regions and clouds over a WireGuard overlay ([Kosmos](https://www.scaleway.com/en/docs/kubernetes/reference-content/understanding-differences-kapsule-kosmos.md)), and self-managed tools such as Talos KubeSpan, k0smotron, and Kamaji attach workers in other regions to a control plane in one region.
- etcd is not spread across regions. Its tuning guide requires an election timeout of at least ten times the round-trip time between members ([etcd tuning](https://etcd.io/docs/v3.6/tuning/)), and vendor guidance for clusters that span sites is to deploy one cluster per region ([OpenShift etcd guidance](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/etcd/guidance-for-clusters-that-span-data-centers)).
- A cluster whose workers sit in other regions than its control plane loses changes to a partitioned region's nodes, evicts their pods after the node grace period and the eviction timeout, and cannot restart a pod on a node whose kubelet restarted during the partition.
- Everything stored through the Kubernetes API lives in etcd in the control plane's region: pod specs with their environment values, annotations, and labels, Secrets, ConfigMaps, Events, Leases, and every custom resource, including Lenny's. Encryption at rest changes how the data is stored and leaves it in that region ([encrypting data at rest](https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/)).

Lenny therefore states residency per deployment. A deployment serves one residency region: its Kubernetes control plane, Postgres, Redis, object storage, gateway, Token Service, `lenny-egress`, and agent nodes all run in that region, and its pools set `placement.region` to it. A tenant with a `dataResidencyRegion` is served by the deployment of that region, and the post-chain check and the node selector above confirm that each pool it uses belongs there. A cluster whose workers span regions gives compute locality, the workspace bytes on nodes of the pool's region, and does not support a residency claim, because the session's control-plane records and Lenny's stores stay in the control plane's region. Routing tenants across regional deployments, one entry point in front of several deployments, is outside this design.

### PM-8 System components

#### Priority classes

Placement renders the priority classes below, each cluster-scoped and named from Helm values so that two releases in one cluster can use distinct names **[design]**:

| Value key | Default name | Value | Preemption | Used by |
| :-- | :-- | :-- | :-- | :-- |
| `placement.priorityClasses.systemCritical` | `lenny-system-critical` | 100000 | `PreemptLowerPriority` | Admission webhooks, the gateway, the Token Service, the WarmPoolController, and the PoolScalingController |
| `placement.priorityClasses.system` | `lenny-system` | 10000 | `PreemptLowerPriority` | Every other system component and the chart's Jobs |
| `placement.priorityClasses.agentPrefix` | `lenny-agent-<class>`, one per agent class, such as `lenny-agent-standard`, `lenny-agent-sandboxed`, and `lenny-agent-microvm` under `strict` | 0 | `Never` | The non-reserved agent pods of that class |
| `placement.priorityClasses.reserved` | `lenny-reserved` | 0 | `Never` | Reserved agent pods, rendered only while reservations are on (DR-10) |

Outside `single-node`, system and agent pods never share a node, so the system priority classes order system pods among themselves, and the agent priority classes keep agent pods from preempting each other, which DR-10 requires because a preemption bypasses the eviction API and the drain-readiness checks. Under `single-node`, the values order the kubelet's node-pressure evictions so that agent pods go before system pods. Every value stays below Kubernetes' built-in `system-cluster-critical`.

**Per-class quota.** An agent priority class exists per node class so that a quota can bound one class's pods inside the single agent namespace (O3). When `placement.classes.<class>.quota` is set, the chart renders a `ResourceQuota` in `lenny-agents` with those hard limits and a `scopeSelector` on `PriorityClass In [lenny-agent-<class>]`, so the class's aggregate requests stay within it, as a separate namespace's quota did for Kata pods. A pod over the quota is refused at creation, and the WarmPoolController's warm-up backoff applies. Reserved pods carry `lenny-reserved` and are bounded by DR-10's reserved quota, which selects that class the same way. `lenny-pod-security` refuses an agent pod whose priority class does not match its class ("PM-6"), so a pod cannot move to another class's quota.

#### Component table

| Component | Namespace | Kind | Replicas (default) | PDB | Anti-affinity | Spread | Priority |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| Gateway | `lenny-system` | Deployment with HPA | 2, 3, or 5 by tier (§17.8.2) | `maxUnavailable: 1` | Preferred, hostname, among replicas | Zone, `ScheduleAnyway` | system-critical |
| Token Service | `lenny-system` | Deployment | 2, or 4 at Tier 3 | `minAvailable: 1` | Preferred, hostname, among replicas; required against `lenny-git-mirror` and `lenny-outbound-gateway` | Zone, `ScheduleAnyway` | system-critical |
| WarmPoolController | `lenny-system` | Deployment, leader-elected | 2, or 3 at Tier 3 | `minAvailable: 1` | `--controller-anti-affinity` as §17.8.2 states | Zone, `ScheduleAnyway` | system-critical |
| PoolScalingController | `lenny-system` | Deployment, leader-elected | 2, or 3 at Tier 3 | `minAvailable: 1` | As the WarmPoolController | Zone, `ScheduleAnyway` | system-critical |
| `lenny-ops` | `lenny-system` | Deployment, leader-elected | 1 | `minAvailable: 1`, rendered only when replicas is 2 or more | Preferred, hostname, at 2 or more | Hostname, at 2 or more | system |
| Each admission webhook and `lenny-crd-conversion` | `lenny-system` | Deployment | 2 | `minAvailable: 1` | Preferred, hostname, among replicas | Zone, `ScheduleAnyway` | system-critical |
| PgBouncer | `lenny-system` | Deployment | 2, or 4 at Tier 3 | `minAvailable: 1` | Preferred, hostname, among replicas | Zone, `ScheduleAnyway` | system |
| Postgres, Redis, Sentinel, MinIO (self-managed) | `lenny-system` | StatefulSet or operator resource | As §17.8.2 | As each store's chart | Preferred, hostname | Zone | system |
| `lenny-agent-dns` | `lenny-system` | Deployment | 2, required to be at least 2 | `minAvailable: 1` | Preferred, hostname | Zone, `ScheduleAnyway` | system |
| `lenny-proxied-dns`, the `proxied` DNS resolver, a second CoreDNS beside `lenny-agent-dns` | `lenny-system` | Deployment behind a Service with a fixed ClusterIP | 2 | `minAvailable: 1` | Preferred, hostname | Zone, `ScheduleAnyway` | system |
| `lenny-egress` and each egress group | `lenny-system` | Deployment | 2 each | `minAvailable: 1` | Preferred, hostname, among replicas | Zone, `ScheduleAnyway` | system |
| `lenny-egress-authz` | `lenny-system` | Deployment | 2 | `minAvailable: 1` | Preferred, hostname, among replicas; required against `lenny-git-mirror` and `lenny-outbound-gateway` | Zone, `ScheduleAnyway` | system |
| `lenny-evidence` | `lenny-system` | Deployment | 2 | `minAvailable: 1` | Preferred, hostname, among replicas | Zone, `ScheduleAnyway` | system |
| `lenny-evidence-reconcile` | `lenny-system` | Deployment | 1 | None | None | None | system |
| `lenny-git-mirror` | `lenny-system`, or `lenny-mirror` under EG's Pod Security choice | StatefulSet | 1 | `maxUnavailable: 1`, rendered at 2 or more | Required against the Token Service, `lenny-egress-authz`, and `lenny-outbound-gateway` | Hostname, at 2 or more | system |
| `lenny-outbound-gateway` | `lenny-outbound` | Deployment | 3 | `maxUnavailable: 1` | Required against the Token Service, `lenny-egress-authz`, and `lenny-git-mirror`; preferred, hostname, among replicas | Zone, `ScheduleAnyway` | system |
| `lenny-siem-forwarder` | `lenny-system` | Deployment | 2 | `minAvailable: 1` | Preferred, hostname, among replicas | Zone, `ScheduleAnyway` | system |
| OpenTelemetry collector | `observability.otlpNamespace` | Deployment | As its values | As its values | Preferred, hostname | Zone | system |
| Chart Jobs and CronJobs, `lenny-backup` Jobs | `lenny-system` | Job, CronJob | 1 | None | None | None | system |
| `lenny-activity-collector` | `lenny-node-agents` | DaemonSet | One per selected node | None | None | None | system |
| `lenny-quota-agent` | `lenny-node-agents` | DaemonSet, while `quotaAgent.mode` is `auto` | One per node of every agent class | None | None | None | system |

Required anti-affinity is enforced by the scheduler in both directions: a pod cannot schedule onto a node whose existing pods' required terms exclude it. The chart renders each required pair on both components, so the rule holds when either side is disabled and when an operator reads one component's manifest alone. Under `global.devMode`, every required anti-affinity term of this table renders as preferred, so a single-node cluster schedules **[design]**. This design owns the relaxation for every component, the git mirror and the outbound gateway included, which [`0127-egress-and-git`](../0127-egress-and-git/change.md) and [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md) stated for their own components before this table took their anti-affinity over. Outside `global.devMode` no required term is relaxed.

#### `lenny-ops` replicas and PDB

`lenny-ops` defaults to 1 replica, with leader election for its singleton work, and an operator may scale it for read traffic as §25.4 states. Its PDB, `minAvailable: 1`, is rendered only when `ops.replicas` is 2 or more. A PDB with `minAvailable: 1` over one replica refuses every voluntary eviction, so `kubectl drain` of that node waits until the PDB is removed. §25.4's statement that such a PDB is "effectively a no-op" and that "the kubelet evicts the pod gracefully" during a drain does not hold for the eviction API, which honours the budget **[inference]**. §17.1, §25.4, and §25.16 change to this one statement ("Specification changes").

### PM-9 Namespaces

| Namespace | Holds | Pod Security label | Owner |
| :-- | :-- | :-- | :-- |
| `lenny-system` (`{Release.Namespace}`) | System components ("PM-8") | As today, with EG's `baseline` option for the mirror | This design for placement, §17.2 |
| `lenny-credentials` | Credential Secrets, and no pods | None | §4.9 |
| `lenny-agents` | Every agent pod, of every profile | `warn` and `audit` at `restricted`, with enforcement by the RuntimeClass-aware policies | §17.2 |
| `lenny-outbound` | `lenny-outbound-gateway` | `enforce: restricted` | OC-1 |
| `lenny-mirror` | `lenny-git-mirror`, when the deployer chooses it over `lenny-system` at `baseline` | `baseline` | EG |
| `lenny-forensics` | Capture Jobs | `enforce: privileged`, with `lenny-pod-security` admitting only a capture Job's pod template in the namespace | FE-5 |
| `lenny-node-agents` | `lenny-activity-collector` and `lenny-quota-agent` | `enforce: privileged`, because the collector's node socket and the quota agent's kubelet directory are `hostPath` volumes and the quota agent holds `CAP_SYS_ADMIN`, with `lenny-pod-security` admitting only their pod templates: the collector's, which holds no capability and runs as non-root, and the quota agent's, which holds `CAP_SYS_ADMIN` alone as UID 0, runs under its `Localhost` seccomp profile, and is not privileged | FE-6 and PL-10, placed here |
| `gateway.interceptorNamespaces` | Deployer interceptors | The deployer's | RT-5 |
| `observability.otlpNamespace` | The OpenTelemetry collector | As today | §16.3 |

- **`lenny-agents-kata` is dropped** (O3, settled). `agentNamespaces` defaults to `[lenny-agents]`, and every rule that lists agent namespaces, including the default-deny policies of [§13.2](../../spec/13_security-model.md#132-network-isolation), reads one entry. Quotas apply per node class through the class's agent priority class ("PM-8"), per pool through `maxWarm`, and to reserved pods through DR-10's split quota.
- **`PriorityClass` scope.** A `PriorityClass` is a cluster-scoped object. DR-10's statement that the chart renders `lenny-reserved` "in the `--agent-namespace` alone" becomes: the chart renders the cluster-scoped class `lenny-reserved`, and the reserved quota in the agent namespace selects it through the `PriorityClass` quota scope, as each class quota selects its agent priority class.

### Removals

**`recycle.allowCrossTenantReuse`.** The tenant pin applies on every pool, so a pool that sets the field still never serves a second tenant, which §5.2 itself states **[source]**. There are no deployments to keep compatible, so the field goes with no alias **[design]**. The specification text that goes, in [§5.2](../../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) unless noted:

- the field in the `sessionPolicy` block and in the Kata/microvm scrub variant's block, and in the derived runtime example of [§5.1](../../spec/05_runtime-registry-and-pool-model.md#51-runtime);
- the paragraph beginning "`recycle.allowCrossTenantReuse: true` applies only to the sequential-reuse path", with its pool controller rejection;
- **T4 cross-tenant reuse prohibition**, with its rejection error and the gateway's assignment-time check, which the tenant pin already covers;
- in **Cross-tenant prohibition (concurrent sessions)**, the validation and its error string. The prohibition itself stays, as the statement that concurrent slots never serve two tenants, enforced by the tenant pin;
- the clause of the scrub profile paragraph that rejects `scrubProfile: standard` on such pools;
- the mentions in the tenant pin paragraphs and in the gateway ServiceAccount paragraph of [§4.6.3](../../spec/04_system-components.md#463-crd-field-ownership-and-write-boundaries). Service mode's **Tenant isolation** paragraph, which also names the field, leaves with service mode under [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md);
- the tree's `ValidateCrossTenantReuseTier`, `RequestsCrossTenantReuse`, the CRD field, the OpenAPI field, and the tests that exercise them, as non-specification changes.

**`allowStandardIsolation` stays** as the operator's explicit opt-in to the `standard` profile.

**The T4 placement mechanism.** The `lenny.dev/workspace-tier` node label and taint, the `lenny-t4-node-isolation` webhook with its alert `T4NodeIsolationWebhookUnavailable`, its preflight check, and its `features.compliance` gate, and the pod builder's `applyT4NodeIsolation` are removed (D10). [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) removes `workspaceTier` itself and gives each of its other controls a tenant setting ("TSS-9").

## Worked example

Acme Corp runs a shared Lenny cluster with three node pools: `system` (3 nodes), `sandboxed` (GKE Sandbox nodes), and `standard` (Ubuntu nodes with kernel 6.8 and containerd 2.0). `placement.preset` is `strict`, and the `microvm` class is unused. The operator maps the `sandboxed` class onto the provider's gVisor node pool labels and taint.

1. The operator creates an offering `dedicated` with `classes: [standard, sandboxed]`, `tenancy: dedicated`, and `default: false`, and grants it to tenant `globex`. It sets `placement.nodeTenancy: dedicated` in `globex`'s security settings with `PUT /v1/admin/tenants/globex/security-settings`, which the gateway accepts because `globex` holds no access to a shared pool yet. Applying the `regulated` template would set the same value.
2. It creates a pool `globex-coder` with `isolationProfile: standard`, `allowStandardIsolation: true`, `placement.offering: dedicated`, and `placement.tenantId: globex`, and provisions a `standard` node pool labelled and tainted `lenny.dev/tenant=globex`. The controller renders `hostUsers: false`, the class and tenant affinities, and both tolerations, and stamps `lenny.dev/tenant-id: globex` on each warm pod at creation.
3. A `globex` session on `globex-coder` reports `nodeClass: standard`, `nodeClassMembers: [standard]`, `nodeTenancy: dedicated`, `sharedPod: false`, and `userNamespaces: true`.
4. A `globex` session that requests a shared gVisor pool is refused with `422 PLACEMENT_REQUIREMENT_UNMET` and `details.requirement: nodeTenancy`, `details.source: operator`, because the operator's security setting is an implied requirement.
5. Tenant `initech` sets `requireNodeSeparation: true` in its security record. Its sessions run on any of the cluster's pools, because every class has one member. If the operator later switches to `compact`, its sessions are refused until a pool in a single-member class exists again.

## Limits

- Dedicated tenancy costs a node pool per tenant and class, and the operator provisions it. Lenny reports a missing node pool and does not create one.
- Dedicated tenancy removes shared warm inventory for that tenant: each dedicated tenant's pools warm their own pods.
- A tenant identifier that is not a valid Kubernetes label value cannot be dedicated ("PM-2").
- A required node affinity is not re-evaluated after scheduling. Label drift leaves a pod in place until the node check drains it, and only idle pods are drained.
- `requireNodeSeparation` reads the class membership only. Third-party DaemonSets and named node agents still share agent nodes.
- `compact` with microvm pools needs every agent node to offer hardware virtualization.
- User namespaces need Kubernetes 1.33 on standard-class nodes. A cluster pinned below it runs its standard pools only with `userNamespaces: disabled`, which Lenny reports as a relaxation.
- A class quota bounds the class's non-reserved pods only. Reserved pods count against DR-10's reserved quota.

## Alternatives rejected

- **Per-pair `allowSharing` lists.** A second list must stay consistent with the classes (O1).
- **Per-pool relaxation.** One pool's choice weakens every pool scheduled on the same nodes (O1).
- **Presets alone.** They cannot express an operator's existing node pools (O1).
- **Soft anti-affinity between tenants.** It grows with the number of tenants and guarantees nothing (D6).
- **A namespace per profile.** Namespaces separate API objects rather than kernels, and the tree already uses one agent namespace. The per-class priority class keeps the one property the namespace added, a quota per profile (O3).
- **A `preferred` user namespace mode.** A per-pod choice made from the latest node check would let a class run part of its pods without user namespaces with no operator action. The owner chose an explicit `disabled` setting with a loud warning (O6).
- **T4 node classes.** A class per profile at T4 would keep T4 as a placement concept, which the tenant security settings retire. A class per T4 profile also keeps the tenant mix inside each T4 class, which dedicated tenancy closes (D10).
- **Lenny-managed node pools.** Creating nodes would tie Lenny to each provider's node API and its permissions. The operator's tools already provision node pools.

## Specification changes

The changes go through `change-proposal`. Each staged edit locates its target by heading and quoted text.

- **§17.10 Placement (new).** Node classes, members, presets, the hard rules, mixing and its visibility, the separation matrix, user namespaces on the standard class with the `disabled` setting and its warnings, node tenancy and single-tenant pools, offerings and the pool `placement` block, the reserved overlay, placement requirements, rendering of agent pods, system components, named node agents with `lenny-quota-agent` among them, and third-party DaemonSets, admission with the `lenny-quota-agent-node-label` policy, preflight, and the continuous check, region placement, the priority class scheme with one agent priority class per class and the class quotas, the component table, and the namespace table. The requirements table includes `allowReusedPod`, and names the operator's tenant security settings of §12.10 as a source.
- **§5.2 Pool Configuration and Execution Modes.** The pool `placement` block (`offering`, `tenantId`, `region`, `reservedOverlay`) in the pool fields, with a pointer to §17.10. The removals listed under "Removals". **Topology spread constraints** gains a sentence that the controller also renders the placement affinity of §17.10.
- **§5.3 Isolation Profiles.** A successor pointer: "Node placement of each profile, the separation between profiles and from system components, and the user namespace requirement for `standard` moved to [§17.10](17_deployment-topology.md), which owns node classes, `hostUsers`, and the class taints." The `standard` row and the dev-mode fallback message state runc factually: it shares the host kernel, gives no protection against kernel exploits, and has its blast radius bounded by node classes and user namespaces, and the choice is the operator's. [`0145-session-isolation`](../0145-session-isolation/change.md) owns that text, and the two proposals carry one wording.
- **§6.4 Pod Filesystem Layout.** **T4 dedicated-node requirement** becomes a successor pointer: "Dedicated nodes moved to [§17.10](17_deployment-topology.md) as the generic `nodeTenancy: dedicated`, which owns the `lenny.dev/tenant` node label and taint. The `lenny.dev/workspace-tier` node label and taint and the `lenny-t4-node-isolation` webhook are removed. Which tenants have dedicated tenancy is stated by the tenant security settings of §12.10." The control list in **Data-at-rest protection** loses "one-session-only pod lifecycle" as a T4 control, with the same pointer. [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) carries the rest of §6.4's T4 text, and the two proposals carry one wording.
- **§7.1 Normal Flow.** The `sessionIsolationLevel` fields of "PM-4".
- **§8.3 Delegation Policy and Lease.** A sentence under **Isolation monotonicity** that a delegated child's effective placement requirements include its parent's, with a pointer to §17.10.
- **§12.8 Compliance Interfaces.** **T4 ephemeral workspace isolation** becomes a successor pointer to §17.10 for dedicated nodes and to §12.10 Tenant Security Settings for the T4 link, naming the removed webhook. **Data residency**, item 1, becomes: the gateway checks the final pool's `placement.region` at the post-chain point for every creation kind, and the pool's region is rendered as a node selector (§17.10). The "region label" wording goes.
- **§12.9 Data Classification.** The top class's column of **Controls driven by classification tier** (T4, renamed Secret) loses any placement control, with a pointer to §12.10.
- **§15.1 REST API.** The offering endpoints, `GET /v1/admin/tenants/{id}/placement-offerings`, the conflict rules on the security settings writes that set `placement.nodeTenancy: dedicated` (whose routes §12.10's proposal adds), the environment record's `placementRequirements` with the relaxation rule of D18 and the route that cancels a pending relaxation, and the error codes `PLACEMENT_REQUIREMENT_UNMET` and `PLACEMENT_OFFERING_NOT_GRANTED`. `REGION_CONSTRAINT_VIOLATED` gains `details.interceptor_ref` and applies to every creation kind.
- **§16.5 and §16.7.** The placement alerts and audit events. `T4NodeIsolationWebhookUnavailable` is removed.
- **§17.1 Kubernetes Resources.** A successor pointer: "Replica defaults, disruption budgets, anti-affinity, spread, and priority classes of every component moved to the component table of [§17.10](#1710-placement)." The `lenny-ops` row states 1 replica and a PDB at 2 or more. **Agent Pods** names the class's agent priority class and the class affinity.
- **§17.2 Namespace Layout.** The namespace block lists `lenny-system`, `lenny-credentials`, and `lenny-agents` (O3) with pointers to §17.10's namespace table. **Node isolation** becomes a successor pointer: "Node isolation moved to §17.10, which owns node classes and their taints. The Kata label `lenny.dev/node-pool: kata` and the taint `lenny.dev/isolation=kata:NoSchedule` are replaced by the class label and taint." Item 10 (`lenny-t4-node-isolation`) is removed from the admission catalogue, and `features.compliance` gates `lenny-data-residency-validator` alone. `lenny-pod-security`'s widened scope and the new rejection labels join **Rejection labels.**, and the namespace block states `enforce: privileged` for `lenny-forensics` and `lenny-node-agents` with the webhook's narrowing of each to its named node agents **Resource governance** names one agent namespace with a quota per node class through the class's priority class.
- **§17.4 Local Development Mode.** The k3s pin moves to a 1.33 or later release, and Embedded Mode uses the `single-node` preset.
- **§17.6 Packaging and Installation.** The preflight checks of "PM-6", the 1.33 check on standard-class nodes, and the removal of the T4 webhook check.
- **§17.8 Capacity Planning and Defaults.** A successor pointer from §17.8.2's component rows to §17.10's component table for replicas, disruption budgets, and anti-affinity, which keeps the per-tier values.
- **§17.9.1 Composition Dimensions.** The isolation profile dimension drops "T4 webhook requirements".
- **§25.4 and §25.16.** The `lenny-ops` PDB statement of "PM-8".
- **§13.2 Network Isolation.** The default-deny agent namespace list follows O3, and `lenny-node-agents` gains its policy rows through FE.
- **§18 Build Sequence.** The compliance webhook item drops `lenny-t4-node-isolation`.

## Non-specification changes

- **Helm.** The `placement` values and their schema in `values.schema.json`, the generated presets with their hard-rule failures, `lenny-placement-config`, the class selector, toleration, and priority class on every system workload, the `RuntimeClass` `scheduling` fields, the priority classes with one agent priority class per class, the class quotas, `componentPlacement`, the PDBs, anti-affinity, and spread of "PM-8", `agentNamespaces: [lenny-agents]`, `lenny-node-agents` with the `lenny-quota-agent` DaemonSet, its tolerations, and its ValidatingAdmissionPolicy `lenny-quota-agent-node-label`, which [PL-10](../0118-agent-pod-layout/change.md#pl-10-project-quotas) owns with its other chart values, and removal of `t4-node-isolation-webhook.yaml`.
- **Controller.** Placement resolution in the PoolScalingController onto `PodSet.spec`, rendering in the WarmPoolController and the pod builder, `hostUsers` from the class's `userNamespaces` setting, the class's agent priority class, the class label, the `PlacementRefused` and `PlacementUnschedulable` conditions, the drift drain, the stamp at creation on `placement.tenantId` pools, and removal of `t4_node_isolation.go` and `kata_node_isolation.go`, which the class rendering replaces.
- **Webhooks.** The placement rules in `lenny-pod-security` with its widened scope, the priority class rule, and `POD_NAMED_AGENT_ONLY` admitting the collector's and the quota agent's pod templates, the class and escape checks in `lenny-pool-config-validator`, the label webhook's controller rule for `placement.tenantId` pools and the immutable `lenny.dev/node-class`, and removal of `pkg/admission/t4_node_isolation`.
- **Adapter.** The `uid_map` check before READY on every pod rendered with `hostUsers: false`.
- **Preflight.** The checks of "PM-6".
- **`lenny-ops`.** The continuous check, its alerts, the warning logged when user namespaces are disabled, each pool's `diskQuota` for posture, the quota agent's status annotation read into the `lenny_quota_agent_*` series with `QuotaAgentUnavailable` (D23), and the PDB rule.
- **Admin API and gateway.** The offering resource and its tables (`placement_offerings`, `placement_offering_tenant_access`), the pool `placement` block, the conflict rules on the security settings writes for `placement.nodeTenancy`, `placementRequirements` on the environment with its pending versions, the relaxation rule, the cancel route, and the applier (D18), the requirement filter in pool resolution with `allowReusedPod`, the post-chain row, the grant and admission rules, the `sessionIsolationLevel` fields, the audit events, the OpenAPI document, and `lenny-ctl` commands for offerings.
- **Removals.** `allowCrossTenantReuse` across the gateway, CRDs, OpenAPI, and tests.
- **Documentation.** A placement chapter in the operator guide, whose content "Documentation" lists, the runc wording in `security-principles.md`, `runtime-configuration.md`, `why-lenny.md`, `index.md`, and `namespace-and-isolation.md` under SI's text, and the removal of `allowCrossTenantReuse` from `configuration.md`, `execution-modes.md`, and `multi-tenancy.md`.

## Documentation

The operator guide's placement chapter owns what this design adds, and `namespace-and-isolation.md`, `security-principles.md`, and the reference pages link to it. Pages carry no section numbers (`doc-content.md`). Each item below is stated in the chapter.

| Topic | What the chapter states |
| :-- | :-- |
| Classes and presets | The `placement` values with their defaults (`preset: strict`, `classLabelKey: lenny.dev/node-class`, `regionLabel: topology.kubernetes.io/region`, each class's `nodeSelector`, `taint`, `tenancy: shared`, `userNamespaces: enabled`, and `quota: null`), the three presets with their memberships, `acknowledgeMixedClass`, the hard rules, the separation matrix, and how to map a class onto a provider's existing node pool labels and taints. |
| Node requirements | Per class: the labels and taints each node carries; on the class that holds `standard`, Kubernetes 1.33 or later on the API server and nodes, containerd 2.0 or later or CRI-O 1.25 or later, runc 1.2 or later, a kernel with idmapped tmpfs (6.3 or later), and `user.max_user_namespaces` above 0 on Bottlerocket; Kata nodes with hardware virtualization; and the node configuration the checkpoint design asks for on each class ([IC decision D13](../0131-incremental-checkpoint/decisions.md#decisions)): the gVisor annotation pass-through, `emptydir_mode = "block-plain"` with the dedicated volume GID on Kata, and the inotify limits on runc nodes. |
| User namespaces | That runc shares the host kernel and gives no protection against kernel exploits, that node classes and user namespaces bound its blast radius, that the profile choice is the operator's through `allowStandardIsolation`, what `userNamespaces: disabled` changes, and every place it is reported. |
| Tenancy and offerings | Shared and dedicated tenancy, single-tenant pools and `placement.tenantId`, the per-tenant setting in the tenant security settings, offerings with `placement.defaultPoolOffering` (`shared`), grants, the reserved overlay, the `nodePlacement` validation rules, and that dedicated capacity is billed as `node.dedicated_usage`. |
| Requirements | Each requirement, who may set it, that a tenant's own removal takes the security record's delay and that an environment's removal is a relaxation that needs a `justification` and takes the same delay, with its cancel route, its audit events, and `SECURITY_SETTING_JUSTIFICATION_REQUIRED`, the order of checks at the post-chain point, and the `sessionIsolationLevel` fields. |
| Region | `placement.region` as a node selector, the post-chain residency check on every creation kind, and the owned-pool template rule. That a residency claim needs one deployment per region with every component listed under PM-7 **Residency across regions.** in that region; that a cluster with workers in several regions gives compute locality only, and why (control-plane records in etcd, partition behaviour); and that the operator assigns each tenant with a `dataResidencyRegion` to the deployment of its region. |
| System components | The component table with replicas, disruption budgets, anti-affinity, spread, and priority classes, the `global.devMode` relaxation of required anti-affinity, `componentPlacement`, the priority class scheme with one agent priority class per class, the class quotas, and the `lenny-ops` PDB rule. |
| Namespaces | The namespace table, with `enforce: privileged` on `lenny-forensics` and `lenny-node-agents` and the webhook's narrowing of each, the named node agents with their nodes and tolerations, `lenny-quota-agent`'s `CAP_SYS_ADMIN` and mount of the kubelet's `pods` directory, the admission rule that requires its `Localhost` seccomp profile, and the `lenny-quota-agent-node-label` policy, which admits its own node's label and status annotation alone. |
| Third-party DaemonSets and interceptors | That they share agent nodes outside Lenny's control, the least-privilege recommendation, and the node check's `info` report. |
| Failures and limits | Every rejection label of "PM-6", every preflight check with its severity, `ops.placement.checkIntervalSeconds` (300), every alert with its runbook, the error codes `PLACEMENT_REQUIREMENT_UNMET`, `PLACEMENT_OFFERING_NOT_GRANTED`, `REGION_CONSTRAINT_VIOLATED`, `INVALID_POOL_CONFIGURATION` with its rules, and `CONFIGURATION_CONFLICT`, the `PodSet` conditions `PlacementRefused` and `PlacementUnschedulable`, the adapter's `user_namespace_missing` failure, and each item under "Limits". |

## Tests

| Tier | Cases |
| :-- | :-- |
| 0 | Chart render under each preset and under `custom`: every system workload carries the system selector and its priority class, `lenny-forensics` and `lenny-node-agents` enforce `privileged`, `lenny-quota-agent` renders on every agent class with each class's taint and the tenant taint and renders nothing under `quotaAgent.mode: off`, every required anti-affinity term renders as preferred under `global.devMode` and as required otherwise, each agent class renders its agent priority class and, when set, a quota scoped to it, `userNamespaces: disabled` renders no `hostUsers`, no system workload tolerates an agent class or tenant taint, every agent class has a taint, a mixed class without its acknowledgment fails, `single-node` without `global.devMode` fails, a class mixing `system` with an agent member fails outside `single-node`, required anti-affinity appears on both sides of each pair, and no template references `allowCrossTenantReuse` or the T4 webhook. |
| 1 | Class resolution from a profile, effective tenancy, `nodePlacement` validation against each escape, affinity and toleration merge with existing terms, the effective requirement union with the operator's security settings, the first unmet requirement, `allowReusedPod` against recycling and `recycle.maxSessionsPerPod`, `hostUsers` rendered under `enabled` and absent under `disabled`, and the agent priority class per class. |
| 2 | The offering API against Postgres: create, update with dependent pools, delete with dependents, grant, revoke with an owned pool, and tenant visibility. An environment write that adds a requirement applies at once; one that removes or weakens a requirement without a `justification` is refused with `SECURITY_SETTING_JUSTIFICATION_REQUIRED`, and with one is held until `weakeningDelaySeconds` passes, mirrored to the tenant's chain, and cancelled by the tenant's `security-admin`; a mixed write applies its additions at once (D18). Pool admission rules, the grant rules, the refusal of `placement.nodeTenancy: dedicated` on a security settings write and a template apply, with its dry run, and owned-pool offering choice. |
| 3 | Post-chain codes for every creation kind: `PLACEMENT_REQUIREMENT_UNMET` with each requirement and source, `PLACEMENT_OFFERING_NOT_GRANTED`, and `REGION_CONSTRAINT_VIOLATED` with `details.interceptor_ref` when a `PreRoute` interceptor moved the session. |
| 4 | Session creation, delegation, and derive on the compose stack with requirements from tenant, environment, operator, and parent, and the `sessionIsolationLevel` fields. |
| 5 | A multi-node Kind cluster with labelled and tainted nodes per class and a dedicated tenant's nodes: pods land on their class, a node with a tenant label and no class label raises `PlacementNodeClassConflict`, a Kata block-plain pod with the dedicated GID is admitted, admission refuses each rule with its rejection label, a removed node label triggers the alert and the drain of idle pods, user namespaces on a 1.33 or later Kind node with the `uid_map` check, the adapter refusing READY when the API server drops `hostUsers`, a class quota refusing a pod over its limit, and the `single-node` preset on one node. |
| 6 | EKS, GKE, and AKS with real node pools, as "Validation owed" in `steps.md` lists. |
| 8 | Node loss in a dedicated class, label drift during a session, and a webhook outage with fail-closed refusal. |
| 9 | A pod spec that tolerates every taint, another class's taint, or another tenant's taint, or names another class's priority class; an agent pod with `fsGroup` outside a Kata block-plain pool or with another GID; a pod template in `lenny-node-agents` other than the collector's and the quota agent's, a quota agent pod with another capability, `privileged: true`, another host path, or an agent container without the `Localhost` profile `lenny/quota-agent.json`, and a quota agent write to another node, to another label, to an annotation other than `lenny.dev/quota-agent-status`, to that annotation with a value over 1 KiB, or to a taint; a non-Job pod in `lenny-forensics`; a system pod with an agent toleration; an offering, overlay, or `componentPlacement` that names a class or tenant key; a tenant write that tries to set a selector; and a forged `lenny.dev/node-class` label. |
| 11 | The operator guide's separation matrix and preset table match the chart's generated classes. |

## Instructions for reviewers of other designs

- [`0145-session-isolation`](../0145-session-isolation/change.md): replace SIR-17 and O8 with a pointer to "PM-1" for user namespaces on the standard class: on by default, and `disabled` only by an explicit operator setting with a loud warning, with no `preferred` mode (O6). Where SI states that runc pools "require" user namespaces, state the default and the operator's `disabled` setting. Change SI-1's **Minimum Kubernetes version** paragraph to state the 1.33 floor for standard-class nodes with user namespaces enabled, with a pointer to "PM-1". Cut SI-11's user namespace part to the facts SI needs and a pointer. Keep the runc wording SI owns, matched to "PM-1".
- [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md): add to PL-1's **Pod-wide settings** the placement fields that "PM-5" renders (`hostUsers`, the class and tenant affinity, tolerations, the priority class, and the `lenny.dev/node-class` label), with a pointer. Add to `sessionIsolationLevel` only through "PM-4", which defines `sharedPod`. The priority class is the agent priority class of the pod's node class ("PM-8"). PLR-10 needs the Kata block-plain `fsGroup` exception of 2026-11-12, which `lenny-pod-security` admits under `POD_FSGROUP_NOT_ALLOWED` ("PM-6"). Name the pool field or chart value that marks a Kata pool block-plain and the Helm value that holds the dedicated volume GID, so the webhook reads both. Its revision 4 names them as the chart values `kata.emptyDirMode` and `kata.volumeGid`, which PM-6's `fsGroup` row reads.
- [`0127-egress-and-git`](../0127-egress-and-git/change.md): in "Network policy and placement", replace "The specification defines no system node pool. The proposal adds a placement rule that keeps these components off nodes tainted for agent isolation" with a pointer to "PM-1" and "PM-8". In the DaemonSet rejection list, name the class taint in place of the Kata taint. Keep the mirror's anti-affinity rationale, `gitMirror.hostUsers`, the `lenny-mirror` namespace, and its Pod Security level, and point to "PM-8" for replicas, PDB, and the two-sided rendering. The `proxied` DNS resolver is `lenny-proxied-dns`, a second CoreDNS beside `lenny-agent-dns`, as "PM-8" now lists it. Egress groups on a dedicated node pool use `componentPlacement`. The `global.devMode` relaxation of the mirror's required anti-affinity is stated in "PM-8" for every component, so this design's own statement of it becomes a pointer.
- [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md) (applied in its revision 29): in "Deployment and network rules", replace "the placement rule that keeps it off nodes tainted for agent isolation" with a pointer to "PM-8", and in "High availability" point to "PM-8" for replicas, PDB, and anti-affinity. The namespace stays with OC. "PM-8" owns the `global.devMode` relaxation of the outbound gateway's required anti-affinity, so the relaxation is intended to leave OC's text.
- [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md): FE-5's capture Jobs and FE-6's collector are named node agents ("PM-5"), and the collector runs in `lenny-node-agents`. "PM-9" now states `enforce: privileged` on both namespaces with the webhook's narrowing, as that design asked. FE-6's "§5.3 restricts `runc` to development and testing" becomes the factual runc statement. FE-1's retirement table gains the node check's drift drain, which skips a quarantined pod ("PM-6").
- [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md): DR-10's `reservedPlacement` becomes `placement.reservedOverlay` with a pointer to "PM-3", and its specific refusals of the T4 selector and the Kata toleration go. DR-10's `PriorityClass` sentence takes the scope correction of "PM-9", and the class joins the scheme of "PM-8", beside one agent priority class per node class. A reserved pod of a pool with dedicated tenancy keeps the tenant affinity and toleration under the overlay ("PM-3"). DR-5's template copy table gains `placement.offering` (template default, tenant choice among granted offerings in the template's class) and `placement.region` (template, no tenant input). A derived-runtime write that chooses an offering outside the tenant's set answers `403 PLACEMENT_OFFERING_NOT_GRANTED`, and one on a base whose template region differs from the tenant's `dataResidencyRegion` answers `403 REGION_CONSTRAINT_VIOLATED`, the statuses those codes answer at the post-chain point, in that design's "Fields" table, its "Their answers." list, and its tests. The namespace reason for leaving `microvm` out of reservations goes, because O3 dropped `lenny-agents-kata`. Posture's `placementConfigured` reads the overlay.
- [`0141-runtime-routing`](../0141-runtime-routing/change.md): the post-chain table gains a row owned by this design: the offering grant, the effective placement requirements, and the residency check, with their codes ("PM-3", "PM-4", "PM-7").
- [`0140-runtime-revisions`](../0140-runtime-revisions/change.md): the `workspaceTier` rows of "Field classes" leave, as [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) asks, together with the pod builder's T4 node isolation. Placement reads no versioned field, so pod sets of one pool share their placement.
- [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md): the environment record gains `placementRequirements`, which only add to the tenant's, and TS-1's post-chain list names them. A child that crosses into T under TS-3 takes T's requirements together with its parent's. A `tenant-admin`'s removal of an environment requirement is a relaxation under the rules of the tenant security settings: it needs a `justification` and takes the tenant security record's weakening delay, cancellable by the tenant's `security-admin` (D18, "PM-4").
- [`0144-session-derive`](../0144-session-derive/change.md): SD-1's target checks include the effective requirements and the residency check at the post-chain point.
- [`0126-delegation-availability`](../0126-delegation-availability/change.md): no change beyond the post-chain row, which covers delegated children.
- [`0142-security-oversight`](../0142-security-oversight/change.md): under O7, the SO-2 record gains a `placement` section with `requireNodeSeparation`, `nodeTenancy`, `allowSharedPod`, and `allowReusedPod`, whose removal is a weakening. Posture's `pools` section gains `nodeClass`, `nodeClassMembers`, `nodeTenancy`, `sharedPod`, `userNamespaces`, `placementOffering`, and `region`. SO-3's `lenny-siem-forwarder` runs in `lenny-system` ("PM-8"). SO-4a mirrors offering grants and revocations.
- [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md) (applied in its revision 13): BR-7 carries dedicated capacity as `node.dedicated_usage` (D17), and a node with a tenant label outside every agent class is reported by "PM-6" rather than billed.
- [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md): under D18 a relaxation of an environment's `placementRequirements` follows TSSR-4 with the tenant security record's weakening delay, and reuses `SECURITY_SETTING_JUSTIFICATION_REQUIRED`. Name environment placement requirements among the relaxations that rule governs. Earlier, this design applied its instructions to this design: `securitySettings.placement.nodeTenancy` replaces the tenant-record `nodeTenancy`, with PM-2's admission and grant rules on the settings write and the template apply; `placement.minIsolationProfile` is the tenant floor; `placement.allowSharedPod` and `placement.allowReusedPod` are operator sources of their requirements; and the reserved overlay keeps a dedicated pool's reserved pods on the tenant's nodes (I25). The tenant column for `nodeTenancy` that TSS-9 declines to add is no longer proposed here.

## Sources

This design read no external source of its own. Its external facts are those [`0145-session-isolation`, "Sources"](../0145-session-isolation/change.md#sources) and [`0127-egress-and-git`](../0127-egress-and-git/change.md) list, in particular the Kubernetes pages on user namespaces, and the spike reports of 2026-11-12 that the session-isolation and pod-layout designs record. The decision brief of 2026-11-12 and its placement inventory were read from the session scratchpad.

Part 2 of the research report under [`evidence/placement-design/`](evidence/README.md) describes `kubernetes-sigs/agent-sandbox` among the sandbox runtimes. This design draws no rule from that part. [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) owns Lenny's pod lifecycle resource kinds, for which Lenny runs its own controllers in the `lenny.dev` group. The kinds this design names are `PodSet`, which records one pod set of a pool, and `AgentPod`, which records one agent pod (["PLC-2"](../0137-pod-lifecycle-controller/change.md#plc-2-kind-and-field-names)). The `PodSet` conditions `PlacementRefused` and `PlacementUnschedulable` of "PM-6" are Lenny's own. The pod lifecycle design adds no `PodScheduled` condition to the `AgentPod`, because `PlacementUnschedulable` carries the scheduler's reason for the pool and the pending pod's own `PodScheduled` condition carries it for the pod.
