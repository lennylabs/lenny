---
id: TOKX-I11
status: open
kind: build-gap
severity: low
requirements: []
---

# `POST /v1/oauth/token` reads a request body and a `scope` list of any size

## Specification

§13.3 (Credential Flow) states the parameters of the token exchange and the rate limits of `/v1/oauth/token`. It sets no limit on the size of a request body or on the number of values in `scope`.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `parseRequest`: a JSON body is decoded with `json.NewDecoder(r.Body).Decode` and no `http.MaxBytesReader`; a form body is bounded only by the 10 MB default of `ParseForm`.
- `pkg/tokenservice/tokenservice.go`, `validateExchange`: a rejected exchange writes the whole requested scope list into the `token.exchanged` row (`requested_scope`, formerly `scope`), under the per-tenant audit advisory lock.
- The security review of 0164.S1 on 2026-11-20 found the gap (finding 5). It predates 0164, which renamed the field only.

## Gap

An authenticated caller in a tenant sends large `scope` lists up to the per-caller rate limit, and each rejected exchange holds the list in memory and writes it to `audit_log` under the tenant's advisory lock.

## Suggested resolution

Bound the request body and the number of `scope` values, and truncate `requested_scope` on a rejected row, with the bounds stated in §13.3 or as operator-tunable defaults. The values need the owner.
