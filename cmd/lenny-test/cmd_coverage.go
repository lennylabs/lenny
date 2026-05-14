// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// runCoverage dispatches the coverage subcommands.
//
// Usage:
//
//	lenny-test coverage --spec      # spec-section coverage
//	lenny-test coverage --go        # Go package coverage (reads cover.out)
func runCoverage(args []string) int {
	fs := flag.NewFlagSet("coverage", flag.ExitOnError)
	wantSpec := fs.Bool("spec", false, "report spec-section coverage from spec-map.json")
	wantGo := fs.Bool("go", false, "report Go coverage from tests/results/cover.out")
	threshold := fs.Float64("threshold", 0.0, "fail when coverage is below this fraction (0..1)")
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*wantSpec && !*wantGo {
		fmt.Fprintln(os.Stderr, "coverage: specify --spec or --go")
		return 2
	}
	if *wantSpec {
		return reportSpecCoverage(*threshold, *jsonOut)
	}
	if *wantGo {
		return reportGoCoverage(*threshold, *jsonOut)
	}
	return 0
}

// reportSpecCoverage walks tests/spec-map.json and reports how many
// leaf sections have at least one test attached.
//
// A leaf section is one whose key contains a dot (e.g. "4.6.1"). The
// non-leaf parents ("4", "4.6") are reported for context but don't
// count toward the threshold.
func reportSpecCoverage(threshold float64, jsonOut bool) int {
	root := repoRoot()
	specMapPath := filepath.Join(root, "tests", "spec-map.json")
	body, err := os.ReadFile(specMapPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage --spec: %v\n", err)
		return 1
	}
	var doc struct {
		Sections map[string]struct {
			Title            string   `json:"title"`
			SpecFile         string   `json:"spec_file"`
			Tests            []string `json:"tests"`
			Packages         []string `json:"packages"`
			BlockedUntilPhase string  `json:"blocked_until_phase"`
			Notes            string   `json:"notes"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "coverage --spec: parse: %v\n", err)
		return 1
	}

	totalLeaves := 0
	withTests := 0
	uncovered := []specRow{}

	for id, sec := range doc.Sections {
		// Non-leaves are the top-level chapters (single token).
		if !strings.Contains(id, ".") {
			continue
		}
		// Skip sections explicitly marked as non-normative.
		if strings.Contains(strings.ToLower(sec.Notes), "non-normative") {
			continue
		}
		totalLeaves++
		if len(sec.Tests) > 0 {
			withTests++
			continue
		}
		uncovered = append(uncovered, specRow{
			ID:    id,
			Title: sec.Title,
			Phase: sec.BlockedUntilPhase,
		})
	}
	rate := 0.0
	if totalLeaves > 0 {
		rate = float64(withTests) / float64(totalLeaves)
	}

	sort.Slice(uncovered, func(i, j int) bool { return uncovered[i].ID < uncovered[j].ID })

	if jsonOut {
		out, _ := json.MarshalIndent(map[string]any{
			"leaf_sections":  totalLeaves,
			"with_tests":     withTests,
			"coverage_rate":  rate,
			"threshold":      threshold,
			"uncovered":      uncovered,
		}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Printf("spec coverage: %d / %d leaf sections have at least one test (%.1f%%)\n",
			withTests, totalLeaves, rate*100)
		if threshold > 0 {
			fmt.Printf("threshold: %.1f%%\n", threshold*100)
		}
		if len(uncovered) > 0 {
			fmt.Printf("\nUncovered (top 20):\n")
			max := len(uncovered)
			if max > 20 {
				max = 20
			}
			for _, u := range uncovered[:max] {
				phase := ""
				if u.Phase != "" {
					phase = fmt.Sprintf("  [phase=%s]", u.Phase)
				}
				fmt.Printf("  §%-8s %s%s\n", u.ID, u.Title, phase)
			}
			if len(uncovered) > 20 {
				fmt.Printf("  ... (%d more)\n", len(uncovered)-20)
			}
		}
	}

	if threshold > 0 && rate < threshold {
		fmt.Fprintf(os.Stderr, "\ncoverage --spec: rate %.1f%% below threshold %.1f%%\n",
			rate*100, threshold*100)
		return 1
	}
	return 0
}

// reportGoCoverage parses tests/results/cover.out (or the path
// supplied via LENNY_COVER_PROFILE) and reports per-package
// coverage.
func reportGoCoverage(threshold float64, jsonOut bool) int {
	root := repoRoot()
	profile := os.Getenv("LENNY_COVER_PROFILE")
	if profile == "" {
		profile = filepath.Join(root, "tests", "results", "cover.out")
	}
	body, err := os.ReadFile(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage --go: read %s: %v\n", profile, err)
		fmt.Fprintf(os.Stderr, "Run `lenny-test --tier unit --output json --coverprofile %s` first.\n", profile)
		return 1
	}
	pkgs := parseCoverProfile(string(body))
	rows := []pkgCovRow{}
	totalStmts := 0
	covered := 0
	for name, c := range pkgs {
		rows = append(rows, pkgCovRow{Package: name, Statements: c.total, Covered: c.covered})
		totalStmts += c.total
		covered += c.covered
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Package < rows[j].Package })
	rate := 0.0
	if totalStmts > 0 {
		rate = float64(covered) / float64(totalStmts)
	}
	if jsonOut {
		out, _ := json.MarshalIndent(map[string]any{
			"profile":    profile,
			"packages":   rows,
			"statements": totalStmts,
			"covered":    covered,
			"rate":       rate,
			"threshold":  threshold,
		}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Printf("Go coverage from %s\n", profile)
		fmt.Printf("Total: %d / %d statements (%.1f%%)\n", covered, totalStmts, rate*100)
		fmt.Println()
		fmt.Printf("%-50s  %8s  %s\n", "Package", "Cov", "Stmts")
		for _, r := range rows {
			pct := 0.0
			if r.Statements > 0 {
				pct = float64(r.Covered) / float64(r.Statements) * 100
			}
			fmt.Printf("%-50s  %7.1f%%  %d/%d\n", r.Package, pct, r.Covered, r.Statements)
		}
	}
	if threshold > 0 && rate < threshold {
		fmt.Fprintf(os.Stderr, "\ncoverage --go: rate %.1f%% below threshold %.1f%%\n",
			rate*100, threshold*100)
		return 1
	}
	return 0
}

type specRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Phase string `json:"phase,omitempty"`
}

type pkgCovRow struct {
	Package    string `json:"package"`
	Statements int    `json:"statements"`
	Covered    int    `json:"covered"`
}

type pkgCov struct {
	total   int
	covered int
}

// parseCoverProfile reads a `go test -coverprofile` document and
// rolls up per-package statement totals + counts.
func parseCoverProfile(body string) map[string]pkgCov {
	out := map[string]pkgCov{}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "mode:") || strings.TrimSpace(line) == "" {
			continue
		}
		// path/to/file.go:start,end col_start.col_end stmts count
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pkgPath := pathToPackage(fields[0])
		// fields[1] is the number of statements; fields[2] is the
		// hit count.
		var stmts, hits int
		fmt.Sscanf(fields[1], "%d", &stmts)
		fmt.Sscanf(fields[2], "%d", &hits)
		c := out[pkgPath]
		c.total += stmts
		if hits > 0 {
			c.covered += stmts
		}
		out[pkgPath] = c
	}
	return out
}

func pathToPackage(p string) string {
	// Strip the file:start,end suffix to leave the directory.
	if i := strings.Index(p, ":"); i >= 0 {
		p = p[:i]
	}
	return filepath.Dir(p)
}
