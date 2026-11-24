# Steps: 0168 Token rule fixes from the property tests

## Validation owed

The property tests of 0165.S2 (`tests/component/tokenproperties`) run on the step's branch with the F1 exclusion and the F1 and F2 skips removed, at the default bound and at the long-run bound that the digest of 0165.S2 records, and the step's digest records both runs. The `TF` variant of change 0169's model (`tests/formal/tokenconcurrency`) is brought to revision 4 of this change (the step's updates in the outer transaction with the retry on `40P01`, the advisory lock of `I13Lock`, and the caller share lock of rule 1 (c)), and every configuration runs under it with the pinned TLC release of 0169 decision 4, the `impersonation` and `nested_start` configurations included as decision 11 requires. C1, C5, C7, C8, and C9 hold, and C6 holds except for the bearer row of decision 10; the digest records each run as 0169's "Model results" records them, and a new counterexample goes to the owner as an open decision of this change. 0168.S1 waits on both before it merges.

## Steps

### 0168.S1 Token rule fixes from the property tests
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0165.S1, 0165.S2]
- closes: [TOKX-I10, TOKX-I13, TOKX-I17, TOKX-I19, TOKX-I20, TOKX-I22]

Decisions 1 to 12 in §13.3, §11.4, §4.2, §12.3, and §12.2, then in `pkg/tokenservice`, `pkg/storage/issuedtokenstore`, and `pkg/credential/impersonation`, with migrations 0201, 0202, and 0203, unit, component, contract, security, and load_local tests, and the `TF` variant of change 0169's model, then the authentication, admin API, and security pages, as `change.md` lists. The fixes form one step because they edit the same paragraphs of §13.3 (step (1a), **Recursive-revocation request authorization.**, and **Platform-admin impersonation.** rules 1 (c) and 7) and the same functions of the Token Service and the issued-token store (`handleRevocationRequest`, `revokeImpersonationsFrom`, `RevokeLineage`, `revokeLineageOnce`, `RevokeBySubject`, `RecordImpersonation`, and `checkCallerLive`), so separate branches would conflict on every file. Decisions 1, 2, and 8 change the same transaction boundary and land together, and decisions 9 and 12 land together because the advisory lock of decision 9 blocks the tenant's issuances for as long as the bound of decision 12 lets a cascade run.

The step depends on 0165.S1 and 0165.S2, which landed: the property tests it edits, the text of items 3 and 4 of **Token rule properties.**, and the issues it closes are on `main`. Decisions 6 and 7 settle the start record of TF-5 and the levels of TF-4's refusal. Step 0169.S2 of change 0169 (a concurrency model of the token rules) checked the locking design of decisions 1, 2, 5, and 6 before the code lane, by the owner's decision of 2026-11-23, and landed with its results; the owner's answers to them are decisions 8 to 12. The specification lane of revision 3, on branch `change/0168.S1-token-fixes`, is revised to revision 4 before the code lane starts.

TOKX-I13 closes with TF-6 and TF-7. The window that rule 7 states for an impersonation started after the step reads the start records ends at the cascade's commit under TF-7, and a caller bearer without an `issued_tokens` row keeps it, as "Limits, and alternatives rejected" in `change.md` states.
