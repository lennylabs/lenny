---
id: ERASE-I6
status: open
kind: build-gap
severity: low
requirements: []
---

# The chart renders no pipeline wait margin

## Specification

§12.8 **Lifecycle fence and drain.**: user erasure and tenant deletion wait for the terminal pipeline budget plus the **pipeline wait margin**, "a gateway setting the deployer configures (default 30 s, and not negative)", and keeping the wait over the longest budget in effect during a rollout that changes the budget is the deployer's responsibility.

## Evidence

- Step 0176.S2 adds the gateway flag `--terminal-pipeline-wait-margin` with the environment variable `LENNY_TERMINAL_PIPELINE_WAIT_MARGIN` (`cmd/lenny-gateway/flags.go`).
- `charts/lenny/values.yaml` and `charts/lenny/templates/gateway-deployment.yaml` carry no value for it and render neither the flag nor the variable, and the chart has no generic extra-arguments or extra-environment value for the gateway.
- `docs/operator-guide/configuration.md`, "Erasure and tenant-deletion wait", tells the deployer to set the flag on the gateway container's arguments or environment.
- Found while building step 0176.S2 on 2026-11-27. SESS-I2 records the same gap for the release allowance.

## Gap

A deployer who installs with the chart cannot set the margin without editing the rendered manifests, so the rollout procedure the configuration page gives (raise the margin before raising the budget) is not available through the chart.

## Suggested resolution

Add a chart value, such as `gateway.terminalPipelineWaitMarginSeconds`, rendered as `LENNY_TERMINAL_PIPELINE_WAIT_MARGIN`, together with the release allowance of SESS-I2, and regenerate the embedded manifests.
