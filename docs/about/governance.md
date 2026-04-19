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

Lenny adopts a **Benevolent Dictator for Now (BDfN)** governance model during early development. A single maintainer makes final decisions on all architectural, implementation, and community matters.

This model is intentionally lightweight to minimize decision-making overhead while the project is in active pre-release development. It is not intended to be permanent.

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

### During BDfN governance

1. **Proposals** are submitted via the Discussions forum or as draft ADRs.
2. **Discussion** happens publicly; all input is welcome.
3. **Decision** is made by the BDfN, documented in the relevant ADR or issue.
4. **Rationale** is always recorded, especially for rejected proposals.

### After steering committee formation

1. **Proposals** follow the same public process.
2. **Consensus** is the preferred decision-making mode. Committee members discuss until agreement is reached.
3. **Voting** is the fallback when consensus cannot be reached within a reasonable timeframe. Simple majority wins, with the committee chair casting tie-breaking votes.
4. **Veto:** any committee member can request a 48-hour delay on a decision for further consideration. This can only be exercised once per proposal.

---

## Architecture Decision Records (ADRs)

All significant architectural decisions are tracked via ADRs in [`docs/adr/`]({{ site.baseurl }}/adr/). The catalog page lists every ADR with its current status; [ADR-0000]({{ site.baseurl }}/adr/0000-use-madr-for-architecture-decisions.html) documents the format choice and authoring workflow.

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

### License

Lenny is licensed under the **MIT License**. The full text is in [`LICENSE`](https://github.com/lennylabs/lenny/blob/main/LICENSE) at the repository root.

MIT was chosen for:

- **Lowest enterprise adoption friction.** Standard enterprise legal review tends to clear MIT quickly.
- **Runtime-author clarity.** No copyleft obligations for runtime adapter authors who distribute their own code against Lenny's contract.
- **Ecosystem alignment.** Compatible with `kubernetes-sigs/agent-sandbox` (Apache 2.0) and other upstream dependencies.

### Contributor License Agreement (CLA)

Lenny uses the **Developer Certificate of Origin (DCO)**: contributors certify authorship by adding `Signed-off-by` to each commit (`git commit -s`). No separate CLA to sign. This matches the Linux kernel and CNCF projects and minimizes friction for contributors.

---

## Roadmap and release process

### Release philosophy

- **Semantic versioning** (SemVer) for all releases.
- **Breaking changes** only in major version bumps.
- **Deprecation policy:** features are deprecated for at least one minor version before removal.
- **Changelog** maintained in `CHANGELOG.md` with entries for every user-facing change.

### Milestone-gated development

Lenny follows a milestone-gated development process where each milestone has defined deliverables and exit criteria. The current plan lives in [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) and is directional — ordering and timing will shift as implementation surfaces new constraints. Governance-relevant gates include:

| Milestone | Key deliverables |
|:----------|:-----------------|
| **Foundation** | License (ADR-008), repository setup, CI pipeline. |
| **First working slice** | `make run` local dev mode, echo runtime, `CONTRIBUTING.md`, `GOVERNANCE.md` draft, benchmark harness. |
| **Pre-hardening baselines** | Load tests at Growth-sized deployment load, pre-hardening performance baselines. |
| **SLO validation** | Full security hardening active, SLO compliance gate. |
| **Community launch** | Documentation review, governance finalization, comparison guides, community onboarding. |

### Release cadence

During early development, releases follow an as-needed cadence driven by milestone completion. After GA:

- **Minor releases** on a regular cadence (target: monthly or bi-monthly).
- **Patch releases** as needed for security fixes and critical bugs.
- **Major releases** as needed for breaking changes (expected to be infrequent).

### Release process

1. **Feature freeze:** no new features merged after the freeze date.
2. **Release candidate:** a tagged RC is cut and tested against the full integration suite.
3. **Regression testing:** all SLO burn-rate alerts validated at Growth-sized sustained load.
4. **Release:** tag, build artifacts, publish Helm chart, update documentation.
5. **Announcement:** release notes published via the Discussions forum and changelog.

---

## Governance artifacts

| Artifact | Location | Status |
|:---------|:---------|:-------|
| `LICENSE` | Repository root | MIT, committed. |
| `CONTRIBUTING.md` | Repository root | Published (design-phase policy; opens fully when the first working slice with `make run` lands). |
| `CODE_OF_CONDUCT.md` | Repository root | Published. Contributor Covenant v2.1. |
| `SECURITY.md` | Repository root | Published. Coordinated disclosure policy. |
| `GOVERNANCE.md` | Repository root | Published. Finalized alongside steering-committee transition. |
| `ROADMAP.md` | Repository root | Published. Short-horizon priorities; full sequence lives in `spec/18_build-sequence.md`. |
| `CHANGELOG.md` | Repository root | Starts at first tagged release. |
| ADRs | `docs/adr/` | Ongoing. New decisions recorded as they are made. |

---

## Next steps

You're at the end of the **About** section. Depending on where you've landed:

- **Decided Lenny fits your use case?** Run the [Quickstart](../getting-started/quickstart.html) — `lenny up` starts the embedded stack in about a minute.
- **Planning a cluster deployment?** Go to the [Operator Guide](../operator-guide/) for the Day 0/1/2 reading path.
- **Reviewing for security or compliance?** Go to [Security Principles](../operator-guide/security-principles.html) and the reviewer shortcut inside the Operator Guide.
- **Want to contribute?** See [Contributing](contributing.html) for the current ways to plug in (issues, discussions, spec feedback, ADRs).
