# Iter3 K8S Review

**Date:** 2026-04-19
**Perspective:** Kubernetes Infrastructure & Controller Design
**Scope:** Regressions from iter2 fixes (K8S-036 admission webhook conflict, K8S-037 admission-policies/ enumeration), plus missed K8s issues in iter1/iter2.

---

## Summary

Two real findings. Both are partial-fix regressions exposed by the iter2 K8S-037 fix (webhook enumeration expanded in §17.2) — the enumeration list grew but the alert catalog and the parallel §13.2 inventory did not follow in lock-step. The iter2 K8S-036 fix (union semantics of `lenny-pool-config-validator`) looks clean at §4.6.3 / §10.1 / §16.5. The iter2 gateway PDB + rolling-update strategy (§17.1 row, `maxUnavailable: 1, maxSurge: 25%`) is arithmetically sound across all tier replica ranges (Tier 1: 2/4 with PDB 2 is surge-compatible; Tier 3: 5/30 with PDB `ceil(replicas/2)` is never in violation at `maxUnavailable: 1`).

---

### K8S-038 Per-webhook unavailability alerts missing for five of the eight enumerated webhooks [High]

**Files:** `17_deployment-topology.md` §17.2 (line 55), `16_observability.md` §16.5 (lines 345–386)

The iter2 K8S-037 fix expanded the §17.2 canonical enumeration to eight `ValidatingWebhookConfiguration`/conversion webhook deployments (`lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, `lenny-data-residency-validator`, `lenny-pool-config-validator`, `lenny-t4-node-isolation`, `lenny-drain-readiness`, `lenny-crd-conversion`) and added the sentence: "an availability drop below this threshold triggers the per-webhook unavailability alerts enumerated in Section 16.5". The §16.5 catalog, however, only defines per-webhook unavailability alerts for three of the eight: `SandboxClaimGuardUnavailable`, `DataResidencyWebhookUnavailable`, `PoolConfigValidatorUnavailable`. The "common `AdmissionWebhookUnavailable`" referenced in the same §17.2 paragraph is scoped to "the RuntimeClass-aware admission policy webhook (OPA/Gatekeeper or Kyverno)" per its own description at §16.5 line 348 — it does NOT cover the Lenny-authored Validating webhooks.

The five webhooks with NO corresponding runtime unavailability alert are:

1. `lenny-label-immutability` — fail-closed on pod CREATE/UPDATE in agent namespaces; outage silently fails open for the NET-003 selector-bypass guard (see §13.2 line 174: "Any pod in an agent namespace that carries [`lenny.dev/managed: "true"`] gains gateway connectivity"). With `failurePolicy: Fail`, outage blocks ALL agent-namespace pod admission (including controller-generated pods) — halts warm pool replenishment entirely, identical operational severity to `SandboxClaimGuardUnavailable`.
2. `lenny-direct-mode-isolation` — fail-closed on multi-tenant deliveryMode+isolationProfile combinations; outage blocks all agent-namespace pod admission.
3. `lenny-t4-node-isolation` — fail-closed on T4 pod placement; outage blocks all T4 pod scheduling.
4. `lenny-drain-readiness` — fail-closed on pod eviction; outage blocks ALL voluntary pod evictions (node drains, HPA scale-down, cluster upgrades) — §17.7 stuck-finalizer runbook cannot complete while this webhook is down.
5. `lenny-crd-conversion` — CRD conversion webhook; outage causes `SandboxTemplate`/`SandboxWarmPool`/`Sandbox`/`SandboxClaim` GETs to return 500s during cross-version conversion, and blocks all controller reconciliation.

The preflight inventory check (§17.2 line 57, §17.9 line 476) catches a MISSING webhook at install time, but not a RUNNING-but-UNREACHABLE webhook at operation time. For the three alerted webhooks, Prometheus fires within 30s of unreachability; for the other five, operators must correlate with symptom alerts (`WarmPoolBootstrapping`, stuck `Terminating` pods, reconciliation lag) to diagnose the cause.

**Recommendation:** Add five `*Unavailable` alert entries to §16.5, one per unalerted webhook, mirroring the structure of `SandboxClaimGuardUnavailable`. At minimum: `LabelImmutabilityWebhookUnavailable` (Critical — blocks warm pool replenishment), `DirectModeIsolationWebhookUnavailable` (Critical — blocks multi-tenant pool admission), `T4NodeIsolationWebhookUnavailable` (Critical when any pool uses T4 runtime), `DrainReadinessWebhookUnavailable` (Warning — blocks evictions but not admission), `CrdConversionWebhookUnavailable` (Critical — blocks CRD reads on version-mismatched clients, stalls controllers). Update the §17.2 paragraph at line 55 to reference all eight alerts by name, and remove the ambiguous "and the common `AdmissionWebhookUnavailable`" wording since that alert does not cover the Lenny-authored webhooks.

---

### K8S-039 §13.2 NetworkPolicy admission-webhook component enumeration disagrees with §17.2 [Medium]

**Files:** `13_security-model.md` §13.2 (line 209), `17_deployment-topology.md` §17.2 (lines 40–55)

The §13.2 NetworkPolicy table row for `lenny.dev/component: admission-webhook` (line 209) enumerates only four webhooks: "`lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, and CRD validation webhooks". The iter2 K8S-037 fix aligned §17.2's canonical enumeration to eight items but did not propagate to §13.2. The four webhooks missing from §13.2's enumeration are `lenny-data-residency-validator`, `lenny-pool-config-validator`, `lenny-t4-node-isolation`, `lenny-drain-readiness`.

This matters for two reasons:

1. **Selector coverage ambiguity.** The §13.2 row is what drives the rendered NetworkPolicy for `lenny-system` admission webhook ingress/egress. If the §13.2 list is the chart author's source of truth (and §17.2 is treated as the HA/replica-count source of truth), the chart author may not apply the `lenny.dev/component: admission-webhook` label to the four missing webhook Deployments. Pods missing the selector label receive no NetworkPolicy ingress rule, and the `lenny-system` default-deny policy silently drops kube-apiserver callbacks — fail-closed webhook endpoints become unreachable even though the pods are running. The `lenny-preflight` selector-consistency audit (§13.2 line 199) flags selectors matching zero pods, but does not flag the opposite case: a running admission-webhook Deployment that is NOT labeled `lenny.dev/component: admission-webhook` would simply not be covered by the NetworkPolicy and would not produce a "matches zero pods" failure.

2. **`lenny-drain-readiness` egress dependency mismatch.** §13.2 line 209's egress allow-list explicitly calls out "Gateway internal HTTP port (TCP 8080) for the `lenny-drain-readiness` webhook to call `GET /internal/drain-readiness`". This confirms `lenny-drain-readiness` is expected to share the `admission-webhook` component label — but the prose enumeration at the start of the same cell does not list it among the four component members. A chart author reading the prose list may conclude `lenny-drain-readiness` is a separate component with its own NetworkPolicy, when in fact the egress rule exists only because it is a member of `admission-webhook`.

**Recommendation:** Update the §13.2 line 209 first-column enumeration to match the §17.2 canonical list: "`lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, `lenny-data-residency-validator`, `lenny-pool-config-validator`, `lenny-t4-node-isolation`, `lenny-drain-readiness`, and the `lenny-crd-conversion` conversion webhook". Additionally extend the `lenny-preflight` selector-consistency audit to enumerate each Deployment listed in §17.2 and verify it carries `lenny.dev/component: admission-webhook`, fail-closing on any enumerated webhook Deployment missing the component label — this catches the inverse of the "zero-match" case.

---

## Regressions from iter2 fixes — verified clean

- **K8S-036 (iter2):** §4.6.3 and §10.1 now agree that `lenny-pool-config-validator` applies the union of two rule sets. §4.6.3 lines 578–583 and §10.1 line 112 are internally consistent: semantic rules apply to ALL writers (including PSC SSA); the `userInfo` authorization-denial rule applies ADDITIONALLY only to the manual-edit path. §16.5 `PoolConfigValidatorUnavailable` (line 386) reflects both consequences. No regression.
- **K8S-037 (iter2) — partial:** §17.2 enumeration expanded from 5 to 12 items (8 webhook Deployments + 4 policy manifests) and preflight inventory check added (§17.9 line 476). The webhook enumeration itself is now consistent and self-auditing. However, §13.2 and §16.5 did not fully track the expansion — see K8S-038 and K8S-039 above.
- **FLR-002 (iter2):** §17.1 gateway row concrete PDB + rolling-update strategy is arithmetically consistent. At Tier 1 (min 2, max 4, PDB `minAvailable: 2`, `maxUnavailable: 1, maxSurge: 25%`), the surge of 1 brings capacity to 3 during rolling update — 1 old drain leaves 2 available, satisfying PDB. At Tier 3 (min 5, max 30, PDB `ceil(replicas/2)`, `maxUnavailable: 1`), rolling-update serialization caps the CheckpointBarrier fan-out to one replica's 400-pod quota at a time, matching the §17.8.2 MinIO burst budget. No math error.

---

*End of Iteration 3. Numbering starts at K8S-038; all prior findings (K8S-001 through K8S-037) are carried as Fixed, Partial, or Skipped per prior iterations. K8S-035/K8S-037 are now Partial — see K8S-038/K8S-039.*
