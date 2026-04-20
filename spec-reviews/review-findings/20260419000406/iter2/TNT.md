# Multi-Tenancy & Tenant Isolation Review Findings — iter2

## Summary

TNT-001 is resolved: `spec/10_gateway-internals.md` §10.3 (lines 267–278) adds a required-key startup table covering `auth.oidc.issuerUrl`, `auth.oidc.clientId`, `defaultMaxSessionDuration`, `noEnvironmentPolicy` with `LENNY_CONFIG_MISSING` FATAL log emission and a `TestGatewayConfigValidation` requirement. Cross-refs from §11.1 (line 13) and §10.6 (line 536) correctly point to §10.3. The dev-mode carve-out on the OIDC rows avoids collision with §17.4 `LENNY_DEV_MODE` TLS bypass — the guards do not conflict. PgBouncer transaction-mode RLS, `__all__`/`__unset__` sentinels, tenant deletion lifecycle (§12.8), and the 3-role model remain coherent. `LENNY_CONFIG_MISSING` is defined only at §10.3 (no conflicting references elsewhere). Two follow-ups and one minor observation below.

---

### TNT-002 `noEnvironmentPolicy` omission semantics contradict the §10.3 startup gate [High]
**Files:** `spec/10_gateway-internals.md` (lines 274, 536), `spec/11_policy-and-controls.md` (line 13)

§10.6 line 536 still states: "an omitted `noEnvironmentPolicy` field — whether at the platform level (Helm) or at the tenant level (admin API) — MUST be treated as `deny-all` by the gateway." This directly contradicts the §10.3 table row (line 274) which mandates the opposite for the platform branch: "the gateway does not infer a default at runtime — the value must reach the gateway as an explicit setting so that a misconfigured chart (with the default stripped) fails closed at startup." An implementer following §10.6 treats a missing Helm value as `deny-all`; §10.3 requires refusing to start. The two rules disagree on the platform-level branch of the `OR`. This is a regression the TNT-001 fix introduced without updating §10.6.

**Recommendation:** In §10.6 line 536, split platform- and tenant-level behavior: "At the **tenant** level, an omitted `noEnvironmentPolicy` MUST be treated as `deny-all`. At the **platform** level, an omitted value is a fatal startup error — see [§10.3](#103-mtls-pki)." This preserves tenant-level forgiveness (needed for backward-compatible tenant creation via admin API) while aligning with the §10.3 startup guard.

---

### TNT-003 `LENNY_CONFIG_MISSING` `remediation` field has no documented Helm key for `noEnvironmentPolicy` [Medium]
**Files:** `spec/10_gateway-internals.md` (lines 274, 276), `spec/17_deployment-topology.md` (§17.6)

§10.3 line 276 promises the `LENNY_CONFIG_MISSING` structured log `remediation` field will point to "the relevant Helm value or admin API path." For `auth.oidc.*` and `defaultMaxSessionDuration`, Helm key paths exist elsewhere in the spec. For `noEnvironmentPolicy` at platform scope, no Helm value name is defined anywhere (searches for `global.noEnvironmentPolicy`, `platform.noEnvironmentPolicy`, `Helm.*noEnvironmentPolicy` return zero matches). §17.6 line 345 only describes a *tenant-scoped* `rbacConfig.noEnvironmentPolicy` via bootstrap seed — a different setting from the platform-level Helm default §10.3 requires. An operator hitting `LENNY_CONFIG_MISSING{config_key=noEnvironmentPolicy, scope=platform}` has no documented Helm key to set.

**Recommendation:** Name the platform-level Helm value explicitly (suggested: `global.noEnvironmentPolicy`) in §17 values reference, and cite that key in the §10.3 table's Rationale column.

---

### TNT-004 `TestGatewayConfigValidation` test scope omits dev-mode positive assertion [Low]
**Files:** `spec/10_gateway-internals.md` (line 278)

The test requirement asserts startup *failure* when required keys are absent, but does not require a corresponding positive test that startup *succeeds* with OIDC keys absent when `LENNY_DEV_MODE=true`. Without it, a future change that tightens the gate (e.g., making OIDC required even in dev mode) would silently regress local developer experience.

**Recommendation:** Extend line 278 to add: "…and that the OIDC keys are exempt from the startup gate when `LENNY_DEV_MODE=true`." Mirrors the dev-mode symmetry of the TLS probe.

---

### Verified clean areas

- **Postgres RLS under PgBouncer:** §4.2 (line 163) and §12.3 (lines 41, 51–57) require transaction mode, `SET LOCAL app.current_tenant`, `connect_query` sentinel (self-managed) or migration-enforced tenant-guard trigger (cloud-managed), and `__unset__` policy rejection. `__all__` sentinel (§4.2 line 165) gated by `lenny_tenant_guard` allowlist and `TestRLSPlatformAdminAllSentinel`.
- **3-role RBAC (`platform-admin`, `tenant-admin`, `user`):** Used consistently across §4.2 resource-scoping table (lines 167–181), §11 (billing corrections, legal hold, erasure), §17.6 bootstrap note (line 352). User role mappings and custom role definitions are tenant-scoped with RLS (lines 180–181). `platform-admin` cross-tenant write to `runtime_tenant_access`/`pool_tenant_access` is guarded by `lenny.admin_mode` session variable and a `BEFORE` trigger.
- **Redis/MinIO isolation:** Redis keys prefixed `t:{tenant_id}:...` (billing stream line 184, session inbox §7.2 line 242, experiment sticky §10.7); MinIO objects keyed `/{tenant_id}/...`. T4 per-tenant SSE-KMS and dedicated-node admission webhook (§6.4) are additive defenses.
- **Tenant deletion:** §12.8 (lines 828–844) defines 6-phase lifecycle with `TenantState` enum, `DeleteByTenant` dependency order, KMS key destruction (Phase 4a), legal-hold gating, 72h (T3) / 4h (T4) SLA, tombstone row (prevents ID reuse), phase idempotency. `MemoryStoreErasurePreflight` (§12.8 line 729) catches pluggable-backend no-op stubs at startup and per-job.
- **`noEnvironmentPolicy` default:** `deny-all` is platform default; `allow-all` is flagged with audit-warning interceptor (`lenny-noenvironmentpolicy-audit`, §10.6 line 538) and `lenny_noenvironmentpolicy_allowall_total` counter.
- **Startup-check conflict scan:** All "refuses to start" sites (§10.3 TLS probe, §10.3 config validation, §10.5 CRD schema version, §11.7 pgaudit, §11.7 regulated-profile SIEM gate, §12.8 MemoryStore preflight, §17.4 `LENNY_DEV_MODE` TLS bypass) guard orthogonal surfaces; no precondition set mutually excludes another guard.
