# Steps: 0180 A formal model of the audit chain and redaction receipts

## Validation owed

None beyond the model's own checks.

## Steps

### 0180.S1 Model of the audit chain and redaction receipts
- status: Landed
- lanes: [non-specification]
- depends-on: [0177.S2]
- closes: []

Reviews §11.7, §12.8 step 14, and the verifiers as built through 0177.S2, writes the properties and the adversary's roles, builds the model under `tests/formal/`, runs it, and records the results and each confirmed counterexample as an open decision of this change. It starts after step 0177.S2 lands ("Ordering" in `change.md`).

### 0180.S2 The verifiers after a retention sweep and a redaction, and the receipt key rotation
- status: Planned
- ruled-ready: 2026-11-27, the owner settled open decisions O4 to O7 as decisions 3 to 6; open decision O3 (AUDIT-I9) does not touch the step
- lanes: [specification, code, documentation]
- depends-on: [0180.S1]
- closes: [AUDIT-I10, AUDIT-I11, AUDIT-I12, AUDIT-I13, AUDIT-I14]

§11.7 and §12.8 state where the verifiers start after a retention sweep and how a kept `gdpr.*` row older than the cutoff is checked (decision 3), the two rollouts of a receipt key rotation (decision 5), and the application role's control of the dead-letter state (decision 6). Then the window check, the query verifier, and the receipt scan start their link checks at the oldest row the retention window holds and assert the sentinel only at the first sequence value (decision 3), and `Store.Rows`, `Store.Verify`, and `auditReceipts` read rows and receipts in one snapshot (decision 4), with unit and component tests: a sweep that keeps `gdpr.*` rows leaves the window check passing and the oldest remaining row reading intact, and a redaction committed between the reads of an audit query no longer reads as broken. Then the security guide's rotation procedure. The model under `tests/formal/auditchain/` is rerun with the configurations that check the fixes.

### 0180.S3 A stored hash of each audit row
- status: Planned
- ruled-ready: 2026-11-28, the owner settled open decision O3 as decision 7
- lanes: [specification, code, documentation]
- depends-on: [0180.S2]
- closes: [AUDIT-I8, AUDIT-I9]

Decision 7. §11.7 and §12.8 state the stored hash, the verifiers' comparison, the receipt's `original_hash` as a cross-check of it, the append path's link to it, and the trigger's `INSERT` check. Then the migration adding the column outside every `UPDATE` grant, the append path that writes it and links to it, the trigger's `INSERT` check, and the window check, the query verifier, and the receipt scan comparing against it, with unit and component tests: an unmarked rewrite of the newest row by the erasure role reads as broken, a redaction with a verifying receipt reads intact, an `INSERT` whose `prev_hash` differs from the tail's stored hash is refused, and a kept `gdpr.*` row isolated by a retention sweep reads broken after a rewrite. Then the security guide and the external verification procedure. The model gains a configuration with the stored hash and is rerun.
