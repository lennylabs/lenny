# Summary: Four spec sites disagree on the finalize workspace-failure envelope

## Summary

**Problem statement.** §6.2's **Client visibility:** bullet says `/finalize` returns a "workspace-validation" error per the §15.1 finalize precondition note, and that row names no workspace envelope. §7.2's **Pre-attached vs. post-attached failure visibility.** paragraph says the failure surfaces as `WORKSPACE_PLAN_INVALID`, and the §15.1 catalog and §14 reserve that code for create-time inner-plan schema validation. The gateway agrees with the catalog. The full evidence is in the problem statement file.

**What changes.**

- `spec/06_warm-pod-model.md` §6.2: the finalize sentence of the **Client visibility:** bullet names the workspace outcome directly and cites the §15.1 finalize note only for the setup-command and credential envelopes (SPEC-1).
- `spec/07_session-lifecycle.md` §7.2: the failure-visibility paragraph loses its `WORKSPACE_PLAN_INVALID` workspace clause (SPEC-2).
- `spec/16_observability.md` §16.5 and `spec/17_deployment-topology.md` §17.7: the `MinIOUnavailable` row and the **MinIO failure** runbook entry stop naming `INTERNAL_ERROR` for a failed finalize (SPEC-3, SPEC-4).
- No code, docs, schema, or test change.

**Decisions.**

- The spec aligns to the shipped behavior. The catalog's `SESSION_CREATION_FAILED` row already covers materialization, the catalog and §14 already reserve `WORKSPACE_PLAN_INVALID`, and the gateway already answers accordingly, so correcting §6.2 and §7.2, and deleting the `INTERNAL_ERROR` at §16.5 and §17.7, aligns the spec with the gateway with the fewest edits.
- The §15.1 finalize row and catalog rows take no edit.

**Watch out for.**

- Proposal 0082 appends to the end of the §15.1 finalize row's Notes cell. This proposal does not edit that cell.

## Goals

- §6.2, §7.2, the §15.1 finalize row, the §15.1 catalog, the §16.5 `MinIOUnavailable` row, and the §17.7 **MinIO failure** entry state one consistent outcome for a workspace-materialization failure at `POST /v1/sessions/{id}/finalize`, and that outcome is what the gateway returns.
- §6.2's pointer to the §15.1 finalize note cites only what the note states.

## Non-goals

- Adding a workspace envelope at finalize, either by extending `WORKSPACE_PLAN_INVALID` to finalize or by minting a new code. Rejected because it contradicts the catalog, §14, and the gateway, and needs a gateway change with no spec requirement behind it.
- Changing the retryability of a deterministic workspace-validation failure at finalize (see OD-2).
- Implementing the §6.2 pre-attached retry on a fresh pod at finalize. Recorded under defects this proposal does not stage.
- Editing proposal 0081's or 0082's files.

## Open decisions for human to make

- **OD-2. Should a structurally invalid staging tree at finalize get a non-retryable envelope, and in which proposal?** On a session-mode pool, the adapter's `FinalizeWorkspace` rejects a staging tree it cannot materialize, such as one with a containment violation, with gRPC `InvalidArgument` (`pkg/adapter/staging.go:306`). The gateway answers `POST /v1/sessions/{id}/finalize` with the retryable `503 SESSION_CREATION_FAILED` and `Retry-After` (`pkg/gateway/sessionserver/sessionserver.go:3144`), and SPEC-1 writes that answer into §6.2. The question is whether to accept the retryable answer now and leave a non-retryable (`PERMANENT`) envelope to a separate proposal, or to make the envelope non-retryable in this proposal.
  - *Recommendation:* accept the retryable answer here and take the non-retryable envelope to a separate proposal. The staging already carries this answer, so nothing in it changes.
  - *Ground:* the §15.1 `SESSION_CREATION_FAILED` row is `TRANSIENT` and names materialize among its causes (`spec/15_external-api-surface.md:1131`), and the gateway implements that row. No existing `PERMANENT` code covers every cause of this failure. `WORKSPACE_PLAN_INVALID` is reserved for create-time inner-plan schema validation; `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` covers the §13.4 / §7.4 archive validators (`spec/15_external-api-surface.md:1124`), which include the adapter's symlink-containment check, but the adapter answers that check and non-validator causes with one `InvalidArgument`, and the gateway maps only `*upload.ValidationError` to the code; and `SLOT_FAILED` has no catalog row. A non-retryable answer therefore needs a new or re-scoped catalog code and a new branch in writePodClaimError, which the first non-goal excludes. SPEC-1 contradicts no spec text: §6.2's **Non-retryable failures:** bullet (`spec/06_warm-pod-model.md:290`) sits under the **Pre-attached failure retry policy:** (`spec/06_warm-pod-model.md:285`), which governs the gateway's internal retry loop, and it fixes no client envelope or `retryable` flag.
  - *The case for the other answer:* the platform answers analogous deterministic failures non-retryably. `SETUP_COMMAND_FAILED` is a `PERMANENT` 422 (`spec/15_external-api-surface.md:1136`), `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` is a `PERMANENT` 413, and the concurrent-workspace `/start` path classifies `InvalidArgument` as the non-retryable `workspace_validation` category (`pkg/gateway/podlifecycle/podsession/slotfailure.go:100`). The gateway maps other deterministic failures to dedicated 422s "rather than the retryable atomic-unit fallback" (`pkg/gateway/sessionserver/start.go:144`).
  - *Alternatives:* making the envelope non-retryable in this proposal lost because it turns a consistency fix into a client contract change, reopens the first non-goal, and needs a catalog row and a gateway change with tests. Mapping the adapter's `InvalidArgument` to `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` lost because that answer also carries causes outside the archive validators, and the first non-goal rejects extending `WORKSPACE_PLAN_INVALID` to finalize.
  - *Cost of deciding otherwise:* answering "in this proposal" rewrites SPEC-1's fallback clause, the first Decisions bullet, and the first two non-goals, and adds a staged catalog edit, a gateway change to writePodClaimError, a test, and a code-lane checklist step. Accepting the recommendation keeps a residual cost: the client is told to retry a deterministic failure. A retried `/finalize` on the same session answers `409 INVALID_STATE_TRANSITION`, because handleFinalize moves the row to `failed` first (`pkg/gateway/sessionserver/sessionserver.go:3143`). The identical failure recurs only when the client creates a new session and uploads the same tree.
  - *Confidence:* moderate.
- **OD-5. Should the §17.7 MinIO failure runbook step (3) and the §16.5 `MinIOUnavailable` row say where a MinIO outage fails first?** After SPEC-3 and SPEC-4, step (3) still says workspace uploads for new sessions fail at the `finalize` step, and the §16.5 row still says the outage blocks workspace uploads at session creation. In a sustained outage the gateway fails the upload call first: a failed blob write at `POST /v1/sessions/{id}/upload` answers `500 INTERNAL_ERROR` and feeds the Upload Handler circuit breaker, which then answers `503 SUBSYSTEM_UNAVAILABLE`. `/finalize` answers `503 SESSION_CREATION_FAILED` only for a session whose upload reached the blob store before the outage. The wording predates this proposal and lies outside its finalize-envelope scope. Recommendation from the review: leave it to a later proposal. The review recorded no confidence.

## Defects in the shipped tree that this proposal does not stage

- **`SLOT_FAILED` has no §15.1 catalog row.** The gateway writes `422 SLOT_FAILED` (`writeSlotFailed` in `pkg/gateway/sessionserver/start.go`), and §5.2's **Client error on exhaustion:** bullet describes the envelope's fields without naming the code.
- **The §6.2 pre-attached retry does not run at finalize.** §6.2's **Pre-attached failure retry policy:** retries a pre-attached failure on a fresh pod. handleFinalize moves the row to `failed` on the first prepare failure and answers the client, so no retry runs. Whether the finalize seam is meant to be exempt is not stated.
- **§5.2's slot retry policy disagrees with the gateway's bind of a slot reserved at create.** §5.2's **Slot retry policy** retries a transient slot failure on a new slot (`sessionPolicy.slotRetries`, default 1) and answers `error.retryable: false` once the budget is exhausted. `bindConcurrentSlot` in `pkg/gateway/sessionserver/start.go` binds a slot reserved at create, at `POST /v1/sessions/{id}/start` and at `POST /v1/sessions/start`, with no retry, and answers a transient failure with the endpoint's retryable fallback (`STARTING_FAILED` or `SESSION_CREATION_FAILED`). No spec text exempts a reserved slot from the policy. Whether §5.2 gains that exemption or the gateway retries a reserved slot is left to a separate proposal, and no proposal stages either answer.

## Impacts on other proposals

| Proposal | Status | What 0083 does to it | What it must do |
|:--|:--|:--|:--|
| 0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry | Implemented (2026-09-26) | 0081 recorded the disagreement over the `/finalize` workspace envelope across §6.2, §7.2, and §15.1 as a shipped-tree defect it does not stage, and named 0083 as the proposal that takes it up. SPEC-1 and SPEC-2 correct that defect. 0081 landed edits in other paragraphs of §6.2 and §7.2 and in the §15.1 `SETUP_COMMAND_FAILED` catalog row, and none of the anchors SPEC-1 to SPEC-4 edit is text that 0081 landed. 0083 therefore retires or contradicts none of 0081's deliverables. | Nothing. 0081 is a landed record and is not edited. |
| 0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for | Draft | 0083 does not edit the §15.1 finalize row 0082's SPEC-1 appends to. | Nothing. |

## Deliverable index

- **SPEC-1** (`spec/06_warm-pod-model.md`): §6.2 **Client visibility:** bullet, the finalize sentence replaced.
- **SPEC-2** (`spec/07_session-lifecycle.md`): §7.2 **Pre-attached vs. post-attached failure visibility.** paragraph, the `WORKSPACE_PLAN_INVALID` workspace clause deleted.
- **SPEC-3** (`spec/16_observability.md`): §16.5 alert table, the finalize parenthetical deleted from the `MinIOUnavailable` row.
- **SPEC-4** (`spec/17_deployment-topology.md`): §17.7 **MinIO failure** entry, one sentence deleted from *Remediation:* step (3).

The non-spec staging carries no deliverable.
