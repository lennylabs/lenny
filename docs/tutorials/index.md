---
layout: default
title: Tutorials
nav_order: 2
has_children: true
---

# Tutorials

Hands-on walkthroughs that take you from zero to a working Lenny deployment. Each tutorial is self-contained with complete code examples, expected outputs, and detailed explanations.

## By Persona

### Client Developer

Build applications that interact with Lenny sessions through its APIs.

| Tutorial | Difficulty | Description |
|----------|------------|-------------|
| [Your First Session](first-session) | Beginner | Create, interact with, and tear down a session using curl, Python, and TypeScript |
| [MCP Client Integration](mcp-client-integration) | Intermediate | Connect an MCP host to Lenny's gateway using the Model Context Protocol |
| [OpenAI SDK Integration](openai-sdk-integration) | Intermediate | Use the OpenAI Python and TypeScript SDKs against Lenny's compatibility layer |
| [Recursive Delegation](recursive-delegation) | Advanced | Build parent and child runtimes that delegate work through the gateway |

### Runtime Author

Build custom agent runtimes that run on the Lenny platform.

| Tutorial | Difficulty | Description |
|----------|------------|-------------|
| [Build a Runtime Adapter](build-a-runtime) | Intermediate | Create a calculator runtime using the stdin/stdout JSON Lines protocol |
| [Recursive Delegation](recursive-delegation) | Advanced | Build coordinator and worker runtimes with delegation, budgets, and scope narrowing |

### Operator

Deploy and operate Lenny in Kubernetes clusters.

| Tutorial | Difficulty | Description |
|----------|------------|-------------|
| [Deploy to Kubernetes](deploy-to-cluster) | Intermediate | Helm-based installation on a real cluster with preflight checks and bootstrap |
| [Multi-Tenant Setup](multi-tenant-setup) | Advanced | Configure tenant isolation with OIDC, RLS, per-tenant quotas, and metering |

## By Difficulty

### Beginner

- [Your First Session](first-session) -- [Client Developer]

### Intermediate

- [Build a Runtime Adapter](build-a-runtime) -- [Runtime Author]
- [Deploy to Kubernetes](deploy-to-cluster) -- [Operator]
- [MCP Client Integration](mcp-client-integration) -- [Client Developer]
- [OpenAI SDK Integration](openai-sdk-integration) -- [Client Developer]

### Advanced

- [Recursive Delegation](recursive-delegation) -- [Runtime Author, Client Developer]
- [Multi-Tenant Setup](multi-tenant-setup) -- [Operator]

## Prerequisites

All tutorials assume you have:

- **Go 1.22+** installed (for runtime author tutorials)
- **Docker** installed (for container-based workflows)
- **curl** installed (for API examples)
- **Python 3.10+** and/or **Node.js 18+** (for SDK examples)

Individual tutorials list additional prerequisites at the top.

## Conventions

Throughout these tutorials:

- `LENNY_GATEWAY` refers to the gateway base URL (default: `http://localhost:8080` for local dev)
- `$SESSION_ID` refers to a session ID returned by a previous API call
- `$TOKEN` refers to your authentication token
- Code blocks marked with `# Expected output` show what you should see
- Sections marked with **State transition** explain what happens inside Lenny when you make a call
