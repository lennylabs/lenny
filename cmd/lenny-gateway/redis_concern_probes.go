// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health/backends"
	"github.com/lennylabs/lenny/pkg/gateway/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// concernProbeTimeout bounds one recovery-probe PING, the same 2s the
// reconcilers used against the base client.
const concernProbeTimeout = 2 * time.Second

// concernRedisProbe returns a recovery probe that PINGs the client serving
// concern. A reconciler or monitor that guards one concern's keys reads its
// outage and recovery edges from that concern's instance: when an operator
// points the concern at its own Redis, the base client's reachability says
// nothing about it. An unsplit concern resolves to the base client, so the
// single-instance topology is unchanged. A nil client (no Redis wired)
// reports down.
//
// spec: §12.4 Redis HA and Failure Modes ("Logical separation of Redis
// concerns"); §12.6 Interface Design (RedisConcern).
func concernRedisProbe(clients *redistopology.Clients, concern storerouter.RedisConcern) func(context.Context) bool {
	return func(context.Context) bool {
		client := clients.For(concern)
		if client == nil {
			return false
		}
		return redisconn.PingWithTimeout(client, concernProbeTimeout) == nil
	}
}

// healthRegistrar is the part of the §25.3 health aggregator the Redis
// registration needs.
type healthRegistrar interface {
	Register(health.Checker)
}

// redisHealthComponent is the base Redis client's §25.3 component name.
const redisHealthComponent = "redis"

// registerRedisHealth registers the base Redis client as the "redis"
// component and each distinct dedicated concern client as its own
// component, so an outage of a split concern's instance shows in the §25.3
// health report. A concern with no dedicated URL resolves to the base
// client and adds no component. Concerns that name the same dedicated URL
// share one client (redistopology.Build) and therefore one component,
// named "redis-" followed by those concerns joined with "-" in
// redistopology.Concerns order (for example "redis-quota" or
// "redis-quota-delegation"). The concern values carry no hyphen, so the
// name is unambiguous.
//
// spec: §12.4 Redis HA and Failure Modes ("Logical separation of Redis
// concerns"); §12.6 Interface Design (RedisConcern); §25.3.
func registerRedisHealth(agg healthRegistrar, base redis.UniversalClient, clients *redistopology.Clients) {
	if base == nil {
		return
	}
	agg.Register(backends.Redis(base, redisHealthComponent))
	split := clients.ByConcern()
	var order []redis.UniversalClient
	names := map[redis.UniversalClient][]string{}
	for _, concern := range redistopology.Concerns {
		client, ok := split[concern]
		if !ok || client == nil || client == base {
			continue
		}
		if _, seen := names[client]; !seen {
			order = append(order, client)
		}
		names[client] = append(names[client], string(concern))
	}
	for _, client := range order {
		agg.Register(backends.Redis(client, redisHealthComponent+"-"+strings.Join(names[client], "-")))
	}
}
