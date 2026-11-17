---
name: review
description: Review a change, a document, a diff, or a branch with two subagents run in parallel, one for a general review and cross-reference (the target itself, the tree, the documentation, and the other changes of the same program) and one for security. Use when the owner asks for a review, before asking the owner to decide or land something, and before merging a branch that changes the merge gate's own files (.claude/rules/git-workflow.md, "Review changes to the merge gate"). One round; it never reruns itself.
argument-hint: <target> [--since <rev>] [--fix] [--record <file>]
---

# Review

Run one review round over a target and report verified findings. The owner decides whether to run another round.

A branch that changes the merge gate's own files is reviewed before it merges, as `.claude/rules/git-workflow.md` ("Review changes to the merge gate") states. For that review the target is the branch's diff of those files against its merge base with `main`, and the general reviewer also checks the points that rule lists.

## Arguments

- `<target>`: a change folder such as `changes/0127-egress-and-git`, other paths, a diff range such as `main..HEAD`, or a branch.
- `--since <rev>`: review only what changed since that commit. Give the subagents the diff, and the full files for context.
- `--fix`: apply the findings that need no decision after checking them.
- `--record <file>`: append a summary entry to that file, such as the change's `review-history.md`.

## Before starting

1. Resolve the target to a list of files, or a diff. For a change folder, the files are `change.md`, `decisions.md`, and `steps.md`.
2. Find the program the target belongs to: the `Programs:` field of the change's status line, or the change that a diff touches. Read that program's "Reading the program" section under `changes/programs/`, and list its member changes. If the target belongs to no program, there is no program context.

## The two subagents

Start both with the Agent tool in one message, so they run in parallel. Give each the target, the program's reading guidance, the list of member changes, and the finding format below. Tell each to read the tree itself and not to edit any file.

**General reviewer.** Its brief:

- Review the target itself: what is ambiguous, contradictory, missing, or would fail when built, and whether its decisions, steps, and documentation agree with each other.
- Check every claim about the code and the specification against the tree, by reading the files named.
- Check every documentation page the target edits or names against the specification, the code, and `.claude/rules/doc-content.md`, and check prose against `.claude/rules/doc-style.md`.
- Cross-reference the target against the other changes of the same program, staged or planned, read together as the program's reading guidance states: a mechanism two changes define differently, a step that another change's step needs and does not list, a requirement one change retires that another relies on.

**Security reviewer.** Its brief:

- Review the target for security: trust boundaries, authentication and authorization, tenant isolation, secrets and credentials, input that crosses a boundary, resource exhaustion, and the failure paths an attacker can drive.
- Check each concern against the tree and against the other changes of the same program, because a control one change assumes may be removed or weakened by another.
- Report only concerns with a concrete attack or failure path, and name it.

## Finding format

Each subagent returns a list. Each finding states:

- the location (file and section, or file and line in code);
- the claim, in one sentence;
- the evidence: a file and section, or a quoted line, that shows the problem;
- the severity: High, Medium, or Low;
- a proposed fix;
- whether the fix needs a decision by the owner.

## After the subagents return

1. Check each finding's evidence yourself by reading what it cites. Drop a finding whose evidence does not show the problem, and say so in the report.
2. With `--fix`, apply the remaining findings that need no decision. Keep each fix to what the finding names.
3. Report, most severe first: the findings fixed, the findings that need a decision (each with options and a recommendation, ready to add to `decisions.md`), the findings to file as issues, and the findings dropped with the reason.
4. With `--record`, append one entry: the date, "review", the target and `--since` range, the counts of findings by outcome, and one line per finding that needs a decision.

Do not start a second round. The owner asks for one if needed.
