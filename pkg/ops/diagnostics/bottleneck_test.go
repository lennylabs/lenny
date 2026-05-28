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
		{
			// spec: §25.6 line 2865 — SETUP_FAILURE bottleneck category.
			name:    "setup failure_spec_25_6_2865",
			signals: diagnostics.PoolSignals{SetupFailures: 2},
			want:    diagnostics.BottleneckSetupFailure,
			ok:      true,
		},
		{
			// spec: §25.6 line 2865 — CRD_SYNC_LAG bottleneck category.
			name:    "crd sync lag_spec_25_6_2865",
			signals: diagnostics.PoolSignals{CRDSyncLag: true},
			want:    diagnostics.BottleneckCRDSyncLag,
			ok:      true,
		},
		{
			// Image-pull failures still out-rank CRD sync lag because a
			// failing warm-up is more actionable than the resulting CRD
			// drift.
			name: "image pull beats crd sync lag",
			signals: diagnostics.PoolSignals{
				ImagePullFailures: 1,
				CRDSyncLag:        true,
			},
			want: diagnostics.BottleneckImagePull,
			ok:   true,
		},
		{
			// CRD sync lag out-ranks the rate comparison because a
			// non-converged CRD cannot serve the desired replenishment
			// rate regardless of headroom.
			name: "crd sync lag beats rate shortfall",
			signals: diagnostics.PoolSignals{
				CRDSyncLag:        true,
				ReplenishmentRate: 1, ClaimRate: 5,
			},
			want: diagnostics.BottleneckCRDSyncLag,
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
