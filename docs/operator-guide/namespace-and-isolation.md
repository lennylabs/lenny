---
layout: default
title: Namespace and Isolation
parent: "Operator Guide"
nav_order: 3
---

# Namespace and Isolation

{: .note }
> **Reviewing Lenny for security or compliance? Start at [Security Principles](security-principles) §1.7 and §2.** This page is the operator-facing configuration reference for the three isolation profiles (`standard`/`sandboxed`/`microvm`), PSS enforcement, RuntimeClass admission, node dedication, and monotonicity enforcement.

This page covers namespace layout, Pod Security Standards enforcement, RuntimeClass-aware admission policies, node isolation for Kata (microvm) workloads, and ResourceQuota/LimitRange governance.

---

## Namespace Layout

Lenny uses three namespaces by default:

```
lenny-system/           # Gateway, token service, controllers, stores
lenny-agents/           # Agent pods (runc + gVisor isolation)
lenny-agents-kata/      # Kata pods (dedicated node pool, microVM isolation)
```

| Namespace | Contents | Isolation Level |
|---|---|---|
| `lenny-system` | Gateway replicas, Token Service, Warm Pool Controller, PoolScalingController, PgBouncer | Platform infrastructure -- standard Kubernetes security |
| `lenny-agents` | Agent pods using `standard` (runc) or `sandboxed` (gVisor) RuntimeClass | Default-deny NetworkPolicy; RuntimeClass-aware admission |
| `lenny-agents-kata` | Agent pods using `microvm` (Kata) RuntimeClass | Default-deny NetworkPolicy; dedicated node pool required |

Additional agent namespaces can be added via `.Values.agentNamespaces`. The Helm chart renders NetworkPolicies and admission policies into each namespace.

---

## Pod Security Standards

### Split Enforcement Model

The agent namespaces use a **split enforcement model** based on RuntimeClass. Namespace-level PSS `enforce` mode is **not** used because PSS enforcement is namespace-scoped and cannot distinguish between RuntimeClasses.

Instead, PSS labels are set to `warn` and `audit` only:

```bash
kubectl label namespace lenny-agents \
  pod-security.kubernetes.io/warn=restricted \
  pod-security.kubernetes.io/audit=restricted

kubectl label namespace lenny-agents-kata \
  pod-security.kubernetes.io/warn=restricted \
  pod-security.kubernetes.io/audit=restricted
```

Enforcement is handled by RuntimeClass-aware admission policies (OPA Gatekeeper or Kyverno).

### Why Not Namespace-Level Enforce?

Using `pod-security.kubernetes.io/enforce=restricted` at the namespace level would cause:

1. **gVisor pods:** The `seccompType: RuntimeDefault` requirement is meaningless under gVisor (gVisor intercepts syscalls in userspace). Enforcing it would silently reject valid gVisor pods.
2. **Kata pods:** Some Kata device plugins require relaxed `allowPrivilegeEscalation` constraints that conflict with Restricted PSS.
3. **Warm pool deadlock:** With namespace-level enforce, non-compliant pods are silently rejected by the API server. The controller recreates the pod, which is rejected again, causing a tight loop with no pods ever reaching `idle` state.

### Per-RuntimeClass Security Properties

| Control | runc (standard) | gVisor (sandboxed) | Kata (microvm) |
|---|---|---|---|
| Non-root UID | Enforced | Enforced | Enforced |
| All capabilities dropped | Enforced | Enforced | Enforced |
| Read-only root filesystem | Enforced | Enforced | Enforced |
| seccompType: RuntimeDefault | Enforced | Skipped (no-op under gVisor) | Skipped (Kata device plugins) |
| allowPrivilegeEscalation | false | false | Relaxed for device plugins |
| shareProcessNamespace | false (enforced by webhook) | false (enforced by webhook) | false (enforced by webhook) |

---

## Admission Policies

### Required Components

Either **OPA Gatekeeper** or **Kyverno** is required for production deployments. The Helm chart includes admission policy manifests for both, deployed under `templates/admission-policies/`.

### Policy Manifests

The chart deploys the following admission policies:

1. **Full Restricted PSS enforcement for runc pods** -- Validates all Restricted PSS constraints for pods referencing the `standard` (runc) RuntimeClass.

2. **RuntimeClass-specific relaxed enforcement for gVisor and Kata** -- Applies per-RuntimeClass constraints that preserve the same security properties while accommodating runtime-specific requirements.

3. **`shareProcessNamespace: false` validation** -- Rejects pods in agent namespaces with `shareProcessNamespace: true`.

4. **Label-based namespace targeting** -- Policies target namespaces listed in `.Values.agentNamespaces`.

5. **`lenny-label-immutability` webhook** -- Enforces immutability of security-critical labels (`lenny.dev/managed`, `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`). Only the warm pool controller ServiceAccount can set these at pod creation.

6. **`lenny-tenant-label-immutability` webhook** -- Prevents mutation of the `lenny.dev/tenant-id` label to a different non-empty value on existing pods.

7. **`lenny-sandboxclaim-guard` webhook** -- Prevents double-claim of pods by intercepting `CREATE`, `PATCH`, and `PUT` on `SandboxClaim` resources.

8. **`lenny-ephemeral-container-cred-guard` webhook** -- Scoped to the `pods/ephemeralcontainers` subresource. Rejects `kubectl debug`-style ephemeral-container attach requests on any of four conditions: reuse of the adapter UID or agent UID, inclusion of the `lenny-cred-readers` GID in `runAsGroup`/`supplementalGroups`, absent `runAsUser`/`runAsGroup`/`supplementalGroups` fields (which would inherit pod-level defaults including fsGroup), or a `volumeMounts` entry that references the credential tmpfs volume by name or mounts anything at the `/run/lenny` directory prefix. The fourth condition closes the fsGroup-inheritance side-channel that the first three cannot close on their own — fsGroup is applied by kubelet to every container regardless of explicit securityContext values, so rejecting the credential volumeMount is the cleanest barrier to credential-file access.

### Webhook Configuration

All admission policy webhooks **must** use `failurePolicy: Fail` (fail-closed):

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: lenny-admission-policies
webhooks:
  - name: admission.lenny.dev
    failurePolicy: Fail    # Fail-closed -- pods rejected if webhook unavailable
    # ...
```

**Webhook HA requirements:**

- `replicas: 2` (configurable via `.Values.admissionController.replicas`)
- `PodDisruptionBudget` with `minAvailable: 1`
- Availability SLO: 99.9% (rolling 30-day window)

The `AdmissionWebhookUnavailable` alert fires when any admission webhook has been unreachable for more than 30 seconds.

### Integration Test Suite

An integration test suite (`tests/integration/admission_policy_test.go`) verifies that controller-generated pod specs for each RuntimeClass pass the deployed admission policies. This prevents policy/spec drift from causing warm pool deadlock.

---

## Node Isolation for Kata

Kata (microvm) pods **must** run on dedicated node pools to prevent kernel-level escape from runc pods on shared nodes.

### Required Controls

**1. RuntimeClass `nodeSelector`:**

The `kata-microvm` RuntimeClass definition must include:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: kata-microvm
scheduling:
  nodeSelector:
    lenny.dev/node-pool: kata
handler: kata-qemu
```

**2. Hard node affinity:**

The controller injects a `requiredDuringSchedulingIgnoredDuringExecution` affinity rule on every Kata pod:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: lenny.dev/node-pool
              operator: In
              values: ["kata"]
```

**3. Dedicated-node taint:**

Kata node pools must carry a taint to prevent non-Kata workloads:

```bash
kubectl taint nodes -l lenny.dev/node-pool=kata \
  lenny.dev/isolation=kata:NoSchedule
```

### Why All Three Controls?

| Control | Prevents | Limitation |
|---|---|---|
| RuntimeClass nodeSelector | Non-Kata pods on Kata nodes | Does not prevent Kata pods on non-Kata nodes |
| Hard node affinity | Kata pods on non-Kata nodes | `IgnoredDuringExecution` -- does not evict if label is removed |
| Dedicated-node taint | Any non-tolerated pod on Kata nodes | Does not prevent Kata pods going elsewhere |

Together, these three controls guarantee that Kata pods run exclusively on Kata nodes and no non-Kata workloads can be scheduled onto those nodes.

---

## ResourceQuota and LimitRange

### ResourceQuota

Each agent namespace includes a `ResourceQuota` to prevent runaway pod creation:

```yaml
agentNamespaces:
  - name: lenny-agents
    resourceQuota:
      pods: 200              # Total pod limit
      requests.cpu: "400"    # Aggregate CPU requests
      requests.memory: "800Gi"  # Aggregate memory requests
```

Defaults are derived from the expected warm pool size with a 2x safety margin. **Operators must tune these values** when configuring large `minWarm` pools -- if the quota is lower than the pool's target size, the warm pool controller cannot create pods.

The preflight Job validates that:
- `ResourceQuota` exists in each agent namespace
- The pod limit is at least as large as the sum of `minWarm` across all pools targeting that namespace

### LimitRange

Each agent namespace includes a `LimitRange` to prevent BestEffort QoS pods:

```yaml
agentNamespaces:
  - name: lenny-agents
    limitRange:
      defaultRequest:
        cpu: "250m"
        memory: "256Mi"
      default:
        cpu: "2"
        memory: "2Gi"
```

These defaults apply only to containers that do not specify their own resource requirements. Controller-generated pods already include explicit resource requests, so the `LimitRange` acts as a safety net for any manually created or misconfigured pods.

---

## Network Isolation

Agent namespaces use a strict default-deny networking model. See [Security](security.html) for the full NetworkPolicy specification.

**Summary of applied policies:**

| Policy | Scope | Purpose |
|---|---|---|
| `default-deny-all` | All pods in agent namespaces | Denies all ingress and egress by default |
| `allow-gateway-ingress` | All managed pods | Allows gateway to reach pod adapter (port 50051) |
| `allow-pod-egress-base` | All managed pods | Allows pod-to-gateway gRPC and DNS |
| `allow-pod-egress-llm-proxy` | Proxy-mode pods only | Allows access to LLM proxy port (8443) |
| `allow-pod-egress-otlp` | All managed pods (when OTLP configured) | Allows OTLP trace export |
| `allow-pod-egress-internet` | Internet-egress pools only | Allows external network access with CIDR exclusions |

---

## Tenant Isolation at the Pod Level

### Tenant Pinning

All execution modes pin pods to a single tenant:

- **Session mode:** One session per pod; pod inherits the session's tenant
- **Task mode:** Tenant ID recorded on first assignment; subsequent requests verified
- **Concurrent mode:** Tenant pinned on first slot assignment

Cross-tenant pod reuse is only permitted with `microvm` isolation and explicit `allowCrossTenantReuse: true`.

### Tenant Label Immutability

The `lenny-tenant-label-immutability` webhook enforces immutability of the `lenny.dev/tenant-id` label:

| Transition | Permitted | Authorized ServiceAccount |
|---|---|---|
| unset to `{tenant_id}` | Yes | `lenny-gateway` |
| `{tenant_id}` to `unassigned` | Yes | `lenny-controller` |
| `{tenant_id}` to different `{tenant_id}` | **No** | N/A -- always rejected |

---

## Isolation Profile Summary

| Profile | Runtime | Isolation Boundary | Use Case |
|---|---|---|---|
| `standard` | runc | Linux cgroups + namespaces | Development, low-risk workloads |
| `sandboxed` | gVisor (runsc) | Userspace syscall interception | **Default for production** |
| `microvm` | Kata Containers | Full VM boundary per pod | High-risk, semi-trusted code, cross-tenant reuse |

**Recommendation:** Use `sandboxed` (gVisor) as the default isolation profile for all production workloads. Use `microvm` (Kata) only for workloads that require a VM-level isolation boundary.
