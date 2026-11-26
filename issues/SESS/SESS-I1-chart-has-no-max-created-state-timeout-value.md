---
id: SESS-I1
status: open
kind: doc-gap
severity: low
requirements: []
---

# The configuration reference lists gateway.maxCreatedStateTimeoutSeconds, which the chart does not have

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**: the token expires at "`session_creation_time + maxCreatedStateTimeoutSeconds` (default 300 s)", and since step 0177.S2 a replaced upload-token key verifies for a time that grows with `maxCreatedStateTimeoutSeconds`.

## Evidence

- `docs/reference/configuration.md`, the gateway table: the row `gateway.maxCreatedStateTimeoutSeconds`, default 300.
- `charts/lenny/values.yaml` and `charts/lenny/templates/gateway-deployment.yaml` carry no `maxCreatedStateTimeoutSeconds` value and render no `--max-created-state-timeout-seconds` flag or `LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS` variable.
- `pkg/gateway/core/session/watchdog/flags.go` registers the flag with the environment override.
- Found while documenting step 0177.S2 on 2026-11-26. The gap predates the step.

## Gap

An operator who sets the documented chart value changes nothing. The token lifetime, the `created`-state budget, and the upload-token key verification time are settable only through the gateway container's arguments or environment.

## Suggested resolution

Add `gateway.maxCreatedStateTimeoutSeconds` to the chart and render it as `LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS`, or correct the reference row to name the gateway flag and its environment variable.
