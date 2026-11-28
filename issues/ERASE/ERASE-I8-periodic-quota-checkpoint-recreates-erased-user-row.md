---
id: ERASE-I8
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0176.S3
---

# The periodic quota checkpoint can recreate a quota row keyed by an erased user's raw id

## Specification

§12.8 user erasure deletes the user's data from every store in dependency order, the quota store included, and writes a receipt once the deletion is verified.

## Evidence

- Found by the security review of step 0176.S2 on 2026-11-27.
- `pkg/quota/quotacheckpoint/adapters.go`, `SessionSubjectLister.ListActiveSubjects`, lists sessions with `sessionstore.ListFilter{}` (a replica read) and returns each non-terminal session's tenant and `UserID`.
- `pkg/quota/quotacheckpoint/service.go`, `Checkpoint`, reads each subject's window total and writes every row in one `Store.Write` at the end of the pass, with `SubjectID: subj.UserID`.
- `pkg/storage/erasurejob/runner_phases.go`: the verification checks billing events only, so a quota row written after the quota deletion is not detected.
- The final checkpoint inside the terminal pipeline (`CheckpointSubject` in `afterTerminalRelease`) is covered by the pipeline wait of step 0176.S1 and by its budget-bounded context; the periodic pass is not.

## Gap

A pass that read the user's counter before the erasure deleted it, and whose write commits after the Postgres delete, leaves a `ScopeUser` row with the raw user id that nothing deletes, while the receipt reports the erasure complete.

## Suggested resolution

Open decision O6 of change [0176](../../changes/0176-erasure-waits-for-terminal-pipeline/decisions.md). Options: (a) the periodic write skips a subject whose user row is `processing_restricted`, read in the write transaction on the primary, the fence pattern of §12.8 (recommended); (b) the erasure job deletes the user's quota rows again just before verification and includes them in the verification.
