# Technical Design Review Findings — 2026-04-21 (Iteration 9)

**Document reviewed:** `spec/` (27 markdown files)
**Review framework:** `spec-reviews/review-guidelines.md`
**Iteration:** 9 of ∞ (converge-when-clean)
**Scope (per iter8+ regressions-only directive):** regressions introduced by the iter8 fix commit `df0e675` only. Long-lived carry-forwards and pre-existing issues outside the iter8 fix envelope are out of scope.
**Total findings:** 1 (1 Medium) across 11 review perspectives.

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 1     |
| Low      | 0     |
| Info     | 0     |

### Medium Findings

| #   | Perspective            | Finding                                                                                                    | Section                                         |
| --- | ---------------------- | ---------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| 1   | Credential Lifecycle   | CRD-030 `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` sub-reason enumeration and retry guidance do not reflect condition (iv) added to §13.1 | 15.1 + `docs/reference/error-catalog.md` L123 |

---

## Perspectives Reviewed

| Perspective           | File                         | Execution                       | Findings |
| --------------------- | ---------------------------- | ------------------------------- | -------- |
| Kubernetes Integration | `p1_kubernetes.md`          | Inline (subagent rate-limited)  | 0        |
| Security Model        | `p2_security.md`             | Inline (subagent rate-limited)  | 0        |
| Session Lifecycle     | `p11_session.md`             | Inline (subagent rate-limited)  | 0        |
| Observability         | `p12_observability.md`       | Inline (subagent rate-limited)  | 0        |
| Compliance            | `p13_compliance.md`          | Subagent                        | 0        |
| API Design            | `p14_api_design.md`          | Subagent                        | 0        |
| Credential Lifecycle  | `p17_credential.md`          | Inline (subagent rate-limited)  | **1 Medium (CRD-030)** |
| Content Delivery      | `p18_content.md`             | Inline (subagent rate-limited)  | 0        |
| Failure Modes         | `p20_failure_modes.md`       | Inline (subagent rate-limited)  | 0        |
| Documentation         | `p22_document.md`            | Inline (subagent rate-limited)  | 0        |
| Policy & Controls     | `p24_policy.md`              | Subagent                        | 0        |

**Note on agent execution:** 3 of the 11 dispatched review subagents completed successfully (CMP, API, POL). The remaining 8 subagents returned with the error `"You've hit your limit · resets 4am (America/Los_Angeles)"`; for those perspectives, the parent agent performed the regressions-only review inline against the same iter8 fix envelope (`df0e675`). Each inline review file documents the scope considered and the inspection results.

---

## Detailed Findings by Perspective

---

## 17. Credential Lifecycle

### CRD-030. `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` sub-reason enumeration and retry guidance do not reflect condition (iv) added to §13.1 [Medium] — Fixed

**Section:** 15.1 (error row `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`, `spec/15_external-api-surface.md` line 1091); `docs/reference/error-catalog.md` line 123.

The iter8 CRD-029 fix added a fourth rejection condition to the `lenny-ephemeral-container-cred-guard` ValidatingAdmissionWebhook in `spec/13_security-model.md` §13.1: condition (iv) rejects ephemeral containers whose `volumeMounts` reference the credential tmpfs volume by name, or whose `mountPath` equals `/run/lenny` or begins with `/run/lenny/`. `spec/17_deployment-topology.md` §17.2 item 13 was updated in the same commit to enumerate four conditions, and `docs/operator-guide/namespace-and-isolation.md` item 8 correctly describes four conditions.

However, the paired error-catalog entries on the REST surface were not updated:

- `spec/15_external-api-surface.md` line 1091 enumerates the `details.reason` sub-code as one of seven values covering only conditions (i)–(iii): `runAsUser_equals_adapter_uid`, `runAsUser_equals_agent_uid`, `cred_readers_gid_in_supplementalGroups`, `cred_readers_gid_in_runAsGroup`, `runAsUser_absent`, `runAsGroup_absent`, `supplementalGroups_absent`. There is no sub-code for the condition-(iv) volumeMount rejection path.
- The same row's retry guidance ("Not retryable as-is — the caller must submit an ephemeral container whose `securityContext` explicitly sets `runAsUser`/`runAsGroup`/`supplementalGroups` to values outside the adapter UID, agent UID, and `lenny-cred-readers` GID") does not instruct the caller to omit the credential-volume mount or the `/run/lenny` mountPath, so a well-intentioned operator following this guidance after a condition-(iv) rejection would repeatedly retry and receive the same rejection.
- `docs/reference/error-catalog.md` line 123 carries the identical seven-sub-code enumeration and incomplete retry guidance.

Because condition (iv) is the operative closure for the fsGroup side-channel (per §13.1's "Relationship among the four conditions" paragraph), it is the most likely rejection path for any sophisticated bypass attempt, yet it has no discriminable sub-code in the operator-facing error payload and no remediation hint in the catalog prose.

**Recommendation:** In both `spec/15_external-api-surface.md` line 1091 and `docs/reference/error-catalog.md` line 123: (a) extend the `details.reason` sub-code enumeration with at least one value covering condition (iv) — suggest `credential_volume_mounted` for the volume-name branch plus `run_lenny_path_mounted` for the mountPath-prefix branch, or a single `credential_volume_or_path_mounted` if a combined code is preferred; whichever is chosen, both branches of condition (iv) must be reachable from the sub-code or `details` payload; (b) extend the retry guidance to state that the caller must additionally ensure the ephemeral container's `volumeMounts` do not reference the pod-level credential tmpfs volume and contain no entry whose `mountPath` equals `/run/lenny` or begins with `/run/lenny/`. Cross-reference `spec/13_security-model.md` §13.1 condition (iv) in both surfaces so the four-condition taxonomy is consistent across the code-path rejection, the spec narrative, and the operator-facing error catalog.

**Status:** Fixed. Both `spec/15_external-api-surface.md` line 1091 and `docs/reference/error-catalog.md` line 123 updated: (a) `details.reason` sub-code enumeration extended with two new codes — `credential_volume_mounted` (volume-name branch of condition (iv)) and `run_lenny_path_mounted` (mountPath-prefix branch); sub-code taxonomy reorganised to group (i)–(iii) UID/GID-surface codes separately from (iv) volumeMounts-surface codes; (b) retry guidance extended to require `volumeMounts` free of the credential tmpfs volume name and of any entry at the `/run/lenny` prefix; (c) cross-reference to §13.1 four-condition rationale added to both rows.

---

## Cross-Cutting Themes

1. **Iter8 fix envelope quality — strong.** 13 fix operations in iter8 (covering 15 reported Medium regressions) produced exactly 1 regression in iter9 review (CRD-030). The earlier iter7→iter8 regression rate was 8 of 13 Mediums; iter8→iter9 is 1 of 13. The fix discipline has materially tightened.
2. **Completeness regressions persist — cross-surface propagation remains the last gap.** CRD-030 follows the same pattern as several prior completeness regressions: an authoritative spec section is updated (§13.1) without updating all downstream consumers (§15.1 and `docs/reference/error-catalog.md`). The §17.2 and `namespace-and-isolation.md` surfaces were successfully updated; the REST-error-catalog pair was not.
3. **Regressions-only scope working as intended.** The iter8+ directive (review each iteration only against the previous fix commit, not against the full spec) held throughout iter9. The inline reviews for the eight rate-limited perspectives stayed within the df0e675 envelope rather than re-surfacing long-lived low-severity items.
4. **Subagent rate-limit is an operational constraint, not a correctness gap.** 8 of 11 review subagents hit a usage rate limit; the parent agent compensated with inline review against the same regressions-only scope. For the next iteration, rate-limit recovery is expected after the 4 AM PT reset and subagent parallelism will resume.
