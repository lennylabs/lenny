# OpenAI Responses translator golden corpus

§12.2.5 + §12.3.3: byte-equivalent round-trips of canonical OpenAI
Responses API request/response pairs. The Responses API has the
extended `id` field behavior documented in §12.3.3; the corpus
demonstrates the round-trip exactly.

Layout: `<scenario>/{request.json,response.json}`.
