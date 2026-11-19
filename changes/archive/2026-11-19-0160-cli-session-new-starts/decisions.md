# Decisions: 0160 lenny session new starts the session before the prompt

## Open decisions

None.

## Decisions

1. **When to fix CLIINST-I1.** Settled by the owner on 2026-11-18, choosing option (a) of the question raised with issue [CLIINST-I1](../../../issues/CLIINST/CLIINST-I1-session-new-prompt-before-start.md): "Make it a small change right after M2". The change is built on its own branch now and lands after the last step of milestone M2 of program v1 merges.

2. **O1. Which surface the path without `--workspace` uses.** Raised by the review of 0160.S1 on 2026-11-19. Settled by the owner on 2026-11-19, choosing option (a): keep the MCP tool `lenny/create_and_start_session`, which the current §24.17 introduction prescribes for session commands, and 0138.S4 replaces the path and removes `MCPClient.CreateAndStartSession`. The option not chosen was (b), adding a REST create-and-start method to the SDK and mapping the §24.17 row to `POST /v1/sessions/start` ahead of 0138's specification step. 0160.S1 is built as option (a) states.

3. **O2. Whether 0160.S1 waits on the create-and-start admission gap.** Raised by the review of 0160.S1 on 2026-11-19. Settled by the owner on 2026-11-19, choosing option (a): land 0160.S1 as built and fix issue [CAPISES-I2](../../../issues/closed/CAPISES/CAPISES-I2-create-and-start-skips-drain-and-degraded-gates.md) separately, in a change of its own or in the change that lands the sessions page. The options not chosen were (b), widening 0160 with a second step that adds both gates to `handleCreateAndStart`, and (c), holding 0160.S1 until CAPISES-I2 is fixed. 0160.S1 merged as built.
