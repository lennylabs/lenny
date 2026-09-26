# Spec changes: Four spec sites disagree on the finalize workspace-failure envelope

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

SPEC-1 corrects the finalize sentence of §6.2's **Client visibility:** bullet to the envelope the §15.1 `SESSION_CREATION_FAILED` row and §13.4's validator-violation bullet already define. SPEC-2 deletes §7.2's `WORKSPACE_PLAN_INVALID` clause; the paragraph's lead already places the failure at the endpoint that runs the failing step. SPEC-3 and SPEC-4 replace the `INTERNAL_ERROR` that the §16.5 `MinIOUnavailable` row and the §17.7 **MinIO failure** runbook entry name for a failed finalize with where a sustained MinIO outage fails first: the upload call, which the Upload Handler circuit breaker refuses with 503 once it opens. SPEC-4 also states that a session whose uploads completed before the outage fails at the request that materializes its workspace. `WORKSPACE_PLAN_INVALID` stays reserved for create-time inner-plan schema validation, as the catalog and §14 state. The §15.1 finalize row and the §15.1 catalog rows take no edit.

## Edge cases and accepted failure modes

- **A deterministic workspace-validation failure at finalize is answered as retryable.** The adapter's `FinalizeWorkspace` answers a structurally invalid staging tree with `InvalidArgument`, and the session-mode finalize path still answers `503 SESSION_CREATION_FAILED` with `Retry-After`.

## Staged edits

### SPEC-1 · spec/06_warm-pod-model.md § 6.2 Pod State Machine (**Pre-attached failure retry policy:**, **Client visibility:** bullet)

Anchor: the bullet that begins "- **Client visibility:** Pre-attached retries are **internal to the gateway's warm-pool retry loop**". In that bullet, replace the sentence

```markdown
`POST /v1/sessions/{id}/finalize` surfaces a workspace-materialization, setup-command, or credential-assignment failure, returning the workspace-validation, setup-command, or credential error per the §15.1 finalize precondition note.
```

with

```markdown
`POST /v1/sessions/{id}/finalize` surfaces a workspace-materialization, setup-command, or credential-assignment failure. It returns the setup-command or credential error per the §15.1 finalize precondition note, and a workspace-materialization failure as the `SESSION_CREATION_FAILED` fallback ([§15.1](15_external-api-surface.md#151-rest-api)) unless it is an archive validator violation, which surfaces as `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` ([§13.4](13_security-model.md#134-upload-security)).
```

Leave every other sentence of the bullet unedited.

### SPEC-2 · spec/07_session-lifecycle.md § 7.2 Interactive Session Model (**Pre-attached vs. post-attached failure visibility.**)

Anchor: the paragraph that begins "**Pre-attached vs. post-attached failure visibility.**". In its parenthetical list of endpoints, delete the clause below together with the space that follows it, so that the list reads from "surfaces at `POST /v1/sessions`;" directly to "a deterministic non-zero setup-command exit".

```markdown
a workspace-materialization failure surfaces as `WORKSPACE_PLAN_INVALID` at `POST /v1/sessions/{id}/finalize`;
```

Leave the rest of the paragraph unedited.

### SPEC-3 · spec/16_observability.md § 16.5 Alerting Rules and SLOs (`MinIOUnavailable` row)

Anchor: the alert-table row whose first cell is `MinIOUnavailable`. In that row, replace

```markdown
 (`finalize` step fails with `INTERNAL_ERROR`)
```

with

```markdown
 (`POST /v1/sessions/{id}/upload` fails with `INTERNAL_ERROR`, and with 503 once the Upload Handler circuit breaker ([Section 4.1](04_system-components.md#41-edge-gateway-replicas)) opens)
```

Keep the row on one physical table line, and leave the rest of it unedited.

### SPEC-4 · spec/17_deployment-topology.md § 17.7 Operational Runbooks (**MinIO failure**, *Remediation:* step (3))

Anchor: the *Remediation:* item of the **MinIO failure** entry. In step (3), replace

```markdown
If MinIO is fully unavailable, workspace uploads for new sessions will fail at the `finalize` step. Session creation returns `INTERNAL_ERROR`.
```

with

```markdown
If MinIO is fully unavailable, workspace uploads for new sessions fail at `POST /v1/sessions/{id}/upload` with `INTERNAL_ERROR`, and with 503 once the Upload Handler circuit breaker ([Section 4.1](04_system-components.md#41-edge-gateway-replicas)) opens. A session whose uploads completed before the outage fails when its workspace is materialized: at `POST /v1/sessions/{id}/finalize` with the retryable `SESSION_CREATION_FAILED`, or at `POST /v1/sessions/{id}/start` on a concurrent-workspace pool.
```

Leave every other sentence unedited.

After applying the edits, run the citation resolver and the naming lint (`scripts/specshift/name`) over the edited files.

## Spec sections deliberately untouched

- **§15.1 "State-mutating endpoint preconditions", the finalize row.** After SPEC-1, §6.2 cites the row only for the setup-command and credential envelopes, which the row names. Proposal 0082 appends a sentence to the same Notes cell. The `SESSION_CREATION_FAILED` catalog row covers the workspace outcome.
- **§15.1 error catalog, the `WORKSPACE_PLAN_INVALID` and `SESSION_CREATION_FAILED` rows.** Both already agree with the gateway.
- **§14's create-time validation list.** It already reserves `WORKSPACE_PLAN_INVALID` for inner-plan schema failures.

## Spec files touched

- `spec/06_warm-pod-model.md`: §6.2, the finalize sentence of the **Client visibility:** bullet replaced.
- `spec/07_session-lifecycle.md`: §7.2, one clause of the **Pre-attached vs. post-attached failure visibility.** paragraph deleted.
- `spec/16_observability.md`: §16.5, the finalize parenthetical of the `MinIOUnavailable` row replaced.
- `spec/17_deployment-topology.md`: §17.7, the first two sentences of the **MinIO failure** remediation step (3) replaced.
