---
id: GWSEAM-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# The environment-scoped CreateSession route is held by the request pipeline but no session path routes to it

## Specification

GWSEAM-HandleRoute: a route whose access check reads the tenant, the session, or a permission is held for the request pipeline, which mounts it behind its check on the session paths. §15.1 lists `POST /v1/environments/{name}/sessions` as the environment-scoped CreateSession.

## Evidence

- `pkg/gateway/core/session/sessionserver/routes.go` registers `POST /v1/environments/{name}/sessions` with the session-create access check, so `pkg/gateway/api/routetable` holds it for the pipeline (`held`).
- The client listener's session paths are `/v1/sessions`, `/v1/sessions/`, `/v1/blobs/`, `/v1/runtimes`, `/v1/runtimes/`, and `/v1/models` (`sessionPaths` in `pkg/gateway/api/routetable/routetable.go`), so the pipeline's handler is never mounted under `/v1/environments/`.
- `cmd/lenny-gateway/testdata/route-table.golden` lists the route among the pipeline's routes (`rest  session   POST /v1/environments/{name}/sessions [session-create]`) and lists no listener pattern under `/v1/environments/`.
- In the assembled client surface of `routeTableWiring`, `mux.Handler` of `POST /v1/environments/dev/sessions` matches no pattern, so the request answers `404`.
- Found while building step 0178.S4, whose replay admission hook for the route is keyed but never reached.

## Gap

A client cannot create a session through the environment-scoped route, and the idempotency middleware's replay admission hook for it never runs.

## Suggested resolution

Add `/v1/environments/` to the client listener's session paths, or mount the route on the listener's mux, and add the route to the reachability test `TestEveryReplayAdmissionRouteIsARouteTheGatewayResolves` in `cmd/lenny-gateway`.
