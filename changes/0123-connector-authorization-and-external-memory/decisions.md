# Decisions: 0123 Connector authorization and external memory

## Owner decisions

> **Partly superseded in revision 17, 2026-11-15, under [`0124-connector-removal`](../0124-connector-removal/change.md).** The owner decided on 2026-11-15 to remove connectors: "Yes, let's drop connectors." The decisions "Remove built-in memory" and "The shared OAuth flow lands first" stay. Every other row concerns connectors, and the table keeps it as the record.

These are settled by the owner, and the rationale stays here so a later reader does not reopen them by accident.

| Decision | Text or effect |
| :-- | :-- |
| Remove built-in memory | The owner asked for "Dropping memory store in Lenny + replacing with this generic mechanism". |
| No stored tool catalog | The owner rejected registering tools and arguments apart from the MCP server: "that creates a shadow config that will be out of sync". `tools/list` always asks the server. |
| The authorization failure reaches the runtime | "Shouldn't the auth error be propagated to the runtime so it knows it needs to authenticate?" The failure is translated at the gateway boundary, because the runtime's server is the adapter's local server. See "CA-5". |
| A link check runs regardless of runtime | The owner: "the Coder-style check makes sense no matter what". Coder, a cloud development environment product, requires a user to link each external account that a workspace template names before it creates or updates the workspace, unless the template marks that account optional. The owner's phrase refers to a check of that kind. |
| The trigger is a connector property | The owner replaced a pool-level "required" flag with "requiring auth to list tools". This design detects that property live, for the owner's own reason that a stored flag can drift. |
| The runtime declares tolerance | "The runtime should indicate whether they can gracefully handle failed `tools/list`". See "CA-8". |
| A parent retries after a link | No push notification. The refusal carries `details.retryAfterSeconds`. See "CA-7". |
| Impersonation and linked connectors | "Accept and document the risk." No check is added for this design's sake. See "Limits". |
| Connector tokens stay keyed by environment | The reason is separation: a token granted in one project context is not the token another context draws. Policy controls access under either key, with or without the environment, so policy is not the reason. |
| The capability snapshot stays | "Keep the snapshot and don't change anything else." A deployer who wants enforcement downstream uses OAuth scopes. See "CA-8". |
| An explicit resume checks links first | The check runs before an explicit resume of a released pod starts its new process, so a user who links gets the connector on that resume. See "CA-6". |
| The bridge reaches every runtime | The platform delivers it to every sidecar `type: agent` pod, using the runtime supervisor's delivery pattern. See "CA-9". Superseded on 2026-11-12 by [`0139-runtime-protocol`](../0139-runtime-protocol/decisions.md#decisions) D7, which settled RP O4 (a): the bridge ships with the runtime SDKs and as a release binary, and the platform delivers no copy. |
| The bridge keeps its own init container | The owner chose this over sharing `lenny-supervisor-stage`, accepting about one second per pod and leaving the supervisor untouched. See "CA-9". Superseded on 2026-11-12 with the decision above: no init container delivers the bridge. |
| The shared OAuth flow lands first | It is its own proposal, "CA-0", ahead of this design's other proposals and of [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) in [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md). "Instructions for reviewers of other designs" carries the changes this requires elsewhere. |
