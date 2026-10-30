# Proposal: §6.3 startup benchmark — per-runtime-class, pod-warm vs SDK-warm measurement

- **Status:** Implemented (2026-06-18); validated end-to-end on Kind (2026-06-19). No `spec/` edits; this status deliberately does not begin with the word the spec-write guard keys on. The harness defect and the §6.3 line 352 SDK-warm per-class validation are delivered and close F-6.3.5 / F-6.3.10; the residual promotion-gate items (a pod-warm gVisor cloud arm for condition (a) and the Phase 2 exit-gate ADR) are carried forward as a separate follow-up. The tier-7b benchmark now runs against a live Kind cluster with both warm pools up: pod-warm and SDK-warm both measure real claim-to-ready session start at 0% error (gate-canonical claim-to-ready P95 ~0.05–0.10s, within the §6.3 2s runc budget), with real baselines seeded. Reaching that required a broad Kind e2e rehabilitation (landed on `impl/kind-e2e-repair`), which also fixed several genuine, spec-verified production chart/code defects the e2e exposed.
- **Date:** 2026-06-18.
- **Type:** Build-gap / test-infrastructure. This proposal closes the implementation half of BUILD-GAPS finding F-6.3.5 (and its duplicate F-6.3.10). It implements behavior the spec already mandates in §6.3; it does not change `spec/`.
- **Scope:** Replace the `startup_latency` host-process micro-benchmark with a cluster-resident benchmark that measures claim-to-ready session start. Tier 7b measures pod-warm versus SDK-warm on runc every PR; tier 12 measures SDK-warm across runc, gVisor, and Kata in an operator-provisioned cloud cluster. Add the cloud node-pool provisioning the gVisor and Kata arms require.

> **Guard-hook note.** This proposal stages no `spec/` edits. When it is signed off, record approval with a status that does not begin with the literal word `Approved` (for example `Signed off for implementation`), because the repository's pre-write hook treats any `proposals/*.md` line beginning `- **Status:** Approved` as authorization to edit `spec/`. This proposal must never grant that.

## 1. Problem

F-6.3.5 reports that the artifact both `BUILD-PROGRESS.md` and `TESTING.md` call "the Phase 2 startup benchmark harness" measures a substantively different thing from what §6.3 mandates. The finding has been DEFERRED across several batches on infrastructure grounds.

### 1.1 The finding is still valid

`tests/tier7b_load_kind/scenarios/startup_latency/main.go` was relocated into the Kind load tier, but its code was never converted. It still:

- Builds `cmd/runtimes/echo` into a temp directory and launches it as a local child process with `exec.CommandContext`, with no Kubernetes, no warm pool, no pod claim, and no gateway.
- Sends one `{"type":"heartbeat"}` line and times the first stdout byte. The recorded baseline is P50 = 2 ms, P95 = 3 ms.
- Measures none of the six §6.3 hot-path phases, has no runtime-class split, and has no SDK-warm arm.

It is also the only scenario in `tier7b_load_kind` with no entry in `scaffolds_test.go`, so the suite never exercises it; it is a standalone baseline generator.

§6.3 requires (spec/06_warm-pod-model.md):

- Line 352: "The startup benchmark harness (Phase 2) must measure pod-warm vs SDK-warm latency per runtime class to validate the complexity tradeoff of the SDK-warm model."
- Line 350 (Tier 2 promotion gate): validated P50/P95/P99 for all hot-path phases across all supported runtime classes (runc, gVisor, Kata). The gate is satisfied when (a) actual P95 pod-warm session start latency is measured and ≤ the targets for runc **and gVisor**, (b) the per-phase histograms `lenny_session_startup_phase_duration_seconds{phase, runtime_class}` are instrumented and producing data in the benchmark environment, and (c) the run is recorded as an annotated benchmark run attached to the Phase 2 exit-gate ADR.

### 1.2 What changed since the deferral

- **F-6.1.1 (SDK-warm path) is CLOSED.** The `echo` (pod-warm) and `preconnect-echo` (SDK-warm) reference runtimes both exist, so the warming-model comparison is buildable.
- **Condition (b) is already satisfied in production code.** F-6.3.1/6.3.2/6.3.3 wired `lenny_session_startup_phase_duration_seconds{phase, runtime_class}`, `lenny_session_startup_duration_seconds`, and the TTFT histogram on the production start path.
- **The cluster-resident load tier exists.** `tier7b_load_kind` (Kind, every PR) and `tier12_load_cloud` (operator-driven cloud) both drive `POST /v1/sessions/start` through k6 against a real gateway. The sibling `pod_claim_latency` scenario is the established pattern.

The residual blocker is narrow: the per-runtime-class arms require `gvisor` and `kata` RuntimeClasses, which the Kind cluster does not carry. The binding gate condition (a) names runc and gVisor; gVisor's `runsc` runs without hardware virtualization, and Kata requires KVM. The deferral conflated the harness defect (fully addressable code) with the operator-gated SLO numbers.

## 2. Decisions

These were settled with the requester before drafting.

1. **Fix the harness defect now on Kind.** The runc pod-warm and runc SDK-warm arms run on Kind every PR. This delivers the runc pod-warm gate number (condition (a) for runc) and the runc pod-warm-versus-SDK-warm comparison.
2. **Two baseline files per tier-7b arm** (`startup_latency_pod_warm.json`, `startup_latency_sdk_warm.json`), matching the existing one-file-per-scenario corpus convention.
3. **The cloud tier validates the SDK-warm model per runtime class.** Tier 12 runs SDK-warm only, across runc, gVisor, and Kata, in one provisioned environment so the three per-class SDK-warm numbers are directly comparable. Pod-warm measurement stays on tier 7b (runc). The cloud tier does not produce a pod-warm gVisor number, so promotion-gate condition (a) for gVisor remains out of scope here; this was an explicit choice.
4. **Write the cloud node-pool provisioning now** for both gVisor and Kata as runnable scripts, beyond documenting the manual steps.

## 3. Design overview

The benchmark splits across the two load tiers by what each environment can run.

| Tier | Cadence | Cluster | Runtime classes | Warming models | Role |
|:--|:--|:--|:--|:--|:--|
| 7b (`load_kind`) | every PR | Kind (runc only) | runc/standard | pod-warm, SDK-warm | Fixes the harness defect; exercises the §6.3 line 352 warming-model comparison on runc continuously; produces the runc pod-warm gate number. |
| 12 (`load_cloud`) | operator-driven, opt-in | cloud (provisioned) | runc, gVisor, Kata | SDK-warm | Produces the per-runtime-class SDK-warm P50/P95 that validates the §6.3 line 352 SDK-warm complexity tradeoff across isolation classes, in one comparable environment. |

Both tiers drive load against `POST /v1/sessions/start`, the create-and-claim path. The handler completes the bind (pod claim, credential assignment, and the agent-session-start RPC) before it writes the 201, so the session-start envelope includes agent session start, which is exactly the phase SDK-warm eliminates. The pod-warm versus SDK-warm comparison is therefore observable on this path.

Two measurements come out of each run, because two envelopes are in play:

- **Client-side wall-clock of `POST /v1/sessions/start`** is the per-arm baseline that the regression diff gates on, the same way `pod_claim_latency` records one baseline per run. It is the broad envelope: pod claim, credential assignment, agent session start, plus workspace materialization and any setup commands.
- **The server histogram `lenny_session_startup_duration_seconds{runtime_class}`** is the gate-canonical number for condition (a). Per `recordStartupMetrics` in `start.go`, that metric is `PodClaim + CredentialAssignment + AgentSessionStart` and deliberately excludes workspace materialization and setup commands, which is precisely the 2s runc / 5s gVisor claim-to-ready SLO envelope. The benchmark reads this histogram's P95 per runtime class from the gateway `/metrics` endpoint after the load run.

The benchmark drives sessions with an empty workspace plan (no uploads) against runtimes with no setup commands (`echo`, `preconnect-echo`), so the two envelopes nearly coincide and the client baseline stays close to the histogram number. Condition (a) is asserted against the histogram rather than the client wall-clock, so the SLO check matches the metric the spec names.

## 4. Detailed design

### 4.1 Scenario contract

A k6 scenario `startup_latency/main.js` drives `POST /v1/sessions/start` under a constant-VU pool, identical in mechanism to `pod_claim_latency`. It reads:

- `LENNY_RUNTIME` — the `runtimeRef` on the create body (selects the pool, and thereby the warming model and runtime class).
- `LENNY_WARMING_MODEL` — `pod_warm` or `sdk_warm`, recorded as a k6 tag and surfaced in the result for the baseline filename.
- `LENNY_RUNTIME_CLASS` — `runc`, `gvisor`, or `kata`, recorded as a k6 tag.

It reports p50, p90, and p95. It does not report p99, and it does not persist the measured binary's path. These two constraints preserve the F-6.3.13 and F-6.3.14 resolutions already encoded in the current harness doc-comment (p99 needs roughly 1000+ samples to defend at one-sample resolution; the host path leaked a developer's private directory into version control).

The two tiers keep their own copies of the scenario, matching the existing convention where `tier7b_load_kind/scenarios` and `tier12_load_cloud/scenarios` hold per-tier scripts resolved through `load.Options.ScenarioRoot`.

A small `tests/testinfra/load` helper reads the gateway `/metrics` endpoint after the run, parses the `lenny_session_startup_duration_seconds_bucket{runtime_class="…"}` series, and computes the P95 by the standard histogram-bucket interpolation. This is the gate-canonical number for the runc and gVisor arms. The helper is the only new non-script Go in the change and carries its own unit test against a recorded exposition fixture.

### 4.2 Tier 7b (Kind) — runc pod-warm and SDK-warm

- `tests/testinfra/kind/agent-workload.yaml` gains a `preconnect-echo` Runtime (`capabilities.preConnect: true`, sidecar deployment model), a SandboxTemplate, and a SandboxWarmPool, so the cluster warms SDK-warm pods through `sdk_connecting` and the gateway drives a genuine SDK-warm session start. The existing `echo-runtime-sidecar` pool is the pod-warm arm.
- `tests/testinfra/kind/install.sh` gains `lenny-runtime-preconnect-echo=runtimes/preconnect-echo` in `RUNTIME_IMAGES` so the SDK-warm image is built into the Kind node.
- `tests/tier7b_load_kind/scaffolds_test.go` gains `TestStartupLatencyPodWarm` and `TestStartupLatencySDKWarm`. Each gates on `kind.InstallLenny` and `load.SkipUnlessAvailable`, port-forwards the gateway, runs the scenario against its pool with `LENNY_RUNTIME_CLASS=runc`, and diffs against its baseline. Each carries a `// spec: 6.3` annotation and a `// diagnosis:` comment per the tier-2-and-up test convention.
- `tests/tier7b_load_kind/baselines/startup_latency_pod_warm.json` and `startup_latency_sdk_warm.json` replace `startup_latency.json`. The two arms share the one `scenarios/startup_latency/` directory: `load.RunScenario` resolves the script from the scenario name, while `load.AssertBaseline` resolves `baselines/<key>.json` from the key it is passed, so each scaffold passes a distinct baseline key (`startup_latency_pod_warm`, `startup_latency_sdk_warm`) against the same script.
- `main.go` and `main_test.go` are deleted.

### 4.3 Tier 12 (cloud) — SDK-warm per runtime class

- `tests/tier12_load_cloud/scenarios/startup_latency/main.js` is the cloud copy of the scenario.
- `tests/tier12_load_cloud/session_startup_latency_test.go` runs three SDK-warm arms: runc, gVisor, and Kata. Running all three in one provisioned cluster makes the per-class SDK-warm numbers directly comparable, which is the point of the §6.3 line 352 complexity-tradeoff validation. Each arm gates on its SDK-warm pool being present: when the operator did not provision the class (the pool or its RuntimeClass is absent), the arm skips with a `NEEDS-OPERATOR` message naming the missing RuntimeClass, mirroring the existing `requireCloudLoad` opt-in idiom and the `TestGateway10kSessions` cloud-only skip. When the class is present, the arm runs and records its baseline.
- Per-arm baselines under `tests/tier12_load_cloud/baselines/` (`startup_latency_runc_sdk_warm.json`, `startup_latency_gvisor_sdk_warm.json`, `startup_latency_kata_sdk_warm.json`).

The cloud tier measures SDK-warm only; pod-warm stays on tier 7b. §6.3 gives a claim-to-ready P95 target only for runc (2s) and gVisor (5s), and only for pod-warm. Because this tier measures SDK-warm, it asserts no §6.3 SLO against any arm: each arm diffs its measured histogram P95 against its stored baseline (the regression gate every load scenario uses) and reports the P50/P95 for the annotated run. The SDK-warm number is expected at or below the pod-warm target for its class, since SDK-warm removes agent session start, but the spec sets no separate SDK-warm SLO to assert against.

### 4.4 Cloud workload pools and the isolation-profile mapping

`tests/testinfra/k8s/agent-workload-load.yaml.tmpl` currently registers four pools, all `isolationProfile: standard`. It gains three `preconnect-echo` SDK-warm pools, one per isolation profile, per the §5.3 mapping (`standard`→`runc`, `sandboxed`→`gvisor`, `microvm`→`kata`):

- `isolationProfile: standard` (runc SDK-warm), rendered by default.
- `isolationProfile: sandboxed` (gVisor SDK-warm), gated by `${LOAD_ENABLE_GVISOR}`.
- `isolationProfile: microvm` (Kata SDK-warm), gated by `${LOAD_ENABLE_KATA}`.

The gVisor and Kata pool blocks render only when the operator's provisioning step enabled the class, so the default cloud workload (standard only) is unchanged. The runc SDK-warm pool on the cloud cluster is what the tier-7b Kind runc SDK-warm arm is to the smoke cluster; running it again in the cloud environment alongside gVisor and Kata gives a same-cluster baseline the three classes are compared against.

### 4.5 Node-pool provisioning and the deployer-managed-infrastructure boundary

The repository deliberately does not provision clusters or node pools. `scripts/cloud/gcp/up.sh` states the cluster is "operator-supplied — the Lenny Terraform module does not create cluster / VPC / node-pool layers (per the spec note on deployer-managed infrastructure)," and §17 specifies Kata node pools as dedicated, taint-isolated, deployer-provisioned hardware.

To honor that boundary while still giving the benchmark a turnkey path, node-pool provisioning lands as a **separate opt-in benchmark helper**, not a change to the `up.sh` contract:

- `scripts/cloud/<provider>/up-runtimeclass-pools.sh` creates the gVisor and Kata node pools and the matching RuntimeClass objects, with the §5.3 Pod Overhead values and the §17 nodeSelector, hard node affinity, and `NoSchedule` taint for Kata. A `down-runtimeclass-pools.sh` tears them down.
- `scripts/cloud/<provider>/run-load.sh` invokes the helper only when `LENNY_BENCH_RUNTIME_CLASSES` names a class beyond `runc`, then exports `LOAD_ENABLE_GVISOR` / `LOAD_ENABLE_KATA` for the workload template.

Provider reality, stated honestly:

- **gVisor** is broadly achievable. GKE Sandbox provisions a `runsc` node pool directly (`gcloud container node-pools create --sandbox type=gvisor`); on EKS and AKS it is a self-managed node group with gVisor installed. gVisor needs no hardware virtualization.
- **Kata** requires nested virtualization. It is available on bare-metal-class nodes (AWS `*.metal`, Azure nested-virt SKUs) and on self-managed node pools with `kata-containers` installed; it is not a first-class managed-Kubernetes feature on every provider. The helper provisions Kata where the provider exposes nested-virt nodes and otherwise fails with a clear message naming the requirement. On a provider without nested-virt node pools, the Kata arm remains a documented operator step.

### 4.6 Documentation and ledger

- `TESTING.md` §13.3: rewrite the harness description. The benchmark drives real claim-to-ready pod-warm versus SDK-warm session start on a cluster for the runc class on every PR, and the per-runtime-class (runc, gVisor, Kata) SDK-warm measurement runs in the cloud load tier against an operator-provisioned cluster. Remove the "heartbeat round-trip P50=2 ms" framing.
- `BUILD-GAPS.md` F-6.3.5 and F-6.3.10: record the harness-defect fix and the SDK-warm comparison as done, and narrow the deferral to the operator-provisioned remainder (a gVisor/Kata cloud node pool plus the Phase 2 exit-gate ADR built from a tier-12 run).

## 5. Promotion gate and ADR

After this lands:

- Condition (b) is satisfied (already true: the per-phase histograms produce data; the benchmark now drives them under controlled load and reads them).
- Condition (a) for **runc** is satisfied: the tier-7b run produces the runc pod-warm histogram P95 against the 2s target every PR.
- Condition (a) for **gVisor** is **not** satisfied by this work. The chosen scope measures SDK-warm across the three classes in the cloud tier and pod-warm only on runc, so no pod-warm gVisor number is produced. This is the explicit scope decision recorded in Section 2; the gVisor pod-warm gate number remains a follow-on (a pod-warm gVisor cloud arm would add it).
- Condition (c) (the Phase 2 exit-gate ADR) is not authored here. It records the results of a gate-clearing run, which requires the gVisor pod-warm number condition (a) still lacks.

What this delivers against §6.3 is the SDK-warm-per-class validation of line 352 (the model whose complexity tradeoff the spec singles out for validation), the runc pod-warm gate number, and a corrected ledger. The finding moves from "harness measures the wrong thing" to a precise, smaller remainder: a pod-warm gVisor cloud arm plus the Phase 2 exit-gate ADR. The line-350 full-coverage statement (pod-warm P50/P95/P99 across runc, gVisor, Kata) is not reached by this scope.

## 6. Proposed spec changes

None. §6.3 already mandates the harness, the per-runtime-class measurement, the promotion gate, and the Phase 2 exit-gate ADR. The §5.3 isolation-profile→RuntimeClass mapping and the §17 Kata node-pool controls already exist. This proposal implements that surface in test infrastructure, which `spec-driven-development.md` classifies as build tooling governed by `code-best-practices.md` rather than a spec behavior. No `spec/` file is touched.

## 7. Files changed

New:

- `tests/tier7b_load_kind/scenarios/startup_latency/main.js`
- `tests/tier12_load_cloud/scenarios/startup_latency/main.js`
- `tests/tier12_load_cloud/session_startup_latency_test.go`
- `tests/testinfra/load/startup_histogram.go` and its test (the `/metrics` scrape + P95 helper, with an exposition fixture)
- `tests/tier7b_load_kind/baselines/startup_latency_pod_warm.json`, `startup_latency_sdk_warm.json`
- `tests/tier12_load_cloud/baselines/startup_latency_<class>_<model>.json` (per provisioned arm)
- `scripts/cloud/<provider>/up-runtimeclass-pools.sh`, `down-runtimeclass-pools.sh` (aws, gcp, azure)

Modified:

- `tests/tier7b_load_kind/scaffolds_test.go` (two new scaffold tests)
- `tests/testinfra/kind/agent-workload.yaml` (preconnect-echo SDK-warm pool)
- `tests/testinfra/kind/install.sh` (build preconnect-echo image)
- `tests/testinfra/k8s/agent-workload-load.yaml.tmpl` (SDK-warm, gVisor, Kata pools)
- `scripts/cloud/<provider>/run-load.sh` (invoke the provisioning helper, export the enable flags)
- `TESTING.md` (§13.3 rewrite)
- `BUILD-GAPS.md` (F-6.3.5, F-6.3.10 status)

Deleted:

- `tests/tier7b_load_kind/scenarios/startup_latency/main.go`, `main_test.go`
- `tests/tier7b_load_kind/baselines/startup_latency.json`

## 8. Testing

Per `test-coverage.md`, this is a tier-7 load and SLO change, so it touches tiers 0, 1, and 7b locally, and tier 12 in the operator-provisioned cloud environment.

- Tier 0 and tier 1: the scenario scripts and scaffolds build and vet; the baseline-diff logic and any percentile helper carry unit tests.
- Tier 7b: `TestStartupLatencyPodWarm` and `TestStartupLatencySDKWarm` run on the Kind cluster and diff against the new baselines. Each carries `// spec: 6.3` and `// diagnosis:`.
- Tier 12: the per-runtime-class matrix runs against a provisioned cloud cluster. Per the tier-6/12 escape hatch in `test-coverage.md`, the arms whose RuntimeClass is not provisioned skip with a stated dependency, and the arms that are provisioned run.

The k6 scenarios themselves are the load artifacts; the Go scaffolds are the test bodies the `// spec:` annotation maps to §6.3.

## 9. Non-goals

- The Phase 2 exit-gate ADR. It records the results of a gate-clearing run, so it is authored after the run exists.
- The pod-warm gVisor and Kata measurements, and therefore the promotion-gate condition (a) gVisor number and the line-350 full pod-warm coverage. The chosen scope measures SDK-warm in the cloud tier and pod-warm only on runc. A pod-warm gVisor/Kata cloud arm is the follow-on that closes the gate.
- Per-phase histogram instrumentation. F-6.3.1/6.3.2/6.3.3 already wired it; this proposal drives it under load and reads it, but adds no new production metric.
- Any change to `up.sh`'s operator-supplied-cluster contract. Node-pool provisioning is a separate opt-in helper.
- Kata on providers without nested-virt node pools. Documented as an operator step where the provider cannot expose it.

## 10. Risks and open questions

- **The line 352 pod-warm-versus-SDK-warm comparison is complete only on runc.** Tier 7b measures both warming models on runc, so the per-class comparison exists there. The cloud tier measures SDK-warm across the three classes, so for gVisor and Kata there is an SDK-warm number but no same-environment pod-warm number to compare it against. The cross-class read the cloud tier supports is how SDK-warm latency itself varies by isolation class.
- **Kata availability is provider-dependent.** On managed Kubernetes without nested-virt node pools, the Kata arm cannot run and skips with a stated dependency.
- **gVisor-in-Kind is not attempted.** Per the recorded decision, gVisor stays in the cloud tier rather than as a custom Kind node image. The Kind tier measures runc only.
- **Cloud cost.** The tier-12 arms run against operator-provisioned, billed resources. They are opt-in behind `LENNY_LOAD_CLOUD_PROVIDERS` and the new `LENNY_BENCH_RUNTIME_CLASSES`.
- **Baseline stability across classes.** Each class has its own baseline; gVisor and Kata carry higher and more variable claim-to-ready latency than runc, so their baselines are seeded from a provisioned run rather than guessed.

## 11. Findings closed or narrowed on application

- F-6.3.5: harness-defect half closed. The benchmark measures real cluster-resident session start: runc pod-warm versus SDK-warm on tier 7b, and SDK-warm across runc, gVisor, and Kata on tier 12. The deferral narrows to a precise remainder: a pod-warm gVisor cloud arm for promotion-gate condition (a), the full pod-warm line-350 coverage, and the Phase 2 exit-gate ADR.
- F-6.3.10: duplicate of F-6.3.5; the `TESTING.md` ledger claim is corrected in the same change.

## 12. Resolved in adversarial review

### Pass 1 (2026-06-18)

1. **Envelope mismatch between client wall-clock and the SLO metric.** The first draft measured condition (a) from client-side wall-clock of `POST /v1/sessions/start`. `recordStartupMetrics` in `pkg/gateway/sessionserver/start.go` shows the 2s/5s claim-to-ready SLO is `PodClaim + CredentialAssignment + AgentSessionStart` and excludes workspace materialization and setup, whereas the client wall-clock includes them. Resolved: the gate number is read from the `lenny_session_startup_duration_seconds{runtime_class}` histogram, the client wall-clock stays as the regression baseline, and the benchmark drives empty-workspace, no-setup runtimes so the envelopes nearly coincide (Section 3, 4.1).
2. **Unsourced Kata SLO.** The draft asserted "Kata ≤ 8s." §6.3 gives a claim-to-ready target only for runc (2s) and gVisor (5s); condition (a) binds only those two. The "~3–8s" Kata figure is an agent-start savings estimate under a different heading. Resolved: the Kata arm records a baseline and reports its histogram P95 but asserts no §6.3 SLO (Section 4.3).
3. **Whether the SDK-warm comparison is observable.** Confirmed against `start.go` that the handler completes the agent-session-start RPC before responding, so the SDK-warm saving is inside the measured envelope; the comparison is meaningful rather than hollow (Section 3).
4. **Two baselines from one scenario directory.** Confirmed `load.RunScenario` resolves the script from the scenario name and `load.AssertBaseline` resolves the baseline path from the key it is passed, so distinct per-arm baseline keys against one script directory are supported (Section 4.2).
5. **New non-script Go surface named.** The `/metrics` scrape and P95 helper is the only new Go beyond scripts and scaffolds; added to the file list with its own unit test and exposition fixture (Section 4.1, 7).

### Pass 2 (2026-06-18) — scope resolution

- **Tier-12 matrix scope resolved to SDK-warm × {runc, gVisor, Kata}** (3 arms), run in one provisioned cluster for comparability. Pod-warm stays on tier 7b (runc). The full six-arm matrix was declined. The accepted consequence: the cloud tier produces no pod-warm gVisor number, so promotion-gate condition (a) for gVisor and the line-350 pod-warm coverage are out of scope and remain the narrowed remainder. Sections 2, 3, 4.3, 4.4, 5, 9, 10, 11 updated to match. Both gVisor and Kata node pools are still provisioned, since all three SDK-warm arms run.
- **Kata provisioning failure mode confirmed:** on a provider without nested-virt node pools, the Kata helper fails with a clear message and the gVisor and runc arms still run; the Kata arm skips with a stated dependency.

### Pass 3 (2026-06-18) — post-implementation review (22 findings) and reconciliations

A five-dimension adversarial review of the implemented diff confirmed 22 findings. The substantive code fix and the design reconciliations:

- **Histogram quantile bug (medium, fixed).** `quantileDelta` computed the P95 from the difference of two cumulative histograms without enforcing monotonicity, so concurrent traffic in lower buckets between the two snapshots (the shared per-runtime_class histogram) or a partial counter reset could feed `sort.Search` a non-monotonic array and yield a silently wrong P95. Fixed with a Prometheus-style forward max-pass (`ensureMonotonic`) plus a `+Inf`-bucket presence check, with new tier-1 tests for the non-monotonic, full-reset, `+Inf`-only, no-`+Inf`, empty, and malformed-`le` paths and an `httptest` cover for `ScrapeStartupBuckets`.
- **Node-pool gating (reconciliation).** The implementation does not use the `${LOAD_ENABLE_GVISOR}` / `${LOAD_ENABLE_KATA}` single-template envsubst flags described in Section 4.4. envsubst cannot conditionally omit a block, so the gVisor and Kata RuntimeClasses and SDK-warm pools live in their own templates (`runtimeclass-{gvisor,kata}.yaml.tmpl`, `runtimeclass-pool-{gvisor,kata}.yaml.tmpl`) that `up-runtimeclass-pools.sh` applies only for the classes named in `LENNY_BENCH_RUNTIME_CLASSES`. Section 4.4's flag description is superseded by this separate-template approach.
- **Tier-12 driver (reconciliation).** The tier-12 provisioning wiring landed in `scripts/cloud/<provider>/run-load-legacy.sh`, which is the functional direct kubectl driver. The newer `run-load.sh` is a loadctl-API trigger stub whose endpoint a later wave wires (`POST /api/v1/runs` currently returns "Wave 6 wires the real endpoint"), so it is not yet a working tier-12 entry point. The legacy script is therefore the correct integration point today; when the loadctl path is built, it must carry the same `LENNY_BENCH_RUNTIME_CLASSES` plumbing, `LOAD_PRECONNECT_RUNTIME_IMAGE`, and `up-runtimeclass-pools.sh` invocation.
- **Tier-12 baselines (reconciliation).** The tier-12 arms gate on `assertScenarioRan` (the suite-wide error-rate gate) and log the per-class SDK-warm P95; they do not commit per-arm baseline JSON files. The Section 4.3 / Section 7 mention of tier-12 baseline files is superseded by this convention, which matches the rest of the tier-12 suite.
- **Shell robustness (fixed).** The AWS Kata node taint now applies on both the create and reuse paths; the AWS and Azure up-scripts fetch the cluster kubeconfig before applying; the GCP legacy `RUNTIMECLASSES` heredoc no longer creates a `gvisor` (handler `runsc`) object that would shadow GKE Sandbox's managed one nor the misnamed `kata-containers` object (the §5.3 microvm profile maps to `kata`).
- **Known follow-up:** `spec/18_build-sequence.md` still names the deleted `baselines/startup_latency.json`. Correcting it requires the spec-proposal pipeline (the guard hook blocks direct `spec/` edits), so it is left as a small spec follow-up rather than hand-edited here.
