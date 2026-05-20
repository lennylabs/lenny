---
layout: default
title: "minio-replication-lag"
parent: "Runbooks"
triggers:
  - alert: MinIOReplicationLag
    severity: warning
components:
  - platform
symptoms:
  - "MinIO cross-zone replication is lagging beyond the §12.5 SLO"
tags:
  - chaos
related: []
---

# minio-replication-lag

MinIO cross-zone replication is lagging beyond the §12.5 SLO.

## Trigger

The `MinIOReplicationLag` alert fires when the §16.5 condition documented in the alert rule holds for its `for:` window. See [Metrics Reference §Alert rules](../reference/metrics.html#alert-rules) for the exact PromQL.

## Diagnosis

1. Inspect the firing alert's labels for the affected component and tenant.
2. Correlate with the gateway and component logs for the same time window.
3. Check the §16.5 dashboards for upstream and downstream signals.

## Remediation

1. Apply the documented remediation for the named alert: see the chaos test mapped to this runbook in `tests/tier8_chaos/runbook-map.yaml` for the failure shape and the recovery path the platform exercises.
2. If the alert persists after the documented remediation, escalate per the Escalation section.

## Verification

The named alert returns to the firing-clear state and the affected component's health-API row reports `healthy` again.

## Escalation

Page the on-call platform engineer when the alert remains firing after one cycle of the documented remediation.
