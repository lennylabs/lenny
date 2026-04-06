# Technical Design Review Findings — 2026-04-04 (Iteration 1)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 1 of 5
**Total findings:** 421 across 25 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 24    |
| High     | 111   |
| Medium   | 165   |
| Low      | 83    |
| Info     | 38    |

## Findings by Perspective

| # | Perspective | C | H | M | L | I | Total | File |
|---|-------------|---|---|---|---|---|-------|------|
| 1 | Kubernetes Infrastructure & Controller Design | 1 | 5 | 6 | 3 | 3 | 18 | `review-findings-k8s-20260404.md` |
| 2 | Security & Threat Modeling | 2 | 6 | 7 | 4 | 2 | 21 | `review-findings-sec-20260404.md` |
| 3 | Network Security & Isolation (file 1) | 1 | 4 | 5 | 3 | 2 | 15 | `review-net-20260404.md` |
| 4 | Scalability & Performance Engineering | 2 | 7 | 8 | 4 | 0 | 21 | *(in consolidated file below)* |
| 5 | Protocol Design & Future-Proofing | 0 | 5 | 8 | 3 | 1 | 17 | *(in consolidated file below)* |
| 6 | Developer Experience (Runtime Authors) | 0 | 3 | 7 | 3 | 1 | 14 | `review-findings-dxr-20260404.md` |
| 7 | Operator & Deployer Experience | 2 | 5 | 8 | 5 | 3 | 23 | `review-findings-ops-20260404.md` |
| 8 | Multi-Tenancy & Tenant Isolation | 1 | 5 | 7 | 3 | 2 | 18 | `review-findings-tnt-20260404.md` |
| 9 | Storage Architecture & Data Management | 2 | 4 | 5 | 3 | 2 | 16 | `review-findings-str-20260404.md` |
| 10 | Recursive Delegation & Task Trees | 0 | 2 | 8 | 2 | 1 | 13 | `review-findings-del-20260404.md` |
| 11 | Session Lifecycle & State Management | 2 | 5 | 6 | 3 | 1 | 17 | `review-findings-slc-20260404.md` |
| 12 | Observability & Operational Monitoring | 0 | 4 | 10 | 3 | 1 | 18 | `review-findings-obs-20260404.md` |
| 13 | Compliance, Governance & Data Sovereignty | 2 | 5 | 6 | 3 | 2 | 18 | `review-findings-cmp-20260404.md` |
| 14 | API Design & External Interface Quality | 1 | 5 | 6 | 3 | 2 | 17 | `review-findings-api-20260404.md` |
| 15 | Competitive Positioning & Open Source Strategy | 1 | 3 | 5 | 3 | 2 | 14 | `review-findings-mkt-20260404.md` |
| 16 | Warm Pool & Pod Lifecycle Management | 0 | 3 | 7 | 3 | 0 | 13 | *(in consolidated file below)* |
| 17 | Credential Management & Secret Handling | 1 | 4 | 5 | 3 | 2 | 15 | `review-findings-crd-20260405.md` |
| 18 | Content Model, Data Formats & Schema Design | 0 | 4 | 8 | 4 | 0 | 16 | `review-findings-sch-20260404.md` |
| 19 | Build Sequence & Implementation Risk | 1 | 4 | 5 | 3 | 0 | 13 | `review-findings-bld-20260404.md` |
| 20 | Failure Modes & Resilience Engineering | 2 | 5 | 5 | 4 | 2 | 18 | `review-findings-res-20260404.md` |
| 21 | Experimentation & A/B Testing Primitives | 0 | 3 | 3 | 1 | 0 | 7 | `review-findings-exp-20260404.md` |
| 22 | Document Quality, Consistency & Completeness | 0 | 5 | 10 | 5 | 2 | 22 | `review-findings-doc-20260404.md` |
| 23 | Messaging, Conversational Patterns & Multi-Turn | 1 | 4 | 5 | 3 | 2 | 15 | `review-findings-msg-20260404.md` |
| 24 | Policy Engine & Admission Control | 1 | 4 | 5 | 3 | 2 | 15 | `review-findings-pol-20260404.md` |
| 25 | Execution Modes & Concurrent Workloads | 1 | 4 | 4 | 3 | 2 | 14 | `review-findings-exm-20260404.md` |

---

### Critical Findings

| # | ID | Perspective | Finding | Section |
|---|-----|-------------|---------|---------|
| 1 | K8S-001 | Kubernetes Infrastructure & Co... | `agent-sandbox` ADR Prerequisite Is Blocking but Has No Deadline | 4.6.1 |
| 2 | SEC-101 | Security & Threat Modeling... | LLM Proxy Internal Endpoint Uses HTTP Not HTTPS | 4.9 |
| 3 | SEC-102 | Security & Threat Modeling... | Isolation Monotonicity Enforcement Has No Admission-Time Gate | 8.3, 5.3, 17.2 |
| 4 | NET-015 | Network Security & Isolation (... | LLM Proxy URL Uses Plain HTTP in Spec Example | 4.9 |
| 5 | OPS-001 | Operator & Deployer Experience... | CRD Upgrades Require Out-of-Band Manual Step That Helm Cannot Enforce | 10.5, 17.6 |
| 6 | OPS-002 | Operator & Deployer Experience... | No Runbook for the CRD Finalizer Stuck Scenario | 4.6.1, 17.7 |
| 7 | TNT-001 | Multi-Tenancy & Tenant Isolati... | Cloud-Managed Connection Proxy `connect_query` Sentinel Not Guaranteed | 12.3, 17.9 |
| 8 | STR-001 | Storage Architecture & Data Ma... | Redis Quota Fail-Open Enables Deliberate Quota Bypass | 12.4, 11.2 |
| 9 | STR-002 | Storage Architecture & Data Ma... | Artifact GC Strategy Lacks Reference-Counting for Shared Blobs — Storage Leak Risk | 12.5, 12.8 |
| 10 | SLC-001 | Session Lifecycle & State Mana... | `resuming` State Has No Failure Transition — Potential Deadlock | 6.2 |
| 11 | SLC-002 | Session Lifecycle & State Mana... | Generation Counter Increment Committed Before `CoordinatorFence` — Window for Stale Coordinator to Proceed | 10.1 |
| 12 | CMP-001 | Compliance, Governance & Data ... | SIEM Requirement Is Conditional, Not Mandatory — Compliant Deployments Can Ship Without External Audit Trail | 11.7 |
| 13 | CMP-002 | Compliance, Governance & Data ... | Audit Log Batching Creates a Data-Loss Window on Gateway Crash | 11.7, 12.3 |
| 14 | API-001 | API Design & External Interfac... | REST/MCP Parity Contract Has No Runtime Enforcement Path | 15.2.1 |
| 15 | MKT-001 | Competitive Positioning & Open... | Startup Latency Claim Is Undefended Against Competitors' Published Numbers | 6.1, 23.0, 23.1 |
| 16 | CRD-001 | Credential Management & Secret... | No Emergency Credential Revocation Path for Compromised Pool Keys | 4.9, 11.4, 16.5 |
| 17 | BLD-001 | Build Sequence & Implementatio... | Token/Connector Service Ships Too Late for Its Declared Role | 18 (Phase table, Phase 12a note) |
| 18 | RES-001 | Failure Modes & Resilience Eng... | Redis fail-open quota bypass window is too long and unbounded across repeated outages | 12.4 |
| 19 | RES-002 | Failure Modes & Resilience Eng... | Cascading failure: MinIO outage during eviction checkpoint causes unrecoverable session loss with no fallback store | 4.4, 12.5 |
| 20 | MSG-001 | Messaging, Conversational Patt... | Session Inbox Has No Persistence, Size Bound, or Durability Contract | 7.2 |
| 21 | POL-001 | Policy Engine & Admission Cont... | Quota Fail-Open Window Allows Full Tenant Budget Overshoot With No Cross-Replica Coordination | 11.2, 12.4 |
| 22 | EXM-001 | Execution Modes & Concurrent W... | Concurrent-Workspace Mode Lacks a Deployer Acknowledgment Field | 5.2 |
| 23 | SCL-001 | Scalability & Performance... | Gateway single-process throughput ceiling has no measured break point | 4.1 |
| 24 | SCL-002 | Scalability & Performance... | No latency budget for the full session creation hot path | 6.3, 16.5 |

---

## Cross-Cutting Themes

### 1. Specification Gaps in Failure Recovery Paths
Multiple perspectives (RES, SLC, STR, OPS, CRD) found that failure recovery paths are described at a high level but lack the operational detail needed for implementation: timeouts, retry budgets, fallback stores, and crash-recovery sequences are frequently missing or underspecified.

### 2. Redis Fail-Open Creates a Systemic Security Boundary Weakness
The Redis fail-open design (POL, STR, SEC, RES) is the most cross-cutting concern: quota enforcement, delegation budgets, rate limiting, and session coordination all degrade simultaneously during a Redis outage, and the cumulative timer mechanism has structural flaws.

### 3. Observability Metrics Scattered Across Body Text, Not Canonical
OBS, SCL, WPL, CRD, and EXP all found that metrics are named in narrative sections but absent from the canonical metrics table (Section 16.1). This creates a gap between what the spec promises and what an implementer would build.

### 4. Multi-Tenancy Isolation Assumed But Not Verified End-to-End
TNT, SEC, STR, and CMP found that tenant isolation relies on conventions (Redis key prefixes, RLS policies, MinIO bucket paths) that are not enforced by the storage layer itself, not validated by preflight checks, and not auditable at runtime.

### 5. Warm Pool Sizing Formulas Are Inconsistent and Under-Parameterized
K8S, SCL, WPL, and OPS all found that the three sizing formulas across Sections 4.6.1, 4.6.2, and 17.8 are structurally different, use different variable names, and omit key terms from each other.

### 6. Build Sequence Has Critical Dependency Ordering Issues
BLD found that the Token Service, OIDC infrastructure, and mTLS PKI are needed early but placed late in the build sequence, and that load testing precedes security hardening, invalidating SLO measurements.

### 7. Document Structural Issues Create Implementation Risk
DOC found section numbering gaps (8.7 missing, duplicate 17.9), stale cross-references, and undefined terms that would cause implementers to build against incorrect section targets.

---

## Detailed Findings by Perspective

Individual perspective findings are in the following files:

- **1. Kubernetes Infrastructure & Controller Design** (K8S): `docs/review-findings-k8s-20260404.md`
- **2. Security & Threat Modeling** (SEC): `docs/review-findings-sec-20260404.md`
- **3. Network Security & Isolation (file 1)** (NET): `docs/review-net-20260404.md`
- **4. Scalability & Performance Engineering** (SCL): *(findings returned as text — see agent output)*
- **5. Protocol Design & Future-Proofing** (PRT): *(findings returned as text — see agent output)*
- **6. Developer Experience (Runtime Authors)** (DXR): `docs/review-findings-dxr-20260404.md`
- **7. Operator & Deployer Experience** (OPS): `docs/review-findings-ops-20260404.md`
- **8. Multi-Tenancy & Tenant Isolation** (TNT): `docs/review-findings-tnt-20260404.md`
- **9. Storage Architecture & Data Management** (STR): `docs/review-findings-str-20260404.md`
- **10. Recursive Delegation & Task Trees** (DEL): `docs/review-findings-del-20260404.md`
- **11. Session Lifecycle & State Management** (SLC): `docs/review-findings-slc-20260404.md`
- **12. Observability & Operational Monitoring** (OBS): `docs/review-findings-obs-20260404.md`
- **13. Compliance, Governance & Data Sovereignty** (CMP): `docs/review-findings-cmp-20260404.md`
- **14. API Design & External Interface Quality** (API): `docs/review-findings-api-20260404.md`
- **15. Competitive Positioning & Open Source Strategy** (MKT): `docs/review-findings-mkt-20260404.md`
- **16. Warm Pool & Pod Lifecycle Management** (WPL): *(findings returned as text — see agent output)*
- **17. Credential Management & Secret Handling** (CRD): `docs/review-findings-crd-20260405.md`
- **18. Content Model, Data Formats & Schema Design** (SCH): `docs/review-findings-sch-20260404.md`
- **19. Build Sequence & Implementation Risk** (BLD): `docs/review-findings-bld-20260404.md`
- **20. Failure Modes & Resilience Engineering** (RES): `docs/review-findings-res-20260404.md`
- **21. Experimentation & A/B Testing Primitives** (EXP): `docs/review-findings-exp-20260404.md`
- **22. Document Quality, Consistency & Completeness** (DOC): `docs/review-findings-doc-20260404.md`
- **23. Messaging, Conversational Patterns & Multi-Turn** (MSG): `docs/review-findings-msg-20260404.md`
- **24. Policy Engine & Admission Control** (POL): `docs/review-findings-pol-20260404.md`
- **25. Execution Modes & Concurrent Workloads** (EXM): `docs/review-findings-exm-20260404.md`
