// SPDX-License-Identifier: MIT

package cidrdrift

import (
	"context"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// DriftCount returns the current value of the
// lenny_network_policy_cidr_drift_total counter for the given policy
// name and the pod_cidr field. It is exported to the package's
// external test so a test can assert the detector incremented the
// metric. The counter is process-global, so tests read a delta around
// a scan rather than an absolute value.
func DriftCount(policy string) float64 {
	return testutil.ToFloat64(driftTotal.WithLabelValues(policy, fieldPodCIDR))
}

// ScanForTest runs one drift-detection pass. It is exported to the
// package's external test so a test can drive a single deterministic
// scan without starting the timer loop.
func (d *Detector) ScanForTest(ctx context.Context) error {
	return d.scan(ctx)
}
