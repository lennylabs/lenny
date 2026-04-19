---
layout: default
title: "Template"
parent: "Architecture Decisions"
nav_order: 99
status: Template
description: The canonical MADR 3.0.0 template for Lenny ADRs. Copy, rename to NNNN-kebab-case-title.md, and fill in.
---

# ADR Template

{: .no_toc }

Copy the body below into a new file named `NNNN-kebab-case-title.md` using the next free number from the [catalog](./). The numbering is permanent — do not re-use retired numbers.

Keep the front matter small. Decision content belongs in the Markdown body, which is what humans and the `lenny-ops` ADR indexer read.

---

## Copy from here

```markdown
---
layout: default
title: "ADR-NNNN: <Short decision statement>"
parent: "Architecture Decisions"
nav_order: NNNN
status: Proposed
date: YYYY-MM-DD
deciders: <GitHub handles>
consulted: <optional — GitHub handles of reviewers>
informed: <optional — teams or groups kept in the loop>
supersedes: <optional — ADR-NNNN>
superseded_by: <optional — ADR-NNNN>
tags:
  - <component or topic tag>
---

# ADR-NNNN: <Short decision statement>

## Status

**Proposed** — replace with `Accepted` on merge, `Deprecated` if retired, or `Superseded by ADR-NNNN` if replaced.

## Context and problem statement

Describe the context that forced this decision. What forces are at play — technical constraints, business requirements, compliance obligations, performance budgets, team capacity? State the problem in a way that is meaningful to someone who was not in the room.

Good context answers: "If you forgot why we chose this, what do you need to remember to re-derive the decision?"

## Decision drivers

- <Driver 1 — e.g., "Support runtime-agnostic adapters written in any language">
- <Driver 2 — e.g., "Stay compatible with `kubernetes-sigs/agent-sandbox` CRD contract">
- <Driver 3 — e.g., "Preserve the 10× TTFT SLO even at Tier-3 sustained load">

## Considered options

- <Option 1 — a short name>
- <Option 2 — a short name>
- <Option 3 — a short name>

## Decision outcome

**Chosen: Option N — <short name>.**

<One paragraph stating the decision clearly. This is the part that callers of this ADR will quote.>

### Consequences

- **Positive:** <what becomes easier, safer, faster, cheaper>
- **Negative:** <what becomes harder, more expensive, or now requires a workaround>
- **Neutral:** <what changes but is neither better nor worse>

### Confirmation

How will we know this decision is working? Specify the verification — a benchmark, a test, a production metric, a post-release review. If confirmation reveals the decision was wrong, the next ADR supersedes this one.

## Pros and cons of the options

### Option 1 — <short name>

- Good because <pro>
- Good because <pro>
- Bad because <con>
- Bad because <con>

### Option 2 — <short name>

- Good because <pro>
- Bad because <con>

### Option 3 — <short name>

- Good because <pro>
- Bad because <con>

## More information

- Related ADRs: ADR-NNNN, ADR-NNNN
- Spec references: [§N.N](https://github.com/lennylabs/lenny/blob/main/spec/...)
- Issue / discussion: <link>
- Implementation PR: <link once landed>
```

---

## Field conventions

| Field | Convention |
|:------|:-----------|
| `nav_order` | The ADR number as an integer. Keeps the catalog sorted. |
| `status` | `Proposed`, `Accepted`, `Deprecated`, or `Superseded by ADR-NNNN`. Match the `## Status` section in the body. |
| `date` | ISO-8601 date of the most recent status change. Update when status transitions. |
| `deciders` | GitHub handles with approval authority for this ADR. During BDfN that is the maintainer; after transition, the steering committee. |
| `supersedes` / `superseded_by` | ADR numbers only (no paths). Renumbering is not allowed, so numbers are stable. |
| `tags` | One or more of: `isolation`, `storage`, `gateway`, `delegation`, `security`, `observability`, `governance`, `controller`, `credential`, `audit`, `tenancy`, `runtime`. Add new tags sparingly. |

---

## Section conventions

- **Context and problem statement** — written so that a new reader (human or AI) understands the forcing function without consulting the conversation thread. Keep it paragraph-form, not bullets, so the prose carries the logic.
- **Decision drivers** — the non-negotiables. If a driver is violated by the chosen option, the decision is wrong. Drivers guide future revisions.
- **Considered options** — at least two. Include the status-quo or do-nothing option if non-trivial.
- **Decision outcome** — one clear paragraph. The first sentence must name the chosen option; downstream docs will quote it.
- **Consequences** — explicit trade-offs. What becomes harder is the part reviewers most often miss on their first pass.
- **Confirmation** — the verification loop. ADRs without confirmation criteria produce drift — the decision succeeds on paper even if implementation contradicts it.

---

## Related

- [Architecture Decisions index](./)
- [ADR-0000](0000-use-madr-for-architecture-decisions.html) — why Lenny uses ADRs and why MADR.
- [MADR 3.0.0 reference](https://adr.github.io/madr/) — the upstream format this template is based on.
