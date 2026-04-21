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
| `playground.authMode` | `oidc` | Auth mode for the playground UI; one of `oidc` (redirect to OIDC provider), `apiKey` (user pastes a standard gateway bearer token — no separate API-key primitive exists; see [§27.3](#273-authentication)), or `dev` (no auth, dev-mode only). |
| `playground.devTenantId` | `default` | Tenant bound to the dev HMAC JWT `tenant_id` claim when `authMode=dev`. Must match `^[a-zA-Z0-9_-]{1,128}$`; format is gated at startup, tenant existence is Ready-gated per-request at `/playground/*` (a transient `503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED` while the `lenny-bootstrap` Job ([§17.6](17_deployment-topology.md#176-packaging-and-installation)) is still running, self-healing once the tenant row commits). Helm-validate rejects the chart if `authMode=dev`, `auth.multiTenant=true`, and this value is left at `default` while multiple tenants are seeded. See [§27.3](#273-authentication). |
| `playground.allowedRuntimes` | `["*"]` | Glob list of runtime IDs visible in the playground runtime picker |
| `playground.maxSessionMinutes` | `30` | Hard cap on playground-initiated session duration |
| `playground.maxIdleTimeSeconds` | `300` | Hard override of the runtime's `maxIdleTimeSeconds` for playground-initiated sessions (bounded `60 ≤ v ≤ runtime's maxIdleTimeSeconds`). See [§27.6](#276-session-lifecycle-and-cleanup). |
| `playground.oidcSessionTtlSeconds` | `3600` | Lifetime of the server-side playground session record and the `lenny_playground_session` cookie. See [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange). |
| `playground.bearerTtlSeconds` | `900` | TTL of MCP bearer tokens minted by `POST /v1/playground/token` (bounded `60 ≤ ttl ≤ 3600`). See [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange). |
| `playground.sessionLabels` | `{origin: "playground"}` | Labels applied to playground sessions for audit/accounting |
| `playground.acknowledgeApiKeyMode` | `false` | Set `true` to acknowledge the `apiKey`-mode paste-form phishing surface documented in [§27.9](#279-security-considerations). When `playground.enabled=true` and `playground.authMode=apiKey` and `global.devMode=false`, `lenny-preflight` emits a non-blocking `WARNING` unless this value is `true` (same pattern as `monitoring.acknowledgeNoPrometheus`, [§25.4](25_agent-operability.md#254-the-lenny-ops-service)). The acknowledgement is install-time only — the gateway does not gate startup on it. |

Default is `false` because the playground surface area is not something every installation wants live. `lenny up` (Embedded Mode, [§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)) sets `playground.enabled=true` and `playground.authMode=dev`; the installer wizard asks explicitly for production installs.

**`playground.devTenantId` validation layering.** The value is validated at three layers, with the **Helm schema and preflight as the primary defenses**, the gateway startup codes as backstops for format/cross-field violations, and a per-request Ready-gate for the bootstrap-ordering window when the tenant row has not yet been seeded:

1. **Primary — Helm `values.schema.json` (install-time, format-only).** The schema entry documented in [§17.6](17_deployment-topology.md#176-packaging-and-installation) ("Key Helm values" list) pins `type: string`, `pattern: ^[a-zA-Z0-9_-]{1,128}$`, `maxLength: 128`. This matches the canonical `tenant_id` regex at [§10.2](10_gateway-internals.md#102-authentication) (`TENANT_CLAIM_INVALID_FORMAT`), so a typo (whitespace, `.`, slash, overlong value) is rejected at `helm install` / `helm upgrade` / `helm install --dry-run` time — including on `lenny-ctl values validate` in CI — before any pod is scheduled.
2. **Primary — `lenny-preflight` row (install-time, cross-field).** The preflight check listed in [§17.6](17_deployment-topology.md#176-packaging-and-installation) ("Checks performed" table, row `playground.devTenantId` format and presence) enforces the cross-field conditionals that JSON Schema cannot express: `authMode=dev` requires a non-empty `devTenantId`, and `authMode=dev` with `auth.multiTenant=true` requires an explicit (non-`default`) value when multiple tenants are seeded. The preflight Job runs as a `helm.sh/hook: pre-install,pre-upgrade` hook and additionally fires under `helm install --dry-run`, so GitOps pipelines (ArgoCD, Flux) and CI-driven `helm template`→`helm install --dry-run` flows fail at template/render time rather than at gateway pod startup.
3. **Backstop — gateway startup (pod-start, defense-in-depth, format/cross-field only).** Two fatal codes — `LENNY_PLAYGROUND_DEV_TENANT_INVALID` (devTenantId regex fails at startup) and `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` (mode=dev but devTenantId empty, or mode=dev with `multiTenant=true` while `default` remains bound against multiple seeded tenants) — remain as a defense-in-depth check for deployments that skipped preflight (`preflight.enabled: false`) or mutated the value after install via a raw `kubectl edit` on the rendered `Deployment`/`ConfigMap`. These codes cover only format and cross-field violations; **tenant-existence is not a startup gate** (see layer 4) to avoid deadlocking against the `post-install` `lenny-bootstrap` Job that seeds the tenant row. A pod `CrashLoopBackOff` on one of these codes indicates that both the schema and preflight layers were bypassed.
4. **Ready-gate — `/playground/*` routes (per-request, bootstrap-ordering).** If `authMode=dev` and the configured `devTenantId` is well-formed but **not yet present** in Postgres (the `lenny-bootstrap` Job has not completed), the gateway starts normally and serves all non-playground routes, but every request to `/playground/*` returns `503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED` with `Retry-After: 5` until the tenant row appears. Non-playground routes (admin API, `/healthz`, `/v1/sessions`, MCP endpoints) are unaffected so the bootstrap Job's `GET /healthz` poll completes and the Job can proceed to seed the tenant; the 503 self-heals on the next `/playground/*` request once the tenant row commits. The gateway does not cache the negative lookup — it consults the same per-request tenant-resolution path already on the auth hot path — so no rollout or restart is required for recovery. Operators consuming this error should interpret it as "bootstrap is in progress" rather than "misconfigured" and follow the `lenny-bootstrap` Job status via `kubectl get jobs -n lenny-system lenny-bootstrap`.

---

### 27.3 Authentication

The playground never bypasses gateway auth. A user hitting `/playground` is routed through the gateway's auth chain identical to API traffic.

- `playground.authMode=oidc`: the gateway redirects unauthenticated users to the configured OIDC provider. On successful OIDC token exchange the gateway establishes an opaque server-side session record (the raw ID token is never placed in a cookie — see [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange)) and sets a single session cookie with an explicit, exact path boundary: `Set-Cookie: lenny_playground_session=<opaque-session-id>; Path=/playground/; HttpOnly; Secure; SameSite=Strict; Max-Age=<oidcSessionTtlSeconds>`. The trailing slash on `Path=/playground/` is load-bearing: browser cookie path matching is prefix-based, so `Path=/playground/` scopes the cookie to `/playground/` and its sub-paths only and excludes sibling paths such as `/playground-admin` or `/playground.json` that would otherwise match a bare `Path=/playground`. The gateway mints short-lived MCP bearer tokens from this cookie on the user's behalf; the complete exchange flow is specified in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange).
- `playground.authMode=apiKey`: the playground renders a bearer-token entry form (labelled "API key" in the UI for familiarity) and stores the token in `sessionStorage` only (not `localStorage`, never cookies). The token is an OIDC ID token or service-account bearer token — the **same credential accepted on the standard Client→Gateway or Automated-clients auth paths** in [§10.2](10_gateway-internals.md#102-authentication); no separate "API-key" credential primitive exists in v1. The token is sent to the gateway as `Authorization: Bearer <token>` on every request and is validated by the standard gateway auth chain, including the `tenant_id`-claim extraction and rejection semantics in [§10.2](10_gateway-internals.md#102-authentication). **Subject-token admission and scope narrowing.** Before the handler invokes the session-JWT mint, it enforces the playground mint invariants pinned in [§10.2 "Playground mint invariants"](10_gateway-internals.md#102-authentication): the pasted bearer's `typ` MUST be `user_bearer` (a `session_capability`, `a2a_delegation`, or `service_token` pasted into the API-key form is rejected with `401 LENNY_PLAYGROUND_BEARER_TYPE_REJECTED`, preventing a narrowly-scoped capability JWT from being re-minted into a broader playground JWT), and the minted JWT's `scope` is `intersection(subject_token.scope, playground_allowed_scope)` — never the union. Policy reviewers and playground-UI authors MUST read the §10.2 invariant block as the authoritative contract; the bullets below and the mint-point note in the "Mode-agnostic" paragraph are pointers, not a redefinition. `apiKey` mode is intended for operator-driven workflows (smoke-tests, runtime-author headless flows) where pasting a service-account token is acceptable; human-user access should use `authMode=oidc`.
- `playground.authMode=dev`: no auth; only permitted when `global.devMode=true` (rejected at Helm-validate otherwise). The `tenant_id` claim on the dev HMAC JWT is sourced from `playground.devTenantId` (Helm value; default `default`, matching the built-in `default` tenant that Embedded Mode (`lenny up`, [§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)) auto-provisions). The value must satisfy the `^[a-zA-Z0-9_-]{1,128}$` format constraint in [§10.2](10_gateway-internals.md#102-authentication). **Format validation is startup-gated; tenant-existence is Ready-gated at the `/playground/*` route**: the gateway refuses to start with `LENNY_PLAYGROUND_DEV_TENANT_INVALID` if the configured tenant id is **malformed** (fails the regex), but if the tenant id is well-formed yet **absent** from Postgres at startup the gateway process starts normally and only the `/playground/*` route family returns `503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED` with `Retry-After: 5` until the tenant row appears. All non-playground routes (admin API, `/healthz`, `/v1/sessions`, MCP endpoints) remain fully available so that the `lenny-bootstrap` Job (`helm.sh/hook: post-install,post-upgrade`, [§17.6](17_deployment-topology.md#176-packaging-and-installation)) — which polls `GET /healthz` before seeding the tenant row — is not deadlocked against a startup gate that would itself require the bootstrap Job to have run first. The gateway re-checks tenant existence on every `/playground/*` request (consulting the same per-request tenant lookup already on the auth hot path, not a separate poll loop), so the 503 self-heals the instant the bootstrap Job commits the tenant row; no gateway restart or rollout is required. The 503 response body carries the canonical error envelope with `code: "LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED"`, `message` pointing at the `lenny-bootstrap` Job status, and `details.devTenantId` echoing the configured value. When `auth.multiTenant: true` is set (a non-Embedded dev deployment with more than one tenant registered), `playground.devTenantId` MUST be set explicitly — Helm-validate rejects the chart with `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` if the value is left at `default` while multiple tenants are seeded, so dev-mode JWTs never silently bind to an ambiguous tenant.

**Mode-agnostic `origin: "playground"` JWT claim.** The `origin: "playground"` claim is minted on **every** session-capability JWT produced for a request that originates from a `/playground/*` route, regardless of `authMode`. This claim — not `authMode` — is the authoritative signal that drives the tighter idle-timeout override ([§27.6](#276-session-lifecycle-and-cleanup)), the `playground.maxSessionMinutes` duration cap ([§27.6](#276-session-lifecycle-and-cleanup)), the `origin=playground` dashboard slice ([§27.8](#278-metrics)), and any policy rules that match on it ([§11](11_policy-and-controls.md)). **The single mint endpoint for all three modes is `POST /v1/playground/token`** (mode-polymorphic — full per-mode admission semantics in the **Auth by mode** table at [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange) "Bearer token exchange"). The per-mode subject resolution differs:
- **`oidc`:** the endpoint reads the `lenny_playground_session` cookie, resolves the subject to the OIDC principal backing the server-side session record, and mints a bearer with the claim attached.
- **`apiKey`:** the endpoint reads `Authorization: Bearer <token>`, validates the pasted bearer via the standard gateway auth chain ([§10.2](10_gateway-internals.md#102-authentication) — same validation as any non-playground API caller, including `tenant_id` claim extraction and `TENANT_CLAIM_MISSING` / `TENANT_NOT_FOUND` rejections), enforces the playground mint invariants in [§10.2 "Playground mint invariants"](10_gateway-internals.md#102-authentication) (subject `typ == user_bearer`, scope narrowed to the intersection of the subject scope and `playground_allowed_scope`, tenant preservation, duration cap), and mints the session-JWT with the `origin: "playground"` claim attached. The attachment is driven by the request's ingress route (`/playground/*`), not by the bearer material — the same token presented on a non-playground route does not produce the claim.
- **`dev`:** the endpoint accepts an empty body with no admission material (the `global.devMode=true` gate is enforced at Helm-validate per [§27.2](#272-placement-and-gating)), synthesizes the subject from `playground.devTenantId` and a synthetic `dev-user` principal, and issues a dev HMAC-signed session JWT ([§10.2](10_gateway-internals.md#102-authentication) dev-mode signer) with the `origin: "playground"` claim attached. Non-playground dev-mode tokens do not carry the claim.

In all three modes the claim is stamped by the `/playground/*` ingress path, so the downstream enforcement points in [§27.6](#276-session-lifecycle-and-cleanup) and [§27.8](#278-metrics) work uniformly. All playground sessions additionally carry the `origin=playground` label on the session record; policy authors ([§11](11_policy-and-controls.md)) can apply stricter rules by matching either the JWT claim or the label.

---

#### 27.3.1 OIDC cookie-to-MCP-bearer exchange

This subsection specifies the complete flow that turns a browser OIDC session into the bearer tokens the playground uses on the MCP WebSocket. The design keeps the browser-side HttpOnly cookie strictly separate from the bearer token that rides the WebSocket: the cookie never leaves the `/playground/` path, and the bearer token never reaches the browser's persistent storage.

This subsection is **OIDC-mode-specific**: the cookie, login, and exchange endpoints below exist only when `playground.authMode=oidc`. The **`origin: "playground"` JWT claim** that downstream sections key on is, however, **mode-agnostic** — it is stamped on session-capability JWTs produced for any `/playground/*`-originated request regardless of `authMode`. See [§27.3](#273-authentication) ("Mode-agnostic `origin: "playground"` JWT claim") for the per-mode mint points covering `apiKey` and `dev`.

**1. Login and cookie issuance (`playground.authMode=oidc` only).**

- `GET /playground/auth/login` — Initiates the OIDC authorization-code flow. The gateway generates a per-login `state` and PKCE `code_verifier`, stores them in a short-lived, signed, HttpOnly `lenny_playground_oidc_state` cookie (TTL 10 min, `Path=/playground/auth/`), and redirects the browser to the configured OIDC provider's authorization endpoint. Unauthenticated requests to any `/playground/*` page are redirected here automatically.
- `GET /playground/auth/callback?code=…&state=…` — OIDC provider redirects here. The gateway verifies `state` against the state cookie, performs the PKCE-protected token exchange with the provider, validates the returned ID token (signature, `iss`, `aud`, `exp`, `nbf`), extracts standard Lenny claims (`user_id`, `tenant_id`, `caller_type`, `scope` — see [§10.2](10_gateway-internals.md#102-authentication) and [§25.1](25_agent-operability.md#251-design-philosophy-and-agent-model)), and establishes a **playground session record** keyed by an opaque server-side session id (see **Session record backing store** below). `tenant_id` extraction reuses the canonical rejection semantics pinned in [§10.2](10_gateway-internals.md#102-authentication) (claim configured by `auth.tenantIdClaim`); when extraction fails the gateway **does not** establish a session record or set `lenny_playground_session`, and instead redirects the browser to the playground error page `GET /playground/auth/error?error=<code>` with the code assigned in the **Tenant-claim rejection codes (OIDC callback)** table immediately below. The record holds the validated OIDC subject claims and the OIDC refresh token (if granted). On success, the gateway sets `Set-Cookie: lenny_playground_session=<opaque-id>; Path=/playground/; HttpOnly; Secure; SameSite=Strict; Max-Age=<oidcSessionTtlSeconds>` and redirects the browser to the playground index. The raw ID token is **not** placed in a cookie — only the opaque session id is. Default `oidcSessionTtlSeconds` is `3600` (1 h); configurable via `playground.oidcSessionTtlSeconds`.
- `POST /playground/auth/logout` — Clears the playground session record server-side and issues a `Set-Cookie: lenny_playground_session=; Path=/playground/; Max-Age=0` response. Any MCP bearer tokens previously minted for the session are revoked synchronously (see **bearer revocation** below). The gateway MUST complete the session-record delete and the revocation writes (see **Session record backing store** below) before returning `200` to the browser, so a successful logout response guarantees that replica-local deny-list state has been durably written to the shared store; the pub/sub fanout to peer replicas then completes within the propagation SLO specified in that subsection. Cookie-auth only.

**Session record backing store.** The opaque server-side playground session record is held in **Redis**, anchored on the per-tenant prefix convention pinned in [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes):

| Key | Role | Value / Semantics |
| --- | --- | --- |
| `t:{tenant_id}:pg:sess:{session_id}` | Playground session record | JSON envelope: `user_id`, `tenant_id`, `caller_type`, `scope`, the most-recently-minted bearer `jti` and `exp`, `origin: "playground"`, `issued_at`, and a CSRF anti-forgery token. `{session_id}` is the opaque id carried by the `lenny_playground_session` cookie. |
| `t:{tenant_id}:pg:revoked:{jti}` | Minted-bearer revocation marker | Empty value (presence-only); the `jti` is the bearer's JWT id from the `POST /v1/playground/token` mint. |

Semantics:

- **TTL.** `pg:sess:*` TTL equals the remaining cookie lifetime (`oidcSessionTtlSeconds − time_since_callback`) — that is, the session record is pinned to the cookie's `Max-Age`, not to an individual bearer's TTL, because the record outlives each silent-refreshed bearer and MUST remain authoritative until the cookie itself expires. On each successful `POST /v1/playground/token` mint, the gateway rewrites the session record in place with the new bearer `jti`/`exp` (without resetting the cookie-anchored TTL). `pg:revoked:*` TTL equals the remaining bearer lifetime at the moment of revocation (`exp − now`, plus a 5 s skew budget to absorb clock drift between gateway replicas and Redis), so each marker self-expires the instant the underlying bearer would have expired naturally — keeping the revocation namespace bounded.
- **Revocation write.** On logout, `user.invalidated` ([§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange) step 4 "OIDC claim invalidation"), or explicit admin revocation, the gateway `DEL`s the `pg:sess:*` key and issues a `SET` on `pg:revoked:{jti}` for **every** bearer `jti` associated with the session within the record's lifetime (the current `jti` stored on the record, plus any in-flight `jti`s the session replaced during silent-refresh — the gateway tracks the last-N `jti`s on the record, bounded by `⌈oidcSessionTtlSeconds / bearerTtlSeconds⌉ + 1`).
- **Per-request revocation check.** Every authenticated request carrying a playground-origin bearer (identified by the `origin: "playground"` claim — [§27.3](#273-authentication)) MUST consult `t:{tenant_id}:pg:revoked:{jti}` on the auth hot path before the bearer is honored. A hit produces `401 UNAUTHORIZED` with `details.reason: "bearer_revoked"` on REST/MCP requests and WebSocket close code `4401` on in-flight upgrades (matching the **Failure modes summary** row below). This check is the correctness guarantee that a logout on one replica cannot be bypassed by presenting the same cookie or bearer to a peer replica.
- **Pub/sub propagation.** Revocation writers additionally `PUBLISH` on the tenant-scoped channel `t:{tenant_id}:pg:revocations` — a dedicated playground-role channel that reuses the per-tenant `t:{tenant_id}:` prefix convention from [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) and sits alongside the EventBus `t:{tenant_id}:evt:{topic}` channels rather than multiplexing onto them (this keeps the tight-latency revocation fanout off the delegation-tree / session-lifecycle event bus). Every gateway replica subscribes to this channel at startup and populates a bounded in-process LRU negative cache (`max-entries = max(1024, 8 × concurrent sessions)`, entry TTL = remaining bearer lifetime) so the auth hot path can short-circuit the Redis `GET` on a cache hit without sacrificing correctness (the cache is *negative-only* — a miss still consults Redis). **Logout propagation SLO:** a revocation published on any replica MUST be visible to the per-request revocation check on every other replica within **500 ms at P99** under nominal Redis health; the sub-authoritative LRU cache MUST converge within the same budget. Pub/sub failures (message drop, subscriber disconnect) never relax the SLO because Redis remains the authoritative store consulted on every request — pub/sub is a propagation accelerator, not a correctness boundary. Replicas with a dropped subscription MUST re-subscribe and emit a `lenny_playground_session_revocation_propagation_seconds` sample tagged `{outcome="resubscribe"}` for the duration of the outage.
- **Redis unavailability.** The revocation check fails **closed**: when Redis is unreachable during the per-request check, playground-origin requests are rejected with `503 REDIS_UNAVAILABLE` rather than permitted. This matches the fail-closed posture for circuit breakers and delegation budget counters in [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) and prevents a Redis outage from silently converting into a revocation-bypass window.
- **Integration test.** `TestPlaygroundSessionRevocationCrossReplica` MUST assert that a logout on replica A invalidates a subsequent request carrying the same cookie or bearer on replica B, both before and after the pub/sub message is delivered (the authoritative Redis check covers the pre-delivery case; the LRU negative cache covers the post-delivery case). The test suite additionally extends `TestRedisTenantKeyIsolation` ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) to cover the two new key prefixes: a `pg:sess:*` write for tenant A must not be readable by a gateway request scoped to tenant B, and a `pg:revoked:*` marker for tenant A's `jti` must not cause rejection of a tenant B request reusing the same (lexically-equal) `jti` value — enforcing that the tenant-prefix isolation extends to these keys.

**Tenant-claim rejection codes (OIDC callback).** The `/playground/auth/callback` path reuses the canonical tenant-claim rejection codes defined in [§10.2](10_gateway-internals.md#102-authentication) — this table is a cross-reference for operator debugging, not a redefinition. §10.2 remains authoritative; any future change to the codes, HTTP statuses, or format regex applies here unchanged. Because the browser cannot consume a JSON error envelope on the OIDC redirect path, the callback handler translates each rejection into a redirect to the playground error page (`GET /playground/auth/error`) with an `error=<code>` query parameter the UI surfaces to the user; the HTTP status column records the status emitted on the final response to the browser (the redirect itself is a 302, and the error page renders with the status below once followed). Every rejection is logged via the tenant-attribution logger ([§16.4](16_observability.md)) and emits an `auth_failure` audit event with the fields indicated.

| Code | Condition | HTTP | Redirect query param | Log attribution |
| --- | --- | --- | --- | --- |
| `TENANT_CLAIM_MISSING` | ID token lacks the configured `auth.tenantIdClaim` (default `tenant_id`) claim, or the claim is an empty string | 401 | `?error=tenant_claim_missing` | `tenant_id=__unset__` |
| `TENANT_NOT_FOUND` | Extracted value is well-formed but does not match any provisioned `Tenant` CR | 403 | `?error=tenant_not_found` | `tenant_id=<extracted value>` (audit only; never bound to a session or used for authorization) |
| `TENANT_CLAIM_INVALID_FORMAT` | Extracted value fails `^[a-zA-Z0-9_-]{1,128}$` (enforced before the tenant-registered lookup per [§10.2](10_gateway-internals.md#102-authentication)) | 401 | `?error=tenant_claim_invalid_format` | `tenant_id=__unset__` |

The redirect query-param namespace (`tenant_claim_missing`, `tenant_not_found`, `tenant_claim_invalid_format`) is reserved for this table and MUST NOT be reused by other `/playground/auth/error` failure modes. No `lenny_playground_session` cookie is set for any row; the state cookie is cleared as part of the error redirect to prevent dangling PKCE state. These rejections are surfaced before session-record creation and are distinct from the **Failure modes summary** table below (which covers post-session-record failures at `POST /v1/playground/token`).

**2. Bearer token exchange.**

- `POST /v1/playground/token` — The single playground bearer-mint endpoint. This endpoint is **mode-polymorphic**: it is the mint surface for all three `playground.authMode` values (`oidc`, `apiKey`, `dev`), with per-mode admission auth specified in the **Auth by mode** table below. The gateway identifies the caller via the per-mode admission material, resolves the subject's `user_id` / `tenant_id` / `caller_type` / `scope` claims, applies the [§10.2 Playground mint invariants](10_gateway-internals.md#102-authentication) (subject-typ, scope narrowing, tenant preservation, origin, caller-type preservation), and mints an MCP bearer token. Request body: empty JSON object `{}` (reserved for future `sessionMetadata` fields; callers MUST send `{}` so the endpoint can evolve without a breaking change). Response:

    ```json
    {
      "bearerToken": "<opaque or JWT-formatted bearer>",
      "tokenType": "Bearer",
      "expiresInSeconds": 900,
      "reusable": true,
      "issuedAt": "2026-04-19T12:34:56Z"
    }
    ```

    Default `expiresInSeconds` is `900` (15 min); configurable via `playground.bearerTtlSeconds` (bounded `60 ≤ ttl ≤ 3600`). The token is a standard gateway-minted session-capability JWT using the `JWTSigner` described in [§10.2](10_gateway-internals.md#102-authentication) (KMS-backed in production, HMAC in dev), carrying the same `user_id`, `tenant_id`, `caller_type`, and `expiry` claims as a non-playground session JWT plus an additional `origin: "playground"` claim; the `scope` claim is narrowed to `intersection(subject.scope, playground_allowed_scope)` per the [§10.2 Playground mint invariants](10_gateway-internals.md#102-authentication). Minting failures return `503 KMS_SIGNING_UNAVAILABLE` with the same retry semantics as other mint paths.

    **Auth by mode.** The endpoint's admission material differs per `playground.authMode`; all three modes converge on the same mint invariants and response schema above. Exactly one admission path is accepted per mode — presenting the wrong material (or both) for the active mode is a hard rejection.

    | `playground.authMode` | Admission material | Subject resolution | Cross-mode rejection |
    | --- | --- | --- | --- |
    | `oidc` | `lenny_playground_session` cookie (HttpOnly, `Path=/playground/`, validated against the server-side playground session record); `Authorization: Bearer` on this endpoint is rejected with `400 LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL` to prevent bearer-to-bearer reissuance. | Subject is the validated OIDC principal backing the `lenny_playground_session` record, normalized to `typ = user_bearer`. | Presenting `Authorization: Bearer` in `oidc` mode → `400 LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL`. |
    | `apiKey` | `Authorization: Bearer <token>` carrying a pasted OIDC ID token or previously-minted `user_bearer` gateway JWT (same credential accepted on the standard Client→Gateway auth chain in [§10.2](10_gateway-internals.md#102-authentication)); the endpoint rejects any `lenny_playground_session` cookie presented in `apiKey` mode with `400 LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL`. | Gateway runs the standard auth chain (signature, `iss`, `aud`, `exp`, `nbf`), applies `tenant_id` extraction per [§10.2](10_gateway-internals.md#102-authentication) (`TENANT_CLAIM_MISSING` / `TENANT_NOT_FOUND` / `TENANT_CLAIM_INVALID_FORMAT` surfaced as JSON error envelopes — not the OIDC-callback redirect flow in the **Tenant-claim rejection codes** table above, which is `oidc`-mode-only), and normalizes the subject to `typ = user_bearer` before invariant evaluation. | Presenting a cookie without an `Authorization: Bearer` header in `apiKey` mode → `400 LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL`. |
    | `dev` | No admission material required (`global.devMode=true` gate is enforced at Helm-validate / startup per [§27.2](#272-placement-and-gating)); the endpoint accepts an empty request body from any caller on the `/playground/*` route and synthesizes the subject from `playground.devTenantId` and a synthetic `dev-user` principal (no OIDC signature check runs). | Subject is the synthetic dev principal; the `JWTSigner` is the dev HMAC backend and invariants (1)–(3) are trivially satisfied (no subject token to narrow from). Invariants (4)–(5) still bind: `origin: "playground"` is stamped, `exp = now + playground.bearerTtlSeconds`, `caller_type` and `roles` are the dev principal's. | Any `Authorization: Bearer` or `lenny_playground_session` cookie presented in `dev` mode is **ignored** (not rejected — dev mode never gates on caller material). |

    **Mode enforcement is route-stamped.** The gateway reads `playground.authMode` from its own configuration, not from the request — a cookie or bearer is accepted or rejected based on the installation's configured mode, never negotiated per-request. This prevents a malicious caller from flipping auth modes by presenting the wrong material type. The `400 LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL` response body carries the canonical error envelope with `details.configuredAuthMode` echoing the installation's mode and `details.presentedMaterial ∈ {cookie, bearer, both}` for operator debugging.

- **Reusability.** `reusable: true` indicates the bearer MAY be reused across any number of concurrent MCP WebSocket connections for the same user within its TTL — opening a second chat tab in the same browser does not require a second exchange. The server does not track or limit concurrent WebSocket count against a single bearer; rate limits apply on the bearer's underlying `user_id` via the standard [§11](11_policy-and-controls.md) controls. This choice trades a small (bounded-TTL) replay surface for predictable behavior when browsers reconnect dropped WebSockets.

- **Caching.** The gateway does not cache bearer tokens beyond the inherent JWT TTL. Each `POST /v1/playground/token` call mints a fresh token; the client caches the response in-memory (never `localStorage`/`sessionStorage`/cookies) for the duration of `expiresInSeconds` minus a 60 s skew budget, then re-exchanges. Multiple rapid calls are rate-limited via the same mechanism as other mint paths — the server does not deduplicate, but clients SHOULD serialize exchanges per tab. The page's CSP (see [§27.7](#277-asset-serving-and-csp)) combined with `HttpOnly` session cookies prevents script access to the cookie.

**3. WebSocket upgrade.**

The client sends the bearer via the standard `Authorization: Bearer <bearerToken>` header on the `wss://<gateway-host>/mcp/v1/ws` upgrade. The gateway validates the bearer exactly as it would for any non-playground MCP client (it is a standard session-capability JWT); no playground-specific WebSocket codepath exists. Browsers that cannot set `Authorization` headers on WebSocket upgrades MUST use the `Sec-WebSocket-Protocol` sub-protocol carrier defined for this purpose: the client sends `Sec-WebSocket-Protocol: lenny.mcp.v1, lenny.bearer.<bearerToken>` and the gateway echoes back `Sec-WebSocket-Protocol: lenny.mcp.v1`. The `lenny.bearer.*` sub-protocol entry MUST be treated as a credential (not logged, not emitted in access logs, redacted in audit traces) — the gateway strips it before audit-event emission.

**4. Refresh and rotation.**

- **Silent refresh.** Before the bearer expires, the client calls `POST /v1/playground/token` again (cookie still valid) to obtain a fresh bearer. Existing WebSocket connections using the old bearer are **not** interrupted — they continue running under the token they were upgraded with until the connection drops or the token's underlying session expiry is hit. New connections use the fresh bearer.
- **Cookie expiry.** When `lenny_playground_session` expires (`oidcSessionTtlSeconds` reached), the next exchange attempt returns `401 UNAUTHORIZED` with `details.reason: "playground_session_expired"`. The client redirects the user to `/playground/auth/login` for re-authentication. The gateway does not perform OIDC refresh-token rotation on behalf of the browser for v1 — the full login flow re-runs. (A future iteration may introduce server-side OIDC refresh; out of scope here.)
- **OIDC claim invalidation.** If the user's underlying OIDC token is revoked (via `POST /v1/admin/users/{user_id}/invalidate`, see [§11.4](11_policy-and-controls.md#114-user-invalidation)), the playground session record is marked invalidated synchronously; subsequent `POST /v1/playground/token` calls return `401 UNAUTHORIZED`, and previously-minted bearer tokens are added to the session-JWT deny list described in [§10.2](10_gateway-internals.md#102-authentication) so in-flight WebSockets are disconnected at the next frame boundary.

**5. Bearer revocation.**

Logout, `user.invalidated`, or playground-session TTL expiry all converge on the same revocation primitive: the gateway writes the bearer's `jti` to the per-tenant **playground revocation key** `t:{tenant_id}:pg:revoked:{jti}` defined in the **Session record backing store** subsection above, fans out the change on `t:{tenant_id}:pg:revocations` (Redis pub/sub), and updates the shared short-lived JWT deny list (same cross-replica mechanism as the mTLS certificate deny list in [§10.3](10_gateway-internals.md#103-mtls-pki), keyed by JWT `jti`). The authoritative check on every playground-origin request is the Redis `GET` on `pg:revoked:{jti}` — the in-process JWT deny-list cache in each replica is a performance optimization that is warmed by the pub/sub fan-out, not a substitute for the store lookup. Entries are held for the bearer TTL (at most 15 min) to keep the list bounded.

**6. Audit events.**

Every `POST /v1/playground/token` call emits a `playground.bearer_minted` audit event (fields: `user_id`, `tenant_id`, `session_cookie_id` (opaque), `bearer_jti`, `bearer_ttl_seconds`, `origin: "playground"`). Logout emits `playground.bearer_revoked`. These events share the taxonomy and redaction rules of other auth events in [§11.7](11_policy-and-controls.md#117-audit-logging).

**7. Failure modes summary.**

| Condition | HTTP | `error.code` | Client action |
| --- | ---- | ------------ | ------------- |
| No `lenny_playground_session` cookie | 401 | `UNAUTHORIZED` | Redirect to `/playground/auth/login` |
| Cookie present but server-side record expired | 401 | `UNAUTHORIZED` (with `details.reason: "playground_session_expired"`) | Redirect to `/playground/auth/login` |
| Cookie present, user invalidated | 401 | `UNAUTHORIZED` (with `details.reason: "user_invalidated"`) | Surface error; do not auto-retry |
| KMS signing unavailable | 503 | `KMS_SIGNING_UNAVAILABLE` | Exponential backoff; reuse any still-valid cached bearer |
| Bearer presented on REST/MCP after revocation (`pg:revoked:{jti}` hit) | 401 | `UNAUTHORIZED` (with `details.reason: "bearer_revoked"`) | Re-exchange (if cookie still valid) or redirect to login |
| Bearer presented on WebSocket after revocation | WebSocket close code `4401` | `bearer_revoked` | Re-exchange (if cookie still valid) or redirect to login |
| Redis unavailable during per-request revocation check | 503 | `REDIS_UNAVAILABLE` | Exponential backoff; do not treat absence of a revocation record as "not revoked" |

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
- Chat stream: MCP WebSocket at `/mcp/v1/ws` with a session-capability JWT as the bearer. The JWT is minted by the single mode-polymorphic endpoint `POST /v1/playground/token` in all three `playground.authMode` values — cookie-authenticated in `oidc` mode, `Authorization: Bearer`-authenticated in `apiKey` mode, and admission-material-free in `dev` mode. Full per-mode admission semantics (the **Auth by mode** table) are specified in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange) ("Bearer token exchange"); the WebSocket codepath itself is identical across modes because all three produce the same standard session-capability JWT with the `origin: "playground"` claim stamped.
- Runtime discovery: `GET /v1/runtimes` filtered by `playground.allowedRuntimes`.

The only playground-specific endpoints are the cookie-auth gatekeepers documented in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange) (`/playground/auth/login`, `/playground/auth/callback`, `/playground/auth/logout`, `/v1/playground/token`). They exist solely to bridge the browser OIDC session to a standard MCP bearer token — they carry no session, runtime, or admin capability and are therefore not exposed as MCP tools.

---

### 27.6 Session lifecycle and cleanup

Playground-initiated sessions follow the standard session lifecycle ([§7](07_session-lifecycle.md)) with these deltas:

- **Hard duration cap.** `min(sandboxTemplate.spec.maxSessionMinutes, playground.maxSessionMinutes)`. Enforcement binds whenever the session-capability JWT carries the `origin: "playground"` claim ([§27.3](#273-authentication)), so the cap applies uniformly to `oidc`, `apiKey`, and `dev` playground sessions.
- **Idle-timeout override.** Playground-initiated sessions MUST NOT remain idle for longer than `playground.maxIdleTimeSeconds` (default: `300` / 5 min). The gateway enforces this value as a **hard override** of the runtime's `maxIdleTimeSeconds` ([§7.2](07_session-lifecycle.md#72-interactive-session-model)) whenever the session was established through a `/playground/*` ingress path — detected via the `origin: "playground"` JWT claim, which [§27.3](#273-authentication) stamps on session-capability JWTs for all three auth modes (`oidc`, `apiKey`, `dev`), not only OIDC. The effective idle cap is therefore `min(runtime.limits.maxIdleTimeSeconds, playground.maxIdleTimeSeconds)` — the override never relaxes a stricter runtime limit, only tightens a looser one. This caps the reclamation window after the best-effort cancel below fails to deliver.
- On browser close / navigation away, the client sends `session.cancel` with reason `playground_client_closed`. Gateway treats this as a best-effort hint; a dropped WebSocket that cannot send the frame falls back to the idle-timeout path described above, which — because of the override — fires within 5 min (default) rather than the runtime default of 10 min.
- Sessions are labeled with `origin=playground` and the authenticated principal for audit queries ([§25.9](25_agent-operability.md#259-audit-log-query-api)). The label is applied for every `/playground/*`-originated session regardless of `authMode`, matching the JWT-claim coverage above.
- **Server-side session-record lifecycle.** The opaque OIDC cookie-to-bearer mapping is held in the Redis-backed **playground session record** keyed `t:{tenant_id}:pg:sess:{session_id}` as specified in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange) ("Session record backing store"), anchored on the per-tenant prefix convention in [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes). Logout (`POST /playground/auth/logout`), `user.invalidated` ([§11.4](11_policy-and-controls.md#114-user-invalidation)), idle timeout, and admin revocation all drive the same revocation path: `DEL` on the session-record key + `SET` on the per-bearer `pg:revoked:{jti}` key + `PUBLISH` on `t:{tenant_id}:pg:revocations`. Logout endpoints MUST NOT return `200` to the browser until the revocation writes have committed to Redis — otherwise a racing re-presentation of the cookie on a peer replica could be honored before the deny-list entry is observable. The authoritative per-request revocation check runs on every playground-origin request (identified by the `origin: "playground"` claim) against `pg:revoked:{jti}`; the in-process negative-cache warmed by pub/sub is a latency optimization, not a correctness boundary. The **logout propagation SLO** (P99 ≤ 500 ms across all gateway replicas) is pinned in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange) and measured via the `lenny_playground_session_revocation_propagation_seconds` histogram in [§27.8](#278-metrics).

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
  object-src 'none';
  media-src 'none';
  frame-ancestors 'none';
  base-uri 'self';
  form-action 'self'
```

`object-src 'none'` and `media-src 'none'` are explicit (rather than inherited from `default-src 'self'`) because several CSP-evaluator tools and some browser versions treat absent directives permissively when the page lacks `<object>` / `<video>` / `<audio>` elements by design — making the posture explicit avoids ambiguity and documents intent.

`frame-ancestors 'none'` prevents clickjacking. The gateway also sets `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin` on all playground responses.

---

### 27.8 Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lenny_playground_page_views_total` | Counter | `authMode` | Playground index loads |
| `lenny_playground_sessions_created_total` | Counter | `runtime` | Sessions initiated from the playground |
| `lenny_playground_ws_connect_total` | Counter | `outcome` | MCP WebSocket connections opened from the playground (success/failure) |
| `lenny_playground_session_revocations_total` | Counter | `reason` | Playground session-record revocations. `reason ∈ {user_logout, idle_timeout, admin_revoke, oidc_session_ended, user_invalidated}`. Incremented exactly once per `DEL t:{tenant_id}:pg:sess:{session_id}` performed by the revocation path in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange) ("Session record backing store"). |
| `lenny_playground_session_revocation_propagation_seconds` | Histogram | `outcome` | End-to-end propagation latency from when a revocation is written on the originating replica to when peer replicas observe it on their auth hot path (authoritative Redis `GET` and/or pub/sub-warmed negative cache). `outcome ∈ {pubsub_delivered, redis_authoritative, resubscribe}`. P99 alert threshold is the 500 ms logout propagation SLO defined in [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange). Histogram buckets SHOULD span `[5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500 ms, 1 s, 2.5 s]` to bracket the SLO. |
| `lenny_playground_dev_tenant_not_seeded_total` | Counter | (none) | `/playground/*` requests rejected with `503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED` because the `authMode=dev` configured `devTenantId` was not yet present in Postgres when the request arrived (see [§27.3](#273-authentication) Ready-gate, [§17.6](17_deployment-topology.md#176-packaging-and-installation) `lenny-bootstrap` Job ordering). This counter should be non-zero only during the post-install bootstrap window; a sustained non-zero rate after bootstrap completion indicates the Job failed or the configured tenant was deleted post-install. Absent label set because the counter exists to surface a bootstrap/operational anomaly, not a per-tenant workload signal. |

Playground request metrics are otherwise the same as other gateway requests; the `origin=playground` session label is the primary way to slice dashboards.

---

### 27.9 Security considerations

- The playground's runtime and policy visibility is limited to what the caller is already authorized to see via the admin API — no elevated scope is granted to the UI.
- The raw-frame inspector displays redacted frames only; the gateway applies the same redaction rules as the audit log ([§16.4](16_observability.md)) before sending frames to the browser.
- File uploads for workspace plan tarballs go through the same size and schema checks as REST-initiated plans ([§14](14_workspace-plan-schema.md)); no client-side trust.
- When `playground.authMode=dev`, the playground UI renders a persistent red banner "DEV MODE — NOT FOR PRODUCTION" sourced from the gateway (so operators cannot easily remove it by swapping the bundle).
- When `playground.authMode=apiKey`, the playground UI renders a persistent, server-sourced yellow banner "API KEY MODE — paste only operator-issued tokens" with a link to the auth-mode documentation explaining when to use `authMode=oidc` instead. The banner text is emitted by the gateway (not the embedded bundle), so swapping or patching the asset bundle does not suppress it. This is the user-visible counterpart to the paste-form phishing surface described in the next bullet.
- `apiKey` mode ships a bearer-token paste form as its primary UX — operationally similar to asking a human user to paste a production credential into a web form, which is a well-known phishing vector. Operators SHOULD prefer `authMode=oidc` for human-user access; `apiKey` is intended for operator-driven workflows (smoke-tests, runtime-author headless flows) where pasting a service-account token is acceptable. An operator who ships `playground.enabled=true` with `playground.authMode=apiKey` outside dev mode MUST acknowledge the posture via the `playground.acknowledgeApiKeyMode` Helm value; the install-time audit is performed by the `playground.apiKeyMode` row of the `lenny-preflight` Job ([§17.6](17_deployment-topology.md#176-packaging-and-installation)), which emits a non-blocking `WARNING` unless the acknowledgement is set (same pattern as `monitoring.acknowledgeNoPrometheus`, [§25.4](25_agent-operability.md#254-the-lenny-ops-service)). The gateway does not gate startup on the acknowledgement — preflight is the single install-time touchpoint.
- The "Copy as client SDK snippet" feature generates code that never includes credentials; snippets reference environment variables / OIDC flow only.

---

### 27.10 Roll-forward notes

The playground is additive: disabling `playground.enabled` is safe at any time and has no effect on in-flight non-playground sessions. Playground-initiated sessions already in flight continue to run to completion or their configured cap; only new playground sessions are blocked.

Future work (not in scope of this section): a richer workspace editor and runtime authoring flows that let authors test a draft runtime manifest before publishing.
