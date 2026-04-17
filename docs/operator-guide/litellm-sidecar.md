---
layout: default
title: LiteLLM sidecar (proxy mode)
parent: "Operator Guide"
nav_order: 16
---

# LiteLLM Sidecar (Proxy Mode)

When a CredentialPool is configured with `deliveryMode: proxy`, the real upstream LLM API key never reaches the agent pod. Instead, the agent pod sees a proxy URL and a short-lived lease token; Lenny's gateway validates the lease, runs policy interceptors, and forwards the request to a **LiteLLM sidecar** container running inside the gateway pod. LiteLLM holds the real upstream credentials, translates requests to the upstream provider's native wire format, and forwards.

This page explains how to deploy, configure, and operate the LiteLLM sidecar.

---

## Architecture

```
┌──────────────────────────────────────────────────────┐
│ Agent pod                                             │
│                                                       │
│  runtime ──(OpenAI or Anthropic SDK)──> lease token + │
│                                         proxy URL    │
└──────────────────┬────────────────────────────────────┘
                   │ HTTPS (mTLS)
                   ▼
┌──────────────────────────────────────────────────────┐
│ Gateway pod                                           │
│                                                       │
│  ┌─────────────────────────────┐    ┌──────────────┐  │
│  │ Lenny proxy subsystem       │    │ LiteLLM      │  │
│  │ - lease validation          │───>│ sidecar      │──> upstream
│  │ - SPIFFE binding            │    │ - translate  │   (Anthropic,
│  │ - PreLLMRequest interceptor │    │ - inject key │    Bedrock,
│  │ - PostLLMResponse + tokens  │<───│ - forward    │<──  Vertex, etc.)
│  └─────────────────────────────┘    └──────────────┘  │
│                       loopback (shared secret auth)   │
└──────────────────────────────────────────────────────┘
```

- Agent pod speaks OpenAI or Anthropic wire format (per pool `proxyDialect`) to Lenny's proxy subsystem.
- Lenny's proxy subsystem validates the lease, runs interceptors, extracts authoritative token usage from the response.
- LiteLLM sidecar translates to the upstream provider's native wire format, injects the real key, and forwards.
- **Real API keys live only in the gateway pod's LiteLLM sidecar memory** — never in the agent pod.

---

## When to use proxy mode

| | Direct mode | Proxy mode (LiteLLM sidecar) |
|---|---|---|
| Simplicity | Simpler — no sidecar dependency | Requires LiteLLM sidecar configured |
| Latency | Lowest (pod → upstream directly) | One extra hop (pod → gateway → LiteLLM → upstream) |
| Credential safety in multi-tenant | Key reaches agent pod | Key never reaches agent pod |
| Authoritative token counting | Pod-reported | Upstream-extracted (not spoofable) |
| Recommended default | Single-tenant, trusted agent pod | Multi-tenant, regulated, untrusted agent |

Proxy mode is the **recommended default for multi-tenant deployments**.

---

## Enabling the sidecar

The Helm chart conditionally adds a `litellm` sidecar container to the gateway Deployment when any active `CredentialPool` has `deliveryMode: proxy`. Configure it via Helm values:

```yaml
llmProxy:
  litellm:
    # The signed hardened wrapper (see "Hardening" below). The chart's default
    # value points at this image; do not override unless you are using a private
    # mirror of the same digest. Admission policy rejects pods whose sidecar
    # image is not the signed wrapper.
    image: "lenny/litellm-hardened:<lenny-version>"
    port: 4000                   # loopback port inside the gateway pod (127.0.0.1:4000)
    config:
      # LiteLLM's model_list configuration — maps dialect+upstreamModel to
      # upstream providers. See LiteLLM docs: https://docs.litellm.ai/docs/proxy/configs
      model_list:
        - model_name: claude-sonnet-4
          litellm_params:
            model: anthropic/claude-sonnet-4-20250514
            api_key: "os.environ/ANTHROPIC_API_KEY"
        - model_name: claude-sonnet-4-bedrock
          litellm_params:
            model: bedrock/anthropic.claude-sonnet-4-v1:0
            aws_region_name: us-east-1
```

Credentials are **not** placed in `values.yaml`. Lenny's Token Service materializes them into a tmpfs file mounted into the sidecar at startup, refreshed on every lease/rotation event.

---

## Pool configuration

Every proxy-mode pool declares a `proxyDialect` that MUST match one of the Runtime's declared `credentialCapabilities.proxyDialect` values:

```yaml
credentialPools:
  - name: claude-proxy-prod
    provider: anthropic_direct
    deliveryMode: proxy
    proxyDialect: anthropic       # openai | anthropic
    proxyEndpoint: https://gateway-internal:8443/llm-proxy
    credentials:
      - id: key-1
        secretRef: lenny-system/anthropic-key-1
```

If `proxyDialect` is not present in the bound Runtime's `credentialCapabilities.proxyDialect` list, pool registration fails with `422 INVALID_POOL_PROXY_DIALECT`.

---

## Runtime declaration

The Runtime must declare the dialects its SDK speaks:

```yaml
name: claude-worker
credentialCapabilities:
  hotRotation: true
  proxyDialect: [openai, anthropic]
```

Runtimes that do not support proxy mode omit `proxyDialect` or set it to `[]` — they can only be bound to direct-mode pools.

---

## Security boundaries

- **LiteLLM SPIFFE identity.** LiteLLM does not have its own SPIFFE identity. Its outbound requests to upstream providers use the gateway pod's network identity. The sidecar is part of the gateway's trust envelope, not a separate trust domain.
- **Loopback authentication.** The Lenny proxy subsystem authenticates to LiteLLM on the pod-local loopback with a shared secret (`litellm_master_key`) generated at gateway startup, held only in process memory, rotated on every gateway restart. No TLS is required on the loopback connection because traffic never leaves the pod.
- **Credential material.** Real upstream API keys are written by the gateway to a tmpfs-backed file mounted read-only into the LiteLLM sidecar. On credential rotation, the gateway atomically replaces the file and signals LiteLLM to reload.
- **Lease enforcement.** Lease token validation happens in the Lenny proxy subsystem **before** requests reach LiteLLM. An expired or revoked lease is rejected at the Lenny layer; LiteLLM never sees it.

---

## Hardening

Because the LiteLLM sidecar handles real upstream credentials and sits on the hot path of every proxy-mode request, Lenny applies aggressive isolation controls. All are mandatory in production deployments (any `credentialPools[*].deliveryMode: proxy` with `LENNY_ENV=production`).

### 1. Hardened wrapper image (`lenny/litellm-hardened`)

Lenny publishes `lenny/litellm-hardened:<lenny-version>` — a thin wrapper image built `FROM` a digest-pinned upstream LiteLLM image with hardening overlays baked in at build time:

- Admin UI static assets deleted from the image filesystem.
- Config lockdown env vars baked in (cannot be undone by runtime `ENV` overrides).
- Default entrypoint binds to `127.0.0.1:4000` only.
- Image is signed with cosign and ships with a Sigstore-attested SBOM.

The admission policy ([Pod Security](security.md#pod-security-controls)) verifies the wrapper signature and refuses to admit gateway pods whose sidecar image is not the signed wrapper.

The upstream pin is bumped within 48 hours of every LiteLLM upstream release, 24 hours for any upstream CVE. **Full vendoring** (forking LiteLLM and building from source) is the emergency fallback when an upstream CVE requires a patch before upstream ships a fix — it is not the default maintenance mode.

### 2. Container security context

Every gateway pod's LiteLLM sidecar container runs with:

| Control | Setting |
|---|---|
| User | `runAsNonRoot: true` (specific UID/GID) |
| Capabilities | All dropped |
| Root filesystem | Read-only |
| Writable paths | `tmpfs /tmp` and `tmpfs /run/lenny/litellm-creds` (mode `0400`, gateway-populated) |
| Privilege escalation | `allowPrivilegeEscalation: false` |
| Seccomp | `RuntimeDefault` |
| Resource limits | CPU and memory limits set explicitly |

### 3. LiteLLM config lockdown

The wrapper image and the gateway's startup probes together enforce:

- **Admin UI and `/admin` routes disabled.** The gateway probes the sidecar at startup and refuses `ready` if any disallowed route returns non-404.
- **No runtime model management.** `model_list` is read from the config file at startup; hot-reload is disabled. Config changes require a gateway rollout.
- **Telemetry callbacks disabled.** No Langfuse, no LangSmith, no custom analytics callbacks, no phone-home. Lenny's own OTel collector is the authoritative telemetry path.
- **Route allowlist.** Only `POST /v1/chat/completions`, `POST /v1/messages`, `POST /v1/embeddings`, and `GET /health` are served. Every other route returns 404.
- **Schedule-driven features disabled.** No cron rotation, no background retries. The gateway's own rotation machinery is authoritative.

### 4. Egress isolation (NetworkPolicy)

The gateway pod's egress NetworkPolicy (`allow-gateway-egress-llm-upstream`, rendered by the Helm chart when any pool has `deliveryMode: proxy`) constrains the LiteLLM sidecar's outbound traffic to the upstream LLM provider destinations the deployment's pools reference. The policy blocks:

- Cluster-internal pod and service CIDRs (via `except` clauses).
- Cloud metadata endpoints (`169.254.169.254`, `fd00:ec2::254`, `100.100.100.200`).
- Any destination not in the allowlist.

There is no separate forward-HTTP-proxy between the sidecar and upstream providers. The sidecar shares the gateway pod's network namespace, so the NetworkPolicy applies to its traffic automatically.

### 5. Attack-surface monitoring

| Metric | Alert | Purpose |
|---|---|---|
| `lenny_gateway_litellm_route_anomaly_total` | `LiteLLMRouteAnomaly` (Critical) | Requests to unexpected sidecar routes — indicates misconfiguration or compromise |
| `lenny_gateway_litellm_egress_anomaly_total` | `LiteLLMEgressAnomaly` (Critical) | Outbound connection attempts to destinations outside the allowlist (eBPF or NetworkPolicy drop counters) |
| `lenny_gateway_litellm_process_restart_total` | `LiteLLMUnexpectedRestart` (Warning) | Restart during an active session (OOM, crash, unexpected config reload) |

Steady-state value for the anomaly counters is zero; any non-zero rate is actionable.

---

## Observability

| Metric | Description |
|---|---|
| `lenny_gateway_llm_proxy_active_connections` | Active upstream LLM connections |
| `lenny_gateway_llm_proxy_request_duration_seconds` | Request duration (histogram) |
| `lenny_gateway_llm_proxy_circuit_state` | Subsystem circuit state: 0 closed, 1 half-open, 2 open |

When the circuit is open the Lenny proxy rejects new requests with `PROVIDER_UNAVAILABLE` before they reach LiteLLM. The `GatewaySubsystemCircuitOpen` warning alert fires if the circuit stays open for more than 60 seconds.

---

## Scalability and the Go-rewrite decision

LiteLLM is Python; Lenny's gateway is Go. The sidecar's CPU and memory footprint competes with the gateway's own budget on the same pod. The platform keeps LiteLLM **as long as its footprint is not dominant** relative to the gateway's work. When the footprint becomes dominant, the platform replaces LiteLLM with a native Go translator (same wire contract, transparent to the agent pod). The decision is driven by measurement at Phase 13.5, not by absolute numbers.

Four ratios are tracked inside the gateway pod under sustained Tier 3 load:

| Signal | Rewrite-trigger threshold |
|---|---|
| LiteLLM sustained CPU as a fraction of gateway-pod total CPU | > 50% |
| LiteLLM RSS as a fraction of gateway-pod total RSS | > 50% |
| LiteLLM processing time as a fraction of `lenny_gateway_llm_proxy_request_duration_seconds` P95 (excluding upstream provider latency) | > 30% |
| `maxSessionsPerReplica` drop when all pools flip from `direct` to `proxy` | > 2× |

**The trigger fires if any two of the four cross their threshold at Phase 13.5 or later load validation.** The thresholds and SLO are PROVISIONAL until Phase 14.5 re-validation. Below the trigger, LiteLLM stays — the sidecar model's provider-catalog breadth and zero Go-side translation code outweigh its costs.

---

---

## Troubleshooting

### `422 INVALID_POOL_PROXY_DIALECT` at pool registration

The pool's `proxyDialect` is not declared in the Runtime's `credentialCapabilities.proxyDialect`. Update the Runtime declaration or change the pool dialect.

### Agent receives `PROVIDER_UNAVAILABLE`

Check `lenny_gateway_llm_proxy_circuit_state`. If open (2), the upstream provider is failing. Investigate via `lenny-ctl admin circuit-breakers list`.

### Latency higher than expected

The LiteLLM sidecar adds one intra-pod hop (usually <1 ms of transport) plus Python translation cost (a few ms per request at sustained Tier 3 rates). If you see significant overhead, inspect `lenny_gateway_llm_proxy_request_duration_seconds` and compare with direct-mode upstream latency in a test pool. Sustained overhead above the [proxy-mode translation SLO](../reference/metrics.md) (P95 ≤ 30% of the proxy-hop latency, excluding upstream provider time) is one of the four Go-rewrite-trigger signals — see *Scalability and the Go-rewrite decision* above. A single elevated sample is not actionable; sustained excess over a 5-minute window is.

### Sidecar restarts during active sessions

Check `lenny_gateway_litellm_process_restart_total{reason=...}`. OOM (`reason=oom`) indicates the memory limit is too low for current load; bump `llmProxy.litellm.resources.limits.memory`. Unexplained crashes (`reason=crash`) on a signed wrapper image are a supply-chain-integrity concern — alert on `LiteLLMUnexpectedRestart` and investigate.

### Alert: `LiteLLMRouteAnomaly` or `LiteLLMEgressAnomaly`

These fire when the sidecar receives requests to routes outside the allowlist, or attempts outbound connections to destinations outside the NetworkPolicy allowlist. Steady-state values are zero. Any non-zero rate is potentially a compromise signal — do not treat as informational. Capture the sidecar's recent logs, cordon the affected gateway replica, and investigate before assuming it's benign.

---

## Related

- [Security](security.md) — credential flow, LLM proxy in context.
- [Reference: Configuration](../reference/configuration.md) — full `llmProxy.litellm.*` values.
- [Runtime adapter guide](../runtime-author-guide/adapter-contract.md) — how runtimes consume `proxyDialect` from the adapter manifest.
