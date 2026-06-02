// SPDX-License-Identifier: MIT

package backup

import (
	"testing"
	"time"
)

// spec: §25.11 line 3957 — estimatedDowntime is scaled by backup size
// and component count rather than a fixed constant.
func TestEstimateDowntimeScalesWithSizeAndComponents(t *testing.T) {
	// A full backup has three components: base 2m + 3×1m = PT5M with no
	// recorded size.
	full := Backup{Components: componentsFor(TypeFull)}
	if got := estimateDowntime(full, 0); got != "PT5M" {
		t.Errorf("full backup downtime = %q, want PT5M", got)
	}
	// A postgres-only backup has one component: base 2m + 1×1m = PT3M.
	pg := Backup{Components: componentsFor(TypePostgres)}
	if got := estimateDowntime(pg, 0); got != "PT3M" {
		t.Errorf("postgres backup downtime = %q, want PT3M", got)
	}
	// Size adds linearly: 100 MiB at the default 50 MiB/s adds 2s.
	sized := Backup{Components: componentsFor(TypePostgres), SizeBytes: 100 << 20}
	if got := estimateDowntime(sized, 0); got != "PT3M2S" {
		t.Errorf("sized backup downtime = %q, want PT3M2S", got)
	}
	// A larger backup yields a longer estimate than a smaller one.
	if estimateDowntime(sized, 0) == estimateDowntime(pg, 0) {
		t.Error("downtime did not change with backup size")
	}
}

// spec: §25.11 line 3957 — a custom throughput overrides the default.
func TestEstimateDowntimeHonorsThroughput(t *testing.T) {
	b := Backup{Components: componentsFor(TypePostgres), SizeBytes: 600 << 20}
	// At 10 MiB/s, 600 MiB takes 60s; base 3m + 60s = PT4M.
	if got := estimateDowntime(b, 10<<20); got != "PT4M" {
		t.Errorf("downtime = %q, want PT4M", got)
	}
}

func TestISO8601Duration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "PT0S"},
		{-time.Second, "PT0S"},
		{45 * time.Second, "PT45S"},
		{2 * time.Minute, "PT2M"},
		{90 * time.Second, "PT1M30S"},
		{time.Hour + 5*time.Minute, "PT1H5M"},
		{time.Hour + 5*time.Minute + 30*time.Second, "PT1H5M30S"},
	}
	for _, tc := range cases {
		if got := iso8601Duration(tc.d); got != tc.want {
			t.Errorf("iso8601Duration(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
