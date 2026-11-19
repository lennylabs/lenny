---
id: GWAPI-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# Four session-handler routes are registered but no listener reaches them

## Specification

§15.1 lists `GET /v1/usage`, `GET /v1/pools`, `POST /v1/environments/{name}/sessions`, and `GET /v1/metering/events` among the client REST endpoints, which the gateway serves on its client listener.

## Evidence

- While building 0117.S8 on 2026-11-18, the production route-table guard `TestGatewayRouteTableIsFixed` (`cmd/lenny-gateway/route_table_test.go` on branch `change/0117.S8-api-front`) dumped the session server's client handler and the REST listener's patterns. The session handler mounts the four routes, but the REST listener mounts no path prefix that dispatches to the session handler for them, so a request to any of them gets the listener's 404.
- `main` at `d4d42b1f4` has the same gap: the guard's golden file was generated from main's wiring before 0117.S8 moved anything.

## Gap

Clients cannot reach usage, pool listing, environment-scoped session creation, or metering event reads over REST, although the session server implements them.

## Suggested resolution

Mount each route on the REST listener, through the API front's route table once 0117.S8 lands, with the access check the session handler applies. Add a test that every pattern the session handler mounts is reachable from some listener, and update the route-table golden.
