# Technical Design Review — Multi-Tenancy & Tenant Isolation
**Document reviewed:** `docs/technical-design.md`
**Perspective:** 8. Multi-Tenancy & Tenant Isolation
**Review date:** 2026-04-04
**Category code:** TNT

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High | 5 |
| Medium | 9 |
| Low | 3 |
| Info | 2 |
| **Total** | **20** |

---

## Findings

### TNT-001 Cloud-Managed Connection Proxy `connect_query` Sentinel Not Guaranteed [Critical]
**Section:** 12.3, 17.9

The spec correctly mandates the `__unset__` sentinel via PgBouncer's `connect_query` and validates this in the preflight job (Section 17.6). However, Section 17.9 identifies GCP Cloud SQL Auth Proxy as a supported alternative and explicitly notes it "does not pool." For this path the spec recommends deploying PgBouncer or pgcat alongside Cloud SQL Auth Proxy, but does not state whether the preflight check for `connect_query` is applied when the provider proxy is `external`. The preflight job (Section 17.6) only checks "PgBouncer pool mode" and "PgBouncer connect_query" — checks that are skipped when `connectionPooler: external`.

If a deployer uses RDS Proxy, Azure PgBouncer, or AlloyDB, the spec notes "Verify `connect_query` / initialization hook support for the `__unset__` sentinel" but this is advisory text, not an enforced requirement. AWS RDS Proxy supports `SET LOCAL` in transaction mode but its equivalent of `connect_query` initialization is not a standard feature. If the sentinel is absent, the defense-in-depth layer that catches bare queries reaching RLS is silently disabled — no runtime alarm fires and isolation degrades to application-layer WHERE clauses alone.

**Recommendation:** Require that the preflight job actively verifies RLS defense-in-depth for all pooler types, not just PgBouncer. For `connectionPooler: external`, the preflight job must execute a canary query _without_ `SET LOCAL app.current_tenant` and verify it returns zero rows (not an error, since provider proxies may not support `connect_query`). If it returns rows, fail preflight with: `"Provider connection pooler does not enforce RLS sentinel; configure initialization hook or switch to transaction-mode PgBouncer."` Document this check as a profile-invariant requirement in Section 17.9 and add it to the integration test that validates tenant isolation at startup.

---

### TNT-002 MemoryStore Tenant Isolation Is Application-Layer Only — No RLS or Prefix Contract [High]
**Section:** 9.4, 12.8

The `MemoryStore` interface exposes `MemoryScope{TenantID, UserID, AgentType, SessionID}` as a caller-supplied parameter. The spec states the default backend is Postgres + pgvector and that the store is "fully replaceable." However, unlike every other Postgres-backed store, the spec never states that `memories` tables carry RLS policies on `tenant_id`, nor does it confirm that the standard `SET LOCAL app.current_tenant` / RLS enforcement path covers MemoryStore queries.

The spec's erasure table (Section 12.8) lists `MemoryStore` as a deletion target during tenant teardown, but relies on `DeleteByTenant(tenant_id)` — an application-level call. If the default Postgres implementation omits RLS on the `memories` table, a missing WHERE clause in any query path leaks all memories across tenants. Because the store is explicitly "fully replaceable," custom implementations have no contract requiring tenant isolation — only the `MemoryScope` struct.

The prior finding TEN-009 noted this gap. The spec text has not been updated to address it.

**Recommendation:** Add a mandatory isolation contract to the `MemoryStore` interface documentation: _"All implementations MUST enforce tenant isolation such that a query with `TenantID=X` can never return records belonging to `TenantID=Y`, regardless of application-layer correctness."_ For the default Postgres implementation, explicitly state that the `memories` table has an RLS policy identical to all other tenant-scoped tables. Add `TestMemoryStoreTenantIsolation` to the integration test suite. The `MemoryScope.TenantID` parameter must be validated as non-empty at the interface boundary, not just relied upon by callers.

---

### TNT-003 Semantic Cache Tenant Partitioning Not Enforced at the Wrapper Layer [High]
**Section:** 4.9, 12.4

Section 12.4 mandates the `t:{tenant_id}:` prefix convention for all Redis-backed roles and states: "no raw Redis command may be issued without the tenant prefix." Section 4.9 defines the optional `SemanticCache` backed by Redis, with a `CachePolicy` on `CredentialPool`. However, the `SemanticCache` is defined as a pluggable interface and its Redis-backed default is not explicitly placed under the Redis wrapper layer that enforces the prefix convention.

The prior finding TEN-011 identified this. It remains unfixed: the spec does not state that the `SemanticCache` Redis implementation uses `t:{tenant_id}:` prefixed keys, that the wrapper layer enforces this, or that cross-tenant cache hits are impossible. A semantic cache hit on a vector-similarity match could return cached LLM responses from another tenant's session — a confidentiality breach more severe than a quota bypass because the cache content is T3 Confidential data (session transcripts, workspace context).

**Recommendation:** Explicitly bring `SemanticCache` under the Redis wrapper layer's prefix convention. Add `t:{tenant_id}:cache:{pool_id}:{hash}` as the required key format. State this in Section 4.9 and cross-reference Section 12.4. Extend `TestRedisTenantKeyIsolation` to cover semantic cache keys. Additionally, require that cache keys encode `tenant_id` in the semantic similarity vector namespace so that cross-tenant vector lookups are impossible by construction — not just by key-naming convention.

---

### TNT-004 Credential Pool Scoping and Tenant Isolation Unresolved [High]
**Section:** 4.9, 12.2

The prior finding TEN-010 raised that credential pool scoping relative to tenants is unspecified. Reviewing the current spec, Section 4.9 defines `CredentialPool` as an admin API resource, and Section 12.2 lists `CredentialPoolStore` as a Postgres-backed role. However, the spec never states:

1. Whether a `CredentialPool` is a platform-level resource (shared across tenants) or a tenant-scoped resource.
2. Whether the `CredentialPoolStore` tables carry `tenant_id` and are covered by RLS.
3. Whether `tenant-admin` can manage credential pools for their own tenant or whether only `platform-admin` can.

The `CredentialPool` admin API endpoint (`/v1/admin/credential-pools`) is listed without a tenant scoping qualifier. If credential pools are platform-global, a tenant workload assigned to a shared pool could interfere with other tenants (noisy-neighbor on rate limits, pool exhaustion). If pools are tenant-scoped, the spec needs to say so explicitly and enforce it through RLS.

The erasure path in Section 12.8 includes `CredentialPoolStore.RevokeByTenant` during tenant deletion Phase 3, implying pools carry tenant associations — but the schema never confirms this.

**Recommendation:** Explicitly state whether `CredentialPool` is a platform resource or tenant resource. If platform-level, add per-tenant concurrency limits on pool slot consumption so one tenant cannot exhaust pool capacity for others, and document this in Section 11.2 (Budgets). If tenant-scoped, confirm `tenant_id` on the `CredentialPoolStore` tables and RLS coverage. Update the RBAC table in Section 10.2 to specify which role can manage credential pools. Add this to the startup RLS integration test.

---

### TNT-005 Three-Role RBAC Has No Permission Matrix and No Extensibility Path [High]
**Section:** 10.2

The spec defines three roles: `platform-admin`, `tenant-admin`, and `user`. The prior finding TEN-006 recommended adding a `tenant-viewer` role and documenting an extensibility path. Neither has been addressed. The current spec lists only three capabilities per role in prose:

- `platform-admin`: full access across all tenants, manage runtimes/pools/config.
- `tenant-admin`: full access scoped to own tenant; manage users, quotas, legal holds, callback URLs.
- `user`: create/manage own sessions; access other sessions only with explicit grant.

Gaps:
1. No `tenant-viewer` or read-only role. A tenant-admin cannot delegate read-only dashboard access without granting full tenant-admin rights.
2. No billing-only role. Finance teams need `GET /v1/usage` and `GET /v1/metering/events` without session management permissions.
3. Environment-level member roles (`viewer`, `creator`, `operator`, `admin` from Section 10.6) are a parallel RBAC system not reconciled with the three-role model. A `user` with environment `operator` role has undefined privileges relative to the platform RBAC model.
4. No documented extensibility path for custom roles via `tenant-rbac-config`.
5. The data classification table in Section 12.9 references `RBAC role ≥ member` as an access control tier, but `member` is not a defined role in the three-role model.

**Recommendation:** Produce a formal permission matrix table: rows are roles, columns are operations (create session, read other's session, manage quota, view billing, set legal hold, manage pools, configure RBAC, manage credential pools, etc.). Add at minimum a `tenant-viewer` role and a `billing-viewer` role. Reconcile environment-level member roles with platform RBAC — define whether environment `admin` implies `tenant-admin` privileges or whether they are orthogonal. Replace the `member` reference in Section 12.9 with a defined role name.

---

### TNT-006 `noEnvironmentPolicy: allow-all` Semantics and Security Implications Undocumented [High]
**Section:** 10.6

The spec states: `noEnvironmentPolicy: deny-all (platform default) or allow-all. Configurable per tenant, with platform-wide default at Helm time.`

The prior finding TEN-007 flagged that `allow-all` meaning is unclear. The current text still does not specify:

1. What "all" means: all runtimes visible to the platform, all runtimes owned by the tenant, or all runtimes the user has OIDC claims matching?
2. Whether `allow-all` bypasses environment-level RBAC checks entirely (i.e., any authenticated user in the tenant can use any runtime).
3. Whether `allow-all` is inherited by sub-environments or applies only at the tenant root.
4. What the security implication is in a multi-tenant deployment where multiple tenants share a platform — does `allow-all` ever expose runtimes owned by other tenants?

For an operator deploying a multi-tenant platform, `allow-all` at the platform-wide Helm level would be catastrophic if it means any authenticated user reaches any runtime across tenants. Even if it is tenant-scoped, undocumented `allow-all` creates a configuration trap — operators who set it for development may not realize the security implication when promoting to production.

**Recommendation:** Rename `allow-all` to `allow-tenant-runtimes` to make the scope explicit. Define in the spec: "When `noEnvironmentPolicy: allow-tenant-runtimes`, an authenticated user with no environment membership can access any runtime tagged to their tenant with no capability restrictions." Add an explicit warning: "Do not set `noEnvironmentPolicy: allow-tenant-runtimes` on the platform-wide Helm default in multi-tenant deployments — restrict this to individual tenants that have reviewed the access implications." Document the inheritance model for environments nested under a tenant with this policy.

---

### TNT-007 EventStore and Checkpoint Store RLS Coverage Not Confirmed [Medium]
**Section:** 4.4, 12.2

The spec establishes that all tenant-scoped Postgres tables use RLS policies with `SET LOCAL app.current_tenant`. Section 4.2 explicitly states this for session, task, quota, and token store records. However, the `EventStore` (audit events, session logs, stream cursors) and checkpoint metadata records written during checkpointing are not listed in the set of RLS-covered tables.

Section 11.7 describes audit table integrity controls (INSERT-only grants, hash chaining) but says nothing about RLS tenant isolation. A tenant-scoped admin who can query the EventStore could potentially read audit events from other tenants if RLS is absent — this is a compliance violation in a multi-tenant deployment.

The prior finding TEN-008 raised this gap and it remains unaddressed.

**Recommendation:** Explicitly confirm that `events`, `session_logs`, `stream_cursors`, and checkpoint metadata tables all carry `tenant_id` columns with the same RLS policy as other tenant-scoped tables. Add these tables to the startup RLS integration test (`TestRLSTenantIsolation`). For the audit table specifically: ensure that the `lenny_app` role's INSERT-only grant is applied per-tenant row and that a `tenant-admin` querying the audit log via the admin API sees only their own tenant's events — verify this is enforced through RLS and not solely through the `WHERE tenant_id = $1` application-layer filter.

---

### TNT-008 Cross-Environment Delegation Has No Intra-Tenant Guard [Medium]
**Section:** 10.6

Section 10.6 defines a cross-environment delegation model with bilateral `outbound`/`inbound` declarations. The spec states: "Effective cross-environment access requires both sides to permit it." However, the spec never explicitly states that cross-environment delegation is restricted to environments within the same tenant.

In a multi-tenant deployment where `platform-admin` creates environments across different tenants, a malicious or misconfigured environment definition could declare `inbound.sourceEnvironment: "*"` — allowing any environment, potentially from any tenant, to delegate into it. The gateway enforcement steps (Section 10.6) check outbound and inbound declarations but do not include a step: "Verify both source and target environments belong to the same tenant."

The prior finding TEN-012 raised this. The current spec has not added an explicit same-tenant guard.

**Recommendation:** Add a fifth enforcement step to the gateway's cross-environment delegation check: "5. Verify that both the calling environment and the target environment share the same `tenantId`. Cross-tenant cross-environment delegation is not permitted — reject with `CROSS_TENANT_DELEGATION_DENIED`." State this explicitly in Section 10.6. Add an integration test: create two environments in different tenants with matching bilateral declarations and verify gateway rejects the delegation.

---

### TNT-009 Tenant Identity Derivation from OIDC Underspecified [Medium]
**Section:** 4.2, 10.2

Section 4.2 states: "Multi-tenant mode is enabled by configuring OIDC claims that carry tenant identity." Section 10.2 states: "Roles are conveyed via OIDC claims (e.g., a `lenny_role` claim in the ID token)." However, neither section specifies:

1. The configurable OIDC claim name for `tenant_id` (e.g., `lenny_tenant`, `org_id`, a custom claim).
2. What happens when the claim is absent — is the request rejected (fail-closed) or assigned to a default tenant?
3. Whether service-to-service clients (client credentials grant) receive a tenant identity from their client registration or a separate mechanism.
4. How tenant identity is validated against the list of provisioned tenants — can a client forge a `tenant_id` claim for a non-existent or deleted tenant?

The prior finding TEN-013 raised all four of these. The spec text has not been updated.

**Recommendation:** Add a sub-section "Tenant Identity Resolution" to Section 10.2 specifying: (a) The Helm-configurable OIDC claim name (default: `lenny_tenant_id`), with a fallback to the OIDC `iss` claim for single-issuer-per-tenant setups. (b) Fail-closed behavior: absent or unrecognized `tenant_id` claims return `401 UNAUTHORIZED` with `"Tenant identity could not be resolved"`. No silent fallback to `default` tenant in multi-tenant mode. (c) Service-to-service identity: client credentials clients are pre-registered with a `tenant_id` in the platform — the gateway ignores any tenant claim in the assertion token and uses the registered value. (d) Tenant existence validation at authentication time: unknown tenant IDs are rejected before the request reaches any store.

---

### TNT-010 Billing Sequence Number Scoping Creates Cross-Tenant Information Disclosure Risk [Medium]
**Section:** 11.2.1

Section 11.2.1 states billing events carry a "monotonically increasing, per-tenant sequence number (no gaps allowed)." The per-tenant scoping is correct for isolation. However, the spec does not confirm that `GET /v1/metering/events` enforces `tenant_id` scoping when called by a `tenant-admin`, or that the sequence number is strictly per-tenant in the Postgres schema (i.e., a separate sequence object or `GENERATED ALWAYS AS IDENTITY` per tenant, not a global sequence filtered by tenant).

If a global sequence is used and then filtered by `WHERE tenant_id = $1`, the exposed sequence numbers reveal the platform's aggregate billing event rate across all tenants to any tenant-admin who can observe their own events. With two tenant-admins, each can compute the other tenant's event throughput from sequence number gaps.

**Recommendation:** Confirm in Section 11.2.1 that sequence numbers are generated from a per-tenant sequence object (Postgres `CREATE SEQUENCE lenny_billing_seq_{tenant_id}` or equivalent) rather than a global sequence. If a global sequence is used for implementation simplicity, document the information disclosure risk and require that billing APIs expose only sequence numbers ≥ the tenant's first sequence number — or, better, renumber returned events with a tenant-local offset before returning them to callers.

---

### TNT-011 Tenant Deletion Phase 5 (CRD Cleanup) Has No Scope Definition [Medium]
**Section:** 12.8

The tenant deletion lifecycle Phase 5 states: "Remove all tenant-scoped Kubernetes CRD instances (`AgentSession`, pool annotations, NetworkPolicy labels)." However, `SandboxClaim` (the CRD representing active session bindings), `SandboxWarmPool`, and tenant-specific `SandboxTemplate` instances are not listed. If warm pool definitions are tenant-scoped (which is implied but never confirmed), residual CRDs after tenant deletion would cause the WarmPoolController to continue creating pods for a deleted tenant — a ghost workload consuming cluster resources indefinitely.

Additionally, the spec does not specify who owns Phase 5 — the deletion controller, the WarmPoolController, or a Kubernetes Job. The ordering relative to Phase 4 (data deletion) is also not explicit: if data is deleted before CRDs, the WarmPoolController may briefly observe CRDs referencing non-existent Postgres records and enter a reconciliation error loop.

**Recommendation:** Expand Phase 5 to enumerate all CRD types requiring cleanup: `Sandbox`, `SandboxClaim`, `SandboxWarmPool`, `SandboxTemplate` instances scoped to the tenant. Assign ownership to the deletion controller (not the WarmPoolController). Specify ordering: CRD deletion happens after data deletion (Phase 4) so that no new pods are created while data teardown is in progress. Add a finalizer or label (`lenny.dev/tenant`) on pool-related CRDs during provisioning so that Phase 5 can enumerate them reliably. Alert on any residual CRDs post-`deleted` state.

---

### TNT-012 Environment Member Role Permissions Are Undefined [Medium]
**Section:** 10.6

Section 10.6 defines four environment member roles: `viewer`, `creator`, `operator`, `admin`. The spec does not define what permissions each role grants:

- Can a `viewer` list sessions within the environment?
- Can a `creator` create sessions but not delete them?
- Can an `operator` terminate sessions belonging to other users?
- Does environment `admin` imply `tenant-admin` privileges, or is it strictly scoped to environment management?

Without a permission matrix for environment roles, gateway enforcement cannot be implemented unambiguously. Implementers must guess, creating an inconsistency risk between what the spec implies and what ships.

**Recommendation:** Add a permission table for environment member roles analogous to the platform RBAC table (recommended in TNT-005). Minimum columns: `viewer` (read sessions, list runtimes, view usage), `creator` (all viewer permissions + create sessions), `operator` (all creator permissions + terminate/interrupt sessions, manage session artifacts), `admin` (all operator permissions + manage environment membership, update selectors, manage delegation policies). Explicitly state that environment `admin` does not imply `tenant-admin` — it cannot modify quotas, credential pools, or users outside the environment.

---

### TNT-013 MemoryStore Pluggability Allows Silent Tenant Isolation Bypass [Medium]
**Section:** 9.4, 22.6

Section 9.4 states the `MemoryStore` is "fully replaceable." The spec's "Hooks-and-Defaults Design Principle" (Section 22.6) states that all interfaces have "sensible default implementations, disabled unless explicitly enabled." However, unlike the `SemanticCache` or `RequestInterceptor`, the `MemoryStore` interface carries no warning that custom implementations must enforce tenant isolation.

A custom `MemoryStore` backed by an external service (Mem0, Zep, a vector database) might not implement per-tenant data isolation if the deployer is unaware of the requirement. Memories contain T3 Confidential data (Section 12.9) — a leaky custom implementation exposes session context and user memories across tenants.

**Recommendation:** Add a mandatory interface contract to the `MemoryStore` Go interface via a documentation comment: `// All implementations MUST guarantee that Write, Query, Delete, and List operations are strictly scoped to the TenantID in the supplied MemoryScope. Cross-tenant reads and writes MUST be impossible regardless of application-layer correctness.` Provide a contract validation helper (e.g., `ValidateMemoryStoreIsolation(t *testing.T, store MemoryStore)`) that deployers can run against custom implementations. Reference this in Section 22.6 as an exception to the general hooks-and-defaults principle.

---

### TNT-014 Single-Tenant Default Tenant ID Not Protected Against Collision [Low]
**Section:** 4.2

The spec states: "For single-tenant deployments, `tenant_id` defaults to a built-in value (`default`)." If a multi-tenant deployment also has an operator who creates a tenant named `default`, all single-tenant sessions would be co-located with that tenant's data under the same RLS partition. The spec does not reserve or protect the `default` tenant ID.

**Recommendation:** Reserve the `default` tenant ID as a platform-internal value that cannot be created via the admin API (`POST /v1/admin/tenants`). Return `VALIDATION_ERROR` if a deployer attempts to create a tenant with `id: "default"`. Document this in Section 4.2 and the admin API tenant creation endpoint.

---

### TNT-015 Legal Hold Not Checked on Individual User Erasure [Low]
**Section:** 12.8

The legal hold check before tenant deletion (Phase 3) is explicit: the controller checks for active legal holds and blocks if found. However, `DeleteByUser(user_id)` (the GDPR per-user erasure path) has no equivalent legal hold check described. A platform-admin or tenant-admin could invoke user-level erasure on a user whose sessions are under legal hold, potentially deleting artifacts that should be preserved.

**Recommendation:** Apply the same legal hold check to `DeleteByUser` as exists for `DeleteByTenant`: before executing user erasure, query for active legal holds on any session or artifact belonging to the user. Block erasure and emit an `admin.user.erasure_blocked` audit event listing the held resource IDs. Add an `?ignoreLegalHolds` parameter (platform-admin only) with mandatory justification recorded in the audit trail.

---

### TNT-016 Quota Hierarchy Does Not Prevent Tenant-Admin From Bypassing Global Limits [Low]
**Section:** 11.2

Section 11.2 states the hierarchical quota model: "global → tenant → user. A user quota cannot exceed its tenant's quota." However, the spec does not state that a `tenant-admin` cannot raise their tenant's quota above the global platform limit, or that global limits are enforced at the gateway independently of tenant quota configuration.

If a `tenant-admin` can modify their own tenant's quota via `PUT /v1/admin/tenants/{id}` and no server-side cap validates against the global maximum, they can self-service set their quota above what the platform was provisioned to handle.

**Recommendation:** Add an explicit rule: "Tenant quotas cannot be set above the platform-wide maximum configured at Helm time (`global.maxTokensPerTenant`, `global.maxSessionsPerTenant`). The admin API rejects updates that would exceed global caps with `QUOTA_EXCEEDED`." Only `platform-admin` can update global caps. Document this in Section 11.2 and the admin API specification.

---

### TNT-017 Preflight Job Does Not Validate RLS Enforcement End-to-End [Info]
**Section:** 17.6

The preflight job verifies PgBouncer pool mode and `connect_query` sentinel presence, but does not execute an end-to-end RLS validation: insert a row for tenant A, attempt to read it as tenant B without setting the local tenant, and verify zero rows returned. The startup integration test referenced in Section 4.2 is specified as a unit test requirement, not a preflight check that runs on every `helm install` / `helm upgrade`.

**Recommendation:** Add an end-to-end RLS validation step to the preflight job: (1) Insert a test row for `tenant_id = 'lenny-preflight-a'` in a test table with RLS enabled. (2) Execute a SELECT without `SET LOCAL app.current_tenant`. Verify the row is not returned. (3) Execute a SELECT with `SET LOCAL app.current_tenant = 'lenny-preflight-b'`. Verify the row is not returned. (4) Execute a SELECT with the correct tenant. Verify the row is returned. (5) Clean up. This catches RLS misconfiguration in cloud-managed poolers that may not support `connect_query`, providing a provider-agnostic correctness guarantee.

---

### TNT-018 Audit Log Hash Chain Is Per-Tenant but Genesis Hash Is Shared [Info]
**Section:** 11.7

Section 11.7 states: "The first entry in each tenant partition uses a well-known genesis hash." If the genesis hash is a fixed constant shared across all tenants, an attacker who can observe two tenants' audit chains can verify they both have the same genesis — confirming they are on the same platform. More importantly, if a platform-admin computes the genesis hash independently, they could forge the first audit entry for any new tenant without breaking the chain.

**Recommendation:** Derive the genesis hash per-tenant from a deterministic combination of the tenant ID and a platform-specific secret (`HMAC-SHA256(platform_secret, tenant_id || "genesis")`). This makes the genesis hash tenant-specific, unguessable without the platform secret, and verifiable by any party with access to the platform secret. Document the derivation in Section 11.7.
