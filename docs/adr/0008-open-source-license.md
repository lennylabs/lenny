---
layout: default
title: "ADR-0008: Open-source license selection (MIT)"
parent: "Architecture Decisions"
nav_order: 8
status: Accepted
date: 2026-05-17
deciders: "@maintainer"
tags:
  - governance
---

# ADR-0008: Open-source license selection (MIT)

## Status

**Accepted**

## Context and problem statement

Lenny is an open-source project that intends to grow a community of deployers and third-party runtime authors. The license governs how that community can use, modify, and redistribute the platform, and it interacts with three forces. Enterprise legal teams evaluate the license before a platform can be adopted internally. Runtime authors need a clear answer to whether building a runtime against Lenny imposes copyleft obligations on their own code. The project builds on `kubernetes-sigs/agent-sandbox`, so the license must be compatible with that upstream dependency and with the broader cloud-native ecosystem.

The license must be chosen in Phase 0, before the first external contribution, because it governs every contribution that follows and changing it later requires the consent of all contributors. This decision resolves question CPS-008.

## Decision drivers

- **Low enterprise adoption friction.** Enterprise legal review should pass quickly; a license that triggers extended review or rejection blocks adoption.
- **Copyleft clarity for runtime authors.** A runtime author must be able to tell, without legal counsel, that their runtime code carries no copyleft obligation from Lenny.
- **Ecosystem alignment.** The license should match what comparable infrastructure projects use, so contributors and deployers encounter no surprises.
- **Upstream compatibility.** The license must be compatible with `kubernetes-sigs/agent-sandbox` and the cloud-native dependency graph.

## Considered options

- **MIT** — a permissive license with no copyleft and minimal terms.
- **Apache 2.0** — a permissive license with an explicit patent grant and contributor terms.
- **AGPL with a commercial exception** — a strong-copyleft license paired with a separately sold commercial license.
- **Business Source License (BSL)** — a source-available license that converts to an open license after a delay.

## Decision outcome

**Chosen: MIT.**

Lenny is licensed under the MIT License. The `LICENSE` file at the repository root carries the MIT text, and every contribution is made under those terms. MIT was selected for the lowest enterprise adoption friction, unambiguous copyleft clarity for runtime authors, and alignment with comparable infrastructure projects: Temporal and LangChain use MIT, while E2B pairs Apache 2.0 with a separate commercial offering. MIT imposes no patent grant and no copyleft, so a runtime author building against Lenny carries no obligation on their own code, and an enterprise deployer faces a license that legal teams already recognize.

The contributor sign-off policy uses a Developer Certificate of Origin (DCO) rather than a Contributor License Agreement, keeping the contribution barrier low. The DCO check, `CONTRIBUTING.md`, and the initial `GOVERNANCE.md` draft ship in Phase 2 as part of the community onboarding milestone; `GOVERNANCE.md` is finalized in Phase 17a. Any change to the license requires a superseding ADR and, because relicensing affects existing contributions, the consent of contributors.

### Consequences

- **Positive.** Enterprise legal review is fast because MIT is a known quantity. Runtime authors have an unambiguous answer on copyleft. The license matches the ecosystem Lenny competes in, which removes a class of adoption objections.
- **Negative.** MIT has no patent grant, so the project does not gain the explicit patent protection Apache 2.0 provides. MIT also permits closed-source redistribution, so a third party can build a proprietary product on Lenny without contributing back.
- **Neutral.** Governance moves from a benevolent dictator for now (BDfN) model to a steering committee over time, which is independent of the license choice and is recorded separately in `GOVERNANCE.md`.

### Confirmation

The decision is working if enterprise deployers adopt Lenny without license-driven legal escalation, and if runtime authors do not raise copyleft questions in discussions or issues. The license is reviewed during the Phase 17a governance pass. If patent exposure or proprietary forks become a material problem, a superseding ADR re-evaluates the choice against Apache 2.0.

## Pros and cons of the options

### MIT

- Good because enterprise legal teams recognize it and clear it quickly.
- Good because it imposes no copyleft, so runtime authors have no obligation on their own code.
- Good because it aligns with comparable infrastructure projects such as Temporal and LangChain.
- Bad because it has no explicit patent grant.
- Bad because it permits proprietary redistribution with no contribution back.

### Apache 2.0

- Good because it includes an explicit patent grant.
- Good because it is widely accepted in the cloud-native ecosystem, including by `kubernetes-sigs` projects.
- Bad because its contributor and notice terms add review overhead relative to MIT.

### AGPL with a commercial exception

- Good because strong copyleft discourages proprietary forks and supports a commercial-license revenue path.
- Bad because enterprise legal teams frequently prohibit AGPL dependencies outright, which blocks adoption.
- Bad because the copyleft boundary is unclear to runtime authors, who would need legal counsel to proceed.

### Business Source License (BSL)

- Good because it protects against a competitor offering Lenny as a managed service during the conversion delay.
- Bad because BSL is not an open-source license, which contradicts the project's open-source positioning.
- Bad because source-available terms deter the community contributions the project depends on.

## More information

- Spec references: [§19 #14](https://github.com/lennylabs/lenny/blob/main/spec/19_resolved-decisions.md), [§23.2](https://github.com/lennylabs/lenny/blob/main/spec/23_competitive-landscape.md)
- Related ADRs: ADR-0000 (use MADR for architecture decisions)
- [Architecture Decisions index](./)
- [`LICENSE`](https://github.com/lennylabs/lenny/blob/main/LICENSE) — the MIT license text at the repository root.
