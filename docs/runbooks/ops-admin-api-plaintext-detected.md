---
layout: default
title: "ops-admin-api-plaintext-detected"
parent: "Runbooks"
triggers:
  - alert: OpsAdminAPIPlaintextDetected
    severity: critical
components:
  - ops
  - admin-api
symptoms:
  - "lenny-ops handshake to the gateway admin API reports result=plaintext"
  - "platform-admin JWT in transit without TLS"
tags:
  - tls
  - confidentiality
  - admin-api
  - ops
requires:
  - cluster-access
  - admin-api
related:
  - otlp-plaintext-egress-detected
---

# ops-admin-api-plaintext-detected

`lenny-ops` is calling the gateway admin API over plaintext HTTP. The admin-API link transports a platform-admin-scoped JWT in every request and carries pool configs, connector settings, upgrade state, and audit-bearing event envelopes. Any non-zero rate is an active confidentiality regression.

## Trigger

`OpsAdminAPIPlaintextDetected` — `rate(lenny_ops_admin_api_tls_handshake_total{result="plaintext"}[5m]) > 0` sustained for 60s.

## Diagnosis

### Step 1 — Inspect the configured endpoint

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-ops -o jsonpath='{.data.LENNY_GATEWAY_ADMIN_URL}'
kubectl -n lenny-system get secret lenny-ops-tls -o yaml 2>/dev/null
```

Confirm the URL scheme is `https://`. A missing `lenny-ops-tls` Secret indicates the `ops.tls.internalEnabled` chart value was not applied.

### Step 2 — Probe the gateway admin endpoint

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system exec deploy/lenny-ops -- \
  curl -sS -o /dev/null -w '%{http_code} %{ssl_verify_result}\n' "$LENNY_GATEWAY_ADMIN_URL/v1/admin/ops/health"
```

A 0 ssl_verify_result with a 200 status confirms plaintext.

## Remediation

1. **If TLS is required and the exporter regressed:** re-enable `ops.tls.internalEnabled: true` via `helm upgrade`, restore the `lenny-ops-tls` Secret, and rotate the platform-admin JWT (the previously transported bearer is considered exposed).
2. **If the deployment is intentionally plaintext (dev mode):** require explicit `acknowledgePlaintextAdminAPI: true` and document the decision in the incident record. Plaintext admin-API egress is not acceptable in production.
3. Confirm the alert clears within one evaluation cycle and the gateway access log shows no further plaintext handshakes.

## Verification

The alert clears and `lenny_ops_admin_api_tls_handshake_total{result="plaintext"}` stops incrementing.

## Escalation

Page security on persistent plaintext admin-API traffic. Treat exposed platform-admin JWTs as an incident — rotate the issuer key and re-issue downstream tokens.

Cross-reference: [§10.3](../../spec/10_gateway-internals.md#103-mtls-pki), [§13.3](../../spec/13_security-model.md#133-credential-flow).
