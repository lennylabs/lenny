# Decisions: 0173 Credentials in stored idempotency responses

## Open decisions

2. **Which token a replay mints.** Decision 1 says that a replay "mints a fresh token". The build mints the token again under the key that signed the original, so the minted token equals the original while that key is in the ring, and the replayed body equals the original. Raised by the general review of 0173.S1 (finding 2).
   - (a) Keep the build: mint under the original signing key. The token's digest stays the one on the session row, which `FinalizeWorkspace` marks consumed, so single-use invalidation covers the replayed token, and §11.5's "same HTTP status and body" holds.
   - (b) Mint a new token under the active key on every replay and write its digest and expiry to the session row. The replayed token differs from the original, the replay writes the session row, and the first token stays valid until it expires unless the row keeps both digests.

   **Recommendation: (a).** It is the reading of "fresh" that keeps the replay identical and keeps single-use invalidation exact, and it writes nothing on replay.

3. **Client values with the token's form.** The redaction works on the bytes of the response, so a value a client sent that the response echoes, such as an `env` or `metadata` value, and that has the form of an upload token or of a redaction reference, is stored with a mark no key reproduces. Its replay then differs from the original response. Raised by the security review of 0173.S1 (finding 3).
   - (a) Keep the byte-level redaction. It reaches a token wherever a response carries it, including in an MCP tool result's text and a JSON-RPC response, and it fails closed on a token another replica's ring signed and on a reference copied out of the table.
   - (b) Redact only the `uploadToken` fields of the responses that create a session, by their JSON position. Client values replay unchanged, and the redactor must know each response format, including the MCP text content.

   **Recommendation: (a).** A client value of exactly that form is rare. (a) keeps every value with a token's form out of the table, whatever field carries it, with no knowledge of each response format, while (b) stores a token that a client copies into its own request data.

## Decisions

1. **Credentials in stored idempotency responses.** Settled by the owner on 2026-11-20, question DV-6 of the M3 readiness decision page, option (a), recorded as decision D14 of [0150](../0150-tenant-security-settings/decisions.md#decisions) (open decision O21 there). A response that carries a credential is not stored with it: a critical operation that returns one stores the response with the token removed and mints a fresh token on replay, or calls `idempotencymw.DoNotStore`, and §12.9 classifies idempotency records. D14 placed the work in the step named when the proposed upload-token change was created. That change was never created, so this change carries the work, and 0173.S1 is the step D14 names. The options not taken were (b), encrypting the stored response body under the tenant key, and (c), classifying the records as Secret and relying on volume encryption.
