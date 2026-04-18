---
layout: default
title: "Local Development"
parent: "Runtime Author Guide"
nav_order: 4
---

# Local Development

Lenny provides three local development modes for runtime authors. Pick the one that matches your workflow:

| Tier | Command | What it runs | Best for |
|------|---------|--------------|----------|
| **Tier 0** | `lenny up` | Single binary; embedded k3s, Postgres, Redis, KMS shim, OIDC, gateway, `lenny-ops`, controllers, reference runtime catalog | **Default choice.** Testing a runtime against the real platform code path; end-to-end demos; wiring the full MCP surface |
| **Tier 1** | `make run` | Single Go process with SQLite, in-memory caches, controller-sim | Iterating on gateway or adapter source code; macOS-friendly Minimum-tier work |
| **Tier 2** | `docker compose up` | Real Postgres/Redis/MinIO + Docker containers for the gateway and your agent | Standard and Full tier runtimes on macOS, credential-mode testing, integration CI |

Tier 0 is the fastest way to see your runtime in context -- it exercises the production code path with zero external dependencies. Tiers 1 and 2 are faster *iteration loops* for contributors modifying core Lenny components.

---

## Tier 0: `lenny up` --- Full Platform, Single Binary

```bash
lenny up
```

Starts the complete platform in-process: embedded k3s, Postgres, Redis, a KMS shim, an OIDC provider, the gateway, `lenny-ops`, the controllers, and the full reference runtime catalog. The first run downloads k3s to `~/.lenny/k3s/`; subsequent runs start in seconds.

**What you need:** the `lenny` binary. Nothing else.

**Best for:**
- Evaluating Lenny against your own workloads
- Validating a runtime against the same code path used in production
- End-to-end demos (including the web playground at `https://localhost:8443/playground`)
- First-time exploration before cluster deployment

### Using Your Own Runtime

Build your runtime image and register it against the live Tier 0 gateway:

```bash
docker build -t my-agent:dev .
lenny runtime publish my-agent --image my-agent:dev
lenny session new --runtime=my-agent --attach "Hello"
```

Or scaffold a runtime from scratch:

```bash
lenny runtime init my-agent --language go --template coding
cd my-agent && make image && lenny runtime publish my-agent --image my-agent:dev
```

### Teardown

```bash
lenny down             # stop components, keep state in ~/.lenny/
lenny down --purge     # also remove ~/.lenny/ for a fresh start
```

### Production warning

Every `lenny up` prints a non-suppressible banner: `"Tier 0 embedded mode. NOT for production use. Credentials, KMS master key, and identities are insecure."` The embedded OIDC provider refuses any audience claim not matching `dev.local`, and any attempt to expose the gateway outside localhost fails closed with `EMBEDDED_MODE_LOCAL_ONLY`.

---

## Tier 1: `make run` --- Zero-Dependency Local Mode

```bash
make run
```

A single binary entry point that embeds all required state:

| Component | Replacement |
|-----------|-------------|
| Postgres | Embedded SQLite |
| Redis | In-memory caches |
| MinIO | Local filesystem directory (`./lenny-data/`) |
| Kubernetes | Controller-sim (manages a single agent process) |
| mTLS | Disabled (plain HTTP) |

**What starts:** Gateway + controller-sim + a single agent container, all running as goroutines in one process.

**What you need:** Go toolchain. Nothing else.

**Best for:**
- Runtime adapter authors testing their binary against the gateway contract
- First-time contributors getting oriented
- Quick demos and evaluations
- Agent binary authors iterating locally

### Using Your Own Runtime Binary

```bash
make run LENNY_AGENT_BINARY=/path/to/my-agent-binary
```

The controller-sim spawns the specified binary as a single agent container. The binary must implement the stdin/stdout JSON Lines protocol. No runtime registration is required in Tier 1 --- the binary is used directly.

### Default Runtime

Without `LENNY_AGENT_BINARY`, `make run` uses the built-in echo runtime. This is a deterministic runtime that echoes messages back, allowing you to test platform mechanics (session lifecycle, workspace materialization) without providing any API keys or LLM credentials.

### Smoke Test

```bash
make test-smoke
```

Creates a session with the echo runtime, sends a prompt, verifies a response, and exits. Validates the entire pipeline (gateway, controller-sim, runtime adapter, agent binary) in under 10 seconds.

### Observability

Tier 1 outputs traces to stdout and exposes Prometheus metrics on `:9090/metrics`.

### macOS Support

`make run` supports macOS for **Minimum-tier runtimes only** (stdin/stdout binary protocol). Standard and Full tier runtimes require abstract Unix sockets (`@` prefix), which are a Linux-only feature. If you are developing a Standard or Full tier runtime on macOS, use Tier 2 (`docker compose up`) instead.

---

## Tier 2: `docker compose up` --- Full Local Stack

```bash
docker compose up
```

Production-like local environment with real infrastructure dependencies:

| Component | What Runs |
|-----------|-----------|
| Gateway | Single replica, no HPA |
| Controller | Controller-sim managing a single Docker container |
| Postgres | Lightweight container |
| Redis | Lightweight container |
| MinIO | Single container for artifact storage |
| Agent pod | Docker container with runtime adapter + agent binary |

**What you need:** Docker and Docker Compose.

**Best for:**
- Lenny core developers iterating on gateway/controller logic
- Integration testing against real storage backends
- CI integration tests
- Production-like local environment validation

### Using Your Own Runtime

```bash
# 1. Build your runtime image
docker build -t my-agent:dev .

# 2. Start the stack (after it is up, register via admin API)
docker compose up -d

# 3. Register the runtime
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{"name": "my-agent", "type": "agent", "image": "my-agent:dev"}'

# 4. Start a session using your runtime
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtimeName": "my-agent", "tenantId": "default"}'
```

Alternatively, add your runtime to the bootstrap seed file (`lenny-data/seed.yaml`) and restart. The controller-sim picks up registered runtimes on the next pool warm cycle. The seed file is applied idempotently on every `docker compose up`.

### Smoke Test

```bash
docker compose run smoke-test
```

### Observability

Enable optional observability containers with the `observability` profile:

```bash
docker compose --profile observability up
```

This adds:
- **Prometheus** --- metrics scraping
- **Grafana** --- pre-built Lenny dashboard
- **Jaeger** --- distributed tracing

Access Grafana at `http://localhost:3000` and Jaeger at `http://localhost:16686`.

---

## TLS and Credentials

### Plain HTTP (Default)

By default, Tier 2 transmits all traffic over plain HTTP between the gateway and agent containers. **Do not configure real LLM credentials in docker-compose unless TLS is enabled.**

### Credential-Testing Profile

When testing with real LLM provider credentials or exercising the mTLS code path:

```bash
make compose-tls
# Equivalent to: docker compose --profile credentials up
```

This profile:
- Sets `LENNY_DEV_TLS=true` automatically
- Generates self-signed mTLS certificates on first run (stored in `./lenny-data/certs/`)
- Encrypts all gateway-to-agent traffic

### Self-Signed Certificate Trust Setup

When `LENNY_DEV_TLS=true` is active, configure your API clients to trust the self-signed CA:

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

## Hot Reload Workflow

### Minimum Tier (stdin/stdout only)

1. Edit your runtime source code.
2. Rebuild your binary.
3. Stop and restart `make run`:

```bash
# Terminal 1: stop the running instance (Ctrl+C), then:
make run LENNY_AGENT_BINARY=./my-agent
```

The controller-sim restarts with your updated binary immediately. Session state is not preserved across restarts (this is development mode).

### Standard/Full Tier (Docker)

1. Edit your runtime source code.
2. Rebuild your container image:

```bash
docker build -t my-agent:dev .
```

3. Restart only the agent container:

```bash
docker compose restart agent
```

The gateway and infrastructure containers continue running. The controller-sim detects the agent restart and re-warms the pool.

For faster iteration, mount your binary as a volume:

```yaml
# In docker-compose.override.yml
services:
  agent:
    volumes:
      - ./build/my-agent:/usr/local/bin/my-agent
```

Then rebuild and copy the binary into `./build/` without rebuilding the Docker image.

---

## Debugging

### Inspecting Adapter-to-Binary Messages

**Tier 1:** All adapter-to-binary messages are logged to stdout with a `[adapter→binary]` prefix when `LENNY_LOG_LEVEL=debug` is set:

```bash
LENNY_LOG_LEVEL=debug make run LENNY_AGENT_BINARY=./my-agent
```

**Tier 2:** View adapter logs for the agent container:

```bash
docker compose logs -f agent
```

The adapter logs every message sent to stdin and received from stdout at `DEBUG` level.

### Inspecting Gateway Logs

**Tier 1:** Gateway logs appear on the same stdout (prefixed with `[gateway]`).

**Tier 2:**

```bash
docker compose logs -f gateway
```

### Inspecting the Adapter Manifest

The adapter manifest is written before your binary starts. In Tier 2, you can inspect it inside the agent container:

```bash
docker compose exec agent cat /run/lenny/adapter-manifest.json | jq .
```

### Common Issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| Session hangs after your binary writes a response | stdout not flushed | Add explicit flush after every `write` (see language-specific guidance in the Adapter Contract) |
| Binary receives SIGTERM after 10s | Heartbeat not acknowledged | Add a `heartbeat` handler that immediately writes `heartbeat_ack` |
| `tool_result` never arrives | `tool_call` has an invalid tool name | Use only `read_file`, `write_file`, `list_dir`, `delete_file` at Minimum tier |
| MCP connection refused (Standard tier) | Running on macOS with `make run` | Use `docker compose up` instead --- abstract Unix sockets require Linux |
| MCP nonce rejected | Stale manifest read | Re-read `/run/lenny/adapter-manifest.json` at startup (nonce is regenerated per task) |

---

## Testing Tools and Endpoints

### Health Check

```bash
# Tier 1
curl http://localhost:8080/healthz

# Tier 2
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

### Admin API (Tier 2)

```bash
# List pools
curl http://localhost:8080/v1/admin/pools

# Get pool status
curl http://localhost:8080/v1/admin/pools/echo-pool
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LENNY_DEV_MODE` | `true` (set automatically) | Enables dev mode security relaxations. Required for TLS bypass. |
| `LENNY_DEV_TLS` | `false` | Enables self-signed mTLS certificates. Requires `LENNY_DEV_MODE=true`. |
| `LENNY_AGENT_BINARY` | (built-in echo) | Path to your agent binary (Tier 1 only). |
| `LENNY_AGENT_RUNTIME` | `echo` | Runtime name to use (Tier 2 only). |
| `LENNY_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error`. |
| `LENNY_PORT` | `8080` | Gateway HTTP listen port. |
| `LENNY_DATA_DIR` | `./lenny-data/` | Local data directory for SQLite, artifacts, and certificates. |

### Dev Mode Guard Rails

Dev mode relaxes security defaults for local convenience, but hard guard rails prevent accidental use outside development:

1. **Hard startup assertion:** The gateway refuses to start with TLS disabled unless `LENNY_DEV_MODE=true` is explicitly set.
2. **Prominent warning:** When dev mode is active, the gateway logs a warning on startup and every 60 seconds: `"WARNING: TLS disabled --- dev mode active. Do not use in production."`
3. **Unified gate:** `LENNY_DEV_MODE` is the single gate for all security relaxations. No individual security feature can be disabled independently.

---

## Zero-Credential Mode

Both tiers can operate without LLM provider credentials by using the built-in echo/mock runtime. This is the default in Tier 1 and selectable in Tier 2 via `LENNY_AGENT_RUNTIME=echo`.

The echo runtime replays deterministic responses, allowing you to test:
- Session lifecycle (create, attach, complete, terminate)
- Workspace materialization (file upload, finalization)
- Heartbeat and shutdown handling
- Response delivery

The echo runtime cannot invoke MCP tools. Delegation flow testing requires the `delegation-echo` test runtime, which executes scripted tool call sequences including `lenny/delegate_task`.
