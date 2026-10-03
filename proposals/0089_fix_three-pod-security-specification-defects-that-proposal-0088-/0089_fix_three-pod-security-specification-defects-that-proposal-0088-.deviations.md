# Deviations: Pod-security specification defects left unstaged by proposal 0088

The implementor owns this file, and it stays empty until an implementation records a departure from what the proposal states.

## Commit message of 7340bb8d7 carries proposal-internal deliverable labels

The commit that landed the §13.1 (lenny-cred-readers membership boundary) residual for embedded-model adapter name-keying has the subject `spec: state the embedded-model adapter name-keying residual in the lenny-cred-readers boundary (proposal 0089 SPEC-C)` and a body stating `SPEC-A was already landed in 3897dfc7a and matches its staged text.` `SPEC-A` and `SPEC-C` are deliverable labels of this proposal rather than specification sections. The commit should have named the behavior and cited §13.1 (lenny-cred-readers membership boundary).

The implementation keeps the history as written. Committed history on the implementation branch is not rewritten, so no rebase or reword was performed, and no commit-message lint or tier-0 gate was added, because the proposal stages neither. The diff of 7340bb8d7 itself carries no scaffolding label in any code, comment, test name, or specification text. The merge commit that integrates this branch should describe that commit as landing the §13.1 (lenny-cred-readers membership boundary) residual for embedded-model adapter name-keying.
