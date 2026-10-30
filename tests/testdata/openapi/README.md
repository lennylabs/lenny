# OpenAPI 3.1 meta-schema

`schema/openapi-3.1.schema.json` is the OpenAPI Initiative's official
JSON Schema for OpenAPI 3.1.x documents, vendored verbatim from
`https://spec.openapis.org/oas/3.1/schema/2022-10-07` (the `2022-10-07`
dated release, referenced by the OpenAPI 3.1.0 specification at
`https://spec.openapis.org/oas/v3.1.0`). It is self-contained: every
`$ref` and `$dynamicRef` inside it resolves to a `$defs` entry or the
`#meta` `$dynamicAnchor` in the same file, and it declares
`"$schema": "https://json-schema.org/draft/2020-12/schema"`, which
`tests/testinfra/schematest.NewCompiler` selects via
`jsonschema.Draft2020` without a network fetch.

`tests/tier3_contract/rest_sessions/openapi_document_test.go` validates
the served `/openapi.json` document (§15.1) against this meta-schema,
via `tests/testinfra/schematest.Compile`.
