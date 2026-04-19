---
layout: default
title: "Operator Guide"
nav_order: 3
has_children: true
description: Installation, configuration, scaling, security hardening, observability, backup/restore, upgrades, multi-tenancy, and the lenny-ctl CLI reference.
---

# Operator Guide

The reference for running Lenny. It covers the first install through steady-state operations -- configuration, scaling, security hardening, observability, backups and restores, upgrades, multi-tenancy, troubleshooting, and the `lenny-ctl` CLI.

{: .note }
> **Security or compliance reviewer?** You don't need the full Operator Guide. Start at [Security Principles](security-principles.html), then read [Namespace and Isolation](namespace-and-isolation.html), [Security](security.html) (configuration reference), [Audit (OCSF)](audit-ocsf.html), and [Multi-Tenancy](multi-tenancy.html). You can skip Installation, Scaling, Observability, Upgrades, and Disaster Recovery unless your certification requires capacity or continuity evidence.

---

## Who this guide is for

You're the person who:

- Installs Lenny on a Kubernetes cluster and keeps it upgraded.
- Embeds Lenny in an internal developer platform, giving other teams sessions as a service.
- Owns the availability, performance, and security of a Lenny deployment.
- Plugs Lenny into existing CI/CD and GitOps pipelines.

The guide assumes you're comfortable administering Kubernetes (custom resources, network policies, Pod Security Standards, RBAC), managing Helm charts, and operating Postgres, Redis, and S3-style object storage.

---

## Prerequisites

You'll need the following available before you install:

| Component | Minimum | Notes |
|---|---|---|
| Kubernetes cluster | 1.28+ | Must support RuntimeClass and NetworkPolicy. Any CNI that enforces network policies works -- Calico, Cilium, or a cloud-native CNI paired with Calico in policy-only mode |
| Helm | 3.12+ | The primary install mechanism |
| kubectl | matching your cluster | Used for custom-resource management and debugging |
| cert-manager | 1.12+ | Handles mTLS certificate issuance and rotation |
| Container runtime | containerd 1.7+ | The base container runtime |
| PostgreSQL | 14+ | Either managed (RDS, Cloud SQL, Azure DB for PostgreSQL) or self-managed |
| Redis | 7.0+ | TLS and authentication enabled |

**Optional but recommended:**

| Component | What it gives you |
|---|---|
| gVisor (runsc) | The default sandboxed isolation profile -- recommended for any workload you don't fully trust |
| Kata Containers | Full microVM isolation, for high-risk workloads or strict multi-tenant separation |
| OPA Gatekeeper or Kyverno | Policy-based admission control that's aware of `RuntimeClass`. You should have one of these in production |
| External Secrets Operator | Pulls credentials from an external vault (Vault, AWS Secrets Manager, etc.) into the cluster |
| KEDA | An alternative to the Prometheus Adapter for exposing metrics to the Horizontal Pod Autoscaler |

**What you should already understand:**

- Kubernetes custom resources, controllers, RBAC, network policies, and Pod Security Standards.
- Helm values, templating, and release management.
- The basics of mTLS, OIDC / OAuth 2.1, and envelope encryption.

---

## Reading paths

You don't need to read every page top to bottom. Pick the path that matches where you are.

### Day 0 — install it

You're standing up a new deployment and want to reach "session completes end-to-end" as fast as possible.

1. [**Installation**](installation.html) — the three install paths (`lenny up`, `lenny-ctl install`, raw Helm), prerequisites, preflight, bootstrap, post-install verification.
2. [**Ingress and TLS**](ingress-and-tls.html) — the three external Ingresses, cert-manager, internal mTLS, SPIFFE trust-domain setup.
3. [**Configuration**](configuration.html) — `values.yaml`, answer files, registering runtimes, configuring pools and credential pools, delegation policies.
4. [**Namespace and Isolation**](namespace-and-isolation.html) — namespace layout, Pod Security Standards, sandbox enforcement, dedicating nodes.
5. Run `lenny-ctl doctor` — confirms the install is healthy; `--fix` auto-remediates known misconfigurations.

Output of Day 0: a cluster you can point a client at.

### Day 1 — make it your first production service

You have a working install. Now harden it, wire observability, decide on auth and tenancy.

6. [**Security Principles**](security-principles.html) — the posture and the control map (skim this even if you're not the security reviewer — it explains what the next page is for).
7. [**Security**](security.html) — configuration reference for mTLS, OIDC, Token Service, KMS, credential leasing, RBAC.
8. [**Observability**](observability.html) — Prometheus metrics, bundled alerting rules, the Prometheus Operator CRs, OpenSLO export, Grafana dashboards. Read this before Scaling — you'll use the signals it surfaces to pick the right autoscaler targets.
9. [**Scaling**](scaling.html) — sizing by deployment size, autoscaler configuration, warm pool sizing, capacity calibration.
10. [**Multi-Tenancy**](multi-tenancy.html) — RLS, per-tenant quotas, isolation testing — needed before you open the platform to more than one team.

Output of Day 1: a deployment that's ready to carry real traffic and that your SRE and security teams can evidence.

### Day 2 — keep it running

Steady-state operations and the incident path. This section also covers you if you inherited a deployment someone else built.

11. [**Agent Operability**](agent-operability.html) — the management plane (`lenny-ops`), diagnostic endpoints, [runbooks]({{ site.baseurl }}/runbooks/), drift detection, backup/restore, `lenny-ctl doctor --fix`.
12. [**Disaster Recovery**](disaster-recovery.html) — RPO / RTO targets, HA topology, backup schedule, restore procedures.
13. [**Upgrades**](upgrades.html) — rolling gateway upgrades, pool image upgrades, rollback, MCP version deprecation.
14. [**Troubleshooting**](troubleshooting.html) — common issues, circuit breakers, orphan reconciliation, emergency procedures.
15. [**`lenny-ctl` Reference**](lenny-ctl.html) — the full CLI. Bookmark this; you'll live in it.

Output of Day 2: you know where every health signal, runbook, and remediation command lives — before you need them.

### Inheriting a deployment someone else built?

Jump to **Agent Operability** and run `lenny-ctl doctor` first, then **Observability** to learn the health signals, then **Configuration** and **Scaling** to understand the tuning surface.

---

## What operating Lenny actually involves

A quick map of the work, grouped by when you'll do it.

### Day 0 -- standing up a deployment

- Provision the dependencies Lenny needs: Postgres, Redis, and object storage (MinIO or a cloud equivalent).
- Install Lenny. Either run `lenny-ctl install` (a guided wizard), or write an answer file and use `helm install` directly. The install flow handles installing custom resources, running preflight checks, and seeding initial data.
- The reference runtime catalog is registered by default -- verify that your tenants have access to the ones they should.
- Run `lenny-ctl doctor` to confirm the install is healthy. Re-run with `--fix` to auto-remediate anything that isn't.

### Day 1 -- configuration and hardening

- Register any additional runtimes you want and configure their warm pools.
- Set up credential pools and pick how each credential is delivered to runtimes (through the gateway's built-in LLM router, direct, or via an external proxy like LiteLLM or Portkey).
- Configure delegation and content policies.
- Harden the cluster: network policies, admission policies, node taints.
- Configure authentication -- OIDC or OAuth 2.1 -- and verify the token-exchange endpoint is reachable.
- Wire up observability: Prometheus Operator custom resources, Grafana dashboards, the bundled alerting rules, and OpenSLO export if you use an SLO platform.
- Decide what to do with the web playground -- leave it on for developer tenants, gate it behind auth, or turn it off entirely.

### Day 2 -- steady state

- Watch the platform through the management plane's diagnostic endpoints, metrics, alerts, and dashboards.
- Run `lenny-ctl doctor --fix` on schedule or on alert. The diagnostic endpoints give you structured cause chains, not raw logs.
- Scale pools and gateway replicas based on demand; the management plane publishes capacity recommendations.
- Roll upgrades of the gateway and runtime images (`lenny-ctl upgrade --answers <file>` replays a captured answer file).
- Rotate credentials and KMS keys on your schedule.
- Manage the tenant lifecycle: creation, quota changes, deletion.
- Respond to incidents using circuit breakers, the [runbook catalog]({{ site.baseurl }}/runbooks/), and emergency procedures.
- Verify disaster recovery periodically through automated restore tests.
- Audit your security posture and compliance controls.

### Capacity planning

- Measure `maxSessionsPerReplica` for your workload using the documented ramp-test method.
- Size warm pools from the formula in the Scaling page.
- Size credential pools for your concurrent-session target.
- Watch the thresholds at which you should extract a subsystem (for example, moving the gateway's LLM routing to a dedicated deployment).
- Plan promotions between deployment sizes as your load grows.
