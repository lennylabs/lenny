# Spec changes: Four spec sites disagree on the finalize workspace-failure envelope

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The §15.1 error catalog and the gateway already agree. A workspace-materialization failure at `POST /v1/sessions/{id}/finalize` is a generic failure of the atomic creation unit and surfaces as the retryable `SESSION_CREATION_FAILED` fallback, which the catalog row defines as covering "claim, materialize, or setup outside a more specific code". The one more specific code is `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` (413), which the catalog applies to client uploads that violate a §13.4 extraction ceiling. `WORKSPACE_PLAN_INVALID` stays reserved for the create-time inner-plan schema validation, as the catalog and §14 state.

A concurrent-workspace pool runs no materialization at finalize. Its reserved slot materializes at `POST /v1/sessions/{id}/start`, where the §5.2 **Slot retry policy (`maxConcurrentSessions > 1`).** governs the client error.

The edits correct the two sites that disagree with this, §6.2's **Client visibility:** bullet (SPEC-1) and §7.2's **Pre-attached vs. post-attached failure visibility.** paragraph (SPEC-2). Each names the envelopes directly and links the catalog, the same way each already names `SETUP_COMMAND_FAILED`. The §15.1 finalize row and the §15.1 catalog rows take no edit.

## Edge cases and accepted failure modes

- **A deterministic workspace-validation failure at finalize is answered as retryable.** The adapter's `FinalizeWorkspace` answers a structurally invalid staging tree with `InvalidArgument`, and the session-mode finalize path still answers `503 SESSION_CREATION_FAILED` with `Retry-After`. The staged text states this shipped behavior. Whether it should instead be a permanent envelope is open decision OD-2 in the summary.
- **A concurrent-workspace slot's workspace failure at `/start`.** The staged text points to §5.2 for the envelope and does not name `SLOT_FAILED`, a code that no §15.1 catalog row defines. The summary records the missing row as a defect this proposal does not stage.

## Staged edits

### SPEC-1 · spec/06_warm-pod-model.md § 6.2 Pod State Machine (**Pre-attached failure retry policy:**, **Client visibility:** bullet)

Anchor: the bullet that begins "- **Client visibility:** Pre-attached retries are **internal to the gateway's warm-pool retry loop**". In that bullet, replace the sentence

```markdown
`POST /v1/sessions/{id}/finalize` surfaces a workspace-materialization, setup-command, or credential-assignment failure, returning the workspace-validation, setup-command, or credential error per the §15.1 finalize precondition note.
```

with

```markdown
`POST /v1/sessions/{id}/finalize` surfaces a setup-command or credential-assignment failure, returning the setup-command or credential error per the §15.1 finalize precondition note. It surfaces a workspace-materialization failure as the retryable `SESSION_CREATION_FAILED` fallback, except that an upload archive violating a [§13.4](13_security-model.md#134-upload-security) extraction ceiling surfaces as the non-retryable `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` (413) ([§15.1](15_external-api-surface.md#151-rest-api)). A concurrent-workspace pool runs no materialization at finalize: its reserved slot materializes at `POST /v1/sessions/{id}/start`, where the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot retry policy determines the client error.
```

Leave every other sentence of the bullet unedited.

### SPEC-2 · spec/07_session-lifecycle.md § 7.2 Interactive Session Model (**Pre-attached vs. post-attached failure visibility.**)

Anchor: the paragraph that begins "**Pre-attached vs. post-attached failure visibility.**". In its parenthetical list of endpoints, replace the clause

```markdown
a workspace-materialization failure surfaces as `WORKSPACE_PLAN_INVALID` at `POST /v1/sessions/{id}/finalize`;
```

with

```markdown
a workspace-materialization failure surfaces at `POST /v1/sessions/{id}/finalize` as the retryable `SESSION_CREATION_FAILED` fallback, or as the non-retryable `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` (HTTP 413) when an upload archive violates a [§13.4](13_security-model.md#134-upload-security) extraction ceiling, and for a concurrent-workspace pool, whose reserved slot materializes at start, it surfaces at `POST /v1/sessions/{id}/start` under the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot retry policy;
```

Leave the rest of the paragraph unedited.

After applying both edits, run the citation resolver and the naming lint (`scripts/specshift/name`) over the two edited files.

## Spec sections deliberately untouched

- **§15.1 "State-mutating endpoint preconditions", the finalize row.** After SPEC-1, §6.2 cites the row only for the setup-command and credential envelopes, which the row names. Proposal 0082 appends a sentence to the same Notes cell. Whether the row should also name the workspace outcome is open decision OD-1.
- **§15.1 error catalog, the `WORKSPACE_PLAN_INVALID` and `SESSION_CREATION_FAILED` rows.** Both already agree with the gateway.
- **§14's create-time validation list.** It already reserves `WORKSPACE_PLAN_INVALID` for inner-plan schema failures.

## Spec files touched

- `spec/06_warm-pod-model.md`: §6.2, one sentence of the **Client visibility:** bullet replaced by three.
- `spec/07_session-lifecycle.md`: §7.2, one clause of the **Pre-attached vs. post-attached failure visibility.** paragraph replaced.
