## 24. `lenny-ctl` Command Reference

`lenny-ctl` is the official CLI for Lenny platform operators. It is a thin client over two complementary external surfaces ([§15](15_external-api-surface.md)) and carries near-zero business logic of its own:

- **Admin / operator commands** (`lenny-ctl admin *`, `lenny-ctl tenants *`, `lenny-ctl runtimes *`, `lenny-ctl pools *`, `lenny-ctl credentials *`, every `/v1/admin/*` call, and the agent-operability commands in [§24.15](#2415-agent-operability-commands)) map 1:1 to the **REST** Admin API ([§15.1](15_external-api-surface.md#151-rest-api)).
- **Session commands** (`lenny session *` — or equivalently `lenny-ctl session *`; see [§24.17](#2417-session-operations)) map to the **MCP** API ([§15.2](15_external-api-surface.md#152-mcp-api)). They use the Lenny Go client SDK under the hood so that the interactive streaming, elicitation, and delegation flows work through the same MCP path as any other client. The short form (`lenny session`) is preferred in developer-facing examples; the long form (`lenny-ctl session`) is preferred in operator runbooks.

Every command requires `LENNY_API_URL` (or `--api-url` flag) and a valid admin token (`LENNY_API_TOKEN` or `--token` flag), with the exceptions below. Minimum required role is noted per command group.

**Exceptions to the thin-client rule.**

1. **`lenny-ctl preflight`** operates in two modes — standalone (reads `values.yaml` directly and probes infrastructure without a running gateway) and API-backed (delegates to `POST /v1/admin/preflight` on a running gateway). In standalone mode it embeds the preflight check logic locally because preflight must run before the platform is deployed.
2. **`lenny-ctl install`** ([§24.20](#2420-installation-wizard)) carries the interactive installer's question engine and values-rendering logic locally; it calls `helm` and then `lenny-ctl bootstrap` internally.
3. **`lenny up` / `lenny down` / `lenny status`** ([§24.19](#2419-local-stack)) manage the Embedded Mode single-binary stack ([§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)) and run the gateway, controllers, embedded Postgres/Redis, and k3s in-process. These are local-only commands; they do not call any remote API.
4. **`lenny runtime init`** ([§24.18](#2418-runtime-scaffolding)) is an offline scaffolder that emits a new runtime repository skeleton.

**One binary, two names.** The binary ships as both `lenny` (short name, Embedded Mode ergonomics) and `lenny-ctl` (long name, operator context). Both names support every subcommand; the docs use the short form in local/developer contexts and the long form in operator contexts. See [§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) for the binary-vs-symlink layout.

### 24.0 Packaging and Installation

The CLI ships in two interchangeable forms that use identical flags, arguments, and output formats:

1. **Standalone binary `lenny-ctl`.** Released as signed binaries for Linux, macOS, and Windows on every tagged release. Installed via the project's Homebrew tap (`brew install lennylabs/tap/lenny-ctl`), via `go install github.com/lennylabs/lenny/cmd/lenny-ctl@latest`, or by downloading the binary directly. This is the canonical form for CI/CD pipelines, air-gapped environments, and operators who do not use `kubectl`.
2. **kubectl plugin `kubectl-lenny`.** The same binary, released with the name `kubectl-lenny`, is distributed through the [krew plugin index](https://krew.sigs.k8s.io/). Installation: `kubectl krew install lenny`. Invocation: `kubectl lenny <subcommand>` — every subcommand, flag, and environment variable is identical to `lenny-ctl <subcommand>`. The plugin is discoverable via `kubectl krew search lenny` and upgradable via `kubectl krew upgrade lenny`.

Both forms read `LENNY_API_URL` / `--api-url` to locate the Lenny gateway. `kubectl-lenny` does NOT auto-discover the Lenny API URL from the active kubeconfig context — `kubectl lenny` and `lenny-ctl` use the same explicit discovery rules described in [§24.16](#2416-server-discovery-and-routing). This is intentional: the `kubectl` context identifies the Kubernetes cluster, not the Lenny deployment within it, and Lenny may live in any namespace or behind any Ingress hostname. Operators who want kubectl-context-derived discovery can set `LENNY_API_URL` via a per-context shell alias.

**Release process.** Each tagged Lenny release produces: (a) the standalone `lenny-ctl` binaries (signed with cosign); (b) the same binaries released as `kubectl-lenny` with a krew `plugin.yaml` manifest pointing at their checksums. The krew-index pull request is opened post-tag by the release automation. See [§17.6](17_deployment-topology.md#176-packaging-and-installation) for the full release pipeline.

### 24.1 Bootstrap

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl bootstrap --from-values <file>` | Apply seed configuration (runtimes, pools, tenants, users) using upsert semantics | `POST /v1/admin/bootstrap` | `platform-admin` |
| `lenny-ctl bootstrap --dry-run --from-values <file>` | Validate seed file and report what would be created/updated without making changes | `POST /v1/admin/bootstrap?dryRun=true` | `platform-admin` |

### 24.2 Preflight

`lenny-ctl preflight` is the only subcommand that supports two execution modes: **standalone** and **API-backed**. In standalone mode (the default when no `--api-url` is set or the gateway is unreachable), the CLI embeds the full preflight check logic and probes infrastructure directly from the caller's machine — no running Lenny deployment is required. In API-backed mode (when `--api-url` is set and the gateway is reachable), the CLI delegates to the Admin API endpoint, which runs the same checks server-side. Standalone mode is the primary use case: pre-deployment validation in CI pipelines and manual verification before `helm install`.

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl preflight --config <values.yaml>` | Run all preflight checks. Uses standalone mode by default; delegates to the Admin API when `--api-url` is set and the gateway is reachable. | `POST /v1/admin/preflight` (API-backed mode) or local execution (standalone mode) | `platform-admin` (API-backed) / none (standalone) |
| `lenny-ctl doctor` | Run a richer diagnostic pass combining `preflight` checks with the server-side diagnostic endpoints from [§25.6](25_agent-operability.md#256-diagnostic-endpoints) (connectivity, pool health, credential pool health). Outputs a remediation report. | `POST /v1/admin/diagnostics/run` | `platform-admin` |
| `lenny-ctl doctor --fix` | Run `lenny-ctl doctor` and apply the auto-remediations documented in [§25.6](25_agent-operability.md#256-diagnostic-endpoints) (e.g., restart CoreDNS replicas, re-seed the bootstrap config, refresh cert-manager certificates). Each remediation is logged with the corresponding operation ID ([§25.2](25_agent-operability.md#252-architecture-overview)). Non-fixable findings are printed as an operator action list. | `POST /v1/admin/diagnostics/run?fix=true` | `platform-admin` |

**Standalone mode credential handling.** In standalone mode, `lenny-ctl preflight` must connect to Postgres, Redis, and MinIO to validate infrastructure. Connection strings (DSNs) are resolved in the following precedence order (highest wins): (1) **CLI flags** `--postgres-dsn`, `--redis-dsn`, `--minio-endpoint` override all other sources (use in CI pipelines where credentials are injected from a secrets manager: `lenny-ctl preflight --config values.yaml --postgres-dsn "\$LENNY_PG_DSN"`); (2) **Environment variables** `LENNY_POSTGRES_DSN`, `LENNY_REDIS_DSN`, `LENNY_MINIO_ENDPOINT` (plus `LENNY_MINIO_ACCESS_KEY`, `LENNY_MINIO_SECRET_KEY`) take precedence over the values file but are overridden by CLI flags; (3) **Values file** fields (`postgres.connectionString`, `redis.connectionString`, `minio.endpoint`) from `--config` are the fallback. This precedence order allows operators to commit a values file without inline credentials and inject secrets at runtime. The values file example in [Section 17.6](17_deployment-topology.md#176-packaging-and-installation) uses inline credentials for illustrative purposes only; production CI pipelines should use option 1 or 2.

### 24.3 Runtime Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin runtimes grant-access --runtime <name> --tenant <id>` | Grant a tenant access to a runtime | `POST /v1/admin/runtimes/{name}/tenant-access` | `platform-admin` |
| `lenny-ctl admin runtimes list-access --runtime <name>` | List tenants with access to a runtime | `GET /v1/admin/runtimes/{name}/tenant-access` | `platform-admin` |
| `lenny-ctl admin runtimes revoke-access --runtime <name> --tenant <id>` | Revoke a tenant's access to a runtime | `DELETE /v1/admin/runtimes/{name}/tenant-access/{tenantId}` | `platform-admin` |

### 24.4 Pool Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin pools list` | List all registered warm pools | `GET /v1/admin/pools` | `platform-admin` |
| `lenny-ctl admin pools get <name>` | Show pool configuration and current status | `GET /v1/admin/pools/{name}` | `platform-admin` |
| `lenny-ctl admin pools set-warm-count --pool <name> --min <N>` | Override `minWarm` for emergency scaling | `PUT /v1/admin/pools/{name}/warm-count` | `platform-admin` |
| `lenny-ctl admin pools exit-bootstrap --pool <name>` | Remove the bootstrap `minWarm` override and switch to formula-driven scaling immediately, regardless of the 48-hour convergence window. Use when early traffic data is sufficient to trust the formula ([§17.8.2](17_deployment-topology.md#1782-capacity-tier-reference)). | `DELETE /v1/admin/pools/{name}/bootstrap-override` | `platform-admin` |
| `lenny-ctl admin pools upgrade start --pool <name> --new-image <digest>` | Begin rolling image upgrade for a pool | `POST /v1/admin/pools/{name}/upgrade/start` | `platform-admin` |
| `lenny-ctl admin pools upgrade proceed --pool <name>` | Advance to next upgrade phase | `POST /v1/admin/pools/{name}/upgrade/proceed` | `platform-admin` |
| `lenny-ctl admin pools upgrade pause --pool <name>` | Pause upgrade state machine | `POST /v1/admin/pools/{name}/upgrade/pause` | `platform-admin` |
| `lenny-ctl admin pools upgrade resume --pool <name>` | Resume paused upgrade | `POST /v1/admin/pools/{name}/upgrade/resume` | `platform-admin` |
| `lenny-ctl admin pools upgrade rollback --pool <name>` | Rollback in-progress upgrade to previous image (from `Expanding` state — restores full routing to old pool and transitions to `Paused`) | `POST /v1/admin/pools/{name}/upgrade/rollback` | `platform-admin` |
| `lenny-ctl admin pools upgrade rollback --pool <name> --restore-old-pool` | Rollback from `Draining` or `Contracting` state — recreates the old pool configuration from `RuntimeUpgrade.previousPoolSpec` and restores routing. Only valid while the old `SandboxTemplate` CRD still exists. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy). | `POST /v1/admin/pools/{name}/upgrade/rollback` (body: `{"restoreOldPool": true}`) | `platform-admin` |
| `lenny-ctl admin pools upgrade status --pool <name>` | Show upgrade state and progress | `GET /v1/admin/pools/{name}/upgrade-status` | `platform-admin` |
| `lenny-ctl admin pools drain --pool <name>` | Drain a pool — stops new session assignments and waits for in-flight sessions to complete. Returns estimated drain completion time. New session requests targeting this pool receive `503 POOL_DRAINING` with a `Retry-After` header. See [Section 15.1](15_external-api-surface.md#151-rest-api). | `POST /v1/admin/pools/{name}/drain` | `platform-admin` |
| `lenny-ctl admin pools sync-status --pool <name>` | Show CRD reconciliation state for a pool (`postgresGeneration`, `crdGeneration`, `lastReconciledAt`, `lagSeconds`, `inSync`). Use when the `PoolConfigDrift` alert fires to diagnose whether the PoolScalingController is keeping up. | `GET /v1/admin/pools/{name}/sync-status` | `platform-admin` |
| `lenny-ctl admin pools circuit-breaker --pool <name> --state <enabled\|disabled\|auto>` | Override the SDK-warm circuit-breaker state for a pool. `enabled` forces SDK-warm on; `disabled` forces off; `auto` restores automatic control. Use after adjusting `sdkWarmBlockingPaths` to re-enable SDK-warm following a circuit-breaker trip. | `PUT /v1/admin/pools/{name}/circuit-breaker` | `platform-admin` |
| `lenny-ctl admin pools grant-access --pool <name> --tenant <id>` | Grant a tenant access to a pool | `POST /v1/admin/pools/{name}/tenant-access` | `platform-admin` |
| `lenny-ctl admin pools list-access --pool <name>` | List tenants with access to a pool | `GET /v1/admin/pools/{name}/tenant-access` | `platform-admin` |
| `lenny-ctl admin pools revoke-access --pool <name> --tenant <id>` | Revoke a tenant's access to a pool | `DELETE /v1/admin/pools/{name}/tenant-access/{tenantId}` | `platform-admin` |

**Orphan session reconciliation** is automatic — the gateway runs a periodic reconciler every 60 seconds ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). There is no manual trigger command; operators can observe reconciliation activity via the `lenny_orphan_session_reconciliations_total` counter. To investigate individual stuck sessions, use `lenny-ctl admin sessions get <id>` or `lenny-ctl admin sessions force-terminate <id>` (see [Section 24.11](#2411-session-investigation)).

### 24.5 Credential Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin credential-pools list` | List all credential pools and their status | `GET /v1/admin/credential-pools` | `platform-admin` |
| `lenny-ctl admin credential-pools get --pool <name>` | Show credential pool details including per-credential health scores and lease counts | `GET /v1/admin/credential-pools/{name}` | `platform-admin` |
| `lenny-ctl admin credential-pools add-credential --pool <name> --provider <p>` | Add credential to pool (also emits required RBAC patch command) | `POST /v1/admin/credential-pools/{name}/credentials` | `platform-admin` |
| `lenny-ctl admin credential-pools update-credential --pool <name> --credential <id>` | Update a credential in the pool | `PUT /v1/admin/credential-pools/{name}/credentials/{credId}` | `platform-admin` |
| `lenny-ctl admin credential-pools remove-credential --pool <name> --credential <id>` | Remove a credential from the pool | `DELETE /v1/admin/credential-pools/{name}/credentials/{credId}` | `platform-admin` |
| `lenny-ctl admin credential-pools revoke-credential --pool <name> --credential <id> --reason <r>` | Emergency revocation of a single credential; terminates all active leases. See [Section 4.9](04_system-components.md#49-credential-leasing-service) emergency revocation runbook. | `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke` | `platform-admin` |
| `lenny-ctl admin credential-pools revoke-pool --pool <name> --reason <r>` | Emergency revocation of all credentials in a pool | `POST /v1/admin/credential-pools/{name}/revoke` | `platform-admin` |
| `lenny-ctl admin credential-pools re-enable --pool <name> --credential <id> --reason <r>` | Re-enable a previously revoked credential. Use after rotating the underlying secret at the provider and updating the Kubernetes Secret. | `POST /v1/admin/credential-pools/{name}/credentials/{credId}/re-enable` | `platform-admin` |

### 24.6 Quota Operations

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin quota reconcile --all-tenants` | Re-aggregate in-flight session usage from Postgres into Redis after Redis recovery | `POST /v1/admin/quota/reconcile` | `platform-admin` |

### 24.7 Circuit Breakers

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin circuit-breakers list` | List all circuit breakers and current state | `GET /v1/admin/circuit-breakers` | `platform-admin` |
| `lenny-ctl admin circuit-breakers open <name>` | Manually open a circuit breaker | `POST /v1/admin/circuit-breakers/{name}/open` | `platform-admin` |
| `lenny-ctl admin circuit-breakers close <name>` | Manually close a circuit breaker | `POST /v1/admin/circuit-breakers/{name}/close` | `platform-admin` |

### 24.8 External Adapter Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin external-adapters validate --name <name>` | Run the `RegisterAdapterUnderTest` compliance suite against a registered adapter. The suite is **schema-driven**: assertions are generated from the published `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, and `schemas/outputpart.schema.json` artifacts ([Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification)) rather than hand-coded against prose. Transitions the adapter from `pending_validation` to `active` on success, or `validation_failed` on failure (validation report cites the specific schema assertion that failed). Must be called before the adapter receives traffic. See [Section 15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract). | `POST /v1/admin/external-adapters/{name}/validate` | `platform-admin` |

### 24.9 User and Token Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin users rotate-token --user <name>` | Rotate admin token and patch Kubernetes Secret. Internally calls the canonical OAuth token endpoint with an RFC 8693 token-exchange grant (`subject_token=<current>`, `requested_token_type=<same>`), then writes the returned token to the `lenny-admin-token` Kubernetes Secret. | `POST /v1/oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` | `platform-admin` |

### 24.10 Tenant Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin tenants list` | List all tenants and their state | `GET /v1/admin/tenants` | `platform-admin` |
| `lenny-ctl admin tenants get <id>` | Show tenant configuration and deletion state | `GET /v1/admin/tenants/{id}` | `platform-admin` |
| `lenny-ctl admin tenants delete <id>` | Initiate tenant deletion lifecycle (transitions to `disabling` → `deleting` → `deleted`). The multi-phase deletion controller runs asynchronously; use `lenny-ctl admin tenants get <id>` to monitor progress. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). | `DELETE /v1/admin/tenants/{id}` | `platform-admin` |
| `lenny-ctl admin tenants force-delete <id> --justification <text>` | Force-delete a tenant with active legal holds. Operator identity and justification are recorded in the audit trail. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). | `POST /v1/admin/tenants/{id}/force-delete` | `platform-admin` |

### 24.11 Session Investigation

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin sessions get <id>` | Show session state, metadata, and assigned pod. Use to investigate stuck, orphaned, or unexpectedly terminated sessions. | `GET /v1/admin/sessions/{id}` | `platform-admin` |
| `lenny-ctl admin sessions force-terminate <id>` | Force-terminate a session that is stuck or unresponsive. The session transitions immediately to `failed` and the assigned pod is released to the pool. | `POST /v1/admin/sessions/{id}/force-terminate` | `platform-admin` |

### 24.12 Erasure Job Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl admin erasure-jobs get <job-id>` | Show erasure job status: phase, completion percentage, elapsed time, and any errors. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). | `GET /v1/admin/erasure-jobs/{job_id}` | `platform-admin` |
| `lenny-ctl admin erasure-jobs retry <job-id>` | Retry a failed erasure job. The job must be in `failed` state. Clears transient errors and resumes from the last persisted phase. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). | `POST /v1/admin/erasure-jobs/{job_id}/retry` | `platform-admin` |
| `lenny-ctl admin erasure-jobs clear-restriction <job-id> --justification <text>` | Manually clear the `processing_restricted` flag for a user after a failed erasure job. Operator identity and justification are recorded in the audit trail. Use only after confirming the failure is unrecoverable and retrying is not feasible. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). | `POST /v1/admin/erasure-jobs/{job_id}/clear-processing-restriction` | `platform-admin` |

### 24.13 Migration Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl migrate status` | Show the current expand-contract phase for every active schema migration. Output includes: `version` (migration file number), `phase` (`phase1_applied` \| `phase2_deployed` \| `phase3_applied` \| `complete`), `appliedAt` (timestamp), `gateCheckResult` (Phase 3 only: `pass`, `fail:<N>_rows`, or `not_run`), and `migrationJobName` (the Kubernetes Job that applied it). Use to confirm Phase 1 is fully deployed before starting Phase 2, or to verify the Phase 3 gate has passed before approving a Phase 3 deployment. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy) for the expand-contract discipline and Phase 3 gate query performance guidance. | `GET /v1/admin/schema/migrations/status` | `platform-admin` |
| `lenny-ctl migrate down --version <N> --confirm` | Reverse the most recently applied partial migration at version `<N>` by launching the down-migration Job with the provided `down.sql` file. Last-resort recovery path for a migration that left the database in a `dirty=true` state and whose failure cannot be resolved by a forward-fix. Requires `--confirm` because the operation is destructive. The Job releases stale advisory locks, applies the down migration, and clears the dirty flag on success. On failure, the caller inspects Job logs and falls back to direct DBA intervention. Audited as `platform.schema_migration_rolled_back`. See [Section 17.7](17_deployment-topology.md#177-operational-runbooks) (Schema migration failure) and [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy). | `POST /v1/admin/schema/migrations/{version}/down` | `platform-admin` |

**Phase coordination workflow:**

```bash
# Verify Phase 1 is complete before enabling Phase 2 reads
lenny-ctl migrate status
# Look for: version=<N> phase=phase1_applied — all gateway replicas must be on Phase 1 code

# Verify Phase 3 gate will pass before deploying Phase 3 migration
lenny-ctl migrate status
# Look for: gateCheckResult=pass (or not_run — means Phase 3 has not been applied yet)
# If gateCheckResult=fail:<N>_rows, un-migrated rows remain — do not deploy Phase 3
```

The `phase` field reflects the last migration Job that completed successfully; it does not automatically advance when operator deploys new code. Operators must advance each phase manually using a deployment or migration Job. The Phase 3 `gateCheckResult` is populated by the migration Job at the time it runs the PL/pgSQL `DO` block gate check (see [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy)).

### 24.14 Policy Management

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl policy audit-isolation` | Report all `DelegationPolicy` rule × pool combinations where a delegation would be rejected at runtime due to an isolation monotonicity violation. For each combination, lists the policy rule, the matching source pool, the matching target pool, and their respective isolation profiles. Read-only — does not modify any resources. Run after registering new pools or runtimes. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). | `GET /v1/admin/delegation-policies` + `GET /v1/admin/pools` (client-side join) | `platform-admin` |

### 24.15 Agent-Operability Commands

[Section 25.14](25_agent-operability.md#2514-lenny-ctl-extensions) adds the following command groups for AI-agent-driven operation. Full command tables (subcommand / description / API mapping / min role) live in §25.14; this section is the index so operators discover them from the main CLI reference.

| Group | Purpose | Primary API surface |
|-------|---------|---------------------|
| `lenny-ctl me` | Inspect the caller's own identity, scope, role, and authorized tools. | `/v1/admin/me`, `/v1/admin/me/authorized-tools`, `/v1/admin/me/operations` |
| `lenny-ctl operations` | List active long-running operations (upgrades, restores, drains, migrations) with canonical Progress Envelope output; fetch a single operation by ID. | `/v1/admin/operations`, `/v1/admin/operations/{id}` |
| `lenny-ctl events` | Tail the operational event stream (SSE), query the gateway buffer, and manage webhook subscriptions. | `/v1/admin/events`, `/v1/admin/events/stream`, `/v1/admin/events/buffer`, `/v1/admin/event-subscriptions` |
| `lenny-ctl diagnose` | Run the diagnostic endpoints for sessions, pools, credential pools, and platform connectivity. | `/v1/admin/diagnostics/*` |
| `lenny-ctl runbooks` | List and fetch structured (agent-parseable) runbook steps. | `/v1/admin/runbooks`, `/v1/admin/runbooks/{name}/steps` |
| `lenny-ctl upgrade` | Drive the platform-upgrade state machine: check, start, pause, rollback, complete, verify. | `/v1/admin/platform/upgrade/*`, `/v1/admin/platform/upgrade-check` |
| `lenny-ctl audit` | Query audit events with scatter-gather across shards; summarize by caller kind / operation ID; retry OCSF translation on failed rows; force-drop blocked audit partitions. | `/v1/admin/audit-events`, `/v1/admin/audit-events/summary`, `/v1/admin/audit-events/{id}/retranslate`, `/v1/admin/audit-partitions/{partition}/drop` |
| `lenny-ctl drift` | Fetch the drift report, trigger reconciliation, validate or refresh the desired-state snapshot. | `/v1/admin/drift`, `/v1/admin/drift/validate`, `/v1/admin/drift/snapshot/refresh` |
| `lenny-ctl backup` | List backups, verify, manage schedule and retention policy. | `/v1/admin/backups`, `/v1/admin/backups/{id}/verify`, `/v1/admin/backups/schedule`, `/v1/admin/backups/policy` |
| `lenny-ctl restore` | Preview, safety-check, execute, monitor, and resume restore operations. | `/v1/admin/restore/*` |
| `lenny-ctl locks` | Inspect, steal, and release remediation locks across `lenny-ops` replicas. | `/v1/admin/remediation-locks`, `/v1/admin/remediation-locks/{id}`, `/v1/admin/remediation-locks/{id}/steal` |
| `lenny-ctl escalations` | List and respond to pending operator escalations. | `/v1/admin/escalations` |
| `lenny-ctl logs` | Fetch pod logs from the ops service for sessions, controllers, and the gateway fleet. | `/v1/admin/logs/pods/*` |
| `lenny-ctl mcp-management` | Exercise the `/mcp/management` tools for local testing and scripting. | `/mcp/management` |

### 24.16 Server Discovery and Routing

In addition to the existing `--api-url` (or `LENNY_API_URL`) flag that targets the gateway, `lenny-ctl` now understands a separate `--ops-server` (or `LENNY_OPS_URL`) flag that targets the `lenny-ops` Ingress. The routing rule is:

1. If `--ops-server` (or `LENNY_OPS_URL`) is set, use it for every Section 25 ops-hosted endpoint.
2. Otherwise, call `GET /v1/admin/platform/version` on the gateway. Its response includes an `opsServiceURL` field; `lenny-ctl` caches this for the duration of the command invocation and routes ops calls there.
3. If auto-discovery fails (gateway unreachable, `opsServiceURL` absent because the cluster is mid-upgrade), `lenny-ctl` falls back to the gateway host under the assumption that gateway-hosted operability endpoints (§25.3) still work, and surfaces a warning for any ops-exclusive command.

This split means operators typically only need `--api-url`; `--ops-server` is for air-gapped clusters, split-DNS configurations, or direct-to-ops debugging.

All commands support `--output json` for machine-readable output and `--quiet` to suppress informational messages. Global flags: `--api-url`, `--ops-server`, `--token`, `--timeout`, `--insecure-skip-verify` (dev only). Cross-reference: [§17.7](17_deployment-topology.md#177-operational-runbooks) runbooks use specific subcommands from each group above.

### 24.17 Session Operations

Session commands route through the **MCP** client SDK, not REST. They exercise the same code path that any other MCP client uses ([§15.2](15_external-api-surface.md#152-mcp-api)) — the CLI embeds the Lenny Go client SDK ([§15.6](15_external-api-surface.md#156-client-sdks)) and opens a full MCP session for the duration of the command.

| Command | Description | API / SDK mapping | Min Role |
|---------|-------------|------------------|----------|
| `lenny session new --runtime <name> [--attach] [--workspace <dir>] [--file <path>]...` | Create a session against the specified runtime. When `--attach` is set (the default in interactive TTYs), the CLI opens an MCP stream and renders the session's output, elicitation prompts, and lifecycle transitions inline until the session terminates. | MCP `lenny/create_session` + stream | `user` |
| `lenny session attach <sessionId>` | Attach to an existing session and stream its output from the current cursor. Supports reconnect-with-cursor. | MCP connect + cursor resume | `user` (owner) |
| `lenny session send <sessionId> <message>` | Send a user message to an existing session. If the CLI is not attached, the message is delivered and the CLI exits. | MCP `lenny/send_message` | `user` (owner) |
| `lenny session interrupt <sessionId>` | Send an interrupt to a running session. | MCP `lenny/interrupt` | `user` (owner) |
| `lenny session cancel <sessionId>` | Cancel a session cooperatively. | MCP `lenny/cancel_session` | `user` (owner) |
| `lenny session list [--runtime <name>] [--status <status>]` | List the caller's sessions. This is the one session command that also works over REST for non-interactive callers. | `GET /v1/sessions` | `user` |
| `lenny session get <sessionId>` | Fetch a session's current state. | `GET /v1/sessions/{id}` | `user` (owner) |
| `lenny session logs <sessionId> [--since <time>]` | Fetch session logs from the event store (paginated, streamable via SSE). | `GET /v1/sessions/{id}/logs` | `user` (owner) |

`lenny session` commands honor the same `--api-url` / `LENNY_API_URL` discovery rules as admin commands; there is no separate MCP endpoint flag because Lenny's MCP surface is served from the same gateway host under `/mcp`.

### 24.18 Runtime Scaffolding

`lenny runtime init` scaffolds a new runtime repository by emitting the files described in [§15.7](15_external-api-surface.md#157-runtime-author-sdks) (Dockerfile, `main.<lang>`, `runtime.yaml`, Makefile, CI workflow). All logic is local — no API calls are made.

| Command | Description | Min Role |
|---------|-------------|----------|
| `lenny runtime init <name> --language {go\|python\|typescript\|binary} --template {chat\|coding\|minimal}` | Generate a new runtime skeleton in `./<name>/`. The `coding` template pre-wires the shared coding-agent workspace plan from [§26.2](26_reference-runtime-catalog.md#262-shared-patterns-for-coding-agent-runtimes); `chat` is the minimal non-coding template; `minimal` is a bare Hello World. | none (local) |
| `lenny runtime validate [<path>]` | Validate a runtime repository against the Runtime Adapter Specification ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)) and the `runtime.yaml` contract. Runs in the current directory by default. Serves as the `lenny runtime validate` entry point for the Conformance Test Suite ([§15.4.6](15_external-api-surface.md#1546-conformance-test-suite)). | none (local) |
| `lenny runtime publish <name> --image <ref>` | Convenience wrapper for `docker push` and `lenny-ctl admin runtimes register` that pushes the built image to the configured registry and registers the runtime against the target gateway. Requires `--api-url` and an admin token. | `platform-admin` (for the register step) |

**Basic-level skeleton emitted by `--language binary --template minimal`.** The `binary`/`minimal` combination deliberately emits a Basic-level-compliant skeleton with **no SDK imports** — the generated `main` file contains only the stdin/stdout JSON Lines loop from [§15.4.4](15_external-api-surface.md#1544-sample-echo-runtime) (read line → parse JSON → switch on `type` → write response + flush). No `github.com/lennylabs/runtime-sdk-go`, no `lenny-runtime` Python package, no `@lennylabs/runtime-sdk` dependency appears in the generated Dockerfile or manifest. This preserves the Basic-level "zero Lenny knowledge" promise from [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels) for authors who prefer to implement the protocol directly. Authors who want SDK conveniences (typed handlers, lifecycle channel helpers, credential-lease refresh loop) instead choose `--language {go|python|typescript}` with any template, which emits the Standard- or Full-level SDK-based skeleton.

### 24.19 Local Stack

The Embedded Mode stack ([§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)) is managed entirely through these local-only commands. They do not require a remote `LENNY_API_URL` — the embedded gateway binds to `https://localhost:8443` by default.

| Command | Description |
|---------|-------------|
| `lenny up` | Start the embedded k3s / Postgres / Redis / KMS / OIDC / gateway / controllers / reference runtimes stack. Idempotent. Prints the local gateway URL and the non-suppressible "Embedded Mode — NOT for production use" banner. |
| `lenny down [--purge]` | Gracefully terminate all embedded components. `--purge` additionally deletes `~/.lenny/`. |
| `lenny status` | Print component health, active session count, and resource usage. |
| `lenny logs [<component>] [--follow]` | Tail merged logs, or filter to one of `gateway`, `controller`, `ops`, `postgres`, `redis`, `kms`, `oidc`, `runtime-<name>`. |
| `lenny restart [<component>]` | Restart a single embedded component without tearing down the rest of the stack. |

When invoked as `lenny` (short name) these commands default to the Embedded Mode stack; invoked as `lenny-ctl <same-command>` they behave identically but are documented under the operator-tool framing.

### 24.20 Installation Wizard

The installer wizard orchestrates the flow from [§17.6](17_deployment-topology.md#176-packaging-and-installation): cluster detection → question phase → preview → preflight → `helm install` → bootstrap seed → smoke test. All detection, question rendering, and values composition logic is local; the actual install step delegates to `helm` (invoked as a subprocess) and `lenny-ctl bootstrap`.

| Command | Description | API Mapping | Min Role |
|---------|-------------|-------------|----------|
| `lenny-ctl install` | Interactive installer. Runs cluster detection, asks the question set from [§17.6](17_deployment-topology.md#176-packaging-and-installation), previews the composite values file, runs preflight, invokes `helm install`, seeds the bootstrap config, and runs a smoke test against the `chat` reference runtime. | `helm install` + `POST /v1/admin/bootstrap` + MCP session smoke test | `platform-admin` on the target cluster |
| `lenny-ctl install --non-interactive --answers <file>` | Run the installer using a pre-captured answer file (shape documented in [§17.6](17_deployment-topology.md#176-packaging-and-installation)). Used in CI/IaC. | Same as above | `platform-admin` |
| `lenny-ctl install --save-answers <file>` | Run the interactive wizard and save the answer file to `<file>` for replay. Does not run `helm install` when combined with `--dry-run`. | Local; optionally followed by the same APIs as above | `platform-admin` |
| `lenny-ctl install --offline` | Skip cluster-reachability probes in the detection phase. Preflight still runs against the target cluster. | Same as above | `platform-admin` |
| `lenny-ctl values validate --config <values.yaml>` | Validate a `values.yaml` against the Helm `values.schema.json` published by the chart ([§17.6](17_deployment-topology.md#176-packaging-and-installation)). Exits 0 on success. | Local | none |
| `lenny-ctl upgrade --answers <file>` | Replay an answer file against an existing install. Runs preflight, renders the values diff, and invokes `helm upgrade`. | `helm upgrade` + `POST /v1/admin/preflight` | `platform-admin` |
