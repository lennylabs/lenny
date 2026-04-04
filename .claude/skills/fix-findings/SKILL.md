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
- **Findings file**: `$1` — the review findings document headings with description and `**Recommendation:**` blocks)
- **Scope** (optional): `$2` — a single finding ID (e.g. `KIN-001`), a comma-separated list (`KIN-001,KIN-002`), a category prefix (`KIN`), a severity filter (`Critical`, `High`), or a range (`KIN-001..KIN-005`). If omitted, process ALL findings.

## Procedure

### Step 1: Parse the findings

Read `$1` and extract every finding that matches the scope filter. Each finding has:

- **ID**: e.g. `KIN-001` (if provided)
- **Title**: heading text
- **Severity**: e.g. `[Critical]`, `[High]`, `[Medium]`, `[Low]`, `[Info]`
- **Description**: the body text explaining the problem
- **Recommendation**: the body text presenting with the recommendation(s)

Build an ordered list of findings to process. Sort by severity (Critical first, then High, Medium, Low, Info), preserving document order within each severity. Then adjust ordering based on what you think is the most appropriate implementation sequence (goal: minimize refactoring as we progress through the list of findings)

Report the list to the user with ID, title, and severity. Wait for confirmation before proceeding. If the user says to skip specific findings, remove them from the list.

### Step 2: Process each finding sequentially

For each finding, launch a **single subagent** using the Agent tool. Do NOT launch multiple finding-fix agents in parallel — fixes must be sequential because later fixes depend on earlier ones.

Give the subagent this exact prompt structure (fill in the placeholders):

---

**Your task**: Fix finding `{FINDING_ID}` in the technical spec.

**Spec file**: `$0`
**Finding ID**: `{FINDING_ID}`
**Finding title**: `{FINDING_TITLE}`
**Severity**: `{SEVERITY}`
**Spec section(s)**: `{SECTION_REF}`
**Problem description**: `{DESCRIPTION}`
**Recommended fix**: `{RECOMMENDATION}`

## Instructions

You MUST follow these three phases in order.

### Phase 1: Verify the finding

Read the relevant section(s) of the spec file. Elaborate on the finding and confirm that the problem described in the finding actually exists in the current state of the document. The spec may have been modified by previous fixes in this session.

- If the finding is **still valid**: proceed to Phase 2.
- If the finding is **already resolved** (by a prior fix or because the description doesn't match reality): report `SKIPPED — already resolved` and explain why. Do NOT make any edits to the technical spec.
- If the finding is **partially resolved**: note what remains and proceed to Phase 2 for the remaining issue only.

### Phase 2: Apply the fix

Edit the spec file to address the finding. Review the recommendations and come up with alternatives if needed. Follow the recommendation as closely as possible, but use your judgment — the recommendation is guidance, not a script. In general, the goal is to find the simplest possible solution that aligns with the intent of the technical spec and isn't a hack.

Rules:

- Make the minimum edit necessary to resolve the finding. Do not rewrite surrounding prose that isn't broken.
- Preserve the document's existing style, tone, and formatting conventions.
- If the fix requires adding a new section or subsection, place it in the most logical location and update any table of contents or cross-references.
- If the fix requires removing content, ensure no dangling cross-references remain.
- If the recommendation is to "document X", add the missing specification text — do not just add a TODO or placeholder.
- If the finding is about an inconsistency between two sections, fix both sections to be consistent.
- Do not add speculative content beyond what the recommendation calls for.
- Find the simplest possible solution that aligns with the intent of the technical spec and isn't a hack. Try to minimize impact to other parts of the design.
- Keep the technical spec's design principles in mind at all times.
- Update the finding's list of recommendations in `$1` if needed.
- If multiple valid options exist and there isn't a clear winner, or if the recommendation requires major architectural changes, provide enough context to the user and ask them to select a path forward. Defer more or less to the user depending on the severity of the finding.

### Phase 3: Validate no regressions

After editing, re-read the sections you modified AND any sections that cross-reference them. Check for:

1. **Internal consistency**: Do the changes contradict anything else in the spec?
2. **Cross-reference integrity**: Are all section references (`Section X.Y`) still valid? Did any referenced content move or get renamed?
3. **Formatting**: Is the markdown well-formed? Are heading levels, list indentation, and code blocks intact?
4. **Scope creep**: Did you accidentally change something unrelated to this finding?

If you find a regression, fix it before reporting.

Report your result as:

```
FINDING: {FINDING_ID}
STATUS: FIXED | SKIPPED | PARTIAL
CHANGES: <brief summary of what was changed, or why it was skipped>
SECTIONS MODIFIED: <list of section numbers touched>
REGRESSION CHECK: PASS | <description of issue found and fixed>
```

Update the status of the finding in `$1`, clearly indicating what the resolution was in detail.

---

### Step 3: After each subagent completes

Record the subagent's result. If the status is `PARTIAL`, note what remains unresolved.

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

If any findings were `PARTIAL` or `SKIPPED`, list them separately with explanations.

## Important constraints

- **Sequential execution is mandatory.** Each fix can change the spec in ways that affect subsequent findings. Never run finding-fix subagents in parallel.
- **Never skip the verification phase.** A finding that was valid when the review was written may already be resolved by an earlier fix in this run.
- **Never skip the regression check.** Even a small edit can break cross-references or introduce contradictions.
- If a finding's recommendation is vague or would require major architectural decisions, ask the user for input.
