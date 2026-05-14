// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// runMutation invokes scripts/mutation.sh against a package pattern
// and compares the result against the §19.3 per-package threshold
// table. Per-package thresholds default to 0.70 (70% kill rate);
// the documented critical-package list (pkg/audit, pkg/circuitbreaker,
// pkg/idempotency, pkg/quota) demands 0.85.
//
// Usage:
//
//	lenny-test mutation                       # runs pkg/...
//	lenny-test mutation --pkg pkg/quota       # one package
//	lenny-test mutation --threshold 0.80
func runMutation(args []string) int {
	fs := flag.NewFlagSet("mutation", flag.ExitOnError)
	pkg := fs.String("pkg", "pkg/...", "package pattern to mutate")
	threshold := fs.Float64("threshold", 0.0, "minimum kill rate (0.0 = use per-package defaults)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *threshold <= 0 {
		*threshold = defaultMutationThreshold(*pkg)
	}

	script := filepath.Join(repoRoot(), "scripts", "mutation.sh")
	cmd := exec.Command("bash", script, *pkg)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return 1
	}
	score := extractMutationScore(string(out))
	if score < 0 {
		fmt.Println(string(out))
		fmt.Fprintln(os.Stderr, "mutation: no score reported (scaffold mode); skipping threshold check")
		return 0
	}
	fmt.Print(string(out))
	if score < *threshold {
		fmt.Fprintf(os.Stderr, "mutation: kill rate %.2f below threshold %.2f for %s\n", score, *threshold, *pkg)
		return 1
	}
	fmt.Printf("mutation: kill rate %.2f ≥ threshold %.2f for %s\n", score, *threshold, *pkg)
	return 0
}

// defaultMutationThreshold returns the §19.3 per-package default.
// Critical packages have a higher bar.
func defaultMutationThreshold(pattern string) float64 {
	critical := []string{
		"pkg/audit",
		"pkg/circuitbreaker",
		"pkg/idempotency",
		"pkg/quota",
		"pkg/tokenexchange",
		"pkg/auth/jwt",
	}
	for _, c := range critical {
		if strings.Contains(pattern, c) {
			return 0.85
		}
	}
	return 0.70
}

// extractMutationScore reads "mutation: kill rate <float>" from the
// script's stdout. Returns -1 when no score line is present (which
// happens when go-mutesting is not installed and the script is in
// scaffold-only mode).
func extractMutationScore(out string) float64 {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "mutation: kill rate ") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				if v, err := strconv.ParseFloat(fields[3], 64); err == nil {
					return v
				}
			}
		}
	}
	return -1
}
