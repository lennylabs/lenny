# Technical Design Review Perspectives

This document defines the review perspectives for evaluating `docs/technical-design.md` (the Lenny v1 technical specification). Each perspective is intended to be reviewed independently by a sub-agent. The conversation in `docs/claude_conversation_20260327.md` provides additional context for decisions and user concerns.

Each perspective has a broad focus area and a set of example concerns. The examples are starting points that must be checked, but each review should go beyond them -- identify any gaps, inconsistencies, risks, or improvements relevant to the perspective, even if not listed.

**WHAT TO FOCUS ON:**

- Security gaps
- Missing details in specification
- Documentation errors and inconsistencies
- Design flaws that can lead to malfunction, edge cases, and exploits
- Observability gaps
- Low-hanging-fruit changes that will improve the product without introducing more complexity

**WHAT NOT TO FOCUS ON:**

- Everything else, including improvements that introduce complexity

---

## 1. Kubernetes Infrastructure & Controller Design

**Focus:** Evaluate whether the Kubernetes-native design choices are idiomatic, scalable, and operationally sound. Look at CRD design, controller patterns, resource management, cluster topology, and upstream dependencies.

**Examples:**

- The `kubernetes-sigs/agent-sandbox` dependency -- maturity, lock-in risk, the "pre-commit requirement" to verify its optimistic-locking guarantee
- Etcd pressure mitigations (3-label coarse state, leader-election, claim batching)
- Controller split (WarmPoolController vs PoolScalingController) -- clean separation or overlapping responsibility?
- PodSecurityStandards choices (warn+audit, not enforce)
- Namespace layout conventions

---

## 2. Security & Threat Modeling

**Focus:** Perform a threat model review of the entire system. Consider attack surfaces, trust boundaries, isolation guarantees, and defense-in-depth across all components.

**Examples:**

- SIGSTOP/SIGCONT checkpoint mechanism under gVisor and Kata -- unvalidated assumption?
- Adapter-agent security boundary -- what if a malicious runtime tries to escape?
- Prompt injection vectors through delegation chains and elicitation flows
- Upload safety (zip-slip, symlink, size limits)
- Isolation monotonicity (children >= parent isolation) enforcement through delegation

---

## 3. Network Security & Isolation

**Focus:** Evaluate the network architecture from a defense perspective. Assess whether the gateway-centric model creates a sound network perimeter and whether internal traffic is properly segmented.

**Examples:**

- The three NetworkPolicy manifests -- completeness and correctness
- Lateral movement risk between pods in the same pool namespace
- Dedicated CoreDNS for DNS exfiltration mitigation -- operationally practical?
- Whether any network paths bypass the gateway (violating the gateway-centric invariant)
- Sufficiency of mTLS PKI (cert-manager) without a service mesh

---

## 4. Scalability & Performance Engineering

**Focus:** Assess whether the architecture can meet production-scale demands. Look for bottlenecks, missing performance targets, unvalidated capacity assumptions, and scaling lag.

**Examples:**

- Gateway-centric model as a potential throughput bottleneck
- Absence of concrete performance targets (max sessions, latency budgets)
- Startup latency analysis (Section 6.3) based on estimates, not benchmarks
- Redis-backed session coordination as a scalability ceiling
- HPA scaling lag with custom metrics (Prometheus Adapter / KEDA)

---

## 5. Protocol Design & Future-Proofing (MCP, A2A, AP, OpenAI)

**Focus:** Evaluate whether the protocol strategy is sound for today and extensible for tomorrow. Assess the abstraction layer, protocol-specific assumptions in the core, and translation fidelity.

**Examples:**

- MCP-specific assumptions baked into core logic that would break under A2A
- `ExternalProtocolAdapter` abstraction -- can it support A2A and Agent Protocol without refactoring?
- `publishedMetadata` flexibility as a replacement for protocol-specific discovery formats
- MCP spec evolution risk -- version compatibility concerns

---

## 6. Developer Experience (Runtime Authors)

**Focus:** Evaluate the experience from the perspective of someone building a new runtime for Lenny. Assess the learning curve, integration tiers, tooling, and whether the spec alone is sufficient to get started.

**Examples:**

- Whether a developer with no Lenny knowledge can build a Minimum-tier runtime from the spec
- Degraded experience for Minimum-tier runtimes (no lifecycle channel) -- clearly documented?
- Echo runtime as a reference implementation -- sufficient?
- SDK/library requirements -- the user explicitly wanted to minimize these
- OutputPart complexity vs. using MCP content blocks directly

---

## 7. Operator & Deployer Experience

**Focus:** Evaluate the experience of deploying and running Lenny in production. Cover initial setup, day-2 operations, configuration management, upgrade paths, and the local development story.

**Examples:**

- Bootstrap vs operational plane split (Helm-only vs API-managed) -- any operations that require Helm but shouldn't?
- Operational runbooks -- sufficient for common failure scenarios?
- Two-tier local dev mode (`make run` vs `docker compose`) -- realistic?
- Expand-contract migration strategy -- practical for rolling upgrades?
- Operational defaults -- sensible for a first deployment?

---

## 8. Multi-Tenancy & Tenant Isolation

**Focus:** Evaluate the multi-tenancy model end-to-end. Assess isolation guarantees across all storage backends, the RBAC model, environment scoping, and the tenant lifecycle.

**Examples:**

- Postgres RLS with `SET app.current_tenant` under PgBouncer transaction mode -- robust?
- 3-role RBAC model (platform-admin, tenant-admin, user) -- sufficient? Custom roles?
- Tenant isolation gaps in Redis, MinIO, or the event/checkpoint store
- Tenant deletion -- clean teardown path?
- `noEnvironmentPolicy` (deny-all vs allow-all) default -- clear for operators?

---

## 9. Storage Architecture & Data Management

**Focus:** Evaluate the storage layer for correctness, durability, scalability, and operational complexity. Assess each storage backend's role, failure behavior, and data lifecycle management.

**Examples:**

- Redis fail-open behavior -- security risks (e.g., quota bypass)?
- Artifact GC strategy (reference counting + periodic sweep) -- storage leak risk?
- "No shared RWX storage" non-goal -- validated against real agent workflows?
- Checkpoint storage scaling with long-running sessions
- Data-at-rest encryption completeness

---

## 10. Recursive Delegation & Task Trees

**Focus:** Evaluate the recursive delegation model for correctness, safety, and recovery. Assess policy propagation, resource budgets, tree lifecycle, and edge cases at depth.

**Examples:**

- "Rejection is permanent for the tree" rule for lease extensions -- too aggressive for long-running workflows?
- Recovery of deep delegation trees (depth 5+) after multiple node failures
- Orphan cleanup interval for `detach` cascading policy -- specified?
- Credential propagation (inherit/isolate/explicit) through delegation chains
- Cross-environment delegation interaction with delegation policy evaluation

---

## 11. Session Lifecycle & State Management

**Focus:** Evaluate the session lifecycle for correctness and completeness. Assess state machines, transitions, edge cases, and the guarantees provided to clients.

**Examples:**

- Pod and session state machines -- unreachable or deadlock states?
- Generation counter mechanism for split-brain prevention -- correct in all edge cases?
- Checkpoint failure mid-SIGSTOP -- timeout and recovery path?
- Session forking (`derive` endpoint) -- workspace snapshot and credential state handling?
- SSE buffer overflow semantics (drop connection) -- acceptable vs. backpressure?

---

## 12. Observability & Operational Monitoring

**Focus:** Evaluate whether the observability stack provides sufficient visibility for understanding system health, diagnosing incidents, and meeting SLOs.

**Examples:**

- Metrics blind spots -- any subsystems with insufficient coverage?
- Delegation tree observability (parent-child relationships, budget consumption)
- Warm pool observability (utilization, claim latency, waste)
- Alerting rule calibration -- missing critical or warning alerts?
- Tracing across delegation chains -- sufficient for debugging?

---

## 13. Compliance, Governance & Data Sovereignty

**Focus:** Evaluate regulatory readiness across the full data lifecycle. Assess whether the design meets common compliance frameworks and handles data sovereignty requirements.

**Examples:**

- GDPR erasure flow completeness across all storage backends
- Data residency "punted to the deployer" -- sufficient for regulated industries?
- Audit log integrity (INSERT-only grants, startup verification) -- enforceable?
- Billing event immutability -- what about corrections?
- SOC2, HIPAA, FedRAMP considerations

---

## 14. API Design & External Interface Quality

**Focus:** Evaluate the external API surface for usability, consistency, completeness, and suitability for third-party tooling. The user explicitly required the admin API be good enough for others to build UIs/CLIs on top.

**Examples:**

- REST/MCP consistency contract -- enforceable? How is parity tested?
- Admin API quality for third-party UI/CLI development
- Error response specification across endpoints
- `dryRun` parameter behavior with side-effecting operations
- Etag-based conditional PUT for concurrent admin access

---

## 15. Competitive Positioning & Open Source Strategy

**Focus:** Evaluate Lenny's market position, differentiation, and community adoption strategy. Assess whether the design choices create a compelling open-source project.

**Examples:**

- Missing differentiation narrative -- competitors listed but "why Lenny?" not articulated
- `kubernetes-sigs/agent-sandbox` upstream risk -- what if the project changes direction?
- Community adoption funnel -- DX features exist but no explicit strategy
- "Hooks and defaults" philosophy -- competitive advantage or adoption barrier?
- Comparison to Temporal, Modal, LangGraph for task orchestration

---

## 16. Warm Pool & Pod Lifecycle Management

**Focus:** Evaluate the warm pool model for correctness, efficiency, and operational complexity. Assess whether the pre-warming strategy delivers its promised latency benefits without excessive waste.

**Examples:**

- SDK-warm mode complexity vs. latency benefit tradeoff
- `sdkWarmBlockingPaths` mechanism -- robust or fragile?
- Pool sizing formulas under burst traffic
- Pod eviction during SDK-warm -- handled?
- Experiment variant impact on pool sizing and waste

---

## 17. Credential Management & Secret Handling

**Focus:** Evaluate the credential lifecycle end-to-end -- provisioning, leasing, rotation, revocation, and propagation through delegation. Assess both security and operational manageability.

**Examples:**

- LLM reverse proxy as a bottleneck risk
- Credential rotation mid-session via lifecycle channel -- reliable?
- Credential pool exhaustion handling
- Three credential modes (pool/user/fallback) -- clearly differentiated for operators?
- KMS integration completeness

---

## 18. Content Model, Data Formats & Schema Design

**Focus:** Evaluate the internal data formats for correctness, extensibility, and clean protocol translation. Assess whether schemas will age well and handle future requirements.

**Examples:**

- `OutputPart` translation fidelity to/from MCP, OpenAI, and future A2A formats
- `MessageEnvelope` sufficiency for future multi-turn conversational patterns
- `RuntimeDefinition` schema (base vs derived, capability inference) -- ambiguity?
- Schema versioning strategy -- what happens when formats evolve?
- `WorkspacePlan` completeness for all workspace materialization scenarios

---

## 19. Build Sequence & Implementation Risk

**Focus:** Evaluate the 17-phase build sequence for feasibility, risk ordering, and dependency correctness. Identify the critical path and highest-risk phases.

**Examples:**

- Phase dependency ordering (e.g., credential leasing needed for real LLM usage but comes late)
- Missing phases (security audit, benchmarking/load testing, compliance validation?)
- Echo runtime sufficiency for testing early phases
- Community onboarding (Phase 17) realism given system complexity
- Phases that could be parallelized

---

## 20. Failure Modes & Resilience Engineering

**Focus:** Evaluate the system's behavior under failure. Assess whether every component has defined failure behavior and whether cascading failures are prevented.

**Examples:**

- Redis fail-open tradeoff (quota bypass during outage) -- acceptable?
- Cascading failure scenarios (e.g., MinIO down -> checkpoint failure -> session loss)
- Postgres failover window -- inconsistency risk?
- Gateway preStop drain hook -- sufficient for zero-downtime rolling updates?
- Controller crash blast radius and recovery time

---

## 21. Experimentation & A/B Testing Primitives

**Focus:** Evaluate the experimentation model for clarity, completeness, and correct boundary-setting between platform primitives and full experimentation features.

**Examples:**

- Experiment results API references "eval scores by variant" without defining how scores flow in
- Health-based rollback specification -- what metrics trigger it?
- Experiment context propagation through delegation leases
- `PoolScalingController` handling of variant pools without waste
- Boundary clarity between platform primitives and full experimentation

---

## 22. Document Quality, Consistency & Completeness

**Focus:** Review the document itself for structural issues, internal consistency, and editorial quality. This is a meta-review of the spec as a document.

**Examples:**

- Billing event stream duplication (Sections 11.2.1 and 11.8)
- Section numbering errors (two sections labeled 17.5)
- Empty Open Questions section (20) -- intentional?
- Cross-reference validity between sections
- Terms used without definition
- Document length (~3,700 lines) -- should sections be extracted?

---

## 23. Messaging, Conversational Patterns & Multi-Turn Interactions

**Focus:** Evaluate the messaging and conversational model for completeness, edge case handling, and readiness for future interaction patterns.

**Examples:**

- 3 message delivery paths -- complete and unambiguous? Edge cases (timeout, terminated recipient)?
- `input_required` task state integration with session lifecycle state machine
- "Agent teams" pattern support (siblings coordinating on shared tasks)
- SSE buffer overflow handling (drop connection vs. backpressure)
- Message routing interaction with delegation policies

---

## 24. Policy Engine & Admission Control

**Focus:** Evaluate the policy engine for correctness, completeness, and safe behavior under edge conditions. Assess the interaction between policy layers and the extensibility model.

**Examples:**

- `RequestInterceptor` chain execution order and short-circuit behavior -- specified?
- Budget propagation through delegation trees -- can a child exceed parent's remaining budget?
- Quota update timing (Redis real-time, Postgres periodic) -- consistency risk?
- Fail-open behavior during Redis outages -- bounded correctly?
- Timeout table completeness for all operations

---

## 25. Execution Modes & Concurrent Workloads

**Focus:** Evaluate the three execution modes for design soundness, security implications, and clear communication of tradeoffs to deployers.

**Examples:**

- Task mode cleanup ("best-effort, not a security boundary") -- sufficient?
- Concurrent `concurrencyStyle: workspace` -- `slotId` multiplexing failure semantics per-slot?
- Concurrent-stateless vs. "should just be a connector" -- distinction clear?
- Graph mode elimination (just a session) -- correctly handled?
- Execution mode interaction with warm pool strategy and pod lifecycle
