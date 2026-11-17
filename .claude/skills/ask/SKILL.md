---
name: ask
description: Turn the open questions in a document into multiple-choice questions for the owner, recommendation first, ask them, and record each answer with its date where the caller says. In a change folder it moves each settled question from "Open decisions" to "Decisions" in decisions.md. Use when a document has open questions the owner has to settle.
argument-hint: <document> [--section <name>] [--record <section>]
---

# Ask

Put a document's open questions to the owner and record the answers.

## Arguments

- `<document>`: the file holding the questions. For a change folder, give the folder or its `decisions.md`.
- `--section <name>`: the section holding the open questions. Default: "Open decisions".
- `--record <section>`: the section that receives each answer. Default: "Decisions" in the same file.

## Steps

1. Read the open questions. For each, take its options and its recommendation from the text. If a question has no options or no recommendation, write them from the document and the tree before asking, and say that you did.
2. Ask with the AskUserQuestion tool, up to four questions per call. Each question stands on its own: it names the change and the thing being decided, because the owner may see only the question. Put the recommended option first, with "(Recommended)" after its label, and keep each option's description to its consequence.
3. Record each answer:
   - In the record section, add the decision with its number, the answer in the owner's words when they gave words, and the date.
   - Remove the question from the open section. If the open section is left empty, write "None."
   - In a change folder, append an entry to `review-history.md` naming the decisions settled, and if no open decision remains and the change was `Draft`, tell the owner it can become `Decided` (`python3 scripts/changes/changes.py status <number> Decided`).
4. When the owner answers "Other" with text, record that text as the answer, and if it raises a new question, add it to the open section.
