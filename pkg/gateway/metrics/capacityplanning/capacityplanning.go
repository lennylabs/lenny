// SPDX-License-Identifier: MIT

// Package capacityplanning carries the gateway-side §17.8.2 capacity-tier
// startup heuristics. The gateway reads capacityPlanning.tier and
// capacityPlanning.singleTenantRedisTopology from the chart-rendered
// environment and surfaces the §17.8.2 Redis-topology guidance at startup.
package capacityplanning

// RedisClusterRecommendedWarning is the §17.8.2 line 1164 gateway startup
// log the gateway emits at Tier 3 when the Redis topology appears to be
// single-tenant Sentinel and the operator has not documented that intent
// via capacityPlanning.singleTenantRedisTopology. It is a gateway startup
// log message, not a Prometheus alert — consistent with the
// `[WARN] capacityPlanning` startup-log pattern. The text is transcribed
// so log scrapers can match on the RedisClusterRecommended marker. spec:
// spec/17_deployment-topology.md line 1164.
const RedisClusterRecommendedWarning = "[WARN] RedisClusterRecommended — Tier 3 deployment is running a single-tenant Redis Sentinel topology; evaluate Redis Cluster if a single concern exceeds ~150-200K ops/s, or set capacityPlanning.singleTenantRedisTopology=sentinel to document the intent and silence this warning"

// Redis topology identifiers the gateway resolves from its startup flags.
const (
	// RedisTopologySentinel is the Sentinel master/replica topology the
	// gateway follows when --redis-sentinel-addrs is set.
	RedisTopologySentinel = "sentinel"
	// RedisTopologyCluster is the Redis Cluster topology the gateway
	// follows when --redis-cluster-addrs is set.
	RedisTopologyCluster = "cluster"
	// RedisTopologyStandalone is a single base Redis client (no Sentinel,
	// no Cluster).
	RedisTopologyStandalone = "standalone"
)

// ShouldWarnRedisClusterRecommended reports whether the gateway must log
// RedisClusterRecommendedWarning at startup. The warning fires only when
// all of the following hold:
//
//   - the deployment is Tier 3 (tier == "tier3"); lower tiers run well
//     within the single-threaded-primary throughput ceiling and are not
//     warned;
//   - the resolved Redis topology is Sentinel (the "appears to be
//     single-tenant Sentinel" signal); a Cluster or standalone topology
//     is not warned;
//   - the operator has not set capacityPlanning.singleTenantRedisTopology
//     to "sentinel", which documents the operator's intent and suppresses
//     the warning.
//
// spec: spec/17_deployment-topology.md line 1164.
func ShouldWarnRedisClusterRecommended(tier, redisTopology, singleTenantRedisTopology string) bool {
	if tier != "tier3" {
		return false
	}
	if redisTopology != RedisTopologySentinel {
		return false
	}
	return singleTenantRedisTopology != RedisTopologySentinel
}
