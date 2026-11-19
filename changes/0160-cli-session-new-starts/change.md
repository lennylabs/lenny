# lenny session new starts the session before the prompt

Status: `In progress`, revision 2, 2026-11-19.

## Problem

`lenny session new --runtime <name> "prompt"` creates a session and then fails to deliver the prompt. The gateway answers `TARGET_NOT_READY` because the session is still in `created`. Issue [CLIINST-I1](../../issues/CLIINST/CLIINST-I1-session-new-prompt-before-start.md) records the failure.

Evidence from the tree:

- The 0157 pilot journey `end-user-first-session`, run `20261118T165838Z-1ae96b`, trial 1, failed. The agent ran `lenny-ctl session new --runtime echo-runtime-sidecar "hello-<nonce>"`. The command created the session and exited with `deliver prompt: lenny: TARGET_NOT_READY (TRANSIENT): session has not yet entered running state; retry after start`, and the session stayed in `created`.
- `pkg/embedded/localcli/session.go`, `cmdSessionNew`: without `--workspace` or `--file`, it calls the SDK's `MCP().CreateSession`, which invokes the MCP tool `lenny/create_session`, and then `SendMessages`, with no finalize or start in between. The `--workspace` and `--file` path (`createSessionWithWorkspace`) finalizes and starts before the prompt is sent.
- [§15.2](../../spec/15_external-api-surface.md#152-mcp-api) and [§15.2.1](../../spec/15_external-api-surface.md#1521-restmcp-consistency-contract) make `lenny/create_session` the counterpart of `POST /v1/sessions`, which leaves the session in `created` until finalize and start run.
- The `lenny session new` row of the session command table in [§24.17](../../spec/24_lenny-ctl-command-reference.md#2417-session-operations) (Session Operations) maps the command to MCP `lenny/create_session` without `--workspace`. It does not name the positional prompt, and it does not say whether the command starts the session, so neither the CLI nor the documentation can be checked against it.
- No `lenny session` verb starts a session that is in `created`, so a session the command leaves there cannot be used from the CLI. `lenny session send` to it fails the same way.

The contract test `TestGoClientMCP` in `tests/contract/sdks` did not catch the defect, because its gateway wires a session creator that starts the session on `lenny/create_session`.

## Scope

The change covers the `lenny session new` command on both of its paths: the MCP path without `--workspace` or `--file`, and the REST staging path with either flag. The command starts the session on both paths, with or without a prompt, and delivers a positional prompt only after the session is `running`.

The change leaves the other `lenny session` verbs, the gateway, and the MCP tools unchanged. It does not change how the prompt is delivered: the command keeps sending it with REST `POST /v1/sessions/{id}/messages`, which the specification row now names.

## Terms

| Term | Meaning |
| :-- | :-- |
| `lenny/create_and_start_session` | The MCP tool of [§15.2](../../spec/15_external-api-surface.md#152-mcp-api) that creates a session and starts it in one call. The gateway runs it as REST `POST /v1/sessions/start` through the shared service layer ([§15.2.1](../../spec/15_external-api-surface.md#1521-restmcp-consistency-contract) rule 1), and its result is the REST response body, a session in `running` with its identifier in the `id` field. |
| Positional prompt | The command's positional arguments joined with spaces. The command sends it as the session's first user message. |

## High-level design

On the MCP path, the command calls `lenny/create_and_start_session` in place of `lenny/create_session`. The gateway creates the session, runs the finalize step with an empty workspace plan and the setup commands, and starts the runtime in that one call, so the session is `running` when the call returns. The command then sends the positional prompt, prints the session identifier, and attaches when `--attach` is set or stdout is a terminal. The staging path keeps its sequence of create, upload, finalize, and start over REST, which already starts the session before the prompt.

When the create-and-start call fails, the command reports the error, sends no prompt, prints no session identifier, and exits with status 1. When the session has started and the prompt cannot be delivered, the command exits with status 1 and names the running session in its error, so the caller can resend the prompt with `lenny session send` or release the session with `lenny session cancel`.

## Requirements

None by identifier. The change modifies the `lenny session new` row of the §24.17 session command table. That section is content of the placeholder page CLIADM (`spec/interfaces/cli/admin-commands.md`), which has no requirement identifiers yet.

## Current state

`cmdSessionNew` calls `lenny/create_session` and then sends the prompt, as the Problem section states. The Go client SDK's `MCPClient` has no method for `lenny/create_and_start_session`, and `MCPCreateSessionResult` does not carry the upload token that a finalize call would need.

## Detailed design

### CT-1 Create and start over MCP

The SDK gains `MCPClient.CreateAndStartSession(ctx, runtimeRef)`, which invokes `lenny/create_and_start_session` with the `runtimeRef` argument and decodes the session identifier from the result's `id` field and its state from `state`. A result with `isError` set is returned as an error, as `MCPClient.CreateSession` does. The SDK sends `tools/call` once and never retries it, so a transport failure does not create a second session.

`cmdSessionNew` creates the session through a helper that chooses the path: the staging flow when `--workspace` or `--file` is given, and `CreateAndStartSession` otherwise. The prompt is sent only after the helper returns a session identifier.

The alternatives considered:

- `lenny/create_session`, then `lenny/finalize_workspace`, then `lenny/start_session`. This keeps the row's `lenny/create_session` mapping, but it needs three round trips, the SDK would have to surface the upload token from the create result, and a failure or an interrupt between calls leaves a session in `created` or `ready` that no CLI verb can start.
- The SDK's REST `Finalize` and `Start` after the MCP create. This mixes the MCP and REST surfaces on one path and needs the same upload token.
- REST `POST /v1/sessions/start`. The REST client does not expose it, and it would move the path off the MCP surface that the §24.17 introduction names for session commands.

`lenny/create_and_start_session` keeps the path on MCP and leaves no window in which the CLI holds a session it has not started. Its handler runs the owner binding, quota, concurrency, admission-rate, policy-chain, and environment-admission gates of the two-step create, but not the pool-drain gate or the refusal while both stores are unavailable; issue [CAPISES-I2](../../issues/CAPISES/CAPISES-I2-create-and-start-skips-drain-and-degraded-gates.md) records the gap, and open decision O2 asks whether this step waits on it.

Change [0138](../0138-rest-only-client-api/change.md) (REST-only client API, program v1) maps `lenny session new` to REST `POST /v1/sessions/start` and removes the SDKs' MCP clients in its steps 0138.S4 and 0138.S6, so it replaces this path and `MCPClient.CreateAndStartSession` when it lands. Open decision O1 asks whether 0160 takes the REST path now.

### CT-2 Specification row

The `lenny session new` row of §24.17 names the positional prompt in the command, states that the command starts the session on both paths and delivers a positional prompt after the session has started, states what the command reports when the start or the delivery fails, and maps the MCP path to `lenny/create_and_start_session` and the prompt to REST `POST /v1/sessions/{id}/messages`.

## Specification and non-specification changes

- [§24.17](../../spec/24_lenny-ctl-command-reference.md#2417-session-operations): the `lenny session new` row.
- `sdks/client/go/lenny/mcp.go`: `MCPClient.CreateAndStartSession` and its result type.
- `pkg/embedded/localcli/session.go`: `cmdSessionNew` and a new helper, `createStartedSession`.
- `tests/registers/spec-section-citations.yaml`: the section-citation counts of the packages whose annotations change.

## Documentation

- `docs/operator-guide/lenny-ctl.md` (operator): the `lenny session new` row of the session command table gains the positional prompt and states that the command starts the session before it delivers the prompt.
- `docs/tutorials/first-session.md` (client developer): step 1 of the CLI path uses the positional prompt with `--attach`, the form the CLI accepts, in place of the absent `--message` flag, and describes the output the command produces.
- `docs/client-guide/index.md` (client developer), `docs/runtime-author-guide/index.md`, `docs/runtime-author-guide/local-development.md`, and `docs/runtime-author-guide/echo-runtime.md` (runtime author): each `lenny session new ... --message "..."` example becomes a positional prompt.
- `README.md` and `docs/index.md` (operator and client developer): the local-trial example uses `--runtime echo`, because the CLI refuses the `--runtime=echo` form.
- `docs/client-guide/index.md`: the CLI block captures the session ID with `SESSION_ID=$(lenny session new ...)`, so the following commands have it.
- `docs/getting-started/quickstart.md` already shows the working form and needs no edit.

The other drift found on these pages, verbs and flags the CLI does not implement, the curl example of the tutorial's Path C, and the `=` flag form in the specification's examples, is filed as issue [CLIADM-I2](../../issues/CLIADM/CLIADM-I2-session-docs-name-absent-verbs-and-flags.md) rather than fixed here.

## Tests

Unit tier, each citing §24.17 (Session Operations):

- `pkg/embedded/localcli`: a fake gateway that refuses a message to a session that is not `running` shows that `lenny session new` with a prompt and no `--workspace` starts the session before it sends the prompt, through `lenny/create_and_start_session` and never `lenny/create_session`;
- `pkg/embedded/localcli`: without a prompt, the command still starts the session and sends no message;
- `pkg/embedded/localcli`: when `lenny/create_and_start_session` fails, the command exits with status 1, reports the gateway's error, sends no prompt, and prints no session identifier;
- `pkg/embedded/localcli`: when the session starts and the prompt is refused, the command exits with status 1 and names the started session and the gateway's reason;
- `sdks/client/go/lenny`: `CreateAndStartSession` decodes the `id` and `state` of the tool result, returns a tool failure as an error, and returns a JSON-RPC error as an error.

Contract tier, in `tests/contract/sdks`: the SDK's `CreateAndStartSession` against the gateway's in-process MCP server returns a `running` session, and a message sent to it reaches the runtime. The test cites §15.2 (MCP API).

The `end-user-first-session` persona journey verifies the fix end to end; the change does not run it.
