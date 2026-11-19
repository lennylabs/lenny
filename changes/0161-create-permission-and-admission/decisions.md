# Decisions: 0161 Session creation applies the create permission and the admission gates on every surface

## Open decisions

None.

## Decisions

1. **When and how to fix CAPISES-I1 and CAPISES-I2.** Settled by the owner on 2026-11-19 ("all ok"): fix both issues now, together, as one small change outside program v1.

2. **How the in-process creation entry points apply the create permission (CAPISES-I1).** Settled by the owner on 2026-11-19 ("all ok"), choosing option (b) of decision 71 of [0117](../0117-gateway-package-layout/decisions.md): `CreateSessionService` and `CreateAndStartService` in `pkg/gateway/core/session/sessionserver` check the `manage_own_sessions` permission through the evaluator that `GWSEAM-AuthorizePermission` uses (`permissionGranted`), before `guardTenant`, and keep their direct handler call. The platform tools seam's `lenny/create_session` and `lenny/create_and_start_session` and the single-shot path of `cmd/lenny-gateway` then refuse a `tenant-viewer` or `billing-viewer` principal with the same 403 that REST `POST /v1/sessions` and `POST /v1/sessions/start` return. The §10.2 permission matrix row applies to the MCP and single-shot creation surfaces as well, and the specification states it where the text does not already. The options not chosen were (a), routing both entry points through the API front's request pipeline, and (c), keeping the behavior.

3. **Which gates create-and-start runs (CAPISES-I2).** Settled by the owner on 2026-11-19 ("all ok"): `handleCreateAndStart` runs the pool-drain gate (`requirePoolNotDraining`) and the refusal while both stores are unavailable, as the two-step create does. The §15.1 drain-backpressure text names `POST /v1/sessions/start` and `lenny/create_and_start_session` alongside `POST /v1/sessions` and `create_session`, and the text on the refusal while both stores are unavailable is checked and edited likewise.
