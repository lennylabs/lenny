---
code: KPCLAIM
type: interface
summary: >-
  PodClaim is the custom resource that records the gateway's claim on one agent pod. This page
  states its fields with their types, constraints, and owners, its binding-state status, its
  validation, its operations as the gateway performs them, and the admission outcomes a Kubernetes
  client sees.
owns: [custom-resources, errors]
depends-on: [K8S, CLAIM, GWCLAIM, WPC, WEBHOOK, KAPOD]
---

# `PodClaim`

`PodClaim` is the custom resource that records the gateway's claim on one agent pod. This page states its fields with their types, constraints, and owners, its binding-state status, its validation, its operations as the gateway performs them, and the admission outcomes a Kubernetes client sees.

## Terms {#terms}

| Term | Gloss |
| :-- | :-- |
| Claim | The exclusive occupancy of one agent pod by the gateway ([Pod claims](../../capabilities/runtimes-and-pools/pod-claims.md#claim)). |
| Binding state | The state of a claim ([Pod claims](../../capabilities/runtimes-and-pools/pod-claims.md#binding-states)). |

## Overview {#overview}

A `PodClaim` is a lock the gateway takes on a pod it has already chosen. No controller fulfils it: its creation either succeeds or fails. It names its pod and carries no owner reference, so deleting the pod does not delete it. The rules that decide when a claim is created, which state it moves to, and when it is deleted belong to [Pod claims](../../capabilities/runtimes-and-pools/pod-claims.md); this page states what a client of the Kubernetes API observes.

The register `REG-CLAIM` names this resource as shared state between the gateway replicas and the WarmPoolController ([Registers](../channels/gateway-to-store.md#reg-claim)).

## Conventions {#conventions}

The conventions of [Lenny's Kubernetes API](index.md#conventions) apply.

## Resource {#resource}

```lenny-def
kind: custom-resource
name: PodClaim
group: lenny.dev
version: v1alpha1
plural: podclaims
crd: podclaims.lenny.dev
short-name: pclaim
scope: Namespaced (agent namespace)
summary: The gateway's claim on one agent pod, with its binding state.
```

**Printer columns.** AgentPod (`.spec.agentPodRef`), Phase (`.status.phase`).

### Metadata {#metadata}

| Field | Type and constraint | Owner |
| :-- | :-- | :-- |
| `metadata.name` | `claim-<podName>` | Gateway |
| `metadata.ownerReferences` | Absent | None |

### Spec {#spec}

The spec is immutable after creation.

| Field | Type and constraint | Owner |
| :-- | :-- | :-- |
| `agentPodRef` | String; required; immutable; the claimed `AgentPod`'s name | Gateway |
| `tenantId` | String; the tenant the claim was created for | Gateway |

### Status {#status}

The gateway writes every status field through the status subresource. The WarmPoolController reads them and never writes them.

| Field | Type | Meaning |
| :-- | :-- | :-- |
| `phase` | Enum: `bound`, `recycling`, `reserved`, `released`, `failed`, `quarantined`; absent until the first write | The binding state ([CLAIM-R8](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r8)). It carries no pod lifecycle meaning and is distinct from [`AgentPod.status.phase`](agentpod.md#status). |
| `bindingStateTransitionTime` | Timestamp | The time of the last binding-state write ([CLAIM-R15](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r15)). |
| `holdExpiresAt` | Timestamp; present in `reserved` | The end of the reserved hold ([CLAIM-R9](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r9)). |
| `observedGeneration` | Integer | |
| `conditions` | List of Kubernetes conditions | <!-- unresolved: the tree carries a conditions list; no source states a condition on a claim. --> |

**Removed field.** `rewarmStartedAt` leaves with the pre-launched runtimes it timed.

## Operations {#operations}

### Create {#create}

The gateway creates a claim with spec only, because the status subresource is not writable on create. It then writes `status.phase: bound`.

| Outcome | Status | Meaning |
| :-- | :-- | :-- |
| Created | `201` | The gateway holds the pod. |
| Name conflict | `409 AlreadyExists` | Another writer created `claim-<podName>` first. |
| Admission refusal | `403 Forbidden` | See [Admission](#admission). |

### Status write {#status-write}

Every status write carries the `resourceVersion` the writer read. A write against a stale version answers `409 Conflict`. A write with a phase outside the enumeration is refused by the schema.

### Delete {#delete}

A deletion that ends a reserved hold carries the preconditions `uid` and `resourceVersion`, the values observed when the claim entered `reserved`. When either no longer matches, the deletion answers `409 Conflict` and the claim stays.

## Validation {#validation}

The CRD's schema refuses a claim without `agentPodRef`, a change to any spec field after creation, and a `status.phase` outside the enumeration.

## Admission {#admission}

[`lenny-podclaim-guard`](../../components/admission-webhooks.md#podclaim-guard) reviews every `CREATE` and admits every update without inspection.

| Operation | Refused when | Status and message | Behind it |
| :-- | :-- | :-- | :-- |
| `CREATE` | A claim whose phase is not terminal already names the same `agentPodRef`, under any name. | `403 Forbidden`, `"PodClaim already exists for AgentPod <name>; concurrent claim rejected"` | [CLAIM-R11](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r11) |
| `CREATE` | The claimed pod carries the quarantine label. | `403 Forbidden` <!-- unresolved: FE-1 states the refusal without a message. --> | [CLAIM-R11](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r11) |
| `CREATE` | The webhook cannot be reached. | The API server's webhook failure | [Lenny's Kubernetes API](index.md#admission) |

```lenny-def
kind: error
interface: kubernetes
name: podclaim concurrent claim refusal
status: 403
message: "PodClaim already exists for AgentPod <name>; concurrent claim rejected"
summary: The guard refused a second claim on one pod (CLAIM-R11).
```
<!-- unresolved: the message carries no rejection label; see the rejection label convention. -->

## Compatibility {#compatibility}

`PodClaim` replaces the earlier `SandboxClaim` kind, and `agentPodRef` replaces `sandboxRef`, with no alias. The conventions of [Lenny's Kubernetes API](index.md#compatibility) apply.
