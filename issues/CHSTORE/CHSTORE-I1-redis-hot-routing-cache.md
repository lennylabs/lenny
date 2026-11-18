---
id: CHSTORE-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# The Redis routing cache has no caller

## Specification

§28.5.7 (Gateway-to-store) states no routing cache. §4.2 (Session Manager) names Redis as the hot routing cache, and the §12.4 (Redis HA and Failure Modes) failure table states its fallback to Postgres.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Redis hot routing cache", status `UNWIRED`, anchor `#2857-gateway-to-store`, deferral `R13`.
- Surface recorded on the row: "zero callers outside its package".
- The routing cache package has no caller outside itself (triage, 2026-11-17).

## Gap

The specification names a routing cache that the tree does not use. The `REG-COORDLEASE` holder value is the forward target's authority.

## Suggested resolution

The row deferred the work to step R13, "Bind-time coordinator resolution", of `gateway-runtime-comms-remediation.md`.

R24, "Disposition of the remaining unwired surfaces", decides between deleting the cache and recording it as intentional. 0117 moves the package and 0132 leaves it out of its scope.
