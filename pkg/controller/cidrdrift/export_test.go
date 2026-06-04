// SPDX-License-Identifier: MIT

package cidrdrift

import (
	"context"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// DriftCount returns the current value of the
// lenny_network_policy_cidr_drift_total counter for the given policy
// label and the pod_cidr field. It is exported to the package's
// external test so a test can assert the detector incremented the
// metric. The counter is process-global, so tests read a delta around
// a scan rather than an absolute value.
func DriftCount(policy string) float64 {
	return DriftCountField(policy, FieldPodCIDR)
}

// DriftCountField returns the counter value for an explicit (policy,
// field) label pair, so a test can assert the service_cidr field and the
// canonical policy labels (internet | gateway-llm-upstream | ops-egress)
// the detector stamps.
func DriftCountField(policy, field string) float64 {
	return testutil.ToFloat64(driftTotal.WithLabelValues(policy, field))
}

// ScanForTest runs one drift-detection pass. It is exported to the
// package's external test so a test can drive a single deterministic
// scan without starting the timer loop.
func (d *Detector) ScanForTest(ctx context.Context) error {
	return d.scan(ctx)
}
