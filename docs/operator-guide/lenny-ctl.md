---
layout: default
title: lenny-ctl Reference
parent: "Operator Guide"
nav_order: 11
---

# lenny-ctl Reference

`lenny-ctl` is the official CLI for Lenny platform operators. It is a thin client over the Admin API with near-zero business logic -- every operation maps to an Admin API call.

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
| `--token` | `LENNY_API_TOKEN` | Admin authentication token |
| `--timeout` | -- | Request timeout (default: 30s) |
| `--output json` | -- | Machine-readable JSON output |
| `--quiet` | -- | Suppress informational messages |
| `--insecure-skip-verify` | -- | Skip TLS verification (dev only) |

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

## Audit Partition Management

| Command | Description | Min Role |
|---|---|---|
| `lenny-ctl admin audit drop-partition --force` | Drop an audit partition that is past retention TTL but blocked by SIEM forwarder backlog | `platform-admin` |
| `lenny-ctl admin audit query --tenant <id> --since <ts> [--filter <field=value>]` | Query OCSF audit records from the hot tier. Output is OCSF v1.1.0 JSON; combine with `jq` to extract specific fields. | `platform-admin` / `tenant-admin` |
| `lenny-ctl admin audit chain-verify --partition <YYYY-MM>` | Re-hash a partition's records and verify the `prev_hash` chain end-to-end. Reports any tamper or gap with row IDs. | `platform-admin` |

### Output format

Audit query output follows the OCSF v1.1.0 wire format documented in the [OCSF audit guide](audit-ocsf.md). Records are NDJSON, one OCSF record per line, unwrapped (no CloudEvents envelope -- the CLI reads directly from the Postgres hot tier).

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
