---
id: TOKX-I14
status: open
kind: spec-defect
severity: low
requirements: []
---

# A delegation child that rotates itself loses its `act` claim

## Specification

§13.3 (Credential Flow), the RFC 8693 claim mapping: `act` is set when `actor_token` is present. A self-rotation carries no `actor_token`, so the specification does not say whether the token it issues keeps the `act` claim of the token it replaces.

## Evidence

- `pkg/tokenexchange/tokenexchange.go`, `Validate`: the issued token's `Act` is `actorOf(r.Actor)`, which is nil without an `actor_token`. An `a2a_delegation` child that rotates itself therefore receives a token with no `act` claim.
- `pkg/tokenservice/tokenservice.go`, `derivedLineage`: the rotated child's `issued_tokens` row records the replaced child as `parent_jti` and no `act_sub`, so the revocation lineage holds, and the row no longer names the delegating actor.
- Adapters receive `CallerIdentity.Act == nil` for the rotated token, although §15 (External API Surface) states that adapters render `Act.Sub` as the delegating actor.
- Found by the security review of 0164.S1 (revision 4) on 2026-11-22 (finding 5). The behaviour predates revision 4.

## Gap

A delegated agent can drop its delegation provenance from its token by rotating it.

## Suggested resolution

State in §13.3 that a self-rotation preserves the subject's `act` claim, and have `Validate` copy it, with `derivedLineage` recording the replaced row's `act_sub` alongside its `parent_jti`. This changes the specification and needs the owner.
