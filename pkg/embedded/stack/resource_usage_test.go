// SPDX-License-Identifier: MIT

package stack

import (
	"os"
	"testing"
)

// TestParsePSOutput covers the three-column `ps -o pid=,pcpu=,rss=`
// shape: each line is "PID CPU% RSS-kB", reported as a sampled
// ResourceUsage with RSS converted to bytes.
//
// spec: §24.19 line 262.
func TestParsePSOutput_spec_24_19_262(t *testing.T) {
	raw := "  1234  12.5  87432\n  5678   0.0   1024\n\nbad row\n   9999 not a number 4\n"
	samples := parsePSOutput(raw)
	if got := samples[1234]; !got.Sampled || got.CPUPercent != 12.5 || got.RSSBytes != 87432*1024 {
		t.Errorf("PID 1234 sample = %+v", got)
	}
	if got := samples[5678]; !got.Sampled || got.CPUPercent != 0.0 || got.RSSBytes != 1024*1024 {
		t.Errorf("PID 5678 sample = %+v", got)
	}
	if _, ok := samples[9999]; ok {
		t.Errorf("PID 9999 with non-numeric column should be skipped, got entry")
	}
}

// TestSampleResourceUsageIgnoresDeadPIDs guards the §24.19 status
// pipeline against calling ps with zero or dead PIDs.
//
// spec: §24.19 line 262.
func TestSampleResourceUsageIgnoresDeadPIDs_spec_24_19_262(t *testing.T) {
	// Override the host probe to record the PIDs it received.
	var got []int
	orig := resourceSampler
	resourceSampler = func(pids []int) map[int]ResourceUsage {
		got = append([]int{}, pids...)
		return map[int]ResourceUsage{}
	}
	t.Cleanup(func() { resourceSampler = orig })
	_ = sampleResourceUsage([]int{0, -1, 1 << 30})
	for _, p := range got {
		if p <= 0 {
			t.Errorf("non-positive PID %d reached sampler", p)
		}
	}
	// A non-existent PID (1<<30) is also filtered by processAlive on
	// Unix; the sampler should either receive an empty list or skip
	// such entries.
	for _, p := range got {
		if p == 1<<30 {
			t.Errorf("dead PID 1<<30 reached sampler")
		}
	}
}

// TestSampleResourceUsageMergesSamples ensures sampleResourceUsage
// returns one entry per requested PID and merges sampler results in.
//
// spec: §24.19 line 262.
func TestSampleResourceUsageMergesSamples_spec_24_19_262(t *testing.T) {
	self := selfPID(t)
	orig := resourceSampler
	resourceSampler = func(pids []int) map[int]ResourceUsage {
		return map[int]ResourceUsage{self: {Sampled: true, CPUPercent: 7.0, RSSBytes: 4096}}
	}
	t.Cleanup(func() { resourceSampler = orig })
	got := sampleResourceUsage([]int{self, 0})
	if !got[self].Sampled || got[self].CPUPercent != 7.0 || got[self].RSSBytes != 4096 {
		t.Errorf("self sample = %+v", got[self])
	}
	if got[0].Sampled {
		t.Errorf("PID 0 should not be sampled, got %+v", got[0])
	}
}

// selfPID returns the current process's PID.
func selfPID(t *testing.T) int {
	t.Helper()
	return os.Getpid()
}
