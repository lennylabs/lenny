# Steps: 0133 LLM access through egress and external charges

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Confirm, against a running LiteLLM, Envoy AI Gateway, and Kong, that each verifies the `lenny-attribution+jwt` of 0124 CR-2 under the `deployment` key set, or calls the reference verifier before it serves a call | "LU-2" relies on the gateway refusing a call without a valid assertion. The documentation read on 2026-11-24 states that LiteLLM's JWT authentication needs its Enterprise licence and its open-source `custom_auth` can verify, that Envoy Gateway's `SecurityPolicy` verifies ES256 against a remote key set, that Kong's free `jwt` plugin reads no key set, and that Portkey's open-source gateway authenticates no caller **[external]**. Neither Envoy nor LiteLLM documents a `typ` check **[unverified]**. | Open. |
| Confirm that each of those gateways can copy the assertion's `jti` into its per-call cost record and deliver the records in batches, through `lenny-litellm`, `lenny-charge-relay`, or its own logging | "LU-3" and `0170-budgets` "BG-12" rely on it. LiteLLM's custom callbacks batch, Envoy exports access logs over OpenTelemetry, and Kong's `http-log` batches **[external]**; whether LiteLLM's `custom_auth` metadata reaches its callbacks on a released version is **[unverified]**. | Open. |
| Run Claude Code with an `http://` base URL through `HTTP_PROXY` and record whether it sends an absolute-form request or opens a `CONNECT` tunnel | "LU-1" relies on `netagent` reading a plain request to a bound host. Claude Code's behaviour is undocumented **[unverified]**, and a `CONNECT` tunnel to port 80 would carry cleartext that 0127 does not describe. | Open. |
| Run gemini-cli non-interactively with `GOOGLE_GEMINI_BASE_URL` and an `http://` base URL | Its documentation requires HTTPS unless the URL names `localhost`, and its non-interactive mode may refuse the gateway authentication type **[unverified]**. | Open. |
| Confirm whether LiteLLM and Envoy refetch a key set when an assertion names an unknown `kid` | A gateway that does not refetch refuses calls after a rotation until its cache expires. 0124's D14 publishes a key an hour before its first use against LiteLLM's 600-second cache **[external]**; the refetch behaviour is **[unverified]**. | Open. |
| Measure the binding propagation delay, from an exhaustion recorded by Lenny to `netagent` refusing to attach the assertion, on the pod binding API | LUR-5 and LUR-6 bound the overshoot by it. | Open. Waits on 0127.S4. |
| Confirm that no VCS path uses `AssignCredentials` or the adapter's credentials file at `/run/lenny/credentials.json` | "LU-4" deletes them for LLM leases. If a VCS path still uses the file, it stays at that path on the slot's run volume ([PL-3](../0118-agent-pod-layout/change.md#pl-3-runtime-visible-filesystem-layout)). | Open. |
| File the defect found while verifying that has no finding yet: `credential.leased` named for LLM leases that never emit it | It is a defect in the tree today, independent of this design. | Open. |

The validation items of revisions 1 to 19 on usage fields, streams, the input estimate, Gemini paths, `gemini-cli` on the proxy, reservation latency, gateway usage fields, and hosted tool types are withdrawn with the proxy (decision 22).

## Steps

### 0133.S1 LLM gateway access
- status: Planned
- aliases: [LU-A, LU-1, LU-2, LU-3, LU-5, LU-6, LU-7, LU-8]
- lanes: [specification, documentation]
- depends-on: [0133.S2, 0124.S2, 0127.S4]

The specification of "LLM gateways and external charges" in §4.9 with LUR-1 to LUR-7, and the operator and runtime-author documentation of "Documentation pages" assigned to this step: the gateway as an egress destination, the attribution binding, the gateway's checks, charges, enforcement by binding suspension, usage, and calls outside the gateway. It builds no code of its own: 0124.S2 mints the attribution assertion and keeps the mint index, 0127.S4 attaches it, removes the placeholder, suspends bindings, and answers a suspended session, and 0170.S6 counts charges by `jti`. The reference integrations of "LU-9" live in their own repositories, outside this step, and the gateway guide links them. Its `integration` and `security` tests run once those steps have landed, and they land with whichever of this step and 0170.S6 merges second.

### 0133.S2 Removal
- status: Planned
- aliases: [LU-B, LU-4]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2]
- closes: [F-4.9.48, F-11.2.38, CHEGRESS-I1, CHGWPOD-I9, CHGWPOD-I12, CHGWPOD-I15, F-4.9.29, F-4.9.32, F-4.9.33, F-4.9.34, F-4.9.37, F-4.9.38, F-4.9.39, F-4.9.40, F-4.9.41 (part), F-4.9.47 (part), F-4.7.31, F-11.4.10, F-4.9.49, F-4.9.50, F-4.9.51, F-4.9.52, F-4.9.53, F-4.9.54]

The removal of "LU-4": LLM credential pools, leases, direct mode and its webhook, the router, fallback, renewal, rotation, the pre-claim check, the LLM proxy with its listener, routes, translators, usage parsing, and in-process budget enforcer, the proxy token and the `llm` member of `session_start`, `PreLLMRequest` and `PostLLMResponse`, `llmFormats`, `supportedProviders`, `credentialCapabilities`, `credentialPropagation`, `vault_transit`, and the `provider` path of `POST /v1/credentials` with the `credentials` table and its gateway-side store, with every section of "Specification changes" that the removal reaches. It resolves the lease and proxy findings of "Records" as obsolete.

It lands before 0133.S1, because 0151.S1 waits on it for the removal of the `credentials` table and 0124.S2, which 0133.S1 needs, waits on 0151. Between this step and 0170.S6, model calls reach no budget, which no deployment relies on before v1 ships. The owner accepted this order on 2026-11-24 (decision 25). It needs EG-2 (0127.S2) for the removal of `provider-direct`, and it deletes the `lenny-direct-mode-isolation` webhook and checks (1) and (2) of the `direct_mode_isolation` validator with `deliveryMode`. 0151.S1 depends on this step for the removal of the `credentials` table (0151-F4).

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it removes: `GWSEAM-CheckChildCredentials`, `GWSEAM-RotateCredential`, `GWSEAM-TenantCredentialProviders`, `GWSEAM-ExtendForBudget` and `GWSEAM-RaiseBudget` in their proxy form, and the LLM half of `GWSEAM-RevokePoolCredentials` and `GWSEAM-RevokeUserLeases`, and it reshapes `GWSEAM-MaterializeChild`, which no longer assigns an LLM credential lease. It keeps `GWSEAM-ExpireSession`, which 0146.S1 needs.

It deletes the rows of `tests/registers/gateway-layout.yaml` with `removed-by: '0133'` as it deletes their packages, and its diff is reviewed under "Review changes to the merge gate" of `.claude/rules/git-workflow.md` because it touches `tests/registers/`.

0132.S1 and 0133.S2 rebase on whichever of them lands first, because both edit `pkg/gateway/meteringproxy/llmproxy/handler.go`, `pkg/credential/credentialpoolstore`, and the admin pool validator (0132-L8).

### 0133.S3 Rate cards and credit budgets
- status: Cancelled
- aliases: [LU-C]
- lanes: [specification, code, documentation]
- depends-on: [0133.S2]

Cancelled by decision 22 on 2026-11-24: rate cards, credits, the input estimate, reservations per call, settlement, and `llm_usage` leave with the proxy. Budgets on model spend count charges on the `external` meter under 0170.S6.

### Notes on the steps

Revision 20 rewrote 0133.S1, which carried the metering corrections, and 0133.S2, which carried upstreams and the proxy, under decision 22. The labels LU-A, LU-B, and LU-C keep naming the steps. Revision 2's LU-D and LU-E stay retired.
