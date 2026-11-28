---
id: LOPSKRES-I1
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0183.S4
---

# The lenny-ops Ingress routes the internal invalidate RPC and the metrics endpoint from outside the cluster

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Kubernetes Resources › Services**: the `lenny-ops` ClusterIP Service carries internal traffic and is the Ingress's target. [§25.5](../../spec/25_agent-operability.md#255-operational-event-stream) sends the `subscription_cache_invalidate` internal RPC to all replicas over the `lenny-ops` headless Service. [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) **Prometheus Requirement**, Pattern 1: Prometheus scrapes `lenny-ops` inside the cluster, and no Ingress is needed for metrics scraping. No section states which paths the ops Ingress routes.

## Evidence

- `charts/lenny/templates/ops-ingress.yaml` renders one rule with `path: {{ .path | default "/" }}` and `pathType: Prefix`, backed by the `lenny-ops` Service's `http` port.
- The `http` port serves the whole mux (`--addr=:{{ .Values.ops.httpPort }}` in `charts/lenny/templates/ops-deployment.yaml`, `w.srv` in `cmd/lenny-ops/httpsurface.go`), including `POST /internal/v1/event-subscriptions/cache/invalidate` and `GET /metrics`, which `isUnauthenticatedPath` in `pkg/ops/opsserver/auth.go` exempts from the bearer check.
- The invalidate RPC is checked only by its shared-secret-derived token header (`handleCacheInvalidate` in `pkg/ops/opsserver/event_subscriptions.go`). `/metrics` relies on the metrics-scrape NetworkPolicy as its access control, which a request through the Ingress controller does not meet, because the controller pods are admitted to the `http` port.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F2), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

With `ops.ingress.enabled`, a client outside the cluster reaches a peer RPC meant for replica-to-replica traffic and the unauthenticated Prometheus surface. The invalidate path is also a write path into `ops_idempotency_keys` ([APICONV-I17](../APICONV/APICONV-I17-lenny-ops-keys-unauthenticated-requests-by-a-client-supplied-caller.md)), and its token check is the only control on it.

## Suggested resolution

Route only the public prefixes through the Ingress, `/v1/` and `/mcp/management` among them, or render a rule that denies `/internal/` and `/metrics`, and state in §25.4 which paths the ops Ingress routes. Serve the invalidate RPC and `/metrics` on a listener the Ingress does not target, if the chart keeps a prefix rule. Add a chart test that the rendered Ingress does not route `/internal/` or `/metrics`.

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0183.S4 of that change closes this issue. The change is `Draft`, and an answer to its open decisions can move the issue to another of its steps.
