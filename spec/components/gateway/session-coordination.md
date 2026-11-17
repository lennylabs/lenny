---
code: GWCOORD
type: component
summary: >-
  How gateway replicas coordinate a session: the coordination lease, handoff, hold state, stale replicas, and behavior when both stores fail.
status: placeholder
---

# Session coordination

This page is a placeholder. The sources under "Content today" hold its content until the page is written.

## Content today

Numbered sections:

- [§7.2 Interactive Session Model](../../07_session-lifecycle.md#72-interactive-session-model), in part
- [§10.1.1 Stateless Replicas and Per-Session Coordination](../../10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination), in part
- [§10.1.2 Coordinator Handoff Protocol](../../10_gateway-internals.md#1012-coordinator-handoff-protocol), in part
- [§10.1.3 Dual-Store Unavailability](../../10_gateway-internals.md#1013-dual-store-unavailability), in part
- [§10.1.4 Coordinator-Loss Detection and Hold State](../../10_gateway-internals.md#1014-coordinator-loss-detection-and-hold-state), in part
- [§10.1.5 Stale Replica Behavior](../../10_gateway-internals.md#1015-stale-replica-behavior)
- [§16.1 Metrics](../../16_observability.md#161-metrics), in part
- [§16.5 Alerting Rules and SLOs](../../16_observability.md#165-alerting-rules-and-slos), in part

Changes of the v1 program whose content lands on this page:

- [0135-message-delivery](../../../changes/0135-message-delivery/change.md): MD-6
