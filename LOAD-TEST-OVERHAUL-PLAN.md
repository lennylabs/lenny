# Load Testing Overhaul — Plan

Working document for the `load-test-overhaul` branch. Referenced during the build process. Not part of `/spec`.

## 1. Goals and success criteria

Bugs that today escape to tier-7 cloud must be caught earlier. The recent 48 hours of fixes are direct evidence of where the pipeline is failing today:

| Commit | Bug | Tier that surfaced it | Where it should have been caught |
|:--|:--|:--|:--|
| 9b5ba3e | Non-atomic slot reservation under N-way race | tier-7 cloud | tier-7a (component, miniredis, race detector) |
| 2b20338 | Ordering window between Redis counter and SSA phase mirror | tier-7 cloud | tier-7a (component, simulated stale-mirror admission) |
| f54b7bb | Missing `delete sandboxclaims` RBAC verb plus silent error suppression | tier-7 cloud | tier-1 unit (RBAC manifest assertion) plus tier-2 (terminate failure surfacing) |
| c503666 | Concurrent-mode session close used the session-mode reclaim path | tier-7 cloud | tier-1 unit (PodExecutor.Close branch on BindResult.SlotID) |
| 0b7c71c | client-go default QPS=5 throttled at 5 req/s | tier-7 cloud | tier-7a (component, gateway-against-fake-apiserver throttle assertion) |
| 1d26c2d | Admission webhook duplicate-claim rule rejected concurrent-mode siblings | tier-2 / tier-5 | tier-1 unit (added once the bug shape was known) |

Each defect is a concurrency-ordering, atomicity, or error-suppression issue that is structurally reproducible in a few seconds of in-process load when the test surface exists. None require a real cloud, real Kubernetes, or real Postgres to expose. The overhaul builds the missing surface.

**Success criteria.** A defect of the kind above must be caught no later than tier-7a (local) for at least 80% of cases. Tier-7b (Kind) and tier-12 (cloud) catch the residue that genuinely requires cluster-level state. Tier-12 runs become exceptional events that ratify a release rather than the place where load bugs are first observed.

## 2. New tier topology

| Tier | Name | Profile | Runs on | Cadence | Wall-clock target |
|:--|:--|:--|:--|:--|:--|
| 7a (new) | `load_local` | local | bare process plus stubs plus miniredis plus testdb | every PR | 2–5 min |
| 7b (renamed) | `load_kind` | kind | Kind cluster with real gateway, controller, token-service | every PR (smoke) plus nightly (extended) | 10–20 min |
| 8–11 | unchanged | | | | |
| 12 (new) | `load_cloud` | cloud | cloud Lenny plus external load runners | pre-release and on-demand | 30 min to several hours |

Tier 7 becomes a two-stage gate. Tier 12 sits after every other tier so cloud spend only fires once everything below is green.

Directory and tag renames:

- `tests/tier7_load/` to `tests/tier7b_load_kind/`; build tag `load` to `load_kind`.
- `tests/tier7_load_cloud/` to `tests/tier12_load_cloud/`; build tag `load_cloud` keeps its name.
- `tests/tier7a_load_local/` is new; build tag `load_local`.
- `cmd/lenny-test/tiers.go` adds `tierLoadLocal`, renames `tierLoad` to `tierLoadKind`, and adds `tierLoadCloud`.
- `allTiers()` order becomes: `static`, `unit`, `component`, `contract`, `integration`, `e2e_kind`, `e2e_cloud`, `load_local`, `load_kind`, `chaos`, `security`, `conformance`, `docs`, `load_cloud`.

## 3. Tier-7a: Local component load tests

### 3.1 Design

Tier-7a runs two coordinated modes under the build tag `load_local`.

**Mode A: per-component benches.** Go benchmark-style hot-path loops over each package's public surface with the race detector on. Each candidate package gains a `bench_load_test.go` that exercises the documented concurrency invariants with N goroutines using miniredis, fake clocks, and in-memory state. Measures throughput, P99, and asserts no race plus invariant preservation.

**Mode B: in-process multi-component harness.** A new `tests/testinfra/inproc` package boots a single-binary Lenny against in-memory state, miniredis for the slot counter and idempotency store, an embedded Postgres-compatible adapter for `sessionstore` and `auditstore`, and a fake Kubernetes API surface backing the gateway's client-go calls. The gateway HTTP listener binds to a loopback port. The pure-Go load driver drives that listener.

The harness avoids Kind entirely. A full tier-7a run completes in a few minutes on a laptop.

### 3.2 Stubs

| Dependency | Stub strategy |
|:--|:--|
| Redis | `miniredis` (already used by `pkg/gateway/slotcounter`); promote into a shared `tests/testinfra/redis` helper |
| Postgres | embedded `pgx`-compatible in-memory adapter, with a `dockertest` shared-container fallback for the few tests that need real SQL behaviour |
| Kubernetes API | `tests/testinfra/fakekube` built on a `client-go` fake.Clientset wired to a controller-runtime fake.Client. The fake honours `ResourceVersion` so SSA conflicts surface truthfully and supports configurable watch-event delay so admission-ordering bugs are reproducible. |
| KMS, OIDC, LLM providers, SIEM | reuse `tests/testinfra/stubs/{kms,oidc,llmprovider,siem}` (already in tree) |
| Adapter and runtime containers | new `tests/testinfra/stubs/runtime`, an in-process gRPC server that speaks the adapter wire format with configurable latency, error injection, and concurrency caps |
| Cloud metadata and IMDS | out of scope for tier-7a |

The fake Kubernetes surface is the highest-effort piece. The recent slot-claim regressions surfaced because SSA conflict handling and watch-notification ordering matter, so the fake reproduces both: a write that loses a CAS, and watch events delivered with configurable delay relative to the corresponding write.

### 3.3 Load driver

The canonical tier-7a driver is a pure-Go load generator under `tests/testinfra/loadgen`. Scenarios are Go files under `tests/tier7a_load_local/scenarios/<name>/scenario.go` and each implements:

```
type Scenario interface {
    Setup(ctx context.Context, env *Env) error
    Run(ctx context.Context, vu int) error
    Teardown(ctx context.Context) error
}
```

The driver supports constant-VU, constant-arrival-rate, and ramping-VU profiles. It emits the same percentile, throughput, and error metrics as a k6 summary so the baseline format stays interoperable with tier-7b and tier-12.

The pure-Go driver removes the k6 binary as a hard dependency for tier-7a, integrates with `go test` and `go bench`, and gives the harness deterministic test output. k6 stays as the driver for tier-7b and tier-12 where the existing JavaScript scenarios already cover the surface.

### 3.4 Regression scenarios derived from recent bugs

Scenarios that re-create the failure modes observed in the last 48 hours of fixes:

| Scenario | Hot path | Asserts |
|:--|:--|:--|
| `slot_counter_race` | `pkg/gateway/slotcounter` with 50–500 goroutines against miniredis | exactly `maxConcurrent` reservers succeed; no overcommit |
| `claim_admission_ordering` | gateway slot reserve, CREATE SandboxClaim, SSA phase mirror, with mirror artificially lagged | webhook admits both first and Nth slot claims regardless of mirror lag |
| `terminate_path_branching` | `PodExecutor.Close` over both session-mode and concurrent-mode bindings | no leaked SandboxClaim; correct Sandbox phase transition |
| `clientgo_throttle_floor` | gateway at default QPS=5 against configured QPS=100 driving create_session | P99 latency floor is bounded and the configured QPS lifts it |
| `idempotency_replay_race` | `pkg/idempotency` with N goroutines replaying the same key under contention | exactly one body executes; replays observe the original 2xx |
| `quota_decrement_race` | `pkg/quota` decrement under N goroutines per tenant | global, then tenant, then user ordering holds; no negative remaining |
| `audit_chain_concurrent` | `pkg/audit` hash-chain append from N goroutines | chain integrity holds; serialization correct |
| `lease_extension_race` | `pkg/leaseextension` proactive renewal with overlapping timers | one renewal per epoch; no double-charge |
| `circuit_breaker_state_machine` | `pkg/middleware/circuitbreaker` from many goroutines | state transitions monotone per epoch |
| `pubsub_fanout` | `pkg/pubsub` with 1000 subscribers and 10k events per second | no drop, no duplication, no reorder within a topic |

Every scenario runs with `-race` set. Run-time budget: each scenario ≤ 15 seconds, total tier ≤ 5 minutes.

### 3.5 New scenarios beyond regression coverage

Wave 3 expands the catalogue with scenarios that are not tied to a specific past bug but exercise concurrency, throughput, isolation, and resource-exhaustion paths that the existing suite covers thinly or not at all:

**Component-isolated benches.**

| Scenario | Package | Asserts |
|:--|:--|:--|
| `tokenservice_issue_burst` | `pkg/tokenservice` | issue throughput, P99 sign latency under N concurrent issuers, no duplicate jti |
| `webhook_admission_latency` | `pkg/admission` | admission decisions under 1000 req/s, P99 < 50ms, no panics |
| `controller_reconcile_rate` | `pkg/controller` | reconcile throughput on N pending Sandbox events, no missed updates |
| `pgtenant_rls_isolation_load` | `pkg/pgtenant` | concurrent multi-tenant writes preserve RLS; no cross-tenant rows visible |
| `auth_jwt_verify_throughput` | `pkg/auth/jwt` | verify throughput at 10k tokens/s, P99 < 1ms; tampered tokens always rejected |
| `credassign_lease_rotation` | `pkg/credassign` | concurrent lease rotations across overlapping windows; no leaked old leases |
| `checkpointer_concurrent` | `pkg/checkpointer` | N sessions checkpointing simultaneously; no chunk corruption, no duplicate IDs |
| `experiment_bucket_determinism` | `pkg/experiment` | bucketing stable under concurrent assigns; HMAC distribution within tolerance |
| `pubsub_slow_consumer` | `pkg/pubsub` | one slow consumer does not stall fast consumers; documented backpressure path holds |
| `sessionstore_write_amplification` | `pkg/sessionstore` | concurrent SetStatus across 1000 sessions does not inflate write count |

**Multi-component harness scenarios.**

| Scenario | Path exercised | Asserts |
|:--|:--|:--|
| `tenant_isolation_load` | two tenants driving the gateway simultaneously, one over-quota | the over-quota tenant's failures do not affect the within-quota tenant's P99 |
| `mixed_workload` | sessions + tasks + streaming + delegation interleaved through one gateway | total throughput meets sum-of-isolated targets minus a documented overhead budget |
| `streaming_reconnect_storm` | 500 streaming clients drop and reconnect within a 5-second window | reattach success rate at 100%, event sequence preserved per session |
| `large_workspace_upload` | concurrent multi-MB workspace uploads through the gateway | bytes-throughput target met; upload-queue depth bounded; no OOM |
| `delegation_depth_n` | depth-10 delegation trees with fan-out 5 at each level | cycle detection holds under N concurrent roots; budget enforcement correct |
| `goroutine_leak_long_run` | a 60-second steady-state run at moderate load | goroutine count returns to baseline within tolerance after teardown |
| `memory_leak_long_run` | a 60-second steady-state run at moderate load | heap returns to baseline within tolerance after teardown; no RSS drift |
| `pg_pool_exhaustion` | gateway driven past Postgres pool capacity | back-pressure observed at the gateway, not a panic; recovers when load drops |
| `redis_disconnect_midflight` | miniredis stopped and restarted mid-run | slot reservations and idempotency reads fail closed and recover on reconnect |
| `clock_skew_admission` | fake clock skewed beyond the ±1s window between gateway and token-service | tokens past the skew bound rejected; tokens inside accepted |
| `webhook_tls_rotation_under_load` | webhook serving cert rotated while admission traffic is in flight | zero failed admissions during rotation |
| `oversized_payload_rejection` | requests at and past the configured body-size cap | cap enforced consistently across all endpoints; clean 413 with the documented envelope |
| `idempotency_cache_eviction` | N idempotency keys past the cache capacity | eviction policy holds; no key returns a stale prior response |
| `audit_sink_backpressure` | audit SIEM sink configured with artificial latency | gateway request latency bounded; audit queue depth bounded; no event loss |
| `connector_oauth_refresh_race` | concurrent refresh of the same connector's OAuth token | exactly one refresh occurs per epoch; siblings observe the refreshed value |
| `runtime_adapter_slow_response` | adapter stub configured with 5s response delays | gateway timeout enforcement uniform across endpoints; no zombie sessions |
| `crd_watch_event_flood` | N rapid Sandbox status changes against the fake K8s | controller absorbs the flood without dropping; no missed terminal transitions |
| `error_injection_matrix` | systematic error injection at each external dependency (KMS, LLM, Postgres, Redis) | every failure path either propagates an error envelope or fails open with a documented metric increment |

Wave 3 lands all scenarios in §3.4 plus the catalogue above. New scenarios may be added to §3.5 between waves as additional gaps come to light.

## 4. Tier-7b: Kind e2e load (renamed)

### 4.1 What stays

Every existing scenario under `tests/tier7_load/scenarios/` carries over. The PR-cadence smoke profile (20 VUs, ~25s, baseline diff) stays. The baseline corpus stays.

### 4.2 What changes

- Directory rename `tests/tier7_load` to `tests/tier7b_load_kind`.
- Build tag `load` to `load_kind`.
- `cmd/lenny-test` tier name `load` to `load_kind`.
- The `requireCloudLoad` skip helper becomes `requireTier12` with the same body, since tier-7b no longer talks to the cloud at all.
- Cases that cannot be exercised in tier-7a get explicit comments naming the Kubernetes primitive they require (real watch semantics, real CRD admission round-trip, real CSI mount, real pod-creation latency from the scheduler, real chart rollout).

### 4.3 What gets added

Cases that genuinely require Kind:

- Real `kubelet` pod-startup latency under N concurrent SandboxClaim CREATE.
- Real `kube-scheduler` placement under node affinity and topology spread.
- Real `kubectl rollout` of the gateway under concurrent traffic.
- Real `cert-manager` and webhook TLS rotation under load.
- Real NetworkPolicy enforcement count under concurrent egress.
- Per-RuntimeClass startup-latency comparison (runc against gVisor) on a single Kind cluster with both runtimes available.

Tier-7b owns only what requires Kind. Anything reproducible in-process moves to tier-7a.

## 5. Tier-12: Cloud load orchestrator

### 5.1 Script split

Current state: `scripts/cloud/<provider>/run-load.sh` provisions the cluster, applies the chart, deploys the load fixture, port-forwards, runs `lenny-test`, and leaves the cluster up.

Proposed state:

| Script | Responsibility |
|:--|:--|
| `scripts/cloud/<p>/up.sh` | Provision EKS / AKS / GKE plus managed RDS / Cloud SQL / Flexible Server plus ElastiCache / Memorystore / Cache for Redis. No application code, no load fixture. Idempotent. |
| `scripts/cloud/<p>/install-lenny.sh` (new) | Install or upgrade the chart. Wait for ready. Idempotent. Separated so tests can re-run against an existing install without re-applying terraform. |
| `scripts/cloud/<p>/up-loadgen.sh` (new) | Provision the dedicated load-runner instance pool plus the metrics-collector instance. Idempotent. Wraps the `loadgen` terraform module (see §5.2). |
| `scripts/cloud/<p>/up-loadctl.sh` (new) | Provision `lenny-loadctl` (the per-cloud managed-container deployment) plus its managed Postgres. Idempotent. Wraps the `loadctl` terraform module (see §5.5). |
| `scripts/cloud/<p>/run-load.sh` (rewritten) | Pre-flight checks only: assert cluster up, chart installed, load runners up, loadctl reachable. Then trigger a load run through the loadctl API. No terraform calls. No helm calls. |
| `scripts/cloud/<p>/down-loadgen.sh` (new) | Tear down the load runners. Idempotent. |
| `scripts/cloud/<p>/down-loadctl.sh` (new) | Tear down loadctl plus its Postgres. Idempotent. |
| `scripts/cloud/<p>/down.sh` | Tear down everything else. Idempotent. |

Every destructive operation lives in its own explicit script. The current `KEEP_CLUSTER=1` default disappears because no script in the run path ever destroys.

### 5.2 External load runners (off-cluster)

Load is generated from the cloud provider but outside the Kubernetes cluster. Per-cloud parity at launch:

| Cloud | Compute pool | Work dispatch | Instance type |
|:--|:--|:--|:--|
| AWS | Auto Scaling Group in the EKS VPC, on private subnets | SQS queue | `c6i.2xlarge` |
| GCP | Managed Instance Group in the GKE VPC, on private subnets | Pub/Sub topic | `c2-standard-8` |
| Azure | Virtual Machine Scale Set in the AKS VNet, on private subnets | Service Bus queue | `Standard_F8s_v2` |

Each instance runs the new `lenny-loadrunner` agent plus the `k6` binary. Instances route to the gateway over a PrivateLink endpoint, a Private Service Connect endpoint, or an Azure Private Endpoint, so traffic stays inside the cloud network.

**Terraform.** A new `loadgen` module under `deploy/terraform/cloud/<provider>/loadgen/` per cloud. Inputs: cluster reference (VPC ID, subnet IDs), pool size, instance type, SSH key reference, container registry path for the `lenny-loadrunner` image, work-queue ARN/topic/queue reference. Outputs: pool autoscaling group name (or MIG / VMSS reference), metrics-collector instance address, work-queue endpoint. Invoked through `up-loadgen.sh` and torn down through `down-loadgen.sh`.

**`lenny-loadrunner` (new binary, `cmd/lenny-loadrunner`).**

- Long-running process on each runner instance.
- Registers with the control plane (heartbeats, health, capacity).
- Receives a `RunScenario` instruction containing scenario name, k6 script reference (S3 / GCS / Blob URL or embedded), VU and rate and duration, target URL, auth bundle, and run ID.
- Executes `k6` in a subprocess; streams k6 output metrics to the control plane in real time; on completion uploads the full k6 JSON report to object storage.
- Implements clean cancel on `StopScenario`.

The agent is cloud-agnostic. The work-dispatch driver lives behind an interface in `pkg/loadrunner/dispatch` with three implementations (SQS, Pub/Sub, Service Bus). The agent picks the implementation from a single environment variable set by its instance template.

### 5.3 Metrics pipeline

**Prometheus deployed alongside Lenny.** A `prometheus` Helm chart deploys into the same cloud cluster as Lenny. The scrape target set is the one already defined in spec §16.9 (gateway port 9090, controller, token-service, `lenny-ops` port 9090). Scrape interval 5–10s during a load run. The chart's existing `prometheusrule.yaml` and alerts compile against this Prometheus with no changes.

**Cloud provider metrics ingestion.** A `cloud-metrics-collector` sidecar (new, running on the metrics-collector instance) polls CloudWatch, Cloud Monitoring, and Azure Monitor for:

- Node CPU, memory, and disk for the cluster.
- Database connections, IOPS, and replication lag for RDS / Cloud SQL / Flexible Server.
- CPU, evictions, and hit rate for ElastiCache / Memorystore / Cache for Redis.
- Request count and latencies for the ALB / Cloud Load Balancer / Azure Load Balancer.

It emits Prometheus-format metrics that the load-run Prometheus scrapes.

**k6 metrics ingestion.** Each `lenny-loadrunner` exports k6 streaming output (the k6 `--out` flag) to Prometheus remote-write, tagged with run ID, scenario, and runner ID.

**Persistence.** During the run, Prometheus holds metrics in memory plus on the collector instance disk. After the run, the collector snapshots Prometheus to object storage (S3 on AWS, GCS on GCP, Blob Storage on Azure) and the snapshot is referenced by run ID.

### 5.4 Report generator

A new Go package `pkg/loadreport`. Given a run ID, it:

1. Reads the run manifest from object storage (scenario list, VU and rate, durations, target version).
2. Reads the Prometheus snapshot.
3. Reads the per-runner k6 JSON reports from object storage.
4. Renders an HTML report with charts.

**Report contents per run:**

- Header: run ID, branch, commit, image tag, scenario set, scale, start and end timestamps, total wall-clock.
- Topology: cluster sizing, node counts, datastore class, runner count.
- Per-scenario section: SLO assertions and pass or fail, latency percentiles (avg, p50, p90, p95, p99, p99.9, max), throughput, error rate, error code histogram.
- Resource section: gateway CPU and memory time-series, controller CPU and memory, database connections and IOPS, cache CPU and commands per second, node CPU and memory.
- Pod-lifecycle section: pod-creation rate, SandboxClaim activity, warm-pool depth time-series.
- Anomalies section: any alert that fired during the run, with timestamp and resolved-at.
- Logs section: links to the gateway log archive and controller log archive in object storage.
- Comparison: diff against a named baseline run pinned by run ID.

**Implementation choice for the report HTML.** Static HTML plus Plotly.js (or Chart.js) loaded from CDN, with all data inlined as JSON in the page. The report is a single self-contained file that opens from object storage with no server. A small JS layer permits filtering by time range, scenario, and percentile.

**Object storage layout (AWS shown; GCP and Azure mirror it):**

```
s3://lenny-load-reports/
  runs/
    <run-id>/
      manifest.json
      report.html
      prometheus-snapshot.tar.gz
      k6/<scenario>/<runner-id>.json
      logs/{gateway,controller,...}.log.gz
  baselines/
    <named-baseline>/  → manifest pointing at a run-id
  index.html  (auto-generated catalogue of all runs)
```

Bucket lifecycle: keep `report.html` and `manifest.json` indefinitely; expire raw artifacts after a configurable retention period.

### 5.5 Control web app (`lenny-loadctl`)

A new service in `cmd/lenny-loadctl` plus a `web/` directory with the UI. Deployed per-cloud as a managed container service:

| Cloud | Runtime | Database | Networking |
|:--|:--|:--|:--|
| AWS | ECS on Fargate behind an ALB | RDS Postgres | Private subnets plus public ALB listener with TLS |
| GCP | Cloud Run service | Cloud SQL Postgres | Cloud Run public endpoint with Cloud Armor; serverless VPC connector to the cluster |
| Azure | Container Apps environment | Flexible Server Postgres | Container Apps ingress with managed certificate |

Cold-start cost is paid once per run trigger. The WebSocket telemetry channel requires session affinity, which is configured on the ALB target group, the Cloud Run service, or the Container Apps revision respectively. Secrets live in AWS Secrets Manager, GCP Secret Manager, and Azure Key Vault.

**Terraform.** A new `loadctl` module under `deploy/terraform/cloud/<provider>/loadctl/` per cloud. Inputs: container image reference for `lenny-loadctl`, cluster reference (for VPC peering or serverless VPC connector), database sizing, TLS certificate reference, OIDC issuer URL, object-storage bucket name. Outputs: loadctl service URL, managed-Postgres endpoint, IAM role / service account reference for the loadctl runtime. Invoked through `up-loadctl.sh` and torn down through `down-loadctl.sh`.

**API surface.**

| Method | Path | Purpose |
|:--|:--|:--|
| `POST /api/v1/runs` | start a new run (scenario set, scale, cluster reference) |
| `GET /api/v1/runs/{id}` | run status, scenario states, current metrics URL |
| `POST /api/v1/runs/{id}:stop` | abort a running run |
| `GET /api/v1/runs/{id}/metrics:stream` | WebSocket; live percentiles and throughput |
| `GET /api/v1/runs/{id}/report` | redirect to the object-storage-hosted report.html |
| `GET /api/v1/runners` | registered runners plus capacity |
| `GET /api/v1/scenarios` | scenario library |
| `POST /api/v1/baselines/{name}` | pin a run as the named baseline |

Every endpoint authenticates through the same OIDC and JWT path Lenny's gateway uses, so an AI agent with platform credentials can drive the API programmatically.

**Frontend.**

- Server-rendered HTML pages for the catalogue and run history.
- A single dynamic page per run that opens the WebSocket and renders live charts.
- Minimal JS framework. HTMX plus Plotly.js keeps the surface small and matches the playground.

**Cloud topology (AWS shown; GCP and Azure mirror it):**

```
+-----------------------------------------------------------------+
|  Cloud account (AWS)                                            |
|                                                                 |
|   +---------------+    +-----------------------------------+    |
|   | lenny-loadctl |    | Lenny EKS cluster                 |    |
|   | (Fargate)     |    |  +---------+  +----------------+  |    |
|   |  +  RDS PG    |<==>|  | gateway |  | Prometheus     |  |    |
|   +-------+-------+    |  +---------+  +----------------+  |    |
|           |            +-----------------------------------+    |
|           v                            ^                        |
|   +---------------+                    |                        |
|   | loadrunner    | ==(load over PL)==>|                        |
|   | ASG (EC2 xN)  |                    |                        |
|   +---------------+                                             |
|                                                                 |
|                          S3 (reports, snapshots, logs)          |
+-----------------------------------------------------------------+
```

The control plane authorizes the runners, dispatches their work, ingests their metrics, and writes the final report to object storage.

### 5.6 What `run-load.sh` becomes

A thin pre-flight and invocation script of around 100 lines:

```
1. Assert provider credentials configured.
2. Assert cluster up.
3. Assert chart installed.
4. Assert loadgen pool present and runners healthy.
5. Assert lenny-loadctl reachable.
6. POST /api/v1/runs to lenny-loadctl with the requested scenario set and scale.
7. Tail run status until terminal (PASS / FAIL / ABORTED).
8. Print the report URL.
```

It contains no terraform calls, no helm calls, no chart edits, and no port-forwards.

## 6. Unit test coverage uplift

Tier-1 unit additions, derived from the recent 48 hours of fixes and the latent bugs of similar shape:

### 6.1 RBAC manifest assertions

A new `tests/tier1_unit/rbac` package walks every `Client.{Create,Get,List,Watch,Patch,Update,Delete}` call site in `pkg/gateway/`, `pkg/controller/`, and `cmd/lenny-*`. It builds a set of `{verb, resource}` pairs per ServiceAccount and asserts the chart's ClusterRole templates contain each pair. Catches the `delete sandboxclaims` class of bug at static-analysis time.

### 6.2 Concurrency contract tests

Every state-machine package (`pkg/sandbox`, `pkg/sandboxclaim`, `pkg/sessionstore`, `pkg/quota`, `pkg/credential`, `pkg/leaseextension`, `pkg/idempotency`, `pkg/middleware/circuitbreaker`, `pkg/audit`, `pkg/gateway/slotcounter`) gains a `concurrent_test.go` containing at minimum:

- N-goroutine race over the package's documented public invariants.
- Run with `-race`.
- Assert no state corruption, no double-fire, no negative counters, and no chain breakage.

### 6.3 Ordering and mirror-lag tests

A new helper `tests/testinfra/clockstep` plus `tests/testinfra/watchlag` lets a test request a configurable delay between a write completing on the fake API server and the corresponding watch event firing in the controller. Used to express tests like "the admission webhook admits a claim even when the SSA phase mirror has not yet propagated."

### 6.4 Error-surfacing tests

A static check in `tests/tier0_static/errorprop` walks the AST for `if err := ...Close(...); err != nil` and similar shapes and fails if the branch body is empty or `_ = err`. Every error returned from a `Close`, `Cleanup`, `Release`, or `Drain` path must be propagated to the caller, logged at level `error`, or recorded as a metric.

### 6.5 Default-tuning regression tests

A table-driven test asserts that every operationally-tunable default chosen by the gateway (cluster QPS and burst, idempotency cache size, slot-counter timeout, claim-create timeout, watch resync period) is read from a flag, exposed in `--help`, documented in TESTING.md or the chart values, and changeable without code changes. Closes the gap that `0b7c71c` documented.

### 6.6 Adapter and runtime contract under concurrency

`pkg/runtimekit` and `pkg/adapter` gain concurrent contract tests: N concurrent JSONL messages on a single adapter connection, asserting frame integrity and the documented "one in-flight tool call at a time per session" invariant.

## 7. TESTING.md changes

The only documentation target for this overhaul is `TESTING.md`. The `/spec` files are untouched. If we promote any of this content into `/spec` later it will happen as a separate, deliberate move.

The TESTING.md edits group into five clusters:

**7.1 — Tier model.** Update §3 ("Test layer model"): add `load_local` and `load_cloud` to the tier list and the gate hierarchy. The new ordering is the one in §2 of this plan. Tier 7 becomes a two-stage gate; tier 12 sits after every other tier.

**7.2 — Per-tier sections.** Replace §12.7 ("Tier 7 — Load and SLO") with two adjacent sub-sections:

- §12.7.a — Tier 7a (`load_local`). Describes the in-process harness, the stub set in §3.2, the pure-Go driver, the scenario catalogues in §3.4 and §3.5, the race-detector requirement, the wall-clock budget, and the scenarios that intentionally live in tier-7b because they cannot be exercised in-process.
- §12.7.b — Tier 7b (`load_kind`). Carries the existing §12.7 contents with the renamed tag, the renamed skip helper, the directory rename, and the additions in §4.3.

Insert §12.12 — Tier 12 (`load_cloud`). Describes the script split (§5.1), the external runner pool (§5.2), the metrics pipeline (§5.3), the report generator (§5.4), the control app (§5.5), the rewritten `run-load.sh` (§5.6), the per-cloud `lenny-loadctl` deployment, and the cadence (pre-release plus on-demand).

**7.3 — Harness and selection.** Update §6 ("The `lenny-test` harness") with the new tier names and the renamed build tags. Update §8 ("Test selection groups"): the `pre-release` group gains `load_cloud`; the `nightly` group keeps `load_kind`; the `pr` group runs `load_local` plus `load_kind` smoke.

**7.4 — Provisioning profiles.** Update §10 ("Service and cluster provisioning"). Add a new "Profile: `loadgen`" entry describing the off-cluster runner pool topology for all three clouds. Update "Profile: `cloud`" to note that tier-12 uses the existing `cloud` profile plus the `loadgen` profile plus the new `loadctl` profile.

**7.5 — Build-tools components and decisions.** Add a new top-level section near the end of TESTING.md (proposed §15: "Load-test platform components") that documents:

- The `lenny-loadctl` and `lenny-loadrunner` binaries as build-time tooling (not part of the v1 production install).
- The recorded decisions: load is generated off-cluster from dedicated VMs; `run-load.sh` does not provision or destroy infrastructure; the cloud-load report is permanent and lives in object storage; the control plane exposes a public API so AI agents and CI can drive runs.
- The per-cloud deployment targets (ECS / Cloud Run / Container Apps) for `lenny-loadctl`.
- The per-cloud runner pool topology (ASG / MIG / VMSS) for `lenny-loadrunner`.
- The terraform module layout: `deploy/terraform/cloud/<provider>/loadgen/` and `deploy/terraform/cloud/<provider>/loadctl/`.
- The API surface table (the same one in §5.5).

No `/spec` change is required for the Prometheus scrape contract; spec §16.9 already defines the scrape target set and §16.1 already defines every metric Lenny emits. Tier-12 consumes those without spec changes.

## 8. Migration sequence

Six waves, each independently shippable:

**Wave 1: tier rename and TESTING.md update.** Rename `tests/tier7_load` to `tests/tier7b_load_kind` and `tests/tier7_load_cloud` to `tests/tier12_load_cloud`. Update `cmd/lenny-test/tiers.go`. Apply the TESTING.md edits in §7 of this plan (clusters 7.1–7.4 in full; 7.5 with placeholder sub-sections that the later waves fill in). No code behaviour change.

**Wave 2: in-process harness scaffolding.** Build `tests/testinfra/inproc`, `tests/testinfra/fakekube`, `tests/testinfra/stubs/runtime`, and `tests/testinfra/loadgen`. Land one tier-7a scenario end-to-end. `slot_counter_race` is the recommended first scenario since the slotcounter package already has a 50-goroutine race test that is a natural starting point. Validate the harness in CI.

**Wave 3: tier-7a scenario coverage.** Two parallel streams:

- Migrate every existing tier-7b scenario whose surface can be exercised in-process (the §4.1 corpus minus the §4.3 Kind-only additions).
- Land every regression scenario in §3.4 and every new scenario in §3.5.

Each scenario is its own commit with a BUILD-PROGRESS update. Wave 3 is not done until the §3.5 catalogue is in the tree; additional scenarios may be added to §3.5 as new gaps come to light during scenario work.

**Wave 4: unit-test uplift.** Land §6.1 through §6.6 in order; each is independent.

**Wave 5: tier-12 script split, terraform modules, and external runners.** Split `run-load.sh` per §5.1. Land `cmd/lenny-loadrunner` (binary + work-dispatch interface + per-cloud implementations). Land the `loadgen` terraform module for all three clouds under `deploy/terraform/cloud/<provider>/loadgen/`. Land `up-loadgen.sh` and `down-loadgen.sh` per cloud. Tier-12 is still driven by the legacy harness during this wave; the new path runs alongside.

**Wave 6: tier-12 control plane, report, and loadctl terraform.** Land `cmd/lenny-loadctl`, the report generator (`pkg/loadreport`), the metrics collector, and the web UI. Land the `loadctl` terraform module for all three clouds under `deploy/terraform/cloud/<provider>/loadctl/` (ECS / Cloud Run / Container Apps deployment, managed Postgres, TLS, OIDC, object-storage permissions). Land `up-loadctl.sh` and `down-loadctl.sh` per cloud. Fill in the TESTING.md §15 sub-sections (§7.5) with the final API surface, deployment topology, and operator runbook references. Cut over `run-load.sh` to the new flow. The legacy harness is removed.

Each wave is gated by a tier-pass at the level the wave touches. Wave 5 and Wave 6 each require a tier-12 dry run on AWS at small scale before being declared done.

## 9. Locked decisions

| Area | Choice |
|:--|:--|
| Tier-7a depth | Isolated per-package benches plus in-process multi-component harness |
| Tier-12 control app | ECS / Cloud Run / Container Apps (per-cloud managed-container runtime) |
| Tier-7a load driver | Pure-Go driver in `tests/testinfra/loadgen`; k6 stays in tier-7b and tier-12 |
| Tier-12 clouds at launch | AWS, GCP, Azure |
| Documentation target | TESTING.md only; no `/spec` edits in this overhaul |
| Tier-12 terraform scope | Provisions cluster + datastores + loadgen pool + loadctl service through dedicated modules per cloud |
| Wave 3 scenario scope | Migrates existing tier-7b scenarios AND lands the new catalogue in §3.5 |
