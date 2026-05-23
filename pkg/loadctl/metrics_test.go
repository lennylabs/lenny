// SPDX-License-Identifier: MIT

package loadctl

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMetricsEndpointSurfacesCounters(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Create a run so the run counter increments.
	resp, _ := http.Post(srv.URL+"/api/v1/runs", "application/json",
		bytes.NewBufferString(`{"scale":"small"}`))
	resp.Body.Close()

	// Wait for scaffold to terminate (~4s).
	time.Sleep(5 * time.Second)

	resp, _ = http.Get(srv.URL + "/metrics")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	text := string(body)

	// The metrics surface should expose the headline counters.
	for _, expect := range []string{
		"lenny_loadctl_requests_total",
		"lenny_loadctl_runs_created_total",
		"lenny_loadctl_runs_terminal_total",
		"lenny_loadctl_progress_events_total",
		"lenny_loadctl_request_duration_seconds",
	} {
		if !strings.Contains(text, expect) {
			t.Errorf("metrics output missing %q", expect)
		}
	}
	// Run was created => the counter should have a non-zero observation.
	if !strings.Contains(text, "lenny_loadctl_runs_created_total 1") {
		t.Errorf("runs_created_total != 1 in metrics output (got: %s)", text[strings.Index(text, "lenny_loadctl_runs_created_total"):strings.Index(text, "lenny_loadctl_runs_created_total")+80])
	}
}
