---
id: AUDIT-I13
status: planned
kind: doc-gap
severity: low
requirements: []
change: 0180.S2
---

# A receipt key rotation in one rollout lets a replica that has not restarted refuse receipts signed under the new key

## Specification

§12.8 (Compliance Interfaces), **`RedactionReceipt` schema.**, `signature_kms_key_id`: "A rotation maps `platform:audit-signing` to the new key version and the optional alias `platform:audit-verify` to the previous one, as for the §10.2 JWT signing key." §10.2 (Authentication), **Verifier key set and rotation.**, maps the verify alias to the new key in a first rollout and swaps the two mappings in a second.

## Evidence

- `docs/operator-guide/security.md`, "Redaction receipt signing key" (step 0177.S2): "map `platform:audit-signing` to the new reference and `platform:audit-verify` to the previous reference in one rollout."
- `buildReceiptSigning` in `cmd/lenny-gateway/receipt_signing.go` loads the trusted receipt keys at start and never refreshes them, and the erasure job on any replica signs under that replica's `platform:audit-signing`.
- The model of change 0180 (`tests/formal/auditchain/`, configuration `rotation_onerollout`): replica 2 restarts on the new mapping, an erasure job on replica 2 signs a receipt under K2, and replica 1, still on K1, does not trust K2 (`ReceiptKeysTrusted` fails), so its receipt scan counts the row and fires `AuditRedactionReceiptMissing`. With the two-rollout order of §10.2 (`rotation_verifyfirst`) the property holds over 33,881 states.
- Found while building step 0180.S1 on 2026-11-27.

## Gap

Every receipt key rotation that follows the documented procedure raises `AuditRedactionReceiptMissing`, a critical alert, on each replica that has not yet restarted while an erasure runs on one that has, and an external verifier that fetched `GET /v1/admin/audit-receipt-keys` from such a replica refuses the new receipts.

## Suggested resolution

Change 0180, open decision O6: state the two-rollout order in §12.8 and in the security guide: map `platform:audit-verify` to the new key, and list the key it named before in `audit.retiredReceiptKeyIds`, in a first rollout; swap the two mappings in a second.
