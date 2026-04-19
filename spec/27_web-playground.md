## 27. Web Playground

This section specifies the bundled web playground — a minimal browser UI served by the gateway for exploring a Lenny installation.

The playground is intentionally scoped for exploration and quick demos; production-grade chat UIs are out of scope and expected to be built on the client SDKs ([§15.6](15_external-api-surface.md#156-client-sdks)).

---

### 27.1 Purpose and non-goals

**Purpose:**

- Give new operators a zero-install way to create a session against any registered runtime and exchange messages.
- Give runtime authors a smoke-test UI after `lenny-ctl runtime publish` ([§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding)) without wiring a client app.
- Give security reviewers a UI surface to exercise the policy/audit pipeline end-to-end.

**Non-goals:**

- Not a general-purpose chat UI. No conversation history management beyond a single session, no workspaces, no multi-user features, no saved prompts.
- Not a replacement for the admin CLI. The playground does not expose installation administration; operators continue to use `lenny-ctl` ([§24](24_lenny-ctl-command-reference.md)).
- No offline mode. The playground requires a live gateway.

---

### 27.2 Placement and gating

The playground is served by the **gateway** (not `lenny-ops`) at `/playground` on the same Ingress as the MCP and REST endpoints. It is compiled into the gateway binary as an embedded static asset bundle (`embed.FS`) so there is no separate deployment target.

**Feature-flag gating:**

| Helm value | Default | Effect |
|------------|---------|--------|
| `playground.enabled` | `false` | When `false`, `/playground/*` returns `404` and the asset bundle is unmounted |
| `playground.authMode` | `oidc` | Auth mode for the playground UI; one of `oidc`, `apiKey`, `dev` |
| `playground.allowedRuntimes` | `["*"]` | Glob list of runtime IDs visible in the playground runtime picker |
| `playground.maxSessionMinutes` | `30` | Hard cap on playground-initiated session duration |
| `playground.maxIdleTimeSeconds` | `300` | Hard override of the runtime's `maxIdleTimeSeconds` for playground-initiated sessions (bounded `60 ≤ v ≤ runtime's maxIdleTimeSeconds`). See [§27.6](#276-session-lifecycle-and-cleanup). |
| `playground.sessionLabels` | `{origin: "playground"}` | Labels applied to playground sessions for audit/accounting |

Default is `false` because the playground surface area is not something every installation wants live. `lenny up` (Tier 0, [§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)) sets `playground.enabled=true` and `playground.authMode=dev`; the installer wizard asks explicitly for production installs.

---

### 27.3 Authentication

The playground never bypasses gateway auth. A user hitting `/playground` is routed through the gateway's auth chain identical to API traffic.

- `playground.authMode=oidc`: the gateway redirects unauthenticated users to the configured OIDC provider. On successful OIDC token exchange the gateway establishes an opaque server-side session record (the raw ID token is never placed in a cookie — see [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange)) and sets a single session cookie with an explicit, exact path boundary: `Set-Cookie: lenny_playground_session=<opaque-session-id>; Path=/playground/; HttpOnly; Secure; SameSite=Strict; Max-Age=<oidcSessionTtlSeconds>`. The trailing slash on `Path=/playground/` is load-bearing: browser cookie path matching is prefix-based, so `Path=/playground/` scopes the cookie to `/playground/` and its sub-paths only and excludes sibling paths such as `/playground-admin` or `/playground.json` that would otherwise match a bare `Path=/playground`. The gateway mints short-lived MCP bearer tokens from this cookie on the user's behalf; the complete exchange flow is specified in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange).
- `playground.authMode=apiKey`: the playground renders an API-key entry form and stores the key in `sessionStorage` only (not `localStorage`, never cookies). The key is sent to the gateway on every request.
- `playground.authMode=dev`: no auth; only permitted when `global.devMode=true` (rejected at Helm-validate otherwise).

All playground sessions carry the `origin=playground` label; policy authors ([§11](11_policy-and-controls.md)) can apply stricter rules by matching this label.

---

#### 27.3.1 OIDC cookie-to-MCP-bearer exchange

This subsection specifies the complete flow that turns a browser OIDC session into the bearer tokens the playground uses on the MCP WebSocket. The design keeps the browser-side HttpOnly cookie strictly separate from the bearer token that rides the WebSocket: the cookie never leaves the `/playground/` path, and the bearer token never reaches the browser's persistent storage.

**1. Login and cookie issuance (`playground.authMode=oidc` only).**

- `GET /playground/auth/login` — Initiates the OIDC authorization-code flow. The gateway generates a per-login `state` and PKCE `code_verifier`, stores them in a short-lived, signed, HttpOnly `lenny_playground_oidc_state` cookie (TTL 10 min, `Path=/playground/auth/`), and redirects the browser to the configured OIDC provider's authorization endpoint. Unauthenticated requests to any `/playground/*` page are redirected here automatically.
- `GET /playground/auth/callback?code=…&state=…` — OIDC provider redirects here. The gateway verifies `state` against the state cookie, performs the PKCE-protected token exchange with the provider, validates the returned ID token (signature, `iss`, `aud`, `exp`, `nbf`), extracts standard Lenny claims (`user_id`, `tenant_id`, `caller_type`, `scope` — see [§10.2](10_gateway-internals.md#102-authentication) and [§25.1](25_agent-operability.md#251-design-philosophy-and-agent-model)), and establishes a **playground session record** keyed by an opaque server-side session id. The record holds the validated OIDC subject claims and the OIDC refresh token (if granted). The gateway then sets `Set-Cookie: lenny_playground_session=<opaque-id>; Path=/playground/; HttpOnly; Secure; SameSite=Strict; Max-Age=<oidcSessionTtlSeconds>` and redirects the browser to the playground index. The raw ID token is **not** placed in a cookie — only the opaque session id is. Default `oidcSessionTtlSeconds` is `3600` (1 h); configurable via `playground.oidcSessionTtlSeconds`.
- `POST /playground/auth/logout` — Clears the playground session record server-side and issues a `Set-Cookie: lenny_playground_session=; Path=/playground/; Max-Age=0` response. Any MCP bearer tokens previously minted for the session are revoked synchronously (see **bearer revocation** below). Cookie-auth only.

**2. Bearer token exchange.**

- `POST /v1/playground/token` — Cookie-authenticated endpoint (no `Authorization` header accepted). The gateway validates the `lenny_playground_session` cookie against the server-side playground session record, confirms the record is non-expired, and mints an MCP bearer token scoped to the caller's `user_id` / `tenant_id` / `scope` claims. Request body: empty JSON object `{}` (reserved for future `sessionMetadata` fields; callers MUST send `{}` so the endpoint can evolve without a breaking change). Response:

    ```json
    {
      "bearerToken": "<opaque or JWT-formatted bearer>",
      "tokenType": "Bearer",
      "expiresInSeconds": 900,
      "reusable": true,
      "issuedAt": "2026-04-19T12:34:56Z"
    }
    ```

    Default `expiresInSeconds` is `900` (15 min); configurable via `playground.bearerTtlSeconds` (bounded `60 ≤ ttl ≤ 3600`). The token is a standard gateway-minted session-capability JWT using the `JWTSigner` described in [§10.2](10_gateway-internals.md#102-authentication) (KMS-backed in production, HMAC in dev), carrying the same `user_id`, `tenant_id`, `caller_type`, `scope`, and `expiry` claims as a non-playground session JWT plus an additional `origin: "playground"` claim. Minting failures return `503 KMS_SIGNING_UNAVAILABLE` with the same retry semantics as other mint paths.

- **Reusability.** `reusable: true` indicates the bearer MAY be reused across any number of concurrent MCP WebSocket connections for the same user within its TTL — opening a second chat tab in the same browser does not require a second exchange. The server does not track or limit concurrent WebSocket count against a single bearer; rate limits apply on the bearer's underlying `user_id` via the standard [§11](11_policy-and-controls.md) controls. This choice trades a small (bounded-TTL) replay surface for predictable behavior when browsers reconnect dropped WebSockets.

- **Caching.** The gateway does not cache bearer tokens beyond the inherent JWT TTL. Each `POST /v1/playground/token` call mints a fresh token; the client caches the response in-memory (never `localStorage`/`sessionStorage`/cookies) for the duration of `expiresInSeconds` minus a 60 s skew budget, then re-exchanges. Multiple rapid calls are rate-limited via the same mechanism as other mint paths — the server does not deduplicate, but clients SHOULD serialize exchanges per tab. The page's CSP (see [§27.7](#277-asset-serving-and-csp)) combined with `HttpOnly` session cookies prevents script access to the cookie.

**3. WebSocket upgrade.**

The client sends the bearer via the standard `Authorization: Bearer <bearerToken>` header on the `wss://<gateway-host>/mcp/v1/ws` upgrade. The gateway validates the bearer exactly as it would for any non-playground MCP client (it is a standard session-capability JWT); no playground-specific WebSocket codepath exists. Browsers that cannot set `Authorization` headers on WebSocket upgrades MUST use the `Sec-WebSocket-Protocol` sub-protocol carrier defined for this purpose: the client sends `Sec-WebSocket-Protocol: lenny.mcp.v1, lenny.bearer.<bearerToken>` and the gateway echoes back `Sec-WebSocket-Protocol: lenny.mcp.v1`. The `lenny.bearer.*` sub-protocol entry MUST be treated as a credential (not logged, not emitted in access logs, redacted in audit traces) — the gateway strips it before audit-event emission.

**4. Refresh and rotation.**

- **Silent refresh.** Before the bearer expires, the client calls `POST /v1/playground/token` again (cookie still valid) to obtain a fresh bearer. Existing WebSocket connections using the old bearer are **not** interrupted — they continue running under the token they were upgraded with until the connection drops or the token's underlying session expiry is hit. New connections use the fresh bearer.
- **Cookie expiry.** When `lenny_playground_session` expires (`oidcSessionTtlSeconds` reached), the next exchange attempt returns `401 UNAUTHORIZED` with `details.reason: "playground_session_expired"`. The client redirects the user to `/playground/auth/login` for re-authentication. The gateway does not perform OIDC refresh-token rotation on behalf of the browser for v1 — the full login flow re-runs. (A future iteration may introduce server-side OIDC refresh; out of scope here.)
- **OIDC claim invalidation.** If the user's underlying OIDC token is revoked (via `POST /v1/admin/users/{user_id}/invalidate`, see [§11.4](11_policy-and-controls.md#114-user-invalidation)), the playground session record is marked invalidated synchronously; subsequent `POST /v1/playground/token` calls return `401 UNAUTHORIZED`, and previously-minted bearer tokens are added to the session-JWT deny list described in [§10.2](10_gateway-internals.md#102-authentication) so in-flight WebSockets are disconnected at the next frame boundary.

**5. Bearer revocation.**

Logout, `user.invalidated`, or playground-session TTL expiry all converge on the same revocation primitive: the gateway writes the bearer's `session_id` / `user_id` to the short-lived JWT deny list propagated across gateway replicas (same mechanism as the mTLS certificate deny list in [§10.3](10_gateway-internals.md#103-mtls-pki), keyed by JWT `jti`). Entries are held for the bearer TTL (at most 15 min) to keep the list bounded.

**6. Audit events.**

Every `POST /v1/playground/token` call emits a `playground.bearer_minted` audit event (fields: `user_id`, `tenant_id`, `session_cookie_id` (opaque), `bearer_jti`, `bearer_ttl_seconds`, `origin: "playground"`). Logout emits `playground.bearer_revoked`. These events share the taxonomy and redaction rules of other auth events in [§11.7](11_policy-and-controls.md#117-audit-logging).

**7. Failure modes summary.**

| Condition | HTTP | `error.code` | Client action |
| --- | ---- | ------------ | ------------- |
| No `lenny_playground_session` cookie | 401 | `UNAUTHORIZED` | Redirect to `/playground/auth/login` |
| Cookie present but server-side record expired | 401 | `UNAUTHORIZED` (with `details.reason: "playground_session_expired"`) | Redirect to `/playground/auth/login` |
| Cookie present, user invalidated | 401 | `UNAUTHORIZED` (with `details.reason: "user_invalidated"`) | Surface error; do not auto-retry |
| KMS signing unavailable | 503 | `KMS_SIGNING_UNAVAILABLE` | Exponential backoff; reuse any still-valid cached bearer |
| Bearer presented on WebSocket after revocation | WebSocket close code `4401` | `bearer_revoked` | Re-exchange (if cookie still valid) or redirect to login |

---

### 27.4 UI surface

The playground ships as a single-page React app with three screens:

1. **Runtime picker.** Lists runtimes visible to the caller (filtered by `playground.allowedRuntimes` and caller scopes). Each entry shows the runtime id, version, description from the Runtime CR, and a "use this runtime" button.
2. **Session configuration.** A form generated from the runtime's `runtimeOptionsSchema` ([§14](14_workspace-plan-schema.md)) using the same JSON-Schema-to-form renderer the installer wizard uses ([§17.6](17_deployment-topology.md)). Also exposes: workspace plan upload (drag-drop tarball), delegation policy selection (if caller has the scope), and session labels.
3. **Chat.** A single-session chat pane backed by the MCP WebSocket. Renders messages, tool-call events, delegation events, and errors. Includes an Interrupt button, a Cancel button, a raw-frame inspector (expandable panel that shows the exact MCP frames for debugging), and a "Copy as client SDK snippet" button that emits equivalent code in Go/Python/TS.

No conversation persistence. Refresh clears the pane; the session continues on the backend until terminated or timed out.

---

### 27.5 Protocol

The playground is **a client of the public MCP surface** — for session, chat, and discovery traffic it uses exactly the same endpoints as any other client. This is deliberate: the playground doubles as a living reference implementation, and any feature the playground uses for session interaction must be exposed on the public API.

- Session creation: `POST /v1/sessions` (REST; see [§15](15_external-api-surface.md)).
- Chat stream: MCP WebSocket at `/mcp/v1/ws` with a bearer token obtained from the cookie-to-bearer exchange in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange).
- Runtime discovery: `GET /v1/runtimes` filtered by `playground.allowedRuntimes`.

The only playground-specific endpoints are the cookie-auth gatekeepers documented in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange) (`/playground/auth/login`, `/playground/auth/callback`, `/playground/auth/logout`, `/v1/playground/token`). They exist solely to bridge the browser OIDC session to a standard MCP bearer token — they carry no session, runtime, or admin capability and are therefore not exposed as MCP tools.

---

### 27.6 Session lifecycle and cleanup

Playground-initiated sessions follow the standard session lifecycle ([§7](07_session-lifecycle.md)) with these deltas:

- Hard duration cap set to `min(sandboxTemplate.spec.maxSessionMinutes, playground.maxSessionMinutes)`.
- **Idle-timeout override.** Playground-initiated sessions MUST NOT remain idle for longer than `playground.maxIdleTimeSeconds` (default: `300` / 5 min). The gateway enforces this value as a **hard override** of the runtime's `maxIdleTimeSeconds` ([§6.2](06_warm-pod-model.md#62-session-lifecycle-state-machine-and-timers)) whenever the session was established through the playground bearer-exchange path (detected via the `origin: "playground"` JWT claim minted in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange)). The effective idle cap is therefore `min(runtime.limits.maxIdleTimeSeconds, playground.maxIdleTimeSeconds)` — the override never relaxes a stricter runtime limit, only tightens a looser one. This caps the reclamation window after the best-effort cancel below fails to deliver.
- On browser close / navigation away, the client sends `session.cancel` with reason `playground_client_closed`. Gateway treats this as a best-effort hint; a dropped WebSocket that cannot send the frame falls back to the idle-timeout path described above, which — because of the override — fires within 5 min (default) rather than the runtime default of 10 min.
- Sessions are labeled with `origin=playground` and the authenticated principal for audit queries ([§25.9](25_agent-operability.md#259-audit-log-query-api)).

---

### 27.7 Asset serving and CSP

Static assets (`index.html`, hashed `*.js` and `*.css` bundles) are served from the embedded FS with long cache headers (`Cache-Control: public, max-age=31536000, immutable`). `index.html` is served with `Cache-Control: no-store` so new releases propagate immediately.

Content-Security-Policy (applied only to `/playground/*`):

```
Content-Security-Policy: default-src 'self';
  script-src 'self';
  style-src 'self' 'unsafe-inline';
  connect-src 'self' wss://<gateway-host>;
  img-src 'self' data:;
  frame-ancestors 'none';
  base-uri 'self';
  form-action 'self'
```

`frame-ancestors 'none'` prevents clickjacking. The gateway also sets `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin` on all playground responses.

---

### 27.8 Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_playground_page_views_total` | Counter | `authMode` | Playground index loads |
| `lenny_playground_sessions_created_total` | Counter | `runtime` | Sessions initiated from the playground |
| `lenny_playground_ws_connect_total` | Counter | `outcome` | MCP WebSocket connections opened from the playground (success/failure) |

Playground request metrics are otherwise the same as other gateway requests; the `origin=playground` session label is the primary way to slice dashboards.

---

### 27.9 Security considerations

- The playground's runtime and policy visibility is limited to what the caller is already authorized to see via the admin API — no elevated scope is granted to the UI.
- The raw-frame inspector displays redacted frames only; the gateway applies the same redaction rules as the audit log ([§16.4](16_observability.md)) before sending frames to the browser.
- File uploads for workspace plan tarballs go through the same size and schema checks as REST-initiated plans ([§14](14_workspace-plan-schema.md)); no client-side trust.
- When `playground.authMode=dev`, the playground UI renders a persistent red banner "DEV MODE — NOT FOR PRODUCTION" sourced from the gateway (so operators cannot easily remove it by swapping the bundle).
- The "Copy as client SDK snippet" feature generates code that never includes credentials; snippets reference environment variables / OIDC flow only.

---

### 27.10 Roll-forward notes

The playground is additive: disabling `playground.enabled` is safe at any time and has no effect on in-flight non-playground sessions. Playground-initiated sessions already in flight continue to run to completion or their configured cap; only new playground sessions are blocked.

Future work (not in scope of this section): a richer workspace editor and runtime authoring flows that let authors test a draft runtime manifest before publishing.
