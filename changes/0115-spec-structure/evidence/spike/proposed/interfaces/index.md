---
code: IFS
type: index
summary: >-
  Lists the interfaces, the parties on each side, and the page that owns each.
---

# Interfaces

An interface page states a contract that one party can be built against without reading the other party: syntax, operations, fields with types and constraints, the errors each operation returns, authentication, idempotency, and versioning. Each outcome links the requirement that decides it. Conventions shared by several interfaces live in [API conventions](../cross-cutting/api-conventions.md) and the [error model](../cross-cutting/error-model.md).

## Interfaces

| Interface | Code | Parties | Summary |
| :-- | :-- | :-- | :-- |
| [Client API](client-api/index.md) | CAPI | clients and the gateway | The client REST API: authentication, versioning, and the resource groups, with links to the shared API conventions and error model. |
| [Admin API](admin-api/index.md) | ADMIN | tenant and platform admins and the gateway | The admin REST API under /v1/admin: the conventions its operation pages rely on and the list of operation pages. |
| [Ops API](ops-api/index.md) | OPSAPI | operators, DevOps agents, the gateway, and lenny-ops | The ops API served by the gateway and lenny-ops: terminology, envelopes, pagination, rate limits, and the operation groups. |
| [Runtime protocol](runtime-protocol/index.md) | RTP | the adapter and a runtime | The runtime protocol between the adapter and a runtime: transports, capabilities in place of integration levels, versioning, and the frame families. |
| [Adapter manifest](adapter-manifest.md) | ADMAN | the adapter and a runtime | The adapter manifest a runtime reads at startup, with every field. |
| [Interceptor protocol](interceptor-protocol.md) | INTCPTIF | the gateway and a request interceptor | The protocol between the gateway and a request interceptor: phases, payloads, verdicts, and authentication. |
| [Channels](channels/index.md) | CHAN | the gateway, the adapter, the supervisor, and the stores | The gateway-to-pod channels and registers: classes, axes, the contract card format, the exclusivity model, and the wire-contract artifacts. |
| [Kubernetes](kubernetes/index.md) | K8S | Kubernetes API clients and Lenny's controllers and webhooks | Lenny's Kubernetes API conventions: the API group and version, the shipped CRDs and their schema version, field ownership, validation, and how an admission refusal reaches a client. |
| [CLI](cli/index.md) | CLI | operators and the APIs | lenny-ctl: global flags, server discovery and routing, output formats, and the command groups. |
| [Events](events/index.md) | EVT | Lenny and event consumers | The CloudEvents envelope and the transports that carry Lenny's events. |
| [Workspace plan](workspace-plan.md) | WSPLANIF | clients and the gateway | The WorkspacePlan schema with every field, and its versioning. |
| [Stored schemas](stored-schemas/index.md) | STOR | one Lenny version and the next | The stored schemas that must stay compatible across versions, and the compatibility rules for each store. |
| [Deployment configuration](deployment-configuration/index.md) | DCFG | deployers and the chart | The deployment configuration: the Helm values structure, the values schema, chart-rendered resources, and feature gates. |
| [Extension points](extension-points.md) | EXT | deployers who replace a backend and Lenny's core | The Go extension points a deployer can replace: storage backends, the pool manager, and the cluster registry. |
