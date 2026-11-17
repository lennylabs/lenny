---
code: CAPI
type: index
summary: >-
  The client REST API: authentication, versioning, and the resource groups, with links to the shared API conventions and error model.
status: placeholder
---

# Client API

The client REST API: authentication, versioning, and the resource groups, with links to the shared API conventions and error model.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Credentials and links](credentials-and-links.md) | `CAPICRED` | User credential and account link operations. |
| [Discovery and usage](discovery-and-usage.md) | `CAPIDISC` | Runtime discovery, published metadata, and usage reporting. |
| [Event stream](event-stream.md) | `CAPIEVT` | The session event stream endpoint, the event kind registry, resume parameters, and keepalive. |
| [Files and artifacts](files-and-artifacts.md) | `CAPIFILE` | Upload, artifact, blob, and export operations. |
| [Messages](messages.md) | `CAPIMSG` | Message operations: send, retract, list, respond to input requests, and client tool results. |
| [Sessions](sessions.md) | `CAPISES` | Session operations: create, start, get, list, interrupt, terminate, delete, derive, and the states and errors each returns. |

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§15.1 REST API](../../15_external-api-surface.md#151-rest-api), in part
- [§15.2.1 REST/MCP Consistency Contract](../../15_external-api-surface.md#1521-restmcp-consistency-contract), in part
- [§15.5 API Versioning and Stability](../../15_external-api-surface.md#155-api-versioning-and-stability), in part
- [§15.6 Client SDKs](../../15_external-api-surface.md#156-client-sdks), in part

Changes of the v1 program whose content lands on this page:

- [0138-rest-only-client-api](../../../changes/0138-rest-only-client-api/change.md): Requirements, RO-1, RO-6
