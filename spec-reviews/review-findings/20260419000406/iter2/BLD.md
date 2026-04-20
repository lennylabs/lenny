# Build Sequence & Implementation Risk Review — Iteration 2

Prior findings verified as fixed:

- **BLD-001 (iter1)**: Phase 4.5 now correctly names Phase 5.75's `AuthEvaluator` as the dependent of the auth-complete milestone (`18_build-sequence.md` line 23).
- **BLD-002 (iter1)**: Phase 5.75 now carries an explicit `**Prerequisite:** Phase 4.5 (authentication infrastructure)` line (`18_build-sequence.md` line 40).
- **BLD-003 (iter1)**: The test-runtime-sufficiency note after Phase 3.5 explicitly covers Phases 3, 3.5, 4, 4.5, 5, 5.4, and 5.5 against the basic echo runtime, and calls out `streaming-echo` / `delegation-echo` scope (`18_build-sequence.md` line 18).
- **CPS-001 (iter1)**: License decision is uniformly shown as `Resolved — MIT (ADR-008)` across §18 Phase 0, §19 row 14, §23.2 Community Adoption Strategy, and §23.1 Feature Comparison Matrix. No stale "pending" markers remain anywhere in the spec.

Per-category re-checks:

- **Phase ordering — credential leasing:** 5.4 (etcd encryption) → 5.5 (Basic Token Service + credential leasing, multi-replica HA) → 5.6 (targeted design review) → 5.75 (auth + quota gate) → 5.8 (LLM proxy) → 6 (real-LLM interactive). The gates before any real credential is written to the cluster and before any real-credential session is exercised are explicit and well-placed.
- **Missing phases — security/load/compliance:** Phases 5.6 and 9.1 cover targeted security design reviews; Phase 14 covers comprehensive audit + pentest; Phase 6.5 / 9.5 / 11.5 cover incremental load baselines; Phase 13.5 covers pre-hardening full-system load; Phase 14.5 covers post-hardening SLO re-validation; Phase 16.5 covers experiment-workload SLO re-validation; Phase 13 covers compliance profile enforcement (SOC2/HIPAA/FedRAMP/NIS2/DORA retention presets) and durable audit. Nothing material is missing.
- **Echo runtime sufficiency:** basic echo, `streaming-echo` (Phase 2.8), and `delegation-echo` (Phase 9) collectively cover all CI validation paths through Phase 13.5 without real LLM credentials. The Phase 2.8 note making `streaming-echo` CI validation *mandatory* for Phase 6/7/8 milestones closes the remaining gap.
- **Community onboarding realism:** Phase 0 commits LICENSE and makes the repo publicly visible; Phase 2 publishes `CONTRIBUTING.md` / `make run` / TTHW < 5 min with a pre-release "no unsolicited PRs" notice; Phase 17a finalizes `GOVERNANCE.md`, runbooks, and comparison guides before solicitation. The sequencing is internally consistent with §23.2.
- **Parallelizable phases:** 12a / 12b / 12c explicitly called out as parallel with documented independence; 17a / 17b flagged as concurrent-eligible. Incremental-baseline phases (6.5, 9.5, 11.5) are appropriately serialized behind the paths they measure.

---

## BLD-004 Phase 0 framing is stale now that license is resolved [LOW]

**Files:** `spec/18_build-sequence.md` line 7

Phase 0's header reads "**Pre-implementation gating decisions** (must be complete before Phase 1 begins)" and lists two items: (1) ADR-008 license selection, now *Resolved — MIT*, and (2) ADR-007 `SandboxClaim` optimistic-locking verification, which the row and the Phase 1 prerequisite line still describe as work that "must be complete before Phase 1 implementation begins".

Item (1) is no longer a decision — it is a committed artifact (LICENSE file + ADR-008). Phase 1's own prerequisite line acknowledges this with "— this gate is satisfied." The effect is that Phase 0 is currently a single-item gate (ADR-007 verification) that is mislabelled as a multi-item "gating decisions" block. The inline `*Resolved — MIT.*` marker reads as past-tense status embedded in a future-tense phase row, which is awkward even though each individual sentence is correct.

This does not affect sequencing correctness, only row-level coherence and clarity for readers scanning the build table.

**Recommendation:** One of:

- Rename the Phase 0 header to "Pre-implementation artifacts and gates" (or similar) so it is accurate whether items are decisions-to-make or committed artifacts.
- Or, since ADR-008 is fully resolved and the LICENSE is committed, reduce Phase 0 to a single item — the ADR-007 verification gate — and fold the "license committed / repo publicly visible" statement into a preamble note above Phase 0 (e.g., `> **Note:** ADR-008 (license, MIT) and the `LICENSE` file were resolved and committed before the build sequence began; the only remaining Phase 0 gate is ADR-007.`).

Either keeps the row coherent without a mixed decisions-vs-artifacts framing.

---

No other real issues found. All section cross-references are valid, phase numbering is consistent, credential-leasing prerequisites are in the correct order, security and load test phases are present, echo runtime coverage is sufficient, and community launch sequencing is internally consistent.
