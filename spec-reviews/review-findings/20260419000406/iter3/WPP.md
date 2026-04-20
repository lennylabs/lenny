# Iter3 WPP Review

**Scope:** `spec/27_web-playground.md` (primary) + cross-section consistency with §6.2, §7.2, §10.2, §11, §15.1, §17.4.
**iter2 regression check:**
- **WPP-005 (mode-agnostic claim):** Applied cleanly. §27.3:54–59 adds the "Mode-agnostic `origin: "playground"` JWT claim" paragraph; §27.3.1:67 scopes the subsection to OIDC-only while forwarding the claim mechanism to §27.3; §27.6:153–156 keys both duration-cap and idle-override off the JWT claim for all three modes; §10.2:177–183 mirrors the per-mode mint points; §6.2:265 cites `/playground/*` ingress path under all three `authMode` values. Confirmed fixed.
- **WPP-006 (Helm values):** Applied. §27.2:38–39 now tabulates `playground.oidcSessionTtlSeconds` (3600) and `playground.bearerTtlSeconds` (900, bounded 60–3600) with cross-refs back to §27.3.1. Confirmed fixed.
- **WPP-007 (CSP `object-src` / `media-src`):** Applied. §27.7:172–173 adds both directives to the CSP template; §27.7:179 adds explicit rationale for why they are not inherited from `default-src`. Confirmed fixed.

Anchor audit: `#273-authentication`, `#276-session-lifecycle-and-cleanup`, `#278-metrics`, `#2731-oidc-cookie-to-mcp-bearer-exchange`, `#102-authentication`, `#72-interactive-session-model`, `#174-local-development-mode-lenny-dev` all resolve to extant sections. OPS-004 Tier-0 rename to "Embedded Mode" propagated to §27.2:42.

Two new findings identified — both inherited/longstanding gaps surfaced (or widened) by iter2's mode-agnostic rewrite rather than fresh regressions. One PARTIAL on §27.6.

---

### WPP-008 `apiKey` mode invokes "standard API-key auth path" that §10.2 does not document [MEDIUM]

**Files:** `27_web-playground.md:51,56`; `10_gateway-internals.md:140–146,180`.

§27.3:56 and §10.2:180 both direct the `apiKey` playground flow through "the standard API-key auth path" as the validation step before minting the session JWT. But the §10.2 auth-boundary table (lines 140–146) enumerates only two client-facing mechanisms: OIDC/OAuth 2.1 for clients, and service-to-service (client-credentials grant) for automated clients. There is no documented "API-key auth path" for end users — no key format, no storage (DB / KMS / hashed?), no rotation semantics, no binding between API key and `user_id` / `tenant_id` / `scope` claims, no revocation primitive.

This gap was survivable in iter1 when `apiKey` mode was just a UI-side feature (key sent on every request, stored in `sessionStorage`). Iter2's WPP-005 fix made it load-bearing: the `/playground/*` handler is now required to **validate** the key and **mint a session-capability JWT** with `origin: "playground"` attached — an operation that needs a concrete key-validation subsystem (the same one the "standard" path would use) to exist. Consequences:

1. A runtime-author reading §27.3 has no way to know what `apiKey` mode actually requires of the installation (does the operator provision keys via `lenny-ctl`? a CRD? a Postgres table?).
2. The "claim is stamped by the ingress route, not by the key material" statement is prescriptive about *where* the claim is attached but silent on *what* the key material itself authenticates (the caller? an opaque identity? does the key carry its own `user_id`/`tenant_id`?).
3. `playground.authMode=apiKey` is advertised as a production-capable auth mode (only `dev` requires `global.devMode=true` — §27.3:52). An operator could legitimately ship this in production with nothing to back it.

This is not strictly within §27's scope to fix, but §27 is the only surface that references the path, so the gap is effectively playground-owned until §10.2 (or a new subsection) documents end-user API-key auth.

**Recommendation.** Pick one:
- **Preferred:** extend §10.2 with a third auth-boundary row — `End-user API key (tenant-scoped bearer, hashed-at-rest, revocable via admin API)` — and add a short subsection covering key format (`lenny_pat_<base62>`), storage (hashed table like gateway client credentials), provisioning (`lenny-ctl user api-keys create …`), claim mapping (key → `user_id` / `tenant_id` / `scope` at validation time), and revocation (`DELETE /v1/admin/users/{user_id}/api-keys/{key_id}`). Then §27.3:56 simply cites the new subsection.
- **Minimal:** if end-user API keys are not a v1-supported mechanism outside the playground, restrict `playground.authMode=apiKey` to `global.devMode=true` installations (matching `dev` mode's guardrail) and explicitly say so in §27.3:51 — removing the production-capable framing.

---

### WPP-009 §27.2 `playground.maxIdleTimeSeconds` bound `60 ≤ v ≤ runtime's maxIdleTimeSeconds` is not validatable at Helm install time [LOW]

**Files:** `27_web-playground.md:37,154`.

The §27.2 table entry for `playground.maxIdleTimeSeconds` specifies `bounded 60 ≤ v ≤ runtime's maxIdleTimeSeconds`. `playground.maxIdleTimeSeconds` is an installation-wide Helm value, but "runtime's `maxIdleTimeSeconds`" is a **per-runtime** field from `RuntimeDefinition.limits` (§11:186, §6.2:257) that varies across the installation's registered runtimes. There is no single upper bound at Helm-install time — a value of `600` might be in-bound for runtime A (whose limit is 900) but out-of-bound for runtime B (whose limit is 300).

§27.6:154 reconciles this at runtime by stating "the override never relaxes a stricter runtime limit, only tightens a looser one" — effective cap is `min(runtime.limits.maxIdleTimeSeconds, playground.maxIdleTimeSeconds)`. That's sane behavior, but it contradicts the table's static-bound wording. An operator reading §27.2 will expect Helm-validate to reject `playground.maxIdleTimeSeconds: 1800` when some runtime has `limits.maxIdleTimeSeconds: 600`, which is not feasible.

**Recommendation.** Reword §27.2:37's bound column to match §27.6's behavior:

> `300` | Cap on idle time for playground-initiated sessions. Helm-validate accepts any `v ≥ 60`. Effective idle cap at runtime is `min(runtime.limits.maxIdleTimeSeconds, v)` — the override only tightens a looser runtime limit; it never relaxes a stricter one. See [§27.6](#276-session-lifecycle-and-cleanup).

Delete the `v ≤ runtime's maxIdleTimeSeconds` clause from the bound; it implies an unrealizable install-time check.

---

### PARTIAL — §27.6 `session.cancel` frame name is not defined anywhere in the protocol

**Files:** `27_web-playground.md:155`; `09_mcp-integration.md` (no match); `07_session-lifecycle.md` (uses `DELETE /v1/sessions/{id}`); `15_external-api-surface.md:1029` (uses tool name `cancel_session`).

§27.6:155 says the playground client "sends `session.cancel` with reason `playground_client_closed`" on browser close. The identifier `session.cancel` appears **only** in this single sentence across the spec — §09's MCP tool catalog has `lenny/cancel_child`, §15's tool catalog has `cancel_session`, §07 uses `DELETE /v1/sessions/{id}`. No frame called `session.cancel` is defined; no reason-code enum is defined that would carry `playground_client_closed`.

This is a longstanding shorthand, not a fresh regression — but iter2's rework did not tidy it. Flagging as PARTIAL because the best-effort-cancel mechanic is essential to §27.6's idle-override design (it's the primary delivery; the 5-min idle override is the fallback), so the frame needs a canonical name and reason-code taxonomy.

**Recommendation.** Replace `session.cancel` in §27.6:155 with the documented mechanism — either (a) an MCP tool call: "the client invokes the `cancel_session` MCP tool (§15.6.1) with `cancelReason: "playground_client_closed"`" (and define the reason-code enum alongside the tool), or (b) if the browser-close path must be a WebSocket control frame rather than a tool call (to cover cases where the WS is still half-open but the page unloaded), define the frame explicitly in §09 with a name and payload schema and cross-reference it here.

---

## Summary

Two new findings:

- **Medium (WPP-008):** `apiKey` playground mode relies on a "standard API-key auth path" that §10.2 never documents; iter2's WPP-005 fix made this path load-bearing (it must now mint session JWTs with `origin: "playground"` attached), so the gap is no longer latent. Either document end-user API-key auth in §10.2 or restrict `apiKey` mode to dev installations.
- **Low (WPP-009):** §27.2 table claims `playground.maxIdleTimeSeconds` is bounded above by "runtime's `maxIdleTimeSeconds`" — an unenforceable install-time constraint since the cap is per-runtime. §27.6's `min()`-at-runtime wording is correct; §27.2 should be reworded to match.

**PARTIAL:** `session.cancel` frame name in §27.6 is spec shorthand — no such frame is defined in §09/§07/§15, where the documented cancel primitives are `cancel_session` (MCP tool) and `DELETE /v1/sessions/{id}` (REST). Not a regression from iter2, but still unreconciled.

iter2's three playground fixes (WPP-005 mode-agnostic claim, WPP-006 Helm values, WPP-007 CSP) all applied cleanly, with supporting changes in §6.2 and §10.2 kept in sync. No regressions detected in the OIDC flow, CSP posture, cookie scoping, or bearer-exchange semantics.
