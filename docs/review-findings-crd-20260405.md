# Credential Management & Secret Handling Review Findings — 2026-04-05

**Document reviewed:** `docs/technical-design.md` (5,277 lines)
**Perspective:** 17. Credential Management & Secret Handling
**Category code:** CRD
**Reviewer focus:** Credential lifecycle end-to-end — provisioning, leasing, rotation, revocation, and propagation through delegation. LLM reverse proxy bottleneck risk, mid-session rotation reliability, pool exhaustion handling, three-mode differentiation for operators, and KMS integration completeness.

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High     | 4 |
| Medium   | 5 |
| Low      | 3 |
| Info     | 2 |

---

## Critical

### CRD-001 No Emergency Credential Revocation Path for Compromised Pool Keys [Critical]
**Section:** 4.9, 11.4, 16.5

The spec describes lease revocation only in the context of user invalidation (Section 11.4, step 6: "Credential leases held by the user's sessions are revoked — returned to pool"). There is no documented procedure for revoking all leases from a **specific credential** within a pool — the scenario where a single API key or IAM role is suspected compromised and must be pulled immediately from all active sessions.

The admin API exposes `POST/PUT/GET/DELETE /v1/admin/credential-pools` for pool management, but there is no endpoint specified for `POST /v1/admin/credential-pools/{id}/credentials/{credId}/revoke` or equivalent. Without this, an operator whose `key-2` in `claude-direct-prod` is compromised must either delete the entire pool (disrupting all sessions using any key in that pool) or wait for natural lease expiry — during which the compromised key continues to be injected into upstream requests.

The `CredentialPoolLow` alert fires only at `Warning` level and only at 20% availability — it does not page on a hard pool exhaustion reached by emergency revocation. The critical alert table (Section 16.5) has no `CredentialPoolExhausted` entry. For proxy mode, the proxy "immediately rejects requests" on lease revocation, but the revocation of a specific pool credential is not triggerable via any documented operator path.

**Recommendation:**
1. Add `POST /v1/admin/credential-pools/{poolId}/credentials/{credId}/revoke` to the admin API. The operation MUST: mark the credential as `revoked` in `CredentialPoolStore`, terminate all active leases backed by that credential (via in-process signal or Redis pub/sub, matching the cert deny-list pattern from Section 10.3), and, for proxy mode, cause the proxy to reject further requests from sessions holding those leases immediately.
2. Add a `CredentialCompromised` critical alert (`CredentialPoolStore` sees a credential in `revoked` state while sessions still hold active leases against it for > 30s — indicating revocation propagation failure).
3. Document the emergency revocation runbook alongside the credential pool exhaustion runbook already referenced in Section 4.9.

---

## High

### CRD-002 Kubernetes Secrets Holding Pool Credentials Have No Rotation or Encryption Guidance [High]
**Section:** 4.9

The credential pool YAML shows `secretRef: lenny-system/anthropic-key-1` — raw Kubernetes Secrets. The spec notes only "Kubernetes Secrets used only for bootstrap/internal credentials, not per-user OAuth tokens" (Section 4.3), and states long-lived credentials live "only in the Token Service and Kubernetes Secrets" (Section 4.9 Security Boundaries). However, there is no guidance on:

- **etcd encryption at rest for Secrets.** Kubernetes does not encrypt Secrets in etcd by default. Anyone with etcd access sees all Secret values in plaintext. The spec requires etcd encryption for CRD data at scale but never for Secrets specifically.
- **Secret rotation.** The KMS key rotation procedure (Section 10.5) covers envelope encryption of OAuth tokens in Postgres. There is no parallel procedure for rotating the `secretRef` API keys — how does an operator cycle `anthropic-key-1` without disrupting active sessions?
- **External Secrets Operator or CSI Secret Store integration.** The spec is silent on whether pool credentials should use a secrets manager (Vault, AWS Secrets Manager, GCP Secret Manager) rather than Kubernetes Secrets.

For a platform where least-privilege is a core design principle (Section 1) and T4 Restricted data classification applies to credential pool secrets (Section 12.6), relying on unencrypted Kubernetes Secrets contradicts the stated posture.

**Recommendation:**
1. Require etcd encryption for Secrets via `EncryptionConfiguration` as part of the production hardening checklist (add to Section 17 or the `lenny-preflight` validation Job in Section 17.6).
2. Add a `secretRef` rotation procedure analogous to the KMS rotation procedure in Section 10.5: to rotate `anthropic-key-1`, add `anthropic-key-3` to the pool, wait for the health scorer to confirm it is healthy, then mark `key-1` for retirement via the credential revocation endpoint (CRD-001).
3. Note that deployers with stricter posture should use External Secrets Operator or a CSI Secret Store provider to source pool credentials from a dedicated secrets manager, avoiding Kubernetes Secret storage entirely.

---

### CRD-003 Full-Tier Credential Rotation via Lifecycle Channel Has No Timeout or Failure Path [High]
**Section:** 4.7, 4.9

The spec defines `credentials_rotated` (adapter → runtime) and `credentials_acknowledged` (runtime → adapter) on the lifecycle channel. Section 4.9 describes the Full-tier rotation path as: "Gateway calls `RotateCredentials` RPC; adapter sends `credentials_rotated` on lifecycle channel; runtime rebinds provider in-place. No session interruption."

However, the spec does not specify:

1. **What happens if `credentials_acknowledged` is never received.** The analogous checkpoint handshake has an explicit 60-second timeout and recovery path (Section 4.4). The credential rotation handshake has no documented timeout. If a runtime hangs after receiving `credentials_rotated` — for example, during an in-flight LLM request that is mid-stream — the adapter has no documented mechanism to know whether the new credential is active or whether the runtime is still using the old one.

2. **What happens to in-flight LLM requests during the rebind window.** A Full-tier runtime receiving `credentials_rotated` while a streaming LLM response is in progress must either: complete the in-flight request with the old credential (acceptable but undocumented), abort and retry with the new credential, or hold new outgoing requests until rebind completes. None of these behaviors are specified.

3. **Whether the old credential is immediately invalidated at the provider when `RotateCredentials` is called.** If the old credential is pulled from the pool simultaneously with the rotation signal, an in-flight LLM request using the old credential will receive an auth failure mid-stream.

**Recommendation:**
1. Add a timeout to the `credentials_acknowledged` wait (matching checkpoint's 60-second window), after which the adapter should fall back to the Standard-tier rotation path (checkpoint + restart) and emit a `credential_rotation_timeout` warning event.
2. Specify the in-flight request behavior: the adapter MUST allow the current in-flight LLM request to complete (or receive an error from the provider) before signaling `credentials_rotated`. New outgoing requests are queued during the rebind window.
3. Clarify whether the old credential is kept active at the provider until `credentials_acknowledged` is received or until a configurable grace period (e.g., 30s) elapses after rotation — to prevent auth failures on in-flight requests.

---

### CRD-004 LLM Proxy Extraction Trigger Has No Defined Threshold or Operability Criteria [High]
**Section:** 4.1, 4.9, 16.5

The spec documents the LLM Proxy as an internal gateway subsystem with its own goroutine pool, concurrency limits, and circuit breaker. The extraction trigger is defined as: "LLM Proxy throughput requires independent scaling from session streaming (e.g., high proxy-mode adoption creates disproportionate upstream connection load and long-lived streaming goroutines that would over-scale or under-scale the other subsystems)."

However, there are no concrete operability criteria for when this trigger is met:

- No metric threshold is specified: at what value of `lenny_gateway_llm_proxy_active_connections` or `lenny_gateway_llm_proxy_request_duration_seconds` p99 should operators begin planning extraction?
- The capacity table shows 5,000 concurrent LLM proxy streams at Tier 3 (Section 16.5). If proxy mode is the recommended default for multi-tenant deployments, and sessions approach 10,000 concurrent, the ratio of proxy streams to session streams approaches 1:1 — meaning the LLM Proxy subsystem is effectively as large as the Stream Proxy subsystem. There is no guidance on whether this ratio is sustainable within a single binary or requires extraction.
- When the LLM Proxy subsystem trips its circuit breaker, all proxy-mode sessions on that gateway replica lose the ability to make LLM calls — but the sessions remain attached and the client receives no error until the pod times out. The behavior of the circuit breaker open state (half-open retry interval, error surfaced to the agent) is not specified for the LLM Proxy specifically.

**Recommendation:**
1. Specify a numeric threshold for the extraction trigger: e.g., "extract when `lenny_gateway_llm_proxy_active_connections` exceeds 60% of `maxConcurrent` across all replicas for more than 10 minutes, and is growing faster than gateway HPA can respond."
2. Specify the LLM Proxy circuit breaker open-state behavior: the adapter should receive a `PROVIDER_UNAVAILABLE` event so it can signal `RATE_LIMITED`/`PROVIDER_UNAVAILABLE` to the gateway fallback chain rather than silently hanging.
3. Add a `LLMProxyCircuitOpen` Warning alert to Section 16.5 to give operators early visibility before sessions begin timing out.

---

### CRD-005 Three Credential Modes Are Underdocumented for Operators — Interaction Effects Unspecified [High]
**Section:** 4.9

The spec defines three credential modes (pool, user-scoped, fallback chain) and the `credentialPolicy` fields that control them. However, operator documentation has significant gaps:

1. **User-scoped credential storage and encryption are unspecified.** Section 4.9 states "User provides their own credential via MCP elicitation or pre-authorized flow. Token Service stores it." But the storage path (which table, what encryption) is never described. The Token Service uses envelope encryption via KMS for OAuth refresh tokens (Section 4.3), but there is no explicit statement that user-supplied API keys receive the same treatment. Given these are T4 Restricted data, the omission is material.

2. **Pre-authorized flow mechanics are not described.** `userCredentialMode: pre-authorized` appears in the config schema, and the implementation roadmap references it (Phase 11), but the mechanism — how a user pre-authorizes a credential before the session starts, what the API endpoint is, how the credential is bound to a specific user+tenant+provider scope — is entirely absent.

3. **Fallback chain failure modes are underspecified.** The `maxRotationsPerSession: 3` field limits the number of rotations, but there is no description of what happens when the limit is reached (session terminated? fallback chain marked permanently degraded for this session?). The field `requiresRuntimeRestart: false # per-provider; overridden by runtime capability` is confusing — it is unclear whether this is a provider-level or session-level override, and how the gateway resolves conflicts between the policy value and the runtime's actual `credentialCapabilities.hotRotation` declaration.

**Recommendation:**
1. Explicitly state that user-supplied credentials (elicitation and pre-authorized) are stored in the `TokenStore` with the same envelope encryption as OAuth refresh tokens, and are subject to the same KMS rotation procedure.
2. Document the pre-authorized flow: add a `POST /v1/users/me/credentials` or equivalent endpoint to the admin/user API surface, describe the credential binding schema, and specify how stored credentials are matched to session creation requests.
3. Clarify `maxRotationsPerSession` exhaustion behavior (add to the fallback flow steps) and replace `requiresRuntimeRestart: false # per-provider; overridden by runtime capability` with a precise description of precedence: provider-level policy declares the default; the runtime's `credentialCapabilities.hotRotation: true/false` overrides it per session; the gateway resolves at session start.

---

## Medium

### CRD-006 `anthropic_direct` Provider Delivers a Long-Lived Key With a TTL Label — Lease Security Guarantee Is Weaker Than Stated [Medium]
**Section:** 4.9

The spec's design principle states "Runtimes receive short-lived credential leases, never long-lived API keys or root credentials." The credential provider table shows `anthropic_direct` delivers a "Short-lived API key or scoped token." However, Section 4.9 also acknowledges: "For API-key-based providers that do not support short-lived token exchange (e.g., providers where the 'short-lived' key is really just the long-lived key with a TTL wrapper)..."

The Anthropic API does not support short-lived token exchange as of the spec date — Anthropic API keys are long-lived. This means for `anthropic_direct` in direct delivery mode, the runtime receives the actual long-lived API key, not a scoped token. The "short-lived" framing in the provider table is misleading. The lease TTL is a Lenny-layer construct; the underlying key is permanently valid until rotated externally.

This is precisely why proxy mode exists, and the spec recommends it for multi-tenant deployments. But the table implies `anthropic_direct` always delivers a scoped token, which is false for direct mode. Operators reading only the provider table may choose direct mode believing they receive scoped tokens.

**Recommendation:**
1. Update the `anthropic_direct` provider table row to accurately distinguish direct vs. proxy delivery: direct mode delivers the full API key (not scoped); proxy mode ensures the key never enters the pod. Flag `anthropic_direct` direct-mode as equivalent in risk to long-lived key delivery.
2. Strengthen the warning box in Section 4.9: elevate the `direct + standard runc` warning to also cover `anthropic_direct + direct mode` for any isolation level in multi-tenant deployments, not only the runc combination.
3. Consider making proxy mode the hard default for `anthropic_direct` pools with an explicit `deliveryMode: direct` opt-in acknowledgment — analogous to the `allowStandardIsolation: true` pattern.

---

### CRD-007 Credential Pool Exhaustion Alert Is Warning-Only; No Critical Path or Runbook [Medium]
**Section:** 4.9, 16.5

`CredentialPoolLow` fires at Warning when available credentials fall below 20% of pool size. There is no Critical alert for complete credential pool exhaustion — the state where `CREDENTIAL_POOL_EXHAUSTED` is returned to every new session creation attempt. The WarmPoolExhausted alert is Critical (fires if warm pods hit zero for > 60s), but credential exhaustion, which has the same user-visible effect on session creation, gets only a Warning.

Furthermore, the 20% threshold is arbitrary and pool-size-relative: a pool with two credentials fires `CredentialPoolLow` when one is in cooldown. A pool with 100 credentials does not fire until 80 are in cooldown or exhausted. For small pools — common at Tier 1 and Tier 2 — the Warning fires far too late to take remedial action (adding credentials) before new sessions are refused.

The credential pool exhaustion runbook is referenced in Section 4.9 ("Credential pool exhaustion — diagnosis, emergency key addition") but never actually written in the spec.

**Recommendation:**
1. Add a `CredentialPoolExhausted` Critical alert: fires when pool utilization reaches 100% (0 assignable credentials) for any pool for > 30 seconds. This matches the urgency of `WarmPoolExhausted`.
2. Adjust `CredentialPoolLow` to fire at an absolute-count threshold in addition to the percentage threshold: `available_credentials < max(1, pool_size * 0.2)`. For pools with ≤ 5 credentials, fire Warning when any single credential enters cooldown.
3. Write the credential pool exhaustion runbook in the operations section (Section 19.2 / wherever runbooks live): steps to add a credential to a live pool via the admin API without restarting any component, and how to verify the new credential is healthy before retiring an old one.

---

### CRD-008 KMS Availability Is Never Analyzed as a Failure Mode for the Credential Path [Medium]
**Section:** 4.3, 10.5

The spec describes KMS as the backing for Token Service envelope encryption (Section 4.3), JWT signing (Section 10.2), and MinIO SSE (Section 12.5). The KMS rotation procedure (Section 10.5) is documented. However, KMS **availability** as a failure mode is never analyzed:

- If KMS is unreachable, the Token Service cannot decrypt stored OAuth refresh tokens or encrypt new ones. New session credential assignments that require Token Service decryption fail — but the spec does not state whether the Token Service returns a retryable error, whether it has a local key cache, or whether there is a fail-open window.
- The gateway caches active credential leases in memory (Section 4.3), providing a grace period for already-leased sessions. But the Token Service is described as stateless (Section 4.3: "all persistent state lives in Postgres and KMS") — if KMS is unavailable, existing Token Service replicas cannot serve new decrypt requests even if Postgres is available.
- There is no alert for KMS connectivity failure, no circuit breaker behavior described, and no degraded-mode semantics.

**Recommendation:**
1. Add a `KMSConnectivityFailure` Warning/Critical alert (Warning if KMS latency p99 > 500ms; Critical if KMS is unreachable for > 30s).
2. Specify Token Service behavior during KMS unavailability: the Token Service SHOULD cache the most recent decrypted envelope keys in memory for a bounded window (e.g., 5 minutes) to serve decrypt operations from cache while KMS recovers. This is explicitly acceptable because the KEK is already in Token Service memory during normal operation — caching it briefly during KMS outages does not expand the threat model.
3. Document KMS as a hard dependency in the architecture dependency graph, alongside Postgres and Redis, with the same level of HA/failure-mode analysis.

---

### CRD-009 Credential Propagation Through Delegation Chains Has No Scope-Reduction Enforcement [Medium]
**Section:** 4.9, 8.3

Section 8.3 defines three credential propagation modes for child sessions: `inherit`, `independent`, and `deny`. The `inherit` mode means "Child uses the same credential pool/source as parent (gateway assigns from same pool)."

Two gaps exist:

1. **`inherit` does not constrain the child to the parent's specific provider.** A parent using `anthropic_direct` pool with `inherit` propagation could spawn a child that — through its own runtime's `supportedProviders` or `credentialPolicy` — ends up assigned a different provider from the same pool. The spec does not clarify whether `inherit` means "same pool" or "same credential ID" or "same provider type." These have materially different security implications.

2. **Delegation policy allows `credentialPropagation: "independent"` with no constraints on which providers the child can use.** A compromised root session using `independent` could spawn children requesting high-privilege provider credentials (e.g., an IAM role with broad write access) if the pool policy does not restrict it. The delegation policy's `DelegationPolicy` (Section 8.3) does not include a `maxCredentialProviders` or `allowedCredentialPools` field to scope what children can request independently.

**Recommendation:**
1. Define `inherit` precisely: child MUST be assigned from the same `credentialPool` (by pool ID) as the parent. If the parent's credential came from user-scoped source, the child inherits the same user-scoped source. The gateway enforces this at delegation time, not just at lease assignment time.
2. Add an `allowedCredentialPools` field to `DelegationPolicy` to restrict which pools children can request in `independent` mode. Default: same pool as parent. Explicitly allowing any pool requires deployer configuration.
3. Emit an audit event `credential.delegation_propagation` that records the propagation mode and resolved pool for each child credential assignment, enabling lineage tracking for regulated environments.

---

### CRD-010 In-Memory Credential Lease Cache on Gateway Replicas Has No Documented Eviction or Size Bound [Medium]
**Section:** 4.3

Section 4.3 states: "the gateway caches active credential leases in memory. Token Service unavailability does not affect already-leased credentials until the lease expires, providing a grace period proportional to the lease TTL."

No details are given about this cache:

- **No size bound.** At Tier 3 with 10,000 concurrent sessions, each holding a credential lease, the in-memory cache could hold 10,000 lease objects. If lease objects include the materialized credential (API key, STS session), this is a significant in-memory secret store on each gateway replica. There is no `maxSize` or LRU eviction policy documented.
- **No eviction on session end.** It is unclear whether the cache entry is evicted when a session ends and the lease is released (step 23 in Section 7.1). If not, evicted-but-unreleased leases could accumulate.
- **No encryption.** Unlike the Redis token cache (which is explicitly AES-256-GCM encrypted, Section 12.4), there is no statement that the in-memory gateway cache encrypts lease material. A heap dump or memory scrape of a gateway replica would expose all currently cached credential material.
- **Cross-replica risk.** If a gateway replica is compromised, all cached leases on that replica (potentially hundreds) are exposed simultaneously.

**Recommendation:**
1. Bound the in-memory lease cache per gateway replica and document the eviction policy (LRU recommended). At Tier 3, a reasonable bound is `max_concurrent_sessions_per_replica * (average_lease_object_size + overhead)`.
2. Explicitly state that the cache is evicted on session end (step 23, Section 7.1) and on lease release.
3. For proxy mode, the in-memory cache needs to store only the lease token and metadata (not the real API key), as the real key stays in the Token Service. Document this distinction and recommend proxy mode in part because it minimizes the value of gateway memory scraping.
4. Add a note that the gateway replica's in-memory state is a threat-model consideration — the gateway is a higher-value target than agent pods because it caches leases for many sessions simultaneously.

---

## Low

### CRD-011 `credential.rotate` Audit Event Is Missing From the Billing Event Schema [Low]
**Section:** 11.2.1, 12.4

The audit span table (Section 16.3) includes `credential.rotate`, but the billing event schema (Section 11.2.1) lists only `credential.leased` as a credential-related event type. The billing event schema has no `credential.rotate` or `credential.revoke` event type. For regulated environments, the rotation of a credential mid-session is an auditable event (PCI-DSS, SOC 2) — the audit trail should record that a rotation occurred, which credential was replaced, and why (rate-limited, auth-expired, etc.).

The existing `credential.leased` event records `credential_pool_id` and `credential_id`, but a rotation changes the `credential_id` — without a `credential.rotate` event, there is no billing-layer record of which credential was in use during which time window of a session.

**Recommendation:**
Add `credential.rotate` to the EventStore billing event schema (Section 11.2.1) with fields: `previous_credential_id`, `new_credential_id`, `rotation_reason` (rate_limited / auth_expired / provider_unavailable / manual), and `credential_pool_id`. This event does not need to carry materialized credential material — only IDs and the reason code.

---

### CRD-012 Credential Health Scoring Has No Documented Decay Function or Reset Condition [Low]
**Section:** 4.9

Section 4.9 describes credential health scoring: the gateway tracks "recent rate-limit events and cooldown expiry, auth failure count, concurrent session count, spend tracking." Assignment strategies use this health data to avoid degraded credentials.

However, there is no specification of:
- How the health score is computed from these inputs (formula or algorithm).
- Whether auth failure count decays over time or is reset only on successful use.
- Whether a credential can recover from a `revoked`/`degraded` state back to `healthy` without operator intervention, and if so, when.
- How health scores are persisted (in `CredentialPoolStore`? in Redis for fast access?).

Without a defined decay function, a credential that experiences a transient auth failure spike (e.g., during an Anthropic API incident) could remain deprioritized permanently, reducing effective pool size over time.

**Recommendation:**
1. Specify the health score formula or at minimum the inputs and their weights (e.g., exponential decay on auth failure count with half-life of 15 minutes).
2. Document the recovery path: a credential transitions from `degraded` to `healthy` after N consecutive successful assignments and zero auth failures within a sliding window of M minutes.
3. Emit a `credential_health_change` metric event (label: `pool`, `credential_id`, `previous_state`, `new_state`) so operators can observe health transitions.

---

### CRD-013 Lease TTL Value Is Never Specified Anywhere in the Spec [Low]
**Section:** 4.9

The credential lease object includes `expiresAt` and `renewBefore` fields (shown in the example JSON, Section 4.9), and the data classification table says the retention default for credential leases is "24 hours" (Section 12.6). However, the actual lease TTL — the duration from assignment to expiry — is never specified. The example shows a 5-minute duration (`expiresAt` minus `renewBefore` = 5 minutes before expiry), but this is an illustrative example, not a specification.

For operators sizing warm pools and planning for rotation events: if the lease TTL is shorter than the maximum session age (7200s = 2h default), credential rotation will be triggered during long sessions. If lease TTL is 1 hour, every session longer than 1 hour undergoes at least one rotation — this has implications for Minimum/Standard-tier sessions where rotation requires a pod restart (Section 4.7 tier table), causing a visible interruption.

**Recommendation:**
1. Define the default lease TTL (suggested: match session max age of 7200s to avoid mandatory rotation during normal sessions).
2. Make the TTL configurable per pool via the `credentialPool` config schema.
3. Add a note to the tier rotation table (Section 4.7) that Standard/Minimum-tier runtimes with short lease TTLs relative to session age will experience periodic pod restarts — operators should size lease TTL to be at least as long as their expected session duration, or use Full-tier runtimes for long-running sessions.

---

## Info

### CRD-014 `lenny-preflight` Validation Job Should Check Credential Pool Reachability [Info]
**Section:** 17.6, 4.9

The `lenny-preflight` Job (Section 17.6) validates RuntimeClass existence and other infrastructure dependencies before installation. Credential pool health — specifically whether the Token Service can reach KMS and successfully decrypt a test envelope — is not listed as a preflight check. A deployment that passes preflight but has misconfigured KMS access will fail silently at first session creation.

**Recommendation:** Add to `lenny-preflight`: (1) Token Service KMS connectivity check (encrypt and decrypt a test value using the configured KMS key); (2) credential pool validation (for each registered pool, attempt to materialize a test lease and immediately revoke it); (3) alert if any credential in a pool is in `revoked` or `permanently_degraded` state at startup.

---

### CRD-015 Semantic Cache May Expose Credential-Correlated Request Patterns [Info]
**Section:** 4.9

The optional `SemanticCache` on `CredentialPool` caches query/response pairs. If semantic cache keys are derived from request content without credential scoping, a response cached by a session using user-scoped credential A could be served to a session using pool credential B — potentially leaking that user A queried a particular topic. More critically, if cache entries are scoped by pool rather than by user+tenant+session, sessions from different tenants sharing a pool could observe cache hits that reveal other tenants' query patterns.

The spec states the cache is "scoped to the user" in Section 12.7 (user data schema), but Section 4.9 places the `cachePolicy` on the `CredentialPool`, suggesting pool-level scoping. These two descriptions are in tension.

**Recommendation:** Clarify the isolation boundary for semantic cache entries: they MUST be scoped by `tenant_id + user_id` at minimum, never by pool alone. Add this as an enforcement requirement on the `SemanticCache` interface contract in Section 4.9, alongside the note that the cache is disabled by default.
