# Governance

This file is the canonical root-level governance document. The long-form version — including ADR process, release process, and the transition plan — lives at [`docs/about/governance.md`](docs/about/governance.md).

## Current model: Benevolent Dictator for Now (BDfN)

During the design and early build phases, a single maintainer has final decision authority over architecture, merges, releases, and community policy. This model is intentionally lightweight and is not intended to be permanent.

## Transition to a steering committee

The BDfN model transitions to a multi-maintainer steering committee when the project reaches **three or more regular contributors**, where "regular" means someone who has:

- Landed substantive changes (not just typos or docs) in `main`,
- Participated in architectural discussions or ADR reviews, and
- Sustained that activity for at least three months.

When the criteria are met, the maintainer will propose an initial committee, the committee will draft a charter (decision rules, voting, membership, rotation), and this file will be updated to reflect the new model.

## Decision-making

- **Proposals** are filed as GitHub Discussions or draft ADRs in [`docs/adr/`](docs/adr/).
- **Discussion** happens in the open; anyone can participate.
- **Decisions** are documented in the relevant ADR or issue with rationale — including rationale for rejected proposals.
- After the steering committee is formed, consensus is the preferred mode; simple-majority voting is the fallback.

## Architecture Decision Records

Significant architectural decisions require an ADR. See [`docs/about/governance.md#architecture-decision-records-adrs`](docs/about/governance.md#architecture-decision-records-adrs) for the triggering criteria and the template.

## License and DCO

- **License:** [MIT](LICENSE). Contributions are accepted under the same license.
- **Developer Certificate of Origin:** every commit must be signed off with `git commit -s`. No separate CLA.

## Contact

- **Issues and discussions:** <https://github.com/lennylabs/lenny>
- **Security:** see [`SECURITY.md`](SECURITY.md)
- **Conduct:** see [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md)
