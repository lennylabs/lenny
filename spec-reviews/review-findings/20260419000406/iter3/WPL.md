# Iter3 WPL Review

No real issues found.

## Regression checks cleared (iter2 fixes)

All four iter2 fixes in the WPL scope are internally consistent and do not
introduce regressions in pool lifecycle, pod state machine, or pool-sizing
semantics:

- **WPP-005 (idle-override scope)** — `spec/06_warm-pod-model.md` §6.5 and
  `spec/27_playground-mode.md` §27.3 consistently gate playground idle
  overrides on the `origin: "playground"` JWT claim. The claim is stamped by
  all three `playground.authMode` values (oidc / apiKey / dev), so the
  `maxIdleTimeSeconds = min(runtime.limits.maxIdleTimeSeconds,
  playground.maxIdleTimeSeconds)` cap binds uniformly across auth modes. No
  bypass path via variant pools or base-pool flow; `SandboxClaim` carries
  `origin` into the pod annotation used by the warm-pool controller for the
  idle-timer scope decision.

- **SES-005 (starting_session exception transitions)** —
  `spec/06_warm-pod-model.md` §6.2 now lists both
  `starting_session → failed [retries_exhausted]` and
  `starting_session → resume_pending [client_disconnect]` as the two
  documented exceptions to the "pre-attached retries are invisible" rule.
  The visibility bullet below the state diagram correctly names
  `starting_session` as the sole state where retry exhaustion or client
  disconnect becomes externally observable, and cross-references
  `spec/07_session-lifecycle.md` §7.2 where the matching session-level
  `starting → resume_pending` / `starting → failed` transitions appear.
  Pre-attached retry budget (3 attempts, 500ms/1s backoff) is unchanged and
  remains invisible on earlier states (`receiving_uploads`,
  `finalizing_workspace`, `running_setup`).

- **EXM-005 (preConnect re-warm on scrub_warning)** —
  `spec/06_warm-pod-model.md` §6.2 adds
  `task_cleanup → sdk_connecting [scrub_warning]` for preConnect pools so
  the "all idle preConnect pods are SDK-warm" invariant is preserved when
  `onCleanupFailure: warn` short-circuits the normal
  `task_cleanup → idle` path. The paragraph "preConnect re-warm on
  scrub_warning" documents that `scrub_warning` annotation and the
  `lenny_task_pod_scrub_failure_count` metric persist across the
  `sdk_connecting` re-warm, and that the pod is not re-admitted to the
  claimable set until `sdk_connecting → idle` completes. No regression to
  the scrub-variants contract in
  `spec/05_runtime-registry-and-pool-model.md` §5.2.

- **Gateway PDB + rolling-update (§17.8.2)** —
  `spec/17_deployment-topology.md` §17.1 Kubernetes Resources table
  sets `minAvailable: 2` at Embedded/Source Mode, `minAvailable:
  ceil(replicas/2)` at Compose Mode, and §17.8.2 constrains the rolling
  update to `maxUnavailable: 1, maxSurge: 25%`. The rationale correctly
  bounds simultaneous CheckpointBarrier fan-out to one replica's 400-pod
  quota (Compose Mode), keeping drain-phase MinIO throughput within the
  per-replica upload budget documented in §10.1. Previous default
  (`maxUnavailable: 25%`) would have produced up to 2000 simultaneous
  drain uploads at 8-replica Compose Mode. No conflict with
  `coordination_generation` fencing or partial-manifest recovery in
  §10.1.

## Additional internal checks performed

Spot-checked the following WPL invariants across iter2 edits for indirect
regression:

- Pool sizing formula `minWarm >= claim_rate × safety_factor ×
  (failover_seconds + pod_startup_seconds) + burst_p99_claims ×
  pod_warmup_seconds` in `spec/04_system-components.md` §4.6.2 uses
  `failover_seconds = 25` (leaseDuration 15s + renewDeadline 10s in crash
  case) consistent with §4.1 leader-election settings. Mode factors
  (session/task/concurrent-workspace/concurrent-stateless) applied
  correctly; variant pool base-pool adjustment via `Σ variant_weights`
  still netted out at 1.0 when all variants are inactive.

- `initialMinWarm` and `bootstrapMinWarm` reset behavior in
  `spec/06_warm-pod-model.md` §6.5 correctly distinguishes bootstrap
  (one-shot at pool creation) from runtime reconciliation, so the iter2
  variant-pool activation path does not accidentally reset the base-pool
  count on first claim.

- `SandboxClaim` admission via `lenny-sandboxclaim-guard` webhook
  (`failurePolicy: Fail`, `spec/04_system-components.md` §4.6.3) still
  enforces double-claim prevention; the iter2 playground idle-override
  patch did not add a bypass code path. The `podClaimQueueTimeout: 60s`
  and Postgres fallback claim path are preserved.

- `spec/10_gateway-internals.md` §10.1 preStop drain stages (drain
  announcement → quiesce → checkpoint fan-out → ack) and the tiered
  checkpoint cap (30s/60s/90s by workspace size) are unchanged by the
  iter2 gateway-PDB patch. CheckpointBarrier's single wall-clock deadline
  and `coordination_generation` fencing remain the authority for partial
  checkpoint admissibility; the new rolling-update cap affects only
  operator-side concurrency, not intra-drain coordination.

- `spec/05_runtime-registry-and-pool-model.md` §5.2 scrub variants
  (`standard`, `microvm-restart`, `microvm-in-place`) still honor
  `onCleanupFailure: warn` semantics; the iter2 `task_cleanup →
  sdk_connecting [scrub_warning]` transition only applies when the pool
  is preConnect-enabled, so non-preConnect pools retain the original
  `task_cleanup → idle [scrub_warning]` path.

## Missed warm-pool issues

None identified. The 28-file spec is internally consistent on:

- Pool controller reconciliation loop ordering (desired → pending
  creates → pending deletes → claim admission), with SSA-based field
  ownership preventing operator conflicts on `SandboxWarmPool.status`.

- Pod replacement under load: concurrent-workspace mode eviction
  sequence correctly invokes `/workspace/evict` before claim handoff;
  `acknowledgeProcessLevelIsolation` gate still required at the
  `SandboxTemplate` level.

- SDK-warm demotion via `sdkWarmBlockingPaths` and the 90% circuit
  breaker (`spec/06_warm-pod-model.md` §6.6) correctly emit the
  `lenny_pool_sdk_warm_circuit_open` signal before routing fallback
  claims through the cold-claim path.

- Orphan claim detection (60s, leader-only) and certificate expiry
  cleanup are unchanged by iter2 edits.

- Drain-phase interaction with `CheckpointBarrier` MinIO throughput
  budget is now correctly bounded by the rolling-update cap — no
  remaining unreconciled budget risk at Compose Mode.

## PARTIAL / SKIPPED

None. All iter2 fixes in the WPL scope are fully realized in the spec;
no open placeholders, no TODOs, no conditional clauses without matching
behavior. The review covered all 28 files for warm-pool and pod-lifecycle
content.
