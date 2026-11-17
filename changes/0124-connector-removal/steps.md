# Steps: 0124 Connector removal

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Claude Code and Codex each reach an HTTP MCP server through `netagent`'s proxy settings and trust its per-pod CA, with an injected credential | CR-3 depends on harnesses sending MCP traffic through the proxy and trusting the CA. | Done on 2026-11-15 for Claude Code 2.1.323 and Codex CLI 0.136.0, on loopback with a Go stand-in **[measured]** ([`evidence/connector-removal-design/harness-egress-spike/RESULTS.md`](evidence/harness-egress-spike/RESULTS.md)). A run in a pod on runc, gVisor, and Kata stays owed with CR-B. |
| A headless harness surfaces a URL-mode elicitation from an MCP server so the runtime can forward it under B6 | CRR-8 and CR-3 **Questions.** | Codex measured through app-server: declined under `never`, forwarded under `on-request`. Claude Code unmeasured: whether its `Elicitation` hook fires under `-p` and can block until Lenny's `reply`, and which of its documented `-p` behaviours holds, stay owed. |
| The identity assertion on multi-slot pods: per-slot attachment on runc, gVisor, and Kata | CRR-4 and O7. Attribution is measured (V10); the attach path with this binding is not. | Open, for CR-B. |
| Claude Code's behaviour on a 401 with a configured `Authorization` header | The placeholder header of CR-3. | Done on 2026-11-15: with the placeholder, no OAuth discovery; without it, discovery and dynamic client registration **[measured]**. |
| Claude Code's 60-second default for HTTP MCP requests, and whether a per-server `timeout` lifts it | CR-3 **Long calls.** **[unverified]**. | Open. |
| Common JWT libraries select a key by `kid` from a JWKS whose keys carry an extra `tenant` member, and expose that member to the caller | CR-2's deployment key set and its verifier rule. | Open, for CR-B. The reference verifier does not depend on it. |

## Steps

### 0124.S1 Connector removal
- status: Planned
- aliases: [CR-A, CR-1]
- lanes: [specification, code, documentation]
- depends-on: [0123.S1]
- closes: [F-4.3.1]

The removal (CR-1), including the D7 refusals, the successor pointers, and the records. CA-0 (0123.S1) lands before it or with it.

### 0124.S2 Identity assertion
- status: Planned
- aliases: [CR-B, CR-2]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S4, 0127.S10, 0151.S3]

The identity assertion (CR-2), with the tenant key sets, the deployment key set, the reference verifier, and the pod refusal of D11. It depends on the egress design's binding and Token Service steps, which the egress design's dependency table names as EG-2, EG-4, and EG-10: the pod credential API, `pkg/binding`, and slot attribution; the typed `secret` union and `inject`; and the `credential.mint` stream. It also depends on the step that lands UC-3's pod refusal and `retireOnRelease`, because D11 extends them.

### 0124.S3 Egress halt at suspension
- status: Planned
- aliases: [CR-C, CR-4]
- lanes: [specification, code, documentation]
- depends-on: [0146.S1, 0146.S2, 0127.S2, 0127.S3]

The egress halt at suspension (CR-4), with `CH-EGRESSHALT` and `HaltSessionEgress`. It depends on the session lifecycle design's SL steps and on the egress design's fetch-grant channel, which EG-3 adds and `CH-EGRESSHALT` follows. The egress design's dependency table also names EG-2, for slot attribution.

### 0124.S4 Documentation of external services
- status: Planned
- aliases: [CR-D, CR-3]
- lanes: [documentation]
- depends-on: [0124.S1, 0124.S2]

Documentation of CR-3.
