---
id: RTPMSG-I1
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0135
---

# The adapter does not report ready_for_input

## Specification

§28 makes no statement about `ready_for_input`. §7.2 (Interactive Session Model) defines it, and §29.3 (Interactive message send) reads it as the adapter's report that the target runtime is available.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "`ready_for_input` signal", status `ABSENT`, anchor `#2853-intra-pod`, deferral `R20`.
- Surface recorded on the row: "comments only".

## Gap

The signal appears in comments only.

## Suggested resolution

The row deferred the work to step R20, "Message-plane durability and cross-replica input wait", of `gateway-runtime-comms-remediation.md`.

Closed at filing: 0135 ("MD-5 Delivery paths") removes it: "The `ready_for_input` definition, the delivery timeout of 30 seconds and its fall-through to the inbox, and the `input_required` overlap rule between old paths 3 and 5 leave §7.2." An open turn that the gateway tracks from the frames replaces it.
