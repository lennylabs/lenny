---
code: KPSET
type: interface
area: kubernetes
summary: >-
  PodSet is the custom resource that records one pod set of a pool: the pool settings the pod
  builder and the webhooks read, the warm targets, and the observed counts and conditions. This
  page states its fields with their types, constraints, and writers, its status and conditions,
  its validation, and the admission outcomes a Kubernetes client sees.
owns: [custom-resources, operations]
depends-on: [K8S, POOL, REV, PSC, WPC, WEBHOOK, KLABEL]
---

# `PodSet`

## Terms {#terms}

| Term | Gloss |
| :-- | :-- |
| Pod set | The warm pods of one pool built for one pod-set key ([POOL](../../capabilities/runtimes-and-pools/warm-pools.md#pod-set)). |
| Pod-set key | The digest that identifies a pod set within its pool ([Runtime revisions](../../capabilities/runtimes-and-pools/runtime-revisions.md)). |
| Pool record | A pool's row in Postgres, which is the source of the pool's configuration ([POOL](../../capabilities/runtimes-and-pools/warm-pools.md#pool-record)). |

## Overview {#overview}

A pool has one `PodSet` outside a rollout and one per pod-set key during one. The PoolScalingController writes each `PodSet` from the pool record, and the WarmPoolController reports on it. An operator reads a pool's settings and its warm counts with `kubectl get podsets -l lenny.dev/pool=<pool>` and does not write a `PodSet`; the admin API is the write path ([Admin API pools](../admin-api/pools.md)).

## Conventions {#conventions}

The conventions of [Lenny's Kubernetes API](index.md) apply.

## Resource {#resource}

<a id="crd-podset"></a>

```lenny-def
kind: custom-resource
name: PodSet
group: lenny.dev
version: v1alpha1
plural: podsets
crd: podsets.lenny.dev
short-name: pset
scope: Namespaced (agent namespace)
writers: [PSC, WPC]
readers: [GWCLAIM, WEBHOOK]
summary: One pod set of a pool, with its pool settings, warm targets, counts, and conditions.
```

**Printer columns.** Runtime (`.spec.runtimeRef`), Isolation (`.spec.isolationProfile`), MinWarm (`.spec.minWarm`), MaxWarm (`.spec.maxWarm`), Warm (`.status.warmCount`), Ready (`.status.readyCount`).

### Metadata {#metadata}

| Field | Type and constraint | Writer | Behind it |
| :-- | :-- | :-- | :-- |
| `metadata.name` | A DNS subdomain of at most 80 characters, formed from the pool name and the pod-set label value | PoolScalingController | [POOL-R4](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r4) |
| label [`lenny.dev/pool`](labels-and-annotations.md#label-lenny-dev-pool) | Required; immutable | PoolScalingController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| label [`lenny.dev/pod-set`](labels-and-annotations.md#label-lenny-dev-pod-set) | Required; immutable; equal to the first 16 characters of `spec.podSetKey` | PoolScalingController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| annotation [`lenny.dev/config-generation`](labels-and-annotations.md#annotation-lenny-dev-config-generation) | Integer as a string | PoolScalingController | [POOL-R23](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r23) |

### Spec {#spec}

The PoolScalingController writes every `spec` field in one apply. Each field's meaning, default, and range are owned by the setting or capability in the last column.

| Field | Type and constraint | Meaning owned by |
| :-- | :-- | :-- |
| `runtimeRef` | String; required; a runtime name | [Runtime registry](../../capabilities/runtimes-and-pools/runtime-registry.md) |
| `podRuntimeRef` | String; the base runtime on a derived runtime's pool | [Tenant-derived runtimes](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md) |
| `runtimeRevision` | Integer; the lowest-numbered revision with `podHash` | [Runtime revisions](../../capabilities/runtimes-and-pools/runtime-revisions.md) |
| `podHash` | String; the revision's pod hash | [Runtime revisions](../../capabilities/runtimes-and-pools/runtime-revisions.md) |
| `podSetKey` | String; 64 lower-case hexadecimal characters; immutable | [Runtime revisions](../../capabilities/runtimes-and-pools/runtime-revisions.md) |
| `ownerTenantId` | String; set on an owned pool; immutable | [Tenant-derived runtimes](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md) |
| `isolationProfile` | Enum of the isolation profiles | [Isolation profiles](../../capabilities/runtimes-and-pools/isolation-profiles.md) |
| `resourceClass` | String; a class defined in the deployment's class configuration | [Resource classes](../../capabilities/runtimes-and-pools/resource-classes.md) |
| `sessionPolicy.maxConcurrentSessionsPerPod` | Integer | [Setting](../../capabilities/sessions/session-isolation.md#setting-sessionpolicy-maxconcurrentsessionsperpod) |
| `sessionPolicy.acknowledgeSharedPod` | Boolean | [Setting](../../capabilities/sessions/session-isolation.md#setting-sessionpolicy-acknowledgesharedpod) |
| `sessionPolicy.recycle.enabled` | Boolean | [Setting](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-enabled) |
| `sessionPolicy.recycle.acknowledgeBestEffortScrub` | Boolean | [Setting](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-acknowledgebesteffortscrub) |
| `sessionPolicy.recycle.maxSessionsPerPod` | Integer | [Setting](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-maxsessionsperpod) |
| `sessionPolicy.recycle.maxPodUptimeSeconds` | Integer | [Setting](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-maxpoduptimeseconds) |
| `sessionPolicy.recycle.maxScrubFailures` | Integer | [Setting](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-maxscrubfailures) |
| `sessionPolicy.recycle.onScrubFailure` | Enum | [Setting](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-onscrubfailure) |
| `sessionPolicy.recycle.scrubProfile` | Enum | [Setting](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-scrubprofile) |
| `sessionPolicy.recycle.acknowledgeMicrovmResidualState` | Boolean | [Setting](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#setting-sessionpolicy-recycle-acknowledgemicrovmresidualstate) |
| `minWarm` | Integer | [Setting](../../capabilities/runtimes-and-pools/warm-pools.md#setting-minwarm) |
| `maxWarm` | Integer | [Setting](../../capabilities/runtimes-and-pools/warm-pools.md#setting-maxwarm) |
| `scalePolicy` | Object: `podWarmupSecondsBaseline`, `bootstrapMinWarm`, `schedules`, `scaleToZero` | [POOL-R10](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r10), [POOL-R12](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r12), [POOL-R13](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r13) |
| `reservedPods` | Integer; 0 outside an owned pool | [POOL-R21](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r21) |
| `maxOwnedPods` | Integer; the owner's pod bound, on an owned pool | [POOL-R22](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r22) |
| `topologySpreadConstraints` | List of Kubernetes `TopologySpreadConstraint` | [POOL-R17](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r17) |
| `placement` | The resolved placement | [Placement](../../capabilities/runtimes-and-pools/placement.md) |
| `warmList` | The rendered warm repository stores | [Runtime revisions](../../capabilities/runtimes-and-pools/runtime-revisions.md) |
| `maxSessionAgeSeconds`, `terminationGracePeriodSeconds`, `maxTerminationGracePeriodSeconds`, `checkpointCadenceSeconds`, `checkpointBarrierAckTimeoutSeconds`, `workspaceSizeLimitBytes` | Integers | [Session states](../../capabilities/sessions/session-lifecycle/session-states.md), [Checkpoint capture](../../capabilities/sessions/checkpoints/checkpoint-capture.md) |
| `egressProfile`, `dnsPolicy`, `spiffeBinding`, `acknowledgeNonceOnlyAuth` | Enums and a boolean | Pages outside this migration <!-- unresolved: the egress and adapter-authentication capabilities are not migrated in this spike. --> |

<!-- unresolved: the field list follows the tree's `SandboxTemplateSpec` and `SandboxWarmPoolSpec` with the changes PLC-4 "Fields after the designs." lists; the volume size settings of PL-9, the slot memory settings, and the field names of the resolved placement, the warm list, and the pod bound are not named by any design. -->

**Removed fields.** `templateRef`, `executionMode`, `maxConcurrent`, `deliveryMode`, `workspaceTier`, `sdkWarmDisabled`, `scalePolicy.sdkConnectTimeoutSeconds`, `scalePolicy.sdkWarmCircuitBreakerMinOpenSeconds`, `status.sdkWarmCircuitBreaker`, `sessionPolicy.maxConcurrentSessions`, `sessionPolicy.acknowledgeProcessLevelIsolation`, `sessionPolicy.recycle.runtimeProcess`, and `sessionPolicy.recycle.allowCrossTenantReuse` are not part of the schema, and the API server refuses a write that names one.

### Status {#status}

| Field | Type | Writer | Meaning |
| :-- | :-- | :-- | :-- |
| `warmCount` | Integer | WarmPoolController | Unpinned pods in `warming` or `idle` ([POOL-R14](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r14)). |
| `readyCount` | Integer | WarmPoolController | Unpinned pods in `idle` ([POOL-R14](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r14)). |
| `scalingMode` | Enum: `bootstrap`, `formula` | PoolScalingController | [POOL-R12](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r12). <!-- unresolved: the non-bootstrap value is not named in the sources; `formula` is assumed. --> |
| `bootstrapHoursOfData` | Integer | PoolScalingController | Hours of demand history the bootstrap convergence has observed ([POOL-R12](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r12)). |
| `observedGeneration` | Integer | WarmPoolController | The `metadata.generation` last reconciled. |
| `conditions` | List of Kubernetes conditions | WarmPoolController | See [Conditions](#conditions). |

### Conditions {#conditions}

| Type | Reasons | Set when |
| :-- | :-- | :-- |
| `PoolWarmingUp` | `Provisioning`, `Drained` | [POOL-R18](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r18) |
| `Degraded` | [`PodSetKeyCollision`](../../capabilities/runtimes-and-pools/warm-pools.md#condition-reason-podsetkeycollision); `RuntimeClassMissing` <!-- unresolved: §4.6.1 names a Degraded condition when the RuntimeClass is missing without a reason string. --> | [POOL-R4](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r4); the pool's RuntimeClass does not exist. |
| `SecurityDegradedMode` | | The pod set renders nonce-only pods ([Gateway-to-pod channels](../channels/gateway-to-pod.md)). |
| `PlacementRefused` | The rejection label | A pod of the pod set was refused at placement admission ([Placement](../../capabilities/runtimes-and-pools/placement.md)). |
| `PlacementUnschedulable` | The scheduler's reason | A pod of the pod set has no node to run on ([Placement](../../capabilities/runtimes-and-pools/placement.md)). |

<!-- unresolved: §4.6.1 names a PoolDrained condition; no source states its reasons or its trigger, so it is left out. -->

### Owned objects {#owned-objects}

- The `PodSet` is the controller owner of each of its [`AgentPod`](agentpod.md#crd-agentpod) objects.
- The `PodSet` owns one PodDisruptionBudget for its warm pods ([POOL-R16](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r16)).

## Operations {#operations}

<a id="podset-list"></a>

### `kubernetes#podset.list` {#list}

The gateway lists a pool's `PodSet` objects by the pool label to compute a claim set ([CLAIM-R4](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r4)). The gateway performs no write on a `PodSet`.

## Validation {#validation}

The CRD's validation rules refuse, with the API server's validation error naming the field:

- `minWarm` above `maxWarm`, and `maxWarm` of 0 or less;
- `sessionPolicy.maxConcurrentSessionsPerPod` below 1;
- `sessionPolicy.recycle.maxSessionsPerPod` of 0 or less when `sessionPolicy.recycle.enabled` is `true`;
- `maxSessionAgeSeconds` of 0 or less;
- a change to `podSetKey` after creation.

## Admission {#admission}

[`lenny-pool-config-validator`](../../components/admission-webhooks.md) and the pod set deletion guard review `PodSet` writes. A client sees these outcomes:

| Operation | Refused when | Rejection label | Behind it |
| :-- | :-- | :-- | :-- |
| `CREATE` | The `lenny.dev/pool` label is absent or is not a valid label value. | <!-- unresolved: no design names the label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `CREATE` | The `lenny.dev/pod-set` label is absent or differs from the first 16 characters of `spec.podSetKey`. | <!-- unresolved: no design names the label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `CREATE` | `metadata.name` is not the pool label value, a `.`, and the pod-set label value. | <!-- unresolved: no design names the label. --> | [POOL-R4](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r4) |
| `UPDATE` | The update changes or removes `lenny.dev/pool` or `lenny.dev/pod-set`. | <!-- unresolved: no design names the label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `CREATE`, `UPDATE` | The writer of `spec` is not the PoolScalingController's ServiceAccount. The message directs the client to the admin API. | <!-- unresolved: §4.6.3 states the denial without a code. --> | [POOL-R1](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r1) |
| `CREATE`, `UPDATE` | The pool's grace period is below the agent-pod checkpoint floor, or its barrier acknowledgment timeout is below its floor. This applies to every writer. | `tiered_cap_exceeds_grace_budget`, `barrier_ack_below_floor` | [Checkpoint capture](../../capabilities/sessions/checkpoints/checkpoint-capture.md) |
| `CREATE`, `UPDATE` | The resolved placement escapes its node class. | The placement rejection labels | [Placement](../../capabilities/runtimes-and-pools/placement.md) |
| `DELETE` | A runtime rollout that still needs the pod set is in progress. | <!-- unresolved: the deletion guard's check once rollouts are per runtime (RV-4) is not stated. --> | [Runtime revisions](../../capabilities/runtimes-and-pools/runtime-revisions.md) |
| Any | The reviewing webhook cannot be reached. | None | [Lenny's Kubernetes API](index.md) |

## Compatibility {#compatibility}

`PodSet` replaces the earlier `SandboxTemplate` and `SandboxWarmPool` pair. No alias exists, and no retired name is accepted.
