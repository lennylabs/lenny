# Decisions: 0164 Token exchange narrowing and identity rules

## Open decisions

None.

## Decisions

1. **How an exchange narrows a subject token without a `scope` claim (SM-5, TOKX-I3).** Settled by the owner on 2026-11-20 on the M3 readiness decision page, choosing option (a): the subset check reads an absent subject claim as unrestricted, as §25.1 **Matching.** states. A requested set is accepted when every value parses as a canonical scope and the subject's parsed set matches it, which also honours `tools:*` and `tools:<domain>:*` subjects. The `authorized_tools` allowlist of §13.3 rule (e) is kept when the issued scope is absent. The operator guide example that shows narrowing is corrected. The option not chosen was (b), stating that an unscoped subject cannot be narrowed through the exchange.
2. **A playground bearer as `subject_token` or `actor_token` (SM-6, TOKX-I4).** Settled by the owner on 2026-11-20, choosing option (a): the Token Service refuses a `subject_token` or `actor_token` whose `origin` claim is `playground` with `invalid_grant`. The option not chosen was (b), copying the subject's `origin` claim onto the issued token.
3. **The caller-identity rule and the lineage of derived tokens (SM-7, TOKX-I5).** Settled by the owner on 2026-11-20, choosing option (a): the Token Service enforces the rule §13.3 **Client authentication on `/v1/oauth/token`.** states. When the caller's `sub` or `caller_type` differs from the subject token's, the request must name a scope strictly narrower than the subject's, or the exchange is refused. Delegation child minting with an `actor_token` keeps its own rules and is unaffected. Every exchange records the derived token's parent, the subject token's `jti`, and not only `actor_token` child mints, so the recursive revocation of a token reaches the tokens derived from it by narrowing or by an exchange from another caller. §13.3 states the rule.
4. **The scope claim of a platform-admin impersonation bearer (SM-8, TOKX-I6).** Settled by the owner on 2026-11-20, choosing option (a): the impersonation bearer carries the caller's `scope` claim when the caller's claim is present, as §13.3 states. The option not chosen was refusing a caller whose claim is present and is not `tools:*`.
5. **The scope fields of the `token.exchanged` row (TOKX-I7).** No decision was needed: the row carries `requested_scope` and `granted_scope` as §16.7 names them.
6. **Placement.** The owner's request of 2026-11-20 places the change outside program v1, as a small change before M3 in the manner of 0161, 0162, and 0163. The change plans one step, because the lineage of decision 3 is one function and one specification sentence, and the other rules share the validator and its tests.
