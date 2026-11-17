---
code: KPSET
type: interface
summary: >-
  PodSet is the custom resource that records one pod set of a pool: the pool settings the pod
  builder and the webhooks read, the warm targets, and the observed counts and conditions. This
  page states its fields with their types, constraints, and owners, its status and conditions,
  its validation, and the admission outcomes a Kubernetes client sees.
owns: [custom-resources, errors]
depends-on: [K8S, POOL, PSC, WPC, WEBHOOK, KLABEL]
---

# `PodSet`

`PodSet` is the custom resource that records one pod set of a pool: the pool settings the pod builder and the webhooks read, the warm targets, and the observed counts and conditions. This page states its fields with their types, constraints, and owners, its status and conditions, its validation, and the admission outcomes a Kubernetes client sees.

## Terms {#terms}

| Term | Gloss |
| :-- | :-- |
| Pod set | The warm pods of one pool built for one pod-set key ([Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#pod-set)). |
| Pod-set key | The digest that identifies a pod set within its pool ([Runtime revisions and rollouts](../../capabilities/runtimes-and-pools/runtime-revisions.md#pod-set-key)). |
| Pool record | A pool's row in Postgres ([Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#pool)). |

## Overview {#overview}

A pool has one `PodSet` outside a rollout and one per pod-set key during one. The PoolScalingController writes each `PodSet` from the pool record, and the WarmPoolController reports on it. An operator reads a pool's settings and its warm counts with `kubectl get podsets -l lenny.dev/pool=<pool>` and does not write a `PodSet`; the admin API is the write path ([Admin API pools](../admin-api/pools.md)).

## Conventions {#conventions}

The conventions of [Lenny's Kubernetes API](index.md#conventions) apply.

## Resource {#resource}

```lenny-def
kind: custom-resource
name: PodSet
group: lenny.dev
version: v1alpha1
plural: podsets
crd: podsets.lenny.dev
short-name: pset
scope: Namespaced (agent namespace)
summary: One pod set of a pool, with its pool settings, warm targets, counts, and conditions.
```

**Printer columns.** Runtime (`.spec.runtimeRef`), Isolation (`.spec.isolationProfile`), MinWarm (`.spec.minWarm`), MaxWarm (`.spec.maxWarm`), Warm (`.status.warmCount`), Ready (`.status.readyCount`).

### Metadata {#metadata}

| Field | Type and constraint | Owner | Behind it |
| :-- | :-- | :-- | :-- |
| `metadata.name` | `<pool>.<key16>`; a DNS subdomain of at most 80 characters | PoolScalingController | [POOL-R4](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r4) |
| label [`lenny.dev/pool`](labels-and-annotations.md#lenny-dev-pool) | Required; immutable; the pool name | PoolScalingController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| label [`lenny.dev/pod-set`](labels-and-annotations.md#lenny-dev-pod-set) | Required; immutable; equal to the first 16 characters of `spec.podSetKey` | PoolScalingController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| annotation [`lenny.dev/config-generation`](labels-and-annotations.md#lenny-dev-config-generation) | Integer as a string | PoolScalingController | [POOL-R23](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r23) |

### Spec {#spec}

The PoolScalingController owns every `spec` field and writes them in one apply. Each field's meaning is owned by the capability linked in the last column.

| Field | Type and constraint | Meaning owned by |
| :-- | :-- | :-- |
| `runtimeRef` | String; required; a runtime name | [Runtime registry](../../capabilities/runtimes-and-pools/runtime-registry.md#runtime-name) |
| `podRuntimeRef` | String; the base runtime on a derived runtime's pool | [Tenant-derived runtimes](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md#owned-pool) |
| `runtimeRevision` | Integer; the lowest-numbered revision with `podHash` | [Runtime revisions and rollouts](../../capabilities/runtimes-and-pools/runtime-revisions.md#pods) |
| `podHash` | String; the revision's pod hash | [Runtime revisions and rollouts](../../capabilities/runtimes-and-pools/runtime-revisions.md#pod-hash) |
| `podSetKey` | String; 64 lower-case hexadecimal characters; immutable | [Runtime revisions and rollouts](../../capabilities/runtimes-and-pools/runtime-revisions.md#pod-set-key) |
| `ownerTenantId` | String; set on an owned pool; immutable | [Tenant-derived runtimes](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md#owned-pool) |
| `isolationProfile` | Enum of the isolation profiles | [Isolation profiles](../../capabilities/runtimes-and-pools/isolation-profiles.md) |
| `resourceClass` | String; a class defined in the deployment's class configuration | [Resource classes](../../capabilities/runtimes-and-pools/resource-classes.md) |
| `sessionPolicy.maxConcurrentSessionsPerPod` | Integer; `>= 1` | [Agent pod lifecycle](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `sessionPolicy.acknowledgeSharedPod` | Boolean | [Slots](../../capabilities/sessions/slot-assignment-and-failure.md#shared-pod) |
| `sessionPolicy.recycle` | Object: `enabled`, `acknowledgeBestEffortScrub`, `maxSessionsPerPod` (`> 0` when `enabled`), `maxPodUptimeSeconds`, `maxScrubFailures`, `onScrubFailure`, `allowCrossTenantReuse` | [Agent pod lifecycle](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `minWarm` | Integer; `>= 0`; `<= maxWarm` | [Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `maxWarm` | Integer; `> 0` | [Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `scalePolicy` | Object: `podWarmupSecondsBaseline`, `bootstrapMinWarm`, `schedules`, `scaleToZero` | [Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `reservedPods` | Integer; `>= 0`; 0 outside an owned pool | [Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r21) |
| `maxOwnedPods` | Integer; the owner's pod bound, on an owned pool | [Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r22) |
| `topologySpreadConstraints` | List of Kubernetes `TopologySpreadConstraint` | [Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r17) |
| `placement` | The resolved placement | [Placement](../../capabilities/runtimes-and-pools/placement.md#rendering) |
| `warmList` | The rendered warm repository stores | [Runtime revisions and rollouts](../../capabilities/runtimes-and-pools/runtime-revisions.md#warm-list-per-revision) |
| `egressProfile`, `dnsPolicy` | Enums | [Egress](../../capabilities/network-and-egress/egress-policy.md) |
| `spiffeBinding`, `acknowledgeNonceOnlyAuth` | Enum; boolean | [Adapter authentication](../channels/gateway-to-pod.md) |
| `maxSessionAgeSeconds`, `checkpointCadenceSeconds`, `checkpointBarrierAckTimeoutSeconds`, `terminationGracePeriodSeconds`, `maxTerminationGracePeriodSeconds`, `workspaceSizeLimitBytes` | Integers | [Session lifecycle](../../capabilities/sessions/session-lifecycle/index.md), [Checkpoints](../../capabilities/sessions/checkpoints/checkpoint-capture.md) |

<!-- unresolved: the field list follows the tree's `SandboxTemplateSpec` and `SandboxWarmPoolSpec` with the changes PLC-4 "Fields after the designs." lists; the volume size settings of PL-9, the slot memory settings, and the field names of the resolved placement, the warm list, and the pod bound are not named by any design. -->

**Removed fields.** `templateRef`, `executionMode`, `maxConcurrent`, `deliveryMode`, `workspaceTier`, `sdkWarmDisabled`, `scalePolicy.sdkConnectTimeoutSeconds`, `scalePolicy.sdkWarmCircuitBreakerMinOpenSeconds`, and `sessionPolicy.recycle.runtimeProcess` are not part of the schema, and the API server refuses a write that names one.

### Status {#status}

| Field | Type | Owner | Meaning |
| :-- | :-- | :-- | :-- |
| `warmCount` | Integer | WarmPoolController | Unpinned pods in `warming` or `idle` ([POOL-R14](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r14)). |
| `readyCount` | Integer | WarmPoolController | Unpinned pods in `idle`. |
| `scalingMode` | Enum: `bootstrap`, `formula` | PoolScalingController (carve-out) | [POOL-R12](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r12). <!-- unresolved: the non-bootstrap value is not named in the sources; `formula` is assumed. --> |
| `bootstrapHoursOfData` | Integer | PoolScalingController (carve-out) | Hours of demand history the bootstrap convergence has observed. |
| `observedGeneration` | Integer | WarmPoolController | The `metadata.generation` last reconciled. |
| `conditions` | List of Kubernetes conditions | WarmPoolController | See below. |

### Conditions {#conditions}

| Type | Reasons | Set when |
| :-- | :-- | :-- |
| `PoolWarmingUp` | `Provisioning`, `Drained` | [POOL-R18](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r18). |
| `PoolDrained` | <!-- unresolved: no source states its reasons or trigger beyond its name. --> | The pod set's pool is draining. |
| `Degraded` | `PodSetKeyCollision`; `RuntimeClassMissing` <!-- unresolved: §4.6.1 says "Degraded when RuntimeClass missing" without a reason string. --> | [POOL-R4](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r4); the pool's RuntimeClass does not exist. The PoolScalingController sets the `PodSetKeyCollision` reason. |
| `SecurityDegradedMode` | | The pod set renders nonce-only pods ([Adapter authentication](../channels/gateway-to-pod.md#nonce-only)). |
| `PlacementRefused` | The rejection label | A pod of the pod set was refused at placement admission ([Placement](../../capabilities/runtimes-and-pools/placement.md#enforcement)). |
| `PlacementUnschedulable` | The scheduler's reason | A pod of the pod set has no node to run on ([Placement](../../capabilities/runtimes-and-pools/placement.md#enforcement)). |

```lenny-def
kind: condition-reason
name: PodSetKeyCollision
resource: PodSet
condition: Degraded
summary: A PodSet already holds the computed name for a different pod-set key; the PoolScalingController wrote nothing for the new key (POOL-R4).
```

### Owned objects {#owned-objects}

- The `PodSet` is the controller owner of each of its [`AgentPod`](agentpod.md) objects.
- The `PodSet` owns one PodDisruptionBudget named `<metadata.name>-warm` when `minWarm` is 2 or more ([POOL-R16](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r16)).

## Validation {#validation}

The CRD's CEL rules refuse, with the API server's validation error naming the field:

- `minWarm > maxWarm`, and `maxWarm <= 0`;
- `sessionPolicy.maxConcurrentSessionsPerPod < 1`;
- `sessionPolicy.recycle.maxSessionsPerPod <= 0` when `sessionPolicy.recycle.enabled` is `true`;
- `maxSessionAgeSeconds <= 0`;
- a change to `podSetKey` after creation.

## Admission {#admission}

[`lenny-pool-config-validator`](../../components/admission-webhooks.md#pool-config-validator) and [`lenny-podset-deletion-guard`](../../components/admission-webhooks.md#podset-deletion-guard) review `PodSet` writes. A client sees these outcomes:

| Operation | Refused when | Rejection label | Behind it |
| :-- | :-- | :-- | :-- |
| `CREATE` | The `lenny.dev/pool` label is absent or is not a valid label value. | `PODSET_POOL_LABEL_INVALID` <!-- unresolved: proposed label; no design names one. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `CREATE` | The `lenny.dev/pod-set` label is absent or differs from the first 16 characters of `spec.podSetKey`. | `PODSET_POD_SET_LABEL_INVALID` <!-- unresolved: proposed label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `CREATE` | `metadata.name` is not `<lenny.dev/pool>.<lenny.dev/pod-set>`. | `PODSET_NAME_MISMATCH` <!-- unresolved: proposed label. --> | [POOL-R4](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r4) |
| `UPDATE` | The update changes or removes `lenny.dev/pool` or `lenny.dev/pod-set`. | `PODSET_LABEL_IMMUTABLE` <!-- unresolved: proposed label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `CREATE`, `UPDATE` | The writer of `spec` is not the PoolScalingController's ServiceAccount. The message directs the client to the admin API. | <!-- unresolved: §4.6.3 states the denial without a code. --> | [POOL-R1](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r1) |
| `CREATE`, `UPDATE` | The pool's grace period is below the agent-pod checkpoint floor, or its barrier acknowledgment timeout is below its floor. Applies to every writer. | `tiered_cap_exceeds_grace_budget`, `barrier_ack_below_floor` | [Checkpoints](../../capabilities/sessions/checkpoints/checkpoint-capture.md#grace-budget) |
| `CREATE`, `UPDATE` | The resolved placement escapes its node class. | The placement rejection labels | [Placement](../../capabilities/runtimes-and-pools/placement.md#enforcement) |
| `DELETE` | A runtime rollout that still needs the pod set is in progress. | <!-- unresolved: see the deletion guard's note on the admission webhooks page. --> | [Runtime revisions and rollouts](../../capabilities/runtimes-and-pools/runtime-revisions.md#rollout) |
| Any | The reviewing webhook cannot be reached. | None; the API server's webhook failure message | [Lenny's Kubernetes API](index.md#admission) |

## Compatibility {#compatibility}

`PodSet` replaces the earlier `SandboxTemplate` and `SandboxWarmPool` pair with no alias. The conventions of [Lenny's Kubernetes API](index.md#compatibility) apply.
