// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
	wantDiff := fs.String("diff", "", "report coverage on lines changed since this git ref (e.g. main, HEAD~5)")
	threshold := fs.Float64("threshold", 0.0, "fail when coverage is below this fraction (0..1)")
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*wantSpec && !*wantGo && *wantDiff == "" {
		fmt.Fprintln(os.Stderr, "coverage: specify --spec, --go, or --diff <ref>")
		return 2
	}
	if *wantDiff != "" {
		return reportDiffCoverage(*wantDiff, *threshold, *jsonOut)
	}
	if *wantSpec {
		return reportSpecCoverage(*threshold, *jsonOut)
	}
	if *wantGo {
		return reportGoCoverage(*threshold, *jsonOut)
	}
	return 0
}

// reportDiffCoverage measures coverage on the lines changed between
// <ref> and HEAD. Walks `git diff <ref>..HEAD --unified=0 -- *.go`
// to identify changed line ranges per file, then cross-references
// tests/results/cover.out for those lines. Applies --threshold to
// the resulting overall rate.
func reportDiffCoverage(ref string, threshold float64, jsonOut bool) int {
	root := repoRoot()
	profile := os.Getenv("LENNY_COVER_PROFILE")
	if profile == "" {
		profile = filepath.Join(root, coverProfileFile)
	}
	if _, err := os.Stat(profile); err != nil {
		fmt.Fprintf(os.Stderr, "coverage --diff: %v\nRun `lenny-test --tier unit` first to emit %s.\n", err, profile)
		return 1
	}

	changedLines, err := changedLineRanges(root, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage --diff: %v\n", err)
		return 1
	}
	if len(changedLines) == 0 {
		fmt.Println("coverage --diff: no .go files changed since", ref)
		return 0
	}

	body, err := os.ReadFile(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage --diff: read profile: %v\n", err)
		return 1
	}
	prof := parseCoverProfileRanges(string(body))

	type fileCov struct {
		File    string `json:"file"`
		Total   int    `json:"total"`
		Covered int    `json:"covered"`
	}
	files := []fileCov{}
	totalChanged := 0
	totalCovered := 0
	for file, ranges := range changedLines {
		fc := fileCov{File: file}
		fc.Total, fc.Covered = countChanged(prof[file], ranges)
		totalChanged += fc.Total
		totalCovered += fc.Covered
		files = append(files, fc)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].File < files[j].File })

	rate := 0.0
	if totalChanged > 0 {
		rate = float64(totalCovered) / float64(totalChanged)
	}

	if jsonOut {
		out, _ := json.MarshalIndent(map[string]any{
			"ref":           ref,
			"profile":       profile,
			"files":         files,
			"total_changed": totalChanged,
			"total_covered": totalCovered,
			"rate":          rate,
			"threshold":     threshold,
		}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Printf("coverage --diff (against %s):\n", ref)
		fmt.Printf("  total: %d / %d changed lines covered (%.1f%%)\n",
			totalCovered, totalChanged, rate*100)
		fmt.Println()
		fmt.Printf("%-50s  %8s  %s\n", "File", "Cov", "Lines")
		for _, fc := range files {
			// Files with no coverable changed lines (test files, and
			// production files whose diff touched only comments, imports,
			// or declarations) contribute nothing to the rate; omit them
			// from the per-file table so the listing stays readable.
			if fc.Total == 0 {
				continue
			}
			pct := float64(fc.Covered) / float64(fc.Total) * 100
			fmt.Printf("%-50s  %7.1f%%  %d/%d\n", fc.File, pct, fc.Covered, fc.Total)
		}
	}
	if threshold > 0 && rate < threshold {
		fmt.Fprintf(os.Stderr, "\ncoverage --diff: rate %.1f%% below threshold %.1f%%\n",
			rate*100, threshold*100)
		return 1
	}
	return 0
}

type lineRange struct{ start, end int }

// countChanged returns the coverable and covered line counts for a
// single file's changed ranges. A changed line counts against the floor
// only when the Go coverage model can instrument it. Comments, blank
// lines, imports, package/type/const/var declarations, and multi-line
// signatures never appear in a `go test -coverprofile` document, so they
// are uncoverable rather than uncovered. Counting them in the
// denominator understates coverage of changed code by an amount that
// scales with how declaration-heavy the file is. A line is coverable
// when it falls inside a statement block the profile reports for this
// file; a file with no statement blocks (all declarations) yields 0/0
// and cannot lower the rate. fp may be nil for such a file.
func countChanged(fp *fileProfile, ranges []lineRange) (total, covered int) {
	if fp == nil {
		return 0, 0
	}
	for _, r := range ranges {
		for line := r.start; line <= r.end; line++ {
			if !fp.coverable[line] {
				continue
			}
			total++
			if fp.covered[line] {
				covered++
			}
		}
	}
	return total, covered
}

// changedLineRanges parses `git diff -U0 <ref>..HEAD -- *.go` and
// returns a map of file → []lineRange (added/modified ranges on
// the new side).
func changedLineRanges(root, ref string) (map[string][]lineRange, error) {
	cmd := exec.Command("git", "diff", "-U0", ref+"..HEAD", "--", "*.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s..HEAD: %w", ref, err)
	}
	result := map[string][]lineRange{}
	current := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			current = strings.TrimPrefix(line, "+++ b/")
			if current == "/dev/null" {
				current = ""
			}
		case strings.HasPrefix(line, "@@") && current != "":
			idx := strings.Index(line, "+")
			if idx < 0 {
				continue
			}
			rest := line[idx+1:]
			end := strings.IndexAny(rest, " ")
			if end < 0 {
				continue
			}
			span := rest[:end]
			parts := strings.SplitN(span, ",", 2)
			start, _ := strconv.Atoi(parts[0])
			count := 1
			if len(parts) == 2 {
				count, _ = strconv.Atoi(parts[1])
			}
			if count == 0 {
				continue
			}
			result[current] = append(result[current], lineRange{start: start, end: start + count - 1})
		}
	}
	return result, nil
}

// fileProfile records, per file, which lines the Go coverage model can
// instrument (coverable: any statement block) and which the profile
// reports as executed at least once (covered: a block with a non-zero
// hit count). Diff coverage measures covered/coverable over changed
// lines so that uncoverable lines (comments, imports, declarations) do
// not enter the denominator.
type fileProfile struct {
	coverable map[int]bool
	covered   map[int]bool
}

// parseCoverProfileRanges parses cover.out into file → fileProfile.
// Every statement block contributes its lines to coverable; blocks with
// a non-zero hit count also contribute to covered. A merged profile (one
// `mode:` header, blocks from several test runs concatenated) is handled
// because a line stays covered once any block over it records a hit.
func parseCoverProfileRanges(body string) map[string]*fileProfile {
	out := map[string]*fileProfile{}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "mode:") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		span := fields[0]
		colonIdx := strings.LastIndex(span, ":")
		if colonIdx < 0 {
			continue
		}
		path := stripModulePrefix(span[:colonIdx])
		rest := span[colonIdx+1:]
		comma := strings.Index(rest, ",")
		if comma < 0 {
			continue
		}
		startLine, _ := strconv.Atoi(strings.SplitN(rest[:comma], ".", 2)[0])
		endLine, _ := strconv.Atoi(strings.SplitN(rest[comma+1:], ".", 2)[0])
		var hits int
		fmt.Sscanf(fields[2], "%d", &hits)
		fp := out[path]
		if fp == nil {
			fp = &fileProfile{coverable: map[int]bool{}, covered: map[int]bool{}}
			out[path] = fp
		}
		for ln := startLine; ln <= endLine; ln++ {
			fp.coverable[ln] = true
			if hits > 0 {
				fp.covered[ln] = true
			}
		}
	}
	return out
}

func stripModulePrefix(p string) string {
	return strings.TrimPrefix(p, "github.com/lennylabs/lenny/")
}

// reportSpecCoverage walks tests/spec-map.json and reports how many
// leaf sections have at least one test attached.
//
// A leaf section is one whose key contains a dot (e.g. "4.6.1"). The
// non-leaf parents ("4", "4.6") are reported for context but don't
// count toward the threshold.
func reportSpecCoverage(threshold float64, jsonOut bool) int {
	root := repoRoot()
	specMapPath := filepath.Join(root, specMapFile)
	body, err := os.ReadFile(specMapPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage --spec: %v\n", err)
		return 1
	}
	var doc struct {
		Sections map[string]struct {
			Title             string   `json:"title"`
			SpecFile          string   `json:"spec_file"`
			Tests             []string `json:"tests"`
			Packages          []string `json:"packages"`
			BlockedUntilPhase string   `json:"blocked_until_phase"`
			Notes             string   `json:"notes"`
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
			"leaf_sections": totalLeaves,
			"with_tests":    withTests,
			"coverage_rate": rate,
			"threshold":     threshold,
			"uncovered":     uncovered,
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
		profile = filepath.Join(root, coverProfileFile)
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
