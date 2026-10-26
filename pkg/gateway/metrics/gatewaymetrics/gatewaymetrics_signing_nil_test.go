// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// TestSigningMetricsToleratesNilReceiver pins that the §16.1 sign-request
// counter and the §16.5 sign-quota gauge setters are safe on a nil
// *Metrics, which the gateway's breaker observer holds until
// gatewaymetrics.New returns, so a sign request before the metrics are
// wired does not panic the mint path.
//
// spec: 16.1 (Metrics), 16.5 (Alerting Rules and SLOs), 10.2 (Authentication)
func TestSigningMetricsToleratesNilReceiver(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.RecordJWTSignRequest("ok")
	m.SetKMSSignQuotaPerSecond(100)
}
