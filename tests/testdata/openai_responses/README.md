# OpenAI Responses translator golden corpus

§12.2.5 + §12.3.3: byte-equivalent round-trips of canonical OpenAI
Responses API request/response pairs. The Responses API has the
extended `id` field behavior documented in §12.3.3; the corpus
demonstrates the round-trip exactly.

Layout: `<scenario>/{request.json,response.json}`.

## `schema/`

`schema/response.schema.json` is the published OpenAI Responses API
`Response` component schema (and its transitive `$ref` closure),
vendored from
[`openai/openai-openapi`](https://github.com/openai/openai-openapi)
commit `5162af98d3147432c14680df789e8e12d4891e6b` (2026-05-13,
`openapi.yaml`). Every `$ref` is rewritten from
`#/components/schemas/<Name>` to a local `#/$defs/<Name>` so the file
compiles standalone; the closure pulls in the full hosted-tool union
(file search, web search, code interpreter, MCP, etc.) because the
`Response.tools` field can carry any of them, even though Lenny's
translator never populates `tools` with anything but an empty array.
One upstream draft-2019-09 self-recursion (`$recursiveAnchor` /
`$recursiveRef: '#'` on `CompoundFilter`) is normalized to a plain
self-`$ref` for draft 2020-12 compatibility; this changes no accepted
or rejected instance since the bundle is self-contained and the
recursion always resolves to the same enclosing schema.
`tests/tier3_contract/rest_openai_responses/published_schema_test.go`
validates the `/v1/responses` response body against this file via
`tests/testinfra/schematest`, so the wire body is checked against
OpenAI's own published schema and not only against Lenny's translator
structs.
