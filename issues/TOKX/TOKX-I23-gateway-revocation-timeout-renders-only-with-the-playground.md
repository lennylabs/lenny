---
id: TOKX-I23
status: open
kind: build-gap
severity: low
requirements: []
---

# The chart passes the token revocation timeout to the gateway only when the web playground is enabled

## Specification

§11.4 (User Invalidation), step 5 of a full revoke: the walk of the user's tokens runs under the statement timeout `auth.tokenRevocationTimeoutMs`. §17.8 (Capacity Planning and Defaults), the row "Token revocation statement timeout (`auth.tokenRevocationTimeoutMs`)": 5000 ms, which bounds the cascade of a recursive-revocation request and the token walk of a full revoke.

## Evidence

- `charts/lenny/templates/gateway-deployment.yaml` renders `--token-revocation-timeout-ms={{ .Values.auth.tokenRevocationTimeoutMs }}` inside the `{{- if .Values.playground.enabled }}` block, beside the playground flags.
- `cmd/lenny-gateway/adminrouter.go` reads `--token-revocation-timeout-ms` as the `RevocationTimeout` of the full revoke on `POST /v1/admin/users/{userId}/invalidate`, which the gateway serves whether or not the playground is enabled.
- `charts/lenny/values.yaml` states that the chart renders `auth.tokenRevocationTimeoutMs` into `--token-revocation-timeout-ms` on `lenny-token-service` and `lenny-gateway`, and `docs/operator-guide/security.md` states that the value is passed to `lenny-gateway`.
- `charts/lenny/tests/gateway-deployment_test.yaml` asserts the flag on no render.

Found while building 0171.S3, which renders `auth.revocationReconnectSpacing` beside `--multi-tenant` in the same template.

## Gap

On an install with `playground.enabled: false`, the chart default, the gateway's full revoke runs under the flag's built-in 5000 ms whatever `auth.tokenRevocationTimeoutMs` says. An operator who raises the value for a tenant with wide token trees raises it for the Token Service's cascade only.

## Suggested resolution

Move the `--token-revocation-timeout-ms` line out of the playground block, so the gateway receives it on every install, and add a chart test that asserts it with the playground disabled and with a value the test sets.
