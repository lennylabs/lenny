# Steps: 0158 Merge gate follow-ups

## Validation owed

## Steps

### 0158.S1 Secret scan in the gate and the review rule for gate changes
- status: Planned
- lanes: [code, documentation]

`gitleaks` pinned in `tests/gate/Dockerfile` with a checked download, a static-tier check that runs it over tracked files with a reviewed allowlist for test fixtures, a test with a planted secret, and the review rule for diffs to the gate's harness in `git-workflow.md`, the `review` and `build` skills, and `TESTING.md`.
