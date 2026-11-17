---
code: KAPI
type: index
summary: >-
  Lenny's Kubernetes API conventions: the API group and version, the shipped CRDs and their schema version, field ownership, validation, and how an admission refusal reaches a client.
status: placeholder
---

# Kubernetes

Lenny's Kubernetes API conventions: the API group and version, the shipped CRDs and their schema version, field ownership, validation, and how an admission refusal reaches a client.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Admission](admission.md) | `KADMIT` | The writes Lenny's admission webhooks and policies refuse, with the refusal each returns. |
| [AgentPod](agentpod.md) | `KAPOD` | The AgentPod kind. |
| [Labels and annotations](labels-and-annotations.md) | `KLABEL` | Every label and annotation key, with its writer and readers. |
| [PodClaim](podclaim.md) | `KPCLAIM` | The PodClaim kind. |
| [PodSet](podset.md) | `KPSET` | The PodSet kind. |
| [Runtime](runtime.md) | `KRUNTIME` | The Runtime kind. |

<!-- /generated:index -->

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§4.6.1 Warm Pool Controller (Pod Lifecycle)](../../04_system-components.md#461-warm-pool-controller-pod-lifecycle), in part

Changes of the v1 program whose content lands on this page:

- [0137-pod-lifecycle-controller](../../../changes/0137-pod-lifecycle-controller/change.md): PLC-3
