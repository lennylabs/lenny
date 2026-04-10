---
layout: default
title: Governance
parent: About
nav_order: 4
---

# Governance
{: .no_toc }

This page describes Lenny's governance model, decision-making process, and policies for contributions, licensing, and releases.

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## Governance model: Benevolent Dictator for Now (BDfN)

Lenny adopts a **Benevolent Dictator for Now (BDfN)** governance model during its early development phase. A single maintainer makes final decisions on all architectural, implementation, and community matters.

This model is intentionally lightweight and designed to minimize decision-making overhead while the project is in active pre-release development. It is not intended to be permanent.

### Why BDfN

- The project is in pre-release development with a small team.
- Rapid iteration requires fast, decisive architectural choices.
- The technical design is still being validated by implementation and benchmarking.
- A formal governance structure would add overhead without proportional benefit at this stage.

### What the BDfN decides

- Architectural direction and trade-offs.
- Which contributions to accept or reject.
- Release timing and scope.
- ADR approval or rejection.
- Community policy and enforcement.

---

## Transition to steering committee

The BDfN model transitions to a multi-maintainer **steering committee** when the project reaches **3 or more regular contributors**.

### Transition criteria

A "regular contributor" is defined as someone who has:

- Made substantive code contributions (not just typo fixes or documentation) merged into `main`.
- Participated in architectural discussions (Discussions forum or ADR reviews).
- Maintained activity over a period of at least 3 months.

### Transition process

When the transition criteria are met:

1. The BDfN nominates initial steering committee members from the pool of regular contributors.
2. The committee drafts a formal charter covering decision-making procedures, voting rules, and membership criteria.
3. The `GOVERNANCE.md` file in the repository root is updated with the committee charter.
4. The BDfN role is dissolved; the committee governs by consensus with a fallback voting mechanism.

### Steering committee responsibilities

Once formed, the steering committee is responsible for:

- Architectural direction and major design decisions.
- Release planning and approval.
- ADR review and approval.
- Contributor recognition and maintainer promotion.
- Code of conduct enforcement.
- License and CLA policy decisions.

---

## Decision-making process

### During BDfN phase

1. **Proposals** are submitted via the Discussions forum or as draft ADRs.
2. **Discussion** happens publicly -- all input is welcome.
3. **Decision** is made by the BDfN, documented in the relevant ADR or issue.
4. **Rationale** is always recorded, especially for rejected proposals.

### After steering committee formation

1. **Proposals** follow the same public process.
2. **Consensus** is the preferred decision-making mode. Committee members discuss until agreement is reached.
3. **Voting** is the fallback when consensus cannot be reached within a reasonable timeframe. Simple majority wins, with the committee chair casting tie-breaking votes.
4. **Veto** -- any committee member can request a 48-hour delay on a decision for further consideration. This can only be exercised once per proposal.

---

## Architecture Decision Records (ADRs)

All significant architectural decisions are tracked via ADRs in `docs/adr/`.

### When an ADR is required

An ADR is required for changes above a defined scope threshold:

- New CRD or removal of an existing CRD.
- Changes to the session or pod state machine.
- Changes to the delegation policy model.
- Addition or removal of a gateway subsystem.
- Storage architecture changes (new store, new table, schema migration pattern).
- Security model changes (isolation profile, credential flow, RLS policy).
- Cross-cutting concerns affecting multiple components.

### ADR lifecycle

| Status | Meaning |
|:-------|:--------|
| `Proposed` | Under discussion. Not yet accepted. |
| `Accepted` | Approved and ready for implementation. |
| `Deprecated` | No longer applicable. Retained for historical context. |
| `Superseded by ADR-NNN` | Replaced by a newer decision. |

### Community-proposed ADRs

Community members can propose ADRs by:

1. Opening a Discussion thread with the `adr-proposal` tag.
2. Including the full ADR format (Context, Decision, Alternatives, Consequences).
3. The maintainer (or steering committee) reviews within 2 weeks and either accepts, requests modifications, or rejects with published rationale.

---

## License and CLA policy

### Open-source license selection (ADR-008)

License selection is a **Phase 0 gating item**. The license must be committed to the repository root before any contributor engagement, `CONTRIBUTING.md` publication, or external PR is accepted. This gates Phase 2 community onboarding.

### Evaluation criteria

| Criterion | Description |
|:----------|:------------|
| **Competitive landscape alignment** | E2B uses Apache 2.0 with a commercial offering. Temporal and LangChain use MIT. The license should position Lenny competitively. |
| **Enterprise legal review** | The license must pass standard enterprise legal review processes. Copyleft licenses face higher friction. |
| **Runtime author copyleft clarity** | The license must not create ambiguity about obligations for runtime adapter authors who distribute their own code alongside Lenny's adapter contract. |
| **Upstream compatibility** | Must be compatible with `kubernetes-sigs/agent-sandbox` (Apache 2.0) and other dependencies. |

### Candidate licenses

| License | Advantages | Disadvantages |
|:--------|:-----------|:--------------|
| **MIT** | Maximally permissive. Lowest enterprise adoption friction. Used by Temporal, LangChain. | No patent protection. No copyleft protection against proprietary forks. |
| **Apache 2.0** | Permissive with explicit patent grant. Used by Kubernetes, E2B. Standard in the cloud-native ecosystem. | Slightly more complex than MIT. Patent retaliation clause may concern some enterprises. |
| **AGPL + commercial exception** | Strong copyleft protects against proprietary hosted forks. Commercial exception allows commercial use without AGPL obligations. | High enterprise legal friction. Unfamiliar to many K8s ecosystem contributors. Runtime author obligations unclear. |
| **BSL (Business Source License)** | Time-delayed open source (code becomes open after a defined period). Protects commercial interests during early growth. | Not truly open source during the BSL period. May discourage enterprise and community adoption. |

The decision and rationale are recorded as ADR-008 in `docs/adr/`.

### Contributor License Agreement (CLA)

The CLA policy is determined alongside the license decision in ADR-008. Options under consideration:

- **No CLA** -- contributions are accepted under the project's license. Simplest for contributors.
- **Developer Certificate of Origin (DCO)** -- contributors certify they have the right to submit the code. Enforced via `Signed-off-by` in commit messages. Used by the Linux kernel and CNCF projects.
- **CLA** -- contributors sign a formal agreement granting rights to the project. More legally precise but higher friction for new contributors.

---

## Roadmap and release process

### Release philosophy

- **Semantic versioning** (SemVer) for all releases.
- **Breaking changes** only in major version bumps.
- **Deprecation policy:** features are deprecated for at least one minor version before removal.
- **Changelog** maintained in `CHANGELOG.md` with entries for every user-facing change.

### Phase-gated development

Lenny follows a phase-gated development process where each phase has defined deliverables and exit criteria:

| Phase | Focus | Key deliverables |
|:------|:------|:-----------------|
| **Phase 0** | Foundation | License (ADR-008), repository setup, CI pipeline. |
| **Phase 2** | Core platform | `make run` local dev mode, echo runtime, `CONTRIBUTING.md`, `GOVERNANCE.md` draft, benchmark harness. |
| **Phase 13.5** | Pre-hardening baselines | Load tests at Tier 2, pre-hardening performance baselines. |
| **Phase 14.5** | SLO validation | Full security hardening active, SLO compliance gate. |
| **Phase 17a** | Community launch | Documentation review, governance finalization, comparison guides, community onboarding. |

### Release cadence

During early development, releases follow an as-needed cadence driven by phase completion. After GA:

- **Minor releases** on a regular cadence (target: monthly or bi-monthly).
- **Patch releases** as needed for security fixes and critical bugs.
- **Major releases** as needed for breaking changes (expected to be infrequent).

### Release process

1. **Feature freeze** -- no new features merged after the freeze date.
2. **Release candidate** -- a tagged RC is cut and tested against the full integration suite.
3. **Regression testing** -- all SLO burn-rate alerts validated at Tier 2 sustained load.
4. **Release** -- tag, build artifacts, publish Helm chart, update documentation.
5. **Announcement** -- release notes published via the Discussions forum and changelog.

---

## Governance artifacts

| Artifact | Location | Status |
|:---------|:---------|:-------|
| `GOVERNANCE.md` | Repository root | Drafted in Phase 2, finalized in Phase 17a. |
| `CONTRIBUTING.md` | Repository root | Published in Phase 2 alongside `make run` quick-start. |
| `CODE_OF_CONDUCT.md` | Repository root | Published in Phase 2. |
| `LICENSE` | Repository root | Phase 0 gating item (ADR-008). |
| ADRs | `docs/adr/` | Ongoing. New decisions recorded as they are made. |

All governance artifacts are v1 launch deliverables.
