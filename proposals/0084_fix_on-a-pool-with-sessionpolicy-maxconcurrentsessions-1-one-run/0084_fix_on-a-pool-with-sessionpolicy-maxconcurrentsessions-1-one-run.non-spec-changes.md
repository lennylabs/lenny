# Non-spec changes: Intra-pod MCP and direct-mode usage cannot attribute calls on concurrent pools

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (implementation-facing)

The adapter gains one attribution resolver, `Server.runtimeSession(declared string) (string, error)`, and every runtime-originated request that needs a session goes through it: the platform MCP provider, the connector MCP provider, and the direct-mode token sink (CODE-2, CODE-3). The resolver implements the two rules of the spec's §15.4.3 **Session address.** paragraph (SPEC-1a) and the attribution sentences of the `CH-RUNTIMEOPS` `llm_request_completed` row (SPEC-4a). `callingSession` is deleted, and `soleSession` stays as the unaddressed arm and as the control-event stamp.

The declared session reaches the resolver without changing the `mcp.ToolProvider` interface. `AuthenticateInitialize` returns the `_lennySessionId` it stripped, and `ServeConn` stores it on the per-connection context it already passes to `Provider.List` and `Provider.Call` (CODE-1). On `CH-RUNTIMEOPS` the frame struct carries it (CODE-2).

Lifting the refusal makes co-tenants depend on the shared surface, so the surface state changes before the providers do (CODE-4 lands before CODE-3). The nonce is minted at claim and pinned for the armed surface, the surface arms whenever it is unarmed, it is cancelled only when the runtime is idle and no started slot remains, connector servers are keyed by connector id and opened idempotently, and one helper replaces the three copies of the arm-and-publish block in `session.go`, `resume.go`, and `sdkwarm.go`.

The first-party runtime SDKs stay single-session and unaddressed. They change only to tolerate a connector the gateway refuses for the session (CODE-6), which the conformance harness then checks (CODE-7).

## Staged code changes

### CODE-1 · pkg/adapter/mcp/nonce.go, pkg/adapter/mcp/server.go · the session address on the connection

`nonce.go`:

- Add `const SessionParamKey = "_lennySessionId"` and `var ErrSessionInvalid = errors.New("mcp: _lennySessionId is not a non-empty string")`.
- Change the signature to `func AuthenticateInitialize(request []byte, expectedNonce string) (cleaned []byte, sessionID string, err error)`.
- After the nonce comparison succeeds: when `params` holds `SessionParamKey`, decode it into a string. A decode failure or an empty string returns `ErrSessionInvalid`. Otherwise delete the key from `params` and return the value as `sessionID`. Delete `NonceParamKey` as today. An absent key returns `sessionID == ""`.

`server.go`:

- `ServeConn` calls `cleaned, declared, err := AuthenticateInitialize(first, nonce)`. Any error, `ErrSessionInvalid` included, returns before the initialize response, so the connection closes with no tool dispatch (SPEC-1b).
- Replace `ctx := context.Background()` with `ctx := withCallerSession(context.Background(), declared)`. `withCallerSession` stores the value under an unexported context key type.
- Add the exported accessor `func CallerSession(ctx context.Context) string`, which returns `""` when the connection named no session.
- The `RequireChallenge` exchange and `OnHandshake` are unchanged.

Rewrite the doc comments of `AuthenticateInitialize` and `ServeConn` to state the optional field and cite `// spec: §15.4.3 (session address)`. Update the existing call sites in `pkg/adapter/mcp/nonce_test.go` to the three-value return in the same commit.

### CODE-2 · pkg/adapter/runtimegeneration.go, runtimeops.go, usage.go, adapterevents.go, cmd/lenny-adapter/main.go · the attribution resolver and the token sink

This deliverable lands alone under fallback (b) of the pending attribution decision (see the summary's **Watch out for.**).

**Resolver** (`runtimegeneration.go`, beside `soleSession`):

```go
func (s *Server) runtimeSession(declared string) (string, error) {
	if declared != "" {
		if err := s.checkSessionBound(declared); err != nil {
			return "", err
		}
		return declared, nil
	}
	if id := s.soleSession(); id != "" {
		return id, nil
	}
	return "", status.Error(codes.FailedPrecondition,
		"the pod's shared runtime process has been given more than one session; the request names none")
}
```

Its doc comment cites §15.4.3 **Session address.** rules 1 and 2 and the `CH-RUNTIMEOPS` `llm_request_completed` row as the rules it implements, and states the admission window: the binding is confirmed when a call or fold enters, under one hold of `s.mu`, as `callingSession` did. The resolver does not stop an admitted call from reaching the gateway after the declared session's release returns. The gateway's forwarded-session membership check (proposal 0070 §5) is the backstop. `checkSessionBound` is the only live-binding check; do not add a second one keyed on `runtimeLive`.

**Frame and sink:**

- `runtimeops.go`: `lifecycleFrame` gains `SessionID string \`json:"sessionId,omitempty"\``. The `tokenSink` interface becomes `AddTokens(sessionID string, inputTokens, outputTokens int64)`. In `readLoop`'s `llm_request_completed` arm, `lc.adjustInflight(frame.Provider, -1)` stays first and unconditional, and the fold becomes `lc.usage.AddTokens(frame.SessionID, frame.InputTokens, frame.OutputTokens)` under the existing non-zero guard. Rewrite the `tokenSink` and `AddTokens` doc comments, which still cite "§6.1 one session per pod", and the `InputTokens`/`OutputTokens` field comment to describe the optional `sessionId`.
- `usage.go`: `sessionTokenSink` holds `meter *SessionUsageMeter` and `resolve func(string) (string, error)`. `AddTokens` resolves the declared session. On an error it logs `slog.Warn("direct_mode_token_fold_dropped", "declared_session_id", sessionID, "error", err.Error())` and returns without folding. On success it calls `meter.Add` under the resolved identifier. The log fires once per dropped frame, and it is expected on an overlapped pod whose direct-mode runtime omits `sessionId`. `NewSessionTokenSink(meter *SessionUsageMeter, resolve func(string) (string, error)) tokenSink`. `WireDirectModeUsage` passes `s.runtimeSession` directly.
- `adapterevents.go`: `SoleSessionID` loses its only production caller. Rewrite its doc comment to state that it is the read-only occupancy probe the tier-2, tier-7a, and tier-9 tests read, and that production attribution goes through `runtimeSession`. `emitControlEvent` keeps calling the unexported `soleSession`.
- `cmd/lenny-adapter/main.go`: rewrite the comment above the `WireDirectModeUsage` call, which describes fold-time resolution through `SoleSessionID`.

Update the fake sinks in `pkg/adapter/runtimeops_test.go` and the sink construction in `pkg/adapter/usage_test.go` to the new signatures in the same commit; TEST-2 replaces `TestSessionTokenSinkResolvesCurrentSession_spec_4_7`.

### CODE-3 · pkg/adapter/platformtoolprovider.go, pkg/adapter/connectortoolprovider.go · providers attribute through the resolver

- `platformToolProvider.List` and `Call` resolve `sessionID, err := p.server.runtimeSession(mcp.CallerSession(ctx))` and return the error unchanged when it is non-nil, so a refused request never reaches the forwarder. `connectorToolProvider.List` and `Call` do the same.
- Delete `callingSession`. Rewrite the `platformToolProvider` and `connectorToolProvider` type comments: attribution is per request under §15.4.3 **Session address.**, and the check runs on each `List` and `Call` because a connection outlives its session.
- `pkg/adapter/holdstate_test.go`: replace the `s.callingSession()` call with `s.runtimeSession("")`.
- Comment-only sync with SPEC-6c: in `pkg/gateway/connectors/connectorauthz/connectorauthz.go`, `pkg/gateway/connectors/connectorinvoke/invoker.go`, and `tests/tier4_integration/connector_delegation_scope_test.go`, change "calling pod's effective" to "calling session's effective".

This deliverable does not land under fallback (b).

### CODE-4 · pkg/adapter/slotsession.go, server.go, platformmcp.go, connectormcp.go, manifest.go, session.go, resume.go, sdkwarm.go · pod-surface state

**State** (`server.go`):

- `mcpSurfaceMu sync.Mutex` serializes binding, connector opening, retirement of stale servers, and manifest publication for the intra-pod MCP surface.
- `mcpPendingNonce string` holds the nonce a claim minted for the surface until the platform server binds.
- `mcpArmedNonce string` keeps its meaning: the nonce the running servers authenticate.
- `connectorCancels` becomes `map[string]servedConnector`, keyed by connector id, where `servedConnector` holds the `sessionConnector` and its stop function.
- `mcpRetiring []context.CancelFunc` holds the stop functions of a surface a takeover retired, until a holder of `mcpSurfaceMu` runs them.

**Lock order.** The per-slot guard, then `mcpSurfaceMu`, then `s.mu`. `cancelPodMCPIfRuntimeIdle` runs under the releasing slot's guard (from `finishSlotRelease`), and `Resume` calls the helper while holding its own guard. No path takes a slot guard while it holds `mcpSurfaceMu`. Stop functions run under `mcpSurfaceMu` with `s.mu` released; `mcp.Server.Serve` returns once its listener closes and does not wait for open connections, so a stop never blocks on a long `tools/call`.

**Rules.** These rules are the home of the surface behaviour. Tests cite them by number.

1. *Surface-bearing pod.* A pod carries an intra-pod MCP surface only when `ManifestDir != ""`, `MCPSocket != ""`, and `RuntimeKind != RuntimeKindMCP`. On any other pod a claim records no pod nonce and the helper binds nothing. When `ManifestDir != ""`, the helper still publishes the manifest with a per-write nonce from `newMCPNonce` and an empty `connectorServers`. When `ManifestDir == ""`, the helper neither mints nor publishes, which keeps today's no-manifest no-op. A pod with `MCPSocket == ""` no longer opens connector servers.
2. *Arm when unarmed.* `claimSessionSlot` mints a candidate nonce with `newMCPNonce` before it takes `s.mu`; a mint failure fails the claim before any state changes. `claimPodMCPStartLocked(sessionID, candidate)` runs under `s.mu` and returns the claim's pod nonce, stored in the new field `slotClaim.mcpNonce`, which replaces `slotClaim.startMCP`. When `s.mcpSession == ""`, it sets `s.mcpPendingNonce = candidate` and `s.mcpSession = sessionID`, and returns `candidate`. The predicate no longer reads `len(s.slots)`.
3. *Takeover.* When `s.mcpSession` names another session and no started slot other than the claimant's remains (`anyStartedSlotLocked(except)`), the claim appends `takePodMCPCancelsLocked()`'s stop functions to `s.mcpRetiring`, sets `s.mcpPendingNonce = candidate` and `s.mcpSession = sessionID`, and returns `candidate`. `takePodMCPCancelsLocked` also clears `mcpPendingNonce`, `mcpArmedNonce`, and the connector map.
4. *Reuse.* Otherwise the claim returns the live pod nonce: `s.mcpPendingNonce` when it is set, else `s.mcpArmedNonce`. The claim does not re-arm.
5. *Retirement.* `claimSessionSlot` no longer runs stale cancels itself. Every holder of `mcpSurfaceMu` first takes `s.mcpRetiring` under `s.mu`, releases `s.mu`, and runs the functions. `claimSessionSlot` takes `mcpSurfaceMu` after its critical section and drains once, so a claim that fails before the helper still retires the stale surface.
6. *Ensure and publish.* The helper `ensureAndPublishSessionMCP(ctx context.Context, sessionID string, claim slotClaim, in manifestInputs) error` in `manifest.go` holds `mcpSurfaceMu` for its whole body and runs these steps in order:
   1. Drain `s.mcpRetiring` (rule 5).
   2. Under `s.mu`, compare the live pod nonce (rule 4's value) with `claim.mcpNonce`. When they differ, return `status.Errorf(codes.Aborted, "session %s slot was reclaimed while the start was in flight", sessionID)` and publish nothing. Supersession is reachable only after the claim's own entry was removed (the §7.1 reclaim race `rollbackUnconfirmedStart` handles), because takeover and cancel both require that no other started slot remains.
   3. When no platform server is bound (`s.mcpCancel == nil`), call `startPlatformMCP(claim.mcpNonce)`. Its `s.mu` section re-checks rule 6.2; on supersession it appends its own stop function to `s.mcpRetiring` and returns the same `Aborted` error. Otherwise it sets `s.mcpCancel` and moves `s.mcpPendingNonce` into `s.mcpArmedNonce`.
   4. For each connector in `in.connectors` (the session's permitted set) whose id is not in `s.connectorCancels`, open its server with `claim.mcpNonce`. Opening stays best-effort per connector and logs `connector_mcp_start_failed` as today. The insert into the map re-checks rule 6.2 the same way.
   5. Under `s.mu`, snapshot the served set: every `servedConnector` in the map, sorted by connector id.
   6. Call `writeSessionManifest` with `nonce = claim.mcpNonce` and `connectors` set to the snapshot. Because every publish runs under `mcpSurfaceMu`, the last rename always lists a superset of every earlier one.
7. *Cancel.* `cancelPodMCPIfRuntimeIdle` takes `mcpSurfaceMu`, drains `s.mcpRetiring`, and then cancels the live surface only when `runtimeIdleLocked()` holds and `anyStartedSlotLocked()` reports no started slot. `mcpArmingHeldLocked` is deleted. The releasing entry is deregistered before this call, as today.

**`writeSessionManifest`.** `manifestInputs` gains `nonce string`. A non-empty value is written as `mcpNonce`; an empty value makes `writeSessionManifest` mint a per-write nonce with `newMCPNonce`, which is rule 1's no-surface path. `manifestInputs.connectors` now means the connector servers the pod serves. The return value stays the nonce written, so existing callers in tests keep compiling.

**Call sites.** `StartSession` (`session.go`), `Resume` (`resume.go`), and `ConfigureWorkspace` (`sdkwarm.go`) replace their inline manifest-write, `startPlatformMCP`, and `startConnectorMCPServers` block with one `ensureAndPublishSessionMCP` call, keeping each site's existing release-on-error rollback and span categorization. The helper applies rule 1 uniformly, which gives `Resume` the `RuntimeKind` guard it lacks today. An `Aborted` from the helper is returned to the gateway unchanged. `startConnectorMCPServers` is deleted, and `startConnectorMCP` takes the connector and the nonce.

**`PodMCPArming`.** Keeps its contract: it returns `s.mcpSession` and `s.mcpArmedNonce`, the nonce the running servers authenticate. Rewrite its comment, because the manifest now names the armed nonce rather than whichever rename landed last.

**Existing tests rewritten in this commit.** `pkg/adapter/one_session_only_test.go` (the `claim.startMCP` assertion) and `pkg/adapter/bindattempt_test.go` (the `claim.startMCP` term) assert `claim.mcpNonce` and `s.mcpSession` instead. `pkg/adapter/podmcp_arming_internal_test.go`: `TestPodMCPArmingDeclinedOnCoTenantedPod_spec_15_4_3` becomes the reuse case of rule 4 (bob's claim returns alice's nonce and does not re-arm, and bob's rollback does not cancel the surface), and the other cases in the file move from `startMCP` to `mcpNonce`. `pkg/adapter/manifest_test.go`: the per-write regeneration check is rescoped to rule 1's no-surface path (inputs with an empty `nonce`).

Cite `// spec: §15.4.3; §4.7 (mcpNonce, connectorServers)` on the rules.

### CODE-5 · pkg/gateway/runtime/poolstore/poolstore.go and the acknowledgment's other carriers · admission message names the new item

Extend each carrier's list of acknowledged properties with the item name SPEC-6b lands, keeping the existing items and their order.

- `pkg/gateway/runtime/poolstore/poolstore.go`, the `ValidateSessionPolicy` error string: `"concurrent slots share the pod process namespace, /tmp, cgroup memory, network stack, " + "credential group-read access, and runtime-asserted session attribution on the intra-pod MCP surface (§5.2)"`. Update the matching doc-comment bullet above `ValidateSessionPolicy`.
- `pkg/gateway/runtime/runtimestore/runtimestore.go`, the `AcknowledgeProcessLevelIsolation` field comment.
- `pkg/gateway/externalapi/openapi/openapi.json`, the `acknowledgeProcessLevelIsolation` description.
- `pkg/ops/mcp/generated_tools.go`: regenerate with `go generate ./pkg/ops/mcp`. Do not hand-edit it.
- `tests/tier9_security/pool_admission_isolation_test.go`: the header comment's enumeration.
- `pkg/gateway/runtime/poolstore/poolstore_test.go`: add a `wantSub` of `"runtime-asserted session attribution"` to the unacknowledged-concurrency case.

Under fallback (b), every carrier uses "intra-pod MCP tool loss" in place of "runtime-asserted session attribution on the intra-pod MCP surface".

### CODE-6 · sdks/runtime/go/runtime/mcp.go, sdks/runtime/python/lenny_runtime/mcp.py, sdks/runtime/typescript/src/mcp.ts · SDK dial tolerates a refused connector

Each SDK's connect step separates a `tools/list` error response from a transport or `initialize` failure: Go returns a sentinel `errToolListRefused` wrapped in the connect error, Python raises a dedicated exception class, and TypeScript throws a dedicated error class.

- In `dialTools` (Go), the `connectorServers` loop in `mcp.py`, and `Tools.dial` (TypeScript), a connector whose connect-time `tools/list` returns an error response is closed, logged, and omitted from the tool set; the remaining connectors and the platform connection stay open. `Tools.connector(id)` (and its Python equivalent) reports the connector as absent, which is the existing answer for a connector the manifest did not advertise.
- A socket dial failure, an `initialize` failure, or a platform `tools/list` error still fails the whole set, as today.
- The SDKs keep dialing once from the startup manifest and name no session (SPEC-1f).

`// spec: §4.7 (connectorServers), §15.4.6 (connector MCP server reachability)`.

### CODE-7 · cmd/lenny-compliance/standard.go · the reachability check exercises a refused connector

- The fake connector server gains a per-instance option under which it answers `tools/list` with a JSON-RPC error after a successful `initialize`, and it records the connections it saw closed. Only `checkConnectorMCPReachability` sets the option, so the other checks' fixtures are unchanged.
- `checkConnectorMCPReachability` sets the option on the second fake connector. It passes when both connectors recorded a completed nonce-authenticated `initialize`, the runtime emitted its `response`, and neither the platform fake nor the first connector recorded a closed connection by the time that response was read.
- Update the check's detail string and comment to describe the refused connector. `cmd/lenny-ctl/runtimescaffold/probe.go` needs no change; it lists category names only.

## Staged schema, chart, and migration changes

### SCHEMA-1 · schemas/runtime-ops-events.schema.json, schemas/examples/runtime-ops.llm_request_completed.tokens.json, schemas/lenny-adapter.proto · the optional `sessionId`

- `llm_request_completed.properties` gains `"sessionId": { "type": "string", "minLength": 1 }`. It stays out of `required`, and `additionalProperties: false` stays. Append to the definition's `description`: "The runtime optionally names the session whose total the token counts join (§28.5.3 CH-RUNTIMEOPS)." `llm_request_started` is unchanged.
- The tokens example gains `"sessionId": "sess_01HX9F0YWXKK0V7QZ7G6P3R5JN"`. Leave `runtime-ops.llm_request_completed.json` without it, so the examples cover both the addressed and the unaddressed frame.
- `schemas/lenny-adapter.proto`: in the connector RPC comment that reads "calling pod's effective delegation policy (line 164)", change the phrase to "calling session's effective delegation policy (§9.3)", which also retires a line citation. Regenerate `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` with the repository's proto generation; do not hand-edit it.

## Staged docs changes

### DOCS-1 · docs/ runtime-author, reference, operator, and runbook pages, and the tier-11 nonce gate

The pages state the behavior without spec section numbers and link the relevant documentation page. Under fallback (b), apply only the `llm_request_completed` parts of items 5 and 6 and item 7, add the intra-pod MCP tool loss to the concurrency and acknowledgment pages in items 4 and 8, and leave the refusal text and the gate as they are.

1. **`docs/runtime-author-guide/platform-tools.md`, Connection Setup, and `docs/runtime-author-guide/integration-levels.md`, Standard section (including the Full-level manifest step).** State that the runtime reads `mcpNonce` and `connectorServers` from the manifest when it opens a session's MCP connections as well as at process start. Add the optional `_lennySessionId` step to the `initialize` params. Replace the refusal sentence with the two attribution rules and the recommended pattern: startup connections name no session, and a runtime that can be given more than one session opens an addressed connection set per session on that session's first message. State that `connectorServers` lists every connector the pod serves and that the gateway refuses a connector outside the session's policy, which the runtime treats as that connector being unavailable. Keep "pod-wide and started at most once per pod", the arming sentence, and the no-re-arm sentence each page carries today.
2. **`docs/runtime-author-guide/testing.md` and `docs/runtime-author-guide/local-development.md`, the "MCP nonce rejected" row.** Cause: the presented value was read before the servers were last armed. Fix: re-read the manifest before opening the connection. Keep "pod-wide and started at most once per pod" and "does not re-arm a running server". Add a "Tool call refused" row beside it: cause, an unaddressed connection on a process that has been given a second session, or a named session that is not bound on the pod; fix, open an addressed connection set per session.
3. **`docs/runtime-author-guide/runtime-sdk.md`.** State that an SDK runtime serves one session per process, dials its tools once from the startup manifest, and names no session, so on a concurrent pod whose sessions overlap its platform and connector tool calls are refused. State that the SDK omits a connector the gateway refuses at `tools/list` (CODE-6).
4. **`docs/runtime-author-guide/runtime-configuration.md` (acknowledgment paragraph), `docs/reference/execution-modes.md` (Concurrent preset row and acknowledgment text), `docs/operator-guide/security-principles.md`, and `docs/getting-started/concepts.md`.** Add runtime-asserted session attribution on the intra-pod MCP surface to the co-tenancy properties `acknowledgeProcessLevelIsolation` accepts, in the words SPEC-6b uses.
5. **`docs/reference/adapter-contract.md`.** In the Adapter Manifest lead, keep "one pod-global file", "authoritative for the session whose start last wrote it", and the three nonce rules the gate pins, and state that `mcpNonce` and `connectorServers` describe the pod's servers. Rewrite the `mcpNonce` and `connectorServers` rows to match SPEC-2b and SPEC-2c. In the `llm_request_completed` row, list `inputTokens`, `outputTokens`, and the optional `sessionId`.
6. **`docs/runtime-author-guide/lifecycle.md`.** In the `llm_request_completed` section, list `inputTokens`, `outputTokens`, and the optional `sessionId`, and state the attribution: a named, bound session receives the counts; otherwise they go to the process's single session, or are dropped when the process has been given more than one. In the concurrent section, state that a direct-mode runtime names the session on each frame.
7. **`docs/runbooks/token-usage-anomaly.md`.** Add to the `zero_delta` diagnosis that on a pool with `maxConcurrentSessions > 1`, `llm_request_completed` frames that omit `sessionId` while sessions overlap have their counts dropped, and that the fix is a runtime that names the session on each frame.
8. **`tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go`**, in the same commit as items 1 and 2:
   - Remove "resolves the calling session at call time" and the refusal sentence from the Connection Setup and Standard want lists, and add `_lennySessionId` to both.
   - Add "has been given exactly one session and that session is the caller" to `retiredNoncePhrasings`.
   - In the "MCP nonce rejected" row check, remove "stale manifest" and "cached the manifest too early" from the ban and add a want of "re-read the manifest".
   - Keep "manifest's current" and "current manifest" in the ban, and re-justify them: the servers validate the nonce that bound them, so a description keyed to the manifest's latest content reads wrong across a re-arm.
   - Add a spec site: `spec/15` label "§15.4.3 Session address paragraph", anchor "**Session address.** The nonce does not name a session.", want `_lennySessionId` and "since it last served none".
   - Relabel "spec/29 §29.4 platform MCP connect step" to §29.2.
   - Rewrite the file header, the `retiredNoncePhrasings` and `retiredManifestStabilityPhrasings` justifications, and the `// diagnosis:` comment so they no longer state that the manifest's latest nonce is rejected or that a later start replaces `mcpNonce`.

## Testing

Under fallback (b), only the `llm_request_completed` and schema cases of TEST-1, the resolver and token-sink cases of TEST-2, the `usagemeter_contention` case of TEST-4, and the admission-message case of TEST-5 land, the last asserting the fallback item name CODE-5 states.

Every test carries a `// spec:` annotation naming the sections it exercises, and every tier-2-and-higher test carries a `// diagnosis:` comment. Each new test file is added to `tests/spec-map.json` under the sections it cites, which `lenny-test validate-maps` checks.

### TEST-1 · Tiers 0 and 3 · wire contracts

- `tests/tier0_static/schemas_test.go`: an `llm_request_completed` frame carrying a string `sessionId` validates; a numeric or empty `sessionId` fails; the updated tokens example validates. `// spec: 28.5.3 (CH-RUNTIMEOPS llm_request_completed)`.
- `tests/tier3_contract/adapter_jsonl/mcp_nonce_wire_test.go`: over a real socket, an `initialize` carrying `_lennySessionId` reaches the MCP server with neither Lenny field in `params`, and the provider observes the declared value through `CallerSession`; an `initialize` whose `_lennySessionId` is `""` or `42` is closed with no initialize response. `// spec: 15.4.3 (nonce wire format, session address)`.
- `tests/tier3_contract/lifecycle_tokens/tokens_wire_test.go`: a frame naming a bound session folds under that session through the wired sink; a frame naming an unbound session folds nothing and still decrements the in-flight counter. `// spec: 28.5.3 (CH-RUNTIMEOPS), 11.2 (direct mode)`.

### TEST-2 · Tier 1 · adapter and Go SDK units

- `pkg/adapter/mcp/nonce_test.go`, `server_test.go`: one case per CODE-1 outcome (present and stripped, absent, empty, non-string).
- Resolver, one case per rule of §15.4.3 **Session address.**: a declared bound session resolves to itself; a declared session never held, registered but unbound, or already released returns `FailedPrecondition`; an undeclared request on a cohort-1 process resolves to the sole session. The discriminating case is undeclared after alice and bob both started and alice released: bob is the only live session and the request is still refused, because the process has not served none since.
- Providers: with a recording forwarder, an addressed `List` and `Call` on the platform and connector providers forward under the declared session; a refused request records no forwarder call.
- Token sink (replaces `TestSessionTokenSinkResolvesCurrentSession_spec_4_7` in `usage_test.go`): named bound folds; named unbound drops; unnamed sole folds; unnamed overlapped drops. In `runtimeops_test.go`, a dropped fold still decrements the provider's in-flight counter to zero.
- Pod surface, one case per CODE-4 rule: rule 1 for each no-surface configuration (`ManifestDir` empty publishes nothing and binds nothing; `type: mcp` publishes a per-write nonce and binds nothing; `MCPSocket` empty binds nothing). Rule 2 on a pod whose second entry was created by `AssignCredentials` before the first start, which is the discriminating case for the predicate change. Rule 3 mints a fresh nonce and retires the stale surface before the rebind. Rule 4 returns the same nonce to a co-tenant. Rule 6.2 returns `Aborted` and leaves the manifest file unchanged. Rule 6.3 binds for a co-tenant after the arming start rolled back before binding. Rule 6.4 opens only the connector the second start adds, and the second manifest lists both. Rule 7 does not cancel while a co-tenant's slot is started. Two manifests published under one armed surface carry the same nonce, and a manifest after a takeover carries a different one. `TestPodMCPArmingReportsTheLiveArming_spec_15_4_3` keeps passing.
- `sdks/runtime/go/runtime/mcp_test.go`: `dialTools` omits a connector whose `tools/list` errors and keeps the platform client; a platform `tools/list` error fails the set.
- `tests/claim-map.json`: update the surface of the "Platform and connector MCP sockets" row to cite `platformmcp.go`, `connectormcp.go`, and `ensureAndPublishSessionMCP` in `manifest.go` by symbol.

### TEST-3 · Tier 4 · principal resolution

`tests/tier4_integration/intra_pod_mcp_session_address_test.go` composes an in-process `adapter.Server` with the real gateway platform-tool service in the manner of `concurrent_delegation_proxy_test.go`, starts alice and bob of different users on one adapter, and asserts that each one's addressed call runs under its own principal. If that composition cannot be built without a live gateway link, drop this test and record in the deviations file that the TEST-5 forwarded-identifier assertion and the existing `leasecontrol` and `platformtools` tests carry the principal property. `// spec: 15.4.3 (session address), 9.1`.

### TEST-4 · Tier 7a · concurrency under `-race`

- `tests/tier7a_load_local/podmcp_once_per_pod_start_race_test.go`: concurrent starts on a pod whose entries were pre-registered bind the platform server exactly once, every published manifest carries one nonce, and the final manifest's `connectorServers` contains every start's permitted connectors.
- `tests/tier7a_load_local/podmcp_arming_handoff_test.go`: the arming start's rollback racing a co-tenant's start leaves a bound surface whose nonce the co-tenant's manifest carries.
- A new addressed-release race beside `sole_session_concurrent_release_test.go`: an addressed call whose bound check begins after the declared session's release has returned is refused, and no call is ever forwarded under a session other than the one it declared. Do not assert that an admitted call is never forwarded after the release returns; CODE-2 does not provide that.
- `tests/tier7a_load_local/usagemeter_contention`: interleaved `llm_request_completed` frames for alice and bob, each naming its session, produce exact per-session sums.

### TEST-5 · Tier 9 · attribution boundaries

In `tests/tier9_security/adapter_shared_mcp_surface_test.go`:

- Keep `TestSharedPlatformMCPRefusesWhenItCannotNameTheCaller_spec_9_1` for the unaddressed case.
- A connection naming a session not bound on this pod (never held, or already released) is refused and records no forwarder call.
- The accepted exposure: on a pod holding alice and bob, a connection naming bob is forwarded under bob regardless of which process dialled. Its `// diagnosis:` ties the case to the §5.2 runtime-asserted-attribution acknowledgment, so a change that narrows or widens the exposure fails here.
- An addressed connector call is forwarded under the addressed session's identifier, as the recording forwarder captures it. The gateway's refusal of an unpermitted connector is covered by `connectorauthz_test.go`, `connectortools_test.go`, and `tests/tier4_integration/external_mcp_tool_test.go`, and is not re-tested here.
- A connection whose `_lennySessionId` is invalid is closed before any dispatch.

`tests/tier9_security/pool_admission_isolation_test.go`: the unacknowledged concurrent pool's rejection names the new acknowledgment item.

## Edge cases and accepted failure modes

- **A stale surface stays up until the next `mcpSurfaceMu` holder.** A takeover parks the retired servers in `mcpRetiring`, and they serve the retired nonce until the claimant or the next helper, claim, or cancel drains them. The claimant drains immediately after its critical section (CODE-4 rule 5), so the window is the claim's own latency.
- **Accepted connections survive a cancel or takeover.** `ServeConn` runs on `context.Background()`, and closing a listener does not close accepted connections. This predates the proposal and is recorded under the summary's defects.
- **An SDK runtime whose sessions overlap loses its tools.** The first-party SDKs name no session, so rule 2 refuses them while the process has been given more than one session. They are refused and never misattributed.
- **A direct-mode drop is logged once per frame.** An unaddressed direct-mode runtime on an overlapped pod produces one `direct_mode_token_fold_dropped` warning per LLM call.

## Files touched on application (non-spec)

- `pkg/adapter/mcp/nonce.go`, `pkg/adapter/mcp/server.go`, `pkg/adapter/mcp/nonce_test.go`, `pkg/adapter/mcp/server_test.go` (CODE-1, TEST-2)
- `pkg/adapter/runtimegeneration.go`, `pkg/adapter/runtimeops.go`, `pkg/adapter/usage.go`, `pkg/adapter/adapterevents.go`, `cmd/lenny-adapter/main.go`, `pkg/adapter/runtimeops_test.go`, `pkg/adapter/usage_test.go` (CODE-2, TEST-2)
- `pkg/adapter/platformtoolprovider.go`, `pkg/adapter/connectortoolprovider.go`, `pkg/adapter/holdstate_test.go`, `pkg/gateway/connectors/connectorauthz/connectorauthz.go`, `pkg/gateway/connectors/connectorinvoke/invoker.go`, `tests/tier4_integration/connector_delegation_scope_test.go` (CODE-3)
- `pkg/adapter/slotsession.go`, `pkg/adapter/server.go`, `pkg/adapter/platformmcp.go`, `pkg/adapter/connectormcp.go`, `pkg/adapter/manifest.go`, `pkg/adapter/session.go`, `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go`, `pkg/adapter/one_session_only_test.go`, `pkg/adapter/bindattempt_test.go`, `pkg/adapter/podmcp_arming_internal_test.go`, `pkg/adapter/manifest_test.go` (CODE-4, TEST-2)
- `pkg/gateway/runtime/poolstore/poolstore.go`, `pkg/gateway/runtime/poolstore/poolstore_test.go`, `pkg/gateway/runtime/runtimestore/runtimestore.go`, `pkg/gateway/externalapi/openapi/openapi.json`, `pkg/ops/mcp/generated_tools.go` (regenerated), `tests/tier9_security/pool_admission_isolation_test.go` (CODE-5, TEST-5)
- `sdks/runtime/go/runtime/mcp.go`, `sdks/runtime/go/runtime/mcp_test.go`, `sdks/runtime/python/lenny_runtime/mcp.py`, `sdks/runtime/typescript/src/mcp.ts` (CODE-6, TEST-2)
- `cmd/lenny-compliance/standard.go` (CODE-7)
- `schemas/runtime-ops-events.schema.json`, `schemas/examples/runtime-ops.llm_request_completed.tokens.json`, `schemas/lenny-adapter.proto`, `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go` (regenerated) (SCHEMA-1)
- `docs/runtime-author-guide/platform-tools.md`, `integration-levels.md`, `testing.md`, `local-development.md`, `runtime-sdk.md`, `runtime-configuration.md`, `lifecycle.md`; `docs/reference/adapter-contract.md`, `execution-modes.md`; `docs/operator-guide/security-principles.md`; `docs/getting-started/concepts.md`; `docs/runbooks/token-usage-anomaly.md`; `tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go` (DOCS-1)
- `tests/tier0_static/schemas_test.go`, `tests/tier3_contract/adapter_jsonl/mcp_nonce_wire_test.go`, `tests/tier3_contract/lifecycle_tokens/tokens_wire_test.go` (TEST-1)
- `tests/tier4_integration/intra_pod_mcp_session_address_test.go` (TEST-3)
- `tests/tier7a_load_local/podmcp_once_per_pod_start_race_test.go`, `podmcp_arming_handoff_test.go`, a new addressed-release race file, `usagemeter_contention` (TEST-4)
- `tests/tier9_security/adapter_shared_mcp_surface_test.go` (TEST-5)
- `tests/claim-map.json`, `tests/spec-map.json` (TEST-2 and every TEST deliverable)
