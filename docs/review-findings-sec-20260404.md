# Security & Threat Model Review Findings — 2026-04-04

**Document reviewed:** `docs/technical-design.md` (5,277 lines)
**Perspective:** 2. Security & Threat Modeling
**Category code:** SEC
**Reviewer focus:** Attack surfaces, trust boundaries, isolation guarantees, defense-in-depth, with mandatory checks on SIGSTOP/SIGCONT checkpointing, adapter-agent boundary, prompt injection, upload safety, and isolation monotonicity.

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 2 |
| High | 6 |
| Medium | 9 |
| Low | 4 |
| Info | 2 |

**Note on prior findings:** SEC-001 through SEC-019 appear in `review-findings-20260404.md` and cover the same perspective. Several of those have been marked FIXED. This document provides a fresh, self-contained threat-model pass against the current spec text, with new finding numbers to avoid collisions. Where a prior finding remains open and materially unfixed, it is re-raised with a cross-reference.

---

## Critical

### SEC-101 LLM Proxy Internal Endpoint Uses HTTP Not HTTPS [Critical]
**Section:** 4.9

The spec shows the LLM proxy endpoint as `http://gateway-internal:8443/llm-proxy/{lease_id}` and the pool configuration as `proxyEndpoint: http://gateway-internal:8443/llm-proxy`. Port 8443 is conventionally HTTPS, but the URI scheme in the spec is `http://`. If taken literally, agent pods transmit the lease token in cleartext over the pod-to-gateway segment. Even inside the cluster, a compromised sidecar, CNI plugin, or network tap can capture the lease token. The lease token authenticates the pod to the proxy and implicitly authorizes injection of the real API key into upstream requests — its compromise is equivalent to credential theft.

The same section says "the real API key never enters the pod" as the security justification for proxy mode, but this guarantee is defeated if the token that substitutes for it travels unencrypted.

Furthermore, there is no statement that the proxy endpoint validates the pod's SPIFFE identity via mTLS, so a token captured from one pod could be replayed from any other network location inside the cluster.

**Recommendation:**
1. Change all example `proxyEndpoint` URIs in Section 4.9 to `https://` (or better, specify they must use the same mTLS channel as the adapter↔gateway control plane).
2. Require the LLM Proxy subsystem to enforce mTLS on incoming pod connections, validating the pod's SPIFFE certificate. Bind each lease token to the pod's SPIFFE URI at issuance time, and reject requests where the token's bound identity does not match the presenting certificate. This prevents token replay across pods.
3. Add a preflight check in the `lenny-preflight` Job (Section 17.6) that validates `proxyEndpoint` scheme is `https://` and rejects `http://` in non-dev-mode deployments.

---

### SEC-102 Isolation Monotonicity Enforcement Has No Admission-Time Gate [Critical]
**Section:** 8.3, 5.3, 17.2

Section 8.3 states: "Children must use an isolation profile at least as restrictive as their parent. The `minIsolationProfile` field in the lease enforces this, and the gateway validates it before approving any delegation." This is the only stated enforcement point — a runtime check inside the gateway delegation path.

However, there is no admission-time or pool-definition-time control that prevents a deployer from registering a pool combination that would permit a `standard` (runc) child to be spawned by a `sandboxed` (gVisor) parent. The gateway's runtime check is correct, but the failure mode is a runtime rejection experienced by the user after pod allocation has been attempted (wasting a pod claim) rather than a declarative impossibility.

More critically, there is no mechanism to prevent a `delegationPolicyRef` from targeting a pool whose isolation profile is weaker than the parent's `minIsolationProfile`. The `DelegationPolicy` uses tag-based matching (`matchLabels`) against runtime labels, and pool labels are not required to encode the isolation profile. A policy author could inadvertently or maliciously construct a policy that matches runtimes with weaker isolation than the parent.

The spec explicitly states that `minIsolationProfile` is **not extendable** via lease extension (Section 8.6), which is correct. But the pre-delegation validation relies entirely on the gateway correctly computing the effective `minIsolationProfile` from the lease, which is in-memory state derived from session creation — there is no authoritative check against the actual `RuntimeClass` of the allocated pod.

**Recommendation:**
1. Require that `DelegationPolicy` rules specify a `minIsolationProfile` constraint as part of target matching — delegation rules that do not constrain isolation should default to "at least as restrictive as current session."
2. Add a `SandboxTemplate` admission webhook that rejects pool definitions whose `isolationProfile` is weaker than what any `DelegationPolicy` referencing them would permit, evaluated at policy-registration time.
3. At delegation time, verify isolation monotonicity against the **actual `RuntimeClass`** of the target pool's `SandboxTemplate`, not only against the computed lease field.
4. Emit a `IsolationMonotonicityViolationAttempt` audit event with Critical severity whenever the gateway rejects a delegation for isolation violation, so that misconfigured or adversarial delegation attempts are visible in the audit trail.

---

## High

### SEC-103 SIGSTOP/SIGCONT Embedded-Adapter Path Still Has a Deadlock Window [High]
**Section:** 4.4

The prior finding SEC-001 is marked FIXED. The spec now correctly restricts `SIGSTOP`/`SIGCONT` to the embedded adapter mode only, and documents a 60-second watchdog timer and a `defer`-based `SIGCONT` to handle adapter crashes. However, a specific residual gap remains.

The spec states: "If the embedded adapter process itself crashes while the agent is SIGSTOPped, the agent process remains stopped; the pod's liveness probe will fail (since the adapter is the probe target), Kubernetes will restart the pod."

In the sidecar model, Kubernetes restarts the **container**, not the pod, by default. In the embedded model (single process), a crash of the adapter goroutine that holds the `SIGSTOP` state may not kill the entire process — in Go, a panic in a goroutine that is recovered (e.g., by a framework) does not kill the process. If the watchdog goroutine also panics or is blocked, `SIGCONT` may never be sent, and the liveness probe may not fail because the adapter's main HTTP server is still running. The agent process is then permanently frozen with no recovery path until the pod's `maxSessionAge` causes an external termination.

Additionally, the spec relies on the liveness probe targeting the adapter. If the adapter is the probe target but is running (serving `/healthz`) while the agent is frozen, the probe passes indefinitely. Only if the adapter itself crashes does the probe fail.

**Recommendation:**
1. The embedded adapter's watchdog MUST actively report to the liveness probe that a `SIGSTOP` has been held for more than `watchdogTimeoutSeconds` (default 60s) without checkpoint completion. This requires the liveness endpoint to expose a "checkpoint_stuck" unhealthy state.
2. Document that any Go `recover()` call wrapping the checkpoint goroutine must re-panic or explicitly signal the liveness endpoint as unhealthy if `SIGCONT` cannot be sent.
3. Add an integration test simulating adapter goroutine panic during SIGSTOP hold to verify the liveness probe fails and Kubernetes restarts the pod.

---

### SEC-104 Adapter-Agent Boundary: `SO_PEERCRED` Check Is Insufficient for UID Spoofing in Rootless Containers [High]
**Section:** 4.7 (Adapter-Agent Security Boundary)

The spec states: "Abstract Unix sockets use `SO_PEERCRED` for peer UID verification — the adapter accepts connections only from the expected agent UID."

`SO_PEERCRED` returns the effective UID of the connecting process as seen by the kernel. This is a reliable control in standard containers. However, in user-namespace-based isolation (e.g., rootless Podman, or gVisor's user namespace mapping), the UID reported by `SO_PEERCRED` is the UID in the **container's user namespace**, which may map to UID 0 (root) in the container regardless of the host UID. If the agent binary is running as UID 1001 in the container but the user namespace maps host UID X to container UID 0, a malicious binary running as container root can connect to the abstract socket and pass the `SO_PEERCRED` check if the expected UID check is not namespace-aware.

Under gVisor specifically, `SO_PEERCRED` semantics are implemented in gVisor's userspace kernel. The behavior may differ from the host kernel. The spec does not validate whether `SO_PEERCRED` functions correctly under gVisor's abstract Unix socket implementation.

**Recommendation:**
1. Validate `SO_PEERCRED` behavior under gVisor in the Phase 3.5 security integration tests. If behavior differs, add a compensating control (e.g., a challenge-response at connection time using a nonce written to the manifest file that only the adapter can have seen).
2. Document that the expected agent UID must be the UID within the container's user namespace, and that the adapter must validate this against the pod spec rather than assuming a fixed UID mapping.
3. Consider adding a connection-time HMAC-based handshake where the agent proves it read the manifest (which is read-only to the agent container) as an additional proof of identity, independent of `SO_PEERCRED`.

---

### SEC-105 Prompt Injection via Elicitation Responses in Delegation Chains [High]
**Section:** 9.2, 13.5

Section 13.5 addresses prompt injection via `TaskSpec.input` in `delegate_task`. However, it does not address prompt injection through the elicitation response path.

The elicitation chain (Section 9.2) flows: External Tool → Gateway connector → Child pod → Parent pod → Gateway edge → Client/Human. The response flows back down the same chain. A malicious or compromised external connector could craft an elicitation **response** (flowing downward) containing adversarial content that is delivered directly to a child pod's context as a `lenny/request_input` reply. This content bypasses the `PreDelegation` interceptor because elicitation responses are not task delegations — they are replies to pending tool calls inside a session.

Similarly, the `lenny/send_message` path (Section 7.2) delivers content to a running session's stdin with no content scanning, even when originating from a sibling session (under `siblings` scope). A compromised sibling can inject arbitrary content into another session's reasoning context.

The `contentPolicy.interceptorRef` hook is only on `DelegationPolicy` at the `PreDelegation` phase. No equivalent hook exists for elicitation responses or inter-session messages.

**Recommendation:**
1. Add a `PostElicitationResponse` interceptor phase to the `RequestInterceptor` chain (Section 4.8) that fires when an elicitation response is delivered down the chain to a pod. The interceptor receives the response content and can ALLOW, REJECT, or MODIFY it before the pod receives it.
2. Add a `PreMessageDelivery` interceptor phase that fires before `lenny/send_message` or `lenny/send_to_child` content is written to a session's stdin. Apply `contentPolicy.maxInputSize` to inter-session messages, not only to delegation inputs.
3. Document this residual attack surface explicitly in Section 22.3 (Explicit Non-Decisions on Guardrails), so deployers understand that elicitation responses and inter-session messages are unscanned injection vectors.

---

### SEC-106 Upload Archive Extraction: Zip Bomb and Decompression Ratio Not Bounded [High]
**Section:** 7.4

Section 7.4 documents zip-slip protection, symlink rejection, and that "total extracted size is checked against the per-session upload limit." However, it does not address **decompression ratio** attacks (zip bombs).

A malicious archive can have a compressed size of a few KB but decompress to hundreds of GB. The spec says extraction aborts if the limit is exceeded — but for streaming extractors (which are standard for `tar.gz` and `zip`), the gateway must decompress data to check its size. If the limit is only checked after extraction of each file, a sufficiently large decompression can exhaust gateway memory before the check fires.

Separately, the spec says "extraction aborts immediately if the limit is exceeded (no 'extract then check')." This is good, but it requires that the extraction library can track running uncompressed byte counts during streaming extraction and abort mid-stream. Not all Go archive libraries support this correctly. The spec does not name the library or document how this is validated.

**Recommendation:**
1. Enforce a **decompression ratio limit** (e.g., compressed-to-uncompressed ratio > 100:1 is rejected). Track cumulative compressed bytes read vs. cumulative uncompressed bytes written during extraction, and abort if the ratio exceeds the configured maximum.
2. Bound the **uncompressed bytes read per I/O operation** to prevent a single `Read()` call from allocating unbounded memory. Use an `io.LimitedReader` wrapping the decompressor with a per-call cap.
3. Name the archive extraction library in the spec and add integration tests with known zip bomb payloads (e.g., a 42 KB "42.zip" bomb expanding to 4.5 PB) to verify abort behavior.
4. Add `lenny_upload_extraction_aborted_total{reason}` (counter, labeled by `zip_bomb` / `size_limit` / `path_traversal`) to the metrics inventory (Section 16.1).

---

### SEC-107 `callbackUrl` SSRF: DNS Pinning Does Not Prevent Cloud Metadata Endpoint Access [High]
**Section:** 14

The spec documents DNS pinning and private IP range rejection for `callbackUrl`. However, cloud metadata endpoints (e.g., AWS `169.254.169.254`, GCP `metadata.google.internal` → `169.254.169.254`, Azure `169.254.169.254`) are link-local addresses in the `169.254.0.0/16` range. The spec mentions "link-local addresses" are rejected, which would cover the AWS metadata IP directly.

However, `metadata.google.internal` resolves to `169.254.169.254` inside GCP but the DNS resolution happens at callback time, and the spec's DNS pinning resolves at registration time. A GCP deployment where the gateway itself resolves `metadata.google.internal` to an internal alias that is **not** `169.254.169.254` (as is possible with some GCP network configurations where metadata is served via a different internal IP) could bypass the link-local check at registration time, then rebind at callback time.

Additionally, the spec does not document whether the callback worker's `http.Client` enforces the pinned IP against the `Host` header or uses the pinned IP as the actual connection target. If the `Host` header is set to the original hostname and the connection target is the pinned IP, the behavior depends on the upstream server — a server with virtual hosting could serve different content for the same IP based on `Host`.

**Recommendation:**
1. Add explicit enumeration of cloud metadata endpoint ranges to the blocklist: `169.254.0.0/16` (link-local, covers AWS/GCP/Azure metadata), `100.64.0.0/10` (Carrier-grade NAT, covers RFC 6598 shared addresses), and the cloud-provider-specific metadata hostnames (`metadata.google.internal`, `instance-data`, `169.254.169.254`).
2. Enforce the pinned IP at the TCP connect level (via a custom dialer), not via `Host` header matching. The `http.Client.Transport` must use a custom dialer that always connects to the pinned IP regardless of the URL hostname.
3. Add an integration test that attempts to register a `callbackUrl` resolving to each of the metadata ranges and verifies rejection.

---

### SEC-108 Semantic Cache Poisoning via Shared Credential Pool Without Tenant Key Isolation [High]
**Section:** 4.9

The `SemanticCache` interface is backed by Redis by default. Section 12.4 states all Redis keys must use `t:{tenant_id}:` prefix, and Section 12.4 says tenant key isolation is enforced at the Redis wrapper layer. However, Section 4.9's `CachePolicy` schema does not include a `tenantId` dimension, and the semantic cache stores query/response pairs keyed on semantic similarity (embedding vectors).

If two tenants issue semantically similar LLM queries and share the same credential pool (which is explicitly supported — "Pool mode: shared team/org API keys"), their queries may produce cache hits against each other's cached responses. The cache key must encode `(tenant_id, query_embedding, model, provider)` at minimum. If the cache key is only `(query_embedding, model, provider)`, a cached response from tenant A can be returned to tenant B.

This is a data leakage risk: tenant B receives tenant A's LLM response, which may contain tenant A's proprietary data, code, or credentials that appeared in the prompt.

**Recommendation:**
1. Mandate that `tenant_id` is a non-optional dimension of the semantic cache key. Document this in the `CachePolicy` schema and the `SemanticCache` interface contract.
2. Add to Section 4.9: "Semantic cache implementations MUST scope cache lookups and writes to the `tenant_id` of the current session. Cross-tenant cache hits are forbidden regardless of semantic similarity."
3. Add an integration test that writes a cached response for tenant A and verifies it is not returned for an identical query from tenant B.
4. For `user`-scoped credential mode where the credential is user-specific, the cache key should additionally include `user_id`, as the response may reflect user-specific context.

---

## Medium

### SEC-109 Task-Mode Workspace Scrub Does Not Restart Runtime Process [Medium]
**Section:** 5.2

The Lenny scrub procedure (Section 5.2) kills all user processes and removes the workspace directory. However, the runtime adapter (sidecar) and agent binary are not restarted between tasks in task mode — only the workspace is cleared. A runtime that maintains in-process state (e.g., Python `sys.modules` caching, JVM class loaders, LLM context windows carried in heap) can leak state between sequential tasks even after the workspace scrub.

Specifically, LLM runtimes (the primary use case) typically maintain a context window in memory. If the task-mode agent binary does not explicitly clear its context between tasks, the second task's agent may have access to the first task's conversation history, including any workspace file content that was loaded into the LLM context.

The spec acknowledges "best-effort, not a security boundary" but does not mandate runtime process restart between tasks, even as an option.

**Recommendation:**
1. Add a `requiresProcessRestart: true` option to `taskPolicy`. When set, the adapter sends `{type: "shutdown"}` to the agent binary after `cleanupCommands` and before the Lenny scrub, then restarts a fresh agent binary process for the next task. This eliminates in-process state leakage.
2. Document that task mode with `requiresProcessRestart: false` (the current default) does not protect against in-process state leakage from LLM context windows or application-layer caches.
3. For multi-tenant task mode (which requires `microvm` isolation), `requiresProcessRestart: true` should be the enforced default, since the VM boundary handles memory isolation but the agent binary's own context window remains a leakage vector.

---

### SEC-110 `runtimeOptions` JSON Schema Validation Gaps [Medium]
**Section:** 14

The spec states: "If the target Runtime defines a `runtimeOptionsSchema` (a JSON Schema document), the gateway validates `runtimeOptions` against it at session creation time." If no schema is registered, options are passed through with only a warning and a 64KB size limit.

Two gaps:
1. JSON Schema validation does not bound structural complexity. A malicious client can submit `runtimeOptions` with deeply nested JSON (depth 100+) or arrays of 10,000 elements that exhaust gateway parser memory/CPU even within 64KB.
2. The `runtimeOptionsSchema` itself is stored and served without validation. A malicious admin could register a schema with a `$ref` pointing to an external URL (`http://attacker.com/schema.json`), causing the gateway to make outbound requests when validating `runtimeOptions`.

**Recommendation:**
1. Enforce structural limits on `runtimeOptions` parsing regardless of schema: maximum nesting depth (e.g., 10 levels), maximum array length (e.g., 100 elements), maximum string length per field (e.g., 4KB). Apply these before JSON Schema validation.
2. Reject `runtimeOptionsSchema` documents that contain `$ref` with non-`#/` (non-local) URIs at registration time. Only allow inline schema definitions with local `$ref`.
3. In multi-tenant mode, make `runtimeOptionsSchema` mandatory for any runtime with `type: agent` that is exposed to non-admin callers. Document this as a security recommendation in Section 5.1.

---

### SEC-111 elicitation Depth Policy Does Not Cover OAuth Flows at Arbitrary Depth [Medium]
**Section:** 9.2

Section 9.2 states: "OAuth flows initiated by gateway-registered connectors are exempt from suppression at any depth, provided the connector is authorized by the session's effective `DelegationPolicy`."

This means a connector authorized at depth 1 can trigger OAuth flows at depth 5 if the child at depth 5 is also authorized for the connector. The authorization check is per-session (`connector_id` in effective delegation policy), not depth-aware. A compromised or manipulated child session at depth 5 that is authorized for a connector can initiate a URL-mode OAuth elicitation that surfaces to the human user, potentially phishing for credentials under the appearance of a legitimate OAuth flow.

The `initiator_type: connector` provenance metadata would be accurate (it is gateway-initiated), making it indistinguishable from a legitimate OAuth flow at depth 1.

**Recommendation:**
1. Add `maxConnectorElicitationDepth` to `DelegationPolicy` or the pool's `elicitationDepthPolicy`. Default: `2` (connector OAuth allowed only at delegation depths 0 and 1). Children below this depth cannot trigger OAuth flows even for authorized connectors.
2. Include `delegation_depth` prominently in the elicitation UI provenance display (Section 9.2 already lists it in the provenance metadata table, but the spec should recommend that UIs render deep-delegation OAuth flows with a distinct warning, e.g., "This OAuth request comes from a sub-agent 5 levels deep").
3. Add an audit event `elicitation.oauth.deep_delegation` (Warning) when a connector OAuth flow is triggered at depth ≥ `maxConnectorElicitationDepth` + 1, even if allowed.

---

### SEC-112 Redis Fail-Open Rate Limiting Exploitable via Targeted Redis Disruption [Medium]
**Section:** 12.4

The spec acknowledges the fail-open risk: "a single user sending unlimited requests through one replica during the fail-open window." The emergency hard limit is per-replica and not shared across replicas (`N * per_replica_limit` effective). At Tier 2 with 10 gateway replicas and a 60-second fail-open window, a single user effectively gets a 10x rate limit multiplier during the window.

An attacker with the ability to disrupt Redis (e.g., through a `FLUSHALL` command if Redis ACLs are misconfigured, or via network disruption) can repeatedly trigger the fail-open window. With a 300-second cumulative timer (`quotaFailOpenCumulativeMaxSeconds`) before fail-closed kicks in, an attacker can sustain the exploit for 5 minutes per hour.

Furthermore, during the fail-open window, the per-replica in-memory counters are not shared. If the attacker maintains multiple sessions across different replicas, each replica's counter is independent — the total effective throughput is `N * per_replica_limit`.

**Recommendation:**
1. Reduce the default fail-open window to 30 seconds for Tier 1/2 (currently 60s) and 15 seconds for Tier 3 (per Section 17.8 for Tier 3, which already proposes 30s). Shorter windows reduce attacker dwell time.
2. When Redis recovers, immediately reconcile the in-memory per-replica counters by querying Postgres for actual session/token usage. Do not wait for the next quota sync interval.
3. Add a circuit-breaker pattern: if Redis becomes unavailable more than N times within a configurable window (e.g., 3 times in 10 minutes), the gateway switches to fail-closed immediately and requires operator intervention to reset. Log `redis_repeated_unavailability` at Critical level.
4. Document Redis ACL configuration requirements: the gateway's Redis user must NOT have `FLUSHALL` or `FLUSHDB` permissions. Add this as a preflight check.

---

### SEC-113 Credential Deny-List After Pod Termination Has No Persistence Across Gateway Restarts [Medium]
**Section:** 10.3

The spec states: "The deny list is propagated across gateway replicas via Redis pub/sub (with Postgres `LISTEN/NOTIFY` as fallback). Entries are ephemeral — each entry expires when the certificate's natural TTL lapses (at most 4h)."

"Ephemeral" means the deny list is not persisted to disk or database. If all gateway replicas restart simultaneously (e.g., during a rolling update or cluster incident), the deny list is lost. A pod that was terminated for security reasons (e.g., anomalous behavior) would have its certificate accepted again by new gateway replicas until the certificate's 4h TTL expires, providing up to 4 hours of re-access.

For security-motivated terminations (malicious runtime, credential theft), this represents an up-to-4h window where a compromised pod could reconnect to a new gateway replica and resume access.

**Recommendation:**
1. Persist security-motivated deny-list entries to Postgres (`certificate_deny_list` table, keyed by SPIFFE URI and certificate serial, with `expires_at` column matching the certificate TTL). This is a small, bounded table.
2. On gateway startup, load all non-expired deny-list entries from Postgres into the in-memory deny list before accepting any pod connections.
3. Differentiate between "normal termination" (pod released after session end — no deny-list entry needed) and "security termination" (anomalous behavior — deny-list entry persisted to Postgres). Add a `terminationReason` enum to the pod termination flow.
4. Add `lenny_certificate_deny_list_active_entries` (gauge) to Section 16.1 metrics.

---

### SEC-114 `publishedMetadata` Content-Type Sniffing Enables MIME Confusion Attack [Medium]
**Section:** 5.1

The spec states: "Gateway treats content as opaque pass-through — stores and serves without parsing or validating." The visibility levels (`internal`, `tenant`, `public`) control access but not content handling.

A runtime author could publish metadata with `contentType: text/html` and `visibility: public`. When served at `GET /v1/runtimes/{name}/meta/{key}`, browsers will render the response as HTML, enabling stored XSS — an attacker registers a runtime, publishes HTML/JavaScript metadata, and any browser that visits the public endpoint executes the script. The public endpoint requires no authentication.

The prior finding SEC-017 raised this issue as a Low severity and recommended `Content-Disposition: attachment`. The spec has not addressed this.

**Recommendation:**
1. The gateway must override the stored `contentType` on all `/v1/runtimes/{name}/meta/{key}` responses with `Content-Type: application/octet-stream` plus `X-Content-Type-Options: nosniff` and `Content-Disposition: attachment; filename="metadata"`. This forces browser download rather than rendering.
2. For the internal and tenant endpoints (`/internal/runtimes/{name}/meta/{key}`), apply the same override. Internal endpoints may be accessed by automation but should not be HTML-rendered.
3. Add a blocklist of dangerous `contentType` values at registration time (`text/html`, `application/javascript`, `text/javascript`, `application/xhtml+xml`) and reject metadata with these types at the admin API level.
4. Validate maximum size of `publishedMetadata` values at registration time (suggest 1MB limit per key).

---

### SEC-115 Session Generation Counter Does Not Cover Out-of-Band Pod Restart [Medium]
**Section:** 10.1

The `coordination_generation` counter prevents split-brain between gateway replicas. However, the generation is incremented by the gateway when it acquires coordination. It is **not** incremented when a pod restarts unexpectedly (e.g., OOMKilled, runtime crash, or Kubernetes preemption).

If a pod restarts and a stale gateway replica sends a `CoordinatorFence` with an old generation to the newly started pod, the pod will record that generation as authoritative. The legitimate current coordinator's RPCs (with a higher generation, set before the pod restart) would be rejected by the pod because the pod's recorded generation was reset to the stale coordinator's value.

This race is possible when: (1) the current coordinator's lease expires while the pod is restarting, (2) a stale replica re-acquires the lease (with the old generation stamp, which hasn't been incremented by Postgres yet since no replica performed `CoordinatorFence`), and (3) the restarted pod accepts the stale replica's fence.

**Recommendation:**
1. When a pod restarts (detected via the adapter's INIT→READY transition), it must generate a fresh pod-side nonce and send it to the gateway during the READY handshake. The current coordinator must acknowledge this nonce. Only the coordinator that acknowledged the nonce can send RPCs; other replicas must re-fence after detecting the pod restart.
2. Alternatively, the pod should record the highest generation it has ever been fenced with (persisted to its tmpfs manifest volume) and reject any `CoordinatorFence` with a generation lower than or equal to the stored maximum, even after restart.
3. Add an integration test simulating simultaneous pod restart and gateway replica failover to verify generation monotonicity.

---

## Low

### SEC-116 Audit Log Hash Chain Uses SHA-256 of Mutable Fields [Low]
**Section:** 11.7

The spec states the hash chain uses: `SHA-256(id, prev_hash, tenant_id, event_type, payload, created_at)`. The `payload` field contains the audit event content, which is correct. However, `created_at` is listed as a field in the hash input, which is a server-generated UTC timestamp.

If the timestamp is generated by the application rather than by the database, there is a window where two gateway replicas generating events simultaneously could produce timestamps in different orders, breaking the assumption that `sequence_number` order and `created_at` order are consistent. A hash chain over fields that include a potentially inconsistent timestamp creates verification difficulties.

Additionally, the spec says "The first entry in each tenant partition uses a well-known genesis hash" but does not specify what the genesis hash is, how it is distributed, or how verifiers can obtain it. Without a published genesis hash, the chain's tamper-evidence depends entirely on the genesis value being known and consistent across all replicas.

**Recommendation:**
1. Generate `created_at` via `NOW()` inside the database transaction (Postgres `CURRENT_TIMESTAMP`), not at the application layer. This ensures `created_at` is consistent with the insertion order and cannot be manipulated by application-layer clock skew.
2. Define the genesis hash as a deployment-specific constant derived from the deployment's cluster name and initial deployment timestamp, stored in the Helm values and published to the SIEM at deployment time. Document the genesis hash derivation in the operational runbooks.
3. Consider using `sequence_number` (already guaranteed monotonic per tenant) instead of `created_at` in the hash input to eliminate timestamp ambiguity.

---

### SEC-117 SSE Buffer Exhaustion Has No Per-User Stream Limit [Low]
**Section:** 7.2

The spec documents an SSE buffer of 1,000 events or 10MB per client, and drops the connection if the buffer fills. However, it does not limit the number of concurrent SSE streams per user or per session. A single user can open hundreds of concurrent `AttachSession` streams, exhausting gateway goroutines and file descriptors even if each individual stream is within limits.

The prior finding SEC-018 raised this as Low and recommended a limit of 5 concurrent streams per user. The spec still does not address this.

**Recommendation:**
1. Enforce a `maxConcurrentStreamsPerUser` limit (default: 5) and a `maxConcurrentStreamsPerSession` limit (default: 3 — to allow reconnect overlap). Return `429 RATE_LIMITED` when the limit is exceeded.
2. Add `lenny_gateway_active_streams_per_user` (gauge) to the metrics inventory, enabling per-user stream exhaustion detection.
3. Apply the per-user stream limit at the goroutine pool level (before spawning the stream handler goroutine), not after, to prevent goroutine exhaustion under attack.

---

### SEC-118 `allowSymlinks: true` Runtime Option Has No Path Verification After Extraction [Low]
**Section:** 7.4

The spec states: "Symlinks within archives are rejected by default. A `allowSymlinks: true` option can be set per Runtime for runtimes that require them, but even then symlinks must resolve within the workspace root."

However, the verification described is at **extraction time** against the staging directory. After promotion from staging to `/workspace/current`, symlinks that were valid at extraction time (pointing within `/workspace/staging/...`) would resolve to paths that no longer exist in `/workspace/current/...` unless the symlink targets are relative (not absolute). If absolute symlinks were allowed at extraction time (pointing to `/workspace/staging/foo.txt`), they become dangling or workspace-escaping after promotion.

Additionally, the spec does not address **symlink chains** (a symlink pointing to another symlink within the archive). A chain `A → B → /etc/passwd` where `B` is itself a symlink extracted first could bypass a per-symlink check that validates only the immediate target.

**Recommendation:**
1. When `allowSymlinks: true`, after extraction to staging and before promotion, re-resolve all symlinks in the staging directory using `filepath.EvalSymlinks()` and verify that every resolved path is within the staging root. Reject the entire archive if any symlink resolves outside.
2. After promotion to `/workspace/current`, perform a second symlink resolution pass using the new root to catch any absolute-path symlinks that become workspace-escaping after the path changes.
3. Enforce a maximum symlink chain depth (e.g., 5 levels) during extraction to prevent symlink loop DoS.

---

### SEC-119 Webhook HMAC Secret Has No Minimum Entropy Requirement [Low]
**Section:** 14

The `callbackSecret` is provided by the client at session creation and stored encrypted. The spec documents that webhooks are signed with HMAC-SHA256, but does not specify minimum entropy for `callbackSecret`. A client that provides a short or predictable secret (e.g., "password") would produce HMAC signatures that an attacker can brute-force.

The spec also does not specify whether `callbackSecret` is used directly as the HMAC key or is passed through a KDF first. Using it directly means short secrets provide proportionally weak security. The HMAC-SHA256 authentication claim is undermined if the key can be brute-forced.

**Recommendation:**
1. Require `callbackSecret` to be at least 32 bytes of cryptographically random data (256 bits). Enforce this at session creation time with a `VALIDATION_ERROR` for secrets below this length.
2. Alternatively, have the gateway generate the `callbackSecret` server-side (returning it to the client once at session creation, never storing it in plaintext) and refuse client-provided secrets. This eliminates weak-secret risk entirely.
3. Pass `callbackSecret` through HKDF with a `lenny-webhook-hmac` info label before using it as the HMAC key, so that even if clients reuse the same secret for multiple purposes, the derived key is unique to webhook signing.

---

## Info

### SEC-120 Trust Boundary Documentation Missing for Gateway Internal Subsystems [Info]
**Section:** 4.1

The gateway binary is internally partitioned into four subsystems (Stream Proxy, Upload Handler, MCP Fabric, LLM Proxy) with per-subsystem goroutine pools and circuit breakers. These are documented as "Go interfaces within a single binary" rather than separate processes.

Because all subsystems share the same process memory and credentials, a memory corruption vulnerability in one subsystem (e.g., a malformed archive causing a heap overflow in the Upload Handler) could affect all other subsystems. The in-process boundary provides isolation against CPU starvation and circuit-breaker isolation, but not memory safety isolation.

This is an informational observation — the design correctly documents these as performance and availability boundaries, not security boundaries. However, the spec should explicitly state this to prevent readers from assuming in-process subsystem boundaries provide security isolation.

**Recommendation:**
Document in Section 4.1: "These subsystem boundaries provide availability isolation (goroutine pool limits, circuit breakers) and not security isolation. A memory safety vulnerability in any subsystem can affect all subsystems in the same process. If a higher security boundary between subsystems is required, extract the relevant subsystem(s) to separate processes (Section 4.1 extraction triggers apply)."

---

### SEC-121 `lenny/get_task_tree` Exposes Sibling Session Metadata to Any Session in the Tree [Info]
**Section:** 8.5

The `lenny/get_task_tree()` tool returns the full task hierarchy including states and `runtimeRef` for all nodes. The spec notes: "A child session can discover its siblings (other children of its parent) by inspecting the tree." Under `siblings` messaging scope, this enables sibling-to-sibling communication.

This also means a child session at depth 1 can observe the runtime type, state, and task metadata of all sibling sessions, including those belonging to concurrent users if the same parent delegates to runtimes serving different users. While the task tree is logically owned by a single session (and `tenant_id` isolation applies), the exposure of sibling runtime types and states may leak information about what tasks the orchestrating parent is running in parallel.

**Recommendation:**
1. Filter `lenny/get_task_tree()` results to include only nodes that the calling session has a direct relationship with (direct parent, direct children, and siblings if `messagingScope: siblings` is configured). Do not expose the full tree recursively to leaf nodes.
2. In the `TaskTreeNode` response, omit `runtimeRef` for nodes that are not direct ancestors or descendants of the calling session. The runtime type of a sibling task is not information the sibling needs.
3. Document the information exposure model for `lenny/get_task_tree()` in Section 8.5 so that deployers understand what each session can observe about its peers.
