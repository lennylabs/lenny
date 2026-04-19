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
> **Status: planned.** This alerting walkthrough is scheduled for the initial tutorial set. The bundled alerts and OpenSLO export are canonical in the spec sections below; until the walkthrough lands, use `kubectl get prometheusrule -n lenny-system` to inspect rules directly and `lenny-ctl slo export --format openslo` for OpenSLO output.

Lenny ships a single source of truth for its alerting rules (`pkg/alerting/rules`), compiled into both the gateway binary (for in-process fallback) and the Helm chart (as `PrometheusRule` CRDs or a plain `ConfigMap`). SLOs are published in the OpenSLO v1 format for import into your SLO tooling.

## What this walkthrough will cover

1. Pick your monitoring format: set `monitoring.format: prometheusrule` (default, requires Prometheus Operator) or `monitoring.format: configmap` in Helm values.
2. Run `helm upgrade --reuse-values --set monitoring.format=prometheusrule`; confirm the `PrometheusRule` CRD is created.
3. Import the bundled rules into your existing Prometheus/Alertmanager stack.
4. Tour the catalog: `StartupLatencyBurnRate`, `TTFTBurnRate`, `PoolExhaustionWarning`, `CredentialPoolDrain`, and the full catalog from Spec §16.5.
5. Export SLOs with `lenny-ctl slo export --format openslo > lenny-slos.yaml`.
6. Apply the OpenSLO manifests to your SLO tool of choice (Nobl9, OpenSLO spec-compatible tools).
7. Validate with `lenny-ctl slo validate --config lenny-slos.yaml`.

## Canonical reference

- Spec §16.5 — alerting rules and SLOs (the full catalog, burn-rate formulas)
- Spec §25.13 — bundled alerting rules (manifest output formats)

## Related tutorials

- [Install with the `lenny-ctl install` Wizard](installer-wizard) — sets `monitoring.format` at install time
- [Diagnose and Remediate with `doctor --fix`](doctor-fix) — reactive companion to proactive alerts
