# Summary: Four spec sites disagree on the finalize workspace-failure envelope

## Summary

**Problem statement.** §6.2's **Client visibility:** bullet says `/finalize` returns a "workspace-validation" error per the §15.1 finalize precondition note, and that row names no workspace envelope. §7.2's **Pre-attached vs. post-attached failure visibility.** paragraph says the failure surfaces as `WORKSPACE_PLAN_INVALID`, and the §15.1 catalog and §14 reserve that code for create-time inner-plan schema validation. The gateway agrees with the catalog: a session-mode finalize answers a workspace-materialization failure with `503 SESSION_CREATION_FAILED`, or `413 UPLOAD_ARCHIVE_LIMIT_EXCEEDED` for an archive that violates a §13.4 ceiling, and a concurrent-workspace pool materializes at `/start` and runs nothing at finalize. The full evidence is in the problem statement file.

**What changes.**

- `spec/06_warm-pod-model.md` §6.2: the finalize sentence of the **Client visibility:** bullet names the workspace outcome directly and cites the §15.1 finalize note only for the setup-command and credential envelopes (SPEC-1).
- `spec/07_session-lifecycle.md` §7.2: the workspace clause of the failure-visibility paragraph names the same outcome (SPEC-2).
- No code, docs, schema, or test change.

**Decisions.**

- The spec aligns to the shipped behavior. The catalog's `SESSION_CREATION_FAILED` row already covers materialization, the catalog and §14 already reserve `WORKSPACE_PLAN_INVALID`, and the gateway already answers accordingly, so correcting §6.2 and §7.2 makes all four sites and the gateway agree with the fewest edits.
- The staged text defers the concurrent-workspace `/start` envelope to §5.2's slot retry policy and names no code, because `SLOT_FAILED` has no §15.1 catalog row.
- The §15.1 finalize row and catalog rows take no edit (see OD-1).

**Watch out for.**

- Proposal 0082 appends to the end of the §15.1 finalize row's Notes cell. An edit to that cell from this proposal, if OD-1 asks for one, must anchor elsewhere in the cell or land after 0082 with a rebased anchor.
- Proposal 0081 edits other paragraphs of `spec/06_warm-pod-model.md` §6.2, `spec/07_session-lifecycle.md` §7.2, and `spec/15_external-api-surface.md`. None of its staged anchors is the **Client visibility:** bullet or the failure-visibility paragraph, so a second-to-land rebase is textual.

## Goals

- §6.2, §7.2, the §15.1 finalize row, and the §15.1 catalog state one consistent outcome for a workspace-materialization failure at `POST /v1/sessions/{id}/finalize`, and that outcome is what the gateway returns.
- §6.2's pointer to the §15.1 finalize note cites only what the note states.

## Non-goals

- Adding a workspace envelope at finalize, either by extending `WORKSPACE_PLAN_INVALID` to finalize or by minting a new code. Rejected because it contradicts the catalog, §14, and the gateway, and needs a gateway change with no spec requirement behind it.
- Changing the retryability of a deterministic workspace-validation failure at finalize (see OD-2).
- Adding a `SLOT_FAILED` catalog row. Recorded under defects this proposal does not stage.
- Implementing the §6.2 pre-attached retry on a fresh pod at finalize. Recorded under defects this proposal does not stage.
- Editing proposal 0081's or 0082's files.

## Open decisions for human to make

- **OD-1. Should the §15.1 finalize row also name the workspace outcome?** After SPEC-1, §6.2 names the workspace envelopes itself and cites the row only for setup and credential failures, so the row is accurate without an edit. Adding a sentence ("A workspace-materialization failure surfaces here as the retryable `SESSION_CREATION_FAILED` fallback, or as `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` (413) for an archive that violates a §13.4 ceiling.") makes the row the single contract home for every finalize failure envelope, and would let §6.2 and §7.2 cite it instead of restating. The cost is a second edit to a cell proposal 0082 also edits. Recommendation: leave the row unedited in this proposal. Confidence: medium.
- **OD-2. Should a deterministic workspace-validation failure at finalize be non-retryable?** The adapter's `FinalizeWorkspace` answers a structurally invalid staging tree with `InvalidArgument`, and the session-mode finalize path answers `503 SESSION_CREATION_FAILED` with `Retry-After`, so a client retries a request that fails identically. §6.2's **Non-retryable failures:** bullet says upload validation errors are returned without retry, and the concurrent-workspace path already classifies `InvalidArgument` at the workspace stage as the non-retryable `workspace_validation` category. Making the finalize path match needs a new spec envelope and a gateway change, which this proposal does not stage. Recommendation: leave it to a separate proposal and keep this one to the consistency fix. Confidence: low; the §6.2 **Non-retryable failures:** bullet could be read as already requiring it.
- **OD-3. Should the proposal add a test?** No test found pins the `503 SESSION_CREATION_FAILED` answer to a non-archive materialization failure at finalize, and no tier-11 gate reads these spec sites. A tier-1 test in `pkg/gateway/sessionserver` injecting a `FinalizeWorkspace` `InvalidArgument` and asserting the 503 envelope would pin the behavior SPEC-1 and SPEC-2 now state. Recommendation: add it if the reviewer holds that a spec correction naming a behavior needs a pinning test under `test-coverage.md`. Confidence: medium.

## Defects in the shipped tree that this proposal does not stage

- **`SLOT_FAILED` has no §15.1 catalog row.** The gateway writes `422 SLOT_FAILED` (`writeSlotFailed` in `pkg/gateway/sessionserver/start.go`), and §5.2's **Client error on exhaustion:** bullet describes the envelope's fields without naming the code.
- **The §6.2 pre-attached retry does not run at finalize.** §6.2's **Pre-attached failure retry policy:** retries a pre-attached failure on a fresh pod. handleFinalize moves the row to `failed` on the first prepare failure and answers the client, so no retry runs. Whether the finalize seam is meant to be exempt is not stated.

## Impacts on other proposals

| Proposal | Status | What 0083 does to it | What it must do |
|:--|:--|:--|:--|
| 0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry | Draft | 0081 records this condition as a shipped-tree defect it does not stage (at drafting time, its open decision 30, whose recommendation is to leave it and record it as such). 0083 is the separate proposal that stages the correction. 0081 edits `spec/06_warm-pod-model.md` §6.2, `spec/07_session-lifecycle.md` §7.2, and `spec/15_external-api-surface.md` in other places, so the two proposals touch the same files; none of 0081's staged anchors is SPEC-1's or SPEC-2's. | Nothing is required. 0081 may point its open decision 30 or defect row at 0083. The second to land rebases textually. |
| 0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for | Draft | 0083 does not edit the §15.1 finalize row 0082's SPEC-1 appends to, unless OD-1 is answered yes. | Nothing, unless OD-1 is answered yes, in which case the two appends to the same Notes cell are ordered at landing. |

## Deliverable index

- **SPEC-1** (`spec/06_warm-pod-model.md`): §6.2 **Client visibility:** bullet names the finalize workspace-failure envelopes and the concurrent-workspace `/start` seam.
- **SPEC-2** (`spec/07_session-lifecycle.md`): §7.2 failure-visibility paragraph replaces the `WORKSPACE_PLAN_INVALID` clause with the same outcome.
