---
id: CHAN-I6
status: open
kind: build-gap
severity: medium
requirements: []
---

# The adapter does not send its service-account token on GatewayControl

## Specification

§28.3 (Registers), link register, `LNK-GWCONTROL`: the pod adapter dials the gateway service ClusterIP on TCP 50051. §10.3 (mTLS PKI), **Projected SA token:**, states that the gateway validates the token's signature, expiry, and audience claim on every pod-to-gateway request.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Service-account token credential on `GatewayControl`", status `UNWIRED`, anchor `#link-register`, deferral `R12`.
- Surface recorded on the row: "`gatewaycontrol/satoken.go:64`".
- The pod spec mounts the projected token when `--sa-token-audience` is set (`pkg/controller/sandbox/podspec/sa_token.go`), and the gateway enforces it (`RequireSATokenInterceptor` in `cmd/lenny-gateway/gwcontrol_server.go`). `WithSAToken` in `pkg/adapter/gatewaycontrol/satoken.go` has no caller (triage, 2026-11-17).

## Gap

The adapter does not attach the mounted token, so an enforcing gateway refuses its calls.

## Suggested resolution

The row deferred the work to step R12, "The adapter-to-gateway control direction, with hold state", of `gateway-runtime-comms-remediation.md`.

No member change plans it.
