# Test Automation Architecture

## Design Principles

1. **Every layer testable without the layer above it.** Store interfaces test against real backends without a gateway. The gateway tests against stores without Kubernetes. E2E tests against a real cluster without real LLM providers.
2. **Test runtimes as first-class infrastructure.** The spec's `echo`, `streaming-echo`, and `delegation-echo` runtimes are the backbone — they eliminate external LLM dependencies from CI.
3. **Progressive environment fidelity.** Unit → component → integration → e2e → load → chaos. Each layer catches a different class of bug. No layer is skipped.
4. **CI gates are non-negotiable.** Every layer defines a pass/fail gate. A failure at any layer blocks merge or promotion.

---

## Layer 1: Unit Tests

**Scope:** Pure logic — no I/O, no network, no containers.

**What gets unit-tested:**
- State machine transitions (session, task, sandbox lifecycle)
- Tenant key prefix validation and RLS context logic
- Quota calculation, budget slicing, token accounting
- Delegation policy evaluation (depth limits, fan-out limits, budget propagation)
- Workspace plan computation (file set diffing, materialization ordering)
- Error code classification (`retryable`, `category` mapping)
- Adapter manifest generation and validation
- Lifecycle channel message parsing/serialization
- MCP ↔ REST schema equivalence functions
- Helm template rendering (given values → expected manifests)

**Tooling:**
- `go test` with `-race` flag (always)
- Table-driven tests as the default pattern
- `go test -fuzz` for parsers: lifecycle channel JSON, MCP message framing, adapter manifest, protobuf edge cases
- Helm unit tests via `helm-unittest` plugin (chart template assertions)

**Convention:**
```
pkg/session/state_machine.go      → pkg/session/state_machine_test.go
pkg/quota/budget.go               → pkg/quota/budget_test.go
charts/lenny/                     → charts/lenny/tests/
```

**CI gate:** `go test -race -count=1 ./...` — must pass on every PR. Coverage threshold: 80% on new code (enforced by CI, not as a repo-wide gate that discourages refactoring).

---

## Layer 2: Component Tests (single component + real dependencies)

**Scope:** One Lenny component wired to real backing services (Postgres, Redis, MinIO) in containers. No Kubernetes. No other Lenny components.

**Infrastructure:** `docker compose` profiles per component.

### 2a. Store Interface Tests

Each store role (Section 12.6) gets its own test suite that exercises the interface contract against real backends:

| Suite | Backend | What it validates |
|-------|---------|-------------------|
| `SessionStore` | Postgres | CRUD, state transitions, concurrent claims (`SELECT ... FOR UPDATE SKIP LOCKED`), tenant RLS isolation |
| `LeaseStore` | Redis (+ Postgres fallback) | Acquire/release/extend, TTL expiry, failover to advisory locks |
| `QuotaStore` | Redis + Postgres | Increment/decrement, sliding window, fail-open reconciliation, counter rehydration |
| `TokenStore` | Postgres | Encrypted storage, rotation, envelope key derivation |
| `ArtifactStore` | MinIO | Upload/download, tenant prefix validation, GC lifecycle, SSE encryption |
| `EventStore` | Postgres | Append-only audit, hash chain integrity, cursor-based streaming |
| `CredentialPoolStore` | Postgres | Lease assignment, health scoring, deny-list, revocation propagation |
| `EvictionStateStore` | Postgres | Minimal state write/read, cleanup on terminal session |

Each suite:
- Starts with a migrated schema (validates migrations as a side effect)
- Runs tenant isolation assertions (write as tenant A, read as tenant B → zero rows)
- Runs concurrent access tests (goroutines racing on the same resource)

### 2b. RLS & Security Tests (named in the spec)

| Test | What it validates |
|------|-------------------|
| `TestRLSTenantGuardMissingSetLocal` | Query without `SET LOCAL app.current_tenant` → exception |
| `TestRLSCrossTenantRead` | Tenant A context → zero rows from tenant B |
| `TestRedisTenantKeyIsolation` | Operations on `t:A:*` keys cannot read `t:B:*` |
| `TestSemanticCacheTenantIsolation` | Cache hit for tenant A never served to tenant B |
| `TestRedisTLSEnforcement` | Plaintext connection → rejected |
| `TestPgBouncerTLSEnforcement` | Plaintext connection → rejected |

### 2c. Gateway Subsystem Tests

Test each of the gateway's four internal subsystems (Session Orchestrator, File Fabric, MCP Fabric, Admin Plane) in isolation, using real stores but mocked pod lifecycle:

- Session lifecycle: create → attach → prompt → complete, with store assertions
- File upload/download through the File Fabric with real MinIO
- Quota enforcement: token budget exhaustion, rate limiting, storage quota
- Eviction checkpoint flow: MinIO available → full checkpoint; MinIO down → Postgres fallback

**Tooling:**
- `testcontainers-go` for Postgres, Redis, MinIO containers
- Shared `testinfra` package that spins up a migrated Postgres + seeded Redis + MinIO bucket
- Tests tagged `//go:build integration` so `go test ./...` skips them by default
- Run via `make test-component` or `go test -tags=integration ./...`

**CI gate:** Runs on every PR. Postgres + Redis + MinIO containers started by the CI job. Must pass 100%.

---

## Layer 3: Contract Tests

**Scope:** Verify that all external API surfaces (REST, MCP, OpenAI Completions, Open Responses) return semantically identical responses for identical operations.

**Architecture:**

```
                    ┌──────────────────────────┐
                    │   Contract Test Harness   │
                    │                           │
                    │  RegisterAdapterUnderTest  │
                    └─────┬───┬───┬───┬────────┘
                          │   │   │   │
                     REST MCP Comp. Open Resp.
                          │   │   │   │
                    ┌─────▼───▼───▼───▼────────┐
                    │    Gateway (real process)  │
                    │    + echo runtime pod      │
                    └──────────────────────────┘
```

**What it validates (from Section 15.2.1):**
- Success path: identical payloads modulo transport envelope
- Validation errors: same `code` and `category`
- Authz rejections: same denial behavior
- `retryable` + `category` flags identical across surfaces
- State transition sequences: create→running→completed, create→running→interrupted→resumed→completed
- Pagination: cursor semantics, page sizes, empty-result shapes

**Test matrix:** Every overlapping operation x every adapter x success + each error class.

**Tooling:**
- Harness exposes `RegisterAdapterUnderTest(adapter)` for third-party adapters
- Runs against a real gateway process (Tier 2 docker-compose stack)
- Tagged `//go:build contract`

**CI gate:** Runs on every PR (Phase 5+). Blocks merge.

---

## Layer 4: Integration Tests (multi-component, no real cluster)

**Scope:** Multiple Lenny components talking to each other. Gateway + controller-sim + agent pods + real stores. Uses docker-compose (Tier 2 local dev stack).

**Suites:**

| Suite | Scope |
|-------|-------|
| `session_lifecycle` | Full create→upload→attach→prompt→complete flow via REST and MCP |
| `checkpoint_resume` | Session eviction → checkpoint to MinIO → resume on new pod → workspace restored |
| `delegation` | Parent delegates to child via `delegation-echo` runtime → result propagation → budget enforcement |
| `credential_lifecycle` | Credential assignment → rotation → `credentials_rotated` lifecycle message → runtime re-bind |
| `credential_revocation` | Emergency revoke → deny-list propagation via Redis pub/sub → active session termination |
| `streaming_reconnect` | Client disconnects mid-stream → reconnects with `Last-Event-ID` → replay from cursor |
| `concurrent_workspace` | `slotId` multiplexing: N prompts on same pod, per-slot credential isolation |
| `quota_enforcement` | Token budget → exhaustion → `budget_exhausted` → session termination; storage quota → rejection |
| `migration_upgrade` | Apply migration N, seed data, apply migration N+1 → data integrity preserved |
| `admin_bootstrap` | `lenny-ctl bootstrap` → seed resources created → first session succeeds |

**Infrastructure:**
- `docker compose --profile test up` — starts gateway, controller-sim, echo/streaming-echo/delegation-echo pods, Postgres, Redis, MinIO
- Tests drive via REST/MCP client libraries against `localhost`
- Tagged `//go:build integration`

**CI gate:** Runs on every PR. Must pass 100%.

---

## Layer 5: E2E Tests (real Kubernetes cluster)

**Scope:** Full Lenny deployment on a real Kubernetes cluster. CRDs, controllers, warm pools, admission webhooks, NetworkPolicy, mTLS — everything real except LLM providers.

**Infrastructure:**
- **CI:** Ephemeral Kind or k3s cluster per test run (lightweight, fast to provision)
- **Nightly:** GKE/EKS cluster with gVisor enabled (validates RuntimeClass, real sandboxing)
- Helm install with `deploymentProfile: self-managed` + test runtimes registered

**Suites:**

| Suite | What it validates |
|-------|-------------------|
| `warm_pool` | Pool scaling: minWarm maintained, scale-up on claim, scale-down on idle, PDB respected during drain |
| `sandbox_claim` | Optimistic locking under 50+ concurrent goroutines, zero double-claims (ADR-007 chaos test) |
| `pod_lifecycle` | Claim → assign credentials → workspace materialize → prompt → checkpoint → release → pod returns to pool |
| `node_drain` | `kubectl drain` → preStop checkpoint fires → session resumes on new pod → workspace intact |
| `admission_policy` | Controller-generated pod specs pass PSS admission for each RuntimeClass (runc, gVisor, Kata) |
| `network_policy` | Agent pod → gateway: allowed. Agent pod → Postgres/Redis/internet: denied. |
| `mtls_enforcement` | Gateway↔pod gRPC over mTLS; plain-text rejected; cert auto-renewal works |
| `sandbox_finalizer` | Delete sandbox with active session → blocked by finalizer → checkpoint → finalizer removed → pod deleted |
| `orphan_claim_gc` | Gateway crash after SandboxClaim creation → controller detects orphan → claim cleaned up |
| `drain_readiness_webhook` | MinIO unhealthy → `ValidatingAdmissionWebhook` blocks pod eviction |
| `tenant_namespace_isolation` | Tenant A pods cannot reach tenant B namespace (NetworkPolicy) |

**Tooling:**
- `envtest` (controller-runtime) for fast controller unit tests (no real cluster, but real API server)
- Full cluster tests use a test harness that:
  1. Installs Lenny via Helm into a test namespace
  2. Registers test runtimes
  3. Runs test suites
  4. Tears down
- Tests tagged `//go:build e2e`

**CI gate:**
- Kind-based subset on every PR (warm pool, sandbox claim, pod lifecycle, mTLS — the critical path)
- Full suite nightly on GKE with gVisor

---

## Layer 6: Load & Performance Tests

**Scope:** Validate SLOs under sustained load at each tier.

**Test harness architecture:**

```
┌─────────────────────────────────────┐
│         Load Test Orchestrator      │
│         (k6 or custom Go harness)   │
├─────────────────────────────────────┤
│  Scenarios (composable):            │
│  - session_throughput               │
│  - streaming_reconnect_under_load   │
│  - delegation_fanout                │
│  - credential_rotation_under_load   │
│  - checkpoint_duration              │
│  - pod_claim_latency                │
│  - concurrent_workspace_slots       │
├─────────────────────────────────────┤
│  Reporters:                         │
│  - Prometheus push (metrics)        │
│  - JSON artifact (CI comparison)    │
│  - SLO pass/fail verdict            │
└─────────────────────────────────────┘
```

**SLO gates (from the spec):**

| Metric | Target | Measured at |
|--------|--------|-------------|
| Pod-warm session start (runc) | P95 < 2s | Phase 2+ |
| Pod-warm session start (gVisor) | P95 < 5s | Phase 2+ |
| Checkpoint <= 100MB workspace | P95 < 2s | Phase 2+ |
| Streaming reconnect latency | P95 < 500ms | Phase 6.5+ |
| Delegation fan-out (N=50) | Complete < 30s | Phase 9.5+ |
| Credential rotation propagation | P95 < 5s | Phase 11.5+ |
| Gateway at 10,000 sessions | No OOM, latency within SLO | Phase 13.5 |

**Incremental load test phases (aligned with build sequence):**

| Phase | Focus | Cluster |
|-------|-------|---------|
| 2 | Startup latency, checkpoint duration baseline | Kind |
| 6.5 | Streaming path: 500 concurrent sessions | Dedicated GKE/EKS |
| 9.5 | Delegation: fan-out N=50, depth=10 | Dedicated GKE/EKS |
| 11.5 | Credential lifecycle: 200 concurrent sessions | Dedicated GKE/EKS |
| 13.5 | Full-system: Tier 2 sustained load, all paths combined | Dedicated GKE/EKS |
| 14.5 | Re-run 13.5 with full security hardening | Dedicated GKE/EKS |

**Regression detection:** Each run produces a JSON artifact with latency histograms. CI compares against the previous baseline and flags regressions > 15%.

**CI gate:** Phase 2 baseline on every PR (fast, Kind-based). Full load tests on nightly/weekly schedule and before releases.

---

## Layer 7: Chaos Tests

**Scope:** Validate resilience and recovery under failure conditions.

**Tooling:** Litmus Chaos or a lightweight custom Go harness that uses the Kubernetes API to inject failures.

| Scenario | Injection | Expected behavior |
|----------|-----------|-------------------|
| Pod kill during active session | `kubectl delete pod --force` | Session resumes on new pod via checkpoint |
| MinIO unavailable during checkpoint | NetworkPolicy blocks MinIO traffic | Postgres minimal state fallback; `CheckpointStorageUnavailable` alert fires |
| Redis unavailable | Kill Redis pod | Quota fail-open; LeaseStore falls back to Postgres advisory locks; session continues |
| Postgres unavailable | Kill Postgres pod | New sessions rejected (503); existing sessions continue (state in Redis); recovery on reconnect |
| Network partition (gateway <-> agent pod) | NetworkPolicy injection | gRPC deadline exceeded → session retry; no data loss |
| Leader election disruption | Kill controller leader pod | New leader elected; pool reconciliation resumes; no double-claims |
| Concurrent SandboxClaim race | 50+ goroutines claiming simultaneously | Zero double-claims (ADR-007 verification) |
| Node drain during MinIO outage | Drain + MinIO NetworkPolicy block | Webhook blocks drain (`lenny-drain-readiness`); if forced, minimal state fallback |
| Dual-store outage (Redis + Postgres) | Kill both | System enters full degraded mode; existing sessions survive on in-memory state; alerts fire |
| Certificate expiry | Advance time / revoke cert | mTLS connections fail → auto-renewal kicks in → connections restored |

**CI gate:** Core chaos scenarios (pod kill, Redis down, concurrent claim race) on every nightly run. Full suite weekly and before releases.

---

## Layer 8: Security Tests

**Scope:** Validate security controls are effective, not just configured.

| Category | Tests |
|----------|-------|
| Tenant isolation | Cross-tenant read at every store layer (Postgres RLS, Redis prefix, MinIO prefix, namespace) |
| TLS enforcement | Plaintext connections rejected at Redis, Postgres/PgBouncer, gateway, intra-pod gRPC |
| Admission policy | Pod specs without required RuntimeClass → rejected; `shareProcessNamespace: true` → rejected |
| Credential leakage | Agent pod env/filesystem inspection: no credentials in env vars, no plaintext tokens on disk |
| NetworkPolicy | Agent pod `curl` to internet/Postgres/Redis → connection refused |
| Input validation | Oversize payloads, malformed JSON, SQL injection strings, path traversal in artifact keys → proper rejection |
| RBAC | User role → cannot access admin API; tenant-admin → cannot access other tenant |

**Tooling:**
- Integration test suites with security-specific assertions
- `kubeaudit` or `kube-bench` for cluster-level compliance scanning
- OWASP ZAP or similar for API surface fuzzing (nightly)

**CI gate:** Tenant isolation + TLS enforcement + admission policy tests on every PR. Full suite nightly.

---

## CI Pipeline Structure

```
PR Pipeline (every push):
├── lint (golangci-lint, helm lint, proto lint)
├── unit tests (go test -race ./...)
├── helm template tests (helm-unittest)
├── component tests (testcontainers: Postgres + Redis + MinIO)
├── contract tests (docker-compose Tier 2 stack)
├── integration tests (docker-compose Tier 2 stack)
├── e2e tests — critical path (Kind cluster)
│   ├── warm_pool
│   ├── sandbox_claim
│   ├── pod_lifecycle
│   └── mtls_enforcement
├── security tests — fast subset
│   ├── tenant_isolation
│   ├── tls_enforcement
│   └── admission_policy
└── migration validation (apply all migrations to clean Postgres)

Nightly Pipeline:
├── full PR pipeline
├── e2e tests — full suite (GKE with gVisor)
├── chaos tests — core scenarios
├── security tests — full suite + API fuzzing
├── load test — Phase 2 baseline (startup latency, checkpoint)
└── dependency audit (go mod vulnerabilities, image CVE scan)

Weekly / Pre-Release Pipeline:
├── full nightly pipeline
├── load tests — full tier (6.5, 9.5, 11.5, 13.5 scenarios)
├── chaos tests — full suite
├── multi-profile validation (cloud-managed + self-managed Helm configs)
└── SLO regression comparison against previous release baseline
```

---

## Directory Structure

```
tests/
├── testinfra/                  # Shared test infrastructure
│   ├── containers.go           # testcontainers-go setup (Postgres, Redis, MinIO)
│   ├── fixtures.go             # Tenant seeds, session seeds, credential seeds
│   ├── assertions.go           # Cross-cutting: tenant isolation, state machine
│   └── k8s.go                  # Kind/envtest cluster helpers
├── component/                  # Layer 2: store interfaces, gateway subsystems
│   ├── session_store_test.go
│   ├── lease_store_test.go
│   ├── quota_store_test.go
│   ├── artifact_store_test.go
│   ├── rls_test.go
│   └── gateway_subsystem_test.go
├── contract/                   # Layer 3: API surface equivalence
│   ├── harness.go              # RegisterAdapterUnderTest
│   ├── rest_mcp_test.go
│   ├── rest_completions_test.go
│   └── rest_responses_test.go
├── integration/                # Layer 4: multi-component flows
│   ├── session_lifecycle_test.go
│   ├── checkpoint_resume_test.go
│   ├── delegation_test.go
│   ├── credential_lifecycle_test.go
│   ├── streaming_reconnect_test.go
│   └── quota_enforcement_test.go
├── e2e/                        # Layer 5: real Kubernetes cluster
│   ├── warm_pool_test.go
│   ├── sandbox_claim_test.go
│   ├── node_drain_test.go
│   ├── admission_policy_test.go
│   ├── network_policy_test.go
│   └── mtls_test.go
├── load/                       # Layer 6: performance & SLO
│   ├── scenarios/
│   │   ├── session_throughput.go
│   │   ├── streaming_reconnect.go
│   │   ├── delegation_fanout.go
│   │   └── checkpoint_duration.go
│   ├── slo.go                  # SLO definitions and verdict logic
│   └── report.go               # JSON artifact + Prometheus push
├── chaos/                      # Layer 7: failure injection
│   ├── pod_kill_test.go
│   ├── store_outage_test.go
│   ├── network_partition_test.go
│   └── concurrent_claim_test.go
└── security/                   # Layer 8: security control verification
    ├── tenant_isolation_test.go
    ├── tls_enforcement_test.go
    ├── network_policy_test.go
    └── input_validation_test.go
```

---

## Key Implementation Decisions

1. **`testcontainers-go` over mocks for stores.** Real Postgres/Redis/MinIO in containers. Mocks hide bugs (the spec explicitly warns about this for migrations). Containers add ~5s startup — acceptable for the confidence gained.

2. **Build tags over separate modules.** `//go:build integration`, `//go:build e2e`, `//go:build contract`. Keeps tests colocated with the code they test where possible, separated only when infrastructure requirements differ.

3. **Test runtimes are shipped artifacts.** `echo`, `streaming-echo`, `delegation-echo` are built and published as container images alongside the main components. They are not test-only code — they are part of the platform's developer experience.

4. **Load tests produce machine-comparable artifacts.** JSON files with histograms, not just human-readable reports. CI diffs against the previous baseline and fails on regression.

5. **Kind for PR-level e2e, GKE for nightly.** Kind is fast (30s cluster creation) and free. GKE validates gVisor, real NetworkPolicy enforcement, and managed-K8s-specific behavior that Kind cannot replicate.

6. **No test-only database schemas.** Tests use the real migration chain. This is both a test of the migrations and a guarantee that tests reflect production schema.
