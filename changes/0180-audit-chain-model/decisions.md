# Decisions: 0180 A formal model of the audit chain and redaction receipts

## Open decisions

Each decision below comes from a counterexample of the model that step 0180.S1 checked against the specification and the code ("Model results" in `change.md`). The fix of each lands in another change, as "Specification and non-specification changes" in `change.md` states.

### O3. A rewrite of the newest row by the erasure role (AUDIT-I9)

`lenny_erasure` may `UPDATE` the payload columns of any row under `lenny.erasure_mode`, which it sets itself. A rewrite of the newest row that leaves out the redaction marker changes no `prev_hash` that another row carries, so the query verifier, the window check, and the receipt scan accept it, and the next append links to the rewritten content (counterexample R4).

- (a) The `lenny_audit_immutability` trigger admits an erasure-mode `UPDATE` of the payload columns only of a row whose `ocsf_translation_state` is `dead_lettered` and whose payload lacks the marker, and only to a payload that carries the marker. Every rewrite the role can make is then a redaction-marked row, which the receipt scan reports when no verifying receipt matches it. The model's `erasure_guard` configuration holds under this rule. It changes one trigger function and the §11.7 item 7 text.
- (b) Store each row's own hash in an immutable column at `INSERT`, outside the erasure grant, and have the verifiers compare the recomputed hash with it unless a verifying receipt explains the difference. It also gives AUDIT-I8 a stored tail hash to compare with. It changes the schema, the append path, and every verifier.
- (c) State the rewrite as a limit of the erasure role in §11.7, with the SIEM copy as the only detection.

Recommendation: (a). It closes the rewrite with the smallest change, and the model checks it.

### O4. The verifiers after a retention sweep (AUDIT-I10, AUDIT-I11)

The retention sweep deletes old rows that are not `gdpr.*` and keeps the `gdpr.*` rows between them, so the next kept row's `prev_hash` names a deleted row. The window check of the periodic and startup checks reports the chain broken, fires `AuditChainGap`, and under `audit.hardFailOnDrift` shuts the gateway down, once the newest `audit.startupChainCheckEntries` rows reach back to a kept `gdpr.*` row (R7). The query verifier also requires the first row it reads to carry the genesis sentinel, so after any sweep it reports the oldest remaining row broken (R8).

- (a) The verifiers check links only from the oldest row the general retention window still holds: a row older than the cutoff that the sweep keeps, a `gdpr.*` row, is checked alone (its content and receipt), as the remnant of a deleted tenant is, and the first row checked carries the sentinel only when it is the first value of the tenant's sequence. A deletion of rows older than the cutoff then reads as the sweep, which is limit L3 already.
- (b) Keep `gdpr.*` rows in a chain or table of their own, so that the sweep deletes a prefix of each chain, and assert the sentinel only for the first sequence value. It changes the schema, the append path of the `gdpr.*` events, and the external verification §12.8 describes.
- (c) The sweep records, in a row of the chain, the hash that each kept row after a hole links to, and the verifiers accept a hole that such a record covers. The record is written by the role that holds `DELETE`, so it adds no detection the role could not defeat.

Recommendation: (a). It needs no schema change and matches the treatment the specification already gives the remnant of a deleted tenant.

### O5. An audit query that overlaps a redaction (AUDIT-I12)

The audit query API reads the rows and the receipts in separate transactions, so a redaction that commits between the reads leaves a row without the marker that has a receipt, which reads `broken` and emits the broken-chain signal (R5).

- (a) Read the rows and the receipts in one `REPEATABLE READ` transaction, or in one statement that joins them, as the receipt scan does, in `Store.Rows`, `Store.Verify`, and `auditReceipts`.
- (b) Retry a query that reports a row `broken` once, and report the second result.

Recommendation: (a). It removes the race rather than narrowing it.

### O6. The order of a receipt key rotation (AUDIT-I13)

The security guide rotates the receipt key in one rollout. A replica that has not restarted does not trust the new key, so a receipt an updated replica signs during the rollout counts as missing on it and fires `AuditRedactionReceiptMissing` (R6). §12.8 says the rotation runs "as for the §10.2 JWT signing key", whose procedure takes two rollouts.

- (a) §12.8 and the security guide state two rollouts: first map `platform:audit-verify` to the new key and list the key it named before in `audit.retiredReceiptKeyIds`; then swap the two mappings. The model's `rotation_verifyfirst` configuration holds.
- (b) Keep one rollout and document the alert it raises while an erasure runs during the rollout.
- (c) Refresh the trusted receipt keys from the KMS while the gateway runs.

Recommendation: (a). It is the order the specification already points to, and the model checks it.

### O7. The application role and the dead-letter state (AUDIT-I14)

`lenny_app` sets `ocsf_translation_state`, which selects the rows step 14 redacts. A compromised application role marks an ordinary row `dead_lettered`, and the next erasure of a user the row names redacts it with a valid receipt (R9).

- (a) State in §11.7 and §12.8 that the application role, which runs the translator, decides which rows are dead-lettered, so a compromised application role can expose a row that names an erased user to the redaction, and that the SIEM copy keeps the original.
- (b) Have the trigger hold the translator's state machine, so that `dead_lettered` is reachable only from `pending` and `retry_pending`. It stops the change of a row already translated, and leaves rows not yet translated exposed.
- (c) Have the erasure job redact a row only when the translator's dead-letter record for it exists outside `audit_log`, such as the class 2004 receipt the SIEM received.

Recommendation: (a). The application role is the translator, so (b) narrows the exposure without removing it, and (c) makes erasure depend on the SIEM.

## Decisions

1. **The modelling tool, and the model kept for reruns (O1).** TLA+ with the TLC model checker, reusing the runner and the pinned `tla2tools.jar` of change 0169. The model is stored under `tests/formal/auditchain/` with its module, one configuration file per scenario, a `run.sh` that runs every configuration, and a `README.md` that states what the model covers, the properties in plain words, how to run it, and its results, as `tests/formal/tokenconcurrency/` does, so that a later change that edits a rule the model describes updates the model and reruns it. The `formal/` entry of `tests/README.md` names the model. Owner's answer in chat, 2026-11-26, with the words "go ahead with 1-4" to the recommendation of TLA+, and "Store the TLA+ models so that we can update and re-run them later if we have to make changes". The options not chosen were Alloy, as change 0165 used, and a model-based property test in Go.
2. **Program membership (O2).** The change stays outside the v1 program, as change 0172 does, and "Formal verifications owed before M3" in [the v1 program](../programs/v1.md) tracks it as FV-3. Owner's answer in chat, 2026-11-26, with the words "go ahead with 1-4" to that recommendation. The option not chosen was membership in milestone M3.
