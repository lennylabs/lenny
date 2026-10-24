# Summary: Co-locate the lenny-ops Tier 2 remediation-lock keys under one hash tag, make every lock mutation a single-slot script, and permit one Redis endpoint for every concern at Tier 3

## Summary

**Problem statement.** On a Redis Cluster, the lenny-ops Tier 2 remediation-lock store cannot carry out any lock operation. Its lock key, its undocumented id index, and its epoch key hash to unrelated slots, so the acquire script, the release script (which `RemoveByID` also runs), and the `persist` transaction behind Extend and Steal are each rejected with CROSSSLOT. The chart already builds a cluster client for lenny-ops, so the failure appears during any Postgres outage on a `redis.provider=cluster` install. On every topology, Acquire reads the epoch and Redis `TIME` outside its script, which contradicts the §25.4 Tier 2 text, and Extend, Steal, and Release check ownership in Go before an unconditional write, so a stale Extend or Release can act on a stealer's lock. §12.4 uses braces for both hash tags and placeholders without a rule that tells them apart. Part 2 records the owner's 2026-10-21 decision that one Redis endpoint may serve every concern, which today contradicts the §17.8.2 Tier 3 placement and the §12.4 migration pre-plan.

**What changes.**

- SPEC-1 and SPEC-2 rewrite the §25.4 Tier 2 storage text, the Error Codes conflict row, the Tier Transitions epoch key, the Clock Source lead-in and Tier 2 bullet, and the Storage Routing row around the `ops:{lock}:` hash tag, the in-script epoch read, the `TIME` read and relative expiry the code performs, and compare-and-set mutations.
- SPEC-3 adds the §12.4 **Braces in key patterns.** paragraph.
- CODE-1 rewrites `pkg/ops/coordination/redisstore` to the tagged keys, an acquire script that stamps the epoch, and one CAS script for Extend, Steal, and Release. TEST-1, TEST-2, and TEST-3 cover it at tiers 1, 2, and 4 and turn on the tier-0 and tier-11 gates.
- SPEC-4, SPEC-5, and SPEC-6 add the §12.4 **Shared Redis topology.** paragraph, correct the lease-renewal row, rewrite the migration pre-plan as a whole-deployment cutover, and rewrite the §17.8.2 Tier 3 rows and the §16.5 `DurableInboxRedisUnavailable` row.
- REC-1 closes F-12.4.28. REC-2 records the Part 2 condition audit and files each unmet condition.

**Decisions.**

- D1. The tag is spelled `ops:{lock}:` with typed sub-namespaces `ops:{lock}:scope:<scope>`, `ops:{lock}:id:<lockID>`, and `ops:{lock}:epoch`. §25.4 Storage Routing requires every ops key to start with `ops:`, and scope is free-form, so without the `scope:` and `id:` segments a scope named `epoch` would overwrite the epoch key with a hash.
- D2. One fixed tag covers every lock. The acquire script reads the epoch key atomically, so the epoch key must share a slot with every lock key, and a by-id operation cannot compute a per-scope tag before it reads the id index. One slot is accepted because lock traffic is low-volume admin traffic served at Tier 2 only during a Postgres outage.
- D3. The acquire script reads the epoch key, decodes the Go-built lock JSON with Redis's built-in `cjson`, sets `epoch`, re-encodes, and returns the stamped epoch. The stored `{id, data}` hash layout stays, so `scanAll` and `conflictFromBlob` do not change. miniredis v2.38.0 loads `cjson`, and `coordination.Lock` has no slice fields, so the round trip loses nothing Go reads.
- D4. The §25.4 `TIME` and `PEXPIREAT` text is corrected to the code mechanism: lenny-ops reads `TIME` before the script that writes a lock record, and that script sets a relative `PEXPIRE`. A keyless `TIME` on a cluster client can be answered by a node that does not own the lock slot, so an absolute `PEXPIREAT` from that clock could expire the key before its recorded `expiresAt`. A relative expiry applied after the `TIME` read keeps the key for at least the requested TTL after the reading, whichever primary answered it. `ServerTime` stays the single `TIME` surface the clock-skew sampler reads.
- D5. Every mutation that names a lock id is a compare-and-set script over the two co-located keys, and the ownership race is fixed in this proposal rather than filed. Part 1 makes Tier 2 writable on Cluster installs for the first time, `persist` is replaced in any case, and the race breaks the §25.4 caller-is-`acquiredBy` MUST and its "never silent success" rule. One `casScript` serves Extend, Steal, and Release and compares the stored `data` string with the raw string the caller read. No hash field, wire field, or error code is added.
- D6. CAS retry exhaustion returns 409 `REMEDIATION_LOCK_CONFLICT` (SPEC-1 CAS rules), never `ErrStoreUnavailable`, because that error sends the request to the in-memory tier and could create a second holder.
- D7. `readByID` returns the raw stored blob and returns (nil, nil) when the decoded lock's id differs from the requested id. Without the check, a by-id CAS could act on a different lock that re-acquired the scope between the index read and the hash read.
- D8. `RemoveByID` keeps its id-matched delete regardless of holder, over the co-located keys, because §25.4 reconciliation removes the losing Redis lock whoever holds it.
- D9. The §12.4 brace rule makes placeholder the default. It names the three existing tags wherever their prefixes appear, so no existing pattern is reclassified, and it quotes no `ops:` key so tier-11 check (e) holds. It adds one sentence placing a named tag before any other braced text, because Redis Cluster hashes the first braced substring and a caller value containing braces must not move a key.
- D10. `scanAll` stays node-routed (`redisconn.ScanKeys` with no routing key) over `ops:{lock}:scope:*`. It stays complete while the slot migrates during resharding.
- D11. The tier-0 `redisSlotGuardExempt` map stays, empty, with its stale-entry check.
- D12. Tier-11 check (e) adds `ops:{lock}:` and keeps `ops:lock:` and `ops:lockidx:`, so retired spellings cannot return to §12.4.
- D13. The §25.4 tier-table row 2 and the Clock Source Tier 2 bullet change with the Tier 2 paragraph. Otherwise `SET NX EX` and "via Lua script" would contradict the corrected paragraph in the same section.
- D14. Part 2 states its conditions once, in a new §12.4 paragraph after **Logical separation of Redis concerns**, and §17.8.2 and the pre-plan cite it. §12.4 owns the separation guidance and the pre-plan, and §17.8.2 already defers to §12.4 for splitting. The permission covers one Sentinel at Tiers 1 and 2 and one Redis Cluster at Tier 3; it does not reopen the §12.4 statement that Sentinel is appropriate only through Tier 2.
- D15. The pre-plan becomes a whole-deployment `redis.provider` cutover that copies every key from Sentinel to the Cluster while every Redis-connected component is stopped. Several roles hold Redis state with no durable copy (operator-opened breakers, playground revocation markers, delegation-budget counters, the durable inbox and DLQ, staged billing entries, and Tier 2 locks), so a discarding switch would lose them, and a rolling switch would split replicas across two keyspaces.
- D16. The §12.4 lease-renewal row is corrected to one renewal per `--coordination-interval`. Its "one per coordination TTL / 2" contradicts the four-interval condition, and the code renews four times per TTL (the Sweeper sets TTL to 4 × interval, the default lease TTL is 60s, and the interval defaults to 15s).
- D17. Part 1 and Part 2 are separate checklist deliverables. No Part 1 step depends on a Part 2 step, Part 1 is verified and recorded by REC-1 on its own, and Part 1 does not wait on the Part 2 audit.

**Watch out for.**

- `cjson` re-encodes the record, so the stored `data` string differs from Go's `json.Marshal` output for the same lock. The CAS expected value is always the raw string `HGET` returned, never a re-marshal of the decoded lock (CODE-1).
- Mapping CAS exhaustion, or any ownership outcome, to `ErrStoreUnavailable` reopens the two-holder path through the in-memory tier (D6, SPEC-1 CAS rules).
- The tier-0 gate passes as soon as the exemption is deleted, because the package's tests already call `clustertest.New`. It does not prove that `newStore` installs the slot guard, so review TEST-1(a) on its own.
- Tier-11 check (e) forbids `ops:{lock}:` anywhere in §12.4. SPEC-3 and SPEC-4 quote no `ops:` key, and an implementor must not add one while editing them.
- The SPEC-3 paragraph goes after the wrapper-layer paragraph. Placing it after the exception-summary paragraph lets the tier-11 exception-summary parser read it as part of that summary.
- Every table row SPEC-1, SPEC-2, SPEC-4, and SPEC-6 replace stays one physical line.
- The owner has not yet decided whether Part 2 stays in this proposal or moves to a sibling proposal. If it moves, checklist steps S4, S5, S6, and S12 leave with it and Part 1 is unchanged.

## Goals

- Every Tier 2 remediation-lock operation runs on a Redis Cluster with no CROSSSLOT error.
- The acquire script reads the epoch atomically, and the §25.4 text states the `TIME` read and expiry mechanism the code uses.
- A stale Extend or Release never acts on a lock that a steal replaced, and never reports success on it.
- §12.4 states which braces in a key pattern are hash tags without reclassifying an existing placeholder.
- The tier-0 Redis cluster safety exemption list is empty, and F-12.4.28 is closed.
- §12.4, §17.8.2, and §16.5 permit one Redis endpoint for every concern under stated conditions, and every condition is audited against every Redis store.

## Non-goals

- A rule that the first braced segment of a key pattern is a literal hash tag. It would misdescribe `cb:{name}`, `sq:{tenant_id}`, `rl:{key}:{minute_epoch}`, `derive_lock:`, `conn:oauth:state:`, `pg:sess-tenant:`, `lenny:events:`, and `ops:escalations:{id}`.
- Converting every spec key pattern to angle-bracket placeholders, or adopting the §28 `<name>` notation for patterns with a fixed tag. Either change needs a sweep across sections or adds a second notation rule (D9).
- Spelling the tag `{ops:lock}:`, which breaks the §25.4 `ops:` prefix rule (D1).
- Flat keys `ops:{lock}:<scope>` and `ops:{lock}:idx:<id>`, which let a free-form scope collide with the epoch key or an index key (D1).
- A hash tag per scope, or spreading lock keys across slots (D2).
- Deleting the id index by encoding the scope in the lock id or by scanning. Encoding changes the `coordination.NewLockID` format the API returns, and scanning makes every by-id operation an O(n), non-atomic `SCAN`.
- Correcting §25.4 to drop the atomic epoch read instead of moving the read into the script. The validated problem requires the move, and the `cjson` stamp costs a few lines of Lua.
- An epoch compare-and-retry, which adds a reply code and a retry loop and still reads outside the script.
- Storing `epoch`, `acquiredAt`, and `expiresAt` as separate hash fields, or a flat-hash layout with no JSON. Either changes the stored layout and every reader (D3).
- Calling `redis.call('TIME')` inside the scripts, which needs RFC3339 formatting in Lua or a flat-hash layout (D4).
- Absolute `PEXPIREAT`, with or without a keyed script that routes `TIME` to the slot owner (D4).
- Mutating the lock JSON in place in Lua for extend and steal. A raw-blob CAS with the decision in Go is simpler (D5).
- A revision or holder hash field for the CAS. Comparing the whole `data` string covers both (D5).
- An unbounded CAS retry loop, or mapping retry exhaustion to `ErrStoreUnavailable` (D6).
- Filing the Extend, Steal, and Release ownership race as a separate defect (D5).
- Routing `scanAll` to the single slot owner (D10).
- Deleting the `redisSlotGuardExempt` map with its last entry (D11).
- `WAIT` after Tier 2 writes to survive asynchronous replication loss on failover. A Redis failover during a Postgres outage is a second fault outside the validated problem, and `WAIT` adds latency and a timeout path.
- A §25.4 sentence on second-fault lock loss after a Redis failover. No test can force it, and the validated problem does not show it.
- Failing acquire closed when the epoch key is absent. That breaks the Redis-only lock service `cmd/lenny-ops/locks.go` still builds, and epoch loss is outside the problem.
- Fixing the remediation-lock reconciliation handoff defects in `service.go` and `pgstore`. They are independent of the cross-slot defect, and REC-2 verifies and files any that hold.
- Fixing §25.4 reconciliation step 2 and the §25.4 claim about Redis durability on failover. Both are adjacent spec defects outside this problem.
- Removing `capacityPlanning.singleTenantRedisTopology` or the `RedisClusterRecommended` warning. Both have code and remain accurate as a capacity hint.
- Per-concern Sentinel or Cluster wiring for lenny-ops, or implementing `redis.quotaStoreBackend`. The owner's decision removes the need, and the per-concern URLs already exist.
- Refusing a cluster client for the lock store, which contradicts the chart and the owner's decision.
- New Part 2 tests. The tier-0 gates enforce the single-slot and node-routed `SCAN` conditions across every store once the exemption list is empty.
- A migration shim for locks under the old key names. No deployments exist, and Redis lock state is ephemeral.

## Open decisions for human to make

The spec review loop routed the question below without a recommendation. The open-decisions-and-impact-review phase derived the recommendation it now carries.

7. **Pub/sub load on a shared Tier 3 Redis Cluster.** The gateway EventBus publishes events with classic Redis `PUBLISH` (`pkg/gateway/storage/eventbus/eventbus.go:254`), and no sharded `SPUBLISH` call exists in the tree. A Redis Cluster broadcasts a classic `PUBLISH` to every node. Under the owner's 2026-10-21 decision that one Redis Cluster may serve every concern at Tier 3 (SPEC-4), event pub/sub traffic therefore reaches the primaries that hold session coordination leases. The question is whether to file this residual as a separate performance finding, for example to evaluate a switch to sharded `SPUBLISH`, or to accept it with nothing filed.
   - Recommendation: accept the residual and file nothing. The operator splits the cache and pub/sub concern onto its own instance when the §12.4 pub/sub ceiling signal fires.
   - Ground: no spec text requires sharded pub/sub or forbids pub/sub on nodes that hold leases, so classic `PUBLISH` is not a code defect. §12.4 defines the pub/sub ceiling signal in its **Pub/sub fan-out cost** bullet (`spec/12_storage-architecture.md:245`). SPEC-4's last condition keeps separation by concern available when a concern's ceiling signals trigger (spec-changes.md:139), and SPEC-6 Edit 1 names the per-store-role connection string as the means.
   - Alternative: file a separate finding to evaluate `SPUBLISH`. It lost because no spec requirement makes the current behavior a defect, and SPEC-4 already names the mitigation. It remains the better choice if the owner wants the broadcast cost measured before any Tier 3 deployment rather than after the ceiling signal fires.
   - Cost of deciding otherwise: filing opens a follow-up finding outside this proposal and changes nothing 0099 stages. Accepting leaves the broadcast load on lease primaries unmeasured until the ceiling signal fires. No capacity estimate of Tier 3 broadcast load against the §12.4 thresholds exists. The §12.4 separation table's rationale for the cache and pub/sub concern (`spec/12_storage-architecture.md:255`) says pub/sub fan-out "should not compete with lease renewals", which a reviewer can read as favoring a finding.
   - Confidence: low. The tree does not derive either answer.

## Defects in the shipped tree that this proposal does not stage

- `Service.Reconcile` treats a failed Redis `Epoch` or `ActiveLocks` read as an empty set and still clears the Postgres-down state (`pkg/ops/coordination/service.go`). It sits outside the cross-slot defect. REC-2 verifies it as audit lead (ii) and files it if confirmed.
- The at-bind session lease TTL is fixed at 60s while `--coordination-interval` is configurable, so a non-default interval breaks the four-interval condition at bind. It sits outside Part 1. REC-2 verifies it as audit lead (i) and files it if confirmed.
- The split-brain reconciler's `RemoveByID` error is discarded in `service.go`. CODE-1 makes `RemoveByID` succeed on a Cluster, and the discarded error stays outside this fix.
- `redistopology.Build` treats every per-concern Redis URL as a direct address and clears its Sentinel and Cluster addresses (`pkg/gateway/storage/redistopology/redistopology.go`). The single-tenant Tier 3 option of one Sentinel instance per concern, which §17.8.2 and §12.4 already state and SPEC-6 Edit 2 keeps, therefore has no Sentinel failover in code. The code is the defect, and the gap predates this proposal.
- The §25.2 Error Response Envelope example gives `REMEDIATION_LOCK_CONFLICT` the category `TRANSIENT`, while the §25.4 Error Codes row and `pkg/ops/opsserver` give `POLICY`. The example is the defect, and SPEC-1 Edit 3 keeps `POLICY`.
- The §12.4 **Data-at-rest posture** says every Redis-backed role has a durable fallback and that total Redis data loss is recoverable, and §28.5.7 and `docs/operator-guide/disaster-recovery.md` repeat the claim. Circuit breakers, playground revocation markers, the durable inbox and DLQ, and staged billing entries have no durable copy, so the claim overstates recovery after an unplanned Redis loss. The overclaim predates this proposal and does not depend on topology. SPEC-4 cites the posture only for its coordination-durability condition, which REC-2 audits store by store.
- §25.4 reconciliation step 2 ("epoch ≥ the epoch of the last reconciliation") disagrees with the code, which copies every active lock, and §25.4 overstates Redis durability on failover. Both are adjacent spec defects outside this problem.
- The `--redis-cluster-addrs` help text in `cmd/lenny-gateway/flags.go` still calls cluster mode the migration topology for the Quota/Rate Limiting instance, which SPEC-5's whole-deployment cutover replaces. It sits outside the P6 code bound.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0032 | Approved | TEST-3 extends the tier-11 check (e) list that 0032 added with `ops:{lock}:`. SPEC-3 and SPEC-4 add §12.4 paragraphs that name no `ops:` key, so 0032's routing of the lenny-ops keys to §25.4 holds. | Nothing. |
| 0093 | Implemented | REC-1 closes F-12.4.28, the cross-slot finding 0093 filed as its REC-1 without staging a fix. | Nothing. 0093 stays verbatim. |

## Deliverable index

- SPEC-1 — spec/25_agent-operability.md — §25.4 Storage Tiers and Error Codes: tier-table row 2, the Tier 2 paragraph rewritten around the `ops:{lock}:` keys, the in-script epoch, `TIME` and relative expiry, and CAS mutations, and the `REMEDIATION_LOCK_CONFLICT` row.
- SPEC-2 — spec/25_agent-operability.md — §25.4 Outage epochs key rename, Clock Source lead-in and Tier 2 bullet, and Storage Routing remediation-lock row.
- SPEC-3 — spec/12_storage-architecture.md — §12.4 **Braces in key patterns.** paragraph.
- SPEC-4 — spec/12_storage-architecture.md — §12.4 **Shared Redis topology.** paragraph and the lease-renewal throughput row.
- SPEC-5 — spec/12_storage-architecture.md — §12.4 Redis Cluster migration pre-plan intro and steps 3, 4, and 5.
- SPEC-6 — spec/17_deployment-topology.md, spec/16_observability.md — §17.8.2 Tier 3 concern-separation cell and single-tenant note, and the §16.5 `DurableInboxRedisUnavailable` row.
- CODE-1 — pkg/ops/coordination/redisstore/redisstore.go, cmd/lenny-ops/locks.go, cmd/lenny-ops/services_wiring.go — Tagged keys, epoch-stamping acquire script, and one CAS script for Extend, Steal, and Release.
- TEST-1 — pkg/ops/coordination/redisstore/redisstore_test.go, tests/spec-map.json — Tier-1 store tests under the slot guard on miniredis and clustertest, with injected interleavings and a randomized checker.
- TEST-2 — tests/tier4_integration/ops_lock_redis_cluster_test.go (or tests/tier4_integration/redis_cluster_smoke_test.go), tests/tier2_component/coordination/tier_transition_test.go — Real Redis Cluster coverage of the operations and epoch transitions, and the tier-2 slot guard.
- TEST-3 — tests/tier0_static/redis_cluster_safety_test.go, tests/tier11_docs/redis_key_prefix_registry_test.go — Remove the tier-0 exemption and extend tier-11 check (e) to §25.4.
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — Close F-12.4.28 and record Part 1 progress on P6.
- REC-2 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — Part 2 condition audit, findings for unmet conditions, and P6 closure.
