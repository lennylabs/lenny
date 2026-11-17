# Decisions: 0158 Merge gate follow-ups

## Open decisions

1. **Isolation from the host.** The gate mounts the host Docker socket and runs tests with `CAP_SYS_ADMIN`, so a test can do on the host what it could do running there directly. Options: (a) accept and document it, as `TESTING.md` does; (b) a rootless Docker-in-Docker sidecar on a private network; (c) a sidecar for the tests that need no host paths, with the socket only for the few that do. The owner asked for an elaboration before deciding.

## Decisions

Settled by the owner on 2026-11-17, answering "go with all your recommendations" to the questions the security review of 0154 raised:

1. **Who builds the gate's harness.** The gate keeps building `lenny-test` from the branch under test, because changes 0116 and 0155 change the harness and must be judged by their own version. Any diff to `cmd/lenny-test/`, `tests/gate/`, or `tests/flake-budget.yaml` is reviewed before it merges. Revisit when pull requests replace direct merges.
2. **Secret scanning.** `gitleaks` runs in the gate's static tier.
3. **Temporary directories.** The gate keeps `TMPDIR` on the container's own file system and a shared directory at its host path for fixtures bind-mounted into testcontainers, as the gate's environment follow-up built.
