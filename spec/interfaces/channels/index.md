---
code: CHAN
type: index
summary: >-
  The gateway-to-pod channels and registers: classes, axes, the contract card format, the exclusivity model, and the wire-contract artifacts.
status: placeholder
---

# Channels

The gateway-to-pod channels and registers: classes, axes, the contract card format, the exclusivity model, and the wire-contract artifacts.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Control plane](control-plane.md) | `CHCTRL` | The control-plane channel for drain-readiness admission. |
| [Gateway to pod](gateway-to-pod.md) | `CHGWPOD` | The gateway-to-pod link and its channels, with the adapter RPC operations and request scope rules. |
| [Gateway to store](gateway-to-store.md) | `CHSTORE` | The gateway-to-store channels and the registers that mediate replicas through Redis, Postgres, and the Kubernetes API. |
| [Inter-replica](inter-replica.md) | `CHREPLICA` | The channel that forwards off-holder session requests between gateway replicas. |
| [Intra-pod](intra-pod.md) | `CHINTRA` | The intra-pod channels between Lenny's own processes, including the supervision channel; the runtime-facing channels point to the runtime protocol. |
| [Pod egress](pod-egress.md) | `CHEGRESS` | The pod-egress channels to the metering proxy and the object store. |
| [Pod to gateway](pod-to-gateway.md) | `CHPODGW` | The pod-to-gateway link and the adapter events channel. |

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§15.4 Runtime Adapter Specification](../../15_external-api-surface.md#154-runtime-adapter-specification), in part
- [§28 Communication Channels](../../28_communication-channels.md)
- [§28.2 Taxonomy and axes](../../28_communication-channels.md#282-taxonomy-and-axes)
- [§28.3 Registers](../../28_communication-channels.md#283-registers)
- [§28.3 Registers › Link register](../../28_communication-channels.md#link-register), in part
- [§28.4 Claim register](../../28_communication-channels.md#284-claim-register), in part
- [§28.5 Contract cards](../../28_communication-channels.md#285-contract-cards)
- [§28.6 Exclusivity and concurrency model](../../28_communication-channels.md#286-exclusivity-and-concurrency-model)
- [§28.7 Wire-contract artifact register](../../28_communication-channels.md#287-wire-contract-artifact-register), in part
