# Credential Management & Secret Handling Review — Iteration 2

**Review date:** 2026-04-19
**Scope:** Credential provisioning, leasing, rotation, revocation, propagation across `/spec/`
**Prior findings:** iter1 had no dedicated CRD.md; SEC.md had no credential findings; STR.md's STR-003 addressed by §12.5 extension. This review starts numbering at CRD-001 for this review series.

---

### CRD-001. Credential-file contract example uses `http://` proxyUrl, contradicting mandatory TLS for proxy mode [Medium]

**Location:** `spec/04_system-components.md` line 894 (multi-provider credential file example) vs. line 1445 (SPIFFE-binding / proxy endpoint transport security requirement).

The "Runtime credential file contract" multi-provider JSON example includes:

```json
"materializedConfig": { "proxyUrl": "http://proxy.lenny.internal/v1", "leaseToken": "lt-..." }
```

This directly contradicts the binding rule later in the same section: "The proxy endpoint **must** use TLS (`https://`). […] The controller **rejects** pool registrations where `proxyEndpoint` uses an `http://` scheme and emits a validation error (`InvalidProxyEndpointScheme`)."

Because the example lives in the normative runtime-author contract (the shape runtime authors will copy verbatim when implementing credential-file parsing/testing), a plaintext `proxyUrl` will propagate into third-party runtime test fixtures and could mask the SPIFFE-binding / TLS preconditions that make proxy mode secure.

**Recommendation:** Update the example to `"proxyUrl": "https://proxy.lenny.internal/v1"` and add a one-line note immediately above the JSON block: "`proxyUrl` values are always `https://` — see the LLM Reverse Proxy section." This also ensures the schema validator emitted from this example (if one is generated from spec by docs-tooling) carries the right constraint.

---

### CRD-002. Credential-file example uses undefined provider `openai_proxy` [Low]

**Location:** `spec/04_system-components.md` line 891.

The multi-provider example declares `"provider": "openai_proxy"`. No such provider exists in the Credential Provider table (line 1050–1058), the `materializedConfig` schema tables (lines 1215–1243), or anywhere else in the spec. Neighboring CRD findings in prior review series (e.g., CRD-025/027/028 in the 20260404232226 series) explicitly curated the provider list; `openai_proxy` is out-of-band with that catalog and appears to conflate "OpenAI upstream under proxy-delivery mode" with a distinct provider type. The actual model is that `deliveryMode: proxy` is orthogonal to provider identity — `anthropic_direct` in proxy mode produces the proxy-uniform `materializedConfig`; there is no separate `openai_proxy` provider.

**Recommendation:** Replace `openai_proxy` with a documented provider in proxy mode (e.g., `anthropic_direct` with `deliveryMode: proxy`, or `azure_openai` with `deliveryMode: proxy`). Either is fine; the example only needs to be consistent with the provider catalog.

---

### CRD-003. User-scoped credential storage callout cross-references the wrong KMS rotation section [Medium]

**Location:** `spec/04_system-components.md` line 1284 (user-scoped credential storage callout), re-referenced at line 1321.

The callout states: "User-scoped credentials are subject to the same KMS key rotation procedure ([Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy))". Section 10.5 is **"Upgrade and Rollback Strategy"** — it does not describe KMS key rotation at all. The KMS key rotation procedure lives at §4.9.1 (line 1632, "KMS Key Rotation Procedure"). This is the only section with the DEK rotation, re-encryption job, rollback, and old-key retention procedure that the callout claims user credentials inherit.

**Impact:** Operators reading the user-credential security documentation are sent to the upgrade-rollback section for key-rotation details and will find nothing; this silently undermines the T4 Restricted classification's "identical encryption-at-rest, key rotation, and access-control treatment" guarantee (line 1279).

**Recommendation:** Replace `[Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy)` with `[Section 4.9.1](#491-kms-key-rotation-procedure)` in the user-scoped credential storage callout.

This is a pure cross-reference fix and does not conflict with the §12.5 STR-003 fix — §12.5's T4 per-tenant KMS lifecycle concerns MinIO SSE-KMS (artifact-at-rest), while §4.9.1 concerns the Token Service's application-layer envelope encryption key for Postgres-stored credentials. They are distinct key hierarchies; the cross-reference just points to the wrong one.

---

### CRD-004. Operationally-added credentials silently bypass RBAC validation [Medium]

**Location:** `spec/04_system-components.md` lines 1119, 1158; interacts with `POST /v1/admin/credential-pools/{name}/credentials`.

The spec states: "The Token Service validates **on startup** that all `secretRef` values referenced in the database are accessible via its RBAC grants" (line 1119). However, credentials can be added post-startup via `POST /v1/admin/credential-pools/{name}/credentials`. The RBAC grant (`resourceNames` list) is populated "at install time" (line 1158); operationally-added credentials "require a manual RBAC patch or a re-run of the `helm upgrade`."

**Gap:** The add-credential admin API accepts a new credential with a `secretRef` whose Secret is not yet in the Token Service's `resourceNames` list. No fail-fast validation occurs at add time — the pool entry is persisted in Postgres, then the Token Service fails at lease-materialization time with "forbidden" on `get secrets`, surfaced as `CREDENTIAL_POOL_EXHAUSTED`. This is indistinguishable from a transient pool-exhaustion event by clients, and it blocks all sessions the new credential was meant to unblock.

**Recommendation:** The admin add-credential handler MUST issue a live-probe `get` on the referenced Secret using the Token Service's SA before committing the pool row. On RBAC failure, reject with `400 CREDENTIAL_SECRET_RBAC_MISSING`, message naming the missing `resourceName`. This converts a latent runtime failure (observable only once a session attempts to use the credential) into an admin-time failure with a clear remediation step (run the emitted RBAC patch from `lenny-ctl admin credential-pools add-credential`).

---

### CRD-005. Full-level rotation in-flight gate is unboundedly stuck if a malicious/buggy runtime never emits `llm_request_completed` [Medium]

**Location:** `spec/04_system-components.md` line 788 (in-flight gate) and §4.9 Emergency Credential Revocation (lines 1556–1601).

In direct mode, the adapter tracks outbound LLM requests via runtime-sent `llm_request_started`/`llm_request_completed` lifecycle messages. The gate clears when the counter reaches zero. The spec explicitly states: "The wait is intentionally unbounded […] imposing a timeout would risk an auth failure on an otherwise successful request." A `credential_rotation_inflight_wait_long` warning fires at 60 s, but rotation does not progress.

**Attack/failure mode:** A compromised or buggy runtime can simply stop emitting `llm_request_completed` messages. Because the adapter-agent security boundary (§4.9 / §4.7 "Adapter-Agent Security Boundary") explicitly treats the agent as untrusted, the runtime's self-reported in-flight counter is a trust-in-untrusted-party control path for emergency revocation. A malicious or wedged runtime can indefinitely block `credentials_rotated` from being sent — which directly defeats emergency revocation's "no window where the compromised key continues to reach the provider" guarantee in direct mode (line 1581 already flags the residual risk of already-extracted keys, but this is a separate orthogonal gap: the on-pod key itself can continue to be used because rotation is blocked).

Proxy mode is not affected (the counter is gateway-tracked via stream count), but direct mode in the path described is at risk.

**Recommendation:** Either (a) cap the unbounded gate with a hard ceiling (e.g., 5 minutes) specifically for **revocation-triggered** rotations (flag `rotationTrigger: emergency_revocation` or `fault_driven_rate_limited`), after which the adapter sends `credentials_rotated` regardless and the session falls through to the standard fault-rotation path, or (b) explicitly document this as an accepted residual risk in direct mode and strengthen the revocation runbook text in line 1581 to note that direct-mode emergency revocation is best-effort when the runtime is suspected compromised — the provider-side key rotation remains the only authoritative control. Option (a) is preferred because it aligns with the stated "no-exposure-window" guarantee.

---

**Summary:** Five findings — four Medium (CRD-001, CRD-003, CRD-004, CRD-005) and one Low (CRD-002). No regressions from §12.5 STR-003 (per-tenant KMS lifecycle) — that fix concerns MinIO SSE-KMS and is orthogonal to credential-pool secret encryption. The cross-reference bug at line 1284 is the only KMS-adjacent contradiction and is a straightforward link fix, not a substantive contradiction with the STR-003 extension.
