## 17. Deployment Topology

### 17.1 Kubernetes Resources

| Component               | K8s Resource                              | Notes                                                                                                                                                      |
| ----------------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Gateway                 | Deployment + Service + Ingress            | HPA, PDB, multi-zone, topology spread. Single-container pod — the LLM Proxy subsystem and its native Go translator run inside the gateway binary ([§4.9](04_system-components.md#49-credential-leasing-service) Native translator). No sidecar, no separate container, no intra-pod loopback listener. |
| Token Service | Deployment + Service + PDB                | 2+ replicas, stateless; separate SA with KMS access; PDB `minAvailable: 1`                                                                                 |
| Warm Pool Controller    | Deployment (2+ replicas, leader election) | Manages pod lifecycle via `PoolManager` interface (default implementation: `kubernetes-sigs/agent-sandbox` CRDs)                                           |
| PoolScalingController   | Deployment (2+ replicas, leader election) | Reconciles pool config from Postgres into CRDs; manages scaling intelligence                                                                               |
| Agent Pods              | Pods owned by `Sandbox` CRD               | RuntimeClass per pool; preStop checkpoint hook for active pods; optional PDB per pool on warm (idle) pods to enforce `minWarm` during voluntary disruption |
| Postgres                | StatefulSet or managed service            | HA: primary + sync replica; connection pooling required (PgBouncer for self-managed, provider proxy for cloud-managed — see [Section 17.9](#179-deployment-answer-files))                  |
| Redis                   | StatefulSet or managed service            | HA: Sentinel (3 nodes) for self-managed, managed cache service for cloud — see [Section 17.9](#179-deployment-answer-files); TLS + AUTH required                                           |
| MinIO                   | StatefulSet or managed service            | Artifact/checkpoint storage; S3/GCS/Azure Blob for cloud-managed — see [Section 17.9](#179-deployment-answer-files)                                                                        |
| `lenny-ops`             | Deployment + Service + Ingress + PDB      | **Mandatory in every tier.** 1–2 replicas with K8s Lease leader election (`lenny-ops-leader`); leader handles webhook delivery and backup scheduling; followers serve read traffic. Runs the operability control plane ([§25.4](25_agent-operability.md#254-the-lenny-ops-service)). PDB `minAvailable: 1`. |
| `lenny-gateway-pods`    | Headless Service (`clusterIP: None`)      | Per-replica DNS so `lenny-ops` can fan out health/recommendation/event queries to every gateway replica individually ([§25.3](25_agent-operability.md#253-gateway-side-ops-endpoints)).                                                                         |
| `lenny-backup` Jobs     | K8s Jobs (created on-demand by `lenny-ops`) | Transient Jobs using the `lenny-backup` image for Postgres/MinIO backup, restore, and verification ([§25.11](25_agent-operability.md#2511-backup-and-restore-api)). Scheduled via `ops_backup_schedule`; ServiceAccount `lenny-backup-sa` (distinct from `lenny-ops-sa`). |
| NetworkPolicies         | `lenny-ops-deny-all-ingress`, `lenny-ops-allow-ingress-from-ingress-controller`, `lenny-ops-egress`, `lenny-backup-job` | Rendered by the chart ([§25.4](25_agent-operability.md#254-the-lenny-ops-service)). Deny-all-ingress is the baseline; allow-from-ingress-controller references `ingress.controllerNamespace` + `ingress.controllerLabel`. Egress allows Postgres, Redis, MinIO, Prometheus. |
| Admission / Lease       | Lease `lenny-ops-leader`                  | Created at runtime by the first `lenny-ops` pod on startup via the K8s Lease API; RBAC grants `lenny-ops-sa` permissions on `leases.coordination.k8s.io` in `{Release.Namespace}`.                                                                          |
| `ServiceMonitor` / `PodMonitor` / `PrometheusRule` (Prometheus Operator CRDs) or ConfigMap | Rendered by chart, gated by `monitoring.format` | One `ServiceMonitor` covers gateway/controller/token-service; one `PodMonitor` covers `lenny-ops`; one `PrometheusRule` emits the bundled alert catalog from `pkg/alerting/rules` ([§16.9](16_observability.md#169-prometheus-scrape-targets-and-crds), [§25.13](25_agent-operability.md#2513-bundled-alerting-rules)). Values: `prometheusrule`, `configmap`, `both`. Falls back to `configmap` when the operator CRDs are absent. |
| Secrets (chart-rendered) | `lenny-backup-postgres`, `lenny-backup-minio`; optionally `lenny-ops-tls` when `ops.tls.internalEnabled: true` | Backup connection strings (narrowly-scoped DB role; bucket-scoped access key). When cert-manager is present, a `Certificate` object renders the TLS secret automatically. |

### 17.2 Namespace Layout

```
lenny-system/         # Gateway, token service, controller, lenny-ops, stores
lenny-agents/         # Agent pods (gVisor/runc isolation boundary)
lenny-agents-kata/    # Kata pods (separate node pool with dedicated hardware)
```

**Operability-vs-agent boundary.** `lenny-ops` ([§25.4](25_agent-operability.md#254-the-lenny-ops-service)) lives in `{Release.Namespace}` (default `lenny-system`) alongside the rest of the control plane, never in the agent namespaces. Agent pods are tenant-supplied code and must not reach the operational control plane — the `lenny-ops-deny-all-ingress` NetworkPolicy and per-namespace egress defaults enforce this. Runbook examples that reference `kubectl get sandboxes -n lenny-agents` ([§25.7](25_agent-operability.md#257-operational-runbooks)) inspect the agent namespace from the operability surface; they do not execute inside it.

**Pod Security Standards:** The `lenny-agents` and `lenny-agents-kata` namespaces use a **split enforcement model** based on RuntimeClass:

- **runc (`standard`) pods:** Full Restricted PSS compliance is **enforced** via RuntimeClass-aware admission policies (OPA/Gatekeeper or Kyverno). The `seccompType: RuntimeDefault` requirement is meaningful for runc (the host kernel seccomp filter is active), and all controller-generated runc pods already satisfy Restricted PSS constraints (non-root, all caps dropped, read-only rootfs). The admission policy rejects any runc pod that does not meet Restricted PSS, ensuring non-compliant pods fail at admission rather than silently running with weaker security.
- **gVisor and Kata pods:** The admission policies apply **relaxed, RuntimeClass-specific** constraints. Restricted PSS `enforce` is unsuitable because its `seccompType: RuntimeDefault` requirement is a no-op under gVisor (gVisor intercepts syscalls in userspace, making the host seccomp profile meaningless) and conflicts with some Kata device plugins that require relaxed `allowPrivilegeEscalation` constraints. With namespace-level PSS `enforce`, non-compliant pods are silently rejected by the API server, which would cause warm pool deadlock: the controller observes a missing pod, recreates it, and the replacement is rejected again in a tight loop. Instead, gVisor pods skip the seccomp profile check while still requiring non-root, all-caps-dropped, and read-only rootfs (the controls listed in [Section 13.1](13_security-model.md#131-pod-security)). Kata pods permit the specific privilege escalation paths needed by their device plugins but enforce all other Restricted constraints.

This approach preserves the same security properties (non-root UID, all capabilities dropped, read-only root filesystem, gateway-mediated file delivery) via admission policy controllers rather than the built-in PSS enforce mode, while applying the strictest possible constraints per RuntimeClass.

**Admission policy manifests** (OPA/Gatekeeper ConstraintTemplates or Kyverno ClusterPolicies) are included in the Helm chart under `templates/admission-policies/` and deployed as part of the chart install. These policies include: (1) full Restricted PSS enforcement for runc pods, (2) RuntimeClass-specific relaxed enforcement for gVisor and Kata pods, (3) the `POD_SPEC_HOST_SHARING_FORBIDDEN` validation policy that rejects pods in agent namespaces with any of `shareProcessNamespace: true`, `hostPID: true`, `hostNetwork: true`, or `hostIPC: true` (see [Section 13.1](13_security-model.md#131-pod-security)), (4) label-based namespace targeting via `.Values.agentNamespaces`, and (5) the `lenny-label-immutability` ValidatingAdmissionWebhook that enforces immutability of the `lenny.dev/managed: "true"`, `lenny.dev/delivery-mode`, and `lenny.dev/egress-profile` labels, permitting them to be set only by the warm pool controller ServiceAccount at pod creation and denying any post-creation mutation (see NET-003 note in [Section 13.2](13_security-model.md#132-network-isolation)). An **integration test suite** (`tests/integration/admission_policy_test.go`) verifies that controller-generated pod specs for each RuntimeClass pass the deployed admission policies, preventing policy/spec drift from causing warm pool deadlock.

**Admission webhook failure mode:** All RuntimeClass-aware admission policy webhooks (OPA/Gatekeeper `ConstraintTemplate` admission controller or Kyverno admission controller) **must** be configured with `failurePolicy: Fail`. If the admission controller webhook is unavailable, pod admission is denied (fail-closed). This prevents pods from being scheduled without security constraints during webhook outages. The Helm chart configures `failurePolicy: Fail` on all admission policy `ValidatingWebhookConfiguration` objects. The admission controller deployment **must** maintain a minimum availability SLO of 99.9% (measured over a rolling 30-day window); the Helm chart deploys the admission controller with `replicas: 2` (configurable via `.Values.admissionController.replicas`) and `podDisruptionBudget.minAvailable: 1` to preserve availability during voluntary disruptions. Alert `AdmissionWebhookUnavailable` fires when the webhook has been unreachable for more than 30 seconds. Note: namespace-level PSS `enforce` cannot serve as a defense-in-depth fallback here because it cannot distinguish RuntimeClasses — gVisor and Kata pods would be incorrectly rejected (see rationale above). The `failurePolicy: Fail` + high-availability SLO combination is the primary mechanism ensuring pods cannot be admitted without security constraints during a webhook outage.

Namespace-level PSS labels remain at `warn` + `audit` (not `enforce`) because PSS enforcement is namespace-scoped and cannot distinguish RuntimeClasses — enforcement is handled by the RuntimeClass-aware admission policies above:

```
pod-security.kubernetes.io/warn: restricted
pod-security.kubernetes.io/audit: restricted
```

**Node isolation:** Kata (`microvm`) pods **must** run on dedicated node pools and **must** use hard scheduling constraints — not merely taints/tolerations — to guarantee they never share nodes with `standard` (runc) pods. A kernel compromise via an runc escape on a shared node would put co-located Kata pods at risk. The following controls are required:

1. **RuntimeClass `nodeSelector`:** The `kata-microvm` RuntimeClass definition **must** include `scheduling.nodeSelector` (e.g., `lenny.dev/node-pool: kata`). Any pod that references this RuntimeClass is automatically constrained to matching nodes at admission time, with no additional pod-level configuration needed.
2. **Hard node affinity:** As a defense-in-depth measure, the controller **must** inject a `requiredDuringSchedulingIgnoredDuringExecution` node affinity rule on every Kata pod, matching the same `lenny.dev/node-pool: kata` label. This ensures scheduling fails rather than falling back to an unsuitable node. Note: `IgnoredDuringExecution` is the strongest node affinity semantic Kubernetes offers — there is no `RequiredDuringExecution` variant. If a node's label is removed after scheduling, the pod is not evicted. However, the dedicated-node taint (control 3 below) independently prevents non-Kata workloads from scheduling onto Kata nodes regardless of label state, so label drift alone cannot cause Kata and runc pods to share a node.
3. **Dedicated-node taint:** Kata node pools **must** carry the taint `lenny.dev/isolation=kata:NoSchedule`. Only pods with the corresponding toleration (added automatically by the RuntimeClass or controller) can schedule onto these nodes, preventing non-Kata workloads from landing on Kata-dedicated hardware.

**Resource governance:** Each agent namespace (`lenny-agents`, `lenny-agents-kata`) includes a `ResourceQuota` and a `LimitRange` deployed by the Helm chart. These prevent runaway pod creation (e.g., from a controller bug or misconfigured `minWarm`) from exhausting cluster resources.

- **ResourceQuota** (configurable via `.Values.agentNamespaces[].resourceQuota`): caps total pods, aggregate CPU requests, and aggregate memory requests per namespace. Default values are derived from the expected warm pool size for the namespace's tier (see [Section 17.8](#178-capacity-planning-and-defaults)) with a safety margin (2x the maximum pool size). Example defaults for the `lenny-agents` namespace: `pods: 200`, `requests.cpu: "400"`, `requests.memory: "800Gi"`. Operators **must** tune these values when configuring large `minWarm` pools — if the quota is lower than the pool's target size, the warm pool controller will be unable to create pods and will emit `pool_quota_exhausted` warning events.
- **LimitRange** (configurable via `.Values.agentNamespaces[].limitRange`): sets default resource requests and limits for containers in agent pods, ensuring no pod is scheduled as BestEffort QoS class. Default container values: `defaultRequest.cpu: "250m"`, `defaultRequest.memory: "256Mi"`, `default.cpu: "2"`, `default.memory: "2Gi"`. These defaults apply only to containers that do not specify their own resource requirements — controller-generated pods already include explicit resource requests ([Section 17.8](#178-capacity-planning-and-defaults)), so the LimitRange acts as a safety net for any manually created or misconfigured pods.

The `lenny-preflight` Job validates that both `ResourceQuota` and `LimitRange` exist in each agent namespace and that the quota's pod limit is at least as large as the sum of `minWarm` across all pools targeting that namespace. If the check fails, the preflight Job reports: `"ResourceQuota in namespace '<ns>' allows <n> pods but configured pools require at least <m>; increase agentNamespaces[].resourceQuota.pods"`.

### 17.3 Disaster Recovery

**RPO/RTO targets:**

| Component                        | RPO                                                                                  | RTO                                                                  |
| -------------------------------- | ------------------------------------------------------------------------------------ | -------------------------------------------------------------------- |
| Postgres (session state, tokens) | 0 (sync replication)                                                                 | < 30s (auto failover)                                                |
| Redis (cache, leases)            | Ephemeral — rebuild from Postgres                                                    | < 15s (Sentinel failover)                                            |
| MinIO (artifacts, checkpoints)   | Near-zero (erasure coding + site replication) or last backup (daily) for single-site | < 30s (surviving nodes serve reads); < 5 min (full node replacement) |

**Cross-zone requirements:**

- Postgres: primary and sync replica in different availability zones
- Redis: Sentinel nodes spread across zones
- Gateway: replicas spread via topology spread constraints
- Agent pods: spread via pool-level topology constraints (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes))

**Backup schedule:**

- Postgres: continuous WAL archival + daily base backups to object storage
- MinIO: daily bucket replication or backup
- Restore testing recommended monthly (configurable by deployer via CronJob schedule) via `lenny-restore-test` CronJob that: (1) creates a temporary Postgres instance from the latest base backup + WAL, (2) verifies schema integrity and row counts against the primary, (3) runs a smoke query (e.g., list recent sessions), (4) records elapsed restore time and emits `lenny_restore_test_success` / `lenny_restore_test_duration_seconds` metrics, and (5) tears down the test instance. Alert if measured RTO exceeds targets (< 30s Postgres, < 5min MinIO). MinIO restore is validated in the same job via a test bucket restore and object checksum comparison.

**Zone failure blast radius:** Loss of one zone causes:

- Gateway: surviving replicas absorb traffic (PDB ensures minimum availability)
- Postgres: automatic failover to sync replica in another zone
- Agent pods: sessions on lost pods enter retry flow; warm pods in surviving zones serve new requests
- No data loss for committed transactions

**Agent-operability recovery paths.** The prose above is the human-operator summary. [Section 25.11](25_agent-operability.md#2511-backup-and-restore-api) (Backup and Restore API) and [Section 25.15](25_agent-operability.md#2515-failure-mode-analysis) (Total-Outage Recovery) are the canonical API-driven and break-glass recovery paths used by autonomous agents — both for routine DR verification and for operator-led total-outage recovery. 25.11's tier-parameterized RTO/RPO targets take precedence over the table above and MUST be honored when planning capacity for higher tiers.

### 17.4 Local Development Mode (`lenny-dev`)

For local use Lenny provides a **three-tier local mode**. Tier 0 is the primary path for deployers evaluating or using Lenny on a workstation; Tier 1 and Tier 2 are developer-oriented paths for contributors working on Lenny itself or authoring runtime adapters.

#### Tier 0: `lenny up` — Single-binary embedded stack

```
lenny up                                  # Brings up a full Lenny stack on localhost
lenny session new --runtime=chat --attach "hello"    # Ready to use in < 60s
lenny down                                 # Tear everything down
```

A single statically-linked binary — `lenny` — embeds every dependency needed to run a complete Lenny installation on one host. No Kubernetes cluster, no Postgres operator, no cert-manager, no OIDC provider required beforehand. Intended audience: operators evaluating Lenny, developers building **against** Lenny (workload authors and runtime authors), and anyone who wants a functioning deployment on their laptop.

**Embedded components.** The binary ships with:

| Dependency     | Embedded option                                                  | Notes                                                                     |
|----------------|------------------------------------------------------------------|---------------------------------------------------------------------------|
| Kubernetes     | [k3s](https://k3s.io) (single-node, rootless where supported)    | Downloaded on first `lenny up` into `~/.lenny/k3s/` and started in-process |
| Postgres       | `embedded-postgres` (PostgreSQL 16 binary bundle)                | `~/.lenny/postgres/`; same Go storage interface as production              |
| Redis          | Embedded `miniredis`-compatible implementation                   | In-process; lost on `lenny down`                                          |
| KMS            | In-process soft-HSM (AES-256-GCM with a file-backed master key)  | `~/.lenny/kms/master.key`; operators MUST NOT reuse this key in production |
| OIDC provider  | Embedded dev-only provider issuing short-lived JWTs              | Single built-in user; rotating signing key                                 |
| Object storage | Local filesystem (`~/.lenny/artifacts/`)                         | Same artifact-store interface as MinIO/S3                                  |
| TLS            | Self-signed certs rotated per `lenny up` (valid for 24h)         | Gateway listens on `https://localhost:8443` and `http://localhost:8080`     |

**Same platform code path as production.** Tier 0 uses the production gateway, controllers, CRDs, and storage interfaces. Only the driver selection differs: `mode=embedded` is signaled by a platform flag that the storage, KMS, and identity interfaces consume to pick their embedded backends. There are no tier-dependent code splits in business logic.

**Reference runtimes pre-installed.** `lenny up` installs all reference runtimes from [Section 26](26_reference-runtime-catalog.md) as platform-global records and auto-grants access to the `default` tenant so the developer can invoke any of them without further configuration. Container images are pulled lazily on first session start for each runtime; subsequent sessions reuse the cached image. The warm pool defaults are overridden to `warmCount: 0` (cold-start on first use) to keep resource usage low on laptops.

**Command surface.**

| Command               | Behavior                                                                                                    |
|-----------------------|-------------------------------------------------------------------------------------------------------------|
| `lenny up`            | Starts the embedded stack. Idempotent — subsequent invocations are no-ops if already running.              |
| `lenny down`          | Gracefully terminates all components. State under `~/.lenny/` is preserved unless `--purge` is passed.      |
| `lenny status`        | Prints component health and active session count.                                                           |
| `lenny logs [<component>]` | Tails merged logs or filters to one component (`gateway`, `controller`, `ops`, `postgres`, etc.).          |
| `lenny session ...`   | Session CLI ([§24.17](24_lenny-ctl-command-reference.md#2417-session-operations)); targets the local stack. |

**Production warning banner.** On every `lenny up` the binary prints a prominent, non-suppressible banner: `"Tier 0 embedded mode. NOT for production use. Credentials, KMS master key, and identities are insecure."` The embedded OIDC provider refuses any audience claim not matching `dev.local`; the gateway rejects externally-issued tokens. Any attempt to expose the gateway outside localhost (e.g., by binding `0.0.0.0`) fails closed with `EMBEDDED_MODE_LOCAL_ONLY`.

**State and resets.**

- `~/.lenny/` is the sole state directory. `lenny down --purge` removes it.
- Upgrades: `lenny up` on a newer binary runs the standard schema migration path ([§10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy)) against the embedded Postgres. Rollback is **not** supported in Tier 0 — the user is expected to `lenny down --purge` and start fresh if they need to revert.

**Binary-vs-symlink.** The `lenny` binary is the same executable as `lenny-ctl` ([§24](24_lenny-ctl-command-reference.md#24-lenny-ctl-command-reference)) installed under a short name. When invoked as `lenny`, the binary defaults to the Tier 0 ergonomics (local stack, no `--api-url` required); when invoked as `lenny-ctl`, it targets a remote gateway (`--api-url` required). Every command is available under both names; docs use the short form in local/developer contexts and the long form in operator contexts.

#### Tier 1: `make run` — Zero-dependency developer mode

```
make run   # Starts: gateway + controller-sim + single agent container (single binary)
```

A developer-oriented local entry point for **contributing to the Lenny platform itself**. Unlike Tier 0 (which uses the released `lenny` binary and embedded k3s), Tier 1 runs the Lenny source tree directly without Kubernetes:

- **Embedded SQLite** replaces Postgres for session and metadata storage
- **In-memory caches** replace Redis for pub/sub and ephemeral state
- **Local filesystem directory** (`./lenny-data/`) replaces MinIO for artifact storage
- Gateway, controller-sim, and a single agent container run as goroutines in one process

No Postgres, Redis, MinIO, Kubernetes, or Docker required. Suitable for:

- Lenny platform contributors iterating on gateway or controller code
- Runtime adapter authors testing their adapter against the gateway contract without full pod scheduling
- First-time contributors getting oriented with the codebase
- CI test jobs that need the full platform surface without cluster provisioning

Deployers evaluating Lenny as an end user should prefer Tier 0 (`lenny up`) — it exercises the real Kubernetes code path and installs reference runtimes.

#### Tier 2: `docker compose up` — Full local stack

```
docker compose up   # Starts: gateway, controller-sim, single agent pod, Postgres, Redis, MinIO
```

Production-like local environment with real infrastructure dependencies:

- Gateway: single replica, no HPA, no mTLS (plain HTTP by default — **unsupported for TLS-related development**; use `make compose-tls` instead)
- Controller simulator: manages a single "pod" (Docker container) instead of CRDs
- Stores: real Postgres + Redis (lightweight containers)
- MinIO: single container for artifact storage
- Agent pod: single Docker container with runtime adapter + agent binary

> **Warning — plain HTTP and real credentials.** The default Tier 2 profile transmits all traffic — including LLM provider API keys injected via credential pools — over plain HTTP between the gateway and agent containers. **Do not configure real LLM credentials in docker-compose unless TLS is enabled** (see credential-testing profile below). This applies to both interactive development and CI environments that run integration tests with real API keys.

Suitable for:

- Lenny core developers iterating on gateway/controller logic
- Integration testing against real storage backends
- CI integration tests
- Production-like local environment validation

#### Credential-testing profile

When testing with real LLM provider credentials locally, or when exercising the mTLS code path, use the `credentials` docker-compose profile via the `make compose-tls` shorthand, which enables TLS by default:

```
make compose-tls                          # Alias for: docker compose --profile credentials up
docker compose --profile credentials up   # Enables LENNY_DEV_TLS=true automatically
```

This profile sets `LENNY_DEV_TLS=true` (which requires `LENNY_DEV_MODE=true`, already set in all dev profiles) and generates self-signed mTLS certificates on first run. All gateway-to-agent traffic is encrypted. Use this profile whenever real API keys are configured in credential pool definitions, and whenever developing or testing TLS-related logic — the plain-HTTP default (`docker compose up`) does not exercise the mTLS code path and must not be used for TLS-related development.

**Self-signed certificate trust setup.** When `LENNY_DEV_TLS=true` is active, the gateway generates a self-signed CA and leaf certificates in `./lenny-data/certs/`. To allow API clients (CLI tools, test harnesses, CI scripts) to verify the gateway's TLS certificate:

1. **macOS:** `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ./lenny-data/certs/ca.crt`
2. **Linux:** `sudo cp ./lenny-data/certs/ca.crt /usr/local/share/ca-certificates/lenny-dev-ca.crt && sudo update-ca-certificates`
3. **Per-process (any OS):** Set `SSL_CERT_FILE=./lenny-data/certs/ca.crt` or use `--cacert ./lenny-data/certs/ca.crt` with curl.
4. **CI environments:** Option 3 (per-process) is recommended to avoid modifying system trust stores in CI runners.

The certificates are regenerated if deleted; no manual key management is required.

#### Observability in dev mode

Tier 2 (`docker compose up`) includes optional observability containers: Prometheus (metrics scraping), Grafana (pre-built Lenny dashboard), and Jaeger (distributed tracing). Enable with `docker compose --profile observability up`. Tier 1 (`make run`) outputs traces to stdout and exposes Prometheus metrics on `:9090/metrics`.

#### Zero-credential mode

In both tiers, the gateway can operate without LLM provider credentials by using a **built-in echo/mock agent runtime** that does not require an LLM provider. The echo runtime replays deterministic responses, allowing contributors to test platform mechanics (session lifecycle, workspace materialization) without providing any API keys. This is the default runtime in Tier 1 and can be selected explicitly in Tier 2 via `LENNY_AGENT_RUNTIME=echo`. Note: the echo runtime cannot invoke MCP tools; delegation flow testing requires the `delegation-echo` test runtime introduced in Phase 9 ([Section 18](18_build-sequence.md)), which executes scripted tool call sequences including `lenny/delegate_task`.

#### Dev mode guard rails

Dev mode relaxes security defaults (TLS, JWT signing) for local convenience, but hard guard rails prevent accidental use outside development:

1. **Hard startup assertion:** The gateway **refuses to start** with TLS disabled unless the environment variable `LENNY_DEV_MODE=true` is explicitly set. Any other value, or absence of the variable, causes an immediate fatal error at startup. This ensures a misconfigured staging or production deployment cannot silently run without encryption.
2. **Prominent startup warning:** When `LENNY_DEV_MODE=true` is set, the gateway logs at `WARN` level on every startup: `"WARNING: TLS disabled — dev mode active. Do not use in production."` The warning is repeated every 60 seconds while the process is running.
3. **Unified security-relaxation gate:** The `LENNY_DEV_MODE` flag is the single gate for all security relaxations in dev mode, including TLS bypass, JWT signing bypass, and any future relaxations. No individual security feature can be disabled independently without this flag.

Setting `LENNY_DEV_TLS=true` (requires `LENNY_DEV_MODE=true`) enables self-signed mTLS certificates that are auto-generated on first run. This is **required when testing with real LLM credentials** in Tier 2 (use the `credentials` docker-compose profile, which sets this automatically) and is also useful for adapter authors testing certificate validation, rotation, and error handling without a full cert-manager setup. See "Credential-testing profile" and "Self-signed certificate trust setup" above for details.

#### Smoke test

Both dev mode tiers include a built-in smoke test: `make test-smoke` (Tier 1) or `docker compose run smoke-test` (Tier 2) creates a session with the echo runtime, sends a prompt, verifies a response, and exits. This validates the entire pipeline (gateway, controller-sim, runtime adapter, agent binary) in under 10 seconds.

#### Plugging in a custom runtime

After reading the echo runtime sample ([Section 15.4.4](15_external-api-surface.md#1544-sample-echo-runtime)), runtime authors can substitute their own binary in either dev tier:

**Tier 1 (`make run`) — override the agent binary path:**

```
make run LENNY_AGENT_BINARY=/path/to/my-agent-binary
```

The controller-sim spawns the specified binary as a single agent container. The binary must implement the stdin/stdout JSON Lines protocol ([Section 15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol)). No runtime registration is required in Tier 1 — the binary is used directly.

**Tier 2 (`docker compose up`) — register a custom runtime and point to your binary:**

```bash
# 1. Build your runtime image
docker build -t my-agent:dev .

# 2. Register the runtime via the admin API (after stack is up)
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{"name": "my-agent", "type": "agent", "image": "my-agent:dev"}'

# 3. Start a session using your runtime
LENNY_AGENT_RUNTIME=my-agent docker compose up
```

Alternatively, add your runtime to the bootstrap seed file (`lenny-data/seed.yaml`) and restart. The controller-sim picks up the registered runtime on next pool warm cycle. The seed file is applied idempotently on every `docker compose up`.

> **macOS note:** `make run` (Tier 1) supports macOS for Basic-level runtimes (stdin/stdout binary protocol only). Standard- and Full-level runtimes require abstract Unix sockets (`@` prefix names), which are **Linux-only** — macOS does not support abstract sockets. If you are developing a Standard- or Full-level runtime on macOS, use `docker compose up` (Tier 2) instead, which runs the adapter inside a Linux container. See [Section 15.4.3](15_external-api-surface.md#1543-runtime-integration-levels) for level definitions.

### 17.5 Cloud Portability

The design avoids baking in cloud-specific assumptions:

- Storage backends are pluggable
- Network policies are standard Kubernetes
- RuntimeClass works with any conformant runtime
- No cloud-specific CRDs required

### 17.6 Packaging and Installation

**Helm chart** is the primary installation mechanism for the platform. The chart packages all Lenny components: gateway, token service, warm pool controller, CRD definitions, RBAC, NetworkPolicies, admission policies (OPA/Gatekeeper or Kyverno manifests per [Section 17.2](#172-namespace-layout)), and cert-manager resources. The `lenny-ctl` CLI is published separately through two release channels ([§24](24_lenny-ctl-command-reference.md#240-packaging-and-installation)): signed standalone binaries (`lenny-ctl`) and a [krew](https://krew.sigs.k8s.io/) kubectl plugin (`kubectl-lenny`). Both forms are produced from the same tagged release. The release automation opens a `krew-index` pull request with the updated `plugin.yaml` (platform binaries + SHA-256 checksums) on every tag.

> **Krew binary-naming contract.** `kubectl` discovers plugins by searching `$PATH` for binaries whose filename matches `kubectl-<plugin_name>`. The krew-published artifact MUST therefore be a file literally named `kubectl-lenny` — not a symlink to `lenny-ctl` and not `lenny-ctl` copied verbatim — because `kubectl lenny <subcommand>` looks up the fixed filename. The release automation produces two separate binaries from the same build: (1) `lenny-ctl`, distributed via the standalone archive and Homebrew, and (2) `kubectl-lenny`, referenced in the krew `plugin.yaml`. CI verifies the invariant after every release by running `kubectl krew install lenny` against a disposable kind cluster and asserting that `kubectl lenny --version` succeeds and reports the release tag.

Key Helm values:

- `global.devMode` — enables `LENNY_DEV_MODE` for local development
- `gateway.replicas` — gateway replica count
- `pools` — array of warm pool configurations (runtime, size, resource limits)
- `agentNamespaces[].resourceQuota` — per-namespace ResourceQuota overrides (pods, CPU, memory caps)
- `agentNamespaces[].limitRange` — per-namespace LimitRange overrides (default container requests/limits)
- `postgres.connectionString` — Postgres DSN
- `redis.connectionString` — Redis DSN
- `minio.endpoint` — object storage endpoint

CRDs are installed via the chart on initial `helm install` but can be managed separately for GitOps workflows (`helm install --skip-crds` combined with external CRD management).

**CRD upgrade procedure (required — read this before every upgrade).** Helm does not update CRDs on `helm upgrade`. This is a known Helm limitation that causes silent production incidents if CRDs become stale (new fields are stripped by the API server, controllers observe unexpected defaults). **Every Lenny upgrade requires CRD application as a separate step.** The required upgrade sequence is:

1. **Apply CRDs first:** `kubectl apply -f charts/lenny/crds/` (or the equivalent from the release tarball). This updates the CRD schemas in the cluster before any controller code changes.
2. **Run `helm upgrade`:** Proceed with the normal Helm upgrade. Controllers validate the installed CRD schema version on startup (see [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy)) and will refuse to start if CRDs are stale.
3. **GitOps workflows:** When using ArgoCD or Flux, configure CRD manifests as a separate sync wave (e.g., ArgoCD `sync-wave: "-5"`) that applies before the main chart resources. **This is not optional** — without a separate sync wave, ArgoCD/Flux will apply controllers and CRDs in arbitrary order, which can produce the same stale-CRD failure.

The `lenny-preflight` Job (above) includes a CRD version check: it compares the `lenny.dev/schema-version` annotation on each installed CRD against the expected version for the chart release. If any CRD is stale, the preflight Job fails with: `"CRD '<name>' schema version is '<installed>'; expected '<expected>'. Apply updated CRDs before running helm upgrade."`

**Helm post-upgrade CRD validation hook.** As a defense-in-depth measure, the Helm chart includes a `lenny-crd-validate` Job (`helm.sh/hook: post-upgrade`, `helm.sh/hook-weight: "-5"`) that runs after `helm upgrade` completes. The Job verifies that every Lenny CRD in the cluster carries the expected `lenny.dev/schema-version` annotation for the chart release. If any CRD is stale, the Job fails with a clear message: `"Post-upgrade CRD validation failed: CRD '<name>' has schema version '<installed>', expected '<expected>'. Run: kubectl apply -f charts/lenny/crds/ && kubectl rollout restart deployment -l app.kubernetes.io/part-of=lenny -n <namespace>. See docs/runbooks/crd-upgrade.md."` This catch-net ensures that even if preflight was skipped (`preflight.enabled: false`) or the operator applied CRDs from an incorrect release, the failure is surfaced immediately rather than manifesting as silent data loss.

**Recovery procedure for stale CRDs after a failed upgrade.** If `helm upgrade` was run without applying CRDs first, or with CRDs from the wrong release version, the following recovery procedure applies:

1. **Symptom identification.** One or more of: (a) controller pods are in `CrashLoopBackOff` with `FATAL: CRD schema version mismatch` in logs, (b) the `lenny-preflight` Job failed with a CRD version mismatch message, (c) the `lenny-crd-validate` post-upgrade hook failed.
2. **Apply the correct CRDs.** Identify the exact chart version that was deployed (`helm list -n <namespace>` shows the chart version). Apply CRDs from that release: `kubectl apply -f charts/lenny/crds/` (from the matching release tarball or git tag). Verify with: `kubectl get crd agentsessions.lenny.dev -o jsonpath='{.metadata.annotations.lenny\.dev/schema-version}'`.
3. **Restart affected controllers.** After CRDs are applied, restart the controllers so they pick up the corrected CRD schemas: `kubectl rollout restart deployment -l app.kubernetes.io/part-of=lenny -n <namespace>`. Wait for rollout to complete: `kubectl rollout status deployment -l app.kubernetes.io/part-of=lenny -n <namespace> --timeout=120s`.
4. **Verify recovery.** Confirm controllers are running: `kubectl get pods -l app.kubernetes.io/part-of=lenny -n <namespace>`. Confirm no CRD mismatch errors in logs: `kubectl logs -l app.kubernetes.io/part-of=lenny -n <namespace> --tail=50 | grep -i "schema version"`. Run the preflight check manually: `lenny-ctl preflight --config <values.yaml>`.
5. **If recovery fails.** If controllers still fail after applying CRDs and restarting, roll back the entire Helm release: `helm rollback <release> <previous-revision> -n <namespace>`, then apply CRDs for the previous version and coordinate a fresh upgrade attempt following the correct procedure above.

**Bootstrap seed mechanism.** After `helm install`, Postgres is empty — no runtimes, pools, tenants, or credentials exist. Lenny provides an idempotent bootstrap mechanism to seed Day-1 configuration:

1. **Helm values: `bootstrap` section.** The chart includes a `bootstrap` values block defining seed resources:

```yaml
bootstrap:
  enabled: true # default: true
  # Seed resources — all optional, all idempotent (upsert by name).
  tenant:
    name: "default"
    displayName: "Default Tenant"
  runtimes: [] # array of Runtime definitions (same schema as POST /v1/admin/runtimes); each may include tenantAccess: ["<tenantId>", ...]
  pools: [] # array of Pool definitions (same schema as POST /v1/admin/pools); each may include tenantAccess: ["<tenantId>", ...]
  credentialPools: [] # array of CredentialPool definitions (tenant-scoped; assigned to the bootstrap tenant above)
  delegationPolicies: [] # array of DelegationPolicy definitions
  environments: [] # array of Environment definitions
```

2. **Init Job: `lenny-bootstrap`.** The Helm chart includes a Kubernetes `Job` (with `helm.sh/hook: post-install,post-upgrade` and `helm.sh/hook-weight: "10"`) that runs after the gateway and database migrations are ready. The Job executes `lenny-ctl bootstrap --from-values /etc/lenny/bootstrap-values.yaml` against the admin API. The bootstrap values ConfigMap is rendered from the `bootstrap` Helm section.

3. **`lenny-ctl bootstrap` CLI command.** The CLI command reads a seed file (YAML, same schema as the Helm `bootstrap` section) and applies each resource via the admin API using upsert semantics (create if absent, skip or update if present with matching name). Behavior:
   - **Idempotent**: safe to run multiple times. Existing resources with matching names are left unchanged unless `--force-update` is passed, in which case they are updated to match the seed file.
   - **Dry-run**: `lenny-ctl bootstrap --dry-run` validates the seed file and reports what would be created/updated without making changes.
   - **Exit codes**: 0 = success (all resources seeded), 1 = validation error, 2 = partial failure (some resources failed, others succeeded — log details which).
   - **Waits for readiness**: the command polls `GET /healthz` on the gateway before applying seeds, with a configurable timeout (`--wait-timeout`, default 120s).

4. **What gets seeded.** The minimum Day-1 seed for a functional deployment:

   | Resource             | Purpose                                                      | Required?                                                 |
   | -------------------- | ------------------------------------------------------------ | --------------------------------------------------------- |
   | Default tenant       | Tenant for initial users                                     | Yes (one tenant required for any API call)                |
   | At least one Runtime | Defines an agent runtime (e.g., echo runtime for smoke test) | Yes (sessions require a runtime)                          |
   | At least one Pool    | Pre-warms pods for the registered runtime                    | Yes (sessions require warm pods)                          |
   | Credential pool      | LLM provider credentials                                     | No (only for real LLM providers; echo runtime needs none) |
   | Delegation policy    | Controls delegation behavior                                 | No (default-deny is safe)                                 |
   | Environment          | Groups runtimes for teams                                    | No (optional organizational construct)                    |
   | Tenant RBAC config   | Sets `noEnvironmentPolicy` for the default tenant            | **Yes if no environments are seeded** (see note below)    |

   > **`noEnvironmentPolicy` bootstrap requirement.** The platform default for `noEnvironmentPolicy` is `deny-all` ([Section 4.2](04_system-components.md#42-session-manager)). Under `deny-all`, any authenticated user who is not a member of at least one environment cannot access any runtime. The full Environment resource (tag-based selectors, member RBAC, `crossEnvironmentDelegation`) is not available until Phase 15. Therefore, **pre-Phase 15 deployments must resolve the access gap by one of the following two approaches in the bootstrap seed:**
   >
   > - **Option A (recommended for development/single-tenant):** Set `noEnvironmentPolicy: allow-all` on the default tenant via the bootstrap seed `rbacConfig` field. This grants all authenticated users in the tenant unrestricted access to all tenant-owned runtimes with no environment membership required. Do not use this in production multi-tenant deployments (see the security warning in [Section 4.2](04_system-components.md#42-session-manager)).
   > - **Option B (recommended for multi-tenant/production pre-Phase 15):** Seed at least one environment that includes all initial users as members (using the `environments` array in the bootstrap seed). Users without environment membership will still be denied. Any user not covered by an environment must be added manually until Phase 15 introduces full environment management.
   >
   > Failure to apply one of these options results in all regular users (`user` platform role) receiving `403 FORBIDDEN` on all runtime access attempts from Phase 5 onward. `platform-admin` and `tenant-admin` roles are not affected because their RBAC allows direct admin API access independent of `noEnvironmentPolicy`.

   The chart ships with a commented-out example seed configuration for a complete deployment with the echo runtime, one pool of 2 warm pods, and the default tenant.

5. **Upsert semantics — full specification.** Every resource type applied by `lenny-ctl bootstrap` uses upsert semantics based on the resource's `name` field (or `id` for tenants). The upsert rules are:

   | Condition | Default behavior (`--force-update` absent) | With `--force-update` |
   |---|---|---|
   | Resource does not exist | **Create** — full resource is created from the seed file. Exit code: 0. | Same — create. |
   | Resource exists with identical fields | **No-op** — resource is left unchanged. Exit code: 0. Logged at `INFO` level: `"resource <type>/<name>: no changes"`. | Same — no-op. |
   | Resource exists with differing fields | **Skip** — resource is left unchanged. Exit code: 0. Logged at `WARN` level: `"resource <type>/<name>: exists with differing fields; skipping (use --force-update to overwrite)"`. The `details.conflictingFields` list is emitted in the log. | **Update** — full resource is replaced with the seed file definition using a PUT (with `If-Match: *` to accept any current version). Exit code: 0. |
   | Resource exists and `--force-update` would overwrite a security-critical field (e.g., tenant `id`, runtime `isolationProfile`) | **Error** — operation is blocked regardless of `--force-update`. Exit code: 1. | **Error** — blocked. |

   Running the bootstrap Job twice on a clean cluster is safe (second run is a complete no-op). Running `helm upgrade` with changed bootstrap values uses the skip-by-default behavior — operators must explicitly pass `--force-update` to apply changed seed values to already-existing resources.

6. **Initial admin credential — Kubernetes Secret handling.** The bootstrap Job creates an initial `platform-admin` user (username: `lenny-admin`) with a generated API token. The token is written to a Kubernetes Secret `lenny-system/lenny-admin-token` with the following properties:

   - **Created by the bootstrap Job** using `kubectl create secret` with `--dry-run=client -o yaml | kubectl apply -f -`, which is idempotent: if the Secret already exists (re-run scenario), the `apply` is a no-op. The token is **not regenerated** on re-run — the existing Secret's token is preserved. This ensures that existing integrations (CI pipelines, dashboards) do not break on `helm upgrade`.
   - **Secret shape:**
     ```yaml
     apiVersion: v1
     kind: Secret
     metadata:
       name: lenny-admin-token
       namespace: lenny-system
       labels:
         app.kubernetes.io/managed-by: lenny-bootstrap
     type: Opaque
     data:
       token: <base64-encoded API token>
       created_at: <base64-encoded RFC3339 timestamp>
     ```
   - **Rotation procedure:** To rotate the initial admin token: (1) generate a new token: `lenny-ctl admin users rotate-token --user lenny-admin`; (2) the CLI updates the user record in Postgres and patches the `lenny-admin-token` Secret with the new token value; (3) the old token is immediately invalidated (not a grace period). Any systems using the old token must be updated to read from the Secret.
   - **First-use prompt:** `lenny-ctl bootstrap` prints a post-run message: `"Initial admin token written to Secret lenny-system/lenny-admin-token. Retrieve with: kubectl get secret lenny-admin-token -n lenny-system -o jsonpath='{.data.token}' | base64 -d"`. This is printed only on the first run (when the Secret is newly created); re-runs print `"Admin token Secret already exists — no changes."`.
   - **Security note:** The `lenny-admin-token` Secret is accessible to any principal with `get` access to Secrets in `lenny-system`. RBAC for this namespace must restrict Secret access appropriately (see [Section 17.2](#172-namespace-layout) for namespace RBAC configuration). The bootstrap Job's ServiceAccount has `create`/`get`/`patch` on Secrets in `lenny-system` but no broader access.

7. **Build sequence integration.** The bootstrap Job is part of Phase 4.5 (Admin API foundation) — it depends on the admin API endpoints being available.

**Preflight validation: `lenny-preflight` Job.** Missing or misconfigured infrastructure dependencies (wrong PgBouncer pool mode, absent CNI plugin, missing RuntimeClasses) cause cryptic failures that are difficult to diagnose after installation. The Helm chart includes a `lenny-preflight` Job (`helm.sh/hook: pre-install,pre-upgrade`, `helm.sh/hook-weight: "-10"`) that validates all infrastructure prerequisites before any Lenny component is deployed. The Job runs to completion and blocks the install/upgrade if any check fails.

#### Checks performed

| Check                                 | Validation                                                                                                                                                                                                                                                                                   | Failure Message                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Postgres connectivity                 | Connect to `postgres.connectionString`, execute `SELECT 1`                                                                                                                                                                                                                                   | `Postgres unreachable at <DSN>`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| Postgres version                      | Verify server version ≥ 14                                                                                                                                                                                                                                                                   | `Postgres version <ver> unsupported; minimum 14 required`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| PgBouncer pool mode                   | Query PgBouncer `SHOW CONFIG` and verify `pool_mode = transaction`                                                                                                                                                                                                                           | `PgBouncer pool_mode is '<mode>'; must be 'transaction' for RLS enforcement (Section 12.3)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| PgBouncer connect_query               | Verify `connect_query` contains `SET app.current_tenant` sentinel                                                                                                                                                                                                                            | `PgBouncer connect_query missing tenant sentinel; see Section 12.3`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| Cloud-managed pooler sentinel defense | When `postgres.connectionPooler = external`: connect to Postgres, verify the per-transaction tenant validation trigger exists on tenant-scoped tables (query `pg_trigger` for the `lenny_tenant_guard` trigger). If absent, fail.                                                            | `Cloud-managed pooler detected but per-transaction tenant validation trigger 'lenny_tenant_guard' not found; required because cloud-managed proxies cannot enforce the __unset__ sentinel via connect_query — see Section 12.3`                                                                                                                                                                                                                                                                                                                                          |
| Billing/audit trigger enabled         | Query `pg_trigger` for `lenny_billing_immutability` and `lenny_audit_immutability` and verify `tgenabled != 'D'` (not disabled). A superuser can `ALTER TABLE ... DISABLE TRIGGER` to bypass immutability controls without revoking grants. | `Integrity trigger '<name>' is disabled (tgenabled=D) on table '<table>'. A superuser has bypassed billing/audit immutability controls. Re-enable with: ALTER TABLE <table> ENABLE TRIGGER <name>; and investigate (Section 11.2.1, 11.7).` |
| Redis connectivity                    | Connect to `redis.connectionString`, execute `PING`                                                                                                                                                                                                                                          | `Redis unreachable at <DSN>`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Redis AUTH / TLS                      | Verify AUTH succeeds and TLS handshake completes                                                                                                                                                                                                                                             | `Redis AUTH or TLS failed; both are required (Section 12.4)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| MinIO connectivity                    | Connect to `minio.endpoint`, verify bucket access                                                                                                                                                                                                                                            | `MinIO unreachable at <endpoint>`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| MinIO encryption                      | Verify server-side encryption is enabled on the target bucket                                                                                                                                                                                                                                | `MinIO SSE not enabled; required for production (Section 12.5)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| Cloud object storage lifecycle rules  | When `objectStorage.provider` is `s3`, `gcs`, or `azure`: verify (a) bucket versioning is enabled and (b) lifecycle rules exist that expire noncurrent versions within ≤ 7 days and expire delete markers (S3/S3-compatible) within ≤ 7 days. Uses provider SDK calls: S3 `GetBucketVersioning` + `GetBucketLifecycleConfiguration`; GCS `storage.buckets.get` (lifecycle field); Azure `BlobServiceProperties.IsVersioningEnabled` + `ManagementPolicy` GET. Skipped when `objectStorage.provider = minio` (MinIO lifecycle is configured by the post-install Job). | `Cloud object storage bucket '<bucket>' is missing required lifecycle rules: <detail>. Configure versioning and lifecycle rules before installing Lenny — see Section 17.9 (Cloud Object Storage Lifecycle Requirements).` |
| RuntimeClasses                        | For each pool in `.Values.bootstrap.pools`, verify the referenced `RuntimeClass` exists in the cluster                                                                                                                                                                                                 | `RuntimeClass '<name>' not found; required by pool '<pool>'`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Agent-sandbox CRDs                    | Verify all four `agent-sandbox` CRDs are installed and at the expected API version: `sandboxtemplates.lenny.dev`, `sandboxwarmpools.lenny.dev`, `sandboxes.lenny.dev`, `sandboxclaims.lenny.dev`. For each CRD, checks: (a) CRD exists in the cluster, (b) `spec.versions[*].name` includes the expected version from `charts/lenny/crds/`. A deployment that omits the CRD apply step fails here with a clear error rather than opaque reconciliation errors later. | `agent-sandbox CRD '<name>' not found — apply CRDs before installing Lenny (kubectl apply -f charts/lenny/crds/)` / `CRD '<name>' missing version '<ver>' — CRDs are stale; apply updated CRDs before running helm upgrade` |
| cert-manager                          | Verify cert-manager CRDs (`certificates.cert-manager.io`) are installed and the configured `ClusterIssuer` is Ready                                                                                                                                                                          | `cert-manager not found or ClusterIssuer '<name>' not Ready`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| CNI NetworkPolicy support             | Create and delete a test `NetworkPolicy` in the target namespace to verify the CNI plugin supports NetworkPolicy enforcement                                                                                                                                                                 | `CNI plugin does not support NetworkPolicy; install Calico, Cilium, or enable Calico in policy-only mode alongside your cloud CNI — required for agent pod isolation (Section 13.2)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Namespace ResourceQuota               | Verify each agent namespace has a `ResourceQuota` and that the quota's pod limit ≥ sum of `minWarm` for pools targeting that namespace                                                                                                                                                       | `ResourceQuota in namespace '<ns>' allows <n> pods but configured pools require at least <m>; increase agentNamespaces[].resourceQuota.pods`                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Namespace LimitRange                  | Verify each agent namespace has a `LimitRange` with default container resource requests                                                                                                                                                                                                      | `LimitRange missing in namespace '<ns>'; required to prevent BestEffort pods (Section 17.2)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Kubernetes version                    | Verify server version ≥ 1.27 (minimum for required API features)                                                                                                                                                                                                                             | `Kubernetes version <ver> unsupported; minimum 1.27 required`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| StorageRouter region coverage         | For each region declared in `storage.regions`, verify that Postgres, MinIO, and Redis endpoints are reachable and that at least one pool carries a matching `region` label. When `storage.regions` is non-empty, verify every tenant with a `dataResidencyRegion` maps to a declared region. | `StorageRouter region '<region>' has no reachable <backend> endpoint` / `Tenant '<name>' references dataResidencyRegion '<region>' which is not declared in storage.regions`                                                                                                                                                                                                                                                                                                                                                                                             |
| kube-apiserver CIDR                   | Resolve the `kubernetes.default` Service ClusterIP (via `KUBERNETES_SERVICE_HOST` env var or `kubectl get svc kubernetes -n default`) and verify it falls within `{{ .Values.kubeApiServerCIDR }}`. A wrong value silently breaks gateway egress to the control plane under the `lenny-system` default-deny NetworkPolicy ([Section 13.2](13_security-model.md#132-network-isolation)). Note: this check validates `kubeApiServerCIDR` (used for gateway egress) only. Webhook ingress is governed by `webhookIngressCIDR` (default `0.0.0.0/0`) which requires no validation — any non-empty value is accepted; omitting it falls back to the default. | `kubeApiServerCIDR '<configured>' does not contain the kube-apiserver Service ClusterIP '<actual>'. Gateway egress to the control plane will fail under default-deny NetworkPolicy (Section 13.2). Correct this value with: kubectl get svc kubernetes -n default -o jsonpath='{.spec.clusterIP}'` |
| Internet egress CIDR exclusions       | When any pool uses `egressProfile: internet`: read actual cluster pod and service CIDRs (from node `spec.podCIDR` aggregation and `kubernetes` Service ClusterIP), compare against `egressCIDRs.excludeClusterPodCIDR` and `egressCIDRs.excludeClusterServiceCIDR` Helm values. Fail if either is missing or does not match the cluster's actual CIDRs. | `internet egress CIDR exclusion mismatch: excludeClusterPodCIDR is '<configured>' but cluster reports '<actual>'. Re-run with the correct CIDR to prevent lateral movement (Section 13.2)` |
| etcd Secret encryption (warning)      | When `LENNY_ENV=production`: emit a non-blocking preflight warning that etcd encryption at rest for Kubernetes Secrets cannot be verified programmatically by the preflight Job (it lacks etcd access).                                                                                      | `WARNING: Kubernetes does not encrypt Secrets in etcd by default. Credential pool API keys stored in Kubernetes Secrets (secretRef) are readable in plaintext from etcd snapshots or backups unless EncryptionConfiguration is enabled. Production deployments MUST configure EncryptionConfiguration with aescbc/aesgcm/kms for the 'secrets' resource type. Cloud-managed clusters: enable envelope encryption via AWS KMS (EKS), GCP Cloud KMS (GKE), or Azure Key Vault (AKS). Self-managed clusters: verify with 'etcdctl get /registry/secrets/lenny-system/<name> \| hexdump'. See Section 4.9.` |
| Node disk encryption (warning)        | When `LENNY_ENV=production`: emit a non-blocking preflight warning that node-level disk encryption cannot be verified programmatically.                                                                                                                                                      | `WARNING: Node-level disk encryption (LUKS/dm-crypt or cloud-provider encrypted volumes) is required for production deployments (Section 6.4) but cannot be verified by Lenny — it depends on the underlying node pool configuration. Verify manually: AWS (EBS encryption on launch template), GCP (CMEK or default encryption on boot/scratch disks), Azure (SSE on managed disks). For T4 workloads, use Kata or gVisor isolation profiles with encrypted scratch volumes.`                                                                                           |
| T4 node isolation webhook             | Verify that the `lenny-t4-node-isolation` `ValidatingWebhookConfiguration` exists and that its `caBundle` field is non-empty. When any pool references a T4 Runtime and the webhook is absent or misconfigured, T4 pods may be admitted to shared nodes.                                   | `lenny-t4-node-isolation ValidatingWebhookConfiguration not found or caBundle empty; required for T4 dedicated-node enforcement (Section 6.4)`                                                                                                                                                                                                                                                                                                                                                                                                                           |
| Drain-readiness webhook               | Verify that the `lenny-drain-readiness` `ValidatingWebhookConfiguration` exists and that its `caBundle` field is non-empty. When the webhook is absent or misconfigured, node drains will not check MinIO health before pod eviction.                                                        | `lenny-drain-readiness ValidatingWebhookConfiguration not found or caBundle empty; required for pre-drain MinIO health check (Section 12.5)`                                                                                                                                                                                                                                                                                                                                                                                                                             |
| SIEM endpoint (warning)               | When `LENNY_ENV=production` and `audit.siem.endpoint` is not set: emit a non-blocking preflight warning.                                                                                                                                                                                     | `WARNING: audit.siem.endpoint is not configured. Audit logs will be stored in Postgres only. A database superuser can bypass INSERT-only grants. This deployment does not meet compliance-grade audit integrity requirements (SOC2 CC7.2, FedRAMP AU-9, HIPAA §164.312(b)). Configure audit.siem.endpoint before using for regulated workloads (Section 11.7).`                                                                                                                                                                                                          |
| Prometheus reachability               | Verify a Prometheus-compatible endpoint is reachable at `ops.prometheus.url`. Tier-specific severity: INFO at Tier 1, WARN at Tier 2/3. Non-blocking unless `monitoring.acknowledgeNoPrometheus: true` is set ([§25.4](25_agent-operability.md#254-the-lenny-ops-service)).                                                                                                                                                  | `Prometheus endpoint '<url>' unreachable — lenny-ops will fall back to per-replica fan-out for metrics. Set monitoring.acknowledgeNoPrometheus=true to silence, or configure a Prometheus instance.`                                                                                                                                                                                                                                                                                                                                                                             |
| `lenny-ops-sa` RBAC                   | Verify that the `lenny-ops-sa` ServiceAccount has the RBAC permissions documented in [§25.4](25_agent-operability.md#254-the-lenny-ops-service). Uses `kubectl auth can-i` against each rule in the canonical RBAC table (Lease coordination, Deployment patches, CRD reads, ConfigMap reads, Secret reads for backup credentials, Job create/watch). | `ServiceAccount lenny-ops-sa is missing required permissions: <rules>. Re-render the chart or apply the Role/ClusterRole templates in deploy/helm/lenny/templates/ops/rbac.yaml`                                                                                                                                                                                                                                                                                                                                                                                              |
| `ops.ingress` ClusterIssuer (warning) | When `ops.ingress` has the `cert-manager.io/cluster-issuer` annotation set: verify the referenced ClusterIssuer exists. Non-blocking warning if missing.                                                                                                                                    | `WARNING: ops.ingress references ClusterIssuer '<name>' which was not found. Lenny-ops will run without TLS until this is corrected.`                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| Monitoring namespace (warning)        | Verify the `monitoring.namespace` exists and contains at least one Prometheus pod matching `monitoring.podLabel`. Non-blocking warning; informational so operators know whether the Helm-rendered `ServiceMonitor` / `PodMonitor` will be picked up.                                         | `WARNING: namespace '<ns>' does not contain a Prometheus pod matching label '<label>'. The rendered PodMonitor/ServiceMonitor may not be discovered by your monitoring stack.`                                                                                                                                                                                                                                                                                                                                                                                                          |

**Behavior:**

- **Exit code 0:** All checks passed — Helm proceeds with installation.
- **Exit code 1:** One or more checks failed — Helm aborts. The Job logs each failed check with the failure message and a reference to the relevant spec section.
- **Warnings (non-blocking):** Checks that detect suboptimal but functional configurations (e.g., MinIO without erasure coding, Redis Sentinel with fewer than 3 sentinels, absent SIEM endpoint) log warnings but do not block installation.
- **`--skip-preflight`:** Deployers can disable preflight validation by setting `preflight.enabled: false` in Helm values. This is intended for air-gapped or constrained environments where the Job cannot reach all backends at install time. A warning is logged: `"Preflight validation skipped — infrastructure misconfigurations may cause runtime failures."`
- **Dev mode:** When `global.devMode: true`, the preflight Job skips checks for MinIO encryption, cert-manager, CNI NetworkPolicy support, and PgBouncer (since dev mode uses embedded stores). Only Postgres and Redis connectivity are validated in Tier 2; Tier 1 (`make run`) skips preflight entirely.
- **Timeout:** The Job has a `activeDeadlineSeconds: 120`. If infrastructure is slow to respond, the deployer can increase this via `preflight.timeoutSeconds` in Helm values.
- **Idempotent:** Safe to re-run on `helm upgrade` — all checks are read-only (except the ephemeral NetworkPolicy create/delete test, which cleans up after itself).

**CLI equivalent:** `lenny-ctl preflight --config <values.yaml>` runs the same checks outside of Helm for pre-deployment validation in CI pipelines or manual verification.

**Local dev:** A `docker-compose.yml` is provided as described in [Section 17.4](#174-local-development-mode-lenny-dev). The `make run` target automatically applies the bootstrap seed with the echo runtime and default tenant.

**GitOps:** The Helm chart supports `helm template` rendering for ArgoCD/Flux integration. For GitOps workflows, the bootstrap seed values are committed alongside other Helm values and applied on every sync (idempotent by design).

**Day 0 installation walkthrough — empty cluster to first echo session.** The following is the minimum sequential procedure for a functional Lenny installation. It assumes Kubernetes ≥ 1.27, cert-manager, a CNI with NetworkPolicy support, Postgres ≥ 14, Redis (TLS + AUTH), and MinIO are already provisioned. For local development, use `make run` instead ([Section 17.4](#174-local-development-mode-lenny-dev)) — this walkthrough covers production-style Tier 2 installs.

1. **Install CRDs.** CRDs must be applied before the Helm chart:
   ```
   kubectl apply -f https://github.com/lennylabs/lenny/releases/latest/download/crds.yaml
   ```

2. **Create a `values.yaml`.** Minimal annotated example with the echo runtime:
   ```yaml
   global:
     devMode: false               # set true only for local dev (Section 17.4)

   # Infrastructure dependencies — supply your actual DSNs/endpoints
   postgres:
     connectionString: "postgres://lenny:password@pgbouncer:5432/lenny"
   redis:
     connectionString: "rediss://:password@redis:6380"   # rediss:// = TLS
   minio:
     endpoint: "https://minio.example.com"
     bucket: "lenny"
     accessKey: "..."
     secretKey: "..."

   # cert-manager ClusterIssuer to use for mTLS certificates (Section 10.3)
   certManager:
     clusterIssuer: "lenny-selfsigned"

   # kubeApiServerCIDR — must contain the kube-apiserver Service ClusterIP (used for
   # gateway egress NetworkPolicy). Discover with:
   #   kubectl get svc kubernetes -n default -o jsonpath='{.spec.clusterIP}'
   # Use the full service CIDR rather than a /32 (e.g. 10.96.0.0/12 for kubeadm defaults,
   # or the serviceIpv4Cidr from your cloud provider). Validated by lenny-preflight.
   kubeApiServerCIDR: "10.96.0.0/12"   # replace with your cluster's service CIDR

   # webhookIngressCIDR — source CIDR allowed to reach admission webhook pods on TCP 443
   # (i.e., the kube-apiserver egress IP for webhook callbacks). Defaults to 0.0.0.0/0,
   # which is safe within lenny-system's default-deny namespace: no unsolicited traffic
   # can reach webhook pods, and mTLS provides actual caller authentication. Tighten to
   # your control-plane node CIDR or cloud-provider control-plane CIDR if desired.
   # See Section 13.2 (NET-040) for cloud-specific discovery commands.
   # webhookIngressCIDR: "0.0.0.0/0"  # default — omit to use this value

   # Gateway replicas — 1 is sufficient for smoke test; 2+ for HA
   gateway:
     replicas: 1

   # Bootstrap seed: creates the default tenant, echo runtime, and pool on first install.
   # The bootstrap section is the AUTHORITATIVE source for pool definitions — the
   # lenny-bootstrap Job writes pools to Postgres via the admin API, and the
   # PoolScalingController reconciles CRDs from Postgres. Do NOT define a separate
   # top-level `pools` array — it is not consumed by Lenny and would be ignored.
   # CRD-level fields (resources, maxSize) are part of the pool definition in
   # bootstrap.pools and are written through to the CRD by the controller.
   bootstrap:
     enabled: true
     tenant:
       name: "default"
       displayName: "Default Tenant"
     runtimes:
       - name: echo
         type: agent
         image: "ghcr.io/lennylabs/runtime-echo:latest"
         isolationProfile: sandboxed   # gVisor; use 'baseline' for test clusters without gVisor
     pools:
       - name: echo-pool
         runtime: echo
         minWarm: 2
         maxSize: 10
         resources:
           requests: { cpu: "100m", memory: "128Mi" }
           limits:   { cpu: "500m", memory: "256Mi" }
   ```

3. **Run preflight.** Validate all infrastructure prerequisites before installing:
   ```
   lenny-ctl preflight --config values.yaml
   ```
   Fix any reported failures before proceeding. Common first-run issues: wrong `kubeApiServerCIDR`, missing `RuntimeClass` for gVisor, PgBouncer `pool_mode` not set to `transaction`.

4. **Install the Helm chart.**
   ```
   helm repo add lenny https://charts.lenny.dev
   helm install lenny lenny/lenny -n lenny-system --create-namespace -f values.yaml
   ```
   The `lenny-preflight` pre-install Job runs automatically and blocks on failure. The `lenny-bootstrap` post-install Job seeds the default tenant, echo runtime, and pool.

5. **Retrieve the initial admin token.**
   ```
   kubectl get secret lenny-admin-token -n lenny-system \
     -o jsonpath='{.data.token}' | base64 -d
   ```
   Store this token; it is written only once (re-runs are a no-op). Rotate with `lenny-ctl admin users rotate-token --user lenny-admin` after initial setup.

6. **Verify warm pool readiness.** Wait for echo pods to be pre-warmed:
   ```
   lenny-ctl admin pools get echo-pool
   # Expected: warm=2, available=2
   ```

7. **Create a first echo session.**
   ```
   curl -s -X POST https://<gateway>/v1/sessions \
     -H "Authorization: Bearer <admin-token>" \
     -H "Content-Type: application/json" \
     -d '{"runtimeName":"echo","tenantId":"default"}' | jq .sessionId
   ```
   A non-empty `sessionId` confirms the full path from empty cluster to live session is functional.

#### Helm `values.schema.json`

The Lenny Helm chart ships a canonical JSON Schema (Draft 2020-12) describing every documented value at `charts/lenny/values.schema.json`. Helm validates `-f values.yaml` and `--set` inputs against this schema on every `helm install` / `helm upgrade`, rejecting unknown keys, wrong types, and out-of-range values before any Kubernetes resource is applied.

**Generation.** The schema is generated at build time from the Go struct definitions in `pkg/chart/values/` using `invopop/jsonschema` (or an equivalent Go reflection-based generator). The build fails if the committed `values.schema.json` differs from the regenerated output, preventing drift between Go types and the published schema. The same Go structs serve as the source of truth for the `lenny-ctl install` wizard's question engine (below), the `lenny-ctl preflight` config parser, and the OpenAPI-based admin types for `POST /v1/admin/platform/upgrade` ([§25.8](25_agent-operability.md#258-platform-lifecycle-management)).

**IDE integration.** Editors with YAML support (VS Code + Red Hat YAML extension, Neovim + `yamlls`, IntelliJ) auto-complete and validate against the schema when the `values.yaml` file includes the schema reference:

```yaml
# yaml-language-server: $schema=https://schemas.lenny.dev/helm/values/v1.json
```

The schema is served from the Lenny documentation domain at a stable URL, versioned per Lenny release. Deployers who prefer a pinned local reference MAY vendor the schema into their repo.

**`lenny-ctl values validate`.** The CLI exposes a standalone validator ([§24.20](24_lenny-ctl-command-reference.md#2420-installation-wizard)): `lenny-ctl values validate --config values.yaml` exits 0 on success and prints a JSON Schema validation report on failure. This is the recommended check for CI pipelines that render values but do not run `helm install`.

#### Interactive installer (`lenny-ctl install`)

For operators who do not want to hand-write a full `values.yaml`, `lenny-ctl install` provides an interactive installation wizard. It detects cluster capabilities, asks a small number of targeted questions, previews the generated values file, and runs `helm install` on approval.

**Flow.**

1. **Detection phase.** The CLI connects to the target cluster (via current `kubeconfig` context, overridable with `--context`). It probes for: cert-manager CRDs and at least one Ready `ClusterIssuer`; Prometheus Operator CRDs (`ServiceMonitor`, `PrometheusRule`); installed CNI plugin (Calico, Cilium, or other NetworkPolicy-supporting CNI); available `RuntimeClass` objects (gVisor, Kata); Postgres/Redis/MinIO availability via the same probes as `lenny-preflight`; cluster Kubernetes version. Results are presented as a summary before the wizard asks any questions.

2. **Question phase.** The wizard asks a minimum set of questions, with sensible defaults for each:

   | Question                                          | Default / behavior                                                                                 |
   |---------------------------------------------------|----------------------------------------------------------------------------------------------------|
   | Cluster name / release namespace                  | `lenny-system`                                                                                     |
   | Target environment                                | `local` \| `dev` \| `prod` (drives alert thresholds, warm-pool sizes, log verbosity)               |
   | Answer file base                                  | Auto-suggested from detection (e.g., `eks-small-team.yaml` if AWS EKS detected) — see §17.9         |
   | Capacity tier                                     | `tier1` \| `tier2` \| `tier3` — defaults to `tier1` for `local`/`dev`, `tier2` for `prod`          |
   | Gateway domain / TLS strategy                     | `cert-manager` if detected, `bring-your-own` otherwise                                             |
   | Postgres DSN                                      | Prompt for external DSN or opt into embedded mode (`local` profile only)                          |
   | Redis DSN                                         | Same pattern as Postgres                                                                           |
   | Object storage endpoint + credentials             | MinIO defaults for `local`/`dev`; external S3-compatible or cloud-managed for `prod`              |
   | OIDC issuer URL and client ID                     | Optional in `local` (embedded dev OIDC is used); required in `prod`                                |
   | Reference runtimes to install (multi-select)      | All of §26 selected by default; deployer can deselect to minimize image-pull footprint             |

   Each question displays a one-line help string explaining the field and a reference to the relevant spec section. Questions are skipped when their answer is unambiguous from detection (e.g., if only one ClusterIssuer is Ready, no TLS strategy prompt is shown).

3. **Preview phase.** The wizard renders the resulting composite values file — an answer-file base plus a tier preset plus the per-question overrides — to stdout (or `--output-values path.yaml` to write to disk). The operator reviews the file before proceeding.

4. **Preflight.** The wizard runs `lenny-ctl preflight --config <rendered.yaml>` against the target cluster using the same checks as the Helm post-install preflight Job. Any hard failure aborts; warnings are displayed with the option to continue.

5. **Apply phase.** On approval, the wizard runs `helm install lenny lenny/lenny -f <rendered.yaml>`, streams the Helm progress, then runs the `lenny-ctl bootstrap` seed (using the bootstrap values from the composed file) and the standard post-install smoke test (create a session against the `chat` reference runtime — §26.7 — to confirm the full path works end-to-end).

**Non-interactive mode.** `lenny-ctl install --non-interactive --answers=answers.yaml` runs the same flow without prompts. The `answers.yaml` file is a simple key-value mapping of question IDs to answers; the wizard's Go structs serialize to this shape on every interactive run (via `--save-answers`), so operators can capture an interactive session once and replay it in CI/IaC. Example:

```yaml
# answers.yaml — captured from interactive install, replayable
release:
  namespace: lenny-system
  name: lenny
environment: prod
profile: eks-small-team
tier: tier2
domain: lenny.example.com
tls: cert-manager
postgres:
  dsn: "postgres://lenny:${LENNY_PG_PASSWORD}@lenny-pg:5432/lenny"
redis:
  dsn: "rediss://:${LENNY_REDIS_PASSWORD}@lenny-redis:6380"
objectStorage:
  endpoint: "https://s3.us-east-1.amazonaws.com"
  bucket: "acme-lenny-prod"
oidc:
  issuer: "https://auth.example.com"
  clientId: "lenny"
referenceRuntimes: [chat, claude-code, langgraph]
```

Environment-variable interpolation (`${VAR}` syntax) is supported for secret material; values are resolved at render time, never stored in the answer file.

**Airgap / no-network mode.** `lenny-ctl install --offline` skips detection probes that require cluster connectivity beyond reading `kubeconfig`, and uses only defaults and operator-supplied answers. The preflight phase is still run against the target cluster; only the detection phase is affected.

**Relationship to the Helm chart.** The wizard produces a normal `values.yaml`; `helm install` can be run directly from that file at any time. Nothing about the wizard is load-bearing — it is a convenience layer over the chart. Operators who prefer hand-written values retain the full Helm surface.

### 17.7 Operational Runbooks

Lenny must ship with operational runbooks for key failure scenarios as part of the documentation deliverables. The minimum required set:

Each runbook stub below uses a common three-part structure:

- **Trigger** — the alert or symptom that leads an operator to this runbook.
- **Diagnosis** — commands and checks to understand the failure scope.
- **Remediation** — ordered steps to restore service, with rollback guidance.

**Machine-consumable runbook format.** The runbooks below are consumed by both humans and agents. For agent parsing, the structure is formalized in [§25.7](25_agent-operability.md#257-operational-runbooks): each section is delimited by `<!-- access: ... -->` HTML-comment markers (`trigger`, `diagnosis`, `remediation`) and is served as structured JSON at `/v1/admin/runbooks/{name}/steps`. The examples below should be treated as the canonical format — when authoring new runbooks or editing existing ones, add the markers so `/v1/admin/runbooks/*` can parse them without ambiguity. The `issueRunbooks` lookup table (maintained in `pkg/gateway/health/runbook_links.go`) maps health-API issue codes to runbook names; the entries for `WARM_POOL_EXHAUSTED`, `WARM_POOL_LOW`, `CREDENTIAL_POOL_EXHAUSTED`, `POSTGRES_UNREACHABLE`, `REDIS_UNREACHABLE`, `MINIO_UNREACHABLE`, `CERT_EXPIRY_IMMINENT`, and `CIRCUIT_BREAKER_OPEN` are required by §25.7 Path B.

---

- **CRD upgrade failure and recovery** (`docs/runbooks/crd-upgrade.md`) — stale CRD detection, recovery procedure, GitOps sync-wave configuration. See [Section 17.6](#176-packaging-and-installation) for the detailed recovery steps.

- **Warm pool exhaustion** (`docs/runbooks/warm-pool-exhaustion.md`)
  - *Trigger:* `WarmPoolLow` / `WarmPoolExhausted` alert fires; `lenny_warmpool_idle_pods` drops to 0 for a pool; session requests return `RUNTIME_UNAVAILABLE`.
  - *Diagnosis:* `kubectl get sandboxes -n <agent-ns> -l pool=<pool>` — count pods in `warming` vs `idle` vs `claimed` states. Check `lenny_warmpool_pod_startup_duration_seconds` histogram for elevated pod startup times. Check `lenny_warmpool_warmup_failure_total` for image pull errors or setup command failures. Review PoolScalingController logs: `kubectl logs -l app=lenny-warm-pool-controller --since=10m`.
  - *Remediation:* (1) Emergency scale: `lenny-ctl admin pools set-warm-count --pool <name> --min <N+10>` to force immediate pod creation. (2) If pods are stuck in `warming`, check node resource pressure: `kubectl describe nodes | grep -A5 "Conditions:"` — cordon saturated nodes if needed. (3) If image pull is failing, verify registry credentials and image digest. (4) After service is restored, investigate root cause of pod startup failures and file an incident if recurrence is likely within 24h.

- **Postgres failover** (`docs/runbooks/postgres-failover.md`)
  - *Trigger:* `PostgresReplicationLag` or `SessionStoreUnavailable` alert fires; gateway logs `pq: connection refused` errors.
  - *Diagnosis:* Connect to PgBouncer admin socket: `psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c "SHOW POOLS;"` — verify active connection counts. Check replication lag: `SELECT now() - pg_last_xact_replay_timestamp() AS replication_delay` on the replica. Verify primary health: `SELECT pg_is_in_recovery()` should return `false` on the primary.
  - *Remediation:* (1) If PgBouncer has stale connections to the old primary, reload PgBouncer: `psql -c "RELOAD;" pgbouncer`. (2) Verify the Postgres DSN in Kubernetes Secret `lenny-system/postgres-credentials` points to the new primary (or the load-balanced endpoint). (3) Check PgBouncer pool mode remains `transaction` after failover: `psql -c "SHOW CONFIG;"`. (4) Once connectivity is restored, verify RLS is still active: `SELECT COUNT(*) FROM sessions` with a non-admin user must return only that tenant's rows. (5) Audit for any sessions that may have been lost during the failover window — check `SELECT * FROM sessions WHERE state NOT IN ('completed','failed','cancelled','expired') AND last_checkpoint_at < now() - interval '5 minutes'` and notify affected users.

- **Redis failure and recovery** (`docs/runbooks/redis-failure.md`)
  - *Trigger:* `RedisUnavailable` alert fires; quota enforcement logs switch to fail-open mode; `lenny_quota_redis_fallback_total` counter increments.
  - *Diagnosis:* `redis-cli -h <host> -a <password> --tls PING` — verify Redis reachability. Check Sentinel or Cluster status: `redis-cli CLUSTER INFO` or `redis-cli -p 26379 SENTINEL masters`. Check gateway logs for the fail-open window: `kubectl logs -l app=lenny-gateway --since=5m | grep "redis_unavailable"`.
  - *Remediation:* (1) While Redis is unavailable, the platform operates in fail-open mode with `cached_replica_count` assumption (the gateway uses the last successfully cached gateway replica count, not a hard-coded `1`) — overage exposure is bounded by `lenny_quota_redis_fallback_window` (default 60s). Do not rely on fail-open for more than 5 minutes. (2) If Redis is recoverable, restart the primary or promote a replica. (3) After Redis is restored, trigger quota reconciliation: `lenny-ctl admin quota reconcile --all-tenants` — this re-aggregates in-flight session usage from Postgres into Redis counters, closing any gap from the fail-open window. (4) Review `lenny_quota_redis_fallback_total` counter to quantify overage exposure. Issue billing corrections if any tenant was materially overcharged or undercharged ([Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream)).

- **Credential pool exhaustion** (`docs/runbooks/credential-pool-exhaustion.md`)
  - *Trigger:* `CredentialPoolExhausted` alert fires; session creation returns `CREDENTIAL_POOL_EXHAUSTED`; `lenny_credential_pool_available` gauge reaches 0.
  - *Diagnosis:* `GET /v1/admin/credential-pools/<name>` — check `availableCount`, `leasedCount`, and `coolingDownCount`. High `coolingDownCount` indicates rate-limit events from the provider. Check `lenny_credential_provider_rate_limit_total` by credential ID to identify hot keys.
  - *Remediation:* (1) If caused by rate limiting, check provider dashboards for rate limit tier. Short-term: extend `cooldownOnRateLimit` to reduce storm. (2) Add credentials: `POST /v1/admin/credential-pools/<name>/credentials` with a new `secretRef`. (3) If the pool has structural exhaustion (too many concurrent sessions per key), increase `maxConcurrentSessions` if the provider tier allows, or reduce session concurrency via tenant quotas. (4) Rotate any credential that is permanently rate-limited or revoked using the emergency revocation procedure (see emergency credential revocation runbook below).

- **Gateway replica failure** (`docs/runbooks/gateway-replica-failure.md`)
  - *Trigger:* `GatewayNoHealthyReplicas` alert fires; `lenny_gateway_healthy_replicas` drops below tier minimum; stream clients receive connection errors.
  - *Diagnosis:* `kubectl get pods -l app=lenny-gateway -n lenny-system` — identify crashed or `CrashLoopBackOff` pods. `kubectl describe pod <failing-pod>` for OOM, probe failures, or startup errors. Check HPA state: `kubectl get hpa lenny-gateway -n lenny-system`.
  - *Remediation:* (1) If OOM: check `lenny_gateway_memory_bytes` — if consistently near limit, increase memory requests in Helm values and redeploy. (2) If crash-looping on startup, check for CRD schema mismatch (see CRD upgrade runbook). (3) While replicas recover, in-flight streams to the failed replica experience a disconnect — clients must reconnect. Active sessions are not lost (session state is in Postgres/Redis); clients reconnect to a healthy replica and resume the stream. (4) After recovery, verify `lenny_gateway_active_sessions` is consistent with Postgres `SELECT COUNT(*) FROM sessions WHERE state = 'running'`.

- **cert-manager outage** (`docs/runbooks/cert-manager-outage.md`)
  - *Trigger:* `AdmissionWebhookUnavailable` alert or pod certificate rotation failures; `kubectl get certificates -n lenny-system` shows `Ready=False`; warm pool warming stalls because new pods cannot get certificates.
  - *Diagnosis:* `kubectl get pods -n cert-manager` — verify cert-manager is running. `kubectl describe certificate <name> -n lenny-system` for failure reason. Check `kubectl logs -l app=cert-manager -n cert-manager --since=5m` for ACME, DNS, or API errors.
  - *Remediation:* (1) If cert-manager pods are down, restart: `kubectl rollout restart deployment cert-manager -n cert-manager`. (2) If certificates have already expired, warm pool pods cannot receive valid mTLS certs and new pod creation halts — existing running pods are unaffected until their cert TTL expires (default 4h per [Section 17.8.1](#1781-operational-defaults--quick-reference)). Operator has up to 4h to restore cert-manager before session pods begin failing. (3) For emergency manual cert issuance (last resort only), see the cert-manager documentation for `kubectl cert-manager create certificate`. (4) After cert-manager recovers, certificates auto-renew — `kubectl get certificaterequests -n lenny-system` should show `Approved` and `Issued` states within 2 minutes.

- **MinIO failure** (`docs/runbooks/minio-failure.md`)
  - *Trigger:* `MinIOUnavailable` alert; workspace upload/download failures; `lenny_artifact_upload_error_total` spikes.
  - *Diagnosis:* `mc admin info <alias>` — check cluster health and erasure set status. `mc admin heal <alias>/lenny-artifacts` — check for healing operations in progress. Review gateway logs for `minio: dial tcp` errors.
  - *Remediation:* (1) Artifact retrieval failures are non-critical for in-flight sessions — sessions continue executing; only artifact upload/download is affected. (2) If MinIO has a quorum of healthy nodes, it is operational — identify and restart the failed node(s). (3) If MinIO is fully unavailable, workspace uploads for new sessions will fail at the `finalize` step. Session creation returns `INTERNAL_ERROR`. Inform users that new session creation is degraded. (4) After MinIO recovers, verify bucket access: `mc ls lenny-artifacts/workspaces/` — ensure encryption is still enabled: `mc encrypt info lenny-artifacts`. (5) Sessions that were terminated during the outage may have incomplete artifact uploads — check `SELECT session_id, state FROM sessions WHERE state = 'completed' AND created_at > now() - interval '2 hours'` and verify artifact presence for each.

- **Emergency credential revocation** (`docs/runbooks/credential-revocation.md`) — compromised credential identification, revocation endpoint usage, propagation verification, replacement credential addition. See [Section 4.9](04_system-components.md#49-credential-leasing-service) for the detailed steps.

- **Token Service outage** (`docs/runbooks/token-service-outage.md`)
  - *Trigger:* `TokenServiceUnavailable` alert fires ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)); circuit breaker in open state for > 30s; new session creation for credential-requiring pools returns `CREDENTIAL_MATERIALIZATION_ERROR`. Existing sessions are unaffected until credential lease expiry.
  - *Diagnosis:* (1) Check Token Service pod health: `kubectl get pods -l app=lenny-token-service -n lenny-system` — identify crashed or `CrashLoopBackOff` pods. (2) Inspect recent Token Service logs: `kubectl logs -l app=lenny-token-service -n lenny-system --since=5m` — look for KMS connectivity errors, RBAC denied messages, or OOM kills. (3) Verify KMS reachability from the Token Service pod: `kubectl exec -it <token-service-pod> -n lenny-system -- <kms-health-check>` (command depends on cloud provider). (4) Check the circuit breaker state in gateway metrics: `lenny_gateway_token_service_circuit_state` — value 2 = open. (5) Verify Secret access: `kubectl auth can-i get secret -n lenny-system --as=system:serviceaccount:lenny-system:lenny-token-service` — RBAC denial prevents credential decryption.
  - *Remediation:* (1) **Existing sessions continue** — sessions holding active leases are unaffected until lease expiry (grace period = lease TTL, typically minutes to hours depending on `credentialLeaseTTL`). Prioritize restoring the Token Service before leases expire to avoid cascading session failures. (2) **Restart the Token Service:** `kubectl rollout restart deployment lenny-token-service -n lenny-system` — resolves transient panics or deadlocks. (3) **KMS unavailability:** If the Token Service cannot reach KMS, verify cloud credentials or IAM role binding for the Token Service ServiceAccount — check IAM event logs for the cloud provider. Restore KMS access before proceeding. (4) **RBAC fix:** If Secret access is denied, re-apply the Token Service RBAC: `kubectl apply -f charts/lenny/templates/token-service-rbac.yaml`. (5) **After Token Service recovers:** The gateway circuit breaker resets automatically once the Token Service health check succeeds for two consecutive probes (per [Section 11.6](11_policy-and-controls.md#116-circuit-breakers) circuit breaker half-open logic). Verify by checking `lenny_gateway_token_service_circuit_state` returns to 0 (closed). (6) **Assess impact:** Query `SELECT session_id, state FROM sessions WHERE state = 'running' AND created_at > now() - interval '30 minutes'` and verify that sessions created near the outage window are operational — sessions that failed during the window will have `CREDENTIAL_MATERIALIZATION_ERROR` in their termination reason.

- **PgBouncer pool saturation** (`docs/runbooks/pgbouncer-saturation.md`)
  - *Trigger:* `PgBouncerPoolSaturated` alert fires ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)); `cl_waiting_time` exceeds 1s for > 60s (self-managed profile only). Client requests to Postgres are queuing — new sessions and state writes experience elevated latency or timeouts.
  - *Diagnosis:* (1) Connect to the PgBouncer admin socket and inspect pool state: `psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c "SHOW POOLS;"` — check `cl_active`, `cl_waiting`, `sv_active`, and `sv_idle` counts. (2) Compare `cl_active` + `cl_waiting` against `default_pool_size` to confirm saturation. (3) Check `max_client_conn`: `psql -c "SHOW CONFIG;" pgbouncer` — if total client connections approach `max_client_conn`, new connections are rejected. (4) Review `pgbouncer_exporter` metrics: `cl_waiting_time` (average wait) and `avg_query_time` — sustained wait > 1s with non-zero `cl_waiting` confirms pool exhaustion rather than slow queries. (5) Verify Postgres itself is not the bottleneck: `SELECT count(*), wait_event_type, wait_event FROM pg_stat_activity GROUP BY 2,3 ORDER BY 1 DESC LIMIT 20` — high lock waits or I/O waits on the Postgres side require separate diagnosis.
  - *Remediation:* (1) **Immediate relief — increase pool size at runtime:** `psql -c "SET default_pool_size=<new-value>;" pgbouncer` (PgBouncer supports runtime config changes via the admin socket). Calculate new value as `max_connections / pgbouncer_replicas`, leaving ≥10% headroom for superuser and replication connections. (2) **Persistent fix — update Helm values:** Set `pgbouncer.defaultPoolSize` and `pgbouncer.maxClientConn` in `values.yaml` and run `helm upgrade` — this persists the change across PgBouncer restarts. (3) **Scale PgBouncer horizontally:** If a single PgBouncer replica is the bottleneck, scale the Deployment: `kubectl scale deployment lenny-pgbouncer -n lenny-system --replicas=<N>`. Each additional replica adds `default_pool_size` backend connections toward Postgres — verify Postgres `max_connections` is not exceeded. (4) **If Postgres `max_connections` is the hard limit:** Increase `max_connections` on Postgres (requires restart) or reduce gateway replica count temporarily to shed load. (5) **After remediation:** Confirm `cl_waiting_time` drops below 100ms and `cl_waiting` returns to 0 in `SHOW POOLS;`. Verify the `PgBouncerPoolSaturated` alert clears within 2 minutes of pool drain. (6) **Post-incident:** Review `lenny_gateway_db_query_duration_seconds` histogram for the affected window to quantify user impact. If saturation recurred within 7 days, re-evaluate sizing guidance against current Tier targets ([Section 17.8](#178-capacity-planning-and-defaults)).

- **Admission webhook outage** (`docs/runbooks/admission-webhook-outage.md`) — covers both `AdmissionWebhookUnavailable` (RuntimeClass-aware policy webhook) and `CosignWebhookUnavailable` (cosign image-verification webhook). Both are `failurePolicy: Fail` webhooks; when either is unreachable, pod admission is blocked and warm pool replenishment halts.
  - *Trigger:* `AdmissionWebhookUnavailable` fires when the OPA/Gatekeeper or Kyverno admission webhook has been unreachable for > 30s; `CosignWebhookUnavailable` fires when the cosign ValidatingAdmissionWebhook endpoint returns errors for > 60s. Symptom: `WarmPoolBootstrapping` alert fires shortly after (pool cannot replenish); `kubectl get sandbox` shows no new pods being created.
  - *Diagnosis:* (1) Identify which webhook is failing: `kubectl get validatingwebhookconfigurations` — inspect the `lenny-admission-policy` and `lenny-cosign-verify` entries; check `caBundle` and `service` references. (2) Check webhook pod health: `kubectl get pods -n lenny-system -l app=lenny-admission-webhook` and `kubectl get pods -n lenny-system -l app=lenny-cosign-webhook` — look for `CrashLoopBackOff` or `Pending` pods. (3) Inspect recent webhook pod logs: `kubectl logs -l app=lenny-admission-webhook -n lenny-system --since=5m` and `kubectl logs -l app=lenny-cosign-webhook -n lenny-system --since=5m` — look for TLS errors, OOM kills, or connectivity failures. (4) Verify TLS certificate validity: `kubectl get certificate -n lenny-system` — check `Ready` status; a `False` value indicates cert-manager failed to rotate the webhook serving certificate (cross-reference the cert-manager outage runbook). (5) Confirm pod admission is actually blocked: `kubectl describe replicaset <warm-pool-rs> -n lenny-agents` — look for `failed calling webhook` events in the event stream.
  - *Remediation:* (1) **Webhook pod restart:** `kubectl rollout restart deployment lenny-admission-webhook -n lenny-system` or `kubectl rollout restart deployment lenny-cosign-webhook -n lenny-system` — resolves transient panics or deadlocks. Wait for rollout: `kubectl rollout status deployment/<name> -n lenny-system`. (2) **Certificate rotation failure:** If the webhook TLS certificate is expired or `Ready=False`, restart cert-manager and re-trigger issuance: `kubectl rollout restart deployment cert-manager -n cert-manager`; then delete the failing Certificate resource to force re-issuance: `kubectl delete certificate <name> -n lenny-system`. See the cert-manager outage runbook (`docs/runbooks/cert-manager-outage.md`) for the full procedure. (3) **Webhook service endpoint unavailable:** If the webhook Service has no ready endpoints (`kubectl get endpoints <webhook-service> -n lenny-system`), verify the backing Deployment is running and its readiness probe is passing. (4) **Emergency bypass (last resort — use with caution):** If the webhook cannot be restored quickly and warm pool exhaustion is imminent, temporarily set `failurePolicy: Ignore` on the affected `ValidatingWebhookConfiguration`: `kubectl patch validatingwebhookconfiguration lenny-cosign-verify --type=json -p '[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Ignore"}]'`. This allows unsigned images to be admitted — **acceptable only in a controlled outage window with no untrusted images in the pipeline and only until the webhook is restored.** Restore `failurePolicy: Fail` immediately after webhook recovery. Log the bypass window in the incident record. (5) **After recovery:** Verify warm pool replenishment resumes: check `lenny_warmpool_idle_pods` metric returns to `minWarm` within 2 minutes. Confirm `AdmissionWebhookUnavailable` and `CosignWebhookUnavailable` alerts clear. (6) **Root cause:** Review webhook pod restart history (`kubectl describe pod`) and HPA/VPA settings — if the webhook pod was OOM-killed, increase memory limits in Helm values (`admissionWebhook.resources.limits.memory`).

- **etcd operations** (`docs/runbooks/etcd-operations.md`) — operational procedures for etcd maintenance under sustained CRD write pressure. Covers: (1) defragmentation on a live cluster — single-member defrag procedure (follower-first, then leader), expected write pause duration, how to verify member health between defrags, and guidance for Tier 3 deployments where off-peak windows are narrow. (2) Compaction setting changes — rolling member restart procedure for self-managed clusters; escalation path for managed Kubernetes where etcd flags are provider-controlled. (3) Quota exhaustion recovery — full procedure for recovering from etcd alarm state: defrag all members, verify space reclaimed, `etcdctl alarm disarm`, verify API server write capability restored. (4) Escalation path — when etcd issues cannot be resolved at the Lenny operator level (managed K8s etcd unavailability, persistent quota growth despite defrag), escalation to the cloud provider or cluster admin team with the diagnostic information to collect (`etcd_disk_wal_fsync_duration_seconds`, `etcd_server_proposals_committed_total`, `etcd_debugging_mvcc_db_total_size_in_bytes`). Referenced by `EtcdQuotaNearLimit` and `EtcdUnavailable` alerts ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)).

- **Stuck finalizer remediation** (`docs/runbooks/stuck-finalizer.md`) — safe procedure for removing a stuck `lenny.dev/session-cleanup` finalizer after a `FinalizerStuck` alert fires. Steps:
  1. **Identify the stuck pod:** `kubectl get sandbox -A --field-selector=metadata.deletionTimestamp!='' | grep Terminating` — locate the `Sandbox` resource that has been in `Terminating` state beyond the 5-minute threshold.
  2. **Check for an active SandboxClaim:** `kubectl get sandboxclaim -A -o json | jq '.items[] | select(.spec.sandboxRef == "<pod-name>")'` — if a claim exists, the session is still logically active. Do **not** remove the finalizer until the claim is resolved.
  3. **Verify session state in Postgres:** Query `SELECT session_id, state, last_checkpoint_at FROM sessions WHERE pod_name = '<pod-name>'` — confirm the session is in a terminal state (`completed`, `failed`, `expired`) or has a recent checkpoint. If the session is still `active` with no checkpoint, coordinate with the gateway to trigger a checkpoint first (`DrainPod` with `checkpointFirst: true`).
  4. **Verify no in-flight artifacts:** Check that the pod's workspace artifacts have been uploaded to object storage: `mc ls lenny-artifacts/workspaces/<session-id>/` — if artifacts are missing and the session is active, do not proceed.
  5. **Remove the finalizer:** `kubectl patch sandbox <pod-name> -n <namespace> --type=json -p '[{"op":"remove","path":"/metadata/finalizers","value":["lenny.dev/session-cleanup"]}]'` — this allows Kubernetes to complete pod deletion.
  6. **Post-removal verification:** Confirm the pod is fully deleted: `kubectl get sandbox <pod-name> -n <namespace>` should return `NotFound`. Verify the warm pool has replenished: check `lenny_warmpool_idle_pods` metric returns to the expected `minWarm` level within 2 minutes. If replenishment does not occur, check WarmPoolController logs for reconciliation errors.
  7. **Root cause investigation:** Examine controller logs for the 5-minute window before the alert: `kubectl logs -l app=lenny-warm-pool-controller --since=10m | grep <pod-name>`. Common causes: controller leader election gap during pod termination, API server throttling on status updates (check `lenny_controller_api_throttle_total`), or a bug in the session cleanup path. File an incident if the stuck finalizer recurs on more than 2 pods within 24 hours.

- **Schema migration failure** (`docs/runbooks/schema-migration-failure.md`)
  - *Trigger:* `lenny-ctl migrate status` shows an unexpected phase (e.g., `phase1_applied` when `phase3_applied` was expected), a migration Kubernetes Job fails, or the `golang-migrate` `schema_migrations` table contains a `dirty` flag.
  - *Diagnosis:* (1) Check migration state: `lenny-ctl migrate status` — look for dirty versions or unexpected phase values. (2) Check advisory lock status: `SELECT * FROM pg_locks WHERE locktype = 'advisory'` — a held lock indicates a migration is still running or crashed mid-way. (3) Inspect the `schema_migrations` table directly: `SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 5` — a `dirty = true` row means a migration started but did not complete. (4) Check the migration Job logs: `kubectl logs job/lenny-migrate-<version> -n lenny-system` for the specific DDL failure (e.g., dependent views blocking a `DROP COLUMN`). (5) Verify partial DDL application: compare the actual table schema (`\d <table>` in psql) against the expected schema for the migration version.
  - *Remediation:* (1) **Forward-fix (preferred):** If the failure is due to a missing prerequisite (e.g., a dependent view), create and apply a forward-fix migration that resolves the dependency, then re-run the original migration. (2) **Re-run:** If the migration failed due to a transient error (connection timeout, advisory lock contention), clear the dirty flag (`UPDATE schema_migrations SET dirty = false WHERE version = <N>`), release any stale advisory locks, and re-run the migration Job. (3) **Down-migration (last resort):** If forward-fix is not feasible, apply the down migration: `lenny-ctl migrate down --version <N> --confirm` ([§24.13](24_lenny-ctl-command-reference.md#2413-migration-management)) — this launches the down-migration Job using the provided `down.sql` file to reverse the partial DDL. Verify the rollback with `lenny-ctl migrate status`. (4) After recovery, re-run preflight: `lenny-ctl preflight --config <values.yaml>`. Cross-reference: Phase 1.5 deliverable `docs/runbooks/db-rollback.md` covers the broader database rollback procedure; [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy) covers the expand-contract migration discipline.

- **Audit pipeline degradation** (`docs/runbooks/audit-pipeline-degraded.md`) — covers `OCSFTranslationBacklog`, `AuditLockContention`, and `EventBusPublishDropped`.
  - *Trigger:* any of (a) `OCSFTranslationBacklog` (`lenny_audit_ocsf_translation_failed_total` rising + `audit_log` rows with `ocsf_translation_state='retry_pending'` exceed `audit.ocsf.alertThreshold`, or any row transitions to `dead_lettered`), (b) `AuditLockContention` (`histogram_quantile(0.99, rate(lenny_audit_lock_acquire_seconds_bucket[5m])) > 0.05` + `lenny_audit_concurrency_timeout_total` rising), (c) `EventBusPublishDropped` (`rate(lenny_event_bus_publish_dropped_total[5m])` > `eventBus.dropAlertThreshold`).
  - *Diagnosis:* (1) `kubectl logs -n lenny-system deploy/gateway --since=15m | grep -E "ocsf_translation|AUDIT_CONCURRENCY|eventbus_publish"` for per-subsystem error messages. (2) Inspect `ocsf_translation_state` distribution: `GET /v1/admin/audit-events?ocsf_translation_state=retry_pending&since=1h` — persistent `retry_pending` rows indicate translator or downstream SIEM pressure; `dead_lettered` rows indicate translator-level rejections already recorded as class 2004 receipts. (3) For lock contention, query `SELECT tenant_id, count(*) FROM pg_stat_activity WHERE query LIKE '%pg_advisory_xact_lock%' GROUP BY tenant_id` to identify hot tenants. (4) For EventBus drops, fetch backlog: `GET /v1/admin/audit-events?eventbus_publish_state=failed&since=<alert_fire_time>`; check Redis pub/sub health and replay-buffer utilization (`lenny_event_bus_replay_buffer_utilization`).
  - *Remediation:* (1) OCSF backlog: verify SIEM endpoint reachability; if transient, no action needed (retries resume automatically). If persistent, raise `audit.ocsf.alertThreshold` temporarily and scale the translator worker pool. Dead-lettered receipts remain in `audit_log` and are visible to `/v1/admin/audit-events` under their class 2004 `unmapped.lenny_dead_letter` extension. (2) Lock contention: reduce new-session pressure on the hot tenant by lowering `oauth.rateLimit.tenantPerSecond` for that tenant (Helm upgrade) so that `/v1/oauth/token` throttles further session creation; force-terminate long-running sessions owned by the hot tenant via `lenny-ctl admin sessions force-terminate <id>` ([§24.11](24_lenny-ctl-command-reference.md)) to drain in-flight audit writes; if systemic, increase Postgres connection pool or shard the hot tenant. (3) EventBus drops: reconcile subscribers by replaying `/v1/admin/audit-events?eventbus_publish_state=failed`; restore Redis health before clearing `eventbus_publish_state=failed` markers. Cross-reference: [§11.7](11_policy-and-controls.md#117-audit-logging), [§12.6](12_storage-architecture.md#126-interface-design), [§16.5](16_observability.md#165-alerting-rules-and-slos).

- **Token store unavailable** (`docs/runbooks/token-store-unavailable.md`)
  - *Trigger:* `TokenStoreUnavailable` — `/v1/oauth/token` is returning `503 token_store_unavailable` for > 30s; session creation, delegation minting, and credential leasing fail platform-wide.
  - *Diagnosis:* (1) Check Postgres primary reachability: `kubectl exec -n lenny-system deploy/gateway -- psql $TOKEN_STORE_DSN -c '\dt issued_tokens'`. (2) If Postgres is up but writes are blocked, inspect `pg_stat_activity` for blocking queries against `issued_tokens` or advisory locks. (3) Check replica-lag gauge: `lenny_postgres_replication_lag_seconds`; if excessive, the token validation Postgres-fallback path is degraded. (4) Verify write-before-issue is not being starved: `lenny_audit_lock_acquire_seconds` P99.
  - *Remediation:* (1) If Postgres primary is down, follow `postgres-failover.md`; token issuance remains blocked fail-closed until primary is restored — this is correct behavior per §13.3. (2) If a blocking query is identified, cancel it and alert on-call for root cause. (3) If replica lag is the blocker, follow `postgres-failover.md` Replica promotion section. Do NOT bypass Postgres; fail-closed is the correct discipline. Cross-reference: [§13.3](13_security-model.md#133-credential-flow) (Authoritative durability for revocation, Write-before-issue ordering).

- **Gateway rate-limit storm** (`docs/runbooks/gateway-rate-limit-storm.md`)
  - *Trigger:* `GatewayRateLimitStorm` — `sum by (tenant_id) (rate(lenny_oauth_token_rate_limited_sampled_total[1m])) > 50` sustained for 5 minutes. The sampling counter increments only after the first `(tenant_id, sub)` rejection per 10-second window has already been audited, so a sustained rise indicates a brute-force burst or a runaway automation loop against `/v1/oauth/token`.
  - *Diagnosis:* (1) Identify source `sub`(s) from the accompanying `token.exchange_rate_limited` audit events: `GET /v1/admin/audit-events?event_type=token.exchange_rate_limited&tenant_id=<id>&since=15m`. (2) Correlate per-tier pressure: `sum by (limit_tier) (rate(lenny_oauth_token_rate_limited_sampled_total{tenant_id="<id>"}[1m]))` — a `caller_per_second` dominance points at a tight single-caller automation loop, `caller_per_minute` at burst followed by sustained traffic, `tenant_per_second` at coordinated multi-caller pressure. (3) Check replica distribution with `sum by (service_instance_id) (rate(lenny_oauth_token_rate_limited_sampled_total[1m]))`; per-replica local sampling (see [§13.3](13_security-model.md#133-credential-flow) Audit sampling for rate-limit rejections) means each replica emits its own first-rejection audit events, so an apparent N× multiplier across replicas is expected and does not indicate duplicated auditing of the *same* rejection.
  - *Remediation:* (1) If a single `sub` dominates and is a legitimate caller with a buggy retry loop, contact the caller's operator to fix the client. Lenny v1 does not expose a per-subject rate-limit override — the per-caller limits (10/s, 300/min) are platform-wide defaults; the only tunable tenant-level knob is `oauth.rateLimit.tenantPerSecond` in Helm values. (2) If the `sub` is hostile, block it at the upstream IdP (revoke the upstream OIDC session); this denies new Lenny token exchanges because `/v1/oauth/token` requires a valid bearer token per [§13.3](13_security-model.md#133-credential-flow) Client authentication. Existing in-flight Lenny tokens for that `sub` remain valid until `exp`, but rate-limiting bounds the damage window to the token lifetime. For faster containment, enumerate recent `session.created` audit events for the subject (`GET /v1/admin/audit-events?event_type=session.created&actorId=<sub>&since=<token_lifetime>`), then force-terminate each active session with `lenny-ctl admin sessions force-terminate <id>` ([§24.11](24_lenny-ctl-command-reference.md)). (3) If the pressure is tenant-wide, temporarily lower `oauth.rateLimit.tenantPerSecond` via a Helm upgrade and review access patterns. Do NOT disable sampling — it is the protective mechanism preventing audit-write saturation. Cross-reference: [§13.3](13_security-model.md#133-credential-flow) (Rate limiting on `/v1/oauth/token`, Audit sampling for rate-limit rejections), [§24.11](24_lenny-ctl-command-reference.md) (`lenny-ctl admin sessions`).

- **Drift snapshot stale after manual admin-API change** (`docs/runbooks/drift-snapshot-refresh.md`)
  - *Trigger:* `GET /v1/admin/drift` response includes `snapshot_stale: true` (default threshold: `bootstrap_seed_snapshot.written_at` older than `ops.drift.snapshotStaleWarningDays` days; default 7). Typically follows an emergency hotfix or out-of-band admin-API mutation (manual `POST /v1/admin/runtimes`, pool edits, tenant RBAC changes) made between upgrades, when the bootstrap snapshot was not also updated. See [§25.10](25_agent-operability.md#2510-configuration-drift-detection).
  - *Diagnosis:* (1) Inspect the drift response header fields: `snapshot_written_at`, `snapshot_age_seconds`, `snapshot_stale`, and the `snapshot_stale_warning` text. (2) Review recent admin audit events since `snapshot_written_at` to confirm whether intentional changes were made that should become the new desired state: `GET /v1/admin/audit-events?since=<snapshot_written_at>&event_type=runtime.created,runtime.updated,pool.updated,tenant.updated,credential_pool.updated,delegation_policy.updated`. (3) Compare the current Helm values file (GitOps source of truth) against the stored snapshot by calling `POST /v1/admin/drift/validate` with the Helm values as the `desired` body — any reported divergence identifies fields that were changed via admin API but not committed back to Helm values.
  - *Remediation:* (1) **Reconcile source-of-truth first.** If the out-of-band changes should persist, commit them to the Helm values file (or equivalent GitOps source) so future upgrades carry the change forward; if the changes were temporary and should be rolled back, revert them via the admin API before refreshing the snapshot. (2) **Refresh the snapshot:** call `POST /v1/admin/drift/snapshot/refresh` with the reconciled desired state as the body (`{"desired": {...}, "confirm": true}`). The endpoint replaces `bootstrap_seed_snapshot` (id=`live`) atomically and records the refresh in audit (`drift.snapshot_refreshed`). (3) **Verify:** re-run `GET /v1/admin/drift` and confirm `snapshot_stale: false` and that any previously-reported drift for intentional changes is now absent. (4) **Post-hotfix cleanup checklist:** any runbook that instructs the operator to make a direct admin-API mutation as an emergency remediation (e.g., credential rotation, pool resize outside a Helm upgrade, tenant suspension) ends with the step "After the incident is resolved, call `POST /v1/admin/drift/snapshot/refresh` with the current desired state to prevent stale-snapshot warnings on subsequent drift runs." Treat this step as a permanent tail of every hotfix runbook entry in this section.

- **Gateway clock drift** (`docs/runbooks/gateway-clock-drift.md`)
  - *Trigger:* `GatewayClockDrift` — `abs(lenny_time_drift_seconds) > 0.5` (warning) or > 2.0 (critical); at > 5.0 the replica self-removes from Service endpoints.
  - *Diagnosis:* (1) Identify the affected replica from the alert labels: `service_instance_id`. (2) Check node NTP status: `kubectl debug node/<node-name> -it --image=busybox -- chronyc tracking` (or `timedatectl` on systemd nodes). (3) Inspect other replicas' drift — if only one is affected, it is a node-local issue; if cluster-wide, investigate the NTP source. (4) Check token rejection metrics: `lenny_oauth_token_rejected_total{reason=~"subject_token_expired|actor_token_expired"}` rising on the affected replica implies `exp` checks are firing on valid tokens.
  - *Remediation:* (1) Restart the NTP/chrony daemon on the affected node. (2) If persistent, cordon and reschedule the pod to a different node. (3) Do NOT override the `lenny_time_drift_seconds > 5.0` self-removal — it is the fail-closed escape valve that prevents an out-of-sync replica from issuing or validating tokens. Cross-reference: [§13.3](13_security-model.md#133-credential-flow) (Clock synchronization tolerance).

Each runbook references the relevant alerts defined in [Section 16.5](16_observability.md#165-alerting-rules-and-slos). Runbooks are version-controlled alongside the platform code in `docs/runbooks/`.

> **Note:** The Lenny documentation targets three operator skill tiers: (1) **Developer** — uses `make run` or docker-compose, no K8s knowledge needed. (2) **Platform operator** — deploys via Helm, manages pools and scaling, follows runbooks for common issues. (3) **Cluster admin** — configures RuntimeClasses, node pools, network policies, and handles node drain/checkpoint integration. The Helm chart and documentation are structured to serve all three tiers, with progressive complexity.

### 17.8 Capacity Planning and Defaults

This section consolidates per-tier capacity guidance and tunable operational defaults. Subsection 17.8.1 provides a quick-reference table of all key defaults; subsection 17.8.2 provides the capacity tier reference with per-tier sizing for gateway replicas, warm pool sizes, HPA thresholds, Postgres and Redis topology, etcd cluster configuration, and controller tuning parameters. All values are starting points — operators should adjust based on observed traffic patterns during the first week of operation (see 17.8.2 "First deployment sizing" for the recommended monitoring workflow).

### 17.8.1 Operational Defaults — Quick Reference

All tunable defaults collected in one place for operator convenience.

| Setting                           | Default                                                                  | Reference |
| --------------------------------- | ------------------------------------------------------------------------ | --------- |
| Artifact retention TTL            | 7 days                                                                   | [§12.5](12_storage-architecture.md#125-artifact-store)     |
| Checkpoint retention              | Latest 2 per session                                                     | [§12.5](12_storage-architecture.md#125-artifact-store)     |
| Periodic checkpoint interval      | 600 s (10 min)                                                           | [§4.4](04_system-components.md#44-event--checkpoint-store)      |
| GC cycle interval (`gc.cycleIntervalSeconds`, min 60)                    | 900 s (15 min)                                                           | [§12.5](12_storage-architecture.md#125-artifact-store)     |
| Max session age                   | 7200 s (2 h)                                                             | [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)     |
| Max idle time                     | 600 s                                                                    | [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)     |
| Max resume window                 | 900 s                                                                    | [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)     |
| Rate limit fail-open window       | 60 s                                                                     | [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)     |
| Quota sync interval               | 30 s (min 10 s)                                                          | [§11.2](11_policy-and-controls.md#112-budgets-and-quotas)     |
| Billing event retention           | 395 days / ~13 months (`billing.retentionDays`; floor: 2190 days for `hipaa`, 365 days for `soc2`/`fedramp`) | [§11.2.1](11_policy-and-controls.md#1121-billing-event-stream)   |
| Audit event retention             | 365 days (override via `audit.retentionPreset` or `audit.retentionDays`) | [§16.4](16_observability.md#164-logging)     |
| GDPR erasure receipt retention    | 2555 days / 7 years (`audit.gdprRetentionDays`; floor: 2190 days when `complianceProfile` is any regulated value: `soc2`, `fedramp`, `hipaa`) | [§12.8](12_storage-architecture.md#128-compliance-interfaces), [§16.4](16_observability.md#164-logging) |
| Session log retention             | 30 days                                                                  | [§16.4](16_observability.md#164-logging)     |
| Pod cert TTL                      | 4 h                                                                      | [§10.3](10_gateway-internals.md#103-mtls-pki)     |
| Audit sync write pool size        | 4 connections                                                            | [§12.3](12_storage-architecture.md#123-postgres-ha-requirements)     |
| Audit startup chain check entries | 1000                                                                     | [§12.3](12_storage-architecture.md#123-postgres-ha-requirements)     |
| Session inbox max size            | 500 messages                                                             | [§7.2](07_session-lifecycle.md#72-interactive-session-model)      |
| DLQ max size                      | 500 messages                                                             | [§7.2](07_session-lifecycle.md#72-interactive-session-model)      |
| Checkpoint barrier ack timeout    | 90 s (min floor enforced by CRD validation, [§10.1](10_gateway-internals.md#101-horizontal-scaling))                       | [§10.1](10_gateway-internals.md#101-horizontal-scaling)     |
| Coordinator hold timeout          | 120 s                                                                    | [§10.1](10_gateway-internals.md#101-horizontal-scaling)     |
| Dual-store unavailability max     | 60 s                                                                     | [§10.1](10_gateway-internals.md#101-horizontal-scaling)     |
| Effective degraded window (dual-store outage) | `max(dualStoreUnavailableMaxSeconds, coordinatorHoldTimeoutSeconds)` = 120 s | [§10.1](10_gateway-internals.md#101-horizontal-scaling)     |
| Max tree recovery                 | 600 s (sized for ≤4-level trees; deep trees need increase — see [§8.10](08_recursive-delegation.md#810-delegation-tree-recovery))   | [§8.10](08_recursive-delegation.md#810-delegation-tree-recovery)     |
| Max suspended pod hold            | 900 s (15 min); `min(deployment, tenant)` — most restrictive wins        | [§6.2](06_warm-pod-model.md#62-pod-state-machine), [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation) |
| Delegation budget key TTL         | 259200 s (72 h)                                                          | [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease), [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation) |
| Eval rate limit — per-session     | 100 submissions/min (`evalRateLimit.perSessionPerMinute`)                | [§10.7](10_gateway-internals.md#107-experiment-primitives)     |
| Eval rate limit — per-tenant      | 10,000 submissions/min (`evalRateLimit.perTenantPerMinute`)              | [§10.7](10_gateway-internals.md#107-experiment-primitives)     |
| Tracing context max size          | 4 KB serialized                                                          | [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)      |
| Tracing context max key length    | 128 bytes                                                                | [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)      |
| Tracing context max value length  | 256 bytes                                                                | [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)      |
| Tracing context max entries       | 32                                                                       | [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)      |
| Audit advisory-lock acquire timeout (`audit.lock.acquireTimeoutMs`) | 5000 ms — gateway aborts with `AUDIT_CONCURRENCY_TIMEOUT` rather than wait longer for `pg_advisory_xact_lock` under starvation | [§11.7](11_policy-and-controls.md#117-audit-logging) |
| Audit advisory-lock max retry attempts (`audit.lock.maxRetries`) | 3                                                                      | [§11.7](11_policy-and-controls.md#117-audit-logging) |
| Audit advisory-lock retry base backoff (`audit.lock.retryBaseMs`) | 20 ms (exponential doubling: 20, 40, 80) with ±20% jitter             | [§11.7](11_policy-and-controls.md#117-audit-logging) |
| OCSF translation retry interval (`audit.ocsf.retryInterval`) | 30 s                                                                   | [§11.7](11_policy-and-controls.md#117-audit-logging) |
| OCSF translation max attempts before dead-letter (`audit.ocsf.maxAttempts`) | 10                                                       | [§11.7](11_policy-and-controls.md#117-audit-logging) |
| OCSF translation backlog alert threshold (`audit.ocsf.alertThreshold`) | 10 pending rows sustained for 5 min (or any single `dead_lettered`) | [§16.5](16_observability.md#165-alerting-rules-and-slos) |
| `/v1/oauth/token` per-tenant global rate limit (`oauth.rateLimit.tenantPerSecond`) | 100 req/s (per-caller limits 10/s and 300/min enforced separately at the `caller_per_second` and `caller_per_minute` tiers) | [§13.3](13_security-model.md#133-credential-flow) |
| EventBus publish-drop alert threshold (`eventBus.dropAlertThreshold`) | 10 drops/min                                                          | [§12.6](12_storage-architecture.md#126-interface-design), [§16.5](16_observability.md#165-alerting-rules-and-slos) |

All values are overridable via Helm values or the corresponding CRD field. See each referenced section for detailed semantics. For per-tier recommended values, see [Section 17.8.2](#1782-capacity-tier-reference).

### 17.8.2 Capacity Tier Reference

This section provides per-tier sizing recommendations for all infrastructure components. These are starting points — production deployments should benchmark and adjust. See [Section 16.5](16_observability.md#165-alerting-rules-and-slos) for tier definitions.

**Gateway and API layer:**

| Parameter                                  | Tier 1                     | Tier 2                     | Tier 3                     |
| ------------------------------------------ | -------------------------- | -------------------------- | -------------------------- |
| Gateway replicas (min / max)               | 2 / 4                      | 3 / 10                     | 5 / 30                     |
| HPA target CPU utilization                 | 70%                        | 65%                        | 60%                        |
| HPA queue depth target (averageValue)      | 15                         | 10                         | 5                          |
| HPA scale-up stabilization window          | 0s                         | 0s                         | 0s                         |
| HPA scale-up max policy                    | 100% / 15s or 4 pods / 15s | 100% / 15s or 4 pods / 15s | 100% / 15s or 8 pods / 15s |
| HPA scale-down pods per period             | 1 / 60s                    | 1 / 60s                    | 3 / 60s                    |
| Stream Proxy maxConcurrent                 | 200                        | 2,000                      | 20,000                     |
| Upload Handler maxConcurrent               | 50                         | 500                        | 2,000                      |
| MCP Fabric maxConcurrent                   | 100                        | 1,000                      | 5,000                      |
| LLM Proxy maxConcurrent                    | 100                        | 1,000                      | 10,000                     |
| Gateway preStop drain timeout              | 60s                        | 60s                        | 120s                       |
| `terminationGracePeriodSeconds` (gateway pod) | 240s                    | 240s                       | 300s                       |
| Gateway scale-down time (max→min replicas) | 2 min (4→2, 1 pod/60s)     | 7 min (10→3, 1 pod/60s)    | 8.3 min (30→5, 3 pods/60s) |
| Minimum healthy gateway replicas (alert)   | 2                          | 3                          | 5                          |

> **Native translator footprint.** Translation runs inside the gateway binary ([§4.9](04_system-components.md#49-credential-leasing-service) Native translator) and is accounted for within the gateway pod's own CPU/memory envelope — no separate row applies. HPA targets (CPU and `request_queue_depth`) are evaluated at the pod level; if the translator adds measurable load, it surfaces in the gateway's existing pod-level metrics and HPA signals. Production deployments with proxy-mode pools SHOULD re-measure gateway pod capacity under their own workload because per-request translation cost varies with model/provider mix.

**`minReplicas` burst-absorption formula (SCL-036):**

The `minReplicas` value must be large enough that idle replicas can absorb the burst of session attempts that arrive during the HPA pipeline lag window before new replicas become available. The formula differs by scaling path:

```
minReplicas >= ceil(burst_arrival_rate * pipeline_lag_seconds / sessions_per_replica)
```

Where:
- `burst_arrival_rate` — peak session arrival rate (sessions/s) at the target tier
- `pipeline_lag_seconds` — worst-case HPA reaction lag (60s for Prometheus Adapter; 20s for KEDA with `pollingInterval: 10s`)
- `sessions_per_replica` — `gateway.maxSessionsPerReplica` (the per-replica session capacity ceiling)

**Path A — KEDA (mandatory for Tier 3, optional for Tier 1/2):**

With KEDA (`pollingInterval: 10s`), worst-case pipeline lag is ~20s:

```
minReplicas >= ceil(burst_arrival_rate * 20 / sessions_per_replica)
```

| Tier | burst_arrival_rate | pipeline_lag | sessions_per_replica | minReplicas (raw) | minReplicas (rounded up) |
| ---- | ------------------ | ------------ | -------------------- | ----------------- | ------------------------ |
| 1    | 5/s                | 20s          | 50                   | ceil(100/50) = 2  | **2**                    |
| 2    | 30/s               | 20s          | 200                  | ceil(600/200) = 3 | **3**                    |
| 3    | 200/s              | 20s          | 400                  | ceil(4000/400) = 10 | **10** → use 5 minimum; scale-up policy absorbs the remainder within one 15s period (100% + 8 pods) |

> **Tier 3 note (KEDA path):** At 200/s burst and 20s lag, 4,000 session attempts arrive before HPA reacts. With 5 `minReplicas` at 400 sessions/replica capacity, the pool can absorb up to 2,000 sessions before scale-up; the scale-up policy (100%/15s or 8 pods/15s) doubles replicas in the first 15s, covering the remaining 2,000 attempts. The table value of `5 / 30` for Tier 3 is therefore valid for the KEDA path when the aggressive scale-up policy ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) is in place. To eliminate reliance on scale-up speed, set `minReplicas: 10`.

**Path B — Prometheus Adapter only (non-KEDA; supported for Tier 1/2; not recommended for Tier 3):**

With Prometheus Adapter, worst-case pipeline lag is ~60s:

```
minReplicas >= ceil(burst_arrival_rate * 60 / sessions_per_replica)
```

| Tier | burst_arrival_rate | pipeline_lag | sessions_per_replica | minReplicas (raw)    | minReplicas (recommended) |
| ---- | ------------------ | ------------ | -------------------- | -------------------- | ------------------------- |
| 1    | 5/s                | 60s          | 50                   | ceil(300/50) = 6     | **6**                     |
| 2    | 30/s               | 60s          | 200                  | ceil(1800/200) = 9   | **9**                     |
| 3    | 200/s              | 60s          | 400                  | ceil(12000/400) = 30 | **30** (equals maxReplicas — not viable; use KEDA) |

> **Tier 3 note (non-KEDA path):** At 200/s burst, the Prometheus Adapter path requires `minReplicas: 30`, which equals `maxReplicas` and provides no headroom for further scale-out. This is the reason KEDA is mandatory for Tier 3 ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). Tier 1/2 deployers using the Prometheus Adapter path should use the values from the table above rather than the gateway table defaults (which are calibrated for the KEDA path); update the `autoscaling.minReplicas` Helm value accordingly.

**Warm pool sizing:**

| Parameter                          | Tier 1 | Tier 2 | Tier 3 |
| ---------------------------------- | ------ | ------ | ------ |
| Expected claim rate                | 0.5/s  | 5/s    | 30/s   |
| Recommended minWarm (per hot pool) | 20     | 175    | 1050   |
| Hot pools                          | 1–2    | 3–5    | 5–10   |
| Pool safety factor (agent-type)    | 1.5    | 1.5    | 1.2    |
| Pool safety factor (mcp-type)      | 2.0    | 2.0    | 1.5    |

> **Note -- no safety margin applied:** The recommended `minWarm` values above use `safety_factor = 1.0` (no safety margin) to provide baseline starting points for initial deployment and capacity planning. These baselines have **zero headroom** above the raw demand estimate during controller failover -- a single claim above the expected rate during the 35-second failover window exhausts the pool. **For production deployments**, operators MUST apply the per-tier `safety_factor` from the table above: Tier 1/2 with 1.5 yields `ceil(0.5 * 1.5 * 35) = 27` / `ceil(5 * 1.5 * 35) = 263`; Tier 3 with 1.2 yields `ceil(30 * 1.2 * 35) = 1,260`. Use the safety-factor-adjusted values as the production `minWarm`.

Formula: `minWarm >= claim_rate * safety_factor * (failover_seconds + pod_startup_seconds) + burst_p99_claims * pod_warmup_seconds`. The `safety_factor` column in the table above (1.5 for Tier 1/2, 1.2 for Tier 3) scales the steady-state term to provide a buffer above the raw demand estimate (see [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration) for the full formula with `safety_factor`). The first term covers sustained demand during failover; the burst term (see [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration)) reserves headroom for demand spikes that outpace pool refill. **Use `failover_seconds = 25` (worst-case crash scenario: `leaseDuration + renewDeadline = 15s + 10s`).** With 25s failover + 10s startup = 35s window; burst term adds headroom proportional to warmup latency and observed burst intensity. Note: clean shutdown (rolling update) reduces failover to near-zero via voluntary lease release, but sizing must cover the crash case.

**Delegation fan-out sizing (SCL-041).** When sessions use the `orchestrator` preset (`maxParallelChildren: 10`) or any high-fan-out delegation lease, delegation-driven pod demand adds to the baseline session-creation claim rate. The total warm pool demand is bounded by the tier's concurrent delegation fan-out limit ([Section 16.5](16_observability.md#165-alerting-rules-and-slos): 10 at Tier 1, 100 at Tier 2, 500 at Tier 3) — not by `sessions × maxParallelChildren`. At Tier 3 this limit is 500 concurrent child delegations, not 100,000 (10,000 sessions × 10 children), because the gateway enforces the system-wide concurrent delegation cap from the capacity tier table.

Formula for delegation-adjusted `minWarm`:

```
delegation_claim_rate = concurrent_delegations / avg_child_session_seconds
adjusted_claim_rate   = base_claim_rate + delegation_claim_rate
adjusted_burst_claims = burst_p99_claims + delegation_burst_claims
minWarm >= adjusted_claim_rate * safety_factor * (failover_seconds + pod_startup_seconds) / mode_factor
            + adjusted_burst_claims * pod_warmup_seconds / burst_mode_factor
```

This formula assumes session mode when `mode_factor = 1.0` and `burst_mode_factor = 1.0`. For task-mode or concurrent-mode delegation child pools, apply the appropriate `mode_factor` and `burst_mode_factor` values from [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). Omitting these divisors for a task-mode pool with `maxTasksPerPod: 50` would over-provision by up to 50x.

Where:
- `concurrent_delegations` — tier concurrent delegation fan-out limit (10 / 100 / 500 for Tier 1/2/3)
- `avg_child_session_seconds` — estimated average child session duration (default: 60s)
- `base_claim_rate` — session-creation claim rate from the warm pool sizing table above
- `delegation_burst_claims` — peak instantaneous delegation claims that outpace pool refill (use `concurrent_delegations × 0.1` as a conservative default; refine from observed `burst_p99_claims` once traffic data is available)

**Tier 3 worked example (orchestrator preset, session mode `mode_factor = 1.0`, default `safety_factor = 1.2`, `pod_warmup_seconds = 35`):**

```
delegation_claim_rate = 500 / 60 ≈ 8.3/s
adjusted_claim_rate   = 30 + 8.3 ≈ 38/s
adjusted_burst_claims = burst_p99_claims + (500 * 0.1) = burst_p99_claims + 50
minWarm >= ceil(38 * 1.2 * 35 + (burst_p99_claims + 50) * 35)
        = ceil(1,596 + (burst_p99_claims + 50) * 35)
```
With `burst_p99_claims = 0` (first deployment, no historical data): `ceil(1,596 + 1,750) = 3,346`. With observed `burst_p99_claims = 10`: `ceil(1,596 + 2,100) = 3,696`.

The recommended baseline of `minWarm: 1050` covers session-creation demand only. Deployments where a significant fraction of sessions use the `orchestrator` preset or other high-fan-out leases should increase `minWarm` using the formula above — a value of approximately **3,400** (with burst term, zero historical burst data) is appropriate for Tier 3 when `orchestrator`-preset sessions represent a large share of load. If `orchestrator`-preset sessions are rare (< 10% of sessions), the baseline 1,050 remains adequate. Operators should monitor `lenny_warmpool_idle_pods` and `lenny_pod_claim_queue_wait_seconds` P99 during the first week (see first-week monitoring workflow below) to confirm whether delegation fan-out is material at their actual traffic mix.

> **Note:** Distributing warm pods across multiple hot pools reduces per-pool `minWarm` proportionally. At Tier 3 with 10 hot pools and zero historical burst data, the per-pool delegation-adjusted `minWarm` is approximately `ceil(3,346 / 10) = 335`, well within normal operating range.

**First deployment sizing.** The formulas in [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration) and above require historical traffic metrics (`base_demand_p95`, `burst_p99_claims`, `pod_warmup_seconds`) that are not available at initial deployment. The PoolScalingController **cannot auto-configure `minWarm` for a new pool** because it has no demand signal — the initial value must always be set manually via the admin API or Helm values.

For first deployments, use the per-tier `minWarm` values in the table above as conservative starting points. These values assume: (a) `failover_seconds` of 25s (worst-case crash scenario), (b) `pod_startup_seconds` of 10s (container pull cached, runtime startup only), (c) the formula `claim_rate * (failover_seconds + pod_startup_seconds)` as the primary term, and (d) a single hot pool per runtime. If your deployment has multiple hot pools, divide the per-tier `minWarm` across pools proportionally to expected traffic share. If pod warmup in your environment is significantly longer (e.g., large container images, slow registry), increase `minWarm` proportionally. **Note on `pod_startup_seconds` vs `pod_warmup_seconds`:** The baseline sizing above uses only `pod_startup_seconds` (container pull + runtime startup, no SDK initialization). For SDK-warm pools, `pod_warmup_seconds` — the full time from pod creation to ready state including SDK initialization — is typically 30–90s, far exceeding the 10s startup baseline. For such pools the burst term (`burst_p99_claims * pod_warmup_seconds`) dominates and operators must substitute the observed SDK-warm startup time for `pod_warmup_seconds` in the full formula ([Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration)) rather than relying on the baseline table values.

**Cold-start bootstrap procedure.** A new pool enters **bootstrap mode** automatically when it has fewer than 48 hours of traffic data and no operator-specified convergence confirmation. The full procedure is:

1. **Set the bootstrap `minWarm` override.** Immediately after pool creation (via admin API `PUT /v1/admin/pools/{name}` or the `bootstrap.pools[].minWarm` Helm value), set `minWarm` to the per-tier conservative starting value from the table above. This is a **static override** — the PoolScalingController will not compute a formula-driven target while bootstrap mode is active. The override is stored as `spec.bootstrapMinWarm` on the `SandboxWarmPool` CRD and takes precedence over the formula output.

2. **Bootstrap mode indicator.** While `spec.bootstrapMinWarm` is set and fewer than 48 hours of `base_demand_p95` data have been accumulated, the PoolScalingController sets `status.scalingMode: bootstrap` on the `SandboxWarmPool` CRD and emits the `lenny_pool_bootstrap_mode` gauge (value `1`, labeled by pool name). The `PoolBootstrapMode` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)) fires immediately when any pool enters bootstrap mode, giving operators visibility that the pool is operating on a static override rather than formula-driven scaling.

3. **Operator-facing override API.** The admin API provides explicit control:
   - `PUT /v1/admin/pools/{name}` with body `{"bootstrapMinWarm": N}` — sets or updates the bootstrap override.
   - `DELETE /v1/admin/pools/{name}/bootstrap-override` — removes the bootstrap override and allows the controller to switch to formula-driven scaling immediately, regardless of the 48-hour window. Use when early traffic data is sufficient to trust the formula.
   - `GET /v1/admin/pools/{name}` response includes a `bootstrapStatus` object: `{"active": true/false, "bootstrapMinWarm": N, "hoursOfData": H, "estimatedConvergenceAt": "<timestamp>"}`.

4. **Convergence criteria.** The PoolScalingController automatically exits bootstrap mode and switches to formula-driven scaling when **all** of the following are true:
   - At least 48 hours of traffic data have been accumulated for the pool (`base_demand_p95` and `burst_p99_claims` are populated with a non-zero P95 window).
   - The controller's formula-computed `target_minWarm` has been stable (variance < 20% across consecutive reconciliation cycles over a 1-hour rolling window) for at least 2 hours.
   - No `WarmPoolLow` alert has fired in the last 6 hours.
   - The formula-computed target does not exceed 3× the bootstrap `minWarm` value (a large multiplier indicates the static override is significantly undersized — the controller emits a `PoolBootstrapUnderprovisioned` warning rather than silently switching to a much larger formula value).
   When all criteria are met, the controller clears `spec.bootstrapMinWarm`, sets `status.scalingMode: formula`, and emits `lenny_pool_bootstrap_mode` gauge `0`.

5. **Bootstrap-mode metric/alert.** `lenny_pool_bootstrap_mode` (gauge, per pool, `1` = active, `0` = converged) enables dashboards to display which pools are still under manual control. The `PoolBootstrapMode` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)) fires as a Warning when any pool has been in bootstrap mode for more than 72 hours — this indicates the operator has not reviewed the initial sizing and the pool has not received enough traffic for convergence.

**First-week monitoring workflow.** During the first week of operation, monitor the following metrics to determine whether the initial `minWarm` is appropriate:

- **`lenny_warmpool_idle_pods`** — if consistently near zero, `minWarm` is too low; increase by 50%.
- **`WarmPoolLow` alert** — if this fires repeatedly (more than twice per hour), `minWarm` is undersized for current demand.
- **`lenny_warmpool_idle_pod_minutes`** — if idle pod-minutes per hour exceeds `minWarm × 30` (i.e., most pods sit idle for half the hour), `minWarm` is oversized; reduce by 25%.
- **`lenny_pod_claim_queue_wait_seconds`** (P99 derived from histogram — canonical name per [§16.1](16_observability.md#161-metrics)) — if P99 claim latency exceeds 2s, the pool is not keeping up with demand; increase `minWarm` or investigate pod startup times.

After 48–72 hours of production traffic, the PoolScalingController will have sufficient `base_demand_p95` and `burst_p99_claims` data to auto-scale. At that point, switch from the manual `minWarm` override to controller-managed scaling by removing the `minWarm` override from Helm values (or calling `DELETE /v1/admin/pools/{name}/bootstrap-override`) and confirming the controller's computed target is reasonable. Re-evaluate weekly for the first month.

**Controller tuning:**

| Parameter                                                 | Tier 1         | Tier 2                                  | Tier 3                                   |
| --------------------------------------------------------- | -------------- | --------------------------------------- | ---------------------------------------- |
| Pod creation rate limiter (QPS / burst)                   | 20 / 50        | 40 / 100                                | 80 / 200                                 |
| Status update rate limiter (QPS / burst)                  | 30 / 100       | 60 / 200                                | 120 / 400                                |
| Max concurrent reconciles (`--max-concurrent-reconciles`) | 1              | 5                                       | 15                                       |
| Work queue max depth                                      | 500            | 2,000                                   | 10,000                                   |
| Initial fill grace period                                 | 60s            | 90s                                     | 120s                                     |
| Controller replicas                                       | 2              | 2                                       | 3                                        |
| Controller pod anti-affinity (`--controller-anti-affinity`) | `preferred` (advisory) | `required` — WPC and PSC leaders must not share a node | `required` — WPC and PSC leaders must not share a node |
| `statusUpdateDeduplicationWindow` (`--status-update-dedup-window`, type: duration) | 500ms | 500ms | 250ms |
| etcd compaction mode / retention                          | periodic / 5m  | periodic / 5m                           | periodic / 2m                            |
| etcd defrag schedule                                      | Daily off-peak | Daily off-peak                          | Every 12h off-peak                       |
| etcd quota-backend-bytes                                  | 4 GB           | 8 GB                                    | 8 GB (dedicated cluster recommended)     |
| etcd monitoring                                           | Standard       | Enhanced (write latency + quota alerts) | Dedicated etcd cluster with full metrics |

**Postgres and connection pooling** (self-managed profile — for cloud-managed equivalents see [Section 17.9](#179-deployment-answer-files)):

| Parameter                              | Tier 1        | Tier 2         | Tier 3                                            |
| -------------------------------------- | ------------- | -------------- | ------------------------------------------------- |
| Postgres instance class                | 2 vCPU / 4 GB | 4 vCPU / 16 GB | 8+ vCPU / 32+ GB                                  |
| Postgres max_connections               | 100           | 200            | 500                                               |
| PgBouncer replicas                     | 2             | 2              | 4                                                 |
| PgBouncer default_pool_size            | 25            | 50             | 110 ³                                             |
| PgBouncer reserve_pool_size            | 5             | 10             | 15                                                |
| Read replicas                          | 0             | 0–1            | 1–2                                               |
| Estimated sustained write IOPS         | ~22/s         | ~220/s         | ~1,300/s                                          |
| Estimated burst write IOPS (3×)        | ~66/s         | ~660/s         | ~3,900/s                                          |
| Billing batch flush interval           | 500ms         | 500ms          | 250ms                                             |
| Audit flush mode (T3/T4 events)        | synchronous (unconditional, no buffer) | synchronous (unconditional, no buffer) | synchronous (unconditional, no buffer) |
| Audit flush mode (T2 events, default)  | synchronous   | synchronous    | synchronous                                       |
| Audit batching for T2 (`audit.batchingEnabled: true`, opt-in) | 250ms / 100 entries | 250ms / 100 entries | 250ms / 100 entries |
| Separate billing/audit Postgres        | No            | No             | Optional (recommended if replication lag > 100ms) |

> ³ **Tier 3 `default_pool_size` derivation:** Per the sizing formula in [Section 12.3](12_storage-architecture.md#123-postgres-ha-requirements), `default_pool_size ≈ (max_connections − superuser_and_replication_headroom) / pgbouncer_replicas`. At Tier 3: `(500 − 20) / 4 = 120`. The value is reduced to 110 to reserve backend connection budget for the audit sync write pool (`audit.syncWritePoolSize`, default: 4 connections per gateway replica; 30 replicas × 4 = 120 total audit connections distributed across 4 PgBouncer replicas, consuming ~30 backend connections per PgBouncer replica). Operators should verify that `(default_pool_size + reserve_pool_size) × pgbouncer_replicas + audit_sync_pool_total ≤ max_connections`.

**Redis** (self-managed profile — for cloud-managed equivalents see [Section 17.9](#179-deployment-answer-files)):

| Parameter                   | Tier 1                                                  | Tier 2                                                           | Tier 3                                                                                            |
| --------------------------- | ------------------------------------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| Topology                    | Sentinel (3 sentinels, 1+1)                             | Sentinel (3 sentinels, 1+1)                                      | Redis Cluster (6+ nodes)                                                                          |
| Memory per node             | 1 GB                                                    | 4 GB                                                             | 8 GB                                                                                              |
| Budget operations estimate  | ~1,000 ops/s                                            | ~10,000 ops/s                                                    | ~100,000 ops/s                                                                                    |
| Concern separation          | Single instance (all concerns)                          | Single instance; split if ceiling signals trigger ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) | Separate instances: coordination (Sentinel), quota (Cluster), cache/pub-sub (Sentinel or Cluster) |
| Capacity ceiling monitoring | Basic (`redis_memory_used`, `redis_commands_processed`) | Enhanced (add P99 latency per store role, pub/sub channel count) | Per-instance dashboards with alerting on all ceiling signals ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes))                       |

**Single-tenant Redis topology note.** The Tier 3 Redis Cluster recommendation in the table above assumes multiple tenants whose keys distribute across hash slots via the `t:{tenant_id}:` prefix. When a Tier 3 deployment has only a **single tenant**, all tenant-prefixed keys share the same `{tenant_id}` hash tag and land on the same Redis Cluster shard — defeating horizontal sharding for those keys. (Delegation budget keys are the exception: they use `{root_session_id}` as the hash tag per R-04 and do distribute across slots even in single-tenant deployments. However, delegation budget ops/s is negligible (~400 ops/s single-tenant; see [§16.5](16_observability.md#165-alerting-rules-and-slos) reconciliation for derivation) and does not justify Cluster on its own.) The correct topology for single-tenant Tier 3 is **concern separation into multiple independent Sentinel instances** (not Redis Cluster), since each concern's ops/s remains well within single-node capacity (~150–200K ops/s):

| Concern | Topology | Estimated ops/s (Tier 3 single-tenant) |
| --- | --- | --- |
| Coordination (session leases) | Sentinel (3 sentinels, 1+1) | ~10K |
| Quota / rate-limiting | Sentinel (3 sentinels, 1+1) | ~20K |
| Delegation budget | Sentinel (3 sentinels, 1+1) | ~400 |
| Cache / hot routing / pub-sub | Sentinel (3 sentinels, 1+1) | ~50K |

Redis Cluster is warranted only when **a single concern** exceeds ~150–200K ops/s on its dedicated Sentinel instance (the single-threaded-primary throughput ceiling). At Tier 3 single-tenant scale the total ops/s across all concerns (~80K) is well below this ceiling; 3–4 Sentinel instances provide sufficient capacity with simpler operations than Cluster. The multi-tenant Redis Cluster guidance in the table above applies unchanged once there is more than one tenant, because per-tenant key distribution resumes across hash slots. Operators should evaluate their tenant count when selecting topology; the `capacityPlanning.singleTenantRedisTopology: sentinel` Helm value documents the operator's intent and suppresses the `[WARN] RedisClusterRecommended` gateway startup log that fires at Tier 3 startup when the topology appears to be single-tenant Sentinel (this is a gateway startup log message, not a Prometheus alert — consistent with the `[WARN] capacityPlanning` startup log pattern in [§16.5](16_observability.md#165-alerting-rules-and-slos)).

**Object storage** (self-managed profile — for cloud-managed equivalents see [Section 17.9](#179-deployment-answer-files)):

| Parameter                         | Tier 1                            | Tier 2                      | Tier 3                       |
| --------------------------------- | --------------------------------- | --------------------------- | ---------------------------- |
| Topology                          | Single-node (dev) or 4-node MinIO | 4-node MinIO erasure coding | 8+ node MinIO erasure coding |
| GC cycle interval (`gc.cycleIntervalSeconds`, Helm value, default 900, min 60) | 15 min | 15 min | 5 min |
| ArtifactGCBacklog alert threshold | 100                               | 1,000                       | 10,000                       |
| Estimated checkpoint write rate   | ~0.2/s                            | ~1.7/s                      | ~17/s                        |
| Estimated checkpoint upload bandwidth (avg 100 MB workspace) | ~20 MB/s | ~170 MB/s | ~1.7 GB/s |
| Estimated checkpoint upload bandwidth (max 512 MB workspace) | ~100 MB/s | ~850 MB/s | ~8.5 GB/s |
| Minimum MinIO aggregate throughput (sustained, avg workspace) | 50 MB/s | 500 MB/s | 5 GB/s |
| Minimum MinIO aggregate throughput (burst, max workspace)     | 200 MB/s | 2 GB/s | 20 GB/s |

> **Tier 3 MinIO throughput budget:** At 10,000 concurrent sessions with a 600s periodic checkpoint interval, the expected steady-state checkpoint rate is ~17/s (`10,000 / 600`). With the recommended average workspace size of ~100 MB, sustained upload bandwidth is ~1.7 GB/s; with the hard workspace limit of 512 MB ([§4.4](04_system-components.md#44-event--checkpoint-store)), worst-case burst bandwidth reaches ~8.5 GB/s. An 8-node MinIO erasure-coded cluster with NVMe-backed nodes (each delivering ~2 GB/s sequential write) provides ~10–12 GB/s aggregate write throughput, giving ~40% headroom above the 8.5 GB/s burst ceiling. Operators should monitor `lenny_checkpoint_duration_seconds` P95 ([§4.4](04_system-components.md#44-event--checkpoint-store) SLO: < 2s for ≤100 MB workspaces) and MinIO's `s3_requests_errors_total` and per-drive throughput metrics. If P95 checkpoint duration exceeds 2.5s (`CheckpointDurationHigh` alert, [§16.5](16_observability.md#165-alerting-rules-and-slos)), investigate MinIO write saturation first — add nodes or upgrade drives before increasing `periodicCheckpointIntervalSeconds`. For cloud-managed object storage (S3, GCS, Azure Blob) the bandwidth budget is the same; providers scale throughput automatically but operators should verify per-bucket throughput quotas (e.g., S3 prefix-level request rate limits) are not the bottleneck.

**Operational defaults by tier:**

| Parameter                      | Tier 1   | Tier 2     | Tier 3      |
| ------------------------------ | -------- | ---------- | ----------- |
| Token Service replicas         | 2        | 2          | 4           |
| Rate limit fail-open window    | 60s      | 60s        | 30s         |
| Quota sync interval            | 30s      | 30s        | 10s         |
| Quota drift — normal operation (Postgres sync window) ¹ | ~600 req | ~6,000 req | ~30,000 req |
| Quota drift — Redis fail-open, effective after caps ² | ≤ tenant\_limit / 2 per replica | ≤ tenant\_limit / 2 per replica | ≤ tenant\_limit / 2 per replica |
| Log volume estimate (per day)  | ~30 MB   | ~300 MB    | ~3 GB       |
| Billing event storage (13 mo)  | ~1M rows | ~10M rows  | ~100M rows  |
| Billing Redis stream MAXLEN (`billingRedisStreamMaxLen`) | 50,000 | 50,000 | 72,000 ⁵ |

> ⁵ **Tier 3 `billingRedisStreamMaxLen` derivation:** At Tier 3 the aggregate billing event rate is ~600/s. The MAXLEN must absorb events across the full outage-plus-recovery envelope, not just the raw Postgres failover RTO. The envelope combines: (a) the Postgres failover RTO of < 30s ([§12.3](12_storage-architecture.md#123-postgres-ha-requirements)); (b) the `XAUTOCLAIM` pending-entry reclaim worst case of `billingReclaimMinIdleSeconds + billingReclaimIntervalSeconds` (default: 45s) after a replica crash ([§11.2.1](11_policy-and-controls.md#1121-billing-event-stream)); and (c) flush-catch-up time once Postgres returns, during which new events continue to be published to the stream at ~600/s while the consumer group drains the backlog. The combined envelope is budgeted at 60s (Postgres RTO + a slice of reclaim/catch-up overlap that runs concurrently rather than serially with the RTO). Applied with a 2× safety factor: `600 events/s × 60s × 2 = 72,000`. For comparison, the Tier 1/2 default of 50,000 would fill in ~83 seconds at the Tier 3 rate — below the combined envelope and therefore unsafe at Tier 3. Tier 1/2 billing rates (~6/s and ~60/s respectively) are comfortably within the 50,000 default (fill time: ~2.3 hours and ~14 minutes). Operators who measure a shorter combined envelope in their environment may tune `billingRedisStreamMaxLen` downward; the floor is `peak_billing_events_per_second × postgres_failover_rto_seconds × 2` with the raw 30s RTO (i.e., 36,000 at Tier 3), which provides headroom for the raw failover only and leaves no budget for reclaim or catch-up.

> ¹ **Normal-operation drift** is the overshoot that can accumulate between Postgres checkpoints during a gateway crash: `quotaSyncIntervalSeconds × peak_request_rate_per_tenant`. At Tier 3 the 10s sync interval and a ~3,000 req/s peak rate (across all active tenants) yields ~30,000 requests total. This exposure is bounded by the Postgres MAX-rule recovery ([Section 11.2](11_policy-and-controls.md#112-budgets-and-quotas), 12.4) and is limited to the window between the last checkpoint and the crash — it does not compound across restarts.
>
> ² **Redis fail-open drift** is bounded by two independent controls ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)):
> - **Per-replica hard cap:** Each gateway replica enforces `effective_ceiling = min(tenant_limit / max(cached_replica_count, 1), per_replica_hard_cap)` where `per_replica_hard_cap` defaults to `tenant_limit / 2`. Even in a dual-outage (Redis + Endpoints simultaneously down), the cluster-wide overshoot cannot exceed `N × per_replica_hard_cap` — in all tiers this is strictly less than `N × tenant_limit`.
> - **Cumulative fail-open timer:** `quotaFailOpenCumulativeMaxSeconds` (default: 300s) limits total time spent in fail-open mode within any rolling 1-hour window. After 300s of cumulative fail-open exposure, the replica transitions to fail-closed for all quota-consuming operations, regardless of whether Redis has recovered. At a 100 req/min tenant limit with `per_replica_hard_cap = tenant_limit / 2`, the maximum per-replica overshoot within the 300s cumulative window is `50 req/min × 5 min = 250 requests` — roughly 2.5× the per-minute limit, not 300×. The 300× figure cited as a concern (30,000 req ÷ 100 req/min) applies only to the normal-operation Postgres-sync drift (row ¹) and conflates two distinct failure scenarios. Deployers with tight financial exposure should reduce `quotaFailOpenCumulativeMaxSeconds` (minimum: match the Postgres sync interval) and/or lower `per_replica_hard_cap` to a fraction of the tenant limit appropriate for their risk tolerance.

**Credential pool sizing:**

The number of credentials needed in each `CredentialPool` is driven by the pool's `maxConcurrentSessions` per-credential setting and the number of concurrent sessions that will use that provider at the target tier.

Formula:

```
min_credentials >= ceil(peak_concurrent_sessions_for_provider / maxConcurrentSessions_per_credential)
```

Where:
- `peak_concurrent_sessions_for_provider` — the number of concurrently active sessions that will hold a credential lease from this pool at peak load. For a pool that serves all sessions at a given tier, use the tier's concurrent session ceiling ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)).
- `maxConcurrentSessions_per_credential` — the per-credential concurrency limit set on the pool (default: 10 for `anthropic_direct`; higher for cloud-provider credentials that are not rate-limited per key).

**Add a safety margin** of 20–30% above the formula minimum to absorb burst demand and allow zero-downtime credential rotation (rotation removes one credential from the pool temporarily, reducing available capacity by `1 / total_credentials`):

```
recommended_credentials = ceil(min_credentials * safety_factor)
```

Where `safety_factor` is 1.3 at Tier 1 (rotation impact is high relative to small pool size), 1.2 at Tier 2, and 1.2 at Tier 3.

**Per-tier starting values** (assuming `maxConcurrentSessions: 10` per credential and all sessions use one provider):

| Parameter                             | Tier 1 | Tier 2    | Tier 3       |
| ------------------------------------- | ------ | --------- | ------------ |
| Peak concurrent sessions              | ~100   | ~1,000    | ~10,000      |
| min\_credentials (formula)            | 10     | 100       | 1,000        |
| Recommended credentials (with margin) | **13** | **120**   | **1,200**    |
| `maxConcurrentSessions` per credential | 10    | 10        | 10           |
| Rotation impact per credential removed | 7.7%  | 0.8%      | 0.08%        |

> **Tier 1 note:** With only 13 credentials, removing one during rotation reduces pool capacity by ~7.7%, temporarily limiting concurrency to ~117 sessions instead of 130. For Tier 1 deployments where peak load approaches the ceiling, either increase `maxConcurrentSessions` per credential (if the provider supports it) or hold a standby credential that is only activated during rotation windows.

> **Cloud provider credentials (Bedrock, Vertex AI, Azure):** Cloud IAM roles and service accounts typically support much higher per-credential concurrency (50–500 depending on API quota limits). For these providers, adjust `maxConcurrentSessions` upward and recalculate — fewer credentials are needed. For example, a Bedrock role with `maxConcurrentSessions: 100` at Tier 3 requires only `ceil(10,000 / 100) * 1.2 = 120` credentials rather than 1,200.

> **Multi-provider deployments:** Each provider's pool is sized independently using the formula above, using the fraction of sessions that will use that provider as `peak_concurrent_sessions_for_provider`. The total credential count across all pools scales with the tier, but individual pools can be smaller if sessions are distributed across providers.

### 17.8.3 Tier Promotion Guide

This subsection provides a structured operator checklist for promoting a deployment from Tier 2 (Growth) to Tier 3 (Scale). The prerequisites are defined in [§4.1](04_system-components.md#41-edge-gateway-replicas); this guide assembles them into an ordered, go/no-go decision workflow.

**When to use this guide:** When sustained active sessions are consistently approaching the Tier 2 ceiling (~800–900 of the 1,000-session target) and the Phase 13.5 load tests are being planned or have completed.

#### Step 1 — Run Phase 13.5 Load Tests

The Phase 13.5 benchmark harness must be executed before any Tier 3 promotion decision is made. The harness validates both gateway capacity and extraction readiness at Tier 2 peak load.

| Test | Pass Criterion | Fail Action |
| ---- | -------------- | ----------- |
| LLM Proxy extraction check | `lenny_llm_proxy_active_connections / lenny_gateway_active_sessions` sustained below **0.3:1** at Tier 2 peak for ≥15 min | Extract LLM Proxy to dedicated service before proceeding (see [§4.1](04_system-components.md#41-edge-gateway-replicas) extraction triggers) |
| Gateway GC pressure | `lenny_gateway_gc_pause_p99_ms` remains below **50 ms** at Tier 2 peak load | Investigate heap allocation and GC tuning; consider LLM Proxy extraction if not yet done |
| `maxSessionsPerReplica` validation | Phase 2 calibration values are in place (provisionals replaced with benchmark-verified values) | Re-run capacity budget calibration methodology ([§4.1](04_system-components.md#41-edge-gateway-replicas)) |
| KEDA deployment | KEDA is deployed and ScaledObjects are configured | KEDA is mandatory for Tier 3 ([§10.1](10_gateway-internals.md#101-horizontal-scaling)); deploy before proceeding |

#### Step 2 — Go/No-Go Decision

**GO — all of the following must be true:**

1. Phase 13.5 load tests passed all four checks in Step 1.
2. LLM Proxy subsystem has been extracted **or** the proxy-to-session ratio is sustainably below 0.3:1 at Tier 2 peak ([§4.1](04_system-components.md#41-edge-gateway-replicas)).
3. `lenny_gateway_gc_pause_p99_ms` remains below 50 ms at Tier 2 peak load ([§4.1](04_system-components.md#41-edge-gateway-replicas)).
4. `maxSessionsPerReplica` has been empirically calibrated (provisional values replaced); Tier 3 provisional value of 400 is in use only if condition 2 is satisfied, otherwise revert to 200 ([§4.1](04_system-components.md#41-edge-gateway-replicas)).
5. KEDA is deployed and validated ([§10.1](10_gateway-internals.md#101-horizontal-scaling), [§17.8.2](#1782-capacity-tier-reference) Path A).
6. etcd is provisioned as a dedicated cluster or confirmed sufficient for ~800 CRD status writes/s ([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle), [§17.8.2](#1782-capacity-tier-reference) controller tuning).

**NO-GO — any of the following blocks promotion:**

- Phase 13.5 tests have not been run or have outstanding failures.
- LLM Proxy ratio exceeds 0.3:1 at Tier 2 peak and extraction is not yet complete.
- `lenny_gateway_gc_pause_p99_ms` exceeds 50 ms under sustained Tier 2 load.
- Provisional `maxSessionsPerReplica` values remain in place without benchmark validation.
- KEDA is not deployed.
- etcd write latency (`etcd_disk_wal_fsync_duration_seconds` p99) consistently exceeds 25 ms under current load ([§16.5](16_observability.md#165-alerting-rules-and-slos) `EtcdWriteLatencyHigh` alert firing).

#### Step 3 — Apply Tier 3 Helm Values

Once all go criteria are satisfied, update the Helm values to the Tier 3 column from [§17.8.2](#1782-capacity-tier-reference):

1. **Gateway:** Set `autoscaling.minReplicas: 5`, `autoscaling.maxReplicas: 30`, KEDA ScaledObject queue-depth target `averageValue: 5`.
2. **`maxSessionsPerReplica`:** Use the benchmark-validated value (default Tier 3 provisional: 400, or 200 if LLM Proxy extraction is incomplete).
3. **Warm pools:** Update `minWarm` to Tier 3 baseline (1,050 per hot pool) or delegation-adjusted value from [§17.8.2](#1782-capacity-tier-reference).
4. **Controller tuning:** Apply Tier 3 controller parameters (rate limiters, `--max-concurrent-reconciles: 15`, `statusUpdateDeduplicationWindow: 250ms`).
5. **etcd:** Switch to dedicated etcd cluster or confirm `EtcdWriteLatencyHigh` alert is not firing.
6. **Redis:** Migrate to Redis Cluster topology (6+ nodes) per [§17.8.2](#1782-capacity-tier-reference).

#### Step 4 — Post-Promotion Validation

After applying Tier 3 values, monitor the following for at least 24 hours before declaring the promotion complete:

- `lenny_gateway_gc_pause_p99_ms` — must remain below 50 ms under Tier 3 load.
- `EtcdWriteLatencyHigh` alert — must not fire.
- `WarmPoolLow` alert — must not fire repeatedly (indicates `minWarm` is undersized at the new scale).
- `lenny_pod_claim_queue_wait_seconds` P99 — must remain below 2s.
- `GatewaySessionBudgetNearExhaustion` alert — must not fire more than transiently during initial load ramp.

If any post-promotion check fails, revert the Helm values to the Tier 2 column and re-evaluate the no-go condition that was triggered.

### 17.8.4 Tier Preset Files

Each supported capacity tier ships with a Helm value preset that overrides a defined subset of the base chart values:

- `deploy/helm/lenny/values-tier1.yaml`
- `deploy/helm/lenny/values-tier2.yaml`
- `deploy/helm/lenny/values-tier3.yaml`

The preset for a given tier overrides the Section-25-dependent fields enumerated in [§25.4](25_agent-operability.md#254-the-lenny-ops-service) "lenny-ops Helm Values" — specifically `monitoring.alertThresholds.*` (tighter thresholds at higher tiers), `backups.retention.*` (longer retention at higher tiers), `ops.events.streamMaxLen` (larger buffer at higher tiers), `ops.selfHealth.checkIntervalSeconds` (10 s at Tier 2/3, 30 s acceptable at Tier 1), and `monitoring.acknowledgeNoPrometheus` (defaults `false` at Tier 2/3 so that missing Prometheus is a blocking configuration error). The comment header of each preset file lists the base values it overrides; deployers layer the preset on top of the base `values.yaml` with `helm upgrade --values values.yaml --values values-tierN.yaml`.

### 17.8.5 Mandatory `lenny-ops` Deployment

`lenny-ops` ([§25.4](25_agent-operability.md#254-the-lenny-ops-service)) is **mandatory in every Lenny installation**, regardless of tier. There is no supported topology without it. The canonical component layout — including the headless `lenny-gateway-pods` service, the `lenny-ops-leader` Lease, the `lenny-backup-sa` ServiceAccount, the NetworkPolicies, and the PodDisruptionBudget `lenny-ops` — is summarized in [§25.16](25_agent-operability.md#2516-deployment-topology-summary) and rendered by the chart described in §17.1. Attempts to disable `lenny-ops` via Helm values are rejected at chart validation: the platform depends on `lenny-ops` for backup orchestration, platform-upgrade choreography, drift detection, and the agent operability surface that `lenny-ctl` and autonomous agents consume.

### 17.8.6 Image Registry and Air-Gap

The Helm chart's `platform.registry.*` values are the single source for all Lenny component image references (gateway, `lenny-ops`, controllers, `lenny-backup`). The canonical value block is documented in [§25.4](25_agent-operability.md#254-the-lenny-ops-service) "lenny-ops Helm Values"; in summary:

- `platform.registry.url` — the registry host and root path to use for all Lenny images. Empty means "use the Lenny-published registry".
- `platform.registry.pullSecretName` — Kubernetes `ImagePullSecret` for private registries.
- `platform.registry.requireDigest` — when `true`, all image references MUST be digest-pinned (no tag-only references). Required for supply-chain-strict deployments.
- `platform.registry.overrides` — per-component registry overrides (e.g., point only `lenny-ops` at a mirrored path).

**Air-gapped deployments** ([§25.8](25_agent-operability.md#258-platform-lifecycle-management) Air-Gapped Support) mirror all Lenny-published images into a private registry, set `platform.registry.url` to that registry, and rely on `--skip-preflight` for environments where the preflight Job cannot reach the mirrored registry before it is populated. The procedure is documented in `docs/deployment/air-gap.md`. The chart's `ImageResolver` shared package (`pkg/common/registry/resolver.go`) composes every image reference from `platform.registry.*`, ensuring the gateway, `lenny-ops`, controllers, `lenny-backup`, and the warm-pool controller all honor the same registry configuration.

### 17.9 Deployment Answer Files

> **Note:** Prior to this section, Sections 17.8.1 (Operational Defaults), 17.8.2 (Capacity Tier Reference), and 17.8.4 (Tier Preset Files) provide per-tier sizing and tunable defaults.

Lenny's data store layer (Postgres, Redis, object storage) is accessed exclusively through pluggable interfaces ([Section 12.6](12_storage-architecture.md#126-interface-design)). The implementation behind each interface varies by deployment environment. Rather than collapsing that variability onto a single "profile" axis, Lenny ships a catalog of **answer files** composed along several orthogonal dimensions. Each answer file is a normal Helm values fragment; operators layer them with `helm install -f base.yaml -f answer-file.yaml -f values-tierN.yaml -f overrides.yaml`.

**Design principle.** Lenny must not depend on any single cloud provider. Cloud-managed backends use provider-native services for operational simplicity; self-managed backends are always a fully supported first-class path. Helm values select the active backends; the gateway and controllers are unaware of which backend is active.

#### 17.9.1 Composition Dimensions

Every Lenny installation is described by a tuple of orthogonal choices. The catalog is organized so that each dimension maps to a separate answer-file stanza; files can be mixed and matched.

| Dimension | Values | Drives |
|---|---|---|
| **Environment** | `local` \| `dev` \| `staging` \| `prod` | Alert thresholds, log verbosity, `LENNY_DEV_MODE`, TLS strictness, `acknowledgeNoPrometheus` default |
| **Cluster type** | `laptop` (k3s/kind) \| `eks` \| `gke` \| `aks` \| `openshift` \| `vanilla` (generic k8s) | CNI assumptions, StorageClass defaults, cloud-provider IAM integration, LoadBalancer behavior |
| **Backends** | `cloud-managed` \| `self-managed` \| `embedded` | Postgres (RDS/CloudSQL/Azure DB vs CloudNativePG/Patroni vs embedded), Redis (ElastiCache/Memorystore/Azure Cache vs Sentinel/Cluster vs miniredis), object storage (S3/GCS/ABS vs MinIO vs local disk) |
| **Capacity tier** | `tier1` \| `tier2` \| `tier3` | Gateway replica counts, warm-pool baselines, controller rate limiters, Redis topology — see [§17.8.2](#1782-capacity-tier-reference) |
| **Isolation profile** | `baseline` \| `sandboxed` (gVisor) \| `hypervisor` (Kata) | Default RuntimeClass for seeded runtimes, T4 webhook requirements |

Each dimension has an independent default so that operators only need to override the axes that differ from the chart base. An `eks-small-team.yaml` answer file, for example, declares cluster type = `eks` and backends = `cloud-managed` but leaves environment and capacity tier to be specified separately.

#### 17.9.2 Answer File Catalog

The chart ships with the following curated answer files under `deploy/helm/lenny/answers/`. Each is documented with a comment header that lists the dimensions it sets and the dimensions it leaves open:

| Answer file | Dimensions fixed | Typical layering |
|---|---|---|
| `answers/laptop.yaml` | cluster=`laptop`, backends=`embedded`, environment=`local`, tier=`tier1` | Used as-is; equivalent to `lenny up` ([§17.4](#174-local-development-mode-lenny-dev)) |
| `answers/docker-compose.yaml` | cluster=n/a, backends=`self-managed` (containerized), environment=`dev` | Layered with `values-tier1.yaml` for Tier 2 dev ([§17.4](#174-local-development-mode-lenny-dev)) |
| `answers/eks-small-team.yaml` | cluster=`eks`, backends=`cloud-managed` (RDS + ElastiCache + S3), environment=`prod`, tier=`tier1` | Layered with `values-tier1.yaml` or `values-tier2.yaml` depending on load |
| `answers/eks-production.yaml` | cluster=`eks`, backends=`cloud-managed`, environment=`prod`, tier=`tier2` (Tier 3 capable) | Layered with `values-tier2.yaml` or `values-tier3.yaml` |
| `answers/gke-production.yaml` | cluster=`gke`, backends=`cloud-managed` (CloudSQL + Memorystore + GCS), environment=`prod` | Layered with `values-tierN.yaml` |
| `answers/aks-production.yaml` | cluster=`aks`, backends=`cloud-managed` (Azure DB + Azure Cache + ABS), environment=`prod` | Layered with `values-tierN.yaml` |
| `answers/openshift-self-managed.yaml` | cluster=`openshift`, backends=`self-managed` (CloudNativePG + Redis Sentinel + MinIO), environment=`prod` | Layered with `values-tierN.yaml` |
| `answers/bare-metal-self-managed.yaml` | cluster=`vanilla`, backends=`self-managed`, environment=`prod` | Layered with `values-tierN.yaml` |
| `answers/airgap-self-managed.yaml` | cluster=`vanilla`, backends=`self-managed`, `platform.registry.*` set to a private mirror, `preflight.skipNetworkProbes: true` | Layered with `values-tierN.yaml`; requires operator to set mirror details |

**Adding new answer files.** Operators may publish their own answer files in their own repos (e.g., `answers/acme-eks.yaml`). Files are just values fragments; no plugin registration is required. The chart CI lints every shipped answer file against `values.schema.json` ([§17.6](#176-packaging-and-installation)) so that committed files cannot drift out of sync with the schema.

**Wizard integration.** The `lenny-ctl install` wizard ([§17.6](#176-packaging-and-installation)) auto-suggests an answer-file base from the detection phase (e.g., `eks-small-team.yaml` when AWS EKS is detected). Operators who prefer hand-written values can skip the wizard entirely and run `helm install -f <answer-file>.yaml -f values-tierN.yaml -f overrides.yaml` directly — both paths are first-class.

**Tier layering.** The capacity tier is intentionally a separate file (`values-tier1.yaml`, `values-tier2.yaml`, `values-tier3.yaml`, see [§17.8.4](#1784-tier-preset-files)) rather than baked into every answer file. Operators promote between tiers by swapping the tier file without rewriting the base answer file.

#### 17.9.3 Cloud-Managed Backends

Answer files whose **backends** dimension is `cloud-managed` omit PgBouncer, Redis Sentinel, and MinIO resources and reference provider-native endpoints instead. Provider-native services handle HA, replication, scaling, patching, and backups.

| Component                         | Cloud-Managed Equivalent                     | Provider Examples                                                           | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| --------------------------------- | -------------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Postgres**                      | Managed relational database with multi-AZ HA | AWS RDS for PostgreSQL, GCP Cloud SQL, Azure Database for PostgreSQL        | Same schema, same RLS enforcement. Managed service handles failover, backups, encryption at rest.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| **Connection pooler** (PgBouncer) | Provider connection proxy                    | AWS RDS Proxy, GCP Cloud SQL Auth Proxy, Azure PgBouncer (built-in)         | Must support transaction-mode pooling for RLS compatibility ([Section 12.3](12_storage-architecture.md#123-postgres-ha-requirements)). RDS Proxy supports `SET LOCAL` in transaction mode. Cloud SQL Auth Proxy terminates IAM auth but does not pool — if using Cloud SQL, deploy PgBouncer or pgcat alongside it, or use AlloyDB with built-in pooling. **`connect_query` limitation:** Most cloud-managed proxies do **not** support `connect_query` or an equivalent initialization hook, so the `__unset__` sentinel cannot be set on connection checkout. Deployments using such proxies **must** rely on the per-transaction tenant validation trigger (`lenny_tenant_guard`) as the sole programmatic defense against stale `app.current_tenant` values from prior connections — see [Section 12.3](12_storage-architecture.md#123-postgres-ha-requirements) for details. The preflight Job verifies this trigger exists when `connectionPooler = external`. |
| **Redis**                         | Managed cache/data store with HA             | AWS ElastiCache for Redis, GCP Memorystore for Redis, Azure Cache for Redis | Same AUTH + TLS requirements. ElastiCache Cluster Mode and Memorystore provide the horizontal sharding that self-managed Redis Cluster provides at Tier 3. Logical concern separation ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) maps to separate ElastiCache replication groups or Memorystore instances.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| **Object storage** (MinIO)        | Provider-native object storage               | AWS S3, GCP Cloud Storage, Azure Blob Storage                               | `ArtifactStore` interface uses S3-compatible API; S3 and GCS are natively compatible. Azure Blob requires the S3-compatible gateway or a thin `ArtifactStore` implementation using Azure SDK. Encryption at rest is provider-managed (enabled by default on all three providers). **Versioning and lifecycle rules must be configured by the deployer** — see [Cloud Object Storage Lifecycle Requirements](#1794-cloud-object-storage-lifecycle-requirements) below.                                                                                                                                                                                                                    |

**Helm configuration (cloud-managed):**

```yaml
backends: cloud-managed  # selected by the answer file; see §17.9.2

postgres:
  # No PgBouncer Deployment created; gateway connects through provider proxy
  connectionPooler: external # "external" = provider-managed, "pgbouncer" = self-managed
  dsn: "postgres://..." # Provider-issued endpoint (e.g., RDS Proxy endpoint)
  readDsn: "postgres://..." # Read replica endpoint (provider reader endpoint)

redis:
  provider: external # "external" = provider-managed, "sentinel" / "cluster" = self-managed
  endpoints:
    - "rediss://elasticache-primary.example.com:6379"
  # For Tier 3 concern separation, configure per-role endpoints:
  # coordinationEndpoints: [...]
  # quotaEndpoints: [...]
  # cacheEndpoints: [...]

objectStorage:
  provider: s3 # "s3" | "gcs" | "azure" | "minio"
  bucket: "lenny-artifacts"
  region: "us-east-1"
  # Encryption, versioning, lifecycle managed by provider
```

#### 17.9.4 Cloud Object Storage Lifecycle Requirements

Cloud-managed object storage buckets must be configured with the same lifecycle rules that [Section 12.5](12_storage-architecture.md#125-artifact-store) mandates for MinIO — bucket versioning (to prevent accidental overwrites during checkpoint upload) and delete-marker / noncurrent-version expiration (to prevent `ListObjects` performance degradation at scale). The Helm chart post-install Job configures these rules for MinIO via `mc ilm add`. For cloud providers, the deployer must apply equivalent rules before installation; the preflight Job validates them (see [Checks performed](#checks-performed) in [Section 17.6](#176-packaging-and-installation)).

**Required rules (all providers, bucket-wide — no prefix filter):**

| Rule | Setting | Rationale |
|---|---|---|
| Bucket versioning | Enabled | Prevents accidental overwrites of checkpoint objects |
| Delete-marker expiration | 1 day | Prevents delete-marker accumulation from degrading `ListObjects` |
| Noncurrent-version expiration | 1 day | Removes superseded object versions after GC deletion |

**Per-provider configuration:**

**AWS S3:**
```json
{
  "Rules": [
    {
      "ID": "lenny-delete-marker-expiry",
      "Status": "Enabled",
      "Filter": {},
      "Expiration": { "ExpiredObjectDeleteMarker": true }
    },
    {
      "ID": "lenny-noncurrent-version-expiry",
      "Status": "Enabled",
      "Filter": {},
      "NoncurrentVersionExpiration": { "NoncurrentDays": 1 }
    }
  ]
}
```
Apply with: `aws s3api put-bucket-versioning --bucket <bucket> --versioning-configuration Status=Enabled` then `aws s3api put-bucket-lifecycle-configuration --bucket <bucket> --lifecycle-configuration file://lenny-lifecycle.json`

**GCP Cloud Storage:**
```json
{
  "lifecycle": {
    "rule": [
      {
        "action": { "type": "Delete" },
        "condition": { "isLive": false, "daysSinceNoncurrentTime": 1 }
      },
      {
        "action": { "type": "Delete" },
        "condition": { "isLive": true, "daysSinceCustomTime": 1, "matchesPrefix": [], "numNewerVersions": 1 }
      }
    ]
  }
}
```
Apply with: `gcloud storage buckets update gs://<bucket> --versioning` then `gcloud storage buckets update gs://<bucket> --lifecycle-file=lenny-lifecycle.json`

**Azure Blob Storage:**
Azure Blob versioning and lifecycle management are configured via Storage Account policies:
```json
{
  "rules": [
    {
      "name": "lenny-version-expiry",
      "enabled": true,
      "type": "Lifecycle",
      "definition": {
        "actions": {
          "version": { "delete": { "daysAfterCreationGreaterThan": 1 } }
        },
        "filters": { "blobTypes": ["blockBlob"] }
      }
    }
  ]
}
```
Apply with: `az storage account blob-service-properties update --account-name <account> --enable-versioning true` then `az storage account management-policy create --account-name <account> --policy @lenny-lifecycle.json`

> **Note:** Azure Blob does not natively emit S3-style delete markers. When using the Azure Blob S3-compatible gateway, delete markers are emulated; noncurrent-version expiration is sufficient to bound storage growth.

#### 17.9.5 Self-Managed Backends

Answer files whose **backends** dimension is `self-managed` deploy PgBouncer, Redis Sentinel (or Cluster), and MinIO as Kubernetes workloads alongside Lenny's own components. Use this when deploying on bare-metal, on-premises Kubernetes, or any environment without managed database/cache services.

| Component             | Self-Managed Implementation                          | Notes                                                                                        |
| --------------------- | ---------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| **Postgres**          | CloudNativePG operator or Patroni on Kubernetes      | See [Section 12.3](12_storage-architecture.md#123-postgres-ha-requirements) for HA, encryption, backup requirements.                                    |
| **Connection pooler** | PgBouncer Deployment (2+ replicas) with PDB          | See [Section 12.3](12_storage-architecture.md#123-postgres-ha-requirements) for sizing, pool mode, readiness probe, and monitoring.                     |
| **Redis**             | Redis Sentinel (Tiers 1–2) or Redis Cluster (Tier 3) | See [Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) for topology, TLS, AUTH, failure behavior, and concern separation triggers. |
| **Object storage**    | MinIO with erasure coding                            | See [Section 12.5](12_storage-architecture.md#125-artifact-store) for HA topology, encryption, tenant isolation, and GC.                      |

**Helm configuration (self-managed):**

```yaml
backends: self-managed  # selected by the answer file; see §17.9.2

postgres:
  connectionPooler: pgbouncer
  pgbouncer:
    replicas: 2
    poolMode: transaction
    defaultPoolSize: 25
    # See Section 17.8 for per-tier sizing

redis:
  provider: sentinel # "sentinel" | "cluster"
  sentinels:
    - "redis-sentinel-0.redis:26379"
    - "redis-sentinel-1.redis:26379"
    - "redis-sentinel-2.redis:26379"

objectStorage:
  provider: minio
  endpoint: "http://minio.lenny-system:9000"
  bucket: "lenny-artifacts"
```

#### 17.9.6 Embedded Backends (Tier 0)

Answer file `answers/laptop.yaml` selects backends=`embedded`, the mode used by `lenny up` ([§17.4](#174-local-development-mode-lenny-dev) Tier 0): embedded Postgres (single-node bundle), in-process Redis, local-disk artifact storage, embedded k3s. This mode requires zero external cloud or cluster dependencies and is the primary path for laptop-scale evaluation of Lenny. Tier 1 (`make run`) and Tier 2 (`docker compose up`) are developer-oriented paths for contributors, documented in [§17.4](#174-local-development-mode-lenny-dev).

#### 17.9.7 Backend-Invariant Requirements

Regardless of backend selection (`cloud-managed`, `self-managed`, or `embedded`), the following requirements apply uniformly:

- **Transaction-mode pooling** for Postgres connections (RLS compatibility)
- **RLS checkout defense:** Either `connect_query` sentinel (self-managed PgBouncer) **or** per-transaction tenant validation trigger (cloud-managed poolers without `connect_query` support) — exactly one must be active per deployment; see [Section 12.3](12_storage-architecture.md#123-postgres-ha-requirements). The embedded-Postgres Tier 0 mode ships the trigger pre-installed.
- **Redis AUTH + TLS** (no plaintext connections, no unauthenticated access): Redis is deployed with `tls-auth-clients yes` and plaintext port disabled (`port 0`); PgBouncer is deployed with `client_tls_sslmode = require`. See [Section 10.3](10_gateway-internals.md#103-mtls-pki) for the full server-side enforcement requirements, startup TLS probe, and integration test requirements (NET-004). Tier 0 embedded Redis runs loopback-only and is exempt from AUTH/TLS.
- **Tenant key prefix** (`t:{tenant_id}:`) enforced at the Redis wrapper layer
- **S3-compatible API** for object storage (all cloud and self-managed providers above satisfy this; the Tier 0 local-disk driver implements the same `ArtifactStore` interface)
- **Encryption at rest** for all persistent stores (exempt in Tier 0 embedded mode, which prints the non-suppressible production-warning banner documented in [§17.4](#174-local-development-mode-lenny-dev))
- **Interface contracts** ([Section 12.6](12_storage-architecture.md#126-interface-design)) are identical across backends — the gateway does not branch on backend selection

