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
seven tests live (`TestRESTMCPSessionLifecycle`, `TestRESTMCPTasks`,
`TestRESTMCPRetryableFlags`, `TestRESTMCPMemory`, plus the three
§15.2.1 by-design no-parity acknowledgements:
`TestRESTMCPElicitation`, `TestRESTMCPDelegation`,
`TestRESTMCPAdmin`). Two tests still skip on missing surfaces:
`TestRESTMCPWorkspaceUpload` (MCP upload tool unbuilt) and
`TestRESTMCPWebhookSubscription` (webhook CRUD unbuilt on either
surface).

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
  live → soft_deleted → tombstoned lifecycle. The remaining
  sub-features — the partial-manifest cleanup sweep, the T4 per-tenant
  KMS-availability probe, and the MinIO-outage Postgres-minimal-state
  fallback router (the EvictionStateStore is the target store; the
  blobstore-side fallback router is unbuilt) — still block the full
  `TestArtifactStoreContract` and the tier-4 checkpoint flow.

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
  `pkg/kms` cloud variants, per-provider Terraform).** Documented in
  BUILD-PROGRESS.md Phase 12a as deferred via `CloudProviderSeam`;
  the §18 build sequence treats cloud adapter implementations as v2
  deliverables. Tier 6 scaffolds carry "blocked:" reasons that name
  the missing adapter precisely. Unblocking needs cloud-provider
  credentials and provider-specific Terraform under
  `deploy/terraform/cloud/<provider>/`.
- **Gateway client migration to the Token Service gRPC.** The
  §4.3 trust boundary requires the gateway to call the Token
  Service over mTLS for lease materialization rather than running
  `pkg/credential.MintLease` in-process. `pkg/tokenservice.GRPCServer`
  (lenny.tokenservice.v1.TokenService — AssignCredentials,
  RotateCredentials, RevokeCredentials) is now built and tier-2
  covered, but the gateway still calls `pkg/gateway/credassign`
  in-process. Cutting over needs a `cmd/lenny-token-service` binary
  that serves the gRPC, an mTLS-aware client in the gateway, and a
  Helm chart Deployment with `PodDisruptionBudget(minAvailable: 1)`.
- **`POST /v1/sessions/{id}/upload` 100% error rate against Kind.**
  Recorded above under Gateway request handling. Resolution needs
  captured k6 output from a Kind run.
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
- **No published Homebrew tap (`lennylabs/tap`).** Tier-11 TTHW
  step 1 cannot run until the tap and formula are published.
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
  per-process). The §17.6 preflight Job will call
  `AssertProductionDefault` to refuse install when the env var
  is non-zero in a production cluster. Wiring time-sensitive call
  sites in the gateway to read through `clockinject.Now` is the
  remaining gateway-refactor step recorded as a follow-on.
- **No HA store topologies in the e2e overlay.** HA Postgres,
  Redis Sentinel, multi-zone MinIO, and cross-zone cluster
  topologies are not deployed; the corresponding tier-8 chaos
  failover tests cannot run.
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
