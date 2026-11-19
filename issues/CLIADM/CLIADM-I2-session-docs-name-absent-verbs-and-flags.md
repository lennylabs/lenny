---
id: CLIADM-I2
status: open
kind: doc-gap
severity: medium
requirements: []
---

# The first-session tutorial and the client guide name `lenny session` verbs and flags the CLI does not have

## Specification

The §24.17 session command table lists `lenny session new`, `attach`, `send`, `interrupt`, `cancel`, `list`, `get`, and `logs <sessionId> [--since <time>]`. Its closing paragraph states that the session commands honor the `--api-url` and `LENNY_API_URL` discovery rules, and `pkg/embedded/localcli/session.go` reads the bearer from `--token` or `LENNY_API_TOKEN`.

## Evidence

- `docs/tutorials/first-session.md`, Path A: step 3 runs `lenny session logs $SESSION_ID --follow`, step 5 runs `lenny session transcript` and `lenny session artifacts`, and step 6 runs `lenny session extend-retention`. None of these verbs or flags is in §24.17 or in `cmdSession`, which refuses an unknown subcommand and an unknown flag with status 2.
- `docs/tutorials/first-session.md`, "Before you start": it points the CLI at a deployed cluster with `LENNY_GATEWAY` and `LENNY_TOKEN`, which the CLI does not read.
- `docs/tutorials/first-session.md`, steps 2 and 6: the sample outputs (`message_id:` and `response:` lines, and `state: completed` after `cancel`) do not match what `cmdSessionSend` and `cmdSessionCancel` print, which is the reply text and `<sessionId> cancelled`.
- `docs/client-guide/index.md`, "The `lenny session` CLI": it runs `lenny session logs $SESSION_ID --follow` and tells a remote user to add `--gateway https://lenny.example.com --token $TOKEN`; the CLI has no `--gateway` flag.
- Found while change 0160 (step 0160.S1) corrected the `--message` flag in the `lenny session new` examples of these pages.

## Gap

A reader who follows the first-session tutorial or the client guide past the first command runs commands the CLI refuses, and a reader targeting a deployed cluster sets variables the CLI ignores.

## Suggested resolution

Rewrite Path A of `docs/tutorials/first-session.md` and the CLI block of `docs/client-guide/index.md` with the verbs and flags §24.17 lists, `--api-url`/`LENNY_API_URL` and `--token`/`LENNY_API_TOKEN`, and outputs the CLI produces. If the transcript, artifact, follow, or retention commands are wanted, raise them as a change to §24.17 first.
