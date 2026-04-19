# Web Playground Security Review — Findings

### WPP-001 OIDC Cookie-to-Bearer Token Exchange Mechanism Undefined [HIGH]
**Files:** `27_web-playground.md:47`, `10_gateway-internals.md` (auth sections), `15_external-api-surface.md` (REST endpoints)

The spec states the gateway "exchanges the cookie for MCP WebSocket bearer tokens on the user's behalf" but provides **no implementation detail** on:
- How the gateway extracts the ID token from the HttpOnly cookie
- How the exchange is initiated (what endpoint? what request flow?)
- Whether the exchange result (bearer token) is cached or issued on-demand per WebSocket connection
- Whether multiple concurrent WebSocket connections share a single bearer token or get independent tokens
- Token TTL and rotation policy for playground-issued bearer tokens
- Cross-section consistency: §10.2 (auth) and §15 (API surface) do not mention playground-specific auth flows

**Recommendation:** Document the complete OIDC-to-bearer exchange flow:
1. Specify the endpoint (`POST /v1/playground/token` or equivalent) that performs the exchange
2. Define request format (bearer cookie only? Include session metadata?)
3. Define response (bearer token, TTL, refresh semantics)
4. Clarify if bearer tokens are single-use per WebSocket or reusable across multiple connections
5. Cross-reference this mechanism in §10.2 and §15.1

---

### WPP-002 Cookie Path Scope Insufficient Against Path Traversal [MEDIUM]
**Files:** `27_web-playground.md:47`

The spec says the ID token cookie is "scoped to `/playground`" but does not specify the `Path=/` attribute in the Set-Cookie header. Without explicit path specification, browser behavior defaults to the path of the request that set the cookie (likely `/playground` or `/playground/`). However:
- If the cookie omits `Path=/`, browsers may permit access to paths like `/playgroundx/` or `/playground-other/` depending on browser path-matching logic
- Cross-section risk: if future endpoints like `/playground-admin` or `/playground/proxy` are added, the cookie scope becomes ambiguous

**Recommendation:** Explicitly specify in §27.3:
"The ID token cookie is set with `Path=/playground/` (exact path boundary) to prevent leakage to sibling or parent paths. The gateway sets the cookie header as: `Set-Cookie: lenny_playground_session=<token>; Path=/playground/; HttpOnly; Secure; SameSite=Strict; Max-Age=<TTL>`"

---

### WPP-003 CSP Missing Object and Media Sources Directives [LOW]
**Files:** `27_web-playground.md:96–103`

The CSP policy does not include `object-src` or `media-src` directives. While these are often redundant (defaulting to `default-src`), explicit inclusion improves clarity and defense-in-depth:
- `object-src 'none'` blocks `<object>`, `<embed>`, `<applet>` (plugin-based attacks)
- `media-src 'none'` blocks `<audio>`, `<video>` with external sources

**Recommendation:** Augment the CSP to:
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

---

### WPP-004 Session Cleanup on Playground Close Relies on Best-Effort Hint [MEDIUM]
**Files:** `27_web-playground.md:84`, `07_session-lifecycle.md` (idle timeout mechanics), `06_warm-pod-model.md:261`

The spec states the playground sends `session.cancel` on browser close but "Gateway treats this as a best-effort hint; a dropped WebSocket that cannot send the frame falls back to the standard idle-timeout path." Cross-referencing §6.2 shows the default idle timeout is **600 seconds (10 minutes)**, not 30 minutes (the playground's max session duration). This creates a **15-minute gap** where:
- A user closes the browser/navigates away
- The cancel frame is lost or not sent
- The session remains active, consuming pod resources, for up to 10 minutes before idle timeout fires
- The hard duration cap (`playground.maxSessionMinutes = 30`) provides final safety but is much larger than the idle timeout

**Recommendation:**
1. Define a playground-specific max idle timeout (recommend: 5 minutes) to match the intended UX lifetime: "Playground sessions MUST NOT remain idle for longer than 5 minutes. The gateway enforces `playground.maxIdleTimeSeconds = 300` (5 min) as a hard override of the runtime's `maxIdleTimeSeconds` for all playground-initiated sessions."
2. Alternatively, explicitly document the acceptable resource loss window and link to the capacity planning section for operators.
3. Add a metric `lenny_playground_session_ungraceful_close_total` to track sessions that idle-timeout rather than receiving a cancel frame.

---

## Summary

Four findings identified. **High severity:** OIDC token exchange mechanism is underspecified, creating ambiguity in auth flow design. **Medium severity:** Cookie path scope lacks explicit bounds; session cleanup relies on fallback to long (10-minute) idle timeout. **Low severity:** CSP policy incomplete (missing explicit object/media directives).

All issues are cross-section inconsistencies or security/clarity gaps — no fundamental design flaw, but implementation risk if not clarified before coding.
