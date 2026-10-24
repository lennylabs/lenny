# Non-spec changes — lenny-ops remediation locks cannot run on a Redis Cluster

## Design (implementation-facing)

CODE-1 implements SPEC-1 and SPEC-2 in `pkg/ops/coordination/redisstore`. Every key moves under the `ops:{lock}:` tag, the acquire script stamps the epoch, and one compare-and-set script replaces `persist` and the id-only release for Extend, Steal, and Release. `RemoveByID` keeps the id-matched `releaseScript`. `scanAll`, `readByScope`, `conflictFromBlob`, `Epoch`, `IncrementEpoch`, `SetEpoch`, `ServerTime`, `Tier`, and `Reap` keep their logic and pick up the new constants. TEST-1 runs the store under the slot guard on miniredis and on `clustertest`, TEST-2 runs it on a real Redis Cluster, and TEST-3 turns on the tier-0 and tier-11 gates. REC-1 records Part 1. REC-2 is the Part 2 condition audit; it adds no code.

## Staged code changes

### CODE-1 · redisstore: tagged keys, epoch-stamping acquire script, and one CAS script

Targets: `pkg/ops/coordination/redisstore/redisstore.go`; the comments in `cmd/lenny-ops/locks.go` and `cmd/lenny-ops/services_wiring.go` that name `ops:lock:{scope}`.

**Constants.** Replace the key constants with

```go
const (
	scopePrefix = "ops:{lock}:scope:"
	idxPrefix   = "ops:{lock}:id:"
	epochKey    = "ops:{lock}:epoch"
)
```

`scopeKey` and `idxKey` keep their bodies.

**acquireScript.** Replace the script with the following. KEYS are the scope key, the ID key, and the epoch key. ARGV are the lock ID, the lock JSON built in Go with `Epoch` 0, the PTTL in milliseconds, and the scope.

```lua
if redis.call('EXISTS', KEYS[1]) == 1 then
  return {1, redis.call('HGET', KEYS[1], 'data')}
end
local e = tonumber(redis.call('GET', KEYS[3]) or '0')
local l = cjson.decode(ARGV[2])
l.epoch = e
local d = cjson.encode(l)
redis.call('HSET', KEYS[1], 'id', ARGV[1], 'data', d)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('SET', KEYS[2], ARGV[4], 'PX', ARGV[3])
return {0, tostring(e)}
```

`Acquire` keeps the `serverTime` call before the script, deletes the `s.Epoch(ctx)` call, and passes the three keys. A reply of `{1, data}` returns `conflictFromBlob(req.Scope, data)`. A reply of `{0, epoch}` sets `lock.Epoch` from the parsed epoch string and returns the lock. Any other reply, or a parse failure, returns `coordination.ErrStoreUnavailable`. The Lua `false` / `redis.Nil` branch goes away.

**casScript.** Add the following. KEYS are the scope key and the ID key. ARGV are the expected stored `data` string, the new `data` string or `''` for delete, the PTTL in milliseconds, the scope, and the lock ID.

```lua
if redis.call('HGET', KEYS[1], 'data') ~= ARGV[1] then return 0 end
if ARGV[2] == '' then
  redis.call('DEL', KEYS[1], KEYS[2])
  return 1
end
redis.call('HSET', KEYS[1], 'id', ARGV[5], 'data', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('SET', KEYS[2], ARGV[4], 'PX', ARGV[3])
return 1
```

**mutate.** Add an unexported helper that Extend, Steal, and Release call, of the form

```go
func (s *Store) mutate(ctx context.Context, id string, ttl int,
	decide func(cur coordination.Lock, now time.Time) (*coordination.Lock, error)) (*coordination.Lock, error)
```

It loops at most 3 times. Each attempt:

1. Calls `readByID`. An error returns `unavailable(err)`. A nil lock returns `&coordination.Error{Code: coordination.ErrCodeNotFound}`.
2. Reads `serverTime`. An error returns `unavailable(err)`.
3. Calls `decide(cur, now)`. An error is returned unchanged. A nil lock means delete.
4. Runs `casScript` with the raw string `readByID` returned as ARGV[1]. A script error returns `unavailable(err)`. A result of 1 returns the new lock (nil for delete). A result of 0 starts the next attempt.

After 3 attempts that each returned 0 it returns `&coordination.Error{Code: coordination.ErrCodeConflict}` with a message naming the lock ID. It never returns `coordination.ErrStoreUnavailable` for exhaustion.

The `decide` functions:

- Extend: return `ErrCodeNotOwned` when `cur.AcquiredBy != caller`; otherwise set `ExpiresAt = now + NormalizeTTL(ttlSeconds)` and increment `Revision`.
- Steal: set `StolenFrom = cur.AcquiredBy`, `AcquiredBy = req.AcquiredBy`, `OperationID = ""`, `AcquiredAt = now`, `ExpiresAt = now + NormalizeTTL(req.TTLSeconds)`, and increment `Revision`. Steal has no holder check.
- Release: return `ErrCodeNotOwned` when `cur.AcquiredBy != caller`; otherwise return nil.

The new `data` string is `json.Marshal` of the lock `decide` returned.

**readByID.** Change it to return the decoded lock and the raw stored `data` string: `(lock *coordination.Lock, raw string, err error)`. It returns a nil lock with a nil error when the ID key or the scope hash is absent, when the blob does not decode, or when the decoded lock's `ID` differs from the requested id. `Get` and `RemoveByID` take the lock and ignore the raw string. `readByScope` may delegate to a shared decode helper; its callers and results stay the same.

**RemoveByID.** Keeps `releaseScript` (the id-matched delete, regardless of holder) over `scopeKey(lock.Scope)` and `idxKey(lockID)`.

**Deletions.** Delete `persist`. `Release` no longer calls `releaseScript`.

**scanAll.** Keeps `redisconn.ScanKeys(ctx, s.client, "", scopePrefix+"*", 256, ...)` with no routing key.

**Comments.** Rewrite the package doc and the `scanAll` doc comment to describe the `ops:{lock}:` keys, the in-script epoch stamp, the `TIME` read and relative expiry (§25.4 Storage Tiers), and the CAS mutations, and drop the F-12.4.28 class B/C text. Cite `§25.4 (Storage Tiers)` and `§12.4 (Redis HA and Failure Modes)` with no line numbers. Remove the "a steal that re-keyed the scope" wording from the `releaseScript` and `RemoveByID` comments, because a steal keeps the lock ID; the `RemoveByID` comment also drops its line citation. In `cmd/lenny-ops/locks.go` and `cmd/lenny-ops/services_wiring.go`, replace `ops:lock:{scope}` with `ops:{lock}:scope:<scope>`.

**IMPLEMENTOR'S CHOICE:** how the acquire reply is decoded (a type switch over `[]interface{}` or a small helper). Any non-conforming reply maps to `coordination.ErrStoreUnavailable`, and the stamped epoch returned to the caller is the value the script read.

## Staged schema, chart, and migration changes

None.

## Staged docs changes

### REC-1 · Close F-12.4.28 and record Part 1 on P6

Targets: `BUILD-GAPS.md`, finding "F-12.4.28 — Gateway and ops Redis stores issue multi-key commands across hash slots, which Redis Cluster rejects", and `gateway-runtime-comms-remediation.md` §10.2 entry "**P6. `lenny-ops` remediation-lock keys and the Redis key brace rule**".

1. Change the F-12.4.28 heading from `### - [ ] F-12.4.28 — … [High] — OPEN` to `### - [x] F-12.4.28 — … [High] — CLOSED`, the form F-12.4.29 uses.
2. In the existing "**Resolution (classes A and A′, …):**" paragraph, replace "(coordination exempt until P6)" with a clause stating that the exemption ended with proposal 0099 Part 1, and replace "the finding stays OPEN for class B/C" with a pointer to the class B/C resolution below.
3. Append a "**Resolution (class B/C, <merge ref>):**" paragraph. It records that proposal 0099 put the lock, ID, and epoch keys under the `ops:{lock}:` hash tag, that the acquire script stamps the epoch, and that Extend, Steal, and Release use one CAS script. It records that the Extend, Steal, and Release ownership race (a stale Extend or Release acting on a stealer's lock) closed in the same change, so no separate finding is filed, and that the tier-0 `redisSlotGuardExempt` entry for `pkg/ops/coordination/redisstore` is removed. It names the TEST-1, TEST-2, and TEST-3 tests by file and function.
4. In P6, leave the checkbox unticked. Replace "(not yet written)" with a pointer to proposal 0099, and add a progress line stating that Part 1 (keys, brace rule, CAS) landed at <merge ref> and closed F-12.4.28 class B/C, and that P6 stays open until the Part 2 topology relaxation and audit land. REC-2 ticks it.

### REC-2 · (Part 2) Condition audit and findings

Targets: a `## Condition audit (REC-2)` section appended to this file at implementation, `BUILD-GAPS.md` (one new finding per unmet condition), and `gateway-runtime-comms-remediation.md` §10.2 P6.

1. Build a table with one row per Redis store package (the set the tier-0 `scanRedisUsage` enumerates, plus the lenny-ops lock and escalation stores) and one column per condition of §12.4 **Shared Redis topology** (SPEC-4). The single-slot and `SCAN` columns cite `TestRedisStoresInstallSlotGuard_spec_12_4` and `TestRedisScanRoutesByNode_spec_12_4` once the exemption list is empty. The durability and fail-closed columns cite each store's error mapping (for example `coordination.ErrStoreUnavailable` and the lease store's Postgres fallback). The lease column cites the Sweeper TTL in `pkg/gateway/coordination/coordination/coordination.go` and `DefaultCoordinationLeaseTTL` in `pkg/gateway/sessionserver/sessionserver.go`.
2. Verify these leads and file each confirmed one in `BUILD-GAPS.md`:
   - (i) The at-bind lease TTL is fixed at 60s while `--coordination-interval` is configurable, so a non-default interval breaks the four-interval condition at bind.
   - (ii) `Service.Reconcile` treats a failed Redis `Epoch` or `ActiveLocks` read as an empty set and still clears the Postgres-down state, which is a fail-open on a Redis failure path.
3. Each unmet cell becomes a BUILD-GAPS finding. An unmet cell does not block this proposal.
4. Tick P6 and add a line naming the audit section and the findings it filed.

## Testing

### TEST-1 · Tier-1 store tests

Target: `pkg/ops/coordination/redisstore/redisstore_test.go`. Every test carries `// spec: 25.4 (Storage Tiers)`, and the slot tests also carry `12.4 (Redis HA and Failure Modes)`.

The existing `clustertest` test already satisfies the tier-0 gate. The guard must also wrap `newStore`, so that every miniredis test runs the single-slot path, as the `newStore` comment requires.

- (a) `newStore` wraps the client in `slotguard.Install`, and its comment drops the P6 text. Add a backend table with two entries, a guarded miniredis and `clustertest.New(t, 3)`, and run the existing operation and epoch tests over both. A test that needs clock control on the cluster backend calls `FastForward` or `SetTime` on `c.ServerFor(key)`. Do not add a separate table-driven cluster test.
- (b) Negative control: an `EVAL` through the guarded `newStore` client over `ops:{lock}:scope:x` and an untagged key returns `slotguard.ErrCrossSlot`. It proves the guard is wired on this package's client.
- (c) `TestRedisLockKeysShareOneSlot_spec_25_4`: the scope key, the ID key, and the epoch key hash to one slot (`slotguard.Slot`) for scopes including `epoch`, `id:x`, and `pool:{evil}`, and acquiring scope `epoch` leaves the epoch key a string that `IncrementEpoch` still increments.
- (d) Rename `TestRedisListCoversEveryClusterMaster_spec_25_4` to `TestRedisListClusterCompleteness_spec_25_4`. Seed it through `Acquire` and assert one slot and a complete, scope-ordered list. Its comment notes that `TestRedisScanRoutesByNode_spec_12_4` pins the scan of every node.
- (e) Injected interleavings. Each case injects an operation through a second, unhooked client from a go-redis `ProcessHook` on the store's client that fires once, immediately before the first script command (`EVALSHA` or `EVAL`) of the operation under test. No test seam is added to `redisstore.go`.
  - (e1) Epoch: the hook runs `INCR ops:{lock}:epoch` before the acquire script. The returned lock and the stored record carry the incremented epoch. This is the assertion that distinguishes the new code from the old, which read the epoch in Go before the script.
  - (e2) Stale extend: holder `alice` extends while the hook steals the lock for `bob`. Extend returns `LOCK_NOT_OWNED`, and the stored record is `bob`'s with `stolenFrom` `alice`.
  - (e3) Stale release: holder `alice` releases while the hook steals the lock for `bob`. Release returns `LOCK_NOT_OWNED`, and `bob`'s lock and its ID key survive.
  - (e4) Disappearance: the hook deletes the lock (release by the holder) before an Extend; the Extend returns `REMEDIATION_LOCK_NOT_FOUND`. In a second case the hook releases the lock and acquires the same scope under a new ID before a `Get` of the old ID resolves the scope hash; `Get` and `Extend` of the old ID return `REMEDIATION_LOCK_NOT_FOUND`, and the new lock is untouched.
  - (e5) Retry: a hook that changes the record once (a same-holder Extend) makes the next operation retry and succeed with `revision` incremented twice. A hook that changes the record before every script makes the operation return `REMEDIATION_LOCK_CONFLICT` after three attempts, and the stored record equals the hook's last write. The test asserts the error is not `ErrStoreUnavailable`.
- (f) Randomized checker, run with `-race`: goroutines issue Acquire, Extend, Release, and Steal over a few scopes with random holders, while a reader polls the stored records. Invariants: a successful Extend or Release returns only when the stored record's `acquiredBy` equaled the caller at the CAS; a stale Extend never overwrites a stealer's record; the stored `revision` a concurrent reader sees for one lock ID never decreases, and every steal increases it. Steals are exempt from the holder check, per §25.4 Stealing.
- (g) Expiry: after Acquire, and again after Extend, the PTTL of the scope key and of the ID key each equals the requested TTL and is at least `expiresAt - acquiredAt`.
- (h) Outage: close miniredis and assert `ErrStoreUnavailable` from Acquire, Get, List, ActiveLocks, Extend, Release, Steal, RemoveByID, Epoch, IncrementEpoch, and SetEpoch. Tier, Reap, and ServerTime are excluded; ServerTime returns the raw error.
- (i) cjson round trip: a lock with `operationId` empty and `stolenFrom` set after a steal decodes after Acquire, Extend, and Steal with every field equal to the value Go wrote, except `epoch`, which equals the stamped value.

Add each new or renamed test to section `"25.4"` in `tests/spec-map.json`, and the slot tests also to `"12.4"`.

### TEST-2 · Real Redis Cluster and tier-2 coverage

Targets: a new `tests/tier4_integration/ops_lock_redis_cluster_test.go`, and `tests/tier2_component/coordination/tier_transition_test.go`.

**IMPLEMENTOR'S CHOICE:** whether the tier-4 test is a new file or a subtest of `tests/tier4_integration/redis_cluster_smoke_test.go`. The function name `TestRedisClusterOpsLockStore_spec_25_4`, its `// diagnosis:` comment, and its spec-map entry are fixed either way.

- `TestRedisClusterOpsLockStore_spec_25_4` runs on `containers.StartRedisCluster` with `// spec: 25.4 (Storage Tiers), 12.4 (Redis HA and Failure Modes)`. It runs acquire, conflict, extend, steal, release, RemoveByID, list, IncrementEpoch then Acquire (the stamp equals the new epoch), and SetEpoch with a lower and a higher value (the MAX rule). It asserts that no call returns a CROSSSLOT error, that every lock key sits on one master, and that a lock decodes after the `cjson` round trip on real Redis.
- In `tier_transition_test.go`, wrap the Redis clients with `slotguard.Install`, and change the comments and the failure message that quote `ops:lock-epoch:current` to `ops:{lock}:epoch`.

### TEST-3 · Gates

Targets: `tests/tier0_static/redis_cluster_safety_test.go` and `tests/tier11_docs/redis_key_prefix_registry_test.go`.

- Tier 0: delete the `pkg/ops/coordination/redisstore` entry and its comment from `redisSlotGuardExempt`. Keep the empty map, its doc comment, and the stale-entry loop.
- Tier 11, in `TestRedisKeyPrefixRegistryCompleteness_0032`:
  - Change the check (e) list to `{"lenny:token:", "ops:lock:", "ops:escalations:", "ops:lockidx:", "ops:{lock}:"}`.
  - Assert that `s124` contains `**Braces in key patterns.**`.
  - Add `s254 := specSection(t, filepath.Join(specDir, "25_agent-operability.md"), "## 25.4 ")`, then assert that `s254` contains `ops:{lock}:epoch` and contains none of `ops:lock:`, `ops:lockidx:`, or `ops:lock-epoch`. That covers the Storage Routing row and the Tier 2 paragraph as well as the epoch key.
  - Add `25.4 (Storage Tiers, Storage Routing)` to the test's `// spec:` annotation.

## Edge cases and accepted failure modes

- A CAS operation that loses three times returns 409 and writes nothing; the caller retries at the API level (SPEC-1, CAS rule 4).
- A Redis error inside `mutate` maps to `ErrStoreUnavailable` and the tiered Service falls back as it does today. Only ownership and exhaustion outcomes are 403, 404, or 409.
- The keyless `TIME` read may be answered by a node other than the lock slot's owner (summary D4).
- Locks written under the old key names are not migrated. No deployments exist, and Redis lock state is ephemeral.

## Files touched on application (non-spec)

- pkg/ops/coordination/redisstore/redisstore.go
- pkg/ops/coordination/redisstore/redisstore_test.go
- cmd/lenny-ops/locks.go
- cmd/lenny-ops/services_wiring.go
- tests/tier4_integration/ops_lock_redis_cluster_test.go (or tests/tier4_integration/redis_cluster_smoke_test.go)
- tests/tier2_component/coordination/tier_transition_test.go
- tests/tier0_static/redis_cluster_safety_test.go
- tests/tier11_docs/redis_key_prefix_registry_test.go
- tests/spec-map.json
- BUILD-GAPS.md
- gateway-runtime-comms-remediation.md
- proposals/0099_fix_lenny-ops-remediation-locks-cannot-run-on-a-redis/0099_fix_lenny-ops-remediation-locks-cannot-run-on-a-redis.non-spec-changes.md (REC-2 audit section)
