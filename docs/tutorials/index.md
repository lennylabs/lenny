---
layout: default
title: Tutorials
nav_order: 2
has_children: true
---

# Tutorials

Hands-on walkthroughs that take you from zero to a working Lenny deployment. Each tutorial is self-contained with complete code examples, expected outputs, and detailed explanations.

## By Persona

### Everyone

| Tutorial | Difficulty | Description |
|----------|------------|-------------|
| [`lenny up` Walkthrough](lenny-up-walkthrough) | Beginner | Install the CLI, start the Tier 0 embedded stack, attach to `chat` and `claude-code`, explore the web playground -- all in 5 minutes |
| [Web Playground Tour](playground-tour) | Beginner | Use the bundled playground to pick a runtime, upload a workspace, and drive a session end-to-end without writing any code |

### Client Developer

Build applications that interact with Lenny sessions through its APIs.

| Tutorial | Difficulty | Description |
|----------|------------|-------------|
| [Your First Session](first-session) | Beginner | Create, interact with, and tear down a session using the `lenny session` CLI, Python, and TypeScript |
| [MCP Client Integration](mcp-client-integration) | Intermediate | Connect an MCP host to Lenny's gateway using the Model Context Protocol |
| [OpenAI SDK Integration](openai-sdk-integration) | Intermediate | Use the OpenAI Python and TypeScript SDKs against Lenny's compatibility layer |
| [OAuth Token Exchange](oauth-token-exchange) | Intermediate | Use `POST /v1/oauth/token` (RFC 8693) to rotate admin tokens and exchange identity-provider tokens for Lenny access tokens |
| [Recursive Delegation](recursive-delegation) | Advanced | Build parent and child runtimes that delegate work through the gateway |

### Runtime Author

Build custom agent runtimes that run on the Lenny platform.

| Tutorial | Difficulty | Description |
|----------|------------|-------------|
| [Scaffold a Runtime with `lenny runtime init`](scaffold-a-runtime) | Beginner | Generate, build, and register a Minimum-tier runtime in Go, Python, or TypeScript |
| [Build a Runtime Adapter](build-a-runtime) | Intermediate | Create a calculator runtime using the stdin/stdout JSON Lines protocol |
| [Wrap a Coding-Agent CLI](wrap-coding-agent-cli) | Intermediate | Fork a reference runtime (`claude-code`, `gemini-cli`, `codex`, `cursor-cli`) to wrap your own CLI in a sandboxed workspace |
| [Runtime SDK Integration (Go / Python / TS)](runtime-sdk-integration) | Intermediate | Use the first-party Runtime Author SDKs to implement Standard tier (MCP tool access) and Full tier (lifecycle channel) |
| [Recursive Delegation](recursive-delegation) | Advanced | Build coordinator and worker runtimes with delegation, budgets, and scope narrowing |

### Operator

Deploy and operate Lenny in Kubernetes clusters.

| Tutorial | Difficulty | Description |
|----------|------------|-------------|
| [Install with the `lenny-ctl install` Wizard](installer-wizard) | Beginner | Run the interactive wizard against EKS, GKE, AKS, or k3s; save the answer file for replay in CI |
| [Deploy to Kubernetes](deploy-to-cluster) | Intermediate | Helm-based installation using answer files + tier overrides, with preflight checks and bootstrap |
| [Diagnose and Remediate with `doctor --fix`](doctor-fix) | Intermediate | Walk through the `lenny-ops` diagnostic endpoints and the auto-remediation guardrails for common misconfigurations |
| [Bundled Alerting and OpenSLO Export](alerting-and-openslo) | Intermediate | Wire Prometheus Operator CRDs, import the bundled alerting rules, and export OpenSLO v1 manifests to your SLO platform |
| [Multi-Tenant Setup](multi-tenant-setup) | Advanced | Configure tenant isolation with OIDC, RLS, per-tenant quotas, and metering |

## By Difficulty

### Beginner

- [`lenny up` Walkthrough](lenny-up-walkthrough) -- [Everyone]
- [Web Playground Tour](playground-tour) -- [Everyone]
- [Your First Session](first-session) -- [Client Developer]
- [Scaffold a Runtime with `lenny runtime init`](scaffold-a-runtime) -- [Runtime Author]
- [Install with the `lenny-ctl install` Wizard](installer-wizard) -- [Operator]

### Intermediate

- [Build a Runtime Adapter](build-a-runtime) -- [Runtime Author]
- [Wrap a Coding-Agent CLI](wrap-coding-agent-cli) -- [Runtime Author]
- [Runtime SDK Integration](runtime-sdk-integration) -- [Runtime Author]
- [Deploy to Kubernetes](deploy-to-cluster) -- [Operator]
- [Diagnose and Remediate with `doctor --fix`](doctor-fix) -- [Operator]
- [Bundled Alerting and OpenSLO Export](alerting-and-openslo) -- [Operator]
- [MCP Client Integration](mcp-client-integration) -- [Client Developer]
- [OpenAI SDK Integration](openai-sdk-integration) -- [Client Developer]
- [OAuth Token Exchange](oauth-token-exchange) -- [Client Developer]

### Advanced

- [Recursive Delegation](recursive-delegation) -- [Runtime Author, Client Developer]
- [Multi-Tenant Setup](multi-tenant-setup) -- [Operator]

## Prerequisites

All tutorials assume you have:

- **Lenny CLI** installed (primary prerequisite; `brew install lenny-dev/tap/lenny` or download from the releases page)
- **Go 1.22+**, **Python 3.10+**, and/or **Node.js 18+** (depending on the SDK tutorial you follow)
- **Docker** installed (for building runtime images and for Tier 2 local development)
- **Kubernetes cluster access** (for Operator tutorials beyond Tier 0)

Individual tutorials list additional prerequisites at the top.

## Conventions

Throughout these tutorials:

- `LENNY_GATEWAY` refers to the gateway base URL (default: `http://localhost:8080` for local dev)
- `$SESSION_ID` refers to a session ID returned by a previous API call
- `$TOKEN` refers to your authentication token
- Code blocks marked with `# Expected output` show what you should see
- Sections marked with **State transition** explain what happens inside Lenny when you make a call
