# Decisions: 0175 A formal model of the session lifecycle

## Open decisions

- **O1. The modelling tool.** TLA+ with TLC, as change 0169 uses for the token rules, or another tool such as Alloy (change 0165) or a model-based property test in Go. Recommendation, to be confirmed when the change starts: TLA+ with TLC, because the state machine's questions are about orderings across replicas and sessions, and 0169's runner and pinned `tla2tools.jar` can be reused.
- **O2. Program membership.** Whether the change is a member of the v1 program in milestone M3 or stays outside the program as 0172 does. The owner asked for it as M3 prep work on 2026-11-25.
- **O3. Where the state machine is stated before the model reads it (asked by the owner on 2026-11-26).** The states, the transitions, the cascade (SL-9), the caps and timers (SL-10), and recovery (SL-12) are 0146's. Change 0166 states the delegation operations and adds rules that act on 0146's states: `onParentEnd` as a security caveat a child may only make stricter, a force-terminate that closes a tree's descendants at once, a tree terminate for a `tenant-admin`, `session.set_limits` raising a live child's age, the `ended` event of an expiry following the parent's `wakeOn`, a parent's parallelism counting `blocked` children, and recovery with no ordering across a tree. Child events and the wait are MD-22's of change 0135. Options:
  - (a) As the first part of 0175.S1, move the rules 0166 adds to the state machine into 0146, as revisions of SL-8 to SL-12, and leave in 0166 its operations and a pointer to each rule, so that 0146 is the one statement of the state machine that the model, its counterexamples, and the M3 area change read. Each conflict the move finds becomes an open decision.
  - (b) Keep the rules where they are. The model reads 0146, 0166, and 0135 MD-22 together, as "Reading the program" in the v1 program states for member changes, and the M3 area change that writes the session lifecycle page brings them together.

  Recommendation: (a). The model's properties and each counterexample then cite one change, a fix lands in one place, and the area change copies one statement rather than reconciling three.

## Decisions
