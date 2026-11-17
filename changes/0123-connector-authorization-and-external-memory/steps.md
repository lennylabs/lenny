# Steps: 0123 Connector authorization and external memory

## Validation owed

> **Withdrawn in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The text below is kept as the record of this design before that decision and no longer states Lenny's behaviour. The OIDC availability item stays live for CA-0. The other items are withdrawn.

| Item | Blocks | How |
| :-- | :-- | :-- |
| Codex behaviour on a failed `tools/list` and `list_changed` | A `deferredToolDiscovery: true` claim for Codex. The path for `deferredToolDiscovery: false` needs no answer. | Run a current, logged-in Codex headless, through the bridge, against a stub that fails `tools/list` and then notifies. Claude Code 2.1.317 is measured under "CA-9". |
| Bridge transport in a pod, on a tool connection of the slot with the peer UID check | "CA-9" | Round trip in the conformance suite, for each CLI. Claude Code passed on macOS with filesystem sockets against the earlier adapter servers. |
| Interaction store lookup by tenant, user, connector, and environment | "CA-1" step 6 | Read `interactionstore` at proposal time. A new index may be needed. |
| OIDC availability on the start page in `apiKey` and dev modes, where a user authenticates with an API key or, under `global.devMode=true`, with no credential, rather than through an OIDC login | "CA-0" | Settle it in that proposal, since the start page is shared with UC-2. |
| MCP resource indicator requirement | "CA-2" | Read the MCP authorization page, and test a server that enforces audience. |

## Steps

> **Revised in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** CA-0 and CA-G stay live. CA-A to CA-F, CA-H, and CA-I are withdrawn with the connectors they land. CA-G no longer waits on CA-A to CA-F: it depends on CR-A of the removal design, so that the documented egress pattern replaces built-in memory when memory goes. The table keeps the earlier order as the record.

### 0123.S1 Shared OAuth link flow
- status: Planned
- aliases: [CA-0]
- lanes: [specification, code, documentation]
- closes: [F-9.3.18]

Shared OAuth link flow ([CA-0](change.md#ca-0-shared-oauth-link-flow)) in `pkg/connectoroauth`, with today's §9.3 handlers converted to it. Closes the account-link defect. It lands first, and [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) depends on it.

[`0151-user-credential-delivery`, "Defects found while verifying"](../0151-user-credential-delivery/change.md#defects-found-while-verifying) rates the account-link defect High wherever a connector is a confidential client, which is why this step comes first.

### 0123.S2 Memory removal and external pattern documentation
- status: Planned
- aliases: [CA-G, CA-10]
- lanes: [specification, code, documentation]
- depends-on: [0124.S1]

Memory removal ([CA-10](change.md#ca-10-removing-built-in-memory)) and the external pattern documentation. It depends on CR-A of the removal design so that the documented egress pattern replaces built-in memory when memory goes. Before revision 17 it depended on CA-A through CA-F, so that the documented replacement worked when memory went.

### Notes on the steps

The withdrawn proposals keep their rows below as the record, in the earlier order, between 0123.S1 (CA-0) and 0123.S2 (CA-G) for CA-A to CA-F and after 0123.S2 for CA-H and CA-I. None of them is a step.

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| CA-A | Connector routes ([CA-1](change.md#ca-1-link-flow)), and the removal of the admin OAuth routes. | 0123.S1 (CA-0) |
| CA-B | Refresh, lease, and endpoint binding ([CA-2](change.md#ca-2-token-lifecycle)). | CA-A |
| CA-C | Default deny and environment on children and derived sessions ([CA-3](change.md#ca-3-connector-access-and-environment)). | The [tenant-scoping decision](../0149-tenant-scoping-identity/decisions.md#decisions) |
| CA-D | Typed error and connector prompts ([CA-5](change.md#ca-5-propagating-the-failure)). | CA-A |
| CA-E | Link check at create, derive, and external adapters, plus `deferConnectors` and the combined missing-links response ([CA-4](change.md#ca-4-link-check)). | CA-A, CA-C, and the interceptor contract proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps), 0141.S1, which adds the post-chain point |
| CA-F | Resume and restart rules ([CA-6](change.md#ca-6-resume-and-restart)), and delegation ([CA-7](change.md#ca-7-delegated-children)). | CA-D, CA-E |
| CA-H | The Claude Code and Codex wrappers' MCP configuration from `toolServers`, with `lenny-mcp-bridge` in their images ([CA-9](change.md#ca-9-stdio-bridge)). The bridge itself lands in the runtime protocol design's proposals. | The reference bridge of [`0139-runtime-protocol`](../0139-runtime-protocol/steps.md#steps), and ships with the Claude Code and Codex integrations |
| CA-I | The `deferredToolDiscovery` true path: the link callback's `UpdateAvailability` call through `CH-HOLDERFORWARD`, and the compliance check ([CA-8](change.md#ca-8-runtime-capability-and-late-tools)). The field and its `false` default land in CA-E. | The availability notice of [`0139-runtime-protocol`](../0139-runtime-protocol/steps.md#steps), and the stock CLI spike or a runtime asking for it |

CA-C's default deny and the child environment fix close a capability-filter bypass that exists today, and they also stand alone.
