// SPDX-License-Identifier: MIT

// Package capacityplanning carries the gateway-side §17.8.2 capacity-tier
// startup heuristics. The gateway reads capacityPlanning.tier and
// capacityPlanning.singleTenantRedisTopology from the chart-rendered
// environment and surfaces the §17.8.2 Redis-topology guidance at startup.
//
// spec: GWSCALE
package capacityplanning
