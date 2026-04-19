---
layout: default
title: "Runtime SDK Integration (Go / Python / TS)"
parent: Tutorials
nav_order: 18
description: Use the official Lenny runtime SDKs to step a Basic runtime up to Standard (platform tool access) and Full (lifecycle signals and delegation).
---

# Runtime SDK Integration (Go / Python / TypeScript)

**Persona:** Runtime Author | **Difficulty:** Intermediate

{: .highlight }
> **Status: planned.** This tutorial is scheduled for the initial tutorial set. The runtime integration levels are canonical in the spec section below; until the walkthrough lands, consult the spec and the SDK READMEs.

Lenny ships first-party runtime SDKs in Go, Python, and TypeScript. They encapsulate the runtime adapter protocol so your agent code calls idiomatic functions — `session.SendOutput(...)`, `session.OnMessage(func (m) { ... })` — instead of hand-assembling JSON-lines frames.

The SDKs also gate access to the three runtime **integration levels**:

- **Basic**: speak the adapter protocol; no platform tools.
- **Standard**: additionally, call gateway-hosted platform tools (web search, code execution, connectors).
- **Full**: additionally, subscribe to lifecycle signals (budget pressure, delegation ancestry, isolation constraints) and participate as a delegation coordinator.

## What this walkthrough will cover

1. Install the SDK for your language; wire it into a scaffolded runtime.
2. Upgrade from Basic to Standard: declare platform-tool requirements in the manifest, then call them from the handler.
3. Upgrade from Standard to Full: subscribe to budget events, delegation context, and isolation constraints; emit structured reasoning traces.
4. Handle elicitation from inside the runtime (prompt the user mid-session).
5. Observe the runtime in the gateway's tracing and metrics.

## Canonical reference

- Spec §5 — runtime registry (integration levels, capability matrix)
- Spec §8 — recursive delegation (relevant at the Full level)
- Spec §11 — policy and controls (budgets, scope narrowing)

## Related tutorials

- [Scaffold a Runtime](scaffold-a-runtime) — generates a Basic-level starter
- [Build a Runtime Adapter](build-a-runtime) — the protocol under the SDK
- [Recursive Delegation](recursive-delegation) — put Full-level features to work
