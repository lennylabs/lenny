---
layout: default
title: "Operator Guide"
nav_order: 3
has_children: true
---

# Operator Guide

This guide is the comprehensive reference for operating a Lenny platform deployment. It covers installation, configuration, scaling, security hardening, observability, disaster recovery, upgrades, multi-tenancy, troubleshooting, and the `lenny-ctl` CLI.

---

## Who This Guide Is For

This guide is written for:

- **Kubernetes operators** responsible for deploying, configuring, and maintaining Lenny clusters
- **Platform engineers** building internal developer platforms that incorporate Lenny as the agent session layer
- **Site Reliability Engineers (SREs)** who own the availability, performance, and security posture of Lenny deployments
- **DevOps engineers** integrating Lenny into CI/CD pipelines and GitOps workflows

You should have hands-on experience with Kubernetes administration, Helm chart management, and infrastructure-as-code practices. Familiarity with Postgres, Redis, and object storage (MinIO or cloud equivalents) is assumed.

---

## Prerequisites

Before using this guide, ensure you have:

| Prerequisite | Minimum Version | Notes |
|---|---|---|
| Kubernetes cluster | 1.28+ | Must support RuntimeClass, NetworkPolicy (Calico, Cilium, or cloud-native CNI + Calico policy-only mode) |
| Helm | 3.12+ | Primary installation mechanism |
| kubectl | Matching cluster version | For CRD management and debugging |
| cert-manager | 1.12+ | Required for mTLS certificate lifecycle |
| Container runtime | containerd 1.7+ | Required base runtime |
| PostgreSQL | 14+ | Managed (RDS, Cloud SQL, Azure DB) or self-managed |
| Redis | 7.0+ | With TLS and AUTH enabled |

**Optional components:**

| Component | Purpose |
|---|---|
| gVisor (runsc) | Default sandboxed isolation profile (recommended for all workloads) |
| Kata Containers | MicroVM isolation for high-risk or multi-tenant workloads |
| OPA Gatekeeper or Kyverno | RuntimeClass-aware admission policies (one required for production) |
| External Secrets Operator | Synchronize credentials from external vaults at Tier 3 scale |
| KEDA | Alternative to Prometheus Adapter for HPA metric surfacing |

**Knowledge prerequisites:**

- Understanding of Kubernetes Custom Resource Definitions (CRDs) and controllers
- Familiarity with Kubernetes RBAC, NetworkPolicy, and Pod Security Standards
- Understanding of Helm values, templating, and release management
- Basic understanding of mTLS, OIDC/OAuth 2.1, and envelope encryption

---

## Recommended Reading Order

For a new deployment, read the guides in the following order:

1. **[Installation](installation.html)** -- Three installation paths (`lenny up`, `lenny-ctl install` wizard, raw Helm), cluster prerequisites, preflight checks, bootstrap, and post-install verification
2. **[Configuration](configuration.html)** -- Deep dive into `values.yaml`, answer files, runtime registration, pool configuration, credential pools, and delegation policies
3. **[Namespace and Isolation](namespace-and-isolation.html)** -- Namespace layout, Pod Security Standards, RuntimeClass enforcement, and node isolation
4. **[Security](security.html)** -- mTLS, OIDC, the `POST /v1/oauth/token` endpoint (RFC 8693 token exchange), credential leasing, KMS integration, network policies, and RBAC
5. **[Scaling](scaling.html)** -- Capacity tiers, HPA configuration, warm pool sizing, and capacity calibration
6. **[Observability](observability.html)** -- Prometheus metrics, bundled alerting rules, ServiceMonitor/PodMonitor/PrometheusRule CRDs, OpenSLO v1 export, Grafana dashboards, and log aggregation
7. **[Agent Operability](agent-operability.html)** -- `lenny-ops` architecture, diagnostic endpoints (`/v1/admin/diagnostics/*`), operational runbooks, backup/restore API, drift detection, MCP Management server, and the `doctor --fix` auto-remediation loop
8. **[Multi-Tenancy](multi-tenancy.html)** -- Tenant model, PostgreSQL RLS, per-tenant quotas, and isolation testing
9. **[Disaster Recovery](disaster-recovery.html)** -- RPO/RTO targets, HA topology, backup schedule, and recovery procedures
10. **[Upgrades](upgrades.html)** -- Gateway rolling upgrades, pool image upgrades, rollback procedures, and MCP version deprecation
11. **[Troubleshooting](troubleshooting.html)** -- Common issues, circuit breaker management, orphan reconciliation, and emergency procedures
12. **[lenny-ctl Reference](lenny-ctl.html)** -- Full CLI reference including admin commands, `lenny session` (MCP-backed), `lenny runtime init` scaffolding, `lenny up` local stack, and the `lenny-ctl install` wizard

For operators taking over an existing deployment, start with **Agent Operability** (run `lenny-ctl doctor` first) and **Observability** to understand the health signals, then read **Configuration** and **Scaling** to understand the tuning surface.

---

## Overview of Operator Responsibilities

Operating a Lenny deployment involves the following key areas:

### Day 0 -- Initial Deployment

- Provision infrastructure dependencies (Postgres, Redis, MinIO or cloud object storage)
- Run `lenny-ctl install` for a guided wizard, or compose an answer file and run `helm install` directly
- CRDs, preflight validation (`lenny-ctl preflight`), and bootstrap seed are all wired into the install flow
- Register the reference runtime catalog (installed by default) and confirm access grants
- Run `lenny-ctl doctor` to verify post-install health; re-run with `--fix` to auto-remediate common issues

### Day 1 -- Configuration and Hardening

- Register additional runtime definitions and configure warm pools
- Set up credential pools with appropriate delivery modes (proxy via the native Go LLM translator, direct, or external proxy)
- Configure delegation policies and content policies
- Harden network isolation (NetworkPolicies, admission webhooks)
- Configure OIDC/OAuth 2.1 authentication; verify `POST /v1/oauth/token` with RFC 8693 token exchange
- Set up observability stack (Prometheus Operator ServiceMonitor/PodMonitor/PrometheusRule, Grafana, bundled alerting, OpenSLO export)
- Gate or enable the bundled web playground (`playground.enabled`, `playground.authMode`)

### Day 2 -- Ongoing Operations

- Monitor platform health via `lenny-ops` operability endpoints, metrics, alerts, and dashboards
- Run `lenny-ctl doctor --fix` periodically or on alert; inspect `/v1/admin/diagnostics/*` for structured cause chains
- Scale pools and gateway replicas based on demand (capacity recommendations come from `lenny-ops`)
- Perform rolling upgrades of gateway and runtime images (`lenny-ctl upgrade --answers <file>` replays the captured answer file)
- Rotate credentials and KMS keys
- Manage tenant lifecycle (creation, quota adjustment, deletion)
- Respond to incidents using circuit breakers, runbooks (`GET /v1/admin/runbooks/*`), and emergency procedures
- Validate disaster recovery via automated restore tests (backup/restore API under `lenny-ops`)
- Audit security posture and compliance controls

### Capacity Planning

- Calibrate `maxSessionsPerReplica` using the ramp test methodology
- Size warm pools using the PoolScalingController formula
- Plan credential pool sizing based on concurrent session targets
- Evaluate subsystem extraction thresholds as load grows
- Plan tier promotions (Tier 1 to Tier 2 to Tier 3)
