# Build gaps

This file catalogs the verified gaps in the Lenny test infrastructure and in the
production code that backs it. Each entry was checked against the filesystem,
package contents, or live test source on 2026-05-19. Counts and file
references reflect that snapshot.

The companion files are [`TESTING.md`](TESTING.md) (the authoritative testing
specification), [`BUILD-PLAN.md`](BUILD-PLAN.md) (the wave-by-wave execution
plan), and [`BUILD-PROGRESS.md`](BUILD-PROGRESS.md) (the live status table).
Entries here are gaps relative to TESTING.md and the spec, scoped to what an
implementer can resolve directly.

## Scope and methodology

Findings come from:

1. A mechanical sweep of `tests/` counting `func Test...` and `t.Skip` calls
   per file and classifying each skip as scaffold, infra-gated, env-gated,
   precondition-gated, or external-dependency.
2. A cross-reference of every scaffold's spec-section anchor against
   `TESTING.md`, `spec/`, and the corresponding `pkg/` or `cmd/` package.
3. Targeted greps for named symbols, endpoints, and dependencies in `pkg/`,
   `cmd/`, `migrations/`, `compose/`, `charts/`, and `scripts/`.

Where a finding rests on a behavioral observation that this audit did not
re-execute (for example a load-test scenario reporting a 100% error rate),
the source of that observation is named inline.

## Test-tier coverage

### Tier 0 (Static)

The static tier is fully implemented. The three test files
`tests/tier0_static/crds_test.go`, `tests/tier0_static/license_test.go`, and
`tests/tier0_static/schemas_test.go` cover the documented linters.
`crds_test.go` skips one assertion when `controller-gen` is absent; that
guard is an external-dependency check.

### Tier 1 (Unit)

Unit tests live next to source per TESTING.md §4. The repository carries
440 `_test.go` files across `pkg/`, `cmd/`, and the language modules.
Fuzz coverage is present at:

- `pkg/audit/fuzz_test.go`
- `pkg/auth/jwt/fuzz_test.go`
- `pkg/checkpoint/fuzz_test.go`
- `pkg/circuitbreaker/fuzz_test.go`
- `pkg/delegation/cycle/fuzz_test.go`
- `pkg/delegation/lease/fuzz_test.go`
- `pkg/elicitation/fuzz_test.go`
- `pkg/environment/fuzz_test.go`
- `pkg/experiment/fuzz_test.go`
- `pkg/idempotency/fuzz_test.go`
- `pkg/podsecurity/fuzz_test.go`
- `pkg/quota/fuzz_test.go`
- `pkg/tokenexchange/fuzz_test.go`
- `pkg/upload/fuzz_test.go`
- `pkg/api/v1/session/fuzz_test.go`

Property-based suites using `pgregory.net/rapid` exist at
`pkg/idempotency/property_test.go`, `pkg/delegation/cycle/property_test.go`,
`pkg/audit/property_test.go`, and `pkg/quota/property_test.go`.

`tests/tier1_unit/helm/helm_test.go` is the only file under
`tests/tier1_unit/`. It skips when either the `helm` binary or the
`helm-unittest` plugin is absent.

### Tier 2 (Component)

The tier carries 19 subdirectories. The following are pure-scaffold groups
where every test in the group skips:

- `tests/tier2_component/stores/scaffolds_test.go` (7 contracts: QuotaStore
  sliding window, TokenStore encrypted Postgres, ArtifactStore SSE-KMS and
  legal-hold, EvictionStateStore, CRDPodRegistry, StoreRouter, mandatory
  `DeleteByUser` / `DeleteByTenant`).
- `tests/tier2_component/translators/scaffolds_test.go` (2 tests:
  `openai_direct` and `openai_responses` native translators).
- `tests/tier2_component/rls/scaffolds_test.go` (2 tests: `__all__`
  tenant-context bypass and PgBouncer connection-pooler leakage).
- `tests/tier2_component/controllers/scaffolds_test.go` (2 tests: Pool
  Scaling Controller admission-retry harness and Token Service gRPC
  controller).
- `tests/tier2_component/gateway_subsystems/scaffolds_test.go` (5 tests:
  Session Orchestrator, File Fabric, MCP Fabric platform tools, Admin
  Plane, LLM Proxy component harnesses).

The store directory carries 28 live store-contract suites alongside the
seven scaffolds.

### Tier 3 (Contract)

All TESTING.md §12.3 contract subdirectories exist. The largest gap is
`tests/tier3_contract/rest_mcp_consistency/scaffolds_test.go`: of nine
tests, only `TestRESTMCPSessionLifecycle` and `TestRESTMCPTasks` are live.
Six others skip because the MCP side lacks a workspace-upload tool,
respond and dismiss elicitation tools, a memory REST surface, a delegation
REST surface, webhook subscription CRUD on either side, or admin MCP
tools. The `retryable` / `category` parity surface is now built
(`pkg/gateway/errorclassify` plus `mcp.NewLennyErrorDetail`); the
`TestRESTMCPRetryableFlags` scaffold can convert to a real assertion.

`tests/tier3_contract/rest_openai_chat/` and `rest_openai_responses/` cover
envelope-structure and field-preservation contracts. Streaming, tool calls,
attachments, system prompts, request-ID propagation, and multi-turn
conversations are not exercised.

`tests/tier3_contract/rest_sessions/sessions_test.go` exercises 7 of the
roughly 25 `/v1/sessions/...` REST endpoints declared at
`pkg/gateway/sessionserver/sessionserver.go:443-476`. The unexercised
endpoints are:

- `POST /v1/sessions/start`
- `POST /v1/sessions/{id}/derive`
- `POST /v1/sessions/{id}/replay`
- `POST /v1/sessions/{id}/extend-retention`
- `POST /v1/sessions/{id}/eval`
- `POST /v1/sessions/{id}/upload`
- `POST /v1/sessions/{id}/messages`
- `GET /v1/sessions/{id}/transcript`
- `GET /v1/sessions/{id}/tree`
- `GET /v1/sessions/{id}/events`
- `POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve`
- `POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny`
- `POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond`
- `POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss`
- `GET /v1/runtimes`
- `GET /v1/runtimes/{name}/meta/{key}`
- `GET /v1/models`
- `POST /v1/environments/{name}/sessions`
- `GET /v1/usage`
- `GET /v1/metering/events`
- `GET /v1/blobs/{ref...}`

`tests/tier3_contract/adapter_jsonl/messages_test.go` covers the Basic
level. Standard-level extensions (tool-call correlation, response-shorthand
normalization, path-traversal guard for local tools) skip.

`tests/tier3_contract/sdks/runtime_sdk_test.go` skips on workspace helpers
and telemetry validation; the client-side SDK tests skip on file upload
and the compatibility matrix.

### Tier 4 (Integration)

The directory carries 18 `.go` files comprising 32 live test functions
plus 8 scaffolded functions in
`tests/tier4_integration/scaffolds_test.go`. The eight scaffolds are
`TestDelegation`, `TestAdminBootstrap`, `TestCheckpointResume`,
`TestStreamingReconnect`, `TestLLMProxyAnthropic`,
`TestMigrationUpgrade`, `TestExperimentRouting`, and
`TestMCPRuntimeEndpoints`.

### Tier 5 (E2E on Kind)

Twelve `.go` files carry 24 test functions in total. Of these, 22 will
run when preconditions are met. Three skip unconditionally:
`TestNodeDrainDuringActiveSession` and `TestCrossEnvironmentDelegation`
in `scaffolds_test.go`, and two of the four sub-tests in
`tests/tier5_e2e_kind/audit_test.go`. `TestConcurrentExecutionModes` in
`scaffolds_test.go` is live.

Documented scenarios with no live test coverage include warm-pool
scaling, `lenny-ops` first deploy, bootstrap fresh install,
label-immutability webhook isolated coverage, orphan-claim GC, the
drain-readiness webhook end-to-end, tenant-namespace isolation against a
live cluster, the pool upgrade state machine, the playground auth-mode
matrix (OIDC, API Key, and Dev), token rotation against a running
session, the operator preflight suite, schema migration with the dirty
flag, and the runtime upgrade controller.

### Tier 6 (E2E on cloud)

All 11 tests in `tests/tier6_e2e_cloud/scaffolds_test.go` skip after the
cloud-availability guard. The blockers are external infrastructure that
the repository does not yet ship; see [Infrastructure gaps](#infrastructure-gaps).

### Tier 7 (Load and SLO)

The `tests/tier7_load/scenarios/` directory contains 14 k6 scenario
folders. The `tests/tier7_load/baselines/` directory contains nine
populated baseline JSON files. The five scenarios without baselines are
`credential_lifecycle`, `delegation_fanout_mcp`, `experiment_load`,
`post_hardening_slo`, and `streaming_throughput`.

Scenarios blocked by production-code defects:

- `TestStreamingReconnectLoad` is blocked because the
  `gatewaymetrics` middleware's `statusRecorder` at
  `pkg/gateway/gatewaymetrics/gatewaymetrics.go:296` does not forward
  `http.Flusher`. The SSE handler at
  `pkg/gateway/sessionserver/events.go:50` requires `http.Flusher` and
  returns 500 when the assertion fails.
- `TestCheckpointDuration` is blocked because
  `POST /v1/sessions/{id}/upload` returns errors on every request in the
  rate-bounded scenario. The handler exists at
  `pkg/gateway/sessionserver/upload.go:76` (`handleUpload`).
- `TestWebhookDeliveryLoad` has no surface to test because no webhook
  subscription endpoint is registered.
- `TestPlaygroundRevocation` skips because the §27 web playground is
  disabled on the e2e gateway.
- `TestConcurrentWorkspaceSlots` skips because per-slot workspace
  multiplexing is pod-internal and exposes no gateway endpoint.
- `TestExperimentActiveUnderLoad` skips because no active experiment is
  configured in the e2e overlay.

Scenarios gated to release runs:

- `TestGateway10kSessions` is cloud-only.
- `TestFullSystemLoadBaseline` is phase-gated to Phase 13.5.
- `TestFullSystemWithHardeningLoad` is phase-gated to Phase 14.5.
- `TestTimeToHelloWorld` depends on a Phase 17a TTHW install-replay
  harness that does not exist.

### Tier 8 (Chaos)

`tests/tier8_chaos/runbook-map.yaml` maps 48 runbooks to chaos tests. Of
the mapped tests, 19 are live and 29 are scaffolded in
`tests/tier8_chaos/scaffolds_test.go`. Every mapped test name resolves to
an existing function.

Live tier-8 tests:

- `tests/tier8_chaos/store_failure_test.go`: `TestPostgresUnavailable`,
  `TestRedisClusterDegraded`, `TestMinIOUnavailable`,
  `TestDualStoreUnavailable`.
- `tests/tier8_chaos/pod_disruption_test.go`: `TestGatewayReplicaFailure`,
  `TestAdmissionWebhookOutage`.
- `tests/tier8_chaos/leader_election_test.go`:
  `TestControllerLeaderElectionDisruption`.
- `tests/tier8_chaos/component_failure_test.go`: `TestTokenServiceOutage`,
  `TestEphemeralContainerCredGuardOutage`, `TestCertManagerOutage`.
- `tests/tier8_chaos/concurrency_test.go`: `TestDoubleClaimVerification`,
  `TestSandboxClaimRaceUnder100Goroutines`, `TestSandboxFinalizerHang`.
- `tests/tier8_chaos/config_drift_test.go`: `TestPoolConfigDrift`,
  `TestNetworkPolicyConfigDrift`, `TestNetworkPolicyDrift`,
  `TestSchemaMigrationDirtyFlag`.
- `tests/tier8_chaos/audit_chain_test.go`: `TestAuditChainGapDetection`.
- `tests/tier8_chaos/live_session_test.go`:
  `TestPodKillDuringActiveSession`.

The 29 scaffolded chaos tests cluster around 11 missing dependencies; see
[Infrastructure gaps](#infrastructure-gaps) and
[Implementation gaps](#implementation-gaps).

### Tier 9 (Security)

Fifteen `.go` files carry 31 test functions: 12 unconditional scaffolds
in `scaffolds_test.go`, 7 functions that skip on missing cluster
preconditions, and 12 functions that run unconditionally when the e2e
cluster is up.

Live security suites (`body-skips=0`):

- `admission_cred_test.go`, `admission_ephemeral_test.go`,
  `admission_label_immutability_test.go`, `admission_security_test.go`
- `tls_test.go`
- `network_policy_test.go`
- `live_session_test.go`

Suites that run when preconditions are met:
`audit_integrity_test.go`, `image_signing_test.go`, `rbac_test.go`,
`ssrf_test.go`, `tenant_isolation_test.go`.

The 12 unconditional scaffolds skip because of these missing pieces:

- An elicitation-emitting runtime plus tamper-detect alerting (3 tests).
- A credential-carrying runtime image with a shell, since the default
  echo image is distroless (3 tests).
- An egress-capture sidecar (2 tests).
- The `lenny-sandboxclaim-guard` webhook API-server reachability (1
  test).
- An OWASP ZAP automation container (1 test).
- TLS-only datastore deployment (1 test).
- A release-pipeline SBOM step (1 test).
- An external pen-test bundle (1 test).

`tests/tier9_security/pentest/driver.go` provides the harness for
replaying external findings; `tests/tier9_security/reviews/` carries
`credential-review.md` and `full-system-review.md`.

### Tier 10 (Conformance)

`tests/tier10_conformance/scaffolds_test.go` carries seven functions.
Four are live: `TestBasicAdapterProtocol`, `TestStandardLevel`,
`TestFullLevel`, and `TestBundledRuntimesEveryPR`. Three skip:

- `TestReferenceCatalogNightly` (the §26 reference-runtime OCI images
  are not published).
- `TestThirdPartyRegistration` (`cmd/lenny-compliance` has no
  `RegisterAdapterUnderTest` API).
- `TestFidelityMatrix` (the per-OutputPart fidelity table and the
  OpenAI/Anthropic translators are not built).

### Tier 11 (Documentation)

Five test files exist. `docs_test.go`, `code_blocks_test.go`, and
`adr_test.go` enforce real assertions and fail when violations occur.

`runbooks_test.go` walks `docs/runbooks/` and parses every front matter
and section heading. Every structural violation is reported with
`t.Logf` rather than `t.Errorf`; only filesystem-walk failures fail the
test. A comment at line 153 documents the deferral: "informational only
today: runbooks ship as placeholders in Phase 0. When the canonical
structure is rolled out (Phase 13.5+), promote the threshold from
informational to a hard regression gate."

`time_to_hello_world_test.go` contains three parent tests with eleven
sub-tests. Five sub-tests skip:

- `TestTimeToHelloWorld/step_1_brew_install` (no published Homebrew
  tap).
- `TestTimeToHelloWorld/step_3_lenny_session_start` (no `lenny
  session` subcommand in `cmd/lenny`).
- `TestTimeToHelloWorld/step_4_session_cleanup_within_5min` (depends on
  steps 1-3).
- `TestRuntimeAuthorQuickStart/session_against_new_runtime` (no
  reachable gateway and no `lenny session` subcommand).
- `TestOperatorInstallNonInteractive/smoke_session_after_install` (same
  reason).

## Implementation gaps

This list names production-code absences each backed by a verified
filesystem or grep check. Tests that reference these absences are
identified.

### Storage and persistence

- **`pkg/storerouter` does not exist.** The TESTING.md §12.2.1 store
  router (session-shard extraction, tenant-shard routing, R-03
  billing/audit routing, scatter-gather control) has no implementation.
  Blocks `TestStoreRouterContract` in
  `tests/tier2_component/stores/scaffolds_test.go:153`.

- **TokenStore stores SHA-256 hashes only.**
  `pkg/gateway/issuedtokenstore/issuedtokenstore.go:43-45` declares
  `TokenHash []byte` as the SHA-256 digest;
  `migrations/0001_initial_schema.up.sql:188-189` confirms the database
  column carries the digest. There is no encrypted TokenStore with
  KMS-envelope writes or a revocation-lookup index. Blocks
  `TestTokenStoreContract`.

- **CRDPodRegistry over the Kubernetes API is absent.**
  `pkg/podsession.Registry` is an in-process map. There is no `WatchPods`
  event-latency surface. Blocks `TestPodRegistryContract`.

- **EvictionStateStore is absent.** No eviction-state migration, no
  Postgres store, and no MinIO context-key index. Blocks
  `TestEvictionStateStoreContract`.

- **ArtifactStore extensions are absent.** `pkg/blobstore/` ships
  `miniostore/` and `replication/` only. SSE-KMS configuration, soft-
  delete tombstones, a hard-prune worker, legal-hold suspension,
  partial-manifest cleanup, the T4 per-tenant KMS probe, and the
  MinIO-outage Postgres-minimal-state fallback are not built. Blocks
  `TestArtifactStoreContract` and the tier-4 checkpoint flow.

- **Mandatory erasure interface is partial.** Only a subset of
  tenant-scoped stores expose both `DeleteByUser` and `DeleteByTenant`.
  Erasure adapters need to land on billing, eval, experiment,
  environment, runtime, connector, user, custom-role, transcript, and
  agent-pod-state stores before
  `TestDeleteByUserAndTenantInterface` becomes meaningful.

### Token Service and credentials

- **`pkg/tokenservice` ships only HTTP token-exchange.**
  `pkg/tokenservice/tokenservice.go` declares `NewServer`, `Handler`,
  `handle`, `parseRequest`, and supporting helpers; none of
  `AssignCredentials`, `RotateCredentials`, or `RevokeCredentials` is
  served. The gRPC stubs from `pkg/proto/adapter/v1` have client-side
  consumers in `pkg/adapter`, `pkg/gateway/credassign`, and
  `pkg/gateway/credrenewal`, but no server-side implementation.
  Blocks `TestTokenServiceController` and four tier-8 chaos tests
  (`TestEmergencyRevocationDuringActiveSession`,
  `TestRotationFailure`, `TestDenyListPropagationUnderRedisOutage`,
  `TestCredentialPoolExhaustion`) and three tier-9 credential-leakage
  tests.

- **Pool Scaling Controller admission-retry is absent.** The
  scaling-formula evaluator and circuit breaker exist in
  `pkg/controller/poolscaling/`, but the admission-denied retry-with-
  backoff loop and the `PoolScalingAdmissionStuck` alert wiring are
  not built. Blocks `TestPoolScalingControllerAdmissionRetry`.

### LLM Proxy and translators

- **`openai_direct` and `openai_responses` native translators are
  absent.** `pkg/gateway/llmproxy/` carries `azure_translator.go`,
  `bedrock_translator.go`, `vertex_translator.go`, and `translator.go`
  (Anthropic) only. There is no native translator for
  `api.openai.com` or for the `/v1/responses` dialect. Blocks
  `TestLLMProxyTranslatorOpenAIChatCompletions`,
  `TestLLMProxyTranslatorOpenAIResponses`, the tier-3 OpenAI Chat and
  Responses fidelity matrices, the tier-10 fidelity matrix, and the
  tier-4 `TestLLMProxyAnthropic` integration test.

### Cloud-provider integrations

- **`pkg/blobstore` ships only MinIO.** GCS, S3, and Azure Blob
  adapters and the matching provider-side service-account binding are
  absent.

- **`pkg/kms` ships only the local provider.** The directory listing is
  `kms.go`, `local.go`, `local_test.go`, and `envelope/`. Cloud KMS,
  AWS KMS, and Azure Key Vault providers are documented as
  CloudProviderSeam but not implemented. The per-tenant KEK allocator
  is not wired.

### Gateway request handling

- **`POST /v1/sessions/{id}/upload` returns errors on every request in
  the checkpoint-duration scenario.** Reported by the tier-7 scaffold
  at `tests/tier7_load/scaffolds_test.go:220`. The handler is at
  `pkg/gateway/sessionserver/upload.go:76`.

### MCP and REST parity

- **Memory operations lack a REST surface.** MCP tools
  `lenny/memory_write`, `lenny/memory_query`, and
  `lenny/memory_delete` are registered at
  `pkg/gateway/mcptools/mcptools.go:1024-1093` with
  `pkg/gateway/memorystore` backing. No `/v1/memories` endpoint is
  registered in `pkg/gateway/sessionserver/`.

- **Webhook subscription CRUD is absent on both sides.** A grep for
  `/v1/webhooks`, `WebhookSubscription`, and `webhook_subscription`
  returns no matches in `pkg/gateway/` or `pkg/api/`. Blocks
  `TestRESTMCPWebhookSubscription` and `TestWebhookDeliveryLoad`.

- **Workspace upload has no MCP tool.** `pkg/gateway/mcptools` registers
  `create_session`, `send_message`, `lenny/delegate_task`,
  `lenny/request_elicitation`, `lenny/get_task_tree`,
  `lenny/memory_write`, and `lenny/memory_query`. There is no upload
  tool. Blocks `TestRESTMCPWorkspaceUpload`.

- **Admin operations have no MCP tools.** `pkg/gateway/admin` exposes
  REST admin handlers (runtimes, pools, connectors, tenants,
  credentialPools, audit). The MCP side has no matching tools. Blocks
  `TestRESTMCPAdmin`.

- **Delegation lacks a REST counterpart.** The REST adapter advertises
  `SupportsDelegation: false`. Delegation is MCP-only via
  `lenny/delegate_task`. Blocks `TestRESTMCPDelegation`.

### Delegation and elicitation

- **Tier-8 delegation chaos tests need fault-injection wiring.** The
  delegation primitives themselves are built (Phase 9 Done): the §8
  lease lives in `pkg/delegation/lease`, the budget service in
  `pkg/gateway/leasecontrol`, and the §8.5 platform MCP tools are
  registered in `pkg/gateway/mcptools/mcptools.go`. The
  `delegation-echo` reference runtime at `cmd/runtimes/delegation-echo`
  exercises the surface end-to-end. The tier-4
  `TestDelegation` spawn-and-tree contract is exercised by
  `tests/tier4_integration/delegation_test.go`. The remaining gap is
  the chaos-tier fault-injection harness that drives
  `TestChildCrashMidTask`, `TestParentCrashDuringAwaitChildren`,
  `TestDelegationBudgetExhaustion`,
  `TestDelegationDepthDeadlockDetection`, and
  `TestLeaseExtensionCoolOffPersistence` against a live cluster.

- **Elicitation chain is incomplete.** The default echo runtime emits
  no elicitations. There is no chained respondent agent and no tamper
  detect/enforce/platform-floor alerting pipeline. Blocks tier-3
  `TestRESTMCPElicitation`, tier-8
  `TestElicitationDeadlockDetection`, and tier-9
  `TestElicitationTamperEnforceMode`,
  `TestElicitationTamperDetectOnlyMode`, and
  `TestElicitationPlatformFloor`.

### CLI and reference runtimes

- **`cmd/lenny` has no `session` subcommand.** The dispatch in
  `cmd/lenny/main.go` handles `up`, `down`, `status`, `logs`, `token`,
  `image`, `__supervise`, and `help`. Blocks tier-11 time-to-hello-
  world sub-steps 3, 4, 5, and 6.

- **`cmd/lenny-compliance` has no `RegisterAdapterUnderTest` API.** A
  grep across `cmd/lenny-compliance/` returns no matches. Blocks
  tier-10 `TestThirdPartyRegistration`.

- **§26 reference-runtime OCI images are not published.** The
  conformance harness drives a local binary path via `--binary`. The
  image-driven path (`lenny-test conformance --image`) and the
  registry coordinates do not exist. Blocks tier-10
  `TestReferenceCatalogNightly`.

- **Per-OutputPart fidelity table is absent.** TESTING.md §12.10
  specifies a table-driven test that asserts which fields are
  preserved or dropped per `(model, role, type)` tuple. The table and
  the OpenAI/Anthropic Completions/Responses translators it would
  validate are not built. Blocks tier-10 `TestFidelityMatrix`.

## Infrastructure gaps

- **`deploy/` does not exist.** TESTING.md §12.6 and the tier-6
  scaffold messages reference `deploy/terraform/cloud/<provider>/`.
  No such directory tree is present.

- **Cloud bring-up scripts are placeholders.**
  `scripts/cloud/gke/up.sh` is 41 lines and prints a "Phase 13+
  deliverable" notice. `scripts/cloud/aks/up.sh` and
  `scripts/cloud/eks/up.sh` are 11-line shells with no body. All three
  exit 0 without doing work.

- **`tests/testinfra/chaos/chaos.go` has a chaos-mesh placeholder.**
  The toxiproxy code path is fully implemented.
  `injectViaChaosMesh` at line 84 emits a log line and returns a no-op
  cleanup. `PartitionService` via chaos-mesh at line 124 does the same.
  TESTING.md §12.8 chaos scenarios that need network-partition
  injection therefore have no working harness on Kind.

- **`tests/testinfra/security/zap/zap.go` defers report parsing.** The
  helper runs `zap.sh -quickout <report>` and returns
  `Result{ReportPath: report}`. A comment near line 98 confirms
  "Parsing is deferred." Tier-9 ZAP fuzzing has no programmatic
  assertion path.

- **The compose profile lacks PgBouncer and Redis Sentinel.**
  `compose/default.yml` provisions neither. Blocks the
  `TestRLSPgBouncerGuard` tier-2 RLS scaffold and the
  `TestPgBouncerSaturation` tier-8 chaos scaffold, and prevents Sentinel-
  failover tests.

- **The e2e cluster runs single-replica datastores by design.**
  `lenny-postgres`, `lenny-redis`, and `lenny-minio` Deployments have
  one replica each. HA Postgres, Redis Sentinel, multi-zone MinIO, and
  cross-zone topologies all need separate deployments.

- **The default runtime image has no shell.** `cmd/runtimes/echo` is
  distroless. Credential leakage probes that need
  `kubectl exec ... cat /proc/<pid>/environ` cannot run. Tier-9
  credential leakage and tier-8 deny-list propagation depend on a
  runtime image with a shell and a runtime that declares credentials.

- **No egress-capture sidecar is deployed.** Agent-pod egress probes
  (tier-9 `TestNetworkPolicyAgentEgress` and the credential network-
  egress test) cannot inspect outbound traffic without a capture layer.

- **No clock-injection harness is deployed.** Tier-8
  `TestGatewayClockDrift`, `TestCertificateExpiryAdvance`, and the
  `TestT3T4SLABreach` scenario all need it.

- **No published Homebrew tap.** `lennylabs/tap` does not exist. The
  formula-install step of the quick-start documentation cannot be
  validated.

## Test infrastructure gaps

- **`tests/testinfra/audit` has no consumers.** The package is real
  but no test directory imports it. The Phase 11 hash-chain helper is
  ready and waiting.

- **`tests/testinfra/matrix` has no consumers.** The contract-test
  matrix harness is real but unused. Tier-3 contract suites that
  TESTING.md describes as parameterized matrices are written as
  straight-line tests today.

- **`tests/testinfra/helm` has no consumers.** A grep across `tests/`
  shows no test imports the helper. `tests/testinfra/kind/install.go`
  calls `helm status` directly via `exec.LookPath("helm")` rather than
  through the helper.

## Cross-cutting findings

- **Runbook coverage is silent in CI.**
  `tests/tier11_docs/runbooks_test.go` reports every structural
  violation with `t.Logf` rather than `t.Errorf`, and
  `tests/testinfra/chaos/runbook_map_test.go` at line 82 does the same
  for runbook-map drift. Failures and drift are visible only in test
  logs.

- **`docs/runbooks/` and `runbook-map.yaml` are out of sync.**
  `docs/runbooks/` carries 60 files (59 runbooks plus `index.md`).
  `tests/tier8_chaos/runbook-map.yaml` carries 48 entries. 44
  runbooks in `docs/runbooks/` have no mapping. The unmapped slugs
  are:

  ```
  admission-plane-feature-flag-downgrade
  audit-grant-drift
  audit-pipeline-degraded
  billing-stream-backlog
  ca-rotation
  checkpoint-stale
  circuit-breaker-open
  coordinator-handoff-slow
  crd-upgrade
  credential-revocation
  cycle-detection-mode-unsafe
  data-residency-violation
  delegation-budget-recovery
  elicitation-backlog
  elicitation-content-integrity-weakened
  elicitation-content-tamper-detected
  ephemeral-container-cred-guard-unavailable
  erasure-job-failed
  etcd-key-rotation
  etcd-operations
  gateway-capacity
  gateway-rate-limit-storm
  gateway-subsystem-extraction
  jwt-key-rotation
  legal-hold-quota-pressure
  llm-egress-anomaly
  llm-translation-degraded
  minio-failure
  pool-bootstrap-mode
  redis-failure
  schema-migration-failure
  sdk-connect-timeout
  session-eviction-loss
  slo-session-availability
  slo-session-creation
  slo-startup-latency
  slo-ttft
  storage-quota-high
  stuck-finalizer
  tenant-deletion-overdue
  tier-promotion
  token-store-unavailable
  total-outage
  warm-pool-exhaustion
  workspace-seal-stuck
  ```

  TESTING.md §1834 claims 56 runbooks, against the on-disk count of 59
  excluding `index.md`. The specification and the on-disk content
  disagree.

- **Spec traceability files are present and populated.**
  `tests/spec-map.json` is 160 KB, `tests/change-graph.json` is 16 KB,
  and `tests/groups.yaml` and `tests/groups.subsets.yaml` together
  cover the group definitions for tier selection. The harness command
  surface that consumes them (`lenny-test validate-maps`, `--changed`,
  `--spec`, `--group`) was not exercised in this audit.

- **CI workflows are present.** `.github/workflows/` carries
  `pr.yml`, `nightly.yml`, `weekly.yml`, `pre-release.yml`,
  `release.yml`, `phase-gate.yml`, `flake-budget.yml`,
  `cache-prune.yml`, `dco.yml`, `sdk-publish.yml`, `secret-scan.yml`,
  and a `reusable/` directory. End-to-end pipeline timing was not
  measured in this audit.

## Recommended sequencing

The implementation gaps cluster such that a small set of investments
unblocks disproportionately many tests.

1. Build the Token Service gRPC controller (`AssignCredentials`,
   `RotateCredentials`, `RevokeCredentials`) and ship a credential-
   carrying runtime image with a shell. Unblocks one tier-2 controller
   test, one tier-4 integration test, four tier-8 chaos tests, and
   three tier-9 security tests.
2. Land the `openai_direct` and `openai_responses` native translators
   and the per-OutputPart fidelity table. Unblocks two tier-2
   translator tests, two tier-3 fidelity tests, and one tier-10
   fidelity test.
3. Ship the elicitation-emitting runtime variant and the tamper-
   detect, tamper-enforce, and platform-floor resolver wiring.
   Unblocks one tier-3 contract test, one tier-8 chaos test, and
   three tier-9 security tests.
4. Fix the `/v1/sessions/{id}/upload` 100% error rate so the
   checkpoint-duration k6 scenario can baseline. The `http.Flusher`
   wrapper that previously paired with this item is resolved by
   adding `Flush()` to the gatewaymetrics statusRecorder; the tier-7
   `TestStreamingReconnectLoad` scaffold is converted.
5. Promote runbook-coverage assertions from `t.Logf` to `t.Errorf` in
   `tests/tier11_docs/runbooks_test.go` and
   `tests/testinfra/chaos/runbook_map_test.go`, then reconcile
   `docs/runbooks/` against `tests/tier8_chaos/runbook-map.yaml` by
   either mapping the 44 unmapped runbooks or deleting the docs.

## Maintenance

This file records the verified state on 2026-05-19. When a gap is
closed, delete its entry. When a new gap surfaces, add it under the
matching section with a verified file or line reference. Counts in
this file are snapshots; treat them as the audit's record of the
state at that date rather than a continuing invariant.
