# Steps: 0138 REST-only client API

## Validation owed

- Whether MCP clients in common use, with their authorization servers, obtain a token whose audience is `https://<host>/mcp` through RFC 8707, and whether such a client accepts the RFC 9728 document R9 serves; TS's "Residual risks" already lists the per-tenant case as unproven.
- Whether the identity providers in common use write the requested RFC 8707 resource, or a configured resource mapping, into `aud`, and which need a client ID alias ("RO-4").
- Whether common ingress controllers strip client-sent `Lenny-*`, `X-Forwarded-*`, and method-override headers as "RO-4" requires, and route `downloads.host` and external origins as "RO-5" states.
- Whether a restored `dormant` session holds the conversation state a warm turn needs for each first-party runtime, which O12's reading of R16 assumes.
- The latency and throughput cost of the extra hop for streaming and for a provider request, measured on Kind with the MCP and Anthropic adapters.
- Whether common ingress controllers route the protocol paths of "RO-5" without buffering SSE, and pass `Last-Event-ID` and `Mcp-*` headers unchanged.
- The proof of concept did not cover the push relay, R9, the adapter listener, narrowing, R13, R19, the `2025-11-25` era, uploads, the A2A JSON-RPC and gRPC bindings, or any provider adapter; each needs a test in its repository or in Lenny before the adapter replaces the in-gateway surface.
- Whether the Anthropic SDKs' retries reach the join of "RO-10" before the first request's message is written, the window in which a retry sends a second message; R5's key closes it when the adapter sends the request digest as `Idempotency-Key` as well.

## Steps

### 0138.S1 REST additions
- status: Planned
- aliases: [RO-A, RO-2, R1, R2, R3, R4, R5, R7, R8, R12, R13, R14, R15, R16, R17, R19]
- lanes: [specification, code, documentation]
- depends-on: [0135.S5, 0146.S1, 0122.S1, 0120.S1, 0139.S1 (part), 0141.S2 (part), 0142.S6 (part)]
- closes: [APICONV-I1]

The REST additions every adapter needs: R1 to R5, R7, R8, R12 to R17, and R19, R13's download origin, the event stream's rules moved from §15.2 with its close at expiry and revocation, and their tests. It needs MD-E's events, SL's routes, CT-A, and AM-A, so that the message and conversation members exist. It needs the effective capabilities of RP-2 (0139.S1) for R19, the aliases of RT-4's "Discovery" (0141.S2) for R8, and the tool-use approvals of SO-5a (0142.S6) for R4's approval list.

R5's key scope changes `Key` in `pkg/idempotency` from the tenant and the key to the scope R5 states, with a migration of `idempotency_keys`, which §11.5 keys by `(tenant_id, idempotency_key)` today. R4's link-prompt entries of UC-8 land with 0151.S8, which depends on this step and adds them to R4's lists. R19 sends the `Interrupt` RPC, whose check of the coordination generation is open as [CHGWPOD-I10](../../issues/CHGWPOD/CHGWPOD-I10-interrupt-ignores-coordination-generation.md).

This step goes first because every adapter and every Lenny client depends on it.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-PendingInputWaits` so that it returns a session's pending elicitation entries with their provenance in place of their identifiers (R4), and adds the list of a session's pending approvals (R4), a crossing that has no operation yet. It reshapes `GWSEAM-HandleRoute` and `GWSEAM-RegisterRoutes` for the route tags (R7) and for the download listener of R13, which `GWSEAM-HandleRoute` does not know today. For the stream close of "RO-1", it reshapes `GWSEAM-RequestCaller` so that the caller it returns carries the bearer's expiry, and gives `GWSEAM-IsBearerRevoked` the stream proxy as a caller, under the revocation sources that open decision O15 settles. R1's `seq` is the sequence number that `GWSEAM-Publish` already returns, so that operation keeps its form.

### 0138.S2 Authorization of adapter requests
- status: Planned
- aliases: [RO-B, RO-4, R9]
- lanes: [specification, code, documentation]
- depends-on: [0149.S2, 0149.S7, 0138.S1]
- closes: [F-10.2.39]

R9 and the rules of "RO-4": the adapter registry, computed identifiers, audiences, metadata, challenges, narrowing, evaluation, leaf enforcement, minting refusals, the refusal code, the adapter listener, and their tests. It needs 0138.S1 for R7's tags and R5's key scope.

F-10.2.39 closes here. 0149.S2 of [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/steps.md#steps) closes its deployment metadata and challenge part, and this step closes the rest with the adapter identifiers of R9. The minting refusals include the impersonation mint's refusal of an `adapter-only` token and of a narrowed request ("RO-4", **Minting refusals**), which this step builds and 0142.S5 of [`0142-security-oversight`](../0142-security-oversight/steps.md#steps) keeps. Whichever of this step and 0151.S3 of [`0151-user-credential-delivery`](../0151-user-credential-delivery/steps.md#steps) lands second tags the plan's `onRequest` `userCredential` binding entries with the `credential-use` capability of "RO-4" (S2).

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-HandleRoute` and `GWSEAM-RegisterRoutes`, which bind each route's `operationId` at registration and gain the adapter listener (RO-4). It reshapes `GWSEAM-RequestCaller`, `GWSEAM-AuthorizeSession`, and `GWSEAM-AuthorizePermission`, through which the narrowed authority (the effective audience, the registration, the role ceiling, and `crossUserReads`) reaches the leaf check of the routes that the upload handler, the stream proxy, and the platform tools seam register. It reshapes `GWSEAM-ForwardRequest`, whose forward to the holder replica over `CH-HOLDERFORWARD` carries the effective audience in the forward identity headers.

### 0138.S3 Webhook subscriptions
- status: Planned
- aliases: [RO-C, R6]
- lanes: [specification, code, documentation]
- depends-on: [0138.S1, 0138.S2]

R6 webhook subscriptions with their target, capability, and lifetime rules, and `callbackUrl` as a subscription under O2.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It adds webhook subscriptions to the event bus (R6), a crossing that has no operation yet.

### 0138.S4 Lenny's clients on REST
- status: Planned
- aliases: [RO-D, RO-7]
- lanes: [specification, code, documentation]
- depends-on: [0138.S1]

Lenny's clients on REST: SDKs, CLI, and playground ("RO-7").

### 0138.S5 Conformance kit, chart, and adapter repositories
- status: Planned
- aliases: [RO-E, RO-5, RO-6, RO-8, RO-9, RO-10]
- lanes: [specification, code, documentation]
- depends-on: [0138.S1, 0138.S2, 0138.S3]

The conformance kit, the umbrella chart's dependencies and routes, and the creation of the adapter repositories with their first releases. It needs 0138.S3 for the A2A adapter.

### 0138.S6 Removal of the surfaces that leave Lenny
- status: Planned
- aliases: [RO-F, RO-11]
- lanes: [specification, code, documentation]
- depends-on: [0138.S4, 0138.S5]
- closes: [F-4.8.27, F-15.1.80 (part), F-15.1.81, F-15.1.82, F-15.1.83, F-15.2.27, F-15.2.28, F-15.2.30, F-15.2.31, CAPIMSG-I1]

The removal of "RO-11" from the specification, the code, the chart, and the documentation, and the BUILD-GAPS closures. It needs 0138.S5 for the MCP adapter, so that a default deployment serves MCP throughout. F-15.1.80 closes in part: this step closes its translator half, the translators' own history delivery, by removal, and its executor half (DF-1) closes with AM-A in 0120.S1. Open decision O20 here and O14 of [`0120-anthropic-messages-api`](../0120-anthropic-messages-api/decisions.md) can change that split. F-4.8.27, under which the Chat Completions and Open Responses translators run no interceptor phase, closes by removal with the translators.

The removal includes `pkg/gateway/runtime/agentcard` with the write-time card generation, the `POST /v1/admin/runtimes/regenerate-cards` route, the `lenny-ops` tool `admin.regenerate_runtime_cards` in `pkg/ops/mcp/generated_tools.go`, and the §5.1 and §15.1 statements that the gateway generates the card at write time and stores it as the `agent-card` `publishedMetadata` entry, all of which this step owns; [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) keeps only its discovery rule (D12). When 0149.S10 of [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/steps.md#steps) lands first, it wires the resolved delegation lease into `lenny/create_and_start_session` and `BindSingleShot`, and this step removes that wiring with those paths. The commit that deletes `pkg/gateway/environment/translator`, `pkg/gateway/externalapi/outputpartfidelity`, `pkg/gateway/mcpfabric/mcpschemagen`, `pkg/gateway/runtime/adapterregistry`, `pkg/gateway/runtime/agentcard`, and `pkg/gateway/runtime/externaladapterstore` deletes their `removed-by: '0138'` rows from `tests/registers/gateway-layout.yaml`, because the layout gate fails on a row that no longer occurs. That diff touches `tests/registers/`, so the branch is reviewed with the `review` skill before it merges, under "Review changes to the merge gate" in `.claude/rules/git-workflow.md`.

This step goes last so that the MCP surface and the playground never disappear between steps, although Lenny has no deployments to protect.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-CreateSession` and `GWSEAM-InvokeClientAPI`, and the `send_message` and `cancel_session` forwards of `GWSEAM-ForwardToolCall`, with the client MCP tools (RO-11 and RO-12). With the attach stream and `/mcp/v1/ws`, it removes their calls of `GWSEAM-SessionAccessible`, `GWSEAM-RequestCaller`, and `GWSEAM-IsBearerRevoked`, which other callers keep (the stream proxy calls the last two for the stream close of "RO-1" from 0138.S1), and the bearer carrier in `pkg/gateway/api/wsbearer`.
