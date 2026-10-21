// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// keyRecorder is a go-redis hook that records the keys of every command the
// Counter sends: the KEYS of each EVALSHA and EVAL, and the arguments of
// every other keyed command.
type keyRecorder struct {
	mu   sync.Mutex
	cmds [][]string
}

func (r *keyRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *keyRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (r *keyRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if keys := commandKeys(cmd); len(keys) > 0 {
			r.mu.Lock()
			r.cmds = append(r.cmds, keys)
			r.mu.Unlock()
		}
		return next(ctx, cmd)
	}
}

// commandKeys extracts the keys of cmd: KEYS for a script call, every
// argument of DEL and EXISTS, and the first argument of other keyed calls.
func commandKeys(cmd redis.Cmder) []string {
	args := cmd.Args()
	strs := make([]string, 0, len(args))
	for _, a := range args {
		strs = append(strs, fmt.Sprint(a))
	}
	switch strings.ToLower(cmd.Name()) {
	case "evalsha", "eval":
		n, err := strconv.Atoi(strs[2])
		if err != nil || len(strs) < 3+n {
			return nil
		}
		return strs[3 : 3+n]
	case "del", "exists":
		return strs[1:]
	case "set", "get", "smembers", "scard", "sadd":
		return strs[1:2]
	default:
		return nil
	}
}

// crc16XModem is the CRC16 variant Redis Cluster hashes keys with.
func crc16XModem(data string) uint16 {
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

// clusterSlot applies the Redis Cluster rule: the CRC16 of the hash tag (the
// first non-empty {...} section), or of the whole key, modulo 16384.
func clusterSlot(key string) uint16 {
	if open := strings.IndexByte(key, '{'); open >= 0 {
		if end := strings.IndexByte(key[open+1:], '}'); end > 0 {
			key = key[open+1 : open+1+end]
		}
	}
	return crc16XModem(key) % 16384
}

// TestCRC16MatchesRedisCluster pins the test's slot function against the
// reference value from the Redis Cluster specification.
//
// spec: §12.4 (lenny:pod:{pod_id}:* keys)
func TestCRC16MatchesRedisCluster(t *testing.T) {
	if got := crc16XModem("123456789"); got != 0x31C3 {
		t.Fatalf("crc16(123456789) = %#x, want 0x31c3", got)
	}
	if clusterSlot("{user1000}.following") != clusterSlot("{user1000}.followers") {
		t.Fatal("keys sharing a hash tag must map to one slot")
	}
}

// TestEveryCounterCommandStaysInOneHashSlot_spec_12_4 drives Reserve, the
// clean, leaked, and flag-absent releases, the recovery rebuild, Members,
// Reap, and Reset,
// and asserts that every key of every command carries the pod's hash tag and
// that all keys of one command map to one Redis Cluster slot.
//
// spec: §12.4 (lenny:pod:{pod_id}:* keys)
func TestEveryCounterCommandStaysInOneHashSlot_spec_12_4(t *testing.T) {
	const pod = "sbx-cluster-7"
	steps := []struct {
		name string
		run  func(t *testing.T, c *slotcounter.Counter, mr *miniredis.Miniredis)
	}{
		{"reserve with rehydration", func(t *testing.T, c *slotcounter.Counter, _ *miniredis.Miniredis) {
			mustReserve(t, c, pod, "s1", 4)
		}},
		{"clean release", func(t *testing.T, c *slotcounter.Counter, _ *miniredis.Miniredis) {
			_, _, _, _ = c.Release(context.Background(), pod, "s1", false)
		}},
		{"leaked release", func(t *testing.T, c *slotcounter.Counter, _ *miniredis.Miniredis) {
			mustReserve(t, c, pod, "s2", 4)
			_, _, _, _ = c.Release(context.Background(), pod, "s2", true)
		}},
		{"release with the flag absent", func(t *testing.T, c *slotcounter.Counter, mr *miniredis.Miniredis) {
			mr.Del("lenny:pod:{" + pod + "}:rehydrated")
			_, _, _, _ = c.Release(context.Background(), pod, "s3", false)
		}},
		{"recovery rebuild", func(t *testing.T, c *slotcounter.Counter, mr *miniredis.Miniredis) {
			c.RecordAdmissionForTest(pod, "s4")
			c.ProbeTickForTest(context.Background())
		}},
		{"members", func(t *testing.T, c *slotcounter.Counter, _ *miniredis.Miniredis) {
			if _, err := c.Members(context.Background(), pod); err != nil {
				t.Fatal(err)
			}
		}},
		{"reap", func(t *testing.T, c *slotcounter.Counter, _ *miniredis.Miniredis) {
			mustReserve(t, c, pod, "s5", 4)
			if _, err := c.Reap(context.Background(), pod, "s5"); err != nil {
				t.Fatal(err)
			}
		}},
		{"reset", func(t *testing.T, c *slotcounter.Counter, _ *miniredis.Miniredis) {
			if err := c.Reset(context.Background(), pod); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			cl := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
			t.Cleanup(func() { _ = cl.Close() })
			rec := &keyRecorder{}
			cl.AddHook(rec)
			src := newFakeSource()
			src.set("live", pod, false)
			c := slotcounter.New(cl, slotcounter.WithSlotSource(src))
			mustReserve(t, c, pod, "s0", 4)
			rec.mu.Lock()
			rec.cmds = nil
			rec.mu.Unlock()
			step.run(t, c, mr)
			rec.mu.Lock()
			defer rec.mu.Unlock()
			if len(rec.cmds) == 0 {
				t.Fatal("no keyed command recorded")
			}
			want := clusterSlot("{" + pod + "}")
			for _, keys := range rec.cmds {
				for _, k := range keys {
					if !strings.Contains(k, "{"+pod+"}") {
						t.Errorf("key %q lacks the hash tag {%s}", k, pod)
					}
					if got := clusterSlot(k); got != want {
						t.Errorf("key %q maps to slot %d, want %d", k, got, want)
					}
				}
			}
		})
	}
}
