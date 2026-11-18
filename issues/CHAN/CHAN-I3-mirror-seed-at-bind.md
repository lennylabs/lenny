---
id: CHAN-I3
status: open
kind: build-gap
severity: medium
requirements: []
---

# The coordination mirror is not refreshed at bind

## Specification

§28.3 (Registers), register-entry register, `REG-COORDMIRROR`: the writer set is the gateway sweeper, and the row is "A single-valued row per tenant and session, and a projection rather than an exclusion primitive".

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Mirror seed at bind", status `ABSENT`, anchor `#register-entry-register`, deferral `R13`.
- Surface recorded on the row: "no `coordlease` reference in `pkg/gateway/sessionserver/`".
- Revert `54292da1b` (2026-07-27) removed the specification statement that the session server seeds the mirror at bind.

## Gap

The mirror is refreshed only by the sweeper, so a barrier sent within one sweeper interval of a resume commit carries a stale generation (F-10.1.35).

## Suggested resolution

The row deferred the work to step R13, "Bind-time coordinator resolution", of `gateway-runtime-comms-remediation.md`.

R13, as re-derived on 2026-10-28, keeps a mirror refresh at bind and at the resume commit. The refresh adds a writer to `REG-COORDMIRROR`, so the specification edit lands before the code.
