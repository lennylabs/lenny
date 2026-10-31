---
layout: default
title: Quickstart
parent: Getting Started
nav_order: 1
---

# Quickstart

**Goal:** install Lenny on your laptop, run a session against an agent, and shut it down.

`lenny up` is a single binary that runs the whole platform on your machine. It renders the production chart under a development profile and runs the gateway, the management plane, and the controllers as pods in an embedded Kubernetes cluster (k3s), together with the credential-free `echo` runtime and catalog records for the reference runtimes. The application data stores are in-process in-memory backends inside the gateway pod, so there is no separate database, cache, key-management, or identity-provider process. In development mode the CLI mints a development bearer from a persisted local key, and the in-cluster gateway trusts it. On Linux it needs no external services and no prior Kubernetes setup. On macOS and Windows the embedded Kubernetes cluster runs inside Docker Desktop's Linux VM, so Docker Desktop is the one prerequisite to install first.

This is the recommended starting point for anyone evaluating Lenny. If you're going to contribute to the gateway or controllers themselves and want faster iteration, see [Local Development](../runtime-author-guide/local-development.html) for `make run` (native process) and `docker compose up` (containerized) alternatives.

---

## Prerequisites

| Requirement        | Version         | Notes                                                                                                                                 |
| ------------------ | --------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| **Lenny CLI**      | latest          | The release channel is the Homebrew formula `lennylabs/tap/lenny-ctl` (`brew install lennylabs/tap/lenny-ctl`), `go install github.com/lennylabs/lenny/cmd/lenny-ctl@latest`, or a binary from the [releases page](https://github.com/lennylabs/lenny/releases). `lenny` is the same binary under a short name. Lenny is at an early, pre-release stage and has no published release yet, so build the binary from source as described in [Local Development](../runtime-author-guide/local-development.html). |
| **macOS, Windows, or Linux** | anything recent | Windows also works through WSL2                                                                                              |
| **Docker Desktop** | latest          | Required on macOS and Windows only. The embedded Kubernetes cluster needs a Linux kernel that the binary cannot embed, and Docker Desktop's Linux VM supplies it. Not required on Linux. |

The first `lenny up` imports the platform and runtime images once and takes longer than later runs. Subsequent bring-ups reuse the persisted substrate and image store and complete in well under a minute. On macOS and Windows, start Docker Desktop before you run `lenny up`.

---

## Step 1: Start the embedded stack

```bash
lenny up
```

`lenny up` prints a non-suppressible warning banner and then reports progress as each component becomes ready. The following output is illustrative; the exact lines vary by version:

```
Embedded Mode. NOT for production use. Credentials, KMS master key,
and identities are insecure.

[✓] Kubernetes cluster ready
[✓] gateway listening on https://localhost:8443
[✓] management plane ready
[✓] playground available at https://localhost:8443/playground
```

The warning banner is deliberate: the embedded stack uses development credentials and is meant for development and evaluation, not production. It cannot be suppressed.

`lenny up` renders the production chart under a development profile and runs the same gateway, controller, and management-plane binaries a production cluster runs, as pods in an embedded k3s. The application data stores (Postgres and Redis) are in-process in-memory backends inside the gateway pod. The local KMS provider derives its keys, including the JWT signing key, from a master key Secret that `lenny up` generates in the embedded cluster when none exists, so the keys persist with the cluster. In place of a standalone identity provider, the CLI mints a development bearer from a persisted local key and the in-cluster gateway trusts it in development mode.

`lenny up` reports the gateway ready when it answers. The seeded runtime's warm pool fills shortly after, so the first session against a pool that is still warming can briefly return a pool-warming response.

---

## Step 2: Open a session

Start a session with the built-in `echo` runtime. `echo` is the conformance reference runtime: it echoes each message back with a sequence number and needs no LLM provider credentials. `lenny up` seeds it with a runnable image, a Runtime resource, and a warm pool, so it is the runtime that works out of the box.

```bash
lenny session new --runtime echo --attach "What is Lenny?"
```

With `--attach`, which is the default in an interactive terminal, the CLI opens a stream and renders the session's output and lifecycle transitions inline. The command prints the session ID, which the following steps use.

Behind the scenes, `lenny session` is a thin CLI over the same public API every other client uses -- the platform doesn't have a back door for the CLI.

---

## Step 3: Continue the conversation

Send a follow-up message:

```bash
lenny session send ses_01HN0J... "Send a second message."
```

Attach to the session's output stream from a second terminal:

```bash
lenny session attach ses_01HN0J...
```

Fetch the session's log entries:

```bash
lenny session logs ses_01HN0J...
```

---

## Step 4: Try a coding agent

The runtime catalog includes coding agents: `claude-code`, `gemini-cli`, `codex`, and `cursor-cli`. Each one runs its respective CLI in an isolated pod with a fresh workspace prepared from a directory you give it. In production these runtimes default to the `sandboxed` (gVisor) isolation profile; on the embedded stack that profile runs under standard `runc`, as noted under [What just happened](#what-just-happened).

`lenny up` registers the catalog runtimes as records with a placeholder image digest, no Runtime resource, and no warm pool, so a session against one fails until three artifacts exist for it:

1. A runnable image digest on the record. Re-register the record with a published digest, or import a local image with `lenny image import` and re-register the record under that image.
2. A Runtime resource for the runtime.
3. A warm pool for the runtime.

The coding agents and the `chat` runtime also call an LLM provider, so they need a credential pool for that provider.

Once those exist, point a coding agent at a local repository:

```bash
lenny session new --runtime claude-code --workspace ./my-repo --attach \
  "Read the README and summarize the architecture."
```

The gateway copies your repo into the pod's workspace, starts the agent, and streams its output back. Anything the agent writes into its output directory is downloadable as an artifact after the session ends.

---

## Step 5: Open the web playground

In a browser, visit [`https://localhost:8443/playground`](https://localhost:8443/playground). The playground is a small web UI that lets you pick a runtime, upload a workspace, and chat with a session -- useful when you want to demo something to a colleague without handing them a terminal. It uses the same public API the CLI and SDKs use.

`lenny up` enables the playground in development authentication mode, and the UI shows a persistent red "not for production" banner. In a cluster install, `playground.enabled` defaults to `false`, and the install wizard asks whether to enable it.

---

## Step 6: Inspect and tear down

List your sessions:

```bash
lenny session list
```

Check platform state:

```bash
lenny status                                # overall health and active sessions
lenny-ctl admin pools list                  # warm pool state
lenny-ctl diagnose connectivity             # end-to-end dependency check
```

When you're done:

```bash
lenny down          # stop everything; keep the persisted substrate and image store
lenny down --purge  # also remove the persisted substrate and image store for a fresh start
```

The in-memory application stores are ephemeral and not preserved across `lenny down`. A non-`--purge` `lenny down` keeps the persisted substrate and the imported-image store, so the next `lenny up` reuses them; `--purge` removes them.

---

## What just happened

You exercised the same flow every client uses in production:

```
lenny up             →  Kubernetes cluster, gateway, controllers, and
                         management plane come up; the warm pool
                         controller starts pre-warming pods.
session new          →  Gateway authenticates you, claims a warm pod,
                         copies your files in, starts the agent, and
                         opens a streaming connection.
session send         →  Your message and the agent's reply flow through
                         the gateway, which also routes tool calls and
                         any mid-session user prompts.
(idle)               →  Between messages the agent stays warm -- no
                         re-initialization cost for the next turn.
lenny down           →  Everything shuts down gracefully. The in-memory
                         application stores, including session history, are
                         lost; the persisted substrate and imported-image
                         store survive a non-`--purge` down, so the next
                         `lenny up` reuses them.
```

A few properties you saw in action, which apply equally to a production install:

- **The gateway is the only entrance.** Your pod never had an IP address you could reach.
- **Workspace files go through the gateway.** No shared disks, no mounted volumes.
- **The same platform runs any agent.** `echo`, the catalog runtimes such as `claude-code`, and whatever you build yourself all integrate the same way.
- **Pods are locked down by default.** Non-root, no extra Linux capabilities, and no network access except back to the gateway. In production, the `sandboxed` profile also wraps each pod in a gVisor sandbox and the `microvm` profile in a Kata virtual machine. The embedded single-node cluster cannot reproduce that isolation: the `sandboxed` profile runs under standard `runc` because the cluster installs no gVisor runtime class, and the `microvm` profile runs under `runc` because it needs hardware virtualization the local substrate cannot nest. The cluster also disables NetworkPolicy enforcement, so the default-deny network boundary is not exercised. Treat the local stack as a functional preview rather than the production isolation boundary.

---

## Next steps

Start with [Core Concepts](concepts.html) to learn the basics about Lenny.

### If you're building an agent

You ran a session against `echo`, the conformance reference runtime. To build your own:

1. Scaffold a new runtime: `lenny runtime init my-agent --language go --template coding`.
2. Read the [Runtime Author Guide](../runtime-author-guide/) for the integration levels and what each one gives you.
3. Use the Go (`github.com/lennylabs/runtime-sdk-go`), Python (`lenny-runtime`), or TypeScript (`@lennylabs/runtime-sdk`) SDK to skip the wire format.
4. Run `lenny runtime validate` before publishing to catch integration problems early.

### If you're deploying to a real cluster

You ran Lenny as a single binary. To move to a cluster:

1. Run `lenny-ctl install` against the target cluster -- an interactive wizard that inspects what your cluster can do, asks targeted questions, and produces a Helm values file you can commit.
2. Or hand-write values using the [examples under `spec/deployment-topology`](../reference/).
3. Read the [Operator Guide](../operator-guide/) for hardening, observability, the management plane, and `lenny-ctl doctor --fix`.

### If you're writing a client

You used the CLI. To build a real integration:

1. Read the [Client Guide](../client-guide/) for the API reference and protocol choices.
2. Pick a client SDK (Go or TypeScript) or use any MCP host.
3. Work through [delegation](../tutorials/recursive-delegation) if you're building multi-agent workflows.

### If you're contributing to Lenny itself

`lenny up` is enough for most development. When you're iterating on the gateway or controllers and don't want to rebuild the binary, use `make run` (native process) or `docker compose up` (containerized) -- both documented in [Local Development](../runtime-author-guide/local-development.html).
