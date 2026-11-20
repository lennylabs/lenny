# Decisions: 0162 The playground mint refuses a subject scope it cannot narrow

## Open decisions

None.

## Decisions

1. **What the playground mint does when it cannot narrow the subject's scope.** Settled by the owner on 2026-11-19, question S-2 of the M3 readiness review, choosing option (a): refuse. An unparseable subject scope refuses the mint with 401, and a disjoint intersection refuses the mint with 403. The §10.2 failure-modes row that calls the empty-scope mint "not an error path" becomes a refusal. The change lands as a small change before M3, outside program v1, in the manner of 0161. The options not chosen were (b), minting a token whose scope is present but grants nothing, which needs a scope value the auth middleware accepts as empty, and (c), minting `playground_allowed_scope` on a parse failure, which can grant scopes the subject never held.

2. **What the playground does with an ID token whose `scope` claim is not a string.** `extractSubject` in `pkg/gateway/playground/oidc.go` reads the claim with `stringClaim`, which returns the empty string for a JSON array, so an ID token carrying `"scope": ["openid", "profile"]` is stored as an absent claim and minted with `playground_allowed_scope`, while the string `"openid profile"` is refused with 401. The ceiling still bounds the result, and the subject had no scope restriction under either reading, so no privilege is gained. Raised by the general review of 0162.S1 (finding 4). Options: (a) file an issue under PLAY and settle it with the scope-claim handling of [0149](../0149-tenant-scoping-identity/change.md) (TS-2), which filters `scope` to `tools:` values at the verifier; (b) widen 0162 to treat a present non-string `scope` as unparseable at the callback or the mint. Recommendation: (a), because 0149 changes how every verifier reads the claim and the playground callback should follow that reading rather than a rule of its own.

   Settled by the owner on 2026-11-19, question SM-2 of the M3 readiness review, choosing option (a): file an issue under PLAY and settle the behaviour with the scope-claim handling of 0149 (TS-2). The issue is filed on the records branch that carries the owner's other answers.

3. **Whether a refused `oidc`-mode mint names the user in its audit event.** `playground.bearer_mint_rejected` carries `tenant_id`, `subject_jti`, `subject_typ`, `invariant_violated`, and `ingress_path` (§10.2, **Playground mint invariants**). An `oidc`-mode subject is a session record with no `jti`, so the scope refusals this change adds in `oidc` mode emit an event with an empty `subject_jti` that an operator cannot trace to a user. Raised by the security review of 0162.S1 (finding 3). Options: (a) add `user_id` to the event field set, filled on every refusal, in a later change; (b) add it in 0162 before it merges; (c) keep the field set. Recommendation: (a), because the field set is shared with the `apiKey` refusals and the §13.3 rejection discipline, and the gap does not weaken the refusal itself.

   Settled by the owner on 2026-11-19, question SM-3 of the M3 readiness review, choosing option (a): add `user_id` to the event field set in a later change. An issue records it, filed on the records branch that carries the owner's other answers.

4. **What the mint issues for a subject with no scope claim.** The build applied a default, recorded in the digest under "Defaults applied" (**Absent subject scope.**): a subject with no `scope` claim, or an empty one, receives `playground_allowed_scope`, as §25.1 reads an absent claim as no restriction. Confirmed by the owner on 2026-11-19, question SM-4 of the M3 readiness review, option (a).
