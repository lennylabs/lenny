# Perspective 19: Build Sequence & Implementation Risk — Iter5

**Scope:** `spec/18_build-sequence.md` only (with cross-references to §17 preflight / chart inventory for dependency verification).

**Iter4 carryover verification:**

- **BLD-009 (High, Phase 8 deploys `lenny-drain-readiness`).** Fixed in iter4. `spec/18_build-sequence.md` Phase 8 (line 48) now contains the explicit webhook first-deploy clause with `features.drainReadiness=true` flip, HA contract (`replicas: 2`, `podDisruptionBudget.minAvailable: 1`, `failurePolicy: Fail`), `lenny.dev/component: admission-webhook` pod label, and `DrainReadinessWebhookUnavailable` alert wiring. §17.9 row 497 skips the check when `features.drainReadiness=false`. No regression.
- **BLD-011 (High, phase-aware preflight enumeration).** Fixed in iter4 via Option A (feature-flag chart inventory). `spec/17_deployment-topology.md` §17.2 lines 59–71 define the three feature flags (`features.llmProxy`, `features.drainReadiness`, `features.compliance`) with their Phase-5.8/8/13 first-deploy assignments, the preflight expected-set composition rule, and the four-case parameterised `admission_webhook_inventory_test.go` table. `spec/18_build-sequence.md` Phase 3.5 (line 14) adds the "Phase-aware preflight and chart inventory" paragraph, and Phases 5.8/8/13 each call out the feature-flag flip. The chain is internally consistent. No regression.
- **BLD-010 (Low, Phase 1 Shared Adapter Types / SessionEventKind registry).** NOT fixed in iter4. `spec/18_build-sequence.md` line 8's Phase 1 wire-contract list still contains only `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, `schemas/outputpart.schema.json`, `schemas/workspaceplan-v1.json`; no `pkg/adapter/shared.go` or `schemas/session-event-v1.json`. Re-raised below as **BLD-013** with iter4's severity anchoring preserved (Low).

Iter4 introduced §18.1 "Build Artifacts Introduced by Section 25" as an index-only appendix listing `lenny-ops`, `lenny-backup`, the shared `pkg/alerting/rules` / `pkg/recommendations/rules` packages, and the `pkg/common/registry/resolver.go` `ImageResolver`. Only a "Pre-GA ordering" note ties these to Phase 17a/Phase 14; **no Phase 0–17 row names them as deliverables**. Cross-referencing §17.8.5 (mandatory `lenny-ops` Deployment, chart validation rejects disabling) and §17.9 row 501 (preflight `lenny-ops-sa` RBAC) produces the Phase-3.5 infeasibility captured in **BLD-012** below.

---

### BLD-012. `lenny-ops` has no phase assignment yet is mandatory from the first chart install (Phase 3.5) [High]

**Section:** spec/18_build-sequence.md §18.1 lines 79–92 (Build Artifacts from Section 25); spec/17_deployment-topology.md §17.8.5 line 1293 ("mandatory in every Lenny installation"); §17.2 lines 15–19 (`lenny-ops` Deployment, `lenny-gateway-pods` headless Service, `lenny-backup` Jobs, NetworkPolicies, Lease); §17.9 row 501 (`lenny-ops-sa` RBAC preflight check); spec/25_agent-operability.md §25.4 line 780 ("Every Lenny installation includes a `lenny-ops` deployment regardless of tier — there is no supported topology without it").

Iter4 added §18.1 as an index of "build-pipeline requirements … layered into the existing phases above." The index names two new container images (`lenny-ops`, `lenny-backup`) and three shared Go packages (`pkg/alerting/rules`, `pkg/recommendations/rules`, `pkg/common/registry/resolver.go` `ImageResolver`), but the only phase-ordering language is the Pre-GA ordering note at line 92:

> "The new images … and the `pkg/common/registry/resolver.go` `ImageResolver` are prerequisites for the Phase 17a community-launch documentation pass — operability must be complete before external deployers are invited."

This treats `lenny-ops` and `lenny-backup` as Phase-17a prerequisites only. But §17.8.5 and §25.4 state unequivocally that `lenny-ops` is mandatory in every install regardless of tier, that "Attempts to disable `lenny-ops` via Helm values are rejected at chart validation," and that §17.2's canonical component layout (lines 15–19) always renders the `lenny-ops` Deployment, the `lenny-backup-sa` ServiceAccount, the `lenny-ops-leader` Lease, the headless `lenny-gateway-pods` Service, and the four NetworkPolicies (`lenny-ops-deny-all-ingress`, `lenny-ops-allow-ingress-from-ingress-controller`, `lenny-ops-egress`, `lenny-backup-job`). The `lenny-preflight` Job's row 501 (`lenny-ops-sa` RBAC) is unconditional — there is no `features.ops`-style flag gating it, unlike the three webhook flags documented in §17.2's Feature-gated chart inventory paragraph.

Concrete consequences, strictly reading the current build sequence:

1. Phase 3.5 is the first phase where a Helm chart is installed (admission policies, NetworkPolicies, ResourceQuota, LimitRange, `lenny-preflight`, `lenny-bootstrap`). With the current §18.1 wording, the Phase 3.5 chart will not have a `lenny-ops` image built, yet §17.8.5's chart-validation rejection and §17.9 row 501's `lenny-ops-sa` RBAC check will both fire. The `lenny-preflight` Job itself runs as a `helm.sh/hook: pre-install` Job that *already* expects `lenny-ops-sa` to be present — so the install fails-closed on the first attempted deployment.
2. Phases 2 (`make run` local dev) and 2.5 (structured logging) notionally could precede `lenny-ops`, but Phase 3.5 is the cliff edge: every phase from 3.5 onward deploys via Helm and therefore requires `lenny-ops` to be either (a) built and shipping as a baseline component or (b) gated behind a feature flag parallel to the three admission-webhook flags, with §17.8.5's "mandatory" statement relaxed to "mandatory from Phase X onward."
3. Phase 4.5 (bootstrap seed mechanism, admin API) is where `lenny-ops`'s admin-API fan-out dependency first becomes load-bearing — `lenny-ops` reads from the gateway admin API per §25.3 — so a strict minimum Phase-X for `lenny-ops` is Phase 4.5, not Phase 17a. But the Helm-chart-validation contract in §17.8.5 is the stronger constraint because it fires at Phase 3.5 chart install time.
4. `lenny-backup` is transient (Jobs created on-demand) and its prerequisite is looser, but §25.11's backup-and-restore API is a documented operability deliverable that §13's operational-readiness milestone implicitly depends on. Without a phase assignment, Phase 13 ("Full observability stack … operational readiness") reads as complete while a core operability binary is still unbuilt.
5. The shared `pkg/alerting/rules` package is load-bearing from Phase 16.5 and earlier: §16.5's alert catalog is consumed by the Helm chart's `PrometheusRule` template (§17.2 line 524, §17.9 row 674) and by the in-process gateway evaluator, so the package must exist before any phase that ships a deployer-visible alerting rule — which is Phase 13 at the latest ("alerting rules, SLO monitoring"), and Phase 3.5 earlier if the `AdmissionWebhookUnavailable`/`SandboxClaimGuardUnavailable` alerts are rendered at that phase.
6. The `pkg/recommendations/rules` shared package is consumed by both the gateway's `/v1/admin/ops/recommendations` endpoint and `lenny-ops`'s aggregated view (§25.3 line 602). It has the same earliest-phase-required argument as `pkg/alerting/rules`: it must exist before any phase that ships the recommendations endpoint in the gateway, which is implicit in Phase 4.5's admin-API foundation.
7. The `ImageResolver` in `pkg/common/registry/resolver.go` is consumed by every Lenny Deployment's image reference per §17.8.6 line 1304 ("The chart's `ImageResolver` shared package … composes every image reference from `platform.registry.*`, ensuring the gateway, `lenny-ops`, controllers, `lenny-backup`, and the warm-pool controller all honor the same registry configuration"). Phase 3.5's chart slice renders at minimum the gateway, controller, and admission-webhook Deployments, all of which must resolve their image references through `ImageResolver`. So `ImageResolver` is a Phase-3 prerequisite at the latest (the chart first ships digest-pinned controller-created pod images per Phase 3's note), and arguably a Phase-1-or-2 prerequisite because Phase 2's `make run` embedded-component binary and Phase 3's digest-pinned images both need deterministic resolution.

The "Pre-GA ordering" note at §18.1 line 92 addresses only the soft constraint ("operability must be complete before external deployers are invited") but not the hard chart-validation constraint at §17.8.5 and the hard preflight constraint at §17.9 row 501. This is the same class of gap as iter3 BLD-005 (webhook first-deploy phase unassigned) and iter4 BLD-011 (preflight expected-set phase-aware), and resolves cleanly using the same feature-flag mechanism or a dedicated phase-assignment clause.

**Recommendation:** Amend §18 with phase-assignment clauses for the §18.1 artifacts. Two mechanically-equivalent options; Option A is the minimum-edit path, Option B is more structurally consistent with the iter4 BLD-011 fix:

- **Option A — name the artifacts in each first-consuming phase row, same pattern iter4 used for `lenny-direct-mode-isolation`/`lenny-drain-readiness`/`lenny-data-residency-validator`/`lenny-t4-node-isolation`.** Concretely:
  - Phase 2 gains a clause: "Build the `pkg/common/registry/resolver.go` `ImageResolver` shared package; Phase 2+ binaries MUST resolve image references through it. Phase 3 digest-pinning and Phase 3.5 chart slices depend on it."
  - Phase 2.5 gains a clause: "Produce the shared `pkg/alerting/rules` and `pkg/recommendations/rules` Go packages; the gateway's in-process evaluators and the Phase 13 deployer-visible `PrometheusRule` / ConfigMap template consume the same package output."
  - Phase 3.5 gains a clause naming `lenny-ops` as a mandatory Helm chart component from this phase onward, with the image built and shipping. The §17.8.5 "mandatory in every Lenny installation" statement is preserved; §18.1's Pre-GA ordering note is reinterpreted as "Phase 17a verifies operability is fully exercised by external deployers," not "`lenny-ops` is first-built at Phase 17a."
  - Phase 13 gains a clause naming `lenny-backup` Jobs as the first phase where backup/restore/verify Jobs are scheduled by `lenny-ops` — `lenny-backup` image first shipped here.
- **Option B — add a new `features.ops` Helm feature flag and phase-aware preflight parallel to BLD-011.** Treat `lenny-ops` as a gated component identical to `lenny-direct-mode-isolation`, with `features.ops=true` flipped at its first-deploy phase. This requires amending §17.8.5 to replace the unconditional "mandatory" language with "mandatory from Phase 3.5 onward (enforced by `features.ops=true` as a non-overridable default from that phase), MUST NOT be disabled." Option B is more defensible because it makes the chart-inventory-parity mechanism uniform across all §17.2 components, but it is a larger spec edit.

Either option must also answer the §17.9 row 501 (`lenny-ops-sa` RBAC preflight check) and §18.1 line 92 "Pre-GA ordering" wording so the three statements — §18 phase assignment, §17.8.5 chart validation, §18.1 Pre-GA ordering — all point at the same phase.

The `admission_webhook_inventory_test.go` companion suite (§17.2 line 71) and/or a new `lenny_ops_deployment_inventory_test.go` must cover the Phase 3.5+ expectation that `lenny-ops` is present, to avoid a chart-author omission shipping silently for `lenny-ops` the way iter3/iter4 caught for the four gated webhooks.

---

### BLD-013. Phase 1 wire-contract artifacts still do not include Shared Adapter Types / SessionEventKind registry [Low]

**Section:** spec/18_build-sequence.md line 8 (Phase 1 wire-contract list); spec/15_external-api-surface.md "Shared Adapter Types" and "SessionEventKind closed enum registry" paragraphs.

Iter4 BLD-010 (Low) asked for Phase 1 to include a normative Go-type artifact (e.g., `pkg/adapter/shared.go`) or JSON Schema pair (`schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json`) codifying the Shared Adapter Types and `SessionEventKind` closed enum. Grepping the current `spec/18_build-sequence.md` confirms neither candidate artifact is named in Phase 1; the wire-contract list remains `schemas/lenny-adapter.proto` + `schemas/lenny-adapter-jsonl.schema.json` + `schemas/outputpart.schema.json` + `schemas/workspaceplan-v1.json`. No iter4 "Status: Fixed" entry exists for BLD-010; this finding is unchanged since iter3.

Re-raising per the iter5 feedback rubric "anchor to prior-iteration severity" at the same Low severity. Not a Phase-infeasibility: Phase 2's adapter binary protocol implementation and Phase 5's `ExternalAdapterRegistry` can build against prose definitions in §15, but the spec-first principle of Phase 1 ("Phase 2 implements against them and the CI build verifies the implementation stays in sync") is violated for the closed-enum contract that §15.2 declares normative for every external adapter.

**Recommendation:** Adopt iter4's BLD-010 recommendation verbatim. Extend Phase 1's "Machine-readable wire-contract artifacts committed to the repository" list with either:

- `pkg/adapter/shared.go` — Go package containing `SessionMetadata`, `AuthorizedRuntime`, `AdapterCapabilities`, `OutboundCapabilitySet`, `SessionEvent`, `PublishedMetadataRef`, and the closed `SessionEventKind` enum; or
- a `schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json` JSON Schema pair with matching Go codegen, paralleling `workspaceplan-v1.json`.

Add the same CI gate as the other Phase 1 schemas: §15 additions mirror code changes (closed-enum additions bump the schema version and require a `SessionEventKind` row + `AdapterCapabilities.SupportedEventKinds` documentation update).

---

### BLD-014. `lenny-preflight` `lenny-ops-sa` RBAC check (§17.9 row 501) has no conditional guard matching BLD-012 [Medium]

**Section:** spec/17_deployment-topology.md §17.9 row 501 (`lenny-ops-sa RBAC` check); spec/18_build-sequence.md Phase 3.5 line 14 (admission-plane enumeration).

Follow-on to BLD-012. Even if BLD-012 Option A is adopted and `lenny-ops` is assigned to Phase 3.5 (first chart install), the `lenny-preflight` row 501 check has no `features.ops` or phase-gating guard in §17.9. Row 501's check is unconditional and reads `lenny-ops-sa` ServiceAccount permissions via `kubectl auth can-i`. That is the correct check *after* `lenny-ops` is first-deployed, but iter4 intentionally gated rows 496 (T4 webhook) and 497 (drain-readiness webhook) on their respective feature flags so pre-first-deploy installs skip cleanly. Row 501 should follow the same pattern if Option B is adopted (gated on `features.ops`), or at minimum cite §17.8.5's "mandatory from Phase 3.5 onward" as the condition under which the unconditional check is correct.

The `admission_webhook_inventory_test.go` four-case parameterisation in §17.2 line 71 likewise does not cover `lenny-ops` presence/absence; it only varies the three webhook flags. A companion suite `tests/integration/core_deployment_inventory_test.go` is needed if BLD-012 Option A lands, or the same suite extended with an `ops` dimension if Option B lands.

**Recommendation:** Align §17.9 row 501 wording with the BLD-012 resolution. If Option A: add a note "(unconditional from Phase 3.5 onward, per §17.8.5 mandatory-`lenny-ops` contract)." If Option B: wrap the check in a `features.ops: true` guard mirroring rows 496/497. In either case, add an integration-test companion parallel to `admission_webhook_inventory_test.go` that enforces the `lenny-ops` Deployment's presence at install and fail-closes on its absence.

Severity Medium because this is a preflight-completeness polish item downstream of BLD-012's structural fix, not an independent infeasibility.

---

### BLD-015. Phase 13 observability milestone does not name the `lenny-ops` bundled-alerting-rules deliverable [Low]

**Section:** spec/18_build-sequence.md Phase 13 line 63 (full observability stack); spec/25_agent-operability.md §25.13 (bundled alerting rules).

Phase 13's deliverable list names "alerting rules, SLO monitoring" but does not name the §25.13 "bundled alerting rules" distribution mechanism by which the `pkg/alerting/rules` output is rendered into deployer manifests (§16.5 line 524: `PrometheusRule` CRD vs ConfigMap, controlled by `monitoring.format`). This is the Section-25-specific responsibility that Phase 13 must satisfy for the operability surface to be complete — `lenny-ops`'s own alerting requires the bundled-rules pipeline to be end-to-end wired, and §25.13 is cited by §17.2 line 524 and §17.9 row 674 as the source of the `PrometheusRule` render.

Info/Low-severity polish: Phase 13 is readable as complete without the explicit §25.13 callout because "alerting rules" is a superset, but §18.1's iter4 summary calls out `docs/alerting/rules.yaml` and the chart build's requirement that "deployer-visible manifests and the in-process compiled rules never diverge" as a Section-25 deliverable — and that deliverable is not phase-anchored anywhere else in §18. Without a phase-anchor, the CI gate that enforces the divergence guard has no phase at which it first becomes blocking.

**Recommendation:** Extend Phase 13's deliverable list with a clause: "Bundled alerting rules pipeline (§25.13): `docs/alerting/rules.yaml` generated from `pkg/alerting/rules`; Helm chart's `PrometheusRule`/ConfigMap template (controlled by `monitoring.format`) consumes the same source; CI gate fails the build if deployer-visible manifests diverge from the in-process compiled rules." This makes the §18.1 line 88 requirement concrete at a specific phase rather than a floating expectation.

---

## Convergence assessment

**Status: Near-convergence, one High finding remaining.**

Iter4's BLD-009 (High) and BLD-011 (High) are both verifiably fixed in the current spec — the Phase 3.5 deferred-items paragraph, the Phase 5.8/8/13 feature-flag flip clauses, and the §17.2 "Feature-gated chart inventory (single source of truth)" paragraph form a consistent three-point fix that the `admission_webhook_inventory_test.go` parameterisation (§17.2 line 71) and §17.9 rows 496/497/498 each honor. No regression has been introduced to either fix path.

Iter4's BLD-010 (Low) is unfixed and is re-raised verbatim as BLD-013 (Low) per the severity-anchoring rule. This is a polish item, not a convergence blocker.

The one remaining High finding, **BLD-012**, is a structural parallel to iter3 BLD-005 and iter4 BLD-011 — an §18.1 iter4 addition that indexed new Section-25 operability artifacts (`lenny-ops`, `lenny-backup`, shared packages, `ImageResolver`) without assigning them to any phase row, combined with §17.8.5's chart-validation contract and §17.9 row 501's unconditional preflight check that together make Phase 3.5 install-infeasible without `lenny-ops` being built. The same feature-flag mechanism that closed BLD-011 (Option B) or a per-phase deliverable clause matching iter4's pattern for the four gated webhooks (Option A) will close BLD-012. BLD-014 (Medium) and BLD-015 (Low) are follow-on polish items conditional on the BLD-012 resolution choice.

No Critical-severity findings. One High (BLD-012) blocks convergence for perspective 19; one Medium (BLD-014) and two Low (BLD-013, BLD-015) are carryover/polish items that should be addressed in the same iter5 fix cycle but do not individually block convergence. Expect convergence in iter6 if BLD-012 is resolved and BLD-013/014/015 are batched into the same fix pass.
