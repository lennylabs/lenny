# Iter5 Review — Perspective 8: Multi-Tenancy & Tenant Isolation

Scope: verify iter4 TNT-008 through TNT-013 against current spec, then scan
`spec/11_policy-and-controls.md`, `spec/12_storage-architecture.md` (tenant
isolation sections), `spec/13_security-model.md` (RLS section), and the
playground / ops-inventory surfaces for concrete NEW isolation bypasses or RBAC
gaps. Severity calibrated against iter4 rubric per
`feedback_severity_calibration_iter5.md`: theoretical "could be stronger"
without a demonstrable bypass = Low.

Iter4 numbering ran to TNT-013. New iter5 findings start at TNT-014.

---

## Iter4 carryover verification

### TNT-008 (Medium, iter4) — Fixed (verified)

Subject-token-type restriction and scope-narrowing invariants are now
normative in `spec/10_gateway-internals.md` §10.2 lines 234–243 (**"Playground
mint invariants"** block) and cross-referenced from `spec/27_web-playground.md`
§27.3:60 and §27.3:63–66:

- Invariant (1) requires `subject_token.typ == user_bearer`; rejects
  `session_capability` / `a2a_delegation` / `service_token` with
  `401 LENNY_PLAYGROUND_BEARER_TYPE_REJECTED` (§15.1:1077).
- Invariant (2) pins `minted.scope = intersection(subject.scope,
  playground_allowed_scope)` with an explicit static scope set for v1.
- Invariants (3)–(5) pin tenant preservation, duration cap, and
  caller-type / role preservation.
- Test matrix row in §10.2:249 enforces the "capability JWT pasted into
  playground produces 401" case the iter4 recommendation asked for.
- Rejected mints emit `playground.bearer_mint_rejected` audit event
  (§10.2:243) and increment
  `lenny_playground_bearer_mint_rejected_total{reason}`.

Fix held.

### TNT-009 (Medium, iter4) — Fixed (verified)

`spec/27_web-playground.md` §27.3.1:100–108 adds a dedicated **"Tenant-claim
rejection codes (OIDC callback)"** table mirroring §10.2's extraction
semantics, covering `TENANT_CLAIM_MISSING`, `TENANT_NOT_FOUND`,
`TENANT_CLAIM_INVALID_FORMAT`, each with HTTP status, query-param code
(`?error=tenant_claim_missing`, etc.), and log-attribution rule
(`tenant_id=__unset__` for missing/invalid-format). §10.2 remains
authoritative; the §27.3.1 table is explicitly a cross-reference. Fix held.

### TNT-010 (Medium, iter4) — Fixed (verified)

`spec/27_web-playground.md` §27.2:46–51 now defines a four-layer validation
stack:

1. Helm `values.schema.json` with `pattern: ^[a-zA-Z0-9_-]{1,128}$` (primary).
2. `lenny-preflight` row for cross-field conditionals (primary).
3. Gateway startup codes `LENNY_PLAYGROUND_DEV_TENANT_INVALID` /
   `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` (backstop; format/cross-field only).
4. Per-request Ready-gate on `/playground/*` for tenant-existence
   (`503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED`, `Retry-After: 5`).

`playground.devTenantId` is added to the §10.3 required-keys table per the
iter4 WPP-010 fix record. Layers 1 and 2 are described as the **primary**
defenses, with startup gates as backstops — matching the `noEnvironmentPolicy`
posture the iter4 finding cited. Fix held.

### TNT-011 (Medium, iter4) — Fixed (verified)

`spec/27_web-playground.md` §27.3.1:84–98 now specifies the playground
OIDC session record backing store explicitly:

- Keys: `t:{tenant_id}:pg:sess:{session_id}` (envelope) and
  `t:{tenant_id}:pg:revoked:{jti}` (presence-only marker), both on the
  per-tenant prefix convention from §12.4.
- TTLs: session record pinned to `oidcSessionTtlSeconds − elapsed`;
  revocation marker pinned to `exp − now + 5s` skew budget.
- Per-request revocation check on the auth hot path (401 with
  `details.reason: "bearer_revoked"` on REST/MCP; WebSocket close `4401`).
- Pub/sub propagation on `t:{tenant_id}:pg:revocations` with a 500ms P99
  cross-replica SLO and bounded LRU negative cache on each replica.
- Redis unavailability fails closed (`503 REDIS_UNAVAILABLE`).
- Integration tests `TestPlaygroundSessionRevocationCrossReplica` and an
  extension of `TestRedisTenantKeyIsolation` are pinned.

The multi-replica logout gap the iter4 finding flagged is closed with a
shared store, a documented SLO, and a fail-closed posture on Redis
degradation. Fix held.

### TNT-012 (Low, iter4) — NOT Fixed (carry forward to TNT-014)

The ambiguous cross-tenant `?tenantId=` outcome for non-`platform-admin`
roles remains unresolved. See TNT-014 below for the carry-forward.

### TNT-013 (Low, iter4) — Substantially addressed (carry forward partial as Low — see TNT-015)

The WPP-011 fix in iter4 added:

- A persistent, server-rendered yellow "API KEY MODE — paste only
  operator-issued tokens" banner (§27.9:254) that cannot be suppressed by
  swapping the client bundle.
- A non-blocking `lenny-preflight` WARNING gated by
  `playground.acknowledgeApiKeyMode` (§27.2:42; §17.6 preflight table).
- The `playground.bearer_mint_rejected` audit event (§10.2:243) covering
  rejected pastes.

The core "credential misdelivery to operator-owned log sink" concern is
substantially mitigated: operators who enable `apiKey` in non-dev mode
without acknowledgement get flagged at install time, and end users see a
server-rendered warning banner. Residual gaps (UI label rename, explicit
bearer redaction rule in auth-failure logs) are doc-polish — see TNT-015
for the narrower carry-forward.

---

## New iter5 findings

### TNT-014. Non-`platform-admin` `?tenantId=` mismatch outcome remains undefined on list endpoints [Low]

**Section:** `spec/25_agent-operability.md` §25.4 (operations inventory,
lines 1769, 1806–1810), §25 ("Filter Parameter Naming" table, line 340);
`spec/10_gateway-internals.md` §10.2:296 (tenant-scoped admin API);
`spec/15_external-api-surface.md` §15.1:805 (`POST
/v1/admin/credential-pools` and sibling list endpoints that advertise
`?tenant_id=` for `platform-admin` only).

This is a carry-forward of iter4 TNT-012. The iter4 finding was Low. The
spec surfaces still converge on three prose statements rather than one
normative rule, and none of them specify the HTTP status code when a
non-platform-admin passes `?tenantId=OTHER`:

- §25.4:1769 — `tenantId` filter "auto-restricted to its own tenant" for
  `tenant-admin` (silent substitution reading).
- §25.4:1808 — tenant-admin "sees only operations where [...] the
  operation carries a `tenantId` field AND its value matches the caller's
  tenant" (silent empty-result reading).
- §10.2:296 — tenant-scoped admin API "only return data belonging to the
  caller's tenant" (no explicit outcome for a mismatched query param).
- §15.1:805 — `?tenant_id=` advertised as platform-admin-only (explicit
  scoping) but without a rejection code when a tenant-admin sends it.

All three readings are defensible. None is pinned. Automation that
attempts a cross-tenant query on behalf of a tenant-admin can therefore
receive (i) silently-substituted own-tenant data, (ii) an empty list
with no diagnostic signal, or (iii) a 403 — depending on the
implementer's choice at each list endpoint, with no conformance test to
anchor the behavior.

Severity: Low per iter4 anchoring. The RLS layer is fully closed (§4.2
lines 163, 165; §12.3 lines 49–57 `lenny_tenant_guard`) so the concrete
cross-tenant **data-leak** path is blocked at the database regardless of
how the gateway resolves the filter param. This is a confused-deputy /
UX-clarity concern, not a demonstrated bypass.

**Recommendation:** In §25.4 (ops inventory authorization block, line
1808) and §10.2:296 (tenant-scoped admin API), add a single normative
row stating that any non-`platform-admin` role passing a `tenantId`
query parameter whose value does not equal the role's scoped
`tenant_id` claim returns `403 AUTH_CROSS_TENANT_FORBIDDEN` — never
silently substituted, never returned as empty. Add the code to §15.1 as
`AUTH_CROSS_TENANT_FORBIDDEN` (`POLICY`, 403, non-retryable,
`details.requestedTenantId` and `details.callerTenantId`). Add
`authz_cross_tenant_attempts_total{role, endpoint}` to §16.1 and an
audit event `authz.cross_tenant_attempt` in §11.7 so the behaviour is
observable. The same rule applies uniformly to every list endpoint that
accepts a `tenantId` filter (ops inventory, sessions, pods,
audit-events, credential-pools, events, metering, usage).

### TNT-015. `apiKey`-mode auth-failure logs lack a bearer-redaction rule; misdelivered vendor credentials can land in operator log sinks [Low]

**Section:** `spec/27_web-playground.md` §27.3 (apiKey mode, line 60),
§27.9 (security considerations, line 255); `spec/10_gateway-internals.md`
§10.2 (Playground mint invariants block at line 243, audit event
`playground.bearer_mint_rejected`); `spec/16_observability.md` (log
attribution / redaction guidance).

Partial carry-forward of iter4 TNT-013. The iter4 banner + preflight +
acknowledgement fixes (WPP-011) close the primary operator-visible
concern. What remains: when a user pastes a non-Lenny vendor credential
(`sk-...`, `sk-ant-...`, GitHub PAT, etc.) into the `apiKey` mode form,
the gateway's standard auth chain rejects it with
`TENANT_CLAIM_MISSING` (correct). The rejected bearer's raw string is
logged unless the gateway's structured-logging wrapper has a
bearer-redaction rule — but the spec does not pin one for the playground
path. `playground.bearer_mint_rejected` (§10.2:243) captures
`subject_jti` and `subject_typ`, not the raw material, so the audit
event is safe by construction; the risk is in the surrounding auth-chain
log lines (the §10.2 extraction table rejections at
`TENANT_CLAIM_MISSING` / `TENANT_CLAIM_INVALID_FORMAT`) which still
flow through the generic auth-failure logger with whatever fields it
chooses.

In SaaS deployments where the log sink is operator-owned, a tenant's
mispasted vendor credential therefore becomes visible to the platform
operator unless the logger independently redacts the `Authorization`
header — a property the spec currently leaves implicit.

Severity: Low. The banner and the acknowledgement preflight (iter4
WPP-011) substantially reduce the incidence, and the receiving surface
is the operator's own log sink (not a cross-tenant leak). This is a
defense-in-depth finding, not a demonstrated isolation bypass.

**Recommendation:** In §27.9 add a bullet stating that the gateway MUST
redact any value matching `Authorization: Bearer \S+` from auth-failure
log lines emitted on the `/playground/*` path (and, for consistency, on
every auth-chain rejection path) to `Authorization: Bearer
sha256(<12-hex-chars>)…` — the 12-char prefix matches the existing
`jti` truncation used elsewhere. Reference this rule from §16.4
alongside the existing PII-redaction guidance. Optionally, rename the
UI label from "API key" to "Lenny bearer token (JWT)" with placeholder
`eyJ…` (§27.3:60) to reduce the misdelivery incidence further; the
`apiKey` mode identifier and Helm value may remain for backward
compatibility.

---

## Convergence assessment

Four of the six iter4 TNT items (TNT-008, TNT-009, TNT-010, TNT-011) are
**Fixed** with the recommended invariants, tables, layering, and backing
store spelled out and cross-referenced. Two remain:

- TNT-014 (carry-forward of TNT-012, Low) — unchanged since iter4; the
  cross-tenant `?tenantId=` outcome is still ambiguous across three
  adjacent prose locations.
- TNT-015 (narrowed carry-forward of TNT-013, Low) — the iter4 WPP-011
  fix closed the primary concern; the residual is a bearer-redaction
  rule on auth-failure logs that the spec leaves implicit.

Both remaining findings are Low (defense-in-depth / UX clarity) with no
concrete cross-tenant bypass path. The RLS-based isolation stack
(§4.2:163–165, §12.3:49–57 `lenny_tenant_guard`, §12.4:177–195
per-tenant Redis prefixes, §13.3 OAuth token-exchange invariants) is
fully closed against demonstrated bypasses.

**Iter5 Perspective 8 status: converged.** No Critical / High / Medium
multi-tenancy findings remain. The two Low items can be addressed in a
doc-polish pass or deferred without blocking.
