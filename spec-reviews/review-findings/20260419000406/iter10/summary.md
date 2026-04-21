# Technical Design Review Findings — 2026-04-21 (Iteration 10)

**Document reviewed:** `spec/` (27 markdown files)
**Review framework:** `spec-reviews/review-guidelines.md`
**Iteration:** 10 of ∞ (converge-when-clean)
**Scope (per iter8+ regressions-only directive):** regressions introduced by the iter9 fix commit `82ddfb2` only.
**Total findings:** 0 across all 11 review perspectives.

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 0     |
| Low      | 0     |
| Info     | 0     |

---

## Scope of iter9 fix commit `82ddfb2`

Two table-row edits plus one summary-file status update:

1. `spec/15_external-api-surface.md` line 1091 — `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` row: `details.reason` sub-code enumeration extended from 7 to 9 codes (added `credential_volume_mounted`, `run_lenny_path_mounted`); retry guidance extended to require `volumeMounts` compliance; cross-reference to §13.1 four-condition rationale added.
2. `docs/reference/error-catalog.md` line 123 — identical update to the paired error-catalog entry.
3. `spec-reviews/review-findings/20260419000406/iter9/summary.md` — marked CRD-030 as Fixed; not a spec surface.

---

## Perspectives Reviewed (inline — subagents still subject to upstream rate limit)

**Execution note:** The parent agent performed the iter10 regressions-only review inline against the two touched surfaces, since the iter9 fix envelope is narrow enough to inspect in a single pass and the earlier dispatched subagents had hit the upstream usage rate limit (`"You've hit your limit · resets 4am (America/Los_Angeles)"`). When the subagent surface recovers, a subsequent regressions-only pass MAY re-dispatch per-perspective agents for redundancy, but the fix envelope is so small that the inline review is authoritative.

| Perspective           | Regressions? | Notes |
| --------------------- | ------------ | ----- |
| Kubernetes Integration | 0           | Neither touched surface intersects K8s integration; §13.1, §17.2 item 13, preflight baseline all unchanged. |
| Security Model        | 0            | Condition-(iv) narrative in §13.1 unchanged; the error-catalog extension strengthens (does not weaken) the credential-boundary posture by making rejection-reason discrimination operator-visible. |
| Session Lifecycle     | 0            | No session/elicitation surfaces touched. |
| Observability         | 0            | No §16 surfaces touched; alerts and audit events unchanged. |
| Compliance            | 0            | No residency/legal-hold surfaces touched. |
| API Design            | 0            | Error-row format preserved (4-column spec table, 4-column docs table); cross-references valid; retry guidance format consistent with other `PERMANENT` rows. |
| Credential Lifecycle  | 0            | CRD-030 from iter9 resolved; sub-code taxonomy now covers all four conditions and both branches of condition (iv); retry guidance is complete. |
| Content Delivery      | 0            | Docs/runbooks/index unchanged; runbook for `EphemeralContainerCredGuardUnavailable` describes four conditions abstractly, no sub-code enumeration to sync. |
| Failure Modes         | 0            | No runbook or failure-mode surface touched. |
| Documentation         | 0            | `docs/reference/error-catalog.md` row matches `spec/15_external-api-surface.md` row in vocabulary, structure, and sub-code enumeration; cross-reference to spec §13.1 added. |
| Policy & Controls     | 0            | No §11 surfaces touched. |

---

## Cross-surface consistency verification

The condition (iv) taxonomy is now consistent end-to-end across five surfaces:

| Surface                                                   | Representation                                                                                                 |
| --------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `spec/13_security-model.md` §13.1                         | Condition (iv) narrative: `volumeMounts` by name OR `mountPath` = `/run/lenny` or prefix                       |
| `spec/17_deployment-topology.md` §17.2 item 13            | Condition (iv) summary: same two branches; forward-refs §13.1 rationale                                        |
| `docs/operator-guide/namespace-and-isolation.md` item 8   | Condition (iv) prose: volumeMount by name OR `/run/lenny` prefix                                               |
| `spec/15_external-api-surface.md` §15.1 line 1091         | Sub-codes `credential_volume_mounted` (name branch) + `run_lenny_path_mounted` (path branch); retry guidance   |
| `docs/reference/error-catalog.md` line 123                | Identical sub-codes and retry guidance                                                                         |

No regressions detected.

---

## Convergence Assessment

**Convergence criterion (per controlling directive):** 0 Critical/High/Medium findings.

**iter10 result:** 0 findings across all severities.

**Decision:** The review-and-fix loop has converged. No further iterations are needed. The spec and paired documentation are internally consistent across the surfaces touched by the iter8 and iter9 fix commits.
