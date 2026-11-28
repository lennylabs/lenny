---
id: AUDIT-I8
status: planned
kind: build-gap
severity: low
requirements: []
change: 0180.S3
---

# The audit immutability trigger does not check the prev_hash of an INSERT, which §11.7 says it does

## Specification

§11.7 (Audit Logging), item 3, **Concurrency control for `prev_hash`.**: "The `lenny_audit_immutability` trigger additionally rejects any INSERT whose `prev_hash` does not match the current tail's hash (under the same transaction-scoped lock), providing defense-in-depth against an application-layer bug that bypasses the lock."

## Evidence

- `migrations/0002_rls_immutability_roles.up.sql`, `lenny_audit_immutability()`: the function admits a write under `lenny.erasure_mode` and otherwise raises for the operation it fires on. It reads no `prev_hash` and no tail row.
- `migrations/0041_audit_state_machine_columns.up.sql`: the replacement function branches on `TG_OP` for `DELETE` and `UPDATE` only. No migration attaches the trigger to `INSERT` with a `prev_hash` comparison.
- The chain tail's hash is not a stored column (`pkg/audit/auditstore/auditstore.go`, `scanRow` recomputes it), and since step 0177.S3 the link to a redacted tail row is the receipt's `original_hash` rather than the tail's recomputed hash, so a trigger that compared `prev_hash` with the tail's recomputed hash would refuse the append after a redacted tail.
- Found while building step 0177.S3 on 2026-11-26.

## Gap

An INSERT whose `prev_hash` does not link to the tail is not refused by the database; only the application's per-tenant advisory lock and `sealAndInsert` produce a linking `prev_hash`. The specification states a database-side check that does not exist.

## Suggested resolution

Either add the INSERT check to the trigger, computing the tail's link the way the append path does (the receipt's `original_hash` for a redacted tail, the canonical-tuple hash otherwise), or remove the sentence from §11.7 through a change. The first needs the canonical-tuple hash in SQL (`pg_jcs_canonicalize()` exists for the payload), and a read of `audit_redaction_receipts` from the trigger.
