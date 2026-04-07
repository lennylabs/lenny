# Technical Design Review Findings — Document Quality, Consistency & Completeness (DOC)

**Document reviewed:** `docs/technical-design.md`
**Perspective:** 22. Document Quality, Consistency & Completeness
**Reviewer focus:** Meta-review of the spec as a document — structural issues, internal consistency, editorial quality, cross-reference validity.
**Date:** 2026-04-04
**Total findings:** 22

---

## Findings Summary

| Severity | Count |
|----------|-------|
| High     | 5     |
| Medium   | 10    |
| Low      | 5     |
| Info     | 2     |

---

## High Findings

### DOC-001 Section 11.8 is an empty redirect that adds no value [High] — Fixed

**Section:** 11.8 (line 3067)

Section 11.8 is titled "Billing Event Stream" and contains only a single sentence: *"See Section 11.2.1 for the authoritative billing event stream specification."* It occupies numbered heading space in the table of contents and implies content that does not exist here. Readers scanning the ToC or jumping to Section 11 for billing information will hit a dead end.

The billing event stream is substantively defined at Section 11.2.1 (lines 2925–2976), which lives inside Section 11.2 (Budgets and Quotas). This placement is itself awkward: billing infrastructure does not belong as a subsection of the quota section.

**Recommendation:** Either (a) relocate Section 11.2.1 content to replace the empty 11.8, making billing its own peer section under 11, or (b) remove 11.8 entirely and update the forward reference in 11.2.1 to note that it is the authoritative billing section. Also update the cross-reference in Section 12.8 (line 3273) which cites `Section 11.8` — that reference would need to point to 11.2.1. Do not leave a section whose entire content is a pointer to another section.

**Resolution:** Removed Section 11.8 entirely. Updated two cross-references that cited "Section 11.8" (in the tenant-controlled billing erasure paragraph and the erasure propagation to external sinks paragraph) to point to Section 11.2.1 instead. No other references to Section 11.8 remain in the document.

---

### DOC-002 Section 17.9 appears twice with different content [High] — Already Fixed

**Section:** 17.9 (lines 4913 and 5022)

Section 17 contains two subsections numbered 17.9:

- **First 17.9** at line 4913: "Operational Defaults — Quick Reference" — a table of default values (artifact retention TTL, checkpoint retention, GC cycle interval, etc.)
- **Second 17.9** at line 5022: "Deployment Profiles" — cloud-managed vs self-managed profile descriptions with Helm YAML examples

The section between them, 17.8 (Capacity Tier Reference, line 4933), is also out of order: 17.9 (Operational Defaults) appears before 17.8 (Capacity Tier Reference) in the file, producing the sequence 17.7 → **17.9** → 17.8 → **17.9**. The numbering is internally inconsistent and the ToC implied by the headings is incorrect.

The 17.8 section (lines 4978–5001) cross-references itself as "Section 17.8" and is referenced by other sections (e.g., Section 4.1 line 121, Section 4.6.1 line 344). The Operational Defaults table (first 17.9) cross-references its own section numbers.

**Recommendation:** Renumber to: 17.8 = Capacity Tier Reference, 17.9 = Operational Defaults — Quick Reference, 17.10 = Deployment Profiles. Update all internal cross-references that cite these sections. Verify the resulting sequence is: 17.1 → 17.2 → 17.3 → 17.4 → 17.5 → 17.6 → 17.7 → 17.8 → 17.9 → 17.10.

**Resolution:** Already fixed by a prior iteration. The duplicate 17.9 and out-of-order 17.8 have been resolved by restructuring the sections as 17.8.1 (Operational Defaults — Quick Reference), 17.8.2 (Capacity Tier Reference), and 17.9 (Deployment Profiles). The current sequence is 17.1 through 17.7, then 17.8.1, 17.8.2, 17.9 — no duplicates, correct ordering. Cross-references to "Section 17.8" naturally encompass both subsections. The approach differs from the recommendation (flat 17.8/17.9/17.10) but achieves the same structural correctness.

---

### DOC-003 Missing section 8.7 breaks the delegation section numbering sequence [High] — Fixed

**Section:** 8 (Recursive Delegation)

Section 8 has subsections 8.1, 8.2, 8.3, 8.4, 8.5, 8.6, then jumps to **8.8**. Section 8.7 does not exist. The delegation section sequence at lines 1678–2270 goes: 8.1, 8.2, 8.3, 8.4, 8.5, 8.6, 8.8, 8.9, 8.10, 8.11. There is no 8.7.

This gap is confusing for readers navigating the document and suggests either a section was deleted without renumbering, or a planned section (possibly the "Delegation Budget Reservation Model" content that is currently inline within 8.3) was never extracted.

**Recommendation:** Either (a) introduce a Section 8.7 containing the Budget Reservation Model content currently embedded in Section 8.3 (lines 1839–1849), which would improve readability and give that important content a navigable header, or (b) renumber 8.8 → 8.7, 8.9 → 8.8, 8.10 → 8.9, 8.11 → 8.10, and update all cross-references. Option (a) is preferred as it also addresses the readability of the embedded budget reservation content.

**Resolution:** Renumbered sections 8.8 through 8.11 to 8.7 through 8.10, closing the gap. Updated all cross-references throughout the document: "Section 8.8" (File Export) became 8.7, "Section 8.9" (TaskRecord) became 8.8, "Section 8.10" (Task Tree) became 8.9, "Section 8.11" (Delegation Tree Recovery) became 8.10. Verified no stale references to old section numbers remain.

---

### DOC-004 Undefined shorthand references "Sec-H1", "Sec-H5", "DevOps-M2", "OSS-L2", "OSS-L3", "Sec-C2" used without definition [High] — Fixed

**Sections:** Multiple (lines 2919, 3169, 4610, 4766, 4774, 4780, 4911)

The document uses internal tracking codes that are never defined within the document itself:

- `DevOps-M2` — appears at line 2919 in a parenthetical: *"bounded per DevOps-M2"*
- `Sec-H5` — appears at line 3169 and line 4610 in an alert condition: *"per Sec-H5"*
- `Sec-H1` — appears at line 4780: *"TLS bypass, JWT signing bypass (see Sec-H1)"*
- `Sec-C2` — appears as a section header label at line 4774: *"Dev mode guard rails (Sec-C2)"*
- `OSS-L2` — appears at line 4766 as a section header label: *"Observability in dev mode (OSS-L2)"*
- `OSS-L3` — appears at line 4911 in a note: *"Note (OSS-L3):"*

These appear to be references to findings or requirements from external review documents (e.g., `review-findings-20260404.md`, `review-findings-sec-20260404.md`), not to sections of this document. They are opaque to any reader who does not have access to those external documents, and they create a hidden dependency between this spec and external review artifacts.

**Recommendation:** Either (a) remove these parenthetical codes from the design document — they are internal tracking artifacts that do not belong in a technical spec intended for community consumption, or (b) add a glossary section (or appendix) that defines each code and links it to its originating finding. The design document should be self-contained; requirements should be stated directly rather than by reference to opaque codes.

**Resolution:** Removed all 7 instances of external tracking codes from the document. Specifically: removed `DevOps-M2` from quota update timing (replaced parenthetical with descriptive text referencing the per-replica ceiling formula), removed two `Sec-H5` references (from Redis fail-open description and alerting table), removed `OSS-L2` from the dev mode observability heading, removed `Sec-C2` from the dev mode guard rails heading, removed `Sec-H1` from the unified security-relaxation gate paragraph, and removed `OSS-L3` from the operator skill tiers note. In each case the surrounding prose remains clear and self-contained without the opaque codes.

---

### DOC-005 "Section 4.5" cross-referenced from two different sections does not exist [High] — Fixed

**Sections:** Multiple (lines 5153, 5241)

Two locations cite "Section 4.5" in contexts that do not match any actual section:

- Line 5153 (Build Sequence, Phase 13.5): *"pod claim + workspace materialization latency under concurrent session creation (target: P99 from Section 4.5)"*
- Line 5241 (Competitive Landscape): *"Lenny's pre-warmed pod pools (Section 4.5) target similar latency…"*

Section 4 has subsections 4.1 through 4.9 (in the form 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.9). However, **Section 4.5 is the Artifact Store** (line 236), which contains no P99 latency targets and no pre-warmed pool discussion. The intended target is likely Section 6.3 (Startup Latency Analysis, line 1321) or Section 16.5 (Alerting Rules and SLOs, line 4588), both of which contain startup latency SLO targets.

**Recommendation:** Correct both references to point to Section 6.3 (for startup latency analysis and estimates) or Section 16.5 (for the formal SLO targets). Verify no other stale "Section 4.5" references exist by searching the document.

**Resolution:** Corrected both incorrect "Section 4.5" references to "Section 6.3" (Startup Latency Analysis). The Phase 13.5 build sequence P99 latency target now cites Section 6.3, and the Competitive Landscape Modal comparison now cites Section 6.3. The one remaining "Section 4.5" reference (line 1687, about `parent_workspace_ref` lineage pointer) correctly refers to the Artifact Store and was left unchanged.

---

## Medium Findings

### DOC-006 Section 20 (Open Questions) is intentionally empty but the signal is unclear [Medium]

**Section:** 20 (line 5185)

Section 20 is titled "Open Questions" and contains exactly two sentences: *"All open questions have been resolved. See Section 19 for decisions."* This is a legitimate state — all questions are resolved — but it wastes a numbered section heading for a two-sentence acknowledgment.

The section was presumably kept to maintain a stable section number (Section 21 references depend on 20 existing) and to confirm the resolved state explicitly. However, readers who navigate to Section 20 expecting active design questions to engage with will be confused.

**Recommendation:** Replace the two-sentence placeholder with a note block that explicitly states the intentional status and points forward: *"> **Note:** This section is intentionally empty. All open design questions have been resolved and are documented in Section 19 (Resolved Decisions). New open questions arising from implementation should be tracked in GitHub issues rather than this document."* Alternatively, consider removing the section and re-routing the "See Section 19" pointer as a sentence within Section 19 itself, then renumbering 21 → 20, 22 → 21, etc.

---

### DOC-007 "Section 13.4" cited in preflight check does not match the actual section title [Medium]

**Section:** 17.6 (line 4878), Section 13.4 (line 3525)

The preflight validation table at line 4878 includes: *"CNI plugin does not support NetworkPolicy; required for agent pod isolation (Section 13.4)"*. Section 13.4 is titled **"Upload Security"** (line 3525), which covers upload validation rules, not network isolation or NetworkPolicy.

The actual NetworkPolicy content lives in Section 13.2 (Network Isolation, line 3384), which contains the agent namespace NetworkPolicy manifests and CNI requirements.

**Recommendation:** Correct the cross-reference in the preflight table from "Section 13.4" to "Section 13.2". Audit other cross-references to Section 13.x for similar mismatches.

---

### DOC-008 Section 15.4 cited in Runtime Author Roadmap does not correspond to actual section [Medium]

**Section:** 15.4.5 (lines 4427–4449)

The Runtime Author Roadmap at lines 4427–4449 cites "Section 15.4.4" as "Sample Echo Runtime" (correct) but then cites "Section 15.4.1" at step 2 as *"Adapter↔Binary Protocol"* (correct) and references other subsections correctly. However, step 3 cites *"Section 15.4.2 — RPC Lifecycle State Machine"* while the actual section at line 4322 is correctly named. Step 4 cites *"Section 15.4.3 — Runtime Integration Tiers"* — also correct. These are consistent.

The issue is that Section 4.7 is separately titled "Runtime Adapter" and contains overlapping content with 15.4. The roadmap at step 7 sends Standard-tier authors to *"Section 4.7 — Runtime Adapter"* and step 8 to *"Section 9.1 — MCP Integration"*. Section 4.7 contains the adapter manifest, RPC table, and startup sequence; Section 15.4 contains the binary protocol, message schemas, and integration tiers. The split between these two sections is not explained anywhere, and readers moving between them must infer which section to consult for which aspect of the adapter contract.

**Recommendation:** Add a navigational note at the start of Section 4.7 (and Section 15.4) that explains the division of content: "Section 4.7 covers the gateway↔adapter gRPC contract and internal adapter architecture. Section 15.4 covers the adapter↔agent binary stdin/stdout protocol and integration tiers intended for runtime authors." This reduces confusion without requiring a structural reorganization.

---

### DOC-009 "Section 4.5" cited as "Startup Latency Analysis" in Section 15.4.5 runtime author roadmap [Medium]

**Section:** 15.4.5 (line 4433)

The Runtime Author Roadmap step 5 reads: *"Section 6.4 — Pod Filesystem Layout."* This reference is correct. However, the roadmap does not reference Section 6.3 (Startup Latency Analysis), which is highly relevant to runtime authors benchmarking their adapters. The roadmap also does not reference Section 4.7's startup sequence (lines 503–512), which describes the exact initialization sequence a runtime must participate in.

This is a completeness gap in the roadmap rather than a broken reference, but it means runtime authors following the roadmap will miss the startup sequence specification.

**Recommendation:** Add to the Minimum-tier reading list: *"Section 4.7 (Startup Sequence for type: agent Runtimes) — the exact initialization handshake your adapter participates in"* and to the Full-tier list: *"Section 6.3 — Startup Latency Analysis — latency benchmarks for pod-warm vs SDK-warm to understand performance implications of your integration tier choice."*

---

### DOC-010 Section 12.4 cited for "audit event schema" in credential leasing section — wrong target [Medium]

**Section:** 4.9 (line 767)

Line 767 reads: *"The audit event `credential.leased` (Section 12.4) includes a `deliveryMode` field…"* Section 12.4 is titled "Redis HA and Failure Modes" and covers Redis topology, failure behavior, and fail-open windows. It contains no audit event schema.

Audit events are defined in Section 11.7 (Audit Logging, line 3043). The billing event stream (which includes `credential.leased`) is at Section 11.2.1 (line 2925).

**Recommendation:** Correct the reference from "Section 12.4" to "Section 11.2.1" (which defines the `credential.leased` billing event type and its fields, including `credential_id` and `credential_pool_id` but not explicitly `deliveryMode` — so this also exposes a content gap where `deliveryMode` is referenced as a field but not listed in the Section 11.2.1 event schema table).

**Secondary finding:** The `deliveryMode` field is described as being on the `credential.leased` billing event at line 767 but does not appear in the Section 11.2.1 event schema table (lines 2941–2957). The schema table should be updated to include `deliveryMode` as a field on `credential.leased` events.

---

### DOC-011 `ReportUsage` RPC referenced but absent from the adapter RPC table [Medium]

**Section:** 11.2 (line 2919), Section 4.7 (lines 413–429)

Section 11.2 (Quota Update Timing, line 2919) states: *"The runtime adapter extracts token counts from LLM provider responses and reports them to the gateway via the `ReportUsage` RPC (Section 4.7)."* Section 4.7 contains the canonical RPC table at lines 413–429, which lists 14 RPCs (`PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `StartSession`, `ConfigureWorkspace`, `DemoteSDK`, `Attach`, `Interrupt`, `Checkpoint`, `ExportPaths`, `AssignCredentials`, `RotateCredentials`, `Resume`, `Terminate`). `ReportUsage` is not in this table.

This is either (a) a missing RPC that should be in the table, or (b) the usage reporting happens via a different mechanism that is not called `ReportUsage`. If it is a gRPC RPC, it must be in the contract table. If it is a streaming update on the existing `Attach` channel, that should be stated.

**Recommendation:** Add `ReportUsage` to the RPC table in Section 4.7 with its description, or correct Section 11.2 to describe the actual mechanism (e.g., "reported via the `ReportUsage` field in the `Attach` streaming response" or similar). This is a contract gap for runtime adapter authors.

---

### DOC-012 Section numbering within Section 4.6 is inconsistent — 4.6.3 has no parallel at document level [Medium]

**Section:** 4.6 (lines 256–405)

Section 4.6 has three subsections: 4.6.1 (Warm Pool Controller), 4.6.2 (PoolScalingController), and 4.6.3 (CRD Field Ownership and Write Boundaries). Sections 4.6.1 and 4.6.2 describe distinct components with their own roles. Section 4.6.3 describes the interaction protocol between those two components. This hierarchical structure is logical.

However, within 4.6.1 the document uses `####` fourth-level headers (e.g., "Gateway Internal Subsystems" within 4.1 uses `####` correctly), but 4.6.1 itself contains an embedded ADR notice, an interface definition block, a CRD mapping table, and extensive operational detail that makes it the longest subsection in the document (~150 lines). The section has grown into a de facto specification appendix for the Kubernetes integration layer.

**Recommendation:** This is primarily a readability issue rather than a numbering error, but it warrants extraction. Consider splitting 4.6.1 into: 4.6.1 (Role and Responsibilities — brief), 4.6.1.x (Interface Definitions — PodLifecycleManager, PoolManager), and 4.6.1.y (CRD Mapping from agent-sandbox). The current length makes it difficult to navigate. Alternatively, move the interface definitions to a new Section 4.6.4 (Interface Definitions) to match the pattern of 4.6.3.

---

### DOC-013 Document length (~5,277 lines) warrants extraction of several sections [Medium]

**Section:** Entire document

At 5,277 lines, the document is at the edge of practical readability for a single Markdown file. Several sections have grown into standalone specifications:

1. **Section 15.4** (Runtime Adapter Protocol, lines 4061–4421) — 360 lines covering the binary protocol, message schemas, tier comparison matrix, echo runtime, and author roadmap. This is explicitly called out as "a standalone specification" at line 4048 but remains embedded.
2. **Section 17.8** (Capacity Tier Reference, ~70 lines) + **Section 17.9** (Deployment Profiles, ~80 lines) together form an operational reference guide separate from the design narrative.
3. **Section 23** (Competitive Landscape + Community Strategy, lines 5229–5277) is marketing/positioning content, not technical design.

The document also mixes three distinct readership audiences inline: runtime adapter authors, platform operators, and Kubernetes cluster admins — these are explicitly acknowledged in Section 17.7 (line 4911) but not structurally separated.

**Recommendation:** Extract Section 15.4 into `docs/runtime-adapter-spec.md` (referenced from 15.4 as "see the runtime adapter specification"). Extract Section 23 into a separate community/competitive document. Consider splitting the remaining document into `technical-design.md` (design decisions, components, protocols) and `operations-guide.md` (Sections 17.x, capacity sizing, deployment profiles, runbooks). This reduces cognitive load and matches the document's own statement at line 4048 that the adapter spec "will be published as a standalone specification."

---

### DOC-014 Section 21.5 cross-referenced for a feature (dryRun preview) that is already defined in Section 15.1 [Medium]

**Section:** 15.1 (line 3909), Section 21.5 (line 5203)

The admin API table at line 3909 includes: *"`PUT /v1/admin/environments` — Validates update, previews selector matches (Section 21.5)"*. Section 21.5 is titled "Environment Management UI" (line 5203) and describes a future web UI. It does not define the preview mechanism.

The `?dryRun=true` preview mechanism is fully specified within Section 15.1 (lines 3892–3917) for all admin endpoints including environments. The reference to Section 21.5 is misleading — it implies the preview semantics are defined there when they are actually defined in the same section the reader is already in.

**Recommendation:** Remove the "Section 21.5" reference from the environments row of the admin API table in Section 15.1. The dryRun semantics are already documented on the same page. If the intention was to cross-reference the future UI, add a separate note rather than inline in the API table row.

---

### DOC-015 Section 9 (MCP Integration) and Section 4.7 (Runtime Adapter) contain duplicate MCP tool lists [Medium]

**Sections:** 4.7 (line 452), 9.1 (lines 2288–2303), 8.5 (lines 1893–1903)

The platform MCP server tools appear in three locations:

1. **Section 4.7** (line 452): inline list within the adapter manifest description — `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`
2. **Section 8.5** (lines 1893–1903): Delegation Tools table — same tools with descriptions
3. **Section 9.1** (lines 2288–2303): Platform MCP Server Tools table — same tools again with descriptions

All three lists are slightly inconsistent: the tool sets differ between Sections 4.7, 8.5, and 9.1. If 4.7 is the authoritative adapter manifest list, it should be complete.

**Recommendation:** Designate Section 9.1 as the single authoritative tool catalog. Replace the lists in Sections 4.7 and 8.5 with "see Section 9.1 for the complete platform MCP server tool catalog." This eliminates three-way synchronization burden and the current inconsistencies.

---

## Low Findings

### DOC-016 Section 7.1 contains a spurious blank code block [Low]

**Section:** 7.1 (lines 1450–1452)

After the "Seal-and-export invariant" note at line 1448, lines 1450–1452 contain:

```
(blank line)
```
(empty code block)
```
(blank line)
```

This is an empty fenced code block that renders as blank whitespace in Markdown and appears to be a copy/paste artifact or editing remnant.

**Recommendation:** Delete the empty code block at lines 1450–1452.

---

### DOC-017 Document status is "Draft" but the date is two days before the review date [Low]

**Section:** Header (line 2)

The document header reads: `**Status:** Draft` and `**Date:** 2026-04-02`. The review perspective document header indicates the current date is 2026-04-04. This is a minor discrepancy — drafts accumulate edits after their initial date — but for a document of this maturity and detail level, the `Draft` status may mislead readers about its completeness and stability. The document contains fully specified schemas, concrete API contracts, and cross-referenced SLOs at a level more consistent with `In Review` or `Approved` status.

**Recommendation:** Update the status to reflect the current review state (e.g., `In Review` or `Under Technical Review`). Update the date to the last significant edit date. Consider adding a `Version` field to track the document's revision history.

---

### DOC-018 Section 5 is referenced inconsistently as "Section 5" vs. "Section 5.x" [Low]

**Sections:** Multiple

The document contains several references to "Section 5" without a subsection qualifier in contexts where a specific subsection is clearly intended:

- Line 5240: *"Lenny's gateway-mediated delegation (Section 5)"* — Section 5 is "Runtime Registry and Pool Model," which does not cover delegation. The intended reference is Section 8 (Recursive Delegation).
- Line 5250: *"enforced scope, token budget, and lineage tracking (Section 5, Principle 5)"* — "Principle 5" appears to reference the Core Design Principles list in Section 1, not Section 5. Section 5 contains no "Principle 5."

These are in the Competitive Landscape and Why Lenny sections (Section 23), which are less rigorously reviewed than the core technical sections, but incorrect cross-references in customer-facing positioning content can undermine credibility.

**Recommendation:** Correct line 5240's "Section 5" to "Section 8" (Recursive Delegation). Correct line 5250's "(Section 5, Principle 5)" to "(Section 1 — Core Design Principle 5, Section 8)".

---

### DOC-019 Several terms used before they are defined [Low]

**Sections:** Multiple early sections

Several terms appear in early sections before their formal definition:

- **`executionMode`** — first used at line 35 (Goals section) without definition; formally defined at Section 5.2 (line 1048).
- **`SandboxClaim`** — first used at line 169 (Session Manager) before the CRD mapping table at Section 4.6.1 (line 303).
- **`DelegationPolicy`** — first used at line 165 (Session Manager) before its formal definition at Section 8.3 (line 1766).
- **`LeaseSlice`** — used at line 1694 within the `delegate_task` signature before its field table at line 1709. (This is acceptable sequential definition within a section.)
- **`MessageEnvelope`** — used at line 1456 (Section 7.2) before its definition at Section 15.4.1 (line 4168).

For a specification document this size, some forward references are unavoidable. However, undefined terms in early sections (Goals, Components overview) that are only defined much later create a comprehension barrier for first-time readers.

**Recommendation:** Add a "Key Terms and Concepts" section (or a glossary appendix) that defines the primary platform concepts (`Runtime`, `Pool`, `Session`, `Task`, `DelegationLease`, `executionMode`, `WorkspacePlan`, `CredentialLease`) at first use. At minimum, add an `(see Section X.Y)` inline reference when these terms first appear in early sections.

---

### DOC-020 The `Section 4.5` warm pool description note is misattributed [Low]

**Section:** Build Sequence note (line 5129)

The Build Sequence note at line 5129 reads: *"> **Note:** Digest-pinned images from a private registry are required from Phase 3 onward. Full image signing and attestation verification (Sigstore/cosign + admission controller) is Phase 14."*

This note appears as a blockquote between Phase 3.5 and Phase 4 table rows. In Markdown table rendering, this interrupts the table continuation. The second table segment at line 5132 restarts a table with the same column headers as the first segment (line 5122). Readers may not realize these are the same continuous build sequence table.

**Recommendation:** Convert the note into a table row annotation or move it to a paragraph after the table. Alternatively, merge the two build sequence table fragments into a single table and place the note as a caption or post-table paragraph.

---

## Info Findings

### DOC-021 Section 23.1 "Why Lenny?" contains several forward-looking claims without substantiation [Info]

**Section:** 23.1 (lines 5244–5256)

The "Why Lenny?" section makes five differentiation claims, each supported by a section citation. All five citations are valid (Sections 15.4, 5/8, 17, 15/3, 2/8/16). The citations correctly ground each claim in the spec. This is good practice.

However, claims 1, 3, and 5 are stated as "architectural commitments, not roadmap aspirations" (line 5246). Claim 3 (self-hosted, Kubernetes-native) is fully substantiated. Claim 1 (runtime-agnostic adapter contract) is substantiated but the specification also acknowledges that the standalone runtime adapter specification document does not yet exist (Section 15.4, line 4048). Claim 5 (enterprise controls) references features like per-hop budget enforcement that are scheduled as Phase 9–11 deliverables.

**Recommendation:** Add a note in Section 23.1 indicating which differentiators are present in v1 vs. which phases they are completed in, referencing Section 18. This prevents the section from being read as a current capability list by readers who do not consult the build sequence.

---

### DOC-022 ADR references are placeholders — no ADR index exists yet [Info]

**Section:** 19 (line 5183), Section 4.6.1 (line 312)

Section 19 states: *"full Architecture Decision Records (ADRs)…will be maintained in `docs/adr/` as separate documents following the MADR format, with this table serving as an index."* Section 4.6.1 (line 312) contains: *"ADR required (ADR-TBD): SandboxClaim optimistic-locking verification."*

The `docs/adr/` directory does not yet exist (it would appear in the glob results if it did). The ADR table in Section 19 is the only index, and there is currently one pending ADR identified inline (ADR-TBD for SandboxClaim) with no assigned number.

This is expected for a draft document but the promises are concrete enough to warrant tracking.

**Recommendation:** Create `docs/adr/` with a `README.md` listing the planned ADRs, including the ADR-TBD for SandboxClaim from Section 4.6.1. Assign it a number (e.g., `ADR-001`). This converts the promise in Section 19 into a concrete artifact before implementation begins.
