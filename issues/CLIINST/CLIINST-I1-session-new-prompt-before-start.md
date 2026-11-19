---
id: CLIINST-I1
status: planned
kind: spec-defect
severity: high
requirements: []
change: 0160.S1
---

# `lenny session new` with a prompt never starts the session

## Specification

§24.17, the `lenny session new` row of the session command table, maps the command to MCP `lenny/create_session` plus the event stream, and to REST `POST /v1/sessions`, upload, finalize, and start when `--workspace` or `--file` is given. It does not name a positional prompt, which the CLI and the documentation treat as the session's opening prompt, and it does not state that the command finalizes and starts the session on the MCP path. §15.1 and §15.2 define `lenny/create_session` as equivalent to `POST /v1/sessions`, which leaves the session in `created` until `POST /v1/sessions/{id}/finalize` and `/start` run.

## Evidence

- The 0157 pilot journey `end-user-first-session`, run `20261118T165838Z-1ae96b`, trial 1, failed. The agent ran `lenny-ctl session new --runtime echo-runtime-sidecar "hello-<nonce>"` as `docs/tutorials/first-session.md` shows. The command created the session and then exited with `deliver prompt: lenny: TARGET_NOT_READY (TRANSIENT): session has not yet entered running state; retry after start`, and the session stayed in `created`.
- `pkg/embedded/localcli/session.go`, `cmdSessionNew`: without `--workspace` or `--file` it calls `client.MCP().CreateSession` and then `client.SendMessages`, with no finalize or start in between. The `--workspace` path (`createSessionWithWorkspace`) calls `client.Start`.
- `docs/tutorials/first-session.md`, `docs/index.md`, and `docs/runtime-author-guide/index.md` show `lenny session new --runtime <name> [--attach] "prompt"` producing a running session.

## Gap

The documented first-session path fails for every user of the local CLI who passes a prompt without a workspace. The specification row does not say whether the command starts the session, so neither the CLI nor the documentation can be checked against it.

## Suggested resolution

State in the CLI page, when it lands, that `lenny session new` finalizes and starts the session before it delivers a positional prompt on both paths, and make `cmdSessionNew` do so (or create through `POST /v1/sessions/start`, which creates and starts in one call). Add a test that a prompt given without `--workspace` reaches a running session. The `end-user-first-session` journey verifies the fix.
