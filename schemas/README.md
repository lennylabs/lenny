# `schemas/`

Wire-contract artifacts. Spec-first: each file is the authoritative shape of the corresponding interface, and the implementation in `pkg/` is verified by CI to match.

| File | Surface | Spec section |
|:-----|:--------|:-------------|
| `lenny-adapter.proto` | Gateway ↔ pod gRPC control protocol | [§15.3](../spec/15_external-api-surface.md), [§15.4](../spec/15_external-api-surface.md) |
| `lenny-interceptor.proto` | Gateway ↔ external interceptor gRPC SPI | [§4.8](../spec/04_system-components.md), [§8.7](../spec/08_recursive-delegation.md) |
| `lenny-adapter-jsonl.schema.json` | Adapter sidecar ↔ agent binary stdin/stdout JSONL | [§15.4](../spec/15_external-api-surface.md) |
| `outputpart.schema.json` | OutputPart shape used in sessions, tasks, and audit | [§15.4](../spec/15_external-api-surface.md) |
| `workspaceplan-v1.json` | WorkspacePlan used at session creation | [§14](../spec/14_workspace-plan-schema.md) |
| `ocsf-mapping.yaml` | Event-type → OCSF class/activity mapping mirror | [§11.7](../spec/11_security-trust-model.md) |
| `audit-events/v1.json` | Audit-event canonical-record schema, per `event_schema_version` | [§11.7](../spec/11_security-trust-model.md) |

## Validation

Phase 1 Tier 0 runs these checks:

```bash
lenny-test --tier static                # full Tier 0
buf lint                                # proto only
# JSON Schema example round-trips: tests/tier0_static/schemas_test.go
```

`buf breaking` runs against the prior commit on `main` in CI to catch wire-incompatible changes that aren't covered by an explicit version bump.

## Versioning

`workspaceplan-v1.json` is versioned in the file name. Phase 1 ships `v1`. Future major versions ship `workspaceplan-v2.json` and the old file remains for as long as the spec's deprecation window requires (currently 6 months — see [`spec/15_external-api-surface.md`](../spec/15_external-api-surface.md) §15.5).

`outputpart.schema.json` and `lenny-adapter-jsonl.schema.json` carry a `schemaVersion` field internally. Forward-compatibility rules: see [`spec/14_workspace-plan-schema.md`](../spec/14_workspace-plan-schema.md) §14.1.

`audit-events/` is versioned by file name (`v1.json`, then `v2.json`). The version matches the audit_log `event_schema_version` column. `ocsf-mapping.yaml` is generated from the Go catalog in `pkg/audit/ocsf/mapping.go`; regenerate it with `go run ./cmd/lenny-ocsf-mapping-gen`, and `TestMappingYAMLInSync` fails CI when the committed file drifts.

Each proto file uses a buf-canonical `v1` package suffix (`lenny.adapter.v1`, `lenny.interceptor.v1`). Breaking changes require a `v2` package suffix and `buf breaking` flags the diff.

## Examples

Each schema has at least one valid example payload in [`examples/`](examples/). The Tier 0 schema-validation test loads every schema and every example and asserts the example validates.
