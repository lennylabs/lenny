// SPDX-License-Identifier: MIT

package warmpool

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: spec/16 §16.1 (lenny_controller_pod_retirement_total, controller-owned
// uptime_limit retirements). IncControllerUptimeRetirement must increment the
// counter under reason="uptime_limit" for the (pool, runtime_class) tuple so
// the summing recording rule sees the controller's level-triggered
// maxPodUptimeSeconds retirements.
func TestIncControllerUptimeRetirementCountsByLabels_spec_16_1(t *testing.T) {
	const (
		pool = "pool-uptime-count"
		rc   = "gvisor"
	)
	before := testutil.ToFloat64(controllerPodRetirement.WithLabelValues(reasonUptimeLimit, pool, rc))
	IncControllerUptimeRetirement(pool, rc)
	IncControllerUptimeRetirement(pool, rc)
	if got := testutil.ToFloat64(controllerPodRetirement.WithLabelValues(reasonUptimeLimit, pool, rc)); got != before+2 {
		t.Errorf("controllerPodRetirement{uptime_limit,%q,%q} = %v, want %v", pool, rc, got, before+2)
	}
}

// spec: spec/16 §16.1 — the controller counter carries only reason="uptime_limit".
// A distinct (pool, runtime_class) tuple must produce an independent series so a
// no-runtime-class pod (empty runtime_class) does not fold into a runtime-class
// series.
func TestIncControllerUptimeRetirementSeparatesRuntimeClass_spec_16_1(t *testing.T) {
	const pool = "pool-uptime-rc-split"
	IncControllerUptimeRetirement(pool, "gvisor")
	IncControllerUptimeRetirement(pool, "")
	if got := testutil.ToFloat64(controllerPodRetirement.WithLabelValues(reasonUptimeLimit, pool, "gvisor")); got != 1 {
		t.Errorf("gvisor series = %v, want 1", got)
	}
	if got := testutil.ToFloat64(controllerPodRetirement.WithLabelValues(reasonUptimeLimit, pool, "")); got != 1 {
		t.Errorf("empty-runtime-class series = %v, want 1", got)
	}
}
