---
code: WEBHOOK
type: component
summary: >-
  Lenny's validating admission webhooks run as separate Deployments that the Kubernetes API server
  calls before it stores a write. This page covers the webhooks the pool and pod lifecycle use:
  the pool configuration validator, the label immutability webhook, the pod claim guard, and the
  pod set deletion guard. It states what each one carries out, how they are deployed, how they
  fail, and their metrics.
owns: [process-settings, metrics]
depends-on: [POOL, CLAIM, APOD, QUAR, K8S, KPSET, KAPOD, KPCLAIM, KLABEL]
---

# Admission webhooks

Lenny's validating admission webhooks run as separate Deployments that the Kubernetes API server calls before it stores a write. This page covers the webhooks the pool and pod lifecycle use: the pool configuration validator, the label immutability webhook, the pod claim guard, and the pod set deletion guard. It states what each one carries out, how they are deployed, how they fail, and their metrics.

<!-- unresolved: the other admission webhooks and policies (`lenny-pod-security`, `lenny-direct-mode-isolation`, `lenny-data-residency-validator`, `lenny-drain-readiness`, `lenny-crd-conversion`, `lenny-ephemeral-container-cred-guard`, and the Kyverno or Gatekeeper policies) are outside this slice; this page grows sub-pages under `components/admission-webhooks/` when they are migrated. -->

## Terms {#terms}

**Owned here.**

| Term | Definition |
| :-- | :-- |
| Webhook Deployment | The Deployment and Service that serve one webhook. |
| Fail-closed | The API server refuses a write it cannot have reviewed, because the webhook's configuration sets `failurePolicy: Fail`. |

**Borrowed.**

| Term | Gloss |
| :-- | :-- |
| Admission outcome | What a Kubernetes client sees when a webhook refuses its write ([Lenny's Kubernetes API](../interfaces/kubernetes/index.md#admission)). |
| Rejection label | The stable code a Lenny admission refusal carries ([Lenny's Kubernetes API](../interfaces/kubernetes/index.md#rejection-labels)). |

## Overview {#overview}

Each webhook holds one kind of rule, so a webhook outage stops only the writes that rule covers. The webhooks in this slice all fail closed: when one is down, the writes it reviews are refused, because each guards an invariant that the controllers and the gateway trust without checking again. The resource pages state each refusal as a client sees it; this page states which process enforces it.

## Webhooks {#webhooks}

### `lenny-pool-config-validator` {#pool-config-validator}

| Property | Value |
| :-- | :-- |
| Reviews | `CREATE` and `UPDATE` of `podsets`. |
| Carries out | The label, name, and spec rules of [`PodSet` admission](../interfaces/kubernetes/podset.md#admission), which hold [POOL-R4](../capabilities/runtimes-and-pools/warm-pools.md#pool-r4) and [POOL-R5](../capabilities/runtimes-and-pools/warm-pools.md#pool-r5); the refusal of a `PodSet.spec` write by any principal other than the PoolScalingController, which holds [POOL-R1](../capabilities/runtimes-and-pools/warm-pools.md#pool-r1); the semantic budget rules that apply to every writer ([Checkpoint budgets](../capabilities/sessions/checkpoints/checkpoint-capture.md#grace-budget)); and the placement class and escape checks ([Placement](../capabilities/runtimes-and-pools/placement.md#enforcement)). |
| Identity check | Reads `userInfo` from the API server, which the caller cannot set. |
| Timeout | 5 s. |

### `lenny-label-immutability` {#label-immutability}

| Property | Value |
| :-- | :-- |
| Reviews | `UPDATE` of pods in the agent namespace; `CREATE`, `UPDATE`, and `DELETE` of `agentpods`; `DELETE` of pods carrying the quarantine label. |
| Carries out | The label rules of [labels and annotations](../interfaces/kubernetes/labels-and-annotations.md) marked "immutable" or with transition rules, which hold [POOL-R5](../capabilities/runtimes-and-pools/warm-pools.md#pool-r5) and [CLAIM-R6](../capabilities/runtimes-and-pools/pod-claims.md#claim-r6); the equality of an `AgentPod`'s labels with its `spec.podSetRef` at create ([`AgentPod` admission](../interfaces/kubernetes/agentpod.md#admission)); and the quarantine rules for label writes and deletions, which hold [APOD-R18](../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#apod-r18). |
| Identity check | Reads `userInfo` to admit a principal-specific transition, such as the gateway's first tenant pin. |

<!-- unresolved: §5.2 and §4.6.3 name a separate `lenny-tenant-label-immutability` webhook for the tenant label transitions, while §17.2 item 5 assigns those transitions to `lenny-label-immutability`; this page takes the single webhook of §17.2. -->

### `lenny-podclaim-guard` {#podclaim-guard}

| Property | Value |
| :-- | :-- |
| Reviews | `CREATE` of `podclaims`. Admits `UPDATE` and `PATCH` without inspection. |
| Carries out | The per-pod uniqueness and quarantine refusals of [`PodClaim` admission](../interfaces/kubernetes/podclaim.md#admission), which hold [CLAIM-R11](../capabilities/runtimes-and-pools/pod-claims.md#claim-r11). |
| Reads | Existing `PodClaim` objects by `spec.agentPodRef`, through a field index, and the target pod's labels. Reads no phase. |

### `lenny-podset-deletion-guard` {#podset-deletion-guard}

| Property | Value |
| :-- | :-- |
| Reviews | `DELETE` of `podsets`. |
| Carries out | The deletion refusal of [`PodSet` admission](../interfaces/kubernetes/podset.md#admission). |
| Calls | The gateway's internal endpoint for the state of the runtime rollout that serves the pod set. |
| Network | Carries the additive pod label `lenny.dev/webhook-name: podset-deletion-guard`, which the NetworkPolicy that admits its egress to the gateway's internal port selects ([Network isolation](../capabilities/network-and-egress/network-isolation.md#webhook-egress)). |

<!-- unresolved: today's guard probes `GET /internal/runtime-upgrade/active` per pool; RV-4 moves rollouts from pools to runtimes and PLC-2 states only that the guard refuses a `PodSet` deletion "under the same conditions", so the endpoint and the exact condition under runtime rollouts are not determined. -->

## Deployment {#deployment}

| Property | Value |
| :-- | :-- |
| Workload | One Deployment and Service per webhook in `lenny-system`, rendered from `templates/admission-policies/`. |
| Replicas | 2, with a PodDisruptionBudget of `minAvailable: 1` per webhook. |
| Failure policy | `failurePolicy: Fail` on every webhook on this page. |
| Pod label | `lenny.dev/component: admission-webhook` on every webhook pod, which the admission-webhook NetworkPolicy selects. |
| Rendering | Always rendered; none of these webhooks has a feature flag. |
| Preflight | `lenny-preflight` fails the install when any of these `ValidatingWebhookConfiguration` objects is missing. |
| Tests | Covered by the admission policy integration suite. |

## Process settings {#process-settings}

These webhooks read no deployer setting beyond the chart values that render them. <!-- unresolved: the TLS certificate issuer and the webhook RBAC in `webhook-cert-issuer.yaml` and `webhook-rbac.yaml` are not stated in §17.2. -->

## Failure and recovery {#failure-and-recovery}

| Webhook down | Effect |
| :-- | :-- |
| `lenny-pool-config-validator` | Every `PodSet` create and update is refused, including the PoolScalingController's, so configuration and warm-target changes stop. Existing pods continue. |
| `lenny-label-immutability` | Every reviewed pod update, `AgentPod` write, and quarantine label write is refused. The WarmPoolController cannot create `AgentPod` objects or update pod labels, so warming and phase labels stop. |
| `lenny-podclaim-guard` | Every claim creation is refused, so no session acquires a new pod. Double claims cannot occur. |
| `lenny-podset-deletion-guard` | Every `PodSet` deletion is refused, so a finished rollout's pod sets stay until it returns. |

Each recovers without operator action once a replica answers. Kubernetes retries nothing on the client's behalf; the controllers' reconcile loops and the gateway's claim retries resubmit.

## Metrics {#metrics}

```lenny-def
kind: metric
name: lenny_podclaim_guard_rejections_total
type: counter
labels: [service_instance_id]
summary: Claim creations the guard refused; a non-zero rate in normal operation points at a claim-path defect.
```

```lenny-def
kind: metric
name: lenny_pool_termination_budget_exceeded_total
type: counter
labels: [pool]
summary: PodSet writes the validator refused because the pool's grace period is below the agent-pod floor.
```

The availability alerts for these webhooks are on the capabilities whose requirements they protect: `PodClaimGuardUnavailable` on [Pod claims](../capabilities/runtimes-and-pools/pod-claims.md#alerts) and `PoolConfigValidatorUnavailable` on [Warm pools](../capabilities/runtimes-and-pools/warm-pools.md#alerts).
