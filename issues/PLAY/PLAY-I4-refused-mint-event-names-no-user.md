---
id: PLAY-I4
status: open
kind: spec-defect
severity: low
requirements: []
---

# A refused playground mint emits an audit event that names no user

## Specification

§10.2 (Authentication), **Playground mint invariants**: "Rejected playground mints emit a `playground.bearer_mint_rejected` audit event (fields: `tenant_id`, `subject_jti`, `subject_typ`, `invariant_violated`, `ingress_path`)". Invariant 6 of the same block states that the subject of the `oidc` and `dev` modes is not a token, so it carries no `jti`.

## Evidence

- `pkg/gateway/playground/audit.go`, `MintRejectedEvent`: the event carries `TenantID`, `SubjectJTI`, `SubjectTyp`, `InvariantViolated`, `IngressPath`, and `At`, and no user identifier. `emitMintRejected` fills it from its arguments.
- `pkg/gateway/playground/chainaudit.go`, `ChainAuditEmitter.EmitMintRejected`: the committed audit event's detail carries `subject_jti`, `subject_typ`, `invariant_violated`, and `ingress_path`.
- `pkg/gateway/playground/token.go`, the `oidc` path of `POST /v1/playground/token`: the subject built from the session record sets `Subject` to the record's `user_id` and leaves `JWTID` empty. `narrowSubjectScope` emits the `subject_scope_invalid` and `scope_intersection_empty` refusals with `subject.JWTID`, so an `oidc`-mode refusal carries an empty `subject_jti`, and the user is in `subject.Subject` but not in the event.
- Raised by the security review of 0162.S1 (finding 3). The owner chose option (a) of decision 3 of change 0162, question SM-3 of the M3 readiness review, on 2026-11-19: add `user_id` to the event's field set, filled on every refusal, in a later change.

## Gap

An operator cannot trace an `oidc`-mode mint refusal to a user, because the event's only subject identifier is the `jti` that an `oidc`-mode subject does not have. The refusal itself is unaffected.

## Suggested resolution

Add `user_id` to the `playground.bearer_mint_rejected` field set in §10.2, filled on every refusal from the subject's `sub` (the pasted bearer's `sub` in the `apiKey` mode, the session record's `user_id` in the `oidc` mode), and empty only when the refusal fires before the subject's claims are read. Carry it through `MintRejectedEvent`, `emitMintRejected`, and `ChainAuditEmitter.EmitMintRejected`, and keep the field set consistent with the `apiKey` refusals and the §13.3 rejection discipline it shares.
