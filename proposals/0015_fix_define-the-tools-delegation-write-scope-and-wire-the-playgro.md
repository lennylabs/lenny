# Proposal: Gate the playground's delegation-policy affordance on the minted bearer's session scope and fix its create payload

- **Status:** Approved (2026-06-21). Verified (2026-06-20); converged after 4 adversarial review rounds (5 findings fixed); signed off by the user for implementation, not yet implemented. The "Resolved in adversarial review" section below records each round.
- **Date:** 2026-06-20.
- **Scope:** Makes the §27.4 playground session-configuration delegation-policy affordance functional and correctly gated. §27.4 item 2 specifies "delegation policy selection (if caller has the scope)" (`spec/27_web-playground.md:177`), but the affordance cannot honor that gate today: the SPA has no source of caller scope, it sends a create-payload key the server never decodes, and the §27.4 gate names no concrete scope. The settled, user-confirmed direction keeps the affordance and makes it work. The fix keys the field's visibility on the minted playground bearer's effective scope granting `tools:sessions:write` (an explicit `tools:sessions:write`, or the `tools:sessions:*` ceiling capability the bearer carries, or `tools:*`, all of which subsume it; `spec/25_agent-operability.md:110`; `pkg/gateway/playground/token.go:32`); surfaces the minted bearer's effective scope to the SPA through the spec-defined `POST /v1/playground/token` mint response so the field renders conditionally; and fixes the SPA wire-key so the selection reaches the existing `delegationLease.delegationPolicyRef` server field. The gate is a client-side UI-visibility check; the session surface does not gate on scope (`spec/10_gateway-internals.md:238,250`), so session creation and its `delegationLease` block remain authorized by the gateway's standard role and tenancy admission on `POST /v1/sessions`. This proposal stages one spec edit (§27.4 item 2 gate wording), two spec edits to the mint-response body (§27.3.1 and its §15.1 endpoint-table duplicate), and the code changes to `pkg/gateway/playground` that surface the scope and fix the payload. It introduces no new scope domain, no new ceiling entry, no new endpoint, and no in-handler per-field scope check. The change is confined to the playground package; no client-facing protocol, RPC, frame, or business-logic split is added.

This document stages the proposed spec and code changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

§27.4 item 2 specifies that the playground's session-configuration screen exposes "delegation policy selection (if caller has the scope)" (`spec/27_web-playground.md:177`). The visibility gate cannot be honored as written, and the affordance is non-functional even when filled in.

### 1.1 The SPA has no source of caller scope, so the visibility gate cannot be applied

The §27.4 gate requires the SPA to know whether the caller holds the relevant scope, but the SPA fetches no scope anywhere. The `/playground/config.json` `uiConfig` carries `authMode`, `allowedRuntimes`, `maxSessionMinutes`, `banner`, `bannerSeverity`, and `wsPath`, but no scope (`pkg/gateway/playground/assets.go:82-89`). The mint-response body carries `bearerToken`, `tokenType`, `expiresInSeconds`, `reusable`, and `issuedAt`, but no scope (`pkg/gateway/playground/token.go:42-48`; the spec body at `spec/27_web-playground.md:114-122`). The SPA fetches no `/v1/me`- or `/v1/scopes`-style endpoint. As a result the delegation field is built unconditionally at `pkg/gateway/playground/ui/app.js:312` and appended unconditionally at `app.js:323-324`, contradicting the "if caller has the scope" gate.

### 1.2 The create payload sends a key the server never decodes

The SPA sends a flat top-level `delegationPolicyId` key (`pkg/gateway/playground/ui/app.js:345`) that `CreateSessionRequest` never decodes. The server reads the delegation-policy reference only from the nested `delegationLease.delegationPolicyRef` field (`pkg/gateway/sessionserver/sessionserver.go:1972` decodes `DelegationLease`; `pkg/gateway/sessionstore/sessionstore.go:544-547` defines `DelegationLeaseRequest{MaxDepth, MaxChildrenTotal, DelegationPolicyRef}`). The create decoder uses a plain `json.Decoder` with no `DisallowUnknownFields`, so the unknown `delegationPolicyId` key is silently dropped and the selection never reaches the server even when the user fills it in.

### 1.3 The §27.4 gate names no concrete scope

The gate text "if caller has the scope" (`spec/27_web-playground.md:177`) names no scope value, so neither a UI author nor a reviewer can determine which scope governs the affordance. The session surface itself does not gate on scope: `POST /v1/sessions` declares no `x-lenny-scope` (`spec/15_external-api-surface.md:919` makes scope enforcement the admin-API surface), the create handler runs a plain JSON decode with no scope check (`pkg/gateway/sessionserver/sessionserver.go:2259-2268`), and a playground bearer minted with an empty `scope` claim is still usable for the WebSocket session ("constrained to scope-agnostic endpoints only. This is not an error path." at `spec/10_gateway-internals.md:250`; "the caller is limited by the usual role ceiling" at `spec/10_gateway-internals.md:238`). The §27.4 gate is therefore a client-side UI-visibility check rather than an enforced authorization boundary. The delegation-policy reference is one outer field of the session-create envelope (`spec/14_workspace-plan-schema.md:311` lists `delegationLease` among the `CreateSessionRequest` outer sibling fields alongside `pool`, `isolationProfile`, `env`, and `runtimeOptions`; `spec/08_recursive-delegation.md:83` establishes `delegationLease` as a client-supplied field at session creation). The playground ceiling already pins `tools:sessions:*` (`spec/25_agent-operability.md:110`; `pkg/gateway/playground/token.go:32`), so the SPA can key the field's visibility on whether the minted bearer's effective scope grants `tools:sessions:write`, satisfied by an explicit `tools:sessions:write` or by the `tools:sessions:*` or `tools:*` wildcard that subsumes it. Wiring the affordance needs no new scope and no ceiling edit.

### 1.4 The settled direction keeps the affordance and makes it work

Removing the field is out of scope. §27.1 names "give security reviewers a UI surface to exercise the policy/audit pipeline end-to-end" as a playground purpose (`spec/27_web-playground.md:15`), and §8.3 makes `DelegationPolicy` a first-class resource whose policy engine, content interceptors, and audit events fire on delegation. Selecting a policy at session create maps to the existing `delegationLease.delegationPolicyRef` field, whose server-side decode, validate, and store path already exists and is round-trip tested (`TestCreateEchoesPoolAndDelegationLease_spec_14` at `pkg/gateway/sessionserver/envelope_test.go:354,370`). The fix surfaces the caller's effective scope to the SPA so the field renders conditionally, names the concrete gate scope in §27.4, and fixes the wire-key defect.

## 2. Decisions

- **Keep the delegation-policy selection affordance.** Removing the field is out of scope: §27.1 names exercising the policy/audit pipeline end-to-end as a playground purpose (`spec/27_web-playground.md:15`), and §8.3 makes `DelegationPolicy` a first-class resource that a reviewer needs to bind a session to from the chat pane. This proposal makes the affordance functional and correctly gated.
- **Gate the affordance's visibility on the minted bearer's effective scope granting the `tools:sessions:write` capability.** The gated capability is selection of `delegationLease.delegationPolicyRef`, an outer field of the session-create envelope (`spec/14_workspace-plan-schema.md:311`; `spec/08_recursive-delegation.md:83`). The playground ceiling pins `tools:sessions:*` at all three sites (`spec/25_agent-operability.md:110`, `spec/10_gateway-internals.md` §10.2 mint invariant, `pkg/gateway/playground/token.go:32`), so a dev-mode bearer carries the literal `tools:sessions:*`, while an `oidc`/`apiKey` bearer carries the intersection of the subject scope and the ceiling, which for a subject scoped to `tools:sessions:write` is the literal `tools:sessions:write` (`Set.Intersect` keeps the narrower operand; `pkg/common/scopes/scopes.go:258-266`). The SPA therefore reads the minted bearer's effective scope and renders the field when that scope grants `tools:sessions:write`, satisfied by an explicit `tools:sessions:write` or by a `tools:sessions:*` or `tools:*` wildcard that subsumes it. The gate is a client-side UI-visibility check; the session surface does not gate on scope (`spec/10_gateway-internals.md:238,250`; `POST /v1/sessions` declares no `x-lenny-scope` per `spec/15_external-api-surface.md:919`), so the gate carries no server-side authorization weight. This matches the existing scope-conditional playground affordance, the runtime picker (§27.4 item 1), which keys its visibility on the `runtimes` ceiling entry. No new scope domain, no taxonomy edit, and no ceiling edit are introduced.
- **Surface the caller's effective scope through the spec-defined mint response.** The effective scope is computed at the mint, where the caller's identity is resolved in all three auth modes: `narrowed := intersectScope(subject.Scope, playgroundAllowedScope)` (`pkg/gateway/playground/token.go:258`), set on the JWT (`token.go:274`). The mint response `POST /v1/playground/token` is a spec-defined body (`spec/27_web-playground.md:114-122`). Adding the effective scope there carries the same narrowed value the bearer holds and works in all three modes. The `config.json` surface cannot carry the scope: its handler discards the request (`handleConfigJSON(w http.ResponseWriter, _ *http.Request)` at `pkg/gateway/playground/assets.go:95`), the `/playground/*` routes carry no auth middleware, and in `apiKey` mode the SPA fetches `config.json` on page load before any auth material exists. `config.json` is also not a spec-defined endpoint, so documenting it as a scope carrier would add spec surface for an endpoint the spec does not define.
- **Store the effective scope client-side from the mint response and gate the field on it.** A bearer is already minted by the time the delegation field renders: `renderSessionConfig` (`app.js:262`) is reached only via the runtime picker's "use this runtime" button (`app.js:201-203`), which is shown after `fetchRuntimes` runs `mintBearer` (`app.js:217`). The SPA stores the effective scope alongside `state.bearer` at the mint (`app.js:124-125`) and gates the delegation field on `tools:sessions:write` membership (matched by `tools:sessions:*` or `tools:*`).
- **Fix the SPA wire-key with no spec gate.** Change `app.js:341-346` to send `delegationLease.delegationPolicyRef` instead of the flat top-level `delegationPolicyId`, so the selection reaches the existing server field (`sessionserver.go:1972` → `sessionstore.go:544-547`). This is a bug fix against the existing §14 field; it needs no spec change.
- **Add no in-handler per-field scope check.** The platform's scope-enforcement model is endpoint-level and MCP-tool-level (`spec/25_agent-operability.md:92-96` names admin-API middleware, MCP `tools/call` dispatch, and `/v1/admin/me/authorized-tools` as the enforcement points). `POST /v1/sessions` carries no per-field scope check today, and adding one for `delegationPolicyRef` alone would be the first in-handler per-body-field check, a parallel enforcement surface. The §27.4 gate is a client-side UI-visibility check, not an enforced authorization boundary: the session surface does not gate on scope (`spec/10_gateway-internals.md:238,250`), so a playground caller can create a session and supply a delegation lease regardless of the gate. Session creation, and the `delegationLease` block within it, is authorized by the gateway's standard role and tenancy admission on `POST /v1/sessions`, with the delegation-lease bounds validated server-side (`pkg/gateway/sessionserver/envelope.go:118-130`). This proposal does not introduce a server-side scope backstop for the field; doing so would require its own enforced spec and code change (a scope on `POST /v1/sessions` or a per-field check), which this proposal explicitly declines.

## 3. Proposed changes

### 3.1 Spec change: `spec/27_web-playground.md` §27.4 item 2 delegation-policy gate wording (line 177)

Anchor on item 2 of the §27.4 UI-surface list, currently at line 177. The current text reads:

```
2. **Session configuration.** A form generated from the runtime's `runtimeOptionsSchema` ([§14](14_workspace-plan-schema.md)) using the same JSON-Schema-to-form renderer the installer wizard uses ([§17.6](17_deployment-topology.md)). Also exposes: workspace plan upload (drag-drop tarball), delegation policy selection (if caller has the scope), and session labels.
```

The phrase "delegation policy selection (if caller has the scope)" names no concrete scope, so neither a UI author nor a reviewer can determine which scope governs the affordance. Make the gate a client-side UI-visibility check keyed on whether the minted playground bearer's effective scope grants `tools:sessions:write`, satisfied by an explicit `tools:sessions:write` or by the `tools:sessions:*` ceiling capability (admitted to the ceiling at `spec/25_agent-operability.md:110`) or `tools:*` that subsumes it, and state that session creation itself is authorized by the gateway's standard role and tenancy admission on `POST /v1/sessions` rather than by a scope check, because the session surface does not gate on scope (`spec/10_gateway-internals.md:238,250`; `spec/15_external-api-surface.md:919`, where scope enforcement is the admin-API surface and `POST /v1/sessions` declares no `x-lenny-scope`). Replace the item with:

```
2. **Session configuration.** A form generated from the runtime's `runtimeOptionsSchema` ([§14](14_workspace-plan-schema.md)) using the same JSON-Schema-to-form renderer the installer wizard uses ([§17.6](17_deployment-topology.md)). Also exposes: workspace plan upload (drag-drop tarball), delegation policy selection, and session labels. The delegation-policy field is a client-side visibility affordance shown only when the minted playground bearer's effective scope grants `tools:sessions:write`, satisfied by an explicit `tools:sessions:write` or by the wildcard `tools:sessions:*` or `tools:*` that subsumes it; it sets `delegationLease.delegationPolicyRef` on the `POST /v1/sessions` envelope ([§14](14_workspace-plan-schema.md), `CreateSessionRequest` outer fields). Session creation, and the `delegationLease` block within it, is authorized by the gateway's standard role and tenancy admission on `POST /v1/sessions`; the session surface does not gate on scope ([§10.2](10_gateway-internals.md#102-authentication)).
```

Notes for the applier:

- Do not introduce a new scope domain. The field keys on the minted bearer's effective scope granting `tools:sessions:write`, the capability the `tools:sessions:*` playground ceiling already admits (`spec/25_agent-operability.md:110`; `pkg/gateway/playground/token.go:32`). The SPA probes with `tools:sessions:write`, which an explicit `tools:sessions:write` satisfies and which the `tools:sessions:*` or `tools:*` wildcard matches.
- Leave §27.4 items 1 and 3 unchanged.

### 3.2 Spec change: `spec/27_web-playground.md` §27.3.1 mint-response body, add `effectiveScope` (lines 114-122)

Anchor on the `POST /v1/playground/token` success-response JSON block in §27.3.1, currently at lines 114-122. The current block reads:

```json
{
  "bearerToken": "<opaque or JWT-formatted bearer>",
  "tokenType": "Bearer",
  "expiresInSeconds": 900,
  "reusable": true,
  "issuedAt": "2026-04-19T12:34:56Z"
}
```

The mint already computes the bearer's effective scope as `intersection(subject.scope, playground_allowed_scope)` and sets it on the minted JWT's `scope` claim (`spec/27_web-playground.md:124`; `pkg/gateway/playground/token.go:258,274`). The SPA needs that effective scope to gate the §27.4 delegation-policy affordance, and the mint response is the point where the caller's identity is resolved in all three auth modes. Add an `effectiveScope` field carrying the same narrowed value, space-joined, mirroring the JWT `scope` claim. Replace the block with:

```json
{
  "bearerToken": "<opaque or JWT-formatted bearer>",
  "tokenType": "Bearer",
  "expiresInSeconds": 900,
  "reusable": true,
  "issuedAt": "2026-04-19T12:34:56Z",
  "effectiveScope": "tools:sessions:* tools:me:read tools:runtimes:read tools:pools:read tools:operations:read tools:events:read"
}
```

Add one sentence to the prose immediately after the block (currently the "Default `expiresInSeconds` is `900` ..." paragraph at `spec/27_web-playground.md:124`) documenting the field. Append:

```
The `effectiveScope` field carries the minted JWT's `scope` claim (the space-separated `intersection(subject.scope, playground_allowed_scope)` computed for this mint), so the SPA can gate scope-conditional affordances (such as the §27.4 delegation-policy selection) without decoding the bearer. It equals the `scope` claim on the returned `bearerToken`. When the intersection is empty the field is the empty string.
```

Notes for the applier:

- The value shown in the example is the dev-mode case, where the synthetic subject carries the full `playground_allowed_scope` (`pkg/gateway/playground/token.go:227`); in `oidc` and `apiKey` modes the value is narrower when the subject scope is narrower.
- Do not change the `expiresInSeconds` bounds or any other field in the §27.3.1 response contract.
- The same response body is enumerated a second time in the §15.1 REST endpoint table (`spec/15_external-api-surface.md:903`), which lists `{"bearerToken", "tokenType": "Bearer", "expiresInSeconds", "reusable", "issuedAt"}` without `effectiveScope`. Apply the matching `effectiveScope` addition there too (staged in §6) so the two spec surfaces and the `tokenResponse` struct agree.

### 3.3 Code change: `pkg/gateway/playground/token.go`, add `effectiveScope` to the mint response

Anchor on the `tokenResponse` struct (lines 42-48) and the `completeMint` response write (lines 311-317). The struct currently reads:

```go
// tokenResponse is the §27.3.1 POST /v1/playground/token success
// body.
type tokenResponse struct {
	BearerToken      string `json:"bearerToken"`
	TokenType        string `json:"tokenType"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`
	Reusable         bool   `json:"reusable"`
	IssuedAt         string `json:"issuedAt"`
}
```

Add the `EffectiveScope` field carrying the same `narrowed` value already computed at `token.go:258` and set on the minted JWT at `token.go:274`. The field is the §27.3.1 carrier the SPA reads to gate the §27.4 delegation-policy affordance. Replacement struct:

```go
// tokenResponse is the §27.3.1 POST /v1/playground/token success
// body. EffectiveScope mirrors the minted JWT's scope claim — the
// space-separated intersection(subject.scope, playground_allowed_scope)
// — so the SPA can gate scope-conditional §27.4 affordances (the
// delegation-policy selection) without decoding the bearer.
// spec: §27.3.1 mint response; §25.1 "Playground-allowed scope set".
type tokenResponse struct {
	BearerToken      string `json:"bearerToken"`
	TokenType        string `json:"tokenType"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`
	Reusable         bool   `json:"reusable"`
	IssuedAt         string `json:"issuedAt"`
	EffectiveScope   string `json:"effectiveScope"`
}
```

In `completeMint`, populate the field from the already-computed `narrowed` value. The response write at `token.go:311-317` becomes:

```go
	writeJSON(w, http.StatusOK, tokenResponse{
		BearerToken:      signed,
		TokenType:        "Bearer",
		ExpiresInSeconds: int64(h.cfg.BearerTTL / time.Second),
		Reusable:         true,
		IssuedAt:         now.Format(time.RFC3339),
		EffectiveScope:   narrowed,
	})
```

Notes for the applier:

- `narrowed` is the value computed at `token.go:258` (`narrowed := intersectScope(subject.Scope, playgroundAllowedScope)`) and set on the minted JWT at `token.go:274`, so the response field equals the bearer's `scope` claim by construction. Do not recompute it; reuse the existing variable in scope at the response write.
- An empty intersection yields the empty string (`intersectScope` returns `""`; see `token.go:366-388`); the JSON field is then `"effectiveScope": ""`, matching the §3.2 prose.
- Add a tier-1 test asserting the mint response carries `effectiveScope` equal to the minted JWT's `scope` claim. A subject holding `tools:sessions:write` yields an `effectiveScope` containing `tools:sessions:write`, because `Set.Intersect` keeps the narrower of two overlapping operands and the subject's `tools:sessions:write` lies inside the ceiling's `tools:sessions:*` (`pkg/common/scopes/scopes.go:258-266`; pinned by the existing `intersect_scope_test.go:16-19`, where a subject `tools:sessions:read` survives as `tools:sessions:read`, not the ceiling wildcard). A dev-mode subject, which carries the full `tools:sessions:*` ceiling literal (`pkg/gateway/playground/token.go:227`), yields an `effectiveScope` containing `tools:sessions:*`; cover both narrowing cases. An absent or empty subject scope is the §25.1 absent-claim case, where the claim does not restrict the caller below the ceiling: `Set.Intersect` returns the other operand when the subject set is not present (`pkg/common/scopes/scopes.go:251-252`), so `intersectScope("", playgroundAllowedScope)` returns the full ceiling string `tools:sessions:* tools:me:read tools:runtimes:read tools:pools:read tools:operations:read tools:events:read`, and the test asserts that value rather than the empty string. The `"effectiveScope": ""` outcome belongs to the present-but-disjoint subject scope case (for example a subject holding only `tools:credential:write`), where the intersection is empty and `intersectScope` returns `""` (pinned by `intersect_scope_test.go:29-33`); add that as a distinct case. Cite `// spec: §27.3.1` per the package convention.

### 3.4 Code change: `pkg/gateway/playground/ui/app.js`, store the effective scope from the mint response

Anchor on the `mintBearer` success branch (lines 124-127) where `state.bearer` is set. The current branch reads:

```js
        state.bearer = body.bearerToken;
        state.bearerExpiresAt = Date.now() + body.expiresInSeconds * 1000;
        authStatusEl.textContent = "session token active";
        return state.bearer;
```

Store the §27.3.1 `effectiveScope` field alongside the bearer so the §27.4 render can gate on it. Replacement:

```js
        state.bearer = body.bearerToken;
        state.bearerExpiresAt = Date.now() + body.expiresInSeconds * 1000;
        // §27.3.1: the mint response carries the bearer's effective
        // scope (intersection of the subject scope and the playground
        // ceiling). The §27.4 session-config screen gates the
        // delegation-policy field on it.
        state.effectiveScope = body.effectiveScope || "";
        authStatusEl.textContent = "session token active";
        return state.bearer;
```

Add `effectiveScope` to the initial `state` object (lines 21-30) so the field is declared before the first mint. Add the line `effectiveScope: "",` to the object literal, for example after `bearerExpiresAt: 0,`.

Notes for the applier:

- The state initializer at `app.js:21-30` currently has no `effectiveScope` key; add it so the gate at §3.5 reads a defined value before the first mint completes.
- Do not alter the cached-bearer early-return at `app.js:99-100`; the stored `effectiveScope` persists across cache hits because it is set only on a fresh mint and the cached bearer carries the same scope.

### 3.5 Code change: `pkg/gateway/playground/ui/app.js`, render the delegation field conditionally on the minted bearer's session scope

Anchor on the delegation-field build and append in `renderSessionConfig` (lines 312, 323-324). The field is built at:

```js
    var delegationField = el("input", { type: "text", placeholder: "delegation policy id (optional)" });
```

and appended unconditionally inside the config-card children at:

```js
      el("label", { text: "Delegation policy (optional, requires scope)" }),
      delegationField,
```

The unconditional append contradicts the §27.4 "if caller has the scope" gate. Gate the label and input on the caller's effective scope satisfying a `tools:sessions:write` probe (matched by the ceiling `tools:sessions:*` or `tools:*`), read from `state.effectiveScope` (populated by §3.4). The gate is a client-side visibility check; the gateway's standard role and tenancy admission on `POST /v1/sessions` authorizes session creation regardless. Add a scope-membership helper near the existing `globMatch` helper (`app.js:148-163`):

```js
  // §27.4 item 2: the delegation-policy field is a client-side
  // visibility affordance, gated on the minted bearer's effective scope
  // granting tools:sessions:write. The helper probes with
  // tools:sessions:write, which an explicit tools:sessions:write
  // satisfies and which the tools:sessions:* playground ceiling (the
  // §25.1 ceiling) or tools:* matches through the §25.1 wildcard-action
  // rule. It mirrors the gateway scopes.Set.Matches semantics: a scope
  // matches when its domain equals the target domain and its action is
  // the target action or `*`, and tools:* matches everything. The
  // session surface does not gate on scope; this hides the field rather
  // than enforcing access.
  function hasScope(target) {
    var claim = (state.effectiveScope || "").split(/\s+/);
    var want = target.split(":"); // ["tools","sessions","write"]
    for (var i = 0; i < claim.length; i++) {
      if (!claim[i]) continue;
      var have = claim[i].split(":");
      if (have.length !== 3) continue;
      if (have[0] !== want[0]) continue;
      if (have[1] === "*" && have[2] === "*") return true; // tools:*
      if (have[1] !== want[1]) continue;
      if (have[2] === "*" || have[2] === want[2]) return true;
    }
    return false;
  }
```

In `renderSessionConfig`, build the delegation label and input only when `hasScope("tools:sessions:write")` is true, and push them into the card children only in that case. Replace the unconditional `var delegationField = ...` build and the two unconditional child entries with a conditional build and a conditional push. One concrete form: compute `var canDelegate = hasScope("tools:sessions:write");` near the top of `renderSessionConfig`, build `delegationField` only when `canDelegate`, and replace the two card-children lines

```js
      el("label", { text: "Delegation policy (optional, requires scope)" }),
      delegationField,
```

with entries that are present only when `canDelegate` (for example by assembling the card children array conditionally, or by using the existing `el` children-skip behavior where a `null` child is dropped at `app.js:48`):

```js
      canDelegate ? el("label", { text: "Delegation policy (optional)" }) : null,
      canDelegate ? delegationField : null,
```

Notes for the applier:

- `el` already drops `null` children (`app.js:48`: `if (c == null) return;`), so emitting `null` for the label and input when `canDelegate` is false hides the affordance cleanly without restructuring the children array.
- When `canDelegate` is false, `delegationField` must not be referenced at create time. Guard the create-payload read at §3.6 on `canDelegate` (or on `delegationField` being defined) so an undefined field is never dereferenced.
- Drop the "(requires scope)" qualifier from the label wording since visibility is now gated; the field is shown only to callers who hold the scope.
- Add or extend a playground SPA test asserting the delegation label and input render when `state.effectiveScope` includes `tools:sessions:*` (or `tools:sessions:write`) and are absent otherwise.

### 3.6 Code change: `pkg/gateway/playground/ui/app.js`, fix the create-payload wire-key

Anchor on the create-session payload assembly in `renderSessionConfig` (lines 341-346). The current payload reads:

```js
            var payload = {
              runtimeRef: state.runtime.id || state.runtime.name,
              runtimeOptions: options,
              labels: parseLabels(labelsField.value),
              delegationPolicyId: delegationField.value.trim() || undefined,
            };
```

The flat top-level `delegationPolicyId` key is never decoded by `CreateSessionRequest` (`sessionserver.go:1972` decodes only `delegationLease`; the decoder has no `DisallowUnknownFields`, so the key is silently dropped). The server reads the policy reference from `delegationLease.delegationPolicyRef` (`sessionstore.go:544-547`). Send the nested field, and send it only when the field is present and non-empty. Replacement:

```js
            var payload = {
              runtimeRef: state.runtime.id || state.runtime.name,
              runtimeOptions: options,
              labels: parseLabels(labelsField.value),
            };
            // §27.4 item 2 → §14 CreateSessionRequest outer field. The
            // selection sets delegationLease.delegationPolicyRef, the
            // field the server decodes (sessionserver.go) and stores
            // (sessionstore.go DelegationLeaseRequest). The flat
            // delegationPolicyId key the SPA sent before was never
            // decoded and was silently dropped.
            if (canDelegate && delegationField && delegationField.value.trim()) {
              payload.delegationLease = { delegationPolicyRef: delegationField.value.trim() };
            }
```

Notes for the applier:

- `canDelegate` is the §3.5 visibility boolean; gating the payload write on it (and on `delegationField` being defined) prevents an undefined-field dereference when the affordance is hidden.
- Omit `delegationLease` entirely when no policy is selected, so an empty selection sends no lease block and the server applies its default lease resolution (`spec/08_recursive-delegation.md:83-89`).
- The server-side contract is `DelegationLeaseRequest{MaxDepth, MaxChildrenTotal, DelegationPolicyRef}` (`sessionstore.go:544-547`) and the §14 envelope example (`spec/14_workspace-plan-schema.md:75-79`); the playground UI sends only `delegationPolicyRef`.
- Extend the playground SPA or envelope round-trip coverage so a playground-shaped body populates `row.DelegationLeaseRequest.DelegationPolicyRef`. The server side is already covered by `TestCreateEchoesPoolAndDelegationLease_spec_14` (`pkg/gateway/sessionserver/envelope_test.go:354,370`); the new coverage asserts the SPA emits the nested key the server decodes.

## 4. Non-goals

- **No removal or redesign of the delegation-policy selection affordance.** The user-confirmed direction is to keep it; this proposal makes it functional and correctly gated.
- **No new `tools:delegation:write` scope domain.** A first draft proposed minting a new `delegation` domain and admitting `tools:delegation:write` into the §25.1/§10.2/code playground ceiling at three sites. It was dropped: the gated capability is one outer field of the session-create envelope (`spec/14_workspace-plan-schema.md:311`; `spec/08_recursive-delegation.md:83`), which the existing `tools:sessions:write` scope already governs and which `tools:sessions:*` already admits to the ceiling at all three sites (`spec/25_agent-operability.md:110`; the §10.2 mint invariant; `pkg/gateway/playground/token.go:32`). A new top-level scope domain for one field already covered by `tools:sessions:*` is a parallel surface the project's reuse principles reject. The new domain would have had exactly one consumer (the §8.3 delegation-policy admin endpoints declare no `x-lenny-scope` today), underscoring the redundancy.
- **No edit to the closed §15.1 / §25.1 scope taxonomy or the in-code `Domains` map.** Because no new domain is introduced, the closed taxonomy (`spec/15_external-api-surface.md` Scope taxonomy; `spec/25_agent-operability.md:79`) and the `Domains` map (`pkg/common/scopes/scopes.go:50-78`) are untouched.
- **No edit to the three-site playground-allowed-scope ceiling.** `tools:sessions:*` is already pinned at all three sites; no scope is added or removed.
- **No in-handler per-field scope check on `POST /v1/sessions`.** The platform's scope-enforcement model is endpoint-level and MCP-tool-level (`spec/25_agent-operability.md:92-96`). Adding an in-handler check on `delegationPolicyRef` alone would be the first per-body-field scope check and a parallel enforcement surface; the create path gates no other outer field (`env`, `pool`, `credentialPolicy`, `callbackUrl`) per-field today, and the session surface does not gate on scope at all (`spec/10_gateway-internals.md:238,250`). The §27.4 gate is a client-side UI-visibility check; session creation and the `delegationLease` block are authorized by the gateway's standard role and tenancy admission on `POST /v1/sessions`. Introducing a server-side scope backstop for the delegation field is a separate enforced change this proposal declines.
- **No new client-facing endpoint.** The effective scope is surfaced through the existing spec-defined `POST /v1/playground/token` mint response. No `/v1/me`, `/v1/scopes`, or `config.json` scope field is added; `config.json` cannot carry the scope because its route is unauthenticated and is fetched before auth material exists in `apiKey` mode.
- **No change to the §8.3 `DelegationPolicy` resource, its admin API, tag-matching, `contentPolicy`, or `scanExportedFiles` semantics.** The proposal only wires the playground's selection to the existing `delegationLease.delegationPolicyRef` field.
- **No change to the delegation-lease bounds validation (`maxDepth` / `maxChildrenTotal`) or the §8.3 `maxDelegationPolicy` session-level cap.** The playground UI sends only `delegationPolicyRef`.
- **No fix of the pre-existing §15.1 scope-taxonomy-vs-code `Domains`-map drift.** The code `Domains` map carries `sessions`, `runtimes`, `pools`, and `experiment` that the §15.1 canonical list omits. That is a separate defect; this proposal touches neither site and leaves the broader reconciliation to its own finding.
- **No alteration of the playground's other scope-conditional behavior (the runtime picker filtering by `allowedRuntimes` and caller scopes).** Only the delegation field's visibility and the create-payload key change.

## 5. Testing

- **Tier 0 (static):** `go build`, `go vet`, `golangci-lint`, and `gofumpt`/`goimports` on the touched `pkg/gateway/playground` files; confirm the edited §27.3.1 and §27.4 spec text renders and the intra-spec anchors resolve.
- **Tier 1 (unit):** a `pkg/gateway/playground` test asserts the mint response carries `effectiveScope` equal to the minted JWT's `scope` claim across the three auth modes. A subject holding `tools:sessions:write` yields an `effectiveScope` containing `tools:sessions:write` (the intersection keeps the narrower subject form inside the ceiling's `tools:sessions:*`), a dev-mode subject carrying the full `tools:sessions:*` ceiling yields an `effectiveScope` containing `tools:sessions:*`, an absent or empty subject scope yields the full ceiling string (the §25.1 absent-claim case: `Set.Intersect` returns the other operand when the subject is not present, so `intersectScope("", playgroundAllowedScope)` is the full ceiling), and a present-but-disjoint subject scope such as `tools:credential:write` yields `"effectiveScope": ""`. A playground SPA test (or the existing app.js test harness) asserts the delegation label and input render when `state.effectiveScope` includes `tools:sessions:write` (or the ceiling `tools:sessions:*`) and are absent otherwise, and that a filled selection produces a `delegationLease.delegationPolicyRef` payload key while an empty selection omits `delegationLease`.
- **Tier 3 (contract):** confirm the §27.3.1 `POST /v1/playground/token` response body now includes `effectiveScope` and that the SPA-emitted `POST /v1/sessions` body populates `DelegationLeaseRequest.DelegationPolicyRef`; the server side is already covered by `TestCreateEchoesPoolAndDelegationLease_spec_14` (`pkg/gateway/sessionserver/envelope_test.go:354,370`).
- **Tier 11 (docs):** confirm the edited §27.4 item 2 gate wording and the §27.3.1 mint-response body agree with each other, with §25.1's playground-allowed scope set, and with §14's `CreateSessionRequest` outer-field list on the `delegationLease.delegationPolicyRef` placement. Confirm the §15.1 `POST /v1/playground/token` Response enumeration (line 903) and the §27.3.1 mint-response body list the same fields, so both carry `effectiveScope` after the edit.
- **No new tier-2-or-higher behavioral test for a new authorization path is added, because no new authorization path is introduced.** Authorization for session creation, and the `delegationLease` block within it, rests on the gateway's existing role and tenancy admission on `POST /v1/sessions`; the §27.4 gate is a client-side visibility check.

## 6. Files touched on application

- `spec/27_web-playground.md`: §27.4 item 2 (line 177) reworded to frame the delegation-policy field as a client-side UI-visibility affordance keyed on the minted bearer's effective scope granting `tools:sessions:write` (satisfied by an explicit `tools:sessions:write` or by the `tools:sessions:*` or `tools:*` wildcard that subsumes it), state that session creation is authorized by the gateway's standard role and tenancy admission on `POST /v1/sessions` rather than a scope check, and name the `delegationLease.delegationPolicyRef` target field; §27.3.1 mint-response body (lines 114-122) given an `effectiveScope` field with one documenting sentence appended to the following prose.
- `spec/15_external-api-surface.md`: the `POST /v1/playground/token` endpoint-table Response enumeration (line 903) given the same `effectiveScope` field so it agrees with §27.3.1 and the `tokenResponse` struct. This entry is hand-written prose (the mint endpoint is not part of the build-time admin OpenAPI, which covers `/v1/admin/*` only), so the applier edits it directly. New value: `Response: {"bearerToken", "tokenType": "Bearer", "expiresInSeconds", "reusable", "issuedAt", "effectiveScope"}`.
- `pkg/gateway/playground/token.go`: `tokenResponse` (lines 42-48) gains `EffectiveScope string json:"effectiveScope"`; `completeMint` (lines 311-317) populates it from the existing `narrowed` value.
- `pkg/gateway/playground/ui/app.js`: the `state` initializer (lines 21-30) gains `effectiveScope`; `mintBearer` (lines 124-127) stores `body.effectiveScope`; `renderSessionConfig` gates the delegation label and input on a new `hasScope("tools:sessions:write")` helper (built near `globMatch` at lines 148-163) and sends `delegationLease.delegationPolicyRef` instead of the flat `delegationPolicyId` (lines 341-346).
- Test files under `pkg/gateway/playground` (and the SPA test harness) gain the §3.3, §3.5, and §3.6 unit and round-trip assertions.
- The §15.1 edit touches only the `POST /v1/playground/token` response enumeration, which is a browser-only playground endpoint rather than an admin-API tool (`spec/15_external-api-surface.md:896`). No scope-taxonomy block, `Domains` map, ceiling site, admin-API endpoint, schema, proto, chart, or `docs/` file is touched.

## 7. Resolved in adversarial review

Adversarial review rounds populate this section. The draft already incorporates the converged direction from the input sketch challenges: the new `tools:delegation:write` domain and its three-site ceiling admission (former C1, C2, C3) are dropped in favor of keying the field's visibility on the existing `tools:sessions:*` ceiling capability already minted into the playground bearer; the `config.json` scope carrier (former C4) is dropped in favor of the spec-defined `POST /v1/playground/token` mint response, which resolves caller identity in all three auth modes and avoids an unauthenticated route that lacks a principal and is fetched before auth material exists in `apiKey` mode; and the in-handler per-field scope check (former C7) is dropped because it would be the platform's first per-body-field check, a parallel enforcement surface.

### Pass 1 (2026-06-20, automated)

- **Corrected the prescribed tier-1 `effectiveScope` test expectation, which inverted the scope-narrowing direction.** §3.3 and §5 prescribed a test asserting that a subject holding `tools:sessions:write` yields an `effectiveScope` containing `tools:sessions:*`, with the rationale "the ceiling narrows `write` against the `*` allowance". `Set.Intersect` keeps the narrower of two overlapping operands, so intersecting a subject's `tools:sessions:write` against the ceiling's `tools:sessions:*` yields `tools:sessions:write` (`pkg/common/scopes/scopes.go:258-266`; pinned by `pkg/gateway/playground/intersect_scope_test.go:16-19`, where a subject `tools:sessions:read` survives as `tools:sessions:read`, not the ceiling wildcard). A test written as prescribed would fail against the reused implementation, and "fixing" the intersection to match would break the §10.2 invariant that the minted scope is the intersection and never the union. Reworded both sites to expect `tools:sessions:write` for the narrower subject, retained the dev-mode full-ceiling case (which legitimately yields `tools:sessions:*`) as a second case, and dropped the inverted parenthetical.
- **Removed the assertion of a non-existent server-side session-create scope backstop in the staged §27.4 text and its rationale.** §3.1, §1.3, §2, and §4 named `tools:sessions:write` "the session-create scope" with "the gateway's standard session-create authorization" as a "server-side backstop". No scope gates `POST /v1/sessions`: the endpoint declares no `x-lenny-scope` (`spec/15_external-api-surface.md:919`), the create handler is a plain JSON decode with no scope check (`pkg/gateway/sessionserver/sessionserver.go:2259-2268`), and a playground bearer minted with an empty `scope` claim is still usable for the session ("constrained to scope-agnostic endpoints only. This is not an error path." at `spec/10_gateway-internals.md:250`; "the caller is limited by the usual role ceiling" at `:238`). Reframed the §27.4 gate as a client-side UI-visibility check keyed on the `tools:sessions:*` ceiling capability the minted bearer already carries, stated plainly that session creation and its `delegationLease` block are authorized by the gateway's standard role and tenancy admission rather than a scope check, and propagated the correction to the §1.3, §2, §4, §5, and §6 rationale prose and the §5 Scope summary. A real server-side gate would require its own enforced change, which the proposal declines.
- **Added the missed §15.1 edit site that independently enumerates the mint-response body.** The §15.1 REST endpoint table duplicates the `POST /v1/playground/token` response body (`spec/15_external-api-surface.md:903`: `{"bearerToken", "tokenType": "Bearer", "expiresInSeconds", "reusable", "issuedAt"}`), so after the §27.3.1 and `tokenResponse` edits added `effectiveScope`, §15.1 would have disagreed with §27.3.1 and the code. The entry is hand-written prose (the mint endpoint is browser-only and outside the build-time admin OpenAPI), so it is a direct edit site. Added the matching `effectiveScope` addition to §6, the §3.2 applier notes, the §5 Scope summary count, and a tier-11 check that the §15.1 and §27.3.1 response enumerations agree.

### Pass 2 (2026-06-20, automated)

- **Corrected the prescribed empty-subject-scope test expectation, which conflated an empty subject scope with an empty intersection.** §3.3 (the §3.3 applier note) and §5 prescribed a tier-1 case asserting that "a subject with an empty scope yields `"effectiveScope": ""`". An absent or empty subject scope is the §25.1 absent-claim case: `Set.Intersect` returns the other operand when the subject set is not present (`pkg/common/scopes/scopes.go:251-252`, `case !s.Present(): return other`), so `narrowed.Scopes()` is non-empty and `intersectScope` returns the full ceiling string `tools:sessions:* tools:me:read tools:runtimes:read tools:pools:read tools:operations:read tools:events:read` (`pkg/gateway/playground/token.go:384-387`), verified by executing the reused implementation. The `"effectiveScope": ""` outcome arises only from a present-but-disjoint subject scope (for example `tools:credential:write`), which produces the empty-intersection sentinel `intersectScope` translates to `""` (pinned by `intersect_scope_test.go:29-33`). A test written as the proposal prescribed for the empty-subject case would fail against the reused implementation, the same class of defect Pass 1 fixed for the inverted narrowing direction. Reworded both sites so the empty-subject case asserts the full ceiling string and the empty-string outcome is a distinct present-but-disjoint case. The §3.2 spec prose and the §3.3 note about the empty-intersection case were already correct and were left unchanged.
- **Aligned the staged §27.4 spec text and the §2 visibility-gate decision with the implemented `tools:sessions:write` probe, removing a predicate drift between the spec text and the code, helper, and tests.** The staged §27.4 text (the §3.1 replacement) and the §2 decision gated the field on the effective scope "carrying the `tools:sessions:*` ceiling capability" or "the `tools:sessions:*` entry being present", while the §3.5 helper, the §3.3/§5 tests, and the §2 follow-on decision gate on a `tools:sessions:write` probe. The two predicates differ for the common `oidc`/`apiKey` case: a subject scoped to the narrower `tools:sessions:write` yields effective scope `tools:sessions:write`, never the literal `tools:sessions:*` token, because `Set.Intersect` keeps the narrower operand (`pkg/common/scopes/scopes.go:258-266`; pinned by `intersect_scope_test.go:16-19`), verified by executing the implementation. Applying the proposal as written would leave the §27.4 spec describing a strictly narrower gate than the code enforces. Reworded the staged §27.4 text and the §2 decision to gate on the effective scope granting `tools:sessions:write` (satisfied by an explicit `tools:sessions:write`, or by the `tools:sessions:*` or `tools:*` wildcard that subsumes it), and propagated the same predicate to the top-level Scope bullet, the §1.3 prose, the §3.1 prose and applier note, the §3.5 helper comment, and the §6 files-touched entry so spec, code, helper, and tests state one predicate.

## 8. Open decisions for review

- **Wildcard breadth of the SPA `hasScope` helper.** The §3.5 helper treats `tools:sessions:*` and `tools:*` as matching the `tools:sessions:write` probe, mirroring the gateway `scopes.Set.Matches` semantics. A reviewer should confirm the SPA need not handle any further wildcard form the gateway admits; the helper is a client-side visibility gate, and the gateway's standard role and tenancy admission on `POST /v1/sessions` remains the authoritative authorization for session creation and the `delegationLease` block (the session surface does not gate on scope per `spec/10_gateway-internals.md:238,250`).
- **Whether to publish `effectiveScope` as a general SPA affordance-gating field or scope it to the delegation case.** This proposal documents `effectiveScope` as a general carrier the SPA may use for any scope-conditional affordance (§3.2 prose), which matches the platform's existing `/v1/admin/me` `authorization.scope` echo. A reviewer may prefer to narrow the documentation to the delegation case only; the field's value is unchanged either way.
