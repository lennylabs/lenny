---
id: APICONV-I13
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0178.S4
---

# An idempotent replay of a session route skips the request pipeline's permission gate and ownership check

## Specification

§11.5 Idempotency; §10.2 session ownership (the owner, or a caller the session's access rules admit, acts on a session). A replay returns the response of an operation the caller is entitled to run.

## Evidence

- The REST idempotency middleware wraps the client listener's mux (`cmd/lenny-gateway/httpsurface.go`, `idempotencymw.Wrap`), and a replay returns before the mux runs (`pkg/idempotency/idempotencymw/idempotency.go`).
- The request pipeline's `guard` (`pkg/gateway/api/pipeline/access.go`) runs the permission gate and `AuthorizeSession` with `r.PathValue("id")` inside the mux, so it does not run on a replay of `/v1/sessions/{id}/finalize`, `start`, `resume`, `derive`, or `tool-use/{toolCallId}/approve` and `deny`. The session-creating routes `POST /v1/sessions`, `POST /v1/environments/{name}/sessions`, and `POST /v1/sessions/start` are pipeline routes with `AccessSessionCreate`, which `guard` turns into the `manage_own_sessions` permission gate (`pkg/gateway/core/session/sessionserver/routes.go`), and a replay of them skips that gate as well.
- A create replay also skips the create handler's own checks of the caller: the §10.6 environment admission, the §27.5 playground runtime boundary (`allowedRuntimes`), and the §12.8 lifecycle fence (`create.go`). The replay of `lenny/delegate_task` runs only the creating-caller check (`replay_admission.go`), and skips the ownership check of the parent session, the §10.6 admission of the target, and the user gate.
- The replay admission of 0178 decision 8 runs only the creating-caller check, the §11.4 gate, and the Article 18 restriction, and only where the handler runs them (`cmd/lenny-gateway/idempotency_wiring.go` on the branch of step 0178.S3); finalize, start, and resume replay with no user gate.
- Running `guard` from the middleware fails closed, because `ServeMux.Handler` does not populate the path values outside the mux.
- Found by the design memo of 2026-11-27 and confirmed by its security review (M2) and design-cost review (L1).

## Gap

A caller of the subject whose permission was revoked, or who no longer passes the session's ownership check, receives the stored response of a session operation by replaying its key and body. The stored response describes the session's state at the original call.

## Suggested resolution

Run the idempotency lookup of every stored REST route, the session-creating routes included, inside the pipeline after `guard` on the client listener, so a replay passes the route's permission gate and, on a session route, its ownership check by construction. Before every replay, run the §11.4 gate and the Article 18 restriction; on a session-creating route or tool, also the creating-caller check, the §12.8 fence, the §10.6 environment admission, and the §27.5 playground runtime boundary; and on `lenny/delegate_task`, the creating-caller check, the user gate, the ownership check of the parent, and the §10.6 admission of the target. Change 0178 states it as IP-7, under its open decision O19.

The issue stays `open` until the owner answers O19 of change 0178; under O19 (a) or (b), step 0178.S4 closes it.

The owner answered O19 of change 0178 with option (a) on 2026-11-27 (0178 decision 18, IP-19). Step 0178.S4 closes this issue.
