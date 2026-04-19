---
layout: default
title: lenny-ctl Reference
parent: "Operator Guide"
nav_order: 11
---

# lenny-ctl Reference

`lenny-ctl` is the official CLI for Lenny platform operators. It is a thin client over the Admin API with near-zero business logic -- every operation maps to an Admin API call.

## Most common commands

If you're already familiar with `lenny-ctl`, this is what you'll reach for most:

**Daily operations**

| Task | Command |
|:-----|:--------|
| Check platform health end-to-end | `lenny-ctl doctor` |
| Auto-remediate common misconfigurations | `lenny-ctl doctor --fix` |
| Install on a new cluster (wizard) | `lenny-ctl install` |
| Replay a captured install on another cluster | `lenny-ctl install --answers <file>` |
| Upgrade using a captured answer file | `lenny-ctl upgrade --answers <file>` |
| List warm pools and their state | `lenny-ctl admin pools list` |
| Drain a pool for maintenance | `lenny-ctl admin pools drain <pool>` |
| Run a specific diagnostic | `lenny-ctl diagnose <check>` |
| List available runbooks | `lenny-ctl runbooks list` |
| Run a runbook (machine-readable) | `lenny-ctl runbooks run <id> --output json` |
| Tail recent audit events for a session | `lenny-ctl audit events --session <id>` |
| Compare deployed state against declared | `lenny-ctl drift diff` |
| Take an on-demand backup | `lenny-ctl backup create` |
| Restore from a backup | `lenny-ctl restore --from <snapshot>` |
| Start the embedded local stack | `lenny up` |
| Stop the embedded local stack | `lenny down` |
| Open a session | `lenny session new --runtime <name>` |

**Emergency / incident response**

| Task | Command |
|:-----|:--------|
| Revoke a compromised credential | `lenny-ctl admin credential-pools revoke-credential --pool <p> --credential <id> --reason "<why>"` |
| Force-terminate a runaway session | `lenny-ctl admin sessions terminate <session-id> --reason "<why>"` |
| Open a circuit breaker for a failing dependency | `lenny-ctl admin circuit-breakers open <subsystem>` |
| Close a circuit breaker after recovery | `lenny-ctl admin circuit-breakers close <subsystem>` |
| Reconcile orphaned sandboxes | `lenny-ctl admin sandboxes reconcile` |
| Raise a human escalation with pre-filled context | `lenny-ctl escalations create --alert <name> --step <n>` |
| Acquire a remediation lock to coordinate concurrent fixes | `lenny-ctl locks acquire <resource>` |
| Verify audit chain integrity | `lenny-ctl audit verify --since <ts>` |

Every command on this page accepts `--output json` for machine-readable output and supports the [global flags](#global-flags).

---

## Installation

The CLI ships in two interchangeable forms with identical flags, arguments, and output:

- **Standalone binary** (`lenny-ctl`) -- Homebrew, `go install`, or direct download.
- **kubectl plugin** (`kubectl-lenny`) -- installed via krew: `kubectl krew install lenny`. Invocation: `kubectl lenny <subcommand>`.

See [krew installation](krew-install.md) for kubectl-plugin details. Everything in this reference works identically under both forms; `kubectl lenny admin pools list` and `lenny-ctl admin pools list` are equivalent.

---

## Global Flags

| Flag | Environment Variable | Description |
|---|---|---|
| `--api-url` | `LENNY_API_URL` | Gateway API endpoint URL |
| `--ops-server` | `LENNY_OPS_URL` | `lenny-ops` Ingress URL. Optional — auto-discovered from the gateway's `/v1/admin/platform/version` response |
| `--token` | `LENNY_API_TOKEN` | Admin authentication token |
| `--timeout` | -- | Request timeout (default: 30s) |
| `--output json` | -- | Machine-readable JSON output |
| `--quiet` | -- | Suppress informational messages |
| `--insecure-skip-verify` | -- | Skip TLS verification (dev only) |

### Server discovery and routing

`lenny-ctl` talks to two services:

- The **gateway** for every `admin`-prefixed command (all of Sections 24.1-24.14 below). Target: `--api-url` / `LENNY_API_URL`.
- **`lenny-ops`** for the Section 25 operability commands (`me`, `operations`, `events`, `diagnose`, `runbooks`, `upgrade`, `audit`, `drift`, `backup`, `restore`, `locks`, `escalations`, `logs`, `mcp-management`). Target: `--ops-server` / `LENNY_OPS_URL`, or auto-discovered from the gateway.

Discovery rules:

1. If `--ops-server` (or `LENNY_OPS_URL`) is set, `lenny-ctl` uses it for every ops-hosted call.
2. Otherwise, `lenny-ctl` calls `GET /v1/admin/platform/version` on the gateway; the response includes an `opsServiceURL` field, which is cached for the invocation.
3. If auto-discovery fails (gateway unreachable, `opsServiceURL` absent because the cluster is mid-upgrade), `lenny-ctl` falls back to the gateway host under the assumption that gateway-hosted operability endpoints still work, and prints a warning for any ops-exclusive command.

Operators typically only need `--api-url` — `--ops-server` is for air-gapped clusters, split-DNS configurations, or direct-to-ops debugging.

---

## Bootstrap

Seed Day-1 configuration into an empty deployment.

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl bootstrap --from-values <file>` | Apply seed configuration using upsert semantics | `platform-admin` |
| `lenny-ctl bootstrap --dry-run --from-values <file>` | Validate seed file without making changes | `platform-admin` |

### Upsert Semantics

| Condition | Default | With `--force-update` |
|---|---|---|
| Resource does not exist | Create | Create |
| Resource exists, identical | No-op | No-op |
| Resource exists, differs | Skip (logged as WARN) | Update (PUT with overwrite) |
| Security-critical field change | Error (blocked) | Error (blocked) |

### Exit Codes

| Code | Meaning |
|---|---|
| 0 | Success -- all resources seeded |
| 1 | Validation error |
| 2 | Partial failure -- some resources failed |

---

## Preflight

Validate infrastructure prerequisites before or after installation.

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl preflight --config <values.yaml>` | Run all preflight checks | `platform-admin` (API) / none (standalone) |

### Execution Modes

**Standalone mode** (default when gateway is unreachable):
- Embeds full preflight check logic locally
- Probes infrastructure directly from the caller's machine
- No running Lenny deployment required
- Primary use case: pre-deployment validation in CI pipelines

**API-backed mode** (when `--api-url` is set and gateway is reachable):
- Delegates to `POST /v1/admin/preflight` on the gateway
- Runs the same checks server-side

### Credential Precedence

1. CLI flags (`--postgres-dsn`, `--redis-dsn`, `--minio-endpoint`) -- highest
2. Environment variables (`LENNY_POSTGRES_DSN`, `LENNY_REDIS_DSN`, `LENNY_MINIO_ENDPOINT`)
3. Values file fields (`postgres.connectionString`, etc.) -- lowest

```bash
# CI pipeline example
lenny-ctl preflight --config values.yaml \
  --postgres-dsn "$LENNY_PG_DSN" \
  --redis-dsn "$LENNY_REDIS_DSN" \
  --minio-endpoint "$LENNY_MINIO_ENDPOINT"
```

---

## Runtime Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin runtimes grant-access --runtime <name> --tenant <id>` | Grant tenant access to a runtime | `platform-admin` |
| `lenny-ctl admin runtimes list-access --runtime <name>` | List tenants with access | `platform-admin` |
| `lenny-ctl admin runtimes revoke-access --runtime <name> --tenant <id>` | Revoke tenant access | `platform-admin` |

---

## Pool Management

### Pool Operations

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin pools list` | List all registered warm pools | `platform-admin` |
| `lenny-ctl admin pools get <name>` | Show pool configuration and status | `platform-admin` |
| `lenny-ctl admin pools set-warm-count --pool <name> --min <N>` | Override `minWarm` for emergency scaling | `platform-admin` |
| `lenny-ctl admin pools exit-bootstrap --pool <name>` | Switch to formula-driven scaling | `platform-admin` |
| `lenny-ctl admin pools drain --pool <name>` | Drain a pool (stops new sessions) | `platform-admin` |
| `lenny-ctl admin pools sync-status --pool <name>` | Show CRD reconciliation state | `platform-admin` |

### Pool Access Control

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin pools grant-access --pool <name> --tenant <id>` | Grant tenant access to a pool | `platform-admin` |
| `lenny-ctl admin pools list-access --pool <name>` | List tenants with access | `platform-admin` |
| `lenny-ctl admin pools revoke-access --pool <name> --tenant <id>` | Revoke tenant access | `platform-admin` |

### Pool Image Upgrades

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin pools upgrade start --pool <name> --new-image <digest>` | Begin rolling image upgrade | `platform-admin` |
| `lenny-ctl admin pools upgrade proceed --pool <name>` | Advance to next upgrade phase | `platform-admin` |
| `lenny-ctl admin pools upgrade pause --pool <name>` | Pause upgrade state machine | `platform-admin` |
| `lenny-ctl admin pools upgrade resume --pool <name>` | Resume paused upgrade | `platform-admin` |
| `lenny-ctl admin pools upgrade rollback --pool <name>` | Rollback (from Expanding state) | `platform-admin` |
| `lenny-ctl admin pools upgrade rollback --pool <name> --restore-old-pool` | Rollback with old pool restoration | `platform-admin` |
| `lenny-ctl admin pools upgrade status --pool <name>` | Show upgrade state and progress | `platform-admin` |

### SDK-Warm Circuit Breaker

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin pools circuit-breaker --pool <name> --state <enabled\|disabled\|auto>` | Override SDK-warm circuit breaker | `platform-admin` |

---

## Credential Management

### Credential Pool Operations

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin credential-pools list` | List all credential pools | `platform-admin` |
| `lenny-ctl admin credential-pools get --pool <name>` | Show pool details with health scores | `platform-admin` |
| `lenny-ctl admin credential-pools add-credential --pool <name> --provider <p>` | Add credential to pool | `platform-admin` |
| `lenny-ctl admin credential-pools update-credential --pool <name> --credential <id>` | Update a credential | `platform-admin` |
| `lenny-ctl admin credential-pools remove-credential --pool <name> --credential <id>` | Remove a credential | `platform-admin` |

### Emergency Revocation

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin credential-pools revoke-credential --pool <name> --credential <id> --reason <r>` | Revoke a single credential (terminates all leases) | `platform-admin` |
| `lenny-ctl admin credential-pools revoke-pool --pool <name> --reason <r>` | Revoke all credentials in a pool | `platform-admin` |
| `lenny-ctl admin credential-pools re-enable --pool <name> --credential <id> --reason <r>` | Re-enable a revoked credential | `platform-admin` |

---

## Quota Operations

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin quota reconcile --all-tenants` | Re-aggregate usage from Postgres into Redis after Redis recovery | `platform-admin` |

---

## Circuit Breakers

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin circuit-breakers list` | List all circuit breakers and state | `platform-admin` |
| `lenny-ctl admin circuit-breakers open <name>` | Open a circuit breaker (platform-wide) | `platform-admin` |
| `lenny-ctl admin circuit-breakers close <name>` | Close a circuit breaker | `platform-admin` |

---

## Tenant Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin tenants list` | List all tenants and their state | `platform-admin` |
| `lenny-ctl admin tenants get <id>` | Show tenant configuration and deletion state | `platform-admin` |
| `lenny-ctl admin tenants delete <id>` | Initiate tenant deletion lifecycle | `platform-admin` |
| `lenny-ctl admin tenants force-delete <id> --justification <text>` | Force-delete with active legal holds | `platform-admin` |

---

## User and Token Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin users rotate-token --user <name>` | Rotate admin token (internally calls `POST /v1/oauth/token` with RFC 8693 token-exchange grant) and patch K8s Secret | `platform-admin` |
| `lenny-ctl admin users invalidate --user <name>` | Invalidate all active sessions for a user | `platform-admin` |

---

## Connector Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin connectors list` | List all registered connectors | `platform-admin` |
| `lenny-ctl admin connectors create --from-file <file>` | Register a new connector from YAML definition | `platform-admin` |
| `lenny-ctl admin connectors update --name <name> --from-file <file>` | Update an existing connector | `platform-admin` |
| `lenny-ctl admin connectors delete --name <name>` | Delete a connector | `platform-admin` |

---

## Experiment Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin experiments list` | List all experiments and their state | `platform-admin` |
| `lenny-ctl admin experiments create --from-file <file>` | Create a new experiment from YAML definition | `platform-admin` |
| `lenny-ctl admin experiments pause --name <name>` | Pause an active experiment (variant pools scale to zero) | `platform-admin` |
| `lenny-ctl admin experiments conclude --name <name>` | Conclude an experiment (variant pools torn down) | `platform-admin` |
| `lenny-ctl admin experiments results --name <name>` | Show per-variant aggregation with mean, p50, p95, and per-dimension breakdowns | `platform-admin` |

---

## Legal Hold Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin legal-holds set --resource-type <type> --resource-id <id> --note <text>` | Set a legal hold on a session or artifact | `platform-admin` / `tenant-admin` |
| `lenny-ctl admin legal-holds clear --resource-type <type> --resource-id <id>` | Clear a legal hold | `platform-admin` / `tenant-admin` |
| `lenny-ctl admin legal-holds list` | List active legal holds (filterable by `--tenant-id`, `--resource-type`) | `platform-admin` / `tenant-admin` |

---

## Session Investigation

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin sessions get <id>` | Show session state, metadata, assigned pod | `platform-admin` |
| `lenny-ctl admin sessions force-terminate <id>` | Force-terminate a stuck session | `platform-admin` |

---

## Erasure Job Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin erasure-jobs get <job-id>` | Show erasure job status and progress | `platform-admin` |
| `lenny-ctl admin erasure-jobs retry <job-id>` | Retry a failed erasure job | `platform-admin` |
| `lenny-ctl admin erasure-jobs clear-restriction <job-id> --justification <text>` | Clear `processing_restricted` flag | `platform-admin` |

---

## Migration Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl migrate status` | Show expand-contract migration status | `platform-admin` |

### Phase Coordination Workflow

```bash
# Verify Phase 1 is complete before enabling Phase 2 reads
lenny-ctl migrate status
# Look for: version=<N> phase=phase1_applied

# Verify Phase 3 gate will pass before deploying Phase 3 migration
lenny-ctl migrate status
# Look for: gateCheckResult=pass
# If gateCheckResult=fail:<N>_rows -- do not deploy Phase 3
```

---

## External Adapter Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin external-adapters validate --name <name>` | Run compliance suite against an adapter | `platform-admin` |

---

## Policy Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl policy audit-isolation` | Report delegation policy isolation violations | `platform-admin` |

This command reports all `DelegationPolicy` rule and pool combinations where a delegation would be rejected at runtime due to isolation monotonicity violations. Run after registering new pools or runtimes.

---

## Common Workflows

### Initial Deployment

```bash
# 1. Validate infrastructure
lenny-ctl preflight --config values.yaml

# 2. Install the chart (bootstrap runs automatically)
helm install lenny lenny/lenny -n lenny-system --values values.yaml

# 3. Retrieve admin token
kubectl get secret lenny-admin-token -n lenny-system \
  -o jsonpath='{.data.token}' | base64 -d

# 4. Verify deployment
lenny-ctl admin pools list
lenny-ctl admin credential-pools list
```

### Credential Rotation

```bash
# 1. Create new credential at provider
# 2. Update K8s Secret
kubectl create secret generic anthropic-key-new \
  -n lenny-system --from-literal=apiKey="sk-ant-new-..."

# 3. Add to pool
lenny-ctl admin credential-pools add-credential \
  --pool anthropic-prod --provider anthropic_direct

# 4. Remove old credential
lenny-ctl admin credential-pools remove-credential \
  --pool anthropic-prod --credential old-key-id
```

### Emergency Response

```bash
# Open circuit breaker to stop all traffic
lenny-ctl admin circuit-breakers open session-creation

# Investigate
lenny-ctl admin sessions get <problematic-session-id>

# Force terminate stuck sessions
lenny-ctl admin sessions force-terminate <session-id>

# Close circuit breaker to resume
lenny-ctl admin circuit-breakers close session-creation
```

### Post-Redis-Recovery

```bash
# Reconcile quotas
lenny-ctl admin quota reconcile --all-tenants

# Verify pool status
lenny-ctl admin pools list

# Check credential pools
lenny-ctl admin credential-pools list
```

---

## Session Operations

Session commands route through the **MCP** client SDK, not REST. They exercise the same code path that any other MCP client uses — the CLI embeds the Lenny Go client SDK and opens a full MCP session for the duration of the command. The short form `lenny session` is preferred in developer-facing examples; the long form `lenny-ctl session` is preferred in operator runbooks. Both are identical.

| Command | Description | Min Role |
|---|---|---|
| `lenny session new --runtime <name> [--attach] [--workspace <dir>] [--file <path>]...` | Create a session against the specified runtime. With `--attach` (default in interactive TTYs), the CLI opens an MCP stream and renders the session's output, elicitation prompts, and lifecycle transitions inline. | `user` |
| `lenny session attach <sessionId>` | Attach to an existing session and stream its output from the current cursor. Supports reconnect-with-cursor. | `user` (owner) |
| `lenny session send <sessionId> <message>` | Send a user message to an existing session. If the CLI is not attached, the message is delivered and the CLI exits. | `user` (owner) |
| `lenny session interrupt <sessionId>` | Send an interrupt to a running session. | `user` (owner) |
| `lenny session cancel <sessionId>` | Cancel a session cooperatively. | `user` (owner) |
| `lenny session list [--runtime <name>] [--status <status>]` | List the caller's sessions. The one session command that also works over REST for non-interactive callers. | `user` |
| `lenny session get <sessionId>` | Fetch a session's current state. | `user` (owner) |
| `lenny session logs <sessionId> [--since <time>]` | Fetch session logs from the event store (paginated, streamable via SSE). | `user` (owner) |

`lenny session` commands honor the same `--api-url` / `LENNY_API_URL` discovery rules as admin commands; there is no separate MCP endpoint flag because Lenny's MCP surface is served from the same gateway host under `/mcp`.

---

## Runtime Scaffolding

`lenny runtime init` scaffolds a new runtime repository offline. No API calls are made by the scaffolder itself.

| Command | Description | Min Role |
|---|---|---|
| `lenny runtime init <name> --language {go\|python\|typescript\|binary} --template {chat\|coding\|minimal}` | Generate a new runtime skeleton in `./<name>/`. The `coding` template pre-wires the shared coding-agent workspace plan; `chat` is the minimal non-coding template; `minimal` is a bare Hello World. | none (local) |
| `lenny runtime validate [<path>]` | Validate a runtime repository against the Runtime Adapter Specification and the `runtime.yaml` contract. Runs in the current directory by default. | none (local) |
| `lenny runtime publish <name> --image <ref>` | Convenience wrapper for `docker push` and `lenny-ctl admin runtimes register`. Pushes the built image to the configured registry and registers the runtime against the target gateway. Requires `--api-url` and an admin token. | `platform-admin` (for the register step) |

---

## Local Stack

The embedded Tier 0 stack is managed entirely through local-only commands. They do not require a remote `LENNY_API_URL` — the embedded gateway binds to `https://localhost:8443` by default.

| Command | Description |
|---|---|
| `lenny up` | Start the embedded k3s / Postgres / Redis / KMS / OIDC / gateway / controllers / reference runtimes stack. Idempotent. Prints the local gateway URL and the non-suppressible "NOT for production use" banner. |
| `lenny down [--purge]` | Gracefully terminate all embedded components. `--purge` additionally deletes `~/.lenny/`. |
| `lenny status` | Print component health, active session count, and resource usage. |
| `lenny logs [<component>] [--follow]` | Tail merged logs, or filter to one of `gateway`, `controller`, `ops`, `postgres`, `redis`, `kms`, `oidc`, `runtime-<name>`. |
| `lenny restart [<component>]` | Restart a single embedded component without tearing down the rest of the stack. |

Invoked as `lenny` (short name) these commands default to the local stack; invoked as `lenny-ctl <same-command>` they behave identically but are documented under the operator-tool framing.

---

## Installation Wizard

The wizard orchestrates the flow from [Installation](installation): cluster detection → question phase → preview → preflight → `helm install` → bootstrap seed → smoke test. Detection, question rendering, and values composition run locally; the install step delegates to `helm` (invoked as a subprocess) and `lenny-ctl bootstrap`.

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl install` | Interactive installer. Runs cluster detection, asks the question set, previews the composite values file, runs preflight, invokes `helm install`, seeds the bootstrap config, and runs a smoke test against the `chat` reference runtime. | `platform-admin` on the target cluster |
| `lenny-ctl install --non-interactive --answers <file>` | Run the installer using a pre-captured answer file. Used in CI/IaC. | `platform-admin` |
| `lenny-ctl install --save-answers <file>` | Run the interactive wizard and save the answer file to `<file>` for replay. Does not run `helm install` when combined with `--dry-run`. | `platform-admin` |
| `lenny-ctl install --offline` | Skip cluster-reachability probes in the detection phase. Preflight still runs against the target cluster. | `platform-admin` |
| `lenny-ctl values validate --config <values.yaml>` | Validate a `values.yaml` against the Helm `values.schema.json` published by the chart. Exits 0 on success. | none |
| `lenny-ctl upgrade --answers <file>` | Replay an answer file against an existing install. Runs preflight, renders the values diff, and invokes `helm upgrade`. | `platform-admin` |

---

## Agent-Operability Commands

These commands target `lenny-ops` (the management plane) rather than the gateway. See [Server discovery and routing](#server-discovery-and-routing) for how `--ops-server` is resolved.

### Identity (`lenny-ctl me`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl me` | Show caller identity, authorization, rate-limits, and platform capabilities | any authenticated role |
| `lenny-ctl me tools` | List tools the caller can actually invoke | any authenticated role |
| `lenny-ctl me operations` | Caller's in-flight operations | any authenticated role |

### Operations (`lenny-ctl operations`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl operations list [--actor <sub>] [--kind <csv>] [--status <csv>]` | Unified list of in-flight operations (upgrades, restores, drains, migrations) with canonical Progress Envelope output | `platform-admin` |
| `lenny-ctl operations get <operationId>` | Full detail of a single operation, including every progress update emitted so far | `platform-admin` |

### Diagnostics (`lenny-ctl diagnose`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl diagnose session <id>` | Structured diagnosis for one session — pod assignment, event timeline, credential lease state, cause chain | `platform-admin` |
| `lenny-ctl diagnose pool <name>` | Pool health, warm-pod counts, controller reconciliation state, CRD drift | `platform-admin` |
| `lenny-ctl diagnose credential-pool <name>` | Credential pool health, lease counts, per-credential status | `platform-admin` |
| `lenny-ctl diagnose connectivity` | Dependency connectivity check (Postgres, Redis, MinIO, KMS, OIDC, providers) | `platform-admin` |

### Events (`lenny-ctl events`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl events tail` | Stream operational events via SSE (CloudEvents v1.0.2 envelopes) | `platform-admin` |
| `lenny-ctl events list --since <time>` | Poll the event buffer for events since a given time | `platform-admin` |
| `lenny-ctl events subscriptions list` | List webhook subscriptions | `platform-admin` |
| `lenny-ctl events subscriptions create --url <url> --types <csv>` | Create a webhook subscription | `platform-admin` |
| `lenny-ctl events subscriptions delete <id>` | Delete a webhook subscription | `platform-admin` |

### Runbooks (`lenny-ctl runbooks`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl runbooks list` | List all registered runbooks with their triggers | `platform-admin` |
| `lenny-ctl runbooks list --alert <name>` | Find runbooks keyed to a specific alert | `platform-admin` |
| `lenny-ctl runbooks get <name>` | Print the full structured runbook (machine-parseable steps) | `platform-admin` |

### Audit (`lenny-ctl audit`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl audit query --since <time> [--tenant <id>] [--filter <field=value>]` | Query audit events with scatter-gather across shards (OCSF v1.1.0 JSON, NDJSON) | `platform-admin` / `tenant-admin` |
| `lenny-ctl audit get <id>` | Get a single audit event by ID | `platform-admin` |
| `lenny-ctl audit summary --since <time>` | Aggregate counts (by caller kind, operation ID, severity) | `platform-admin` |
| `lenny-ctl audit retranslate <id> [--translator-version <semver>]` | Retry OCSF translation on a `retry_pending` or `dead_lettered` row | `platform-admin` |
| `lenny-ctl audit chain-verify --partition <YYYY-MM>` | Re-hash a partition's records and verify the `prev_hash` chain end-to-end | `platform-admin` |
| `lenny-ctl audit drop-partition <partition> --force --acknowledge-data-loss` | Force-drop an audit partition held by the SIEM delivery guard — permanently discards any events not yet forwarded | `platform-admin` |

### Drift detection (`lenny-ctl drift`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl drift report [--scope <s>] [--against <live\|target\|both>]` | Structured drift report against the stored desired-state snapshot | `platform-admin` |
| `lenny-ctl drift validate --desired <file>` | Validate a proposed desired-state file against the stored snapshot | `platform-admin` |
| `lenny-ctl drift snapshot refresh --desired <file>` | Replace the stored desired-state snapshot | `platform-admin` |
| `lenny-ctl drift reconcile [--scope <s>] [--confirm]` | Reconcile drifted resources back to desired state (requires `--confirm` to mutate) | `platform-admin` |

### Backup (`lenny-ctl backup`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl backup list` | List available backups with size, age, and provenance | `platform-admin` |
| `lenny-ctl backup get <id>` | Backup details | `platform-admin` |
| `lenny-ctl backup create --type <full\|postgres\|config> [--confirm]` | Trigger an on-demand backup | `platform-admin` |
| `lenny-ctl backup verify <id> [--mode test-restore]` | Verify backup integrity; `--mode test-restore` spins up an ephemeral Postgres and replays | `platform-admin` |
| `lenny-ctl backup schedule get / set` | Get or set the backup schedule | `platform-admin` |
| `lenny-ctl backup policy get / set` | Get or set the retention policy | `platform-admin` |

### Restore (`lenny-ctl restore`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl restore safety-check --backup <id>` | Estimate data loss (RPO) from restoring the given backup | `platform-admin` |
| `lenny-ctl restore preview --backup <id>` | Preview what would be restored, which shards are affected, and the estimated duration | `platform-admin` |
| `lenny-ctl restore execute --backup <id> --confirm --acknowledge-data-loss` | Execute the restore; both explicit confirmation flags are required | `platform-admin` |
| `lenny-ctl restore status <id>` | Per-shard restore status | `platform-admin` |
| `lenny-ctl restore resume <id>` | Resume a partially-completed restore after a transient failure | `platform-admin` |

### Remediation locks (`lenny-ctl locks`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl locks list` | List active remediation locks across `lenny-ops` replicas | `platform-admin` |
| `lenny-ctl locks acquire --scope <scope> --op <op>` | Acquire a lock explicitly (normally taken automatically by mutating endpoints) | `platform-admin` |
| `lenny-ctl locks release <id>` | Release a lock held by the caller | `platform-admin` |
| `lenny-ctl locks steal <id>` | Forcibly steal a lock held by another caller. Audited with the caller identity and a required justification. | `platform-admin` |

### Escalations (`lenny-ctl escalations`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl escalations list` | List pending and recent escalations | `platform-admin` |
| `lenny-ctl escalations create --severity <sev> --summary <text>` | Raise an escalation (e.g., agent detects a condition it cannot resolve) | `platform-admin` |
| `lenny-ctl escalations resolve <id>` | Mark an escalation as resolved, with optional resolution notes | `platform-admin` |

### Logs (`lenny-ctl logs`)

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl logs pod <ns> <name>` | Get the pod's container logs via the ops service (no cluster access required) | `platform-admin` |
| `lenny-ctl logs pod <ns> <name> --tail 100` | Last N lines | `platform-admin` |
| `lenny-ctl logs pod <ns> <name> --previous` | Previous container's logs (for crash-loop triage) | `platform-admin` |

### Platform upgrade (`lenny-ctl upgrade`)

These orchestrate the platform-upgrade state machine driven by `lenny-ops`. They're distinct from `lenny-ctl admin pools upgrade *` (which upgrades a single pool's image) and from `lenny-ctl upgrade --answers <file>` (which replays the installer's answer file).

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl upgrade check` | Check for a new Lenny release and compatibility with the current install | `platform-admin` |
| `lenny-ctl upgrade preflight --version <v>` | Validate upgrade safety before starting (preflight + schema-migration dry-run) | `platform-admin` |
| `lenny-ctl upgrade start --version <v> [--confirm]` | Begin the upgrade state machine | `platform-admin` |
| `lenny-ctl upgrade proceed` | Advance to the next phase | `platform-admin` |
| `lenny-ctl upgrade pause` | Pause the upgrade | `platform-admin` |
| `lenny-ctl upgrade rollback [--confirm]` | Rollback the upgrade | `platform-admin` |
| `lenny-ctl upgrade status` | Current upgrade state | `platform-admin` |
| `lenny-ctl upgrade verify` | Post-upgrade health verification | `platform-admin` |

### MCP management (`lenny-ctl mcp-management`)

Direct access to the MCP management surface served under `/mcp/management` on `lenny-ops`. Useful for end-to-end testing and scripting.

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl mcp-management tools list` | List exposed MCP tools (`tools/list`) | `platform-admin` |
| `lenny-ctl mcp-management tools call <name> --args <json>` | Invoke a tool through MCP (`tools/call`) | `platform-admin` |
