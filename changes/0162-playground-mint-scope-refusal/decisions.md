# Decisions: 0162 The playground mint refuses a subject scope it cannot narrow

## Open decisions

None.

## Decisions

1. **What the playground mint does when it cannot narrow the subject's scope.** Settled by the owner on 2026-11-19, question S-2 of the M3 readiness review, choosing option (a): refuse. An unparseable subject scope refuses the mint with 401, and a disjoint intersection refuses the mint with 403. The §10.2 failure-modes row that calls the empty-scope mint "not an error path" becomes a refusal. The change lands as a small change before M3, outside program v1, in the manner of 0161. The options not chosen were (b), minting a token whose scope is present but grants nothing, which needs a scope value the auth middleware accepts as empty, and (c), minting `playground_allowed_scope` on a parse failure, which can grant scopes the subject never held.
