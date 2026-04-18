---
layout: default
title: Getting Started
nav_order: 1
has_children: true
---

# Getting Started with Lenny

Lenny runs interactive AI agent sessions in isolated sandboxes on your own Kubernetes cluster. Your clients talk to a single gateway; each session gets its own pod with a fresh workspace and leased credentials. The agent itself can be anything from Anthropic's Claude Code CLI to a LangGraph graph to a custom program -- a catalog of ready-to-use ones ships with every install, and the [Runtime Author Guide](../runtime-author-guide/) covers building your own.

---

## Start here

Three pages, in order. Together they take less than an hour and leave you with a working local install.

| Page | What you'll do | Time |
|------|----------------|------|
| [Quickstart](quickstart.html) | Install the CLI, start the embedded stack with `lenny up`, open a chat session, try a coding agent, and explore the web playground | ~5 minutes |
| [Core Concepts](concepts.html) | Learn the vocabulary: sessions, runtimes, pools, the gateway, delegation, workspaces, tenants, and credentials | ~20 minutes |
| [Architecture Overview](architecture.html) | Component diagram, request flow, where state lives, and the trust boundaries between pods, the gateway, and your clients | ~15 minutes |

---

## Choose your path

After the Quickstart, your next step depends on what you're trying to do.

### I'm building an agent to run on Lenny

You have an agent program -- maybe wrapping a CLI, maybe built on a framework, maybe your own code -- and you want it to run in a Lenny session.

1. Finish the **Quickstart** to see the reference runtimes in action.
2. Scaffold a new one to skip the boilerplate: `lenny runtime init my-agent --language go --template coding`.
3. Read **Core Concepts**, focusing on sessions, runtimes, and workspaces.
4. Skim **Architecture Overview** for how the gateway talks to your pod.
5. Continue to the [**Runtime Author Guide**](../runtime-author-guide/) for the message protocol, the three integration levels, and the Go / Python / TypeScript SDKs.

### I'm deploying Lenny to a Kubernetes cluster

You're the person who will install Lenny, size it, harden it, and keep it upgraded.

1. Run the **Quickstart** once. The embedded stack uses the same code as a real cluster install, so it's worth seeing the shape of things before you touch Helm.
2. Read **Core Concepts**, focusing on pools, the gateway, tenants, credentials, and the management plane.
3. Read **Architecture Overview** to understand the components you'll be operating.
4. Continue to the [**Operator Guide**](../operator-guide/) for the install wizard, configuration reference, `lenny-ctl doctor --fix`, capacity planning, and production hardening.

### I'm calling Lenny from an application

You're writing code -- an app, a script, a CI pipeline, an MCP host -- that creates sessions and interacts with them.

1. Run the **Quickstart** to see a session lifecycle end to end.
2. Read **Core Concepts**, focusing on sessions (especially the state machine) and delegation.
3. Skim **Architecture Overview** for the external API surface.
4. Point your browser at `https://localhost:8443/playground` while `lenny up` is running -- it's a useful way to poke at the API interactively.
5. Continue to the [**Client Guide**](../client-guide/) for the API reference, the official SDKs, and patterns like delegation and mid-session user prompts.

### I'm on call for a deployed Lenny

Alerts page you, and you need to know where to look and what to do.

1. Run the **Quickstart** so you have a sandbox to practice diagnostic commands against before you need them in production.
2. Read **Core Concepts**, focusing on pools, the gateway, session states, and the management plane.
3. Skim **Architecture Overview** for where failures propagate.
4. Read [**Agent Operability**](../operator-guide/agent-operability) for the diagnostic endpoints, the runbook catalog, drift detection, and backup and restore.
5. Walk through [**`lenny-ctl doctor --fix`**](../tutorials/doctor-fix) and [**Bundled Alerting and OpenSLO Export**](../tutorials/alerting-and-openslo) to wire the platform into your paging and SLO tooling.

### I'm reviewing Lenny for security or compliance

You need to know how sessions are isolated, how credentials are handled, what the audit trail looks like, and whether this can live inside a regulated environment.

1. Read **Architecture Overview**, focusing on the trust boundaries between clients, the gateway, and pods.
2. Read **Core Concepts**, focusing on credentials, tenants, and workspaces.
3. Read [**Security**](../operator-guide/security) for the three sandbox profiles (plain containers, gVisor, Kata microVMs), the credential leasing model, the default-deny network perimeter, and how LLM API keys are kept out of pods.
4. Read the Compliance section of the Operator Guide for GDPR-style erasure, the tamper-evident audit log, retention windows for SOC 2 / HIPAA / FedRAMP, legal holds, and data residency.

### I want to contribute to Lenny itself

You're changing gateway code, a controller, a storage backend, or the docs.

1. Run the **Quickstart** once to see the full system from the outside. Day-to-day, you'll probably prefer `make run` (native process) or `docker compose up` (containerized) for faster iteration on core components.
2. Read **Core Concepts** end to end.
3. Read **Architecture Overview**, then dive into the canonical spec under [`spec/`](https://github.com/lenny-dev/lenny/tree/main/spec), starting with `spec/README.md`.
4. Check `CONTRIBUTING.md` for the development workflow and code conventions.
