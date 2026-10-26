# Spec changes — Ending an impersonation session does not revoke its bearer

## Design (as the spec must state it)

An impersonation session is the `issued_tokens` row of the bearer it mints. The gateway keeps no other session state. Every gateway replica reads the row, so any replica can list or end any session, and a restart loses nothing.

The normative home of the mechanism is the new §13.3 paragraph **Platform-admin impersonation.** (SPEC-1), with numbered rules:

- Rule 1 fixes issuance order, the caller check, and the refusals of the rule 1 (c) fence.
- Rule 2 defines the impersonation row predicate and an active row.
- Rule 3 is the `DELETE` end procedure: a conditional revoke, then the cache push and peer fan-out, then the ended event.
- Rule 4 states how a session ends without `DELETE` and that no other route writes the ended event.
- Rule 5 names the verifiers and the mints that refuse the bearer.
- Rule 6 is the full-revoke cascade to the impersonating admin's bearers.
- Rule 7 is the cross-tenant read discipline.

Every other staged edit cites that paragraph by heading and rule number. §10.2 gains a citation and applies step (1a) to the session-creation and playground mints (SPEC-2). §12.2 documents `act_sub` for impersonation rows and the partial index, and §12.6 names the rule 1 (c) fence (SPEC-3). §16.7 gains the session id in the started payload, cites rules 3 and 4 for the ended event, and defines its payload (SPEC-4). §11.4 states that a full revoke commits the invalidation level before step 1, and step 5 points at rule 6 (SPEC-5). §15.1 gains the three endpoint rows with their catalogued error codes, and its `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED` row names step (1a) (SPEC-6). §11.7 item 3 cites rule 3, and the §15.1, §25.11, and §16.5 rows drop their restatement of item 3 (SPEC-7).

No §25.4 edit is needed: a revoked impersonation row reaches `lenny-ops` through rule 1 of its revocation set, as every other gateway-side revoke does.

## Edge cases and accepted failure modes

- **Sign or record failure at issuance.** The started event exists and no bearer is returned (rule 1). The unpaired started event overstates the session and never understates it (rule 4).
- **Ended append fails after the revoke commits.** The bearer stays revoked, `DELETE` returns the append's error, and a retry returns `409` (rule 3). The session has no ended event, and its started event bounds its end (rule 4).
- **Cross-tenant-read emit fails on `DELETE` or in the cascade.** The revoke stands and the ended event is still attempted (rule 7).
- **Cross-tenant-read emit fails on `GET`.** No list is returned (rule 7; §15.1 row).
- **Concurrent `DELETE` on two replicas.** The conditional update has one winner. The loser receives `409` (rule 3; §15.1 row).
- **`DELETE` races a full revoke of the target or of the admin.** Whichever commits first revokes the row. A later `DELETE` receives `409` and writes no ended event (rules 3 and 4).
- **`POST` races a full revoke of the target or of the admin.** The `POST` is refused, or it commits its row before step 5 and rule 6 read `issued_tokens` (rule 1 (c)).
- **An operator revokes the caller's bearer by `jti` while a `POST` is in flight.** The `POST` can commit its row, as a session started before the revocation keeps its row (rule 4). `DELETE` or a full revoke of the admin ends the session.
- **A caller without `platform-admin` fully revokes a platform-admin.** No `__all__` query runs. Only the user's own tokens are revoked, and the response reports the skipped revocation (rule 6).
- **Two users in different tenants share one `sub`.** Rule 6 keys on `act_sub` alone, so a full revoke of either user by a `platform-admin` caller revokes the impersonation bearers of a platform-admin with that `sub`. This over-revokes and never under-revokes. Rule 1 (c) locks only the impersonating admin's own user row, so a `POST` in flight during the other user's full revoke can commit a row that rule 6 does not reach.
- **Expiry.** No event is written. The session ends no later than the started event's time plus `impersonation_duration_seconds` (rule 4), and the row leaves the `GET` listing at `exp` (rule 2).
- **`lenny-ops` latency.** `lenny-ops` refuses an ended bearer at its next §25.4 rule 1 read, which is not immediate (rule 5).

## Staged edits

### SPEC-1 · spec/13_security-model.md § 13.3 Credential Flow

Edit (a). In the paragraph that begins "**Tenant-scope enforcement (cross-tenant exchange prevention).**", replace

"platform-admin impersonation of a tenant user happens via a distinct admin-impersonation code path that writes its own `admin.impersonation_started` audit event and is NOT routed through `/v1/oauth/token`."

with

```
platform-admin impersonation of a tenant user happens via a distinct admin-impersonation code path that writes its own `admin.impersonation_started` audit event and is NOT routed through `/v1/oauth/token`, as "Platform-admin impersonation" below states.
```

Edit (b). Insert the following immediately after the paragraph that begins "**Tenant-scope enforcement (cross-tenant exchange prevention).**", as a new paragraph followed by its numbered list:

```
**Platform-admin impersonation.** A `platform-admin` starts, lists, and ends impersonation sessions through `POST`, `GET`, and `DELETE /v1/admin/impersonation` ([§15.1](15_external-api-surface.md#151-rest-api)). An impersonation session is the `issued_tokens` row ([§12.2](12_storage-architecture.md#122-storage-roles)) of the bearer it mints. The row is the only session state the gateway keeps, and every gateway replica reads it.

1. **Issuance.** The gateway first reads the `issued_tokens` row of the caller's bearer `jti` in the caller's tenant, and ends the call with `403 FORBIDDEN` and writes no audit row when step (1a) of "Write-before-issue ordering" would reject that row as a `subject_token`. It then (a) appends `admin.impersonation_started` ([§16.7](16_observability.md#167-section-25-audit-events)) under [§11.7](11_policy-and-controls.md#117-audit-logging) "Platform-tenant audit event residency"; (b) signs a `user_bearer` for the target user in the target tenant whose `jti` is the impersonation session id and whose `exp` is the started event's time plus the request's `durationSeconds` ([§15.1](15_external-api-surface.md#151-rest-api)); and (c) in a first transaction reads the user row of the caller's `tenant_id` and `sub`, and in a second transaction that commits before the first reads the user row of the target's `tenant_id` and `sub`, each under a shared row lock as the creation fence of [§12.8](12_storage-architecture.md#128-compliance-interfaces) "Lifecycle fence and drain" reads a session owner's row, ends the call with `403 FORBIDDEN` and writes no rejection audit row when either user row records an invalidation level of hard disable or full revoke ([§11.4](11_policy-and-controls.md#114-user-invalidation)), and otherwise records the bearer in `issued_tokens` in the second transaction with that `jti`, the target's `tenant_id` and `sub`, `act_sub` set to the impersonating admin's `sub`, `parent_jti` null, the token hash, `audience`, `issued_at`, and `exp`. A user with no user row carries no invalidation level. A `StoreRouter` that places a tenant's `issued_tokens` rows on an instance without its users' rows must replace the second transaction before rule 1 is served. A full revoke commits the user's invalidation level before step 1 of [§11.4](11_policy-and-controls.md#114-user-invalidation), so (c) either observes that level or commits before step 5 and rule 6 read `issued_tokens`. It returns the bearer only after the first transaction of (c) commits. It writes no `token.exchanged` row for the mint, so the row and every audit row of the mint carry a single `tenant_id`. A failure at (b) or (c) returns no bearer and leaves the started event without an ended event.
2. **Impersonation row.** A row with `act_sub IS NOT NULL AND parent_jti IS NULL` is an impersonation row. An impersonation row is active while its `revoked_at` is null and its `exp` is in the future. Every `issued_tokens` query of rules 3, 6, and 7 matches this predicate, so none of them reads or revokes another token's row.
3. **End.** `DELETE /v1/admin/impersonation/{id}` sets `revoked_at` and `revoked_reason = 'impersonation_ended'` on the impersonation row whose `jti` is the id, in one transaction and only while the row is active. After the transaction commits, the gateway pushes the `jti` into its revocation cache and publishes it to peer replicas as [§11.4](11_policy-and-controls.md#114-user-invalidation) step 5 does, and only then appends `admin.impersonation_ended`. Because the update is conditional on the row being active, exactly one of any number of concurrent calls on any replicas revokes the row. A call whose update matches no row revokes nothing and returns the error [§15.1](15_external-api-surface.md#151-rest-api) lists for an unknown or inactive session. An audit outage therefore never blocks the revocation. When the ended append fails after the transaction commits, the bearer stays revoked, the call returns the append's error, and the session has no ended event.
4. **End by another route.** A session that `DELETE` does not end ends no later than its started event's time plus `impersonation_duration_seconds`. Expiry writes no event. A full revoke of the target user (step 5 of [§11.4](11_policy-and-controls.md#114-user-invalidation) reaches the row through the target's `tenant_id` and `sub`), a full revoke of the impersonating platform-admin by a `platform-admin` caller (rule 6), and an operator revocation of the bearer's `jti` each revoke the row under their own audit behaviour, and none of them writes `admin.impersonation_ended`.
5. **Refusal.** Every gateway replica refuses a revoked impersonation bearer through its revocation cache, every mint that applies step (1a) of "Write-before-issue ordering" to it refuses it whether or not it is revoked, and `lenny-ops` refuses it at its next [§25.4](25_agent-operability.md#254-the-lenny-ops-service) "Revocation set" rule 1 read.
6. **Full-revoke cascade.** When a `platform-admin` caller fully revokes a user, step 5 of [§11.4](11_policy-and-controls.md#114-user-invalidation) also revokes every active impersonation row whose `act_sub` is that user's `sub`, in every tenant, whatever roles the user holds when revoked. The revocation keys on `act_sub` alone, so a full revoke of a user who shares a platform-admin's `sub` also revokes that platform-admin's impersonation bearers. When the caller does not hold `platform-admin`, step 5 revokes only the user's own issued tokens and reports in the response's token-revocation error that the revocation of impersonation bearers the user started was skipped.
7. **Cross-tenant reads.** The `DELETE` lookup, the `GET /v1/admin/impersonation` listing, and the rule 6 revocation read `issued_tokens` under the `__all__` sentinel only after the gateway confirms that the caller holds `platform-admin` ([§4.2](04_system-components.md#42-session-manager)). Each call emits one `cross_tenant_read` event ([§12.3](12_storage-architecture.md#123-postgres-ha-requirements)) with query category `impersonation_session`. `GET` emits the event before it returns rows and returns no list when the emit fails. `DELETE` and the rule 6 revocation emit the event after the revocation commits; an emit failure is reported, does not undo the revocation, and does not stop the ended append.
```

Edit (c). In the paragraph that begins "**Write-before-issue ordering.**", replace

"and reject the exchange when either row has `revoked_at` set, with `invalid_grant` reason `subject_token_revoked` for the subject and `actor_token_revoked` for the actor;"

with

```
and reject the exchange when either row has `revoked_at` set, with `invalid_grant` reason `subject_token_revoked` for the subject and `actor_token_revoked` for the actor, and otherwise when either row is an impersonation row ("Platform-admin impersonation" rule 2), with reason `subject_token_impersonation` or `actor_token_impersonation`;
```

### SPEC-2 · spec/10_gateway-internals.md § 10.2 Authentication

Edit (a). In the paragraph that begins "**Signing topology.**", in the sentence "The gateway mints in process, through its `JWTSigner`, the bootstrap admin credential (...), platform-admin impersonation bearers, playground bearers, and `session_capability` tokens on the session-creation path.", replace "platform-admin impersonation bearers" with:

```
platform-admin impersonation bearers ([§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation")
```

Edit (b). In the same paragraph, replace

```
in one transaction under statements (1), (2), and (3) of [§13.3](13_security-model.md#133-credential-flow) "Write-before-issue ordering" before it returns the token.
```

with

```
in one transaction under statements (1), (1a), (2), and (3) of [§13.3](13_security-model.md#133-credential-flow) "Write-before-issue ordering", with the caller's bearer as the `subject_token`, before it returns the token. When step (1a) rejects, session creation fails with `403 FORBIDDEN`.
```

Edit (c). In the paragraph that begins "**Playground mint invariants (mirror of the §13.3 OAuth token-exchange invariants).**", in numbered item 1 "**Subject-token type restriction.**", append after "the invariant is evaluated at mint time as a defense-in-depth check).":

```
 The mint also applies step (1a) of [§13.3](13_security-model.md#133-credential-flow) "Write-before-issue ordering" to the subject's `jti`, and rejects a subject that step rejects with the same status and error code, `details.subjectTyp` carrying the subject's `typ`.
```

Edit (d). In the table under "**Playground mint failure modes (incremental to the §27.3.1 OIDC-specific table):**", in the row that begins "| Subject token `typ` is not `user_bearer`", replace "(capability / delegation / service token pasted into `apiKey` mode)" with "(capability / delegation / service token pasted into `apiKey` mode), or a subject that step (1a) rejects (invariant 1)", and replace "`details.subjectTyp` carries the offending value" with "`details.subjectTyp` carries the subject's `typ`". The row stays one physical table line.

### SPEC-3 · spec/12_storage-architecture.md § 12.2 Storage Roles and § 12.6 Interface Design

In the `TokenIssuanceStore` row, replace "`act_sub` (text NULL, present for delegation children)" with:

```
`act_sub` (text NULL; the acting subject: present for delegation children, and, with `parent_jti` null, for platform-admin impersonation bearers, where it is the admin's `sub`; the pair `act_sub IS NOT NULL AND parent_jti IS NULL` identifies an impersonation row, see [§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation")
```

In the same row, replace "`(exp)` (expired-row GC)." with:

```
`(exp)` (expired-row GC), `(act_sub)` WHERE `act_sub IS NOT NULL AND parent_jti IS NULL` (impersonation listing and platform-admin full revoke).
```

The row stays one physical table line.

In the §12.6 paragraph that begins "**Scaling extension interfaces.**", replace "requires a multi-shard `StoreRouter` to replace the creation fence." with:

```
requires a multi-shard `StoreRouter` to replace the creation fence, or [§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 1 (c) requires it to replace that rule's second transaction.
```

### SPEC-4 · spec/16_observability.md § 16.7 Section 25 Audit Events

In the bullet that begins "- `admin.impersonation_started`":

Edit (a). Replace "`ticket_id` (external justification reference, e.g., a support ticket)." with:

```
`ticket_id` (external justification reference, e.g., a support ticket), `impersonation_session_id` (the bearer's `jti`).
```

Edit (b). Replace "A matching `admin.impersonation_ended` event is emitted when the impersonation session terminates." with:

```
`admin.impersonation_ended` is emitted as [§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rules 3 and 4 state. Payload fields: `admin_sub`, `admin_tenant_id`, `target_tenant_id`, `target_user_id`, `impersonation_session_id`, `ended_by`, `started_at`, and `ended_at`.
```

The residency sentence and the `PLATFORM_AUDIT_REGION_UNRESOLVABLE` sentence stay unchanged.

### SPEC-5 · spec/11_policy-and-controls.md § 11.4 User Invalidation

Edit (a). Replace "**Full revoke propagation mechanism:**" with

```
**Full revoke propagation mechanism:** The gateway commits the user's invalidation level to the user's row before step 1 begins.
```

Edit (b). In **Full revoke propagation mechanism:** step 5, append after "so every replica's cache rejects the tokens within seconds.":

```
 Step 5 also revokes the impersonation bearers the user started, under the conditions of [§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 6, and pushes and fans out those token ids the same way. A failure of that revocation does not stop the revocation or cache push of the user's own issued tokens.
```

### SPEC-6 · spec/15_external-api-surface.md § 15.1 REST API

Edit (a). Insert the following three rows immediately after the row that begins "| `POST`   | `/v1/admin/users/{userId}/rotate-token`". Each row is one physical line.

```
| `POST`   | `/v1/admin/impersonation`                                       | Start a platform-admin impersonation session ([§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 1). Body: `{"targetTenantId", "targetUserId", "reason", "ticketId", "durationSeconds"}`; every field is required, and `durationSeconds` is from 60 to 3600 (the maximum impersonation duration). Response `200`: `{"impersonationSessionId", "bearerToken", "tokenType": "Bearer", "targetTenantId", "targetUserId", "expiresAt"}`; the bearer is a `user_bearer` for the target user in the target tenant. Emits `admin.impersonation_started`. Errors: `400 VALIDATION_ERROR` (a missing field, or `durationSeconds` out of range), `403 FORBIDDEN` (the caller lacks `platform-admin`, or [§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 1 refuses the call), `404 RESOURCE_NOT_FOUND` (the target tenant or user does not exist), `422 PLATFORM_AUDIT_REGION_UNRESOLVABLE`, `503 KMS_SIGNING_UNAVAILABLE`, and `500 INTERNAL_ERROR`. Requires `platform-admin`. |
| `GET`    | `/v1/admin/impersonation`                                       | List the active impersonation sessions in every tenant ([§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 2). Any gateway replica serves the call. Response `200`: the cursor-paginated envelope ("Cursor-based pagination" in this section), whose `items` are `{"impersonationSessionId", "adminSub", "targetTenantId", "targetUserId", "issuedAt", "expiresAt"}`. Reads across tenants as [§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 7 states. Errors: `403 FORBIDDEN`, and `500 INTERNAL_ERROR` (including when the cross-tenant-read audit event cannot be written, in which case no list is returned). Requires `platform-admin`. |
| `DELETE` | `/v1/admin/impersonation/{id}`                                  | End an impersonation session by revoking its bearer ([§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 3). Any gateway replica serves the call. Response `200`: `{"impersonationSessionId", "endedAt"}`. Emits `admin.impersonation_ended`. Reads across tenants as [§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 7 states. A failure to write the ended event after the revocation commits leaves the bearer revoked ([§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation"). Errors: `403 FORBIDDEN`, `404 RESOURCE_NOT_FOUND` (no impersonation session has the id), `409 INVALID_STATE_TRANSITION` (the bearer is revoked or expired; `details.currentState` is `revoked` or `expired`, and `details.allowedStates` is `["active"]`), `422 PLATFORM_AUDIT_REGION_UNRESOLVABLE` (the ended event could not be written and the bearer stays revoked), and `500 INTERNAL_ERROR`. Requires `platform-admin`. |
```

Edit (b). In the **Error code catalog:** row that begins "| `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED`", replace "rejected the subject token because its `typ` is not `user_bearer`." with

```
rejected the subject token because its `typ` is not `user_bearer` or because [§13.3](13_security-model.md#133-credential-flow) "Write-before-issue ordering" step (1a) rejects it.
```

and replace "`details.subjectTyp` carries the offending value (`session_capability` \| `a2a_delegation` \| `service_token`)" with "`details.subjectTyp` carries the subject's `typ`", and replace "the caller must obtain a `user_bearer`-typed credential" with "the caller must obtain a `user_bearer`-typed credential that step (1a) admits". The row stays one physical table line.

### SPEC-7 · spec/11_policy-and-controls.md § 11.7 Audit Logging; spec/15_external-api-surface.md § 15.1 REST API; spec/25_agent-operability.md § 25.11 Backup and Restore API; spec/16_observability.md § 16.5 Alerting Rules and SLOs

Edit (a). In §11.7, in the paragraph that begins "**Platform-tenant audit event residency (CMP-058, fail-closed).**", numbered item 3, insert after "and decommission is rolled back.":

```
 Ending an impersonation session is ordered by [§13.3](13_security-model.md#133-credential-flow) "Platform-admin impersonation" rule 3 instead.
```

Edit (b). In §15.1, in the **Error code catalog:** row that begins "| `PLATFORM_AUDIT_REGION_UNRESOLVABLE`", delete "The originating operation halts (impersonation ticket issuance fails, Phase 3.5 escrow ledger write aborts the tenant-delete into `deleting` state, `compliance.profile_decommissioned` rolls back) because audit must be durable before any externally observable side effect. ".

Edit (c). In §25.11, in the "### Error Codes" row that begins "| `PLATFORM_AUDIT_REGION_UNRESOLVABLE`", delete "The originating operation halts (impersonation issuance, Phase 3.5 escrow ledger write, `compliance.profile_decommissioned`, etc.). ".

Edit (d). In §16.5, in the row that begins "| `PlatformAuditResidencyViolation`", replace "Because the originating operation halts until audit commits, this alert correlates with" with "This alert correlates with", and delete " before the originating operation will succeed".

Each edited row keeps its closing §11.7 pointer and stays one physical table line.

## Spec files touched

- spec/13_security-model.md (SPEC-1)
- spec/10_gateway-internals.md (SPEC-2)
- spec/12_storage-architecture.md (SPEC-3)
- spec/16_observability.md (SPEC-4, SPEC-7)
- spec/11_policy-and-controls.md (SPEC-5, SPEC-7)
- spec/15_external-api-surface.md (SPEC-6, SPEC-7)
- spec/25_agent-operability.md (SPEC-7)
