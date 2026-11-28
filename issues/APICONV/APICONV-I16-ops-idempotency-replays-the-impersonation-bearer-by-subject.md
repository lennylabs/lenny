---
id: APICONV-I16
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0178.S6
---

# lenny-ops idempotency stores and replays the impersonation bearer to any caller with the same subject

## Specification

§25.4 The `lenny-ops` Service, **Idempotency**: `lenny-ops` keys a record by `(key, caller_id)`, where `caller_id` is the OIDC `sub` claim of the requesting service account, and returns the stored response without executing again. §11.5 **Stored credentials.** keeps credentials out of the gateway's stored responses; §25.4 states no such rule.

## Evidence

- `opsidem` wraps every POST to `lenny-ops`, including `/mcp/management` (`pkg/ops/opsserver/opsserver.go`), and stores the response as JSONB keyed by `(key, caller_id)` (`pkg/ops/opsidem/opsidem.go`).
- The management MCP tool `admin.start_impersonation` (`pkg/ops/mcp/generated_tools.go`) proxies `POST /v1/admin/impersonation`, whose response carries `bearerToken` (`pkg/gateway/api/admin/impersonation.go`), and `invokeGateway` passes the body through unchanged (`pkg/ops/opsserver/mcp.go`).
- The `Claim` of each store keys a record by `(key, caller_id)` alone (`pkg/ops/opsidem/memory.go`, `pkg/ops/opsidem/pgstore/pgstore.go`). The middleware passes the endpoint, `method path`, and the store records it without comparing it, and no hash of the request body is computed (`pkg/ops/opsidem/opsidem.go`). A POST with the same key to any `lenny-ops` path therefore replays the stored response, the impersonation bearer included.
- `opsidem` wraps the whole mux (`opsserver.go`), and the MCP bridge's per-tool scope gate runs inside it (`pkg/ops/mcp/registry.go`), so a replay through `/mcp/management` returns before the scope gate runs, and a narrowed token of the same service account receives the stored bearer.
- Found by the security review of design C of change 0178 on 2026-11-27, and widened by the review of 0178 revision 11 on the same day. The defect predates 0178, and 0178 leaves the `lenny-ops` records out of its scope.

## Gap

Any holder of a token with the same service-account `sub` that knows the key replays a live impersonation bearer, through any `lenny-ops` path and whatever its scope, and no `admin.impersonation_started` audit row is written for the replay. The gateway's high-level rule that no stored response carries a credential, which change 0178 states, holds for the gateway only.

## Suggested resolution

State in §25.4 that a record is matched on the key, the caller, the endpoint, and a SHA-256 of the request body, and that a mismatch answers `422 IDEMPOTENCY_KEY_REUSED`; that a response carrying a credential is not stored, as §11.5 does for the gateway; and that a replay through the MCP bridge runs the bridge's scope gate first. Have `opsidem` compare the endpoint and the body hash, skip storing a response that a handler marks as carrying a credential, with the impersonation proxy and any tool whose response carries a token marked, and run the scope gate before a replay. Add tests that a replay of `admin.start_impersonation` executes again and writes its audit row, that the same key on another path is refused, and that a token without the tool's scope is refused on a replay.

Open decision O20 of change 0178 asks whether a new step of 0178 fixes it, a change of its own does, or the issue stays unscheduled.

The owner answered O20 of change 0178 with option (a) on 2026-11-27 (0178 decision 19, IP-20): step 0178.S6 fixes the `lenny-ops` records and closes this issue. The owner asked for an in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design, which started on 2026-11-27; the step starts after it reports.
