# Steps: 0161 Session creation applies the create permission and the admission gates on every surface

## Validation owed

None.

## Steps

### 0161.S1 Create permission and admission gates on every creation surface
- status: Building
- lanes: [specification, code, documentation]
- closes: [CAPISES-I1, CAPISES-I2]

§10.2 states that the "Create / cancel own sessions" row governs every creation surface, §10.1.3 and the §15.1 drain row name every creation surface in the dual-store refusal and the drain backpressure, and §15 **Built-in adapter single-shot compute model.** lists the create permission, the dual-store refusal, and the pool-drain gate among the gates it shares with `POST /v1/sessions/start`. `CreateSessionService` and `CreateAndStartService` check `manage_own_sessions` before `guardTenant` (decision 2), and `handleCreateAndStart` runs the dual-store refusal and the pool-drain gate at the points the two-step create runs them (decision 3), with unit tests in the session core, the platform tools MCP tools package, and `cmd/lenny-gateway`. The REST and MCP reference pages list the refusals.
