# Steps: 0160 lenny session new starts the session before the prompt

## Validation owed

The `end-user-first-session` persona journey of change 0157 runs `lenny session new` with a prompt against a local stack. A run of it after this step lands confirms the fix end to end; the step does not wait on it.

## Steps

### 0160.S1 Start the session before delivering the prompt
- status: Landed
- lanes: [specification, code, documentation]
- closes: [CLIINST-I1]

The `lenny session new` row of §24.17 states that the command starts the session on both paths and delivers a positional prompt after the session has started, and maps the MCP path to `lenny/create_and_start_session`. The Go client SDK gains `MCPClient.CreateAndStartSession`, and `cmdSessionNew` in `pkg/embedded/localcli` creates the session through it when neither `--workspace` nor `--file` is given, with unit tests and an SDK contract test. The documentation examples that pass the absent `--message` flag use the positional prompt. The step lands after the last step of milestone M2 merges (decision 1).
