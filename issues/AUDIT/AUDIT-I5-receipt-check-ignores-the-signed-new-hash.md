---
id: AUDIT-I5
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0177.S2
---

# A redacted audit row can be rewritten again after its receipt is signed, because no verifier compares the row with the receipt's new_hash

## Specification

§12.8 (Compliance Interfaces), `DeleteByUser` step 14: "Chain verifiers that encounter a `redacted_gdpr` row without a signature-verifying `RedactionReceipt` MUST raise `broken`", and the receipt "is the sole provenance token that distinguishes an authorized GDPR redaction from a tamper that happens to clear `payload`". The **`RedactionReceipt` schema.** signs `new_hash`, the SHA-256 of the canonical tuple after redaction, and states that it "is recorded for provenance" and "is not consulted for link continuity".

## Evidence

- `pkg/audit/chain.go`, `receiptedRedaction` and the content-hash check in `Verify`: a redacted row whose recomputed hash differs from its preserved hash passes when its receipt's signature verifies and its `original_hash`, sequence number, tenant, and event id match. `NewHash` is signed and never compared with the row.
- `pkg/audit/integrity/redaction.go`, the periodic receipt scan: the same checks, without `new_hash`.
- `migrations/0165`: `lenny_erasure` holds `UPDATE` on `audit_log.payload` and `payload_canonical_json`.
- Found by the security review of step 0177.S1 on 2026-11-26.

## Gap

A writer with the `lenny_erasure` grant, or a database administrator, can rewrite a row that a GDPR erasure already redacted to any payload that keeps the redaction marker. The chain verifier, `auditstore.Verify`, and the periodic scan still report the row `redacted_gdpr`, so the receipt does not pin the redacted content it was signed for.

## Suggested resolution

Have the chain verifier and the periodic scan also require the row's recomputed content hash to equal the receipt's `new_hash`, and state the check in §12.8 step 14. Open decision O6 of change 0177 asks the owner to choose.
