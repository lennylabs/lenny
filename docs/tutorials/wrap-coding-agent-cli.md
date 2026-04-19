---
layout: default
title: "Wrap a Coding-Agent CLI"
parent: Tutorials
nav_order: 17
description: Fork one of the reference runtimes (claude-code, gemini-cli, codex, cursor-cli) to wrap your own CLI-based coding agent in a sandboxed workspace.
---

# Wrap a Coding-Agent CLI

**Persona:** Runtime Author | **Difficulty:** Intermediate

{: .highlight }
> **Status: planned.** This tutorial is scheduled for the initial tutorial set. The reference-runtime catalog is canonical in the spec section below; until the walkthrough lands, clone one of the bundled runtimes and adapt it to your CLI.

If you already have a coding agent that runs as a CLI (LLM-backed or otherwise), wrapping it as a Lenny runtime is the shortest path to giving it a sandboxed workspace, streaming output, artifact export, and optional delegation — without rewriting its core loop.

## What this walkthrough will cover

1. Pick the reference runtime closest to your CLI's shape: `claude-code`, `gemini-cli`, `codex`, or `cursor-cli`.
2. Fork the runtime repo, rename the module, and wire in your CLI invocation.
3. Map the CLI's `stdout` to `agent_output` events; map its `stderr` to `log` events.
4. Translate the CLI's input-waiting state to Lenny's `input_required` status.
5. Handle the workspace: your CLI runs inside `/workspace/current`, which Lenny materializes from the session's workspace plan.
6. Surface tool calls. If your CLI exposes tool-call JSON, forward it as structured events; otherwise, parse text markers.
7. Export artifacts: on session end, Lenny seals `/workspace/current` and delivers it to the gateway automatically.
8. Add conformance tests so your runtime passes the same suite as the reference runtimes.

## Canonical reference

- Spec §26 — reference-runtime catalog (the four wrappable runtimes, their capability matrices)
- Spec §5 — runtime registry and pool model
- Spec §7 — session lifecycle (events your wrapper must emit)

## Related tutorials

- [Scaffold a Runtime with `lenny runtime init`](scaffold-a-runtime) — start from a fresh template instead
- [Build a Runtime Adapter](build-a-runtime) — learn the protocol fundamentals
- [Runtime SDK Integration](runtime-sdk-integration) — step up to Standard and Full integration levels
