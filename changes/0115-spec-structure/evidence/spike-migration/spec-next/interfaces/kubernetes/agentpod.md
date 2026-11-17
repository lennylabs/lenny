---
code: KAPOD
type: interface
area: kubernetes
summary: >-
  AgentPod is the custom resource that records one agent pod: the pod set it belongs to, the slot
  count it was rendered with, its projected phase, and the address the gateway dials. This page
  states its fields with their types, constraints, and writers, its status and conditions, its
  validation, and the admission outcomes a Kubernetes client sees.
owns: [custom-resources, operations]
depends-on: [K8S, APOD, POOL, CLAIM, WPC, WEBHOOK, KLABEL]
---

# `AgentPod`

## Terms {#terms}

| Term | Gloss |
| :-- | :-- |
| Agent pod | A Kubernetes pod that serves sessions for one pod set of one pool ([APOD](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#agent-pod)). |
| Pod phase | The coarse occupancy state of an agent pod ([APOD](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#pod-phase)). |
| Slot count | The number of sessions an agent pod was rendered to serve at once ([APOD](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#slot-count)). |

## Overview {#overview}

Every agent pod has one `AgentPod` of the same name, and the pod's pod set owns it. The WarmPoolController is its only writer. The gateway reads `status.phase` to find idle pods and `status.podIP` to reach the pod's adapter. The `AgentPod` holds no pod template, because the WarmPoolController renders the pod from the `PodSet`, the revision, and the placement.

## Conventions {#conventions}

The conventions of [Lenny's Kubernetes API](index.md) apply.

## Resource {#resource}

<a id="crd-agentpod"></a>

```lenny-def
kind: custom-resource
name: AgentPod
group: lenny.dev
version: v1alpha1
plural: agentpods
crd: agentpods.lenny.dev
short-name: apod
scope: Namespaced (agent namespace)
writers: [WPC]
readers: [GWCLAIM, WEBHOOK]
summary: One agent pod, its pod set, its slot count, its projected phase, and its address.
```

**Printer columns.** Phase (`.status.phase`), PodSet (`.spec.podSetRef`), Pod (`.status.podName`).

### Metadata {#metadata}

| Field | Type and constraint | Writer | Behind it |
| :-- | :-- | :-- | :-- |
| `metadata.name` | Generated from the pool name; at most 63 characters; no `.` | WarmPoolController | [APOD-R1](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r1) |
| `metadata.ownerReferences` | The owning `PodSet`, as controller | WarmPoolController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `metadata.finalizers` | Includes [`lenny.dev/session-cleanup`](labels-and-annotations.md#finalizer-lenny-dev-session-cleanup) | WarmPoolController | [APOD-R16](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r16) |
| labels [`lenny.dev/pool`](labels-and-annotations.md#label-lenny-dev-pool) and [`lenny.dev/pod-set`](labels-and-annotations.md#label-lenny-dev-pod-set) | Required; immutable; copied from the `PodSet` | WarmPoolController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |

### Spec {#spec}

| Field | Type and constraint | Writer | Behind it |
| :-- | :-- | :-- | :-- |
| `podSetRef` | String; required; immutable; the owning `PodSet`'s name | WarmPoolController | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `runtimeRef` | String; required; the runtime name | WarmPoolController | [APOD-R2](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r2) |
| `slotCount` | Integer; at least 1; immutable | WarmPoolController | [APOD-R2](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r2) |
| `isolationProfile` | Enum of the isolation profiles | WarmPoolController | [Isolation profiles](../../capabilities/runtimes-and-pools/isolation-profiles.md) |
| `resourceClass` | String; a configured class | WarmPoolController | [Resource classes](../../capabilities/runtimes-and-pools/resource-classes.md) |
| `topologySpreadConstraints` | List of Kubernetes `TopologySpreadConstraint` | WarmPoolController | [POOL-R17](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r17) |
| `reserved` | Boolean; immutable; set on an owned pool's reserved pods | WarmPoolController | [POOL-R21](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r21) |
| `requireSoPeercred` | Boolean | WarmPoolController | [Gateway-to-pod channels](../channels/gateway-to-pod.md) |
| `warmStores` | List of the store directories the pod holds | WarmPoolController <!-- unresolved: DR-11 says "the `AgentPod`'s `warmStores` drops" a withheld store without naming the writer or whether the field is in spec or status. --> | [Tenant-derived runtimes](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md) |

**Removed fields.** `poolRef`, `executionMode`, and `deliveryMode` are not part of the schema.

### Status {#status}

| Field | Type | Writer | Meaning |
| :-- | :-- | :-- | :-- |
| `phase` | Enum. The values are generated from the [pod phases](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#pod-phase): `warming`, `idle`, `claimed`, `reserved`, `draining`, `failed`, `terminated`, `quarantined`. | WarmPoolController | [APOD-R5](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r5) |
| `podName` | String | WarmPoolController | The pod's name, equal to the `AgentPod`'s. |
| `podIP` | String | WarmPoolController | A copy of the pod's `status.podIP`. The gateway dials the adapter at this address ([GWCLAIM-R2](../../components/gateway/pod-claimer.md#gwclaim-r2)). |
| `nodeName` | String | WarmPoolController | The node the pod runs on. |
| `tenantId`, `sessionId` | Strings | WarmPoolController | <!-- unresolved: the tree carries these status fields; no design states whether they stay once the claim and the session row record the same values. --> |
| `observedGeneration` | Integer | WarmPoolController | The `metadata.generation` last reconciled. |
| `conditions` | List of Kubernetes conditions | WarmPoolController | See [Conditions](#conditions). |

### Conditions {#conditions}

| Type | Set when |
| :-- | :-- |
| `SOPeercredDisabled` | The pod was rendered without peer-credential checking ([Gateway-to-pod channels](../channels/gateway-to-pod.md)). |

No other condition is set. A pending pod's scheduler reason is on the pod's own `PodScheduled` condition, and the pod set's `PlacementUnschedulable` condition reports it per pod set.

## Operations {#operations}

<a id="agentpod-get"></a>

### `kubernetes#agentpod.get` and `kubernetes#agentpod.list` {#get}

<a id="agentpod-list"></a>

The gateway reads one `AgentPod` by name and lists the `AgentPod` objects of a claim set by the pool label together with the pod-set label ([CLAIM-R4](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r4)). The gateway performs no other operation on an `AgentPod`.

## Validation {#validation}

The CRD's schema refuses an `AgentPod` without `podSetRef` or `runtimeRef`, a change to `podSetRef`, `slotCount`, or `reserved` after creation, and a `status.phase` outside the enumeration.

## Admission {#admission}

[`lenny-label-immutability`](../../components/admission-webhooks.md) reviews `AgentPod` writes. A client sees these outcomes:

| Operation | Refused when | Rejection label | Behind it |
| :-- | :-- | :-- | :-- |
| `CREATE` | `lenny.dev/pool` is not the text of `spec.podSetRef` before its first `.`, or `lenny.dev/pod-set` is not the text after it. | <!-- unresolved: no design names the rejection label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `UPDATE` | The update changes or removes `lenny.dev/pool` or `lenny.dev/pod-set`. | <!-- unresolved: no design names the rejection label. --> | [POOL-R5](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) |
| `DELETE` | The pod carries the quarantine label and the caller is not the WarmPoolController after the quarantine's release. | <!-- unresolved: FE-1 states the refusal without a label. --> | [APOD-R18](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r18) |
| Any | The webhook cannot be reached. | None | [Lenny's Kubernetes API](index.md) |

## Compatibility {#compatibility}

`AgentPod` replaces the earlier `Sandbox` kind. No alias exists, and no retired name is accepted.
