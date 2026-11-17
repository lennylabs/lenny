---
id: CHAN-I8
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: proposal 0101
---

# Routing does not read the coordination mirror by session

## Specification

§28.3 (Registers), register-entry register, `REG-COORDMIRROR`: "A single-valued row per tenant and session, and a projection rather than an exclusion primitive".

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "`coordlease.GetBySession` routing read", status `UNWIRED`, anchor `#register-entry-register`, deferral `R13`.
- Surface recorded on the row: "definitions and tests only".
- Revert `54292da1b` removed `GetBySession`, and no code under `pkg/` or `cmd/` names it.

## Gap

None remains: the read the row recorded no longer exists.

## Suggested resolution

The row deferred the work to step R13, "Bind-time coordinator resolution", of `gateway-runtime-comms-remediation.md`.

Closed at filing: Proposal 0101 (D2) made the `REG-COORDLEASE` holder value the routing authority and states that `REG-COORDMIRROR` is never read for routing (§10.1.1, **Replica address:**).
