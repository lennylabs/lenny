---
id: ERASE-I5
status: open
kind: build-gap
severity: low
requirements: []
---

# A keyed request that finishes after the idempotency-records erasure step writes a record the completed erasure leaves behind

## Specification

[§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) (Compliance Interfaces), **`DeleteByUser` dependency-ordered deletion sequence** step 17 deletes the user's idempotency records, the `idempotency_keys` rows whose `subject` is the user's ID, under every `session_id`. [§11.5](../../spec/11_policy-and-controls.md#115-idempotency) (Idempotency), **Mechanism.** stores every response other than the ones it names as not stored, so a 4xx refusal is stored.

## Evidence

- `pkg/idempotency/idempotencymw/idempotency.go`: `maybePersist` stores every response below 500 that the request did not mark as not stored, including the `403 ERASURE_IN_PROGRESS` that `pkg/gateway/core/session/sessionserver/user_gate.go` writes for a user under processing restriction.
- `pkg/idempotency/idempotencymw/pgstore/pgstore.go`: `Put` is an `INSERT … ON CONFLICT DO UPDATE` with no check of the user's erasure state.
- `cmd/lenny-gateway/adminrouter.go`: the user eraser `idempotency_records` runs one `DeleteByUser` during the `store_deleting` phase, and nothing fences later writes for the subject.

Found by the security review of steps 0178.S1 and 0178.S2.

## Gap

A keyed request of the erased user that claims its key before the eraser runs and finishes after it, or one that arrives after it and is refused with `403 ERASURE_IN_PROGRESS`, writes a row under the erased `(tenant_id, subject)`. The erasure job completes with that row in place, and the 24-hour garbage-collection sweep removes it. A refusal record holds the key, the request hash, and the error envelope. A record of a request that claimed before the restriction took effect can hold a stored response.

## Suggested resolution

Re-run the idempotency-records deletion in the job's `verifying` phase, or skip the store write, and release the claim, when the principal's user is under processing restriction. Either needs a statement in §12.8 of when the last write of a user's record can land relative to the erasure receipt.
