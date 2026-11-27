---
id: SESS-I2
status: open
kind: build-gap
severity: low
requirements: []
---

# The chart renders no release allowance

## Specification

§7.1 "Terminal pipeline context:": the terminal pipeline budget is the largest `maxWorkspaceSealDurationSeconds` of any pool plus the **release allowance**, "a gateway setting the deployer configures (default 30 s)".

## Evidence

- `cmd/lenny-gateway/flags.go` registers `--stream-failure-report-timeout` with `LENNY_STREAM_FAILURE_REPORT_TIMEOUT` as the release allowance.
- `charts/lenny/values.yaml` and `charts/lenny/templates/gateway-deployment.yaml` render neither, and the chart's `claimOrphanTimeoutSeconds` check assumes the 30 s default (`charts/lenny/templates/gateway-deployment.yaml`).
- Found while building step 0176.S2 on 2026-11-27. The gap predates the step.

## Gap

A deployer who installs with the chart cannot change the release allowance without editing the rendered manifests, and a chart value would also have to feed the chart's `claimOrphanTimeoutSeconds` floor.

## Suggested resolution

Add a chart value for the release allowance, render it as `LENNY_STREAM_FAILURE_REPORT_TIMEOUT`, and compute the chart's `claimOrphanTimeoutSeconds` floor from it.
