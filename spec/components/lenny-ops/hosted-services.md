---
code: LOPSSVC
type: component
summary: >-
  The internal parts of the services lenny-ops hosts: storage tiers, implementation notes, and metrics for events, diagnostics, locks, escalations, audit queries, drift, backups, and upgrades.
status: placeholder
---

# Hosted services

This page is a placeholder. The sources under "Content today" hold its content until the page is written.

## Content today

Numbered sections:

- [§16.1 Metrics](../../16_observability.md#161-metrics), in part
- [§25.4 The `lenny-ops` Service › Caller Identity and Capability Discovery › Metrics](../../25_agent-operability.md#caller-identity-and-capability-discovery)
- [§25.4 The `lenny-ops` Service › Operations Inventory › Implementation](../../25_agent-operability.md#operations-inventory)
- [§25.4 The `lenny-ops` Service › Operations Inventory › Metrics](../../25_agent-operability.md#operations-inventory)
- [§25.4 The `lenny-ops` Service › Idempotency](../../25_agent-operability.md#idempotency), in part
- [§25.4 The `lenny-ops` Service › Idempotency › Behavior during Postgres outage](../../25_agent-operability.md#behavior-during-postgres-outage)
- [§25.4 The `lenny-ops` Service › Idempotency › Retention](../../25_agent-operability.md#retention)
- [§25.4 The `lenny-ops` Service › Remediation Coordination › Storage Tiers](../../25_agent-operability.md#storage-tiers)
- [§25.4 The `lenny-ops` Service › Remediation Coordination › Tier Transitions and Split-Brain Prevention](../../25_agent-operability.md#tier-transitions-and-split-brain-prevention), in part
- [§25.4 The `lenny-ops` Service › Remediation Coordination › Clock Source](../../25_agent-operability.md#clock-source)
- [§25.4 The `lenny-ops` Service › Remediation Coordination › Multi-Replica Behavior](../../25_agent-operability.md#multi-replica-behavior)
- [§25.4 The `lenny-ops` Service › Remediation Coordination › Metrics](../../25_agent-operability.md#remediation-coordination)
- [§25.4 The `lenny-ops` Service › Escalation › Storage Tiers (Create Path)](../../25_agent-operability.md#storage-tiers-create-path)
- [§25.4 The `lenny-ops` Service › Escalation › Reconciliation](../../25_agent-operability.md#escalation)
- [§25.4 The `lenny-ops` Service › Escalation › Storage Tiers (Query Path)](../../25_agent-operability.md#storage-tiers-query-path)
- [§25.5 Operational Event Stream › Storage](../../25_agent-operability.md#255-operational-event-stream), in part
- [§25.5 Operational Event Stream › Storage › Delivery Tracking Retention](../../25_agent-operability.md#delivery-tracking-retention)
- [§25.5 Operational Event Stream › Webhook Delivery › Subscription Cache and Invalidation](../../25_agent-operability.md#subscription-cache-and-invalidation)
- [§25.5 Operational Event Stream › Metrics](../../25_agent-operability.md#255-operational-event-stream)
- [§25.6 Diagnostic Endpoints › Data Sources](../../25_agent-operability.md#256-diagnostic-endpoints)
- [§25.6 Diagnostic Endpoints › Metrics](../../25_agent-operability.md#256-diagnostic-endpoints)
- [§25.8 Platform Lifecycle Management › Metrics](../../25_agent-operability.md#258-platform-lifecycle-management)
- [§25.9 Audit Log Query API › Implementation](../../25_agent-operability.md#259-audit-log-query-api)
- [§25.9 Audit Log Query API › Query Limits and Scatter-Gather](../../25_agent-operability.md#query-limits-and-scatter-gather), in part
- [§25.9 Audit Log Query API › Metrics](../../25_agent-operability.md#259-audit-log-query-api)
- [§25.10 Configuration Drift Detection › Drift Detection Logic › Running-State Caching](../../25_agent-operability.md#running-state-caching)
- [§25.10 Configuration Drift Detection › Metrics](../../25_agent-operability.md#2510-configuration-drift-detection)
- [§25.11 Backup and Restore API › Storage](../../25_agent-operability.md#2511-backup-and-restore-api), in part
- [§25.11 Backup and Restore API › Metrics](../../25_agent-operability.md#2511-backup-and-restore-api)
