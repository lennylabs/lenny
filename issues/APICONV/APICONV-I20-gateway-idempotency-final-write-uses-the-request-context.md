---
id: APICONV-I20
status: open
kind: build-gap
severity: medium
requirements: []
---

# The gateway's idempotency middleware stores or releases a claim with the request context, so a client disconnect leaves the claim pending for 24 hours

## Specification

[§11.5](../../spec/11_policy-and-controls.md#115-idempotency) Idempotency, **Mechanism.**: within the 24-hour window, a duplicate request with the same key returns the stored response without executing again, and a response that is not stored lets a retry execute again.

## Evidence

- `pkg/idempotency/idempotencymw/idempotency.go` passes `r.Context()` to `maybePersist` after the inner handler returns, and `maybePersist` and `skipAndRelease` call `m.store.Put` and `m.store.Release` with it. The deferred `Release` for a panicking handler also uses `r.Context()`.
- When the client disconnects while the handler runs, `r.Context()` is cancelled, and the Postgres store's `Put` or `Release` (`pkg/idempotency/idempotencymw/pgstore/pgstore.go`) fails. A failed `Put` leaves the pending row in place by design, and a failed `Release` leaves it as well.
- Every retry of that key then answers `IDEMPOTENCY_KEY_IN_FLIGHT` until the row's window ends ([APICONV-I9](APICONV-I9-a-stuck-idempotency-claim-blocks-its-key-for-24-hours.md) records the same outcome after a crash or a store failure).
- The review reported the gateway's defect alongside the `lenny-ops` one ([APICONV-I19](APICONV-I19-lenny-ops-pending-idempotency-row-outlives-a-client-disconnect-or-panic.md)).
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F7), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

A client whose connection drops after the operation ran, the case a retry with the same key exists for, receives `IDEMPOTENCY_KEY_IN_FLIGHT` for 24 hours instead of the stored response or a fresh execution.

## Suggested resolution

Make the final `Put` and `Release`, the deferred one included, with `context.WithoutCancel(r.Context())` and a bounded timeout. Add a unit test that a request whose context is cancelled after the handler returns still stores the response, and one that a released claim lets a retry execute. The lease that APICONV-I9 suggests covers the crash case and complements this fix.

Scheduling waits on the owner's answer to IP-21 on the M3 readiness decision page: whether step 0178.S6 takes the full list of findings of that review, keeps the scope that decision 19 of change 0178 approved, or splits off an urgent fix. The issue stays `open` with no `change` until that answer.
