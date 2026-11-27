# Audit chain model

`AuditChain.tla` is a TLA+ model of the per-tenant audit hash chain of §11.7, the in-place dead-letter redaction of §12.8 step 14 with its signed `RedactionReceipt`, the rotation of the receipt signing key, the chain verifiers, and the periodic receipt scan, checked against an adversary that holds each database role the specification names. Change 0180 (`changes/0180-audit-chain-model/`) builds it, and its `change.md` records the results and the open decisions the counterexamples raised.

The model is a design check. No tier of `lenny-test` runs it, and no Go test reads it. A change that edits a rule the model describes updates the model and reruns it.

## Revisions read

The model was read against two commits:

| Branch | Commit | What it holds |
| :-- | :-- | :-- |
| `main` | `256e036b7` | §11.7, §12.8 step 14, steps 0177.S1 and 0177.S2, and the code of the `Main` variant. |
| `change/0177.S3-S4-audit-tail-and-key-incidents` | `c1859701c` | Step 0177.S3: the append path links a row written after a redacted newest row to that row's receipt `original_hash`, a redaction-marked row without a receipt reads `broken` anywhere, and the window check of `pkg/audit/integrity/continuity.go` applies receipts. The `S3` variant, which every configuration but `redaction_main` uses. |

## Language

The model is plain TLA+ rather than PlusCal. Each action is one SQL statement or one commit of the code, and the adversary's actions are nondeterministic choices over rows, hashes, and keys, which PlusCal's process structure expresses less directly. The module needs no translation step, so the runner has none to check.

## What the model covers

One tenant's chain is modelled. A row holds the hashed columns of §11.7 item 3 that differ between rows: `prev_hash`, the payload with or without the redaction marker, the event type (a `gdpr.*` row or another row), and `created_at`. The sequence number is the row's key, as the primary key `(tenant_id, sequence_number)` makes it. `ocsf_translation_state = 'dead_lettered'` is a flag on the row and is not hashed.

### Abstractions

- **Hashes.** A row's hash is the tuple of its sequence number and hashed columns, so two rows hash alike only when every hashed column is equal. The hash is collision-free, and any party that knows a row computes its hash.
- **Signatures.** A receipt carries the key identifier it names and a flag that is true only when the holder of that key signed exactly the receipt's fields. A role without the KMS `Sign` grant produces a receipt with a valid signature only under a key of its own (`Kadv`), or a receipt that names a platform key without a valid signature.
- **Row identity.** The row `id` is folded into the content: a row written again with other content hashes differently.
- **Payload columns.** `payload` and `payload_canonical_json` are one column. The code hashes `payload` and reads `payload_canonical_json` nowhere; AUDIT-I15 records the difference.
- **Time.** `created_at` is a small clock that only the retention configurations advance. The SIEM delivery guard of the retention sweep, which keeps rows the forwarder has not acknowledged, is left out; it only keeps more rows.
- **Transactions.** A statement sees the rows committed before it and its own transaction's rows (`READ COMMITTED`). `nextval` is not rolled back, so a failed transaction leaves a sequence gap. An `INSERT` fails on a committed row at the same key.

### Actions and code

| Action | Code |
| :-- | :-- |
| `WLock` | `acquireAuditLock` / `AcquireLockInTx`: `pg_advisory_xact_lock(hashtext('audit:' \|\| tenant_id))`. The writer of `BypassWriter` skips it, standing for an application path that does not take the lock. |
| `WReadTail` | `scanTail`, the `SELECT ... ORDER BY sequence_number DESC LIMIT 1`, which sees the transaction's own earlier rows in `AppendBatch`. |
| `WReadTailReceipt` | `scanTail` under S3: `tailReceiptHash` for a redaction-marked tail, then `markRedacted` and `audit.LinkHash`. Under `Main`, `LinkHash` of the tail's recomputed hash. |
| `WNextVal` | `SELECT nextval(audit_seq_...)` in `sealAndInsert`. |
| `WInsert` | The `INSERT INTO audit_log` of `sealAndInsert`, with the `lenny_audit_immutability` trigger. `TriggerMode = "none"` is the trigger as built (AUDIT-I8); `"naive"` compares `prev_hash` with the tail's recomputed hash; `"receiptAware"` compares it with the link the append path computes. |
| `WCommit`, `WAbort` | `COMMIT` and `ROLLBACK` of `pgtenant.InTx` in `Append`, `AppendInTx`, and `AppendBatch` (`BatchSize = 2`). |
| `JSelect` | `DeadLetteredForUser`: a dead-lettered row without the marker, its hash taken as `original_hash`. |
| `JSign` | `redactRow` and `signReceipt` in `pkg/storage/deadletterredaction`: `new_hash` over the redacted tuple, signed inside the KMS under the key `platform:audit-signing` names on the job's replica. |
| `JRedact` | `RedactDeadLettered`: the payload `UPDATE` and the receipt `INSERT` in one transaction under `lenny.erasure_mode`, failing on a missing row (`ErrNotFound`) or on `audit_redaction_receipts_position_unique`. The job then appends `gdpr.erasure_deadletter_redacted` through the append actions. |
| `Tick`, `Sweep` | `PruneRetention`: one `DELETE` of the rows that are not `gdpr.*` and are older than the cutoff. |
| `QReadRows`, `QReadRcptA`, `QReadRcptB` | `handleListAuditEvents`: `Store.Rows` (the rows, then `Receipts` for `applyReceipts`), then `auditReceipts`, then `Chain.VerifyRows`. `Store.Verify` reads in the same order and reports the same rows `broken`. |
| `WvReadRows`, `WvReadRcpt` | `CheckChainContinuityRecent` from `PeriodicCheck.CheckOnce`: `loadRecentChainRows`, then `applyChainReceipts`, then `verifyChainWindow`. |
| `Missing` (a state function) | `CheckRedactionReceipts`, one statement, from `PeriodicCheck.CheckOnce`. |
| `OperatorStep`, `Restart` | The operator's change of `kms.aliases` and `audit.retiredReceiptKeyIds`, and a gateway restart, which loads them in `buildReceiptSigning` and holds them for the life of the process. |
| `AInsert`, `AUpdate`, `ARelink`, `ADelete`, `AInsertRcpt`, `ADeleteRcpt`, `AMarkDL` | The adversary's statements, below. |

The verifiers are also state functions of the committed tables: `QBroken` is `Chain.VerifyRows` and `Chain.Verify`, `WBroken` is `verifyChainWindow` over the newest `WindowN` rows, and `Missing` is the receipt scan. `Detected(T)` holds when any of them reports a break under the trusted key set `T`. The window check asserts the genesis sentinel only for sequence number 1, and checks links only; the query verifier asserts the sentinel for whatever row comes first, and also checks each row's content against its receipt.

### The adversary's capabilities per role

| Role | Statements | Source |
| :-- | :-- | :-- |
| `app` (`lenny_app`) | `INSERT` of any `audit_log` row at any free sequence number with any `prev_hash` the trigger admits, `UPDATE` of `ocsf_translation_state`, and `SELECT` on the receipts. No `UPDATE` of the payload or `DELETE`, and no receipt `INSERT`. | Migrations 0002, 0041, and 0160; §11.7 item 1. |
| `erasure` (`lenny_erasure`) | `UPDATE (payload, payload_canonical_json)` of any row, `DELETE` of any row, `INSERT` of `gdpr.*` rows (`lenny_erasure_insert_guard`), and `INSERT` of receipts, each under `SET LOCAL lenny.erasure_mode = 'true'`, which the role sets itself. No `UPDATE` of `prev_hash`, the identity columns, or receipts, and no KMS grant. | Migrations 0002, 0160, and 0165; §11.7 item 7; §12.8 step 14. |
| `signer` | The erasure grants and the KMS `Sign` grant on the key `platform:audit-signing` names: the erasure job's own authority. | §12.8 **`RedactionReceipt` schema.** |
| `dba` | Any statement on any column of either table, with the triggers disabled. No KMS grant. | §11.7 **Superuser bypass mitigation.** |

Each adversary action counts against `MaxTamper`, and the ghost tables `gLog` and `gRcpt` record what the legitimate processes alone wrote.

## Properties

| Property | Invariant | What it states |
| :-- | :-- | :-- |
| P1 | `LinearChain`, `NoFork` | Concurrent appends keep one linear chain: each row's `prev_hash` is the link of the row before it in sequence order (the receipt's `original_hash` for a redacted row), and no two rows carry the same `prev_hash`. |
| P2 | `TamperDetected`, `PlainRewriteDetected` | While the tables differ from the ghost tables by an effect the configuration does not list in `Accepted`, the chain verifier, the window check, or the receipt scan reports a break. The effects are classified by what the verifiers can see of them; the module lists the classes. |
| P2 for `dba` | `AnchoredByReceipt` | A row at or before a redacted row whose legitimate receipt is still in place cannot be changed, added, or removed, other than from the head of the chain, without detection. |
| P3 | `RedactionBoundToNewHash` | A row whose receipt's `new_hash` differs from the row's content hash reads `broken` in the chain verifier and counts in the receipt scan. |
| P4 | `ForgedReceiptRefused` | A receipt that does not verify under a trusted key, whether unsigned or signed by a key outside the trusted set, reads `broken` and counts in the receipt scan. |
| P5 | `ReceiptKeysTrusted` | Every receipt verifies under the trusted keys of every replica, across rotations. |
| P6 | `NoFalseAlarm` | Without an adversary, no verifier run reports a break, and on every replica the chain verifier, the window check, and the receipt scan accept the committed tables. |
| | `NoRefusedAppend` | No append by a writer that holds the advisory lock is refused by the trigger. |
| | `NoJobFailsOnRedactedRow` | No erasure job fails because another job redacted its row first (AUDIT-I7). |
| | `RedactedOnlyTranslatorDeadLetters` | Every legitimate redaction rewrote a row the translator dead-lettered. |

## Stated limits

These are tamperings a role can do without any verifier reporting a break, and which the specification accepts. The configurations list them in `Accepted`.

- **L1. A linked append (`insertLinked`).** A role that may `INSERT` appends a row that links to the newest row. `lenny_app` is the writer of every event, so a compromised application role writes false events; `lenny_erasure` writes false `gdpr.*` events. The chain records what was written, not whether it is true.
- **L2. Removal of the newest rows (`deleteTail`, `deleteLinkedOver`).** A role with `DELETE` removes the newest rows. No verifier reports it, and once a later row is appended the removed rows read as a sequence gap whose `prev_hash` link is intact, which §11.7 **Authoritative total order** classifies as the benign `gap_suspected`.
- **L3. Removal of the oldest rows (`deleteHead`).** A role with `DELETE` removes the oldest rows, which the window check cannot tell from the retention sweep. The query verifier reports the new first row `broken`, as it does after a legitimate sweep (AUDIT-I11).
- **L4. A rewrite the receipt key authorizes (`rewriteReceipted`).** The holder of the erasure grants and the KMS `Sign` grant rewrites any row's payload and signs a matching receipt. The verifiers check that a receipt is signed by a trusted key and matches the row, not that the row was dead-lettered or that the payload is the redaction form beyond its marker.
- **L5. A superuser rebuild.** A database superuser rewrites and relinks rows after the newest redacted row whose receipt is in place, or removes that row and rebuilds the rest. §11.7 **Superuser bypass mitigation** states that such a change is detectable only against the SIEM copy, which the model leaves out. The model shows the part the receipts protect: `AnchoredByReceipt` holds.
- **L6. The operator keeps each retired receipt key listed.** A receipt signed by a key that neither alias names and `audit.retiredReceiptKeyIds` does not list reads `broken` on a replica (`rotation_unlisted`). §12.8 and `docs/operator-guide/security.md` state the operator's obligation.

## Configurations

Each configuration sets every constant of the module; its first line states the scenario.

| Configuration | Scenario |
| :-- | :-- |
| `appends` | Two replicas append concurrently, each a batch of two rows, with one failed transaction. |
| `bypass` | A writer that skips the advisory lock appends beside one that takes it, with the trigger as built (no `prev_hash` check). |
| `bypass_trigger` | As `bypass`, with the `prev_hash` check of §11.7 taken under the lock and comparing with the receipt-aware link. |
| `redaction` | Two erasure jobs redact the dead-lettered newest row while a replica appends and the query and window verifiers run. |
| `redaction_main` | As `redaction`, under `Main`. |
| `redaction_naive_trigger` | One erasure job and one append, with a `prev_hash` check against the tail's recomputed hash. |
| `redaction_trigger` | As `redaction_naive_trigger`, with the receipt-aware check. |
| `retention` | A retention sweep over a chain whose second row is a `gdpr.*` row, while a replica appends and the verifiers run. |
| `retention_nogdpr` | As `retention`, with no `gdpr.*` row. |
| `app` | The application role, two tamper actions, an append, and an erasure job. |
| `erasure` | The erasure role, two tamper actions, over a chain holding a receipted redaction, and an append. |
| `erasure_three` | As `erasure`, with three tamper actions, checking `TamperDetectedExceptPlainRewrite`. |
| `erasure_guard` | As `erasure`, with a trigger that admits an erasure-mode `UPDATE` only to the redaction form of a dead-lettered row. |
| `signer` | The erasure grants and the receipt signing key, two tamper actions, with the guard of `erasure_guard`. |
| `dba` | A superuser without a KMS grant, three tamper actions, over a chain holding a receipted redaction. |
| `rotation_onerollout` | Two receipt key rotations in one rollout each across two replicas, while two erasure jobs run on either replica. |
| `rotation_verifyfirst` | As `rotation_onerollout`, with each rotation in two rollouts, verify alias first. |
| `rotation_unlisted` | As `rotation_verifyfirst`, with an operator who does not list retired keys. |

## Results

Step 0180.S1 ran every configuration on 2026-11-27 with TLC `v1.7.4` (`tla2tools.jar` SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`) and 2 workers per run. Every run finished. Each run that found a violation was repeated with the invariants it had not violated; the distinct states are those of the run that explored the whole state space. `change.md` of change 0180, "Model results", gives each counterexample, and its `decisions.md` the open decisions they raised.

| Configuration | Distinct states | Time | Violated |
| :-- | --: | --: | :-- |
| `appends` | 4,038 | 3 s | None |
| `bypass` | (stops at 67) | 3 s | `LinearChain`, `NoFork`: the fork the trigger of §11.7 would refuse (AUDIT-I8) |
| `bypass_trigger` | 53 | 3 s | None |
| `redaction` | 136,472 | 6 s | `NoQueryFalseAlarm` (AUDIT-I12), `NoJobFailsOnRedactedRow` (AUDIT-I7) |
| `redaction_main` | 135,226 | 5 s | `LinearChain` (AUDIT-I6), `NoWindowFalseAlarm` (fixed by 0177.S3), `NoQueryFalseAlarm`, `NoJobFailsOnRedactedRow` |
| `redaction_naive_trigger` | 81 | 1 s | `NoRefusedAppend` (AUDIT-I8) |
| `redaction_trigger` | 90 | 1 s | None |
| `retention` | 961 | 1 s | `NoWindowFalseAlarm` (AUDIT-I10), `NoQueryFalseAlarm` (AUDIT-I11) |
| `retention_nogdpr` | 961 | 1 s | `NoQueryFalseAlarm` (AUDIT-I11) |
| `app` | 82,812 | 5 s | `RedactedOnlyTranslatorDeadLetters` (AUDIT-I14) |
| `erasure` | 11,632 | 2 s | `TamperDetected`, `PlainRewriteDetected` (AUDIT-I9) |
| `erasure_three` | 246,715 | 7 s | None |
| `erasure_guard` | 7,194 | 2 s | None |
| `signer` | 7,194 | 1 s | None |
| `dba` | 41,878 | 2 s | None |
| `rotation_onerollout` | 20,705 | 2 s | `ReceiptKeysTrusted`, `NoScanFalseAlarm`, `NoQueryFalseAlarm` (AUDIT-I13) |
| `rotation_verifyfirst` | 33,881 | 3 s | None |
| `rotation_unlisted` | 33,881 | 2 s | `ReceiptKeysTrusted`, `NoScanFalseAlarm`, `NoQueryFalseAlarm` (limit L6) |

| Property | Result |
| :-- | :-- |
| P1 | Holds for every append path that takes the lock. Violated by a writer that skips it while the trigger checks no `prev_hash` (AUDIT-I8), and under `Main` after a redacted newest row (AUDIT-I6). |
| P2 | Holds for `app` (L1), `signer` (L4), and `dba` (`AnchoredByReceipt`). Violated for `erasure` by a rewrite of the newest row (AUDIT-I9), and holds under the trigger guard of `erasure_guard`. |
| P3, P4 | Hold. |
| P5 | Holds with two rollouts per rotation. Violated during a one-rollout rotation (AUDIT-I13) and without the retired list (L6). |
| P6 | The receipt scan holds without rotation; the window check fails after a sweep that keeps a `gdpr.*` row (AUDIT-I10); the query verifier fails during a redaction (AUDIT-I12) and after any sweep (AUDIT-I11). |

## Running it

The runner needs the `tla2tools.jar` of TLC `v1.7.4`, from `https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar`, and Java 11 or later. It checks the jar's SHA-256 against `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88` and stops when it differs. It runs TLC on the host's Java: `TLC_JAVA` names the `java` binary, else `$JAVA_HOME/bin/java`, else `java` on the `PATH`. `TLC_DOCKER=1` runs TLC in an `eclipse-temurin:21-jdk` container with no network instead, with the model directory and the jar mounted read-only and the output directory writable.

```sh
tests/formal/auditchain/run.sh <path-to-tla2tools.jar> <output-dir> <configuration> [invariant ...]
tests/formal/auditchain/run.sh <path-to-tla2tools.jar> <output-dir> all
```

With no invariant named, TLC checks every invariant the configuration lists; with invariants named, it checks only those. TLC stops at the first violation, so a run that finds one is repeated with the other invariants named. `all` runs every configuration in turn. TLC runs with `-deadlock`, because a finished behavior has no successor and the model states no liveness property. `TLC_WORKERS` sets the worker count (default 8), `TLC_HEAP` the heap (default 12g), and with `TLC_DOCKER=1`, `TLC_DETACH=1` starts the container detached. The model belongs to no gate and is run ad hoc; on the owner's workstation a run takes the shared lock and stops at one hour, as `.claude/rules/working-practices.md` states. The output directory receives the effective configuration, TLC's log, and a receipt with the TLC release, the jar's SHA-256, and the checks.

A new scenario is a new `.cfg` file that sets every constant of the module; the existing files show the defaults.
