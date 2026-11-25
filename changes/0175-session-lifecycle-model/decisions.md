# Decisions: 0175 A formal model of the session lifecycle

## Open decisions

- **O1. The modelling tool.** TLA+ with TLC, as change 0169 uses for the token rules, or another tool such as Alloy (change 0165) or a model-based property test in Go. Recommendation, to be confirmed when the change starts: TLA+ with TLC, because the state machine's questions are about orderings across replicas and sessions, and 0169's runner and pinned `tla2tools.jar` can be reused.
- **O2. Program membership.** Whether the change is a member of the v1 program in milestone M3 or stays outside the program as 0172 does. The owner asked for it as M3 prep work on 2026-11-25.

## Decisions
