# A formal model of the audit chain and redaction receipts

Status: `In progress`, revision 1, 2026-11-27.

## Problem

The audit log is a hash chain per tenant (§11.7): each row records the hash of the row before it, and a chain verifier and a periodic scan check the links. A GDPR erasure (§12.8 step 14) rewrites the payload of the rows that name the erased subject and writes a redaction receipt that records the row's hash before (`original_hash`) and after (`new_hash`), signed inside the KMS under `platform:audit-signing` (change [0177](../0177-shared-upload-token-keys/change.md), decision 5). The verifiers accept a redacted row whose receipt verifies against a trusted key (0177 decision 9) and whose content matches the signed `new_hash` (0177 decision 8). The guarantees depend on who can write which rows and columns, on concurrent appends and redactions, and on key rotations across a retention period of years. The review of 0177.S1 found a gap by reasoning ([AUDIT-I5](../../issues/closed/AUDIT/AUDIT-I5-receipt-check-ignores-the-signed-new-hash.md)). The owner asked on 2026-11-26 that the audit mechanisms be verified formally before the implementation of M3 starts, "to make sure there are no gaps".

## Scope

The change builds a formal model of the audit chain of a tenant, appends from several replicas, redactions with their receipts, rotations of the receipt signing key and the trusted key set, the chain verifier, and the periodic scan, against an adversary with each of the database roles the specification names: the application role, the role holding the `lenny_erasure` grant, and a database administrator. It checks these properties: concurrent appends keep one linear chain; a rewrite, deletion, insertion, or reordering of a row is detected unless it is a redaction with a valid receipt; a redacted row whose content differs from its receipt's `new_hash` is detected; a receipt signed by a key outside the trusted set is refused; every receipt signed by a trusted key verifies across rotations for the retention period; and the model states which tampering each role can do undetected, such as deleting the newest rows of a chain.

It leaves out:

- The cryptography. A hash is modelled as collision-free and a signature as unforgeable.
- The content of the audit events and the OCSF mapping, which the model treats as opaque payloads.

## Ordering

The change starts after step 0177.S2 lands, so the model reads the design with decisions 7, 8, and 9 of change 0177 built. The owner directed on 2026-11-26 that 0177.S2 be built first. Step 0180.S1 started on 2026-11-27, after 0177.S2 landed, and reads step 0177.S3 from its unmerged branch ("Current state").

## Terms

| Term | Meaning |
| :-- | :-- |
| Receipt | The record of a redaction that §12.8 step 14 defines, with `original_hash`, `new_hash`, the signing key, and the signature. |
| Model | The TLA+ specification this change adds under `tests/formal/auditchain/`. |
| Counterexample | A trace of the model that breaks one of the properties. |
| Ghost tables | The audit rows and receipts as the legitimate processes alone left them. The model compares the tables with them to tell what the adversary changed. |
| Stated limit | A tampering a role can do without any verifier reporting it, which the specification accepts. |

## High-level design

The model describes one tenant's audit chain as a table of rows keyed by sequence number and a table of receipts, and the processes that act on them: replicas that append under the per-tenant advisory lock, erasure jobs that redact a dead-lettered row and sign its receipt, the retention sweep, the operator who rotates the receipt signing key, and the verifiers. Each action is one SQL statement or one commit, so the model explores every order in which the statements of concurrent processes interleave. A hash is the tuple of its inputs, so it is collision-free, and a signature is a flag that only the holder of the signing key sets.

An adversary holds one database role at a time: the application role, the erasure role, the erasure role together with the receipt signing key, or a superuser. Its statements are bounded in number, and the ghost tables record what the legitimate processes alone wrote. A property states that while the tables differ from the ghost tables in a way the configuration does not list as a stated limit, one of the verifiers reports a break. TLC checks each property over every reachable state of each configuration, and each counterexample was checked against the specification and the code before it was recorded as a defect.

`tests/formal/auditchain/README.md` maps each action to the code, states each property and each role's capabilities, lists the configurations, and says how to rerun them.

## Requirements

None. The change adds a model and tests of the specification and changes no requirement.

## Current state

The model reads two commits. `main` at `256e036b7` holds §11.7, §12.8 step 14, and steps 0177.S1 and 0177.S2: receipts signed inside the KMS under `platform:audit-signing`, the trusted key set of the two aliases and `audit.retiredReceiptKeyIds`, and the check of a redacted row against its receipt's `new_hash`. Branch `change/0177.S3-S4-audit-tail-and-key-incidents` at `c1859701c` holds step 0177.S3, which is built and not merged: the append path links a row written after a redacted newest row to that row's receipt `original_hash`, a redaction-marked row without a receipt reads `broken` anywhere, and the window check of the periodic and startup checks applies receipts. The model's `S3` variant is the second commit and its `Main` variant the first; every configuration but `redaction_main` uses `S3`.

The code the model describes:

- The append path: `Append`, `AppendInTx`, `AppendBatch`, `sealAndInsert`, and `scanTail` in `pkg/audit/auditstore`, under the advisory lock of `acquireAuditLock`.
- The redaction: `RedactForUser` and `redactRow` in `pkg/storage/deadletterredaction`, and `DeadLetteredForUser`, `RedactDeadLettered`, `Receipts`, and `applyReceipts` in `pkg/audit/auditstore/redaction.go`.
- The verifiers: `Chain.Verify` and `Chain.VerifyRows` in `pkg/audit/chain.go`, which `Store.Verify` and the audit query API run over every row; `verifyChainWindow` in `pkg/audit/integrity/continuity.go`, which the startup and periodic checks run over the newest `audit.startupChainCheckEntries` rows; and `CheckRedactionReceipts` in `pkg/audit/integrity/redaction.go`, the periodic receipt scan.
- The keys: `ReceiptKeySet` in `pkg/audit/receipt.go` and `buildReceiptSigning` in `cmd/lenny-gateway/receipt_signing.go`, which loads the trusted keys at start.
- The roles: the grants of migrations 0002, 0041, 0160, and 0165, and the `lenny_audit_immutability` and `lenny_erasure_insert_guard` triggers.
- The retention sweep: `PruneRetention` in `pkg/audit/auditstore/auditstore.go`.

Open issues the model touches: AUDIT-I6 (fixed by 0177.S3), AUDIT-I7, and AUDIT-I8 (on the 0177.S3 branch). SESS-I1, about the chart value `gateway.maxCreatedStateTimeoutSeconds`, concerns the upload-token lifetime and is outside the audit chain.

## Detailed design

### Rows, hashes, and transactions

A row holds the hashed columns of §11.7 item 3 that differ between rows: `prev_hash`, the payload with or without the redaction marker, the event type (a `gdpr.*` row or another row), and `created_at`. The dead-letter state is a flag that is not hashed. A statement reads the committed rows and its own transaction's rows; `nextval` is not rolled back; an `INSERT` fails on a committed row at the same key. The model treats `payload` and `payload_canonical_json` as one column (AUDIT-I15) and treats the genesis link as a constant (AUDIT-I16).

### Processes

- **Appends.** Lock, the tail read of `scanTail`, the receipt read of `scanTail` under `S3`, `nextval`, the `INSERT` with the trigger the configuration names, and the commit; a batch of two rows repeats the tail read inside one transaction. A failure rolls the transaction back.
- **Erasure jobs.** Select a dead-lettered row without the marker, sign `original_hash` and `new_hash` under the job's replica's signing key, commit the payload rewrite and the receipt together, then append the `gdpr.*` event through the append path.
- **Retention.** A clock and one sweep that deletes rows that are not `gdpr.*` and are older than a cutoff.
- **Key rotation.** An operator who changes the alias map and the retired list once every replica runs the previous one, and replicas that restart one at a time, in one rollout (the security guide) or in two (§10.2).
- **Verifiers.** The query verifier reads the rows, the receipts, and the receipts again; the window check reads the newest rows, then the receipts; the receipt scan is one statement. Each is also a function of the committed tables, which the properties evaluate in every state.
- **Adversary.** The statements each role's grants allow, bounded by `MaxTamper`; `README.md` lists them.

### Properties

| Property | Statement |
| :-- | :-- |
| P1 | Concurrent appends keep one linear chain (`LinearChain`, `NoFork`). |
| P2 | A rewrite, deletion, insertion, or reordering of a row is detected unless it is a redaction with a valid receipt or a stated limit (`TamperDetected`, `PlainRewriteDetected`, `TamperDetectedExceptPlainRewrite`); for a superuser, a row at or before a redacted row whose receipt is in place cannot be changed without detection (`AnchoredByReceipt`). |
| P3 | A redacted row whose content differs from its receipt's `new_hash` is detected (`RedactionBoundToNewHash`). |
| P4 | A receipt signed by a key outside the trusted set, or not signed, is refused (`ForgedReceiptRefused`). |
| P5 | Every receipt verifies on every replica across rotations (`ReceiptKeysTrusted`). |
| P6 | Without an adversary no verifier reports a break (`NoWindowFalseAlarm`, `NoScanFalseAlarm`, `NoQueryFalseAlarm`). |
| | No append that holds the lock is refused (`NoRefusedAppend`); no erasure job fails on a row another job redacted (`NoJobFailsOnRedactedRow`); every legitimate redaction rewrote a row the translator dead-lettered (`RedactedOnlyTranslatorDeadLetters`). |

### Stated limits

`README.md`, "Stated limits", states each in full.

- **L1.** A role that may `INSERT` appends a row that links to the newest row: the application role any event, the erasure role a `gdpr.*` event.
- **L2.** A role with `DELETE` removes the newest rows; after the next append they read as the benign `gap_suspected` that §11.7 **Authoritative total order** defines.
- **L3.** A role with `DELETE` removes the oldest rows, which the window check cannot tell from the retention sweep.
- **L4.** The holder of the erasure grants and the receipt signing key rewrites a row and signs a matching receipt; the receipt key is the authority §12.8 names.
- **L5.** A superuser rebuilds the chain after the newest redacted row whose receipt is in place; §11.7 **Superuser bypass mitigation** leaves its detection to the SIEM copy. The receipts pin every row before them (`AnchoredByReceipt` holds).
- **L6.** The operator keeps each retired receipt key in `audit.retiredReceiptKeyIds` for `audit.gdprRetentionDays`, as §12.8 states; a key left out makes its receipts read `broken` (`rotation_unlisted`).

## Specification and non-specification changes

None to the specification. The model and its runner are non-specification files under `tests/formal/auditchain/`. A confirmed counterexample becomes an open decision of this change and an issue under `issues/AUDIT/`, and its fix lands in change 0177 or another change.

## Documentation

The `formal/` entry of `tests/README.md` names the new model, and `tests/formal/auditchain/README.md` describes it.

## Tests

The model's checks are the tests. Their results are recorded in this file per configuration, as 0169 records its results.

## Model results

Step 0180.S1 ran every configuration of `tests/formal/auditchain/` on 2026-11-27 with TLC `v1.7.4` (`tla2tools.jar` SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`), 2 workers per run, in the `eclipse-temurin:21-jdk` container the runner starts, with `-deadlock`. Every run finished. TLC stops at the first violation, so each run that found one was repeated with the invariants it had not violated, and the violated invariants were run alone to obtain each counterexample. The distinct states are those of the run that explored the whole state space; a run that stops at a violation explores part of it.

### Bounds

At most 5 sequence numbers and 3 initial rows; 2 appending replicas with batches of 2 rows (`appends`), otherwise 1; 2 erasure jobs; 1 failed transaction (`appends`); 1 retention sweep over a clock of 2 values; 2 tamper actions per behavior, 3 in `erasure_three` and `dba`; 2 replicas and 2 rotations (4 operator steps in two rollouts each) in the rotation configurations; the window check reads every row.

### Runs

| Configuration | Distinct states | Time | Violated |
| :-- | --: | --: | :-- |
| `appends` | 4,038 | 3 s | None |
| `bypass` | (stops at 67) | 3 s | `LinearChain`, `NoFork` (R1) |
| `bypass_trigger` | 53 | 3 s | None |
| `redaction` | 136,472 | 6 s | `NoQueryFalseAlarm` (R5), `NoJobFailsOnRedactedRow` (R10) |
| `redaction_main` | 135,226 | 5 s | `LinearChain` (R2), `NoWindowFalseAlarm` (R3), `NoQueryFalseAlarm` (R5), `NoJobFailsOnRedactedRow` (R10) |
| `redaction_naive_trigger` | 81 | 1 s | `NoRefusedAppend` (R11) |
| `redaction_trigger` | 90 | 1 s | None |
| `retention` | 961 | 1 s | `NoWindowFalseAlarm` (R7), `NoQueryFalseAlarm` (R8) |
| `retention_nogdpr` | 961 | 1 s | `NoQueryFalseAlarm` (R8) |
| `app` | 82,812 | 5 s | `RedactedOnlyTranslatorDeadLetters` (R9) |
| `erasure` | 11,632 | 2 s | `TamperDetected`, `PlainRewriteDetected` (R4) |
| `erasure_three` | 246,715 | 7 s | None |
| `erasure_guard` | 7,194 | 2 s | None |
| `signer` | 7,194 | 1 s | None |
| `dba` | 41,878 | 2 s | None |
| `rotation_onerollout` | 20,705 | 2 s | `ReceiptKeysTrusted`, `NoScanFalseAlarm`, `NoQueryFalseAlarm` (R6) |
| `rotation_verifyfirst` | 33,881 | 3 s | None |
| `rotation_unlisted` | 33,881 | 2 s | `ReceiptKeysTrusted`, `NoScanFalseAlarm`, `NoQueryFalseAlarm` (L6) |

### Results by property

| Property | Result |
| :-- | :-- |
| P1 Linear chain | Holds for every append path that takes the advisory lock (`appends`, `redaction`). A writer that skips the lock forks the chain while the trigger checks no `prev_hash` (R1); with the check §11.7 states, taken under the lock and comparing with the receipt-aware link, it holds (`bypass_trigger`). Under `Main` an append after a redacted newest row breaks the link (R2). |
| P2 Tampering detected | Holds for the application role except a linked append (L1, `app`), for the superuser as `AnchoredByReceipt` (`dba`), and for the signer except receipted rewrites (L4, `signer`, under the guard of O3). Violated for the erasure role by a rewrite of the newest row (R4); with that rewrite also accepted it holds with two and three tamper actions (`erasure`, `erasure_three`), and under the trigger guard of O3 (a) it holds (`erasure_guard`). |
| P3 Bound to `new_hash` | Holds in every adversary configuration. |
| P4 Untrusted key refused | Holds in every adversary configuration. |
| P5 Receipts verify across rotations | Holds with the two-rollout order of §10.2 (`rotation_verifyfirst`). Violated during a one-rollout rotation (R6) and when the operator does not list a retired key (L6). |
| P6 No false alarm | The receipt scan holds without rotation. The window check holds under `S3` without retention; it fails under `Main` at a redacted row (R3) and after a sweep that keeps a `gdpr.*` row (R7). The query verifier fails when a redaction overlaps a query (R5) and after any sweep (R8). |

### Counterexamples

Each was checked against the specification and the code before it was recorded. Two violations of the first runs were modelling errors and were fixed in the model: a row deleted and replaced at the same sequence number by a linked `gdpr.*` insert was classified as a rewrite rather than as limits L2 and L1, and a valid receipt the signer wrote for a row it inserted was classified as an unrelated receipt change rather than as limit L4.

- **R1. A writer that skips the lock forks the chain (P1, AUDIT-I8, `bypass`).** The writer that takes the lock reads row 1 as the tail and holds its row 2 uncommitted; the writer without the lock reads row 1 as well; both commit, and rows 2 and 3 carry the same `prev_hash`. The verifiers report the fork, but the trigger that §11.7 says refuses it does not exist. AUDIT-I8 is filed on the unmerged branch of 0177.S3, so this record carries the trace rather than the issue file. `redaction_naive_trigger` shows the resolution AUDIT-I8 names: a check against the tail's recomputed hash refuses a legitimate append (R11), and the receipt-aware check does not (`redaction_trigger`).
- **R2. An append after a redacted newest row does not link (P1, AUDIT-I6, `redaction_main`).** Under `Main`, an erasure job redacts row 2, the newest row, and a replica's append links row 3 to row 2's post-redaction hash. Step 0177.S3 fixes it; the `S3` variant holds. AUDIT-I6 gains the trace.
- **R3. The window check without receipts (P6, `redaction_main`).** Under `Main`, `verifyChainWindow` does not apply receipts, so the row after a redacted row reads broken in every periodic check. The review of 0177.S3 found it, and commit `64d3b6dfc` on that branch fixes it; the `S3` variant holds.
- **R4. The erasure role rewrites the newest row (P2, AUDIT-I9, `erasure`).** From rows 1 to 3 with row 2 redacted, the role rewrites row 3's payload to other content without the marker. No successor pins row 3's earlier hash, the receipt scan reads only marked or receipted rows, and the next append links to the rewritten row. Open decision O3.
- **R5. A redaction during an audit query (P6, AUDIT-I12, `redaction`).** The query reads the rows, an erasure job commits its redaction of row 2, and the query reads the receipts: row 2 is read without the marker and has a receipt, which reads `broken`. Open decision O5.
- **R6. A one-rollout receipt key rotation (P5, AUDIT-I13, `rotation_onerollout`).** Replica 2 restarts on `platform:audit-signing` = K2, an erasure job on replica 2 signs under K2, and replica 1, still trusting K1 only, counts the row in its receipt scan. Open decision O6.
- **R7. Retained `gdpr.*` rows after a sweep (P6, AUDIT-I10, `retention`).** Rows 1 and 3 are ordinary, row 2 is a `gdpr.*` row, and a replica appends row 4; the sweep deletes rows 1 and 3. Row 4 links to the deleted row 3, so the window check reports the chain broken, which fires `AuditChainGap` and, under `audit.hardFailOnDrift`, shuts the gateway down. Open decision O4.
- **R8. The first row after a sweep (P6, AUDIT-I11, `retention`, `retention_nogdpr`).** The sweep deletes rows 1 to 3 and keeps row 4; the query verifier requires row 4 to carry the genesis sentinel and reports it `broken`. Open decision O4.
- **R9. The application role marks a row dead-lettered (AUDIT-I14, `app`).** The role sets row 1, which the translator did not dead-letter, to `dead_lettered`, and an erasure job redacts it with a valid receipt. Open decision O7.
- **R10. Two erasures of one row (AUDIT-I7, `redaction`).** Both jobs select row 2; the first commits; the second fails on the receipt uniqueness. The chain stays intact. AUDIT-I7 gains the trace.
- **R11. A trigger that compares with the recomputed tail hash (AUDIT-I8, `redaction_naive_trigger`).** A replica reads row 2 as the tail under the lock; an erasure job, which takes no advisory lock, redacts row 2; the replica's `INSERT` carries row 2's pre-redaction hash, which the check against the recomputed hash refuses. This is the trap AUDIT-I8's evidence names; the resolution it suggests, the receipt-aware link, holds.

### Found by reading

Two defects found while mapping the model to the code are outside what the model checks, and are filed: AUDIT-I15 (the verifiers hash `payload`, and `payload_canonical_json`, which §11.7 names as the hash input, is read nowhere) and AUDIT-I16 (the first row links to a fixed all-zero sentinel rather than to the per-tenant genesis hash of §11.7).
