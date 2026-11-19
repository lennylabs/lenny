# Steps: 0138 REST-only client API

## Validation owed

- Whether MCP clients in common use, with their authorization servers, obtain a token whose audience is `https://<host>/mcp` through RFC 8707, and whether such a client accepts the RFC 9728 document R9 serves; TS's "Residual risks" already lists the per-tenant case as unproven.
- Whether the identity providers in common use write the requested RFC 8707 resource, or a configured resource mapping, into `aud`, and which need a client ID alias ("RO-4").
- Whether common ingress controllers strip client-sent `Lenny-*`, `X-Forwarded-*`, and method-override headers as "RO-4" requires, and route `downloads.host` and external origins as "RO-5" states.
- Whether a restored `dormant` session holds the conversation state a warm turn needs for each first-party runtime, which O12's reading of R16 assumes.
- The latency and throughput cost of the extra hop for streaming and for a provider request, measured on Kind with the MCP and Anthropic adapters.
- Whether common ingress controllers route the protocol paths of "RO-5" without buffering SSE, and pass `Last-Event-ID` and `Mcp-*` headers unchanged.
- Whether the REST message send's forwarding of §29.3 shares `holder_forward.go` with the Responses and MCP forwards that leave.
- Whether `pkg/gateway/mcpfabric/elicitationfloor` serves the client surface or the runtime-facing elicitation chain.
- The proof of concept did not cover the push relay, R9, the adapter listener, narrowing, R13, R19, the `2025-11-25` era, uploads, the A2A JSON-RPC and gRPC bindings, or any provider adapter; each needs a test in its repository or in Lenny before the adapter replaces the in-gateway surface.
- Whether the Anthropic SDKs' retries reach the join of "RO-10" before the first request's message is written, the window in which a retry sends a second message; R5's key closes it when the adapter sends the request digest as `Idempotency-Key` as well.

## Steps

### 0138.S1 REST additions
- status: Planned
- aliases: [RO-A, RO-2, R1, R2, R3, R4, R5, R7, R8, R12, R13, R14, R15, R16, R17, R19]
- lanes: [specification, code, documentation]
- depends-on: [0135.S5, 0146.S1, 0122.S1, 0120.S1]
- closes: [APICONV-I1]

The REST additions every adapter needs: R1 to R5, R7, R8, R12 to R17, and R19, R13's download origin, the event stream's rules moved from §15.2 with its close at expiry and revocation, and their tests. It needs MD-E's events, SL's routes, CT-A, and AM-A, so that the message and conversation members exist.

This step goes first because every adapter and every Lenny client depends on it.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-Publish`, which returns the assigned sequence number (R1), and `GWSEAM-HandleRoute` and `GWSEAM-RegisterRoutes` for the route tags (R7), and adds the pending elicitation and approval lists (R4), a crossing that has no operation yet.

### 0138.S2 Authorization of adapter requests
- status: Planned
- aliases: [RO-B, RO-4, R9]
- lanes: [specification, code, documentation]
- depends-on: [0149.S2, 0149.S7, 0138.S1]
- closes: [F-10.2.39]

R9 and the rules of "RO-4": the adapter registry, computed identifiers, audiences, metadata, challenges, narrowing, evaluation, leaf enforcement, minting refusals, the refusal code, the adapter listener, and their tests. It needs 0138.S1 for R7's tags and R5's key scope.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-HandleRoute` and `GWSEAM-RegisterRoutes`, which bind each route's `operationId` at registration (RO-4).

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
- closes: [F-15.1.80, F-15.1.81, F-15.1.82, F-15.1.83, F-15.2.27, F-15.2.28, F-15.2.30, F-15.2.31, CAPIMSG-I1]

The removal of "RO-11" from the specification, the code, the chart, and the documentation, and the BUILD-GAPS closures. It needs 0138.S5 for the MCP adapter, so that a default deployment serves MCP throughout.

This step goes last so that the MCP surface and the playground never disappear between steps, although Lenny has no deployments to protect.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-CreateSession` and `GWSEAM-InvokeClientAPI`, and the `send_message` and `cancel_session` forwards of `GWSEAM-ForwardToolCall`, with the client MCP tools (RO-11 and RO-12). With the attach stream and `/mcp/v1/ws`, it removes their calls of `GWSEAM-SessionAccessible` and `GWSEAM-RequestCaller`, which other callers keep, `GWSEAM-IsBearerRevoked`, whose only caller is the WebSocket, and the bearer carrier in `pkg/gateway/api/wsbearer`.
