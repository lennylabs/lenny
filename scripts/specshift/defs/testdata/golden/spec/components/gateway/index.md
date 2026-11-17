---
code: GW
type: component
summary: The gateway as a unit.
---

# Gateway

## Deployment

<!-- generated:rbac-grants -->

| Grant | Stated on | API groups | Resources | Verbs | Scope | Requirement |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| [`podclaims write`](pod-claimer.md#rbac-grant-podclaims-write) | [Pod claimer](pod-claimer.md) | `lenny.dev` | `podclaims`, `podclaims/status` | `create`, `get`, `patch` | agent namespace | POOL-R1 |

<!-- /generated:rbac-grants -->

<!-- generated:network-rules -->

| Rule | Stated on | Direction | Peer | Ports | Requirement |
| :-- | :-- | :-- | :-- | :-- | :-- |
| [`claimer to API server`](pod-claimer.md#network-rule-claimer-to-api-server) | [Pod claimer](pod-claimer.md) | egress | kube-apiserver | `443/TCP` | POOL-R1 |

<!-- /generated:network-rules -->

## Pages

<!-- generated:index -->

| Page | Summary |
| :-- | :-- |
| [Pod claimer](pod-claimer.md) | The gateway part that claims pods. |

<!-- /generated:index -->
