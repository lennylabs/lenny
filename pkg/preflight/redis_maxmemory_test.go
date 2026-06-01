// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §12.4 — billing stream and per-tenant counters must not evict.
func TestCheckRedisMaxmemoryPolicy_spec_12_4(t *testing.T) {
	cases := []struct {
		name       string
		policy     string
		probeErr   error
		wantPass   bool
		wantReason string // substring expected in the failure reason
	}{
		{name: "noeviction passes", policy: "noeviction", wantPass: true},
		{name: "uppercase noeviction passes", policy: "NOEVICTION", wantPass: true},
		{name: "padded noeviction passes", policy: "  noeviction\n", wantPass: true},
		{name: "allkeys-lru fails closed", policy: "allkeys-lru", wantPass: false, wantReason: "REDIS_EVICTION_POLICY_DRIFT"},
		{name: "volatile-ttl fails closed", policy: "volatile-ttl", wantPass: false, wantReason: "REDIS_EVICTION_POLICY_DRIFT"},
		{name: "empty value fails closed", policy: "", wantPass: false, wantReason: "REDIS_MAXMEMORY_POLICY_UNKNOWN"},
		{name: "unreachable fails closed", probeErr: errors.New("dial tcp: connection refused"), wantPass: false, wantReason: "REDIS_UNREACHABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotParam string
			prober := RedisConfigProbeFunc(func(_ context.Context, param string) (string, error) {
				gotParam = param
				return tc.policy, tc.probeErr
			})
			got := CheckRedisMaxmemoryPolicy(context.Background(), prober)
			if got.Passed != tc.wantPass {
				t.Fatalf("Passed = %v, want %v (reason: %q)", got.Passed, tc.wantPass, got.Reason)
			}
			if tc.probeErr == nil && gotParam != "maxmemory-policy" {
				t.Errorf("probed param = %q, want maxmemory-policy", gotParam)
			}
			if !tc.wantPass && !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want substring %q", got.Reason, tc.wantReason)
			}
			if tc.wantPass && got.Reason != "" {
				t.Errorf("passing check carried a reason: %q", got.Reason)
			}
		})
	}
}
