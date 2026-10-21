// SPDX-License-Identifier: MIT

package redisconn_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn"
)

// spec: §12.4 Redis HA and Failure Modes (tenant key isolation enforcement
// clause; Redis Cluster at Tier 3). CommandKeys reads a command's keys the
// way Redis Cluster hashes them: the declared KEYS of a script, every key of
// a multi-key command, the key positions of MSET, the source and destination
// of a two-key command, and argument 1 otherwise; keyless commands carry no
// key.
func TestCommandKeys_spec_12_4(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		cmd  redis.Cmder
		want []string
	}{
		{redis.NewCmd(ctx, "evalsha", "sha", 2, "k1", "k2", "arg"), []string{"k1", "k2"}},
		{redis.NewCmd(ctx, "eval", "return 1", "1", "k1", "arg"), []string{"k1"}},
		{redis.NewCmd(ctx, "eval", "return 1", int64(0)), []string{}},
		{redis.NewCmd(ctx, "fcall", "fn"), nil},
		{redis.NewCmd(ctx, "mget", "a", "b", "c"), []string{"a", "b", "c"}},
		{redis.NewCmd(ctx, "del", []byte("a"), "b"), []string{"a", "b"}},
		{redis.NewCmd(ctx, "mset", "a", 1, "b", 2), []string{"a", "b"}},
		{redis.NewCmd(ctx, "rename", "a", "b"), []string{"a", "b"}},
		{redis.NewCmd(ctx, "lmove", "a", "b", "LEFT", "RIGHT"), []string{"a", "b"}},
		{redis.NewCmd(ctx, "set", "a", "v"), []string{"a"}},
		{redis.NewCmd(ctx, "publish", "chan", "msg"), []string{"chan"}},
		{redis.NewCmd(ctx, "scan", 0, "match", "cb:*"), nil},
		{redis.NewCmd(ctx, "multi"), nil},
		{redis.NewCmd(ctx, "ping", "x"), nil},
	}
	for _, tc := range cases {
		got := redisconn.CommandKeys(tc.cmd)
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("CommandKeys(%v) = %q, want %q", tc.cmd.Args(), got, tc.want)
		}
	}
}
