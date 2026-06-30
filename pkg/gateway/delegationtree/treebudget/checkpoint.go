// SPDX-License-Identifier: MIT

package treebudget

import (
	"context"
	"fmt"
	"strconv"
)

// TreeCounters is the tree-wide subset of the delegation budget counters
// that the §11.2 line 29 Postgres checkpoint persists: the active node
// count (maxTreeSize), the consumed token pool (maxTokenBudget), and the
// aggregate in-memory footprint (maxTreeMemoryBytes). The per-parent
// counters (parallel_children, children_total) are scoped to a single
// delegating parent and are not checkpointed — they are reconstructed
// implicitly when each live parent re-enters the tree on resume.
//
// spec: §11.2 line 29 (counters included in the checkpoint).
type TreeCounters struct {
	TreeSize   int64
	Tokens     int64
	TreeMemory int64
}

// treeWideKeys returns the three tree-wide §12.4 counter keys for
// rootSessionID. They share the `{root_session_id}` hash tag so they
// co-locate on one Redis Cluster slot, and they carry no parent suffix
// because they aggregate the whole tree.
func treeWideKeys(rootSessionID string) []string {
	root := "{" + rootSessionID + "}:dlg:"
	return []string{root + "tree_size", root + "tree_memory", root + "tokens"}
}

// Snapshot reads the current tree-wide counter values for rootSessionID
// so the §11.2 periodic checkpoint can persist them to Postgres. An
// absent key reads as zero (the tree admitted no delegation on that
// axis). A Redis error is returned to the caller; the checkpoint loop
// skips a tree it cannot read rather than persisting a wrong value.
//
// spec: §11.2 line 29, line 44 (durable checkpoint).
func (s *Reserver) Snapshot(ctx context.Context, rootSessionID string) (TreeCounters, error) {
	if rootSessionID == "" {
		return TreeCounters{}, fmt.Errorf("treebudget: empty root session id")
	}
	vals, err := s.client.MGet(ctx, treeWideKeys(rootSessionID)...).Result()
	if err != nil {
		return TreeCounters{}, fmt.Errorf("treebudget: snapshot: %w", err)
	}
	if len(vals) != 3 {
		return TreeCounters{}, fmt.Errorf("treebudget: snapshot: expected 3 values, got %d", len(vals))
	}
	return TreeCounters{
		TreeSize:   parseRedisInt(vals[0]),
		TreeMemory: parseRedisInt(vals[1]),
		Tokens:     parseRedisInt(vals[2]),
	}, nil
}

// Restore writes the §11.2 line 48 reconstructed counter values back to
// Redis on a recovery edge, so the fast-path reserve script resumes
// enforcement against the MAX-reconstructed value rather than a
// stale-zero counter left by a Redis restart. It refreshes the GC TTL on
// each key so a long-lived tree's restored counters do not immediately
// lapse. The three axes are set in one round trip; the per-parent
// counters are deliberately untouched.
//
// spec: §11.2 line 48; §12.4 line 218 (counters reconstructed before new
// delegations are accepted).
func (s *Reserver) Restore(ctx context.Context, rootSessionID string, c TreeCounters) error {
	if rootSessionID == "" {
		return fmt.Errorf("treebudget: empty root session id")
	}
	keys := treeWideKeys(rootSessionID)
	ttl := s.ttl
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, keys[0], c.TreeSize, ttl)
	pipe.Set(ctx, keys[1], c.TreeMemory, ttl)
	pipe.Set(ctx, keys[2], c.Tokens, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("treebudget: restore: %w", err)
	}
	return nil
}

// parseRedisInt coerces an MGet result element to int64. A nil (absent
// key, returned by go-redis as a non-string element) or an unparseable
// value reads as zero — the counter never existed, so the tree consumed
// none of that axis.
func parseRedisInt(v any) int64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
