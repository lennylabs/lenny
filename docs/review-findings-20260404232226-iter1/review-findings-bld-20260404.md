# Review Findings: Build Sequence & Implementation Risk

**Category:** BLD — Build Sequence & Implementation Risk
**Perspective:** 19. Build Sequence & Implementation Risk
**Spec:** `docs/technical-design.md` (Section 18)
**Date:** 2026-04-04
**Reviewer:** Claude (Sonnet 4.6)

---

## Summary

The 17-phase build sequence is broadly logical and has several commendable structural choices: the early introduction of observability (Phase 2.5), splitting basic from advanced credential leasing (Phases 5.5 and 11), and inserting a dedicated load-testing phase (Phase 13.5) before production hardening. However, several ordering issues create correctness risks, the echo runtime's coverage boundary is only partly specified, one architectural dependency arrives extremely late (Token/Connector Service in Phase 12a), Phase 17's community-onboarding scope is unrealistic as defined, and there are meaningful parallelization opportunities left on the table.

---

## Findings (ordered by severity)

---

### BLD-001 Token/Connector Service Ships Too Late for Its Declared Role [Critical] — VALIDATED/FIXED

**Section:** 18 (Phase table, Phase 12a note)

The Token/Connector Service is described in Section 4.3 as the **only component with KMS decrypt permissions** for downstream OAuth tokens. The design explicitly states that gateway replicas call the Token Service over mTLS; they cannot directly decrypt stored tokens. The tenant deletion flow (Section 12.8) calls `TokenStore.DeleteByTenant` and `CredentialPoolStore.RevokeByTenant`, both of which depend on the Token Service being operational.

Yet Phase 12a — "Token/Connector service (separate deployment, KMS integration, OAuth flows, credential pools)" — lands after:
- Phase 7: Policy engine (auth, rate limits, quota) — uses `TokenStore` and `UserStateStore`
- Phase 8: Checkpoint/resume
- Phase 9: Full delegation with `lenny/delegate_task`
- Phase 10: MCP fabric with elicitation chain
- Phase 11: Advanced credential leasing (multi-provider rotation)

Phases 5–11 are described as using the echo runtime (Phase 5 note) and basic credential leasing (Phase 5.5). But:
1. Phase 7 (policy engine) includes AuthN/AuthZ evaluation backed by `TokenStore`. If the Token Service is not yet a separate hardened deployment, where does this token storage live during Phases 5.5–11? The spec does not clarify that there is an interim implementation.
2. Phase 11 (advanced credential leasing: rotation, fallback chains, user-scoped credentials via elicitation) explicitly depends on KMS-backed credential pools, STS integrations, and `RotateCredentials` RPC — all described as features of the Token Service. Attempting Phase 11 without the Phase 12a component is incoherent.
3. Phase 12b ("type: mcp" runtime support) and Phase 12c (concurrent execution modes) are listed after Phase 12a but are functionally independent; splitting them as sub-phases of 12 instead of sequential phases suggests the sequencing was not carefully reviewed.

**Recommendation:** Restructure the phase table so that a basic Token/Connector Service (single-replica, no KMS) is introduced no later than Phase 5.5 alongside basic credential leasing — since credential leasing already requires `AssignCredentials` RPC delivery that semantically belongs there. The production-hardened version (KMS integration, multi-replica, OAuth flows) can remain in a later hardening phase (current Phase 12a becomes Phase 12a-hardened). Add an explicit note to Phase 5.5 naming the interim credential store component and its limitations.

**Resolution:** Phase 5.5 in Section 18 was expanded to include a Basic Token Service (single-replica, K8s Secrets backend, no KMS) alongside basic credential leasing. The phase now names the component, its RPCs (`AssignCredentials`/`RevokeCredentials`), states it is the authoritative `TokenStore` owner from Phase 5.5 onward, and documents Phase 7 and Phase 11's dependency on it. Phase 12a was retitled "Token/Connector Service hardening" with explicit "builds on Phase 5.5 Basic Token Service" framing, listing what hardening adds: KMS envelope encryption, multi-replica PDB, OAuth flows, `RotateCredentials` RPC, per-user OAuth token storage.

---

### BLD-002 Policy Engine (Phase 7) Operates Without Auth Infrastructure Validation [High]

**Section:** 18 (Phase 4, Phase 4.5, Phase 5, Phase 7)

Phase 7 introduces the full policy engine including AuthN/AuthZ (`AuthEvaluator`), rate limits, token budgets, and concurrency controls (Section 4.8, Section 11.1). However:

- Phase 4 introduces only "Session manager + session lifecycle + REST API." There is no phase that explicitly installs OIDC/OAuth 2.1 authentication infrastructure. Section 10.2 describes the gateway's authentication model (OIDC, JWT validation, user identity extraction) but no build phase names "auth infrastructure" or "OIDC integration" as a milestone.
- Phase 4.5 (Admin API foundation) includes tenant management and gateway config loading from Postgres — but tenant-scoped authentication and the `UserStateStore` backing `AuthEvaluator` are not mentioned.
- Phase 5 adds `ExternalAdapterRegistry`, Tenant RBAC config API, and `noEnvironmentPolicy` enforcement — but still does not name the OIDC stack delivery.

The practical risk: Phase 7 can be built only once JWT/OIDC validation is wired into the gateway. If auth infrastructure is implicitly bundled into Phase 4 or 5 without being named, there is no explicit milestone to verify it is tested, especially for multi-tenant scenarios where JWT claims carry `tenant_id`. The first integration that actually exercises auth (Phase 5 session creation with RBAC) would be done without a named auth-complete milestone.

**Recommendation:** Add an explicit "Authentication infrastructure" deliverable to Phase 4 or 4.5 covering OIDC/OAuth 2.1 JWT validation, `tenant_id` claim extraction, `UserStateStore` integration, and basic multi-tenant JWT fixtures used in CI. Name this explicitly in the phase milestone column so it is verifiable.

---

### BLD-003 mTLS PKI Required Before Phase 3 But No Phase Installs cert-manager [High]

**Section:** 18 (Phase 3, Phase 3.5); Section 10.3

Section 10.3 specifies cert-manager as the mTLS PKI mechanism for gateway–pod communication. Phase 3.5 (basic security hardening) validates that gVisor is functional and deploys admission policies. The Phase 3 note states "Digest-pinned images from a private registry are required from Phase 3 onward."

However, gateway↔pod communication uses mTLS (the architectural diagram in Section 3 labels this link explicitly). No phase in the table names cert-manager installation, `ClusterIssuer` setup, certificate issuance for gateway replicas, or the mTLS trust bundle distribution to pods. The `lenny-preflight` Job (Section 17.6) checks that cert-manager CRDs and `ClusterIssuer` are Ready — but this check is a pre-install gate, not a build phase deliverable.

Without a named phase for mTLS PKI, the Phase 2 milestone ("Can start an agent session") implicitly depends on this infrastructure existing without the sequence tracking it. Local dev mode relaxes mTLS, which masks the gap during early phases but creates a risk of deferring mTLS testing until late.

**Recommendation:** Add "mTLS PKI setup: cert-manager installation, ClusterIssuer configuration, gateway and pod certificate issuance, trust bundle distribution" as an explicit deliverable in Phase 2 or Phase 3. Confirm in Phase 3.5's milestone text that mTLS is verified end-to-end in integration tests (not just admitted by policy). Phase 14 can then refer to "advanced certificate rotation hardening" building on Phase 3's baseline.

---

### BLD-004 Load Testing Arrives After Full Security Hardening is Impossible to Validate [High]

**Section:** 18 (Phase 13.5, Phase 14); Section 16.5

Phase 13.5 (load testing) is placed before Phase 14 (security hardening), which seems correct at first glance. However, the sequence is:

```
Phase 12c: Concurrent execution modes
Phase 13:  Full observability stack
Phase 13.5: Load testing
Phase 14:  Security hardening (image signing, advanced NetworkPolicy, seccomp tuning, security audit, penetration testing)
```

The problem is that load testing under Phase 13.5 exercises the system without full security controls in place. The security audit and penetration testing in Phase 14 may find issues that require architectural or configuration changes — at which point the load test results from Phase 13.5 are invalid, because the system under test will have changed. Specifically:
- Advanced NetworkPolicy refinement (per-runtime egress rules) in Phase 14 changes pod egress behavior, affecting any load test that exercises external connectivity.
- Seccomp profile tuning may introduce syscall denials that affect pod startup and checkpoint latency, directly invalidating startup latency SLO measurements from Phase 13.5.
- Image signing enforcement via admission controller may add latency to pod scheduling.

The spec's own load testing mandate (Section 16.5) says: "Before GA, load tests must demonstrate that all SLOs below hold at sustained Tier 2 load." GA cannot be declared until after Phase 14, making Phase 13.5 a pre-hardening baseline — but the spec's milestone for Phase 13.5 is "All scaling SLOs validated under load; capacity plan documented," implying final validation, not a baseline.

**Recommendation:** Rename Phase 13.5's milestone to "Pre-hardening load baseline: identify bottlenecks, document capacity ceiling, not final SLO validation." Add a Phase 14.5 (or integrate into Phase 14's milestone) that re-runs load tests after security hardening is complete to produce the final SLO validation required for GA. Alternatively, reorder so Phase 14 precedes Phase 13.5, accepting the sequencing cost of hardening before load testing.

---

### BLD-005 Echo Runtime Cannot Test Key Phase 5–8 Behaviors, Gap Not Quantified [High]

**Section:** 18 (Phase 2, Phase 5 note, Phase 9); Section 17.4 (zero-credential mode)

The spec note between Phase 5 and Phase 5.5 states: "Phase 5 sessions use the zero-credential echo runtime (Phase 2). Phase 5.5 introduces basic credential leasing, enabling real LLM provider testing from Phase 6 onward." The echo runtime also cannot invoke MCP tools (Section 17.4). The delegation-echo runtime (Phase 9) fills part of the gap for delegation testing.

This creates a window (Phases 5–8) where the following behaviors cannot be tested with any available runtime:
1. **Streaming reconnect (Phase 6 milestone: "Full interactive sessions work")** — requires multi-turn state, SSE stream continuity, and response interrupts. The echo runtime produces deterministic single responses and cannot simulate streaming reconnect scenarios. Phase 6's milestone cannot be meaningfully validated without either a real LLM or a streaming-capable mock that simulates mid-stream disconnection.
2. **Policy engine under realistic load (Phase 7)** — token budget enforcement requires per-token usage reporting from the runtime adapter (`ReportUsage` RPC). The echo runtime does not report token usage. The `QuotaEvaluator` cannot be tested end-to-end without it.
3. **Checkpoint/resume (Phase 8)** — checkpoint correctness depends on the adapter executing a real `checkpoint_request`/`checkpoint_ready` handshake (Full-tier) or SIGSTOP/SIGCONT (embedded mode). The echo runtime is Minimum-tier and does not implement the lifecycle channel. The Phase 8 milestone ("Sessions survive pod failure") cannot be validated with the echo runtime alone.

The spec acknowledges the delegation gap and introduces `delegation-echo` in Phase 9, but does not acknowledge or address the streaming, quota, and checkpoint testing gaps for Phases 6–8.

**Recommendation:** Define a `streaming-echo` test runtime (or extend the existing echo runtime) that: (a) reports simulated token usage per message via `ReportUsage`, (b) simulates mid-stream disconnection and reconnect, and (c) implements the Full-tier lifecycle channel (`checkpoint_request`/`checkpoint_ready`). This runtime should ship no later than Phase 5.5 so Phases 6, 7, and 8 can be validated with meaningful CI coverage before real LLM provider integration.

---

### BLD-006 ADR Authoring Has No Dedicated Phase [Medium]

**Section:** 18; Section 19; Section 23.2

Section 19 states: "Full Architecture Decision Records (ADRs) with context, alternatives considered, and consequences will be maintained in `docs/adr/` as separate documents following the MADR format." Section 12.2 notes an explicit "ADR-TBD" requirement for `SandboxClaim` optimistic-locking verification before implementation begins.

No phase in the build sequence includes ADR authoring as a deliverable. Phase 1 introduces the most consequential architectural decisions (CRD types, adapter protocol, billing schema fields), yet the ADRs for these decisions would typically be authored before implementation to lock down alternatives. The spec lists 13 resolved decisions in Section 19 — all of which need ADRs — but places ADR authoring nowhere in the sequence, not even as a sub-bullet of Phase 1 or Phase 2.

For an open-source project where community contributors need to understand why decisions were made, absent ADRs mean institutional knowledge lives only in the original design conversation. The `CONTRIBUTING.md` published in Phase 2 references ADRs for community-proposed scope-threshold changes, but those ADRs do not exist yet at that point.

**Recommendation:** Add ADR authoring to Phase 1's deliverables: "Initial ADR set covering resolved decisions 1–13 (Section 19) in MADR format." Add the specific `ADR-TBD` for SandboxClaim optimistic-locking as a Phase 1 prerequisite gate — it must be verified and documented before Phase 3 (PoolScalingController) is built. The `CONTRIBUTING.md` (Phase 2) should reference the completed ADR index rather than a future one.

---

### BLD-007 Helm Chart and Production Packaging Have No Named Phase [Medium]

**Section:** 18; Section 17.6; Section 17.4

The build sequence has no phase that produces the Helm chart as a named milestone. The chart is described extensively in Section 17.6 — it packages all components, includes preflight validation, CRD management, bootstrap seeding, admission policies, and GitOps support — but no phase in the sequence says "Helm chart ships." Phase 17 mentions "Production-grade docker-compose" and "documentation, community guides" but not the Helm chart.

This matters because:
1. The `lenny-preflight` Job is part of Phase 4.5 (bootstrap seed mechanism reference), but the Job itself runs inside the Helm chart. If the chart does not exist yet, there is no vehicle to deploy the preflight Job.
2. The bootstrap mechanism (`lenny-ctl bootstrap`, Helm init Job) is listed as a Phase 4.5 deliverable but the Helm chart is its packaging vehicle.
3. CRD upgrade procedures (Section 17.6) require the chart to manage CRD versioning — without a named chart phase, there is no milestone to validate CRD upgrade safety.

**Recommendation:** Add "Helm chart: packages all components, CRDs, RBAC, NetworkPolicies, admission policies, preflight Job, bootstrap Job; validated with `helm install` on a clean cluster" as a Phase 4.5 or Phase 5 deliverable. Keep "Production-grade docker-compose" in Phase 17 for community quickstart purposes (it serves a different audience). The Helm chart is infrastructure for any real deployment and should gate Phase 5 (first external API access).

---

### BLD-008 Compliance Validation Phase Is Absent [Medium]

**Section:** 18; Section 12.7–12.9; Section 11.7

The spec contains substantial compliance infrastructure: GDPR erasure flows (Section 12.8), data residency enforcement (Section 12.8), data classification tiers (Section 12.9), audit log integrity with hash chaining (Section 11.7), billing event immutability, and legal hold support. These are enterprise table-stakes features that require explicit validation — not just implementation.

The build sequence has Phase 14 (security hardening including "security audit and penetration testing") but no phase for compliance validation: GDPR erasure end-to-end verification, data residency enforcement tests, audit log chain integrity verification, or SOC 2 / HIPAA readiness assessment. Unlike security testing which is inherently technical, compliance validation often requires a compliance officer or external auditor review and cannot be folded into a technical security audit.

**Recommendation:** Add a Phase 14.5 (or sub-phase): "Compliance validation: GDPR erasure end-to-end tests across all stores, data residency enforcement tests, audit log chain integrity CI test, billing event immutability verification, data classification coverage audit." Include the note that SOC 2 / HIPAA self-assessment is the operator's responsibility but the spec should enumerate the controls that support it, referencing the relevant sections. This phase gates any "enterprise-ready" claim in marketing materials.

---

### BLD-009 Phase 17 Community Onboarding Milestone Is Underspecified and Conflated [Medium]

**Section:** 18 (Phase 17); Section 23.2

Phase 17's milestone — "Full community onboarding" — is a single phase that bundles:
1. `MemoryStore` + platform memory tools (new feature)
2. Semantic caching (new feature)
3. Guardrail interceptor hooks (new hook infrastructure)
4. Eval hooks (new hook infrastructure)
5. Production-grade docker-compose (infrastructure)
6. Documentation and community guides (content)

This conflation has two problems:
- Items 1–4 are **features**, not community infrastructure. Shipping new features in the "community onboarding" phase means the system is still gaining functionality when it should be stabilizing for external contributors. A contributor who arrives during Phase 17 gets a moving target.
- Items 5–6 are **the actual community onboarding work** and deserve their own milestone so they are not de-prioritized against feature delivery.

Section 23.2 sets the TTHW target at < 5 minutes (Phase 2) and `CONTRIBUTING.md` at Phase 2, which is appropriate — but the advanced community infrastructure (guides, comparison docs, example runtimes) is deferred to Phase 17. Given that Phase 17 is the last phase before GA, any slip in MemoryStore or semantic caching directly delays documentation publishing.

**Recommendation:** Split Phase 17 into:
- Phase 16.5: Community infrastructure — production-grade docker-compose, runtime author guides, operator playbooks, comparison guides, example runtimes beyond echo. Milestone: "External contributor can build and deploy a custom runtime without reading the design doc."
- Phase 17: Feature extensions — MemoryStore, semantic caching, guardrail hooks, eval hooks. Milestone: "Advanced platform hooks available for ecosystem integrators."

This decouples community readiness from feature completeness, allowing the project to accept outside contributions one phase earlier.

---

### BLD-010 Phases 12a, 12b, and 12c Are Independent and Should Be Parallelized [Medium]

**Section:** 18 (Phase 12a–12c)

The three sub-phases of Phase 12 have no stated dependency between them:
- **12a**: Token/Connector Service (separate deployment, KMS, OAuth flows, credential pools)
- **12b**: `type: mcp` runtime support (runtime endpoints, lifecycle, discovery)
- **12c**: Concurrent execution modes (`slotId` multiplexing)

Phase 12b depends on the session manager and runtime adapter (Phases 4–6), not on Phase 12a (Token Service) — MCP runtimes have no dependency on KMS-backed credential pools for basic operation. Phase 12c depends on the execution mode infrastructure (Phases 5–6) and the `slotId` field introduced in Phase 2, not on Phase 12a or 12b.

Presenting them sequentially as 12a → 12b → 12c implies a team works on them serially, adding unnecessary calendar time to the critical path between Phase 11 and Phase 13.

**Recommendation:** Restructure as parallel workstreams: "Phase 12 (parallel): 12a — Token/Connector Service hardening; 12b — MCP runtime type; 12c — Concurrent execution modes. Can proceed in parallel after Phase 11 completes. Merge milestone: all three complete." Add explicit dependency notes: 12b depends on Phase 5 (gateway ExternalAdapterRegistry), 12c depends on Phase 6 (interactive session model). This recovers potential calendar parallelism during what is likely the longest phase block.

---

### BLD-011 Parallelization of Phases 13 and 13.5 Not Mentioned [Low]

**Section:** 18 (Phase 13, Phase 13.5)

Phase 13 (full observability stack: audit logging, OTel metrics, distributed tracing, dashboards, alerting, SLO monitoring) and Phase 13.5 (load testing and capacity planning) are listed sequentially, but they have a partial dependency only. Phase 13.5 requires sufficient metrics to measure, but the startup benchmark harness (Phase 2) and per-phase measurement instrumentation are already in place. A load test can proceed using the Phase 2 benchmarks and partial observability while the Phase 13 dashboards and alerting rules are being authored.

Sequencing Phase 13 strictly before Phase 13.5 means dashboard and alerting work sits on the critical path to load testing, which adds unnecessary delay.

**Recommendation:** Note that Phase 13 and Phase 13.5 can be partially parallelized: the load test script development and initial runs can begin once Phase 12c is complete, while Phase 13 dashboard and alerting authoring continues in parallel. Phase 13.5's final results (capacity plan document) are gated on Phase 13 dashboards being available for visual validation, not on their prior completion. Add this note to the phase table.

---

### BLD-012 Phase 16 (Experiments) Has No Testing Infrastructure Defined [Low]

**Section:** 18 (Phase 16); Section 10.7

Phase 16 introduces "Experiment primitives, PoolScalingController experiment integration" with the milestone "A/B testing infrastructure." Section 10.7 describes experiment context propagation through delegation leases, health-based rollback triggers, and variant pool sizing — a non-trivial surface area.

No test runtime or fixture is named for exercising experiment variant routing. The echo runtime cannot produce variant-specific outcomes. Unlike delegation testing (which got `delegation-echo` in Phase 9), experiments have no equivalent test fixture named in the sequence.

**Recommendation:** Add a note to Phase 16 requiring a `variant-echo` test runtime (or extension of `delegation-echo`) that returns responses tagged with the experiment variant, enabling CI tests to assert correct variant routing, budget accounting, and rollback trigger behavior. This can be a lightweight extension of an existing test runtime rather than a new binary.

---

### BLD-013 Critical Path Is Not Identified in the Spec [Low]

**Section:** 18 (Phase table)

The build sequence table lists phases linearly but does not identify the critical path — the sequence of phases with the least scheduling slack that determines the minimum project duration. For a project of this scope, the absence of a critical path analysis means resource allocation decisions (which phases to staff first, which can be deferred without affecting GA) are made ad hoc.

Based on this review, the likely critical path is:
Phase 1 → Phase 2 → Phase 2.5 → Phase 3 → Phase 3.5 → Phase 4 → Phase 4.5 → Phase 5 → Phase 5.5 → Phase 6 → Phase 7 → Phase 8 → Phase 9 → Phase 10 → Phase 11 → Phase 12a → Phase 13 → Phase 13.5 → Phase 14 → Phase 15

Phases 12b, 12c, and 16 are off the critical path and could be resourced separately without affecting GA.

**Recommendation:** Add a critical path note after the phase table identifying which phases are on the critical path versus which can be parallelized or deferred. This is a one-paragraph addition that has significant value for project planning and resource allocation conversations with contributors.

---

## Summary Table

| ID | Title | Severity |
|----|-------|----------|
| BLD-001 | Token/Connector Service ships too late for its declared role | Critical — VALIDATED/FIXED |
| BLD-002 | Policy engine (Phase 7) operates without auth infrastructure validation | High |
| BLD-003 | mTLS PKI required before Phase 3 but no phase installs cert-manager | High |
| BLD-004 | Load testing arrives after full security hardening is impossible to validate | High |
| BLD-005 | Echo runtime cannot test key Phase 5–8 behaviors, gap not quantified | High |
| BLD-006 | ADR authoring has no dedicated phase | Medium |
| BLD-007 | Helm chart and production packaging have no named phase | Medium |
| BLD-008 | Compliance validation phase is absent | Medium |
| BLD-009 | Phase 17 community onboarding milestone is underspecified and conflated | Medium |
| BLD-010 | Phases 12a, 12b, and 12c are independent and should be parallelized | Medium |
| BLD-011 | Parallelization of Phases 13 and 13.5 not mentioned | Low |
| BLD-012 | Phase 16 (Experiments) has no testing infrastructure defined | Low |
| BLD-013 | Critical path is not identified in the spec | Low |
