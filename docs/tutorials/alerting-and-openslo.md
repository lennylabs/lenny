---
layout: default
title: "Bundled Alerting and OpenSLO Export"
parent: Tutorials
nav_order: 21
description: Wire Lenny's bundled alerting rules into Prometheus Operator, and export its SLOs as OpenSLO v1 manifests for your SLO tooling.
---

# Bundled Alerting and OpenSLO Export

**Persona:** Platform Operator | **Difficulty:** Intermediate

{: .highlight }
> **Status: partial.** The bundled alerting rules, the OpenSLO export, and `lenny-ctl slo export` ship today. The chart renders the OpenSLO documents under `monitoring.openslo.enabled`, and `lenny-ctl slo export --format openslo` prints the same documents offline. The `lenny-ctl slo validate` subcommand in step 7 is planned and returns `unknown command "validate"` for now; validate the manifests with your SLO tool instead.

Lenny ships a single source of truth for its alerting rules and SLOs (`pkg/alerting/rules`), compiled into the gateway binary (for in-process fallback) and the Helm chart (as `PrometheusRule` CRDs or a plain `ConfigMap`). The same SLO catalog renders the OpenSLO v1 documents the chart bundles under `monitoring.openslo.enabled`, so the OpenSLO export cannot drift from the bundled burn-rate alerts.

## What this walkthrough will cover

1. Pick your monitoring format: set `monitoring.format: prometheusrule` (default, requires Prometheus Operator) or `monitoring.format: configmap` in Helm values.
2. Run `helm upgrade --reuse-values --set monitoring.format=prometheusrule`; confirm the `PrometheusRule` CRD is created.
3. Import the bundled rules into your existing Prometheus/Alertmanager stack.
4. Tour the catalog: `StartupLatencyBurnRate`, `TTFTBurnRate`, `WarmPoolExhausted`, `WarmPoolLow`, `CredentialPoolExhausted`, `CredentialPoolLow`, and the full catalog from Spec §16.5.
5. Enable the OpenSLO ConfigMap with `helm upgrade --reuse-values --set monitoring.openslo.enabled=true`, or export the documents offline with `lenny-ctl slo export --format openslo > lenny-slos.yaml`.
6. Apply the OpenSLO manifests to your SLO tool of choice (Nobl9, OpenSLO spec-compatible tools).
7. Validate with your SLO tool. The `lenny-ctl slo validate --config lenny-slos.yaml` helper is planned.

## Canonical reference

- Spec §16.5 — alerting rules and SLOs (the full catalog, burn-rate formulas)
- Spec §25.13 — bundled alerting rules (manifest output formats)

## Related tutorials

- [Install with the `lenny-ctl install` Wizard](installer-wizard) — sets `monitoring.format` at install time
- [Diagnose and Remediate with `doctor --fix`](doctor-fix) — reactive companion to proactive alerts
