# Decisions: 0179 A formal model of the upload-token keys and consumed state

## Open decisions

- **O1. The modelling tool.** TLA+ with TLC, as change [0169](../archive/2026-11-23-0169-token-concurrency-model/change.md) uses for the token rules, or another tool such as Alloy (change [0165](../archive/2026-11-23-0165-token-rules-model/change.md)) or a model-based property test in Go. Recommendation, to be confirmed when the change starts: TLA+ with TLC, because the questions are about orderings across gateway replicas and time, and 0169's runner and pinned `tla2tools.jar` can be reused.
- **O2. Program membership.** Whether the change is a member of the v1 program or stays outside it as 0175 and 0177 do. The owner listed it on 2026-11-26 among the formal verifications owed before the implementation of M3 starts.

## Decisions

None.
