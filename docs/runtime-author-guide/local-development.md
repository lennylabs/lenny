---
layout: default
title: "Local Development"
parent: "Runtime Author Guide"
nav_order: 4
---

# Local Development

There are three ways to run Lenny locally while you work on a runtime. Pick whichever matches what you're doing:

| Command | What it is | Best for |
|---------|------------|----------|
| **`lenny up`** | The whole platform running in a single binary. The default. | Testing your runtime against the real platform; end-to-end demos; anything you don't specifically need a different mode for. |
| **`make run`** | The gateway running as a single Go process with an in-memory backend and a lightweight controller that spawns one agent process. | Contributors iterating on the gateway or adapter source code; Basic-level runtimes on macOS. |
| **`docker compose up`** | Real Postgres, Redis, and artifact storage running in containers, plus Docker containers for the gateway and your agent. | Standard- and Full-level runtimes on macOS; exercising the real credential code path; integration CI. |

`lenny up` is the fastest path to seeing your runtime in context -- it runs the same code as production with nothing else to set up. `make run` and `docker compose up` exist mainly so contributors modifying core Lenny components get a tighter iteration loop.

---

## `lenny up` -- the whole platform, one binary

```bash
lenny up
```

Starts everything in-process: an embedded Kubernetes (k3s), Postgres, Redis, a development key-management shim, an identity provider, the gateway, the management plane, the controllers, and the full reference runtime catalog. The first run downloads k3s to `~/.lenny/k3s/`; every run after that starts in seconds.

**What you need:** the `lenny` binary. Nothing else.

**Use it for:**

- Trying Lenny out with your own workloads
- Testing a runtime against the same code path production uses
- End-to-end demos, including the web playground at `https://localhost:8443/playground`
- Exploring before you deploy to a cluster

### Using your own runtime

Build your image and register it against the running gateway:

```bash
docker build -t my-agent:dev .
lenny runtime publish my-agent --image my-agent:dev
lenny session start --runtime my-agent --message "Hello"
```

Or scaffold one from scratch:

```bash
lenny runtime init my-agent --language go --template coding
cd my-agent && make image && lenny runtime publish my-agent --image my-agent:dev
```

### Shutting it down

```bash
lenny down             # stop everything; keep state in ~/.lenny/
lenny down --purge     # also wipe ~/.lenny/ for a fresh start
```

### Not for production

Every `lenny up` prints a banner you can't suppress: "Local mode. NOT for production use. Credentials, master keys, and identities are insecure." The embedded identity provider refuses any audience claim that isn't `dev.local`, and any attempt to expose the gateway beyond localhost fails closed with `EMBEDDED_MODE_LOCAL_ONLY`.

---

## `make run` -- zero dependencies, for gateway contributors

```bash
make run
```

A single binary that embeds everything the gateway would normally talk to:

| Component | What it's replaced with |
|-----------|-------------------------|
| Postgres | Embedded SQLite |
| Redis | In-memory caches |
| Artifact storage | A local directory (`./lenny-data/`) |
| Kubernetes | A lightweight in-process controller that spawns one agent process |
| mTLS | Disabled -- plain HTTP |

**What starts:** the gateway, the in-process controller, and a single agent, all as goroutines inside one process.

**What you need:** the Go toolchain.

**Use it for:**

- Iterating on runtime code against the gateway's contract
- Getting oriented as a first-time Lenny contributor
- Quick demos
- Fast local iteration on an agent binary

### Using your own agent binary

```bash
make run LENNY_AGENT_BINARY=/path/to/my-agent-binary
```

The in-process controller spawns your binary directly. It has to speak the stdin/stdout JSON-lines contract. There's no runtime registration step -- the binary is used as-is.

### The default runtime

Without `LENNY_AGENT_BINARY`, `make run` uses a built-in echo runtime. It replays deterministic responses, which is enough to exercise session lifecycle, workspace preparation, heartbeats, and shutdown without needing any LLM credentials.

### Smoke test

```bash
make test-smoke
```

Creates a session with the echo runtime, sends a prompt, checks the response, and exits. Validates the whole pipeline end-to-end in under 10 seconds.

### Observability

Traces go to stdout; Prometheus metrics are exposed on `:9090/metrics`.

### macOS

`make run` works on macOS for **Basic-level runtimes** -- those that only use stdin/stdout. Standard and Full runtimes need Linux abstract Unix sockets, which macOS doesn't have, so use `docker compose up` for those.

---

## `docker compose up` -- a production-like local stack

```bash
docker compose up
```

A production-like local environment, with real storage dependencies:

| Component | What's running |
|-----------|----------------|
| Gateway | Single replica, no autoscaling |
| Controller | Lightweight controller managing one Docker container |
| Postgres | Small container |
| Redis | Small container |
| Artifact storage | Single container (MinIO) |
| Agent pod | Docker container with the sidecar and your agent binary |

**What you need:** Docker and Docker Compose.

**Use it for:**

- Iterating on gateway or controller logic with real storage backends
- Testing against production-like infrastructure
- Running integration tests in CI

### Using your own runtime

```bash
# 1. Build your runtime image
docker build -t my-agent:dev .

# 2. Start the stack (then register via the admin API once it's up)
docker compose up -d

# 3. Register the runtime
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{"name": "my-agent", "type": "agent", "image": "my-agent:dev"}'

# 4. Start a session with your runtime
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtimeName": "my-agent", "tenantId": "default"}'
```

Alternatively, add the runtime to the bootstrap seed file (`lenny-data/seed.yaml`) and restart. The controller picks up registered runtimes on the next warm cycle, and the seed file is applied idempotently on every `docker compose up`.

### Smoke test

```bash
docker compose run smoke-test
```

### Observability

Turn on the optional observability containers with the `observability` profile:

```bash
docker compose --profile observability up
```

That adds:

- **Prometheus** for metrics
- **Grafana** with a pre-built Lenny dashboard
- **Jaeger** for distributed tracing

Grafana is at `http://localhost:3000`, Jaeger at `http://localhost:16686`.

---

## TLS and credentials

### Plain HTTP by default

With `docker compose up`, traffic between the gateway and agent containers goes over plain HTTP. **Don't configure real LLM credentials in this mode -- turn on TLS first.**

### Credential testing

When you want to test real LLM credentials or the mTLS code path:

```bash
make compose-tls
# Equivalent to: docker compose --profile credentials up
```

That profile:

- Sets `LENNY_DEV_TLS=true`
- Generates self-signed mTLS certificates on the first run (in `./lenny-data/certs/`)
- Encrypts all gateway-to-agent traffic

### Trusting the self-signed CA

When `LENNY_DEV_TLS=true` is on, configure your clients to trust the self-signed CA:

**macOS:**
```bash
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain ./lenny-data/certs/ca.crt
```

**Linux:**
```bash
sudo cp ./lenny-data/certs/ca.crt /usr/local/share/ca-certificates/lenny-dev-ca.crt
sudo update-ca-certificates
```

**Per-process (any OS, recommended for CI):**
```bash
export SSL_CERT_FILE=./lenny-data/certs/ca.crt
# or
curl --cacert ./lenny-data/certs/ca.crt https://localhost:8443/healthz
```

Certificates are regenerated if deleted; no manual key management is required.

---

## Hot reload

### Basic-level runtimes (stdin/stdout only)

1. Edit your runtime.
2. Rebuild the binary.
3. Stop `make run` (Ctrl+C) and start it again:

```bash
make run LENNY_AGENT_BINARY=./my-agent
```

The in-process controller starts with your updated binary right away. Session state doesn't survive a restart -- this is development mode.

### Standard and Full runtimes (Docker)

1. Edit your runtime.
2. Rebuild the container image:

```bash
docker build -t my-agent:dev .
```

3. Restart just the agent container:

```bash
docker compose restart agent
```

The gateway and backing containers keep running. The controller notices the agent restarted and re-warms the pool.

For faster iteration, mount your binary as a volume:

```yaml
# In docker-compose.override.yml
services:
  agent:
    volumes:
      - ./build/my-agent:/usr/local/bin/my-agent
```

Then rebuild and copy the binary into `./build/` without rebuilding the image.

---

## Debugging

### Seeing what the sidecar sends your binary

**`make run`:** every message between the sidecar and your binary is logged to stdout with a `[adapter→binary]` prefix when `LENNY_LOG_LEVEL=debug` is set:

```bash
LENNY_LOG_LEVEL=debug make run LENNY_AGENT_BINARY=./my-agent
```

**`docker compose`:** read the sidecar's logs off the agent container:

```bash
docker compose logs -f agent
```

At DEBUG level the sidecar logs every line it sends to stdin and every line it reads from stdout.

### Gateway logs

**`make run`:** they go to the same stdout, prefixed with `[gateway]`.

**`docker compose`:**

```bash
docker compose logs -f gateway
```

### Reading the sidecar's manifest

The sidecar writes its manifest before your binary starts. With `docker compose`, you can read it inside the agent container:

```bash
docker compose exec agent cat /run/lenny/adapter-manifest.json | jq .
```

### Common issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| Session hangs after your binary writes a response | stdout not flushed | Flush explicitly after every write (see your language's guidance in the Adapter Contract) |
| Your binary gets SIGTERM after 10 seconds | Heartbeat wasn't acknowledged | Handle `heartbeat` by immediately writing `heartbeat_ack` |
| `tool_result` never arrives | `tool_call` referenced an invalid tool | Stick to `read_file`, `write_file`, `list_dir`, `delete_file` at the Basic level |
| MCP connection refused (Standard level) | You're on macOS with `make run` | Use `docker compose up` -- abstract Unix sockets only exist on Linux |
| MCP nonce rejected | You cached the manifest too early | Re-read `/run/lenny/adapter-manifest.json` at startup -- the nonce is regenerated per task |

---

## Health checks and quick commands

### Health check

```bash
curl http://localhost:8080/healthz
```

### Create a Session

```bash
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtimeName": "echo", "tenantId": "default"}'
```

### Send a Message

```bash
curl -X POST http://localhost:8080/v1/sessions/{session_id}/messages \
  -H "Content-Type: application/json" \
  -d '{"input": [{"type": "text", "inline": "Hello"}]}'
```

### List Runtimes

```bash
curl http://localhost:8080/v1/runtimes
```

### Admin API (docker compose)

```bash
# List pools
curl http://localhost:8080/v1/admin/pools

# Get pool status
curl http://localhost:8080/v1/admin/pools/echo-pool
```

---

## Environment variables

| Variable | Default | What it does |
|----------|---------|--------------|
| `LENNY_DEV_MODE` | `true` (set automatically) | Turns on dev-mode relaxations. Required if you want to run without TLS. |
| `LENNY_DEV_TLS` | `false` | Turns on self-signed mTLS certificates. Requires `LENNY_DEV_MODE=true`. |
| `LENNY_AGENT_BINARY` | (built-in echo) | Path to your agent binary. Applies only to `make run`. |
| `LENNY_AGENT_RUNTIME` | `echo` | Which runtime to use. Applies only to `docker compose`. |
| `LENNY_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error`. |
| `LENNY_PORT` | `8080` | Gateway HTTP port. |
| `LENNY_DATA_DIR` | `./lenny-data/` | Local directory for SQLite, artifacts, and certificates. |

### Dev-mode guardrails

Dev mode relaxes security defaults so you can iterate locally, but guardrails keep it from leaking into production:

1. **Hard startup assertion.** The gateway refuses to start with TLS off unless `LENNY_DEV_MODE=true` is set explicitly.
2. **Loud warning.** When dev mode is on, the gateway logs `"WARNING: TLS disabled -- dev mode active. Do not use in production."` on startup and every 60 seconds.
3. **One switch for everything.** `LENNY_DEV_MODE` gates every security relaxation. You can't disable individual security features one at a time.

---

## Working without LLM credentials

You can run either `make run` or `docker compose up` without any LLM credentials by using the built-in echo runtime. It's the default in `make run` and selectable in `docker compose` via `LENNY_AGENT_RUNTIME=echo`.

The echo runtime plays back deterministic responses, which is enough to test:

- Session lifecycle (create, attach, complete, terminate)
- Workspace preparation (file upload, finalization)
- Heartbeat and shutdown handling
- Response delivery

The echo runtime can't call MCP tools. If you're testing delegation, use the `delegation-echo` test runtime instead -- it runs scripted tool-call sequences that include `lenny/delegate_task`.
