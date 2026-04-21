---
layout: default
title: "elicitation-content-tamper-detected"
parent: "Runbooks"
triggers:
  - alert: ElicitationContentTamperDetected
    severity: critical
components:
  - gateway
symptoms:
  - "intermediate pod re-emitted an MCP elicitation/create frame with a divergent {message, schema} pair"
  - "ELICITATION_CONTENT_TAMPERED PERMANENT/409 error observed"
  - "prompt-injection or hostile-runtime signal on a recursive-delegation graph"
tags:
  - elicitation
  - content-integrity
  - gateway-origin-binding
  - prompt-injection
  - security
requires:
  - admin-api
related:
  - elicitation-backlog
  - credential-revocation
  - session-eviction-loss
---

# elicitation-content-tamper-detected

The gateway records the original `{message, schema}` pair of every elicitation at origination time, keyed by `elicitation_id` and `origin_pod`. Intermediate pods on a recursive-delegation graph MAY forward an elicitation upstream by re-emitting the native MCP `elicitation/create` frame, but the forwarded frame's `{message, schema}` MUST match the originally-recorded pair. Any divergence is rejected with `ELICITATION_CONTENT_TAMPERED` (`PERMANENT`/409) — the modified text never reaches the client — and `lenny_elicitation_content_tamper_detected_total` is incremented with `origin_pod` and `tampering_pod` labels.

This alert pages on **any** non-zero count because every increment is either an active prompt-injection attempt or a hostile-runtime signal.

See SPEC §9.2 "Elicitation content integrity (gateway-origin binding)" for the invariant; SPEC §15.1 `ELICITATION_CONTENT_TAMPERED` error row; SPEC §16.7 `elicitation.content_tamper_detected` audit event.

## Trigger

- `ElicitationContentTamperDetected` — `increase(lenny_elicitation_content_tamper_detected_total[5m]) > 0` (non-zero any time in the last 5 minutes). Fires with Critical severity.

## Diagnosis

### Step 1 — Identify the pods involved

Inspect the alert labels: `origin_pod` (the pod that originated the elicitation) and `tampering_pod` (the pod that forwarded a modified `{message, schema}`).

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_elicitation_content_tamper_detected_total&groupBy=origin_pod,tampering_pod&window=15m
```

### Step 2 — Correlate with the audit event

Each tamper-detection writes an `elicitation.content_tamper_detected` audit event carrying `elicitation_id`, `origin_pod`, `tampering_pod`, `divergent_fields` (subset of `{message, schema}`), `original_sha256`, and `attempted_sha256`:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=elicitation.content_tamper_detected&since=1h
```

The `divergent_fields` enumeration tells you which of `{message, schema}` was mutated — a `schema` divergence is typically more suspicious (attempts to widen accepted-input shape) than a `message` divergence (which may be a rephrasing or translation attempt).

### Step 3 — Walk the delegation graph

Identify the session(s) that include `tampering_pod` as an intermediate hop:

<!-- access: api method=GET path=/v1/admin/sessions -->
```
GET /v1/admin/sessions?pod_name=<tampering_pod>&since=1h
```

If the tampering pod is a runtime that an operator expected to be trustworthy, the incident scope widens — review the runtime's image digest and registry provenance before proceeding.

### Step 4 — Determine whether a sanctioned transform was misimplemented

A legitimate pattern for presenting transformed text (translation, rephrasing, audience-targeted summarization) is to emit a **new** `lenny/request_elicitation` with a fresh `elicitation_id` — not to mutate content on a forwarded frame. If the runtime author tried to transform in-place, the design intent was rejected correctly by the gateway; coordinate with the runtime author to fix the adapter.

If the mutation is not plausibly accidental (e.g., the `schema` was widened in ways that weaken input validation), treat this as a prompt-injection or hostile-runtime incident and proceed to the security-escalation path below.

## Remediation

### Step 1 — Suspend the affected session(s)

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin sessions force-terminate <session-id>
```

The tampered elicitation has already been dropped by the gateway, so the client never saw the modified text — but the session's runtime has demonstrated it will attempt content mutation and should not continue operating on live traffic.

### Step 2 — Quarantine the runtime (for hostile-runtime cases)

If the `tampering_pod` is an agent runtime image that appears on multiple sessions, pause further pool allocation of that runtime until the root cause is established:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools pause --runtime <runtime-name>
```

(If the platform does not yet expose a pause primitive for runtimes in your deployment, lower the corresponding pool's `maxWarm` and `minWarm` to `0` via Helm values and run `helm upgrade` as an equivalent drain.)

### Step 3 — Revoke delegation leases if the runtime acted as a delegator

If the tampering runtime holds active delegation leases (it originated sessions via `lenny/delegate_task`), revoke them so it cannot spawn further children while under investigation:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin delegation list --actor-pod <tampering_pod>
lenny-ctl admin delegation revoke <lease-id>
```

### Step 4 — Credential revocation (if the runtime had credential leases)

If the runtime container held credential leases (e.g., LLM provider keys), treat the tamper event as a credential-compromise trigger and follow the [credential-revocation](credential-revocation.html) runbook — even absent direct evidence of exfiltration, the demonstrated malicious-forward behavior lowers the trust posture of any credentials the pod observed.

### Step 5 — Verify the alert clears

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_elicitation_content_tamper_detected_total[5m])&window=15m
```

- The rate drops to zero for a sustained 10-minute window after the session is terminated and the runtime is quarantined.
- No further `elicitation.content_tamper_detected` audit events recorded.

### Step 6 — Post-incident review

1. Pull the full audit-event set for the incident window and reconstruct the delegation graph.
2. Correlate the `tampering_pod` image digest against registry provenance and recent runtime-adapter publish events.
3. If the runtime is a first-party Lenny adapter, file a platform bug on the content-mutation path; if third-party, suspend pool-registration for that runtime pending a patched release.
4. Call `POST /v1/admin/drift/snapshot/refresh` per the drift-snapshot-refresh hotfix tail-convention so the drift snapshot reflects any pool/runtime state changes made during Remediation.

## Escalation

Escalate to:

- **Security on-call** for every firing — content-integrity violations are treated as security incidents regardless of apparent intent; the initial classification (operator bug, runtime bug, hostile runtime) is for the security team to make, not the on-call platform operator.
- **Runtime author** if the tampering pod runs a third-party or in-house runtime — the adapter implementation must be fixed to either (a) forward without modification or (b) emit a fresh elicitation for transformed text.
- **Compliance officer / DPO** if the tenant has a regulated `complianceProfile` — modified-prompt exposure is a potential content-handling policy violation even if the gateway blocked the forward.

Cross-reference: [SPEC §9.2](https://github.com/lennylabs/lenny/blob/main/spec/09_mcp-integration.md) "Elicitation content integrity"; [SPEC §15.1](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#151-rest-api) `ELICITATION_CONTENT_TAMPERED`; [SPEC §16.5](https://github.com/lennylabs/lenny/blob/main/spec/16_observability.md#165-alerting-rules-and-slos) `ElicitationContentTamperDetected`; [Metrics Reference](../reference/metrics.html#alert-rules).
