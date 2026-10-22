# Spec changes: Data lifecycle second half: remaining erasure stores, Phase 3 lease revocation, audit-receipt foreign key, and full_revoke

## Design (as the spec must state it)

After these edits the specification states the following mechanisms. Each staged edit below is the normative home of its rule, and this section names the homes.

- **Redaction receipts outlive the rows they describe.** `audit_redaction_receipts.audit_event_id` carries no foreign key, and verifiers locate a receipt by tenant and sequence number (SPEC-1).
- **Phase 3 revokes every credential of a deleting tenant.** The controller commits `deleting` first, the tenant then stops being registered for `tenant_id` extraction, the lease step of §11.4 returns the leases of the tenant's sessions, and `TokenStore.DeleteByTenant` and the access-token cache flush follow (SPEC-2). In a multi-tenant deployment the registration rule in §10.2 makes the gateway refuse the tenant's bearers (SPEC-3). A single-tenant deployment ignores every bearer's tenant claim and serves `default` (§10.2), and the non-spec subsection **Tenant liveness and the default tenant** states what bounds a deleted tenant's bearers there. A single-tenant deployment never deletes `default`, and a multi-tenant deployment deletes it as an ordinary tenant (SPEC-2).
- **The tenant ID `platform` is reserved** for the platform audit chain, so no tenant's Phase 4 audit entry reaches that chain (SPEC-3).
- **full_revoke lists from the primary, returns every lease without deny-listing it, and continues past a failed effect** (SPEC-4).
- **Phase 4 erases every store §12.8 names and deletes the originals of escrowed resources.** The scope table and step 18 name `TokenIssuanceStore`, the Phase 4 order names the issued-token index, the exempt billing rule, and the tenant's definitions, and the escrow marker rule is removed (SPEC-5 and SPEC-6).
- **Issued-token rows are kept until they leave the §13.3 trailing hour.** §13.3 **Authoritative durability for revocation** states the retention rule once, and user erasure step 18, tenant deletion Phase 4, and the expired-row GC follow it (SPEC-5 Edit 5d).

## Edge cases and accepted failure modes

1. **A bearer of a deleting tenant presented during a Phase 3.5 legal-hold pause** to a multi-tenant deployment is refused with `403 TENANT_NOT_FOUND` by the SPEC-3 row. A tenant-admin of that tenant therefore cannot release the tenant's holds, and a `platform-admin` releases them or force-deletes the tenant.
2. **Issued-token rows of an erased user or a deleted tenant** outlive the erasure job as the §13.3 retention rule (SPEC-5 Edit 5d) states.
3. **The access-token cache flush of Phase 3** stays normative. The cache has no production writer, so the flush deletes nothing today, and the non-spec REC-1 records the gap as a departure.
4. **A replica crash during full_revoke** is outside the SPEC-4 Note: a client disconnect does not stop the effects on the handling replica, and a crash of that replica stops them. Every effect is idempotent, so an operator repeats the call.
5. **Deleting `default` in a multi-tenant deployment** refuses the initial admin token from Phase 3 onward, and every later bootstrap run whose seed still names `default` reports that entry as an `error`. SPEC-2 Edits 2b and 2c state them.

## Staged edits

### SPEC-1 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces: `RedactionReceipt` schema

**Edit 1a.** In **`DeleteByUser` dependency-ordered deletion sequence.**, step 14, **`RedactionReceipt` schema.**, replace the bullet `    - `audit_event_id` (UUID, foreign key `audit_log.id`) — the redacted row.` with:

```markdown
    - `audit_event_id` (UUID): the `id` of the redacted `audit_log` row. The column carries no foreign key, because the receipt outlives the row: the [§16.4](16_observability.md#164-logging) retention sweep, a partition force-drop, and tenant-deletion Phase 4 each delete audit rows and never delete receipts. Verifiers locate a receipt by (`tenant_id`, `sequence_number`).
```

**Edit 1b.** In the grant-restriction paragraph that follows the field list (it opens "The table is grant-restricted:"), replace `a receipt outlives the redacted row if the partition containing the row is ever force-dropped, but the receipt itself` with:

```markdown
a receipt outlives the redacted row (see `audit_event_id`), and the receipt itself
```

### SPEC-2 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces: Tenant deletion lifecycle; spec/17_deployment-topology.md § 17.6 Packaging and Installation

**Edit 2a.** In the **Tenant deletion lifecycle.** table, replace the row that opens `| 3. Revoke credentials | `deleting`    |` with the row below. The row stays one physical line.

```markdown
| 3. Revoke credentials | `deleting`    | The controller commits `TenantState` `deleting` before any other action of this phase. From then on the tenant is not registered for `tenant_id` extraction ([Section 10.2](10_gateway-internals.md#102-authentication)), so in a multi-tenant deployment the gateway rejects every bearer token of the tenant with `403 TENANT_NOT_FOUND`, whenever it was issued. The controller lists the tenant's sessions from the Postgres primary and returns every credential lease they hold through the lease step of [§11.4](11_policy-and-controls.md#114-user-invalidation) step 1. It then deletes the tenant's downstream OAuth tokens, refresh tokens, and registered user credentials via `TokenStore.DeleteByTenant`, and flushes the tenant's entries from the Redis access-token cache ([Section 4.3](04_system-components.md#43-token-service)) so that no cached downstream access token outlives the TokenStore rows it was derived from. Each action is idempotent, and a job resumed at Phase 3 runs them again. |
```

**Edit 2b.** In the paragraph that opens **Tenant deletion lifecycle.**, after `persisted in Postgres and exposed via the admin API.`, insert:

```markdown
In a single-tenant deployment ([Section 10.2](10_gateway-internals.md#102-authentication)), the gateway refuses a request to delete or force-delete the built-in `default` tenant with `409 INVALID_STATE_TRANSITION`, and the tenant stays `active`. In a multi-tenant deployment, `default` is deleted like any other tenant, and when the deleted tenant holds the initial `platform-admin` user `lenny-admin` ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation); the gateway's `--admin-token-tenant`, `default` unless set), the gateway refuses that user's admin token from Phase 3 onward.
```

**Edit 2c.** In §17.6 item 5 **Upsert semantics — full specification.**, after the table row that opens `| Resource exists and `--force-update` would overwrite a security-critical field`, insert the row below. The row stays one physical line.

```markdown
   | Tenant entry whose `id` is the reserved `platform` ([Section 10.2](10_gateway-internals.md#102-authentication)), or that names an existing tenant in the `deleting` or `deleted` state ([Section 12.8](12_storage-architecture.md#128-compliance-interfaces) **Tenant deletion lifecycle**) | **Error** — no tenant is created or changed. Exit code: 2, or 1 when no entry succeeds. | **Error** — blocked. |
```

In §17.6 item 3, **Idempotent** bullet, replace `in which case they are updated to match the seed file.` with `in which case item 5 gives the outcome.`

### SPEC-3 · spec/10_gateway-internals.md § 10.2 Authentication; spec/27_web-playground.md § 27.3.1 OIDC cookie-to-MCP-bearer exchange; spec/04_system-components.md § 4.2 Session Manager

**Edit 3a.** In §10.2, replace the label `**`tenant_id` extraction from OIDC tokens:**` with `**`tenant_id` extraction:**`, and in the same paragraph replace `The gateway reads this claim from the validated OIDC ID token immediately after signature verification and before any further request processing.` with:

```markdown
The gateway reads the tenant identifier from every bearer token it validates, from this claim in an OIDC ID token and from the `tenant_id` claim in a gateway-issued token, immediately after signature verification and before any further request processing.
```

The sentence on `auth.tenantIdClaim` before it is unchanged.

**Edit 3b.** In the table that follows, replace the row `| Claim present but tenant not registered | Request rejected: `403 Forbidden`, error code `TENANT_NOT_FOUND`. |` with:

```markdown
| Claim present but tenant not registered. A tenant whose `TenantState` is `deleting` or `deleted` is not registered ([§12.8](12_storage-architecture.md#128-compliance-interfaces) **Tenant deletion lifecycle**, Phase 3). | Request rejected: `403 Forbidden`, error code `TENANT_NOT_FOUND`. |
```

**Edit 3c.** In spec/27_web-playground.md, **Tenant-claim rejection codes (OIDC callback).** table, row `TENANT_NOT_FOUND`, replace the condition cell `Extracted value is well-formed but does not match any provisioned `Tenant` CR` with:

```markdown
Extracted value is well-formed but names no registered tenant ([§10.2](10_gateway-internals.md#102-authentication))
```

The other cells of the row are unchanged.

**Edit 3e.** In §10.2 **`tenant_id` format validation.**, after `with `401 TENANT_CLAIM_INVALID_FORMAT` (before the tenant-registered lookup).`, insert:

```markdown
The tenant ID `platform` is reserved for the platform tenant, under which the platform audit chain is written ([Section 11.7](11_policy-and-controls.md#117-audit-logging)). `POST /v1/admin/tenants` rejects it with `422 RESERVED_IDENTIFIER`.
```

**Edit 3d.** In spec/04_system-components.md §4.2, keep the label **`tenant_id` claim extraction:** and replace the rest of that paragraph, from `The OIDC claim used to extract the tenant identifier` through `emitted as `auth_failure` audit events.`, with:

```markdown
The gateway extracts and validates the `tenant_id` of every request as [Section 10.2](10_gateway-internals.md#102-authentication) **`tenant_id` extraction** states.
```

### SPEC-4 · spec/11_policy-and-controls.md § 11.4 User Invalidation

**Edit 4a.** In §11.4 **Full revoke propagation mechanism:**, replace step 1 with:

```markdown
1. The gateway lists every session the user has, reading the Postgres primary so a session committed before the lookup is included (via SessionStore), and the listing includes terminal sessions. Before it writes any cancel in step 2, the gateway returns every credential lease those sessions hold to its pool, which revokes the lease ([Section 4.9](04_system-components.md#49-credential-leasing-service)). The step writes no credential deny-list entry.
```

**Edit 4b.** In step 2, replace `The gateway marks each of those sessions `cancelled` in the SessionStore.` with:

```markdown
The gateway marks each listed session that is not terminal `cancelled` in the SessionStore.
```

**Edit 4c.** Replace step 6 with:

```markdown
6. Step 1 returns the credential leases held by the listed sessions.
```

**Edit 4d.** In the Note that follows the steps, make the replacements below:

- Replace `it revokes the credential leases of the user's non-terminal sessions, cancels those sessions in the SessionStore,` with `it returns the credential leases of the user's sessions, cancels the non-terminal ones in the SessionStore,`.
- Replace `The reported lease count is the number of leases step 1 adds to the deny list.` with `The reported lease count is the number of leases step 1 returns.`.
- After `The response body reports the per-effect counts and any partial-failure detail for the effects enumerated above.`, insert:

```markdown
A failure of one effect, or of one session's cancel write, does not stop the other effects, and the response reports it as partial-failure detail. A client disconnect does not stop the effects on the handling replica.
```

- Replace `the gateway publishes the step-2 release request, the token revocations, and the credential deny-list entries on Redis pub/sub channels` with `the gateway publishes the step-2 release request and the token revocations on Redis pub/sub channels`.
- Replace `peer-replica pods, revocation-cache entries, and deny-list entries follow asynchronously` with `peer-replica pods and revocation-cache entries follow asynchronously`.

### SPEC-5 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces: erasure scope table, deletion step 18, and Tenant deletion Phase 4; spec/13_security-model.md § 13.3 Credential Flow: Authoritative durability for revocation

**Edit 5a.** In **Storage backends in erasure scope:**, replace the `TokenStore` row with the two rows below. Each row stays one physical line.

```markdown
| `TokenStore`           | Postgres                | OAuth tokens, refresh tokens, and registered user credentials ([§4.9](04_system-components.md#49-credential-leasing-service))  |
| `TokenIssuanceStore`   | Postgres                | Issued-token index rows (`issued_tokens`, [§12.2](#122-storage-roles)) whose `sub` is the user, except the rows that [§13.3](13_security-model.md#133-credential-flow) **Authoritative durability for revocation** keeps. |
```

**Edit 5b.** In **`DeleteByUser` dependency-ordered deletion sequence.**, replace step 18 with:

```markdown
18. `TokenStore` and `TokenIssuanceStore`: delete OAuth tokens, refresh tokens, and registered credentials for the user, and the issued-token index rows whose `sub` is the user, except the rows that [§13.3](13_security-model.md#133-credential-flow) **Authoritative durability for revocation** keeps.
```

**Edit 5c.** In the **Tenant deletion lifecycle.** table, Phase 4 row, make the replacements below. The row stays one physical line.

- Replace `billing Redis stream (`XGROUP DESTROY t:{tenant_id}:billing:stream billing-flusher` then `DEL t:{tenant_id}:billing:stream`)` with:

```markdown
billing Redis stream (`XGROUP DESTROY t:{tenant_id}:billing:stream billing-flusher` then `DEL t:{tenant_id}:billing:stream`; skipped for a tenant whose `billingErasurePolicy` is `exempt`)
```

- Replace `` `EventStore` (billing, respecting `billingErasurePolicy`) `` with:

```markdown
`EventStore` (billing; a tenant whose `billingErasurePolicy` is `exempt` keeps its billing events until `billing.retentionDays` ages them out ([§11.2.1](11_policy-and-controls.md#1121-billing-event-stream)); otherwise they are deleted)
```

- Replace `` → `SessionStore` → `TokenStore` → `CredentialPoolStore` → `` with:

```markdown
→ `SessionStore` → `TokenStore` → `TokenIssuanceStore` (the tenant's issued-token index rows, except those that [§13.3](13_security-model.md#133-credential-flow) **Authoritative durability for revocation** keeps) → `CredentialPoolStore` →
```

- Replace `→ custom role definitions for this tenant → tenant configuration record (including explicit KMS destruction of the `erasure_salt` key material via `ScheduleKeyDeletion` / `DestroyCryptoKeyVersion` if a salt was present; the KMS key destruction is recorded in the erasure receipt).` with:

```markdown
→ custom role definitions for this tenant → the tenant's connector, environment, and experiment definitions → tenant configuration record.
```

- Replace `` `DeleteByTenant` on `ArtifactStore` and on the `EventStore` audit partitions explicitly **excludes** any resource that Phase 3.5 migrated to the legal-hold escrow bucket; the escrow records carry the `legal_hold_escrow: true` marker (see Phase 3.5 and **Legal hold interaction during deletion** below) and are skipped by `DeleteByTenant` for the same rationale that `gdpr.*` rows are skipped. `` with:

```markdown
`DeleteByTenant` deletes the originals of resources that Phase 3.5 copied to the legal-hold escrow bucket. The escrow copy and its legal-hold ledger record are the retained evidence, and both live outside the tenant's stores (see Phase 3.5 and **Legal hold interaction during deletion** below).
```

- Replace `with `state = 'deleted'` and all mutable fields nulled.` with:

```markdown
with `state = 'deleted'` and every mutable field nulled except those that a resumed or later phase, or a reader of the tombstone snapshot, still reads, such as `billingErasurePolicy`, `workspaceTier`, and `dataResidencyRegion` ([§11.7](11_policy-and-controls.md#117-audit-logging)).
```

**Edit 5d.** In spec/13_security-model.md §13.3 **Authoritative durability for revocation.**, after `(the trailing hour covers tokens that expired after revocation and may still be presented by a buggy client).`, insert:

```markdown
An `issued_tokens` row is kept, revoked or not, until its `exp` precedes that trailing hour, so that a later revocation or rotation of a token that can still be presented finds its row and a replica that restarts loads every revocation it must still enforce. The expired-row GC ([§12.2](12_storage-architecture.md#122-storage-roles)), user erasure ([§12.8](12_storage-architecture.md#128-compliance-interfaces) step 18), and tenant deletion Phase 4 ([§12.8](12_storage-architecture.md#128-compliance-interfaces) **Tenant deletion lifecycle**) each delete only rows past the trailing hour, and the GC covers every tenant, a deleted tenant included.
```

### SPEC-6 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces: `--acknowledge-hold-override` admin action (tenant scope)

SPEC-6 lands with SPEC-5's escrow replacement and is dropped if that replacement is dropped.

**Edit 6a.** In sub-step 3, replace `as an authorized export and the post-Phase-4 deletion of the original audit row as a `redacted_gdpr` discontinuity — but because Phase 4 explicitly excludes escrowed rows from `DeleteByTenant` (see Phase 4 actions above), the originals are not deleted; only the escrow copy exists for post-tenant-tombstone retrieval.` with:

```markdown
as an authorized export. Phase 4 deletes the original rows, and the escrow copy is the record retrieved after the tenant tombstone.
```

**Edit 6b.** In sub-step 4, replace `The ledger update is part of the same transaction that marks the ArtifactStore / EventStore record as `legal_hold_escrow: true` so that Phase 4's `DeleteByTenant` skip logic can observe the marker without re-reading the audit chain.` with:

```markdown
The ledger write also persists the durable ledger row that the escrow-GC release reads to locate and delete the escrow object when the hold is cleared.
```

**Edit 6c.** In sub-step 4, replace `Phase 4 does not proceed because the escrow ciphertext must be accompanied by a durable ledger row or Phase 4's `DeleteByTenant` skip logic cannot distinguish escrowed from non-escrowed records.` with:

```markdown
Phase 4 does not proceed because the escrow ciphertext must be accompanied by a durable ledger row, without which the escrow-GC release cannot locate the escrow object.
```

**Edit 6d.** In the paragraph after the sub-steps, replace `Phase 4 then proceeds with `DeleteByTenant` excluding the escrowed records (see Phase 4 actions above), and Phase 4a destroys` with:

```markdown
Phase 4 then proceeds with `DeleteByTenant`, which deletes the originals of the escrowed records (see Phase 4 actions above), and Phase 4a destroys
```

## Spec files touched

- `spec/04_system-components.md` (SPEC-3)
- `spec/10_gateway-internals.md` (SPEC-3)
- `spec/11_policy-and-controls.md` (SPEC-4)
- `spec/12_storage-architecture.md` (SPEC-1, SPEC-2, SPEC-5, SPEC-6)
- `spec/13_security-model.md` (SPEC-5)
- `spec/17_deployment-topology.md` (SPEC-2)
- `spec/27_web-playground.md` (SPEC-3)
