---
layout: default
title: Security
parent: "Operator Guide"
nav_order: 5
---

# Security

This page covers mTLS, OIDC/OAuth 2.1, the Token Service, KMS integration, credential leasing, the LLM Proxy, pod security controls, network policies, RBAC, and audit logging.

---

## mTLS (Gateway-to-Pod)

All communication between gateway replicas and agent pods uses mutual TLS (mTLS) provisioned by cert-manager.

### Certificate Lifecycle

- **Issuer:** cert-manager `ClusterIssuer` creates per-pod certificates
- **Certificate TTL:** Configurable per pool (recommended: 24h for production)
- **Renewal:** cert-manager auto-renews certificates before expiry
- **Idle pod replacement:** The WarmPoolController proactively replaces any idle pod whose certificate will expire within 30 minutes

### Gateway mTLS Identity

Each gateway replica has a **distinct mTLS identity**:
- Compromise of one replica is attributable and revocable independently
- Certificates are scoped to the replica's ServiceAccount
- The Token Service validates per-replica identity on every credential request

### Alert

The `CertExpiryImminent` warning alert fires when any mTLS cert expiry is less than 1 hour away, indicating a cert-manager failure.

---

## OIDC / OAuth 2.1 Authentication

### Client Authentication

The gateway authenticates all client requests via OIDC/OAuth 2.1:

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

The Token/Connector Service runs as a **separate process** with its own ServiceAccount and KMS access:

- Only component with KMS decrypt permissions for downstream OAuth tokens
- Gateway replicas call the Token Service over mTLS
- Gateway replicas receive short-lived access tokens, never refresh tokens or KMS keys
- 2+ replicas with `PodDisruptionBudget` (`minAvailable: 1`)

### What It Manages

1. **MCP tool tokens** -- OAuth tokens for external tools (GitHub, Jira, etc.)
2. **LLM provider credentials** -- API keys, cloud IAM roles for backing LLMs

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

| Cloud | KMS Service | Purpose |
|---|---|---|
| AWS | AWS KMS | DEK wrapping, etcd Secret encryption |
| GCP | Cloud KMS | DEK wrapping |
| Azure | Azure Key Vault | DEK wrapping, etcd Secret encryption |

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

**Direct mode** (single-tenant/dev only):

1. Gateway writes the API key to `/run/lenny/credentials.json` on the pod
2. Pod contacts the LLM provider directly
3. Credential file is removed on session end or between tasks

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

The LLM Proxy is a gateway subsystem that acts as a credential-injecting reverse proxy:

- Validates lease tokens from pods
- Injects real API keys into upstream requests
- Forwards streaming responses back to pods
- Provides gateway-observed token counting for quota enforcement

### Network Isolation

Only pods in pools with `deliveryMode: proxy` can reach the LLM Proxy port (8443). The NetworkPolicy `allow-pod-egress-llm-proxy` is applied selectively using the `lenny.dev/delivery-mode: proxy` label.

### Token Counting

In proxy mode, the gateway extracts `input_tokens` and `output_tokens` directly from upstream LLM responses. These gateway-observed counts are **authoritative** -- `ReportUsage` calls from pods are ignored, preventing malicious runtimes from underreporting.

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
              - 10.0.0.0/8       # Pod CIDR
              - 172.16.0.0/12    # Service CIDR
```

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
| `platform-admin` | Cluster-wide | Full admin API access, cross-tenant reads, credential management |
| `tenant-admin` | Per-tenant | Tenant configuration, user management, runtime access (own tenant only) |
| `user` | Per-tenant | Session creation, workspace access (subject to environment membership) |

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

Set the compliance profile per tenant or globally:

```yaml
tenants:
  - name: healthcare-tenant
    complianceProfile: hipaa
```
