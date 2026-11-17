# Steps: 0158 Merge gate follow-ups

## Validation owed

## Steps

### 0158.S1 Secret scan in the gate and the review rule for gate changes
- status: Landed
- lanes: [code, documentation]

`gitleaks` pinned in `tests/gate/Dockerfile` with a checked download, a static-tier check that runs it over tracked files with a reviewed allowlist for test fixtures, a test with a planted secret, and the review rule for diffs to the gate's harness in `git-workflow.md`, the `review` and `build` skills, and `TESTING.md`.

### 0158.S2 Skipped paths, wider review rule, and reviewed binary files
- status: Planned
- lanes: [code, documentation]
- depends-on: [0158.S1]

The secret-scan check fails when a tracked path matches a path pattern of gitleaks's default global allowlist (decision 5), the review rule names `tests/static/`, `tests/registers/`, `go.mod`, and `go.sum` (decision 6), and a reviewed list of tracked archives and binary files makes the check fail on one it does not name (decision 7), each with a test, and `TESTING.md` and the rule state them.

