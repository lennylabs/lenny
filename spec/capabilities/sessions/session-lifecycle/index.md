---
code: SESLC
type: index
area: sessions
summary: >-
  Lists the pages of the session lifecycle: states, creation, suspension and dormancy, the end of a session, and timeouts.
---

# Session lifecycle

Lists the pages of the session lifecycle: states, creation, suspension and dormancy, the end of a session, and timeouts.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Session creation](session-creation.md) | `SCREATE` | States the creation flow from request to a bound pod, its atomicity, the checks at the post-chain point, and the isolation level reported to the client. |
| [Session end](session-end.md) | `SEND` | States how a session closes, the seal against further delivery, the terminal pipeline, and the window after a terminal state. |
| [Session states](session-states.md) | `SESS` | Defines the session object, its identity, its states and transitions, its failure class, and the mapping of internal states to the states clients see. |
| [Suspension and dormancy](suspension-and-dormancy.md) | `SUSP` | States when a session suspends, what happens to its slot and pod, dormancy of idle sessions, and suspension of a delegation tree. |
| [Timeouts](timeouts.md) | `TIMEOUT` | Defines every session timer and watchdog, which states pause each one, and cancellation. |

<!-- /generated:index -->
