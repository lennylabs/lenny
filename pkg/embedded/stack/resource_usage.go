// SPDX-License-Identifier: MIT

package stack

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
)

// resourceSampler is overridden in tests to feed deterministic data
// into CollectStatus without depending on the host ps binary.
var resourceSampler = psResourceSample

// sampleResourceUsage queries the host `ps` for CPU% and RSS of each
// PID, returning a map keyed by PID. Unknown / dead PIDs and platforms
// where ps is unavailable produce zero-value ResourceUsage entries.
//
// spec: §24.19 line 262 "resource usage".
func sampleResourceUsage(pids []int) map[int]ResourceUsage {
	out := make(map[int]ResourceUsage, len(pids))
	live := make([]int, 0, len(pids))
	for _, p := range pids {
		out[p] = ResourceUsage{}
		if p > 0 && processAlive(p) {
			live = append(live, p)
		}
	}
	if len(live) == 0 {
		return out
	}
	samples := resourceSampler(live)
	for pid, sample := range samples {
		out[pid] = sample
	}
	return out
}

// psResourceSample shells out to `ps -o pid=,pcpu=,rss= -p <pid>,<pid>`.
// The format is portable across the macOS/BSD and Linux ps builds the
// embedded mode supports. RSS is reported in kilobytes; pcpu is a
// percentage of one CPU.
func psResourceSample(pids []int) map[int]ResourceUsage {
	if len(pids) == 0 {
		return nil
	}
	args := []string{"-o", "pid=,pcpu=,rss=", "-p", joinInts(pids, ",")}
	cmd := exec.Command("ps", args...)
	stdout, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parsePSOutput(string(stdout))
}

// parsePSOutput parses the three-column ps output into per-PID samples.
// Each non-blank line is "PID CPU% RSS-kB". Lines with fewer than three
// columns are skipped.
func parsePSOutput(raw string) map[int]ResourceUsage {
	out := map[int]ResourceUsage{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		cpu, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		rssKB, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}
		out[pid] = ResourceUsage{Sampled: true, CPUPercent: cpu, RSSBytes: rssKB * 1024}
	}
	return out
}

// joinInts renders a slice of ints joined by sep.
func joinInts(xs []int, sep string) string {
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, strconv.Itoa(x))
	}
	return strings.Join(parts, sep)
}
