# Decisions: 0158 Merge gate follow-ups

## Open decisions

Raised by the build and review of 0158.S1:

1. **gitleaks's default global allowlist.** Extending the default rules also brings in a default allowlist that skips any path containing `gitleaks.toml` or under `node_modules/`, images, lockfiles, and `go.sum`, and carries a loose pattern, `(?i)^true|false|null$`, that lets through any value containing "false". Options: (a) copy the default rules into `.gitleaks.toml` with a trimmed allowlist; (b) keep extending the defaults, and fail the check when a tracked path matches those skipped path patterns; (c) accept as is. Recommendation: (b).
2. **Wider review rule.** Scripts, `tests/static/`, `tests/registers/`, the packages `lenny-test` imports, and `go.mod` and `go.sum` can also weaken checks, or could put a fake `gitleaks` on the path through an `init()` function. Options: (a) add them to the reviewed paths; (b) keep the four paths. Recommendation: (a) for `tests/static/`, `tests/registers/`, and `go.mod`/`go.sum`, which are few and decisive; the broad sets (every script, every imported package) would make most changes reviewable and dilute the rule.
3. **Archives and binary files.** Tracked archives and binary files are not scanned, and the upload fixtures include archive bombs. Options: (a) a reviewed list of tracked binary files, which the scan checks against so a new one fails until listed; (b) leave them unscanned. Recommendation: (a).

## Decisions

Settled by the owner on 2026-11-17, answering "go with all your recommendations" to the questions the security review of 0154 raised:

1. **Who builds the gate's harness.** The gate keeps building `lenny-test` from the branch under test, because changes 0116 and 0155 change the harness and must be judged by their own version. Any diff to `cmd/lenny-test/`, `tests/gate/`, or `tests/flake-budget.yaml` is reviewed before it merges. Revisit when pull requests replace direct merges.
2. **Secret scanning.** `gitleaks` runs in the gate's static tier.
3. **Temporary directories.** The gate keeps `TMPDIR` on the container's own file system and a shared directory at its host path for fixtures bind-mounted into testcontainers, as the gate's environment follow-up built.
4. **Isolation from the host.** The gate keeps the host Docker socket and states that it is no isolation boundary; no Docker-in-Docker sidecar. Revisit when pull requests replace direct merges and the gate runs in CI. Settled by the owner on 2026-11-17 ("(a) is fine"), after an elaboration of what the socket exposes and how it compares with running tests on the host.

Default applied in 0158.S1: `.gitleaks.toml` joined the paths decision 1 makes reviewable, because a change to it can switch the secret scan off.

