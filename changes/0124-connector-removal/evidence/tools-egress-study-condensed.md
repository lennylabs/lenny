# Study 2.5, condensed: tool connectors or network egress for external MCP tool servers

This is a condensed copy of a read-only design study written on 2026-11-15 on branch `design/v1-cleanup`. The full study is about 400 lines and lived in a session scratchpad. This copy keeps its question, its recommendation, the properties it compared, its risks, its rejected option C, its open decisions, its validation items, and its sources. It drops the per-design change tables, which `0124-connector-removal` replaces with its own sweep. Claims about MCP and about harnesses were checked on 2026-11-15 against the pages under "Sources", and a claim marked **[unverified]** was not confirmed on a live page.

The owner's decision of 2026-11-15 went further than the study's option B. The study kept a Lenny registration of each MCP server, an MCP rule family in `netagent`, approvals held in `netagent`, and a per-call record. The owner removed connectors with no Lenny catalog and left tool rules, approvals, and per-call audit to the operator's own service ("Yes, let's drop connectors"). `0124-connector-removal` records the decision and the parts of the study it keeps.

## Question

Should Lenny keep tool connectors, the external MCP servers a deployer registers under §9.3 Connector Definition and OAuth/OIDC and that the gateway calls for the runtime through `CallConnectorTool`, or should an external MCP tool server become ordinary network egress, in which the harness in the pod speaks MCP to the server and `netagent` attaches the user's token? The study left external agents (A2A) gateway-mediated as remote child sessions. The owner later removed those too.

## Options

- **A.** Keep tool connectors and add the fidelity work of study 1 (widened descriptors and results, `residue`, `render_report`, `tool_progress`, and MP-6 handling of `input_required` and Tasks).
- **B.** Retire tool connectors. An MCP server becomes a credential binding on its host, an entry the wrapper writes into the harness's MCP configuration, and an MCP rule set in `netagent`. Platform tools stay on tool frames and the reference bridge.
- **C.** A pass-through MCP proxy in the gateway that forwards JSON-RPC verbatim and inspects `tools/call`.

## Recommendation of the study

Option B, with five additions:

- **B1.** A link kind for MCP authorization, because UC-2's `kind: oauth2` requires a `userInfoEndpoint` and sends no `resource` parameter, which MCP `2026-07-28` requires.
- **B2.** Pod reuse for `delivery: inject` user bindings, because UC-3 refuses a `userCredential` binding on recycled pods, used pods, and multi-slot pods without per-slot attribution.
- **B3.** Session activity, seal, and suspension for MCP calls, because SL-6, §4.7.1 **Session seal.**, and SL-3 see only tool frames and the gateway's dispatch.
- **B4.** A prompt to the human when a link is missing or lapses mid-session.
- **B5.** A decision on approval holds against harness timeouts, because Claude Code's HTTP MCP requests time out after 60 seconds by default.

The decisive reasons for B:

1. **Fidelity becomes the harness's.** Structured results, output schemas, annotations, `_meta`, resources, progress, Tasks, the `2026-07-28` input-required results, and every later MCP revision reach the harness without a Lenny translation.
2. **The traffic enters the egress controls.** A connector call runs on the gateway, outside `netagent`, `lenny-egress`, the bytes-out alert, `egressLockHosts`, and credential pinning. Under B it passes all of them, and an MCP server on a private network becomes reachable through an outbound connector route.
3. **One credential mechanism.** Per-user MCP tokens become account links in the Token Service, delivered with `delivery: inject`, which removes the gateway's second token store (F-4.3.1) and brings MCP use under UC-3's impersonation check.
4. **Smaller platform surface.** About 3,750 lines of non-test Go in `pkg/gateway/connectors` leave, with the connector RPCs, the probe, the capability snapshot, connector prompts, and the late-tool machinery.

Option A is better only if approval and audit evidence must be produced outside the pod (`enforced` provenance) and deployer interceptors must see each tool call and result.

## Properties compared

| Property | Today (A) | Under B |
| :-- | :-- | :-- |
| Tool-use approvals (SO-5a) | Held at the gateway's tool dispatch. | Moves into `netagent`, as `self_reported` evidence. |
| Per-call audit | No per-call audit event; FE-6a records gateway-carried calls as `enforced`. | `netagent` records each call as `self_reported`; `lenny-egress` records the tunnel as `enforced`. |
| Catalog and scope | Default deny through a `DelegationPolicy` target of type `connector`; tokens keyed by tenant, connector, user, and environment. | A registration and its binding scoped by TS-1 `environments`. |
| Link check at creation | Probe, `403 LINKS_REQUIRED` with `details.connectors`, and `deferConnectors`. | UC-3's check with `details.userLinks`; `required: false` is the deferral. |
| Connector prompts (CA-5) | One URL-mode elicitation per tree and connector. | Lost unless B4 is added. |
| Late tools (CA-8) | `deferredToolDiscovery` and `availability_changed`. | Lost; the loss is small. |
| Progress of a long call | `tool_progress` under `toolProgress`. | Native MCP progress from the server to the harness. |
| Stdio bridge | Needed for connectors and platform tools. | Needed for platform tools alone. |
| External memory (CA-10) | A memory server registered as a connector. | A memory server reached through egress. |
| Token custody | Tokens in the gateway. | Access token in `netagent` memory; refresh token in the Token Service. |
| Session activity, seal, suspension | An in-flight connector call is activity, holds the seal, and is cancelled on suspension. | Lost unless B3 is added. |
| Interceptors on calls and results | `PreConnectorRequest` and `PostConnectorResponse`. | Lost. |
| Private-network MCP servers | Unreachable. | Reachable through an outbound connector route. |
| Impersonation | Accepted risk. | Closed by UC-3's check for `userCredential` bindings. |
| Exfiltration controls | None on the tool path. | Every egress control applies. |
| Recycled and multi-slot pods | Connectors work on every pod. | Per-user servers lose these pods unless B2 is added. |

## Risks of B

Each risk is rated High (blocks B without the named addition), Medium, or Low.

- **R1, Medium.** The approval carrier from `netagent` to the gateway's approvals gate needs its own intra-pod channel identifier.
- **R2, Medium.** Enforcement and evidence move inside the pod, so approvals and call records become `self_reported`.
- **R3, High without B2.** Pod reuse and slot density for per-user servers.
- **R4, High without B3.** An MCP call through `netagent` writes no frame, so it does not reset the idle clock, the seal can take while it runs, and SL-3 cannot cancel it.
- **R5, Medium.** Harness timeouts against approval holds: Claude Code's HTTP request limit of 60 seconds, and Codex's `tool_timeout_sec` of 60 seconds **[unverified]**.
- **R6, Medium.** The harness must take a remote Streamable HTTP server, send it through `HTTPS_PROXY`, and trust the per-pod CA. Claude Code documents the proxy variables and `NODE_EXTRA_CA_CERTS`; whether the proxy applies to its MCP traffic is **[unverified]**, and Codex's MCP proxy behaviour is **[unverified]**.
- **R7, Medium.** MCP parsing in `netagent` must handle both protocol eras, batches, input-required retries, and Tasks.
- **R8, Medium.** Elicitation and sampling reach a harness with no human.
- **R9, Low.** Token scope: MCP `2026-07-28` requires the `resource` parameter and audience validation, and one `inject` binding per host limits two servers on one host.
- **R10, Low.** A prompt-injected agent can reach any host the egress policy allows, as it can today.
- **R11, Medium.** No deployer hook scans tool results.
- **R12, Low.** Children receive a `userCredential` binding only when the lease names it.
- **R13, Low with B2.** Slot attribution on multi-slot pods relies on owner UID attribution, measured on runc and owed on gVisor and Kata.

## Option C

A pass-through MCP proxy in the gateway keeps tokens in the gateway, `enforced` approvals and audit, the connector interceptors, elicitation routing to the human, and use on every pod, with near-verbatim fidelity. It costs a pod-to-gateway carrier for raw JSON-RPC (a local HTTP listener in the adapter was refused, and wrapping each message in a frame was rejected by study 1), keeps the gateway in the data path of every long call, and keeps the egress bypass, the private-network gap, F-4.3.1, and the impersonation gap. C is the right choice only if the owner requires `enforced` approvals and audit, or deployer result interceptors, for external MCP tools.

## Open decisions of the study

| # | Decision | Recommendation |
| :-- | :-- | :-- |
| O1 | Retire tool connectors. | B, with B1 to B5. |
| O2 | The MCP link kind. | A UC-2 kind `mcp` with RFC 9728 discovery and the `resource` parameter. |
| O3 | How a session receives an MCP registration. | A binding scope that attaches a registration to every session its environment and policy grant. |
| O4 | Link key per user or per user and environment. | Per user. |
| O5 | Children and MCP registrations. | Inherit, narrowed by policy. |
| O6 | Pod reuse for inject-only user bindings. | Lift the recycle and used-pod refusals; keep the multi-slot refusal on pools without attribution. |
| O7 | Tool filtering. | Name allow and deny only. |
| O8 | `toolProgress`, `tool_progress`, `gateway.toolProgressMinIntervalMs`, `deferredToolDiscovery`, and `server`. | Remove the first four; keep `server` with the single value `platform`. |
| O9 | `details.connectors` in `LINKS_REQUIRED`. | Remove it unless agent links stay in the gateway. |
| O10 | Per-registration call rate bound. | None for now. |
| O11 | Result scanning. | Accept and document the loss of `PostConnectorResponse`. |
| O12 | Client capability rewrite in `netagent`. | Remove `sampling` always and URL-mode elicitation always. |
| O13 | A stdio-to-HTTP relay for harnesses without HTTP MCP. | Not until a harness needs it. |
| O14 | Two MCP servers on one host with different tokens. | One `inject` binding per host for v1. |
| O15 | Names after tool connectors leave. | Retire the bare word "connector" with a successor pointer; "outbound connector" keeps its name. |
| O16 | Where external agent credentials live. | The Token Service. |
| O17 | Approval hold mode. | Per rule, defaulting to hold. |
| O18 | Evidence for MCP calls. | `agent.activity.tool` with a `netagent` producer. |

## Validation owed by the study

- Claude Code and Codex each reach a stub Streamable HTTP MCP server through `netagent` with `HTTPS_PROXY`, the per-pod CA, and an injected token, including a held call of 90 seconds and of 30 minutes.
- Whether Claude Code's per-server `timeout` lifts the 60-second HTTP request limit.
- Codex's MCP HTTP options on the live page.
- The MCP rule family against both protocol eras, the Tasks extension, and an input-required retry.
- Per-slot attribution on gVisor and Kata (V10).

## Sources

Read on 2026-11-15.

- MCP versioning: https://modelcontextprotocol.io/specification/versioning
- MCP authorization, `2026-07-28`: https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization
- MCP client registration: https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/client-registration
- MCP Streamable HTTP, `2026-07-28`: https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http
- MCP progress: https://modelcontextprotocol.io/specification/2026-07-28/basic/utilities/progress
- MCP elicitation: https://modelcontextprotocol.io/specification/2026-07-28/client/elicitation
- MCP Tasks extension: https://modelcontextprotocol.io/extensions/tasks/overview.md
- Claude Code MCP: https://code.claude.com/docs/en/mcp
- Claude Code environment variables: https://code.claude.com/docs/en/env-vars
- Claude Code network configuration: https://code.claude.com/docs/en/network-config
- Codex environment variables: https://learn.chatgpt.com/docs/config-file/environment-variables
- Codex MCP and configuration reference: https://learn.chatgpt.com/docs/extend/mcp?surface=cli and https://learn.chatgpt.com/docs/config-file/config-reference, read through a summary.
- Gemini CLI MCP: https://geminicli.com/docs/tools/mcp-server/
