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
// failure counts by cause and the replenishment-versus-claim rates,
// read from the warm-pool metrics.
type PoolSignals struct {
	ImagePullFailures     int
	NodePressureFailures  int
	QuotaExceededFailures int
	// ReplenishmentRate and ClaimRate are pods per minute.
	ReplenishmentRate float64
	ClaimRate         float64
}

// ClassifyPoolBottleneck determines the §25.6 warm-pool bottleneck
// category from the pool's metric signals. A specific warm-up failure
// — image pull, node pressure, or resource quota — takes precedence
// over the rate comparison, since a failing warm-up is a more
// actionable diagnosis than the resulting supply shortfall. When no
// warm-up is failing, demand outpacing replenishment is the
// bottleneck. ok is false when the pool shows no bottleneck.
func ClassifyPoolBottleneck(s PoolSignals) (BottleneckCategory, bool) {
	switch {
	case s.ImagePullFailures > 0:
		return BottleneckImagePull, true
	case s.NodePressureFailures > 0:
		return BottleneckNodePressure, true
	case s.QuotaExceededFailures > 0:
		return BottleneckQuotaExhausted, true
	case s.ReplenishmentRate < s.ClaimRate:
		return BottleneckDemandExceedsSupply, true
	default:
		return "", false
	}
}
