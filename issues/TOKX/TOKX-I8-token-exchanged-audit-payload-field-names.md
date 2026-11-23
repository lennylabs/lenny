---
id: TOKX-I8
status: open
kind: build-gap
severity: low
requirements: []
---

# The `token.exchanged` row names its identity fields differently from the catalog

## Specification

§16.7 (Section 25 Audit Events), `token.exchanged`: the payload carries `subject_token_sub`, `subject_token_jti`, `actor_sub` (from the `actor_token` when present), and `issued_token_jti`, and a scope-narrowing violation produces a `rejected:invalid_scope` event.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `exchangeAuditPayload`: the payload writes `subject_sub` where the catalog names `subject_token_sub`, and `jti` where it names `issued_token_jti`. It writes no `subject_token_jti` and no `actor_sub`, and it writes a `caller_sub` field the catalog does not name.
- `pkg/tokenservice/tokenservice.go`, `validateExchange`: a rejected exchange writes `policy_result` as `rejected:` followed by the validator's reason, such as `rejected:scope_not_subset` or `rejected:scope_not_narrowed`, where the catalog names `rejected:invalid_scope` for a scope-narrowing violation.
- Change 0164 (TOKX-I7) renamed the scope fields to `requested_scope` and `granted_scope` and left these fields as they were, because the owner's decision named the scope fields only. The drift predates 0164.

## Gap

A SIEM rule written against the §16.7 field names finds no subject jti, no actor, and no issued-token jti on a `token.exchanged` row, and a rule that matches `rejected:invalid_scope` matches no rejected narrowing.

## Suggested resolution

Choose between renaming the payload fields to the §16.7 names, adding `subject_token_jti` and `actor_sub`, and stating `caller_sub` and the reason form of `policy_result` in §16.7; or editing §16.7 to name the fields the Token Service writes. The choice needs the owner, because a SIEM rule may read either form.
