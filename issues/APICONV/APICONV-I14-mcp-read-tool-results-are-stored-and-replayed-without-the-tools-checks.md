---
id: APICONV-I14
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0178.S4
---

# `/mcp` stores the result of any edge MCP tool under an idempotency key and replays it without the tool's checks

## Specification

§11.5 Idempotency lists the critical operations whose responses are stored. `lenny/create_session` and `lenny/delegate_task` are the MCP tools among them.

## Evidence

- `/mcp` is in the middleware's `AllowedPaths` (`cmd/lenny-gateway/httpsurface.go`), so a JSON-RPC POST with an `Idempotency-Key` header stores the result of whichever tool the call names for 24 hours.
- Per-tool authorization of the edge MCP tools lives inside the tool handlers. `AdmitJSONRPCReplay` (`pkg/gateway/platformtools/mcp/idempotency.go`) checks only `create_session` and `delegate_task` before a replay.
- `idempotencymw.DoNotStoreCredential` has one caller, `lenny/vcs_token` (`pkg/gateway/platformtools/mcptools/mcptools.go`); every other tool result is stored by default, including a future tool that returns a credential and omits the mark.
- Found by the security review of design C of change 0178 on 2026-11-27 (M1, M2).

## Gap

A credential of the same subject that knows the key and the body replays a stored read-tool result, such as `download_artifact` content, `get_session_logs`, or `list_sessions`, with no per-tool check. That includes data of another user's session that the storing caller could read through a role since revoked, and data of sessions deleted since. Under the record scope of 0178 revision 11 any credential of the subject shares the record; under decision 8 the same holds within one rotation chain.

## Suggested resolution

Store only the JSON-RPC `tools/call` requests of the tools whose replay admission exists, `lenny/create_session`, `lenny/create_and_start_session`, and `lenny/delegate_task`, on `/mcp` and on the MCP `idempotencyKey` path, so no other tool and no other method, such as `tools/list`, is stored; and add a static test and a contract test that no stored response carries a credential field. Change 0178 states it as IP-8.

The issue stays `open` until the owner answers O19 of change 0178; under O19 (a) or (b), step 0178.S4 closes it.

The owner answered O19 of change 0178 with option (a) on 2026-11-27 (0178 decision 18, IP-19). Step 0178.S4 closes this issue.
