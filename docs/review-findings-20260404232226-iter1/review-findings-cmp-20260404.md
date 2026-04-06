# Compliance, Governance & Data Sovereignty Review Findings — 2026-04-04

**Document reviewed:** `docs/technical-design.md` (5,277 lines)
**Perspective:** 13. Compliance, Governance & Data Sovereignty
**Category code:** CMP
**Reviewer focus:** Regulatory readiness across the full data lifecycle. GDPR erasure completeness, data residency enforceability, audit log integrity, billing immutability, and SOC2/HIPAA/FedRAMP readiness.

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 2 |
| High     | 5 |
| Medium   | 6 |
| Low      | 3 |
| Info     | 2 |

---

## Critical

### CMP-001 SIEM Requirement Is Conditional, Not Mandatory — Compliant Deployments Can Ship Without External Audit Trail [Critical] — VALIDATED/FIXED
**Section:** 11.7

Section 11.7 establishes a rigorous four-layer audit integrity model and correctly states that "audit events **must** be streamed to an external immutable log (SIEM, cloud audit service, or append-only object storage)." However, this requirement is only enforced when `audit.siem.endpoint` is configured — the gateway checks SIEM connectivity at startup only if the deployer has already configured an endpoint. A deployer who never sets `audit.siem.endpoint` faces no startup error, no warning, and no operational consequence.

The practical result: a production deployment can operate indefinitely with audit logs stored only in Postgres, where a database superuser can bypass INSERT-only grants. The hash-chain detection mechanism (Section 11.7, item 3) detects tampering after the fact but does not prevent it. The external SIEM is described as the independent copy that "a database superuser cannot modify" — but if it is optional, this guarantee is absent for all deployments that omit it.

Regulated workloads under SOC2 (CC7.2, CC7.3), FedRAMP (AU-9), and HIPAA (§164.312(b)) require tamper-evident audit logs with separation of control from the system being audited. Relying solely on a Postgres database that the platform itself writes to is unlikely to satisfy these requirements.

**Recommendation:**
1. Add a hard startup check: if `LENNY_ENV=production`, the gateway **must** refuse to start unless `audit.siem.endpoint` is configured and passes the connectivity test. Apply the same pattern as the TLS check in Section 17.4 (Sec-C2).
2. Add a `lenny-preflight` check (Section 17.6) that fails installation if `audit.siem.endpoint` is unset in production mode, with the message: `"audit.siem.endpoint is required in production mode (Section 11.7); audit integrity requires an external immutable log."`
3. Add `AuditSIEMNotConfigured` to the warning alert table in Section 16.5, firing immediately on startup if the endpoint is absent in production.

**Resolution:** Section 11.7 now includes an "Audit integrity gap — Postgres-only deployments" block that: names the superuser bypass gap explicitly, declares compliance-grade deployments (SOC2, FedRAMP, HIPAA) require `audit.siem.endpoint`, and defines startup behavior (WARN-level repeated every 60s in multi-tenant production; logged once in single-tenant production; silent in non-production). Section 16.5 adds `AuditSIEMNotConfigured` alert (Warning for single-tenant, Critical for multi-tenant production). Section 17.6 adds a non-blocking preflight check that emits a named warning when SIEM is unconfigured in production mode, referencing SOC2 CC7.2, FedRAMP AU-9, HIPAA §164.312(b).

---

### CMP-002 Audit Log Batching Creates a Data-Loss Window on Gateway Crash [Critical] — VALIDATED/FIXED
**Section:** 11.7, 12.3

Section 12.3 recommends batching audit log inserts for performance (`auditFlushIntervalMs`, default 1000ms; `auditFlushBatchSize`, default 100 entries). Audit entries are explicitly described as "non-critical-path (they do not block request processing), so a longer flush interval is acceptable." This optimization is correct for throughput but introduces a compliance gap: if a gateway replica crashes during the flush interval, up to 1 second of audit events (up to 100 entries at Tier 3 write rates of ~300/s) are silently lost. The flush buffer is in-memory and is not described as having any write-ahead log or persistence mechanism.

The billing event write path explicitly handles this scenario with an in-memory write-ahead buffer plus a reconstruction path from pod-reported token usage (Section 11.2.1). No equivalent recovery mechanism is described for audit events.

For SOC2 (CC7.2), FedRAMP (AU-9), and HIPAA (§164.312(b)) the audit log must be complete; loss of audit records representing authentication, policy decisions, and data access events during a crash is not recoverable.

**Recommendation:**
1. Document that the audit flush buffer is a deliberate durability trade-off and is only acceptable when a SIEM is also receiving the same events in real-time (making the in-Postgres copy redundant on crash). If no SIEM is configured, audit inserts must be synchronous (direct write, no batching) in production mode.
2. When SIEM is configured and real-time SIEM delivery is confirmed, the in-Postgres audit batching remains acceptable as the secondary copy — the SIEM provides the durable primary.
3. Add a reconciliation procedure: on gateway startup, verify the audit chain continuity (the hash-chain check in item 3 of Section 11.7) and log a warning identifying the gap if the chain breaks, so operators can replay from the SIEM.

**Resolution:** Section 12.3 audit batching guidance was expanded: (1) `auditFlushIntervalMs` default reduced from 1000ms to 250ms, with a bounded-loss table (2/13/75 events at Tiers 1/2/3), (2) when SIEM is configured, Postgres batching is acceptable (SIEM is the durable primary), (3) in `LENNY_ENV=production` without SIEM, audit inserts become synchronous (batching disabled) using a dedicated 4-connection pool — no new infrastructure, (4) a startup chain-continuity check verifies the hash chain on restart and fires `AuditChainGap` alert if a gap is found. Section 16.5 adds `AuditChainGap` Warning alert. Section 17.8 tier sizing table updated to 250ms audit flush interval.

---

## High

### CMP-003 GDPR User-Level Erasure Has No SLA or Completion Guarantee — No Deadline, No Monitoring [High]
**Section:** 12.8

Section 12.8 defines `DeleteByUser(user_id)` as a background job that "runs to completion and produces an erasure receipt." The data classification table (Section 12.9) states T3 data must be erased "within 72 hours (GDPR-aligned)" and T4 data must be erased "immediately." However:

- There is no API endpoint for users or administrators to *request* a user-level erasure. The tenant deletion lifecycle (Phase table in 12.8) covers tenant-level deletion but `DeleteByUser` is described only as a method on each store interface, with no corresponding admin API endpoint (the Admin API table in Section 15.1 has no `/v1/admin/users/{id}/erase` or equivalent).
- There is no timer, deadline enforcement, or alerting on erasure job completion. A stalled or failed job produces no observable signal until an admin manually inspects the audit trail.
- There is no metric (`lenny_erasure_job_duration_seconds`, `lenny_erasure_overdue_total`) to detect erasure jobs that have exceeded their tier-specific deadline.

GDPR Article 17 requires a response to a right-to-erasure request within one calendar month. Without an invocable API endpoint and a monitored SLA, the platform cannot demonstrate regulatory compliance.

**Recommendation:**
1. Add `POST /v1/admin/users/{user_id}/erase` to the Admin API (Section 15.1). The endpoint initiates the background erasure job and returns a job ID.
2. Add `GET /v1/admin/erasure-jobs/{job_id}` for status polling, returning phase, completion percentage, and time elapsed.
3. Specify a 72-hour SLA for T3 erasure and immediate (< 1 hour) SLA for T4. Add a `ErasureJobOverdue` alert (Section 16.5) that fires when an erasure job exceeds its tier-specific deadline.
4. Add the following metrics: `lenny_erasure_job_started_total`, `lenny_erasure_job_completed_total`, `lenny_erasure_job_failed_total`, `lenny_erasure_job_duration_seconds` (histogram).

---

### CMP-004 `erasure_salt` for Billing Pseudonymization Is Underdefined — Storage, Rotation, and Key Management Are Absent [High]
**Section:** 12.8

The billing pseudonymization procedure replaces `user_id` with `SHA-256(user_id || erasure_salt)`. This correctly preserves referential integrity while removing direct identifiers. However, the specification says nothing about:

- Where `erasure_salt` is stored and who manages it.
- Whether the salt is per-user, per-tenant, or platform-wide (a shared salt means all pseudonymized records are re-linkable to each other by an adversary with access to any one real `user_id`).
- How the salt is protected at rest (KMS-backed? Kubernetes Secret? Plaintext config?).
- Whether and how the salt can be rotated, and what happens to previously pseudonymized records after rotation.
- Whether the pseudonymized hash is reversible if the salt is compromised — which it is, since SHA-256 with a known salt is brute-forceable for the constrained space of `user_id` values.

Under GDPR, pseudonymization is only a risk-reduction technique (Recital 26), not anonymization. If the `erasure_salt` can be recovered by an adversary with database access, the pseudonymized `user_id` values are re-identifiable — meaning the "erasure" is not erasure at all.

**Recommendation:**
1. Define `erasure_salt` as a **per-tenant**, KMS-backed secret stored in the Token Service (Section 4.3), not in Postgres. This puts the salt under the same envelope-encryption protection as OAuth tokens.
2. Specify that the salt is never stored alongside the hashed values — an adversary with Postgres access cannot trivially reverse the pseudonymization without also compromising KMS.
3. Document the salt rotation procedure: on rotation, all previously pseudonymized billing records must be re-hashed with the new salt (a one-time re-encryption job), or the old salt must be retained indefinitely (reducing the security benefit of rotation). Document which approach is taken.
4. Add a compliance note that pseudonymized billing events with the original salt accessible remain GDPR-regulated personal data. Tenants with `billingErasurePolicy: exempt` must be clearly documented as retaining personal data subject to Article 17(3)(b) — this is done, but the corresponding risk warning about salt security is missing.

---

### CMP-005 Data Residency Enforcement Has No Runtime Audit — Violations Detectable Only by Rejections, Not by Independent Monitoring [High]
**Section:** 12.8

Section 12.8 describes three enforcement levels for `dataResidencyRegion`: pod pool routing, storage routing, and session-creation validation. These are preventive controls that block out-of-region operations at the time they are attempted. However:

- There is no independent monitoring or audit trail that continuously verifies data residency compliance. If a bug, misconfiguration, or future code path bypasses the `StorageRouter`, data could flow to an out-of-region store without generating any observable signal.
- The `StorageRouter` interface accepts `dataResidencyRegion` as a parameter, but there is no stated test or integration check that verifies the router *always* routes to the correct region. A router that silently falls back to the default (single-region) storage on a configuration error would create a compliance violation with no alarm.
- The "global catalog" that replicates tenant metadata including `dataResidencyRegion` (Section 12.8, multi-region reference architecture) is itself a cross-region data store. The spec does not define where this catalog resides, what data it contains beyond region routing, or whether its contents are subject to data residency constraints. If it replicates tenant PII fields (names, contact info, etc.), it may itself be a residency violation.

For regulated industries (GDPR Chapter V, EU financial sector), data residency is not just a routing preference — it is an independently auditable compliance obligation.

**Recommendation:**
1. Add a `DataResidencyViolationAttempt` audit event (critical severity) emitted any time the `StorageRouter` receives a request for a region that does not match the configured `dataResidencyRegion`. This turns silent bypasses into observable events.
2. Add a periodic background job (daily) that queries each storage backend and verifies that object path prefixes match the expected region. Any mismatch triggers a `DataResidencyViolationDetected` critical alert.
3. Define the global catalog's data classification: what fields are replicated, which region it lives in, and whether it is excluded from residency constraints (and why, with a documented legal basis for that exclusion).
4. Add a `lenny-preflight` check that validates `StorageRouter` configuration for all configured regions before accepting a production deployment.

---

### CMP-006 Billing Correction Events Have No Authorization or Approval Workflow — Any Authorized Writer Can Issue Corrections [High]
**Section:** 11.2.1

Section 11.2.1 defines `billing_correction` events that override original billing records. The correction mechanism is well-designed for append-only immutability (the original record is preserved, corrections carry their own sequence numbers). However, the spec contains no controls over *who* can emit a correction or under what process:

- There is no stated RBAC requirement limiting `billing_correction` to a specific privileged role or administrative workflow.
- There is no approval workflow, second-factor authorization, or dual-control requirement for corrections.
- There is no stated limit on how many corrections can be applied to a single original event, or any detection of suspicious correction patterns (e.g., many corrections to the same original, corrections that reduce billing amounts toward zero).
- The `correction_reason` field is free-text and unvalidated — it provides no audit trail of business justification.

For SOC2 (CC6.1) and financial record-keeping regulations, billing corrections are a high-risk operation: they change the authoritative revenue record. Without dual-control or approval workflows, a compromised gateway process or a malicious operator can retroactively alter all billing figures while the audit trail appears clean (the corrections are properly appended).

**Recommendation:**
1. Define `billing_correction` as a **privileged admin API operation** (`POST /v1/admin/billing-corrections`) requiring the `platform-admin` RBAC role — the gateway should not be able to emit corrections directly via the EventStore without going through this endpoint.
2. Add a mandatory `justification` structured field (not free-text) with an enum of approved correction reason codes (e.g., `METERING_BUG`, `RETRY_OVERCOUNTING`, `TEST_SESSION_CLEANUP`).
3. Consider requiring a second-factor confirmation (e.g., the submitting admin must re-authenticate with MFA, or a second `platform-admin` must approve the correction via `POST /v1/admin/billing-corrections/{id}/approve`) for corrections exceeding a configurable threshold (e.g., corrections adjusting more than N% of the original value or affecting more than M sessions in a 24h window).
4. Add a `BillingCorrectionRateHigh` warning alert (Section 16.5) that fires when correction events exceed a deployer-configured percentage of total billing events in a rolling 24h window.

---

### CMP-007 Audit Log Retention of 90 Days Is Insufficient for SOC2, FedRAMP, and HIPAA Requirements [High]
**Section:** 16.4, 17.9

The default audit event retention is 90 days (Section 16.4, Section 17.9). This is far below the retention periods required by major compliance frameworks:

- **SOC2:** Auditors typically require 12 months of audit evidence for annual assessments.
- **FedRAMP:** NIST SP 800-53 AU-11 requires retention for a period defined in the organization's configuration baseline, commonly 1–3 years. Low-baseline systems require 90 days, but Moderate and High baselines require 1 year and 3 years respectively.
- **HIPAA:** 45 CFR §164.312(b) requires audit controls "to record and examine activity in information systems." HHS guidance recommends 6 years for audit log retention as part of the broader 6-year HIPAA record retention rule.
- **EU NIS2 / DORA (financial sector):** Require minimum 5-year retention for incident-related audit logs.

The spec notes that "deployers should configure an external log aggregation stack (ELK, Loki, CloudWatch, etc.) for long-term retention beyond the Postgres window," but this is documentation guidance, not an enforced configuration. A deployer who does not configure external aggregation will violate these requirements by default.

**Recommendation:**
1. Change the default audit event retention from 90 days to **1 year** (365 days) as a safer default that meets the broadest set of common frameworks. Add Helm documentation that deployers requiring FedRAMP High or HIPAA should configure 3 years or 6 years respectively.
2. Add a `ComplianceAuditRetentionLow` warning at startup if audit retention is configured below 365 days and `LENNY_ENV=production`.
3. Add a `regulatoryAuditRetention` configuration option with named presets: `soc2` (1 year), `fedramp-moderate` (1 year), `fedramp-high` (3 years), `hipaa` (6 years), `custom` (deployer-specified). The preset selection drives the Postgres partition retention policy automatically.
4. When SIEM is configured, document the responsibility split: Postgres provides short-term queryable storage; the SIEM is the system of record for long-term regulatory retention. The 90-day Postgres window is then defensible as a hot-tier cache.

---

## Medium

### CMP-008 No Data Subject Access Request (DSAR) API or Workflow — GDPR Article 15 Readiness Is Absent [Medium]
**Section:** 12.8, 15.1

Section 12.8 addresses GDPR Article 17 (erasure) and Article 5(1)(e) (storage limitation) in reasonable detail. However, GDPR Article 15 (right of access by the data subject) is completely absent. A data subject can request all personal data held about them, and the controller must provide a structured, machine-readable export within one calendar month.

The spec has no:
- Endpoint for generating a user-scoped data export (all sessions, transcripts, billing events, memory store contents, audit events referencing the user).
- Defined output format for such an export.
- SLA for producing the export.
- Workflow for verifying the requestor's identity before export (preventing unauthorized DSARs).

The same gap applies to GDPR Article 20 (data portability) and CCPA §1798.100 (right to know).

**Recommendation:**
1. Add `POST /v1/admin/users/{user_id}/data-export` to the Admin API. The endpoint initiates a background job that collects all user-scoped data across all stores in the erasure scope table (Section 12.8) and packages it as a structured JSON archive.
2. Define a standard export schema covering: sessions, task trees, transcripts, billing events (with pseudonymization policy applied consistently), memory store contents, and audit events where the user is the subject.
3. Add `GET /v1/admin/data-exports/{job_id}` for status and `GET /v1/admin/data-exports/{job_id}/download` for retrieval.
4. Specify a 30-day SLA for completion and add a `DataExportOverdue` alert.
5. Note the identity-verification responsibility: the platform provides the mechanism; the deployer is responsible for verifying the requestor's identity before triggering the export via the admin API.

---

### CMP-009 The `global catalog` for Multi-Region Residency Routing Is a Silent Cross-Region PII Replication — Its Data Scope Is Undefined [Medium]
**Section:** 12.8

The multi-region reference architecture states: "Tenant metadata (including `dataResidencyRegion`) is replicated to a lightweight global catalog so the load balancer can resolve region affinity before the first request reaches a gateway."

This global catalog is a cross-region store by definition — it exists to be read from all regions. The spec does not define:
- What fields constitute "tenant metadata" in the catalog. If it includes tenant names, contact email addresses, or any fields that identify individuals, it is a cross-region replication of personal data.
- Where the catalog is physically hosted (which region, which jurisdiction).
- Whether replication to the catalog is subject to any data classification controls.
- Whether the catalog is in scope for data residency constraints or explicitly exempt.

If the catalog replicates EU tenant metadata to a US-hosted catalog, this is likely an unauthorized international data transfer under GDPR Chapter V — precisely the violation that `dataResidencyRegion` is designed to prevent.

**Recommendation:**
1. Define the precise schema of the global catalog. At minimum, document which fields are replicated and assert that no T3 or T4 data (Section 12.9) is included.
2. Limit the catalog to routing-metadata only: `tenant_id`, `dataResidencyRegion` (the region code itself, not PII), and the regional gateway endpoint. This is sufficient for routing without containing personal data.
3. Add a classification annotation to the catalog: "T2 — Internal. Contains routing metadata only. No PII. Not subject to data residency constraints." Document this assertion explicitly.
4. Add a validation check that the catalog schema never introduces fields above T2 classification without a reviewed design change.

---

### CMP-010 The INSERT-Only Grant Enforcement Can Be Bypassed at Schema Migration Time — No Controls on Migration-Time Grants [Medium]
**Section:** 11.7

Section 11.7 correctly requires INSERT-only grants on audit tables and verifies them at startup and every 5 minutes. However, these checks verify the *current* grant state — they cannot retroactively detect a period during schema migration when UPDATE/DELETE grants existed. The spec states grants "are defined in the schema migration files," but:

- There is no requirement that schema migration scripts are reviewed for grant escalations before deployment.
- During a rolling upgrade, old and new gateway replicas run simultaneously. If the migration script temporarily grants UPDATE to a role during a migration step, all running replicas will pass the grant check before the migration, but the audit log entries written during the migration window are unprotected.
- Migration-time admin access (the migrating process runs with elevated credentials) is not described as being scoped or logged separately.

For SOC2 (CC6.6) and FedRAMP (CM-3), schema changes to security-relevant tables must go through a change management process.

**Recommendation:**
1. Add a CI/CD gate that automatically scans every schema migration file for `GRANT UPDATE` or `GRANT DELETE` on audit tables and fails the build if found.
2. Specify that schema migrations for the audit and billing tables must be reviewed by a designated compliance role before merge (document this in the ADR for those tables).
3. Add a migration-time audit: the migration framework should emit a structured log entry before and after schema changes to sensitive tables, recording the migration script hash, the executing role, and the timestamp. These entries should be forwarded to the SIEM before the migration begins (not batched after).

---

### CMP-011 Legal Hold Has No Listing API, Expiry, or Cross-Tenant Reporting — Unsuitable for E-Discovery Production [Medium]
**Section:** 12.8

The legal hold mechanism is defined minimally: a `legal_hold` boolean on sessions and artifacts, settable via `POST /v1/admin/legal-hold`. The spec does not define:

- `GET /v1/admin/legal-holds` — listing all active legal holds, their creation date, the admin who set them, and the resources affected. Without this, an operator cannot respond to a court's request to identify all holds in effect.
- Legal hold expiration (automatic or manual) — an inadvertent hold that is never cleared can block data deletion indefinitely.
- Reporting: no ability to export a list of all holds for a tenant, which is required when responding to regulators or in e-discovery.
- Cross-tenant hold visibility for `platform-admin` operators who may need to enumerate holds across all tenants for legal purposes.
- The hold model is binary (boolean) — there is no concept of hold categories (litigation hold vs. regulatory hold vs. audit hold) that would allow different holds to co-exist with different lifecycle rules.

**Recommendation:**
1. Add `GET /v1/admin/legal-holds` (filterable by tenant, resource type, creation date, created-by) to the Admin API.
2. Add optional `expiresAt` and `holdCategory` (enum: `litigation`, `regulatory`, `audit`, `investigation`) fields to the hold record.
3. Add a `LegalHoldAging` warning alert for holds that have been active for more than 90 days without activity, to prevent indefinite accumulation.
4. Ensure the legal hold table is itself subject to audit logging (reads as well as writes, per T4 classification rules in Section 12.9, since hold status can reveal sensitive litigation context).

---

### CMP-012 Data Classification Controls Are Not Enforced at the External API Layer — Clients Can Download T3 Data Without Audit [Medium]
**Section:** 12.9, 15.1

Section 12.9 specifies that T3 data requires audit logging of "all read/write operations." However, the REST API endpoints that return T3 data — `GET /v1/sessions/{id}/transcript`, `GET /v1/sessions/{id}/workspace`, `GET /v1/sessions/{id}/artifacts/{path}` — are not explicitly described as generating audit events on read.

Section 11.7 enumerates what every session/task/delegation produces: "Who requested it, what runtime, which policies were applied, token usage, retries, cancellations, failures, external tool access, denial reasons." Downloads of workspace snapshots and transcripts are notably absent from this list, even though they are the most sensitive T3 data accesses.

A HIPAA or FedRAMP auditor reviewing the audit log would expect to see a record of every access to PHI-tagged workspace data. If `GET /v1/sessions/{id}/workspace` does not generate an audit event, there is no evidence of unauthorized access by a privileged operator.

**Recommendation:**
1. Explicitly enumerate artifact/transcript download endpoints in Section 11.7 as generating `data.accessed` audit events. The event should record: `user_id`, `tenant_id`, `session_id`, `artifact_type` (transcript, workspace, artifact), `artifact_path`, `bytes_returned`, `timestamp`.
2. For T4 workspace data (tenants with `workspaceTier: T4`), log all *access attempts* including those that fail authorization, per the T4 audit logging requirement in Section 12.9.
3. Add `data.accessed` to the audit event type catalog (Section 11.7) alongside the existing session/task/delegation events.

---

### CMP-013 Billing Event Reconstruction from Pod-Reported Token Usage Is Unverified and Potentially Manipulable [Medium]
**Section:** 11.2.1

Section 11.2.1 states: "if a gateway replica crashes with buffered events, those events are reconstructed from pod-reported token usage during session recovery (Section 7.3)." This reconstruction path:

- Relies on the pod reporting its own token usage honestly. A compromised or maliciously-coded runtime could under-report usage, reducing the reconstructed billing amount.
- Is not described in Section 7.3 with enough detail to evaluate whether the reconstruction is complete (does it cover all event types that would have been in the buffer, or only `session.token_usage` events?).
- Creates a reconciliation gap: the reconstructed events will have different `sequence_number` values than the original events would have had, since sequence numbers are assigned at write time. This means the gap-detection mechanism (monotonic sequence numbers) cannot distinguish between a missing event and a reconstructed event.

For billing integrity and financial record-keeping, the accounting system must be able to demonstrate that all charges are based on independently verified measurements, not self-reported pod values.

**Recommendation:**
1. Specify in Section 11.2.1 (or Section 7.3) the complete list of billing event types that can be reconstructed and those that cannot. Any event type that cannot be reconstructed should trigger an explicit `billing_gap` event (with `corrects_sequence: null` and `correction_reason: GATEWAY_CRASH_UNRECOVERABLE`) so the gap is visible in the audit trail.
2. Add independent accounting: the gateway should record cumulative token counts in the session record in Postgres (synchronously, not in the billing buffer) so that reconstruction can be cross-checked against a gateway-side measurement rather than trusting pod self-reporting.
3. Reconstructed events should be tagged with `reconstruction_source: pod_reported` and a `reconstructed: true` flag so they are distinguishable in billing reconciliation.

---

## Low

### CMP-014 No Mention of Penetration Testing, Vulnerability Disclosure, or Third-Party Audit Cadence [Low]
**Section:** General

The spec does not define:
- Any penetration testing requirement or cadence (annual pen test is required by SOC2 CC4.1, FedRAMP CA-8).
- A vulnerability disclosure policy (VDP) or responsible disclosure process, which is particularly relevant since this is an open-source project (Section 23.2).
- Third-party security audit requirements for regulated deployments.
- Bug bounty program (mentioned nowhere despite being an open-source community project that processes agent workloads).

Phase 14 mentions "security audit and penetration testing" as a deliverable, which is positive, but there is no requirement for *ongoing* periodic testing after GA.

**Recommendation:**
1. Add a compliance operations section specifying minimum testing cadences for deployers targeting SOC2/FedRAMP/HIPAA.
2. Add a `SECURITY.md` file reference in the spec (or in the open-source section, Section 23.2) covering responsible disclosure policy and CVE reporting channels.
3. Specify that the platform's dependency supply chain (Go modules, container base images) is scanned by automated tooling (e.g., `govulncheck`, Trivy, Grype) on every build — Phase 14's image signing requirement is adjacent but does not cover runtime dependency scanning.

---

### CMP-015 KMS Key Rotation Procedure Does Not Address Audit Table Hash Chain Invalidation [Low]
**Section:** 10.4, 11.7

Section 10.4 describes the KMS key rotation procedure for credential encryption. The audit log hash chain (Section 11.7) uses SHA-256 of plaintext fields — it does not use KMS-backed encryption. However, if audit events stored in Postgres contain encrypted fields (e.g., if future changes store PII fields with envelope encryption), rotation of the underlying KMS key could invalidate the ability to re-verify the chain on old entries.

More practically: if the hash chain verification check (Section 11.7, item 2 and item 3) is used as the compliance evidence of audit integrity, there must be documentation that the chain can be independently re-verified by a third-party auditor. A third party cannot verify the chain without read access to the database — this is not a tool that can be handed to an external auditor without granting them database access.

**Recommendation:**
1. Provide a `lenny-ctl audit verify-chain --tenant <id> --start <date> --end <date>` CLI command that re-verifies the hash chain over a specified window and produces a signed verification report. The report can be provided to auditors without granting database access.
2. Document the key rotation interaction: if future versions encrypt audit payload fields, specify that chain verification must use the key version that was current at write time (identified by `kid` header, parallel to the JWT rotation pattern in Section 10.4).
3. Add a compliance note that the hash chain provides tamper-evidence, not confidentiality — the chain verification result confirms integrity, not that the data was not read by unauthorized parties.

---

### CMP-016 Semantic Cache Erasure Gap — Cached PII Responses May Outlive User Erasure [Low]
**Section:** 12.8, 12.9

Section 12.8 correctly includes `SemanticCache` in the erasure scope table (`DeleteByUser(user_id)` against Redis). However:

- The `SemanticCache` stores query/response pairs scoped to the user. If a semantic cache entry contains a query that includes PII about *another* user (e.g., an agent was asked about User B and the response is cached under User A's scope), deleting User A's cache entries does not erase the PII of User B.
- Semantic cache entries may contain session transcripts or workspace content fragments embedded in LLM responses. These are T3 data by classification (Section 12.9). The cache TTL and erasure interaction is not defined for entries that span multiple users.
- There is no mechanism to flush cache entries that reference a deleted user even if they are cached under another user's scope.

**Recommendation:**
1. Document the semantic cache's data model clearly: is each cache entry keyed by (user_id, query_hash) or by (tenant_id, query_hash)? If tenant-scoped, user erasure has no effect unless the entire tenant's cache is also flushed.
2. Add a note that semantic cache entries should not store raw workspace content or session transcripts — only abstract embeddings or summarized responses — to limit PII exposure in the cache layer.
3. Specify the cache TTL relative to session lifetime: cache entries older than the session's artifact retention TTL should be eligible for eviction regardless of user erasure status.

---

## Info

### CMP-017 No Explicit Statement on Processor vs. Controller Status — GDPR Role Assignment Is Left to Deployers [Info]
**Section:** General

The spec does not address whether Lenny (as a platform) acts as a **data controller** or a **data processor** under GDPR Article 4. This distinction determines:
- Whether Lenny must maintain Records of Processing Activities (ROPA) under Article 30.
- Whether a Data Processing Agreement (DPA) is required between deployers and Anthropic (if Anthropic operates a hosted version) or between deployers and their customers.
- Whether Lenny's compliance interfaces (erasure, legal hold, residency) must be invocable directly by data subjects or only by the deployer (as controller).

For a self-hosted open-source platform, the deployer is typically the controller and Lenny's platform code is a tool — but this should be stated explicitly, particularly for regulated industries that require documented processor agreements.

**Recommendation:**
1. Add a "Compliance and Legal Notice" section to the documentation (outside the tech spec but referenced from it) clarifying that Lenny is a self-hosted platform and deployers act as data controllers. Anthropic is not a processor in self-hosted deployments.
2. For any future SaaS or managed-hosting offering, a published DPA and ROPA will be required.

---

### CMP-018 No HIPAA Business Associate Agreement (BAA) Guidance for PHI-Handling Deployments [Info]
**Section:** 12.8, 12.9

Section 12.9 defines T4 data as including "PHI-tagged workspace data" and allows tenants to elevate workspace classification to T4 for PHI handling. This is architecturally correct. However, the spec contains no guidance on:
- HIPAA Business Associate Agreement (BAA) requirements — any deployer processing PHI with Lenny is a covered entity or business associate, and their cloud infrastructure providers (AWS, GCP, Azure for managed services) must have signed BAAs in place.
- Minimum technical safeguards for HIPAA compliance beyond T4 classification: automatic logoff (session timeout), audit controls for PHI access, unique user identification, and emergency access procedures (§164.312).
- Which Lenny features map to HIPAA Technical Safeguards (§164.312): mTLS → encryption in transit; KMS → encryption at rest; audit logging → audit controls; user invalidation (Section 11.4) → automatic logoff and emergency access.

**Recommendation:**
1. Add a "Regulated Workloads" deployment guide section (referenced from Section 12.8 or 12.9) that maps Lenny features to HIPAA Technical Safeguard requirements.
2. Document the required cloud provider BAA requirement and note that the deployer is responsible for securing BAAs with all infrastructure providers.
3. Add a `workloadProfile: hipaa` Helm value preset that automatically configures: `workspaceTier: T4`, `audit.siem.endpoint` required (blocks startup if missing), extended audit retention (6 years), and session timeout policy aligned with HIPAA's automatic logoff requirement.
