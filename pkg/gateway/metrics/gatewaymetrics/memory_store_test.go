// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// TestMemoryStoreMetricsExposed_spec_9_4_F_9_4_1 pins the §9.4 / §16.1
// lines 151-154 contract: all four metric families register, accept
// the documented label sets, and surface on /metrics. The label
// shapes here are the ones the catalog test in
// pkg/observability/metrics enforces.
func TestMemoryStoreMetricsExposed_spec_9_4_F_9_4_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveMemoryStoreOperation("write", "memory", 0.001)
	m.ObserveMemoryStoreOperation("delete_by_user", "postgres", 12.3)
	m.IncMemoryStoreError("query", "memory", "internal")
	m.SetMemoryStoreRecordCount("acme", 42)
	m.IncMemoryStoreUserOverThreshold("acme", "postgres")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		// duration histogram bucket
		`lenny_memory_store_operation_duration_seconds_bucket{backend="memory",operation="write"`,
		`lenny_memory_store_operation_duration_seconds_bucket{backend="postgres",operation="delete_by_user"`,
		// error counter
		`lenny_memory_store_errors_total{backend="memory",error_type="internal",operation="query"} 1`,
		// record-count gauge
		`lenny_memory_store_record_count{tenant_id="acme"} 42`,
		// over-threshold counter
		`lenny_memory_store_user_count_over_threshold_total{backend="postgres",tenant_id="acme"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// TestMemoryStoreEmittersNilSafe_spec_9_4_F_9_4_1 covers a nil
// *Metrics receiver. Production code paths that fire before
// gatewaymetrics.New() returns must not panic; the emitters
// short-circuit when the receiver is nil.
func TestMemoryStoreEmittersNilSafe_spec_9_4_F_9_4_1(t *testing.T) {
	var m *gatewaymetrics.Metrics
	// All four are no-ops when m is nil.
	m.ObserveMemoryStoreOperation("write", "memory", 1)
	m.IncMemoryStoreError("write", "memory", "internal")
	m.SetMemoryStoreRecordCount("acme", 1)
	m.IncMemoryStoreUserOverThreshold("acme", "memory")
}
