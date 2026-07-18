# OpenSLO v1 structural schema

§16.10 renders the §16.5 SLO catalog as OpenSLO v1 documents. Unlike
CloudEvents, OpenAI, and MCP (each of which publishes a machine-readable
JSON Schema this repository vendors verbatim), the
[OpenSLO](https://github.com/OpenSLO/OpenSLO) project publishes its v1
object model only as prose and YAML examples in its README (commit
`e74b589cc98b98a5413611176d659a72318e7519`, 2025-10-09); it has no
JSON Schema release artifact to vendor.

`schema/openslo-v1.schema.json` is authored by this repository rather
than vendored, and it transcribes the OpenSLO v1 README's "General
Schema" and "Object Types" (`SLI`, `SLO`, `AlertPolicy`,
`AlertCondition`, `AlertNotificationTarget`) sections into JSON Schema
draft 2020-12. Every
`required` and `enum`/`const` constraint in the file cites, in a
`description` key alongside it, the README sentence it encodes, so a
reviewer can check the transcription against the source without
re-deriving it.

The transcription is cross-checked against the OpenSLO project's own
reference implementation, the official Go SDK
(`github.com/OpenSLO/go-sdk`, commit
`ca884bbb946dafe237e536116c7503270cfddb8c`, 2026-06-24), whose
`govy`-based validators enforce the same constraints in code (for
example, `pkg/openslo/v1/alert_policy.go` calls
`rules.SliceLength[[]AlertPolicyCondition](1, 1)` on
`AlertPolicySpec.Conditions` and `rules.SliceMinLength(1)` on
`AlertPolicySpec.NotificationTargets`, matching the README's "(max of
one condition)" and "required field" notes respectively; and
`pkg/openslo/v1/alert_notification_target.go` calls `rules.NotEmpty` on
`AlertNotificationTargetSpec.Target`, matching the README's "target
string, required field" note). Sloth, one
of the OpenSLO-compatible tools §16.10 names, parses OpenSLO input
through this same SDK lineage (`github.com/OpenSLO/oslo`, the SDK's
prior module name), so a document this schema rejects is a document a
real OpenSLO-conformant tool also rejects.

`tests/tier0_static/openslo_export_test.go` validates the rendered
chart fragment (`charts/lenny/files/openslo.yaml`) against the per-kind
subschemas (`#/$defs/SLIDocument`, `#/$defs/SLODocument`,
`#/$defs/AlertPolicyDocument`, `#/$defs/AlertNotificationTargetDocument`)
via `tests/testinfra/schematest`. `AlertNotificationTargetDocument`
requires `spec.target`, matching the go-sdk validator that calls
`rules.NotEmpty` on `AlertNotificationTargetSpec.Target`.
