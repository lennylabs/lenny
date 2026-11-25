# Steps: 0177 A shared upload-token key ring and consumed-token tracker

## Validation owed

None.

## Steps

### 0177.S1 Shared signing keys and consumed state
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: []
- closes: []

Waits on open decisions O1 and O2. §7.1 **`uploadToken` format and security properties** and the §12 section for the chosen store, as SK-5 in `change.md` states. Then the shared key storage, rotation with a publish lead, and refresh (SK-2), the shared consumed state (SK-3), the failure behavior (SK-4), the wiring in `cmd/lenny-gateway/token_signing.go`, and the unit, component, contract, load_local, integration, chaos, and security tests. Then `docs/operator-guide/security.md` and `docs/operator-guide/configuration.md`. The step lands before any milestone whose tests run more than one gateway replica (decision 1).
