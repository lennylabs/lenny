// SPDX-License-Identifier: MIT

// Package slotguard is a test-only go-redis hook that rejects any command
// Redis Cluster would reject for spanning hash slots. A store's tests
// install it on the single-node client they build (usually over
// miniredis), so a script, a multi-key command, or a MULTI block whose keys
// hash to more than one slot fails in tier 1 with ErrCrossSlot instead of
// passing on one node and failing with CROSSSLOT on a cluster.
//
// The hook reads a command's keys with redisconn.CommandKeys, the same
// reading the tenant-isolation guard uses, and hashes each key with Slot,
// which applies the Redis Cluster rule (CRC16 of the first non-empty `{...}`
// hash tag, or of the whole key, modulo 16384). A plain pipeline is not a
// unit on a cluster (the cluster client splits it per node), so each of its
// commands is checked on its own; a MULTI block is one unit, so every key in
// it must share one slot.
//
// Production code never imports this package; the tier-0 check
// `TestRedisStoresInstallSlotGuard_spec_12_4` confirms that every Redis
// store package's tests install it.
//
// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3; the
// `t:{tenant_id}:` prefix hashes a tenant's keys to one slot).
package slotguard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn"
)

// ErrCrossSlot reports a command or MULTI block whose keys span hash slots.
var ErrCrossSlot = errors.New("slotguard: keys span more than one Redis Cluster hash slot")

// SlotCount is the number of Redis Cluster hash slots.
const SlotCount = 16384

// Slot returns the Redis Cluster hash slot of key: the CRC16 (XMODEM) of
// the first non-empty `{...}` section, or of the whole key when it has
// none, modulo 16384.
func Slot(key string) uint16 {
	if open := strings.IndexByte(key, '{'); open >= 0 {
		if end := strings.IndexByte(key[open+1:], '}'); end > 0 {
			key = key[open+1 : open+1+end]
		}
	}
	return crc16(key) % SlotCount
}

// crc16 is the CRC16-CCITT (XMODEM) variant Redis Cluster hashes keys with.
func crc16(data string) uint16 {
	var crc uint16
	for i := 0; i < len(data); i++ {
		crc ^= uint16(data[i]) << 8
		for b := 0; b < 8; b++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// Hook is the go-redis hook that rejects cross-slot commands and MULTI
// blocks with ErrCrossSlot before they reach the server.
type Hook struct{}

var _ redis.Hook = Hook{}

// Install adds a Hook to c and returns c, so a test constructor can wrap
// the client it builds in one expression.
func Install[C interface{ AddHook(redis.Hook) }](c C) C {
	c.AddHook(Hook{})
	return c
}

// DialHook passes through; the hook inspects commands only.
func (Hook) DialHook(next redis.DialHook) redis.DialHook { return next }

// ProcessHook rejects a single command whose keys span slots.
func (Hook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := checkKeys(cmd.Name(), redisconn.CommandKeys(cmd)); err != nil {
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}

// ProcessPipelineHook checks a MULTI block as one unit and every other
// pipeline command on its own. go-redis hands a transaction to the hook
// wrapped in MULTI ... EXEC, which is how a MULTI block is recognized.
func (Hook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if err := checkPipeline(cmds); err != nil {
			for _, cmd := range cmds {
				cmd.SetErr(err)
			}
			return err
		}
		return next(ctx, cmds)
	}
}

func checkPipeline(cmds []redis.Cmder) error {
	if len(cmds) > 0 && cmds[0].Name() == "multi" {
		var keys []string
		for _, cmd := range cmds {
			keys = append(keys, redisconn.CommandKeys(cmd)...)
		}
		return checkKeys("multi", keys)
	}
	for _, cmd := range cmds {
		if err := checkKeys(cmd.Name(), redisconn.CommandKeys(cmd)); err != nil {
			return err
		}
	}
	return nil
}

// checkKeys returns ErrCrossSlot when keys hash to more than one slot.
func checkKeys(name string, keys []string) error {
	if len(keys) < 2 {
		return nil
	}
	first := Slot(keys[0])
	for _, k := range keys[1:] {
		if s := Slot(k); s != first {
			return fmt.Errorf("%w: %s over %q (slot %d) and %q (slot %d)", ErrCrossSlot, name, keys[0], first, k, s)
		}
	}
	return nil
}
