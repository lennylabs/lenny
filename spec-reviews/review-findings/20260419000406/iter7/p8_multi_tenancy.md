# Iter7 Review — Perspective 8: Multi-Tenancy & Tenant Isolation

Scope: iter6 deferred P8 (sub-agent rate-limit exhaustion; see
`iter6/p8_multi_tenancy.md`), so the iter5 baseline is the most recent
reviewed state. This iter7 review (a) verifies the iter5 TNT-014 /
TNT-015 carry-forwards against the current spec, (b) confirms the iter6
API-022 change (`PLATFORM_AUDIT_REGION_UNRESOLVABLE`
`POLICY` → `PERMANENT` / 422) did not break the tenant-residency audit
path in §12.8 Phase 3.5, and (c) sweeps the tenant-isolation surfaces
(RLS with `app.current_tenant` under PgBouncer / cloud-managed pooler,
Redis / MinIO / event-store prefixes, 5-role RBAC, tenant deletion
lifecycle, `noEnvironmentPolicy` asymmetry) for new bypasses or RBAC
gaps.

Severity is calibrated to the iter4 / iter5 anchor per
`feedback_severity_calibration_iter5.md`: a theoretical "could be
stronger" without a demonstrable bypass remains Low; prior-iteration
carry-forwards retain their prior severity unless material spec changes
moved the posture.

ID namespace note: iter1–iter5 used `TNT-NNN`. Iter7 continues with the
`MTI-NNN` prefix as directed for this iteration; carry-forwards are
cross-referenced to their prior `TNT-NNN` IDs.

---

## Prior-iteration carry-forward verification

### MTI-001 (TNT-014 carry-forward, Low) — Still unresolved

**Carried from:** iter5 TNT-014 (itself carry-forward of iter4 TNT-012).

**Spec sections re-examined:**
- `spec/25_agent-operability.md` §25.4 — operations inventory filter
  row, line 1790 (`actor` "auto-restricted to `me`") and line 1795
  (`tenantId` "auto-restricted to its own tenant"); authorization block
  at line 1834.
- `spec/10_gateway-internals.md` §10.2:296 — "Tenant-scoped admin API"
  paragraph: `GET /v1/usage`, `GET /v1/pools`, `GET /v1/metering/events`
  "only return data belonging to the caller's tenant (with
  `billing-viewer` restricted to usage/metering endpoints only).
  `platform-admin` callers see data across all tenants, with an
  optional `?tenant_id=` filter."
- `spec/15_external-api-surface.md` — admin list endpoints that
  advertise `?tenantId=` / `?tenant_id=`.

The iter5 recommendation was to add a normative rule stating that any
non-`platform-admin` passing `?tenantId=OTHER` receives
`403 AUTH_CROSS_TENANT_FORBIDDEN` (`POLICY`, 403, non-retryable,
`details.requestedTenantId` and `details.callerTenantId`), plus
`authz_cross_tenant_attempts_total{role, endpoint}` in §16.1 and an
`authz.cross_tenant_attempt` audit event in §11.7.

**iter7 verification:** grep across the whole `spec/` tree for
`AUTH_CROSS_TENANT_FORBIDDEN`, `authz_cross_tenant_attempts_total`,
`authz.cross_tenant_attempt`, and `TENANT_ID_MISMATCH` returns **no
matches** in any spec file. The §25.4 `tenantId` filter row at line
1795 still uses the **"auto-restricted to its own tenant"** wording —
the silent-substitution phrasing that iter5 called out. The §25.4
authorization block at line 1834 still describes the outcome in terms
of visibility ("tenant-admin sees only operations where …"), not
rejection.

So the iter5 recommendation was not applied, and the three adjacent
prose surfaces (§25.4:1795, §25.4:1834, §10.2:296) still admit the
three readings enumerated in iter5: silent substitution, silent
empty-result, or 403.

**Severity:** Low (retained from iter5). RLS (§4.2:163–165) and
`lenny_tenant_guard` (§12.3:49–57) still block the data-leak path at
the database layer; this is a confused-deputy / UX-clarity concern,
not a demonstrated cross-tenant bypass.

**Recommendation (unchanged from iter5 TNT-014):**
- Add a single normative row in §25.4 (authorization block at
  line 1834) and §10.2:296 stating that any non-`platform-admin` role
  passing a `tenantId` query parameter whose value does not equal the
  role's scoped `tenant_id` claim returns
  `403 AUTH_CROSS_TENANT_FORBIDDEN` — never silently substituted, never
  returned as empty.
- Replace the "auto-restricted" prose on §25.4:1790 and :1795 with the
  rejection semantics, so operators and automation have a single
  deterministic rule.
- Add `AUTH_CROSS_TENANT_FORBIDDEN` to the §15.1 error catalog with
  category `POLICY`, HTTP 403, `details.requestedTenantId` and
  `details.callerTenantId`.
- Add `authz_cross_tenant_attempts_total{role, endpoint}` to §16.1
  and an `authz.cross_tenant_attempt` audit event to §11.7 so the
  rejection is observable by the SIEM.

### MTI-002 (TNT-015 carry-forward, Low) — Still unresolved

**Carried from:** iter5 TNT-015 (narrowed carry-forward of iter4
TNT-013).

**Spec sections re-examined:**
- `spec/27_web-playground.md` §27.3:60 (`apiKey` mode pastes OIDC ID
  token / gateway bearer JWT), §27.9:248–257 (security considerations).
- `spec/10_gateway-internals.md` §10.2 Playground mint invariants block
  and `playground.bearer_mint_rejected` audit event (captures
  `subject_jti` + `subject_typ`, not the raw material).
- `spec/27_web-playground.md` §27.3.1:100–108 "Tenant-claim rejection
  codes (OIDC callback)" table — logs every rejection via the
  tenant-attribution logger and emits an `auth_failure` audit event.
- `spec/16_observability.md` — log attribution / redaction guidance.

The iter5 recommendation was a bullet in §27.9 requiring the gateway
to redact any value matching `Authorization: Bearer \S+` from
auth-failure log lines emitted on the `/playground/*` path (and on
every auth-chain rejection path), replacing the matched value with a
`sha256(<12-hex-chars>)…` token aligned with the existing `jti`
truncation. The rule would be cross-referenced from §16.4.

**iter7 verification:** §27.9 now has six security-consideration
bullets (lines 250–256). Each addresses a distinct posture — redacted
frame inspector, file-upload sanity, the dev-mode red banner, the
`apiKey`-mode yellow banner, the paste-form phishing-vector warning
with the `playground.acknowledgeApiKeyMode` preflight gate, and the
"no credentials in snippets" rule. **None** of them adds a
bearer-redaction rule for the auth-failure logger. A grep for the
recommended sentinel (`Authorization: Bearer \S+`, `bearer.*redact`,
`redact.*bearer`) across the entire `spec/` tree returns only
examples that present bearers in non-log contexts (cURL examples,
header definitions, token type identifiers) — not a redaction
directive. §16.4 still does not carry an equivalent rule.

So when a user mispastes a non-Lenny vendor credential (`sk-...`,
`sk-ant-...`, GitHub PAT, etc.) into the `apiKey` form, the auth
chain's `TENANT_CLAIM_MISSING` / `TENANT_CLAIM_INVALID_FORMAT` /
`TENANT_NOT_FOUND` rejection still flows through the generic
auth-failure logger with whatever fields it chooses. The spec does not
pin a redaction rule.

**Severity:** Low (retained from iter5). The banner (§27.9:254) + the
install-time preflight gate (§27.2:42; §17.6 preflight table) + the
audit-event field choice (§10.2 `playground.bearer_mint_rejected`
carries only `subject_jti` / `subject_typ`) substantially reduce the
incidence. The receiving surface remains the operator's own log sink,
not a cross-tenant leak.

**Recommendation (unchanged from iter5 TNT-015):**
- Add a §27.9 bullet: *"The gateway's auth-failure logger MUST redact
  any value matching `Authorization: Bearer \S+` to
  `Authorization: Bearer sha256(<12-hex>)…` before writing the log
  line, for auth-chain rejections on every `/playground/*` path. For
  consistency the same rule applies on every auth-chain rejection
  path outside playground."*
- Cross-reference the rule from §16.4 (log attribution / redaction).
- Optionally rename the UI label "API key" → "Lenny bearer token
  (JWT)" with placeholder `eyJ…` (§27.3:60) to reduce misdelivery
  incidence further. The `apiKey` mode identifier and Helm value may
  remain for configuration-surface compatibility.

### MTI-003 (iter6 API-022 integrity check) — Verified; no regression

**Cross-iteration verification of iter6 API-022.**

Iter6 API-022 reclassified `PLATFORM_AUDIT_REGION_UNRESOLVABLE` from
`POLICY` to `PERMANENT` and set HTTP 422 to mirror
`BACKUP_REGION_UNRESOLVABLE` /
`ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` /
`LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`. The change is tenant-scoped
in effect (it governs platform-tenant audit writes whose
`unmapped.lenny.target_tenant_id` is non-platform and whose residency
must follow the target tenant's `dataResidencyRegion`).

**iter7 verification of integrity across the multi-tenancy surface:**

1. **Error catalog consistency.** `spec/25_agent-operability.md`
   §25.10 error-catalog row at line 4339 now carries
   `PERMANENT` / 422 (confirmed by direct Read). This matches the
   sibling `REGION_UNRESOLVABLE` rows in the catalog.

2. **Tenant-residency audit path (§12.8 Phase 3.5 sub-step 4).**
   `spec/12_storage-architecture.md` line 885 specifies that the
   `legal_hold.escrowed` ledger row, which references the tombstone
   target tenant via `unmapped.lenny.target_tenant_id`, is routed via
   `StoreRouter.PlatformPostgres(region)` to the target tenant's
   regional platform-Postgres. If the target region's
   `storage.regions.<region>.postgresEndpoint` is missing or
   unreachable, the write is rejected fail-closed with
   `PLATFORM_AUDIT_REGION_UNRESOLVABLE` (HTTP 422, `PERMANENT`), the
   Phase 3.5 migration aborts, and the controller halts in `deleting`
   state pending operator remediation — Phase 4 does not proceed
   because the escrow ciphertext must be accompanied by a durable
   ledger row or Phase 4's `DeleteByTenant` skip logic cannot
   distinguish escrowed from non-escrowed records.

3. **Audit-event residency table consistency.**
   `spec/12_storage-architecture.md` §12.8 "Data residency audit
   events" (line 923) and `spec/16_observability.md` §16.7 audit-event
   enumeration (line 675) both describe the CMP-058 routing with the
   **global `PlatformPostgres()` fallback for the violation event
   itself**, so the incident does not disappear into the unreachable
   region. The metric
   `lenny_platform_audit_region_unresolvable_total{region, failure_mode}`
   and alert `PlatformAuditResidencyViolation` are consistent with
   the 422 / `PERMANENT` posture (observable + paging).

4. **Error-category semantics in context.** `PERMANENT` correctly
   signals that retrying the same write without config remediation
   will not succeed, whereas `POLICY` (the pre-iter6 classification)
   would have mis-signalled a retryable or caller-correctable
   condition — inconsistent with the sibling `REGION_UNRESOLVABLE`
   fail-closed codes.

**Verdict:** No residual category drift, no orphaned `POLICY`-labelled
call sites, and the Phase 3.5 ledger write integrity is preserved. The
tenant-residency audit path remains fail-closed. MTI-003 is resolved
(no action required). Recording it here because iter6's dispatch
notes explicitly asked iter7 to verify it for perspective 8.

---

## New iter7 findings

No new Critical / High / Medium tenant-isolation findings surfaced on
this iter7 sweep. The multi-tenancy surface — RLS under PgBouncer /
cloud-managed pooler, Redis / MinIO / event-store prefixes, 5-role
RBAC with custom-role narrowing, tenant deletion lifecycle with Phase
3.5 legal-hold segregation, `noEnvironmentPolicy` asymmetry — is
convergent at the previously-reviewed posture.

The swept evidence for each sub-surface is summarised below so the
next iteration can confirm the posture without re-reading the full
spec.

### Postgres RLS with `SET app.current_tenant` under pooled connections

`spec/12_storage-architecture.md` §12.3 (lines 24–58) pins the
PgBouncer-compatible path (`RESET` on release; `SET LOCAL
app.current_tenant = ...` at transaction start) and the
cloud-managed-pooler (`lenny_tenant_guard`) trigger alternative. Both
paths produce the same fail-closed semantics — an unset or `__unset__`
`app.current_tenant` rejects every RLS-scoped query. `platform-admin`
`__all__` and `__unset__` sentinels are normative at §4.2 (lines
163–185). This is unchanged from iter4 / iter5.

### 5-role RBAC (not "3-role") with custom tenant-scoped roles

`spec/10_gateway-internals.md` §10.2:260–298 enumerates the five
built-in platform roles — `platform-admin`, `tenant-admin`,
`tenant-viewer`, `billing-viewer`, `user` — with an orthogonal
environment-level role set (`viewer`, `creator`, `operator`, `admin`)
at §10.6. Custom roles are tenant-scoped and **cannot exceed
`tenant-admin` permissions** (§10.2:292). Role assignment precedence
(platform-managed mapping overrides OIDC-derived roles) is pinned at
§10.2:294.

Note: the iter7 dispatch prompt cited a "3-role RBAC model" —
this description appears to be out of date or scoped to a different
surface. The authoritative rubric is the five-role matrix at
§10.2:260–289 plus custom roles at §10.2:292. No finding; recording
for reviewer continuity.

### Redis tenant-prefix isolation with documented exceptions

`spec/12_storage-architecture.md` §12.4 (lines 177–195) pins the
`t:{tenant_id}:` key prefix. Documented exceptions
(pod-scoped coordination keys, circuit-breaker state keys, delegation
tree keys keyed on root session id, playground session-record keys
at `t:{tenant_id}:pg:sess:*`) are enumerated and constrained.
`TestRedisTenantKeyIsolation` (§12.4 test listing; §27.3.1:98 for the
playground-record extension) pins the cross-tenant read / revocation
isolation assertion. No drift.

### MinIO `/{tenant_id}/` prefix validation

`spec/12_storage-architecture.md` §12.5 (T4 per-tenant KMS lifecycle
surface) retains the tenant-prefix rule for ArtifactStore objects.
The `lenny_t4_kms_probe_last_success_timestamp` and
`lenny_t4_kms_probe_result_total` metrics (§16.1:255–256) detect
silent post-provisioning key revocation or lifecycle drift on idle
tenants, which is the sub-finding iter5 recorded as resolved.

### Tenant deletion lifecycle (6 phases + 3.5 legal-hold segregation + 4a KMS)

`spec/12_storage-architecture.md` §12.8 tenant deletion lifecycle
(lines 864–907) covers:

- Phase 3 credential revocation (OAuth tokens, credential-pool
  leases, Redis access-token cache flush).
- Phase 3.5 legal-hold segregation (CMP-052 / CMP-054) with
  region-scoped escrow KEK, region-resolution abort codes
  (`LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE`,
  `PLATFORM_AUDIT_REGION_UNRESOLVABLE`), and the ledger-co-location
  residency gate verified in MTI-003.
- Phase 4 dependency-ordered `DeleteByTenant` across every store in
  the erasure scope, including the platform-global tenant-access
  join tables `runtime_tenant_access` / `pool_tenant_access`, user
  role mappings, **custom role definitions for this tenant**, and
  explicit KMS destruction of the `erasure_salt` key material
  (§12.8:873 line).
- Phase 4a T4 tenant KMS key (`tenant:{tenant_id}`) scheduled
  deletion with provider-standard minimum pending-window (AWS 7d,
  GCP 24h, Vault transit immediate).
- Phase 5 CRD cleanup (`SandboxClaim`, pool annotations, NetworkPolicy
  labels).
- Phase 6 erasure receipt to the audit trail.

The deletion is fail-closed on legal holds (`TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD`,
HTTP 409, `POLICY`) with a `platform-admin`-only override path
(`POST /v1/admin/tenants/{id}/force-delete` with
`acknowledgeHoldOverride: true` and a required `justification`).
The 6 phases + 3.5 + 4a are individually idempotent. No drift.

### `noEnvironmentPolicy` default handling

`spec/10_gateway-internals.md` §10.6 (lines 643–665) pins the
asymmetric default handling: platform-level omission is fatal
(`LENNY_CONFIG_MISSING{config_key=noEnvironmentPolicy,
scope=platform}` at startup; `CrashLoopBackOff` surfaces it in
Kubernetes), while tenant-level omission defaults to `deny-all`. The
audit interceptor `lenny-noenvironmentpolicy-audit` emits the
non-blocking RFC 9110 `Warning:` header and increments
`lenny_noenvironmentpolicy_allowall_total{tenant_id}` on every
`allow-all` write. The posture is operator-observable and fail-closed
by default. No drift.

### OIDC `tenant_id` claim extraction

`spec/10_gateway-internals.md` §10.2 — the `tenant_id` extraction
table and the three rejection codes (`TENANT_CLAIM_MISSING`,
`TENANT_NOT_FOUND`, `TENANT_CLAIM_INVALID_FORMAT`) — have been the
authoritative contract since iter4 TNT-009. The playground
cross-reference at §27.3.1:100–108 preserves this contract in the
OIDC-callback redirect surface. No drift.

---

## Convergence assessment

**Iter7 Perspective 8 status: converged (two Low carry-forwards
outstanding, no Critical / High / Medium findings).**

Two Low items remain (MTI-001 / TNT-014 and MTI-002 / TNT-015), both
unchanged since iter4 / iter5 in concrete scope and both classed as
defense-in-depth rather than demonstrated bypasses:

- MTI-001: non-`platform-admin` `?tenantId=` mismatch outcome is still
  undefined across §25.4:1795, §25.4:1834, §10.2:296. The RLS layer
  and `lenny_tenant_guard` block the data-leak path regardless, but
  the three adjacent prose statements still admit three readings
  (silent substitution, silent empty-result, 403).
- MTI-002: `apiKey`-mode auth-failure logs still lack an explicit
  bearer-redaction rule in §27.9, so a mispasted vendor credential
  can land in the operator log sink. The banner + preflight gate +
  audit-event field choice substantially mitigate.

MTI-003 (iter6 API-022 integrity check) is resolved: the
`PLATFORM_AUDIT_REGION_UNRESOLVABLE` category change to
`PERMANENT` / 422 propagates consistently through §25.10 error
catalog, §16.1 metric semantics, §16.5 alert, §12.8 Phase 3.5
sub-step 4 fail-closed posture, and §12.8 "Data residency audit
events" violation-event routing. No orphaned `POLICY`-labelled call
sites on the tenant-residency audit path.

The tenant-isolation stack (RLS under PgBouncer / cloud-managed
pooler, Redis per-tenant prefix with documented exceptions, MinIO
tenant prefix + T4 per-tenant KMS with continuous probe, event-store
and audit shard routing, 5-role RBAC with tenant-scoped custom roles,
tenant deletion lifecycle with Phase 3.5 legal-hold segregation and
Phase 4a KMS destruction, `noEnvironmentPolicy` fail-closed default,
OIDC `tenant_id` claim extraction) has no new Critical / High /
Medium gaps in iter7.

The two Low carry-forwards (MTI-001, MTI-002) can be addressed in a
doc-polish pass or deferred without blocking convergence.
