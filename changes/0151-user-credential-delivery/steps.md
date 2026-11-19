# Steps: 0151 User credential delivery

## Dependencies

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| SA-A to SA-D (0143.S1 to 0143.S4) of [`0143-service-account-credentials`](../0143-service-account-credentials/steps.md#steps) | UC-1, UC-3, UC-5, and the egress design's proposals that design names | Principal-owned rows extend UC-1's table and UC-3's release rule. No `UC-` proposal depends on them. |

No `EG-` proposal depends on a `UC-` proposal, and the hooks sit inside [EG-1](../0127-egress-and-git/steps.md#steps), [EG-2](../0127-egress-and-git/steps.md#steps), [EG-3](../0127-egress-and-git/steps.md#steps), [EG-4](../0127-egress-and-git/steps.md#steps), [EG-7](../0127-egress-and-git/steps.md#steps), [EG-9](../0127-egress-and-git/steps.md#steps), and [EG-12](../0127-egress-and-git/steps.md#steps). The VCS layer's UG proposals depend on UC-2, UC-3, UC-4, and UC-7, and each add-on's proposals depend on the VCS layer, as those documents state. [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture)'s new guardrails depend on UC-1 for `tokenTrust`, with UC-2 for link providers, UC-4 for `requestRules`, and UC-5 for `userCredential.file`. [TS-1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) scopes the objects here when both have landed, and neither waits for the other.

## Steps

### 0151.S1 Credential types and the registered source
- status: Planned
- aliases: [UC-1]
- lanes: [specification, code, documentation]
- depends-on: [0127.S9]
- closes: [F-4.9.46, F-13.3.26 (part)]

The design is in [UC-1](change.md#uc-1-credential-types-and-the-registered-source), which covers the types, the `registered` source, and the token trust record. It needs the single write path of 0127.S9 (EG-9). It builds the digest format and `token_trust_records`, which 0151.S2 uses. It can land with no delivery. It closes the `/v1/credentials` half of F-13.3.26.

### 0151.S2 The oauth2Link source and account linking
- status: Planned
- aliases: [UC-2]
- lanes: [specification, code, documentation]
- depends-on: [0123.S1, 0151.S1, 0127.S1, 0127.S2, 0127.S9, 0149.S6, 0148.S1, 0148.S2]

The design is in [UC-2](change.md#uc-2-the-oauth2link-source-and-account-linking), which covers `oauth2Link`, the link flow, and stored links. It needs the mint path of 0127.S2 (EG-2), 0149.S6 (TS-6) for the login path, and 0148.S1 and 0148.S2 (OC-1 and OC-2) for a private host. The flow's mechanics come from 0123.S1 (CA-0), which lands first and closes F-9.3.18. The digest format and `token_trust_records`, the single write path, and the route to every link provider endpoint come from the others. The interceptor contract proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps), step 0141.S1, places the link check at the post-chain point, and this step answers it with the combined missing-links response, which [CA-4](../0123-connector-authorization-and-external-memory/change.md#ca-4-link-check) defined before the removal of connectors withdrew it. It keys on a provider name, so an added kind needs no rework.

### 0151.S3 The userCredential binding
- status: Planned
- aliases: [UC-3]
- lanes: [specification, code, documentation]
- depends-on: [0151.S1, 0127.S4, 0127.S2, 0149.S4]
- closes: [F-13.3.26 (part)]

The design is in [UC-3](change.md#uc-3-the-usercredential-binding), which covers the `userCredential` binding and `inject`. It needs 0127.S2 (EG-2) with the requests under ["Changes requested of other designs"](change.md#changes-requested-of-other-designs), and 0149.S4 (TS-4) for the invalidation level. The dependencies cover bindings, the pod credential API, and `pkg/binding`. It addresses the session input half of F-13.3.26. It also builds the session input check and `retireOnRelease`, which 0152.S2 (UG-2) uses. 0124.S2 (CR-B) of [`0124-connector-removal`](../0124-connector-removal/steps.md#steps) extends the check to the `identityAssertion` binding.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-AuthorizeSession` and `GWSEAM-SessionAccessible`, which applies the same ownership rule to a session record the platform tools seam read, with the session input check (UCP-3). It also reshapes the route access checks of "Route registration" and `GWSEAM-HandleRoute`, because from 0117.S8 the manage check of a session route runs in the API front's request pipeline, which passes `GWSEAM-AuthorizeSession` no route identity, so the routes the input check exempts (`DELETE`, terminate, and interrupt) need an access check or a session action of their own.

### 0151.S4 Request rules and presets
- status: Planned
- aliases: [UC-4]
- lanes: [specification, code, documentation]
- depends-on: [0151.S3, 0127.S4]

The design is in [UC-4](change.md#uc-4-request-rules-and-presets). It needs the stage of 0127.S4 (EG-4).

### 0151.S5 File delivery
- status: Planned
- aliases: [UC-5]
- lanes: [specification, code, documentation]
- depends-on: [0151.S3, 0127.S4, 0127.S9]

The design is in [UC-5](change.md#uc-5-delivery-file), which covers `delivery: file`. It needs the `file` delivery of 0127.S4 (EG-4) for the mechanics and the grant under 0127.S9 (EG-9). The `allowRuntimeHeldCredentials` setting of the [VCS layer](../0152-user-git-identity/change.md), with its refusal and attach rule, builds on it.

### 0151.S6 Leak scanning
- status: Planned
- aliases: [UC-6]
- lanes: [specification, code, documentation]
- depends-on: [0151.S5]

The design is in [Leak scanning (UC-6)](change.md#leak-scanning-uc-6) under UC-5. The step is optional.

### 0151.S7 Add-ons
- status: Planned
- aliases: [UC-7]
- lanes: [specification, code, documentation]
- depends-on: [0151.S2, 0151.S3, 0151.S4]

The design is in [UC-7](change.md#uc-7-add-ons). The dependencies cover the kind block, the registry, and the extension points those steps call. It can land with `oauth2` alone.

### 0151.S8 Link prompts
- status: Planned
- aliases: [UC-8]
- lanes: [specification, code, documentation]
- depends-on: [0151.S2, 0151.S3, 0138.S1, 0135.S5, 0124.S1]

The design is in [UC-8](change.md#uc-8-link-prompts). It needs R4 and R12 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-2-rest-additions), which land in 0138.S1, the human-required elicitation of [`0135-message-delivery`, "MD-22 Parents and children"](../0135-message-delivery/change.md#md-22-parents-and-children), which lands in 0135.S5, and 0124.S1 (CR-A) of [`0124-connector-removal`](../0124-connector-removal/steps.md#steps) for the §9.2 provenance. The dependencies cover `WatchLinkStates`, the `user_link_prompts` row, the prompt on the root streams, and its resolution at the callback.

## Validation owed

Measurements of this mechanism ran on a stub with stock git, and **the stub proves forwarding only**. The add-on designs hold the validation owed against their providers, including the matcher's behaviour at each host.

| Item | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| V3 | Variable re-encoding on both flavours | Forwarding | Owed. Printing ran **[measured]**, and re-encoding has not. |
| S7 | Is `gqlparser` suitable? | Parser choice | **Done for the rule evaluator's parsing** **[captured]** at 2.5.33. A black-box test sending the corpus to a scratch repository on GitHub would name its parser, and the design no longer depends on the answer. |
| S9 | `netagent` memory at the rule bounds. | The concurrency cap and `GOMEMLIMIT` | **[measured]** to 41.7 MB `memory.peak`, under a 15,000 token limit. Check allocation at the final bounds is **[measured]**, and `memory.peak` under them is owed. |
| V8 | `netagent` memory at the final bounds | The concurrency cap | Partly **[measured]**: `memory.peak` reached 41.7 MB under a 15,000 token limit. Not measured under the final bounds. A 40,000 node body checks in 10.5 ms and 7.5 MB. |
| V9 | Leak scanning's cost at the listed phases' rates | Whether UC-6 is on by default anywhere | Owed |
| V10 | Does `netagent` attribute a loopback connection to its slot on runc with user namespaces, gVisor, and Kata, and does the pool's attribution status record the union scope where it does not? | On which runtime classes pods with several slots accept a `userCredential` binding under decision 41 | Measured on 2026-11-14. The owner asked on 2026-11-13 for a spike to measure it ("run a spike to test this"), and the AWS spike of 2026-11-14 found that runc, with and without user namespaces, answers `sock_diag`, and that gVisor with either network mode and Kata with either volume mode answer only `/proc/net/tcp`, with each connection's own slot UID from every answering reader ([its report, "Test 1: per-slot attribution"](../0118-agent-pod-layout/evidence/attribution-and-quota-spike/RESULTS.md#test-1-per-slot-attribution); [`0127-egress-and-git`, "Slot attribution and fetch grants"](../0127-egress-and-git/change.md#slot-attribution-and-fetch-grants), revision 83) **[measured]**. No measured runtime class is a fallback pool, and a pool's attribution status still decides each pool. The production `netagent` under a burst of connections and the managed runtimes stay owed in the egress design ("Validation still owed", **Slot attribution**). |
