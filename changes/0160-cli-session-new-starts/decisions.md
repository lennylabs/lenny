# Decisions: 0160 lenny session new starts the session before the prompt

## Open decisions

Both were raised by the review of 0160.S1 on 2026-11-19. The branch is built as each recommendation states, so neither recommendation needs further work on it.

1. **O1. Which surface the path without `--workspace` uses.** Change [0138](../0138-rest-only-client-api/change.md) (REST-only client API, program v1, every decision settled, no step started) maps `lenny session new` to REST `POST /v1/sessions/start` and removes the SDKs' MCP clients in 0138.S4 and 0138.S6. 0160.S1 uses the MCP tool `lenny/create_and_start_session` and adds `MCPClient.CreateAndStartSession`, which 0138 then removes. Both reach the same gateway handler, `handleCreateAndStart`.

   - (a) Keep the MCP tool, which the current §24.17 introduction prescribes for session commands, and record that 0138.S4 replaces the path and removes the method. **Recommended**: it matches the specification as it stands, and the code 0138 removes is one SDK method and one call.
   - (b) Take the REST path now: add a REST create-and-start method to the SDK's REST client, call it from `cmdSessionNew`, and map the §24.17 row to `POST /v1/sessions/start`. This anticipates 0138, but it edits the §24.17 introduction ahead of 0138's own specification step, and 0160 is outside program v1.

2. **O2. Whether 0160.S1 waits on the create-and-start admission gap.** `handleCreateAndStart` does not run the pool-drain gate or the refusal while both stores are unavailable, which the two-step create runs, and §15.1 names only `POST /v1/sessions` and `create_session` for the drain backpressure. 0160.S1 puts the default `lenny session new` path on that handler. Issue [CAPISES-I2](../../issues/CAPISES/CAPISES-I2-create-and-start-skips-drain-and-degraded-gates.md) records it.

   - (a) Land 0160.S1 as built and fix CAPISES-I2 separately, in a change of its own or in the change that lands the sessions page. **Recommended**: the gap exists today for every caller of `POST /v1/sessions/start` and `lenny/create_and_start_session`, and the CLI without the fix cannot deliver a prompt at all.
   - (b) Widen 0160 with a second step that edits the §15.1 drain text and adds both gates to `handleCreateAndStart`, and land the two steps together.
   - (c) Hold 0160.S1 until CAPISES-I2 is fixed elsewhere.

## Decisions

1. **When to fix CLIINST-I1.** Settled by the owner on 2026-11-18, choosing option (a) of the question raised with issue [CLIINST-I1](../../issues/CLIINST/CLIINST-I1-session-new-prompt-before-start.md): "Make it a small change right after M2". The change is built on its own branch now and lands after the last step of milestone M2 of program v1 merges.
