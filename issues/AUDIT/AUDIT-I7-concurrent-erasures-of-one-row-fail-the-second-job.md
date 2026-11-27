---
id: AUDIT-I7
status: open
kind: build-gap
severity: low
requirements: []
---

# Two erasures that select the same dead-lettered row at once fail the second erasure job instead of finding the row redacted

## Specification

§12.8 (Compliance Interfaces), **`RedactionReceipt` schema.**: "A row is redacted at most once: the erasure job selects only dead-lettered rows whose `payload` does not carry the redaction marker, and the redacted `payload` names no user, so a later erasure of another user that the same row named finds nothing left to redact in it. The table is unique on (`tenant_id`, `sequence_number`), so a second redaction of a row that two erasures select at once fails and rolls back."

## Evidence

- `pkg/audit/auditstore/redaction.go`, `RedactDeadLettered`: `UPDATE audit_log SET payload = ... WHERE tenant_id = $1 AND sequence_number = $2` carries no guard on the redaction marker, and the receipt `INSERT` in the same transaction fails on `audit_redaction_receipts_position_unique` when another erasure already redacted the row.
- `pkg/storage/deadletterredaction/redaction.go`, `RedactForUser`: a failed `redactRow` returns the error and stops the job.
- Found by the review of step 0177.S2 on 2026-11-26.
- The model of change 0180 (`tests/formal/auditchain/`, configuration `redaction`) reproduces it: job 1 selects and signs for row 2, job 2 selects row 2, job 1 commits its redaction, and job 2's receipt `INSERT` fails on the uniqueness, so `NoJobFailsOnRedactedRow` fails. The chain stays intact: `LinearChain` and the window and receipt-scan checks hold over the same configuration.

## Gap

The row stays redacted once, but the erasure job of the second user fails and is retried under the erasure-job failure handling, although nothing is left to redact for that user in the row.

## Suggested resolution

Add `AND (payload->>'redacted') IS DISTINCT FROM 'true'` to the `UPDATE`, return a sentinel when it affects no row, and have `redactRow` skip that row without writing a receipt or emitting the redaction events. Cover it with a load_local test of two concurrent erasures naming one row.
