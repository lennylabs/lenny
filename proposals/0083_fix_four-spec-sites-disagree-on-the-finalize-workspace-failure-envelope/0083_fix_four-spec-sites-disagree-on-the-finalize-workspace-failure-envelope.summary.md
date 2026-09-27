# Summary: Four spec sites disagree on the finalize workspace-failure envelope

## Summary

**Problem statement.** §6.2's **Client visibility:** bullet says `/finalize` returns a "workspace-validation" error per the §15.1 finalize precondition note, and that row names no workspace envelope. §7.2's **Pre-attached vs. post-attached failure visibility.** paragraph says the failure surfaces as `WORKSPACE_PLAN_INVALID`, and the §15.1 catalog and §14 reserve that code for create-time inner-plan schema validation. The gateway agrees with the catalog. The full evidence is in the problem statement file.

**What changes.**

- `spec/06_warm-pod-model.md` §6.2: the finalize sentence of the **Client visibility:** bullet names the workspace outcome directly and cites the §15.1 finalize note only for the setup-command and credential envelopes (SPEC-1).
- `spec/07_session-lifecycle.md` §7.2: the failure-visibility paragraph loses its `WORKSPACE_PLAN_INVALID` workspace clause (SPEC-2).
- `spec/16_observability.md` §16.5 and `spec/17_deployment-topology.md` §17.7: the `MinIOUnavailable` row and the **MinIO failure** runbook entry state that a sustained MinIO outage fails the upload call first, and stop naming `INTERNAL_ERROR` for a failed finalize (SPEC-3, SPEC-4).
- `docs/runbooks/minio-failure.md`: the **Step 2 — Full outage** tenant notice stops naming a finalize code (DOCS-1). No code, schema, or test change.

**Decisions.**

- The spec aligns to the shipped behavior. The catalog's `SESSION_CREATION_FAILED` row already covers materialization, the catalog and §14 already reserve `WORKSPACE_PLAN_INVALID`, and the gateway already answers accordingly except on the symlink re-validation path listed under defects, so correcting §6.2 and §7.2, and correcting §16.5 and §17.7, aligns the spec with the gateway with the fewest edits.
- The §15.1 finalize row and catalog rows take no edit.
- A structurally invalid staging tree at finalize keeps the retryable `503 SESSION_CREATION_FAILED` answer. A non-retryable envelope for it is left to a separate proposal (the human's answer to OD-2).

**Watch out for.**

## Goals

- §6.2, §7.2, the §15.1 finalize row, and the §15.1 catalog state one consistent outcome for a workspace-materialization failure at `POST /v1/sessions/{id}/finalize`, and the §16.5 `MinIOUnavailable` row and the §17.7 **MinIO failure** entry no longer contradict it.
- §6.2's pointer to the §15.1 finalize note cites only what the note states.

## Non-goals

- Adding a workspace envelope at finalize, either by extending `WORKSPACE_PLAN_INVALID` to finalize or by minting a new code. Rejected because it contradicts the catalog, §14, and the gateway, and needs a gateway change with no spec requirement behind it.
- Changing the retryability of a deterministic workspace-validation failure at finalize. A separate proposal takes it up.
- Implementing the §6.2 pre-attached retry on a fresh pod at finalize. Recorded under defects this proposal does not stage.
- Editing proposal 0081's or 0082's files.

## Open decisions for human to make

## Defects in the shipped tree that this proposal does not stage

- **`SLOT_FAILED` has no §15.1 catalog row.** The gateway writes `422 SLOT_FAILED` (`writeSlotFailed` in `pkg/gateway/sessionserver/start.go`), and §5.2's **Client error on exhaustion:** bullet describes the envelope's fields without naming the code.
- **The §6.2 pre-attached retry does not run at finalize.** §6.2's **Pre-attached failure retry policy:** retries a pre-attached failure on a fresh pod. handleFinalize moves the row to `failed` on the first prepare failure and answers the client, so no retry runs. Whether the finalize seam is meant to be exempt is not stated.
- **§5.2's slot retry policy disagrees with the gateway's bind of a slot reserved at create.** §5.2's **Slot retry policy** retries a transient slot failure on a new slot (`sessionPolicy.slotRetries`, default 1) and answers `error.retryable: false` once the budget is exhausted. `bindConcurrentSlot` in `pkg/gateway/sessionserver/start.go` binds a slot reserved at create, at `POST /v1/sessions/{id}/start` and at `POST /v1/sessions/start`, with no retry, and answers a transient failure with the endpoint's retryable fallback (`STARTING_FAILED` or `SESSION_CREATION_FAILED`). No spec text exempts a reserved slot from the policy. Whether §5.2 gains that exemption or the gateway retries a reserved slot is left to a separate proposal, and no proposal stages either answer.
- **A post-promotion symlink re-validation failure at finalize answers `503 SESSION_CREATION_FAILED`, where §13.4 states `UPLOAD_ARCHIVE_LIMIT_EXCEEDED`.** The gateway's `upload.ValidateSymlinkTarget` check resolves each archive entry lexically at its nominal path, and the adapter's writeSymlink in `pkg/adapter/workspace/materialize.go` creates each link through links written earlier in the same build. An `allowSymlinks` archive with entry `d -> .` followed by `d/link -> ../y` therefore passes the gateway and fails revalidatePromotedSymlinks. No proposal stages the fix.
- **The §15.1 REST endpoint table omits `POST /v1/sessions/{id}/upload-archive`.** The gateway routes it beside `POST /v1/sessions/{id}/upload` (`pkg/gateway/sessionserver/sessionserver.go`), and §7.1's session diagram and §18's build sequence name both endpoints. The §15.1 REST endpoint table and state-mutating endpoint preconditions table list only `/upload`. No staged edit names either endpoint, so none depends on the missing row. No proposal stages the fix.

## Impacts on other proposals

| Proposal | Status | What 0083 does to it | What it must do |
|:--|:--|:--|:--|
| 0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry | Implemented (2026-09-26) | 0081 recorded the disagreement over the `/finalize` workspace envelope across §6.2, §7.2, and §15.1 as a shipped-tree defect it does not stage, and named 0083 as the proposal that takes it up. SPEC-1 and SPEC-2 correct that defect. 0081 landed edits in other paragraphs of §6.2 and §7.2 and in the §15.1 `SETUP_COMMAND_FAILED` catalog row, and none of the anchors SPEC-1 to SPEC-4 edit is text that 0081 landed. 0083 therefore retires or contradicts none of 0081's deliverables. | Nothing. 0081 is a landed record and is not edited. |
| 0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for | Draft | 0082's SPEC-1 appends to the end of the §15.1 finalize row's Notes cell. 0083 does not edit that cell. | Nothing. |

## Deliverable index

- **SPEC-1** (`spec/06_warm-pod-model.md`): §6.2 **Client visibility:** bullet, the finalize sentence replaced.
- **SPEC-2** (`spec/07_session-lifecycle.md`): §7.2 **Pre-attached vs. post-attached failure visibility.** paragraph, the `WORKSPACE_PLAN_INVALID` workspace clause deleted.
- **SPEC-3** (`spec/16_observability.md`): §16.5 alert table, the finalize parenthetical of the `MinIOUnavailable` row replaced with the upload-first outcome.
- **SPEC-4** (`spec/17_deployment-topology.md`): §17.7 **MinIO failure** entry, *Remediation:* step (3) states the upload-first and finalize outcomes.
- **DOCS-1** (`docs/runbooks/minio-failure.md`): Remediation **Step 2 — Full outage** item 1, the finalize clause deleted.
