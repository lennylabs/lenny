---
id: PLAY-I2
status: open
kind: spec-defect
severity: low
requirements: []
---

# A playground logout revokes the bearer in Redis and leaves its `issued_tokens` row live

## Specification

§27.3.1 (OIDC cookie-to-MCP-bearer exchange), "Session record backing store": logout, user invalidation, idle timeout, and admin revocation revoke a playground bearer by a `pg:revoked:{jti}` marker in Redis, which the per-request check consults. §10.2 (Authentication) "Playground mint invariants" invariant 6, added by change 0167, records every playground bearer in `issued_tokens`. §13.3 (Credential Flow) **Authoritative durability for revocation.** names Postgres as the sole authoritative store for token revocation.

## Evidence

- `pkg/gateway/playground/auth.go`, `revokeSessionRecord`: the logout and idle paths delete the session record and write the Redis deny-list entries, and do not stamp `revoked_at` on the bearer's `issued_tokens` row.
- `pkg/gateway/playground/token.go`, `recordBearer`: the bearer's row is written live at mint time.
- The gateway refuses the bearer through the Redis check of the playground path, so the bearer is refused on the gateway. A reader of `issued_tokens`, such as `lenny-ops`'s revocation set or the admin issued-token listing, sees the row as live until its `exp`.
- Found by the general review of 0167.S1 on 2026-11-22 (finding 12).

## Gap

Two stores disagree about a logged-out playground bearer, and the store §13.3 names as authoritative is the one that says it is live.

## Suggested resolution

Have the playground revocation path also stamp `revoked_at` on the bearer rows it revokes, or state in §27.3.1 that a playground bearer's revocation by logout lives in Redis only and that `issued_tokens` rows of playground bearers are not authoritative for it. The owner decides.
