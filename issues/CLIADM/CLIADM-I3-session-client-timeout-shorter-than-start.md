---
id: CLIADM-I3
status: open
kind: build-gap
severity: low
requirements: []
---

# The `lenny session` client times out after 30 seconds, which a synchronous session start can exceed

## Specification

§24.17 states that `lenny session new` creates and starts the session before it delivers a prompt, on the MCP path through `lenny/create_and_start_session` and on the staging path through `POST /v1/sessions/{id}/start`. Both calls return only once the session is `running`, which includes the pod claim, workspace materialization, setup commands, and the runtime launch. Their bounds include `podClaimQueueTimeout` (60 seconds by default), `runtime.setupTimeoutSeconds` (300 seconds), and `maxStartingTimeoutSeconds` (120 seconds).

## Evidence

- `sdks/client/go/lenny/client.go`, `New`: the default HTTP client is `&http.Client{Timeout: 30 * time.Second}`, and `MCP()` reuses it.
- `pkg/embedded/localcli/session.go`, `sessionClient`: builds the client with `lenny.New` and no `WithHTTPClient`, so every `lenny session` call carries the 30-second limit.
- The review of change 0160 (step 0160.S1) on 2026-11-19 found the gap. 0160 moves the default `lenny session new` path onto the synchronous create-and-start call; the staging path's `Start` had the same limit before it.

## Gap

For a runtime whose start takes longer than 30 seconds, such as one with setup commands or a cold pool, `lenny session new` fails with a client timeout every time and reports no session ID. The gateway rolls the claim back, unless the cancellation lands after the session row is written, in which case a running session is left that the user was not told about.

## Suggested resolution

Give the CLI's session client no fixed total timeout on the create and start calls, with the command's context cancelled on SIGINT and SIGTERM, or a deadline derived from the start bounds above. A fixed value that the specification does not define needs a flag, as `.claude/rules/code-best-practices.md` states.
