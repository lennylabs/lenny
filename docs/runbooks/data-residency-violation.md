---
layout: default
title: "data-residency-violation"
parent: "Runbooks"
triggers:
  - alert: DataResidencyViolationAttempt
    severity: critical
  - alert: DataResidencyWebhookUnavailable
    severity: critical
components:
  - gateway
  - compliance
symptoms:
  - "cross-border transfer attempt detected"
  - "data residency validator webhook unreachable"
  - "tenant session routed outside permitted region"
tags:
  - compliance
  - data-residency
  - gdpr
  - regions
requires:
  - admin-api
  - cluster-access
related:
  - admission-webhook-outage
---

# data-residency-violation

A data-residency violation was detected or the validator webhook is unreachable. Violations are potential compliance incidents (GDPR, Schrems II, sectoral residency requirements).

## Trigger

- `DataResidencyViolationAttempt` — cross-border transfer attempt detected.
- `DataResidencyWebhookUnavailable` — validator webhook unreachable past the configured sustain window (admission fails closed).
- Audit events of type `residency.violation_attempt`.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Violation attempt details

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=residency.violation_attempt&since=1h
```

Each event records: tenant, session, source region, destination region, payload class (checkpoint, artifact, LLM prompt), and the policy that rejected it.

### Step 2 — Which policy?

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin residency-policies list
lenny-ctl admin residency-policies get <policy-name>
```

### Step 3 — Webhook health (if unavailable)

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n lenny-system -l app=lenny-residency-webhook
kubectl logs -l app=lenny-residency-webhook -n lenny-system --since=5m | tail -50
```

If the webhook itself is down, admission is fail-closed — no data can cross borders but normal traffic may also be blocked; see [admission-webhook-outage](admission-webhook-outage.html).

## Remediation

### Step 1 — Violation attempt

The violation was **prevented** by design (fail-closed). Your action:

1. Identify the client path that attempted the cross-border transfer.
2. Work with the client owner to route the traffic to a tenant configured for multi-region, or reject the request upstream.
3. Confirm no partial data crossed the border by querying the validator's decision log:
   <!-- access: api method=GET path=/v1/admin/audit-events -->
   ```
   GET /v1/admin/audit-events?event_type=residency.decision&tenant_id=<id>&since=1h
   ```

### Step 2 — Webhook outage

Follow [admission-webhook-outage](admission-webhook-outage.html). Do **not** apply an emergency `failurePolicy: Ignore` on the residency webhook — a fail-open residency webhook is a compliance incident.

### Step 3 — Policy correction

If the violation was a **false positive** (legitimate traffic blocked):

1. Review the policy definition.
2. Update via:
   <!-- access: lenny-ctl -->
   ```bash
   lenny-ctl admin residency-policies update <name> -f policy.yaml
   ```
3. Verify the policy propagates (the webhook watches for policy changes):
   <!-- access: api method=GET path=/v1/admin/metrics -->
   ```
   GET /v1/admin/metrics?q=lenny_residency_policy_version&window=5m
   ```

### Step 4 — Compliance record

Every residency event creates an audit receipt. Export the relevant window for the compliance record:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin audit-events export \
  --filter 'event_type~"residency.*"' --since <alert_fire_time> \
  --output residency-incident-<date>.json
```

## Escalation

Escalate to:

- **Compliance officer / DPO immediately** for any confirmed violation attempt — even prevented attempts may require tenant notification under contractual terms.
- **Tenant operator** if the client behavior suggests a misconfiguration on their side.
- **Security on-call** if the pattern suggests exfiltration intent (repeat attempts, unusual destinations).
