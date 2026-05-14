# OpenAI Chat Completions translator golden corpus

§12.2.5 + §12.3.2: byte-equivalent round-trips of canonical OpenAI
Chat Completions request/response pairs through the LLM Proxy
native translator.

Layout: `<scenario>/{request.json,response.json}`. The §12.3.2
fidelity matrix names which Lenny-side fields are preserved,
dropped, or lossy through this translation; the corpus must
demonstrate each documented behavior.
