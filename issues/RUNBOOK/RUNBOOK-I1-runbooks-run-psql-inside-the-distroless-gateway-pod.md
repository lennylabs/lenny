---
id: RUNBOOK-I1
status: open
kind: doc-gap
severity: medium
requirements: []
---

# Runbooks run psql inside the gateway pod, whose distroless image has no psql

## Specification

The runbooks are the operator procedures that §25.7 serves through `GET /v1/admin/runbooks/{name}/steps`; each step's command is meant to run as written.

## Evidence

- `Dockerfile`: the gateway's runtime stage is `FROM gcr.io/distroless/static:nonroot`, which carries no shell and no `psql`.
- `kubectl exec deploy/lenny-gateway -- psql ...` (or `exec -i`) appears in `docs/runbooks/audit-grant-drift.md`, `audit-pipeline-degraded.md`, `dual-store-unavailable.md`, `gateway-replica-failure.md`, `minio-failure.md`, `schema-migration-failure.md`, `postgres-failover.md`, `stuck-finalizer.md`, `token-service-outage.md`, and `token-store-unavailable.md`.
- Several pass `"$POSTGRES_DSN"` or `"$LENNY_POSTGRES_DSN"`, which the operator's local shell expands, not the pod.
- Found by the general review of steps 0177.S3 and 0177.S4 on 2026-11-26. The step's new runbook, `upload-token-key-rotation.md`, runs psql from a short-lived client pod with the DSN read from the `lenny-datastore-conn` Secret instead.

## Gap

Each of those steps fails when an operator runs it: `kubectl exec` cannot start `psql` in the gateway container.

## Suggested resolution

Rewrite each step to run the Postgres client from a short-lived client pod (`kubectl run --rm -it ... --image=postgres:<version> -- psql "$DSN"`) with the DSN read from the `lenny-datastore-conn` Secret's `postgres-dsn` key, as `upload-token-key-rotation.md` does, and add a docs-tier check that no runbook execs a binary into a distroless Lenny image.
