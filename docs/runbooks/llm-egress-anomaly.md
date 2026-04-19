---
layout: default
title: "llm-egress-anomaly"
parent: "Runbooks"
triggers:
  - alert: LLMUpstreamEgressAnomaly
    severity: critical
components:
  - gateway
symptoms:
  - "lenny_gateway_llm_upstream_egress_anomaly_total > 0"
  - "unexpected upstream LLM endpoint in request logs"
  - "egress policy violation"
tags:
  - llm
  - egress
  - security
  - network-policy
requires:
  - admin-api
  - cluster-access
related:
  - network-policy-drift
  - credential-revocation
---

# llm-egress-anomaly

The gateway detected an LLM upstream request going to an endpoint that is not on the allowlist, or going to an allowed endpoint with unexpected headers / SNI. This is a security-sensitive alert: either a misconfiguration or a potential exfiltration signal.

## Trigger

- `LLMUpstreamEgressAnomaly` — `rate(lenny_gateway_llm_upstream_egress_anomaly_total[1m]) > 0`.
- Audit events of type `llm.egress_anomaly`.

## Diagnosis

### Step 1 — Inspect anomaly records

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=llm.egress_anomaly&since=1h
```

Each event includes: `upstream_host`, `upstream_port`, `sni`, `credential_id`, `session_id`, `tenant_id`, `anomaly_class` (e.g., `host_not_allowlisted`, `sni_mismatch`, `unexpected_header`).

### Step 2 — Which policy fired?

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin egress-policies get <policy-name>
```

Confirm the policy is current.

### Step 3 — Session context

<!-- access: api method=GET path=/v1/admin/sessions -->
```
GET /v1/admin/sessions/<session-id>
```

Is this a new session, a long-running delegation, or an admin session? Admin-initiated anomalies are usually intentional; tenant sessions almost never are.

### Step 4 — Source credential

<!-- access: api method=GET path=/v1/admin/credentials/{id} -->
```
GET /v1/admin/credentials/<credential-id>
```

If the credential's scope doesn't permit the attempted destination, treat it as potential compromise.

## Remediation

### Step 1 — Confirm the intent

Three shapes:

- **Misconfigured allowlist.** The destination is legitimate but not on the list. Update the policy.
- **Legitimate new provider.** Validate with security, add to the allowlist.
- **Suspected compromise.** Proceed to Step 2 immediately.

### Step 2 — Suspected compromise

If the destination is unknown, unexpected, or matches a known exfiltration pattern:

1. Pause the session:
   <!-- access: lenny-ctl -->
   ```bash
   lenny-ctl admin sessions force-terminate <session-id>
   ```
2. Rotate the credential per [credential-revocation](credential-revocation.html).
3. Preserve evidence — do NOT delete the audit event or clear the session record.
4. Page security on-call.

### Step 3 — Policy update

If the destination is legitimate:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin egress-policies update <policy-name> -f policy.yaml
```

Verify propagation:

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_egress_policy_version&window=5m
```

### Step 4 — NetworkPolicy

Confirm the Kubernetes NetworkPolicy allows the new destination:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get networkpolicy -n lenny-system | grep egress
kubectl describe networkpolicy lenny-llm-egress -n lenny-system
```

If a NetworkPolicy blocks the destination, update its CIDR list — see [network-policy-drift](network-policy-drift.html).

### Step 5 — Verify

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_llm_egress_anomalies_total[5m])&window=30m
```

- Anomaly rate returns to 0.
- Egress policy version reflects the update.

## Escalation

Escalate to:

- **Security on-call immediately** for any anomaly matching a compromise shape (unknown destination, credential scope mismatch, tenant-session violations).
- **Tenant operator** for misconfigurations originating from their automation.
- **Compliance officer** if the destination suggests cross-border data movement — cross-reference [data-residency-violation](data-residency-violation.html).
