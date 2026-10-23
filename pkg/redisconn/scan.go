// SPDX-License-Identifier: MIT

package redisconn

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/redis/go-redis/v9"
)

// DefaultScanCount is the SCAN COUNT hint ScanKeys and ScanDelete pass when
// the caller passes zero. It bounds the work one SCAN step does on the
// server; it is a hint, so a step may return more or fewer keys.
const DefaultScanCount = 256

// ScanNodes returns the clients a SCAN must run on to cover every key that
// matches a pattern.
//
// A cluster client routes a keyless command such as SCAN to one arbitrary
// node, so a SCAN issued on it covers one master's keys and silently misses
// the rest. On a *redis.ClusterClient, ScanNodes therefore returns the master
// that owns routingKey's hash slot when routingKey is non-empty (a
// tenant-prefixed pattern such as `t:{acme}:quota:*`, whose keys all carry
// the tenant's hash tag and so share one slot), and every master when
// routingKey is empty (a cross-tenant or platform pattern such as
// `cb:*`). Any other client holds the whole keyspace and is returned as is.
//
// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3; the
// `t:{tenant_id}:` prefix hashes a tenant's keys to one slot).
func ScanNodes(ctx context.Context, c redis.Cmdable, routingKey string) ([]redis.Cmdable, error) {
	cc, ok := c.(*redis.ClusterClient)
	if !ok {
		return []redis.Cmdable{c}, nil
	}
	if routingKey != "" {
		node, err := cc.MasterForKey(ctx, routingKey)
		if err != nil {
			return nil, fmt.Errorf("redisconn: resolve the master for %q: %w", routingKey, err)
		}
		return []redis.Cmdable{node}, nil
	}
	return clusterMasters(ctx, cc)
}

// clusterMasters lists the cluster's masters in address order. ForEachMaster
// calls its callback concurrently, so the callback only collects the node
// clients, and the scan itself then runs on them one at a time.
func clusterMasters(ctx context.Context, cc *redis.ClusterClient) ([]redis.Cmdable, error) {
	var (
		mu    sync.Mutex
		nodes []*redis.Client
	)
	err := cc.ForEachMaster(ctx, func(_ context.Context, node *redis.Client) error {
		mu.Lock()
		nodes = append(nodes, node)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("redisconn: list cluster masters: %w", err)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Options().Addr < nodes[j].Options().Addr })
	out := make([]redis.Cmdable, len(nodes))
	for i, n := range nodes {
		out[i] = n
	}
	return out, nil
}

// ScanKeys runs a cursor-based SCAN for pattern on every node ScanNodes
// returns for routingKey and calls fn with each non-empty batch of keys.
// count is the SCAN COUNT hint; zero selects DefaultScanCount. An error
// from SCAN or from fn stops the scan and is returned.
//
// fn issues its follow-up commands on c rather than on the node that
// returned the batch: a cluster client routes each keyed command to the
// key's owner, and the hooks installed on c (the tenant guard among them)
// run on c's commands only. The keys of one batch can span hash slots when
// routingKey is empty, so fn must not issue one multi-key command over a
// batch in that case; it reads or deletes each key with its own command, or
// pipelines single-key commands.
func ScanKeys(ctx context.Context, c redis.Cmdable, routingKey, pattern string, count int64, fn func(keys []string) error) error {
	if count <= 0 {
		count = DefaultScanCount
	}
	nodes, err := ScanNodes(ctx, c, routingKey)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if err := scanNode(ctx, node, pattern, count, fn); err != nil {
			return err
		}
	}
	return nil
}

// scanNode walks one node's SCAN cursor from 0 back to 0.
func scanNode(ctx context.Context, node redis.Cmdable, pattern string, count int64, fn func(keys []string) error) error {
	var cursor uint64
	for {
		keys, next, err := node.Scan(ctx, cursor, pattern, count).Result()
		if err != nil {
			return fmt.Errorf("redisconn: scan %q: %w", pattern, err)
		}
		if len(keys) > 0 {
			if err := fn(keys); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

// ScanDelete deletes every key matching pattern on the nodes ScanNodes
// returns for routingKey and returns the number of keys removed. Each batch
// is deleted with a pipeline of single-key DEL commands, so no command spans
// two hash slots whatever the keys' tags. A key that expires between the
// SCAN and the DEL does not count. The erasure paths (§12.8) call it with a
// tenant prefix as routingKey.
//
// spec: §12.4 Redis HA and Failure Modes; §12.8 (erasure scope).
func ScanDelete(ctx context.Context, c redis.Cmdable, routingKey, pattern string) (int, error) {
	deleted := 0
	err := ScanKeys(ctx, c, routingKey, pattern, 0, func(keys []string) error {
		n, err := deleteEach(ctx, c, keys)
		deleted += n
		return err
	})
	return deleted, err
}

// deleteEach pipelines one DEL per key on c and sums the removals.
func deleteEach(ctx context.Context, c redis.Cmdable, keys []string) (int, error) {
	cmds := make([]*redis.IntCmd, len(keys))
	_, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, k := range keys {
			cmds[i] = p.Del(ctx, k)
		}
		return nil
	})
	n := 0
	for _, cmd := range cmds {
		if cmd != nil {
			n += int(cmd.Val())
		}
	}
	if err != nil {
		return n, fmt.Errorf("redisconn: delete scanned keys: %w", err)
	}
	return n, nil
}
