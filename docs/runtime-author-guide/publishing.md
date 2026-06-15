---
layout: default
title: "Publishing"
parent: "Runtime Author Guide"
nav_order: 9
---

# Publishing

This page covers the path from a built runtime to one a Lenny deployment can run: packaging the runtime as a container image, registering it through the admin API, attaching a warm pool, and rolling out new versions. It also describes optional Go module distribution and the planned community registry.

In v1, runtime adapters are distributed through standard Go module hosting, container registries, and Helm chart repositories. A community runtime registry is planned as a post-v1 platform service. The [Community Registry](#community-registry-planned-post-v1) section describes the planned flow for authors who want to prepare submissions in advance.

Registration applies to both runtime types. A `type: agent` runtime runs a session lifecycle and can be a delegation target. A `type: mcp` runtime hosts an MCP server that Lenny isolates and scales, with no task lifecycle and no delegation. The registration fields differ between the two, and each section below notes where. For the conceptual difference, see [Integration Levels](integration-levels.md#type-mcp-runtimes).

---

## Container Packaging

Every Lenny runtime runs inside a Kubernetes pod alongside the adapter sidecar. Your runtime must be packaged as a container image.

### Dockerfile Best Practices

```dockerfile
# Multi-stage build for minimal image size
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o my-agent .

# Final image: scratch or distroless for minimal attack surface
FROM scratch
COPY --from=builder /build/my-agent /my-agent
ENTRYPOINT ["/my-agent"]
```

**Key guidelines:**

| Guideline | Reason |
|-----------|--------|
| Use multi-stage builds | Keeps the final image small (no build tools). |
| Use `scratch` or `distroless` as the base | Reduces attack surface. No shell, no package manager. |
| Build with `CGO_ENABLED=0` | Produces a static binary that runs on `scratch`. |
| Use `-ldflags="-s -w"` | Strips debug info, reducing binary size by ~30%. |
| Copy only the binary | No source code, no intermediate artifacts. |
| Set `ENTRYPOINT` to your binary | The adapter spawns your binary via the entrypoint. |

### Python Runtimes

```dockerfile
FROM python:3.12-slim AS builder
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir --target=/deps -r requirements.txt
COPY . .

FROM python:3.12-slim
COPY --from=builder /deps /usr/local/lib/python3.12/site-packages
COPY --from=builder /app /app
WORKDIR /app
ENTRYPOINT ["python", "-u", "main.py"]
```

The `-u` flag disables Python's stdout buffering. Without it, the adapter never receives your output and the session hangs.

### TypeScript Runtimes

```dockerfile
FROM node:20-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json tsconfig.json ./
RUN npm ci
COPY src/ ./src/
RUN npm run build

FROM node:20-alpine
WORKDIR /app
COPY --from=builder /app/dist ./dist
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/package.json .
ENTRYPOINT ["node", "dist/main.js"]
```

### Image Size Targets

| Language | Recommended Base | Expected Size |
|----------|-----------------|---------------|
| Go | `scratch` | 2--10 MB |
| Rust | `scratch` | 2--15 MB |
| Python | `python:3.12-slim` | 150--250 MB |
| TypeScript | `node:20-alpine` | 100--200 MB |
| Java | `eclipse-temurin:21-jre-alpine` | 200--300 MB |

Smaller images reduce warm pool startup time and image pull latency.

---

## Runtime Registration

Once your image is built and pushed to a container registry, register it with the Lenny deployment through the admin API. Runtime definitions are platform-global; tenant visibility is granted separately.

### Via Admin API (`type: agent`)

```bash
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{
    "name": "my-agent",
    "type": "agent",
    "image": "registry.example.com/my-agent:v1.0.0",
    "description": "A code review agent",
    "integrationLevel": "standard",
    "labels": {
      "team": "platform",
      "tier": "standard"
    },
    "capabilities": {
      "interaction": "multi_turn",
      "injection": { "supported": true },
      "preConnect": false,
      "midSessionUpload": false
    },
    "supportedProviders": ["anthropic", "openai"],
    "delegationPolicyRef": "standard-policy"
  }'
```

The `capabilities` block declares what the runtime supports. `interaction` is `one_shot` or `multi_turn`. `injection.supported` declares whether the runtime accepts mid-session message delivery, which a `multi_turn` runtime requires. `midSessionUpload` declares whether clients can add workspace files during an active session. `preConnect` controls SDK-warm pre-connection, described below.

**`preConnect` explained:** When `preConnect: true`, the adapter starts the runtime binary during the warm phase, before a session is assigned. This removes cold-start latency from the assignment path. The runtime must implement the `DemoteSDK` contract: the ability to tear down a pre-connected SDK session when an actual session is assigned and the workspace includes files matching `sdkWarmBlockingPaths` (default: `["CLAUDE.md", ".claude/*"]`). Demotion adds 1--3 seconds and ensures the agent starts with workspace files present. `preConnect` is admitted only when `sessionPolicy.maxConcurrentSessions` is 1, and the pool controller rejects it for service-mode pools.

### Via Admin API (`type: mcp`)

A `type: mcp` runtime hosts an MCP server that Lenny isolates, scales, and audits. The registration omits the `capabilities` block, `integrationLevel`, `delegationPolicyRef`, and `supportedProviders`, because an MCP server has no session lifecycle, integration level, or delegation policy. The gateway reads the server's `tools/list` at registration and infers tool capabilities from the MCP annotations.

```bash
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{
    "name": "my-mcp-server",
    "type": "mcp",
    "image": "registry.example.com/my-mcp-server:v1.0.0",
    "description": "A document search MCP server",
    "labels": {
      "team": "platform"
    }
  }'
```

### Via Bootstrap Seed File

For deployments using Helm, add your runtime to the bootstrap seed:

```yaml
# In values.yaml
bootstrap:
  enabled: true
  runtimes:
    - name: my-agent
      type: agent
      image: registry.example.com/my-agent:v1.0.0
      description: A code review agent
      integrationLevel: standard
      labels:
        team: platform
        tier: standard
      capabilities:
        interaction: multi_turn
        injection: { supported: true }
        preConnect: false
      supportedProviders: [anthropic, openai]
      delegationPolicyRef: standard-policy
  pools:
    - name: my-agent-pool
      runtime: my-agent
      minWarm: 1
      maxWarm: 10
      resourceClass: medium
```

The `lenny-bootstrap` Job runs `lenny-ctl bootstrap` against the admin API on every `helm install` and `helm upgrade`. It applies each seed resource with upsert semantics, so re-running it is idempotent.

### Derived Runtimes

A derived runtime customizes an existing runtime without shipping its own image. It declares a `baseRuntime` reference and omits `image`, reusing the base runtime's image. Creating one is a configuration change rather than a release.

```bash
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent-for-team-alpha",
    "type": "agent",
    "baseRuntime": "my-agent",
    "workspaceDefaults": {
      "files": [
        { "path": "CLAUDE.md", "content": "You are a code reviewer for Team Alpha..." }
      ]
    },
    "delegationPolicyRef": "alpha-restricted-policy"
  }'
```

A derived runtime can set its own workspace defaults, setup commands, labels, and pool configuration. Its `delegationPolicyRef` must be a subset of the base policy; the gateway rejects a derived policy that grants more than the base. Security-critical fields are inherited from the base and cannot be overridden, including `type`, `executionMode`, `isolationProfile`, `capabilities.interaction`, and `integrationLevel`. Setting any of these on a derived runtime is a registration error. For the inheritance rules, see [Derived runtimes](../getting-started/concepts.md#derived-runtimes).

---

## Pool Configuration

After registering your runtime, create a warm pool that holds pre-initialized pods ready for assignment:

```bash
curl -X POST http://localhost:8080/v1/admin/pools \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent-pool",
    "runtime": "my-agent",
    "minWarm": 2,
    "maxWarm": 20,
    "resourceClass": "medium",
    "isolationProfile": "sandboxed",
    "executionMode": "session"
  }'
```

**Key pool settings:**

| Setting | What it is | What it controls |
|---------|-----------|------------------|
| `minWarm` | Minimum warm pods kept ready | Floor for instant availability. A higher value absorbs bursts and controller failover at the cost of idle pods. Set `0` for scale-to-zero with cold-start latency on the next request. |
| `maxWarm` | Maximum warm pods | Ceiling the scaling controller will not exceed. Caps idle cost. |
| `resourceClass` | CPU and memory class for pods | One of the configured resource classes (for example `small`, `medium`, `large`), bounded by the runtime's `allowedResourceClasses`. |
| `isolationProfile` | Container isolation profile, selecting the Kubernetes `RuntimeClass` | `sandboxed` (gVisor, the default) intercepts syscalls in userspace to reduce kernel attack surface. `microvm` (Kata) runs each pod in a lightweight VM for stronger multi-tenant isolation at higher startup cost. `standard` (runc) has no kernel-level isolation and is dev-only, requiring explicit deployer opt-in via `allowStandardIsolation`. |
| `executionMode` | How sessions map to pods | Declared on the runtime and reflected on the pool. `session` binds each session to a claimed pod and is parameterized by `sessionPolicy`; in the default policy the pod is exclusive to one session and terminates when the session ends. `service` routes each message to any ready, tenant-pinned replica, with no pod claim and no workspace materialization. |
| `sessionPolicy` | Session-mode pod-occupancy policy | Applies only to `executionMode: session`. Sets `maxConcurrentSessions` (simultaneous sessions per pod), the `recycle` block (pod reuse across sequential sessions), `maxClientIdleSeconds`, and `onPoolExhausted` (`reject` or `queue`). See [Runtime Configuration](runtime-configuration.md#execution-mode). |

---

## Versioning

### Image Tags

Use semantic versioning for your container images:

```
registry.example.com/my-agent:v1.0.0
registry.example.com/my-agent:v1.0.1
registry.example.com/my-agent:v1.1.0
```

**Never use `latest` in production pool definitions.** Pools should reference specific image tags for reproducibility.

### Rolling Updates

To update your runtime to a new version:

1. Push the new image tag.
2. Update the runtime registration. The update requires the current `ETag` in an `If-Match` header for optimistic concurrency:

```bash
curl -X PUT http://localhost:8080/v1/admin/runtimes/my-agent \
  -H "Content-Type: application/json" \
  -H "If-Match: \"$RUNTIME_ETAG\"" \
  -d '{"image": "registry.example.com/my-agent:v1.1.0"}'
```

3. The warm pool controller drains old pods and provisions new ones with the updated image.

For a staged rollout with pause and rollback, the admin API also exposes a managed pool upgrade through the `POST /v1/admin/pools/{name}/upgrade/start` endpoint and its companion `proceed`, `pause`, `resume`, and `rollback` calls. See the [Admin API reference](../api/admin.md#pools) for the upgrade state machine.

### Compatibility across adapter versions

The runtime adapter contract follows a forward-compatibility rule: a runtime ignores inbound message types it does not recognize rather than aborting. New inbound message types and new platform MCP tools may appear in future adapter versions, and a runtime that drops them continues to work. Build to this rule so a runtime keeps running against a newer adapter:

- **Unknown message types.** Ignore an inbound `type` you do not recognize. Do not treat it as an error.
- **New platform tools.** Discover tools through `tools/list` and tolerate tools you do not call. New tools may be added to the platform tool server.
- **OutputPart fields.** Emitting additional fields in a structured `OutputPart` is safe for downstream consumers; the schema reserves a namespace for custom types. A consumer reads the fields it knows and forwards the rest.

The adapter contract is the stable interface for v1 distribution. See [Adapter Contract](../reference/adapter-contract.md) for the full message set.

---

## Go Module Distribution

If your runtime is written in Go, you can distribute it as a Go module:

### Module Structure

```
github.com/your-org/my-agent/
  cmd/
    my-agent/
      main.go        # Binary entrypoint
  internal/
    handler/
      handler.go     # Message handling logic
  go.mod
  go.sum
  Dockerfile
```

### go.mod

```
module github.com/your-org/my-agent

go 1.22

// No Lenny SDK dependency required for the Basic level.
// Standard level: add MCP client library
// require github.com/mark3labs/mcp-go v0.x.x
```

### Installation

Users can build your runtime directly:

```bash
go install github.com/your-org/my-agent/cmd/my-agent@latest
```

Or use it in local dev with `make run`:

```bash
make run LENNY_AGENT_BINARY=$(go env GOPATH)/bin/my-agent
```

---

## Community Registry (planned, post-v1)

The Lenny community registry is a planned catalog of published runtimes, where runtime authors publish versioned adapter packages for others to discover and install. It is a post-v1 platform service and is not available in v1. The schema and publishing flow below describe the target state so authors can prepare submissions in advance. In v1, distribute runtime adapters through Go modules, container registries, and Helm chart repositories.

### Registry Entry

A registry entry includes:

```yaml
name: my-agent
author: your-org
description: A code review agent that checks for security vulnerabilities
level: standard
image: ghcr.io/your-org/my-agent:v1.0.0
source: https://github.com/your-org/my-agent
labels:
  category: code-review
  language: go
complianceReport:
  level: standard
  status: pass
  version: "1.0.0"
```

### Publishing Checklist

Before publishing:

1. **Pass the conformance suite** at your declared integration level. `lenny runtime validate` reads the declared `integrationLevel` from your `runtime.yaml`, and `--report` writes a machine-readable result you can attach to a submission:

   ```bash
   lenny runtime validate --report compliance.json
   ```

   The suite validates every JSON Lines frame your runtime emits against the canonical schemas published at [schemas.lenny.dev/adapter/v1/](https://schemas.lenny.dev/adapter/v1/) -- `lenny-adapter-jsonl.schema.json` for stdin/stdout frames and `outputpart.schema.json` for structured content parts. Validation failures are reported as structured diffs. See [Testing](testing.md) for the full workflow and [Adapter Contract → Canonical artifacts](../reference/adapter-contract.md#canonical-artifacts) for the schema list.

2. **Write a clear description** of what your runtime does, what integration level it implements, and what LLM providers it supports.

3. **Include a Dockerfile** in your repository.

4. **Publish the container image** to a public registry (ghcr.io, Docker Hub, etc.).

5. **Tag a release** with semantic versioning.

6. **Submit to the registry** via pull request to the community registry repository.

### Registry Validation

The registry CI pipeline:

1. Pulls your published image.
2. Runs the compliance suite at your declared integration level.
3. Verifies the compliance report matches your submission.
4. Publishes the entry if all checks pass.

---

## Helm Chart Integration

For deployers who include your runtime in their Lenny installation, provide a Helm values snippet that they paste into the `bootstrap` section described in [Via Bootstrap Seed File](#via-bootstrap-seed-file):

```yaml
# Add to your Lenny Helm values.yaml
bootstrap:
  runtimes:
    - name: my-agent
      type: agent
      image: ghcr.io/your-org/my-agent:v1.0.0
      description: A code review agent
      integrationLevel: standard
      labels:
        category: code-review
      supportedProviders: [anthropic, openai]
  pools:
    - name: my-agent-pool
      runtime: my-agent
      minWarm: 1
      maxWarm: 5
      resourceClass: medium
```

This allows deployers to add your runtime to their cluster with a single `helm upgrade`.
