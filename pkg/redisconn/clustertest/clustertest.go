// SPDX-License-Identifier: MIT

// Package clustertest builds an in-process Redis Cluster for tier-1 tests:
// a go-redis ClusterClient over several miniredis servers, each owning an
// equal, contiguous range of the 16384 hash slots. The client routes every
// keyed command to the server that owns the key's slot, so a store test
// observes the same key placement a real cluster produces: a SCAN issued
// through the cluster client covers one server only, a transaction whose
// keys span slots fails with redis.ErrCrossSlot, and a key written through
// the client lands on exactly one server.
//
// miniredis does not itself reject a script or multi-key command whose keys
// span slots, so New also installs slotguard.Hook on the client.
//
// Production code never imports this package.
//
// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3).
package clustertest

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// Cluster is an in-process cluster: the client and the servers behind it.
type Cluster struct {
	// Client is the cluster client with slotguard.Hook installed.
	Client *redis.ClusterClient
	// Servers are the masters, in slot order: Servers[0] owns the lowest
	// slot range.
	Servers []*miniredis.Miniredis
}

// New starts masters miniredis servers (at least two) and returns a cluster
// client over them. The servers and the client close when t ends.
func New(t testing.TB, masters int) *Cluster {
	t.Helper()
	if masters < 2 {
		t.Fatalf("clustertest: a cluster needs at least two masters, got %d", masters)
	}
	servers := make([]*miniredis.Miniredis, masters)
	slots := make([]redis.ClusterSlot, masters)
	per := slotguard.SlotCount / masters
	for i := range servers {
		servers[i] = miniredis.RunT(t)
		end := (i+1)*per - 1
		if i == masters-1 {
			end = slotguard.SlotCount - 1
		}
		slots[i] = redis.ClusterSlot{
			Start: i * per,
			End:   end,
			Nodes: []redis.ClusterNode{{ID: servers[i].Addr(), Addr: servers[i].Addr()}},
		}
	}
	client := redis.NewClusterClient(&redis.ClusterOptions{
		ClusterSlots: func(context.Context) ([]redis.ClusterSlot, error) { return slots, nil },
	})
	slotguard.Install(client)
	t.Cleanup(func() { _ = client.Close() })
	return &Cluster{Client: client, Servers: servers}
}

// ServerFor returns the server that owns key's hash slot.
func (c *Cluster) ServerFor(key string) *miniredis.Miniredis {
	per := slotguard.SlotCount / len(c.Servers)
	i := int(slotguard.Slot(key)) / per
	if i >= len(c.Servers) {
		i = len(c.Servers) - 1
	}
	return c.Servers[i]
}

// Keys returns every key held across all servers.
func (c *Cluster) Keys() []string {
	var out []string
	for _, s := range c.Servers {
		out = append(out, s.Keys()...)
	}
	return out
}
