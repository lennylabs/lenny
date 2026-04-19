# Iteration 1 Kubernetes Infrastructure & Controller Design Review

**Date:** 2026-04-19  
**Perspective:** Kubernetes Infrastructure & Controller Design  
**Scope:** CRD design, controller patterns, resource management, cluster topology, upstream dependencies  
**Prior iterations:** 8 iterations completed; K8S-001 through K8S-034 findings reviewed

---

## Summary

**One real finding.** This iteration confirms K8S-035 from prior reviews remains valid and unresolved.

---

### K8S-035 Postgres-Authoritative-State Validating Webhook Lacks Formal Definition and Alert [High]

**Files:** `04_system-components.md` §4.6.3 (line 578), `16_observability.md` alerts table (§16.5)

**Description:**

Section 4.6.3 describes a validating admission webhook that protects Postgres-authoritative CRD state:

> "A validating admission webhook rejects manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` and `SandboxWarmPool.spec` fields unless the request's `userInfo` maps to the PoolScalingController ServiceAccount... The webhook runs in `Fail` mode with a 5s timeout; if the webhook is unavailable, updates are denied (fail-closed) to protect Postgres-authoritative state."

This webhook is **never formally named** in the spec (no label like `lenny-pool-config-validator` or similar). More critically, **no corresponding unavailability alert is defined** in §16.5's alert inventory. All other fail-closed webhooks have dedicated alerts:

- `CosignWebhookUnavailable` (line 329)
- `AdmissionWebhookUnavailable` (line 332)
- `SandboxClaimGuardUnavailable` (line 333)
- `DataResidencyWebhookUnavailable` (line 335)

The Postgres-authoritative-state webhook operates at the same criticality level (fail-closed, `Fail` mode, 5s timeout, operator-facing manual CRD edits would be blocked if unavailable) but has no alert defined. This leaves operators without observability during an outage.

**Why this matters:**

1. **No alerting name in spec:** Operators following the spec would not know what to name or how to reference this webhook in their monitoring.
2. **Missing alert definition:** §16.5 is the authoritative alert catalog. An on-call operator would find no alert entry and assume this webhook either doesn't exist or doesn't require monitoring.
3. **Deployment gap:** The Helm chart templates under `templates/admission-policies/` are listed as including specific webhooks (label-immutability, direct-mode-isolation, sandbox-claim-guard, CRD validation) but the Postgres-authoritative-state webhook is not enumerated.

**Verification:**

- Searched §4.6.3 for explicit webhook name: not found. Only referred to as "A validating admission webhook for Postgres-authoritative state".
- Searched §16.5 alert table and §25 operability section for corresponding unavailability alert: not found.
- Compared to other fail-closed webhooks (CosignWebhook, AdmissionWebhook, SandboxClaimGuard) — all have formal names and alerts.

**Recommendation:**

Formally name this webhook (e.g., `lenny-pool-config-validator`) and add a corresponding alert entry to §16.5:

```
| `PoolConfigValidatorUnavailable` | The `lenny-pool-config-validator` ValidatingAdmissionWebhook (configured `failurePolicy: Fail`) has been unreachable for more than 30 seconds. With `failurePolicy: Fail`, all manual edits to `SandboxTemplate.spec` and `SandboxWarmPool.spec` are rejected — Postgres-authoritative state is protected, but operators cannot make emergency manual corrections to CRD fields during a PoolScalingController outage. See [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries). | Warning |
```

Additionally, update §4.6.3 to formally name the webhook at first mention: "A validating admission webhook (`lenny-pool-config-validator`) rejects..."

---

*End of Iteration 1. Prior K8S-001 through K8S-034 carry forward as marked Fixed or Skipped.*
