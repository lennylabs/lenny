# Credential Subsystem — Targeted Security Design Review

> Phase 5.6 checklist per TESTING.md §13.13. Recorded findings link
> to the commits that resolved them.

## Scope

The §4.9 credential leasing service, §11.5 idempotency around credential operations, §12.4 Redis-backed lease state, and §13.3 credential flow before the credential subsystem is exposed to first-party tenants.

## Checklist

The reviewer confirms each item before checking off. A finding entry below records anything that needs follow-up.

- [ ] Token Service mTLS material rotates without dropping in-flight leases.
- [ ] KMS-backed envelope keys are wrapped per tenant; no plain-text DEK persists to disk.
- [ ] Lease IDs are uniformly random; predictable IDs do not leak the global counter.
- [ ] Per-tenant rate limits on `acquire_lease` and `renew_lease` survive replica failover.
- [ ] Compromised lease detection wires into the §11.7 audit hash chain.
- [ ] The §4.9.1 key-rotation procedure is documented and rehearsed.

## Findings

_None yet. Each finding lists: short title, severity, the commit or PR that resolved it, and a one-line summary._

## How this file is consumed

`phase-5.6-gate` (groups.yaml) asserts this file exists and parses as Markdown. The gate does not parse the checklist items; it pins the artifact in place so the review surface is discoverable. A separate human pass walks the checklist and edits the Findings section before the §5.6 phase gate closes.
