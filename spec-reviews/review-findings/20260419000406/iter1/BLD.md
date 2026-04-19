# Build Sequence & Implementation Risk Review

## BLD-001 Phase Reference Mismatch: AuthEvaluator Phase Number [CRITICAL]

**Files:** `spec/18_build-sequence.md` (lines 21, 31)

In Phase 4.5, the spec states: "This deliverable is the named auth-complete milestone that Phase 7's `AuthEvaluator` depends on."

However, Phase 5.75 explicitly implements the `AuthEvaluator`: "Wire `AuthEvaluator` (JWT validation + `tenant_id` extraction, backed by Phase 4.5 authentication infrastructure)..."

The reference is incorrect. Phase 4.5 authentication infrastructure is a prerequisite for Phase 5.75's `AuthEvaluator`, not Phase 7's. Phase 7 implements broader policy controls (rate limits, delegation policy evaluation) but the `AuthEvaluator` itself is Phase 5.75.

**Recommendation:** Update Phase 4.5 milestone statement to say "This deliverable is the named auth-complete milestone that Phase 5.75's `AuthEvaluator` depends on."

---

## BLD-002 Phase 5.75 Dependency on Phase 4.5 Not Explicitly Stated [HIGH]

**Files:** `spec/18_build-sequence.md` (line 38)

Phase 5.75 states: "Wire `AuthEvaluator` (JWT validation + `tenant_id` extraction, backed by Phase 4.5 authentication infrastructure)..." but does not explicitly list Phase 4.5 as a prerequisite in a "Prerequisite" section like other phases do (e.g., Phase 5.5 lists Phase 5.4).

While the dependency is clear from the descriptive text, making it explicit would improve clarity and consistency with the pattern used elsewhere.

**Recommendation:** Add explicit prerequisite statement to Phase 5.75: "**Prerequisite:** Phase 4.5 (authentication infrastructure) must be complete before this phase begins."

---

## BLD-003 Echo Runtime Sufficiency Not Verified for Early Phases [MEDIUM]

**Files:** `spec/18_build-sequence.md` (phases 2–5.5)

Phases 2–5.4 reference the echo runtime for testing but do not explicitly confirm that the echo runtime (as defined in Phase 2) is sufficient for all testing in these phases. Phase 2.8 introduces `streaming-echo` with streaming and Full-level lifecycle support, which is critical for later phases (6–8).

The spec lacks an explicit statement confirming the basic echo runtime is adequate through Phase 5.4, or documenting any gaps that would block earlier testing.

**Recommendation:** Add a note after Phase 2 explicitly stating: "The basic echo runtime (Phase 2) is sufficient for all CI validation through Phase 5.5. `streaming-echo` (Phase 2.8) extends this with streaming output and Full-level lifecycle support required by Phase 6+ milestones."

---

No other real issues found. All section cross-references are valid, phase numbering is consistent, and dependency ordering is sound apart from the AuthEvaluator phase reference.
