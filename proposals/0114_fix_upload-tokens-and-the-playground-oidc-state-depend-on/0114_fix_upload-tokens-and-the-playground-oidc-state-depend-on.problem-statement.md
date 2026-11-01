# Problem: Upload tokens and gateway OAuth state fail across gateway replicas

## Statement

Three gateway flows verify a client-held value against a secret that exists only in one process, although §10.1.1 defines replicas as stateless proxies over externalized state and states that a client can reach any replica. The default chart runs two gateway replicas with no session affinity on the gateway Service, so each flow fails when the verifying request reaches a replica other than the one that issued the value, and after a restart of the issuing replica. The findings are F-7.1.31 (High) and F-27.3.16 (Medium). The third instance is unfiled.

1. **`uploadToken` (F-7.1.31).** `buildTokenSigningStores` seeds the `uploadToken` key ring from `crypto/rand` at startup under key id `boot`, and every process then runs its own `Rotator`, which installs a fresh random key every 24 hours. Sharing the boot seed alone therefore does not fix the defect, because the replicas diverge at the first rotation. A token minted on one replica fails HMAC verification on every peer and after a restart. Since F-7.4.21 every finalize verifies the token before reading the session row, including a finalize that uploaded nothing, so a finalize on a peer fails with `401 UPLOAD_TOKEN_INVALID`. Token-authorized uploads fail the same way. §7.1 says only "a gateway-held signing key" and states neither a key source nor how rotation is coordinated. The wire code `UPLOAD_TOKEN_INVALID` appears nowhere in `spec/`.

2. **Consumed-token record (F-7.1.31).** The consumed-token tracker is a per-process `MemoryTracker`, so a replay reaching a peer does not receive the §7.1 `410 UPLOAD_TOKEN_CONSUMED`. This is a wrong-status defect and no replay is admitted. Today the peer refuses at the HMAC check with 401. Once a token verifies on every replica, the same replay reaches the §15.1 precondition and is refused there, still without 410. Finalize and token-authorized upload run only in `created`, and that state lives in the shared session row. The contract needs either a shared record or a rule derived from the row.

3. **Playground OIDC state (F-27.3.16).** The state-cookie key `stateSecret` is drawn per process at package init, so in `oidc` mode a login whose callback reaches another replica, or follows a restart, redirects to `oidc_state_invalid`. The playground is off by default.

4. **Connector OAuth state (unfiled).** `credentialsurface.go` seeds the connector `StateSigner` per process, and the callback verifies that signature before it consults the Redis-backed `StateStore`. A callback on a peer replica therefore fails with `CONNECTOR_OAUTH_STATE_INVALID` although the flow context is in Redis. The chart renders neither `--connector-oauth-callback-url` nor its environment variable, so this flow is reached only when a deployer sets one of them.

These items share a cause and do not share a remedy. No shared key source is implied: item 3 has a recorded owner direction toward server-side Redis state, and items 1 and 2 have an option that uses the session row instead of any shared key. The authority for each verification is the open design question.

Per-replica `uploadLimits` and `uploadAborts` are out of scope. They are per-process state on the upload path, and they do not refuse a token. The dead-letter receipt signer is out of scope pending evidence that a cross-replica verification depends on it.

The kind stack runs two gateway replicas, so a kind test of this defect is feasible today by addressing two pods directly.

## Evidence

The evidence below was verified against the tree at `edee08bdf`.

- Verified: `cmd/lenny-gateway/token_signing.go`, `buildTokenSigningStores`, fills a seed from `crypto/rand`, builds the ring with key id `boot`, and constructs `uploadtoken.NewMemoryTracker()` and a `Rotator`.
- Verified: `cmd/lenny-gateway/workers.go` starts `w.uploadRotator.Run`, and the default `keyGen` in `pkg/uploadtoken/rotator.go` draws from `crypto/rand`.
- Verified: `pkg/uploadtoken/uploadtoken.go`, `Verify`, checks the HMAC first and the tracker last.
- Verified: `pkg/gateway/sessionserver/finalize_handler.go`, `handleFinalize`, calls `verifyUploadToken` before `s.store.Get`. `writeUploadTokenError` in `upload.go` maps an HMAC failure to `401 UPLOAD_TOKEN_INVALID`.
- Verified: `pkg/gateway/sessionserver/upload.go`, `admitUpload`, passes no capability flag to `session.Validate`, and `preconditionTable` in `pkg/api/v1/session/session.go` limits finalize to `created` and upload to `created` unless `CapabilityMidSessionUpload` is set. §7.4 (Upload authorization) states that mid-session uploads use the caller's bearer.
- Verified: `pkg/gateway/mcpfabric/playground/crypto.go` draws `stateSecret` with `mustRandom(32)`, and its comment calls the cookie "single-process-scoped", which §10.1.1 contradicts.
- Verified: `cmd/lenny-gateway/credentialsurface.go` seeds `stateSeed` per process, and `pkg/gateway/externalapi/admin/connector_oauth.go` calls `StateSigner.Verify` before `StateStore.Consume`. A search of `charts/`, `tests/testinfra/kind/`, `docs/`, and `spec/` finds neither the flag nor the environment variable.
- Verified: `charts/lenny/values.yaml` sets `gateway.replicas: 2`, `gateway-deployment.yaml` renders it, and the only `sessionAffinity` in `charts/lenny/templates` is on `lenny-ops`. `tests/testinfra/kind/e2e-values.yaml` sets `replicas: 1` only in the controller block, and its gateway block has no replicas key. The live `lenny-e2e` Deployment reports `lenny-gateway` 2/2.
- Verified: §7.1 (`uploadToken` format and security properties) names no key source. §27.3.1 requires a "short-lived, signed, HttpOnly" state cookie and names no key source. §10.1.1 states the correctness rule that sticky routing is an optimization.
- Verified: F-7.1.31 and F-27.3.16 are open in `BUILD-GAPS.md`, and plan §10.2 records the entry "Shared gateway key-source proposal" with the owner direction for F-27.3.16.
- Verified by the prior-art and alternatives lenses, and consistent with the `uploadTokenDigest` use in `handleFinalize`: `migrations/0001_initial_schema.up.sql` and `sessionstore.go` persist the token digest and expiry on the session row at every mint path. `upload_token_digest` appears nowhere in `spec/`, `charts/`, or `schemas/`.

Corrected citations:

- The sentence "The kind stack runs one replica, so no kind tier exercises this" comes from the F-7.1.31 note of 2026-10-31 in `BUILD-GAPS.md`. It is false, as stated above.
- F-27.3.16's "roughly one login in N fails" is inverted. Under per-request balancing about one login in N succeeds.

## Who observes it

- An API or SDK client on any non-dev install. The chart default and the tier presets run two or more gateway replicas, and only the dev preset runs one. The client sees `401 UPLOAD_TOKEN_INVALID` on finalize or upload. The Go SDK retries only 429, 500, 502, 503, and 504, so the 401 reaches the caller at once.
- A browser user of the playground on an install that sets `playground.enabled=true` with `authMode=oidc`. The user sees the error page with `oidc_state_invalid`.
- A deployer who sets the connector OAuth callback URL on a multi-replica gateway, whose users see `CONNECTOR_OAUTH_STATE_INVALID`.
- The rate depends on the edge. Per-request load balancing, which the chart comments name as the production topology through an Ingress, fails about (N-1)/N of requests that verify a token or state value. A single kept-alive connection through an L4 path stays on one pod. This rate is reasoned from general Kubernetes behavior and was not measured on this stack.
- The repository's code rules describe the platform as pre-deployment, so no external deployment is affected today. The harm lands on the first multi-replica deployment.

## What breaks if nothing changes

- A multi-replica deployment cannot finalize reliably, including a session that uploads nothing. Finalize is mandatory on the create-then-finalize path, and the Go, Python, and TypeScript SDKs and the MCP tool layer all send the token.
- A caller retry works only if it reaches the minting replica. A session that keeps failing runs until the `created` deadline (default 300 s). A pod claim can already exist at this stage, so a refused finalize could hold a claim until that deadline. The release path was not traced.
- A playground login under per-request balancing succeeds at about one in N, and a repeated attempt draws at the same odds.
- A restart or rolling update refuses only in-flight session setups (300 s window) and logins (10 minute window). A fix must cover this path, and it does not raise the priority.
- A client receives a 401 code the specification does not define. The `errorclassify` table also omits it, beside the three other `UPLOAD_TOKEN` codes.
- `uploadToken` replay stays refused either way, so inaction carries no replay exposure. The cost on that path is a non-conforming status.

## Findings this unblocks

- F-7.1.31 (High), `uploadToken` key ring and consumed-token tracker.
- F-27.3.16 (Medium), playground OIDC state.
- F-15.1.69 (Low), `UPLOAD_TOKEN_INVALID` absent from the §15.1 catalog.
- The unfiled connector OAuth `StateSigner` instance, which RECORDS-1 files as F-9.3.18 and closes.

## Prior art considered

- **Proposal 0097** made Lenny-issued JWTs verify across replicas through a remote KMS key. Its summary leaves the `uploadToken` ring out as a separate finding. The §10.2 `platform:jwt-signing` key is a remote asymmetric ES256 key with no HMAC use, so it is not a drop-in key source.
- **Proposal 0104** declines F-7.1.31 as a separate plan step. No other proposal stages either finding. Sibling drafts 0111 and 0113 have no hit for the findings, `uploadToken`, `stateSecret`, or the state cookie name, as of the prior-art lens's search.
- **§9.3 and the §12.4 `conn:oauth:state:{state}` row** define replica-shared, single-use, 10-minute Redis state, implemented by `pkg/connectoroauth/redis_state_store.go` with an atomic tombstone `Consume`. The playground already uses Redis for its session records under `oidc`, wired in `cmd/lenny-gateway/httpsurface.go`. This is the model the owner direction names for F-27.3.16. The store is the part to copy: the connector signer has the defect in item 4. §9.3 requires only a random `state` stored in Redis and matched exactly, while §12.4 calls the value an "HMAC nonce". `FlowContext` is connector-specific, and §27.3.1 requires a signed cookie, so spec edits and a §12.4 row are needed.
- **The session row** holds the digest and expiry of the exact token, written at create, create-and-start, derive, and replay. Any replica can authenticate a token by comparing digests, with no shared key, no rotation, and no tracker, and `ready` and later states imply consumption. This option requires a §7.1 edit, because §7.1 and §7.4 mandate HMAC verification and rotation, `docs/client-guide/session-lifecycle.md` states HMAC, and `handleFinalize` would read the row before the token check. `admitUpload` already reads it first.
- **The `ConsumedTracker` interface** anticipates a Redis tracker in its package comment, and no such implementation exists.
- **The suggested resolutions** in `BUILD-GAPS.md` assume a shared key and a Redis or Postgres tracker. The plan's "apply the same reasoning to F-7.1.31 where it fits" is a lead rather than a settled requirement.
- **Test infrastructure:** `twoReplicaDrivers` pins one driver to each of two Ready gateway pods, and `tests/testinfra/gateway` starts replicas that share a stack key for tier-4 cross-process tests.

## Validated premises

The lens verdicts are premise revise, evidence revise, prior-art stands, scope revise, impact revise, and alternatives revise.

### Upheld

- **The `uploadToken` ring and tracker are per process (premise, evidence, prior-art).** No flag, Secret, or store shares either.
- **Every finalize verifies the token first (premise, evidence, impact).** A finalize with no upload can fail with 401. REST, MCP, and the SDKs carry the token, so no client path bypasses the check.
- **Multi-replica is the default deployment and nothing masks the fault (premise, evidence, prior-art, impact, alternatives).** Service affinity would contradict the §10.1.1 correctness rule in any case.
- **The playground state key is per process (premise, evidence, impact).** It is narrowed to `authMode=oidc` with the playground enabled, and the failure also follows a restart.
- **The specification gap exists (premise, evidence, impact).** §7.1 names no key source or tracker scope, so the fix goes through the proposal pipeline.
- **The defect is unsolved (prior-art).** Only records commits cite either finding.
- **No-change is not acceptable (alternatives).** Restart loss alone would be tolerable, and cross-replica failure breaks §10.1.1.

### Revised

- **Rotation is part of the defect (premise, evidence, prior-art, scope).** The boot seed is not the only per-process key. Each process rotates independently, so a shared-key design must share rotation or remove per-process rotation.
- **A third instance exists (scope, alternatives).** Connector OAuth has the same defect and no finding names it. This consolidation adds a fact neither lens reported: the chart does not render the flag, so the flow is opt-in. It is lower in reach than the lenses imply and it remains a defect.
- **The items are not one key problem (scope, alternatives).** The queue entry's "shared gateway key-source" framing does not hold, because the plan rejected every key source for the playground cookie. This statement frames the defect as the authority for verification.
- **The kind observation is partly explained (premise, evidence, impact).** The session driver reaches the gateway through a port-forward, which pins one pod. The tier-5 eager-claim tests are different: `finalizeRetry` posts through the Service ClusterIP from a probe pod, returns on any status below 500, and the tests assert 200. `eager_claim_e2e_test.go` is the only tier-5 file that spells the `/finalize` path directly; finalize calls made through the session driver were not counted. By reading, they should fail intermittently on two replicas. They were not run, and their skip paths and pass history were not checked. The premise, impact, and alternatives lenses' explanations do not cover this path, so the question stays open.

### Refuted

- **"The kind stack runs one replica" (premise, evidence, scope, impact, alternatives).** The `replicas: 1` in the overlay is the controller's. The gateway takes the chart default of 2, and the live Deployment reports 2/2.
- **"Single use holds only per replica" as a security failure (premise, impact, prior-art, alternatives).** A consumed token is not accepted on a peer. The observable fault is the wrong status.
- **A consumed token passes a peer's verify today (claim in the impact lens).** The peer's ring differs, so the token fails at the HMAC check with 401. It reaches the precondition only after a key or authority is shared. The premise lens has this right.
- **A shared key without a shared tracker opens a replay hole (evidence lens).** A replay on a peer would pass `Verify` and then be refused by the `created` precondition on the shared row, so the result is a wrong status. Key and consumption should still be designed together, because the row option answers both. A later capability-gated path that admits token uploads in `running` would reopen this conclusion. Whether any transition returns a session to `created` was not surveyed.
- **"A new shared consumed store is required" (premise, prior-art, alternatives).** The row state already implies consumption, so a store is one option among others.
- **The direct-fix reading of F-27.3.16 in `BUILD-GAPS.md` (evidence, prior-art).** No existing key source has specification text for this use.

### Noted without a verdict

- **Split question (scope against the others).** The scope lens recommends cutting 0114 to F-7.1.31 and moving items 3 and 4 to a second proposal, because they share no code, the spec sections are disjoint except a possible §12.4 row, and F-27.3.16 has a settled direction while F-7.1.31 does not. The other lenses keep the items together. This statement keeps them together under the plan's single entry and the one §10.1.1 rule, and records the split for the owner.
- **Removing the playground signature (alternatives).** The state check is a double-submit comparison, so the HMAC adds little beyond protecting `IssuedAt`. The alternatives lens does not recommend this, because it leaves the PKCE verifier in the browser and provides no single-use state.
- **Severity of F-27.3.16 (impact).** Medium is defensible, and the record understates the rate.
