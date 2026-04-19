---
layout: default
title: "credential-pool-exhaustion"
parent: "Runbooks"
triggers:
  - alert: CredentialPoolExhausted
    severity: critical
  - alert: CredentialPoolLow
    severity: warning
components:
  - credentialPools
symptoms:
  - "session creation returns CREDENTIAL_POOL_EXHAUSTED"
  - "lenny_credential_pool_available reaches 0"
  - "elevated rate of provider 429 responses"
tags:
  - credentials
  - rate-limiting
  - capacity
  - providers
requires:
  - admin-api
related:
  - credential-revocation
  - token-service-outage
---

# credential-pool-exhaustion

A credential pool has no assignable credentials. New sessions that require a credential from this pool fail with `CREDENTIAL_POOL_EXHAUSTED`. Existing sessions with active leases are unaffected until lease expiry.

## Trigger

- `CredentialPoolExhausted` alert — no assignable credentials for the configured sustain window.
- `CredentialPoolLow` alert — pool availability below the configured warning threshold.
- Session creation returns `CREDENTIAL_POOL_EXHAUSTED`.
- `/v1/admin/health` returns `credentialPools: degraded` or `unhealthy`.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Pool state

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin credential-pools get <pool-name>
```

<!-- access: api method=GET path=/v1/admin/credential-pools/{name} -->
```
GET /v1/admin/credential-pools/<name>
```

Inspect:

- `availableCount` -- credentials with capacity for a new lease.
- `leasedCount` -- credentials currently assigned to a session.
- `coolingDownCount` -- credentials recovering from a provider 429.
- `disabledCount` -- credentials administratively disabled.

If `coolingDownCount` dominates, the provider is rate-limiting you. If `leasedCount` dominates, session concurrency exceeds your pool headroom.

### Step 2 — Which credentials are hot?

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_credential_provider_rate_limit_total&window=15m&groupBy=credential_id
```

The top few credential IDs show where the 429 pressure is concentrated. A single hot credential usually means session-to-credential affinity is misbehaving or you need more keys at the provider's higher tier.

### Step 3 — Provider-side state

Check the provider's dashboard (OpenAI, Anthropic, etc.) for:

- Current request rate vs. tier limit.
- Recent rate-limit events or quota resets.
- Any provider-side outage announcements.

### Step 4 — Demand signal

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_active_sessions&groupBy=credential_pool&window=15m
```

A rising trend points at structural undersizing; a sudden spike points at a burst that will self-correct if the pool is sized for peak.

## Remediation

### Step 1 — Short-term: extend the cooldown

If provider rate-limiting is the cause, widen the cooldown so credentials don't come back hot:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin credential-pools update <pool> \
  --cooldown-on-rate-limit <duration>
```

<!-- access: api method=PATCH path=/v1/admin/credential-pools/{name} -->
```
PATCH /v1/admin/credential-pools/<name>
{"cooldownOnRateLimit": "<duration>"}
```

### Step 2 — Add credentials

Add new credentials to the pool. You need a provisioned Kubernetes Secret containing the new provider key:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin credential-pools add-credential <pool> \
  --secret-ref lenny-system/<secret-name> \
  --max-concurrent-sessions 50
```

<!-- access: api method=POST path=/v1/admin/credential-pools/{name}/credentials -->
```
POST /v1/admin/credential-pools/<name>/credentials
{"secretRef": "lenny-system/<secret-name>", "maxConcurrentSessions": 50}
```

**Expected outcome:** `availableCount` > 0 shortly after the add completes. New sessions succeed.

### Step 3 — Structural fix: raise per-key concurrency

If the provider tier allows higher per-key concurrency, raise it:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin credential-pools update-credential <pool> <credential-id> \
  --max-concurrent-sessions 80
```

### Step 4 — Structural fix: reduce session pressure

Lower per-tenant concurrency via quotas:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin quotas set --tenant <id> \
  --concurrent-sessions 20
```

### Step 5 — If a credential is permanently rate-limited or revoked

Follow [credential-revocation](credential-revocation.html) to rotate it out of the pool cleanly.

### Step 6 — Verify recovery

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose credential-pool <pool>
```

- `availableCount > 0`.
- `coolingDownCount` stable or trending down.
- `lenny_credential_provider_rate_limit_total` rate flat.
- `CredentialPoolExhausted` alert resolves.

## Escalation

Escalate to:

- **Cloud provider / LLM provider support** if a specific credential is disabled, IP-blocked, or rate-limited at a tier that does not match your agreement.
- **Finance ops** if the incident caused material billing-accuracy drift -- some providers bill on attempted request volume, so heavy 429 traffic still counts.
- **Capacity planning owner** if the pool has exhausted 3+ times in 30 days; this indicates structural under-provisioning and a re-sizing conversation is due.
