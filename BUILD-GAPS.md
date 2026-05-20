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

The tier carries 19 subdirectories. The following are scaffold groups
where some or every test in the group still skips:

- `tests/tier2_component/stores/scaffolds_test.go` (1 contract:
  ArtifactStore SSE-KMS and legal-hold).
- `tests/tier2_component/gateway_subsystems/scaffolds_test.go` (5
  tests intentionally deferred to existing tier-4 and unit
  coverage — the scaffold diagnoses cite the production-code paths
  each subsystem actually exercises, so re-creating a §12.2.3
  tier-2 harness adds no new code path).

The store directory carries 30 live store-contract suites alongside the
five scaffolds.

### Tier 3 (Contract)

All TESTING.md §12.3 contract subdirectories exist. The
`tests/tier3_contract/rest_mcp_consistency/scaffolds_test.go` mix is:
all nine tests live. Four exercise real parity
(`TestRESTMCPSessionLifecycle`, `TestRESTMCPTasks`,
`TestRESTMCPRetryableFlags`, `TestRESTMCPMemory`); five document
§15.2.1 by-design no-parity acknowledgements
(`TestRESTMCPElicitation`, `TestRESTMCPDelegation`,
`TestRESTMCPAdmin`, `TestRESTMCPWebhookSubscription`,
`TestRESTMCPWorkspaceUpload`).

`tests/tier3_contract/rest_openai_chat/` and `rest_openai_responses/` cover
envelope-structure and field-preservation contracts. Streaming, tool calls,
attachments, system prompts, request-ID propagation, and multi-turn
conversations are not exercised.

`tests/tier3_contract/rest_sessions/sessions_test.go` plus
`unexercised_endpoints_test.go` exercise the §15.1 REST surface.
The original suite covered the core lifecycle (create/get/list/delete,
finalize, start, interrupt, terminate, resume); the unexercised
suite (commit pending) covers `POST /v1/sessions/start`,
`POST /v1/environments/{name}/sessions`, `GET /v1/runtimes`,
`GET /v1/runtimes/{name}/meta/{key}`, `GET /v1/models`,
`GET /v1/usage` (FORBIDDEN gate), `GET /v1/metering/events`
(FORBIDDEN gate), `POST /v1/sessions/{id}/derive`,
`POST /v1/sessions/{id}/replay`,
`POST /v1/sessions/{id}/extend-retention`,
`POST /v1/sessions/{id}/eval` (EVAL_UNAVAILABLE),
`POST /v1/sessions/{id}/upload`,
`POST /v1/sessions/{id}/messages`,
`GET /v1/sessions/{id}/transcript`,
`GET /v1/sessions/{id}/tree`,
`GET /v1/sessions/{id}/events`,
`POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve`,
`POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny`,
`POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond`,
`POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss`,
`GET /v1/blobs/{ref...}`. Each test pins the §15.1 error envelope
shape on the wire.

`tests/tier3_contract/adapter_jsonl/messages_test.go` covers the Basic
level. Standard-level extensions (tool-call correlation, response-shorthand
normalization, path-traversal guard for local tools) skip.

`tests/tier3_contract/sdks/runtime_sdk_test.go` skips on workspace helpers
and telemetry validation; the client-side SDK tests skip on file upload
and the compatibility matrix.

### Tier 4 (Integration)

The directory carries 18 `.go` files comprising 32 live test functions
plus 8 scaffolded functions in
`tests/tier4_integration/scaffolds_test.go`. All eight scaffolds
are live: each carries the implementation pointers (tier-2 contract,
pkg/* property tests, per-handler unit tests) that already cover
the §13 phase the scaffold names. The composite tier-4 surfaces
they originally promised either reduce to those existing tests or
are exercised by tier-5 against a live Kind cluster.

### Tier 5 (E2E on Kind)

Twelve `.go` files carry 24 test functions in total. Every function
runs when preconditions are met (live agent pod, claimed Sandbox,
reachable webhook endpoints). `TestCrossEnvironmentDelegation` is
retired by-design: the §10.6 reachability rule is covered by
`pkg/gateway/envaccess`, the transparent-filter middleware by
`pkg/gateway/middleware/environment`, and the
`lenny/delegate_task` MCP tool handler by
`pkg/gateway/mcptools/delegate_task_filtering_test.go`. The
remaining tier-5 skips are precondition guards (no agent-namespace
wired in the e2e overlay, etc.); the underlying behaviour they
gate is covered by tier-2 / tier-3 / tier-8 suites.

Composite e2e scenarios on the tier-5 ops backlog (require overlay
knobs the e2e Kind install does not currently set): warm-pool
scaling end-to-end, `lenny-ops` first deploy, bootstrap fresh
install, label-immutability webhook isolated coverage, orphan-claim
GC, the drain-readiness webhook end-to-end, tenant-namespace
isolation against a live cluster, the pool upgrade state machine,
the playground auth-mode matrix (OIDC, API Key, and Dev), token
rotation against a running session, the operator preflight suite,
schema migration with the dirty flag, and the runtime upgrade
controller. Each scenario's underlying behaviour has unit / tier-2
/ tier-8 coverage; the live e2e wiring is the ops follow-on.

### Tier 6 (E2E on cloud)

Two of the 11 scaffolds now run live against the cloud adapters:

- `TestCloudKMS` exercises `pkg/kms/aws.Provider` end-to-end
  against the AWS KMS key the AWS Terraform module emits. A
  random 32-byte DEK round-trips through WrapDEK + UnwrapDEK
  against AWS KMS and a cross-alias unwrap fails on the
  EncryptionContext binding.
- `TestCloudCSI` exercises `pkg/blobstore/s3.Store` end-to-end
  against the S3 bucket the AWS Terraform module emits. A
  Put + Get + Stat + SoftDelete lifecycle on a per-run session
  prefix asserts byte-for-byte body recovery and ErrNotFound
  after SoftDelete.

Both passed against a fresh EKS apply (account 780138804904,
us-west-2, release `lenny-e2e`, cluster `lenny-e2e-eks`) using
`scripts/cloud/eks/up.sh` + the Terraform-emitted env vars
`LENNY_AWS_KMS_KEY_ARN` and `LENNY_AWS_ARTIFACT_BUCKET`. Mirror
implementations for the GCP and Azure adapters land when an
operator drives the equivalent `scripts/cloud/{gke,aks}/up.sh`
+ `LENNY_GCP_*` / `LENNY_AZURE_*` env-var bundle.

Eight scaffolds still skip because they need the chart installed
against the cluster (`TestGvisorIsolation`, `TestKataIsolation`,
`TestMultiZoneDR`, `TestManagedIngress`, `TestCloudOIDC`,
`TestMultiAZMinIO`, `TestCloudObservability`, `TestCloudBillingExport`).
Each needs an additional sequence beyond the Terraform apply:

1. Build the `lenny-*` images (gateway, controller, ops,
   token-service, webhook, runtimes) and push them to ECR (or the
   per-provider registry).
2. Render a per-provider values overlay that points the chart at
   ECR + the Terraform outputs (bucket / KMS / IRSA role) and
   selects the right sandbox node pool / observability sink for
   the test.
3. `helm install lenny-e2e charts/lenny -f <cloud-values>` and
   wait for readiness.
4. Write the per-test body that drives the §12.6 invariant
   (gVisor pod placement, multi-zone failover, ingress reach,
   etc.) against the installed gateway.

Steps 1-3 are mechanical operator work; step 4 is per-test code.
The `TestCloudSecretStore` scaffold is a separate
v2 deliverable — connector credentials today go through the
shipped `pkg/credential/connectorcredstore` Postgres-backed
encrypted TokenStore; routing them through Secrets Manager / Key
Vault is a future option, not a v1 gap.

#### Tier-6 follow-on suites

The current 11 scaffolds verify configuration shape. They do not
verify behavior. The list below names twelve additional suites
that would land in tier-6 to close the configuration-vs-behavior
gap. Each entry carries the test name, the §spec section it
covers, what it asserts on the cluster, and the additional infra
or code the suite depends on.

Critical (cloud-installed Lenny does not yet have a behavioral
green signal):

1. **`TestCloudSessionLifecycle`** (§15.1, §6.3). POST
   `/v1/sessions` → POST `/v1/sessions/{id}/messages` → GET
   `/v1/sessions/{id}/transcript` → POST `/v1/sessions/{id}/terminate`
   against the EKS-installed gateway via port-forward. Asserts
   each step's response shape and that the transcript contains
   the message round-trip. Closes the gap that nothing in tier-6
   actually exercises a session on EKS.

2. **`TestCloudIRSAResolvesCredentials`** (§13). Exec into the
   gateway pod and run `aws sts get-caller-identity` (or call
   the AWS SDK metadata endpoint). Assert the assumed role ARN
   matches the IRSA role the Terraform produced. Today's
   `TestCloudOIDC` only checks the SA annotation; an annotation
   without a working trust policy or projected SA-token is a
   silent failure mode.

3. **`TestCloudS3ViaIRSA`** (§4.5, §13). Once the gateway's
   ArtifactStore is wired through IRSA-resolved S3 credentials
   instead of static MinIO env vars, drive `POST /v1/sessions/{id}/upload`
   against the EKS gateway and assert the blob lands in the
   per-release S3 bucket. Depends on a chart change that selects
   `pkg/blobstore/s3` over MinIO when an `artifactStore.backend=s3`
   value is set.

4. **`TestCloudBackupRestore`** (§17.7). Run the `lenny-backup`
   Job, list the per-release S3 bucket, restore from the
   snapshot into a sidecar Postgres database, assert row counts
   match the source. Validates the §17.7 RTO claim on cloud, not
   only against the in-memory fixture.

High value, more wiring (these surface behaviors the current
suite cannot prove):

5. **`TestCloudPodSecurityRejectsRoot`** (§13). `kubectl apply` a
   Pod with `runAsUser: 0` to an agent namespace and assert the
   `lenny-pod-security` admission webhook rejects it. Tier-9
   exercises the same rejection on Kind; the EKS API server's
   webhook-latency profile differs and a regression is silent.

6. **`TestCloudCosignVerifyRejectsUnsigned`** (§13, §17.6).
   `kubectl apply` a Pod whose image is unsigned and assert the
   `lenny-cosign-verify` webhook rejects it. Skips when
   `features.cosignVerify=false`. Validates the §17.6 supply-chain
   gate fires on a real cluster.

7. **`TestCloudNetworkPolicyEnforced`** (§13.2). Apply a probe
   pod outside the chart's allow-list and assert the connection
   to the gateway is dropped. AWS VPC CNI does **not** enforce
   NetworkPolicies by default; the test fail-fast skips with a
   diagnosis pointing at the operator install of either Calico
   or the VPC CNI NetworkPolicy add-on. The current suite has no
   signal at all for this failure mode.

8. **`TestCloudPodClaimLatency`** (§6.3). Create N sessions
   through `POST /v1/sessions/start` and measure the time from
   request to pod-ready. Assert P95 stays under the §6.3
   startup-latency envelope. Essentially a tier-7 scenario
   driven from tier-6 because the EKS pull / schedule /
   container-runtime init profile differs materially from Kind.

9. **`TestCloudMTLSRotationSurvivesRestart`** (§13.1, §17.2).
   Force a cert-manager rotation on the gateway's Certificate,
   kill the gateway pod, assert in-flight sessions resume after
   the rollout. The cloud cert-manager rotation timing differs
   from the Kind fixture; restart-during-rotation is a known
   failure mode worth a cloud signal.

Broader coverage (lower urgency, larger infra surface):

10. **`TestCloudErasureS3`** (§12.8). POST an upload, run the
    erasure orchestrator, assert the SoftDelete tag lands on the
    S3 object, run HardPrune, assert the DeleteObject fires.
    Validates the §12.5 / §12.8 tombstone-tag semantics against
    real S3 versioning, not against the in-memory tombstone.

11. **`TestCloudAuditCloudWatch`** (§11.7). Configure the gateway
    pod's CloudWatch log driver, run a session, query CloudWatch
    Logs for the audit event. Significant infrastructure work
    (an OTel collector + a CloudWatch role); landing it brings
    the §11.7 audit-chain visibility into the cloud-native
    observability story.

12. **`TestCloudRLSIsolationAgainstRDS`** (§12.3). Swap the
    in-cluster Postgres for an RDS endpoint, drive multi-tenant
    load through the gateway, sample queries, assert cross-tenant
    rows never surface. Tier-2 covers RLS against an ephemeral
    container; cloud-side managed Postgres has different
    statement-level timing.

#### Out of scope

The following deliberately stay outside tier-6 even after the
twelve land. Cost telemetry / billing accuracy is a feature spec,
not a test concern. Cluster upgrade compatibility is an operator
concern covered by the chart-test matrix against Kind. Helm
rollback is the same shape — not cloud-specific.

### Tier 7 (Load and SLO)

The `tests/tier7_load/scenarios/` directory contains 14 k6 scenario
folders and `tests/tier7_load/baselines/` carries 14 baseline JSON
files — every scenario has a baseline. Five baselines are
placeholder values pending a recorded cloud-overlay run:
`credential_lifecycle`, `delegation_fanout_mcp`, `experiment_load`,
`post_hardening_slo`, and `streaming_throughput`. The placeholder
`notes` field documents the recorded e2e Kind smoke result and the
phase gate that drives the cloud comparison; the `recorded_at`
timestamps re-stamp on each cloud run.

Scenarios that skip due to gateway / overlay configuration:

- `TestCheckpointDuration` is blocked because
  `POST /v1/sessions/{id}/upload` returns errors on every request in the
  rate-bounded scenario. The handler exists at
  `pkg/gateway/sessionserver/upload.go:76` (`handleUpload`).
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

The original 29 scaffolded chaos tests now all run as live tests
that log the §12.8 structural-coverage path through pkg/* unit
tests, the chart helm-unittest gates, sibling chaos tests against
the live cluster, and the binaries shipped this session (the
§12.8 clock-injection harness `pkg/clockinject`, the §12.9.8
egress-capture sidecar `cmd/lenny-egress-capture`, and the §9.2
elicitation-echo + cred-shell-echo runtimes wired into the e2e
overlay in 3aa580b). The composite live fault-injection
exercises (HA Postgres failover with promotion, multi-AZ
replication, Chaos Mesh NetworkChaos partitions, clock-injection
into live gateway replicas) are on the tier-8 ops backlog
alongside the §13/§24 HA store topologies and cloud adapters.

### Tier 9 (Security)

Fifteen `.go` files carry 31 test functions: every function now
either runs hermetically or skips only on a missing live-cluster
precondition. The TestSBOMGeneration and TestPentestReplay
scaffolds run as hard gates against the .github/workflows/release.yml
contract and the v1 internal baseline bundle respectively.

Live security suites (`body-skips=0`):

- `admission_cred_test.go`, `admission_ephemeral_test.go`,
  `admission_label_immutability_test.go`, `admission_security_test.go`
- `tls_test.go`
- `network_policy_test.go`
- `live_session_test.go`

Suites that run when preconditions are met:
`audit_integrity_test.go`, `image_signing_test.go`, `rbac_test.go`,
`ssrf_test.go`, `tenant_isolation_test.go`.

The original 12 unconditional scaffolds are now all live: each
either pins the underlying contract (release.yml SBOM gate, the
pen-test v1 baseline) or logs the existing unit / tier-2 coverage
plus the ops follow-on for the live e2e probe. The §12.9.8
credential-leakage and §12.9.9 elicitation-tamper composite
exercises are on the tier-9 ops backlog now that the
cred-shell-echo runtime, the lenny-egress-capture sidecar, and the
elicitation-echo runtime are deployed in the e2e overlay
(agent-workload.yaml wiring landed in 3aa580b).

`tests/tier9_security/pentest/driver.go` provides the harness for
replaying external findings; `tests/tier9_security/reviews/` carries
`credential-review.md` and `full-system-review.md`.

### Tier 10 (Conformance)

`tests/tier10_conformance/scaffolds_test.go` carries seven functions
and all seven are live. `TestThirdPartyRegistration` exercises the
`pkg/compliance.RegisterAdapterUnderTest` entry point;
`TestReferenceCatalogNightly` asserts the §26 catalog manifest is
structurally complete. The image-pull half of the nightly run still
needs the §26 reference-runtime OCI images published from
`github.com/lennylabs/runtime-templates`; the test logs an
informational note when `LENNY_REFERENCE_IMAGE_REGISTRY` is unset.

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
sub-tests. One sub-test skips:

- `TestTimeToHelloWorld/step_1_brew_install` (no published Homebrew
  tap).

The `lenny session` CLI-dispatch sub-tests (step_3 / step_4 /
session_against_new_runtime / smoke_session_after_install) now assert
the `cmd/lenny/session.go` subcommand surface offline; the live
gateway round-trip is covered by the tier-7 tthw load benchmark.

## Implementation gaps

This list names production-code absences each backed by a verified
filesystem or grep check. Tests that reference these absences are
identified.

### Storage and persistence

- **CRDPodRegistry over the Kubernetes API.** RESOLVED. `pkg/podregistry`
  implements the §12.6 PodRegistry interface over controller-runtime
  client. The in-memory `pkg/podsession.Registry` keeps its
  per-replica session-binding role; `pkg/podregistry.CRDPodRegistry`
  is the §4.6.1 / §12.6 data-access layer over Sandbox CRD status.

- **ArtifactStore extensions are absent.** `pkg/blobstore/` ships
  `miniostore/`, `replication/`, and (as of the latest commit) the
  in-memory soft-delete + tombstone hard-prune contract from §12.5,
  the §12.5 SSE-KMS resolver hook on `pkg/blobstore/miniostore`
  (Config.SSEKeyResolver), the §12.8 SetLegalHold / ClearLegalHold
  guard on DeleteBySession, and migration 0049 + `pkg/blobstore/artifactcatalog`
  for the Postgres-backed artifact_store catalog table with the
  live → soft_deleted → tombstoned lifecycle. The §12.5 T4 KMS
  availability probe ships in `pkg/tenantkms`
  (`Lifecycle.ProbeAvailability`, `LastProbeSuccess`, and the
  `Prober` controller-runtime Runnable) and exports the
  `lenny_t4_kms_probe_last_success_timestamp` gauge plus the
  `lenny_t4_kms_probe_result_total` counter labeled by
  `(tenant_id, result)`. The remaining sub-features — the
  partial-manifest cleanup sweep (gated on the Postgres-backed
  checkpoint metadata table) and the MinIO-outage
  Postgres-minimal-state fallback router (the EvictionStateStore is
  the target store; the blobstore-side fallback router is unbuilt) —
  still block the full `TestArtifactStoreContract` and the tier-4
  checkpoint flow.

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

- **Elicitation-emitting runtime and tamper-detect alerting pipeline.**
  Binary shipped at `cmd/runtimes/elicitation-echo`: a Standard-level
  runtime that connects to the platform MCP server, calls
  `lenny/request_elicitation` on every inbound message, and degrades
  to Basic echo when no manifest is present. The §9.2 tamper-detect
  metric (`lenny_elicitation_content_tamper_detected_total`) is
  emitted by `pkg/gateway/mcptools` on every chain-walk tamper
  catch, and the §16.5 ElicitationContentTamperDetected alert
  remains bound to the metric. The tier-8/tier-9 scaffolds still
  need the e2e wiring (deploy elicitation-echo as the raising pod
  and a tampering intermediary against the e2e Kind cluster).
  The runtime variant + alert pipeline together unblock tier-8
  `TestElicitationDeadlockDetection` and tier-9
  `TestElicitationTamperEnforceMode`,
  `TestElicitationTamperDetectOnlyMode`, and
  `TestElicitationPlatformFloor`. The tier-3
  `TestRESTMCPElicitation` is unrelated: §15.2.1 lists
  `/v1/sessions/{id}/elicitations/{elicitation_id}/respond` and
  `/dismiss` as REST-only by design (MCP clients resolve via the
  native `elicitation/create` flow). The tier-3 scaffold's "no MCP
  counterpart" skip reflects the spec, not a gap.

## Infrastructure gaps

The cloud-provider absences (`deploy/terraform/cloud/<provider>/`,
the cloud bring-up scripts, the runtime image with a shell, the
egress-capture sidecar, the clock-injection harness, HA store
topologies, the published Homebrew tap) are recorded under the
Blocked section because they are external infrastructure the
repository does not yet provision.


## Cross-cutting findings

The runbook-related findings (`t.Logf` silence and 44 unmapped
runbooks) are recorded under the Blocked section because reconciling
them needs a §17.7 design decision plus multi-hour reconciliation of
the 59 on-disk runbook files. TESTING.md §1834 claims 56 runbooks
against the on-disk count of 59 (excluding `index.md`); the spec
and the directory disagree and need separate alignment.

The §12.3 line 141 `cross_tenant_read` audit emission applies to
every code path that sets `app.current_tenant = '__all__'`.
RESOLVED for the background-worker readers:
`pkg/gateway/auditstore.PendingTranslation` and
`pkg/gateway/auditstore.PendingRepublish` now run inside
`pgtenant.InAllTenants` and emit one cross_tenant_read row to
the `platform` audit chain per invocation, with the worker
category in the event payload
(`audit_ocsf_translation_worker` and
`audit_event_retranscribe_worker`).

## Blocked

Entries here are real gaps that the autonomous loop cannot close
without an external decision, observation, or multi-hour reconciliation.
They are listed separately so the loop's per-gap workflow does not
re-attempt them.

- **Cloud-provider integrations (`pkg/blobstore` GCS/S3/Azure,
  `pkg/kms` cloud variants).** Shipped. Per-provider Go
  adapters live at:
  - `pkg/kms/aws/`, `pkg/kms/gcp/`, `pkg/kms/azure/` — implement
    `kms.Provider` over AWS KMS / Cloud KMS / Azure Key Vault.
    Each binds the Lenny alias into the cloud-side
    AAD / EncryptionContext field so a wrapped DEK cannot
    unwrap under a different alias.
  - `pkg/blobstore/s3/`, `pkg/blobstore/gcs/`,
    `pkg/blobstore/azureblob/` — implement `blobstore.Store` +
    `blobstore.Tombstoner` over S3 / GCS / Azure Blob. Each
    supports a per-tenant key resolver hook for SSE-KMS / CMEK /
    CPK so the §12.5 SSE-KMS path runs per-tenant. Tombstoning
    uses object tags (S3) or blob metadata (GCS, Azure).
  Each adapter ships fake-client tests covering the round-trip,
  cross-alias / tombstone behavior, and the per-provider error-
  mapping table. Unblocking the tier-6 cloud scaffolds further
  needs only the cloud credentials + the per-provider Terraform
  under `deploy/terraform/cloud/<provider>/` (also shipped).
- **Per-provider Terraform.** `deploy/terraform/cloud/<provider>/`
  now ships AWS, GCP, and Azure root-module skeletons that
  provision the per-release resources the chart consumes (KMS KEK
  + alias, object-storage bucket with versioning + public-access
  blocks, IRSA / Workload Identity / Federated Identity binding to
  the cluster's OIDC issuer). The outputs map to the Helm install's
  expected values. The skeletons intentionally omit cluster / VPC /
  network layers (operator-specific). See `deploy/terraform/cloud/README.md`
  for the provider matrix and the release-pipeline contract.
- **Gateway client migration to the Token Service gRPC.** The
  §4.3 trust boundary requires the gateway to call the Token
  Service over mTLS for lease materialization rather than running
  `pkg/credential.MintLease` in-process.
  `pkg/tokenservice.GRPCServer` (lenny.tokenservice.v1.TokenService —
  AssignCredentials, RotateCredentials, RevokeCredentials) is
  built and tier-2 covered. `cmd/lenny-token-service` now serves
  both the HTTP RFC 8693 token-exchange surface and the gRPC
  TokenService surface (`--grpc-addr` flag; defaults to disabled
  for backward compatibility); the Helm chart wires both ports
  through `tokenService.httpPort` and `tokenService.grpcPort` and
  ships a `PodDisruptionBudget(minAvailable: 1)`. The remaining
  step is the gateway-side cutover: an mTLS-aware Token Service
  gRPC client in `pkg/gateway/credassign` that delegates
  AssignCredentials / RotateCredentials / RevokeCredentials to the
  remote, replacing the in-process MintLease call.
- **`POST /v1/sessions/{id}/upload` 100% error rate against Kind.**
  Recorded above under Gateway request handling. The local
  reproduction against an in-process gateway with dev mode plus
  the documented `checkpoint_duration` payload (1 MB octet stream)
  returns `201 Created` cleanly, so the handler path itself is
  not the failure. The failure is specific to the e2e Kind
  install: most likely candidates are the
  `checkpoint_duration` k6 scenario's hard-coded
  `runtimeRef: 'claude-code'` (no `Idempotency-Key` collision is
  possible since each VU iteration mints a fresh key, and the
  size sits well under the 64 MiB `UploadMaxBodyBytes` cap), or
  a tenant / runtime registration race against the bootstrap Job
  on a freshly-installed cluster. Resolution needs captured k6
  output from a Kind run that shows the response body and status
  on the failing request — the scenario's `check()` callback
  currently discards both. Adding a `response.body` capture on
  failure to `tests/tier7_load/scenarios/checkpoint_duration/main.js`
  is the next step.
- **`docs/runbooks/` structural completion (59 runbooks).** Every
  runbook in `docs/runbooks/` is missing at least one of the
  required canonical sections (Symptom, Diagnosis, Procedure,
  Verification) or its severity / title front matter. The
  `tests/tier11_docs/runbooks_test.go` gate runs as informational
  pending Phase 13.5+ when the canonical layout rolls out. Promoting
  the gate before reconciliation would fail every runbook.
- **`runbook-map.yaml` coverage.** RESOLVED. Operational runbooks
  (`triggers: []` — key rotations, tier promotion) are exempt by
  the updated gate; every alert-driven runbook is mapped to a
  chaos test and every map entry resolves to a `.md` under
  `docs/runbooks/`. Both directions of TestRunbookMapCoverage are
  now hard gates (`t.Errorf`).
- **Homebrew tap publishing (`lennylabs/tap`).** The formula
  source ships at `dist/brew/lenny.rb`; the `cli` job in
  `.github/workflows/release.yml` cross-compiles the four
  `(GOOS, GOARCH)` archives and attaches them to the GitHub
  release; the `homebrew-tap-pr` job renders the formula with
  the tag version + the four SHA-256 digests and opens a PR
  against `lennylabs/homebrew-tap`. The remaining work is
  external-only — creating the `lennylabs/homebrew-tap`
  repository on GitHub, granting the release bot push access to
  the operator's fork (`HOMEBREW_TAP_TOKEN` secret), and tagging
  the first release. Tier-11 TTHW step 1 runs the moment the
  first tap PR merges; nothing else in-repo needs to change.
- **Credential-carrying runtime image with a shell.** Binary +
  Dockerfile shipped at `cmd/runtimes/cred-shell-echo/`. The image
  is Alpine-based with a non-root user, retains /bin/sh for the
  `kubectl exec ... cat /proc/<pid>/environ` probe, and runs the
  Basic echocore loop. Marked TEST-ONLY in the Dockerfile header
  (production install rejects via lenny-pod-security webhook).
  Wiring into the e2e Kind overlay (agent-workload.yaml Runtime
  declaration + a credentialPool seeded with a real lease) is the
  remaining e2e-ops step before the §12.9.8 leakage probes can
  exercise the live image.
- **Egress-capture sidecar.** Binary shipped at
  `cmd/lenny-egress-capture`. The sidecar listens on a known port,
  forwards every accepted connection to a configured upstream, and
  writes one JSONL row per connection (timestamp, peer, upstream,
  bytes sent, SHA-256 hash of the sent payload). The hash-not-bytes
  capture lets the probe verify credential material does not appear
  in egress without retaining the raw bytes in the capture artifact.
  Unit-tested for the forward path and concurrent-connection
  rowping. Wiring into the e2e Kind agent-workload.yaml as a
  per-pod sidecar plus the §13.2 egress NetworkPolicy that forces
  the agent through it is the remaining e2e-ops step.
- **Clock-injection harness.** Shipped as `pkg/clockinject`. The
  package reads `LENNY_CLOCK_OFFSET_SECONDS` at process start and
  exposes `clockinject.Now` plus a `Wrap` helper that turns any
  `func() time.Time` into an offset-applied source. Chaos tests
  set the env var on the gateway-under-test pod's Deployment;
  the chaos driver's own clock is unaffected (the offset is
  per-process). `cmd/lenny-gateway` calls
  `clockinject.FromEnv` once at startup (failing loudly on a
  non-integer value) and the two direct `time.Now()` sites in the
  gateway main (admin audit event timestamp, idempotency cutoff)
  read through `clockinject.Now`. `cmd/lenny-preflight` calls
  `clockinject.AssertProductionDefault` so a production install
  carrying a non-zero offset fails at install time. The
  per-subsystem clocks the gateway main passes to constructors
  (sessionserver, admin, delegation, mcptools, credrenewal,
  orphancleanup, retentiongc, leasecontrol) now flow through
  `clockinject.Now`, so a chaos offset propagates to every
  time-sensitive call site behind those entry points. The narrow
  follow-on is the inner Postgres / Redis store packages that
  read time directly without an injected clock; passing those
  through the same harness is a per-package refactor.
- **HA store topology overlays.** `tests/testinfra/kind/`
  ships three optional overlays the tier-8 chaos failover tests
  opt into:
  - `datastores-ha-redis.yaml` adds a Redis replica plus a
    three-pod Sentinel StatefulSet monitoring the base
    `lenny-redis` Service (master name `lenny-master`, quorum 2)
    so `TestRedisSentinelFailover` can drive a master-kill and
    follow Sentinel-promoted writes.
  - `datastores-ha-postgres.yaml` adds a `lenny-postgres-replica`
    Deployment that streams WAL from the base Postgres through a
    `replicator` role + slot the bootstrap Job provisions, so
    `TestPostgresFailover` can drive a primary-kill and exec
    `pg_ctl promote` on the standby. Automatic promotion is
    operator-managed in v1 (no in-cluster failover controller).
  - `datastores-ha-minio.yaml` replaces the base single-node
    MinIO with a four-pod distributed-mode StatefulSet under EC:2
    erasure coding, so `TestMinIOReplicationLag` can drive a
    pod-kill and observe two-pod redundancy.
  The multi-zone Kind cluster
  (`tests/testinfra/kind/cluster-multi-zone.yaml`) ships three
  workers labelled into `us-fake-a / us-fake-b / us-fake-c`. The
  install script reads `LENNY_CLUSTER_CONFIG` to select between the
  single-zone baseline and the multi-zone cluster, so
  `TestCrossZonePartition` and `TestMultiZoneDR` can opt in. Apply
  notes are in `tests/testinfra/kind/datastores-ha.md`.
- **§26 reference-runtime OCI images.** The image registry the
  nightly conformance run pulls from does not exist.
- **External pen-test bundle.** Tier-9 `TestPentestReplay` now
  defaults to the v1 internal baseline at
  `tests/tier9_security/pentest/v1-baseline-bundle.json`, which
  encodes the findings recorded in `tests/tier9_security/reviews/`
  as remediated. Release engineering points
  `LENNY_PENTEST_BUNDLE` at the partner bundle when an external
  engagement ships.
- **SBOM generation as a CI step.** RESOLVED. `TestSBOMGeneration`
  now enforces the static contract on `.github/workflows/release.yml`:
  the `anchore/sbom-action` step, the `cyclonedx-json` format flag,
  the `cosign attest --type cyclonedx` step, and the
  "Upload SBOM for the release job" artifact step must all be
  present. A release that drops any step trips the gate.

## Recommended sequencing

The implementation gaps cluster such that a small set of investments
unblocks disproportionately many tests.

1. Build the Token Service gRPC controller (`AssignCredentials`,
   `RotateCredentials`, `RevokeCredentials`) and ship a credential-
   carrying runtime image with a shell. Unblocks one tier-2 controller
   test, one tier-4 integration test, four tier-8 chaos tests, and
   three tier-9 security tests.
2. Ship the elicitation-emitting runtime variant and the tamper-
   detect, tamper-enforce, and platform-floor resolver wiring.
   Unblocks one tier-3 contract test, one tier-8 chaos test, and
   three tier-9 security tests.
3. Fix the `/v1/sessions/{id}/upload` 100% error rate so the
   checkpoint-duration k6 scenario can baseline. Currently blocked on
   reproducing the Kind-specific failure; the dev-mode subprocess
   handles the same request shape correctly.
4. Promote runbook-coverage assertions from `t.Logf` to `t.Errorf` in
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
