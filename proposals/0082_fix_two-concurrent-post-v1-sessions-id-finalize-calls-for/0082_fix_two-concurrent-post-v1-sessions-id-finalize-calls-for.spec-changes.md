# Spec changes: Concurrent finalize calls both prepare the session's pod

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The existing specification already governs both halves of the defect, so the code changes enforce text that is in force today:

- The §15.1 "State-mutating endpoint preconditions" preamble requires `409 INVALID_STATE_TRANSITION` with `details.currentState` and `details.allowedStates` for a call in an invalid state, and the finalize row admits only `created`. A second overlapping `/finalize` call that finds `finalizing` or `ready` is such a call. The entry guard (CODE-2) enforces the preamble.
- The §15.1 terminate row and DELETE row admit `finalizing` and end the session in `completed` and `cancelled`. §6.2 moves a session whose `maxFinalizingTimeoutSeconds` watchdog fires to `failed` with reason `FINALIZE_TIMEOUT`. §7.2 defines no edge out of a terminal state. §7.1 step 23 releases the credential lease on a terminal transition. The exit guards and the lease revoke (CODE-3) enforce these.

One outcome is stated nowhere: the response to a finalize call that was valid when admitted in `created` and was then overtaken by a terminal writer. The preamble's 409 rule covers a call made in an invalid state, and this call was admitted in a valid one. SPEC-1 states that outcome in the finalize row's Notes cell, which is the endpoint's single contract home. SPEC-1 is neutral on whether terminate aborts the in-progress setup or lets it run to completion, because that behavior belongs to the terminate row.

SPEC-1 does not restate atomic admission, the overlapping-call refusal, the finalizing-only closing write, or the lease release, because the preamble, the terminate and DELETE rows, §6.2, and §7.1 step 23 already state them. It is scoped to `/finalize` and does not generalize the preamble, because a general atomic-admission rule would be false for `/start` and would place handleTransition and handleDelete out of conformance.

## Edge cases and accepted failure modes

- **Another request or gateway process ends the session while finalization is in progress.** SPEC-1 governs the finalize call's response and the retained terminal state.
- **Two overlapping finalize calls without a shared `Idempotency-Key`.** The existing §15.1 preamble governs the refused call. Calls that share an `Idempotency-Key` are collapsed by §11.5 and receive the replayed response.

## Staged edits

### SPEC-1 · spec/15_external-api-surface.md § 15.1 REST API

Anchor: the "State-mutating endpoint preconditions" table, row `` POST /v1/sessions/{id}/finalize ``, Notes cell. Append the sentence below to the end of the cell, after the existing final sentence "A deterministic non-zero setup command surfaces here as the non-retryable `SETUP_COMMAND_FAILED` (422)." and before the cell's closing `|`, separated by one space. Leave the other cells of the row, the preamble, and every other row unedited.

```markdown
When any request or gateway process other than this finalize call moves the session to a terminal state while finalization is in progress, the session keeps that terminal state and the finalize call returns `409 INVALID_STATE_TRANSITION` with `details.currentState` set to the terminal state, in place of any setup-failure error the call's own prepare phase returns.
```

Do not edit §7.1, §6.2, §7.2, or §29. After applying, run the citation resolver and the naming lint (`scripts/specshift/name`) over the edited file.

## Spec files touched

- `spec/15_external-api-surface.md`
