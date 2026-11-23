# Decisions: 0167 Token rule fixes from the formal model

## Open decisions

None.

## Decisions

The owner answered questions FM-0 to FM-11 on the M3 readiness decision page on 2026-11-22, each with option (a). The counterexamples and gaps are those of change 0165 (a formal model of the token rules), whose `change.md` lists them under "Counterexamples" and "Current state", and whose `decisions.md` lists open decisions 1 to 11 with the options the page offered.

0. **Where the fixes land (FM-0).** The fixes go into a new change after 0164 merged. The page named it 0166; that number was taken by another change created the same day, so this change is 0167.
1. **CX-3 and G2, a playground bearer outlives the revocation of its subject (FM-1, 0165 open decision 2).** Record each playground bearer in `issued_tokens` with the pasted subject token as its parent, so revoking the subject ends it, and the revocation request's handling of a playground subject then matches the text.
2. **CX-4, an impersonation bearer outlives and escapes the caller's token (FM-2, 0165 open decision 3), Medium.** Cap the bearer's expiry at the calling admin token's expiry, and record the admin token on the bearer's row so revoking it ends the impersonation. The record uses a separate column pair rather than `parent_jti`, as "Limits, and alternatives rejected" in `change.md` and "Defaults applied" in the step's digest state.
3. **CX-5 and G3, a token without a row cannot be a revocation root (FM-3, 0165 open decision 4).** A revocation may start from a token that has no row: record it as revoked and cascade to what was derived from it.
4. **CX-6, a child is broader than its actor (FM-4, 0165 open decision 5), Medium.** For a child mint, the actor token's scope must also grant every scope value the child is issued.
5. **CX-7, strictly narrower by values (FM-5, 0165 open decision 6, its option (b)).** Documentation only: state that a domain wildcard is broader than a list of that domain's current actions, because it also covers actions added later, so the strictly-narrower rule accepts the listed form.
6. **CX-8, a child's depth (FM-6, 0165 open decision 7).** State in §13.3 that a child's depth is the delegating token's depth plus one, and that rule (b)'s no-decrement check compares a child with the delegating token. The text changes to match the code.
7. **CX-9, a delegated token loses `act` (FM-7, 0165 open decision 8).** On exchanges without an actor token, copy the subject's `act` claim, so narrowing or rotating a delegation child keeps its delegation record. This closes [TOKX-I14](../../issues/closed/TOKX/TOKX-I14-rotated-delegation-child-drops-its-act-claim.md).
8. **CX-10 and G4, a revoked caller authenticates a mint (FM-8, 0165 open decision 9), Medium.** Check the caller token's revocation on every mint, in step (1a) of write-before-issue, as for the subject and actor tokens.
9. **CX-11, a full revoke misses delegations (FM-9, 0165 open decision 10).** A full revocation of a user (§11.4 step 5) also cascades through the lineage of each revoked token, so tokens others minted from the user's tokens end too.
10. **G1, default audience (FM-10).** Apply the default audience `lenny-gateway` when an exchange request omits `audience`, as §13.3's parameter table states; a self-exchange without `audience` is then detected as a rotation.
11. **G5, G6, and G7 (FM-11).** File them as issues under the owning page codes, each with the evidence from 0165, and do not fix them in this change. The property tests (0165.S2) and the TLA+ model come later.
