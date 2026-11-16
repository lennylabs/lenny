---
code: SECAREA
type: index
area: security-and-compliance
summary: >-
  Lists the capabilities that record evidence, oversee security, and meet data obligations.
---

# Security and compliance

The capabilities in this area record evidence, oversee security, and meet data obligations. The audit log records actions with integrity and exports them to a SIEM, oversight gives tenants a view of their security posture and of operator access, forensics preserves evidence from pods, and holds, erasure, and residency govern stored data.

## Reading paths

- To change what is audited, read [Audit log](audit-log.md), then [SIEM export](siem-export.md).
- To change what happens to data when a user is erased, read [Erasure](erasure.md), then [Pseudonymization and receipts](pseudonymization-and-receipts.md), then [Legal hold](legal-hold.md).
- To change incident handling, read [Quarantine and evidence](quarantine-and-evidence.md), then [Agent activity streams](agent-activity-streams.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Audit log](audit-log.md) | AUDIT | States what is audited, the integrity of the audit log, tenant validation at write, and audit queries. |
| [SIEM export](siem-export.md) | SIEM | States how audit records reach a SIEM, the OCSF mapping, per-tenant destinations, dead-lettering, and the erasure boundary. |
| [Security oversight](security-oversight.md) | SECOV | States the tenant security record and posture, operator access transparency, and the rights of the operability service. |
| [Quarantine and evidence](quarantine-and-evidence.md) | FORENSIC | States pod quarantine for evidence, the reconciler that ends it, the capture workflow, and security incident response. |
| [Agent activity streams](agent-activity-streams.md) | ACTSTR | States the agent activity streams recorded for forensics and their retention. |
| [Legal hold](legal-hold.md) | HOLD | States legal and platform-only holds, their effect on retention and checkpoint rotation, and the hold reconciler. |
| [Erasure](erasure.md) | ERASE | States user-level erasure: the erasure job, its order of deletion, pseudonymization, receipts, processing restriction, and erasure of backups and external sinks. |
| [Data residency](data-residency.md) | RESID | States data residency, its admission control, key and backup residency, and the multi-region consistency model. |
| [Pseudonymization and receipts](pseudonymization-and-receipts.md) | PSEUDO | States the pseudonymization of retained records at erasure, the erasure salt, billing erasure policy, and erasure receipts. |

## Components that carry out this area

- [lenny-ops](../../components/lenny-ops/index.md): The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS.
- [Postgres](../../components/external/postgres.md): Postgres and its pooler: topology, HA, connection pooling, write classification, failover behavior, encryption, and backups.
- [Object store](../../components/external/object-store.md): The object store: backends, HA, versioning, replication, lifecycle rules, encryption, and outage handling.
- [Git mirror](../../components/git-mirror.md): The git mirror (lenny-git-mirror): storage, fetch scheduling, holds, deployment, and metrics.

## Interfaces this area exposes

- [Audit records](../../interfaces/events/audit-records.md): The audit record schema and the audit event types.
- [Audit and drift](../../interfaces/ops-api/audit-and-drift.md): Audit query and configuration drift operations.
- [Compliance](../../interfaces/admin-api/compliance.md): Legal hold, erasure job, residency, and tenant deletion operations.
