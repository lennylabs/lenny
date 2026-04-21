---
layout: default
title: "gateway-rate-limit-storm"
parent: "Runbooks"
triggers:
  - alert: GatewayRateLimitStorm
    severity: warning
components:
  - gateway
symptoms:
  - "sustained /v1/oauth/token rejections by one tenant"
  - "token.exchange_rate_limited audit events spike"
  - "lenny_oauth_token_rate_limited_sampled_total climbing"
tags:
  - oauth
  - rate-limit
  - abuse
  - brute-force
requires:
  - admin-api
  - cluster-access
related:
  - credential-revocation
  - audit-pipeline-degraded
---

# gateway-rate-limit-storm

A tenant is sustaining high rejection rates on `/v1/oauth/token`. Indicates either a brute-force burst, a runaway client retry loop, or coordinated automation pressure.

## Trigger

- `GatewayRateLimitStorm`: per-tenant `rate(lenny_oauth_token_rate_limited_sampled_total[1m])` sustained above the configured alert threshold.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

> The sampling counter increments only after the first `(tenant_id, sub)` rejection per sampling window has already been audited, so a sustained rise indicates a continuous rejection stream — not a transient spike.

## Diagnosis

### Step 1 — Identify the subject(s)

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=token.exchange_rate_limited&tenant_id=<id>&since=15m
```

The `actorId` (`sub`) field tells you which caller(s) are being rejected. A single dominant `sub` is almost always a buggy retry loop. Multiple `sub`s pointing at the same upstream IdP org is the coordinated-abuse shape.

### Step 2 — Per-tier breakdown

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=sum by (limit_tier) (rate(lenny_oauth_token_rate_limited_sampled_total{tenant_id="<id>"}[1m]))&window=15m
```

- `caller_per_second` dominance → tight single-caller automation loop.
- `caller_per_minute` dominance → burst followed by sustained traffic.
- `tenant_per_second` dominance → coordinated multi-caller pressure.

### Step 3 — Replica distribution

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=sum by (service_instance_id) (rate(lenny_oauth_token_rate_limited_sampled_total[1m]))&window=15m
```

Per-replica local sampling is expected (Spec §13.3 "Audit sampling for rate-limit rejections"). An N× multiplier across N replicas is normal and does **not** indicate duplicate audit events for the same rejection.

### Step 4 — Upstream IdP signals

Check the upstream IdP event log for the suspect `sub`(s) — sign-in anomalies, IP geolocation shifts, or unusual device fingerprints inform containment.

## Remediation

### Step 1 — Legitimate caller with buggy loop

If the dominant `sub` is a legitimate automation:

1. Contact the operator of that caller to fix the retry loop (exponential back-off, circuit breaker).
2. Lenny does not expose a per-subject rate-limit override; per-caller limits (10/s, 300/min) are platform-wide.
3. The only tenant-scoped tunable is `oauth.rateLimit.tenantPerSecond` in Helm values — use sparingly, as it affects all callers in the tenant.

### Step 2 — Hostile caller

1. Block the `sub` at the upstream IdP (revoke the OIDC session). This denies new Lenny token exchanges because `/v1/oauth/token` requires a valid bearer per Spec §13.3.
2. In-flight Lenny tokens for that `sub` remain valid until `exp`. For faster containment, enumerate recent sessions and force-terminate them:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=session.created&actorId=<sub>&since=<token_lifetime>
```

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin sessions force-terminate <session-id>
```

3. If credentials from a specific pool were materialized for that `sub`, rotate the pool via [credential-revocation](credential-revocation.html).

### Step 3 — Tenant-wide pressure

Temporarily lower `oauth.rateLimit.tenantPerSecond` via Helm upgrade to shed load while investigating. Review access patterns after the storm subsides and restore the prior value.

### Step 4 — Do not disable sampling

Sampling is the protective mechanism preventing audit-write saturation. Disabling it would cause an audit-pipeline incident on top of the rate-limit storm.

### Step 5 — Verify recovery

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=sum by (tenant_id) (rate(lenny_oauth_token_rate_limited_sampled_total[1m]))&window=15m
```

Rate returns to near-zero once the offending caller is blocked or fixed.

## Escalation

Escalate to:

- **Security on-call** if the pattern resembles credential stuffing or token-theft — IdP session audit required.
- **Tenant operator** if the storm is from their own infrastructure and they need guidance on client back-off behavior.
- **Capacity owner** if the per-tenant limit is repeatedly inadequate for legitimate traffic — indicates a tier or pricing-plan mismatch.

Cross-reference: Spec §13.3 (rate limiting on `/v1/oauth/token`, audit sampling), §24.11 (`lenny-ctl admin sessions`).
