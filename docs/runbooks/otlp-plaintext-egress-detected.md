---
layout: default
title: "otlp-plaintext-egress-detected"
parent: "Runbooks"
triggers:
  - alert: OTLPPlaintextEgressDetected
    severity: critical
components:
  - observability
  - gateway
symptoms:
  - "OTel exporter handshake reports result=plaintext"
  - "tenant/session metadata may be transiting without TLS"
tags:
  - otel
  - tls
  - confidentiality
  - observability
requires:
  - cluster-access
  - admin-api
related:
  - ops-admin-api-plaintext-detected
---

# otlp-plaintext-egress-detected

A gateway or pod OTel exporter is shipping trace payloads without TLS, transporting tenant and session metadata in cleartext. Any non-zero rate is an active confidentiality regression.

## Trigger

`OTLPPlaintextEgressDetected` — `rate(lenny_otlp_export_tls_handshake_total{result="plaintext"}[5m]) > 0` sustained for 60s.

## Diagnosis

### Step 1 — Identify the affected replica and target

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get pods -o wide \
  -l app.kubernetes.io/name=lenny
```

Cross-reference the alert's `service_instance_id` label with the running pods.

### Step 2 — Inspect the exporter configuration

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-otel -o yaml
```

Look at `otlpEndpoint`, `otlpTlsEnabled`, `otlpCaBundle`, and any `acknowledgeOtlpPlaintext` override. Confirm the endpoint scheme is `https://` and the collector certificate is reachable from the gateway namespace.

### Step 3 — Probe the collector

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system exec deploy/lenny-gateway -- \
  openssl s_client -connect <collector-host>:<port> -servername <collector-host> </dev/null
```

A failed handshake explains the plaintext fallback; a successful one points at a misconfigured exporter.

## Remediation

1. **If TLS is required and the exporter regressed:** restore `otlpTlsEnabled: true` and a valid `otlpCaBundle` via `helm upgrade`. Confirm the alert clears within the evaluation window.
2. **If the operator deliberately disabled TLS:** require explicit acknowledgement via `acknowledgeOtlpPlaintext: true` (recorded in audit) and document the decision in the incident record. Plaintext egress remains a high-severity policy violation.
3. Repeat the openssl probe to confirm the handshake succeeds end-to-end.

## Verification

The alert clears and `lenny_otlp_export_tls_handshake_total{result="plaintext"}` stops incrementing.

## Escalation

Page security when the plaintext stream cannot be quenched within one evaluation window. Treat any persistent plaintext OTLP egress as a confidentiality incident.

Cross-reference: [§13.2](../../spec/13_security-model.md#132-network-policy), [§16.4](../../spec/16_observability.md#164-logging).
