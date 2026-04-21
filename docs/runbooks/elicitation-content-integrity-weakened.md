---
layout: default
title: "elicitation-content-integrity-weakened"
parent: "Runbooks"
triggers:
  - alert: ElicitationContentIntegrityWeakened
    severity: warning
  - alert: ElicitationContentIntegrityPermissiveTamper
    severity: warning
components:
  - gateway
symptoms:
  - "one or more tenants running with effective elicitation-integrity mode below enforce"
  - "tamper signal observed on the detect-only stream — modified prompt was forwarded to client"
  - "platform floor lowered, or tenant stored mode lowered via admin API"
tags:
  - elicitation
  - content-integrity
  - enforcement-posture
  - security
requires:
  - admin-api
related:
  - elicitation-content-tamper-detected
  - credential-revocation
---

# elicitation-content-integrity-weakened

Elicitation content integrity enforcement is governed per tenant (`elicitationContentIntegrityEnforcement.mode` ∈ `{enforce, detect-only, off}`) and clamped from below by the deployment-scope platform floor (`security.elicitationContentIntegrity.floor`). The effective mode for a tenant is `max(platform_floor, tenant_stored_mode)` under the ordering `off < detect-only < enforce`. Under effective `enforce`, divergent `{message, schema}` forwards are rejected with `ELICITATION_CONTENT_TAMPERED`; under `detect-only`, divergences are observed, audited, and counted but the modified frame is forwarded to the client; under `off`, detection does not run.

This runbook covers two related warning alerts that fire when the enforcement posture is below `enforce`:

- **`ElicitationContentIntegrityWeakened`** is a **standing** alert that fires while any tenant has an effective mode below `enforce` (i.e., the deployment is running with a weakened posture, regardless of whether a tamper was observed). It signals an ongoing configuration choice, not an incident.
- **`ElicitationContentIntegrityPermissiveTamper`** fires on an observed divergence on the `enforcement_mode="detect-only"` stream — a real tamper signal whose modified frame was forwarded to the client because the tenant is not on `enforce`.

The companion Critical alert [`ElicitationContentTamperDetected`](elicitation-content-tamper-detected.html) handles the `enforcement_mode="enforce"` stream (blocked tampers).

See SPEC §9.2 "Elicitation content integrity" for the three-mode model and platform-floor semantics; SPEC §15.1 admin endpoints `PUT`/`GET /v1/admin/tenants/{id}/elicitation-content-integrity`; SPEC §16.5 alerts `ElicitationContentIntegrityWeakened` and `ElicitationContentIntegrityPermissiveTamper`; SPEC §16.7 audit events `tenant.elicitation_content_integrity_changed`, `platform.elicitation_content_integrity_floor_changed`, `tenant.elicitation_content_integrity_floor_clamp`; SPEC §17 platform-floor rendering on the `lenny-deployment-phase-stamp` ConfigMap.

## Trigger

- `ElicitationContentIntegrityWeakened` — standing warning while any tenant's effective mode is below `enforce`. Intended as a persistent configuration signal, not a time-bounded incident.
- `ElicitationContentIntegrityPermissiveTamper` — `increase(lenny_elicitation_content_tamper_detected_total{enforcement_mode="detect-only"}[5m]) > 0`. Fires on an actual observed divergence that was forwarded to the client.

Thresholds and evaluation windows are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Identify the affected tenants

For `ElicitationContentIntegrityWeakened`, enumerate tenants whose effective mode is below `enforce`:

<!-- access: api method=GET path=/v1/admin/tenants -->
```
GET /v1/admin/tenants?elicitationContentIntegrity.effectiveMode=in:off,detect-only
```

For each tenant returned, read the full record:

<!-- access: api method=GET path=/v1/admin/tenants/{id}/elicitation-content-integrity -->
```
GET /v1/admin/tenants/<tenant_id>/elicitation-content-integrity
```

The response carries `storedMode`, `effectiveMode`, `platformFloor`, `justification`, `changedAt`, and `changedBy`. `effectiveMode` below `storedMode` is impossible — the platform floor only clamps from below.

For `ElicitationContentIntegrityPermissiveTamper`, the alert labels include `origin_pod` and `tampering_pod`; derive the tenant from the pod labels and follow the same read.

### Step 2 — Correlate with audit events

Find the most recent change that reduced the posture:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=tenant.elicitation_content_integrity_changed&tenant_id=<tenant_id>&since=30d
GET /v1/admin/audit-events?event_type=platform.elicitation_content_integrity_floor_changed&since=30d
GET /v1/admin/audit-events?event_type=tenant.elicitation_content_integrity_floor_clamp&tenant_id=<tenant_id>&since=30d
```

The first surfaces intentional per-tenant downgrades (with `justification` and `changedBy`). The second surfaces deployment-scope floor changes (typically raised or lowered at `helm upgrade` time). The third records the per-tenant effective-mode shifts caused by a floor change — one event is emitted per tenant whose effective mode moved.

If the latest change is recent and expected (e.g., a compliance-approved `detect-only` window for a specific tenant), the alert is acknowledged configuration; confirm the `justification` matches the approval record. If the change is unexpected, escalate immediately on the operator identified in `changedBy`.

For `ElicitationContentIntegrityPermissiveTamper`, also pull the tamper-detection audit event:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=elicitation.content_tamper_detected&tenant_id=<tenant_id>&since=1h
```

The event payload carries `enforcement_mode` (`detect-only` for this alert) and `forward_outcome` (`forwarded`) alongside `original_sha256`, `attempted_sha256`, and `divergent_fields`. Treat the divergence itself exactly as for the Critical alert — the runtime demonstrated it will mutate forwarded elicitation content, which is a prompt-injection or hostile-runtime signal regardless of enforcement mode.

### Step 3 — Determine the remediation path

Two independent levers control the effective mode:

- **Per-tenant**: the tenant's `storedMode` — changed via `PUT /v1/admin/tenants/{id}/elicitation-content-integrity`. Under a strictly raised floor, the stored value below the floor is preserved but no longer effective; lowering the floor restores it.
- **Platform-wide**: the floor — changed by editing `.Values.security.elicitationContentIntegrity.floor` in the deployment's Helm values and running `helm upgrade`. The render rewrites the `security.elicitationContentIntegrity.floor` key on the `lenny-deployment-phase-stamp` ConfigMap (unlike feature-flag keys on the same ConfigMap, which are append-only).

Pick the lever that matches intent: raise the tenant's stored mode if the weakened posture was tenant-scoped; raise the platform floor if the deployment-wide baseline needs tightening.

## Remediation

### Step 1 — Raise the tenant's stored mode (tenant-scoped fix)

<!-- access: api method=PUT path=/v1/admin/tenants/{id}/elicitation-content-integrity -->
```
PUT /v1/admin/tenants/<tenant_id>/elicitation-content-integrity
If-Match: <etag>
Content-Type: application/json

{"mode": "enforce"}
```

The request records `changedAt` and `changedBy` (operator OIDC `sub`) and emits a `tenant.elicitation_content_integrity_changed` audit event. `justification` is not required when raising to `enforce`; it is required when `mode` is `detect-only` or `off` (omitting it returns `400 ELICITATION_INTEGRITY_JUSTIFICATION_REQUIRED`).

If the request returns `400 ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR`, the platform floor already forbids the requested mode — nothing to do at the tenant level; the tenant's effective mode is already at the floor. Move to Step 2 or acknowledge the alert.

### Step 2 — Raise the platform floor (deployment-wide fix)

Edit the deployment's Helm values file:

```yaml
security:
  elicitationContentIntegrity:
    floor: enforce        # tightens from off / detect-only
```

Run `helm upgrade`. The render rewrites the `security.elicitationContentIntegrity.floor` key on the `lenny-deployment-phase-stamp` ConfigMap and the controller emits one `platform.elicitation_content_integrity_floor_changed` audit event plus one `tenant.elicitation_content_integrity_floor_clamp` event per affected tenant.

Note: raising the floor does not overwrite any tenant's `storedMode`; it clamps from below, so operators can later lower the floor to restore the per-tenant stored preference if intent changes.

### Step 3 — Investigate the tamper signal (Permissive Tamper alert only)

For `ElicitationContentIntegrityPermissiveTamper`, the observed divergence is a real security signal even though the frame was forwarded. Follow the Critical runbook's [tamper-detected Diagnosis & Remediation](elicitation-content-tamper-detected.html) for the affected session — suspend the session, quarantine the runtime, revoke delegation leases, and (if credential-bearing) rotate credentials. The only material difference from the Critical case is that the modified prompt did reach the client, so the downstream impact assessment is wider.

### Step 4 — Verify the alert clears

<!-- access: api method=GET path=/v1/admin/tenants -->
```
GET /v1/admin/tenants?elicitationContentIntegrity.effectiveMode=in:off,detect-only
```

- For `ElicitationContentIntegrityWeakened`: the tenant set returned by the query is empty (every tenant's effective mode is `enforce`) and the standing alert resolves.
- For `ElicitationContentIntegrityPermissiveTamper`: `rate(lenny_elicitation_content_tamper_detected_total{enforcement_mode="detect-only"}[5m])` returns to zero for a sustained 10-minute window after the offending session is terminated and the runtime is quarantined.

## Escalation

Escalate to:

- **Security on-call** for any `ElicitationContentIntegrityPermissiveTamper` firing — treat the divergence as a content-integrity incident regardless of enforcement mode; the fact that the frame was forwarded widens downstream impact.
- **Compliance officer / DPO** for `ElicitationContentIntegrityWeakened` on tenants carrying a regulated `complianceProfile` — running a regulated tenant below `enforce` may violate the tenant's compliance posture even without an observed divergence.
- **Platform operator who changed the posture** (as named in the audit event's `changedBy`) if the weakened state is unintended — they are the fastest path to restoring the prior posture and can attest to the change's intent.

Cross-reference: [SPEC §9.2](https://github.com/lennylabs/lenny/blob/main/spec/09_mcp-integration.md) "Elicitation content integrity"; [SPEC §15.1](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#151-rest-api) admin API endpoints and error rows; [SPEC §16.5](https://github.com/lennylabs/lenny/blob/main/spec/16_observability.md#165-alerting-rules-and-slos) warning alert definitions; [SPEC §16.7](https://github.com/lennylabs/lenny/blob/main/spec/16_observability.md#167-audit-events) audit event catalog; [SPEC §17](https://github.com/lennylabs/lenny/blob/main/spec/17_deployment-topology.md) platform-floor rendering; [Metrics Reference](../reference/metrics.html#alert-rules); [Admin API — Elicitation content integrity enforcement](../api/admin.html).
