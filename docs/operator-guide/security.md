---
layout: default
title: Security
parent: "Operator Guide"
nav_order: 5
---

# Security

This page is the configuration reference for every security-related control in Lenny: mTLS, OIDC/OAuth 2.1, the Token Service, KMS integration, credential leasing, the LLM Proxy, pod security controls, network policies, RBAC, and audit logging.

For the *why* — the design principles behind these controls and how they map to common compliance frameworks — read [Security Principles](security-principles) first. Lenny does not claim any certification; the principles page describes the control primitives Lenny provides and the framework clauses those primitives are designed to support in your deployment.

---

## mTLS (Gateway-to-Pod)

All communication between gateway replicas and agent pods uses mutual TLS (mTLS) provisioned by cert-manager.

### Certificate Lifecycle

- **Issuer:** cert-manager `ClusterIssuer` creates per-pod certificates
- **Certificate TTL (per component):**
  - Gateway replicas: 24h
  - Agent pods: 4h
  - Controller: 24h
  - Token Service: 24h
- **Renewal:** cert-manager auto-renews certificates at 2/3 lifetime
- **Idle pod replacement:** The WarmPoolController proactively replaces any idle pod whose certificate will expire within 30 minutes

### Gateway mTLS Identity

Each gateway replica has a **distinct mTLS identity**:
- Compromise of one replica is attributable and revocable independently
- Certificates are scoped to the replica's ServiceAccount
- The Token Service validates per-replica identity on every credential request

### SPIFFE Trust Domain

SPIFFE (Secure Production Identity Framework for Everyone) is the identity framework Lenny uses to give each pod a verifiable cryptographic identity. Deployers **MUST** override `global.spiffeTrustDomain` (default: `lenny`) for shared-cluster deployments to prevent cross-deployment pod impersonation. Each Lenny deployment sharing a cluster must use a unique trust domain. Additionally, set `global.saTokenAudience` to a deployment-specific value (default: `lenny-gateway-default`).

```yaml
global:
  spiffeTrustDomain: "prod.lenny.example.com"
  saTokenAudience: "lenny-prod"
```

Failure to override these values in a shared cluster allows pods from one Lenny deployment to present valid pod identities belonging to another deployment's trust domain, enabling credential lease theft.

### Alert

The `CertExpiryImminent` warning alert fires when any mTLS cert expiry is less than 1 hour away, indicating a cert-manager failure.

---

## OIDC / OAuth 2.1 Authentication

### Client Authentication

The gateway authenticates all client requests via OIDC (OpenID Connect, the identity-provider layer on top of OAuth 2.0) and OAuth 2.1:

```yaml
auth:
  issuerUrl: "https://auth.example.com"
  audience: "lenny-api"
  tenantIdClaim: "tenant_id"           # OIDC claim for tenant extraction
```

### Tenant ID Extraction

| Configuration | Behavior |
|---|---|
| Single-tenant mode | `tenantIdClaim` is ignored; all requests use the `default` tenant |
| Multi-tenant mode | Gateway reads `tenantIdClaim` from the validated OIDC ID token |

**Rejection conditions:**

| Condition | Response |
|---|---|
| Claim absent or empty string | `401 Unauthorized` with error code `TENANT_CLAIM_MISSING` |
| Claim value doesn't match any tenant | `403 Forbidden` with error code `TENANT_NOT_FOUND` |

Both rejection reasons are logged (INFO level, with `user_id` and `jti`) and emitted as `auth_failure` audit events.

---

## Token Service

### Architecture

The Token Service runs as a **separate process** with its own ServiceAccount and KMS access:

- Only component with KMS decrypt permissions for downstream OAuth tokens
- Gateway replicas call the Token Service over mTLS
- Gateway replicas receive short-lived access tokens, never refresh tokens or KMS keys
- 2+ replicas with `PodDisruptionBudget` (`minAvailable: 1`)
- Serves the canonical `POST /v1/oauth/token` endpoint (RFC 6749 + RFC 8693 token exchange) for all bearer-token minting

### What It Manages

1. **Connector credentials** -- OAuth tokens for external tools and agents (GitHub, Jira, etc.) accessed via registered connectors
2. **LLM provider credentials** -- API keys, cloud IAM roles for backing LLMs
3. **Bearer tokens** -- admin tokens, session tokens, delegation child tokens, credential-lease tokens, agent-operability scoped tokens. Every token minted by Lenny flows through the RFC 8693 token-exchange path, either by direct caller request (admin rotation via `lenny-ctl`) or by internal Token Service calls (credential leasing, delegation minting). RFC 8693 semantic preservation is enforced: scope may only narrow, `delegation_depth` may only increase, audience may not broaden, `caller_type` may not elevate.

### Token Storage

| Token Type | Storage | Encryption |
|---|---|---|
| Refresh tokens | Postgres | Envelope encryption via KMS |
| Access tokens (cache) | Redis | Encrypted, short-lived |
| Per-user OAuth tokens | Postgres (via Token Service) | KMS-encrypted at rest |

### Failure Handling

The gateway wraps Token Service calls in a circuit breaker:

| Condition | Impact |
|---|---|
| Token Service unavailable | Existing leases continue; new credential-requiring sessions fail (retryable) |
| Sessions without credentials | Unaffected |
| Cached leases | Continue until lease expiry (grace period) |

The `TokenServiceUnavailable` critical alert fires when the circuit breaker has been in open state for > 30 seconds.

---

## KMS Integration

### Envelope Encryption

Lenny uses envelope encryption for sensitive data:

1. A Data Encryption Key (DEK) encrypts the actual data
2. The DEK is encrypted (wrapped) by a Key Encryption Key (KEK) in KMS
3. Only the Token Service has KMS decrypt permissions

### Supported KMS Providers

| Backend | Purpose |
|---|---|
| AWS KMS | JWT signing, DEK wrapping, etcd Secret encryption (EKS) |
| GCP Cloud KMS | JWT signing, DEK wrapping, etcd Secret encryption (GKE) |
| HashiCorp Vault Transit | JWT signing, DEK wrapping |
| Azure Key Vault | etcd Secret encryption (AKS) |

### What Gets Encrypted

| Data | Encryption Method |
|---|---|
| OAuth refresh tokens | Envelope encryption via KMS |
| LLM provider API keys | Kubernetes Secrets (etcd at-rest encryption) |
| Redis token cache | Application-level encryption |
| Credential lease tokens | Short-lived, signed JWTs |

---

## Credential Leasing

### Credential Pool Model

Credentials are organized into pools with health tracking and lease management:

```yaml
credentialPools:
  - name: anthropic-prod
    provider: anthropic_direct
    deliveryMode: proxy
    maxLeases: 50
    credentials:
      - id: key-1
        secretRef: lenny-system/anthropic-key-1
```

### Delivery Modes

**Proxy mode** (recommended):

1. Pod sends LLM requests to the gateway's LLM Proxy (port 8443)
2. Gateway validates the lease token
3. Gateway injects the real API key
4. Gateway forwards to the upstream LLM provider
5. **Key never enters the pod**

**Direct mode** (default for single-tenant; permitted in multi-tenant only with `sandboxed` or `microvm` isolation):

1. Gateway writes the API key to `/run/lenny/credentials.json` on the pod
2. Pod contacts the LLM provider directly
3. Credential file is removed on session end or between tasks

The combination of `deliveryMode: direct` + `isolationProfile: standard` (runc) is **blocked by admission control** in multi-tenant mode — a container escape under runc would expose materialized credential material across tenants. Pool registration returns `DirectModeStandardIsolationMultiTenantRejected`.

### Credential Lifecycle

```
Available → Leased → (Active use) → Released → Available
                ↓
         Rate-limited → Cooldown → Available
                ↓
           Revoked → (requires re-enable)
```

### Emergency Credential Revocation

When a credential is compromised:

```bash
lenny-ctl admin credential-pools revoke-credential \
  --pool anthropic-prod \
  --credential key-1 \
  --reason "Key exposed in logs"
```

This immediately:
1. Terminates all active leases against the credential
2. Removes the credential from the assignable pool
3. Emits a `credential.revoked` billing event
4. Fires the `CredentialCompromised` alert if active leases persist > 30s

---

## LLM Proxy

### Architecture

The LLM Proxy is the gateway subsystem that talks to LLM providers on behalf of agent pods. It runs inside the gateway process (it's one of four internal subsystem boundaries), which means agent pods never hold real provider API keys — keys live only in the gateway's in-memory credential cache. On every request, the subsystem validates the pod's lease token, verifies the pod's cryptographic identity, runs the `PreLLMRequest` / `PostLLMResponse` interceptor chain, converts between the agent pod's dialect (OpenAI or Anthropic, per the pool's `proxyDialect`) and the upstream provider's wire format, injects the real upstream API key from the gateway's in-memory credential cache, forwards to the upstream provider, and extracts authoritative token usage from the response.

**See also:** [`external-llm-proxy.md`](external-llm-proxy.md) for deployers who want to route traffic through an external LLM gateway (LiteLLM, Portkey, cloud-managed) as the upstream.

### Security posture

- Real API keys live only in the gateway process's in-memory credential cache. They are never written to disk, never placed on tmpfs, and never entered into the agent pod's environment, memory, filesystem, or network path.
- Agent-pod wire format is provider-agnostic. Runtimes use standard OpenAI or Anthropic SDKs; the upstream provider can change without any runtime code change.
- The translator runs inside the gateway process. It has no separate pod identity; outbound provider connections use the gateway pod's network identity.
- Credential rotation does not interrupt traffic. The gateway's credential cache is refreshed atomically on lease rotation; the next outbound call reads the new key. No reload signal, no file replacement, no SIGHUP.
- Lease revocation is enforced before the translator runs. Expired or revoked leases are rejected by the subsystem's lease check before any upstream call is attempted.

### Network Isolation

Only pods in pools with `deliveryMode: proxy` can reach the Lenny proxy port (8443). The NetworkPolicy `allow-pod-egress-llm-proxy` is applied selectively using the `lenny.dev/delivery-mode: proxy` label. The gateway pod's own egress to upstream providers is governed by `allow-gateway-egress-llm-upstream` (see [§13.2](../../spec/13_security-model.md#132-network-isolation) NET-046).

### Token Counting

In proxy mode, the gateway's translator extracts `input_tokens` and `output_tokens` directly from the upstream LLM response metadata. These upstream-extracted counts are **authoritative** -- `ReportUsage` calls from pods are ignored, preventing malicious runtimes from underreporting.

---

## Pod Security Controls

### Security Context

Every agent pod runs with:

| Control | Value |
|---|---|
| User | Non-root (specific UID/GID) |
| Capabilities | All dropped |
| Root filesystem | Read-only |
| Writable paths | tmpfs (`/tmp`), workspace, sessions, artifacts |
| Credentials | No standing credentials; short-lived lease only |
| File delivery | Gateway-mediated only |

### Adapter-Agent Boundary

The adapter-agent connection within a pod is secured via:

- **Primary:** `SO_PEERCRED` UID check + manifest nonce
- **Fallback (gVisor):** Nonce + per-connection HMAC challenge-response

If `SO_PEERCRED` is unavailable (gVisor divergence), the pool is marked with `SecurityDegradedMode=True` and an alert fires.

---

## Network Policies

### Default-Deny Foundation

Every agent namespace starts with a default-deny-all policy:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-all
  namespace: lenny-agents
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
```

### Allowed Communication Paths

| Policy | Direction | Source/Destination | Port |
|---|---|---|---|
| `allow-gateway-ingress` | Ingress | Gateway pods | 50051 (gRPC) |
| `allow-pod-egress-base` | Egress | Gateway pods, CoreDNS | 50051, 53 |
| `allow-pod-egress-llm-proxy` | Egress | Gateway pods (proxy-mode only) | 8443 |
| `allow-pod-egress-otlp` | Egress | OTLP collector (when configured) | 4317 |
| `allow-pod-egress-internet` | Egress | External (internet-egress pools) | Configurable |

### Provider-Direct Egress Profile

Pools with `egressProfile: provider-direct` receive a supplemental policy that allows outbound traffic to LLM provider endpoints only (Anthropic, OpenAI, AWS Bedrock, GCP Vertex). CIDR ranges are maintained in the Helm values (`egressCIDRs.providers`) and must be updated by deployers when provider endpoints change.

**Mutual exclusivity with proxy delivery:** A pool **cannot** use `deliveryMode: proxy` with `egressProfile: provider-direct`. The proxy mode routes all LLM traffic through the gateway to prevent API keys from reaching pods, but `provider-direct` gives pods a direct network path to providers -- combining them creates an incoherent security posture. The correct pairings are:

- `deliveryMode: proxy` with `egressProfile: restricted` (traffic goes only to the gateway proxy)
- `deliveryMode: direct` with `egressProfile: provider-direct` (pod contacts provider directly with a short-lived lease)

Pool registration validation rejects configurations that violate this constraint with an `InvalidPoolEgressDeliveryCombo` error.

### IMDS Blocking

Network policies block cloud metadata service (IMDS) addresses via `except` clauses on supplemental policies that include broad CIDR rules. The blocked addresses are:

- `169.254.169.254/32` (AWS/GCP/Azure IPv4 IMDS)
- `fd00:ec2::254/128` (AWS IPv6 IMDS)
- `100.100.100.200/32` (Alibaba Cloud IMDS)

These are configurable via the `egressCIDRs.excludeIMDS` Helm value. Deployers can extend the list for additional cloud providers. The base `allow-pod-egress-base` policy implicitly blocks IMDS because it is an allowlist-only policy (gateway gRPC + DNS only). Supplemental policies (`provider-direct`, `internet`) carry explicit `except` blocks.

### Internet Egress Profile

Pools with `egressProfile: internet` receive a supplemental policy that allows external traffic while excluding cluster-internal CIDRs:

```yaml
spec:
  podSelector:
    matchLabels:
      lenny.dev/egress-profile: internet
  egress:
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - 10.0.0.0/8          # Pod CIDR
              - 172.16.0.0/12       # Service CIDR
              - 169.254.169.254/32  # IMDS (AWS/GCP/Azure IPv4)
              - fd00:ec2::254/128   # IMDS (AWS IPv6)
              - 100.100.100.200/32  # IMDS (Alibaba Cloud)
```

`egressProfile: internet` requires a `sandboxed` or `microvm` isolation profile — pool admission rejects the combination with `standard` (runc).

The `NetworkPolicyCIDRDrift` critical alert fires when the configured exclusion CIDRs no longer match actual cluster CIDRs.

### Dedicated CoreDNS

Agent namespaces use a dedicated CoreDNS deployment (not the cluster-wide `kube-dns`) to:
- Apply DNS-level filtering and rate limiting
- Log DNS queries for security auditing
- Isolate agent DNS from cluster DNS failures

The `DedicatedDNSUnavailable` critical alert fires when all dedicated CoreDNS replicas have zero ready pods.

---

## RBAC Model

### Platform Roles

| Role | Scope | Capabilities |
|---|---|---|
| `platform-admin` | Cluster-wide | Full admin API access across all tenants; manage runtimes, pools, global configuration |
| `tenant-admin` | Per-tenant | Full access scoped to own tenant; user management, quotas, credential pools, legal holds |
| `tenant-viewer` | Per-tenant | Read-only access to own tenant's sessions, runtimes, pools, usage, configuration |
| `billing-viewer` | Per-tenant | Read-only access to usage and metering data for own tenant; no session content |
| `user` | Per-tenant | Create and manage own sessions; no access to other users' sessions without explicit grant |

### Kubernetes RBAC

| ServiceAccount | Namespace | Purpose | Key Permissions |
|---|---|---|---|
| `lenny-gateway` | `lenny-system` | Gateway replicas | Read/write Sandbox CRDs, read Secrets |
| `lenny-controller` | `lenny-system` | Warm Pool Controller | Full Sandbox CRD management |
| `lenny-token-service` | `lenny-system` | Token Service | KMS decrypt, Secret read |

---

## Audit Logging

### Audit Event Properties

Every audit event includes:
- `session_id`, `tenant_id`, `trace_id`, `span_id`
- Structured error codes (TRANSIENT/PERMANENT/POLICY/UPSTREAM)
- Timestamp and sequence number

### Wire Format -- OCSF v1.1.0

Every audit event that leaves the Postgres hot tier (SIEM forwarding, pgaudit sink, webhook subscribers, `/v1/admin/audit-events` query responses) is serialized as an [OCSF v1.1.0](https://schema.ocsf.io/1.1.0/) JSON record. OCSF is the single wire format with no alternative format switch. The hash chain is computed over the canonical Postgres tuple (not OCSF bytes), so OCSF translation cannot affect chain integrity. See [Audit (OCSF wire format)](audit-ocsf.md) for the full field mapping, SIEM delivery configuration, and verifier guidance.

### Credential-Sensitive Exclusions

RPCs that handle credentials (`AssignCredentials`, `RotateCredentials`) are excluded from:
- Payload-level logging
- gRPC access logs
- OTel trace span attributes

Only RPC name, lease ID, provider type, and outcome are recorded.

### Audit Chain Integrity

Audit events use a hash chain for tamper detection:
- Each event references the hash of the previous event (`prev_hash`)
- Startup chain-continuity check detects gaps
- `AuditChainGap` alert fires if a broken chain is detected

### Compliance Profiles

| Profile | Requirements |
|---|---|
| `soc2` | SIEM required for regulated tenants; 365-day retention |
| `fedramp` | SIEM required; pgaudit enabled; grant-check interval enforced |
| `hipaa` | SIEM required; 6-year audit retention; 6-year billing retention |
| `none` | No additional requirements (default) |

### pgaudit Requirement

For regulated compliance profiles (`soc2`, `fedramp`, `hipaa`), `audit.pgaudit.enabled` **must** be set to `true` to provide tamper-evident database audit logging. When enabled, the gateway's startup preflight check validates that the `pgaudit` extension is installed and that `pgaudit.log` includes `DDL` and `ROLE` classes. If validation fails in production mode with a regulated `complianceProfile`, the gateway refuses to start.

```yaml
audit:
  pgaudit:
    enabled: true
    sinkEndpoint: "https://audit-sink.example.com"
```

pgaudit captures any `GRANT`, `REVOKE`, or DDL statement on audit tables -- including those by superusers -- and streams them to an external append-only sink. This closes the residual tamper window that remains between periodic grant checks.

### Cosign Image Verification

Container image signing is verified via a cosign `ValidatingAdmissionWebhook` configured as **fail-closed** (`failurePolicy: Fail`). If the webhook is unavailable, pod admission is blocked for agent namespaces -- no new pods can be scheduled, halting warm pool replenishment.

The `CosignWebhookUnavailable` critical alert fires when the cosign webhook endpoint returns errors for > 60 seconds. See the admission webhook outage runbook for recovery steps.

Set the compliance profile per tenant or globally:

```yaml
tenants:
  - name: healthcare-tenant
    complianceProfile: hipaa
```

---

## Data Classification

Lenny uses a 4-tier data classification model configured per-tenant via the `workspaceTier` field:

| Tier | Label | Description |
|---|---|---|
| T1 | Public | No sensitivity constraints |
| T2 | Internal | Standard encryption, configurable retention |
| T3 | Confidential | Encryption at rest, audit logging, data residency enforcement |
| T4 | Restricted | Dedicated node pools, per-tenant KMS key isolation, cross-region transfer prohibited |

Each tier sets escalating requirements for encryption, retention, access control, and audit:

```yaml
tenants:
  - name: sensitive-tenant
    workspaceTier: "T4"
```

When a tenant sets `workspaceTier: T4`, the platform applies Restricted-tier controls to all workspace files, snapshots, and session transcripts. T4 requires dedicated node pools for per-tenant key isolation. The setting is inherited by all environments under the tenant unless explicitly overridden to a **stricter** (never looser) tier.

---

## GDPR Erasure

Lenny implements a 19-step `DeleteByUser` sequence that covers all storage layers in dependency order, ensuring complete data removal across Postgres, Redis, MinIO, and KMS.

### Erasure Process

- **Processing restriction (Article 18):** Before full deletion, a `processing_restricted` flag can be set on a user, blocking all new session creation while the erasure job runs.
- **Erasure SLA:** 72 hours for T3 data, 1 hour for T4 data.
- **Billing pseudonymization:** Configurable per-tenant via `billingErasurePolicy`:
  - `default` -- billing events are pseudonymized using a per-tenant `erasure_salt` (salt is deleted immediately after pseudonymization for GDPR Recital 26 compliance)
  - `exempt` -- billing events retained as-is for financial reconciliation (no pseudonymization occurs)
- **Cryptographic receipt:** An erasure receipt is stored in the audit trail recording each phase's completion timestamp, errors, and final state.

### Initiating Erasure

```bash
# Initiate user-level erasure (returns a job ID)
curl -X POST \
  -H "Authorization: Bearer $LENNY_API_TOKEN" \
  "$LENNY_API_URL/v1/admin/users/<user-id>/erase"

# Check erasure job status
lenny-ctl admin erasure-jobs get <job-id>

# Retry a failed job
lenny-ctl admin erasure-jobs retry <job-id>
```

The `ErasureJobFailed` and `ErasureJobOverdue` alerts fire if the erasure job fails or exceeds its tier-specific deadline.

---

## Content Policy / Request Interceptors

The `RequestInterceptor` chain provides 12 hook phases spanning the full request lifecycle (`PreAuth`, `PostAuth`, `PreRoute`, `PreDelegation`, `PreMessageDelivery`, `PostRoute`, `PreToolResult`, `PostAgentOutput`, `PreLLMRequest`, `PostLLMResponse`, `PreConnectorRequest`, `PostConnectorResponse`).

### Configuration

Interceptors are configured via the `interceptors` Helm values. External interceptors are invoked via gRPC (like Kubernetes admission webhooks) and can `ALLOW`, `DENY`, or `MODIFY` content at any phase:

```yaml
interceptors:
  - name: content-filter
    endpoint: "dns:///content-filter.lenny-system:50053"
    phases: [PreDelegation, PreLLMRequest]
    timeoutMs: 500
    failPolicy: fail-closed        # fail-closed | fail-open
```

- **`fail-closed`:** Timeout or error is treated as REJECT (recommended for security-critical interceptors)
- **`fail-open`:** Timeout or error is treated as ALLOW

Content policy interceptors can be attached to delegation chains via `contentPolicy.interceptorRef` on `DelegationPolicy` definitions.
