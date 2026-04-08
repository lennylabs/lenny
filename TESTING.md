# Testing Architecture

## Overview

Lenny's test infrastructure is built **before** the application code. The technical design spec is the source of truth. Tests are scaffolded from the spec as failing stubs, and AI agents implement Lenny by making those tests pass. Test results are the primary feedback mechanism that drives agentic development.

This inverts the traditional workflow:

```
Traditional:  code → tests → CI
Lenny:        spec → tests (failing) → agent writes code → tests (passing) → next spec section
```

The testing architecture is designed around three requirements:
1. **AI agents are the primary test consumers.** Output is structured for machine reasoning, not human scanning.
2. **Agents select tests intelligently.** A change graph maps code changes to the minimal set of relevant tests.
3. **Tests encode the spec.** Every test traces back to a spec section. Passing all tests means the spec is implemented.

---

## Development Loop

### The Agent Feedback Cycle

```
┌─────────────────────────────────────────────────────────┐
│                    Agent Work Cycle                      │
│                                                         │
│  1. Agent receives task (spec section / failing tests)  │
│  2. Agent queries: "what tests cover this?"             │
│  3. Agent writes / modifies code                        │
│  4. Agent runs selected tests (not all tests)           │
│  5. Harness returns structured verdict                  │
│  6. On failure: agent reads diagnosis, adjusts code     │
│  7. On pass: agent marks task done, picks next          │
│                                                         │
│  Steps 3-6 repeat until green.                          │
│  Step 4 takes seconds, not minutes.                     │
└─────────────────────────────────────────────────────────┘
```

### Spec Traceability

Every test function carries a `// spec:` annotation linking it to one or more technical design sections:

```go
// spec: 4.2 (tenant context propagation), 12.3 (RLS enforcement)
func TestRLSTenantGuardMissingSetLocal(t *testing.T) { ... }

// spec: 12.5 (artifact store), 12.5 (tenant isolation)
func TestArtifactStoreTenantPrefixValidation(t *testing.T) { ... }
```

This serves two purposes:
- **Agent → tests:** Given "implement Section 4.2", the agent greps for `// spec: 4.2` to find all relevant tests.
- **Tests → spec:** When a test fails, the agent knows which spec section to re-read for requirements.

A machine-readable index is maintained at `tests/spec-map.json`:

```json
{
  "4.2": {
    "title": "Tenant Context Propagation",
    "tests": [
      "tests/component/rls_test.go::TestRLSTenantGuardMissingSetLocal",
      "tests/component/rls_test.go::TestRLSCrossTenantRead",
      "tests/integration/session_lifecycle_test.go::TestSessionTenantIsolation"
    ],
    "packages": ["pkg/session", "pkg/gateway/middleware", "migrations/"],
    "dependencies": ["4.1", "12.3"]
  },
  "12.5": {
    "title": "Artifact Store",
    "tests": [
      "tests/component/artifact_store_test.go::TestArtifactUploadDownload",
      "tests/component/artifact_store_test.go::TestArtifactStoreTenantPrefixValidation",
      "tests/component/artifact_store_test.go::TestArtifactGCLifecycle"
    ],
    "packages": ["pkg/store/artifact", "pkg/gc"],
    "dependencies": ["12.1", "12.6"]
  }
}
```

### Test Scaffolding Phase

Before any implementation begins, the test scaffolding phase produces:

1. **Failing test stubs for every spec section.** Each stub contains the spec requirement as a comment and a `t.Fatal("not implemented: <spec section>")` body. This is the backlog — the agent's task list is "make these tests pass."

2. **Interface definitions extracted from the spec.** Go interfaces for every store role (Section 12.6), every controller, every gateway subsystem. These compile but have no implementations. Tests are written against the interfaces.

3. **Test infrastructure (`tests/testinfra/`).** Container setup, fixtures, assertion helpers — all working before any application code exists.

4. **`spec-map.json`.** The complete mapping from spec sections to test files and packages.

The scaffolding is a one-time bootstrapping step. After it, agents work by picking a failing test (or group of tests for a spec section) and implementing until it passes.

---

## Change-Aware Test Selection

### The Change Graph

The harness maintains a dependency graph from packages to test suites. When an agent modifies files, it queries the graph to determine the minimal test set.

**`tests/change-graph.json`:**

```json
{
  "pkg/store/session": {
    "unit": ["pkg/store/session/..."],
    "component": ["tests/component/session_store_test.go"],
    "integration": ["tests/integration/session_lifecycle_test.go", "tests/integration/checkpoint_resume_test.go"],
    "e2e": ["tests/e2e/pod_lifecycle_test.go"]
  },
  "pkg/store/artifact": {
    "unit": ["pkg/store/artifact/..."],
    "component": ["tests/component/artifact_store_test.go"],
    "integration": ["tests/integration/checkpoint_resume_test.go"],
    "e2e": ["tests/e2e/node_drain_test.go"]
  },
  "pkg/gateway/session_orchestrator": {
    "unit": ["pkg/gateway/session_orchestrator/..."],
    "component": ["tests/component/gateway_subsystem_test.go"],
    "integration": ["tests/integration/session_lifecycle_test.go", "tests/integration/streaming_reconnect_test.go"],
    "contract": ["tests/contract/..."],
    "e2e": ["tests/e2e/pod_lifecycle_test.go"]
  },
  "pkg/controller/warmpool": {
    "unit": ["pkg/controller/warmpool/..."],
    "e2e": ["tests/e2e/warm_pool_test.go", "tests/e2e/sandbox_claim_test.go"]
  },
  "migrations/": {
    "component": ["tests/component/..."],
    "integration": ["tests/integration/migration_upgrade_test.go"]
  },
  "charts/lenny/": {
    "unit": ["charts/lenny/tests/..."],
    "e2e": ["tests/e2e/admission_policy_test.go", "tests/e2e/network_policy_test.go"]
  }
}
```

### The `lenny-test` CLI

A lightweight CLI wraps test selection and execution:

```bash
# Run tests affected by current uncommitted changes
lenny-test --changed

# Run tests for specific spec sections
lenny-test --spec 4.2,12.3

# Run tests for specific packages (explicit)
lenny-test --pkg pkg/store/session

# Run only up to a specific layer (fast inner loop)
lenny-test --changed --max-layer component

# Run everything (CI mode)
lenny-test --all

# Dry run: show which tests would run, don't execute
lenny-test --changed --dry-run
```

The `--changed` flag inspects `git diff` (staged + unstaged) to determine modified packages, then walks the change graph to collect the relevant tests. It runs them bottom-up: unit first, then component, then integration. It stops at the first failing layer — there is no value in running e2e tests when unit tests fail.

**Layered escalation:** By default, `--changed` runs layers 1-4 (unit through integration). Layers 5+ (e2e, load, chaos, security) are opt-in because they require infrastructure (Kind cluster, dedicated load cluster). CI runs all layers; agents in the inner loop run layers 1-4.

---

## Structured Test Output

All test output is machine-readable. The harness wraps `go test -json` and enriches it.

### Verdict Format

Every `lenny-test` invocation produces a JSON verdict file at `tests/results/latest.json`:

```json
{
  "run_id": "a1b2c3",
  "timestamp": "2026-04-08T10:30:00Z",
  "trigger": {
    "mode": "changed",
    "changed_packages": ["pkg/store/session", "pkg/gateway/session_orchestrator"],
    "spec_sections": ["4.2", "7.1"]
  },
  "layers": {
    "unit": {
      "status": "pass",
      "duration_ms": 1200,
      "total": 47,
      "passed": 47,
      "failed": 0,
      "skipped": 0
    },
    "component": {
      "status": "fail",
      "duration_ms": 8300,
      "total": 12,
      "passed": 10,
      "failed": 2,
      "skipped": 0,
      "failures": [
        {
          "test": "TestSessionStoreConcurrentClaim",
          "file": "tests/component/session_store_test.go",
          "line": 142,
          "spec_sections": ["4.2", "4.6"],
          "error": "expected session claimed by gateway-1, got gateway-2",
          "duration_ms": 340,
          "stdout_tail": "... last 20 lines of output ...",
          "diagnosis": "SELECT ... FOR UPDATE SKIP LOCKED returned row already claimed by another goroutine. Likely missing transaction isolation or incorrect WHERE clause in ClaimSession."
        },
        {
          "test": "TestSessionStoreRLSIsolation",
          "file": "tests/component/session_store_test.go",
          "line": 203,
          "spec_sections": ["4.2", "12.3"],
          "error": "expected 0 rows for tenant-B, got 3",
          "duration_ms": 120,
          "stdout_tail": "...",
          "diagnosis": "RLS policy not filtering by app.current_tenant. Check that SET LOCAL is issued before the query and that the RLS policy references current_setting('app.current_tenant')."
        }
      ]
    },
    "integration": {
      "status": "skipped",
      "reason": "component layer failed — skipping higher layers"
    }
  },
  "verdict": "FAIL",
  "next_action": "Fix 2 component-layer failures in tests/component/session_store_test.go. See spec sections 4.2, 4.6, 12.3."
}
```

### Key Fields for Agent Consumption

| Field | Purpose |
|-------|---------|
| `trigger.changed_packages` | What the agent changed — context for understanding failures |
| `failures[].spec_sections` | Which spec sections to re-read for requirements |
| `failures[].diagnosis` | Pre-written hint explaining the most likely root cause. Written when the test is authored, not generated at runtime. |
| `failures[].file` + `line` | Exact location to navigate to |
| `verdict` | Single pass/fail — the agent's stop condition |
| `next_action` | Human/agent-readable summary of what to do next |
| `layers.*.status: "skipped"` | Agent knows not to investigate skipped layers |

### Diagnosis Strings

Every test failure includes a `diagnosis` — a static string written by the test author (during scaffolding) that explains what typically causes this specific test to fail. This is not a stack trace; it is a targeted hint for the implementing agent:

```go
func TestSessionStoreConcurrentClaim(t *testing.T) {
    // diagnosis: SELECT ... FOR UPDATE SKIP LOCKED returned row already
    // claimed by another goroutine. Likely missing transaction isolation
    // or incorrect WHERE clause in ClaimSession.
    ...
}
```

The harness extracts the `// diagnosis:` comment from the test source and includes it in the JSON output. This transforms test failures from "figure out what went wrong" into "here's what's probably wrong, go fix it."

---

## Test Layers

### Layer 1: Unit Tests

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
- MCP <-> REST schema equivalence functions
- Helm template rendering (given values -> expected manifests)

**Tooling:**
- `go test` with `-race` flag (always)
- Table-driven tests as the default pattern
- `go test -fuzz` for parsers: lifecycle channel JSON, MCP message framing, adapter manifest, protobuf edge cases
- Helm unit tests via `helm-unittest` plugin (chart template assertions)

**Convention:**
```
pkg/session/state_machine.go      -> pkg/session/state_machine_test.go
pkg/quota/budget.go               -> pkg/quota/budget_test.go
charts/lenny/                     -> charts/lenny/tests/
```

**CI gate:** `go test -race -count=1 ./...` — must pass on every PR. Coverage threshold: 80% on new code (enforced by CI, not as a repo-wide gate that discourages refactoring).

### Layer 2: Component Tests (single component + real dependencies)

**Scope:** One Lenny component wired to real backing services (Postgres, Redis, MinIO) in containers. No Kubernetes. No other Lenny components.

**Infrastructure:** `testcontainers-go` spins up dependencies per suite. No docker-compose needed — the test process manages container lifecycle.

#### 2a. Store Interface Tests

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
- Runs tenant isolation assertions (write as tenant A, read as tenant B -> zero rows)
- Runs concurrent access tests (goroutines racing on the same resource)

#### 2b. RLS & Security Tests (named in the spec)

| Test | What it validates |
|------|-------------------|
| `TestRLSTenantGuardMissingSetLocal` | Query without `SET LOCAL app.current_tenant` -> exception |
| `TestRLSCrossTenantRead` | Tenant A context -> zero rows from tenant B |
| `TestRedisTenantKeyIsolation` | Operations on `t:A:*` keys cannot read `t:B:*` |
| `TestSemanticCacheTenantIsolation` | Cache hit for tenant A never served to tenant B |
| `TestRedisTLSEnforcement` | Plaintext connection -> rejected |
| `TestPgBouncerTLSEnforcement` | Plaintext connection -> rejected |

#### 2c. Gateway Subsystem Tests

Test each of the gateway's four internal subsystems (Session Orchestrator, File Fabric, MCP Fabric, Admin Plane) in isolation, using real stores but mocked pod lifecycle:

- Session lifecycle: create -> attach -> prompt -> complete, with store assertions
- File upload/download through the File Fabric with real MinIO
- Quota enforcement: token budget exhaustion, rate limiting, storage quota
- Eviction checkpoint flow: MinIO available -> full checkpoint; MinIO down -> Postgres fallback

**Tooling:**
- `testcontainers-go` for Postgres, Redis, MinIO containers
- Shared `testinfra` package that spins up a migrated Postgres + seeded Redis + MinIO bucket
- Tests tagged `//go:build component` so `go test ./...` skips them by default
- Run via `lenny-test --max-layer component` or `go test -tags=component ./...`

**CI gate:** Runs on every PR. Must pass 100%.

### Layer 3: Contract Tests

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
- State transition sequences: create->running->completed, create->running->interrupted->resumed->completed
- Pagination: cursor semantics, page sizes, empty-result shapes

**Test matrix:** Every overlapping operation x every adapter x success + each error class.

**Tooling:**
- Harness exposes `RegisterAdapterUnderTest(adapter)` for third-party adapters
- Runs against a real gateway process (Tier 2 docker-compose stack)
- Tagged `//go:build contract`

**CI gate:** Runs on every PR (Phase 5+). Blocks merge.

### Layer 4: Integration Tests (multi-component, no real cluster)

**Scope:** Multiple Lenny components talking to each other. Gateway + controller-sim + agent pods + real stores. Uses docker-compose (Tier 2 local dev stack).

**Suites:**

| Suite | Scope |
|-------|-------|
| `session_lifecycle` | Full create->upload->attach->prompt->complete flow via REST and MCP |
| `checkpoint_resume` | Session eviction -> checkpoint to MinIO -> resume on new pod -> workspace restored |
| `delegation` | Parent delegates to child via `delegation-echo` runtime -> result propagation -> budget enforcement |
| `credential_lifecycle` | Credential assignment -> rotation -> `credentials_rotated` lifecycle message -> runtime re-bind |
| `credential_revocation` | Emergency revoke -> deny-list propagation via Redis pub/sub -> active session termination |
| `streaming_reconnect` | Client disconnects mid-stream -> reconnects with `Last-Event-ID` -> replay from cursor |
| `concurrent_workspace` | `slotId` multiplexing: N prompts on same pod, per-slot credential isolation |
| `quota_enforcement` | Token budget -> exhaustion -> `budget_exhausted` -> session termination; storage quota -> rejection |
| `migration_upgrade` | Apply migration N, seed data, apply migration N+1 -> data integrity preserved |
| `admin_bootstrap` | `lenny-ctl bootstrap` -> seed resources created -> first session succeeds |

**Infrastructure:**
- `docker compose --profile test up` — starts gateway, controller-sim, echo/streaming-echo/delegation-echo pods, Postgres, Redis, MinIO
- Tests drive via REST/MCP client libraries against `localhost`
- Tagged `//go:build integration`

**CI gate:** Runs on every PR. Must pass 100%.

### Layer 5: E2E Tests (real Kubernetes cluster)

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
| `pod_lifecycle` | Claim -> assign credentials -> workspace materialize -> prompt -> checkpoint -> release -> pod returns to pool |
| `node_drain` | `kubectl drain` -> preStop checkpoint fires -> session resumes on new pod -> workspace intact |
| `admission_policy` | Controller-generated pod specs pass PSS admission for each RuntimeClass (runc, gVisor, Kata) |
| `network_policy` | Agent pod -> gateway: allowed. Agent pod -> Postgres/Redis/internet: denied. |
| `mtls_enforcement` | Gateway<->pod gRPC over mTLS; plain-text rejected; cert auto-renewal works |
| `sandbox_finalizer` | Delete sandbox with active session -> blocked by finalizer -> checkpoint -> finalizer removed -> pod deleted |
| `orphan_claim_gc` | Gateway crash after SandboxClaim creation -> controller detects orphan -> claim cleaned up |
| `drain_readiness_webhook` | MinIO unhealthy -> `ValidatingAdmissionWebhook` blocks pod eviction |
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

### Layer 6: Load & Performance Tests

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

### Layer 7: Chaos Tests

**Scope:** Validate resilience and recovery under failure conditions.

**Tooling:** Litmus Chaos or a lightweight custom Go harness that uses the Kubernetes API to inject failures.

| Scenario | Injection | Expected behavior |
|----------|-----------|-------------------|
| Pod kill during active session | `kubectl delete pod --force` | Session resumes on new pod via checkpoint |
| MinIO unavailable during checkpoint | NetworkPolicy blocks MinIO traffic | Postgres minimal state fallback; `CheckpointStorageUnavailable` alert fires |
| Redis unavailable | Kill Redis pod | Quota fail-open; LeaseStore falls back to Postgres advisory locks; session continues |
| Postgres unavailable | Kill Postgres pod | New sessions rejected (503); existing sessions continue (state in Redis); recovery on reconnect |
| Network partition (gateway <-> agent pod) | NetworkPolicy injection | gRPC deadline exceeded -> session retry; no data loss |
| Leader election disruption | Kill controller leader pod | New leader elected; pool reconciliation resumes; no double-claims |
| Concurrent SandboxClaim race | 50+ goroutines claiming simultaneously | Zero double-claims (ADR-007 verification) |
| Node drain during MinIO outage | Drain + MinIO NetworkPolicy block | Webhook blocks drain (`lenny-drain-readiness`); if forced, minimal state fallback |
| Dual-store outage (Redis + Postgres) | Kill both | System enters full degraded mode; existing sessions survive on in-memory state; alerts fire |
| Certificate expiry | Advance time / revoke cert | mTLS connections fail -> auto-renewal kicks in -> connections restored |

**CI gate:** Core chaos scenarios (pod kill, Redis down, concurrent claim race) on every nightly run. Full suite weekly and before releases.

### Layer 8: Security Tests

**Scope:** Validate security controls are effective, not just configured.

| Category | Tests |
|----------|-------|
| Tenant isolation | Cross-tenant read at every store layer (Postgres RLS, Redis prefix, MinIO prefix, namespace) |
| TLS enforcement | Plaintext connections rejected at Redis, Postgres/PgBouncer, gateway, intra-pod gRPC |
| Admission policy | Pod specs without required RuntimeClass -> rejected; `shareProcessNamespace: true` -> rejected |
| Credential leakage | Agent pod env/filesystem inspection: no credentials in env vars, no plaintext tokens on disk |
| NetworkPolicy | Agent pod `curl` to internet/Postgres/Redis -> connection refused |
| Input validation | Oversize payloads, malformed JSON, SQL injection strings, path traversal in artifact keys -> proper rejection |
| RBAC | User role -> cannot access admin API; tenant-admin -> cannot access other tenant |

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

### Agent-Optimized Pipeline

During agentic development, the full CI pipeline is too slow for the inner loop. Agents use `lenny-test` locally with layered escalation:

```
Agent Inner Loop (~10-30s):
  lenny-test --changed --max-layer component

Agent Pre-Commit (~1-3min):
  lenny-test --changed

Agent Full Validation (before marking task complete):
  lenny-test --spec <sections touched> --max-layer e2e
```

The `lenny-test` harness caches testcontainer instances across runs within the same agent session. Postgres, Redis, and MinIO containers are started once and reused, reducing component test startup from ~5s to <100ms after the first run.

---

## Directory Structure

```
tests/
├── spec-map.json               # Spec section -> test file mapping (machine-readable)
├── change-graph.json           # Package -> test suite dependency graph
├── results/
│   └── latest.json             # Most recent test run verdict
├── cmd/
│   └── lenny-test/             # Test selection and execution CLI
│       └── main.go
├── testinfra/                  # Shared test infrastructure
│   ├── containers.go           # testcontainers-go setup (Postgres, Redis, MinIO)
│   ├── fixtures.go             # Tenant seeds, session seeds, credential seeds
│   ├── assertions.go           # Cross-cutting: tenant isolation, state machine
│   ├── diagnosis.go            # Diagnosis comment extraction from test source
│   ├── verdict.go              # Structured JSON verdict generation
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

## Key Decisions

1. **`testcontainers-go` over mocks for stores.** Real Postgres/Redis/MinIO in containers. Mocks hide bugs (the spec explicitly warns about this for migrations). Containers are reused across test runs within an agent session.

2. **Build tags for layer separation.** `//go:build component`, `//go:build contract`, `//go:build integration`, `//go:build e2e`. Unit tests have no tag (always run). `lenny-test` manages tag selection.

3. **Test runtimes are shipped artifacts.** `echo`, `streaming-echo`, `delegation-echo` are built and published as container images alongside the main components. They are not test-only code — they are part of the platform's developer experience.

4. **Load tests produce machine-comparable artifacts.** JSON files with histograms, not human-readable reports. CI diffs against the previous baseline and fails on regression.

5. **Kind for PR-level e2e, GKE for nightly.** Kind is fast (30s cluster creation) and free. GKE validates gVisor, real NetworkPolicy enforcement, and managed-K8s-specific behavior that Kind cannot replicate.

6. **No test-only database schemas.** Tests use the real migration chain. This is both a test of the migrations and a guarantee that tests reflect production schema.

7. **`spec-map.json` and `change-graph.json` are maintained alongside code.** When an agent adds a new test, it updates both files. When an agent adds a new package, it adds the package to the change graph. The `lenny-test --validate-graph` command checks that every test file appears in at least one spec-map entry and every package appears in the change graph — CI fails if either is stale.

8. **Diagnosis strings are mandatory.** Every test that exercises a non-trivial behavior must include a `// diagnosis:` comment. The scaffolding phase writes these for every stub. A linter enforces their presence on component tests and above.

9. **Container caching for agent speed.** `testcontainers-go` instances persist across `lenny-test` invocations within a session via a Unix socket coordinator. First run pays the startup cost; subsequent runs reuse warm containers. This keeps the agent inner loop under 30 seconds for component-level tests.
