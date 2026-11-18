---
id: CHAN-I2
status: open
kind: build-gap
severity: medium
requirements: []
---

# A draining replica does not release coordination leases or mirror rows

## Specification

§28.3 (Registers), register-entry register, `REG-COORDLEASE`: "One holder per tenant and session, on a compare-and-set with a 60 second expiry". No section of §28 or §10.1 states that a draining replica releases the lease or the mirror row.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Lease or mirror release on gateway drain", status `ABSENT`, anchor `#register-entry-register`, deferral `R16`.
- Surface recorded on the row: "`cmd/lenny-gateway/runserver.go:213-252` closes stores only".

## Gap

A replica that drains closes its stores and leaves its leases to expire, so a session it held waits up to the lease expiry before another replica can take it.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

R16 recasts the item as the lease and mirror release at a terminal write. No member change plans it. The specification states no release on drain, so this issue first needs a decision on whether the release is required.
