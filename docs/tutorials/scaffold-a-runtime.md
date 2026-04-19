---
layout: default
title: "Scaffold a Runtime with `lenny runtime init`"
parent: Tutorials
nav_order: 16
description: Generate a Basic-level runtime scaffold in Go, Python, or TypeScript; build the container image; register it with the gateway; and run your first session against it.
---

# Scaffold a Runtime with `lenny runtime init`

**Persona:** Runtime Author | **Difficulty:** Beginner

{: .highlight }
> **Status: planned.** This scaffolding walkthrough is scheduled for the initial tutorial set. The canonical CLI behavior is defined in the spec section below; until the walkthrough lands, follow the spec or [Build a Runtime Adapter](build-a-runtime) for the manual path.

`lenny-ctl runtime init` is the fastest path from zero to a registered runtime. It generates a project that already speaks the runtime adapter protocol, ships a multi-stage Dockerfile, and includes tests, CI, and a Helm-compatible manifest.

## What this walkthrough will cover

1. Run `lenny-ctl runtime init --language go --name my-runtime` (or `--language python`, `--language typescript`).
2. Tour the generated project: `main.go` or equivalent, `Dockerfile`, `Makefile`, `.github/workflows/`, `manifest.yaml`.
3. Implement the `onMessage` hook — where the agent logic lives.
4. Build the image with `make image`; push it or load it into your local stack.
5. Register the runtime with `lenny-ctl runtime register --manifest manifest.yaml`.
6. Create a session against your new runtime and verify it responds.
7. Run the bundled `make conformance` target, which exercises the runtime adapter contract end-to-end.

## Canonical reference

- Spec §24.18 — `lenny-ctl runtime init` scaffolding behavior, generated project shape
- Spec §5 — runtime registry and pool model (how the manifest is consumed)
- Spec §26 — reference-runtime catalog (working examples you can diff against)

## Related tutorials

- [Build a Runtime Adapter](build-a-runtime) — the calculator runtime, hand-written
- [Runtime SDK Integration](runtime-sdk-integration) — layer platform tools on top
- [Wrap a Coding-Agent CLI](wrap-coding-agent-cli) — fork a reference runtime
