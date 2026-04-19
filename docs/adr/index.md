---
layout: default
title: "Architecture Decisions"
nav_order: 9
has_children: true
description: Architecture Decision Records (ADRs) for Lenny. Context, alternatives, and consequences for every significant design decision.
---

# Architecture Decisions

Architecture Decision Records (ADRs) capture every significant design decision in Lenny — the context that forced the decision, the alternatives that were considered, what was chosen, and what the consequences look like going forward.

ADRs are the durable memory of *why* the system looks the way it does. When a reviewer, new maintainer, or AI agent asks "why did we pick Postgres instead of etcd for session state?" or "why is gVisor the default isolation?", the answer lives here — not in commit messages, not in Slack history, not in a decade-old tech design doc.

This page is the catalog. The ADRs themselves follow the [MADR 3.0.0](https://adr.github.io/madr/) format and are individually linked below.

---

## When to write an ADR

An ADR is required for any change that:

- Introduces or removes a CRD.
- Changes the session, pod, or delegation state machine.
- Alters the isolation model (runtime class, sandbox profile, admission policy).
- Changes storage architecture (new store, new table family, schema-migration pattern).
- Adds or removes a gateway subsystem (or a Tier-3 extraction).
- Modifies the credential, token-exchange, or RLS model.
- Cuts across multiple components in a way that future contributors would otherwise have to reverse-engineer.

Typo fixes, one-file refactors, and local implementation changes do **not** need an ADR. If you are not sure whether your change is ADR-worthy, open a [discussion](https://github.com/lennylabs/lenny/discussions) first.

---

## ADR lifecycle

| Status | Meaning |
|:-------|:--------|
| `Proposed` | Draft. Under discussion on GitHub. |
| `Accepted` | Approved and ready for implementation. |
| `Deprecated` | The decision no longer applies. Retained for historical context. |
| `Superseded by ADR-NNNN` | Replaced by a newer decision. The superseding ADR links back. |

Accepted ADRs are never edited in substance. If the decision changes, write a new ADR that supersedes it and update the old one's status.

The BDfN maintainer approves ADRs today; after the governance transition described in [Governance](../about/governance.html), the steering committee does.

---

## How to write one

1. Copy [`template.md`](template.html) and rename it to `NNNN-kebab-case-title.md`, using the next free number.
2. Fill in Context, Decision Drivers, Considered Options, Decision Outcome, Consequences, and Links.
3. Set `status: Proposed` and open a PR tagging `@maintainer`.
4. Discuss. Iterate. When the maintainer accepts, flip the status to `Accepted` in the same PR that merges the decision.
5. Add an entry to the catalog table below in the same PR.

Decision content stays in the Markdown body; the front matter only carries metadata the docs site and `lenny-ops` catalog need for indexing.

---

## The catalog

### Meta

| # | Title | Status |
|:--|:------|:-------|
| [ADR-0000](0000-use-madr-for-architecture-decisions.html) | Use MADR for architecture decisions | Accepted |

### Platform

The table below seeds every resolved decision from [Spec §19](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md). Each row becomes a full ADR as contributors backfill context, alternatives, and consequences from the spec and design-conversation archives. ADR numbers are reserved — do not renumber.

| # | Title | Status | Spec ref |
|:--|:------|:-------|:---------|
| ADR-0001 | Full snapshots with size cap for checkpointing | Planned | [§19 #1](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0002 | Sidecar packaging for runtime adapters | Planned | [§19 #2](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0003 | Logical multi-tenancy via `tenant_id` filtering | Planned | [§19 #3](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0004 | `kubernetes-sigs/agent-sandbox` + controller-runtime | Planned | [§19 #4](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0005 | cert-manager + manual mTLS (no service-mesh dep) | Planned | [§19 #5](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0006 | gVisor as the default isolation profile | Planned | [§19 #6](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0007 | MinIO for blob storage (never Postgres) | Planned | [§19 #7](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0008 | Open-source license selection (MIT) | Planned | [§19 #14](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0009 | Delegation file export structure (strip + rebase) | Planned | [§19 #8](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0010 | No first-class `pipe_artifacts`; reuse export flow | Planned | [§19 #9](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0011 | Allowlist-default setup command policy | Planned | [§19 #10](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0012 | Per-session / token / minute usage tracking | Planned | [§19 #11](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0013 | No session forking; derive via workspace snapshot | Planned | [§19 #12](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |
| ADR-0014 | Lease extension via adapter↔gateway gRPC | Planned | [§19 #13](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) |

`Planned` ADRs have reserved numbers but no file yet. When a contributor writes one, they flip the status in both the ADR and this table (to `Accepted` or whatever the outcome is) in the same PR. Inbound links from the rest of the docs should reference the ADR number, not the path — renumbering is not allowed, so the number is stable.

---

## Where ADRs are referenced

| Surface | How it links |
|:--------|:-------------|
| [Spec §19](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md) | Summary table; full context lives here. |
| [Contributing](../about/contributing.html#adr-process) | When an ADR is required for a PR. |
| [Governance](../about/governance.html#architecture-decision-records-adrs) | Review process and lifecycle authority. |
| `lenny-ops` | ADRs are eligible for indexing in the management plane's decision catalog (post-v1). |

---

## Related

- [Template](template.html) — the canonical MADR 3.0.0 body.
- [ADR-0000](0000-use-madr-for-architecture-decisions.html) — why we use ADRs at all, and why MADR.
- [Contributing](../about/contributing.html) — PR process, DCO, and where ADRs fit in the review pipeline.
- [Governance](../about/governance.html) — who approves ADRs today and what changes at the steering-committee transition.
