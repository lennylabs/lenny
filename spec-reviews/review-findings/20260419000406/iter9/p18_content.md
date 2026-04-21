## CNT iter9 review — regressions from iter8 fix commit `df0e675`

**Scope:** content-delivery and documentation-catalog surfaces touched by iter8 fix: three new runbook files under `docs/runbooks/`, `docs/runbooks/index.md` alert-to-runbook map, `docs/api/admin.md` circuit-breaker dryRun paragraphs, `docs/reference/metrics.md` three new alert rows.

**Note on agent availability:** The dispatched CNT subagent hit a usage-rate-limit failure (`"You've hit your limit · resets 4am (America/Los_Angeles)"`); this review was performed inline by the parent agent against the same regressions-only scope.

### Inspection results — no content-delivery regressions detected

1. **`docs/runbooks/index.md` map entries** — three new rows added linking `ElicitationContentTamperDetected` → `elicitation-content-tamper-detected.html`, `EphemeralContainerCredGuardUnavailable` → `ephemeral-container-cred-guard-unavailable.html`, `AdmissionPlaneFeatureFlagDowngrade` → `admission-plane-feature-flag-downgrade.html`. Components assignment (`gateway` for elicitation, `admission` for the two admission-plane alerts) matches the runbook frontmatter.
2. **`docs/runbooks/admission-plane-feature-flag-downgrade.md`** — file exists (9.7 KB) with correct frontmatter (alert trigger, severity warning, components admission). Referenced from §17.7 stub and §16.5 alert rule.
3. **`docs/runbooks/elicitation-content-tamper-detected.md`** — file exists (8.1 KB); critical severity matches §16.5 alert and §16.7 audit event severity.
4. **`docs/runbooks/ephemeral-container-cred-guard-unavailable.md`** — file exists (9.0 KB); warning severity matches §16.5 alert; line 34 correctly refers to "the four rejection conditions the webhook enforces" matching §13.1's four-condition taxonomy.
5. **`docs/api/admin.md` circuit-breaker dryRun paragraphs** — lines 60/66 describe the generic "mirrors a real success" rule with the circuit-breaker exception; /open and /close dryRun paragraphs each enumerate the explicit simulation-object fields. Consistent with §15.1 dryRun rows.
6. **`docs/reference/metrics.md`** — three new alert rows (lines 477, 526, 527) present and internally consistent with spec §16.5; PromQL expressions either mirror or explicitly forward-reference spec §16.5 as source of truth for the canonical expression body.

No content-delivery or documentation-catalog regressions detected in the iter8 fix envelope.
