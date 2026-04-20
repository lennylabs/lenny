# Iter3 TNT Review

## Summary

Iter2 fixes for TNT-002 / TNT-003 / TNT-004 are **all cleanly applied** and visible in `spec/10_gateway-internals.md` §10.3 (lines 278–286), §10.6 (lines 544–549) and `spec/17_deployment-topology.md` §17.6 (line 335):

- **TNT-002 regression check:** §10.6 (lines 544–549) now splits platform- vs. tenant-level omission of `noEnvironmentPolicy`. Platform omission is a fatal startup error; tenant omission is treated as `deny-all`. The contradiction with §10.3 is resolved. The "security warning" paragraph at line 560 still correctly warns against platform-wide `allow-all`.
- **TNT-003 regression check:** §17.6 line 335 defines `global.noEnvironmentPolicy` as a **Required — no runtime default** Helm value, with the correct `LENNY_CONFIG_MISSING{config_key=noEnvironmentPolicy, scope=platform}` cross-reference to §10.3. §10.3 line 282 and §10.6 line 549 both cite this Helm key by its fully qualified path. The rationale column and the §17.6 prose are consistent.
- **TNT-004 regression check:** §10.3 line 286 now asserts the dev-mode positive case: "the OIDC keys (`auth.oidc.issuerUrl`, `auth.oidc.clientId`) are exempt from the startup gate when `LENNY_DEV_MODE=true` (mirroring the dev-mode symmetry of the TLS probe — see §17.4)." `TestGatewayConfigValidation` now covers both the startup-failure case and the dev-mode exemption. Guards at §10.3 and §17.4 do not collide.

No regressions introduced. `__all__`/`__unset__` RLS sentinels, PgBouncer transaction-mode + `lenny_tenant_guard` trigger (§12.3 lines 49–57), per-tenant Redis key prefix `t:{tenant_id}:` (§12.4 lines 177–188), MinIO `/{tenant_id}/` path prefix (§12.5), 3-role RBAC (`platform-admin`, `tenant-admin`, `user`), resource tenant-scoping classification table (§4.2 lines 167–181), `tenant_id` format validation `^[a-zA-Z0-9_-]{1,128}$`, OIDC `TENANT_CLAIM_MISSING` / `TENANT_NOT_FOUND` rejection semantics (§10.2 lines 158–169), and the 6-phase tenant deletion lifecycle with `DeleteByTenant` dependency order (§12.8 lines 828–857) all remain coherent.

Three new gaps found in iter2's newly-added surface area (playground auth modes at §10.2 line 180 / §27.3, operations inventory tenant scoping at §25). None are regressions in the literal sense, but two were present-but-unflagged in prior iterations and one was introduced by the iter2 fix for the mode-agnostic `origin: "playground"` JWT claim.

---

### TNT-005 Playground `apiKey` auth path references an undefined "standard API-key auth path" — tenant identity source unspecified [High]
**Files:** `spec/10_gateway-internals.md` §10.2 (line 180), `spec/27_web-playground.md` §27.3 (line 51, line 56)

§10.2 line 180 states: "the `/playground/*` handler validates the user-supplied API key via the standard API-key auth path, then invokes the session-JWT mint…" and §27.3 line 51 says "the key is sent to the gateway on every request." However, the §10.2 authentication boundary table (lines 140–145) enumerates only OIDC/OAuth 2.1, service-to-service client credentials, mTLS+projected-SA (gateway↔pod), and projected-SA (pod→gateway). **There is no "standard API-key auth path" defined anywhere in the spec.** A grep across all 28 spec files for caller-facing API-key bearer auth returns only the playground reference.

The tenant-isolation consequence: §10.2 line 160 mandates that `tenant_id` is extracted from an OIDC claim (`auth.tenantIdClaim`, default `tenant_id`), with `TENANT_CLAIM_MISSING` / `TENANT_NOT_FOUND` hard rejections and "no silent fallback to the `default` tenant in multi-tenant mode." For an API key presented on `/playground/*`, the spec does not say:

- Which database table stores the `(api_key_hash, tenant_id, user_id)` binding.
- Whether API keys are tenant-scoped at issuance (are they created via `POST /v1/admin/tenants/{id}/api-keys`?).
- What error the gateway returns when an API key is not bound to any tenant.
- Whether the admin API-key issuance endpoint exists at all.

Because §27.3 line 56 says "the attachment is driven by the request's ingress route (`/playground/*`), not by the key material" and §10.2 line 180 mirrors this, the gateway is documented to mint a session-capability JWT from an API key — but with no defined source for `tenant_id`, an implementer could either (a) invent a platform-global fallback (cross-tenant hazard), (b) use the `default` tenant (silent fallback, which §10.2 line 169 explicitly forbids for OIDC), or (c) refuse to start because the auth path is undefined. Each choice has different tenant-isolation consequences.

**Recommendation:** Either (a) add a subsection to §10.2 Authentication defining the API-key auth path — admin endpoint `POST /v1/admin/tenants/{id}/api-keys`, the `api_keys` Postgres table with `tenant_id` NOT NULL + RLS, the bearer header (`X-Lenny-API-Key` or `Authorization: Bearer <key>`), the `tenant_id` / `user_id` extraction semantics, and the rejection codes (`401 API_KEY_INVALID`, `403 API_KEY_TENANT_MISSING`); or (b) if API-key auth is not a v1 surface outside the playground, scope-limit the playground `apiKey` mode to require the API key itself to be a gateway-minted service-account token bound to a specific `tenant_id`/`user_id` at issuance (i.e., collapse `apiKey` mode onto the existing service-to-service path). Add `TestPlaygroundApiKeyTenantBinding` to the test requirement.

---

### TNT-006 Playground `dev` mode: `tenant_id` source for the dev HMAC JWT is unspecified [Medium]
**Files:** `spec/27_web-playground.md` §27.3 (line 52, line 57), `spec/10_gateway-internals.md` §10.2 (line 181)

§27.3 line 52 states `playground.authMode=dev` is "no auth; only permitted when `global.devMode=true`." Line 57 says the handler "issues a dev HMAC-signed session JWT with the `origin: "playground"` claim attached." But since there is **no authentication at all** in this mode, there is no source from which to derive `tenant_id`, `user_id`, or `scope`. Yet §10.2 line 160 is unconditional: "the gateway reads this claim from the validated OIDC ID token…" and §4.2 line 185 says dev-mode single-tenant deployments use the built-in `default` tenant — but dev-mode is a gateway flag, not a per-request auth-mode setting, and `playground.authMode=dev` can be used even in a deployment where `auth.multiTenant=true` (the spec does not prohibit this combination). If the playground `dev`-mode handler silently stamps `tenant_id=default` on the dev HMAC JWT while the deployment has other tenants, a developer "testing locally" could bypass RLS against the `default` tenant's data if that tenant happens to also hold real data.

Compare with the strictness of the OIDC path: §10.2 line 166 rejects an absent claim with `TENANT_CLAIM_MISSING` rather than falling through. The dev-mode playground path has no such guard documented.

**Recommendation:** Tighten §27.3 line 52 to add: "`playground.authMode=dev` MUST be rejected at Helm-validate when `auth.multiTenant=true`; i.e., dev-mode playground is incompatible with multi-tenant deployments. The dev HMAC JWT minted by this handler carries `tenant_id=default`, `user_id=dev-user`, and no custom scopes; these fixed values are documented and tested so that no silent cross-tenant access can occur." Also add a sentence to §10.2 line 181: "The dev HMAC JWT minted for `/playground/*` carries `tenant_id=default` and is only permitted when `auth.multiTenant=false`."

---

### TNT-007 Operations inventory `tenant-admin` authorization rule is ambiguous for operations with absent `tenantId` [Medium]
**Files:** `spec/25_agent-operability.md` §25.4 (line 1639)

§25.4 line 1639 states: "`tenant-admin` sees only operations where `started_by` is themselves OR `tenantId` (if present on the operation) matches their tenant." The parenthetical "(if present on the operation)" is ambiguous on the central question: **when `tenantId` is absent** on an operation record (i.e., a platform-scoped operation like a `platform_upgrade`, `restore`, `backup`, or drift reconciliation), can a `tenant-admin` see it?

Reading 1 (rule applies only if `tenantId` is present): absent-tenantId operations are visible to `tenant-admin` iff `started_by == self`. Reading 2 (clause is an allowlist filter): absent-tenantId operations are visible to `tenant-admin` iff `started_by == self`, matching Reading 1. Reading 3 (vacuous-truth): absent `tenantId` vacuously "matches" anything, so `tenant-admin` sees all platform-scoped operations. Reading 3 is a cross-tenant information leak — platform upgrade progress, restore targets, and backup schedules reveal cross-tenant operational posture.

The companion event-subscription rule at line 2558 is tighter: "events that carry no tenant label (platform-scoped events) are matched by all `tenantFilter: "*"` subscriptions but **not** by tenant-scoped subscriptions." The inventory endpoint should follow the same posture.

**Recommendation:** Tighten §25.4 line 1639 to: "`tenant-admin` sees only operations where (a) `started_by` is themselves, OR (b) the operation carries a `tenantId` field AND its value matches the caller's tenant. Platform-scoped operations (no `tenantId` field) are visible **only** when `started_by` matches the caller — the caller never sees operations owned by other tenants or platform-scoped operations started by other principals." Mirrors the event-subscription semantics at line 2558.

---

### Verified clean areas (regression-check results)

- **`noEnvironmentPolicy`:** Platform-level `global.noEnvironmentPolicy` (§17.6 line 335) is correctly named as "Required — no runtime default" with `FATAL` startup failure via `LENNY_CONFIG_MISSING{scope=platform}`. Tenant-level omission defaults to `deny-all` (§10.6 line 547) — asymmetry is now explicit in the spec.
- **`LENNY_CONFIG_MISSING`:** Defined only at §10.3 line 284; no conflicting references elsewhere. Structured fields `config_key`, `scope` (`platform` or `tenant`), `remediation` remain consistent.
- **`TestGatewayConfigValidation`:** §10.3 line 286 now asserts both (a) startup failure when required keys are absent and (b) dev-mode exemption for OIDC keys when `LENNY_DEV_MODE=true`.
- **Postgres RLS:** `SET LOCAL app.current_tenant` under transaction-mode PgBouncer (§4.2 line 163, §12.3 lines 41, 49–57), `__unset__` sentinel for self-managed poolers, `__all__` sentinel for `platform-admin` code paths guarded by `lenny.admin_mode` `SET LOCAL` and a BEFORE trigger, `lenny_tenant_guard` migration-enforced trigger for cloud-managed poolers, `LENNY_POOLER_MODE=external` startup refusal without the trigger.
- **Redis tenant isolation:** All Redis-backed roles (`LeaseStore`, `QuotaStore`, `SemanticCache`, routing cache, token cache, session inbox, DLQ, experiment sticky, billing stream, EventBus) use `t:{tenant_id}:` prefix (§12.4 lines 177–188). EventBus channel names constructed by the EventBus implementation (callers never build raw channel names) — avoids accidental cross-tenant publish/subscribe.
- **MinIO / ArtifactStore:** `/{tenant_id}/` path prefix with mandatory interface-level validation (§12.6 line 26).
- **3-role RBAC:** `platform-admin`, `tenant-admin`, `user` consistently applied across §4.2 (resource scoping), §11 (policy), §12.8 (deletion), §17.6 (bootstrap), §25 (operability). User role mappings + custom role definitions both tenant-scoped with RLS (§4.2 lines 180–181).
- **Tenant deletion:** §12.8 lines 828–857 retains the 6-phase lifecycle with dependency-ordered `DeleteByTenant`, legal-hold gating, KMS key destruction in Phase 4a (T4 only), 72h (T3) / 4h (T4) SLA, tombstone row with `410 Gone` response, post-restore erasure reconciler (CMP-044 fix) that fails-closed on reconciler failure.
- **Cross-environment delegation:** §10.6 lines 532–540 preserves bilateral (outbound ∩ inbound) declaration checks; runtime target resolution goes through tenant identity; tenant-cross-environment delegation remains architecturally impossible because every environment carries `tenantId` with RLS (§4.2 line 174).
- **Helm tenant keys:** `global.noEnvironmentPolicy`, `auth.tenantIdClaim`, `auth.multiTenant`, `agentNamespaces[].resourceQuota`, `agentNamespaces[].limitRange`, `bootstrap.tenant`, `bootstrap.runtimes[].tenantAccess[]`, `bootstrap.pools[].tenantAccess[]`, `bootstrap.credentialPools[]` all cross-referenced consistently between §17.6 and §10/11/12.
- **Startup-check scan:** §10.3 TLS probe, §10.3 config validation, §10.5 CRD schema version, §11.7 pgaudit, §11.7 SIEM regulated profile, §12.8 `MemoryStoreErasurePreflight`, §17.4 `LENNY_DEV_MODE` TLS bypass, §12.3 `LENNY_POOLER_MODE=external` trigger check — orthogonal surfaces; no precondition set mutually excludes another guard.
