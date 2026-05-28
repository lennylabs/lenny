---
layout: default
title: "emergency-credential-revocation"
parent: "Runbooks"
triggers:
  - alert: CredentialCompromised
    severity: critical
components:
  - platform
symptoms:
  - "the §10.4 emergency credential revocation path was triggered"
  - "a revoked pool credential still has active leases after propagation"
tags:
  - chaos
  - credentials
related:
  - credential-revocation
---

# emergency-credential-revocation

Direct-mode residual-risk operator steps for emergency credential revocation per §4.9. This runbook covers post-revocation provider-side disablement, lease re-binding, and the audit-trail review checklist after the §10.4 emergency credential revocation path has been triggered.

## Trigger

The `CredentialCompromised` alert (defined in `pkg/alerting/rules/rules.go`) fires when a revoked pool credential still has active leases against it for more than 30s, indicating that propagation has not fully cleared. See [Metrics Reference §Alert rules](../reference/metrics.html#alert-rules) for the exact PromQL. The [credential-revocation](credential-revocation.html) runbook covers the full rotation procedure; this runbook covers direct-mode residual-risk steps that apply after the revocation has been issued.

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
