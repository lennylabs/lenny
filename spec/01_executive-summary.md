## 1. Executive Summary

Lenny is a **Kubernetes-native, runtime-agnostic agent session platform** that provides on-demand, pre-warmed, isolated cloud agent instances to clients. It is not tied to any single agent runtime — it defines a standard contract that any compliant pod binary can implement.

The platform solves a specific problem: teams need cloud-hosted agent sessions (e.g., Claude Code, custom agents) that start fast, run in isolation, support long-lived interactive workflows, and can delegate work recursively under platform-enforced budgets, narrowing scope, and monotonic isolation — all behind a unified gateway that owns lifecycle, policy, security, and MCP-facing behavior.

For a concise explanation of where Lenny fits relative to Temporal, Modal, LangGraph, E2B, Fly.io Sprites, and Daytona — including target personas and explicit trade-offs — see **[Section 23](23_competitive-landscape.md) (Competitive Landscape)**, specifically **[Section 23.1](23_competitive-landscape.md#231-why-lenny) (Why Lenny?)**.

### Core Design Principles

1. **Gateway-centric**: All external interaction goes through the gateway. Pods are internal workers, never directly exposed.
2. **Pre-warm everything possible**: Pods are warm before requests arrive. Workspace setup is the only hot-path work.
3. **Pod-local workspace, gateway-owned state**: Pod disk is a cache. Session truth lives in durable stores.
4. **MCP for interaction, custom protocol for infrastructure**: Use MCP where its semantics matter (tasks, elicitation, auth, delegation). Use a custom protocol for lifecycle plumbing.
5. **Recursive delegation as a platform primitive**: Any pod can delegate to other pods through gateway-mediated tools. The gateway enforces scope, budget, and lineage.
6. **Least privilege by default**: No broad credentials in pods. No shared mounts. Gateway-mediated file delivery only.

> **For Runtime Authors: Start Here.** If you are building a custom agent binary or adapter to run on Lenny, you do not need to read the full specification to get started. Go directly to **[Section 15.4.5](15_external-api-surface.md#1545-runtime-author-roadmap) (Runtime Author Roadmap)** for a guided reading path organized by integration level (Basic → Standard → Full). The echo runtime sample ([Section 15.4.4](15_external-api-surface.md#1544-sample-echo-runtime)) is your copy-paste starting point, and `make run` ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)) lets you test locally without Kubernetes. The roadmap will tell you exactly which sections are relevant for your tier and when to read them.

