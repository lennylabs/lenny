---
layout: default
title: "Runbooks"
nav_order: 5
has_children: true
description: The operational runbook catalog -- keyed to alerts in the Metrics Reference and the component fields returned by the health API. Agent-consumable via `GET /v1/admin/runbooks`.
---

# Operational Runbooks

The runbook catalog for Lenny. Every alert in the [Metrics Reference](../reference/metrics.html) and every component that can turn `degraded` or `unhealthy` in the health API maps to one of the runbooks listed below.

Each runbook follows the three-part **Trigger → Diagnosis → Remediation** structure and is parseable by `lenny-ops` at runtime (`GET /v1/admin/runbooks`, `GET /v1/admin/runbooks/{name}`, `GET /v1/admin/runbooks/{name}/steps`). They are version-controlled alongside the platform code so human operators and AI agents see the same content.

If you are responding to an alert right now, start with the alert-to-runbook mapping below.

{: .note }
> **Thresholds are deployer-configurable.** Runbooks describe *what* the alert watches and the qualitative direction of the symptom ("elevated", "approaching limit", "non-zero sustained"). The numeric thresholds, burn rates, and evaluation windows ship with defaults but are tuned per deployment — the canonical values for your cluster live in your rendered `PrometheusRule` objects and in [Metrics Reference § Alert rules](../reference/metrics.html#alert-rules). Do not hard-code thresholds from prose into automation; always read the alert expression.

---

## Alert → runbook map

Every alert listed in [Metrics Reference §Alert rules](../reference/metrics.html#alert-rules) has an entry here.

### Critical alerts

| Alert | Runbook | Component |
|:------|:--------|:----------|
| `WarmPoolExhausted` | [warm-pool-exhaustion](warm-pool-exhaustion.html) | `warmPools` |
| `PostgresReplicationLag` | [postgres-failover](postgres-failover.html) | `postgres` |
| `GatewayNoHealthyReplicas` | [gateway-replica-failure](gateway-replica-failure.html) | `gateway` |
| `SessionStoreUnavailable` | [postgres-failover](postgres-failover.html) | `postgres` |
| `CheckpointStorageUnavailable` | [minio-failure](minio-failure.html) | `objectStore` |
| `EtcdUnavailable` | [etcd-operations](etcd-operations.html) | `controlPlane` |
| `CredentialPoolExhausted` | [credential-pool-exhaustion](credential-pool-exhaustion.html) | `credentialPools` |
| `CredentialCompromised` | [credential-revocation](credential-revocation.html) | `credentialPools` |
| `TokenServiceUnavailable` | [token-service-outage](token-service-outage.html) | `tokenService` |
| `ControllerLeaderElectionFailed` | [controller-leader-election](controller-leader-election.html) | `controllers` |
| `DedicatedDNSUnavailable` | [dns-outage](dns-outage.html) | `cluster` |
| `CosignWebhookUnavailable` | [admission-webhook-outage](admission-webhook-outage.html) | `admission` |
| `AuditGrantDrift` | [audit-grant-drift](audit-grant-drift.html) | `audit` |
| `NetworkPolicyCIDRDrift` | [network-policy-drift](network-policy-drift.html) | `cluster` |
| `AdmissionWebhookUnavailable` | [admission-webhook-outage](admission-webhook-outage.html) | `admission` |
| `SandboxClaimGuardUnavailable` | [admission-webhook-outage](admission-webhook-outage.html) | `admission` |
| `DualStoreUnavailable` | [dual-store-unavailable](dual-store-unavailable.html) | `postgres`, `redis` |
| `DataResidencyWebhookUnavailable` | [admission-webhook-outage](admission-webhook-outage.html) | `admission` |
| `DataResidencyViolationAttempt` | [data-residency-violation](data-residency-violation.html) | `gateway` |
| `PgBouncerAllReplicasDown` | [pgbouncer-saturation](pgbouncer-saturation.html) | `postgres` |
| `SessionEvictionTotalLoss` | [session-eviction-loss](session-eviction-loss.html) | `postgres`, `objectStore` |
| `DelegationBudgetKeysExpired` | [delegation-budget-recovery](delegation-budget-recovery.html) | `redis` |
| `BillingStreamEntryAgeHigh` | [billing-stream-backlog](billing-stream-backlog.html) | `billing` |
| `TokenStoreUnavailable` | [token-store-unavailable](token-store-unavailable.html) | `tokenService` |
| `LLMUpstreamEgressAnomaly` | [llm-egress-anomaly](llm-egress-anomaly.html) | `gateway` |

### Warning alerts

| Alert | Runbook | Component |
|:------|:--------|:----------|
| `WarmPoolLow` | [warm-pool-exhaustion](warm-pool-exhaustion.html) | `warmPools` |
| `RedisMemoryHigh` | [redis-failure](redis-failure.html) | `redis` |
| `CredentialPoolLow` | [credential-pool-exhaustion](credential-pool-exhaustion.html) | `credentialPools` |
| `GatewayActiveStreamsHigh` | [gateway-capacity](gateway-capacity.html) | `gateway` |
| `GatewaySessionBudgetNearExhaustion` | [gateway-capacity](gateway-capacity.html) | `gateway` |
| `Tier3GCPressureHigh` | [gateway-subsystem-extraction](gateway-subsystem-extraction.html) | `gateway` |
| `CheckpointStale` | [checkpoint-stale](checkpoint-stale.html) | `warmPools` |
| `CheckpointDurationHigh` | [checkpoint-stale](checkpoint-stale.html) | `warmPools` |
| `RateLimitDegraded` | [redis-failure](redis-failure.html) | `redis` |
| `CertExpiryImminent` | [cert-manager-outage](cert-manager-outage.html) | `certManager` |
| `ElicitationBacklogHigh` | [elicitation-backlog](elicitation-backlog.html) | `gateway` |
| `DelegationBudgetNearExhaustion` | [delegation-budget-recovery](delegation-budget-recovery.html) | `redis` |
| `PodClaimQueueSaturated` | [warm-pool-exhaustion](warm-pool-exhaustion.html) | `warmPools` |
| `GatewaySubsystemCircuitOpen` | [circuit-breaker-open](circuit-breaker-open.html) | `gateway` |
| `LLMTranslationLatencyHigh` | [llm-translation-degraded](llm-translation-degraded.html) | `gateway` |
| `LLMTranslationSchemaDrift` | [llm-translation-degraded](llm-translation-degraded.html) | `gateway` |
| `PoolConfigDrift` | [pool-config-drift](pool-config-drift.html) | `warmPools` |
| `WarmPoolReplenishmentSlow` | [warm-pool-exhaustion](warm-pool-exhaustion.html) | `warmPools` |
| `WarmPoolReplenishmentFailing` | [warm-pool-exhaustion](warm-pool-exhaustion.html) | `warmPools` |
| `SDKConnectTimeout` | [sdk-connect-timeout](sdk-connect-timeout.html) | `warmPools` |
| `RuntimeUpgradeStuck` | [runtime-upgrade-stuck](runtime-upgrade-stuck.html) | `controllers` |
| `CircuitBreakerActive` | [circuit-breaker-open](circuit-breaker-open.html) | `gateway` |
| `WorkspaceSealStuck` | [workspace-seal-stuck](workspace-seal-stuck.html) | `gateway` |
| `CoordinatorHandoffSlow` | [coordinator-handoff-slow](coordinator-handoff-slow.html) | `gateway` |
| `StorageQuotaHigh` | [storage-quota-high](storage-quota-high.html) | `objectStore` |
| `ErasureJobFailed` | [erasure-job-failed](erasure-job-failed.html) | `compliance` |
| `TenantDeletionOverdue` | [tenant-deletion-overdue](tenant-deletion-overdue.html) | `compliance` |
| `BillingStreamBackpressure` | [billing-stream-backlog](billing-stream-backlog.html) | `billing` |
| `PoolBootstrapMode` | [pool-bootstrap-mode](pool-bootstrap-mode.html) | `warmPools` |
| `EventBusPublishDropped` | [audit-pipeline-degraded](audit-pipeline-degraded.html) | `audit` |
| `GatewayRateLimitStorm` | [gateway-rate-limit-storm](gateway-rate-limit-storm.html) | `gateway` |
| `GatewayClockDrift` | [gateway-clock-drift](gateway-clock-drift.html) | `gateway` |
| `FinalizerStuck` | [stuck-finalizer](stuck-finalizer.html) | `controllers` |

### SLO burn-rate alerts

| Alert | Runbook |
|:------|:--------|
| `SessionCreationSuccessRateBurnRate` | [slo-session-creation](slo-session-creation.html) |
| `SessionCreationLatencyBurnRate` | [slo-session-creation](slo-session-creation.html) |
| `SessionAvailabilityBurnRate` | [slo-session-availability](slo-session-availability.html) |
| `GatewayAvailabilityBurnRate` | [gateway-replica-failure](gateway-replica-failure.html) |
| `StartupLatencyBurnRate` | [slo-startup-latency](slo-startup-latency.html) |
| `StartupLatencyGVisorBurnRate` | [slo-startup-latency](slo-startup-latency.html) |
| `TTFTBurnRate` | [slo-ttft](slo-ttft.html) |
| `CheckpointDurationBurnRate` | [checkpoint-stale](checkpoint-stale.html) |

---

## Component → runbook map

When the health API (`GET /v1/admin/health`) returns `degraded` or `unhealthy` for a component, the runbooks below are the primary responses. Health API component names come from [Spec §25.6](https://github.com/lennylabs/lenny/blob/main/spec/25_agent-operability.md).

| Component | Primary runbooks |
|:----------|:-----------------|
| `warmPools` | [warm-pool-exhaustion](warm-pool-exhaustion.html), [sdk-connect-timeout](sdk-connect-timeout.html), [pool-config-drift](pool-config-drift.html), [pool-bootstrap-mode](pool-bootstrap-mode.html) |
| `postgres` | [postgres-failover](postgres-failover.html), [pgbouncer-saturation](pgbouncer-saturation.html) |
| `redis` | [redis-failure](redis-failure.html), [delegation-budget-recovery](delegation-budget-recovery.html) |
| `objectStore` | [minio-failure](minio-failure.html), [storage-quota-high](storage-quota-high.html) |
| `gateway` | [gateway-replica-failure](gateway-replica-failure.html), [gateway-capacity](gateway-capacity.html), [circuit-breaker-open](circuit-breaker-open.html), [gateway-rate-limit-storm](gateway-rate-limit-storm.html), [gateway-clock-drift](gateway-clock-drift.html) |
| `certManager` | [cert-manager-outage](cert-manager-outage.html) |
| `credentialPools` | [credential-pool-exhaustion](credential-pool-exhaustion.html), [credential-revocation](credential-revocation.html) |
| `tokenService` | [token-service-outage](token-service-outage.html), [token-store-unavailable](token-store-unavailable.html) |
| `controllers` | [stuck-finalizer](stuck-finalizer.html), [controller-leader-election](controller-leader-election.html), [runtime-upgrade-stuck](runtime-upgrade-stuck.html) |
| `admission` | [admission-webhook-outage](admission-webhook-outage.html) |
| `audit` | [audit-pipeline-degraded](audit-pipeline-degraded.html), [audit-grant-drift](audit-grant-drift.html) |
| `billing` | [billing-stream-backlog](billing-stream-backlog.html) |
| `compliance` | [erasure-job-failed](erasure-job-failed.html), [tenant-deletion-overdue](tenant-deletion-overdue.html), [data-residency-violation](data-residency-violation.html) |
| `controlPlane` | [etcd-operations](etcd-operations.html) |
| `cluster` | [dns-outage](dns-outage.html), [network-policy-drift](network-policy-drift.html) |

The health API's `issueRunbooks` lookup returns the same mapping; `lenny-ops` populates it from this catalog.

---

## Escalation tree

Runbooks describe what a platform operator can fix with the Lenny admin API, `lenny-ctl`, or `kubectl`. When a step says "escalate," follow this tree.

```
                Platform operator (you)
                         │
                         ▼
            Can you fix this with:
            - lenny-ctl / Admin API   ← preferred
            - kubectl / Helm
            - cloud console (managed services)?
                         │
                    ┌────┴────┐
                   Yes       No
                    │         │
                    ▼         ▼
             Apply remediation  Identify the layer:
             Verify recovery    │
                                ├─ Kubernetes layer ───────►  Cluster admin
                                │   (node pressure, quotas,
                                │   CNI, webhook TLS rot)
                                │
                                ├─ Managed service (Postgres,
                                │   Redis, object store, KMS) ► Cloud provider
                                │                                support
                                │
                                ├─ Data / compliance impact  ─► Security &
                                │   (credential compromise,      compliance
                                │   audit gap, PII exposure)     on-call
                                │
                                ├─ Billing accuracy impact   ─► Finance ops
                                │   (quota, lease, measured      (with billing
                                │    usage disagrees)            reconciliation
                                │                                runbook output)
                                │
                                └─ Unclear / multi-layer    ─► Lenny
                                                                platform
                                                                on-call
```

**Named roles** (adjust the names to your org):

| Role | When to page | What they own |
|:-----|:-------------|:--------------|
| Platform operator | First responder for every alert | Lenny admin surface, runbook execution, capacity actions |
| Cluster admin | Escalated from platform operator for Kubernetes-layer problems | Nodes, CNI, admission-webhook TLS, cert-manager, RuntimeClass, ResourceQuota |
| Cloud provider support | When a managed dependency is unreachable or mis-behaving | Postgres / Redis / object store / KMS that your platform operator cannot inspect directly |
| Security on-call | Credential compromise, audit-trail gaps, suspected data exfiltration, residency violations | Incident declaration, credential rotation timing, regulator notifications |
| Finance ops | Billing accuracy or dispute-impacting events | Billing reconciliation and correction approval |
| Lenny platform on-call | Multi-layer outages, suspected platform-code bugs | Deep dive into gateway/controller internals |

### What to include when escalating

Every escalation MUST include:

1. **Alert and firing time.** Exact alert name, first fire time, current state (firing / pending / resolved).
2. **Runbook followed and the step you're on.** "I am on Step 3b of `warm-pool-exhaustion`, scaling did not recover the pool within 2 minutes."
3. **Observed symptoms and metrics.** Output of the relevant `lenny-ctl diagnose` command. Relevant Prometheus graphs (screenshot or PromQL).
4. **What you have already tried.** Every remediation step attempted so far, even if it did nothing.
5. **Blast radius.** Which tenants, pools, or sessions are affected, and the estimated error rate or user impact.
6. **Correlation IDs.** Any `X-Lenny-Operation-ID` from attempted remediations and the tenant / session / pool IDs involved.

The agent-operability surface ([Agent Operability](../operator-guide/agent-operability.html)) generates a pre-filled escalation payload when you call `lenny-ctl escalations create` -- it gathers the items above from recent diagnostics, events, and your most recent remediation operations.

---

## Runbook authoring conventions

Every runbook follows the format described in [Spec §25.7](https://github.com/lennylabs/lenny/blob/main/spec/25_agent-operability.md#257-operational-runbooks). Summary:

- **Front matter.** YAML with `triggers[]`, `components[]`, `symptoms[]`, `tags[]`, `requires[]`, `related[]`.
- **Three sections.** `## Trigger`, `## Diagnosis`, `## Remediation`.
- **Access markers.** Before each command block, an HTML comment declaring the access path: `<!-- access: lenny-ctl -->`, `<!-- access: api method=GET path=... -->`, `<!-- access: kubectl requires=cluster-access -->`. The indexer parses these to expose the structured per-step form at `/v1/admin/runbooks/{name}/steps`.
- **Decisions in prose, not YAML.** Agents read the decision logic as natural language; we don't author decision trees in a domain-specific language.
- **Expected outcome.** Each remediation step says what success looks like so the agent can verify.
- **Escalation criteria.** An explicit "escalate if ..." list at the end.

New runbooks belong in `docs/runbooks/`, follow the naming convention `<symptom-or-trigger>.md` (kebab-case), and must be added to the alert-to-runbook map above.

---

## Index of runbooks

Alphabetical. See the catalog above to pick one by alert or component.

- [admission-webhook-outage](admission-webhook-outage.html)
- [audit-grant-drift](audit-grant-drift.html)
- [audit-pipeline-degraded](audit-pipeline-degraded.html)
- [billing-stream-backlog](billing-stream-backlog.html)
- [cert-manager-outage](cert-manager-outage.html)
- [checkpoint-stale](checkpoint-stale.html)
- [circuit-breaker-open](circuit-breaker-open.html)
- [controller-leader-election](controller-leader-election.html)
- [coordinator-handoff-slow](coordinator-handoff-slow.html)
- [credential-pool-exhaustion](credential-pool-exhaustion.html)
- [credential-revocation](credential-revocation.html)
- [crd-upgrade](crd-upgrade.html)
- [data-residency-violation](data-residency-violation.html)
- [delegation-budget-recovery](delegation-budget-recovery.html)
- [dns-outage](dns-outage.html)
- [dual-store-unavailable](dual-store-unavailable.html)
- [elicitation-backlog](elicitation-backlog.html)
- [erasure-job-failed](erasure-job-failed.html)
- [etcd-operations](etcd-operations.html)
- [gateway-capacity](gateway-capacity.html)
- [gateway-clock-drift](gateway-clock-drift.html)
- [gateway-rate-limit-storm](gateway-rate-limit-storm.html)
- [gateway-replica-failure](gateway-replica-failure.html)
- [gateway-subsystem-extraction](gateway-subsystem-extraction.html)
- [llm-egress-anomaly](llm-egress-anomaly.html)
- [llm-translation-degraded](llm-translation-degraded.html)
- [minio-failure](minio-failure.html)
- [network-policy-drift](network-policy-drift.html)
- [pgbouncer-saturation](pgbouncer-saturation.html)
- [pool-bootstrap-mode](pool-bootstrap-mode.html)
- [pool-config-drift](pool-config-drift.html)
- [postgres-failover](postgres-failover.html)
- [redis-failure](redis-failure.html)
- [runtime-upgrade-stuck](runtime-upgrade-stuck.html)
- [schema-migration-failure](schema-migration-failure.html)
- [sdk-connect-timeout](sdk-connect-timeout.html)
- [session-eviction-loss](session-eviction-loss.html)
- [slo-session-availability](slo-session-availability.html)
- [slo-session-creation](slo-session-creation.html)
- [slo-startup-latency](slo-startup-latency.html)
- [slo-ttft](slo-ttft.html)
- [storage-quota-high](storage-quota-high.html)
- [stuck-finalizer](stuck-finalizer.html)
- [tenant-deletion-overdue](tenant-deletion-overdue.html)
- [token-service-outage](token-service-outage.html)
- [token-store-unavailable](token-store-unavailable.html)
- [total-outage](total-outage.html)
- [warm-pool-exhaustion](warm-pool-exhaustion.html)
- [workspace-seal-stuck](workspace-seal-stuck.html)
