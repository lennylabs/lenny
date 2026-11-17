# Coverage and test selection from annotations

Status: `Draft`, revision 1, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M1.

## Problem

Coverage of the specification is recorded in `tests/spec-map.json`, and `--changed` test selection in `tests/change-graph.json`, both maintained by hand and keyed by path or section number, with registers and self-tests around them. They caused most of the friction when the migration spike moved a package, and they go stale as the specification restructure moves text ([test infrastructure review](../0154-merge-gate/evidence/test-infrastructure-review.md), "Registers and maps with 0116").

## Scope

To be framed. As the v1 program states it:

- Coverage (identifier or section to tests) derived from `// spec:` annotations and generated on demand, with the gate that every requirement a diff adds or modifies has a citing test, as 0115 plans.
- `--changed` selection derived from the Go import graph (`go list -deps -test`) for Go sources, from annotations for specification pages, and from a short hand-kept list for chart templates, migrations, and schemas.
- `spec-map.json`, `spec-map-exceptions.yaml`, `spec-map-pending.txt`, `change-graph.json`, and `claim-map.json` retired, with their registers and self-tests; claim-map claims become issues under `issues/`.
- The annotation lint of 0115 extended to test names.
