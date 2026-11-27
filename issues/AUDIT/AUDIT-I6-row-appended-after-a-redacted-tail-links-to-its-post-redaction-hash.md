---
id: AUDIT-I6
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0177.S3
---

# A row appended after a redacted tail row links to the tail's post-redaction hash, so the chain reads broken

## Specification

§12.8 (Compliance Interfaces), `DeleteByUser` step 14: the redaction "leaves `prev_hash` and the row's identity and position columns ... untouched, the chain link stays intact: each successor row's `prev_hash` remains the redacted row's preserved pre-redaction hash, and no hash is written into the redacted row or into any subsequent row's `prev_hash`."

## Evidence

- `pkg/audit/auditstore/auditstore.go`, the append path: `scanTail` reads the tenant's tail row through `scanRow`, which sets `Hash` to `audit.ComputeHash` of the row's current columns, and the new row's `PrevHash` is `audit.LinkHash(tail)`. When the tail row is a redacted row, that is its post-redaction hash (the receipt's `new_hash`) rather than its preserved pre-redaction hash.
- `pkg/audit/auditstore/redaction.go`, `applyReceipts`: the verifier sets a redacted row's `Hash` to the receipt's `original_hash`, so the successor's `prev_hash` does not link and `Verify` reports `broken` at the successor.
- `pkg/storage/deadletterredaction/redaction.go`, `redactRow`: the redaction appends `gdpr.erasure_deadletter_redacted` right after it rewrites the row, so redacting the tenant's newest audit row always produces this successor.
- The model of change 0180 (`tests/formal/auditchain/`, configuration `redaction_main`, which reads `main` at `256e036b7`) reproduces it: an erasure job redacts row 2, the newest row, and a replica's append then links row 3 to row 2's post-redaction hash, so `LinearChain` fails. Under the `S3` variant, which reads step 0177.S3 at `c1859701c`, `LinearChain` holds over 136,472 states (configuration `redaction`).
- Found while building step 0177.S2 on 2026-11-26: `TestASecondErasureOfAUserTheRedactedRowNamedFindsNothingToRedact` in `tests/component/auditstore/receipt_key_set_test.go` read `broken` at seq 2 ("row 2 prev_hash does not link to row 1") when the redacted row was the only row. The test now appends a later row first. The defect predates the step.

## Gap

When the dead-lettered row an erasure redacts is the tenant's newest audit row, the next append, which is the erasure's own `gdpr.erasure_deadletter_redacted` event, links to the redacted content. Every later chain verification reports the tenant's chain `broken` at that event, and an external verifier sees the same break.

## Suggested resolution

Have the append path link to a redacted tail row's preserved pre-redaction hash: read the tail's receipt in the append transaction and use its `original_hash`, as `applyReceipts` does for the verifier. `AppendBatch` needs the same treatment. Add a component test that redacts a tenant's newest row and verifies the chain.
