---
name: review-and-fix
description: Review a technical spec against guidelines, generate findings, fix them, and iterate until clean
argument-hint: <spec-file> <guidelines-file> [max-iterations]
allowed-tools: Agent Bash Read Write Edit Grep Glob Skill
---

# Review and Fix Loop

You are given a technical specification and a review guidelines file. Your job is to iteratively review the spec, generate findings, fix them, and re-review until the spec is clean or a maximum number of iterations is reached.

## Inputs

- **Spec file**: `$0` — the technical specification to review and fix
- **Guidelines file**: `$1` — the review perspectives/guidelines document (defines the review framework)
- **Max iterations** (optional): `$2` — maximum review-fix cycles (default: 3)

## Output files

Each iteration produces a findings file:

- Iteration 1: `{spec-dir}/review-findings-{datetime}-iter1.md`
- Iteration 2: `{spec-dir}/review-findings-{datetime}-iter2.md`
- etc.

where `{spec-dir}` is the directory containing the spec file and `{datetime}` is today's date and time in `YYYYMMDDHHMMSS` format. All iteration files in this run must use the same date and time (the date and time as of the time the run was initiated).

## Procedure

### Step 0: Setup

- Determine `{datetime}` from today's date and time.
- Set `iteration = 1` and `max_iterations` from `$2` (default 3).
- Read the guidelines file `$1` to understand the review perspectives.
- Read the spec file `$0` to get a high-level understanding of its structure (table of contents, section numbering, length).

Report to the user:

```
Starting review-and-fix loop
  Spec: $0
  Guidelines: $1
  Max iterations: {max_iterations}
```

---

### Step 1: Generate findings (review phase)

Launch **one subagent per review perspective** defined in `$1`, all in parallel. Each perspective is a `## N. Title` section in the guidelines file.

Give each subagent this prompt:

---

**Your task**: Review a technical specification from one specific perspective and produce findings.

**Spec file**: `{SPEC_FILE}`
**Perspective number**: `{N}`
**Perspective title**: `{PERSPECTIVE_TITLE}`
**Perspective focus**: `{FOCUS_TEXT}`
**Must-check examples**: `{MUST_CHECK_LIST}`
**Iteration**: `{ITERATION}` (if > 1, prior findings files exist — check what was already fixed)

{IF ITERATION > 1}
**Prior findings**: `{PRIOR_FINDINGS_FILE}` — read this to understand what was already reviewed and fixed. Focus on:

1. Issues that were marked PARTIAL or SKIPPED in the prior iteration
2. NEW issues introduced by fixes in the prior iteration (regressions)
3. Issues that were missed entirely in prior reviews
4. Issues whose fixes were insufficient or incorrect

Do NOT re-report findings that were fully resolved. If a prior finding was fixed correctly, skip it.
{END IF}

## Instructions

Read the entire spec file. You may need multiple Read calls with offset/limit for large files.

Evaluate the spec against your perspective's focus area and must-check examples. Go beyond the examples — identify any gaps, inconsistencies, risks, or improvements relevant to the perspective, even if not listed in the examples.

For each finding, produce a block in this exact format:

```
### {CAT}-{NNN} {TITLE} [{SEVERITY}]
**Section:** {SECTION_NUMBERS}

{DESCRIPTION — 1-3 paragraphs explaining the problem, with specific references to the spec text}

**Recommendation:** {Concrete, actionable recommendation for fixing the issue. Be as descriptive as possible}
```

Severity scale:

- **Critical**: Blocks deployment or creates serious security/data-integrity risk
- **High**: Significant gap that must be resolved before production use
- **Medium**: Design improvement that should be addressed but has workarounds
- **Low**: Minor improvement or clarification
- **Info**: Observation or suggestion, no action required

Rules:

- Be specific. Reference exact section numbers, field names, and quoted text.
- Each finding must have a concrete recommendation, not just "consider" or "think about".
- Do not report style preferences or subjective opinions as findings.
- Do not duplicate findings — if something was covered by a prior iteration's fix, skip it.
- Focus on substance: correctness, completeness, security, consistency, operability.
- IDs should be in the format {CAT}-{NNN}, where CAT is the category.

Return all findings as a single markdown block, ordered by severity (Critical first).

---

After all subagents complete, consolidate their results into a single findings file.

#### Findings file format

Follow this exact structure (matching the format of existing findings files in this project):

```markdown
# Technical Design Review Findings — {DATE} (Iteration {N})

**Document reviewed:** `{SPEC_FILE}`
**Review framework:** `{GUIDELINES_FILE}`
**Iteration:** {N} of {MAX}
**Total findings:** {COUNT} across {NUM_PERSPECTIVES} review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | {N}   |
| High     | {N}   |
| Medium   | {N}   |
| Low      | {N}   |
| Info     | {N}   |

### Critical Findings

| #   | Perspective | Finding | Section |
| --- | ----------- | ------- | ------- |

{table rows for each critical finding}

---

## Detailed Findings by Perspective

---

## {N}. {Perspective Title}

### {CAT}-{NNN}. {Finding Title} [{Severity}]

**Section:** {X.Y}

{Description}

**Recommendation:** {Recommendation}

{... more findings ...}

---

## Cross-Cutting Themes

{Identify 3-7 systemic themes that appear across multiple perspectives}
```

Assign unique identifiers to every finding using the format `{CAT}-{NNN}`:

- `{CAT}` is a 3-letter category code derived from the perspective (use the same codes as any prior iteration if they exist; otherwise derive sensible 3-letter abbreviations)
- `{NNN}` is a zero-padded sequential number starting at 001 within each category

Write this file to `{spec-dir}/review-findings-{date}-iter{iteration}.md`.

Report to the user:

```
Iteration {iteration} review complete: {total} findings ({critical} Critical, {high} High, {medium} Medium, {low} Low, {info} Info)
```

---

### Step 2: Decide whether to fix

Evaluate whether to proceed with fixes:

- If there are **0 findings**: the spec is clean. Go to Step 4.
- If all findings are **Info** severity only: report them and go to Step 4 (no fixes needed).
- Otherwise: proceed to Step 3.

If this is the **last iteration** (iteration == max_iterations), report remaining findings without fixing and go to Step 4.

---

### Step 3: Fix the findings

Invoke the `/fix-findings` skill:

```
/fix-findings {SPEC_FILE} {FINDINGS_FILE}
```

This will process each finding sequentially with verification, fixing, and regression checking.

After `/fix-findings` completes, increment `iteration` and go back to **Step 1** for re-review.

---

### Step 4: Final report

Print a summary of all iterations:

```
## Review-and-Fix Summary

| Iteration | Findings | Critical | High | Medium | Low | Info | Fixed | Skipped | Partial |
|-----------|----------|----------|------|--------|-----|------|-------|---------|---------|
| 1         | ...      | ...      | ...  | ...    | ... | ...  | ...   | ...     | ...     |
| 2         | ...      | ...      | ...  | ...    | ... | ...  | ...   | ...     | ...     |

**Final state:** {CLEAN | N remaining findings}
**Files produced:**
- {list of all findings files}
```

If findings remain after the last iteration, list them with their IDs and note they require human review.

---

## Important constraints

- **Review agents run in parallel** (one per perspective) for speed.
- **Fix agents run sequentially** (via `/fix-findings`) for correctness.
- **Each iteration re-reviews from scratch** against the current state of the spec, but skips findings that were already fixed.
- **Never modify the guidelines file.** Only the spec and findings files are written/edited.
- **Findings files are append-only across iterations** — never overwrite a prior iteration's file. Each iteration gets its own file.
- **Category codes must be consistent** across iterations so findings can be cross-referenced.
- If a fix in iteration N introduces a new problem caught in iteration N+1, this is expected and healthy — that's the purpose of the loop.
