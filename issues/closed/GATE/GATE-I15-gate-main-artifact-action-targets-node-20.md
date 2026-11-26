---
id: GATE-I15
status: closed
kind: build-gap
severity: low
requirements: []
change: 0172.S12
closed: fixed
closed-by: 0172.S12
---

# The gate-main workflow pins an artifact action that targets the deprecated Node.js 20

## Specification

None: the defect is in `.github/workflows/gate-main.yml`, the workflow of change 0172 decision 27. The issue is filed under `GATE`.

## Evidence

- `.github/workflows/gate-main.yml` pins `actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02  # v4.6.2`.
- The first run of the workflow, `45768100398` on 2026-11-26, carries the annotation: "Node.js 20 is deprecated. The following actions target Node.js 20 but are being forced to run on Node.js 24: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02."

## Gap

The action runs today because GitHub forces it onto Node.js 24. A runner image that drops the forced fallback would fail the upload step, and the gate's verdict and log would not be kept as artifacts.

## Suggested resolution

Pin a release of `actions/upload-artifact` that targets Node.js 24, by commit SHA as the workflow does now, and confirm the annotation is gone from the next run.
