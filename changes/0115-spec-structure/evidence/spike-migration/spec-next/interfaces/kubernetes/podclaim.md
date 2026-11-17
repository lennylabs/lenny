---
code: KPCLAIM
type: interface
area: kubernetes
summary: >-
  PodClaim is the custom resource that records the gateway's claim on one agent pod. This page
  states its fields with their types, constraints, and writers, its status, its validation, the
  operations the gateway performs on it, and the admission outcomes a Kubernetes client sees.
owns: [custom-resources, operations]
depends-on: [K8S, CLAIM, GWCLAIM, WPC, WEBHOOK, KAPOD]
---

# `PodClaim`

## Terms {#terms}

| Term | Gloss |
| :-- | :-- |
| Claim | The exclusive occupancy of one agent pod by the gateway on behalf of the sessions it places there ([CLAIM](../../capabilities/runtimes-and-pools/pod-claims.md#claim)). |
| Binding state | The state of a claim as the gateway records it ([CLAIM](../../capabilities/runtimes-and-pools/pod-claims.md#binding-state)). |

## Overview {#overview}

A `PodClaim` is a lock the gateway takes on a pod it has already chosen. No controller fulfils it, so its creation either succeeds or fails. It names its pod and carries no owner reference, so deleting the pod does not delete it. The rules that decide when a claim is created, which binding state it moves to, and when it is deleted are on the [pod claims](../../capabilities/runtimes-and-pools/pod-claims.md) page. This page states what a client of the Kubernetes API observes.

## Conventions {#conventions}

The conventions of [Lenny's Kubernetes API](index.md) apply: the API group, the version, field ownership through Server-Side Apply, and the way an admission refusal reaches a client.

## Resource {#resource}

<a id="crd-podclaim"></a>

```lenny-def
kind: custom-resource
name: PodClaim
group: lenny.dev
version: v1alpha1
plural: podclaims
crd: podclaims.lenny.dev
short-name: pclaim
scope: Namespaced (agent namespace)
writers: [GWCLAIM]
readers: [WPC, WEBHOOK]
summary: The gateway's claim on one agent pod, with its binding state.
```

**Printer columns.** AgentPod (`.spec.agentPodRef`), Phase (`.status.phase`).

### Metadata {#metadata}

| Field | Type and constraint | Writer | Behind it |
| :-- | :-- | :-- | :-- |
| `metadata.name` | Derived from the claimed pod's name | Gateway | [CLAIM-R1](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r1) |
| `metadata.ownerReferences` | Absent | None | [CLAIM-R2](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r2) |

### Spec {#spec}

The spec is immutable after creation.

| Field | Type and constraint | Writer | Behind it |
| :-- | :-- | :-- | :-- |
| `agentPodRef` | String; required; immutable; the claimed `AgentPod`'s name | Gateway | [CLAIM-R1](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r1) |
| `tenantId` | String; required; immutable; the tenant the claim was created for | Gateway | [CLAIM-R6](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r6) |

### Status {#status}

The gateway writes every status field through the status subresource. The WarmPoolController reads them and writes none.

| Field | Type | Meaning |
| :-- | :-- | :-- |
| `phase` | Enum; absent until the first status write. The values are generated from the [binding states](../../capabilities/runtimes-and-pools/pod-claims.md#binding-state): `bound`, `recycling`, `reserved`, `released`, `failed`, `quarantined`. | The binding state ([CLAIM-R8](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r8)). It is distinct from [`AgentPod.status.phase`](agentpod.md#crd-agentpod). |
| `bindingStateTransitionTime` | Timestamp | The time of the last binding-state write ([CLAIM-R8](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r8)). |
| `holdExpiresAt` | Timestamp; present while `phase` is `reserved` | The end of the reserved hold ([CLAIM-R9](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r9)). |
| `observedGeneration` | Integer | The `metadata.generation` the gateway last wrote against. |

**Removed field.** `rewarmStartedAt` is not part of the schema. It timed the re-warm of pre-launched runtimes, which no longer exist.

## Operations {#operations}

<a id="podclaim-create"></a>

### `kubernetes#podclaim.create` {#create}

The gateway creates a claim with its spec only, because the status subresource is not writable on create, and then performs [`kubernetes#podclaim.updateStatus`](#podclaim-updatestatus) with `phase: bound`.

| Outcome | Status | Behind it |
| :-- | :-- | :-- |
| Created | `201 Created` | [CLAIM-R1](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r1) |
| Name conflict: another writer created the same name first | `409 AlreadyExists` | [CLAIM-R1](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r1) |
| Refused by admission | `403 Forbidden`; see [Admission](#admission) | [CLAIM-R11](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r11) |

<a id="podclaim-updatestatus"></a>

### `kubernetes#podclaim.updateStatus` {#update-status}

Every status write is a patch of the status subresource that carries the `resourceVersion` the writer read.

| Outcome | Status | Behind it |
| :-- | :-- | :-- |
| Written | `200 OK` | [CLAIM-R8](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r8) |
| The claim changed since the writer read it | `409 Conflict` | [CLAIM-R8](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r8) |
| `phase` outside the enumeration | `422 Invalid` | [Validation](#validation) |

<a id="podclaim-delete"></a>

### `kubernetes#podclaim.delete` {#delete}

A deletion that ends a reserved hold carries the preconditions `uid` and `resourceVersion`. Every other deletion carries none.

| Outcome | Status | Behind it |
| :-- | :-- | :-- |
| Deleted | `200 OK` | [CLAIM-R2](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r2) |
| A precondition no longer matches, because a rebind wrote the claim first | `409 Conflict`; the claim stays | [CLAIM-R9](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r9) |
| The claim no longer exists | `404 NotFound` | [CLAIM-R2](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r2) |

<a id="podclaim-get"></a>

### `kubernetes#podclaim.get` and `kubernetes#podclaim.list` {#get}

<a id="podclaim-list"></a>

The gateway reads one claim by name. The WarmPoolController lists every claim in the agent namespace. Neither read has an outcome beyond the API server's.

## Validation {#validation}

The CRD's schema refuses a claim without `agentPodRef` or `tenantId`, a change to any spec field after creation, and a `status.phase` outside the enumeration.

## Admission {#admission}

[`lenny-podclaim-guard`](../../components/admission-webhooks.md) reviews every `CREATE` and admits every update without inspection.

| Operation | Refused when | Status and error | Behind it |
| :-- | :-- | :-- | :-- |
| `CREATE` | A claim whose phase is not terminal already names the same `agentPodRef`, under any name. | `403 Forbidden`, [`PODCLAIM_CONCURRENT`](../../capabilities/runtimes-and-pools/pod-claims.md#error-podclaim-concurrent) | [CLAIM-R11](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r11) |
| `CREATE` | The claimed pod carries the quarantine label. | `403 Forbidden`, [`PODCLAIM_QUARANTINED`](../../capabilities/runtimes-and-pools/pod-claims.md#error-podclaim-quarantined) | [CLAIM-R11](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r11) |
| `CREATE` | The webhook cannot be reached. | The API server's webhook failure | [CLAIM-R11](../../capabilities/runtimes-and-pools/pod-claims.md#claim-r11) |

## Compatibility {#compatibility}

`PodClaim` replaces the earlier `SandboxClaim` kind, and `agentPodRef` replaces `sandboxRef`. No alias exists for either, and no retired name is accepted.
