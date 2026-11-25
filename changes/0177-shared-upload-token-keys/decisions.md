# Decisions: 0177 A shared upload-token key ring and consumed-token tracker

## Open decisions

- **O1. The store for the signing keys and the consumed state.** "Detailed design", SK-1, compares the options.
  - (a) Postgres only. The keys sit in a platform table, each envelope-encrypted under the KMS alias `platform:upload-token-signing`, and the consumed state is a column of the session row, set in the `finalizing` → `ready` write.
  - (b) Redis only. The keys sit in Redis, envelope-encrypted under the same alias, and the consumed state is a `SET NX` of the digest with a TTL.
  - (c) Both. The keys sit in Postgres as in (a), and the consumed state sits in Redis as in (b).
  - Recommendation: (a). The upload and finalize paths already depend on Postgres for the session row, so (a) adds no store and no failure mode to them, and the consumed check can ride on a row read the paths already make. Postgres keeps the keys across a restart, which (b) does not, and a lost key ring under (b) leaves every session in `created` unable to finalize, because §7.1 does not reissue a token. (b) and (c) add a Redis round trip per upload and a refusal of uploads during a Redis outage while Postgres is up, and they need a memory fallback for the development mode that runs without Redis.
- **O2. The behavior when the ring cannot be refreshed.** Under SK-4, a replica that has not refreshed its ring for longer than the overlap window stops minting tokens, so session creation on that replica answers `503`, while verification continues with the loaded ring.
  - (a) Stop minting after the overlap window without a refresh, as SK-4 states.
  - (b) Keep minting with the last loaded active key for as long as the process runs, and rely on peers that have the same ring.
  - Recommendation: (a). After the overlap window a peer may have rotated to a key this replica has not loaded and retired the key this replica signs with, so a token from this replica may not verify elsewhere. (a) fails closed, and the window is long enough that a short Postgres outage does not reach it.

## Decisions

1. **A change for a shared key ring and a shared consumed-token tracker (IK-4).** The per-replica upload-token key ring and consumed tracker are replaced by shared ones, through a change whose design the owner reviews before it is settled, with the choice between Redis, Postgres, or both. The change lands before the milestones that run several gateway replicas. Owner's answer to IK-4 on the M3 readiness decision page, 2026-11-25, option (a), with the words "but I would like to see the design first (Redis, Postgres, or both)". The question arose from the review of change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md), whose replay rebuilds a token only on a replica whose ring holds the signing key.
