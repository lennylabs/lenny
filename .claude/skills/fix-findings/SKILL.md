---
name: fix-findings
description: Fix a technical spec based on a list of review findings, one subagent per finding
argument-hint: <spec-file> <findings-file> [finding-id-or-range]
allowed-tools: Agent Bash Read Grep Glob
---

# Fix Technical Spec from Review Findings

You are given a technical specification and a document containing review findings. Your job is to apply fixes to the spec, one finding at a time, using a subagent for each finding.

## Inputs

- **Spec file**: `$0` — the technical specification to fix
- **Findings file**: `$1` — the review findings document (headings with description and `**Recommendation:**` blocks)
- **Scope** (optional): `$2` — a single finding ID (e.g. `KIN-001`), a comma-separated list (`KIN-001,KIN-002`), a category prefix (`KIN`), a severity filter (`Critical`, `High`), or a range (`KIN-001..KIN-005`). If omitted, process ALL findings.

## Procedure

### Step 1: Parse the findings

Read `$1` and extract every finding that matches the scope filter. Each finding has:

- **ID**: e.g. `KIN-001` (if provided)
- **Title**: heading text
- **Severity**: e.g. `[Critical]`, `[High]`, `[Medium]`, `[Low]`, `[Info]`
- **Description**: the body text explaining the problem
- **Recommendation**: the body text presenting the recommendation(s)

Build an ordered list of findings to process **in document order**. Report the list to the user with ID, title, and severity. Wait for confirmation before proceeding. If the user says to skip specific findings, remove them from the list.

### Step 2: Process each finding sequentially

For each finding, launch a **single subagent** using the Agent tool. Do NOT launch multiple finding-fix agents in parallel — fixes must be sequential because later fixes depend on earlier ones.

Give the subagent this exact prompt structure (fill in the placeholders):

---

**Your task**: Fix finding `{FINDING_ID}` in the technical spec.

**Spec file**: `$0`
**Findings file**: `$1`
**Finding ID**: `{FINDING_ID}`
**Finding title**: `{FINDING_TITLE}`
**Severity**: `{SEVERITY}`
**Spec section(s)**: `{SECTION_REF}`
**Problem description**: `{DESCRIPTION}`
**Recommended fix**: `{RECOMMENDATION}`

## Instructions

You MUST follow these steps in exact order. Do not deviate or combine steps.

### Step 1 — Validate

Read the relevant section(s) of the spec file. Determine whether the finding is still valid and unaddressed in the current state of the document. The spec may have been modified by previous fixes in this session.

- If the finding is **already resolved** (by a prior fix or because the description doesn't match reality): mark the finding **"Already Fixed"** in `$1` with a one-sentence explanation and **stop processing this finding**.
- If the finding is **partially resolved**: note what remains and proceed to Step 2 for the remaining issue only.
- If the finding is **still valid**: proceed to Step 2.
- If the finding was previously reported in previous finding reports and skipped or deferred, mark the finding as **"Skipped"** in `$1` with a one sentence explanation and **stop processing this finding**.

### Step 2 — Decompose

Break the finding down thoroughly:

- Identify which concerns are genuine correctness/reliability/security issues
- Identify which concerns are style preferences, nice-to-haves, or over-engineering
- List all affected components, interfaces, and contracts in the technical design
- Go deeper than the recommendation in the findings doc — surface root causes and second-order effects

### Step 3 — Generate Alternatives

Propose distinct solutions. For each, briefly note tradeoffs (complexity, risk, scope of change).

### Step 4 — Select Best Solution

Choose the simplest solution that addresses only the real problems identified in Step 2. The tech spec is already mature — think critically before acting. Challenge the finding itself and the original recommendation. Bias strongly toward minimal, targeted fixes over refactoring or enhancement.

- If the best solution is **"do nothing"** (e.g., the concern is a nice-to-have or the risk is acceptable): mark the finding **"Skipped"** in `$1`, update the finding's recommendation field with your rationale, and **stop processing this finding**. Do NOT modify the spec file.

### Step 5 — Gate Check (before touching any files)

Ask: Does the selected solution introduce any of the following?

- An architectural change
- A change to the project's external capabilities or runtime contracts

If **yes** to either: mark the finding **"Deferred - Input Required"** in `$1`, update the finding's recommendation with the selected solution from Step 4, and **stop processing this finding**. Do NOT modify the spec file.

If **no**: proceed to Step 6.

### Step 6 — Implement

Apply the fix directly in the spec file. Be surgical — only change what is necessary to address the finding.

Rules:

- Make the minimum edit necessary to resolve the finding. Do not rewrite surrounding prose that isn't broken.
- Preserve the document's existing style, tone, and formatting conventions.
- If the fix requires adding a new section or subsection, place it in the most logical location and update any table of contents or cross-references.
- If the fix requires removing content, ensure no dangling cross-references remain.
- If the recommendation is to "document X", add the missing specification text — do not just add a TODO or placeholder.
- If the finding is about an inconsistency between two sections, fix both sections to be consistent.
- Do not add speculative content beyond what the fix calls for.

### Step 7 — Regression Check

Re-read the modified sections AND any sections that cross-reference them. Verify no existing behavior, constraint, or contract was inadvertently broken. Check for:

1. **Internal consistency**: Do the changes contradict anything else in the spec?
2. **Cross-reference integrity**: Are all section references still valid? Did any referenced content move or get renamed?
3. **Formatting**: Is the markdown well-formed? Are heading levels, list indentation, and code blocks intact?
4. **Scope creep**: Did you accidentally change something unrelated to this finding?

- If regressions found: revert the change and return to Step 2 with the new information.
- If clean: mark the finding **"Fixed"** in `$1` and append a precise description of what was changed and why.

---

Report your result as:

```
FINDING: {FINDING_ID}
STATUS: Fixed | Skipped | Deferred - Input Required | Already Fixed
CHANGES: <brief summary of what was changed, or why it was skipped/deferred>
SECTIONS MODIFIED: <list of section numbers touched, or "None">
REGRESSION CHECK: PASS | <description of issue found and fixed>
```

---

### Step 3: After each subagent completes

Record the subagent's result.

Print a progress line:

```
[N/TOTAL] FINDING_ID — STATUS — brief summary
```

### Step 4: After all findings are processed

Print a summary table:

```
| # | Finding | Severity | Status | Sections Modified |
|---|---------|----------|--------|-------------------|
```

If any findings were `Skipped`, `Deferred - Input Required`, or `Already Fixed`, list them separately with explanations.

## Status values

Use exactly these strings: `Fixed` | `Skipped` | `Deferred - Input Required` | `Already Fixed`

## Important constraints

- **Document order is mandatory.** Process findings strictly in the order they appear in the findings document.
- **Sequential execution is mandatory.** Each fix can change the spec in ways that affect subsequent findings. Never run finding-fix subagents in parallel.
- **Never skip validation.** A finding that was valid when the review was written may already be resolved by an earlier fix in this run. Each sub-agent operates independently — re-read source files fresh at Step 1.
- **Never skip the regression check.** Even a small edit can break cross-references or introduce contradictions.
- **Do not modify the spec file for Skipped or Deferred findings.** Only `Fixed` (and regression-fix) statuses result in spec edits.
- **Bias toward minimal fixes.** The tech spec is mature. Challenge findings and recommendations — prefer doing nothing over over-engineering.
- **Remember to update the finding status and details of the implemented solution, if applicable, in the findings file.**
