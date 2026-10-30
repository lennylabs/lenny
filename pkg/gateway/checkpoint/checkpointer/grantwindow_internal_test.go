// SPDX-License-Identifier: MIT

package checkpointer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// stubPoolPolicy is a podsession.PoolPolicyReader test double: it returns
// the configured mirror for any pool name, or an error when err is set.
type stubPoolPolicy struct {
	mirror podsession.PoolPolicyMirror
	found  bool
	err    error
}

func (s stubPoolPolicy) PoolPolicy(context.Context, string) (podsession.PoolPolicyMirror, bool, error) {
	return s.mirror, s.found, s.err
}

func int32p(v int32) *int32 { return &v }

// spec: §17.8.1 — checkpointGrantWindow default 4 / checkpointCapabilityTTL
// default 30 s. grantWindow and capabilityTTL apply the mandated defaults
// when the deployment-wide fields are unset.
func TestCheckpointerAppliesConfigDefaults_spec_17_8_1(t *testing.T) {
	c := &Checkpointer{}
	if got := c.grantWindow(); got != DefaultGrantWindow {
		t.Errorf("grantWindow() with no config = %d, want %d", got, DefaultGrantWindow)
	}
	if got := c.capabilityTTL(); got != DefaultCapabilityTTLSeconds*time.Second {
		t.Errorf("capabilityTTL() with no config = %s, want %s", got, DefaultCapabilityTTLSeconds*time.Second)
	}
	c = &Checkpointer{GrantWindow: 8, CapabilityTTL: 45 * time.Second}
	if got := c.grantWindow(); got != 8 {
		t.Errorf("grantWindow() = %d, want 8", got)
	}
	if got := c.capabilityTTL(); got != 45*time.Second {
		t.Errorf("capabilityTTL() = %s, want 45s", got)
	}
}

// spec: §5.2 — the checkpoint driver resolves a pool's per-pool
// checkpointGrantWindow override at attempt time and falls back to the
// deployment-wide default when the pool sets none.
func TestGrantWindowForPoolResolvesOverride_spec_5_2(t *testing.T) {
	cases := []struct {
		name string
		c    *Checkpointer
		pool string
		want int
	}{
		{
			name: "no reader falls back to deployment-wide default",
			c:    &Checkpointer{GrantWindow: 4},
			pool: "cp-pool",
			want: 4,
		},
		{
			name: "empty pool name falls back to deployment-wide default",
			c:    &Checkpointer{GrantWindow: 4, PoolGrantWindow: stubPoolPolicy{mirror: podsession.PoolPolicyMirror{CheckpointGrantWindow: int32p(9)}, found: true}},
			pool: "",
			want: 4,
		},
		{
			name: "pool override wins over the deployment-wide default",
			c:    &Checkpointer{GrantWindow: 4, PoolGrantWindow: stubPoolPolicy{mirror: podsession.PoolPolicyMirror{CheckpointGrantWindow: int32p(9)}, found: true}},
			pool: "cp-pool",
			want: 9,
		},
		{
			name: "pool with no override row falls back to the default",
			c:    &Checkpointer{GrantWindow: 4, PoolGrantWindow: stubPoolPolicy{found: false}},
			pool: "cp-pool",
			want: 4,
		},
		{
			name: "pool present but no override field falls back to the default",
			c:    &Checkpointer{GrantWindow: 4, PoolGrantWindow: stubPoolPolicy{mirror: podsession.PoolPolicyMirror{}, found: true}},
			pool: "cp-pool",
			want: 4,
		},
		{
			name: "reader error degrades to the deployment-wide default",
			c:    &Checkpointer{GrantWindow: 4, PoolGrantWindow: stubPoolPolicy{err: errors.New("mirror read failed")}},
			pool: "cp-pool",
			want: 4,
		},
		{
			name: "unset deployment default falls back to the mandated constant",
			c:    &Checkpointer{PoolGrantWindow: stubPoolPolicy{found: false}},
			pool: "cp-pool",
			want: DefaultGrantWindow,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.grantWindowForPool(context.Background(), tc.pool); got != tc.want {
				t.Errorf("grantWindowForPool(%q) = %d, want %d", tc.pool, got, tc.want)
			}
		})
	}
}
