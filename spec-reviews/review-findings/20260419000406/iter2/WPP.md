# Web Playground Review — Iteration 2

**Scope:** `spec/27_web-playground.md` (+ cross-section consistency with §6.2, §10.2, §11, §15.1).
**iter1 status:** WPP-001, WPP-002, WPP-004 confirmed fixed. WPP-003 (CSP `object-src`/`media-src`) was not applied — re-raised as WPP-007.

---

### WPP-005 Idle-timeout override and duration cap do not bind `apiKey` / `dev` playground sessions [HIGH]

**Files:** `27_web-playground.md:37,49–50,144–146`; `06_warm-pod-model.md:261`.

§27.6 enforces the 5-min idle override "whenever the session was established through the playground bearer-exchange path (detected via the `origin: "playground"` JWT claim minted in §27.3.1)." §27.3.1 is explicitly `oidc`-only (step 1 heading "Login and cookie issuance (`playground.authMode=oidc` only)"). The `origin: "playground"` claim is therefore minted **only** in OIDC mode.

In `apiKey` mode (§27.3:49) the browser presents a user-supplied API key on every request; the session JWT produced flows through the standard gateway auth chain (§10.2), which does not stamp `origin: "playground"`. `dev` mode mints a dev-grade token with no auth, again without the claim. Consequences:

1. **Idle override never fires** for `apiKey` / `dev` sessions — they inherit the runtime default `maxIdleTimeSeconds` (600s per §6.2 / §17.8), not 300s. The iter1 WPP-004 fix is silently mode-scoped.
2. **`playground.maxSessionMinutes = 30` cap** (§27.6 bullet 1) has no defined enforcement path keyed off the `origin` claim either — §27.2 wording says "playground-initiated sessions", but the only detector the spec provides is the claim. Same gap.
3. **§27.8 dashboard slice** — "`origin=playground` session label is the primary way to slice dashboards" — also misses `apiKey` / `dev` sessions unless the label is applied by a path the spec does not document for non-OIDC modes.

This is a cross-section gap introduced by iter1 WPP-004's fix, not a fresh design issue.

**Recommendation.** Pick one and apply consistently:
- **Preferred:** mint the `origin: "playground"` claim on **every** session token produced for a `/playground/*`-originated request, regardless of `authMode`. The `/playground/*` handler attaches the claim to the downstream mint (apiKey: after API-key validation; dev: on the dev HMAC token). Then §27.6's override, §27.2's duration cap, and §27.8's label slice work uniformly.
- **Alternative:** key the override and label on the request-ingress path (a `session.origin_playground` flag set at session-create time from the `/playground/*` route) rather than the JWT claim. Update §27.6 and §6.2:261 to cite the ingress signal.

Update §27.3 to state which modes carry the claim/label and under what mechanism; align §27.6 text with that coverage.

---

### WPP-006 `playground.oidcSessionTtlSeconds` and `playground.bearerTtlSeconds` absent from §27.2 Helm-values table [MEDIUM]

**Files:** `27_web-playground.md:31–38, 63, 80`.

§27.3.1 introduces two configurable Helm values in prose only:
- `playground.oidcSessionTtlSeconds` (default `3600`, line 63) — cookie/session-record lifetime.
- `playground.bearerTtlSeconds` (default `900`, bounded `60 ≤ ttl ≤ 3600`, line 80) — bearer-token TTL.

Neither appears in §27.2's consolidated `playground.*` Helm-values table — the only concentrated reference for operators writing `values.yaml`. The `bearerTtlSeconds` bound is especially easy to miss when it's buried in paragraph prose rather than tabulated.

**Recommendation.** Add two rows to §27.2:

| Helm value | Default | Effect |
|---|---|---|
| `playground.oidcSessionTtlSeconds` | `3600` | Lifetime of the server-side playground session record and the `lenny_playground_session` cookie. See [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange). |
| `playground.bearerTtlSeconds` | `900` | TTL of MCP bearer tokens minted by `POST /v1/playground/token` (bounded `60 ≤ ttl ≤ 3600`). See [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange). |

---

### WPP-007 CSP still missing `object-src` and `media-src` directives [LOW, iter1 WPP-003 carryover]

**Files:** `27_web-playground.md:155–166`.

iter1 WPP-003 recommended explicit `object-src 'none'; media-src 'none'`. The §27.7 CSP block still lacks both. Low severity — they inherit from `default-src 'self'` — but the iter1 fix was not applied. Re-raised for completeness.

**Recommendation.** Add `object-src 'none';` and `media-src 'none';` to the CSP template at §27.7:158–166.

---

## Summary

**High:** §27.6 idle-timeout override (iter1 WPP-004 fix) only binds OIDC-mode sessions because it keys off a claim minted only by §27.3.1's OIDC-only path; `apiKey` / `dev` modes silently retain the 600s runtime default and likely the 30-min `maxSessionMinutes` cap too. **Medium:** `oidcSessionTtlSeconds` and `bearerTtlSeconds` knobs referenced in §27.3.1 prose are missing from §27.2's consolidated Helm-values table. **Low:** iter1 WPP-003's CSP recommendation was not applied.

The §27.6 ↔ §6.2 cross-reference for the idle-timer override is internally consistent on the OIDC path; the `origin: "playground"` claim is referenced in §6.2:261, §10.2 (playground paragraph), §27.3.1, §27.6, and the audit-event payload at §27.3.1 step 6 — no orphan references. The gap uncovered here is about the breadth of the claim's issuance, not its reference graph.
