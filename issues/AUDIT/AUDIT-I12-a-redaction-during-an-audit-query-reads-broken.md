---
id: AUDIT-I12
status: open
kind: build-gap
severity: low
requirements: []
---

# The audit query reads the rows and the receipts in separate transactions, so a redaction that commits between them reads broken

## Specification

§11.7 (Audit Logging): `broken` is a "mismatch detected and not attributable to a known outage or receipted redaction". §12.8 (Compliance Interfaces), step 14, **`RedactionReceipt` schema.**: "A row that has a receipt is a redacted row whether or not its `payload` carries the redaction marker."

## Evidence

- `Store.Rows` in `pkg/audit/auditstore/auditstore.go` reads the rows in one transaction and the receipts in another; `auditReceipts` in `pkg/gateway/api/admin/audit_query.go` reads the receipts a third time, and `Chain.VerifyRows` checks the rows of the first read against the receipts of the last. `Store.Verify` reads in the same order.
- `RedactDeadLettered` in `pkg/audit/auditstore/redaction.go` commits the payload rewrite and the receipt together, so a reader that sees the row before the commit and the receipts after it holds a row without the marker that has a receipt, which `Chain.contentCheck` reports as "has a redaction receipt but does not carry the redaction".
- The model of change 0180 (`tests/formal/auditchain/`, configuration `redaction`): the query reads the rows, an erasure job commits its redaction of row 2, and the query reads the receipts; `NoQueryFalseAlarm` fails. The same configuration without the query invariant holds over 136,472 states.
- Found while building step 0180.S1 on 2026-11-27.

## Gap

An audit query that overlaps an erasure reports a lawfully redacted row `broken` and emits the broken-chain signal that feeds `AuditChainGap`. A repeated query reads the row `redacted_gdpr`.

## Suggested resolution

Change 0180, open decision O5: read the rows and the receipts in one `REPEATABLE READ` transaction, or in one statement that joins them, as `CheckRedactionReceipts` does.
