# Iteration 2 Kubernetes Infrastructure & Controller Design Review

**Date:** 2026-04-19
**Perspective:** Kubernetes Infrastructure & Controller Design
**Scope:** CRD design, controller patterns, resource management, cluster topology, upstream dependencies
**Prior iterations:** K8S-001 through K8S-035; iter1 closed K8S-035 (`lenny-pool-config-validator` named, alert added).

---

## Summary

Two real findings; both are regressions introduced or exposed by the iter1 fix for K8S-035.

---

### K8S-036 `lenny-pool-config-validator` webhook has two conflicting responsibility definitions [High]

**Files:** `04_system-components.md` §4.6.3 (line 578), `10_gateway-internals.md` §10.1 (lines 110–118)

The iter1 fix named the webhook `lenny-pool-config-validator` in §4.6.3 and added a matching regression-touched reference in §10.1. In doing so, it conflated two materially different validation duties under one webhook name, and the two descriptions now disagree on what the webhook actually does.

- §4.6.3 scopes the webhook to **authorization-based denial**: "rejects manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` and `SandboxWarmPool.spec` fields **unless the request's `userInfo` maps to the PoolScalingController ServiceAccount**". Under this definition, any write originating from the PoolScalingController SA bypasses the webhook entirely.
- §10.1 lines 110–118 asserts the same webhook enforces `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds` and the BarrierAck floor rule on pool configuration. These are semantic validation rules that must apply to **every** SSA apply of `SandboxWarmPool.spec` — including the PSC's reconciliation writes — otherwise the PSC can happily write a configuration that guarantees a SIGKILL during drain, and no admission gate will catch it.

If the webhook applies the §4.6.3 `userInfo` bypass to the §10.1 rules, the §10.1 validation is effectively dead code for the only writer that produces those fields. If the webhook applies the §10.1 rules universally, the §4.6.3 "unless userInfo maps to PSC" claim is false. Either way, one of the two sections is normatively wrong.

**Recommendation:** Split this into two separately named and separately scoped webhooks (e.g., keep `lenny-pool-config-validator` for the §10.1 semantic/budget rules applying to *all* writes, and introduce `lenny-pool-config-writer-guard` for the §4.6.3 `userInfo`-based manual-edit denial). Alternately, clarify in both sections that this is one webhook whose rule set is the *union* — §10.1 semantic rules always apply; the §4.6.3 `userInfo` check applies additionally only to the authorization path — and update the §16.5 `PoolConfigValidatorUnavailable` alert body to reflect both consequences (manual edits denied *and* pool reconciliation writes denied) during webhook outage.

---

### K8S-037 `templates/admission-policies/` enumeration missed by the iter1 fix [Medium]

**Files:** `17_deployment-topology.md` §17.2 (line 40), `13_security-model.md` §13.2 (line 206)

The iter1 K8S-035 fix note explicitly identified a "deployment gap" — that the Helm `templates/admission-policies/` enumeration in §17.2 line 40 omitted the Postgres-authoritative-state webhook — but the fix did not update that enumeration. The §17.2 list still stops at five items: (1) PSS runc enforcement, (2) PSS gVisor/Kata relaxed enforcement, (3) `POD_SPEC_HOST_SHARING_FORBIDDEN`, (4) label-based namespace targeting, (5) `lenny-label-immutability`. The webhook inventory is additionally contradicted by §13.2 line 206, which names `lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, and "CRD validation webhooks" as living in the `admission-webhook` component — none of which (except label-immutability) appear in the §17.2 enumeration. `lenny-pool-config-validator`, `lenny-data-residency-validator`, `lenny-direct-mode-isolation`, and `lenny-sandboxclaim-guard` are all specified elsewhere as Helm-deployed webhooks (e.g., §4.6.1 says `lenny-sandboxclaim-guard` is "deployed as part of the Helm chart under `templates/admission-policies/`"), but the canonical §17.2 enumeration lists none of them.

This creates two concrete risks: (1) a Helm-chart author reading §17.2 as the source of truth will ship an incomplete chart that silently omits fail-closed webhooks whose absence would go undetected (no preflight check covers these webhooks specifically), and (2) the `admissionController.replicas: 2` + `podDisruptionBudget.minAvailable: 1` HA requirement in §17.2 line 42 is stated only for "RuntimeClass-aware admission policy webhooks", leaving it ambiguous whether the same HA is required for the five unenumerated webhooks (§4.6.1 states it for `lenny-sandboxclaim-guard`, §13.2 line 178 states it for `lenny-label-immutability`, but §4.6.3 does not state it for `lenny-pool-config-validator`).

**Recommendation:** Expand §17.2 line 40 to enumerate every webhook shipped under `templates/admission-policies/` (at minimum: `lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, `lenny-data-residency-validator`, `lenny-pool-config-validator`, plus CRD conversion webhooks if they live in the same template directory). Explicitly state — either in §17.2 or per-webhook — that the `replicas: 2` + `PDB minAvailable: 1` HA requirement applies to all of them. Add a `lenny-preflight` check that enumerates the deployed `ValidatingWebhookConfiguration` resources and verifies the expected set is present, so a missing webhook fails the install rather than silently shipping.

---

*End of Iteration 2. Numbering starts at K8S-036; all prior findings (K8S-001 through K8S-035) are carried as Fixed or Skipped.*
