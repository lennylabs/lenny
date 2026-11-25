# A formal model of the session lifecycle

Status: `Draft`, revision 1, 2026-11-25.

## Problem

Change 0146 (session lifecycle, [`changes/0146-session-lifecycle/`](../0146-session-lifecycle/change.md)) defines the session state machine that the v1 program builds in M3: the states of "SL-1", the transitions of "SL-2", suspension and the open turn ("SL-3"), tree suspension ("SL-4"), messages to a waiting or paused session ("SL-5"), idle pods and dormancy ("SL-6"), closing ("SL-7"), parents and children ("SL-8" and "SL-9"), admission, caps, and timers ("SL-10"), and the recovery of child events ("SL-12"). Change 0166 (delegation as session primitives, on branch `change/0166-delegation-primitives`) adds the delegation operations that move sessions through those states. The state machine spans several gateway replicas, the adapter, the controllers, and a tree of sessions, so an ordering of events that no section states on its own can leave a session in a state no rule leaves, or a tree in which a parent and a child disagree.

The owner asked on 2026-11-25 for a comprehensive review of the state machine with a formal validation, as prep work for M3, before the steps of 0146 are built. Changes 0165 (an Alloy model of the token rules) and 0169 (a TLA+ model of their concurrent orderings) are the precedent: each counterexample their models found was confirmed against the text and the code and became an open decision for the owner.

## Scope

The change reviews the state machine of 0146 as 0166 extends it, and builds a formal model of it (TLA+ with TLC, as 0169 does, unless decision O1 settles another tool) that checks the properties the review writes down: the safety properties, such as a session holding one state, every transition taken only from the states its rule names, and a terminal state never left, and the liveness properties, such as every suspended tree resuming or closing and every child event reaching its parent.

It leaves out:

- Fixing the state machine. Each counterexample is confirmed against the text, written as a scenario, and reported to the owner as an open decision. The fix lands in 0146, in 0166, or in another change.
- Timing bounds. The model states orderings; a timer is modelled as an event that may fire, and the values of the timers are outside what it checks.

## Ordering

The change starts after branch `change/0166-delegation-primitives` merges into `main`, so the model reads the delegation operations as they land. The owner set this order on 2026-11-25, and neither the merge nor this change is started yet.

## Terms

| Term | Meaning |
| :-- | :-- |
| Model | The TLA+ specification of the session state machine this change adds under `tests/formal/`. |
| Counterexample | A trace of the model that breaks one of the properties. |

## High-level design

To be written when the change starts.

## Requirements

None. The change adds a model and tests of the specification and changes no requirement.

## Current state

To be written when the change starts, from 0146, 0166, and the specification on `main` at that time.

## Detailed design

To be written when the change starts.

## Specification and non-specification changes

None to the specification. The model and its runner are non-specification files under `tests/formal/`.

## Documentation

The `formal/` entry of `tests/README.md` names the new model.

## Tests

The model's checks are the tests. Their results are recorded in this file per configuration, as 0169 records its results.
