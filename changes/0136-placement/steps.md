# Steps: 0136 Placement

## Validation owed

- Real node pools on EKS, GKE, and AKS under `strict` and `compact`, with classes mapped onto provider labels and taints, including GKE Sandbox's own gVisor node pool label and taint and AKS's Kata node pool, and which provider labels are stable enough to name in a class.
- Taints and node selectors with each `RuntimeClass`: `RuntimeClass` `scheduling` merged with the pod's own affinity and tolerations, for gVisor, `gvisor-monitored`, and Kata on each provider.
- User namespace prerequisites per node image: EKS AL2023 and Bottlerocket, GKE Container-Optimized OS and Ubuntu, and AKS Azure Linux and Ubuntu, with every volume type a Lenny pod mounts, and `hostUsers: false` on k3s 1.33 or later in rootless and rootful mode for `lenny up`.
- DaemonSet tolerations of common CNI, CSI, kube-proxy, and logging agents (aws-node, ebs-csi, Calico, Cilium, and Fluent Bit), and what each can reach on an agent node.
- Cluster autoscaler and Karpenter scale-up for a pending pod that needs a class taint and a tenant taint at once.
- The Pod Security level that `lenny-activity-collector`'s node socket needs in `lenny-node-agents`, and `lenny-quota-agent`'s `CAP_SYS_ADMIN` and read-write mount there, with its `Localhost` seccomp profile and the `hostPath` of `seccomp-install`.
- The `authentication.kubernetes.io/node-name` claim of a pod-bound ServiceAccount token, which `lenny-quota-agent-node-label` reads, on each supported Kubernetes version and on EKS, GKE, and AKS, and that the API server serves ValidatingAdmissionPolicy there. 0136.S7 and [0118.S6](../0118-agent-pod-layout/steps.md#steps) wait on it.
- The node check's cost on a cluster with thousands of nodes and pods.

## Steps

### 0136.S1 Removal of allowCrossTenantReuse
- status: Planned
- aliases: [PM-A]
- lanes: [specification, code, documentation]
- closes: [T-5.2.6]

Removal of `allowCrossTenantReuse`, as "Removals" in `change.md` lists it. The code sites are `ValidateCrossTenantReuseTier` and `RequestsCrossTenantReuse` (`pkg/gateway/api/admin/pools.go`, `pkg/gateway/api/admin/runtimes.go`, and `pkg/sandbox/isolation/profile.go`), the pool store (`pkg/runtime/poolstore`) and the runtime store (`pkg/runtime/runtimestore`), the `PodSet` field (`pkg/apis/lenny/v1alpha1/podset_types.go` and the generated CRDs under `charts/lenny/crds` and `pkg/embedded/crds`), the OpenAPI document (`pkg/api/openapi/openapi.json`), the generated ops MCP tools (`pkg/ops/mcp/generated_tools.go`), the bootstrap resources (`pkg/gateway/api/admin/bootstrap_resources.go`), the session resolver and the pod claimer (`pkg/gateway/core/session/podsession/resolve.go` and `pkg/gateway/core/podclaim/claimer.go`), the `PoolPolicyMirror` (`pkg/gateway/core/session/sessionserver/pool_policy_mirror.go`), and the `sessionIsolationLevel.scrubPolicy` derivation in `pkg/gateway/core/session/sessionserver/isolationlevel.go`, which reads the field today to report `vm-restart` and `best-effort-in-place` and reads the pool's `scrubProfile` after this step, as §7.1 states. A migration drops the column `sandbox_warm_pools.allow_cross_tenant_reuse` that `migrations/0040_warm_pool_concurrency.up.sql` added. The tests that exercise the field change with it, among them `tests/security/pool_admission_isolation_test.go`, `tests/security/concurrent_slot_isolation_test.go`, `tests/component/stores/poolstore_test.go`, and `tests/component/migrations/prod_columns_test.go`. [0145.S6](../0145-session-isolation/steps.md#steps) depends on this step, because 0145 removes `in-place` scrub (its D39) and its instruction to this change states that the two scrub-profile edits land together. The removal of the T4 cross-tenant reuse prohibition and its assignment-time check makes T-5.2.6 moot. The step leaves a successor pointer to **Tenant pinning** in place of that prohibition in §5.2, and [0150.S3](../0150-tenant-security-settings/steps.md#steps) adds §12.10 to the pointer.

### 0136.S2 Components, priority classes, and namespaces
- status: Planned
- aliases: [PM-B, PM-8, PM-9]
- lanes: [specification, code, documentation]

The component table with a shared chart helper that renders a component's row of "PM-8", the system priority classes, the value keys of the priority class scheme, `placement.priorityClasses.reserved` among them, which [0147.S9](../0147-tenant-derived-runtimes/steps.md#steps) reads, the `lenny-ops` settlement, and the namespace table for the namespaces that exist when it lands. It renders the rows of the components the tree has. A component the tree does not have yet, such as `lenny-egress`, `lenny-egress-authz`, `lenny-evidence`, `lenny-git-mirror`, `lenny-outbound-gateway`, `lenny-siem-forwarder`, or `lenny-proxied-dns`, gets its row rendered through the helper by the step of its own change that adds it. For `lenny-outbound-gateway`, whichever of this step and [0148.S1](../0148-tenant-outbound-connector/steps.md#steps) lands second renders the outbound gateway's rows, and [0148.S4](../0148-tenant-outbound-connector/steps.md#steps) renders the Deployments of OC-4's exclusive gateway groups under the rule that "PM-8" states for them. The agent priority class per node class, the class quotas, and the drop of `lenny-agents-kata` under O3 land in 0136.S3, because the classes arrive there.

### 0136.S3 Classes, presets, and rendering
- status: Planned
- aliases: [PM-C, PM-1, PM-5 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0136.S2]
- closes: [T-5.3.9 (part)]

Classes, presets, `lenny-placement-config` with `platform.placement_config_changed`, the agent priority class per class with the class quotas, the drop of `lenny-agents-kata` (O3), rendering of agent and system pods with the `RuntimeClass` `scheduling` fields, `hostUsers` on the standard class, and the replacement of the Kata node controls (`kata_node_isolation.go`) by the class rendering. It depends on 0136.S2 for the priority class scheme, and on SI's userns decision. The adapter's `uid_map` check is [0145.S5](../0145-session-isolation/steps.md#steps)'s. It closes the part of T-5.3.9 that the `e2e_kind` case of pods landing on the nodes of their class covers; a sandboxed pool on a gVisor node stays with the `e2e_cloud` tier.

### 0136.S4 Node tenancy and single-tenant pools
- status: Planned
- aliases: [PM-D, PM-2]
- lanes: [specification, code, documentation]
- depends-on: [0136.S8, 0147.S5, 0150.S2]

Node tenancy and single-tenant pools. It depends on DR-5 (0147.S5) for owned pools and the `lenny.dev/served` reader, and on TSS-B (0150.S2), which stores `securitySettings.placement.nodeTenancy`. Between 0150.S2 and this step a tenant's `placement.nodeTenancy: dedicated` is stored without PM-2's conflict check, so this step checks every tenant's stored value under PM-2 when it lands: the preflight check "Dedicated tenants" fails for each tenant whose stored settings set `dedicated` while it holds access to a pool with shared tenancy or its identifier is not a valid label value.

### 0136.S5 Offerings and the pool placement block
- status: Planned
- aliases: [PM-E, PM-3]
- lanes: [specification, code, documentation]
- depends-on: [0136.S4, 0141.S1, 0147.S9, 0142.S2, 0142.S4]

Offerings, the admin API, the pool `placement` block, the routing check, owned-pool choice, and the reserved overlay. It applies `placement.reservedOverlay` to the reserved pods that 0147.S9 marks, because 0147.S9 renders no node placement for them, and it carries the overlay's tests that left 0147's `component` tier: a marked pod carries the template's overlay, and an overlay that names another class's taint or a `lenny.dev/tenant` key is refused at the template write. O11 records whether the field lands here or in an earlier step. It depends on RT-2's post-chain point, which 0141.S1 lands, and on DR-10 (0147.S9). It also adds the offering grant mirror of SO-4a (0142.S4) and the posture's `placementOffering` and `placementConfigured` fields of SO-2 (0142.S2).

### 0136.S6 Placement requirements and region placement
- status: Planned
- aliases: [PM-F, PM-4, PM-7]
- lanes: [specification, code, documentation]
- depends-on: [0136.S5, 0141.S1, 0149.S1, 0142.S2]
- closes: [T-12.8.7 (part)]

Placement requirements with `allowReusedPod`, `sessionIsolationLevel` fields, and region placement. It depends on RT-2 (0141.S1), on TS-1 (0149.S1), and on SO-2 (0142.S2) for the record, its write path, `weakeningDelaySeconds`, and the applier that activates an environment's pending relaxation from `environment_placement_versions`. Under O7 it adds the record's `placement` section and the posture's placement fields. 0150.S3 (TSS-C) lands the operator's placement settings as sources once 0136.S4 and 0136.S6 are in. Step 1 of PM-4's order, the runtime's isolation floor, and the check of the chosen revision's pod set read [RV-1 and RV-3](../0140-runtime-revisions/change.md#rv-1-revisions); of this step and the 0140 step that lands them, whichever lands second adds that case. It closes the part of T-12.8.7 that the post-chain residency check covers: `REGION_CONSTRAINT_VIOLATED` on every creation kind, delegation included, at the `contract` and `integration` tiers. Per-region storage routing is outside it and depends on O10.

### 0136.S7 Admission and named node agents
- status: Planned
- aliases: [PM-5 (part), PM-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0136.S3]
- closes: [T-12.9.11]

The placement rules of `lenny-pod-security` with its widened scope, the class and escape checks of `lenny-pool-config-validator`, the immutable `lenny.dev/node-class` label, the `lenny-node-agents` and `lenny-forensics` namespaces with `POD_NAMED_AGENT_ONLY`, the named node agents' tolerations, and the removal of the T4 placement mechanism (the webhook, its alert, its preflight check, its `features.compliance` gate, and `applyT4NodeIsolation`), whose placement against [0150.S3](../0150-tenant-security-settings/steps.md#steps) is O9. The quota agent's DaemonSet and its policy `lenny-quota-agent-node-label` are [0118.S6](../0118-agent-pod-layout/steps.md#steps)'s, which depends on this step. The removal of the T4 webhook makes T-12.9.11 moot.

### 0136.S8 Preflight and the continuous check
- status: Planned
- aliases: [PM-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0136.S7]

The preflight checks of "PM-6", the continuous check in `lenny-ops` with its alerts and the drift drain, and the cluster-wide pod reads of `lenny-ops` that the check needs. The pass computes no quota figure itself: [0118.S6](../0118-agent-pod-layout/steps.md#steps) adds each pool's `diskQuota`, the `lenny_quota_agent_*` series, and `QuotaAgentUnavailable` to it.

### Notes on the steps

0136.S1 and 0136.S2 can land first and in either order. 0136.S3, 0136.S7, 0136.S8, 0136.S4, 0136.S5, and 0136.S6 land in that order.
