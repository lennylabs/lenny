# Iter3 CRD Review

**Review date:** 2026-04-19
**Scope:** Regressions from iter2 CRD-001..005 fixes and gaps that remained partial after those fixes.
**Prior findings:** iter1 had no dedicated CRD.md; iter2 filed CRD-001..005. This iteration begins at CRD-006.

---

### CRD-006. New `credential_rotation_inflight_ceiling_hit_total` metric missing mandatory `lenny_` prefix [Medium]

**Files:**
- `spec/16_observability.md` line 45 (§16.1 metrics table)
- `spec/16_observability.md` line 366 (§16.5 warning alerts, `OutstandingInflightAtRotationCeiling`)
- `spec/04_system-components.md` line 795 (§4.7 revocation-triggered rotation ceiling)

Introduced by the iter2 CRD-005 fix. Every other metric in §16.1 is prefixed with `lenny_` (e.g., `lenny_credential_rotation_inflight_wait_seconds`, `lenny_credential_rotation_timeout_total`, `lenny_credential_proactive_renewal_exhausted_total`). The new metric is written as `credential_rotation_inflight_ceiling_hit_total` — unprefixed. This will collide with non-Lenny Prometheus metric namespaces and break the fleet-wide dashboards/alerts that assume a stable `lenny_*` glob (e.g., Grafana panels scoped to `lenny_credential_*`, recording rules, and the `pkg/metrics/lint` validator referenced in §16.1.1).

**Recommendation:** Rename all three occurrences to `lenny_credential_rotation_inflight_ceiling_hit_total`. The alert `OutstandingInflightAtRotationCeiling` expression in §16.5 must also be updated.

---

### CRD-007. New `rotationTrigger: scheduled_renewal` value contradicts canonical `proactive_renewal` used elsewhere [Medium]

**Files:**
- `spec/04_system-components.md` line 795 (§4.7 revocation-triggered rotation ceiling — "e.g., `rotationTrigger: scheduled_renewal`")
- vs. `spec/04_system-components.md` line 1385 (§4.9 Proactive Lease Renewal — "`rotationTrigger: proactive_renewal`")
- vs. `spec/04_system-components.md` line 1675 (§4.9.2 audit events — `credential.renewed` says "`rotationTrigger: proactive_renewal`")

Introduced by the iter2 CRD-005 fix. The ceiling rule text says the ceiling does NOT apply to "scheduled rotations (e.g., `rotationTrigger: scheduled_renewal`)" — but the `rotationTrigger` enum value for non-fault-driven renewals established elsewhere in the spec is `proactive_renewal`. `scheduled_renewal` is not defined anywhere else. Readers will be unable to resolve whether the ceiling applies to `proactive_renewal` (the documented value) or whether there is a separate trigger. The fault-rotation carve-out becomes ambiguous, which matters because misinterpreting it either re-opens the CRD-005 attack surface (if the adapter treats a long-running proactive renewal as "scheduled" and disables the ceiling against a compromised runtime) or causes unexpected false-auth-failure regressions on legitimately long requests under proactive renewal.

**Recommendation:** Change line 795 to read "(e.g., `rotationTrigger: proactive_renewal`)" to match the canonical value used at lines 1385 and 1675. If a separate `scheduled_renewal` trigger is intended, document it in §4.9 Proactive Lease Renewal and add it to §4.9.2 audit events before referencing it here.

---

### CRD-008. New `rotationTrigger: fault_driven_rate_limited` value not defined in §4.9 Fallback Flow enum [Low]

**Files:**
- `spec/04_system-components.md` line 795 (§4.7 revocation-triggered rotation ceiling)
- vs. `spec/04_system-components.md` lines 1346–1370 (§4.9 Fallback Flow, which instead uses `RATE_LIMITED`, `AUTH_EXPIRED`, `PROVIDER_UNAVAILABLE` as the rotation-cause classification)
- `spec/16_observability.md` line 45 (§16.1) and line 366 (§16.5) repeat `fault_driven_rate_limited`

Introduced by the iter2 CRD-005 fix. The ceiling rule says the 300s cap applies when `rotationTrigger ∈ {emergency_revocation, fault_driven_rate_limited}`. `emergency_revocation` is implicit in the revocation endpoint; `fault_driven_rate_limited` has no definition in the spec. The Fallback Flow classifies fault rotations by `error_type` (`RATE_LIMITED`, `AUTH_EXPIRED`, `PROVIDER_UNAVAILABLE`), not by a trigger value named `fault_driven_rate_limited`. A reader cannot tell whether the ceiling also applies to `AUTH_EXPIRED` and `PROVIDER_UNAVAILABLE` fault rotations, or whether only `RATE_LIMITED` receives the cap — a meaningful security ambiguity, because an adversary-compromised runtime could suppress `llm_request_completed` on any of those paths.

**Recommendation:** Either (a) define the full set of `rotationTrigger` enum values in §4.9 (e.g., `emergency_revocation`, `fault_rate_limited`, `fault_auth_expired`, `fault_provider_unavailable`, `proactive_renewal`) and enumerate the ones to which the ceiling applies, or (b) change the ceiling rule to key on error-category instead (e.g., "ceiling applies to any rotation whose trigger is NOT `proactive_renewal`"). Option (b) is stronger — any fault-driven or operator-initiated rotation should cap the in-flight gate, because the threat model (compromised runtime suppressing `llm_request_completed`) is identical across AUTH_EXPIRED, RATE_LIMITED, and PROVIDER_UNAVAILABLE.

---

### CRD-009. Admin-time RBAC live-probe covers only add-credential; pool creation and update paths retain the original CRD-004 gap [Medium]

**Files:**
- `spec/04_system-components.md` line 1171 (§4.9 Admin-time RBAC live-probe — fix scoped to `POST /v1/admin/credential-pools/{name}/credentials`)
- `spec/15_external-api-surface.md` line 571 (`POST /v1/admin/credential-pools` — pool creation, no live-probe)
- `spec/24_lenny-ctl-command-reference.md` line 88 (`PUT /v1/admin/credential-pools/{name}/credentials/{credId}` — update-credential, no live-probe)

iter2 CRD-004 is a partial fix. The admin-time RBAC live-probe added to §4.9 only fires on the add-credential endpoint. However, the underlying latent-failure pattern (credential added to Postgres whose `secretRef` is not in the Token Service's `resourceNames` allow-list → lease-materialization-time failure surfaced as `CREDENTIAL_POOL_EXHAUSTED`) occurs on any admin path that writes a new `secretRef` value:

1. **Pool creation (`POST /v1/admin/credential-pools`)** — tenant-admins or platform-admins register a new pool; the pool body includes `credentials[].secretRef` values that must be in the Token Service's allow-list. No live-probe is performed at creation time.
2. **Credential update (`PUT /v1/admin/credential-pools/{name}/credentials/{credId}`)** — if this endpoint allows the operator to change the `secretRef` to point at a different Secret name, the new reference has the same RBAC gap and the same silent-fail-at-materialization outcome.
3. **Bootstrap seed (`bootstrap.credentialPools` in Helm values)** — the `lenny-bootstrap` Job at install time is the only path that carries an implicit guarantee (Helm template populates the RBAC `resourceNames` list from the same values block); see line 1169. But operator-driven pool creation via the admin API does not share that Helm-rendered guarantee.

Path 1 is particularly risky because `tenant-admin` callers create pools in their own namespace context; they may not even have rights to patch the Token Service RBAC Role, yet the pool creation would succeed.

**Recommendation:** Extend the admin-time RBAC live-probe to fire on all three paths: pool creation, pool update, and credential update. For each `secretRef` value in the request body (including the full list on pool create), the admin handler MUST issue `get` probes with the Token Service ServiceAccount before committing. On any failure, reject with `400 CREDENTIAL_SECRET_RBAC_MISSING` and list the specific missing `resourceName`s. Also update §24.5 `add-credential` CLI row and add a note on `create-pool` and `update-credential` CLI commands to document the same error path.

---

### CRD-010. 300 s revocation-triggered rotation ceiling emits a metric but no audit event [Low]

**Files:**
- `spec/04_system-components.md` line 795 (ceiling rule)
- `spec/04_system-components.md` lines 1662–1679 (§4.9.2 Credential Audit Events — no event for "ceiling hit" / forced rotation)

A ceiling hit is a security-relevant signal: it indicates a runtime failed to emit `llm_request_completed` during an emergency revocation or fault-driven rotation, which the spec itself calls out as "a compromised or buggy runtime that failed to emit `llm_request_completed`" (§16.1, line 45). The current fix records only a counter and fires an alert, both of which are volatile (counters reset on process restart; alerts are delivery-best-effort). No durable audit event is written to `EventStore`. This breaks the principle stated at §4.9.2: "All credential lifecycle events are written to the `EventStore`." Forensic reconstruction after a suspected compromise would be unable to correlate which specific session/lease/pod was the one that failed to quiesce during revocation.

**Recommendation:** Add a `credential.rotation_forced` audit event to the table at §4.9.2 with fields: `tenant_id`, `session_id`, `lease_id`, `pool_id`, `credential_id`, `rotation_trigger` (`emergency_revocation` \| `fault_driven_*`), `outstanding_inflight_count` (the counter value at ceiling), `elapsed_seconds`. Emit it at the same point where the `credential_rotation_inflight_ceiling_hit_total` counter increments. This event should also be classified as SIEM-streamed under §11.7 because it is a compromise-indicator signal.

---

### CRD-011. Admin API surface and CLI reference do not document `CREDENTIAL_SECRET_RBAC_MISSING` error path [Low]

**Files:**
- `spec/15_external-api-surface.md` line 639 (`POST /v1/admin/credential-pools/{name}/credentials` — description only points to §24.5, does not mention the new live-probe or the `CREDENTIAL_SECRET_RBAC_MISSING` failure path)
- `spec/24_lenny-ctl-command-reference.md` line 87 (`lenny-ctl admin credential-pools add-credential` — says "also emits required RBAC patch command" but does not mention the 400 error or the new `details.rbacPatch` response field)

iter2 CRD-004 added the error code to §15.1 line 747 but did not update the endpoint row at line 639 or the CLI reference at §24.5. An operator reading the admin API table or CLI help will not know that a 400 `CREDENTIAL_SECRET_RBAC_MISSING` is a possible response, nor that the response `details.rbacPatch` contains a ready-to-apply remediation. This is a discoverability regression — the whole point of CRD-004's recommendation was to surface the failure at admin-time with a clear remediation path; leaving the admin-surface documentation out of sync defeats that.

**Recommendation:** Add to line 639 description: "Fails with `400 CREDENTIAL_SECRET_RBAC_MISSING` if the Token Service ServiceAccount lacks `get` on the referenced Secret; `details.rbacPatch` contains the required RBAC patch command. See [§4.9](04_system-components.md#49-credential-leasing-service) (Admin-time RBAC live-probe)." Mirror in the CLI reference row at §24.5 line 87.

---

**Summary:** Six findings — one is a direct regression introduced by the iter2 CRD-005 fix (CRD-006, metric naming), two are definition/consistency gaps introduced by the same fix (CRD-007, CRD-008), one is a partial-fix gap against iter2 CRD-004's stated scope (CRD-009), and two are completeness gaps that surfaced while regression-checking the iter2 fixes (CRD-010, CRD-011). No regression was found for CRD-001, CRD-002, or CRD-003 — those fixes are clean. CRD-005's substantive ceiling mechanism is sound; the issues are naming/enum consistency and audit-event emission around it.
