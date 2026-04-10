---
layout: default
title: "Publishing"
parent: "Runtime Author Guide"
nav_order: 9
---

# Publishing

This page covers how to package your runtime as a container image, register it with a Lenny deployment, distribute it as a Go module, and publish to the community registry.

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
| Use `scratch` or `distroless` as the base | Minimizes attack surface. No shell, no package manager. |
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

The `-u` flag disables Python's stdout buffering. This is critical --- without it, the adapter never receives your output and the session hangs.

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

Smaller images improve warm pool startup time and reduce image pull latency.

---

## Runtime Registration

Once your image is built and pushed to a container registry, register it with the Lenny deployment.

### Via Admin API

```bash
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{
    "name": "my-agent",
    "type": "agent",
    "image": "registry.example.com/my-agent:v1.0.0",
    "description": "A code review agent",
    "labels": {
      "team": "platform",
      "tier": "standard"
    },
    "capabilities": {
      "preConnect": false,
      "midSessionUpload": false
    },
    "supportedProviders": ["anthropic", "openai"],
    "delegationPolicyRef": "standard-policy"
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
      labels:
        team: platform
        tier: standard
      capabilities:
        preConnect: false
      supportedProviders: [anthropic, openai]
      delegationPolicyRef: standard-policy
  pools:
    - name: my-agent-pool
      runtimeRef: my-agent
      minSize: 1
      maxSize: 10
      resourceClass: standard
```

The bootstrap Job runs `lenny-ctl bootstrap` idempotently on every `helm install` and `helm upgrade`.

### Derived Runtimes

A derived runtime extends an existing runtime with additional configuration (workspace defaults, environment variables, delegation policy overrides) without modifying the base image:

```bash
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent-for-team-alpha",
    "type": "agent",
    "derivedFrom": "my-agent",
    "workspaceDefaults": {
      "files": [
        { "path": "CLAUDE.md", "inline": "You are a code reviewer for Team Alpha..." }
      ]
    },
    "delegationPolicyRef": "alpha-restricted-policy"
  }'
```

Derived runtimes use the same base image but get their own pool configuration, delegation policy (can only restrict the base), and workspace defaults.

---

## Pool Configuration

After registering your runtime, create a warm pool:

```bash
curl -X POST http://localhost:8080/v1/admin/pools \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-agent-pool",
    "runtimeRef": "my-agent",
    "minSize": 2,
    "maxSize": 20,
    "scalingPolicy": {
      "targetUtilization": 0.7,
      "scaleUpStabilizationSeconds": 30,
      "scaleDownStabilizationSeconds": 300
    },
    "resourceClass": "standard",
    "executionMode": "session"
  }'
```

**Key pool settings:**

| Setting | Description |
|---------|-------------|
| `minSize` | Minimum number of warm pods (always available) |
| `maxSize` | Maximum number of pods (scaling ceiling) |
| `resourceClass` | CPU/memory tier for pods |
| `executionMode` | `session` (one session per pod), `task` (sequential reuse), or `concurrent` |
| `runtimeClass` | Container isolation: `runc` (default), `gvisor`, or `kata` |

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
2. Update the runtime registration:

```bash
curl -X PUT http://localhost:8080/v1/admin/runtimes/my-agent \
  -H "Content-Type: application/json" \
  -d '{"image": "registry.example.com/my-agent:v1.1.0"}'
```

3. The warm pool controller drains old pods and provisions new ones with the updated image.

### Backward Compatibility

When upgrading your runtime:

- **Protocol changes:** The stdin/stdout JSON Lines protocol is stable. Adding new response fields is safe. Removing required fields is a breaking change.
- **New message types:** Your runtime must ignore unknown types (forward compatibility rule). New inbound message types may be added in future adapter versions.
- **MCP tool changes:** New tools may be added to the platform MCP server. Your runtime discovers tools via `tools/list` and should handle new tools gracefully.

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

// No Lenny SDK dependency required for Minimum tier.
// Standard tier: add MCP client library
// require github.com/mark3labs/mcp-go v0.x.x
```

### Installation

Users can build your runtime directly:

```bash
go install github.com/your-org/my-agent/cmd/my-agent@latest
```

Or use it in Tier 1 local dev:

```bash
make run LENNY_AGENT_BINARY=$(go env GOPATH)/bin/my-agent
```

---

## Community Registry

The Lenny community registry is a catalog of published runtimes. Publishing to the registry makes your runtime discoverable by other Lenny users.

### Registry Entry

A registry entry includes:

```yaml
name: my-agent
author: your-org
description: A code review agent that checks for security vulnerabilities
tier: standard
image: ghcr.io/your-org/my-agent:v1.0.0
source: https://github.com/your-org/my-agent
labels:
  category: code-review
  language: go
complianceReport:
  tier: standard
  passed: 25
  total: 25
  version: "1.0.0"
```

### Publishing Checklist

Before publishing:

1. **Pass the compliance suite** at your declared tier:

   ```bash
   lenny-compliance --binary ./my-agent --tier standard --json > compliance.json
   ```

2. **Write a clear description** of what your runtime does, what tier it implements, and what LLM providers it supports.

3. **Include a Dockerfile** in your repository.

4. **Publish the container image** to a public registry (ghcr.io, Docker Hub, etc.).

5. **Tag a release** with semantic versioning.

6. **Submit to the registry** via pull request to the community registry repository.

### Registry Validation

The registry CI pipeline:

1. Pulls your published image.
2. Runs the compliance suite at your declared tier.
3. Verifies the compliance report matches your submission.
4. Publishes the entry if all checks pass.

---

## Helm Chart Integration

For deployers who want to include your runtime in their Lenny installation, provide a Helm values snippet:

```yaml
# Add to your Lenny Helm values.yaml
bootstrap:
  runtimes:
    - name: my-agent
      type: agent
      image: ghcr.io/your-org/my-agent:v1.0.0
      description: A code review agent
      labels:
        category: code-review
      supportedProviders: [anthropic, openai]
  pools:
    - name: my-agent-pool
      runtimeRef: my-agent
      minSize: 1
      maxSize: 5
      resourceClass: standard
```

This allows deployers to add your runtime to their cluster with a single `helm upgrade`.
