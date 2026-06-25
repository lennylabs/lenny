---
layout: default
title: "Local Development"
parent: "Runtime Author Guide"
nav_order: 4
---

# Local Development

To develop a runtime, use `lenny up`. It runs the full Lenny platform from a single binary on your machine, against the same Kubernetes code path production uses, with the reference runtimes pre-installed. Build your image, register it, and start a session.

On Linux, `lenny up` needs no external services: it downloads a pinned k3s and supervises it as a managed child process. On macOS and Windows, the embedded k3s needs a Linux kernel the binary cannot embed, so it runs the same pinned k3s version as a container under Docker Desktop's Linux VM. Docker Desktop is therefore a prerequisite on macOS and Windows; start it before you run `lenny up`. The gateway, controllers, CRDs, and storage interfaces are identical across operating systems, and only the substrate that hosts the embedded cluster differs by host.

Two other local modes exist mainly for contributors working on Lenny itself. A runtime author rarely needs them, but each gives a tighter loop in a specific case, so all three are documented here.

| Mode | What it runs | Use it to | Limitations |
|------|--------------|-----------|-------------|
| **`lenny up`** (recommended) | The whole platform from one binary: an embedded k3s running the gateway, the controllers, and the reference runtime catalog as pods, rendered from the production chart under a development profile. The data stores (Postgres, Redis, key management, and artifacts) are in-process in-memory backends inside the gateway pod. Your runtime runs in a real Kubernetes pod. On Linux the k3s runs as a managed child process; on macOS and Windows it runs as a container under Docker Desktop's Linux VM. | Develop and test your runtime against the real platform, and run end-to-end demos. | Local only, with insecure development credentials and keys. Not for production. Requires Docker Desktop on macOS and Windows. The single-node cluster does not reproduce the production isolation surface (see [Local isolation fidelity](#local-isolation-fidelity)). |
| **`make run`** | The Lenny source tree with Kubernetes stubbed out: the gateway, an in-process controller, and one agent, all as goroutines in a single process, backed by SQLite and in-memory stores. Your agent runs as a host process rather than in a pod. | Iterate on the gateway or controller source, or run a Basic-level runtime through a fast host-process loop. | No real pod lifecycle, scaling, or isolation. The host-side adapter connects Standard- and Full-level runtimes over Linux abstract Unix sockets, so under `make run` those levels work only on a Linux host. |
| **`docker compose up`** | The gateway, a controller, and one agent container, plus Postgres, Redis, and MinIO as containers. | Iterate on gateway or controller logic against real storage backends, or run integration tests in CI. | Plain HTTP by default; enable TLS before configuring real credentials. A single agent container, with no scaling. |

The difference between the first two is what runs underneath. `lenny up` is a released binary that starts a real single-node Kubernetes (k3s) and runs your runtime in an actual pod, so it exercises the production code path. `make run` runs the source tree with no Kubernetes at all and launches your agent as a plain host process, which rebuilds faster but does not reproduce pod scheduling, isolation, or scaling.

---

## `lenny up` -- the whole platform, one binary

```bash
lenny up
```

`lenny up` renders the production chart under a development profile and runs the gateway, the management plane, the controllers, and the reference runtime catalog as pods in an embedded Kubernetes (k3s). The application data stores (Postgres, Redis, key management, and artifact storage) are in-process in-memory backends inside the gateway pod, so there is no separate Postgres, Redis, KMS, or identity-provider process. The CLI authenticates by minting a development bearer from a persisted local key, and the in-cluster gateway trusts it in development mode. The k3s runs as a managed child process on Linux, where the first run downloads it to `~/.lenny/k3s/`; on macOS and Windows it runs as a container under Docker Desktop's Linux VM. The first run imports the platform and runtime images once and may take longer; every run after the first reuses the persisted substrate and image store and starts in seconds.

**What you need:** on Linux, the `lenny` binary and nothing else. On macOS and Windows, the `lenny` binary plus Docker Desktop, which supplies the Linux VM the embedded k3s runs in. Start Docker Desktop before `lenny up`.

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
lenny session new --runtime my-agent --message "Hello"
```

Or scaffold one from scratch:

```bash
lenny runtime init my-agent --language go --template coding
cd my-agent && make image && lenny runtime publish my-agent --image my-agent:dev
```

### Shutting it down

```bash
lenny down             # stop everything; keep the persisted substrate and image store
lenny down --purge     # also remove the persisted substrate and image store for a fresh start
```

The in-memory application stores are ephemeral and not preserved across `lenny down`. A non-`--purge` `lenny down` keeps the persisted substrate and the imported-image store, so the next `lenny up` reuses them; `--purge` removes them.

### Not for production

`lenny up` is for local development only. Its credentials, master keys, and identities are insecure. In development mode the gateway rejects any token whose audience claim is not `dev.local`, even when signed by the trusted development key, and it fails closed with `EMBEDDED_MODE_LOCAL_ONLY` if you try to expose it beyond localhost.

### Local isolation fidelity
{: #local-isolation-fidelity }

The embedded single-node cluster cannot reproduce the full production isolation surface, on any host. Two limits hold equally on the Linux child-process substrate and the Docker-backed substrate:

- **Isolation profiles degrade to runc.** In production the `sandboxed` profile wraps each pod in a gVisor sandbox and the `microvm` profile in a Kata virtual machine. Locally, the `sandboxed` profile runs under standard `runc` because the embedded cluster installs no gVisor runtime class, and the `microvm` profile runs under `runc` because it needs hardware virtualization the local substrate cannot nest. Each profile degrades for its own reason.
- **NetworkPolicy is not enforced.** The embedded k3s runs with NetworkPolicy enforcement disabled, so the default-deny egress boundary that isolates pods in production is not exercised locally.

Treat the local stack as a functional preview rather than the production isolation boundary. The runtime your agent sees is the same; the kernel- and network-level isolation around it is not.

---

## `make run` -- zero dependencies, for platform contributors

```bash
make run
```

Unlike `lenny up`, which runs the released binary against a real Kubernetes cluster, `make run` runs the Lenny source tree in a single process with Kubernetes left out entirely. Everything the gateway would normally talk to is replaced by an embedded equivalent:

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

- Iterating on the gateway or controller source while running a runtime against it
- A fast host-process loop on a Basic-level agent binary
- Getting oriented as a first-time Lenny contributor

### Using your own agent binary

```bash
make run LENNY_AGENT_BINARY=/path/to/my-agent-binary
```

The in-process controller spawns your binary directly. The binary must speak the stdin/stdout JSON Lines contract. There is no runtime registration step under `make run`; the binary is used as-is.

### The default runtime

Without `LENNY_AGENT_BINARY`, `make run` uses a built-in echo runtime. It replays deterministic responses, which is enough to exercise session lifecycle, workspace preparation, heartbeats, and shutdown without needing any LLM credentials.

### Smoke test

```bash
make test-smoke
```

It creates a session with the echo runtime, sends a prompt, checks the response, and exits. The run validates the pipeline from gateway to agent binary in under 10 seconds.

### Observability

Traces go to stdout; Prometheus metrics are exposed on `:9090/metrics`.

### Limitations

`make run` has no Kubernetes underneath, so it does not reproduce pod scheduling, isolation, scaling, or the warm pool. It runs your agent as a host process, which connects over Linux abstract Unix sockets for Standard- and Full-level features. Those sockets are a Linux kernel feature, so a host-side adapter has them only on Linux. On macOS or Windows use `make run` for Basic-level runtimes only. For Standard- and Full-level work on those hosts, use `lenny up`, which runs the adapter inside an in-cluster Linux pod (under Docker Desktop's Linux VM on macOS and Windows), so abstract sockets are available there. `docker compose up`, which runs the adapter inside a Linux container, also works.

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

With `docker compose up`, traffic between the gateway and agent containers goes over plain HTTP. **Turn on TLS before you configure live LLM credentials in this mode.**

### Credential testing

To test live LLM credentials or the mTLS code path:

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

The in-process controller starts with your updated binary right away. Session state does not survive a restart in this mode.

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
| Session hangs after your binary writes a response | stdout not flushed | Flush explicitly after every write (see your language's guidance in the [Adapter Contract](../reference/adapter-contract.md)) |
| Your binary gets SIGTERM after 10 seconds | Heartbeat wasn't acknowledged | Handle `heartbeat` by immediately writing `heartbeat_ack` |
| `tool_result` never arrives | `tool_call` referenced an invalid tool | Stick to `read_file`, `write_file`, `list_dir`, `delete_file` at the Basic level |
| MCP connection refused (Standard level) | You're on macOS with `make run`, where the host-side adapter has no Linux abstract Unix sockets | Use `lenny up` (the adapter runs in an in-cluster Linux pod) or `docker compose up` (the adapter runs in a Linux container) |
| MCP nonce rejected | You cached the manifest too early | Re-read `/run/lenny/adapter-manifest.json` at startup -- the nonce is regenerated per session |

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
2. **Loud warning.** When dev mode is on, the gateway logs a warning that TLS is disabled and the deployment is not for production, on startup and every 60 seconds.
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
