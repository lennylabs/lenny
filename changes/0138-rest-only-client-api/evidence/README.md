# Evidence for 0138-rest-only-client-api

`REPORT.md` is the spike of 2026-11-13 that examined what Lenny's REST API needs so that MCP and A2A can be served by separate adapter projects in front of it. It records the protocol versions it verified, the mapping of each MCP and A2A feature onto REST, the REST additions R1 to R14, the authorization constraint, and its recommendation.

`poc/` is the proof of concept the report's "Proof of concept" section describes:

- `mock_lenny.py` mocks the REST routes the spike relies on, including R1 to R5, enforces session ownership, and keeps a small replay buffer so that a gap marker occurs.
- `adapter.py` is a stateless adapter that serves a subset of MCP `2026-07-28` with the Tasks extension at `POST /mcp`, and A2A 1.0 `message:stream` in the HTTP+JSON binding.
- `demo.py` starts the mock and two adapter replicas and alternates steps between them. `demo-output.txt` is its output from the run of 2026-11-13. Run it with `python3 -I demo.py` from `poc/`.

The run used Python's standard library only and reached no network service. The bearer tokens in `mock_lenny.py` are fixed test strings for two example users. The proof of concept does not cover the push relay, authorization audiences, the `2025-11-25` handshake era, uploads, or the A2A JSON-RPC and gRPC bindings.
