---
layout: default
title: Security Principles
parent: "Operator Guide"
nav_order: 4.5
description: The security posture, design principles, and control primitives that make Lenny a defensible foundation for regulated workloads — without claiming certifications Lenny has not obtained.
---

# Security Principles

{: .no_toc }

This page describes the security posture Lenny is built to. It is a companion to the configuration-heavy [Security](security) page: start here to understand *why* Lenny enforces what it does, then use [Security](security) to see *how* each control is configured.

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

{: .warning }
> **Lenny does not claim certifications.** This page describes the **control primitives** Lenny provides and the **framework clauses** those primitives are designed to support. The certification itself (SOC 2, HIPAA, ISO 27001, FedRAMP, PCI DSS, etc.) is a property of your deployment, not of the Lenny codebase. Your auditor evidences your deployment; Lenny's job is to give you the controls your auditor will ask for.

---

## 1. Design principles

### 1.1 The pod is the trust boundary

Every session runs in its own Kubernetes pod. Pods do not share filesystems, credentials, or long-lived network paths. Cross-session leakage through `/tmp`, environment, or a shared in-process cache is architecturally impossible because nothing is shared. The gateway — not the pod — is the sole intermediary that sees cross-session context, and it sees only the slivers a given request requires.

### 1.2 Default-deny networking

Every agent pod starts with a NetworkPolicy that denies all ingress and egress by default. Allowed paths — gateway-to-pod mTLS, pod-to-gateway mTLS, DNS, and the explicit egress allowlist for LLM providers and connectors — are added back one at a time. An operator who misconfigures a NetworkPolicy fails closed: the pod has no connectivity and the session errors, instead of unintentionally gaining exfiltration paths.

### 1.3 Credentials are leased, never embedded

Agent pods never receive raw LLM provider API keys, connector OAuth tokens, or long-lived secrets. Instead, the gateway:

1. Resolves the right credential at session claim time via the Token Service.
2. Proxies all provider traffic from the gateway (LLM Proxy), injecting the credential server-side.
3. Revokes the lease as soon as the session ends.

Compromising a pod yields no usable long-term credential material.

### 1.4 Cryptographic pod identity, rotated aggressively

Each pod has a distinct mTLS certificate issued by cert-manager, scoped to its ServiceAccount, and bound to a SPIFFE trust domain unique per Lenny deployment. Certificates expire in hours, not days. An attacker who extracts a cert material from one pod has a short window and no lateral reach.

### 1.5 KMS envelope encryption for sensitive data

Every piece of data classified as T3 or T4 — workspace files at rest, credential ciphertext, audit logs, webhook secrets — is encrypted at the application layer with KMS-envelope encryption. The database role with read access to the encrypted column cannot decrypt without a separate KMS permission.

### 1.6 Audit logging is tamper-evident by default

Audit events are emitted in OCSF v1.1.0 format, stream to an external append-only sink (SIEM), and are backed by pgaudit when running in `soc2`, `fedramp`, or `hipaa` compliance profiles. The chain-integrity check detects gaps. An operator cannot silently drop events without the integrity monitor noticing.

### 1.7 Isolation chosen, not assumed

Three isolation profiles: `standard` (runc — dev only), `sandboxed` (gVisor, the production default), `microvm` (Kata Containers). Production deployments run sandboxed or microvm. A session's isolation profile cannot monotonically weaken when it delegates to child sessions — `ISOLATION_MONOTONICITY_VIOLATED` blocks the request before any child pod starts.

### 1.8 Least privilege at every layer

Kubernetes RBAC grants each gateway component only the verbs it needs. The gateway cannot `exec` into agent pods. The agent adapter cannot reach the Kubernetes API at all. Gateway database access is split across distinct Postgres roles (`lenny_app`, `lenny_token`, `lenny_audit`, `lenny_read`) so that a compromised component gets a narrow slice of the data, not the whole store.

### 1.9 Self-hosted and auditable

Lenny runs entirely inside the operator's cluster. There is no managed service component, no phone-home telemetry, and no data that leaves the perimeter you control unless you explicitly wire it to leave (for example, a callback URL or an external SIEM). The entire codebase is open-source under the MIT license; reviewers can evidence control behavior from source, not just from documentation.

### 1.10 Secure by default, not secure-if-configured

Defaults are production-safe. Turning a control off is an explicit action — `allowStandardIsolation: true`, `networkPolicy.defaultDeny: false` — that leaves an audit trail in your values file. A forgotten setting does not silently downgrade the posture.

---

## 2. Control primitives

Each primitive below has a canonical configuration surface on the [Security](security) page and reference material elsewhere in this guide.

| Primitive | What it does | Where it's configured |
|:----------|:-------------|:----------------------|
| **mTLS everywhere** | Mutual TLS between gateway replicas and every agent pod. Per-replica and per-pod identities. | [Security — mTLS](security#mtls-gateway-to-pod) |
| **SPIFFE trust domain** | Cryptographic identity per deployment; prevents cross-deployment pod impersonation. | [Security — SPIFFE Trust Domain](security#spiffe-trust-domain) |
| **OIDC/OAuth 2.1 auth** | Every client request carries a validated IdP token; admin token managed separately. | [Security — OIDC / OAuth 2.1 Authentication](security#oidc--oauth-21-authentication) |
| **Token Service** | Stateless credential lifecycle manager; KMS-backed; per-replica mTLS. | [Security — Token Service](security#token-service) |
| **KMS envelope encryption** | AWS KMS / GCP KMS / Azure Key Vault / HashiCorp Vault; application-layer crypto for T3/T4 data. | [Security — KMS Integration](security#kms-integration) |
| **Credential leasing** | Per-session / per-task / per-slot leases; emergency revocation API. | [Security — Credential Leasing](security#credential-leasing) |
| **LLM Proxy** | Outbound provider traffic flows from the gateway, not from pods. | [Security — LLM Proxy](security#llm-proxy) |
| **Pod security controls** | PSS Restricted, non-root, read-only rootfs, dropped capabilities, adapter-agent boundary. | [Security — Pod Security Controls](security#pod-security-controls) |
| **Default-deny NetworkPolicies** | Foundation policy denies everything; allowed paths added explicitly. | [Security — Network Policies](security#network-policies) |
| **IMDS blocking** | Cloud metadata endpoints (169.254.169.254, etc.) blocked in all agent namespaces. | [Security — IMDS Blocking](security#imds-blocking) |
| **Dedicated CoreDNS** | Agent DNS queries go through a CoreDNS instance with strict resolution policies. | [Security — Dedicated CoreDNS](security#dedicated-coredns) |
| **RBAC (K8s and platform)** | Built-in and custom roles; ServiceAccount-level permissions. | [Security — RBAC Model](security#rbac-model) |
| **Audit logging (OCSF v1.1.0)** | Structured, tamper-evident, integrity-checked event stream. | [Security — Audit Logging](security#audit-logging) |
| **pgaudit** | Database-layer audit of all DDL/ROLE statements; required under regulated profiles. | [Security — pgaudit Requirement](security#pgaudit-requirement) |
| **Cosign image verification** | Fail-closed admission webhook blocks unsigned images. | [Security — Cosign Image Verification](security#cosign-image-verification) |
| **Data classification (T1–T4)** | 4-tier scheme driving encryption, retention, residency, and node-pool isolation. | [Security — Data Classification](security#data-classification) |
| **Isolation profiles** | `standard` (runc, dev only) / `sandboxed` (gVisor, default) / `microvm` (Kata). | [Namespace and Isolation](namespace-and-isolation) |
| **Isolation monotonicity** | Delegation cannot weaken isolation. Blocked at the gateway. | [Namespace and Isolation](namespace-and-isolation) |
| **GDPR erasure** | 19-step `DeleteByUser` across Postgres, Redis, MinIO, KMS; cryptographic receipt. | [Security — GDPR Erasure](security#gdpr-erasure) |
| **Compliance profiles** | `soc2`, `fedramp`, `hipaa`, `none` — apply pre-bundled control bundles. | [Security — Compliance Profiles](security#compliance-profiles) |
| **SSRF mitigations (callbacks)** | URL validation, DNS pinning, private-IP rejection, isolated worker, optional allowlist. | [Spec §14 — callbackUrl](https://github.com/lennylabs/lenny/blob/main/spec/14_workspace-plan-schema.md) |
| **Input validation and size caps** | Every gateway-facing endpoint enforces schema validation, payload caps, and rate limits. | [Wire Format](../client-guide/wire-format) |

---

## 3. Certification readiness

Lenny's controls map to common framework clauses as follows. **This is not a certification claim**: it is a mapping from Lenny primitives to the clauses those primitives can support when combined with your deployment procedures, personnel controls, physical security, vendor management, and audit evidence collection.

Mappings cite widely-used public frameworks. Your auditor's interpretation governs.

### 3.1 SOC 2 Trust Services Criteria

| TSC | Criterion (abbreviated) | Lenny primitives that contribute |
|:----|:------------------------|:---------------------------------|
| CC6.1 | Logical access — identification/authentication | OIDC/OAuth 2.1, mTLS, SPIFFE |
| CC6.2 | New access authorization | Roles (`platform-admin`, `tenant-admin`, `tenant-viewer`, `billing-viewer`, `user`); custom roles API |
| CC6.3 | Access removed on termination | Emergency credential revocation; lease-based access |
| CC6.6 | Logical access to systems and data | Default-deny NetworkPolicies, RBAC, data classification |
| CC6.7 | Protect sensitive data in transit/at rest | mTLS, KMS envelope encryption |
| CC6.8 | Prevention/detection of malicious code | Cosign admission (fail-closed), PSS Restricted, gVisor/Kata |
| CC7.1 | System monitoring for security events | OCSF audit stream, `auth_failure` events, circuit breakers |
| CC7.2 | Incident response | Emergency credential revocation, isolation monotonicity, audit chain integrity |
| CC7.3 | Evaluate/communicate security events | SIEM sink (required for regulated tenants) |
| CC8.1 | Change management | Image signature verification, `RuntimeUpgrade` CRD with controlled rollout |

### 3.2 ISO/IEC 27001:2022 Annex A

| Control | Title | Lenny primitives |
|:--------|:------|:-----------------|
| A.5.15 | Access control | RBAC (K8s and platform), tenant scoping |
| A.5.16 | Identity management | OIDC/OAuth 2.1, SPIFFE, per-pod mTLS identity |
| A.8.2 | Privileged access rights | Admin token; split Postgres roles; least-privilege ServiceAccounts |
| A.8.3 | Information access restriction | Data classification (T1–T4); per-tenant row-level security |
| A.8.5 | Secure authentication | OIDC; short-lived mTLS certs; KMS-protected secrets |
| A.8.7 | Protection against malware | Cosign verification; PSS Restricted; sandboxed runtimes |
| A.8.9 | Configuration management | Declarative `values.yaml`; preflight validation; helm-managed state |
| A.8.15 | Logging | OCSF audit events; pgaudit; SIEM streaming |
| A.8.16 | Monitoring activities | Prometheus metrics; burn-rate alerts; `auth_failure` audit |
| A.8.21 | Security of network services | Default-deny NetworkPolicies; LLM Proxy; dedicated CoreDNS |
| A.8.23 | Web filtering | Internet egress profile; domain allowlists |
| A.8.24 | Use of cryptography | KMS envelope encryption; HMAC-signed webhooks; mTLS |
| A.8.26 | Application security requirements | Input validation; schema-versioned APIs; SSRF mitigations |

### 3.3 HIPAA Security Rule (45 CFR §164.308, §164.312)

| Safeguard | Citation | Lenny primitives |
|:----------|:---------|:-----------------|
| Access control — unique user identification | §164.312(a)(2)(i) | OIDC subject; per-user session attribution |
| Access control — automatic logoff | §164.312(a)(2)(iii) | Short mTLS cert TTL; session idle timeout |
| Access control — encryption/decryption | §164.312(a)(2)(iv) | KMS envelope encryption |
| Audit controls | §164.312(b) | OCSF audit stream; pgaudit; chain integrity |
| Integrity controls | §164.312(c)(1) | KMS-signed audit records; HMAC-signed webhooks |
| Transmission security — integrity | §164.312(e)(1) | mTLS; HMAC webhook signatures |
| Transmission security — encryption | §164.312(e)(2)(ii) | mTLS; TLS for all external egress |
| Workforce security — authorization | §164.308(a)(3)(ii)(B) | RBAC; tenant scoping |
| Information access management | §164.308(a)(4) | Role hierarchy; credential leasing |
| Security incident procedures | §164.308(a)(6) | Audit integrity monitoring; emergency revocation |

### 3.4 FedRAMP (NIST 800-53 control families)

| Family | Example controls | Lenny primitives |
|:-------|:-----------------|:-----------------|
| AC (Access Control) | AC-2, AC-3, AC-6, AC-17 | RBAC, least privilege, tenant scoping, mTLS |
| AU (Audit and Accountability) | AU-2, AU-3, AU-6, AU-9, AU-12 | OCSF, chain integrity, pgaudit, SIEM streaming |
| IA (Identification and Authentication) | IA-2, IA-4, IA-5 | OIDC/OAuth 2.1, SPIFFE, short-lived mTLS |
| SC (System and Communications Protection) | SC-7, SC-8, SC-12, SC-13, SC-39 | Default-deny NetworkPolicies, mTLS, KMS, isolation profiles |
| SI (System and Information Integrity) | SI-3, SI-4, SI-7, SI-10 | Cosign, default-deny, audit chain integrity, input validation |
| CM (Configuration Management) | CM-2, CM-5, CM-7 | Declarative configuration, RuntimeUpgrade CRD, preflight |

### 3.5 PCI DSS v4.0

PCI DSS primarily applies to systems that **store, process, or transmit cardholder data (CHD)**. Lenny's architecture keeps CHD out of agent pods unless explicitly placed there; most PCI obligations accrue to the integrating application rather than to Lenny itself. When a deployment does include PCI-scoped data, the following primitives contribute:

| Requirement | Theme | Lenny primitives |
|:------------|:------|:-----------------|
| 1.x | Network segmentation | Default-deny NetworkPolicies; dedicated node pools for T4 |
| 2.x | Secure configuration | PSS Restricted; Cosign; declarative values |
| 3.x | Protect stored CHD | KMS envelope encryption; T4 dedicated key isolation |
| 4.x | Protect CHD in transit | mTLS; TLS egress |
| 6.x | Secure software development | Schema-versioned APIs; input validation; SSRF mitigations |
| 7.x | Restrict access by need-to-know | RBAC; tenant scoping; credential leasing |
| 8.x | Identify and authenticate users | OIDC/OAuth 2.1; SPIFFE |
| 10.x | Log and monitor | OCSF; pgaudit; SIEM |

### 3.6 GDPR (Regulation (EU) 2016/679)

| Article | Obligation | Lenny primitives |
|:--------|:-----------|:-----------------|
| 5(1)(f) | Integrity and confidentiality | Encryption in transit and at rest; default-deny |
| 17 | Right to erasure | `DeleteByUser` 19-step sequence; cryptographic receipt |
| 18 | Right to restriction | `processing_restricted` flag |
| 25 | Data protection by design and by default | Secure-by-default configuration; T4 dedicated isolation |
| 30 | Records of processing activities | OCSF audit stream |
| 32 | Security of processing | All of §3.1–§3.3 above |
| 33 | Breach notification | Audit chain integrity; incident detection metrics |
| 44–49 | International transfers | Data residency enforcement; `REGION_CONSTRAINT_VIOLATED` |

Recital 26 pseudonymization (for billing event retention under the `default` erasure policy) uses a per-tenant `erasure_salt` that is deleted at pseudonymization time — a one-way operation that removes the link between the pseudonymized record and the original subject.

---

## 4. What Lenny does not do

These are **explicit non-goals** — we do not claim them and operators should not assume them:

- Lenny is not a SIEM. Ship audit events to Splunk, Elastic, Datadog, etc.
- Lenny is not a secrets manager. It integrates with AWS KMS, GCP KMS, Azure Key Vault, and HashiCorp Vault; it does not store long-lived secrets itself.
- Lenny is not a WAF. Use a WAF in front of the gateway for layer-7 defenses against generic web attacks.
- Lenny is not an IdP. Plug in your existing OIDC provider (Google, Okta, Azure AD, Keycloak, etc.).
- Lenny does not evaluate LLM prompts for compliance content. Content policy interceptors are a hook where you plug in DLP, PII detection, or jailbreak detection from your existing provider.
- Lenny does not prevent agents from misbehaving *within* an isolated pod's budget. It prevents them from escaping the pod, exfiltrating credentials, or affecting other tenants.

---

## 5. How to produce audit evidence

When an auditor asks for evidence of a control, a typical workflow:

1. Point to the spec section that defines the behavior (e.g., [Spec §13 — Security Model](https://github.com/lennylabs/lenny/blob/main/spec/13_security-model.md)).
2. Point to the open-source implementation under [`lennylabs/lenny`](https://github.com/lennylabs/lenny).
3. Export your `values.yaml` (sanitized) showing how you configured the control.
4. Export audit events from your SIEM demonstrating the control fired for the evidence period.
5. Show the preflight output (`lenny-ctl preflight`) demonstrating the control was enabled at install and at upgrade.

The combination of open-source source, declarative configuration, and streaming audit makes "show me evidence of control X" a matter of running two queries, not a forensic investigation.

---

## Related

- [Security](security) — configuration reference for every primitive on this page
- [Namespace and Isolation](namespace-and-isolation) — the three isolation profiles and the monotonicity invariant
- [Audit Logging (OCSF)](audit-ocsf) — audit event schema and streaming
- [Spec §13 — Security Model](https://github.com/lennylabs/lenny/blob/main/spec/13_security-model.md) (source of truth)
