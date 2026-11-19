// SPDX-License-Identifier: MIT

package sessionserver

// DelegationHighWatermarkObserver records the §8.3 per-tree
// parallel-children high-watermark onto the
// `lenny_delegation_parallel_children_high_watermark` histogram.
// *gatewaymetrics.Metrics implements it. Nil drops the observation.
// F-8.9.6.
type DelegationHighWatermarkObserver interface {
	ObserveDelegationParallelChildrenHighWatermark(pool, tenantID string, value int64)
}
