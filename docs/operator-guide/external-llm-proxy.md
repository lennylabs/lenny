# External LLM Routing Proxy

Lenny's gateway talks to LLM providers on behalf of agent pods. It ships with a built-in translator that converts OpenAI Chat Completions and Anthropic Messages requests into the upstream wire formats for the v1 provider set (`anthropic_direct`, `aws_bedrock`, `vertex_ai`, `azure_openai`). See [`../reference/metrics.md`](../reference/metrics.md) for the metrics the translator emits, and [§4.9](../../spec/04_system-components.md#49-credential-leasing-service) of the spec for the subsystem contract.

This page documents how to integrate an **external** LLM routing proxy — LiteLLM, Portkey, OpenRouter, AWS Bedrock Agent, Azure API Management, or any custom in-house gateway — as the upstream for one or more Lenny `CredentialPool`s. The external-proxy path is a supported deployer option; Lenny itself does not ship or manage the external proxy.

## When to use an external LLM proxy

The gateway's built-in translator covers the mainstream provider set. Reach for an external proxy when your deployment needs one or more of the following:

- **Broader provider catalog.** You route to providers the gateway's built-in translator does not yet support (e.g., `cohere`, `mistral`, self-hosted vLLM/TGI, on-prem inference endpoints, private-preview foundation models).
- **Custom routing intelligence.** Cost-aware model selection, region-aware routing, per-tenant model pinning, canary rollouts across provider accounts — logic that would live outside Lenny's `CredentialRouter` and that you prefer to centralize in a shared team gateway.
- **Shared spend and observability.** Multiple internal teams already consume LLMs through a common gateway that aggregates spend, budget caps, and provider-side usage dashboards. Sending Lenny's traffic through the same gateway keeps spend reporting consistent.
- **Per-request model rewriting beyond interceptor scope.** If `PreLLMRequest` interceptors are sufficient (allowlist/denylist, trivial field rewrites), prefer them — they run inside the gateway process and keep Lenny-side observability intact. Reach for an external proxy when the rewriting logic depends on external state (spend dashboards, experiment assignments, model-availability signals) that interceptors cannot access cheaply.

If none of these apply, the built-in translator is the recommended path — it is lower latency, requires no additional service to operate, and keeps `PreLLMRequest` / `PostLLMResponse` interceptors firing on every request.

## Deployment topologies

Lenny is agnostic to where the external proxy runs. The three common topologies are:

**Cluster Service (same Kubernetes cluster).** Deploy the proxy (e.g., LiteLLM) as a `Deployment` + `Service` inside the same cluster as Lenny. Reference it by its cluster DNS name (`http://litellm.llm-gateway.svc.cluster.local:4000`). The gateway pod's egress NetworkPolicy must allow traffic to the proxy's namespace/pods (add a supplemental allow rule for the `lenny-system` gateway pods to reach the proxy's labeled pods). This is the lowest-latency option and the one that stays inside the cluster's trust boundary.

**External cluster.** The proxy runs on a separate cluster, VPC, or on-prem host. Reference it by its stable FQDN. The gateway pod's `allow-gateway-egress-llm-upstream` NetworkPolicy CIDR list must include the external proxy's IP range. This is typical when a shared team proxy serves many downstream consumers beyond Lenny.

**Cloud-managed LLM gateway.** Services like AWS Bedrock's cross-region inference endpoints, Azure API Management fronting Azure OpenAI, or Google Cloud's Vertex AI endpoints with custom routing rules. Treat them the same as the external-cluster case — reference by FQDN, allow the CIDR.

A minimal LiteLLM `Deployment` sketch (for the cluster-service topology):

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: litellm
  namespace: llm-gateway
spec:
  replicas: 2
  selector: { matchLabels: { app: litellm } }
  template:
    metadata:
      labels: { app: litellm }
    spec:
      containers:
        - name: litellm
          image: ghcr.io/berriai/litellm:main-stable
          args: ["--config", "/etc/litellm/config.yaml"]
          ports: [{ containerPort: 4000 }]
          envFrom:
            - secretRef: { name: litellm-provider-keys }
          volumeMounts:
            - { name: config, mountPath: /etc/litellm, readOnly: true }
      volumes:
        - name: config
          configMap: { name: litellm-config }
---
apiVersion: v1
kind: Service
metadata:
  name: litellm
  namespace: llm-gateway
spec:
  selector: { app: litellm }
  ports: [{ port: 4000, targetPort: 4000 }]
```

The LiteLLM config (`litellm-config` ConfigMap) enumerates your downstream provider accounts and any routing rules. This ConfigMap is owned and maintained by the team operating the external proxy; Lenny does not render it.

## Lenny integration patterns

Two integration patterns are supported. Choose based on whether you want Lenny's `PreLLMRequest` / `PostLLMResponse` interceptors to fire on the Lenny-to-external-proxy leg.

### Pattern A — external proxy as the upstream provider (recommended)

Lenny treats the external proxy as an opaque upstream. The runtime's SDK talks to the external proxy **directly**; the gateway's LLM routing subsystem is not in the request path.

Implement a custom `CredentialProvider` ([§4.9](../../spec/04_system-components.md#49-credential-leasing-service)) that mints `materializedConfig` entries pointing at the external proxy:

```go
// Pseudocode — the CredentialProvider interface is defined in Lenny's Go package.
func (p *ExternalProxyProvider) Materialize(ctx Context, req LeaseRequest) (MaterializedConfig, error) {
    return MaterializedConfig{
        BaseURL: "https://litellm.llm-gateway.svc.cluster.local:4000",
        APIKey:  p.fetchProxyKey(ctx, req.TenantID), // per-tenant proxy key, not the upstream provider's key
        Headers: map[string]string{
            // Any metadata the external proxy needs (e.g., billing tag, routing hint).
            "X-Tenant": req.TenantID,
        },
    }, nil
}
```

Configure the pool with `deliveryMode: direct`:

```yaml
credentialPools:
  - name: team-shared-llm-proxy
    provider: external_llm_proxy          # matches the registered CredentialProvider
    deliveryMode: direct                  # key + URL materialized into the pod
    assignmentStrategy: least-loaded
```

The runtime receives `{ baseUrl, apiKey }` in its `CredentialLease`, configures its OpenAI or Anthropic SDK with that base URL, and talks to the external proxy directly. Routing intelligence, model catalog, and provider switching all live at the external proxy.

**Governance implications.**

- Lenny retains its credential-governance role: who can lease the external-proxy key is still controlled by Lenny's `CredentialPolicy`, and every lease is audited (`credential.leased`).
- `PreLLMRequest` and `PostLLMResponse` interceptors do **not** fire on Pattern A traffic — the request never traverses the gateway's LLM routing subsystem. Enforce policy (model allowlist/denylist, prompt inspection, PII redaction) at the external proxy's inbound side.
- Authoritative token counting comes from the external proxy, not from Lenny. Configure the external proxy to emit usage to your billing/spend system; Lenny's `lenny_gateway_llm_proxy_*` metrics will not reflect this traffic.
- The hard-rejection rule for `deliveryMode: direct` + `isolationProfile: standard` in multi-tenant mode still applies ([§4.9](../../spec/04_system-components.md#49-credential-leasing-service)) — Pattern A pools must use `sandboxed` or `microvm` isolation in multi-tenant deployments.

### Pattern B — external proxy behind Lenny's LLM Proxy

The gateway's built-in translator runs normally, treating the external proxy as the upstream endpoint. `PreLLMRequest` and `PostLLMResponse` interceptors fire on the Lenny-to-external-proxy leg.

Configure an existing built-in provider with a `baseUrl` override pointing at the external proxy:

```yaml
credentialPools:
  - name: claude-via-team-proxy
    provider: anthropic_direct
    deliveryMode: proxy
    proxyDialect: anthropic
    providerConfig:
      baseUrl: https://litellm.llm-gateway.svc.cluster.local:4000  # overrides api.anthropic.com
    credentials:
      - name: team-proxy-key-1
        secretRef: { name: litellm-tenant-key, key: api_key }
```

The gateway's translator converts the runtime's dialect (OpenAI or Anthropic) into the Anthropic wire format, injects the credential, and forwards to the external proxy. The external proxy accepts the Anthropic-dialect request and re-translates downstream to whatever provider it routes to.

> **External-proxy inbound contract.** The external proxy **must accept the pool's configured upstream provider wire format on its inbound side**. For a pool configured with `provider: anthropic_direct`, the external proxy's inbound must be Anthropic Messages API-compatible; for a pool configured with `provider: aws_bedrock`, the inbound must be Bedrock's Converse/InvokeModel shape; and so on. LiteLLM, Portkey, and most LLM routing gateways accept OpenAI wire format natively and an Anthropic-compatible inbound as an option — verify your specific external proxy's inbound contract before configuring the pool. A mismatch between the provider's expected shape and the external proxy's inbound will surface as `schema_mismatch` translation errors.

**Governance implications.**

- `PreLLMRequest` and `PostLLMResponse` interceptors fire on every request (Lenny has full visibility up to the external proxy's inbound side).
- Cross-provider translation happens at the external proxy, outside Lenny's observability scope — Lenny sees the Anthropic request and response shapes, not the downstream provider Bedrock/Vertex/Mistral shapes.
- Authoritative token counting: the gateway's translator extracts `usage.input_tokens` / `usage.output_tokens` from the Anthropic-shaped response returned by the external proxy. The external proxy MUST preserve these fields; if it aggregates or rewrites them, Lenny's counts will not match the external proxy's own spend reports.
- Pod identity binding, interceptor chain, and the Lenny-side circuit breaker all behave as they do for any proxy-mode pool.

## Picking a pattern

| Requirement | Pattern A | Pattern B |
|---|---|---|
| Lenny-side `PreLLMRequest` / `PostLLMResponse` interceptors must fire | — | ✓ |
| External proxy is the sole place for policy enforcement | ✓ | — |
| Custom provider beyond the gateway's built-in set | ✓ | ✓ (if the external proxy accepts Anthropic or OpenAI dialect) |
| Per-request cost routing based on external-proxy logic | ✓ | ✓ |
| Lowest latency path Lenny can offer | — (runtime → external proxy directly) | — (runtime → gateway translator → external proxy) |
| Authoritative token count produced by Lenny | — | ✓ |

Pattern A is recommended unless you specifically need Lenny-side interception of external-proxy traffic.

## Observability

**Pattern A.** Lenny's `lenny_gateway_llm_*` metrics do not cover external-proxy-routed traffic. Scrape the external proxy's own metrics (LiteLLM exposes `/metrics` with per-model latency and spend counters; Portkey has an analytics dashboard; cloud-managed gateways expose provider-specific metrics). Cross-reference against Lenny's `credential.leased` audit events to correlate which tenant/session was using which external-proxy key.

**Pattern B.** Lenny's existing metrics apply end-to-end from runtime to external proxy:

- `lenny_gateway_llm_proxy_request_duration_seconds` — end-to-end hop including the external proxy.
- `lenny_gateway_llm_translation_duration_seconds` — time spent in the gateway's translator (does not include the external proxy's processing).
- `lenny_gateway_llm_translation_errors_total{error_type="upstream_5xx"}` — external proxy failures feed this counter and the LLM Proxy circuit breaker.

## Security

- **Egress.** The gateway pod's `allow-gateway-egress-llm-upstream` NetworkPolicy ([`security.md`](security.md), [§13.2](../../spec/13_security-model.md#132-network-isolation) NET-046) must allow the external proxy's address (CIDR or in-cluster pod selector). Without an explicit rule, Lenny's egress-default-deny policy blocks the connection.
- **Credentials at rest.** The external proxy's own provider keys (AWS access keys, Anthropic keys, etc.) live in the external proxy's secret store, not in Lenny. Lenny only holds the key the pod or the translator uses to authenticate to the external proxy. Rotate that key via Lenny's normal credential lifecycle ([`../tutorials/user-credentials.md`](../tutorials/user-credentials.md) and [§4.9](../../spec/04_system-components.md#49-credential-leasing-service) Fallback Flow); rotate the external proxy's own provider keys via its own tooling.
- **TLS.** Always use `https://` for the external proxy `baseUrl`. The gateway process's outbound TLS verification is controlled by the `gateway.llmProxy.upstreamTLSVerify` Helm value ([`../reference/configuration.md`](../reference/configuration.md)); leave it enabled unless you have a specific reason (e.g., a self-signed internal CA you have added to the gateway's trust store).
- **Tenant scoping.** If the external proxy is multi-tenant, mint a distinct proxy key per Lenny tenant and encode the tenant ID in a header the external proxy is configured to enforce on. Otherwise a cross-tenant cache or budget leak at the external proxy could affect Lenny tenants.

## See also

- [`security.md`](security.md) — credential flow through the gateway's translator, NET-046 egress policy.
- [`../reference/metrics.md`](../reference/metrics.md) — LLM translator metrics.
- [`../reference/configuration.md`](../reference/configuration.md) — `gateway.llmProxy.*` Helm values.
- [`../../spec/04_system-components.md#49-credential-leasing-service`](../../spec/04_system-components.md#49-credential-leasing-service) — full credential leasing contract, delivery modes, gateway translator, external-proxy integration.
