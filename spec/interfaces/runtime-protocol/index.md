---
code: RTP
type: index
summary: >-
  The runtime protocol between the adapter and a runtime: transports, capabilities in place of integration levels, versioning, and the frame families.
status: placeholder
---

# Runtime protocol

The runtime protocol between the adapter and a runtime: transports, capabilities in place of integration levels, versioning, and the frame families.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Conformance](conformance.md) | `RTPCONF` | The conformance suite by capability, how it is packaged, and how failures are categorized. |
| [Filesystem and environment](filesystem-and-environment.md) | `RTPFS` | The paths, environment variables, and injected configuration a runtime sees in its slot. |
| [Handshake and capabilities](handshake-and-capabilities.md) | `RTPHAND` | The handshake frames, the capabilities a runtime declares, and their dependencies. |
| [Message parts](message-parts.md) | `RTPPART` | The MessagePart format, the message envelope fields, binary input and output, and the mapping from MCP content blocks. |
| [Messages](messages.md) | `RTPMSG` | The message socket frames, the MessagePart format, tool and frame names, exit codes, and leniency rules. |
| [Platform tools](platform-tools.md) | `RTPTOOL` | The platform tools a runtime calls, through frames or the reference MCP bridge, with schemas including the delegation tools and the task result. |
| [Runtime ops](runtime-ops.md) | `RTPOPS` | The runtime-ops channel frames: quiesce, interrupt acknowledgement, and the remaining rotation frames. |
| [Runtime SDKs](runtime-sdks.md) | `RTPSDK` | The runtime author SDKs: packages, what each adds to the protocol, and what each leaves to the runtime. |

<!-- /generated:index -->

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§4.7.4 Adapter ↔ Runtime Protocol (Intra-Pod)](../../04_system-components.md#474-adapter--runtime-protocol-intra-pod)
- [§4.7.7 Runtime Integration Levels (agent-type only)](../../04_system-components.md#477-runtime-integration-levels-agent-type-only)
- [§15.4 Runtime Adapter Specification](../../15_external-api-surface.md#154-runtime-adapter-specification), in part
- [§15.4.3 Runtime Integration Levels](../../15_external-api-surface.md#1543-runtime-integration-levels), in part

Changes of the v1 program whose content lands on this page:

- [0139-runtime-protocol](../../../changes/0139-runtime-protocol/change.md): Requirements, RP-6, RP-7
