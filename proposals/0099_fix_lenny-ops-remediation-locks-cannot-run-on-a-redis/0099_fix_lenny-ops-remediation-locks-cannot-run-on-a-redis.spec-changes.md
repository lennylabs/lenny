# Spec changes — lenny-ops remediation locks cannot run on a Redis Cluster

## Design (as the spec must state it)

Part 1. §25.4 names every Tier 2 remediation-lock key under the `ops:{lock}:` prefix, whose `{lock}` segment is a Redis Cluster hash tag, so the scope key, the ID key, and the epoch key hash to one slot. SPEC-1 owns the key layout, the acquire script, the `TIME` and expiry statement, and the compare-and-set rules for extend, steal, release, and reconciliation removal. SPEC-2 renames the epoch key in the Outage epochs paragraph, corrects the Clock Source lead-in and Tier 2 bullet, and points the Storage Routing row at the Tier 2 paragraph. SPEC-3 adds the §12.4 **Braces in key patterns** rule. SPEC-1 relies on that rule when it names `{lock}` as a hash tag.

Part 2. SPEC-4 adds the §12.4 **Shared Redis topology.** paragraph, which permits one Sentinel instance at Tiers 1 and 2 and one Redis Cluster at Tier 3 to serve every concern while its listed conditions hold. It is the single home of those conditions. SPEC-5 rewrites the §12.4 migration pre-plan as a whole-deployment `redis.provider` cutover, and SPEC-6 rewrites the §17.8.2 Tier 3 rows and the §16.5 `DurableInboxRedisUnavailable` row to cite SPEC-4 rather than assume separate instances at Tier 3.

## Edge cases and accepted failure modes

- A scope whose text contains braces, such as `pool:{evil}`, stays in the lock slot, because the key's first braced substring is `{lock}` (SPEC-3, last sentence).
- A scope named `epoch` or `id:x` does not collide with the epoch key or an ID key, because scope keys carry the `scope:` segment (SPEC-1 key layout).
- A lock ID whose ID key outlives a re-acquisition of the same scope under a new ID is treated as absent (SPEC-1, CAS paragraph).
- All remediation locks share one Redis Cluster slot, and so one primary. This is accepted at remediation-lock volume.

## Staged edits

### SPEC-1 · spec/25_agent-operability.md § 25.4 Storage Tiers and Error Codes

**Edit 1.** Under the heading `#### Storage Tiers`, in the tier table, replace the row

```
| 2 | Redis | `SET NX EX` with Lua script writing `{lockID, epoch, revision}` atomically | All replicas (shared keyspace) | Postgres unreachable |
```

with

```
| 2 | Redis | Lua script over the `ops:{lock}:` keys that checks the scope key and writes the lock with the current outage epoch atomically | All replicas (shared keyspace) | Postgres unreachable |
```

The row stays one physical line.

**Edit 2.** Under the same heading, after the `**Tier 2 — Redis:**` label, replace the text from "Uses a Lua script on key `ops:lock:{scope}` that atomically:" through "Release uses a similar CAS script that verifies the lock ID matches before deleting." with:

```
Every Tier 2 key starts with `ops:{lock}:`, whose `{lock}` segment is a Redis Cluster hash tag ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) **Braces in key patterns**), so every Tier 2 key hashes to one slot:

- `ops:{lock}:scope:<scope>` (the scope key) is a hash whose `id` field holds the lock ID and whose `data` field holds the serialized lock record.
- `ops:{lock}:id:<lockID>` (the ID key) holds the scope, so a lock ID resolves to its scope key.
- `ops:{lock}:epoch` (the epoch key) holds the Redis-side outage epoch (see Tier Transitions).

`<scope>` and `<lockID>` are placeholders: the key carries the value without angle brackets.

Acquire runs one Lua script over the scope key, the ID key, and the epoch key that atomically:
1. Returns `409 REMEDIATION_LOCK_CONFLICT` with the existing lock record when the scope key exists.
2. Reads the current outage epoch from the epoch key, treating an absent key as 0, and stamps it as the record's `epoch`.
3. Writes the serialized `Lock` record (see Lock Struct) to the scope key and the scope to the ID key, and sets both keys to expire after the requested TTL.

`lenny-ops` computes `acquiredAt` and `expiresAt` from a Redis `TIME` reading (see Clock Source) taken immediately before it runs a script that writes a lock record, and that script sets the expiry of the keys it writes as a TTL relative to the moment it runs.

Extend, steal, and release resolve the lock ID through the ID key, read the stored lock record, and apply their change with one compare-and-set script over the scope key and the ID key. The script writes the new record, or deletes both keys on release, only when the stored record is identical to the record that was read. A lock ID whose scope key holds a different lock ID is treated as absent. When the comparison fails, `lenny-ops` reads the lock again and decides again:
1. An absent lock returns `404 REMEDIATION_LOCK_NOT_FOUND`.
2. On extend and release, a lock whose `acquiredBy` is no longer the caller returns `403 LOCK_NOT_OWNED`.
3. Any other change retries the operation.
4. After three failed comparisons the operation returns `409 REMEDIATION_LOCK_CONFLICT` and writes nothing.

The split-brain reconciliation (see Tier Transitions) removes a losing Redis lock with a script that deletes the scope key and the ID key only when the stored lock ID matches, regardless of `acquiredBy`.

The Redis client re-sends a command whose reply it lost, so each Tier 2 write in this section tolerates a repeat: an acquire whose scope key already holds its own lock ID at revision 0 returns that lock in place of the conflict that acquire step 1 returns, a compare-and-set whose stored record is identical to the new record counts as success without a write, a repeated release finds the lock absent, a repeated reconciliation removal deletes nothing, and a repeated `INCR` of the epoch key (see Tier Transitions) advances the epoch twice, which keeps it monotonic.
```

**Edit 3.** Under the `#### Error Codes` heading that follows `#### Multi-Replica Behavior`, in the `REMEDIATION_LOCK_CONFLICT` row, replace "Another agent holds a lock on this scope." with "Another agent holds a lock on this scope, or a Tier 2 extend, steal, or release exhausted its compare-and-set retries (see Storage Tiers)." The row stays one physical line.

### SPEC-2 · spec/25_agent-operability.md § 25.4 Tier Transitions and Split-Brain Prevention, Clock Source, and Storage Routing

**Edit 1.** Under the heading `#### Tier Transitions and Split-Brain Prevention`, in the **Outage epochs.** paragraph, replace

```
stored **in both Postgres (`ops_lock_epoch.current`) and Redis (`ops:lock-epoch:current`)**
```

with

```
stored **in both Postgres (`ops_lock_epoch.current`) and Redis (`ops:{lock}:epoch`)**
```

**Edit 2.** In the bullet beginning "On Postgres → Tier 2 transition:", replace "`INCR ops:lock-epoch:current`" with "`INCR ops:{lock}:epoch`".

**Edit 3.** In the bullet beginning "On Tier 2 → Postgres recovery:", replace

```
the service reads `MAX(ops:lock-epoch:current_redis, ops_lock_epoch.current_postgres)`
```

with

```
the service reads the maximum of `ops:{lock}:epoch` and `ops_lock_epoch.current`
```

**Edit 4.** Under the heading `#### Clock Source`, replace

```
All TTLs and `expiresAt` timestamps are authored by a single clock per tier:
- Tier 1: Postgres `now() at time zone 'UTC'`.
- Tier 2: Redis `TIME` command (via Lua script).
```

with

```
All TTLs and `expiresAt` timestamps are authored by a server-side clock source per tier:
- Tier 1: Postgres `now() at time zone 'UTC'`.
- Tier 2: Redis `TIME` command, which any primary of a Redis Cluster may answer.
```

**Edit 5.** Under the heading `### Storage Routing` (inside §25.4), in the `**Redis:**` table, replace the row

```
| Remediation locks (`ops:lock:{scope}`) | `PlatformRedis()` | Platform-scoped coordination keys. |
```

with

```
| Remediation locks (`ops:{lock}:*`, see Tier 2 — Redis) | `PlatformRedis()` | Platform-scoped coordination keys under one hash tag. |
```

The row stays one physical line. The escalation row `ops:escalations:{id}` is unchanged; under SPEC-3 its `{id}` reads as a placeholder.

After SPEC-1 and SPEC-2, an exact search over `spec/` for `ops:lock:`, `ops:lockidx:`, and `ops:lock-epoch` returns no hits.

### SPEC-3 · spec/12_storage-architecture.md § 12.4 Redis HA and Failure Modes

Insert a new paragraph after the paragraph beginning "The tenant-leading convention is enforced in the Redis wrapper layer" and before the paragraph beginning "An integration test (`TestRedisTenantKeyIsolation`)":

```
**Braces in key patterns.** A braced segment in a Redis key pattern in this specification is a placeholder: the key carries the value in its place, without braces. A braced segment is a Redis Cluster hash tag only where this paragraph or the text that defines the pattern names it a hash tag; the key then carries the braces, and Redis Cluster hashes only the text between them. The hash tags are the `{tenant_id}` segment of the `t:{tenant_id}:` prefix, the `{root_session_id}` segment of the `{root_session_id}:dlg:` prefix ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) R-04), and the `{pod_id}` segment of the `lenny:pod:{pod_id}:` prefix, wherever these prefixes appear in this specification. Every other braced segment in the table is a placeholder. Redis Cluster hashes the first braced substring of a key, so a pattern that names a hash tag places it before any other braced text.
```

The paragraph quotes no `ops:` key (tier-11 check (e)).

### SPEC-4 · spec/12_storage-architecture.md § 12.4 Redis HA and Failure Modes

**Edit 1.** Insert a new paragraph after the paragraph beginning "At Tiers 1 and 2, all concerns run on a single Sentinel instance." and before the paragraph beginning "**Tier 3 Redis write throughput quantification:**":

```
**Shared Redis topology.** One Redis deployment may serve every concern: a single Sentinel instance at Tiers 1 and 2, and a single Redis Cluster at Tier 3. This holds while all of the following are true. Every Redis store keeps each script, transaction, and multi-key command inside one hash slot, and runs each cross-slot `SCAN` on every primary (**Braces in key patterns** above). No coordination guarantee depends on Redis durability, per the data-at-rest posture above. Every Redis failure path is bounded and ends in the fallback or the fail-closed state that the failure-behavior table, or the section that owns the store, states for it. The session coordination lease TTL is at least four renewal intervals. The separation by concern above remains available when a concern's ceiling signals trigger.
```

The paragraph beginning "At Tiers 1 and 2" and the throughput paragraph stating that Sentinel is appropriate only through Tier 2 stay unchanged.

**Edit 2.** In the **Tier 3 Redis write throughput quantification** table, replace the row

```
| Lease renewals (`SET` with TTL)         | ~1,000/s                 | Per-session lease heartbeat; one per coordination TTL / 2 — this is the only row that scales directly with concurrent session count. |
```

with

```
| Lease renewals (`SET` with TTL)         | ~1,000/s                 | Per-session lease heartbeat; one per coordination renewal interval (`--coordination-interval`, default 15s, a quarter of the default 60s lease TTL) — this is the only row that scales directly with concurrent session count. |
```

The row stays one physical line, and the ~1,000/s estimate is unchanged.

### SPEC-5 · spec/12_storage-architecture.md § 12.4 Redis HA and Failure Modes (Redis Cluster migration pre-plan)

**Edit 1.** Replace the intro sentence

```
**Redis Cluster migration pre-plan (Tier 2→3 transition):** For deployments targeting Tier 3 scale, Redis Cluster migration for the Quota/Rate Limiting instance must be planned **before** the Tier 2→3 promotion decision — not reactively once ceiling signals have already fired in production. The migration procedure is:
```

with

```
**Redis Cluster migration pre-plan (Tier 2→3 transition):** For deployments targeting Tier 3 scale, the migration of the deployment's Redis from Sentinel to Redis Cluster must be planned **before** the Tier 2→3 promotion decision rather than after ceiling signals have fired in production. The migration procedure is:
```

**Edit 2.** Replace step 3

```
3. **Interface compatibility:** No application code changes are required. Each store role interface ([Section 12.6](#126-interface-design)) already abstracts the Redis client. The `QuotaStore` and rate limiter implementations must use `CLUSTER KEYSLOT`-aware client libraries (e.g., `go-redis/v9` with cluster mode) — verify this before migration.
```

with

```
3. **Interface compatibility:** No application code changes are required. Each store role interface ([Section 12.6](#126-interface-design)) already abstracts the Redis client. Before migration, verify that every Redis store meets the conditions of **Shared Redis topology** above.
```

**Edit 3.** Replace step 4 (the item beginning "4. **Migration steps:**" and ending "Cluster is not required for low-throughput coordination workloads.") with

```
4. **Migration steps:** (a) Deploy the Cluster in parallel with the existing Sentinel instance; (b) validate every Redis-backed role against the Cluster in staging for 24h; (c) during a maintenance window, stop every component that connects to Redis, empty the Cluster, copy every key with its remaining TTL from the Sentinel instance to the Cluster, switch `redis.provider` to `cluster`, and restart the components; (d) decommission the Sentinel instance.
```

**Edit 4.** Replace step 5

```
5. **Rollback:** The per-tenant feature flag allows individual rollback to Sentinel if correctness issues are observed. Any quota counters that diverged between the two stores during the migration window must be reconciled by re-reading from Postgres (same procedure as fail-open reconciliation above).
```

with

```
5. **Rollback:** Until step 4(d), the deployment rolls back by repeating step 4(c) with the roles of the Cluster and the Sentinel instance exchanged and `redis.provider` restored to its previous value.
```

Steps 1 and 2 stay unchanged.

### SPEC-6 · spec/17_deployment-topology.md § 17.8.2 Capacity Tier Reference; spec/16_observability.md § 16.5 Alerting Rules and SLOs

**Edit 1.** Under the heading `### 17.8.2 Capacity Tier Reference`, in the Redis table, replace the row

```
| Concern separation          | Single instance (all concerns)                          | Single instance; split if ceiling signals trigger ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) | Separate instances: coordination (Sentinel), quota (Cluster), cache/pub-sub (Sentinel or Cluster) |
```

with

```
| Concern separation          | Single instance (all concerns)                          | Single instance; split if ceiling signals trigger ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) | One Redis Cluster for every concern under [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) **Shared Redis topology**. Split a concern onto its own instance through its per-store-role connection string if its ceiling signals trigger. |
```

The row stays one physical line. The Topology row ("Redis Cluster (6+ nodes)" at Tier 3) is unchanged.

**Edit 2.** In the **Single-tenant Redis topology note.** paragraph, replace the sentence

```
The correct topology for single-tenant Tier 3 is **concern separation into multiple independent Sentinel instances** (not Redis Cluster), since each concern's ops/s remains well within single-node capacity (~150–200K ops/s):
```

with

```
A single-tenant Tier 3 deployment runs either one Redis Cluster for every concern, with its tenant-prefixed keys on one shard, or one Sentinel instance per concern through the per-store-role connection strings, sized as follows:
```

The per-concern table after it stays unchanged. In the paragraph after that table, delete the two sentences from "Redis Cluster is warranted only when **a single concern** exceeds" through "with simpler operations than Cluster.". The rest of that paragraph stays unchanged.

**Edit 3.** Under the heading `### 16.5 Alerting Rules and SLOs`, in the `DurableInboxRedisUnavailable` row, replace

```
deployments that have separated the two Redis instances (Tier 3 and beyond) should keep both alerts active
```

with

```
deployments that have separated the Coordination and Quota/Rate Limiting instances should keep both alerts active
```

The row stays one physical line.

## Spec files touched

- spec/25_agent-operability.md
- spec/12_storage-architecture.md
- spec/17_deployment-topology.md
- spec/16_observability.md
