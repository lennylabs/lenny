# Anthropic translator golden corpus

§12.2.5 requires byte-equivalent round-trips of canonical Anthropic
SDK request/response pairs through the LLM Proxy native translator.

Layout: `<scenario>/{request.json,response.json}`. Each pair is a
canonical request shape on one side and the expected translator
output on the other. The tier-2 component test
(`tests/tier2_component/translators/`) iterates this directory and
asserts that:

1. Marshalling the request through the translator produces the
   expected `request.json` byte-for-byte (modulo whitespace).
2. Unmarshalling the upstream response and marshalling back through
   the translator preserves the documented fields per the §12.3.2
   fidelity matrix.

When adding a scenario, generate the fixture from the real Anthropic
SDK once and pin the bytes. Drift in the upstream API triggers an
explicit test update.
