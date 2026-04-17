---
layout: default
title: OpenFeature (external experiments)
parent: "Operator Guide"
nav_order: 17
---

# OpenFeature Integration (External Experiment Targeting)

Lenny's built-in experiment primitives support two targeting modes:

- **`mode: percentage`** — deterministic HMAC-SHA256 bucketing. Fully built-in, no external dependency. Use for simple A/B traffic splits.
- **`mode: external`** — delegates variant assignment to a flag/experimentation service via the [OpenFeature](https://openfeature.dev/) Go SDK.

This page covers `mode: external` setup. Percentage-mode experiments require no external configuration and work out of the box.

---

## Integration paths

Lenny offers two integration paths for external targeting, sharing a single configuration surface:

1. **OFREP (Remote Evaluation Protocol) — recommended.** OpenFeature's [Remote Evaluation Protocol](https://openfeature.dev/specification/appendix-c/) is a vendor-neutral REST API. Any flag service exposing OFREP works without a provider-specific adapter. Flagd, GO Feature Flag, ConfigCat, and LaunchDarkly (via its Relay Proxy) all expose OFREP.
2. **Built-in OpenFeature SDK providers.** For services that don't yet expose OFREP, Lenny's gateway binary includes OpenFeature SDK providers for LaunchDarkly, Statsig, and Unleash.

---

## Configuration

### OFREP (recommended)

```yaml
experimentTargeting:
  provider: ofrep
  timeoutMs: 200                # session-creation hot path timeout
  ofrep:
    endpoint: https://flags.internal/ofrep
    headers:
      Authorization: "Bearer ${OFREP_TOKEN}"
```

### LaunchDarkly

```yaml
experimentTargeting:
  provider: launchdarkly
  timeoutMs: 200
  launchdarkly:
    sdkKey: "${LD_SDK_KEY}"
    baseUrl: https://app.launchdarkly.com   # optional, for private instances
```

### Statsig

```yaml
experimentTargeting:
  provider: statsig
  timeoutMs: 200
  statsig:
    serverSecret: "${STATSIG_SERVER_SECRET}"
```

### Unleash

```yaml
experimentTargeting:
  provider: unleash
  timeoutMs: 200
  unleash:
    apiUrl: https://unleash.internal/api
    apiToken: "${UNLEASH_API_TOKEN}"
```

---

## How evaluation works

For each `mode: external` experiment registered in Lenny, the gateway calls:

```go
client.ObjectValue(ctx, experimentId, defaultVariant, evaluationContext)
```

where `evaluationContext` carries:

- `user_id` — the session's authenticated user (or `"anon:<session_id>"` for anonymous sessions).
- `tenant_id` — the tenant.
- session metadata (`runtime`, labels).

The provider's returned `ObjectValue` is either a string (the variant ID directly) or an object `{"variant_id": "<id>", ...}`. Lenny matches the returned variant against the `ExperimentDefinition`'s variant list:

- A matching variant enrolls the session.
- `"control"` routes the session to the base runtime with no variant.
- An unknown variant emits `experiment.unknown_variant_from_provider` and routes to control.

Lenny-registered experiments not returned by the provider are treated as "session runs control for this experiment"; provider-returned experiments not registered in Lenny are logged as `experiment.unknown_external_id` and otherwise ignored — the platform only routes experiments it knows how to pool-size.

---

## Stickiness

When `sticky: user` is set on an experiment, Lenny caches the evaluation result per `user_id` across sessions. The OpenFeature client is not re-queried for subsequent sessions if a cached assignment exists. `sticky: session` caches per session; `sticky: none` evaluates on every session creation.

---

## Failure handling

If the OpenFeature call times out or returns an error:

- No external experiment assignment is made for the affected session.
- The session runs the base runtime with no experiment context.
- `mode: percentage` experiments on the same tenant are unaffected — they are evaluated by Lenny's built-in hash independently.
- Metrics: `lenny_experiment_targeting_error_total{provider, error_type}` increments; `experiment.targeting_failed` warning event emitted.

A per-tenant circuit breaker protects session creation from cascading slowdowns during provider outages:

- 5 consecutive failures in 10 seconds opens the circuit.
- While open, the gateway skips the OpenFeature call entirely (returns empty assignment immediately).
- After 30 seconds the circuit half-opens and sends one probe request.

The `ExperimentTargetingCircuitOpen` warning alert fires if the circuit stays open for more than 60 seconds.

---

## Troubleshooting

### All sessions run control despite active external experiments

Check `lenny_experiment_targeting_circuit_open` — a value of 1 means the circuit is open. Inspect `experiment.targeting_failed` events for the underlying error. Verify the OFREP endpoint or SDK key/secret is reachable and correct.

### Variant distribution doesn't match provider's expected split

Your provider's evaluation is authoritative — Lenny simply routes based on what the provider returns. Check the provider's analytics to confirm the split. If `sticky: user` is set, re-check users' cached assignments in the provider.

### Anonymous sessions always get control

Anonymous sessions receive `evaluationContext.user_id = "anon:<session_id>"`. Providers that don't support targeting on that key return the default, which Lenny maps to control. Adjust the provider's targeting rules to support anonymous IDs, or accept the control-only behavior.

---

## Related

- [Reference: Configuration](../reference/configuration.md) — full `experimentTargeting.*` values.
- [OpenFeature specification](https://openfeature.dev/specification/).
- [OFREP specification](https://openfeature.dev/specification/appendix-c/).
