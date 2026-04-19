---
layout: default
title: Documentation Style Guide
parent: About
nav_order: 4
description: Voice, tense, terminology, and link/code conventions used throughout the Lenny documentation.
---

# Documentation Style Guide

{: .no_toc }

The Lenny documentation is used for spec-driven development with AI agents. Consistency across pages matters more than literary flair — a coding agent that sees the same term used two different ways on two pages has to choose, and it often chooses badly. This page fixes the conventions.

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## Source of truth

The **`spec/`** directory on GitHub ([`lennylabs/lenny/spec`](https://github.com/lennylabs/lenny/tree/main/spec)) is the source of truth for behavior. Documentation distills, teaches, and cross-references the spec — it does not contradict it. When a doc and the spec disagree, the spec wins, and the doc is a bug.

When writing or reviewing docs:

- Quote canonical structures (API shapes, schema fields, error codes, isolation profiles) from the spec rather than re-inventing them.
- Cite the spec section with a link (e.g. "see [Spec §15.5 API Versioning](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#155-api-versioning-and-stability)"). Treat the spec like RFC references: specific section, stable URL.

---

## Voice and tense

**Voice.** Direct, second-person (`you`), active. Avoid hedging (`might`, `may`, `could`) unless the behavior is genuinely optional or version-dependent.

**Tense.** Present tense for what the v1 platform does (assume v1 is fully built for doc purposes — we use finalized docs for spec-driven development). Future tense is reserved for explicitly planned work ("v1.1 will add …") and for "Status: Planned" tutorial stubs.

**Person.**

- `you` = the reader, whichever persona they are
- `we` = the Lenny maintainers (only in governance, contributing, and ADR pages; not in reference content)
- third-person + role name for persona-specific guidance ("an operator configures…", "a runtime author implements…")

**Imperative for instructions.** "Run `helm install`", not "You should run `helm install`" or "One can run `helm install`".

**No marketing speak.** No "seamlessly", "powerful", "leverages", "cutting-edge". State what the thing does in one short sentence.

---

## Certifications and security claims

Lenny does not claim certifications it has not obtained. Do **not** write "SOC 2 compliant", "HIPAA-compliant", "FedRAMP-authorized", or similar. Instead, describe the **security principles** and **control primitives** that make Lenny a strong substrate for a compliant deployment:

- "Default-deny network perimeter with explicit egress allowlisting enables SOC 2 CC6.1 controls."
- "KMS-envelope-encrypted secret storage supports the key-management requirements under HIPAA Security Rule §164.312(a)(2)(iv)."
- "Per-tenant row-level security and cryptographic audit logging provide the foundation for ISO 27001 A.12.4 evidence."

Document the control; let the customer claim the certification for their deployment.

---

## Terminology (canonical)

Use these spellings and formulations everywhere. Do not introduce synonyms.

| Term | Notes |
|:-----|:------|
| `session` | The unit of work. Never "job", "task", or "agent run" (unless in the specific Tasks-API sense). |
| `runtime` | The agent adapter image (e.g. `claude-code`, `chat`). Not "worker", "executor", or "handler". |
| `pool` | A horizontally-scaled, pre-warmed set of pods for a runtime. |
| `gateway` | The client-facing service. Not "proxy" or "API server". |
| `workspace` | The mounted, session-local filesystem at `/workspace/current`. |
| `WorkspacePlan` | Camel-case when used as the schema name; lowercase "workspace plan" in prose only when not referring to the schema. |
| `isolation profile` | Canonical: `standard` (runc, dev only), `sandboxed` (gVisor, default), `microvm` (Kata). |
| `RuntimeClass` | Kubernetes-level: `runc`, `gvisor`, `kata`. Distinct from isolation profile. |
| `integration level` | Runtime integration tier. Canonical: `Basic`, `Standard`, `Full`. Never "tier". |
| `delegation` | Parent session creating a child session via the gateway. Never "subagent" or "sub-task" at the platform level. |
| `elicitation` | Mid-session prompt for user input. Never "human-in-the-loop popup" or similar. |
| `connector` | Gateway-managed OAuth-backed MCP server (GitHub, Jira, Slack). Distinct from a runtime. |
| `OutputPart` | The discriminated-union type for streamed output elements. Canonical types: `text`, `code`, `reasoning_trace`, `citation`, `screenshot`, `image`, `diff`, `file`, `execution_result`, `error`. |
| `platform-admin` / `tenant-admin` / `tenant-viewer` / `billing-viewer` / `user` | Built-in role names. Hyphenated, lowercase, in backticks in reference material. |

**API shapes (do not paraphrase):**

- Inbound message body: `{"type":"message","input":[{"type":"text","inline":"..."}]}`
- Outbound response body: `{"type":"response","output":[{"type":"text","inline":"..."}]}`
- TaskResult: `{"output":{"parts":[OutputPart[]], "artifactRefs":[...]}}` — note this has an extra `output.` wrapper, unlike the outbound response
- Streaming endpoint: `GET /v1/sessions/{id}/logs` (with `Accept: text/event-stream` for SSE)
- Message send: `POST /v1/sessions/{id}/messages`
- Upload token header: `X-Upload-Token: <token>`

---

## Links

- **Internal docs.** Use relative links without the `.html` extension (`[Getting Started](../getting-started/)`). Jekyll resolves `.md` to `.html` automatically.
- **Spec references.** Use absolute GitHub URLs to the spec (`https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#…`). The spec is versioned with the repo; always cite the section anchor.
- **External docs.** Use full URLs. Do not abbreviate third-party product names.
- **Glossary terms.** First mention per page links to the [glossary](../reference/glossary) entry. Subsequent mentions stay unlinked.

---

## Code blocks

- **Language tags.** Always tag fenced blocks (` ```bash `, ` ```json `, ` ```go `, ` ```yaml `, ` ```python `, ` ```typescript `). Untagged blocks don't syntax-highlight.
- **Commands.** Show the full command; don't use `$ ` prompt prefixes.
- **Example output.** Use a separate fenced block with a comment header (`# Expected output`) when output matters.
- **Placeholders.** `$LIKE_THIS` for environment variables the reader supplies; `<like-this>` only in URL path segments (`/v1/sessions/{id}`).

---

## Front-matter checklist

Every doc page should have:

```yaml
---
layout: default
title: "Page Title"
parent: "Section Name"        # omitted on section index pages
nav_order: <integer>
description: One-line sentence summarizing what the page covers. Used for SEO and nav hints.
---
```

For section-index pages (e.g. `getting-started/index.md`), add `has_children: true` and omit `parent`.

For pages that should not appear in nav (legacy, redirects), use `nav_exclude: true`.

---

## Accessibility and readability

- Use sentence case for headings (except proper nouns: "Lenny", "Kubernetes", "GitHub").
- Prefer tables for structured reference material over bullet lists with `**Field**:` prefixes.
- Prefer short paragraphs over walls of text; chunk at semantic boundaries.
- Every mermaid diagram has a prose alt description immediately before or after it.
- Every code block that shows output has a one-line explanation of what the reader should notice.

---

## Review checklist for a new or changed page

Run through this before opening a PR:

- [ ] Front-matter has `title`, `parent` (if child), `nav_order`, `description`.
- [ ] Voice is direct, present-tense, no hedging or marketing speak.
- [ ] Canonical terminology used throughout (no "job", "worker", "subagent", "tier").
- [ ] API shapes match the spec exactly — no paraphrased JSON.
- [ ] Every internal link resolves (no `.html` of nonexistent files).
- [ ] Spec claims cite the specific spec section.
- [ ] No certification claims; only principles and control primitives.
- [ ] First glossary-term mention per page is linked; subsequent mentions aren't.
- [ ] Code blocks have language tags.
