# Steps: 0162 The playground mint refuses a subject scope it cannot narrow

## Validation owed

None.

## Steps

### 0162.S1 Playground mint scope refusal
- status: Building
- lanes: [specification, code, documentation]

§10.2 invariant 2 and the **Playground mint failure modes** table, the §15.1 `POST /v1/playground/token` row, the §15 `SCOPE_FORBIDDEN` catalog row, and §27.3.1 state that the mint refuses an unparseable subject scope with `401 UNAUTHORIZED` and a disjoint subject scope with `403 SCOPE_FORBIDDEN` (decision 1). `intersectScope` and `completeMint` in `pkg/gateway/playground/token.go` refuse both before signing and emit `playground.bearer_mint_rejected`, with unit tests in the package and contract tests in `tests/contract/playground`. The operator guide's web playground page states the narrowing and the refusals.
