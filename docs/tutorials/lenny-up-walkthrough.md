---
layout: default
title: "`lenny up` Walkthrough"
parent: Tutorials
nav_order: 0
description: Install the Lenny CLI, bring up the local stack, run a session against the chat and claude-code reference runtimes, and explore the web playground.
---

# `lenny up` Walkthrough

**Persona:** Everyone (first contact) | **Difficulty:** Beginner

{: .highlight }
> **Status: planned.** This walkthrough is scheduled for the initial tutorial set. The canonical behavior is defined in the spec sections linked below; until the walkthrough lands, follow the spec or [Your First Session](first-session) for a hands-on introduction.

`lenny up` is the zero-config local bring-up: one command spins up the gateway, Postgres, Redis, and the bundled reference runtimes (`chat`, `claude-code`, `gemini-cli`, `codex`, `cursor-cli`) as a Docker Compose stack, and opens the web playground in your browser.

## What this walkthrough will cover

1. Install the CLI: `brew install lennylabs/tap/lenny` or `go install github.com/lennylabs/lenny/cmd/lenny-ctl@latest`.
2. Run `lenny up` and watch the stack come up (gateway on `https://localhost:8443`, playground on `https://localhost:8443/ui`).
3. Drive a session against `chat` from the CLI.
4. Drive a session against `claude-code` with a local workspace.
5. Open the web playground and repeat the same interaction without code.
6. Tear everything down with `lenny down` (or leave it up and iterate).

## Canonical references

- Spec §24.19 — `lenny-ctl` local stack (`lenny up`, `lenny down`, `lenny status`)
- Spec §26 — reference-runtime catalog (the runtimes bundled into `lenny up`)
- Spec §27 — web playground (the UI served by `lenny up`)

## Related tutorials

- [Your First Session](first-session) — drives the same stack programmatically
- [Web Playground Tour](playground-tour) — guided tour of the playground UI
