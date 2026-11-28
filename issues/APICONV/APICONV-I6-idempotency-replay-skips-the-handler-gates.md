---
id: APICONV-I6
status: planned
change: 0178.S4
kind: build-gap
severity: medium
requirements: []
---

# An idempotent replay returns a stored response without the checks the operation runs before it executes

## Specification

§11.5 Idempotency; §11.4 User invalidation; §12.8 restriction of processing (Article 18); the creating-caller check of §13.3 (`GWSEAM-CheckCreatingCaller`). Change 0138 R5 states that the operation check runs before the replay.

## Evidence

- The REST idempotency middleware wraps the whole mux (`cmd/lenny-gateway/httpsurface.go:725`, `idempotencymw.Wrap`), and the MCP hook replays before the tool handler (`pkg/gateway/platformtools/mcp/mcp.go`).
- `requireActiveUser`, which runs `callercheck`, the §11.4 invalidation gate, and the Article 18 restriction, runs only inside the handlers (`pkg/gateway/core/session/sessionserver/create.go:89`, `create_and_start.go:134`, `derive.go:145`).
- A replay of a stored create returns the session id and a re-signed `uploadToken` (change 0173).
- Found by the review of 0178 open decision O4 on 2026-11-26.

## Gap

A bearer that the handler would refuse, such as an impersonation bearer, a soft-disabled user, or a user under an Article 18 restriction, receives a stored session-creating response, including a fresh upload token, by replaying the key.

## Suggested resolution

Run the checks a session-creating operation applies before it executes (the creating-caller check, the §11.4 gate, and the Article 18 restriction) before replaying a stored response, on the REST paths and the MCP tools that create sessions, and state the rule in §11.5.
