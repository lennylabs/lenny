---
id: ERASE-I2
status: closed
kind: spec-defect
severity: medium
requirements: []
change: 0178.S2
closed: fixed
closed-by: 0178.S2
---

# User erasure leaves the user's idempotency records, and the §12.8 store table does not list them

## Specification

[§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) (Compliance Interfaces), **Storage backends in erasure scope:** lists the stores `DeleteByUser` erases, from `SessionStore` ("Sessions, task trees, delegation lineage, retry state, and the session-ID reservations of **Lifecycle fence and drain**") to the billing write-ahead buffer. The table has no row for idempotency records, and the **`DeleteByUser` dependency-ordered deletion sequence** has no step for them.

[§12.9](../../spec/12_storage-architecture.md#129-data-classification) (Data Classification) classifies them: "Idempotency records (key, request hash, stored response; [§11.5](../../spec/11_policy-and-controls.md#115-idempotency)); a stored response holds no `uploadToken`", tier "T3 — Confidential", store "Postgres".

[§11.5](../../spec/11_policy-and-controls.md#115-idempotency) (Idempotency), **Mechanism.**: "The gateway stores the key, request hash, and response in Postgres (table `idempotency_keys`, keyed by `(tenant_id, idempotency_key)`) with a TTL of 24 hours."

## Evidence

- `pkg/storage/erasure/order.go`: `canonicalRank` names every store that user erasure and tenant deletion wire, and `userExempt` names the stores user erasure leaves alone with a reason. Neither map names idempotency records, so no eraser deletes them and no exemption records why.
- `migrations/0005_idempotency_keys.up.sql`: `idempotency_keys` holds `tenant_id`, `idempotency_key`, `body_hash`, `response_status`, `response_headers`, `response_body`, and `stored_at`. No column names the user who sent the request, so the records cannot be found by user today.
- Archived change [0173](../../changes/archive/2026-11-25-0173-idempotency-stored-credentials/change.md), "SC-3": a stored response holds "session metadata, user identifiers, and, for `Resume`, task results, which the table classifies as Confidential". Since 0173 a stored response holds a redaction reference in place of an `uploadToken`, so the records no longer hold that credential, and they still hold the other response bodies.
- `pkg/idempotency/idempotencymw/pgstore`: a record past the 24-hour TTL reads as absent, and the garbage-collection sweep deletes rows by `stored_at`.

## Gap

An erasure job completes and records its verified receipt while the user's stored responses stay in `idempotency_keys` for up to 24 hours after the user's last idempotent request. Those bodies can hold the user's session ids and identifiers and, for `Resume`, Confidential task results. The specification does not name the records in the erasure scope, so the omission is a specification defect before it is a code gap.

The severity is medium rather than high because the 24-hour TTL is shorter than the 72-hour T3 erasure deadline of §12.8 **Erasure SLA enforcement.**, so the records are gone within the deadline in practice. The erasure job nevertheless reports completion while personal data remains, which §12.8 does not allow.

## Suggested resolution

Add idempotency records to the §12.8 store table and to the `DeleteByUser` sequence, deleted by `(tenant_id, subject)`, and add an eraser that the erasure job runs, with a test that a real erasure leaves no record of the user. The deletion needs a subject on each record, which change [0178](../../changes/0178-idempotency-keys-by-principal/change.md) step 0178.S1 adds, so step 0178.S2 carries this fix (Owner's direction in chat, 2026-11-25).
