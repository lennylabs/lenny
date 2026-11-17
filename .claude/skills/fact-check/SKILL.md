---
name: fact-check
description: List each factual claim a document makes about the code, the specification, the tree, or an external source, check each one, and report the claims that cannot be confirmed with the evidence found. It edits nothing. Use before a review or a decision, or on any document the owner names.
argument-hint: <document> [--section <name>] [--external]
---

# Fact-check

Check the factual claims of one document and report what does not hold.

## Arguments

- `<document>`: the file to check.
- `--section <name>`: check only that section.
- `--external`: also check claims about external sources (standards, vendor documentation, versions) against the live source. Without it, external claims are listed as unchecked.

## Steps

1. List each factual claim: a statement about what the code does, what a file holds, what the specification says, what a test covers, a count, a version, or what an external source states. Opinions, decisions, and plans are not claims.
2. Check each claim against its source: read the file or section it names, grep for the identifier, or run a read-only command. For an external claim with `--external`, fetch the live source; never rely on memory for a version or a date.
3. When the list is long, split it across subagents by the file or area the claims concern, each with the brief "Check each claim against the tree by reading what it names, and return confirmed, refuted, or unconfirmable with the evidence. Do not edit any file."
4. Report the refuted and unconfirmable claims, each with the claim quoted, the location in the document, the evidence, and the correction when one is evident. Give the count of confirmed claims without listing them.
