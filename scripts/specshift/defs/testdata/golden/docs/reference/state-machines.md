---
title: State Machines
---

# State Machines

Lists the state machines.

## Session state machine

A hand-written section.

<!-- generated:reference-state-machines -->

## Pod phase

The phase of an agent pod. The states belong to the agent pod.

States, in lifecycle order: `warming`, `idle`, `active`, `draining`. A new agent pod starts in `warming`. Terminal states: `draining`.

| From | To | Trigger |
| :-- | :-- | :-- |
| `warming` | `idle` | ready |
| `idle` | `active` |  |
| `idle`, `active` | `draining` |  |

<!-- /generated:reference-state-machines -->
