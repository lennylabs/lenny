// SPDX-License-Identifier: MIT

package sessionserver

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: §16.1 — NewUploadMetrics registers the catalogued upload-handler
// metric names and AddUploadBytes / SetUploadQueueDepth move them.
// F-13.4.12.
func TestPromUploadMetrics_EmitsCatalogedNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewUploadMetrics(reg)
	if err != nil {
		t.Fatalf("NewUploadMetrics: %v", err)
	}

	// Both series materialize at construction so /metrics emits them
	// before the first upload.
	if got := testutil.ToFloat64(m.bytesTotal); got != 0 {
		t.Fatalf("lenny_upload_bytes_total at construction = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.queueDepth); got != 0 {
		t.Fatalf("lenny_upload_queue_depth at construction = %v, want 0", got)
	}

	m.AddUploadBytes(100)
	m.AddUploadBytes(23)
	if got := testutil.ToFloat64(m.bytesTotal); got != 123 {
		t.Fatalf("lenny_upload_bytes_total = %v, want 123", got)
	}
	// Non-positive byte counts are dropped.
	m.AddUploadBytes(0)
	m.AddUploadBytes(-5)
	if got := testutil.ToFloat64(m.bytesTotal); got != 123 {
		t.Fatalf("lenny_upload_bytes_total after no-op adds = %v, want 123", got)
	}

	m.SetUploadQueueDepth(7)
	if got := testutil.ToFloat64(m.queueDepth); got != 7 {
		t.Fatalf("lenny_upload_queue_depth = %v, want 7", got)
	}
	// A negative depth clamps to zero rather than emitting a nonsense gauge.
	m.SetUploadQueueDepth(-1)
	if got := testutil.ToFloat64(m.queueDepth); got != 0 {
		t.Fatalf("lenny_upload_queue_depth after negative set = %v, want 0", got)
	}

	// The registered names match the §16.1 catalog entries.
	names := registeredNames(t, reg)
	for _, want := range []string{"lenny_upload_bytes_total", "lenny_upload_queue_depth"} {
		if !names[want] {
			t.Errorf("metric %q not registered", want)
		}
	}
}

// Nil-receiver calls are safe (the minimal gateway leaves the emitter
// unset). F-13.4.12.
func TestPromUploadMetrics_NilReceiverSafe(t *testing.T) {
	var m *PromUploadMetrics
	m.AddUploadBytes(10)
	m.SetUploadQueueDepth(3)
}

func registeredNames(t *testing.T, reg *prometheus.Registry) map[string]bool {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := map[string]bool{}
	for _, mf := range mfs {
		if strings.HasPrefix(mf.GetName(), "lenny_upload_") {
			out[mf.GetName()] = true
		}
	}
	return out
}
