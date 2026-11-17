---
id: CHCTRL-I1
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0137
---

# No PodDisruptionBudget protects a busy agent pod

## Specification

§28.5.6 (Control-plane) states no disruption budget for agent pods. §4.6.1 (Warm Pool Controller Pod Lifecycle), **Disruption protection for agent pods:**, states an optional budget for warm pods only.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "PodDisruptionBudget for a busy pod", status `ABSENT`, anchor `#2856-control-plane`, deferral `R16`.
- Surface recorded on the row: "`pkg/controller/warmpool/pdb.go:70-76`".
- `pkg/controller/warmpool/pdb.go` creates the warm-pod budget only.

## Gap

The row recorded a budget for busy pods that the specification does not state.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

Closed at filing: 0137 ("PLC-2 Kind and field names", **PodDisruptionBudget.**) sets one budget per `PodSet`, selecting idle pods. A busy pod is protected by the preStop eviction checkpoint, which CHGWPOD-I2 records.
