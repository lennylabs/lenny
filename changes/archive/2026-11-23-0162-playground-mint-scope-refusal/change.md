# The playground mint refuses a subject scope it cannot narrow

Status: `Implemented`, revision 1, 2026-11-23.

## Problem

`POST /v1/playground/token` mints a session-capability JWT whose `scope` claim is the intersection of the subject's scope and `playground_allowed_scope`. In two cases the mint writes an empty `scope` claim, and an empty claim is the §25.1 absent claim, which grants every scope up to the role ceiling. The mint therefore issues a bearer broader than its subject held.

Evidence from the tree:

- `pkg/gateway/playground/token.go`, `intersectScope`: when `scopes.Parse` refuses the subject's scope, the branch commented "Fail closed" returns the empty string. When the subject's scope parses and shares no value with `playground_allowed_scope`, `Set.Intersect` returns its empty-intersection sentinel, and `intersectScope` translates the sentinel back to the empty string. `completeMint` writes the result to the minted JWT's `scope` claim and to `effectiveScope`.
- `pkg/common/scopes/scopes.go`, `Set.Present`: a set whose raw text is empty is not present, and `Set.Matches` returns true for every required scope on a set that is not present.
- `pkg/gateway/api/admin/scope_enforce.go`, `scopeGate.ServeHTTP`: the admin scope gate refuses a request only when the caller's scope is present and does not match, so a bearer with an empty `scope` claim passes the scope gate on every route and is limited by its roles alone.
- `completeMint` copies the subject's `roles` and `groups` onto the minted JWT.

A bearer deliberately narrowed to `tools:health:read`, pasted into the `apiKey` form, is exchanged for a playground bearer with the subject's roles and no scope limit. An identity provider token whose `scope` claim is a raw OAuth value such as `openid profile`, which the §25.1 parser refuses, takes the same path in `apiKey` mode, and in `oidc` mode when the provider puts that claim in the ID token.

The specification states the empty-scope mint as intended. §10.2 (Authentication), **Playground mint invariants**, invariant 2 says that the mint proceeds with an empty `scope` claim when the intersection is empty, and the **Playground mint failure modes** table has a row with status 200 that calls the case "not an error path". §27.3.1 (OIDC cookie-to-MCP-bearer exchange) says that `effectiveScope` is the empty string when the intersection is empty. The defect was found by the security review of the branch `fix/branch-only-findings` and raised as question S-2 of the M3 readiness review.

## Scope

The change covers the scope-narrowing invariant of the playground mint for every mint the endpoint serves in the `apiKey` and `oidc` modes, including the silent refresh, which calls the same endpoint. It edits the invariant, the failure-modes table, the §15.1 row of `POST /v1/playground/token`, the `SCOPE_FORBIDDEN` row of the §15.1 **Error code catalog**, and §27.3.1.

It leaves out:

- The `dev` mode. Its synthetic subject carries `playground_allowed_scope` itself, so its intersection is never empty.
- A subject with no `scope` claim. §25.1 reads an absent claim as no restriction, and `Set.Intersect` returns the other operand, so the mint already writes `playground_allowed_scope` for such a subject. The change keeps that result and states it in invariant 2.
- The `oidc` callback. The change refuses at the mint and does not refuse the login of a user whose ID token carries an unparseable `scope` claim.
- The playground UI. `mintBearer` in `pkg/gateway/playground/ui/app.js` redirects to the login page only on a 401 whose `details.reason` is absent or `playground_session_expired`, and surfaces every other mint failure as an error, so both refusals surface as errors without a code change.

## Terms

| Term | Meaning |
| :-- | :-- |
| Subject | The principal the mint narrows from: the pasted bearer in `apiKey` mode, or the validated OIDC principal of the session record in `oidc` mode (§10.2, **Playground mint invariants**). |
| `playground_allowed_scope` | The fixed set of scopes a playground bearer may carry, defined in §25.1 (**Playground-allowed scope set.**) and held in `playgroundAllowedScope` in `pkg/gateway/playground/token.go`. |
| Absent claim | A `scope` claim that is missing. §25.1 reads it as no scope restriction, and the mint and the auth chain read an empty or whitespace-only claim the same way. |
| Disjoint subject scope | A subject `scope` claim that parses and whose intersection with `playground_allowed_scope` holds no value. |
| Unparseable subject scope | A subject `scope` claim with a value the §25.1 canonical form refuses, such as `openid` or `bogusdomain:read`. |

## Requirements

None by identifier. The change edits §10.2 (Authentication), §15.1 (REST API), and §27.3.1 (OIDC cookie-to-MCP-bearer exchange), which are content of the placeholder pages AUTHN, PLAY, and others that have no requirement identifiers yet.

## Detailed design

### MS-1 Refusals

The mint computes the narrowed scope before it checks the signer, signs, or writes the session record:

- An unparseable subject scope refuses the mint with `401 UNAUTHORIZED` and `details.reason: "subject_scope_invalid"`.
- A disjoint subject scope refuses the mint with `403 SCOPE_FORBIDDEN`, with `details.activeScope` carrying the subject's `scope` claim and `details.requiredScope` carrying `playground_allowed_scope` as a space-separated list, any one value of which admits the mint.
- A subject with an absent claim receives `playground_allowed_scope`, and any other subject receives the non-empty intersection, as today.

Each refusal emits `playground.bearer_mint_rejected` and increments `lenny_playground_bearer_mint_rejected_total`, with the invariant identifiers `subject_scope_invalid` and `scope_intersection_empty`. A refused mint signs nothing, writes no session record, and emits no `playground.bearer_minted` event.

Both refusals use codes the §15.1 **Error code catalog** defines. `UNAUTHORIZED` carries the playground's other 401 refusals with a `details.reason`, and `SCOPE_FORBIDDEN` is the catalog's code for a refusal caused by the caller's `scope` claim alone.

### MS-2 Specification text

- §10.2, invariant 2: the mint writes the intersection, `playground_allowed_scope` for a subject with an absent claim, and refuses the two cases of MS-1; the minted `scope` claim is never empty.
- §10.2, **Playground mint failure modes**: the row with status 200 is replaced by two refusal rows.
- §15.1, the `POST /v1/playground/token` row: the refusals are listed.
- §15.1, the `SCOPE_FORBIDDEN` row of the **Error code catalog**: the playground mint's use of `details.requiredScope` is stated.
- §27.3.1: the sentence on an empty `effectiveScope` is replaced by the statement that the field is never empty, and the **Failure modes summary** table gains the two refusals.

## Specification and non-specification changes

- `spec/10_gateway-internals.md`: §10.2 Authentication, **Playground mint invariants** and **Playground mint failure modes**.
- `spec/15_external-api-surface.md`: §15.1, the `POST /v1/playground/token` row and the `SCOPE_FORBIDDEN` row of the **Error code catalog**.
- `spec/27_web-playground.md`: §27.3.1, **Bearer token exchange** and **Failure modes summary**.
- `pkg/gateway/playground`: `token.go`.
- `tests/registers/spec-section-citations.yaml`: the section-citation counts of the packages whose annotations change.

## Documentation

- `docs/operator-guide/web-playground.md` (operator): the authentication modes section states how the mint narrows the subject's scope and when it refuses, so an operator whose identity provider puts a raw OAuth `scope` claim in its tokens knows the mint refuses it.
- `docs/reference/error-catalog.md` (client developer, operator): the policy errors gain a `SCOPE_FORBIDDEN` row, which the page lacked, naming the playground mint's use of it. `UNAUTHORIZED` keeps its generic row.

## Tests

Unit tier, in `pkg/gateway/playground`:

- `intersectScope` returns the narrowed scope for a subject that overlaps the allowed set, the allowed set for a subject whose scope is exactly the allowed set and for an absent claim, the refusal for an unparseable subject scope, and the refusal for a disjoint subject scope. Cites §10.2 (Authentication) and §25.1.
- `POST /v1/playground/token` in `apiKey` mode refuses an unparseable subject scope with 401 `UNAUTHORIZED` and `details.reason: subject_scope_invalid`, and a disjoint subject scope with 403 `SCOPE_FORBIDDEN` and the two details, emits one `playground.bearer_mint_rejected` event with the invariant identifier, and signs nothing; it mints with the intersection when the subject overlaps the allowed set, and with the allowed set when the subject's scope is exactly that set. Cites §10.2 (Authentication).
- `POST /v1/playground/token` in `oidc` mode refuses a session record whose scope is unparseable or disjoint with the same statuses and codes, and leaves the record's tracked bearer identifiers unchanged. Cites §10.2 (Authentication) and §27.3.1.

Contract tier, in `tests/contract/playground`: over HTTP against the playground token routes, a pasted bearer narrowed to `tools:health:read` is refused with 403 `SCOPE_FORBIDDEN`, and a pasted bearer with the scope `openid profile` is refused with 401 `UNAUTHORIZED`. Cites §10.2 (Authentication).
