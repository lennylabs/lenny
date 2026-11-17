---
name: tidy
description: Bring one document into a template without changing what it says - section order, terms first, each statement once, history moved out, and the documentation style applied - with a verifier subagent that refuses the edit if any decision, requirement, value, or dependency changes meaning. Use on a change file, a specification page, or any document the owner names.
argument-hint: <document> [--template <file>] [--history <file>]
---

# Tidy

Restructure one document so it follows a template, without changing its meaning.

## Arguments

- `<document>`: the file to tidy.
- `--template <file>`: the template to follow. Without it, a file under `changes/` follows the template in `changes/README.md` for its kind (`change.md`, `decisions.md`, or `steps.md`), and any other file keeps its own section order.
- `--history <file>`: where text that records history goes, such as `review-history.md`. Without it, history stays where it is.

## Steps

1. Read the document and the template. Read `.claude/rules/doc-style.md`.
2. Edit the document:
   - Order the sections as the template does, and give each section the template's name. Keep a section the template does not name where it reads best, and say where in the report.
   - Put terms first, and gloss or link every label from another document on first use.
   - State each fact once. Where two passages state the same fact, keep the one in the section that owns it and delete the other, or replace it with a link.
   - Move text that records history (what a revision changed, when a review ran, what an earlier version said) to the history file, quoted as it stood.
   - Apply the documentation style rules.
3. Copy the original to the scratchpad first. Start a verifier subagent with the original and the result, and this brief: "Compare the two versions. List every decision, requirement, value, default, limit, dependency, step, and closed finding whose meaning differs, or that is in one version and missing from both the other and the history file. Moving text is not a difference. Do not edit any file."
4. If the verifier lists a difference, restore what was lost or changed and run the verifier again. If a difference remains after two attempts, restore the original and report.
5. Report the sections moved, the duplicates removed, the history moved, and the verifier's result.
