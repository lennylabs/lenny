// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaffinity"
)

// TestMetricsSatisfiesStatelessSink asserts the §5.2 line 573
// demand-signal seam: *gatewaymetrics.Metrics is a usable
// tenantaffinity.StatelessMetrics, so the tenant-affinity router emits
// lenny_stateless_requests_total / lenny_stateless_concurrent_active
// straight into the gateway's registered collectors when the cluster
// wiring injects it. spec: §5.2 line 573.
func TestMetricsSatisfiesStatelessSink(t *testing.T) {
	var _ tenantaffinity.StatelessMetrics = (*gatewaymetrics.Metrics)(nil)
}
