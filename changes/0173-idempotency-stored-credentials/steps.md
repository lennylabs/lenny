# Steps: 0173 Credentials in stored idempotency responses

## Validation owed

None.

## Steps

### 0173.S1 Stored idempotency responses hold no upload token
- status: Building
- lanes: [specification, code, documentation]

§7.1 states that the gateway persists no upload token, §11.5 states that a stored response holds a redaction reference in place of each upload token and that a replay rebuilds the token, and §12.9 classifies idempotency records (decision 1). `uploadtoken.Redactor` strips and remints upload tokens, `idempotencymw` and the MCP idempotency hook apply it through a `Credentials` option, and `cmd/lenny-gateway` wires one redactor over the gateway's signing-key ring into both. Tests in the unit, component, and contract tiers. The REST API page and the operator security page state the behavior.
