# Decisions: 0175 A formal model of the session lifecycle

## Open decisions

- **O1. The modelling tool.** TLA+ with TLC, as change 0169 uses for the token rules, or another tool such as Alloy (change 0165) or a model-based property test in Go. Recommendation, to be confirmed when the change starts: TLA+ with TLC, because the state machine's questions are about orderings across replicas and sessions, and 0169's runner and pinned `tla2tools.jar` can be reused.
- **O2. Program membership.** Whether the change is a member of the v1 program in milestone M3 or stays outside the program as 0172 does. The owner asked for it as M3 prep work on 2026-11-25.
## Decisions
1. **0146 states the whole state machine before the model is built (O3).** As the first part of 0175.S1, the rules that change 0166 adds to the session state machine move into 0146 as revisions of SL-8 to SL-12: `onParentEnd` as a security caveat a child may only make stricter, the force-terminate that closes a tree's descendants at once, the tree terminate for a `tenant-admin`, `session.set_limits` raising a live child's age, the `ended` event of an expiry following the parent's `wakeOn`, a parent's parallelism counting `blocked` children, and recovery with no ordering across a tree. 0166 keeps its operations and a pointer to each rule, and each conflict the move finds becomes an open decision. Owner's answer to LM-1 (open decision O3) in chat, 2026-11-26, option (a), with the words "LM-1: ok". The option not chosen was (b), the model reading 0146, 0166, and 0135 MD-22 together.
