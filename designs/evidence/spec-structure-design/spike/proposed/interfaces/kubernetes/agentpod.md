---
code: KAPOD
type: interface
summary: >-
  AgentPod is the custom resource that records one agent pod: the pod set it belongs to, the slot
  count it was rendered with, its projected phase, and the address the gateway dials. This page
  states its fields with their types, constraints, and owners, its status and conditions, its
  validation, and the admission outcomes a Kubernetes client sees.
owns: [custom-resources, errors]
depends-on: [K8S, APOD, POOL, WPC, WEBHOOK, KLABEL]
---

# `AgentPod`

`AgentPod` is the custom resource that records one agent pod: the pod set it belongs to, the slot count it was rendered with, its projected phase, and the address the gateway dials. This page states its fields with their types, constraints, and owners, its status and conditions, its validation, and the admission outcomes a Kubernetes client sees.

## Terms {#terms}

| Term | Gloss |
| :-- | :-- |
| Agent pod | A Kubernetes pod that serves sessions for one pool ([Agent pod lifecycle](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#agent-pod)). |
| Phase | The coarse occupancy state of an agent pod ([Agent pod lifecycle](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#phases)). |
| Slot count | The number of sessions an agent pod was rendered to serve at once ([Agent pod lifecycle](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#slot-count)). |

## Overview {#overview}

Every agent pod has one `AgentPod` of the same name, and the pod's pod set owns it. The WarmPoolController is its only writer. The gateway reads `status.phase` to find idle pods and `status.podIP` to reach the pod's adapter. The `AgentPod` holds no pod template: the WarmPoolController renders the pod from the `PodSet`, the revision, and the placement.

## Conventions {#conventions}

The conventions of [Lenny's Kubernetes API](index.md#conventions) apply.

## Resource {#resource}

```lenny-def
kind: custom-resource
name: AgentPod
group: lenny.dev
version: v1alpha1
plural: agentpods
crd: agentpods.lenny.dev
short-name: apod
scope: Namespaced (agent namespace)
summary: One agent pod, its pod set, its slot count, its projected phase, and its address.
```

**Printer columns.** Phase (`.status.phase`), PodSet (`.spec.podSetRef`), Pod (`.status.podName`).

### Metadata {#metadata}

| Field | Type and constraint | Owner | Behind it |
| :-- | :-- | :-- | :-- |
| `metadata.name` | Generated as `<pool>-<random>`; at most 63 characters; no `.` | WarmPoolController | [APOD-R1](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r1) |
| `metadata.ownerReferences` | The owning `PodSet`, as controller | WarmPoolController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `metadata.finalizers` | Includes [`lenny.dev/session-cleanup`](labels-and-annotations.md#lenny-dev-session-cleanup) | WarmPoolController | [APOD-R16](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r16) |
| labels [`lenny.dev/pool`](labels-and-annotations.md#lenny-dev-pool), [`lenny.dev/pod-set`](labels-and-annotations.md#lenny-dev-pod-set) | Required; immutable; copied from the `PodSet` | WarmPoolController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |

### Spec {#spec}

| Field | Type and constraint | Owner | Behind it |
| :-- | :-- | :-- | :-- |
| `podSetRef` | String; required; immutable (`self == oldSelf`); the owning `PodSet`'s name | WarmPoolController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `runtimeRef` | String; required; the runtime name | WarmPoolController | [APOD-R2](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r2) |
| `slotCount` | Integer; `>= 1`; immutable | WarmPoolController | [APOD-R2](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r2) |
| `isolationProfile` | Enum of the isolation profiles | WarmPoolController | [Isolation profiles](../../capabilities/runtimes-and-pools/isolation-profiles.md) |
| `resourceClass` | String; a configured class | WarmPoolController | [Resource classes](../../capabilities/runtimes-and-pools/resource-classes.md) |
| `topologySpreadConstraints` | List of Kubernetes `TopologySpreadConstraint` | WarmPoolController | [POOL-R17](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r17) |
| `reserved` | Boolean; immutable; set on an owned pool's reserved pods | WarmPoolController | [POOL-R21](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r21) |
| `requireSoPeercred` | Boolean | WarmPoolController | [Adapter authentication](../channels/gateway-to-pod.md#nonce-only) |
| `warmStores` | List of store directories the pod holds | WarmPoolController <!-- unresolved: DR-11 says "the `AgentPod`'s `warmStores` drops" a withheld store without naming the writer or whether the field is in spec or status. --> | [Tenant-derived runtimes](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md#warm-stores) |

**Removed fields.** `poolRef`, `executionMode`, and `deliveryMode` are not part of the schema.

### Status {#status}

| Field | Type | Owner | Meaning |
| :-- | :-- | :-- | :-- |
| `phase` | Enum: `warming`, `idle`, `claimed`, `reserved`, `draining`, `failed`, `terminated`, `quarantined` | WarmPoolController | [APOD-R5](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r5) |
| `podName` | String | WarmPoolController | The pod's name, equal to the `AgentPod`'s. |
| `podIP` | String | WarmPoolController | A copy of the pod's `status.podIP`. The gateway dials the adapter at this address and refuses to dial while it is empty. |
| `nodeName` | String | WarmPoolController | The node the pod runs on. |
| `tenantId`, `sessionId` | Strings | WarmPoolController | <!-- unresolved: the tree carries these status fields; no design states whether they stay once the claim and the session row record the same values. --> |
| `observedGeneration` | Integer | WarmPoolController | The `metadata.generation` last reconciled. |
| `conditions` | List of Kubernetes conditions | WarmPoolController | See below. |

### Conditions {#conditions}

| Type | Set when |
| :-- | :-- |
| `SOPeercredDisabled` | The pod was rendered without peer-credential checking ([Adapter authentication](../channels/gateway-to-pod.md#nonce-only)). |

No other condition is set. A pending pod's scheduler reason is on the pod's own `PodScheduled` condition, which `kubectl describe pod <name>` shows, and the pod set's `PlacementUnschedulable` condition reports it per pod set.

## Validation {#validation}

The CRD's schema refuses an `AgentPod` without `podSetRef` or `runtimeRef`, a change to `podSetRef`, `slotCount`, or `reserved` after creation, and a `status.phase` outside the enumeration.

## Admission {#admission}

[`lenny-label-immutability`](../../components/admission-webhooks.md#label-immutability) reviews `AgentPod` writes. A client sees these outcomes:

| Operation | Refused when | Rejection label | Behind it |
| :-- | :-- | :-- | :-- |
| `CREATE` | `lenny.dev/pool` is not the text of `spec.podSetRef` before its first `.`, or `lenny.dev/pod-set` is not the text after it. | `AGENTPOD_LABEL_MISMATCH` <!-- unresolved: proposed label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `UPDATE` | The update changes or removes `lenny.dev/pool` or `lenny.dev/pod-set`. | `LABEL_IMMUTABLE` <!-- unresolved: proposed label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `DELETE` | The `AgentPod`'s pod carries the quarantine label and the caller is not the WarmPoolController after the quarantine's release. | `QUARANTINE_DELETE_DENIED` <!-- unresolved: proposed label. --> | [APOD-R18](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r18) |
| Any | The webhook cannot be reached. | None | [Lenny's Kubernetes API](index.md#admission) |

## Compatibility {#compatibility}

`AgentPod` replaces the earlier `Sandbox` kind with no alias. The conventions of [Lenny's Kubernetes API](index.md#compatibility) apply.
