# Deviations — 0099_fix_lenny-ops-remediation-locks-cannot-run-on-a-redis

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: CHART-1 values.yaml Redis comments re-wrapped after the text change

**Status:** proposed

**What the proposal says.** CHART-1 in the non-spec changes replaces a phrase in the `redis.cluster.addrs` comment and deletes a sentence from the `singleTenantRedisTopology` comment in `charts/lenny/values.yaml`. The proposal states that the rest of each comment stays unchanged.

**What landed instead.** Step S14 replaced and deleted the specified text word for word in `charts/lenny/values.yaml` (the `redis.cluster.addrs` comment and the `singleTenantRedisTopology` comment). It then re-wrapped the surrounding comment lines to keep them under 80 columns, and it changed the em-dash before the replaced phrase to a comma. The words outside the changed phrase are unchanged.

**Why.** Step S14 reported that the substitution left one line of the `redis.cluster.addrs` comment much wider than its neighbours, and that the deletion left a short dangling line in the `singleTenantRedisTopology` comment. The re-wrap changes only line breaks and punctuation.

**What a later reader would otherwise get wrong.** A reader who diffs the two comments against CHART-1 expects only the replaced phrase and the deleted sentence to differ. The diff also shows moved line breaks in the neighbouring comment lines and a comma where an em-dash stood. Those differences come from the re-wrap and carry no change in wording.

## Proposed: gateway Redis Cluster seed-node flag help still names Cluster as the Tier 2 to 3 migration topology for the Quota/Rate Limiting instance

**Status:** proposed

**What the proposal says.** The proposal stages no change to `cmd/lenny-gateway/flags.go`.

**What landed instead.** The sweep step left `cmd/lenny-gateway/flags.go` unchanged. The help text of the Redis Cluster seed-node flag (line 512) still states: "§12.4 comma-separated Redis Cluster seed nodes (host:port). When set the base Redis client is a CLUSTER KEYSLOT-aware go-redis ClusterClient — the Tier 2→3 migration topology for the Quota/Rate Limiting instance. ..."

**Why.** The sweep step reported that this text contradicts the landed specification. §12.4 Redis HA and Failure Modes, in the Redis Cluster migration pre-plan, now describes "the migration of the deployment's Redis from Sentinel to Redis Cluster". §17.8.2 Capacity Tier Reference, in the Concern separation row for Tier 3, now states "One Redis Cluster for every concern under §12.4 Shared Redis topology". The sweep step did not change the text because it is flag help rather than a comment, so changing it changes user-visible behaviour the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `cmd/lenny-gateway/flags.go`, or of the gateway's flag help output, takes the pre-0099 contract as current and concludes that Redis Cluster is a migration target for the Quota/Rate Limiting instance alone rather than a topology one Redis Cluster serves for every concern.

## Proposed: RedisClusterRecommended capacity-planning warning still states the retired single-concern ops/s threshold

**Status:** proposed

**What the proposal says.** The proposal stages no change to `pkg/gateway/metrics/capacityplanning/capacityplanning.go`.

**What landed instead.** The sweep step left `pkg/gateway/metrics/capacityplanning/capacityplanning.go` unchanged. The constant `RedisClusterRecommendedWarning` (line 17) still reads: "[WARN] RedisClusterRecommended — Tier 3 deployment is running a single-tenant Redis Sentinel topology; evaluate Redis Cluster if a single concern exceeds ~150-200K ops/s, or set ..."

**Why.** The sweep step reported that this string contradicts the landed specification. The Single-tenant Redis topology note in §17.8.2 Capacity Tier Reference now states "A single-tenant Tier 3 deployment runs either one Redis Cluster for every concern, with its tenant-prefixed keys on one shard, or one Sentinel instance per concern through the per-store-role connection strings". The rule "Redis Cluster is warranted only when a single concern exceeds ~150–200K ops/s" no longer appears in the specification. The sweep step did not change the string because it is an emitted warning rather than a comment, so changing it changes behaviour the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `pkg/gateway/metrics/capacityplanning/capacityplanning.go`, or an operator who sees the warning, takes the retired per-concern ops/s threshold as the current criterion for moving a Tier 3 deployment to Redis Cluster.
