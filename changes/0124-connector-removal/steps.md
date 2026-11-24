# Steps: 0124 Connector removal

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Claude Code and Codex each reach an HTTP MCP server through `netagent`'s proxy settings and trust its per-pod CA, with an injected credential | CR-3 depends on harnesses sending MCP traffic through the proxy and trusting the CA. | Done on 2026-11-15 for Claude Code 2.1.323 and Codex CLI 0.136.0, on loopback with a Go stand-in **[measured]** ([`evidence/connector-removal-design/harness-egress-spike/RESULTS.md`](evidence/harness-egress-spike/RESULTS.md)). A run in a pod on runc, gVisor, and Kata stays owed with CR-B. |
| A headless harness surfaces a URL-mode elicitation from an MCP server so the runtime can forward it under B6 | CRR-8 and CR-3 **Questions.** | Codex measured through app-server: declined under `never`, forwarded under `on-request`. Claude Code unmeasured: whether its `Elicitation` hook fires under `-p` and can block until Lenny's `reply`, and which of its documented `-p` behaviours holds, stay owed. 0124.S4 waits on it, and so does the step that open decision O18 names if the reference runtimes forward elicitations. |
| The identity assertion on multi-slot pods: per-slot attachment on runc, gVisor, and Kata | CRR-4 and O7. Attribution is measured (V10); the attach path with this binding is not. | Open, for CR-B. |
| Claude Code's behaviour on a 401 with a configured `Authorization` header | The placeholder header of CR-3. | Done on 2026-11-15: with the placeholder, no OAuth discovery; without it, discovery and dynamic client registration **[measured]**. |
| Claude Code's 60-second default for HTTP MCP requests, and whether a per-server `timeout` lifts it | CR-3 **Long calls.** **[unverified]**. | Open. 0124.S4 waits on it. |
| Common JWT libraries select a key by `kid` from a JWKS whose keys carry an extra `tenant` member, and expose that member to the caller | CR-2's deployment key set and its verifier rule. | Open, for CR-B. The reference verifier does not depend on it. |

## Steps

### 0124.S1 Connector removal
- status: Planned
- aliases: [CR-A, CR-1]
- lanes: [specification, code, documentation]
- depends-on: [0123.S1]
- closes: [F-9.3.19, F-9.3.20, T-5.1.3, T-9.2.11, T-9.3.2, T-9.3.4, T-9.3.5, T-9.3.7, T-9.3.8, T-9.3.12, T-13.3.9, T-7.1.9 (part), T-11.6.6 (part), T-25.6.5 (part), F-5.1.32 (part)]

The removal (CR-1), including the D7 refusals, the successor pointers, and the records. CA-0 (0123.S1) lands before it or with it. Whether that edge stays, which name the renamed `pkg/connectoroauth` and its Redis state key take, and which step renames them are open decision O21. The detail format of the D7 refusals is open decision O16, and the `DelegationPolicy` item of D7 follows open decision O24.

`BUILD-GAPS.md` carries two entries under each of F-9.3.19 and F-9.3.20, and the step closes all four, each of which exists only for connectors: "The connector OAuth callback reports a state store fault as `400 CONNECTOR_OAUTH_STATE_UNKNOWN`" and "The chart gives the gateway no `get` on a connector's client Secret, so a confidential connector's token exchange fails at the callback under chart defaults" (F-9.3.19), and "The chart cannot enable the connector OAuth flow, because it renders neither the callback URL flag nor its environment variable" and "No code refreshes a connector token, so a connector stops working when its access token expires" (F-9.3.20). The parts it closes of the others are the connector-state rule of T-7.1.9, the `connector` admission tier of T-11.6.6, the connector probes of T-25.6.5, and `toolCapabilityOverrides` in F-5.1.32's merge table. F-4.3.1 is already closed and is not listed.

The step removes the connector path as the tree holds it: the adapter's connector MCP servers, the manifest's `connectorServers`, the SDK helpers, the conformance case, the `CH-MCP-CONNECTOR` register row with its successor pointer, the connector RPCs on `LNK-GWCONTROL`, and the gateway side, whichever of this step and 0139.S2 lands first (CR-1 **Ownership against other steps.**). The `server` narrowing of tool frames with `UNKNOWN_SERVER`, and the refusals of `capabilities.deferredToolDiscovery` and `capabilities.toolProgress`, belong to 0139.S1 and 0139.S2. Whichever of this step and 0119.S1 lands first removes capability inference from MCP `ToolAnnotations` whole, with `pkg/runtime/capabilityinference`, the runtime columns, and `mcpAnnotationMapping`, and the other finds it gone.

The step also edits the gateway layout records that name the removed packages: GW-R2 on `spec/components/gateway/index.md`, `pkg/gateway/core/adminops` (`ConnectorSecrets`), `pkg/gateway/ARCHITECTURE.md`, the connector rows of `tests/static/lint/gatewaylayout/confined.go`, the rows of `tests/registers/gateway-layout.yaml` marked `removed-by: '0124'` in the same commit as the packages, `tests/registers/spec-section-citations.yaml`, and the generated `spec/catalogs/api-operations.md`. Its diff touches `tests/static/` and `tests/registers/`, so the `review` skill runs over it before the merge under "Review changes to the merge gate" in `.claude/rules/git-workflow.md`, and the digest records the outcome.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-ResolveConnectorSecret`, through which the admin connector routes resolve a connector's client secret, with `connectorsecret` and those routes.

### 0124.S2 Identity assertion
- status: Planned
- aliases: [CR-B, CR-2]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S4, 0127.S10, 0151.S3, 0143.S1 (part), 0149.S3 (part)]

The identity assertion (CR-2), with the tenant key sets, the deployment key set, the reference verifier, the public routes on their own listener, and the pod refusal of D11. It depends on the egress design's binding and Token Service steps, which the egress design's dependency table names as EG-2, EG-4, and EG-10: the pod credential API, `pkg/binding`, and slot attribution; the typed `secret` union and `inject`; and the `credential.mint` stream. It also depends on the step that lands UC-3's pod refusal and `retireOnRelease`, because D11 extends them. The part of the `sub` claim that strips the `~svc~` or `~agt~` prefix, with its unit test, waits for 0143.S1, which adds the prefixes under SA-6, and the `environment` claim waits for 0149.S3, which carries the session's environment under TS-3. The rest of the step can land first.

The emergency rotation waits on open decision O20, the reading of a used pod on a pool with several slots waits on open decision O17, and the delegation rule for a child waits on open decision O23. The serving path of the public routes waits on open decision O26, which hosts the erasure receipt names waits on open decision O25, and whether the `sub` part keeps its edge on 0143.S1 or moves it to 0149.S6 waits on open decision O27.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It adds the refusal of a delegation whose child fails the pod test of D11 to the failures that `GWSEAM-MaterializeChild` and `GWSEAM-CheckChildCredentials` list, and it extends the impersonation check that 0151.S3 places behind `GWSEAM-AuthorizeSession` to `identityAssertion` bindings.

Under decision 22 of 0133, the step also mints the attribution assertion of CR-2 **Attribution assertion.**, with its pseudonym key per binding, the refusal of a host that no charge reporter lists, and the pod rule that refuses only a fallback pool. O29 and O30 settle its form and the reference verifier before the step starts.

### 0124.S3 Egress halt at suspension
- status: Planned
- aliases: [CR-C, CR-4]
- lanes: [specification, code, documentation]
- depends-on: [0146.S1, 0127.S2, 0127.S3]

The egress halt at suspension (CR-4), with `CH-EGRESSHALT` and `HaltSessionEgress`. It depends on 0146.S1, which lands SL-3 and SL-4 apart from their child events, and on the egress design's fetch-grant channel, which EG-3 adds and `CH-EGRESSHALT` follows. The egress design's dependency table also names EG-2, for slot attribution. CR-4 uses no child event, so the step does not wait for 0146.S2. What the halt states on a pool with `egressProfile: internet` waits on open decision O19. If 0146.S1 is split, the edge on it names the suspension step under open decision O28.

### 0124.S4 Documentation of external services
- status: Planned
- aliases: [CR-D, CR-3]
- lanes: [documentation]
- depends-on: [0124.S1, 0124.S2, 0151.S8 (part), 0146.S1 (part), 0135.S5 (part), 0148.S2 (part), 0140.S2 (part)]

Documentation of CR-3: the page on reaching operator services that replaces `docs/tutorials/using-connectors.md`, which 0124.S1 deletes, and the runtime author guide entries of "Changes to Lenny's documentation". Each `(part)` edge holds only the statement that names the other step's mechanism: the link prompt of UC-8 (0151.S8), the `busy: true` rule and the idle clock of SL-6 (0146.S1), the `request` frame with `kind: "question"` under B6 and MD-19 (0135.S5, which follows 0135.S4), the outbound connector route for private services (0148.S2), and `runtimeOptions` as a way to name a service's URL (0140.S2, which carries RV-7). The rest of the page can land first. The pages that describe CR-2 and CR-4 land with 0124.S2 and 0124.S3, as "Changes to Lenny's documentation" marks. The runtime author guide's elicitation entry waits on open decision O18, and `docs/tutorials/agent-memory.md` waits on open decision O22. If 0146.S1 is split, the `(part)` edge on it names the idle and dormancy step under open decision O28.
