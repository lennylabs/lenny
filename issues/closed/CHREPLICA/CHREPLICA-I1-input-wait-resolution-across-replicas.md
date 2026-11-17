---
id: CHREPLICA-I1
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0135
---

# Input wait does not resolve across replicas

## Specification

§28.5.3 (Intra-pod), `CH-MCP-PLATFORM` **Timing.**: "`lenny/request_input` blocks until an answer arrives and is bounded by `maxRequestInputWaitSeconds`". §28.5.4 (Inter-replica) states no cross-replica wait.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Input-wait resolution across replicas", status `ABSENT`, anchor `#2854-inter-replica`, deferral `R20`.
- Surface recorded on the row: "`pkg/gateway/session/inputwait/inputwait.go:41-49` is process-local".
- Note recorded on the row: "the pending lenny/request_input call waits on the replica the pod's LNK-GWCONTROL connection reached, and no store carries it to another replica".

## Gap

A pending input wait lives on one replica, and an answer that reaches another replica does not resolve it.

## Suggested resolution

The row deferred the work to step R20, "Message-plane durability and cross-replica input wait", of `gateway-runtime-comms-remediation.md`.

Closed at filing: 0135 ("MD-22 Parents and children") removes the tool: "`lenny/request_input`, the `input_required` sub-state it causes, `maxRequestInputWaitSeconds`, path 1 and path 3 of §7.2, and the subtree deadlock detector leave the specification." Its non-specification changes remove `inputwait` with the reply path, in step 0135.S5.
