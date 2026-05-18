// SPDX-License-Identifier: MIT

package diagnostics_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

func TestClassifyPoolBottleneck(t *testing.T) {
	cases := []struct {
		name    string
		signals diagnostics.PoolSignals
		want    diagnostics.BottleneckCategory
		ok      bool
	}{
		{
			name:    "healthy pool",
			signals: diagnostics.PoolSignals{ReplenishmentRate: 10, ClaimRate: 4},
			ok:      false,
		},
		{
			name:    "demand exceeds supply",
			signals: diagnostics.PoolSignals{ReplenishmentRate: 2, ClaimRate: 9},
			want:    diagnostics.BottleneckDemandExceedsSupply,
			ok:      true,
		},
		{
			name:    "image pull failure",
			signals: diagnostics.PoolSignals{ImagePullFailures: 3, ReplenishmentRate: 1, ClaimRate: 9},
			want:    diagnostics.BottleneckImagePull,
			ok:      true,
		},
		{
			name:    "node pressure",
			signals: diagnostics.PoolSignals{NodePressureFailures: 1},
			want:    diagnostics.BottleneckNodePressure,
			ok:      true,
		},
		{
			name:    "quota exhausted",
			signals: diagnostics.PoolSignals{QuotaExceededFailures: 2},
			want:    diagnostics.BottleneckQuotaExhausted,
			ok:      true,
		},
		{
			name: "image pull takes precedence over the rate shortfall",
			signals: diagnostics.PoolSignals{
				ImagePullFailures: 1,
				ReplenishmentRate: 0, ClaimRate: 5,
			},
			want: diagnostics.BottleneckImagePull,
			ok:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := diagnostics.ClassifyPoolBottleneck(c.signals)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if got != c.want {
				t.Errorf("category = %q, want %q", got, c.want)
			}
		})
	}
}
