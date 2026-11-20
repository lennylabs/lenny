# Steps: 0134 MCP protocol upgrade

## Validation owed

> **Revision 9.** [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15 ("Yes, let's drop connectors."). In this section, every statement about the connector client, connector links, the combined response's connector list, and progress from connectors is withdrawn and kept as the record. The statements about the gateway, the reference bridge, the management server, and the client SDKs stand.

Each item names the step that waits on it. An item that only the gateway's `/mcp` needs waits on 0134.S1 or 0134.S2, which move to the MCP adapter project, so the adapter project owes it.

- The `2025-11-25` tool name guidance (SEP-986), and whether it admits `/`. Waits: 0134.S1 (the MCP adapter project). The bridge's tool names are the unprefixed frame names of "MD-23" of `0135-message-delivery`.
- Whether a `2026-07-28` POST response may be an SSE stream, which the attach stream needs. Waits: 0134.S2 (the MCP adapter project).
- The part of the `2026-07-28` `schema.ts` that was not read: `CacheableResult`, `ServerCapabilities`, and the Tasks types. Waits: 0134.S5, 0134.S3, and 0134.S4 for `CacheableResult` and `ServerCapabilities`, and 0134.S2 (the MCP adapter project) for the Tasks types.
- The Tasks extension's stability and its repository's current revision. Waits: 0134.S2 (the MCP adapter project).
- The spike under O2: whether the Go SDK v1.7.0 or later can host the gateway's auth middleware, Lenny's error envelope, the attach stream, and per-tenant routing. Check the release page for the current tag. Waits: 0134.S1 (the MCP adapter project). Whether and how the spike applies to the bridge and the management server is open decision O11, which 0134.S3 and 0134.S4 wait on.
- A survey of the MCP versions that common harness clients offer, which sizes the cost of O1 for the reference bridge. Waits: 0134.S3.

## Steps

> **Revision 9.** [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15 ("Yes, let's drop connectors."). In this section, every statement about the connector client, connector links, the combined response's connector list, and progress from connectors is withdrawn and kept as the record. The statements about the gateway, the reference bridge, the management server, and the client SDKs stand.

Under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), 0134.S1 and 0134.S2 move to the MCP adapter project, and 0134.S3, 0134.S4, and 0134.S5 are Lenny's. The status of 0134.S1 and 0134.S2 in Lenny's records is open decision O8. Revision 11 adds 0134.S5, which holds the Lenny work that 0134.S1 carried, and removes 0134.S1 from the `depends-on` of 0134.S3 and 0134.S4.

### 0134.S1 Versions and conformance
- status: Planned
- aliases: [proposal 1, MP-8, MP-1, MP-2]
- lanes: [specification, code, documentation]

"MP-8", "MP-1", and "MP-2" for the gateway's `/mcp`. 0134.S2 depends on the gateway's per-version serializers. The vendored schemas and the part of "MP-8" that is Lenny's land in 0134.S5.

### 0134.S2 Elicitation, Tasks, and playground
- status: Planned
- aliases: [proposal 2, MP-3, MP-4, MP-9]
- lanes: [specification, code, documentation]
- depends-on: [0134.S1]

"MP-3", "MP-4", and "MP-9". The playground moves once the Tasks extension exists. "MP-3"'s combined missing-links response lands with or after the link check of user credential delivery.

### 0134.S3 Bridge
- status: Planned
- aliases: [proposal 3, MP-5]
- lanes: [specification, code, documentation]
- depends-on: [0134.S5, 0139.S2, 0139.S3]

"MP-5", MPR-5, and MPR-6, with or after the runtime protocol design's proposal that adds the reference bridge (RP-B, 0139.S2), which supersedes revision 2's intra-pod part of "MP-5". The bridge's code arrives with 0139.S3 (RP-C). This step adds the era selection, the version refusal, and the result format of "MP-5" to the bridges of the Go, Python, and TypeScript runtime SDKs and to `lenny-mcp-bridge`, and it lands on 0139.S3's branch, so that the bridge never ships at a single MCP version. It reads the schemas that 0134.S5 vendors.

### 0134.S4 Management server
- status: Planned
- aliases: [proposal 4, MP-7]
- lanes: [specification, code, documentation]
- depends-on: [0134.S5]
- closes: [T-25.12.6]

The rest of "MP-7", with MPR-7: the management server's versions, its era selection and refusals, and `subscriptions/listen`, which closes T-25.12.6. It reads the schemas that 0134.S5 vendors. "MP-6" is withdrawn since revision 9 under [`0124-connector-removal`](../0124-connector-removal/change.md).

### 0134.S5 Vendored schemas and the version rule
- status: Planned
- aliases: [MPR-8]
- lanes: [specification, code]

The Lenny work that 0134.S1 carried before revision 11: the `2025-11-25` and `2026-07-28` schemas under `tests/testdata/mcp/schema/` with a schema-validation helper that the tests of 0134.S3 and 0134.S4 use ("MP-7" **Schemas.**), and the part of "MP-8" that is Lenny's, stated as MPR-8 on the page that O10 settles.
