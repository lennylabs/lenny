## 18. Build Sequence

This section enumerates the application-code phases for the Lenny platform. Each phase identifier (`Phase 0`, `Phase 1`, `Phase 1.5`, ..., `Phase 17b`) is a stable cross-reference target and maps one-to-one to a subsection in [`TESTING.md`](../TESTING.md) §13. This section names the application-code deliverables and the exit gate for each phase. The test infrastructure that gates each phase is defined in the corresponding [`TESTING.md`](../TESTING.md) §13.X subsection.

The phasing is directional. Surface ordering and timing will shift as implementation surfaces new constraints. Treat the sequence as authoritative but evolving.

Application code and test infrastructure are built in parallel. The test infrastructure for a given phase lands first (per [`TESTING.md`](../TESTING.md) §13.X), and the application-code deliverables for that phase are then gated by the matching test group.

### 18.1 Phasing Intent

The build order is organized into the following bands.

1. **Foundations (Phase 0 through Phase 2.8).** Repository bootstrap, ADRs, core domain types, wire contracts, the database migration framework, the adapter binary protocol, observability primitives, and the two reference runtimes that anchor adapter conformance (`echo` for Basic-level, `streaming-echo` for Full-level).
2. **Controllers, admission, and gateway core (Phase 3 through Phase 4.5).** The `PoolScalingController`, the `RuntimeUpgrade` state machine, the mTLS PKI, the admission webhooks that enforce CRD field ownership and pod-label immutability, the minimum-viable gateway binary with the Session API, and the admin API foundation with OIDC/OAuth 2.1 validation.
3. **External APIs and credentials (Phase 5 through Phase 5.8).** The `ExternalAdapterRegistry`, the REST and MCP overlapping operations, etcd encryption at rest, basic credential leasing, the Token Service, the minimum-viable quota and authentication interceptors, and the LLM Proxy with the native `anthropic_direct` translator.
4. **Interactive sessions, policy, and delegation (Phase 6 through Phase 10).** Interactive sessions and client SDKs, the full policy engine (idempotency, circuit breakers, audit hooks), checkpoint and resume, recursive delegation with `delegation-echo`, and the MCP fabric with the elicitation chain.
5. **Hardening and compliance (Phase 11 through Phase 14.5).** Multi-provider translators, credential rotation and revocation, Token Service KMS hardening, OAuth connectors, `type: mcp` runtimes, the `sessionPolicy` presets and service mode, full audit and `lenny-backup`, comprehensive security hardening, and the post-hardening SLO re-validation.
6. **Extensibility and launch (Phase 15 through Phase 17b).** The Environment resource with RBAC and cross-environment delegation, experiment primitives integrated with the `PoolScalingController`, documentation and community launch, and the post-v1 surfaces (memory, semantic caching, and evaluation hooks).

The load-test phases (Phase 6.5, Phase 9.5, Phase 11.5, Phase 13.5, Phase 14.5, and Phase 16.5) are explicit re-baselines. Each captures a Tier 7 baseline after a major capability lands and records the delta against the prior baseline, so regressions are visible before a subsequent phase consumes the same load envelope.

Two security review milestones are scheduled as human activities and produce documented findings rather than code. Phase 5.6 is the credential security design review. The Phase 13.5 → Phase 14 transition includes a full-system review built into the Phase 14 hardening pass.

Three Helm feature flags gate admission webhooks and subsystem templates against the phase a deployment has reached. `features.llmProxy` (default `false`) is enabled at Phase 5.8, `features.drainReadiness` (default `false`) at Phase 8, and `features.compliance` (default `false`) at Phase 13. The downgrade-enforcement mechanism is documented in [§17.2](17_deployment-topology.md#172-namespace-layout).

### 18.2 Phase Map

| Phase | Title                                                                  | Hard prerequisite           | TESTING.md row                                                                                                                                              |
| ----- | ---------------------------------------------------------------------- | --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0     | Bootstrap the infrastructure repo                                      | —                                       | [§13.0](../TESTING.md#130-phase-0--bootstrap-the-infrastructure-repo)                                                                                       |
| 1     | Core types and wire contracts                                          | **Phase 0** (ADR-007)                   | [§13.1](../TESTING.md#131-phase-1--core-types-and-wire-contracts)                                                                                           |
| 1.5   | Database migration framework                                           | —                                       | [§13.2](../TESTING.md#132-phase-15--database-migration-framework)                                                                                           |
| 2     | Adapter protocol + `make run` + ImageResolver + startup benchmark      | **Phase 1** (agent-sandbox)             | [§13.3](../TESTING.md#133-phase-2--adapter-protocol--make-run--imageresolver--startup-benchmark)                                                            |
| 2.5   | Observability foundation + shared rule packages                        | —                                       | [§13.4](../TESTING.md#134-phase-25--observability-foundation--shared-rule-packages)                                                                         |
| 2.8   | `streaming-echo` runtime                                               | —                                       | [§13.5](../TESTING.md#135-phase-28--streaming-echo-runtime)                                                                                                 |
| 3     | Pool scaling, delegation policy, runtime upgrade, mTLS                 | —                                       | [§13.6](../TESTING.md#136-phase-3--pool-scaling-delegation-policy-runtime-upgrade-mtls)                                                                     |
| 3.5   | Admission policies + `lenny-ops` first deploy                          | —                                       | [§13.7](../TESTING.md#137-phase-35--admission-policies--lenny-ops-first-deploy)                                                                             |
| 4     | Session manager + REST                                                 | **Phase 1.5 + 3** (`agent_pod_state`)   | [§13.8](../TESTING.md#138-phase-4--session-manager--rest)                                                                                                   |
| 4.5   | Admin API foundation + authentication + bootstrap                      | —                                       | [§13.9](../TESTING.md#139-phase-45--admin-api-foundation--authentication--bootstrap)                                                                        |
| 5     | ExternalAdapterRegistry + MCP/Completions/Open Responses               | —                                       | [§13.10](../TESTING.md#1310-phase-5--externaladapterregistry--mcpcompletionsopen-responses--restmcp-contract-tests)                                         |
| 5.4   | etcd encryption at rest                                                | —                                       | [§13.11](../TESTING.md#1311-phase-54--etcd-encryption-at-rest)                                                                                              |
| 5.5   | Basic credential leasing + Token Service                               | **Phase 5.4** (etcd encryption)         | [§13.12](../TESTING.md#1312-phase-55--basic-credential-leasing--token-service)                                                                              |
| 5.6   | Targeted security design review (credential)                           | —                                       | [§13.13](../TESTING.md#1313-phase-56--targeted-security-design-review-credential)                                                                           |
| 5.75  | Minimum viable policy enforcement                                      | —                                       | [§13.14](../TESTING.md#1314-phase-575--minimum-viable-policy-enforcement)                                                                                   |
| 5.8   | LLM Proxy + `lenny-direct-mode-isolation` admission webhook            | **Phase 3.5** (phase-stamp)             | [§13.15](../TESTING.md#1315-phase-58--llm-proxy--lenny-direct-mode-isolation-admission-webhook)                                                             |
| 6     | Interactive sessions + SDKs                                            | **Phase 5.75** (real-credential)        | [§13.16](../TESTING.md#1316-phase-6--interactive-sessions--sdks)                                                                                            |
| 6.5   | Incremental load test (streaming)                                      | —                                       | [§13.17](../TESTING.md#1317-phase-65--incremental-load-test-streaming)                                                                                      |
| 7     | Policy engine (quotas, budgets, audit hooks)                           | —                                       | [§13.18](../TESTING.md#1318-phase-7--policy-engine-quotas-budgets-audit-hooks)                                                                              |
| 8     | Checkpoint/resume + drain-readiness webhook                            | **Phase 3.5** (phase-stamp)             | [§13.19](../TESTING.md#1319-phase-8--checkpointresume--drain-readiness-webhook)                                                                             |
| 9     | Delegation + `delegation-echo`                                         | **Phase 5.5** (`/v1/oauth/token`)       | [§13.20](../TESTING.md#1320-phase-9--delegation--delegation-echo)                                                                                           |
| 9.5   | Incremental load test (delegation)                                     | —                                       | [§13.21](../TESTING.md#1321-phase-95--incremental-load-test-delegation)                                                                                     |
| 10    | MCP fabric (virtual child interfaces, elicitation chain)               | —                                       | [§13.22](../TESTING.md#1322-phase-10--mcp-fabric-virtual-child-interfaces-elicitation-chain)                                                                |
| 11    | Advanced credentials + multi-provider translators + revocation         | —                                       | [§13.23](../TESTING.md#1323-phase-11--advanced-credentials--multi-provider-translators--revocation)                                                         |
| 11.5  | Incremental load test (credential lifecycle)                           | —                                       | [§13.24](../TESTING.md#1324-phase-115--incremental-load-test-credential-lifecycle)                                                                          |
| 12a   | Token Service hardening (KMS envelope + OAuth)                         | —                                       | [§13.25](../TESTING.md#1325-phase-12a--token-service-hardening-kms-envelope--oauth)                                                                         |
| 12b   | `type: mcp` runtime support                                            | —                                       | [§13.26](../TESTING.md#1326-phase-12b--type-mcp-runtime-support)                                                                                            |
| 12c   | `sessionPolicy` presets and service mode                               | —                                       | [§13.27](../TESTING.md#1327-phase-12c--sessionpolicy-presets-and-service-mode)                                                                              |
| 13    | Full observability + audit + `lenny-backup` + compliance webhooks      | **Phase 3.5 + 5** (phase-stamp, OpenAPI) | [§13.28](../TESTING.md#1328-phase-13--full-observability--audit--lenny-backup--compliance-webhooks)                                                        |
| 13.5  | Pre-hardening full-system load baseline                                | —                           | [§13.29](../TESTING.md#1329-phase-135--pre-hardening-full-system-load-baseline)                                                                             |
| 14    | Comprehensive security hardening                                       | —                           | [§13.30](../TESTING.md#1330-phase-14--comprehensive-security-hardening)                                                                                     |
| 14.5  | Post-hardening SLO re-validation                                       | —                           | [§13.31](../TESTING.md#1331-phase-145--post-hardening-slo-re-validation)                                                                                    |
| 15    | Environment resource + RBAC + cross-environment delegation             | —                           | [§13.32](../TESTING.md#1332-phase-15--environment-resource--rbac--cross-environment-delegation)                                                             |
| 16    | Experiments + PoolScalingController integration                        | —                           | [§13.33](../TESTING.md#1333-phase-16--experiments--poolscalingcontroller-integration)                                                                       |
| 16.5  | Experiment load test SLO re-validation                                 | —                           | [§13.34](../TESTING.md#1334-phase-165--experiment-load-test-slo-re-validation)                                                                              |
| 17a   | Documentation + governance + community launch                          | —                           | [§13.35](../TESTING.md#1335-phase-17a--documentation--governance--community-launch)                                                                         |
| 17b   | Memory, semantic caching, eval hooks                                   | —                           | [§13.36](../TESTING.md#1336-phase-17b--memory-semantic-caching-eval-hooks)                                                                                  |

Hard prerequisites are summarized in [§18.40](#1840-hard-prerequisite-chain).

### 18.3 Phase 0 — Bootstrap the infrastructure repo

**Deliverables.**

- Repository structure under `cmd/`, `pkg/`, `tests/`, `schemas/`, `scripts/`, `docs/`, and `spec/`.
- ADR-007 (`SandboxClaim` optimistic locking) authored and merged under `docs/adr/`. The two verification tests required by ADR-007 (concurrent-claim integration test and leader-kill chaos test per [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers)) are stubbed; the chaos test executes against the WarmPoolController binary that ships in Phase 3.
- ADR-008 (open-source license) committed; `LICENSE` (MIT, per Resolved Decision 14 in [§19](19_resolved-decisions.md)) at the repository root.
- ADR template at `docs/adr/template.md` and the `docs/adr/README.md` index that describes the ADR process and numbering convention.
- Branch protection rules on `main` (required reviews, required status checks, signed-commit policy via DCO sign-off enforced by CI).
- Multi-tier CI matrix that routes community PRs through the static and unit tiers by default and gates kind-tier execution behind explicit reviewer approval.
- Secret-scanning configuration for forks (e.g., gitleaks on every PR; CI fails on a positive hit).
- CI scaffolding for static checks and documentation builds.

**Prerequisites.** None.

**Exit criteria.** The `phase-0-gate` test group passes per [TESTING.md §13.0](../TESTING.md#130-phase-0--bootstrap-the-infrastructure-repo): Tier 0 (Static) and Tier 11 (Documentation) pass on the empty repository and `lenny-test validate-maps` passes against the empty spec map and change graph.

### 18.4 Phase 1 — Core types and wire contracts

**Deliverables.**

- Core domain types as Go packages: `Runtime`, `SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`, `TaskRecord`, `TaskResult`, and the session state machines (suspended-session state and input-required state).
- Wire-contract artifacts under `schemas/`: `lenny-adapter.proto`, `lenny-adapter-jsonl.schema.json`, `messagepart.schema.json`, and `workspaceplan-v1.json`.
- Generated Go stubs from `lenny-adapter.proto`.
- buf-driven schema breaking-change detection wired into CI.

**Prerequisites.** Phase 0 exit gate. ADR-007 must be merged before Phase 1 implementation begins, per the verification requirement in [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers).

**Exit criteria.** The `phase-1-gate` test group passes per [TESTING.md §13.1](../TESTING.md#131-phase-1--core-types-and-wire-contracts): every Phase 1 contract test compiles and emits "not implemented" failures with diagnosis strings; Tier 0 (Static) passes. The Phase 1 exit gate additionally includes the agent-sandbox dependency health assessment defined in [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers); see [§18.40](#1840-hard-prerequisite-chain).

### 18.5 Phase 1.5 — Database migration framework

**Deliverables.**

- Migration tool selected and committed under `cmd/lenny-migrate` (or equivalent path documented in the chart).
- Initial Postgres schema migration covering the tables defined in [§12](12_storage-architecture.md), including the `agent_pod_state` mirror table per [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers).
- `lenny_tenant_guard` RLS trigger per [§12.3](12_storage-architecture.md#123-postgres-ha-requirements): rejects any query from a transaction that has not set `app.current_tenant`. Required for cloud-managed pooler deployments where transaction-mode pooling can otherwise leak across tenants.
- `lenny_billing_immutability` and `lenny_audit_immutability` Postgres triggers per [§11.7](11_policy-and-controls.md#117-audit-logging) items 1–2.
- `lenny_app` and `lenny_erasure` Postgres role separation per [§11.7](11_policy-and-controls.md#117-audit-logging) item 1; only `lenny_erasure` holds DELETE on audit and billing tables.
- Schema linter R-01 (`scripts/lint-schema.sh`) and query linter R-02 (`scripts/lint-queries.sh`) per [§14.1](../TESTING.md#141-rls-and-tenant-isolation).
- CI migration gate that runs migrations forward and back on every PR.
- Bundled `docs/runbooks/crd-upgrade.md` and `docs/runbooks/db-rollback.md` runbook stubs landed alongside the migration framework.

**Prerequisites.** Phase 1 exit gate.

**Exit criteria.** The `phase-1.5-gate` test group passes per [TESTING.md §13.2](../TESTING.md#132-phase-15--database-migration-framework): static linters pass and the Tier 2 migration round-trip suite passes against the initial schema. The `lenny_tenant_guard` trigger rejects a representative cross-tenant query.

### 18.6 Phase 2 — Adapter protocol, `make run`, ImageResolver, startup benchmark

**Deliverables.**

- `cmd/runtimes/echo` Basic-level reference runtime per [§15.4](15_external-api-surface.md#154-runtime-adapter-specification).
- `cmd/lenny-compliance` conformance harness with the Basic-level battery (binary execution, empty-stdin shutdown, message-to-response round-trip, heartbeat acknowledgement, unknown-type forward compatibility, shutdown deadline, and sequential-message handling).
- Adapter binary protocol implementation against the Phase 1 wire contracts.
- `make run` local dev mode launching the gateway against an in-memory store and the `echo` runtime.
- `pkg/common/registry/resolver.go` (`ImageResolver` with override > url > default precedence and digest enforcement).
- Adapter `SO_PEERCRED` startup self-test per [§4.7](04_system-components.md#47-runtime-adapter) (runs on every pod start before the READY signal; ships with `lenny_adapter_sopeercred_selftest_failed_total` and the adapter binary's `--require-so-peercred` process flag, default `true`).
- Per-connection HMAC challenge-response (nonce handshake) for the adapter↔agent socket per [§4.7](04_system-components.md#47-runtime-adapter), plus the `lenny_adapter_sopeercred_disabled_total` counter for the nonce-only fallback path.
- Startup-latency benchmark harness in `tests/tier7b_load_kind/scenarios/startup_latency` with a first baseline committed under `tests/tier7b_load_kind/baselines/startup_latency.json`.
- SQLite-dev-mode schema for `make run` use.
- Checkpoint-duration baseline (best-effort; the consistent-quiescence rebaseline is deferred to Phase 8).
- Runtime-author SDKs `runtime-sdk-go`, `lenny-runtime` (Python), and `@lennylabs/runtime-sdk` (TypeScript) per [§15.7](15_external-api-surface.md#157-runtime-author-sdks), built in lockstep with the adapter binary protocol so the SDKs and the protocol ship together.
- `lenny-ctl runtime init` scaffolder per [§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding) covering the Language × Template matrix.
- `lenny image import`, `lenny image list`, and `lenny image rm` subcommands per [§24.19.1](24_lenny-ctl-command-reference.md#24191-image-management); `lenny token print` per [§24.9](24_lenny-ctl-command-reference.md#249-user-and-token-management).
- `CONTRIBUTING.md` published alongside the `make run` quick-start, with the early-development notice required by [§23.2](23_competitive-landscape.md#232-community-adoption-strategy).
- DCO/CLA bot wiring (CI check that every PR commit is signed off per the `CONTRIBUTING.md` policy) per Resolved Decision 14 in [§19](19_resolved-decisions.md).
- `GOVERNANCE.md` initial draft per [§23.2](23_competitive-landscape.md#232-community-adoption-strategy).
- Documented upstream-contribution cadence for `kubernetes-sigs/agent-sandbox` per [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers) (ownership, PR cadence, quarterly dependency review). The fallback `PodLifecycleManager`/`PoolManager` implementations land if the quarterly review fails any of the three go/no-go criteria.
- Calibration of `gateway.maxSessionsPerReplica` and the subsystem extraction thresholds in [§4.1](04_system-components.md#41-edge-gateway-replicas).

**Prerequisites.** Phase 1.5 exit gate. The agent-sandbox dependency health assessment in [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers) must be resolved before Phase 2 begins; see [§18.40](#1840-hard-prerequisite-chain).

**Exit criteria.** The `phase-2-gate` test group passes per [TESTING.md §13.3](../TESTING.md#133-phase-2--adapter-protocol--make-run--imageresolver--startup-benchmark): static, unit, and contract tiers pass; `lenny-compliance --level basic` passes against `cmd/runtimes/echo`; the startup-latency baseline is captured and committed.

### 18.7 Phase 2.5 — Observability foundation, shared rule packages

**Deliverables.**

- `pkg/observability/correlation` carrying the correlation fields (`trace_id`, `span_id`, `session_id`, `task_id`, `tenant_id`, `request_id`, `operation_id`, `agent_name`, `component`, `runtime_class`, `pool`) on a `context.Context` and across HTTP headers and gRPC metadata, including the [§25.1](25_agent-operability.md#251-design-philosophy-and-agent-model) agent-operability headers.
- `pkg/observability/logging` (slog handler enforcing the [§16.4](16_observability.md#164-logging) required keys).
- `pkg/observability/tracing` (otel wrapper with the [§16.3](16_observability.md#163-distributed-tracing) span-name catalog and the error taxonomy `TRANSIENT`, `PERMANENT`, `POLICY`, and `UPSTREAM`).
- `pkg/observability/metrics` (typed Counter, Gauge, and Histogram constructors with the [§16.1.1](16_observability.md#161-metrics) label-hygiene validator).
- `pkg/alerting/rules` (typed `Rule`, PromQL validator, representative alert catalog covering `WarmPoolExhausted`, `PostgresReplicationLagHigh`, `CredentialPoolLow`, `WarmPoolLow`, and `CertExpiryImminent`). Subsequent phases populate the remaining alerts from [§16.5](16_observability.md#165-alerting-rules-and-slos) as the underlying metrics ship.
- `pkg/recommendations/rules` substrate (the typed `Rule` and PromQL validator for the §25 capacity-recommendations engine, shared by the gateway and `lenny-ops`; the rule catalog is populated alongside its consumers).
- Helm chart `ServiceMonitor`, `PodMonitor`, and `PrometheusRule` templates per [§16.9](16_observability.md#169-prometheus-scrape-targets-and-crds), behind the `monitoring.format` selector (`prometheusrule`, `configmap`, or `both`). The CRD-presence preflight check that falls back to `configmap` when the Prometheus Operator is absent ships at the same time.

**Prerequisites.** Phase 2 exit gate.

**Exit criteria.** The `phase-2.5-gate` test group passes per [TESTING.md §13.4](../TESTING.md#134-phase-25--observability-foundation--shared-rule-packages): static, unit, and the observability component subset pass; every observability primitive validates against the [§16](16_observability.md) contracts.

### 18.8 Phase 2.8 — `streaming-echo` runtime

**Deliverables.**

- `cmd/runtimes/streaming-echo` Full-level reference runtime, inheriting the Basic stdin/stdout protocol from `cmd/runtimes/echo` and adding a lifecycle-channel client over a Unix socket. The runtime handles `lifecycle_capabilities`, `lifecycle_support`, `checkpoint_request`, `checkpoint_ready`, `interrupt_request`, `interrupt_acknowledged`, `credentials_rotated`, `deadline_signal`, and `draining`.
- `schemas/lifecycle-events.schema.json` (lifecycle-channel envelope) with example fixtures under `schemas/examples/lifecycle.*.json`.
- `cmd/lenny-compliance --level full` battery (lifecycle handshake, checkpoint quiesce/resume, interrupt acknowledgement, `credentials_rotated`, and `deadline_signal`).

**Prerequisites.** Phase 2.5 exit gate.

**Exit criteria.** `streaming-echo` passes both `lenny-compliance --level basic` (7/7) and `lenny-compliance --level full` (12/12) per [TESTING.md §13.5](../TESTING.md#135-phase-28--streaming-echo-runtime).

### 18.9 Phase 3 — Pool scaling, delegation policy, runtime upgrade, mTLS

**Deliverables.**

- `PoolScalingController` binary that reconciles `SandboxWarmPool.spec` into pod counts using the [§4.6.2](04_system-components.md#46-pod-lifecycle-controllers) sizing formula (standard, mode-adjusted per [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), variant pool, adjusted base pool) plus the cold-start bootstrap fallback. The strategy substrate lives in `pkg/controller/poolscaling/strategy`.
- `DelegationPolicy` CRD plus controller-side validation.
- `RuntimeUpgrade` state machine (`Pending → Expanding → Draining → Contracting → Complete`, with `Paused` as a per-record side-state) per [§10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy). State substrate in `pkg/runtime/upgrade/state`.
- WarmPoolController write path that mirrors `Sandbox` CRD status into the Phase 1.5 `agent_pod_state` Postgres table on every state transition per [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers), including the `lenny_agent_pod_state_mirror_lag_seconds` gauge and the `statusUpdateDeduplicationWindow` controller flag.
- WarmPoolController cluster-CIDR drift detection per NET-022 ([§13.2](13_security-model.md#132-network-isolation)): re-reads cluster pod and service CIDRs every 5 minutes, compares against the rendered `egressCIDRs`, and fires the `NetworkPolicyCIDRDrift` alert on divergence. Phase 14 adds the preflight half (NET-065 cluster-CIDR/IMDS symmetry across all three egress surfaces).
- `SDK-warm circuit breaker` surfaced on `SandboxWarmPool.status.sdkWarmCircuitBreaker.{openedAt,openedReason,minOpenUntil}` per [§4.6.3](04_system-components.md#46-pod-lifecycle-controllers) ownership matrix, with the PSC leader-failover persistence carve-out.
- Nonce-only-mode activation vertical (controller side) per [§4.7](04_system-components.md#47-runtime-adapter): the `Runtime.spec.requireSoPeercred` CRD field with RuntimeReconciler validation (`Registered=False`/`InvalidRuntime` on an embedded runtime setting `requireSoPeercred: false`), the WarmPoolController podspec render of `--require-so-peercred=false` gated on the pool acknowledgment mirrored onto `SandboxTemplate.spec`, and the `SOPeercredDisabled=True` condition on each nonce-only `Sandbox` and `SecurityDegradedMode=True` on the `SandboxTemplate`, both written by the WarmPoolController per the [§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries) ownership table.
- `RuntimeProvider` abstraction per [§5.3](05_runtime-registry-and-pool-model.md#53-isolation-profiles) (extension point for future backends; default implementation wraps `kubernetes-sigs/agent-sandbox`).
- Adapter-side `DemoteSDK` and `ConfigureWorkspace` gRPC stubs per [§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) and [§6.2](06_warm-pod-model.md#62-pod-state-machine); the gateway-side caller wires in Phase 8 alongside checkpoint quiescence.
- mTLS PKI via cert-manager per [§10.3](10_gateway-internals.md#103-mtls-pki), including the documented CA rotation procedure with the 24-hour overlap window.
- `pkg/mtls/spiffe` parsing the SPIFFE URI shapes (`spiffe://<trust-domain>/agent/{pool}/{pod-name}` and `spiffe://<trust-domain>/interceptor/{namespace}/{pod-name}`) with no-userinfo, no-query, and no-fragment constraints, plus `ValidateAgent` and `ValidateInterceptor`.
- `pkg/mtls/denylist` (in-memory, TTL-evicting certificate deny list, goroutine-safe, with opportunistic eviction and explicit `Sweep`), plus cross-replica Redis pub/sub propagation with Postgres `LISTEN/NOTIFY` fallback.
- Gateway startup TLS-enforcement probes for Redis and PgBouncer per [§10.3](10_gateway-internals.md#103-mtls-pki) (`TestRedisTLSEnforcement`, `TestPgBouncerTLSEnforcement`).

**Prerequisites.** Phase 2.8 exit gate. Private container registry with digest-based image references is required before pod creation, per [§5](05_runtime-registry-and-pool-model.md#5-runtime-registry-and-pool-model).

**Exit criteria.** The `phase-3-gate` test group passes per [TESTING.md §13.6](../TESTING.md#136-phase-3--pool-scaling-delegation-policy-runtime-upgrade-mtls): static and unit tiers pass over the new packages. The e2e Kind smoke and the mTLS enforcement tests land alongside the K8s-integration phase.

### 18.10 Phase 3.5 — Admission policies, `lenny-ops` first deploy

**Deliverables.**

- Default-deny NetworkPolicy per [§13.2](13_security-model.md#132-network-isolation).
- gVisor RuntimeClass admission policies (RuntimeClass-aware enforcement per [§17.2](17_deployment-topology.md#172-namespace-layout)), including the integration test that validates gVisor's `SO_PEERCRED` semantics against the Phase 2 self-test.
- Baseline admission webhooks under `templates/admission-policies/`: `lenny-label-immutability`, `lenny-tenant-label-immutability`, `lenny-sandboxclaim-guard`, `lenny-pool-config-validator`, `lenny-crd-conversion`, and `lenny-ephemeral-container-cred-guard`. Each webhook is a thin HTTP shim over its pure-decision package under `pkg/admission/`.
- `pkg/admission/sandboxclaim_guard` (ADR-007 enforcement: CREATE rejection when a non-terminal `SandboxClaim` already exists).
- `pkg/admission/label_immutability` ([§17.2](17_deployment-topology.md#172-namespace-layout) item 5 and [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) NET-003).
- `pkg/admission/tenant_label_immutability` ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) per-tenant pinning enforcement; rejects mutations of `lenny.dev/tenant-id` outside the allowed `unset → {tenant_id} → unassigned` transitions).
- `pkg/admission/ownership` (the [§4.6.3](04_system-components.md#46-pod-lifecycle-controllers) CRD field-ownership matrix).
- `lenny-ops` first deploy: Deployment, Service, Ingress, PodDisruptionBudget, the four NetworkPolicies, the `lenny-backup-sa` ServiceAccount, and the `lenny-ops-leader` Lease per [§17.8.5](17_deployment-topology.md#1785-mandatory-lenny-ops-deployment) and [§25.4](25_agent-operability.md#254-the-lenny-ops-service). The `lenny-ops` runtime surfaces (leader-elected webhook delivery, backup scheduling, reconciliation goroutines, MCP management surface) ship in Phase 13.
- `lenny-deployment-phase-stamp` ConfigMap rendering and the four-layer downgrade enforcement per [§17.2](17_deployment-topology.md#172-namespace-layout): the namespace-scoped append-only ConfigMap, the fail-closed `PHASE_STAMP_FEATURE_FLAG_DOWNGRADE` chart render-time validation, the `lenny-preflight` `PREFLIGHT_PHASE_STAMP_MISMATCH` consistency check, and the `acceptFeatureFlagDowngrade.<flag>` override path. The `AdmissionPlaneFeatureFlagDowngrade` runtime alert ships in Phase 13.
- `lenny-preflight` Job per [§17.6](17_deployment-topology.md#176-packaging-and-installation) with the initial check catalog: admission-webhook inventory, `Phase-stamp consistency`, CRD version annotations, ResourceQuota and LimitRange sizing, ConstraintTemplate/ClusterPolicy inventory, otlp-tls, ops-admin-tls, the NET-064 SPIFFE-trust-domain uniqueness check (enumerates `lenny.dev/spiffe-trust-domain` annotations cluster-wide), and the NET-064 SA-token-audience uniqueness check (enumerates `lenny.dev/sa-token-audience` annotations). The pod-security validator integration and the additional selector-parity audits land in Phase 14.
- `lenny-bootstrap` Job rendered as a `helm.sh/hook: post-install,post-upgrade` resource that runs `lenny-ctl bootstrap --from-values` against the rendered values.
- `lenny-ctl preflight` standalone CLI (the in-cluster Job equivalent for pre-Sync GitOps gating per [§24.2](24_lenny-ctl-command-reference.md#242-preflight)).
- `lenny-ctl doctor` and `doctor --fix` diagnostic CLI per [§24.2](24_lenny-ctl-command-reference.md#242-preflight). The CLI binary ships here; the diagnostic endpoints it queries (`GET /v1/admin/diagnostics/*`) ship in Phase 13, so prior to Phase 13 the CLI surfaces the chart-rendered static checks (admission inventory, preflight phase-stamp) and returns "endpoint not yet available" for runtime diagnostics.
- ResourceQuota and LimitRange tier defaults rendered into the chart for `agentNamespaces` per [§17.2](17_deployment-topology.md#172-namespace-layout) and [§17.8](17_deployment-topology.md#178-capacity-planning-and-defaults).
- mTLS end-to-end verification on Kind.

**Prerequisites.** Phase 3 exit gate.

**Exit criteria.** The `phase-3.5-gate` test group passes per [TESTING.md §13.7](../TESTING.md#137-phase-35--admission-policies--lenny-ops-first-deploy): static and unit tiers pass over the admission-decision packages. The phase-stamp render-time and preflight enforcement layers reject every documented downgrade case. The e2e-Kind admission and chaos suites land alongside the K8s-integration phase.

### 18.11 Phase 4 — Session manager, REST

**Deliverables.**

- `pkg/api/v1/session` (the `State` enum and `IsTerminal`, the [§7.1](07_session-lifecycle.md#71-normal-flow) `FailureClass` enum, and the [§15.1](15_external-api-surface.md#151-rest-api) state-mutating endpoint precondition table as a Go-callable `Validate` function).
- Pre-running state-lifetime watchdogs per [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation) and [§6.2](06_warm-pod-model.md#62-pod-state-machine): the four goroutines driven by `maxCreatedStateTimeoutSeconds`, `maxFinalizingTimeoutSeconds`, `maxReadyTimeoutSeconds`, and `maxStartingTimeoutSeconds` that force `created`, `finalizing`, `ready`, and `starting` to the `failed` state with explicit reasons (`CREATED_TIMEOUT`, `FINALIZE_TIMEOUT`, `READY_TIMEOUT`, `STARTING_TIMEOUT`) so abandoned setup flows cannot indefinitely pin a warm pod.
- `pkg/gateway/sessionstore` (the [§4.2](04_system-components.md#42-session-manager) `SessionStore` interface with strict tenant isolation; cross-tenant reads return `ErrNotFound`).
- `pkg/gateway/sessionstore/memstore` (in-memory implementation behind the same interface; the Postgres-backed implementation swaps in behind it in a later phase).
- `StoreRouter` interface per [§12.6](12_storage-architecture.md#126-interface-design) build-phase table (request-scoped routing of every gateway call to a `tenant_id`-pinned store handle).
- `pkg/gateway/sessionserver` ([§15.1](15_external-api-surface.md#151-rest-api) REST handler for `POST /v1/sessions`, `GET /v1/sessions/{id}`, `GET /v1/sessions`, `DELETE /v1/sessions/{id}`, and the five state-mutating endpoints `finalize`, `start`, `interrupt`, `terminate`, and `resume`).
- `POST /v1/sessions/{id}/derive` endpoint per [§7.1](07_session-lifecycle.md#71-normal-flow): per-source advisory lock (`derive_lock:{source_session_id}`), workspace snapshot copy, `allowIsolationDowngrade` admin gate, monotonicity check, and the `persistDeriveFailureRows` audit-only `created → failed` write.
- `GET /v1/sessions/{id}/workspace` workspace-snapshot tarball endpoint per [§15.1](15_external-api-surface.md#151-rest-api).
- `GET /v1/blobs/{ref}` blob-dereference endpoint per [§15.1](15_external-api-surface.md#151-rest-api) (load-bearing for every MessagePart `ref` payload above the inline-size threshold).
- Session introspection endpoints `GET /v1/sessions/{id}/transcript`, `GET /v1/sessions/{id}/tree`, `GET /v1/sessions/{id}/webhook-events`, and `POST /v1/sessions/{id}/extend-retention` per [§15.1](15_external-api-surface.md#151-rest-api).
- Upload Handler subsystem in the gateway per [§7.4](07_session-lifecycle.md#74-upload-safety) and [§13.4](13_security-model.md#134-upload-security): goroutine pool, circuit breaker, staging→validation→promotion pipeline, archive limits (256 MiB, 100:1 compression ratio, 10 000 entries, 64 MiB per entry, depth 32, 4096-byte path), symlink and device-entry rejection, post-promotion symlink re-validation, and the ten `UPLOAD_ARCHIVE_*` error subcodes.
- `uploadToken` HMAC issuance, validation, single-use invalidation, the automatic 24-hour key rotation timer, and the 5-minute key-overlap window per [§7.1](07_session-lifecycle.md#71-normal-flow). The operator-initiated rotation admin surface ships in Phase 13.
- `POST /v1/sessions/{id}/upload` and `POST /v1/sessions/{id}/upload-archive` request handlers per [§15.1](15_external-api-surface.md#151-rest-api).
- Workspace plan source-type validators (`inlineFile`, `uploadFile`, `uploadArchive`, `mkdir`, `gitClone`) per [§14](14_workspace-plan-schema.md): mode-string validator, path-traversal rejection, setuid/setgid rejection, sticky-on-file rejection, archive-bomb guard, and the `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` consumer-side migration handling.
- Callback URL SSRF mitigations per [§14](14_workspace-plan-schema.md): HTTPS-only, DNS pinning (cached resolution applied to `DialContext`), RFC1918 and cloud-metadata blocklist, dedicated callback goroutine pool, HMAC-signed CloudEvents webhooks with replay-window enforcement, and KMS-encrypted `callbackSecret` storage.
- `cmd/lenny-gateway` minimal gateway binary that boots the handler against `memstore`, with no OIDC, Postgres, or Kubernetes dependencies, and reads the tenant from `X-Lenny-Tenant-ID` (defaulting to `default` per [§10.2](10_gateway-internals.md#102-authentication) single-tenant mode).
- `CoordinatorFence` gRPC and `coordination_generation` CAS write per [§10.1](10_gateway-internals.md#101-horizontal-scaling) (5 s deadline; the `AdapterTerminating` gRPC message; the `agent_pod_state` mirror-staleness gauge consumer; the `lenny_orphan_session_reconciliations_total` counter).
- Postgres-backed fallback claim path per [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers): when API-server-based claim exhausts `podClaimQueueTimeout`, the gateway reads the `agent_pod_state` mirror under the two preconditions (`lenny_agent_pod_state_mirror_lag_seconds` below threshold and the admission-webhook reachability probe). Emits `lenny_pod_claim_fallback_total` and `lenny_pod_claim_fallback_skipped_total`.
- Callback webhook delivery worker per [§7.1](07_session-lifecycle.md#71-normal-flow) and [§14](14_workspace-plan-schema.md): goroutine pool with exponential backoff retry, per-attempt timeout, a dead-letter path that records to `session_dlq_archive`-style storage for the webhook surface, and the receiving-side replay-window verification contract that webhook consumers must implement.

**Prerequisites.** Phase 3.5 exit gate. The `agent_pod_state` mirror (Phase 1.5 + 3) must be writable before the fallback claim path activates; see [§18.40](#1840-hard-prerequisite-chain).

**Exit criteria.** Tier 3 contract tests in `tests/tier3_contract/rest_sessions/` pass against the handler driven by `httptest`. The integration tests (compose stack, real Postgres, MinIO, upload pipeline) listed as deferred in [TESTING.md §13.8](../TESTING.md#138-phase-4--session-manager--rest) land in subsequent phases.

### 18.12 Phase 4.5 — Admin API foundation, authentication, bootstrap

**Deliverables.**

- Admin API surface for runtimes, pools, connectors, policies, tenants, and users per [§15.1](15_external-api-surface.md#151-rest-api).
- `lenny-ctl bootstrap` seed command per [§24.1](24_lenny-ctl-command-reference.md#241-bootstrap), plus `lenny-ctl admin runtimes register`, `lenny-ctl admin tenants *`, `lenny-ctl admin users *` per [§24.9](24_lenny-ctl-command-reference.md#249-user-and-token-management) and [§24.10](24_lenny-ctl-command-reference.md#2410-tenant-management).
- `pkg/auth` (the `TokenType` enum `user_bearer`, `session_capability`, `a2a_delegation`, and `service_token`; the `Role` enum `platform-admin`, `tenant-admin`, `tenant-viewer`, `billing-viewer`, and `user`; `ValidateTenantID` enforcing the `^[a-zA-Z0-9_-]{1,128}$` format; and `ExtractTenant` implementing the [§10.2](10_gateway-internals.md#102-authentication) single-tenant and multi-tenant claim-extraction state machine).
- OIDC/OAuth 2.1 JWT validation integrated into the gateway request path.
- `tenant_id` claim propagation into the gateway's correlation context.
- `UserStateStore` integration and the `noEnvironmentPolicy` default (`deny-all`) validation.
- `lenny-noenvironmentpolicy-audit` interceptor and the gateway startup config-key matrix per [§10.3](10_gateway-internals.md#103-mtls-pki) and [§10.6](10_gateway-internals.md#106-environment-resource-and-rbac-model): startup-refusal on missing required keys, the `LENNY_CONFIG_MISSING` log code, and the `lenny_noenvironmentpolicy_allowall_total` counter.
- `ConnectorDefinition` admin resource per [§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc): `POST /v1/admin/connectors`, `GET`, `PUT`, `DELETE`, `pkg/connector` package, secret-ref resolver, and the registration-time validators.
- OAuth/OIDC `state` and `code_verifier` storage with TTL in Redis per [§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc), plus the `/v1/oauth/callback` handler.
- `lenny-ctl policy audit-isolation` subcommand per [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease).
- Nonce-only-mode acknowledgment gate (gateway side) per [§4.7](04_system-components.md#47-runtime-adapter): the `acknowledgeNonceOnlyAuth` pool configuration flag ([§5.3](05_runtime-registry-and-pool-model.md#53-isolation-profiles)) with the pool admission rejection at create and at update, plus the runtime registry column and CRD-to-store mirror of `requireSoPeercred` the gate evaluates.

**Prerequisites.** Phase 4 exit gate.

**Exit criteria.** The unit and component suites in [TESTING.md §13.9](../TESTING.md#139-phase-45--admin-api-foundation--authentication--bootstrap) pass. The Kind-tier bootstrap test and the OIDC mock-driven integration tests land alongside the K8s-integration phase.

### 18.13 Phase 5 — ExternalAdapterRegistry, MCP/Completions/Open Responses

**Deliverables.**

- `ExternalAdapterRegistry` per [§4.7](04_system-components.md#47-runtime-adapter).
- Native translators for MCP, OpenAI Completions, and OpenAI Open Responses.
- REST/MCP overlapping operations per the fidelity matrix in [§15.2](15_external-api-surface.md#152-mcp-api).
- `type: mcp` runtime gateway-side endpoints (request routing, response translation, lifecycle event forwarding). The runtime-side adapter path and the reference `type: mcp` runtime ship in Phase 12b.
- OpenAPI 3.1 document served at `GET /openapi.yaml` and `GET /v1/openapi.json` per [§15.1](15_external-api-surface.md#151-rest-api).
- OpenAPI generator that emits the document from Go type definitions at build time, plus the CI gate that fails the build when (a) the committed document is out of sync, or (b) any admin-API endpoint lacks the `x-lenny-mcp-tool`, `x-lenny-scope`, `x-lenny-required-role`, or `x-lenny-category` extension.
- `RegisterAdapterUnderTest` contract-test harness per [§15.2.1](15_external-api-surface.md#152-mcp-api) and the `POST /v1/admin/external-adapters/{name}/validate` registration gate.
- `gitClone` workspace-plan source materializer per [§14](14_workspace-plan-schema.md) with `ls-remote` ref pinning and credential-lease bridging (HTTPS-only; SSH and `git://` rejected at schema validation; `resolvedCommitSha` written gateway-side only).

**Prerequisites.** Phase 4.5 exit gate.

**Exit criteria.** Contract suites in `tests/tier3_contract/rest_mcp/`, `tests/tier3_contract/rest_openai_completions/`, and `tests/tier3_contract/rest_openai_responses/` pass per [TESTING.md §13.10](../TESTING.md#1310-phase-5--externaladapterregistry--mcpcompletionsopen-responses--restmcp-contract-tests). Tier 2 component tests for `openai_translator` and `anthropic_translator` pass.

### 18.14 Phase 5.4 — etcd encryption at rest

**Deliverables.**

- Helm chart `etcdEncryption.enabled: true` default and the underlying EncryptionConfiguration manifest.
- Documented operator procedure for key rotation per [§13.1](13_security-model.md#131-pod-security).

**Prerequisites.** Phase 5 exit gate.

**Exit criteria.** The Kind-tier `etcd_encryption_test` passes per [TESTING.md §13.11](../TESTING.md#1311-phase-54--etcd-encryption-at-rest): a Kubernetes Secret is written, `etcdctl get` on the raw key returns ciphertext, and the chart-template unit test asserts the default. **Hard prerequisite for Phase 5.5**; see [§18.40](#1840-hard-prerequisite-chain).

### 18.15 Phase 5.5 — Basic credential leasing, Token Service

**Deliverables.**

- `pkg/credential` (the closed enums `Provider`, `AssignmentStrategy`, `LeaseSource`, and `RotationTrigger`; rotation-trigger semantics `IsFaultTriggered`, `IsCeilingApplicable` enforcing the [§4.7](04_system-components.md#47-runtime-adapter) 300-second in-flight gate rule, and `CountsAgainstRotationBudget` enforcing the proactive-renewal exclusion from `maxRotationsPerSession`; and `PoolConfig.Validate` rejecting malformed admin-path `CredentialPool` definitions).
- Token Service binary (initial form) implementing `AssignCredentials` and `RevokeCredentials` per [§4.9](04_system-components.md#49-credential-leasing-service), backed by the K8s Secrets store on the agent pod and Postgres-resident lease state in the control plane.
- Pre-claim provider-intersection check per [§4.9](04_system-components.md#49-credential-leasing-service): the gateway computes the intersection of Runtime `supportedProviders` and tenant `credentialPolicy.providerPools` before claiming a pod and rejects with `CREDENTIAL_POOL_EXHAUSTED` when no provider has an available credential.
- `POST /v1/oauth/token` Token Service endpoint per [§13.3](13_security-model.md#133-credential-flow): the single canonical RFC 8693 token-minting surface, with the write-before-issue Postgres transaction, the `issued_tokens` table (`TokenIssuanceStore` interface), the in-memory revocation cache, the `token_revoked` 401 path, and per-endpoint rate limiting. The KMS envelope wrapping ships in Phase 12a.
- `CredentialGenerator` pluggable interface per [§12.6](12_storage-architecture.md#126-interface-design) build-phase table; default in-process implementation issues OIDC and service tokens.
- `/v1/credentials`, `/v1/credentials/{ref}`, and `/v1/credentials/{ref}/revoke` end-user credential REST endpoints per [§15.1](15_external-api-surface.md#151-rest-api), backed by `UserStateStore`.
- Initial Postgres schema migration for the lease, credential-pool, and `issued_tokens` tables.
- Sandbox lease lifecycle hooks for credential assignment and revocation.

**Prerequisites.** Phase 5.4 exit gate (etcd encryption at rest is verified before any credential lease is written). Phase 5 exit gate.

**Exit criteria.** Unit and contract tests for the `pkg/credential` arithmetic pass per [TESTING.md §13.12](../TESTING.md#1312-phase-55--basic-credential-leasing--token-service). The KMS envelope encryption and the OAuth connector flow ship in Phase 12a.

### 18.16 Phase 5.6 — Targeted security design review (credential)

**Deliverables.**

- Documented findings in `tests/tier9_security/reviews/credential-review.md` covering the credential subsystem shipped in Phase 5.5.
- Tracked remediation items linked to commits.

**Prerequisites.** Phase 5.5 exit gate.

**Exit criteria.** The review is recorded per [TESTING.md §13.13](../TESTING.md#1313-phase-56--targeted-security-design-review-credential). This phase produces no application-code deliverables of its own; remediation items, if any, land in subsequent phases.

### 18.17 Phase 5.75 — Minimum viable policy enforcement

**Deliverables.**

- `pkg/quota` (the `ResetPeriod` enum; soft-warning (80%) and hard-limit (100%) threshold tests; the global-tenant-user `Hierarchy` validator and `HierarchicalCheck` resolver; the [§11.2](11_policy-and-controls.md#112-budgets-and-quotas) `FailOpenCeiling` formula `min(tenant_limit / replicas, per_replica_hard_cap)`; the `PerUserFailOpenCeiling` companion; the `MaxOvershoot` formula; and the `ReconcileMax` rule for Redis-recovery counter reconciliation).
- `AuthEvaluator` interceptor active on the gateway request path.
- `QuotaEvaluator` interceptor active on the gateway request path.

**Prerequisites.** Phase 5.6 exit gate.

**Exit criteria.** Unit suites for the quota arithmetic and the interceptor wiring pass per [TESTING.md §13.14](../TESTING.md#1314-phase-575--minimum-viable-policy-enforcement). **Hard prerequisite for Phase 6** real-credential testing; see [§18.40](#1840-hard-prerequisite-chain). The `storage_quota_reserve.lua` Redis script, the per-replica fail-open accounting goroutine, the cumulative fail-open timer, and the [§11.2.1](11_policy-and-controls.md#112-budgets-and-quotas) billing event emitter ship in Phase 7.

### 18.18 Phase 5.8 — LLM Proxy, `lenny-direct-mode-isolation` admission webhook

**Deliverables.**

- LLM Proxy subsystem inside the gateway binary per [§4.9](04_system-components.md#49-credential-leasing-service) and [§17.1](17_deployment-topology.md#171-kubernetes-resources) ("Single-container pod" note).
- Native Go translator for `anthropic_direct` per [§4](04_system-components.md#4-system-components) ("Supported upstream providers").
- SSE relay implementation for streaming responses.
- Circuit breaker around the upstream LLM provider.
- Lease-token validation on every proxy request.
- `lenny-direct-mode-isolation` admission webhook per [§6.2](06_warm-pod-model.md#62-pod-state-machine), [§13.2](13_security-model.md#132-network-isolation), and [§4.9](04_system-components.md#49-credential-leasing-service): rejects (a) `deliveryMode: direct` combined with `isolationProfile: standard`, and (b) `deliveryMode: proxy` combined with `spiffeBinding: disabled` when `tenancy.mode: multi`, unless `allowProxyModeSpiffeBindingDisabled` is set.
- Helm feature flag `features.llmProxy` enabled (rendered as `true` for Phase 5.8+ deployments).
- Phase-stamp ConfigMap entry recorded per [§17.2](17_deployment-topology.md#172-namespace-layout) downgrade-enforcement layer.

**Prerequisites.** Phase 5.75 exit gate. The Phase 3.5 phase-stamp ConfigMap and the four-layer downgrade enforcement must be in place before the `features.llmProxy` flag flips from `false` to `true`; see [§18.40](#1840-hard-prerequisite-chain).

**Exit criteria.** Tier 2 component tests for `llm_proxy` pass, Tier 3 contract tests against the Anthropic mock pass, and the Kind-tier `llm_proxy_proxy_mode_test` and `admission_direct_mode_isolation_test` pass per [TESTING.md §13.15](../TESTING.md#1315-phase-58--llm-proxy--lenny-direct-mode-isolation-admission-webhook).

### 18.19 Phase 6 — Interactive sessions, SDKs

**Deliverables.**

- Interactive sessions: the `input_required` state in the session manager, message injection, and the elicitation surface per [§7.2](07_session-lifecycle.md#72-interactive-session-model).
- Tool-use and elicitation REST endpoints per [§15.1](15_external-api-surface.md#151-rest-api): `POST /v1/sessions/{id}/tool-use/{tool_use_id}/approve`, `POST /v1/sessions/{id}/tool-use/{tool_use_id}/deny`, `POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond`, `POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss`. Each handler validates the `(session_id, user_id, elicitation_id|tool_use_id)` authorization triple.
- Session inbox subsystem per [§7.2](07_session-lifecycle.md#72-interactive-session-model): the seven delivery paths, in-memory inbox, Redis-list durable inbox at `t:{tenant_id}:session:{session_id}:inbox`, `ForwardMessage` cross-replica gRPC, drain-on-resume, and the `session_dlq_archive` Postgres table for DLQ entries with TTL.
- `POST /v1/sessions/{id}/messages` and `POST /v1/sessions/{id}/replay` REST handlers per [§15.1](15_external-api-surface.md#151-rest-api). `replay` validates `replayMode`, `allowIsolationDowngrade`, and `targetRuntime`.
- Streaming reconnect with cursor.
- Go client SDK per [§15.6](15_external-api-surface.md#156-client-sdks).
- TypeScript client SDK per [§15.6](15_external-api-surface.md#156-client-sdks).
- Python client SDK per [§15.6](15_external-api-surface.md#156-client-sdks).

**Prerequisites.** Phase 5.75 exit gate (real-credential testing requires the minimum-viable policy interceptors). Phase 5.8 exit gate (the LLM Proxy is the surface clients call in proxy mode).

**Exit criteria.** The streaming-reconnect integration suite passes per [TESTING.md §13.16](../TESTING.md#1316-phase-6--interactive-sessions--sdks). The full language-SDK test surface comes online per [§14.13](../TESTING.md#1413-language-sdks).

### 18.20 Phase 6.5 — Incremental load test (streaming)

**Deliverables.**

- Streaming-reconnect load scenario in `tests/tier7b_load_kind/scenarios/streaming_reconnect.go`.
- Phase 6.5 baseline JSON committed under `tests/tier7b_load_kind/baselines/`.

**Prerequisites.** Phase 6 exit gate.

**Exit criteria.** Tier 7 streaming-reconnect baseline is captured and reviewed per [TESTING.md §13.17](../TESTING.md#1317-phase-65--incremental-load-test-streaming).

### 18.21 Phase 7 — Policy engine (quotas, budgets, audit hooks)

**Deliverables.**

- `pkg/circuitbreaker` ([§11.6](11_policy-and-controls.md#116-circuit-breakers) operator-managed circuit-breaker admission logic; the `State` enum `open` and `closed`; the `LimitTier` enum `runtime`, `pool`, `connector`, and `operation_type`; the `OperationType` enum `uploads`, `delegation_depth`, `session_creation`, and `message_injection`; per-tier `Scope` validator; `Match` keying on the (`LimitTier`, `Scope`) pair; `FirstMatch` and `ScopeMatches` enforcing the immutable-scope invariant when an operator reopens a previously-existing breaker).
- `pkg/idempotency` ([§11.5](11_policy-and-controls.md#115-idempotency) primitives: 128-character `MaxKeyLength` cap, 24-hour `TTL`, tenant-scoped `Key.Validate` returning `*KeyTooLongError`, `HashBody` returning a 64-character SHA-256 hex digest, and `DetectReuse` returning `store_new`, `replay`, or `reject` with `*ReuseError` carrying the canonical `IDEMPOTENCY_KEY_REUSED` 422 envelope).
- Quota enforcement at the gateway: the `storage_quota_reserve.lua` Redis script, the per-replica fail-open accounting goroutine, the cumulative fail-open timer (`quotaFailOpenCumulativeMaxSeconds`), and the [§11.2.1](11_policy-and-controls.md#112-budgets-and-quotas) billing event emitter.
- User invalidation per [§11.4](11_policy-and-controls.md#114-user-invalidation): the three-level admin endpoint `POST /v1/admin/users/{user_id}/invalidate` with `soft_disable`, `hard_disable`, and `full_revoke` modes; session-termination RPC fan-out; issued-token revocation in the durable Postgres issued-token index with in-memory revocation-cache push and cross-replica fan-out; lease revocation; and elicitation dismissal. (The `full_revoke` KMS-backed end-to-end wiring lands in Phase 12a alongside Token Service KMS hardening; Phase 7 ships the request path and the soft/hard tiers.)
- Interceptor `failPolicy` weakening cooldown per [§11.2.1](11_policy-and-controls.md#112-budgets-and-quotas) and SEC-013 ([§8.2](08_recursive-delegation.md#82-delegation-mechanism)): `gateway.interceptorWeakeningCooldownSeconds`, `INTERCEPTOR_WEAKENING_COOLDOWN` rejection during cooldown, the `delegation_policy.export_scan_weakened`/`strengthened` ratchet emission, and the SEC-013 admin-immutable `transition_ts` field protection (server-minted, not admin-API-writable).
- External interceptor registration framework per [§4.8](04_system-components.md#48-gateway-policy-engine) and [§22.3](22_explicit-non-decisions.md): the `InterceptorService` gRPC contract that deployer-supplied interceptors implement, the registration-time validators (`INVALID_INTERCEPTOR_PRIORITY`, `INVALID_INTERCEPTOR_PHASE`), the priority-range admission gate, `interceptorFailOpenMaxConsecutive` cumulative-failure escalation, and the `interceptor.fail_open_escalated`/`interceptor.fail_open_restored` audit emissions.
- Audit hooks plumbing per [§11.7](11_policy-and-controls.md#117-audit-logging) (the audit-log writer itself ships in Phase 13).
- `GET /v1/usage` and `GET /v1/metering/events` REST endpoints per [§15.1](15_external-api-surface.md#151-rest-api), backed by the billing event emitter.
- Admin API handlers for circuit breaker open and close, plus `lenny-ctl circuit-breakers list/open/close` per [§24.7](24_lenny-ctl-command-reference.md#247-circuit-breakers) and `lenny-ctl quota reconcile` per [§24.6](24_lenny-ctl-command-reference.md#246-quota-operations).
- Redis-backed circuit-breaker cache with pub/sub propagation and the 5-second cache-stale fallback path.

**Prerequisites.** Phase 6.5 exit gate.

**Exit criteria.** Tier 2 component tests for `pkg/circuitbreaker` and `pkg/idempotency` pass per [TESTING.md §13.18](../TESTING.md#1318-phase-7--policy-engine-quotas-budgets-audit-hooks). The Tier 4 integration tests for quota enforcement and audit pipelines land alongside the K8s-integration phase that ships the gateway interceptors against real Redis and Postgres.

### 18.22 Phase 8 — Checkpoint/resume, drain-readiness webhook

**Deliverables.**

- `pkg/checkpoint` (the `Level` enum `basic`, `standard`, `full`, and `embedded` with the `ConsistencyForLevel` mapping; the `Trigger` enum `periodic`, `pre_scale_down`, and `eviction` with `IsEviction` and `RetryBudgetFor` returning the per-trigger budget (200ms/5s/5s for non-eviction; 500ms/5s/30s for eviction); the `Outcome` enum `success`, `failed`, `aborted`, and `partial`; the `ResumeMode` enum `full`, `partial_workspace`, `conversation_only`, and `coordinator_handoff` with the `WorkspaceLost` mapping; the `CheckpointTimeout` 60-second constant; the `WorkspaceSizePreCheck` from [§4.4](04_system-components.md#44-event--checkpoint-store); and the `FreshnessCheck` with the [§16.5](16_observability.md#165-alerting-rules-and-slos) `CheckpointStale` alert condition).
- Checkpoint-and-resume orchestration in the gateway: eviction → checkpoint → resume on a new pod.
- `CheckpointBarrier` and `CheckpointBarrierAck` protocol per [§10.1](10_gateway-internals.md#101-horizontal-scaling): `checkpointBarrierAckTimeoutSeconds`, intent-row INSERT before partial-manifest upload, `chunk_encoding`, supersede-on-write reconciliation, `partial_manifest_active_uniq` Postgres index, `lenny_checkpoint_partial_*` metrics, `lenny_prestop_cap_selection_total`, and the `PreStopCapFallbackRateHigh` alert.
- Gateway-side wire-in of the Phase 3 adapter `DemoteSDK` and `ConfigureWorkspace` RPCs for the SDK-warm transition path.
- SIGSTOP/SIGCONT embedded-adapter path per [§4.4](04_system-components.md#44-event--checkpoint-store).
- Eviction-fallback Postgres minimal-state record per [§4.4](04_system-components.md#44-event--checkpoint-store).
- Gateway-side session-deadline emitter per [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation): the 5-minute pre-expiry timer that fires before `maxSessionAge`, the `session_expiring_soon` event sent to the client, and the `DEADLINE_APPROACHING` lifecycle-channel signal dispatched to the runtime (`streaming-echo` handled the runtime side in Phase 2.8; the gateway emitter lands here).
- `lenny-drain-readiness` admission webhook (pre-drain MinIO health check per [§12.5](12_storage-architecture.md#125-artifact-store) and [§13.2](13_security-model.md#132-network-isolation) NET-037).
- Helm feature flag `features.drainReadiness` enabled and the corresponding phase-stamp entry.
- Checkpoint-duration baseline re-captured with cooperative quiescence overhead.

**Prerequisites.** Phase 7 exit gate. The Phase 3.5 phase-stamp ConfigMap and the four-layer downgrade enforcement must be in place before the `features.drainReadiness` flag flips from `false` to `true`; see [§18.40](#1840-hard-prerequisite-chain).

**Exit criteria.** Tier 4 `checkpoint_resume_test`, Tier 5 `drain_readiness_webhook_test`, and Tier 8 `minio_outage_during_checkpoint_test` and `node_drain_during_minio_outage_test` pass per [TESTING.md §13.19](../TESTING.md#1319-phase-8--checkpointresume--drain-readiness-webhook).

### 18.23 Phase 9 — Delegation, `delegation-echo`

**Deliverables.**

- `pkg/delegation/cycle` ([§8.2](08_recursive-delegation.md#82-delegation-mechanism) cycle-detection decision matrix: the `Mode` enum `enforce`, `warn`, and `permissive`; the `Layer` enum `platform`, `runtime`, and `policy`; the `Identity` tuple with pool-differentiated equality; the `Lineage` chain with `Contains` and `Depth`; the `Decide` function implementing the three-layer AND gate; decisions carrying `BlockedBy` and the full `WouldHaveBlockedLayers` set; and `ToError` producing the `*Rejection` typed error returned as `DELEGATION_CYCLE_DETECTED`).
- `pkg/delegation/lease` ([§8.2](08_recursive-delegation.md#82-delegation-mechanism) `LeaseSlice` arithmetic with `ValidateChildSlice`; the [§8.2 bis](08_recursive-delegation.md#82-delegation-mechanism) `maxDepth` precedence chain with `ResolveMaxDepth`, `EnforcePolicyCeiling`, and `CheckDepth`).
- `cmd/runtimes/delegation-echo` Standard-level reference runtime that exercises `lenny/delegate_task` through the platform MCP server.
- `cmd/lenny-compliance --level standard` battery.
- The platform MCP tool surface per [§8.5](08_recursive-delegation.md#85-delegation-tools): `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/get_task_tree`, `lenny/output`, `lenny/request_input`, `lenny/send_message`, and `lenny/set_tracing_context` gateway handlers. `lenny/request_elicitation`, `lenny/memory_write`, and `lenny/memory_query` are deferred to Phase 10 and Phase 17b respectively.
- `lenny/request_input` timeout resolution per [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation): when the `maxRequestInputWaitSeconds` timer fires, the gateway resolves the blocked tool call with the `REQUEST_INPUT_TIMEOUT` error and emits a `request_input_expired` event on the parent's `lenny/await_children` stream so the parent agent can react.
- `lenny/send_message` MCP gateway handler details per [§7.2](07_session-lifecycle.md#72-interactive-session-model): the three-level `messagingScope` resolver (`session`, `task_tree`, `tenant`), the `CROSS_TENANT_MESSAGE_DENIED` validator, and inbound/outbound `messagingRateLimit` enforcement.
- `lenny/set_tracing_context` validators per [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) and [§9.1](09_mcp-integration.md#91-where-mcp-is-used): `TRACING_CONTEXT_TOO_LARGE`, `TRACING_CONTEXT_SENSITIVE_KEY`, `TRACING_CONTEXT_URL_NOT_ALLOWED`, and propagation through delegation lineage.
- `treeVisibility` enforcement per [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) and [§8.5](08_recursive-delegation.md#85-delegation-tools): the three-mode `full` / `parent-and-self` / `self-only` scoping, the `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` gate on `lenny/delegate_task` and `lenny/send_message`, and the tree-view filter applied to `lenny/get_task_tree` responses.
- Approval-mode registration-time validation and runtime enforcement per [§8.4](08_recursive-delegation.md#84-approval-modes): the `policy` mode (auto-approve against lease constraints) and `deny` mode (reject delegation) are evaluated in the `PreDelegation` interceptor; the `approval` mode is post-v1.
- Lease extension control plane per [§8.6](08_recursive-delegation.md#86-lease-extension): adapter↔gateway `ExtendLease` gRPC, `auto` and `elicitation` modes, serialized-per-tree elicitation queue, `rejectionCoolOffSeconds` cool-off timer, `autoModeRateLimit` budget, persistent `extension-denied` flag in the new `delegation_tree_budget` Postgres table, and the `DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial` admin endpoint. Extension responses carry the `GRANTED`, `PARTIALLY_GRANTED`, `CEILING_REACHED`, or `REJECTED` status taxonomy.
- Delegation-tree recovery per [§8.10](08_recursive-delegation.md#810-delegation-tree-recovery): the `session_tree_archive` Postgres table, bottom-up reattach traversal, `maxTreeRecoverySeconds` and `maxLevelRecoverySeconds` deadlines, the `children_reattached` event, the `cascadeOnFailure` policy, and the orphan-cleanup Job bounded by `maxOrphanTasksPerTenant`.
- Subtree deadlock detector per [§8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema): `deadlock_detected` event, `DEADLOCK_TIMEOUT` error code, and the `maxDeadlockWaitSeconds` timer.
- `PreDelegation`, `PreMessageDelivery`, and `PreExportMaterialization` interceptor phases per [§11.2.1](11_policy-and-controls.md#112-budgets-and-quotas), [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease), and [§8.7](08_recursive-delegation.md#87-file-export-model). `PreExportMaterialization` enforces `DelegationPolicy.contentPolicy.scanExportedFiles` with the `EXPORT_FILE_SCAN_REJECTED` rejection, and emits the `delegation_policy.export_scan_weakened` audit event per [§13.5](13_security-model.md#135-delegation-chain-content-security).
- `ClusterRegistry` and `EventBus` storage-extension interfaces per [§12.6](12_storage-architecture.md#126-interface-design) build-phase table.
- `gateway.cycle_detection_mode_changed` audit emission alongside the `pkg/delegation/cycle` mode switches.
- Redis-backed delegation budget counters (`budget_reserve.lua` and `budget_return.lua`).
- `lenny-ctl policy audit-isolation` pool-registration audit per [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) and the `pool.isolation_warning` proactive audit event emission.

**Prerequisites.** Phase 8 exit gate. The Phase 5.5 `POST /v1/oauth/token` endpoint and `TokenIssuanceStore` are required for child-token minting at every delegation hop; see [§18.40](#1840-hard-prerequisite-chain).

**Exit criteria.** Tier 4 `delegation_test` passes per [TESTING.md §13.20](../TESTING.md#1320-phase-9--delegation--delegation-echo).

### 18.24 Phase 9.5 — Incremental load test (delegation)

**Deliverables.**

- Delegation fan-out load scenario in `tests/tier7b_load_kind/scenarios/delegation_fanout.go`.
- Phase 9.5 baseline JSON committed under `tests/tier7b_load_kind/baselines/`.

**Prerequisites.** Phase 9 exit gate.

**Exit criteria.** Tier 7 delegation fan-out baseline is captured and reviewed per [TESTING.md §13.21](../TESTING.md#1321-phase-95--incremental-load-test-delegation).

### 18.25 Phase 10 — MCP fabric (virtual child interfaces, elicitation chain)

**Deliverables.**

- Virtual MCP server on the gateway per [§9.1](09_mcp-integration.md#91-where-mcp-is-used).
- `pkg/elicitation` ([§9.2](09_mcp-integration.md#92-elicitation-chain) primitives: the `EnforcementMode` enum `off < detect-only < enforce` with `ResolveEffective` platform-floor clamp; the `DepthPolicy` enum `allow_all`, `suppress_at_depth`, and `block_all` with `ShouldSuppress` honouring the connector-exempt rule; the `InitiatorType` enum `connector` and `agent`; the canonicalised SHA-256 `Content.Digest` and `VerifyContent` implementing the gateway-origin-binding tamper detector; and `Provenance.Validate` enforcing the metadata `origin_pod`, connector `connector_id`, and depth ≥ 0 constraints).
- Hop-by-hop elicitation chain across the parent and child task tree.
- `lenny/request_elicitation` MCP gateway handler per [§8.5](08_recursive-delegation.md#85-delegation-tools) and [§9.2](09_mcp-integration.md#92-elicitation-chain): adapter-initiated elicitation requests are validated against the elicitation enforcement mode and forwarded up the chain.
- `respond_to_elicitation` gateway endpoint validating the `(session_id, user_id, elicitation_id)` authorization triple. The companion `dismiss_elicitation` endpoint shipped in Phase 6 is integrated with the elicitation chain here so dismissal events propagate up the lineage.
- `maxElicitationWait` and 30-second hop-forwarding timers in the gateway.
- URL-mode elicitation allowlist enforcement (per-pool `domainAllowlist`) and depth-based suppression integration.
- `maxElicitationsPerSession` budget enforcement and the `lenny_elicitation_dropped_total{reason}` counter.
- Tenant elicitation-content-integrity admin endpoints per [§9.2](09_mcp-integration.md#92-elicitation-chain): `GET` and `PUT /v1/admin/tenants/{id}/elicitation-content-integrity` with justification-required writes and the `tenant.elicitation_content_integrity_mode_changed` audit event.
- Helm value `.Values.security.elicitationContentIntegrity.floor` rendered into the `lenny-deployment-phase-stamp` ConfigMap per [§17.2](17_deployment-topology.md#172-namespace-layout), plus the `platform.elicitation_content_integrity_floor_changed` and `tenant.elicitation_content_integrity_floor_clamp` audit events.

**Prerequisites.** Phase 9 exit gate.

**Exit criteria.** Tier 4 `mcp_elicitation_chain_test`, `mcp_provenance_test`, and the Tier 2 `mcp/integrity_test` pass per [TESTING.md §13.22](../TESTING.md#1322-phase-10--mcp-fabric-virtual-child-interfaces-elicitation-chain).

### 18.26 Phase 11 — Advanced credentials, multi-provider translators, revocation

**Deliverables.**

- Native translators for `aws_bedrock`, `vertex_ai`, and `azure_openai` per [§4](04_system-components.md#4-system-components) ("Supported upstream providers").
- Hot rotation via `credentials_rotated` and `credentials_acknowledged` per [§4.9](04_system-components.md#49-credential-leasing-service).
- Emergency revocation propagating through Redis pub/sub; active leases terminate within the documented SLO; `streaming-echo` mid-stream observes the revocation.
- `docs/runbooks/emergency-credential-revocation.md` runbook covering the direct-mode residual-risk operator steps per [§4.9](04_system-components.md#49-credential-leasing-service): post-revocation provider-side disablement, lease re-binding, and the audit-trail review checklist.
- Fallback chain per `credentialPolicy` with primary-cooldown handling.
- `CredentialRenewalWorker` proactive-renewal loop.
- `materializedConfig` schemas per provider.

**Prerequisites.** Phase 10 exit gate.

**Exit criteria.** Tier 2 component tests for `bedrock_translator`, `vertex_translator`, and `azure_translator` pass. Tier 4 `credential_rotation_test` and `credential_revocation_test` pass per [TESTING.md §13.23](../TESTING.md#1323-phase-11--advanced-credentials--multi-provider-translators--revocation).

### 18.27 Phase 11.5 — Incremental load test (credential lifecycle)

**Deliverables.**

- Credential-rotation-under-load scenario in `tests/tier7b_load_kind/scenarios/credential_rotation_under_load.go`.
- Phase 11.5 baseline JSON committed under `tests/tier7b_load_kind/baselines/`.

**Prerequisites.** Phase 11 exit gate.

**Exit criteria.** Tier 7 credential-rotation-under-load baseline is captured and reviewed per [TESTING.md §13.24](../TESTING.md#1324-phase-115--incremental-load-test-credential-lifecycle).

### 18.28 Phase 12a — Token Service hardening (KMS envelope, OAuth)

**Deliverables.**

- KMS envelope encryption in the Token Service per [§13.3](13_security-model.md#133-credential-flow), wrapping the Phase 5.5 `issued_tokens` table writes inside the write-before-issue Postgres transaction.
- `pkg/tokenexchange` ([§13.3](13_security-model.md#133-credential-flow) RFC 8693 invariants as a pure `Validate` function: scope narrowing `issued.scope ⊆ subject.scope`; tenant-scope match `issued.tenant_id == subject.tenant_id == caller.tenant_id`; caller-type cannot-elevate `agent < service < human`; audience cannot broaden; `typ` rules; `delegation_depth = parent + 1`; and `exp` clamp with ±1s skew and RFC 7519 whole-second truncation).
- Full OAuth 2.1 connector flow with PKCE, signed `state`, and KMS-encrypted credential storage per [§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc), building on the `ConnectorDefinition` resource and OAuth callback handler shipped in Phase 4.5.
- Redis EventBus `token.revoked` cluster-wide propagation.
- `TokenRevocationPropagationLag` alert wiring per [§16.5](16_observability.md#165-alerting-rules-and-slos).
- `lenny-ctl admin users force-revoke` end-to-end wiring per [§24.9](24_lenny-ctl-command-reference.md#249-user-and-token-management), completing the Phase 7 `full_revoke` user-invalidation flow with KMS-backed revocation.

**Prerequisites.** Phase 11.5 exit gate.

**Exit criteria.** Tier 2 `token_store_kms_test` and Tier 4 `oauth_connector_test` pass per [TESTING.md §13.25](../TESTING.md#1325-phase-12a--token-service-hardening-kms-envelope--oauth).

### 18.29 Phase 12b — `type: mcp` runtime support

**Deliverables.**

- `type: mcp` runtime-side adapter path (the runtime-author handler that speaks the adapter protocol against the gateway-side surface shipped in Phase 5).
- Reference `type: mcp` runtime under `cmd/runtimes/mcp-reference/` (or via one of the bundled connectors) per [§26.12](26_reference-runtime-catalog.md#2612-adding-a-new-reference-runtime).

**Prerequisites.** Phase 12a exit gate.

**Exit criteria.** Tier 4 `mcp_runtime_lifecycle_test` passes per [TESTING.md §13.26](../TESTING.md#1326-phase-12b--type-mcp-runtime-support).

### 18.30 Phase 12c — `sessionPolicy` presets and service mode

**Deliverables.**

- Pod recycling across whole sessions (`sessionPolicy.recycle`) per [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): the per-session slot cleanup and whole-pod scrub split, the reserved-hold claim window, and the retirement limits (`maxSessionsPerPod`, `maxPodUptimeSeconds`, and `maxScrubFailures`).
- Concurrent session slots (`sessionPolicy.maxConcurrentSessions > 1`) per [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): per-slot workspaces, the Redis slot-counter capacity gate, and the `acknowledgeProcessLevelIsolation` requirement.
- Service execution mode (`executionMode: service`) per [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): claimless tenant-affinity routing over the per-pod `maxConcurrent` slot bound, the `sessionIsolationLevel.conversationContinuity` contract field, and the registration-time warning for `multi_turn` runtimes on service-mode pools.
- Pod-level isolation enforcement for multiplexed pods (`maxConcurrentSessions > 1` and service-mode pools).

**Prerequisites.** Phase 12b exit gate.

**Exit criteria.** Tier 4 `session_recycle_test`, `session_concurrency_test`, and `service_mode_test` pass; Tier 5 `execution_modes_test` confirms admission webhooks and pod-level isolation hold per [TESTING.md §13.27](../TESTING.md#1327-phase-12c--sessionpolicy-presets-and-service-mode).

### 18.31 Phase 13 — Full observability, audit, `lenny-backup`, compliance webhooks

Phase 13 is the consolidation pass that completes the v1 observability surface, the audit and compliance pipelines, the `lenny-ops` runtime surfaces, and the agent-facing operability APIs that turn the platform into an AI-DevOps target.

**Deliverables.**

_Audit pipeline._

- `pkg/audit` ([§11.7](11_policy-and-controls.md#117-audit-logging) audit-log enums: `ChainIntegrity` with `IsAlarming` mapped to `AuditChainGap`; `ComplianceProfile` with `RequiresSIEM`; `RetentionPreset` with `PresetDays`, `PresetWindow`, `CompatibleProfiles`, and `ValidatePairing`; and `OCSFTranslationState` with `IsTerminal`).
- Hash-chain verifier and the OCSF translator binary, with the dead-letter PII redaction path that re-seals the chain.
- EventBus publish-state machine for audit events; retranscribe worker.
- Per-tenant advisory lock implementation per [§11.7](11_policy-and-controls.md#117-audit-logging).
- `audit_redaction_receipts` Postgres table and the `RedactionReceipt` schema (KMS-signed records) per [§12.8](12_storage-architecture.md#128-compliance-interfaces).
- Runtime drift detection for the `lenny_app` and `lenny_erasure` Postgres role separation per [§11.7](11_policy-and-controls.md#117-audit-logging) (the role schema itself ships in Phase 1.5): the goroutine that runs `audit.grantCheckInterval` clamped per `ComplianceProfile`, the `lenny_audit_grant_drift_total` counter, and the `audit.hardFailOnDrift` startup check.
- SIEM-streaming audit-event pipeline.
- Audit Log Query API per [§25.9](25_agent-operability.md#259-audit-log-query-api): `GET /v1/admin/audit-events`, `GET /v1/admin/audit-events/{id}`, `POST /v1/admin/audit-events/{id}/retranslate`, `POST /v1/admin/audit-events/republish`, and `DELETE /v1/admin/audit-partitions/{partition}` (admin-scoped partition drop with audit emission).

_Compliance and tenancy._

The compliance and tenant-deletion KMS operations below depend on the Phase 12a Token Service KMS envelope substrate; the per-tenant data-encryption keys and the audit-receipt signing keys reuse the same KMS access path.

- Compliance admission webhooks `lenny-data-residency-validator` per [§12.8](12_storage-architecture.md#128-compliance-interfaces) and `lenny-t4-node-isolation` per [§6.4](06_warm-pod-model.md#64-pod-filesystem-layout).
- Helm feature flag `features.compliance` enabled and the corresponding phase-stamp entry.
- GDPR erasure pipeline per [§12.8](12_storage-architecture.md#128-compliance-interfaces): `erasure_jobs` Postgres table with the phase-recovery field, the dependency-ordered `DeleteByUser` and `DeleteByTenant` job, per-store `DeleteByUser` adapters for every v1 store, the processing-restriction enforcement trigger, the `erasure_salt` lifecycle managed via KMS, the legal-hold preflight, and the region-scoped `legal_hold_escrow_kek`. The `ValidateMemoryStoreErasure` preflight contract is registered here as a no-op for the v1 store set; it activates in Phase 17b when the `MemoryStore` interface ships.
- Tenant deletion controller per [§12.8](12_storage-architecture.md#128-compliance-interfaces): the lifecycle states (`Active → MarkedForDeletion → Quiescing → Deleting → LegalHoldRetained → Tombstoned`), the `Quiescing → Deleting` legal-hold segregation step, the per-tenant KMS key deletion step within `Deleting`, and tombstone retention.
- T4 per-tenant KMS key lifecycle per [§12.9](12_storage-architecture.md#129-data-classification): admin-time KMS probe at `PUT /v1/admin/tenants/{id}`, leader-elected `T4KmsProbe` background worker, `T4KmsKeyUnusable` alert, and the `lenny_t4_kms_probe_*` metrics.
- Per-region backup pipeline per [§12.8](12_storage-architecture.md#128-compliance-interfaces): `pg_dump` routing, `backups.regions.<region>` config gating, and the continuous artifact-replication residency preflight with the `ArtifactReplicationResidencyViolation` and `BackupRegionUnresolvable` alerts.
- Post-restore erasure reconciler per [§12.8](12_storage-architecture.md#128-compliance-interfaces): the `lenny-ops`-orchestrated Job that replays erasure receipts after a restore, the `confirm-legal-hold-ledger` admin endpoint, and the fail-closed readiness gate.
- `POST /v1/admin/tenants/{id}/compliance-profile` and `POST /v1/admin/tenants/{id}/compliance-profile/decommission` admin surfaces per [§24.10](24_lenny-ctl-command-reference.md#2410-tenant-management).

_Backup and disaster recovery._

- `lenny-backup` Job (Postgres and MinIO backup, restore, and verification) per [§25.11](25_agent-operability.md#2511-backup-and-restore-api).
- `BackupService` orchestration surface per [§25.11](25_agent-operability.md#2511-backup-and-restore-api): `POST /v1/admin/backups`, `GET /v1/admin/backups`, `POST /v1/admin/backups/{id}/verify`, `POST /v1/admin/restore/preview`, `POST /v1/admin/restore/execute`, `POST /v1/admin/restore/safety-check`, `GET /v1/admin/restore/{id}/status`, `POST /v1/admin/restore/resume`, scheduled-backup leader-only cron with retention enforcement, and `POST /v1/admin/artifact-replication/{region}/resume`.
- `lenny-restore-test` CronJob per [§17.3](17_deployment-topology.md#173-disaster-recovery) running scheduled restore-validation against the verified backup set, emitting the `lenny_restore_test_duration_seconds` histogram and the `RestoreTestRtoExceeded` alert when the measured RTO exceeds the target.
- MinIO continuous async bucket replication per [§17.3](17_deployment-topology.md#173-disaster-recovery).
- `uploadToken` HMAC operator-initiated rotation admin surface: `lenny-ctl admin keys rotate uploadToken` and the corresponding admin REST endpoint, with audit emission per [§7.1](07_session-lifecycle.md#71-normal-flow). The automatic 24-hour rotation timer shipped in Phase 4; this surface adds out-of-cycle rotation for incident response.
- Pool drain procedure runbook at `docs/runbooks/pool-drain.md`: covers the pre-warmed pod replacement window, in-flight session policy, max wait, and the `lenny-ctl pools drain` invocation sequence.

_Gateway operability surface._

- Platform Health API per [§25.3](25_agent-operability.md#253-gateway-side-ops-endpoints): `GET /v1/admin/health`, `GET /v1/admin/health/{component}`, and `GET /v1/admin/health/summary`. `pkg/gateway/health/service.go` with the `SuggestedAction` contract and `pkg/gateway/health/runbook_links.go` issue-to-runbook lookup.
- Capacity Recommendations service per [§25.3](25_agent-operability.md#253-gateway-side-ops-endpoints): `GET /v1/admin/recommendations`, the per-replica ring-buffer aggregation, and the shared `pkg/recommendations/rules` catalog (the substrate shipped in Phase 2.5; the rule catalog is populated here).
- Version and configuration introspection per [§25.3](25_agent-operability.md#253-gateway-side-ops-endpoints): `GET /v1/admin/platform/version`, `GET /v1/admin/platform/version/full`, `GET /v1/admin/platform/config`, `GET /v1/admin/platform/config/diff`, and `GET /v1/admin/platform/registry`.
- Gateway event emitter per [§25.3](25_agent-operability.md#253-gateway-side-ops-endpoints): `pkg/gateway/events/emitter.go`, the in-process 500-event ring buffer (`EventBuffer`), and `GET /v1/admin/events/buffer` plus the headless `lenny-gateway-pods` Service for fan-out.
- `GET /v1/admin/me`, `GET /v1/admin/me/authorized-tools`, `GET /v1/admin/me/operations`, and the unified `GET /v1/admin/operations` Operations Inventory per [§25.4](25_agent-operability.md#254-the-lenny-ops-service).

_`lenny-ops` runtime surfaces._

- `lenny-ops` leader-elected runtime per [§25.4](25_agent-operability.md#254-the-lenny-ops-service): cron evaluator, webhook delivery goroutines, scheduled-backup runner, reconciliation goroutines, and the `platform_upgrade_check` cron.
- `GatewayClient` (authenticated gateway-to-`lenny-ops` HTTP client), `ReplicaDiscovery` (headless `lenny-gateway-pods` Service consumer), and `MetricSource` (Prometheus query path with per-replica fan-out fallback) per [§25.4](25_agent-operability.md#254-the-lenny-ops-service); internal TLS handshake on the NET-070 admin listener.
- Self-monitoring per [§25.4](25_agent-operability.md#254-the-lenny-ops-service): the self-health goroutine, `GET /v1/admin/ops/health`, the `ops_health_status_changed` audit emission, and the structured-log proxy at `GET /v1/admin/logs/pods/{ns}/{name}`.

_Operational coordination._

- Remediation Lock Service per [§25.4](25_agent-operability.md#254-the-lenny-ops-service): `pkg/ops/coordination/locks.go`, the tiered Postgres → Redis → in-memory storage, the `ops_remediation_locks`, `ops_lock_epoch`, and `ops_lock_conflicts` tables, outage-epoch tracking, and the deterministic split-brain reconciliation pass. `GET`, `POST`, `DELETE /v1/admin/remediation-locks/*` admin endpoints.
- Escalations per [§25.4](25_agent-operability.md#254-the-lenny-ops-service): `pkg/ops/escalations`, tiered storage, and the `GET /v1/admin/escalations` and child endpoints.
- Diagnostic Endpoints per [§25.6](25_agent-operability.md#256-diagnostic-endpoints): `pkg/ops/diagnostics/service.go`, `GET /v1/admin/diagnostics/sessions/{id}`, `GET /v1/admin/diagnostics/pools/{name}`, `GET /v1/admin/diagnostics/connectivity`, `GET /v1/admin/diagnostics/credential-pools/{name}`, and the auto-remediation `fix=true` flow.
- Operational Event Stream per [§25.5](25_agent-operability.md#255-operational-event-stream): `pkg/ops/events/service.go`, the Redis EventBus consumer, SSE relay at `GET /v1/admin/events/stream`, polling at `GET /v1/admin/events`, the webhook delivery worker with SSRF protections, the subscription cache, and `POST`, `GET`, `PUT`, `DELETE /v1/admin/event-subscriptions/*`.
- Configuration Drift Detection per [§25.10](25_agent-operability.md#2510-configuration-drift-detection): `pkg/ops/drift`, `bootstrap_seed_snapshot` Postgres table, `GET /v1/admin/drift`, `POST /v1/admin/drift/validate`, `POST /v1/admin/drift/snapshot/refresh`, `POST /v1/admin/drift/reconcile`, and the snapshot-staleness warning path.
- Platform Lifecycle Management per [§25.8](25_agent-operability.md#258-platform-lifecycle-management): the 7-phase upgrade state machine (`Preflight → OpsRoll → CRDUpdate → SchemaMigration → GatewayRoll → ControllerRoll → Verification`), `platform_upgrade_state` Postgres table, release-channel client, image-pull preflight, and the `POST /v1/admin/platform/upgrade/start`, `proceed`, `pause`, `rollback`, `verify`, `GET /v1/admin/platform/upgrade-check`, `GET /v1/admin/platform/upgrade/status` endpoints.
- Operational Runbook Index API per [§25.7](25_agent-operability.md#257-operational-runbooks): `pkg/ops/runbooks/index.go`, YAML front-matter parsing, `GET /v1/admin/runbooks`, `GET /v1/admin/runbooks/{name}`, and `GET /v1/admin/runbooks/{name}/steps`. The bundled `docs/runbooks/*.md` corpus (including `total-outage.md`, `manual-restore.md`, `admission-plane-feature-flag-downgrade.md`, `redis-failure.md`, `minio-failure.md`, `postgres-failover.md`, `etcd-operations.md`, and `security-incident-response.md` covering the [§11.8](11_policy-and-controls.md#118-security-incident-response) triage flow for `CredentialCompromised`, `DataResidencyViolationAttempt`, `AuditGrantDrift`, and `ArtifactReplicationResidencyViolation`) is finalized here; per-feature runbook stubs that landed alongside their phases are reviewed for completeness.

_MCP management._

- MCP Management Server per [§25.12](25_agent-operability.md#2512-mcp-management-server): the `ManagementMCPAdapter`, `POST /mcp/management`, the build-time `openapi-to-mcp` generator that emits one MCP tool per admin-API endpoint from the Phase 5 OpenAPI 3.1 document, capability negotiation, and the `x-lenny-mcp-tool`, `x-lenny-scope`, `x-lenny-required-role`, and `x-lenny-category` scope-and-RBAC enforcement.

_Observability catalog and chart wiring._

- Full [§16.1](16_observability.md#161-metrics) metrics catalog wiring: every metric not introduced by an earlier phase is registered against its emitting subsystem and validated by the [§16.1.1](16_observability.md#161-metrics) label-hygiene validator.
- Full [§16.5](16_observability.md#165-alerting-rules-and-slos) alert catalog wired into `pkg/alerting/rules`: every alert listed in [§16.5](16_observability.md#165-alerting-rules-and-slos), [§25.13](25_agent-operability.md#2513-bundled-alerting-rules), and the per-webhook unavailability table in [§17.2](17_deployment-topology.md#172-namespace-layout) is committed, including `AdmissionPlaneFeatureFlagDowngrade`, `BillingStreamBackpressure`, `BillingStreamEntryAgeHigh`, `BillingCorrectionApprovalBacklog`, `PodStateMirrorStale`, `T4KmsKeyUnusable`, `ArtifactReplicationResidencyViolation`, `BackupRegionUnresolvable`, `DataResidencyViolationAttempt`, `ElicitationContentTamperDetected`, and `CosignWebhookUnavailable`.
- [§16.6](16_observability.md#166-operational-events-catalog) Operational Events Catalog emission: every CloudEvent type (`alert_fired`, `pool_state_changed`, `experiment.*`, `restore_*`, `remediation.lock_*`, `deployment.feature_flag_downgrade_acknowledged`, `platform.elicitation_content_integrity_floor_changed`, `tenant.elicitation_content_integrity_floor_clamp`, `playground.bearer_mint_rejected`, etc.) is emitted by the responsible subsystem and flows through the [§25.5](25_agent-operability.md#255-operational-event-stream) stream.
- [§16.7](16_observability.md#167-section-25-audit-events) Section 25 audit events catalog: every audit event listed (e.g., `identity.discovered`, `compliance.profile_decommissioned`, `legal_hold.escrow_region_resolved`, `gdpr.erasure_deadletter_redacted`) is emitted by its phase-of-introduction subsystem and recorded in the hash chain.
- [§16.8](16_observability.md#168-section-25-metrics) Section 25 metrics catalog: `lenny-ops` self-monitoring, audit, drift, backup, escalation, lock, and platform-lifecycle metrics are registered.
- [§16.10](16_observability.md#1610-openslo-export) OpenSLO export rendered behind `monitoring.openslo.enabled`.
- Two-tier billing failover pipeline per [§11.2.1](11_policy-and-controls.md#112-budgets-and-quotas): `t:{tenant_id}:billing:stream` Redis stream buffer, `billing-flusher` consumer group, `billingReclaimIntervalSeconds`, `billingStreamTTLSeconds`, write-ahead buffer, `XAUTOCLAIM` reclaim path, and per-tenant `billing_seq_{tenant_id}` sequence.
- R-03 `TestBillingAuditRoutedThroughStoreRouter` integration test per [§12.2](12_storage-architecture.md#122-storage-roles): asserts every billing and audit write routes through the Phase 4 `StoreRouter` rather than a direct pool handle.
- Billing correction admin workflow per [§11.2.1](11_policy-and-controls.md#112-budgets-and-quotas): `POST /v1/admin/billing-corrections`, `.../approve`, `.../reject`, the `billing_correction_pending` state, `approver_notification_webhook`, and the `BillingCorrectionApprovalBacklog` alert.

_`lenny-ctl` operability extensions per [§25.14](25_agent-operability.md#2514-lenny-ctl-extensions)._

- `lenny-ctl admin backup *`, `lenny-ctl admin restore *`, `lenny-ctl admin audit *`, `lenny-ctl admin drift *`, `lenny-ctl admin operations *`, `lenny-ctl admin events *`, `lenny-ctl admin diagnose *`, `lenny-ctl admin runbooks *`, `lenny-ctl admin me`, `lenny-ctl admin locks *`, `lenny-ctl admin escalations *`, `lenny-ctl admin logs *`, `lenny-ctl admin mcp-management *`, `lenny-ctl admin erasure-jobs *`, `lenny-ctl admin sessions *`, `lenny-ctl admin migrate *`, `lenny-ctl admin tenants force-delete`, `lenny-ctl pools *` (sync-status, resume-reconciliation, circuit-breaker, exit-bootstrap, drain), and `lenny-ctl external-adapters validate`.

**Prerequisites.** Phase 12c exit gate. The Phase 3.5 phase-stamp ConfigMap and the Phase 5 OpenAPI generator are both required: the `features.compliance` flag is gated by the former, and the MCP Management Server's `openapi-to-mcp` tool inventory is gated by the latter. See [§18.40](#1840-hard-prerequisite-chain).

**Exit criteria.** Tier 2 `event_store_test`, Tier 4 `audit_pipeline_test`, Tier 5 `admission_data_residency_test`, `admission_t4_node_isolation_test`, and `backup_restore_test` pass per [TESTING.md §13.28](../TESTING.md#1328-phase-13--full-observability--audit--lenny-backup--compliance-webhooks). The metrics, alert, audit-event, and operational-event catalog cross-checks pass against the rendered chart manifests.

### 18.32 Phase 13.5 — Pre-hardening full-system load baseline

**Deliverables.**

- Tier 7 cloud load scenarios fully operational against the production-class environment.
- Phase 13.5 baseline JSON committed under `tests/tier7b_load_kind/baselines/`.
- Lenny-specific Postgres write-pattern benchmark and the calibrated `postgres.writeCeilingIops` per [§12.3](12_storage-architecture.md#123-postgres-ha-requirements).
- Calibrated `PostgresWriteBurstIops` alert threshold per [§16.5](16_observability.md#165-alerting-rules-and-slos).
- HPA and KEDA custom-metrics pipeline per [§10.1](10_gateway-internals.md#101-horizontal-scaling): the Prometheus Adapter wiring, the `lenny_gateway_request_queue_depth` and `lenny_gateway_rejection_rate` leading-indicator metrics, the KEDA `ScaledObject` Helm conditional, and the SCL-024 mutual-exclusion enforcement. KEDA is mandatory at Tier 3.
- SCL-026 HPA metric-role canonical mapping per [§4.1](04_system-components.md#41-edge-gateway-replicas): the authoritative mapping of `lenny_gateway_active_sessions`, `lenny_gateway_active_streams`, and `lenny_gateway_request_queue_depth` to HPA target-metric, alerting, and operator-dashboard roles. The chart's HPA template renders these as named roles rather than raw metric strings, so a metric rename touches one place.
- SCL-036 `minReplicas` burst-absorption formula per [§17.8.2](17_deployment-topology.md#1782-capacity-tier-reference): computed in the tier-preset Helm values from the documented burst-traffic envelope and HPA cooldown.
- SCL-041 system-wide concurrent-delegation cap per [§17.8.2](17_deployment-topology.md#1782-capacity-tier-reference): gateway-side enforcement bounded by the tier's concurrent-delegation budget, surfaced through the Phase 9 `budget_reserve.lua` substrate and validated at the tier-promotion gate.
- Tier 1 → Tier 2 and Tier 2 → Tier 3 promotion-gate validation harness per [§17.8.3](17_deployment-topology.md#1783-tier-promotion-guide) and [§4.1](04_system-components.md#41-edge-gateway-replicas): LLM Proxy-to-session ratio, `lenny_gateway_gc_pause_p99_ms` budget, `gateway.maxSessionsPerReplica` empirical calibration, and the KEDA-active-at-Tier-3 validation.

**Prerequisites.** Phase 13 exit gate.

**Exit criteria.** Tier 7 baselines and calibrations are captured and reviewed per [TESTING.md §13.29](../TESTING.md#1329-phase-135--pre-hardening-full-system-load-baseline).

### 18.33 Phase 14 — Comprehensive security hardening

**Deliverables.**

_Pod security and admission._

- `pkg/podsecurity` ([§13.1](13_security-model.md#131-pod-security) pod-spec validator: rejects host-sharing flags `shareProcessNamespace`, `hostPID`, `hostNetwork`, and `hostIPC` with `POD_SPEC_HOST_SHARING_FORBIDDEN`; requires pod-level `fsGroup = lenny-cred-readers GID` and `runAsNonRoot = true`; and enforces per-container `allowPrivilegeEscalation = false`, `privileged = false`, `readOnlyRootFilesystem = true`, `capabilities.drop = [ALL]`, and `capabilities.add = []`).
- Kubernetes admission webhook wrapping `pkg/podsecurity.ValidateAgentPod`.
- `lenny-preflight` pod-security integration: the Phase 3.5 preflight Job gains the `pkg/podsecurity` validator that stops any install whose Lenny-managed Deployment, DaemonSet, or Job pod template fails the rules.

_Release-time supply chain._

- Release pipeline workflow under `.github/workflows/release.yml`: multi-arch (`amd64` + `arm64`) image build, cosign sign-time signing of every Lenny-built image, SBOM emission (CycloneDX) attached as a sigstore attestation, GitHub release artifact attach, and the krew-index PR opener invoked by release automation.
- Helm chart provenance: `.tgz` charts signed at release time with a custodied PGP key per [§17.6](17_deployment-topology.md#176-packaging-and-installation); the public key is published alongside the release.
- Runtime-author SDK publish pipeline per [§15.7](15_external-api-surface.md#157-runtime-author-sdks): Go module proxy attestation, PyPI Sigstore provenance, and npm provenance for `runtime-sdk-go`, `lenny-runtime`, and `@lennylabs/runtime-sdk` respectively.
- Release-channel manifest publisher per [§25.8](25_agent-operability.md#258-platform-lifecycle-management): the HTTPS endpoint that serves the signed `latest` JSON (Ed25519-signed by a custodied operator key), the response-signing key rotation procedure with overlap window, and the 99.9% availability SLO for the endpoint.
- `platform.registry.requireDigest: true` enforcement at admission per [§13.1](13_security-model.md#131-pod-security); air-gap installs are required to set it.
- cosign image-signing verification at admission per [§13.1](13_security-model.md#131-pod-security) with `failurePolicy: Fail` and the `CosignWebhookUnavailable` alert.

_Network and TLS posture._

- Final default-deny + gateway-egress allowlist NetworkPolicy posture.
- `lenny-preflight` NetworkPolicy selector-consistency and parity audits per [§13.2](13_security-model.md#132-network-isolation) and [§17.2](17_deployment-topology.md#172-namespace-layout): NET-047, NET-050, NET-067, and NET-068 canonical-selector parity; DNS `podSelector` parity; the `ipblock-family-parity` check; the NET-057 SSRF private-range `except` parity between `allow-gateway-egress-llm-upstream` and `lenny-ops-egress`; the NET-061 `ops-egress-selector-parity` two-selector enforcement; the NET-064 SPIFFE-trust-domain and SA-token-audience uniqueness checks; and the NET-065 cluster-CIDR/IMDS symmetry check extended across the three rendered egress surfaces.
- CA rotation procedure executed end-to-end against the documented 24-hour overlap window, including the `TestRedisTLSEnforcement` and `TestPgBouncerTLSEnforcement` regression suite.
- JWT signing-key rotation procedure per [§13.3](13_security-model.md#133-credential-flow): JWK Set publication endpoint, key-overlap window, rollover audit event emission (`platform.jwt_signing_key_rotated`), and the operator runbook.
- seccomp profile validation.

_Hardening validation._

- External pen-test driver under `tests/tier9_security/pentest/`.
- Full-system security design review per [§18.1](#181-phasing-intent): findings recorded under `tests/tier9_security/reviews/full-system-review.md`, mirroring the Phase 5.6 credential review pattern. Tracked remediation items linked to commits.

**Prerequisites.** Phase 13.5 exit gate.

**Exit criteria.** Tier 9 security suites including `image_signing_test`, `network_policy/`, and `pentest/` pass per [TESTING.md §13.30](../TESTING.md#1330-phase-14--comprehensive-security-hardening). The release pipeline produces signed images, SBOM attestations, and a signed release-channel manifest on every tag.

### 18.34 Phase 14.5 — Post-hardening SLO re-validation

**Deliverables.**

- Re-run of every Phase 13.5 scenario with full security hardening in place.
- Delta documentation recorded under `tests/tier7b_load_kind/baselines/`.

**Prerequisites.** Phase 14 exit gate.

**Exit criteria.** The Tier 7 re-baseline is captured and the delta against the Phase 13.5 baseline is documented per [TESTING.md §13.31](../TESTING.md#1331-phase-145--post-hardening-slo-re-validation).

### 18.35 Phase 15 — Environment resource, RBAC, cross-environment delegation

**Deliverables.**

- `pkg/environment` ([§10.6](10_gateway-internals.md#106-environment-resource-and-rbac-model) primitives: the `Role` enum `viewer`, `creator`, `operator`, and `admin` with `AtLeast` enforcing the escalation order; the `Selector` evaluator with `MatchLabels`, `MatchExpressions` (`In`, `NotIn`, `Exists`, `DoesNotExist`), the `Types` filter, and the `Include`/`Exclude` overrides; `Requirement.Validate` rejecting malformed operators and value lists; and `Filter` returning the admitted subset for the transparent-filtering path).
- Environment admin API surface (`POST /v1/admin/environments`, list, update, and delete).
- Cross-environment delegation policy resolver and the explicit-environment endpoint dispatcher.
- OIDC-group resolution against the [§10.6](10_gateway-internals.md#106-environment-resource-and-rbac-model) `members` list.
- Transparent-filtering middleware that wraps `Selector.Filter` and joins authorized runtimes across the user's environments.

**Prerequisites.** Phase 14.5 exit gate.

**Exit criteria.** Tier 4 `environment_resource_test`, Tier 5 `cross_environment_delegation_test`, and Tier 9 `rbac/environment_rbac_test` pass per [TESTING.md §13.32](../TESTING.md#1332-phase-15--environment-resource--rbac--cross-environment-delegation).

### 18.36 Phase 16 — Experiments, PoolScalingController integration

**Deliverables.**

- `pkg/experiment` ([§10.7](10_gateway-internals.md#107-experiment-primitives) experiment primitives: `Status` `active`, `paused`, and `concluded` with `IsRoutable`; `TargetingMode` `percentage` and `external`; `Sticky` `user`, `session`, and `none`; `Propagation` `inherit`, `control`, and `independent`; `Definition.Validate` enforcing variant id uniqueness, the reserved `ControlVariantID` rule, weight ranges in `(0, 1)`, and `Σ weights < 1`; and `AssignVariant` implementing the HMAC-SHA256 percentage-mode bucketing with the empty-key control rule).
- ExperimentRouter interceptor in the gateway request path.
- PoolScalingController variant-pool sizing path per [§10.7](10_gateway-internals.md#107-experiment-primitives).
- OpenFeature/OFREP external-targeting integration with the SCL-023 per-tenant circuit breaker per [§10.7](10_gateway-internals.md#107-experiment-primitives): 200 ms upstream-provider timeout, breaker state per tenant, fallback to control variant under sustained OpenFeature provider failure.
- Experiment admin API surface (`POST /v1/admin/experiments`, list, update, conclude).

**Prerequisites.** Phase 15 exit gate.

**Exit criteria.** Tier 4 `experiment_routing_test` and Tier 7 `experiment_active_under_load` pass per [TESTING.md §13.33](../TESTING.md#1333-phase-16--experiments--poolscalingcontroller-integration).

### 18.37 Phase 16.5 — Experiment load test SLO re-validation

**Deliverables.**

- Phase 16.5 baseline JSON committed under `tests/tier7b_load_kind/baselines/`.
- Experiment-active scenario re-baselined with PoolScalingController variant-pool sizing active.

**Prerequisites.** Phase 16 exit gate.

**Exit criteria.** Tier 7 experiment-active baseline is captured and reviewed per [TESTING.md §13.34](../TESTING.md#1334-phase-165--experiment-load-test-slo-re-validation).

### 18.38 Phase 17a — Documentation, governance, community launch

**Deliverables.**

_Documentation and governance._

- Documentation site at general-availability quality: every doc page builds and every link resolves.
- `GOVERNANCE.md` finalized per [§23.2](23_competitive-landscape.md#232-community-adoption-strategy).
- The Phase 2 `CONTRIBUTING.md` early-development notice is removed or replaced per [§23.2](23_competitive-landscape.md#232-community-adoption-strategy).
- Final operational runbook corpus under `docs/runbooks/` reviewed against the [§17.7](17_deployment-topology.md#177-operational-runbooks) checklist and surfaced through the [Phase 13 Runbook Index API](#1831-phase-13--full-observability-audit-lenny-backup-compliance-webhooks).
- [§25.15](25_agent-operability.md#2515-failure-mode-analysis) failure-mode-analysis matrix finalized as a documentation deliverable: the per-subsystem failure-behavior table for `gateway`, `lenny-ops`, Postgres, Redis, and Prometheus, the Total-Outage Recovery decision tree, and the cross-references from `docs/runbooks/total-outage.md` and `manual-restore.md` are reviewed against current subsystem behavior.
- Community launch and the BDfN → steering committee transition criteria captured per [§23.2](23_competitive-landscape.md#232-community-adoption-strategy).
- API versioning surface per [§15.5](15_external-api-surface.md#155-api-versioning-and-stability): versioning middleware, deprecation-header machinery (`X-Lenny-Mcp-Version-Deprecated`), the session-lifetime exception, and the 6-month deprecation-window enforcement.

_Reference runtime catalog._

- Nine first-party reference runtimes per [§26](26_reference-runtime-catalog.md): `claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, and `crewai`, each packaged, signed, and registered with the gateway via `lenny-ctl admin runtimes register`.
- `lenny-ctl runtime init`, `validate`, and `publish` scaffolder per [§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding) (the language × template matrix; the substrate landed in Phase 2).
- `github.com/lennylabs/runtime-templates` template repository and the §26.12 proposal/PR process and acceptance checklist.

_Installer and packaging._

- `lenny-ctl install` interactive installer, `--non-interactive`, `--save-answers`, `lenny-ctl values validate`, and `lenny-ctl upgrade` per [§24.20](24_lenny-ctl-command-reference.md#2420-installation-wizard).
- `kubectl-lenny` krew plugin packaging per [§24.0](24_lenny-ctl-command-reference.md#240-packaging-and-installation) with a krew-index PR opened by release automation.
- Tier preset Helm values files `values-tier1.yaml`, `values-tier2.yaml`, and `values-tier3.yaml` per [§17.8.4](17_deployment-topology.md#1784-tier-preset-files).
- Nine curated answer files under `deploy/helm/lenny/answers/` per [§17.9.2](17_deployment-topology.md#1792-answer-file-catalog).
- Cloud portability surfaces per [§17.5](17_deployment-topology.md#175-cloud-portability): the S3, GCS, and Azure Blob `ArtifactStore` provider implementations and the per-provider lifecycle-rule preflight checks. Provider-specific cloud-managed answer files per [§17.9.3](17_deployment-topology.md#1793-cloud-managed-backends).
- CRD conversion graduation: the v1alpha1 → v1beta1 transition (60-day stability requirement) and the v1beta1 → v1 sign-off per [§15.5](15_external-api-surface.md#155-api-versioning-and-stability), executed against the `lenny-crd-conversion` webhook shipped in Phase 3.5.
- Air-gap install procedure documented at `docs/deployment/air-gap.md` per [§17.8.6](17_deployment-topology.md#1786-image-registry-and-air-gap) and [§25.8](25_agent-operability.md#258-platform-lifecycle-management): mirror-registry digest pinning, the `platform.registry.requireDigest: true` preflight requirement (Phase 14 enforces at admission), and the operator-held Ed25519 keys for re-signed release-channel mirrors with the chart-side `platform.releaseChannel.publicKeyPath` preflight validation.
- Decommission and uninstall runbook at `docs/runbooks/uninstall.md`: `helm uninstall` sequence, residual cleanup (`lenny-deployment-phase-stamp` ConfigMap, tombstoned tenants in legal-hold retention, MinIO bucket replication targets, persistent volumes), and the `lenny-ctl admin tenants force-delete` final-clear procedure.

_Local development mode._

- `lenny up` quick-start end-to-end per [§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) plus the full Embedded Mode surface: the in-cluster control plane rendered from the production chart under a development profile and run as pods in the embedded k3s, on in-process in-memory stores inside the gateway pod, with the gateway dev-mode bearer-trust authentication path, the `lenny down --purge` lifecycle command, and the production-warning banner. (`lenny image import`/`list`/`rm` and `lenny token print` substrate landed in Phase 2; Phase 17a wires them into the embedded-mode workflow.)

_Web playground._

- Web playground per [§27](27_web-playground.md), compiled into the gateway binary as an embedded static asset bundle and served from the gateway at `/playground`. The deliverable covers: the strict CSP, the `/playground/auth/*` endpoints, the `POST /v1/playground/token` mint with the `playground_allowed_scope` invariants, the Redis-backed session-record store at `t:{tenant_id}:pg:sess:*` and the `pg:revoked:*` revocation pub/sub with a 500 ms cross-replica revocation SLO, the dev-tenant Ready-gate, the `playground.bearer_mint_rejected` audit event emission, the `lenny_playground_bearer_mint_rejected_total` counter, and the `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED` log code per [§10.2](10_gateway-internals.md#102-authentication).
- Playground server-sourced posture banners per [§27.9](27_web-playground.md#279-security-considerations): the persistent red "DEV MODE — NOT FOR PRODUCTION" banner emitted from the gateway when `global.devMode=true`, and the persistent yellow "API KEY MODE — paste only operator-issued tokens" banner emitted when `playground.authMode=apiKey`. Both banners are server-rendered so a bundle swap cannot suppress them.
- Playground raw-frame inspector gateway-side redaction per [§27.9](27_web-playground.md#279-security-considerations): the gateway applies the same redaction rules as the [§16.4](16_observability.md#164-logging) audit log before forwarding frames to the browser, so credential material cannot leak through dev tools.
- Playground metrics catalog per [§27.8](27_web-playground.md#278-metrics): `lenny_playground_page_views_total`, `lenny_playground_sessions_created_total`, `lenny_playground_ws_connect_total`, `lenny_playground_session_revocations_total`, `lenny_playground_session_revocation_propagation_seconds`, and `lenny_playground_dev_tenant_not_seeded_total`. These extend the Phase 13 metrics catalog because the playground subsystem ships here.
- `lenny-preflight` playground rows per [§27.9](27_web-playground.md#279-security-considerations) and [§17.6](17_deployment-topology.md#176-packaging-and-installation): `playground.devTenantId` format and presence (cross-field check against `auth.multiTenant`), and the non-blocking `playground.apiKeyMode` WARN that fires when `playground.authMode=apiKey` and `global.devMode=false` unless `playground.acknowledgeApiKeyMode=true`.

_Benchmarks._

- `tests/tier7b_load_kind/scenarios/tthw.go` time-to-hello-world benchmark validated against the < 5-minute target.

**Prerequisites.** Phase 16.5 exit gate.

**Exit criteria.** Tier 11 documentation is fully exercised, every reference runtime in [§26](26_reference-runtime-catalog.md) is installable through `lenny-ctl install` against a Tier 1 deployment, the web playground passes the [§27.5](27_web-playground.md#275-protocol) protocol suite, and the time-to-hello-world benchmark confirms the target per [TESTING.md §13.35](../TESTING.md#1335-phase-17a--documentation--governance--community-launch).

### 18.39 Phase 17b — Memory, semantic caching, eval hooks

**Deliverables.**

- `MemoryStore` interface per [§9.4](09_mcp-integration.md#94-memory-store) with the reference backend, conversation indexing, and retrieval API. `lenny/memory_write` and `lenny/memory_query` MCP tool handlers per [§8.5](08_recursive-delegation.md#85-delegation-tools) and [§9.4](09_mcp-integration.md#94-memory-store).
- `MemoryStore.DeleteByUser` adapter wiring into the Phase 13 GDPR erasure pipeline; the `ValidateMemoryStoreErasure` preflight (registered as a no-op in Phase 13) becomes active here.
- `SemanticCacheStore` interface and reference backend per [§21](21_planned-post-v1.md): embedding integration, vector-store backing, TTL handling, and eviction policy.
- Evaluation hooks plumbing per [§21](21_planned-post-v1.md): the `POST /v1/sessions/{id}/eval` REST endpoint, the eval-hook interceptor wiring, and the `GET /v1/sessions/{id}/eval` read surface.
- Documented custom-backend preflight contract for `MemoryStore` and `SemanticCacheStore`.

**Prerequisites.** Phase 17a exit gate.

**Exit criteria.** Tier 2 `memory_store_test` and `semantic_cache_test`, and Tier 4 `eval_hooks_test`, pass per [TESTING.md §13.36](../TESTING.md#1336-phase-17b--memory-semantic-caching-eval-hooks). The GDPR erasure pipeline test that exercises `ValidateMemoryStoreErasure` against the new `MemoryStore` backend passes.

### 18.40 Hard Prerequisite Chain

Most phases follow linear ordering: Phase N requires the Phase N−1 exit gate. The following are the explicit cross-phase hard prerequisites that gate a later phase beyond linear ordering. Each one is also called out on the affected phase's "Prerequisites" line.

1. **Phase 0 → Phase 1 (ADR-007 verification).** ADR-007 (`SandboxClaim` optimistic locking and failover fencing) must be merged before Phase 1 implementation begins. The two ADR-007 verification tests (concurrent-claim integration test and leader-kill chaos test) are required before the verification hypothesis is treated as accepted. Source: [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers).
2. **Phase 1 → Phase 2 (agent-sandbox dependency health).** The Phase 1 exit gate includes the agent-sandbox dependency health assessment per [§4.6.1](04_system-components.md#46-pod-lifecycle-controllers). If any of the three criteria is not met, the fallback (custom kubebuilder-based controllers implementing the same `PodLifecycleManager` and `PoolManager` interfaces) is activated before Phase 2 begins, and the decision is recorded as an ADR.
3. **Phase 1.5 + Phase 3 → Phase 4 (`agent_pod_state` mirror).** The `agent_pod_state` Postgres mirror table (Phase 1.5) and its WarmPoolController write path (Phase 3) must both be in place before the Phase 4 fallback claim path that reads it activates. Until both writer and reader land, the API-server-only claim path is the sole production code path.
4. **Phase 3.5 → Phase 5.8 / Phase 8 / Phase 13 (phase-stamp ConfigMap).** The four-layer feature-flag downgrade enforcement per [§17.2](17_deployment-topology.md#172-namespace-layout) must be in place before any phase that flips a `features.*` Helm flag from `false` to `true` (`features.llmProxy` at Phase 5.8, `features.drainReadiness` at Phase 8, `features.compliance` at Phase 13). Each flip writes a phase-stamp entry; subsequent renders that attempt to set the flag back to `false` are rejected unless the operator sets the `acceptFeatureFlagDowngrade.<flag>` override.
5. **Phase 5 → Phase 13 (OpenAPI generator).** The build-time OpenAPI generator and `x-lenny-*` extension validation must be in place before the Phase 13 MCP Management Server can render its tool inventory via `openapi-to-mcp`. The MCP tool surface is generated from the OpenAPI document at build time.
6. **Phase 5.4 → Phase 5.5.** etcd encryption at rest must pass the Kind-tier `etcd_encryption_test` before basic credential leasing ships. The Token Service writes lease state to Kubernetes Secrets, whose underlying etcd storage must be encrypted before any production-grade credential is leased. Source: [TESTING.md §13.11](../TESTING.md#1311-phase-54--etcd-encryption-at-rest).
7. **Phase 5.5 → Phase 9 (`/v1/oauth/token` endpoint).** The Token Service `POST /v1/oauth/token` endpoint and `TokenIssuanceStore` write-before-issue ordering must be in place before recursive delegation ships, because child-token minting at every delegation hop calls the token-exchange endpoint. The Phase 12a KMS envelope wrapping hardens the same endpoint; it is not itself a prerequisite for Phase 9.
8. **Phase 5.75 → Phase 6 (real-credential testing).** The `AuthEvaluator` and `QuotaEvaluator` interceptors must be active before interactive sessions exercise real credentials. Real-credential testing requires the policy interceptors to enforce per-tenant rate and quota limits against authenticated calls. Source: [TESTING.md §13.14](../TESTING.md#1314-phase-575--minimum-viable-policy-enforcement).

Additional sequencing constraints (Phase 5.6 → 5.75 ordering by review cadence, the feature-flag downgrade prohibition per [§17.2](17_deployment-topology.md#172-namespace-layout), and the Tier 1 → Tier 2 → Tier 3 capacity-tier promotion gates per [§4.1](04_system-components.md#41-edge-gateway-replicas) and [§17.8.3](17_deployment-topology.md#1783-tier-promotion-guide)) are documented in their own sections and are not duplicated here.
