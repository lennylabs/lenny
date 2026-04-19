---
layout: default
title: Tutorials
nav_order: 2
has_children: true
description: Self-contained walkthroughs — each with runnable code, expected output, and an explanation of what happens inside Lenny. Grouped by role and by difficulty.
---

# Tutorials

Self-contained walkthroughs. Each includes runnable code, expected output, and an explanation of what happens inside Lenny.

## By role

### Everyone starts here

| Tutorial | Difficulty | What you'll do |
|----------|------------|----------------|
| [`lenny up` Walkthrough](lenny-up-walkthrough) | Beginner | Install the CLI, run the platform on your laptop, talk to `chat` and `claude-code`, and explore the web playground. Approximately five minutes. |
| [Web Playground Tour](playground-tour) | Beginner | Pick a runtime, upload a workspace, and drive a session without writing code |

### If you're building a client

Driving sessions from your own application, script, or MCP host.

| Tutorial | Difficulty | What you'll do |
|----------|------------|----------------|
| [Your First Session](first-session) | Beginner | Create, interact with, and tear down a session using the CLI, Python, and TypeScript |
| [User Credentials](user-credentials) | Beginner | Attach per-user credentials to a session and scope them safely |
| [MCP Client Integration](mcp-client-integration) | Intermediate | Plug an MCP host into Lenny's gateway |
| [OpenAI SDK Integration](openai-sdk-integration) | Intermediate | Point the OpenAI Python and TypeScript SDKs at Lenny |
| [OAuth Token Exchange](oauth-token-exchange) | Intermediate | Use `POST /v1/oauth/token` to rotate admin tokens and exchange identity-provider tokens for Lenny access tokens |
| [Using Connectors](using-connectors) | Intermediate | Register and use connectors (GitHub, Jira, Slack, etc.) for gateway-managed OAuth |
| [Session Derive and Replay](session-derive-replay) | Intermediate | Fork a completed session's workspace, or replay its prompt history against a new runtime version |
| [Agent Memory](agent-memory) | Intermediate | Persist per-user memory across sessions with the agent-memory service |
| [Evaluation and Scoring](evaluation-scoring) | Intermediate | Score runs, run A/B replays, and export evaluation datasets |
| [Recursive Delegation](recursive-delegation) | Advanced | Build parent and child sessions that delegate work through the gateway |

### If you're writing a runtime

Building your own agent to run on Lenny.

| Tutorial | Difficulty | What you'll do |
|----------|------------|----------------|
| [Scaffold a Runtime with `lenny runtime init`](scaffold-a-runtime) | Beginner | Generate, build, and register a Basic-level runtime in Go, Python, or TypeScript |
| [Build a Runtime Adapter](build-a-runtime) | Intermediate | Write a calculator runtime against the stdin/stdout JSON-lines contract |
| [Wrap a Coding-Agent CLI](wrap-coding-agent-cli) | Intermediate | Fork one of the reference runtimes (`claude-code`, `gemini-cli`, `codex`, `cursor-cli`) to wrap your own CLI in a sandboxed workspace |
| [Runtime SDK Integration (Go / Python / TS)](runtime-sdk-integration) | Intermediate | Use the official SDKs to build up to the Standard level (platform tool access) and the Full level (lifecycle signals) |
| [Recursive Delegation](recursive-delegation) | Advanced | Build coordinator and worker runtimes with delegation, budgets, and scope narrowing |

### If you're operating a deployment

Running Lenny on a Kubernetes cluster.

| Tutorial | Difficulty | What you'll do |
|----------|------------|----------------|
| [Install with the `lenny-ctl install` Wizard](installer-wizard) | Beginner | Run the interactive wizard against EKS, GKE, AKS, or k3s, and capture an answer file you can replay in CI |
| [Deploy to Kubernetes](deploy-to-cluster) | Intermediate | Helm-based installation from an answer file, with preflight checks and bootstrap |
| [Diagnose and Remediate with `doctor --fix`](doctor-fix) | Intermediate | Use the management plane's diagnostic endpoints and walk through the auto-remediation guardrails |
| [Bundled Alerting and OpenSLO Export](alerting-and-openslo) | Intermediate | Wire up the Prometheus Operator custom resources, import the bundled alerting rules, and export OpenSLO v1 manifests |
| [Multi-Tenant Setup](multi-tenant-setup) | Advanced | Set up tenant isolation with identity-provider integration, row-level security, per-tenant quotas, and metering |

## By difficulty

### Beginner

- [`lenny up` Walkthrough](lenny-up-walkthrough): everyone
- [Web Playground Tour](playground-tour): everyone
- [Your First Session](first-session): client developers
- [User Credentials](user-credentials): client developers
- [Scaffold a Runtime with `lenny runtime init`](scaffold-a-runtime): runtime authors
- [Install with the `lenny-ctl install` Wizard](installer-wizard): operators

### Intermediate

- [Build a Runtime Adapter](build-a-runtime): runtime authors
- [Wrap a Coding-Agent CLI](wrap-coding-agent-cli): runtime authors
- [Runtime SDK Integration](runtime-sdk-integration): runtime authors
- [Deploy to Kubernetes](deploy-to-cluster): operators
- [Diagnose and Remediate with `doctor --fix`](doctor-fix): operators
- [Bundled Alerting and OpenSLO Export](alerting-and-openslo): operators
- [MCP Client Integration](mcp-client-integration): client developers
- [OpenAI SDK Integration](openai-sdk-integration): client developers
- [OAuth Token Exchange](oauth-token-exchange): client developers
- [Using Connectors](using-connectors): client developers
- [Session Derive and Replay](session-derive-replay): client developers
- [Agent Memory](agent-memory): runtime authors and client developers
- [Evaluation and Scoring](evaluation-scoring): client developers and operators

### Advanced

- [Recursive Delegation](recursive-delegation): runtime authors and client developers
- [Multi-Tenant Setup](multi-tenant-setup): operators

## Before you start

- Every tutorial needs the **Lenny CLI**: `brew install lennylabs/tap/lenny` or grab a binary from the releases page.
- Some SDK tutorials also need **Go 1.22+**, **Python 3.10+**, or **Node.js 18+**.
- **Docker** is required for building runtime images.
- **Kubernetes cluster access** is required for the operator tutorials beyond `lenny up`.

Each tutorial lists anything extra it needs at the top.

## Conventions

Throughout the tutorials:

- `LENNY_GATEWAY` is your gateway's base URL. With `lenny up`, that's `https://localhost:8443`.
- `$SESSION_ID` is a session ID returned by a previous call.
- `$TOKEN` is your authentication token.
- Code blocks marked `# Expected output` show what you should see.
- Sections marked **State transition** explain what's happening inside Lenny when you make a call.
