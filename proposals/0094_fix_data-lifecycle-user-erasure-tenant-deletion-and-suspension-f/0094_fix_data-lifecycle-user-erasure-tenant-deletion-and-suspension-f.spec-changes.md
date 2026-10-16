# Spec changes: Data lifecycle operations do not terminate and erase every session and store they cover

## Design (as the spec must state it)

The specification gains one mechanism, the lifecycle fence and drain, and states it once in a new §12.8 paragraph (SPEC-1). Every other staged edit cites that paragraph by its bold label, **Lifecycle fence and drain**, and adds only what its own operation decides.

- User erasure uses the fence through `processing_restricted` and runs the drain before any store deletion. The Article 18 paragraph, the invocation paragraph, the phase-field paragraph, and the §24.12 retry row state the erasure-specific parts: the one-transaction start and retry, the single in-progress job per user, the drain step and its failure label, and the completion order (SPEC-2).
- Tenant deletion uses the fence from Phase 1 and runs the drain as Phase 2. A Phase 2 drain that does not succeed keeps the job in Phase 2 (SPEC-3).
- Tenant suspension uses the fence through the suspension marker and runs the drain on every suspend request. The §15.1 suspend row states the repeat behavior and the response body (SPEC-4).
- The fence's refusal codes `TENANT_NOT_ACTIVE` and `USER_INVALIDATED` are already emitted on the wire. SPEC-4 catalogs them and adds them to the §15.2.1 rule 5 parity list.

The specification names no new terminal state or reason for a drained session. The drain reuses the §24.11 forced terminal transition as it stands.

## Edge cases and accepted failure modes

Each case below is owned by the staged text named in parentheses.

1. **Marker read error during an insert** (SPEC-1, *Creation fence*).
2. **A session with no owning user, or an owner with no registry row** (SPEC-1, *Creation fence*).
3. **Delegated children of a soft-disabled user** (SPEC-1, *Creation fence*, following §11.4).
4. **Tenant resume during an in-flight suspend drain** (SPEC-1 *Drain*, SPEC-4 `failedSessions`).
5. **Remote-bound session** (SPEC-1 *Drain*; REC-1 item 6 files the P2 residual and its bound).
6. **Crash between the erasure receipt and job completion** (SPEC-2, Edit 2e).
7. **Drain listing failure during suspension** (SPEC-4, Edit 4a).
8. **Phase 2 drain that never succeeds** (SPEC-3, Edit 3b).
9. **Multi-shard placement** (SPEC-1, *Creation fence*, Edits 1b and 1c).

## Staged edits

### SPEC-1 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces and § 12.6 Interface Design; spec/16_observability.md § 16.5 Alerting Rules and SLOs

**Edit 1a.** Insert a new paragraph immediately before the paragraph that opens `**Processing restriction during erasure (GDPR Article 18).**`, separated from its neighbors by one blank line. The paragraph is one physical line.

```markdown
**Lifecycle fence and drain.** User erasure acts on one user of a tenant, and tenant suspension and tenant deletion act on a whole tenant. Each of these operations sets a marker on the tenant row or the user row, fences its scope against new sessions, and drains the scope's live sessions before it acts. The markers are the tenant's suspension marker (`POST /v1/admin/tenants/{id}/suspend`, [Section 15.1](15_external-api-surface.md#151-rest-api)), a `TenantState` other than `active` (**Tenant deletion lifecycle** below), the user's `processing_restricted` flag (**Processing restriction during erasure (GDPR Article 18)** below), and the user's invalidation level ([§11.4](11_policy-and-controls.md#114-user-invalidation)). *Creation fence.* The rule covers every path that inserts a session row, such as `POST /v1/sessions`, MCP `create_session`, `POST /v1/sessions/start`, the implicit sessions of the built-in adapters and of `type: mcp` runtime endpoints, derive, replay, and delegated child creation. The transaction that inserts the row reads the tenant row and the row of the user who owns the new session under a shared row lock, and it refuses the insert when a marker is set. Every write of a marker holds an exclusive lock on the row it changes, so the insert and the marker write serialize: an insert that commits first is visible to the drain's listing, and an insert that runs after the marker commits observes the marker and is refused. A tenant marker and `processing_restricted` refuse every insert, delegated children included. A soft-disabled user's marker rejects new sessions. A hard-disabled user's marker also rejects delegated child creation ([§11.4](11_policy-and-controls.md#114-user-invalidation)). A session with no owning user, and a user with no registry row, carry no user marker. When the marker read fails, the insert fails. A refused insert returns `403` with the first matching code in this order: `USER_INVALIDATED`, `ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`, and `TENANT_NOT_ACTIVE` ([Section 15.1](15_external-api-surface.md#151-rest-api) **Error code catalog**). On `POST /v1/sessions` and MCP `create_session` these codes are session-creation rejections under [§15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract) rule 5. An endpoint may run the same check earlier, before it claims a pod or copies a snapshot, and the check inside the insert transaction is the authoritative one. The fence depends on session rows, tenant rows, and user rows sharing one Postgres instance, as they do in v1, where `SingleShardRouter` returns one pool for every call ([Section 12.6](#126-interface-design)). A `StoreRouter` that places them on different shards must replace this mechanism. *Drain.* The drain lists the scope's sessions from the Postgres primary, because a replica can omit a row the fence admitted before the marker committed ([Section 12.3](#123-postgres-ha-requirements) **Read replicas**). It applies the forced terminal transition of [§24.11](24_lenny-ctl-command-reference.md#2411-session-investigation) to each non-terminal session, and it continues past a session whose transition fails. A second listing from the primary then verifies the scope. The drain succeeds only when no transition failed and the verifying listing finds no non-terminal session. The transition writes the terminal state on the session row. The replica that writes a session's terminal state runs the session's terminal steps, and a replica that holds the session's pod binding releases the pod afterwards, so a step that follows a successful drain can start before another replica finishes either one. A drain that does not succeed leaves the marker set, and the operation that ran it runs it again in the way that operation states.
```

**Edit 1b.** In the paragraph that opens `**Scaling extension interfaces.**` (§12.6), replace `adding a Tier 4 implementation requires no application code restructuring — the swap is a configuration-only change at the store-router level.` with the text below. The paragraph stays one physical line.

```markdown
adding a Tier 4 implementation requires no application code restructuring, and the swap is a configuration-only change at the store-router level, except where [§12.8](#128-compliance-interfaces) **Lifecycle fence and drain** requires a multi-shard `StoreRouter` to replace the creation fence.
```

**Edit 1c.** In `spec/16_observability.md` §16.5, in the paragraph that opens `**Tier 4 (Platform) definition.**`, replace `The interface boundaries in [§12.6](12_storage-architecture.md#126-interface-design) are designed so that each swap is a configuration-level change — no application code restructuring is required, though accompanying infrastructure provisioning (Postgres schema migrations, NATS/Kafka deployment, additional cluster federation) is needed.` with the text below. The paragraph stays one physical line.

```markdown
The [§12.6](12_storage-architecture.md#126-interface-design) **Scaling extension interfaces** paragraph states which swaps change application code, and each swap also needs accompanying infrastructure provisioning (Postgres schema migrations, NATS/Kafka deployment, additional cluster federation).
```

### SPEC-2 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces: Article 18, invocation, and phase field; spec/24_lenny-ctl-command-reference.md § 24.12 Erasure Job Management

Every edit in SPEC-2 keeps its paragraph one physical line.

**Edit 2a.** In the paragraph that opens `**Processing restriction during erasure (GDPR Article 18).**`, replace the sentence `` When an erasure job is initiated, the platform sets `processing_restricted: true` on the user record in the `UserStore`. `` with:

```markdown
When an erasure job is initiated, the platform sets `processing_restricted: true` on the user record in the `UserStore`. The job record and the flag are written in one transaction on the Postgres primary. When that transaction fails, the erase request fails and neither the job nor the flag is written.
```

**Edit 2b.** In the same paragraph, replace `` (1) all session creation endpoints (`POST /v1/sessions` and `POST /v1/sessions/start`) are rejected with `403 ERASURE_IN_PROGRESS` for the affected `user_id`; `` with:

```markdown
(1) every session creation path rejects a new session owned by the affected `user_id` with `403 ERASURE_IN_PROGRESS`, through the creation fence (**Lifecycle fence and drain** above);
```

**Edit 2c.** In the same paragraph, replace `The restriction is enforced at the session creation gate upstream of endpoint-specific logic — both endpoints share the same gate.` with:

```markdown
The restriction is enforced at the session creation gate upstream of endpoint-specific logic, and the creation fence enforces it again in the transaction that inserts the session row.
```

**Edit 2d.** In the same paragraph, replace `In-flight sessions that are already running at erasure initiation time are allowed to complete naturally (they will be erased by the job).` with:

```markdown
Before any store deletion, the job drains the user's sessions (**Lifecycle fence and drain**). A drain failure fails the job with `failure_phase: store_delete` and leaves the flag set.
```

**Edit 2e.** In the same paragraph, replace `` The `processing_restricted` flag is cleared atomically when the erasure job writes its completion receipt — after all stores have confirmed deletion or pseudonymization. `` with:

```markdown
After all stores have confirmed deletion or pseudonymization, the erasure job writes its completion receipt, and one transaction then records the job `completed` and clears the `processing_restricted` flag. A crash between the receipt and that transaction makes the resumed job write a second receipt with the same `jobId` and the same content.
```

**Edit 2f.** In the same paragraph, replace `` The trigger exempts the `lenny_erasure` database role (which clears the flag as part of the erasure completion receipt transaction) and the explicit `clear-processing-restriction` admin endpoint (which sets a session-local variable analogous to `lenny.erasure_mode`). `` with:

```markdown
The trigger exempts the `lenny_erasure` database role and the explicit `clear-processing-restriction` admin endpoint (which sets a session-local variable analogous to `lenny.erasure_mode`). The erasure job's completion transaction passes the trigger without an exemption, because it records the job `completed` before it clears the flag.
```

**Edit 2g.** In the paragraph that opens `**User-level erasure invocation.**`, append after the sentence `These endpoints are listed in [Section 15.1](15_external-api-surface.md#151-rest-api).`:

```markdown
At most one erasure job per user is in progress at a time. A request for a user who already has a job in progress returns that job's ID and starts no second job. A retry first runs the legal-hold preflight (step 0 of **`DeleteByUser` dependency-ordered deletion sequence**). When a hold blocks, the retry is rejected with `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` and emits `gdpr.erasure_blocked_by_hold`, and the job and `processing_restricted` are left unchanged. Otherwise the retry moves the job to `initiated` and sets `processing_restricted` again in one transaction. The retry is rejected with `INVALID_STATE_TRANSITION` while another job for the same user is in progress.
```

**Edit 2h.** In the paragraph that opens `**Erasure job phase field (crash recovery).**`, replace `` it reads the persisted `phase` to determine the safe resume point: jobs in `store_deleting` re-run `` with:

```markdown
it reads the persisted `phase` to determine the safe resume point: jobs in `initiated` re-run the session drain (**Lifecycle fence and drain**) before advancing to `store_deleting`; jobs in `store_deleting` re-run
```

Then append after the paragraph's last sentence, `This prevents double-deletion errors and ensures crash recovery cannot silently skip a deletion step.`:

```markdown
One executor advances a given job at a time.
```

**Edit 2i.** In `spec/24_lenny-ctl-command-reference.md` §24.12, in the row `` | `lenny-ctl admin erasure-jobs retry <job-id>` ``, delete `Clears transient errors and resumes from the last persisted phase. `. The row stays one physical line.

### SPEC-3 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces: tenant deletion phases 1 and 2

Both rows stay one physical line each. Keep the first two cells of each row byte-identical.

**Edit 3a.** In the table under `**Tenant deletion lifecycle.**`, replace the Actions cell of the row `` | 1. Soft-disable       | `disabling`   | `` with:

```markdown
Reject new session creation through the creation fence (**Lifecycle fence and drain**), API key issuance, and user sign-ups for the tenant. Existing sessions continue until Phase 2.
```

**Edit 3b.** In the same table, replace the Actions cell of the row `` | 2. Terminate sessions | `disabling`   | `` with:

```markdown
Drain the tenant's sessions (**Lifecycle fence and drain**). The phase completes only when the drain succeeds. When the drain does not succeed, the job stays in this phase and the controller runs the drain again on its next pass.
```

### SPEC-4 · spec/15_external-api-surface.md § 15.1 REST API and § 15.2.1 REST/MCP Consistency Contract; spec/16_observability.md § 16.7 Section 25 Audit Events

Every table row in SPEC-4 is one physical line.

**Edit 4a.** In the §15.1 admin endpoint table, replace the Description cell of the row `` | `POST`   | `/v1/admin/tenants/{id}/suspend` `` with:

```markdown
Suspend a tenant. Body: `{"reason": "<string>"}`. All active sessions in the tenant are drained ([§12.8](12_storage-architecture.md#128-compliance-interfaces) **Lifecycle fence and drain**); new session creation and message injection are rejected with `TENANT_SUSPENDED` until the tenant is resumed. Every suspend request is recorded in the audit trail with operator identity and reason. A suspend request for an already-suspended tenant leaves the marker unchanged and runs the drain again. The response body reports `drainedSessions`, the number of sessions the drain terminated, and `failedSessions`, the IDs of sessions the drain did not bring to a terminal state. A non-empty `failedSessions` means the drain is incomplete. When the drain cannot list the tenant's sessions, the request returns `INTERNAL_ERROR` and the suspension marker stays set. Requires `platform-admin`.
```

**Edit 4b.** In the §15.1 **Error code catalog:** table, insert this row immediately after the row for `TENANT_SUSPENDED`:

```markdown
| `TENANT_NOT_ACTIVE`           | `POLICY`    | 403         | Session creation rejected because the tenant's `TenantState` is not `active` ([§12.8](12_storage-architecture.md#128-compliance-interfaces) **Lifecycle fence and drain**). `details.tenantId` and `details.state` are included. Not retryable. |
```

**Edit 4c.** In the same table, insert this row immediately after the row for `ERASURE_IN_PROGRESS`:

```markdown
| `USER_INVALIDATED`          | `POLICY`    | 403         | The request is rejected because the authenticated or owning user is invalidated ([§11.4](11_policy-and-controls.md#114-user-invalidation)). The code is returned for session creation on every creation path, delegated-task creation, and interaction resolution. Not retryable. |
```

**Edit 4d.** In §15.2.1, in rule 5's `All error classes:` bullet, replace `and the session-creation rejection family catalogued in §15.4 (` with `and the session-creation rejection family catalogued in the [Section 15.1](#151-rest-api) **Error code catalog** (`; replace `` `ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`) `` with:

```markdown
`ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`, `TENANT_NOT_ACTIVE`, `USER_INVALIDATED`)
```

and replace `keeping the matrix in lockstep with the §15.4 error code catalog (any future session-creation rejection added to §15.4 MUST be added to this list in the same change)` with:

```markdown
keeping the matrix in lockstep with the §15.1 Error code catalog (any future session-creation rejection added to that catalog MUST be added to this list in the same change)
```

**Edit 4e.** In the §15.1 **Error code catalog:** table, in the row for `ERASURE_BLOCKED_BY_LEGAL_HOLD`, replace `` `POST /v1/admin/users/{userId}/erase` rejected by `` with:

```markdown
`POST /v1/admin/users/{userId}/erase` or `POST /v1/admin/erasure-jobs/{jobId}/retry` rejected by
```

**Edit 4f.** In `spec/16_observability.md` §16.7, in the list item for `gdpr.erasure_blocked_by_hold`, `gdpr.legal_hold_overridden`, replace `` written when `POST /v1/admin/users/{user_id}/erase` is blocked `` with the text below. The list item stays one physical line.

```markdown
written when `POST /v1/admin/users/{user_id}/erase` or `POST /v1/admin/erasure-jobs/{job_id}/retry` is blocked
```

## Spec files touched

- `spec/12_storage-architecture.md` (SPEC-1, SPEC-2, SPEC-3)
- `spec/15_external-api-surface.md` (SPEC-4)
- `spec/16_observability.md` (SPEC-1, SPEC-4)
- `spec/24_lenny-ctl-command-reference.md` (SPEC-2)
