---
code: GW
type: component
summary: >-
  The gateway is Lenny's stateless, horizontally scaled front door and session coordinator. This
  index lists its subsystems; each subsystem page states its responsibilities, interfaces, and
  failure behavior.
owns: []
depends-on: []
---

# Gateway

The gateway is Lenny's stateless, horizontally scaled front door and session coordinator. This index lists its subsystems; each subsystem page states its responsibilities, interfaces, and failure behavior.

<!-- stub: written by the slice to give the pod claimer a parent. The gateway's deployment properties (replicas, leader election, probes, ServiceAccount, RBAC as a whole, NetworkPolicy) belong on this page and were not migrated. -->

## Subsystems

- [Pod claimer](pod-claimer.md)
- Client API handlers
- Admin API handlers
- Session coordinator
- Release and recycle coordinator
- Orphan session reconciler
- Pool configuration drift detector
- LLM proxy
- Credential leasing client
- Event and webhook delivery
