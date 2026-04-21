## FMR iter9 review — regressions from iter8 fix commit `df0e675`

**Scope:** failure-mode and runbook surfaces touched by iter8 fix: `spec/17_deployment-topology.md` §17.7 runbook stubs, three new runbook files under `docs/runbooks/`, `spec/16_observability.md` §16.7 `deployment.feature_flag_downgrade_acknowledged` audit event.

**Note on agent availability:** The dispatched FMR subagent hit a usage-rate-limit failure (`"You've hit your limit · resets 4am (America/Los_Angeles)"`); this review was performed inline by the parent agent against the same regressions-only scope.

### Inspection results — no failure-mode or runbook regressions detected

1. **§17.7 `Admission-plane feature-flag downgrade` runbook stub** — phantom step 4 (ex-`admin.freezeAdmission` reference) removed; remediation flows cleanly from steps 1–3 to the cross-reference block. Step 1 verification command (`kubectl get validatingwebhookconfigurations -l app.kubernetes.io/name=lenny` + alert-clear confirmation) correctly added. No dangling references. Pairs with `docs/runbooks/admission-plane-feature-flag-downgrade.md`.
2. **§17.7 stub Diagnosis step 4 audit-log pivot** — correctly references `GET /v1/admin/audit-events?event_type=deployment.feature_flag_downgrade_acknowledged&since=24h`; event type exists in §16.7 catalog (line 669) after the iter8 FMR-024 fix, so the pivot no longer points at a phantom event.
3. **`deployment.feature_flag_downgrade_acknowledged` audit event (§16.7)** — full payload defined, written under the platform tenant (correctly scoped because the override is deployment-level), Notice severity (symmetric with `platform.upgrade_paused`), retention `audit.gdprRetentionDays`, not sampled (security-salient regardless of volume). Retention-days reference correctly points at the authoritative `audit.gdprRetentionDays` value.
4. **Three new runbook files** — file sizes and frontmatter confirmed for all three: `admission-plane-feature-flag-downgrade.md` (~9.7 KB), `elicitation-content-tamper-detected.md` (~8.1 KB), `ephemeral-container-cred-guard-unavailable.md` (~9.0 KB). All three appear as targets of the index-map entries in `docs/runbooks/index.md`.
5. **Fail-closed vs fail-open posture statements** — each runbook correctly identifies whether the invariant protected is maintained while the alert-source surface is unavailable (cred-guard unavailable → credential-boundary invariant still protected fail-closed; elicitation-tamper invariant holds because the gateway drops the divergent forward; feature-flag downgrade alert narrative is posture-signalling only, not a blocking condition).
6. **§17.7 stub cross-references** — all three new runbook-stub cross-references point to existing §16.5 alerts, §16.7 audit events, and files under `docs/runbooks/`. No dangling references.

No failure-mode or runbook regressions detected in the iter8 fix envelope.
