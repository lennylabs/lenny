# Lenny Testing Infrastructure

> Status: planning document. The infrastructure described here does not exist yet. This file is the build plan and the architecture reference for the test infrastructure that the platform will be built against.

## Table of Contents

1. [Purpose and philosophy](#1-purpose-and-philosophy)
2. [Outcomes the test infrastructure must deliver](#2-outcomes-the-test-infrastructure-must-deliver)
3. [Test layer model](#3-test-layer-model)
4. [Repository and directory layout](#4-repository-and-directory-layout)
5. [Spec traceability and the change graph](#5-spec-traceability-and-the-change-graph)
6. [The `lenny-test` harness](#6-the-lenny-test-harness)
7. [Verdict format and machine-readable output](#7-verdict-format-and-machine-readable-output)
8. [Test selection groups](#8-test-selection-groups)
9. [Local dependencies](#9-local-dependencies)
10. [Service and cluster provisioning](#10-service-and-cluster-provisioning)
11. [Test runtimes](#11-test-runtimes)
12. [Per-layer specification](#12-per-layer-specification)
13. [Build-and-test sequence](#13-build-and-test-sequence)
14. [Domain-specific test suites](#14-domain-specific-test-suites)
15. [Operability, CLI, and runbook tests](#15-operability-cli-and-runbook-tests)
16. [Documentation tests](#16-documentation-tests)
17. [Test authoring conventions](#17-test-authoring-conventions)
18. [Fixtures and golden data](#18-fixtures-and-golden-data)
19. [Property-based, fuzz, and mutation testing](#19-property-based-fuzz-and-mutation-testing)
20. [CI pipeline](#20-ci-pipeline)
21. [Flakiness and reliability](#21-flakiness-and-reliability)
22. [Quality gates and metrics](#22-quality-gates-and-metrics)
23. [Open questions and forward compatibility](#23-open-questions-and-forward-compatibility)

---

## 1. Purpose and philosophy

Lenny is being built with a spec-driven, test-driven workflow. The technical specification in `spec/` is the source of truth. The user-facing documentation in `docs/` reinforces and clarifies the contracts the spec defines. Implementation follows by making tests pass.

The test infrastructure exists before any application code. It encodes the spec as executable contracts, scaffolds failing tests for every required behavior, and gives implementing agents structured feedback when those tests fail.

This document is the build plan for that infrastructure. It defines the layered test taxonomy, the directory structure, the shared infrastructure packages, the dependencies, the local development modes, the CI pipeline, the test authoring conventions, and the optimal sequence in which the infrastructure should itself be built.

### Operating principles

1. **Tests are first-class artifacts of the spec.** Every test carries a `// spec:` annotation pointing back to one or more spec sections. Every spec section that defines behavior has at least one test.
2. **Agents are the primary test consumers.** Output is structured for machine reasoning. Diagnosis strings are written by the test author and surfaced verbatim on failure.
3. **Real backing services beat mocks.** Postgres, Redis, MinIO, and Kubernetes run in containers or Kind clusters. Mocks are reserved for genuinely external surfaces such as LLM providers and OAuth servers.
4. **Layered escalation.** Fast layers gate slow layers. A unit-test failure stops the run before a load test even starts.
5. **Local first.** Every layer below `e2e/cloud` runs on a developer laptop with one-command setup. The cloud tier is reserved for behavior that requires managed Kubernetes, gVisor at the node level, or sustained Tier 3 load.
6. **Deterministic and reproducible.** Container images, seed data, time, and randomness are pinned. Test runs that fail in CI must be reproducible on a laptop with the same command.
7. **Forward-compatible.** Areas the spec calls out as deferred (A2A adapters, multi-cluster federation, SSH git URLs) appear in the test infrastructure as interface stubs and unimplemented suites. The infrastructure does not lock in behavior the spec has not committed to.
8. **Tests are documentation.** A new contributor or a new agent reading a test must be able to understand the spec contract it covers from the test alone.

### How this document is maintained

This file changes when the test taxonomy changes, when a new dependency is introduced, when a new test layer is added, or when the build sequence is revised. Day-to-day test additions do not change this file: they update the spec map, the change graph, and the test packages.

---

## 2. Outcomes the test infrastructure must deliver

The infrastructure is complete when an implementing agent can:

1. Run `lenny-test --spec <section>` and get every test that encodes that spec section.
2. Run `lenny-test --changed` and get the minimal set of tests affected by uncommitted changes.
3. Run `lenny-test --group pr` and get the PR gate suite that completes in under fifteen minutes on a laptop.
4. Run `lenny-test --group nightly` and get the nightly suite that includes e2e on Kind and a representative load and chaos pass.
5. Run `lenny-test --group pre-release` and get the full release gate including the SLO re-validation suite.
6. Inspect `tests/results/latest.json` and find a machine-readable verdict, per-test diagnoses, and pointers to spec sections.
7. Add a new test, update the spec map and change graph in the same change, and have CI confirm the maps stay in sync.

The infrastructure is also complete only when the operator and the runtime author can:

8. Run the conformance harness against a third-party runtime image and receive a pass/fail report against Basic, Standard, and Full integration levels.
9. Run the operator preflight suite against a candidate Kubernetes cluster and receive a deterministic compatibility report.
10. Reproduce any failing test seen in CI with one local command and identical artifacts.

---

## 3. Test layer model

Lenny uses an eleven-layer model. Each layer has a scoped responsibility, a defined dependency set, an order in which it runs, and a position in the gate hierarchy.

| Tier | Name | Scope | Backing services | Runs in PR | Runs nightly | Runs pre-release |
|:----:|:-----|:------|:-----------------|:----------:|:------------:|:----------------:|
| 0 | Static | Lint, type-check, schema validate, query/schema linter, license check | None | Yes | Yes | Yes |
| 1 | Unit | Pure logic, no I/O, no goroutines spanning processes | None | Yes | Yes | Yes |
| 2 | Component | One Lenny component against real backing services | Postgres, Redis, MinIO, KMS stub, OIDC stub | Yes | Yes | Yes |
| 3 | Contract | Wire-format equivalence across surfaces (REST, MCP, OpenAI Completions, OpenAI Responses) | Gateway process, echo runtime | Yes | Yes | Yes |
| 4 | Integration | Multi-component flows via a local stack | Compose stack: gateway, controller-sim, agent pods, stores | Yes | Yes | Yes |
| 5 | E2E (Kind) | Full deployment on local Kubernetes | Kind cluster, Helm install, agent-sandbox CRDs | Critical-path subset | Full | Full |
| 6 | E2E (cloud) | Real managed Kubernetes with gVisor and Kata | GKE, EKS, and AKS, each with a sandbox-capable node pool | No | Subset | Full on all three |
| 7 | Load and SLO | Sustained traffic at tier scale; SLO assertions | Dedicated cloud cluster | Smoke only | Path-specific | Full Tier 2 plus 3 sampling |
| 8 | Chaos | Failure injection on a real cluster | Kind plus toxiproxy, plus cloud cluster for severe scenarios | Core scenarios | Most | All |
| 9 | Security | Tenant isolation, RBAC, NetworkPolicy, mTLS, admission policy, fuzzing | Kind plus dedicated security cluster, OWASP ZAP, kube-bench | Critical subset | Full | Full plus external pen-test |
| 10 | Conformance | Runtime adapter validation against the published contracts | `lenny-compliance` binary, sample images | Internal runtimes | All bundled | All bundled plus third-party |
| 11 | Documentation | Doc samples, links, code blocks, ADR catalog | Local doc build | Yes | Yes | Yes |

### Gate hierarchy

A failure in a lower tier short-circuits higher tiers within the same invocation. `lenny-test --changed --max-tier e2e` runs tiers 0 through 5 and stops at the first failing tier. The CI orchestrator records the skipped tiers as `status: skipped, reason: <lower-tier-failure>` rather than as absent.

### What "component" means

A component test wires a single Lenny component (for example, the Session Manager, the Token Service, the Warm Pool Controller) to real backing services and exercises the public interface of that component. It does not start a full gateway, does not stand up a Kubernetes cluster, and does not invoke a runtime. The boundary is `<one component> + <its stores>`. Component tests are the workhorse of the suite: they have realistic dependencies but they start in seconds.

### What "integration" means

An integration test exercises a flow that involves two or more Lenny components communicating through their normal interfaces, using the docker-compose local stack. It exercises wire formats, control flow, and persistence, but it does not require a Kubernetes API. The Warm Pool Controller appears as `controller-sim`, a stand-in that obeys the same RPC contract.

### What "e2e" means

An e2e test installs the full chart, registers runtimes, claims pods through the real Warm Pool Controller, exercises admission webhooks, and validates NetworkPolicy and mTLS end to end. Tier 5 uses Kind. Tier 6 uses managed Kubernetes for the behaviors Kind cannot reproduce (gVisor at node level, Kata, real load-balancer integration, multi-zone DR).

---

## 4. Repository and directory layout

```
lenny/
├── spec/                          # Source of truth (existing)
├── docs/                          # User-facing docs (existing)
├── schemas/                       # Wire-contract artifacts (Phase 1 deliverable)
│   ├── lenny-adapter.proto
│   ├── lenny-adapter-jsonl.schema.json
│   ├── outputpart.schema.json
│   └── workspaceplan-v1.json
├── migrations/                    # Authoritative Postgres migrations (Phase 1.5)
├── charts/lenny/                  # Helm chart
│   └── tests/                     # helm-unittest cases
├── pkg/                           # Application code (Phase 2 onward)
├── cmd/                           # Binaries
│   ├── lenny-gateway/
│   ├── lenny-ops/
│   ├── lenny-ctl/
│   ├── lenny-compliance/          # Runtime conformance harness
│   ├── lenny-test/                # Test selector and runner (this plan's CLI)
│   └── runtimes/
│       ├── echo/                  # Basic-level reference runtime
│       ├── streaming-echo/        # Streaming + lifecycle channel
│       └── delegation-echo/       # Scripted delegation behavior
└── tests/
    ├── README.md                  # Pointer to this file
    ├── spec-map.json              # Spec section → tests
    ├── change-graph.json          # Source package → tests
    ├── groups.yaml                # Named test selection groups
    ├── results/                   # Latest verdict, kept under .gitignore
    │   └── latest.json
    ├── testinfra/                 # Shared infrastructure
    │   ├── containers/            # testcontainers-go wrappers
    │   ├── kind/                  # Kind cluster lifecycle helpers
    │   ├── compose/               # docker-compose harness
    │   ├── fixtures/              # Seeded tenants, users, runtimes, pools
    │   ├── mocks/                 # LLM provider mocks, OAuth mocks
    │   ├── assertions/            # Cross-cutting assertions (RLS, state machine)
    │   ├── matrix/                # Contract-test matrix runner
    │   ├── chaos/                 # Failure injection primitives
    │   ├── load/                  # k6 / vegeta wrappers
    │   ├── timectl/               # Deterministic time control
    │   ├── randctl/               # Seeded RNG
    │   ├── diagnosis/             # Diagnosis extraction
    │   └── verdict/               # JSON verdict producer
    ├── tier0_static/
    ├── tier1_unit/                # Co-located with packages; this is the index
    ├── tier2_component/
    │   ├── stores/
    │   ├── controllers/
    │   ├── gateway_subsystems/
    │   └── translators/
    ├── tier3_contract/
    │   ├── rest_mcp/
    │   ├── rest_openai_completions/
    │   ├── rest_openai_responses/
    │   ├── adapter_jsonl/
    │   └── ocsf_audit/
    ├── tier4_integration/
    ├── tier5_e2e_kind/
    ├── tier6_e2e_cloud/
    ├── tier7_load/
    │   ├── scenarios/
    │   ├── slo.go
    │   └── baselines/
    ├── tier8_chaos/
    ├── tier9_security/
    │   ├── tenant_isolation/
    │   ├── network_policy/
    │   ├── admission/
    │   ├── mtls/
    │   ├── fuzz/
    │   └── pentest/               # Driver only; real pentest is external
    ├── tier10_conformance/
    │   ├── basic/
    │   ├── standard/
    │   └── full/
    └── tier11_docs/
```

### Why this layout

- Unit tests live next to the code they cover (`pkg/.../foo_test.go`). The `tests/tier1_unit/` directory is a convention marker, not a code location.
- Every other tier has its own directory, its own build tag, and its own dependency set.
- Shared infrastructure lives under `tests/testinfra/`. There is one canonical helper per concern (one container manager, one fixture loader, one verdict producer).
- `cmd/lenny-test/` builds the harness binary. It is the only entry point developers and CI use to run tests.

---

## 5. Spec traceability and the change graph

### Spec map

`tests/spec-map.json` maps every spec section to the tests, packages, migrations, and chart templates that encode it.

```json
{
  "version": 1,
  "sections": {
    "4.6.1": {
      "title": "Warm Pool Controller — Pod Lifecycle",
      "tests": [
        "tests/tier2_component/controllers/warmpool/claim_atomicity_test.go::TestSandboxClaimSkipLocked",
        "tests/tier5_e2e_kind/sandbox_claim_test.go::TestConcurrentClaimNoDoubleAssignment",
        "tests/tier8_chaos/concurrent_claim_test.go::TestSandboxClaimUnder100Goroutines"
      ],
      "packages": ["pkg/controller/warmpool", "pkg/store/podregistry"],
      "schemas": ["api/crds/sandbox_claim.yaml"],
      "migrations": ["0007_sandbox_claim_lock.sql"],
      "chart_templates": ["charts/lenny/templates/admission/sandboxclaim-guard.yaml"],
      "depends_on_sections": ["4.6", "12.3", "13.2"],
      "blocked_until_phase": "3.5"
    }
  }
}
```

Conventions:
- One entry per leaf spec section. Nested headings collapse to the leaf.
- `blocked_until_phase` records when the section is testable. Earlier phases skip its tests with a `not-yet-applicable` reason rather than failing.
- `depends_on_sections` enables the harness to surface adjacent context when a test fails: "Section 4.6.1 failed; cross-reference 4.6, 12.3, 13.2."

### Change graph

`tests/change-graph.json` maps source packages, schemas, migrations, and chart templates to the tests that exercise them.

```json
{
  "version": 1,
  "pkg/store/session": {
    "unit": ["pkg/store/session/..."],
    "component": ["tests/tier2_component/stores/session_store_test.go"],
    "integration": [
      "tests/tier4_integration/session_lifecycle_test.go",
      "tests/tier4_integration/checkpoint_resume_test.go"
    ],
    "e2e_kind": ["tests/tier5_e2e_kind/pod_lifecycle_test.go"]
  },
  "migrations/": {
    "component": ["tests/tier2_component/stores/...", "tests/tier2_component/rls/..."],
    "integration": ["tests/tier4_integration/migration_upgrade_test.go"],
    "static": ["tests/tier0_static/schema_lint_test.go"]
  },
  "charts/lenny/": {
    "static": ["charts/lenny/tests/...", "tests/tier0_static/helm_lint_test.go"],
    "e2e_kind": [
      "tests/tier5_e2e_kind/admission_policy_test.go",
      "tests/tier5_e2e_kind/network_policy_test.go"
    ]
  }
}
```

### Validation

The harness ships a `lenny-test validate-maps` subcommand. It runs in CI on every PR.

1. Every test function with a `// spec:` annotation appears in the spec map under each section it lists.
2. Every package under `pkg/` appears in the change graph (or under an explicit `pkg/**` glob).
3. Every spec section referenced under `spec/**.md` headings has at least one entry in the spec map. Exceptions are listed in `tests/spec-map-exceptions.yaml` with a justification.
4. Every test file appears in at least one tier directory and carries the expected build tag.
5. Every diagnosis comment (`// diagnosis: ...`) is present on tier-2-and-up tests that exercise non-trivial behavior.

### Diagnosis strings

Every component-and-higher test must carry a `// diagnosis:` comment immediately above the function declaration. The harness extracts this comment at compile time and surfaces it in the verdict.

```go
// spec: 4.6.1 (warm pool controller — pod lifecycle), 12.3 (postgres ha)
// diagnosis: ClaimSandbox returned a row that was already claimed.
//            Likely missing transaction isolation in pkg/controller/warmpool/claim.go,
//            or SELECT ... FOR UPDATE SKIP LOCKED is wrapping the wrong query.
func TestSandboxClaimSkipLocked(t *testing.T) { ... }
```

Diagnosis strings are not stack traces. They explain the most common cause of the most-likely failure mode. The author of the test owns the diagnosis.

---

## 6. The `lenny-test` harness

`cmd/lenny-test/` is the single entry point for running tests. It wraps `go test`, `helm-unittest`, `k6`, and the Kind cluster lifecycle. It produces a structured JSON verdict and a human-readable summary.

### Command surface

```bash
# Tier and group selection
lenny-test --tier <unit|component|...>            # Run a single tier
lenny-test --max-tier <tier>                      # Run tiers 0 through <tier>
lenny-test --group <pr|nightly|pre-release|...>   # Named selection (see §8)

# Change-aware selection
lenny-test --changed                              # Inspects git diff
lenny-test --changed --max-tier component         # Layered escalation
lenny-test --spec 4.2,12.3                        # Spec sections
lenny-test --pkg pkg/store/session                # Explicit package

# Discovery and validation
lenny-test --dry-run --changed                    # Show what would run
lenny-test list --tier component                  # List discoverable tests
lenny-test validate-maps                          # CI gate: maps in sync
lenny-test validate-diagnosis                     # CI gate: diagnosis coverage

# Infrastructure lifecycle (see §10)
lenny-test infra up   --profile compose|kind|all
lenny-test infra down --profile compose|kind|all
lenny-test infra prune
lenny-test infra status

# Conformance and operator surfaces
lenny-test conformance --image <runtime-image> --level <basic|standard|full>
lenny-test preflight   --cluster <kubeconfig>

# Output
lenny-test --output json|junit|tap|human
lenny-test --verdict-file tests/results/<name>.json
```

### Selection algorithm

1. Resolve selectors into a candidate test set:
   - `--tier` → all tests with that build tag.
   - `--max-tier` → all tests with tags up to and including that tier.
   - `--group` → the set named in `tests/groups.yaml`.
   - `--changed` → walks `git diff --name-only` (staged plus unstaged plus untracked under `pkg/`, `schemas/`, `migrations/`, `charts/`) through the change graph.
   - `--spec` → resolves each section through the spec map.
   - `--pkg` → resolves through the change graph.
2. Intersect with `--max-tier` if both selectors are present.
3. Order by tier ascending. Within a tier, group by package to maximize test-binary reuse.
4. For each tier, ensure required infrastructure is up. If `lenny-test infra status` reports the dependency stack as not running, start it (subject to `--no-infra`).
5. Execute. Stop after the first failing tier unless `--continue-on-failure` is set.
6. Emit the verdict to `tests/results/latest.json` and, if `--verdict-file` is set, to that path.

### Container caching daemon

The harness ships a long-lived helper, `lenny-test-cached`, that holds testcontainers running between invocations. On first run it provisions Postgres, Redis, MinIO, an OIDC stub, a KMS stub, and an OTLP collector. On subsequent runs it reuses the same containers and applies migrations on demand. Without this daemon, each component-tier run pays a five-to-ten-second container-startup tax.

The daemon exposes a Unix socket at `${XDG_RUNTIME_DIR}/lenny-test/cached.sock`. The harness probes the socket before starting; if absent, it spawns the daemon and waits for readiness.

The daemon is opt-in via `--cached`. CI defaults to fresh containers per run for isolation. Developers default to cached for speed.

---

## 7. Verdict format and machine-readable output

Every `lenny-test` invocation writes `tests/results/latest.json`. The format is stable and versioned.

```json
{
  "version": 1,
  "run_id": "01HV9X0ZW1ZF7K8Q1V2T3M4N5P",
  "started_at": "2026-05-13T11:00:00Z",
  "finished_at": "2026-05-13T11:02:13Z",
  "duration_ms": 133012,
  "command": "lenny-test --changed --max-tier integration",
  "trigger": {
    "mode": "changed",
    "git_revision": "abc1234",
    "changed_paths": ["pkg/store/session/session.go", "migrations/0042_session_lifetime.sql"],
    "resolved_packages": ["pkg/store/session", "migrations/"],
    "resolved_specs": ["4.2", "7.1", "12.3"]
  },
  "infrastructure": {
    "compose_profile": "default",
    "kind_cluster": null,
    "container_cache": "warm"
  },
  "tiers": {
    "static":      { "status": "pass", "duration_ms": 4200, "total": 18, "failed": 0 },
    "unit":        { "status": "pass", "duration_ms": 6100, "total": 412, "failed": 0 },
    "component":   {
      "status": "fail",
      "duration_ms": 18500,
      "total": 47,
      "passed": 45,
      "failed": 2,
      "skipped": 0,
      "failures": [
        {
          "test": "TestSessionStoreConcurrentClaim",
          "package": "tests/tier2_component/stores",
          "file": "tests/tier2_component/stores/session_store_test.go",
          "line": 142,
          "spec_sections": ["4.2", "4.6"],
          "diagnosis": "SELECT ... FOR UPDATE SKIP LOCKED returned an already-claimed row. ...",
          "error": "expected session claimed by gateway-1, got gateway-2",
          "duration_ms": 340,
          "stdout_tail": "...",
          "rerun_command": "lenny-test --pkg pkg/store/session --test TestSessionStoreConcurrentClaim"
        }
      ]
    },
    "integration": { "status": "skipped", "reason": "lower-tier-failure" },
    "e2e_kind":    { "status": "skipped", "reason": "not-requested" }
  },
  "verdict": "FAIL",
  "next_action": "Fix 2 component-tier failures in pkg/store/session. See spec sections 4.2 and 4.6 and tests/tier2_component/stores/session_store_test.go.",
  "spec_section_status": {
    "4.2": "FAIL",
    "4.6": "FAIL",
    "12.3": "PASS"
  }
}
```

### Field semantics

- `verdict` is one of `PASS`, `FAIL`, `INCONCLUSIVE`. `INCONCLUSIVE` means the harness could not complete the run (for example, infrastructure failed to start).
- `tiers.<name>.status` is one of `pass`, `fail`, `skipped`, `not-selected`. `skipped` carries a `reason`.
- `next_action` is one short, machine-readable sentence describing what the implementing agent should do next.
- `spec_section_status` aggregates test results by spec section. An agent working on section 4.2 reads this field to know whether the section is currently green.
- `rerun_command` is a copy-paste-ready command that runs the single failing test.

### Auxiliary formats

- `--output junit` emits JUnit XML for CI integration.
- `--output tap` emits TAP for terminal display.
- `--output human` emits a colorized summary suitable for an interactive terminal.

### Telemetry

Every run writes a row to `tests/results/history.jsonl` (gitignored). The harness uses this history for flakiness analysis (§21).

---

## 8. Test selection groups

`tests/groups.yaml` defines named selection groups. The same file backs `lenny-test --group <name>` and the CI pipeline.

```yaml
version: 1
groups:
  pr:
    description: PR gate. Target wall-clock under 15 minutes on a laptop.
    selectors:
      max_tier: integration
      include:
        - tier: e2e_kind
          subset: critical-path     # warm-pool, sandbox-claim, pod-lifecycle, mtls
        - tier: security
          subset: tenant-isolation,tls,admission
        - tier: chaos
          subset: core              # pod-kill, sandbox-claim-race, redis-down
        - tier: conformance
          subset: bundled-runtimes
        - tier: docs

  pr-fast:
    description: Developer inner loop. Target wall-clock under 90 seconds.
    selectors:
      changed: true
      max_tier: component

  nightly:
    description: Nightly gate. Includes full e2e on Kind plus path-specific load.
    selectors:
      include_all_tiers: true
      cloud_subset: critical-path

  pre-release:
    description: Release gate. Full e2e, full load, full chaos, security pentest driver.
    selectors:
      include_all_tiers: true
      cloud_subset: full
      load_baselines:
        compare_against: prior-release
        regression_threshold_pct: 15

  phase-3.5-gate:
    description: Phase 3.5 sign-off (admission policies, mTLS, lenny-ops first deploy).
    selectors:
      spec_sections: [4.6.1, 4.6.3, 13.1, 13.2, 17.2, 25.4]
      max_tier: e2e_kind

  phase-8-gate:
    description: Phase 8 sign-off (checkpoint, drain-readiness webhook, MinIO outage handling).
    selectors:
      spec_sections: [4.4, 4.5, 7.3, 12.5, 17.7]
      max_tier: chaos

groups_post_v1:
  - a2a-adapter
  - multi-cluster
  - kata-microvm-isolation
```

Phase-gated groups (`phase-3.5-gate`, `phase-8-gate`, etc.) exist for every milestone in the build sequence. They are the executable definition of "phase complete" used by the implementing agents.

### Subset definitions

Subsets are concrete sets of test names listed under `tests/groups.subsets.yaml`. Subsets are referenced by `subset: <name>` in groups. Subset definitions are reviewed when added or modified.

---

## 9. Local dependencies

The infrastructure runs on macOS (Intel and Apple Silicon) and Linux. Windows is supported through WSL2. The list below is the union of all dependencies; a developer working in tiers 0 through 4 needs only the core set.

### Core (required for tiers 0 through 4)

| Tool | Minimum version | Purpose |
|:-----|:---------------:|:--------|
| Go | 1.22 | Build the platform and run unit/component tests |
| Docker (or compatible: Colima, Rancher Desktop, OrbStack) | 24.0 | Container runtime for testcontainers and compose |
| docker compose | v2.20 | Tier 4 integration stack |
| make | any | Build orchestration |
| git | 2.40 | VCS |
| jq | 1.6 | JSON tooling in scripts |
| protoc | 25.0 | Generate gRPC stubs |
| buf | 1.30 | Proto linting and breaking-change detection |
| openssl | 3.0 | Self-signed mTLS certs in dev |
| sqlc or pgx codegen | latest | SQL codegen for migrations |
| golangci-lint | 1.56 | Tier 0 lint |
| migrate (or atlas, or goose) | latest | Run migrations |

### Local Kubernetes (tiers 5, 8, 9, parts of 7)

| Tool | Minimum version | Purpose |
|:-----|:---------------:|:--------|
| kubectl | 1.28 | Cluster interaction |
| Kind | 0.22 | Ephemeral local clusters |
| Helm | 3.13 | Chart install |
| helm-unittest | 0.4 | Chart template assertions |
| cert-manager CLI (cmctl) | 1.13 | mTLS PKI ops in tests |
| kuttl (optional) | 0.15 | Declarative e2e assertions |
| stern (optional) | 1.27 | Pod log multiplexing during debugging |

### Cloud Kubernetes (tier 6)

| Tool | Purpose |
|:-----|:--------|
| gcloud SDK | GKE cluster lifecycle, Cloud KMS, Cloud DNS, Cloud SQL, GCS |
| aws CLI v2 | EKS cluster lifecycle, AWS KMS, Route 53, RDS, S3 |
| az CLI | AKS cluster lifecycle, Azure Key Vault, Azure DNS, Azure Database for PostgreSQL, Azure Blob Storage |
| eksctl | EKS-specific cluster bring-up convenience |
| terraform | Optional, for reproducible cluster bring-up across providers |
| kubectl auth plugins | `gke-gcloud-auth-plugin`, `aws-iam-authenticator`, `kubelogin` (Azure AD) |

CI provisions credentials for each provider through OIDC federation rather than long-lived keys. Developers running cloud tests locally use `gcloud auth application-default login`, `aws sso login`, and `az login` against project-specific dev tenants.

### Load and chaos

| Tool | Purpose |
|:-----|:--------|
| k6 | Tier 7 load scenarios |
| vegeta (optional) | Burst HTTP load |
| toxiproxy | Latency/failure injection between services |
| chaos-mesh (cloud only) | Tier 8 chaos in real clusters |

### Security (tier 9)

| Tool | Purpose |
|:-----|:--------|
| OWASP ZAP | API fuzzing |
| kubeaudit | Pod-spec compliance |
| kube-bench | CIS Kubernetes benchmark |
| trivy | Image CVE scanning |
| cosign | Image signing verification |

### Documentation (tier 11)

| Tool | Purpose |
|:-----|:--------|
| Ruby + Jekyll | Build `docs/` |
| markdown-link-check | Link validation |
| vale (optional) | Prose linting per `.claude/rules/doc-style.md` |

### Setup automation

`scripts/setup-dev.sh` (deliverable in Phase 2 of the test-infrastructure build, §13) detects the host platform and installs the core set via the appropriate package manager (Homebrew on macOS, apt or dnf on Linux, scoop in WSL2). It refuses to run as root, refuses to overwrite existing installs without `--force`, and produces a single-line per-tool status table at the end.

`scripts/setup-cluster.sh` provisions a fresh Kind cluster with the Lenny test profile (single control plane, two workers, CNI with NetworkPolicy support). It is idempotent.

`scripts/preflight.sh` runs the same checks `lenny-ctl preflight` will eventually run, against the developer's host. It is the local "are you ready to develop" command.

---

## 10. Service and cluster provisioning

Tests need real backing services. Lenny defines four profiles for service provisioning, named in `tests/testinfra/`.

### Profile: `inproc` (tier 1, parts of tier 0)

- All services are stub or in-memory (SQLite, miniredis, fs-backed blob store).
- Used by unit tests and by `make run`.
- Starts in milliseconds.
- Does **not** validate Postgres-specific behavior (RLS, advisory locks, JSONB-specific indexes). Component and higher tiers must not depend on this profile.

### Profile: `containers` (tier 2)

- testcontainers-go starts Postgres, Redis, MinIO, an OIDC stub, a KMS stub, and an OTLP collector per test suite.
- Migrations applied on container startup. Same migration chain as production.
- Containers are torn down on suite completion. With `lenny-test-cached`, they are reused across suites within a developer session.
- Each test gets a fresh schema (`tests/testinfra/containers/postgres.NewSchema(t)`) so suites do not contaminate each other.

### Profile: `compose` (tiers 3, 4, 7-smoke)

- `docker compose -f tests/testinfra/compose/docker-compose.yaml --profile <name> up` starts a multi-service stack: gateway, controller-sim, stores, observability, mock LLM provider, mock OAuth provider.
- Profiles: `default` (plain HTTP, no TLS), `mtls` (cert-manager-equivalent self-signed PKI), `observability` (Prometheus, Grafana, Jaeger), `chaos` (toxiproxy in front of stores), `credentials` (real Token Service with KMS stub).
- The compose stack is the local equivalent of a Tier 1 deployment. It is the developer's "is the system working end-to-end" sanity check.
- The compose YAML and its profiles are versioned in the repository. Image tags pinned by digest from Phase 2 onward.

### Profile: `kind` (tier 5, parts of tiers 7, 8, 9)

- `tests/testinfra/kind/cluster.go` provisions a Kind cluster from a fixed config. The config enables NetworkPolicy via Calico, registers RuntimeClass entries for runc and gVisor (when host gVisor is available), and applies the `cert-manager` and `metrics-server` add-ons.
- A typical cluster takes 30 seconds to bring up cold; a cached cluster image takes 10 seconds.
- The harness applies the Helm chart with the test values file (`tests/testinfra/kind/values.yaml`). The values file sets `deploymentTier: tier1`, `global.devMode: false`, mounts test-specific TLS material, and enables the bootstrap Job.
- Each e2e test suite gets a fresh namespace and tears it down on completion. The cluster is reused across suites.

### Profile: `cloud` (tier 6, full tier 7, parts of tiers 8 and 9)

- Three providers, two cluster shapes each: `cloud-small-<provider>` (3-node, runc only, no sandbox) and `cloud-sandbox-<provider>` (3-node with the provider's sandbox node pool: gVisor on GKE, gVisor-via-Bottlerocket or Firecracker-via-Fargate on EKS, gVisor-via-containerd-handler or Confidential Containers on AKS).
- Cluster bring-up is automated via `scripts/cloud/<provider>/up.sh` and torn down via `scripts/cloud/<provider>/down.sh`. The full matrix detail lives in §12.6.
- Cluster lifecycle is managed by CI for nightly and pre-release. Developers do not bring up cloud clusters from their laptops in routine work.
- The cloud profile is the only one that validates managed-K8s-specific behavior: external LB ingress, cloud-provider CSI, real DNS via external-dns, real cert-manager with Let's Encrypt staging, provider-native KMS, provider-native workload identity.

### Service stubs and mocks

The following services are stubbed because their real counterparts are external or expensive:

- **LLM providers (Anthropic, OpenAI, Google, AWS Bedrock, Azure OpenAI).** `tests/testinfra/mocks/llm-provider/` is an HTTP server that speaks the public protocols. It supports configurable token-count responses, configurable streaming patterns, configurable error injection (429, 500, 503, dropped TCP), and deterministic token usage. Component, integration, and e2e tests use the mock by default. A pre-release smoke run can optionally point at real providers under a separate secret.
- **OIDC provider.** `tests/testinfra/mocks/oidc/` issues JWTs with configurable claims and rotates signing keys on demand.
- **OAuth connector provider.** `tests/testinfra/mocks/oauth-connector/` simulates GitHub, Jira, and a generic provider.
- **KMS.** `tests/testinfra/mocks/kms/` implements the KMS interface in-memory with envelope encryption, key rotation, and per-tenant-key admin operations.
- **SIEM endpoint.** `tests/testinfra/mocks/siem/` receives OCSF events and logs them for audit-pipeline tests.

Mocks are versioned with the platform. Their interfaces are exercised by their own unit tests in `tests/testinfra/mocks/*/...`. Production code does not depend on mock packages.

### Time and randomness control

- `tests/testinfra/timectl/` exposes a `Clock` interface used by every component that reads wall-clock time. The default implementation is real time. Tests substitute a frozen or advanceable clock.
- `tests/testinfra/randctl/` exposes a deterministic RNG keyed by the test name. UUIDs, nonces, and bucketing seeds are derived from this RNG in tests. The platform code reads RNG from a registry that defaults to `crypto/rand` in production and the test RNG in tests.

These two utilities are foundational: most flaky tests in distributed systems trace back to uncontrolled time or uncontrolled randomness.

---

## 11. Test runtimes

The reference catalog (§spec/26) defines nine production runtimes. The test infrastructure builds, ships, and tests three additional runtimes that exist purely to drive the test harness. They are first-class artifacts: built, published, and run in CI alongside the production runtimes.

### `echo` (Basic level)

- Reads JSON Lines from stdin, echoes input with sequence numbers.
- Responds to `heartbeat`, `shutdown`, unknown message types per the adapter contract.
- Built and registered as a reference runtime from Phase 2 onward.
- Used by every tier from 2 through 6 to exercise session lifecycle without an LLM.

### `streaming-echo` (Full level)

- Extends `echo` with simulated streaming output (configurable chunk count and inter-chunk delay).
- Implements `ReportUsage` with deterministic token counts.
- Implements the lifecycle channel: `checkpoint_request` → `checkpoint_ready`, `interrupt_request` → `interrupt_acknowledged`, `credentials_rotated` → `credentials_acknowledged`, `deadline_approaching`.
- Built from Phase 2.8 onward.
- Used to validate streaming reconnect, quota enforcement, checkpoint timing, credential rotation, and interrupt flows.

### `delegation-echo` (Standard level)

- Executes a pre-defined sequence of MCP tool calls (`lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/send_message`, `lenny/request_input`) according to a JSON script supplied in `runtimeOptions`.
- Built from Phase 9 onward.
- Used to validate delegation budgets, scope narrowing, isolation monotonicity, cycle detection, the elicitation chain, and the task-tree state machine.

### Conformance fixtures

`tests/testinfra/runtimes/conformance-fixtures/` ships additional runtime images that intentionally violate the contract: missing `heartbeat_ack`, mis-formatted JSON Lines, unknown message types, oversized payloads, blocked stdin, etc. The conformance harness runs each fixture against `lenny-compliance` and asserts the expected diagnostic.

### Runtime test images are reproducible

Every test runtime ships with a `Dockerfile`, a pinned base image digest, and a build script that produces a deterministic image (`SOURCE_DATE_EPOCH`, no embedded timestamps, no network during build). CI builds them on every PR; release builds digest-pin them in `tests/testinfra/runtimes/images.lock`.

---

## 12. Per-layer specification

This section defines each tier's scope, dependencies, conventions, gate criteria, and a representative set of test cases. It is the longest section of this document.

### 12.0 Tier 0 — Static

**Scope.** Anything that does not need a running process or container.

**Checks.**
1. `go vet ./...`
2. `golangci-lint run` with the project's `.golangci.yml`. Enabled linters include `errcheck`, `gosimple`, `staticcheck`, `unused`, `govet`, `ineffassign`, `gocyclo`, `gosec`.
3. `gofumpt -l .` — formatting.
4. `goimports -l -local github.com/lenny-labs/lenny .` — import ordering.
5. `helm lint charts/lenny`.
6. `helm template charts/lenny --values tests/testinfra/kind/values.yaml | conftest test --policy charts/lenny/policy/` — chart policy.
7. `buf lint schemas/` and `buf breaking schemas/ --against .git#branch=main`.
8. `scripts/lint-schema.sh` — every tenant-scoped table has `tenant_id` as the leading index column (R-01).
9. `scripts/lint-queries.sh` — no cross-tenant JOIN without the matching `a.tenant_id = b.tenant_id` clause (R-02), and the cross-tenant exception annotation inventory matches the documented list.
10. `scripts/lint-migrations.sh` — every migration has a corresponding rollback script and a non-empty test under `tests/tier2_component/migrations/`.
11. JSON Schema validation of every example payload under `docs/`.
12. `markdown-link-check` over `docs/` and `spec/`.
13. License header on every Go source file.
14. ADR catalog cross-check: every ADR file has a matching entry in `docs/adr/index.md` and the spec §19 table.
15. Spec-map and change-graph integrity (`lenny-test validate-maps`).
16. Diagnosis-comment coverage (`lenny-test validate-diagnosis`).
17. Proto-generated code is up to date (`buf generate && git diff --exit-code`).

**Gate.** Every check must pass. No retries.

**Wall-clock target.** Under 30 seconds on a laptop.

### 12.1 Tier 1 — Unit

**Scope.** Pure logic. No I/O, no goroutines coordinating across processes, no clock-of-the-host dependencies.

**What gets unit-tested.**

- State machines. Every state machine in the platform has table-driven transition tests with full coverage of valid and invalid transitions.
  - Session state machine (created → finalizing → ready → starting → running → completed/failed/cancelled/expired, with the suspended and resume_pending intermediates).
  - Pod state machine (pod-warm, SDK-warm, claimed, running, draining, terminated, failed).
  - SandboxClaim state machine (pending, granted, fenced, released, expired).
  - TaskRecord state machine (submitted, running, completed, failed, cancelled, expired, input_required).
  - Lease-extension state machine including cool-off and rejection flag.
  - Pool upgrade state machine (scaling, expanding, draining, contracting, complete) with pause/resume/rollback.
- Tenant context propagation. Helpers that wrap a transaction with `SET LOCAL app.current_tenant` and clean up on exit.
- Quota arithmetic. Sliding window, per-replica budget allocation, fail-open cumulative timer, MAX rule reconciliation.
- Delegation policy evaluation. Depth and fan-out limits. Isolation monotonicity. Scope narrowing. Budget carving. Self-recursion cycle detection.
- Workspace plan parsing and validation. Every source type. Every mode validation. Path normalization. Last-writer-wins.
- Setup command allowlist. Argument splitting, shell-false enforcement.
- Adapter manifest generation. Nonce computation. Schema correctness.
- Lifecycle channel message parsing.
- Output part validation. Inline-versus-ref mutual exclusion, size thresholds, MIME type checks, schema version handling.
- MCP message framing. Protocol version negotiation.
- OCSF translation. Lenny event → OCSF event mapping is exhaustive over the event catalog.
- Wire-contract Go bindings. Round-trip of every proto message and every JSON Schema.
- Helm template rendering. Given values, produce expected manifests. Use `helm-unittest` for assertions.
- Error classification. Retryable vs. permanent, category mapping.
- Bucketing function. Deterministic variant assignment for experiment routing.
- Hash-chain integrity computation. Audit-event `prev_hash` calculation.
- Tracing-context propagation. OTel context to and from `lenny/set_tracing_context`.
- Image-resolver precedence. Override > url > default; digest enforcement.
- Recommendations rule engine. Pool sizing formula, credential utilization, OOM, retention.

**Conventions.**
- Table-driven tests are the default. Each row carries a name, an input, and an expected output (or expected error).
- `go test -race -count=1` is the default. Race detection is non-negotiable for any code that uses goroutines or channels.
- Fuzz targets exist for every parser: lifecycle channel, MCP envelope, adapter manifest, output parts, workspace plan, OCSF event.
- Property-based tests via `pgregory.net/rapid` cover algebraic invariants: monotonicity of budget carving, idempotency of GC sweeps, stability of state-machine round-trips.

**Coverage target.** 80% on new code, measured by `go-cover` and enforced by CI. The threshold is not a project-wide floor; refactoring is not penalized.

**Wall-clock target.** Under 60 seconds for the full unit-tier run.

### 12.2 Tier 2 — Component

**Scope.** One Lenny component plus its real backing services. No other Lenny components.

**Profile.** `containers`. testcontainers-go starts the required stores. Migrations applied. Tests run against the resulting schema.

**Suites.** Organized under `tests/tier2_component/`.

#### 12.2.1 Store interface contracts

For every store role identified in spec §12.2, a contract suite asserts the public interface against the real backend. Each suite is parameterized so a future implementation (for example, `PostgresPodRegistry` in place of `CRDPodRegistry`) reuses the same tests.

| Suite | Backend | Coverage |
|:------|:--------|:---------|
| SessionStore | Postgres | CRUD, state machine, concurrent claim via `SELECT ... FOR UPDATE SKIP LOCKED`, RLS, delegation-tree co-location, lineage, orphan reconciliation, retry-state persistence, FK cascade |
| LeaseStore | Redis + Postgres advisory | Acquire/renew/release, TTL expiry, Redis-outage fallback to advisory lock, atomic re-acquisition after crash |
| QuotaStore | Redis + Postgres | Increment/decrement, sliding window, fail-open semantics, per-user fraction, per-replica hard cap, cumulative timeout, MAX-rule reconciliation, storage-quota pre-check, GC decrement |
| TokenStore | Postgres encrypted | KMS-envelope encryption, hash storage, revocation lookup, rotation, RLS |
| TokenIssuanceStore | Postgres | JTI uniqueness, revocation index, parent-jti tracking, expired-row GC |
| ArtifactStore | MinIO | Tenant-prefix validation, SSE-KMS for T3/T4, per-tenant key for T4, checkpoint rotation, legal-hold suspension, soft-delete idempotency, tombstone hard-prune, partial-manifest cleanup, eviction-context cleanup, GC exception handling, T4 KMS probe, MinIO-outage fallback to minimal state |
| EventStore | Postgres | Audit hash chain, sequence monotonicity, event schema versioning, OCSF translation state machine, EventBus publish state, retranscribe-worker sweep, terminal-failure escalation, T2 batching loss alert, startup chain-continuity check, RLS, erasure |
| CredentialPoolStore | Postgres encrypted | Pool CRUD, lease assignment, health scoring, deny-list, revocation, encryption, RLS, erasure |
| EvictionStateStore | Postgres | Eviction-state CRUD, MinIO context-key storage, terminal-state cleanup, RLS |
| MemoryStore | Postgres + pgvector (default), pluggable | RLS, user-scope isolation, tenant-scope isolation, mandatory DeleteByUser/DeleteByTenant, startup preflight, per-job preflight, custom-backend contract validation, retention TTL, capacity eviction |
| EvalResultStore | Postgres | Score CRUD, FK to sessions, RLS, erasure cascade |
| SemanticCache | Redis (default), pluggable | Scope isolation (u:/s:/t:), per-tenant prefix, miss-on-outage, erasure |
| PodRegistry | Kubernetes API (CRDPodRegistry) | All ops listed in spec §12.6, optimistic locking via `resource_version`, WatchPods events within 500ms P99 |
| StoreRouter | Postgres + Redis | Session-shard extraction, tenant-shard routing, billing/audit-shard routing (R-03), scatter-gather concurrency and timeout, partial-result semantics |
| EventBus | Redis pub/sub | CloudEvents envelope, tenant-prefixed channels, at-most-once delivery, retranscribe semantics |

Every suite asserts the mandatory `DeleteByUser(ctx, tenantID, userID) error` and `DeleteByTenant(ctx, tenantID) error` methods. The interface is compile-time-enforced; the tests confirm the implementation actually deletes.

#### 12.2.2 RLS and tenant isolation

`tests/tier2_component/rls/` is a self-contained suite that does not test a single component. It connects to Postgres directly, seeds tenants A and B with rows on every tenant-scoped table, and asserts:

- A query in tenant-A context returns zero rows for tenant-B data on every table.
- A query without `SET LOCAL app.current_tenant` raises an exception via `lenny_tenant_guard`.
- A query with `app.current_tenant = '__all__'` succeeds and emits a `cross_tenant_read` audit event.
- Connection pooler reuse does not leak the previous transaction's `app.current_tenant`.
- The schema linter (`scripts/lint-schema.sh`) running on the migration set identifies all tenant-scoped tables and confirms `tenant_id` is the leading index column or is annotated.

#### 12.2.3 Gateway internal subsystems

The gateway is internally partitioned into Session Orchestrator, File Fabric, MCP Fabric, Admin Plane, and LLM Proxy. Each subsystem has a component-tier suite that wires it to real stores and mocked peers.

- Session Orchestrator: create → attach → prompt → complete with `streaming-echo`.
- File Fabric: upload/download through the gateway against real MinIO.
- MCP Fabric: platform MCP tools (`lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/send_message`, `lenny/request_input`) against real stores.
- Admin Plane: the full admin REST surface against real stores and a real OIDC stub. Includes role ceiling enforcement and idempotency-key handling.
- LLM Proxy: lease-token validation, native translator for `anthropic_direct`, request/response/SSE translation, deny-list enforcement, per-subsystem isolation, circuit-breaker behavior. Validated against the mock LLM provider.

#### 12.2.4 Controllers

- Warm Pool Controller: reconciliation loop against a fake `client-go` lister, then against `envtest`. Pool grows to target warm count, scales down on idle, respects PDB, recovers leader election.
- Pool Scaling Controller: scaling-formula computation, admission-denied retry-with-backoff, `PoolScalingAdmissionStuck` alert wiring.
- Token Service: AssignCredentials, RevokeCredentials, RotateCredentials, multi-replica leader election, KMS-envelope encryption.

#### 12.2.5 Translators

The LLM Proxy native translator is exercised against canonical request/response pairs from the OpenAI and Anthropic SDKs. The mock LLM provider records every translated request and the suite asserts the round-trip is byte-equivalent for the canonical inputs.

**Wall-clock target.** Under 5 minutes for the full component-tier run with warm containers; under 8 minutes cold.

**Gate.** 100%. No flakiness budget.

### 12.3 Tier 3 — Contract

**Scope.** Wire-format equivalence and protocol conformance across surfaces.

**Profile.** `compose` with the gateway, echo runtime, and stores. mTLS profile when validating TLS-only behaviors.

**Suites.**

#### 12.3.1 REST ↔ MCP consistency

The single largest contract suite. For every overlapping operation, the harness sends the same logical request through REST and MCP and asserts semantic equivalence.

Operations covered:
- Session create, get, list, message send, message list, delete.
- Workspace upload (including multipart, archive, gitClone).
- Task create, list, cancel, get-task-tree.
- Elicitation request, respond, dismiss.
- Memory write, query, delete.
- Delegation: delegate_task, await_children, cancel_child, discover_agents.
- Webhook subscription CRUD.
- Admin: runtime CRUD, pool CRUD, connector CRUD, tenant CRUD, credential-pool CRUD, audit query.

For each operation and each adapter, assertions:
1. Success: identical payloads modulo transport envelope.
2. Validation errors: same `code` and `category`.
3. Authz rejections: same denial behavior.
4. `retryable` and `category` flags identical across surfaces.
5. State transition sequences identical.
6. Pagination: cursor semantics, page sizes, empty-result shapes.

#### 12.3.2 REST ↔ OpenAI Chat Completions

Translation-fidelity matrix. The harness sends a Lenny session over REST and the equivalent request over `/v1/chat/completions`. It asserts the documented fidelity loss is exact (for example, `schemaVersion` is dropped, `reasoning_trace` is lossy, `ref` is dropped). Anything dropped that should not be dropped, and anything preserved that should be lossy, is a contract violation.

#### 12.3.3 REST ↔ OpenAI Responses

Same shape as 12.3.2 against the Responses API, including the `id` field's extended behavior.

#### 12.3.4 Adapter binary protocol

The harness drives an adapter binary over its stdin/stdout JSON Lines channel and asserts:
- Every documented message type is accepted.
- Unknown message types are ignored.
- Heartbeat ack arrives within 10 seconds.
- Shutdown completes within the declared deadline.
- Forward compatibility: future-typed messages do not crash the adapter.
- Lifecycle channel handshake (`lifecycle_capabilities` → `lifecycle_support`) is correct.

This suite runs against every test runtime and every reference runtime. A third-party runtime registers via `lenny-compliance` (see §12.10).

#### 12.3.5 OCSF audit-event schema

Every audit event Lenny emits has an OCSF translation. The suite generates one of each event type and asserts the translated event passes OCSF schema validation. The OCSF retranslation API is exercised: a failed translation is retried and the audit row transitions to success or dead-letter.

#### 12.3.6 Workspace plan JSON schema

The published schema at `https://schemas.lenny.dev/workspaceplan/v1.json` validates every documented example. Forward-compatibility: a plan with a `schemaVersion` higher than the gateway understands is rejected with `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED`. Unknown source types are skipped with the documented warning. Known types with unknown fields are rejected.

#### 12.3.7 CloudEvents envelope

EventBus publishes CloudEvents v1.0.2 messages. The suite asserts every documented event type maps to the correct envelope shape, with the documented Lenny extensions (`lennytenantid`, `lennyrootsessionid`, `lennyoperationid`).

**Gate.** 100%. Contract tests block merge.

**Wall-clock target.** Under 4 minutes.

### 12.4 Tier 4 — Integration

**Scope.** Multi-component flows through the compose stack.

**Profile.** `compose` with the chosen profile. Most tests use `default`; mTLS-specific tests use `mtls`.

**Suites.**

| Suite | Coverage |
|:------|:---------|
| session_lifecycle | Create → upload → attach → prompt → complete via REST and MCP, with `streaming-echo` |
| streaming_reconnect | Client disconnects mid-stream, reconnects with `Last-Event-ID`, replay from cursor, no duplicate or missing events |
| checkpoint_resume | Eviction → checkpoint to MinIO → resume on new pod → workspace restored. Including MinIO-outage fallback to Postgres minimal state |
| delegation | Parent delegates to `delegation-echo` child; result propagation; budget carving and enforcement; isolation monotonicity; scope narrowing; depth and fan-out limits |
| delegation_recovery | Parent loses connection to child mid-task; reconnect; task-tree restored from Postgres |
| delegation_self_recursion | `allowSelfRecursion: false` rejects; `true` permits one repeat; second repeat rejected by cycle detector |
| credential_lifecycle | Credential assignment → rotation (`credentials_rotated` lifecycle message) → runtime re-bind → emergency revoke → active session terminated |
| credential_fallback | Primary provider unavailable; fallback chain activates; health scores update; cooldown respected |
| credential_revocation | Emergency revoke → deny-list propagation via Redis pub/sub → active leases terminated within propagation SLO |
| concurrent_workspace | `slotId` multiplexing: N prompts on same pod; per-slot credential isolation; per-slot cleanup timeout |
| concurrent_stateless | Tenant-affinity routing; cross-tenant routing rejected; pod assignment correctness |
| quota_enforcement | Token budget exhaustion → `budget_exhausted` → session termination; storage quota → upload rejection; per-tenant rate limit → 429 |
| quota_recovery | Redis outage → fail-open with timer; Redis recovery → MAX-rule reconciliation |
| migration_upgrade | Apply migration N, seed data, apply N+1, assert data integrity; rollback N+1, assert reversibility |
| migration_concurrent | Two replicas attempt the same migration simultaneously; dirty-flag lock prevents duplicate apply |
| admin_bootstrap | `lenny-ctl bootstrap` seeds runtimes, pools, tenants, users; idempotent re-run succeeds; dry-run shows accurate diff |
| admission_policy_compose | Compose-level policy mocks reject the same payloads the e2e admission webhooks would reject |
| mcp_elicitation_chain | External tool → gateway connector → child → parent → gateway edge → client; SHA-256 integrity in `enforce` mode rejects tampering |
| oauth_connector_flow | State parameter, PKCE S256, code exchange, token storage encrypted, token never transits pod |
| audit_pipeline | Audit events written, hash chain valid, OCSF translation succeeds, EventBus publishes, SIEM endpoint receives |
| erasure_job | `DeleteByUser` runs the 19-step sequence in order, MemoryStore preflight passes, legal-hold blocks override, audit trail records actor and justification |
| webhook_delivery | Subscription registered, event delivered, HMAC-SHA256 signature validates, replay window enforced, retry with exponential backoff |
| llm_proxy_anthropic | `streaming-echo` issues an Anthropic chat completion; native translator converts to Anthropic; SSE streams back; usage extracted |
| lenny_ops_endpoints | Health, recommendations, version/config, drift detection, audit query, backup/restore preview against the compose stack |

**Wall-clock target.** Under 10 minutes for the full integration-tier run.

**Gate.** 100%.

### 12.5 Tier 5 — E2E on Kind

**Scope.** Full Lenny install on Kind. CRDs, controllers, warm pools, admission webhooks, NetworkPolicy, mTLS. RuntimeClass runc only on Kind (gVisor is exercised on cloud).

**Profile.** `kind`. The harness brings up a fresh Kind cluster per test suite or per CI job (configurable).

**Cluster bring-up.** `tests/testinfra/kind/cluster.go` creates a Kind cluster with the project's pinned image. CNI: Calico. RuntimeClasses: runc and (when available) gVisor. Add-ons: cert-manager, metrics-server. Helm install applies the chart with `tests/testinfra/kind/values.yaml`. Bootstrap Job seeds the canonical Day-1 resources.

**Suites.**

| Suite | Coverage |
|:------|:---------|
| warm_pool | Pool scaling to `minWarm`; scale-up on claim; scale-down on idle; PDB respected during drain |
| sandbox_claim | Optimistic locking under concurrent goroutines; zero double-claims; `lenny-sandboxclaim-guard` webhook fences stale claims |
| pod_lifecycle | Claim → assign credentials → workspace materialize → prompt → checkpoint → release; pod returns to pool |
| node_drain | `kubectl drain` triggers preStop checkpoint; session resumes on new pod; workspace intact |
| admission_policy | Controller-generated pod specs pass PSS admission for each RuntimeClass. Violations are rejected with the documented error codes |
| admission_inventory | Phase-aware: chart-rendered webhook set matches the preflight expected set; `lenny-preflight` Job passes |
| network_policy | Pod → gateway: allowed. Pod → Postgres/Redis/internet: denied. Pod → DNS: only `lenny-system` CoreDNS |
| mtls_enforcement | Gateway ↔ pod gRPC over mTLS; plain text rejected; cert auto-renewal at 2/3 lifetime |
| label_immutability | `lenny-label-immutability` webhook prevents post-creation label mutation; non-WarmPoolController creator setting `lenny.dev/managed: "true"` is rejected |
| sandbox_finalizer | Delete sandbox with active session blocked by finalizer; checkpoint completes; finalizer removed; pod deleted |
| orphan_claim_gc | Gateway crash after SandboxClaim creation; controller detects orphan and cleans up |
| drain_readiness_webhook | MinIO unhealthy; `lenny-drain-readiness` webhook blocks pod eviction; forced drain falls back to minimal state |
| tenant_namespace_isolation | Tenant A pods cannot reach tenant B namespace (NetworkPolicy validated end-to-end) |
| pool_upgrade | `lenny-ctl admin pools upgrade start` drives the state machine through expanding, draining, contracting, complete; pause/resume/rollback work |
| schema_migration | Phase 1 applied, Phase 2 deployed (gateway warm-restarts on new schema), Phase 3 applied (gate check passes); rollback dirty migration |
| bootstrap_first_install | Fresh chart install with the default values produces a Ready gateway; smoke test against `chat` runtime succeeds within five minutes |
| lenny_ops_first_deploy | `lenny-ops` Deployment Ready in Phase 3.5 chart slice; Lease-based leader election works |
| playground | Web playground serves `/playground`; OIDC, API-key, and dev auth modes work; CSP and HttpOnly cookies present |
| oidc_authentication | Real OIDC stub: PKCE flow, state cookie, tenant-claim extraction, cookie-to-bearer exchange |
| token_rotation | `lenny-ctl admin users rotate-token` issues new token; old token valid through graceful handoff |
| audit_query | `lenny-ctl audit` scatter-gather across shards returns expected events |

**Critical-path subset** (runs on PR): warm_pool, sandbox_claim, pod_lifecycle, mtls_enforcement, admission_policy, admission_inventory, lenny_ops_first_deploy, bootstrap_first_install. Target wall-clock under 10 minutes including cluster bring-up with cached node image.

**Full e2e Kind suite** (runs nightly): everything above. Target wall-clock under 45 minutes.

### 12.6 Tier 6 — E2E on cloud

**Scope.** Behaviors that Kind cannot reproduce. Lenny supports the three major managed-Kubernetes platforms and validates each independently.

**Profile.** `cloud`. Three cluster shapes per provider:

| Provider | Service | Sandbox option | Notes |
|:---------|:--------|:---------------|:------|
| Google Cloud | GKE | gVisor (built-in sandbox node pool) | Canonical configuration; tightest sandbox integration |
| AWS | EKS | gVisor via Bottlerocket variant, or Firecracker via Fargate | Object storage swap to S3; KMS via AWS KMS |
| Azure | AKS | gVisor via Kata-equivalent containerd handler, or Confidential Containers | Object storage swap to Azure Blob; KMS via Azure Key Vault |

For each provider the harness brings up two cluster shapes: `cloud-small-<provider>` (3-node, runc only, no sandbox) and `cloud-sandbox-<provider>` (3-node with the provider's sandbox node pool). Cluster lifecycle is automated by `scripts/cloud/<provider>/{up,down}.sh`. All three providers use the same Helm chart and the same Lenny version.

**Suites.** Each suite runs against each provider unless noted.

| Suite | Coverage |
|:------|:---------|
| gvisor_isolation | Default isolation profile (sandboxed) creates pods on the gVisor (or provider-equivalent) node pool. Process namespace, syscall filtering, and capability dropping behave per documented sandbox semantics |
| kata_isolation | Kata or microVM RuntimeClass pods boot, accept claims, and tear down within the documented latency budget. GKE and AKS variants validated; EKS validates Firecracker-via-Fargate as the equivalent shape |
| multi_zone_dr | Postgres failover across zones with RPO=0 and RTO under 30 seconds. Session in-flight survives. Runs per provider against the provider's native multi-AZ Postgres offering (Cloud SQL HA, RDS Multi-AZ, Azure Database for PostgreSQL Flexible Server with HA) |
| managed_ingress | Provider-native external LB (GCP HTTPS LB, AWS ALB via the AWS Load Balancer Controller, Azure Application Gateway); TLS termination at the edge; real cert-manager with Let's Encrypt staging or provider-native managed certs |
| external_dns | External-DNS-driven CNAME entries for the gateway and playground against Cloud DNS, Route 53, or Azure DNS |
| cloud_csi | `ArtifactStore` against the provider's native object storage: GCS on GKE, S3 on EKS, Azure Blob Storage on AKS. The same interface tests pass against every backend |
| cloud_kms | T4 per-tenant keys against the provider's native KMS: Cloud KMS on GKE, AWS KMS on EKS, Azure Key Vault on AKS. KMS probe, key rotation, key-unavailable fail-closed validated for each |
| cloud_oidc | Workload identity / IRSA / Workload Identity Federation: pods receive provider-issued tokens that map to cloud IAM roles per the documented configuration |
| cloud_secret_store | Native secret backend integration: Secret Manager (GCP), Secrets Manager (AWS), Key Vault (Azure) as alternative TokenStore backends through the same interface |
| multi_az_minio | If using self-managed MinIO, multi-zone replication and near-zero RPO. Optional per provider |
| cloud_observability | OTLP delivery to the provider-native collector (Cloud Trace and Cloud Logging on GCP, X-Ray and CloudWatch on AWS, Application Insights and Log Analytics on Azure) |
| cloud_billing_export | Per-tenant usage events flow to the provider's billing-export sink (BigQuery, Athena, Azure Data Lake) in the documented format |

**Provider parity matrix.** A row in `tests/tier6_e2e_cloud/parity-matrix.yaml` lists every documented capability and the providers it is validated against. CI fails if a capability is claimed in the spec or chart but missing from the matrix.

**Cadence.**
- Nightly: critical-path subset (`gvisor_isolation`, `cloud_csi`, `cloud_kms` per provider) rotated across providers so each provider runs at least every 48 hours.
- Weekly: full suite on the canonical provider (GKE).
- Pre-release: full suite on **all three providers (GKE, EKS, AKS)** including both cluster shapes per provider.

### 12.7 Tier 7 — Load and SLO

**Scope.** Performance under sustained load. SLOs from spec §16.5 are asserted directly.

**Tooling.** `k6` is the primary load generator. `tests/testinfra/load/scenarios/` holds the Go-and-JavaScript scenarios.

**Scenarios.**

| Scenario | Load shape | SLO asserted |
|:---------|:-----------|:-------------|
| session_throughput | Ramp to 500 concurrent on Kind, 5000 on cloud, sustained 10 minutes | Session creation P99 < 500ms; pod startup P95 < 2s (runc) and < 5s (gVisor) |
| streaming_reconnect_under_load | 500 concurrent streaming sessions, periodic disconnects | Streaming reconnect latency P95 < 500ms; zero event loss |
| delegation_fanout | Single root with N=50 concurrent children, depth=10 | Tree completes within 30 seconds; budget enforcement correct |
| credential_rotation_under_load | 200 concurrent sessions, rotate provider credentials | Rotation propagation P95 < 5 seconds; in-flight requests succeed |
| checkpoint_duration | Workspaces at 10MB, 100MB, 500MB | ≤ 100MB checkpoint P95 < 2s with cooperative quiescence overhead included |
| pod_claim_latency | 100 concurrent claims | P99 < 100ms cache-warm; SandboxClaim CAS under 50ms |
| concurrent_workspace_slots | 8 slots per pod, N pods | Per-slot isolation maintained, no cross-slot credential bleed |
| gateway_10k_sessions | 10000 sessions across replicas (cloud only) | No OOM, latency within SLO |
| postgres_write_burst | Quota-flush burst pattern at Tier 3 | Sustained IOPS within `postgres.writeCeilingIops`; burst within 3× ceiling |
| playground_revocation | 1000 active playground sessions; tenant-wide revoke | P99 propagation ≤ 500ms |
| audit_lock | 1000 audit-event writes/sec at single-tenant burst | `pg_advisory_xact_lock` P95 < 50ms |
| webhook_delivery | 1000 subscriptions, event burst | Delivery within retry budget; signature validates |

**Baselines.** Each scenario produces a JSON artifact with latency histograms, P50/P90/P99/P99.9, error rates, throughput, and resource usage. The harness compares against the prior baseline. A regression > 15% in any percentile, or any SLO miss, fails the run.

**Cadence.**
- Smoke (Kind, 60 seconds, low load): PR.
- Path-specific (cloud-small, 5–10 minutes per scenario): nightly per the spec's incremental load gates (Phase 6.5, 9.5, 11.5).
- Full Tier 2 and Tier 3 sampling: pre-release. Phase 13.5 and 14.5 baselines applied.

### 12.8 Tier 8 — Chaos

**Scope.** Resilience and recovery under failure.

**Tooling.** Local: `toxiproxy` for store latency and partition; `kubectl delete pod --force` and `chaos-mesh`-equivalent Go primitives for pod kills. Cloud: `chaos-mesh`.

**Scenarios.** Each runbook in `docs/runbooks/` implies at least one chaos scenario. The mapping is maintained in `tests/tier8_chaos/runbook-map.yaml`. The full list of runbooks is 56 entries (§spec/17.7 and `docs/runbooks/`); the chaos suite covers all of them, organized into groups:

- Store failures: Postgres failover, Postgres unavailable, Redis Sentinel failover, Redis cluster degraded, MinIO unavailable, MinIO replication lag, KMS unavailable, KMS key probe stale, dual-store unavailable, PgBouncer saturation.
- Component failures: gateway replica failure, controller leader-election disruption, admission webhook outage, cert-manager outage, DNS outage, token service outage, ephemeral-container-cred-guard outage.
- Lifecycle failures: pod kill during active session, node drain during MinIO outage, sandbox finalizer hang, runtime upgrade stuck, pool upgrade rollback during expanding phase.
- Network failures: gateway-to-pod partition, agent-to-LLM-provider partition, network-policy drift, cross-zone partition.
- Credential failures: emergency revocation during active session, rotation failure, deny-list propagation under Redis outage, credential pool exhaustion.
- Delegation failures: child crash mid-task, parent crash during await_children, budget exhaustion, lease-extension cool-off persistence across restart.
- Compliance failures: erasure job failure mid-sequence, legal-hold override flow, T3/T4 SLA breach, audit-chain gap detection.
- Concurrency: SandboxClaim race under 100+ goroutines, double-claim verification (ADR-007), elicitation deadlock detection, deadlock detection across delegation depth.
- Time: gateway clock drift, certificate expiry advance.
- Configuration: pool config drift, NetworkPolicy drift, schema migration failure (dirty flag), CRD upgrade with immutable field changes.

**Pattern.** Every chaos test follows the same shape:
1. Bring the system to a known-good state.
2. Inject the failure.
3. Assert the documented behavior: alert fires, fallback engages, audit trail records the event, SLO degraded annotation appears.
4. Resolve the failure.
5. Assert recovery: alert clears, system returns to healthy.
6. Assert no data loss (or assert the documented bounded loss).

**Cadence.** Core scenarios (Postgres failover, pod kill during session, sandbox-claim race, Redis Sentinel failover, MinIO outage during checkpoint): PR (Kind subset). Full suite: nightly. Severe scenarios that require cloud (cross-zone partition, KMS key revocation): nightly cloud or pre-release.

### 12.9 Tier 9 — Security

**Scope.** Security controls verified end-to-end, not merely configured.

**Suites.**

#### 12.9.1 Tenant isolation (cross-store)

A composed adversarial scenario: seed tenants A and B with rich state on every store, then for each store and each operation, attempt cross-tenant reads and writes through every code path (REST, MCP, OpenAI Completions, OpenAI Responses, admin API, MCP management server, audit query, drift detection, lenny-ops endpoints). Every attempt must fail with the documented isolation error.

#### 12.9.2 TLS enforcement

Plaintext connections to Postgres, PgBouncer, Redis, MinIO, OTLP, gateway-to-pod gRPC, gateway-to-token-service gRPC, gateway-to-lenny-ops are rejected. mTLS handshakes require both certificates. SPIFFE URIs match expected templates.

#### 12.9.3 Admission policy

Every documented admission rejection is verified:
- `POD_SPEC_HOST_SHARING_FORBIDDEN`: `shareProcessNamespace: true`, `hostPID: true`, `hostNetwork: true`, `hostIPC: true`.
- `POD_SPEC_CRED_FSGROUP_MISSING`: agent pod missing `fsGroup`.
- `POD_SPEC_CRED_GROUP_OVERBROAD`: non-adapter, non-agent container declaring `lenny-cred-readers`.
- `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`: ephemeral container with adapter or agent UID or `lenny-cred-readers` GID.
- `lenny-label-immutability`: post-creation label mutation, non-WarmPoolController creator setting `lenny.dev/managed: "true"`.
- `lenny-sandboxclaim-guard`: double claim attempt.
- `lenny-pool-config-validator`: pool-config violations (tiered-cap, grace-budget, checkpoint-barrier-ack-timeout).
- `lenny-direct-mode-isolation`: `tenancy.mode: multi` plus `deliveryMode: direct` + `isolationProfile: standard`, and `deliveryMode: proxy` + `spiffeBinding: disabled`.
- `lenny-drain-readiness`: pod eviction during MinIO outage.
- `lenny-data-residency-validator`: cross-region writes.
- `lenny-t4-node-isolation`: T4 pod scheduled to non-dedicated node.
- `lenny-crd-conversion`: storage-version migration safety.

Each webhook's HA contract is verified: 2 replicas, PDB minAvailable 1, failurePolicy Fail, `WebhookUnavailable` alert wiring.

#### 12.9.4 NetworkPolicy adversarial

Test pods attempt to reach forbidden endpoints: agent pod to internet, agent pod to Postgres, agent pod to Redis, agent pod to another tenant's namespace, ephemeral container to credentials, agent pod to cloud metadata service. Every attempt times out at the CNI layer.

#### 12.9.5 SSRF and callback validation

Callback URLs are tested with: HTTP URLs (rejected), IP literals (rejected), localhost (rejected), 169.254.169.254 (rejected), DNS-rebinding attacks (DNS pinning prevents the rebind), cloud metadata hostnames (blocklisted), private IPs after DNS resolution (rejected post-pin).

#### 12.9.6 Input fuzzing

OWASP ZAP runs against the REST and MCP surfaces with the project's policy. Oversize payloads, malformed JSON, SQL-injection strings, path-traversal in artifact keys, oversize headers, and deeply nested objects are all rejected with the appropriate error codes. The fuzz suite is gated to a fixed seed for reproducibility.

#### 12.9.7 RBAC

Every documented role's access is positively asserted. Every escalation attempt fails: tenant-admin cannot access platform-admin endpoints; user cannot access tenant-admin endpoints; viewer cannot create sessions.

#### 12.9.8 Credential leakage

Adversarial inspection of agent pods: process environment dumped, filesystem walked, `/proc/<pid>/environ` examined, network egress captured. No standing LLM-provider credentials are present. Credential files exist only in `0440` group-owned form. The `lenny-cred-readers` group membership is exactly the documented set.

#### 12.9.9 Elicitation content integrity

In `enforce` mode, a tampering intermediate pod modifying the elicitation payload is rejected with `ELICITATION_CONTENT_TAMPERED`. In `detect-only` mode, the tampered version is delivered, the audit event fires, and the `ElicitationContentIntegrityPermissiveTamper` alert is recorded. Platform floor enforcement (`max(platform_floor, tenant_stored_mode)`) is verified.

#### 12.9.10 Audit chain integrity

A direct database write that bypasses Lenny is followed by a chain-continuity check; the gap is detected and the alert fires. Sequence-number monotonicity is verified across a million events.

#### 12.9.11 Image signing and SBOM

Pre-release only. Production images are signed with cosign; trivy reports zero critical CVEs; SBOM is generated and stored.

**External pen-test.** Pre-release ships an artifact bundle to the external pen-test partner. The pen-test driver under `tests/tier9_security/pentest/` is the harness for replaying the partner's findings against future builds.

**Cadence.** Critical subset (12.9.1, 12.9.2, 12.9.3): PR. Full suite: nightly. External pen-test: per release.

### 12.10 Tier 10 — Conformance

**Scope.** Runtime adapter conformance to the published contracts.

**Tooling.** `cmd/lenny-compliance/` is the conformance harness. It is a standalone binary that takes a runtime image (or a local binary path) and a declared integration level, runs the test battery for that level, and produces a JSON report.

```bash
lenny-compliance --image ghcr.io/example/my-runtime:1.0 --level full --json
```

**Test battery by level.**

- Basic: every adapter binary protocol message; stdin/stdout JSON Lines; shutdown deadline; heartbeat ack; unknown message types.
- Standard: Basic plus MCP socket connection, nonce auth via adapter manifest, platform-tool discovery, capability inference at registration.
- Full: Standard plus lifecycle channel handshake, checkpoint flow with timeouts, interrupt flow, credential rotation, deadline notification, task lifecycle (task mode).

**Built-in vs. registered runtimes.**
- The three test runtimes (`echo`, `streaming-echo`, `delegation-echo`) run conformance on every PR.
- The nine reference runtimes (`claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, `crewai`) run conformance on every nightly.
- Third-party runtimes register themselves via the `RegisterAdapterUnderTest(adapter)` mechanism in the harness.

**Fidelity matrix.** A separate table-driven test asserts the documented OpenAI/Anthropic Completions/Responses translation fidelity. For each `OutputPart` type, the lossy fields are documented and the suite confirms the exact loss.

**Cadence.** Bundled runtimes: PR. Reference catalog: nightly. Third-party: invoked by `lenny-test conformance` per request.

### 12.11 Tier 11 — Documentation

**Scope.** Anything documentation-shaped under `docs/`, `spec/`, and the repository root.

**Checks.**
1. Markdown link integrity.
2. Code block syntax: every Go, Bash, YAML, JSON, and SQL block in docs parses or compiles.
3. Doc examples that emit configuration produce schema-valid configuration.
4. ADR catalog: every numbered ADR is present, no gaps, no renumbering, every "Planned" entry has a spec reference.
5. Runbook structure: every runbook has the documented step format and parseable metadata.
6. Spec section cross-references resolve.
7. Prose style: project rules under `.claude/rules/doc-style.md` and `.claude/rules/doc-diagram-style.md` are advisory but the linter reports violations. The PR template prompts for review.
8. Diagrams in `docs/assets/diagrams/` build (`qlmanage -t -s 2000` on macOS, equivalent on Linux) and the ASCII fallback is non-empty.
9. Onboarding docs (`README.md`, `CONTRIBUTING.md`, `docs/getting-started/`) execute end-to-end against a fresh clone.

**Cadence.** PR.

---

## 13. Build-and-test sequence

This section maps the spec's build sequence (§spec/18) to the test infrastructure that must be in place before each phase, and to the test groups that gate each phase. The sequence below is the order in which the test infrastructure itself is built. Application code from spec/18 follows in parallel, gated by the corresponding test groups.

The phases are deliberately fine-grained to match spec/18. They number through Phase 17b for parity.

### 13.0 Phase 0 — Bootstrap the infrastructure repo

**Test infrastructure to land.**
- `cmd/lenny-test/` skeleton: command parser, group selection, verdict producer, output formats.
- `tests/spec-map.json` and `tests/change-graph.json` seeded with the spec's table of contents and an initial change-graph entry per planned package.
- `tests/groups.yaml` with `pr`, `pr-fast`, `nightly`, `pre-release`, and per-phase placeholder groups.
- `scripts/setup-dev.sh`, `scripts/preflight.sh`, `scripts/setup-cluster.sh`.
- ADR-007 (SandboxClaim optimistic locking) authored. ADR-008 (license) committed. LICENSE file at repo root.
- `tests/testinfra/timectl/` and `tests/testinfra/randctl/`.
- Tier 0 (Static) is partially live: license header check, markdown link check, ADR catalog cross-check, basic golangci-lint.
- Tier 11 (Documentation) is partially live: link check, code-block syntax check.

**Test group gating Phase 0 → Phase 1.**
- `phase-0-gate`: Tier 0 and Tier 11 pass on the empty repo. `lenny-test validate-maps` passes (empty maps).

### 13.1 Phase 1 — Core types and wire contracts

**Spec/18 components.** Core types (`Runtime`, `SandboxTemplate`, etc.). Wire-contract artifacts under `schemas/` (`lenny-adapter.proto`, `lenny-adapter-jsonl.schema.json`, `outputpart.schema.json`, `workspaceplan-v1.json`).

**Test infrastructure to land.**
- Proto and JSON Schema compilation in Tier 0.
- `tests/tier3_contract/adapter_jsonl/` and `tests/tier3_contract/workspaceplan/` directories with stub failing tests for every documented contract.
- `tests/tier1_unit/` table-driven tests for every state machine introduced in Phase 1 (TaskRecord state, suspended session state, input_required state).
- buf-driven schema breaking-change detection.
- Spec map entries for spec §1, §2, §3, §14, §15.4, §15.5.

**Test group gating Phase 1 → Phase 1.5.**
- `phase-1-gate`: every Phase 1 contract test compiles and emits "not implemented" failures with diagnosis strings. Static tier passes.

### 13.2 Phase 1.5 — Database migration framework

**Spec/18 components.** Migration tool selected. Initial schema migration. Schema and query linters (R-01, R-02). CI migration gate.

**Test infrastructure to land.**
- `scripts/lint-schema.sh` and `scripts/lint-queries.sh` operational and wired into Tier 0.
- `tests/testinfra/containers/postgres.go` operational. testcontainers-go starts Postgres, applies migrations, returns a clean schema.
- `tests/tier2_component/migrations/` exists with migration round-trip tests, rollback tests, and idempotency tests.
- A migration regression test: apply N, seed data, apply N+1, assert data integrity, roll back, assert reversibility.

**Test group gating Phase 1.5 → Phase 2.**
- `phase-1.5-gate`: Phase 1.5 deliverables verified. Static linters pass. Tier 2 migration suite passes against the initial schema.

### 13.3 Phase 2 — Adapter protocol + `make run` + ImageResolver + startup benchmark

**Spec/18 components.** Adapter binary protocol against Phase 1 wire contracts. `make run` local dev mode. `ImageResolver`. Startup-latency benchmark. SQLite-dev-mode schema. Checkpoint-duration baseline (best-effort).

**Test infrastructure delivered in Phase 2.**
- `cmd/runtimes/echo/` built. The binary passes `lenny-compliance --level basic`.
- `cmd/lenny-compliance/` is the conformance harness. The Basic-level battery covers binary execution, empty-stdin shutdown, message-to-response round-trip, heartbeat acknowledgement, unknown-type forward compatibility, shutdown deadline, and sequential-message handling.
- `tests/tier3_contract/adapter_jsonl/messages_test.go` passes against the echo runtime for Basic-level expectations. Standard-level expectations (tool-call correlation, response-shorthand normalisation, adapter-local tool path-traversal guard) are skipped until the relevant runtimes and the gateway-side normaliser ship.
- `tests/tier7_load/scenarios/startup_latency/main.go` is the executable benchmark harness. It records the first baseline at `tests/tier7_load/baselines/startup_latency.json` (heartbeat round-trip P50=2 ms, P90=3 ms, P99=3 ms over 20 iterations on the development machine).
- `pkg/common/registry/resolver.go` implements `ImageResolver` with override > url > default precedence and digest enforcement. Tests in `pkg/common/registry/resolver_test.go`.

**Test infrastructure deferred to later phases.**
- `make run` end-to-end gateway smoke and the SQLite dev-mode schema are deferred to the gateway implementation phase. The harness for both requires a gateway binary that does not yet exist.
- `tests/tier2_component/translators/image_resolver_test.go` is deferred to the same phase. The component-tier shape requires a translator stack to drive the resolver in context; the Phase 2 deliverable validates the resolver in isolation via the unit tests above.
- The checkpoint-duration baseline is deferred to Phase 8 (checkpoint/resume).
- `pr-fast` `lenny-test --changed --max-tier component` end-to-end timing is deferred until Phase 1's component tier is wired into the default selectors.

**Test group gating Phase 2 → Phase 2.5.**
- `phase-2-gate`: static, unit, and contract tiers pass. `lenny-compliance --level basic` passes against `cmd/runtimes/echo`. Startup-latency baseline is captured and committed.

### 13.4 Phase 2.5 — Observability foundation + shared rule packages

**Spec/18 components.** Structured logging with correlation fields. OTel trace propagation. Shared `pkg/alerting/rules`.

**Test infrastructure delivered in Phase 2.5.**
- `pkg/observability/correlation` carries the correlation Fields (trace_id, span_id, session_id, task_id, tenant_id, request_id, operation_id, agent_name, component, runtime_class, pool) on a `context.Context` and across HTTP headers and gRPC metadata. Unit tests cover round-trip via headers, merge semantics, and the §25.4 agent-operability headers.
- `pkg/observability/logging` is the slog handler that auto-attaches the correlation fields to every JSON record and enforces the §16.4 required keys (ts RFC 3339 UTC, level, msg, component). Tests cover required-key emission, attribute projection, level gating, and `With()` preservation.
- `pkg/observability/tracing` wraps `go.opentelemetry.io/otel/trace` with the §16.3 span-name catalog (`session.create`, `delegation.spawn_child`, etc.) and the §16.3 error taxonomy (`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`). Tests cover attribute projection, error categorisation, and catalog hygiene.
- `pkg/observability/metrics` is the typed Counter/Gauge/Histogram constructor with the §16.1.1 label-hygiene validator. The validator rejects metric names without the `lenny_` prefix and rejects any of the forbidden high-cardinality labels (`session_id`, `operation_id`, `root_session_id`, `credential_id`, `user_id`, `lease_id`, `request_id`).
- `pkg/alerting/rules` is the typed `Rule` struct plus a PromQL validator (parsed via `prometheus/prometheus/promql/parser`) and a representative catalog (`WarmPoolExhausted`, `PostgresReplicationLagHigh`, `CredentialPoolLow`, `WarmPoolLow`, `CertExpiryImminent`). Tests cover validation, catalog hygiene, and the critical-severity-requires-runbook rule.
- `tests/testinfra/mocks/otelcollector` is the in-process tracer-provider helper. It returns spans recorded synchronously so unit and component tests can assert what was emitted.
- `tests/tier2_component/observability/integration_test.go` wires the correlation context, the slog handler, and the tracer wrapper together against the in-process collector and asserts that span attributes and log fields share the same correlation values.

**Test infrastructure deferred to later phases.**
- "Every component emits the documented log fields and trace spans" is deferred to the phases that ship those components. The gateway, controllers, runtime adapter, token service, and `lenny-ops` do not exist yet; their per-component log-and-span assertions land alongside each binary.
- The full §16.1 metrics catalog is deferred. Phase 2.5 ships the registration pattern (label validator plus typed constructors); each metric is registered as the feature that emits it lands.
- The full §16.5 alert catalog is deferred. Phase 2.5 ships the `Rule` type plus a representative subset; each alert is added as the underlying metric and feature ship together.
- The OTLP-over-gRPC receiver harness is deferred to the phase that ships the first OTLP-emitting binary (Phase 4 or later). The in-process collector in `tests/testinfra/mocks/otelcollector` satisfies the Phase 2.5 obligation for tests that exercise the tracer wrapper directly.
- `pkg/recommendations/rules` is deferred. Spec §16 does not define a recommendations package; the surface is implied by §10.7 (experiment primitives) and lands with that phase.

**Test group gating Phase 2.5 → Phase 2.8.**
- `phase-2.5-gate`: static, unit, and the observability component subset pass. Every observability primitive validates against the spec contracts above.

### 13.5 Phase 2.8 — `streaming-echo` runtime

**Test infrastructure delivered in Phase 2.8.**
- `cmd/runtimes/streaming-echo/` is the Full-level reference runtime. It inherits the Basic stdin/stdout protocol from `cmd/runtimes/echo` and adds a lifecycle-channel client over a Unix socket (abstract on Linux, file-based on macOS). The runtime handles `lifecycle_capabilities` / `lifecycle_support`, `checkpoint_request` / `checkpoint_ready`, `interrupt_request` / `interrupt_acknowledged`, `credentials_rotated`, `deadline_signal`, and `draining`.
- `schemas/lifecycle-events.schema.json` is the JSON Schema for the lifecycle-channel envelope. Example fixtures live under `schemas/examples/lifecycle.*.json` and are validated by `tests/tier0_static/schemas_test.go::TestLifecycleEventExamplesValidate`.
- `cmd/lenny-compliance --level full` adds the Full-level conformance battery (lifecycle handshake, checkpoint quiesce/resume, interrupt acknowledgement, credentials_rotated, deadline_signal). The harness opens a Unix-socket lifecycle server, writes a manifest pointing the runtime at it, and asserts each event round-trip.
- `streaming-echo` passes both `lenny-compliance --level basic` (7/7) and `lenny-compliance --level full` (12/12).

**Test infrastructure deferred to later phases.**
- The integration `streaming_reconnect` suite is deferred to the phase that ships the gateway. The runtime can be checkpointed and restarted, but the reconnect flow runs through the gateway's stream proxy.
- The Phase 2 checkpoint-duration baseline re-validation with cooperative quiescence overhead is deferred to Phase 8 (checkpoint/resume). The lifecycle channel handshake in Phase 2.8 demonstrates the quiescence protocol; the duration baseline requires the gateway's checkpoint manager.

### 13.6 Phase 3 — Pool scaling, delegation policy, runtime upgrade, mTLS

**Spec/18 components.** PoolScalingController. DelegationPolicy resource. RuntimeUpgrade state machine. mTLS PKI via cert-manager.

**Test infrastructure delivered in Phase 3.**
- `pkg/controller/poolscaling/strategy` ships the `PoolScalingStrategy` interface and the `DefaultStrategy` implementing the §4.6.2 formula (standard, mode-adjusted per §5.2, variant pool, adjusted base pool) plus the cold-start bootstrap fallback. Tests cover every worked-example variant and the `Σ variant_weights ≥ 1` rejection path.
- `pkg/runtime/upgrade/state` ships the §10.5 `RuntimeUpgrade` state machine (`Pending → Expanding → Draining → Contracting → Complete`, with `Paused` as a side-state captured per `Record`). The `Record` type enforces pause-and-resume invariants and rejects pausing a `Complete` record.
- `pkg/mtls/spiffe` parses both SPIFFE URI shapes from §10.3 (`spiffe://<trust-domain>/agent/{pool}/{pod-name}`, `spiffe://<trust-domain>/interceptor/{namespace}/{pod-name}`), enforces the no-userinfo / no-query / no-fragment constraints, and ships `ValidateAgent` and `ValidateInterceptor` for the NET-060 and NET-063 inbound-handshake checks.
- `pkg/mtls/denylist` ships the in-memory, TTL-evicting certificate deny list from §10.3 (certificate revocation). The implementation is goroutine-safe and exposes opportunistic eviction on read plus an explicit `Sweep`.

**Test infrastructure deferred to later phases.**
- `tests/testinfra/kind/cluster.go` (Kind cluster harness with cert-manager) is deferred. Bringing it up requires Docker, kind, cert-manager CRDs, and a controller image — none of which exist on this branch.
- `tests/tier2_component/controllers/poolscaling_test.go` against `envtest` is deferred. It requires the `envtest` binary plus the PoolScalingController binary; the strategy package above is the unit-testable substrate that test will drive.
- `tests/tier5_e2e_kind/pool_upgrade_test.go` exercises the runtime-upgrade state machine end-to-end against real CRDs and pods. It is deferred until the WarmPoolController and PoolScalingController binaries exist alongside the Kind harness.
- `tests/tier5_e2e_kind/mtls_enforcement_test.go` and `tests/tier9_security/mtls/` are deferred. The SPIFFE validation and deny-list units above are the substrate; the e2e tests need the gateway binary, cert-manager wiring, and a Kind cluster.
- The Redis pub/sub fan-out that propagates deny-list entries across gateway replicas is deferred to the phase that ships the gateway. The single-replica primitive is in place.

**Test group gating Phase 3 → Phase 3.5.**
- `phase-3-gate`: static and unit tiers pass over the new packages. The e2e Kind smoke and the mTLS enforcement test land alongside the K8s-integration phase.

### 13.7 Phase 3.5 — Admission policies + `lenny-ops` first deploy

**Spec/18 components.** Default-deny NetworkPolicy. gVisor RuntimeClass validation. Admission webhooks (`lenny-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, `lenny-crd-conversion`, `lenny-ephemeral-container-cred-guard`). PoolScalingController admission-denied integration test. mTLS end-to-end verification. Mandatory `lenny-ops` first deploy.

**Test infrastructure delivered in Phase 3.5.**
- `pkg/admission/sandboxclaim_guard` is the pure-decision logic for the `lenny-sandboxclaim-guard` webhook from §4.6.1 (ADR-007). CREATE rejects when a non-terminal `SandboxClaim` already exists for the same `Sandbox`; PATCH/PUT rejects when the referenced `Sandbox.status.phase` is not `claimed`. Rejection messages match the spec verbatim.
- `pkg/admission/label_immutability` is the pure-decision logic for the `lenny-label-immutability` webhook from §17.2 item 5 / §5.2 NET-003. The three immutable agent-pod labels (`lenny.dev/managed`, `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`) reject any mutation; `lenny.dev/tenant-id` allows `unset → {tenant_id}` only for the gateway ServiceAccount and `{tenant_id} → unassigned` only for the WarmPoolController ServiceAccount.
- `pkg/admission/ownership` encodes the §4.6.3 CRD field-ownership matrix as a Go-callable validator. The matrix covers `SandboxTemplate`, `SandboxWarmPool` (with the `status.sdkWarmCircuitBreaker.*` carve-out for the PoolScalingController), `Sandbox`, and `SandboxClaim`. `Validate(controller, crd, fieldPath)` reports cross-controller writes as `*OwnershipError`.

**Test infrastructure deferred to later phases.**
- The webhook binaries that wrap each decision package with a `k8s.io/api/admission/v1` AdmissionReview HTTP handler are deferred to the K8s-integration phase. The handlers are thin shims over the decision packages above; they need the controller-runtime stack and a cert-manager-issued serving certificate.
- `tests/tier5_e2e_kind/admission_inventory_test.go`, `admission_policy_test.go`, `sandbox_claim_test.go` (ADR-007 concurrent-claim chaos test), `label_immutability_test.go`, and `lenny_ops_first_deploy_test.go` are deferred. They need Kind, cert-manager, the webhook binaries, and the rendered Helm chart.
- `tests/tier8_chaos/admission_webhook_outage_test.go` (`failurePolicy: Fail` chaos) is deferred to the same phase.
- The `lenny-pool-config-validator` webhook is also deferred — its semantic rule set (the §10.1 tiered-cap vs `terminationGracePeriodSeconds` budget and the `checkpointBarrierAckTimeoutSeconds` floor) depends on §10.1 details that are out of scope for Phase 3.5.
- `lenny-ops` first-deploy and its NetworkPolicies are deferred. The lenny-ops binary does not exist yet; the §25.4 correlation primitive shipped in Phase 2.5 (`pkg/observability/correlation`) is the substrate it will use.

**Test group gating Phase 3.5 → Phase 4.**
- `phase-3.5-gate`: static and unit tiers pass over the new admission-decision packages. The e2e-Kind admission and chaos suites land alongside the K8s-integration phase.

### 13.8 Phase 4 — Session manager + REST

**Test infrastructure delivered in Phase 4.**
- `pkg/api/v1/session` ships the externally-visible Session API surface from §15.1: the twelve-state `State` enum, `IsTerminal`, the §7.1 `FailureClass` enum, and the §15.1 state-mutating endpoint precondition table as a Go-callable `Validate` function. The validator returns `*PreconditionError` whose `Code()` and `ErrorCode()` match the spec's HTTP 409 INVALID_STATE_TRANSITION envelope, including the `details.allowedStates` slice for the gateway to echo back to clients.

**Test infrastructure deferred to later phases.**
- `tests/tier4_integration/session_lifecycle_test.go` (`create → upload → attach → complete` against the compose stack) is deferred. It needs the gateway binary plus the docker-compose integration profile.
- The Tier 4 integration profile (`docker compose --profile default up` bringing up gateway, controller-sim, stores) is deferred to the K8s-integration phase. The fixtures it composes do not exist yet.
- `tests/tier2_component/gateway_subsystems/session_orchestrator_test.go` (orchestrator-in-isolation against an envtest API server) is deferred to the K8s-integration phase. The Go-callable precondition validator from Phase 4 is the substrate; the orchestrator binary that drives it lands with the gateway.
- The OpenAPI document served at `GET /openapi.yaml` and the per-endpoint request/response shapes (CreateSessionRequest, SessionResponse, etc.) are deferred. They require the gateway binary so the served spec reflects the binary's release version.

### 13.9 Phase 4.5 — Admin API foundation + authentication + bootstrap

**Spec/18 components.** Admin API for runtimes/pools/connectors/policies/tenants/users. Bootstrap seed (`lenny-ctl bootstrap`). OIDC/OAuth 2.1 JWT validation. `tenant_id` claim propagation. `UserStateStore` integration. `noEnvironmentPolicy` default (`deny-all`) validation.

**Test infrastructure delivered in Phase 4.5.**
- `pkg/auth` ships the §10.2 primitives that do not depend on a live OIDC provider or KMS: the `TokenType` enum (`user_bearer`, `session_capability`, `a2a_delegation`, `service_token`), the `Role` enum (`platform-admin`, `tenant-admin`, `tenant-viewer`, `billing-viewer`, `user`), `ValidateTenantID` for the `^[a-zA-Z0-9_-]{1,128}$` format constraint, and `ExtractTenant` for the §10.2 single-tenant / multi-tenant claim-extraction state machine. The function returns the spec-mandated rejection categories (`ErrTenantClaimMissing` → 401, `*TenantIDFormatError` → 401, `ErrTenantNotFound` → 403) so the gateway HTTP handler can map them to the §10.2 error codes.

**Test infrastructure deferred to later phases.**
- `tests/tier2_component/gateway_subsystems/admin_plane_test.go` (every admin endpoint against real stores) is deferred. The admin endpoints do not exist yet.
- `tests/tier2_component/auth/` (JWT signature validation, signing-key rotation, KMS / HMAC backends) is deferred. The pure primitive in `pkg/auth` is the substrate; the `JWTSigner` interface and its KMS implementation land with the gateway binary.
- `tests/tier4_integration/admin_bootstrap_test.go` (`lenny-ctl bootstrap --from-values` idempotency) is deferred. `lenny-ctl` does not yet implement the bootstrap subcommand.
- `tests/tier5_e2e_kind/bootstrap_first_install_test.go` is deferred to the K8s-integration phase.
- `tests/testinfra/mocks/oidc/` (a mock OIDC provider that mints tokens for the role matrix above) is deferred. The pure-Go primitives in `pkg/auth` can be exercised without it; the mock provider is needed only by the component-tier and integration-tier suites.

### 13.10 Phase 5 — ExternalAdapterRegistry + MCP/Completions/Open Responses + REST/MCP contract tests

**Test infrastructure to land.**
- `tests/tier3_contract/rest_mcp/` covers every overlapping operation per §12.3.1.
- `tests/tier3_contract/rest_openai_completions/` and `tests/tier3_contract/rest_openai_responses/` cover the documented fidelity matrices.
- `tests/tier2_component/translators/openai_translator_test.go` and `anthropic_translator_test.go` exercise the native translators.
- `tests/tier4_integration/mcp_runtime_endpoints_test.go` exercises `type: mcp` runtime endpoints.

### 13.11 Phase 5.4 — etcd encryption at rest

**Test infrastructure to land.**
- `tests/tier5_e2e_kind/etcd_encryption_test.go` writes a Kubernetes Secret, queries `etcdctl get` on the raw key, asserts ciphertext.
- The chart's `etcdEncryption.enabled: true` default is asserted in the chart-template unit tests.

**Hard prerequisite for Phase 5.5.** Without this test passing, Phase 5.5 must not proceed.

### 13.12 Phase 5.5 — Basic credential leasing + Token Service

**Test infrastructure delivered in Phase 5.5.**
- `pkg/credential` ships the §4.9 closed enums (`Provider`, `AssignmentStrategy`, `LeaseSource`, `RotationTrigger`) plus the rotation-trigger semantics: `IsFaultTriggered`, `IsCeilingApplicable` (the §4.7 300-second in-flight gate rule — only `proactive_renewal` escapes), and `CountsAgainstRotationBudget` (the §4.9 proactive-renewal exclusion from `maxRotationsPerSession`). `PoolConfig.Validate` rejects malformed admin-path `CredentialPool` definitions before the K8s/Postgres write.

**Test infrastructure deferred to later phases.**
- `tests/tier2_component/stores/token_store_test.go` (real Postgres + KMS stub) is deferred. It needs the Token Service binary and the schema migration to land.
- `tests/tier2_component/gateway_subsystems/token_service_test.go` (AssignCredentials, RevokeCredentials, multi-replica leader election, K8s Secrets backend) is deferred to the K8s-integration phase that ships the Token Service.
- `tests/tier4_integration/credential_lifecycle_test.go` (end-to-end credential lifecycle against the compose stack) is deferred to the same phase.
- The `CredentialRenewalWorker` proactive-renewal loop, the materializedConfig schemas per provider, the §4.9.1 KMS-key-rotation procedure, and the SIEM-streaming audit-event pipeline all need the gateway binary and the Token Service to land first.

### 13.13 Phase 5.6 — Targeted security design review (credential)

**Test infrastructure to land.**
- A documented checklist in `tests/tier9_security/reviews/credential-review.md`. The review itself is a human activity; the infrastructure records findings and links them to commits.

### 13.14 Phase 5.75 — Minimum viable policy enforcement

**Spec/18 components.** `AuthEvaluator` and `QuotaEvaluator` interceptors active.

**Test infrastructure delivered in Phase 5.75.**
- `pkg/quota` ships the §11.2 quota arithmetic primitives that the `QuotaEvaluator` interceptor wraps with Redis I/O: the `ResetPeriod` enum, the soft-warning (80%) and hard-limit (100%) threshold tests, the global→tenant→user `Hierarchy` validator and `HierarchicalCheck` resolver, the §11.2 `FailOpenCeiling` formula (`min(tenant_limit / replicas, per_replica_hard_cap)`), the `PerUserFailOpenCeiling` companion, the `MaxOvershoot` formula, and the `ReconcileMax` MAX-rule for Redis-recovery counter reconciliation.

**Test infrastructure deferred to later phases.**
- `tests/tier4_integration/policy_gate_test.go` (unauthenticated session-creation denial, per-tenant `maxConcurrentSessions` enforcement, token-budget rejection) is deferred. It needs the gateway, the `AuthEvaluator` and `QuotaEvaluator` interceptors, and Redis wired in.
- `tests/tier8_chaos/redis_down_during_policy_check.go` (fail-open / fail-closed behaviour under Redis outage) is deferred. The arithmetic primitives are in place; the chaos test needs the gateway and a Redis instance the chaos harness can disable.
- The `storage_quota_reserve.lua` Redis Lua script, the per-replica fail-open accounting goroutine, the cumulative fail-open timer (`quotaFailOpenCumulativeMaxSeconds`), and the §11.2.1 billing event emitter are all deferred to the K8s-integration phase.

**Hard prerequisite for Phase 6 real-credential testing.**

### 13.15 Phase 5.8 — LLM Proxy + `lenny-direct-mode-isolation` admission webhook

**Test infrastructure to land.**
- `tests/tier2_component/gateway_subsystems/llm_proxy_test.go` validates lease-token validation, native translator for `anthropic_direct`, SSE relay, circuit breaker.
- `tests/tier3_contract/llm_proxy/` validates request and response shapes against the Anthropic mock and OpenAI mock.
- `tests/tier5_e2e_kind/llm_proxy_proxy_mode_test.go` exercises proxy-mode end-to-end with `streaming-echo`.
- `tests/tier5_e2e_kind/admission_direct_mode_isolation_test.go` validates the new webhook.

### 13.16 Phase 6 — Interactive sessions + SDKs

**Test infrastructure to land.**
- `tests/tier4_integration/streaming_reconnect_test.go` covers reconnect with cursor under controlled disconnects.
- The full language-SDK test surface comes online per §14.13. Phase 6 ships the Go and TypeScript client SDKs (per spec/15.6); Python ships with Phase 6 or 6.5 depending on release packaging. Runtime-author SDKs (Go, Python, TypeScript) ship alongside the runtime scaffolder (Phase 2 onward, with full coverage by Phase 12b).

### 13.17 Phase 6.5 — Incremental load test (streaming)

**Test infrastructure to land.**
- `tests/tier7_load/scenarios/streaming_reconnect.go` operational.
- Phase 6.5 baseline JSON committed.

### 13.18 Phase 7 — Policy engine (quotas, budgets, audit hooks)

**Test infrastructure delivered in Phase 7.**
- `pkg/circuitbreaker` ships the §11.6 operator-managed circuit-breaker admission logic. The `State` enum (open, closed), `LimitTier` enum (runtime, pool, connector, operation_type), `OperationType` enum (uploads, delegation_depth, session_creation, message_injection), and per-tier `Scope` validator capture every shape the §11.6 admin POST /open body can take. The `Match` function implements the §11.6 AdmissionController evaluation rule: a breaker keys exclusively on its (`LimitTier`, `Scope`) pair, never on its `{name}`. `FirstMatch` picks the first open match in cache order; `ScopeMatches` enforces the §11.6 immutable-scope invariant when an operator reopens a previously-existing breaker.
- `pkg/idempotency` ships the §11.5 idempotency primitives: 128-character `MaxKeyLength` cap, 24-hour `TTL` constant, tenant-scoped `Key` shape with `Validate` returning `*KeyTooLongError` on length overflow, `HashBody` returning a stable 64-character SHA-256 hex digest, and `DetectReuse` returning the canonical action (`store_new` | `replay` | `reject`) with `*ReuseError` carrying the §11.5 `IDEMPOTENCY_KEY_REUSED` 422 envelope.

**Test infrastructure deferred to later phases.**
- `tests/tier4_integration/quota_enforcement_test.go` and `quota_recovery_test.go` are deferred. The Phase 5.75 `pkg/quota` arithmetic is the substrate; integration tests need the gateway, Redis, and the storage_quota_reserve.lua script.
- `tests/tier4_integration/policy_audit_test.go` is deferred to the K8s-integration phase that ships the gateway's policy interceptors and the §11.7 audit-log writer.
- The §11.6 Redis-backed cache with pub/sub propagation, the admin API handlers, the 5-second cache-stale fallback path, and the §11.6 sampling-under-breaker-storms audit emitter are deferred to the K8s-integration phase.

### 13.19 Phase 8 — Checkpoint/resume + drain-readiness webhook

**Test infrastructure delivered in Phase 8.**
- `pkg/checkpoint` ships the §4.4 checkpoint primitives: the `Level` enum (basic, standard, full, embedded) with the `ConsistencyForLevel` mapping (basic/standard → best-effort; full/embedded → consistent), the `Trigger` enum (periodic, pre_scale_down, eviction) with `IsEviction` and `RetryBudgetFor` returning the §4.4 per-trigger budget (200ms/5s/5s for non-eviction; 500ms/5s/30s for eviction), the `Outcome` enum (success, failed, aborted, partial), the `ResumeMode` enum (full, partial_workspace, conversation_only, coordinator_handoff) with `WorkspaceLost` mapping, the `CheckpointTimeout` 60-second constant, the `WorkspaceSizePreCheck` implementing the §4.4 "Hard workspace size limit" pre-check, and the `FreshnessCheck` implementing the §4.4 "Checkpoint freshness SLO" with the §16.5 `CheckpointStale` alert condition.

**Test infrastructure deferred to later phases.**
- `tests/tier4_integration/checkpoint_resume_test.go` (eviction → checkpoint → resume on a new pod) is deferred. It needs the gateway, the adapter, MinIO, and the compose stack.
- `tests/tier5_e2e_kind/drain_readiness_webhook_test.go` is deferred. The `lenny-drain-readiness` admission-decision logic that the webhook calls into is also deferred (Phase 3.5 covered the three webhooks whose decision logic was clearly bounded; drain-readiness depends on a MinIO health probe that does not exist yet).
- `tests/tier8_chaos/minio_outage_during_checkpoint_test.go` and `tests/tier8_chaos/node_drain_during_minio_outage_test.go` (chaos tests) are deferred to the same phase that brings up the MinIO-bearing compose stack.
- The SIGSTOP/SIGCONT embedded-adapter path and the §4.4 eviction-fallback Postgres minimal-state record are deferred to the K8s-integration phase. The pure primitives are in place.

### 13.20 Phase 9 — Delegation + `delegation-echo`

**Test infrastructure delivered in Phase 9.**
- `pkg/delegation/cycle` ships the §8.2 cycle-detection decision matrix: the `Mode` enum (`enforce`, `warn`, `permissive`), the `Layer` enum (`platform`, `runtime`, `policy`), the `Identity` (runtime, pool) tuple with pool-differentiated equality, the `Lineage` chain with `Contains`/`Depth`, and the `Decide` function implementing the three-layer AND gate. Decisions carry `BlockedBy` plus the full `WouldHaveBlockedLayers` set for the §16 metric and audit emission. `ToError` produces the `*Rejection` typed error the gateway returns as `DELEGATION_CYCLE_DETECTED`.
- `pkg/delegation/lease` ships the §8.2 `LeaseSlice` arithmetic (`ValidateChildSlice` rejecting any axis that exceeds the parent's remaining budget) and the §8.2.bis maxDepth precedence chain (`ResolveMaxDepth` applies the explicit → preset → runtime → policy → Helm fallback order; `EnforcePolicyCeiling` caps the resolved value at the policy ceiling; `CheckDepth` enforces the next-hop bound).

**Test infrastructure deferred to later phases.**
- `cmd/runtimes/delegation-echo/` (Standard-level reference runtime that exercises `lenny/delegate_task` through the platform MCP server) is deferred. The Standard-level conformance battery in `cmd/lenny-compliance` is also deferred to the same phase; the cycle and lease packages above are the pure substrate.
- `tests/tier4_integration/delegation_test.go` is deferred to the K8s-integration phase that ships the gateway's `lenny/delegate_task` handler and the Redis-backed delegation budget counters (`budget_reserve.lua`, `budget_return.lua`).
- `tests/tier7_load/scenarios/delegation_fanout.go` (Phase 9.5 incremental load test) is deferred to the gateway-bearing phase.
- `tests/tier9_security/reviews/delegation-review.md` (the §9.1 security review) is a human activity, recorded by the documentation-only deliverable in Phase 17a.

### 13.21 Phase 9.5 — Incremental load test (delegation)

**Test infrastructure to land.**
- `tests/tier7_load/scenarios/delegation_fanout.go` operational.
- Phase 9.5 baseline JSON committed.

### 13.22 Phase 10 — MCP fabric (virtual child interfaces, elicitation chain)

**Test infrastructure to land.**
- `tests/tier4_integration/mcp_elicitation_chain_test.go`.
- `tests/tier4_integration/mcp_provenance_test.go` covers ElicitationProvenance fields, URL-mode elicitation, depth-based suppression.
- `tests/tier2_component/mcp/integrity_test.go` covers SHA-256 canonicalization across modes.

### 13.23 Phase 11 — Advanced credentials + multi-provider translators + revocation

**Test infrastructure to land.**
- `tests/tier2_component/translators/bedrock_translator_test.go`, `vertex_translator_test.go`, `azure_translator_test.go`.
- `tests/tier4_integration/credential_rotation_test.go`, `credential_revocation_test.go`.
- `tests/testinfra/mocks/llm-provider/` extended to handle all four upstreams.

### 13.24 Phase 11.5 — Incremental load test (credential lifecycle)

**Test infrastructure to land.**
- `tests/tier7_load/scenarios/credential_rotation_under_load.go` operational.
- Phase 11.5 baseline JSON committed.

### 13.25 Phase 12a — Token Service hardening (KMS envelope + OAuth)

**Test infrastructure to land.**
- `tests/tier2_component/stores/token_store_kms_test.go` validates KMS envelope encryption.
- `tests/tier4_integration/oauth_connector_test.go` covers the full OAuth flow including state, PKCE, and storage.

### 13.26 Phase 12b — `type: mcp` runtime support

**Test infrastructure to land.**
- `tests/tier4_integration/mcp_runtime_lifecycle_test.go`.
- A reference `type: mcp` runtime under `cmd/runtimes/mcp-reference/` (or via one of the bundled connectors).

### 13.27 Phase 12c — Concurrent execution modes

**Test infrastructure to land.**
- `tests/tier4_integration/concurrent_workspace_test.go` and `concurrent_stateless_test.go`.
- `tests/tier5_e2e_kind/concurrent_modes_test.go` confirms admission webhooks and pod-level isolation hold.

### 13.28 Phase 13 — Full observability + audit + `lenny-backup` + compliance webhooks

**Test infrastructure delivered in Phase 13.**
- `pkg/audit` ships the §11.7 audit-log enums: `ChainIntegrity` (verified, broken, unchecked, rechained_post_outage, gap_suspected, redacted_gdpr) with `IsAlarming` mapping to the §16.5 `AuditChainGap` alert; `ComplianceProfile` (none, soc2, fedramp, hipaa) with `RequiresSIEM` mapping; `RetentionPreset` (soc2, fedramp-high, hipaa, nis2-dora, custom) with `PresetDays`, `PresetWindow`, `CompatibleProfiles`, and `ValidatePairing`; `OCSFTranslationState` (pending, retry_pending, succeeded, dead_lettered) with `IsTerminal` mapping.

**Test infrastructure deferred to later phases.**
- `tests/tier2_component/stores/event_store_test.go` (extended for hash-chain verifier, OCSF translator, EventBus publish state, retranscribe worker) is deferred. It needs the gateway's EventStore writer and the OCSF translator binary.
- `tests/tier4_integration/audit_pipeline_test.go` (end-to-end including the SIEM mock) is deferred. It needs the gateway, Postgres, and the §11.7 per-tenant advisory lock implementation.
- `tests/tier5_e2e_kind/admission_data_residency_test.go` and `tests/tier5_e2e_kind/admission_t4_node_isolation_test.go` are deferred to the K8s-integration phase that ships the corresponding admission webhooks.
- `tests/tier5_e2e_kind/backup_restore_test.go` (§25.11 API end-to-end against Postgres and MinIO) is deferred to the K8s-integration phase that ships `lenny-backup`.

### 13.29 Phase 13.5 — Pre-hardening full-system load baseline

**Test infrastructure to land.**
- Tier 7 cloud scenarios fully operational.
- Phase 13.5 baseline JSON committed.
- The Lenny-specific Postgres write-pattern benchmark is implemented and `postgres.writeCeilingIops` is calibrated.
- The `PostgresWriteBurstIops` alert threshold is calibrated.

### 13.30 Phase 14 — Comprehensive security hardening

**Test infrastructure delivered in Phase 14.**
- `pkg/podsecurity` ships the §13.1 pod-spec validator: rejects every host-sharing flag (`shareProcessNamespace`, `hostPID`, `hostNetwork`, `hostIPC` → `POD_SPEC_HOST_SHARING_FORBIDDEN`), requires pod-level `fsGroup = lenny-cred-readers GID` (`POD_SPEC_CRED_FSGROUP_MISSING`) and `runAsNonRoot = true`, and enforces per-container `allowPrivilegeEscalation = false`, `privileged = false`, `readOnlyRootFilesystem = true`, `capabilities.drop = [ALL]`, and `capabilities.add = []`. `PodSecurityError.HasViolation` lets callers assert which §13.1 rejection code fired.

**Test infrastructure deferred to later phases.**
- The Kubernetes admission webhook that wraps `pkg/podsecurity.ValidateAgentPod` is deferred to the K8s-integration phase that ships the webhook server + cert-manager-issued serving certificate.
- The `lenny-preflight` startup hard-fail is deferred to the same phase; it imports this package and stops the install when any Lenny-managed Deployment / DaemonSet / Job pod template fails the validator.
- `tests/tier9_security/image_signing_test.go` (cosign verification at admission) is deferred; the §13.1 validator above is the orthogonal posture control.
- `tests/tier9_security/network_policy/` (CIDR drift, default-deny, gateway-egress allowlist enforcement) is deferred to the K8s-integration phase.
- seccomp profile assertions and the external pen-test driver under `tests/tier9_security/pentest/` are deferred to the same phase.

### 13.31 Phase 14.5 — Post-hardening SLO re-validation

**Test infrastructure to land.**
- Re-run of every Phase 13.5 scenario with full security hardening.
- Delta documentation in `tests/tier7_load/baselines/`.

### 13.32 Phase 15 — Environment resource + RBAC + cross-environment delegation

**Test infrastructure delivered in Phase 15.**
- `pkg/environment` ships the §10.6 Environment primitives: the `Role` enum (viewer, creator, operator, admin) with `AtLeast` enforcing the escalation order; the K8s-style `Selector` evaluator with `MatchLabels`, `MatchExpressions` (operators `In`, `NotIn`, `Exists`, `DoesNotExist`), the §10.6 `Types` filter, and the `Include`/`Exclude` overrides (exclude beats include for deterministic deny precedence); `Requirement.Validate` rejects malformed operators and value lists; `Filter` returns the admitted subset for the transparent-filtering path.

**Test infrastructure deferred to later phases.**
- `tests/tier4_integration/environment_resource_test.go` (full admin API surface for creating, listing, updating, deleting environments) is deferred to the K8s-integration phase that ships the admin API.
- `tests/tier5_e2e_kind/cross_environment_delegation_test.go` (`crossEnvironmentDelegation: true` flow) is deferred. The selector primitives above are the substrate; the delegation policy resolver and the explicit-environment endpoint dispatcher land with the gateway.
- `tests/tier9_security/rbac/environment_rbac_test.go` (OIDC-group resolution against the §10.6 `members` list) is deferred to the same phase.
- The transparent-filtering middleware that wraps `Selector.Filter` and joins authorized runtimes across the user's environments is deferred to the gateway phase.

### 13.33 Phase 16 — Experiments + PoolScalingController integration

**Test infrastructure delivered in Phase 16.**
- `pkg/experiment` ships the §10.7 experiment primitives: `Status` (active, paused, concluded) with `IsRoutable`, `TargetingMode` (percentage, external), `Sticky` (user, session, none), `Propagation` (inherit, control, independent). `Definition.Validate` enforces the admin-API admission rules — every variant id is unique, none equals the reserved `ControlVariantID`, each weight is in `(0, 1)`, and `Σ weights < 1` so a control group always exists. `AssignVariant` implements the §10.7 HMAC-SHA256 percentage-mode bucketing with the §10.7 anonymous-session rule (empty key → control). Tests cover determinism, the ±2% weight distribution over 10k trials, definition-order sensitivity, and experiment-id independence.

**Test infrastructure deferred to later phases.**
- `tests/tier4_integration/experiment_routing_test.go` is deferred. It needs the gateway, the ExperimentRouter interceptor wiring, and the PoolScalingController's variant-pool `minWarm` coupling.
- `tests/tier7_load/scenarios/experiment_active_under_load.go` is deferred to the K8s-integration phase that ships the gateway and the load harness.
- The OpenFeature / OFREP external-targeting integration, the §10.7 PSC variant-pool sizing path, and the §10.7 admin API surface (`POST /v1/admin/experiments`, etc.) are deferred to the same phase.

### 13.34 Phase 16.5 — Experiment load test SLO re-validation

**Test infrastructure to land.**
- Phase 16.5 baseline JSON committed.

### 13.35 Phase 17a — Documentation + governance + community launch

**Test infrastructure to land.**
- Tier 11 fully exercised: every doc page builds, every link resolves, the playground and `lenny up` quick-starts run end-to-end as advertised.
- `time-to-hello-world` benchmark under `tests/tier7_load/scenarios/tthw.go` confirms the < 5-minute target.

### 13.36 Phase 17b — Memory, semantic caching, eval hooks

**Test infrastructure to land.**
- `tests/tier2_component/stores/memory_store_test.go` extended for custom backends and the preflight contract.
- `tests/tier2_component/stores/semantic_cache_test.go`.
- `tests/tier4_integration/eval_hooks_test.go`.

---

## 14. Domain-specific test suites

This section enumerates the suites that cut across multiple tiers and that the table above touches on only briefly.

### 14.1 RLS and tenant isolation

The single most important security suite. Every store, every query path, every API surface, every adapter is validated for cross-tenant isolation. The Tier 2 RLS suite is the foundation; the Tier 9 cross-store suite is the adversarial overlay.

Key tests:
- Every query through PgBouncer is preceded by `SET LOCAL app.current_tenant`. Queries without it raise via the `lenny_tenant_guard` trigger.
- Transaction-mode pooling does not leak `app.current_tenant` across connection reuse.
- The cloud-managed-pooler fallback (sentinel value `__unset__`) is exercised.
- The schema linter flags every tenant-scoped table missing `tenant_id` as a leading index column.
- The query linter flags every cross-tenant JOIN missing the `a.tenant_id = b.tenant_id` clause.
- Platform-admin cross-tenant paths (`app.current_tenant = '__all__'`) emit `cross_tenant_read` audit events on every access.

### 14.2 Workspace plan and source handling

`tests/tier2_component/workspace_plan/` and `tests/tier3_contract/workspaceplan/` together cover the workspace plan schema. Every source type (`inlineFile`, `uploadFile`, `uploadArchive`, `mkdir`, `gitClone`) has positive and adversarial cases:
- Path traversal (`../../etc/passwd`) is rejected.
- Setuid/setgid bits are rejected on every type.
- Sticky on file is rejected; sticky on directory is allowed.
- Archive bomb: high compression ratio, deeply nested, large file count, exceeding archive limits.
- gitClone with HTTPS-only protocol; SSH and git:// rejected at schema validation.
- gitClone ref resolution: transient failure returns 503 retryable, auth/ref failure returns 422 not-retryable.
- gitClone resolvedCommitSha is gateway-written; client-supplied value rejected.
- Env-var blocklist matches exact names and glob patterns.
- callbackUrl: HTTPS only, DNS pinning, RFC1918 rejection, metadata blocklist.

### 14.3 Credential leasing lifecycle

End-to-end tests across Tier 2 (TokenStore, CredentialPoolStore, Token Service component), Tier 4 (compose integration with `delegation-echo` and `streaming-echo`), and Tier 5 (Kind e2e with real cert-manager and KMS stub).

Coverage:
- `AssignCredentials` writes a per-session lease atomically across all providers in `supportedProviders ∩ providerPools`.
- Lease expires per TTL, RenewBefore triggers refresh.
- Hot rotation via `credentials_rotated`/`credentials_acknowledged`.
- Emergency revocation propagates via Redis pub/sub; active leases terminated within the documented SLO; `streaming-echo` mid-stream sees the revocation.
- Fallback chain activates per `credentialPolicy`; primary cooldown respected.
- User-scoped credentials registered via `POST /v1/credentials`; resolution at session creation; preference logic for `preferredSource: user`.
- Credential never on the agent pod's disk in proxy mode; never in environment variables; not present in `/proc/<pid>/environ` for the agent.

### 14.4 Delegation and the task tree

Coverage spans Tier 1 (policy evaluation, budget arithmetic), Tier 4 (`delegation-echo` flows), Tier 5 (e2e), and Tier 8 (chaos).

Notable cases:
- Token budget carving: parent 100K, child 30K, parent remaining 70K. Child cannot grant more than 30K to its own children.
- Isolation monotonicity: parent sandboxed, child standard rejected.
- Scope narrowing: parent's connector set is the upper bound.
- maxDepth and maxTreeSize and maxChildrenTotal and maxParallelChildren enforced and reported with the documented error codes.
- File export: glob within `/workspace/current`; destPrefix relative; archive validation on each entry; size limit; file count limit; PreExportMaterialization interceptor.
- Lease extension: auto mode independent grants; elicitation mode serialized; cool-off persisted across gateway restart; rejection extension-denied flag persisted; in-flight requests check flag within transaction.
- TaskRecord schema versioning: envelope additive-only; concurrent writers at different schema versions; old readers can pass through unknown fields per forward-read rule.
- Self-recursion: `allowSelfRecursion: false` rejects repeating (runtime_name, pool_name). `true` allows one repeat; second rejected by cycle detector.
- Delegation tree recovery: parent loses connection; reconnect restores tree from Postgres.

### 14.5 MCP elicitation chain

Coverage spans Tier 2 (integrity), Tier 4 (chain flow), Tier 5 (provenance display, depth suppression), Tier 9 (tamper detection).

Notable cases:
- Hop-by-hop chain: External Tool → Gateway connector → Child → Parent → Gateway edge → Client.
- SHA-256 integrity: enforce mode rejects tampering with `ELICITATION_CONTENT_TAMPERED`; detect-only mode delivers tampered version but raises `ElicitationContentIntegrityPermissiveTamper`.
- Platform floor enforcement: `max(platform_floor, tenant_stored_mode)`. Floor change emits clamp event per affected tenant.
- URL-mode elicitation: agent-initiated blocked by default; domainAllowlist required when enabled; connector expected_domain hard boundary; initiator_type field in provenance.
- Depth-based suppression: agent-initiated auto-suppressed at depth ≥ 3 unless allowlisted.
- Timeouts: maxElicitationWait (600s default) separate from maxIdleTime; per-hop forwarding timeout 30s.
- Authorization: `respond_to_elicitation` validates (session_id, user_id, elicitation_id) triple exactly; mismatch returns 404 to avoid existence leak.

### 14.6 Operability surface

Coverage spans Tier 2 (each `lenny-ops` subsystem), Tier 4 (compose-level end-to-end), Tier 5 (Kind with the full chart), Tier 9 (RBAC and SSRF on webhook callbacks).

Notable cases:
- `GET /v1/admin/health` reports healthy/degraded/unhealthy from in-process metrics with fallback when Prometheus is down.
- `GET /v1/admin/recommendations` returns recommendations from sliding-window aggregation.
- `GET /v1/admin/events/buffer` returns events with monotonic cursor; gap detection works; eventKey deduplication across replicas.
- `GET /v1/admin/audit-events` scatter-gathers across shards with per-shard and aggregate timeouts.
- `POST /v1/admin/diagnostics/run?fix=true` applies idempotent auto-remediations.
- `POST /v1/admin/restore/execute` requires both `confirm: true` and `acknowledgeDataLoss: true`.
- Webhook subscriptions validate the callback URL (SSRF mitigations) and sign deliveries with HMAC-SHA256.
- The MCP management server at `/mcp/management` exposes every admin operation as a tool; scope tokens narrow access.

### 14.7 Multi-protocol gateway

Coverage in Tier 3 contract is the canonical layer. Each surface (REST, MCP, OpenAI Completions, OpenAI Responses) is validated for: success-path equivalence, error-code equivalence, pagination semantics, streaming envelope semantics, idempotency-key handling, role-based access.

### 14.8 The 13-phase request interceptor chain

The gateway's interceptor chain is exercised at Tier 2 (per-interceptor) and Tier 4 (composed). Coverage:
- Phase order: PreRoute, PreAuth, PreDelegation, PreUpload, PreExportMaterialization, PreWorkspacePlan, lifecycle pre/post.
- Priority sorting within phase.
- failPolicy: fail vs. warn.
- ExperimentRouter at PreRoute priority 300.
- Interceptor failure propagation.

### 14.9 Pool and warm-pod lifecycle

Coverage in Tier 5 e2e. Includes pod-warm vs. SDK-warm states, DemoteSDK on upload matching `sdkWarmBlockingPaths`, session mode strict one-session-only, task mode workspace scrub, concurrent workspace mode per-slot directories, projected SA token mount audience.

### 14.10 Compliance and erasure

Coverage spans Tier 2 (every store's DeleteByUser/DeleteByTenant), Tier 4 (erasure_job orchestrator running the 19-step sequence in order), and Tier 5 (legal-hold override flow).

Notable cases:
- T3 SLA: erasure completes within 72 hours.
- T4 SLA: erasure completes within 1 hour.
- Legal hold blocks both TTL deletion and rotation deletion.
- Processing-restricted flag set on user during erasure; database trigger prevents clear while active job exists.
- Audit events `gdpr.erasure_*` retained per audit retention.
- MemoryStore startup preflight detects no-op stub backends and refuses to start the gateway.
- Per-job preflight runs before step 8 (MemoryStore deletion).

### 14.11 Data residency and T4 controls

Coverage:
- `lenny-data-residency-validator` admission webhook rejects cross-region writes.
- `lenny-t4-node-isolation` admission webhook rejects T4 pods scheduled to non-dedicated nodes.
- T4 KMS probe (`t4KmsProbeInterval`, default 300s) fires `T4KmsKeyUnusable` alert on stale timestamp.
- T4 artifact writes fail closed with `CLASSIFICATION_CONTROL_VIOLATION` on key unavailability.
- Tenant deletion legal-hold override re-encrypts held evidence under `legal_hold_escrow_kek` and migrates to region-scoped escrow bucket.

### 14.12 Web playground

Coverage spans Tier 5 e2e (full chart with `playground.enabled: true`) and Tier 9 (security).

Notable cases:
- Three auth modes: OIDC, API Key, Dev. Dev mode requires `global.devMode: true` and synthetic principal.
- Bearer token in sessionStorage only (not localStorage).
- Cross-replica revocation propagation P99 ≤ 500ms.
- CSP: `default-src 'self'`, `script-src 'self'`, `frame-ancestors 'none'`.
- HttpOnly cookie on session; Secure; SameSite=Strict.
- Audit events: `playground.bearer_minted`, `playground.bearer_revoked`.
- `lenny_playground_session_revocation_propagation_seconds` histogram captured.

### 14.13 Language SDKs

Lenny publishes two SDK families across three languages (spec/15.6 and spec/15.7):

| Family | Audience | Languages | What it wraps |
|:-------|:---------|:----------|:--------------|
| Client SDK | External applications that talk to a Lenny gateway | Go, Python, TypeScript | REST + MCP + streaming + webhook signature verification |
| Runtime-author SDK | Authors of agent binaries running on Lenny | Go, Python, TypeScript | Adapter binary protocol + MCP platform tools + lifecycle channel |

Both families ship under `sdks/<family>/<language>/`. The harness runs each through its language-native test runner (`go test`, `pytest`, `vitest`) and includes results in the Tier 3 contract verdict and the Tier 10 conformance verdict.

**What gets tested per SDK.**

*Client SDKs* (`sdks/client/{go,python,typescript}/`):

1. Wire-format round-trip. Every documented REST operation in the OpenAPI spec, every documented MCP tool, exercised through the SDK against a live gateway (`compose` profile in CI). Request and response shapes match the published schemas byte-for-byte modulo language-idiomatic field naming.
2. Streaming and reconnect. SSE for REST log streams and message streams; MCP Streamable HTTP for sessions. Disconnect mid-stream and reconnect with `Last-Event-ID`; assert zero gaps and zero duplicates. Idle timeouts produce the documented error.
3. File upload. Multipart upload, tar/tar.gz/zip archive upload via `uploadArchive`, gitClone source materialization. Progress callbacks fire monotonically. Resumable upload (if supported by the language client) survives a connection drop.
4. Webhook signature verification. HMAC-SHA256 against `X-Lenny-Signature` with the `t=<unix>,v1=<hex>` format and the 5-minute replay window. Helper exposed for receiving applications.
5. Authentication. OIDC bearer, service-account token, API key (per the chosen mode). Token refresh before expiry. Multi-tenant context (`tenant_id` claim) honored.
6. Idempotency-key handling. Same key produces the same response; expired key produces a fresh response.
7. Error envelope. `{code, category, message, retryable, docs_url}` parsed into typed errors. Retryable errors retry with exponential backoff and jitter; non-retryable errors do not.
8. Pagination cursors. Every paginated endpoint exercised end-to-end through cursor iteration helpers.
9. MCP client. Tool discovery, `lenny/delegate_task` invocation, `lenny/send_message`, `lenny/request_input`, `lenny/await_children`, elicitation response.
10. Language-native ergonomics. Go: context cancellation propagates; channels for streams. Python: `async/await` and synchronous variants; type hints validated by mypy. TypeScript: full TS types generated from OpenAPI; ESM and CJS builds.
11. Compatibility matrix. Each SDK validated against the two most recent minor gateway versions (forward and backward compatibility within the documented support window per spec/15.5).

*Runtime-author SDKs* (`sdks/runtime/{go,python,typescript}/`):

1. Adapter binary protocol compliance. Every Basic-level message type. The SDK skeleton (the equivalent of `lenny runtime init --language <lang> --template minimal`) passes `lenny-compliance --level basic`.
2. MCP socket integration. Standard-level: connect to `@lenny`, read `_lennyNonce` from the manifest, invoke platform tools. Compliance against `--level standard`.
3. Lifecycle channel. Full-level: connect to `@lenny-lifecycle`, capability handshake, checkpoint flow, interrupt flow, credential rotation, deadline notification. Compliance against `--level full`.
4. Workspace helpers. `read_file`, `write_file`, `list_dir`, `delete_file` confined to `/workspace/current` and `/workspace/output`. Path traversal blocked.
5. Delegation tools. `lenny/delegate_task` invoked through the SDK; budget metadata propagates; child results awaited and parsed.
6. Heartbeat handling. Automatic `heartbeat_ack` within 10 seconds without runtime-author intervention.
7. Graceful shutdown. Shutdown deadline honored; SDK exits cleanly.
8. Telemetry pass-through. `tracingContext` set via `lenny/set_tracing_context`; OTel context flows into the runtime's own tracer where the runtime author opts in.
9. Quick-start TTHW. `lenny runtime init my-agent --language <lang> --template <minimal|coding|chat>` produces a runnable image within the documented five-minute target. The CI test mirrors the runtime author guide step-by-step.

**Code generation.** The OpenAPI spec and the MCP tool schemas are the source of truth for SDK types. Each SDK has a `generate.sh` (Go: `oapi-codegen`; Python: `openapi-python-client` plus a hand-written streaming layer; TypeScript: `openapi-typescript`). The generators run in Tier 0 and `git diff --exit-code` ensures generated code is committed. Hand-written code lives in clearly demarcated directories (`*/streaming/`, `*/mcp/`, `*/lifecycle/`) and is reviewed separately from generated code.

**SDK contract test harness.** `tests/tier3_contract/sdks/` is the shared harness. The same Go test driver exercises each SDK by spawning a language-native helper process (`sdks/client/python/test-helper`, `sdks/client/typescript/test-helper`) that accepts commands over stdin and reports outcomes over stdout. The harness runs the same operation matrix against every SDK and asserts equivalence — Python client → gateway behaves identically to Go client → gateway. This is the SDK equivalent of the REST ↔ MCP consistency suite.

**Test runners required (local dependencies).**

| Language | Runner | Version |
|:---------|:-------|:-------:|
| Go | `go test` | 1.22 |
| Python | `pytest` + `tox` | Python 3.11, pytest 8.0 |
| TypeScript | `vitest` (preferred) or `jest` | Node 20 LTS, vitest 1.4 |

Each language runtime is installed by `scripts/setup-dev.sh`. CI uses pre-built images that pin the same versions.

**Idiomatic style validation.**

- Go: `golangci-lint` with the project profile; `go vet`; `gofumpt`.
- Python: `ruff` for lint and format; `mypy --strict` for type checks.
- TypeScript: `eslint` with the project config; `tsc --noEmit` for type checks; `prettier`.

**Cadence.**
- PR: Tier 0 static, Tier 1 unit, the full Tier 3 SDK contract harness, the runtime-author quick-start TTHW.
- Nightly: full Tier 4 integration with each SDK against the compose stack; the gateway-version compatibility matrix.
- Pre-release: SDK conformance against the production-grade chart on Tier 5 (Kind) and Tier 6 (cloud); package-manager publication smoke tests (`go install`, `pip install`, `npm install`) against the staging registry.

**Anti-goals.** The SDKs do not embed retry logic for non-retryable errors. They do not silently swallow errors. They do not store credentials on disk. They do not provide a "convenience" surface that diverges from the published API. The test suite asserts these anti-goals explicitly.

---

## 15. Operability, CLI, and runbook tests

The operability surface is mandatory from Phase 3.5 onward (§spec/17.8.5, §spec/25). It must be testable from Phase 3.5 onward as well.

### 15.1 `lenny-ctl` command coverage

Every documented subcommand in spec/24 has at least one Tier 4 integration test against the compose stack and, for cluster-affecting commands, a Tier 5 Kind test. The full list is enumerated in §spec/24; the test surface mirrors it.

- Bootstrap: idempotent re-run, dry-run shows accurate diff.
- Preflight: DSN precedence (flags > env > values.yaml), probe timeouts respected.
- Runtime management: grant-access, revoke-access, list-access.
- Pool management: upgrade state machine with pause/resume/rollback, drain, sync-status, resume-reconciliation, circuit-breaker override.
- Credential pool management: add/update/remove/revoke/re-enable.
- Quota: reconcile (post-Redis-recovery MAX-rule re-aggregation).
- Circuit breakers: open/close at runtime/pool/connector/operation_type scope.
- External adapter management: validate (schema-driven compliance).
- User and token management: rotate-token; embedded-mode `lenny token print`.
- Tenant management: delete; force-delete with legal-hold override.
- Session investigation: get; force-terminate.
- Erasure job management: get; retry; clear-restriction.
- Migration management: status; down.
- Policy management: audit-isolation.
- Agent-operability extensions: me; operations; events; diagnose; runbooks; upgrade; audit; drift; backup; restore; locks; escalations; logs; mcp-management.
- Server discovery and routing: ops-server flag precedence; auto-discovery; fallback.
- Session operations (MCP-based): new; attach; send; interrupt; cancel; list; get; logs.
- Runtime scaffolding: init (language × template matrix); validate; publish.
- Local stack: up; down; status; logs; restart; image import/list/rm.
- Installation wizard: interactive; non-interactive; save-answers; offline.

### 15.2 Runbook validation

`docs/runbooks/` contains 56 runbooks. Each has a Tier 8 chaos test that:
1. Triggers the runbook's condition.
2. Confirms the documented alert fires.
3. Executes the runbook's documented remediation through the API (not kubectl).
4. Asserts the system returns to healthy.
5. Confirms the audit trail records the operator action and any required justification.

The mapping between runbook and test lives in `tests/tier8_chaos/runbook-map.yaml` and is validated by `lenny-test validate-maps`: every runbook has a matching test, and every test names its runbook.

### 15.3 Operability event stream

Spec/25.5 defines an SSE event stream and an in-memory buffer. The Tier 4 integration suite exercises:
- SSE connection and event delivery.
- Event-type and severity filtering.
- Primary source (Redis stream) with fallback to gateway buffer.
- Webhook subscriptions with SSRF-validated callback URLs.
- Cursor model: monotonic uint64 per replica, ULID-like eventKey, gap detection on cursor eviction.

### 15.4 MCP management server

`/mcp/management` exposes admin operations as MCP tools. The Tier 3 contract suite validates: tool inventory, scope enforcement via `x-lenny-scope`, parameter validation, output format. The OpenAPI-to-MCP code generation is CI-enforced (Tier 0) so the tool inventory and the OpenAPI never diverge.

---

## 16. Documentation tests

Documentation is a first-class artifact. The Tier 11 suite verifies:

1. Every code block in `docs/` parses or compiles in its declared language.
2. Every link resolves.
3. Every shell example runs in a sandboxed environment and produces the documented output.
4. Every YAML example validates against its schema.
5. The Jekyll site builds.
6. The diagrams build (one render per documented diagram).
7. The ADR catalog is intact: every numbered ADR has a file, every "Planned" entry references the spec, no renumbering.
8. The `time-to-hello-world` sequence works: `brew install`, `lenny up`, `lenny session start` against the bundled `chat` runtime, terminate within five minutes total wall-clock.
9. Runtime author quick-start works: `lenny runtime init my-agent --language go --template coding`, `make image`, `lenny runtime publish` (against a local mock registry), session creation against the new runtime.
10. The operator install path works: `lenny-ctl install --non-interactive --answers <file>`, smoke session succeeds.

These checks run on PR. Failure on doc tests is treated the same as failure on application tests.

---

## 17. Test authoring conventions

The conventions below are mandatory. A test that violates them fails Tier 0 lint or `lenny-test validate-*`.

### 17.1 Naming

- Test functions follow `Test<Subject><Behavior>`. The Subject is a noun (the component or store under test). The Behavior is a sentence fragment in declarative voice.
- File names match the subject. `pkg/store/session/session.go` → `pkg/store/session/session_test.go`. Component-tier suites mirror the package path under `tests/tier2_component/`.
- Build tags are mandatory above the tier. Unit tests carry no tag. Component tests: `//go:build component`. Contract: `//go:build contract`. Integration: `//go:build integration`. E2E: `//go:build e2e`. Load: `//go:build load`. Chaos: `//go:build chaos`. Security: `//go:build security`. Conformance: `//go:build conformance`.

### 17.2 Required annotations

Above every test function:

```go
// spec: 4.6.1 (warm pool controller — pod lifecycle), 12.3 (postgres ha requirements)
// diagnosis: ClaimSandbox returned a row already claimed by another goroutine.
//            Likely missing transaction isolation in pkg/controller/warmpool/claim.go,
//            or SELECT ... FOR UPDATE SKIP LOCKED applied to the wrong query.
func TestSandboxClaimSkipLocked(t *testing.T) { ... }
```

- `spec:` is mandatory on every test from Tier 2 onward. It lists every spec section the test encodes.
- `diagnosis:` is mandatory on every test from Tier 2 onward.
- The harness extracts both at compile time. Missing or malformed annotations fail Tier 0 lint.

### 17.3 Table-driven by default

Tests with more than one input/output pair use table-driven form:

```go
func TestWorkspacePlanMode(t *testing.T) {
    cases := []struct {
        name     string
        mode     string
        wantCode string
    }{
        {"valid_644", "0644", ""},
        {"setuid_rejected", "4755", "WORKSPACE_PLAN_INVALID"},
        {"sticky_on_file_rejected", "1644", "WORKSPACE_PLAN_INVALID"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) { ... })
    }
}
```

Each row is a subtest with a deterministic name. `go test -run TestWorkspacePlanMode/setuid_rejected` runs a single row.

### 17.4 Determinism

- No `time.Now()` in tests. Use `testinfra/timectl`.
- No `crypto/rand` or `math/rand` directly in tests. Use `testinfra/randctl`.
- No naked `time.Sleep` in tests. Use a condition wait (`testinfra/wait.For`) with an explicit timeout and a descriptive message.
- Goroutine leaks fail the test. Every test that spawns goroutines uses `testinfra/goleak`.
- No port hardcoding. Every test that opens a port uses `testinfra/ports.NewListener(t)`.

### 17.5 Cleanup

- Every test that creates a resource (a tenant, a session, a sandbox) registers a `t.Cleanup` that deletes it.
- Every test that allocates infrastructure (a schema, a Redis namespace, a MinIO bucket) registers a `t.Cleanup` that returns it.
- Cleanup is best-effort and silent on failure. The infrastructure layer reaps orphans on the next test run.

### 17.6 Parallelism

- Tier 1 tests are `t.Parallel()` by default. The unit tier is embarrassingly parallel.
- Tier 2 component tests are `t.Parallel()` within a suite. Each test reserves its own schema or namespace.
- Tier 4 integration tests are serial by default within a suite. The compose stack has finite capacity.
- Tier 5 e2e and beyond are serial within a suite; parallelism is at the suite level managed by CI.

### 17.7 Assertions

- The project uses standard `testing` plus a thin `testinfra/assertions` package (typed comparisons, JSON equality with ordering hints, structural matchers for state machines).
- No `testify` or `gomega` to keep dependency surface narrow. Existing projects can borrow assertion helpers via the `testinfra/assertions` API.

### 17.8 Logging

- `t.Log` is for human debugging.
- Structured assertions emit machine-readable failure context that the verdict producer captures.
- No `fmt.Println` in tests.

### 17.9 Skipping

- A test that requires unavailable infrastructure (no Kind, no gVisor, no cloud access) calls `t.Skip` with a reason that the harness records.
- A test gated by a phase calls `t.Skip` with reason `not-yet-applicable: phase-<N>`.

### 17.10 Flake budget

- Tests must pass 50 consecutive runs. The harness runs `lenny-test stress --test <Name> --runs 50` periodically. A test that fails in this loop is quarantined and the owner is paged.

---

## 18. Fixtures and golden data

`tests/testinfra/fixtures/` holds seed data. Three classes:

### 18.1 Reference fixtures

Stable, version-controlled fixtures used across many tests:
- Tenants: `acme`, `globex`, `initech`.
- Users: `alice@acme.com`, `bob@acme.com`, `carol@globex.com`.
- Runtimes: `echo`, `streaming-echo`, `delegation-echo` plus stubbed reference catalog entries.
- Pools: `acme-default-runc`, `acme-default-gvisor`, `globex-default-gvisor`.
- Credential pools: `mock-anthropic`, `mock-openai`, `mock-google`.
- Delegation policies: `coding-default`, `chat-strict`.
- Environments: `acme/dev`, `acme/prod`.

### 18.2 Generated fixtures

For property-based and load tests: generators that produce valid `WorkspacePlan`, `TaskRecord`, `OutputPart` instances under fixed seeds.

### 18.3 Golden files

Output that should not change without intent: HTTP response payloads, OpenAPI schemas, CRD YAML, Helm-rendered manifests. The harness diffs against the golden file on assertion failure and `lenny-test --update-golden` updates the file.

Golden file updates are PR-reviewed. The diff in the PR is the explicit signal that an externally visible contract changed.

### 18.4 Naming examples

Per the `.claude/rules/doc-style.md` cryptography convention: `alice`, `bob`, `carol`, `dave`, etc. for users; `acme`, `globex`, `initech` for tenants. No real names of project people, customers, or contributors.

---

## 19. Property-based, fuzz, and mutation testing

### 19.1 Property-based

`pgregory.net/rapid` powers property tests for algebraic invariants.

Targeted properties:
- Budget carving: for any parent budget P and child budget set {c_i}, sum(c_i) ≤ P and parent_remaining = P − sum(c_i).
- Isolation monotonicity: for any chain, the isolation profile sequence is monotonically non-weakening.
- Audit hash chain: for any sequence of audit events under a single tenant, the chain validates from origin.
- Migration round-trip: forward then backward then forward leaves the schema bit-identical.
- State machine round-trip: serialize then deserialize a state leaves the same state.
- Workspace plan: any valid plan produces a deterministic materialization.

### 19.2 Fuzz

Go fuzz targets for every parser and every wire-format boundary:
- Adapter JSON Lines messages.
- Lifecycle channel messages.
- MCP envelope.
- Adapter manifest.
- Output parts (inline and ref, every type).
- Workspace plan.
- OCSF event.
- Webhook callback URLs (the SSRF mitigation pipeline).
- Audit event hash chain.

Each fuzz target runs with a small corpus per PR and a longer corpus nightly. Crashes are stored under `tests/testinfra/fuzz/crashes/` and replayed on every subsequent run.

### 19.3 Mutation

`gremlins` runs nightly on the unit tier. The mutation report identifies surviving mutants in critical packages (`pkg/store/...`, `pkg/policy/...`, `pkg/audit/...`). Surviving mutants are filed as issues to the package owner; resolution may be a stronger test or a justified suppression.

---

## 20. CI framework and pipeline

This section is the bridge between the framework-agnostic test infrastructure described above and the concrete CI integration. The harness (`cmd/lenny-test/`) is the single source of truth: every workflow below is a thin wrapper that calls `lenny-test --group <name>` or `lenny-test --tier <name>`. The same commands run on a developer laptop.

### 20.1 Framework choice

**Selected: GitHub Actions.** The repository is hosted on GitHub, the project targets community contributions, and GHA offers the tightest integration available for the use cases below.

Decision factors:

| Factor | GHA | Notes |
|:-------|:---:|:------|
| Free for OSS public repos | yes | Unlimited minutes for `ubuntu-latest`, `macos-latest`, `windows-latest` runners |
| OIDC federation to GCP, AWS, Azure | yes | First-class support per provider; no long-lived secrets |
| PR-comment and inline-annotation surface | yes | Required for the verdict integration in §20.5 |
| Self-hosted runner support | yes | Required for Tier 5+ (memory and time ceilings on hosted runners) |
| Reusable workflows and composite actions | yes | Bounds YAML sprawl |
| Mature community familiarity | yes | Lowers contributor friction |
| Per-repo cache cap | 10 GB | Manageable with the strategy in §20.6 |
| Hosted-runner wall-clock cap | 6 hours | Tier 7 sustained load and Tier 6 cloud must run on self-hosted |

Alternatives considered:
- **Buildkite.** More programmable pipeline model, scales better for very complex matrices. Adds operational cost (agents must be hosted somewhere). Recommended reconsideration trigger: GHA YAML becomes a recurring debugging bottleneck.
- **CircleCI.** Strong caching and matrix support. Less GitHub-native; paid plans for the scale Lenny will reach.
- **GitLab CI.** Tightly coupled to GitLab hosting; not applicable.
- **Tekton.** Kubernetes-native, conceptually attractive for a K8s-native project. Operational burden outweighs the benefits at this stage.
- **Jenkins.** Mature but high-maintenance; not a fit for a new project.

### 20.2 Deferred: Dagger as an orchestration layer

Dagger lets you express CI pipelines as code (Go, Python, or TypeScript) that compile to a DAG of containerized operations. Workflow YAML becomes a one-liner that runs `dagger call <function>`. The same code runs locally on a laptop. BuildKit-based caching is operation-level and survives across local and CI runs through Dagger Cloud or a self-hosted cache.

Lenny's `lenny-test` harness already provides the "same command everywhere" property that Dagger's main pitch builds on. Adopting Dagger at Phase 0 would add tooling complexity for value we can't yet quantify. The reconsideration triggers are:

1. Matrix fan-out for SDKs or cloud providers becomes unwieldy in YAML (likely Phase 11+).
2. Cache misses on PR routinely dominate wall-clock.
3. Local replay of CI failures becomes a recurring source of friction.

Migration path when triggered: introduce a `dagger/` module that wraps each `lenny-test --group` invocation as a Dagger function, then thin the GHA workflows to `dagger call`. This is a refactor of `.github/workflows/`, not of the test infrastructure.

### 20.3 Runner architecture

Lenny uses two runner classes.

**Hosted runners (GitHub-managed).**
- `ubuntu-latest` for Tier 0 (static), Tier 1 (unit), Tier 3 (contract), Tier 4 (integration with the compose stack), Tier 10 (conformance against bundled runtimes), Tier 11 (docs).
- `macos-latest` for Tier 11 documentation diagram rendering (the `qlmanage` step) and SDK smoke tests against the macOS toolchain.
- Windows is not in scope.

**Self-hosted runners.**
- Tier 2 (component) optionally uses self-hosted for the cached-container performance win, but works fine on hosted.
- Tier 5 (e2e on Kind) requires self-hosted: 14 GB RAM and 8 cores are the documented minimum; hosted runners cap at 7 GB / 4 cores.
- Tier 6 (e2e on cloud) requires self-hosted with provider credentials accessible via OIDC.
- Tier 7 (load) requires self-hosted; sustained runs exceed the 6-hour hosted ceiling.
- Tier 8 (chaos) and Tier 9 (security) run wherever Tier 5 runs.

Self-hosted runner pool layout:

- The runners themselves are Kubernetes pods scheduled by `actions-runner-controller` (ARC) in a dedicated GKE or AKS cluster (`lenny-ci-cluster`). ARC autoscales from zero based on the GitHub webhook queue.
- Pool sizing: minimum 0, maximum 20 standard runners, maximum 4 cloud-test runners with higher resource budgets.
- Each runner is ephemeral: one job per pod, pod deleted after the job. This eliminates state leakage between jobs.
- Runners receive cloud credentials via Workload Identity / IRSA / Workload Identity Federation depending on which cluster they live in.
- The CI cluster is operated by the Lenny project. The configuration is in `deploy/ci/` (a deliverable of Phase 17a, but stubbed from Phase 0 onward).
- A security review precedes every runner-image change; see §20.13.

### 20.4 Cloud authentication

CI authenticates to GCP, AWS, and Azure via OIDC federation. Long-lived access keys are not stored in repository secrets. The setup steps are documented in [`TESTING_DEPENDENCIES.md`](TESTING_DEPENDENCIES.md) §13.

Per-workflow snippet:

```yaml
permissions:
  id-token: write           # required for OIDC
  contents: read
  pull-requests: write      # required for verdict PR comments
```

Per-provider auth step (reusable workflow form):

```yaml
- uses: google-github-actions/auth@v2
  with:
    workload_identity_provider: ${{ vars.GCP_WIP }}
    service_account: ${{ vars.GCP_SA }}

- uses: aws-actions/configure-aws-credentials@v4
  with:
    role-to-assume: ${{ vars.AWS_ROLE_ARN }}
    aws-region: us-east-2

- uses: azure/login@v2
  with:
    client-id: ${{ vars.AZURE_CLIENT_ID }}
    tenant-id: ${{ vars.AZURE_TENANT_ID }}
    subscription-id: ${{ vars.AZURE_SUBSCRIPTION_ID }}
```

Provider trust configuration (WIP pools, IAM roles, federated credentials) is provisioned by Terraform in `deploy/terraform/cloud/` and documented per provider in `docs/operator-guide/installation.md`.

### 20.5 Verdict integration

Every workflow step that runs `lenny-test` produces three artifacts in `tests/results/`:

1. **`latest.json`** — the canonical machine-readable verdict per §7. Uploaded as a build artifact and indexed by run ID.
2. **`latest.junit.xml`** — JUnit XML emitted via `--output junit`. GHA's `actions/upload-artifact@v4` plus `dorny/test-reporter@v1` surface this as the "Tests" tab on the workflow run.
3. **GHA annotations** — `lenny-test --output github-annotations` emits `::error file=<file>,line=<line>::<message>` lines for each failing test, producing inline annotations on the PR diff. The diagnosis string from `// diagnosis:` is the annotation body.

PR comment summary: a sticky comment (one per PR, updated on every push) summarizes:
- Verdict per tier (✓ / ✗ / skipped / pending).
- Spec sections currently FAIL.
- Top three failing tests with diagnosis snippets.
- Re-run commands as copy-paste-ready code blocks.
- A link to the full verdict JSON.

The PR-comment action is implemented as a composite action in `.github/actions/post-verdict-comment/`. Phase 0 ships a stub; full implementation lands with Phase 2 when `lenny-test` produces real verdicts.

### 20.6 Caching strategy

The 10 GB per-repo cache cap is the binding constraint. The strategy:

- **Go module cache** (`~/go/pkg/mod`): keyed by `go.sum` hash. Restore-on-miss falls back to the previous main branch cache. Typical size: 500 MB to 1 GB.
- **Go build cache** (`~/.cache/go-build`): keyed by `go.sum` plus a workflow tag. Typical size: 1 to 2 GB. Restored before Tier 1 and Tier 2.
- **npm cache** (`~/.npm`): keyed by `package-lock.json` hash. Per-SDK and per-tool.
- **pip cache** (`~/.cache/pip`): keyed by `pyproject.toml` plus lock file.
- **Ruby gem cache** (`vendor/bundle`): keyed by `Gemfile.lock` (docs tier only).
- **Docker layer cache** (testcontainers and compose images): persisted via the GitHub Container Registry (`ghcr.io/lennylabs/cache/*`) using `docker buildx build --cache-to type=gha,mode=max`. This bypasses the 10 GB repo cap because GHCR images are not subject to it.
- **Kind node images**: pinned by digest and pre-pulled to self-hosted runners on the ARC node template. Hosted-runner Kind jobs (if any) pull from GHCR using the Docker layer cache.
- **Test runtime images** (`echo`, `streaming-echo`, `delegation-echo`): built on every change and pushed to `ghcr.io/lennylabs/runtimes/*` keyed by Git SHA. Used by Tier 5+ tests by SHA.
- **Container cache socket** (`lenny-test-cached`) is a developer convenience and does not apply to CI; CI always starts with cold containers for isolation.

Cache eviction policy: stale caches older than seven days are removed by a weekly workflow (`.github/workflows/cache-prune.yml`). Branch-scoped caches are pruned when the branch is deleted.

### 20.7 Reusable workflows and composite actions

Cross-cutting patterns live under `.github/workflows/reusable/` and `.github/actions/`. They keep the top-level workflows thin.

| Workflow / action | Purpose |
|:------------------|:--------|
| `reusable/go-setup.yml` | Install Go, restore caches, install `golangci-lint`, `gofumpt`, `goimports`, `sqlc`, `migrate`, `oapi-codegen` |
| `reusable/node-setup.yml` | Install Node 20, restore npm cache, run `npm ci` in the named workspace |
| `reusable/python-setup.yml` | Install Python 3.11, restore pip cache, install dev dependencies |
| `reusable/lenny-test.yml` | Run `lenny-test --group <input>` with verdict upload and PR-comment update |
| `reusable/cloud-auth-gcp.yml` | OIDC federation to GCP; outputs configured gcloud context |
| `reusable/cloud-auth-aws.yml` | OIDC federation to AWS; outputs AWS_PROFILE env |
| `reusable/cloud-auth-azure.yml` | OIDC federation to Azure; configures kubelogin |
| `reusable/kind-up.yml` | Provisions a Kind cluster via `scripts/setup-cluster.sh` |
| `reusable/cloud-cluster-up.yml` | Provisions a managed cluster on the named provider |
| `actions/post-verdict-comment/` | Composite action that posts or updates the PR verdict comment |
| `actions/upload-verdict/` | Uploads `tests/results/latest.json` and `latest.junit.xml` as artifacts |

### 20.8 PR pipeline (every push)

Workflow file: `.github/workflows/pr.yml`. Triggered on `pull_request` and `push` to feature branches. Target wall-clock: under 15 minutes.

1. `lenny-test --group pr-fast` (changed-only, max-tier component). Fails fast on common bugs.
2. `lenny-test --tier static`.
3. `lenny-test --tier unit`.
4. `lenny-test --tier component`.
5. `lenny-test --tier contract`.
6. `lenny-test --tier integration`.
7. `lenny-test --tier e2e_kind --subset critical-path`.
8. `lenny-test --tier security --subset critical`.
9. `lenny-test --tier chaos --subset core`.
10. `lenny-test --tier conformance --subset bundled-runtimes`.
11. `lenny-test --tier docs`.

Each step produces a verdict JSON. The sticky PR comment summarizes status, links to artifacts, and identifies the failing spec sections. A failing test publishes the diagnosis and the rerun command directly in the comment.

Concurrency: `concurrency.group = pr-${{ github.head_ref }}`, `cancel-in-progress: true`. A new push to the same PR cancels the in-flight run.

### 20.9 Nightly pipeline

Workflow file: `.github/workflows/nightly.yml`. Scheduled `0 5 * * *` UTC. Target wall-clock: under 2 hours.

1. Full PR pipeline.
2. `lenny-test --tier e2e_kind` (full suite).
3. `lenny-test --tier e2e_cloud --subset critical-path` rotated across the three providers (so each provider runs at least every 48 hours).
4. `lenny-test --tier chaos` (most scenarios; severe-cloud scenarios run on cloud).
5. `lenny-test --tier security` (full suite minus pen-test).
6. `lenny-test --tier load --scenario <per-phase-baseline>` per the spec's incremental gates (Phase 6.5, 9.5, 11.5).
7. `lenny-test --tier conformance --subset reference-catalog`.
8. Dependency audit: `go mod verify`, `go list -m -u all`, `trivy fs`, `trivy image` against all built images.

A nightly failure pages the on-call rotation defined in `docs/runbooks/index.md`.

### 20.10 Weekly / pre-release pipeline

Workflow files: `.github/workflows/weekly.yml` and `.github/workflows/pre-release.yml`. Scheduled weekly on Sunday; pre-release triggered manually via `workflow_dispatch`. Target wall-clock: under 8 hours.

1. Full nightly pipeline.
2. `lenny-test --tier load` (full Tier 2 plus Tier 3 sampling). Phase 13.5 and 14.5 baselines.
3. `lenny-test --tier chaos` (full suite).
4. `lenny-test --tier e2e_cloud` (full suite on **all three providers — GKE, EKS, AKS** — each in both `cloud-small-<provider>` and `cloud-sandbox-<provider>` shapes; parity matrix asserted).
5. External pen-test driver run against the prior pen-test's findings.
6. Multi-profile validation: cloud-managed, self-managed, embedded chart configs.
7. SLO regression comparison against the prior release baseline.

### 20.11 Per-phase gate pipeline

Workflow file: `.github/workflows/phase-gate.yml`, parameterized by `inputs.phase`. For each phase in §13, the workflow runs `lenny-test --group phase-<N>-gate` and produces a phase-completion artifact. The artifact is required to merge the phase's implementation branches.

### 20.12 Branch protection

Required checks for `main`:
- Tier 0 through Tier 6 critical-path on PR.
- Tier 11 (docs).
- `lenny-test validate-maps`.
- `lenny-test validate-diagnosis`.

No force-push to `main`. No merge with failing checks. No merge without an ADR for changes that require one (per `docs/adr/index.md`). Linear history enforced. Signed commits recommended after Phase 14 (image signing GA).

### 20.13 Secrets management

The project minimizes long-lived secrets:

- Cloud credentials: OIDC federation only. No long-lived service-account keys in repository secrets.
- GHCR push: uses `GITHUB_TOKEN` with `packages: write`. No personal access tokens.
- External pen-test report uploads: encrypted at rest, scoped to the pen-test partner via a separate org-level secret with two-person approval.
- Real LLM provider keys (for pre-release smoke against real providers): held in `lenny-prerelease` environment with required reviewers.

Secret scanning: GitHub's built-in scanner is on. `trivy fs --scanners secret` runs in Tier 0.

Runner-image security: the ARC runner image is rebuilt weekly with the latest base image, signed with cosign, and digest-pinned in the ARC values file. Every PR that modifies the runner image requires review from a maintainer.

### 20.14 Concurrency and queueing

Per-workflow concurrency groups:

| Workflow | Group | Cancel in progress |
|:---------|:------|:------------------:|
| pr.yml | `pr-${{ github.head_ref }}` | yes |
| nightly.yml | `nightly` | no |
| weekly.yml | `weekly` | no |
| pre-release.yml | `pre-release-${{ inputs.tag }}` | no |
| phase-gate.yml | `phase-gate-${{ inputs.phase }}-${{ github.ref }}` | yes |
| cache-prune.yml | `cache-prune` | yes |

Self-hosted runner queueing: when the ARC pool is saturated, jobs queue. The dashboard at `lenny-ci-cluster:/grafana/d/runners` reports queue depth and pages on sustained backlog > 10 jobs for > 15 minutes.

### 20.15 Artifact retention

| Artifact | Retention |
|:---------|:---------:|
| PR verdict JSON and JUnit | 30 days |
| Nightly verdict and load baselines | 90 days |
| Pre-release verdicts and load baselines | 365 days |
| Phase-gate completion artifacts | indefinite (used as merge gates) |
| Container images pushed to GHCR | indefinite for tagged releases, 30 days for SHA-tagged builds |
| Test failure logs | 30 days |

### 20.16 Migration path off GHA

The test infrastructure is GHA-agnostic by construction: every concrete piece of CI logic lives in `cmd/lenny-test/`, `scripts/`, and provider-specific reusable workflows. Migrating to Buildkite, Dagger, or another framework involves rewriting `.github/workflows/` and porting the reusable workflows. The harness, the verdict format, the spec-map, the change-graph, the test runtimes, and the per-tier suites are unaffected.

---

## 21. Flakiness and reliability

### 21.1 Flake budget

A test is flaky if it fails non-deterministically. The budget for flaky tests is zero. The reality is non-zero, so the harness enforces a process:

1. `lenny-test stress --test <Name> --runs 50` periodically. The harness invokes this against all PR-tier tests once a week.
2. Failures in stress mode quarantine the test under a `t.Skip("quarantined: <issue-link>")`.
3. Quarantine is a one-week clock. After one week, the test owner has authored a fix or the test is removed.
4. Quarantined tests do not run in PR. Nightly runs them in a separate report.

### 21.2 Root-cause categories

The harness annotates failing runs with a root-cause guess based on patterns in `tests/results/history.jsonl`:
- `flaky-time`: failure correlates with high CI load.
- `flaky-network`: failure correlates with retried HTTP requests.
- `flaky-ordering`: failure correlates with parallel test execution.
- `genuine`: failure reproduces deterministically.

The annotation is a hint, not a verdict. The author confirms the cause.

### 21.3 Infrastructure failures

A test that fails because infrastructure failed (testcontainer crash, Kind cluster bring-up timeout, cloud quota exceeded) reports `verdict: INCONCLUSIVE`. The harness retries up to two times with fresh infrastructure before declaring `FAIL`.

### 21.4 Tracking

`tests/flake-budget.yaml` lists currently quarantined tests and the owner. CI fails to merge a PR that adds a quarantined test without an associated issue and ETA.

---

## 22. Quality gates and metrics

The infrastructure ships with quality metrics that go-live gates enforce.

### 22.1 Coverage

- Unit-tier coverage on new code: 80%, enforced per PR.
- Repository coverage: tracked but not gated. Refactoring is not penalized.
- Mutation-survival rate on critical packages: tracked nightly; surviving mutants in `pkg/audit/`, `pkg/store/`, `pkg/policy/` are filed as issues.

### 22.2 Spec coverage

- Every spec section with behavior is mapped to at least one test.
- The spec-map dashboard (built into the verdict reporter) shows which sections are green, red, or unimplemented.
- The "spec implementation percentage" is the fraction of spec sections currently green at the highest tier they reach (the highest tier that runs end-to-end without skips for that section).

### 22.3 Diagnosis coverage

Every Tier 2-and-up test has a diagnosis. The harness validates and reports the percentage; the gate is 100%.

### 22.4 Determinism gate

Tier 1 unit tests must be deterministic. The harness runs them under `-count=10` weekly. Any flake fails the gate.

### 22.5 Load baseline drift

The Phase 14.5 baseline is the production SLO baseline. Drift > 15% in any percentile against the prior release blocks pre-release.

### 22.6 Conformance gate

The `lenny-compliance` harness must pass for every bundled runtime and every reference runtime at the runtime's declared integration level. A failure blocks the runtime from registering and from being included in release artifacts.

### 22.7 Documentation gate

The Time-to-Hello-World scenario must complete in under five minutes. The runtime-author scaffold-to-publish flow must complete in under five minutes for the supported Language × Template combinations.

---

## 23. Open questions and forward compatibility

The areas below are explicitly not committed in the spec. The test infrastructure must remain flexible.

### 23.1 Post-V1 features

- A2A adapter (§spec/21). The test surface accommodates new external protocols through `ExternalAdapterRegistry`; tests do not assume A2A endpoints exist.
- AP (Agent Protocol) adapter. Same accommodation.
- Multi-cluster federation. Session ID uniqueness rules are encoded; tests assume single cluster in v1 but do not bake single-cluster paths into the wire format.
- SSH git URLs. v1 rejects them at schema validation; post-v1 enables them. Tests assert HTTPS-only in v1 but do not bake the restriction into post-v1 expectations.
- Direct external connector access. v1 keeps connectors session-internal. Post-v1 may expose them. The test surface treats connector visibility as a configuration knob.

### 23.2 Pluggable surfaces

- Memory backends: the test infrastructure validates the default Postgres + pgvector implementation and the custom-backend preflight contract. Third-party backends provide their own contract validation.
- Semantic cache backends: similar.
- Evaluation: Lenny does not ship eval logic. The test infrastructure validates the eval result store and the hook surface; eval logic is the deployer's concern.
- Guardrails and content classification: the interceptor chain is tested. The classifier implementations are deployer-supplied.

### 23.3 Future scaling

The five scaling-extension interfaces (`PodRegistry`, `StoreRouter`, `ClusterRegistry`, `CredentialGenerator`, `EventBus`) have v1 stub implementations and well-defined contracts. The test infrastructure parameterizes the contract suites so future implementations (`PostgresPodRegistry`, multi-shard `StoreRouter`, multi-cluster `ClusterRegistry`) can be plugged in without rewriting tests.

### 23.4 Custom rules and operator overrides

Bundled alerting rules and recommendation rules come from `pkg/alerting/rules` and `pkg/recommendations/rules`. Operators can override them. The test suite covers both the bundled defaults and the override mechanism, but it does not bake specific custom rules.

### 23.5 Cross-environment delegation

Phase 15 ships basic per-environment rules. Richer controls and runtime-level exceptions are post-V1. The test infrastructure covers the v1 bilateral-declaration contract and leaves room for additional gates.

---

**End of plan.**
