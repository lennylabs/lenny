---
code: IFS
type: index
summary: >-
  Lists the interfaces, the parties on each side, and the page that owns each.
---

# Interfaces

Lists the interfaces, the parties on each side, and the page that owns each.

## Pages

<!-- generated:index -->

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Adapter manifest](adapter-manifest.md) | `ADMAN` | The adapter manifest a runtime reads at startup, with every field. |
| [Admin API](admin-api/index.md) | `ADMIN` | The admin REST API under /v1/admin: the conventions its operation pages rely on and the list of operation pages. |
| [Channels](channels/index.md) | `CHAN` | The gateway-to-pod channels and registers: classes, axes, the contract card format, the exclusivity model, and the wire-contract artifacts. |
| [CLI](cli/index.md) | `CLI` | lenny-ctl: global flags, server discovery and routing, output formats, and the command groups. |
| [Client API](client-api/index.md) | `CAPI` | The client REST API: authentication, versioning, and the resource groups, with links to the shared API conventions and error model. |
| [Deployment configuration](deployment-configuration/index.md) | `DCFG` | The deployment configuration: the Helm values structure, the values schema, chart-rendered resources, and feature gates. |
| [Events](events/index.md) | `EVT` | The CloudEvents envelope and the transports that carry Lenny's events. |
| [Extension points](extension-points.md) | `EXT` | The Go extension points a deployer can replace: storage backends, the pool manager, and the cluster registry. |
| [Gateway seams](gateway-seams.md) | `GWSEAM` | The boundary between each gateway seam and the session core: the operations each side calls on the other, the values they carry, and the route registration interface of the API front. |
| [Interceptor protocol](interceptor-protocol.md) | `INTCPTIF` | The protocol between the gateway and a request interceptor: phases, payloads, verdicts, and authentication. |
| [Kubernetes](kubernetes/index.md) | `KAPI` | Lenny's Kubernetes API conventions: the API group and version, the shipped CRDs and their schema version, field ownership, validation, and how an admission refusal reaches a client. |
| [Ops API](ops-api/index.md) | `OPSAPI` | The ops API served by the gateway and lenny-ops: terminology, envelopes, pagination, rate limits, and the operation groups. |
| [Runtime protocol](runtime-protocol/index.md) | `RTP` | The runtime protocol between the adapter and a runtime: transports, capabilities in place of integration levels, versioning, and the frame families. |
| [Stored schemas](stored-schemas/index.md) | `STOR` | The stored schemas that are kept compatible across versions, and the compatibility rules for each store. |
| [Workspace plan](workspace-plan.md) | `WSPLANIF` | The WorkspacePlan schema with every field, and its versioning. |

<!-- /generated:index -->
