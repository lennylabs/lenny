# Perspective 17: Credential Management & Secret Handling — Iter7 Review

**Scope:** Verify the iter6 fixes for CRD-020 (High) and CRD-021 (Medium) on main (commit `8604ce9`). Re-check iter6 CRD-022 (Low) and the four open iter5 carry-forwards (CRD-016, CRD-017, CRD-018, CRD-019). Look for regressions introduced by the iter6 observability/runbook changes and for new credential/secret-handling issues surfaced by the iter6 fix commit.

**Calibration:** Severities anchored to the iter1–iter6 rubric per the severity-calibration feedback. Critical = deploy-blocker or security-contract break; High = gap with no runtime workaround on the affected path; Medium = design improvement with workarounds; Low = forensic-trail / docs-consistency polish. A gap that matches a prior-iteration Low is kept at Low even when re-surfaced, per `feedback_severity_calibration_iter5.md`.

## 1. Iter6 fix verification

### CRD-020 (High → Fixed)

End-to-end verification against the iter6 fix commit `8604ce9`:

| Element | Location | Status |
| --- | --- | --- |
| New gauge `lenny_user_credential_revoked_with_active_leases{tenant_id, provider}` in metrics catalog | `spec/16_observability.md` §16.1 line 59 | Present. Description explicitly states steady-state zero, 30s sustained → `CredentialCompromised` critical alert, and cross-refers to §4.9 user revoke endpoint. `credential_ref`/`user_id` excluded per §16.1.1 cardinality rule. |
| `CredentialCompromised` alert extended to cover user path | `spec/16_observability.md` §16.5 line 412 | Present. Expression is `(max by (pool, provider) (lenny_credential_revoked_with_active_leases) > 0) or (max by (tenant_id, provider) (lenny_user_credential_revoked_with_active_leases) > 0)`. Alert description identifies whether the firing instance is pool-scoped (`pool`, `provider` labels) or user-scoped (`tenant_id`, `provider` labels). |
| User-scoped credential revocation section in runbook | `docs/runbooks/credential-revocation.md` lines 163–222 | Present. "User-scoped credentials" section with When-it-applies triggers (U1–U5) + Step U5's audit trail explicitly wires the `lenny_user_credential_revoked_with_active_leases` gauge to the `CredentialCompromised` alert alongside its pool counterpart. |
| `docs/reference/metrics.md` alert catalog sync | line 453 | Present. `CredentialCompromised` entry names both pool and user gauges. |

**Alert-expression correctness.** The Prometheus expression at §16.5 line 412 is well-formed: each clause produces a vector keyed by a compatible label set, `or` at the top level takes the union, and the `max by (...)` aggregations eliminate the high-cardinality `credential_id`/`credential_ref`/`user_id` labels at the rule level so the alerting instance carries only `{pool, provider}` or `{tenant_id, provider}`. The "sustained for more than 30s" requirement is expressed in English rather than via a PromQL `for: 30s` duration, but this is consistent with every other entry in the §16.5 alert table (they are all prose-normative; the actual `for:` expression is left to the PrometheusRule CR).

**Cardinality correctness.** The new gauge labels are `{tenant_id, provider}`. Per §16.1.1, `tenant_id` is explicitly permitted (it appears on `lenny_tokens_consumed_total`, `lenny_policy_denials_total`, `lenny_delegation_budget_utilization_ratio`, and many others). Upper-bound cardinality is `num_tenants × num_providers` ≤ ~10² × ~10¹ = ~10³ for realistic deployments — well within budget. Both forbidden labels (`credential_ref`, `user_id`) are correctly excluded.

**Symmetry with pool-path alert.** The two clauses are structurally identical: same `max by (...) > 0` shape, same 30s sustain condition, same `CredentialCompromised` critical severity. An operator receiving a `CredentialCompromised` page can identify the affected path by the label set on the firing instance (pool→`{pool, provider}`, user→`{tenant_id, provider}`). This closes the structural monitoring asymmetry flagged by CRD-020.

**Conclusion on CRD-020:** Fixed. The gauge, alert, runbook, and docs-catalog sync are all in place. The observability contract now matches the tagged-union deny-list contract from CRD-015 — both the pool and user paths have a production-grade propagation-failure signal.

### CRD-021 (Medium → Fixed)

**Runbook content verification.** `docs/runbooks/credential-revocation.md` lines 163–222 add a "User-scoped credentials" section with:

- "When it applies" triggers (user-reported compromise, telemetry-attributed 4xx, compliance rotation such as contractor offboarding).
- Step U1 — Identify: `GET /v1/credentials` (invoked as the affected user, or as `platform-admin` impersonating).
- Step U2 — Revoke: `POST /v1/credentials/<credential_ref>/revoke` with body `{reason, note}`; describes the deny-list user-shaped entry, the Redis pub/sub propagation path, and the `DELETE` alternative for non-disruptive cleanup.
- Step U3 — Provider-side revocation: explicit mention that this is the user's responsibility (with operator confirmation where possible).
- Step U4 — Rotate: `POST /v1/credentials` re-registration.
- Step U5 — Audit trail: `GET /v1/admin/audit-events?event_type=credential.user_revoked` (and `credential.registered`) + the new gauge/alert reference.

**Cross-reference completeness.** Step U5's last paragraph explicitly ties the new `lenny_user_credential_revoked_with_active_leases` gauge to the `CredentialCompromised` alert, which addresses iter6 CRD-021's "verification procedure" recommendation.

**Conclusion on CRD-021:** Fixed. The operator-facing runbook now covers both pool-credential and user-credential paths with symmetric content depth.

## 2. Prior-iteration carry-forwards

| Prior finding | Iter7 disposition | Evidence on main |
| --- | --- | --- |
| CRD-016 (iter5) `credential.deleted` lacks `active_leases_at_deletion` | Unresolved (Low carry-forward) | §4.9.2 line 1735 `credential.deleted` still carries only `tenant_id, user_id, provider, credential_ref`. Iter6 fix commit did not touch this. |
| CRD-017 (iter3→iter4→iter5→iter6) CLI RBAC scope contradiction | Unresolved (Low carry-forward) | §24.5 lines 85–93 still show `platform-admin` only for `list`, `get`, `add-credential`, `update-credential`, `remove-credential`, `re-enable`; §4.9 line 1102 and §15.2 line 805 still allow tenant-admin. Iter6 fix commit did not touch §24.5. |
| CRD-018 (iter4→iter5→iter6, partial) fault-driven rotation audit gap | Unresolved (Low carry-forward) | No `credential.rotation_completed` event added to §4.9.2; only `credential.rotation_ceiling_hit`, `credential.renewed`, and terminal `credential.fallback_exhausted` exist. Every normal fault-driven or revocation-triggered rotation still produces no audit event. Iter6 fix commit did not touch §4.9.2. |
| CRD-019 (iter5→iter6) user-scoped credential rotation race window | Unresolved (Low carry-forward) | §4.9 line 1347 still contains "one rotation cycle" without defining it; no per-`credential_ref` advisory-lock semantics added to §4.9 or §12. Iter6 fix commit did not touch this. |
| CRD-022 (iter6) `TokenStore` schema for user credentials | Unresolved (Low carry-forward) | §4.9 line 1335 "User-scoped credential storage" callout and §12.2 `TokenStore` role description still lack the `status` column and user-credential lease-index location. Iter6 fix commit did not touch these. |

**Calibration decision.** Per `feedback_severity_calibration_iter5.md`, all five remain at Low. None of them blocks correctness or security posture; each is a forensic-trail or documentation-completeness defect with workarounds for operators familiar with the spec end-to-end. The per-iteration anchor is stable: CRD-016/017/018/019 have been Low since iter4/iter5; CRD-022 was introduced at Low in iter6. No escalation is warranted — but the severity-calibration feedback notes they should not remain indefinitely open, and the iter6 fix pass missed a natural opportunity to clear them.

## 3. New findings (iter7)

### CRD-023. Runbook frontmatter trigger name `CredentialCompromiseSuspected` does not exist as an alert — breaks `runbooks/index.md` and `alerts → runbook` auto-linkage [Low]

**Section:** `docs/runbooks/credential-revocation.md` line 6 (`triggers:` frontmatter)

```yaml
triggers:
  - alert: CredentialCompromiseSuspected
    severity: critical
```

The actual alert name across the specification and the rest of the docs is `CredentialCompromised` (singular noun, no "Suspected" suffix):

- `spec/11_policy-and-controls.md` line 484: `CredentialCompromised`
- `spec/16_observability.md` line 412: `CredentialCompromised` (the rule)
- `docs/operator-guide/observability.md` line 128: `CredentialCompromised`
- `docs/operator-guide/security.md` line 228: `CredentialCompromised`
- `docs/runbooks/index.md` line 37: `CredentialCompromised` → this runbook

The runbook's own body (lines 222) references `CredentialCompromised`. The frontmatter has been drifting since before iter6; the iter6 fix commit added the "User-scoped credentials" section but did not fix the existing mismatch, despite the iter6 `docs/runbooks/credential-revocation.md` being 61 lines of new content. Per `feedback_docs_sync_after_spec_changes.md`, the runbook-touching commit should reconcile adjacent drift.

**Downstream impact.** An operator looking up the `CredentialCompromised` alert in runbook indexers, linters, or automation (e.g., the frontmatter-driven runbook linking table at `docs/runbooks/index.md` which already maps `CredentialCompromised` → `credential-revocation.html`) will not match the frontmatter's `alert: CredentialCompromiseSuspected`. Any tooling that reconciles alert→runbook links against frontmatter (e.g., a CI check that verifies every critical alert in §16.5 has a runbook with matching frontmatter) will miss this runbook for the `CredentialCompromised` alert — exactly the signal that both the iter6 CRD-020 fix and the original CRD-015 fix depend on.

**Severity rationale — Low.** This is a docs-drift defect, not a correctness defect: the runbook content is correct and is discoverable from `docs/runbooks/index.md`. The risk is against tooling that autogenerates alert→runbook wiring from frontmatter. Anchors to the iter5 CRD-016/017 Low rubric (docs-consistency polish) and the iter6 CRD-021 Medium decision (which was higher because of the missing-runbook-content aspect; this is a narrower name-drift).

**Recommendation:** Change line 6 of `docs/runbooks/credential-revocation.md` from `alert: CredentialCompromiseSuspected` to `alert: CredentialCompromised`. Add a CI check (as already recommended by iter6 DOC-024/025) that reconciles every `alert:` frontmatter field across `docs/runbooks/*.md` against the §16.5 alert table.

### CRD-024. User-scoped revocation runbook (`Step U1–U5`) has no blast-radius assessment step; pool path has one, but user-path operators have no parallel way to scope incident response [Low]

**Section:** `docs/runbooks/credential-revocation.md` lines 175–222 (Step U1–U5) vs. lines 55–62 (Step 2 — "Confirm the blast radius")

The pool-credential runbook includes a "Step 2 — Confirm the blast radius" step at lines 55–62 that calls `GET /v1/admin/credential-leases?credentialId=<id>&state=active` to enumerate active leases before taking action. This is essential when the operator's intent is to minimize session disruption (e.g., rotate a credential with 3 active leases during a low-traffic window) or to scope an incident report (e.g., "147 sessions were affected by this compromise").

The user-credential runbook's Step U1 (`GET /v1/credentials`) returns the registration metadata but not the active-lease count, and there is no parallel `GET /v1/admin/user-credential-leases?credentialRef=<ref>&state=active` endpoint in `spec/15_external-api-surface.md`. The `credential.user_revoked` audit event does carry `active_leases_terminated` (§4.9.2 line 1737) *after* revocation, but the operator has no pre-action way to assess scope. The spec does specify that the response of `POST /v1/credentials/{credential_ref}/revoke` returns `{"credentialRef", "leasesTerminated", "revokedAt"}` (§4.9 line 1348) — but this is post-hoc: the revocation has already occurred.

**Concrete operational gap.** An on-call operator responding to a suspected user-credential compromise has no way to answer "how many sessions will I disrupt if I revoke?" before taking the action. For a user who may have 10s of concurrent sessions (e.g., a developer with multiple interactive MCP clients), this is non-trivial.

**Severity rationale — Low.** Workarounds exist: the operator can (a) query the audit log for recent `credential.leased` events filtered by `(tenant_id, user_id, provider)` to estimate active leases; (b) accept that the `POST .../revoke` response carries `leasesTerminated` as the post-hoc scope measurement; (c) coordinate with the user directly to pause their sessions first. None of these are runbook-surfaced. This is docs-completeness defect that maps cleanly to the iter5 CRD-016/017 Low anchor (operator-guidance polish); not Medium because there is no correctness break.

**Recommendation:** Either (a) add a `GET /v1/admin/credential-leases?credentialRef=<ref>&state=active` endpoint to `spec/15_external-api-surface.md` §15.2 alongside the existing pool-credential-leases endpoint (which itself is undocumented per §4 below — see CRD-026), or (b) add an explicit Step U1.5 "Confirm blast radius" to the runbook that runs an audit-log query over recent `credential.leased` events filtered by the target `(tenant_id, user_id, provider)` to produce an approximate affected-session list before Step U2 (revoke).

### CRD-025. `CredentialCompromised` alert firing on the user path provides `{tenant_id, provider}` labels only — runbook has no "alert-driven triage" entry that maps back from those labels to a specific `credential_ref`/`user_id` [Low]

**Section:** `docs/runbooks/credential-revocation.md` Diagnosis section (lines 38–66) vs. §16.5 line 412 `CredentialCompromised` alert

The iter6 fix correctly restricts the new user-path gauge's labels to `{tenant_id, provider}` — the forbidden labels (`credential_ref`, `user_id`) are aggregated out per §16.1.1. Consequently, when `CredentialCompromised` fires with a user-path label set, the operator sees `{tenant_id="...", provider="anthropic"}` — enough to know *which tenant* has a propagation failure, but not *which user's credential* is the leak.

The runbook's Diagnosis section (Step 1 — Identify the credential, lines 40–53) assumes a pool-path incident: the operator already has a `credentialId` and looks up the pool. There is no parallel "Diagnosis — user path" section that, given `{tenant_id, provider}` alert labels, walks the operator through: (a) enumerate recently revoked `credential.user_revoked` audit events in that tenant; (b) cross-join against `credential.leased` events where `source: user` and `active_leases_terminated > 0`; (c) identify the `credential_ref` whose revocation has not propagated.

**Alert-to-runbook workflow gap.** This is the complement of CRD-023: the runbook frontmatter names the wrong alert, *and* the runbook body's diagnosis flow doesn't accept the (correct) alert's label set as a trigger. An operator paged by `CredentialCompromised` on the user-path clause is dropped into a runbook whose first step (`lenny-ctl admin credential-pools list`) is nonsensical for their incident.

**Severity rationale — Low.** The operator has workaround: the audit event stream answers the question. The gap is that it's not surfaced as a runbook step. Matches the iter5 CRD-016 Low anchor (operator-guidance polish). Not Medium because the user-path alert fires on a finite tenant set, and the operator can cross-reference audit events by `tenant_id` filter; but it should be documented.

**Recommendation:** Add a Diagnosis subsection at `docs/runbooks/credential-revocation.md` called "User-credential alert triage" between the pool-path Diagnosis (current lines 38–66) and the User-scoped credentials section (current line 163). Structure: (1) alert labels carry `{tenant_id, provider}` only; (2) query `GET /v1/admin/audit-events?event_type=credential.user_revoked&tenant_id=<id>&provider=<prov>&since=30m` to find candidate `credential_ref` values; (3) cross-check each with `GET /v1/admin/audit-events?event_type=credential.leased&tenant_id=<id>` to identify still-active leases. Cross-reference §4.9.2 for the audit-event field shapes.

### CRD-026. `GET /v1/admin/credential-leases` is referenced in two production runbooks but is not defined in the §15 API surface [Low]

**Section:** `docs/runbooks/credential-revocation.md` line 57–59 (`GET /v1/admin/credential-leases?credentialId=<id>&state=active`), `docs/runbooks/postgres-failover.md` line 154 (`GET /v1/admin/credential-leases?state=active&ageSeconds=gt:<lease-ttl>`) vs. `spec/15_external-api-surface.md` §15.2

The pool-path runbook's Step 2 (Diagnosis — Confirm the blast radius) and the postgres-failover runbook's in-flight-lease audit both use `GET /v1/admin/credential-leases` with query parameters `credentialId`, `state`, `ageSeconds`. This endpoint does not appear anywhere in `spec/15_external-api-surface.md` §15.2 endpoint catalog (verified: `credential-leases` does not appear in the §15 spec). It is also not defined in §4.9's endpoint tables, not in §24's `lenny-ctl` command table, and not listed in `docs/reference/error-catalog.md` / `docs/reference/metrics.md` as an admin surface.

This is a **pre-existing** gap (present before iter6) — the iter6 runbook edit did not introduce it. However, the iter6 fix commit touched `docs/runbooks/credential-revocation.md` and added new runbook content that a reviewer should verify against the spec's API surface; the unresolved pool-path reference to an undefined admin endpoint persisted.

**Severity rationale — Low.** This is a cross-file contract drift between runbook instructions and the §15 endpoint catalog. Anchors to the iter6 DOC-024/025 Low/Medium class (broken cross-references) and the iter5 CRD-016/017 Low anchor (documentation polish). Not Medium because the runbook's Step 2 is advisory and the operator can answer the same question via audit-log queries (which are documented). Not High because the runbook's remediation steps (Steps 1–7) are independently executable even if Step 2's query cannot be run.

**Recommendation:** Either (a) define `GET /v1/admin/credential-leases` in `spec/15_external-api-surface.md` §15.2 with `credentialId`, `credentialRef`, `state`, `ageSeconds`, `tenant_id`, `user_id` query parameters and document the response shape, matching the calls in the two runbooks; or (b) replace the runbook calls with the audit-event-based workarounds documented in CRD-025's recommendation. Option (a) is preferred since the operator workflow (blast-radius scoping, in-flight lease audit after a failover) is a legitimate platform-admin surface.

### CRD-027. Spec §4.9 line 1674 "Emergency revocation runbook" step (4) confirmation instruction ("confirm `CredentialCompromised` clears within 60s") was extended to cover the user path by the iter6 gauge/alert fix, but the inline spec text at §4.9 was not updated to reference the user path [Low]

**Section:** `spec/04_system-components.md` §4.9 line 1674 (Emergency revocation runbook steps) vs. §4.9 line 1348 (user revoke handler)

The iter6 fix commit `8604ce9` touched:
- `spec/16_observability.md` (new gauge + alert expression update)
- `docs/runbooks/credential-revocation.md` (new user section)
- `docs/reference/metrics.md` (alert catalog)

It did NOT touch `spec/04_system-components.md`. Consequently:

1. **§4.9 line 1674's runbook step (4)** — "confirm the `CredentialCompromised` alert clears within 60s (indicating revocation propagation succeeded)" — is pool-specific in context (it appears inside the "Emergency Credential Revocation" subsection at line 1621, which documents only the pool-path `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke` endpoint). A reader walking through §4.9 for the user-path at line 1348 finds no corresponding step-(4)-style confirmation guidance; the runbook linkage exists only in the docs/ runbook, not in the normative spec.

2. **§4.9 line 1348 user revoke endpoint description** lists the action steps (1–4): mark revoked, enumerate leases, terminate, emit event. There is no step (5) "verify `CredentialCompromised` alert clears" mirroring the pool-path runbook step (4). This leaves the normative user-path specification silent on the propagation-verification contract that the iter6 gauge/alert explicitly enables.

The iter6 CRD-020 fix narrative in `p17_credential.md` noted: "Update §4.9 line 1674's emergency-revocation runbook to add a user-credential step, or add a new line 1348 inline runbook fragment that references the alert confirmation step for the user path." Neither spec edit happened — only the docs/ runbook and observability spec were updated.

**Severity rationale — Low.** The `docs/runbooks/credential-revocation.md` user-section (Step U5) now does reference the alert, so the operator-facing runbook is complete. The gap is in the normative spec: an implementer reading §4.9 end-to-end cannot see the propagation-verification contract for user revocations; they see only the bare action-steps (1–4). This is a spec-to-docs divergence of the kind `feedback_docs_sync_after_spec_changes.md` warns against, but in reverse: the docs were updated and the spec was not. Low per iter5 CRD-016/017 anchor (normative-vs-implementation-guidance mismatch without correctness break).

**Recommendation:** Option 1 (minimal): add a sentence to §4.9 line 1348 after step (4): "(5) Operators may verify propagation via the `CredentialCompromised` alert ([§16.5](../spec/16_observability.md#165-alerting-rules-and-slos)) — the user-path clause (`max by (tenant_id, provider) (lenny_user_credential_revoked_with_active_leases) > 0`) clears within 60s once the deny list has propagated." Option 2 (preferred): move the current §4.9 "Emergency Credential Revocation" subsection at line 1621 up one level and give it two subsections — "Pool-credential revocation" (current content) and "User-credential revocation" (new, mirroring the user-path runbook section).

### CRD-028. Runbook Step 7 (audit trail verification) references `event_type=credential.added` — this event does not exist in §4.9.2 audit event catalog [Low]

**Section:** `docs/runbooks/credential-revocation.md` line 158 (`GET /v1/admin/audit-events?event_type=credential.added&since=1h`) vs. §4.9.2 lines 1732–1745

The pool-path runbook's Step 7 (Audit trail) at line 158 queries for `event_type=credential.added`. The §4.9.2 audit event catalog (lines 1732–1745) does not define any `credential.added` event. The closest candidates are:

- `credential.registered` (line 1734) — user-scoped registration (`POST /v1/credentials`), not pool-credential addition
- `credential.re_enabled` (line 1740) — revoked pool credential restoration, a different lifecycle event

There is no audit event for pool-credential addition via `POST /v1/admin/credential-pools/{name}/credentials` — which is a separate CRD-016-class gap (forensic trail incomplete), but scoped to the docs runbook rather than the spec.

This is a **pre-existing** docs-drift issue, not introduced by iter6. But it's material: an operator following Step 7 to verify the rotation audit trail will run `GET /v1/admin/audit-events?event_type=credential.added&since=1h` and get an empty result — not because the operation failed but because the event doesn't exist. The operator will then question whether the rotation itself succeeded.

**Severity rationale — Low.** Matches the iter5 CRD-016 Low anchor (forensic-trail documentation gap). Not Medium because the rotation is audited via `credential.revoked` and `credential.leased` (new) events, both of which are catalogued and queryable — the runbook step just references a nonexistent event name.

**Recommendation:** Either (a) add a pool-credential addition audit event `credential.added` to §4.9.2 (fields: `tenant_id`, `pool_id`, `credential_id`, `added_by`, `secret_ref`) and update the runbook to remain correct; or (b) fix the runbook Step 7 line 158 to query a catalogued event (e.g., `event_type=credential.re_enabled` for rotation-back scenarios, or drop the Step 7 `credential.added` query and keep only `credential.revoked`).

## 4. Convergence assessment

**New iter7 findings:** 6 (CRD-023 Low, CRD-024 Low, CRD-025 Low, CRD-026 Low, CRD-027 Low, CRD-028 Low). None are Critical/High/Medium.

**Carry-forwards open:** 5 (CRD-016 Low, CRD-017 Low, CRD-018 Low, CRD-019 Low, CRD-022 Low) — none escalated; all remain at their prior-iteration severity.

**Iter6 fix verification:** CRD-020 (High) and CRD-021 (Medium) are fixed end-to-end. The observability stack now has symmetric coverage for pool and user revocation paths; the `CredentialCompromised` alert expression is correct, the gauge cardinality is within budget, and the operator-facing runbook has a parallel user-scoped section. The fix does not introduce any regressions — the existing pool-path behavior (gauge, alert, runbook content) is unchanged, and the user-path extension is additive.

**Converged for Critical/High/Medium.** No C/H/M findings remain after iter6. The 11 open Low findings (5 carry-forwards + 6 new iter7) are all forensic-trail, documentation-consistency, and operator-guidance polish items that do not block correctness or security posture. Per `feedback_severity_calibration_iter5.md`, these should not be held open indefinitely; but per the same feedback, they do not warrant escalation to Medium/High solely because of iteration-to-iteration persistence.

**Recommendation:** Declare convergence for Perspective 17 on C/H/M findings. Sweep the 11 open Low findings in a single cleanup pass — they cluster tightly (7 of 11 are runbook/catalog/audit-event drift; 2 of 11 are TokenStore-schema documentation; 2 of 11 are race-window / audit-event completeness) and can be resolved together with targeted spec edits at §4.9.2 (audit events), §15.2 (admin-credential-leases endpoint), §24.5 (RBAC min-role column), and `docs/runbooks/credential-revocation.md` (frontmatter + Step U1.5 + triage section + Step 7 event name).

**Status:** Converged (0 Critical, 0 High, 0 Medium, 11 Low).
