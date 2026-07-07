# Vendored MCP protocol schemas

§15.2 "Version negotiation": Lenny concurrently serves the current
(`2025-03-26`) and previous (`2024-11-05`) MCP spec versions. The two
files under `schema/` are the published `schema.json` for each
revision, vendored verbatim (no `$ref` rewriting; both files are
already self-contained under their own `definitions` block) from
[`modelcontextprotocol/modelcontextprotocol`](https://github.com/modelcontextprotocol/modelcontextprotocol):

- `schema/2025-03-26.schema.json` — commit `ef5ebfaaac37d6086f522aeed29015dda0403359`
  (2025-04-22), path `schema/2025-03-26/schema.json`.
- `schema/2024-11-05.schema.json` — commit `dfc88487d491ae1f3e21963b55f56be073cc1859`
  (2025-02-21), path `schema/2024-11-05/schema.json`.

Vendored 2026-07-06.

`tests/tier3_contract/rest_mcp_consistency/published_schema_test.go`
validates the gateway-edge `/mcp` `initialize` result and `tools/list`
catalog against the `InitializeResult` and `Tool`/`ListToolsResult`
definitions in these files via `tests/testinfra/schematest`, so the
wire frames are checked against the MCP specification's own schema
and not only against Lenny's `map[string]any` construction or its
internal `Tool` struct tags.

Neither published revision defines an `elicitation/create` method, a
`Task`/`notifications/tasks/statusUpdate` type, or the
`notifications/lenny/*` extension namespace — Elicitation was added in
a later MCP revision, and Tasks and the `notifications/lenny/*`
extension have no published MCP schema counterpart at any revision.
The per-kind `SessionEventKind` → MCP wire frame projection those
methods belong to (spec/15_external-api-surface.md "MCPAdapter
OutboundChannel mapping") has no corresponding non-test source yet
(`pkg/gateway/mcpfabric/mcp/mcp.go` marks it a follow-on) and so is out
of scope for a schema-conformance test against these files.
