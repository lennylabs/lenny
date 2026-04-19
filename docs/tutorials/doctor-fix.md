---
layout: default
title: "Diagnose and Remediate with `doctor --fix`"
parent: Tutorials
nav_order: 20
description: Use lenny-ctl doctor to diagnose the gateway, pools, and credentials; then walk through the auto-remediation guardrails that `--fix` applies.
---

# Diagnose and Remediate with `doctor --fix`

**Persona:** Platform Operator | **Difficulty:** Intermediate

{: .highlight }
> **Status: planned.** This diagnostics walkthrough is scheduled for the initial tutorial set. The doctor command and its remediation guardrails are canonical in the spec sections below; until the walkthrough lands, consult the spec.

`lenny-ctl doctor` combines local preflight checks with server-side diagnostic endpoints to produce an actionable report of what is wrong with a running deployment. The `--fix` flag takes the safe subset of findings and applies remediation — within explicit guardrails.

## What this walkthrough will cover

1. Run `lenny-ctl doctor` with no flags; read the diagnostic report.
2. Understand each category: connectivity, pool health, credential pool health, runtime registry integrity, resource pressure.
3. Preview remediations with `lenny-ctl doctor --fix --dry-run`.
4. Walk through the auto-remediation guardrails: what `--fix` will and won't touch (e.g., it will scale up a drained pool but not delete user data; it will rotate a stale admin token but not revoke active user sessions).
5. Apply the fix with `lenny-ctl doctor --fix`; verify the remediation report.
6. Rerun `doctor` to confirm resolution.

## Canonical reference

- Spec §24.2 — `lenny-ctl doctor` (the diagnostic command)
- Spec §25.6 — diagnostic endpoints (the server-side API `doctor` calls)
- Spec §11 — policy and controls (remediation guardrails)

## Related tutorials

- [Install with the `lenny-ctl install` Wizard](installer-wizard) — installs the diagnostic endpoints
- [Bundled Alerting and OpenSLO Export](alerting-and-openslo) — pairs with doctor for continuous health
