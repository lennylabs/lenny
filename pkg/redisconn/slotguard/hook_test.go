// SPDX-License-Identifier: MIT

package slotguard_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

func guarded(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3). Slot
// matches the reference value in the Redis Cluster specification and honors
// the first non-empty hash tag.
func TestSlotMatchesRedisCluster_spec_12_4(t *testing.T) {
	cases := map[string]uint16{
		"123456789": 0x31C3 % slotguard.SlotCount,
		"foo":       12182,
		"{foo}bar":  12182,
		"a{}{foo}":  slotguard.Slot("a{}{foo}"),
	}
	for k, want := range cases {
		if got := slotguard.Slot(k); got != want {
			t.Errorf("Slot(%q) = %d, want %d", k, got, want)
		}
	}
	if slotguard.Slot("{user1000}.following") != slotguard.Slot("{user1000}.followers") {
		t.Fatal("keys sharing a hash tag must map to one slot")
	}
	if slotguard.Slot("t:{acme}:quota:x") != slotguard.Slot("t:{acme}:lease:session:y") {
		t.Fatal("two keys under one tenant prefix must map to one slot")
	}
	// An empty tag `{}` is not a tag: the whole key is hashed.
	if slotguard.Slot("a{}{foo}") == slotguard.Slot("foo") {
		t.Fatal("an empty {} must not be read as a hash tag")
	}
}

// spec: §12.4 Redis HA and Failure Modes (the `t:{tenant_id}:` prefix
// hashes a tenant's keys to one slot). This is the hook's negative control:
// a script, a multi-key command, and a MULTI block over two unbraced tenant
// keys span slots and are rejected with ErrCrossSlot, while the same
// operations over one tenant's braced keys pass.
func TestHookRejectsCrossSlotOperations_spec_12_4(t *testing.T) {
	cl := guarded(t)
	ctx := context.Background()
	script := redis.NewScript(`return #KEYS`)
	spread := []string{"t:acme:a", "t:acme:b"}
	tagged := []string{"t:{acme}:a", "t:{acme}:b"}
	if slotguard.Slot(spread[0]) == slotguard.Slot(spread[1]) {
		t.Fatal("fixture: the unbraced keys must hash to different slots")
	}

	if err := script.Run(ctx, cl, spread).Err(); !errors.Is(err, slotguard.ErrCrossSlot) {
		t.Errorf("cross-slot script err = %v, want ErrCrossSlot", err)
	}
	if err := cl.MGet(ctx, spread...).Err(); !errors.Is(err, slotguard.ErrCrossSlot) {
		t.Errorf("cross-slot MGET err = %v, want ErrCrossSlot", err)
	}
	if err := cl.Del(ctx, spread...).Err(); !errors.Is(err, slotguard.ErrCrossSlot) {
		t.Errorf("cross-slot DEL err = %v, want ErrCrossSlot", err)
	}
	_, err := cl.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, spread[0], "1", 0)
		p.Set(ctx, spread[1], "1", 0)
		return nil
	})
	if !errors.Is(err, slotguard.ErrCrossSlot) {
		t.Errorf("cross-slot MULTI err = %v, want ErrCrossSlot", err)
	}

	if err := script.Run(ctx, cl, tagged).Err(); err != nil {
		t.Errorf("same-slot script err = %v", err)
	}
	if err := cl.MGet(ctx, tagged...).Err(); err != nil {
		t.Errorf("same-slot MGET err = %v", err)
	}
	_, err = cl.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, tagged[0], "1", 0)
		p.Set(ctx, tagged[1], "1", 0)
		return nil
	})
	if err != nil {
		t.Errorf("same-slot MULTI err = %v", err)
	}
}

// spec: §12.4 Redis HA and Failure Modes. A plain pipeline is not a unit on
// a cluster (the cluster client splits it per node), so single-key commands
// on different slots pass; a WATCH over one key passes, and a cross-slot
// command inside a plain pipeline is still rejected.
func TestHookChecksPlainPipelinePerCommand_spec_12_4(t *testing.T) {
	cl := guarded(t)
	ctx := context.Background()
	_, err := cl.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Get(ctx, "cb:a")
		p.Get(ctx, "cb:b")
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		t.Fatalf("single-key pipeline err = %v", err)
	}
	_, err = cl.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.MGet(ctx, "cb:a", "cb:b")
		return nil
	})
	if !errors.Is(err, slotguard.ErrCrossSlot) {
		t.Fatalf("cross-slot command in pipeline err = %v, want ErrCrossSlot", err)
	}
	err = cl.Watch(ctx, func(tx *redis.Tx) error {
		_, err := tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, "cb:a", "1", 0)
			return nil
		})
		return err
	}, "cb:a")
	if err != nil {
		t.Fatalf("single-key WATCH transaction err = %v", err)
	}
	err = cl.Watch(ctx, func(tx *redis.Tx) error { return nil }, "cb:a", "cb:b")
	if !errors.Is(err, slotguard.ErrCrossSlot) {
		t.Fatalf("cross-slot WATCH err = %v, want ErrCrossSlot", err)
	}
}
