# Proposal: Reconcile §12.8 audit-log GDPR erasure to the shipped chain-safe implementation: drop the phantom `audit_log.user_id` column, restate step-14 as a receipt-authorized redaction discontinuity, reconcile step-13's unimplementable mid-chain DELETE, and align `DeleteByTenant` with the `gdpr.*` skip (closes F-12.8.4, F-12.2.5)

- **Status:** Approved (2026-07-04). Signed off by the user for implementation; both open decisions resolved with the recommended/staged option, neither altering the staged edits: (1) step-13 = direction (b), retain ordinary audit rows under the tamper-evidence + legal-obligation (GDPR Art. 17(3)(b)/(e)) basis with `DeleteByUser` a no-op, rather than per-row redaction; (2) `DeleteByTenant` excludes `gdpr.%` from the Phase-4 teardown, keeps the standalone compliance-receipt remnant, and scopes the continuity verifier to skip `deleting`/`deleted` tenants, rather than a whole-chain teardown reliant on SIEM copies. Genuinely converged on the hardened workflow (5 rounds, 2 consecutive clean rounds, every round complete).
- **Date:** 2026-07-03.
- **Scope:** Reconciles the §12.8 GDPR erasure sequence and the §12.2 store catalog to the audit-erasure behavior already built and chain-consistent in the tree. Findings F-12.8.4 (High) and F-12.2.5 (High) are the same residual seen from two catalogs; both are DEFERRED on one spec contradiction. The §12.1 audit erasure pair, `DeleteByTenant`, the `gdpr.%` retention carve-out, the step-14 dead-letter redaction, the `audit_redaction_receipts` table (migrations 0160/0165), the KMS-signed `RedactionReceipt`, the chain verifier's `redacted_gdpr` classification, and the orchestrator wiring already ship. This proposal rewrites the divergent spec passages to match the shipped model: the phantom `user_id` column in §12.8 step-14 and the Article 20 export; the step-14 chain re-seal wording in §12.8, the `RedactionReceipt.new_hash` field, the parallel re-seal language in the §18 build deliverable, the missing-receipt-condition wording in the §16.5 alert rows, the §25 chain-integrity enumeration, and the metrics reference, and the same "chain rewrite boundary" model in the single-source §16.5 alert catalog (`pkg/alerting/rules/rules.go`, which the Helm chart and the in-gateway alert tracker consume) and its external-verifier code comment; step-13's unimplementable mid-chain DELETE, the scope-table row that mirrors it, and the `DeleteByUser`-filters-`gdpr.%` clause. Its code fixes are the `DeleteByTenant` `gdpr.*` skip, the chain-continuity verifier reconciliation so the retained `gdpr.*`-only remnant does not raise a false chain-broken alert across the tenant-teardown window (resolving the tenant deletion-state skip-set from the control-plane Postgres so the exclusion holds under the §12.3 split billing/audit-pool topology), and the alert-catalog `Description` correction. It touches `spec/12_storage-architecture.md`, `spec/16_observability.md`, `spec/18_build-sequence.md`, `spec/25_agent-operability.md`, `docs/reference/metrics.md`, `pkg/gateway/audit/auditstore/auditstore.go`, `pkg/audit/integrity/continuity.go`, `pkg/audit/integrity/periodic.go`, `pkg/audit/integrity/redaction.go`, `pkg/alerting/rules/rules.go` (with `make generate` refreshing `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, and `pkg/embedded/manifests/manifests.yaml`), the gateway wiring (`cmd/lenny-gateway/metricsbackfill.go`, `runserver.go`, `main.go`), the tests, and `BUILD-GAPS.md`. It adds no new RPC, HTTP endpoint, store method, or migration.

This document stages the proposed spec and code changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

F-12.8.4 (`BUILD-GAPS.md:22647`, "Audit-log GDPR erasure … is unimplemented", High, OPEN) and F-12.2.5 (`BUILD-GAPS.md:19556`, "`EventStore` (audit) exposes no erasure interface", High, OPEN) are the same residual seen from two catalogs: the §12.8 erasure sequence and the §12.2 store-role list. Both are DEFERRED, and both record the identical blocker (`BUILD-GAPS.md:22665`): the §12.2-line-16-versus-§12.8-step-14 `user_id` contradiction, and the §11.7 `chainIntegrity` enumeration with no authorized-deletion state.

Against the current tree, most of the named surface is built and chain-consistent. The §12.1 `EventStore` erasure pair exists and is compile-asserted (`pkg/gateway/audit/auditstore/auditstore.go:59-66`). `DeleteByTenant` is a tenant-wide `DELETE` under `SET LOCAL lenny.erasure_mode = 'true'` (`auditstore.go:475-499`). The `gdpr.%` retention carve-out is enforced by `PruneRetention` (`auditstore.go:549-599`, the §16.4 sweep). The step-14 dead-letter redaction is wired end to end: payload-key row matching (`pkg/gateway/audit/auditstore/redaction.go:26-45,57-91`, deliberately keyed on payload keys because `audit_log` has no `user_id` column), in-place `RedactDeadLettered` rewriting only the two payload columns and leaving `prev_hash` and the row's identity columns untouched (`redaction.go:112-167`, `147-150`), the `audit_redaction_receipts` table (migration 0160) with the column-scoped `UPDATE (payload, payload_canonical_json)` grant that keeps `prev_hash` immutable (migration 0165), the KMS-signed `deadletterredaction` service emitting `gdpr.erasure_deadletter_redacted` (`pkg/gateway/storage/deadletterredaction/redaction.go`), wired via `erasureRunner.WithDeadLetterRedaction` (`cmd/lenny-gateway/adminrouter.go:687`, `pkg/gateway/storage/erasurejob/runner.go`), and the chain verifier's `redacted_gdpr` classification (`pkg/audit/chain.go:412-462`). §11.7 already describes `redacted_gdpr` as an in-place receipt-authorized discontinuity (`spec/11_policy-and-controls.md:369`).

The genuine residuals are four spec contradictions plus one code-versus-spec divergence, all confirmed against the tree.

### 1.1 Phantom `audit_log.user_id` column

The authoritative `audit_log` column list (`spec/12_storage-architecture.md:16`) and the schema (`migrations/0001_initial_schema.up.sql:130-155`) define no `user_id` column and no `session_id` column. The table's `tenant_id` is deliberately not a foreign key to `tenants` (the `platform` pseudo-tenant has no `tenants` row, `0001_initial_schema.up.sql:131-134`). Yet §12.8 step-14 (`spec/12_storage-architecture.md:813`) both reads the column ("whose `user_id` column equals the target user") and writes it ("the `user_id` column is set to `NULL`"), and the normative Article 20 DSAR export repeats it in prose (`spec/12_storage-architecture.md:962`) and in its template query (`spec/12_storage-architecture.md:975`, `WHERE tenant_id = $2 AND user_id = $1`). The shipped code resolves this by payload-key matching (`userMatchKeys`, `redaction.go:26-29`, with the in-code note "the `audit_log` table has no `user_id` column") for redaction, and the OCSF projector derives `actor.user.uid` from the single payload key `user_id` (`pkg/audit/ocsf/ocsf.go:518-519`; `spec/11_policy-and-controls.md:399`). The spec text is the defect.

### 1.2 Chain re-sealing wording

§12.8 step-14 (`spec/12_storage-architecture.md:813`) and the `RedactionReceipt.new_hash` field (`spec/12_storage-architecture.md:822`) require physically re-sealing the chain by writing the recomputed hash into the row's own position and into the immediately subsequent row's `prev_hash`. The shipped mechanism rewrites the two payload columns, leaves `prev_hash` and the row's identity columns untouched so the chain link is preserved, and authorizes the resulting content-hash break with the signed receipt's pre-redaction `original_hash` (`redaction.go:112-167`; `chain.go:412-462` keeps the pre-redaction link and classifies `redacted_gdpr`). Migration 0165 grants `UPDATE` only on `(payload, payload_canonical_json)`, so `prev_hash` is immutable to every database role and the spec's re-seal is grant-forbidden. §11.7 (`spec/11_policy-and-controls.md:369`) already matches the receipt model. The divergent re-seal passages are §12.8:813 and :822, the §18 build deliverable (`spec/18_build-sequence.md:549`), the missing-receipt-condition wording in the §16.5 alert rows (`spec/16_observability.md:471-472`), the §25 enumeration entry (`spec/25_agent-operability.md:3664`), and the metrics reference (`docs/reference/metrics.md:367`). The same "chain rewrite boundary" model also survives in the single-source §16.5 alert catalog `pkg/alerting/rules/rules.go:621` (the `AuditRedactionReceiptMissing` `Description`, rendered verbatim into the Helm chart's PrometheusRule CRD and the in-gateway alert tracker) and in the external-verifier comment `pkg/audit/integrity/redaction.go:38-39`; both are corrected in lockstep so the shipped alert and the reconciled spec agree.

### 1.3 Step-13 unimplementable mid-chain DELETE

§12.8 step-13 (`spec/12_storage-architecture.md:812`) directs a literal user-scoped mid-chain `DELETE` of the user's non-`gdpr`, non-dead-lettered audit rows. §11.7 states that any retroactive deletion breaks the chain for all subsequent entries (`spec/11_policy-and-controls.md:369`), and the `chainIntegrity` enumeration (`verified | broken | unchecked | rechained_post_outage | gap_suspected | redacted_gdpr`) has no authorized-deletion state. `audit_log` also has no `user_id` or `session_id` column to resolve "the user's sessions." `DeleteByUser` is therefore a deliberate `(0, nil)` no-op (`auditstore.go:441-460`) and is not registered as a user-scoped eraser in the §12.8 orchestrator (`adminrouter.go:462-562`, `506-562`), so ordinary audit rows are retained today with no spec basis stated for that behavior.

### 1.4 `DeleteByTenant` versus the `gdpr.*` skip

`spec/12_storage-architecture.md:840` requires "`DeleteByTenant` (Phase 4 of tenant deletion) similarly skips `gdpr.*` rows", `spec/12_storage-architecture.md:831` exempts `audit_redaction_receipts`, and the retention note states receipts outlive "any subsequent tenant deletion" (`spec/12_storage-architecture.md:842`). The Phase-4 actions (`spec/12_storage-architecture.md:876`) confirm the skip. The shipped `DeleteByTenant` runs `DELETE FROM audit_log WHERE tenant_id = $1` with no `gdpr.%` exclusion (`auditstore.go:488`), deleting the compliance receipts. This is the code-versus-spec divergence F-12.8.4 names in its "gdpr.* exemption" clause.

Removing PII from ordinary audit rows is forced only if those rows must be scrubbed. The confirmed evidence is that the raw canonical PII lives only in dead-lettered rows (`spec/11_policy-and-controls.md:423`, "a `dead_lettered` row holds the untranslated raw canonical payload"; `spec/12_storage-architecture.md:813`), while ordinary translated rows carry structured actor and subject identifiers already covered by the existing `gdpr.*`-style retention basis. §11.7 makes user-scoped deletion of those rows impossible, so the reconciliation states the platform's actual chain-safe behavior and its basis rather than asserting an erasure the ledger cannot perform.

## 2. Decisions

- **Close both findings as one proposal.** F-12.8.4 (the §12.8 erasure-sequence view) and F-12.2.5 (the §12.2 store-catalog view) are one residual; their DEFERRED bodies already agree the blocker is identical.
- **This is a reconciliation rather than a new build.** The §12.1 interface, `DeleteByTenant`, the `gdpr.%` retention carve-out, the step-14 dead-letter redaction, the `audit_redaction_receipts` table, the KMS-signed receipt, the chain verifier's `redacted_gdpr` classification, and the orchestrator wiring already ship and are chain-consistent. The proposal rewrites the divergent spec passages to match, plus the `DeleteByTenant` `gdpr.*` code fix, the continuity-verifier reconciliation (resolving the tenant deletion-state skip-set from the control-plane pool so it holds under the §12.3 split billing/audit-pool topology), and the single-source alert-catalog `Description` correction with its `make generate` re-render.
- **§11.7 tamper-evidence is authoritative and constrains every option.** Any retroactive mid-chain deletion breaks the chain for all subsequent rows, and the only authorized chain discontinuity the spec defines is the step-14 in-place receipted redaction (`redacted_gdpr`). No new `chainIntegrity` state is invented; the reconciliation lives inside the one discontinuity the spec already sanctions.
- **`audit_log` has no `user_id` or `session_id` column and will not gain one.** Adding a column would force a §11.7 chain recompute and violate append-only immutability. The shipped payload-key / OCSF-projection matching (`userMatchKeys`, `redaction.go:26-29`) is the intended row-scoping mechanism for redaction; the spec's `user_id`-column references are phantom and are rewritten to payload-key matching (redaction scan) and to the single payload actor key (Article 20 export).
- **The shipped redaction is a receipt-authorized content-hash discontinuity rather than a physical re-seal.** `RedactDeadLettered` rewrites only `payload` and `payload_canonical_json`, leaves `prev_hash` and every other hash-input column untouched, and the signed `RedactionReceipt`'s pre-redaction `original_hash` keeps the link verifiable (`redaction.go:112-167`, `chain.go:412-462`). Migration 0165's column-scoped `UPDATE` grant makes `prev_hash` immutable to every role, so the spec's "rewrite the subsequent row's `prev_hash`" language is both unimplemented and grant-forbidden. §12.8:813/822 are rewritten to the receipt model; §11.7:369 already matches.
- **The audit ledger participates in user erasure via the step-14 redaction; ordinary rows are retained under a stated basis.** The ledger participates via the step-14 in-place receipted redaction of dead-lettered rows (which uniquely embed the untranslated raw canonical payload with unbounded free-text PII). Ordinary audit rows are retained under the ledger's tamper-evidence and legal-obligation basis, mirroring the existing `gdpr.*` exemption, and the retained structured actor/subject identifier is surfaced in the data subject's Article 20 export. `DeleteByUser`'s `(0, nil)` no-op becomes spec-sanctioned. Whether a stricter per-row identifier redaction (direction a) is required is a reviewer compliance decision (Open decisions).
- **`DeleteByTenant` must honor the §12.8 `gdpr.*` skip, and the verifier must accept the remnant.** The recommended reconciliation is the code fix (exclude `event_type LIKE 'gdpr.%'` from the teardown `DELETE`) plus a spec note that the retained `gdpr.*` remnant is a standalone compliance-receipt set exempt from chain verification. The Phase-4 audit `DELETE` runs while the tenant is `state='deleting'`, and the tombstone `state='deleted'` is written only at Phase 6, so the retained `gdpr.*`-only remnant carries a discontinuous chain from Phase 4 onward. The chain-continuity verifier must therefore skip any tenant in `state='deleting'` or `state='deleted'` so the teardown, including a deletion that stalls mid-way, does not raise a false `AuditChainGap` alert. The verifier resolves that deletion skip-set from the control-plane Postgres where `tenants.state` is authoritative, because under the §12.3 Tier-3 split billing/audit-pool topology the verifier reads audit_log from the separate ledger instance while tenants state stays on the primary; a `tenants` join on the ledger connection would read an unpopulated table and exclude nothing. The exclusion predicate stays `state IN ('deleting', 'deleted')` in both topologies.
- **No new protocol surface, store method, or migration.** The reconciliation reuses `userMatchKeys`, `DeadLetteredForUser`, `RedactDeadLettered`, `RedactionReceipt`, `chain.go` classification, `PruneRetention`, and migrations 0160/0165. The code changes are the `DeleteByTenant` `gdpr.%` exclusion, the continuity-verifier exclusion of tenants in `state='deleting'` or `state='deleted'` (resolving that skip-set from the control-plane pool, which adds an internal `Querier` parameter to the continuity functions and a field to `PeriodicCheck`, no wire surface), the alert-catalog `Description` correction with its `make generate` re-render, and doc-comment updates on `DeleteByUser`/`DeleteByTenant`.

## 3. Design constraints and what already ships

The §11.7 per-tenant hash chain is the envelope every option lives inside. `classifyRow` (`pkg/audit/chain.go:412-462`) recomputes each row's content hash and its link to the predecessor. A row that fails the content-hash recomputation is reported `broken` unless it is redaction-marked and a signature-verifying `RedactionReceipt` reclassifies the break as `redacted_gdpr`. A row whose predecessor was lawfully redacted is tolerated because the predecessor's `prev_hash` was never rewritten. There is no branch for a deleted row: a mid-chain deletion produces a sequence gap and a `prev_hash` that references a hash no surviving row reproduces, which the verifier reports `broken` or `gap_suspected`.

The shipped redaction path already fits this envelope. `RedactDeadLettered` updates only the two payload columns and inserts the receipt in the same transaction (`redaction.go:143-166`). `Receipts` loads the per-tenant receipts (`redaction.go:177-215`), and `applyReceipts` restores each redacted row's preserved pre-redaction hash so the chain stays linked (`redaction.go:217-235`). The verifier consults the receipts to accept the content-hash break (`chain.go:421-433,459-462`).

The reconciliation stays inside this envelope. It removes the phantom column from the redaction scope and the Article 20 export (C1), restates the step-14 re-seal as the receipt-authorized discontinuity the code already performs (C2), replaces the step-13 mid-chain `DELETE` with the retention basis the ledger already exhibits (C3), aligns `DeleteByTenant` and the continuity verifier with the `gdpr.*` skip the spec already mandates (C4), and pins the newly-introduced behavior with tests (C5).

## 4. Proposed changes

### C1. Remove the phantom `audit_log.user_id` column from §12.8 step-14 and the Article 20 DSAR export

**Target:** `spec/12_storage-architecture.md`, §12.8 step 14 (line 813) and §12.8 "Audit event scope for portable exports (normative)" (line 962 prose and the template query at line 975). Reference (already aligned, no change): `pkg/gateway/audit/auditstore/redaction.go:26-45,57-91`; `pkg/audit/ocsf/ocsf.go:518-519`.

The step-14 redaction scan matches rows wherever the user appears (as actor or subject), so it keeps the broad `userMatchKeys` allow-list. The Article 20 category (a) is deliberately actor-only, so its predicate is the single actor projection `payload->>'user_id'` (equal to `actor.user.uid`). These are two different concerns with two correct predicates; the redaction allow-list is not imported into the export query.

**Anchor and change (step-14 read side, line 813):** Replace

```markdown
For every `audit_log` row whose `ocsf_translation_state = 'dead_lettered'` and whose `user_id` column equals the target user (equivalently, whose `payload` carries the target user as actor or subject — see [§11.7](11_policy-and-controls.md#117-audit-logging) Lenny → OCSF field mapping), the erasure job redacts the PII-bearing payload fields in place:
```

with

```markdown
For every `audit_log` row whose `ocsf_translation_state = 'dead_lettered'` and whose `payload` names the target user in any actor or subject position (the erasure job matches on the top-level payload keys `user_id`, `userId`, `sub`, `actor`, `actor_sub`, `subject`, `subject_user_id`, `target_user_id`, and `targetUserId`, per the [§11.7](11_policy-and-controls.md#117-audit-logging) Lenny → OCSF field mapping — the `audit_log` table has no `user_id` column, so row scoping is by payload key), the erasure job redacts the PII-bearing payload fields in place:
```

**Anchor and change (step-14 write side, line 813):** Replace

```markdown
the `payload_canonical_json` generated column is recomputed from the redacted payload; the `user_id` column is set to `NULL`; any derived columns that mirror user-identifying fields are set to `NULL`.
```

with

```markdown
the `payload_canonical_json` generated column is recomputed from the redacted payload. The `payload` rewrite (and the recomputed `payload_canonical_json`) removes every user-identifying field the payload carried; the `audit_log` table has no `user_id` or other user-identifying column to null out separately.
```

**Anchor and change (Article 20 prose, line 962):** Replace

```markdown
In the Lenny schema, this is every row whose `user_id` column equals the target user (equivalently, `actor.user.uid == target_user_id` in the OCSF projection, [§11.7](11_policy-and-controls.md#117-audit-logging) Lenny → OCSF field mapping). These rows are written under the target user's tenant.
```

with

```markdown
In the Lenny schema, `audit_log` has no `user_id` column; the acting identity is carried in the payload's `user_id` key, from which the OCSF projection derives `actor.user.uid` ([§11.7](11_policy-and-controls.md#117-audit-logging) Lenny → OCSF field mapping). An actor-scoped row is therefore one whose `payload->>'user_id'` equals the target user (`actor.user.uid == target_user_id`). These rows are written under the target user's tenant.
```

**Anchor and change (Article 20 template query, line 975):** Replace

```sql
WHERE tenant_id = $2 AND user_id = $1                              -- (a) actor-scoped
```

with

```sql
WHERE tenant_id = $2 AND payload->>'user_id' = $1                  -- (a) actor-scoped
```

**Rationale:** §12.2 line 16 and `migrations/0001_initial_schema.up.sql:130-155` define no `user_id` column; step-14 reads and writes it and the Article 20 export reads it. The shipped redaction scan matches by payload key (`userMatchKeys`) with the in-code note "the `audit_log` table has no `user_id` column" (`redaction.go:22-29`), and the OCSF projector derives `actor.user.uid` from the single payload key `user_id` (`ocsf.go:518-519`). The spec is the defect. Category (a) stays actor-only so the normative Article 20 scope is not silently broadened from actor to actor-plus-subject; category (b) continues to handle subject-scoped platform-tenant impersonation rows unchanged.

### C2. Restate §12.8 step-14 chain re-sealing as the shipped receipt-authorized discontinuity

**Target:** `spec/12_storage-architecture.md`, §12.8 step 14 (line 813, the "re-sealed across the rewrite" clause) and the `RedactionReceipt.new_hash` field (line 822); `spec/18_build-sequence.md:549` (the §11.7 build deliverable that restates the re-seal); `spec/16_observability.md:471-472` (the `AuditChainGap` and `AuditRedactionReceiptMissing` alert rows); `spec/25_agent-operability.md:3664` (the `redacted_gdpr` enumeration entry); `docs/reference/metrics.md:367` (the `lenny_audit_redaction_receipt_missing_total` row); the single-source §16.5 alert catalog `pkg/alerting/rules/rules.go:621` (the `AuditRedactionReceiptMissing` `Description`, which the Helm chart and the in-gateway alert tracker both consume); and the external-verifier comment `pkg/audit/integrity/redaction.go:38-39`. The generated renders of the alert catalog (`charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, and `pkg/embedded/manifests/manifests.yaml`) are regenerated by `make generate` from the catalog source and are not hand-edited. Reference (already aligned, no change): `pkg/audit/chain.go:412-462`; `migrations/0165_audit_log_redaction_grant.up.sql`; the `AuditChainGap` catalog entry (`rules.go:885-889`), whose `Description` already states the receipt model and carries no "chain rewrite boundary" language.

**Anchor and change (step-14 re-seal clause, line 813):** Replace

```markdown
Because the redaction is a rewrite of `payload` (not a row delete), the pre-commit hash chain must be re-sealed: the erasure job recomputes the per-row hash using the redacted canonical tuple, writes the new value back to the row's `prev_hash`-equivalent position and to the immediately subsequent row's `prev_hash` input so the chain is re-sealed across the rewrite, sets the row's `chainIntegrity` verifier state to `redacted_gdpr` (see [§11.7](11_policy-and-controls.md#117-audit-logging) `chainIntegrity` enumeration and the `lenny_audit_chain_integrity_total{state="redacted_gdpr"}` counter in [§16.1](16_observability.md#161-metrics) Audit Integrity), persists a signed **`RedactionReceipt`** (schema defined immediately below) pinning the discontinuity to a specific erasure job and legal basis, and records a `gdpr.erasure_deadletter_redacted` audit event per redacted row carrying
```

with

```markdown
Because the redaction rewrites `payload` and `payload_canonical_json` (both content-hash inputs, so the row's recomputed content hash changes) and leaves `prev_hash` and the row's identity and position columns (`id`, `tenant_id`, `sequence_number`, `event_type`, `event_schema_version`, `created_at`) untouched, the chain link stays intact: each successor row's `prev_hash` remains the redacted row's preserved pre-redaction hash, and no hash is written into the redacted row or into any subsequent row's `prev_hash`. The `prev_hash` and identity columns remain immutable to every database role, because the `lenny_erasure` `UPDATE` grant covers only the two payload columns. The verifier sets the row's `chainIntegrity` state to `redacted_gdpr` (see [§11.7](11_policy-and-controls.md#117-audit-logging) `chainIntegrity` enumeration and the `lenny_audit_chain_integrity_total{state="redacted_gdpr"}` counter in [§16.1](16_observability.md#161-metrics) Audit Integrity). The erasure job persists a signed **`RedactionReceipt`** (schema defined immediately below), whose pre-redaction `original_hash` authorizes the content-hash discontinuity the rewrite introduces and pins it to a specific erasure job and legal basis, and records a `gdpr.erasure_deadletter_redacted` audit event per redacted row carrying
```

**Anchor and change (`RedactionReceipt.new_hash` field, line 822):** Replace

```markdown
    - `new_hash` (bytea, 32 bytes) — SHA-256 of the canonical tuple as hashed **after** redaction (the post-redaction value used to re-seal the chain at the rewrite boundary and written into the subsequent row's `prev_hash`).
```

with

```markdown
    - `new_hash` (bytea, 32 bytes) — SHA-256 of the canonical tuple as hashed **after** redaction. It is recorded for provenance of the rewrite; it is never written into the redacted row or into any subsequent row's `prev_hash`, and it is not consulted for link continuity (the verifier reconciles the discontinuity through `original_hash`).
```

The same re-seal model is restated in the surfaces below outside §12.8, which must be corrected in lockstep so the applied spec does not describe a "chain rewrite boundary" that step 14 says does not exist.

**Anchor and change (§18 build deliverable, `spec/18_build-sequence.md:549`):** Replace

```markdown
- Hash-chain verifier and the OCSF translator binary, with the dead-letter PII redaction path that re-seals the chain.
```

with

```markdown
- Hash-chain verifier and the OCSF translator binary, with the dead-letter PII redaction path that produces the receipt-authorized `redacted_gdpr` chain discontinuity (the redaction rewrites only the payload columns, which feed the content hash, and leaves `prev_hash` and the row's identity columns untouched; the signed `RedactionReceipt` authorizes the resulting content-hash break, so no hash is re-sealed into any row's `prev_hash`).
```

**Anchor and change (`AuditChainGap` alert row, `spec/16_observability.md:471`):** Replace

```markdown
Rows classified `redacted_gdpr` whose receipt is missing, fails signature verification, or mismatches the row's pre/post hash pair are surfaced by the separate `AuditRedactionReceiptMissing` alert (critical) rather than here.
```

with

```markdown
Rows classified `redacted_gdpr` whose receipt is missing, fails signature verification, or whose preserved pre-redaction row hash does not match the receipt's `original_hash` are surfaced by the separate `AuditRedactionReceiptMissing` alert (critical) rather than here.
```

**Anchor and change (`AuditRedactionReceiptMissing` alert row, `spec/16_observability.md:472`):** Replace

```markdown
the corresponding signed `RedactionReceipt` in `audit_redaction_receipts` is absent, signature-invalid, or its `(original_hash, new_hash)` pair does not match the observed chain rewrite boundary.
```

with

```markdown
the corresponding signed `RedactionReceipt` in `audit_redaction_receipts` is absent, signature-invalid, or its `original_hash` does not match the redacted row's preserved pre-redaction hash.
```

**Anchor and change (`redacted_gdpr` enumeration entry, `spec/25_agent-operability.md:3664`):** Replace

```markdown
External verifiers accept `redacted_gdpr` as a valid discontinuity only after verifying the receipt's signature and matching its `(original_hash, new_hash)` pair to the observed chain rewrite boundary; otherwise they MUST classify the row as `broken` and fire the `AuditRedactionReceiptMissing` alert
```

with

```markdown
External verifiers accept `redacted_gdpr` as a valid discontinuity only after verifying the receipt's signature and matching its `original_hash` to the redacted row's preserved pre-redaction hash; otherwise they MUST classify the row as `broken` and fire the `AuditRedactionReceiptMissing` alert
```

**Anchor and change (`lenny_audit_redaction_receipt_missing_total` metrics row, `docs/reference/metrics.md:367`):** Replace

```markdown
Rows classified `chainIntegrity=redacted_gdpr` where the corresponding signed `RedactionReceipt` is absent, signature-invalid, or the `(original_hash, new_hash)` pair does not match the observed chain rewrite.
```

with

```markdown
Rows classified `chainIntegrity=redacted_gdpr` where the corresponding signed `RedactionReceipt` is absent, signature-invalid, or its `original_hash` does not match the redacted row's preserved pre-redaction hash.
```

The `docs/reference/metrics.md` row and the §16.5 spec alert rows are two views of one alert. The §16.5 catalog is single-sourced in the Go alert catalog `pkg/alerting/rules/rules.go`, whose header states "Spec §16.5 enumerates the catalog of alerts; each entry here corresponds to one row of those tables" and "The catalog is the single source consumed by two surfaces: the gateway compiles it into an in-process alert tracker (§25.13), and the Helm chart renders it into a PrometheusRule CRD" (`rules.go:3-20`). `make generate` renders that source verbatim into `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, and `pkg/embedded/manifests/manifests.yaml`, all headed "DO NOT EDIT. Run `make generate` to refresh." The catalog `AuditRedactionReceiptMissing` `Description` still carries the deleted "chain rewrite boundary" model, so it must be rewritten at the source and the artifacts regenerated; hand-editing the generated YAMLs would fail the tier-2 catalog-versus-chart cross-check (`tests/tier2_component/observability/catalog_crosscheck_test.go`, `TestPrometheusRuleMatchesAlertCatalog`).

**Anchor and change (alert catalog `AuditRedactionReceiptMissing` `Description`, `pkg/alerting/rules/rules.go:621`):** Replace

```go
			Description: "A row is classified chainIntegrity=redacted_gdpr by the verifier but the corresponding signed RedactionReceipt is absent, signature-invalid, or mismatches the chain rewrite boundary. Distinguishes an orphaned GDPR redaction from a genuine tamper.",
```

with

```go
			Description: "A row is classified chainIntegrity=redacted_gdpr by the verifier but the corresponding signed RedactionReceipt is absent, signature-invalid, or its original_hash does not match the redacted row's preserved pre-redaction hash. Distinguishes an orphaned GDPR redaction from a genuine tamper.",
```

Then run `make generate` to regenerate `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, and `pkg/embedded/manifests/manifests.yaml` from the edited catalog; the three generated files are not edited by hand.

**Anchor and change (external-verifier comment, `pkg/audit/integrity/redaction.go:38-39`):** Replace

```go
// A receipt counts as missing when the row exists but has no receipt, or
// the receipt carries no signature (the absent / signature-invalid cases
// the §16.5 alert enumerates). The full KMS signature verification and the
// (original_hash, new_hash) boundary match are performed by external
// verifiers that hold the audit public key; this in-process detector
```

with

```go
// A receipt counts as missing when the row exists but has no receipt, or
// the receipt carries no signature (the absent / signature-invalid cases
// the §16.5 alert enumerates). The full KMS signature verification and the
// match of the receipt's original_hash against the redacted row's
// preserved pre-redaction hash are performed by external verifiers that
// hold the audit public key; this in-process detector
```

**Rationale:** Step-14 and `new_hash` require recomputing the row hash and writing it into the row's own position and the subsequent row's `prev_hash`. The shipped mechanism rewrites the two payload columns, leaves `prev_hash` and the row's identity columns untouched, and reconciles the resulting content-hash break with the receipt's pre-redaction `original_hash` (`redaction.go:112-167`; `chain.go:412-462`). Migration 0165 grants `UPDATE` only on `(payload, payload_canonical_json)`, so `prev_hash` is immutable to every role and the spec's re-seal is grant-forbidden. §11.7:369 already describes the receipt model. The §18 build deliverable, the §16.5 `AuditChainGap` and `AuditRedactionReceiptMissing` alert rows, the §25 enumeration entry, and the `docs/reference/metrics.md` row all restate the deleted "chain rewrite boundary" / `new_hash`-into-`prev_hash` model and are corrected to the `original_hash`-vs-preserved-row-hash and signature check the shipped verifier actually performs (`chain.go:classifyRow` consults only `original_hash`; `Receipts()` does not load `new_hash`). Because the §16.5 `AuditRedactionReceiptMissing` row is single-sourced in the Go alert catalog (`rules.go:621`) that the Helm chart and the in-gateway alert tracker both consume, the catalog `Description` is rewritten at the source and the generated renders (`charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, `pkg/embedded/manifests/manifests.yaml`) are refreshed by `make generate`; leaving the catalog stale would keep the deployed PrometheusRule CRD describing the removed boundary and would regenerate the stale string on the next `make generate`. The external-verifier comment `redaction.go:38-39` mirrors the §25 external-verifier description and is corrected in the same lockstep. C2 therefore carries these code and generated-artifact edits in addition to the spec and docs edits.

### C3. Reconcile §12.8 step-13 and the scope-table row to the chain-safe reality

**Target:** `spec/12_storage-architecture.md`, §12.8 step 13 (line 812), the erasure-scope table row for `EventStore` (audit) (line 778), and the `gdpr.*` exemption paragraph (line 840). And `pkg/gateway/audit/auditstore/auditstore.go:441-460` (the `DeleteByUser` doc comment). Reference (cross-reference, no change): `spec/11_policy-and-controls.md:369`.

The raw canonical PII lives only in dead-lettered rows (`spec/11_policy-and-controls.md:423`); ordinary translated rows carry structured actor/subject identifiers that the existing `gdpr.*`-style retention basis already covers. The reconciliation states the retained-row basis in one sentence folded into the existing exemption paragraph rather than adding a standalone legal-basis paragraph, and it does not stage prose committing to a specific GDPR legal-obligation position for the stricter per-row redaction (that stays the reviewer's open decision).

**Anchor and change (step 13, line 812):** Replace

```markdown
13. `EventStore` (audit) — delete audit events for the user's sessions, **excluding** rows where `event_type LIKE 'gdpr.%'` (see exemption below) and rows where `ocsf_translation_state = 'dead_lettered'` (see OCSF dead-letter redaction below — these rows are preserved for forwarder-failure forensics but PII-redacted in the next step).
```

with

```markdown
13. `EventStore` (audit) — the audit `EventStore` performs no user-scoped row deletion. The `audit_log` table is an append-only [§11.7](11_policy-and-controls.md#117-audit-logging) per-tenant hash chain: any retroactive mid-chain deletion breaks the chain for every subsequent row, and the `chainIntegrity` enumeration defines no authorized-deletion state (only the in-place `redacted_gdpr` discontinuity of step 14). The table is keyed by `(tenant_id, sequence_number)` and carries no `user_id` or `session_id` column on which to scope the user's rows. The ledger therefore participates in user erasure solely through the step-14 dead-letter redaction below; ordinary translated rows retain their structured actor and subject identifiers under the §11.7 tamper-evidence and legal-obligation basis and age out under `audit.retentionDays` (the [§16.4](16_observability.md#164-logging) retention sweep), while dead-lettered rows are redacted in place in step 14. `DeleteByUser` on this store is a spec-sanctioned no-op: it rejects an empty scope and otherwise returns without deleting.
```

**Anchor and change (erasure-scope table row, line 778):** Replace

```markdown
| `EventStore` (audit)   | Postgres                | Audit events for the user's sessions. **`gdpr.*` event types (erasure receipts) are exempt from `DeleteByUser` and are retained for the full audit period (see note below).** Rows with `ocsf_translation_state = 'dead_lettered'` are **not deleted**; their payload is PII-redacted in the dedicated OCSF dead-letter redaction step of the deletion sequence so forwarder-failure forensics remain usable while the user's raw canonical PII is scrubbed (see OCSF dead-letter PII redaction step below). |
```

with

```markdown
| `EventStore` (audit)   | Postgres                | No user-scoped row deletion. The `audit_log` chain is append-only with no authorized mid-chain deletion state and no `user_id`/`session_id` column, so `DeleteByUser` deletes nothing (see step 13). Ordinary rows retain their structured actor/subject identifiers under the [§11.7](11_policy-and-controls.md#117-audit-logging) tamper-evidence and legal-obligation basis and age out under `audit.retentionDays`. Rows with `ocsf_translation_state = 'dead_lettered'` embed the untranslated raw canonical payload (unbounded free-text PII); their payload is redacted in place in the step-14 OCSF dead-letter redaction so forwarder-failure forensics stay usable while the raw PII is scrubbed. **`gdpr.*` event types (erasure receipts) are exempt and retained for the full audit period (see note below).** |
```

**Anchor and change (`gdpr.*` exemption paragraph `DeleteByUser` clause, line 840):** Replace

```markdown
`DeleteByUser` MUST filter out rows where `event_type LIKE 'gdpr.%'` — these rows are never deleted by user-level erasure jobs, regardless of which `user_id` is being erased.
```

with

```markdown
`DeleteByUser` performs no user-scoped audit deletion (see step 13), so `gdpr.*` erasure receipts are retained along with every other audit row through user-level erasure, regardless of which user is being erased.
```

**Anchor and change (`gdpr.*` exemption paragraph, line 840):** Append the following sentence to the paragraph, after `` `DeleteByTenant` (Phase 4 of tenant deletion) similarly skips `gdpr.*` rows. ``

```markdown
Ordinary (non-`gdpr.*`, non-dead-lettered) audit rows are likewise retained through user-level erasure: the §11.7 append-only chain admits no user-scoped deletion, and the structured actor and subject identifiers those rows carry are held under the ledger's tamper-evidence and legal-obligation basis and surfaced in the data subject's Article 20 export (see **Audit event scope for portable exports (normative)** above, category (a)) rather than deleted.
```

**Anchor and change (`DeleteByUser` doc comment, `auditstore.go:441-460`):** Replace

```go
// DeleteByUser satisfies the §12.1 mandatory-erasure primitive. The
// audit ledger is deliberately retained on user erasure: gdpr.* rows
// (erasure receipts) are exempt and kept for the full audit period,
// dead-lettered rows are PII-redacted in place rather than deleted,
// and the audit_log table is keyed only by (tenant_id,
// sequence_number) with no user_id column, so there is nothing this
// store can delete keyed by user without breaking the §11.7 hash
// chain. The substantive §12.8 step-13 selective deletion and step-14
// OCSF dead-letter redaction are tracked under F-12.2.5; this method
// satisfies the §12.1 compile-time contract and returns (0, nil) after
// rejecting empty arguments (§12.8 line 753).
//
// spec: §12.1 line 5 (mandatory primitive); §12.8 line 775 (audit
// retention carve-out for gdpr.* and dead-lettered rows).
```

with

```go
// DeleteByUser satisfies the §12.1 mandatory-erasure primitive. The
// audit ledger performs no user-scoped row deletion, which §12.8
// step 13 sanctions as a no-op: audit_log is an append-only §11.7
// per-tenant hash chain with no authorized mid-chain deletion state,
// and it is keyed only by (tenant_id, sequence_number) with no user_id
// or session_id column, so there is nothing this store can delete keyed
// by user without breaking the chain. gdpr.* rows (erasure receipts)
// are exempt and kept for the full audit period, dead-lettered rows are
// PII-redacted in place by the §12.8 step-14 redaction service rather
// than deleted, and ordinary rows retain their structured actor/subject
// identifiers under the §11.7 tamper-evidence basis and age out under
// audit.retentionDays. This method returns (0, nil) after rejecting an
// empty scope (§12.8 line 753).
//
// spec: §12.1 line 5 (mandatory primitive); §12.8 step 13 (no
// user-scoped audit deletion); §12.8 line 775 (audit retention
// carve-out for gdpr.* and dead-lettered rows).
```

**Rationale:** Step-13's literal user-scoped `DELETE` cannot be implemented: §11.7:369 breaks the chain on any retroactive deletion and defines no authorized-deletion `chainIntegrity` state, and `audit_log` has no `user_id`/`session_id` column to scope the user's sessions. The shipped system already implements the only chain-safe behavior; the spec now states it and its basis. The scope-table row is rewritten in lockstep with step 13 so the table and prose agree. The exemption paragraph's `DeleteByUser MUST filter out rows where event_type LIKE 'gdpr.%'` clause is reworded to the no-deletion model in the same lockstep, because that clause presupposes `DeleteByUser` deletes some (non-`gdpr.*`) audit rows, the exact mechanism step 13 eliminates. The retention basis is folded into the existing exemption paragraph rather than duplicated. The general audit payload is not classified PII-bearing: the raw canonical PII is unique to dead-lettered rows (`spec/11_policy-and-controls.md:423`), and ordinary rows carry structured identifiers the exemption basis covers.

### C4. Align `DeleteByTenant` and the chain-continuity verifier with the §12.8 `gdpr.*` skip

**Target:** `pkg/gateway/audit/auditstore/auditstore.go:462-499` (`DeleteByTenant` doc comment and DELETE), `pkg/audit/integrity/continuity.go:67-125,183-204` (`CheckChainContinuity`, `CheckChainContinuityRecent`, and `auditTenants`), `pkg/audit/integrity/periodic.go` (`PeriodicCheck` control-plane field), `cmd/lenny-gateway/main.go:1084-1088` (`runStartupChainContinuityCheck`), `cmd/lenny-gateway/metricsbackfill.go:381-385` and `cmd/lenny-gateway/runserver.go:120-121` (control-plane pool wiring), and `spec/12_storage-architecture.md:840` (the `gdpr.*` exemption paragraph, spec note on the post-teardown remnant).

**Anchor and change (`DeleteByTenant` doc comment, `auditstore.go:462-464`):** Replace

```go
// DeleteByTenant satisfies the §12.1 mandatory-erasure primitive. It
// removes the tenant's entire audit chain for the §12.8 Phase-4 tenant
// teardown and returns the count deleted.
```

with

```go
// DeleteByTenant satisfies the §12.1 mandatory-erasure primitive. It
// deletes every non-gdpr.% row of the tenant's audit chain for the
// §12.8 Phase-4 tenant teardown and retains the gdpr.* erasure-receipt
// remnant that must outlive the tenant (§12.8 line 840); the returned
// count is the rows deleted and excludes the retained receipts.
```

**Anchor and change (`DeleteByTenant` DELETE, `auditstore.go:488`):** Replace

```go
		tag, err := tx.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id = $1`, tenantID)
```

with

```go
		// §12.8 line 840 / Phase 4: skip gdpr.* erasure-receipt rows so
		// the tenant teardown retains the compliance receipts that must
		// outlive the tenant (audit_redaction_receipts are separately
		// exempt, §12.8 line 831). The retained gdpr.*-only remnant is a
		// standalone compliance set exempt from §11.7 chain verification;
		// the continuity check skips any tenant in state='deleting' or
		// state='deleted' (the states the tenant carries from this Phase-4
		// delete through the Phase-6 tombstone) so the teardown raises no
		// false AuditChainGap alert. Reuses the PruneRetention gdpr.%
		// predicate.
		tag, err := tx.Exec(ctx,
			`DELETE FROM audit_log WHERE tenant_id = $1 AND event_type NOT LIKE 'gdpr.%'`,
			tenantID)
```

**Anchor and change (`auditTenants`, `continuity.go:185`):** The audit `EventStore` enumeration and the tenants `state` column can live on different Postgres instances: under the §12.3 Tier-3 topology an operator configures a separate billing/audit instance (`LENNY_PG_BILLING_AUDIT_DSN`), and audit_log inserts (and the Phase-4 `DeleteByTenant`) route to it while every other write, including the authoritative tenants state, stays on the primary (`spec/12_storage-architecture.md:103`; `cmd/lenny-gateway/flags.go:415`). The startup continuity check reads audit_log from whichever instance the ledger lives on (`w.billingAuditPool` when configured, `cmd/lenny-gateway/metricsbackfill.go:381-385`), so a `tenants` join issued on that connection reads a `tenants` table that is never populated with deletion state (empty, or absent), and the exclusion silently fails: the retained `gdpr.*`-only remnant is not skipped and the false `AuditChainGap` still fires. The reconciliation therefore reads the deletion skip-set from the control-plane pool (where `tenants.state` is authoritative) and filters the audit-instance enumeration against it, keeping the single canonical state-based exclusion (`state IN ('deleting', 'deleted')`) rather than a per-topology variant. Replace

```go
func auditTenants(ctx context.Context, db Querier) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT tenant_id FROM audit_log`)
	if err != nil {
		return nil, fmt.Errorf("integrity: query audit tenants: %w", err)
	}
```

with

```go
// auditTenants returns the distinct tenant ids with at least one
// audit_log row, sorted, excluding any tenant undergoing or past §12.8
// deletion. auditDB is the ledger instance (the separate §12.3
// billing/audit Postgres when configured, otherwise the primary);
// ctrlDB is the control-plane Postgres where the tenants.state column
// is authoritative. §12.3 routes audit_log to the separate instance
// while tenants state stays on the primary, so the deletion skip-set
// MUST be read from ctrlDB. A join on the ledger connection would read
// an unpopulated tenants table and exclude nothing. When no separate
// instance is configured auditDB and ctrlDB are the same pool.
func auditTenants(ctx context.Context, auditDB, ctrlDB Querier) ([]string, error) {
	// Skip-set: tenants in state='deleting' (Phases 4, 4a, and 5 per
	// stateForPhase, lifecycle.go) or state='deleted' (the Phase-6
	// tombstone). After Phase-4 DeleteByTenant skips the gdpr.* rows
	// (§12.8 line 840) such a tenant carries a gdpr.*-only remnant whose
	// chain is deliberately discontinuous; verifying it would report
	// ChainBroken and fire a false §16.5 AuditChainGap alert. Excluding
	// both states covers the whole Phase-4-through-Phase-6 teardown
	// window and a deletion that stalls mid-teardown. A tenant_id with no
	// tenants row (the 'platform' pseudo-tenant chain,
	// 0001_initial_schema.up.sql:131) is never in the skip-set, so live
	// chains are still walked.
	skip, err := tenantsInDeletion(ctx, ctrlDB)
	if err != nil {
		return nil, err
	}
	rows, err := auditDB.Query(ctx, `SELECT DISTINCT tenant_id FROM audit_log`)
	if err != nil {
		return nil, fmt.Errorf("integrity: query audit tenants: %w", err)
	}
```

and filter each scanned `tenant_id` against `skip` before appending it (`if _, deleting := skip[t]; deleting { continue }`). Add the helper that reads the skip-set from the control-plane pool:

```go
// tenantsInDeletion reads the §12.8 deletion skip-set from the
// control-plane pool: tenants in state='deleting' or state='deleted'
// (migration 0105). Their audit chains carry a deliberately
// discontinuous gdpr.*-only remnant after Phase-4 DeleteByTenant and are
// excluded from continuity verification so the teardown raises no false
// §16.5 AuditChainGap alert.
func tenantsInDeletion(ctx context.Context, ctrlDB Querier) (map[string]struct{}, error) {
	rows, err := ctrlDB.Query(ctx, `SELECT id FROM tenants WHERE state IN ('deleting', 'deleted')`)
	if err != nil {
		return nil, fmt.Errorf("integrity: query tenants in deletion: %w", err)
	}
	defer rows.Close()
	skip := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("integrity: scan tenant in deletion: %w", err)
		}
		skip[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("integrity: iterate tenants in deletion: %w", err)
	}
	return skip, nil
}
```

**Anchor and change (continuity-check signatures and wiring):** `CheckChainContinuity` (`continuity.go:67`) and `CheckChainContinuityRecent` (`continuity.go:102`) gain the control-plane `ctrlDB Querier` parameter and pass it through to `auditTenants(ctx, auditDB, ctrlDB)`. `runStartupChainContinuityCheck` (`cmd/lenny-gateway/main.go:1084`) gains the same parameter, and the startup wiring passes the control-plane pool alongside the ledger pool (`cmd/lenny-gateway/metricsbackfill.go:381-385`): `runStartupChainContinuityCheck(ctx, chainPool, w.pgPool, ...)` where `chainPool` is `w.billingAuditPool` when configured and `w.pgPool` otherwise. `PeriodicCheck` (`pkg/audit/integrity/periodic.go`) gains a `CtrlDB Querier` field wired to `w.pgPool` at construction (`cmd/lenny-gateway/runserver.go:120-121`) and passed to `CheckChainContinuityRecent`; its existing `DB` field continues to supply the audit enumeration, so the co-located path is unchanged. The `CheckChainContinuity` test callers (`tests/tier2_component/stores/eventstore_test.go`, `tests/tier4_integration/audit_pipeline_test.go`) pass the same pool for `auditDB` and `ctrlDB` in the co-located harness. The tier-1 `PeriodicCheck` unit tests (`pkg/audit/integrity/periodic_test.go`, `pkg/audit/integrity/redaction_test.go`) construct `PeriodicCheck` with only `DB` set today and reach `CheckChainContinuityRecent` through `CheckOnce` (`periodic.go:124`), so each of their nine constructions (`periodic_test.go:179,201,221,248,266,300,318`; `redaction_test.go:70,92`) sets `CtrlDB` to the same `scriptQuerier` as `DB` (the same co-located "same pool for both parameters" convention the tier-2 and tier-4 callers above use), keeping `p.CtrlDB` non-nil so the new `tenantsInDeletion` control-plane read does not dereference a nil `Querier`. `scriptQuerier` dispatches by SQL substring (`periodic_test.go:132-156`) and its default arm returns no rows, so the new `SELECT id FROM tenants WHERE state IN ('deleting', 'deleted')` query resolves to an empty deletion skip-set under those constructions and the nine tests enumerate every tenant exactly as before; `scriptQuerier` gains a dispatch case for that query so a test can script a non-empty skip-set when it needs one.

**Anchor and change (spec note, line 840):** Append the following sentence to the `gdpr.*` exemption paragraph, after the C3 retention-basis sentence added above.

```markdown
Once Phase 4 deletes the non-`gdpr.*` rows, the retained `gdpr.*` rows are the only audit rows left for the tenant. This remnant is a standalone compliance-receipt set: the deletion leaves it without a continuous §11.7 hash chain, so it is exempt from chain verification. The tenant carries `state='deleting'` from the Phase-4 delete through Phase 5, and `state='deleted'` from the Phase-6 tombstone onward. The continuity verifier therefore skips any tenant whose `state` is `deleting` or `deleted` and never walks the remnant, so tenant teardown, including a deletion that stalls after Phase 4, raises no `AuditChainGap` alert ([§16.5](16_observability.md#165-alerting-rules-and-slos)).
```

**Rationale:** `spec/12_storage-architecture.md:840` requires `DeleteByTenant` to skip `gdpr.*` rows, `:831` exempts the receipts, and `:842` says receipts outlive tenant deletion, but the shipped `DeleteByTenant` deletes every row (`auditstore.go:488`). The `event_type NOT LIKE 'gdpr.%'` predicate reuses the pattern already in `PruneRetention` (`auditstore.go:576-577`). Retaining a `gdpr.*`-only remnant leaves a discontinuous chain, so the continuity verifier would report `ChainBroken` and fire the §16.5 `AuditChainGap` alert (`continuity.go:41-43`). Both verifier entry points — `CheckChainContinuity` (the full walk, `ChainFromRows(...).Verify()`, `continuity.go:67-86`) and `CheckChainContinuityRecent` (the windowed startup and periodic path, `continuity.go:102-125`, `main.go:1085`, `periodic.go:124`) — enumerate tenants through the shared `auditTenants` helper (`continuity.go:185`), so applying the exclusion there covers both. The Phase-4 audit `DeleteByTenant` runs while the tenant is `state='deleting'` (`stateForPhase` maps Phases 4, 4a, and 5 to `deleting`, `lifecycle.go:142-146`); the tombstone `state='deleted'` is written only at Phase 6 completion (`pgstore.go:398-403`, which also sets `deleted_at`). A predicate scoped to `state='deleted'` alone (equivalently `deleted_at IS NOT NULL`, set in the same statement) would therefore leave the whole Phase-4-through-Phase-6 window, and a deletion that stalls in `deleting`, falsely alarming, so the exclusion covers `state IN ('deleting', 'deleted')`. The skip-set is resolved from the control-plane pool rather than joined on the ledger connection. Under the §12.3 Tier-3 topology an operator routes audit_log inserts (and the Phase-4 `DeleteByTenant`) to a separate billing/audit Postgres instance while tenants state stays on the primary (`spec/12_storage-architecture.md:103`; `cmd/lenny-gateway/flags.go:415`), and the startup check reads audit_log from that ledger instance (`w.billingAuditPool`, `metricsbackfill.go:381-385`). A `tenants` join issued on the ledger connection would read a `tenants` table that carries no deletion state there (empty or absent), so it would exclude nothing and the false `AuditChainGap` would still fire in exactly the topology the reconciliation must serve. Reading `tenantsInDeletion` from `ctrlDB` (`w.pgPool`, where `tenants.state` is authoritative) and filtering the ledger-instance enumeration against that in-memory skip-set keeps the state-based exclusion correct under both the co-located and the split topology; a tenant with no `tenants` row (the `platform` pseudo-tenant) is never in the skip-set, so live chains are still walked. The mechanism stays one canonical `state IN ('deleting', 'deleted')` skip-set across topologies: when no separate instance is configured `auditDB` and `ctrlDB` are the same pool and both queries run against it. Threading the control-plane `Querier` through `CheckChainContinuity`, `CheckChainContinuityRecent`, `runStartupChainContinuityCheck`, and `PeriodicCheck` adds an internal function parameter and a struct field only; it introduces no new wire surface, RPC, or migration.

### C5. Tests for the reconciliation

**Target:** `tests/tier2_component/auditstore/redaction_test.go` (tier-2 component, real Postgres); `tests/tier11_docs/audit_host_reconciliation_test.go` or a sibling (tier-11 doc/spec consistency).

The reconciliation introduces exactly two new behaviors: the `DeleteByTenant` `gdpr.*` skip with its verifier consequence (C4), and the reconciled §12.8 spec text (C1–C4). The remaining surfaces already ship and are covered; those existing tests are cited under Testing rather than re-added. Details are in the Testing section below.

### C6. Close F-12.8.4 and F-12.2.5 on application

**Target:** `BUILD-GAPS.md`, F-12.8.4 (line 22647) and F-12.2.5 (line 19556).

On application (after the spec edits land and the code and tests are green), mark F-12.8.4 and F-12.2.5 CLOSED with a note that the §12.8 audit-erasure sequence is reconciled to the shipped chain-safe implementation: the phantom `user_id` column is removed, step-14 is restated as a receipt-authorized discontinuity, step-13's mid-chain `DELETE` is replaced by the retention/redaction basis, `DeleteByUser`'s `(0, nil)` no-op is spec-sanctioned, and `DeleteByTenant` honors the `gdpr.*` skip with the continuity verifier reconciled. Reference this proposal id (0028).

## 5. Non-goals

- **Adding a `user_id` (or `session_id`) column to `audit_log`.** It would require recomputing the §11.7 chain, violates append-only immutability, and the phantom column is removed from the spec rather than added to the schema.
- **Implementing a literal step-13 mid-chain user-scoped `DELETE`.** §11.7 provides no non-broken `chainIntegrity` outcome for it, and there is no column to scope the user's sessions; the proposal reconciles the spec instead of building an impossible primitive.
- **Inventing a new `chainIntegrity` enum state (for example an authorized-deletion state).** The reconciliation stays inside the one discontinuity §11.7 already sanctions (`redacted_gdpr`, in-place receipted redaction).
- **Generalizing in-place redaction to every ordinary audit row (direction a) by default.** It is presented as the reviewer's compliance alternative in Open decisions; the recommended and shipped behavior retains ordinary rows under the tamper-evidence basis (direction b).
- **Changing `audit.retentionDays` / `audit.gdprRetentionDays` defaults or the §16.4 retention GC.** `PruneRetention` already enforces the `gdpr.*` retention carve-out; only the erasure-time `gdpr.*` skip in `DeleteByTenant` and the continuity-verifier enumeration are touched.
- **Touching the KMS receipt signing, the SIEM downstream-notification event chain (`gdpr.erasure_deadletter_downstream_notified`), or the erasure-role connection wiring into the orchestrator (F-12.2.16).**
- **Adding any new RPC, HTTP endpoint, store-interface method, or migration.** The reconciliation reuses shipped surfaces only.

## 6. Testing

Two tiers: tier-2 component (the new `DeleteByTenant` behavior and its verifier consequence, against real Postgres) and tier-11 documentation (the reconciled spec text). Tier-0/1 build the doc-comment and code edits. The C4 continuity-signature change (the new `ctrlDB Querier` parameter on `CheckChainContinuity` and `CheckChainContinuityRecent`, reached from `PeriodicCheck.CheckOnce` at `periodic.go:124`) requires the existing tier-1 `PeriodicCheck` unit tests (`pkg/audit/integrity/periodic_test.go`, `pkg/audit/integrity/redaction_test.go`) to set the co-located `CtrlDB` field on each of their nine constructions, so the new control-plane `tenantsInDeletion` read receives a non-nil `Querier` instead of panicking on a nil interface; those constructions otherwise assert the same behavior as before, because the co-located `scriptQuerier` returns an empty deletion skip-set and every tenant is still enumerated. Per `.claude/rules/test-coverage.md`, the store code path reads and writes Postgres, so its behavior is pinned at tier-2 rather than tier-1 (the `auditstore.New(nil)` unit harness cannot exercise a `DELETE`).

- **tier-2 component (`DeleteByTenant` `gdpr.*` skip, boundary and spec-named-failure paths):** Against real Postgres, seed a tenant chain that interleaves ordinary `event_type` rows, `dead_lettered` rows, and `gdpr.%` erasure-receipt rows. Assert `DeleteByTenant` deletes every row whose `event_type NOT LIKE 'gdpr.%'` and retains every `gdpr.%` receipt row and the `audit_redaction_receipts` rows. Boundary case: a tenant whose only rows are `gdpr.%` receipts leaves those rows intact and deletes nothing. `// spec: 12.8 (DeleteByTenant gdpr.* skip, line 840), 12.8 (receipt exemption, line 831). // diagnosis: a failure means tenant teardown either wipes the compliance receipts that must outlive the tenant or fails to purge the non-receipt rows.`
- **tier-2 component (post-teardown remnant is not chain-broken, spec-named-failure path):** After `DeleteByTenant` on a tenant whose `tenants` row is left in `state='deleting'` (the state the tenant actually carries through the Phase-4 audit delete, before the Phase-6 tombstone), run both `integrity.CheckChainContinuity` and `integrity.CheckChainContinuityRecent` (passing the same pool for `auditDB` and `ctrlDB`, the co-located case), and assert the tenant is not enumerated, no result reports `Broken()`, `FirstBroken` returns nil, and the `broken`-state metric does not increment, so no `AuditChainGap` alert fires. Repeat with the `tenants` row advanced to `state='deleted'` and assert the same, so the exclusion holds across the whole teardown window. Contrast case: a live tenant (`state='active'`, or no `tenants` row, the `platform` pseudo-tenant) whose chain is intact still verifies, and a live tenant with a genuinely broken chain still reports `Broken()`. `// spec: 12.8 (post-teardown remnant exempt from chain verification, line 840), 11.7 (chain integrity). // diagnosis: a failure means either tenant teardown fires a false AuditChainGap on the retained gdpr.* remnant during the deleting-or-deleted window, or the deletion-state scoping masks a real chain break.`
- **tier-2 component (split billing/audit-pool exclusion, spec-named-failure path):** Simulate the §12.3 Tier-3 topology with two Postgres databases (or two schemas) in the harness: seed the retained `gdpr.*`-only remnant of a deleted tenant into `audit_log` on the ledger pool (`auditDB`), and write that tenant's `state='deleting'` row into `tenants` on the control-plane pool (`ctrlDB`) while the ledger pool's `tenants` table has no deletion state for it. Assert `integrity.CheckChainContinuityRecent(ctx, auditDB, ctrlDB, lastN)` excludes the tenant and reports no `Broken()` result, confirming the deletion skip-set resolves from `ctrlDB`. Negative control: passing the ledger pool as `ctrlDB` (the wrong pool, with no populated deletion state) does not exclude the remnant and does report `Broken()`, which is the exact defect a `tenants` join on the ledger connection would ship. `// spec: 12.3 (separate billing/audit Postgres, line 103), 12.8 (post-teardown remnant exempt, line 840). // diagnosis: a failure means the continuity verifier reads tenants deletion state from the ledger instance instead of the control-plane pool, so the §12.3 split topology fires a false AuditChainGap on the retained gdpr.* remnant.`
- **tier-11 doc/spec consistency (reconciled §12.8 text, cross-reference resolution):** Assert that §12.8 step-14 and the Article 20 export no longer reference an `audit_log` `user_id` column; that step-14 and the `RedactionReceipt.new_hash` field describe the receipt-authorized discontinuity with no `prev_hash` rewrite; that no "re-seal", "chain rewrite boundary", or `(original_hash, new_hash)`-pair language survives in the §18 build deliverable (`spec/18_build-sequence.md:549`), the §16.5 `AuditChainGap` and `AuditRedactionReceiptMissing` alert rows, the §25 `redacted_gdpr` enumeration entry, the `lenny_audit_redaction_receipt_missing_total` row in `docs/reference/metrics.md`, the single-source alert catalog `pkg/alerting/rules/rules.go` and its generated renders (`charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, `pkg/embedded/manifests/manifests.yaml`), or the external-verifier comment `pkg/audit/integrity/redaction.go`, and that those surfaces instead state the `original_hash`-versus-preserved-row-hash and signature check (the tier-2 `TestPrometheusRuleMatchesAlertCatalog` cross-check separately guarantees the rendered chart manifest matches the corrected catalog, so a stale hand-edited render is caught); that step-13 and the `EventStore` (audit) scope-table row agree that no user-scoped `DELETE` occurs and that the `gdpr.*` exemption paragraph's `DeleteByUser` clause no longer states it filters `gdpr.%` rows out of a deletion; that the `gdpr.*` exemption paragraph carries the ordinary-row retention basis and the post-teardown-remnant note; and that every cross-reference (§11.7, §16.4 at anchor `#164-logging`, §16.5, the Article 20 export subsection) resolves to a real anchor. `// spec: 12.8 (audit erasure reconciliation), 11.7 (chain integrity, redacted_gdpr).`

Already covered by shipped tests (cited rather than re-added, since the reconciliation does not change these behaviors): `DeleteByUser` stays `(0, nil)` and the empty-scope guard holds (`TestDeleteByUserRetainsAudit_spec_12_8_line775`, `TestErasureRejectsEmptyScope_spec_12_8_line753`, `pkg/gateway/audit/auditstore/erasure_test.go:24,36`); `DeadLetteredForUser` payload-key matching, `RedactDeadLettered` leaving `prev_hash` and the row's identity columns untouched, and the `redacted_gdpr` classification with a redacted-row-plus-successor chain that still verifies (`TestDeadLetterRedaction_spec_12_8`, `tests/tier2_component/auditstore/redaction_test.go:32`; `TestHashChainRechainAfterRedaction`, `TestVerifyRowsLawfulRedaction`, `pkg/audit/chain_test.go:107,221`).

## Findings closed on application

- **F-12.8.4** (`BUILD-GAPS.md:22647`, "Audit-log GDPR erasure … is unimplemented", High). Closed: the §12.8 audit-erasure sequence is reconciled to the shipped chain-safe implementation (C1–C4), and the one code divergence (`DeleteByTenant` `gdpr.*` skip, with the continuity verifier reconciled) is fixed.
- **F-12.2.5** (`BUILD-GAPS.md:19556`, "`EventStore` (audit) exposes no erasure interface", High). Closed by the same reconciliation: the §12.2 audit-store erasure participation is `DeleteByUser` as a spec-sanctioned no-op and `DeleteByTenant` honoring the `gdpr.*` skip, both now spec-backed.

## Resolved in adversarial review

Review rounds populate this section. It records each finding fixed and the converging change.

### Pass 1 (2026-07-03, automated)

- **C4 continuity-verifier window covered the tombstone only, leaving a false-`AuditChainGap` window across the whole Phase-4-through-Phase-6 teardown.** The audit `DeleteByTenant` runs at Phase 4 while the tenant is `state='deleting'` (`stateForPhase` maps Phases 4, 4a, and 5 to `deleting`, `lifecycle.go:142-146`), and the tombstone `state='deleted'` is written only at Phase 6 completion (`pgstore.go:398-403`). Broadened the `auditTenants` exclusion predicate from `t.state = 'deleted'` to `t.state IN ('deleting', 'deleted')` so the exclusion covers the whole teardown window and a deletion that stalls mid-teardown. Updated the C4 code comment, the spec note, the §2 decision, and the C4 rationale (which now cites both `CheckChainContinuity` and the runtime `CheckChainContinuityRecent` path sharing the `auditTenants` enumeration). Rewrote the tier-2 test to seed `state='deleting'` after `DeleteByTenant` and assert both verifier paths skip the tenant and report no `Broken()` result or `broken`-state metric increment, then repeat at `state='deleted'`. Dropped the "two equivalent implementations" claim and the deployment-dependent content-based variant from Open decisions item 2 and the C4 rationale, committing to the single canonical enumeration exclusion. (Pass 2 corrects how that exclusion resolves the tenant state under the §12.3 split billing/audit-pool topology; the co-location assumption this note originally recorded does not hold when audit_log is routed to a separate instance.)
- **C2 left the parallel re-seal surfaces in §16.5, §25, and the metrics reference describing the deleted "chain rewrite boundary".** Added C2 anchors rewriting the §16.5 `AuditChainGap` row (`spec/16_observability.md:471`) and `AuditRedactionReceiptMissing` row (`:472`), the §25 `redacted_gdpr` enumeration entry (`spec/25_agent-operability.md:3664`), and the `lenny_audit_redaction_receipt_missing_total` row (`docs/reference/metrics.md:367`) from the `(original_hash, new_hash)`-pair / chain-rewrite-boundary model to the `original_hash`-versus-preserved-row-hash and signature check the shipped verifier performs. Added the surfaces to the C2 target, the summary, §1.2, Section 7, and the tier-11 assertion.
- **C4 left the `DeleteByTenant` doc comment asserting it removes the tenant's entire audit chain.** Added a C4 anchor rewriting the doc comment (`auditstore.go:462-464`) so it states the method deletes every non-`gdpr.%` row and retains the `gdpr.*` erasure-receipt remnant, and that the returned count excludes the retained receipts. Extended the C4 target and Section 7.
- **spec/18:549 build deliverable still described the dead-letter redaction path as re-sealing the chain.** Added a C2 anchor rewriting `spec/18_build-sequence.md:549` from "re-seals the chain" to "produces the receipt-authorized `redacted_gdpr` chain discontinuity", the only remaining non-restore "re-seal" reference once §12.8:813/:822 are rewritten. Updated the summary, §1.2, the C2 target, Section 7, and the tier-11 assertion to cover it.
- **§12.8:840 still said `DeleteByUser` MUST filter `gdpr.%` out of a deletion, contradicting C3's no-deletion rewrite.** Added a C3 anchor replacing the `DeleteByUser` MUST filter clause with the no-deletion wording (`DeleteByUser` performs no user-scoped audit deletion, so `gdpr.*` receipts are retained along with every other audit row), so the exemption paragraph agrees with step 13. Recorded the rationale and added the clause to Section 7 and the tier-11 assertion.
- **C3 step-13 introduced a broken §16.4 cross-reference anchor.** Changed the anchor in the step-13 replacement text from `16_observability.md#164-log-aggregation-and-retention` (no such heading) to `16_observability.md#164-logging` (the `### 16.4 Logging` heading, `spec/16_observability.md:373`). Added the anchor to the tier-11 cross-reference assertion.

### Pass 2 (2026-07-03, automated)

- **C2 corrected the §16.5 alert only in the spec table, leaving the deleted "chain rewrite boundary" model in the single-source alert catalog and its generated chart, docs, and embedded manifests.** The §16.5 `AuditRedactionReceiptMissing` row is single-sourced in the Go alert catalog `pkg/alerting/rules/rules.go:621`, whose `Description` still read "or mismatches the chain rewrite boundary" (`rules.go:3-20` documents that `make generate` renders the catalog verbatim into `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, and `pkg/embedded/manifests/manifests.yaml`, and `render.go:68-69` emits the `Description` as the PrometheusRule annotation). The shipped verifier reconciles the discontinuity through `original_hash` only (`chain.go:425`, `rcpt.OriginalHash == row.Hash && rcpt.Signature != ""`), so the catalog described a boundary it never computes. Added a C2 anchor rewriting the `rules.go:621` `Description` to the `original_hash`-versus-preserved-row-hash and signature check, with a `make generate` step to refresh the three generated renders (not hand-edited, since the tier-2 `TestPrometheusRuleMatchesAlertCatalog` cross-check would fail on a stale render). Added a C2 anchor correcting the mirroring external-verifier comment `pkg/audit/integrity/redaction.go:38-39` from the `(original_hash, new_hash)` boundary match to the `original_hash`-only match. Confirmed the `AuditChainGap` catalog entry (`rules.go:885-889`) already states the receipt model and needed no change. Dropped C2's "No code change" claim, and added the catalog source, the comment, and the generated renders to the C2 target, the summary, §1.2, Section 7, and the tier-11 assertion (now scanning the catalog source and its renders).
- **C4's `auditTenants` join to `tenants` could not resolve the deletion state under the §12.3 split billing/audit-pool topology, so the false `AuditChainGap` still fired in exactly the topology the reconciliation must serve.** When an operator configures a separate billing/audit Postgres (`LENNY_PG_BILLING_AUDIT_DSN`, `spec/12_storage-architecture.md:103`, `flags.go:415`), audit_log inserts and the Phase-4 `DeleteByTenant` route to that instance while the authoritative `tenants` state stays on the primary; the startup continuity check reads audit_log from the ledger instance (`metricsbackfill.go:381-385`, `w.billingAuditPool`), so the `NOT EXISTS (SELECT 1 FROM tenants ...)` join read an unpopulated `tenants` table and excluded nothing. Replaced the cross-table join with a two-`Querier` design: `auditTenants(ctx, auditDB, ctrlDB)` enumerates audit_log from the ledger pool and resolves the `state IN ('deleting', 'deleted')` skip-set from the control-plane pool via a new `tenantsInDeletion` helper, then filters the enumeration against it in memory. Threaded the control-plane `Querier` through `CheckChainContinuity`, `CheckChainContinuityRecent`, `runStartupChainContinuityCheck`, and `PeriodicCheck`, wiring `w.pgPool` as the control-plane pool at the startup and periodic call sites; the co-located case passes the same pool for both. The exclusion predicate stays `state IN ('deleting', 'deleted')`, so the single canonical mechanism holds. Removed the false "co-located in `migrations/0001` / one control-plane Postgres" claim from the C4 rationale and §2 decision and the Pass-1 note, and added a tier-2 split-pool test seeding the remnant and the tenant state on different pools.

### Pass 3 (2026-07-03, automated)

- **C4's new `ctrlDB Querier` parameter broke the nine existing tier-1 `PeriodicCheck` unit tests via a nil control-plane `Querier`, and the two test files were absent from the edit lists.** `PeriodicCheck.CheckOnce` reaches the changed path through `CheckChainContinuityRecent` (`pkg/audit/integrity/periodic.go:124`), so under the proposal it passes `p.CtrlDB`. The nine constructions in `pkg/audit/integrity/periodic_test.go` (lines 179, 201, 221, 248, 266, 300, 318) and `pkg/audit/integrity/redaction_test.go` (lines 70, 92) set only `DB` and then call `CheckOnce`, leaving `p.CtrlDB` a nil interface, so the new `auditTenants` -> `tenantsInDeletion(ctx, ctrlDB)` control-plane read would dereference `nil.Query(...)` and panic before enumerating any tenant. The proposal specifies no in-function nil-fallback, and its "co-located ... same pool" framing holds only at the production wiring sites (`metricsbackfill.go`, `runserver.go`), so the §6 claim that tier-1 has "no other behavior change" was wrong and the two files were missing from §7. Extended the C4 continuity-signatures-and-wiring paragraph to set `CtrlDB` to the same `scriptQuerier` as `DB` on each of the nine tier-1 constructions (matching the co-located "same pool for both parameters" convention the proposal already states for the tier-2 and tier-4 callers) and to add a `scriptQuerier` dispatch case for the `SELECT id FROM tenants WHERE state IN ('deleting', 'deleted')` query (its default arm already returns no rows, so the skip-set is empty and the nine tests enumerate every tenant as before). Corrected the §6 tier-0/1 statement to record the required co-located `CtrlDB` field on the tier-1 constructions, and added `pkg/audit/integrity/periodic_test.go` and `pkg/audit/integrity/redaction_test.go` to the §7 Files-touched Tests bullet.

### Pass 4 (2026-07-03, automated)

- **C2's step-14 replacement stopped at `redacted_gdpr`, re-attributing the retained receipt-persistence and event-emission verbs to the read-only verifier.** The spec tail that survives past the C2 anchor is a coordinated verb series (`sets ... , persists a signed RedactionReceipt ... , and records a gdpr.erasure_deadletter_redacted audit event ...`, `spec/12_storage-architecture.md:813`) that shared the subject "the erasure job" in the original. Because the C2 "after" text ended with "the verifier sets the row's `chainIntegrity` state to `redacted_gdpr`", the applied spec would read "the verifier ... persists a signed `RedactionReceipt` ... and records a `gdpr.erasure_deadletter_redacted` audit event", assigning write actions to an actor that performs none. The verifier is read-only (`classifyRow` returns a classification with no writes, `pkg/audit/chain.go:414-462`); the redaction service persists the receipt (`s.store.RedactDeadLettered`, `pkg/gateway/storage/deadletterredaction/redaction.go:201`) and emits the event (`s.emit.Append`, `redaction.go:228`). Extended the C2 "before" anchor to cover the retained "(see …) , persists a signed `RedactionReceipt` … , and records a `gdpr.erasure_deadletter_redacted` audit event per redacted row carrying" tail, and rewrote the "after" so the verifier only sets the `chainIntegrity` state (the read-side classification) while a fresh sentence, "The erasure job persists a signed `RedactionReceipt` …, and records a `gdpr.erasure_deadletter_redacted` audit event …", restores the erasure job as the subject of the write actions. The "after" ends at "carrying" so the retained field list continues unchanged.
- **C2's step-14 replacement claimed the redaction "leaves every hash-input column untouched", but `payload_canonical_json` is a hash input the redaction rewrites.** The row content hash folds in the canonical payload (`canonicalBytes` marshals `payload_canonical_json` into the hashed tuple, `pkg/audit/chain.go:140,150`; `computeHash` hashes it, `chain.go:159-162`), and the shipped redaction rewrites exactly that column (`UPDATE audit_log SET payload = $3, payload_canonical_json = $4`, `pkg/gateway/audit/auditstore/redaction.go:147-150`). The "leaves every hash-input column untouched" enumeration excluded `payload_canonical_json` yet the same sentence then said the receipt "authorizes the content-hash discontinuity the rewrite introduces", which is self-contradictory: if every hash input were untouched, the recomputed hash would equal the pre-redaction hash and there would be no discontinuity to authorize. Reworded the C2 "after" to state that the redaction rewrites `payload` and `payload_canonical_json` (both content-hash inputs, so the recomputed content hash changes and the receipt is required) while it leaves `prev_hash` and the row's identity and position columns (`id`, `tenant_id`, `sequence_number`, `event_type`, `event_schema_version`, `created_at`) untouched, so the chain link (each successor row's `prev_hash` remains the redacted row's preserved pre-redaction hash) is unbroken. Propagated the same corrected characterization to the §18 build-deliverable C2 "after" (`spec/18_build-sequence.md:549`, which carried the identical "leaves every hash-input column untouched" contradiction) and to the proposal's own prose in §1 (the tree summary), §1.2, the C2 rationale, and the Testing citation, so no section states the removed "leaves every/all hash-input column(s) untouched" claim. The §2 decision already used the accurate wording ("rewrites only `payload` and `payload_canonical_json`, leaves `prev_hash` and every other hash-input column untouched") and was left unchanged. Confirmed `migrations/0165_audit_log_redaction_grant.up.sql:24` grants `UPDATE (payload, payload_canonical_json) ON audit_log TO lenny_erasure`, so the `prev_hash` and identity columns remain immutable to the erasure role.

## Open decisions for review

Both decisions were resolved by sign-off on 2026-07-04 with the recommended/staged option, and neither alters the staged edits: **(1)** step-13 = direction (b), retention basis (retain ordinary audit rows under tamper-evidence + GDPR Art. 17(3)(b)/(e) legal-obligation, `DeleteByUser` stays a no-op); **(2)** `DeleteByTenant` = the C4 staged option (exclude `gdpr.%` from the Phase-4 teardown, keep the compliance-receipt remnant, verifier skips `deleting`/`deleted` tenants). The original framings follow for reference.

1. **Step-13 direction (retention basis versus per-row redaction).** Recommended and staged: retain ordinary (non-dead-lettered) audit rows' structured actor/subject identifiers in the immutable ledger under the tamper-evidence and legal-obligation basis (direction b), matching the shipped behavior and the existing `gdpr.*` exemption, and surface them in the Article 20 export. Alternative: require in-place field-level receipted redaction of the user identifier across every ordinary audit row (direction a), a larger build that reuses the step-14 machinery but destroys the audit trail's user attribution and generates a signed receipt per row. This is a GDPR legal-basis versus audit-integrity tradeoff for the reviewer. The staged spec prose states the retained-row basis (direction b) without committing to a specific legal-obligation argument; direction a would replace C3's step-13 rewrite with a redaction sequence.
2. **`DeleteByTenant` `gdpr.*` handling.** Recommended and staged (C4): honor `spec/12_storage-architecture.md:840` by excluding `event_type LIKE 'gdpr.%'` from the Phase-4 teardown `DELETE`, retain a standalone compliance-receipt remnant, and scope the continuity verifier to skip any tenant in `state='deleting'` or `state='deleted'` so the remnant is exempt from chain verification across the whole Phase-4-through-Phase-6 teardown window (and a deletion that stalls mid-teardown). Alternative: reconcile spec:840 to a whole-chain teardown that also drops `gdpr.*` rows and relies on SIEM-forwarded `gdpr.*` copies for the GDPR enforcement-window proof. The retention note ("receipts outlive any subsequent tenant deletion", spec:842) and the receipt exemption (spec:831) favor the code fix; the broken-chain remnant it leaves is the reviewer's call.

## 7. Files touched on application

- `spec/12_storage-architecture.md`: C1 (§12.8 step-14 phantom-column removal; Article 20 export prose and template query), C2 (§12.8 step-14 re-seal clause; `RedactionReceipt.new_hash` field), C3 (§12.8 step-13; erasure-scope table row; `gdpr.*` exemption paragraph `DeleteByUser` clause and retention-basis sentence), C4 (§12.8 `gdpr.*` exemption paragraph post-teardown-remnant note).
- `spec/16_observability.md`: C2 (§16.5 `AuditChainGap` and `AuditRedactionReceiptMissing` alert rows, missing-receipt condition reworded to the `original_hash`-versus-preserved-row-hash and signature check).
- `spec/18_build-sequence.md`: C2 (§11.7 build deliverable, re-seal wording reworded to the receipt-authorized `redacted_gdpr` discontinuity).
- `spec/25_agent-operability.md`: C2 (§25 `redacted_gdpr` enumeration entry, external-verifier check reworded).
- `docs/reference/metrics.md`: C2 (`lenny_audit_redaction_receipt_missing_total` row, condition reworded).
- `pkg/alerting/rules/rules.go`: C2 (§16.5 alert catalog `AuditRedactionReceiptMissing` `Description`, "chain rewrite boundary" reworded to the `original_hash`-versus-preserved-row-hash and signature check).
- `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, `pkg/embedded/manifests/manifests.yaml`: C2 (regenerated by `make generate` from the edited catalog; not hand-edited).
- `pkg/audit/integrity/redaction.go`: C2 (external-verifier comment, `(original_hash, new_hash)` boundary match reworded to the `original_hash`-versus-preserved-row-hash match).
- `pkg/gateway/audit/auditstore/auditstore.go`: C3 (`DeleteByUser` doc comment), C4 (`DeleteByTenant` doc comment and `gdpr.%` exclusion).
- `pkg/audit/integrity/continuity.go`: C4 (`auditTenants` reads the `deleting`-or-`deleted` skip-set from the control-plane pool and filters the audit-instance enumeration; `CheckChainContinuity` and `CheckChainContinuityRecent` gain the control-plane `Querier` parameter).
- `pkg/audit/integrity/periodic.go`: C4 (`PeriodicCheck` gains the control-plane `Querier` field wired through to `CheckChainContinuityRecent`).
- `cmd/lenny-gateway/metricsbackfill.go`, `cmd/lenny-gateway/main.go`, `cmd/lenny-gateway/runserver.go`: C4 (thread the control-plane pool `w.pgPool` into the startup and periodic continuity checks alongside the ledger pool).
- Tests: `tests/tier2_component/auditstore/redaction_test.go` (tier-2 `DeleteByTenant` `gdpr.*` skip and post-teardown remnant, including the split-pool wiring where audit_log and tenants state resolve from different pools), the existing `CheckChainContinuity` callers (`tests/tier2_component/stores/eventstore_test.go`, `tests/tier4_integration/audit_pipeline_test.go`) updated to pass the control-plane `Querier` (the same pool in the co-located case), the tier-1 `PeriodicCheck` unit tests (`pkg/audit/integrity/periodic_test.go`, `pkg/audit/integrity/redaction_test.go`) updated to set the co-located `CtrlDB` field on each construction (with a `scriptQuerier` dispatch case for the tenants-in-deletion query), tier-11 doc/spec consistency (see Testing).
- `BUILD-GAPS.md`: C6 (mark F-12.8.4 and F-12.2.5 CLOSED, referencing proposal 0028).
