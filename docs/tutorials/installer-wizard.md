---
layout: default
title: "Install with the `lenny-ctl install` Wizard"
parent: Tutorials
nav_order: 19
description: Run the interactive Lenny installer against EKS, GKE, AKS, or k3s; capture a reproducible answer file; and replay it in CI.
---

# Install with the `lenny-ctl install` Wizard

**Persona:** Platform Operator | **Difficulty:** Beginner

{: .highlight }
> **Status: planned.** This installer walkthrough is scheduled for the initial tutorial set. The installer behavior is canonical in the spec section below; until the walkthrough lands, follow the spec or [Deploy to Kubernetes](deploy-to-cluster) for the Helm-first path.

`lenny-ctl install` is the recommended first-time installation path. It detects your cluster type, asks a short question set, previews the rendered Helm values, runs preflight checks, invokes `helm install`, seeds the bootstrap config, and runs a smoke test against the `chat` reference runtime — all in one pass.

## What this walkthrough will cover

1. Run `lenny-ctl install` against an empty `kubectl` context (EKS, GKE, AKS, or k3s).
2. Step through the question set: namespace, admin token bootstrap, identity provider, storage class, ingress class, isolation profile (`standard` / `sandboxed` / `microvm`), monitoring format (`prometheusrule` / `configmap`).
3. Review the composite `values.yaml` preview.
4. Run preflight; inspect any warnings.
5. Apply the install (behind the scenes: `helm install` + bootstrap seed + smoke test).
6. Capture the answer file with `--save-answers answers.yaml` for CI/IaC replay.
7. Replay the exact install with `lenny-ctl install --non-interactive --answers answers.yaml`.

## Canonical reference

- Spec §24.20 — installation wizard (question set, detection, preview, preflight, smoke test)
- Spec §17.6 — packaging and installation (Helm chart, answer-file schema)

## Related tutorials

- [Deploy to Kubernetes](deploy-to-cluster) — Helm-first install from an answer file
- [Diagnose and Remediate with `doctor --fix`](doctor-fix) — post-install validation
- [Bundled Alerting and OpenSLO Export](alerting-and-openslo) — monitoring hookup
