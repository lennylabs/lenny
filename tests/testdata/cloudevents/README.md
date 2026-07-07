# Vendored CloudEvents JSON Schema

§12.3.7 pins the EventBus envelope to CloudEvents v1.0.2. The file under
this directory is the published CloudEvents JSON Schema for that
envelope, vendored verbatim from
[`cloudevents/spec`](https://github.com/cloudevents/spec):

- `v1.0.2-cloudevents.schema.json` — tag `v1.0.2`, path
  `cloudevents/formats/cloudevents.json`.

Vendored 2026-07-06.

`tests/tier3_contract/cloudevents/published_schema_test.go` validates
one marshalled `Event` per kind against this file via
`tests/testinfra/schematest`, so the wire envelope is checked against
the CloudEvents specification's own schema and not only against
Lenny's `Event.Validate()`.

The published schema does not encode two constraints from the
CloudEvents spec prose itself, so the test asserts them directly
against the marshalled envelope rather than through the schema:

- The "Attribute Naming Convention" section requires every attribute
  name, including extension attributes, to be lower-case ASCII letters
  or digits. The schema permits any additional property name.
- The `datacontenttype` attribute description requires an RFC 2046
  media type. The schema only requires a non-empty string (or `null`).
