# Steps: 0173 Credentials in stored idempotency responses

## Validation owed

None.

## Steps

### 0173.S1 Stored idempotency responses hold no upload token
- status: Building
- lanes: [specification, code, documentation]

§7.1 states that the gateway persists no upload token, §11.5 states that a stored response holds a redaction reference in place of each upload token and that a replay rebuilds the token, and §12.9 classifies idempotency records (decision 1). `uploadtoken.Redactor` strips and remints upload tokens, `idempotencymw.WithCredentials` applies it at the store boundary, and `cmd/lenny-gateway` wraps the store that the REST middleware and the MCP idempotency hook share with one redactor over the gateway's signing-key ring. The verifier accepts only the canonical spelling of a token, and `lenny/vcs_token` marks its result as not to be stored. Tests in the unit, component, contract, and security tiers. The REST API page and the operator security page state the behavior.
