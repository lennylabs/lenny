# Decisions: 0158 Merge gate follow-ups

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-17, answering "go with all your recommendations" to the questions the security review of 0154 raised:

1. **Who builds the gate's harness.** The gate keeps building `lenny-test` from the branch under test, because changes 0116 and 0155 change the harness and must be judged by their own version. Any diff to `cmd/lenny-test/`, `tests/gate/`, or `tests/flake-budget.yaml` is reviewed before it merges. Revisit when pull requests replace direct merges.
2. **Secret scanning.** `gitleaks` runs in the gate's static tier.
3. **Temporary directories.** The gate keeps `TMPDIR` on the container's own file system and a shared directory at its host path for fixtures bind-mounted into testcontainers, as the gate's environment follow-up built.
4. **Isolation from the host.** The gate keeps the host Docker socket and states that it is no isolation boundary; no Docker-in-Docker sidecar. Revisit when pull requests replace direct merges and the gate runs in CI. Settled by the owner on 2026-11-17 ("(a) is fine"), after an elaboration of what the socket exposes and how it compares with running tests on the host.

Default applied in 0158.S1: `.gitleaks.toml` joined the paths decision 1 makes reviewable, because a change to it can switch the secret scan off.

Settled by the owner on 2026-11-17 ("go with all your recommendations on 0158"), on the questions 0158.S1 raised; `review-history.md` keeps them as framed:

5. **gitleaks's default global allowlist.** The scan keeps extending the default rules, and the check fails when a tracked path matches one of the path patterns the default allowlist skips, so nothing the repository tracks escapes the scan by its path. Its value patterns stay as gitleaks ships them.
6. **Wider review rule.** `tests/static/`, `tests/registers/`, `go.mod`, and `go.sum` join the paths whose changes are reviewed before they merge (decision 1). Scripts and the packages `lenny-test` imports stay outside the rule.
7. **Archives and binary files.** A reviewed list of the tracked archives and binary files is kept in the repository, and the check fails on a tracked archive or binary file that the list does not name, until it is reviewed and listed.

