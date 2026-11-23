# Steps: 0167 Token rule fixes from the formal model

## Validation owed

None. Change 0165's Alloy model is rerun against the fixed rules on a copy of the model, as the step's digest records.

## Steps

### 0167.S1 Token rule fixes from the formal model
- status: Building
- lanes: [specification, code, documentation]
- depends-on: [0164.S1]
- closes: [TOKX-I14]

Decisions 1 to 10 in §13.3, §10.2, §11.4, §12.2, and §16.7, then in `pkg/tokenexchange`, `pkg/tokenservice`, `pkg/storage/issuedtokenstore`, `pkg/credential/impersonation`, `pkg/gateway/playground`, `pkg/gateway/api/admin`, and `pkg/auth/authmw`, with migration 0200 and unit, contract, component, and security tests, then the authentication, admin API, security, and web playground pages. Decision 11 files three issues. The fixes form one step because they edit the same paragraphs of §13.3 and the same functions of the Token Service and the issued-token store, so two branches would conflict on every file. Decisions 5 and 6 change text only and land with the rest.
