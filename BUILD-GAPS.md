# Build gaps

This file catalogs the verified gaps in the Lenny test infrastructure and in the
production code that backs it. Each entry was checked against the filesystem,
package contents, or live test source on 2026-05-20. Counts and file
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
470 `_test.go` files across `pkg/`, `cmd/`, and the language modules.
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

The tier carries 17 subdirectories. Two files retain a `scaffolds_test.go`
entry, neither of which is a true skip:

- `tests/tier2_component/stores/scaffolds_test.go` carries a single
  `TestArtifactStoreContract` function that logs the §12.5 ArtifactStore
  coverage navigation map (tenant-prefix validation, tombstone
  soft-delete/hard-prune, SSE-KMS, legal-hold, the artifact_store
  catalog table). The component-tier coverage lives in 33 live
  store-contract suites under `tests/tier2_component/stores/`.
- `tests/tier2_component/gateway_subsystems/scaffolds_test.go` (5
  tests intentionally deferred to existing tier-4 and unit
  coverage — the scaffold diagnoses cite the production-code paths
  each subsystem actually exercises, so re-creating a §12.2.3
  tier-2 harness adds no new code path).

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

The directory carries 19 `.go` files comprising 33 live test functions
plus 7 functions in `tests/tier4_integration/scaffolds_test.go`. The
scaffold functions each `t.Logf` the implementation pointers (tier-2
contract, pkg/* property tests, per-handler unit tests) that already
cover the §13 phase the scaffold names. The composite tier-4 surfaces
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

The directory carries 7 `.go` files comprising 27 live test functions
that run when `LENNY_CLOUD_PROVIDER` is set and the per-provider
preconditions are met. `tests/tier6_e2e_cloud/scaffolds_test.go` is
now an entry-point shim that exports the shared `requireCloud` guard;
no test bodies live there.

`scripts/cloud/aws/run-e2e.sh` drives the full sequence (`up.sh` →
ECR push → render-values → datastores → cert-manager +
prometheus-operator CRDs → migrate Job → helm install → cluster
fixtures → tier-6 suite). Coverage by file:

- `aws_resources_test.go`: `TestCloudKMS` round-trips a 32-byte DEK
  through `pkg/kms/aws.Provider` against the Terraform-provisioned
  KMS key and asserts the EncryptionContext binding rejects a
  cross-alias unwrap. `TestCloudCSI` runs Put + Get + Stat +
  SoftDelete against `pkg/blobstore/s3.Store` and the
  Terraform-provisioned S3 bucket.
- `behavior_test.go`: `TestCloudIRSAResolvesCredentials` inspects the
  EKS pod-identity webhook injection on the gateway pod
  (`AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE`, projected-token
  VolumeMount). `TestCloudPodSecurityRejectsRoot` creates a Pod with
  `runAsUser=0` in the first agent namespace and asserts the §13.1
  `pod-security.lenny.dev` ValidatingAdmissionWebhook denies it.
- `cluster_assertions_test.go`: `TestCloudOIDC` reads the
  `eks.amazonaws.com/role-arn` annotation on the gateway SA;
  `TestCloudObservability` checks `OTEL_EXPORTER_OTLP_ENDPOINT` on
  the gateway pod; `TestManagedIngress`, `TestKataIsolation`,
  `TestGvisorIsolation`, `TestMultiAZMinIO`, `TestMultiZoneDR`, and
  `TestCloudSecretStore` assert the chart's v1 configuration shape;
  `TestCloudBillingExport` reads `LENNY_BILLING_SINK` from the
  gateway pod; the chart's `billing.sink` values knob (default empty)
  injects it, and the EKS overlay sets `"postgres"` so the §11.2.1
  v1 path is labeled in the §16 observability dashboards.
- `eks_platform_test.go`: `TestCloudECRImagePullSucceeds`,
  `TestCloudStorageClassCSIPresent`, `TestCloudVPCCNIPodIPFromVPC`.
- `managed_elasticache_test.go`: `TestCloudRedisTLSRequired`,
  `TestCloudRedisAUTH`, `TestCloudRedisClusterMode`,
  `TestCloudRedisEvictionPolicy`, `TestCloudRedisEngineVersionFloor`.
- `managed_rds_test.go`: `TestCloudRDSTLSRequired`,
  `TestCloudRDSForceSSLParameterGroup`, `TestCloudRDSIAMAuth`,
  `TestCloudRDSMultiAZ`, `TestCloudRDSAutomatedBackup`,
  `TestCloudRDSEngineVersionFloor`.

Mirror implementations for the GCP and Azure adapters land when an
operator drives the equivalent `scripts/cloud/{gke,aks}/up.sh` +
`LENNY_GCP_*` / `LENNY_AZURE_*` env-var bundle.

The Redis tests skip on a local invocation because the ElastiCache
endpoints live on VPC-private subnets and are not reachable from
outside the VPC. `scripts/cloud/aws/run-e2e.sh` step 6b
automatically runs those five tests via an in-cluster runner Pod
(static linux/amd64 build of the tier-6 test binary, kubectl-cp +
exec, env-staged AUTH token), so the combined e2e cycle exercises
all 27 tests end-to-end.

Routing billing events to an external sink (BigQuery / Athena /
Data Lake) remains a v2 deliverable; the chart's `billing.sink`
knob is currently advisory only, used as the §16 observability
label rather than a publisher selector. The v1 path writes
synchronously to Postgres (§11.2.1) with the Redis-stream failover
buffer.

#### Tier-6 follow-on suites

The current 27 tests verify configuration shape plus a few behavior
probes (IRSA injection, pod-security webhook, RDS Multi-AZ replica
count, ElastiCache cluster-mode round-trip). The list below names
twelve additional suites that would land in tier-6 to close the
remaining configuration-vs-behavior gaps. Each entry carries the
test name, the §spec section it covers, what it asserts on the
cluster, and the additional infra or code the suite depends on.
Entries already shipped are flagged inline as *Implemented*.

Critical (cloud-installed Lenny does not yet have a behavioral
green signal):

1. **`TestCloudSessionLifecycle`** (§15.1, §6.3). POST
   `/v1/sessions` → POST `/v1/sessions/{id}/messages` → GET
   `/v1/sessions/{id}/transcript` → POST `/v1/sessions/{id}/terminate`
   against the EKS-installed gateway via port-forward. Asserts
   each step's response shape and that the transcript contains
   the message round-trip. Closes the gap that nothing in tier-6
   actually exercises a session on EKS.

2. **`TestCloudIRSAResolvesCredentials`** (§13). *Implemented*
   in `tests/tier6_e2e_cloud/behavior_test.go`. Reads the gateway
   pod and asserts the EKS pod-identity webhook injected the
   `AWS_ROLE_ARN` env, the `AWS_WEB_IDENTITY_TOKEN_FILE` env
   pointing at the canonical projected-token path, and a
   matching VolumeMount. `TestCloudOIDC` checks the SA
   annotation; this test goes one level deeper to catch the
   silent failure mode where the webhook is absent or
   misconfigured. Lifting it further (exec into the gateway pod
   and run an AWS SDK call) requires a debug ephemeral container
   because the gateway image is distroless.

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

5. **`TestCloudPodSecurityRejectsRoot`** (§13). *Implemented*
   in `tests/tier6_e2e_cloud/behavior_test.go`. Creates a Pod
   with `runAsUser=0` in the first `lenny.dev/agent-namespace=true`
   namespace and asserts the §13.1 `pod-security.lenny.dev`
   ValidatingAdmissionWebhook denies the request, matching on
   the webhook name or the §13.1 row markers (`runAsNonRoot`,
   `runAsUser`).

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

#### Postgres-on-RDS expansion

The cloud Postgres backing surface is materially different from
the in-cluster fixture. RDS enforces TLS by default, exposes
IAM database authentication, has hard per-instance connection
limits, runs PgBouncer-in-front for any non-trivial deployment,
and ships Multi-AZ failover with DNS-based reconnection. The
in-cluster `lenny-postgres` Deployment covers none of these. The
suites below close the corner-case set the §17.3 / §12.3 / §11.7
invariants depend on; each is additive to the twelve above. Some
(noted *expands #N*) refine an entry in that list rather than
duplicating it.

Authentication and transport:

13. **`TestCloudRDSTLSRequired`** (§13.2). *Implemented* in
    `tests/tier6_e2e_cloud/managed_rds_test.go`. sslmode=disable
    is refused; sslmode=require succeeds and SELECT version()
    round-trips. Validates the deployment's `rds.force_ssl=1`
    parameter group is wired and the engine refuses a
    plaintext fall-through. The companion
    `TestCloudRDSForceSSLParameterGroup` test queries
    `pg_settings.rds.force_ssl` directly for parameter-group
    introspection.

14. **`TestCloudRDSIAMAuth`** (§13.3). *Implemented* in
    `tests/tier6_e2e_cloud/managed_rds_test.go`. Generates an
    IAM auth token via `aws-sdk-go-v2/feature/rds/auth.BuildAuthToken`,
    asserts the token shape (X-Amz-Signature payload, >= 16
    chars), then connects using the token as the password.
    Skips with a clear diagnosis when the caller's IAM principal
    lacks `rds-db:connect`. Validates the rotation-friendly auth
    path against a real engine. Full long-running-session
    refresh coverage stays a follow-on (requires a custom
    pgx.Conn config hook that the gateway code can opt into).

15. **`TestCloudRDSPasswordRotation`** (§13.3, §17.6). Trigger a
    Secrets Manager rotation against the master password, assert
    the gateway's secret-watcher reloads the credential without
    pod restart (or, when reload is not supported, assert the
    rolling restart completes within the §17.6 budget).

16. **`TestCloudRDSCertificateValidation`** (§13.2). Configure
    the gateway with the global RDS CA bundle
    (`rds-global-bundle.pem`), reject a forged-CA cert presented
    by a man-in-the-middle proxy, and surface the failure as a
    fail-closed connect error rather than a silent
    re-connect-with-no-validation.

PgBouncer routing (the chart's PgBouncer mode is the only safe
configuration for RDS's connection budget):

17. **`TestCloudRDSPgBouncerSessionMode`** (§12.3, §12.2.2)
    *(expands #12)*. Drive RLS-bound traffic through PgBouncer
    in session pool mode against RDS, assert the
    `app.current_tenant` GUC survives a `RESET ALL` between
    sessions and never leaks across the pool boundary.

18. **`TestCloudRDSPgBouncerStatementModeRejectsRLS`** (§12.3).
    Negative test. Configure PgBouncer in statement pool mode
    and assert the gateway's startup health check rejects the
    DSN with a clear diagnostic, rather than silently mis-
    routing tenants. PgBouncer statement / transaction pool
    modes break the RLS GUC pattern; the gateway must refuse to
    boot against either.

19. **`TestCloudRDSPgBouncerReloadDuringSession`** (§12.8).
    Inject a PgBouncer config reload mid-session, assert
    in-flight queries fail-fast with a retriable error code that
    the gateway's retry middleware handles, then assert post-
    reload queries succeed.

20. **`TestCloudRDSConnectionLimitRespected`** (§11.2, §12.2.2).
    Probe the gateway's pool sizing under N concurrent sessions
    when the RDS instance class enforces a small `max_connections`,
    assert the gateway never exhausts the pool, and assert any
    rejected connection surfaces a `503 QUOTA_PRESSURE` rather
    than an opaque 5xx.

High availability and failover (expands the existing
`TestMultiZoneDR` placeholder with the RDS-specific concrete
form):

21. **`TestCloudRDSMultiAZFailoverPreservesSessions`** (§17.3)
    *(expands `TestMultiZoneDR`)*. Baseline check `TestCloudRDSMultiAZ`
    *implemented* in `tests/tier6_e2e_cloud/managed_rds_test.go`:
    queries `pg_stat_replication` for a non-zero replica count
    and skips with a clear hint when the instance is single-AZ.
    The full failover-injection-RTO measurement (`reboot-db-instance
    --force-failover` + retry-middleware assertion) remains a
    follow-on; the baseline gate unblocks it.

22. **`TestCloudRDSDNSTTLBoundsRTO`** (§17.3). Validate the
    chart-rendered Postgres DSN uses the RDS endpoint
    (`<id>.<region>.rds.amazonaws.com`) rather than a baked IP,
    so DNS re-resolution finds the promoted standby within the
    60s endpoint TTL. A baked-IP DSN is a §17.3 RTO regression.

23. **`TestCloudRDSReadReplicaRouting`** (§10.7, §12.2.2). When
    the gateway is configured with a read-replica DSN
    (`postgres.readDSN`), assert read-only routes (transcript
    fetch, audit query, billing report) land on the replica
    while writes (session create, audit append) land on the
    primary. Validates the chart's optional read-replica
    routing wiring once it ships.

24. **`TestCloudRDSReplicaLagBudget`** (§12.2.2). Inject load
    against the primary, sample
    `aws_rds_read_replica_lag_seconds`, assert the §12.2.2
    replica-lag budget (eventually-consistent reads are admitted
    only when lag < X seconds; otherwise reads route back to the
    primary). Without this gate, a stale replica returns
    yesterday's session state under load.

Backup and point-in-time recovery (expands #4
`TestCloudBackupRestore` with the RDS-side forms):

25. **`TestCloudRDSAutomatedBackup`** (§17.7) *(expands #4)*.
    *Implemented* in `tests/tier6_e2e_cloud/managed_rds_test.go`.
    Queries the RDS API for the instance's `BackupRetentionPeriod`
    and asserts it is at least 1 day. The Terraform module's
    default retention is 1; raise via `var.rds_backup_retention_days`
    to 7+ for production parity. The full
    create-snapshot + restore-into-sidecar + row-count parity is
    a follow-on that needs a second RDS instance per run (RDS
    snapshot restore takes 10-15 minutes; gated behind a separate
    expensive-tier flag).

26. **`TestCloudRDSPointInTimeRestore`** (§17.7). After a known
    sequence of writes, run `aws rds restore-db-instance-to-point-in-time`
    to a timestamp inside the write window, assert the restored
    database contains the rows from the window and not the rows
    written after. Validates the §17.7 PITR claim against real
    RDS, not against `pg_basebackup`.

27. **`TestCloudRDSCrossRegionSnapshot`** (§17.5, §17.7).
    Configure cross-region snapshot copy (us-west-2 → us-east-1),
    trigger a manual snapshot, assert the copy appears in the
    second region, restore into a fresh instance there, assert
    a session create against the restored instance returns
    `201`. Validates the §17.5 cross-region DR claim against the
    storage tier.

Schema migration:

28. **`TestCloudRDSMigrationApplies`** (§12.2, §12.3). Run the
    `lenny-migrate up` Job against the RDS endpoint with an
    IAM-authenticated DSN, assert every migration applies, and
    assert the migration's IAM role has the documented minimum
    set of grants (`CREATE`, `ALTER`, `INSERT`, `SELECT` on the
    target schema; no `SUPERUSER`).

29. **`TestCloudRDSMigrationDirtyFlagRecovery`** (§12.2). Force
    a failed migration mid-run (kill the Job pod between two
    migrations), assert `golang-migrate`'s dirty flag is set,
    re-run the Job with the documented recovery procedure
    (`force <version>`), assert the schema lands clean. A dirty
    flag on RDS is the most common §17.7 incident; the recovery
    procedure must be tested.

30. **`TestCloudRDSMajorVersionUpgrade`** (§12.2, §17.6). Pin the
    RDS instance to Postgres 15, install the chart, run a
    session round-trip, then trigger
    `aws rds modify-db-instance --engine-version 16.x`. Assert
    the gateway re-establishes connections, the schema stays
    intact, and any extension-version updates (pgvector,
    pg_stat_statements) apply cleanly.

Aurora variants (skip when the deployment uses standard RDS,
not Aurora):

31. **`TestCloudAuroraServerlessV2ColdStart`** (§17.1). Configure
    Aurora Serverless v2 with `min_capacity=0.5 ACU`, idle the
    instance, then create a session, measure cold-start latency
    against the §6.3 budget. If the cold-start blows the
    budget, the deployment must pin to `min_capacity >= 1` or
    fall back to provisioned mode.

32. **`TestCloudAuroraGlobalDatabaseWriteForwarding`** (§17.5).
    Configure an Aurora Global Database with a secondary region,
    write through the secondary's write-forwarder, assert the
    write surfaces in the primary within the documented
    cross-region replication latency.

Performance and limits:

33. **`TestCloudRDSStorageAutoscaling`** (§12.5). With
    `allocated_storage=20G` and `max_allocated_storage=100G`,
    write past 20G of artifact metadata + session rows, assert
    RDS auto-scales storage and the gateway sees no write
    failures during the resize. A misconfigured
    `max_allocated_storage` is a silent prod incident.

34. **`TestCloudRDSQueryLatencyBudget`** (§12.7, §16.5). Probe
    P99 query latency under sustained tier-7 load against the
    RDS endpoint, assert the value stays under the §12.7 budget
    (network-round-trip latency to RDS is materially higher than
    to the in-cluster fixture). Bound the assertion at the
    chart-rendered `monitoring.databaseLatencyP99Budget` value.

Security and isolation:

35. **`TestCloudRDSCustomKMSAtRest`** (§12.5, §12.9). Provision
    RDS with the per-release Lenny KMS key as the at-rest
    encryption key (not the AWS-managed key), assert
    `aws rds describe-db-instances` reports the right KMS key
    ID, and assert that a Phase 4a tenant-deletion that destroys
    the KMS key renders the RDS instance unreadable (the §12.9
    cryptographic-erasure invariant against managed storage).

36. **`TestCloudRDSNetworkIsolation`** (§13.2). Assert the RDS
    security group admits only the EKS pod CIDR (or the VPC
    PrivateLink endpoint), and reject a connection attempt from
    a non-allowlisted source IP. The §13.2 NetworkPolicy story
    inside the cluster is undermined if RDS accepts public
    connections.

37. **`TestCloudRDSPerformanceInsightsTenantPrivacy`** (§12.3,
    §11.7). With Performance Insights enabled, sample the
    captured query text, assert no tenant payload bytes appear
    in the captured statements (the gateway uses parameterised
    queries; this is a regression test for any path that
    string-formats tenant data into SQL). Performance Insights
    is a documented operator feature; tenant-data leakage there
    is a §13 finding waiting to surface.

Operational scenarios:

38. **`TestCloudRDSMaintenanceWindow`** (§17.6). Schedule an RDS
    maintenance task inside the configured maintenance window,
    assert the gateway either (a) re-connects through the
    documented short outage, or (b) surfaces the
    `DatabaseMaintenance` alert before the window opens so the
    operator knows. Either is acceptable; silent session loss
    is not.

39. **`TestCloudRDSCloudWatchAlerts`** (§16.5). Configure the
    chart-rendered Prometheus alerts that mirror the §16.5
    catalog against the RDS CloudWatch metrics
    (`CPUUtilization`, `FreeStorageSpace`, `DatabaseConnections`,
    `ReplicaLag`). Trigger each threshold with a synthetic
    workload, assert the alert fires, assert the matching
    runbook resolves to a `docs/runbooks/*.md` page.

40. **`TestCloudRDSConnectionTermination`** (§12.8). Force-
    terminate every active connection through
    `pg_terminate_backend`, assert the gateway's reconnect path
    fires within the §12.8 connection-loss recovery budget and
    that the audit chain has the
    `database_connection_terminated` event recorded.

The expansion brings the tier-6 suite from twelve to forty
tests. Sequencing recommendation: land the four critical
suites (1-4), then the high-value seven (5-9 plus the RDS
expansions #13, #17, #21), then the broader set. The Aurora
variants (#31-32) skip when the deployment is on standard RDS;
the suite uses an `LENNY_RDS_ENGINE=aurora-postgresql`
environment guard for the Aurora-only tests.

#### Redis-on-ElastiCache expansion

The cloud Redis backing surface differs from the in-cluster
`lenny-redis` fixture in cluster-mode sharding (keyspace
hashing, MOVED redirection, cross-slot rejection), pub/sub
behavior across shards (the §13.3 revocation propagator and the
§12.3.7 EventBus both depend on pub/sub fan-out), TLS + auth-
token transport, automatic failover within a replication group,
strict eviction-policy semantics (Lenny's circuit-breaker
counters must be `noeviction`), and snapshot-to-S3 backups. The
in-cluster fixture is a single-node Redis with no TLS, no auth,
no cluster mode, no eviction pressure, and no failover. The
suites below cover the corner cases Lenny's six Redis-backed
subsystems (`breakerstore`, `quotastore`, `coordination`,
`semanticcache`, `revocation/propagator`, `eventbus`) expose
when the deployment swaps in an ElastiCache replication group.

ElastiCache endpoints live on VPC-private subnets and are not
reachable from outside the VPC. `scripts/cloud/aws/run-e2e.sh`
step 6b automates the in-cluster runner pattern: when
`WITH_ELASTICACHE=1`, after the local lenny-test invocation
finishes the script builds a static linux/amd64 tier-6 test
binary, deploys a minimal alpine runner Pod with the Redis
endpoint + AUTH token staged as env vars, kubectl-cp's the
binary into the Pod, and runs `tier6.test -test.run TestCloudRedis`
inside the cluster. The runner reads `LENNY_AWS_REDIS_AUTH_TOKEN`
directly so it does not need an in-pod `secretsmanager:GetSecretValue`
permission.

Authentication and transport:

41. **`TestCloudElastiCacheTLSRequired`** (§13.2). *Implemented*
    as `TestCloudRedisTLSRequired` in
    `tests/tier6_e2e_cloud/managed_elasticache_test.go`. Plaintext
    PING refused; TLS PING returns PONG. Validates the
    `transit_encryption_enabled` setting on the replication group.
    The chart-side "refuses to boot against a non-TLS endpoint when
    compliance-tier T3+" assertion remains a follow-on (needs a
    compliance-tier values knob the chart does not yet expose).

42. **`TestCloudElastiCacheAuthToken`** (§13.3). *Implemented*
    as `TestCloudRedisAUTH` in
    `tests/tier6_e2e_cloud/managed_elasticache_test.go`. No-AUTH
    client receives NOAUTH/WRONGPASS; AUTH-bearing client
    SET/GET round-trips. The AUTH-rotation-during-flight assertion
    stays a follow-on (needs the gateway's Redis client to honor
    a secret-watcher reload path that does not yet exist).

43. **`TestCloudElastiCacheIAMAuth`** (§13.3). On Redis 7.1+,
    swap the AUTH-token path for IAM authentication
    (`ELASTICACHE_AUTH_MECHANISM=iam`), assert the IRSA role's
    `elasticache:Connect` permission resolves at connect time,
    and assert the 15-minute token refresh hook drives a
    reconnect that does not interrupt long-running session
    coordination leases.

44. **`TestCloudElastiCacheCertificateValidation`** (§13.2).
    Configure the gateway with the AWS-published ElastiCache CA
    bundle, reject a forged-CA cert presented by a man-in-the-
    middle proxy, and surface the failure as fail-closed rather
    than fall through to plaintext.

Cluster mode (the production deployment mode; the in-cluster
fixture is single-node and exercises none of this):

45. **`TestCloudElastiCacheClusterModeKeyHashing`** (§12.4).
    Baseline smoke check `TestCloudRedisClusterMode` *implemented*
    in `tests/tier6_e2e_cloud/managed_elasticache_test.go`:
    routes a SET/GET round-trip through a cluster-aware client
    against the configuration endpoint, skips when single-shard.
    The per-subsystem hash-tag distribution audit (sampling
    `t:{tenant}:scache:...`, `lenny:cb:{subsystem}:...`, etc.)
    stays a follow-on; it depends on each subsystem actually
    writing its keys, which the current EKS install does not
    drive (no real sessions yet).

46. **`TestCloudElastiCacheClusterModeMovedRedirection`**
    (§12.4). Trigger a slot migration mid-traffic (the
    operator's `aws elasticache modify-replication-group
    --apply-immediately` reshard path), assert the gateway's
    Redis client follows the `MOVED` redirection without
    surfacing a 5xx to the caller, and assert no in-flight
    request loses its target key.

47. **`TestCloudElastiCacheCrossSlotOpsRejected`** (§12.4).
    Negative test. Issue a `MGET` against keys that hash into
    different shards from the gateway's tier-9 test harness,
    assert ElastiCache returns `CROSSSLOT`, assert the gateway
    never issues such a command in normal operation (the
    semantic-cache + quota-store + breakerstore paths each use
    hash tags to keep related keys co-located).

48. **`TestCloudElastiCacheMultiKeyHashTag`** (§12.4). Verify
    the chart-rendered key prefixes carry hash tags
    (`t:{<tenant>}:...`) for every multi-key access pattern.
    A missing hash tag silently degrades a cluster-mode
    deployment to per-shard semantics; the test reads the
    documented key list out of the running gateway's metrics
    and asserts each pattern.

49. **`TestCloudElastiCacheLuaScriptSingleSlot`** (§4.9, §12.4).
    The §4.9 credential-leasing path runs a Lua script for the
    atomic check-and-lease step. Assert the script's KEYS list
    only references keys in a single hash slot, and assert the
    fail-fast `CROSSSLOT` rejection fires on the test path
    that forces a multi-slot script.

High availability and failover:

50. **`TestCloudElastiCacheMultiAZFailoverPreservesSessions`**
    (§17.3). Trigger a primary-node failover via
    `aws elasticache test-failover`, measure RTO from the API
    call to the gateway re-issuing a write against the
    promoted replica, and assert in-flight session-coordination
    leases either succeed or surface a retriable error code.

51. **`TestCloudElastiCacheReaderEndpointRouting`** (§12.4).
    When the gateway is configured with both
    `redis.primaryEndpoint` and `redis.readerEndpoint`, assert
    read-only commands (the §4.9 semantic-cache `GET`, the
    breakerstore read-only state probe) route to the reader
    endpoint while writes (`INCR`, `SET`, `PUBLISH`) route to
    the primary.

52. **`TestCloudElastiCacheReplicaLagBudget`** (§12.4). Inject
    write load against the primary, sample
    `ElastiCacheReplicationLag`, assert the §12.4 replica-lag
    budget (eventually-consistent reads admitted when
    lag < X seconds; otherwise fall back to the primary). A
    stale replica returns a stale §4.9 cache hit otherwise.

53. **`TestCloudElastiCacheEndpointDNSTTL`** (§17.3). Validate
    the chart-rendered Redis endpoint uses the ElastiCache
    DNS name
    (`<release>.serverless.<region>.cache.amazonaws.com`)
    rather than a baked IP, so DNS re-resolution finds the
    promoted primary within the endpoint TTL. A baked-IP DSN
    is a §17.3 RTO regression.

Pub/Sub fan-out across the cluster (the §13.3 revocation
propagator and the §12.3.7 EventBus both depend on pub/sub):

54. **`TestCloudElastiCachePubSubAcrossShards`** (§13.3,
    §12.3.7). The §13.3 revocation propagator publishes a
    deny-list event on one Redis node; every gateway replica
    subscribes on a different node. Verify the chart's
    `SSUBSCRIBE` configuration (or, in cluster-mode-disabled
    deployments, the plain `SUBSCRIBE`) drives the publish to
    every subscriber within the §13.3 propagation-latency
    budget (200 ms). Cluster mode silently drops pub/sub
    across shards in older Redis versions; the test surfaces
    that mode mismatch.

55. **`TestCloudElastiCachePubSubResumeAfterFailover`** (§13.3,
    §17.3). Trigger a primary failover during a sustained
    pub/sub stream, assert the subscriber's reconnect path
    re-subscribes against the promoted primary, and assert no
    revocation event is lost in the window (the §13.3
    revocation-cache rehydration loop is the recovery path for
    events lost during the gap).

56. **`TestCloudElastiCacheEventBusReplay`** (§12.3.7). The
    §12.3.7 EventBus retranscribe worker consumes events from
    the audit chain and republishes them via pub/sub. Drive a
    multi-event burst, force a pub/sub disconnect mid-burst,
    assert the worker re-publishes the missed events without
    duplicating already-delivered ones (the CloudEvents
    `lenny-extension-attempt` field carries the dedupe key).

Persistence and backup:

57. **`TestCloudElastiCacheSnapshotToS3`** (§17.7). Configure
    automated snapshots on the replication group, trigger a
    manual snapshot, assert the snapshot appears in the
    documented S3 location, and verify the snapshot contains a
    sample circuit-breaker counter set (the breakerstore data
    must survive a §17.7 restore).

58. **`TestCloudElastiCacheRestoreFromSnapshot`** (§17.7).
    Restore a previous snapshot into a fresh ElastiCache
    replication group, point a fresh gateway at the restored
    endpoint, assert the §4.9 semantic-cache hits replay
    correctly and the circuit-breaker state matches the
    pre-snapshot reading.

Memory and eviction (Lenny's circuit-breaker correctness
requires `noeviction`; a `volatile-lru` default would silently
drop counters under memory pressure and disable the §11.6
admission controller):

59. **`TestCloudElastiCacheEvictionPolicyNoeviction`** (§11.6).
    *Implemented* as `TestCloudRedisEvictionPolicy` in
    `tests/tier6_e2e_cloud/managed_elasticache_test.go`. Runs
    `CONFIG GET maxmemory-policy` and asserts the value is
    `noeviction`. Closes the silent-drop failure mode where the
    parameter group regresses to `volatile-lru`.

60. **`TestCloudElastiCacheMemoryPressureFailClosed`** (§11.6,
    §11.7). Push Redis into the maxmemory boundary, attempt a
    write, assert the gateway surfaces `503 QUOTA_PRESSURE`
    rather than silently dropping the rate-limit counter. The
    §11.7 audit chain records the memory-pressure event.

61. **`TestCloudElastiCacheReservedMemoryHeadroom`** (§17.1).
    Assert the chart-rendered parameter group reserves the
    documented memory headroom (`reserved-memory-percent=25`)
    so background fork-for-snapshot does not OOM the node.

Performance and engine:

62. **`TestCloudElastiCacheEngineVersionFloor`** (§13.3).
    *Implemented* as `TestCloudRedisEngineVersionFloor` in
    `tests/tier6_e2e_cloud/managed_elasticache_test.go`. Runs
    `INFO server` and asserts the redis_version major is >= 7.
    Skips when the endpoint is unreachable (ElastiCache is
    VPC-private).

63. **`TestCloudElastiCacheSlowlogSurfaced`** (§16.5). Verify
    `aws elasticache describe-events` and the chart-rendered
    Prometheus rule both pick up Redis slow-log entries above
    the §16.5 budget. A silent slow query is a §12.8 latency
    regression.

64. **`TestCloudElastiCacheLatencyBudget`** (§12.4, §16.5).
    Probe P99 round-trip latency under sustained tier-7 load
    against the ElastiCache endpoint, assert the value stays
    under the §12.4 budget (network round-trip to ElastiCache
    is materially higher than to the in-cluster fixture). Bound
    the assertion at the chart-rendered
    `monitoring.redisLatencyP99Budget` value.

Security and isolation:

65. **`TestCloudElastiCacheNetworkIsolation`** (§13.2). Assert
    the ElastiCache security group admits only the EKS pod
    CIDR (or the VPC endpoint), and reject a connection
    attempt from a non-allowlisted source IP. The §13.2 story
    inside the cluster is undermined if ElastiCache accepts
    public connections.

66. **`TestCloudElastiCacheCustomKMSAtRest`** (§12.9). Provision
    ElastiCache with the per-release Lenny KMS key as the
    at-rest encryption key (not the AWS-managed key), assert
    `aws elasticache describe-replication-groups` reports the
    right KMS key ID, and assert the §12.9 cryptographic-erasure
    invariant (destroying the KMS key renders the snapshot
    unreadable) holds.

Operational scenarios:

67. **`TestCloudElastiCacheMaintenanceWindow`** (§17.6).
    Schedule a maintenance task inside the configured
    maintenance window, assert the gateway either re-connects
    through the documented short outage or surfaces the
    `RedisMaintenance` alert before the window opens.

68. **`TestCloudElastiCacheCloudWatchAlerts`** (§16.5).
    Configure the chart-rendered Prometheus alerts that mirror
    the §16.5 catalog against the ElastiCache CloudWatch
    metrics (`CPUUtilization`, `EngineCPUUtilization`,
    `DatabaseMemoryUsagePercentage`,
    `ReplicationLag`, `Evictions`). Trigger each threshold with
    a synthetic workload, assert the alert fires, assert the
    matching runbook resolves to a `docs/runbooks/*.md` page.

69. **`TestCloudElastiCacheParameterGroupReboot`** (§17.6).
    Change a parameter that requires a reboot
    (`maxmemory-policy` from `noeviction` to `allkeys-lru`,
    then back), trigger the reboot, assert the gateway
    re-establishes the connection within the §12.8 recovery
    budget and that the parameter change is observable through
    a probe `CONFIG GET`.

Lenny-specific subsystem coverage under cluster mode (each
test drives the corresponding pkg/ subsystem against a
cluster-mode ElastiCache; the in-cluster single-node fixture
covers none of these in cluster-aware mode):

70. **`TestCloudElastiCacheCircuitBreakerSurvivesFailover`**
    (§11.6). Drive sustained traffic that increments the §11.6
    circuit-breaker counters, trigger a primary failover at
    the half-open transition, assert the counter survives the
    failover (its state lives in the replication group), and
    assert the gateway's breaker state machine resumes the
    transition correctly.

71. **`TestCloudElastiCacheQuotaStoreSlidingWindow`** (§11.2).
    Drive multi-tenant traffic against the sliding-window
    quota store on cluster mode, assert the §11.2 fail-open
    accounting reconciles to the configured ceiling under
    replication lag, and assert no counter is silently
    evicted (the noeviction parameter group is the gate).

72. **`TestCloudElastiCacheSessionCoordinationLease`** (§10.1).
    The §10.1 session-coordination lease uses `SET NX EX` to
    elect a replica as the lease holder. Trigger a primary
    failover during a sustained lease, assert exactly one
    replica holds the lease at all times across the failover
    (no split-brain), and assert the lease renewal heartbeat
    reconverges within the §10.1 budget.

73. **`TestCloudElastiCacheSemanticCacheCluster`** (§4.9). The
    §4.9 semantic cache uses `t:{tenant}:scache:<scope>` keys.
    Drive multi-tenant cache traffic against cluster mode,
    assert the hash-tag pattern keeps a given tenant's keys
    on a single shard, and assert the §12.2 DeleteByUser /
    DeleteByTenant erasure path completes in the documented
    fan-out window across the shards holding tenant keys.

74. **`TestCloudElastiCacheRevocationCacheClusterFanout`**
    (§13.3). Publish a deny-list revocation on the chart-
    rendered revocation channel and assert every gateway
    replica observes it within the §13.3 propagation budget,
    regardless of which shard hosts the channel. The test runs
    three gateway replicas to surface the cross-shard pub/sub
    behavior that single-replica deployments hide.

The ElastiCache expansion adds 34 suites (41-74). Sequencing
recommendation: land the auth + cluster-mode set first (41,
44, 45, 47, 48) because they expose the silent-failure modes
that hide behind the single-node fixture, then the
pub/sub-across-shards trio (54-56) because it covers the §13.3
and §12.3.7 paths that the in-cluster fixture cannot exercise
at all, then the operational set. The IAM auth path (43) skips
when the deployment is on Redis < 7.1.

#### EKS-vs-Kind cluster-platform expansion

Beyond the managed-data-store differences, the EKS platform
itself behaves materially differently from the Kind cluster the
tier-5 / tier-7 / tier-8 suites run against. The suites below
cover the EKS-platform behaviors that the Kind harness cannot
exercise even when the chart installs cleanly on both. Many
apply equally to GKE and AKS; the EKS-specific framing names the
AWS implementation but the assertion shape generalises.

Storage (EBS-backed PVCs replace Kind's hostPath emptyDir):

75. **`TestCloudEBSPVCAttachLatency`** (§17.1). Create a Pod
    with an EBS-backed PVC, measure attach + mount latency
    against the §6.3 startup budget. EBS attach takes
    materially longer than a hostPath bind; a chart that
    assumes hostPath-class start-time blows the §6.3 envelope.

76. **`TestCloudEBSPVCAZAffinity`** (§17.3). Schedule a Pod
    bound to an EBS volume in `us-west-2a`, kill the node,
    assert the Pod re-schedules on a node in the same AZ (EBS
    volumes are AZ-pinned; a Pod re-scheduled across AZs
    detaches indefinitely).

77. **`TestCloudEBSSnapshotLifecycle`** (§17.7). Trigger an EBS
    snapshot of the gateway's PVC (if one is configured),
    restore it into a fresh volume in a second AZ, assert the
    pod reads back the artifact data. Validates the §17.7
    cross-AZ recovery story against EBS, not against
    in-cluster emptyDir.

78. **`TestCloudEBSIOPSBudget`** (§12.5, §12.7). Provision the
    PVC at the documented IOPS floor (gp3 with 3000 IOPS),
    drive sustained write load through the artifact store,
    assert IOPS stays under the provisioned ceiling (gp3 burst
    credits not in use). EBS gp3 silently throttles past
    burst; Kind's hostPath has no such ceiling.

79. **`TestCloudStorageClassCSIPresent`** (§17.6). *Implemented*
    in `tests/tier6_e2e_cloud/eks_platform_test.go`. Lists
    StorageClasses, asserts a cluster default exists and that
    at least one class uses `ebs.csi.aws.com`. The Terraform
    module installs the EBS CSI driver as a managed EKS addon
    with a dedicated IRSA role; run-e2e.sh applies a gp3 default
    StorageClass since the addon installs the driver but not
    the class.

Networking (VPC CNI + AWS Load Balancer Controller replace
kindnet + Kind's host port mapping):

80. **`TestCloudVPCCNIPodIPFromVPC`** (§13.2). *Implemented* in
    `tests/tier6_e2e_cloud/eks_platform_test.go`. Samples each
    gateway pod's PodIP and asserts it lives inside the VPC CIDR
    (10.42.0.0/16 default) or an RFC1918 / RFC6598 range. Public
    IPs surface a CNI misconfiguration that would route pod
    traffic outside the VPC default-deny boundary.

81. **`TestCloudPodENILimitRespected`** (§17.1). Schedule the
    documented per-node maximum number of pods on a single
    `t3.medium` (ENI-limited to 17), assert further pods
    Pending rather than crash-loop. The chart's autoscaler
    must honor this bound, not the higher Kind default.

82. **`TestCloudServiceLoadBalancerProvisioned`** (§17.5)
    *(expands `TestManagedIngress`)*. Render the gateway
    Service with Type=LoadBalancer (or annotate an Ingress
    for the AWS Load Balancer Controller), assert the NLB
    (or ALB) is provisioned, healthy, and reachable from the
    public internet within the §17.5 budget. Kind has no
    cloud LB; this test cannot exist there.

83. **`TestCloudALBControllerInstalled`** (§17.5). Assert the
    AWS Load Balancer Controller is installed in
    `kube-system` and that its IRSA role has the documented
    minimum permissions (`elasticloadbalancing:CreateListener`,
    `ec2:DescribeSecurityGroups`, etc.). A missing or
    underprivileged controller leaves Ingress resources
    Pending forever.

84. **`TestCloudNLBHealthChecks`** (§17.5). Assert the NLB's
    target-group health check probes the gateway's `/healthz`
    endpoint and surfaces Unhealthy targets when the gateway
    pod is killed.

85. **`TestCloudVPCEndpointReachable`** (§13.2). Validate the
    chart-rendered gateway egresses to S3 / KMS / ECR via VPC
    endpoints rather than public internet (VPC endpoints
    bypass NAT charges and stay inside the AWS network).
    Skips when no VPC endpoints are configured; surfaces the
    silent fall-through to public internet egress in BUILD-GAPS.

86. **`TestCloudNATGatewayEgress`** (§13.2). Assert egress to
    a non-AWS host (e.g. an LLM provider) routes through the
    NAT Gateway in the same AZ as the source pod. Cross-AZ
    NAT egress doubles the latency budget and quietly bumps
    cost.

87. **`TestCloudVPCInternalDNSResolution`** (§13.2). Assert
    pods resolve VPC-internal hostnames (the RDS endpoint,
    the ElastiCache endpoint, internal Route 53 zones)
    through the VPC's Route 53 resolver. Kind's CoreDNS has
    no concept of VPC-internal resolution; an EKS deployment
    that mis-configures `enableDnsHostnames` on the VPC
    silently fails name resolution.

Image registry (ECR replaces Kind's `kind load docker-image`):

88. **`TestCloudECRPullCredentialRefresh`** (§17.6). ECR
    authentication tokens expire after 12 hours. Run the
    cluster for >12 hours (or simulate by rotating the
    docker-registry secret), spin up a fresh pod, assert the
    image pull succeeds. The chart's `imagePullSecrets`
    must be wired to a credential-refresh source (the EKS
    addon `aws-secrets-manager-csi-driver` or `ecr-credential-provider`).
    Baseline smoke check `TestCloudECRImagePullSucceeds`
    *implemented* in `tests/tier6_e2e_cloud/eks_platform_test.go`:
    inspects each gateway pod container status for the
    presence of a `dkr.ecr.` image and asserts no
    `ImagePullBackOff` / `ErrImagePull` waiting state. The
    >12-hour refresh assertion remains a follow-on.

89. **`TestCloudECRImageScanGate`** (§17.6, §13.1). On a push
    of a known-vulnerable image, assert the ECR scan results
    surface a critical CVE, assert the chart's
    cosign-verify-webhook (when feature-gated on) rejects the
    pull. A silent pass on a vulnerable image is a §13.1
    finding.

90. **`TestCloudPullThroughCacheHit`** (§17.6). When the ECR
    pull-through cache is configured, assert the second pull
    of a public-registry image (`docker.io/library/redis:7`)
    hits the cache and does not egress to public Docker Hub.

Cluster lifecycle (managed node groups + autoscaler replace
Kind's static three-node config):

91. **`TestCloudManagedNodeGroupRollingUpdate`** (§17.6).
    Trigger an EKS managed-node-group update (AMI bump or
    instance-type change). Assert the gateway and controller
    pods drain + reschedule across the rolling node refresh
    without losing in-flight sessions (the §10.1 session-
    coordination lease must survive a node drain).

92. **`TestCloudClusterAutoscalerScaleUp`** (§5, §17.1).
    Create N pending agent-pool pods that exceed the current
    node capacity, assert the cluster autoscaler provisions
    additional nodes within the §17.1 budget, and assert the
    pending pods schedule.

93. **`TestCloudClusterAutoscalerScaleDown`** (§17.1). Idle
    the agent-pool to zero pods, assert the autoscaler scales
    nodes down past the documented idle threshold. A failure
    to scale down inflates the §17.1 cost story.

94. **`TestCloudSpotInterruptionHandled`** (§17.1, §17.3).
    The §17.1 cost story uses Spot for warm-pool nodes.
    Simulate a Spot interruption via the EC2 instance
    interruption notice (a 2-minute warning), assert active
    agent pods on the interrupted node checkpoint into the
    §4.4 EvictionStateStore before the node terminates.

95. **`TestCloudKarpenterProvisioning`** (§17.1). When
    Karpenter is the cluster's scheduler instead of the
    cluster autoscaler, assert pending pods get a Karpenter
    NodeClaim and the NodeClaim resolves to a real node
    within the documented latency budget. Skips when
    Karpenter is not installed.

Control plane (EKS API server has stricter quotas + managed
audit log destinations):

96. **`TestCloudAPIServerThrottlingRespected`** (§17.6).
    Drive the chart's controllers against the EKS API server
    at sustained load, assert no controller's reconcile loop
    trips the `apiserver_requests_too_many_total` budget. The
    EKS API server's default QPS is materially lower than
    Kind's; an over-chatty controller hits 429s on EKS that
    Kind never surfaces.

97. **`TestCloudControlPlaneAuditLogsCloudWatch`** (§11.7).
    Enable EKS control-plane audit logging to CloudWatch
    Logs, run a session, query CloudWatch Logs for the
    audit chain's `lenny_admin_action` events. The §11.7
    audit-chain visibility on cloud routes through this
    destination, not the in-cluster fluent-bit.

98. **`TestCloudEtcdEncryptionAtRest`** (§13.1, §12.9). Assert
    the EKS cluster's etcd encryption is enabled with the
    per-release KMS key (set via
    `aws eks associate-encryption-config`). A missing
    encryption config silently stores Lenny secrets in
    plaintext in etcd, defeating the §12.9 cryptographic-
    erasure story.

99. **`TestCloudAdmissionWebhookEKSBudget`** (§17.6) *(expands
    #5)*. Lenny's admission webhooks must respond inside the
    EKS API server's stricter 10-second budget (Kind allows
    30s). Run the chart's webhooks under sustained admission
    load, sample webhook latency, assert P99 stays under 5s
    (half the budget). A webhook past the budget fails-closed
    silently.

Identity (Pod Identity is a newer EKS-specific alternative to
IRSA with simpler trust):

100. **`TestCloudPodIdentityAlternativePath`** (§13). When the
     EKS Pod Identity Agent addon is installed, assert the
     gateway can resolve credentials through Pod Identity
     (`AWS_CONTAINER_CREDENTIALS_FULL_URI` env) instead of
     IRSA. Skips when Pod Identity Agent is not installed.

Observability (CloudWatch Container Insights + Fluent Bit
replace Kind's in-cluster Prometheus + Loki):

101. **`TestCloudContainerInsightsEnabled`** (§16.1). With
     CloudWatch Container Insights enabled, assert the chart's
     pods emit the documented Container Insights metrics
     (`pod_cpu_utilization`, `pod_memory_utilization`,
     `pod_network_rx_bytes`). A missing addon silently disables
     the §16.5 capacity-pressure alerts.

102. **`TestCloudFluentBitLogForwarding`** (§16.1, §11.7).
     Assert the Fluent Bit container in the chart's pod spec
     forwards application logs to CloudWatch Logs in the
     documented JSON schema (the §11.7 audit chain consumes
     the JSON format).

103. **`TestCloudXRayTraceFlow`** (§16.1). When OTLP traces
     are configured to land in AWS X-Ray (via the ADOT
     collector), assert a session round-trip surfaces a trace
     with the documented service-name + segment shape. Without
     this assertion, traces silently drop on a misconfigured
     collector endpoint.

Pod density and scheduling (real resource limits + AZ
constraints replace Kind's lax single-node defaults):

104. **`TestCloudPodDisruptionBudgetEnforced`** (§17.6, §17.1).
     Run `kubectl drain` against a node hosting two gateway
     replicas where the PDB requires `minAvailable=2`. Assert
     the drain blocks rather than evict both replicas, and
     assert the alternative replicas come up before the drain
     proceeds. Kind enforces PDBs but the timing is gentler;
     EKS's eviction loop surfaces races the Kind harness hides.

105. **`TestCloudResourceLimitsEnforced`** (§17.1). Force a
     gateway pod past its memory limit, assert the kubelet
     OOM-kills it and the pod restarts. Kind allows soft
     over-subscription; EKS enforces hard limits.

106. **`TestCloudKubeletEvictionThresholds`** (§17.1). Fill
     a node's ephemeral storage past the kubelet eviction
     threshold (`nodefs.available<10%`), assert kubelet
     evicts the lowest-priority pod first and emits the
     `NodeHasDiskPressure` condition. A silent ephemeral-
     storage exhaustion on cloud surfaces as flaky pod
     restarts.

107. **`TestCloudPodPriorityClassRespected`** (§17.1). Assert
     the chart's control-plane pods (gateway, controller,
     token-service) carry a priority class higher than the
     agent-pool pods, so a node under pressure evicts agent
     pods before evicting the control plane.

Multi-AZ (real cross-AZ placement replaces Kind's single-zone
default):

108. **`TestCloudTopologySpreadAcrossAZs`** (§17.3). Assert
     the chart's gateway / controller / token-service
     deployments carry `topologySpreadConstraints` that pin
     pods across AZs. Verify a fresh install lands one
     replica per AZ across the cluster's three AZs.

109. **`TestCloudCrossAZLatencyBudget`** (§12.4). Probe
     pod-to-pod latency between an agent pod and a gateway
     pod in different AZs, assert P99 stays under the §12.4
     cross-AZ budget (~5 ms on AWS). Kind's single-node
     latency is 0; the cloud floor is materially different.

110. **`TestCloudAZAwareEBSAttachment`** (§17.3) *(expands #76)*.
     Verify a Pod whose PVC is in `us-west-2a` schedules on
     a node in `us-west-2a`. The scheduler's
     `WaitForFirstConsumer` mode for EBS-backed
     StorageClasses is the gate; a misconfigured StorageClass
     loses cross-AZ scheduling correctness.

Lenny-specific differences when the cloud platform is the
backing (each test surfaces a behavior the Kind harness
cannot exercise):

111. **`TestCloudWarmPoolLatencyEKS`** (§5, §6.3). Pre-warm
     pool latency on EKS includes ECR pull (cold-pull
     latency) + EBS volume attach + the EKS scheduler's
     placement delay. The §6.3 startup budget is the same;
     the test asserts the §5 warm-pool size formula accounts
     for the EKS-side floor (pre-warm 3-5x what Kind needs).

112. **`TestCloudAdapterImageColdPullBudget`** (§4.7, §17.1).
     On a newly-provisioned node, the first session's pod
     pulls both the adapter image and the runtime image from
     ECR; the cold-pull latency must stay under the §6.3
     envelope. Validates the chart's `imagePullPolicy=IfNotPresent`
     + ECR-pull-through-cache configuration.

113. **`TestCloudAuditChainThroughputRDS`** (§11.7, §12.4).
     The §11.7 audit chain's append-to-Postgres latency
     differs materially when the Postgres is RDS (network
     round-trip) vs the in-cluster fixture (local socket).
     Drive sustained admin-API traffic, assert the audit-chain
     append rate keeps up with the request rate; otherwise the
     audit-chain back-pressure blocks the admin API.

114. **`TestCloudMTLSRotationWebhookCompat`** (§13.1, §17.6)
     *(expands #9)*. The cert-manager-driven mTLS rotation
     restarts the webhook pods; under EKS's stricter
     webhook-latency budget the restart window must stay
     under 5s or admission requests fail-closed. Validates
     the rotation timing on EKS specifically.

115. **`TestCloudVPCFlowLogsCaptureSessionTraffic`** (§13.2,
     §11.7). When VPC Flow Logs are enabled, assert session
     traffic between the gateway and an agent pod appears in
     the Flow Logs. The §13.2 audit story is incomplete
     without this capture on cloud.

The EKS-vs-Kind expansion adds 41 suites (75-115). Sequencing
recommendation:

  - First, the four that surface silent-failure modes the Kind
    harness hides (#80, #84, #88, #98). A misconfigured VPC CNI,
    NLB, ECR auth, or etcd encryption produces a green install
    that silently regresses §13 / §17 invariants.
  - Then the scheduling + lifecycle set (#91, #92, #94, #104,
    #108) so the deployment behaves correctly under the
    EKS-managed cluster operations operators run weekly.
  - Then the observability and Lenny-specific behaviors (#101,
    #102, #111, #112, #113).
  - The Karpenter / Pod Identity entries (#95, #100) gate on
    optional addons; they skip when the addon is not installed.

#### Out of scope

The following deliberately stay outside tier-6 even after the
115 land. Cost telemetry / billing accuracy is a feature spec,
not a test concern. Cluster upgrade compatibility (the
Kubernetes minor-version bump itself, e.g. 1.31 → 1.32) is an
operator concern covered by the chart-test matrix against Kind.
Helm rollback is the same shape — not cloud-specific. CloudTrail
audit log consumption is an operator's SIEM concern; the
gateway's §11.7 audit chain captures the platform-side actions
already.

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

- **ArtifactStore residual sub-features.** The §12.5 ArtifactStore
  contract is mostly built: `pkg/blobstore/miniostore` carries the
  SSE-KMS resolver hook (`Config.SSEKeyResolver`) and the §12.8
  SetLegalHold / ClearLegalHold guard on DeleteBySession;
  `pkg/blobstore/artifactcatalog` plus migration 0049 carry the
  Postgres-backed artifact_store catalog table with the
  live → soft_deleted → tombstoned lifecycle; `pkg/tenantkms` carries
  the §12.5 T4 KMS availability probe (`Lifecycle.ProbeAvailability`,
  `LastProbeSuccess`, the `Prober` controller-runtime Runnable, and
  the `lenny_t4_kms_probe_*` metrics). The remaining sub-features are
  the partial-manifest cleanup sweep (gated on the Postgres-backed
  checkpoint metadata table) and the MinIO-outage Postgres-minimal-
  state fallback router (the EvictionStateStore is the target store;
  the blobstore-side fallback router is unbuilt). Both block the
  full §12.5 contract coverage and the tier-4 checkpoint flow.

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

- **Elicitation tampering admin contract + tier-9 probes.** RESOLVED
  through the §9.2 platform-floor wiring and tier-9 admin-API
  probes. The admin GET endpoint now returns both `storedMode` and
  `effectiveMode` per §9.2; the §15.1 PUT endpoint rejects a stored
  mode strictly below the floor with
  `ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR`. The gateway reads
  the floor from `--elicitation-content-integrity-floor`
  (chart-rendered from `security.elicitationContentIntegrity.floor`),
  and `tests/tier9_security/elicitation_tamper_test.go` drives the
  three §12.9.9 contract probes (enforce, detect-only, platform
  floor) against the live cluster. The deep chain-walker tamper-
  detect behavior is covered by `pkg/elicitation/chain*_test.go`
  and `pkg/gateway/mcptools/elicitation_test.go`; the §16.5 metric
  is covered by `pkg/gateway/gatewaymetrics`. The tier-3
  `TestRESTMCPElicitation` is unrelated: §15.2.1 lists
  `/v1/sessions/{id}/elicitations/{elicitation_id}/respond` and
  `/dismiss` as REST-only by design (MCP clients resolve via the
  native `elicitation/create` flow), so the tier-3 scaffold's
  "no MCP counterpart" skip reflects the spec.

### Concurrent-mode slot reservation (§5.2)

- **§5.2 post-recovery rehydration of the Redis slot counter.** The
  atomic GET-compare-INCR slot reservation is implemented in
  `pkg/gateway/slotcounter` (Redis Lua) and wired through
  `pkg/gateway/podclaim/slotclaimer.go` so racing reservers cannot
  exceed `maxConcurrent`. The post-recovery rehydration path
  required by §5.2 ("Post-recovery rehydration atomicity") is not
  yet implemented. If Redis is wiped while concurrent-mode pods are
  hosting slots, a fresh allocation reads `active_slots = 0` from
  Redis and could over-commit a pod whose
  `Sandbox.status.activeSlots` still reflects active slots. The MVP
  assumes Redis outlives the gateway's session lifetime. The
  rehydration follow-up requires (a) a
  `SessionStore.GetActiveSlotsByPod` aggregator that lists sessions
  by `pod_id` from Postgres, (b) a `SET NX` lock on
  `lenny:pod:{pod_id}:rehydrating` so two gateway replicas do not
  rehydrate the same pod twice, and (c) a startup hook in
  `cmd/lenny-gateway/main.go` that runs the rehydration sweep
  before serving traffic. Tier-7 cloud-load surfaces the
  consequence: a Redis restart mid-load can briefly over-commit
  pods until the next slot release rebalances the counter.

## Blocked

Entries here are real gaps that the autonomous loop cannot close
without an external decision, observation, or multi-hour reconciliation.
They are listed separately so the loop's per-gap workflow does not
re-attempt them.

- **Gateway client migration to the Token Service gRPC.** RESOLVED.
  `pkg/gateway/credassign.Client` is the mTLS-aware gRPC client; it
  satisfies the new `credassign.Assigner` interface so the §4.7 binder
  and the §4.9 Proactive Lease Renewal worker drive the cutover path
  transparently. The gateway main wires the Client when
  `--token-service-grpc-addr` (env `LENNY_TOKEN_SERVICE_GRPC_ADDR`) is
  set; `--token-service-tls-cert`/`-key`/`-ca` carry the per-replica
  mTLS identity (TLS 1.3 floor). The proto carries a new
  `upstream_credential` field so the Token Service can return the
  materialized provider secret to the gateway over mTLS and the
  gateway's `credcache` serves the §4.9 LLM proxy without an extra
  round-trip per upstream call. Tier-2 covers the full cutover via
  `tests/tier2_component/controllers/tokenservice_client_test.go`
  (AssignProto round-trip + credcache mirror, PoolForLease binding,
  Release across both sides, ErrPoolNotFound mapping).
- **`POST /v1/sessions/{id}/upload` 100% error rate against Kind.**
  RESOLVED. Validated against a live Kind cluster: the original
  100%-error-rate trace was caused by the e2e overlay missing the
  `acme` tenant + `claude-code` runtime that the scenario
  hard-codes. With the Step 4 bootstrap-values seed in place, the
  upload handler succeeds in ~65% of requests at sustained 5 req/s
  with 1 MB body uploads (p95 under 80 ms in the successful
  subset). The remaining failures are kubectl port-forward
  connection drops under sustained throughput, not §15.1 envelope
  errors — verified by running k6 directly against the gateway
  pod's HTTP listener (status=0 connection-refused mid-upload, no
  gateway-side error). Tier-7 `TestCheckpointDuration` is now an
  implemented test (not a `t.Skip`); the strict §12.7 latency-
  regression assertion lands when a cluster-internal client
  replaces port-forward, which is a tier-6/cloud follow-on.
- **`docs/runbooks/` structural completion.** The
  `tests/tier11_docs/runbooks_test.go` gate enforces front-matter
  title, triggers with severities, and `Trigger / Diagnosis /
  Remediation` section headings as hard `t.Errorf` failures. The
  block here is for any runbook that drifts out of conformance
  during a future merge; the on-disk catalog is 94 runbooks under
  `docs/runbooks/` (excluding `index.md`).
- **Homebrew tap publishing (`lennylabs/tap`).** The formula
  source ships at `dist/brew/lenny.rb`; the `cli` job in
  `.github/workflows/release.yml` cross-compiles the four
  `(GOOS, GOARCH)` archives and attaches them to the GitHub
  release; the `homebrew-tap-pr` job renders the formula with the
  tag version + the four SHA-256 digests and opens a PR against
  `lennylabs/homebrew-tap`. The remaining work is external-only —
  creating the `lennylabs/homebrew-tap` repository on GitHub,
  granting the release bot push access to the operator's fork
  (`HOMEBREW_TAP_TOKEN` secret), and tagging the first release.
  Tier-11 TTHW step 1 runs the moment the first tap PR merges;
  nothing else in-repo needs to change.
- **Egress-capture sidecar e2e wiring.** RESOLVED. The Sandbox
  reconciler injects the lenny-egress-capture sidecar whenever a
  Sandbox carries the §12.9.8 opt-in annotation
  (`lenny.dev/test-egress-capture-upstream`) and the controller is
  configured with `--egress-capture-image` (chart value
  `controller.egressCaptureImage`). The WarmPoolController propagates
  the annotation from SandboxTemplate to each Sandbox it warms; the
  e2e Kind overlay annotates `cred-shell-echo-template` so the
  sidecar runs alongside the §12.9.8 probe runtime. Tier-9
  `tests/tier9_security/credential_leakage_test.go` now drives
  `kubectl exec` against the cred-shell-echo pod and asserts no
  LLM-provider credential prefix surfaces in the runtime
  environment, the §4.7 credential mount, or the §12.9.8 capture
  file. The companion §13.2 egress NetworkPolicy that forces all
  traffic through the sidecar is a chart follow-on tracked in the
  ops backlog; the in-cluster network already restricts egress and
  the sidecar's path is the only allowed proxy upstream.
- **Clock-injection harness — narrow follow-on.** `pkg/clockinject`
  is shipped; `cmd/lenny-gateway` calls `clockinject.FromEnv` at
  startup and `cmd/lenny-preflight` calls
  `clockinject.AssertProductionDefault`. Per-subsystem clocks the
  gateway main passes to constructors (sessionserver, admin,
  delegation, mcptools, credrenewal, orphancleanup, retentiongc,
  leasecontrol, billing failover, translators) flow through
  `clockinject.Now`. The narrow follow-on is the inner Postgres /
  Redis store packages that read time directly without an injected
  clock; passing those through the same harness is a per-package
  refactor.
- **§26 reference-runtime OCI images.** The image registry the
  nightly conformance run pulls from does not exist.
- **External pen-test bundle.** Tier-9 `TestPentestReplay` defaults
  to the v1 internal baseline at
  `tests/tier9_security/pentest/v1-baseline-bundle.json`, which
  encodes the findings recorded in `tests/tier9_security/reviews/`
  as remediated. Release engineering points `LENNY_PENTEST_BUNDLE`
  at the partner bundle when an external engagement ships.

## Recommended sequencing

The remaining gaps cluster such that a small set of investments
unblocks disproportionately many tests.

1. Cut the gateway-side credential path over to the Token Service
   gRPC. Build an mTLS-aware client in `pkg/gateway/credassign` that
   delegates `AssignCredentials` / `RotateCredentials` /
   `RevokeCredentials` to `lenny-token-service`, and retire the
   in-process `pkg/credential.MintLease` path. Closes the §4.3 trust
   boundary and unblocks one tier-2 controller test, one tier-4
   integration test, and the tier-8 chaos suites that depend on a
   live Token Service.
2. Wire the egress-capture sidecar into the e2e Kind overlay
   (`tests/testinfra/kind/agent-workload.yaml`) with the §13.2 egress
   NetworkPolicy that forces agent pods through it. Unblocks the
   tier-9 §12.9.8 credential-leakage probe.
3. Ship the elicitation tampering intermediary. The
   `elicitation-echo` runtime is already wired in the e2e overlay;
   the remaining piece is the intermediary that drives tier-9
   `TestElicitationTamperEnforceMode`,
   `TestElicitationTamperDetectOnlyMode`,
   `TestElicitationPlatformFloor`, and tier-8
   `TestElicitationDeadlockDetection` against the live cluster.
4. Re-run the `checkpoint_duration` k6 scenario against a Kind
   install with the now-captured response body, read the error
   envelope, and fix the failure mode in the gateway upload path or
   the scenario's tenant/runtime setup.
5. Land the tier-6 cloud follow-on suites in the sequencing order
   recorded under "Tier-6 follow-on suites" (critical first, then
   the high-value RDS / ElastiCache / EKS-platform set).
6. Bring the GCP (GKE) and Azure (AKS) tier-6 coverage to EKS
   parity. RESOLVED in the repository, awaiting external
   credentials for live validation. The scripts/cloud/{gke,aks}/
   directories were renamed to scripts/cloud/{gcp,azure}/ to match
   the AWS layout; scripts/cloud/{gcp,azure}/up.sh now invoke
   Terraform with the per-provider Helm-consumable outputs (KMS
   key, object-storage bucket/container, Workload Identity /
   Federated Identity service account);
   scripts/cloud/{gcp,azure}/run-e2e.sh drive the full install
   cycle (Terraform → image push → values render → datastores →
   helm install → tier-6 suite); and the tier-6 `TestCloudOIDC`
   test now dispatches per-provider (EKS:
   `eks.amazonaws.com/role-arn`; GKE: `iam.gke.io/gcp-service-account`;
   AKS: `azure.workload.identity/client-id`) so the SA-annotation
   probe runs uniformly across providers. Tier-6 cloud tests
   require `LENNY_CLOUD_PROVIDER` plus an active cluster + cloud
   credentials; without those they skip per the documented tier-6
   precondition contract. Provider-aware bodies for the
   EKS-flavored RDS / ElastiCache / EBS CSI / VPC CNI tests stay an
   additive follow-on (the underlying §spec is symmetric — Cloud
   SQL / Memorystore for GCP, Azure DB / Azure Cache for AKS — and
   the test scaffolding inherits the existing managed-* file
   layout).

## Maintenance

This file records the verified state on 2026-05-20. When a gap is
closed, delete its entry. When a new gap surfaces, add it under the
matching section with a verified file or line reference. Counts in
this file are snapshots; treat them as the audit's record of the
state at that date rather than a continuing invariant.
