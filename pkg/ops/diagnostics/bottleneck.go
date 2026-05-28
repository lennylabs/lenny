// SPDX-License-Identifier: MIT

package diagnostics

// BottleneckCategory is a §25.6 PoolBottleneck category.
type BottleneckCategory string

const (
	// BottleneckDemandExceedsSupply: claims outpace replenishment.
	BottleneckDemandExceedsSupply BottleneckCategory = "DEMAND_EXCEEDS_SUPPLY"
	// BottleneckImagePull: pods fail to warm because the image will not pull.
	BottleneckImagePull BottleneckCategory = "IMAGE_PULL"
	// BottleneckNodePressure: pods fail to schedule under node pressure.
	BottleneckNodePressure BottleneckCategory = "NODE_PRESSURE"
	// BottleneckQuotaExhausted: pods fail to create against a resource quota.
	BottleneckQuotaExhausted BottleneckCategory = "QUOTA_EXHAUSTED"
	// BottleneckSetupFailure: pods fail during workspace setup.
	BottleneckSetupFailure BottleneckCategory = "SETUP_FAILURE"
	// BottleneckCRDSyncLag: the pool CRD has not converged.
	BottleneckCRDSyncLag BottleneckCategory = "CRD_SYNC_LAG"
)

// PoolSignals are the §25.6 warm-pool bottleneck signals: the warm-up
// failure counts by cause, the replenishment-versus-claim rates, the
// setup-command failure count, and the CRD-sync-lag flag.
// spec: §25.6 lines 2862–2865.
type PoolSignals struct {
	ImagePullFailures     int
	NodePressureFailures  int
	QuotaExceededFailures int
	// SetupFailures is the §25.6 count of warm-up failures that exited
	// non-zero during the workspace setup phase
	// (lenny_warmpool_warmup_failure_total{error_type="setup_command_failed"}).
	SetupFailures int
	// ReplenishmentRate and ClaimRate are pods per minute.
	ReplenishmentRate float64
	ClaimRate         float64
	// CRDSyncLag indicates that the pool CRD has not converged to the
	// desired state — derived from PoolRecord.CRDSynced.
	CRDSyncLag bool
}

// ClassifyPoolBottleneck determines the §25.6 warm-pool bottleneck
// category from the pool's metric signals. A specific warm-up failure
// — image pull, node pressure, resource quota, or setup-command —
// takes precedence over the rate comparison, since a failing warm-up
// is a more actionable diagnosis than the resulting supply shortfall.
// CRD sync lag is a control-plane bottleneck that out-ranks the rate
// comparison: a pool whose CRD has not converged cannot replenish at
// the desired rate regardless of node or image health. When no warm-up
// is failing and the CRD is converged, demand outpacing replenishment
// is the bottleneck. ok is false when the pool shows no bottleneck.
// spec: §25.6 lines 2862–2865.
func ClassifyPoolBottleneck(s PoolSignals) (BottleneckCategory, bool) {
	switch {
	case s.ImagePullFailures > 0:
		return BottleneckImagePull, true
	case s.NodePressureFailures > 0:
		return BottleneckNodePressure, true
	case s.QuotaExceededFailures > 0:
		return BottleneckQuotaExhausted, true
	case s.SetupFailures > 0:
		return BottleneckSetupFailure, true
	case s.CRDSyncLag:
		return BottleneckCRDSyncLag, true
	case s.ReplenishmentRate < s.ClaimRate:
		return BottleneckDemandExceedsSupply, true
	default:
		return "", false
	}
}
