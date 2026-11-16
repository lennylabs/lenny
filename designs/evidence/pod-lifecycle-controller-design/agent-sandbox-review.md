# Review: upstream agent-sandbox compared with Lenny's pod lifecycle

Date: 2026-11-15. Lenny tree: branch `design/v1-cleanup`, commit `8702041e8`. Upstream tree read: `kubernetes-sigs/agent-sandbox` `main` at commit `454d3353` (2026-11-13). The API type files under `api/` and `extensions/api/` are identical between that commit and the `v1.0.11` tag (checked with `git diff v1.0.11 HEAD`).

Conventions in this report:

- Every upstream claim carries a URL. Type-level claims cite the `v1.0.11` tag. Documentation claims cite `main`, which is what the docs site renders.
- Lenny claims cite the spec by section heading and bold paragraph label, the designs by file and heading, and the code by path.
- **[unverified]** marks a statement that could not be confirmed from a primary source.
- The designs listed in the brief are treated as implemented. Each of them says in its status line that nothing in it is built. Where the code at this commit still reflects the pre-design state (for example `sdkWarmDisabled` on `SandboxWarmPool`), the report says so.

---

## 1. Upstream today

### 1.1 Release and API version

- The latest release is `v1.0.11`, published 2026-11-12. `v1.0.0` was published 2026-08-28. Releases since then have appeared about once a week (`v1.0.1` through `v1.0.11`) ([releases](https://github.com/kubernetes-sigs/agent-sandbox/releases)).
- `v1.0.0` moved the core and extension APIs to `v1beta1` and removed `v1alpha1` together with the conversion webhook. Direct upgrades from releases before `v0.5.0` to `v1.0.0` are not supported ([v1.0.0 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0)).
- `v1beta1` is the only served version across all CRDs. The roadmap records the alpha-to-beta move as done ([roadmap](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/roadmap.md)).
- There are two API groups. `Sandbox` is in `agents.x-k8s.io`, and `SandboxTemplate`, `SandboxClaim`, and `SandboxWarmPool` are in `extensions.agents.x-k8s.io` ([extensions groupversion](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/extensions/api/v1beta1/groupversion_info.go), [README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)).
- The Go module is `sigs.k8s.io/agent-sandbox`. At `v1.0.11` it requires Go 1.26, `k8s.io/api` v0.37.1, and `controller-runtime` v0.25.2 ([go.mod](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/go.mod)).
- Kubernetes 1.25 or later with CRD CEL validation is required, and 1.29 or later is recommended ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)).
- No newer CRD kind exists in the repository. The `api/v1beta1` and `extensions/api/v1beta1` directories define exactly these four kinds ([api](https://github.com/kubernetes-sigs/agent-sandbox/tree/v1.0.11/api/v1beta1), [extensions/api](https://github.com/kubernetes-sigs/agent-sandbox/tree/v1.0.11/extensions/api/v1beta1)). The snapshot feature uses GKE's own `podsnapshot.gke.io` CRDs ([snapshots doc](https://agent-sandbox.sigs.k8s.io/docs/sandbox/snapshots/)).

### 1.2 CRDs and fields

Source for this subsection: [sandbox_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/api/v1beta1/sandbox_types.go), [sandboxtemplate_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/extensions/api/v1beta1/sandboxtemplate_types.go), [sandboxclaim_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/extensions/api/v1beta1/sandboxclaim_types.go), and [sandboxwarmpool_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/extensions/api/v1beta1/sandboxwarmpool_types.go).

**`SandboxBlueprint`** is a struct that `Sandbox.spec` and `SandboxTemplate.spec` both inline. Its fields are as follows:

- `podTemplate`: a full `corev1.PodSpec` plus pod labels and annotations.
- `volumeClaimTemplates`: up to 128 entries, each a PVC named `<template>-<sandbox>` and owned by the Sandbox. The list is immutable on a Sandbox.
- `service`: a `*bool` that controls a headless Service.
- `failedPodPolicy`: `Retain` (the default) or `Recreate`.

**`Sandbox`** (`agents.x-k8s.io/v1beta1`, short name `sandbox`, status subresource only):

- The spec holds the blueprint fields, `shutdownTime`, `shutdownPolicy` (`Delete` or `Retain`, default `Retain`), and `operatingMode` (`Running` or `Suspended`, default `Running`).
- The status holds `serviceFQDN`, `service`, `conditions`, `selector`, `podIPs` (a list), and `nodeName`.
- The conditions are `Ready`, `Suspended`, `PodScheduled` (mirrored from the pod), and `Finished`.
- The backing pod has the same name as the Sandbox. The `agents.x-k8s.io/pod-name` annotation is deprecated ([v1.0.0 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0)).
- The Sandbox has no `/scale` subresource. The alpha `spec.replicas` field (0 or 1) was replaced by `operatingMode`, and the KEP records that this drops HPA and KEDA integration on Sandbox ([KEP-694](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/694-kep-for-suspend-and-resume-for-beta/README.md)).

**`SandboxTemplate`**:

- The spec holds the blueprint plus four further fields:
  - `networkPolicy`: ingress and egress rules.
  - `networkPolicyManagement`: `Managed` (the default) or `Unmanaged`.
  - `envVarsInjectionPolicy`: `Disallowed` (the default), `Allowed`, or `Overrides`.
  - `volumeClaimTemplatesPolicy`: `Disallowed` (the default), `Allowed`, or `Overrides`.
- Under `Managed` with no rules, the controller creates one shared NetworkPolicy per template. It admits ingress from the sandbox router only, and it admits egress to the public internet while blocking RFC 1918 ranges and the metadata server.
- When a template provisions a Sandbox and the PodSpec leaves `automountServiceAccountToken` unset, the controller sets it to `false`.

**`SandboxWarmPool`**:

- The spec holds `replicas` (default 1, minimum 0), `sandboxTemplateRef`, and `updateStrategy.type` (`Recreate` or `OnReplenish`, default `OnReplenish`).
- The status holds `replicas`, `readyReplicas`, `selector`, and `observedGeneration`.
- The resource has a `/scale` subresource on `.spec.replicas`, so HPA and KEDA can drive it ([HPA example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/hpa-swp-scaling), [KEDA scale-to-zero example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/keda-scale-to-zero)).

**`SandboxClaim`**:

- The spec fields are as follows:
  - `warmPoolRef`: required. No `templateRef` exists in `v1beta1`.
  - `lifecycle.shutdownTime`.
  - `lifecycle.ttlSecondsAfterFinished`.
  - `lifecycle.shutdownPolicy`: `Delete`, `DeleteForeground`, or `Retain`.
  - `additionalPodMetadata`: label keys must come from an allowlisted domain, which defaults to `sandbox.users.io`.
  - `env`: forces a cold start.
  - `volumeClaimTemplates`: forces a cold start.
- The status holds `conditions` and `sandbox.{name, podIPs, serviceFQDN}`.
- The Kubernetes blog post of March 2026 still describes a claim "against a SandboxTemplate". `v1beta1` changed this to a warm-pool reference ([blog source](https://github.com/kubernetes/website/blob/main/content/en/blog/_posts/2026/agent-sandbox.md), [KEP-1434 "Client-Side Resolution"](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/1434-multi-cluster-fleet/README.md)).

### 1.3 Controller behaviour

- **Claiming.**
  - A `SandboxClaim` names a warm pool. The claim controller looks for a ready, unclaimed Sandbox in that pool and adopts it with optimistically locked writes. When no warm member is available, it cold-starts a new Sandbox from the pool's template ([sandboxclaim_controller.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/controllers/sandboxclaim_controller.go)).
  - On adoption, ownership of the Sandbox moves from the pool to the claim, and the pool no longer manages that Sandbox ([sandboxwarmpool_types.go, `updateStrategy` comment](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/extensions/api/v1beta1/sandboxwarmpool_types.go)).
  - The controller stamps the claim's UID label and name annotation on the pod after adoption ([configuration.md, "SandboxClaim identity on Pods"](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/configuration.md)).
- **Warm pools.**
  - The pool controller keeps `replicas` unclaimed Sandboxes.
  - It marks a Sandbox stale when the effective blueprint hash changes. A metadata-only edit does not make a Sandbox stale. `Recreate` replaces stale unclaimed members, and `OnReplenish` leaves them in place. Claimed Sandboxes are never replaced ([sandboxwarmpool_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/extensions/api/v1beta1/sandboxwarmpool_types.go)).
  - The refill rate and the delay before refill are tunable with `--sandbox-warm-pool-max-refill-rate` and `--sandbox-warm-pool-replenish-delay`.
  - Warm-pool pods are marked as safe to evict by default. The claim controller removes that annotation on adoption ([configuration.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/configuration.md)).
- **Scaling.** Scaling uses `replicas` through the scale subresource, and the project's examples drive it with HPA or KEDA. The roadmap reports claim bursts of up to 300 sandboxes per second and targets 1000 or more claims per second ([roadmap](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/roadmap.md)).
- **Lifecycle and deletion.**
  - At `shutdownTime` the controller deletes the pod and Service, then applies `shutdownPolicy` to the Sandbox.
  - A claim's `shutdownPolicy` governs the claim. `Delete` and `DeleteForeground` cascade to the Sandbox, and `Retain` deletes the Sandbox but keeps the claim ([sandboxclaim_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/extensions/api/v1beta1/sandboxclaim_types.go)).
  - PVCs are garbage-collected when the Sandbox object is deleted.
- **Pod failure.** Under `failedPodPolicy: Recreate`, the controller deletes a failed pod and creates a new one under the same Sandbox identity. Recreation backs off exponentially from 5 s to 5 min, and the backoff state is held in memory ([KEP-729](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/729-opt-in-pod-recreation-on-failure/README.md)).
- **Pause and resume.** `operatingMode: Suspended` deletes the pod and keeps the Sandbox object and its PVCs. `Running` creates a new pod from the current `podTemplate`, which reattaches the PVCs. Process memory and non-PVC storage are lost unless GKE Pod Snapshots are used (section 1.4) ([sandbox_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/api/v1beta1/sandbox_types.go)). Automatic suspend on inactivity is listed as planned ([roadmap](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/roadmap.md)).

### 1.4 Snapshot primitives (GKE Pod Snapshot)

- **Client-side only.** Snapshot support lives in the Python SDK as `PodSnapshotSandboxClient` and `SnapshotEngine`. The SDK creates `PodSnapshotManualTrigger` resources and lists, deletes, and restores `podsnapshot.gke.io` objects. The Go SDK does not wrap these resources, and the controllers contain no snapshot logic ([snapshots doc](https://agent-sandbox.sigs.k8s.io/docs/sandbox/snapshots/), [SDK snapshot README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/clients/python/agentic-sandbox-client/k8s_agent_sandbox/gke_extensions/snapshots/README.md)). A `grep` for `podsnapshot` in `controllers/`, `extensions/controllers/`, and `cmd/` at `454d3353` returns nothing.
- **Operations.**
  - `snapshots.create` takes a snapshot.
  - `suspend(snapshot_before_suspend=True)` takes a snapshot and then sets `operatingMode: Suspended`.
  - `resume()` sets `Running`, and GKE restores the latest matching snapshot.
  - `restore(snapshot_uid)` restores a named snapshot while the Sandbox is suspended.
  - Sources: [SDK snapshot README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/clients/python/agentic-sandbox-client/k8s_agent_sandbox/gke_extensions/snapshots/README.md). `v1.0.8` added the same primitives to the RL SDK ([v1.0.8 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.8)).
- **Golden-snapshot warm pools.** A `PodSnapshotPolicy` that groups snapshots by a template label lets every new warm-pool member boot restored from one primed snapshot. GKE matches a restore by a hash of the distilled pod spec rather than by pod identity ([golden warm pool example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/podsnapshot-golden-warmpool)).
- **Runtime requirements.**
  - The pod runs in GKE Sandbox, which is gVisor.
  - The Python SDK test documents GKE Standard 1.35.2-gke.1842000 or later. The golden example documents Autopilot or Standard 1.35.3 or later with `--enable-pod-snapshots`.
  - Storage is a Cloud Storage bucket configured through `PodSnapshotStorageConfig`, and the pod's KSA needs IAM grants ([SDK snapshot README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/clients/python/agentic-sandbox-client/k8s_agent_sandbox/gke_extensions/snapshots/README.md), [golden example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/podsnapshot-golden-warmpool)).
- **What GKE captures** ([GKE Pod snapshots concept](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/pod-snapshots)):
  - Captured: process memory, threads, registers, open file descriptors, the container rootfs, `emptyDir` volumes, and `tmpfs` mounts.
  - Not captured: PVCs.
  - Preserved: loopback connections, listening sockets, and Unix sockets. Active external connections are closed on restore.
  - The restored pod gets a new IP address and hostname. Wall-clock time jumps forward. GKE states that unique application state and secrets must be reinitialized.
  - `whole-pod` scope needs the same distilled spec hash, compatible hardware, and matching GKE Sandbox kernel and GPU driver versions, and it does not support E2 machines. `rootfs-only` scope drops the spec-hash match.
  - L4 GPUs are supported, and a GPU example was added in `v1.0.10` ([v1.0.10 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.10)).
- **Snapshot API KEP.** A KEP for a Suspend/Resume and SnapshotClass API (PR #762) was closed without merge ([PR #762](https://github.com/kubernetes-sigs/agent-sandbox/pull/762)). The beta KEP explicitly excludes a snapshot API ([KEP-694](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/694-kep-for-suspend-and-resume-for-beta/README.md)).

### 1.5 Multi-cluster warm-pool planner

- **Status.** `examples/multi-cluster-fleet` is marked "Status: example" and is "Not part of the supported `sigs.k8s.io/agent-sandbox` API surface" ([fleet example README](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/multi-cluster-fleet)). It began in `v1.0.6` and was completed in `v1.0.7` ([v1.0.6 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.6), [v1.0.7 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.7)).
- **Design** ([KEP-1434](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/1434-multi-cluster-fleet/README.md)):
  - `fleetctl` is a stateless planner CLI that the admin runs outside the clusters.
  - Each cluster runs a `fleet-member` Deployment that polls every 30 s and creates `SandboxWarmPool` objects locally.
  - The members coordinate through one object-storage bucket. GCS is the only backend implemented, and the bucket must support compare-and-set.
  - The design adds no CRD, no hub apiserver, and no cross-cluster networking. The admin must pre-apply the `SandboxTemplate` objects on every cluster.
  - Placement is capacity-aware, uses Hamilton apportionment ([v1.0.6 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.6)), and has stale-cluster freezing.
  - A Python `FleetSandboxClient` resolves a template to a cluster before it creates the claim.
- **Stated non-goals.** The KEP excludes interactive serving ("Batch and RL only"), autoscaling, cross-cluster networking, and any throughput improvement. It reports 1,069,000 concurrent sandboxes across six clusters.

### 1.6 Networking and port-forward notes

- **Router.**
  - The sandbox router is a Go reverse proxy. It routes on `X-Sandbox-ID`, `X-Sandbox-Namespace`, `X-Sandbox-Port`, and related headers, and it also routes by URL path for browsers.
  - It supports HTTP, native gRPC, and gRPC-Web.
  - Its authorizers are `allow-all` (the default), `tokenreview`, and `scoped-token` (HMAC v1 and Ed25519 v2).
  - It never creates or looks up Sandbox resources ([router README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/sandbox-router/README.md)).
  - The threat model notes that with the default authorizer, the IP-class check is the only SSRF defence ([threat model](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/security/threat_model.md)).
- **Port-forward.** A direct pod port-forward "doesn't work on Kata or gVisor", because the workload runs in a separate kernel and nothing listens in the pod's host-side network namespace. Such clients go through the router instead ([Python client README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/clients/python/agentic-sandbox-client/README.md), [README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)). The Nitro example states that `kubectl exec`, `logs`, and `port-forward` work as for any pod ([Nitro example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/nitro-eks-sandbox)).
- **Headless Service.** Each Sandbox gets a headless Service only when `spec.service: true`. Warm-pool sandboxes have no per-sandbox Service, so the router finds them by name through its cache ([router README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/sandbox-router/README.md)).

### 1.7 SDKs and clients

- **Python.** The package is `k8s-agent-sandbox` on PyPI ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)).
- **Go.** The package is `sigs.k8s.io/agent-sandbox/clients/go/sandbox`. It is versioned by repository tag and offers Gateway, port-forward, router-port-forward, and in-cluster connectivity ([Go client README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/clients/go/README.md)).
- **TypeScript.** The client lives in `clients/typescript/agentic-sandbox-client`, and its README shows import from `./dist/index.js` ([TS README](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/clients/typescript/agentic-sandbox-client)). Whether it is published to npm is **[unverified]**.
- **`sandboxd`.** This is the in-pod runtime daemon defined by KEP-539.2. It serves a gRPC `ProcessService` on :9090 and a REST filesystem API on :8080 ([sandboxd guide](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/packages/sandboxd/USER_GUIDE.md), [KEP-539.2](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/539.2-runtime-standardization/README.md)).
- **Integrations.** Integrations exist for an MCP server, NeMo Gym, Gymnasium, OpenAI, OpenHands, and deepagents ([clients/integrations](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/clients/integrations)).

### 1.8 Isolation runtimes

The project states that it delegates isolation to the runtime through `RuntimeClass` ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)). The following runtimes have documentation:

- **gVisor** ([gVisor use case](https://agent-sandbox.sigs.k8s.io/docs/use-cases/gvisor-isolation/)).
- **Kata Containers** on GKE with nested virtualisation on N2 and Ubuntu nodes, and on AKS ([Kata GKE example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/kata-gke-sandbox)).
- **Kata with Firecracker** (`kata-fc`) ([Firecracker example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/firecracker-sandbox)).
- **urunc** ([urunc example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/urunc-sandbox)).
- **Hyperlight.** The micro-VM runs in an ordinary pod and receives `/dev/kvm` through a device plugin. It uses no RuntimeClass and runs a Unikraft guest ([Hyperlight example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/hyperlight-sandbox), added in [v1.0.10](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.10)).
- **AWS Nitro Sandboxes** on EKS Auto Mode. The pod uses the `isolated-sandbox` RuntimeClass and requires EKS 1.36 or later ([Nitro example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/nitro-eks-sandbox), added in [v1.0.11](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.11)).

### 1.9 Multi-tenancy

- The tenancy unit is the Kubernetes namespace plus RBAC. The threat model treats "different Sandboxes" as different tenants. Its mitigations are the managed NetworkPolicy, the `automountServiceAccountToken: false` default, filtering of system labels in user templates, and RBAC on the CRDs ([threat model](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/security/threat_model.md)).
- The controller-owned claim identity on pods (`agents.x-k8s.io/claim-uid` and `claim-name`) is documented as discovery metadata. The documentation states that it does not authenticate a caller ([configuration.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/configuration.md)).
- The planned items include "Sandbox / Pod Identity Association" at claim time and NetworkPolicy "attach" at claim time ([roadmap](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/roadmap.md)).
- The fleet KEP uses a namespace and a bucket as the isolation boundary between teams ([KEP-1434](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/1434-multi-cluster-fleet/README.md)). `v1.0.11` added a multi-tenant RL example ([v1.0.11 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.11)).

### 1.10 Stability guarantees

- The release line is `v1.0.x` and the API version is `v1beta1`.
- The repository has no written compatibility or deprecation policy for `v1beta1`:
  - `RELEASE.md` describes tagging only ([RELEASE.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/RELEASE.md)).
  - `docs/release.md` asks contributors to label breaking changes ([docs/release.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/release.md)).
  - The docs landing page makes no stability statement ([docs](https://agent-sandbox.sigs.k8s.io/docs/)).
- Patch releases still carry "Breaking Changes / Action Required" items. `v1.0.11` changed a router default, changed a Python exception type, and added a metric label ([v1.0.11 notes](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.11)).
- The type comments state that moving a field out of `SandboxBlueprint` "is breaking" ([sandbox_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/api/v1beta1/sandbox_types.go)). That comment records an intent about field changes, and the repository publishes no matching guarantee.

### 1.11 Governance and maintainers

- The project is a SIG Apps subproject ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)).
- Google announced it in November 2025 ([Google Open Source blog](https://opensource.googleblog.com/2025/11/unleashing-autonomous-ai-agents-why-kubernetes-needs-a-new-standard-for-agent-execution.html)). The spec says it launched at KubeCon Atlanta. That venue is **[unverified]**.
- Root approvers are `janetkuo`, `justinsb`, `soltysh`, and `barney-s` ([OWNERS](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/OWNERS)). The extension-controller approvers add `vicentefb`, `igooch`, `shruti6991`, and `aditya-shantanu` ([extensions OWNERS](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/controllers/OWNERS)).
- The Kubernetes blog post of 2026-03-20 is by Janet Kuo and Justin Santa Barbara ([blog](https://kubernetes.io/blog/2026/03/20/running-agents-on-kubernetes-with-agent-sandbox/)).
- On 2026-11-15 the repository reported Apache-2.0, 4,623 stars, 185 open issues, and 189 contributors on the first API page (`gh api`, [repo](https://github.com/kubernetes-sigs/agent-sandbox)). KEPs are optional and modelled on the Kubernetes process ([KEP index](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/README.md)).

### 1.12 Roadmap

The roadmap below is taken from [roadmap.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/roadmap.md). Several items marked "In Progress" link to issues or PRs that are now closed: #323, #491, #127, and #597 (#597 closed unmerged). The roadmap text therefore lags the tracker.

- **In progress:**
  - Decoupling the API from the runtime through a common proto backend (KEP #597 closed unmerged, KEP #747 merged as KEP-539.2).
  - Rolling updates for templates and warm pools.
  - Smart warm-pool selection.
  - Strict Sandbox-to-pod mapping.
- **Planned:**
  - "API Support for Multi-Sandbox per Pod".
  - Automatic suspend and resume, and scale to zero.
  - Identity association at claim time.
  - Network policy attach at claim time.
  - Storage customisation at claim time.
  - Startup actions.
  - TTL auto-deletion.
  - An MCP server.
  - A UI.
  - Claim latency targets of 100 ms and then 50 ms.

---

## 2. Lenny today

### 2.1 What exists

- **No upstream code dependency.** `go.mod` does not list `sigs.k8s.io/agent-sandbox`. Lenny uses Go 1.25, `k8s.io/api` v0.31.4, and `controller-runtime` v0.19.4.
- **Lenny's own CRDs.** Lenny defines its CRDs under `lenny.dev/v1alpha1` in `pkg/apis/lenny/v1alpha1/` and ships them in `charts/lenny/crds/`:
  - `Sandbox`
  - `SandboxTemplate`
  - `SandboxClaim`
  - `SandboxWarmPool`
  - `Runtime`, a fifth CRD with no upstream counterpart (`lenny.dev_runtimes.yaml`).
- **CRD metadata.** Each CRD carries `lenny.dev/schema-version: "1"`, a single served and stored version, and `PreserveUnknownFields` on spec and status.
- **Controllers.** Lenny runs these controllers:
  - The Sandbox-to-Pod reconciler in `pkg/controller/sandbox`, which renders pods in `podspec/`.
  - The WarmPoolController in `pkg/controller/warmpool`, which plans pods in `plan/`.
  - The PoolScalingController in `pkg/controller/poolscaling`.
- **Ownership.** The warm pool is the controller owner of each Sandbox (`pkg/controller/warmpool/controller.go`, `SetControllerReference`), and the Sandbox owns its Pod (`pkg/controller/sandbox/controller.go`).
- **Interface layer.** `pkg/podlifecycle/agentsandbox.go` implements the spec's `PoolReader`, `PodLifecycleManager`, and `PoolManager` as `AgentSandbox*` types. Those types read and write the `lenny.dev` types.

### 2.2 Field-by-field comparison of the shared names

#### `Sandbox`

| Aspect | Upstream `agents.x-k8s.io/v1beta1` | Lenny `lenny.dev/v1alpha1` |
|:--|:--|:--|
| Pod definition | `spec.podTemplate` (full PodSpec) and `volumeClaimTemplates` | No pod template. The controller renders the pod from the pool's `SandboxTemplate` and the `Runtime` (`pkg/controller/sandbox/podspec`). Spec fields are `runtimeRef`, `poolRef`, `isolationProfile`, `executionMode`, `deliveryMode`, `resourceClass`, `topologySpreadConstraints`, and `requireSoPeercred`. |
| Desired run state | `operatingMode: Running \| Suspended` | None. A Lenny pod is never suspended in place. |
| Expiry | `shutdownTime` and `shutdownPolicy` | None. Uptime retirement comes from `recycle.maxPodUptimeSeconds` against the pod creation time (§4.6.1 **Occupancy projection**). |
| Failure | `failedPodPolicy: Retain \| Recreate` | A failed pod is retired and replaced through the pool. Session-isolation design SI-4 adds dead slots and replaces a pod when half its slots are dead. |
| Service | Optional headless Service and `status.serviceFQDN` | None. The gateway dials `status.podIP`. |
| Status state | `Ready`, `Suspended`, `PodScheduled`, and `Finished` conditions, with no phase | `status.phase` is an enum: `warming`, `idle`, `reserved`, `claimed`, `sdk_connecting`, `draining`, `failed`, `terminated`. It is a level-triggered projection of claim state (§4.6.1 **Occupancy projection**). Conditions include the nonce-only marker. |
| Identity in status | `podIPs` (list) and `nodeName` | `podIP` (single), `podName`, `nodeName`, `tenantId`, and `sessionId` |
| Pod name | Equal to the Sandbox name | Recorded in `status.podName` |
| Finalizer | None documented | `lenny.dev/session-cleanup` (§4.6.1 **Sandbox finalizers**) |
| Design additions | None | Slot count recorded on the Sandbox (SI-4, no field name given). `spec.reserved` (tenant-derived-runtimes DR-10). `warmStores`, `fillFailure`, and `withheldStores`, plus the `lenny.dev/owner-tenant` label (tenant-derived-runtimes, "Stored formats and wire contracts this forces"). |

#### `SandboxTemplate`

| Aspect | Upstream | Lenny |
|:--|:--|:--|
| Content | A pod blueprint (PodSpec and PVC templates) plus NetworkPolicy and claim-injection policies | A pool definition with no PodSpec: `runtimeRef`, `isolationProfile`, `workspaceTier`, `egressProfile`, `dnsPolicy`, `resourceClass` (`small`, `medium`, or `large`), `executionMode`, `maxConcurrent`, `deliveryMode`, `spiffeBinding`, checkpoint timing fields, grace periods, `workspaceSizeLimitBytes`, `sessionPolicy.recycle.{scrubProfile, acknowledgeMicrovmResidualState, runtimeProcess}`, `acknowledgeNonceOnlyAuth`, and `topologySpreadConstraints` |
| Network policy | One generated policy per template | Pre-created NetworkPolicies selected by pod labels such as `lenny.dev/delivery-mode` and the egress-profile label (`pkg/controller/warmpool/controller.go`) |
| Who writes it | Users or admins | The PoolScalingController renders it from the Postgres pool record (§4.6.2; tenant-derived-runtimes "Terms") |
| Design additions | None | `podRuntimeRef`, the owner, `warmClones`, `warmCloneBytes`, `runtimeRevision`, the resolved placement, and the `PlacementRefused` and `PlacementUnschedulable` conditions (tenant-derived-runtimes; placement PM-5 and PM-6). The renames `maxConcurrentSessions` to `maxConcurrentSessionsPerPod` and `acknowledgeProcessLevelIsolation` to `acknowledgeSharedPod` (agent-pod-layout PL-9). `recycle.runtimeProcess` is removed (session-isolation SI-3). |

#### `SandboxWarmPool`

| Aspect | Upstream | Lenny |
|:--|:--|:--|
| Size | `replicas` with a `/scale` subresource, driven by HPA or KEDA | `minWarm`, `maxWarm`, and `scalePolicy` (`schedules`, `scaleToZero`, `bootstrapMinWarm`, `podWarmupSecondsBaseline`, `sdkConnectTimeoutSeconds`, and SDK-warm breaker settings), set by the PoolScalingController. There is no scale subresource. |
| Template link | `sandboxTemplateRef.name` | `templateRef` (a string) |
| Update policy | `updateStrategy: Recreate \| OnReplenish` with blueprint-hash staleness | No field. Runtime revisions create a pod set per active revision (tenant-derived-runtimes DR-5, "Pods"). Whether the code tracks drift between a template and its live pods was not traced **[unverified]**. |
| Membership after claim | A claimed Sandbox leaves the pool, because ownership moves to the claim. | A claimed Sandbox stays owned by the pool and returns to idle inventory after scrub and reserved hold (§4.6.1 **Reserved hold**). |
| Status | `replicas`, `readyReplicas`, and `selector` | `warmCount`, `readyCount`, `scalingMode`, `bootstrapHoursOfData`, `sdkWarmCircuitBreaker`, and conditions |
| Design changes | None | `reservedPods` added (DR-10). `sdkWarmDisabled` and `sdkWarmCircuitBreaker` removed (agent-pod-layout PL-8). The code at this commit still has both. |

#### `SandboxClaim`

| Aspect | Upstream | Lenny |
|:--|:--|:--|
| Meaning | A user's request for a sandbox from a pool. It lasts for the user's use of that sandbox. | A per-pod occupancy lock that the gateway holds. It multiplexes every session on the pod (§4.6.1 **Pod claim mechanism**). |
| Target | `warmPoolRef`. The controller chooses the Sandbox. | `sandboxRef`. The gateway chooses the pod and creates `claim-<podName>`, and `AlreadyExists` resolves races. |
| Controller | A claim controller adopts or cold-starts and transfers ownership. | No claim controller. The WarmPoolController projects phase from the claim and garbage-collects orphan claims (§4.6.1 **Orphaned `SandboxClaim` detection**). The `lenny-sandboxclaim-guard` webhook rejects a second claim on a pod (§4.6.1 **`SandboxClaim` admission webhook**). |
| Lifecycle | `shutdownTime`, `ttlSecondsAfterFinished`, and a cascading `shutdownPolicy` | No ownerReference. Explicit deletion happens on hold expiry, release, orphan GC, or pod termination. |
| Status | Conditions and `sandbox.{name, podIPs, serviceFQDN}` | `phase` (`bound`, `recycling`, `reserved`, `released`, or `failed`), `holdExpiresAt`, `rewarmStartedAt`, and `bindingStateTransitionTime` |
| Per-claim input | `env`, `volumeClaimTemplates`, and `additionalPodMetadata`, each forcing a cold start or being allowlisted | `tenantId` only. Per-session inputs travel over the adapter protocol. |

The upstream claim and Lenny's claim share a name and serve different purposes. Upstream's claim selects and then owns a Sandbox. Lenny's claim locks a pod that the gateway has already selected.

### 2.3 Lenny features with no upstream counterpart

- **Slots and several sessions per pod.**
  - A pod has `maxConcurrentSessionsPerPod` slots (default 1). Going above 1 requires `acknowledgeSharedPod`.
  - Each slot is a `runtime-<n>` container with its own UID and GID. A supervisor runs as PID 1 in each slot container and starts a fresh runtime process per session at bind.
  - These details are in session-isolation-design.md SI-1, SI-3, and SI-4, and in agent-pod-layout-design.md PL-9.
  - Upstream lists "API Support for Multi-Sandbox per Pod" as planned and has no design for it ([roadmap](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/roadmap.md)).
- **Recycling with scrub.**
  - A per-slot release runs a fixed sequence of steps (session-isolation SI-3 "Slot release"):
    - End the slot's processes.
    - Sweep the slot UID's files and IPC objects.
    - Run the cleanup commands.
    - End and sweep again.
    - The adapter removes what remains and verifies that the volumes are empty.
    - Prestage warm checkouts.
  - A whole-pod scrub reported through `ReportPodScrub` runs at occupancy zero, followed by the reserved hold (§5.2 **Scrub model.**, **Recycle lifecycle**).
  - Upstream has no reuse path. A claimed sandbox is never returned to a pool.
- **Tenant pinning.**
  - A recycling or concurrent pod is pinned to its first tenant for its lifetime (§5.2 **Tenant pinning:**, **Pinned idle inventory.**).
  - Upstream has no tenant field. Its tenancy unit is the namespace.
- **Session-isolation layout and broker.**
  - Lenny gives each slot its own volumes: `slot-<n>-workspace`, `-session`, `-tmp`, `-dshm`, `-run`, and `-netcred`.
  - The file-descriptor broker in each slot's supervisor opens entries beneath pinned roots with `openat2` and hands the descriptors to the adapter over `SCM_RIGHTS`.
  - `netagent` attributes each connection to a slot by socket owner UID.
  - Sources: agent-pod-layout PL-3 and PL-5; session-isolation SI-5 and SI-10.
- **Workspace checkpoints.**
  - Checkpoints are per slot and cover files only, in `/workspace` and `/session`.
  - Capture runs in pre-copy, capture, and upload passes. Chunks are FastCDC with SHA-256 names, packed and stored in the object store under per-tenant keys, and uploaded through presigned URLs.
  - The pause on a 516k-file Chromium tree is 0.11 s to 0.16 s with the change tracker (incremental-checkpoint-design.md "High-level design", "Benchmarks").
  - Upstream has PVC retention and GKE whole-pod snapshots, and nothing at the session level.
- **Placement classes.**
  - Node classes are `system`, `standard`, `sandboxed`, and `microvm`, combined with presets and `dedicated` node tenancy through tenant labels and taints.
  - Offerings, region affinity, and per-class PriorityClasses and quotas complete the model (placement-design.md PM-1 through PM-9).
  - Upstream leaves node placement to the user's PodSpec.
- **Warm stores.**
  - A pod-wide bare git store is filled by `lenny-store-fill` before the other containers start.
  - Slots borrow objects through alternates, and `prestage: true` checkouts are adopted at bind with a rename (workspace-materialization-design.md "Terms", WM-15).
- **Owned pools.**
  - A tenant-derived runtime gets one owned pool, rendered as a `SandboxTemplate` and `SandboxWarmPool` pair with `ownerTenantId`.
  - `maxOwnedPods` and `reservedPods` bound the pool, and reserved pods use a `lenny-reserved` PriorityClass (tenant-derived-runtimes-design.md DR-5, DR-10).
- **Other Lenny-only mechanisms.** The following are specified in §4.6.1, §5.2, and §6:
  - The SDK-warm and pod-warm distinction (§6.1), which the agent-pod-layout design removes (PL-8).
  - Per-session credential leases.
  - Certificate-expiry replacement of idle pods.
  - The `lenny.dev/host-schedulable` label.
  - The integer `minAvailable` PDB on idle pods.
  - The Postgres fallback claim path.
  - Session suspension, dormancy, and resume onto a new pod from a checkpoint (session-lifecycle-design.md SL-1, SL-3, SL-6).

### 2.4 Behavioural differences in the controller

- **Hot path.** Lenny keeps the controller off the claim hot path. The gateway claims by `CREATE` (§4.6.1 **Pod claim mechanism**). Upstream's claim controller is on the hot path, and its roadmap targets lower claim latency ([roadmap](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/roadmap.md)).
- **State authority.** Lenny's authoritative session state is in Postgres (`pod_assignment`), and the CRDs project it. Upstream's state is the CRDs.
- **Suspend.** Lenny's suspension releases a slot after checkpointing the workspace. A later resume restores onto a slot of any suitable pod, and process memory is not kept (session-lifecycle SL "Limits"). Upstream's suspend deletes a whole pod and keeps its PVCs. Lenny pods have no PVCs, and all slot volumes are `emptyDir` (agent-pod-layout PL-5).
- **Pool sizing.** Lenny computes warm targets from demand formulas, schedules, and bootstrap data (§4.6.2). Upstream delegates sizing to HPA or KEDA through `replicas`.

---

## 3. Comparison table

| Capability | Status | Note |
|:--|:--|:--|
| One Sandbox object per pod | Both, different | Upstream embeds a PodSpec. Lenny renders the pod from the pool template and the Runtime. |
| Pod template on the CRD | Upstream only | Lenny keeps pod rendering in code, driven by `Runtime` and pool settings. |
| `Runtime` CRD | Lenny only | Upstream has no runtime registry. |
| Warm pools | Both, different | Upstream uses `replicas` and a scale subresource. Lenny uses `minWarm`, `maxWarm`, schedules, and a formula-driven PoolScalingController. |
| Claiming | Both, different | Upstream: the controller picks from a pool reference and takes ownership. Lenny: the gateway picks a pod and locks it with a deterministic name, guarded by a webhook. |
| Pod reuse across sessions | Lenny only | Lenny has scrub, the reserved hold, and return to idle. Upstream never returns a claimed sandbox. |
| Several sessions per pod (slots) | Lenny only | Upstream lists this as planned. |
| Tenant pinning | Lenny only | Upstream's tenancy unit is the namespace. |
| Template update policy | Upstream only (as a field) | Upstream has `Recreate` and `OnReplenish` with hash staleness. Lenny uses runtime revisions and pod sets. |
| Suspend and resume | Both, different | Upstream deletes the pod and keeps PVCs. Lenny releases the slot and checkpoints files to the object store. |
| Process-memory snapshots | Upstream only | GKE Pod Snapshots, gVisor only, through the Python SDK. |
| Workspace checkpoints | Lenny only | Incremental, per slot, and deduplicated. |
| Scheduled expiry (`shutdownTime`) | Upstream only | Lenny uses session age, uptime, and recycle limits. |
| Failed-pod recreate with backoff | Upstream only | Lenny retires and replaces pods. |
| Headless Service and stable hostname | Upstream only | Lenny dials the pod IP from the gateway. |
| Data-plane router | Upstream only | Lenny's gateway fills this role. |
| In-pod runtime API | Both, different | Upstream has `sandboxd` (REST and gRPC). Lenny has the adapter protocol and supervisor channels. |
| Managed NetworkPolicy | Both, different | Upstream generates one per template. Lenny pre-creates policies per egress profile, selected by labels. |
| Placement classes and dedicated nodes | Lenny only | Upstream leaves placement to the PodSpec. |
| Region pinning | Lenny only | Placement design PM-7. |
| Multi-cluster warm-pool planner | Upstream only (example) | Batch and RL only. Lenny deploys once per region (placement PM-7, D24). |
| Warm git stores and prestaged checkouts | Lenny only | Workspace-materialization design WM-15. |
| Owned pools | Lenny only | Tenant-derived-runtimes design DR-5. |
| PDB for warm pods | Lenny only | Integer `minAvailable` (§4.6.1 **Disruption protection for agent pods**). Upstream marks warm pods as safe to evict. |
| Finalizer guarding active sessions | Lenny only | `lenny.dev/session-cleanup` |
| `PodScheduled` mirrored to status | Upstream only | Lenny's placement design adds `PlacementUnschedulable` on the template. |
| SDKs (Python, Go, TypeScript) | Upstream only | Lenny's client surface is its gateway API. |
| Isolation through RuntimeClass | Both | Lenny maps its isolation profiles to RuntimeClasses. Upstream also documents Hyperlight without a RuntimeClass. |
| Controller metrics and tuning guides | Both, different | Upstream publishes performance tuning and API priority-and-fairness guides. Lenny specifies metrics per mechanism. |

---

## 4. Features worth borrowing

### 4.1 GKE Pod Snapshots

**What it would give Lenny:**

- **Golden warm pods.** A warm Lenny pod runs no runtime process before bind (agent-pod-layout PL-8). Its memory therefore holds little of value, but its `emptyDir` volumes hold the filled warm git stores and prestaged checkouts. GKE captures `emptyDir` contents ([GKE concept](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/pod-snapshots)). A golden snapshot could skip `lenny-store-fill` and prestage time for `sandboxed` (gVisor) pools on GKE.
- **Keeping a suspended runtime's memory.** Session-lifecycle SL "Limits" states that a suspension without `interrupt` loses the runtime's in-memory state. A snapshot could keep that state, in principle.

**What it would cost:**

- **Platform scope.** It works only on GKE with gVisor, and it requires GCS storage, IAM, and `podsnapshot.gke.io` CRDs.
- **Integration work.** Upstream offers no controller integration, only a Python client. Lenny would drive `PodSnapshotManualTrigger` and `PodSnapshotPolicy` from Go itself. The spec already reserves `SnapshotPod` and `RestoreFromSnapshot` as future extension points (§4.6.1 **Internal abstraction layer:**).
- **Storage.** It adds per-pod GCS storage.

**Conflicts with Lenny's model:**

- **Whole-pod scope against slots.**
  - A snapshot captures every slot together. A pod at `maxConcurrentSessionsPerPod > 1` holds several sessions, possibly of several users of one tenant (session-isolation "Problem", SI-1), so a per-session restore is impossible.
  - Restoring a pod restores all its slots onto one new pod, and Lenny's resume model restores one session onto any pod (incremental-checkpoint IC-5).
  - Even at one slot, the snapshot covers adapter and `netagent` state that is per pod.
- **Secrets in snapshots.**
  - `tmpfs` mounts are captured. The per-slot `slot-<n>-run` volume (credentials file and manifest with `connectionNonce`) and `slot-<n>-netcred` are memory volumes (agent-pod-layout PL-3). A snapshot would write live credentials and nonces to GCS.
  - This conflicts with per-session credential leasing (§6.1 **Per-session credential lease lifecycle.**) and with the rule that the pod holds no object-store credential (incremental-checkpoint "Object-store grant"). GKE itself says secrets must be reinitialized after restore.
  - A golden snapshot taken from a warm pod would replicate any per-pod secret, nonce, or mTLS key material across every restored pod. Whether warm pods hold such material before bind is **[unverified]**; the adapter's mTLS certificate and its storage were not traced.
- **IP and hostname change on restore.** The gateway dials the adapter at `Sandbox.status.podIP` (`pkg/apis/lenny/v1alpha1/sandbox_types.go`). A restored pod is a new pod with a new IP, so the gateway would re-resolve it, and any certificate bound to the old identity would be invalid. External connections close, which ends the adapter's gateway streams.
- **Pinned broker roots.** The adapter records each broker root's device and mount ID and rejects descriptors that do not match (session-isolation SI-5 "The pinned root"). A restore onto new mounts would trip that check unless the adapter re-pins after a restore.
- **Single-connection supervisor channel.** `CH-SUPERVISE` admits one connection per slot for the pod's lifetime (session-isolation SI-2). GKE states that Unix sockets are preserved, so the channel could survive inside the restored sandbox. Whether that holds with SO_PEERCRED checks across a restore is **[unverified]**.
- **Spec-hash matching.** `whole-pod` restore needs an identical distilled pod spec. Lenny renders per-pod differences such as nonce-only mode, labels, and slot counts, so pods may not match. `rootfs-only` scope relaxes the match and still captures `emptyDir` **[unverified: whether `rootfs-only` includes `emptyDir`; the GKE page lists `emptyDir` without tying it to a scope]**.
- **Tenancy grouping.** Lenny runs all tenants in one agent namespace (placement PM-9). Snapshot grouping rules would have to key on tenant and pool labels to keep snapshots from crossing tenants.

**Assessment:** The golden warm-pod use on `sandboxed` pools is the only use that fits Lenny's model without redesign. Before that use is possible, the warm-pod contents would need an audit for secrets, and the adapter would need to re-pin roots and re-read its identity after a restore.

### 4.2 Multi-cluster warm-pool planner

- **What it would give Lenny:** It would spread warm capacity across several clusters inside one residency region.
- **What it would cost:**
  - Lenny's gateway claims pods through one API server, and `ClaimOpts.clusterID` is reserved without an implementation (§4.6.1 **Internal abstraction layer:**). The gateway, the Postgres pod registry, and the PoolScalingController would all need per-cluster awareness.
  - The planner creates `SandboxWarmPool` objects itself. In Lenny the PoolScalingController owns those objects (§4.6.2).
- **Conflicts with Lenny's model:**
  - The planner is batch and RL only. It polls every 30 s and states that interactive serving is a non-goal ([KEP-1434](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/1434-multi-cluster-fleet/README.md)).
  - Lenny deploys once per residency region, and cross-deployment routing is outside the placement design (placement PM-7 **Residency across regions (D24)**). Moving a warm pool between clusters is a teardown plus a cold fill, which conflicts with pinned and reserved inventory.
- **Assessment:** The parts worth reading are the planner's capacity report format, stale-cluster freeze, and apportionment rules. They are relevant if Lenny later adds multi-cluster pools within a region.

### 4.3 Other upstream items

| Item | What it would give Lenny | Cost | Conflict |
|:--|:--|:--|:--|
| `PodScheduled` mirrored onto the CRD ([sandbox_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/api/v1beta1/sandbox_types.go)) | Operators see why a warm pod is unscheduled without reading the pod. | Small. One condition on `Sandbox`. | None. Complements the placement design's `PlacementUnschedulable`. |
| Blueprint-hash staleness and `Recreate`/`OnReplenish` ([sandboxwarmpool_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/extensions/api/v1beta1/sandboxwarmpool_types.go)) | A defined rule for which idle pods are replaced when pool settings change. | Moderate. A hash label and a policy field. | Lenny's runtime revisions already create a pod set per revision. The hash rule would apply to non-revision settings only. |
| Refill pacing (`--sandbox-warm-pool-max-refill-rate`, `--sandbox-warm-pool-replenish-delay`) and the API priority-and-fairness guide ([configuration.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/configuration.md), [apf-insulation.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/apf-insulation.md)) | Protects the API server during refill bursts, which §4.6.1 **API server rate limiting** and **etcd write pressure at scale** address separately. | Small. | None. |
| Crash-loop backoff for pod recreation ([KEP-729](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/729-opt-in-pod-recreation-on-failure/README.md)) | A tested pattern for warm-pod create backoff. | Small. | None. Lenny's `warmup_failure.go` may already cover this **[unverified]**. |
| Nitro Sandboxes ([Nitro example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/nitro-eks-sandbox)) | A further RuntimeClass for the `microvm` class on EKS. | Testing and documentation. | Lenny's Kata-specific paths (block-plain volumes, `kata.volumeGid`, and project quotas on virtio-fs) do not apply as written **[unverified]**. |
| Hyperlight ([Hyperlight example](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/hyperlight-sandbox)) | Fast micro-VM start for a single process. | High. | It uses no RuntimeClass, and its unikernel guest runs one process. That conflicts with Lenny's multi-container pod (adapter, `netagent`, and slot containers). |
| Claim-time identity and network-policy attach (roadmap) | Not available yet. | n/a | Lenny already binds identity per session through credential leases and per-slot attribution. |
| Performance methodology ([performance assessment](https://agent-sandbox.sigs.k8s.io/docs/performance-assessment/)) | Comparable numbers for controller throughput. | Small. | None. |

---

## 5. Options for the dependency

### 5.1 Evidence that applies to every option

- The spec says Lenny is "built on" agent-sandbox (§4.6.1 **Implementation:**). The code has no dependency on it. The spec's own API-group paragraph already registers every kind under `lenny.dev` and calls the fallback "replacing agent-sandbox with custom controllers" (§4.6.1 **API group:**). The tree is in the fallback state today.
- The shared names hide divergent schemas. Section 2.2 shows that `SandboxClaim` and `SandboxTemplate` have no field in common with upstream beyond standard metadata, and that `SandboxWarmPool` shares only the idea of a template reference.
- The benefits the spec lists for building on upstream are not realized:
  - Lenny still maintains its controllers.
  - The GKE snapshot support upstream offers lives in the Python client, and the controllers contain no snapshot logic.
  - Upstream's claim model differs from the claim model the spec specifies.
- The spec's statement that agent-sandbox "is a v0.x project" (§4.6.1 **Dependency pinning and upgrade policy:**) is outdated. Upstream is at `v1.0.11` with a `v1beta1` API.

### 5.2 Option (a): describe Lenny's own implementation and track upstream

**For:**

- It matches the code. No migration is needed.
- It removes spec obligations that no code honours: pinning, the v0.x upgrade policy, the go/no-go gate, and ADR-007's premise about upstream claim semantics.
- Lenny keeps its single-namespace, tenant-pinned, slot-based model and its Postgres-authoritative claim path, none of which upstream's API expresses.

**Against:**

- Lenny continues to maintain about 10,000 lines of controller code in `pkg/controller/warmpool` and `pkg/controller/sandbox`, which it maintains today in any case.
- Upstream fixes and performance work arrive only by deliberate porting.
- The kinds keep upstream names with different meanings. An operator who also runs upstream would see `sandboxes.agents.x-k8s.io` next to `sandboxes.lenny.dev`. The kinds coexist technically because the groups differ, and the shared names will confuse readers.

**Spec text that would change under (a):**

- §4.6.1 **Implementation:** Replace the "Built on `kubernetes-sigs/agent-sandbox`" paragraph with a statement that Lenny implements its own controllers modelled on agent-sandbox's resources and tracks upstream.
- §4.6.1 **Internal abstraction layer:**
  - The phrase "never directly with agent-sandbox CRD types".
  - The sentence "Default implementations: `AgentSandboxPodLifecycleManager` and `AgentSandboxPoolManager`, which delegate to agent-sandbox CRDs".
  - The closing sentence about "a breaking upstream change or a decision to replace agent-sandbox".
  - Decide whether `SnapshotPod` and `RestoreFromSnapshot` stay as reserved extension points.
- §4.6.1 **CRD mapping from agent-sandbox:**
  - The heading.
  - The **API group:** paragraph, which mentions the "fallback plan".
  - The table header "Agent-Sandbox CRD".
  - The `SandboxWarmPool` row, "Upstream warm pool management".
- §4.6.1 ADR-007 paragraph: "verify that `SandboxClaim`'s optimistic-locking semantics match Lenny's claim approach". The tests it names still apply to Lenny's own claim. The upstream framing goes.
- §4.6.1 **Dependency pinning and upgrade policy:**, **Go/no-go criteria for the agent-sandbox dependency:**, and **Fallback plan if agent-sandbox is abandoned or diverges:** These are removed, or replaced with a short upstream-tracking paragraph.
- §3 High-Level Architecture: the diagram label "Warm Pool Controller (pod lifecycle, agent-sandbox CRDs)".
- §17.1 Kubernetes Resources: the Warm Pool Controller row, "(default implementation: `kubernetes-sigs/agent-sandbox` CRDs)".
- §17.6 **Checks performed** (preflight): the "Agent-sandbox CRDs" row, "all four `agent-sandbox` CRDs". The chart ships a fifth `lenny.dev` CRD, `Runtime`.
- §18.2 Phase Map: the "Phase 1 (agent-sandbox)" cell.
- §18.6 Phase 2: the deliverable "Documented upstream-contribution cadence for `kubernetes-sigs/agent-sandbox`" and the **Prerequisites.** sentence about the dependency health assessment.
- §18.9 Phase 3: "`RuntimeProvider` abstraction … default implementation wraps `kubernetes-sigs/agent-sandbox`".
- §18.40 Hard Prerequisite Chain: item 2, "Phase 1 → Phase 2 (agent-sandbox dependency health)".
- §19 Resolved Decisions: row 4, "Controller framework".
- §23 Competitive Landscape: the `kubernetes-sigs/agent-sandbox` row, "adopted as Lenny's infrastructure layer".

**Non-spec carriers that would follow:**

- `README.md`.
- In `docs/`:
  - `docs/index.md`
  - `docs/getting-started/architecture.md`
  - `docs/reference/glossary.md` (the Sandbox and Warm Pool Controller entries)
  - `docs/operator-guide/installation.md`
  - `docs/tutorials/deploy-to-cluster.md`
  - `docs/adr/index.md` (ADR-0004, "Planned")
  - `docs/adr/0007-sandboxclaim-optimistic-locking.md`
  - `docs/adr/0008-open-source-license.md`
  - `docs/about/governance.md`
  - `docs/assets/diagrams/architecture-overview.svg`
- In the code, the `AgentSandbox*` type names and the comment in `pkg/podlifecycle/agentsandbox.go` that calls them "the agent-sandbox types".

### 5.3 Option (b): adopt upstream `v1beta1` as a real dependency

**What would map:**

- Lenny's pod renderer could emit a PodSpec into an upstream `SandboxTemplate.podTemplate`, and upstream's Sandbox controller would create and watch the pod.
- Lenny could add its finalizer, labels, and annotations to upstream objects.
- `networkPolicyManagement: Unmanaged` would keep Lenny's own policies.
- An explicit `automountServiceAccountToken` would keep Lenny's projected token.

**What would not map:**

- **Claims.**
  - Upstream's `SandboxClaim` requires `warmPoolRef`, and the controller picks the sandbox. Lenny's gateway picks a specific pod because of tenant pinning, the reserved hold, and the host-schedulable label (§4.6.1 **Pod claim mechanism**, §5.2 **Pinned idle inventory.**).
  - Upstream transfers ownership on claim and replenishes the pool. Lenny's recycled pod returns to the pool, and upstream has no return path.
  - Lenny would bypass upstream's claim and keep a claim kind of its own.
- **State machine.** Upstream `Sandbox.status` has no phase. Lenny's projected phase (§6.2) would need another carrier: a Lenny CRD, labels, or annotations.
- **Pool sizing.** Upstream's `replicas` counts unclaimed members only. Lenny's `minWarm` and `maxWarm` logic, reserved pods, and pinned inventory would sit outside it, and Lenny would write `replicas` from the PoolScalingController.
- **Default network policy.** The managed default admits ingress only from the sandbox router (section 1.2), which Lenny does not run.

**Costs:**

- Lenny's Go toolchain and Kubernetes libraries would have to move from Go 1.25, k8s v0.31, and controller-runtime v0.19 to Go 1.26, v0.37, and v0.25 to import the types ([go.mod](https://github.com/kubernetes-sigs/agent-sandbox/blob/v1.0.11/go.mod)).
- A cluster holds one version of a CRD. If Lenny installed `agents.x-k8s.io` CRDs, Lenny and any other upstream user in the cluster would share one CRD version and one controller. The `lenny.dev` group avoids that today (§4.6.1 **API group:**).
- Upstream releases weekly and patch releases can carry action-required changes (section 1.10).

**Extension points:** Upstream has no plugin interface in its controllers. The available hooks are the PodSpec itself, labels and annotations, admission webhooks, and watching upstream objects. The adoptable label covers adoption of unowned Services and PVCs. The portable-backend KEP for a runtime gRPC contract (#597) closed unmerged. KEP-539.2, which merged, standardizes the in-pod runtime API (`sandboxd`) and leaves the controller unchanged ([PR #597](https://github.com/kubernetes-sigs/agent-sandbox/pull/597), [PR #747](https://github.com/kubernetes-sigs/agent-sandbox/pull/747)).

**What Lenny would gain:**

- Upstream maintenance of the Sandbox-to-Pod step, including Ready semantics, the `PodScheduled` mirror, failure recreation, and metrics.
- Labels that GKE snapshot policies key on (`agents.x-k8s.io/sandbox-name-hash`). Lenny could stamp its own labels for the same purpose without the dependency.

**Assessment:** Under (b), upstream would own the Sandbox-to-Pod step, which is the layer the spec says least about. Lenny would keep the claim, recycle, hold, projection, scaling, and pinning logic that the spec specifies in detail. The share of `pkg/controller/sandbox` that upstream would replace is the pod create and observe loop. The pod rendering in `podspec/`, nonce-only handling, supervision, SDK-warm handling (until removed), and phase projection would remain.

### 5.4 Middle paths

- **Field-name alignment.** Rename where the semantics already match, so that tooling and readers carry over:
  - `status.podIP` to `podIPs`.
  - `templateRef` to `sandboxTemplateRef.name`.
  - `Ready` and `PodScheduled` condition types and reasons, which follow the same conventions.

  This option is cheap while the API is `v1alpha1` and Lenny has no deployments to keep compatible. It gives no runtime compatibility, because the claim and template schemas stay different.
- **Rename Lenny's kinds.** Use names that do not collide with upstream, such as `AgentPod`, `AgentPool`, and `PodClaim`. The spec's own mapping table records that these kinds once replaced `AgentPool`, `AgentPod`, and `AgentSession`. This option removes the naming confusion described under (a). It touches the spec, the code, the chart, the RBAC, the webhooks, and the documentation.
- **Contribute upstream.** Upstream plans "API Support for Multi-Sandbox per Pod" without a design. Lenny's slot model, per-slot release, and return-to-pool semantics are candidate inputs. Upstream's stated focus is singleton sandboxes and RL and batch scale ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md), [KEP-1434](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/keps/1434-multi-cluster-fleet/README.md)). Acceptance of a multi-tenant slot model is **[unverified]**.
- **Upstream core only.** Use upstream's `Sandbox` for pod creation and keep Lenny's own pool and claim kinds. This option carries most of the costs of (b): the toolchain, the shared CRD version, and the missing phase carrier. Its benefit is limited to the pod create and observe loop.
- **A tracking checklist in place of the go/no-go gate.** Review upstream each quarter for a fixed set of items:
  - A snapshot API in the controller.
  - Multi-sandbox per pod.
  - Return-to-pool semantics.
  - Claim-time identity.
  - Changes to the warm-pool update strategy.

### 5.5 Recommendation

Option (a) with field-name alignment fits the evidence:

- Lenny's model and upstream's model diverge at the claim, which is the core of both designs.
- The divergence is structural. Several sessions per pod, return to pool, tenant pinning, and a Postgres-authoritative binding are absent from upstream's API, and the one related upstream item ("Multi-Sandbox per Pod") is only planned.
- Adopting upstream would replace the thinnest layer of Lenny's controller, at the cost of a toolchain jump, a shared cluster-wide CRD version, and a weekly upstream cadence.

Reconsider (b) if upstream lands multi-sandbox per pod with a return-to-pool path, or moves snapshots into the controller. Renaming the kinds is a separate decision for the owner. It removes a source of confusion and touches the whole tree.

---

## 6. Gaps and risks found in Lenny's spec and code

1. **Stale dependency premise.** §4.6.1 **Dependency pinning and upgrade policy:** says agent-sandbox "is a v0.x project". Upstream is at `v1.0.11` with a `v1beta1` API (section 1.1).
2. **The spec describes a dependency that does not exist.** §4.6.1 **Implementation:** says Lenny is "built on" agent-sandbox and gets "upstream community maintenance, Pod Snapshots on GKE". `go.mod` has no such module, and upstream snapshot support is a Python client feature that the controller does not use (section 1.4).
3. **Mapping table: `SandboxTemplate` row.** The §4.6.1 **CRD mapping from agent-sandbox:** row says `SandboxTemplate` declares "warm count range, scaling policy". In the code, `minWarm`, `maxWarm`, and `scalePolicy` are on `SandboxWarmPool` (`pkg/apis/lenny/v1alpha1/sandboxwarmpool_types.go`). `SandboxTemplate` has neither field.
4. **Mapping table: `SandboxWarmPool` row.** The row says `SandboxWarmPool` is "Upstream warm pool management with configurable `minWarm`/`maxWarm`". Upstream's warm pool has `replicas` and no `minWarm` or `maxWarm` (section 1.2). Lenny's warm pool is its own type.
5. **ADR-007 premise.** The §4.6.1 ADR-007 paragraph asks to verify that "`SandboxClaim`'s optimistic-locking semantics match Lenny's claim approach". The premise assumes an upstream claim that Lenny uses. Lenny's claim is its own type with gateway-created deterministic names, so the verification is of Lenny's own mechanism.
6. **CRD count in preflight and docs.** §17.6 **Checks performed** and `docs/tutorials/deploy-to-cluster.md` refer to "all four `agent-sandbox` CRDs". The chart ships five `lenny.dev` CRDs, including `Runtime` (`charts/lenny/crds/lenny.dev_runtimes.yaml`). Whether preflight checks `Runtime` was not traced **[unverified]**.
7. **Pool settings on the CRD versus in the spec.**
   - §5.2 and §4.6.1 name `sessionPolicy.maxConcurrentSessions` and `recycle.*` settings such as `enabled`, `maxSessionsPerPod`, and `maxPodUptimeSeconds`.
   - The designs rename the first to `maxConcurrentSessionsPerPod` (agent-pod-layout PL-9).
   - The `SandboxTemplate` CRD carries `sessionPolicy.recycle` with only `scrubProfile`, `acknowledgeMicrovmResidualState`, and `runtimeProcess`, plus a top-level `maxConcurrent` that the code comment describes as "the per-pod slot count for service mode".
   - The other settings live in the Postgres pool record and in annotations (`pkg/apis/lenny/v1alpha1/annotations.go`, `pkg/agentpodstate`).
   - The spec does not state which pool settings the CRD projects. A reader of §4.6.1 would expect `sessionPolicy.maxConcurrentSessions` on the template.
8. **Resource class enum against comment.** `SandboxTemplate.spec.resourceClass` is an enum of `small`, `medium`, and `large` in both the type and the CRD. The `Sandbox.spec.resourceClass` comment says "small, medium, large, or a deployer-defined class". One of them is wrong, or deployer-defined classes cannot reach a template.
9. **Interface names imply delegation.** In `pkg/podlifecycle/agentsandbox.go`, the `AgentSandbox*` names and the comment "agent-sandbox types are the v1 PoolReader…" describe `lenny.dev` types as upstream types.
10. **Naming collision.** Lenny's `SandboxClaim` and upstream's `SandboxClaim` have opposite semantics (section 2.2). Operators, docs, and the glossary entry that calls `Sandbox` "the `kubernetes-sigs/agent-sandbox` Custom Resource Definition" (`docs/reference/glossary.md`) will mislead readers who know upstream.
11. **Designs record themselves as unbuilt.** The session-isolation, agent-pod-layout, placement, and tenant-derived-runtimes designs each say nothing in them is built. The CRDs at this commit still have `sdkWarmDisabled`, `sdkWarmCircuitBreaker`, `recycle.runtimeProcess`, and the pre-rename fields. The comparison above treats the designs as implemented, as the brief asks. A spec rewrite under (a) should land after or with those designs so the new §4.6.1 text describes the final fields.
12. **Snapshot extension point.** §4.6.1 reserves `SnapshotPod` and `RestoreFromSnapshot` for GKE Pod Snapshots. Section 4.1 lists conflicts with the slot model, with per-slot secrets on memory volumes, and with pinned broker roots. If the reservation stays, the spec should state that it applies to whole-pod snapshots of warm pods only, or remove it.
13. **PDB rationale.** §4.6.1 **Disruption protection for agent pods** explains the integer `minAvailable` by the absence of a scale subresource on `Sandbox`. That holds for both Lenny's and upstream's `Sandbox` (section 1.2), so the rationale is unaffected by the dependency decision.

## Sources not fully read

- The Kubernetes blog page could not be rendered in full through the web fetcher. Its source file was read from `kubernetes/website` instead ([blog source](https://github.com/kubernetes/website/blob/main/content/en/blog/_posts/2026/agent-sandbox.md)).
- The Google Open Source blog post was seen only as a search result. Its launch date of November 2025 comes from the URL path.
- The GKE Pod snapshots page was read through a summarising fetcher, so quoted limits should be checked against the page before they are cited in the spec.
