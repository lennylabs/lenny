# Build plan

This document is the execution plan that carries the Lenny implementation to
v1-complete: every §18 phase exit gate satisfied and every TESTING.md tier green through
the `lenny-test` harness. It regroups the remaining work into dependency-ordered waves
and states the rules an autonomous agent follows while executing them.

The authoritative deliverable list is [`spec/18_build-sequence.md`](spec/18_build-sequence.md),
the test architecture is [`TESTING.md`](TESTING.md), and the live build status is
tracked in [`BUILD-PROGRESS.md`](BUILD-PROGRESS.md). This plan is a stable reference; it
changes only when the strategy changes.

## Background

The build did not follow the §18 phase order. Two tracks were built ahead of the
Kubernetes layer: the gateway request-handling surface (session lifecycle, admin API,
stores, translators) and the per-phase pure-logic Go packages that §18 lists as
deliverables (`pkg/checkpoint`, `pkg/elicitation`, `pkg/environment`, `pkg/experiment`,
`pkg/podsecurity`, `pkg/credential`, and others). The Kubernetes control plane (the
CRDs, the WarmPoolController, the PoolScalingController) was built most recently.

The consequence is that most phases are partially complete: the logic substrate for a
phase exists as a tested Go package, while the controller, binary, or gateway
integration that consumes it does not. The waves below regroup the remaining work so
that each phase's deferred deliverables are finished and its §18 exit gate is run.

## How the plan is organized

The plan is organized into dependency-ordered waves, Wave 0 through Wave 7. Each wave
closes a coherent set of §18 phases and ends with a `lenny-test` harness gate that must
pass before the next wave starts. A wave that "closes" a phase finishes that phase's
deferred deliverables and runs its §18 exit gate. The §18 phase sequence remains the
authoritative deliverable list; the waves only regroup the remaining work.

## Completion principles

1. **The harness is the definition of done.** A phase is complete when `lenny-test`
   runs its §18 exit-gate group and reports PASS. Code that is written but not gated
   does not count as complete.
2. **The spec is the source of truth and is read-only.** Do not modify anything under
   `spec/`. When the spec is silent or self-contradictory, log the gap (see "Autonomous
   execution") and do not invent behavior, wire formats, or error codes.
3. **Real backing services, every tier.** Per TESTING.md §1, the component and
   integration tiers run against real Postgres, Redis, MinIO, and envtest, and the test
   dependencies are installed and available. Reference TESTING.md and exercise every
   tier the changed code touches as edits land, rather than only at a wave boundary. No
   new mocks are added for infrastructure the platform owns.
4. **Test coverage is part of every change.** New code ships with new tests that cover
   it. Un-skip as you build: each `t.Skip` removed becomes a passing test in the same
   change, and `tests/spec-map.json` and `tests/change-graph.json` are updated in that
   change so `lenny-test validate-maps` stays green.
5. **A failing test is a code defect until proven otherwise.** Existing tests can carry
   bugs, but the default diagnosis is that the product code is wrong. Change a test
   only after confirming the test itself is wrong, and record that reasoning in the
   commit message.
6. **Security is a spec requirement.** Each deliverable conforms to the security
   controls in its spec sections (§13 and the per-section requirements). Do not weaken
   a control, a NetworkPolicy, or an admission check to make a test pass.
7. **Keep the code modular and organized.** Extract shared logic, maximize reuse, and
   do not duplicate a package that already exists.
8. **Follow §18, but do not halt on a roadblock.** Build in §18 phase order and honor
   the §18.40 prerequisite chain (ADR-007 before Phase 1 sign-off, etcd encryption
   before credential hardening, the phase-stamp ConfigMap before each `features.*` flag
   flips). The goal is 100% of the spec: when one deliverable is blocked, route around
   it and keep progressing rather than stopping the run.
9. **Commit frequently.** One deliverable per commit, with a message that names the
   spec section and the deliverable. Update the phase-status table in
   `BUILD-PROGRESS.md` when a phase's status changes. The git history is the
   per-increment record; there is no separate in-file progress log.
10. **Waves are ordered by dependency rather than calendar time.** Sizing is relative
    complexity (S, M, L, or XL).

## Autonomous execution

This plan runs as a single long-lived autonomous session. The agent does not halt for
input; it progresses toward 100% of the spec and surfaces decisions asynchronously.

**Run as an orchestrator.** The main session owns the plan, the wave sequencing, the
harness gates, the git history, and the `BUILD-PROGRESS.md` state, and never hands those
off. It delegates scoped units of work to sub-agents to keep its own context lean across
a multi-day run.

- **Delegate scoped units.** Use a sub-agent for one self-contained piece of work:
  researching a spec area, or implementing one well-specified deliverable such as a
  single store backend, endpoint, translator, or controller. Brief each sub-agent in
  full; it has none of this session's context. Give it the spec sections, the interface
  it implements, the completion principles above, and the test it must make pass.
- **Parallelize only independent work.** Sub-agents that edit overlapping files
  conflict. Run sub-agents concurrently only for deliverables in separate packages, and
  sequence dependent ones.
- **Never delegate understanding or verification.** Understand each deliverable well
  enough to write a precise brief and to judge the result. When a sub-agent returns,
  the orchestrator runs the deliverable's test tier itself, because a returned summary
  reports intent rather than outcome. Wave exit gates are run by the orchestrator and
  are never taken on a worker's word.

**Operating loop.** Repeat until the Definition of complete is met.

1. Re-read `BUILD-PROGRESS.md` (the current wave, the phase-status table, the blocker
   log) and the current wave in this file. Identify the next unfinished deliverable.
2. Read the spec sections that deliverable implements and the test that gates it.
3. Implement it directly when small, or delegate it to a fully briefed sub-agent. Run
   sub-agents in parallel only for deliverables in separate packages.
4. Run the test tier the deliverable touches. A failing test is a code defect until
   proven otherwise (principle 5). Do not advance until it passes.
5. Commit the deliverable and update the `BUILD-PROGRESS.md` phase-status table.
6. At a wave boundary, run the wave's exit-gate `lenny-test` command; the wave is
   complete only on PASS. Then advance the "Current wave" line in `BUILD-PROGRESS.md`.

**Never halt; route around blockers.** When a deliverable is blocked by a missing
dependency, a spec gap, or a failing upstream component, record it and move to the next
independent deliverable. A blocked item never stops the run.

- **Blocker log.** Maintain the "Blocked / needs human decision" section of
  `BUILD-PROGRESS.md`. Each entry names the deliverable, the blocker, and the interim
  action taken. Wire contracts and security controls are never guessed: when one is
  ambiguous, skip the deliverable and log it.
- **State lives in the repo.** A multi-day run compacts its working context many times.
  The durable state is the git history, the `BUILD-PROGRESS.md` phase-status table, and
  its blocker log. Re-read this file and `BUILD-PROGRESS.md` at each session boundary to
  recover position before continuing.

## Open spec item

One narrow spec gap is open. It does not block a wave; it needs a one-line decision
before the Phase 13 tenant-update validation ships in Wave 4.

- **The `workspaceTier` downgrade error code.** §15.1 states that `workspaceTier` on
  `PUT /v1/admin/tenants/{id}` is ratcheted stricter-only and attributes the rule to
  §12.9, but §12.9 spells out only the environment-level override stricter rule and
  names no error code for a tenant-update downgrade rejection. The parallel
  `complianceProfile` ratchet has a dedicated `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED`
  code; `workspaceTier` has none. Wave 4 needs either a new
  `WORKSPACE_TIER_DOWNGRADE_PROHIBITED` code or a documented reuse of
  `CLASSIFICATION_CONTROL_VIOLATION` with a `details.reason`.

Two items that earlier progress notes flagged as spec ambiguities were re-verified and
are not spec gaps. The `ExtendLease` direction is settled: §4.7, §8.3, §8.6, and the
proto's own RPC comment all agree that the adapter calls the gateway. The open work is
a proto fix, because the `ExtendLease` RPC currently sits in the gateway-hosted
`Adapter` service and belongs in a gateway-side control service the adapter calls; it
is Wave 2 implementation work and needs no spec decision. The §8.7 file-export model is
fully specified, including the `PreExportMaterialization` per-file interceptor contract
and its `EXPORT_FILE_SCAN_REJECTED` and `EXPORT_FILE_SCAN_SIZE_EXCEEDED` error codes, so
the interceptor is unbuilt Wave 2 work.

## Wave 0 — Restore the verification gate

Goal: make `lenny-test` the authority on completion. Until the harness runs the gate,
a claim of "done" cannot be verified.

- Commit ADR-007 (`SandboxClaim` optimistic locking and failover fencing) and ADR-008
  (open-source license) under `docs/adr/`, closing the Phase 0 gap and §18.40
  prerequisite 1.
- Wire tiers 2, 3, and 4 into the `lenny-test` selectors and the CI workflow so
  `lenny-test --group pr` executes them. These tiers already pass when run directly;
  CI has only ever run static and unit.
- Make `--group pr` the required merge check. The `--group nightly` run adds tiers 5,
  8, and 9 once Wave 5 lands.
- Reconcile `tests/spec-map.json`: every normative spec section gets a test reference
  or an entry in `tests/spec-map-exceptions.yaml` with a reason. 120 of 175 sections
  are currently unmapped.
- Verify the remaining Phase 0 deliverables: branch protection, the DCO sign-off check,
  secret-scanning, and the community-PR CI matrix.

Closes Phase 0 and signs off Phase 1. Prerequisite: none. Size: M.

Exit gate: `lenny-test --group pr` runs tiers 0 through 4 and reports PASS;
`lenny-test validate-maps` is green with no unexplained spec-map gaps.

## Wave 1 — Persistence and infrastructure foundation

Goal: give every gateway store a Postgres backend and make the migration framework run,
so the data plane no longer depends on in-memory fallbacks.

- Postgres backends for the memory-only stores (`evalstore`, `experimentstore`,
  `interactionstore`, `memorystore`, `poolstore`, `quotastore`, `environmentstore`,
  `credentialstore`, `leasestore`, `usagestore`, `delegationpolicystore`,
  `customrolestore`, and the rest), each behind its existing interface.
- A migration runner: `cmd/lenny-migrate` or an embedded runner invoked at startup,
  plus the Phase 1.5 CI gate that runs migrations forward and back on every PR.
- Phase 5.4 etcd encryption at rest: the `EncryptionConfiguration` manifest, the chart
  default, and the documented key-rotation procedure.
- The `agent_pod_state` mirror write path (Phase 3) and the Phase 4 Postgres fallback
  claim path that reads it.
- The Redis pub/sub substrate: mTLS denylist propagation (Phase 3), circuit-breaker
  propagation (Phase 7), and the cross-replica revocation channel (Phase 11).
- Phase 5.6: the credential security design review, recorded under
  `tests/tier9_security/reviews/credential-review.md`. It reviews the credential
  subsystem already built in Phase 5.5 so the findings feed the Wave 2 credential
  hardening; tracked remediation items link to Wave 2 commits.

Closes Phase 1.5, Phase 5.4, and Phase 5.6, and the storage half of Phases 3 and 4.
Prerequisite: Wave 0. Size: L.

Exit gate: `lenny-test --tier component` runs every store against real Postgres and
reports PASS; the Phase 1.5 migration round-trip and the `lenny_tenant_guard`
cross-tenant rejection pass; the Phase 5.4 `etcd_encryption_test` passes on Kind; the
Phase 5.6 credential security review is recorded.

## Wave 2 — Close the gateway's infrastructure-coupled phases

Goal: finish the deferred half of every gateway-resident phase so the contract and
integration tiers go green.

- Phase 4: the adapter workspace-staging RPCs (`PrepareWorkspace`, `FinalizeWorkspace`,
  and `RunSetup`) so the finalize step materializes the workspace instead of
  short-circuiting.
- Phase 5: the `gitClone` materializer that clones the pinned commit into the staging
  area, and confirmation of the `type: mcp` gateway endpoints.
- Phase 5.8: the `AssignCredentials` path that mints proxy leases and populates the
  credential cache.
- Phase 7: the `full_revoke` pod-RPC `Terminate` fan-out, Redis cached-auth
  invalidation, credential-lease revocation, and the external interceptor registration
  framework.
- Phase 9: the `ExtendLease` lease-extension control plane, which includes the proto
  fix that moves the RPC out of the gateway-hosted `Adapter` service into a gateway-side
  control service the adapter calls (§8.6); external gRPC interceptors; and the
  `PreExportMaterialization` interceptor phase.
- Phase 10: the gateway virtual MCP server and the hop-by-hop elicitation chain.
- Phase 11: hot credential rotation (the `credentials_rotated` and
  `credentials_acknowledged` handshake) and cross-replica revocation propagation.
- Phase 12a: KMS envelope encryption in the Token Service, and the full OAuth 2.1
  connector flow with PKCE and a signed `state`.
- Phase 15: the cross-environment delegation resolver, OIDC-group resolution, and the
  transparent-filtering middleware.
- Phase 16: the OpenFeature external-targeting integration.
- Gateway authorization: enforce session-scoped custom roles on the session endpoints,
  and reconcile the `requireAdmin` groupings against the §10.2 role matrix.

Closes the gateway half of Phases 4, 5, 5.8, 7, 9, 10, 11, 12a, 15, and 16.
Prerequisite: Wave 1. Size: XL.

Exit gate: `lenny-test --tier contract` and `lenny-test --tier integration` report
PASS; the `scaffolds_test.go` skips in tiers 3 and 4 are converted to passing tests
with spec-map entries.

## Wave 3 — Adapter, runtimes, and concurrent modes

Goal: complete the runtime and adapter surface and bring it under conformance testing.

- The remaining adapter RPCs: `ReportUsage` end to end, the `LifecycleChannel` stream,
  and the intra-pod platform MCP server over the abstract Unix socket with the
  `mcpNonce` handshake.
- The `delegation-echo` Standard-level reference runtime and the
  `lenny-compliance --level standard` battery.
- Phase 12b: the `type: mcp` runtime-side adapter path and the reference `type: mcp`
  runtime.
- Phase 12c: the workspace-concurrent and stateless-concurrent execution modes with
  pod-level isolation enforcement.

Closes the runtime half of Phase 9, and all of Phases 12b and 12c. Prerequisite:
Wave 2. Size: M.

Exit gate: `lenny-test --tier conformance` reports PASS; `lenny-compliance` passes the
Basic, Standard, and Full batteries against `echo`, `delegation-echo`, and
`streaming-echo`; the tier 4 `concurrent_workspace_test` and `concurrent_stateless_test`
pass.

## Wave 4 — lenny-ops, observability, audit, and backup

Goal: build the agent-operability platform (§25) and the audit, compliance, and backup
pipelines. Phase 13 is the spec's consolidation phase and is the largest wave.

- The `lenny-ops` leader-elected runtime: the cron evaluator, webhook delivery, the
  scheduled-backup runner, the reconciliation goroutines, and self-monitoring.
- `lenny-backup` and the `BackupService` orchestration surface, the
  `lenny-restore-test` CronJob, and MinIO continuous bucket replication.
- The audit pipeline: the OCSF translator binary, the hash-chain verifier, SIEM
  streaming, the EventBus publish-state machine, and the Audit Log Query API.
- Compliance: the `lenny-data-residency-validator` and `lenny-t4-node-isolation`
  webhooks, the tenant-deletion controller, the T4 per-tenant KMS key lifecycle, and
  the Postgres-backed GDPR erasure path under the `lenny_erasure` role.
- The §25 operability APIs: Platform Health, Capacity Recommendations, Diagnostic
  Endpoints, the Operational Event Stream, Configuration Drift Detection, Platform
  Lifecycle Management, the Runbook Index API, Remediation Locks, Escalations, and the
  MCP Management Server.
- The full §16 metrics, alert, operational-event, and audit-event catalogs.
- The two-tier billing failover pipeline and the billing-correction admin workflow.
- The `lenny-ctl` operability extensions per §25.14.

Closes Phase 13, and the audit and erasure remainder of Phases 7 and 12a. Prerequisite:
Wave 3. The `workspaceTier` error-code decision (see "Open spec item") is needed before
the tenant-update validation in this wave ships. Size: XL.

Exit gate: the tier 2 `event_store_test` and the tier 4 `audit_pipeline_test` pass; the
§16 catalog cross-checks against the rendered chart pass; the §25 endpoints are covered
by contract tests.

## Wave 5 — Helm chart and the cluster test tiers

Goal: stand up a complete Kubernetes deployment and bring tiers 5, 8, and 9 online.

- Complete the Helm chart: the remaining admission webhooks (`pool-config-validator`,
  the `crd-conversion` wiring, and the Phase 13 compliance webhooks), the full
  NetworkPolicy posture, the `ServiceMonitor`, `PodMonitor`, and `PrometheusRule`
  templates (Phase 2.5), the `ResourceQuota` and `LimitRange` presets, and the
  `lenny-bootstrap` Job.
- Phase 3 and Phase 3.5 completion: the `DelegationPolicy` CRD and controller sync,
  cluster-CIDR drift detection, the SDK-warm circuit breaker, and the cert-manager PKI
  wiring.
- The Phase 16 PoolScalingController variant-pool sizing path.
- The Phase 14 network and TLS posture: the final default-deny and egress-allowlist
  policy, seccomp validation, cosign image-signing admission, and JWT signing-key
  rotation.
- Build tiers 5 (e2e Kind), 8 (chaos), and 9 (security) from skip-stubs into real
  tests.

Closes Phases 2.5, 3, and 3.5, the controller half of Phase 16, and the cluster half
of Phase 14. Prerequisite: Wave 4. Size: L.

Exit gate: `lenny-test --group nightly` runs tiers 5, 8, and 9 on a real Kind cluster
and reports PASS; e2e admission, NetworkPolicy, and mTLS are verified end to end; the
ADR-007 leader-kill chaos test passes.

## Wave 6 — External product surface and documentation

Goal: deliver the SDKs, reference runtimes, web playground, installer, and
documentation site.

- The client SDKs in Go, TypeScript, and Python (Phase 6) and the runtime-author SDKs
  (Phase 2), with their publish pipelines. SDK work can begin once the Wave 2 API
  surface is stable.
- The first-party reference runtimes per §26.
- The web playground per §27, embedded in the gateway binary.
- The `lenny-ctl install` installer wizard, the `kubectl-lenny` krew plugin, the
  tier-preset values files, and the answer-file catalog.
- The `lenny-ctl runtime init` scaffolder, the `lenny image` subcommands, and
  `lenny token print` (Phase 2).
- The documentation site at general-availability quality, and the API versioning
  surface per §15.5.
- Phase 17b: the semantic-cache store, the Postgres and pgvector memory backend, and
  the evaluation-hook plumbing.

Closes Phases 2, 6, 17a, and 17b. Prerequisite: Wave 5. Size: L.

Exit gate: `lenny-test --tier docs` reports PASS; the SDK conformance suites pass;
every §26 reference runtime is installable through `lenny-ctl install`; the playground
passes the §27.5 protocol suite; the time-to-hello-world benchmark meets the
five-minute target.

## Wave 7 — Load, cloud, hardening, and convergence

Goal: bring up the cloud and load tiers, complete the security hardening, and reach the
full pre-release gate.

- Phase 14: the release pipeline (multi-arch build, cosign signing, and CycloneDX SBOM
  attestation), the Helm chart provenance signing, the release-channel manifest
  publisher, and the external pen-test driver.
- The Phase 14 full-system security design review, recorded under
  `tests/tier9_security/reviews/full-system-review.md`.
- Phase 13.5: the HPA and KEDA custom-metrics pipeline, the tier-promotion harness, and
  the full-system load baseline.
- The load re-baselines for Phases 6.5, 9.5, 11.5, 13.5, 14.5, and 16.5.
- Tiers 6 (e2e cloud on GKE, EKS, and AKS) and 7 (load and SLO) brought online.

Closes Phases 6.5, 9.5, 11.5, 13.5, 14, 14.5, and 16.5. Prerequisite: Wave 6.
Size: L.

Exit gate: `lenny-test --group pre-release` reports PASS across tiers 0 through 11;
every §18 phase exit gate has been run and is green; `tests/spec-map.json` covers every
normative spec section.

## Definition of complete

The build is v1-complete when all of the following hold.

- `lenny-test --group pre-release` reports PASS across tiers 0 through 11.
- Every §18 phase has had its exit-gate group run and reported green, and the
  phase-status table in `BUILD-PROGRESS.md` reads "Done" for every row.
- `tests/spec-map.json` maps every normative spec section to at least one passing test,
  with `tests/spec-map-exceptions.yaml` accounting for the sections that are exempt.
- No `t.Skip` remains in `tests/` except guards for external dependencies that the
  project's own CI cannot run, such as cloud-provider credentials and third-party
  runtime images.

The §20 open questions, the §21 post-v1 surfaces beyond Phase 17b, and the §22 explicit
non-decisions are out of scope for v1 by the spec's own framing.
