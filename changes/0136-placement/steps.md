# Steps: 0136 Placement

## Validation owed

- Real node pools on EKS, GKE, and AKS under `strict` and `compact`, with classes mapped onto provider labels and taints, including GKE Sandbox's own gVisor node pool label and taint and AKS's Kata node pool, and which provider labels are stable enough to name in a class.
- Taints and node selectors with each `RuntimeClass`: `RuntimeClass` `scheduling` merged with the pod's own affinity and tolerations, for gVisor, `gvisor-monitored`, and Kata on each provider.
- User namespace prerequisites per node image: EKS AL2023 and Bottlerocket, GKE Container-Optimized OS and Ubuntu, and AKS Azure Linux and Ubuntu, with every volume type a Lenny pod mounts, and `hostUsers: false` on k3s 1.33 or later in rootless and rootful mode for `lenny up`.
- DaemonSet tolerations of common CNI, CSI, kube-proxy, and logging agents (aws-node, ebs-csi, Calico, Cilium, and Fluent Bit), and what each can reach on an agent node.
- Cluster autoscaler and Karpenter scale-up for a pending pod that needs a class taint and a tenant taint at once.
- The Pod Security level that `lenny-activity-collector`'s node socket needs in `lenny-node-agents`, and `lenny-quota-agent`'s `CAP_SYS_ADMIN` and read-write mount there, with its `Localhost` seccomp profile and the `hostPath` of `seccomp-install`.
- The `authentication.kubernetes.io/node-name` claim of a pod-bound ServiceAccount token, which `lenny-quota-agent-node-label` reads, on each supported Kubernetes version and on EKS, GKE, and AKS.
- The node check's cost on a cluster with thousands of nodes and pods.

## Steps

### 0136.S1 Removal of allowCrossTenantReuse
- status: Planned
- aliases: [PM-A]
- lanes: [specification, code, documentation]

Removal of `allowCrossTenantReuse`.

### 0136.S2 Components, priority classes, and namespaces
- status: Planned
- aliases: [PM-B, PM-8, PM-9]
- lanes: [specification, code, documentation]

The component table, the priority class scheme with one agent priority class per class and the class quotas, the `lenny-ops` settlement, and the namespaces with O3.

### 0136.S3 Classes, presets, rendering, and enforcement
- status: Planned
- aliases: [PM-C, PM-1, PM-5, PM-6]
- lanes: [specification, code, documentation]
- depends-on: [0136.S2]

Classes, presets, rendering of agent and system pods, user namespaces on the standard class, named node agents, admission, preflight, the continuous check, and removal of the T4 placement mechanism. It depends on 0136.S2 for the priority classes, and on SI's userns decision.

### 0136.S4 Node tenancy and single-tenant pools
- status: Planned
- aliases: [PM-D, PM-2]
- lanes: [specification, code, documentation]
- depends-on: [0136.S3, 0147.S5, 0150.S2]

Node tenancy and single-tenant pools. It depends on DR-5 (0147.S5) for owned pools and the `lenny.dev/served` reader, and on TSS-B (0150.S2), which stores `securitySettings.placement.nodeTenancy`.

### 0136.S5 Offerings and the pool placement block
- status: Planned
- aliases: [PM-E, PM-3]
- lanes: [specification, code, documentation]
- depends-on: [0136.S4, 0141.S1, 0147.S9, 0142.S2, 0142.S4]

Offerings, the admin API, the pool `placement` block, the routing check, owned-pool choice, and the reserved overlay. It depends on RT-2's post-chain point, which 0141.S1 lands, and on DR-10 (0147.S9). It also adds the offering grant mirror of SO-4a (0142.S4) and the posture's `placementOffering` and `placementConfigured` fields of SO-2 (0142.S2).

### 0136.S6 Placement requirements and region placement
- status: Planned
- aliases: [PM-F, PM-4, PM-7]
- lanes: [specification, code, documentation]
- depends-on: [0136.S5, 0141.S1, 0149.S1, 0142.S2]

Placement requirements with `allowReusedPod`, `sessionIsolationLevel` fields, and region placement. It depends on RT-2 (0141.S1), on TS-1 (0149.S1), and on SO-2 (0142.S2) for the record, its write path, `weakeningDelaySeconds`, and the applier that activates an environment's pending relaxation. Under O7 it adds the record's `placement` section and the posture's placement fields. 0150.S3 (TSS-C) lands the operator's placement settings as sources once 0136.S4 and 0136.S6 are in.

### Notes on the steps

0136.S1 and 0136.S2 can land first and in either order. 0136.S3 to 0136.S6 land in order.
