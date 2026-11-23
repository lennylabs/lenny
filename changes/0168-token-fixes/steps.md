# Steps: 0168 Token rule fixes from the property tests

## Validation owed

The property tests of 0165.S2 (`tests/component/tokenproperties`) run on the step's branch with the F1 exclusion and the F1 and F2 skips removed, at the default bound and at the long-run bound that the digest of 0165.S2 records, and the step's digest records both runs. 0168.S1 waits on them before it merges.

## Steps

### 0168.S1 Token rule fixes from the property tests
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0165.S1, 0165.S2]
- closes: [TOKX-I17, TOKX-I19, TOKX-I20, TOKX-I22]

Decisions 1 to 5 in §13.3, §11.4, §4.2, §12.3, and §12.2, then in `pkg/tokenservice`, `pkg/storage/issuedtokenstore`, and `pkg/credential/impersonation`, with migrations 0201 and 0202 and unit, component, contract, security, and load_local tests, then the authentication, admin API, and security pages, as `change.md` lists. The fixes form one step because they edit the same paragraphs of §13.3 (step (1a), **Recursive-revocation request authorization.**, and **Platform-admin impersonation.** rule 7) and the same functions of the Token Service and the issued-token store (`handleRevocationRequest`, `revokeImpersonationsFrom`, `RevokeLineage`, and `checkCallerLive`), so separate branches would conflict on every file. Decisions 1 and 2 change the same transaction boundary and land together.

The step depends on 0165.S1 and 0165.S2, which share a branch: the property tests it edits, the text of items 3 and 4 of **Token rule properties.**, and the issues it closes are on that branch. Decisions 6 and 7 settle the start record of TF-5 and the levels of TF-4's refusal.
