# Technical Design Review Findings

**Document reviewed:** `docs/technical-design.md` (Draft, 2026-03-23)
**Review date:** 2026-03-24
**Perspectives:** Kubernetes, Security, DevOps/SRE, Open Source Community, Business Logic, Architecture/Interfaces

---

## Summary

| Severity | Count | Key Themes                                                                                                                                                                                                                        |
| -------- | ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Critical | 10    | Pod claim race condition, gateway monolith, multi-tenancy isolation, no billing hooks, MinIO SPOF, PgBouncer underspecified, schema migration gaps, dev mode deferred, adapter spec missing, PSS/RuntimeClass conflict            |
| High     | 19    | Split-brain risks, Token Service SPOF, elicitation timeouts, finalizer design, NetworkPolicy label trust, node isolation enforcement, alerting gaps, SSRF, tenant quotas, onboarding barriers |
| Medium   | 41    | Checkpoint atomicity, CRD validation, leader election tuning, credential naming, delegation complexity, SDK needs, configuration intimidation, quota reconciliation, tmpfs accounting, API surface consistency, Postgres deployment guidance, cert-manager ops, envelope key rotation, controller failover sizing, user invalidation propagation, compliance interfaces, session deriving, gVisor onboarding |
| Low      | 23    | Section numbering, ADR formatting, dev observability, packaging strategy, minor spec gaps                                                                                                                                         |

**Total findings: 93**

---

## Critical Findings

### K8s-C1 — Pod Claim Mechanism Has a Race Condition (Section 4.6)

The claim mechanism says gateways create `AgentSession` resources referencing idle `AgentPod` objects, and the controller reconciles via optimistic concurrency on the `AgentPod` status subresource. However, there is a window between `AgentSession` creation and the controller's reconcile loop where two `AgentSession` objects can exist for one `AgentPod`. The document does not specify how the controller handles multiple `AgentSession` objects referencing the same pod, or how the losing gateway is notified. This is a genuine race that can result in double-allocation.

**Recommendation:** Gateways should patch `AgentPod.status.claimedBy` directly using `resourceVersion`-based optimistic locking. Only one patch wins; others get a 409. `AgentSession` is created only after the claim succeeds. Alternatively, expose a claim API via the controller so gateways never write pod state directly.

### K8s-C2 — Pod Security Standards Conflict with gVisor/Kata RuntimeClass (Section 17.2)

The Restricted PSS requires `seccompType: RuntimeDefault`, which is a no-op for gVisor and may conflict with some Kata configurations (e.g., `allowPrivilegeEscalation: false` can be violated by certain Kata device plugins). In `enforce` mode, pods will be silently rejected, causing warm pool deadlock as the controller spins recreating failed pods.

**Recommendation:** Use `warn` + `audit` PSS modes initially. Consider OPA/Gatekeeper or Kyverno with more surgical policies instead of blanket Restricted PSS.

### Sec-C1 — Multi-Tenancy Relies on Application-Layer Query Filtering Only (Section 4.2)

Tenant isolation depends entirely on `tenant_id` filtering in application queries. A single missing `WHERE tenant_id = ?` clause breaks isolation. For a platform running untrusted LLM-generated code on behalf of multiple tenants, this is a critical gap.

**Recommendation:** PostgreSQL Row-Level Security (RLS) policies tied to the database role should be the primary isolation enforcement, with application-layer filtering as defense-in-depth.

### Sec-C2 — Dev Mode Disables mTLS With No Guard Rails (Section 17.4)

Local development mode runs with "no mTLS (plain HTTP)." If accidentally used in staging/production, all gateway-to-pod traffic is unencrypted. Session JWTs and credential leases transit in plaintext.

**Recommendation:** Hard startup assertion that fails if TLS is disabled outside an explicit `LENNY_DEV_MODE=true` flag with a prominent warning.

### Biz-C1 — No Billing or Pricing Hooks (Section 11.2, 15.1)

The design tracks usage via `GET /v1/usage` but provides no billing event stream, cost attribution beyond token counts (no pod-minutes, no idle cost allocation, no per-delegation rollup), metering API, or invoice-grade immutability guarantee. Usage counters in Redis fail open. Any deployer wanting to charge tenants has no integration point.

**Recommendation:** Add a billing event stream (webhook or message queue) with per-tenant, per-session cost events. Define a metering API separate from the observability usage endpoint.

### Biz-C2 — No Billing or Cost Attribution for Delegated Task Trees (Section 8.9, 11.2)

`TaskResult.usage` reports per-child tokens, but there is no rolled-up cost for entire trees. The `GET /v1/sessions/{id}/usage` response schema is not defined. Deployers cannot produce accurate invoices for recursive delegation.

**Recommendation:** Define tree-aggregated usage in the API response. Include pod-minutes and credential usage, not just LLM tokens.

### DevOps-C1 — MinIO Has No HA Topology and 24h RPO (Section 17.3)

MinIO's RPO is "last backup (daily)" with no mention of erasure coding or replication. A single-node MinIO StatefulSet is a SPOF for all artifact recovery. This contradicts the seal-and-export invariant ("session output is never lost due to pod cleanup").

**Recommendation:** Specify MinIO with erasure coding (minimum 4 nodes), versioning, and continuous replication to a second bucket/region for near-zero RPO.

### DevOps-C2 — PgBouncer Is "Required" But Entirely Underspecified (Section 12.3)

No topology (sidecar vs. shared Deployment), no pool mode (transaction vs. session), no sizing guidance, no HA design for PgBouncer itself (single instance is a SPOF in front of HA Postgres), no monitoring of pool saturation.

**Recommendation:** Specify PgBouncer deployment topology, pool mode (transaction mode for stateless gateway queries), sizing defaults, and HA approach (at minimum two replicas behind a Service).

### DevOps-C3 — Schema Migration Strategy Has No Tooling or Runbook (Section 10.5)

Expand-contract is mentioned but there is no migration tool specified, no process for who runs migrations, no rollback procedure, no guidance on locking behavior, and no dual-write window documentation.

**Recommendation:** Select a migration tool (e.g., golang-migrate, Atlas). Document the migration runbook: who runs it, how to roll back, how to handle partial completion.

### Arch-C1 — Gateway Monolith Risk (Section 4.1, 4.8, 9, 10)

The gateway conflates three fundamentally different workload profiles in one process:

1. **Stream proxy** — long-lived bidirectional connections (SSE/WebSocket). CPU-light but connection-heavy, memory-bound by buffer depth.
2. **Upload proxy** — short-lived, I/O-heavy, bandwidth-bound, bursty.
3. **MCP fabric** — virtual child interfaces, elicitation chain mediation, delegation approval. CPU-heavy during delegation bursts, memory-heavy when hosting many child interfaces per session.

A 50-child delegation tree and a simple single-session file upload are radically different resource profiles, but they compete for the same HPA-managed replica pool. Upload bandwidth saturation can starve stream proxying; a delegation burst can exhaust the goroutine budget available to new session attachments.

**Recommendation: Internal seams in a single binary (v1), with defined extraction triggers for later.**

For v1, keep the gateway as one Go binary but define explicit internal boundaries with independent resource budgets:

- **Goroutine pool isolation** — separate bounded worker pools for stream proxying, upload handling, and MCP fabric operations. A delegation burst cannot starve the stream proxy goroutines.
- **Internal Go interfaces** — define interfaces between the subsystems (`StreamProxy`, `UploadHandler`, `MCPFabric`, `PolicyEngine`) so they can be tested, profiled, and eventually extracted independently.
- **Per-subsystem metrics** — expose separate Prometheus gauges/histograms for each subsystem's queue depth, latency, and goroutine count. This gives operators visibility into which subsystem is the bottleneck before any split is needed.
- **Request classification at the router** — the HTTP router classifies incoming requests into subsystem categories at the edge, enabling per-category rate limiting and admission control.

This is the lightest lift. It doesn't change the deployment model but makes future extraction trivial because the seams are already defined.

**Future extraction triggers (when to split):**

- **Extract upload proxy** if upload bandwidth saturates gateway CPU or if upload latency P99 regresses stream proxy latency. This is the easiest split: deploy a separate `lenny-upload` Deployment behind the same ingress with a path-based route. The upload service validates, streams to pod staging, and writes the upload record to Postgres. The gateway reads the record during finalize.
- **Extract MCP fabric** if delegation tree depth > 3 causes P99 stream latency regression, or if virtual child interface memory dominates gateway RSS. Deploy a separate `lenny-fabric` Deployment that owns all virtual child interfaces, elicitation chain mediation, and delegation lifecycle. This is the most invasive split — only pursue if profiling shows delegation-heavy workloads starving non-delegation sessions.

**Required technical design doc changes:**

- Name the three subsystem boundaries (stream proxy, upload handler, MCP fabric) in Section 4.1 and state that they are designed as separable concerns
- Define the internal Go interfaces between them in Section 4.1
- Add per-subsystem HPA signals to Section 16.1 (goroutine pool utilization, queue depth, and latency per subsystem)
- State the extraction triggers explicitly so operators know when a split is warranted

---

## High Findings

### Kubernetes

**K8s-H1 — AgentPod Finalizer Design Not Specified (Section 4.6)**
Without finalizers, deleting an `AgentPool` cascades to all `AgentPod` objects immediately via GC, potentially while sessions are active. Add finalizers; only remove after confirming no active session and checkpoint completion.

**K8s-H2 — HPA Custom Metric Requires Unspecified External Metrics Adapter (Section 10.1)**
`lenny_gateway_active_streams` requires a metrics adapter (KEDA, Prometheus Adapter). If unavailable, HPA scale-down uses defaults, potentially killing active-session pods. Specify the adapter and add a preStop hook that blocks termination while streams > 0.

**K8s-H3 — NetworkPolicy Uses Mutable Labels for Trust (Section 13.2)**
Policies trust pods matching `lenny.dev/component: gateway` in namespaces matching `lenny.dev/component: system`. Labels are mutable. Any pod acquiring these labels is trusted. Use immutable `kubernetes.io/metadata.name` for namespace selection.

**K8s-H4 — Node Isolation for Kata Is Advisory, Not Enforced (Section 17.2)**
Taints/tolerations are recommended but do not prevent runc pods from landing on Kata nodes. Use `nodeAffinity` with `requiredDuringSchedulingIgnoredDuringExecution` and `RuntimeClass.scheduling.nodeSelector`. Explicitly state runc pods must not share nodes with gVisor or Kata.

**K8s-H5 — No PodDisruptionBudget Mechanism Defined for Agent Pods (Section 4.6, 17.1)**
"PDB via CRD" is mentioned but the mechanism is not specified. A blanket PDB on all managed pods blocks node drain even for idle pods. Use preStop hooks that checkpoint before allowing termination as the primary guard.

**K8s-H6 — AgentSession Owner Reference Creates Problematic GC Chain (Section 4.6)**
`AgentSession` owned by `AgentPod` means pod deletion cascade-deletes session metadata before the gateway can perform cleanup, billing finalization, and audit logging. Use a field reference (`spec.agentPodRef`) instead of an owner reference.

### Security

**Sec-H1 — Session JWT Key Management Not Specified (Section 10.2)**
The gateway mints session capability JWTs (containing `session_id`, `user_id`, `tenant_id`, `delegation_depth`, `allowed_operations`) signed with "a gateway-internal key" using HMAC-SHA256. The design specifies nothing about where this key lives, how it is distributed across replicas, or how it is rotated. HMAC is symmetric — every replica holds the full signing secret. One compromised replica can forge JWTs for any session, any tenant, and any delegation depth, undoing the blast-radius protections the rest of the design carefully builds (per-replica mTLS certs, credential leasing, one-session-only pods). Key rotation with a shared HMAC key invalidates all in-flight sessions unless a dual-key validation window is implemented, which is also unspecified.

**Recommendation:** Sign session JWTs via KMS. Gateway replicas call KMS to sign and verify locally with the cached public key. No replica ever holds the private key. Rotation is a KMS operation with automatic dual-key support.

- **Pluggable KMS backend** — provide built-in support for the most common KMS options (AWS KMS, GCP Cloud KMS, HashiCorp Vault Transit) with a `JWTSigner` interface so deployers can add custom backends.
- **Local development mode** — support a `local` signing backend that uses a file-based ES256 keypair (auto-generated on first run) or a `none`/`insecure` mode that disables JWT signing entirely. Gate the insecure mode behind an explicit `LENNY_DEV_MODE=true` flag with a startup warning, consistent with the dev-mode mTLS bypass (Section 17.4).
- **Design doc must specify:** the `JWTSigner` interface, supported backends, key rotation procedure per backend, dual-key validation window semantics, and what happens to in-flight sessions during rotation.

**Sec-H2 — API-Key Providers (Anthropic, OpenAI, etc.) Have No Short-Lived Token Mechanism (Section 4.9)**
The credential leasing model assumes all providers support short-lived token derivation. This is true for cloud-hosted providers (Bedrock via STS, Vertex via OAuth2, Azure via AAD), but not for direct API-key providers: Anthropic, OpenAI, Mistral, Cohere, and most smaller providers have no token exchange endpoint. The `anthropic_direct` provider claims to deliver a "short-lived or scoped" API key, but no such mechanism exists — pods would receive the full long-lived `sk-ant-...` key. A compromised pod has the key indefinitely.

**Recommendation: Credential-injecting LLM reverse proxy for API-key providers.**

For providers that cannot mint short-lived tokens, introduce an LLM proxy that sits between the pod and the provider API. The pod never sees the real API key.

_How it works:_

1. The `materializedConfig` returns a proxy URL and a lease token (not the real key):
   ```json
   {
     "apiKey": "lenny_lease_abc123",
     "baseUrl": "http://lenny-llm-proxy.lenny-system.svc:8080"
   }
   ```
2. The pod uses its provider SDK normally — it thinks the lease token is a real API key and the proxy is the provider endpoint.
3. The proxy scans all auth-relevant headers (`Authorization`, `x-api-key`, `api-key`), finds the one containing a `lenny_lease_` prefixed token, validates it against the lease store, and replaces it in-place with the real provider key. Everything else is forwarded as-is.
4. NetworkPolicy ensures pods cannot reach the real provider endpoints directly — only the proxy.

_This design is fully provider-agnostic._ The proxy has zero per-provider auth logic — it doesn't care which header the SDK uses. Adding a new API-key provider requires no proxy changes. Per-provider config is only needed for two things: the upstream URL (from the lease record) and optionally usage field extraction from responses (for budget enforcement at the proxy layer).

_Lease lifecycle — the pod never renews:_

The lease token is a stable session-scoped handle, not a time-limited credential. The proxy validates it against a lease record in the store; the gateway manages lease record validity (renewal, revocation). The pod holds the same token for the session's lifetime. This avoids forcing the runtime to handle token rotation for proxy-mediated providers. (`RotateCredentials` remains in the design for non-proxy providers where the pod holds real short-lived tokens that actually expire at the provider.)

_Hard rejection (revocation, budget exhausted, session terminated):_

The proxy returns HTTP 503 with a `Lenny-Lease-Error` header. The runtime adapter (sidecar) — which is Lenny-aware — intercepts this before the agent binary sees it, reports the appropriate event to the gateway (`AUTH_EXPIRED`, `RATE_LIMITED`, etc.) via the control channel, and the gateway decides: push new credentials, terminate session, or trigger fallback. The agent binary never sees lease-level errors.

_When the proxy is NOT needed:_

Providers with native short-lived token derivation (Bedrock, Vertex, Azure) skip the proxy entirely. Their `CredentialProvider` mints real tokens and the pod uses them directly. The proxy is only for API-key providers. The `CredentialProvider` interface decides which path to use.

_Deployment considerations:_

The proxy is a separate Deployment with its own HPA and HA (it becomes a SPOF for all API-key provider traffic). Latency overhead is sub-millisecond (cluster-internal hop, negligible vs. LLM response time). It must handle SSE streaming passthrough for streaming responses. Deployers who accept the risk of key exposure can opt out via `mode: direct` on the credential pool config, with a clear security warning.

**Sec-H3 — Shared Unix Socket Between Adapter and Agent Binary Is Unprotected (Section 4.7)**
The runtime adapter (sidecar) communicates with the agent binary over a Unix socket on a shared `emptyDir` volume. `shareProcessNamespace: false` prevents cross-container process visibility, but the shared volume is read-write for both containers. A compromised agent binary can: (1) delete the socket and create a fake one, intercepting adapter commands including credential delivery; (2) connect to the socket and impersonate the adapter, sending fake events (`RATE_LIMITED`, `AUTH_EXPIRED`) to the gateway via the control channel; (3) read/write the socket file if both containers share a UID.

Socket-level authentication (TLS over Unix socket, shared secret handshake) is **not recommended** — both containers share a filesystem trust boundary, so any secret accessible to the adapter is also accessible to the compromised agent binary. More importantly, requiring TLS client auth or token handshakes on the socket contradicts the stated goal of "minimizing what third-party binary authors need to implement."

**Recommendation: harden the socket through protocol design, not authentication.**

1. **Separate UIDs** — run the adapter and agent binary as different UIDs. The adapter creates the socket with mode `0660` owned by `adapter:shared-group`. The `emptyDir` directory is owned by the adapter UID with mode `0750`, preventing the agent binary from deleting and replacing the socket file. Both UIDs are non-root (compatible with Restricted PSS).

2. **Adapter-initiated protocol** — the adapter is always the initiator (client), the agent binary is always the responder (server). The agent binary cannot proactively send events to the gateway — it can only respond to adapter-initiated RPCs. The runtime-to-gateway events (`RATE_LIMITED`, `AUTH_EXPIRED`, etc.) should be restructured as responses to periodic adapter health polls rather than agent-initiated messages.

3. **Adapter treats all agent responses as untrusted input** — validate that `RATE_LIMITED`/`AUTH_EXPIRED` responses correspond to real provider errors (the adapter can independently check the LLM proxy's response codes), never forward raw agent-produced data to the gateway control channel without validation, and rate-limit event frequency.

4. **No credential material over the socket** — with the LLM proxy approach (see Sec-H2), the agent binary receives a lease token + proxy URL via config file or env var before startup. The `AssignCredentials` RPC goes from the gateway to the adapter over gRPC/mTLS; the adapter writes a config file before launching the agent process. Credential material never transits the socket protocol, making socket compromise less impactful.

These four measures reduce the socket to a control channel where the adapter is the trusted initiator and the agent binary is an untrusted responder that can answer questions but not ask them. The design doc should specify these constraints as part of the runtime adapter contract (Section 15.4).

**Sec-H4 — DNS Exfiltration Mitigation Is Optional (Section 13.2)**
The design proposes a DNS rate-limiting proxy only "for high-security profiles," but every Lenny pod runs untrusted LLM-generated code with access to workspace files. When all other egress is blocked (the design's default), DNS is the only remaining exfiltration channel. Data is encoded in DNS subdomain labels (`base64data.attacker.com`), forwarded by CoreDNS to upstream resolvers, and received by an attacker-controlled nameserver. At ~200 bytes/query and 50-100 qps, an attacker can steal API keys instantly and source files within minutes. This is not a niche threat — it is the primary data exfiltration vector for the platform's core threat model.

**Recommendation: DNS proxy as default for all agent namespaces, not opt-in.**

Deploy a dedicated CoreDNS instance in `lenny-system` with filtering plugins, sitting between agent pods and the cluster's main CoreDNS. Update the base egress NetworkPolicy to allow DNS only to the proxy (not kube-system CoreDNS directly).

_Proxy enforcement rules:_

| Control                             | Action                                                          | Default                                              |
| ----------------------------------- | --------------------------------------------------------------- | ---------------------------------------------------- |
| Per-pod query rate limit            | Drop queries exceeding threshold                                | 10 qps per pod IP                                    |
| Subdomain label length > 50 chars   | **Refuse** (REFUSED rcode) + alert                              | Enabled                                              |
| Total query name length > 100 chars | **Refuse** + alert                                              | Enabled                                              |
| High-entropy subdomain labels       | **Refuse** + alert (base64/hex patterns common in exfiltration) | Enabled                                              |
| TXT response size > 256 bytes       | Truncate or refuse                                              | Enabled                                              |
| Query logging                       | Log all queries with pod IP, session ID                         | All queries                                          |
| Domain allowlist                    | Lock down to only known-needed domains                          | Off by default, available for high-security profiles |

Queries matching refusal patterns are dropped at the proxy and never forwarded to upstream resolvers. The pod receives a REFUSED response — functionally identical to the domain not existing. Legitimate DNS queries (resolving cluster services, short domain names) are unaffected by these thresholds.

_NetworkPolicy change:_

Replace the current base egress rule allowing DNS to kube-system with:

```yaml
egress:
  - to:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: lenny-system
        podSelector:
          matchLabels:
            lenny.dev/component: dns-proxy
    ports:
      - protocol: UDP
        port: 53
      - protocol: TCP
        port: 53
```

_Interaction with egress profiles:_

For `restricted` egress profiles (pods using the LLM proxy from Sec-H2), pods need only gateway + LLM proxy + DNS proxy — no internet egress at all. DNS exfiltration via the proxy is the only remaining vector, and the filtering rules above mitigate it. For pods with relaxed egress (direct LLM provider access, package installation), the DNS proxy is even more critical as the only visibility into what those pods are resolving.

_Operational overhead:_

Lightweight — a 2-replica CoreDNS Deployment using ~20MB RAM per replica. Sub-millisecond added latency per query. Agent pods make very few DNS queries in practice (gateway and LLM proxy are cluster-internal, already resolved). The main ongoing cost is maintaining alerting rules for anomalous query patterns flagged by the proxy's logging.

**Sec-H5 — Rate Limits Fail Open Without Bounds (Section 12.4)**
On Redis unavailability, rate limits fail open with no maximum window. An attacker inducing Redis unavailability bypasses rate limiting entirely. Define a configurable maximum fail-open window (e.g., default 60s) after which the gateway switches to fail-closed. Add a configurable emergency hard limit at the gateway (in-memory, no external dependency) that caps requests per tenant/user regardless of Redis state. Both the fail-open window duration and the hard limit thresholds must be deployer-configurable to accommodate different risk tolerances. Alert immediately when fail-open activates.

**Sec-H6 — callbackUrl Is an SSRF Vector (Section 14)**
The `callbackUrl` field in `WorkspacePlan` is a client-supplied URL the gateway POSTs to on session completion. The gateway is the most privileged network position in the architecture — it has mTLS credentials to every pod, access to the Token Service (all KMS-decrypted secrets), Postgres (all tenants), and Redis. An SSRF from the gateway can reach cloud metadata endpoints (`169.254.169.254` — returns node IAM credentials), cluster-internal services (Token Service, Postgres, Redis), and can be used for port scanning. The URL is stored and used later (at session completion), giving attackers time to set up DNS rebinding between registration and callback.

**Recommendation: four mitigation layers.**

_Layer 1 — URL validation at registration time:_

- Scheme allowlist: HTTPS only (reject `http://`, `ftp://`, `file://`, `gopher://`)
- Resolve the hostname and validate the resolved IP against a blocklist: RFC 1918 private ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), loopback (`127.0.0.0/8`), link-local (`169.254.0.0/16`), IPv6 equivalents (`::1`, `fc00::/7`, `fe80::/10`), and the Kubernetes service CIDR (deployer-configured)
- Reject `.internal`, `.svc`, `.local`, `.cluster` TLDs (cluster-internal names)

_Layer 2 — DNS pinning (anti-rebinding):_
At registration time, resolve the hostname, validate the IP, and store the resolved IP alongside the URL. At callback time, connect directly to the stored IP with the original hostname in the `Host` header and TLS SNI. The hostname is never re-resolved, preventing DNS rebinding attacks.

_Layer 3 — Isolated callback worker:_
The gateway itself should not make callbacks. A dedicated callback worker (lightweight sidecar or separate Deployment) makes the outbound HTTP call. This worker has egress only to the internet — no access to Postgres, Redis, Token Service, or pod mTLS certs. Even if URL validation has a bypass, the callback originates from an unprivileged network position that cannot pivot to internal services.

_Layer 4 — Deployer-configurable domain allowlist (optional):_
For high-security deployments, deployers can restrict callback URLs to an explicit allowlist:

```yaml
callbackPolicy:
  allowedDomains:
    - "ci.example.com"
    - "*.hooks.company.internal"
  requireHttps: true
```

Clients can only register callback URLs matching these patterns.

Layers 1 and 2 are the minimum for any deployment. Layer 3 is recommended for production. Layer 4 is optional for enterprise. All layers should be implemented. The design doc should also specify the callback payload schema (currently undefined — see Biz-L2) and require HMAC signing so receivers can verify authenticity.

**Sec-H7 — Blocklist Mode for Setup Commands Is Easily Bypassed (Section 7.4)**
The default `blocklist` mode blocks command prefixes (`curl`, `wget`, `nc`, `ssh`, `scp`), but prefix matching on raw command strings is trivially bypassed: language interpreters (`python3 -c "import urllib..."`, `node -e "require('https')..."`), shell builtins (`bash -c "exec 5<>/dev/tcp/..."`), alternative binaries (`busybox wget`, `socat`, `telnet`), and indirect execution (`/usr/bin/curl`, `env curl`, `bash -c "curl ..."`, `$(which curl)`). Unless `bash`, `sh`, and `env` are also blocklisted (which would break most setup commands), the blocklist is cosmetic against adversarial input.

Network is blocked during setup (static NetworkPolicy), which mitigates most direct exfiltration — but setup commands can: (1) write malicious scripts to workspace files (`.bashrc`, `Makefile`) that execute later when the session is active and network may be available; (2) use DNS exfiltration if the DNS proxy from Sec-H4 is not deployed; (3) make connections to allowed egress destinations if the pool has a relaxed egress profile for package installation.

*Recommendation: reframe the command policy as a usability feature, not a security boundary, and add safer execution modes.*

1. **Explicitly document that blocklist mode is a convenience guard** — it catches accidental `curl` in a setup script written by a developer who forgot network is blocked. It does not stop an adversary. The real security boundaries are the sandbox (gVisor/Kata), NetworkPolicy, resource limits, and time bounds. The command policy is defense-in-depth at best.

2. **Make allowlist mode the default for multi-tenant deployments** — if setup commands come from untrusted sources, allowlist should be required, not optional. The existing commented-out allowlist (`npm ci`, `pip install`, `make`, `chmod`) is a good starting set. Deployers who haven't explicitly opted into blocklist should get allowlist by default.

3. **Add `shell: false` execution mode** — execute the command directly via exec (not through a shell), preventing shell metacharacters, subshells, pipes, redirects, and variable expansion. `["npm", "ci"]` runs `npm` with argument `ci` — no shell interpretation. Most setup commands are simple (`npm ci`, `pip install -r requirements.txt`, `make build`) and don't need shell features. This is dramatically safer than parsing a shell command string and should be the default for allowlist mode.

4. **Document the real security model** — the command policy's role is catching mistakes and providing clear error messages. The sandbox + network policy is the security boundary. This framing prevents deployers from developing false confidence in the blocklist.

**Sec-H8 — Image Supply Chain Controls Need Clearer Phasing in Build Sequence (Section 5.3, 18)**
Section 5.3 describes image signing and trusted registry enforcement as hard requirements, but Section 18 defers all of this to Phase 14 ("Hardening"). Since all phases are prerequisites for production (no go-live before Phase 15), there is no actual window of exposure. However, Section 18 should explicitly note that image supply chain controls (digest pinning, admission webhook, cosign verification) are a prerequisite for any production or staging deployment — and that running earlier phases in shared or pre-production environments without these controls requires a conscious risk acceptance. This prevents teams from normalizing unsigned images during development and carrying that habit into production.

### DevOps

**DevOps-H1 — Postgres Deployment Guidance Missing (Section 12.3)** *(downgraded from High to Medium)*
Section 12.3 lists Patroni, CloudNativePG, and managed services as options without guidance on when to use which. Postgres HA is the deployer's concern and should be transparent to Lenny — the platform should only specify what it needs from Postgres, not how Postgres is deployed. Section 12.3 should be reframed as minimum requirements that any deployment must meet:

- Version: 14+
- Replication: synchronous streaming (for RPO 0)
- Connection pooling: required (PgBouncer, pgcat, RDS Proxy, or equivalent)
- Read replica: at least one, for read-heavy query routing (session list, task tree, audit, usage reports)
- Automated backups with point-in-time recovery
- Minimum available connections: deployer-configurable (formula based on gateway replica count)

The design should note that managed services (AWS RDS, GCP Cloud SQL, Azure Database for PostgreSQL) satisfy all of these out of the box and are the recommended default. For self-hosted Kubernetes deployments, CloudNativePG is suggested as the simplest K8s-native option (no external DCS, single CRD, built-in PgBouncer, CNCF sandbox project). Deployers with existing Postgres infrastructure can use it as-is if it meets the requirements above.

Read/write splitting is an application-level concern: the gateway maintains two connection pools (primary for writes + strong reads, replica for eventually-consistent reads) configured via two connection strings. This should be documented in the gateway configuration, not in the Postgres HA section.

**DevOps-H2 — No Alerting Rules or SLO Definitions (Section 16)**
Comprehensive metrics are defined but zero alerting rules, SLO definitions, or runbook references exist. Key missing alerts: warm pool exhaustion, Postgres replication lag, Redis memory, credential pool utilization, gateway stream limits, artifact GC failure.

**DevOps-H3 — cert-manager Failure Modes and CA Rotation Undocumented (Section 10.3)** *(downgraded from High to Medium)*
cert-manager is the right choice and the TTL design is sound (4h pod certs > 2h max session age — active sessions don't hit cert expiry). However, the design doesn't document failure modes or CA rotation.

The most impactful failure mode is not cert expiry — it's **warm pool stall during cert-manager outage**: new pods can't get certificates, never reach `idle`, and the warm pool drains as existing pods are consumed by sessions with no replacements. This starts affecting capacity immediately, not after 4h.

The design should add:

1. **Warm pool controller cert awareness** — treat certificate readiness as part of pod health. Replace warm pods whose certs are within 30 minutes of expiry and haven't renewed. If cert-manager is down and new pods can't get certs, log clearly, emit a metric (`lenny_warmpool_cert_failures_total`), continue using existing warm pods, and don't spin-loop creating pods that will fail.

2. **Alerting** — alert on cert-manager pod not ready, `CertificateRequest` resources stuck pending > 5 minutes, any Certificate resource with `Ready=False`, and warm pool pods with certs expiring within 1h.

3. **cert-manager HA** — recommend 2+ replicas with leader election and a PDB, same as other critical controllers.

4. **CA rotation procedure** — document the process: deploy new `ClusterIssuer` with new CA keypair, distribute dual trust bundle (old + new CA) via `trust-manager`, update Certificate resources to use new issuer, wait one full TTL cycle (24h) for all certs to renew, remove old CA from trust bundle. For Vault-backed CA, document the PKI secrets engine integration.

**DevOps-H4 — Token Service Envelope Key Rotation Undocumented (Section 4.9, 10.5)** *(downgraded from High to Medium; JWT signing key rotation now covered by Sec-H1)*
The Token Service encrypts stored OAuth refresh tokens and credential pool secrets using envelope encryption via KMS. Section 10.5 mentions "re-encrypt stored tokens in a background migration job; old and new envelope keys coexist during rotation" but gives no operational detail: what triggers the re-encryption job, how to monitor progress, how to verify completion, what happens if the job fails mid-way (partially re-encrypted state), and how long the dual-key coexistence window lasts. The design should document the rotation procedure as an operational runbook for the Token Service.

**DevOps-H5 — Controller Failover Impact on Warm Pool Undocumented (Section 4.6)** *(downgraded from High to Medium)*
The ~17-second controller failover window is standard Kubernetes controller behavior, not a design flaw. Existing sessions are unaffected. If the K8s-C1 fix is adopted (gateways directly patch `AgentPod.status.claimedBy`), the controller is not on the claim hot path at all — only warm pod replenishment pauses during failover. The design should document:

1. **Warm pool sizing guidance** — `minWarm` should account for the failover window: `minWarm >= peak_requests_per_second × 17s + buffer`. Document this formula so deployers size pools correctly.

2. **Gateway behavior when pool is empty** — queue incoming session creation requests with a configurable timeout (e.g., 30s). If a pod becomes available (controller recovers, another session completes), serve the request. If timeout expires, return an error with `Retry-After`. This absorbs transient stalls without rejecting requests unnecessarily.

3. **Alerting** — `lenny_warmpool_available_pods` (alert when below `minWarm`), `lenny_warmpool_claim_queue_depth` (alert when > 0 for sustained period), `lenny_warmpool_claim_wait_seconds` (alert on P99 regression), `lenny_controller_leader_transitions_total` (alert on frequent transitions indicating instability).

**DevOps-H6 — Packaging Strategy Not Stated (Section 17)** *(downgraded from High to Low)*
The original finding bundled several concerns: Helm chart, CI/CD pipeline, environment promotion, and CRD versioning. CI/CD pipeline and environment promotion are deployer concerns, not platform design. CRD versioning is covered by K8s-M3 and Arch-M3. The only actionable design doc change is: Section 17 should state the packaging strategy (Helm chart as the primary install mechanism) as a delivery item to be built alongside the components.

### Architecture

**Arch-H1 — Split-Brain Risk in Session Coordination (Section 10.1)**
Postgres advisory locks (Redis fallback) are connection-scoped. A gateway replica losing and recovering its Postgres connection doesn't know it lost its lock. Use `SELECT ... FOR UPDATE SKIP LOCKED` with generation counters instead.

**Arch-H2 — Token Service Is a Hidden SPOF (Section 4.3, 4.9)**
The Token Service is the only component with KMS decrypt permissions. With the LLM proxy (Sec-H2) and KMS-based JWT signing (Sec-H1), its blast radius is narrower than it first appears: in-flight sessions are unaffected (the proxy validates lease tokens against Postgres/Redis, not the Token Service), and ongoing LLM API calls don't touch it. However, it remains a SPOF for: (1) new session creation — the gateway needs it to select and decrypt a credential from the pool for lease assignment; (2) OAuth flows for external MCP tools — no Token Service means no refresh token decryption; (3) credential rotation that requires bringing in a new pool credential needing KMS decryption.

**Recommendation:** Run the Token Service as a multi-replica Deployment (2+ replicas) with a PDB. Add a circuit breaker in the gateway — when the Token Service is unavailable, new session creation fails immediately with a clear error rather than hanging. No graceful degradation: sessions without credentials should not start.

**Arch-H3 — Elicitation Chain Has No Timeout or Timer Semantics (Section 9.2, 11.3)**
An elicitation blocks every session in the delegation chain while waiting for the human to respond. At depth 3, a human going to lunch can cause the grandchild's `maxSessionAge` or `maxIdleTime` to expire, collapsing the entire tree via cascade policy — all because the human was slow. The design defines no timeout for elicitations, no timer-pausing semantics, no cancellation mechanism, and no budget to prevent prompt spam.

**Recommendation: add elicitation timeout semantics to Sections 9.2 and 11.3.**

1. **Pause session timers during pending elicitation** — session age and idle timers stop while the session is waiting for a human response. The session is "waiting," not "idle." Timers resume when the response arrives or the elicitation times out.

2. **`maxElicitationWait` timeout** — add to the timeout table (default 600s, deployer-configurable). When it fires, the elicitation is marked as timed out, the originating pod receives an elicitation timeout error and can decide how to handle it (fail, retry differently, continue without the answer), and session timers resume.

3. **Per-hop forwarding timeout** (default 30s) — each hop in the chain must forward the elicitation within this window. Prevents a stalled intermediate pod from blocking the entire chain. Distinct from the human response timeout.

4. **`dismiss_elicitation` message type** — add to the client→gateway message types in Section 7.2. The human can reject an elicitation without answering it. Dismissal flows back down the chain; the originating pod receives a "user declined" result.

5. **Elicitation budgets** — max elicitations per session (e.g., 5) and max concurrent pending elicitations per tree (e.g., 3), configurable per pool and per delegation lease. Prevents a runaway agent from spamming the human with prompts.

**Arch-H4 — SessionStore and TaskStore Are Redundant (Section 4.2, 12.2)**
The design is internally inconsistent: Section 4.2 (Session Manager) already says it is the "source of truth for all session and task metadata" and manages "task records and parent/child lineage (task DAG)." But Section 12.2 lists a separate `TaskStore` for "task metadata, delegation tree." Every consumer accesses tasks through a session ID (`GET /v1/sessions/{id}/tree`, `get_task_tree(session_id)`). The Policy Engine backs onto `SessionStore`, not `TaskStore`. Sessions and tasks are one tightly coupled domain — every task maps 1:1 to a session, and the delegation tree is keyed by session IDs.

**Recommendation:** Merge `TaskStore` into `SessionStore`. Remove the `TaskStore` row from the Section 12.2 table. Update Section 8.11's reference to "TaskStore" to say "SessionStore." The merged store still exposes domain operations (e.g., `CreateChildTask`, `GetTaskTree`, `UpdateTaskStatus`) — the role-based interface principle from Section 12.1 is preserved. This fixes the inconsistency between Section 4.2 and Section 12.2 rather than creating new problems.

### Business Logic

**Biz-H1 — User Invalidation Propagation Mechanism Not Specified (Section 11.4)** *(downgraded from High to Medium)*
The design says revocation "propagates through the task tree" but doesn't specify the mechanism. With the LLM proxy (Sec-H2), NetworkPolicy, and DNS proxy (Sec-H4), the blast radius is smaller than it appears: the gateway can revoke all credential leases in the tree with one query, and the next LLM API call from any child is rejected by the proxy. Children cannot exfiltrate data (network blocked, DNS filtered) or make further LLM calls. The worst case is a child doing local-only compute (running tests, writing files) for minutes until it hits the proxy and dies — but this produces no externally-visible side effects since the workspace is pod-local and ephemeral.

**Recommendation:** On full revocation, the gateway should actively send `Terminate` RPCs to all pods in the task tree (it already knows every pod via the task DAG), not just passively wait for proxy rejection. This makes revocation reach all descendants within seconds. Section 11.4 should specify this as the mechanism for "full revoke" level invalidation.

**Biz-H2 — No Data Residency, Legal Hold, or Right to Erasure (Section 12.5, 4.5)** *(downgraded from High to Medium)*
The finding bundles three compliance features at different urgency levels. None are architecturally blocked — the design just needs to build in the right interfaces now.

*Legal hold (v1 — trivial):*
A boolean `legal_hold` flag on the session record. When set, the background GC job skips the session's artifacts regardless of TTL expiry. The `extend_artifact_retention` API (Section 7.1) already exists — legal hold is "extend to infinity until manually released." Add `PUT /v1/sessions/{id}/hold` to set/release. GC query becomes: `WHERE expires_at < now() AND legal_hold = false`. High compliance value, minimal implementation cost.

*Right to erasure (post-v1 — design interfaces now):*
An `EraseUserData(user_id)` operation that cascades across all stores: anonymize session/audit records, delete artifacts from MinIO, delete stored OAuth tokens, invalidate cached data. Legal hold takes precedence (GDPR Article 17(3)(e) exemption). To enable this later, each store interface should include a `DeleteByUser(user_id)` method from v1, even if initially unimplemented:

```go
type SessionStore interface {
    // ... existing methods ...
    DeleteByUser(userID string) error          // Anonymize/delete all session records for user
    SetLegalHold(sessionID string, hold bool) error
}

type ArtifactStore interface {
    // ... existing methods ...
    DeleteByUser(userID string) error          // Delete all artifacts for user's sessions
    DeleteBySession(sessionID string) error    // Delete artifacts for a specific session
}

type EventStore interface {
    // ... existing methods ...
    AnonymizeByUser(userID string) error       // Replace user_id with anonymized marker in audit logs
}

type TokenStore interface {
    // ... existing methods ...
    DeleteByUser(userID string) error          // Delete all stored OAuth tokens for user
}

type CredentialPoolStore interface {
    // ... existing methods ...
    DeleteLeaseHistoryByUser(userID string) error
}
```

*Data residency (post-v1 — ensure nothing blocks it):*
Per-tenant geographic routing requires a tenant-to-region mapping and region-aware store routing. Not needed for single-region deployments (most early adopters). The `ArtifactStore` interface should be instantiable per-region (not a global singleton) so multi-region deployers can route tenant data to the correct bucket. No v1 work needed beyond ensuring the interface design doesn't preclude it — e.g., `ArtifactStore` methods should accept a context carrying tenant metadata, not hardcode a single bucket.

**Biz-H3 — No Per-Tenant Quotas or Hierarchical Quota Model (Section 11.2)**
Quotas exist at user, global, and per-session levels but not per-tenant aggregate. No hierarchy (tenant > team > user), no soft warnings, no configurable reset periods. A single tenant can exhaust shared resources.

**Biz-H4 — Callback/Webhook Model Is Insufficient for CI/CD (Section 14, 15.1)**
No payload schema, no HMAC authentication, no retry behavior, no event types beyond terminal state, no idempotency key. Every CI/CD system needs all of these.

**Biz-H5 — Session Forking Dropped Without Viable Alternative (Section 19, 6.4)** *(downgraded from High to Medium)*
The decision to drop `fork_session` was correct — true forking with LLM context window state is impossible without provider support. However, the "create new session from workspace snapshot" workaround is closer to viable than it appears, if the design clarifies what gets snapshotted.

The pod filesystem has `/workspace/current` (agent's working directory, disk-backed), `/sessions/` (runtime conversation state like Claude's JSONL transcript, tmpfs), and `/artifacts/` (exportable outputs, disk-backed). The seal-and-export step (Section 7.1) exports a "workspace snapshot," and Section 4.4 separately stores "Claude session file snapshots." But the design doesn't clarify: does the workspace snapshot include `/sessions/` content? The resume flow (Section 7.3 step d) restores the session file as a separate step from replaying the workspace checkpoint, confirming they are independent artifacts.

If the session transcript (JSONL) is available as a downloadable artifact, a new session can be created with both the workspace files *and* the previous transcript injected into the workspace. The agent binary can read the transcript and understand prior context — this is how Claude Code's `--resume` works. Not true forking, but close enough for practical branching workflows.

**Recommendation:**

1. **Clarify the pod filesystem roles in Section 6.4** — the distinction between `/sessions/` (tmpfs, sensitive runtime conversation state, never on disk) and `/artifacts/` (disk-backed, exportable outputs staged for MinIO) is a data classification distinction, not a functional one. Document this explicitly so runtime adapter authors understand where to write what.

2. **Ensure the session transcript is a downloadable artifact** — the sealed session should export both the workspace snapshot and the session file as separate artifacts available via `GET /v1/sessions/{id}/artifacts`. This may already be the intent but is not stated explicitly.

3. **Add a convenience API** — `POST /v1/sessions/{id}/derive` creates a new session pre-populated with the parent session's latest workspace snapshot and session transcript file. This is sugar over "create session + upload parent's artifacts" but makes the fork-like workflow a single call. The derived session's transcript is injected into the workspace (e.g., at `/workspace/current/.session-history/`) so the agent can read it, not loaded into the LLM context automatically.

**Biz-H6 — No Tenant Self-Service API or Admin RBAC (Section 15.1)**
Admin endpoints have no access control scoping. No tenant-scoped admin role, no self-service portal path, no RBAC model beyond "authenticated client."

### Open Source Community

**OSS-H1 — No Quick-Start Experience (Section 1, 17, 18)**
7+ infrastructure components, 3 CRDs, separate namespaces, CNI requirements, and optional gVisor/Kata. No "run one agent session on my laptop in 15 minutes" story. Primary adoption killer. The dev mode in Section 17.4 addresses this but is Phase 15 of 15 in the build sequence.

**Recommendation: two-tier local dev mode, starting at Phase 2.**

*Tier 1 — `make run` (Phase 2, zero external dependencies):*
A single Go binary that starts the gateway + controller in-process with SQLite/in-memory stores and an embedded sample echo runtime that receives prompts and echoes them back. No Docker, no Kubernetes, no Postgres, no Redis. The user clones the repo, runs `make run`, and sends a curl to `localhost:8080`. This is the "it works" moment. The echo runtime also serves as the reference implementation for adapter authors (Section 15.4).

*Tier 2 — `docker compose up` (Phase 4-5, realistic local):*
The setup described in Section 17.4: gateway, controller simulator, real Postgres + Redis + MinIO, separate adapter container. This is for testing multi-container interactions, real storage, and adapter development against the full gateway contract.

*Supporting requirements:*

1. **Move dev mode to Phase 2** in the build sequence — it's a prerequisite for developing the platform itself, not a post-hoc convenience.
2. **Zero-credential mode** — dev mode works without LLM credentials. The echo runtime needs none. For users who want a real LLM, a single `ANTHROPIC_API_KEY` env var should be enough — no credential pools, Token Service, or KMS configuration.
3. **Minimal config** — the quick-start config should be < 10 lines:
   ```yaml
   runtimes:
     - name: sample-echo
       image: lenny/sample-echo:latest
   ```
   Everything else uses sensible defaults. No isolation profiles, delegation leases, or egress profiles required.
4. **Sample echo runtime** — built as part of Phase 2, doubles as the reference implementation and the quick-start's default runtime.

**OSS-H3 — gVisor Default Blocks First-Time K8s Deployment (Section 5.3)** *(downgraded from High to Medium; OSS-H2 removed — fully subsumed by OSS-H1)*
Local dev is handled by OSS-H1 (`make run` / docker-compose, no K8s needed). For production K8s, gVisor is the correct default, but most clusters don't have it installed (EKS, AKS, self-managed all require manual `runsc` setup; GKE supports it but not on default node pools). A deployer's first experience on most clusters is pods stuck in Pending with no clear error. However, the target audience (platform teams) routinely handles RuntimeClass setup — this is a UX issue, not an architecture gap.

**Recommendation: clear error messages and a dev-cluster escape hatch.**

1. **Controller startup validation** — when the warm pool controller starts, check that RuntimeClasses referenced by configured pools exist. If missing, log a clear error ("RuntimeClass 'gvisor' not found — install gVisor or set `allowStandardIsolation: true`"), emit a Kubernetes Event on the AgentPool resource, and expose a metric (`lenny_pool_runtime_class_missing`).

2. **Helm pre-install hook** — include a pre-install Job that checks for required RuntimeClasses and fails with a clear message if they're missing, preventing a broken install. The hook should list exactly which RuntimeClasses are needed based on the configured pools.

3. **`devMode: true` Helm value** — creates a default pool with `runc` isolation and `allowStandardIsolation: true`, skips RuntimeClass checks, and prints a warning in the install notes: "Development mode: using runc isolation. Not recommended for production."

**OSS-H4 — Runtime Adapter Contract Underspecified for Third Parties (Section 4.7, 15.4)**
Section 15.4 correctly plans a standalone adapter spec with .proto files, error codes, and a reference implementation. The gateway↔adapter gRPC contract (Section 4.7) is outlined with 13 RPCs and 4 events. However, the design has critical gaps that prevent a third-party author from evaluating feasibility:

The most important gap is the **adapter↔binary protocol** — the sidecar model says the binary "reads/writes on a well-defined socket protocol," but this protocol is completely absent. This is the only contract third-party authors actually implement (the adapter is provided by Lenny). The gateway↔adapter gRPC is Lenny's internal concern.

Also missing: RPC lifecycle ordering (can `Interrupt` be called during `Attach`? can `Checkpoint` be called during active streaming?), `Attach` stream message types (the bidirectional stream is the core of the interactive session but its message format is undefined), error handling contract (which errors trigger retry vs. session failure), and capability negotiation timing.

**Recommendation:**

1. **Define the adapter↔binary protocol** — recommend stdin/stdout JSON-line as the default for the sidecar model. Any language can read/write lines to stdin/stdout. The adapter launches the binary as a subprocess, handles all gRPC/mTLS complexity, and communicates via simple messages:
   ```
   Adapter → Binary (stdin):
     {"type": "start", "cwd": "/workspace/current", "env": {...}}
     {"type": "prompt", "text": "...", "attachments": [...]}
     {"type": "interrupt"}
     {"type": "configure_workspace", "cwd": "/workspace/current"}
     {"type": "terminate"}

   Binary → Adapter (stdout):
     {"type": "text", "content": "...", "final": false}
     {"type": "tool_use", "id": "...", "tool": "...", "args": {...}}
     {"type": "tool_result", "id": "...", "result": {...}}
     {"type": "elicitation", "id": "...", "schema": {...}}
     {"type": "complete", "result": {...}}
     {"type": "error", "code": "...", "message": "..."}
   ```
   This is dramatically simpler than gRPC for third parties. The adapter absorbs all platform complexity; the binary just reads prompts and writes responses.

2. **Document the RPC lifecycle state machine** — add a diagram to Section 4.7 showing valid RPC orderings and which RPCs can be called in which pod states. E.g., `AssignCredentials` must precede `StartSession`; `Interrupt` and `Checkpoint` can be called during `Attach`; `Terminate` can be called in any state.

3. **Define minimum vs. full adapter** — enumerate what a binary must implement for a basic session (receive `start`, receive `prompt`, emit `text` + `complete`) vs. full capabilities (checkpoint, delegation, elicitation, mid-session upload). Everything beyond the minimum is opt-in via RuntimeType capabilities.

4. **Build the sample echo runtime (OSS-H1)** as the living reference implementation — makes the contract concrete and testable. Third-party authors can fork and modify rather than implementing from scratch.

**OSS-H5 — No API Versioning or Stability Guarantees (Section 5.1, 15)**
Lenny has four versioned surfaces that third parties depend on: REST API, MCP tools, adapter↔binary protocol, and CRDs. The tech spec is the v1 blueprint, so the versioning policy must ship with v1 — third-party adapter authors need to know the stability contract before they start building.

**Recommendation: add a "Versioning and Stability" section to the tech spec covering all four surfaces.**

1. **REST API** — `/v1/` prefix already exists. State that v1 endpoints won't have breaking changes (removing fields, changing types, removing endpoints) without a v2 endpoint available alongside for at least one release cycle. Adding fields is non-breaking.

2. **MCP tools** — MCP has no built-in versioning mechanism. Treat MCP tool schemas as stable once published at v1. Breaking changes require a new tool name (e.g., `create_session_v2`) with the old tool supported for at least one release cycle. Alternatively, add a `version` field to tool input schemas for internal dispatch.

3. **Adapter↔binary protocol** — `protocolVersion` in RuntimeType (Section 5.1) is the right hook. State that protocol version N is supported for at least 2 release cycles after version N+1 is introduced. The adapter (provided by Lenny) handles version negotiation — third-party binaries only implement one version at a time.

4. **CRDs** — ship at `v1beta1` for v1 release (not `v1alpha1` — the tech spec is the v1 blueprint, so CRDs should reflect that maturity). Commit to conversion webhooks for any schema changes from `v1beta1` onward. Graduate to `v1` when the schema stabilizes.

5. **Define "breaking change"** for each surface so the contract is unambiguous. Publish a compatibility matrix showing which adapter protocol versions work with which gateway versions.

*(OSS-H6 removed — fully covered by DevOps-H6 and OSS-H3)*

---

## Medium Findings

### Kubernetes

| ID     | Finding                                                                                                                        | Section |
| ------ | ------------------------------------------------------------------------------------------------------------------------------ | ------- |
| K8s-M1 | Leader election params (15s/10s/2s) are controller-runtime defaults and are appropriate; increasing them would lengthen the failover dead zone. Keep defaults. The real mitigation is warm pool sizing and gateway queueing (see DevOps-H5). | 4.6 |
| K8s-M2 | 4h cert TTL on pods idle for 3.9h leaves 6min validity when claimed; track cert expiry in pod health                           | 10.3    |
| K8s-M3 | Controller creating NetworkPolicy per pool requires high-privilege RBAC; use pre-created policies with label selectors instead | 13.2    |
| K8s-M4 | No topology spread constraints specified for agent pods; define defaults in AgentPool spec                                     | 17.3    |
| K8s-M5 | RuntimeClass overhead values not quantified; provide reference manifests for gVisor (~200m/200Mi) and Kata (~500m/500Mi)       | 5.3     |
| K8s-M6 | tmpfs memory accounting not included in resource class definitions; pods will OOMKill during large session file accumulation   | 6.4     |
| K8s-M7 | SA token audience `gateway-internal` needs explicit statement that mTLS + NetworkPolicy are required companion controls        | 10.3    |
| K8s-M8 | No CRD CEL/OpenAPI validation rules; malformed AgentPool specs (minWarm > maxWarm) will cause controller panics                | 4.6     |

### Security

| ID     | Finding                                                                                                                           | Section   |
| ------ | --------------------------------------------------------------------------------------------------------------------------------- | --------- |
| Sec-M1 | AssignCredentials/RotateCredentials RPCs must be excluded from payload-level logging, gRPC access logs, and OTel trace attributes | 4.7, 13.3 |
| Sec-M2 | MinIO encryption at rest not specified; checkpoints containing conversation history stored in plaintext                           | 4.4, 12.5 |
| Sec-M3 | *(Reframed)* Cryptographic signing of elicitation provenance is unnecessary — the gateway already mediates all elicitations and sets provenance from its own records, not from pod-supplied data. The real control is URL domain validation (already in the design) and blocking agent-initiated URL-mode elicitations by default. The design should: (1) emphasize URL domain validation as a hard security control, not just a metadata check; (2) block URL-mode elicitations from agent binaries by default — only registered connectors can trigger URL-mode elicitations; (3) require the gateway to distinguish connector-initiated vs. agent-initiated elicitations in provenance metadata so client UIs can render them differently. See also Sec-M6 (connector_id validation). | 9.2 |
| Sec-M4 | No pod certificate revocation path; compromised pod retains valid mTLS cert for up to 4h                                          | 10.3      |
| Sec-M5 | Audit log streaming to external SIEM is "should" not "must"; compromised Postgres can modify audit tables                         | 11.7      |
| Sec-M6 | Connector impersonation in elicitations not addressed; gateway must validate connector_id against pod's authorized connectors     | 8.3, 9.3  |
| Sec-M7 | Mid-session uploads written directly to /workspace/current with no staging step; agent may read partial files                     | 7.3       |
| Sec-M8 | No admission-time enforcement that agent pod ServiceAccounts have zero RBAC bindings                                              | 10.3      |
| Sec-M9 | zip-slip protection mentioned but not specified: no supported formats, no symlink handling, no atomic cleanup on failure          | 7.3       |

### DevOps

| ID        | Finding                                                                                                               | Section |
| --------- | --------------------------------------------------------------------------------------------------------------------- | ------- |
| DevOps-M1 | No log aggregation stack, retention policy, or volume estimate; unbounded EventStore growth in Postgres               | 16.4    |
| DevOps-M2 | Quota fail-open reconciliation mechanism undefined; max drift and recovery window unspecified                         | 12.4    |
| DevOps-M3 | No K8s API server rate limiting for controller during pool-scale events; work queue depth limits not configured       | 4.6     |
| DevOps-M4 | No restore testing procedure or RTO validation; "tested quarterly" is aspirational without a runbook                  | 17.3    |
| DevOps-M5 | Zero operational runbooks for any failure mode (warm pool exhaustion, Postgres failover, credential exhaustion, etc.) | General |
| DevOps-M6 | No trace sampling strategy or backend specified; 100% sampling not viable at production scale                         | 16.3    |
| DevOps-M7 | No rollback procedure for pool rotation if new version is broken; old AgentPool CRD may already be deleted            | 10.5    |
| DevOps-M8 | Checkpoint GC job failure mode unaddressed: no monitoring, no idempotency, no ownership specified                     | 12.5    |
| DevOps-M9 | HPA custom metric sourcing mechanism not described (KEDA? Prometheus Adapter?)                                        | 10.1    |

### Architecture

| ID      | Finding                                                                                                                                                                                                                                                                                              | Section    |
| ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| Arch-M1 | SDK-warm pod selection coupled to workspace plan content parsing; needs explicit predicate in RuntimeType                                                                                                                                                                                            | 6.1, 14    |
| Arch-M2 | Checkpoint atomicity not defined; workspace and session file snapshots may be inconsistent                                                                                                                                                                                                           | 4.4, 7.3   |
| Arch-M3 | CRD schema versioning not addressed; gateway writing v1 CRDs while controller expects v2 during rolling deploy                                                                                                                                                                                       | 4.6        |
| Arch-M4 | Quota Postgres update timing unclear; if only on session completion, fail-open has unbounded overrun                                                                                                                                                                                                 | 12.4, 11.2 |
| Arch-M5 | Virtual MCP child interface lifecycle underspecified: storage, pending elicitation handling, replay on parent resume                                                                                                                                                                                 | 8.2, 8.11  |
| Arch-M6 | callbackUrl has no authentication or replay protection; define HMAC signing and retry semantics                                                                                                                                                                                                      | 14, 15.1   |
| Arch-M7 | `env` field called both "allowlist" and "blocklist" across sections; standardize terminology and enforcement point                                                                                                                                                                                   | 14, 4.9    |
| Arch-M8 | REST/MCP API overlap on session lifecycle is intentional (MCP clients need self-contained management), but three gaps remain: no consistency contract guaranteeing identical responses, no MCP tool versioning (REST has `/v1/`, MCP tools have none), and no shared error taxonomy between surfaces | 15.1, 15.2 |

### Business Logic

| ID     | Finding                                                                                               | Section  |
| ------ | ----------------------------------------------------------------------------------------------------- | -------- |
| Biz-M1 | "Hybrid" credential mode naming is confusing; 5 effective modes across 2 fields needs a decision tree | 4.9      |
| Biz-M2 | `awaiting_client_action` has no expiry, children behavior, or CI discovery mechanism defined          | 7.3      |
| Biz-M3 | Delegation configuration is enormously complex with no progressive simplification or presets          | 8        |
| Biz-M4 | No workspace versioning or lineage tracking; users manage snapshot references manually                | 4.5, 7.1 |
| Biz-M5 | Session labels not confirmed as filterable in usage API; blocks project/team cost attribution         | 14, 15.1 |
| Biz-M6 | Mid-session upload capability not discoverable by clients before session creation                     | 7.3      |
| Biz-M7 | Elicitation at depth 3+ creates unpredictable UX with no suppression or batching for known-safe flows | 9.2      |
| Biz-M8 | Warm pool idle cost has no deployer visibility, threshold alerts, or maintenance-window scale-to-zero | 4.6, 6   |

### Open Source Community

| ID     | Finding                                                                                                          | Section      |
| ------ | ---------------------------------------------------------------------------------------------------------------- | ------------ |
| OSS-M1 | No SDK or client library strategy; complex protocols (MCP streaming, reconnect-with-cursor) hard to re-implement | 15.1, 15.2   |
| OSS-M2 | Configuration surface is large and intimidating; no "minimal viable config" examples                             | 4.9, 5.1, 14 |
| OSS-M3 | Single-tenant path unclear; is tenant_id optional? What is the default?                                          | 4.2          |
| OSS-M4 | No competitive positioning against Argo, Temporal, Knative, Dapr                                                 | 1, 2         |
| OSS-M5 | No contribution guidelines or governance model implied; reads as internal spec                                   | General      |
| OSS-M6 | Build sequence has no demo-able milestones until Phase 5                                                         | 18           |
| OSS-M7 | Sidecar vs. embedded adapter trade-offs not quantified; no authoring guide                                       | 4.7          |
| OSS-M8 | `runtimeOptions` is a pass-through with no schema registry or validation; silent misconfiguration                | 5.1, 14      |

---

## Low Findings

| ID        | Area         | Finding                                                                                          | Section   |
| --------- | ------------ | ------------------------------------------------------------------------------------------------ | --------- |
| K8s-L1    | K8s          | HPA "active sessions" metric sourcing (Postgres vs. gateway Prometheus metric) unspecified       | 4.1       |
| K8s-L2    | K8s          | Section 8.7 referenced but does not exist; actual content is at 8.8                              | 4.7, 8.5  |
| K8s-L3    | K8s          | Dev mode mTLS gap prevents adapter authors from testing TLS-related issues                       | 17.4      |
| K8s-L4    | K8s          | `shareProcessNamespace: false` not enforceable via PSS; needs Kyverno/Gatekeeper policy          | 6.4       |
| K8s-L5    | K8s          | MinIO daily RPO contradicts "session output never lost" invariant                                | 17.3, 7.1 |
| K8s-L6    | K8s          | NetworkPolicy namespace selector `lenny.dev/component: system` too broad as lenny-system grows   | 13.2      |
| Sec-L1    | Security     | SA token audience `gateway-internal` is a static string; use deployment-specific audience        | 10.3      |
| Sec-L2    | Security     | File export `destPrefix` allows intentional overwriting with no logging                          | 8.8       |
| Sec-L3    | Security     | Lease extension audit missing gateway replica ID and client IP                                   | 8.6       |
| Sec-L4    | Security     | `runtimeOptions` passed through without validation or size limits                                | 14        |
| Sec-L5    | Security     | Redis token cache encryption algorithm and key management not specified                          | 4.3, 12.4 |
| Sec-L6    | Security     | Workspace file hash verification is optional; should be mandatory for delegation exports         | 7.3       |
| DevOps-L1 | DevOps       | Cosign enforcement webhook fail-open vs. fail-closed behavior not specified                      | 5.3       |
| DevOps-L2 | DevOps       | Audit table append-only enforcement relies on DB role grants not documented or drift-checked     | 11.7      |
| DevOps-L3 | DevOps       | No health check or smoke test for dev mode                                                       | 17.4      |
| DevOps-L4 | DevOps       | No admin API for manual pool drain, warm count adjustment, or force-terminate                    | 15.1      |
| DevOps-L5 | DevOps       | etcd pressure from high-volume CRD status updates at 500+ concurrent sessions not addressed      | 4.6       |
| DevOps-L6 | DevOps       | `egressProfile` enum and CIDR update procedures not defined                                      | 13.2      |
| Arch-L1   | Architecture | AgentSession CRD overloaded as both claim signal and state container                             | 4.6       |
| Arch-L2   | Architecture | File export globs may follow agent-created symlinks outside workspace                            | 8.8       |
| Arch-L3   | Architecture | No SSE back-pressure mechanism; gateway buffer policy for slow clients undefined                 | 7.2, 9.1  |
| Arch-L4   | Architecture | Build Phase 5 (gateway) cannot run sessions end-to-end without Phase 11 (credentials)            | 18        |
| Arch-L5   | Architecture | Admin API has no authorization scoping; regular users may enumerate cross-tenant usage           | 15.1      |
| Arch-L6   | Architecture | `await_children(mode="any")` does not specify whether remaining children are auto-cancelled      | 8.9       |
| Biz-L1    | Business     | 2h default `maxSessionAge` may be too short; no `session_expiring_soon` event before hard kill   | 11.3      |
| Biz-L2    | Business     | Callback payload schema not defined anywhere                                                     | 14, 15.1  |
| Biz-L3    | Business     | `GET /v1/usage` response schema undefined                                                        | 15.1      |
| Biz-L4    | Business     | No SLO targets for session creation P99, time-to-first-token, or resume latency                  | 17.3      |
| OSS-L1    | Community    | Decision log (Section 19) should be formatted as proper ADRs                                     | 19        |
| OSS-L2    | Community    | No observability defaults (Grafana dashboards, Jaeger) for dev mode                              | 16        |
| OSS-L3    | Community    | Node drain/checkpoint integration assumes cluster-admin expertise; needs tiered deployment guide | 4.6, 17.2 |
| OSS-L4    | Community    | Artifact retention defaults buried in storage section; need prominent operational defaults table | 12.5      |

---

## Top 10 Priorities for Next Revision

1. **Fix the pod claim race condition** (K8s-C1) — correctness blocker for the core allocation mechanism
2. **Enforce multi-tenancy at the database layer** (Sec-C1) — add Postgres RLS; application-layer filtering is not a security boundary
3. **Specify MinIO HA and PgBouncer topology** (DevOps-C1, C2) — production-blocking infrastructure gaps
4. **Add billing event stream and cost attribution** (Biz-C1, C2) — no commercial viability without metering
5. **Define schema migration tooling and runbook** (DevOps-C3) — deploy-time risk with no mitigation
6. **Move local dev mode to early build phase** (OSS-C2, H1) — community adoption requires a frictionless first-run
7. **Publish runtime adapter .proto specification** (OSS-C1, H4) — the primary community contribution path is blocked
8. **Add alerting rules and SLO definitions** (DevOps-H2) — metrics without alerts are decorative
9. **Address gateway monolith risk** (Arch-C1) — define internal seams before the codebase solidifies
10. **Specify Token Service HA and failure handling** (Arch-H2) — hidden SPOF that blocks all session creation and credential rotation
