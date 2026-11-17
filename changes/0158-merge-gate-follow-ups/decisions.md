# Decisions: 0158 Merge gate follow-ups

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-17, answering "go with all your recommendations" to the questions the security review of 0154 raised:

1. **Who builds the gate's harness.** The gate keeps building `lenny-test` from the branch under test, because changes 0116 and 0155 change the harness and must be judged by their own version. Any diff to `cmd/lenny-test/`, `tests/gate/`, or `tests/flake-budget.yaml` is reviewed before it merges. Revisit when pull requests replace direct merges.
2. **Isolation from the host.** The gate keeps the host Docker socket and states that it is no isolation boundary; no Docker-in-Docker sidecar.
3. **Secret scanning.** `gitleaks` runs in the gate's static tier.
4. **Temporary directories.** The gate keeps `TMPDIR` on the container's own file system and a shared directory at its host path for fixtures bind-mounted into testcontainers, as the gate's environment follow-up built.
