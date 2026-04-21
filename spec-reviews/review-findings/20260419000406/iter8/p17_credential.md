# Perspective 17: Credential Management & Secret Handling — Iter8 Regressions-Only Review

**Scope:** Regressions introduced by fix commit `bed7961` only. Pre-existing issues and prior carry-forwards are out of scope per iter8 regressions-only protocol (`feedback_iter8_regressions_only.md`).

**Fix envelope surveyed (CRD-relevant surfaces):**
- `spec/13_security-model.md` §13.1 — new `lenny-ephemeral-container-cred-guard` webhook narrative (three rejection conditions) and its interaction with the fsGroup-based credential-delivery model.
- `spec/13_security-model.md` §13.2 — admission-webhook NetworkPolicy row: count bump 8 → 9 and inclusion of `lenny-ephemeral-container-cred-guard` in enumerated Deployments.
- `spec/17_deployment-topology.md` §17.2 — new admission-policies inventory item 13 plus feature-gated chart baseline set growth (four → five baseline webhooks).
- `docs/operator-guide/namespace-and-isolation.md` — new admission-policies item 8 describing the ephemeral-container-cred-guard webhook.

**Calibration:** Severities anchored to the iter1–iter7 rubric per `feedback_severity_calibration_iter5.md`. Critical = deploy-blocker or security-contract break; High = gap with no runtime workaround; Medium = design gap with correctness impact; Low = forensic-trail / docs-consistency polish (out of scope for this regressions-only pass).

## Findings

### CRD-029. `lenny-ephemeral-container-cred-guard` rejection conditions do not close the credential-file read boundary they claim to close — fsGroup mechanism automatically grants `lenny-cred-readers` supplementary-group membership to any ephemeral container, bypassing conditions (i)/(ii)/(iii) [Medium]

**Sections:**
- `spec/13_security-model.md` §13.1 line 25 (Cross-UID file delivery — fsGroup-based) — introduced pre-iter7, unchanged by bed7961.
- `spec/13_security-model.md` §13.1 line 27 (`lenny-cred-readers` membership boundary — ephemeral-container rejection conditions) — **added by bed7961**.
- `spec/17_deployment-topology.md` §17.2 lines 54 (admission-policies inventory item 13) — **added by bed7961**.

**Regression (internal inconsistency within §13.1 introduced by bed7961).** The fix commit added a three-condition rejection narrative for the new `lenny-ephemeral-container-cred-guard` webhook, closing with:

> "Closes the ephemeral-container credential-boundary bypass noted in [Section 13.1](13_security-model.md#131-pod-security)." (§17.2 item 13)

and

> "The webhook rejects, fail-closed, any ephemeral-container request where (i) `container.securityContext.runAsUser` equals the target pod's adapter UID or agent UID, or (ii) `container.securityContext.supplementalGroups` or `container.securityContext.runAsGroup` includes the `lenny-cred-readers` GID, or (iii) any of `runAsUser`, `runAsGroup`, or `supplementalGroups` is absent on the ephemeral container's `securityContext` (absent values would otherwise inherit pod-level defaults, including the fsGroup that grants `/run/lenny/credentials.json` read access)." (§13.1 line 27)

These three conditions are insufficient to close the boundary, and the parenthetical rationale in (iii) conflates two distinct Kubernetes mechanisms:

1. **fsGroup is applied by the kubelet regardless of any container-level `securityContext` setting.** Per the Kubernetes `PodSecurityContext` contract (documented in §13.1 line 25 — "kubelet to set group ownership and group-readable/writable semantics on every file in the volume at mount time"), when `spec.securityContext.fsGroup: <lenny-cred-readers GID>` is set on the pod, the kubelet (a) changes the group owner of every file in pod-mounted volumes to `lenny-cred-readers` and (b) adds the fsGroup GID to the effective supplementary-group list of every container process in the pod — including ephemeral containers. This fsGroup addition is unconditional: it happens even when the ephemeral container sets its own `securityContext.supplementalGroups` to a non-empty list that excludes `lenny-cred-readers`, and it cannot be overridden or opted out of at the container level.

2. **The three rejection conditions only inspect the ephemeral container's own `securityContext` fields.** Condition (i) rejects a `runAsUser` that matches adapter/agent UID. Condition (ii) rejects explicit inclusion of `lenny-cred-readers` in `runAsGroup` or `supplementalGroups`. Condition (iii) rejects absence (to prevent inheritance of pod-level `supplementalGroups` defaults). None of these three conditions interact with the fsGroup mechanism described in the adjacent paragraph at §13.1 line 25.

**Concrete attack path that bypasses all three conditions.** An actor with `update` on `pods/ephemeralcontainers` (the same threat model the webhook is scoped to) constructs an `EphemeralContainer.securityContext` with:

```yaml
runAsUser: 99999         # non-privileged, not matching adapter/agent UID — passes (i)
runAsGroup: 99999        # not lenny-cred-readers GID — passes (ii)
supplementalGroups: [99999]   # not lenny-cred-readers GID, explicit non-empty list — passes (ii) and (iii)
```

All three fields are present (passes iii), no field matches the adapter/agent UID or `lenny-cred-readers` GID (passes i and ii). The webhook admits the request. After kubelet starts the ephemeral container:

- Process primary UID = 99999 (not `adapter`, not `agent`).
- Process primary GID = 99999 (not `lenny-cred-readers`).
- Process supplementary-groups list = `{99999}` ∪ `{<lenny-cred-readers GID>}` — the second element comes from fsGroup, automatically added by kubelet because the pod's `spec.securityContext.fsGroup` is `<lenny-cred-readers GID>` per §13.1 line 25.

The process can `read(2)` `/run/lenny/credentials.json` because the file is mode `0440`, group-owned by `lenny-cred-readers`, and the process is a member of `lenny-cred-readers` via the kubelet-managed fsGroup supplementary-group addition. **The credential-file read boundary is not closed.**

**Why this is a regression from bed7961, not a pre-existing gap.** The ephemeral-container attack vector was flagged at iter7 as SEC-017 and the iter7 fix commit (bed7961) introduced the webhook narrative plus the three rejection conditions as the remediation. The iter7 `summary.md` records SEC-017 as "Fixed" with these exact three conditions as the fix surface. The fix pass did not account for the fsGroup-based supplementary-group addition, so the fix as shipped does not deliver the security property SEC-017 requires. This is internal inconsistency between §13.1 line 25 (fsGroup paragraph, which specifies the mechanism by which ANY container process in the pod obtains `lenny-cred-readers` group membership through fsGroup inheritance) and §13.1 line 27 (the new paragraph, which specifies rejection conditions that do not touch the fsGroup path).

The regression is structural: the narrative-described rejection conditions cannot logically close the boundary as long as the pod-level fsGroup continues to grant every container process (ephemeral included) `lenny-cred-readers` supplementary-group membership. Absent a change either to the rejection conditions (e.g., reject any ephemeral container whose `volumeMounts` include the credential tmpfs volume) or to the pod-level fsGroup model (e.g., move the credential tmpfs to a separate volume mount gated by a per-slot GID), the iter7 SEC-017 fix is not effective.

**Severity rationale — Medium.** The iter1–iter7 rubric calls Medium when there is a design gap with correctness/security impact that has a runtime workaround. Workarounds available to operators:

- Restrict `update pods/ephemeralcontainers` RBAC away from every namespace where agent pods run (ServiceAccounts that could exploit the gap are typically SRE-tooling accounts that could be scoped down).
- Deploy a supplemental OPA/Kyverno ConstraintTemplate on `pods/ephemeralcontainers` that rejects any ephemeral-container spec whose `volumeMounts` references `/run/lenny` (independent of the webhook's UID/GID checks).
- Accept the residual risk because an attacker already holding `update pods/ephemeralcontainers` in an agent namespace has broad lateral-movement capability already.

Not Critical because the attack requires an authenticated actor with `update pods/ephemeralcontainers` RBAC in the target agent namespace — the default RBAC posture under the Helm chart is that no Lenny-managed ServiceAccount has this permission, so the exposure is limited to externally-configured ServiceAccounts (SRE tooling, deployer-supplied debug accounts). Not High because no Lenny-shipped ServiceAccount holds this permission by default and the narrative in §13.1 line 27 explicitly names "an SRE-tooling ServiceAccount whose RBAC was not scoped away from agent namespaces" as the pre-condition. Calibrates to the iter7 SEC-017 Medium anchor (same surface, same threat model).

**Recommendation.** Close the fsGroup bypass by adding a fourth rejection condition to the webhook, and update the narrative to name the fsGroup mechanism explicitly. One of:

- **Option A (preferred — most targeted):** Add rejection condition (iv) to both §13.1 line 27 and §17.2 item 13: "(iv) the ephemeral container's `volumeMounts` includes any mount path that resolves to the pod's credential tmpfs volume (the volume with `fsGroup: <lenny-cred-readers GID>`), unless the corresponding `VolumeMount.readOnly: false` would also require condition (i) or (ii) to pass — which they never will for this volume because the adapter and agent UIDs are the only legitimate readers." Phrasing alternative: "(iv) the ephemeral container declares a `volumeMount` against the credential tmpfs volume (by name or path — the chart-rendered volume name is `lenny-credentials` per §4.7), independent of its `securityContext`."

- **Option B (broader):** Add rejection condition (iv) that rejects ANY ephemeral container in an agent namespace unconditionally, documenting that Lenny-managed agent pods do not support `kubectl debug`-style post-hoc attach and that SRE troubleshooting must use out-of-band paths (pod logs, `lenny-ctl admin session diagnose`, gateway-mediated introspection).

- **Option C (defense-in-depth — complements A or B):** Separate the credential tmpfs from the pod-level fsGroup by mounting credentials into a dedicated per-UID volume (e.g., `emptyDir` with `securityContext.runAsUser` / `runAsGroup` delivered via init-container `chown` rather than `fsGroup`), narrowing the fsGroup surface to non-credential artifacts. This is a larger spec change and closes the bypass at the root mechanism rather than at the admission boundary.

Whichever option is chosen, the §13.1 parenthetical in condition (iii) ("absent values would otherwise inherit pod-level defaults, including the fsGroup that grants `/run/lenny/credentials.json` read access") should be rewritten to acknowledge that fsGroup is applied unconditionally by the kubelet and is not suppressed by setting explicit container-level `securityContext` fields — so that the narrative accurately reflects the mechanism the rejection closes.

## Convergence assessment

**New iter8 findings:** 1 (CRD-029 Medium). Regression introduced by bed7961's iter7 SEC-017 fix.

**Other surfaces touched by bed7961 (CRD-relevant) verified clean:**

| Check | Location | Result |
| --- | --- | --- |
| §13.1 rejection-condition text verbatim matches §17.2 item 13 | §13.1 line 27, §17.2 line 54 | Consistent (both enumerate the same (i)/(ii)/(iii) conditions and same `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` error code). |
| Admission-webhook NetworkPolicy row count bump 8 → 9 | §13.2 line 221 | Correct — row now enumerates nine Deployments including `lenny-ephemeral-container-cred-guard`. |
| Feature-gated chart baseline set growth 4 → 5 (addition to baseline) | §17.2 line 68, line 82 | Consistent — `lenny-ephemeral-container-cred-guard` is listed as a baseline (unconditional) webhook, and line 82 explicitly calls this out (`"The SEC-017 addition of lenny-ephemeral-container-cred-guard (item 13) is part of this baseline set"`). |
| `docs/operator-guide/namespace-and-isolation.md` item 8 | line 102 | Consistent with authoritative §13.1 / §17.2 narrative at the operator-guide abstraction level (terse summary of rejection conditions; no behavioral drift). |
| Webhook scope (admission target) | §13.1, §17.2, docs item 8 | Consistent — all three name `pods/ephemeralcontainers` subresource in every agent namespace. |
| Webhook unavailability alert naming | §13.1 cross-references §16.5, §17.2 line 56 lists `EphemeralContainerCredGuardUnavailable` | Consistent. |

**Status:** 1 Medium regression detected (CRD-029). The webhook narrative introduced by bed7961 makes a security claim (closure of ephemeral-container credential-read boundary) that the rejection conditions as described do not deliver on, because the pod-level fsGroup mechanism — detailed in the adjacent §13.1 paragraph — automatically grants `lenny-cred-readers` supplementary-group membership to any ephemeral container regardless of its own `securityContext` settings. No other regressions detected in the CRD-relevant surfaces touched by bed7961.
