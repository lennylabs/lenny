# Contributing to Lenny

Thanks for your interest in Lenny. This file is the canonical entry point; the long-form version lives at [`docs/about/contributing.md`](docs/about/contributing.md).

## Where the project is right now

Lenny is in the **design phase**. The [technical specification](spec/) is complete and drives implementation under a spec- and test-driven workflow. The [Implementation Status](docs/about/status.md) page tracks what's wired up today.

Because there is no merged codebase for PRs to land against yet, the highest-signal contributions right now are **design feedback**, not code.

## Best ways to contribute today

- **Open an issue or start a discussion.** Questions, disagreements with the spec, missing use cases, and concrete suggestions are all welcome. File an [issue](https://github.com/lennylabs/lenny/issues) or start a [discussion](https://github.com/lennylabs/lenny/discussions).
- **Read the spec and push on it.** [`spec/`](spec/) is the source of truth. If a section is unclear, contradictory, or leaves a case out, we want to know. Security threat-modelling against [`spec/13_security.md`](spec/13_security.md) is especially valuable.
- **Sketch a runtime adapter.** Prototype an adapter for your framework against the [adapter contract](spec/04_system-components.md). It doesn't need to run — contract pressure helps us find gaps before they become expensive.
- **Fix typos and broken links.** Small documentation PRs are welcome anytime. Keep them focused.

## When code PRs open up

Code contributions against core platform components open up once Phase 2 lands — that's the first working slice (`make run`, echo runtime, gateway skeleton). See [`spec/18_build-sequence.md`](spec/18_build-sequence.md) for the plan. This file will be updated the day the policy changes.

## Ground rules

- **License:** Lenny is [MIT-licensed](LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin (DCO):** sign off each commit with `git commit -s`. No separate CLA.
- **Code of Conduct:** participation is subject to the [Contributor Covenant](CODE_OF_CONDUCT.md).
- **Security issues:** do not file public issues for vulnerabilities. See [SECURITY.md](SECURITY.md) for the disclosure process.

## Getting help

- Documentation: [`docs/`](docs/)
- Long-form contributor guide: [`docs/about/contributing.md`](docs/about/contributing.md)
- Governance and decision-making: [`GOVERNANCE.md`](GOVERNANCE.md)
- Short-horizon roadmap: [`ROADMAP.md`](ROADMAP.md)
