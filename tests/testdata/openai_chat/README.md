# OpenAI Chat Completions translator golden corpus

§12.2.5 + §12.3.2: byte-equivalent round-trips of canonical OpenAI
Chat Completions request/response pairs through the LLM Proxy
native translator.

Layout: `<scenario>/{request.json,response.json}`. The §12.3.2
fidelity matrix names which Lenny-side fields are preserved,
dropped, or lossy through this translation; the corpus must
demonstrate each documented behavior.

## `schema/`

`schema/create-chat-completion-response.schema.json` is the published
OpenAI `CreateChatCompletionResponse` component schema (and its
transitive `$ref` closure), vendored from
[`openai/openai-openapi`](https://github.com/openai/openai-openapi)
commit `5162af98d3147432c14680df789e8e12d4891e6b` (2026-05-13,
`openapi.yaml`). Every `$ref` is rewritten from
`#/components/schemas/<Name>` to a local `#/$defs/<Name>` so the file
compiles standalone. One upstream draft-2019-09 self-recursion
(`$recursiveAnchor` / `$recursiveRef: '#'` on `CompoundFilter`) is
normalized to a plain self-`$ref` for draft 2020-12 compatibility;
this changes no accepted or rejected instance since the bundle is
self-contained and the recursion always resolves to the same enclosing
schema. `tests/tier3_contract/rest_openai_chat/published_schema_test.go`
validates the `/v1/chat/completions` response body against this file
via `tests/testinfra/schematest`, so the wire body is checked against
OpenAI's own published schema and not only against Lenny's translator
structs.
