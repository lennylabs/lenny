# Perspective 19: Build Sequence & Implementation Risk — Iter6

**Scope:** `spec/18_build-sequence.md` with cross-references to `spec/17_deployment-topology.md` §17.2 / §17.8.5 / §17.9 for dependency verification; [`spec/15_external-api-surface.md`](../../../spec/15_external-api-surface.md) Shared Adapter Types for BLD-013 carry-over; [`spec/25_agent-operability.md`](../../../spec/25_agent-operability.md) §25.13 for BLD-015 carry-over.

---

## Iter5 carry-forward verification

### BLD-012 (iter5 High). `lenny-ops` phase assignment. **FIXED.**

Iter5's recommendation was Option A (per-phase deliverable clauses matching the iter4 BLD-011 pattern for gated webhooks). The iter5 fix adopted Option A with the following observable changes:

- **§18.1 restructured.** Lines 75–99 now contain (a) the original artifact index (images, OpenAPI/MCP generation, alerting-rule artifacts, release-channel signing), (b) a new **Phase assignments** subsection at line 92 that explicitly anchors each §18.1 artifact to a specific phase, and (c) a slimmed **Pre-GA ordering** subsection at line 99 that is no longer the only phase-ordering statement. The four assignments are:
  - `pkg/common/registry/resolver.go` `ImageResolver` → Phase 2 (line 94).
  - `pkg/alerting/rules` and `pkg/recommendations/rules` shared Go packages → Phase 2.5 (line 95).
  - `lenny-ops` container image → Phase 3.5 (line 96), with the chart-validation contract (§17.8.5) and unconditional preflight check (§17.9 `lenny-ops-sa` RBAC) cited as the mechanism that makes `lenny-ops` mandatory from this phase without a `features.ops` flag.
  - `lenny-backup` container image → Phase 13 (line 97), deferred until `lenny-ops` schedules its first backup Jobs against the `lenny-backup-sa` ServiceAccount provisioned at Phase 3.5.
- **Phase 2 (line 10) gains an `ImageResolver` clause.** "**Shared `ImageResolver` package** (`pkg/common/registry/resolver.go`, §18.1, §17.8.6): every Lenny binary from Phase 2 onward resolves image references through `ImageResolver` so `platform.registry.*` … compose deterministically into the gateway, controller, token-service, `lenny-ops`, `lenny-backup`, and warm-pool controller image references. Phase 3's digest-pinning requirement and Phase 3.5's first chart install both depend on `ImageResolver` being present; a CI unit-test suite covers resolution precedence (override > url > default) and digest enforcement." This matches the iter5 recommendation's Option A bullet for Phase 2 verbatim in intent.
- **Phase 2.5 (line 11) gains a shared-rule-packages clause.** "**Shared rule packages** (`pkg/alerting/rules`, `pkg/recommendations/rules`, §18.1, §16.5, §25.13): produce the shared Go packages that later phases consume. `pkg/alerting/rules` is the single source for the bundled alert catalog — the gateway's in-process evaluator, the Phase 3.5 `AdmissionWebhookUnavailable` / `SandboxClaimGuardUnavailable` alerts, and the Phase 13 deployer-visible `PrometheusRule` / ConfigMap template all compile against it …; `pkg/recommendations/rules` is consumed by the Phase 4.5 admin-API `/v1/admin/ops/recommendations` endpoint and by `lenny-ops`'s aggregated view …".
- **Phase 3.5 (line 14) gains the mandatory `lenny-ops` first-deploy paragraph.** The paragraph explicitly names the Phase 3.5 chart slice rendering the full canonical `lenny-ops` layout from §17.2 lines 15–19 (Deployment, headless `lenny-gateway-pods` Service, `lenny-backup-sa` ServiceAccount with backup image not yet shipping, four NetworkPolicies, and the `lenny-ops` PodDisruptionBudget), and explicitly calls out the reduced-responsibilities posture until Phase 4.5 admin-API fan-out is available. This addresses iter5 BLD-012 consequence #3 (admin-API load-bearing at 4.5) by naming it as a recognized trajectory rather than a bug.
- **Phase 13 (line 63) gains the `lenny-backup` first-ship clause.** "**`lenny-backup` Jobs first ship** (§25.11, §18.1): Phase 13 is the first phase where transient backup / restore / verify Jobs are scheduled by `lenny-ops` against the `lenny-backup-sa` ServiceAccount provisioned at Phase 3.5. The `lenny-backup` container image is built and shipping from this phase onward …".

Consistency with §17.8.5 mandatory-component contract: **consistent.** §17.8.5 line 1302 still reads "mandatory in every Lenny installation, regardless of tier. There is no supported topology without it. … Attempts to disable `lenny-ops` via Helm values are rejected at chart validation." §18 Phase 3.5 and §18.1 Phase assignments both explicitly cite §17.8.5 and §17.9's unconditional preflight check as the reason there is no `features.ops` flag. No contradiction remains.

Consistency of "Pre-GA ordering" language with phase assignments: **consistent.** §18.1 line 99 now reads "Phase 17a (community-launch documentation pass) and Phase 14 (image signing) each impose additional gates on the §18.1 artifacts: (a) operability must be complete — all of the above first-ship phases cleared and the §25 surface exercised against external deployers — before Phase 17a invites external contributors; and (b) Phase 14 image signing is a prerequisite for `lenny-ops`'s upgrade-check signature verification …". Pre-GA is a gate layered on top of the already-assigned phases; it is no longer the sole phase-ordering statement.

No regression has been introduced to BLD-009 or BLD-011 (iter4) by this fix.

### BLD-014 (iter5 Medium). `lenny-ops-sa` RBAC preflight-check traceability + core-Deployment test-suite naming. **FIXED.**

Iter5's recommendation (Option A path) requested two things:

1. Align the `lenny-ops-sa` RBAC preflight check wording with the §17.8.5 mandatory-component contract (add an "unconditional from Phase 3.5 onward, per §17.8.5 mandatory-`lenny-ops` contract" annotation).
2. Add an integration-test companion parallel to `admission_webhook_inventory_test.go` that enforces the `lenny-ops` Deployment's presence.

Both are delivered:

- **§17.9 row 503 body** (file line 503) now reads: "Verify that the `lenny-ops-sa` ServiceAccount has the RBAC permissions documented in §25.4. Uses `kubectl auth can-i` against each rule in the canonical RBAC table (Lease coordination, Deployment patches, CRD reads, ConfigMap reads, Secret reads for backup credentials, Job create/watch). **Unconditional from Phase 3.5 onward, per the §17.8.5 mandatory-`lenny-ops` contract — there is no `features.ops` flag because chart validation rejects any attempt to disable `lenny-ops`, so this check fires on every preflight run.**" The bold clause is the new traceability annotation and matches the iter5 recommendation verbatim.
- **`tests/integration/core_deployment_inventory_test.go` is named** in §17.2 line 71's inventory-tests paragraph: "A parallel suite (`tests/integration/core_deployment_inventory_test.go`) covers the unconditional core-Deployment inventory — the `lenny-ops` Deployment and its supporting resources from §17.8.5 (headless `lenny-gateway-pods` Service, `lenny-ops-leader` Lease, `lenny-backup-sa` ServiceAccount, the four NetworkPolicies, and the `lenny-ops` PodDisruptionBudget) — and fail-closes on absence so a chart-author omission of a mandatory core deployment cannot ship silently the way iter3/iter4 caught for gated webhooks; this suite requires no feature-flag parameterisation because `lenny-ops` is present in every phase from 3.5 onward." This mirrors the iter5 recommendation's "parallel to `admission_webhook_inventory_test.go`" language.
- **Phase 3.5 deliverable list** (line 14) also names the `core_deployment_inventory_test.go` suite inside the "Mandatory `lenny-ops` first-deploy" paragraph: "A `core_deployment_inventory_test.go` integration suite parallel to `admission_webhook_inventory_test.go` verifies the `lenny-ops` Deployment and its supporting resources are present after `helm install`; the suite fail-closes on absence so a chart-author omission cannot ship silently."

Test-suite naming mirrored across §17.2 and §18: **consistent.** Both sections name `core_deployment_inventory_test.go` as a sibling suite of `admission_webhook_inventory_test.go`. No drift.

### BLD-013 (iter5 Low, carry-over). Phase 1 wire-contract list still does not include Shared Adapter Types / SessionEventKind registry. **NOT fixed in iter5.**

`spec/18_build-sequence.md` Phase 1 (line 8) wire-contract list is unchanged from iter5: it still contains `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, `schemas/outputpart.schema.json`, `schemas/workspaceplan-v1.json`. Neither `pkg/adapter/shared.go` nor `schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json` is named. Grepping `pkg/adapter/shared|session-event-v1|adapter-shared-v1` against the build-sequence file returns zero matches.

§15 continues to define these types normatively (§15.2 line 83 `SupportedEventKinds []SessionEventKind`, line 178 "Shared Adapter Types" section, lines 315–323 closed `SessionEventKind` const block) and §18 Phase 1's own preamble declares "spec-first artifacts; Phase 2 implements against them and the CI build verifies the implementation stays in sync" — yet the closed-enum and shared-type artifacts are not among the committed artifacts.

No iter5 "Fixed" entry was produced for BLD-013. The iter5 fix commit `c941492` focused on BLD-012 / BLD-014 (Critical/High/Medium findings only); BLD-013 (Low) was carried forward per the iter5 convergence note. Re-raised at the same Low severity below.

### BLD-015 (iter5 Low, carry-over). Phase 13 observability milestone does not name the §25.13 bundled-alerting-rules deliverable. **NOT fixed in iter5.**

Phase 13 (line 63) now explicitly names `lenny-backup` (the §25.11 deliverable) but still does not name the §25.13 bundled alerting rules pipeline as a Phase 13 deliverable — the Phase 13 "alerting rules, SLO monitoring" bullet still refers to the superset without naming:
- `docs/alerting/rules.yaml` generation from `pkg/alerting/rules`,
- the Helm chart's `PrometheusRule`/ConfigMap template consuming the same source, and
- the CI gate failing the build if deployer-visible manifests diverge from the in-process compiled rules.

§18.1 line 88 still describes this pipeline but does not anchor it at a phase: "`docs/alerting/rules.yaml` is generated from the shared `pkg/alerting/rules` Go package and committed on each release. The Helm chart's `PrometheusRule` / ConfigMap templates consume the same package output; the chart build must embed or reference the generated rules so deployer-visible manifests and the in-process compiled rules never diverge." The divergence guard is stated as a floating expectation without a phase at which it first becomes blocking. Re-raised at Low severity below.

---

## Iter6 findings

### BLD-016. Phase 1 wire-contract artifacts still do not include Shared Adapter Types / SessionEventKind registry [Low, carry-over of BLD-010 iter4 / BLD-013 iter5]

**Section:** [`spec/18_build-sequence.md`](../../../spec/18_build-sequence.md) line 8 (Phase 1 wire-contract list); [`spec/15_external-api-surface.md`](../../../spec/15_external-api-surface.md) §15.2 lines 83, 178, 315–323 (Shared Adapter Types + closed `SessionEventKind` enum).

Iter5 BLD-013 (Low) asked for Phase 1 to include a normative Go-type artifact (e.g., `pkg/adapter/shared.go`) or a JSON Schema pair (`schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json`) codifying the Shared Adapter Types and the closed `SessionEventKind` enum. The iter5 fix did not address this (BLD-013 was Low and carried forward rather than batched into the iter5 fix pass). Re-raising at the same Low severity per the iter5 feedback rubric "anchor to prior-iteration severity".

Not a phase-infeasibility: Phase 2's adapter binary protocol implementation and Phase 5's `ExternalAdapterRegistry` can build against prose definitions in §15. However, the spec-first principle of Phase 1 — "these are spec-first artifacts; Phase 2 implements against them and the CI build verifies the implementation stays in sync" — is violated for the closed-enum contract that §15.2 declares normative for every external adapter. The cost of the gap is that closed-enum additions after Phase 2 can diverge between the §15 prose, adapter implementations, and the runtime registry without CI visibility, and the first signal is likely to arrive as an integration-test break rather than a commit-time schema conflict.

**Recommendation:** Adopt iter4 BLD-010's / iter5 BLD-013's recommendation verbatim. Extend Phase 1's "Machine-readable wire-contract artifacts committed to the repository" list with either:

- `pkg/adapter/shared.go` — Go package containing `SessionMetadata`, `AuthorizedRuntime`, `AdapterCapabilities`, `OutboundCapabilitySet`, `SessionEvent`, `PublishedMetadataRef`, and the closed `SessionEventKind` enum; **or**
- a `schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json` JSON Schema pair with matching Go codegen, paralleling `workspaceplan-v1.json`.

Add the same CI gate as the other Phase 1 schemas: §15 additions mirror code changes (closed-enum additions bump the schema version and require a `SessionEventKind` row + `AdapterCapabilities.SupportedEventKinds` documentation update).

---

### BLD-017. Phase 13 observability milestone does not name the §25.13 bundled-alerting-rules deliverable [Low, carry-over of BLD-015 iter5]

**Section:** [`spec/18_build-sequence.md`](../../../spec/18_build-sequence.md) Phase 13 line 63 (full observability stack); §18.1 line 88 (Alerting-rule artifacts); [`spec/25_agent-operability.md`](../../../spec/25_agent-operability.md) §25.13 (bundled alerting rules).

Phase 13's deliverable list names "alerting rules, SLO monitoring" generically and the iter5 fix extended Phase 13 with the `lenny-backup` Jobs-first-ship clause (for §25.11), but the §25.13 bundled-alerting-rules distribution mechanism by which the `pkg/alerting/rules` output is rendered into deployer manifests (§16.5 line 524: `PrometheusRule` CRD vs ConfigMap, controlled by `monitoring.format`) is not named in Phase 13. §18.1 line 88 describes the pipeline (`docs/alerting/rules.yaml` generation + Helm chart `PrometheusRule`/ConfigMap rendering + non-divergence guarantee) but without a phase anchor, the CI gate that enforces the divergence guard has no phase at which it first becomes blocking.

Phase 2.5's shared-rule-packages clause produces the package itself; Phase 3.5's admission-plane alerts (`AdmissionWebhookUnavailable`, `SandboxClaimGuardUnavailable`) begin consuming it in-process; the deployer-visible `PrometheusRule`/ConfigMap render is the Phase 13 GA-readiness deliverable. Without naming the render pipeline at Phase 13, the build sequence is silent on when the chart-rendered manifests become the canonical deployer artifact.

**Recommendation:** Extend Phase 13's deliverable list with an explicit clause: "Bundled alerting rules pipeline (§25.13): `docs/alerting/rules.yaml` generated from `pkg/alerting/rules`; Helm chart's `PrometheusRule`/ConfigMap template (controlled by `monitoring.format`) consumes the same source; CI gate fails the build if deployer-visible manifests diverge from the in-process compiled rules." This makes the §18.1 line 88 requirement concrete at a specific phase rather than a floating expectation, and completes the Phase 2.5 → Phase 3.5 → Phase 13 chain for the shared alerting-rules package.

---

### BLD-018. §18 Phase 3.5 and §18.1 Phase assignments cite "§17.9 row 501" but the `lenny-ops-sa` RBAC check is now at row 503 [Low, new]

**Section:** [`spec/18_build-sequence.md`](../../../spec/18_build-sequence.md) Phase 3.5 line 14 (Mandatory `lenny-ops` first-deploy paragraph) and §18.1 line 96 (Phase assignments — `lenny-ops` container image); [`spec/17_deployment-topology.md`](../../../spec/17_deployment-topology.md) §17.9 row 503 (actual `lenny-ops-sa` RBAC check).

The iter5 BLD-014 fix inserted new preflight-check rows above the `lenny-ops-sa` RBAC check — in particular `StorageRouter region coverage` (line 489), `Legal-hold escrow per-region coverage` (line 490), and other NetworkPolicy parity checks (lines 493–495, 506–509). These additions shifted the `lenny-ops-sa` RBAC row from its previous line position (called "row 501" in the iter5 review-finding prose) down to line 503. §17.9 row 501 is now "SIEM endpoint (warning)".

However, two other spec locations still reference "§17.9 row 501" when they mean the `lenny-ops-sa` RBAC check:

1. **§18 Phase 3.5 Mandatory `lenny-ops` first-deploy paragraph** (line 14): "there is no `features.ops` flag — §17.8.5 rejects any attempt to disable it, and **§17.9 row 501's unconditional `lenny-ops-sa` RBAC preflight check** is unconditional precisely because `lenny-ops` is mandatory from this phase".
2. **§18.1 Phase assignments — `lenny-ops` container image** (line 96): "§17.8.5 rejects any attempt to disable `lenny-ops` at chart validation, and **§17.9 row 501's `lenny-ops-sa` RBAC preflight check** is unconditional precisely because `lenny-ops` is mandatory from this phase onward".

Both references are correct in substance but cite a stale row number. A reader following the cross-reference to §17.9 row 501 will land on the SIEM-endpoint warning rather than the `lenny-ops-sa` RBAC check. This is a docs-sync lapse introduced by iter5's fix inserting rows above 501 in §17.9.

Severity Low because the referent is unambiguous from the surrounding prose (both mentions inline-name the `lenny-ops-sa` RBAC preflight check), and the §17.8.5 cross-reference is correct. A reader who follows the row-501 pointer will still be able to recover the intended check by scanning the table. Still, the cross-reference hygiene that BLD-014 itself was asking for is weakened by this staleness.

**Recommendation:** Update both sites in `spec/18_build-sequence.md` to cite row 503 (or, more robustly, cite the row by its table key "`lenny-ops-sa` RBAC" rather than by numeric position, so future §17.9 additions do not require cross-file edits):

- Phase 3.5 paragraph: "§17.8.5 rejects any attempt to disable it, and §17.9's **`lenny-ops-sa` RBAC** preflight check is unconditional precisely because `lenny-ops` is mandatory from this phase".
- §18.1 line 96: "§17.8.5 rejects any attempt to disable `lenny-ops` at chart validation, and §17.9's **`lenny-ops-sa` RBAC** preflight check is unconditional precisely because `lenny-ops` is mandatory from this phase onward".

Prefer referring to §17.9 check rows by their human-readable table key rather than line/row number throughout §18, mirroring how §17.9 itself refers to other rows by name in its error-message column.

---

## Convergence assessment

**Status: Near-convergence — no High or Medium findings open. Three Low findings (two carry-over + one new docs-sync slip) remain.**

Iter5's one High finding (BLD-012) and one Medium finding (BLD-014) are both verifiably fixed in the current spec. The fix used Option A (per-phase deliverable clauses mirroring the iter4 BLD-011 gated-webhook pattern) and the result is internally consistent across §18 (Phase assignments in §18.1 + per-phase deliverable clauses at Phases 2, 2.5, 3.5, and 13), §17.8.5 (unchanged mandatory-component contract), §17.9 row 503 (unconditional-check traceability annotation to §17.8.5), and §17.2 line 71 (`core_deployment_inventory_test.go` named as a parallel suite to `admission_webhook_inventory_test.go`). No regression to BLD-009 or BLD-011 (iter4) has been introduced.

The three remaining findings — BLD-016 (Low, carry-over of BLD-013/BLD-010), BLD-017 (Low, carry-over of BLD-015), and BLD-018 (Low, new docs-sync slip from iter5 fix) — are all polish-level items and do not individually block convergence. BLD-016 and BLD-017 are two-iteration carry-overs at this point; per the iter5 feedback rubric "anchor to prior-iteration severity" they remain Low. BLD-018 is a new-iteration finding caused by the iter5 BLD-014 fix inserting preflight rows above the `lenny-ops-sa` RBAC check; severity Low matches the prior-iteration rubric for cross-reference-staleness issues (no runtime or install-time impact, unambiguous referent in context).

No Critical, High, or Medium severity findings. The perspective 19 review has **converged** under the iter5 severity-anchoring rule — all open items are Low-severity polish that the iter5 fix cycle explicitly accepted as non-blocking. If the iter6 fix cycle batches BLD-016/017/018 together it would close the perspective fully; skipping them to iter7 is also acceptable given their Low severity and non-blocking nature.

**Per-finding summary:**

| Finding | Severity | Status | Carry-forward lineage |
|---|---|---|---|
| BLD-009 (iter4) | High | Fixed iter4, verified iter5, verified iter6 | — |
| BLD-010 (iter4) | Low | Carry-over → BLD-013 (iter5) → BLD-016 (iter6) | Unfixed across three iterations |
| BLD-011 (iter4) | High | Fixed iter4, verified iter5, verified iter6 | — |
| BLD-012 (iter5) | High | Fixed iter5 | Closed |
| BLD-013 (iter5) | Low | Carry-over → BLD-016 (iter6) | Continues iter4 BLD-010 |
| BLD-014 (iter5) | Medium | Fixed iter5 | Closed |
| BLD-015 (iter5) | Low | Carry-over → BLD-017 (iter6) | Still unfixed |
| **BLD-016 (iter6)** | Low | **New (carry-over of BLD-010 / BLD-013)** | — |
| **BLD-017 (iter6)** | Low | **New (carry-over of BLD-015)** | — |
| **BLD-018 (iter6)** | Low | **New (docs-sync slip from iter5 fix)** | — |

Convergence: **Yes.** The perspective is converged under the iter5 severity-anchoring rule — no Critical/High/Medium findings remain, and all Low-severity carry-overs are non-blocking polish.
