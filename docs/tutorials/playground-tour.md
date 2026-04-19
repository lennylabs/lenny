---
layout: default
title: "Web Playground Tour"
parent: Tutorials
nav_order: 14
description: Pick a runtime, upload a workspace, send messages, observe streaming output, and inspect artifacts in the web playground — no code required.
---

# Web Playground Tour

**Persona:** Everyone (first contact) | **Difficulty:** Beginner

{: .highlight }
> **Status: planned.** This tour is scheduled for the initial tutorial set. The canonical UI behavior is defined in the spec section linked below; until the tour lands, follow the spec or launch the playground yourself with `lenny up`.

The web playground is the gateway's built-in UI for creating and driving sessions interactively. It authenticates through your identity provider, lets you pick a runtime and upload a workspace, streams tokens as they arrive, and surfaces artifacts, tool calls, and delegation trees.

## What this tour will cover

1. Open the playground at `https://localhost:8443/ui` after `lenny up`.
2. Sign in (dev-mode bypass on local; OIDC on production).
3. Pick a runtime from the catalog and see its capability matrix.
4. Drag-and-drop a workspace, or skip workspace upload entirely.
5. Send a message; watch `agent_output`, `tool_call`, and `tool_result` events stream in.
6. Inspect artifacts — files the agent produced, along with diffs and screenshots.
7. Follow a delegation tree into child sessions and back.
8. Export the full transcript or replay against a different runtime version.

## Canonical reference

- Spec §27 — web playground (UI components, auth flow, event rendering, artifact surface)

## Related tutorials

- [`lenny up` Walkthrough](lenny-up-walkthrough) — gets the playground running locally
- [Your First Session](first-session) — the same session flow, from the CLI
