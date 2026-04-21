## DOC iter9 review — regressions from iter8 fix commit `df0e675`

**Scope:** documentation-consistency surfaces touched by iter8 fix: `docs/operator-guide/configuration.md`, `docs/operator-guide/namespace-and-isolation.md`, `docs/operator-guide/observability.md`, `docs/reference/error-catalog.md`, `docs/reference/metrics.md`, `docs/runbooks/*`, `docs/runtime-author-guide/platform-tools.md`, `docs/api/admin.md`; cross-consistency between spec §9.2/§13.1/§15.1/§16.5/§16.7/§17.2/§17.7/§17.9 and the corresponding docs.

**Note on agent availability:** The dispatched DOC subagent hit a usage-rate-limit failure (`"You've hit your limit · resets 4am (America/Los_Angeles)"`); this review was performed inline by the parent agent against the same regressions-only scope.

### Inspection results — no documentation-consistency regressions detected

1. **`docs/operator-guide/configuration.md` downgrade enforcement narrative** — correctly documents the four-layer enforcement posture after the iter8 DOC-033 fix: (a) phase-stamp ConfigMap, (b) Helm render-time guard scoped to `helm install`/`helm upgrade` (with the GitOps note explaining `helm template` empty-lookup), (c) `lenny-preflight` Job as the sole fail-closed downgrade gate for GitOps deployments, (d) `AdmissionPlaneFeatureFlagDowngrade` runtime alert. `kube-state-metrics` `--metric-labels-allowlist=configmaps=[lenny.dev/flag-*]` precondition block (line 500) is correctly scoped to the alert's operator responsibility with a concrete verification command.
2. **`docs/operator-guide/namespace-and-isolation.md` item 8** — cred-guard webhook description enumerates all four conditions and correctly identifies the fourth as the fsGroup side-channel closure. No drift from spec §13.1.
3. **`docs/operator-guide/observability.md`** — aligned with §16.5 new alert catalog; no stale "three layers" narrative, no stale PromQL expressions.
4. **`docs/reference/error-catalog.md`** — `ELICITATION_CONTENT_TAMPERED` description uses `{message, schema}` vocabulary matching spec §15.1, §9.2. (Note: `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` row at line 123 carries the same condition-(iv) sub-code gap as the spec-side entry — captured as CRD-030, not a doc-only finding.)
5. **`docs/reference/metrics.md`** — all three new alert rows present, `lenny_elicitation_content_tamper_detected_total` entry updated to `{message, schema}` vocabulary, PromQL expressions consistent with spec §16.5.
6. **`docs/runbooks/index.md`** — three new alert-to-runbook map entries present with correct component assignments.
7. **`docs/runtime-author-guide/platform-tools.md`** — iter8 2-line edit inspected; aligned with §8.5 `lenny/request_elicitation` `{message, schema}` schema. No vocabulary drift.
8. **`docs/api/admin.md`** — circuit-breaker dryRun paragraphs enumerate explicit simulation-object fields (consistent with spec §15.1 dryRun rows after the API-030 iter8 fix); 404 `RESOURCE_NOT_FOUND` on /close (but not /open) consistent with spec §15.1 and §11.6.
9. **Runbook frontmatter consistency** — all three new runbook YAML frontmatter blocks use valid values (`severity`, `components`, `triggers`, `related`).

No documentation-consistency regressions detected in the iter8 fix envelope beyond the CRD-030 cross-surface gap already captured under the CRD perspective.
