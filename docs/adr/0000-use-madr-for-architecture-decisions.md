---
layout: default
title: "ADR-0000: Use MADR for architecture decisions"
parent: "Architecture Decisions"
nav_order: 0
status: Accepted
date: 2026-04-18
deciders: "@maintainer"
tags:
  - governance
  - documentation
---

# ADR-0000: Use MADR for architecture decisions

## Status

**Accepted**

## Context and problem statement

Lenny is developed spec-first with a small contributor base and AI-agent collaborators doing much of the implementation. Every significant design decision needs a durable, machine-readable record — one that survives contributor turnover, compresses onboarding, and gives AI agents enough context to act without repeatedly re-deriving the reasoning.

The alternative — leaving design intent in commit messages, pull-request threads, or chat transcripts — has known failure modes: commit archaeology is expensive, PR threads get garbage-collected by the host, and chat history is not navigable. A light-weight, structured decision log is an industry-standard solution.

Before writing any platform ADRs, the project needs to pick a format and a publishing pipeline so later decisions can be filed without bikeshedding.

## Decision drivers

- **Low friction for authors.** Contributors must be able to write one in under an hour; otherwise decisions land elsewhere (code, chat, memory).
- **Readable by humans and AI agents.** Both audiences consume ADRs — they need clear, predictable sections and stable front matter.
- **Version-controlled alongside the code.** ADRs must live in the same repository and go through the same review workflow as code changes.
- **Link-stable.** ADR numbers never change; external references (including `lenny-ops` indexes, spec cross-links, and third-party citations) stay valid forever.
- **Open format.** No vendor lock-in; the format must be consumable as plain Markdown even if the docs site goes away.

## Considered options

- **MADR 3.0.0** — the Markdown Architecture Decision Records format, widely adopted in the CNCF ecosystem.
- **Michael Nygard's original ADR format** — the simpler 5-section format (Context / Decision / Status / Consequences / [Alternatives]) popularised by the *Documenting Architecture Decisions* blog post.
- **Y-Statements** — one-paragraph decisions, inline in code or the spec.
- **ARC42 decisions chapter** — ADRs maintained as a chapter inside the architecture document rather than a separate catalog.

## Decision outcome

**Chosen: MADR 3.0.0.**

ADRs for Lenny follow the [MADR 3.0.0](https://adr.github.io/madr/) format, live in `docs/adr/`, and are indexed in [`docs/adr/index.md`](./). Each ADR is named `NNNN-kebab-case-title.md`; numbering is permanent and contiguous. This ADR is number 0000 and establishes the practice.

The canonical body template lives at [`template.md`](template.html).

### Consequences

- **Positive.**
  - New decisions have a ready-made structure — Context, Decision Drivers, Considered Options, Decision Outcome, Consequences, Confirmation, Pros/Cons.
  - MADR's explicit "Considered options" and "Confirmation" sections force authors to document the paths not taken and the verification loop, which is exactly the information reviewers and future readers need.
  - Machine-parseable: `lenny-ops` can index ADRs the same way it indexes runbooks, exposing them to AI-agent operators through the management plane.
  - Broad industry adoption means contributors coming from other open-source projects recognise the format without retraining.
- **Negative.**
  - MADR is heavier than Nygard's original; for very small decisions the extra sections can feel like ceremony. Accepted trade-off: the consistency pays off at catalog scale.
  - Template drift is a risk if contributors fork the template informally. Mitigated by the canonical [`template.md`](template.html) in this directory, which is the only source of truth.
- **Neutral.**
  - Existing summaries in [Spec §19](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) continue to serve as the canonical index table; the ADRs provide the full context each row abstracts.

### Confirmation

The decision is working if:

1. Every ADR in `docs/adr/` parses as MADR 3.0.0 (the template captures the required sections).
2. Spec §19's table has a one-to-one mapping to ADR numbers in the catalog; no decision is orphaned.
3. PRs that hit the ADR threshold (see [Contributing § ADR process](../about/contributing.html#adr-process)) include an ADR in the same PR.

Review the health of the catalog once per quarter during a governance review. If authors systematically skip ADRs or the template drifts, supersede this ADR with a simpler format rather than tolerating drift.

## Pros and cons of the options

### MADR 3.0.0

- Good because it has broad CNCF-ecosystem recognition; contributors arrive knowing it.
- Good because the explicit Considered Options and Confirmation sections prevent the two most common ADR failure modes (no alternatives documented, no verification criteria).
- Good because the format is Markdown-native with no toolchain dependency.
- Bad because it is heavier than Nygard's original; small decisions feel over-structured.

### Nygard's original ADR format

- Good because it is genuinely minimal (four to five sections).
- Good because it has the longest historical track record.
- Bad because it does not require documenting alternatives — decisions ship without path-not-taken context.
- Bad because it does not require a confirmation / verification section.

### Y-Statements

- Good because they are the lightest possible format — a single structured sentence.
- Bad because they collapse all context into one line; unsuitable for decisions with real trade-offs.
- Bad because they are hard to index or cross-link programmatically.

### ARC42 decisions chapter

- Good because it keeps all architecture content (including decisions) in one document.
- Bad because it couples decision lifecycle to architecture-doc versioning; individual decisions cannot be superseded without a full doc revision.
- Bad because it does not scale — at dozens of decisions the chapter becomes unreadable.

## More information

- [Architecture Decisions index](./)
- [ADR Template](template.html)
- [MADR 3.0.0](https://adr.github.io/madr/) — upstream spec.
- [Contributing — ADR process](../about/contributing.html#adr-process)
- [Governance — Architecture Decision Records](../about/governance.html#architecture-decision-records-adrs)
- [Spec §19 — Resolved decisions](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md)
