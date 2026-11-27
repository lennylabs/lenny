---
id: APICONV-I9
status: open
kind: build-gap
severity: low
requirements: []
---

# A pending idempotency claim left by a crash or a failed store blocks its key for 24 hours

## Specification

§11.5 Idempotency, **Mechanism.**

## Evidence

- `Claim` overwrites an existing row only once it has expired (`pkg/idempotency/idempotencymw/pgstore/pgstore.go`, `WHERE idempotency_keys.stored_at < $7`).
- A replica that crashes after `Claim` leaves a pending row, and a failed `Put` leaves it deliberately (`pkg/idempotency/idempotencymw/idempotency.go:546-551`).
- Every retry of that key then answers `IDEMPOTENCY_KEY_IN_FLIGHT`, which is retryable, until the 24-hour window ends.
- Found by the adversarial review of a per-subject idempotency key with a conflict error, on 2026-11-26.

## Gap

A client whose first attempt was interrupted at the wrong moment cannot complete the operation under its key for 24 hours.

## Suggested resolution

Give a pending claim a lease shorter than the window, after which a retry may take the claim over, with a claim identifier that a late `Put` or `Release` must match.
