# Decisions: 0163 A token exchange without scope issues the subject token's scope

## Open decisions

None.

## Decisions

1. **What a token exchange without `scope` issues.** Settled by the owner on 2026-11-19, question SM-1 of the M3 readiness review, choosing option (a): when the request names no scope, the Token Service issues the subject token's own scope, so the issued token is never broader than the subject token. A subject token with no `scope` claim stays unrestricted, as §25.1 (**Matching.**) states for an absent claim. The change lands as a small change before M3, outside program v1, in the manner of 0161 and 0162. The option not chosen was (b), refusing the request with `invalid_scope` when the subject's scope is present and the request's is empty.
