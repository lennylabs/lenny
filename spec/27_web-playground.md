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
| `playground.sessionLabels` | `{origin: "playground"}` | Labels applied to playground sessions for audit/accounting |

Default is `false` because the playground surface area is not something every installation wants live. `lenny up` (Tier 0, [§17.4.0](17_deployment-topology.md)) sets `playground.enabled=true` and `playground.authMode=dev`; the installer wizard asks explicitly for production installs.

---

### 27.3 Authentication

The playground never bypasses gateway auth. A user hitting `/playground` is routed through the gateway's auth chain identical to API traffic.

- `playground.authMode=oidc`: the gateway redirects unauthenticated users to the configured OIDC provider. The returned ID token is stored in an `HttpOnly; Secure; SameSite=Strict` cookie scoped to `/playground`. The gateway exchanges the cookie for MCP WebSocket bearer tokens on the user's behalf.
- `playground.authMode=apiKey`: the playground renders an API-key entry form and stores the key in `sessionStorage` only (not `localStorage`, never cookies). The key is sent to the gateway on every request.
- `playground.authMode=dev`: no auth; only permitted when `global.devMode=true` (rejected at Helm-validate otherwise).

All playground sessions carry the `origin=playground` label; policy authors ([§11](11_policy-and-controls.md)) can apply stricter rules by matching this label.

---

### 27.4 UI surface

The playground ships as a single-page React app with three screens:

1. **Runtime picker.** Lists runtimes visible to the caller (filtered by `playground.allowedRuntimes` and caller scopes). Each entry shows the runtime id, version, description from the Runtime CR, and a "use this runtime" button.
2. **Session configuration.** A form generated from the runtime's `runtimeOptionsSchema` ([§14](14_workspace-plan-schema.md)) using the same JSON-Schema-to-form renderer the installer wizard uses ([§17.6](17_deployment-topology.md)). Also exposes: workspace plan upload (drag-drop tarball), delegation policy selection (if caller has the scope), and session labels.
3. **Chat.** A single-session chat pane backed by the MCP WebSocket. Renders messages, tool-call events, delegation events, and errors. Includes an Interrupt button, a Cancel button, a raw-frame inspector (expandable panel that shows the exact MCP frames for debugging), and a "Copy as client SDK snippet" button that emits equivalent code in Go/Python/TS.

No conversation persistence. Refresh clears the pane; the session continues on the backend until terminated or timed out.

---

### 27.5 Protocol

The playground is **a client of the public MCP surface** — it uses exactly the same endpoints as any other client. This is deliberate: the playground doubles as a living reference implementation, and any feature the playground uses must be exposed on the public API.

- Session creation: `POST /v1/sessions` (REST; see [§15](15_external-api-surface.md)).
- Chat stream: MCP WebSocket at `/mcp/v1/ws` with bearer token from the auth flow above.
- Runtime discovery: `GET /v1/runtimes` filtered by `playground.allowedRuntimes`.

The playground carries no private gateway endpoints.

---

### 27.6 Session lifecycle and cleanup

Playground-initiated sessions follow the standard session lifecycle ([§7](07_session-lifecycle.md)) with these deltas:

- Hard duration cap set to `min(sandboxTemplate.spec.maxSessionMinutes, playground.maxSessionMinutes)`.
- On browser close / navigation away, the client sends `session.cancel` with reason `playground_client_closed`. Gateway treats this as a best-effort hint; a dropped WebSocket that cannot send the frame falls back to the standard idle-timeout path.
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
