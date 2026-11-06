# Built-in experiment provider removal

Status: design for owner review, revision 2, 2026-11-03, with no independent review yet. The owner has chosen contract documentation for deployers on vendors without an OFREP server ("Vendor coverage"). It stages no specification edit. The changes go through `change-proposal` after review.

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[measured]** | Run on 2026-11-03 against this tree through a Go build overlay, with no change to the tree. |
| **[source]** | Read in the Lenny tree or the specification. |
| **[documented]** | Stated on a vendor or OpenFeature page under "Sources", read and not tested. |
| **[not found]** | Absent from the vendor pages read. This records what those pages say and does not prove the vendor lacks the feature. |
| **[design]** | What this design requires. Nothing implements it. |

## Problem

The gateway links vendor SDKs for LaunchDarkly, Statsig, and Unleash into its binary so that a `mode: external` experiment can ask one of those services for a variant. OFREP, the vendor-neutral REST protocol, is a second path to the same result and is the path §10.7 recommends.

The SDK path has three costs:

- It adds vendor code and background connections to the gateway process, which holds the platform's credentials and serves every tenant.
- It behaves incorrectly on the session creation path in ways the OFREP path does not. "Current state" lists them.
- Its presence in §10.7, §23, and the documentation adds to the scope the product review called too broad for the project's stage.

Removing it leaves OFREP as the only external path, and that path has defects of its own. The example configuration in §10.7 does not work, a flag that has not been created disables every external experiment for the session, and an in-cluster flag service is unreachable. This design removes the SDK path and corrects the OFREP path in the same proposal, because the removal makes OFREP the only route for external assignment.

## Scope

**Removed:**

- The `experimentprovider` package and its vendor dependencies.
- The `launchdarkly`, `statsig`, and `unleash` values of `experimentTargeting.provider`, and their configuration blocks.
- The `experimentTargeting.provider` field, which has one remaining value.
- The use of the OpenFeature Go SDK. The OFREP client is plain `net/http` and never imported it **[source]**.

**Kept without behaviour change:**

- `ExperimentDefinition` and its admin API.
- The `ExperimentRouter`, including `mode: percentage` HMAC bucketing and the first-match rule.
- `mode: external`, the sticky assignment cache, and the per-tenant targeting circuit breaker.
- Variant pools and their sizing through the PoolScalingController, including `initialMinWarm`.
- `propagation.childSessions` in all its modes. A child under `independent` calls the same `ApplyExperimentRouting` function, so it reaches OFREP through the same code **[source]**.
- `experimentContext` on `session_start` and on delegation leases.
- The isolation monotonicity checks.

**Out of scope:**

- Eval storage, meaning `POST /v1/sessions/{id}/eval`, the results API, and `lenny_eval_aggregates`. The product review treats it as a separate decision, and this design neither depends on nor changes it.
- Statistical analysis, which stays outside Lenny. A runtime can still report metrics to LaunchDarkly or Statsig from inside its pod through §10.7 "Full A/B testing with external experimentation and eval platforms". That pattern uses a vendor SDK in the runtime and never used the gateway's providers.

## Vendor coverage

**Who loses direct integration.** The earlier recommendation said that OFREP alone may be enough for external flag services. That rested on the §10.7 statement that LaunchDarkly exposes OFREP through its Relay Proxy. The Relay Proxy endpoint documentation has no OFREP endpoint **[not found]**, and the OpenFeature ecosystem page lists no OFREP API for LaunchDarkly, Statsig, or Unleash **[documented]**.

| Service | OFREP server | Evidence | Path after removal |
| :-- | :-- | :-- | :-- |
| flagd | Yes, labelled experimental, default port 8016 | **[documented]** | Direct |
| ConfigCat Proxy | Yes, from Proxy v2.0.0 | **[documented]** | Direct |
| GO Feature Flag, Flipt, DevCycle, and others | Listed by the OpenFeature ecosystem page; their own pages were not confirmed | **[documented]**, ecosystem listing only | Direct, once confirmed |
| LaunchDarkly | None on the Relay Proxy endpoint page | **[not found]** | Deployer-run bridge |
| Statsig | None on the forward proxy page | **[not found]** | Deployer-run bridge |
| Unleash | None on the Edge page | **[not found]** | Deployer-run bridge |

The vendors named most often in the documentation are therefore the ones that lose direct integration. A deployer on one of them runs an OFREP bridge ("AB-6"), uses `mode: percentage`, or uses another flag service. A vendor could add OFREP later, and this design does not rely on that.

The SDK path is removed despite this loss. No deployment exists, the SDK path has not run against a vendor in this repository ("Current state"), and a bridge places the same SDKs in a process with one tenant's credentials.

**Decision.** Lenny provides contract documentation for the bridge and ships no bridge code. An operator guide page states the OFREP subset the gateway uses, the failure mapping, and the network rule. The deployer writes and operates the bridge. The documentation makes no claim about the size or effort of a bridge until the spike in "Validation owed" has run.

## Current state

**SDK path defects [source]:**

| Defect | Location | Effect |
| :-- | :-- | :-- |
| The cache mutex is held across `openfeature.SetNamedProviderAndWait`. | `Cache.For` in `experimentprovider.go` | The first session for a new configuration blocks, and sessions of every other tenant using an SDK provider wait on the same mutex. |
| The initialization context is created and discarded with `_ = initCtx`. | `Cache.For` | `DefaultInitTimeout` does not bound the wait for that call. |
| No per-evaluation deadline reaches the SDK path. `timeoutMs` is applied only as the OFREP HTTP client timeout. | `buildExternalEvaluator`, `resolveExternalClient` | The §10.7 200 ms budget is not enforced for SDK providers. |
| The Statsig and Unleash SDKs initialize process-global state, so a second configuration for either is rejected. | `Cache.For`, package comment | On one gateway process, only one tenant can use each of those vendors. A second tenant receives `targeting_failed` on every session. |
| The tests use an in-memory OpenFeature provider or a fake client. | `experimentprovider_test.go`, `experimentrouter_sdkprovider_internal_test.go` | No test in the tree drives a vendor SDK against a live or stub vendor endpoint. `TEST-GAPS.md` T-10.7.9 records the missing coverage. |

**Size of the SDK path [measured].** Both builds use `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 -trimpath -ldflags='-s -w'`. The second replaces `experimentprovider.go` with a stub through an overlay, so the router and wiring remain and the result slightly understates the removal.

| | Current | Without vendor SDKs | Change |
| :-- | --: | --: | --: |
| `lenny-gateway` binary | 110,805,154 bytes | 108,286,114 bytes | 2,519,040 bytes, 2.3% |
| Packages in the gateway build | 1942 | 1884 | 58 |

The packages removed include `stretchr/testify` and `golang.org/x/exp`, which reach the production binary only through this path. The `govulncheck` result in binary mode was the same for both builds and named no vendor module, so vulnerability count is no argument for the removal. The reasons are the process boundary, the defects above, and scope.

**OFREP path defects:**

| Defect | Evidence | Effect |
| :-- | :-- | :-- |
| The §10.7 and operator guide examples set `endpoint: https://flags.internal/ofrep`, and the client appends `/ofrep/v1/evaluate/flags/{key}`. | **[measured]** The request path was `/ofrep/ofrep/v1/evaluate/flags/exp`, and the result was a 404. | The documented example fails on a standard server. OFREP defines its paths from the host root **[documented]**. |
| The example header `Authorization: "Bearer ${OFREP_TOKEN}"` is never expanded. | **[measured]** The server received the literal string. No non-test Go code expands environment references in this configuration **[source]**. | The documented example sends an invalid credential. |
| Header values are stored in `tenants.experiment_targeting` as JSON, and tenant responses return them. | **[source]** `migrations/0025_tenant_experiment_targeting.up.sql`, `fromTenant` | An OFREP bearer token sits unencrypted in a table column and in responses to `platform-admin` callers. The SDK blocks have the same exposure. |
| A 404 `FLAG_NOT_FOUND` counts as a provider failure. | **[source]** `ofrep.Evaluate`, `buildExternalEvaluator`, `experimentrouter_targeting_test.go` | One experiment registered before its flag exists sets the failure latch, so every later external experiment for that session is skipped, and five such sessions open the breaker for the tenant. §10.7 says an experiment the provider does not return is skipped for that experiment alone, and OFREP says this 404 "can be an expected response". |
| A 200 with no `variant` and no `value` emits `experiment.unknown_variant_from_provider`. | **[source]** `ResolveExternalVariant`, and the OFREP `codeDefaultFlag` response **[documented]** | A well-formed answer meaning "use the default" raises a warning. |
| §10.7 says Lenny reads `variant` directly for OFREP, but OFREP's `variant` is optional and the code falls back to `value`. | **[measured]** A response with only `value: "treatment"` parsed with an empty `Variant`. **[source]** `extractProviderVariant` | The code is correct and the specification text is wrong. |
| Session labels are not passed in the evaluation context. | **[source]** `buildExternalEvaluator` omits `Labels`, while §10.7 lists them | Targeting rules on labels never match. |
| The HTTP client follows redirects. | **[source]** `ofrep.New` sets no `CheckRedirect` | A 307 or 308 resends the body, with its user ID, to the redirect target. |
| `endpoint` is checked only for being non-empty, and accepts `http`. | **[source]** `TargetingConfig.Validate` | Connector URLs are `https` only in production under §15, and this URL is not. |
| The gateway cannot reach a private address on any port. | **[source]** §13.2 gateway egress row, `gateway-llm-upstream-egress.yaml` | The rule admits TCP 443 and excludes cluster and RFC1918 ranges, so an in-cluster flag service or bridge, or a host like the example `flags.internal` on a private address, is blocked. Vendor SaaS endpoints on public addresses were reachable, so the SDK path did not meet this limit. |

`T-10.7.7` and other open test gaps record that no test exercises a provider outage through the session path, which is why these defects have gone unseen.

## Requirements

| ID | Requirement |
| :-- | :-- |
| ABR-1 | The gateway binary links no feature-flag vendor SDK and no OpenFeature SDK. |
| ABR-2 | `mode: external` assignment uses OFREP alone, and the evaluation, sticky, failure, and breaker semantics of §10.7 still hold. |
| ABR-3 | A configuration copied from the specification or documentation works against a conforming OFREP server. |
| ABR-4 | A flag that does not exist affects only its own experiment. |
| ABR-5 | No OFREP credential is stored in the tenant record or returned by the API. |
| ABR-6 | A deployer can make an in-cluster or private flag service reachable, and API configuration cannot widen gateway egress. |
| ABR-7 | Every kept capability in "Scope" behaves as it did before. |

## Design

### AB-1 Removal

Delete `pkg/gateway/experiment/experimentprovider`, and remove from `pkg/gateway/sessionserver/experimentrouter.go`:

- `ExternalProviderResolver`, `NewExternalProviderResolver`, `cacheResolver`, `evaluatorEvalClient`, and `constErrEvalClient`.
- The SDK case in `resolveExternalClient`, and the SDK branch in `classifyTargetingError`.

Remove the `externalProviders` field and option from the session server, the wiring in `cmd/lenny-gateway`, and the `openfeature` shutdown step. Remove the vendor modules and `open-feature/go-sdk` from `go.mod` with `go mod tidy`.

`externalEvalClient` remains as the consumer-side seam the router tests use for a fake client.

### AB-2 Configuration

```yaml
experimentTargeting:
  timeoutMs: 200
  circuitBreaker:
    failureThreshold: 5
    windowSeconds: 10
    openDurationSeconds: 30
  ofrep:
    endpoint: https://flags.acme.com
    headerSecretRefs:
      Authorization: lenny-system/acme-ofrep-token
```

- The presence of the `ofrep` block means the tenant has configured external targeting. `Configured()` tests for it.
- `TargetingProvider`, `AllTargetingProviders`, and the vendor configuration types are deleted.
- `endpoint` is the URL the gateway prepends to `/ofrep/v1/evaluate/flags/{key}`. Admission rejects a path ending in `/ofrep` or `/ofrep/v1`, naming the correction, because that is the mistake the current examples teach.
- `endpoint` uses `https` in production, following the connector scheme rule in §15.
- An unknown field in `experimentTargeting` returns `400 VALIDATION_ERROR`. The tenant handlers decode leniently **[source]**, so a stale `provider` or `launchdarkly` key would otherwise be ignored and the tenant's external experiments would skip without any error.

The platform has no deployments, so this needs no data migration. The column stays JSONB with the same default.

### AB-3 OFREP evaluation semantics

| OFREP response | Gateway behaviour | Breaker | Signal |
| :-- | :-- | :-- | :-- |
| 200 with `variant` | Uses `variant` | Success | None |
| 200 with `value` only | Uses a string `value`, or `value.variant_id` from an object | Success | None |
| 200 with neither | Control for this experiment | Success | None |
| 200 with an unregistered variant, or an unusable `value` | Control for this experiment | Success | `experiment.unknown_variant_from_provider` |
| 404 `FLAG_NOT_FOUND` | Control for this experiment, and evaluation continues with the next experiment | Success | `lenny_experiment_targeting_error_total{error_type="FLAG_NOT_FOUND"}` |
| 400, 401, 403, 429, 5xx, redirect, timeout, or transport error | §10.7 `targeting_failed`: no external assignment for any experiment on this session | Failure | Existing event and counter |

The 404 row keeps the counter so that a misspelled flag stays visible, although the condition is neither a failure nor an outage. The client returns a typed not-found result for it.

A 429 opens the breaker through the same count as any other failure, and the gateway does not honour `Retry-After`. The breaker's half-open interval bounds the retry rate.

The evaluation context gains the session's labels through the `Labels` input that `BuildEvaluationContext` already accepts. The client refuses redirects, and a redirect response is a `targeting_failed` condition.

The metric label `provider` keeps its name and carries the endpoint hostname, as it already does for OFREP. The `provider` field on the `experiment.targeting_failed`, `experiment.unknown_variant_from_provider`, and `experiment.unknown_external_id` events changes from the constant `ofrep` to the same hostname.

### AB-4 Credentials

`ofrep.headers` is replaced by `ofrep.headerSecretRefs`, a map from header name to a `namespace/name` or `namespace/name/key` Secret reference. The form and the resolver are those of connector `clientSecretRef` (`pkg/gateway/connectors/connectorsecret`). OFREP names bearer and `X-API-Key` authentication, and ConfigCat uses its own header **[documented]**, so the header name is configurable.

- Admission rejects `Content-Type`, `Accept`, and `Host` as header names.
- `PUT /v1/admin/tenants/{id}` already requires `platform-admin` through `requireAdmin` **[source]**, so no tenant-scoped role can write a reference or an endpoint. This matters because a reference plus a chosen endpoint sends the named Secret to that endpoint. Any later move of this block to a tenant-writable surface has to keep this restriction, as a credential pool `secretRef` does under §4.9.
- The gateway caches a resolved value per replica for 60 seconds, configurable by flag, so rotation applies within that time and the session creation path does not read the Kubernetes API on each evaluation.
- A resolution failure is a `targeting_failed` condition. The error text carries the reference and never the value.
- A flag service that needs no credential omits the map.

### AB-5 Network reach

The chart gains a supplemental gateway egress rule, modeled on `gateway.interceptorNamespaces` (NET-039):

```yaml
gateway:
  flagServiceEgress:
    namespaces: []      # for example, [flags]
    cidrs: []           # for example, [10.20.0.0/24]
    port: 443
  flagServiceCaBundleConfigMap: ""
```

- An empty list renders no rule, so the default egress is unchanged.
- The rule is a Helm value, so only the deployer controls it. An `endpoint` reaches only what the deployer has admitted, plus the existing public HTTPS rule.
- The rendered rule keeps the IMDS addresses in `except` for each CIDR, following NET-057. The chart refuses `0.0.0.0/0` and `::/0` here, following the KMS rule, because NetworkPolicy allows are additive and an unrestricted peer cancels the excepts of the external HTTPS rule.
- `flagServiceCaBundleConfigMap` adds a private CA to the trust the OFREP client uses. The gateway builds one shared transport per replica for this.

A deployer who sets `egressCIDRs.llmProviders` narrows the public HTTPS rule, so a public OFREP service needs its CIDR added there, as a connector or webhook host already does.

### AB-6 Bridge contract

A bridge is a deployer-run HTTP service that wraps a vendor SDK and speaks OFREP to the gateway. The gateway has no knowledge of bridges, and a bridge is an ordinary OFREP server to it. The Lenny repository contains no bridge implementation or example, and the tier 0 check in "Tests" keeps vendor flag SDKs out of the gateway's dependency list. The operator guide states what the gateway requires:

- `POST /ofrep/v1/evaluate/flags/{key}`, with the experiment ID as the key and a JSON body of `{"context": {...}}`.
- The context carries `targetingKey`, `user_id`, `tenant_id`, `runtime`, and session labels. An anonymous session uses `anon:<session_id>` for both `targetingKey` and `user_id`.
- A 200 response carries `key`, `reason`, and either `variant` or a `value` that resolves as in "AB-3". `control` means no enrollment.
- A flag the vendor does not know returns 404 with `errorCode: FLAG_NOT_FOUND`.
- The response arrives within the tenant's `timeoutMs`, which defaults to 200 ms.

A bridge holds one vendor configuration per process, which removes the process-global limit: tenants with different Statsig or Unleash projects run separate bridges. Each bridge needs a `gateway.flagServiceEgress` entry. Exposure events and any vendor analytics are emitted from the bridge when it evaluates a flag.

## Limits

- **A network hop on session creation.** A vendor SDK evaluates in memory after its first load, and OFREP adds an HTTP call per evaluated external experiment. Evaluation is sequential until the first match, so the worst case is the number of external experiments multiplied by `timeoutMs`. The OFREP bulk endpoint would reduce this and is not adopted, because a slow call is bounded and a tenant rarely runs many concurrent external experiments on one base runtime.
- **A new deployer component for three vendors.** A LaunchDarkly, Statsig, or Unleash deployer operates a bridge and its availability. A bridge outage falls under the existing rule: sessions run the base runtime, the breaker opens, and `ExperimentTargetingCircuitOpen` fires.
- **Unenrolled sessions during an outage.** This behaviour is unchanged. External targeting fails open to the base runtime with no experiment context.
- **OFREP maturity.** The OpenAPI document is at version 0.4.0, and flagd labels its endpoint experimental **[documented]**. The gateway uses the single-flag evaluation endpoint alone.
- **Sticky caching hides repeat evaluations.** Under `sticky: user`, a cached assignment does not reach the flag service, so a vendor's exposure count reflects first evaluations. This behaviour is unchanged.
- **User identifiers leave the platform.** The evaluation context sends `user_id` to the flag service. The sticky cache is covered by erasure, and the flag service or bridge is a deployer responsibility. This behaviour is unchanged, and a bridge adds a location to the deployer's list.

## Alternatives rejected

- **Keep the SDK providers and fix their defects.** The mutex and deadline are fixable. The process-global limit lies in the vendor SDKs, and vendor code would remain in the gateway process.
- **Keep the LaunchDarkly adapter alone.** The dependency and the session-creation defects would remain for that vendor, and the `provider` field would stay.
- **Ship a compiled bridge example in a separate Go module.** Lenny would maintain vendor code in its repository and track SDK releases, which the owner declined.
- **Run the SDK providers as a Lenny-operated sidecar or service.** This keeps the integration out of the gateway process but makes Lenny own the same vendor surface and one more component.
- **Load providers as Go plugins.** Go plugins need a matching toolchain and dependency set, and they share the gateway's address space, so the process-boundary concern remains.
- **Keep `provider: ofrep` as a one-value enum.** A discriminator with one value carries no information, and the platform takes no compatibility shims before its first deployment.
- **Let the client choose the variant at session creation.** A caller could then select any pool, which moves assignment outside the platform's control and removes the experiment's randomization guarantee.
- **Drop `mode: external`.** Assignment would be limited to percentage splits, which removes the hook the owner asked to keep.

## Specification changes

The following edits locate their targets by heading:

| Section | Change |
| :-- | :-- |
| §10.7 Experiment Primitives, opening paragraph | Replace "through the OpenFeature Go SDK — either via OFREP (recommended) or a built-in provider adapter" with assignment through OFREP. Replace the vendor list with flag services that expose OFREP, and name the bridge pattern. |
| §10.7 "Targeting modes" and "Targeting schema" | Describe `external` as delegating to an OFREP service. |
| §10.7 "Tenant-level experiment targeting configuration" | Remove the integration paths list, the statement that LaunchDarkly exposes OFREP through its Relay Proxy, and the SDK examples. Add the "AB-2" schema and "AB-4" rules. Correct the endpoint example. |
| §10.7 "Evaluation semantics" | Replace `client.ObjectValue(...)` with the OFREP POST. Merge the two resolution bullets into the "AB-3" table. Restate the "not returned by the provider" rule in terms of `FLAG_NOT_FOUND`. |
| §10.7 "OpenFeature evaluation failure behavior", "Targeting circuit breaker", and "Anonymous session handling under external targeting" | Rename to OFREP terms, and state that `FLAG_NOT_FOUND` counts as a breaker success. |
| §10.7 "Full A/B testing with external experimentation and eval platforms" | In the first row, change "external targeting via LaunchDarkly, Statsig, Unleash" to external targeting through OFREP. The analysis row stays. |
| §13.2 gateway egress row | Add the `gateway.flagServiceEgress` supplemental rule. |
| §15.1 validation rules | Add the `experimentTargeting` rules from "AB-2". |
| §16.1 "Experiment Targeting" | Remove "sourced from OpenFeature provider metadata", so `provider` is the endpoint hostname. |
| §16.6 `experiment.targeting_failed` and `experiment.unknown_variant_from_provider` | Remove OpenFeature client terms, and state the `provider` field value. |
| §18 build sequence, experiment phase | Change "OpenFeature/OFREP external-targeting integration" to OFREP. |
| §23 "Built-in A/B experimentation" | Replace "external targeting services (LaunchDarkly, Statsig, Unleash, generic webhook)" with external targeting through OFREP. The webhook claim has no implementation in the tree **[source]**. |

The channel registers in §28 and the scenarios in §29 name no provider **[source]**. The proposal's verifier should confirm this and search §28 for a gateway-to-flag-service link that needs a row.

## Non-specification changes

| Area | Change |
| :-- | :-- |
| `pkg/experiment/external.go` | "AB-2" types and validation. `ResolveExternalVariant` gains the neither-field result. |
| `pkg/gateway/experiment/ofrep` | Typed not-found result, refused redirects, and a shared transport with the optional CA bundle. |
| `pkg/gateway/sessionserver/experimentrouter.go` | "AB-1" removals, the "AB-3" outcomes, labels in the context, secret resolution, and hostname on events. |
| `pkg/gateway/externalapi/admin/tenants.go` | Strict decode of the block. The `platform-admin` gate already exists. |
| `cmd/lenny-gateway` | Remove the cache wiring and the `openfeature` shutdown step, and update `shutdown_helper_test.go`. |
| `charts/lenny` | `flagServiceEgress`, the CA bundle mount, the schema, and render refusals. Regenerate `pkg/embedded/manifests/manifests.yaml` if it changes. |
| `docs/operator-guide/openfeature-integration.md` | Rename for OFREP, delete the vendor sections, correct the endpoint and credential examples, add the network step and the bridge contract. |
| `docs/reference/configuration.md` | Replace the targeting table. It already lists `flagd`, `goff`, `ofrep.timeoutMs`, `ofrep.cacheTTLSeconds`, and a `percentage` default, none of which exist in the code **[source]**. |
| `README.md`, `docs/index.md`, `docs/operator-guide/configuration.md`, `docs/about/comparisons.md`, `docs/about/why-lenny.md`, `docs/about/status.md`, `docs/getting-started/concepts.md`, `docs/getting-started/architecture.md`, `docs/api/admin.md`, `docs/tutorials/evaluation-scoring.md`, and `docs/reference/glossary.md` | Remove the claim that Lenny integrates with named vendors directly. State OFREP, and link the operator guide. |
| `pkg/gateway/ARCHITECTURE.md`, `tests/registers/change-graph-coverage.yaml`, `tests/change-graph.json`, and `tests/spec-map.json` | Remove the package and the provider references. |
| `BUILD-GAPS.md` | Record that the proposal retires the F-10.7.3 deliverable, and leave the closed finding's text intact. Another agent is editing this file on this branch, so this edit waits for the proposal. |
| `TEST-GAPS.md` | Retarget T-10.7.7, T-10.7.9, T-10.7.10, T-META.5, and the other findings that name an OpenFeature provider to OFREP. |

## Tests

| Tier | Coverage |
| :-- | :-- |
| 0 | A static check fails if a vendor flag SDK or `open-feature/go-sdk` enters the gateway's dependency list (ABR-1). |
| 1 | Each row of the "AB-3" table. `Validate` for the scheme, the path rule, reserved headers, and unknown fields. Labels in the context, including a label that collides with a reserved attribute. |
| 3 | The OFREP client against a stub that follows the published OpenAPI document, covering the root path, every response in "AB-3", and refused redirects (ABR-3). |
| 4 | Through session creation on the compose stack: enrollment through OFREP, one missing flag alongside a second experiment that still enrolls (ABR-4), a provider outage reaching the event, counter, and breaker, and `sticky: user` with a cached hit (ABR-2). A delegated child under `independent` is evaluated by the stub, and one under `inherit` is not (ABR-7). |
| 5 | On Kind with chart values: an in-cluster stub is blocked by default and reachable with `flagServiceEgress`, and IMDS stays blocked (ABR-6). |
| 7a | Concurrent session creation shows no serialization across tenants, and breaker state stays per tenant under `-race`. |
| 8 | The flag service is killed and restored, and the breaker opens and recovers with the session path unaffected. |
| 9 | A tenant response never contains a resolved credential. A `tenant-admin` cannot write `experimentTargeting`. The credential value never appears in events or logs (ABR-5). |
| 11 | Every `experimentTargeting` example in the documentation passes `Validate`, and the `ExperimentTargetingCircuitOpen` runbook link resolves. |

Every test carries the `// spec:` annotation, and tests at tier 2 and above carry `// diagnosis:`, as `test-coverage.md` requires.

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Run the client against flagd and one other OFREP server | The evidence here is one local stub and vendor pages. | Open. It needs the owner's approval to pull those images. |
| Spike a bridge for one vendor, including its latency against 200 ms | No bridge exists, so the documented contract has not been implemented against a vendor SDK, and the documentation cannot state a bridge's size or latency without this result. A test account or a local Unleash instance is required. | Open. |
| Confirm each vendor's OFREP position before the documentation names it | The vendor table reads a limited set of pages. The Unleash README and Statsig's OpenFeature pages were not read, and the GO Feature Flag and Flipt pages returned 404. | Open. |
| Confirm the Kubernetes Secret read path is cached in the gateway | "AB-4" sets a 60-second cache on the assumption that a direct read is too slow for session creation. | Open. |
| Confirm that §28 needs no new link row | "Specification changes" assumes it. | Open, for the proposal's verifier. |

## Instructions for reviewers of other designs

No other design mentions `experimentTargeting`, OFREP, or an OpenFeature provider **[source]**, so none needs a content change for this removal. Two reviewers should check one point each:

- `tenant-scoping-identity-design.md` states that `allow-gateway-egress-llm-upstream` is the gateway's only external HTTPS path. `gateway.flagServiceEgress` adds a deployer-declared path, so that reviewer should decide whether the sentence needs a qualifier.
- A future eval storage removal design edits the same §10.7 section and the same "Full A/B testing" table. Whichever lands second rebases its staged edits by heading.

## Proposal split and order

One proposal covers the whole design, because the removal is only safe once OFREP works:

1. Land the specification edits.
2. Correct the OFREP path: "AB-3", the "AB-2" validation, "AB-4", and "AB-5", with tests at each tier.
3. Remove the SDK path ("AB-1"), tidy `go.mod`, and add the tier 0 guard.
4. Update the documentation and records.

This design is independent of `connector-authorization-and-external-memory-design.md` and the other designs on this branch. The child-session check in that design runs after experiment routing, and this design leaves the order and outcome of routing unchanged.

## Sources

- [OFREP overview](https://openfeature.dev/docs/reference/other-technologies/ofrep/)
- [OpenFeature specification, Appendix C](https://openfeature.dev/specification/appendix-c/)
- [OFREP OpenAPI document](https://raw.githubusercontent.com/open-feature/protocol/main/service/openapi.yaml)
- [OpenFeature ecosystem](https://openfeature.dev/ecosystem)
- [flagd OFREP reference](https://flagd.dev/reference/flagd-ofrep/)
- [ConfigCat Proxy endpoints](https://configcat.com/docs/advanced/proxy/endpoints/)
- [LaunchDarkly Relay Proxy endpoints](https://raw.githubusercontent.com/launchdarkly/ld-relay/v8/docs/endpoints.md)
- [Statsig forward proxy](https://docs.statsig.com/server/concepts/forward_proxy)
- [Unleash Edge](https://docs.getunleash.io/reference/unleash-edge)
