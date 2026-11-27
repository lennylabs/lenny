---
id: AUDIT-I9
status: open
kind: build-gap
severity: medium
requirements: []
---

# The erasure role rewrites the payload of a tenant's newest audit row, and no verifier detects it

## Specification

§11.7 (Audit Logging), item 3: "This creates a tamper-evident chain — any retroactive modification or deletion of an entry breaks the chain for all subsequent entries." §12.8 (Compliance Interfaces), step 14: "the receipt is the sole provenance token that distinguishes an authorized GDPR redaction from a tamper that happens to clear `payload`."

## Evidence

- `migrations/0165_audit_log_redaction_grant.up.sql` grants `lenny_erasure` `UPDATE (payload, payload_canonical_json)` on every row, and `lenny_audit_immutability()` (`migrations/0041_audit_state_machine_columns.up.sql`) admits any `UPDATE` once the session sets `lenny.erasure_mode`, which the role sets itself.
- No column stores a row's own hash: `scanRow` in `pkg/audit/auditstore/auditstore.go` and `scanChainRows` in `pkg/audit/integrity/continuity.go` recompute it from the current columns. Only a successor's `prev_hash` pins a row's earlier content.
- `Chain.contentCheck` in `pkg/audit/chain.go` and `CheckRedactionReceipts` in `pkg/audit/integrity/redaction.go` read a row's receipt only when the row carries the redaction marker or has a receipt.
- `scanTail` links the next append to the newest row's recomputed hash, so the next row written pins the rewritten content.
- The model of change 0180 (`tests/formal/auditchain/`, configuration `erasure`) finds it in one step. From a chain whose rows 1 to 3 are committed, row 2 redacted with its receipt, the role rewrites the payload of row 3 to other content without the marker. The query verifier, the window check, and the receipt scan accept the chain, and an append that follows links to the rewritten row. `TamperDetected` and `PlainRewriteDetected` fail; with that rewrite also accepted, `TamperDetectedExceptPlainRewrite` holds with two and with three tamper steps (`erasure`, `erasure_three`).
- Found while building step 0180.S1 on 2026-11-27.

## Gap

A holder of the `lenny_erasure` database credentials replaces the content of a tenant's newest audit row with anything that does not carry the redaction marker, and every verifier reads the chain as intact, then and after later appends. Step 0177.S3 covers the same rewrite when it writes the marker, which the receipt scan counts as a row without a receipt.

## Suggested resolution

Change 0180, open decision O3. The recommendation is a trigger guard: under `lenny.erasure_mode`, `lenny_audit_immutability()` admits an `UPDATE` of the payload columns only of a row whose `ocsf_translation_state` is `dead_lettered` and whose payload does not carry the marker, and only to a payload that carries it. Every rewrite the role can then make is a redaction-marked row, which the receipt scan reports without a verifying receipt. The model's `erasure_guard` configuration holds under that guard.
