# Steps: 0134 MCP protocol upgrade

## Validation owed

> **Revision 9.** [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15 ("Yes, let's drop connectors."). In this section, every statement about the connector client, connector links, the combined response's connector list, and progress from connectors is withdrawn and kept as the record. The statements about the gateway, the reference bridge, the management server, and the client SDKs stand.

- The `2025-11-25` tool name guidance (SEP-986), and whether it admits `/`.
- Whether a `2026-07-28` POST response may be an SSE stream, which the attach stream needs.
- The part of the `2026-07-28` `schema.ts` that was not read: `CacheableResult`, `ServerCapabilities`, and the Tasks types.
- The Tasks extension's stability and its repository's current revision.
- The spike under O2: whether the Go SDK v1.7.0 or later can host the gateway's auth middleware, Lenny's error envelope, the attach stream, and per-tenant routing. Check the release page for the current tag.
- A survey of the MCP versions that common harness clients offer, which sizes the cost of O1 for the reference bridge.

## Steps

> **Revision 9.** [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15 ("Yes, let's drop connectors."). In this section, every statement about the connector client, connector links, the combined response's connector list, and progress from connectors is withdrawn and kept as the record. The statements about the gateway, the reference bridge, the management server, and the client SDKs stand.

Under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), 0134.S1 and 0134.S2 move to the MCP adapter project, and 0134.S3 and 0134.S4 are Lenny's.

### 0134.S1 Versions and conformance
- status: Planned
- aliases: [proposal 1, MP-8, MP-1, MP-2]
- lanes: [specification, code, documentation]

"MP-8", "MP-1", and "MP-2", with the vendored schemas. Every later step depends on the per-version serializers.

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
- depends-on: [0134.S1]

"MP-5", with or after the runtime protocol design's proposal that adds the reference bridge (RP-B, 0139.S2), which supersedes revision 2's intra-pod part of "MP-5".

### 0134.S4 Management server
- status: Planned
- aliases: [proposal 4, MP-7]
- lanes: [specification, code, documentation]
- depends-on: [0134.S1]

The rest of "MP-7". "MP-6" is withdrawn since revision 9 under [`0124-connector-removal`](../0124-connector-removal/change.md).
