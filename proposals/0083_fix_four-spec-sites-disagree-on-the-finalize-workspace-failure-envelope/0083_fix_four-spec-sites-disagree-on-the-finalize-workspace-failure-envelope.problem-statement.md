# Problem: Four spec sites disagree on the finalize workspace-failure envelope

## Statement

Four specification sites disagree about the error a client receives when workspace materialization fails at `POST /v1/sessions/{id}/finalize`, and two of them disagree with the gateway.

1. §6.2's pre-attached **Client visibility:** bullet (spec/06_warm-pod-model.md, under **Pre-attached failure retry policy:**) says `POST /v1/sessions/{id}/finalize` "surfaces a workspace-materialization, setup-command, or credential-assignment failure, returning the workspace-validation, setup-command, or credential error per the §15.1 finalize precondition note".
2. The §15.1 finalize row of the "State-mutating endpoint preconditions" table (spec/15_external-api-surface.md) names a credential envelope (`CREDENTIAL_POOL_EXHAUSTED`) and a setup-command envelope (`SETUP_COMMAND_FAILED`). It names no workspace envelope, so §6.2's pointer cites a statement the row does not make.
3. §7.2's **Pre-attached vs. post-attached failure visibility.** paragraph (spec/07_session-lifecycle.md) says "a workspace-materialization failure surfaces as `WORKSPACE_PLAN_INVALID` at `POST /v1/sessions/{id}/finalize`".
4. The §15.1 error catalog's `WORKSPACE_PLAN_INVALID` row reserves that code for an inner `workspacePlan` on `POST /v1/sessions` or `POST /v1/sessions/start` failing JSON Schema validation, "Reserved for inner-plan schema failures only". §14 states the same reservation for the create-time validation pass.

The gateway agrees with the catalog. It writes `WORKSPACE_PLAN_INVALID` only on request-validation paths and emits no workspace envelope at finalize.

What a client receives today when workspace materialization fails:

- **Session-mode (exclusive) pool, at `/finalize`.** `prepareAtFinalize` calls `Binder.Prepare`. A staging or `FinalizeWorkspace` failure returns a wrapped error with no workspace type. `handleFinalize` moves the row to `failed` and routes the error through `writePodClaimError` with fallback `SESSION_CREATION_FAILED`. The client receives `503 SESSION_CREATION_FAILED` with `Retry-After`, whatever gRPC code the adapter returned (the adapter's `FinalizeWorkspace` answers `InvalidArgument` or `FailedPrecondition`). The one exception is an upload archive that violates a §13.4 extraction ceiling: the `*upload.ValidationError` case answers `413 UPLOAD_ARCHIVE_LIMIT_EXCEEDED` with no `Retry-After`.
- **Concurrent-workspace pool (`maxConcurrentSessions > 1`), at `/finalize`.** `prepareAtFinalize` returns `(nil, nil)` and the call is a plain state transition. No materialization runs at finalize, so no workspace failure can surface there.
- **Concurrent-workspace pool, at `/start`.** The reserved slot materializes through `BindReservedSlot`. `classifySlotBindFailure` maps an `InvalidArgument` at the workspace stage to the `workspace_validation` category, and the client receives `422 SLOT_FAILED` with `details.category = "workspace_validation"`. Any other workspace-stage failure (including `FailedPrecondition`, which `SlotBindError.Reason` keeps transient at the workspace stage) stays the retryable `503 STARTING_FAILED` fallback.

The §15.1 catalog's `SESSION_CREATION_FAILED` row already covers this outcome: "Atomic session-creation unit failed for a generic reason (claim, materialize, or setup outside a more specific code)". The defective sites are §6.2 and §7.2.

## Evidence

- verified: spec/06_warm-pod-model.md §6.2, **Pre-attached failure retry policy:**, the **Client visibility:** bullet sentence quoted in item 1.
- verified: spec/15_external-api-surface.md §15.1, "State-mutating endpoint preconditions" table, the `POST /v1/sessions/{id}/finalize` row Notes cell. It names `CREDENTIAL_POOL_EXHAUSTED`, `USER_CREDENTIAL_NOT_FOUND` (as a create-only error), and `SETUP_COMMAND_FAILED`, and no workspace code.
- verified: spec/07_session-lifecycle.md §7.2, **Pre-attached vs. post-attached failure visibility.**, the clause quoted in item 3.
- verified: spec/15_external-api-surface.md §15.1 error catalog, the `WORKSPACE_PLAN_INVALID` row ("Reserved for inner-plan schema failures only") and the `SESSION_CREATION_FAILED` row ("claim, materialize, or setup outside a more specific code").
- verified: spec/14_workspace-plan-schema.md, the create-time validation list: "`WORKSPACE_PLAN_INVALID` is **reserved for inner-plan schema failures only**".
- verified: pkg/gateway/sessionserver/start.go, the two `WORKSPACE_PLAN_INVALID` writes in the request-validation helper, and pkg/gateway/sessionserver/sessionserver.go, the JSON Schema validation writer. These are the only non-test writers of the code.
- verified: pkg/gateway/sessionserver/sessionserver.go, handleFinalize: on a `prepareAtFinalize` error it calls `failSession` and then `writePodClaimError(w, err, "SESSION_CREATION_FAILED", "workspace finalization failed")`.
- verified: pkg/gateway/sessionserver/finalize.go, prepareAtFinalize: it returns `(nil, nil)` when `match.MaxConcurrentSessions > 1`, and otherwise returns the `Binder.Prepare` error unchanged. finalize.go writes only `VALIDATION_ERROR` and `PAYLOAD_TOO_LARGE` itself.
- verified: pkg/gateway/podlifecycle/podsession/binder.go, Binder.Prepare: a `stageWorkspace` failure returns `fmt.Errorf("podsession: stage workspace on pod %s: %w", ...)` and a `FinalizeWorkspace` failure returns `fmt.Errorf("podsession: finalize workspace on pod %s: %w", ...)`, with no typed workspace error.
- verified: pkg/gateway/sessionserver/start.go, writePodClaimError: it has no workspace case. An `*upload.ValidationError` answers `413 UPLOAD_ARCHIVE_LIMIT_EXCEEDED`, and the default answers the fallback code as 503 with `Retry-After`.
- verified: pkg/adapter/staging.go, the adapter's `FinalizeWorkspace` handler answers `InvalidArgument` and `FailedPrecondition`.
- verified: pkg/gateway/podlifecycle/podsession/slotfailure.go, `SlotBindError.Reason`: `InvalidArgument` maps to `workspace_validation`; `FailedPrecondition` at the `workspace_prep` stage maps to transient.
- verified: pkg/gateway/sessionserver/start.go, `bindConcurrentSlot` routes a `BindReservedSlot` error through `classifySlotBindFailure`, and `writeSlotFailed` writes `422 SLOT_FAILED` with `details.category`. The two-step `/start` handler uses fallback `STARTING_FAILED`.
- verified: pkg/gateway/sessionserver/start_pod_test.go, `TestFinalizeRejectsOverLimitArchiveAsNonRetryable_spec_13_4` pins the 413 at finalize.
- verified: docs/reference/error-catalog.md, the `WORKSPACE_PLAN_INVALID` and `SESSION_CREATION_FAILED` rows agree with the §15.1 catalog. No page under docs/ repeats the §6.2 or §7.2 claim.

## Who observes it

A client author or SDK author who reads §6.2 or §7.2 and handles `WORKSPACE_PLAN_INVALID` at finalize never sees it. The same reader who follows §6.2's pointer to the §15.1 finalize row finds no workspace envelope there. A test author who writes a finalize test from §7.2 writes an assertion the gateway fails.

## What breaks if nothing changes

- A client that branches on `WORKSPACE_PLAN_INVALID` at finalize treats a materialization failure as an unknown error, and a client that reads §7.2 treats it as non-retryable when the gateway marks it retryable.
- A conformance or fidelity review of the finalize path against §7.2 reports a gateway defect where the gateway matches the catalog.

## Findings this unblocks

**BUILD-GAPS finding ids: none.** No BUILD-GAPS.md finding references this problem.

## Prior art considered

- Proposal 0081 (Draft) records this condition in its summary as open decision 30 and recommends leaving it to a separate proposal. 0081 stages no edit to the four sites.
- Proposal 0082 (Draft) appends a sentence to the end of the §15.1 finalize row's Notes cell. It does not edit §6.2's **Client visibility:** bullet or §7.2's failure-visibility paragraph.
- No other proposal under proposals/ edits these sites.

## Validated premises

This section is filled by the validation stage of the change-proposal workflow. This first draft was written without it.
